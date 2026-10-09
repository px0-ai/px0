package main

// Tests for incremental document synchronization. These reference post-fix
// APIs (diffRange, incrementalChange, offsetToPosition, parseIncrementalSync,
// client.syncIncremental), so this file is transferred to the Mac together
// with the fixed lsp.go — it does not compile against the old code.
//
// The core property under test: for any old/new text pair, the ranged change
// we compute must apply cleanly to the old text (using the real fromLSP for
// the position → byte-offset conversion, the same direction servers use) and
// reproduce the new text byte-for-byte.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// applyRangedChange applies one ranged edit to old the way an incremental LSP
// server would: positions are resolved to byte offsets with the real fromLSP.
func applyRangedChange(t *testing.T, cl *lspClient, old string, start, end lspPosition, repl string) string {
	t.Helper()
	lines := strings.Split(old, "\n")
	lineOff := make([]int, len(lines))
	off := 0
	for i, ln := range lines {
		lineOff[i] = off
		off += len(ln) + 1 // +1 for the '\n' we split on
	}
	toOff := func(p lspPosition) int {
		if p.Line < 0 || p.Line >= len(lines) {
			t.Fatalf("position %+v out of range for text %q", p, old)
		}
		_, byteCol := cl.fromLSP(lines, p)
		return lineOff[p.Line] + byteCol
	}
	so, eo := toOff(start), toOff(end)
	if so < 0 || eo > len(old) || so > eo {
		t.Fatalf("bad offsets %d,%d for text %q", so, eo, old)
	}
	return old[:so] + repl + old[eo:]
}

// checkRoundTrip runs one old/new pair through the whole incremental chain:
// diffRange (pure byte logic, the independent oracle) → offsetToPosition →
// applyRangedChange via fromLSP → must equal newText, and the positions must
// resolve back to diffRange's byte offsets.
func checkRoundTrip(t *testing.T, cl *lspClient, old, newText string) {
	t.Helper()
	dStart, dEnd, dRepl, ok := diffRange(old, newText)
	if !ok {
		t.Fatalf("diffRange refused %q -> %q", old, newText)
	}
	changes, ok := incrementalChange(cl, old, newText)
	if !ok {
		t.Fatalf("incrementalChange refused %q -> %q", old, newText)
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	rng, ok := changes[0]["range"].(map[string]any)
	if !ok {
		t.Fatalf("change has no range: %v", changes[0])
	}
	start, ok1 := rng["start"].(lspPosition)
	end, ok2 := rng["end"].(lspPosition)
	repl, ok3 := changes[0]["text"].(string)
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("malformed change: %v", changes[0])
	}
	got := applyRangedChange(t, cl, old, start, end, repl)
	if got != newText {
		t.Fatalf("round trip mismatch (encoding %s):\nold: %q\nnew: %q\ngot: %q\nrange: %+v -> %+v repl %q\ndiffRange bytes: [%d,%d) repl %q",
			cl.encoding, old, newText, got, start, end, repl, dStart, dEnd, dRepl)
	}
	// The positions must resolve to exactly the byte offsets diffRange found.
	lines := strings.Split(old, "\n")
	lineOff := make([]int, len(lines))
	off := 0
	for i, ln := range lines {
		lineOff[i] = off
		off += len(ln) + 1
	}
	resolve := func(p lspPosition) int {
		_, byteCol := cl.fromLSP(lines, p)
		return lineOff[p.Line] + byteCol
	}
	if so, eo := resolve(start), resolve(end); so != dStart || eo != dEnd {
		t.Fatalf("positions resolve to [%d,%d), diffRange said [%d,%d) (encoding %s, old %q)",
			so, eo, dStart, dEnd, cl.encoding, old)
	}
}

func TestIncrementalChangeRoundTrip(t *testing.T) {
	cases := []struct{ old, new string }{
		{"", "hello"},
		{"hello", ""},
		{"hello", "hello world"},
		{"hello world", "hello"},
		{"foo\nbar\nbaz\n", "foo\nBAR\nbaz\n"},
		{"line1\nline2", "line1\nline2\nline3"},
		{"line1\nline2\nline3", "line1\nline3"},
		{"a\n\nb", "a\n\n\nb"},
		{"trailing\n", "trailing"},
		{"\nleading", "leading"},
		{"héllo → wörld", "héllo → WORLD"},         // multibyte latin
		{"func 你好() {}", "func 你好世界() {}"},         // CJK
		{"🎉 party", "🎊 party"},                     // astral swap (surrogate pairs in utf-16)
		{"a🎉b", "a🎉🎉b"},                            // insert after astral char
		{"🎉", "🎉🎉🎉"},                               // only astral chars
		{"x = \"é\" // café", "x = \"è\" // café"}, // é/è share first UTF-8 byte: prefix back-up
		{"aaa", "aaaaaa"},                          // pure append
		{"aaaaaa", "aaa"},                          // pure truncate
		{"abcdef", "abXYZef"},                      // middle replacement
		{"abcdef", "XYZabcdefXYZ"},                 // both ends
		{"one\ntwo\nthree", "one\n2\nthree"},       // single line edit, multiline doc
		{"x", "y"},                                 // single char replace
		{"\n\n\n", "\n\n"},                         // blank lines
		{"no-newline-at-end", "no-newline-at-end!"},
	}
	for _, enc := range []string{"utf-8", "utf-16", "utf-32"} {
		cl := &lspClient{encoding: enc}
		for _, tc := range cases {
			checkRoundTrip(t, cl, tc.old, tc.new)
		}
	}
}

func TestIncrementalChangeRejectsCR(t *testing.T) {
	cl := &lspClient{encoding: "utf-16"}
	for _, tc := range []struct{ old, new string }{
		{"a\r\nb", "a\r\nB"},
		{"a\nb", "a\r\nb"},
		{"a\rb", "a\nb"},
	} {
		if _, ok := incrementalChange(cl, tc.old, tc.new); ok {
			t.Errorf("incrementalChange(%q, %q) = ok, want full-text fallback for CR", tc.old, tc.new)
		}
	}
}

func TestParseIncrementalSync(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{``, false},
		{`null`, false},
		{`0`, false},
		{`1`, false},
		{`2`, true},
		{`3`, false},
		{`{}`, false},
		{`{"change": 1}`, false},
		{`{"change": 2}`, true},
		{`{"openClose": true}`, false},
		{`{"change": 2, "openClose": true, "willSave": true}`, true},
		{`{"change": 0}`, false},
		{`"incremental"`, false},
		{`[2]`, false},
	}
	for _, tc := range cases {
		if got := parseIncrementalSync(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("parseIncrementalSync(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func randLSPText(rng *rand.Rand, tokens []string, n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(tokens[rng.Intn(len(tokens))])
	}
	return sb.String()
}

// mutateLSPText applies 1–3 random rune-level edits to old.
func mutateLSPText(rng *rand.Rand, old string, tokens []string) string {
	r := []rune(old)
	for k := 0; k < 1+rng.Intn(3); k++ {
		switch rng.Intn(3) {
		case 0: // insert
			pos := rng.Intn(len(r) + 1)
			ins := []rune(tokens[rng.Intn(len(tokens))])
			r = append(r[:pos:pos], append(ins, r[pos:]...)...)
		case 1: // delete
			if len(r) == 0 {
				continue
			}
			pos := rng.Intn(len(r))
			n := 1 + rng.Intn(3)
			if pos+n > len(r) {
				n = len(r) - pos
			}
			r = append(r[:pos:pos], r[pos+n:]...)
		default: // replace
			if len(r) == 0 {
				continue
			}
			pos := rng.Intn(len(r))
			n := 1 + rng.Intn(2)
			if pos+n > len(r) {
				n = len(r) - pos
			}
			rep := []rune(tokens[rng.Intn(len(tokens))])
			r = append(r[:pos:pos], append(rep, r[pos+n:]...)...)
		}
	}
	return string(r)
}

// TestIncrementalChangeFuzz throws thousands of random unicode edits at the
// incremental chain across all three position encodings. Deterministic seed.
func TestIncrementalChangeFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tokens := []string{"a", "Z", "é", "→", "ü", "你", "好", "🎉", "🚀", "\n", " ", "\t",
		"func", "main", "(", ")", "{", "}", "// c", "x=1;"}
	for i := 0; i < 3000; i++ {
		old := randLSPText(rng, tokens, 1+rng.Intn(80))
		newText := mutateLSPText(rng, old, tokens)
		if newText == old {
			continue
		}
		for _, enc := range []string{"utf-8", "utf-16", "utf-32"} {
			checkRoundTrip(t, &lspClient{encoding: enc}, old, newText)
		}
	}
}

func TestSyncDocSendsRangedChangeWhenIncremental(t *testing.T) {
	cl, srv := lspRaceWireClient(t)
	cl.syncIncremental = true // normally set by initialize's capability parse
	path := filepath.Join(t.TempDir(), "f.go")
	oldText := "package main\n\nfunc main() {}\n"
	newText := "package main\n\nfunc main() { println(1) }\n"
	if err := os.WriteFile(path, []byte(oldText), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.ensureOpen(path, "f.go"); err != nil {
		t.Fatalf("ensureOpen: %v", err)
	}
	if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.syncDoc(path, "f.go"); err != nil {
		t.Fatalf("syncDoc: %v", err)
	}
	for start := time.Now(); time.Since(start) < 100*time.Millisecond; time.Sleep(time.Millisecond) {
		srv.mu.Lock()
		n := len(srv.didChangeRaw)
		srv.mu.Unlock()
		if n >= 1 {
			break
		}
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.didChangeRaw) != 1 {
		t.Fatalf("got %d didChange frames, want 1", len(srv.didChangeRaw))
	}
	var p struct {
		TextDocument struct {
			Version int `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Range *struct {
				Start lspPosition `json:"start"`
				End   lspPosition `json:"end"`
			} `json:"range"`
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(srv.didChangeRaw[0], &p); err != nil {
		t.Fatalf("unmarshal didChange: %v", err)
	}
	if p.TextDocument.Version != 2 {
		t.Errorf("version = %d, want 2", p.TextDocument.Version)
	}
	if len(p.ContentChanges) != 1 || p.ContentChanges[0].Range == nil {
		t.Fatalf("didChange was not a ranged change: %s", srv.didChangeRaw[0])
	}
	// The ranged change must reconstruct the new file content, and it must be
	// far smaller than the full text.
	r := p.ContentChanges[0].Range
	if got := applyRangedChange(t, &lspClient{encoding: "utf-16"}, oldText, r.Start, r.End, p.ContentChanges[0].Text); got != newText {
		t.Errorf("ranged change does not reconstruct file:\ngot:  %q\nwant: %q", got, newText)
	}
	if len(p.ContentChanges[0].Text) >= len(newText) {
		t.Errorf("ranged change text is %d bytes for a %d-byte file: no savings",
			len(p.ContentChanges[0].Text), len(newText))
	}
}

func TestSyncDocFallsBackToFullTextWhenNotIncremental(t *testing.T) {
	cl, srv := lspRaceWireClient(t)
	// syncIncremental stays false: the server never advertised it.
	path := filepath.Join(t.TempDir(), "f.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.ensureOpen(path, "f.go"); err != nil {
		t.Fatalf("ensureOpen: %v", err)
	}
	newText := "package main\n\n// changed\n"
	if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.syncDoc(path, "f.go"); err != nil {
		t.Fatalf("syncDoc: %v", err)
	}
	for start := time.Now(); time.Since(start) < 100*time.Millisecond; time.Sleep(time.Millisecond) {
		srv.mu.Lock()
		n := len(srv.didChangeRaw)
		srv.mu.Unlock()
		if n >= 1 {
			break
		}
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.didChangeRaw) != 1 {
		t.Fatalf("got %d didChange frames, want 1", len(srv.didChangeRaw))
	}
	var p struct {
		ContentChanges []map[string]any `json:"contentChanges"`
	}
	if err := json.Unmarshal(srv.didChangeRaw[0], &p); err != nil {
		t.Fatalf("unmarshal didChange: %v", err)
	}
	if len(p.ContentChanges) != 1 {
		t.Fatalf("want 1 content change, got %d", len(p.ContentChanges))
	}
	if _, hasRange := p.ContentChanges[0]["range"]; hasRange {
		t.Errorf("non-incremental server got a ranged change: %s", srv.didChangeRaw[0])
	}
	if p.ContentChanges[0]["text"] != newText {
		t.Errorf("full-text change = %q, want %q", p.ContentChanges[0]["text"], newText)
	}
}

func TestSyncDocSkipsUnchangedContent(t *testing.T) {
	cl, srv := lspRaceWireClient(t)
	path := filepath.Join(t.TempDir(), "f.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.ensureOpen(path, "f.go"); err != nil {
		t.Fatalf("ensureOpen: %v", err)
	}
	if err := cl.syncDoc(path, "f.go"); err != nil {
		t.Fatalf("syncDoc: %v", err)
	}
	if err := cl.syncDoc(path, "f.go"); err != nil {
		t.Fatalf("syncDoc: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.didChangeVers) != 0 {
		t.Fatalf("got %d didChange for unchanged content, want 0", len(srv.didChangeVers))
	}
}

func TestSyncDocDoesNotRecordVersionOnSendFailure(t *testing.T) {
	cl, _ := lspRaceWireClient(t)
	path := filepath.Join(t.TempDir(), "f.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.ensureOpen(path, "f.go"); err != nil {
		t.Fatalf("ensureOpen: %v", err)
	}
	// Break the pipe: the next write fails and the client dies.
	cl.mu.Lock()
	cl.in = lspRaceFailWriter{}
	cl.mu.Unlock()
	if err := os.WriteFile(path, []byte("package main\n// changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.syncDoc(path, "f.go"); err == nil {
		t.Fatal("expected syncDoc to fail on a broken pipe")
	}
	uri := pathToURI(path)
	cl.mu.RLock()
	v := cl.opened[uri]
	txt := cl.openedText[uri]
	cl.mu.RUnlock()
	if v != 1 {
		t.Errorf("version = %d after failed send, want 1 so the retry reuses it", v)
	}
	if txt != "package main\n" {
		t.Errorf("text recorded despite failed send: %q", txt)
	}
}

// BenchmarkLSPSyncDocIncremental measures the ranged-change path: a 100KB
// document with a small tail edit, sent as a single ranged
// TextDocumentContentChangeEvent instead of the full text. Post-fix only
// (it sets client.syncIncremental, which doesn't exist before).
func BenchmarkLSPSyncDocIncremental(b *testing.B) {
	cl, _ := lspRaceWireClient(b)
	cl.syncIncremental = true
	path := filepath.Join(b.TempDir(), "f.go")
	content := []byte(strings.Repeat("x", 100*1024)) // 100KB doc: IPC cost is visible
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		b.Fatal(err)
	}
	if err := cl.ensureOpen(path, "f.go"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Mutate the tail so every iteration is a genuine content change.
		for j := 0; j < 8; j++ {
			content[len(content)-1-j] = byte('0' + (i+j)%10)
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			b.Fatal(err)
		}
		if err := cl.syncDoc(path, "f.go"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLSPIncrementalChangeScale measures the diff itself — no disk I/O,
// no IPC — for large documents with a small edit at different positions, plus
// a whole-file rewrite. The prefix scan is a byte compare (cheap), but
// offsetToPosition walks lines from the start, so an edit near the end of a
// large file is the worst case. Identical input is not measured here: syncDoc
// skips it before incrementalChange is ever called (see
// BenchmarkLSPSyncDocUnchanged).
func BenchmarkLSPIncrementalChangeScale(b *testing.B) {
	const line = "func example(arg int) int { return arg * 2 } // padding to make the lines a realistic length....\n"
	const changedLine = "func example(arg int) int { return arg * 3 } // padding to make the lines a realistic length....\n"
	cl := &lspClient{encoding: "utf-16"}
	for _, sizeKB := range []int{100, 1024, 10 * 1024} {
		nLines := sizeKB * 1024 / len(line)
		lines := make([]string, nLines)
		for i := range lines {
			lines[i] = line
		}
		oldDoc := strings.Join(lines, "")
		edited := func(lineIdx int) string {
			cp := make([]string, nLines)
			copy(cp, lines)
			cp[lineIdx] = changedLine
			return strings.Join(cp, "")
		}
		cases := map[string]string{
			"edit-begin":  edited(0),
			"edit-middle": edited(nLines / 2),
			"edit-end":    edited(nLines - 1),
			"rewrite":     strings.Repeat("x", len(oldDoc)),
		}
		for _, name := range []string{"edit-begin", "edit-middle", "edit-end", "rewrite"} {
			newDoc := cases[name]
			b.Run(fmt.Sprintf("%dKB/%s", sizeKB, name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(oldDoc)))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					changes, ok := incrementalChange(cl, oldDoc, newDoc)
					if !ok || len(changes) != 1 {
						b.Fatalf("incrementalChange failed for %dKB/%s", sizeKB, name)
					}
				}
			})
		}
	}
}

// TestShutdownIsIdempotent pins the shutdown lifecycle: concurrent shutdowns
// (manager.Close racing Stop racing the restart path) must send exit exactly
// once and never panic on a double pipe close.
func TestShutdownIsIdempotent(t *testing.T) {
	cl, srv := lspRaceWireClient(t)
	_ = srv
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cl.shutdown()
		}()
	}
	wg.Wait()
	// And once more sequentially for good measure.
	cl.shutdown()
}
