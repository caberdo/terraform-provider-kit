// Package coverage parses, merges and checks Go coverage profiles.
//
// The types here are the shared currency of the coverage subcommands: a
// profile is its cover mode plus the blocks it records, so a mode can never
// drift away from the blocks it describes.
package coverage

import (
	"bufio"
	"cmp"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// DefaultMode is the cover mode assumed when a profile has no `mode:` header.
// It matches the default of the shell script this package replaces.
const DefaultMode = "atomic"

// Block is one covered range in a Go cover profile: a source range, the
// number of statements it spans, and how often it was executed.
type Block struct {
	File      string
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
	NumStmt   int
	Count     int
}

// String returns the block's range in profile syntax, without the statement
// and hit counts: `file.go:12.34,56.78`.
func (b Block) String() string {
	return fmt.Sprintf("%s:%d.%d,%d.%d", b.File, b.StartLine, b.StartCol, b.EndLine, b.EndCol)
}

// Profile is a Go cover profile: the cover mode plus its blocks.
type Profile struct {
	Mode   string
	Blocks []Block
}

// blockPattern matches one profile line: `file:startLine.startCol,endLine.endCol
// numStmt count`. The file part is greedy so a path that contains a colon (as
// Windows paths do) still parses, and every numeric field is digits only.
var blockPattern = regexp.MustCompile(`^(.+):([0-9]+)\.([0-9]+),([0-9]+)\.([0-9]+) ([0-9]+) ([0-9]+)$`)

// ParseProfile reads a Go cover profile from r.
//
// The `mode:` header is optional and defaults to [DefaultMode], because the
// shell script this replaces also defaults it. A malformed line is an error
// naming its line number: silently skipping it, as the shell does, would drop
// data from the merged total.
func ParseProfile(r io.Reader) (Profile, error) {
	profile := Profile{Mode: DefaultMode}
	modeSet := false

	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "mode:") {
			mode := strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
			if mode == "" {
				return Profile{}, fmt.Errorf("line %d: mode header has no mode", lineNo)
			}
			if modeSet && mode != profile.Mode {
				return Profile{}, fmt.Errorf("line %d: mode %q conflicts with mode %q on an earlier line", lineNo, mode, profile.Mode)
			}
			profile.Mode = mode
			modeSet = true
			continue
		}

		block, err := parseBlock(line)
		if err != nil {
			return Profile{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
		profile.Blocks = append(profile.Blocks, block)
	}
	if err := scanner.Err(); err != nil {
		return Profile{}, fmt.Errorf("read profile: %w", err)
	}

	return profile, nil
}

// parseBlock parses one profile line into a Block.
func parseBlock(line string) (Block, error) {
	match := blockPattern.FindStringSubmatch(line)
	if match == nil {
		return Block{}, fmt.Errorf("%q is not a coverage block, want file:start.col,end.col statements count", line)
	}

	block := Block{File: match[1]}
	numbers := []struct {
		name  string
		text  string
		value *int
	}{
		{"start line", match[2], &block.StartLine},
		{"start column", match[3], &block.StartCol},
		{"end line", match[4], &block.EndLine},
		{"end column", match[5], &block.EndCol},
		{"statement count", match[6], &block.NumStmt},
		{"hit count", match[7], &block.Count},
	}
	for _, number := range numbers {
		value, err := strconv.Atoi(number.text)
		if err != nil {
			return Block{}, fmt.Errorf("%s %q in %q is out of range", number.name, number.text, line)
		}
		*number.value = value
	}

	return block, nil
}

// WriteProfile writes p to w as a Go cover profile.
//
// The `mode:` header is written first and blocks are sorted by file, start
// line and start column. The shell script this replaces emits awk hash order,
// which differs between runs and releases; a deterministic order is what makes
// the merged file comparable and testable.
func WriteProfile(w io.Writer, p Profile) error {
	mode := p.Mode
	if mode == "" {
		mode = DefaultMode
	}

	blocks := slices.Clone(p.Blocks)
	slices.SortFunc(blocks, compareBlocks)

	lines := make([]string, 0, len(blocks)+1)
	lines = append(lines, "mode: "+mode)
	for _, block := range blocks {
		lines = append(lines, fmt.Sprintf("%s %d %d", block, block.NumStmt, block.Count))
	}

	if _, err := io.WriteString(w, strings.Join(lines, "\n")+"\n"); err != nil {
		return fmt.Errorf("write profile: %w", err)
	}
	return nil
}

// compareBlocks orders blocks deterministically. File, start line and start
// column are the identity of a block; the remaining fields only break ties so
// that even a hand-built profile with duplicate ranges writes the same bytes
// every time.
func compareBlocks(a, b Block) int {
	if c := cmp.Compare(a.File, b.File); c != 0 {
		return c
	}
	if c := cmp.Compare(a.StartLine, b.StartLine); c != 0 {
		return c
	}
	if c := cmp.Compare(a.StartCol, b.StartCol); c != 0 {
		return c
	}
	if c := cmp.Compare(a.EndLine, b.EndLine); c != 0 {
		return c
	}
	if c := cmp.Compare(a.EndCol, b.EndCol); c != 0 {
		return c
	}
	if c := cmp.Compare(a.NumStmt, b.NumStmt); c != 0 {
		return c
	}
	return cmp.Compare(a.Count, b.Count)
}
