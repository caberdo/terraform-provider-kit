package branchprotection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Status is the outcome of a branch protection check. The values are the exit
// codes of the script this command replaces.
type Status int

const (
	// StatusOK means the ruleset matches the policy.
	StatusOK Status = 0
	// StatusDrift means the ruleset exists but does not match the policy.
	StatusDrift Status = 1
	// StatusConfig means the check could not run, for example because a
	// workflow is unreadable. It matches the configuration exit code every
	// tfkit command uses.
	StatusConfig Status = 2
	// StatusUnreadable means the rulesets API could not be read, the normal
	// case for a fork or a token without administration permission.
	StatusUnreadable Status = 3
)

// GitHub is the seam through which the ruleset API is reached. The production
// implementation shells out to gh, because the scripts this command replaces
// drive the same CLI.
type GitHub interface {
	// Run invokes gh with args, feeding input on stdin when it is non-nil, and
	// returns stdout.
	Run(args []string, input []byte) ([]byte, error)
}

// NewGitHub returns the GitHub seam that shells out to gh.
func NewGitHub() GitHub {
	return ghClient{}
}

// ghClient is the production GitHub seam.
type ghClient struct{}

func (ghClient) Run(args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(context.Background(), "gh", args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics

	out, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(diagnostics.String()); detail != "" {
			return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, detail)
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// Workflow fetches a reusable workflow through the contents API. It satisfies
// the parser's workflowFetcher.
func (gh ghClient) Workflow(owner, repo, path, ref string) ([]byte, error) {
	return gh.Run([]string{
		"api", "-H", "Accept: application/vnd.github.raw",
		fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", owner, repo, path, ref),
	}, nil)
}

// CheckOptions configures Check. The zero value is usable: it discovers the
// repository with gh, looks for workflows in the current directory and applies
// the default policy.
type CheckOptions struct {
	// Repo is the repository as owner/name. Empty means GITHUB_REPOSITORY,
	// then `gh repo view`.
	Repo string
	// Root is the checkout holding .github/workflows. Empty means the current
	// directory.
	Root string
	// Ruleset is the ruleset name. Empty means RULESET_NAME, then
	// "main-protection".
	Ruleset string
	// Contexts are the required status check contexts. Empty means
	// REQUIRED_CONTEXTS, then the default policy.
	Contexts []string
	// FallbackFile documents a repository that cannot use a merge queue. Empty
	// means .github/branch-protection-fallback under Root.
	FallbackFile string
	// Soft turns an unreadable ruleset into a warning instead of an error.
	Soft bool
	// GitHub is the API seam. Empty means NewGitHub.
	GitHub GitHub
	// Stdout and Stderr receive the human-facing report. Empty discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// DefaultContexts are the status checks the policy requires when neither the
// options nor REQUIRED_CONTEXTS name any.
func DefaultContexts() []string {
	return []string{"Lint & Security", "Test"}
}

// Check verifies that the repository ruleset still requires the checks the
// workflows actually report. It returns StatusOK on a match, StatusDrift on
// drift, StatusUnreadable when the rulesets API cannot be read and StatusConfig
// when the check itself cannot run. Under Soft, an unreadable API is a warning
// and StatusOK.
func Check(opts CheckOptions) (Status, error) {
	gh := opts.GitHub
	if gh == nil {
		gh = NewGitHub()
	}
	stdout, stderr := orDiscard(opts.Stdout), orDiscard(opts.Stderr)

	repo, err := resolveRepo(gh, opts.Repo)
	if err != nil {
		if opts.Soft {
			_, _ = fmt.Fprintf(stderr, "warning: %v; skipping the drift check\n", err)
			return StatusOK, nil
		}
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return StatusUnreadable, nil
	}

	root := resolveRoot(opts.Root)
	contexts := requiredContexts(opts.Contexts)
	jobs, err := pullRequestJobsFor(root, gh)
	if err != nil {
		return StatusConfig, fmt.Errorf("read pull request workflows: %w", err)
	}

	// The expected contexts are checked before the API is read, exactly as the
	// script does: a name that can never report is drift, not an API problem.
	if invalid := invalidContexts(jobs, contexts); len(invalid) > 0 {
		_, _ = io.WriteString(stderr, "the required status check contexts are not valid:\n")
		for _, err := range invalid {
			_, _ = fmt.Fprintf(stderr, "  %v\n", err)
		}
		writeAvailableNames(stderr, jobs)
		return StatusDrift, nil
	}

	name := resolveRulesetName(opts.Ruleset)
	summaries, err := listRulesets(gh, repo)
	if err != nil {
		return opts.unreadable(stderr, repo)
	}
	id := rulesetID(summaries, name)
	if id == 0 {
		writeDrift(stderr, repo, []string{fmt.Sprintf("ruleset %s does not exist", name)})
		return StatusDrift, nil
	}

	detail, err := fetchRuleset(gh, repo, id)
	if err != nil {
		return opts.unreadable(stderr, repo)
	}

	fallback := resolveFallbackPath(root, opts.FallbackFile)
	if fallbackExists(fallback) {
		_, _ = fmt.Fprintf(stderr, "note: %s exists; accepting the documented no-merge-queue fallback\n",
			displayPath(root, fallback))
	}
	failures := policyFailures(detail, contexts, root, fallback)
	failures = append(failures, contextFailures(jobs, detail.requiredContexts())...)

	if len(failures) > 0 {
		writeDrift(stderr, repo, failures)
		return StatusDrift, nil
	}

	_, _ = fmt.Fprintf(stdout, "branch protection for %s is correct (%s is active).\n", repo, name)
	return StatusOK, nil
}

// rulesetSummary is one entry of the ruleset listing.
type rulesetSummary struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// rulesetState is the part of a ruleset the policy reads.
type rulesetState struct {
	Enforcement  string            `json:"enforcement"`
	BypassActors []json.RawMessage `json:"bypass_actors"`
	Conditions   struct {
		RefName struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []rulesetRule `json:"rules"`
}

// rulesetRule is one rule of a ruleset.
type rulesetRule struct {
	Type       string          `json:"type"`
	Parameters *ruleParameters `json:"parameters"`
}

// ruleParameters holds the parameters the policy reads. GitHub returns the same
// object shape for every rule type, with only the relevant fields set.
type ruleParameters struct {
	StrictRequiredStatusChecksPolicy bool `json:"strict_required_status_checks_policy"`
	RequiredReviewThreadResolution   bool `json:"required_review_thread_resolution"`
	RequiredStatusChecks             []struct {
		Context string `json:"context"`
	} `json:"required_status_checks"`
}

// has reports whether the ruleset contains a rule of the given type.
func (d rulesetState) has(ruleType string) bool {
	for _, rule := range d.Rules {
		if rule.Type == ruleType {
			return true
		}
	}
	return false
}

// parameters returns the parameters of the first rule of the given type.
func (d rulesetState) parameters(ruleType string) *ruleParameters {
	for _, rule := range d.Rules {
		if rule.Type == ruleType {
			return rule.Parameters
		}
	}
	return nil
}

// requiredContexts returns every context the required_status_checks rules name.
func (d rulesetState) requiredContexts() []string {
	var contexts []string
	for _, rule := range d.Rules {
		if rule.Type != "required_status_checks" || rule.Parameters == nil {
			continue
		}
		for _, check := range rule.Parameters.RequiredStatusChecks {
			contexts = append(contexts, check.Context)
		}
	}
	return contexts
}

// policyFailures compares a ruleset with the policy and returns one message per
// violation, in the order the script reports them.
func policyFailures(detail rulesetState, expected []string, root, fallback string) []string {
	var failures []string
	fail := func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}

	if detail.Enforcement != "active" {
		fail("ruleset enforcement is '%s', expected 'active'", detail.Enforcement)
	}

	for _, rule := range []string{
		"deletion", "non_fast_forward", "required_linear_history",
		"pull_request", "required_status_checks",
	} {
		if !detail.has(rule) {
			fail("ruleset is missing the '%s' rule", rule)
		}
	}

	if !slices.Contains(detail.Conditions.RefName.Include, "~DEFAULT_BRANCH") {
		fail("ruleset conditions do not include '~DEFAULT_BRANCH'")
	}

	if count := len(detail.BypassActors); count != 0 {
		fail("ruleset has %d bypass actor(s); a solo maintainer must not bypass the gate", count)
	}

	threadResolution := false
	if parameters := detail.parameters("pull_request"); parameters != nil {
		threadResolution = parameters.RequiredReviewThreadResolution
	}
	if !threadResolution {
		fail("required_review_thread_resolution is not enabled")
	}

	actual := detail.requiredContexts()
	sort.Strings(actual)
	sorted := slices.Clone(expected)
	sort.Strings(sorted)
	if !slices.Equal(actual, sorted) {
		fail("required contexts are [%s], expected [%s]",
			strings.Join(actual, " "), strings.Join(sorted, " "))
	}

	if fallbackExists(fallback) {
		return failures
	}

	if !detail.has("merge_queue") {
		fail("ruleset is missing the 'merge_queue' rule (or document the fallback in %s)",
			displayPath(root, fallback))
	}

	strict := false
	if parameters := detail.parameters("required_status_checks"); parameters != nil {
		strict = parameters.StrictRequiredStatusChecksPolicy
	}
	if !strict {
		fail("strict_required_status_checks_policy is not enabled")
	}

	return failures
}

// contextFailures reports every actual context that could never report.
func contextFailures(jobs []Job, contexts []string) []string {
	var failures []string
	for _, context := range contexts {
		if context == "" {
			continue
		}
		if err := ValidateContexts(jobs, []string{context}); err != nil {
			failures = append(failures, err.Error())
		}
	}
	return failures
}

// invalidContexts validates the expected contexts against the pull request
// jobs without stopping at the first failure, so one run names them all.
func invalidContexts(jobs []Job, contexts []string) []error {
	var invalid []error
	for _, context := range contexts {
		if err := ValidateContexts(jobs, []string{context}); err != nil {
			invalid = append(invalid, err)
		}
	}
	return invalid
}

// writeAvailableNames lists the pull request job names a caller could require.
func writeAvailableNames(w io.Writer, jobs []Job) {
	_, _ = io.WriteString(w, "pull_request job names in the tree:\n")
	for _, name := range sortedJobNames(jobs) {
		_, _ = fmt.Fprintf(w, "  - %s\n", name)
	}
}

// jobNames returns the sorted, unique display names of jobs.
func sortedJobNames(jobs []Job) []string {
	seen := make(map[string]bool, len(jobs))
	names := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if seen[job.Name] {
			continue
		}
		seen[job.Name] = true
		names = append(names, job.Name)
	}
	sort.Strings(names)
	return names
}

// writeDrift prints the drift report the script prints.
func writeDrift(w io.Writer, repo string, failures []string) {
	_, _ = fmt.Fprintf(w, "branch protection drift detected for %s:\n", repo)
	for _, failure := range failures {
		_, _ = fmt.Fprintf(w, "  - %s\n", failure)
	}
}

// unreadable reports a ruleset the API refused to return.
func (opts CheckOptions) unreadable(stderr io.Writer, repo string) (Status, error) {
	if opts.Soft {
		_, _ = fmt.Fprintf(stderr, "warning: cannot read rulesets for %s; skipping the drift check\n", repo)
		return StatusOK, nil
	}
	_, _ = fmt.Fprintf(stderr,
		"error: cannot read rulesets for %s; the token needs administration read permission\n", repo)
	return StatusUnreadable, nil
}

// listRulesets returns every ruleset the repository exposes. gh --paginate can
// emit one JSON array per page back to back, so the decoder consumes them in a
// loop.
func listRulesets(gh GitHub, repo string) ([]rulesetSummary, error) {
	out, err := gh.Run([]string{"api", "--paginate", "repos/" + repo + "/rulesets"}, nil)
	if err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(out))
	var summaries []rulesetSummary
	for {
		var page []rulesetSummary
		err := decoder.Decode(&page)
		if errors.Is(err, io.EOF) {
			return summaries, nil
		}
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, page...)
	}
}

// rulesetID returns the id of the first ruleset with the given name, or 0.
func rulesetID(summaries []rulesetSummary, name string) int64 {
	for _, summary := range summaries {
		if summary.Name == name {
			return summary.ID
		}
	}
	return 0
}

// fetchRuleset fetches one ruleset.
func fetchRuleset(gh GitHub, repo string, id int64) (rulesetState, error) {
	out, err := gh.Run([]string{"api", fmt.Sprintf("repos/%s/rulesets/%d", repo, id)}, nil)
	if err != nil {
		return rulesetState{}, err
	}

	var detail rulesetState
	if err := json.Unmarshal(out, &detail); err != nil {
		return rulesetState{}, err
	}
	return detail, nil
}

// resolveRepo returns the repository to inspect: the argument, then
// GITHUB_REPOSITORY, then `gh repo view`.
func resolveRepo(gh GitHub, repo string) (string, error) {
	if repo != "" {
		return repo, nil
	}
	if env := os.Getenv("GITHUB_REPOSITORY"); env != "" {
		return env, nil
	}

	out, err := gh.Run([]string{"repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"}, nil)
	if err != nil {
		return "", errors.New("cannot determine the repository; set GITHUB_REPOSITORY=owner/repo")
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", errors.New("cannot determine the repository; set GITHUB_REPOSITORY=owner/repo")
	}
	return name, nil
}

// pullRequestJobsFor resolves remote reusable workflows when the seam can fetch
// them. A Run-only seam leaves them unresolved, as the shell helper must.
func pullRequestJobsFor(root string, gh GitHub) ([]Job, error) {
	if fetcher, ok := gh.(workflowFetcher); ok {
		return pullRequestJobs(root, fetcher)
	}
	return PullRequestJobs(root)
}

// requiredContexts returns the contexts to require: the options, then
// REQUIRED_CONTEXTS, then the default policy.
func requiredContexts(contexts []string) []string {
	if len(contexts) > 0 {
		return trimContexts(contexts)
	}
	if env := os.Getenv("REQUIRED_CONTEXTS"); env != "" {
		return trimContexts(strings.Split(env, ","))
	}
	return DefaultContexts()
}

// trimContexts trims the whitespace a comma-separated list may carry.
func trimContexts(contexts []string) []string {
	trimmed := make([]string, 0, len(contexts))
	for _, context := range contexts {
		trimmed = append(trimmed, strings.TrimSpace(context))
	}
	return trimmed
}

// resolveRoot is the checkout the commands read workflows from.
func resolveRoot(root string) string {
	if root == "" {
		return "."
	}
	return root
}

// resolveRulesetName is the ruleset name, with RULESET_NAME as the environment
// override.
func resolveRulesetName(ruleset string) string {
	if ruleset != "" {
		return ruleset
	}
	if env := os.Getenv("RULESET_NAME"); env != "" {
		return env
	}
	return "main-protection"
}

// resolveFallbackPath is the documented no-merge-queue fallback file.
func resolveFallbackPath(root, fallback string) string {
	if fallback != "" {
		return fallback
	}
	return filepath.Join(root, ".github", "branch-protection-fallback")
}

// fallbackExists reports whether the fallback file is a regular file.
func fallbackExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// displayPath renders path relative to root when it is inside it, matching the
// note the script prints.
func displayPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(relative, "..") {
		return path
	}
	return filepath.ToSlash(relative)
}

// orDiscard keeps a nil writer from panicking.
func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
