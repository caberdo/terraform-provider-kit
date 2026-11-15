package coverage_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

// block is one range repeated across the merge tests; only the parts a test
// varies are overwritten.
var block = coverage.Block{
	File:      "github.com/x/y/a.go",
	StartLine: 10,
	StartCol:  2,
	EndLine:   12,
	EndCol:    3,
	NumStmt:   3,
}

func TestMergeSumsCountsForIdenticalBlocks(t *testing.T) {
	unit := block
	unit.Count = 3
	acceptance := block
	acceptance.Count = 4

	merged, err := coverage.Merge([]coverage.Profile{
		{Mode: "atomic", Blocks: []coverage.Block{unit}},
		{Mode: "atomic", Blocks: []coverage.Block{acceptance}},
	})
	if err != nil {
		t.Fatalf("Merge() error = %v, want nil", err)
	}
	if got, want := merged.Mode, "atomic"; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
	if len(merged.Blocks) != 1 {
		t.Fatalf("len(Blocks) = %d, want 1", len(merged.Blocks))
	}
	if got, want := merged.Blocks[0].Count, 7; got != want {
		t.Errorf("Count = %d, want %d", got, want)
	}
	if got, want := merged.Blocks[0].NumStmt, 3; got != want {
		t.Errorf("NumStmt = %d, want %d", got, want)
	}
}

func TestMergeKeepsBlocksPresentInOnlyOneProfile(t *testing.T) {
	inUnit := block
	inUnit.Count = 1
	inAcceptance := block
	inAcceptance.File = "github.com/x/y/b.go"
	inAcceptance.Count = 2

	merged, err := coverage.Merge([]coverage.Profile{
		{Mode: "atomic", Blocks: []coverage.Block{inUnit}},
		{Mode: "atomic", Blocks: []coverage.Block{inAcceptance}},
	})
	if err != nil {
		t.Fatalf("Merge() error = %v, want nil", err)
	}
	want := []coverage.Block{inUnit, inAcceptance}
	if !slices.Equal(merged.Blocks, want) {
		t.Errorf("Blocks = %+v, want %+v", merged.Blocks, want)
	}
}

func TestMergeSumsRepeatedBlocksWithinOneProfile(t *testing.T) {
	// The shell script keys hits by range, so a range repeated inside one
	// profile is summed there too. Merging must not silently drop one copy.
	first := block
	first.Count = 2
	second := block
	second.Count = 5

	merged, err := coverage.Merge([]coverage.Profile{
		{Mode: "atomic", Blocks: []coverage.Block{first, second}},
	})
	if err != nil {
		t.Fatalf("Merge() error = %v, want nil", err)
	}
	if len(merged.Blocks) != 1 {
		t.Fatalf("len(Blocks) = %d, want 1", len(merged.Blocks))
	}
	if got, want := merged.Blocks[0].Count, 7; got != want {
		t.Errorf("Count = %d, want %d", got, want)
	}
}

func TestMergeRejectsDifferentCoverModes(t *testing.T) {
	_, err := coverage.Merge([]coverage.Profile{
		{Mode: "atomic"},
		{Mode: "set"},
	})
	if err == nil {
		t.Fatal("Merge() error = nil, want a mode mismatch error")
	}
	for _, mode := range []string{"atomic", "set"} {
		if !strings.Contains(err.Error(), mode) {
			t.Errorf("error = %q, want it to mention %q", err, mode)
		}
	}
}

func TestMergeRejectsConflictingStatementCounts(t *testing.T) {
	unit := block
	otherRevision := block
	otherRevision.NumStmt = 5

	_, err := coverage.Merge([]coverage.Profile{
		{Mode: "atomic", Blocks: []coverage.Block{unit}},
		{Mode: "atomic", Blocks: []coverage.Block{otherRevision}},
	})
	if err == nil {
		t.Fatal("Merge() error = nil, want a statement count conflict")
	}
	// The shell script would take the last value silently; a disagreement
	// can only mean the inputs came from different source revisions, so the
	// error must name the block a human has to look at.
	if !strings.Contains(err.Error(), "github.com/x/y/a.go:10.2,12.3") {
		t.Errorf("error = %q, want it to name the block", err)
	}
	if !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "5") {
		t.Errorf("error = %q, want it to mention both statement counts", err)
	}
}

func TestMergeDefaultsMissingModeToAtomic(t *testing.T) {
	merged, err := coverage.Merge([]coverage.Profile{
		{Blocks: []coverage.Block{block}},
		{Mode: "atomic", Blocks: []coverage.Block{block}},
	})
	if err != nil {
		t.Fatalf("Merge() error = %v, want nil", err)
	}
	if got, want := merged.Mode, "atomic"; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
}

func TestMergeOfNoProfilesReturnsEmptyAtomicProfile(t *testing.T) {
	merged, err := coverage.Merge(nil)

	if err != nil {
		t.Fatalf("Merge(nil) error = %v, want nil", err)
	}
	if got, want := merged.Mode, "atomic"; got != want {
		t.Errorf("Mode = %q, want %q", got, want)
	}
	if len(merged.Blocks) != 0 {
		t.Errorf("len(Blocks) = %d, want 0", len(merged.Blocks))
	}
}
