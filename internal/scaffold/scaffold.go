// Package scaffold renders the canonical repository scaffold and compares a
// repository against it.
//
// The scaffold is the shape every caberdo provider repository shares: the
// community files, the editor, ignore and lint configuration, and the workflow
// callers. Render substitutes the provider-specific values; Check compares a
// repository against the rendered files that must be identical; Fix writes the
// rendered enforced files over a drifting repository.
//
// Check is check-only by design: it never writes into the repository it reads,
// so a drift run cannot turn a review finding into a silent edit. Fix is the
// separate, explicit repair: it writes only the enforced set and never touches
// README prose or a provider's own files.
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
	// templateSuffix marks a scaffold file that cannot be stored under its
	// target name because the Go tool would try to compile it. The renderer
	// strips the suffix, so main.go.tmpl becomes main.go in a repository.
	templateSuffix = ".tmpl"
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

// knownPlaceholders is the complete set a template may use. An unknown name is
// a configuration error even when a value for it was supplied: a typo in the
// template must not become a literal in a generated repository.
var knownPlaceholders = map[string]struct{}{
	"PROVIDER":           {},
	"PRODUCT":            {},
	"MODULE":             {},
	"REPO":               {},
	"REGISTRY_NAMESPACE": {},
	"YEAR":               {},
	"COPYRIGHT":          {},
	"KIT_VERSION":        {},
	"PACKAGES":           {},
	"VERSIONS_FILE":      {},
	"UPSTREAM_URL":       {},
	"VERSION_ENV":        {},
	"EXPECT_VERSION_ENV": {},
	"LICENSE":            {},
}

// Allowance records a deliberate deviation: a path the check skips and the
// reason it skips it. The reason is required, so the deviation is recorded
// rather than silent. Used is set by Check in Report.Allowed and ignored in
// Options.Allow.
type Allowance struct {
	Path   string
	Reason string
	Used   bool
}

// ErrFixIO marks a failure to inspect or replace a file while Fix is applying
// its plan. It is an I/O refusal, not a configuration problem: the caller
// should report the partial write list and exit 1, not 2.
var ErrFixIO = errors.New("fix scaffold file")

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
	// Matched is the number of checked files whose content equals the
	// scaffold. An allowed deviation is not a match.
	Matched int
	// Problems lists the deviations that are not allowed, in canonical order.
	Problems []Problem
	// Allowed lists every exemption in checked-file order. Used reports
	// whether it suppressed a deviation, so an unused exemption is visible
	// rather than silently dropped.
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
		target := strings.TrimSuffix(p, templateSuffix)
		name, err := substitute(target, values)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		body, err := substitute(string(content), values)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		// A malformed token such as {{PRODUCT-NAME}}, {{ PRODUCT }} or {{}}
		// survives substitution because it is not a NAME-shaped placeholder.
		// Rendering it would write the token into the repository, so it is a
		// configuration error like a missing value.
		if leftover, ok := leftoverPlaceholder(name); ok {
			return fmt.Errorf("%s: unrendered placeholder %q in the file name", p, leftover)
		}
		if leftover, ok := leftoverPlaceholder(body); ok {
			return fmt.Errorf("%s: unrendered placeholder %q", p, leftover)
		}
		rendered[name] = []byte(body)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("render scaffold: %w", err)
	}
	return rendered, nil
}

// substitute replaces every known placeholder in text. An unknown name and a
// known name without a value are collected separately, so one run names every
// problem instead of stopping at the first. A `${{ … }}` expression is Go
// template syntax in a workflow, not a scaffold placeholder, and is copied
// through untouched.
func substitute(text string, values map[string]string) (string, error) {
	missing := make(map[string]struct{})
	unknown := make(map[string]struct{})

	var rendered strings.Builder
	last := 0
	for _, match := range placeholderPattern.FindAllStringSubmatchIndex(text, -1) {
		start, end := match[0], match[1]
		if isTemplateExpression(text, start) {
			continue
		}
		name := text[match[2]:match[3]]
		value, hasValue := values[name]

		rendered.WriteString(text[last:start])
		switch {
		case !isKnown(name):
			unknown[name] = struct{}{}
			rendered.WriteString(text[start:end])
		case !hasValue || value == "":
			missing[name] = struct{}{}
			rendered.WriteString(text[start:end])
		default:
			rendered.WriteString(value)
		}
		last = end
	}
	rendered.WriteString(text[last:])

	if len(unknown) > 0 {
		return "", fmt.Errorf("unknown placeholder %s", quotedNames(unknown))
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("no value for placeholder %s", quotedNames(missing))
	}
	return rendered.String(), nil
}

// isTemplateExpression reports whether the "{{" at start is the body of a
// GitHub Actions `${{ … }}` expression.
func isTemplateExpression(text string, start int) bool {
	return start > 0 && text[start-1] == '$'
}

// isKnown reports whether name is in the scaffold's placeholder set.
func isKnown(name string) bool {
	_, ok := knownPlaceholders[name]
	return ok
}

// quotedNames renders a set of names as sorted, quoted, comma-separated text.
func quotedNames(names map[string]struct{}) string {
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	quoted := make([]string, 0, len(sorted))
	for _, name := range sorted {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	return strings.Join(quoted, ", ")
}

// leftoverPlaceholder returns the first `{{…}}` that is not a GitHub Actions
// `${{ … }}` expression, so a malformed or unclosed token cannot be written
// into a generated repository.
func leftoverPlaceholder(text string) (string, bool) {
	for i := 0; i+1 < len(text); i++ {
		if text[i] != '{' || text[i+1] != '{' || isTemplateExpression(text, i) {
			continue
		}
		if end := strings.Index(text[i:], "}}"); end >= 0 {
			return text[i : i+end+2], true
		}
		// Unclosed: report the token to the end of the line, or the last 40
		// bytes when the line is longer, so the error stays readable.
		end := strings.IndexByte(text[i:], '\n')
		if end < 0 {
			end = len(text) - i
		}
		if end > 40 {
			end = 40
		}
		return text[i : i+end], true
	}
	return "", false
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

	checked, content, err := enforcedFiles(root, rendered)
	if err != nil {
		return Report{}, err
	}

	allow, err := normalizeAllowances(opts.Allow, checked)
	if err != nil {
		return Report{}, err
	}

	report := Report{Checked: checked}
	allowedIndex := make(map[string]int, len(allow))
	for _, rel := range checked {
		if allowance, ok := allow[rel]; ok {
			allowedIndex[rel] = len(report.Allowed)
			report.Allowed = append(report.Allowed, allowance)
		}
	}

	for _, rel := range checked {
		problem, err := compare(root, rel, content[rel])
		if err != nil {
			return Report{}, err
		}
		if problem == nil {
			report.Matched++
			continue
		}
		if index, ok := allowedIndex[rel]; ok {
			report.Allowed[index].Used = true
			continue
		}
		report.Problems = append(report.Problems, *problem)
	}
	return report, nil
}

// Fix writes the canonical content of every enforced file that is missing or
// differs, and returns the repository-relative paths it wrote in check order.
//
// It honours the same allowances as Check: an allowed path is never written,
// so a deliberate deviation survives a mechanical repair. A rendered file
// outside the enforced set (README prose, AGENTS.md, provider-specific files)
// is never a candidate: Fix repairs the contract, it does not copy the tree.
//
// The caller is expected to re-run Check afterwards; Fix reports what it did,
// not whether the repository is now clean.
func Fix(root string, values map[string]string, opts Options) ([]string, error) {
	return fix(root, values, opts, true)
}

// FixPlan returns the paths Fix would write, in check order, without touching
// the repository. It is the dry run behind `tfkit scaffold fix --dry-run`.
func FixPlan(root string, values map[string]string, opts Options) ([]string, error) {
	return fix(root, values, opts, false)
}

// fix is Fix and FixPlan with the write suppressed for the plan. It shares
// rendering, enforcement and allowance validation with Check, so a path the
// check reports is exactly a path the repair targets.
func fix(root string, values map[string]string, opts Options, apply bool) ([]string, error) {
	rendered, err := renderFS(assets.FS, values)
	if err != nil {
		return nil, err
	}

	checked, content, err := enforcedFiles(root, rendered)
	if err != nil {
		return nil, err
	}

	allow, err := normalizeAllowances(opts.Allow, checked)
	if err != nil {
		return nil, err
	}

	enforcedSet := make(map[string]struct{}, len(checked))
	for _, rel := range checked {
		enforcedSet[rel] = struct{}{}
	}

	var written []string
	for _, rel := range checked {
		if _, allowed := allow[rel]; allowed {
			continue
		}
		// A rendered path outside the enforced set must never be written. The
		// loop already ranges over the enforced list; the guard keeps a future
		// refactor from turning Fix into a tree copy.
		if !contains(enforcedSet, rel) {
			return written, fmt.Errorf("refuse to write %s: not an enforced path", rel)
		}

		problem, err := compare(root, rel, content[rel])
		if err != nil {
			return written, fmt.Errorf("%w: %v", ErrFixIO, err)
		}
		if problem == nil {
			continue
		}
		if apply {
			if err := writeCanonical(root, rel, content[rel]); err != nil {
				return written, fmt.Errorf("%w: %v", ErrFixIO, err)
			}
		}
		written = append(written, rel)
	}
	return written, nil
}

// writeCanonical replaces one enforced file. A path that exists but is not a
// regular file is refused rather than followed or replaced: a symlink may point
// outside the repository, and a directory is not a drifting file.
func writeCanonical(root, rel string, content []byte) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return fmt.Errorf("refuse to overwrite %s: not a regular file", rel)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s: %w", rel, err)
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	return nil
}

// enforcedFiles returns the paths the check compares and the content each path
// must carry, in report order.
//
// The issue templates follow the repository's existing form: a repository that
// already ships .yml issue forms is checked and repaired as .yml, with the
// scaffold's rendered template content, instead of gaining competing .md
// siblings. A repository with the scaffold's .md forms stays on .md.
func enforcedFiles(root string, rendered map[string][]byte) ([]string, map[string][]byte, error) {
	content := make(map[string][]byte, len(enforced))
	checked := make([]string, 0, len(enforced))
	for _, rel := range enforced {
		body, ok := rendered[rel]
		if !ok {
			return nil, nil, fmt.Errorf("scaffold has no %q", rel)
		}
		checked = append(checked, rel)
		content[rel] = body
	}

	templates := make([]string, 0)
	for p := range rendered {
		if strings.HasPrefix(p, issueTemplateDir+"/") {
			templates = append(templates, p)
		}
	}
	sort.Strings(templates)
	if len(templates) == 0 {
		return nil, nil, fmt.Errorf("scaffold has no %s/* files", issueTemplateDir)
	}

	extension, err := issueTemplateExtension(root)
	if err != nil {
		return nil, nil, err
	}
	for _, template := range templates {
		target := strings.TrimSuffix(template, ".md") + extension
		checked = append(checked, target)
		content[target] = rendered[template]
	}
	return checked, content, nil
}

// issueTemplateExtension picks the form the repository already uses. Only .yml
// is recognised: the two shapes in the provider family are the scaffold's .md
// template and the GitHub issue form, whose files are .yml.
func issueTemplateExtension(root string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(issueTemplateDir), "*.yml"))
	if err != nil {
		return "", fmt.Errorf("list %s forms: %w", issueTemplateDir, err)
	}
	for _, match := range matches {
		info, err := os.Lstat(match)
		if err == nil && info.Mode().IsRegular() {
			return ".yml", nil
		}
	}
	return ".md", nil
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

// compare reads one enforced path. A path that is absent, a symlink or not a
// regular file is missing: comparison never follows a link out of the
// repository, and writing a file through one would overwrite something the
// checker did not inspect.
func compare(root, rel string, want []byte) (*Problem, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
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
