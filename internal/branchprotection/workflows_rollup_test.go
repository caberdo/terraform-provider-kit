package branchprotection

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateContextsAcceptsAlwaysRollUp(t *testing.T) {
	// The acceptance workflow keeps its required context stable with a roll-up
	// job over the matrix: non-matrix, needs the matrix job, if always().
	path := filepath.Join(t.TempDir(), "acceptance.yml")
	writeFile(t, path, `on: pull_request
jobs:
  acceptance:
    name: Acceptance Tests
    strategy:
      matrix:
        version: [1, 2]
    runs-on: ubuntu-latest

  roll-up:
    name: Acceptance
    needs: [acceptance]
    if: always()
    runs-on: ubuntu-latest
`)

	jobs, err := Jobs(path)
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}

	rollUp, ok := findJob(jobs, "Acceptance")
	if !ok {
		t.Fatalf("jobs = %+v, want an Acceptance job", jobs)
	}
	if !rollUp.HasIf {
		t.Errorf("HasIf = false, want true")
	}
	if !rollUp.HasNeeds {
		t.Errorf("HasNeeds = false, want true")
	}
	if rollUp.HasMatrix {
		t.Errorf("HasMatrix = true, want false")
	}
	if got, want := rollUp.Condition, "always()"; got != want {
		t.Errorf("Condition = %q, want %q", got, want)
	}

	if err := ValidateContexts(jobs, []string{"Acceptance"}); err != nil {
		t.Fatalf("ValidateContexts: %v", err)
	}
}

func TestValidateContextsAcceptsAlwaysRollUpSpellings(t *testing.T) {
	for _, condition := range []string{"always()", "always() ", "ALWAYS()", "${{ always() }}", "${{always()}}"} {
		t.Run(condition, func(t *testing.T) {
			jobs := workflowWithCondition(t, condition, true, false)

			if err := ValidateContexts(jobs, []string{"Roll Up"}); err != nil {
				t.Fatalf("ValidateContexts(%q): %v", condition, err)
			}
		})
	}
}

func TestValidateContextsRejectsConditionalJobs(t *testing.T) {
	tests := []struct {
		name      string
		condition string
		needs     bool
		matrix    bool
		want      string
	}{
		{
			name:      "compounded always",
			condition: "always() && github.event_name == 'push'",
			needs:     true,
			want:      "if:",
		},
		{
			name:      "success",
			condition: "success()",
			needs:     true,
			want:      "if:",
		},
		{
			name:      "wrapped success",
			condition: "${{ success() }}",
			needs:     true,
			want:      "if:",
		},
		{
			name:      "always without needs",
			condition: "always()",
			needs:     false,
			want:      "needs:",
		},
		{
			name:      "always with matrix",
			condition: "always()",
			needs:     true,
			matrix:    true,
			want:      "matrix",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jobs := workflowWithCondition(t, test.condition, test.needs, test.matrix)

			err := ValidateContexts(jobs, []string{"Roll Up"})
			if err == nil {
				t.Fatalf("ValidateContexts(%q) = nil, want an error", test.condition)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("ValidateContexts(%q) = %v, want %q in the message", test.condition, err, test.want)
			}
		})
	}
}

func TestValidateContextsRejectsRollUpLaunderedThroughACaller(t *testing.T) {
	// The called workflow's Report job is a roll-up on its own, but it runs
	// under a caller whose condition is compounded, so the composed context is
	// not stable. Clearing Condition and HasNeeds is what keeps it rejected.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "called.yml"), `on: workflow_call
jobs:
  build:
    name: Build
    runs-on: ubuntu-latest

  report:
    name: Report
    needs: [build]
    if: always()
    runs-on: ubuntu-latest
`)
	caller := filepath.Join(dir, "caller.yml")
	writeFile(t, caller, `on: pull_request
jobs:
  ci:
    if: always() && github.ref == 'refs/heads/main'
    uses: ./called.yml
`)

	jobs, err := Jobs(caller)
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}

	composed, ok := findJob(jobs, "ci / Report")
	if !ok {
		t.Fatalf("jobs = %+v, want a composed ci / Report job", jobs)
	}
	if !composed.HasIf {
		t.Errorf("HasIf = false, want the caller's condition propagated")
	}
	if composed.HasNeeds {
		t.Errorf("HasNeeds = true, want it cleared under a conditional caller")
	}
	if composed.Condition != "" {
		t.Errorf("Condition = %q, want it cleared under a conditional caller", composed.Condition)
	}

	err = ValidateContexts(jobs, []string{"ci / Report"})
	if err == nil || !strings.Contains(err.Error(), "if:") {
		t.Fatalf("ValidateContexts = %v, want an if: error for the composed context", err)
	}
}

// workflowWithCondition writes a workflow whose "Roll Up" job carries if: with
// the given condition, and optionally needs: and a strategy matrix.
func workflowWithCondition(t *testing.T, condition string, needs, matrix bool) []Job {
	t.Helper()

	var workflow strings.Builder
	workflow.WriteString(`on: pull_request
jobs:
  dep:
    name: Dep
    runs-on: ubuntu-latest

  rollup:
    name: Roll Up
`)
	if needs {
		workflow.WriteString("    needs: [dep]\n")
	}
	if matrix {
		workflow.WriteString("    strategy:\n      matrix:\n        version: [1, 2]\n")
	}
	fmt.Fprintf(&workflow, "    if: %q\n", condition)
	workflow.WriteString("    runs-on: ubuntu-latest\n")

	path := filepath.Join(t.TempDir(), "workflow.yml")
	writeFile(t, path, workflow.String())

	jobs, err := Jobs(path)
	if err != nil {
		t.Fatalf("Jobs: %v", err)
	}
	return jobs
}

func findJob(jobs []Job, name string) (Job, bool) {
	for _, job := range jobs {
		if job.Name == name {
			return job, true
		}
	}
	return Job{}, false
}
