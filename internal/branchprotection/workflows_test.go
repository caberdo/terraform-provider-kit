package branchprotection

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixtureRepo is a miniature provider checkout with one workflow per shape the
// parser must understand.
const fixtureRepo = "testdata/repo"

func TestJobsReadsNameAndDefaultsToKey(t *testing.T) {
	jobs, err := Jobs(filepath.Join(fixtureRepo, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}

	if got, want := jobNames(jobs), []string{"Lint & Security", "test"}; !slices.Equal(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
	for _, job := range jobs {
		if job.HasIf || job.HasMatrix || job.Remote {
			t.Errorf("%q = %+v, want a plain job", job.Name, job)
		}
	}
}

func TestJobsReadsIfAndMatrix(t *testing.T) {
	jobs, err := Jobs(filepath.Join(fixtureRepo, ".github/workflows/conditional.yml"))
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(jobs) != 1 || !jobs[0].HasIf {
		t.Fatalf("jobs = %+v, want one job with HasIf", jobs)
	}

	jobs, err = Jobs(filepath.Join(fixtureRepo, ".github/workflows/matrix.yml"))
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(jobs) != 1 || !jobs[0].HasMatrix {
		t.Fatalf("jobs = %+v, want one job with HasMatrix", jobs)
	}
}

func TestJobsComposesReusableWorkflowContexts(t *testing.T) {
	jobs, err := Jobs(filepath.Join(fixtureRepo, ".github/workflows/caller.yml"))
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}

	if got, want := jobNames(jobs), []string{"ci / Lint & Security", "ci / Test"}; !slices.Equal(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
}

func TestJobsPropagatesTheCallersCondition(t *testing.T) {
	// A caller job with an `if:` makes every context it reports skippable, so
	// the composed jobs must inherit the flag.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "called.yml"), `on: workflow_call
jobs:
  test:
    name: Test
`)
	path := filepath.Join(dir, "caller.yml")
	writeFile(t, path, `on: pull_request
jobs:
  ci:
    if: github.event_name == 'push'
    uses: ./called.yml
`)

	jobs, err := Jobs(path)
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v, want one composed job", jobs)
	}
	if got, want := jobs[0].Name, "ci / Test"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if !jobs[0].HasIf {
		t.Errorf("job = %+v, want HasIf propagated from the caller", jobs[0])
	}
}

func TestJobsTreatsTheOnKeyAsAString(t *testing.T) {
	// yaml.v2 decoded an unquoted `on:` key as the boolean true, which is why
	// the parser walks nodes instead of decoding into a map. A quoted key must
	// behave exactly like the unquoted form.
	path := filepath.Join(t.TempDir(), "workflow.yml")
	writeFile(t, path, `name: Quoted
"on":
  pull_request:
jobs:
  verify:
    name: Verify
`)

	jobs, err := Jobs(path)
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	if got, want := jobNames(jobs), []string{"Verify"}; !slices.Equal(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
}

func TestPullRequestJobsIgnoresWorkflowsWithoutPRTrigger(t *testing.T) {
	jobs, err := PullRequestJobs(fixtureRepo)
	if err != nil {
		t.Fatalf("PullRequestJobs: %v", err)
	}

	names := jobNames(jobs)
	for _, name := range names {
		if name == "Publish" {
			t.Fatalf("names = %q, want the push-only workflow ignored", names)
		}
		if name == "Target Job" {
			t.Fatalf("names = %q, want pull_request_target ignored", names)
		}
	}

	for _, want := range []string{"Lint & Security", "test", "Deploy", "Acceptance Tests",
		"ci / Lint & Security", "ci / Test", "Sequence Job", "Scalar Job"} {
		if !slices.Contains(names, want) {
			t.Errorf("names = %q, want %q included", names, want)
		}
	}
	if len(names) != 8 {
		t.Errorf("got %d names %q, want 8", len(names), names)
	}
}

func TestPullRequestJobsResolvesRemoteReusableWorkflow(t *testing.T) {
	github := &fakeGitHub{workflows: map[string][]byte{
		"caberdo/terraform-provider-kit/.github/workflows/provider-ci.yml@v0.1.0": []byte(calledWorkflow),
	}}

	jobs, err := pullRequestJobs("testdata/remote-repo", github)
	if err != nil {
		t.Fatalf("pullRequestJobs: %v", err)
	}

	if got, want := jobNames(jobs), []string{"ci / Lint & Security", "ci / Test"}; !slices.Equal(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
}

func TestPullRequestJobsReportsAnUnresolvedRemoteCaller(t *testing.T) {
	jobs, err := pullRequestJobs("testdata/remote-repo", &fakeGitHub{})
	if err != nil {
		t.Fatalf("pullRequestJobs: %v", err)
	}
	if len(jobs) != 1 || !jobs[0].Remote || jobs[0].Name != "ci" {
		t.Fatalf("jobs = %+v, want the bare remote caller job", jobs)
	}

	err = ValidateContexts(jobs, []string{"ci / Lint & Security"})
	if err == nil || !strings.Contains(err.Error(), "reusable workflow") {
		t.Fatalf("ValidateContexts = %v, want a reusable-workflow diagnostic", err)
	}
}

func TestValidateContextsAcceptsPlainJob(t *testing.T) {
	jobs := fixtureJobs(t, "ci.yml")
	if err := ValidateContexts(jobs, []string{"Lint & Security", "test"}); err != nil {
		t.Fatalf("ValidateContexts: %v", err)
	}
}

func TestValidateContextsRejectsUnknownContext(t *testing.T) {
	jobs := fixtureJobs(t, "ci.yml")
	err := ValidateContexts(jobs, []string{"Missing"})
	if err == nil || !strings.Contains(err.Error(), "not a job name") {
		t.Fatalf("ValidateContexts = %v, want a not-a-job-name error", err)
	}
}

func TestValidateContextsRejectsDuplicateContext(t *testing.T) {
	jobs := []Job{{Name: "Test", Key: "one"}, {Name: "Test", Key: "two"}}
	err := ValidateContexts(jobs, []string{"Test"})
	if err == nil || !strings.Contains(err.Error(), "matches 2 jobs") {
		t.Fatalf("ValidateContexts = %v, want a duplicate error", err)
	}
}

func TestValidateContextsRejectsMatrixJob(t *testing.T) {
	jobs := fixtureJobs(t, "matrix.yml")
	err := ValidateContexts(jobs, []string{"Acceptance Tests"})
	if err == nil || !strings.Contains(err.Error(), "matrix") {
		t.Fatalf("ValidateContexts = %v, want a matrix error", err)
	}
}

func TestValidateContextsRejectsConditionalJob(t *testing.T) {
	jobs := fixtureJobs(t, "conditional.yml")
	err := ValidateContexts(jobs, []string{"Deploy"})
	if err == nil || !strings.Contains(err.Error(), "if:") {
		t.Fatalf("ValidateContexts = %v, want an if: error", err)
	}
}

func TestValidateContextsAcceptsComposedReusableWorkflowContext(t *testing.T) {
	jobs := fixtureJobs(t, "caller.yml")
	if err := ValidateContexts(jobs, []string{"ci / Lint & Security", "ci / Test"}); err != nil {
		t.Fatalf("ValidateContexts: %v", err)
	}
}

func TestValidateContextsRejectsABareCallerJobName(t *testing.T) {
	// GitHub reports "<caller> / <called>" for a delegating job, never the bare
	// caller id, so requiring the id would block every pull request.
	jobs, err := pullRequestJobs("testdata/remote-repo", &fakeGitHub{})
	if err != nil {
		t.Fatalf("pullRequestJobs: %v", err)
	}

	err = ValidateContexts(jobs, []string{"ci"})
	if err == nil || !strings.Contains(err.Error(), "reusable workflow") {
		t.Fatalf("ValidateContexts = %v, want a caller diagnostic", err)
	}
}

func TestJobsRejectsAMissingFile(t *testing.T) {
	if _, err := Jobs(filepath.Join(t.TempDir(), "absent.yml")); err == nil {
		t.Fatal("Jobs returned nil error for a missing file")
	}
}

func TestJobsRejectsACircularCall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yml"), `on: pull_request
jobs:
  a:
    uses: ./b.yml
`)
	writeFile(t, filepath.Join(dir, "b.yml"), `on: workflow_call
jobs:
  b:
    uses: ./a.yml
`)

	if _, err := Jobs(filepath.Join(dir, "a.yml")); err == nil {
		t.Fatal("Jobs returned nil error for a circular call")
	}
}

func TestJobsRejectsAMalformedJob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.yml")
	writeFile(t, path, "on: pull_request\njobs:\n  broken: a string\n")

	if _, err := Jobs(path); err == nil {
		t.Fatal("Jobs returned nil error for a job that is not a mapping")
	}
}

// calledWorkflow is what the fake fetcher returns for the kit's reusable CI
// workflow.
const calledWorkflow = `name: Provider CI
on:
  workflow_call:
jobs:
  lint:
    name: Lint & Security
    runs-on: ubuntu-latest
    steps:
      - run: echo lint
  test:
    name: Test
    runs-on: ubuntu-latest
    steps:
      - run: echo test
`

func fixtureJobs(t *testing.T, name string) []Job {
	t.Helper()
	jobs, err := Jobs(filepath.Join(fixtureRepo, ".github/workflows", name))
	if err != nil {
		t.Fatalf("Jobs(%s): %v", name, err)
	}
	return jobs
}

func jobNames(jobs []Job) []string {
	names := make([]string, 0, len(jobs))
	for _, job := range jobs {
		names = append(names, job.Name)
	}
	return names
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// fakeGitHub is the test double for the gh seam. It is shared by the workflow,
// check and setup tests.
type fakeGitHub struct {
	// responses are consulted in order; the first entry whose match is a
	// substring of the joined arguments wins.
	responses []fakeResponse
	// workflows maps "owner/repo/path@ref" to the fetched workflow.
	workflows map[string][]byte
	// calls records every gh invocation, in order.
	calls [][]string
	// inputs records stdin for invocations that receive a payload.
	inputs [][]byte
}

// fakeResponse is one canned gh invocation.
type fakeResponse struct {
	match  string
	stdout string
	err    error
}

func (f *fakeGitHub) Run(args []string, input []byte) ([]byte, error) {
	f.calls = append(f.calls, args)
	f.inputs = append(f.inputs, input)

	joined := strings.Join(args, " ")
	for _, response := range f.responses {
		if strings.Contains(joined, response.match) {
			return []byte(response.stdout), response.err
		}
	}
	return nil, fmt.Errorf("unexpected gh invocation: %s", joined)
}

func (f *fakeGitHub) Workflow(owner, repo, path, ref string) ([]byte, error) {
	key := fmt.Sprintf("%s/%s/%s@%s", owner, repo, path, ref)
	content, ok := f.workflows[key]
	if !ok {
		return nil, errors.New("no such workflow")
	}
	return content, nil
}
