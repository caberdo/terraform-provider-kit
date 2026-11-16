package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/branchprotection"
)

const cliTestVersion = "v0.1.0-test"

// cliRulesetJSON is a ruleset the policy accepts.
const cliRulesetJSON = `{
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
      "required_status_checks": [{"context": "Lint & Security"}, {"context": "Test"}]
    }},
    {"type": "merge_queue", "parameters": {"merge_method": "SQUASH"}}
  ]
}`

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
