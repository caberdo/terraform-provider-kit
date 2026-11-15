package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/cli"
)

func TestRunCoverageMergeWritesMergedProfile(t *testing.T) {
	dir := t.TempDir()
	unit := writeProfileFile(t, dir, "a.out",
		"mode: atomic\n"+
			"github.com/x/y/a.go:1.1,2.2 1 1\n")
	acceptance := writeProfileFile(t, dir, "b.out",
		"mode: atomic\n"+
			"github.com/x/y/a.go:1.1,2.2 1 2\n"+
			"github.com/x/y/b.go:3.1,4.2 2 1\n")
	output := filepath.Join(dir, "union.out")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"coverage", "merge", "--output", output, unit, acceptance}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if got, want := stdout.String(), "merged 2 profiles into "+output+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}

	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want := "mode: atomic\n" +
		"github.com/x/y/a.go:1.1,2.2 1 3\n" +
		"github.com/x/y/b.go:3.1,4.2 2 1\n"
	if string(got) != want {
		t.Errorf("merged profile =\n%s\nwant:\n%s", got, want)
	}
}

func TestRunCoverageMergeAcceptsOneInput(t *testing.T) {
	// The script this replaces accepts `<output> <input>`, because its guard
	// is `$# -lt 2` and the output counts as the first argument. Tightening
	// that here would be a behaviour change CI has not asked for.
	dir := t.TempDir()
	input := writeProfileFile(t, dir, "a.out", "mode: atomic\ngithub.com/x/y/a.go:1.1,2.2 1 1\n")
	output := filepath.Join(dir, "union.out")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"coverage", "merge", "--output", output, input}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr.String())
	}
	if _, err := os.Stat(output); err != nil {
		t.Errorf("output was not written: %v", err)
	}
}

func TestRunCoverageMergeRequiresOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"coverage", "merge", "a.out", "b.out"}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--output") {
		t.Errorf("stderr = %q, want the usage text naming --output", stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
}

func TestRunCoverageMergeRequiresInput(t *testing.T) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"coverage", "merge", "--output", filepath.Join(dir, "union.out")}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--output") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestRunCoverageMergeMissingInputExitsOne(t *testing.T) {
	dir := t.TempDir()
	input := writeProfileFile(t, dir, "a.out", "mode: atomic\n")
	missing := filepath.Join(dir, "absent.out")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"coverage", "merge", "--output", filepath.Join(dir, "union.out"), input, missing}, testVersion, &stdout, &stderr)

	// The shell exits 1 for a profile it cannot find; CI depends on that.
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), missing) {
		t.Errorf("stderr = %q, want it to name %q", stderr.String(), missing)
	}
	if _, err := os.Stat(filepath.Join(dir, "union.out")); !os.IsNotExist(err) {
		t.Errorf("output exists after a failed merge, want it absent (stat err = %v)", err)
	}
}

func TestRunCoverageMergeMalformedInputExitsOne(t *testing.T) {
	dir := t.TempDir()
	input := writeProfileFile(t, dir, "a.out",
		"mode: atomic\n"+
			"github.com/x/y/a.go:1.1,2.2 1 1 extra\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"coverage", "merge", "--output", filepath.Join(dir, "union.out"), input, input}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "line 2") {
		t.Errorf("stderr = %q, want it to name the malformed line", stderr.String())
	}
}

func TestRunCoverageMergeModeMismatchExitsOne(t *testing.T) {
	dir := t.TempDir()
	atomic := writeProfileFile(t, dir, "a.out", "mode: atomic\n")
	set := writeProfileFile(t, dir, "b.out", "mode: set\n")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"coverage", "merge", "--output", filepath.Join(dir, "union.out"), atomic, set}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "atomic") || !strings.Contains(stderr.String(), "set") {
		t.Errorf("stderr = %q, want it to mention both modes", stderr.String())
	}
}

func TestRunCoverageMergeOutputErrorExitsOne(t *testing.T) {
	dir := t.TempDir()
	input := writeProfileFile(t, dir, "a.out", "mode: atomic\n")
	output := filepath.Join(dir, "absent", "union.out")

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"coverage", "merge", "--output", output, input}, testVersion, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), output) {
		t.Errorf("stderr = %q, want it to name %q", stderr.String(), output)
	}
}

func TestRunCoverageRequiresSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"coverage"}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "coverage requires a subcommand") {
		t.Errorf("stderr = %q, want it to ask for a subcommand", stderr.String())
	}
}

func TestRunCoverageUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"coverage", "nope"}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown coverage command "nope"`) {
		t.Errorf("stderr = %q, want it to name the unknown subcommand", stderr.String())
	}
}

func TestRunCoverageMergeHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"coverage", "merge", "--help"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "--output") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
}

func TestRunCoverageMergeRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"coverage", "merge", "--nope"}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-nope") {
		t.Errorf("stderr = %q, want it to name the unknown flag", stderr.String())
	}
}

// writeProfileFile writes content to name inside dir and returns the path.
func writeProfileFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
