package coverage_test

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

// currentGateCommand is the command the sentence names now that the shell
// script it used to name is retired. The -Stale fixtures below still carry the
// script's old spelling, so a rewrite has to fix the sentence as well as the
// floor.
const (
	currentGateCommand = "make coverage-gate"
	legacyGateCommand  = "./scripts/coverage-gate.sh"
)

// The fixtures below are the three READMEs the floor blocks live in, with both
// blocks deliberately out of date so a rewrite has something to fix. The prose
// around the blocks is the real neighbourhood: a section above, a section below,
// and no blank line between a block and its end marker.
var (
	trinoReadme = "# terraform-provider-trino\n" +
		"\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)](#testing-and-coverage)\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		"The contribution workflow, review gates and contract-change protocol are\n" +
		"in CONTRIBUTING.md.\n" +
		"\n" +
		"## Testing and coverage\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"The coverage gate fails below **80%** (`make coverage-gate`).\n" +
		scopeBlockEnd + "\n" +
		"\n" +
		"## Verified unsupported\n" +
		"\n" +
		"DENY is rejected by every bundled connector.\n"

	trinoReadmeStale = "# terraform-provider-trino\n" +
		"\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-79%25-green)](#testing-and-coverage)\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		"The contribution workflow, review gates and contract-change protocol are\n" +
		"in CONTRIBUTING.md.\n" +
		"\n" +
		"## Testing and coverage\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"The coverage gate fails below **79%** (`./scripts/coverage-gate.sh`).\n" +
		scopeBlockEnd + "\n" +
		"\n" +
		"## Verified unsupported\n" +
		"\n" +
		"DENY is rejected by every bundled connector.\n"

	agentgatewayReadme = "# Terraform Provider for agentgateway\n" +
		"\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)](#testing-and-coverage)\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		"## Testing and coverage\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"The coverage gate fails below **80%** (`make coverage-gate`).\n" +
		scopeBlockEnd + "\n" +
		"\n" +
		"## License\n" +
		"\n" +
		"Mozilla Public License 2.0.\n"

	// agentgatewayReadmeStale is the whole-number trap: a floor that arrives
	// as 80.0 must print as 80, not 80.0.
	agentgatewayReadmeStale = "# Terraform Provider for agentgateway\n" +
		"\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-80.0%25-green)](#testing-and-coverage)\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		"## Testing and coverage\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"The coverage gate fails below **80.0%** (`./scripts/coverage-gate.sh`).\n" +
		scopeBlockEnd + "\n" +
		"\n" +
		"## License\n" +
		"\n" +
		"Mozilla Public License 2.0.\n"

	openmetadataReadme = "# Terraform Provider for OpenMetadata\n" +
		"\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-33%25-green)](#testing-and-coverage)\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		"## Testing and coverage\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"The coverage gate fails below **33%** (`make coverage-gate`).\n" +
		scopeBlockEnd + "\n" +
		"\n" +
		"`make coverage` writes the unit profile to `coverage.out`.\n"

	openmetadataReadmeStale = "# Terraform Provider for OpenMetadata\n" +
		"\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-32%25-green)](#testing-and-coverage)\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		"## Testing and coverage\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"The coverage gate fails below **32%** (`./scripts/coverage-gate.sh`).\n" +
		scopeBlockEnd + "\n" +
		"\n" +
		"`make coverage` writes the unit profile to `coverage.out`.\n"
)

// renderReadme renders doc and fails the test on error.
func renderReadme(t *testing.T, doc string, baseline float64, gateCommand string) []byte {
	t.Helper()

	rendered, err := coverage.RenderReadme([]byte(doc), baseline, gateCommand)
	if err != nil {
		t.Fatalf("RenderReadme() error = %v", err)
	}
	return rendered
}

func TestRenderReadmeRewritesBothBlocks(t *testing.T) {
	tests := []struct {
		name        string
		doc         string
		baseline    float64
		gateCommand string
		want        string
	}{
		{
			name:        "trino",
			doc:         trinoReadmeStale,
			baseline:    80,
			gateCommand: currentGateCommand,
			want:        trinoReadme,
		},
		{
			name:        "agentgateway drops a trailing .0",
			doc:         agentgatewayReadmeStale,
			baseline:    80,
			gateCommand: currentGateCommand,
			want:        agentgatewayReadme,
		},
		{
			name:        "openmetadata",
			doc:         openmetadataReadmeStale,
			baseline:    33,
			gateCommand: currentGateCommand,
			want:        openmetadataReadme,
		},
		{
			name:        "fractional floor with another gate command",
			doc:         openmetadataReadmeStale,
			baseline:    83.4,
			gateCommand: "make coverage-gate-strict",
			want: "# Terraform Provider for OpenMetadata\n" +
				"\n" +
				scopeBadgeStart + "\n" +
				"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-83.4%25-green)](#testing-and-coverage)\n" +
				scopeBadgeEnd + "\n" +
				"\n" +
				"## Testing and coverage\n" +
				"\n" +
				scopeBlockStart + "\n" +
				"The coverage gate fails below **83.4%** (`make coverage-gate-strict`).\n" +
				scopeBlockEnd + "\n" +
				"\n" +
				"`make coverage` writes the unit profile to `coverage.out`.\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := renderReadme(t, test.doc, test.baseline, test.gateCommand)

			if string(got) != test.want {
				t.Errorf("RenderReadme() =\n%s\nwant:\n%s", got, test.want)
			}
		})
	}
}

func TestRenderReadmeFormatsTheFloorWithoutTrailingZeros(t *testing.T) {
	// The shell scripts echo the baseline token verbatim, so a whole floor is
	// `80` and a fractional one keeps one decimal. Rendering 80 as 80.0 would
	// turn every provider's README stale on the first run.
	tests := []struct {
		baseline float64
		want     string
	}{
		{baseline: 80, want: "80"},
		{baseline: 0, want: "0"},
		{baseline: 100, want: "100"},
		{baseline: 83.4, want: "83.4"},
	}

	for _, test := range tests {
		t.Run(test.want, func(t *testing.T) {
			got := string(renderReadme(t, trinoReadmeStale, test.baseline, currentGateCommand))

			for _, want := range []string{
				"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-" + test.want + "%25-green)](#testing-and-coverage)",
				"The coverage gate fails below **" + test.want + "%** (`" + currentGateCommand + "`).",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("RenderReadme() =\n%s\nwant it to contain %q", got, want)
				}
			}
		})
	}
}

func TestRenderReadmeRejectsAnImpossibleBaseline(t *testing.T) {
	// A badge that promises a floor the gate cannot enforce is worse than no
	// badge, so the same 0..100 rule the gate applies is applied here.
	for _, baseline := range []float64{-1, 100.5, math.NaN()} {
		_, err := coverage.RenderReadme([]byte(trinoReadme), baseline, currentGateCommand)
		if err == nil {
			t.Errorf("baseline %v: RenderReadme() error = nil, want an error", baseline)
		}
	}
}

func TestRenderReadmeRejectsAnUnrenderableGateCommand(t *testing.T) {
	// The command sits in an inline code span on one line. An empty value or
	// one carrying a backtick or a newline cannot be written without breaking
	// the block, and a broken README is worse than a failed command.
	for _, gateCommand := range []string{"", "make `coverage-gate`", "make\ncoverage-gate"} {
		_, err := coverage.RenderReadme([]byte(trinoReadme), 80, gateCommand)
		if err == nil {
			t.Errorf("gate command %q: RenderReadme() error = nil, want an error", gateCommand)
		}
	}
}

func TestRenderReadmeIsIdempotent(t *testing.T) {
	tests := []struct {
		name        string
		doc         string
		baseline    float64
		gateCommand string
	}{
		{name: "stale", doc: trinoReadmeStale, baseline: 80, gateCommand: currentGateCommand},
		{name: "already current", doc: trinoReadme, baseline: 80, gateCommand: currentGateCommand},
		{name: "fractional", doc: openmetadataReadmeStale, baseline: 83.4, gateCommand: "make coverage-gate-strict"},
		{
			name:        "no trailing newline",
			doc:         strings.TrimSuffix(trinoReadmeStale, "\n"),
			baseline:    80,
			gateCommand: currentGateCommand,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			once := renderReadme(t, test.doc, test.baseline, test.gateCommand)
			twice := renderReadme(t, string(once), test.baseline, test.gateCommand)

			if !bytes.Equal(once, twice) {
				t.Errorf("second render changed the document:\nfirst:\n%s\nsecond:\n%s", once, twice)
			}
		})
	}
}

func TestRenderReadmeLeavesTextOutsideMarkersUntouched(t *testing.T) {
	// Only the marker regions are the tool's to edit. The awk script this
	// replaces copies every line outside the pairs verbatim, including blank
	// lines, prose between the pairs and prose that merely mentions coverage.
	const prefix = "# Title\n" +
		"\n" +
		"Prose about coverage markers, with `code` and a <!-- comment --> shape.\n" +
		"\n" +
		"```bash\n" +
		"make coverage-gate\n" +
		"```\n" +
		"\n"
	const middle = "\n" +
		"Middle prose, between the two marker pairs.\n" +
		"\n"
	const suffix = "\n" +
		"## After the blocks\n" +
		"\n" +
		"Trailing prose.\n"
	doc := prefix + readmeBadgeBlockLines(79) + middle + readmeBlockLines(79, legacyGateCommand) + suffix
	want := prefix + readmeBadgeBlockLines(80) + middle + readmeBlockLines(80, currentGateCommand) + suffix

	got := string(renderReadme(t, doc, 80, currentGateCommand))

	if got != want {
		t.Errorf("RenderReadme() =\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderReadmeTerminatesTheDocument(t *testing.T) {
	// Every line outside the markers is copied whole, and the last one gains
	// the newline a text file is expected to end with, as the awk script's
	// `print` does.
	doc := strings.TrimSuffix(trinoReadmeStale, "\n")

	got := string(renderReadme(t, doc, 80, currentGateCommand))

	if got != trinoReadme {
		t.Errorf("RenderReadme() =\n%q\nwant:\n%q", got, trinoReadme)
	}
}

// readmeBadgeBlockLines is the badge pair as the fixtures above spell it, for
// the tests that need to assemble one inline.
func readmeBadgeBlockLines(floor int) string {
	return scopeBadgeStart + "\n" +
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-" + strconv.Itoa(floor) + "%25-green)](#testing-and-coverage)\n" +
		scopeBadgeEnd
}

// readmeBlockLines is the sentence pair as the fixtures above spell it.
func readmeBlockLines(floor int, gateCommand string) string {
	return scopeBlockStart + "\n" +
		"The coverage gate fails below **" + strconv.Itoa(floor) + "%** (`" + gateCommand + "`).\n" +
		scopeBlockEnd
}

func TestRenderReadmeRequiresBothMarkerPairs(t *testing.T) {
	// Unlike the old bare renderer, a floor block is two pairs now. A README
	// with only one of them is half rendered, so it is a marker error rather
	// than something to guess about.
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{
			name: "coverage pair only",
			doc:  "# Title\n\n" + readmeBlockLines(80, currentGateCommand) + "\n",
			want: []string{scopeBadgeStart, scopeBadgeEnd},
		},
		{
			name: "badge pair only",
			doc:  "# Title\n\n" + readmeBadgeBlockLines(80) + "\n",
			want: []string{scopeBlockStart, scopeBlockEnd},
		},
		{
			name: "neither pair",
			doc:  "# Title\n\nNo coverage block here.\n",
			want: []string{scopeBadgeStart, scopeBadgeEnd},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rendered, err := coverage.RenderReadme([]byte(test.doc), 80, currentGateCommand)

			if !errors.Is(err, coverage.ErrReadmeMarkers) {
				t.Fatalf("RenderReadme() error = %v, want ErrReadmeMarkers", err)
			}
			if rendered != nil {
				t.Errorf("RenderReadme() = %q, want nil on error", rendered)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestRenderReadmeFailsWhenOnlyOneMarkerIsPresent(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		// want lists the substrings the diagnosis must carry; a duplicated
		// marker is named once, because that is the whole problem.
		want []string
	}{
		{
			name: "badge start only",
			doc:  "# Title\n\n<!-- coverage-badge:start -->\n![badge](x)\n",
			want: []string{scopeBadgeStart, scopeBadgeEnd},
		},
		{
			name: "badge end only",
			doc:  "# Title\n\n![badge](x)\n<!-- coverage-badge:end -->\n",
			want: []string{scopeBadgeStart, scopeBadgeEnd},
		},
		{
			name: "coverage start only",
			doc: "# Title\n\n" + readmeBadgeBlockLines(80) +
				"\n\n<!-- coverage:start -->\nThe sentence.\n",
			want: []string{scopeBlockStart, scopeBlockEnd},
		},
		{
			name: "coverage end only",
			doc: "# Title\n\n" + readmeBadgeBlockLines(80) +
				"\n\nThe sentence.\n<!-- coverage:end -->\n",
			want: []string{scopeBlockStart, scopeBlockEnd},
		},
		{
			name: "duplicate badge start",
			doc:  "<!-- coverage-badge:start -->\n<!-- coverage-badge:start -->\n<!-- coverage-badge:end -->\n",
			want: []string{"more than one", scopeBadgeStart},
		},
		{
			name: "duplicate coverage end",
			doc:  readmeBadgeBlockLines(80) + "\n\n<!-- coverage:start -->\n<!-- coverage:end -->\n<!-- coverage:end -->\n",
			want: []string{"more than one", scopeBlockEnd},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rendered, err := coverage.RenderReadme([]byte(test.doc), 80, currentGateCommand)

			if !errors.Is(err, coverage.ErrReadmeMarkers) {
				t.Fatalf("RenderReadme() error = %v, want ErrReadmeMarkers", err)
			}
			if rendered != nil {
				t.Errorf("RenderReadme() = %q, want nil on error", rendered)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestRenderReadmeFailsWhenNoMarkersArePresent(t *testing.T) {
	// The error cannot name the README path: RenderReadme works on a byte
	// slice, so the caller adds the path. It must still say which markers are
	// missing, because that is the only diagnosis the user gets.
	rendered, err := coverage.RenderReadme([]byte(trinoReadme[strings.Index(trinoReadme, "## Verified"):]), 80, currentGateCommand)

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if rendered != nil {
		t.Errorf("RenderReadme() = %q, want nil on error", rendered)
	}
	for _, want := range []string{scopeBadgeStart, scopeBadgeEnd} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestRenderReadmeFailsWhenTheMarkersAreReversed(t *testing.T) {
	// Both markers present but in the wrong order is a broken README, not a
	// stale one: swapping them silently would delete the text between them.
	doc := "# Title\n\n" + readmeBadgeBlockLines(80) +
		"\n\n<!-- coverage:end -->\nstale\n<!-- coverage:start -->\n"

	rendered, err := coverage.RenderReadme([]byte(doc), 80, currentGateCommand)

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if rendered != nil {
		t.Errorf("RenderReadme() = %q, want nil on error", rendered)
	}
	if !strings.Contains(err.Error(), "coverage:start") || !strings.Contains(err.Error(), "coverage:end") {
		t.Errorf("error = %q, want it to name both markers", err)
	}
}

func TestRenderReadmeRejectsOverlappingBlocks(t *testing.T) {
	// Interleaved pairs would make the splice delete one block's markers, so
	// they are a configuration error rather than something to guess about.
	doc := "# Title\n\n" + scopeBadgeStart + "\n" + scopeBlockStart + "\n" +
		scopeBadgeEnd + "\n" + scopeBlockEnd + "\n"

	_, err := coverage.RenderReadme([]byte(doc), 80, currentGateCommand)

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if !strings.Contains(err.Error(), "overlap") {
		t.Errorf("error = %q, want it to report the overlap", err)
	}
}

func TestRenderReadmeRejectsAScopeReadme(t *testing.T) {
	// Both dialects now share the marker pairs, so the baseline is the only
	// dialect signal. A bare baseline applied to a scope README must be refused:
	// rewriting the table pair would replace the per-scope rows and the
	// repository-owned prose inside them with one sentence.
	badge, block := committedBlocks(t, "gravitino")
	doc := "# Title\n\n" + badge + "\n\nProse.\n\n" + block + "\n"

	rendered, err := coverage.RenderReadme([]byte(doc), 80, currentGateCommand)

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if rendered != nil {
		t.Errorf("RenderReadme() = %q, want nil on error", rendered)
	}
	for _, want := range []string{"key=value coverage table", "single percentage"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestCheckReadmeReportsStale(t *testing.T) {
	doc := []byte(trinoReadmeStale)
	before := bytes.Clone(doc)

	err := coverage.CheckReadme(doc, 80, currentGateCommand)

	if !errors.Is(err, coverage.ErrReadmeStale) {
		t.Fatalf("CheckReadme() error = %v, want ErrReadmeStale", err)
	}
	if !bytes.Equal(doc, before) {
		t.Errorf("CheckReadme() modified its input:\n%s\nwant:\n%s", doc, before)
	}
}

func TestCheckReadmeAcceptsTheCurrentBlocks(t *testing.T) {
	if err := coverage.CheckReadme([]byte(trinoReadme), 80, currentGateCommand); err != nil {
		t.Errorf("CheckReadme() error = %v, want nil", err)
	}
}

func TestCheckReadmeFailsOnAMarkerProblem(t *testing.T) {
	err := coverage.CheckReadme([]byte("# Title\n\nNo block here.\n"), 80, currentGateCommand)

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Errorf("CheckReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if errors.Is(err, coverage.ErrReadmeStale) {
		t.Errorf("CheckReadme() error = %v, want a marker error rather than a stale one", err)
	}
}
