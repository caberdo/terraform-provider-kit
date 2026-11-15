// Package examples validates a provider's Terraform examples against the
// provider built from the current working tree.
//
// It replaces scripts/validate-examples.sh: the provider is built into a
// throwaway plugin directory, a Terraform CLI config points dev_overrides at
// that binary, and every module under examples/ is validated in place. Because
// dev_overrides supplies the provider, `terraform validate` needs neither
// `terraform init` nor access to the Terraform Registry.
//
// All process and filesystem work lives here; the CLI only parses flags and maps
// the error to an exit code.
package examples

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// ErrUsage marks a usage or configuration mistake: a missing provider name, a
// provider name that is not a single path element, or a plugin directory that
// cannot be prepared. The CLI maps it to exit code 2, as opposed to 1 for a
// failed validation.
var ErrUsage = errors.New("invalid options")

// Exec is the slice of the process world validation needs. It exists so the
// command can be tested without Terraform or a built provider binary.
type Exec interface {
	// LookPath reports the path of name, or an error when it is unavailable.
	LookPath(name string) (string, error)
	// Run runs name with args in dir and waits for it to finish.
	Run(dir, name string, args ...string) error
	// SetEnv adds key=value to the environment of every subsequent Run.
	SetEnv(key, value string)
}

// OSExec is the Exec that runs real processes. The zero value inherits the
// process's own streams and environment.
type OSExec struct {
	// Stdout and Stderr receive the child's output. A nil writer means the
	// process's corresponding stream.
	Stdout io.Writer
	Stderr io.Writer

	env map[string]string
}

// OSExec is the production Exec.
var _ Exec = (*OSExec)(nil)

// LookPath reports whether name is on PATH.
func (e *OSExec) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// SetEnv adds an environment entry. A later call for the same key replaces the
// earlier value and shadows any ambient value, the way an exported shell
// variable does.
func (e *OSExec) SetEnv(key, value string) {
	if e.env == nil {
		e.env = make(map[string]string)
	}
	e.env[key] = value
}

// Run runs the command to completion, inheriting the process environment plus
// whatever SetEnv added.
func (e *OSExec) Run(dir, name string, args ...string) error {
	stdout := e.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := e.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	cmd := exec.CommandContext(context.Background(), name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = e.environ()
	return cmd.Run()
}

// OSExec is the production Exec.
var _ Exec = (*OSExec)(nil)

// environ returns the environment for a child process, or nil to inherit the
// current one when nothing was overridden.
func (e *OSExec) environ() []string {
	if len(e.env) == 0 {
		return nil
	}

	env := make([]string, 0, len(os.Environ())+len(e.env))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := e.env[key]; overridden {
			continue
		}
		env = append(env, entry)
	}
	for key, value := range e.env {
		env = append(env, key+"="+value)
	}
	return env
}

// Options configures [Validate]. Provider is required. Runner defaults to
// [OSExec] and Stdout/Stderr default to io.Discard.
type Options struct {
	// Provider is the provider name without the caberdo/ prefix, such as
	// "trino". It names the build output and both dev_overrides keys.
	Provider string
	// PluginDir is where the provider and its dev.tfrc are built. Empty means
	// "<temp dir>/<provider>-provider-dev", as the shell script defaulted.
	PluginDir string
	// RequireTerraform turns a missing terraform into a failure instead of a
	// skip. The CLI initialises it from REQUIRE_TERRAFORM.
	RequireTerraform bool
	// Runner executes commands. Tests inject a fake.
	Runner Exec
	// Stdout receives progress and the "all examples validated" line.
	Stdout io.Writer
	// Stderr receives the skip notices. Command failures are returned as
	// errors and printed by the caller.
	Stderr io.Writer
}

// describeVersion returns the version of the provider built from dir. It is a
// variable so tests can pin the plugin path without a tagged checkout.
var describeVersion = func(dir string) string {
	cmd := exec.CommandContext(context.Background(), "git", "describe", "--tags", "--always", "--dirty")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		return "dev"
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return "dev"
	}
	return version
}

// Validate builds the provider in dir and validates every Terraform module
// under dir/examples.
//
// A missing terraform is a skip unless opts.RequireTerraform is set; a missing
// examples/ directory is always a skip. Both write a notice to opts.Stderr and
// return nil. A failing fmt, build or validate is returned as an error and is
// the caller's cue to exit 1.
func Validate(dir string, opts Options) error {
	if err := validateProvider(opts.Provider); err != nil {
		return err
	}

	stdout := opts.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	runner := opts.Runner
	if runner == nil {
		runner = &OSExec{Stdout: stdout, Stderr: stderr}
	}

	if _, err := runner.LookPath("terraform"); err != nil {
		if opts.RequireTerraform {
			return errors.New("terraform is required but not installed (--require-terraform)")
		}
		_, _ = fmt.Fprintln(stderr, "terraform is not installed; skipping example validation")
		return nil
	}

	examplesDir := filepath.Join(dir, "examples")
	info, err := os.Stat(examplesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			_, _ = fmt.Fprintln(stderr, "no examples/ directory yet; skipping example validation")
			return nil
		}
		return fmt.Errorf("%w: %s: %w", ErrUsage, examplesDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUsage, examplesDir)
	}

	// Formatting is checked before anything is built, as the shell script did:
	// a badly formatted example needs no provider to report.
	if err := runner.Run(dir, "terraform", "fmt", "-check", "-recursive", "examples"); err != nil {
		return fmt.Errorf("terraform fmt: %w", err)
	}

	version := describeVersion(dir)
	pluginDir := opts.PluginDir
	if pluginDir == "" {
		pluginDir = filepath.Join(os.TempDir(), opts.Provider+"-provider-dev")
	}
	binary, err := buildProvider(runner, stdout, dir, opts.Provider, pluginDir, version)
	if err != nil {
		return err
	}

	configPath, err := writeDevConfig(pluginDir, opts.Provider, filepath.Dir(binary))
	if err != nil {
		return err
	}
	runner.SetEnv("TF_CLI_CONFIG_FILE", configPath)
	runner.SetEnv("TF_IN_AUTOMATION", "1")

	modules, err := moduleDirs(examplesDir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	for _, module := range modules {
		rel, err := filepath.Rel(dir, module)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUsage, module, err)
		}
		name := filepath.ToSlash(rel)
		_, _ = fmt.Fprintf(stdout, "==> %s\n", name)
		if err := runner.Run(module, "terraform", "validate", "-no-color"); err != nil {
			return fmt.Errorf("%s: terraform validate: %w", name, err)
		}
	}

	_, _ = fmt.Fprintln(stdout, "all examples validated")
	return nil
}

// buildProvider builds the provider from dir into the plugin layout Terraform
// expects and returns the binary path.
func buildProvider(runner Exec, stdout io.Writer, dir, provider, pluginDir, version string) (string, error) {
	binDir := filepath.Join(pluginDir, "registry.terraform.io", "caberdo", provider,
		version, runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("%w: create plugin directory %s: %w", ErrUsage, binDir, err)
	}

	binary := filepath.Join(binDir, "terraform-provider-"+provider)
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	_, _ = fmt.Fprintf(stdout, "building provider %s...\n", version)
	if err := runner.Run(dir, "go", "build", "-o", binary,
		"-ldflags", "-X main.version="+version, "."); err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}
	return binary, nil
}

// writeDevConfig writes the CLI config that points both the canonical provider
// source and Terraform's inferred default at the built binary, then returns the
// path. Direct installation stays enabled for every other provider.
func writeDevConfig(pluginDir, provider, binDir string) (string, error) {
	config := fmt.Sprintf(`provider_installation {
  dev_overrides {
    "caberdo/%[1]s" = %[2]q
    "hashicorp/%[1]s" = %[2]q
  }
  direct {}
}
`, provider, binDir)

	path := filepath.Join(pluginDir, "dev.tfrc")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return "", fmt.Errorf("%w: write %s: %w", ErrUsage, path, err)
	}
	return path, nil
}

// moduleDirs returns the directories under root that contain at least one .tf
// file, sorted. Every such directory is a standalone module, as the `find`
// pipeline in the shell script assumed.
func moduleDirs(root string) ([]string, error) {
	var modules []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tf") {
			return nil
		}
		modules = append(modules, filepath.Dir(path))
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(modules)
	return slices.Compact(modules), nil
}

// validateProvider rejects a name that would escape the plugin directory or
// produce an invalid dev_overrides key.
func validateProvider(provider string) error {
	if provider == "" {
		return fmt.Errorf("%w: --provider is required", ErrUsage)
	}
	if provider == "." || provider == ".." || strings.ContainsAny(provider, `/\`) {
		return fmt.Errorf("%w: provider %q must be a single path element", ErrUsage, provider)
	}
	return nil
}
