package cli

import (
	"bytes"
	"strings"
	"testing"
)

// scopeGateBaseline is a key=value baseline in the shape gravitino and rustfs
// commit. The floors live in this file, so no floors file exists beside it.
const scopeGateBaseline = `# Statement-coverage baseline for the merged profile.
measured_internal_a=100
measured_total=100
measured_commit=abc1234
measured_date=2026-10-19

floor_internal_a=79.0
floor_total=82.5

target=80
gold=90
`

func TestRunCoverageGateUsesTheScopeDialectFromTheBaseline(t *testing.T) {
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(2, 0))
	baseline := gateFile(t, dir, "baseline.txt", scopeGateBaseline)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q, stdout = %q)", code, stderr.String(), stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "baseline dialect: key=value") {
		t.Errorf("stderr = %q, want the chosen dialect printed", got)
	}
	for _, want := range []string{
		"Merged statement coverage (unit + acceptance)\n",
		"Profile:  " + profile + "\n",
		"Baseline: " + baseline + "\n",
		"| Scope | Coverage | Floor | Delta |\n",
		"| `internal/a` | 100.0% | 79.0% | +21.0 |\n",
		"| `total` | 100.0% | 82.5% | +17.5 |\n",
		"coverage-gate: PASS — 2 scope(s) at or above their floor.\n",
		"The 80% milestone is met.\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout =\n%s\nwant it to contain %q", stdout.String(), want)
		}
	}
	if strings.Contains(stdout.String(), "Total statement coverage:") {
		t.Errorf("stdout =\n%s\nwant the scope report, not the bare baseline report", stdout.String())
	}
}

func TestRunCoverageGateScopeDialectBelowFloorExitsOne(t *testing.T) {
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(1, 1))
	baseline := gateFile(t, dir, "baseline.txt", strings.ReplaceAll(scopeGateBaseline, "=100", "=50"))

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{
		"coverage-gate: FAIL — merged coverage in " + profile + " is below the floor in " + baseline + "\n",
		"  - internal/a: 50.00% is below the 79.0% floor\n",
		"  - total: 50.00% is below the 82.5% floor\n",
		"lower the floor in " + baseline + " in the same pull request",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout =\n%s\nwant it to contain %q", stdout.String(), want)
		}
	}
	if strings.Contains(stderr.String(), "FAIL") {
		t.Errorf("stderr = %q, want the report on stdout like the script", stderr.String())
	}
}

func TestRunCoverageGateScopeDialectFailsClosedOnAMissingFloor(t *testing.T) {
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(2, 0))
	baseline := gateFile(t, dir, "baseline.txt", strings.Replace(
		scopeGateBaseline, "floor_internal_a=79.0\n", "", 1))

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "internal_a") {
		t.Errorf("stderr = %q, want it to name the scope without a floor", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing on a configuration error", stdout.String())
	}
}

func TestRunCoverageGateScopeDialectFailsWhenAScopeIsAbsent(t *testing.T) {
	// The package exists, but the profile has no block for it: it is measured
	// at 0 and fails, rather than disappearing from the table.
	dir := t.TempDir()
	stubPackages(t, "internal/a", "internal/b")
	profile := gateFile(t, dir, "coverage.out", gateProfile(2, 0))
	baseline := gateFile(t, dir, "baseline.txt", strings.Replace(
		scopeGateBaseline, "measured_total=100", "measured_internal_b=90\nfloor_internal_b=80\nmeasured_total=100", 1))

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "| `internal/b` | 0.0% | 80% | -80.0 |") {
		t.Errorf("stdout =\n%s\nwant the absent scope reported at 0.0%%", stdout.String())
	}
	if !strings.Contains(stdout.String(), "  - internal/b: 0.00% is below the 80% floor\n") {
		t.Errorf("stdout =\n%s\nwant the absent scope in the failure detail", stdout.String())
	}
}

func TestRunCoverageGateScopeDialectFailsClosedOnBackwardsMilestones(t *testing.T) {
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(2, 0))
	baseline := gateFile(t, dir, "baseline.txt", strings.Replace(
		scopeGateBaseline, "target=80\ngold=90", "target=95\ngold=90", 1))

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{"target", "gold", "95", "90"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to name %q", stderr.String(), want)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing on a configuration error", stdout.String())
	}
}

func TestRunCoverageGateScopeDialectFailsClosedOnAnUnknownPackage(t *testing.T) {
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(2, 0))
	baseline := gateFile(t, dir, "baseline.txt", strings.Replace(
		scopeGateBaseline, "measured_total=100", "measured_internal_gone=90\nfloor_internal_gone=80\nmeasured_total=100", 1))

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"coverage", "gate",
		"--profile", profile,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "internal_gone") {
		t.Errorf("stderr = %q, want it to name the baseline key", stderr.String())
	}
}

func TestRunCoverageGateBareBaselineWithAFloorCommentStaysBare(t *testing.T) {
	// Detection reads keys, not prose: a comment naming floor_* must not
	// flip a bare baseline to the scope parser.
	dir := t.TempDir()
	stubPackages(t, "internal/a")
	profile := gateFile(t, dir, "coverage.out", gateProfile(2, 0))
	baseline := gateFile(t, dir, "baseline.txt", "# the floors live in coverage-floors.txt, not floor_* keys\n80\n")
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
	if !strings.Contains(stdout.String(), "Total statement coverage: 100.0% (floor 80%)") {
		t.Errorf("stdout =\n%s\nwant the bare baseline report", stdout.String())
	}
	if strings.Contains(stderr.String(), "baseline dialect") {
		t.Errorf("stderr = %q, want no dialect line for the bare parser", stderr.String())
	}
}
