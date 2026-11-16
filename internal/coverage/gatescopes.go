package coverage

import (
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
)

// ScopeResult is one row of a [GateScopes] report: the measured coverage of one
// scope and the floor it is compared against. Package is the scope rendered as
// a package path (`internal/client`) or [TotalPackage] for the module.
type ScopeResult struct {
	Package   string
	Actual    float64
	Floor     float64
	FloorText string
}

// GateScopes checks a profile against a `key=value` scope baseline.
//
// Every scope the baseline names is reported, in the baseline's order with the
// module total last, and every scope below its floor is returned in the report.
// A configuration problem is an error so the caller can fail closed: a scope
// without a floor or floor without a scope, a percentage outside 0..100, or a
// scope key that does not resolve to a package `go list` returned.
//
// A declared scope whose package has no statements in the profile is measured
// at 0 and stays in the report as a failure, rather than being skipped: a scope
// must not be able to drop out of the table while coverage falls.
//
// Coverage is compared at two decimal places, because the shell scripts this
// replaces summarise the profile with `printf "%.2f"` before comparing it.
func GateScopes(p Profile, b ScopeBaseline, packages []string) (Report, error) {
	if len(b.Measured) == 0 {
		return Report{}, fmt.Errorf("baseline defines no measured_<scope> keys")
	}
	if _, ok := b.Measured[TotalPackage]; !ok {
		return Report{}, fmt.Errorf("baseline needs a measured_%s scope", TotalPackage)
	}
	if _, ok := b.Floors[TotalPackage]; !ok {
		return Report{}, fmt.Errorf("baseline has measured_%s but no floor_%s", TotalPackage, TotalPackage)
	}
	for _, scope := range slices.Sorted(maps.Keys(b.Floors)) {
		if _, ok := b.Measured[scope]; !ok {
			return Report{}, fmt.Errorf("baseline has floor_%s but no measured_%s", scope, scope)
		}
	}

	// Resolve every scope to the package it measures before looking at the
	// profile, so a rotted key is reported as the configuration problem it is.
	paths := make(map[string]string, len(b.Measured))
	for _, scope := range slices.Sorted(maps.Keys(b.Measured)) {
		if scope == TotalPackage {
			continue
		}
		if _, ok := b.Floors[scope]; !ok {
			return Report{}, fmt.Errorf("baseline has measured_%s but no floor_%s", scope, scope)
		}
		if err := validateScopeValue("measured_"+scope, b.Measured[scope]); err != nil {
			return Report{}, err
		}
		path := scopePath(scope)
		if !slices.Contains(packages, path) {
			return Report{}, fmt.Errorf(
				"scope %q maps to package %q, which the package list does not contain", scope, path)
		}
		paths[scope] = path
	}
	for _, scope := range slices.Sorted(maps.Keys(b.Floors)) {
		if err := validateScopeValue("floor_"+scope, b.Floors[scope]); err != nil {
			return Report{}, err
		}
	}
	if err := validateScopeValue("measured_"+TotalPackage, b.Measured[TotalPackage]); err != nil {
		return Report{}, err
	}

	tallies := tallyPackages(p, packages)
	report := Report{Total: totalCoverage(p, 2)}

	for _, scope := range scopeOrder(b) {
		actual := report.Total
		path := TotalPackage
		if scope != TotalPackage {
			path = paths[scope]
			// A package that exists but writes no profile records has no
			// tests, so it is measured at 0 and fails its floor. Skipping it
			// would let a scope drop out of the table exactly when coverage
			// fell.
			tally := tallies[path]
			actual = 0
			if tally != nil && tally.statements > 0 {
				actual = roundTo(100*float64(tally.covered)/float64(tally.statements), 2)
			}
		}

		floor := b.Floors[scope]
		result := ScopeResult{
			Package:   path,
			Actual:    actual,
			Floor:     floor,
			FloorText: b.floorText[scope],
		}
		report.Scopes = append(report.Scopes, result)
		if actual < floor {
			report.Failures = append(report.Failures, Failure{
				Package: path,
				Floor:   floor,
				Actual:  actual,
			})
		}
	}

	return report, nil
}

// WriteScopeReport writes a [GateScopes] result in the shape gravitino's
// coverage-gate.sh prints: the provenance header, the scope table in baseline
// order with total last, and either the failure detail or the pass line with
// the milestone sentence.
func WriteScopeReport(w io.Writer, baseline ScopeBaseline, report Report, profilePath, baselinePath string) error {
	var out strings.Builder

	out.WriteString("Merged statement coverage (unit + acceptance)\n")
	fmt.Fprintf(&out, "Profile:  %s\n", profilePath)
	fmt.Fprintf(&out, "Baseline: %s\n\n", baselinePath)
	out.WriteString("| Scope | Coverage | Floor | Delta |\n")
	out.WriteString("|-------|----------|-------|-------|\n")
	for _, scope := range report.Scopes {
		fmt.Fprintf(&out, "| `%s` | %.1f%% | %s%% | %+.1f |\n",
			scope.Package, scope.Actual, scopeFloorText(scope), scope.Actual-scope.Floor)
	}
	out.WriteString("\n")

	if len(report.Failures) > 0 {
		fmt.Fprintf(&out, "coverage-gate: FAIL — merged coverage in %s is below the floor in %s\n",
			profilePath, baselinePath)
		for _, failure := range report.Failures {
			fmt.Fprintf(&out, "  - %s: %.2f%% is below the %s%% floor\n",
				failure.Package, failure.Actual, failureFloorText(report, failure))
		}
		out.WriteString("\n")
		out.WriteString("Add tests for what you changed, or — only if the drop is deliberate and justified —\n")
		fmt.Fprintf(&out, "lower the floor in %s in the same pull request and run 'make coverage-readme'.\n",
			baselinePath)
	} else {
		fmt.Fprintf(&out, "coverage-gate: PASS — %d scope(s) at or above their floor.\n", len(report.Scopes))
		if baseline.hasTarget {
			targetText := baseline.targetText
			if targetText == "" {
				targetText = formatPercent(baseline.Target)
			}
			goldText := "90" // rustfs's script falls back to 90 when gold is absent.
			if baseline.hasGold {
				goldText = baseline.goldText
				if goldText == "" {
					goldText = formatPercent(baseline.Gold)
				}
			}
			if report.Total >= baseline.Target {
				fmt.Fprintf(&out, "The %s%% milestone is met.\n", targetText)
			} else {
				fmt.Fprintf(&out, "Milestones tracked for this project: %s%% first, then %s%%.\n",
					targetText, goldText)
			}
		}
	}

	if _, err := io.WriteString(w, out.String()); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// scopeOrder returns the baseline's measured scopes in file order with the
// module total last, which is how the shell script orders the table. A baseline
// built without a parse (and so without an order) falls back to sorted keys so
// the output stays deterministic.
func scopeOrder(b ScopeBaseline) []string {
	order := make([]string, 0, len(b.Measured))
	seen := make(map[string]bool, len(b.Measured))
	for _, scope := range b.order {
		if scope == TotalPackage || seen[scope] {
			continue
		}
		if _, ok := b.Measured[scope]; !ok {
			continue
		}
		order = append(order, scope)
		seen[scope] = true
	}
	for _, scope := range slices.Sorted(maps.Keys(b.Measured)) {
		if scope == TotalPackage || seen[scope] {
			continue
		}
		order = append(order, scope)
	}
	return append(order, TotalPackage)
}

// scopePath renders a scope key as the package it measures. `_` is the path
// separator in the key, so `internal_client` is `internal/client`.
func scopePath(scope string) string {
	return strings.ReplaceAll(scope, "_", "/")
}

// validateScopeValue rejects a configured percentage the gate cannot compare
// safely: a NaN or out-of-range value makes every comparison false and would
// let the gate fail open.
func validateScopeValue(key string, value float64) error {
	if math.IsNaN(value) || value < 0 || value > 100 {
		return fmt.Errorf("%s is %s, which is outside 0..100", key, formatPercent(value))
	}
	return nil
}

// scopeFloorText returns the floor token as written, falling back to the
// numeric value for a baseline built without a parse.
func scopeFloorText(scope ScopeResult) string {
	if scope.FloorText != "" {
		return scope.FloorText
	}
	return formatPercent(scope.Floor)
}

// failureFloorText finds the floor token for a failure's scope.
func failureFloorText(report Report, failure Failure) string {
	for _, scope := range report.Scopes {
		if scope.Package == failure.Package {
			return scopeFloorText(scope)
		}
	}
	return formatPercent(failure.Floor)
}
