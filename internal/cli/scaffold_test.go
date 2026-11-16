package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/cli"
	"github.com/caberdo/terraform-provider-kit/internal/scaffold"
)

// scaffoldKitVersion is the pin the aligned fixture uses.
const scaffoldKitVersion = "v0.9.0"

// scaffoldFixtureValues are the values the CLI derives for a repository checked
// out as terraform-provider-trino with this go.mod. They are spelled out rather
// than built by the code under test, so a derivation change breaks the test.
func scaffoldFixtureValues() map[string]string {
	return map[string]string{
		"PROVIDER":           "trino",
		"PRODUCT":            "Trino",
		"MODULE":             "github.com/caberdo/terraform-provider-trino",
		"REGISTRY_NAMESPACE": "caberdo",
		"YEAR":               "2026",
		"COPYRIGHT":          "Dennis de Jong",
		"KIT_VERSION":        scaffoldKitVersion,
		"PACKAGES":           "./internal/...",
		"VERSIONS_FILE":      ".github/trino-versions.txt",
		"UPSTREAM_URL":       "trinodb/trino",
		"VERSION_ENV":        "TRINO_VERSION",
		"EXPECT_VERSION_ENV": "TRINO_EXPECT_VERSION",
	}
}

// alignedScaffoldRepo writes the rendered scaffold into a directory named
// terraform-provider-trino, with a go.mod the CLI can read.
func alignedScaffoldRepo(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "terraform-provider-trino")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	rendered, err := scaffold.Render(filepath.Join("..", "..", "scaffold"), scaffoldFixtureValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for path, content := range rendered {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	goMod := "module github.com/caberdo/terraform-provider-trino\n\ngo 1.27.4\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

// scaffoldCheckArgs are the flags the fixture needs; provider, module,
// packages and versions-file are left to the derivation.
func scaffoldCheckArgs(extra ...string) []string {
	args := []string{
		"scaffold", "check",
		"--product", "Trino",
		"--year", "2026",
		"--copyright", "Dennis de Jong",
		"--kit-version", scaffoldKitVersion,
		"--upstream", "trinodb/trino",
	}
	return append(args, extra...)
}

// editRepoFile appends text to a repository file.
func editRepoFile(t *testing.T, dir, path, addition string) {
	t.Helper()

	full := filepath.Join(dir, path)
	content, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(full, append(content, []byte(addition)...), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// replaceInRepoFile replaces one substring in a repository file.
func replaceInRepoFile(t *testing.T, dir, path, old, replacement string) {
	t.Helper()

	full := filepath.Join(dir, path)
	content, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(content), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	changed := strings.Replace(string(content), old, replacement, 1)
	if err := os.WriteFile(full, []byte(changed), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRunScaffoldCheckPassesAnAlignedRepository(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "14 files match the scaffold") {
		t.Errorf("stdout = %q, want the match count", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunScaffoldCheckNamesTheDriftingFileAndLine(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	replaceInRepoFile(t, dir, ".editorconfig", "charset = utf-8", "charset = utf-9")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stdout = %q, stderr = %q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), ".editorconfig:4:") {
		t.Errorf("stderr = %q, want the file and its first differing line", stderr.String())
	}
	if !strings.Contains(stderr.String(), "utf-8") || !strings.Contains(stderr.String(), "utf-9") {
		t.Errorf("stderr = %q, want both lines quoted", stderr.String())
	}
}

func TestRunScaffoldCheckReportsAMissingFile(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	if err := os.Remove(filepath.Join(dir, "SUPPORT.md")); err != nil {
		t.Fatalf("remove SUPPORT.md: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "SUPPORT.md: missing") {
		t.Errorf("stderr = %q, want a missing SUPPORT.md", stderr.String())
	}
}

func TestRunScaffoldCheckAllowSuppressesOneFile(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	editRepoFile(t, dir, ".editorconfig", "\n# local preference\n")
	editRepoFile(t, dir, ".gitignore", "\n# local ignore\n")

	var stdout, stderr bytes.Buffer
	args := scaffoldCheckArgs("--allow", ".editorconfig:the organisation's editor standard")
	code := cli.Run(args, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 because .gitignore still drifts", code)
	}
	if strings.Contains(stderr.String(), ".editorconfig") {
		t.Errorf("stderr = %q, want .editorconfig suppressed", stderr.String())
	}
	if !strings.Contains(stderr.String(), ".gitignore") {
		t.Errorf("stderr = %q, want .gitignore reported", stderr.String())
	}
	if !strings.Contains(stdout.String(), "allowed") ||
		!strings.Contains(stdout.String(), "the organisation's editor standard") {
		t.Errorf("stdout = %q, want the allowance and its reason", stdout.String())
	}
}

func TestRunScaffoldCheckUsesTheVersionsFileFlag(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs("--versions-file", ".github/other-versions.txt"),
		testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 because ROADMAP.md names the versions file", code)
	}
	if !strings.Contains(stderr.String(), "ROADMAP.md") {
		t.Errorf("stderr = %q, want ROADMAP.md to name the passed path", stderr.String())
	}
}

func TestRunScaffoldCheckRequiresProduct(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	args := []string{
		"scaffold", "check",
		"--year", "2026",
		"--copyright", "Dennis de Jong",
		"--kit-version", scaffoldKitVersion,
		"--upstream", "trinodb/trino",
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--product") {
		t.Errorf("stderr = %q, want it to name --product", stderr.String())
	}
}

func TestRunScaffoldCheckFailsLoudlyWhenTheModuleCannotBeDerived(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("remove go.mod: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "go.mod") {
		t.Errorf("stderr = %q, want it to name go.mod", stderr.String())
	}
}

func TestRunScaffoldCheckRejectsADriftingKitPin(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs("--kit-version", "main"), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--kit-version") {
		t.Errorf("stderr = %q, want it to name --kit-version", stderr.String())
	}
}

func TestRunScaffoldCheckAllowNeedsAReason(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs("--allow", ".editorconfig:"), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "reason") {
		t.Errorf("stderr = %q, want it to name the missing reason", stderr.String())
	}
}

func TestRunScaffoldCheckRejectsAnAllowPathItDoesNotCheck(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs("--allow", "README.md:our prose is different"),
		testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "README.md") {
		t.Errorf("stderr = %q, want it to name README.md", stderr.String())
	}
}

func TestRunScaffoldCheckRejectsAProductWithoutAProvider(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	// The directory name is what the provider is derived from, so a provider
	// flag with a name that cannot be a provider fails before any comparison.
	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs("--provider", "Not_A_Provider"), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "provider") {
		t.Errorf("stderr = %q, want it to name the provider", stderr.String())
	}
}

func TestRunScaffoldCheckHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"scaffold", "check", "-h"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "tfkit scaffold check") {
		t.Errorf("stderr = %q, want the usage", stderr.String())
	}
}

func TestRunScaffoldWithoutASubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"scaffold"}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "scaffold requires a subcommand") {
		t.Errorf("stderr = %q, want the dispatch error", stderr.String())
	}
}
