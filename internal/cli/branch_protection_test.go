package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/branchprotection"
)

const cliTestVersion = "v0.1.0-test"

// cliRulesetTemplate is a ruleset the policy accepts; the placeholder holds the
// required_status_checks entries.
const cliRulesetTemplate = `{
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "required_linear_history"},
    {"type": "pull_request", "parameters": {"required_review_thread_resolution": true}},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": true,
      "required_status_checks": [%s]
    }},
    {"type": "merge_queue", "parameters": {"merge_method": "SQUASH"}}
  ]
}`

// cliRulesetJSON is a ruleset that requires the two default contexts.
var cliRulesetJSON = cliRulesetWithContexts("Lint & Security", "Test")

// cliRulesetWithContexts builds a ruleset that requires exactly contexts.
func cliRulesetWithContexts(contexts ...string) string {
	checks := make([]string, 0, len(contexts))
	for _, context := range contexts {
		checks = append(checks, fmt.Sprintf(`{"context": %q}`, context))
	}
	return fmt.Sprintf(cliRulesetTemplate, strings.Join(checks, ", "))
}

// cliGitHubResponse is one canned gh invocation. The first entry whose match is
// a substring of the joined arguments wins.
type cliGitHubResponse struct {
	match  string
	stdout string
	err    error
}

// cliFakeGitHub stands in for gh so no command test needs the binary or a
// network.
type cliFakeGitHub struct {
	responses []cliGitHubResponse
	calls     [][]string
}

func (f *cliFakeGitHub) Run(args []string, input []byte) ([]byte, error) {
	f.calls = append(f.calls, args)

	joined := strings.Join(args, " ")
	for _, response := range f.responses {
		if strings.Contains(joined, response.match) {
			return []byte(response.stdout), response.err
		}
	}
	return nil, errors.New("unexpected gh invocation: " + joined)
}

func (f *cliFakeGitHub) Workflow(owner, repo, path, ref string) ([]byte, error) {
	return nil, errors.New("no remote workflow in this test")
}

// cliFakeGit is the checkout-root seam.
type cliFakeGit struct {
	root string
	err  error
}

func (f cliFakeGit) Root() (string, error) {
	return f.root, f.err
}

// stubBranchProtectionSeams replaces both seams for one test.
func stubBranchProtectionSeams(t *testing.T, gh branchprotection.GitHub, git branchprotection.Git) {
	t.Helper()

	originalGitHub, originalGit := newBranchProtectionGitHub, newBranchProtectionGit
	newBranchProtectionGitHub = func() branchprotection.GitHub { return gh }
	newBranchProtectionGit = func() branchprotection.Git { return git }
	t.Cleanup(func() {
		newBranchProtectionGitHub = originalGitHub
		newBranchProtectionGit = originalGit
	})
}

// cliPolicyRoot writes a checkout whose pull_request workflow reports the two
// default contexts.
func cliPolicyRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, ".github", "workflows", "test.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	workflow := `name: Test
on:
  pull_request:
jobs:
  lint:
    name: Lint & Security
  test:
    name: Test
`
	if err := os.WriteFile(path, []byte(workflow), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return root
}

// cliContextsRoot writes a checkout whose pull_request workflow reports
// exactly contexts, so a flag can be checked against job names other than the
// two defaults.
func cliContextsRoot(t *testing.T, contexts ...string) string {
	t.Helper()

	var workflow strings.Builder
	workflow.WriteString("name: CI\non:\n  pull_request:\njobs:\n")
	for i, context := range contexts {
		fmt.Fprintf(&workflow, "  job%d:\n    name: %q\n", i, context)
	}

	root := t.TempDir()
	path := filepath.Join(root, ".github", "workflows", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(workflow.String()), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return root
}

// cliMigratedContexts are the contexts a provider reports after moving to the
// reusable ci workflow: the caller prefix and a slash.
var cliMigratedContexts = []string{"ci / Lint & Security", "ci / Test"}

// runBranchProtectionCLI invokes the command with the fixture checkout wired
// in and returns the exit code and both streams.
func runBranchProtectionCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := Run(args, cliTestVersion, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunBranchProtectionCheckPassesTheStatusThrough(t *testing.T) {
	tests := []struct {
		name       string
		responses  []cliGitHubResponse
		wantCode   int
		wantStderr string
	}{
		{
			name: "match",
			responses: []cliGitHubResponse{
				{match: "repos/o/r/rulesets/7", stdout: cliRulesetJSON},
				{match: "repos/o/r/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
			},
			wantCode: 0,
		},
		{
			name:       "drift",
			responses:  []cliGitHubResponse{{match: "repos/o/r/rulesets", stdout: "[]"}},
			wantCode:   1,
			wantStderr: "branch protection drift detected for o/r:",
		},
		{
			name:       "unreadable",
			responses:  []cliGitHubResponse{{match: "repos/o/r/rulesets", err: errors.New("HTTP 403")}},
			wantCode:   3,
			wantStderr: "cannot read rulesets for o/r",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stubBranchProtectionSeams(t, &cliFakeGitHub{responses: test.responses}, cliFakeGit{root: cliPolicyRoot(t)})

			code, _, stderr := runBranchProtectionCLI(t, "branch-protection", "check", "--repo", "o/r")

			if code != test.wantCode {
				t.Fatalf("exit = %d, want %d (stderr = %q)", code, test.wantCode, stderr)
			}
			if test.wantStderr != "" && !strings.Contains(stderr, test.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, test.wantStderr)
			}
		})
	}
}

func TestRunBranchProtectionCheckSoftWarnsOnAnUnreadableRuleset(t *testing.T) {
	gh := &cliFakeGitHub{responses: []cliGitHubResponse{
		{match: "repos/o/r/rulesets", err: errors.New("HTTP 403")},
	}}
	stubBranchProtectionSeams(t, gh, cliFakeGit{root: cliPolicyRoot(t)})

	code, stdout, stderr := runBranchProtectionCLI(t,
		"branch-protection", "check", "--repo", "o/r", "--soft")

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "warning: cannot read rulesets for o/r; skipping the drift check") {
		t.Errorf("stderr = %q, want the soft warning", stderr)
	}
}

func TestRunBranchProtectionCheckSoftDoesNotSoftenDrift(t *testing.T) {
	// --soft only downgrades an unreadable API. A ruleset that is readable but
	// wrong must still fail, whichever way the flag is spelled.
	gh := &cliFakeGitHub{responses: []cliGitHubResponse{
		{match: "repos/o/r/rulesets", stdout: "[]"},
	}}
	stubBranchProtectionSeams(t, gh, cliFakeGit{root: cliPolicyRoot(t)})

	code, _, stderr := runBranchProtectionCLI(t,
		"branch-protection", "check", "--repo", "o/r", "--soft")

	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr = %q)", code, stderr)
	}
	if !strings.Contains(stderr, "branch protection drift detected for o/r:") {
		t.Errorf("stderr = %q, want the drift report", stderr)
	}
}

func TestRunBranchProtectionCheckReadsTheEnvironment(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "env/repo")
	t.Setenv("RULESET_NAME", "env-protection")

	gh := &cliFakeGitHub{responses: []cliGitHubResponse{
		{match: "repos/env/repo/rulesets/5", stdout: cliRulesetJSON},
		{match: "repos/env/repo/rulesets", stdout: `[{"id": 5, "name": "env-protection"}]`},
	}}
	stubBranchProtectionSeams(t, gh, cliFakeGit{root: cliPolicyRoot(t)})

	code, _, stderr := runBranchProtectionCLI(t, "branch-protection", "check")

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr)
	}
	if len(gh.calls) == 0 {
		t.Fatal("no gh invocation was recorded")
	}
	for _, args := range gh.calls {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "repo view") {
			t.Errorf("gh call %q asked for the repository, want $GITHUB_REPOSITORY to supply it", joined)
		}
	}
	if !strings.Contains(strings.Join(gh.calls[len(gh.calls)-1], " "), "repos/env/repo/rulesets") {
		t.Errorf("last gh call = %v, want repos/env/repo/rulesets", gh.calls[len(gh.calls)-1])
	}
}

func TestRunBranchProtectionCheckFlagsOverrideTheEnvironment(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "env/repo")
	t.Setenv("RULESET_NAME", "env-protection")

	gh := &cliFakeGitHub{responses: []cliGitHubResponse{
		{match: "repos/flag/repo/rulesets/9", stdout: cliRulesetJSON},
		{match: "repos/flag/repo/rulesets", stdout: `[{"id": 9, "name": "flag-protection"}]`},
	}}
	stubBranchProtectionSeams(t, gh, cliFakeGit{root: cliPolicyRoot(t)})

	code, _, stderr := runBranchProtectionCLI(t,
		"branch-protection", "check", "--repo", "flag/repo", "--ruleset", "flag-protection")

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr)
	}
	if !strings.Contains(strings.Join(gh.calls[len(gh.calls)-1], " "), "repos/flag/repo/rulesets") {
		t.Errorf("last gh call = %v, want repos/flag/repo/rulesets", gh.calls[len(gh.calls)-1])
	}
	for _, args := range gh.calls {
		if joined := strings.Join(args, " "); strings.Contains(joined, "env/repo") {
			t.Errorf("gh call %q used the environment repository, want the flag", joined)
		}
	}
}

func TestRunBranchProtectionHelpExitsZero(t *testing.T) {
	for _, command := range []string{"check", "setup"} {
		t.Run(command, func(t *testing.T) {
			code, _, stderr := runBranchProtectionCLI(t, "branch-protection", command, "-h")

			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr)
			}
			if !strings.Contains(stderr, "usage: tfkit branch-protection "+command) {
				t.Errorf("stderr = %q, want the %s usage text", stderr, command)
			}
			if !strings.Contains(stderr, "git rev-parse --show-toplevel") {
				t.Errorf("stderr = %q, want it to document the checkout-root discovery", stderr)
			}
		})
	}
}

func TestRunBranchProtectionCheckTrimsTheContextsFlag(t *testing.T) {
	// The migrated contexts carry spaces and a slash. The flag's entries are
	// trimmed, so the surrounding spaces the shell hands over are not part of
	// the names.
	gh := &cliFakeGitHub{responses: []cliGitHubResponse{
		{match: "repos/o/r/rulesets/7", stdout: cliRulesetWithContexts(cliMigratedContexts...)},
		{match: "repos/o/r/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
	}}
	stubBranchProtectionSeams(t, gh, cliFakeGit{root: cliContextsRoot(t, cliMigratedContexts...)})

	code, _, stderr := runBranchProtectionCLI(t,
		"branch-protection", "check", "--repo", "o/r",
		"--contexts", " ci / Lint & Security , ci / Test ")

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr)
	}
}

func TestRunBranchProtectionCheckContextsFlagOverridesTheEnvironment(t *testing.T) {
	// REQUIRED_CONTEXTS names the pre-migration contexts, which this checkout
	// no longer reports; only the flag's list can match.
	t.Setenv("REQUIRED_CONTEXTS", "Lint & Security,Test")

	gh := &cliFakeGitHub{responses: []cliGitHubResponse{
		{match: "repos/o/r/rulesets/7", stdout: cliRulesetWithContexts(cliMigratedContexts...)},
		{match: "repos/o/r/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
	}}
	stubBranchProtectionSeams(t, gh, cliFakeGit{root: cliContextsRoot(t, cliMigratedContexts...)})

	code, _, stderr := runBranchProtectionCLI(t,
		"branch-protection", "check", "--repo", "o/r",
		"--contexts", strings.Join(cliMigratedContexts, ","))

	if code != 0 {
		t.Fatalf("exit = %d, want 0: the flag must win over REQUIRED_CONTEXTS (stderr = %q)", code, stderr)
	}
}

func TestRunBranchProtectionCheckRejectsAnEmptyContext(t *testing.T) {
	for _, value := range []string{
		"Lint & Security,,Test",
		"Lint & Security,",
		"Lint & Security, ,Test",
		"",
	} {
		t.Run(value, func(t *testing.T) {
			stubBranchProtectionSeams(t, &cliFakeGitHub{}, cliFakeGit{root: cliPolicyRoot(t)})

			code, _, stderr := runBranchProtectionCLI(t,
				"branch-protection", "check", "--repo", "o/r", "--contexts", value)

			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr)
			}
			if !strings.Contains(stderr, "empty entry") {
				t.Errorf("stderr = %q, want the empty-entry usage error", stderr)
			}
			if !strings.Contains(stderr, "usage: tfkit branch-protection check") {
				t.Errorf("stderr = %q, want the usage text", stderr)
			}
		})
	}
}

func TestRunBranchProtectionSetupAcceptsTheContextsFlag(t *testing.T) {
	gh := &cliFakeGitHub{responses: []cliGitHubResponse{
		{match: "repos/o/r/rulesets --input -", stdout: `{"id": 7}`},
		{match: "repos/o/r/rulesets", stdout: "[]"},
		{match: "repos/o/r", stdout: `{"permissions": {"admin": true}}`},
	}}
	stubBranchProtectionSeams(t, gh, cliFakeGit{root: cliContextsRoot(t, cliMigratedContexts...)})

	code, stdout, stderr := runBranchProtectionCLI(t,
		"branch-protection", "setup", "--repo", "o/r",
		"--contexts", strings.Join(cliMigratedContexts, ","))

	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr = %q)", code, stderr)
	}
	want := "created ruleset main-protection on o/r with contexts: " +
		strings.Join(cliMigratedContexts, ", ") + "\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestRunBranchProtectionSetupRejectsAnEmptyContext(t *testing.T) {
	stubBranchProtectionSeams(t, &cliFakeGitHub{}, cliFakeGit{root: cliContextsRoot(t, cliMigratedContexts...)})

	code, _, stderr := runBranchProtectionCLI(t,
		"branch-protection", "setup", "--repo", "o/r", "--contexts", "ci / Test,")

	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr = %q)", code, stderr)
	}
	if !strings.Contains(stderr, "empty entry") {
		t.Errorf("stderr = %q, want the empty-entry usage error", stderr)
	}
}
