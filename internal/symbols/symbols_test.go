package symbols

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFamilyFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ext  string
		want string
	}{
		{".go", "go"},
		{".py", "py"},
		{".pyi", "py"},
		{".js", "js"},
		{".jsx", "js"},
		{".mjs", "js"},
		{".cjs", "js"},
		{".ts", "js"},
		{".tsx", "js"},
		{".mts", "js"},
		{".cts", "js"},
		{".rs", "rs"},
		{".c", "c"},
		{".h", "c"},
		{".cc", "c"},
		{".cpp", "c"},
		{".cxx", "c"},
		{".hpp", "c"},
		{".hh", "c"},
		{".java", "jvm"},
		{".kt", "jvm"},
		{".kts", "jvm"},
		{".scala", "jvm"},
		{".cs", "jvm"},
		{".swift", "jvm"},
		{".rb", "rb"},
		{".sh", "sh"},
		{".bash", "sh"},
		{".zsh", "sh"},
		{".php", "php"},
		{".lua", "lua"},
		{".zig", "zig"},
		{".ex", "ex"},
		{".exs", "ex"},
		{".hs", "hs"},
		{".txt", ""},
		{"", ""},
	}

	for _, tc := range cases {
		if got := FamilyFor(tc.ext); got != tc.want {
			t.Errorf("FamilyFor(%q) = %q, want %q", tc.ext, got, tc.want)
		}
	}
}

func TestIdentRe(t *testing.T) {
	t.Parallel()

	valid := []string{"foo", "_bar", "Baz123", "$name", "CamelCase", "_"}
	for _, s := range valid {
		if !IdentRe.MatchString(s) {
			t.Errorf("IdentRe should match valid identifier: %q", s)
		}
	}

	invalid := []string{"123foo", "foo-bar", "foo bar", "foo.bar", "foo/bar", "foo+bar", ""}
	for _, s := range invalid {
		if IdentRe.MatchString(s) {
			t.Errorf("IdentRe should NOT match invalid identifier: %q", s)
		}
	}
}

func TestCompileDeclFamily(t *testing.T) {
	t.Parallel()

	if re := CompileDeclFamily("nonexistent", "Foo"); re != nil {
		t.Errorf("expected nil for nonexistent family, got %v", re)
	}

	if reInvalid := CompileDeclFamily("go", "[invalid-regex("); reInvalid != nil {
		t.Errorf("expected nil for invalid regex compilation, got %v", reInvalid)
	}

	reGo := CompileDeclFamily("go", "MyFunc")
	if reGo == nil {
		t.Fatal("expected compiled regex for go MyFunc")
	}
	if !reGo.MatchString("func MyFunc() {") {
		t.Error("expected go regex to match 'func MyFunc() {'")
	}
	if reGo.MatchString("func OtherFunc() {") {
		t.Error("expected go regex not to match other function")
	}
}

func TestDeclPatterns(t *testing.T) {
	t.Parallel()

	if pats := DeclPatterns("invalid-ident"); pats != nil {
		t.Errorf("expected nil patterns for invalid identifier, got %v", pats)
	}

	pats := DeclPatterns("HandleRequest")
	if pats == nil {
		t.Fatal("expected non-nil patterns for valid identifier")
	}
	if pats[".go"] == nil {
		t.Error("missing .go pattern")
	}
	if pats[".py"] == nil {
		t.Error("missing .py pattern")
	}
	if pats[".ts"] == nil {
		t.Error("missing .ts pattern")
	}

	if !pats[".go"].MatchString("func HandleRequest(w http.ResponseWriter) {") {
		t.Error("go pattern should match func declaration")
	}
	if !pats[".py"].MatchString("def HandleRequest():") {
		t.Error("py pattern should match def")
	}
}

func TestOutline(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// 1. Missing file error
	_, err := Outline(filepath.Join(dir, "missing.go"), "missing.go")
	if err == nil {
		t.Error("expected error for missing file")
	}

	// 2. Unsupported extension -> empty slice, nil error
	unsuppFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(unsuppFile, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	syms, err := Outline(unsuppFile, "test.txt")
	if err != nil || len(syms) != 0 {
		t.Errorf("expected empty outline for txt, got %v, err=%v", syms, err)
	}

	// 3. Markdown headings
	mdFile := filepath.Join(dir, "README.md")
	mdContent := "# Main Title\n\nSome text\n\n## Sub Section\n### Sub Sub Section\n"
	if err := os.WriteFile(mdFile, []byte(mdContent), 0644); err != nil {
		t.Fatal(err)
	}
	syms, err = Outline(mdFile, "README.md")
	if err != nil {
		t.Fatalf("Outline markdown error: %v", err)
	}
	if len(syms) != 3 {
		t.Fatalf("expected 3 headings, got %d", len(syms))
	}
	if syms[0].Name != "Main Title" || syms[0].Kind != "heading" || syms[0].Indent != 0 {
		t.Errorf("unexpected heading 0: %+v", syms[0])
	}
	if syms[1].Name != "Sub Section" || syms[1].Indent != 1 {
		t.Errorf("unexpected heading 1: %+v", syms[1])
	}

	// 4. Go code
	goFile := filepath.Join(dir, "sample.go")
	goContent := `package main

type Config struct {
	Port int
}

const DefaultPort = 8080
var GlobalServer *Server

func StartServer(cfg Config) error {
	return nil
}

func (c *Config) Validate() bool {
	return true
}
`
	if err := os.WriteFile(goFile, []byte(goContent), 0644); err != nil {
		t.Fatal(err)
	}
	syms, err = Outline(goFile, "sample.go")
	if err != nil {
		t.Fatalf("Outline go error: %v", err)
	}
	if len(syms) != 5 {
		t.Fatalf("expected 5 symbols in sample.go, got %d: %+v", len(syms), syms)
	}
	names := []string{syms[0].Name, syms[1].Name, syms[2].Name, syms[3].Name, syms[4].Name}
	expected := []string{"Config", "DefaultPort", "GlobalServer", "StartServer", "Validate"}
	for i := range names {
		if names[i] != expected[i] {
			t.Errorf("sym[%d] = %s, want %s", i, names[i], expected[i])
		}
	}

	// 5. Python code (with methods indented)
	pyFile := filepath.Join(dir, "sample.py")
	pyContent := `class Worker:
    def __init__(self):
        pass

    async def execute(self):
        pass

def global_helper():
    pass
`
	if err := os.WriteFile(pyFile, []byte(pyContent), 0644); err != nil {
		t.Fatal(err)
	}
	syms, err = Outline(pyFile, "sample.py")
	if err != nil {
		t.Fatalf("Outline python error: %v", err)
	}
	if len(syms) != 4 {
		t.Fatalf("expected 4 symbols in sample.py, got %d: %+v", len(syms), syms)
	}
	if syms[0].Kind != "class" || syms[0].Name != "Worker" {
		t.Errorf("unexpected py sym 0: %+v", syms[0])
	}
	if syms[1].Kind != "method" || syms[1].Name != "__init__" || syms[1].Indent == 0 {
		t.Errorf("unexpected py sym 1: %+v", syms[1])
	}
	if syms[2].Kind != "method" || syms[2].Name != "execute" {
		t.Errorf("unexpected py sym 2: %+v", syms[2])
	}
	if syms[3].Kind != "def" || syms[3].Name != "global_helper" || syms[3].Indent != 0 {
		t.Errorf("unexpected py sym 3: %+v", syms[3])
	}

	// 6. JavaScript / TypeScript code
	jsFile := filepath.Join(dir, "sample.ts")
	jsContent := `export interface User {
	id: string;
}

export type ID = string;
export enum Role { Admin, User }

export class AppService {
}

export const helper = async () => {};
export function main() {}
`
	if err := os.WriteFile(jsFile, []byte(jsContent), 0644); err != nil {
		t.Fatal(err)
	}
	syms, err = Outline(jsFile, "sample.ts")
	if err != nil {
		t.Fatalf("Outline JS/TS error: %v", err)
	}
	if len(syms) != 6 {
		t.Fatalf("expected 6 symbols in sample.ts, got %d: %+v", len(syms), syms)
	}

	// 7. Rust code
	rsFile := filepath.Join(dir, "sample.rs")
	rsContent := `pub mod config;
pub struct Client;
pub enum Status { Active }
pub trait Service {}
impl Service for Client {
    pub fn connect() {}
}
pub type Result<T> = std::result::Result<T, ()>;
pub const MAX_RETRIES: usize = 3;
`
	if err := os.WriteFile(rsFile, []byte(rsContent), 0644); err != nil {
		t.Fatal(err)
	}
	syms, err = Outline(rsFile, "sample.rs")
	if err != nil {
		t.Fatalf("Outline Rust error: %v", err)
	}
	if len(syms) < 6 {
		t.Fatalf("expected at least 6 symbols in sample.rs, got %d: %+v", len(syms), syms)
	}

	// 8. Shell script (alternations)
	shFile := filepath.Join(dir, "script.sh")
	shContent := `#!/usr/bin/env bash
function build_all {
    echo "building"
}
cleanup() {
    echo "clean"
}
`
	if err := os.WriteFile(shFile, []byte(shContent), 0644); err != nil {
		t.Fatal(err)
	}
	syms, err = Outline(shFile, "script.sh")
	if err != nil {
		t.Fatalf("Outline Shell error: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("expected 2 shell functions, got %d: %+v", len(syms), syms)
	}
	if syms[0].Name != "build_all" || syms[1].Name != "cleanup" {
		t.Errorf("unexpected shell symbols: %+v", syms)
	}
}

func TestIndentCols(t *testing.T) {
	t.Parallel()

	if got := indentCols(""); got != 0 {
		t.Errorf("indentCols('') = %d, want 0", got)
	}
	if got := indentCols("  "); got != 1 {
		t.Errorf("indentCols('  ') = %d, want 1", got)
	}
	if got := indentCols("    "); got != 2 {
		t.Errorf("indentCols('    ') = %d, want 2", got)
	}
	if got := indentCols("\t"); got != 2 {
		t.Errorf("indentCols('\t') = %d, want 2", got)
	}
	if got := indentCols("\t  "); got != 3 {
		t.Errorf("indentCols('\t  ') = %d, want 3", got)
	}
}

// Micro-benchmarks for package symbols

func BenchmarkFamilyFor(b *testing.B) {
	exts := []string{".go", ".py", ".ts", ".rs", ".c", ".rb", ".java", ".sh", ".php", ".txt"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FamilyFor(exts[i%len(exts)])
	}
}

func BenchmarkDeclPatterns(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = DeclPatterns("ProcessRequest")
	}
}

func BenchmarkOutlineGo(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.go")
	content := `package main
type Client struct{}
const Version = "1.0"
var DefaultClient *Client
func NewClient() *Client { return nil }
func (c *Client) Send() error { return nil }
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Outline(path, "bench.go")
	}
}

func BenchmarkOutlineMarkdown(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.md")
	content := `# Title
## Section 1
Text text
### Subsection 1.1
## Section 2
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Outline(path, "bench.md")
	}
}

func BenchmarkIndentCols(b *testing.B) {
	ws := "    \t  \t"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = indentCols(ws)
	}
}

func TestMarkdownOutlineParser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readme.md")
	src := "# Title\n\n```md\n## Not a heading\n```\n\nSection *one*\n===========\n\n### Repeat\n### Repeat\n"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := Outline(path, "readme.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []Symbol{
		{Name: "Title", Kind: "heading", Line: 1, Indent: 0},
		{Name: "Section one", Kind: "heading", Line: 7, Indent: 0},
		{Name: "Repeat", Kind: "heading", Line: 10, Indent: 2},
		{Name: "Repeat", Kind: "heading", Line: 11, Indent: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("heading %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
