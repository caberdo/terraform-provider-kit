package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/cli"
)

// readmeDoc is a README with a current floor block and prose on both sides.
const readmeDoc = "# terraform-provider-trino\n" +
	"\n" +
	"The contribution workflow is in CONTRIBUTING.md.\n" +
	"\n" +
	"<!-- coverage:start -->\n" +
	"![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)\n" +
	"\n" +
	"The coverage gate fails below **80%** (`./scripts/coverage-gate.sh`).\n" +
	"<!-- coverage:end -->\n" +
	"\n" +
	"## Verified unsupported\n" +
	"\n" +
	"DENY is rejected.\n"

// readmeDocAtFloor is readmeDoc with an out-of-date block: both the badge and
// the sentence name floor instead of 80.
func readmeDocAtFloor(floor string) string {
	return strings.ReplaceAll(
		strings.ReplaceAll(readmeDoc, "coverage%20floor-80%25", "coverage%20floor-"+floor+"%25"),
		"below **80%**", "below **"+floor+"%**")
}

// readmeFile writes content to name inside a fresh temp dir and returns its
// path.
func readmeFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// readFile reads path and fails the test on error.
func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func TestRunCoverageReadmeRewritesStaleBlock(t *testing.T) {
	readme := readmeFile(t, "README.md", readmeDocAtFloor("79"))
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if got := readFile(t, readme); got != readmeDoc {
		t.Errorf("README =\n%s\nwant:\n%s", got, readmeDoc)
	}
	if want := "coverage-readme: updated '" + readme + "' from '" + baseline + "'\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

func TestRunCoverageReadmeLeavesACurrentBlockAlone(t *testing.T) {
	readme := readmeFile(t, "README.md", readmeDoc)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if got := readFile(t, readme); got != readmeDoc {
		t.Errorf("README =\n%s\nwant it unchanged", got)
	}
	if !strings.Contains(stdout.String(), "is already in sync") {
		t.Errorf("stdout = %q, want it to report that nothing changed", stdout.String())
	}
}

func TestRunCoverageReadmeCheckAcceptsACurrentBlock(t *testing.T) {
	readme := readmeFile(t, "README.md", readmeDoc)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme", "--check",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if want := "coverage-readme: '" + readme + "' is in sync with '" + baseline + "'\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunCoverageReadmeCheckReportsStale(t *testing.T) {
	stale := readmeDocAtFloor("79")
	readme := readmeFile(t, "README.md", stale)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme", "--check",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	// Exit 1, not 2: a stale README is the condition --check exists to
	// report, and CI distinguishes it from a broken marker pair.
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{readme, baseline, "out of date", "make coverage-readme"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if got := readFile(t, readme); got != stale {
		t.Errorf("--check rewrote the README:\n%s\nwant it unchanged:\n%s", got, stale)
	}
}

func TestRunCoverageReadmeMissingMarkerExitsTwo(t *testing.T) {
	withoutBlock := "# Title\n\nNo coverage block here.\n"
	readme := readmeFile(t, "README.md", withoutBlock)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{readme, "coverage:start", "coverage:end"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if got := readFile(t, readme); got != withoutBlock {
		t.Errorf("README =\n%s\nwant it unchanged after a marker error", got)
	}
}

func TestRunCoverageReadmeReversedMarkersExitTwo(t *testing.T) {
	reversed := "# Title\n\n<!-- coverage:end -->\nstale\n<!-- coverage:start -->\n"
	readme := readmeFile(t, "README.md", reversed)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), readme) {
		t.Errorf("stderr = %q, want it to name %q", stderr.String(), readme)
	}
	if got := readFile(t, readme); got != reversed {
		t.Errorf("README =\n%s\nwant it unchanged after a marker error", got)
	}
}

func TestRunCoverageReadmeMissingReadmeExitsTwo(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "README.md")
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", missing,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), missing) {
		t.Errorf("stderr = %q, want it to name %q", stderr.String(), missing)
	}
}

func TestRunCoverageReadmeMissingBaselineExitsTwo(t *testing.T) {
	readme := readmeFile(t, "README.md", readmeDoc)
	missing := filepath.Join(t.TempDir(), "coverage-baseline.txt")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", missing,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), missing) {
		t.Errorf("stderr = %q, want it to name %q", stderr.String(), missing)
	}
}

func TestRunCoverageReadmeMalformedBaselineExitsTwo(t *testing.T) {
	readme := readmeFile(t, "README.md", readmeDoc)
	baseline := readmeFile(t, "coverage-baseline.txt", "eighty\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "eighty") {
		t.Errorf("stderr = %q, want it to quote the bad baseline", stderr.String())
	}
}

func TestRunCoverageReadmeHonoursTheGateCommand(t *testing.T) {
	// Task 12 passes `make coverage-gate` so the sentence survives the script
	// deletion; today the default keeps the block byte-identical.
	readme := readmeFile(t, "README.md", readmeDoc)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
		"--gate-command", "make coverage-gate",
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	want := strings.ReplaceAll(readmeDoc,
		"(`./scripts/coverage-gate.sh`)", "(`make coverage-gate`)")
	if got := readFile(t, readme); got != want {
		t.Errorf("README =\n%s\nwant:\n%s", got, want)
	}
}

func TestRunCoverageReadmeRejectsAPositionalArgument(t *testing.T) {
	// The command it replaces took [README.md] [baseline] positionally. Flags
	// are the interface now, so a stray word is a usage error rather than a
	// guessed path.
	readme := readmeFile(t, "README.md", readmeDoc)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
		"README.md",
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: tfkit coverage readme") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
	if got := readFile(t, readme); got != readmeDoc {
		t.Errorf("README =\n%s\nwant it unchanged", got)
	}
}

func TestRunCoverageReadmeRejectsAnUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"coverage", "readme", "--stale"}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-stale") {
		t.Errorf("stderr = %q, want it to name the unknown flag", stderr.String())
	}
}

func TestRunCoverageReadmeUnreadableReadmeExitsTwo(t *testing.T) {
	// A directory in the --readme slot is the portable way to make the read
	// fail: it opens, and reading it does not.
	dir := t.TempDir()
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", dir,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), dir) {
		t.Errorf("stderr = %q, want it to name %q", stderr.String(), dir)
	}
}

func TestRunCoverageReadmeHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"coverage", "readme", "--help"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, flag := range []string{"--check", "--readme", "--baseline", "--gate-command"} {
		if !strings.Contains(stderr.String(), flag) {
			t.Errorf("stderr = %q, want it to mention %s", stderr.String(), flag)
		}
	}
}
