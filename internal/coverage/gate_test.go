package coverage_test

import (
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

// gateProfile builds a profile with one block per entry, all counted as
// covered statements. Files are full import paths, as a real profile records
// them, so the package-name matching is exercised rather than bypassed.
func gateProfile(t *testing.T, files map[string]int) coverage.Profile {
	t.Helper()

	profile := coverage.Profile{Mode: "atomic"}
	for file, statements := range files {
		profile.Blocks = append(profile.Blocks, coverage.Block{
			File:      file,
			StartLine: 1,
			StartCol:  1,
			EndLine:   2,
			EndCol:    2,
			NumStmt:   statements,
			Count:     1,
		})
	}
	return profile
}

func TestGateFailsBelowTotalFloor(t *testing.T) {
	profile := coverage.Profile{Mode: "atomic", Blocks: []coverage.Block{
		{File: "github.com/x/y/internal/a/a.go", NumStmt: 4, Count: 0},
		{File: "github.com/x/y/internal/a/b.go", NumStmt: 4, Count: 1},
	}}

	report, err := coverage.Gate(profile, 80, nil, nil)
	if err != nil {
		t.Fatalf("Gate() error = %v, want nil", err)
	}
	if got, want := report.Total, 50.0; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
	if len(report.Failures) == 0 {
		t.Fatal("Failures = empty, want the total failure")
	}
	if got, want := report.Failures[0].Package, coverage.TotalPackage; got != want {
		t.Errorf("failure package = %q, want %q", got, want)
	}
	if got, want := report.Failures[0].Actual, 50.0; got != want {
		t.Errorf("failure actual = %v, want %v", got, want)
	}
}

func TestGateFailsPackageWithoutFloorAboveDefault(t *testing.T) {
	// internal/b has no entry of its own, so `default` applies and its 25%
	// must fail against 70.
	profile := coverage.Profile{Mode: "atomic", Blocks: []coverage.Block{
		{File: "github.com/x/y/internal/a/a.go", NumStmt: 10, Count: 1},
		{File: "github.com/x/y/internal/b/b.go", NumStmt: 4, Count: 1},
		{File: "github.com/x/y/internal/b/c.go", NumStmt: 12, Count: 0},
	}}
	floors := coverage.Floors{coverage.DefaultFloorKey: 70, "internal/a": 70}
	packages := []string{"internal/a", "internal/b"}

	report, err := coverage.Gate(profile, 24, floors, packages)
	if err != nil {
		t.Fatalf("Gate() error = %v, want nil", err)
	}
	if len(report.Failures) != 1 {
		t.Fatalf("Failures = %+v, want exactly one", report.Failures)
	}
	failure := report.Failures[0]
	if got, want := failure.Package, "internal/b"; got != want {
		t.Errorf("failure package = %q, want %q", got, want)
	}
	if got, want := failure.Floor, 70.0; got != want {
		t.Errorf("failure floor = %v, want %v", got, want)
	}
	if got, want := failure.Actual, 25.0; got != want {
		t.Errorf("failure actual = %v, want %v", got, want)
	}
}

func TestGateFailsClosedOnFloorForUnknownPackage(t *testing.T) {
	// A floor naming a package `go list` no longer returns is rotted
	// configuration: it cannot silently stop applying.
	floors := coverage.Floors{coverage.DefaultFloorKey: 70, "internal/gone": 60}

	_, err := coverage.Gate(gateProfile(t, map[string]int{
		"github.com/x/y/internal/a/a.go": 10,
	}), 10, floors, []string{"internal/a"})
	if err == nil {
		t.Fatal("Gate() error = nil, want an unknown-package error")
	}
	if !strings.Contains(err.Error(), "internal/gone") {
		t.Errorf("error = %q, want it to name the orphaned floor", err)
	}
}

func TestGateFailsClosedOnFloorsWithoutDefault(t *testing.T) {
	floors := coverage.Floors{"internal/a": 70}

	_, err := coverage.Gate(gateProfile(t, map[string]int{
		"github.com/x/y/internal/a/a.go": 10,
	}), 10, floors, []string{"internal/a"})
	if err == nil {
		t.Fatal("Gate() error = nil, want an error for the missing default floor")
	}
	if !strings.Contains(err.Error(), coverage.DefaultFloorKey) {
		t.Errorf("error = %q, want it to name the missing default entry", err)
	}
}

func TestGateWithoutFloorsFileGatesTheTotalOnly(t *testing.T) {
	report, err := coverage.Gate(gateProfile(t, map[string]int{
		"github.com/x/y/internal/a/a.go": 10,
	}), 80, nil, nil)
	if err != nil {
		t.Fatalf("Gate() error = %v, want nil", err)
	}
	if got, want := report.Total, 100.0; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %+v, want none", report.Failures)
	}
}

func TestGateFailsPackageWithNoTestsAtZero(t *testing.T) {
	// internal/b exists but writes no profile records: it has no tests, and
	// the gate must measure it at 0% rather than treating it as absent.
	floors := coverage.Floors{coverage.DefaultFloorKey: 50}

	report, err := coverage.Gate(gateProfile(t, map[string]int{
		"github.com/x/y/internal/a/a.go": 10,
	}), 10, floors, []string{"internal/a", "internal/b"})
	if err != nil {
		t.Fatalf("Gate() error = %v, want nil", err)
	}
	if len(report.Failures) != 1 {
		t.Fatalf("Failures = %+v, want one for the untested package", report.Failures)
	}
	if got, want := report.Failures[0].Package, "internal/b"; got != want {
		t.Errorf("failure package = %q, want %q", got, want)
	}
	if got, want := report.Failures[0].Actual, 0.0; got != want {
		t.Errorf("failure actual = %v, want %v", got, want)
	}
}

func TestGatePassesWhenEveryFloorIsMet(t *testing.T) {
	profile := coverage.Profile{Mode: "atomic", Blocks: []coverage.Block{
		{File: "github.com/x/y/internal/a/a.go", NumStmt: 10, Count: 1},
		{File: "github.com/x/y/internal/b/b.go", NumStmt: 10, Count: 0},
	}}
	floors := coverage.Floors{coverage.DefaultFloorKey: 50, "internal/a": 100, "internal/b": 0}

	report, err := coverage.Gate(profile, 50, floors, []string{"internal/a", "internal/b"})
	if err != nil {
		t.Fatalf("Gate() error = %v, want nil", err)
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %+v, want none", report.Failures)
	}
	if got, want := report.Total, 50.0; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
}

func TestGateRejectsBaselineOutsideRange(t *testing.T) {
	_, err := coverage.Gate(gateProfile(t, map[string]int{
		"github.com/x/y/internal/a/a.go": 10,
	}), 101, nil, nil)
	if err == nil {
		t.Fatal("Gate() error = nil, want an out-of-range baseline error")
	}
	if !strings.Contains(err.Error(), "101") {
		t.Errorf("error = %q, want it to name the baseline", err)
	}
}

func TestMeasureRoundsLikeTheScript(t *testing.T) {
	// Per-package coverage is compared at four decimal places by the shell
	// script, and the total at one, so the same rounding must be applied here.
	profile := coverage.Profile{Mode: "atomic", Blocks: []coverage.Block{
		{File: "github.com/x/y/internal/a/a.go", NumStmt: 2, Count: 1},
		{File: "github.com/x/y/internal/a/b.go", NumStmt: 1, Count: 0},
	}}

	measured := coverage.Measure(profile, []string{"internal/a"})
	if len(measured) != 1 {
		t.Fatalf("len(measured) = %d, want 1", len(measured))
	}
	if got, want := measured[0].Coverage, 66.6667; got != want {
		t.Errorf("coverage = %v, want %v", got, want)
	}
	if got, want := coverage.Total(profile), 66.7; got != want {
		t.Errorf("Total() = %v, want %v", got, want)
	}
}

func TestMeasureIgnoresBlocksOutsideTheListedPackages(t *testing.T) {
	profile := coverage.Profile{Mode: "atomic", Blocks: []coverage.Block{
		{File: "github.com/x/y/internal/a/a.go", NumStmt: 10, Count: 1},
		{File: "github.com/x/y/internal/other/a.go", NumStmt: 10, Count: 0},
	}}

	measured := coverage.Measure(profile, []string{"internal/a"})
	if got, want := measured[0].Coverage, 100.0; got != want {
		t.Errorf("coverage = %v, want %v", got, want)
	}
}
