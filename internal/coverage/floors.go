package coverage

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// DefaultFloorKey is the [Floors] entry that applies to every package without
// an entry of its own. The floor files use the same literal key.
const DefaultFloorKey = "default"

// Floor is one configured minimum. Value is what the gate compares; Text is
// the token exactly as it was written in the floors file, which the report
// echoes so the output matches the shell script it replaces (for example
// `71.0` does not become `71`).
type Floor struct {
	Value float64
	Text  string
}

// Floors maps a repo-relative package name to its minimum statement coverage.
// The [DefaultFloorKey] entry is the fallback for packages without their own
// entry.
//
// A nil Floors means the provider has no floors file: only the module total is
// gated. An empty but non-nil map is a configuration error, because a floors
// file without `default` cannot cover a package that is not listed.
type Floors map[string]Floor

// percentagePattern is the shape the shell script accepts: a non-negative
// decimal with an optional fractional part and no exponent or sign.
var percentagePattern = regexp.MustCompile(`^[0-9]+([.][0-9]+)?$`)

// parsePercentage parses one percentage and rejects values outside 0..100.
func parsePercentage(text string) (float64, error) {
	if !percentagePattern.MatchString(text) {
		return 0, fmt.Errorf("%q is not a percentage", text)
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a percentage: %w", text, err)
	}
	if value < 0 || value > 100 {
		return 0, fmt.Errorf("%q is outside 0..100", text)
	}
	return value, nil
}

// ParseFloors reads a `<package> <percent>` floors file.
//
// Blank lines and `#` comments are ignored, and `default` may appear at any
// position: agentgateway's file puts it last. Every entry is validated and a
// duplicate is an error, so a typo cannot silently disable a floor. Each entry
// keeps the token as written in [Floor.Text] alongside its parsed value.
func ParseFloors(r io.Reader) (Floors, error) {
	floors := Floors{}

	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := raw
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = line[:comment]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("line %d: expected '<package> <percent>', got %q", lineNo, strings.TrimSpace(raw))
		}

		key := fields[0]
		if _, duplicate := floors[key]; duplicate {
			return nil, fmt.Errorf("line %d: duplicate entry for %q", lineNo, key)
		}
		value, err := parsePercentage(fields[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		floors[key] = Floor{Value: value, Text: fields[1]}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read floors: %w", err)
	}

	return floors, nil
}

// ReadBaseline reads the bare baseline dialect: a file whose only content is
// one percentage, optionally surrounded by blank and `#` comment lines.
//
// The other dialect in the providers is a `key=value` file with `measured_*`
// and `floor_*` keys. That dialect is not implemented yet, so it is rejected
// explicitly rather than misread as a bare percentage.
func ReadBaseline(r io.Reader) (float64, error) {
	var percentages []string

	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = line[:comment]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if equals := strings.IndexByte(line, '='); equals >= 0 {
			key := strings.TrimSpace(line[:equals])
			return 0, fmt.Errorf(
				"line %d: baseline uses the key=value dialect (%s=...), which this gate does not support; use a bare percentage",
				lineNo, key)
		}
		percentages = append(percentages, line)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read baseline: %w", err)
	}

	switch len(percentages) {
	case 0:
		return 0, fmt.Errorf("baseline has no percentage")
	case 1:
	default:
		return 0, fmt.Errorf("baseline has %d percentages, want exactly one", len(percentages))
	}

	return parsePercentage(percentages[0])
}
