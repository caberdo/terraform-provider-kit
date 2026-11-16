package coverage

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The marker pair that bounds the floor block. The lines must match exactly, as
// the awk script this replaces compared whole lines.
const (
	readmeStartMarker = "<!-- coverage:start -->"
	readmeEndMarker   = "<!-- coverage:end -->"
)

// ErrReadmeMarkers reports that doc does not carry exactly one coverage marker
// pair, in order. The caller adds the README path, which a byte slice cannot
// carry. RenderReadme and CheckReadme both wrap it, so a caller can map it to a
// configuration exit code.
var ErrReadmeMarkers = errors.New("coverage markers are missing or malformed")

// ErrReadmeStale reports that [CheckReadme] found the block out of date with the
// baseline. It is the one error that means "rewrite the README", as opposed to
// "the README cannot be rendered".
var ErrReadmeStale = errors.New("README coverage block is out of date with the baseline")

// RenderReadme returns doc with everything between the coverage markers replaced
// by the floor block for baseline. The badge URL and the sentence match what
// scripts/coverage-readme.sh writes today, except for the gate command, which
// the caller supplies so the sentence can outlive that script.
//
// Only the marker region changes: every line outside it is copied whole. The
// result ends with a newline, as the awk script's `print` made it, so a README
// that had lost its final newline gains one.
func RenderReadme(doc []byte, baseline float64, gateCommand string) ([]byte, error) {
	floor, err := formatFloor(baseline)
	if err != nil {
		return nil, err
	}
	if err := validateGateCommand(gateCommand); err != nil {
		return nil, err
	}

	lines := splitReadmeLines(doc)
	start, end, err := findReadmeMarkers(lines)
	if err != nil {
		return nil, err
	}

	block := []string{
		readmeStartMarker,
		"![Coverage floor](https://img.shields.io/badge/coverage%20floor-" + floor + "%25-green)",
		"",
		"The coverage gate fails below **" + floor + "%** (`" + gateCommand + "`).",
		readmeEndMarker,
	}

	rendered := make([]string, 0, len(lines)+len(block))
	rendered = append(rendered, lines[:start]...)
	rendered = append(rendered, block...)
	rendered = append(rendered, lines[end+1:]...)

	return []byte(strings.Join(rendered, "\n") + "\n"), nil
}

// CheckReadme reports whether doc already carries the block RenderReadme would
// write. It never writes and never modifies doc, so a caller can check before it
// decides to rewrite. A stale block is [ErrReadmeStale]; a marker problem is
// [ErrReadmeMarkers].
func CheckReadme(doc []byte, baseline float64, gateCommand string) error {
	rendered, err := RenderReadme(doc, baseline, gateCommand)
	if err != nil {
		return err
	}
	if !bytes.Equal(rendered, doc) {
		return ErrReadmeStale
	}
	return nil
}

// splitReadmeLines splits doc into lines without their newline, so joining the
// result with "\n" reproduces the document.
func splitReadmeLines(doc []byte) []string {
	lines := strings.Split(string(doc), "\n")
	if last := len(lines) - 1; last >= 0 && lines[last] == "" {
		lines = lines[:last]
	}
	return lines
}

// findReadmeMarkers returns the indexes of the single start and end marker.
func findReadmeMarkers(lines []string) (int, int, error) {
	return findMarkerPair(lines, readmeStartMarker, readmeEndMarker)
}

// findMarkerPair returns the indexes of the single start and end marker of one
// pair. A pair that is missing, duplicated or reversed is an error: swapping a
// reversed pair would delete the text between the markers.
func findMarkerPair(lines []string, startMarker, endMarker string) (int, int, error) {
	starts, ends := -1, -1
	for i, line := range lines {
		switch line {
		case startMarker:
			if starts >= 0 {
				return 0, 0, fmt.Errorf("%w: found more than one %q", ErrReadmeMarkers, startMarker)
			}
			starts = i
		case endMarker:
			if ends >= 0 {
				return 0, 0, fmt.Errorf("%w: found more than one %q", ErrReadmeMarkers, endMarker)
			}
			ends = i
		}
	}

	switch {
	case starts < 0 && ends < 0:
		return 0, 0, fmt.Errorf("%w: found neither %q nor %q", ErrReadmeMarkers, startMarker, endMarker)
	case starts < 0:
		return 0, 0, fmt.Errorf("%w: found %q but no %q", ErrReadmeMarkers, endMarker, startMarker)
	case ends < 0:
		return 0, 0, fmt.Errorf("%w: found %q but no %q", ErrReadmeMarkers, startMarker, endMarker)
	case starts > ends:
		return 0, 0, fmt.Errorf("%w: %q appears after %q", ErrReadmeMarkers, startMarker, endMarker)
	}

	return starts, ends, nil
}

// formatFloor renders a floor the way the providers publish it: whole numbers
// bare (80) and everything else with one decimal (83.4). Printing 80 as 80.0
// would make every README stale on the first run.
//
// A floor the gate would reject is rejected here too, so a badge cannot promise
// a number `tfkit coverage gate` refuses to enforce.
func formatFloor(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return "", fmt.Errorf("baseline %v is outside 0..100", value)
	}
	if value == math.Trunc(value) {
		return strconv.FormatFloat(value, 'f', 0, 64), nil
	}
	return strconv.FormatFloat(value, 'f', 1, 64), nil
}

// validateGateCommand rejects a command that cannot sit inside the sentence's
// inline code span. A backtick would end the span early and a newline would
// split the block, so either one silently corrupts the README rather than
// failing.
func validateGateCommand(gateCommand string) error {
	switch {
	case gateCommand == "":
		return errors.New("gate command is empty")
	case strings.ContainsAny(gateCommand, "`\n\r"):
		return fmt.Errorf("gate command %q contains a backtick or a newline", gateCommand)
	}
	return nil
}
