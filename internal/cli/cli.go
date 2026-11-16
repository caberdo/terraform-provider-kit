// Package cli implements the tfkit command line.
//
// Run is the single place dispatch lives: every subcommand of tfkit adds one
// case to its switch and keeps its logic in its own package.
package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/caberdo/terraform-provider-kit/internal/branchprotection"
	"github.com/caberdo/terraform-provider-kit/internal/coverage"
	"github.com/caberdo/terraform-provider-kit/internal/examples"
	"github.com/caberdo/terraform-provider-kit/internal/release"
)

// usage is printed when tfkit is invoked without a subcommand.
const usage = `tfkit is the shared tooling for the caberdo Terraform providers.

Usage:
  tfkit <command> [arguments]

Commands:
  version   Print the tfkit version.
  coverage  Work with Go coverage profiles.
  examples  Validate the Terraform examples of a provider.
  branch-protection  Check or install a repository's branch ruleset.
  release   Check a provider's release metadata.
  scaffold  Compare a provider repository against the canonical scaffold.

Run "tfkit <command> -h" for more about a command.
`

// coverageUsage is printed when `tfkit coverage` has no usable subcommand.
const coverageUsage = `Usage:
  tfkit coverage merge --output <path> <input> [input ...]
  tfkit coverage gate [flags]
  tfkit coverage readme [flags]

Commands:
  merge   Merge cover profiles, adding the hit counts of shared blocks.
  gate    Fail when coverage is below the baseline or a package floor.
  readme  Rewrite the README coverage block from the baseline.

Run "tfkit coverage <command> -h" for more about a command.
`

// examplesUsage is printed when `tfkit examples` has no usable subcommand.
const examplesUsage = `Usage:
  tfkit examples validate --provider <name> [flags]

Commands:
  validate  Build this provider and validate every module under examples/.

Run "tfkit examples <command> -h" for more about a command.
`

// branchProtectionUsage is printed when `tfkit branch-protection` has no usable
// subcommand.
const branchProtectionUsage = `Usage:
  tfkit branch-protection check [flags]
  tfkit branch-protection setup [flags]

Commands:
  check  Verify the repository ruleset still requires the checks the workflows report.
  setup  Install or update the repository ruleset.

Environment:
  GITHUB_REPOSITORY        default for --repo
  RULESET_NAME             default for --ruleset (main-protection when unset)
  REQUIRED_CONTEXTS        comma-separated required status checks
                           (default "Lint & Security,Test")
  BRANCH_PROTECTION_SOFT   "1" turns an unreadable ruleset into a warning

Run "tfkit branch-protection <command> -h" for more about a command.
`

// releaseUsage is printed when `tfkit release` has no usable subcommand.
const releaseUsage = `Usage:
  tfkit release check [flags]

Commands:
  check  Verify that the released version, the changelog and the README agree.

Run "tfkit release <command> -h" for more about a command.
`

// scaffoldUsage is printed when `tfkit scaffold` has no usable subcommand.
const scaffoldUsage = `Usage:
  tfkit scaffold check [flags]
  tfkit scaffold fix [flags] [--dry-run]

Commands:
  check  Verify that this repository still matches the canonical scaffold.
  fix    Write the enforced scaffold files over this repository, then check.

The provider and module are derived from the repository when their flags are
omitted. A deliberate deviation is recorded with --allow <path>:<reason> or in
.github/scaffold-allow.txt. fix accepts --dry-run to print the writes without
changing a file.

Run "tfkit scaffold <command> -h" for more about a command.
`

// Exit codes returned by Run.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// Run dispatches args to the subcommand it names, writing user-facing output to
// stdout and diagnostics to stderr. It returns the process exit code: 0 on
// success, 1 when a command fails, and 2 for a usage error. The caller passes
// the program version so that main, which owns the -ldflags-injected value,
// stays the only source of it.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	switch args[0] {
	case "version":
		// A failed write cannot be reported through an exit code that is
		// reserved for the command itself, so it is deliberately dropped.
		_, _ = fmt.Fprintf(stdout, "tfkit %s\n", version)
		return exitOK
	case "coverage":
		return runCoverage(args[1:], stdout, stderr)
	case "examples":
		return runExamples(args[1:], stdout, stderr)
	case "branch-protection":
		return runBranchProtection(args[1:], stdout, stderr)
	case "release":
		return runRelease(args[1:], stdout, stderr)
	case "scaffold":
		return runScaffold(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return exitUsage
	}
}

// runCoverage dispatches the coverage subcommands.
func runCoverage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "coverage requires a subcommand\n\n")
		_, _ = io.WriteString(stderr, coverageUsage)
		return exitUsage
	}

	switch args[0] {
	case "merge":
		return runCoverageMerge(args[1:], stdout, stderr)
	case "gate":
		return runCoverageGate(args[1:], stdout, stderr)
	case "readme":
		return runCoverageReadme(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown coverage command %q\n\n", args[0])
		_, _ = io.WriteString(stderr, coverageUsage)
		return exitUsage
	}
}

// runExamples dispatches the examples subcommands.
func runExamples(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "examples requires a subcommand\n\n")
		_, _ = io.WriteString(stderr, examplesUsage)
		return exitUsage
	}

	switch args[0] {
	case "validate":
		return runExamplesValidate(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown examples command %q\n\n", args[0])
		_, _ = io.WriteString(stderr, examplesUsage)
		return exitUsage
	}
}

// newExamplesRunner is the process runner `tfkit examples validate` hands to the
// examples package. It is a variable so the command tests can run without
// Terraform or a built provider binary.
var newExamplesRunner = func(stdout, stderr io.Writer) examples.Exec {
	return &examples.OSExec{Stdout: stdout, Stderr: stderr}
}

// runExamplesValidate implements `tfkit examples validate`. It only parses
// arguments; building the provider, writing the Terraform CLI config and
// validating the modules belong to the examples package. Exit codes match the
// shell script it replaces: 0 on success or a skip, 1 when validation fails and
// 2 for a usage or configuration problem.
func runExamplesValidate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit examples validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider name without the caberdo/ prefix (required)")
	pluginDir := flags.String("plugin-dir", os.Getenv("TFKIT_PLUGIN_DIR"),
		"directory to build the provider into (default $TFKIT_PLUGIN_DIR, else a temp directory)")
	requireTerraform := flags.Bool("require-terraform", os.Getenv("REQUIRE_TERRAFORM") == "1",
		"fail instead of skipping when terraform is not installed")
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, `usage: tfkit examples validate [flags]

Builds the provider from the current directory, points Terraform's dev_overrides
at it and validates every module under examples/.

Flags:
  --provider           provider name without the caberdo/ prefix (required)
  --plugin-dir         directory to build the provider into
                       (default $TFKIT_PLUGIN_DIR, else a temp directory)
  --require-terraform  fail instead of skipping when terraform is not installed
                       (default $REQUIRE_TERRAFORM=1)
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

	err := examples.Validate(".", examples.Options{
		Provider:         *provider,
		PluginDir:        *pluginDir,
		RequireTerraform: *requireTerraform,
		Runner:           newExamplesRunner(stdout, stderr),
		Stdout:           stdout,
		Stderr:           stderr,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "examples-validate: %v\n", err)
		if errors.Is(err, examples.ErrUsage) {
			_, _ = io.WriteString(stderr, "\n")
			flags.Usage()
			return exitUsage
		}
		return exitFailure
	}
	return exitOK
}

// newBranchProtectionGitHub and newBranchProtectionGit are the seams the
// branch-protection commands hand to the package. They are variables so the
// command tests can run without gh or a real checkout.
var (
	newBranchProtectionGitHub = branchprotection.NewGitHub
	newBranchProtectionGit    = branchprotection.NewGit
)

// contextsValue is the --contexts flag: a comma-separated list of required
// status check contexts, trimmed per entry. An empty entry is rejected while
// the flag is parsed, so a doubled or trailing comma is a usage error instead
// of a requirement for a context named "". The zero value means the flag was
// not given, which lets the branchprotection package fall back to
// REQUIRED_CONTEXTS and its default.
type contextsValue struct {
	contexts []string
}

// String renders the list the way it was given, for flag diagnostics.
func (v *contextsValue) String() string {
	return strings.Join(v.contexts, ",")
}

// Set parses one comma-separated list. It is called by flag.Parse, which turns
// the error into a usage exit.
func (v *contextsValue) Set(value string) error {
	parts := strings.Split(value, ",")
	contexts := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			return fmt.Errorf("--contexts has an empty entry in %q; use a comma-separated list", value)
		}
		contexts = append(contexts, trimmed)
	}
	v.contexts = contexts
	return nil
}

// List returns the parsed contexts, or nil when the flag was not given.
func (v *contextsValue) List() []string {
	return v.contexts
}

// runBranchProtection dispatches the branch-protection subcommands.
func runBranchProtection(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "branch-protection requires a subcommand\n\n")
		_, _ = io.WriteString(stderr, branchProtectionUsage)
		return exitUsage
	}

	switch args[0] {
	case "check":
		return runBranchProtectionCheck(args[1:], stdout, stderr)
	case "setup":
		return runBranchProtectionSetup(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown branch-protection command %q\n\n", args[0])
		_, _ = io.WriteString(stderr, branchProtectionUsage)
		return exitUsage
	}
}

// runBranchProtectionCheck implements `tfkit branch-protection check`. It only
// parses arguments; auditing the ruleset belongs to the branchprotection
// package. Exit codes are the script's: 0 on a match, 1 on drift, 3 when the
// rulesets API cannot be read, and 2 for a configuration problem.
func runBranchProtectionCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit branch-protection check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", os.Getenv("GITHUB_REPOSITORY"),
		"repository as owner/name (default $GITHUB_REPOSITORY, else gh repo view)")
	ruleset := flags.String("ruleset", os.Getenv("RULESET_NAME"),
		`ruleset name (default $RULESET_NAME, else "main-protection")`)
	soft := flags.Bool("soft", os.Getenv("BRANCH_PROTECTION_SOFT") == "1",
		"warn and exit 0 instead of exiting 3 when the ruleset cannot be read")
	var contexts contextsValue
	flags.Var(&contexts, "contexts",
		`comma-separated required status check contexts (default $REQUIRED_CONTEXTS, else "Lint & Security,Test")`)
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, `usage: tfkit branch-protection check [flags]

Verifies that the repository ruleset still requires the status check contexts
the pull_request workflows report. Exits 0 on a match, 1 on drift and 3 when the
rulesets API cannot be read (a fork, or a token without administration
permission).

Workflows are read from the git checkout root (git rev-parse --show-toplevel),
falling back to the current directory when git is unavailable, so the command
works from any directory inside the checkout.

Flags:
  --repo      repository as owner/name
              (default $GITHUB_REPOSITORY, else gh repo view)
  --ruleset   ruleset name (default $RULESET_NAME, else "main-protection")
  --contexts  comma-separated required status check contexts, trimmed per entry
              (default $REQUIRED_CONTEXTS, else "Lint & Security,Test")
  --soft      warn and exit 0 instead of exiting 3 when the ruleset cannot be
              read (default $BRANCH_PROTECTION_SOFT=1)
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

	status, err := branchprotection.Check(branchprotection.CheckOptions{
		Repo:     *repo,
		Ruleset:  *ruleset,
		Contexts: contexts.List(),
		Soft:     *soft,
		GitHub:   newBranchProtectionGitHub(),
		Git:      newBranchProtectionGit(),
		Stdout:   stdout,
		Stderr:   stderr,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "branch-protection check: %v\n", err)
		if status == branchprotection.StatusConfig {
			return exitUsage
		}
		return exitFailure
	}
	return int(status)
}

// runBranchProtectionSetup implements `tfkit branch-protection setup`. It only
// parses arguments; validating the contexts and writing the ruleset belongs to
// the branchprotection package. A failure exits 1, as the script does.
func runBranchProtectionSetup(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit branch-protection setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", os.Getenv("GITHUB_REPOSITORY"),
		"repository as owner/name (default $GITHUB_REPOSITORY, else gh repo view)")
	ruleset := flags.String("ruleset", os.Getenv("RULESET_NAME"),
		`ruleset name (default $RULESET_NAME, else "main-protection")`)
	var contexts contextsValue
	flags.Var(&contexts, "contexts",
		`comma-separated required status check contexts (default $REQUIRED_CONTEXTS, else "Lint & Security,Test")`)
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, `usage: tfkit branch-protection setup [flags]

Installs or updates the repository ruleset. The required contexts are validated
against the job names in workflows that run on pull_request, so a matrix or
if:-guarded job is refused before anything is written. The token needs admin
permission on the repository.

Workflows are read from the git checkout root (git rev-parse --show-toplevel),
falling back to the current directory when git is unavailable, so the command
works from any directory inside the checkout.

Flags:
  --repo      repository as owner/name
              (default $GITHUB_REPOSITORY, else gh repo view)
  --ruleset   ruleset name (default $RULESET_NAME, else "main-protection")
  --contexts  comma-separated required status check contexts, trimmed per entry
              (default $REQUIRED_CONTEXTS, else "Lint & Security,Test")
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

	if err := branchprotection.Setup(branchprotection.SetupOptions{
		Repo:     *repo,
		Ruleset:  *ruleset,
		Contexts: contexts.List(),
		GitHub:   newBranchProtectionGitHub(),
		Git:      newBranchProtectionGit(),
		Stdout:   stdout,
		Stderr:   stderr,
	}); err != nil {
		_, _ = fmt.Fprintf(stderr, "branch-protection setup: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// runRelease dispatches the release subcommands.
func runRelease(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintf(stderr, "release requires a subcommand\n\n")
		_, _ = io.WriteString(stderr, releaseUsage)
		return exitUsage
	}

	switch args[0] {
	case "check":
		return runReleaseCheck(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown release command %q\n\n", args[0])
		_, _ = io.WriteString(stderr, releaseUsage)
		return exitUsage
	}
}

// runReleaseCheck implements `tfkit release check`. It only parses arguments;
// reading the three files and deciding whether they agree belongs to the
// release package. Exit codes are the script's: 0 when the metadata is
// consistent, 1 when it is not (with every problem listed) and 2 for a usage
// problem or a required file that is missing or unreadable.
func runReleaseCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit release check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	versionFile := flags.String("version-file", "",
		"path of the file holding the version; empty derives it from the last git tag")
	changelog := flags.String("changelog", release.DefaultChangelog, "path of the changelog")
	readme := flags.String("readme", release.DefaultReadme,
		"path of the README carrying the install constraint")
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, `usage: tfkit release check [flags]

Verifies that the version being shipped has a dated changelog section and that
the README installs that minor. Every inconsistency is reported in one run, so a
release pull request is fixed in one pass.

The version comes from --version-file when that names a file. Without the flag,
a VERSION file beside the working directory is still read; when there is none,
the version is the last git tag (git describe --tags --abbrev=0) with any
leading 'v' removed. The repository does not carry the version as a file.

Exits 0 when the metadata is consistent, 1 when it is not, and 2 when a required
file is missing or unreadable or the repository has no tag.

Flags:
  --version-file  path of the file holding the version; empty derives it from
                  the last git tag (default: read VERSION when it exists)
  --changelog     path of the changelog (default "CHANGELOG.md")
  --readme        path of the README carrying the install constraint
                  (default "README.md")
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

	// An explicit path is an instruction: a missing file is an error rather
	// than a silent fallback to the tag. Only the unflagged case probes the
	// legacy file and then derives the version.
	explicitVersionFile := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "version-file" {
			explicitVersionFile = true
		}
	})
	versionPath := *versionFile
	if versionPath == "" && !explicitVersionFile {
		if _, err := os.Stat(release.DefaultVersionFile); err == nil {
			versionPath = release.DefaultVersionFile
		}
	}

	report, err := release.Check(release.Options{
		VersionFile: versionPath,
		Changelog:   *changelog,
		Readme:      *readme,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release-check: %v\n", err)
		return exitUsage
	}
	if len(report.Problems) > 0 {
		for _, problem := range report.Problems {
			_, _ = fmt.Fprintf(stderr, "release-check: %s\n", problem)
		}
		return exitFailure
	}

	if report.Tag != "" {
		_, _ = fmt.Fprintf(stdout, "release-check: %s from the last git tag (%s), %s and %s agree\n",
			report.Version, report.Tag, *changelog, *readme)
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "release-check: %s=%s, %s and %s agree\n",
		versionPath, report.Version, *changelog, *readme)
	return exitOK
}

// runCoverageMerge implements `tfkit coverage merge`. It only parses arguments
// and moves files; parsing, merging and formatting a profile belongs to the
// coverage package.
func runCoverageMerge(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit coverage merge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "path of the merged profile to write")
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, "usage: tfkit coverage merge --output <path> <input> [input ...]\n")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	inputs := flags.Args()
	// The script this replaces guards with `$# -lt 2`, where the output file
	// counts as the first argument: one output plus at least one input. A
	// missing --output is the same class of mistake.
	if *output == "" || len(inputs) == 0 {
		flags.Usage()
		return exitUsage
	}

	files := make([]*os.File, 0, len(inputs))
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	for _, path := range inputs {
		file, err := os.Open(path)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
			return exitFailure
		}
		files = append(files, file)
	}

	profiles := make([]coverage.Profile, 0, len(files))
	for i, file := range files {
		profile, err := coverage.ParseProfile(file)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "coverage merge: %s: %v\n", inputs[i], err)
			return exitFailure
		}
		profiles = append(profiles, profile)
	}

	merged, err := coverage.Merge(profiles)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
		return exitFailure
	}

	out, err := os.Create(*output)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
		return exitFailure
	}
	if err := coverage.WriteProfile(out, merged); err != nil {
		_ = out.Close()
		_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
		return exitFailure
	}
	if err := out.Close(); err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage merge: %v\n", err)
		return exitFailure
	}

	_, _ = fmt.Fprintf(stdout, "merged %d profiles into %s\n", len(profiles), *output)
	return exitOK
}

// runCoverageGate implements `tfkit coverage gate`. It owns file handling and
// the human-facing report; measuring the profile and deciding whether a floor
// is met belongs to the coverage package.
func runCoverageGate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit coverage gate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "coverage.out", "path of the cover profile to check")
	baselinePath := flags.String("baseline", ".github/coverage-baseline.txt", "path of the statement-coverage baseline")
	floorsPath := flags.String("floors", ".github/coverage-floors.txt", "path of the per-package floors")
	patterns := flags.String("packages", "./internal/...", "go list pattern for the packages to check")
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, `usage: tfkit coverage gate [flags]

Flags:
  --profile   path of the cover profile to check (default "coverage.out")
  --baseline  path of the statement-coverage baseline (default ".github/coverage-baseline.txt")
  --floors    path of the per-package floors; when absent only the total is gated
              (default ".github/coverage-floors.txt"); not used when the baseline
              is the key=value dialect, whose floors live in that file
  --packages  go list pattern for the packages to check (default "./internal/...")
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

	profile, err := readProfileFile(*profilePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %v\n", err)
		return exitUsage
	}
	baselineData, err := readBaselineData(*baselinePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %v\n", err)
		return exitUsage
	}

	// The file is the interface: a baseline that carries `floor_` keys is the
	// key=value scope dialect and is gated by GateScopes, everything else by
	// the bare-percentage Gate. A flag would let a provider gate with the
	// wrong parser by forgetting it, so there is none.
	if coverage.ScopeDialect(baselineData) {
		return runCoverageGateScopes(profile, baselineData, *profilePath, *baselinePath, *patterns, stdout, stderr)
	}

	baseline, err := coverage.ReadBaseline(bytes.NewReader(baselineData))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %s: %v\n", *baselinePath, err)
		return exitUsage
	}

	// The total is checked before the floors are read, as the shell script
	// does: a run that is already below the module floor reports that and
	// stops, rather than surfacing a floor-file problem it would never reach.
	total := coverage.Total(profile)
	_, _ = fmt.Fprintf(stdout, "Total statement coverage: %.1f%% (floor %s%%)\n",
		total, formatPercent(baseline))
	if total < baseline {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: FAIL — %.1f%% is below the %s%% floor in %s\n",
			total, formatPercent(baseline), *baselinePath)
		_, _ = io.WriteString(stderr, "Add tests for what you changed, or lower the floor deliberately in the same pull request.\n")
		return exitFailure
	}

	floors, err := readFloorsFile(*floorsPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %v\n", err)
		return exitUsage
	}

	var packages []string
	if floors != nil {
		packages, err = listGoPackages(*patterns)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "coverage-gate: go list %q: %v\n", *patterns, err)
			return exitUsage
		}
		if len(packages) == 0 {
			_, _ = fmt.Fprintf(stderr, "coverage-gate: go list %q returned no packages\n", *patterns)
			return exitUsage
		}
	}

	report, err := coverage.Gate(profile, baseline, floors, packages)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %v\n", err)
		return exitUsage
	}

	if floors != nil {
		printPackageTable(stdout, profile, packages, floors, *floorsPath)
	}

	if len(report.Failures) > 0 {
		_, _ = fmt.Fprintf(stderr, "\ncoverage-gate: FAIL — %d package(s) below their floor:\n", len(report.Failures))
		for _, failure := range report.Failures {
			_, _ = fmt.Fprintf(stderr, "  %s: %.2f%% < %s%% (delta %.2f)\n",
				failure.Package, failure.Actual, formatPercent(failure.Floor), failure.Actual-failure.Floor)
		}
		_, _ = fmt.Fprintf(stderr, "Add tests for what you changed, or lower the floor deliberately in %s in the same pull request.\n", *floorsPath)
		return exitFailure
	}

	_, _ = io.WriteString(stdout, "coverage-gate: PASS\n")
	return exitOK
}

// runCoverageGateScopes implements `tfkit coverage gate` for the key=value
// baseline dialect: the floors live in the baseline file, so no floors file is
// read. It prints the dialect it chose on stderr, keeping stdout byte-identical
// to the shell script whose CI output it replaces.
func runCoverageGateScopes(
	profile coverage.Profile,
	baselineData []byte,
	profilePath, baselinePath, patterns string,
	stdout, stderr io.Writer,
) int {
	_, _ = fmt.Fprintf(stderr, "coverage-gate: baseline dialect: key=value (%s carries floor_ keys)\n", baselinePath)

	baseline, err := coverage.ParseScopeBaseline(bytes.NewReader(baselineData))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %s: %v\n", baselinePath, err)
		return exitUsage
	}

	packages, err := listGoPackages(patterns)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: go list %q: %v\n", patterns, err)
		return exitUsage
	}
	if len(packages) == 0 {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: go list %q returned no packages\n", patterns)
		return exitUsage
	}

	report, err := coverage.GateScopes(profile, baseline, packages)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %v\n", err)
		return exitUsage
	}

	if err := coverage.WriteScopeReport(stdout, baseline, report, profilePath, baselinePath); err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-gate: %v\n", err)
		return exitFailure
	}
	if len(report.Failures) > 0 {
		return exitFailure
	}
	return exitOK
}

// runCoverageReadme implements `tfkit coverage readme`. It owns file handling
// and the messages; rendering and checking the block belongs to the coverage
// package. The sentence names `make coverage-gate` by default, the target every
// provider ships now that the shell script is retired; --gate-command stays an
// override for a provider that enforces the floor some other way.
func runCoverageReadme(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("tfkit coverage readme", flag.ContinueOnError)
	flags.SetOutput(stderr)
	check := flags.Bool("check", false, "report a stale README instead of rewriting it")
	readmePath := flags.String("readme", "README.md", "path of the README to update")
	baselinePath := flags.String("baseline", ".github/coverage-baseline.txt", "path of the statement-coverage baseline")
	gateCommand := flags.String("gate-command", "make coverage-gate", "command the emitted sentence names as the coverage gate")
	flags.Usage = func() {
		_, _ = io.WriteString(stderr, `usage: tfkit coverage readme [flags]

Flags:
  --check         report drift instead of rewriting; exits 1 when stale
  --readme        path of the README to update (default "README.md")
  --baseline      path of the statement-coverage baseline (default ".github/coverage-baseline.txt")
  --gate-command  command the emitted sentence names as the gate
                  (default "make coverage-gate")
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

	doc, mode, err := readReadmeFile(*readmePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-readme: %v\n", err)
		return exitUsage
	}
	baselineData, err := readBaselineData(*baselinePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-readme: %v\n", err)
		return exitUsage
	}

	// The baseline file decides the rendering, exactly as it decides the gate:
	// `floor_` keys mean the key=value scope dialect, with its measured badge and
	// per-scope bar table; anything else is the bare-percentage floor block. A
	// flag would let a provider render the wrong shape by forgetting it.
	block, err := readmeBlocks(doc, baselineData, *gateCommand)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-readme: %s: %v\n", *baselinePath, err)
		return exitUsage
	}

	if *check {
		if err := block.check(); err != nil {
			if errors.Is(err, coverage.ErrReadmeStale) {
				_, _ = fmt.Fprintf(stderr, "coverage-readme: '%s' is out of date with '%s'\n", *readmePath, *baselinePath)
				_, _ = io.WriteString(stderr, "Run 'make coverage-readme' and commit the result.\n")
				return exitFailure
			}
			_, _ = fmt.Fprintf(stderr, "coverage-readme: '%s': %v\n", *readmePath, err)
			return exitUsage
		}
		_, _ = fmt.Fprintf(stdout, "coverage-readme: '%s' is in sync with '%s'\n", *readmePath, *baselinePath)
		return exitOK
	}

	rendered, err := block.render()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-readme: '%s': %v\n", *readmePath, err)
		return exitUsage
	}
	// A marker or baseline problem is caught above, before anything is
	// written; this is the ordinary "nothing to do" case.
	if bytes.Equal(rendered, doc) {
		_, _ = fmt.Fprintf(stdout, "coverage-readme: '%s' is already in sync with '%s'\n", *readmePath, *baselinePath)
		return exitOK
	}
	if err := os.WriteFile(*readmePath, rendered, mode); err != nil {
		_, _ = fmt.Fprintf(stderr, "coverage-readme: %v\n", err)
		return exitFailure
	}

	_, _ = fmt.Fprintf(stdout, "coverage-readme: updated '%s' from '%s'\n", *readmePath, *baselinePath)
	return exitOK
}

// readmeBlock is one dialect's renderer: the rewrite and the drift check, so the
// caller never compares bytes itself and a check can never write.
type readmeBlock struct {
	render func() ([]byte, error)
	check  func() error
}

// readmeBlocks chooses the block shape and parses the baseline for it, once, so
// a configuration error is reported before anything is written. A baseline with
// `floor_` keys is the key=value scope dialect, whose floors live in the same
// file; everything else is the bare-percentage floor block, which ignores the
// scope fields.
func readmeBlocks(doc, baselineData []byte, gateCommand string) (readmeBlock, error) {
	if coverage.ScopeDialect(baselineData) {
		baseline, err := coverage.ParseScopeBaseline(bytes.NewReader(baselineData))
		if err != nil {
			return readmeBlock{}, err
		}
		return readmeBlock{
			render: func() ([]byte, error) { return coverage.RenderScopeReadme(doc, baseline) },
			check:  func() error { return coverage.CheckScopeReadme(doc, baseline) },
		}, nil
	}

	baseline, err := coverage.ReadBaseline(bytes.NewReader(baselineData))
	if err != nil {
		return readmeBlock{}, err
	}
	return readmeBlock{
		render: func() ([]byte, error) { return coverage.RenderReadme(doc, baseline, gateCommand) },
		check:  func() error { return coverage.CheckReadme(doc, baseline, gateCommand) },
	}, nil
}

// readReadmeFile reads the README at path and returns its permissions, so a
// rewrite keeps the mode the file already had.
func readReadmeFile(path string) ([]byte, os.FileMode, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, 0, fmt.Errorf("'%s' not found", path)
		}
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", path, err)
	}
	doc, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", path, err)
	}
	return doc, info.Mode().Perm(), nil
}

// printPackageTable writes the per-package report in the shape the shell
// script prints today, so a CI log stays comparable while the script is
// retired.
func printPackageTable(w io.Writer, profile coverage.Profile, packages []string, floors coverage.Floors, floorsPath string) {
	_, _ = fmt.Fprintf(w, "\nPer-package statement coverage (floors from %s):\n", floorsPath)
	_, _ = fmt.Fprintf(w, "  %-46s %8s %8s %8s\n", "PACKAGE", "COVER", "FLOOR", "DELTA")

	defaultFloor := floors[coverage.DefaultFloorKey]
	for _, measured := range coverage.Measure(profile, packages) {
		floor := defaultFloor
		if explicit, ok := floors[measured.Package]; ok {
			floor = explicit
		}
		text := floor.Text
		if text == "" {
			text = formatPercent(floor.Value)
		}
		_, _ = fmt.Fprintf(w, "  %-46s %7.1f%% %7s%% %+8.1f\n",
			measured.Package, measured.Coverage, text, measured.Coverage-floor.Value)
	}
}

// readProfileFile parses the profile at path. A missing profile is a
// configuration error, not a failed gate.
func readProfileFile(path string) (coverage.Profile, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return coverage.Profile{}, fmt.Errorf("profile %q not found; run 'make coverage' first", path)
		}
		return coverage.Profile{}, err
	}
	defer func() { _ = file.Close() }()

	profile, err := coverage.ParseProfile(file)
	if err != nil {
		return coverage.Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	return profile, nil
}

// readBaselineData reads the raw baseline bytes. The dialect is detected from
// the file's keys, so the gate needs the content before it can choose a parser.
func readBaselineData(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("baseline %q not found", path)
		}
		return nil, err
	}
	return data, nil
}

// readFloorsFile reads the per-package floors at path. A missing file is not an
// error: it means the provider gates the module total only. The nil map is
// what tells [coverage.Gate] that.
func readFloorsFile(path string) (coverage.Floors, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	floors, err := coverage.ParseFloors(file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return floors, nil
}

// listGoPackages runs `go list` and returns the packages matching pattern as
// repo-relative names. `go list` prints full import paths, while profiles and
// floors files use paths relative to the module root, so the module prefix from
// `go list -m` is stripped. A package that is the module root itself keeps its
// full path, as the shell script's awk does.
//
// It is a variable so the command tests can run without a Go module.
var listGoPackages = func(pattern string) ([]string, error) {
	ctx := context.Background()

	moduleOutput, err := exec.CommandContext(ctx, "go", "list", "-m").Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m: %w", err)
	}
	module := strings.TrimSpace(string(moduleOutput))
	if module == "" {
		return nil, errors.New("go list -m returned an empty module path")
	}

	output, err := exec.CommandContext(ctx, "go", "list", pattern).Output()
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(output))
	packages := make([]string, 0, len(fields))
	prefix := module + "/"
	for _, name := range fields {
		packages = append(packages, strings.TrimPrefix(name, prefix))
	}
	return packages, nil
}

// formatPercent renders a percentage without trailing zeros, matching the raw
// tokens the shell script echoes from the baseline.
func formatPercent(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// printUsage writes the usage text to w, for the same reason Run drops the
// write errors above.
func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usage)
}
