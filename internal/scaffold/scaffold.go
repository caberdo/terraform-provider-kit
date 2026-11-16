// Package scaffold renders the canonical repository scaffold and compares a
// repository against it.
//
// The scaffold is the shape every caberdo provider repository shares: the
// community files, the editor, ignore and lint configuration, and the workflow
// callers. Render substitutes the provider-specific values; Check compares a
// repository against the rendered files that must be identical.
//
// Check is check-only by design: it never writes into the repository it reads,
// so a drift run cannot turn a review finding into a silent edit. A `--fix`
// mode belongs to a later change.
package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	assets "github.com/caberdo/terraform-provider-kit/scaffold"
)

const (
	// embedFile is the Go declaration that embeds the scaffold into tfkit; it
	// is not repository content and the renderer skips it.
	embedFile = "embed.go"
	// issueTemplateDir holds the issue forms, which are discovered rather than
	// listed so adding a form to the scaffold adds it to the check.
	issueTemplateDir = ".github/ISSUE_TEMPLATE"
	// eofMarker names the side of a line comparison that ran out of lines.
	eofMarker = "(end of file)"
)

// enforced is the exact set of files whose content must match the scaffold,
// in report order. README prose, AGENTS.md and ARCHITECTURE.md content,
// provider-specific scripts, NOTICE, LICENSE and the workflow callers are
// deliberately not here; the scaffold ships them, but they carry facts a
// provider legitimately states differently.
var enforced = []string{
	".editorconfig",
	".gitignore",
	".golangci.yml",
	"CODE_OF_CONDUCT.md",
	"GOVERNANCE.md",
	"ROADMAP.md",
	"SUPPORT.md",
	"SECURITY.md",
	"CONTRIBUTING.md",
	".github/CODEOWNERS",
	".github/dependabot.yml",
	".github/PULL_REQUEST_TEMPLATE.md",
}

// placeholderPattern matches a {{NAME}} placeholder. The name is matched
// liberally so a lowercase typo becomes a missing value the user hears about,
// not a silent literal in the output.
var placeholderPattern = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)

// Allowance records a deliberate deviation: a path the check skips and the
// reason it skips it. The reason is required, so the deviation is recorded
// rather than silent.
type Allowance struct {
	Path   string
	Reason string
}

// Options configures Check.
type Options struct {
	// Allow lists the per-repository exemptions. Each path must name a file
	// the check enforces, and each reason must be non-empty.
	Allow []Allowance
}

// Kind distinguishes a missing file from one whose content differs.
type Kind int

const (
	// Missing means the repository has no file at the path.
	Missing Kind = iota
	// Differs means the file exists but its content is not canonical.
	Differs
)

// Problem is one deviation the check reports.
type Problem struct {
	// Path is the repository-relative path, with forward slashes.
	Path string
	// Kind says whether the file is missing or differs.
	Kind Kind
	// Line is the first differing line (1-based) for Kind Differs.
	Line int
	// Want is the canonical line and Got the repository's line.
	Want string
	Got  string
}

// Report is the outcome of a check.
type Report struct {
	// Checked lists every path the check compared, in canonical order.
	Checked []string
	// Problems lists the deviations that are not allowed, in canonical order.
	Problems []Problem
	// Allowed lists the allowances that suppressed a deviation.
	Allowed []Allowance
}

// OK reports whether the repository matches the scaffold.
func (r Report) OK() bool {
	return len(r.Problems) == 0
}

// Render walks the scaffold directory and returns repository-relative path to
// content, with every placeholder substituted in both file names and contents.
//
// A placeholder without a value is an error: rendering a half-substituted tree
// would silently turn a template token into repository content.
func Render(dir string, values map[string]string) (map[string][]byte, error) {
	return renderFS(os.DirFS(dir), values)
}

// renderFS is Render over any file system, so Check can use the embedded tree.
func renderFS(fsys fs.FS, values map[string]string) (map[string][]byte, error) {
	rendered := make(map[string][]byte)

	err := fs.WalkDir(fsys, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || p == embedFile {
			return nil
		}
		content, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		name, err := substitute(p, values)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		body, err := substitute(string(content), values)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		rendered[name] = []byte(body)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("render scaffold: %w", err)
	}
	return rendered, nil
}

// substitute replaces every placeholder in text, or reports every name without
// a value in one error so a first run names all of them.
func substitute(text string, values map[string]string) (string, error) {
	missing := make(map[string]struct{})
	rendered := placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
		name := match[2 : len(match)-2]
		value, ok := values[name]
		if !ok || value == "" {
			missing[name] = struct{}{}
			return match
		}
		return value
	})
	if len(missing) == 0 {
		return rendered, nil
	}

	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	return "", fmt.Errorf("no value for placeholder %s", strings.Join(quoted, ", "))
}

// Check compares root against the scaffold embedded in tfkit.
//
// It returns a report of the files that differ or are missing. A problem is the
// exit-1 class; an error means the scaffold is unusable or a value is missing,
// which is the exit-2 class. Check never writes to root.
func Check(root string, values map[string]string, opts Options) (Report, error) {
	rendered, err := renderFS(assets.FS, values)
	if err != nil {
		return Report{}, err
	}

	checked, err := enforcedPaths(rendered)
	if err != nil {
		return Report{}, err
	}

	allow, err := normalizeAllowances(opts.Allow, checked)
	if err != nil {
		return Report{}, err
	}

	report := Report{Checked: checked}
	for _, rel := range checked {
		problem, err := compare(root, rel, rendered[rel])
		if err != nil {
			return Report{}, err
		}
		if problem == nil {
			continue
		}
		if allowance, ok := allow[rel]; ok {
			report.Allowed = append(report.Allowed, allowance)
			continue
		}
		report.Problems = append(report.Problems, *problem)
	}
	return report, nil
}

// enforcedPaths returns the literal list plus the issue templates the scaffold
// carries, and fails when the scaffold is missing a file it claims to enforce.
func enforcedPaths(rendered map[string][]byte) ([]string, error) {
	checked := append([]string(nil), enforced...)

	templates := make([]string, 0)
	for p := range rendered {
		if strings.HasPrefix(p, issueTemplateDir+"/") {
			templates = append(templates, p)
		}
	}
	sort.Strings(templates)
	if len(templates) == 0 {
		return nil, fmt.Errorf("scaffold has no %s/* files", issueTemplateDir)
	}
	checked = append(checked, templates...)

	for _, rel := range checked {
		if _, ok := rendered[rel]; !ok {
			return nil, fmt.Errorf("scaffold has no %q", rel)
		}
	}
	return checked, nil
}

// normalizeAllowances validates the exemptions and indexes them by path. A path
// the check does not enforce is a configuration error: an exemption that
// silently applies to nothing is a typo, not a policy.
func normalizeAllowances(allow []Allowance, checked []string) (map[string]Allowance, error) {
	enforcedSet := make(map[string]struct{}, len(checked))
	for _, rel := range checked {
		enforcedSet[rel] = struct{}{}
	}

	normalized := make(map[string]Allowance, len(allow))
	for _, entry := range allow {
		rel, err := normalizePath(entry.Path)
		if err != nil {
			return nil, err
		}
		if !contains(enforcedSet, rel) {
			return nil, fmt.Errorf("allow path %q is not checked", entry.Path)
		}
		reason := strings.TrimSpace(entry.Reason)
		if reason == "" {
			return nil, fmt.Errorf("allow path %q needs a reason", rel)
		}
		if _, duplicate := normalized[rel]; duplicate {
			return nil, fmt.Errorf("allow path %q is listed twice", rel)
		}
		normalized[rel] = Allowance{Path: rel, Reason: reason}
	}
	return normalized, nil
}

// normalizePath turns a user-supplied allowance path into the repository-
// relative slash form the checker uses.
func normalizePath(value string) (string, error) {
	cleaned := strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = path.Clean(cleaned)
	if cleaned == "." || cleaned == "" || path.IsAbs(cleaned) ||
		cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("allow path %q is not a repository-relative path", value)
	}
	return cleaned, nil
}

// compare reads one enforced path. A path that is absent or not a regular file
// is missing; a read that fails after that keeps the underlying error.
func compare(root, rel string, want []byte) (*Problem, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return &Problem{Path: rel, Kind: Missing}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if !info.Mode().IsRegular() {
		return &Problem{Path: rel, Kind: Missing}, nil
	}

	got, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if bytes.Equal(want, got) {
		return nil, nil
	}

	line, wantLine, gotLine := firstDifference(want, got)
	return &Problem{Path: rel, Kind: Differs, Line: line, Want: wantLine, Got: gotLine}, nil
}

// firstDifference returns the 1-based number and the text of the first line
// that differs between want and got. A file that ends early reports the line
// after its last one, with eofMarker on the missing side; a file that differs
// only in its final newline reports the blank line the other file lacks.
func firstDifference(want, got []byte) (int, string, string) {
	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(string(got), "\n")

	shared := min(len(wantLines), len(gotLines))
	for i := 0; i < shared; i++ {
		if wantLines[i] != gotLines[i] {
			return i + 1, wantLines[i], gotLines[i]
		}
	}
	return shared + 1, lineAt(wantLines, shared), lineAt(gotLines, shared)
}

// lineAt returns the line at index, or eofMarker when the file ended.
func lineAt(lines []string, index int) string {
	if index < len(lines) {
		return lines[index]
	}
	return eofMarker
}

// contains reports whether set holds key.
func contains(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}
