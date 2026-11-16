package branchprotection

import (
	"bytes"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// policyRoot writes a miniature provider checkout whose pull_request workflow
// reports the two default contexts plus a matrix and a conditional job.
func policyRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".github", "workflows", "test.yml"), `name: Test
on:
  pull_request:
jobs:
  lint:
    name: Lint & Security
    runs-on: ubuntu-latest
  test:
    name: Test
    runs-on: ubuntu-latest
  matrix:
    name: Matrix
    strategy:
      matrix:
        go: ["1.26", "1.27"]
  conditional:
    name: Conditional
    if: github.event_name == 'push'
`)
	return root
}

// validRulesetJSON is a ruleset that satisfies the policy check.
const validRulesetJSON = `{
  "id": 7,
  "name": "main-protection",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {
    "ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}
  },
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

func TestCheckMatchesRuleset(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets/7", stdout: validRulesetJSON},
		{match: "repos/o/r/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v (stderr = %q)", err, stderr.String())
	}
	if status != StatusOK {
		t.Fatalf("status = %d, want %d (stderr = %q)", status, StatusOK, stderr.String())
	}
	want := "branch protection for o/r is correct (main-protection is active).\n"
	if got := stdout.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
}

func TestCheckReportsMissingRuleset(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets", stdout: "[]"},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusDrift {
		t.Fatalf("status = %d, want %d", status, StatusDrift)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	for _, want := range []string{
		"branch protection drift detected for o/r:",
		"- ruleset main-protection does not exist",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

func TestCheckReportsEveryPolicyViolation(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets/7", stdout: `{
  "enforcement": "evaluate",
  "bypass_actors": [{"actor_id": 1, "actor_type": "User"}],
  "conditions": {"ref_name": {"include": ["refs/heads/main"], "exclude": []}},
  "rules": [
    {"type": "pull_request", "parameters": {"required_review_thread_resolution": false}},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": false,
      "required_status_checks": [{"context": "Matrix"}]
    }}
  ]
}`},
		{match: "repos/o/r/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusDrift {
		t.Fatalf("status = %d, want %d", status, StatusDrift)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	for _, want := range []string{
		"- ruleset enforcement is 'evaluate', expected 'active'",
		"- ruleset is missing the 'deletion' rule",
		"- ruleset is missing the 'non_fast_forward' rule",
		"- ruleset is missing the 'required_linear_history' rule",
		"- ruleset conditions do not include '~DEFAULT_BRANCH'",
		"- ruleset has 1 bypass actor(s); a solo maintainer must not bypass the gate",
		"- required_review_thread_resolution is not enabled",
		"- required contexts are [Matrix], expected [Lint & Security Test]",
		"- ruleset is missing the 'merge_queue' rule (or document the fallback in .github/branch-protection-fallback)",
		"- strict_required_status_checks_policy is not enabled",
		"- required check \"Matrix\" is a matrix job",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

func TestCheckAcceptsTheDocumentedFallback(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets/7", stdout: `{
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "required_linear_history"},
    {"type": "pull_request", "parameters": {"required_review_thread_resolution": true}},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": false,
      "required_status_checks": [{"context": "Lint & Security"}, {"context": "Test"}]
    }}
  ]
}`},
		{match: "repos/o/r/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
	}}
	root := policyRoot(t)
	writeFile(t, filepath.Join(root, ".github", "branch-protection-fallback"), "no merge queue\n")
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: root, GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusOK {
		t.Fatalf("status = %d, want %d (stderr = %q)", status, StatusOK, stderr.String())
	}
	if !strings.Contains(stderr.String(), "note: .github/branch-protection-fallback exists") {
		t.Errorf("stderr = %q, want the fallback note", stderr.String())
	}
}

func TestCheckUnreadableRulesetsExitsThree(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets", err: errors.New("HTTP 403: Resource not accessible")},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusUnreadable {
		t.Fatalf("status = %d, want %d", status, StatusUnreadable)
	}
	if !strings.Contains(stderr.String(), "cannot read rulesets for o/r") {
		t.Errorf("stderr = %q, want it to name the unreadable rulesets", stderr.String())
	}
	if !strings.Contains(stderr.String(), "administration read permission") {
		t.Errorf("stderr = %q, want the permission hint", stderr.String())
	}
}

func TestCheckUnreadableRulesetDetailExitsThree(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets/7", err: errors.New("HTTP 500")},
		{match: "repos/o/r/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusUnreadable {
		t.Fatalf("status = %d, want %d", status, StatusUnreadable)
	}
}

func TestCheckSoftTurnsAnUnreadableRulesetIntoAWarning(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets", err: errors.New("HTTP 403")},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: policyRoot(t), Soft: true, GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusOK {
		t.Fatalf("status = %d, want %d", status, StatusOK)
	}
	if got := stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if !strings.Contains(stderr.String(), "warning: cannot read rulesets for o/r; skipping the drift check") {
		t.Errorf("stderr = %q, want the soft warning", stderr.String())
	}
}

func TestCheckRejectsInvalidExpectedContextsBeforeCallingGitHub(t *testing.T) {
	gh := &fakeGitHub{}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: policyRoot(t), Contexts: []string{"Nope"},
		GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusDrift {
		t.Fatalf("status = %d, want %d", status, StatusDrift)
	}
	if len(gh.calls) != 0 {
		t.Errorf("gh calls = %v, want none: the contexts are invalid before any API call", gh.calls)
	}
	for _, want := range []string{"required status check contexts are not valid", "Nope", "Lint & Security"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

func TestCheckReadsTheRepositoryFromTheEnvironment(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "env/repo")
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/env/repo/rulesets/7", stdout: validRulesetJSON},
		{match: "repos/env/repo/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusOK {
		t.Fatalf("status = %d, want %d (stderr = %q)", status, StatusOK, stderr.String())
	}
}

func TestCheckAsksGitHubForTheRepository(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repo view", stdout: "view/repo\n"},
		{match: "repos/view/repo/rulesets/7", stdout: validRulesetJSON},
		{match: "repos/view/repo/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusOK {
		t.Fatalf("status = %d, want %d (stderr = %q)", status, StatusOK, stderr.String())
	}
	if calls := gh.calls; len(calls) == 0 || !slices.Contains(calls[0], "view") {
		t.Errorf("calls = %v, want the first call to be `gh repo view`", calls)
	}
}

func TestCheckCannotDetermineTheRepository(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "")
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repo view", err: errors.New("not a git repository")},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusUnreadable {
		t.Fatalf("status = %d, want %d", status, StatusUnreadable)
	}
	if !strings.Contains(stderr.String(), "cannot determine the repository; set GITHUB_REPOSITORY=owner/repo") {
		t.Errorf("stderr = %q, want the repository hint", stderr.String())
	}
}

func TestCheckReadsTheRulesetNameFromTheEnvironment(t *testing.T) {
	t.Setenv("RULESET_NAME", "other-protection")
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets/9", stdout: strings.ReplaceAll(validRulesetJSON, `"id": 7`, `"id": 9`)},
		{match: "repos/o/r/rulesets", stdout: `[{"id": 9, "name": "other-protection"}]`},
	}}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if status != StatusOK {
		t.Fatalf("status = %d, want %d (stderr = %q)", status, StatusOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "(other-protection is active)") {
		t.Errorf("stdout = %q, want it to name the environment ruleset", stdout.String())
	}
}

func TestCheckReportsUnreadableWorkflowsAsAConfigurationError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".github", "workflows", "broken.yml"), "on: pull_request\njobs: [\n")
	gh := &fakeGitHub{}
	var stdout, stderr bytes.Buffer

	status, err := Check(CheckOptions{
		Repo: "o/r", Root: root, GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil {
		t.Fatal("Check = nil error, want the workflow parse failure")
	}
	if status != StatusConfig {
		t.Errorf("status = %d, want %d", status, StatusConfig)
	}
}
