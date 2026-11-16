package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// providerCI is the part of the reusable CI workflow the scaffold adoption
// depends on: the opt-in inputs, the job they gate and the shell the job runs.
type providerCI struct {
	On struct {
		WorkflowCall struct {
			Inputs map[string]struct {
				Type    string `yaml:"type"`
				Default any    `yaml:"default"`
			} `yaml:"inputs"`
		} `yaml:"workflow_call"`
	} `yaml:"on"`
	Env  map[string]string `yaml:"env"`
	Jobs map[string]struct {
		Name  string `yaml:"name"`
		If    string `yaml:"if"`
		Steps []struct {
			Name string            `yaml:"name"`
			Run  string            `yaml:"run"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
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

// TestProviderCIPinsItsOwnRelease pins the version the job installs. A reusable
// workflow ships inside its own tag, so the pin must name that tag: when tagging
// vX, TFKIT_VERSION is vX. v0.12.0 shipped the inverted release check while
// claiming the new default, which is why an older pin is not acceptable.
func TestProviderCIPinsItsOwnRelease(t *testing.T) {
	workflow := readProviderCI(t)
	if got, want := workflow.Env["TFKIT_VERSION"], "v0.13.0"; got != want {
		t.Errorf("TFKIT_VERSION = %q, want %q (the release this workflow ships in)", got, want)
	}
}

// scaffoldJobScript returns the shell the Scaffold job runs.
func scaffoldJobScript(t *testing.T) string {
	t.Helper()

	job := readProviderCI(t).Jobs["scaffold"]
	for _, step := range job.Steps {
		if step.Name == "Check the repository scaffold" {
			return step.Run
		}
	}
	t.Fatal("the Scaffold job has no 'Check the repository scaffold' step")
	return ""
}

// runScaffoldJob runs the job's shell with the workflow expressions resolved.
// tfkit is a fake that exits with tfkitExit, so the script's own status mapping
// is what the test observes.
func runScaffoldJob(t *testing.T, env map[string]string, tfkitExit int) (int, string) {
	t.Helper()

	script := strings.NewReplacer(
		"${{ inputs.provider-display }}", env["provider-display"],
		"${{ inputs.provider }}", "trino",
		"${{ inputs.module }}", "github.com/caberdo/terraform-provider-trino",
		"${{ inputs.packages }}", "./internal/...",
		"${{ inputs.versions-file }}", ".github/trino-versions.txt",
		"${{ github.repository_owner }}", "caberdo",
	).Replace(scaffoldJobScript(t))

	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	argsFile := filepath.Join(dir, "args")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$FAKE_ARGS\"\necho drift\nexit \"${FAKE_EXIT:-0}\"\n"
	if err := os.WriteFile(filepath.Join(bin, "tfkit"), []byte(fake), 0o755); err != nil {
		t.Fatalf("write fake tfkit: %v", err)
	}
	summary := filepath.Join(dir, "summary.md")

	command := exec.CommandContext(context.Background(), "bash", "-c", script)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TFKIT_VERSION=v0.13.0",
		"FAKE_ARGS="+argsFile,
		"FAKE_EXIT="+strconv.Itoa(tfkitExit),
		"GITHUB_STEP_SUMMARY="+summary,
		"SCAFFOLD_STRICT="+env["strict"],
		"SCAFFOLD_UPSTREAM="+env["upstream"],
		"SCAFFOLD_LICENSE="+env["license"],
	)
	output, err := command.CombinedOutput()
	code := 0
	if exitError, ok := err.(*exec.ExitError); ok {
		code = exitError.ExitCode()
	} else if err != nil {
		t.Fatalf("run the job script: %v", err)
	}

	summaryContent, _ := os.ReadFile(summary)
	return code, string(output) + string(summaryContent)
}

func TestScaffoldJobFailsWithoutProviderDisplay(t *testing.T) {
	code, output := runScaffoldJob(t, map[string]string{
		"provider-display": "",
		"strict":           "true",
		"upstream":         "trinodb/trino",
		"license":          "MIT",
	}, 0)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (output = %q)", code, output)
	}
	if !strings.Contains(output, "provider-display is required") {
		t.Errorf("output = %q, want the provider-display guard", output)
	}
}

func TestScaffoldJobFailsWithoutUpstream(t *testing.T) {
	code, output := runScaffoldJob(t, map[string]string{
		"provider-display": "Trino",
		"strict":           "true",
		"upstream":         "",
		"license":          "MIT",
	}, 0)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (output = %q)", code, output)
	}
	if !strings.Contains(output, "scaffold-upstream is required") {
		t.Errorf("output = %q, want the upstream guard", output)
	}
}

func TestScaffoldJobPassesDriftWhenReportOnly(t *testing.T) {
	code, output := runScaffoldJob(t, map[string]string{
		"provider-display": "Trino",
		"strict":           "false",
		"upstream":         "trinodb/trino",
		"license":          "MIT",
	}, 1)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 because scaffold-strict is false (output = %q)", code, output)
	}
	if !strings.Contains(output, "scaffold-strict is false") {
		t.Errorf("output = %q, want the report-only note in the summary", output)
	}
}

func TestScaffoldJobFailsDriftWhenStrict(t *testing.T) {
	code, _ := runScaffoldJob(t, map[string]string{
		"provider-display": "Trino",
		"strict":           "true",
		"upstream":         "trinodb/trino",
		"license":          "MIT",
	}, 1)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestScaffoldJobNeverPassesAConfigurationProblem(t *testing.T) {
	code, _ := runScaffoldJob(t, map[string]string{
		"provider-display": "Trino",
		"strict":           "false",
		"upstream":         "trinodb/trino",
		"license":          "MIT",
	}, 2)

	if code != 2 {
		t.Fatalf("exit = %d, want 2 even when scaffold-strict is false", code)
	}
}

// TestSelfCITfkitGuardRejectsV090 proves the guard would have caught the v0.9.0
// pin: that release answers --help for every command except scaffold, and the
// guard names it. The current checkout passes the same guard.
func TestSelfCITfkitGuardRejectsV090(t *testing.T) {
	guard := filepath.Join("..", "..", "scripts", "check-tfkit-commands.sh")
	bin := filepath.Join(t.TempDir(), "tfkit")

	build := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "./cmd/tfkit")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tfkit: %v (%s)", err, output)
	}
	if output, err := exec.CommandContext(context.Background(), "bash", guard, bin, "current").
		CombinedOutput(); err != nil {
		t.Fatalf("the current tfkit failed the guard: %v (%s)", err, output)
	}

	wrapper := filepath.Join(t.TempDir(), "tfkit-v090")
	script := "#!/bin/sh\nexec go run github.com/caberdo/terraform-provider-kit/cmd/tfkit@v0.9.0 \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write v0.9.0 wrapper: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "bash", guard, wrapper, "v0.9.0").CombinedOutput()
	if err == nil {
		t.Fatalf("the guard accepted v0.9.0 (output = %q)", output)
	}
	if !strings.Contains(string(output), "scaffold") {
		t.Errorf("output = %q, want it to name the missing scaffold command", output)
	}
}
