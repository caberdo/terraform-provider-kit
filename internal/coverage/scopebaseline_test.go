package coverage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

// scopeFixture parses a fixture copied verbatim from a provider repository.
func scopeFixture(t *testing.T, name string) coverage.ScopeBaseline {
	t.Helper()

	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })

	baseline, err := coverage.ParseScopeBaseline(file)
	if err != nil {
		t.Fatalf("ParseScopeBaseline(%s) error = %v", name, err)
	}
	return baseline
}

func TestParseScopeBaselineReadsMeasuredFloorTargetGold(t *testing.T) {
	baseline := scopeFixture(t, "gravitino-coverage-baseline.txt")

	if got, want := len(baseline.Measured), 11; got != want {
		t.Errorf("len(Measured) = %d, want %d", got, want)
	}
	if got, want := len(baseline.Floors), 11; got != want {
		t.Errorf("len(Floors) = %d, want %d", got, want)
	}

	for scope, want := range map[string]float64{
		"internal_client":                79.7,
		"internal_models":                86.3,
		"internal_provider":              94.6,
		"internal_client_auth":           76.7,
		"internal_datasources_schema":    94.6,
		"internal_datasources_policy":    86.1,
		"internal_datasources_role":      85.6,
		"internal_datasources_fileset":   86.5,
		"internal_datasources_partition": 92.2,
		"internal_resources_partition":   92.4,
		"total":                          83.4,
	} {
		if got := baseline.Measured[scope]; got != want {
			t.Errorf("Measured[%q] = %v, want %v", scope, got, want)
		}
	}
	for scope, want := range map[string]float64{
		"internal_client":                79.0,
		"internal_datasources_partition": 91.5,
		"internal_resources_partition":   91.5,
		"total":                          82.5,
	} {
		if got := baseline.Floors[scope]; got != want {
			t.Errorf("Floors[%q] = %v, want %v", scope, got, want)
		}
	}

	if got, want := baseline.Target, 80.0; got != want {
		t.Errorf("Target = %v, want %v", got, want)
	}
	if got, want := baseline.Gold, 90.0; got != want {
		t.Errorf("Gold = %v, want %v", got, want)
	}
	if got, want := baseline.Commit, "c2fc783"; got != want {
		t.Errorf("Commit = %q, want %q", got, want)
	}
	if got, want := baseline.Date, "2026-10-19"; got != want {
		t.Errorf("Date = %q, want %q", got, want)
	}
}

func TestParseScopeBaselineReadsRustfsFile(t *testing.T) {
	baseline := scopeFixture(t, "rustfs-coverage-baseline.txt")

	for scope, want := range map[string]float64{
		"total":      90.2,
		"provider":   88.0,
		"pkg_rustfs": 97.8,
	} {
		if got := baseline.Measured[scope]; got != want {
			t.Errorf("Measured[%q] = %v, want %v", scope, got, want)
		}
	}
	for scope, want := range map[string]float64{
		"total":      89.5,
		"provider":   87.5,
		"pkg_rustfs": 97.3,
	} {
		if got := baseline.Floors[scope]; got != want {
			t.Errorf("Floors[%q] = %v, want %v", scope, got, want)
		}
	}
}

func TestParseScopeBaselineRequiresAFloorForEveryMeasuredScope(t *testing.T) {
	_, err := coverage.ParseScopeBaseline(strings.NewReader(`measured_internal_client=79.7
measured_total=83.4
floor_total=82.5
target=80
gold=90
`))
	if err == nil {
		t.Fatal("ParseScopeBaseline() error = nil, want an error for the missing floor")
	}
	if !strings.Contains(err.Error(), "internal_client") {
		t.Errorf("error = %q, want it to name the scope without a floor", err)
	}
}

func TestParseScopeBaselineRejectsAFloorWithoutAMeasuredScope(t *testing.T) {
	_, err := coverage.ParseScopeBaseline(strings.NewReader(`measured_total=83.4
floor_total=82.5
floor_internal_gone=70
target=80
gold=90
`))
	if err == nil {
		t.Fatal("ParseScopeBaseline() error = nil, want an error for the stray floor")
	}
	if !strings.Contains(err.Error(), "internal_gone") {
		t.Errorf("error = %q, want it to name the stray floor's scope", err)
	}
}

func TestParseScopeBaselineRequiresAMeasuredTotal(t *testing.T) {
	_, err := coverage.ParseScopeBaseline(strings.NewReader(`measured_internal_client=79.7
floor_internal_client=79.0
target=80
gold=90
`))
	if err == nil {
		t.Fatal("ParseScopeBaseline() error = nil, want an error for the missing total")
	}
	if !strings.Contains(err.Error(), "measured_total") {
		t.Errorf("error = %q, want it to name measured_total", err)
	}
}

func TestParseScopeBaselineFailsClosedOnUnparseablePercentage(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		key     string
	}{
		{
			"measured",
			"measured_internal_client=seventy\nmeasured_total=83.4\nfloor_internal_client=79.0\nfloor_total=82.5\ntarget=80\ngold=90\n",
			"measured_internal_client",
		},
		{
			"floor",
			"measured_internal_client=79.7\nmeasured_total=83.4\nfloor_internal_client=79.0%\nfloor_total=82.5\ntarget=80\ngold=90\n",
			"floor_internal_client",
		},
		{
			"target",
			"measured_total=83.4\nfloor_total=82.5\ntarget=eighty\ngold=90\n",
			"target",
		},
		{
			"gold",
			"measured_total=83.4\nfloor_total=82.5\ntarget=80\ngold=101\n",
			"gold",
		},
		{
			"outside range",
			"measured_total=83.4\nfloor_total=-1\ntarget=80\ngold=90\n",
			"floor_total",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := coverage.ParseScopeBaseline(strings.NewReader(test.content))
			if err == nil {
				t.Fatal("ParseScopeBaseline() error = nil, want an error for an unparseable percentage")
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Errorf("error = %q, want it to name %q", err, test.key)
			}
		})
	}
}

func TestParseScopeBaselineIgnoresCommitAndDateAsMeasurements(t *testing.T) {
	baseline, err := coverage.ParseScopeBaseline(strings.NewReader(`measured_total=83.4
floor_total=82.5
measured_commit=c2fc783
measured_date=2026-10-19
target=80
gold=90
`))
	if err != nil {
		t.Fatalf("ParseScopeBaseline() error = %v", err)
	}
	if got, want := len(baseline.Measured), 1; got != want {
		t.Errorf("len(Measured) = %d, want %d: commit and date are metadata", got, want)
	}
	if _, ok := baseline.Measured["commit"]; ok {
		t.Error("Measured[commit] exists, want the commit to stay metadata")
	}
	if _, ok := baseline.Measured["date"]; ok {
		t.Error("Measured[date] exists, want the date to stay metadata")
	}
}

func TestParseScopeBaselineRejectsMalformedFiles(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		want    string
	}{
		{"not key=value", "measured_total=83.4\nfloor_total=82.5\n80\n", "key=value"},
		{"unknown key", "measured_total=83.4\nfloor_total=82.5\nscope=80\n", "scope"},
		{"duplicate key", "measured_total=83.4\nmeasured_total=83.4\nfloor_total=82.5\n", "more than once"},
		{"empty value", "measured_total=\nfloor_total=82.5\n", "empty"},
		{"empty key", "=83.4\nfloor_total=82.5\n", "empty"},
		{"invalid scope", "measured_internal-client=83.4\nfloor_total=82.5\n", "internal-client"},
		{"no measured scopes", "floor_total=82.5\ntarget=80\ngold=90\n", "measured_"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := coverage.ParseScopeBaseline(strings.NewReader(test.content))
			if err == nil {
				t.Fatal("ParseScopeBaseline() error = nil, want a rejection")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestParseScopeBaselineRequiresTargetBelowGold(t *testing.T) {
	// A backwards milestone pair renders a sentence that reads "95% first,
	// then 90%", so it is a configuration error, as in gravitino's script.
	for _, test := range []struct {
		name string
		gold string
	}{
		{"target above gold", "90"},
		{"target equal to gold", "95"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := coverage.ParseScopeBaseline(strings.NewReader(`measured_total=83.4
floor_total=82.5
target=95
gold=` + test.gold + "\n"))
			if err == nil {
				t.Fatal("ParseScopeBaseline() error = nil, want a target/gold error")
			}
			for _, want := range []string{"95", test.gold} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to name %q", err, want)
				}
			}
		})
	}
}

func TestParseScopeBaselineAllowsTargetAndGoldToBeOmitted(t *testing.T) {
	// The milestones are optional here: only a file that names both must keep
	// them in order.
	baseline, err := coverage.ParseScopeBaseline(strings.NewReader("measured_total=83.4\nfloor_total=82.5\n"))
	if err != nil {
		t.Fatalf("ParseScopeBaseline() error = %v, want the milestones to stay optional", err)
	}
	if baseline.Target != 0 || baseline.Gold != 0 {
		t.Errorf("Target, Gold = %v, %v, want zero when absent", baseline.Target, baseline.Gold)
	}
}

func TestScopeDialectDetectsTheFloorKeys(t *testing.T) {
	// Prose in a comment names `floor_*` too, and must not flip the dialect:
	// only a real `floor_<scope>=` line does.
	scope := "measured_total=83.4\nfloor_total=82.5\n"
	if !coverage.ScopeDialect([]byte(scope)) {
		t.Error("ScopeDialect() = false, want true for a file with floor_total")
	}

	bare := "# this baseline has no floor_ keys; the floors are elsewhere\n83.4\n"
	if coverage.ScopeDialect([]byte(bare)) {
		t.Error("ScopeDialect() = true, want false for a bare percentage")
	}

	spaced := "  floor_internal_client = 79.0\n"
	if !coverage.ScopeDialect([]byte(spaced)) {
		t.Error("ScopeDialect() = false, want true for a padded floor key")
	}
}
