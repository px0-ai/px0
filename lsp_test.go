package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Position encodings are the classic source of off-by-N bugs in LSP clients:
// Go counts bytes, JavaScript counts UTF-16 units, and the protocol defaults to
// UTF-16 while some servers negotiate UTF-8.
func TestPositionEncodings(t *testing.T) {
	// "héllo → wörld" mixes 1-, 2- and 3-byte runes.
	line := "héllo → wörld"
	cases := []struct {
		enc   string
		word  string
		wantC int
	}{
		{"utf-8", "wörld", strings.Index(line, "wörld")}, // bytes
		{"utf-16", "wörld", 8},                           // h é l l o ␠ → ␠
		{"utf-32", "wörld", 8},                           // same here: no surrogates
	}
	for _, c := range cases {
		cl := &lspClient{encoding: c.enc}
		byteCol := strings.Index(line, c.word)
		got := cl.toLSP(line, 1, byteCol)
		if got.Character != c.wantC {
			t.Errorf("%s: toLSP character = %d, want %d", c.enc, got.Character, c.wantC)
		}
		if got.Line != 0 {
			t.Errorf("%s: toLSP line = %d, want 0 (LSP is 0-based)", c.enc, got.Line)
		}
		// Round-tripping must land back on the same byte.
		gotLine, gotByte := cl.fromLSP([]string{line}, got)
		if gotLine != 1 || gotByte != byteCol {
			t.Errorf("%s: round trip = %d:%d, want 1:%d", c.enc, gotLine, gotByte, byteCol)
		}
	}
}

func TestUTF16ToByte(t *testing.T) {
	cases := []struct {
		line string
		u16  int
		want int
	}{
		{"abc", 0, 0},
		{"abc", 2, 2},
		{"abc", 99, 3},  // past the end clamps
		{"héllo", 2, 3}, // é is two bytes
		{"→x", 1, 3},    // → is three bytes, one UTF-16 unit
		{"🎉x", 2, 4},    // emoji is a surrogate pair: two units, four bytes
		{"🎉x", 3, 5},
		{"", 5, 0},
	}
	for _, c := range cases {
		if got := utf16ToByte(c.line, c.u16); got != c.want {
			t.Errorf("utf16ToByte(%q, %d) = %d, want %d", c.line, c.u16, got, c.want)
		}
	}
}

func TestURIRoundTrip(t *testing.T) {
	paths := []string{
		"/home/user/project/main.go",
		"/home/user/my project/a b.go", // spaces must be escaped
		"/tmp/weird#name$x.go",
	}
	if runtime.GOOS == "windows" {
		paths = []string{`C:\Users\me\main.go`}
	}
	for _, p := range paths {
		uri := pathToURI(p)
		if !strings.HasPrefix(uri, "file://") {
			t.Errorf("pathToURI(%q) = %q, want a file:// uri", p, uri)
		}
		if strings.Contains(uri, " ") {
			t.Errorf("pathToURI(%q) = %q, spaces must be percent-encoded", p, uri)
		}
		got, err := uriToPath(uri)
		if err != nil {
			t.Fatalf("uriToPath(%q): %v", uri, err)
		}
		if filepath.Clean(got) != filepath.Clean(p) {
			t.Errorf("round trip %q -> %q -> %q", p, uri, got)
		}
	}
	if _, err := uriToPath("https://example.com/x.go"); err == nil {
		t.Error("uriToPath accepted a non-file uri")
	}
}

// A disabled manager must never resolve a server, whatever is installed.
func TestManagerDisabled(t *testing.T) {
	m := newLSPManager(t.TempDir(), false)
	if got := m.Available(); len(got) != 0 {
		t.Errorf("disabled manager offers %v", got)
	}
	if d := m.defFor("x.go"); d != nil {
		t.Errorf("disabled manager resolved %v for x.go", d.Name)
	}
	if state, _ := m.State("x.go"); state != lspOff {
		t.Errorf("disabled manager state = %v, want off", state)
	}
}

// Only paths a server actually named may be opened outside the indexed tree.
func TestExternalAllowlist(t *testing.T) {
	root := t.TempDir()
	ix := NewIndex(root)
	ix.Build()
	m := newLSPManager(root, false)
	s := NewServer(ix, m)

	if _, _, ok := s.resolvePath("/etc/passwd"); ok {
		t.Error("resolvePath allowed an arbitrary absolute path")
	}
	m.allow("/usr/lib/go/src/strings/builder.go")
	abs, _, ok := s.resolvePath("/usr/lib/go/src/strings/builder.go")
	if !ok || abs != "/usr/lib/go/src/strings/builder.go" {
		t.Errorf("resolvePath refused an allowlisted path: %q %v", abs, ok)
	}
	if _, _, ok := s.resolvePath("/usr/lib/go/src/strings/other.go"); ok {
		t.Error("allowlisting one file allowed a sibling")
	}
	if _, _, ok := s.resolvePath("../../../etc/shadow"); ok {
		t.Error("relative traversal still escapes the root")
	}
}

func TestLanguageIDMapping(t *testing.T) {
	var ts lspServerDef
	for _, d := range lspRegistry {
		if d.Name == "typescript" {
			ts = d
		}
	}
	cases := map[string]string{
		"a.ts": "typescript", "a.tsx": "typescriptreact",
		"a.jsx": "javascriptreact", "a.js": "javascript", "a.mjs": "javascript",
	}
	for file, want := range cases {
		if got := ts.LanguageID(file); got != want {
			t.Errorf("LanguageID(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestLSPCloseDoc(t *testing.T) {
	cl := newLSPClient(lspServerDef{Name: "test", Cmd: []string{"echo"}}, t.TempDir())
	cl.opened["file:///test.go"] = 1

	// closeDoc removes from opened map
	cl.closeDoc("/test.go")
	cl.mu.Lock()
	_, exists := cl.opened["file:///test.go"]
	cl.mu.Unlock()
	if exists {
		t.Fatal("expected uri to be removed from opened map")
	}

	// Repeated closeDoc does not panic
	cl.closeDoc("/test.go")
}
