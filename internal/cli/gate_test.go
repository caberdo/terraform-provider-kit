package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// testVersion is the version the in-package tests pass to Run. The external
// test package has its own copy; both describe the same ldflags value.
const testVersion = "v0.1.0-test"

// stubPackages replaces the `go list` invocation so a gate test does not need
// a Go module. The stub returns repo-relative package names, as the real
// helper does after stripping the module prefix.
func stubPackages(t *testing.T, packages ...string) {
	t.Helper()

	original := listGoPackages
	listGoPackages = func(string) ([]string, error) {
		return packages, nil
	}
	t.Cleanup(func() { listGoPackages = original })
}

// gateFile writes content to name inside dir and returns the path.
func gateFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// gateProfile is a one-package profile with coveredStatements covered
// statements and uncoveredStatements uncovered ones. Paths are module-prefixed
// as a real profile records them, so package-name matching is exercised.
func gateProfile(coveredStatements, uncoveredStatements int) string {
	lines := []string{"mode: atomic"}
	for range coveredStatements {
		lines = append(lines, "example.com/m/internal/a/a.go:1.1,2.2 1 1")
	}
	for range uncoveredStatements {
		lines = append(lines, "example.com/m/internal/a/b.go:3.1,4.2 1 0")
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestRunCoverageGateReportsPass(t *testing.T) {
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 0))
	baseline := gateFile(t, dir, "baseline.txt", "80\n")
	floors := gateFile(t, dir, "floors.txt", "default 70\ninternal/a 70\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
		"--floors", floors,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
	out := stdout.String()
	for _, want := range []string{
		"Total statement coverage: 100.0% (floor 80%)",
		"Per-package statement coverage (floors from " + floors + "):",
		"PACKAGE",
		"internal/a",
		"coverage-gate: PASS",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout =\n%s\nwant it to contain %q", out, want)
		}
	}
}

func TestRunCoverageGateEchoesFloorToken(t *testing.T) {
	// The shell script echoes the floor token as written, so `71.0` must not
	// become `71` in the table: agentgateway's CI log compares byte for byte.
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 0))
	baseline := gateFile(t, dir, "baseline.txt", "10\n")
	floors := gateFile(t, dir, "floors.txt", "default 50.0\ninternal/a 71.0\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
		"--floors", floors,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "71.0%") {
		t.Errorf("stdout =\n%s\nwant the floor echoed as 71.0%%", out)
	}
	if strings.Contains(out, " 71%") {
		t.Errorf("stdout =\n%s\nwant no floor rendered as 71%%", out)
	}
}

func TestRunCoverageGateBelowPackageFloorExitsOne(t *testing.T) {
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 1))
	baseline := gateFile(t, dir, "baseline.txt", "40\n")
	floors := gateFile(t, dir, "floors.txt", "default 70\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
		"--floors", floors,
	}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "Total statement coverage: 50.0% (floor 40%)") {
		t.Errorf("stdout =\n%s\nwant the total line", stdout.String())
	}
	for _, want := range []string{
		"coverage-gate: FAIL — 1 package(s) below their floor:",
		"internal/a: 50.00% < 70% (delta -20.00)",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr =\n%s\nwant it to contain %q", stderr.String(), want)
		}
	}
}

func TestRunCoverageGateBelowTotalFloorExitsOne(t *testing.T) {
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 1))
	baseline := gateFile(t, dir, "baseline.txt", "80\n")
	floors := gateFile(t, dir, "floors.txt", "default 40\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
		"--floors", floors,
	}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "50.0% is below the 80% floor") {
		t.Errorf("stderr =\n%s\nwant the total failure message", stderr.String())
	}
	if strings.Contains(stdout.String(), "Per-package") {
		t.Errorf("stdout =\n%s\nwant no package table below a failed total, as the script does today", stdout.String())
	}
}

func TestRunCoverageGateFailedTotalWinsOverMalformedFloors(t *testing.T) {
	// The script checks the total before it reads the floor file, so a run
	// that is already below the module floor reports 1, not a configuration
	// error it would never have reached.
	dir := t.TempDir()
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 1))
	baseline := gateFile(t, dir, "baseline.txt", "80\n")
	floors := gateFile(t, dir, "floors.txt", "default seventy\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
		"--floors", floors,
	}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "line 1") {
		t.Errorf("stderr = %q, want the total failure rather than the floor-file error", stderr.String())
	}
}

func TestRunCoverageGateFailsClosedOnMissingProfile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "coverage.out")
	baseline := gateFile(t, dir, "baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", missing,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), missing) {
		t.Errorf("stderr = %q, want it to name %q", stderr.String(), missing)
	}
}

func TestRunCoverageGateWithoutFloorsFileGatesTheTotalOnly(t *testing.T) {
	dir := t.TempDir()
	// A floors file that existed would make the command shell out to go list;
	// total-only gating must not need a package list at all.
	original := listGoPackages
	listGoPackages = func(string) ([]string, error) {
		t.Fatal("go list was invoked without a floors file")
		return nil, nil
	}
	t.Cleanup(func() { listGoPackages = original })

	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 0))
	baseline := gateFile(t, dir, "baseline.txt", "80\n")
	missingFloors := filepath.Join(dir, "absent-floors.txt")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
		"--floors", missingFloors,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "coverage-gate: PASS") {
		t.Errorf("stdout =\n%s\nwant PASS", stdout.String())
	}
}

func TestRunCoverageGateKeyValueBaselineExitsTwo(t *testing.T) {
	dir := t.TempDir()
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 0))
	baseline := gateFile(t, dir, "baseline.txt", "measured_total=83.4\nfloor_total=82.5\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "key=value") {
		t.Errorf("stderr = %q, want it to name the unsupported dialect", stderr.String())
	}
}

func TestRunCoverageGateMalformedFloorsExitsTwo(t *testing.T) {
	dir := t.TempDir()
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 0))
	baseline := gateFile(t, dir, "baseline.txt", "80\n")
	floors := gateFile(t, dir, "floors.txt", "default seventy\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
		"--floors", floors,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "line 1") {
		t.Errorf("stderr = %q, want it to name the malformed line", stderr.String())
	}
}

func TestRunCoverageGateMissingBaselineExitsTwo(t *testing.T) {
	dir := t.TempDir()
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 0))
	missing := filepath.Join(dir, "absent.txt")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", missing,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), missing) {
		t.Errorf("stderr = %q, want it to name %q", stderr.String(), missing)
	}
}

func TestRunCoverageGateListFailureExitsTwo(t *testing.T) {
	dir := t.TempDir()
	original := listGoPackages
	listGoPackages = func(string) ([]string, error) {
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { listGoPackages = original })

	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 0))
	baseline := gateFile(t, dir, "baseline.txt", "80\n")
	floors := gateFile(t, dir, "floors.txt", "default 70\n")

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
		"--floors", floors,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "go list") {
		t.Errorf("stderr = %q, want it to name the failed go list", stderr.String())
	}
}

func TestRunCoverageGateHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"coverage", "gate", "--help"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, flag := range []string{"--profile", "--baseline", "--floors", "--packages"} {
		if !strings.Contains(stderr.String(), flag) {
			t.Errorf("stderr = %q, want it to mention %s", stderr.String(), flag)
		}
	}
}

func TestGoListPackagesStripsModulePrefix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.27.4\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for _, name := range []string{"internal/a/a.go", "internal/b/b.go"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte("package "+filepath.Base(filepath.Dir(path))+"\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	t.Chdir(dir)

	packages, err := listGoPackages("./internal/...")
	if err != nil {
		t.Fatalf("goListPackages() error = %v", err)
	}
	want := []string{"internal/a", "internal/b"}
	if !slices.Equal(packages, want) {
		t.Errorf("packages = %v, want %v", packages, want)
	}
}
