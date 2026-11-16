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
// the awk script this replaces compared whole lines. The badge pair is shared
// with the key=value scope dialect; only its body differs.
const (
	readmeStartMarker = "<!-- coverage:start -->"
	readmeEndMarker   = "<!-- coverage:end -->"

	// readmeFloorBadgeAnchor is the section the floor badge links to. The
	// scope badge keeps whatever link the repository gave it; the floor badge
	// always points at the section its sentence lives in.
	readmeFloorBadgeAnchor = "(#testing-and-coverage)"
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

// RenderReadme returns doc with both marker regions replaced by the blocks the
// bare-percentage dialect publishes: a one-line floor badge in the badge pair
// and the gate sentence in the coverage pair.
//
// Both pairs are required. A README carrying only one of them is reported as
// [ErrReadmeMarkers] rather than half rendered, because a badge that never
// moves again is easier to miss than a stale number.
//
// Only the marker regions change: every line outside them is copied whole. The
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
	badgeStart, badgeEnd, err := findMarkerPair(lines, readmeBadgeStartMarker, readmeBadgeEndMarker)
	if err != nil {
		return nil, err
	}
	start, end, err := findReadmeMarkers(lines)
	if err != nil {
		return nil, err
	}
	if badgeStart <= end && start <= badgeEnd {
		return nil, fmt.Errorf("%w: the %q and %q blocks overlap",
			ErrReadmeMarkers, readmeBadgeStartMarker, readmeStartMarker)
	}

	badge := []string{
		readmeBadgeStartMarker,
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-" + floor + "%25-green)]" +
			readmeFloorBadgeAnchor,
		readmeBadgeEndMarker,
	}
	block := []string{
		readmeStartMarker,
		"The coverage gate fails below **" + floor + "%** (`" + gateCommand + "`).",
		readmeEndMarker,
	}

	rendered := make([]string, 0, len(lines)+len(badge)+len(block))
	for i := 0; i < len(lines); i++ {
		switch i {
		case badgeStart:
			rendered = append(rendered, badge...)
			i = badgeEnd
		case start:
			rendered = append(rendered, block...)
			i = end
		default:
			rendered = append(rendered, lines[i])
		}
	}

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
