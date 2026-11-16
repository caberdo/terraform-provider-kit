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

// defaultGateCommand is the command the three providers name today. The block
// must stay byte-identical until the Makefile passes its own value.
const defaultGateCommand = "./scripts/coverage-gate.sh"

// The fixtures below are the three READMEs the floor block lives in, with the
// block deliberately out of date so a rewrite has something to fix. The prose
// around the block is the real neighbourhood: a section above, a section
// below, and no blank line between the sentence and the end marker.
var (
	trinoReadme = "# terraform-provider-trino\n" +
		"\n" +
		"The contribution workflow, review gates and contract-change protocol are\n" +
		"in CONTRIBUTING.md.\n" +
		"\n" +
		"<!-- coverage:start -->\n" +
		"![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)\n" +
		"\n" +
		"The coverage gate fails below **80%** (`./scripts/coverage-gate.sh`).\n" +
		"<!-- coverage:end -->\n" +
		"\n" +
		"## Verified unsupported\n" +
		"\n" +
		"DENY is rejected by every bundled connector.\n"

	trinoReadmeStale = "# terraform-provider-trino\n" +
		"\n" +
		"The contribution workflow, review gates and contract-change protocol are\n" +
		"in CONTRIBUTING.md.\n" +
		"\n" +
		"<!-- coverage:start -->\n" +
		"![Coverage floor](https://img.shields.io/badge/coverage%20floor-79%25-green)\n" +
		"\n" +
		"The coverage gate fails below **79%** (`./scripts/coverage-gate.sh`).\n" +
		"<!-- coverage:end -->\n" +
		"\n" +
		"## Verified unsupported\n" +
		"\n" +
		"DENY is rejected by every bundled connector.\n"

	agentgatewayReadme = "## Coverage\n" +
		"\n" +
		"<!-- coverage:start -->\n" +
		"![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)\n" +
		"\n" +
		"The coverage gate fails below **80%** (`./scripts/coverage-gate.sh`).\n" +
		"<!-- coverage:end -->\n" +
		"\n" +
		"## License\n" +
		"\n" +
		"Mozilla Public License 2.0.\n"

	// agentgatewayReadmeStale is the whole-number trap: a floor that arrives
	// as 80.0 must print as 80, not 80.0.
	agentgatewayReadmeStale = "## Coverage\n" +
		"\n" +
		"<!-- coverage:start -->\n" +
		"![Coverage floor](https://img.shields.io/badge/coverage%20floor-80.0%25-green)\n" +
		"\n" +
		"The coverage gate fails below **80.0%** (`./scripts/coverage-gate.sh`).\n" +
		"<!-- coverage:end -->\n" +
		"\n" +
		"## License\n" +
		"\n" +
		"Mozilla Public License 2.0.\n"

	openmetadataReadme = "## Coverage\n" +
		"\n" +
		"<!-- coverage:start -->\n" +
		"![Coverage floor](https://img.shields.io/badge/coverage%20floor-33%25-green)\n" +
		"\n" +
		"The coverage gate fails below **33%** (`./scripts/coverage-gate.sh`).\n" +
		"<!-- coverage:end -->\n" +
		"\n" +
		"`make coverage` writes the unit profile to `coverage.out`.\n"

	openmetadataReadmeStale = "## Coverage\n" +
		"\n" +
		"<!-- coverage:start -->\n" +
		"![Coverage floor](https://img.shields.io/badge/coverage%20floor-32%25-green)\n" +
		"\n" +
		"The coverage gate fails below **32%** (`./scripts/coverage-gate.sh`).\n" +
		"<!-- coverage:end -->\n" +
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

func TestRenderReadmeRewritesFloorBlock(t *testing.T) {
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
			gateCommand: defaultGateCommand,
			want:        trinoReadme,
		},
		{
			name:        "agentgateway drops a trailing .0",
			doc:         agentgatewayReadmeStale,
			baseline:    80,
			gateCommand: defaultGateCommand,
			want:        agentgatewayReadme,
		},
		{
			name:        "openmetadata",
			doc:         openmetadataReadmeStale,
			baseline:    33,
			gateCommand: defaultGateCommand,
			want:        openmetadataReadme,
		},
		{
			name:        "fractional floor with another gate command",
			doc:         openmetadataReadmeStale,
			baseline:    83.4,
			gateCommand: "make coverage-gate",
			want: "## Coverage\n" +
				"\n" +
				"<!-- coverage:start -->\n" +
				"![Coverage floor](https://img.shields.io/badge/coverage%20floor-83.4%25-green)\n" +
				"\n" +
				"The coverage gate fails below **83.4%** (`make coverage-gate`).\n" +
				"<!-- coverage:end -->\n" +
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
			got := string(renderReadme(t, trinoReadmeStale, test.baseline, defaultGateCommand))

			for _, want := range []string{
				"![Coverage floor](https://img.shields.io/badge/coverage%20floor-" + test.want + "%25-green)",
				"The coverage gate fails below **" + test.want + "%** (`" + defaultGateCommand + "`).",
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
		_, err := coverage.RenderReadme([]byte(trinoReadme), baseline, defaultGateCommand)
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
		{name: "stale", doc: trinoReadmeStale, baseline: 80, gateCommand: defaultGateCommand},
		{name: "already current", doc: trinoReadme, baseline: 80, gateCommand: defaultGateCommand},
		{name: "fractional", doc: openmetadataReadmeStale, baseline: 83.4, gateCommand: "make coverage-gate"},
		{
			name:        "no trailing newline",
			doc:         strings.TrimSuffix(trinoReadmeStale, "\n"),
			baseline:    80,
			gateCommand: defaultGateCommand,
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
	// Only the marker region is the tool's to edit. The awk script this
	// replaces copies every line outside the pair verbatim, including blank
	// lines and prose that merely mentions coverage.
	const prefix = "# Title\n" +
		"\n" +
		"Prose about coverage markers, with `code` and a <!-- comment --> shape.\n" +
		"\n" +
		"```bash\n" +
		"make coverage-gate\n" +
		"```\n" +
		"\n"
	const suffix = "\n" +
		"## After the block\n" +
		"\n" +
		"Trailing prose.\n"
	doc := prefix + readmeBlockLines(79, defaultGateCommand) + suffix
	want := prefix + readmeBlockLines(80, defaultGateCommand) + suffix

	got := string(renderReadme(t, doc, 80, defaultGateCommand))

	if got != want {
		t.Errorf("RenderReadme() =\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderReadmeTerminatesTheDocument(t *testing.T) {
	// Every line outside the markers is copied whole, and the last one gains
	// the newline a text file is expected to end with, as the awk script's
	// `print` does.
	doc := strings.TrimSuffix(trinoReadmeStale, "\n")

	got := string(renderReadme(t, doc, 80, defaultGateCommand))

	if got != trinoReadme {
		t.Errorf("RenderReadme() =\n%q\nwant:\n%q", got, trinoReadme)
	}
}

// readmeBlockLines is the block as the fixtures above spell it, for the tests
// that need to assemble one inline.
func readmeBlockLines(floor int, gateCommand string) string {
	return "<!-- coverage:start -->\n" +
		"![Coverage floor](https://img.shields.io/badge/coverage%20floor-" + strconv.Itoa(floor) + "%25-green)\n" +
		"\n" +
		"The coverage gate fails below **" + strconv.Itoa(floor) + "%** (`" + gateCommand + "`).\n" +
		"<!-- coverage:end -->"
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
			name: "start only",
			doc:  "# Title\n\n<!-- coverage:start -->\n![badge](x)\n",
			want: []string{"<!-- coverage:start -->", "<!-- coverage:end -->"},
		},
		{
			name: "end only",
			doc:  "# Title\n\n![badge](x)\n<!-- coverage:end -->\n",
			want: []string{"<!-- coverage:start -->", "<!-- coverage:end -->"},
		},
		{
			name: "duplicate start",
			doc:  "<!-- coverage:start -->\n<!-- coverage:start -->\n<!-- coverage:end -->\n",
			want: []string{"more than one", "<!-- coverage:start -->"},
		},
		{
			name: "duplicate end",
			doc:  "<!-- coverage:start -->\n<!-- coverage:end -->\n<!-- coverage:end -->\n",
			want: []string{"more than one", "<!-- coverage:end -->"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rendered, err := coverage.RenderReadme([]byte(test.doc), 80, defaultGateCommand)

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

func TestRenderReadmeFailsWhenThePairIsMissing(t *testing.T) {
	// The error cannot name the README path: RenderReadme works on a byte
	// slice, so the caller adds the path. It must still say which markers are
	// missing, because that is the only diagnosis the user gets.
	rendered, err := coverage.RenderReadme([]byte(trinoReadme[strings.Index(trinoReadme, "## Verified"):]), 80, defaultGateCommand)

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if rendered != nil {
		t.Errorf("RenderReadme() = %q, want nil on error", rendered)
	}
	for _, want := range []string{"<!-- coverage:start -->", "<!-- coverage:end -->"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestRenderReadmeFailsWhenTheMarkersAreReversed(t *testing.T) {
	// Both markers present but in the wrong order is a broken README, not a
	// stale one: swapping them silently would delete the text between them.
	const doc = "# Title\n\n<!-- coverage:end -->\nstale\n<!-- coverage:start -->\n"

	rendered, err := coverage.RenderReadme([]byte(doc), 80, defaultGateCommand)

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

func TestCheckReadmeReportsStale(t *testing.T) {
	doc := []byte(trinoReadmeStale)
	before := bytes.Clone(doc)

	err := coverage.CheckReadme(doc, 80, defaultGateCommand)

	if !errors.Is(err, coverage.ErrReadmeStale) {
		t.Fatalf("CheckReadme() error = %v, want ErrReadmeStale", err)
	}
	if !bytes.Equal(doc, before) {
		t.Errorf("CheckReadme() modified its input:\n%s\nwant:\n%s", doc, before)
	}
}

func TestCheckReadmeAcceptsTheCurrentBlock(t *testing.T) {
	if err := coverage.CheckReadme([]byte(trinoReadme), 80, defaultGateCommand); err != nil {
		t.Errorf("CheckReadme() error = %v, want nil", err)
	}
}

func TestCheckReadmeFailsOnAMarkerProblem(t *testing.T) {
	err := coverage.CheckReadme([]byte("# Title\n\nNo block here.\n"), 80, defaultGateCommand)

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Errorf("CheckReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if errors.Is(err, coverage.ErrReadmeStale) {
		t.Errorf("CheckReadme() error = %v, want a marker error rather than a stale one", err)
	}
}
