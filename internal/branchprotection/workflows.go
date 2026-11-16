// Package branchprotection audits the branch protection a provider repository
// needs for its required status checks to mean anything.
package branchprotection

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Job is one job of a workflow, as GitHub reports it in a check run name.
//
// Name is what GitHub shows: the job's display name when it has one and its
// key otherwise. Key is the job id in the workflow file. Composed jobs, the
// jobs of a reusable workflow called by another workflow, carry
// "<caller key> / <called name>" in Name.
type Job struct {
	// Name is the check run name GitHub reports for the job.
	Name string
	// Key is the job id as written in the workflow file.
	Key string
	// HasIf reports whether the job is guarded by an if: expression, which lets
	// GitHub skip it and leave its required check pending.
	HasIf bool
	// HasMatrix reports whether the job fans out over a strategy matrix, which
	// makes its check name unstable across runs.
	HasMatrix bool
	// Remote reports a reusable workflow caller whose called workflow could not
	// be fetched, so its composed job names are unknown.
	Remote bool
}

// workflowFetcher is the seam through which remote reusable workflows are
// fetched. *ghClient and the tests' fakeGitHub both satisfy it.
type workflowFetcher interface {
	Workflow(owner, repo, path, ref string) ([]byte, error)
}

// errUnresolved marks a remote reusable workflow the fetcher could not return.
var errUnresolved = errors.New("reusable workflow could not be resolved")

// Jobs returns the jobs of the workflow at path, expanding reusable workflow
// calls. A local "./file.yml" call is followed on disk. A remote call cannot be
// followed without a fetcher, so it yields one job with Remote set and the bare
// caller key as its name.
func Jobs(path string) ([]Job, error) {
	return jobsInFile(path, nil, map[string]bool{})
}

// PullRequestJobs returns the jobs of every workflow under
// root/.github/workflows that runs on pull_request, expanding local reusable
// workflow calls. Workflows that only run on push, workflow_call or
// pull_request_target are ignored: they never contribute a required check to a
// pull request.
func PullRequestJobs(root string) ([]Job, error) {
	return pullRequestJobs(root, nil)
}

// pullRequestJobs is PullRequestJobs with the remote workflow fetcher injected.
// A nil fetcher leaves remote reusable workflow calls unresolved.
func pullRequestJobs(root string, gh workflowFetcher) ([]Job, error) {
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		return nil, fmt.Errorf("list workflows under %s: %w", root, err)
	}

	var jobs []Job
	for _, path := range paths {
		triggers, err := workflowTriggers(path)
		if err != nil {
			return nil, err
		}
		if !triggers["pull_request"] {
			continue
		}

		found, err := jobsInFile(path, gh, map[string]bool{})
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, found...)
	}
	return jobs, nil
}

// jobsInFile parses one workflow and returns its jobs, following reusable
// workflow calls. stack holds the files already being expanded on this branch,
// which turns a call cycle into an error instead of infinite recursion.
func jobsInFile(path string, gh workflowFetcher, stack map[string]bool) ([]Job, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve workflow %s: %w", path, err)
	}
	if stack[absolute] {
		return nil, fmt.Errorf("workflow %s calls itself through a reusable workflow", path)
	}
	stack[absolute] = true
	defer delete(stack, absolute)

	root, err := parseWorkflow(path)
	if err != nil {
		return nil, err
	}

	jobsMapping, err := jobsMapping(root)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	jobs := make([]Job, 0, len(jobsMapping.Content)/2)
	for i := 0; i+1 < len(jobsMapping.Content); i += 2 {
		keyNode, valueNode := jobsMapping.Content[i], jobsMapping.Content[i+1]

		job, err := jobFromNode(keyNode, valueNode)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		uses := mappingValue(valueNode, "uses")
		if uses == nil {
			jobs = append(jobs, job)
			continue
		}

		called, err := calledJobs(path, uses.Value, gh, stack)
		if errors.Is(err, errUnresolved) {
			job.Remote = true
			jobs = append(jobs, job)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: job %q: %w", path, keyNode.Value, err)
		}

		for _, callee := range called {
			callee.Name = keyNode.Value + " / " + callee.Name
			callee.Key = keyNode.Value + " / " + callee.Key
			// A conditional caller skips every job it delegates to.
			callee.HasIf = callee.HasIf || job.HasIf
			callee.HasMatrix = callee.HasMatrix || job.HasMatrix
			jobs = append(jobs, callee)
		}
	}
	return jobs, nil
}

// jobFromNode reads the display name, if: and strategy: of one job entry.
func jobFromNode(keyNode, valueNode *yaml.Node) (Job, error) {
	if valueNode.Kind != yaml.MappingNode {
		return Job{}, fmt.Errorf("job %q is not a mapping", keyNode.Value)
	}

	job := Job{Name: keyNode.Value, Key: keyNode.Value}
	if name := mappingValue(valueNode, "name"); name != nil && name.Value != "" {
		job.Name = name.Value
	}
	job.HasIf = mappingValue(valueNode, "if") != nil
	job.HasMatrix = mappingValue(valueNode, "strategy") != nil
	return job, nil
}

// calledJobs expands the "uses:" reference of a caller job. A local reference
// is read from disk relative to the calling workflow; a remote one is fetched
// through gh. It returns errUnresolved when a remote call cannot be fetched.
func calledJobs(callerPath, uses string, gh workflowFetcher, stack map[string]bool) ([]Job, error) {
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "../") {
		return jobsInFile(filepath.Join(filepath.Dir(callerPath), uses), gh, stack)
	}

	owner, repo, path, ref, err := splitRemoteCall(uses)
	if err != nil {
		return nil, err
	}
	if gh == nil {
		return nil, errUnresolved
	}
	content, err := gh.Workflow(owner, repo, path, ref)
	if err != nil {
		return nil, errUnresolved
	}

	called, err := parseCalledWorkflow(content)
	if err != nil {
		return nil, fmt.Errorf("reusable workflow %s: %w", uses, err)
	}

	jobs := make([]Job, 0, len(called.Content)/2)
	for i := 0; i+1 < len(called.Content); i += 2 {
		job, err := jobFromNode(called.Content[i], called.Content[i+1])
		if err != nil {
			return nil, fmt.Errorf("reusable workflow %s: %w", uses, err)
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

// splitRemoteCall splits "owner/repo/path/to/workflow.yml@ref".
func splitRemoteCall(uses string) (owner, repo, path, ref string, err error) {
	spec, ref, found := strings.Cut(uses, "@")
	if !found || ref == "" {
		return "", "", "", "", fmt.Errorf("reusable workflow reference %q has no @ref", uses)
	}
	parts := strings.SplitN(spec, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", "", fmt.Errorf("reusable workflow reference %q is not owner/repo/path@ref", uses)
	}
	return parts[0], parts[1], parts[2], ref, nil
}

// parseCalledWorkflow decodes a fetched reusable workflow and returns its jobs
// mapping.
func parseCalledWorkflow(content []byte) (*yaml.Node, error) {
	root, err := decodeWorkflow(content)
	if err != nil {
		return nil, err
	}
	return jobsMapping(root)
}

// parseWorkflow reads and decodes the workflow at path.
func parseWorkflow(path string) (*yaml.Node, error) {
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read workflow %s: %w", path, err)
	}
	root, err := decodeWorkflow(content)
	if err != nil {
		return nil, fmt.Errorf("parse workflow %s: %w", path, err)
	}
	return root, nil
}

// decodeWorkflow returns the root mapping of a workflow document.
func decodeWorkflow(content []byte) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, err
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) == 0 {
		return nil, errors.New("workflow is empty")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("workflow is not a mapping")
	}
	return root, nil
}

// jobsMapping returns the jobs mapping of a decoded workflow.
func jobsMapping(root *yaml.Node) (*yaml.Node, error) {
	jobs := mappingValue(root, "jobs")
	if jobs == nil {
		return nil, errors.New("workflow has no jobs")
	}
	if jobs.Kind != yaml.MappingNode {
		return nil, errors.New("workflow jobs is not a mapping")
	}
	return jobs, nil
}

// workflowTriggers returns the events the workflow at path runs on.
// It matches keys by their scalar text, so an unquoted "on:" keeps reading as
// "on" rather than the boolean true that a map decoder produced.
func workflowTriggers(path string) (map[string]bool, error) {
	root, err := parseWorkflow(path)
	if err != nil {
		return nil, err
	}

	triggers := make(map[string]bool)
	on := mappingValue(root, "on")
	if on == nil {
		return triggers, nil
	}

	switch on.Kind {
	case yaml.ScalarNode:
		triggers[on.Value] = true
	case yaml.SequenceNode:
		for _, event := range on.Content {
			triggers[event.Value] = true
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content); i += 2 {
			triggers[on.Content[i].Value] = true
		}
	default:
		return nil, fmt.Errorf("%s: on: is not a trigger", path)
	}
	return triggers, nil
}

// mappingValue returns the value stored under key in a mapping node, or nil.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// ValidateContexts reports the first required status check in contexts that
// branch protection cannot honour: a name no pull request workflow reports, a
// name shared by several jobs, a matrix job whose check name changes per
// combination, an if:-guarded job that may never run, or a reusable workflow
// caller whose called job names are unknown.
func ValidateContexts(jobs []Job, contexts []string) error {
	for _, context := range contexts {
		if err := validateContext(jobs, context); err != nil {
			return err
		}
	}
	return nil
}

func validateContext(jobs []Job, context string) error {
	var matches []Job
	for _, job := range jobs {
		if job.Name == context {
			matches = append(matches, job)
		}
	}

	if len(matches) == 0 {
		for _, job := range jobs {
			if job.Remote && (context == job.Key || strings.HasPrefix(context, job.Key+" / ")) {
				return fmt.Errorf(
					"required check %q belongs to the reusable workflow called by job %q, which could not be resolved",
					context, job.Key)
			}
		}
		return fmt.Errorf("%q is not a job name in the workflows that run on pull_request", context)
	}

	if len(matches) > 1 {
		return fmt.Errorf("required check %q matches %d jobs; give the jobs distinct names", context, len(matches))
	}

	job := matches[0]
	switch {
	case job.Remote:
		return fmt.Errorf(
			"required check %q names the reusable workflow caller %q, which could not be resolved; require its called job names instead",
			context, job.Key)
	case job.HasMatrix:
		return fmt.Errorf("required check %q is a matrix job, so its check names differ per combination", context)
	case job.HasIf:
		return fmt.Errorf("required check %q is guarded by if:, so GitHub may skip it and leave the check pending", context)
	}
	return nil
}
