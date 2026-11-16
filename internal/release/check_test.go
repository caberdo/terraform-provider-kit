package release_test

import (
	"fmt"
	"os"
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

// readmeInstall is a README whose install example pins the 1.2 minor.
const readmeInstall = "# terraform-provider-example\n" +
	"\n" +
	"```hcl\n" +
	"terraform {\n" +
	"  required_providers {\n" +
	"    example = {\n" +
	"      source  = \"caberdo/example\"\n" +
	"      version = \"~> 1.2\"\n" +
	"    }\n" +
	"  }\n" +
	"}\n" +
	"```\n"

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

	wantProblem(t, report, fmt.Sprintf("'%s' has no '## [X.Y.Z]' release section", opts.Changelog))
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
		"VERSION":      "1.2.3\n",
		"CHANGELOG.md": changelogCut,
		"README.md":    readmeInstall,
	} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	report, err := release.Check(release.Options{})
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
