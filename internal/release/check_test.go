package release_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/release"
)

// changelogCut is a changelog cut for 1.2.3: one Unreleased heading, the newest
// release section dated, and an older section below it.
const changelogCut = `# Changelog

All notable changes are recorded here.

## Unreleased

Nothing yet.

## [1.2.3] - 2026-11-14

- Fixed a thing.

## [1.2.2] - 2026-10-01

- Earlier.
`

// changelogPending is a release pull request: the changelog's newest section
// is 1.3.0 while the last tag is still 1.2.3.
const changelogPending = `# Changelog

## Unreleased

## [1.3.0] - 2026-11-16

- Added a thing.

## [1.2.3] - 2026-11-14

- Fixed a thing.
`

// changelogParenthesised is changelogPending in rustfs's assembler shape:
// `## X.Y.Z (YYYY-MM-DD)` instead of `## [X.Y.Z] - YYYY-MM-DD`.
const changelogParenthesised = `# Changelog

## Unreleased

## 1.3.0 (2026-11-16)

- Added a thing.

## 1.2.3 (2026-11-14)

- Fixed a thing.
`

// readmeMinor returns a README whose install example pins the minor of
// version.
func readmeMinor(version string) string {
	minor := version[:strings.LastIndex(version, ".")]
	return "# terraform-provider-example\n" +
		"\n" +
		"```hcl\n" +
		"terraform {\n" +
		"  required_providers {\n" +
		"    example = {\n" +
		"      source  = \"caberdo/example\"\n" +
		"      version = \"~> " + minor + "\"\n" +
		"    }\n" +
		"  }\n" +
		"}\n" +
		"```\n"
}

// readmeInstall is readmeMinor for the 1.2 minor.
var readmeInstall = readmeMinor("1.2.3")

// metadata writes the three release files into a fresh directory and returns
// the options that point at them.
func metadata(t *testing.T, version, changelog, readme string) release.Options {
	t.Helper()

	dir := t.TempDir()
	opts := release.Options{
		VersionFile: filepath.Join(dir, "VERSION"),
		Changelog:   filepath.Join(dir, "CHANGELOG.md"),
		Readme:      filepath.Join(dir, "README.md"),
	}
	for path, content := range map[string]string{
		opts.VersionFile: version,
		opts.Changelog:   changelog,
		opts.Readme:      readme,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return opts
}

// tagMetadata builds options that derive the version from the changelog and
// report the tag, instead of reading a version file.
func tagMetadata(t *testing.T, tag string, tagErr error, changelog, readme string) release.Options {
	t.Helper()

	dir := t.TempDir()
	opts := release.Options{
		Changelog: filepath.Join(dir, "CHANGELOG.md"),
		Readme:    filepath.Join(dir, "README.md"),
		LastTag:   func() (string, error) { return tag, tagErr },
	}
	for path, content := range map[string]string{
		opts.Changelog: changelog,
		opts.Readme:    readme,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return opts
}

// checkProbs runs Check and fails the test when it returns an error. Problems
// are exit 1, not an error: only an unreadable file is an error.
func checkProbs(t *testing.T, opts release.Options) release.Report {
	t.Helper()

	report, err := release.Check(opts)
	if err != nil {
		t.Fatalf("Check returned an error, want reported problems: %v", err)
	}
	return report
}

// wantProblem fails the test unless one problem contains the wanted text.
func wantProblem(t *testing.T, report release.Report, want string) {
	t.Helper()

	for _, problem := range report.Problems {
		if strings.Contains(problem, want) {
			return
		}
	}
	t.Errorf("problems = %q, want one containing %q", report.Problems, want)
}

func TestCheckAcceptsConsistentMetadata(t *testing.T) {
	opts := metadata(t, "1.2.3\n", changelogCut, readmeInstall)

	report := checkProbs(t, opts)

	if got, want := report.Version, "1.2.3"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none", report.Problems)
	}
}

func TestCheckAcceptsVersionFileWithSurroundingWhitespace(t *testing.T) {
	opts := metadata(t, " 1.2.3 \n\n", changelogCut, readmeInstall)

	report := checkProbs(t, opts)

	if got, want := report.Version, "1.2.3"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none", report.Problems)
	}
}

func TestCheckRejectsChangelogWithoutTheVersionSection(t *testing.T) {
	// The changelog was not cut for 1.2.3: its newest section is still 1.2.2.
	changelog := strings.Replace(changelogCut, "## [1.2.3] - 2026-11-14", "## [1.2.2] - 2026-11-14", 1)
	opts := metadata(t, "1.2.3\n", changelog, readmeInstall)

	report := checkProbs(t, opts)

	if len(report.Problems) == 0 {
		t.Fatal("problems = none, want the missing changelog section reported")
	}
	wantProblem(t, report, fmt.Sprintf("the newest changelog section is '1.2.2' but '%s' says '1.2.3'", opts.VersionFile))
}

func TestCheckRejectsVersionThatIsNotSemver(t *testing.T) {
	for _, version := range []string{"v1.2.3", "1.2", "1.2.3-rc1", ""} {
		t.Run(version, func(t *testing.T) {
			opts := metadata(t, version, changelogCut, readmeInstall)

			report := checkProbs(t, opts)

			if len(report.Problems) == 0 {
				t.Fatal("problems = none, want the version rejected")
			}
			wantProblem(t, report, fmt.Sprintf("'%s' must hold X.Y.Z without a leading 'v', got '%s'", opts.VersionFile, strings.TrimSpace(version)))
		})
	}
}

func TestCheckRejectsReadmeConstraintMismatch(t *testing.T) {
	opts := metadata(t, "1.2.3\n", changelogCut, strings.Replace(readmeInstall, `~> 1.2`, `~> 1.1`, 1))

	report := checkProbs(t, opts)

	if len(report.Problems) == 0 {
		t.Fatal("problems = none, want the README constraint reported")
	}
	wantProblem(t, report, fmt.Sprintf(`'%s' must install with version = "~> 1.2"`, opts.Readme))
}

func TestCheckFailsClosedOnMissingFile(t *testing.T) {
	opts := metadata(t, "1.2.3\n", changelogCut, readmeInstall)
	if err := os.Remove(opts.Readme); err != nil {
		t.Fatalf("remove %s: %v", opts.Readme, err)
	}

	_, err := release.Check(opts)

	if err == nil {
		t.Fatal("Check returned nil, want an error for the missing file")
	}
	if !strings.Contains(err.Error(), opts.Readme) {
		t.Errorf("error = %q, want it to name %q", err, opts.Readme)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to say the file is not found", err)
	}
}

func TestCheckRejectsMissingUnreleasedHeading(t *testing.T) {
	for name, changelog := range map[string]string{
		"none": strings.Replace(changelogCut, "## Unreleased\n", "", 1),
		"two":  strings.Replace(changelogCut, "## Unreleased\n", "## Unreleased\n\n## Unreleased\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			opts := metadata(t, "1.2.3\n", changelog, readmeInstall)

			report := checkProbs(t, opts)

			want := "must have exactly one '## Unreleased' heading"
			if name == "two" {
				wantProblem(t, report, want+", found 2")
				return
			}
			wantProblem(t, report, want)
		})
	}
}

func TestCheckRejectsChangelogWithoutAnyReleaseSection(t *testing.T) {
	changelog := "# Changelog\n\n## Unreleased\n\nNothing yet.\n"
	opts := metadata(t, "1.2.3\n", changelog, readmeInstall)

	report := checkProbs(t, opts)

	wantProblem(t, report, fmt.Sprintf("'%s' has no dated release section", opts.Changelog))
}

func TestCheckRejectsUndatedChangelogSection(t *testing.T) {
	changelog := strings.Replace(changelogCut, "## [1.2.3] - 2026-11-14", "## [1.2.3]", 1)
	opts := metadata(t, "1.2.3\n", changelog, readmeInstall)

	report := checkProbs(t, opts)

	wantProblem(t, report, "the '## [1.2.3]' section must be dated '- YYYY-MM-DD'")
}

func TestCheckTreatsACRLFChangelogAsUndated(t *testing.T) {
	// grep -E's '$' does not match a line that ends in '\r', so the shell
	// script rejects a CRLF changelog at the dated-section check. Passing it
	// here would be metadata the script failed, which parity forbids.
	changelog := strings.ReplaceAll(changelogCut, "\n", "\r\n")
	opts := metadata(t, "1.2.3\r\n", changelog, strings.ReplaceAll(readmeInstall, "\n", "\r\n"))

	report := checkProbs(t, opts)

	wantProblem(t, report, "the '## [1.2.3]' section must be dated '- YYYY-MM-DD'")
}

func TestCheckReportsEveryProblemInOneRun(t *testing.T) {
	// A release pull request that bumped the README but nothing else: the
	// changelog is uncut, its headings are wrong and the install constraint is
	// stale. One run must name all of them, so the pull request is fixed in one
	// pass.
	changelog := strings.Replace(changelogCut, "## [1.2.3] - 2026-11-14", "## [1.2.2] - 2026-11-14", 1)
	changelog = strings.Replace(changelog, "## Unreleased\n", "## Unreleased\n\n## Unreleased\n", 1)
	opts := metadata(t, "1.2.3\n", changelog, strings.Replace(readmeInstall, `~> 1.2`, `~> 1.1`, 1))

	report := checkProbs(t, opts)

	wantProblem(t, report, "must have exactly one '## Unreleased' heading")
	wantProblem(t, report, "the newest changelog section is '1.2.2'")
	wantProblem(t, report, "the '## [1.2.3]' section must be dated")
	wantProblem(t, report, `must install with version = "~> 1.2"`)
}

func TestCheckDefaultsToTheRepositoryFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	for name, content := range map[string]string{
		"CHANGELOG.md": changelogCut,
		"README.md":    readmeInstall,
	} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// The version has no default file: an empty VersionFile derives it from the
	// changelog and reports the tag beside it, so the tag is the input this
	// fixture supplies.
	report, err := release.Check(release.Options{
		LastTag: func() (string, error) { return "v1.2.3", nil },
	})
	if err != nil {
		t.Fatalf("Check returned an error: %v", err)
	}

	if got, want := report.Version, "1.2.3"; got != want {
		t.Errorf("version = %q, want %q", got, want)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none", report.Problems)
	}
}

// TestCheckReleasePullRequestIsPendingNotAFailure is the regression the tag
// gate caused: a release pull request moves the changelog ahead of the last
// tag, so the last tag must not be an input to the verdict.
func TestCheckReleasePullRequestIsPendingNotAFailure(t *testing.T) {
	report := checkProbs(t, tagMetadata(t, "v1.2.3", nil, changelogPending, readmeMinor("1.3.0")))

	if got, want := report.Version, "1.3.0"; got != want {
		t.Errorf("version = %q, want %q (the changelog's newest section)", got, want)
	}
	if got, want := report.Tag, "v1.2.3"; got != want {
		t.Errorf("tag = %q, want %q", got, want)
	}
	if got, want := report.State, release.StatePending; got != want {
		t.Errorf("state = %q, want %q", got, want)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none: a release pull request is the normal state", report.Problems)
	}
}

func TestCheckReleasedState(t *testing.T) {
	report := checkProbs(t, tagMetadata(t, "v1.2.3", nil, changelogCut, readmeInstall))

	if got, want := report.State, release.StateReleased; got != want {
		t.Errorf("state = %q, want %q", got, want)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none", report.Problems)
	}
}

func TestCheckRejectsChangelogReadmeDisagreement(t *testing.T) {
	// The changelog is cut for 1.3.0 but the README still installs 1.2.
	report := checkProbs(t, tagMetadata(t, "v1.2.3", nil, changelogPending, readmeInstall))

	wantProblem(t, report, `must install with version = "~> 1.3"`)
}

func TestCheckAcceptsBothChangelogHeadingShapes(t *testing.T) {
	for name, changelog := range map[string]string{
		"bracketed":     changelogPending,
		"parenthesised": changelogParenthesised,
	} {
		t.Run(name, func(t *testing.T) {
			report := checkProbs(t, tagMetadata(t, "v1.2.3", nil, changelog, readmeMinor("1.3.0")))

			if len(report.Problems) != 0 {
				t.Errorf("problems = %q, want none for the %s heading", report.Problems, name)
			}
			if got, want := report.Version, "1.3.0"; got != want {
				t.Errorf("version = %q, want %q", got, want)
			}
		})
	}
}

func TestCheckWithoutATagPasses(t *testing.T) {
	// A repository with no tag is a normal state; the tag is reporting only.
	report := checkProbs(t, tagMetadata(t, "", release.ErrNoTag, changelogPending, readmeMinor("1.3.0")))

	if got, want := report.Tag, ""; got != want {
		t.Errorf("tag = %q, want empty", got)
	}
	if got, want := report.State, ""; got != want {
		t.Errorf("state = %q, want empty", got)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none", report.Problems)
	}
}

func TestCheckChangelogBehindTheTagIsReportedNotFailed(t *testing.T) {
	// The tag names 1.3.0 but the changelog's newest section is 1.2.3. This is
	// not the release pull request state, but R45 keeps the tag out of the
	// verdict; the direction is reported instead.
	report := checkProbs(t, tagMetadata(t, "v1.3.0", nil, changelogCut, readmeInstall))

	if got, want := report.State, release.StateBehind; got != want {
		t.Errorf("state = %q, want %q", got, want)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none", report.Problems)
	}
}

func TestCheckTreatsAChangelogWithoutAReleaseSectionAsPreRelease(t *testing.T) {
	// A scaffolded repository starts with an empty Unreleased section. Before
	// the first release there is no version and no README constraint to
	// compare, so the gate has nothing to reject.
	changelog := "# Changelog\n\n## Unreleased\n"
	report := checkProbs(t, tagMetadata(t, "", release.ErrNoTag, changelog, readmeInstall))

	if got, want := report.Version, ""; got != want {
		t.Errorf("version = %q, want empty before the first release", got)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none", report.Problems)
	}
}

func TestCheckAcceptsAPrereleaseTagWithoutCrashing(t *testing.T) {
	report := checkProbs(t, tagMetadata(t, "v1.3.0-rc1", nil, changelogPending, readmeMinor("1.3.0")))

	if got, want := report.Tag, "v1.3.0-rc1"; got != want {
		t.Errorf("tag = %q, want %q", got, want)
	}
	if len(report.Problems) != 0 {
		t.Errorf("problems = %q, want none", report.Problems)
	}
}

// gitRun runs git in dir with signing disabled, so a developer's global config
// cannot open an editor or need a GPG key in a fixture.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()

	args = append([]string{
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "tag.forceSignAnnotated=false",
	}, args...)
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture",
		"GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture",
		"GIT_COMMITTER_EMAIL=fixture@example.com",
		"GIT_EDITOR=true",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

// gitRepo creates a repository in a fresh directory, writes CHANGELOG.md and
// README.md, and commits them.
func gitRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range map[string]string{
		"CHANGELOG.md": changelogPending,
		"README.md":    readmeMinor("1.3.0"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "fixture")
	return dir
}

func TestLastTagClassifiesGitFailures(t *testing.T) {
	t.Run("git missing", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := release.LastTag()
		if !errors.Is(err, release.ErrGitMissing) {
			t.Fatalf("error = %v, want ErrGitMissing", err)
		}
	})

	t.Run("not a repository", func(t *testing.T) {
		t.Chdir(t.TempDir())
		_, err := release.LastTag()
		if !errors.Is(err, release.ErrNotARepository) {
			t.Fatalf("error = %v, want ErrNotARepository", err)
		}
	})

	t.Run("no tag", func(t *testing.T) {
		t.Chdir(gitRepo(t))
		_, err := release.LastTag()
		if !errors.Is(err, release.ErrNoTag) {
			t.Fatalf("error = %v, want ErrNoTag", err)
		}
	})

	t.Run("shallow clone", func(t *testing.T) {
		source := gitRepo(t)
		gitRun(t, source, "tag", "v1.2.3")
		clone := filepath.Join(t.TempDir(), "clone")
		cmd := exec.CommandContext(t.Context(), "git", "clone", "-q", "--depth", "1", "--no-tags", "file://"+source, clone)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git clone: %v (%s)", err, out)
		}
		t.Chdir(clone)
		_, err := release.LastTag()
		if !errors.Is(err, release.ErrShallowClone) {
			t.Fatalf("error = %v, want ErrShallowClone", err)
		}
	})
}

func TestLastTagSelectsMatchingTags(t *testing.T) {
	t.Run("ignores tags that do not match v*", func(t *testing.T) {
		dir := gitRepo(t)
		// A non-release tag alone is not a release tag.
		gitRun(t, dir, "tag", "nightly")
		t.Chdir(dir)
		_, err := release.LastTag()
		if !errors.Is(err, release.ErrNoTag) {
			t.Fatalf("error = %v, want ErrNoTag", err)
		}
	})

	t.Run("rejects a same-commit tag pair", func(t *testing.T) {
		dir := gitRepo(t)
		for _, tag := range []string{"v1.2.3", "v1.2.4"} {
			gitRun(t, dir, "tag", tag)
		}
		t.Chdir(dir)
		_, err := release.LastTag()
		if !errors.Is(err, release.ErrAmbiguousTag) {
			t.Fatalf("error = %v, want ErrAmbiguousTag", err)
		}
		for _, tag := range []string{"v1.2.3", "v1.2.4"} {
			if !strings.Contains(err.Error(), tag) {
				t.Errorf("error = %q, want it to name %q", err, tag)
			}
		}
	})

	t.Run("describes the nearest v* tag", func(t *testing.T) {
		dir := gitRepo(t)
		for _, tag := range []string{"v1.2.3", "nightly"} {
			gitRun(t, dir, "tag", tag)
		}
		t.Chdir(dir)
		tag, err := release.LastTag()
		if err != nil {
			t.Fatalf("LastTag: %v", err)
		}
		if got, want := tag, "v1.2.3"; got != want {
			t.Errorf("tag = %q, want %q", got, want)
		}
	})
}

func TestCheckPropagatesAGitFailure(t *testing.T) {
	opts := tagMetadata(t, "", release.ErrShallowClone, changelogPending, readmeMinor("1.3.0"))

	_, err := release.Check(opts)

	if !errors.Is(err, release.ErrShallowClone) {
		t.Fatalf("error = %v, want ErrShallowClone", err)
	}
}
