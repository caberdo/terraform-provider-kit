package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// calledWorkflow is the shape these tests read: the workflow_call inputs and
// the job keys, display names and conditions that gate them.
type calledWorkflow struct {
	On struct {
		WorkflowCall struct {
			Inputs map[string]struct {
				Type    string `yaml:"type"`
				Default any    `yaml:"default"`
			} `yaml:"inputs"`
		} `yaml:"workflow_call"`
	} `yaml:"on"`
	Jobs map[string]struct {
		Name        string `yaml:"name"`
		If          string `yaml:"if"`
		Concurrency struct {
			Group            string `yaml:"group"`
			CancelInProgress bool   `yaml:"cancel-in-progress"`
		} `yaml:"concurrency"`
		Steps []struct {
			Name string `yaml:"name"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// readWorkflow parses one of the kit's reusable workflows.
func readWorkflow(t *testing.T, name string) calledWorkflow {
	t.Helper()

	content, err := os.ReadFile("../../.github/workflows/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var workflow calledWorkflow
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return workflow
}

func TestProviderCIDeclaresTheChangelogFragmentJob(t *testing.T) {
	workflow := readWorkflow(t, "provider-ci.yml")

	input, ok := workflow.On.WorkflowCall.Inputs["changelog-fragments"]
	if !ok {
		t.Fatal("input changelog-fragments is not declared")
	}
	if input.Type != "boolean" {
		t.Errorf("changelog-fragments type = %q, want boolean", input.Type)
	}
	if input.Default != false {
		t.Errorf("changelog-fragments default = %v, want false", input.Default)
	}

	job, ok := workflow.Jobs["changelog"]
	if !ok {
		t.Fatal("job changelog is not declared")
	}
	if job.Name != "Changelog Fragment" {
		t.Errorf("job changelog name = %q, want %q", job.Name, "Changelog Fragment")
	}
	if !strings.Contains(job.If, "inputs.changelog-fragments") {
		t.Errorf("job changelog if = %q, want it gated on inputs.changelog-fragments", job.If)
	}
	if !strings.Contains(job.If, "github.event_name == 'pull_request'") {
		t.Errorf("job changelog if = %q, want it gated on a pull request", job.If)
	}
}

// TestProviderCIChangelogJobKeepsItsExemptions pins the exemptions rustfs
// already implements. A change that no fragment describes must pass, so the
// path filter has to cover Go code, workflows, docs and examples only.
func TestProviderCIChangelogJobKeepsItsExemptions(t *testing.T) {
	job, ok := readWorkflow(t, "provider-ci.yml").Jobs["changelog"]
	if !ok {
		t.Fatal("job changelog is not declared")
	}

	var script strings.Builder
	for _, step := range job.Steps {
		script.WriteString(step.Run)
	}
	run := script.String()

	for _, want := range []string{
		"release/*",
		"skip-changelog",
		`\.go`,
		"go.mod",
		".github/",
		"docs/",
		"examples/",
		"scripts/",
		"templates/",
		"GNUmakefile",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("changelog job does not mention %q; the path filter and exemptions must stay", want)
		}
	}
}

// TestProviderCIChangelogJobRestoresTheConcurrencyGroup keeps the group rustfs's
// own job carried: a superseded pull request run must not keep checking a
// fragment nobody will merge.
func TestProviderCIChangelogJobRestoresTheConcurrencyGroup(t *testing.T) {
	job, ok := readWorkflow(t, "provider-ci.yml").Jobs["changelog"]
	if !ok {
		t.Fatal("job changelog is not declared")
	}
	if got, want := job.Concurrency.Group, "${{ github.workflow }}-${{ github.event.pull_request.number }}"; got != want {
		t.Errorf("changelog concurrency group = %q, want %q", got, want)
	}
	if !job.Concurrency.CancelInProgress {
		t.Error("changelog concurrency does not cancel in progress")
	}
}

// TestSelfCITfkitGuardRejectsV0120 proves the guard checks behaviour, not
// wording: v0.12.0's help mentions the changelog and the last git tag, but its
// release check still exits 1 on a release pull request. The guard must reject
// it, naming the missing pending-release line.
func TestSelfCITfkitGuardRejectsV0120(t *testing.T) {
	guard := filepath.Join("..", "..", "scripts", "check-tfkit-commands.sh")
	wrapper := filepath.Join(t.TempDir(), "tfkit-v0120")
	script := "#!/bin/sh\nexec go run github.com/caberdo/terraform-provider-kit/cmd/tfkit@v0.12.0 \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write v0.12.0 wrapper: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "bash", guard, wrapper, "v0.12.0").CombinedOutput()
	if err == nil {
		t.Fatalf("the guard accepted v0.12.0, whose release check still fails a release PR (output = %q)", output)
	}
	if !strings.Contains(string(output), "pending release") {
		t.Errorf("output = %q, want it to name the missing 'pending release' line", output)
	}
}

func TestProviderReleaseDefaultsToTheTagDrivenFlow(t *testing.T) {
	workflow := readWorkflow(t, "provider-release.yml")

	input, ok := workflow.On.WorkflowCall.Inputs["version-file"]
	if !ok {
		t.Fatal("input version-file is not declared")
	}
	if input.Default != "" {
		t.Errorf("version-file default = %v, want empty", input.Default)
	}

	tag, ok := workflow.Jobs["tag"]
	if !ok {
		t.Fatal("job tag is not declared")
	}
	if !strings.Contains(tag.If, "inputs.version-file != ''") {
		t.Errorf("job tag if = %q, want it skipped when version-file is empty", tag.If)
	}

	release, ok := workflow.Jobs["goreleaser"]
	if !ok {
		t.Fatal("job goreleaser is not declared")
	}
	for _, want := range []string{
		"needs.tag.result == 'skipped'",
		"refs/tags/",
	} {
		if !strings.Contains(release.If, want) {
			t.Errorf("job goreleaser if = %q, want it to contain %q", release.If, want)
		}
	}
}
