package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Windowed lexing must agree with a full-file pass, or the view changes under
// the user when the background pass lands.
func fixtures(t *testing.T) []string {
	var files []string
	root := os.Getenv("PX0_FIXTURES")
	if root == "" {
		if _, err := os.Stat("../../go.mod"); err == nil {
			root = "../.."
		} else {
			root = "."
		}
	}
	filepath.WalkDir(root, func(p string, de os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if de.IsDir() {
			name := de.Name()
			if name == ".git" || name == "bench-repos" || name == "node_modules" || name == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".go", ".py", ".js", ".ts", ".c", ".h", ".rs", ".java", ".css", ".html", ".md", ".sh", ".json", ".yaml":
			if st, err := os.Stat(p); err == nil && st.Size() > 400 && st.Size() < 3<<20 {
				files = append(files, p)
			}
		}
		return nil
	})
	maxFiles := 400
	if testing.Short() {
		maxFiles = 10
	}
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	if len(files) == 0 {
		t.Skip("no fixtures")
	}
	return files
}

func TestWindowMatchesFullPass(t *testing.T) {
	files := fixtures(t)
	var checked, mismatchFiles, mismatchLines, totalLines int
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil || IsBinary(data) {
			continue
		}
		src := strings.ReplaceAll(string(data), "\r\n", "\n")
		d := NewDoc(src, p)
		if d.Total < hlChunk+2 {
			continue // single chunk: nothing to compare
		}
		checked++
		full := d.tokenise(src, d.Total)

		bad := 0
		for c := 0; c*hlChunk < d.Total; c++ {
			win, exact := d.chunk(c)
			if exact {
				t.Fatalf("%s: chunk %d claimed exact before any full pass ran", p, c)
			}
			for i, got := range win {
				line := c*hlChunk + i
				totalLines++
				if got != full[line] {
					bad++
					if bad == 1 {
						t.Logf("%s:%d\n  window: %q\n  full:   %q", p, line+1, got, full[line])
					}
				}
			}
		}
		if bad > 0 {
			mismatchFiles++
			mismatchLines += bad
		}
	}
	t.Logf("windowed vs full: %d multi-chunk files, %d lines; %d files differ, %d lines differ (%.4f%%)",
		checked, totalLines, mismatchFiles, mismatchLines, float64(mismatchLines)*100/float64(max(totalLines, 1)))
	// Windows are an approximation by construction: a string or comment longer
	// than hlContext cannot be resolved from a window alone. Hold the line at a
	// small fraction so a regression in context handling still shows up.
	if totalLines > 0 && float64(mismatchLines)/float64(totalLines) > 0.01 {
		t.Errorf("windowed lexing drifts too far from the full pass: %d/%d lines", mismatchLines, totalLines)
	}
}

// Whatever the windows guessed, the background pass is authoritative and every
// chunk must match it once that pass has run.
func TestBackgroundPassIsExact(t *testing.T) {
	for _, p := range fixtures(t) {
		data, err := os.ReadFile(p)
		if err != nil || IsBinary(data) {
			continue
		}
		src := strings.ReplaceAll(string(data), "\r\n", "\n")
		d := NewDoc(src, p)
		if d.Total < hlChunk+2 {
			continue
		}
		d.chunk(0) // seed a window first, so the pass has something to overwrite
		d.backgroundPass()
		full := d.tokenise(src, d.Total)
		for c := 0; c*hlChunk < d.Total; c++ {
			win, exact := d.chunk(c)
			if !exact {
				t.Fatalf("%s: chunk %d not exact after background pass", p, c)
			}
			for i, got := range win {
				if line := c*hlChunk + i; got != full[line] {
					t.Fatalf("%s:%d after background pass\n got %q\nwant %q", p, line+1, got, full[line])
				}
			}
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Windows and the background pass race on the same chunk map by design.
func TestConcurrentChunkAccess(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString("func f() { /* c */ s := `raw` }\n")
	}
	d := NewDoc(sb.String(), "x.go")

	done := make(chan struct{})
	go func() { d.backgroundPass(); close(done) }()

	errs := make(chan error, 64)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			loops := 40
			if testing.Short() {
				loops = 4
			}
			for i := 0; i < loops; i++ {
				start := ((w*13 + i*7) % 5) * hlChunk
				lines, _ := d.Lines(start, start+hlChunk)
				if len(lines) == 0 {
					errs <- fmt.Errorf("empty window at %d", start)
					return
				}
				for _, l := range lines {
					if !strings.Contains(l, "func") {
						errs <- fmt.Errorf("garbled line at %d: %q", start, l)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	<-done
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// Generated files exist with a few multi-megabyte lines. A window measured in
// lines would cover the whole file there, so the byte cap has to take over.
func TestGiantLinesDoNotBlowUpTheWindow(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("package x\n")
	for i := 0; i < 8; i++ {
		sb.WriteString("var s")
		sb.WriteString(strings.Repeat("0", 1))
		sb.WriteString(" = \"")
		sb.WriteString(strings.Repeat("abcdefgh", 200_000)) // 1.6 MB on one line
		sb.WriteString("\"\n")
	}
	src := sb.String()
	d := NewDoc(src, "giant.go")

	if d.MaxCols > maxReportedCols {
		t.Errorf("MaxCols = %d, must be capped at %d so the client can size itself", d.MaxCols, maxReportedCols)
	}

	lines, _ := d.Lines(0, d.Total)
	if len(lines) != d.Total {
		t.Fatalf("got %d lines, want %d", len(lines), d.Total)
	}
	// Content must survive even though this window is not highlighted.
	if !strings.Contains(lines[0], "package") {
		t.Errorf("first line lost its content: %q", lines[0])
	}
	// The last element is the empty string after the file's final newline.
	for i, l := range lines[1 : len(lines)-1] {
		if len(l) == 0 {
			t.Errorf("line %d came back empty", i+2)
			break
		}
	}
	if lines[len(lines)-1] != "" {
		t.Errorf("trailing line = %q, want empty", lines[len(lines)-1])
	}
	// The whole point: no chunk may hold vastly more than the source it covers.
	total := 0
	for _, l := range lines {
		total += len(l)
	}
	if total > 4*len(src) {
		t.Errorf("rendered %d bytes for %d bytes of source", total, len(src))
	}
}

func TestEvictRemovesDocument(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "test.go")
	if err := os.WriteFile(tmp, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(tmp, "test.go")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if d == nil {
		t.Fatal("expected doc, got nil")
	}
	cache.mu.Lock()
	usedBefore := cache.used
	_, foundBefore := cache.items[d.key]
	cache.mu.Unlock()
	if !foundBefore || usedBefore == 0 {
		t.Fatalf("expected doc in cache, found=%v used=%d", foundBefore, usedBefore)
	}

	if !Evict(tmp) {
		t.Fatalf("expected Evict(%q) to return true", tmp)
	}

	cache.mu.Lock()
	usedAfter := cache.used
	_, foundAfter := cache.items[d.key]
	cache.mu.Unlock()
	if foundAfter {
		t.Fatalf("expected doc to be removed from cache")
	}
	if usedAfter >= usedBefore {
		t.Fatalf("expected used bytes to decrease, before=%d after=%d", usedBefore, usedAfter)
	}

	// Evict again on already evicted file returns false
	if Evict(tmp) {
		t.Fatalf("expected second Evict to return false")
	}
}

func TestEvictAllClearsCache(t *testing.T) {
	tmp1 := filepath.Join(t.TempDir(), "a.go")
	tmp2 := filepath.Join(t.TempDir(), "b.go")
	if err := os.WriteFile(tmp1, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp2, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(tmp1, "a.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(tmp2, "b.go"); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	countBefore := len(cache.items)
	cache.mu.Unlock()
	if countBefore == 0 {
		t.Fatalf("expected items in cache")
	}

	EvictAll()

	cache.mu.Lock()
	countAfter := len(cache.items)
	usedAfter := cache.used
	cache.mu.Unlock()
	if countAfter != 0 || usedAfter != 0 {
		t.Fatalf("expected empty cache after EvictAll, got count=%d used=%d", countAfter, usedAfter)
	}
}

func TestHighlightDiff(t *testing.T) {
	// Empty diff
	if hunks := HighlightDiff("foo.go", ""); len(hunks) != 0 {
		t.Fatalf("expected 0 hunks for empty diff, got %d", len(hunks))
	}

	diff := `@@ -10,3 +10,4 @@
 func greet() {
-	return "hello"
+	// new greeting
+	return "hello world"
 }`

	hunks := HighlightDiff("greet.go", diff)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 10 || h.NewStart != 10 {
		t.Fatalf("unexpected hunk starts: old=%d new=%d", h.OldStart, h.NewStart)
	}
	if len(h.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(h.Rows))
	}

	// Row 0: ctx (func greet() {)
	if h.Rows[0].Type != "ctx" || h.Rows[0].Text != "\tfunc greet() {" && !strings.Contains(h.Rows[0].HTML, "class=k") {
		t.Errorf("row 0 missing keyword class: %s", h.Rows[0].HTML)
	}

	// Row 1: del (return "hello")
	if h.Rows[1].Type != "del" || h.Rows[1].OldLine != 11 || h.Rows[1].At != 11 {
		t.Errorf("row 1 incorrect line numbers: %+v", h.Rows[1])
	}
	if !strings.Contains(h.Rows[1].HTML, "class=s") {
		t.Errorf("row 1 missing string class: %s", h.Rows[1].HTML)
	}

	// Row 2: add (// new greeting)
	if h.Rows[2].Type != "add" || h.Rows[2].NewLine != 11 {
		t.Errorf("row 2 incorrect line numbers: %+v", h.Rows[2])
	}
	if !strings.Contains(h.Rows[2].HTML, "class=c") {
		t.Errorf("row 2 missing comment class: %s", h.Rows[2].HTML)
	}

	// Row 3: add (return "hello world")
	if h.Rows[3].Type != "add" || h.Rows[3].NewLine != 12 {
		t.Errorf("row 3 incorrect line numbers: %+v", h.Rows[3])
	}
	if !strings.Contains(h.Rows[3].HTML, "class=k") || !strings.Contains(h.Rows[3].HTML, "class=s") {
		t.Errorf("row 3 missing keyword or string class: %s", h.Rows[3].HTML)
	}
}

func TestHighlightDiffJSONFragments(t *testing.T) {
	// A dependency moved between objects, as in a package.json diff. Neither
	// hunk contains the opening of the document or its enclosing objects.
	diff := `@@ -43,3 +43,2 @@
     "@ali/deep": "3.3.2",
-    "sdk": "0.2.0",
     "next": "1.0.0"
@@ -81,4 +80,5 @@
     "xss": "^1.0.14"
   },
   "devDependencies": {
+    "sdk": "0.2.0",
     "next": "1.0.0"`
	for _, name := range []string{"package.json", "settings.jsonc", "schema.avsc"} {
		t.Run(name, func(t *testing.T) {
			hunks := HighlightDiff(name, diff)
			if len(hunks) != 2 {
				t.Fatalf("expected two hunks, got %d", len(hunks))
			}
			for _, h := range hunks {
				for _, row := range h.Rows {
					if strings.Contains(row.HTML, "class=err") {
						t.Errorf("false syntax error in %q: %s", row.Text, row.HTML)
					}
					if strings.Contains(row.Text, `"sdk"`) {
						if !strings.Contains(row.HTML, `<i class=nt>"sdk"</i>`) ||
							!strings.Contains(row.HTML, `<i class=s>"0.2.0"</i>`) {
							t.Errorf("lost key/value highlighting: %s", row.HTML)
						}
					}
				}
			}
		})
	}
}

func TestJSONDiffBlockComments(t *testing.T) {
	for _, src := range []string{
		"/*",
		"/* unterminated\n\"key\": true, <tag>&",
		"/* nested /* opener is comment text */",
		"/* comment ending with stars ***/",
		"/*" + strings.Repeat("*", 24000),
		strings.Repeat("/* ", 8000),
	} {
		raw := strings.Split(src, "\n")
		lines := highlightLines(jsonDiffLexer, src, len(raw))
		for i, line := range lines {
			text := strings.ReplaceAll(strings.ReplaceAll(line, "<i class=c>", ""), "</i>", "")
			if text != htmlEscaper.Replace(raw[i]) {
				t.Fatalf("comment text or highlighting changed on line %d (input length %d)", i, len(src))
			}
			if !strings.Contains(line, "<i class=c>") {
				t.Fatalf("missing comment highlighting on line %d", i)
			}
		}
	}

	// The first closing delimiter ends the comment; following JSON tokens
	// must regain their normal colours, on both sides of a diff.
	hunks := HighlightDiff("settings.jsonc", "@@ -1 +1 @@\n-/* old */ \"key\": false\n+/* /* new ***/ \"key\": true")
	for _, row := range hunks[0].Rows {
		if !strings.Contains(row.HTML, `<i class=nt>"key"</i>`) ||
			(!strings.Contains(row.HTML, `<i class=k>true</i>`) && !strings.Contains(row.HTML, `<i class=k>false</i>`)) {
			t.Errorf("JSON highlighting did not resume after block comment: %s", row.HTML)
		}
	}
}

func BenchmarkHighlightDiffUnterminatedJSONComment(b *testing.B) {
	for _, n := range []int{1000, 2000, 4000, 8000} {
		diff := "@@ -1 +1 @@\n-0\n+" + strings.Repeat("/* ", n)
		b.Run(fmt.Sprintf("%d-bytes", n*3), func(b *testing.B) {
			b.SetBytes(int64(n * 3))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = HighlightDiff("settings.jsonc", diff)
			}
		})
	}
}

func BenchmarkHighlightDiff(b *testing.B) {
	diff := `@@ -10,3 +10,4 @@ func greet() {
-	return "hello"
+	// new greeting
+	return "hello world"
 }
`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = HighlightDiff("greet.go", diff)
	}
}

func BenchmarkNewDoc(b *testing.B) {
	src := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}
`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewDoc(src, "main.go")
	}
}

func TestHeadingIDsFollowGitHub(t *testing.T) {
	t.Parallel()
	ids := &headingIDs{seen: map[string]bool{}}
	for _, c := range []struct{ in, want string }{
		{"Getting Started!", "getting-started"},
		{"snake_case API", "snake_case-api"},
		{"Überblick", "überblick"},
		{"Getting Started", "getting-started-1"},
		{"???", "section"},
	} {
		if got := string(ids.Generate([]byte(c.in), 0)); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderMarkdown(t *testing.T) {
	t.Parallel()

	if !IsMarkdown("doc.md") || !IsMarkdown("doc.markdown") || IsMarkdown("code.go") {
		t.Errorf("IsMarkdown incorrect results")
	}

	src := []byte("# Header\n\nParagraph with **bold** and `code`.\n\n```go\nfunc main() {}\n```\n")
	html, err := RenderMarkdown(src)
	if err != nil {
		t.Fatalf("RenderMarkdown returned error: %v", err)
	}
	if len(html) == 0 {
		t.Fatal("expected non-empty rendered markdown")
	}
}

func BenchmarkRenderMarkdown(b *testing.B) {
	src := []byte("# Header\n\nParagraph with **bold** text and list:\n- item 1\n- item 2\n\n```go\npackage main\nfunc main() {}\n```\n")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = RenderMarkdown(src)
	}
}

func BenchmarkHighlightLines_Go(b *testing.B) {
	src := `package main

import (
	"fmt"
	"sync"
	"time"
)

type WorkerPool struct {
	tasks chan func()
	wg    sync.WaitGroup
}

func NewWorkerPool(workers int) *WorkerPool {
	p := &WorkerPool{tasks: make(chan func(), 128)}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for t := range p.tasks {
				t()
			}
		}()
	}
	return p
}
`
	d := NewDoc(src, "main.go")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = d.Lines(0, d.Total)
	}
}

func BenchmarkHighlightLines_Python(b *testing.B) {
	src := `import os
import sys
from dataclasses import dataclass

@dataclass
class Config:
    host: str = "127.0.0.1"
    port: int = 8080
    debug: bool = False

def run_server(cfg: Config):
    print(f"Starting server on {cfg.host}:{cfg.port}")
    if cfg.debug:
        print("Debug mode enabled")
`
	d := NewDoc(src, "app.py")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = d.Lines(0, d.Total)
	}
}

func BenchmarkDocCreation(b *testing.B) {
	src := strings.Repeat("func exampleLine() { return 42 }\n", 200)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = NewDoc(src, "example.go")
	}
}

func BenchmarkColdHighlight_Go(b *testing.B) {
	src := strings.Repeat("func exampleLine(x int) int { return x * 2 }\n", 200)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d := NewDoc(src, "example.go")
		_, _ = d.Lines(0, 300)
	}
}
