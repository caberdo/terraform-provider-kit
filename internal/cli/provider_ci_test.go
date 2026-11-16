package cli_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// providerCI is the part of the reusable CI workflow the scaffold adoption
// depends on: the two opt-in inputs and the job they gate.
type providerCI struct {
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

// readProviderCI parses the workflow from the kit checkout.
func readProviderCI(t *testing.T) providerCI {
	t.Helper()

	content, err := os.ReadFile("../../.github/workflows/provider-ci.yml")
	if err != nil {
		t.Fatalf("read provider-ci.yml: %v", err)
	}
	var workflow providerCI
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatalf("parse provider-ci.yml: %v", err)
	}
	return workflow
}

func TestProviderCIDeclaresTheScaffoldInputs(t *testing.T) {
	workflow := readProviderCI(t)

	for name, want := range map[string]any{
		"scaffold":        false,
		"scaffold-strict": true,
	} {
		input, ok := workflow.On.WorkflowCall.Inputs[name]
		if !ok {
			t.Errorf("input %s is not declared", name)
			continue
		}
		if input.Type != "boolean" {
			t.Errorf("input %s type = %q, want boolean", name, input.Type)
		}
		if input.Default != want {
			t.Errorf("input %s default = %v, want %v", name, input.Default, want)
		}
	}
}

func TestProviderCIScaffoldJobIsSkippedByDefault(t *testing.T) {
	workflow := readProviderCI(t)

	job, ok := workflow.Jobs["scaffold"]
	if !ok {
		t.Fatal("provider-ci.yml has no scaffold job")
	}
	if job.Name != "Scaffold" {
		t.Errorf("scaffold job name = %q, want Scaffold", job.Name)
	}
	if !strings.Contains(job.If, "inputs.scaffold") {
		t.Errorf("scaffold job if = %q, want it gated on inputs.scaffold", job.If)
	}
}
