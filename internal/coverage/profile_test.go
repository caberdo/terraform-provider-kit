package coverage_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

func TestParseProfileReadsModeAndBlocks(t *testing.T) {
	in := "mode: atomic\n" +
		"\n" +
		"github.com/x/y/a.go:10.2,12.3 3 1\n" +
		"github.com/x/y/b.go:1.1,2.2 2 0\n"

	profile, err := coverage.ParseProfile(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseProfile() error = %v, want nil", err)
	}
	if got, want := profile.Mode, "atomic"; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
	want := []coverage.Block{
		{File: "github.com/x/y/a.go", StartLine: 10, StartCol: 2, EndLine: 12, EndCol: 3, NumStmt: 3, Count: 1},
		{File: "github.com/x/y/b.go", StartLine: 1, StartCol: 1, EndLine: 2, EndCol: 2, NumStmt: 2, Count: 0},
	}
	if !slices.Equal(profile.Blocks, want) {
		t.Errorf("Blocks = %+v, want %+v", profile.Blocks, want)
	}
}

func TestParseProfileDefaultsToAtomicWithoutHeader(t *testing.T) {
	// The shell script this command replaces defaults a missing header to
	// `mode: atomic`, and older tooling emits profiles without one.
	in := "github.com/x/y/a.go:10.2,12.3 3 1\n"

	profile, err := coverage.ParseProfile(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseProfile() error = %v, want nil", err)
	}
	if got, want := profile.Mode, "atomic"; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
	if len(profile.Blocks) != 1 {
		t.Fatalf("len(Blocks) = %d, want 1", len(profile.Blocks))
	}
}

func TestParseProfileRejectsMalformedLine(t *testing.T) {
	// A valid header followed by a line with five fields: the shell script
	// silently skips anything it cannot split, this parser must not.
	in := "mode: atomic\n" +
		"github.com/x/y/a.go:10.2,12.3 3 1 extra\n"

	_, err := coverage.ParseProfile(strings.NewReader(in))
	if err == nil {
		t.Fatal("ParseProfile() error = nil, want an error naming the malformed line")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error = %q, want it to name line 2", err)
	}
}

func TestParseProfileRejectsModeHeaderWithoutMode(t *testing.T) {
	_, err := coverage.ParseProfile(strings.NewReader("mode:\n"))

	if err == nil {
		t.Fatal("ParseProfile() error = nil, want an error naming the empty mode header")
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error = %q, want it to name line 1", err)
	}
}

func TestWriteProfileWritesModeFirstAndSortsBlocks(t *testing.T) {
	profile := coverage.Profile{
		Mode: "atomic",
		Blocks: []coverage.Block{
			{File: "b.go", StartLine: 1, StartCol: 1, EndLine: 2, EndCol: 2, NumStmt: 1, Count: 4},
			{File: "a.go", StartLine: 20, StartCol: 2, EndLine: 22, EndCol: 3, NumStmt: 2, Count: 5},
			{File: "a.go", StartLine: 3, StartCol: 4, EndLine: 5, EndCol: 6, NumStmt: 1, Count: 6},
		},
	}

	var out bytes.Buffer
	if err := coverage.WriteProfile(&out, profile); err != nil {
		t.Fatalf("WriteProfile() error = %v, want nil", err)
	}

	// Sorted by (file, start line, start column) so two runs, or two Go
	// releases that iterate a map differently, produce the same bytes.
	want := "mode: atomic\n" +
		"a.go:3.4,5.6 1 6\n" +
		"a.go:20.2,22.3 2 5\n" +
		"b.go:1.1,2.2 1 4\n"
	if got := out.String(); got != want {
		t.Errorf("output =\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteProfileDefaultsMode(t *testing.T) {
	var out bytes.Buffer

	if err := coverage.WriteProfile(&out, coverage.Profile{}); err != nil {
		t.Fatalf("WriteProfile() error = %v, want nil", err)
	}
	if got, want := out.String(), "mode: atomic\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestParseProfileRejectsConflictingModeHeaders(t *testing.T) {
	in := "mode: atomic\n" +
		"mode: set\n"

	_, err := coverage.ParseProfile(strings.NewReader(in))
	if err == nil {
		t.Fatal("ParseProfile() error = nil, want a conflicting mode header error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error = %q, want it to name line 2", err)
	}
}

func TestParseProfileRejectsOutOfRangeNumber(t *testing.T) {
	in := "mode: atomic\n" +
		"github.com/x/y/a.go:99999999999999999999.1,2.2 1 1\n"

	_, err := coverage.ParseProfile(strings.NewReader(in))
	if err == nil {
		t.Fatal("ParseProfile() error = nil, want an out-of-range error")
	}
	if !strings.Contains(err.Error(), "start line") {
		t.Errorf("error = %q, want it to name the field that overflowed", err)
	}
}

func TestParseProfileReportsReadError(t *testing.T) {
	errRead := errors.New("read failed")

	_, err := coverage.ParseProfile(failingReader{err: errRead})
	if !errors.Is(err, errRead) {
		t.Errorf("ParseProfile() error = %v, want it to wrap %v", err, errRead)
	}
}

func TestWriteProfileOrdersBlocksThatShareAStart(t *testing.T) {
	// Blocks on one line differ only in their columns, and ranges that start
	// at the same column differ only in where they end. Without those
	// tie-breaks the order would depend on the input slice.
	profile := coverage.Profile{
		Mode: "atomic",
		Blocks: []coverage.Block{
			{File: "a.go", StartLine: 1, StartCol: 9, EndLine: 2, EndCol: 2, NumStmt: 1, Count: 1},
			{File: "a.go", StartLine: 1, StartCol: 5, EndLine: 3, EndCol: 4, NumStmt: 3, Count: 1},
			{File: "a.go", StartLine: 1, StartCol: 5, EndLine: 2, EndCol: 4, NumStmt: 2, Count: 1},
			{File: "a.go", StartLine: 1, StartCol: 5, EndLine: 2, EndCol: 3, NumStmt: 1, Count: 1},
		},
	}

	var out bytes.Buffer
	if err := coverage.WriteProfile(&out, profile); err != nil {
		t.Fatalf("WriteProfile() error = %v, want nil", err)
	}

	want := "mode: atomic\n" +
		"a.go:1.5,2.3 1 1\n" +
		"a.go:1.5,2.4 2 1\n" +
		"a.go:1.5,3.4 3 1\n" +
		"a.go:1.9,2.2 1 1\n"
	if got := out.String(); got != want {
		t.Errorf("output =\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteProfileReportsWriteError(t *testing.T) {
	errWrite := errors.New("write failed")

	err := coverage.WriteProfile(failingWriter{err: errWrite}, coverage.Profile{Mode: "atomic"})
	if !errors.Is(err, errWrite) {
		t.Errorf("WriteProfile() error = %v, want it to wrap %v", err, errWrite)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }
