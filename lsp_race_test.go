package main

// Concurrency regression tests for the LSP client.
//
// Each test pins down a correctness bug by its observable behavior, using only
// APIs that exist before and after the fix:
//   - TestEnsureOpenSendsSingleDidOpen: concurrent ensureOpen must not send
//     textDocument/didOpen twice for the same URI (check-then-act race).
//   - TestSyncDocVersionsIncreaseMonotonically: concurrent syncDoc must not
//     emit duplicate document versions (lost-update race on the version
//     counter; LSP requires monotonically increasing versions).
//   - TestWriteDoesNotHoldMutexDuringPipeIO: a blocked pipe write must not
//     wedge unrelated client operations (alive must stay responsive).
//   - TestCallCleansPendingOnMarshalFailure / TestCallCleansPendingOnWriteFailure:
//     a request that never reaches the server must not leak its pending slot.
//
// A fake language server on an io.Pipe pair records what the client sends, so
// no real server binary is needed.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// lspRaceFakeServer records the notifications a client sends.
type lspRaceFakeServer struct {
	mu            sync.Mutex
	didOpen       int
	didChangeVers []int
	didChangeRaw  []json.RawMessage // raw didChange params, for shape assertions
	toCliW        *io.PipeWriter
}

func (s *lspRaceFakeServer) run(r *io.PipeReader) {
	br := bufio.NewReader(r)
	for {
		msg, err := readFrame(br)
		if err != nil {
			return
		}
		if len(msg.ID) > 0 {
			if s.toCliW != nil {
				resp := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": nil}
				b, _ := json.Marshal(resp)
				frame := append([]byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(b))), b...)
				s.toCliW.Write(frame)
			}
			continue
		}
		s.mu.Lock()
		switch msg.Method {
		case "textDocument/didOpen":
			s.didOpen++
		case "textDocument/didChange":
			var p struct {
				TextDocument struct {
					Version int `json:"version"`
				} `json:"textDocument"`
			}
			if json.Unmarshal(msg.Params, &p) == nil {
				s.didChangeVers = append(s.didChangeVers, p.TextDocument.Version)
			}
			// Keep the raw params (they alias the frame buffer, which is
			// fine for test lifetimes) so tests can assert on the change
			// shape (ranged vs full text).
			s.didChangeRaw = append(s.didChangeRaw, msg.Params)
		}
		s.mu.Unlock()
	}
}

// lspRaceWireClient builds a client whose pipes are looped to a fake server
// instead of a spawned process. On the fixed implementation it also starts the
// dedicated writer loop (via a capability check, so this still compiles and
// runs against the old code, where writes are synchronous). It takes
// testing.TB so benchmarks can reuse it too.
func lspRaceWireClient(t testing.TB) (*lspClient, *lspRaceFakeServer) {
	t.Helper()
	cl := newLSPClient(lspServerDef{Name: "fake", Cmd: []string{"fake"}}, t.TempDir())
	toSrvR, toSrvW := io.Pipe() // client -> server
	toCliR, toCliW := io.Pipe() // server -> client (never written in these tests)
	cl.in = toSrvW
	cl.out = bufio.NewReader(toCliR)
	srv := &lspRaceFakeServer{toCliW: toCliW}
	go srv.run(toSrvR)
	go cl.readLoop()
	if starter, ok := any(cl).(interface{ beginWriteLoop() }); ok {
		starter.beginWriteLoop()
	}
	t.Cleanup(func() {
		toSrvR.Close()
		toSrvW.Close()
		toCliR.Close()
		toCliW.Close()
	})
	return cl, srv
}

func TestEnsureOpenSendsSingleDidOpen(t *testing.T) {
	rounds := 3
	if testing.Short() {
		rounds = 1
	}
	for round := 0; round < rounds; round++ {
		cl, srv := lspRaceWireClient(t)
		path := filepath.Join(t.TempDir(), "f.go")
		if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
		const n = 32
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = cl.ensureOpen(path, "f.go")
			}()
		}
		wg.Wait()
		for start := time.Now(); time.Since(start) < 100*time.Millisecond; time.Sleep(time.Millisecond) {
			srv.mu.Lock()
			done := srv.didOpen >= 1
			srv.mu.Unlock()
			if done {
				break
			}
		}
		srv.mu.Lock()
		got := srv.didOpen
		srv.mu.Unlock()
		if got != 1 {
			t.Fatalf("round %d: server got %d textDocument/didOpen, want exactly 1", round, got)
		}
	}
}

func TestSyncDocVersionsIncreaseMonotonically(t *testing.T) {
	rounds := 3
	if testing.Short() {
		rounds = 1
	}
	for round := 0; round < rounds; round++ {
		cl, srv := lspRaceWireClient(t)
		path := filepath.Join(t.TempDir(), "f.go")
		if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := cl.ensureOpen(path, "f.go"); err != nil {
			t.Fatalf("ensureOpen: %v", err)
		}
		// One real change, then a storm of concurrent syncs over identical
		// content: every didChange the server sees must carry a distinct,
		// gap-free version starting at 2.
		if err := os.WriteFile(path, []byte("package main\n\n// changed\n"), 0644); err != nil {
			t.Fatal(err)
		}
		const n = 64
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = cl.syncDoc(path, "f.go")
			}()
		}
		wg.Wait()
		for start := time.Now(); time.Since(start) < 100*time.Millisecond; time.Sleep(time.Millisecond) {
			srv.mu.Lock()
			done := len(srv.didChangeVers) > 0
			srv.mu.Unlock()
			if done {
				break
			}
		}
		srv.mu.Lock()
		vers := append([]int(nil), srv.didChangeVers...)
		srv.mu.Unlock()

		seen := map[int]bool{}
		for _, v := range vers {
			if seen[v] {
				t.Fatalf("round %d: duplicate didChange version %d in %v", round, v, vers)
			}
			seen[v] = true
		}
		if len(vers) == 0 {
			t.Fatalf("round %d: no didChange sent for a real content change", round)
		}
		min, max := vers[0], vers[0]
		for _, v := range vers[1:] {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		if min != 2 || max-min+1 != len(vers) {
			t.Fatalf("round %d: versions %v are not contiguous starting at 2", round, vers)
		}
	}
}

func TestWriteDoesNotHoldMutexDuringPipeIO(t *testing.T) {
	cl := newLSPClient(lspServerDef{Name: "fake", Cmd: []string{"fake"}}, t.TempDir())
	r, w := io.Pipe() // never drained: writes block once the buffer fills
	cl.in = w
	if starter, ok := any(cl).(interface{ beginWriteLoop() }); ok {
		starter.beginWriteLoop()
	}
	t.Cleanup(func() { w.Close(); r.Close() })

	big := strings.Repeat("x", 1<<20) // 1MB: far beyond any pipe buffer
	done := make(chan error, 1)
	go func() {
		done <- cl.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{"uri": "file:///big.go", "text": big},
		})
	}()
	time.Sleep(300 * time.Millisecond) // let the write wedge itself in the pipe

	aliveCh := make(chan error, 1)
	go func() { aliveCh <- cl.alive() }()
	select {
	case err := <-aliveCh:
		if err != nil {
			t.Fatalf("alive() = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("alive() blocked for 2s: write() holds the client mutex during pipe I/O")
	}

	w.Close() // release the wedged writer, whichever implementation it is
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notify never returned after the pipe was closed")
	}
}

func TestCallCleansPendingOnMarshalFailure(t *testing.T) {
	cl := newLSPClient(lspServerDef{Name: "fake", Cmd: []string{"fake"}}, t.TempDir())
	// A func value cannot be marshalled to JSON.
	err := cl.call(context.Background(), "test/method", func() {}, nil)
	if err == nil {
		t.Fatal("expected json.Marshal to fail on a func param")
	}
	cl.mu.Lock()
	n := len(cl.pending)
	cl.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending holds %d entries after marshal failure: leaked", n)
	}
}

// lspRaceFailWriter is an io.WriteCloser whose writes always fail.
type lspRaceFailWriter struct{}

func (lspRaceFailWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }
func (lspRaceFailWriter) Close() error              { return nil }

func TestCallCleansPendingOnWriteFailure(t *testing.T) {
	cl := newLSPClient(lspServerDef{Name: "fake", Cmd: []string{"fake"}}, t.TempDir())
	cl.in = lspRaceFailWriter{}
	if starter, ok := any(cl).(interface{ beginWriteLoop() }); ok {
		starter.beginWriteLoop()
	}
	err := cl.call(context.Background(), "test/method", map[string]any{"a": 1}, nil)
	if err == nil {
		t.Fatal("expected the write to fail")
	}
	cl.mu.Lock()
	n := len(cl.pending)
	cl.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending holds %d entries after write failure: leaked", n)
	}
}

// TestReplyDoesNotBlockReadLoop pins the readLoop → reply path: when the
// server sends a server→client request (e.g. workspace/configuration) while
// its stdin pipe is stuck full, the reply must be fire-and-forget. A
// synchronous reply would deadlock — the writer blocks on the full stdin pipe
// while the server blocks writing to us, and the readLoop would never drain
// the server's output again.
func TestReplyDoesNotBlockReadLoop(t *testing.T) {
	cl := newLSPClient(lspServerDef{Name: "fake", Cmd: []string{"fake"}}, t.TempDir())
	_, toServerW := io.Pipe()         // read end intentionally never drained: simulates
	toClientR, toClientW := io.Pipe() // a server stuck writing to us
	cl.in, cl.out = toServerW, bufio.NewReader(toClientR)
	cl.beginWriteLoop()
	go cl.readLoop()
	// Nobody drains the server's stdin pipe: the first flush blocks the writer,
	// simulating a server stuck writing to us while not reading its stdin.

	sendFrame := func(body string) {
		t.Helper()
		if _, err := io.WriteString(toClientW, "Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body); err != nil {
			t.Fatalf("send frame: %v", err)
		}
	}
	// 1. Server asks for configuration; the reply is enqueued fire-and-forget.
	sendFrame(`{"jsonrpc":"2.0","id":1,"method":"workspace/configuration","params":{}}`)
	// 2. The readLoop must still be alive to process what follows.
	deadline := time.Now().Add(5 * time.Second)
	for {
		sendFrame(`{"jsonrpc":"2.0","method":"$/progress","params":{"token":"t","value":{"kind":"begin"}}}`)
		time.Sleep(50 * time.Millisecond)
		if cl.busy() {
			break // readLoop processed the notification: not stuck in reply()
		}
		if time.Now().After(deadline) {
			t.Fatal("readLoop stuck: reply() blocked on a synchronous write")
		}
	}
	toServerW.Close()
	toClientW.Close()
}

// TestSyncDocStaleReadDiscarded pins the syncDoc generation counter: when two
// syncDocs race, the one with the older READ must not overwrite the newer
// one's result, regardless of lock order. Without the counter, if B (newer
// read) wins the lock first and A (stale read) second, A would send the older
// text with a higher version and the server would be left stale.
//
// The test sets up the guard state directly: after a sync applies generation
// N, a syncDoc with generation < N must be discarded even if its file content
// differs.
func TestSyncDocStaleReadDiscarded(t *testing.T) {
	cl, srv := lspRaceWireClient(t)
	path := filepath.Join(t.TempDir(), "f.go")
	uri := pathToURI(path)
	if err := os.WriteFile(path, []byte("base"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.ensureOpen(path, "f.go"); err != nil {
		t.Fatal(err)
	}
	for start := time.Now(); time.Since(start) < 50*time.Millisecond; time.Sleep(time.Millisecond) {
		srv.mu.Lock()
		done := srv.didOpen >= 1
		srv.mu.Unlock()
		if done {
			break
		}
	}

	// A normal sync applies and records its generation.
	if err := os.WriteFile(path, []byte("package new\n// fresh-marker-bbb\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.syncDoc(path, "f.go"); err != nil {
		t.Fatal(err)
	}
	for start := time.Now(); time.Since(start) < 50*time.Millisecond; time.Sleep(time.Millisecond) {
		srv.mu.Lock()
		done := len(srv.didChangeRaw) >= 1
		srv.mu.Unlock()
		if done {
			break
		}
	}
	srv.mu.Lock()
	nBefore := len(srv.didChangeRaw)
	srv.mu.Unlock()

	// Simulate a stale reader: pretend a much newer generation already
	// applied (e.g. a concurrent syncDoc that read later but won the lock
	// first). The next syncDoc gets an older generation and must bail.
	cl.mu.Lock()
	cl.syncGen[uri] = cl.syncGenNext.Add(1) + 1000
	cl.mu.Unlock()

	if err := os.WriteFile(path, []byte("package old\n// stale-marker-aaa\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.syncDoc(path, "f.go"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)

	// The stale sync must not have sent anything.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.didChangeRaw) != nBefore {
		t.Fatalf("stale syncDoc sent didChange: got %d, want %d", len(srv.didChangeRaw), nBefore)
	}
	for _, raw := range srv.didChangeRaw {
		if strings.Contains(string(raw), "stale-marker-aaa") {
			t.Fatalf("server got stale didChange: %s", raw)
		}
	}
	cl.mu.RLock()
	recorded := cl.openedText[uri]
	cl.mu.RUnlock()
	if !strings.Contains(recorded, "fresh-marker-bbb") {
		t.Fatalf("client recorded stale text: %q", recorded)
	}
}

// TestCallRespectsContextDuringFlush pins the call() flush path: when the
// server stops reading its stdin and the pipe fills, call() must return the
// context error at the deadline instead of blocking in the flush forever.
func TestCallRespectsContextDuringFlush(t *testing.T) {
	cl := newLSPClient(lspServerDef{Name: "fake", Cmd: []string{"fake"}}, t.TempDir())
	_, toServerW := io.Pipe() // read end never drained: the flush blocks
	toClientR, toClientW := io.Pipe()
	cl.in, cl.out = toServerW, bufio.NewReader(toClientR)
	cl.beginWriteLoop()
	go cl.readLoop()
	t.Cleanup(func() {
		toServerW.Close()
		toClientW.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := cl.call(ctx, "test/blockedFlush", map[string]any{}, nil)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("call returned %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("call blocked for %v, want it to respect the 500ms context", elapsed)
	}
	cl.mu.RLock()
	n := len(cl.pending)
	cl.mu.RUnlock()
	if n != 0 {
		t.Fatalf("pending holds %d entries after ctx timeout: leaked", n)
	}
}
