package branchprotection

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestJobsResolvesRepoRootReusableWorkflow(t *testing.T) {
	// GitHub requires local reusable workflows to be referenced from the
	// repository root, so a caller inside .github/workflows writes
	// "./.github/workflows/called.yml" and not "./called.yml".
	root := t.TempDir()
	workflows := filepath.Join(root, ".github", "workflows")

	writeFile(t, filepath.Join(workflows, "called.yml"), `on: workflow_call
jobs:
  lint:
    name: Lint & Security
  test:
    name: Test
`)
	caller := filepath.Join(workflows, "caller.yml")
	writeFile(t, caller, `on: pull_request
jobs:
  ci:
    uses: ./.github/workflows/called.yml
`)

	jobs, err := Jobs(caller)
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	want := []string{"ci / Lint & Security", "ci / Test"}
	if got := jobNames(jobs); !slices.Equal(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
	for _, job := range jobs {
		if job.Remote {
			t.Errorf("%q = %+v, want a resolved local call", job.Name, job)
		}
	}
	if err := ValidateContexts(jobs, want); err != nil {
		t.Errorf("ValidateContexts: %v", err)
	}

	// The trigger filter skips the called workflow, so the caller's composed
	// jobs are the only pull request contexts.
	prJobs, err := PullRequestJobs(root)
	if err != nil {
		t.Fatalf("PullRequestJobs: %v", err)
	}
	if got := jobNames(prJobs); !slices.Equal(got, want) {
		t.Errorf("pull request names = %q, want %q", got, want)
	}
}
