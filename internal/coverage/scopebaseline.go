package coverage

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// ScopeBaseline is the `key=value` coverage baseline that gravitino and rustfs
// commit: one measurement and one floor per scope, plus the milestone and
// provenance metadata.
//
// Measured and Floors are keyed by the scope as written in the file, without
// its prefix (`internal_client`), not by package (`internal/client`). A key is
// the package path with `/` written as `_`, so a package whose own directory
// name contains an underscore keeps it: `internal/datasources/idp_user` is
// `internal_datasources_idp_user`. [TotalPackage] is the whole-module scope.
// Commit and Date are metadata: they describe the last measurement and are
// never compared as percentages.
type ScopeBaseline struct {
	Measured map[string]float64
	Floors   map[string]float64
	Target   float64
	Gold     float64
	Commit   string
	Date     string

	// order is the file's measured_* order. The report uses it so the table
	// reads in the same order as the file, with total last.
	order []string
	// floorText keeps each floor token as written, because the scripts echo
	// the token (`79.0`, not `79`) into the table.
	floorText map[string]string
	// targetText and goldText keep the milestone tokens as written, and
	// hasTarget/hasGold record whether the file named them at all.
	targetText string
	goldText   string
	hasTarget  bool
	hasGold    bool
}

// ParseScopeBaseline reads a `key=value` scope baseline.
//
// Blank lines and lines whose first non-space character is `#` are ignored.
// Every non-comment line must be `key=value` with a non-empty key and value, and
// no key may appear twice. The keys are `measured_<scope>`, `floor_<scope>`,
// `target`, `gold`, `measured_commit` and `measured_date`; an unknown key is an
// error, so a typo cannot silently disable a scope.
//
// The rules that keep a scope from slipping out of the report are enforced
// here: there must be a `measured_total`, every `measured_<scope>` needs a
// `floor_<scope>` and every `floor_<scope>` needs a `measured_<scope>`, and
// every percentage must be a non-negative decimal inside 0..100. The scope
// itself is `[A-Za-z0-9_]+`: a key is the package path with `/` written as `_`,
// so `internal_client` is the package `internal/client` and
// `internal_datasources_idp_user` is `internal/datasources/idp_user`.
//
// `target` and `gold` are optional, but when both are present `target` must be
// below `gold`, or the milestone sentence would read backwards.
func ParseScopeBaseline(r io.Reader) (ScopeBaseline, error) {
	baseline := ScopeBaseline{
		Measured:  map[string]float64{},
		Floors:    map[string]float64{},
		floorText: map[string]string{},
	}
	seen := map[string]bool{}

	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimSuffix(scanner.Text(), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		equals := strings.IndexByte(line, '=')
		if equals < 0 {
			return ScopeBaseline{}, fmt.Errorf("line %d: %q is not 'key=value'", lineNo, line)
		}
		key := strings.TrimSpace(line[:equals])
		value := strings.TrimSpace(line[equals+1:])
		if key == "" || value == "" {
			return ScopeBaseline{}, fmt.Errorf("line %d: has an empty key or value", lineNo)
		}
		if seen[key] {
			return ScopeBaseline{}, fmt.Errorf("line %d: defines %q more than once", lineNo, key)
		}
		seen[key] = true

		switch key {
		case "measured_commit":
			baseline.Commit = value
		case "measured_date":
			baseline.Date = value
		case "target":
			percentage, err := parseScopePercentage(key, value)
			if err != nil {
				return ScopeBaseline{}, fmt.Errorf("line %d: %w", lineNo, err)
			}
			baseline.Target = percentage
			baseline.targetText = value
			baseline.hasTarget = true
		case "gold":
			percentage, err := parseScopePercentage(key, value)
			if err != nil {
				return ScopeBaseline{}, fmt.Errorf("line %d: %w", lineNo, err)
			}
			baseline.Gold = percentage
			baseline.goldText = value
			baseline.hasGold = true
		default:
			switch {
			case strings.HasPrefix(key, "measured_"):
				scope := strings.TrimPrefix(key, "measured_")
				if err := validateScopeName(scope); err != nil {
					return ScopeBaseline{}, fmt.Errorf("line %d: %w", lineNo, err)
				}
				percentage, err := parseScopePercentage(key, value)
				if err != nil {
					return ScopeBaseline{}, fmt.Errorf("line %d: %w", lineNo, err)
				}
				baseline.Measured[scope] = percentage
				baseline.order = append(baseline.order, scope)
			case strings.HasPrefix(key, "floor_"):
				scope := strings.TrimPrefix(key, "floor_")
				if err := validateScopeName(scope); err != nil {
					return ScopeBaseline{}, fmt.Errorf("line %d: %w", lineNo, err)
				}
				percentage, err := parseScopePercentage(key, value)
				if err != nil {
					return ScopeBaseline{}, fmt.Errorf("line %d: %w", lineNo, err)
				}
				baseline.Floors[scope] = percentage
				baseline.floorText[scope] = value
			default:
				return ScopeBaseline{}, fmt.Errorf("line %d: has an unknown key %q", lineNo, key)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ScopeBaseline{}, fmt.Errorf("read baseline: %w", err)
	}

	if len(baseline.Measured) == 0 {
		return ScopeBaseline{}, fmt.Errorf("baseline defines no measured_<scope> keys")
	}
	if _, ok := baseline.Measured[TotalPackage]; !ok {
		return ScopeBaseline{}, fmt.Errorf("baseline needs a measured_%s scope", TotalPackage)
	}
	for _, scope := range baseline.order {
		if _, ok := baseline.Floors[scope]; !ok {
			return ScopeBaseline{}, fmt.Errorf("baseline has measured_%s but no floor_%s", scope, scope)
		}
	}
	for _, scope := range slices.Sorted(maps.Keys(baseline.Floors)) {
		if _, ok := baseline.Measured[scope]; !ok {
			return ScopeBaseline{}, fmt.Errorf("baseline has floor_%s but no measured_%s", scope, scope)
		}
	}
	if baseline.hasTarget && baseline.hasGold && baseline.Target >= baseline.Gold {
		return ScopeBaseline{}, fmt.Errorf(
			"baseline needs target (%s) below gold (%s)", baseline.targetText, baseline.goldText)
	}

	return baseline, nil
}

// parseScopePercentage parses one percentage and names the key it came from, so
// a bad value is reported as `measured_internal_client: "x" is not a
// percentage`.
func parseScopePercentage(key, value string) (float64, error) {
	percentage, err := parsePercentage(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return percentage, nil
}

// validateScopeName rejects a scope key the shell scripts would not accept.
func validateScopeName(scope string) error {
	if scope == "" {
		return fmt.Errorf("has an empty scope")
	}
	for _, r := range scope {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("has an invalid scope %q", scope)
	}
	return nil
}

// ScopeDialect reports whether data is the `key=value` dialect: some non-comment
// line has a key starting with `floor_`.
//
// Prose in a comment is deliberately ignored, and a bare-percentage baseline can
// never contain a `floor_` key, so the test cannot pick the scope parser for a
// file whose gate has no scope floors.
func ScopeDialect(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		equals := strings.IndexByte(line, '=')
		if equals < 0 {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line[:equals]), "floor_") {
			return true
		}
	}
	return false
}
