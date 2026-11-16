// Package release validates the release metadata a release pull request
// carries: the version being shipped, the changelog section that documents it
// and the README install constraint.
//
// It replaces scripts/release-check.sh. The released version is the last git
// tag, not a file the repository carries, so a repository without VERSION still
// has one version to check: the tag that was cut last. Supplying VersionFile
// keeps the older flow, because a repository mid-migration may still hold one.
//
// Check returns the problems it finds instead of stopping at the first, so a
// broken release pull request is fixed in one pass. The shell script stopped at
// the first problem; every check below runs unless its input is unusable, which
// is a deliberate difference. An unusable file is an error rather than a
// problem, and the CLI maps it to exit code 2, as the script did.
package release

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Default paths, used when Options leaves a field empty.
const (
	// DefaultVersionFile is the file tfkit probes when --version-file was not
	// given. It is not a default for Check: an empty Options.VersionFile means
	// the version comes from the last tag. The CLI keeps reading VERSION when a
	// repository still has one, so the migration needs no flag.
	DefaultVersionFile = "VERSION"
	// DefaultChangelog is the changelog beside the working directory.
	DefaultChangelog = "CHANGELOG.md"
	// DefaultReadme is the README beside the working directory.
	DefaultReadme = "README.md"
)

// versionPattern is the only acceptable content of the version file: X.Y.Z
// without a leading 'v', as the shell script's `^[0-9]+\.[0-9]+\.[0-9]+$`.
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// unreleasedPattern is the heading the next release accumulates under. Exactly
// one must survive cutting a release.
var unreleasedPattern = regexp.MustCompile(`^## Unreleased[[:space:]]*$`)

// releasedPattern finds a released section heading and captures its version.
var releasedPattern = regexp.MustCompile(`^## \[([0-9]+\.[0-9]+\.[0-9]+)\]`)

// Options names the inputs Check reads. An empty VersionFile derives the
// version from the last git tag; a non-empty one reads that file.
type Options struct {
	// VersionFile holds the version being shipped, such as "VERSION". Empty
	// means the last git tag supplies it.
	VersionFile string
	// Changelog holds the release sections, such as "CHANGELOG.md".
	Changelog string
	// Readme holds the install constraint, such as "README.md".
	Readme string
	// LastTag returns the last git tag. nil means LastTag, which runs
	// `git describe --tags --abbrev=0`. Tests inject a tag so the check does
	// not depend on the checkout they run in.
	LastTag func() (string, error)
}

// Report is the outcome of a consistent or inconsistent run. Version is the
// version being shipped, with a leading 'v' removed when a tag supplied it.
// Tag is the last git tag the version was derived from, empty when a version
// file supplied it. Problems lists every inconsistency, in the order the checks
// run; an empty list means the metadata agrees.
type Report struct {
	Version  string
	Tag      string
	Problems []string
}

// LastTag returns the last tag that `git describe` reaches from the working
// directory. A repository with no tag, a missing git binary and a directory
// outside a repository are all the same unusable-input class: error, which the
// CLI reports as exit code 2.
func LastTag() (string, error) {
	out, err := exec.CommandContext(context.Background(), "git", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return "", fmt.Errorf("no git tag found: %w", err)
	}
	tag := strings.TrimSpace(string(out))
	if tag == "" {
		return "", fmt.Errorf("no git tag found: git describe returned an empty tag")
	}
	return tag, nil
}

// Check verifies that the shipped version, the changelog and the README agree.
//
// A reported problem is an inconsistency and belongs in the exit-1 class; an
// error means a required file is missing or unreadable, or no tag exists, and
// belongs in the exit-2 class. Every independent inconsistency is reported in
// one run.
func Check(opts Options) (Report, error) {
	changelogPath := orDefault(opts.Changelog, DefaultChangelog)
	readmePath := orDefault(opts.Readme, DefaultReadme)

	var version, tag string
	if opts.VersionFile == "" {
		lastTag := opts.LastTag
		if lastTag == nil {
			lastTag = LastTag
		}
		found, err := lastTag()
		if err != nil {
			return Report{}, err
		}
		tag = strings.TrimSpace(found)
		if tag == "" {
			return Report{}, fmt.Errorf("no git tag found")
		}
		// The release tags carry a 'v' prefix; the changelog, the README
		// constraint and the version file do not.
		version = strings.TrimPrefix(tag, "v")
	} else {
		content, err := readFile(opts.VersionFile)
		if err != nil {
			return Report{}, err
		}
		version = stripWhitespace(content)
	}

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

	// Checks whose input is unusable are skipped rather than reported against
	// it: a version that is not X.Y.Z cannot also be compared with a changelog
	// section or turned into a README constraint, and the noise would bury the
	// one thing that needs fixing.
	validVersion := versionPattern.MatchString(version)
	if !validVersion {
		if tag != "" {
			fail("the last git tag '%s' must name X.Y.Z without a leading 'v', got '%s'", tag, version)
		} else {
			fail("'%s' must hold X.Y.Z without a leading 'v', got '%s'", opts.VersionFile, version)
		}
	}

	if found := countUnreleased(changelog); found != 1 {
		fail("'%s' must have exactly one '## Unreleased' heading, found %d", changelogPath, found)
	}

	released, hasRelease := newestReleased(changelog)
	switch {
	case !hasRelease:
		fail("'%s' has no '## [X.Y.Z]' release section", changelogPath)
	case validVersion && released != version:
		if tag != "" {
			fail("the newest changelog section is '%s' but the last git tag is '%s', which names '%s'",
				released, tag, version)
		} else {
			fail("the newest changelog section is '%s' but '%s' says '%s'",
				released, opts.VersionFile, version)
		}
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

	return Report{Version: version, Tag: tag, Problems: problems}, nil
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
