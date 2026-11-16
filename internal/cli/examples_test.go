package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/examples"
)

// examplesCall records one command the fake runner was asked to run.
type examplesCall struct {
	dir  string
	name string
	args []string
}

// fakeExamplesExec stands in for terraform and the Go toolchain so no CLI test
// needs either, or a built provider binary.
type fakeExamplesExec struct {
	lookPathErr error
	fail        func(examplesCall) error
	calls       []examplesCall
	env         map[string]string
}

func (f *fakeExamplesExec) LookPath(name string) (string, error) {
	if f.lookPathErr != nil {
		return "", f.lookPathErr
	}
	return filepath.Join("/usr/local/bin", name), nil
}

func (f *fakeExamplesExec) Run(dir, name string, args ...string) error {
	call := examplesCall{dir: dir, name: name, args: args}
	f.calls = append(f.calls, call)
	if f.fail != nil {
		return f.fail(call)
	}
	return nil
}

func (f *fakeExamplesExec) SetEnv(key, value string) {
	if f.env == nil {
		f.env = make(map[string]string)
	}
	f.env[key] = value
}

// stubExamplesRunner replaces the process runner for one test.
func stubExamplesRunner(t *testing.T, runner examples.Exec) {
	t.Helper()

	original := newExamplesRunner
	newExamplesRunner = func(io.Writer, io.Writer) examples.Exec { return runner }
	t.Cleanup(func() { newExamplesRunner = original })
}

// examplesRepo creates a repository root with one module under examples/ and
// returns the root.
func examplesRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	module := filepath.Join(root, "examples", "complete")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatalf("create examples/complete: %v", err)
	}
	if err := os.WriteFile(filepath.Join(module, "main.tf"), []byte("# module\n"), 0o600); err != nil {
		t.Fatalf("write examples/complete/main.tf: %v", err)
	}
	return root
}

func TestRunExamplesValidateSkipsWhenTerraformIsMissing(t *testing.T) {
	runner := &fakeExamplesExec{lookPathErr: errors.New("terraform not found")}
	stubExamplesRunner(t, runner)
	t.Chdir(examplesRepo(t))

	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate", "--provider", "widget")

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "terraform is not installed; skipping") {
		t.Errorf("stderr = %q, want the skip notice", stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if len(runner.calls) != 0 {
		t.Errorf("ran %d commands, want none", len(runner.calls))
	}
}

func TestRunExamplesValidateFailsWhenTerraformIsRequired(t *testing.T) {
	runner := &fakeExamplesExec{lookPathErr: errors.New("terraform not found")}
	stubExamplesRunner(t, runner)
	t.Chdir(examplesRepo(t))

	t.Setenv("REQUIRE_TERRAFORM", "1")
	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate", "--provider", "widget")

	// Exit 1, not 0: REQUIRE_TERRAFORM=1 turns the skip into a failure, as the
	// shell script did, and that is not a usage error.
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "terraform is required") {
		t.Errorf("stderr = %q, want it to name the requirement", stderr.String())
	}
	if len(runner.calls) != 0 {
		t.Errorf("ran %d commands, want none", len(runner.calls))
	}
}

func TestRunExamplesValidateRequiresProvider(t *testing.T) {
	runner := &fakeExamplesExec{}
	stubExamplesRunner(t, runner)
	t.Chdir(examplesRepo(t))

	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate")

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--provider") {
		t.Errorf("stderr = %q, want it to name --provider", stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: tfkit examples validate") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
	if len(runner.calls) != 0 {
		t.Errorf("ran %d commands, want none", len(runner.calls))
	}
}

func TestRunExamplesValidateRejectsAPathInTheProvider(t *testing.T) {
	runner := &fakeExamplesExec{}
	stubExamplesRunner(t, runner)
	t.Chdir(examplesRepo(t))

	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate", "--provider", "../widget")

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if len(runner.calls) != 0 {
		t.Errorf("ran %d commands, want none", len(runner.calls))
	}
}

func TestRunExamplesValidateBuildsAndValidates(t *testing.T) {
	root := examplesRepo(t)
	pluginDir := t.TempDir()
	runner := &fakeExamplesExec{}
	stubExamplesRunner(t, runner)
	t.Chdir(root)

	// The plugin directory is configured through the environment so CI can run
	// providers in parallel, which is what R13 keeps from the shell scripts.
	t.Setenv("TFKIT_PLUGIN_DIR", pluginDir)
	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate", "--provider", "widget")

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if len(runner.calls) != 3 {
		t.Fatalf("ran %d commands, want fmt, build and validate: %v", len(runner.calls), runner.calls)
	}

	build := runner.calls[1]
	if build.name != "go" {
		t.Fatalf("second command = %q, want go", build.name)
	}
	// args: build -o <binary> -ldflags <value> .
	binary := build.args[2]
	if !strings.HasPrefix(binary, pluginDir+string(filepath.Separator)) {
		t.Errorf("build output = %q, want it under TFKIT_PLUGIN_DIR %q", binary, pluginDir)
	}
	if !slices.Contains(build.args, "-X main.version="+exampleVersion(binary)) {
		t.Errorf("build args = %v, want the version ldflag", build.args)
	}

	if got := runner.env["TF_CLI_CONFIG_FILE"]; got != filepath.Join(pluginDir, "dev.tfrc") {
		t.Errorf("TF_CLI_CONFIG_FILE = %q, want %q", got, filepath.Join(pluginDir, "dev.tfrc"))
	}
	if !strings.Contains(stdout.String(), "==> examples/complete\n") {
		t.Errorf("stdout = %q, want the module heading", stdout.String())
	}
	if !strings.Contains(stdout.String(), "all examples validated\n") {
		t.Errorf("stdout = %q, want the completion line", stdout.String())
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

// exampleVersion extracts the version segment from the plugin layout:
// <plugin-dir>/registry.terraform.io/caberdo/<provider>/<version>/<os>_<arch>/...
func exampleVersion(binary string) string {
	parts := strings.Split(filepath.ToSlash(binary), "/")
	return parts[len(parts)-3]
}

func TestRunExamplesValidateExitsOneWhenAModuleFails(t *testing.T) {
	root := examplesRepo(t)
	runner := &fakeExamplesExec{fail: func(call examplesCall) error {
		if call.name == "terraform" && len(call.args) > 0 && call.args[0] == "validate" {
			return errors.New("exit status 1")
		}
		return nil
	}}
	stubExamplesRunner(t, runner)
	t.Chdir(root)

	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate", "--provider", "widget")

	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "examples/complete") {
		t.Errorf("stderr = %q, want it to name the failing module", stderr.String())
	}
	if strings.Contains(stdout.String(), "all examples validated") {
		t.Errorf("stdout = %q, want no completion line", stdout.String())
	}
}

func TestRunExamplesValidateRejectsPositionalArguments(t *testing.T) {
	stubExamplesRunner(t, &fakeExamplesExec{})
	t.Chdir(examplesRepo(t))

	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate", "--provider", "widget", "extra")

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: tfkit examples validate") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestRunExamplesValidateRejectsAnUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate", "--terraform")

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "-terraform") {
		t.Errorf("stderr = %q, want it to name the unknown flag", stderr.String())
	}
}

func TestRunExamplesValidateHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "validate", "--help")

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, flag := range []string{"--provider", "--plugin-dir", "--require-terraform"} {
		if !strings.Contains(stderr.String(), flag) {
			t.Errorf("stderr = %q, want it to mention %s", stderr.String(), flag)
		}
	}
}

func TestRunExamplesRequiresASubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples")

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "examples requires a subcommand") {
		t.Errorf("stderr = %q, want it to name the missing subcommand", stderr.String())
	}
}

func TestRunExamplesRejectsAnUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cliRun(t, &stdout, &stderr, "examples", "lint")

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `unknown examples command "lint"`) {
		t.Errorf("stderr = %q, want it to name the unknown subcommand", stderr.String())
	}
}

// cliRun invokes Run with the shared test version.
func cliRun(t *testing.T, stdout, stderr io.Writer, args ...string) int {
	t.Helper()

	return Run(args, testVersion, stdout, stderr)
}
