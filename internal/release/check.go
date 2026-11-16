// Package release validates the release metadata a release pull request
// carries: the version in VERSION, the changelog section that documents it and
// the README install constraint.
//
// It replaces scripts/release-check.sh. The release pull request is the only
// change that moves VERSION, and merging it starts the tag workflow, so the
// three files disagreeing is caught before the merge instead of after it.
//
// Check returns the problems it finds instead of stopping at the first, so a
// broken release pull request is fixed in one pass. The shell script stopped at
// the first problem; every check below runs unless its input is unusable, which
// is a deliberate difference. An unusable file is an error rather than a
// problem, and the CLI maps it to exit code 2, as the script did.
package release

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Default paths, used when Options leaves a field empty.
const (
	DefaultVersionFile = "VERSION"
	DefaultChangelog   = "CHANGELOG.md"
	DefaultReadme      = "README.md"
)

// versionPattern is the only acceptable content of the version file: X.Y.Z
// without a leading 'v', as the shell script's `^[0-9]+\.[0-9]+\.[0-9]+$`.
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// unreleasedPattern is the heading the next release accumulates under. Exactly
// one must survive cutting a release.
var unreleasedPattern = regexp.MustCompile(`^## Unreleased[[:space:]]*$`)

// releasedPattern finds a released section heading and captures its version.
var releasedPattern = regexp.MustCompile(`^## \[([0-9]+\.[0-9]+\.[0-9]+)\]`)

// Options names the three files Check reads. An empty field means the default
// path beside the working directory.
type Options struct {
	// VersionFile holds the version being shipped, such as "VERSION".
	VersionFile string
	// Changelog holds the release sections, such as "CHANGELOG.md".
	Changelog string
	// Readme holds the install constraint, such as "README.md".
	Readme string
}

// Report is the outcome of a consistent or inconsistent run. Version is the
// version the file held with its whitespace stripped. Problems lists every
// inconsistency, in the order the checks run; an empty list means the metadata
// agrees.
type Report struct {
	Version  string
	Problems []string
}

// Check verifies that the version file, the changelog and the README agree.
//
// A reported problem is an inconsistency and belongs in the exit-1 class; an
// error means a required file is missing or unreadable and belongs in the
// exit-2 class. Every independent inconsistency is reported in one run.
func Check(opts Options) (Report, error) {
	versionFile := orDefault(opts.VersionFile, DefaultVersionFile)
	changelogPath := orDefault(opts.Changelog, DefaultChangelog)
	readmePath := orDefault(opts.Readme, DefaultReadme)

	// All three files are read before anything is checked, as the shell
	// script's `-f` guard did: nothing can be compared without all of them, and
	// a missing file is the exit-2 class rather than a reported problem.
	versionContent, err := readFile(versionFile)
	if err != nil {
		return Report{}, err
	}
	changelog, err := readFile(changelogPath)
	if err != nil {
		return Report{}, err
	}
	readme, err := readFile(readmePath)
	if err != nil {
		return Report{}, err
	}

	version := stripWhitespace(versionContent)
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	// Checks whose input is unusable are skipped rather than reported against
	// it: a version that is not X.Y.Z cannot also be compared with a changelog
	// section or turned into a README constraint, and the noise would bury the
	// one thing that needs fixing.
	validVersion := versionPattern.MatchString(version)
	if !validVersion {
		fail("'%s' must hold X.Y.Z without a leading 'v', got '%s'", versionFile, version)
	}

	if found := countUnreleased(changelog); found != 1 {
		fail("'%s' must have exactly one '## Unreleased' heading, found %d", changelogPath, found)
	}

	released, hasRelease := newestReleased(changelog)
	switch {
	case !hasRelease:
		fail("'%s' has no '## [X.Y.Z]' release section", changelogPath)
	case validVersion && released != version:
		fail("the newest changelog section is '%s' but '%s' says '%s'",
			released, versionFile, version)
	}

	if validVersion {
		if !hasDatedSection(changelog, version) {
			fail("the '## [%s]' section must be dated '- YYYY-MM-DD'", version)
		}
		if constraint := installConstraint(version); !strings.Contains(string(readme), constraint) {
			// A patch release keeps the current minor's constraint; a minor
			// release moves it, which is why the expected value is derived from
			// the version instead of hard-coded.
			fail("'%s' must install with %s", readmePath, constraint)
		}
	}

	return Report{Version: version, Problems: problems}, nil
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
			return match[1], true
		}
	}
	return "", false
}

// hasDatedSection reports whether the changelog carries the version's section
// with a '- YYYY-MM-DD' date on the heading itself.
func hasDatedSection(changelog []byte, version string) bool {
	dated := regexp.MustCompile(`^## \[` + regexp.QuoteMeta(version) + `\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	for _, line := range lines(changelog) {
		if dated.MatchString(line) {
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
