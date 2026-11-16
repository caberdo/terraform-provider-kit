package examples

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// invocation records one command a fake runner was asked to run.
type invocation struct {
	dir  string
	name string
	args []string
}

// fakeExec stands in for terraform and the Go toolchain. It records every
// call and fails the ones fail returns an error for, which is how the skip,
// fmt and per-module tests drive the three exit paths without either tool.
type fakeExec struct {
	lookPathErr error
	fail        func(invocation) error
	invocations []invocation
	env         map[string]string
}

func (f *fakeExec) LookPath(name string) (string, error) {
	if f.lookPathErr != nil {
		return "", f.lookPathErr
	}
	return filepath.Join("/usr/local/bin", name), nil
}

func (f *fakeExec) Run(dir, name string, args ...string) error {
	inv := invocation{dir: dir, name: name, args: args}
	f.invocations = append(f.invocations, inv)
	if f.fail != nil {
		return f.fail(inv)
	}
	return nil
}

// SetEnv records the environment every Run inherits, so a test can assert that
// terraform is pointed at the dev_overrides config.
func (f *fakeExec) SetEnv(key, value string) {
	if f.env == nil {
		f.env = make(map[string]string)
	}
	f.env[key] = value
}

// stubVersion pins git describe so the plugin path the tests assert is stable
// in any checkout, tagged or not.
func stubVersion(t *testing.T, version string) {
	t.Helper()

	original := describeVersion
	describeVersion = func(string) string { return version }
	t.Cleanup(func() { describeVersion = original })
}

// exampleModule creates <root>/<rel>/main.tf so moduleDirs has a module to
// find. The file contents are never read by the fake runner.
func exampleModule(t *testing.T, root, rel string) string {
	t.Helper()

	dir := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", rel, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# module\n"), 0o600); err != nil {
		t.Fatalf("write %s/main.tf: %v", rel, err)
	}
	return dir
}

// wantInvocation compares one recorded command with the expected one.
func wantInvocation(t *testing.T, got invocation, dir, name string, args ...string) {
	t.Helper()

	if got.dir != dir {
		t.Errorf("dir = %q, want %q", got.dir, dir)
	}
	if got.name != name {
		t.Errorf("command = %q, want %q", got.name, name)
	}
	if !slices.Equal(got.args, args) {
		t.Errorf("args = %v, want %v", got.args, args)
	}
}

func TestValidateSkipsWhenTerraformMissing(t *testing.T) {
	stubVersion(t, "v1.2.3")
	runner := &fakeExec{lookPathErr: errors.New(`exec: "terraform": executable file not found in $PATH`)}
	var stdout, stderr bytes.Buffer

	err := Validate(t.TempDir(), Options{
		Provider: "widget",
		Runner:   runner,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})

	if err != nil {
		t.Fatalf("Validate() = %v, want nil (a missing terraform is a skip)", err)
	}
	if len(runner.invocations) != 0 {
		t.Errorf("ran %d commands, want none", len(runner.invocations))
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if want := "terraform is not installed; skipping example validation\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestValidateFailsWhenTerraformMissingAndRequired(t *testing.T) {
	stubVersion(t, "v1.2.3")
	runner := &fakeExec{lookPathErr: errors.New(`exec: "terraform": executable file not found in $PATH`)}
	var stdout, stderr bytes.Buffer

	err := Validate(t.TempDir(), Options{
		Provider:         "widget",
		RequireTerraform: true,
		Runner:           runner,
		Stdout:           &stdout,
		Stderr:           &stderr,
	})

	if err == nil {
		t.Fatal("Validate() = nil, want an error when terraform is required")
	}
	if !strings.Contains(err.Error(), "terraform is required") {
		t.Errorf("error = %q, want it to name the requirement", err)
	}
	if errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want a validation failure (exit 1), not a usage error", err)
	}
	if len(runner.invocations) != 0 {
		t.Errorf("ran %d commands, want none", len(runner.invocations))
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty: the error carries the message", got)
	}
}

func TestValidateSkipsWhenExamplesDirectoryIsAbsent(t *testing.T) {
	stubVersion(t, "v1.2.3")
	runner := &fakeExec{}
	var stdout, stderr bytes.Buffer

	err := Validate(t.TempDir(), Options{
		Provider: "widget",
		Runner:   runner,
		Stdout:   &stdout,
		Stderr:   &stderr,
	})

	if err != nil {
		t.Fatalf("Validate() = %v, want nil (no examples yet is a skip)", err)
	}
	if len(runner.invocations) != 0 {
		t.Errorf("ran %d commands, want none: fmt must not run without examples", len(runner.invocations))
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if want := "no examples/ directory yet; skipping example validation\n"; stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestValidateRunsFmtAndValidateAgainstDevOverrides(t *testing.T) {
	const version = "v1.2.3"
	stubVersion(t, version)

	root := t.TempDir()
	complete := exampleModule(t, root, "examples/complete")
	widget := exampleModule(t, root, "examples/resources/widget")
	// A directory without a .tf file is not a module and must be skipped.
	if err := os.MkdirAll(filepath.Join(root, "examples", "data-sources"), 0o755); err != nil {
		t.Fatalf("create examples/data-sources: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "examples", "data-sources", "README.md"), []byte("docs\n"), 0o600); err != nil {
		t.Fatalf("write examples/data-sources/README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "examples", "README.md"), []byte("docs\n"), 0o600); err != nil {
		t.Fatalf("write examples/README.md: %v", err)
	}

	pluginDir := t.TempDir()
	runner := &fakeExec{}
	var stdout, stderr bytes.Buffer

	err := Validate(root, Options{
		Provider:  "widget",
		PluginDir: pluginDir,
		Runner:    runner,
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	binDir := filepath.Join(pluginDir, "registry.terraform.io", "caberdo", "widget",
		version, runtime.GOOS+"_"+runtime.GOARCH)
	binary := filepath.Join(binDir, "terraform-provider-widget")

	if info, err := os.Stat(binDir); err != nil {
		t.Errorf("stat plugin directory: %v", err)
	} else if !info.IsDir() {
		t.Errorf("%s is not a directory", binDir)
	}

	if len(runner.invocations) != 4 {
		t.Fatalf("ran %d commands, want 4: %v", len(runner.invocations), runner.invocations)
	}
	wantInvocation(t, runner.invocations[0], root, "terraform", "fmt", "-check", "-recursive", "examples")
	wantInvocation(t, runner.invocations[1], root, "go",
		"build", "-o", binary, "-ldflags", "-X main.version="+version, ".")
	wantInvocation(t, runner.invocations[2], complete, "terraform", "validate", "-no-color")
	wantInvocation(t, runner.invocations[3], widget, "terraform", "validate", "-no-color")

	wantConfig := fmt.Sprintf(`provider_installation {
  dev_overrides {
    "caberdo/widget" = %q
    "hashicorp/widget" = %q
  }
  direct {}
}
`, binDir, binDir)
	config, err := os.ReadFile(filepath.Join(pluginDir, "dev.tfrc"))
	if err != nil {
		t.Fatalf("read dev.tfrc: %v", err)
	}
	if string(config) != wantConfig {
		t.Errorf("dev.tfrc =\n%s\nwant\n%s", config, wantConfig)
	}

	wantStdout := "building provider " + version + "...\n" +
		"==> examples/complete\n" +
		"==> examples/resources/widget\n" +
		"all examples validated\n"
	if got := stdout.String(); got != wantStdout {
		t.Errorf("stdout = %q, want %q", got, wantStdout)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}

	// The dev_overrides only take effect when terraform is told where the
	// config is, so both entries must be exported to every command.
	if want := filepath.Join(pluginDir, "dev.tfrc"); runner.env["TF_CLI_CONFIG_FILE"] != want {
		t.Errorf("TF_CLI_CONFIG_FILE = %q, want %q", runner.env["TF_CLI_CONFIG_FILE"], want)
	}
	if got := runner.env["TF_IN_AUTOMATION"]; got != "1" {
		t.Errorf("TF_IN_AUTOMATION = %q, want 1", got)
	}
}

func TestValidateDefaultsPluginDirToTempDirectory(t *testing.T) {
	stubVersion(t, "v9.9.9")
	t.Setenv("TMPDIR", t.TempDir())

	root := t.TempDir()
	exampleModule(t, root, "examples/complete")
	runner := &fakeExec{}

	err := Validate(root, Options{
		Provider: "gadget",
		Runner:   runner,
		Stdout:   &bytes.Buffer{},
		Stderr:   &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	wantBinary := filepath.Join(os.TempDir(), "gadget-provider-dev", "registry.terraform.io",
		"caberdo", "gadget", "v9.9.9", runtime.GOOS+"_"+runtime.GOARCH, "terraform-provider-gadget")
	build := runner.invocations[1]
	if got := build.args[2]; got != wantBinary {
		t.Errorf("build output = %q, want %q", got, wantBinary)
	}
}

func TestValidateFailsWhenFmtFailsBeforeBuilding(t *testing.T) {
	stubVersion(t, "v1.2.3")

	root := t.TempDir()
	exampleModule(t, root, "examples/complete")
	runner := &fakeExec{fail: func(inv invocation) error {
		if inv.name == "terraform" && inv.args[0] == "fmt" {
			return errors.New("exit status 1")
		}
		return nil
	}}
	var stdout, stderr bytes.Buffer

	err := Validate(root, Options{
		Provider:  "widget",
		PluginDir: t.TempDir(),
		Runner:    runner,
		Stdout:    &stdout,
		Stderr:    &stderr,
	})

	if err == nil {
		t.Fatal("Validate() = nil, want an error when terraform fmt fails")
	}
	if !strings.Contains(err.Error(), "terraform fmt") {
		t.Errorf("error = %q, want it to name terraform fmt", err)
	}
	if errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want a validation failure (exit 1), not a usage error", err)
	}
	if len(runner.invocations) != 1 {
		t.Errorf("ran %d commands, want only fmt: %v", len(runner.invocations), runner.invocations)
	}
}

func TestValidateStopsAtTheFirstFailingModule(t *testing.T) {
	stubVersion(t, "v1.2.3")

	root := t.TempDir()
	exampleModule(t, root, "examples/complete")
	firstFailure := exampleModule(t, root, "examples/resources/alpha")
	exampleModule(t, root, "examples/resources/zeta")
	runner := &fakeExec{fail: func(inv invocation) error {
		if inv.name == "terraform" && inv.args[0] == "validate" && inv.dir == firstFailure {
			return errors.New("exit status 1")
		}
		return nil
	}}

	err := Validate(root, Options{
		Provider:  "widget",
		PluginDir: t.TempDir(),
		Runner:    runner,
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
	})

	if err == nil {
		t.Fatal("Validate() = nil, want an error when a module fails validation")
	}
	if !strings.Contains(err.Error(), "examples/resources/alpha") {
		t.Errorf("error = %q, want it to name the failing module", err)
	}
	// fmt, go build, validate complete, validate alpha: zeta must not run.
	if len(runner.invocations) != 4 {
		t.Errorf("ran %d commands, want 4: %v", len(runner.invocations), runner.invocations)
	}
}

func TestValidateRequiresProvider(t *testing.T) {
	runner := &fakeExec{}

	err := Validate(t.TempDir(), Options{
		Runner: runner,
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	})

	if err == nil {
		t.Fatal("Validate() = nil, want an error when the provider is empty")
	}
	if !errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want ErrUsage so the CLI exits 2", err)
	}
	if len(runner.invocations) != 0 {
		t.Errorf("ran %d commands, want none", len(runner.invocations))
	}
}

func TestValidateRejectsProviderContainingAPathSeparator(t *testing.T) {
	runner := &fakeExec{}

	err := Validate(t.TempDir(), Options{
		Provider: "../widget",
		Runner:   runner,
		Stdout:   &bytes.Buffer{},
		Stderr:   &bytes.Buffer{},
	})

	if !errors.Is(err, ErrUsage) {
		t.Errorf("error = %v, want ErrUsage for a provider that is not a single path element", err)
	}
}

func TestDevConfigIsStableAndPutsCaberdoFirst(t *testing.T) {
	// Terraform echoes the dev_overrides entries in the order it reads them and
	// the shell script always writes caberdo first. A single render cannot
	// detect map iteration order, so render repeatedly: every result must be
	// byte-identical and every one must order caberdo before hashicorp.
	const provider = "widget"
	binDir := "/plugins/registry.terraform.io/caberdo/widget/v1.2.3/darwin_arm64"

	first := devConfig(provider, binDir)
	for range 20 {
		if got := devConfig(provider, binDir); got != first {
			t.Fatalf("devConfig() =\n%s\nwant the same bytes on every render:\n%s", got, first)
		}
	}

	caberdo := strings.Index(first, `"caberdo/`+provider+`"`)
	hashicorp := strings.Index(first, `"hashicorp/`+provider+`"`)
	if caberdo < 0 || hashicorp < 0 {
		t.Fatalf("devConfig() =\n%s\nwant both dev_overrides entries", first)
	}
	if caberdo > hashicorp {
		t.Errorf("devConfig() =\n%s\nwant caberdo/%s before hashicorp/%s", first, provider, provider)
	}
}

func TestDescribeVersionFallsBackToDevWithoutATag(t *testing.T) {
	// t.TempDir() is outside any repository, so `git describe` fails and the
	// script's `|| echo dev` fallback applies.
	if got := describeVersion(t.TempDir()); got != "dev" {
		t.Errorf("describeVersion() = %q, want %q", got, "dev")
	}
}

func TestOSExecEnvironmentOverridesAmbientValues(t *testing.T) {
	// Terraform's CLI config is exported the way the shell script exported it:
	// a value the runner sets must shadow the ambient one, not be appended as a
	// second entry the child could resolve the other way round.
	t.Setenv("TFKIT_EXAMPLE_TEST", "ambient")
	runner := &OSExec{}
	runner.SetEnv("TFKIT_EXAMPLE_TEST", "override")
	runner.SetEnv("TFKIT_EXAMPLE_ONLY", "value")

	env := runner.environ()

	values := map[string][]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		values[key] = append(values[key], value)
	}
	if got := values["TFKIT_EXAMPLE_TEST"]; !slices.Equal(got, []string{"override"}) {
		t.Errorf("TFKIT_EXAMPLE_TEST = %v, want exactly [override]", got)
	}
	if got := values["TFKIT_EXAMPLE_ONLY"]; !slices.Equal(got, []string{"value"}) {
		t.Errorf("TFKIT_EXAMPLE_ONLY = %v, want exactly [value]", got)
	}
	if got := values["PATH"]; len(got) != 1 {
		t.Errorf("PATH entries = %v, want the ambient PATH carried through once", got)
	}
}

func TestOSExecZeroValueInheritsTheProcessEnvironment(t *testing.T) {
	// A nil environment is os/exec's "inherit"; returning an empty slice would
	// hand terraform a child with no PATH.
	if got := (&OSExec{}).environ(); got != nil {
		t.Errorf("environ() = %v, want nil so the child inherits the process environment", got)
	}
}
