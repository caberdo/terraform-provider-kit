package cli_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// canaryFailedJobs is the shape of `gh api .../jobs --paginate --slurp`: one
// page holding one failed and one successful job. Only the failed job's log is
// downloaded.
const canaryFailedJobs = `[{
  "jobs": [
    {"id": 42, "conclusion": "failure"},
    {"id": 43, "conclusion": "success"}
  ]
}]`

// canaryLog is the tail of a failed acceptance job: one behaviour failure and a
// package-level FAIL, which is what the report must name.
const canaryLog = `=== RUN   TestAccThing
--- FAIL: TestAccThing (0.01s)
FAIL	github.com/caberdo/terraform-provider-trino/internal/provider	0.5s
FAIL	github.com/caberdo/terraform-provider-trino/internal/client	0.2s
`

func canaryRoutes() map[string]string {
	return map[string]string{
		"actions/runs/99/jobs": canaryFailedJobs,
		"actions/jobs/42/logs": canaryLog,
	}
}

// runCanaryReport executes the step that turns the reusable workflow's result
// into one canary-failure issue. The canary job itself calls a reusable
// workflow, so its result arrives as the RESULT variable rather than as shell.
func runCanaryReport(t *testing.T, result string, stub ghStub) stepRun {
	t.Helper()

	return runStep(
		t,
		"provider-freshness.yml",
		"Open, update or close the canary issue",
		map[string]string{},
		map[string]string{
			"REPO":     "caberdo/terraform-provider-trino",
			"GH_TOKEN": "token",
			"SERVER":   "https://github.com",
			"RUN_ID":   "99",
			"DISPLAY":  "Trino",
			"VERSION":  "487",
			"RESULT":   result,
		},
		stub,
	)
}

// TestCanarySkipsWhenNothingIsMissing pins the job gate: a canary must not run
// when the matrix is complete (behind is false) and must not run with an empty
// version when the gap is below the newest pin.
func TestCanarySkipsWhenNothingIsMissing(t *testing.T) {
	condition := readWorkflow(t, "provider-freshness.yml").Jobs["canary"].If

	for _, testCase := range []struct {
		name string
		gate string
	}{
		{name: "behind is false", gate: "needs.upstream.outputs.behind == 'true'"},
		{name: "the newest missing version is empty", gate: "needs.upstream.outputs.newest-missing != ''"},
		{name: "the caller opted in", gate: "inputs.canary"},
	} {
		if !strings.Contains(condition, testCase.gate) {
			t.Errorf("canary if = %q, want it gated on %s", condition, testCase.name)
		}
	}
}

// TestCanaryCallsAcceptanceForOneVersion pins the caller contract: the newest
// missing release becomes the single matrix leg, the gate cell is disabled and
// a missing profile is tolerated, because a canary never gates a bump.
func TestCanaryCallsAcceptanceForOneVersion(t *testing.T) {
	job, ok := readWorkflow(t, "provider-freshness.yml").Jobs["canary"]
	if !ok {
		t.Fatal("job canary is not declared")
	}
	if !strings.Contains(job.Uses, "/.github/workflows/provider-acceptance.yml@") {
		t.Errorf("canary uses = %q, want a pinned call of provider-acceptance.yml", job.Uses)
	}

	want := map[string]string{
		"provider-display":                   "${{ inputs.provider-display }}",
		"versions-file":                      "${{ inputs.versions-file }}",
		"version-env":                        "${{ inputs.version-env }}",
		"expect-version-env":                 "${{ inputs.expect-version-env }}",
		"acceptance-compose":                 "${{ inputs.acceptance-compose }}",
		"compose-file-args":                  "${{ inputs.compose-file-args }}",
		"version":                            "${{ needs.upstream.outputs.newest-missing }}",
		"gate-cell":                          "none",
		"fail-on-missing-acceptance-profile": "false",
	}
	for key, value := range want {
		got, ok := job.With[key]
		if !ok {
			t.Errorf("canary does not pass with.%s", key)
			continue
		}
		if fmt.Sprint(got) != value {
			t.Errorf("canary with.%s = %v, want %q", key, got, value)
		}
	}
}

func TestCanaryReportOpensOneIssueNamingTheVersionAndPackages(t *testing.T) {
	run := runCanaryReport(t, "failure", ghStub{routes: canaryRoutes()})

	if !hasCall(run.calls, "issue create") {
		t.Fatalf("a failed canary did not open an issue; gh calls = %q", run.calls)
	}
	if hasCall(run.calls, "issue edit") {
		t.Errorf("a failed canary edited an issue it did not open; gh calls = %q", run.calls)
	}

	body := issueBody(t, run)
	for _, want := range []string{
		"canary failed on `487`",
		"internal/provider",
		"internal/client",
		"TestAccThing",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("issue body does not name %q:\n%s", want, body)
		}
	}
}

func TestCanaryReportUpdatesTheSameIssueOnASecondFailure(t *testing.T) {
	run := runCanaryReport(t, "failure", ghStub{issue: "7", routes: canaryRoutes()})

	if !hasCall(run.calls, "issue edit 7") {
		t.Errorf("the existing issue was not updated in place; gh calls = %q", run.calls)
	}
	if hasCall(run.calls, "issue create") {
		t.Errorf("a second failure opened a duplicate issue; gh calls = %q", run.calls)
	}
	if body := issueBody(t, run); !strings.Contains(body, "canary failed on `487`") {
		t.Errorf("the updated body lost the version:\n%s", body)
	}
}

func TestCanaryReportClosesAPassingCanary(t *testing.T) {
	run := runCanaryReport(t, "success", ghStub{issue: "7"})

	if !hasCall(run.calls, "issue comment 7") {
		t.Errorf("a passing canary did not comment on the open issue; gh calls = %q", run.calls)
	}
	if !hasCall(run.calls, "issue close 7") {
		t.Errorf("a passing canary did not close the open issue; gh calls = %q", run.calls)
	}
	if hasCall(run.calls, "issue create") || hasCall(run.calls, "issue edit") {
		t.Errorf("a passing canary wrote an issue; gh calls = %q", run.calls)
	}
}

func TestCanaryReportStaysQuietWhenTheCanaryDidNotRun(t *testing.T) {
	run := runCanaryReport(t, "skipped", ghStub{issue: "7"})

	if len(run.calls) != 0 {
		t.Errorf("a skipped canary touched GitHub; gh calls = %q", run.calls)
	}
}

// issueBody reads the body file the step handed to `gh issue create/edit`.
func issueBody(t *testing.T, run stepRun) string {
	t.Helper()

	for _, call := range run.calls {
		fields := strings.Fields(call)
		for i, field := range fields {
			if field != "--body-file" || i+1 >= len(fields) {
				continue
			}
			body, err := os.ReadFile(fields[i+1])
			if err != nil {
				t.Fatalf("read issue body %q: %v", fields[i+1], err)
			}
			return string(body)
		}
	}
	t.Fatalf("the step wrote no issue body; gh calls = %q", run.calls)
	return ""
}
