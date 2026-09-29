package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func generateFileEntries(n int) []FileEntry {
	dirs := []string{"src", "pkg", "internal", "cmd", "pkg/util", "web/src/components", "api/v1", "services"}
	names := []string{"server.go", "client.go", "handler.go", "types.go", "utils.go", "index.ts", "app.py", "main.rs"}
	entries := make([]FileEntry, n)
	for i := 0; i < n; i++ {
		d := dirs[i%len(dirs)]
		name := fmt.Sprintf("%d_%s", i, names[i%len(names)])
		p := fmt.Sprintf("%s/%s", d, name)
		entries[i] = FileEntry{
			Path:      p,
			Name:      name,
			lower:     strings.ToLower(p),
			nameStart: len(p) - len(name),
		}
	}
	return entries
}

func BenchmarkFuzzyFind_1K(b *testing.B) {
	files := generateFileEntries(1000)
	query := "handler"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FuzzyFind(files, query, 50)
	}
}

func BenchmarkFuzzyFind_10K(b *testing.B) {
	files := generateFileEntries(10000)
	query := "handler"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FuzzyFind(files, query, 50)
	}
}

func BenchmarkFuzzyFind_50K(b *testing.B) {
	files := generateFileEntries(50000)
	query := "srvutil"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FuzzyFind(files, query, 50)
	}
}

func BenchmarkFuzzyScore(b *testing.B) {
	e := &FileEntry{
		Path:      "internal/server/http_server.go",
		Name:      "http_server.go",
		lower:     "internal/server/http_server.go",
		nameStart: 16,
	}
	q := "httpserver"
	origQ := "httpserver"
	pos := make([]int, 0, len(e.Path))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = fuzzyScore(q, origQ, e, pos)
	}
}

func BenchmarkIgnoreSetMatch(b *testing.B) {
	patterns := []string{
		"*.tmp",
		"build/",
		"dist/",
		"node_modules/",
		".git/",
		"docs/**/draft.md",
		"!build/keep.txt",
		"vendor/",
	}
	ig := newIgnoreSet(patterns)
	testPaths := []string{
		"src/server/handler.go",
		"build/output.js",
		"node_modules/react/index.js",
		"docs/internal/draft.md",
		"pkg/vendor.go",
		"tmp/test.tmp",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p := testPaths[i%len(testPaths)]
		_ = ig.match(p, false)
	}
}

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
	d := newDoc(src, "main.go")
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
	d := newDoc(src, "app.py")
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
		_ = newDoc(src, "example.go")
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
