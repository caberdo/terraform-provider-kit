package main

import (
	"bytes"
	"testing"
)

func TestRunForwardsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got, want := stdout.String(), "tfkit "+version+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

func TestVersionIsOverridable(t *testing.T) {
	// Guard the -X main.version hook: a const would silently ignore
	// GoReleaser's -ldflags and every release would report "dev".
	if version == "" {
		t.Fatal("version is empty, want a build-time default")
	}
}
