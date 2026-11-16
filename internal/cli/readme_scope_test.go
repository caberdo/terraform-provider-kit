package cli_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/cli"
)

// scopeFixtureDir holds the four real blocks and the two committed baselines,
// so the CLI contract is tested against the files gravitino and rustfs publish.
const scopeFixtureDir = "../coverage/testdata/"

// scopeFixtureRead reads one fixture and fails the test on error.
func scopeFixtureRead(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(scopeFixtureDir + name)
	if err != nil {
		t.Fatalf("read %s: %v", scopeFixtureDir+name, err)
	}
	return string(data)
}

// scopeReadmeDoc assembles the gravitino README around its committed blocks.
func scopeReadmeDoc(t *testing.T) string {
	t.Helper()

	return "# Terraform Provider for Apache Gravitino\n" +
		"\n" +
		scopeFixtureRead(t, "gravitino-coverage-badge-block.txt") +
		"\n" +
		"The gate is enforced on every pull request.\n" +
		"\n" +
		scopeFixtureRead(t, "gravitino-coverage-block.txt") +
		"\n" +
		"The floor is enforced.\n"
}

func TestRunCoverageReadmeScopeCheckAcceptsTheCommittedReadme(t *testing.T) {
	readme := readmeFile(t, "README.md", scopeReadmeDoc(t))
	baseline := readmeFile(t, "coverage-baseline.txt",
		scopeFixtureRead(t, "gravitino-coverage-baseline.txt"))

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme", "--check",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if want := "coverage-readme: '" + readme + "' is in sync with '" + baseline + "'\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

func TestRunCoverageReadmeScopeIgnoresTheGateCommand(t *testing.T) {
	// The scope block has no sentence naming the gate, so the flag must not
	// alter the rendered bytes.
	readme := readmeFile(t, "README.md", scopeReadmeDoc(t))
	baseline := readmeFile(t, "coverage-baseline.txt",
		scopeFixtureRead(t, "gravitino-coverage-baseline.txt"))

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme", "--check",
		"--readme", readme,
		"--baseline", baseline,
		"--gate-command", "make coverage-gate-strict",
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
}

func TestRunCoverageReadmeScopeRewritesAStaleReadme(t *testing.T) {
	readme := readmeFile(t, "README.md", scopeReadmeDoc(t))
	baseline := readmeFile(t, "coverage-baseline.txt", strings.Replace(
		scopeFixtureRead(t, "gravitino-coverage-baseline.txt"),
		"measured_total=83.4", "measured_total=84.4", 1))

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	want := "coverage-readme: updated '" + readme + "' from '" + baseline + "'\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
	got := readFile(t, readme)
	for _, wantLine := range []string{
		"coverage-84.4%25",
		"  84.4%   floor 82.5%   -> 80%, then 90%",
		"Measured on `c2fc783` (unit + acceptance) on 2026-10-19.",
	} {
		if !strings.Contains(got, wantLine) {
			t.Errorf("README =\n%s\nwant it to contain %q", got, wantLine)
		}
	}
	if strings.Contains(got, "coverage-83.4%25") {
		t.Errorf("README =\n%s\nwant the old badge number replaced", got)
	}

	// The rewrite is stable: a second --check must pass.
	var checkOut, checkErr bytes.Buffer
	if code := cli.Run([]string{
		"coverage", "readme", "--check",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &checkOut, &checkErr); code != 0 {
		t.Fatalf("--check after rewrite: exit = %d, want 0 (stderr = %q)", code, checkErr.String())
	}
}

func TestRunCoverageReadmeScopeCheckReportsStale(t *testing.T) {
	stale := scopeReadmeDoc(t)
	readme := readmeFile(t, "README.md", stale)
	baseline := readmeFile(t, "coverage-baseline.txt", strings.Replace(
		scopeFixtureRead(t, "gravitino-coverage-baseline.txt"),
		"measured_total=83.4", "measured_total=84.4", 1))

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme", "--check",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{readme, baseline, "out of date", "make coverage-readme"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if got := readFile(t, readme); got != stale {
		t.Errorf("--check rewrote the README:\n%s\nwant it unchanged:\n%s", got, stale)
	}
}

func TestRunCoverageReadmeScopeHalfRenderedExitsTwo(t *testing.T) {
	// A scope README with only the table pair would be half rendered: the badge
	// would silently keep an old number, so it is a configuration error.
	half := "# Title\n\n" + scopeFixtureRead(t, "gravitino-coverage-block.txt") + "\nProse.\n"
	readme := readmeFile(t, "README.md", half)
	baseline := readmeFile(t, "coverage-baseline.txt",
		scopeFixtureRead(t, "gravitino-coverage-baseline.txt"))

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{readme, "coverage-badge:start", "coverage-badge:end"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if got := readFile(t, readme); got != half {
		t.Errorf("README =\n%s\nwant it unchanged after a marker error", got)
	}
}

func TestRunCoverageReadmeBareHalfRenderedExitsTwo(t *testing.T) {
	// A bare README carrying only the sentence pair is half rendered: the
	// badge cannot be regenerated, so it is a configuration error rather than
	// something to rewrite.
	half := "# Title\n\n" +
		"<!-- coverage:start -->\n" +
		"The coverage gate fails below **80%** (`make coverage-gate`).\n" +
		"<!-- coverage:end -->\n" +
		"\nProse.\n"
	readme := readmeFile(t, "README.md", half)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{readme, "coverage-badge:start", "coverage-badge:end"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if got := readFile(t, readme); got != half {
		t.Errorf("README =\n%s\nwant it unchanged after a marker error", got)
	}
}

func TestRunCoverageReadmeBareBaselineRejectsAScopeReadme(t *testing.T) {
	// The baseline selects the dialect. A bare baseline applied to a scope
	// README must fail closed rather than replace the table and the prose inside
	// it with one sentence.
	doc := "# Terraform Provider for x\n" +
		"\n" +
		scopeFixtureRead(t, "gravitino-coverage-badge-block.txt") + "\n" +
		"\n" +
		"The gate is enforced on every pull request.\n" +
		"\n" +
		scopeFixtureRead(t, "gravitino-coverage-block.txt") + "\n"
	readme := readmeFile(t, "README.md", doc)
	baseline := readmeFile(t, "coverage-baseline.txt", "80\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{readme, "key=value coverage table", "single percentage"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if got := readFile(t, readme); got != doc {
		t.Errorf("README =\n%s\nwant it unchanged after a marker error", got)
	}
}

func TestRunCoverageReadmeScopeBaselineRejectsABareReadme(t *testing.T) {
	// The reverse direction must also fail closed: the sentence would otherwise
	// be rewritten into a per-scope table.
	readme := readmeFile(t, "README.md", readmeDoc)
	baseline := readmeFile(t, "coverage-baseline.txt",
		scopeFixtureRead(t, "gravitino-coverage-baseline.txt"))

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	for _, want := range []string{readme, "floor badge", "key=value"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
	if got := readFile(t, readme); got != readmeDoc {
		t.Errorf("README =\n%s\nwant it unchanged after a marker error", got)
	}
}

func TestRunCoverageReadmeScopeMalformedRegionExitsTwo(t *testing.T) {
	// Finding A: a corrupt table region must exit 2 without writing, rather than
	// republishing the old rows beside a fresh table.
	doc := strings.Replace(scopeReadmeDoc(t), "```\n\nMerged statement coverage", "\nMerged statement coverage", 1)
	if doc == scopeReadmeDoc(t) {
		t.Fatal("the fixture did not change; the closing fence was not found")
	}
	readme := readmeFile(t, "README.md", doc)
	baseline := readmeFile(t, "coverage-baseline.txt",
		scopeFixtureRead(t, "gravitino-coverage-baseline.txt"))

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "inside its fence") {
		t.Errorf("stderr = %q, want the region diagnosis", stderr.String())
	}
	if got := readFile(t, readme); got != doc {
		t.Errorf("README =\n%s\nwant it unchanged after a region error", got)
	}
}

func TestRunCoverageReadmeScopeMalformedBaselineExitsTwo(t *testing.T) {
	readme := readmeFile(t, "README.md", scopeReadmeDoc(t))
	baseline := readmeFile(t, "coverage-baseline.txt",
		"measured_total=83.4\nfloor_total=eighty\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"coverage", "readme",
		"--readme", readme,
		"--baseline", baseline,
	}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "eighty") {
		t.Errorf("stderr = %q, want it to quote the bad baseline", stderr.String())
	}
}
