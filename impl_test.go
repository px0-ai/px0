package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestImplementation proves the manager issues textDocument/implementation and
// normalises the server's Location array into a NavHit at the implementer.
func TestImplementation(t *testing.T) {
	root := t.TempDir()
	// Line 3 (0-based 2) declares the interface; line 7 (0-based 6) implements it.
	src := "package a\n\ntype Barker interface{ Bark() }\n\ntype Dog struct{}\n\nfunc (Dog) Bark() {}\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	aURI := pathToURI(filepath.Join(root, "a.go"))

	var mu sync.Mutex
	var sawMethod string
	m := fakeCallServer(t, root, func(method string, params json.RawMessage) any {
		mu.Lock()
		sawMethod = method
		mu.Unlock()
		if method == "textDocument/implementation" {
			pos := map[string]any{"line": 6, "character": 9} // 0-based -> a.go:7
			return []any{map[string]any{"uri": aURI, "range": map[string]any{"start": pos, "end": pos}}}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hits, err := m.Implementation(ctx, filepath.Join(root, "a.go"), "a.go", 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := sawMethod
	mu.Unlock()
	if got != "textDocument/implementation" {
		t.Fatalf("sent %q; want textDocument/implementation", got)
	}
	if len(hits) != 1 || hits[0].Path != "a.go" || hits[0].Line != 7 {
		t.Fatalf("hits = %+v; want one hit at a.go:7", hits)
	}
}
