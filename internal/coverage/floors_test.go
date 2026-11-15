package coverage_test

import (
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

func TestReadBaselineAcceptsBarePercentage(t *testing.T) {
	got, err := coverage.ReadBaseline(strings.NewReader("80\n"))
	if err != nil {
		t.Fatalf("ReadBaseline() error = %v, want nil", err)
	}
	if got != 80 {
		t.Errorf("ReadBaseline() = %v, want 80", got)
	}
}

func TestReadBaselineIgnoresCommentAndBlankLines(t *testing.T) {
	// The bare dialect files in the providers hold only the number, but a
	// comment explaining it must not turn the file into a parse error.
	in := "# Statement-coverage floor for this module.\n\n80\n"

	got, err := coverage.ReadBaseline(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ReadBaseline() error = %v, want nil", err)
	}
	if got != 80 {
		t.Errorf("ReadBaseline() = %v, want 80", got)
	}
}

func TestReadBaselineRejectsKeyValueDialect(t *testing.T) {
	// gravitino and rustfs use `key=value` baselines. Guessing at that dialect
	// would gate them against a number that is not the enforced floor, so it
	// must fail closed until it is implemented deliberately.
	in := "measured_total=83.4\nfloor_total=82.5\ntarget=80\n"

	_, err := coverage.ReadBaseline(strings.NewReader(in))
	if err == nil {
		t.Fatal("ReadBaseline() error = nil, want a key=value dialect error")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("error = %q, want it to name the key=value dialect", err)
	}
}

func TestReadBaselineRejectsEmptyFile(t *testing.T) {
	_, err := coverage.ReadBaseline(strings.NewReader("# only a comment\n\n"))

	if err == nil {
		t.Fatal("ReadBaseline() error = nil, want an error for a file with no percentage")
	}
}

func TestReadBaselineRejectsOutOfRange(t *testing.T) {
	_, err := coverage.ReadBaseline(strings.NewReader("101\n"))

	if err == nil {
		t.Fatal("ReadBaseline() error = nil, want an out-of-range error")
	}
	if !strings.Contains(err.Error(), "101") {
		t.Errorf("error = %q, want it to name the value", err)
	}
}

func TestParseFloorsReadsDefaultAndPackages(t *testing.T) {
	floors, err := coverage.ParseFloors(strings.NewReader("default 70\ninternal/x 74\n"))
	if err != nil {
		t.Fatalf("ParseFloors() error = %v, want nil", err)
	}
	if got, want := len(floors), 2; got != want {
		t.Fatalf("len(floors) = %d, want %d", got, want)
	}
	if got, want := floors[coverage.DefaultFloorKey], 70.0; got != want {
		t.Errorf("floors[%q] = %v, want %v", coverage.DefaultFloorKey, got, want)
	}
	if got, want := floors["internal/x"], 74.0; got != want {
		t.Errorf("floors[%q] = %v, want %v", "internal/x", got, want)
	}
}

func TestParseFloorsAcceptsCommentsAndDefaultAnywhere(t *testing.T) {
	// agentgateway puts `default 50` after the package entries and comments
	// inside the body, so neither position nor indentation may matter.
	in := "# header\n" +
		"internal/providerdata 0\n" +
		"\n" +
		"# providerdata has no executable statements\n" +
		"  internal/x 74.5  # trailing comment\n" +
		"default 50\n"

	floors, err := coverage.ParseFloors(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseFloors() error = %v, want nil", err)
	}
	if got, want := floors["internal/x"], 74.5; got != want {
		t.Errorf("floors[internal/x] = %v, want %v", got, want)
	}
	if got, want := floors[coverage.DefaultFloorKey], 50.0; got != want {
		t.Errorf("floors[%q] = %v, want %v", coverage.DefaultFloorKey, got, want)
	}
	if got, want := floors["internal/providerdata"], 0.0; got != want {
		t.Errorf("floors[internal/providerdata] = %v, want %v", got, want)
	}
}

func TestParseFloorsRejectsNonPercentage(t *testing.T) {
	_, err := coverage.ParseFloors(strings.NewReader("default 70\ninternal/x not-a-number\n"))

	if err == nil {
		t.Fatal("ParseFloors() error = nil, want a non-percentage error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error = %q, want it to name line 2", err)
	}
	if !strings.Contains(err.Error(), "not-a-number") {
		t.Errorf("error = %q, want it to name the value", err)
	}
}

func TestParseFloorsRejectsOutOfRange(t *testing.T) {
	_, err := coverage.ParseFloors(strings.NewReader("default 101\n"))

	if err == nil {
		t.Fatal("ParseFloors() error = nil, want an out-of-range error")
	}
}

func TestParseFloorsRejectsDuplicateEntry(t *testing.T) {
	// A duplicate is how a floor silently stops applying: the script this
	// replaces dies rather than letting the last line win.
	_, err := coverage.ParseFloors(strings.NewReader("default 70\ninternal/x 70\ninternal/x 60\n"))

	if err == nil {
		t.Fatal("ParseFloors() error = nil, want a duplicate-entry error")
	}
	if !strings.Contains(err.Error(), "internal/x") {
		t.Errorf("error = %q, want it to name the duplicate package", err)
	}
}

func TestParseFloorsRejectsExtraFields(t *testing.T) {
	_, err := coverage.ParseFloors(strings.NewReader("default 70 extra\n"))

	if err == nil {
		t.Fatal("ParseFloors() error = nil, want an error for the extra field")
	}
}
