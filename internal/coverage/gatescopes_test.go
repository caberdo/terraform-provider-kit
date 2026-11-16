package coverage_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

// scopeBaseline parses an inline key=value baseline.
func scopeBaseline(t *testing.T, content string) coverage.ScopeBaseline {
	t.Helper()

	baseline, err := coverage.ParseScopeBaseline(strings.NewReader(content))
	if err != nil {
		t.Fatalf("ParseScopeBaseline() error = %v", err)
	}
	return baseline
}

// scopeBlock appends one block for file with the given statement and hit
// counts. Files are full import paths, as a real profile records them.
func scopeBlock(profile *coverage.Profile, file string, statements, hits int) {
	profile.Blocks = append(profile.Blocks, coverage.Block{
		File:      file,
		StartLine: 1,
		StartCol:  1,
		EndLine:   2,
		EndCol:    2,
		NumStmt:   statements,
		Count:     hits,
	})
}

// scopeMeasure builds a profile whose internal/client package is covered at
// covered/(covered+uncovered), with no other package.
func scopeMeasure(covered, uncovered int) coverage.Profile {
	profile := coverage.Profile{Mode: "atomic"}
	if covered > 0 {
		scopeBlock(&profile, "github.com/x/y/internal/client/a.go", covered, 1)
	}
	if uncovered > 0 {
		scopeBlock(&profile, "github.com/x/y/internal/client/b.go", uncovered, 0)
	}
	return profile
}

func TestGateScopesFailsWhenAScopeDropsBelowItsFloor(t *testing.T) {
	baseline := scopeBaseline(t, `measured_internal_client=80
measured_total=90
floor_internal_client=85
floor_total=82.5
target=80
gold=90
`)

	report, err := coverage.GateScopes(scopeMeasure(800, 200), baseline, []string{"internal/client"})
	if err != nil {
		t.Fatalf("GateScopes() error = %v", err)
	}
	if got, want := report.Total, 80.0; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
	if got, want := len(report.Failures), 2; got != want {
		t.Fatalf("Failures = %+v, want %d", report.Failures, want)
	}
	for i, want := range []struct {
		pkg    string
		floor  float64
		actual float64
	}{
		{"internal/client", 85, 80},
		{coverage.TotalPackage, 82.5, 80},
	} {
		got := report.Failures[i]
		if got.Package != want.pkg || got.Floor != want.floor || got.Actual != want.actual {
			t.Errorf("Failures[%d] = %+v, want package %q floor %v actual %v",
				i, got, want.pkg, want.floor, want.actual)
		}
	}
}

func TestGateScopesFailsWhenADeclaredScopeIsMissingFromTheProfile(t *testing.T) {
	// A scope must not be able to drop out of the report while coverage
	// falls: the package exists, but the profile carries no block for it, so
	// it is measured at 0 and reported as a failure rather than skipped.
	baseline := scopeBaseline(t, `measured_internal_client=80
measured_internal_models=86
measured_total=83
floor_internal_client=79
floor_internal_models=85
floor_total=82
target=80
gold=90
`)

	report, err := coverage.GateScopes(scopeMeasure(800, 200), baseline,
		[]string{"internal/client", "internal/models"})
	if err != nil {
		t.Fatalf("GateScopes() error = %v", err)
	}
	if got, want := len(report.Failures), 2; got != want {
		t.Fatalf("Failures = %+v, want the absent scope and the total", report.Failures)
	}
	failure := report.Failures[0]
	if got, want := failure.Package, "internal/models"; got != want {
		t.Errorf("failure package = %q, want %q", got, want)
	}
	if got, want := failure.Actual, 0.0; got != want {
		t.Errorf("failure actual = %v, want the absent scope measured at %v", got, want)
	}
	if got, want := len(report.Scopes), 3; got != want {
		t.Fatalf("len(Scopes) = %d, want every declared scope reported", got)
	}
}

func TestGateScopesResolvesAPackageWhoseDirectoryHasAnUnderscore(t *testing.T) {
	// `_` in a scope key is not a separator to split on: the key is built from
	// the package path, so internal/datasources/idp_user is
	// internal_datasources_idp_user and must not be read as idp/user.
	profile := coverage.Profile{Mode: "atomic"}
	scopeBlock(&profile, "github.com/x/y/internal/datasources/idp_user/a.go", 10, 1)

	baseline := scopeBaseline(t, `measured_internal_datasources_idp_user=100
measured_total=100
floor_internal_datasources_idp_user=100
floor_total=100
target=80
gold=90
`)

	report, err := coverage.GateScopes(profile, baseline,
		[]string{"internal/datasources", "internal/datasources/idp_user"})
	if err != nil {
		t.Fatalf("GateScopes() error = %v, want the underscore directory resolved", err)
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %+v, want none", report.Failures)
	}
	var found *coverage.ScopeResult
	for i := range report.Scopes {
		if report.Scopes[i].Package == "internal/datasources/idp_user" {
			found = &report.Scopes[i]
		}
	}
	if found == nil {
		t.Fatalf("Scopes = %+v, want the idp_user package reported", report.Scopes)
	}
	if got, want := found.Actual, 100.0; got != want {
		t.Errorf("idp_user coverage = %v, want %v", got, want)
	}
}

func TestGateScopesFailsClosedWhenTwoPackagesShareAScopeKey(t *testing.T) {
	// internal/a/b and internal/a_b both render as internal_a_b. Picking one
	// silently would gate the wrong package, so the collision is an error.
	baseline := scopeBaseline(t, `measured_internal_a_b=100
measured_total=100
floor_internal_a_b=80
floor_total=80
target=80
gold=90
`)

	_, err := coverage.GateScopes(scopeMeasure(10, 0), baseline,
		[]string{"internal/a/b", "internal/a_b"})
	if err == nil {
		t.Fatal("GateScopes() error = nil, want a collision error")
	}
	for _, want := range []string{"internal/a/b", "internal/a_b"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err, want)
		}
	}
}

func TestGateScopesSuggestsScopeKeysForATypo(t *testing.T) {
	baseline := scopeBaseline(t, `measured_internal_client_aut=100
measured_total=100
floor_internal_client_aut=80
floor_total=80
target=80
gold=90
`)

	_, err := coverage.GateScopes(scopeMeasure(10, 0), baseline,
		[]string{"internal/client", "internal/client/auth"})
	if err == nil {
		t.Fatal("GateScopes() error = nil, want an unknown-scope error")
	}
	if !strings.Contains(err.Error(), "internal_client_auth") {
		t.Errorf("error = %q, want it to suggest the nearest scope key", err)
	}
}

func TestGateScopesFailsClosedOnAScopeThatIsNotAPackage(t *testing.T) {
	baseline := scopeBaseline(t, `measured_internal_gone=80
measured_total=83
floor_internal_gone=79
floor_total=82
target=80
gold=90
`)

	_, err := coverage.GateScopes(scopeMeasure(800, 200), baseline, []string{"internal/client"})
	if err == nil {
		t.Fatal("GateScopes() error = nil, want an unknown-scope error")
	}
	if !strings.Contains(err.Error(), "internal_gone") {
		t.Errorf("error = %q, want it to name the baseline key", err)
	}
}

func TestGateScopesPassesWhenEveryScopeMeetsItsFloor(t *testing.T) {
	baseline := scopeBaseline(t, `measured_internal_client=79.7
measured_total=83.4
floor_internal_client=79.0
floor_total=82.5
target=80
gold=90
`)

	report, err := coverage.GateScopes(scopeMeasure(1000, 0), baseline, []string{"internal/client"})
	if err != nil {
		t.Fatalf("GateScopes() error = %v", err)
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %+v, want none", report.Failures)
	}
	if got, want := len(report.Scopes), 2; got != want {
		t.Fatalf("len(Scopes) = %d, want %d", got, want)
	}
	if got, want := report.Scopes[0].Package, "internal/client"; got != want {
		t.Errorf("Scopes[0].Package = %q, want %q", got, want)
	}
	if got, want := report.Scopes[1].Package, coverage.TotalPackage; got != want {
		t.Errorf("Scopes[1].Package = %q, want the total last", got)
	}
	if got, want := report.Scopes[0].FloorText, "79.0"; got != want {
		t.Errorf("Scopes[0].FloorText = %q, want the floor token as written", got)
	}
}

func TestGateScopesDoesNotCountSubpackagesInTheParentScope(t *testing.T) {
	// The scripts aggregate one row per directory: a block in
	// internal/client/auth belongs to internal/client/auth, never to
	// internal/client.
	profile := coverage.Profile{Mode: "atomic"}
	scopeBlock(&profile, "github.com/x/y/internal/client/a.go", 10, 1)
	scopeBlock(&profile, "github.com/x/y/internal/client/auth/a.go", 10, 0)

	baseline := scopeBaseline(t, `measured_internal_client=100
measured_internal_client_auth=0
measured_total=50
floor_internal_client=100
floor_internal_client_auth=0
floor_total=50
target=80
gold=90
`)

	report, err := coverage.GateScopes(profile, baseline,
		[]string{"internal/client", "internal/client/auth"})
	if err != nil {
		t.Fatalf("GateScopes() error = %v", err)
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %+v, want none", report.Failures)
	}
	for _, scope := range report.Scopes {
		switch scope.Package {
		case "internal/client":
			if got, want := scope.Actual, 100.0; got != want {
				t.Errorf("internal/client = %v, want %v", got, want)
			}
		case "internal/client/auth":
			if got, want := scope.Actual, 0.0; got != want {
				t.Errorf("internal/client/auth = %v, want %v", got, want)
			}
		}
	}
}

func TestGateScopesComparesTheRoundedPercentageLikeTheScript(t *testing.T) {
	// coverage-summary.sh rounds a scope to two decimals before the gate
	// compares it, so 79.6999% is 79.70% and meets a 79.7 floor.
	baseline := scopeBaseline(t, `measured_internal_client=79.6999
measured_total=79.6999
floor_internal_client=79.7
floor_total=79.7
target=80
gold=90
`)

	report, err := coverage.GateScopes(scopeMeasure(796999, 203001), baseline, []string{"internal/client"})
	if err != nil {
		t.Fatalf("GateScopes() error = %v", err)
	}
	if len(report.Failures) != 0 {
		t.Errorf("Failures = %+v, want none: the script compares the rounded percentage", report.Failures)
	}
	if got, want := report.Total, 79.7; got != want {
		t.Errorf("Total = %v, want %v", got, want)
	}
}

func TestGateScopesRejectsInvalidBaselines(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*coverage.ScopeBaseline)
		want   string
	}{
		{"floor for an undeclared scope", func(b *coverage.ScopeBaseline) {
			b.Floors["internal_gone"] = 70
		}, "internal_gone"},
		{"no total", func(b *coverage.ScopeBaseline) {
			delete(b.Measured, coverage.TotalPackage)
			delete(b.Floors, coverage.TotalPackage)
		}, "total"},
		{"NaN floor", func(b *coverage.ScopeBaseline) {
			b.Floors["internal_client"] = math.NaN()
		}, "internal_client"},
		{"floor outside range", func(b *coverage.ScopeBaseline) {
			b.Floors["internal_client"] = 100.5
		}, "internal_client"},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := scopeBaseline(t, `measured_internal_client=80
measured_total=83
floor_internal_client=79
floor_total=82
target=80
gold=90
`)
			test.modify(&baseline)

			_, err := coverage.GateScopes(scopeMeasure(800, 200), baseline, []string{"internal/client"})
			if err == nil {
				t.Fatal("GateScopes() error = nil, want a configuration error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to name %q", err, test.want)
			}
		})
	}
}

// scopeRenderBaseline is the baseline the report tests render.
const scopeRenderBaseline = `measured_internal_client=80
measured_total=90
floor_internal_client=79.0
floor_total=82.5
target=80
gold=90
measured_commit=abc1234
measured_date=2026-10-19
`

// scopeRenderProfile covers internal/client at 80% and the module at 90%.
func scopeRenderProfile() coverage.Profile {
	profile := coverage.Profile{Mode: "atomic"}
	scopeBlock(&profile, "github.com/x/y/internal/client/a.go", 800, 1)
	scopeBlock(&profile, "github.com/x/y/internal/client/b.go", 200, 0)
	scopeBlock(&profile, "github.com/x/y/internal/helpers/a.go", 1000, 1)
	return profile
}

func renderScopeReport(t *testing.T, baselineText string, profile coverage.Profile, packages []string) string {
	t.Helper()

	baseline := scopeBaseline(t, baselineText)
	report, err := coverage.GateScopes(profile, baseline, packages)
	if err != nil {
		t.Fatalf("GateScopes() error = %v", err)
	}

	var out bytes.Buffer
	if err := coverage.WriteScopeReport(&out, baseline, report, "coverage.out", "baseline.txt"); err != nil {
		t.Fatalf("WriteScopeReport() error = %v", err)
	}
	return out.String()
}

func TestWriteScopeReportMatchesTheGravitinoTable(t *testing.T) {
	got := renderScopeReport(t, scopeRenderBaseline, scopeRenderProfile(),
		[]string{"internal/client", "internal/helpers"})

	want := "Merged statement coverage (unit + acceptance)\n" +
		"Profile:  coverage.out\n" +
		"Baseline: baseline.txt\n" +
		"\n" +
		"| Scope | Coverage | Floor | Delta |\n" +
		"|-------|----------|-------|-------|\n" +
		"| `internal/client` | 80.0% | 79.0% | +1.0 |\n" +
		"| `total` | 90.0% | 82.5% | +7.5 |\n" +
		"\n" +
		"coverage-gate: PASS — 2 scope(s) at or above their floor.\n" +
		"The 80% milestone is met.\n"
	if got != want {
		t.Errorf("report =\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteScopeReportPrintsTheFailureDetail(t *testing.T) {
	baselineText := strings.Replace(scopeRenderBaseline, "floor_internal_client=79.0", "floor_internal_client=85.0", 1)
	baselineText = strings.Replace(baselineText, "floor_total=82.5", "floor_total=95.0", 1)
	baselineText = strings.Replace(baselineText, "target=80", "target=85", 1)

	got := renderScopeReport(t, baselineText, scopeRenderProfile(),
		[]string{"internal/client", "internal/helpers"})

	want := "Merged statement coverage (unit + acceptance)\n" +
		"Profile:  coverage.out\n" +
		"Baseline: baseline.txt\n" +
		"\n" +
		"| Scope | Coverage | Floor | Delta |\n" +
		"|-------|----------|-------|-------|\n" +
		"| `internal/client` | 80.0% | 85.0% | -5.0 |\n" +
		"| `total` | 90.0% | 95.0% | -5.0 |\n" +
		"\n" +
		"coverage-gate: FAIL — merged coverage in coverage.out is below the floor in baseline.txt\n" +
		"  - internal/client: 80.00% is below the 85.0% floor\n" +
		"  - total: 90.00% is below the 95.0% floor\n" +
		"\n" +
		"Add tests for what you changed, or — only if the drop is deliberate and justified —\n" +
		"lower the floor in baseline.txt in the same pull request and run 'make coverage-readme'.\n"
	if got != want {
		t.Errorf("report =\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteScopeReportPrintsTheMilestoneFallback(t *testing.T) {
	baselineText := strings.Replace(scopeRenderBaseline, "target=80", "target=95", 1)
	baselineText = strings.Replace(baselineText, "gold=90", "gold=99", 1)

	got := renderScopeReport(t, baselineText, scopeRenderProfile(),
		[]string{"internal/client", "internal/helpers"})

	if !strings.Contains(got, "Milestones tracked for this project: 95% first, then 99%.\n") {
		t.Errorf("report =\n%s\nwant the milestone fallback naming both targets", got)
	}
}

func TestWriteScopeReportEchoesTheFloorToken(t *testing.T) {
	// The scripts echo the floor as written, so a file saying 79.0 must not
	// print 79 in the table.
	got := renderScopeReport(t, scopeRenderBaseline, scopeRenderProfile(),
		[]string{"internal/client", "internal/helpers"})
	if strings.Contains(got, "| 79% |") {
		t.Errorf("report =\n%s\nwant the floor echoed as 79.0%%", got)
	}
}

func TestWriteScopeReportRendersAnEmptyTotalSafely(t *testing.T) {
	// An empty profile has a 0% total, and the report must still render it
	// rather than divide by zero.
	baseline := scopeBaseline(t, `measured_total=83
floor_total=82
target=80
gold=90
`)
	report, err := coverage.GateScopes(coverage.Profile{Mode: "atomic"}, baseline, nil)
	if err != nil {
		t.Fatalf("GateScopes() error = %v", err)
	}
	var out bytes.Buffer
	if err := coverage.WriteScopeReport(&out, baseline, report, "coverage.out", "baseline.txt"); err != nil {
		t.Fatalf("WriteScopeReport() error = %v", err)
	}
	if !strings.Contains(out.String(), "| `total` | 0.0% | 82% | -82.0 |") {
		t.Errorf("report =\n%s\nwant a 0.0%% total row", out.String())
	}
}
