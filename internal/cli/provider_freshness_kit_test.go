package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kitLatest and kitTags are the two kit API reads: the newest release, and the
// tags a pin may legitimately name. `--slurp` wraps the paginated tag pages, so
// that fixture is an array of pages.
const kitLatest = `{"tag_name": "v0.14.0"}`

const kitTags = `[[
  {"name": "v0.12.0"},
  {"name": "v0.13.0"},
  {"name": "v0.14.0"}
]]`

func kitRoutes() map[string]string {
	return map[string]string{
		"releases/latest": kitLatest,
		"tags?per_page":   kitTags,
	}
}

// kitFixture writes a repository fixture: the caller's workflow pins and the
// GNUmakefile pin, which the kit job reads from the checked-out repository.
func kitFixture(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func kitPin(pin string) string {
	return "jobs:\n  freshness:\n    uses: caberdo/terraform-provider-kit/.github/workflows/provider-freshness.yml@" + pin + "\n"
}

func kitMakefile(pin string) string {
	return "TFKIT ?= go run github.com/caberdo/terraform-provider-kit/cmd/tfkit@" + pin + "\n"
}

// runKitCheck executes the kit step against a repository fixture. A finding
// makes the step exit 1, so the caller says whether that is expected.
func runKitCheck(t *testing.T, dir string, stub ghStub, expectFailure bool) stepRun {
	t.Helper()

	return runStepIn(
		t,
		dir,
		"provider-freshness.yml",
		"Compare the kit pins with the newest kit release",
		map[string]string{
			"inputs.kit-repo":         "caberdo/terraform-provider-kit",
			"inputs.kit-pin-glob":     ".github/workflows/*.yml",
			"inputs.provider-display": "Trino",
		},
		map[string]string{
			"REPO":     "caberdo/terraform-provider-trino",
			"GH_TOKEN": "token",
		},
		stub,
		expectFailure,
	)
}

// TestKitDeclaresTheCheckInputs pins the inputs the caller snippet relies on,
// and that the kit job is independent of the other two: it must run on its own
// monthly schedule even when upstream is clean.
func TestKitDeclaresTheCheckInputs(t *testing.T) {
	workflow := readWorkflow(t, "provider-freshness.yml")

	for name, want := range map[string]struct {
		typ     string
		initial any
	}{
		"kit-repo":     {typ: "string", initial: "caberdo/terraform-provider-kit"},
		"kit-pin-glob": {typ: "string", initial: ".github/workflows/*.yml"},
		"kit-check":    {typ: "boolean", initial: true},
	} {
		input, ok := workflow.On.WorkflowCall.Inputs[name]
		if !ok {
			t.Errorf("input %s is not declared", name)
			continue
		}
		if input.Type != want.typ {
			t.Errorf("%s type = %q, want %q", name, input.Type, want.typ)
		}
		if input.Default != want.initial {
			t.Errorf("%s default = %v, want %v", name, input.Default, want.initial)
		}
	}

	job, ok := workflow.Jobs["kit"]
	if !ok {
		t.Fatal("job kit is not declared")
	}
	if job.Needs != nil {
		t.Errorf("kit needs = %v, want no dependency on the other jobs", job.Needs)
	}
	if !strings.Contains(job.If, "inputs.kit-check") {
		t.Errorf("kit if = %q, want it gated on inputs.kit-check", job.If)
	}
}

// TestKitReportsAPinBehindTheLatestRelease is the common case: every pin is
// v0.13.0 while v0.14.0 is released.
func TestKitReportsAPinBehindTheLatestRelease(t *testing.T) {
	dir := kitFixture(t, map[string]string{
		".github/workflows/version-freshness.yml": kitPin("v0.13.0"),
		"GNUmakefile": kitMakefile("v0.13.0"),
	})
	run := runKitCheck(t, dir, ghStub{routes: kitRoutes()}, true)

	if run.exitCode != 1 {
		t.Errorf("exit code = %d, want 1; log %s", run.exitCode, run.log)
	}
	if !hasCall(run.calls, "issue create") {
		t.Fatalf("a stale pin did not open an issue; gh calls = %q", run.calls)
	}

	body := issueBody(t, run)
	for _, want := range []string{
		"`v0.13.0`",
		"`GNUmakefile`",
		"`.github/workflows/version-freshness.yml`",
		"is behind `v0.14.0`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("issue body does not name %q:\n%s", want, body)
		}
	}
}

// TestKitReportsPinsThatDisagree covers the mixed set: a repository that
// advanced one workflow but not the makefile or a second workflow.
func TestKitReportsPinsThatDisagree(t *testing.T) {
	dir := kitFixture(t, map[string]string{
		".github/workflows/version-freshness.yml": kitPin("v0.14.0"),
		".github/workflows/release.yml":           kitPin("v0.13.0"),
		"GNUmakefile":                             kitMakefile("v0.13.0"),
	})
	run := runKitCheck(t, dir, ghStub{routes: kitRoutes()}, true)

	if run.exitCode != 1 {
		t.Errorf("exit code = %d, want 1; log %s", run.exitCode, run.log)
	}
	if body := issueBody(t, run); !strings.Contains(body, "pins 2 different kit tags") {
		t.Errorf("issue body does not name the disagreement:\n%s", body)
	}
}

// TestKitReportsATagThatDoesNotExist is the typo/stale-ref case: a pin above
// the newest release names no tag at all.
func TestKitReportsATagThatDoesNotExist(t *testing.T) {
	dir := kitFixture(t, map[string]string{
		".github/workflows/version-freshness.yml": kitPin("v0.15.0"),
		"GNUmakefile": kitMakefile("v0.15.0"),
	})
	run := runKitCheck(t, dir, ghStub{routes: kitRoutes()}, true)

	if run.exitCode != 1 {
		t.Errorf("exit code = %d, want 1; log %s", run.exitCode, run.log)
	}
	if body := issueBody(t, run); !strings.Contains(body, "`v0.15.0` in `.github/workflows/version-freshness.yml`, `GNUmakefile` is not a tag") {
		t.Errorf("issue body does not name the missing tag:\n%s", body)
	}
}

// TestKitClosesWhenEveryPinIsTheLatestRelease covers the clean run, including
// the stale issue it must close.
func TestKitClosesWhenEveryPinIsTheLatestRelease(t *testing.T) {
	dir := kitFixture(t, map[string]string{
		".github/workflows/version-freshness.yml": kitPin("v0.14.0"),
		"GNUmakefile": kitMakefile("v0.14.0"),
	})
	run := runKitCheck(t, dir, ghStub{issue: "3", routes: kitRoutes()}, false)

	if run.exitCode != 0 {
		t.Errorf("exit code = %d, want 0; log %s", run.exitCode, run.log)
	}
	if !hasCall(run.calls, "issue comment 3") {
		t.Errorf("the cleared kit issue was not commented on; gh calls = %q", run.calls)
	}
	if !hasCall(run.calls, "issue close 3") {
		t.Errorf("the cleared kit issue was not closed; gh calls = %q", run.calls)
	}
	if hasCall(run.calls, "issue create") || hasCall(run.calls, "issue edit") {
		t.Errorf("a clean repository wrote an issue; gh calls = %q", run.calls)
	}
}
