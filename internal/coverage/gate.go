package coverage

import (
	"fmt"
	"maps"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
)

// TotalPackage is the [Failure.Package] used for the whole-module check, as
// opposed to a repo-relative package name.
const TotalPackage = "total"

// Report is the outcome of a gate run: the measured module total and every
// floor that was missed.
type Report struct {
	Total    float64
	Failures []Failure
}

// Failure is one missed floor. Package is [TotalPackage] when the module total
// is below the baseline.
type Failure struct {
	Package string
	Floor   float64
	Actual  float64
}

// PackageCoverage is the measured statement coverage of one package.
type PackageCoverage struct {
	Package  string
	Coverage float64
}

// Total returns the statement coverage of the whole profile as a percentage,
// rounded to one decimal place like `go tool cover -func` prints it.
func Total(p Profile) float64 {
	statements, covered := 0, 0
	for _, block := range p.Blocks {
		statements += block.NumStmt
		if block.Count > 0 {
			covered += block.NumStmt
		}
	}
	if statements == 0 {
		return 0
	}
	return roundTo(100*float64(covered)/float64(statements), 1)
}

// Measure returns the statement coverage of each package in packages, in the
// order given. A package with no blocks in the profile is measured at 0, so a
// package whose tests were deleted cannot pass a floor.
//
// Profile paths are full import paths (`github.com/.../internal/x/a.go`) while
// packages are repo-relative (`internal/x`), so a block belongs to the longest
// listed package name its directory ends in. Blocks outside every listed
// package are left out, as the shell script leaves them out of its table; they
// still count towards [Total].
func Measure(p Profile, packages []string) []PackageCoverage {
	type tally struct {
		statements int
		covered    int
	}

	longestFirst := slices.Clone(packages)
	slices.SortFunc(longestFirst, func(a, b string) int {
		if diff := len(b) - len(a); diff != 0 {
			return diff
		}
		return strings.Compare(a, b)
	})

	tallies := make(map[string]*tally, len(packages))
	for _, block := range p.Blocks {
		dir := path.Dir(block.File)
		for _, name := range longestFirst {
			if dir != name && !strings.HasSuffix(dir, "/"+name) {
				continue
			}
			count := tallies[name]
			if count == nil {
				count = &tally{}
				tallies[name] = count
			}
			count.statements += block.NumStmt
			if block.Count > 0 {
				count.covered += block.NumStmt
			}
			break
		}
	}

	measured := make([]PackageCoverage, len(packages))
	for i, name := range packages {
		value := 0.0
		if count := tallies[name]; count != nil && count.statements > 0 {
			value = roundTo(100*float64(count.covered)/float64(count.statements), 4)
		}
		measured[i] = PackageCoverage{Package: name, Coverage: value}
	}
	return measured
}

// Gate checks a profile against the module baseline and the per-package
// floors. Coverage that is below a floor is returned in the report; a
// configuration problem (an impossible baseline, a missing `default`, a floor
// outside 0..100 or NaN, or a floor for a package that does not exist) is an
// error so the caller can fail closed.
//
// A nil floors map means total-only gating; a non-nil map must contain
// [DefaultFloorKey]. The total is checked first, as the shell script this
// replaces does, and a failed total short-circuits both the floors validation
// and the package checks.
func Gate(p Profile, baseline float64, floors Floors, packages []string) (Report, error) {
	if math.IsNaN(baseline) || baseline < 0 || baseline > 100 {
		return Report{}, fmt.Errorf("baseline %s is outside 0..100", formatPercent(baseline))
	}

	report := Report{Total: Total(p)}
	if report.Total < baseline {
		report.Failures = append(report.Failures, Failure{
			Package: TotalPackage,
			Floor:   baseline,
			Actual:  report.Total,
		})
		return report, nil
	}

	if floors == nil {
		return report, nil
	}

	if _, ok := floors[DefaultFloorKey]; !ok {
		return Report{}, fmt.Errorf("floors: missing %q entry", DefaultFloorKey)
	}

	names := slices.Sorted(maps.Keys(floors))

	// Validate here as well as in ParseFloors: Gate is the reuse point for
	// the other baseline dialect, and a NaN or out-of-range value would make
	// every comparison false and let the gate fail open.
	for _, name := range names {
		if err := validateFloor(name, floors[name]); err != nil {
			return Report{}, err
		}
	}

	// A floor for a package that no longer exists is rotted configuration:
	// fail so the file stays in step with the tree.
	for _, name := range names {
		if name == DefaultFloorKey {
			continue
		}
		if !slices.Contains(packages, name) {
			return Report{}, fmt.Errorf("floor names %q, which the package list does not contain", name)
		}
	}

	for _, measured := range Measure(p, packages) {
		floor := floors[DefaultFloorKey]
		if explicit, ok := floors[measured.Package]; ok {
			floor = explicit
		}
		if measured.Coverage < floor.Value {
			report.Failures = append(report.Failures, Failure{
				Package: measured.Package,
				Floor:   floor.Value,
				Actual:  measured.Coverage,
			})
		}
	}

	return report, nil
}

// validateFloor rejects a configured floor the gate cannot compare against.
func validateFloor(name string, floor Floor) error {
	if math.IsNaN(floor.Value) || floor.Value < 0 || floor.Value > 100 {
		return fmt.Errorf("floor for %q is %s, which is outside 0..100", name, formatPercent(floor.Value))
	}
	return nil
}

// formatPercent renders a percentage without trailing zeros, matching the raw
// tokens the shell script echoes from the baseline and floors files.
func formatPercent(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// roundTo rounds value to digits decimal places using the same formatting
// rules as the shell script's printf, so a gate verdict cannot differ from it
// by one unit in the last place.
func roundTo(value float64, digits int) float64 {
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(value, 'f', digits, 64), 64)
	if err != nil {
		return value
	}
	return rounded
}
