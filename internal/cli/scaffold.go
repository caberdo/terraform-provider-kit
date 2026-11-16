package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/caberdo/terraform-provider-kit/internal/scaffold"
)

// Scaffold defaults. PACKAGES is the canonical layout a repository scaffolded
// from the kit starts with; a repository that moved its packages passes the
// flag explicitly.
const (
	defaultPackages          = "./internal/..."
	defaultRegistryNamespace = "caberdo"
)

// Validators for the values that must be named rather than derived. A wrong
// value is a configuration error, not a guess the checker should paper over.
var (
	providerPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	yearPattern       = regexp.MustCompile(`^[0-9]{4}$`)
	kitVersionPattern = regexp.MustCompile(`^(v[0-9]+\.[0-9]+\.[0-9]+|[0-9a-f]{40})$`)
	upstreamPattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	modulePattern     = regexp.MustCompile(`(?m)^module[ \t]+(\S+)[ \t]*$`)
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

// Set parses one <path>:<reason> pair.
func (v *allowValues) Set(value string) error {
	path, reason, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(path) == "" || strings.TrimSpace(reason) == "" {
		return fmt.Errorf("want <path>:<reason>, got %q", value)
	}
	*v = append(*v, scaffold.Allowance{
		Path:   strings.TrimSpace(path),
		Reason: strings.TrimSpace(reason),
	})
	return nil
}

// runScaffoldCheck implements `tfkit scaffold check`. It only parses arguments
// and derives the values the repository supplies; rendering and comparing
// belong to the scaffold package. Exit codes: 0 when the repository matches,
// 1 on drift (with every differing or missing file named) and 2 for a usage or
// configuration problem.
func runScaffoldCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit scaffold check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "",
		"provider name without the terraform-provider- prefix (default: the directory name)")
	product := flags.String("product", "", "product display name, such as Trino (required)")
	module := flags.String("module", "", "Go module path (default: the module line of go.mod)")
	year := flags.String("year", "", "copyright year in LICENSE (required)")
	copyright := flags.String("copyright", "", "copyright holder in LICENSE (required)")
	kitVersion := flags.String("kit-version", "",
		"kit release tag or full commit SHA the workflow callers pin (required)")
	packages := flags.String("packages", defaultPackages, "go list pattern the tests and coverage gate use")
	versionsFile := flags.String("versions-file", "",
		"supported-versions file (default: .github/<provider>-versions.txt)")
	upstream := flags.String("upstream", "",
		"upstream repository whose releases define the version window, as owner/name (required)")
	var allow allowValues
	flags.Var(&allow, "allow",
		"exempt a checked file, as <path>:<reason> (repeatable; the reason is required)")
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, `usage: tfkit scaffold check [flags]

Verifies that this repository still matches the canonical scaffold the kit owns:
the editor, ignore and lint configuration, the community files and the issue
templates. A differing file is named with its first differing line; a missing
file is named as missing. A deliberate deviation is recorded with --allow
<path>:<reason>, and a placeholder without a value is a configuration error.

The provider and module are derived from the repository when the matching flag
is not given; the product-facing facts have no reliable derivation and must be
supplied. The check never writes to the repository.

Exits 0 when the repository matches, 1 on drift, and 2 for a usage or
configuration problem.

Flags:
  --provider       provider name without the terraform-provider- prefix
  --product        product display name (required)
  --module         Go module path (default: the module line of go.mod)
  --year           copyright year in LICENSE (required)
  --copyright      copyright holder in LICENSE (required)
  --kit-version    kit release tag or full commit SHA (required)
  --packages       go list pattern for tests and coverage (default "./internal/...")
  --versions-file  supported-versions file (default ".github/<provider>-versions.txt")
  --upstream       upstream repository as owner/name (required)
  --allow          exempt a checked file as <path>:<reason> (repeatable)
`)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if flags.NArg() > 0 {
		flags.Usage()
		return exitUsage
	}

	values, err := scaffoldValues(".", scaffoldInput{
		Provider:     *provider,
		Product:      *product,
		Module:       *module,
		Year:         *year,
		Copyright:    *copyright,
		KitVersion:   *kitVersion,
		Packages:     *packages,
		VersionsFile: *versionsFile,
		Upstream:     *upstream,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "scaffold-check: %v\n", err)
		return exitUsage
	}

	report, err := scaffold.Check(".", values, scaffold.Options{Allow: allow})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "scaffold-check: %v\n", err)
		return exitUsage
	}

	for _, allowance := range report.Allowed {
		_, _ = fmt.Fprintf(stdout, "scaffold-check: %s: allowed (%s)\n",
			allowance.Path, allowance.Reason)
	}
	if !report.OK() {
		printScaffoldProblems(stderr, report.Problems)
		return exitFailure
	}

	_, _ = fmt.Fprintf(stdout, "scaffold-check: %d files match the scaffold\n", len(report.Checked))
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

// scaffoldInput is the raw flag set runScaffoldCheck parsed.
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
		return nil, errors.New("--packages must not be empty")
	}
	versionsFile := strings.TrimSpace(in.VersionsFile)
	if versionsFile == "" {
		versionsFile = ".github/" + provider + "-versions.txt"
	}
	upstream := strings.TrimSpace(in.Upstream)
	if upstream == "" {
		return nil, errors.New("--upstream is required: the release window has no reliable derivation")
	}
	if !upstreamPattern.MatchString(upstream) {
		return nil, fmt.Errorf("--upstream %q must be owner/name", upstream)
	}

	upper := strings.ToUpper(provider)
	return map[string]string{
		"PROVIDER":           provider,
		"PRODUCT":            product,
		"MODULE":             module,
		"REGISTRY_NAMESPACE": defaultRegistryNamespace,
		"YEAR":               year,
		"COPYRIGHT":          copyright,
		"KIT_VERSION":        kitVersion,
		"PACKAGES":           packages,
		"VERSIONS_FILE":      versionsFile,
		"UPSTREAM_URL":       upstream,
		"VERSION_ENV":        upper + "_VERSION",
		"EXPECT_VERSION_ENV": upper + "_EXPECT_VERSION",
	}, nil
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
