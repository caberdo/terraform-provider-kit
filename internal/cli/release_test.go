package cli_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/cli"
)

// releaseChangelog is a changelog cut for 1.2.3.
const releaseChangelog = `# Changelog

## Unreleased

Nothing yet.

## [1.2.3] - 2026-11-14

- Fixed a thing.
`

// releasePending is a release pull request: the newest section is 1.3.0 while
// the last tag is still 1.2.3.
const releasePending = `# Changelog

## Unreleased

## [1.3.0] - 2026-11-16

- Added a thing.

## [1.2.3] - 2026-11-14

- Fixed a thing.
`

// releaseReadme pins the 1.2 minor in its install example.
const releaseReadme = "# terraform-provider-example\n" +
	"\n" +
	"```hcl\n" +
	"terraform {\n" +
	"  required_providers {\n" +
	"    example = {\n" +
	"      version = \"~> 1.2\"\n" +
	"    }\n" +
	"  }\n" +
	"}\n" +
	"```\n"

// releaseReadme13 pins the 1.3 minor.
const releaseReadme13 = "# terraform-provider-example\n" +
	"\n" +
	"```hcl\n" +
	"terraform {\n" +
	"  required_providers {\n" +
	"    example = {\n" +
	"      version = \"~> 1.3\"\n" +
	"    }\n" +
	"  }\n" +
	"}\n" +
	"```\n"

// releaseFiles writes VERSION, CHANGELOG.md and README.md into a fresh
// directory and returns the three paths, in flag order.
func releaseFiles(t *testing.T, version, changelog, readme string) (string, string, string) {
	t.Helper()

	dir := t.TempDir()
	for name, content := range map[string]string{
		"VERSION":      version,
		"CHANGELOG.md": changelog,
		"README.md":    readme,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	return filepath.Join(dir, "VERSION"),
		filepath.Join(dir, "CHANGELOG.md"),
		filepath.Join(dir, "README.md")
}

// releaseArgs builds the flag list for a check against three explicit paths.
func releaseArgs(versionFile, changelog, readme string) []string {
	return []string{
		"release", "check",
		"--version-file", versionFile,
		"--changelog", changelog,
		"--readme", readme,
	}
}

func TestRunReleaseCheckAcceptsConsistentMetadata(t *testing.T) {
	versionFile, changelog, readme := releaseFiles(t, "1.2.3\n", releaseChangelog, releaseReadme)

	var stdout, stderr bytes.Buffer
	code := cli.Run(releaseArgs(versionFile, changelog, readme), testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
	want := "release-check: " + versionFile + "=1.2.3, " + changelog + " and " + readme + " agree\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunReleaseCheckListsEveryProblem(t *testing.T) {
	changelog := strings.Replace(releaseChangelog, "## [1.2.3] - 2026-11-14", "## [1.2.2] - 2026-11-14", 1)
	versionFile, changelogPath, readme := releaseFiles(t, "1.2.3\n", changelog, releaseReadme)

	var stdout, stderr bytes.Buffer
	code := cli.Run(releaseArgs(versionFile, changelogPath, readme), testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	for _, want := range []string{
		"release-check: the newest changelog section is '1.2.2'",
		"release-check: the '## [1.2.3]' section must be dated",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

func TestRunReleaseCheckMissingFileExitsTwo(t *testing.T) {
	versionFile, changelog, readme := releaseFiles(t, "1.2.3\n", releaseChangelog, releaseReadme)
	if err := os.Remove(readme); err != nil {
		t.Fatalf("remove %s: %v", readme, err)
	}

	var stdout, stderr bytes.Buffer
	code := cli.Run(releaseArgs(versionFile, changelog, readme), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if want := "release-check: '" + readme + "' not found"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

func TestRunReleaseCheckRejectsStrayArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := append([]string{"release", "check"}, "extra")

	code := cli.Run(args, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: tfkit release check") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestRunReleaseWithoutSubcommandPrintsUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"missing": {"release"},
		"unknown": {"release", "nope"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := cli.Run(args, testVersion, &stdout, &stderr)

			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "Usage:") || !strings.Contains(stderr.String(), "tfkit release check") {
				t.Errorf("stderr = %q, want the release usage text", stderr.String())
			}
		})
	}
}

func TestRunReleaseCheckHelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"release", "check", "--help"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"--version-file", "last git tag", "newest"} {
		if !strings.Contains(strings.ToLower(stderr.String()), strings.ToLower(want)) {
			t.Errorf("stderr = %q, want it to mention %q", stderr.String(), want)
		}
	}
}

// releaseRepo writes a git repository holding the changelog and README, commits
// them and creates tag when it is non-empty. The version file is deliberately
// absent: the repository carries its version as the tag.
func releaseRepo(t *testing.T, tag, changelog, readme string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range map[string]string{
		"CHANGELOG.md": changelog,
		"README.md":    readme,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	run := func(args ...string) {
		t.Helper()
		// The developer's global config may sign tags and commits; a fixture
		// must not need a GPG key or an editor.
		args = append([]string{
			"-c", "commit.gpgsign=false",
			"-c", "tag.gpgsign=false",
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
	run("init", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "fixture")
	if tag != "" {
		run("tag", tag)
	}
	return dir
}

// TestRunReleaseCheckReleasePullRequestExitsZero is the regression that made
// the check useless as a gate: a release pull request is exactly the commit
// that moves the changelog ahead of the last tag.
func TestRunReleaseCheckReleasePullRequestExitsZero(t *testing.T) {
	dir := releaseRepo(t, "v1.2.3", releasePending, releaseReadme13)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
	want := "release-check: 1.3.0, CHANGELOG.md and README.md agree (pending release; last tag v1.2.3)\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunReleaseCheckReleasedStateExitsZero(t *testing.T) {
	dir := releaseRepo(t, "v1.2.3", releaseChangelog, releaseReadme)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	want := "release-check: 1.2.3, CHANGELOG.md and README.md agree (released; last tag v1.2.3)\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunReleaseCheckBehindTheTagSaysWhatIsTrue(t *testing.T) {
	// The changelog and README agree with each other; the tag is newer than
	// the changelog. The line must not claim the repository agrees with a tag
	// it is behind.
	dir := releaseRepo(t, "v1.3.0", releaseChangelog, releaseReadme)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	want := "release-check: 1.2.3, CHANGELOG.md and README.md agree (the last tag v1.3.0 is newer than the changelog)\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunReleaseCheckShallowCloneWarnsAndExitsZero(t *testing.T) {
	// A shallow checkout has no tags; the tag is reporting only, so the gate
	// still decides from the changelog and README.
	source := releaseRepo(t, "v1.2.3", releasePending, releaseReadme13)
	clone := filepath.Join(t.TempDir(), "clone")
	cmd := exec.CommandContext(t.Context(), "git", "clone", "-q", "--depth", "1", "--no-tags", "file://"+source, clone)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v (%s)", err, out)
	}
	t.Chdir(clone)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if want := "warning: shallow clone"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want a warning containing %q", stderr.String(), want)
	}
	if want := "1.3.0, CHANGELOG.md and README.md agree (no tag yet)"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
	}
}

func TestRunReleaseCheckChangelogReadmeDisagreementExitsOne(t *testing.T) {
	dir := releaseRepo(t, "v1.2.3", releasePending, releaseReadme)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check"}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if want := `must install with version = "~> 1.3"`; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

func TestRunReleaseCheckWithoutATagExitsZero(t *testing.T) {
	dir := releaseRepo(t, "", releasePending, releaseReadme13)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	want := "release-check: 1.3.0, CHANGELOG.md and README.md agree (no tag yet)\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunReleaseCheckNotARepositoryExitsTwo(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"CHANGELOG.md": releasePending,
		"README.md":    releaseReadme13,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check"}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if want := "not a git repository"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to say %q", stderr.String(), want)
	}
}

func TestRunReleaseCheckLegacyVersionFileExitsTwo(t *testing.T) {
	dir := releaseRepo(t, "v1.2.3", releaseChangelog, releaseReadme)
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.2.3\n"), 0o600); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check"}, testVersion, &stdout, &stderr)

	// A botched migration must fail loudly instead of restoring the old
	// contract: the flag is the only way to say "read a version file".
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	for _, want := range []string{"legacy", "VERSION", "--version-file"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to mention %q", stderr.String(), want)
		}
	}
}

func TestRunReleaseCheckExplicitMissingVersionFileExitsTwo(t *testing.T) {
	dir := releaseRepo(t, "v1.2.3", releaseChangelog, releaseReadme)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check", "--version-file", "VERSION"}, testVersion, &stdout, &stderr)

	// An explicit path is an instruction, not a hint: falling back to the tag
	// would validate something other than what the caller asked about.
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if want := "release-check: 'VERSION' not found"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
	}
}

func TestRunReleaseCheckExplicitVersionFileKeepsTheOldContract(t *testing.T) {
	// An explicitly passed version file still supplies the version, even when
	// it disagrees with the tag: that behaviour is unchanged.
	dir := releaseRepo(t, "v1.2.4", releaseChangelog, releaseReadme)
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.2.3\n"), 0o600); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check", "--version-file", "VERSION"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	want := "release-check: VERSION=1.2.3, CHANGELOG.md and README.md agree\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunReleaseCheckEmptyVersionFileFlagDerivesFromTheChangelog(t *testing.T) {
	dir := releaseRepo(t, "v1.2.3", releasePending, releaseReadme13)
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.2.2\n"), 0o600); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"release", "check", "--version-file", ""}, testVersion, &stdout, &stderr)

	// An explicit empty value opts out of the legacy file without tripping the
	// migration error.
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if want := "(pending release; last tag v1.2.3)"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
	}
}
