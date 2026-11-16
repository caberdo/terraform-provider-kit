package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These helpers execute the shell the reusable workflows actually ship, with a
// stub `gh` on PATH, instead of a copy kept in a test fixture. The steps are a
// handful of jq/sort expressions whose failure mode is silent drift, and the
// comparison cannot live in a kit-side script: a reusable workflow checks out
// the caller, so only the workflow file itself is on disk when it runs.

// ghStub describes what the fake `gh` prints. `releases` is the JSON body of
// `gh api .../releases`; `issue` is the open issue number `gh issue list`
// reports, empty when no issue is open.
type ghStub struct {
	releases string
	issue    string
}

// stepRun is the observable result of one workflow step: the `$GITHUB_OUTPUT`
// key/value pairs, every `gh` invocation (in order) and the combined log.
type stepRun struct {
	outputs map[string]string
	calls   []string
	log     string
}

// runStep extracts the named step's `run` script from a workflow, substitutes
// the `${{ ... }}` expressions with the fixture's values, and runs it with bash.
func runStep(
	t *testing.T,
	workflow, step string,
	inputs, env map[string]string,
	stub ghStub,
) stepRun {
	t.Helper()

	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required to run a workflow step", tool)
		}
	}

	script := workflowStep(t, workflow, step)
	for expression, value := range inputs {
		script = strings.ReplaceAll(script, "${{ "+expression+" }}", value)
	}
	if strings.Contains(script, "${{ ") {
		t.Fatalf("step %q still has an unsubstituted expression:\n%s", step, script)
	}

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create stub bin directory: %v", err)
	}

	apiPath := filepath.Join(dir, "releases.json")
	if err := os.WriteFile(apiPath, []byte(stub.releases), 0o644); err != nil {
		t.Fatalf("write releases fixture: %v", err)
	}
	logPath := filepath.Join(dir, "gh.log")
	outputPath := filepath.Join(dir, "github-output")
	for _, path := range []string{logPath, outputPath} {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
	}

	// The stub answers the two read calls with fixtures and accepts every write
	// call, recording it so a test can assert what the step did.
	const ghStubScript = `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$GH_STUB_LOG"
if [[ "$1" == "api" ]]; then
  cat "$GH_STUB_API"
  exit 0
fi
if [[ "$1 $2" == "issue list" ]]; then
  printf '%s' "${GH_STUB_ISSUE:-}"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(ghStubScript), 0o755); err != nil {
		t.Fatalf("write gh stub: %v", err)
	}

	cmdEnv := withoutEnv(os.Environ(), "PATH", "GITHUB_OUTPUT", "GH_STUB_LOG", "GH_STUB_API", "GH_STUB_ISSUE")
	cmdEnv = append(cmdEnv,
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"GITHUB_OUTPUT="+outputPath,
		"GH_STUB_LOG="+logPath,
		"GH_STUB_API="+apiPath,
		"GH_STUB_ISSUE="+stub.issue,
	)
	for key, value := range env {
		cmdEnv = append(cmdEnv, key+"="+value)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = cmdEnv
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("step %q failed: %v\n%s", step, err, combined)
	}

	outputData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read step outputs: %v", err)
	}
	outputs := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(outputData)), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed step output line %q", line)
		}
		outputs[key] = value
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(calls) == 1 && calls[0] == "" {
		calls = nil
	}

	return stepRun{outputs: outputs, calls: calls, log: string(combined)}
}

// workflowStep returns the `run` script of the step with the given name.
func workflowStep(t *testing.T, workflow, step string) string {
	t.Helper()

	for _, job := range readWorkflow(t, workflow).Jobs {
		for _, candidate := range job.Steps {
			if candidate.Name != step {
				continue
			}
			if strings.TrimSpace(candidate.Run) == "" {
				t.Fatalf("step %q has no run script", step)
			}
			return candidate.Run
		}
	}
	t.Fatalf("workflow %s has no step named %q", workflow, step)
	return ""
}

// withoutEnv drops the named variables so a stale value in the test process
// cannot leak into the step.
func withoutEnv(environ []string, names ...string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, candidate := range names {
			if name == candidate {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, entry)
		}
	}
	return kept
}

// versionsFile writes a versions file with a comment, which the workflows must
// strip, and returns its path.
func versionsFile(t *testing.T, versions ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "trino-versions.txt")
	content := "# supported floor and window\n" + strings.Join(versions, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write versions fixture: %v", err)
	}
	return path
}
