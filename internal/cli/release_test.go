package cli_test

import (
	"bytes"
	"os"
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
	if !strings.Contains(stderr.String(), "--version-file") {
		t.Errorf("stderr = %q, want the flag documentation", stderr.String())
	}
}
