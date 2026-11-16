// Package release validates the release metadata a provider repository
// carries: the changelog section that documents the newest release and the
// README install constraint.
//
// The version is the tag, so the gate does not ask the tag to agree with
// anything. It validates the repository's internal consistency — the newest
// CHANGELOG.md release section against the README install constraint — and
// reports the relationship to the last git tag beside the verdict: "pending
// release" when the changelog is ahead of the tag (the normal state of a
// release pull request and of every commit between its merge and the tag
// push), "released" when they match. A mismatch is not an error; requiring the
// tag would make the check fail exactly the commit it exists to gate.
//
// Supplying VersionFile keeps the older flow: the file supplies the version and
// is compared with the changelog and the README. The repository does not carry
// a version file by default, and a VERSION file that is not named on the
// command line is an error rather than a silent fallback.
//
// Check returns the problems it finds instead of stopping at the first, so a
// broken release pull request is fixed in one pass. The shell script stopped at
// the first problem; every check below runs unless its input is unusable, which
// is a deliberate difference. An unusable file is an error rather than a
// problem, and the CLI maps it to exit code 2, as the script did.
package release

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Default paths, used when Options leaves a field empty.
const (
	// DefaultVersionFile is the file a repository used before the version moved
	// to the tag. It is not a default for Check: an empty Options.VersionFile
	// derives the version from the changelog. The CLI refuses a VERSION file
	// that the caller did not name on the command line, so a botched migration
	// fails loudly instead of restoring the old contract.
	DefaultVersionFile = "VERSION"
	// DefaultChangelog is the changelog beside the working directory.
	DefaultChangelog = "CHANGELOG.md"
	// DefaultReadme is the README beside the working directory.
	DefaultReadme = "README.md"
)

// The tag relationship the success line reports. None of them changes the exit
// status: the tag is not an input to the verdict.
const (
	// StatePending means the changelog's newest section is ahead of the last
	// tag: a release is staged but not tagged.
	StatePending = "pending release"
	// StateReleased means the changelog's newest section is the last tag.
	StateReleased = "released"
	// StateBehind means the last tag is ahead of the changelog's newest
	// section. It is reported rather than failed, because the tag is not a
	// gate input; a missing changelog entry still deserves an operator's
	// attention.
	StateBehind = "behind the last tag"
)

// Git failure classes. They are distinct so an operator hears which of the
// four situations they are in instead of "exit status 128".
var (
	// ErrGitMissing means no git binary is on PATH.
	ErrGitMissing = errors.New("git is not installed: 'git' was not found on PATH")
	// ErrNotARepository means the working directory is outside a git
	// repository.
	ErrNotARepository = errors.New("not a git repository")
	// ErrShallowClone means the checkout has no tags to describe: a shallow
	// clone fetched no tag refs.
	ErrShallowClone = errors.New("shallow clone: tags are not available, fetch the full history with tags")
	// ErrNoTag means the repository has no tag matching 'v*'. It is the one
	// class Check treats as a normal state: a repository without a tag is
	// valid, and the success line says "no tag yet".
	ErrNoTag = errors.New("no 'v*' tag found")
	// ErrAmbiguousTag means two or more 'v*' tags point at the same commit, so
	// `git describe` cannot choose the release.
	ErrAmbiguousTag = errors.New("ambiguous tag")
)

// versionPattern is the only acceptable content of the version file: X.Y.Z
// without a leading 'v', as the shell script's `^[0-9]+\.[0-9]+\.[0-9]+$`.
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// semverCorePattern matches the numeric core of a tag with a prerelease or
// build suffix, so `v1.3.0-rc1` is comparable without crashing.
var semverCorePattern = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)`)

// unreleasedPattern is the heading the next release accumulates under. Exactly
// one must survive cutting a release.
var unreleasedPattern = regexp.MustCompile(`^## Unreleased[[:space:]]*$`)

// releasedPattern finds a released section heading among the two conventions
// the providers use. rustfs's fragment assembler writes `## X.Y.Z (YYYY-MM-DD)`
// and the kit's scaffold ships `## [X.Y.Z] - YYYY-MM-DD`; the checker accepts
// both so the two can meet. The bracketed shape also matches without its date,
// so an undated heading is reported by the dated-section check instead of
// making the changelog look empty.
var releasedPattern = regexp.MustCompile(
	`^## (?:\[([0-9]+\.[0-9]+\.[0-9]+)\]|([0-9]+\.[0-9]+\.[0-9]+) \([0-9]{4}-[0-9]{2}-[0-9]{2}\))`,
)

// Options names the inputs Check reads. An empty VersionFile derives the
// version from the changelog's newest release section; a non-empty one reads
// that file.
type Options struct {
	// VersionFile holds the version being shipped, such as "VERSION". Empty
	// means the newest changelog release section supplies it.
	VersionFile string
	// Changelog holds the release sections, such as "CHANGELOG.md".
	Changelog string
	// Readme holds the install constraint, such as "README.md".
	Readme string
	// LastTag returns the last git tag. nil means LastTag, which runs
	// `git describe --tags --abbrev=0 --match 'v*'`. Tests inject a tag so the
	// check does not depend on the checkout they run in.
	LastTag func() (string, error)
}

// Report is the outcome of a consistent or inconsistent run. Version is the
// version being shipped: the newest changelog release section, or the version
// file's content. Tag is the last git tag, empty when a version file supplied
// the version or the repository has no tag. State is the reported relationship
// between Version and Tag (StatePending, StateReleased, StateBehind or empty).
// Problems lists every inconsistency, in the order the checks run; an empty
// list means the metadata agrees.
type Report struct {
	Version  string
	Tag      string
	State    string
	Problems []string
}

// LastTag returns the last tag that `git describe` reaches from the working
// directory. A missing git binary, a directory outside a repository, a shallow
// clone without tags and a repository without a `v*` tag are four distinct
// errors, so the CLI can say which one it is. ErrNoTag is the only one Check
// treats as a normal state.
func LastTag() (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", ErrGitMissing
	}

	if _, err := gitOutput("rev-parse", "--is-inside-work-tree"); err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return "", ErrNotARepository
		}
		return "", fmt.Errorf("inspect the git repository: %w", err)
	}

	out, err := gitOutput("describe", "--tags", "--abbrev=0", "--match", "v*")
	if err != nil {
		shallow, shallowErr := gitOutput("rev-parse", "--is-shallow-repository")
		if shallowErr == nil && strings.TrimSpace(shallow) == "true" {
			return "", ErrShallowClone
		}
		return "", ErrNoTag
	}

	tag := strings.TrimSpace(out)
	if tag == "" {
		return "", ErrNoTag
	}

	// `git describe` picks one of several tags that point at the same commit
	// arbitrarily. Refusing the ambiguity is better than reporting whichever
	// name git happened to choose.
	commit, err := gitOutput("rev-list", "-n", "1", tag)
	if err == nil {
		tags, tagsErr := gitOutput("tag", "--points-at", strings.TrimSpace(commit), "--list", "v*")
		if tagsErr == nil {
			if names := strings.Fields(tags); len(names) > 1 {
				return "", fmt.Errorf("%w: %s all point at commit %.7s; delete the one that is not the release",
					ErrAmbiguousTag, strings.Join(names, ", "), strings.TrimSpace(commit))
			}
		}
	}

	return tag, nil
}

// gitOutput runs one git command and returns its standard output. The combined
// output is carried in the error so a classification can read git's own words.
func gitOutput(args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Check verifies that the changelog's newest release section and the README
// install constraint agree. When a version file is supplied, it also verifies
// that the file, the changelog and the README agree, which is the older
// contract.
//
// A reported problem is an inconsistency and belongs in the exit-1 class; an
// error means a required file is missing or unreadable, or the git state cannot
// be inspected, and belongs in the exit-2 class. Every independent
// inconsistency is reported in one run.
func Check(opts Options) (Report, error) {
	changelogPath := orDefault(opts.Changelog, DefaultChangelog)
	readmePath := orDefault(opts.Readme, DefaultReadme)

	changelog, err := readFile(changelogPath)
	if err != nil {
		return Report{}, err
	}
	readme, err := readFile(readmePath)
	if err != nil {
		return Report{}, err
	}

	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	if found := countUnreleased(changelog); found != 1 {
		fail("'%s' must have exactly one '## Unreleased' heading, found %d", changelogPath, found)
	}

	released, hasRelease := newestReleased(changelog)

	if opts.VersionFile != "" {
		return checkVersionFile(opts, changelog, readme, released, hasRelease, problems)
	}

	// Without a version file the newest changelog section is the version. A
	// changelog that holds only `## Unreleased` is a repository before its
	// first release: there is no version and no README constraint to compare
	// yet, which is a normal state rather than a problem. The tag is reporting
	// only: no tag, a tag ahead of the changelog and a tag behind it all keep
	// their verdict from this comparison.
	report := Report{Version: released}
	if hasRelease {
		if !hasDatedSection(changelog, released) {
			fail("the '%s' release section must be dated", released)
		}
		// A section heading that carries no usable X.Y.Z cannot produce an
		// install constraint, so the README comparison is skipped rather than
		// reported against a version nobody wrote.
		if validVersion(released) {
			if constraint := installConstraint(released); !strings.Contains(string(readme), constraint) {
				fail("'%s' must install with %s", readmePath, constraint)
			}
		}
	}

	lastTag := opts.LastTag
	if lastTag == nil {
		lastTag = LastTag
	}
	tag, err := lastTag()
	switch {
	case errors.Is(err, ErrNoTag):
		// A repository without a tag is a normal state. Say so; do not fail.
	case err != nil:
		return Report{}, err
	default:
		tag = strings.TrimSpace(tag)
		if tag != "" {
			report.Tag = tag
			report.State = relationship(report.Version, tag)
		}
	}

	report.Problems = problems
	return report, nil
}

// checkVersionFile is the older contract: the file supplies the version and is
// compared with the changelog's newest section, the dated section and the
// README constraint.
func checkVersionFile(opts Options, changelog, readme []byte, released string, hasRelease bool, problems []string) (Report, error) {
	content, err := readFile(opts.VersionFile)
	if err != nil {
		return Report{}, err
	}
	version := stripWhitespace(content)

	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	valid := validVersion(version)
	if !valid {
		fail("'%s' must hold X.Y.Z without a leading 'v', got '%s'", opts.VersionFile, version)
	}

	if !hasRelease {
		fail("'%s' has no dated release section ('## [X.Y.Z] - YYYY-MM-DD' or '## X.Y.Z (YYYY-MM-DD)')", orDefault(opts.Changelog, DefaultChangelog))
	} else if valid && released != version {
		fail("the newest changelog section is '%s' but '%s' says '%s'", released, opts.VersionFile, version)
	}

	if valid {
		if !hasDatedSection(changelog, version) {
			fail("the '## [%s]' section must be dated '- YYYY-MM-DD'", version)
		}
		if constraint := installConstraint(version); !strings.Contains(string(readme), constraint) {
			fail("'%s' must install with %s", orDefault(opts.Readme, DefaultReadme), constraint)
		}
	}

	return Report{Version: version, Problems: problems}, nil
}

// relationship compares the changelog version with the tag's numeric core. A
// tag without a numeric core (for example `nightly`) has no relationship to
// report; it is still returned in Report.Tag.
func relationship(version, tag string) string {
	match := semverCorePattern.FindStringSubmatch(strings.TrimPrefix(tag, "v"))
	if match == nil || !validVersion(version) {
		return ""
	}
	switch compareSemver(version, match[1]) {
	case 1:
		return StatePending
	case 0:
		return StateReleased
	default:
		return StateBehind
	}
}

// compareSemver compares two X.Y.Z strings and returns -1, 0 or 1.
func compareSemver(left, right string) int {
	a := strings.Split(left, ".")
	b := strings.Split(right, ".")
	for i := 0; i < 3; i++ {
		x, err := strconv.Atoi(a[i])
		if err != nil {
			return 0
		}
		y, err := strconv.Atoi(b[i])
		if err != nil {
			return 0
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// validVersion reports whether version is X.Y.Z without a leading 'v'.
func validVersion(version string) bool {
	return versionPattern.MatchString(version)
}

// readFile reads one required file. A path that is not a regular file is
// reported as not found, as the shell script's `-f` test reported it; a read
// that fails after that keeps the underlying error.
func readFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("'%s' not found", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("'%s': %w", path, err)
	}
	return content, nil
}

// stripWhitespace removes every ASCII whitespace character, as
// `tr -d '[:space:]'` does in the C locale, so a version file with a trailing
// newline reads the same as one without.
func stripWhitespace(content []byte) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\v', '\f', '\r':
			return -1
		default:
			return r
		}
	}, string(content))
}

// countUnreleased counts the lines that are an Unreleased heading.
func countUnreleased(changelog []byte) int {
	count := 0
	for _, line := range lines(changelog) {
		if unreleasedPattern.MatchString(line) {
			count++
		}
	}
	return count
}

// newestReleased returns the version of the first released section heading,
// which is the newest one because the changelog is ordered newest first.
func newestReleased(changelog []byte) (string, bool) {
	for _, line := range lines(changelog) {
		if match := releasedPattern.FindStringSubmatch(line); match != nil {
			if match[1] != "" {
				return match[1], true
			}
			return match[2], true
		}
	}
	return "", false
}

// hasDatedSection reports whether the changelog carries the version's section
// with a date, in either heading shape.
func hasDatedSection(changelog []byte, version string) bool {
	quoted := regexp.QuoteMeta(version)
	bracketed := regexp.MustCompile(`^## \[` + quoted + `\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	parenthesised := regexp.MustCompile(`^## ` + quoted + ` \([0-9]{4}-[0-9]{2}-[0-9]{2}\)$`)
	for _, line := range lines(changelog) {
		if bracketed.MatchString(line) || parenthesised.MatchString(line) {
			return true
		}
	}
	return false
}

// installConstraint is the README text that pins the version's minor, the way
// the install example carries it.
func installConstraint(version string) string {
	minor := version[:strings.LastIndex(version, ".")]
	return fmt.Sprintf("version = \"~> %s\"", minor)
}

// lines splits content into lines without their terminators. A trailing
// newline does not produce an empty final line.
//
// CRLF is deliberately left alone: grep -E's `$` does not match a line that
// ends in '\r', so the shell script this replaces fails the dated-section check
// on a CRLF checkout. Normalising here would pass metadata the script rejected,
// which is exactly the drift a parity replacement must not introduce.
func lines(content []byte) []string {
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}

// orDefault returns value, or fallback when value is empty.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
