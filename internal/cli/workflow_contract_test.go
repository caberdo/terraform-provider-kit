package cli_test

import (
	"os"
	"strings"
	"testing"

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
		Name string `yaml:"name"`
		If   string `yaml:"if"`
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
