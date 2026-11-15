package coverage

import "fmt"

// Merge combines profiles into one, adding the hit counts of blocks that cover
// the same range. Every block from every profile survives; the order is
// first-seen, and [WriteProfile] imposes the output order.
//
// Profiles must use the same cover mode: counts from `set` and `atomic`
// profiles are not comparable, so mixing them is an error rather than a guess.
// A block whose statement count differs between profiles is also an error: the
// shell script this replaces would silently take the last value, but the only
// way two profiles disagree on how many statements a range spans is that they
// came from different source revisions.
func Merge(profiles []Profile) (Profile, error) {
	merged := Profile{Mode: DefaultMode}

	type key struct {
		file      string
		startLine int
		startCol  int
		endLine   int
		endCol    int
	}
	index := make(map[key]int)

	for _, profile := range profiles {
		mode := profile.Mode
		if mode == "" {
			mode = DefaultMode
		}
		if mode != merged.Mode {
			return Profile{}, fmt.Errorf("cannot merge coverage profiles with different cover modes: %q and %q", merged.Mode, mode)
		}

		for _, block := range profile.Blocks {
			k := key{block.File, block.StartLine, block.StartCol, block.EndLine, block.EndCol}
			if at, seen := index[k]; seen {
				existing := &merged.Blocks[at]
				if existing.NumStmt != block.NumStmt {
					return Profile{}, fmt.Errorf(
						"block %s: statement count %d does not match %d in another profile",
						block, existing.NumStmt, block.NumStmt)
				}
				existing.Count += block.Count
				continue
			}
			index[k] = len(merged.Blocks)
			merged.Blocks = append(merged.Blocks, block)
		}
	}

	return merged, nil
}
