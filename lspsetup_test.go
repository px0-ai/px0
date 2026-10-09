package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Only a POST from px0's own page, addressed by IP or localhost, may install.
func TestLocalPost(t *testing.T) {
	cases := []struct {
		name, method, host, origin string
		want                       bool
	}{
		{"same origin", "POST", "127.0.0.1:7777", "http://127.0.0.1:7777", true},
		{"localhost", "POST", "localhost:7777", "http://localhost:7777", true},
		{"ipv6 loopback", "POST", "[::1]:7777", "http://[::1]:7777", true},
		{"GET", "GET", "127.0.0.1:7777", "http://127.0.0.1:7777", false},
		{"no origin", "POST", "127.0.0.1:7777", "", false},
		{"other site", "POST", "127.0.0.1:7777", "https://evil.example", false},
		{"dns rebinding", "POST", "evil.example:7777", "http://evil.example:7777", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "/api/lsp/install", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := localPost(httptest.NewRecorder(), r); got != c.want {
			t.Errorf("%s: localPost = %v, want %v", c.name, got, c.want)
		}
	}
}

// A binary outside PATH is still found in one of the extra folders.
func TestLookPathInExtraDir(t *testing.T) {
	dir := t.TempDir()
	name := "px0-test-fake-language-server"
	file := name
	if runtime.GOOS == "windows" {
		file += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := lookPathIn(name, nil); ok {
		t.Fatalf("%s found without its folder; the test binary name collides with something on PATH", name)
	}
	if p, ok := lookPathIn(name, []string{t.TempDir(), dir}); !ok || filepath.Dir(p) != dir {
		t.Errorf("lookPathIn = %q, %v; want a path in %s", p, ok, dir)
	}
}

// Every recipe is well formed, and nothing px0 runs by itself asks for root.
func TestInstallRecipes(t *testing.T) {
	for _, def := range lspRegistry {
		if def.Lang == "" {
			t.Errorf("%s: no Lang", def.Name)
		}
		for _, in := range def.Install {
			if len(in.Cmd) < 2 {
				t.Errorf("%s: install command %q too short", def.Name, in.Cmd)
			}
			switch in.OS {
			case "", "darwin", "linux", "windows":
			default:
				t.Errorf("%s: unknown OS %q", def.Name, in.OS)
			}
			if in.Auto && (in.Cmd[0] == "sudo" || in.Cmd[0] == "winget") {
				t.Errorf("%s: %q needs an administrator and must not be Auto", def.Name, strings.Join(in.Cmd, " "))
			}
		}
	}
}

func TestInstallRefusals(t *testing.T) {
	off := newLSPManager(t.TempDir(), false)
	if _, err := off.Install("gopls", 0); err == nil {
		t.Error("Install with language servers disabled succeeded")
	}
	m := &lspManager{enabled: true}
	if _, err := m.Install("px0-no-such-server", 0); err == nil {
		t.Error("Install of an unknown server succeeded")
	}
	if _, err := m.Install("gopls", 99); err == nil {
		t.Error("Install with an out-of-range option succeeded")
	}
	for _, def := range lspRegistry {
		for i, in := range def.installsFor(runtime.GOOS) {
			if !in.Auto {
				if _, err := m.Install(def.Name, i); err == nil {
					t.Errorf("Install ran the manual recipe %q", strings.Join(in.Cmd, " "))
				}
			}
		}
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 8}
	b.Write([]byte("0123456789"))
	b.Write([]byte("ab"))
	if got := b.String(); got != "456789ab" {
		t.Errorf("tail = %q, want %q", got, "456789ab")
	}
}

func TestLSPServersEndpoint(t *testing.T) {
	s, root := newTestServer(t)
	s.lsp = newLSPManager(root, true)

	// Ensure we have Go and Python files
	_ = os.WriteFile(filepath.Join(root, "app.py"), []byte("print('hello')\n"), 0o644)
	s.ix.Build()

	req := httptest.NewRequest(http.MethodGet, "/api/lsp/servers", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var res struct {
		Enabled    bool             `json:"enabled"`
		AnyRunning bool             `json:"anyRunning"`
		Servers    []map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("json parse error: %v", err)
	}

	foundGo := false
	foundPy := false
	for _, srv := range res.Servers {
		name, _ := srv["name"].(string)
		if name == "gopls" {
			foundGo = true
		}
		if name == "pyright" || name == "pylsp" || name == "ruff" {
			foundPy = true
		}
	}
	if !foundGo {
		t.Errorf("expected gopls in relevant servers for Go files")
	}
	if !foundPy {
		t.Errorf("expected python language server in relevant servers for Python files")
	}
}

func TestLSPStopAndStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real LSP stop/start in short mode")
	}
	s, root := newTestServer(t)
	s.lsp = newLSPManager(root, true)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644)

	// Stop gopls
	stopReq := httptest.NewRequest(http.MethodPost, "/api/lsp/stop?server=gopls", nil)
	stopReq.Host = "127.0.0.1:8080"
	stopReq.Header.Set("Origin", "http://127.0.0.1:8080")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, stopReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("stop returned code %d", rec.Code)
	}

	state, _ := s.lsp.State("main.go")
	if state != lspOff {
		t.Errorf("expected lspOff after stop, got %v", state)
	}

	// Verify Stop All
	stopAllReq := httptest.NewRequest(http.MethodPost, "/api/lsp/stop", nil)
	stopAllReq.Host = "127.0.0.1:8080"
	stopAllReq.Header.Set("Origin", "http://127.0.0.1:8080")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, stopAllReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("stop all returned code %d", rec.Code)
	}

	if s.lsp.AnyRunning() {
		t.Errorf("expected no servers running after stop all")
	}
}
