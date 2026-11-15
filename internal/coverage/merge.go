package coverage

import "fmt"

// Merge combines profiles into one, adding the hit counts of blocks that cover
// the same range. Every block from every profile survives; the order is
// first-seen, and [WriteProfile] imposes the output order.
//
// The merged profile adopts the first input's mode, so a single `set` profile
// merges to `set` rather than being compared against a mode no input named.
// Profiles must then agree on that mode: counts from `set` and `atomic`
// profiles are not comparable, so mixing them is an error rather than a guess.
// A block whose statement count differs between profiles is also an error: the
// shell script this replaces would silently take the last value, but the only
// way two profiles disagree on how many statements a range spans is that they
// came from different source revisions.
func Merge(profiles []Profile) (Profile, error) {
	// An empty Mode marks "not adopted yet"; a normalised mode is never empty,
	// because a profile without a header means [DefaultMode].
	merged := Profile{}

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
		switch merged.Mode {
		case "":
			merged.Mode = mode
		case mode:
			// Same mode: nothing to adopt.
		default:
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

	if merged.Mode == "" {
		// No profiles at all: keep the documented default.
		merged.Mode = DefaultMode
	}

	return merged, nil
}
