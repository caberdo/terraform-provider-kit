package branchprotection

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// setupPayload is the subset of the PUT/POST body the tests pin.
type setupPayload struct {
	Name         string            `json:"name"`
	Target       string            `json:"target"`
	Enforcement  string            `json:"enforcement"`
	BypassActors []json.RawMessage `json:"bypass_actors"`
	Conditions   struct {
		RefName struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []struct {
		Type       string          `json:"type"`
		Parameters json.RawMessage `json:"parameters"`
	} `json:"rules"`
}

func (p setupPayload) rule(name string) (json.RawMessage, bool) {
	for _, rule := range p.Rules {
		if rule.Type == name {
			return rule.Parameters, true
		}
	}
	return nil, false
}

func (p setupPayload) ruleTypes() []string {
	types := make([]string, 0, len(p.Rules))
	for _, rule := range p.Rules {
		types = append(types, rule.Type)
	}
	return types
}

// findCall returns the arguments and stdin of the gh invocation whose joined
// arguments contain substr.
func findCall(t *testing.T, gh *fakeGitHub, substr string) ([]string, []byte) {
	t.Helper()

	for i, args := range gh.calls {
		if strings.Contains(strings.Join(args, " "), substr) {
			return args, gh.inputs[i]
		}
	}
	t.Fatalf("no gh call contained %q; calls = %v", substr, gh.calls)
	return nil, nil
}

// createFlowAnswers every gh call of a first-time setup.
func createFlowAnswers() []fakeResponse {
	return []fakeResponse{
		{match: "repos/o/r/rulesets --input -", stdout: `{"id": 7}`},
		{match: "repos/o/r/rulesets", stdout: "[]"},
		{match: "repos/o/r", stdout: `{"permissions": {"admin": true}}`},
	}
}

func TestSetupCreatesTheRuleset(t *testing.T) {
	gh := &fakeGitHub{responses: createFlowAnswers()}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Setup: %v (stderr = %q)", err, stderr.String())
	}
	if got, want := stdout.String(), "created ruleset main-protection on o/r with contexts: Lint & Security, Test\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}

	args, input := findCall(t, gh, "--method POST")
	if !slices.Contains(args, "repos/o/r/rulesets") {
		t.Errorf("POST args = %v, want the collection path", args)
	}
	if len(input) == 0 {
		t.Fatal("POST received no payload")
	}

	var payload setupPayload
	if err := json.Unmarshal(input, &payload); err != nil {
		t.Fatalf("decode payload: %v\n%s", err, input)
	}
	if payload.Name != "main-protection" || payload.Target != "branch" || payload.Enforcement != "active" {
		t.Errorf("payload = %+v, want main-protection/branch/active", payload)
	}
	if payload.BypassActors == nil || len(payload.BypassActors) != 0 {
		t.Errorf("bypass_actors = %v, want an empty JSON array", payload.BypassActors)
	}
	if got, want := payload.Conditions.RefName.Include, []string{"~DEFAULT_BRANCH"}; !slices.Equal(got, want) {
		t.Errorf("ref include = %v, want %v", got, want)
	}
	wantRules := []string{
		"deletion", "non_fast_forward", "required_linear_history",
		"pull_request", "required_status_checks", "merge_queue",
	}
	if got := payload.ruleTypes(); !slices.Equal(got, wantRules) {
		t.Errorf("rules = %v, want %v", got, wantRules)
	}

	var statusChecks struct {
		Strict               bool `json:"strict_required_status_checks_policy"`
		RequiredStatusChecks []struct {
			Context string `json:"context"`
		} `json:"required_status_checks"`
	}
	parameters, _ := payload.rule("required_status_checks")
	if err := json.Unmarshal(parameters, &statusChecks); err != nil {
		t.Fatalf("decode status checks: %v", err)
	}
	if !statusChecks.Strict {
		t.Error("strict_required_status_checks_policy = false, want true")
	}
	contexts := make([]string, 0, len(statusChecks.RequiredStatusChecks))
	for _, check := range statusChecks.RequiredStatusChecks {
		contexts = append(contexts, check.Context)
	}
	if want := []string{"Lint & Security", "Test"}; !slices.Equal(contexts, want) {
		t.Errorf("contexts = %v, want %v", contexts, want)
	}

	var pullRequest struct {
		RequiredReviewThreadResolution bool `json:"required_review_thread_resolution"`
	}
	parameters, _ = payload.rule("pull_request")
	if err := json.Unmarshal(parameters, &pullRequest); err != nil {
		t.Fatalf("decode pull_request: %v", err)
	}
	if !pullRequest.RequiredReviewThreadResolution {
		t.Error("required_review_thread_resolution = false, want true")
	}

	var mergeQueue struct {
		MergeMethod string `json:"merge_method"`
	}
	parameters, _ = payload.rule("merge_queue")
	if err := json.Unmarshal(parameters, &mergeQueue); err != nil {
		t.Fatalf("decode merge_queue: %v", err)
	}
	if mergeQueue.MergeMethod != "SQUASH" {
		t.Errorf("merge_method = %q, want SQUASH", mergeQueue.MergeMethod)
	}
}

func TestSetupUpdatesAnExistingRuleset(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets/7 --input -", stdout: `{"id": 7}`},
		{match: "repos/o/r/rulesets", stdout: `[{"id": 7, "name": "main-protection"}]`},
		{match: "repos/o/r", stdout: `{"permissions": {"admin": true}}`},
	}}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Setup: %v (stderr = %q)", err, stderr.String())
	}
	if got, want := stdout.String(), "updated ruleset main-protection on o/r with contexts: Lint & Security, Test\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	args, _ := findCall(t, gh, "--method PUT")
	if !slices.Contains(args, "repos/o/r/rulesets/7") {
		t.Errorf("PUT args = %v, want the ruleset path", args)
	}
}

func TestSetupRefusesAMatrixContext(t *testing.T) {
	gh := &fakeGitHub{}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), Contexts: []string{"Matrix"},
		GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil || !strings.Contains(err.Error(), "matrix job") {
		t.Fatalf("Setup = %v, want a matrix-job refusal", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("gh calls = %v, want none: an invalid context is refused before any API call", gh.calls)
	}
}

func TestSetupRefusesAConditionalContext(t *testing.T) {
	gh := &fakeGitHub{}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), Contexts: []string{"Conditional"},
		GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil || !strings.Contains(err.Error(), "guarded by if:") {
		t.Fatalf("Setup = %v, want an if:-guarded refusal", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("gh calls = %v, want none", gh.calls)
	}
}

func TestSetupListsTheAvailableContextsWhenOneIsUnknown(t *testing.T) {
	gh := &fakeGitHub{}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), Contexts: []string{"Nope"},
		GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil {
		t.Fatal("Setup = nil, want the unknown-context refusal")
	}
	for _, want := range []string{"pull_request job names available to require", "Lint & Security", "Test"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

func TestSetupRequiresAdminPermission(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r", stdout: `{"permissions": {"admin": false}}`},
	}}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil || !strings.Contains(err.Error(), "does not have admin permission on o/r") {
		t.Fatalf("Setup = %v, want the admin refusal", err)
	}
	for _, args := range gh.calls {
		if slices.Contains(args, "--input") {
			t.Errorf("gh call = %v, want no write without admin permission", args)
		}
	}
}

func TestSetupWithoutAdminPermissionIsNotReportedAsAnAPIError(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r", err: errors.New("HTTP 403")},
	}}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil || !strings.Contains(err.Error(), "does not have admin permission") {
		t.Fatalf("Setup = %v, want the admin refusal", err)
	}
}

func TestSetupHonoursTheFallbackFile(t *testing.T) {
	gh := &fakeGitHub{responses: createFlowAnswers()}
	root := policyRoot(t)
	writeFile(t, filepath.Join(root, ".github", "branch-protection-fallback"), "no merge queue\n")
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: root, GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Setup: %v (stderr = %q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: .github/branch-protection-fallback exists") {
		t.Errorf("stderr = %q, want the fallback warning", stderr.String())
	}

	_, input := findCall(t, gh, "--method POST")
	var payload setupPayload
	if err := json.Unmarshal(input, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if _, ok := payload.rule("merge_queue"); ok {
		t.Errorf("rules = %v, want no merge_queue under the fallback", payload.ruleTypes())
	}
	var statusChecks struct {
		Strict bool `json:"strict_required_status_checks_policy"`
	}
	parameters, _ := payload.rule("required_status_checks")
	if err := json.Unmarshal(parameters, &statusChecks); err != nil {
		t.Fatalf("decode status checks: %v", err)
	}
	if statusChecks.Strict {
		t.Error("strict_required_status_checks_policy = true, want false under the fallback")
	}
}

func TestSetupRefusesARepositoryWithoutPullRequestWorkflows(t *testing.T) {
	gh := &fakeGitHub{}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: t.TempDir(), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil || !strings.Contains(err.Error(), "no workflows under .github/workflows run on pull_request") {
		t.Fatalf("Setup = %v, want the no-workflows refusal", err)
	}
}

func TestSetupReportsARejectedRuleset(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets --input -", err: errors.New("HTTP 422: merge queue is not available")},
		{match: "repos/o/r/rulesets", stdout: "[]"},
		{match: "repos/o/r", stdout: `{"permissions": {"admin": true}}`},
	}}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil || !strings.Contains(err.Error(), "GitHub rejected the ruleset for o/r") {
		t.Fatalf("Setup = %v, want the rejection", err)
	}
	if !strings.Contains(stderr.String(), "cannot use a merge queue") {
		t.Errorf("stderr = %q, want the fallback hint", stderr.String())
	}
}

func TestSetupReportsAnUnreadableRulesetList(t *testing.T) {
	gh := &fakeGitHub{responses: []fakeResponse{
		{match: "repos/o/r/rulesets", err: errors.New("HTTP 403")},
		{match: "repos/o/r", stdout: `{"permissions": {"admin": true}}`},
	}}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err == nil || !strings.Contains(err.Error(), "cannot list rulesets for o/r") {
		t.Fatalf("Setup = %v, want the list failure", err)
	}
	if !strings.Contains(err.Error(), "administer the repository") {
		t.Errorf("error = %v, want the administration hint", err)
	}
}

func TestSetupReadsTheRulesetNameFromTheEnvironment(t *testing.T) {
	t.Setenv("RULESET_NAME", "other-protection")
	gh := &fakeGitHub{responses: createFlowAnswers()}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	_, input := findCall(t, gh, "--method POST")
	var payload setupPayload
	if err := json.Unmarshal(input, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Name != "other-protection" {
		t.Errorf("name = %q, want other-protection", payload.Name)
	}
}

func TestSetupReadsTheContextsFromTheEnvironment(t *testing.T) {
	t.Setenv("REQUIRED_CONTEXTS", " Lint & Security , Test ")
	gh := &fakeGitHub{responses: createFlowAnswers()}
	var stdout, stderr bytes.Buffer

	err := Setup(SetupOptions{
		Repo: "o/r", Root: policyRoot(t), GitHub: gh, Stdout: &stdout, Stderr: &stderr,
	})

	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if got, want := stdout.String(), "created ruleset main-protection on o/r with contexts: Lint & Security, Test\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}
