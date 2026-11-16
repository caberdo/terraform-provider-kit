package coverage

import (
	"bytes"
	"fmt"
	"math"
	"strings"
)

// The second marker pair, and the shape of the block it holds. A scope README
// carries a measured-total badge in the first pair and the per-scope ASCII table
// plus its provenance in the second.
const (
	readmeBadgeStartMarker = "<!-- coverage-badge:start -->"
	readmeBadgeEndMarker   = "<!-- coverage-badge:end -->"

	readmeBarWidth = 20
	readmeBarFull  = "█"
	readmeBarEmpty = "░"

	// readmeDefaultBadgeLink is the badge's link target when the existing badge
	// carries none. The target is a section heading, which is the repository's
	// choice (gravitino links `#coverage`, rustfs `#testing-and-coverage`), so
	// an existing one is kept.
	readmeDefaultBadgeLink = "(#coverage)"
	// readmeProvenancePrefix starts the one line the renderer regenerates from
	// `measured_commit`/`measured_date`.
	readmeProvenancePrefix = "Measured on `"
)

// RenderScopeReadme returns doc with both marker regions replaced by the blocks
// the `key=value` dialect publishes today: a measured-total badge whose colour
// follows the `target`/`gold` milestones, and a per-scope bar table with a floor
// column, a milestone on the total row and a provenance line.
//
// The renderer owns the numbers and their colour. Everything the baseline cannot
// carry is the repository's: the badge's link target, the milestone sentence's
// form (`-> 80%, then 90%` versus `milestones 80% and 90% met`), and any prose
// paragraph between the table and the provenance line. Those are kept, so a
// README stays byte-identical across a rewrite and gravitino's merge paragraph
// survives a floor bump.
//
// Both pairs are required. A README carrying only one of them is reported as
// [ErrReadmeMarkers] rather than half rendered, because a stale number is easier
// to notice than a block whose badge never moved again.
func RenderScopeReadme(doc []byte, baseline ScopeBaseline) ([]byte, error) {
	lines := splitReadmeLines(doc)

	badgeStart, badgeEnd, err := findMarkerPair(lines, readmeBadgeStartMarker, readmeBadgeEndMarker)
	if err != nil {
		return nil, err
	}
	blockStart, blockEnd, err := findMarkerPair(lines, readmeStartMarker, readmeEndMarker)
	if err != nil {
		return nil, err
	}
	if badgeStart <= blockEnd && blockStart <= badgeEnd {
		return nil, fmt.Errorf("%w: the %q and %q blocks overlap",
			ErrReadmeMarkers, readmeBadgeStartMarker, readmeStartMarker)
	}

	badge, err := scopeBadgeBlock(lines[badgeStart+1:badgeEnd], baseline)
	if err != nil {
		return nil, err
	}
	block, err := scopeCoverageBlock(lines[blockStart+1:blockEnd], baseline)
	if err != nil {
		return nil, err
	}

	rendered := make([]string, 0, len(lines)+len(badge)+len(block))
	for i := 0; i < len(lines); i++ {
		switch i {
		case badgeStart:
			rendered = append(rendered, badge...)
			i = badgeEnd
		case blockStart:
			rendered = append(rendered, block...)
			i = blockEnd
		default:
			rendered = append(rendered, lines[i])
		}
	}

	return []byte(strings.Join(rendered, "\n") + "\n"), nil
}

// CheckScopeReadme reports whether doc already carries the blocks
// RenderScopeReadme would write. It never writes and never modifies doc, so a
// caller can check before it decides to rewrite. A stale block is
// [ErrReadmeStale]; a marker problem is [ErrReadmeMarkers].
func CheckScopeReadme(doc []byte, baseline ScopeBaseline) error {
	rendered, err := RenderScopeReadme(doc, baseline)
	if err != nil {
		return err
	}
	if !bytes.Equal(rendered, doc) {
		return ErrReadmeStale
	}
	return nil
}

// scopeBadgeBlock renders the badge pair. old is the region's current body, read
// only for its link target.
func scopeBadgeBlock(old []string, baseline ScopeBaseline) ([]string, error) {
	total, ok := baseline.Measured[TotalPackage]
	if !ok {
		return nil, fmt.Errorf("baseline needs a measured_%s scope for the coverage badge", TotalPackage)
	}
	if err := validateScopeValue("measured_"+TotalPackage, total); err != nil {
		return nil, err
	}

	badge := fmt.Sprintf("[![Coverage](https://img.shields.io/badge/coverage-%s%%25-%s)]%s",
		formatPercent(total), readmeBadgeColor(total), readmeBadgeLink(old))
	return []string{readmeBadgeStartMarker, badge, readmeBadgeEndMarker}, nil
}

// scopeCoverageBlock renders the table pair: the fenced bar table in baseline
// order with the module total last, then the prose the repository owns, then the
// provenance line.
func scopeCoverageBlock(old []string, baseline ScopeBaseline) ([]string, error) {
	order := readmeScopeOrder(baseline, old)
	labels := make([]string, len(order))
	width := 0
	for i, scope := range order {
		labels[i] = readmeScopeLabel(scope)
		width = max(width, len(labels[i]))
	}

	style := readmeMilestoneStyleOf(old)
	target, targetText, gold, goldText := readmeMilestones(baseline)

	block := []string{readmeStartMarker, "```"}
	for i, scope := range order {
		measured := baseline.Measured[scope]
		if err := validateScopeValue("measured_"+scope, measured); err != nil {
			return nil, err
		}
		row := fmt.Sprintf("%-*s  %s  %s%%   floor %s%%", width, labels[i],
			readmeBar(measured), readmeMeasuredText(baseline, scope), readmeFloorText(baseline, scope))
		if scope == TotalPackage {
			row += "   " + readmeMilestone(measured, style, target, targetText, gold, goldText)
		}
		block = append(block, row)
	}
	block = append(block, "```", "")

	if prose := readmeScopeProse(old); len(prose) > 0 {
		block = append(block, prose...)
		block = append(block, "")
	}

	if baseline.Commit != "" {
		date := baseline.Date
		if date == "" {
			// rustfs's script falls back to `unknown` when only the commit is set.
			date = "unknown"
		}
		block = append(block, readmeProvenancePrefix+baseline.Commit+"` (unit + acceptance) on "+date+".")
	}

	return append(block, readmeEndMarker), nil
}

// readmeScopeLabel renders a scope key as the table's label: `_` is the path
// separator, and the module total stays bare.
func readmeScopeLabel(scope string) string {
	if scope == TotalPackage {
		return TotalPackage
	}
	return strings.ReplaceAll(scope, "_", "/")
}

// readmeFloorText echoes the baseline's floor token, because the scripts print
// `79.0` and not `79`. A baseline built without a parse falls back to the number.
func readmeFloorText(baseline ScopeBaseline, scope string) string {
	if text := baseline.floorText[scope]; text != "" {
		return text
	}
	return formatPercent(baseline.Floors[scope])
}

// readmeMeasuredText echoes the baseline's measurement token for the same
// reason, falling back to the number for a baseline built without a parse.
func readmeMeasuredText(baseline ScopeBaseline, scope string) string {
	if text := baseline.measuredText[scope]; text != "" {
		return text
	}
	return formatPercent(baseline.Measured[scope])
}

// readmeScopeOrder returns the table's row order: the scopes the table already
// lists, in their order, then any scope the baseline adds, then the module total.
// The row order is presentation, not data — rustfs lists `pkg/rustfs` before
// `provider` while its baseline lists `provider` first — so a rewrite keeps the
// chart a repository already publishes.
func readmeScopeOrder(baseline ScopeBaseline, old []string) []string {
	order := make([]string, 0, len(baseline.Measured))
	seen := map[string]bool{TotalPackage: true}
	for _, scope := range readmeTableOrder(old) {
		if _, ok := baseline.Measured[scope]; !ok || seen[scope] {
			continue
		}
		order = append(order, scope)
		seen[scope] = true
	}
	for _, scope := range scopeOrder(baseline) {
		if seen[scope] {
			continue
		}
		order = append(order, scope)
		seen[scope] = true
	}
	return append(order, TotalPackage)
}

// readmeTableOrder reads the scope keys of the fenced table in old, in order.
func readmeTableOrder(old []string) []string {
	var order []string
	inFence := false
	for _, line := range old {
		if line == "```" {
			inFence = !inFence
			continue
		}
		if !inFence {
			continue
		}
		label, _, ok := strings.Cut(line, "  ")
		if !ok || label == TotalPackage {
			continue
		}
		order = append(order, strings.ReplaceAll(label, "/", "_"))
	}
	return order
}

// readmeBar draws the measured percentage as a fixed-width bar, rounding the
// filled width the way the scripts' awk does (`int(pct * width / 100 + 0.5)`).
func readmeBar(measured float64) string {
	filled := int(math.Floor(measured*readmeBarWidth/100 + 0.5))
	filled = min(max(filled, 0), readmeBarWidth)
	return strings.Repeat(readmeBarFull, filled) + strings.Repeat(readmeBarEmpty, readmeBarWidth-filled)
}

// readmeBadgeColor is shields.io's colour for the measured total, at the
// thresholds both scripts use.
func readmeBadgeColor(measured float64) string {
	switch {
	case measured >= 90:
		return "brightgreen"
	case measured >= 80:
		return "green"
	case measured >= 70:
		return "yellow"
	case measured >= 60:
		return "orange"
	default:
		return "red"
	}
}

// readmeBadgeLink returns the link target the existing badge already uses.
func readmeBadgeLink(old []string) string {
	for _, line := range old {
		marker := strings.LastIndex(line, "](")
		if marker < 0 {
			continue
		}
		link := line[marker+1:]
		if strings.HasPrefix(link, "(#") && strings.HasSuffix(link, ")") {
			return link
		}
	}
	return readmeDefaultBadgeLink
}

// readmeMilestoneStyle is the form of the total row's milestone sentence.
type readmeMilestoneStyle int

const (
	// readmeArrowMilestone names the milestones unconditionally:
	// `-> 80%, then 90%`.
	readmeArrowMilestone readmeMilestoneStyle = iota
	// readmeStepMilestone reports which milestone is met: `80% met; 90% next`
	// and `milestones 80% and 90% met`.
	readmeStepMilestone
)

// readmeMilestoneStyleOf reads the form from the table being replaced, so each
// repository keeps the sentence its own script writes.
func readmeMilestoneStyleOf(old []string) readmeMilestoneStyle {
	for _, line := range old {
		tail, ok := readmeTotalRowTail(line)
		if !ok {
			continue
		}
		if strings.Contains(tail, "milestones") ||
			strings.Contains(tail, " met") || strings.Contains(tail, " next") {
			return readmeStepMilestone
		}
		return readmeArrowMilestone
	}
	return readmeArrowMilestone
}

// readmeTotalRowTail returns the sentence after the floor column of an existing
// total row.
func readmeTotalRowTail(line string) (string, bool) {
	if !strings.HasPrefix(line, TotalPackage+" ") {
		return "", false
	}
	floor := strings.Index(line, "floor ")
	if floor < 0 {
		return "", false
	}
	rest := line[floor+len("floor "):]
	percent := strings.IndexByte(rest, '%')
	if percent < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[percent+1:]), true
}

// readmeMilestones returns the milestone numbers and the tokens as written. A
// baseline that names neither keeps rustfs's 80/90 defaults.
func readmeMilestones(baseline ScopeBaseline) (target float64, targetText string, gold float64, goldText string) {
	target, gold = 80, 90
	targetText, goldText = formatPercent(target), formatPercent(gold)
	if baseline.hasTarget {
		target = baseline.Target
		if baseline.targetText != "" {
			targetText = baseline.targetText
		}
	}
	if baseline.hasGold {
		gold = baseline.Gold
		if baseline.goldText != "" {
			goldText = baseline.goldText
		}
	}
	return target, targetText, gold, goldText
}

// readmeMilestone writes the total row's sentence in the given form.
func readmeMilestone(
	total float64,
	style readmeMilestoneStyle,
	target float64, targetText string,
	gold float64, goldText string,
) string {
	if style == readmeStepMilestone {
		switch {
		case total >= gold:
			return fmt.Sprintf("milestones %s%% and %s%% met", targetText, goldText)
		case total >= target:
			return fmt.Sprintf("%s%% met; %s%% next", targetText, goldText)
		}
	}
	return fmt.Sprintf("-> %s%%, then %s%%", targetText, goldText)
}

// readmeScopeProse returns the lines a repository owns inside the table block:
// everything outside the fence except the generated provenance line, with the
// blank edges trimmed. The fenced table itself is always regenerated.
func readmeScopeProse(old []string) []string {
	prose := make([]string, 0, len(old))
	inFence := false
	for _, line := range old {
		switch {
		case line == "```":
			inFence = !inFence
		case inFence:
		case strings.HasPrefix(line, readmeProvenancePrefix) && strings.HasSuffix(line, "."):
		default:
			prose = append(prose, line)
		}
	}
	for len(prose) > 0 && prose[0] == "" {
		prose = prose[1:]
	}
	for len(prose) > 0 && prose[len(prose)-1] == "" {
		prose = prose[:len(prose)-1]
	}
	return prose
}
