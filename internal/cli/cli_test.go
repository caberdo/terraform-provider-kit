package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/cli"
)

const testVersion = "v0.1.0-test"

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"version"}, testVersion, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got, want := stdout.String(), "tfkit "+testVersion+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"nope"}, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "nope"`) {
		t.Errorf("stderr = %q, want it to name the unknown command", stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
}

func TestRunNoArgsPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := cli.Run(nil, testVersion, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
}
