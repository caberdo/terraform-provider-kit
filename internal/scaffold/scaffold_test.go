package scaffold_test

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-kit/internal/coverage"
	"github.com/caberdo/terraform-provider-kit/internal/scaffold"
	assets "github.com/caberdo/terraform-provider-kit/scaffold"
)

// scaffolds are the two real repositories used as fixtures. They were copied
// on 2026-11-16 from the working branches of terraform-provider-trino and
// terraform-provider-rustfs; only the files the checker reads, the issue
// templates (including rustfs's .yml ones, which must be replaced) and go.mod
// are included.
var fixtures = []string{"trino", "rustfs"}

const kitVersion = "v0.9.0"

// trinoValues are the values a trino repository supplies. They are kept
// separate from the fixtures so a test can prove that a rendered tree passes
// before the one-character change is made.
func trinoValues() map[string]string {
	return map[string]string{
		"PROVIDER":           "trino",
		"PRODUCT":            "Trino",
		"MODULE":             "github.com/caberdo/terraform-provider-trino",
		"REPO":               "caberdo/terraform-provider-trino",
		"REGISTRY_NAMESPACE": "caberdo",
		"YEAR":               "2026",
		"COPYRIGHT":          "Dennis de Jong",
		"KIT_VERSION":        kitVersion,
		"PACKAGES":           "./internal/...",
		"VERSIONS_FILE":      ".github/trino-versions.txt",
		"UPSTREAM_URL":       "trinodb/trino",
		"VERSION_ENV":        "TRINO_VERSION",
		"EXPECT_VERSION_ENV": "TRINO_EXPECT_VERSION",
		"LICENSE":            "MIT",
	}
}

// rustfsValues are the values a rustfs repository supplies.
func rustfsValues() map[string]string {
	return map[string]string{
		"PROVIDER":           "rustfs",
		"PRODUCT":            "RustFS",
		"MODULE":             "github.com/caberdo/terraform-provider-rustfs",
		"REPO":               "caberdo/terraform-provider-rustfs",
		"REGISTRY_NAMESPACE": "caberdo",
		"YEAR":               "2026",
		"COPYRIGHT":          "Dennis de Jong",
		"KIT_VERSION":        kitVersion,
		"PACKAGES":           "./...",
		"VERSIONS_FILE":      ".github/rustfs-versions.txt",
		"UPSTREAM_URL":       "rustfs/rustfs",
		"VERSION_ENV":        "RUSTFS_VERSION",
		"EXPECT_VERSION_ENV": "RUSTFS_EXPECT_VERSION",
		"LICENSE":            "Apache-2.0",
	}
}

// enforced is the exact list the checker must compare, in report order. It is
// spelled out here rather than read from the package so the contract cannot
// silently shrink.
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
	"changelog.d/README.md",
	"changelog.d/.gitkeep",
	".github/ISSUE_TEMPLATE/bug_report.md",
	".github/ISSUE_TEMPLATE/feature_request.md",
}

// scaffoldDir is the canonical tree, two levels up from this package's
// directory.
const scaffoldDir = "../../scaffold"

// copyFixture copies a testdata fixture into a fresh directory.
func copyFixture(t *testing.T, name string) string {
	t.Helper()

	src := filepath.Join("testdata", name)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture %s: %v", name, err)
	}
	return dst
}

// writeTree writes a rendered tree into dir.
func writeTree(t *testing.T, dir string, rendered map[string][]byte) {
	t.Helper()

	for p, content := range rendered {
		target := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", p, err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

// aligned copies a fixture and writes the rendered scaffold over it, which is
// what a repository that has completed the alignment would look like.
func aligned(t *testing.T, fixture string, values map[string]string) string {
	t.Helper()

	dir := copyFixture(t, fixture)
	rendered, err := scaffold.Render(scaffoldDir, values)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	writeTree(t, dir, rendered)
	return dir
}

// problemFor returns the problem for path, or nil.
func problemFor(report scaffold.Report, path string) *scaffold.Problem {
	for i := range report.Problems {
		if report.Problems[i].Path == path {
			return &report.Problems[i]
		}
	}
	return nil
}

// assertChecked pins the list of files the checker compares.
func assertChecked(t *testing.T, report scaffold.Report) {
	t.Helper()

	if len(report.Checked) != len(enforced) {
		t.Fatalf("Checked = %v, want %d paths", report.Checked, len(enforced))
	}
	for i, want := range enforced {
		if report.Checked[i] != want {
			t.Errorf("Checked[%d] = %q, want %q", i, report.Checked[i], want)
		}
	}
}

func TestRealRepositoriesDriftBeforeAlignment(t *testing.T) {
	for _, fixture := range fixtures {
		values := trinoValues()
		if fixture == "rustfs" {
			values = rustfsValues()
		}

		dir := copyFixture(t, fixture)
		report, err := scaffold.Check(dir, values, scaffold.Options{})
		if err != nil {
			t.Fatalf("%s: Check: %v", fixture, err)
		}
		if report.OK() {
			t.Errorf("%s: unaligned fixture passed; the fixture no longer proves drift is caught", fixture)
		}
		if len(report.Problems) == 0 {
			t.Errorf("%s: no problems reported", fixture)
		}
	}
}

func TestFullyAlignedRepositoriesPass(t *testing.T) {
	for _, fixture := range fixtures {
		values := trinoValues()
		if fixture == "rustfs" {
			values = rustfsValues()
		}

		dir := aligned(t, fixture, values)
		report, err := scaffold.Check(dir, values, scaffold.Options{})
		if err != nil {
			t.Fatalf("%s: Check: %v", fixture, err)
		}
		if !report.OK() {
			t.Errorf("%s: aligned repository did not pass: %v", fixture, report.Problems)
		}
		assertChecked(t, report)
		if len(report.Allowed) != 0 {
			t.Errorf("%s: Allowed = %v, want none", fixture, report.Allowed)
		}
	}
}

func TestCheckNamesTheFirstDifferingLine(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())

	path := filepath.Join(dir, ".editorconfig")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .editorconfig: %v", err)
	}
	changed := strings.Replace(string(content), "charset = utf-8", "charset = utf-9", 1)
	if changed == string(content) {
		t.Fatal("fixture .editorconfig no longer contains 'charset = utf-8'")
	}
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatalf("write .editorconfig: %v", err)
	}

	report, err := scaffold.Check(dir, trinoValues(), scaffold.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	problem := problemFor(report, ".editorconfig")
	if problem == nil {
		t.Fatalf("Problems = %v, want a problem for .editorconfig", report.Problems)
	}
	if problem.Kind != scaffold.Differs {
		t.Errorf("Kind = %v, want Differs", problem.Kind)
	}
	if problem.Line != 4 {
		t.Errorf("Line = %d, want 4", problem.Line)
	}
	if !strings.Contains(problem.Want, "utf-8") {
		t.Errorf("Want = %q, want the canonical line", problem.Want)
	}
	if !strings.Contains(problem.Got, "utf-9") {
		t.Errorf("Got = %q, want the repository's line", problem.Got)
	}
}

func TestCheckReportsAMissingFile(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())
	if err := os.Remove(filepath.Join(dir, "GOVERNANCE.md")); err != nil {
		t.Fatalf("remove GOVERNANCE.md: %v", err)
	}

	report, err := scaffold.Check(dir, trinoValues(), scaffold.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	problem := problemFor(report, "GOVERNANCE.md")
	if problem == nil {
		t.Fatalf("Problems = %v, want a missing GOVERNANCE.md", report.Problems)
	}
	if problem.Kind != scaffold.Missing {
		t.Errorf("Kind = %v, want Missing", problem.Kind)
	}
}

func TestAllowSuppressesExactlyThatFile(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())

	for path, addition := range map[string]string{
		".editorconfig": "\n# local editor preference\n",
		".gitignore":    "\n# local ignore\n",
	} {
		full := filepath.Join(dir, path)
		content, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if err := os.WriteFile(full, append(content, []byte(addition)...), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	reason := "the maintainer's editor writes this section"
	report, err := scaffold.Check(dir, trinoValues(), scaffold.Options{
		Allow: []scaffold.Allowance{{Path: ".editorconfig", Reason: reason}},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if problem := problemFor(report, ".editorconfig"); problem != nil {
		t.Errorf(".editorconfig was reported despite the allowance: %+v", problem)
	}
	if problem := problemFor(report, ".gitignore"); problem == nil {
		t.Errorf("Problems = %v, want .gitignore still reported", report.Problems)
	}
	if len(report.Allowed) != 1 {
		t.Fatalf("Allowed = %v, want one entry", report.Allowed)
	}
	if report.Allowed[0].Path != ".editorconfig" || report.Allowed[0].Reason != reason {
		t.Errorf("Allowed = %+v, want the editorconfig allowance with its reason", report.Allowed[0])
	}
	if !report.Allowed[0].Used {
		t.Error("Allowed[0].Used = false, want true for an allowance that suppressed a deviation")
	}
}

func TestCheckReportsAnUnusedAllowance(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())

	report, err := scaffold.Check(dir, trinoValues(), scaffold.Options{
		Allow: []scaffold.Allowance{{Path: ".editorconfig", Reason: "kept in reserve"}},
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !report.OK() {
		t.Fatalf("Problems = %v, want none", report.Problems)
	}
	if report.Matched != len(report.Checked) {
		t.Errorf("Matched = %d, want %d", report.Matched, len(report.Checked))
	}
	if len(report.Allowed) != 1 {
		t.Fatalf("Allowed = %v, want the unused allowance echoed", report.Allowed)
	}
	if report.Allowed[0].Used {
		t.Error("Allowed[0].Used = true, want false for an allowance that suppressed nothing")
	}
}

func TestAllowRequiresAReason(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())

	_, err := scaffold.Check(dir, trinoValues(), scaffold.Options{
		Allow: []scaffold.Allowance{{Path: ".editorconfig"}},
	})
	if err == nil {
		t.Fatal("Check with an empty reason succeeded, want a configuration error")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("error = %q, want it to name the missing reason", err)
	}
}

func TestAllowRejectsAPathTheCheckDoesNotEnforce(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())

	_, err := scaffold.Check(dir, trinoValues(), scaffold.Options{
		Allow: []scaffold.Allowance{{Path: "README.md", Reason: "our README differs"}},
	})
	if err == nil {
		t.Fatal("Check with an unenforced allow path succeeded, want a configuration error")
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Errorf("error = %q, want it to name README.md", err)
	}
}

func TestCheckFailsWhenAPlaceholderHasNoValue(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())
	values := trinoValues()
	delete(values, "PRODUCT")

	_, err := scaffold.Check(dir, values, scaffold.Options{})
	if err == nil {
		t.Fatal("Check without a PRODUCT value succeeded, want a configuration error")
	}
	if !strings.Contains(err.Error(), "PRODUCT") {
		t.Errorf("error = %q, want it to name PRODUCT", err)
	}
}

func TestRenderSubstitutesContentsAndFileNames(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs", "{{PROVIDER}}"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "{{PROVIDER}}-{{PRODUCT}}.md"),
		[]byte("# {{PRODUCT}}\n\n{{MODULE}}\n"), 0o644); err != nil {
		t.Fatalf("write root file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "{{PROVIDER}}", "index.md"),
		[]byte("{{PRODUCT}} docs\n"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	rendered, err := scaffold.Render(dir, trinoValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := string(rendered["trino-Trino.md"]); got != "# Trino\n\ngithub.com/caberdo/terraform-provider-trino\n" {
		t.Errorf("rendered content = %q", got)
	}
	if got := string(rendered["docs/trino/index.md"]); got != "Trino docs\n" {
		t.Errorf("nested content = %q", got)
	}
}

func TestRenderFailsWhenAPlaceholderHasNoValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# {{PRODUCT}}\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	_, err := scaffold.Render(dir, map[string]string{})
	if err == nil {
		t.Fatal("Render without a PRODUCT value succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "PRODUCT") {
		t.Errorf("error = %q, want it to name PRODUCT", err)
	}
}

func TestRenderFailsWhenTheScaffoldIsUnreadable(t *testing.T) {
	_, err := scaffold.Render(filepath.Join(t.TempDir(), "missing"), trinoValues())
	if err == nil {
		t.Fatal("Render of a missing directory succeeded, want an error")
	}
}

func TestRenderSkipsTheEmbedDeclaration(t *testing.T) {
	rendered, err := scaffold.Render(scaffoldDir, trinoValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, ok := rendered["embed.go"]; ok {
		t.Error("Render returned embed.go, which is the embed declaration, not scaffold content")
	}
	for _, path := range []string{".github/workflows/test.yml", "LICENSE", "README.md"} {
		if _, ok := rendered[path]; !ok {
			t.Errorf("Render did not return %s", path)
		}
	}
}

// TestEmbeddedScaffoldMatchesTheDirectory proves the embedded tree Check uses
// carries every file Render sees in the checkout. A file added to scaffold/
// without extending the go:embed patterns would otherwise only surface as a
// confusing "scaffold has no ..." at runtime.
func TestEmbeddedScaffoldMatchesTheDirectory(t *testing.T) {
	rendered, err := scaffold.Render(scaffoldDir, trinoValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var embedded []string
	err = fs.WalkDir(assets.FS, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			embedded = append(embedded, strings.TrimSuffix(p, ".tmpl"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded scaffold: %v", err)
	}
	sort.Strings(embedded)

	onDisk := make([]string, 0, len(rendered))
	for p := range rendered {
		onDisk = append(onDisk, p)
	}
	sort.Strings(onDisk)

	if strings.Join(embedded, "\n") != strings.Join(onDisk, "\n") {
		t.Errorf("embedded scaffold files = %v, on disk = %v", embedded, onDisk)
	}
}

func TestRenderRejectsAnUnknownPlaceholder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("{{PRODUCTX}}\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	values := trinoValues()
	values["PRODUCTX"] = "a value for a name the scaffold does not know"

	_, err := scaffold.Render(dir, values)
	if err == nil {
		t.Fatal("Render with an unknown placeholder succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "unknown placeholder") || !strings.Contains(err.Error(), "PRODUCTX") {
		t.Errorf("error = %q, want it to name the unknown placeholder", err)
	}
}

func TestRenderRejectsMalformedPlaceholders(t *testing.T) {
	cases := map[string]string{
		"hyphen": "{{PRODUCT-NAME}}",
		"spaces": "{{ PRODUCT }}",
		"empty":  "{{}}",
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(token+"\n"), 0o644); err != nil {
				t.Fatalf("write README: %v", err)
			}

			_, err := scaffold.Render(dir, trinoValues())
			if err == nil {
				t.Fatalf("Render of %q succeeded, want an error", token)
			}
			if !strings.Contains(err.Error(), "unrendered placeholder") {
				t.Errorf("error = %q, want an unrendered-placeholder error", err)
			}
		})
	}
}

func TestRenderRejectsAMalformedPlaceholderInAFileName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "{{PRODUCT-NAME}}.md"), []byte("text\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := scaffold.Render(dir, trinoValues())
	if err == nil {
		t.Fatal("Render with a malformed placeholder in a file name succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "file name") {
		t.Errorf("error = %q, want it to name the file name", err)
	}
}

func TestRenderPreservesGoTemplateExpressions(t *testing.T) {
	dir := t.TempDir()
	content := "{{PRODUCT}} ${{ github.ref }} ${{github.sha}}\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	rendered, err := scaffold.Render(dir, trinoValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "Trino ${{ github.ref }} ${{github.sha}}\n"
	if got := string(rendered["README.md"]); got != want {
		t.Errorf("rendered = %q, want %q", got, want)
	}
}

func TestRenderSeparatesModuleFromRepositoryURL(t *testing.T) {
	values := trinoValues()
	values["MODULE"] = "github.com/gravitino/terraform-provider-gravitino"
	values["REPO"] = "caberdo/terraform-provider-gravitino"

	rendered, err := scaffold.Render(scaffoldDir, values)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	documents := string(rendered["NOTICE"]) + string(rendered["SUPPORT.md"]) + string(rendered["SECURITY.md"])
	if !strings.Contains(documents, "github.com/gravitino/terraform-provider-gravitino") {
		t.Error("the module path is missing from the rendered documents")
	}
	if strings.Contains(documents, "https://github.com/gravitino/terraform-provider-gravitino") {
		t.Error("a repository URL was built from the module path, which is not the repository")
	}
	if !strings.Contains(documents, "https://github.com/caberdo/terraform-provider-gravitino") {
		t.Error("the repository URL does not use the REPO value")
	}
}

func TestRenderStripsTheTemplateSuffix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go.tmpl"), []byte("package {{PROVIDER}}\n"), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	rendered, err := scaffold.Render(dir, trinoValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := string(rendered["main.go"]); got != "package trino\n" {
		t.Errorf("main.go = %q, want the rendered template", got)
	}
	if _, ok := rendered["main.go.tmpl"]; ok {
		t.Error("the .tmpl suffix survived rendering")
	}
}

func TestRenderedMainIsParsableAndCarriesTheAddress(t *testing.T) {
	for _, fixture := range fixtures {
		values := trinoValues()
		if fixture == "rustfs" {
			values = rustfsValues()
		}

		rendered, err := scaffold.Render(scaffoldDir, values)
		if err != nil {
			t.Fatalf("%s: Render: %v", fixture, err)
		}
		main, ok := rendered["main.go"]
		if !ok {
			t.Fatalf("%s: rendered tree has no main.go", fixture)
		}
		if _, err := format.Source(main); err != nil {
			t.Errorf("%s: main.go is not gofmt-parsable: %v", fixture, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "main.go", main, parser.ParseComments); err != nil {
			t.Errorf("%s: main.go does not parse: %v", fixture, err)
		}

		address := "registry.terraform.io/" + values["REGISTRY_NAMESPACE"] + "/" + values["PROVIDER"]
		constant := "const " + values["PROVIDER"] + "Address = \"" + address + "\""
		if !bytes.Contains(main, []byte(constant)) {
			t.Errorf("%s: main.go has no %s", fixture, constant)
		}
		for _, fragment := range []string{
			"//go:generate go run github.com/hashicorp/terraform-plugin-docs",
			"func run(args []string) error",
			"flag.NewFlagSet(",
			"errors.Is(err, flag.ErrHelp)",
			`var version = "dev"`,
			"-X main.version",
		} {
			if !bytes.Contains(main, []byte(fragment)) {
				t.Errorf("%s: main.go is missing %q", fixture, fragment)
			}
		}
	}
}

func TestRenderUsesTheLicenseValue(t *testing.T) {
	rendered, err := scaffold.Render(scaffoldDir, rustfsValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := string(rendered["SECURITY.md"]); !strings.Contains(got, "License: Apache-2.0") {
		t.Errorf("SECURITY.md does not state the supplied licence: %q", got)
	}
	if got := string(rendered["NOTICE"]); !strings.Contains(got, "licensed under Apache-2.0") {
		t.Errorf("NOTICE does not state the supplied licence: %q", got)
	}
	if got := string(rendered["CONTRIBUTING.md"]); !strings.Contains(got, "[Apache-2.0 License](LICENSE)") {
		t.Errorf("CONTRIBUTING.md does not state the supplied licence: %q", got)
	}
}

func TestRenderedReadmeMatchesTheRenderedBaseline(t *testing.T) {
	for _, fixture := range fixtures {
		values := trinoValues()
		if fixture == "rustfs" {
			values = rustfsValues()
		}

		rendered, err := scaffold.Render(scaffoldDir, values)
		if err != nil {
			t.Fatalf("%s: Render: %v", fixture, err)
		}
		baseline, err := coverage.ReadBaseline(bytes.NewReader(rendered[".github/coverage-baseline.txt"]))
		if err != nil {
			t.Fatalf("%s: ReadBaseline: %v", fixture, err)
		}
		if err := coverage.CheckReadme(rendered["README.md"], baseline, "make coverage-gate"); err != nil {
			t.Errorf("%s: rendered README disagrees with the rendered baseline: %v", fixture, err)
		}
	}
}

func TestCheckNeverWritesToTheRepository(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())

	path := filepath.Join(dir, ".editorconfig")
	if err := os.WriteFile(path, []byte("local\n"), 0o644); err != nil {
		t.Fatalf("write .editorconfig: %v", err)
	}
	if _, err := scaffold.Check(dir, trinoValues(), scaffold.Options{}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .editorconfig: %v", err)
	}
	if string(content) != "local\n" {
		t.Errorf(".editorconfig = %q after Check; the checker must not write", content)
	}
}

func TestCheckRejectsANonRegularEnforcedPath(t *testing.T) {
	dir := aligned(t, "trino", trinoValues())

	path := filepath.Join(dir, "SUPPORT.md")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove SUPPORT.md: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir SUPPORT.md: %v", err)
	}

	report, err := scaffold.Check(dir, trinoValues(), scaffold.Options{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	problem := problemFor(report, "SUPPORT.md")
	if problem == nil || problem.Kind != scaffold.Missing {
		t.Fatalf("problem = %+v, want a missing SUPPORT.md", problem)
	}
}

// TestReportOK pins the drift predicate the CLI maps to exit 1.
func TestReportOK(t *testing.T) {
	if !(scaffold.Report{}).OK() {
		t.Error("an empty report is not OK")
	}
	report := scaffold.Report{Problems: []scaffold.Problem{{Path: ".editorconfig", Kind: scaffold.Differs}}}
	if report.OK() {
		t.Error("a report with a problem is OK")
	}
}

// TestRenderRetiresTheVersionFileAndShipsTheFragmentDirectory pins the two
// release decisions: the version is the last tag, so no file carries it, and
// every repository has the changelog.d/ convention.
func TestRenderRetiresTheVersionFileAndShipsTheFragmentDirectory(t *testing.T) {
	rendered, err := scaffold.Render(scaffoldDir, trinoValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if _, ok := rendered["VERSION"]; ok {
		t.Error("rendered tree contains VERSION; the version is the last tag")
	}

	readme, ok := rendered["changelog.d/README.md"]
	if !ok {
		t.Fatal("rendered tree has no changelog.d/README.md")
	}
	for _, want := range []string{
		"<issue>-<kind>-<slug>.md",
		"breaking",
		"feature",
		"improvement",
		"bugfix",
		"security",
		"chore",
	} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("changelog.d/README.md does not mention %q", want)
		}
	}
	if _, ok := rendered["changelog.d/.gitkeep"]; !ok {
		t.Error("rendered tree has no changelog.d/.gitkeep")
	}
}

// TestRenderedCallersFollowTheReleaseConventions pins the callers the scaffold
// generates: the fragment job is on and no caller drains a version file.
func TestRenderedCallersFollowTheReleaseConventions(t *testing.T) {
	rendered, err := scaffold.Render(scaffoldDir, trinoValues())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	ci, ok := rendered[".github/workflows/test.yml"]
	if !ok {
		t.Fatal("rendered tree has no .github/workflows/test.yml")
	}
	if !strings.Contains(string(ci), "changelog-fragments: true") {
		t.Error("test.yml does not enable changelog-fragments")
	}

	release, ok := rendered[".github/workflows/release.yml"]
	if !ok {
		t.Fatal("rendered tree has no .github/workflows/release.yml")
	}
	if strings.Contains(string(release), "version-file:") {
		t.Error("release.yml still passes a version-file input")
	}
	if strings.Contains(string(release), "VERSION") {
		t.Error("release.yml still mentions a VERSION file")
	}
}
