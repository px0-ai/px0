package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIgnorePatterns(t *testing.T) {
	ig := newIgnoreSet([]string{"build/", "*.tmp", "!keep.tmp", "docs/**/draft.md", "/only-root.txt"})
	cases := []struct {
		path string
		dir  bool
		want bool
	}{
		{".git", true, true},
		{"node_modules", true, true},
		{"src/main.go", false, false},
		{"build", true, true},
		{"build/out.js", false, true},
		{"a/b/c.tmp", false, true},
		{"a/b/keep.tmp", false, false},
		{"docs/x/y/draft.md", false, true},
		{"docs/draft.md", false, true},
		{"only-root.txt", false, true},
		{"sub/only-root.txt", false, false},
		{"vendor", true, true},
		{"src/vendor.go", false, false},
	}
	for _, c := range cases {
		if got := ig.match(c.path, c.dir); got != c.want {
			t.Errorf("match(%q, dir=%v) = %v, want %v", c.path, c.dir, got, c.want)
		}
	}
}

func TestFuzzyRanking(t *testing.T) {
	paths := []string{
		"internal/server/http_server.go",
		"cmd/px0/main.go",
		"web/app.js",
		"pkg/util/strings.go",
		"vendor/github.com/x/http/server.go",
		"httpserver.go",
	}
	files := make([]FileEntry, len(paths))
	for i, p := range paths {
		name := p[strings.LastIndex(p, "/")+1:]
		files[i] = FileEntry{Path: p, Name: name, lower: strings.ToLower(p), nameStart: len(p) - len(name)}
	}

	got := FuzzyFind(files, "httpserver", 10)
	if len(got) == 0 {
		t.Fatal("no matches for httpserver")
	}
	if got[0].Path != "httpserver.go" {
		t.Errorf("best match for httpserver = %q, want httpserver.go", got[0].Path)
	}
	// Every returned path must actually contain the query as a subsequence.
	for _, r := range got {
		if !subsequence("httpserver", strings.ToLower(r.Path)) {
			t.Errorf("%q is not a subsequence match", r.Path)
		}
		for _, p := range r.Pos {
			if p < 0 || p >= len(r.Path) {
				t.Errorf("%q: highlight position %d out of range", r.Path, p)
			}
		}
	}
	if got := FuzzyFind(files, "zzqq", 10); len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}
	if got := FuzzyFind(files, "appjs", 10); got[0].Path != "web/app.js" {
		t.Errorf("best match for appjs = %q, want web/app.js", got[0].Path)
	}
}

func subsequence(q, s string) bool {
	i := 0
	for j := 0; j < len(s) && i < len(q); j++ {
		if s[j] == q[i] {
			i++
		}
	}
	return i == len(q)
}

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n\nfunc main() {\n\tgreet(\"hi\")\n}\n")
	write("greet.go", "package main\n\nimport \"fmt\"\n\nfunc greet(s string) {\n\tfmt.Println(s)\n}\n")
	write(".gitignore", "secret/\n")
	write("secret/keys.go", "package secret\n\nconst Token = \"nope\"\n")
	write("sub/deep.py", "def handler(req):\n    return 1\n")

	ix := NewIndex(root)
	ix.Build()
	return NewServer(ix, nil), root
}

func get(t *testing.T, s *Server, url string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func TestThemesStylesheetJoinsEveryThemeFile(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/themes.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("content type %q", ct)
	}
	files, _ := filepath.Glob(filepath.Join("web", "themes", "*.css"))
	if len(files) == 0 {
		t.Fatal("no themes in web/themes")
	}
	// Tokens with no fallback in style.css. Keep in sync with docs/internals/styling-and-themes.md.
	required := []string{
		"--bg", "--bg2", "--bg3", "--bg4", "--fg", "--dim", "--faint", "--line",
		"--accent", "--accent-fg", "--sel", "--mark", "--mark-active", "--cur", "--shadow",
		"--k", "--nf", "--s", "--m", "--c", "--err",
	}
	body := rec.Body.String()
	for _, f := range files {
		// The file name is the theme id, which the picker and saved preference use.
		id := strings.TrimSuffix(filepath.Base(f), ".css")
		if !strings.Contains(body, `:root[data-theme="`+id+`"]`) {
			t.Errorf("%s: no :root[data-theme=%q] rule", f, id)
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, tok := range required {
			if !strings.Contains(string(src), tok+":") {
				t.Errorf("%s: missing required token %s", f, tok)
			}
		}
	}
}

func TestIndexHonoursGitignore(t *testing.T) {
	s, _ := newTestServer(t)
	for _, f := range s.ix.Files() {
		if strings.HasPrefix(f.Path, "secret/") {
			t.Errorf("indexed an ignored path: %s", f.Path)
		}
	}
	if n, _, _ := s.ix.Stats(); n != 4 {
		t.Errorf("indexed %d files, want 4", n)
	}
}

// Ignored entries stay visible in the tree, flagged, but out of search.
func TestTreeListsIgnoredEntries(t *testing.T) {
	_, root := newTestServer(t)
	os.MkdirAll(filepath.Join(root, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)

	entries := func(url string) (int, map[string]map[string]any) {
		code, body := get(t, s, url)
		out := map[string]map[string]any{}
		kids, _ := body["children"].([]any)
		for _, k := range kids {
			m := k.(map[string]any)
			out[m["name"].(string)] = m
		}
		return code, out
	}

	_, top := entries("/api/tree?dir=")
	if e := top["secret"]; e == nil || e["ignored"] != true || e["dir"] != true {
		t.Errorf("secret/ = %v, want a listed, ignored directory", e)
	}
	if e := top["main.go"]; e == nil || e["ignored"] != nil {
		t.Errorf("main.go = %v, want listed and not ignored", e)
	}
	if _, ok := top[".git"]; ok {
		t.Error(".git is listed; version control internals should stay hidden")
	}

	code, inside := entries("/api/tree?dir=secret")
	if code != http.StatusOK || inside["keys.go"] == nil || inside["keys.go"]["ignored"] != true {
		t.Errorf("secret listing = %d %v, want keys.go marked ignored", code, inside)
	}
	for _, bad := range []string{"secret/..", "secret/../..", "secret/../../etc", "secret//keys.go"} {
		if code, _ := get(t, s, "/api/tree?dir="+bad); code == http.StatusOK {
			t.Errorf("tree listing for %q returned 200", bad)
		}
	}

	if _, body := get(t, s, "/api/search?q=Token"); body["files"] != float64(0) {
		t.Errorf("workspace search reached an ignored file: %v", body)
	}
	if _, body := get(t, s, "/api/search?q=Token&glob=secret/keys.go"); body["files"] != float64(1) {
		t.Errorf("find-in-file on an open ignored file found %v files, want 1", body["files"])
	}
	if _, body := get(t, s, "/api/search?q=Token&glob=secret/../secret/keys.go"); body["files"] != float64(0) {
		t.Errorf("a glob with .. reached an unindexed file: %v", body)
	}
	if code, _ := get(t, s, "/api/file?path=secret/keys.go"); code != http.StatusOK {
		t.Errorf("opening an ignored file returned %d", code)
	}
}

func TestPathTraversalRefused(t *testing.T) {
	s, _ := newTestServer(t)
	for _, bad := range []string{
		"/api/file?path=../../../etc/passwd",
		"/api/file?path=/etc/passwd",
		"/api/file?path=sub/../../outside",
		"/api/outline?path=..%2F..%2Fetc%2Fpasswd",
	} {
		code, body := get(t, s, bad)
		if code == http.StatusOK {
			t.Errorf("%s returned 200: %v", bad, body)
		}
	}
}

func TestFileAndSearchAndDef(t *testing.T) {
	s, _ := newTestServer(t)

	code, body := get(t, s, "/api/file?path=greet.go")
	if code != http.StatusOK {
		t.Fatalf("file: %d %v", code, body)
	}
	if body["lang"] != "Go" {
		t.Errorf("lang = %v, want Go", body["lang"])
	}
	lines := body["lines"].([]any)
	if !strings.Contains(lines[0].(string), "class=k>package") {
		t.Errorf("first line not highlighted: %q", lines[0])
	}

	code, body = get(t, s, "/api/search?q=greet")
	if code != http.StatusOK {
		t.Fatalf("search: %d", code)
	}
	if int(body["files"].(float64)) != 2 {
		t.Errorf("search hit %v files, want 2", body["files"])
	}

	// The declaration, not the call site, must come back as the definition.
	code, body = get(t, s, "/api/def?sym=greet")
	if code != http.StatusOK {
		t.Fatalf("def: %d", code)
	}
	defs := body["defs"].([]any)
	if len(defs) != 1 {
		t.Fatalf("got %d definitions, want 1: %v", len(defs), defs)
	}
	d := defs[0].(map[string]any)
	if d["path"] != "greet.go" || int(d["line"].(float64)) != 5 {
		t.Errorf("definition at %v:%v, want greet.go:5", d["path"], d["line"])
	}

	code, body = get(t, s, "/api/outline?path=sub/deep.py")
	if code != http.StatusOK {
		t.Fatalf("outline: %d", code)
	}
	syms := body["symbols"].([]any)
	if len(syms) != 1 || syms[0].(map[string]any)["name"] != "handler" {
		t.Errorf("outline = %v, want one symbol named handler", syms)
	}
}

func TestChunkedReadsCoverWholeFile(t *testing.T) {
	root := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 4321; i++ {
		sb.WriteString("x := ")
		sb.WriteString(strings.Repeat("a", i%17))
		sb.WriteString("\n")
	}
	os.WriteFile(filepath.Join(root, "big.go"), []byte(sb.String()), 0o644)
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)

	var all []string
	for start := 0; ; start += hlChunk {
		_, body := get(t, s, "/api/file?path=big.go&start="+itoa(start))
		total := int(body["total"].(float64))
		for _, l := range body["lines"].([]any) {
			all = append(all, l.(string))
		}
		if start+hlChunk >= total {
			if len(all) != total {
				t.Fatalf("chunked reads produced %d lines, want %d", len(all), total)
			}
			break
		}
	}
	if !strings.Contains(all[0], ":=") {
		t.Errorf("line 1 lost content: %q", all[0])
	}
	if !strings.Contains(all[4320], ":=") {
		t.Errorf("last line lost content: %q", all[4320])
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The segment fast paths must be indistinguishable from the regexp they
// replace. This compares the two on every combination of a pattern set and a
// path set, so a shortcut that is merely "close enough" fails here.
func TestIgnoreFastPathMatchesRegex(t *testing.T) {
	patterns := []string{
		"node_modules", "dist", ".git", "build/", "vendor/", "target",
		"*.pyc", "*.log", "*.min.js", "*", "*.", ".DS_Store",
		"a?c", "**", "x**y", "sub/dir", "/rooted.txt", "docs/**/draft.md",
		"weird name", "dash-name", "dot.name.ext", "UPPER",
		// anchored shapes, which now take a literal-prefix reject or an exact path
		"tests/baselines", "tests/baselines/reference", "src/*.ts", "src/**/gen.js",
		"/built/local", "a/b/c/d", "tests/cases/*/x?.ts", "pkg/*/vendor",
		// "**/" shapes, which have no literal head and rely on the run filter
		"**/.vscode/*", "**/node_modules/**", "**/*.generated.ts", "**/a/b",
		"x/**/y", "*/mid/*", "**/",
	}
	paths := []string{
		"", "a", "node_modules", "node_modules/x.js", "a/node_modules",
		"a/node_modules/b/c.js", "src/dist", "src/dist/app.js", "dist",
		"build", "build/out.js", "a/build/b/c", "vendor", "vendor/x",
		"x.pyc", "a/b/x.pyc", "pyc", ".pyc", "a.min.js", "a/b.min.js",
		"abc", "a/abc", "axc", "sub/dir", "sub/dir/f", "a/sub/dir",
		"rooted.txt", "a/rooted.txt", "docs/x/draft.md", "docs/draft.md",
		"weird name", "weird name/f", "dash-name", "dot.name.ext",
		"UPPER", "upper", "target", "a/target/b", ".DS_Store", "a/.DS_Store",
		"x**y", "xzzy", "a/b/c/d/e/f/g",
		"tests", "tests/baselines", "tests/baselines/reference", "tests/baselines/reference/x.js",
		"tests/baselinesX", "src/a.ts", "src/a/b.ts", "src/x/gen.js", "src/gen.js",
		"built/local", "built/local/tsc.js", "builtX/local", "a/b/c/d", "a/b/c/d/e",
		"tests/cases/foo/x1.ts", "tests/cases/foo/x12.ts", "pkg/a/vendor", "pkg/vendor",
		".vscode", ".vscode/settings.json", "a/.vscode/x.json", "a/b/.vscode",
		"node_modules/x", "a/node_modules/b/c", "x.generated.ts", "a/b/x.generated.ts",
		"a/b", "q/a/b", "x/y", "x/m/y", "x/m/n/y", "p/mid/q", "mid/q", "p/mid",
	}

	for _, pat := range patterns {
		for _, dirOnlySuffix := range []string{"", "/"} {
			full := pat + dirOnlySuffix
			r, ok := compilePattern(full)
			if !ok {
				continue
			}
			// Rebuild the same pattern forced onto the regexp path.
			slow, ok := compileRegexOnly(full)
			if !ok {
				t.Fatalf("%q: regex form failed to compile", full)
			}
			if r.kind != rkRegex {
				if slow.re == nil || slow.sub == nil {
					t.Fatalf("%q: reference rule has no regexps", full)
				}
			}
			for _, p := range paths {
				for _, isDir := range []bool{false, true} {
					got := r.hit(p, isDir)
					want := slow.hit(p, isDir)
					if got != want {
						t.Errorf("pattern %q path %q dir=%v: fast=%v regex=%v (kind=%d lit=%q)",
							full, p, isDir, got, want, r.kind, r.lit)
					}
				}
			}
		}
	}
}

// compileRegexOnly builds the same rule with the segment shortcuts disabled.
func compileRegexOnly(p string) (rule, bool) {
	r, ok := compilePattern(p)
	if !ok {
		return r, false
	}
	if r.kind == rkRegex {
		return r, true
	}
	// Re-derive the regexp form by compiling a pattern that cannot be
	// shortcut, then transplanting the flags.
	forced, ok := compilePatternRegex(p)
	if !ok {
		return r, false
	}
	return forced, true
}

// A Unicode character whose lowercase form is a different byte length used to
// shift every match offset after it on the same line, so the highlighted span
// drifted. Folding ASCII only keeps offsets exact.
func TestCaseInsensitiveOffsetsWithUnicode(t *testing.T) {
	root := t.TempDir()
	// U+0130 lowercases from two bytes to one under full Unicode folding.
	body := "package x\n\nvar İd = FindUser(name)\n"
	if err := os.WriteFile(filepath.Join(root, "u.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)

	code, out := get(t, s, "/api/search?q=finduser")
	if code != http.StatusOK {
		t.Fatalf("search: %d", code)
	}
	res := out["results"].([]any)
	if len(res) != 1 {
		t.Fatalf("got %d files, want 1", len(res))
	}
	m := res[0].(map[string]any)["matches"].([]any)[0].(map[string]any)
	if m["mid"] != "FindUser" {
		t.Errorf("matched text = %q, want %q", m["mid"], "FindUser")
	}
	if m["pre"] != "var İd = " {
		t.Errorf("text before the match = %q, want %q", m["pre"], "var İd = ")
	}
	if m["post"] != "(name)" {
		t.Errorf("text after the match = %q, want %q", m["post"], "(name)")
	}
}

func TestAsciiLowerPreservesLength(t *testing.T) {
	for _, s := range []string{"", "abc", "ABC", "İd", "cafÉ", "日本", "MiXeD123"} {
		var buf []byte
		got := asciiLower(&buf, []byte(s))
		if len(got) != len(s) {
			t.Errorf("asciiLower(%q) changed length %d -> %d", s, len(s), len(got))
		}
		if asciiLowerString(s) != string(got) {
			t.Errorf("string and byte forms disagree for %q", s)
		}
	}
}

// The include filter has to work for plain names and full paths, not only for
// wildcard patterns. Find-in-file relies on it with an exact path.
func TestSearchGlobFilter(t *testing.T) {
	s, _ := newTestServer(t)

	cases := []struct {
		glob string
		want []string
	}{
		{"", []string{"greet.go", "main.go"}},
		{"greet.go", []string{"greet.go"}}, // plain name, no wildcard
		{"*.go", []string{"greet.go", "main.go"}},
		{"sub/deep.py", nil}, // exact path, no match for this query
		{"nothing.go", nil},
	}
	for _, c := range cases {
		url := "/api/search?q=greet"
		if c.glob != "" {
			url += "&glob=" + c.glob
		}
		code, body := get(t, s, url)
		if code != http.StatusOK {
			t.Fatalf("glob %q: status %d", c.glob, code)
		}
		list, ok := body["results"].([]any)
		if !ok {
			t.Fatalf("glob %q: results is %T, want an array even when empty", c.glob, body["results"])
		}
		var got []string
		for _, r := range list {
			got = append(got, r.(map[string]any)["path"].(string))
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("glob %q matched %v, want %v", c.glob, got, c.want)
		}
	}

	// Find-in-file passes the active file's exact path.
	code, body := get(t, s, "/api/search?q=package&glob=sub/deep.py")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if n := len(body["results"].([]any)); n != 0 {
		t.Errorf("exact-path glob leaked %d other files", n)
	}
	code, body = get(t, s, "/api/search?q=handler&glob=sub/deep.py")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	res := body["results"].([]any)
	if len(res) != 1 || res[0].(map[string]any)["path"] != "sub/deep.py" {
		t.Errorf("exact-path glob returned %v, want just sub/deep.py", res)
	}
}

func TestCloseEndpoint(t *testing.T) {
	s, root := newTestServer(t)

	// 1. Open file to ensure it is in highlight cache
	code, body := get(t, s, "/api/file?path=greet.go")
	if code != http.StatusOK {
		t.Fatalf("file: %d %v", code, body)
	}
	abs := filepath.Join(root, "greet.go")
	cache.mu.Lock()
	found := false
	for k := range cache.items {
		if strings.HasPrefix(k, abs+"|") {
			found = true
			break
		}
	}
	cache.mu.Unlock()
	if !found {
		t.Fatal("expected greet.go to be in highlight cache")
	}

	// 2. Call /api/close?path=greet.go
	code, body = get(t, s, "/api/close?path=greet.go")
	if code != http.StatusOK {
		t.Fatalf("close: %d %v", code, body)
	}
	if body["ok"] != true {
		t.Errorf("expected ok: true, got %v", body)
	}

	// 3. Verify evicted from cache
	cache.mu.Lock()
	foundAfter := false
	for k := range cache.items {
		if strings.HasPrefix(k, abs+"|") {
			foundAfter = true
			break
		}
	}
	cache.mu.Unlock()
	if foundAfter {
		t.Fatal("expected greet.go to be evicted from highlight cache after /api/close")
	}

	// 4. Bad path returns 400
	code, _ = get(t, s, "/api/close?path=../../nonexistent")
	if code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad path, got %d", code)
	}
}

func TestListenPortFallback(t *testing.T) {
	// Bind a port first
	ln1, addr1, err := listen("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("first listen failed: %v", err)
	}
	defer ln1.Close()

	// Extract port number
	_, portStr, err := net.SplitHostPort(addr1)
	if err != nil {
		t.Fatalf("split host port failed: %v", err)
	}
	p, _ := strconv.Atoi(portStr)

	// Trying to listen on the same port should find the next available port
	ln2, addr2, err := listen("127.0.0.1", p)
	if err != nil {
		t.Fatalf("second listen failed: %v", err)
	}
	defer ln2.Close()

	if addr2 == addr1 {
		t.Fatalf("second listener got the same address %s", addr2)
	}
}

func TestResolveTarget(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "report #1.json")
	if err := os.WriteFile(file, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirRoot, initialFile, initialLine, err := resolveTarget(root)
	if err != nil {
		t.Fatalf("resolveTarget(directory): %v", err)
	}
	if dirRoot != resolvedRoot || initialFile != "" || initialLine != 0 {
		t.Fatalf("resolveTarget(directory) = %q, %q, %d; want %q, empty, 0", dirRoot, initialFile, initialLine, resolvedRoot)
	}

	fileRoot, initialFile, initialLine, err := resolveTarget(file)
	if err != nil {
		t.Fatalf("resolveTarget(file): %v", err)
	}
	if fileRoot != resolvedRoot || initialFile != filepath.Base(file) || initialLine != 0 {
		t.Fatalf("resolveTarget(file) = %q, %q, %d; want %q, %q, 0", fileRoot, initialFile, initialLine, resolvedRoot, filepath.Base(file))
	}
}

func TestResolveTargetGitRepo(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	repo := gitRepo(t)
	subFile := filepath.Join(repo, "pkg", "sub", "app.go")
	if err := os.MkdirAll(filepath.Dir(subFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subFile, []byte("package sub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root, initialFile, initialLine, err := resolveTarget(subFile)
	if err != nil {
		t.Fatalf("resolveTarget(subFile): %v", err)
	}
	if root != repo {
		t.Fatalf("resolveTarget root = %q, want repo %q", root, repo)
	}
	if initialFile != "pkg/sub/app.go" || initialLine != 0 {
		t.Fatalf("resolveTarget file = %q, line = %d; want pkg/sub/app.go, 0", initialFile, initialLine)
	}

	targetWithLine := subFile + ":42"
	root, initialFile, initialLine, err = resolveTarget(targetWithLine)
	if err != nil {
		t.Fatalf("resolveTarget(%q): %v", targetWithLine, err)
	}
	if root != repo || initialFile != "pkg/sub/app.go" || initialLine != 42 {
		t.Fatalf("resolveTarget with line: got root=%q file=%q line=%d", root, initialFile, initialLine)
	}

	targetWithLineCol := subFile + ":42:15"
	root, initialFile, initialLine, err = resolveTarget(targetWithLineCol)
	if err != nil {
		t.Fatalf("resolveTarget(%q): %v", targetWithLineCol, err)
	}
	if root != repo || initialFile != "pkg/sub/app.go" || initialLine != 42 {
		t.Fatalf("resolveTarget with line:col: got root=%q file=%q line=%d", root, initialFile, initialLine)
	}
}

func TestResolveTargetRejectsMissingPath(t *testing.T) {
	if _, _, _, err := resolveTarget(filepath.Join(t.TempDir(), "missing.go")); err == nil {
		t.Fatal("resolveTarget accepted a missing path")
	}
}

func TestViewerURL(t *testing.T) {
	got := viewerURL("127.0.0.1:7777", "report #1.json", 0)
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("viewerURL returned an invalid URL: %v", err)
	}
	if u.Scheme != "http" || u.Host != "127.0.0.1:7777" || u.Query().Get("path") != "report #1.json" || u.Query().Get("line") != "" {
		t.Fatalf("viewerURL = %q", got)
	}

	gotWithLine := viewerURL("127.0.0.1:7777", "report #1.json", 42)
	uWithLine, err := url.Parse(gotWithLine)
	if err != nil {
		t.Fatalf("viewerURL with line returned an invalid URL: %v", err)
	}
	if uWithLine.Query().Get("path") != "report #1.json" || uWithLine.Query().Get("line") != "42" {
		t.Fatalf("viewerURL with line = %q", gotWithLine)
	}

	if got := viewerURL("127.0.0.1:7777", "", 0); got != "http://127.0.0.1:7777" {
		t.Fatalf("viewerURL without a file = %q", got)
	}
}

func TestPrintURLIsMachineReadable(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "px0-test")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build px0: %v\n%s", err, out)
	}

	cmd := exec.Command(bin,
		"-print-url", "-no-open", "-port", "0", "-no-lsp", "-no-git",
		"-no-agent", "-no-telemetry", t.TempDir(),
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	reader := bufio.NewReader(stdout)
	go func() {
		line, err := reader.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- strings.TrimSpace(line)
	}()

	var line string
	select {
	case line = <-lineCh:
	case err := <-errCh:
		t.Fatalf("read URL: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for URL")
	}
	u, err := url.Parse(line)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" {
		t.Fatalf("-print-url output %q is not a bound loopback URL", line)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if strings.TrimSpace(string(rest)) != "" {
		t.Fatalf("-print-url emitted extra stdout: %q", rest)
	}
}

func TestVersionDrivenFromVERSIONFile(t *testing.T) {
	data, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("failed to read VERSION file: %v", err)
	}
	expected := strings.TrimSpace(string(data))
	if version != expected {
		t.Fatalf("version variable %q does not match VERSION file %q", version, expected)
	}
}

func TestMetaIncludesVersion(t *testing.T) {
	s, _ := newTestServer(t)
	code, body := get(t, s, "/api/meta")
	if code != http.StatusOK {
		t.Fatalf("/api/meta returned %d: %v", code, body)
	}
	v, ok := body["version"].(string)
	if !ok || v != version {
		t.Fatalf("expected version %q in /api/meta, got %v", version, body["version"])
	}
}
