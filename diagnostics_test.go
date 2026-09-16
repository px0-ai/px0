package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type lspRecorder struct {
	bytes.Buffer
}

func (r *lspRecorder) Close() error { return nil }

func diagnosticParams(uri string, version int, diagnostics any) json.RawMessage {
	body, _ := json.Marshal(map[string]any{
		"uri": uri, "version": version, "diagnostics": diagnostics,
	})
	return body
}

func TestPublishDiagnostics(t *testing.T) {
	c := newLSPClient(lspServerDef{Name: "test"}, t.TempDir())
	uri := "file:///test.go"
	c.opened[uri] = lspOpenDocument{version: 2}
	diagnostic := map[string]any{
		"range": map[string]any{
			"start": map[string]any{"line": 3, "character": 2},
			"end":   map[string]any{"line": 3, "character": 5},
		},
		"severity": 1, "message": "undefined: value",
	}

	c.onNotification(rpcMessage{
		Method: "textDocument/publishDiagnostics",
		Params: diagnosticParams(uri, 2, []any{diagnostic}),
	})
	snapshot, ok := c.diagnosticSnapshot("/test.go")
	if !ok || len(snapshot.items) != 1 || snapshot.items[0].Message != "undefined: value" {
		t.Fatalf("snapshot = %+v, %v", snapshot, ok)
	}

	c.onDiagnostics(diagnosticParams(uri, 1, []any{}))
	snapshot, _ = c.diagnosticSnapshot("/test.go")
	if len(snapshot.items) != 1 {
		t.Fatal("stale version replaced current diagnostics")
	}

	c.onDiagnostics(diagnosticParams("file:///other.go", 2, []any{diagnostic}))
	if _, ok := c.diagnosticSnapshot("/other.go"); ok {
		t.Fatal("stored diagnostics for an unopened document")
	}

	c.onDiagnostics(diagnosticParams(uri, 2, []any{}))
	snapshot, ok = c.diagnosticSnapshot("/test.go")
	if !ok || len(snapshot.items) != 0 {
		t.Fatalf("empty publication = %+v, %v", snapshot, ok)
	}
}

func TestEnsureOpenRefreshesChangedFile(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "a.go")
	if err := os.WriteFile(abs, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newLSPClient(lspServerDef{Name: "test", Exts: []string{".go"}}, root)
	recorder := &lspRecorder{}
	c.in = recorder

	if err := c.ensureOpen(abs, "a.go"); err != nil {
		t.Fatal(err)
	}
	firstLen := recorder.Len()
	if err := c.ensureOpen(abs, "a.go"); err != nil {
		t.Fatal(err)
	}
	if recorder.Len() != firstLen {
		t.Fatal("unchanged document sent another notification")
	}

	uri := pathToURI(abs)
	c.onDiagnostics(diagnosticParams(uri, 1, []any{}))
	if err := os.WriteFile(abs, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(abs, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := c.ensureOpen(abs, "a.go"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.diagnosticSnapshot(abs); ok {
		t.Fatal("changed document kept stale diagnostics")
	}

	reader := bufio.NewReader(bytes.NewReader(recorder.Bytes()))
	opened, err := readFrame(reader)
	if err != nil || opened.Method != "textDocument/didOpen" {
		t.Fatalf("first notification = %q, %v", opened.Method, err)
	}
	changed, err := readFrame(reader)
	if err != nil || changed.Method != "textDocument/didChange" {
		t.Fatalf("second notification = %q, %v", changed.Method, err)
	}
	var params struct {
		TextDocument struct {
			Version int `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(changed.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.TextDocument.Version != 2 || len(params.ContentChanges) != 1 || params.ContentChanges[0].Text != "package b\n" {
		t.Fatalf("didChange params = %+v", params)
	}
}

func TestDiagnosticsPositionEncoding(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "a.go")
	if err := os.WriteFile(abs, []byte("a🎉x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	def := lspServerDef{Name: "test", Exts: []string{".go"}}
	cases := []struct {
		encoding string
		start    int
		end      int
	}{
		{encoding: "utf-8", start: 1, end: 5},
		{encoding: "utf-16", start: 1, end: 3},
		{encoding: "utf-32", start: 1, end: 2},
	}
	for _, tc := range cases {
		t.Run(tc.encoding, func(t *testing.T) {
			c := newLSPClient(def, root)
			c.encoding = tc.encoding
			c.in = &lspRecorder{}
			uri := pathToURI(abs)
			c.opened[uri] = lspOpenDocument{version: 1, modTime: st.ModTime().UnixNano(), size: st.Size()}
			code := json.RawMessage(`17`)
			c.diagnostics[uri] = lspDiagnosticSnapshot{version: 1, items: []lspDiagnostic{{
				Range: lspRange{
					Start: lspPosition{Line: 0, Character: tc.start},
					End:   lspPosition{Line: 0, Character: tc.end},
				},
				Severity: 1, Code: code, Source: "test", Message: "bad emoji",
			}}}
			m := &lspManager{
				root: root, enabled: true,
				byExt: map[string]*lspServerDef{".go": &def}, clients: map[string]*lspClient{"test": c},
				starting: map[string]chan struct{}{}, failed: map[string]string{},
			}
			items, pending, err := m.Diagnostics(context.Background(), abs, "a.go")
			if err != nil || pending || len(items) != 1 {
				t.Fatalf("Diagnostics = %+v, %v, %v", items, pending, err)
			}
			got := items[0]
			if got.Line != 1 || got.Col != 1 || got.EndLine != 1 || got.EndCol != 3 || got.Code != "17" {
				t.Errorf("diagnostic = %+v", got)
			}
		})
	}
}

func TestDiagnosticsHandlerWithoutServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, newLSPManager(root, false))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lsp/diagnostics?path=a.go&wait=1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"diagnostics":[]`) || !strings.Contains(rec.Body.String(), `"state":"off"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lsp/diagnostics?path=../a.go", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad path status = %d", rec.Code)
	}
}

func TestDiagnosticsHandlerFailedServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	def := lspServerDef{Name: "test", Exts: []string{".go"}}
	m := &lspManager{
		root: root, enabled: true,
		byExt: map[string]*lspServerDef{".go": &def}, clients: map[string]*lspClient{},
		starting: map[string]chan struct{}{}, failed: map[string]string{"test": "server stopped"},
	}
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, m)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lsp/diagnostics?path=a.go&wait=1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"diagnostics":[]`) ||
		!strings.Contains(rec.Body.String(), `"state":"failed"`) || !strings.Contains(rec.Body.String(), `"error":"server stopped"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}
