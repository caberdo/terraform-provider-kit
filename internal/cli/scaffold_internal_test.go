package cli

import (
	"strings"
	"testing"
)

func TestParseRemoteAcceptsGitSpellings(t *testing.T) {
	cases := map[string]string{
		"https://github.com/caberdo/terraform-provider-trino.git": "caberdo/terraform-provider-trino",
		"https://github.com/caberdo/terraform-provider-trino":     "caberdo/terraform-provider-trino",
		"git@github.com:caberdo/terraform-provider-trino.git":     "caberdo/terraform-provider-trino",
		"ssh://git@github.com/caberdo/terraform-provider-trino":   "caberdo/terraform-provider-trino",
	}
	for remote, want := range cases {
		got, err := parseRemote(remote)
		if err != nil {
			t.Errorf("parseRemote(%q): %v", remote, err)
			continue
		}
		if got != want {
			t.Errorf("parseRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestParseRemoteRejectsWhatIsNotARepositoryAddress(t *testing.T) {
	for _, remote := range []string{
		"/tmp/local/repository",
		"https://github.com/caberdo",
		"github.com/caberdo/terraform-provider-trino",
	} {
		if got, err := parseRemote(remote); err == nil {
			t.Errorf("parseRemote(%q) = %q, want an error", remote, got)
		}
	}
}

func TestParseRemoteRequiresGitHub(t *testing.T) {
	for _, remote := range []string{
		"https://gitlab.com/caberdo/terraform-provider-trino.git",
		"git@gitlab.com:caberdo/terraform-provider-trino.git",
		"ssh://git@example.com/caberdo/terraform-provider-trino",
	} {
		got, err := parseRemote(remote)
		if err == nil {
			t.Errorf("parseRemote(%q) = %q, want a non-GitHub host to be rejected", remote, got)
			continue
		}
		if !strings.Contains(err.Error(), "github.com") {
			t.Errorf("parseRemote(%q) error = %q, want it to name github.com", remote, err)
		}
	}
}

func TestPackagePatternFromMakefile(t *testing.T) {
	cases := map[string]string{
		"go test -v -cover ./...\n": "./...",
		"go test -count=1 -skip '^TestAcc' -covermode=atomic -coverprofile=c.out ./internal/...\n": "./internal/...",
		"\t@go test -run '^TestAcc' ./pkg/...\n":                                                   "./pkg/...",
		"# go test ./commented-out\n":                                                              "",
		"build:\n\tgo build ./...\n":                                                               "",
		"go test ./a/...\ngo test ./b/...\ngo test ./b/...\n":                                      "./b/...",
	}
	for content, want := range cases {
		if got := packagePatternFromMakefile(content); got != want {
			t.Errorf("packagePatternFromMakefile(%q) = %q, want %q", content, got, want)
		}
	}
}
