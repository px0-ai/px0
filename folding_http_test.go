package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"px0/internal/render"
)

// Endpoint tests need no watchers, idle scavengers, or language-server workers.
func newFoldingTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	for path, text := range map[string]string{
		"main.go":     "package main\n\nfunc main() {\n\tgreet(\"hi\")\n}\n",
		"sub/deep.py": "def handler(req):\n    return 1\n",
	} {
		abs := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{ix: NewIndex(root), lsp: newLSPManager(root, false), mux: http.NewServeMux(), diffBase: "HEAD"}
	s.registerRoutes()
	t.Cleanup(func() {
		render.Evict(filepath.Join(root, "main.go"))
		render.Evict(filepath.Join(root, "sub/deep.py"))
		render.Evict(filepath.Join(root, "notes.txt"))
	})
	return s, root
}

func TestFoldsEndpoint(t *testing.T) {
	s, root := newFoldingTestServer(t)
	_, file := get(t, s, "/api/file?path=main.go")
	version := file["version"].(string)
	if version == "" {
		t.Fatal("missing source version")
	}
	code, body := get(t, s, "/api/folds?path=main.go&version="+url.QueryEscape(version))
	if code != 200 || body["version"] != version || body["limited"] != false {
		t.Fatalf("folds: %d %v", code, body)
	}
	want := []any{[]any{float64(3), float64(5)}}
	if !reflect.DeepEqual(body["ranges"], want) {
		t.Fatalf("ranges: %v want %v", body["ranges"], want)
	}
	code, raw := get(t, s, "/api/file?path=main.go&text=1&start=2&count=3&version="+url.QueryEscape(version))
	if code != 200 || raw["text"] != "func main() {\n\tgreet(\"hi\")\n}" {
		t.Fatalf("raw selection: %d %v", code, raw)
	}
	if _, ok := raw["lines"]; ok {
		t.Fatal("raw selection must not tokenize hidden lines")
	}
	code, _ = get(t, s, "/api/folds?path=../outside.go")
	if code != 400 {
		t.Fatalf("traversal: %d", code)
	}
	code, _ = get(t, s, "/api/folds?path=main.go&version=old")
	if code != 409 {
		t.Fatalf("stale folds: %d", code)
	}
	code, _ = get(t, s, "/api/file?path=main.go&text=1&version=old")
	if code != 409 {
		t.Fatalf("stale selection: %d", code)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("{\nx\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	code, body = get(t, s, "/api/folds?path=notes.txt")
	if code != 200 || len(body["ranges"].([]any)) != 0 {
		t.Fatalf("unsupported: %d %v", code, body)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/folds?path=main.go", nil))
	if rec.Code != 405 {
		t.Fatalf("method: %d", rec.Code)
	}
}

func TestFoldingCacheLifecycle(t *testing.T) {
	s, root := newFoldingTestServer(t)
	abs := filepath.Join(root, "main.go")
	d, err := render.Open(abs, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := d.Folds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	get(t, s, "/api/close?path=main.go")
	next, _ := render.Open(abs, "main.go")
	if d == next {
		t.Fatal("closing a tab must evict the document and folding cache")
	}
	if err := os.WriteFile(abs, []byte("package main\nfunc f() {\n if x {\n work()\n }\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, _ := render.Open(abs, "main.go")
	if changed.Version() == d.Version() {
		t.Fatal("changed source must have a different version")
	}
	second, _, _ := changed.Folds(context.Background())
	if reflect.DeepEqual(first, second) {
		t.Fatal("stale folding metadata after file change")
	}
}

func TestFoldsGoImportsEndpoint(t *testing.T) {
	s, root := newFoldingTestServer(t)
	src := "package main\n\nimport (\n \"os\"\n \"path/filepath\"\n)\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	code, body := get(t, s, "/api/folds?path=main.go")
	want := []any{[]any{float64(3), float64(6)}}
	if code != 200 || !reflect.DeepEqual(body["ranges"], want) {
		t.Fatalf("import group: %d %v, want %v", code, body, want)
	}
}

func TestFoldsMarkupEndpoint(t *testing.T) {
	s, root := newFoldingTestServer(t)
	for _, path := range []string{"page.html", "page.xhtml", "data.xml", "icon.svg"} {
		t.Run(path, func(t *testing.T) {
			abs := filepath.Join(root, path)
			t.Cleanup(func() { render.Evict(abs) })
			if err := os.WriteFile(abs, []byte("<root>\n<child>\ntext\n</child>\n</root>\n"), 0644); err != nil {
				t.Fatal(err)
			}
			code, body := get(t, s, "/api/folds?path="+path)
			want := []any{[]any{float64(1), float64(5)}, []any{float64(2), float64(4)}}
			if code != 200 || body["limited"] != false || !reflect.DeepEqual(body["ranges"], want) {
				t.Fatalf("markup ranges: %d %v, want %v", code, body, want)
			}
		})
	}
}

func TestFoldsBasePathAndCancellation(t *testing.T) {
	s, _ := newFoldingTestServer(t)
	s.basePath = "/review/"
	s.mux = http.NewServeMux()
	s.registerRoutes()
	code, body := get(t, s, "/review/api/folds?path=main.go")
	if code != 200 || len(body["ranges"].([]any)) != 1 {
		t.Fatalf("subpath: %d %v", code, body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/review/api/folds?path=sub/deep.py", nil).WithContext(ctx))
	if rec.Code != 408 {
		t.Fatalf("cancelled request: %d", rec.Code)
	}
}

func BenchmarkHandleFoldsCached(b *testing.B) {
	root := b.TempDir()
	src := strings.Repeat("func f() {\n work()\n}\n", 2000)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(src), 0644); err != nil {
		b.Fatal(err)
	}
	s := &Server{ix: NewIndex(root)}
	r := httptest.NewRequest(http.MethodGet, "/api/folds?path=a.go", nil)
	s.handleFolds(httptest.NewRecorder(), r)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		s.handleFolds(w, r)
		var res struct{ Ranges []render.FoldRange }
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			b.Fatal(err)
		}
	}
}
