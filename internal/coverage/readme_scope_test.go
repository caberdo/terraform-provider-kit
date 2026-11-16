package coverage_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
)

// The scope-dialect fixtures are the four blocks gravitino and rustfs publish
// today, copied byte for byte out of their READMEs, plus their committed
// baselines. They are the contract: tfkit must reproduce them exactly, because
// both providers run `--check` in CI and a single byte of drift fails the build.
const (
	scopeBadgeStart = "<!-- coverage-badge:start -->"
	scopeBadgeEnd   = "<!-- coverage-badge:end -->"
	scopeBlockStart = "<!-- coverage:start -->"
	scopeBlockEnd   = "<!-- coverage:end -->"
)

// scopeFixtureFiles names the two repositories the scope fixtures come from.
var scopeFixtureFiles = []string{"gravitino", "rustfs"}

// readFixture reads one testdata file and fails the test on error.
func readFixture(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return string(data)
}

// scopeBlock returns the committed badge block and coverage block of repo.
func committedBlocks(t *testing.T, repo string) (badge, block string) {
	t.Helper()

	return readFixture(t, repo+"-coverage-badge-block.txt"),
		readFixture(t, repo+"-coverage-block.txt")
}

// parseScopeFixture parses repo's committed baseline.
func parseScopeFixture(t *testing.T, repo string) coverage.ScopeBaseline {
	t.Helper()

	baseline, err := coverage.ParseScopeBaseline(strings.NewReader(
		readFixture(t, repo+"-coverage-baseline.txt")))
	if err != nil {
		t.Fatalf("ParseScopeBaseline(%s): %v", repo, err)
	}
	return baseline
}

// scopeReadme assembles a README around the two committed blocks. The prose on
// both sides is the neighbourhood the blocks live in: the renderer must not
// touch one line of it.
func scopeReadme(t *testing.T, repo string) string {
	t.Helper()

	badge, block := committedBlocks(t, repo)
	return "# Terraform Provider for " + repo + "\n" +
		"\n" +
		"Statement coverage of this repository, merged from the unit and acceptance profiles:\n" +
		"\n" +
		badge +
		"\n" +
		"The gate is enforced on every pull request.\n" +
		"\n" +
		block +
		"\n" +
		"The floor is enforced: the coverage gate fails a pull request below the baseline.\n"
}

// scopeMarkersOnly is a README carrying both marker pairs with stale filler
// between them, so a rewrite has to regenerate every generated line.
func scopeMarkersOnly(anchor string) string {
	return "# Terraform Provider for x\n" +
		"\n" +
		"Intro prose.\n" +
		"\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage](https://img.shields.io/badge/coverage-1.0%25-red)]" + anchor + "\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		"Middle prose.\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"```\n" +
		"total       ░░░░░░░░░░░░░░░░░░░░  1.0%   floor 0.0%\n" +
		"```\n" +
		"\n" +
		"Measured on `deadbee` (unit + acceptance) on 2000-01-01.\n" +
		scopeBlockEnd + "\n" +
		"\n" +
		"Outro prose.\n"
}

func TestRenderScopeReadmeAcceptsTheCommittedBlocks(t *testing.T) {
	// Both READMEs are in sync with their baselines today, so `--check` must
	// exit 0 on each: the blocks are the output of the scripts being replaced.
	for _, repo := range scopeFixtureFiles {
		t.Run(repo, func(t *testing.T) {
			doc := []byte(scopeReadme(t, repo))
			baseline := parseScopeFixture(t, repo)

			if err := coverage.CheckScopeReadme(doc, baseline); err != nil {
				t.Fatalf("CheckScopeReadme() error = %v, want nil", err)
			}
			rendered, err := coverage.RenderScopeReadme(doc, baseline)
			if err != nil {
				t.Fatalf("RenderScopeReadme() error = %v", err)
			}
			if !bytes.Equal(rendered, doc) {
				t.Errorf("RenderScopeReadme() changed an in-sync README:\n%s\nwant:\n%s", rendered, doc)
			}
		})
	}
}

func TestRenderScopeReadmeWritesADefaultBlockFromScratch(t *testing.T) {
	// With nothing to preserve, the renderer emits the default shape: the
	// badge's link target it is given, the baseline's row order, and the arrow
	// form of the milestone sentence. A repository then owns that output, and
	// every later rewrite keeps it (see the tests above).
	doc := scopeMarkersOnly("(#testing-and-coverage)")
	want := "# Terraform Provider for x\n" +
		"\n" +
		"Intro prose.\n" +
		"\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage](https://img.shields.io/badge/coverage-90.2%25-brightgreen)](#testing-and-coverage)\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		"Middle prose.\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"```\n" +
		"provider    ██████████████████░░  88.0%   floor 87.5%\n" +
		"pkg/rustfs  ████████████████████  97.8%   floor 97.3%\n" +
		"total       ██████████████████░░  90.2%   floor 89.5%   -> 80%, then 90%\n" +
		"```\n" +
		"\n" +
		"Measured on `f84538b` (unit + acceptance) on 2026-10-19.\n" +
		scopeBlockEnd + "\n" +
		"\n" +
		"Outro prose.\n"

	rendered, err := coverage.RenderScopeReadme([]byte(doc), parseScopeFixture(t, "rustfs"))
	if err != nil {
		t.Fatalf("RenderScopeReadme() error = %v", err)
	}
	if string(rendered) != want {
		t.Errorf("RenderScopeReadme() =\n%s\nwant:\n%s", rendered, want)
	}
}

func TestRenderScopeReadmeKeepsTheRepositoriesOwnProse(t *testing.T) {
	// gravitino's block explains how the merged profile is produced and rustfs's
	// does not. That paragraph is the repository's wording, not a number the
	// baseline can carry, so the renderer preserves it instead of deleting it.
	badge, block := committedBlocks(t, "gravitino")
	rendered, err := coverage.RenderScopeReadme(
		[]byte(scopeReadme(t, "gravitino")), parseScopeFixture(t, "gravitino"))
	if err != nil {
		t.Fatalf("RenderScopeReadme() error = %v", err)
	}
	for _, want := range []string{
		"Merged statement coverage from `coverage.out` (unit) and `coverage-acceptance.out`",
		"(acceptance, `-covermode=atomic -coverpkg=./internal/...`) via `scripts/merge-coverage.sh`",
		"(`make coverage-merge`).",
		badge,
		block,
	} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("RenderScopeReadme() =\n%s\nwant it to contain %q", rendered, want)
		}
	}
}

func TestRenderScopeReadmeUpdatesTheNumbersItOwns(t *testing.T) {
	// A raised measurement must move the badge, its colour and the table, and
	// leave the floors and the provenance alone.
	baseline, err := coverage.ParseScopeBaseline(strings.NewReader(strings.Replace(
		readFixture(t, "gravitino-coverage-baseline.txt"),
		"measured_total=83.4", "measured_total=91.2", 1)))
	if err != nil {
		t.Fatalf("ParseScopeBaseline(): %v", err)
	}

	rendered, err := coverage.RenderScopeReadme(
		[]byte(scopeReadme(t, "gravitino")), baseline)
	if err != nil {
		t.Fatalf("RenderScopeReadme() error = %v", err)
	}

	for _, want := range []string{
		"[![Coverage](https://img.shields.io/badge/coverage-91.2%25-brightgreen)](#coverage)",
		"total" + strings.Repeat(" ", 25) + "  ██████████████████░░  91.2%   floor 82.5%   -> 80%, then 90%",
		"Measured on `c2fc783` (unit + acceptance) on 2026-10-19.",
	} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("RenderScopeReadme() =\n%s\nwant it to contain %q", rendered, want)
		}
	}
	if err := coverage.CheckScopeReadme([]byte(scopeReadme(t, "gravitino")), baseline); !errors.Is(err, coverage.ErrReadmeStale) {
		t.Errorf("CheckScopeReadme() error = %v, want ErrReadmeStale", err)
	}
}

func TestRenderScopeReadmeEchoesTheFloorToken(t *testing.T) {
	// The scripts print the baseline's floor token, so `79.0` stays `79.0`:
	// printing `79` would make gravitino's README stale on the first run.
	rendered, err := coverage.RenderScopeReadme(
		[]byte(scopeReadme(t, "gravitino")), parseScopeFixture(t, "gravitino"))
	if err != nil {
		t.Fatalf("RenderScopeReadme() error = %v", err)
	}
	if !strings.Contains(string(rendered), "floor 79.0%") {
		t.Errorf("RenderScopeReadme() =\n%s\nwant the floor token 79.0 preserved", rendered)
	}
	if strings.Contains(string(rendered), "floor 79%") {
		t.Errorf("RenderScopeReadme() =\n%s\nwant no rounded floor 79%%", rendered)
	}
}

func TestRenderScopeReadmeKeepsTheMilestoneWordingOfTheRepository(t *testing.T) {
	// gravitino prints `-> 80%, then 90%` at 83.4% and rustfs prints
	// `milestones 80% and 90% met` at 90.2%. Both are in sync with their own
	// script, so the sentence's form is the repository's and only its numbers
	// are regenerated.
	tests := []struct {
		name    string
		total   float64
		oldTail string
		want    string
	}{
		{name: "arrow below the milestones", total: 76, oldTail: "-> 80%, then 90%", want: "-> 80%, then 90%"},
		{name: "arrow above the milestones", total: 96, oldTail: "-> 80%, then 90%", want: "-> 80%, then 90%"},
		{name: "steps below target", total: 76, oldTail: "-> 80%, then 90%", want: "-> 80%, then 90%"},
		{name: "steps between target and gold", total: 85, oldTail: "80% met; 90% next", want: "80% met; 90% next"},
		{name: "steps at gold", total: 91, oldTail: "milestones 80% and 90% met", want: "milestones 80% and 90% met"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := coverage.ScopeBaseline{
				Measured: map[string]float64{coverage.TotalPackage: test.total},
				Floors:   map[string]float64{coverage.TotalPackage: test.total - 1},
				Target:   80,
				Gold:     90,
			}
			doc := scopeMarkersOnly("(#coverage)")
			doc = strings.Replace(doc, "floor 0.0%", "floor 0.0%   "+test.oldTail, 1)

			rendered, err := coverage.RenderScopeReadme([]byte(doc), baseline)
			if err != nil {
				t.Fatalf("RenderScopeReadme() error = %v", err)
			}
			if !strings.Contains(string(rendered), "   "+test.want+"\n") {
				t.Errorf("RenderScopeReadme() =\n%s\nwant the total row to end with %q", rendered, test.want)
			}
		})
	}
}

func TestRenderScopeReadmeRejectsAHalfRenderedReadme(t *testing.T) {
	// Both pairs are required: a README with only one of them is a
	// configuration error, because a half-rendered README is worse than a
	// stale one.
	badge, block := committedBlocks(t, "gravitino")
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "badge pair only",
			doc:  "# Title\n\n" + badge + "\n\nProse.\n",
			want: scopeBlockStart,
		},
		{
			name: "coverage pair only",
			doc:  "# Title\n\n" + block + "\n\nProse.\n",
			want: scopeBadgeStart,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rendered, err := coverage.RenderScopeReadme(
				[]byte(test.doc), parseScopeFixture(t, "gravitino"))

			if !errors.Is(err, coverage.ErrReadmeMarkers) {
				t.Fatalf("RenderScopeReadme() error = %v, want ErrReadmeMarkers", err)
			}
			if rendered != nil {
				t.Errorf("RenderScopeReadme() = %q, want nil on error", rendered)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to name %q", err, test.want)
			}
		})
	}
}

func TestRenderScopeReadmeRejectsDuplicatedMarkers(t *testing.T) {
	doc := scopeReadme(t, "rustfs") + "\n" + scopeBadgeStart + "\n"

	rendered, err := coverage.RenderScopeReadme([]byte(doc), parseScopeFixture(t, "rustfs"))

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderScopeReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if rendered != nil {
		t.Errorf("RenderScopeReadme() = %q, want nil on error", rendered)
	}
	if !strings.Contains(err.Error(), "more than one") || !strings.Contains(err.Error(), scopeBadgeStart) {
		t.Errorf("error = %q, want it to report the duplicated badge marker", err)
	}
}

func TestRenderScopeReadmeRejectsOverlappingBlocks(t *testing.T) {
	// Interleaved pairs would make the splice delete one block's markers, so
	// they are a configuration error rather than something to guess about.
	doc := "# Title\n\n" + scopeBadgeStart + "\n" + scopeBlockStart + "\n" +
		scopeBadgeEnd + "\n" + scopeBlockEnd + "\n"

	_, err := coverage.RenderScopeReadme([]byte(doc), parseScopeFixture(t, "rustfs"))

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderScopeReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if !strings.Contains(err.Error(), "overlap") {
		t.Errorf("error = %q, want it to report the overlap", err)
	}
}

func TestRenderScopeReadmeIsIdempotent(t *testing.T) {
	for _, repo := range scopeFixtureFiles {
		t.Run(repo, func(t *testing.T) {
			baseline := parseScopeFixture(t, repo)
			once, err := coverage.RenderScopeReadme([]byte(scopeReadme(t, repo)), baseline)
			if err != nil {
				t.Fatalf("RenderScopeReadme() error = %v", err)
			}
			if err := coverage.CheckScopeReadme(once, baseline); err != nil {
				t.Errorf("CheckScopeReadme() on the render = %v, want nil", err)
			}
			twice, err := coverage.RenderScopeReadme(once, baseline)
			if err != nil {
				t.Fatalf("RenderScopeReadme() second error = %v", err)
			}
			if !bytes.Equal(once, twice) {
				t.Errorf("second render changed the document:\nfirst:\n%s\nsecond:\n%s", once, twice)
			}
		})
	}
}

func TestRenderScopeReadmeLeavesTextOutsideTheMarkersAlone(t *testing.T) {
	// Only the two marker regions are the tool's to edit.
	doc := scopeMarkersOnly("(#coverage)")
	before := strings.Split(doc, "\n")
	rendered, err := coverage.RenderScopeReadme([]byte(doc), parseScopeFixture(t, "rustfs"))
	if err != nil {
		t.Fatalf("RenderScopeReadme() error = %v", err)
	}
	after := strings.Split(string(rendered), "\n")

	for _, want := range []string{"Intro prose.", "Middle prose.", "Outro prose."} {
		if !slicesContain(after, want) || !slicesContain(before, want) {
			t.Errorf("RenderScopeReadme() =\n%s\nwant it to keep %q", rendered, want)
		}
	}
	if !strings.HasPrefix(string(rendered), "# Terraform Provider for x\n\nIntro prose.\n\n") {
		t.Errorf("RenderScopeReadme() =\n%s\nwant the prefix untouched", rendered)
	}
	if !strings.HasSuffix(string(rendered), "\n\nOutro prose.\n") {
		t.Errorf("RenderScopeReadme() =\n%s\nwant the suffix untouched", rendered)
	}
}

// slicesContain reports whether lines holds want.
func slicesContain(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func TestCheckScopeReadmeReportsStaleWithoutWriting(t *testing.T) {
	doc := []byte(scopeReadme(t, "rustfs"))
	before := bytes.Clone(doc)
	baseline := parseScopeFixture(t, "rustfs")
	baseline.Commit = "0123456"

	err := coverage.CheckScopeReadme(doc, baseline)

	if !errors.Is(err, coverage.ErrReadmeStale) {
		t.Fatalf("CheckScopeReadme() error = %v, want ErrReadmeStale", err)
	}
	if !bytes.Equal(doc, before) {
		t.Errorf("CheckScopeReadme() modified its input:\n%s\nwant:\n%s", doc, before)
	}
}

func TestRenderScopeReadmeRejectsMalformedRegions(t *testing.T) {
	// A region the renderer cannot classify must fail, never be half copied: the
	// old rows would otherwise be republished beside the generated table, and the
	// second render would be byte-stable, so --check would bless the duplicate.
	body := scopeBody(t, "gravitino")
	badge, block := committedBlocks(t, "gravitino")

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "rows outside the fence",
			body: body + "total       ████░░░░░░░░░░░░░░░░  50.0%   floor 1.0%\n",
			want: "outside the fence",
		},
		{
			name: "text fence left open",
			body: "```text\n" + scopeRows(block) + "\n",
			want: "never closed",
		},
		{
			name: "missing closing fence",
			body: "```\n" + scopeRows(block) + "\n",
			want: "never closed",
		},
		{
			name: "second fenced block",
			body: body + "```\n" + scopeRows(block) + "\n```\n",
			want: "second fenced block",
		},
		{
			name: "unparseable row inside the fence",
			body: "```\nThis is not a table row.\n```\n",
			want: "is not a table row",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := "# Title\n\n" + badge + "\n\nProse.\n\n" +
				scopeBlockStart + "\n" + test.body + scopeBlockEnd + "\n\nOutro.\n"

			rendered, err := coverage.RenderScopeReadme(
				[]byte(doc), parseScopeFixture(t, "gravitino"))

			if !errors.Is(err, coverage.ErrReadmeMarkers) {
				t.Fatalf("RenderScopeReadme() error = %v, want ErrReadmeMarkers", err)
			}
			if rendered != nil {
				t.Errorf("RenderScopeReadme() = %q, want nil on error", rendered)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestRenderScopeReadmeAcceptsATextFence(t *testing.T) {
	// A complete ```text fence is a table, not prose: it must be regenerated into
	// the single plain-fenced table the scripts write, proving the rows were
	// recognised instead of copied.
	body := scopeBody(t, "gravitino")
	textBody := "```text" + strings.TrimPrefix(body, "```")

	badge, _ := committedBlocks(t, "gravitino")
	doc := "# Terraform Provider for gravitino\n" +
		"\n" +
		"Statement coverage of this repository, merged from the unit and acceptance profiles:\n" +
		"\n" +
		badge +
		"\n" +
		"The gate is enforced on every pull request.\n" +
		"\n" +
		scopeBlockStart + "\n" + textBody + scopeBlockEnd + "\n" +
		"\n" +
		"The floor is enforced: the coverage gate fails a pull request below the baseline.\n"

	rendered, err := coverage.RenderScopeReadme(
		[]byte(doc), parseScopeFixture(t, "gravitino"))
	if err != nil {
		t.Fatalf("RenderScopeReadme() error = %v", err)
	}
	if want := scopeReadme(t, "gravitino"); string(rendered) != want {
		t.Errorf("RenderScopeReadme() =\n%s\nwant:\n%s", rendered, want)
	}
}

// scopeBody returns the committed coverage block's lines between its markers.
func scopeBody(t *testing.T, repo string) string {
	t.Helper()

	_, block := committedBlocks(t, repo)
	return strings.TrimSuffix(strings.TrimPrefix(block, scopeBlockStart+"\n"), scopeBlockEnd+"\n")
}

// scopeRows returns the table rows of a committed block, in order.
func scopeRows(block string) string {
	var rows []string
	for _, line := range strings.Split(block, "\n") {
		if strings.ContainsRune(line, '█') {
			rows = append(rows, line)
		}
	}
	return strings.Join(rows, "\n")
}

func TestRenderScopeReadmeEchoesTheBadgeToken(t *testing.T) {
	// Both scripts echo the measured token in the badge, so a baseline that
	// writes `90.0` publishes `coverage-90.0%25`, matching the table's `90.0%`.
	baseline, err := coverage.ParseScopeBaseline(strings.NewReader(
		"measured_total=90.0\nfloor_total=89.5\ntarget=80\ngold=90\n"))
	if err != nil {
		t.Fatalf("ParseScopeBaseline(): %v", err)
	}

	rendered, err := coverage.RenderScopeReadme(
		[]byte(scopeMarkersOnly("(#coverage)")), baseline)
	if err != nil {
		t.Fatalf("RenderScopeReadme() error = %v", err)
	}
	for _, want := range []string{
		"coverage-90.0%25-brightgreen",
		"  90.0%   floor 89.5%",
	} {
		if !strings.Contains(string(rendered), want) {
			t.Errorf("RenderScopeReadme() =\n%s\nwant it to contain %q", rendered, want)
		}
	}
}

func TestRenderScopeReadmeRejectsABareReadme(t *testing.T) {
	// The reverse direction: a scope baseline applied to a bare README would
	// rewrite the sentence into a table. The floor badge is the first give-away.
	doc := "# Title\n\n" +
		scopeBadgeStart + "\n" +
		"[![Coverage floor](https://img.shields.io/badge/coverage%20floor-80%25-green)](#testing-and-coverage)\n" +
		scopeBadgeEnd + "\n" +
		"\n" +
		scopeBlockStart + "\n" +
		"The coverage gate fails below **80%** (`make coverage-gate`).\n" +
		scopeBlockEnd + "\n"

	rendered, err := coverage.RenderScopeReadme([]byte(doc), parseScopeFixture(t, "gravitino"))

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderScopeReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if rendered != nil {
		t.Errorf("RenderScopeReadme() = %q, want nil on error", rendered)
	}
	for _, want := range []string{"floor badge", "key=value"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestRenderScopeReadmeRejectsABareCoverageBlock(t *testing.T) {
	// A measured scope badge with a sentence-only region is still the bare
	// shape, so the coverage region itself must be diagnosed.
	badge, _ := committedBlocks(t, "gravitino")
	doc := "# Title\n\n" + badge + "\n\n" +
		scopeBlockStart + "\n" +
		"The coverage gate fails below **80%** (`make coverage-gate`).\n" +
		scopeBlockEnd + "\n"

	rendered, err := coverage.RenderScopeReadme([]byte(doc), parseScopeFixture(t, "gravitino"))

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("RenderScopeReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if rendered != nil {
		t.Errorf("RenderScopeReadme() = %q, want nil on error", rendered)
	}
	for _, want := range []string{"bare-percentage sentence", "key=value"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestCheckScopeReadmeFailsOnAMarkerProblem(t *testing.T) {
	err := coverage.CheckScopeReadme([]byte("# Title\n\nNo blocks here.\n"), parseScopeFixture(t, "rustfs"))

	if !errors.Is(err, coverage.ErrReadmeMarkers) {
		t.Fatalf("CheckScopeReadme() error = %v, want ErrReadmeMarkers", err)
	}
	if errors.Is(err, coverage.ErrReadmeStale) {
		t.Errorf("CheckScopeReadme() error = %v, want a marker error rather than a stale one", err)
	}
}
