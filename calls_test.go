package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCallServer wires an lspClient to an in-process responder over pipes, so
// the call hierarchy plumbing is exercised without a real language server.
func fakeCallServer(t *testing.T, root string, answer func(method string, params json.RawMessage) any) *lspManager {
	t.Helper()
	def := lspServerDef{Name: "fake", Cmd: []string{"fake"}, Exts: []string{".go"}}
	c := newLSPClient(def, root)
	toServerR, toServerW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	c.in, c.out = toServerW, bufio.NewReader(toClientR)
	go c.readLoop()

	var wmu sync.Mutex
	go func() {
		in := bufio.NewReader(toServerR)
		for {
			msg, err := readFrame(in)
			if err != nil {
				return
			}
			if len(msg.ID) == 0 {
				continue // notifications such as didOpen
			}
			body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": answer(msg.Method, msg.Params)})
			wmu.Lock()
			fmt.Fprintf(toClientW, "Content-Length: %d\r\n\r\n%s", len(body), body)
			wmu.Unlock()
		}
	}()
	t.Cleanup(func() { toServerW.Close(); toClientW.Close() })

	return &lspManager{
		root: root, enabled: true,
		byExt:    map[string]*lspServerDef{".go": &def},
		clients:  map[string]*lspClient{"fake": c},
		starting: map[string]chan struct{}{},
		failed:   map[string]string{},
	}
}

func callItem(name, uri string, line int) map[string]any {
	pos := map[string]any{"line": line, "character": 5}
	rng := map[string]any{"start": pos, "end": pos}
	return map[string]any{"name": name, "kind": 12, "uri": uri, "range": rng, "selectionRange": rng, "data": map[string]any{"token": name}}
}

func siteRange(line int) map[string]any {
	pos := map[string]any{"line": line, "character": 1}
	return map[string]any{"start": pos, "end": pos}
}

func TestCallTrail(t *testing.T) {
	root := t.TempDir()
	src := "package a\n\nfunc A() { C(); B() }\n\nfunc B() {}\n"
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	aURI := pathToURI(filepath.Join(root, "a.go"))
	// A file outside the served root, which is what "ext" means here. It has to
	// be a real path on this platform: a hardcoded "/usr/lib/go/..." is not
	// absolute on Windows, where filepath.Rel resolves it back inside root and
	// the item stops being external at all.
	extDir := t.TempDir()
	extPath := filepath.Join(extDir, "print.go")
	if err := os.WriteFile(extPath, []byte("package fmt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extURI := pathToURI(extPath)

	var mu sync.Mutex
	sent := map[string]string{}
	m := fakeCallServer(t, root, func(method string, params json.RawMessage) any {
		mu.Lock()
		sent[method] = string(params)
		mu.Unlock()
		switch method {
		case "textDocument/prepareCallHierarchy":
			return []any{callItem("B", aURI, 4)}
		case "callHierarchy/incomingCalls":
			return []any{
				map[string]any{"from": callItem("A", aURI, 2), "fromRanges": []any{siteRange(2), siteRange(2)}},
				map[string]any{"from": callItem("Println", extURI, 9), "fromRanges": []any{siteRange(12)}},
			}
		case "callHierarchy/outgoingCalls":
			return []any{
				map[string]any{"to": callItem("B", aURI, 4), "fromRanges": []any{siteRange(2)}},
				map[string]any{"to": callItem("C", extURI, 0), "fromRanges": []any{siteRange(1)}},
			}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	roots, err := m.PrepareCalls(ctx, filepath.Join(root, "a.go"), "a.go", 5, 5)
	if err != nil || len(roots) != 1 {
		t.Fatalf("PrepareCalls = %v, %v; want one root", roots, err)
	}
	b := roots[0]
	if b.Name != "B" || b.Path != "a.go" || b.Line != 5 || b.Kind != "func" {
		t.Errorf("root = %+v, want B at a.go:5 (func)", b)
	}

	callers, err := m.Calls(ctx, "a.go", b.Item, false)
	if err != nil || len(callers) != 2 {
		t.Fatalf("incoming = %v, %v; want two callers", callers, err)
	}
	mu.Lock()
	echoed := sent["callHierarchy/incomingCalls"]
	mu.Unlock()
	if !strings.Contains(echoed, `"token":"B"`) {
		t.Errorf("item was not echoed verbatim to the server: %s", echoed)
	}
	// Sorted by path: the absolute external path sorts before "a.go".
	// Path is what the UI is given, which is always slash-separated.
	ext, a := callers[0], callers[1]
	if !ext.Ext || ext.Path != filepath.ToSlash(extPath) || !m.Allowed(extPath) {
		t.Errorf("external caller = %+v, allowed=%v; want ext and allowlisted", ext, m.Allowed(extPath))
	}
	if a.Name != "A" || a.SitePath != "a.go" || fmt.Sprint(a.Sites) != "[3]" {
		t.Errorf("caller A = %+v, want call site a.go:[3] deduplicated", a)
	}

	callees, err := m.Calls(ctx, "a.go", a.Item, true)
	if err != nil || len(callees) != 2 {
		t.Fatalf("outgoing = %v, %v; want two callees", callees, err)
	}
	if callees[0].Name != "C" || callees[1].Name != "B" {
		t.Errorf("callees = %s, %s; want call order C, B", callees[0].Name, callees[1].Name)
	}
	if callees[0].SitePath != "a.go" || fmt.Sprint(callees[0].Sites) != "[2]" {
		t.Errorf("callee C sites = %s %v, want a.go [2] (inside the caller)", callees[0].SitePath, callees[0].Sites)
	}
}

// An item arrives from the browser, so its path must not open the filesystem.
func TestCallTrailForgedItem(t *testing.T) {
	root := t.TempDir()
	m := fakeCallServer(t, root, func(string, json.RawMessage) any { return []any{} })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A path outside the root, spelled for this platform so the check means the
	// same thing everywhere.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	forged, _ := json.Marshal(callItem("x", pathToURI(outside), 0))
	if _, err := m.Calls(ctx, "a.go", string(forged), false); err != nil {
		t.Fatalf("Calls: %v", err)
	}
	if m.Allowed(outside) {
		t.Error("a browser-supplied item allowlisted a path outside the tree")
	}
	if _, err := m.Calls(ctx, "a.go", `{"name":"x"}`, false); err == nil {
		t.Error("an item without a uri was accepted")
	}
}
