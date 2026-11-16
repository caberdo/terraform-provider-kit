package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/caberdo/terraform-provider-kit/internal/scaffold"
)

// defaultLicense is the kit's licence default. rustfs is the documented
// exemption and passes --license Apache-2.0.
const (
	defaultLicense           = "MIT"
	defaultRegistryNamespace = "caberdo"
)

// Validators for the values that must be named rather than derived. A wrong
// value is a configuration error, not a guess the checker should paper over.
var (
	providerPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	yearPattern           = regexp.MustCompile(`^[0-9]{4}$`)
	kitVersionPattern     = regexp.MustCompile(`^(v[0-9]+\.[0-9]+\.[0-9]+|[0-9a-f]{40})$`)
	ownerNamePattern      = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	repoOwnerPattern      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	repoNamePattern       = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	licensePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]*$`)
	modulePattern         = regexp.MustCompile(`(?m)^module[ \t]+(\S+)[ \t]*$`)
	goPackageTokenPattern = regexp.MustCompile(`^(\.{1,2}/.*|\.)$`)
)

// runScaffold dispatches the scaffold subcommands.
func runScaffold(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "scaffold requires a subcommand\n\n")
		_, _ = io.WriteString(stderr, scaffoldUsage)
		return exitUsage
	}

	switch args[0] {
	case "check":
		return runScaffoldCheck(args[1:], stdout, stderr)
	case "fix":
		return runScaffoldFix(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown scaffold command %q\n\n", args[0])
		_, _ = io.WriteString(stderr, scaffoldUsage)
		return exitUsage
	}
}

// allowValues collects the repeatable --allow <path>:<reason> flag. The reason
// is required so a deviation is recorded, not silent.
type allowValues []scaffold.Allowance

// String renders the list for flag diagnostics.
func (v *allowValues) String() string {
	parts := make([]string, 0, len(*v))
	for _, entry := range *v {
		parts = append(parts, entry.Path+":"+entry.Reason)
	}
	return strings.Join(parts, ",")
}

// Set parses one <path>:<reason> pair. The same parser reads the per-repository
// allow-list file, so the two spellings cannot drift apart.
func (v *allowValues) Set(value string) error {
	entry, err := parseAllowance(value)
	if err != nil {
		return err
	}
	*v = append(*v, entry)
	return nil
}

// parseAllowance parses one `path: reason` entry. Everything after the first
// colon is the reason, so a reason may contain a colon.
func parseAllowance(value string) (scaffold.Allowance, error) {
	path, reason, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(path) == "" || strings.TrimSpace(reason) == "" {
		return scaffold.Allowance{}, fmt.Errorf("want <path>:<reason>, got %q", value)
	}
	return scaffold.Allowance{
		Path:   strings.TrimSpace(path),
		Reason: strings.TrimSpace(reason),
	}, nil
}

// scaffoldAllowFile is the durable per-repository exemption list. It is the
// record of a deliberate deviation; --allow stays available for one-off
// debugging.
const scaffoldAllowFile = ".github/scaffold-allow.txt"

// parseAllowFile parses the allow-list file. Blank lines and lines whose first
// non-space character is # are ignored. A malformed line is a configuration
// error, and the error names the file and line: a silently dropped exemption
// would let drift back in without a record.
func parseAllowFile(content string) ([]scaffold.Allowance, error) {
	// An editor may write a UTF-8 BOM; it is not part of the first path.
	content = strings.TrimPrefix(content, "\ufeff")
	var entries []scaffold.Allowance
	for i, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		entry, err := parseAllowance(trimmed)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", scaffoldAllowFile, i+1, err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// loadAllowFile reads the per-repository allow list. An absent file is normal:
// most repositories record no deviations.
func loadAllowFile(root string) ([]scaffold.Allowance, error) {
	full := filepath.Join(root, filepath.FromSlash(scaffoldAllowFile))
	content, err := os.ReadFile(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", scaffoldAllowFile, err)
	}
	return parseAllowFile(string(content))
}

// scaffoldCommand is the parsed flag set shared by check and fix.
type scaffoldCommand struct {
	input  scaffoldInput
	allow  allowValues
	dryRun bool
}

// parseScaffoldFlags parses the flags both subcommands accept. It returns a nil
// command when the command has already finished (help or usage), with the exit
// code to report.
func parseScaffoldFlags(command string, args []string, stderr io.Writer) (*scaffoldCommand, int) {
	name := "tfkit scaffold " + command
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)

	parsed := &scaffoldCommand{}
	flags.StringVar(&parsed.input.Provider, "provider", "",
		"provider name without the terraform-provider- prefix (default: the directory name)")
	flags.StringVar(&parsed.input.Product, "product", "",
		"product display name, such as Trino (required)")
	flags.StringVar(&parsed.input.Module, "module", "",
		"Go module path (default: the module line of go.mod)")
	flags.StringVar(&parsed.input.Year, "year", "",
		"copyright year in LICENSE (required)")
	flags.StringVar(&parsed.input.Copyright, "copyright", "",
		"copyright holder in LICENSE (required)")
	flags.StringVar(&parsed.input.KitVersion, "kit-version", "",
		"kit release tag or full commit SHA the workflow callers pin (required)")
	flags.StringVar(&parsed.input.Packages, "packages", "",
		"go list pattern the tests and coverage gate use (default: derived from GNUmakefile)")
	flags.StringVar(&parsed.input.VersionsFile, "versions-file", "",
		"supported-versions file (default: .github/<provider>-versions.txt)")
	flags.StringVar(&parsed.input.Upstream, "upstream", "",
		"upstream repository whose releases define the version window, as owner/name (required)")
	flags.StringVar(&parsed.input.Repo, "repo", "",
		"GitHub repository as owner/name (default: derived from `git remote get-url origin`)")
	flags.StringVar(&parsed.input.License, "license", defaultLicense,
		"SPDX licence identifier stated in the generated documents")
	flags.Var(&parsed.allow, "allow",
		"exempt a checked file, as <path>:<reason> (repeatable; the reason is required)")
	if command == "fix" {
		flags.BoolVar(&parsed.dryRun, "dry-run", false,
			"print the writes instead of performing them")
	}

	flags.Usage = func() {
		_, _ = io.WriteString(stderr, scaffoldCommandUsage(command))
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, exitOK
		}
		return nil, exitUsage
	}
	if flags.NArg() > 0 {
		flags.Usage()
		return nil, exitUsage
	}
	return parsed, -1
}

// scaffoldCommandUsage is the help text for one subcommand. The two commands
// share the flags; fix adds --dry-run and the repair paragraph.
func scaffoldCommandUsage(command string) string {
	var action, dryRunFlag, dryRunHelp string
	if command == "fix" {
		action = `Writes the rendered, enforced scaffold files over this repository's copies and
reports every path it wrote. It never writes README prose, provider-specific
files or a path the allow list exempts, and it re-runs the check afterwards so
the exit status is the check's. A path outside the enforced set is never
touched.`
		dryRunFlag = " [--dry-run]"
		dryRunHelp = "  --dry-run        print the writes without changing a file\n"
	} else {
		action = `Verifies that this repository still matches the canonical scaffold the kit owns:
the editor, ignore and lint configuration, the community files and the issue
templates. A differing file is named with its first differing line; a missing
file is named as missing. A placeholder without a value is a configuration
error. The check never writes to the repository.`
	}

	return fmt.Sprintf(`usage: tfkit scaffold %s [flags]%s

%s

Allowances are read from %s when it exists, one `+"`path: reason`"+` per line;
blank lines and # comments are ignored. --allow <path>:<reason> adds a one-off
exemption for debugging. A path the check does not enforce and an allowance
without a reason are configuration errors.

The provider, module, package pattern and repository are derived from the
repository when the matching flag is not given; the product-facing facts have no
reliable derivation and must be supplied.

Exits 0 when the repository matches, 1 on drift, and 2 for a usage or
configuration problem.

Flags:
  --provider       provider name without the terraform-provider- prefix
  --product        product display name (required)
  --module         Go module path (default: the module line of go.mod)
  --year           copyright year in LICENSE (required)
  --copyright      copyright holder in LICENSE (required)
  --kit-version    kit release tag or full commit SHA (required)
  --packages       go list pattern for tests and coverage
                   (default: the pattern in GNUmakefile)
  --versions-file  supported-versions file (default ".github/<provider>-versions.txt")
  --upstream       upstream repository as owner/name (required)
  --repo           GitHub repository as owner/name
                   (default: derived from "git remote get-url origin"; must be github.com)
  --license        SPDX licence identifier (default "MIT"; rustfs uses "Apache-2.0")
  --allow          exempt a checked file as <path>:<reason> (repeatable)
%s`, command, dryRunFlag, action, scaffoldAllowFile, dryRunHelp)
}

// runScaffoldCheck implements `tfkit scaffold check`. Exit codes: 0 when the
// repository matches, 1 on drift (with every differing or missing file named)
// and 2 for a usage or configuration problem.
func runScaffoldCheck(args []string, stdout, stderr io.Writer) int {
	return runScaffoldCommand("check", args, stdout, stderr)
}

// runScaffoldFix implements `tfkit scaffold fix`: repair the enforced files,
// print exactly what was written, then report the check's outcome. --dry-run
// prints the same list without writing.
func runScaffoldFix(args []string, stdout, stderr io.Writer) int {
	return runScaffoldCommand("fix", args, stdout, stderr)
}

// runScaffoldCommand is the shared body of check and fix. Rendering, comparison and
// writing belong to the scaffold package; this function owns argument parsing,
// the durable allow list and the report.
func runScaffoldCommand(command string, args []string, stdout, stderr io.Writer) int {
	parsed, code := parseScaffoldFlags(command, args, stderr)
	if parsed == nil {
		return code
	}

	values, err := scaffoldValues(".", parsed.input)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "scaffold-%s: %v\n", command, err)
		return exitUsage
	}

	fileAllow, err := loadAllowFile(".")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "scaffold-%s: %v\n", command, err)
		return exitUsage
	}
	allow := append(fileAllow, parsed.allow...)
	opts := scaffold.Options{Allow: allow}

	if command == "fix" {
		written, err := repairScaffold(parsed.dryRun, values, opts)
		verb := "wrote"
		if parsed.dryRun {
			verb = "would write"
		}
		for _, path := range written {
			_, _ = fmt.Fprintf(stdout, "scaffold-fix: %s %s\n", verb, path)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "scaffold-fix: %v\n", err)
			// An I/O refusal is a failure, not a usage or configuration
			// problem: the partial write list above is the evidence.
			if errors.Is(err, scaffold.ErrFixIO) {
				return exitFailure
			}
			return exitUsage
		}
	}

	report, err := scaffold.Check(".", values, opts)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "scaffold-%s: %v\n", command, err)
		return exitUsage
	}
	return reportScaffold(report, stdout, stderr)
}

// repairScaffold writes, or plans with dryRun, the enforced files that drift.
func repairScaffold(dryRun bool, values map[string]string, opts scaffold.Options) ([]string, error) {
	if dryRun {
		return scaffold.FixPlan(".", values, opts)
	}
	return scaffold.Fix(".", values, opts)
}

// reportScaffold writes the allowances and problems of a check and maps the
// report to the exit code.
func reportScaffold(report scaffold.Report, stdout, stderr io.Writer) int {
	for _, allowance := range report.Allowed {
		note := ""
		if !allowance.Used {
			note = "; unused"
		}
		_, _ = fmt.Fprintf(stdout, "scaffold-check: %s: allowed (%s%s)\n",
			allowance.Path, allowance.Reason, note)
	}
	if !report.OK() {
		printScaffoldProblems(stderr, report.Problems)
		_, _ = fmt.Fprintf(stderr, "scaffold-check: %d of %d files match the scaffold\n",
			report.Matched, len(report.Checked))
		return exitFailure
	}

	if report.Matched == len(report.Checked) {
		_, _ = fmt.Fprintf(stdout, "scaffold-check: %d files match the scaffold\n", report.Matched)
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "scaffold-check: %d of %d files match the scaffold (%d allowed)\n",
		report.Matched, len(report.Checked), len(report.Allowed))
	return exitOK
}

// printScaffoldProblems writes one report per deviation, naming the file and,
// for a difference, its first differing line.
func printScaffoldProblems(w io.Writer, problems []scaffold.Problem) {
	for _, problem := range problems {
		if problem.Kind == scaffold.Missing {
			_, _ = fmt.Fprintf(w, "scaffold-check: %s: missing\n", problem.Path)
			continue
		}
		_, _ = fmt.Fprintf(w, "scaffold-check: %s:%d: differs from the scaffold\n",
			problem.Path, problem.Line)
		_, _ = fmt.Fprintf(w, "  scaffold:   %q\n", problem.Want)
		_, _ = fmt.Fprintf(w, "  repository: %q\n", problem.Got)
	}
}

// scaffoldInput is the raw flag set the scaffold subcommands parsed.
type scaffoldInput struct {
	Provider     string
	Product      string
	Module       string
	Year         string
	Copyright    string
	KitVersion   string
	Packages     string
	VersionsFile string
	Upstream     string
	Repo         string
	License      string
}

// scaffoldValues validates the supplied values and derives the ones the
// repository itself answers. It fails on anything it cannot derive rather than
// inventing a value that would then be checked against every provider.
func scaffoldValues(root string, in scaffoldInput) (map[string]string, error) {
	provider := strings.TrimSpace(in.Provider)
	if provider == "" {
		derived, err := providerFromDir(root)
		if err != nil {
			return nil, err
		}
		provider = derived
	}
	if !providerPattern.MatchString(provider) {
		return nil, fmt.Errorf("provider %q must match %s", provider, providerPattern)
	}

	module := strings.TrimSpace(in.Module)
	if module == "" {
		derived, err := moduleFromGoMod(root)
		if err != nil {
			return nil, err
		}
		module = derived
	}

	product := strings.TrimSpace(in.Product)
	if product == "" {
		return nil, errors.New("--product is required: the product display name has no reliable derivation")
	}
	year := strings.TrimSpace(in.Year)
	if year == "" {
		return nil, errors.New("--year is required: the copyright year has no reliable derivation")
	}
	if !yearPattern.MatchString(year) {
		return nil, fmt.Errorf("--year %q must be a four-digit year", year)
	}
	copyright := strings.TrimSpace(in.Copyright)
	if copyright == "" {
		return nil, errors.New("--copyright is required: the copyright holder has no reliable derivation")
	}
	kitVersion := strings.TrimSpace(in.KitVersion)
	if kitVersion == "" {
		return nil, errors.New("--kit-version is required: pin the callers to a release tag or a full commit SHA")
	}
	if !kitVersionPattern.MatchString(kitVersion) {
		return nil, fmt.Errorf("--kit-version %q must be a vX.Y.Z tag or a full 40-character commit SHA", kitVersion)
	}

	packages := strings.TrimSpace(in.Packages)
	if packages == "" {
		derived, err := packagesFromMakefile(root)
		if err != nil {
			return nil, err
		}
		packages = derived
	}

	versionsFile := strings.TrimSpace(in.VersionsFile)
	if versionsFile == "" {
		versionsFile = ".github/" + provider + "-versions.txt"
	}

	upstream := strings.TrimSpace(in.Upstream)
	if upstream == "" {
		return nil, errors.New("--upstream is required: the release window has no reliable derivation")
	}
	if !ownerNamePattern.MatchString(upstream) {
		return nil, fmt.Errorf("--upstream %q must be owner/name", upstream)
	}

	repo := strings.TrimSpace(in.Repo)
	if repo == "" {
		derived, err := repoFromGit(root)
		if err != nil {
			return nil, err
		}
		repo = derived
	}
	if err := validateRepo(repo); err != nil {
		return nil, fmt.Errorf("--repo %q is invalid: %w (expected owner/name)", repo, err)
	}

	license := strings.TrimSpace(in.License)
	if license == "" {
		license = defaultLicense
	}
	if !licensePattern.MatchString(license) {
		return nil, fmt.Errorf("--license %q must be an SPDX identifier such as MIT or Apache-2.0", license)
	}

	upper := strings.ToUpper(provider)
	return map[string]string{
		"PROVIDER":           provider,
		"PRODUCT":            product,
		"MODULE":             module,
		"REPO":               repo,
		"REGISTRY_NAMESPACE": defaultRegistryNamespace,
		"YEAR":               year,
		"COPYRIGHT":          copyright,
		"KIT_VERSION":        kitVersion,
		"PACKAGES":           packages,
		"VERSIONS_FILE":      versionsFile,
		"UPSTREAM_URL":       upstream,
		"VERSION_ENV":        upper + "_VERSION",
		"EXPECT_VERSION_ENV": upper + "_EXPECT_VERSION",
		"LICENSE":            license,
	}, nil
}

// packagesFromMakefile derives the go list pattern from the repository's own
// `go test` invocations. A repository whose Makefile tests `./...` must not be
// asked to say so a second time; when no pattern can be found the flag is
// required rather than guessed.
func packagesFromMakefile(root string) (string, error) {
	for _, name := range []string{"GNUmakefile", "Makefile"} {
		path := filepath.Join(root, name)
		content, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read %s to derive --packages: %w", path, err)
		}
		if pattern := packagePatternFromMakefile(string(content)); pattern != "" {
			return pattern, nil
		}
		return "", fmt.Errorf("%s has no `go test` package pattern; pass --packages", path)
	}
	return "", errors.New("cannot derive --packages without a GNUmakefile or Makefile; pass --packages")
}

// packagePatternFromMakefile picks the most frequent `./…` token from the
// `go test` lines. The last such token on a line is the package pattern: a
// `-run` or `-fuzz` argument can precede it, and flags never start with `./`.
func packagePatternFromMakefile(content string) string {
	counts := make(map[string]int)
	order := make([]string, 0)

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) > 0 {
			fields[0] = strings.TrimLeft(fields[0], "@+-")
		}
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] != "go" || fields[i+1] != "test" {
				continue
			}
			pattern := ""
			for _, field := range fields[i+2:] {
				if goPackageTokenPattern.MatchString(field) {
					pattern = field
				}
			}
			if pattern == "" {
				continue
			}
			if counts[pattern] == 0 {
				order = append(order, pattern)
			}
			counts[pattern]++
			break
		}
	}

	best := ""
	for _, pattern := range order {
		if best == "" || counts[pattern] > counts[best] {
			best = pattern
		}
	}
	return best
}

// repoFromGit derives owner/name from the origin remote, so a repository does
// not have to repeat a fact its checkout already carries.
func repoFromGit(root string) (string, error) {
	output, err := exec.CommandContext(context.Background(),
		"git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", errors.New("cannot derive --repo from `git remote get-url origin`; pass --repo owner/name")
	}
	raw := strings.TrimSpace(string(output))
	repo, err := parseRemote(raw)
	if err != nil {
		return "", fmt.Errorf("cannot derive --repo from origin %q: %w; pass --repo owner/name", raw, err)
	}
	return repo, nil
}

// parseRemote accepts the https, ssh and scp-style spellings git accepts and
// returns owner/name. The host must be github.com: every rendered URL is a
// GitHub path, so a GitLab or self-hosted origin would produce wrong links
// rather than a useful check. A local path is deliberately not accepted: it has
// no owner either.
func parseRemote(raw string) (string, error) {
	cleaned := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(raw), "/"), ".git")

	var repoPath string
	switch {
	case strings.Contains(cleaned, "://"):
		parsed, err := url.Parse(cleaned)
		if err != nil || parsed.Host == "" {
			return "", errors.New("not a repository URL")
		}
		if !isGitHubHost(parsed.Hostname()) {
			return "", fmt.Errorf("host %q is not github.com", parsed.Hostname())
		}
		repoPath = parsed.Path
	case strings.HasPrefix(cleaned, "git@"):
		address := strings.TrimPrefix(cleaned, "git@")
		host, rest, ok := strings.Cut(address, ":")
		if !ok {
			return "", errors.New("not an scp-style repository address")
		}
		if !isGitHubHost(host) {
			return "", fmt.Errorf("host %q is not github.com", host)
		}
		repoPath = rest
	default:
		return "", errors.New("not a URL or scp-style repository address")
	}

	parts := strings.Split(strings.Trim(repoPath, "/"), "/")
	if len(parts) < 2 {
		return "", errors.New("no owner/name in the repository address")
	}
	repo := parts[len(parts)-2] + "/" + parts[len(parts)-1]
	if err := validateRepo(repo); err != nil {
		return "", err
	}
	return repo, nil
}

// validateRepo checks the two components a GitHub repository path has. The
// owner is a user or organisation name: letters, digits and hyphens only, with
// no leading or trailing hyphen. A dot in the owner is the marker of a host
// that was pasted as owner (gitlab.com/x), which would otherwise render as
// https://github.com/gitlab.com/x.
func validateRepo(value string) error {
	owner, name, ok := strings.Cut(value, "/")
	if !ok || strings.Contains(name, "/") {
		return errors.New("must be exactly owner/name")
	}
	if !repoOwnerPattern.MatchString(owner) {
		return fmt.Errorf(
			"owner %q must be letters, digits and hyphens (no dots, scheme or slash)", owner)
	}
	if !repoNamePattern.MatchString(name) {
		return fmt.Errorf(
			"repository %q must contain only letters, digits, dots, underscores or hyphens", name)
	}
	return nil
}

// isGitHubHost reports whether host is github.com, case-insensitively. The
// whole provider family lives there and every rendered repository URL assumes
// its path shape.
func isGitHubHost(host string) bool {
	return strings.EqualFold(host, "github.com")
}

// providerFromDir derives the provider name from the repository directory,
// which is named terraform-provider-<provider>.
func providerFromDir(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve the working directory: %w", err)
	}
	const prefix = "terraform-provider-"
	base := filepath.Base(abs)
	if !strings.HasPrefix(base, prefix) || base == prefix {
		return "", fmt.Errorf(
			"cannot derive the provider from directory %q; pass --provider", base)
	}
	return strings.TrimPrefix(base, prefix), nil
}

// moduleFromGoMod reads the module path from the repository's go.mod.
func moduleFromGoMod(root string) (string, error) {
	path := filepath.Join(root, "go.mod")
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s to derive --module: %w", path, err)
	}
	match := modulePattern.FindSubmatch(content)
	if match == nil {
		return "", fmt.Errorf("%s has no module line; pass --module", path)
	}
	return string(match[1]), nil
}
