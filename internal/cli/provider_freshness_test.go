package cli_test

import (
	"strings"
	"testing"
)

// trinoReleases is one page of the releases API: 487, 486 and 485 are stable,
// the newest two entries are a draft and a prerelease and must be ignored.
const trinoReleases = `[[
  {"tag_name": "v487", "draft": false, "prerelease": false, "published_at": "2026-11-01T00:00:00Z"},
  {"tag_name": "v486", "draft": false, "prerelease": false, "published_at": "2026-10-01T00:00:00Z"},
  {"tag_name": "v485", "draft": false, "prerelease": false, "published_at": "2026-09-01T00:00:00Z"},
  {"tag_name": "v488", "draft": true, "prerelease": false, "published_at": "2026-11-10T00:00:00Z"},
  {"tag_name": "v489", "draft": false, "prerelease": true, "published_at": "2026-11-12T00:00:00Z"}
]]`

// freshnessInputs substitutes the freshness workflow's inputs for a trino-like
// caller: tags carry a `v` prefix and the window is the default five.
func freshnessInputs(file string) map[string]string {
	return map[string]string{
		"inputs.versions-file":     file,
		"inputs.window":            "5",
		"inputs.strip-v && 1 || 0": "1",
		"inputs.strip-suffix":      "",
		"inputs.provider-display":  "Trino",
		"inputs.upstream":          "trinodb/trino",
		"inputs.issue-label":       "version-matrix",
	}
}

func runFreshness(t *testing.T, file string, stub ghStub) stepRun {
	t.Helper()

	return runFreshnessWith(t, file, stub, nil)
}

// runFreshnessWith is runFreshness with input overrides, for the callers whose
// tags carry something other than a `v` prefix.
func runFreshnessWith(t *testing.T, file string, stub ghStub, overrides map[string]string) stepRun {
	t.Helper()

	inputs := freshnessInputs(file)
	for key, value := range overrides {
		inputs[key] = value
	}

	return runStep(
		t,
		"provider-freshness.yml",
		"Compare the matrix with the last stable releases",
		inputs,
		map[string]string{"REPO": "caberdo/terraform-provider-trino", "GH_TOKEN": "token"},
		stub,
	)
}

func hasCall(calls []string, prefix string) bool {
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

// TestProviderFreshnessNamesTheNewestMissingRelease is the case the canary
// relies on: the matrix stops at 486 while upstream published 487, so `behind`
// is true and `newest-missing` names 487 after `strip-v` normalisation.
func TestProviderFreshnessNamesTheNewestMissingRelease(t *testing.T) {
	run := runFreshness(t, versionsFile(t, "480", "485", "486"), ghStub{releases: trinoReleases})

	if got := run.outputs["behind"]; got != "true" {
		t.Errorf("behind = %q, want %q (output %q; log %s)", got, "true", run.outputs, run.log)
	}
	if got := run.outputs["newest-missing"]; got != "487" {
		t.Errorf("newest-missing = %q, want %q (log %s)", got, "487", run.log)
	}
	if !hasCall(run.calls, "issue create") {
		t.Errorf("a stale matrix did not open an issue; gh calls = %q", run.calls)
	}
}

// TestProviderFreshnessClearsWhenTheMatrixIsComplete covers the auto-close
// precondition: a file that is exactly floor plus the stable window reports
// behind=false and an empty newest-missing, so no issue is opened.
func TestProviderFreshnessClearsWhenTheMatrixIsComplete(t *testing.T) {
	run := runFreshness(t, versionsFile(t, "480", "485", "486", "487"), ghStub{releases: trinoReleases})

	if got := run.outputs["behind"]; got != "false" {
		t.Errorf("behind = %q, want %q (log %s)", got, "false", run.log)
	}
	value, ok := run.outputs["newest-missing"]
	if !ok {
		t.Fatalf("newest-missing was not emitted; outputs = %q", run.outputs)
	}
	if value != "" {
		t.Errorf("newest-missing = %q, want empty", value)
	}
	if hasCall(run.calls, "issue create") || hasCall(run.calls, "issue edit") {
		t.Errorf("a complete matrix still wrote an issue; gh calls = %q", run.calls)
	}
}

// TestProviderFreshnessIgnoresTheLatestMarker pins rustfs's rolling entry: the
// file ends in `latest`, which is not a version and must not make the matrix
// look stale or be compared with a release tag.
func TestProviderFreshnessIgnoresTheLatestMarker(t *testing.T) {
	run := runFreshness(t, versionsFile(t, "485", "486", "487", "latest"), ghStub{releases: trinoReleases})

	if got := run.outputs["behind"]; got != "false" {
		t.Errorf("behind = %q, want %q; `latest` was treated as a pinned version (log %s)", got, "false", run.log)
	}
	if got := run.outputs["newest-missing"]; got != "" {
		t.Errorf("newest-missing = %q, want empty; `latest` was treated as a version", got)
	}
}

// TestProviderFreshnessNeverOffersAReleaseBelowTheNewestPin pins Review Focus
// #1: a matrix whose floor is newer than every upstream release is stale, but
// it must not report one of those older releases as the canary's version.
func TestProviderFreshnessNeverOffersAReleaseBelowTheNewestPin(t *testing.T) {
	run := runFreshness(t, versionsFile(t, "490"), ghStub{releases: trinoReleases})

	if got := run.outputs["behind"]; got != "true" {
		t.Errorf("behind = %q, want %q (log %s)", got, "true", run.log)
	}
	if got := run.outputs["newest-missing"]; got != "" {
		t.Errorf("newest-missing = %q, want empty; the canary would test a release below the floor", got)
	}
}

// openmetadataReleases is one page of the releases API with OpenMetadata's tag
// shape: a stable release ends in `-release` while the versions file holds the
// bare version, so neither the comparison nor the canary can match without the
// suffix being removed. The rc is a prerelease and must be ignored.
const openmetadataReleases = `[[
  {"tag_name": "2.1.0-rc2-release", "draft": false, "prerelease": true, "published_at": "2026-11-12T00:00:00Z"},
  {"tag_name": "2.0.8-release", "draft": false, "prerelease": false, "published_at": "2026-11-01T00:00:00Z"},
  {"tag_name": "2.0.7-release", "draft": false, "prerelease": false, "published_at": "2026-10-01T00:00:00Z"}
]]`

// openmetadataInputs is the caller's normalisation: no `v` prefix, a literal
// `-release` suffix.
func openmetadataInputs() map[string]string {
	return map[string]string{
		"inputs.provider-display":  "OpenMetadata",
		"inputs.upstream":          "open-metadata/OpenMetadata",
		"inputs.strip-v && 1 || 0": "0",
		"inputs.strip-suffix":      "-release",
	}
}

// TestProviderFreshnessStripsTheReleaseSuffix pins the openmetadata case on both
// halves of the report: the matrix that covers the normalised window is not
// stale, and a stale matrix names the release without the suffix, because a
// canary cannot pull an image tagged `2.0.8-release`.
func TestProviderFreshnessStripsTheReleaseSuffix(t *testing.T) {
	stale := runFreshnessWith(t, versionsFile(t, "1.13.6"), ghStub{releases: openmetadataReleases}, openmetadataInputs())
	if got := stale.outputs["behind"]; got != "true" {
		t.Errorf("behind = %q, want %q (log %s)", got, "true", stale.log)
	}
	if got := stale.outputs["newest-missing"]; got != "2.0.8" {
		t.Errorf("newest-missing = %q, want %q; the suffix was not stripped (log %s)", got, "2.0.8", stale.log)
	}

	covered := runFreshnessWith(t, versionsFile(t, "1.13.6", "2.0.7", "2.0.8"), ghStub{releases: openmetadataReleases}, openmetadataInputs())
	if got := covered.outputs["behind"]; got != "false" {
		t.Errorf("behind = %q, want %q; the normalised window was not recognised (log %s)", got, "false", covered.log)
	}
	if got := covered.outputs["newest-missing"]; got != "" {
		t.Errorf("newest-missing = %q, want empty (log %s)", got, covered.log)
	}
}

// TestProviderFreshnessIgnoresALiteralReleaseSuffix pins `strip-suffix` as a
// literal end-of-tag match rather than a regex or a mid-string substitution.
func TestProviderFreshnessIgnoresALiteralReleaseSuffix(t *testing.T) {
	releases := `[[
  {"tag_name": "2.0.9-release-notes", "draft": false, "prerelease": false, "published_at": "2026-11-05T00:00:00Z"},
  {"tag_name": "2.0.8-release", "draft": false, "prerelease": false, "published_at": "2026-11-01T00:00:00Z"}
]]`
	run := runFreshnessWith(t, versionsFile(t, "1.13.6"), ghStub{releases: releases}, openmetadataInputs())

	if got := run.outputs["newest-missing"]; got != "2.0.9-release-notes" {
		t.Errorf("newest-missing = %q, want %q; a suffix inside the tag must be left alone (log %s)", got, "2.0.9-release-notes", run.log)
	}
}

// TestProviderFreshnessClosesTheClearedIssue pins the new auto-close path: with
// no finding and an open labelled issue, it comments once and closes it.
func TestProviderFreshnessClosesTheClearedIssue(t *testing.T) {
	run := runFreshness(t, versionsFile(t, "480", "485", "486", "487"), ghStub{
		releases: trinoReleases,
		issue:    "12",
	})

	if !hasCall(run.calls, "issue comment 12") {
		t.Errorf("the cleared issue was not commented on; gh calls = %q", run.calls)
	}
	if !hasCall(run.calls, "issue close 12") {
		t.Errorf("the cleared issue was not closed; gh calls = %q", run.calls)
	}
	if hasCall(run.calls, "issue create") {
		t.Errorf("a complete matrix opened another issue; gh calls = %q", run.calls)
	}
}
