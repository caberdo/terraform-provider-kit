package cli_test

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestProviderAcceptanceDeclaresTheVersionInput pins the single input that
// makes a canary possible, and that an empty default keeps today's behaviour.
func TestProviderAcceptanceDeclaresTheVersionInput(t *testing.T) {
	workflow := readWorkflow(t, "provider-acceptance.yml")

	input, ok := workflow.On.WorkflowCall.Inputs["version"]
	if !ok {
		t.Fatal("input version is not declared")
	}
	if input.Type != "string" {
		t.Errorf("version type = %q, want string", input.Type)
	}
	if input.Default != "" {
		t.Errorf("version default = %v, want empty", input.Default)
	}
}

// TestProviderAcceptanceSingleVersionMatrix covers the canary path: the matrix
// is one leg and `latest` names it, so the coverage gate keeps its meaning.
func TestProviderAcceptanceSingleVersionMatrix(t *testing.T) {
	run := acceptanceMatrix(t, versionsFile(t, "480", "481", "482"), "push", "485")

	assertMatrix(t, run.outputs["list"], []string{"485"})
	if got := run.outputs["latest"]; got != "485" {
		t.Errorf("latest = %q, want %q", got, "485")
	}
}

// TestProviderAcceptanceMatrixFromTheFile covers the unchanged path: an empty
// version reads the file, all legs on push and floor plus newest on a pull
// request.
func TestProviderAcceptanceMatrixFromTheFile(t *testing.T) {
	file := versionsFile(t, "480", "481", "482")

	for _, testCase := range []struct {
		event string
		want  []string
	}{
		{event: "push", want: []string{"480", "481", "482"}},
		{event: "pull_request", want: []string{"480", "482"}},
	} {
		t.Run(testCase.event, func(t *testing.T) {
			run := acceptanceMatrix(t, file, testCase.event, "")

			assertMatrix(t, run.outputs["list"], testCase.want)
			if got, want := run.outputs["latest"], "482"; got != want {
				t.Errorf("latest = %q, want %q", got, want)
			}
		})
	}
}

func acceptanceMatrix(t *testing.T, file, event, version string) stepRun {
	t.Helper()

	return runStep(
		t,
		"provider-acceptance.yml",
		"Read the supported versions into a JSON array",
		map[string]string{
			"inputs.versions-file":    file,
			"inputs.provider-display": "Trino",
		},
		map[string]string{"EVENT_NAME": event, "VERSION": version},
		ghStub{},
	)
}

func assertMatrix(t *testing.T, list string, want []string) {
	t.Helper()

	var got []string
	if err := json.Unmarshal([]byte(list), &got); err != nil {
		t.Fatalf("matrix %q is not a JSON array of strings: %v", list, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("matrix = %v, want %v", got, want)
	}
}
