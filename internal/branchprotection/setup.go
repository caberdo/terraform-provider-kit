package branchprotection

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SetupOptions configures Setup. The zero value is usable: it discovers the
// repository with gh, reads workflows from the git checkout root and installs
// the default policy.
type SetupOptions struct {
	// Repo is the repository as owner/name. Empty means GITHUB_REPOSITORY,
	// then `gh repo view`.
	Repo string
	// Root is the checkout holding .github/workflows. Empty means the git
	// checkout root (`git rev-parse --show-toplevel`), then the current
	// directory when git cannot answer.
	Root string
	// Git discovers the checkout when Root is empty. Empty means NewGit.
	Git Git
	// Ruleset is the ruleset name. Empty means RULESET_NAME, then
	// "main-protection".
	Ruleset string
	// Contexts are the required status check contexts. Empty means
	// REQUIRED_CONTEXTS, then the default policy.
	Contexts []string
	// FallbackFile documents a repository that cannot use a merge queue. Empty
	// means .github/branch-protection-fallback under Root.
	FallbackFile string
	// GitHub is the API seam. Empty means NewGitHub.
	GitHub GitHub
	// Stdout and Stderr receive the human-facing report. Empty discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// Setup installs or updates the repository ruleset. It is idempotent: it looks
// the ruleset up by name and writes the same payload on every run.
//
// It refuses to guess context names. The requested contexts are validated
// against the job names of workflows that run on pull_request, so a matrix or
// if:-guarded job, whose check may never report the required name, is rejected
// before anything is written. The current token must have admin permission on
// the repository.
//
// A .github/branch-protection-fallback file documents a repository that cannot
// use a merge queue: Setup then omits the merge_queue rule and disables the
// strict status check policy, which is what the check command expects.
func Setup(opts SetupOptions) error {
	gh := opts.GitHub
	if gh == nil {
		gh = NewGitHub()
	}
	stdout, stderr := orDiscard(opts.Stdout), orDiscard(opts.Stderr)

	repo, err := resolveRepo(gh, opts.Repo)
	if err != nil {
		return err
	}

	root := resolveRoot(opts.Root, opts.Git)
	jobs, err := pullRequestJobsFor(root, gh)
	if err != nil {
		return fmt.Errorf("read pull request workflows: %w", err)
	}
	if len(jobs) == 0 {
		return errors.New("no workflows under .github/workflows run on pull_request")
	}

	contexts := requiredContexts(opts.Contexts)
	if invalid := invalidContexts(jobs, contexts); len(invalid) > 0 {
		_, _ = io.WriteString(stderr, "pull_request job names available to require:\n")
		for _, name := range sortedJobNames(jobs) {
			_, _ = fmt.Fprintf(stderr, "  - %s\n", name)
		}
		return errors.Join(invalid...)
	}

	admin, err := hasAdmin(gh, repo)
	if err != nil || !admin {
		return fmt.Errorf("the current token does not have admin permission on %s", repo)
	}

	fallback := resolveFallbackPath(root, opts.FallbackFile)
	useFallback := fallbackExists(fallback)
	if useFallback {
		_, _ = fmt.Fprintf(stderr,
			"warning: %s exists; installing the documented no-merge-queue fallback\n",
			displayPath(root, fallback))
	}

	name := resolveRulesetName(opts.Ruleset)
	payload, err := rulesetPayloadJSON(name, contexts, !useFallback)
	if err != nil {
		return fmt.Errorf("build the ruleset payload: %w", err)
	}

	summaries, err := listRulesets(gh, repo)
	if err != nil {
		return fmt.Errorf("cannot list rulesets for %s (is the token allowed to administer the repository?)", repo)
	}
	id := rulesetID(summaries, name)

	method, path, action := "POST", "repos/"+repo+"/rulesets", "created"
	if id != 0 {
		method = "PUT"
		path = fmt.Sprintf("repos/%s/rulesets/%d", repo, id)
		action = "updated"
	}

	if _, err := gh.Run([]string{"api", "--method", method, path, "--input", "-"}, payload); err != nil {
		if !useFallback {
			_, _ = io.WriteString(stderr,
				"\nIf this repository cannot use a merge queue, write the trade-off down in\n"+
					".github/branch-protection-fallback and re-run. The check command then expects strict:false.\n")
		}
		return fmt.Errorf("GitHub rejected the ruleset for %s: %w", repo, err)
	}

	_, _ = fmt.Fprintf(stdout, "%s ruleset %s on %s with contexts: %s\n",
		action, name, repo, strings.Join(contexts, ", "))
	return nil
}

// hasAdmin reports whether the current token may administer the repository.
func hasAdmin(gh GitHub, repo string) (bool, error) {
	out, err := gh.Run([]string{"api", "repos/" + repo}, nil)
	if err != nil {
		return false, err
	}

	var repository struct {
		Permissions struct {
			Admin bool `json:"admin"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(out, &repository); err != nil {
		return false, err
	}
	return repository.Permissions.Admin, nil
}

// payloadStatusCheck is one required status check in the payload.
type payloadStatusCheck struct {
	Context string `json:"context"`
}

// payloadPullRequestParameters is the pull_request rule's parameter block.
type payloadPullRequestParameters struct {
	RequiredApprovingReviewCount   int  `json:"required_approving_review_count"`
	DismissStaleReviewsOnPush      bool `json:"dismiss_stale_reviews_on_push"`
	RequireCodeOwnerReview         bool `json:"require_code_owner_review"`
	RequireLastPushApproval        bool `json:"require_last_push_approval"`
	RequiredReviewThreadResolution bool `json:"required_review_thread_resolution"`
}

// payloadStatusCheckParameters is the required_status_checks rule's parameter
// block.
type payloadStatusCheckParameters struct {
	StrictRequiredStatusChecksPolicy bool                 `json:"strict_required_status_checks_policy"`
	RequiredStatusChecks             []payloadStatusCheck `json:"required_status_checks"`
}

// payloadMergeQueueParameters is the merge_queue rule's parameter block.
type payloadMergeQueueParameters struct {
	MergeMethod                  string `json:"merge_method"`
	GroupingStrategy             string `json:"grouping_strategy"`
	MaxEntriesToBuild            int    `json:"max_entries_to_build"`
	MinEntriesToMerge            int    `json:"min_entries_to_merge"`
	MaxEntriesToMerge            int    `json:"max_entries_to_merge"`
	MinEntriesToMergeWaitMinutes int    `json:"min_entries_to_merge_wait_minutes"`
	CheckResponseTimeoutMinutes  int    `json:"check_response_timeout_minutes"`
}

// payloadRule is one rule of the ruleset payload.
type payloadRule struct {
	Type       string `json:"type"`
	Parameters any    `json:"parameters,omitempty"`
}

// payloadConditions scopes the ruleset to the default branch.
type payloadConditions struct {
	RefName struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"ref_name"`
}

// rulesetPayload is the body of the ruleset PUT or POST.
type rulesetPayload struct {
	Name         string            `json:"name"`
	Target       string            `json:"target"`
	Enforcement  string            `json:"enforcement"`
	BypassActors []json.RawMessage `json:"bypass_actors"`
	Conditions   payloadConditions `json:"conditions"`
	Rules        []payloadRule     `json:"rules"`
}

// rulesetPayloadJSON builds the ruleset body. mergeQueue is false only for a
// repository that documented why it cannot use one; the strict status check
// policy depends on the merge queue, so it is disabled in the same case.
func rulesetPayloadJSON(name string, contexts []string, mergeQueue bool) ([]byte, error) {
	checks := make([]payloadStatusCheck, 0, len(contexts))
	for _, context := range contexts {
		checks = append(checks, payloadStatusCheck{Context: context})
	}

	payload := rulesetPayload{
		Name:         name,
		Target:       "branch",
		Enforcement:  "active",
		BypassActors: []json.RawMessage{},
		Rules: []payloadRule{
			{Type: "deletion"},
			{Type: "non_fast_forward"},
			{Type: "required_linear_history"},
			{
				Type: "pull_request",
				Parameters: payloadPullRequestParameters{
					RequiredApprovingReviewCount:   0,
					DismissStaleReviewsOnPush:      true,
					RequireCodeOwnerReview:         false,
					RequireLastPushApproval:        false,
					RequiredReviewThreadResolution: true,
				},
			},
			{
				Type: "required_status_checks",
				Parameters: payloadStatusCheckParameters{
					StrictRequiredStatusChecksPolicy: mergeQueue,
					RequiredStatusChecks:             checks,
				},
			},
		},
	}
	payload.Conditions.RefName.Include = []string{"~DEFAULT_BRANCH"}
	payload.Conditions.RefName.Exclude = []string{}

	if mergeQueue {
		payload.Rules = append(payload.Rules, payloadRule{
			Type: "merge_queue",
			Parameters: payloadMergeQueueParameters{
				MergeMethod:                  "SQUASH",
				GroupingStrategy:             "ALLGREEN",
				MaxEntriesToBuild:            3,
				MinEntriesToMerge:            1,
				MaxEntriesToMerge:            1,
				MinEntriesToMergeWaitMinutes: 5,
				CheckResponseTimeoutMinutes:  60,
			},
		})
	}

	return json.Marshal(payload)
}
