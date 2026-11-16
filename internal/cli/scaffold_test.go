package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io/fs"
	"os"
	"os/exec"
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
		"REPO":               "caberdo/terraform-provider-trino",
		"REGISTRY_NAMESPACE": "caberdo",
		"YEAR":               "2026",
		"COPYRIGHT":          "Dennis de Jong",
		"KIT_VERSION":        scaffoldKitVersion,
		"PACKAGES":           "./internal/...",
		"VERSIONS_FILE":      ".github/trino-versions.txt",
		"UPSTREAM_URL":       "trinodb/trino",
		"VERSION_ENV":        "TRINO_VERSION",
		"EXPECT_VERSION_ENV": "TRINO_EXPECT_VERSION",
		"LICENSE":            "MIT",
	}
}

// scaffoldRustfsValues are the values a rustfs repository supplies. Its
// Makefile tests ./..., so the package pattern must be derived rather than
// assumed, and its licence is the documented Apache-2.0 exemption.
func scaffoldRustfsValues() map[string]string {
	return map[string]string{
		"PROVIDER":           "rustfs",
		"PRODUCT":            "RustFS",
		"MODULE":             "github.com/caberdo/terraform-provider-rustfs",
		"REPO":               "caberdo/terraform-provider-rustfs",
		"REGISTRY_NAMESPACE": "caberdo",
		"YEAR":               "2026",
		"COPYRIGHT":          "Dennis de Jong",
		"KIT_VERSION":        scaffoldKitVersion,
		"PACKAGES":           "./...",
		"VERSIONS_FILE":      ".github/rustfs-versions.txt",
		"UPSTREAM_URL":       "rustfs/rustfs",
		"VERSION_ENV":        "RUSTFS_VERSION",
		"EXPECT_VERSION_ENV": "RUSTFS_EXPECT_VERSION",
		"LICENSE":            "Apache-2.0",
	}
}

// alignedRepo writes a rendered tree into a directory with the given name,
// writes its go.mod and gives it an origin remote, so the CLI can derive the
// provider, module, package pattern and repository.
func alignedRepo(t *testing.T, dirName string, values map[string]string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	rendered, err := scaffold.Render(filepath.Join("..", "..", "scaffold"), values)
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

	goMod := "module " + values["MODULE"] + "\n\ngo 1.27.4\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	runGit(t, dir, "init", "-q")
	runGit(t, dir, "remote", "add", "origin", "https://github.com/"+values["REPO"]+".git")
	return dir
}

// alignedScaffoldRepo is a trino-shaped repository.
func alignedScaffoldRepo(t *testing.T) string {
	t.Helper()
	return alignedRepo(t, "terraform-provider-trino", scaffoldFixtureValues())
}

// alignedRustfsRepo is a rustfs-shaped repository: ./... package pattern and
// the Apache-2.0 licence exemption.
func alignedRustfsRepo(t *testing.T) string {
	t.Helper()
	return alignedRepo(t, "terraform-provider-rustfs", scaffoldRustfsValues())
}

// runGit runs one git command in dir and fails the test when it fails.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	command := exec.CommandContext(context.Background(),
		"git", append([]string{"-C", dir}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}

// scaffoldCheckArgs are the flags the trino fixture needs; provider, module,
// packages, versions-file and repo are left to the derivation.
func scaffoldCheckArgs(extra ...string) []string {
	return scaffoldCheckArgsFor("Trino", "trinodb/trino", extra...)
}

// scaffoldCheckArgsFor builds the flags for a provider whose product and
// upstream differ from trino's.
func scaffoldCheckArgsFor(product, upstream string, extra ...string) []string {
	args := []string{
		"scaffold", "check",
		"--product", product,
		"--year", "2026",
		"--copyright", "Dennis de Jong",
		"--kit-version", scaffoldKitVersion,
		"--upstream", upstream,
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

func TestRunScaffoldCheckDerivesTheRepositoryFromAnSSHRemote(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	runGit(t, dir, "remote", "set-url", "origin",
		"git@github.com:caberdo/terraform-provider-trino.git")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
}

func TestRunScaffoldCheckRequiresRepoWithoutAnOrigin(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	runGit(t, dir, "remote", "remove", "origin")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--repo") {
		t.Errorf("stderr = %q, want it to name --repo", stderr.String())
	}
}

func TestRunScaffoldCheckRejectsAnUnparsableRemote(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	runGit(t, dir, "remote", "set-url", "origin", "/tmp/not-a-github-repository")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--repo") {
		t.Errorf("stderr = %q, want it to name --repo", stderr.String())
	}
}

func TestRunScaffoldCheckRejectsANonGitHubRemote(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	runGit(t, dir, "remote", "set-url", "origin",
		"https://gitlab.com/caberdo/terraform-provider-trino.git")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "gitlab.com") || !strings.Contains(stderr.String(), "--repo") {
		t.Errorf("stderr = %q, want the offending remote and the --repo escape hatch", stderr.String())
	}
}

func TestRunScaffoldCheckAcceptsAValidRepoOverride(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	runGit(t, dir, "remote", "set-url", "origin",
		"https://gitlab.com/caberdo/terraform-provider-trino.git")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs("--repo", "caberdo/terraform-provider-trino"),
		testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 with a valid --repo override (stderr = %q)", code, stderr.String())
	}
}

func TestRunScaffoldCheckValidatesTheRepoFlag(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  int
	}{
		{"host pasted as owner", "gitlab.com/terraform-provider-trino", 2},
		{"scheme and path", "https://github.com/x/y", 2},
		{"dotted owner", "x.y/z", 2},
		{"valid", "caberdo/terraform-provider-trino", 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := alignedScaffoldRepo(t)
			t.Chdir(dir)

			var stdout, stderr bytes.Buffer
			code := cli.Run(scaffoldCheckArgs("--repo", testCase.value),
				testVersion, &stdout, &stderr)

			if code != testCase.want {
				t.Fatalf("exit = %d, want %d (stderr = %q)", code, testCase.want, stderr.String())
			}
			if testCase.want == 2 {
				if !strings.Contains(stderr.String(), "--repo") ||
					!strings.Contains(stderr.String(), "owner/name") {
					t.Errorf("stderr = %q, want the value and the expected owner/name shape", stderr.String())
				}
			}
		})
	}
}

func TestRunScaffoldCheckEchoesAnUnusedAllowance(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs("--allow", ".editorconfig:kept in reserve"),
		testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "unused") {
		t.Errorf("stdout = %q, want the unused allowance echoed", stdout.String())
	}
	if !strings.Contains(stdout.String(), "14 files match the scaffold") {
		t.Errorf("stdout = %q, want the true matched count", stdout.String())
	}
}

func TestRunScaffoldCheckDerivesThePackagePattern(t *testing.T) {
	dir := alignedRustfsRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	args := scaffoldCheckArgsFor("RustFS", "rustfs/rustfs", "--license", "Apache-2.0")
	code := cli.Run(args, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; ./... must be derived from GNUmakefile (stderr = %q)",
			code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "14 files match the scaffold") {
		t.Errorf("stdout = %q, want the match count", stdout.String())
	}
}

func TestRenderedScaffoldCoverageReadmeIsInSync(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme", "--check",
		"--readme", "README.md",
		"--baseline", ".github/coverage-baseline.txt",
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; rendered README and baseline must agree (stderr = %q)",
			code, stderr.String())
	}
}

func TestRunScaffoldCheckRejectsAnInvalidLicense(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs("--license", "MIT OR Apache-2.0"),
		testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--license") {
		t.Errorf("stderr = %q, want it to name --license", stderr.String())
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

func TestRunScaffoldFixRepairsAnUnalignedRepository(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "terraform-provider-trino")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module github.com/caberdo/terraform-provider-trino\n\ngo 1.27.4\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "remote", "add", "origin",
		"https://github.com/caberdo/terraform-provider-trino.git")
	t.Chdir(dir)

	args := scaffoldCheckArgs("--packages", "./...")
	args[1] = "fix"
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("fix exit = %d, want 0 (stdout = %q, stderr = %q)", code, stdout.String(), stderr.String())
	}
	for _, path := range []string{
		"wrote .editorconfig",
		"wrote .github/PULL_REQUEST_TEMPLATE.md",
		"wrote .github/ISSUE_TEMPLATE/bug_report.md",
	} {
		if !strings.Contains(stdout.String(), path) {
			t.Errorf("stdout = %q, want %q", stdout.String(), path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); !os.IsNotExist(err) {
		t.Errorf("fix created README.md (err = %v); it must write only the enforced set", err)
	}

	var checkOut, checkErr bytes.Buffer
	checkArgs := scaffoldCheckArgs("--packages", "./...")
	code = cli.Run(checkArgs, testVersion, &checkOut, &checkErr)
	if code != 0 {
		t.Fatalf("check after fix exit = %d, want 0 (stderr = %q)", code, checkErr.String())
	}
}

func TestRunScaffoldFixHonoursTheAllowListFile(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	editRepoFile(t, dir, ".editorconfig", "\n# local preference\n")
	editRepoFile(t, dir, ".gitignore", "\n# local ignore\n")
	writeAllowFile(t, dir, "# durable deviations\n\n.editorconfig: the organisation's editor standard\n")

	var stdout, stderr bytes.Buffer
	args := scaffoldCheckArgs()
	args[1] = "fix"
	code := cli.Run(args, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("fix exit = %d, want 0 because the only remaining drift is allowed (stderr = %q)",
			code, stderr.String())
	}
	if strings.Contains(stdout.String(), "wrote .editorconfig") {
		t.Errorf("stdout = %q, want the allowed path left unwritten", stdout.String())
	}
	if !strings.Contains(stdout.String(), "wrote .gitignore") {
		t.Errorf("stdout = %q, want .gitignore repaired", stdout.String())
	}
	content, err := os.ReadFile(filepath.Join(dir, ".editorconfig"))
	if err != nil {
		t.Fatalf("read .editorconfig: %v", err)
	}
	if !strings.Contains(string(content), "# local preference") {
		t.Error("fix overwrote an allowed path")
	}
}

func TestRunScaffoldFixHonoursTheAllowFlag(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	editRepoFile(t, dir, ".editorconfig", "\n# local preference\n")

	var stdout, stderr bytes.Buffer
	args := scaffoldCheckArgs("--allow", ".editorconfig:debugging this one file")
	args[1] = "fix"
	code := cli.Run(args, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("fix exit = %d, want 0 because the only drift is allowed (stderr = %q)",
			code, stderr.String())
	}
	if strings.Contains(stdout.String(), "wrote .editorconfig") {
		t.Errorf("stdout = %q, want the allowed path left unwritten", stdout.String())
	}
}

func TestRunScaffoldFixDryRunChangesNothing(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	editRepoFile(t, dir, ".editorconfig", "\n# local preference\n")
	before := snapshotTree(t, dir)

	var stdout, stderr bytes.Buffer
	args := scaffoldCheckArgs("--dry-run")
	args[1] = "fix"
	code := cli.Run(args, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("dry-run exit = %d, want 1 because the drift remains (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "would write .editorconfig") {
		t.Errorf("stdout = %q, want the planned write", stdout.String())
	}
	after := snapshotTree(t, dir)
	if len(before) != len(after) {
		t.Fatalf("file count changed from %d to %d", len(before), len(after))
	}
	for path, digest := range before {
		if after[path] != digest {
			t.Errorf("%s changed during a dry run", path)
		}
	}
}

func TestRunScaffoldCheckReadsTheAllowListFile(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	editRepoFile(t, dir, ".editorconfig", "\n# local preference\n")
	writeAllowFile(t, dir, "# durable deviations\n\n.editorconfig: the organisation's editor standard\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout = %q, stderr = %q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "allowed") ||
		!strings.Contains(stdout.String(), "the organisation's editor standard") {
		t.Errorf("stdout = %q, want the file's allowance and its reason", stdout.String())
	}
}

func TestRunScaffoldCheckRejectsAnAllowListLineWithoutAReason(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	writeAllowFile(t, dir, "# durable deviations\n.editorconfig:\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "scaffold-allow.txt:2") ||
		!strings.Contains(stderr.String(), "reason") {
		t.Errorf("stderr = %q, want the line number and the missing reason", stderr.String())
	}
}

func TestRunScaffoldCheckRejectsADuplicateAllowListPath(t *testing.T) {
	dir := alignedScaffoldRepo(t)
	t.Chdir(dir)
	writeAllowFile(t, dir, ".editorconfig: first reason\n.editorconfig: second reason\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run(scaffoldCheckArgs(), testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "listed twice") {
		t.Errorf("stderr = %q, want the duplicate path named", stderr.String())
	}
}

// writeAllowFile writes the per-repository allow list.
func writeAllowFile(t *testing.T, dir, content string) {
	t.Helper()

	path := filepath.Join(dir, ".github", "scaffold-allow.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for the allow file: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write the allow file: %v", err)
	}
}

// snapshotTree hashes every regular file under dir, keyed by slash path, so a
// test can compare a directory before and after a command.
func snapshotTree(t *testing.T, dir string) map[string][sha256.Size]byte {
	t.Helper()

	snapshot := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = sha256.Sum256(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return snapshot
}

func TestRunScaffoldFixHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"scaffold", "fix", "-h"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "tfkit scaffold fix") {
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
