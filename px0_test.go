package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/px0-ai/harness"

	"px0/internal/metrics"
	"px0/internal/render"
	"px0/internal/settings"
	"px0/internal/ui"
)

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

func TestStaticAssetServing(t *testing.T) {
	s, _ := newTestServer(t)

	// 1. Serving uncompressed assets (e.g. style.css)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/style.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("style.css status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("style.css content-type: %s", ct)
	}

	// 2. Serving compressed vendor assets with Accept-Encoding: gzip
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/static/vendor/mermaid-12.0.0.min.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mermaid gzip status %d", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("mermaid content-encoding = %q, want gzip", ce)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("mermaid content-type = %q, want text/javascript", ct)
	}
	gzr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	decompressed, err := io.ReadAll(gzr)
	gzr.Close()
	if err != nil {
		t.Fatalf("failed to decompress gzip body: %v", err)
	}
	if bytes.HasPrefix(decompressed, []byte("\x1f\x8b")) {
		t.Errorf("decompressed mermaid body is still gzipped (double gzip detected)")
	}
	if !strings.HasPrefix(string(decompressed), "\"use strict\";") {
		t.Errorf("decompressed mermaid body missing expected javascript prefix")
	}
	if !strings.Contains(string(decompressed), "mermaid") {
		t.Errorf("decompressed mermaid body missing 'mermaid' keyword")
	}

	// 3. Serving compressed vendor assets without Accept-Encoding: gzip (transparent decompression)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/static/vendor/mermaid-12.0.0.min.js", nil)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mermaid uncompressed status %d", rec.Code)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("mermaid content-encoding without gzip request = %q, want none", ce)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("mermaid content-type = %q, want text/javascript", ct)
	}
	if !strings.Contains(rec.Body.String(), "mermaid") {
		t.Errorf("uncompressed mermaid body missing 'mermaid' keyword")
	}

	// 4. Missing static file returns 404
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/nonexistent.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing asset status = %d, want 404", rec.Code)
	}

	// 5. Path traversal attempts are rejected (never 200 OK)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/../server.go", nil))
	if rec.Code == http.StatusOK {
		t.Errorf("traversal returned 200 OK")
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
	for start := 0; ; start += render.ChunkLines {
		_, body := get(t, s, "/api/file?path=big.go&start="+itoa(start))
		total := int(body["total"].(float64))
		for _, l := range body["lines"].([]any) {
			all = append(all, l.(string))
		}
		if start+render.ChunkLines >= total {
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

	// 2. Call /api/close?path=greet.go
	code, body = get(t, s, "/api/close?path=greet.go")
	if code != http.StatusOK {
		t.Fatalf("close: %d %v", code, body)
	}
	if body["ok"] != true {
		t.Errorf("expected ok: true, got %v", body)
	}

	// 3. Verify evicted from cache
	if render.Evict(abs) {
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

	// Base path tests
	if got := viewerURL("127.0.0.1:7777", "", 0, "/rev-123/"); got != "http://127.0.0.1:7777/rev-123/" {
		t.Fatalf("viewerURL with base path = %q, want http://127.0.0.1:7777/rev-123/", got)
	}
	if got := viewerURL("127.0.0.1:7777", "main.go", 10, "/rev-123"); got != "http://127.0.0.1:7777/rev-123/?line=10&path=main.go" {
		t.Fatalf("viewerURL with base path and file = %q", got)
	}
}

func TestCleanBasePath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "/"},
		{"/", "/"},
		{".", "/"},
		{"rev-123", "/rev-123/"},
		{"/rev-123", "/rev-123/"},
		{"/rev-123/", "/rev-123/"},
		{"//rev-123///", "/rev-123/"},
		{"sub/path", "/sub/path/"},
		{"/a/b/", "/a/b/"},
	}
	for _, tc := range cases {
		got := cleanBasePath(tc.in)
		if got != tc.want {
			t.Errorf("cleanBasePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestServerBasePathRouting(t *testing.T) {
	root := t.TempDir()
	ix := NewIndex(root)
	s := NewServer(ix, nil, "/rev-123/")

	if s.BasePath() != "/rev-123/" {
		t.Fatalf("BasePath() = %q, want /rev-123/", s.BasePath())
	}

	// 1. GET /rev-123/ should return 200 and have injected <base href="/rev-123/">
	req := httptest.NewRequest(http.MethodGet, "/rev-123/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /rev-123/ code = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<base href="/rev-123/">`) {
		t.Errorf("GET /rev-123/ body missing injected base href: %s", rec.Body.String())
	}

	// 2. GET /rev-123 without trailing slash should redirect to /rev-123/
	reqNoSlash := httptest.NewRequest(http.MethodGet, "/rev-123", nil)
	recNoSlash := httptest.NewRecorder()
	s.ServeHTTP(recNoSlash, reqNoSlash)
	if recNoSlash.Code != http.StatusMovedPermanently && recNoSlash.Code != http.StatusFound {
		t.Errorf("GET /rev-123 code = %d, want redirect", recNoSlash.Code)
	}
	if loc := recNoSlash.Header().Get("Location"); loc != "/rev-123/" {
		t.Errorf("GET /rev-123 redirect Location = %q, want /rev-123/", loc)
	}

	// 3. GET / should redirect to /rev-123/
	reqRoot := httptest.NewRequest(http.MethodGet, "/", nil)
	recRoot := httptest.NewRecorder()
	s.ServeHTTP(recRoot, reqRoot)
	if recRoot.Code != http.StatusFound {
		t.Errorf("GET / code = %d, want 302", recRoot.Code)
	}
	if loc := recRoot.Header().Get("Location"); loc != "/rev-123/" {
		t.Errorf("GET / redirect Location = %q, want /rev-123/", loc)
	}

	// 4. GET /rev-123/api/meta returns 200 and basePath
	reqMeta := httptest.NewRequest(http.MethodGet, "/rev-123/api/meta", nil)
	recMeta := httptest.NewRecorder()
	s.ServeHTTP(recMeta, reqMeta)
	if recMeta.Code != http.StatusOK {
		t.Fatalf("GET /rev-123/api/meta code = %d, want 200", recMeta.Code)
	}
	var meta map[string]any
	if err := json.Unmarshal(recMeta.Body.Bytes(), &meta); err != nil {
		t.Fatalf("failed to decode meta: %v", err)
	}
	if meta["basePath"] != "/rev-123/" {
		t.Errorf("meta.basePath = %v, want /rev-123/", meta["basePath"])
	}

	// 5. GET /api/meta without prefix should 404
	reqRootMeta := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
	recRootMeta := httptest.NewRecorder()
	s.ServeHTTP(recRootMeta, reqRootMeta)
	if recRootMeta.Code != http.StatusNotFound {
		t.Errorf("GET /api/meta code = %d, want 404", recRootMeta.Code)
	}
}

func TestNetworkURLs(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("10.0.0.4"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
	}

	got := networkURLsFromAddrs("0.0.0.0:7777", "main.go", 42, addrs)
	want := []string{
		"http://10.0.0.4:7777?line=42&path=main.go",
		"http://192.168.1.20:7777?line=42&path=main.go",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("networkURLsFromAddrs = %v, want %v", got, want)
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

// completionTestFlags mirrors the kinds of flags main defines: boolean,
// free-valued, -agent with fixed choices, and a hidden internal flag.
func completionTestFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("px0", flag.ContinueOnError)
	fs.Int("port", 7777, "port to listen on (0 picks a free one)")
	fs.Bool("no-lsp", false, "do not use language servers, even if installed")
	fs.String("agent", "", "pin the coding harness used for edits (claude, ...); detected when omitted")
	fs.String("base-path", "", "base URL path prefix (e.g. /rev-123/)")
	fs.Bool("workbench-child", false, "internal: run as a workspace of the px0 workbench")
	fs.Bool("y", false, "answer yes to prompts (deprecated)")
	return fs
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range completionShells {
		var out bytes.Buffer
		if err := runCompletion(&out, io.Discard, []string{shell}, completionTestFlags()); err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		script := out.String()
		var want []string
		if shell == "zsh" {
			for _, name := range []string{"port", "no-lsp", "agent", "base-path"} {
				want = append(want, "!-"+name)
			}
		}
		for _, c := range completionCommands {
			want = append(want, c.name)
		}
		if shell == "zsh" { // zsh retains flag specs to parse existing options
			want = append(want, "port to listen on")
		}
		for _, w := range want {
			if !strings.Contains(script, w) {
				t.Errorf("%s script is missing %q", shell, w)
			}
		}
		for _, name := range harness.PresetNames() {
			if !strings.Contains(script, name) {
				t.Errorf("%s script does not offer -agent %s", shell, name)
			}
		}
		for _, hidden := range []string{"workbench-child", "yes to prompts"} {
			if strings.Contains(script, hidden) {
				t.Errorf("%s script exposes hidden flag text %q", shell, hidden)
			}
		}
		// Shells that can check syntax without running anything.
		if shell == "bash" || shell == "zsh" {
			if path, err := exec.LookPath(shell); err == nil {
				cmd := exec.Command(path, "-n")
				cmd.Stdin = strings.NewReader(script)
				if b, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("%s -n rejected the script: %v\n%s", shell, err, b)
				}
			}
		}
	}
}

func TestBashCompletionCandidates(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	var script bytes.Buffer
	if err := runCompletion(&script, io.Discard, []string{"bash"}, completionTestFlags()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, words, want string
	}{
		{"global flag", "px0 -no", ""},
		{"flag after target", "px0 ./repo -no", ""},
		{"agent value", "px0 -agent co", "codex"},
		{"agent equals", "px0 -agent=co", "-agent=codex"},
		{"long agent equals", "px0 --agent=co", "--agent=codex"},
		{"word break equals", "px0 -agent = co", "codex"},
		{"agent after target", "px0 ./repo -agent co", ""},
		{"agent equals after target", "px0 ./repo -agent=co", ""},
		{"command", "px0 ver", "version"},
		{"command after flag", "px0 -port 7777 ver", "version"},
		{"command after equals", "px0 -port = 7777 ver", "version"},
		{"shell", "px0 completion zs", "zsh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bash, "--noprofile", "--norc")
			cmd.Dir = t.TempDir()
			cmd.Stdin = strings.NewReader(script.String() + "\nCOMP_WORDS=(" + tc.words + "); COMP_CWORD=$((${#COMP_WORDS[@]}-1)); _px0; for candidate in \"${COMPREPLY[@]}\"; do printf '%s\\n' \"$candidate\"; done\n")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("completion failed: %v: %s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Fatalf("candidates = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCompletionNoDescriptions(t *testing.T) {
	var out bytes.Buffer
	if err := runCompletion(&out, io.Discard, []string{"-no-descriptions", "zsh"}, completionTestFlags()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "port to listen on") || strings.Contains(out.String(), "print version and exit") {
		t.Fatalf("-no-descriptions still printed descriptions:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "'!-port=:port: '") {
		t.Fatalf("-no-descriptions dropped the -port spec:\n%s", out.String())
	}
}

func TestCompletionRejectsBadArgs(t *testing.T) {
	for _, args := range [][]string{nil, {"tcsh"}, {"bash", "zsh"}} {
		var out bytes.Buffer
		if err := runCompletion(&out, io.Discard, args, completionTestFlags()); err == nil {
			t.Errorf("runCompletion(%q) succeeded, want an error", args)
		}
		if out.Len() > 0 {
			t.Errorf("runCompletion(%q) wrote to stdout: %q", args, out.String())
		}
	}
	if err := runCompletion(io.Discard, io.Discard, []string{"-h"}, completionTestFlags()); err != nil {
		t.Errorf("runCompletion(-h) = %v, want nil", err)
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

func TestVerboseRequestLogging(t *testing.T) {
	origVerbose := ui.Verbose
	origQuiet := ui.Quiet
	defer func() {
		ui.Verbose = origVerbose
		ui.Quiet = origQuiet
	}()

	s, _ := newTestServer(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	ui.Verbose = true
	ui.Quiet = false

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/tree", nil)
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	rec404 := httptest.NewRecorder()
	req404 := httptest.NewRequest(http.MethodGet, "/api/nonexistent", nil)
	s.ServeHTTP(rec404, req404)

	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()

	logOutput := string(out)
	if !strings.Contains(logOutput, "GET /api/tree · 200") {
		t.Errorf("expected log output to contain 'GET /api/tree · 200', got:\n%s", logOutput)
	}
	if !strings.Contains(logOutput, "GET /api/nonexistent · 404") {
		t.Errorf("expected log output to contain 'GET /api/nonexistent · 404', got:\n%s", logOutput)
	}
}

func TestStatusRecorderInterfaces(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec}

	// Verify http.Flusher
	if flusher, ok := any(sr).(http.Flusher); ok {
		flusher.Flush()
		if !rec.Flushed {
			t.Error("expected Flush to propagate to underlying recorder")
		}
	} else {
		t.Error("statusRecorder does not implement http.Flusher")
	}

	// Verify Unwrap
	if unwrapped := sr.Unwrap(); unwrapped != rec {
		t.Errorf("expected Unwrap to return %p, got %p", rec, unwrapped)
	}

	// Verify gzipWriter Flusher and Unwrap
	gw := gzipWriter{ResponseWriter: rec}
	if _, ok := any(gw).(http.Flusher); !ok {
		t.Error("gzipWriter does not implement http.Flusher")
	}
	if unwrapped := gw.Unwrap(); unwrapped != rec {
		t.Errorf("expected gzipWriter Unwrap to return %p, got %p", rec, unwrapped)
	}
}

func TestUnifiedEventStream(t *testing.T) {
	s, _ := newTestServer(t)
	ts := httptest.NewServer(s)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/stream", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext failed: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	scanner := bufio.NewScanner(resp.Body)
	var foundMetrics bool
	var metricsData string

	for scanner.Scan() {
		line := scanner.Text()
		if line == "event: metrics" {
			foundMetrics = true
		} else if foundMetrics && strings.HasPrefix(line, "data: ") {
			metricsData = strings.TrimPrefix(line, "data: ")
			break
		}
	}

	if !foundMetrics || metricsData == "" {
		t.Fatalf("did not receive initial metrics event on /api/stream")
	}

	var m metrics.ProcessMetrics
	if err := json.Unmarshal([]byte(metricsData), &m); err != nil {
		t.Fatalf("failed to unmarshal metrics JSON: %v, raw: %s", err, metricsData)
	}
	if m.Goroutine <= 0 {
		t.Errorf("expected goroutines > 0, got %d", m.Goroutine)
	}
}

func TestGzipWriterBodilessResponsesAndContentLength(t *testing.T) {
	rec := httptest.NewRecorder()
	gz, _ := gzip.NewWriterLevel(rec, gzip.BestSpeed)
	gw := &gzipWriter{ResponseWriter: rec, w: gz}

	// 1. Test 304 Not Modified
	gw.Header().Set("Content-Length", "1234")
	gw.Header().Set("Content-Encoding", "gzip")
	gw.WriteHeader(http.StatusNotModified)
	gz.Close()

	if rec.Header().Get("Content-Length") != "" {
		t.Errorf("expected Content-Length to be deleted on 304, got: %q", rec.Header().Get("Content-Length"))
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("expected Content-Encoding to be deleted on 304, got: %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected no body written on 304, got %d bytes: %q", rec.Body.Len(), rec.Body.String())
	}

	// 2. Test 200 with normal body
	rec2 := httptest.NewRecorder()
	gz2, _ := gzip.NewWriterLevel(rec2, gzip.BestSpeed)
	gw2 := &gzipWriter{ResponseWriter: rec2, w: gz2}
	gw2.Header().Set("Content-Length", "999")
	gw2.Header().Set("Content-Encoding", "gzip")
	gw2.WriteHeader(http.StatusOK)
	gw2.Write([]byte("hello compressed world"))
	gz2.Close()

	if rec2.Header().Get("Content-Length") != "" {
		t.Errorf("expected Content-Length to be stripped from compressed response, got: %q", rec2.Header().Get("Content-Length"))
	}
	if rec2.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding to be gzip on 200, got: %q", rec2.Header().Get("Content-Encoding"))
	}
	if rec2.Body.Len() == 0 {
		t.Errorf("expected compressed body on 200")
	}
}

func TestServerServeHTTPGzip(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("expected gzip Content-Encoding, got %q", rec.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader on response body: %v (len=%d)", err, rec.Body.Len())
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("failed to read decompressed body: %v", err)
	}
	if len(body) == 0 {
		t.Errorf("expected non-empty decompressed body, got 0 bytes")
	}
	if !strings.Contains(string(body), "<!doctype html>") {
		t.Errorf("expected html content in body")
	}
}

func TestChildrenReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	ix := NewIndex(dir)
	ix.Build()

	kids1, ok := ix.Children("")
	if !ok || len(kids1) == 0 {
		t.Fatalf("expected children at root")
	}

	// Mutating the returned slice must not mutate ix.children
	kids1[0].Dirty = true
	kids1[0].Status = "M"

	kids2, _ := ix.Children("")
	if kids2[0].Dirty != false || kids2[0].Status != "" {
		t.Errorf("ix.Children did not return an isolated copy of node slice")
	}
}

func TestLSPBoundsChecks(t *testing.T) {
	c := &lspClient{encoding: "utf-32"}

	// Test toLSP with negative column
	pos := c.toLSP("hello", 1, -5)
	if pos.Character != 0 {
		t.Errorf("expected 0 for negative byteCol, got %d", pos.Character)
	}

	// Test fromLSP with negative character
	lines := []string{"hello"}
	line, col := c.fromLSP(lines, lspPosition{Line: 0, Character: -10})
	if line != 1 || col != 0 {
		t.Errorf("expected line 1 col 0 for negative character, got line %d col %d", line, col)
	}
}

func TestSnipBoundsChecks(t *testing.T) {
	// Negative from, out of bounds to, inverted range
	m1 := snip([]byte("hello world"), -5, 100)
	if m1.Mid != "hello world" {
		t.Errorf("expected clamped mid 'hello world', got %q", m1.Mid)
	}

	m2 := snip([]byte("hello world"), 8, 3)
	if m2.Mid != "" {
		t.Errorf("expected empty mid for inverted range, got %q", m2.Mid)
	}
}

func TestSetRawHeaders(t *testing.T) {
	cases := []struct {
		path            string
		wantContentType string
		wantFilename    string
	}{
		{"index.html", "application/octet-stream", "index.html"},
		{"app.js", "application/octet-stream", "app.js"},
		{"main.go", "application/octet-stream", "main.go"},
		{"nested/path/style.css", "application/octet-stream", "style.css"},
		{"assets/logo.png", "image/png", "logo.png"},
		{"assets/photo.jpg", "image/jpeg", "photo.jpg"},
		{"assets/anim.gif", "image/gif", "anim.gif"},
		{"assets/vector.svg", "image/svg+xml", "vector.svg"},
		{"assets/icon.ico", "image/x-icon", "icon.ico"},
		{"assets/pic.webp", "image/webp", "pic.webp"},
	}

	for _, c := range cases {
		rec := httptest.NewRecorder()
		setRawHeaders(rec, c.path)

		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: got nosniff header %q, want 'nosniff'", c.path, got)
		}

		gotCT := rec.Header().Get("Content-Type")
		if strings.HasPrefix(c.wantContentType, "image/") {
			if !strings.HasPrefix(gotCT, "image/") {
				t.Errorf("%s: got Content-Type %q, want image/*", c.path, gotCT)
			}
		} else if gotCT != c.wantContentType {
			t.Errorf("%s: got Content-Type %q, want %q", c.path, gotCT, c.wantContentType)
		}

		gotCD := rec.Header().Get("Content-Disposition")
		if !strings.HasPrefix(gotCD, "attachment") || !strings.Contains(gotCD, c.wantFilename) {
			t.Errorf("%s: got Content-Disposition %q, expected attachment containing %q", c.path, gotCD, c.wantFilename)
		}
	}
}

func TestRawEndpointHeaders(t *testing.T) {
	s, root := newTestServer(t)

	// Create test files
	files := map[string]string{
		"evil.html": "<!DOCTYPE html><html><script>alert(1)</script></html>",
		"icon.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><circle r="10"/></svg>`,
		"data.json": `{"foo": "bar"}`,
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		rel             string
		wantContentType string
		wantDisposition string
	}{
		{"main.go", "application/octet-stream", "main.go"},
		{"evil.html", "application/octet-stream", "evil.html"},
		{"data.json", "application/octet-stream", "data.json"},
		{"icon.svg", "image/svg+xml", "icon.svg"},
	}

	for _, tc := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/raw?path="+url.QueryEscape(tc.rel), nil)
		s.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d (body: %s)", tc.rel, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", tc.rel, got)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.wantContentType) {
			t.Errorf("%s: Content-Type = %q, want %q", tc.rel, got, tc.wantContentType)
		}
		if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, tc.wantDisposition) {
			t.Errorf("%s: Content-Disposition = %q, want %q", tc.rel, got, tc.wantDisposition)
		}
	}
}

func TestContentSecurityPolicyHeader(t *testing.T) {
	s, _ := newTestServer(t)

	endpoints := []string{"/", "/api/meta", "/api/raw?path=main.go"}
	for _, ep := range endpoints {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		s.ServeHTTP(rec, req)

		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatalf("%s: missing Content-Security-Policy header", ep)
		}
		if csp != contentSecurityPolicy {
			t.Errorf("%s: got CSP %q, want %q", ep, csp, contentSecurityPolicy)
		}

		requiredDirectives := []string{
			"default-src 'self'",
			"script-src 'self'",
			"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
			"font-src 'self' https://fonts.gstatic.com",
			"img-src 'self' data: https: http:",
			"connect-src 'self'",
			"object-src 'none'",
			"base-uri 'self'",
			"frame-ancestors 'none'",
			"form-action 'none'",
		}
		for _, dir := range requiredDirectives {
			if !strings.Contains(csp, dir) {
				t.Errorf("%s: CSP missing directive %q", ep, dir)
			}
		}
	}
}

func TestFileSave(t *testing.T) {
	s, root := newTestServer(t)

	// 1. Save changes to existing file
	newContent := "package main\n\nfunc main() {\n\tgreet(\"updated\")\n}\n"
	body, _ := json.Marshal(map[string]string{
		"path":    "main.go",
		"content": newContent,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/file/save", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	diskBytes, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("failed to read disk file: %v", err)
	}
	if string(diskBytes) != newContent {
		t.Fatalf("file content mismatch: got %q, want %q", string(diskBytes), newContent)
	}

	// 2. Reject path traversal
	body, _ = json.Marshal(map[string]string{
		"path":    "../../escape.txt",
		"content": "bad",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/file/save", bytes.NewReader(body))
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got %d", rec.Code)
	}

	// 3. Reject directory target
	body, _ = json.Marshal(map[string]string{
		"path":    "sub",
		"content": "bad",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/file/save", bytes.NewReader(body))
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for directory target, got %d", rec.Code)
	}
}

func TestFSNew(t *testing.T) {
	s, root := newTestServer(t)

	// 1. Create new file with nested directory
	body, _ := json.Marshal(map[string]string{
		"path": "pkg/nested/new_file.go",
		"type": "file",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fs/new", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	targetPath := filepath.Join(root, "pkg", "nested", "new_file.go")
	st, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("expected file to exist at %s: %v", targetPath, err)
	}
	if st.IsDir() {
		t.Fatalf("expected regular file, got directory")
	}

	// 2. Creating already existing file should fail with 409 Conflict
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/fs/new", bytes.NewReader(body))
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d", rec.Code)
	}

	// 3. Create new directory
	body, _ = json.Marshal(map[string]string{
		"path": "src/components",
		"type": "dir",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/fs/new", bytes.NewReader(body))
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	dirPath := filepath.Join(root, "src", "components")
	st, err = os.Stat(dirPath)
	if err != nil || !st.IsDir() {
		t.Fatalf("expected directory to exist at %s", dirPath)
	}

	// 4. Reject traversal
	body, _ = json.Marshal(map[string]string{
		"path": "../evil",
		"type": "file",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/fs/new", bytes.NewReader(body))
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got %d", rec.Code)
	}
}

func TestTableEndpointServesRowsAndFileFlagsTables(t *testing.T) {
	t.Parallel()
	s, root := newTestServer(t)
	if err := os.WriteFile(filepath.Join(root, "bench.csv"), []byte("repo,files\nlinux,81902\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bench.tsv"), []byte("repo\tfiles\nlinux\t81902\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, m := get(t, s, "/api/table?path=bench.csv")
	if code != 200 {
		t.Fatalf("status %d: %v", code, m)
	}
	rows, _ := m["rows"].([]any)
	if len(rows) != 1 || m["cols"] != float64(2) {
		t.Errorf("response = %v", m)
	}
	for path, want := range map[string]bool{"bench.csv": true, "bench.tsv": true, "main.go": false} {
		if _, m := get(t, s, "/api/file?path="+path); m["table"] != want {
			t.Errorf("%s: table = %v, want %v", path, m["table"], want)
		}
	}
	if code, _ := get(t, s, "/api/table?path=main.go"); code != 415 {
		t.Errorf("non-table file: status %d, want 415", code)
	}
}

func TestMarkdownPreview(t *testing.T) {
	t.Parallel()
	s, root := newTestServer(t)
	src := "# Hello World\n\nSee [deep](sub/deep.py#L2).\n\n## API_reference\n\n```go\nfunc main() {}\n```\n\n- [x] done\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	code, m := get(t, s, "/api/markdown?path=README.md")
	if code != 200 {
		t.Fatalf("status %d: %v", code, m)
	}
	out, _ := m["html"].(string)
	for _, want := range []string{
		`<h1 id="hello-world" data-line="1">Hello World</h1>`,
		`<h2 id="api_reference" data-line="5">`,
		`<pre class="md-code" data-line="8" data-lang="go"><code><i class=k>func</i>`,
		`<a href="sub/deep.py#L2">deep</a>`,
		`type="checkbox"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview is missing %q:\n%s", want, out)
		}
	}

	if _, m := get(t, s, "/api/file?path=README.md"); m["markdown"] != true {
		t.Errorf("file response should flag Markdown, got %v", m["markdown"])
	}
	if code, _ := get(t, s, "/api/markdown?path=main.go"); code != 415 {
		t.Errorf("non-Markdown file: status %d, want 415", code)
	}
	if code, _ := get(t, s, "/api/markdown?path=../x.md"); code != 400 {
		t.Errorf("path outside the root: status %d, want 400", code)
	}
}

func TestSettingsAPIEndpoints(t *testing.T) {
	isolateSettings(t)
	root := t.TempDir()
	srv := agentServer(t, root, "echo")

	// 1. GET /api/settings
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings failed: code %d, body: %s", rec.Code, rec.Body.String())
	}
	var getResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("invalid GET json response: %v", err)
	}
	if _, ok := getResp["schema"]; !ok {
		t.Errorf("missing schema in GET /api/settings response")
	}

	// 2. POST /api/settings with key/value updates
	payload := map[string]any{
		"editor.tabSize":              2,
		"diffEditor.renderSideBySide": false,
	}
	b, _ := json.Marshal(payload)
	postReq := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(b))
	postReq.Host = "127.0.0.1:7777"
	postReq.Header.Set("Origin", "http://127.0.0.1:7777")
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	srv.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/settings failed: code %d, body: %s", postRec.Code, postRec.Body.String())
	}

	// 3. Verify changes persisted
	m := settings.ReadMergedMap()
	if m["editor.tabSize"] != float64(2) && m["editor.tabSize"] != 2 {
		t.Errorf("expected editor.tabSize 2, got %v", m["editor.tabSize"])
	}
	if m["diffEditor.renderSideBySide"] != false {
		t.Errorf("expected diffEditor.renderSideBySide false, got %v", m["diffEditor.renderSideBySide"])
	}

	// 4. POST /api/settings with raw JSON
	rawPayload := map[string]any{
		"raw": "{\n  \"editor.fontSize\": 15,\n  \"workbench.colorTheme\": \"nord\"\n}\n",
	}
	rb, _ := json.Marshal(rawPayload)
	rawReq := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(rb))
	rawReq.Host = "127.0.0.1:7777"
	rawReq.Header.Set("Origin", "http://127.0.0.1:7777")
	rawReq.Header.Set("Content-Type", "application/json")
	rawRec := httptest.NewRecorder()
	srv.ServeHTTP(rawRec, rawReq)
	if rawRec.Code != http.StatusOK {
		t.Fatalf("POST /api/settings raw failed: code %d, body: %s", rawRec.Code, rawRec.Body.String())
	}

	m2 := settings.ReadMergedMap()
	if m2["editor.fontSize"] != float64(15) && m2["editor.fontSize"] != 15 {
		t.Errorf("expected editor.fontSize 15 from raw, got %v", m2["editor.fontSize"])
	}
	if m2["workbench.colorTheme"] != "nord" {
		t.Errorf("expected workbench.colorTheme nord from raw, got %v", m2["workbench.colorTheme"])
	}
}

// ---------------------------------------------------------------- Session tests

func TestSessionAPIEndpoints(t *testing.T) {
	// Isolate session path to a temp dir
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	root := t.TempDir()
	ix := NewIndex(root)
	s := NewServer(ix, nil, "/rev-999/")

	// 1. GET /rev-999/api/session initially
	req := httptest.NewRequest(http.MethodGet, "/rev-999/api/session", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /rev-999/api/session failed: code %d, body %s", rec.Code, rec.Body.String())
	}
	var initResp WorkspaceSession
	if err := json.Unmarshal(rec.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("unmarshal session failed: %v", err)
	}
	if len(initResp.Tabs) != 0 {
		t.Errorf("expected 0 tabs initially, got %d", len(initResp.Tabs))
	}

	// 2. POST /rev-999/api/session with tabs and openDirs
	active := 1
	payload := map[string]any{
		"tabs": []map[string]string{
			{"path": "foo.go"},
			{"path": "bar.go"},
		},
		"active":   active,
		"openDirs": []string{"pkg", "cmd"},
	}
	b, _ := json.Marshal(payload)
	postReq := httptest.NewRequest(http.MethodPost, "/rev-999/api/session", bytes.NewReader(b))
	postReq.Host = "127.0.0.1:7777"
	postReq.Header.Set("Origin", "http://127.0.0.1:7777")
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	s.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /rev-999/api/session failed: code %d, body %s", postRec.Code, postRec.Body.String())
	}

	// 3. Verify file was written to disk under rev-999.json
	sessFile := filepath.Join(tmpDir, "px0", "sessions", "rev-999.json")
	if _, err := os.Stat(sessFile); err != nil {
		t.Fatalf("session file not found on disk: %v", err)
	}

	// 4. Create a new server instance with same basePath and check that state is restored
	s2 := NewServer(ix, nil, "/rev-999/")
	sess2 := s2.session.Get()
	if len(sess2.Tabs) != 2 {
		t.Fatalf("expected 2 tabs in restored session, got %d", len(sess2.Tabs))
	}
	if sess2.Tabs[0].Path != "foo.go" || sess2.Tabs[1].Path != "bar.go" {
		t.Errorf("unexpected tab paths: %+v", sess2.Tabs)
	}
	if sess2.Active != 1 {
		t.Errorf("expected active tab 1, got %d", sess2.Active)
	}
	if len(sess2.OpenDirs) != 2 || sess2.OpenDirs[0] != "pkg" || sess2.OpenDirs[1] != "cmd" {
		t.Errorf("unexpected openDirs: %+v", sess2.OpenDirs)
	}
}

func TestPRDraftSessionPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	root := t.TempDir()
	ix := NewIndex(root)
	s := NewServer(ix, nil, "/rev-42/")
	pr := &prSession{
		diffBase: "HEAD",
	}
	s.SetPR(pr)

	// Add a draft comment via POST /rev-42/api/pr/comments
	payload := map[string]any{
		"path": "main.go",
		"line": 25,
		"side": "RIGHT",
		"body": "Check this line",
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/rev-42/api/pr/comments", bytes.NewReader(b))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /rev-42/api/pr/comments failed: code %d, body %s", rec.Code, rec.Body.String())
	}

	// Create a second server instance simulating a restart
	s2 := NewServer(ix, nil, "/rev-42/")
	pr2 := &prSession{
		diffBase: "HEAD",
	}
	s2.SetPR(pr2)

	// Verify drafts were restored in pr2
	pr2.mu.Lock()
	defer pr2.mu.Unlock()
	if len(pr2.comments) != 1 {
		t.Fatalf("expected 1 restored draft comment, got %d", len(pr2.comments))
	}
	if pr2.comments[0].Path != "main.go" || pr2.comments[0].Body != "Check this line" {
		t.Errorf("unexpected restored comment: %+v", pr2.comments[0])
	}
}

func postDraft(t *testing.T, s *Server, prefix, body string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"path": "main.go", "line": 25, "side": "RIGHT", "body": body})
	req := httptest.NewRequest(http.MethodPost, prefix+"/api/pr/comments", bytes.NewReader(b))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s/api/pr/comments failed: code %d, body %s", prefix, rec.Code, rec.Body.String())
	}
}

// A direct `px0 <pr-url>` run has no base path and checks out into a new
// temporary directory each time; drafts must still come back.
func TestPRDraftsSurviveNewCheckoutRoot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pt := PRTarget{Provider: "github", Owner: "acme", Repo: "widget", Number: 7}

	s := NewServer(NewIndex(t.TempDir()), nil)
	s.SetPR(&prSession{diffBase: "HEAD", target: pt})
	postDraft(t, s, "", "first run")

	s2 := NewServer(NewIndex(t.TempDir()), nil)
	pr2 := &prSession{diffBase: "HEAD", target: pt}
	s2.SetPR(pr2)
	if len(pr2.comments) != 1 || pr2.comments[0].Body != "first run" {
		t.Fatalf("expected the draft from the earlier run, got %+v", pr2.comments)
	}

	other := &prSession{diffBase: "HEAD", target: PRTarget{Provider: "github", Owner: "acme", Repo: "widget", Number: 8}}
	NewServer(NewIndex(t.TempDir()), nil).SetPR(other)
	if len(other.comments) != 0 {
		t.Fatalf("another PR must not see these drafts, got %+v", other.comments)
	}
}

// Drafts an older px0 saved under the base-path file move to the PR's file.
func TestPRDraftsMigrateFromBasePathFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	newSessionManager("/w/abc/", root).Update(func(ws *WorkspaceSession) {
		ws.Drafts = []prComment{{ID: 3, Path: "a.go", Line: 1, Side: "RIGHT", Body: "old"}}
	})

	pt := PRTarget{Provider: "github", Owner: "acme", Repo: "widget", Number: 7}
	pr := &prSession{diffBase: "HEAD", target: pt}
	NewServer(NewIndex(root), nil, "/w/abc/").SetPR(pr)
	if len(pr.comments) != 1 || pr.comments[0].Body != "old" || pr.nextID != 3 {
		t.Fatalf("expected the migrated draft, got %+v (nextID %d)", pr.comments, pr.nextID)
	}
	if got := newPRSessionManager(pt).Get().Drafts; len(got) != 1 {
		t.Fatalf("expected the draft in the PR session file, got %+v", got)
	}
	if got := newSessionManager("/w/abc/", root).Get().Drafts; len(got) != 0 {
		t.Fatalf("expected the base-path file's drafts cleared, got %+v", got)
	}
}

// ---------------------------------------------------------------- Search cancellation tests

func TestSearchContextCancellation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		content := "package main\n\nfunc SearchTarget() {}\n"
		if err := os.WriteFile(filepath.Join(root, "file_"+string(rune('a'+i))+".go"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ix := NewIndex(root)
	ix.Build()

	// 1. Normal search should find all files
	res, _, err := SearchContext(context.Background(), ix, SearchOpts{Query: "SearchTarget"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 20 {
		t.Fatalf("expected 20 file hits, got %d", len(res))
	}

	// 2. Search with already canceled context returns immediately
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = SearchContext(ctxCanceled, ix, SearchOpts{Query: "SearchTarget"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// 3. Search cancelled while in-flight terminates worker goroutines
	ctxTimed, cancelTimed := context.WithTimeout(context.Background(), 10*time.Microsecond)
	defer cancelTimed()
	_, _, err = SearchContext(ctxTimed, ix, SearchOpts{Query: "SearchTarget"})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation or deadline error, got %v", err)
	}
}

func TestHandleSearchWithCanceledRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Hello", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel() // cancel immediately to simulate aborted request
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.handleSearch(rec, req)

	// Since context was canceled, handleSearch should return cleanly without writing a 400 error
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("expected canceled search to return quietly, got status %d", rec.Code)
	}
}

// ---------------------------------------------------------------- Benchmarks

func BenchmarkSnip(b *testing.B) {
	rawLine := []byte("    func handleSearchRequest(w http.ResponseWriter, r *http.Request, query string) (SearchResult, error) {")
	from := 9
	to := 28

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = snip(rawLine, from, to)
	}
}

func BenchmarkSearchLiteralInMemory(b *testing.B) {
	root := b.TempDir()
	content := "package main\n\n// TargetSearchToken is here\nfunc TargetSearchToken() string { return \"ok\" }\n"
	for i := 0; i < 20; i++ {
		_ = os.WriteFile(filepath.Join(root, fmt.Sprintf("file_%d.go", i)), []byte(content), 0o644)
	}
	ix := NewIndex(root)
	ix.Build()

	opts := SearchOpts{
		Query:    "TargetSearchToken",
		MaxFiles: 50,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = SearchContext(context.Background(), ix, opts)
	}
}

func BenchmarkSearchDefWord(b *testing.B) {
	root := b.TempDir()
	missContent := strings.Repeat("package main\nfunc helperFunc(x int) int { return x + 1 }\n", 50)
	for i := 0; i < 40; i++ {
		_ = os.WriteFile(filepath.Join(root, fmt.Sprintf("miss_%d.go", i)), []byte(missContent), 0o644)
	}
	hitContent := "package main\nfunc TargetSymbolDef() string { return \"ok\" }\n"
	_ = os.WriteFile(filepath.Join(root, "hit.go"), []byte(hitContent), 0o644)

	ix := NewIndex(root)
	ix.Build()

	opts := SearchOpts{
		Query:        "TargetSymbolDef",
		Word:         true,
		Case:         true,
		classifyDefs: true,
		MaxFiles:     50,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = SearchContext(context.Background(), ix, opts)
	}
}

func BenchmarkSortNodes(b *testing.B) {
	base := make([]Node, 64)
	for i := range base {
		base[i] = Node{
			Name: fmt.Sprintf("File_%02d_Component.go", 64-i),
			Path: fmt.Sprintf("pkg/File_%02d_Component.go", 64-i),
			Dir:  i%5 == 0,
		}
	}
	work := make([]Node, len(base))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		copy(work, base)
		sortNodes(work)
	}
}

func BenchmarkHandleFileScrollChunk(b *testing.B) {
	wd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	ix := NewIndex(wd)
	ix.Build()
	srv := &Server{ix: ix, lsp: newLSPManager(wd, false), diffBase: "HEAD"}
	req := httptest.NewRequest(http.MethodGet, "/api/file?path=server.go&start=300&count=300", nil)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleFile(w, req)
	}
}

func TestFindIndexingStatus(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "testfile.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(root)
	srv := NewServer(ix, nil)

	// Before indexing: indexing should be true and files empty
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/find?q=test", nil)
	srv.handleFind(w, req)

	var payload struct {
		Results  []any `json:"results"`
		Indexing bool  `json:"indexing"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode find response: %v", err)
	}
	if !payload.Indexing {
		t.Fatalf("expected indexing: true before build")
	}

	// Build index
	ix.Build()

	// After indexing: indexing should be false and results found
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/find?q=testfile", nil)
	srv.handleFind(w2, req2)

	var payload2 struct {
		Results  []any `json:"results"`
		Indexing bool  `json:"indexing"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &payload2); err != nil {
		t.Fatalf("decode find response 2: %v", err)
	}
	if payload2.Indexing {
		t.Fatalf("expected indexing: false after build")
	}
	if len(payload2.Results) == 0 {
		t.Fatalf("expected results after build, got 0")
	}
}
