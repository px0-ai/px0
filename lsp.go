package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// A minimal LSP client: enough of the protocol to answer navigation questions
// (definition, references, document symbols) and nothing else. Everything is
// best-effort — if a server is missing, slow, or broken, callers fall back to
// the regex index, which is always available.

// ---------------------------------------------------------------- protocol

type lspPosition struct {
	Line      int `json:"line"`      // 0-based
	Character int `json:"character"` // 0-based, in the negotiated encoding
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

// locationLink is what servers return when they support LinkSupport.
type lspLocationLink struct {
	TargetURI            string   `json:"targetUri"`
	TargetRange          lspRange `json:"targetRange"`
	TargetSelectionRange lspRange `json:"targetSelectionRange"`
}

type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children"`
	// symbolInformation form, used by servers without hierarchical support
	Location *lspLocation `json:"location"`
}

// Kind numbers come from the LSP spec; we only need display names.
var lspSymbolKind = map[int]string{
	1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class",
	6: "method", 7: "property", 8: "field", 9: "ctor", 10: "enum",
	11: "interface", 12: "func", 13: "var", 14: "const", 15: "string",
	16: "number", 17: "bool", 18: "array", 19: "object", 20: "key",
	21: "null", 22: "enum", 23: "struct", 24: "event", 25: "operator",
	26: "type",
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("lsp error %d: %s", e.Code, e.Message) }

// ---------------------------------------------------------------- client

// writeReq is one framed message handed to the dedicated writer goroutine.
// res is buffered(1) so the writer never blocks delivering the result.
type writeReq struct {
	body []byte
	res  chan error
}

// writeQueueCap bounds how many unsent messages may pile up behind a slow
// server. A full queue applies backpressure to the sender (write blocks)
// instead of growing memory without bound.
const writeQueueCap = 256

type lspClient struct {
	def  lspServerDef
	root string

	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	logf func(string, ...any)

	// mu guards nextID, pending, opened, openedText, syncGen, dead and the
	// writer handles below. It is an RWMutex so hot read paths (alive, status
	// lookups) don't serialize against each other. The mutex is NEVER held
	// across pipe I/O: all writes go through the dedicated writer goroutine,
	// so a stalled server can't wedge every other caller on this client.
	mu          sync.RWMutex
	nextID      int64
	pending     map[int64]chan rpcMessage
	opened      map[string]int    // uri -> document version
	openedText  map[string]string // uri -> last text sent (unchanged skip + incremental diffs)
	syncGen     map[string]uint64 // uri -> generation of the latest syncDoc read that applied
	syncGenNext atomic.Uint64     // dispenses syncDoc generations; no lock needed
	dead        error

	// writeCh feeds the dedicated writer goroutine. It is created with the
	// client (never closed: closing it would race with concurrent senders,
	// since send-on-closed panics) and drained+abandoned on fail().
	writeCh chan writeReq
	// deadCh is closed by fail(); it unblocks senders stuck enqueueing and
	// tells the writer to drain with errors and exit.
	deadCh           chan struct{}
	writeLoopStarted bool

	// docMu serializes document open/sync/close sequences per client, so the
	// check → notify → record steps in ensureOpen/syncDoc/closeDoc are atomic
	// across goroutines. Version assignment can't interleave, and a document
	// can't be didOpen'ed twice.
	docMu sync.Mutex

	// encoding is how the server counts Character offsets: "utf-8", "utf-16"
	// (the spec default) or "utf-32".
	encoding string

	// syncIncremental is true when the server advertised incremental
	// textDocumentSync during initialize. Only then may didChange carry
	// ranged edits; otherwise the full text is always sent. Set once by
	// initialize before the client is published; read under c.mu.
	syncIncremental bool

	readyCh  chan struct{}
	once     sync.Once
	failOnce sync.Once

	// shutdownOnce makes shutdown idempotent: manager.Close, Stop, and the
	// client restart path can all trigger it, including concurrently. A
	// second shutdown on the same client must not re-send exit or double-close
	// the pipes.
	shutdownOnce sync.Once

	// indexing tracks $/progress tokens so callers can tell "no result" from
	// "the server has not finished indexing yet".
	indexing atomic.Int32
}

func newLSPClient(def lspServerDef, root string) *lspClient {
	return &lspClient{
		def: def, root: root,
		pending:    map[int64]chan rpcMessage{},
		opened:     map[string]int{},
		openedText: map[string]string{},
		syncGen:    map[string]uint64{},
		encoding:   "utf-16",
		writeCh:    make(chan writeReq, writeQueueCap),
		deadCh:     make(chan struct{}),
		readyCh:    make(chan struct{}),
		logf:       func(string, ...any) {},
	}
}

func (c *lspClient) start(ctx context.Context) error {
	c.cmd = exec.Command(c.def.Cmd[0], c.def.Cmd[1:]...)
	c.cmd.Dir = c.root
	c.cmd.Stderr = io.Discard

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.cmd.Start(); err != nil {
		return err
	}
	c.in, c.out = stdin, bufio.NewReaderSize(stdout, 64<<10)
	c.beginWriteLoop()

	go c.readLoop()
	go func() {
		c.cmd.Wait()
		c.fail(fmt.Errorf("%s exited", c.def.Name))
	}()

	return c.initialize(ctx)
}

// beginWriteLoop starts the dedicated writer goroutine. start() calls it once
// the pipes exist; tests that wire a client by hand (no spawned process) call
// it directly after assigning c.in/c.out.
func (c *lspClient) beginWriteLoop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeLoopStarted {
		return
	}
	c.writeLoopStarted = true
	select {
	case <-c.deadCh:
		return // already failed; nothing will ever be enqueued
	default:
	}
	go c.writeLoop()
}

// writeLoop is the single goroutine that touches the server's stdin. One
// writer means frames can't interleave, and no caller ever holds c.mu while
// blocked in a pipe write.
func (c *lspClient) writeLoop() {
	for {
		select {
		case req := <-c.writeCh:
			if err := c.writeFrame(req.body); err != nil {
				req.res <- err
				// A failed pipe write means the server is gone: mark the
				// client dead now so senders blocked on a full queue are
				// released via deadCh and pending calls fail fast, instead
				// of every subsequent write failing individually forever.
				// fail is idempotent; the drain branch above takes it from
				// here. The writer holds no locks, so this can't deadlock.
				c.fail(err)
			} else {
				req.res <- nil
			}
		case <-c.deadCh:
			// The client died: fail everything still queued, then exit.
			// writeCh itself is never closed (send-on-closed panics), so
			// drain it here instead.
			err := c.alive()
			if err == nil {
				err = fmt.Errorf("%s: connection lost", c.def.Name)
			}
			for {
				select {
				case req := <-c.writeCh:
					req.res <- err
				default:
					return
				}
			}
		}
	}
}

// writeFrame performs the actual pipe I/O for one message. It snapshots the
// stdin handle under a read lock and never holds the mutex across the write.
func (c *lspClient) writeFrame(body []byte) error {
	c.mu.RLock()
	in := c.in
	c.mu.RUnlock()
	if in == nil {
		return fmt.Errorf("lsp client stdin closed")
	}
	// Frame header without fmt.Fprintf's allocation.
	var hdr [48]byte
	p := append(hdr[:0], "Content-Length: "...)
	p = strconv.AppendInt(p, int64(len(body)), 10)
	p = append(p, '\r', '\n', '\r', '\n')
	if _, err := in.Write(p); err != nil {
		return err
	}
	_, err := in.Write(body)
	return err
}

func (c *lspClient) fail(err error) {
	c.mu.Lock()
	if c.dead == nil {
		c.dead = err
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.failOnce.Do(func() { close(c.deadCh) })
	c.once.Do(func() { close(c.readyCh) })
}

func (c *lspClient) alive() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dead
}

// readLoop consumes framed messages and routes them: responses to their waiting
// caller, server-initiated requests to a stub reply (a server that never hears
// back from us can stall), notifications to progress tracking.
func (c *lspClient) readLoop() {
	for {
		msg, err := readFrame(c.out)
		if err != nil {
			c.fail(err)
			return
		}
		switch {
		case msg.Method == "" && len(msg.ID) > 0: // response
			id, err := strconv.ParseInt(strings.Trim(string(msg.ID), `"`), 10, 64)
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch, ok := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ok {
				ch <- msg
				close(ch)
			}
		case len(msg.ID) > 0: // server -> client request; must be answered
			c.reply(msg.ID, msg.Method)
		default: // notification
			c.onNotification(msg)
		}
	}
}

func (c *lspClient) reply(id json.RawMessage, method string) {
	var result any
	switch method {
	case "workspace/configuration":
		result = []any{map[string]any{}}
	case "workspace/workspaceFolders":
		result = []any{map[string]string{"uri": pathToURI(c.root), "name": filepath.Base(c.root)}}
	default:
		result = nil
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
	// Fire-and-forget: reply() runs on the readLoop, and a synchronous write
	// could deadlock against a server that is blocked writing to us.
	// Best-effort is fine here; the server will time out a lost reply.
	_ = c.writeAsync(body)
}

func (c *lspClient) onNotification(msg rpcMessage) {
	switch msg.Method {
	case "$/progress":
		var p struct {
			Value struct {
				Kind string `json:"kind"`
			} `json:"value"`
		}
		if json.Unmarshal(msg.Params, &p) != nil {
			return
		}
		switch p.Value.Kind {
		case "begin":
			c.indexing.Add(1)
		case "end":
			// Floor at zero: a stray "end" without a matching "begin"
			// must not drive the counter negative.
			for {
				v := c.indexing.Load()
				if v <= 0 || c.indexing.CompareAndSwap(v, v-1) {
					break
				}
			}
		}
	}
}

func (c *lspClient) busy() bool {
	return c.indexing.Load() > 0
}

// readFrame parses one Content-Length-framed JSON-RPC message.
//
// Header parsing is allocation-free: header lines are sliced out of the
// reader's buffer and matched/parsed in place.
//
// The body buffer is intentionally NOT pooled or reused. rpcMessage carries
// Params/Result/ID as json.RawMessage slices aliasing that buffer, and those
// outlive readFrame (responses are handed to waiting callers, notifications
// are unmarshalled after routing), so reusing the buffer would corrupt
// in-flight messages.
func readFrame(r *bufio.Reader) (rpcMessage, error) {
	var length int
	for {
		line, err := readHeaderLine(r)
		if err != nil {
			return rpcMessage{}, err
		}
		line = trimCRLF(line)
		if len(line) == 0 {
			break
		}
		if name, val, ok := cutHeaderField(line); ok && isContentLength(name) {
			if n, ok := parseHeaderInt(val); ok {
				length = n
			}
		}
	}
	if length <= 0 || length > 64<<20 {
		return rpcMessage{}, fmt.Errorf("bad content length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return rpcMessage{}, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		return rpcMessage{}, err
	}
	return msg, nil
}

// readHeaderLine reads one \n-terminated header line without allocating in the
// common case. The returned slice aliases the reader's buffer and is only
// valid until the next read — callers must consume it immediately.
func readHeaderLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err == nil {
		return line, nil
	}
	if err != bufio.ErrBufferFull {
		return nil, err
	}
	// Pathological header line longer than the buffer: accumulate the chunks.
	var buf []byte
	buf = append(buf, line...)
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == nil {
			return buf, nil
		}
		if err != bufio.ErrBufferFull {
			return nil, err
		}
	}
}

func trimCRLF(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// cutHeaderField splits a "Name: value" header line on the first colon,
// trimming OWS (spaces/tabs) around both parts without allocating.
func cutHeaderField(line []byte) (name, val []byte, ok bool) {
	for i, ch := range line {
		if ch == ':' {
			return trimHeaderSpace(line[:i]), trimHeaderSpace(line[i+1:]), true
		}
	}
	return nil, nil, false
}

func trimHeaderSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

// isContentLength reports whether a header name is "Content-Length",
// case-insensitively (header names are ASCII by spec).
func isContentLength(name []byte) bool {
	const want = "content-length"
	if len(name) != len(want) {
		return false
	}
	for i := 0; i < len(want); i++ {
		ch := name[i]
		if 'A' <= ch && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		if ch != want[i] {
			return false
		}
	}
	return true
}

// parseHeaderInt parses a decimal header value without allocating. It rejects
// empty/non-numeric input and values beyond the frame size cap, mirroring the
// bounds check readFrame applies afterwards.
func parseHeaderInt(b []byte) (int, bool) {
	if len(b) == 0 {
		return 0, false
	}
	n := 0
	for _, ch := range b {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
		if n > 64<<20 {
			return 0, false
		}
	}
	return n, true
}

// enqueue hands one framed message to the dedicated writer and returns the
// channel the writer will deliver the flush result on. It blocks only on
// enqueueing (backpressure when the queue is full), never on pipe I/O.
// Callers choose whether to wait for the flush.
func (c *lspClient) enqueue(body []byte) (<-chan error, error) {
	c.mu.RLock()
	dead := c.dead
	ch := c.writeCh
	done := c.deadCh
	started := c.writeLoopStarted
	c.mu.RUnlock()
	if dead != nil {
		return nil, dead
	}
	if ch == nil {
		return nil, fmt.Errorf("lsp client not started")
	}
	if !started {
		c.beginWriteLoop() // idempotent; no-op if a concurrent write beat us
	}
	req := writeReq{body: body, res: make(chan error, 1)}
	select {
	case ch <- req:
		return req.res, nil
	case <-done:
		return nil, c.connLost()
	}
}

// write enqueues one framed message for the dedicated writer and waits for it
// to hit the pipe. It never holds c.mu across I/O: the handle and the dead
// flag are snapshotted under a read lock, then everything blocks on channels.
// If the client dies mid-enqueue, the sender is released via deadCh instead of
// hanging on a queue nobody drains.
//
// The writer is started lazily on first write as a backstop: start() always
// starts it, but a client wired by hand (tests, embedders) may not have.
// Without this, write() would block forever on a queue nobody drains.
func (c *lspClient) write(body []byte) error {
	res, err := c.enqueue(body)
	if err != nil {
		return err
	}
	select {
	case err := <-res:
		return err
	case <-c.deadCh:
		return c.connLost()
	}
}

// writeAsync enqueues one framed message without waiting for the flush. It
// exists for server→client request replies issued from the readLoop: a
// synchronous write there can deadlock — if the server is blocked writing to
// us while its stdin pipe is full, we would wait for a flush the server can't
// drain because it's waiting for us to read. The reply still goes out in FIFO
// order; we just don't wait for it.
func (c *lspClient) writeAsync(body []byte) error {
	_, err := c.enqueue(body)
	return err
}

func (c *lspClient) connLost() error {
	if err := c.alive(); err != nil {
		return err
	}
	return fmt.Errorf("%s: connection lost", c.def.Name)
}

func (c *lspClient) notify(method string, params any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	return c.write(body)
}

// notifyAsync enqueues a notification without waiting for the flush. For
// shutdown-time messages where the process kill is the real guarantee and a
// hung server must not trap the caller.
func (c *lspClient) notifyAsync(method string, params any) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return
	}
	_ = c.writeAsync(body)
}

func (c *lspClient) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	if c.dead != nil {
		err := c.dead
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	// forget drops the pending slot. It runs on every path where the request
	// never reached (or will never reach) the server, so a marshal or write
	// failure can't leak the entry — and its channel — forever.
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		forget()
		return err
	}
	// Enqueue without waiting for the flush: the flush wait below joins the
	// same select as ctx.Done(), so a server that stops reading its stdin
	// can't trap the caller past its deadline even when the pipe is full.
	flushed, err := c.enqueue(body)
	if err != nil {
		forget()
		return err
	}
	select {
	case err := <-flushed:
		if err != nil {
			forget()
			return err
		}
	case <-ctx.Done():
		// The request may still be flushed later; dropping the pending slot
		// means a late response is ignored instead of delivered nowhere.
		// Send a cancel in case it does get flushed — best-effort, and async
		// so a wedged server can't trap us here either.
		forget()
		c.notifyAsync("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// Tell the server to stop working on something nobody is waiting for.
		// Async: the server may already be wedged, and a synchronous notify
		// here would block with no timeout.
		c.notifyAsync("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return fmt.Errorf("%s: connection lost", c.def.Name)
		}
		if msg.Error != nil {
			return msg.Error
		}
		if out == nil || len(msg.Result) == 0 || string(msg.Result) == "null" {
			return nil
		}
		return json.Unmarshal(msg.Result, out)
	}
}

func (c *lspClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToURI(c.root),
		"clientInfo": map[string]string{
			"name": "px0", "version": version,
		},
		"workspaceFolders": []any{
			map[string]string{"uri": pathToURI(c.root), "name": filepath.Base(c.root)},
		},
		"capabilities": map[string]any{
			"general": map[string]any{
				// Ask for byte offsets so we can skip UTF-16 conversion where
				// the server is willing; we handle either answer.
				"positionEncodings": []string{"utf-8", "utf-16"},
			},
			"workspace": map[string]any{
				"workspaceFolders": true,
				"configuration":    true,
				"symbol":           map[string]any{"dynamicRegistration": false},
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{"didSave": true, "dynamicRegistration": false},
				"definition":      map[string]any{"linkSupport": true},
				"typeDefinition":  map[string]any{"linkSupport": true},
				"implementation":  map[string]any{"linkSupport": true},
				"references":      map[string]any{"dynamicRegistration": false},
				"callHierarchy":   map[string]any{"dynamicRegistration": false},
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
					"dynamicRegistration":               false,
				},
				// Order matters: servers pick the first format they support,
				// and markdown is what carries the fenced signature block.
				"hover": map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			},
			"window": map[string]any{"workDoneProgress": true},
		},
		"initializationOptions": c.def.InitOptions,
	}

	var res struct {
		Capabilities struct {
			PositionEncoding string          `json:"positionEncoding"`
			TextDocumentSync json.RawMessage `json:"textDocumentSync"`
		} `json:"capabilities"`
	}
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		return err
	}
	if res.Capabilities.PositionEncoding != "" {
		c.encoding = res.Capabilities.PositionEncoding
	}
	// Set under the mutex: the client is published to the manager (and thus
	// to other goroutines) as soon as start returns.
	c.mu.Lock()
	c.syncIncremental = parseIncrementalSync(res.Capabilities.TextDocumentSync)
	c.mu.Unlock()
	if err := c.notify("initialized", map[string]any{}); err != nil {
		return err
	}
	c.once.Do(func() { close(c.readyCh) })
	return nil
}

// parseIncrementalSync reports whether an initialize result's textDocumentSync
// capability asks for incremental updates. The capability is either a number
// (0=none, 1=full, 2=incremental) or an object with a numeric "change" field.
// Anything unrecognized conservatively means full sync: sending a ranged
// change to a full-sync server would corrupt its copy of the document.
func parseIncrementalSync(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n == 2
	}
	var obj struct {
		Change *float64 `json:"change"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Change != nil {
		return *obj.Change == 2
	}
	return false
}

func (c *lspClient) shutdown() {
	c.shutdownOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// The shutdown request respects ctx (see call): a hung server can't
		// trap us past the deadline.
		c.call(ctx, "shutdown", nil, nil)
		// Deliver the exit notification if the server is healthy enough to
		// take it, but don't wait forever: 200ms is plenty for a live
		// server to flush, and a hung one can't trap shutdownOnce.Do (which
		// would block every other caller). Wedged servers die on stdin EOF
		// or the kill timer below.
		if body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "exit"}); err == nil {
			if flushed, err := c.enqueue(body); err == nil {
				select {
				case <-flushed:
				case <-time.After(200 * time.Millisecond):
				}
			}
		}
		if c.in != nil {
			c.in.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			time.AfterFunc(time.Second, func() { c.cmd.Process.Kill() })
		}
	})
}

// ensureOpen tells the server about a file. Most servers refuse to answer
// questions about a document they were never handed.
//
// The file is read before taking docMu: holding the document lock across file
// I/O would serialize every other document's sync behind a slow disk. The
// check → didOpen → record sequence still runs atomically under docMu, so two
// concurrent callers can't both see "not open" and send didOpen twice for the
// same URI. If the file changed between the read and the didOpen, the next
// syncDoc repairs it.
func (c *lspClient) ensureOpen(abs, rel string) error {
	uri := pathToURI(abs)
	// Fail fast on a dead client instead of doing file I/O for a didOpen
	// that can never be sent.
	if err := c.alive(); err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	text := string(data)

	c.docMu.Lock()
	defer c.docMu.Unlock()
	// Re-check under docMu: the client may have died while we were reading,
	// or another goroutine may have opened the document first.
	if err := c.alive(); err != nil {
		return err
	}
	c.mu.RLock()
	_, already := c.opened[uri]
	c.mu.RUnlock()
	if already {
		return nil
	}
	if err := c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": c.def.LanguageID(rel),
			"version":    1,
			"text":       text,
		},
	}); err != nil {
		return err
	}
	c.mu.Lock()
	c.opened[uri] = 1
	c.openedText[uri] = text
	c.mu.Unlock()
	return nil
}

// syncDoc re-reads an already opened file from disk and notifies the server of
// changes, e.g. after a reindex or external edit.
//
// The version read → increment → store sequence runs under docMu, so it is
// atomic: concurrent syncDocs can no longer read the same version and send
// duplicate version numbers with different content (an LSP violation —
// versions must increase monotonically per document). The file read itself
// happens before docMu is taken, so a slow disk doesn't serialize other
// documents' syncs; the didChange notify still goes out under docMu to keep
// versions and sends in order.
//
// If the file is byte-identical to what was last sent, the didChange/didSave
// round trip is skipped entirely: reindex sweeps call syncDoc over every open
// file, and resending identical full text is pure IPC/CPU waste.
//
// When the server advertised incremental textDocumentSync during initialize,
// only the changed range is sent (a prefix/suffix diff); otherwise the full
// text goes, exactly like didOpen. The diff is conservative: anything it can't
// express safely falls back to the full text.
//
// The version and text are recorded only after the didChange was actually
// sent, so a failed notify leaves the old state in place and the next syncDoc
// retries cleanly instead of skipping a change the server never saw.
func (c *lspClient) syncDoc(abs, rel string) error {
	uri := pathToURI(abs)
	if err := c.alive(); err != nil {
		return err
	}
	// Skip the file read entirely for documents we never opened.
	c.mu.RLock()
	_, opened := c.opened[uri]
	c.mu.RUnlock()
	if !opened {
		return nil
	}
	// Read the file before taking docMu, as in ensureOpen. The generation
	// counter below keeps concurrent syncDocs applying in read order (not
	// lock order): a syncDoc whose read is older than one that already
	// applied is discarded, so a stale read can't overwrite newer text.
	gen := c.syncGenNext.Add(1)
	data, readErr := os.ReadFile(abs)

	c.docMu.Lock()
	defer c.docMu.Unlock()

	if readErr != nil {
		c.closeDocLocked(abs)
		return readErr
	}
	if err := c.alive(); err != nil {
		return err
	}
	c.mu.RLock()
	v, ok := c.opened[uri]
	oldText := c.openedText[uri]
	incremental := c.syncIncremental
	lastGen := c.syncGen[uri]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	if gen < lastGen {
		// A syncDoc with a newer read already won the lock; our data is
		// stale. Returning here (not an error) keeps the newer text.
		return nil
	}
	newText := string(data)
	if newText == oldText {
		// Record the generation even though nothing is sent: our read is
		// the latest, so a concurrent syncDoc with an older read must be
		// discarded rather than overwriting with stale text.
		c.mu.Lock()
		c.syncGen[uri] = gen
		c.mu.Unlock()
		return nil
	}
	v++

	contentChanges := fullTextChange(newText)
	if incremental {
		if ranged, ok := incrementalChange(c, oldText, newText); ok {
			contentChanges = ranged
		}
	}

	if err := c.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     uri,
			"version": v,
		},
		"contentChanges": contentChanges,
	}); err != nil {
		return err
	}
	_ = c.notify("textDocument/didSave", map[string]any{
		"textDocument": map[string]any{
			"uri": uri,
		},
	})
	c.mu.Lock()
	c.opened[uri] = v
	c.openedText[uri] = newText
	c.syncGen[uri] = gen
	c.mu.Unlock()
	return nil
}

// fullTextChange is the didChange payload for full-sync servers: a single
// TextDocumentContentChangeEvent carrying the whole document.
func fullTextChange(text string) []map[string]any {
	return []map[string]any{{"text": text}}
}

// incrementalChange builds a single ranged TextDocumentContentChangeEvent that
// turns oldText into newText. It returns ok=false when the change can't be
// expressed safely as a range, in which case the caller sends the full text.
//
// Safety rules: positions are computed on '\n'-split lines (the same line
// model fromLSP uses, so the server and client agree on what a line is),
// change boundaries are backed up to UTF-8 rune edges so no character is
// split, and any '\r' in either text forces a full-text fallback — servers
// disagree on whether '\r' belongs to the line, and a one-off position there
// would corrupt the server's copy of the document.
func incrementalChange(c *lspClient, oldText, newText string) ([]map[string]any, bool) {
	if strings.IndexByte(oldText, '\r') >= 0 || strings.IndexByte(newText, '\r') >= 0 {
		return nil, false
	}
	start, end, repl, ok := diffRange(oldText, newText)
	if !ok {
		return nil, false
	}
	return []map[string]any{{
		"range": map[string]any{
			"start": c.offsetToPosition(oldText, start),
			"end":   c.offsetToPosition(oldText, end),
		},
		"text": repl,
	}}, true
}

// diffRange finds the smallest single byte range [start, end) in oldText that,
// replaced by repl, yields newText. Boundaries are on UTF-8 rune edges so the
// position conversion can't split a character. ok=false when the texts are
// identical (callers skip those before calling).
func diffRange(oldText, newText string) (start, end int, repl string, ok bool) {
	// Common prefix, in bytes.
	p := 0
	for p < len(oldText) && p < len(newText) && oldText[p] == newText[p] {
		p++
	}
	// Back up to a rune boundary: a shared partial rune must be replaced
	// whole on both sides, never split down the middle.
	for p > 0 && p < len(oldText) && !utf8.RuneStart(oldText[p]) {
		p--
	}
	// Common suffix, in bytes, not overlapping the prefix.
	s := 0
	for s < len(oldText)-p && s < len(newText)-p &&
		oldText[len(oldText)-1-s] == newText[len(newText)-1-s] {
		s++
	}
	// Same backup for the suffix cut: shrink the shared suffix until the cut
	// sits on a rune boundary (or vanishes).
	for s > 0 && len(oldText)-s > p && !utf8.RuneStart(oldText[len(oldText)-s]) {
		s--
	}
	if p == len(oldText) && p == len(newText) {
		return 0, 0, "", false
	}
	return p, len(oldText) - s, newText[p : len(newText)-s], true
}

// offsetToPosition converts a byte offset in text to an lspPosition in the
// negotiated encoding. The caller guarantees no '\r' in text and a
// rune-boundary offset (both ensured by incrementalChange).
func (c *lspClient) offsetToPosition(text string, off int) lspPosition {
	line := 1
	lineStart := 0
	for i := 0; i < off; i++ {
		if text[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	lineEnd := len(text)
	if i := strings.IndexByte(text[lineStart:], '\n'); i >= 0 {
		lineEnd = lineStart + i
	}
	return c.toLSP(text[lineStart:lineEnd], line, off-lineStart)
}

// closeDoc notifies the server that the file was closed, allowing the server
// to free ASTs and file memory.
func (c *lspClient) closeDoc(abs string) {
	c.docMu.Lock()
	defer c.docMu.Unlock()
	c.closeDocLocked(abs)
}

// closeDocLocked is the docMu-held half of closeDoc; syncDoc's error path uses
// it directly to avoid re-locking.
func (c *lspClient) closeDocLocked(abs string) {
	uri := pathToURI(abs)
	c.mu.Lock()
	_, already := c.opened[uri]
	if already {
		delete(c.opened, uri)
		delete(c.openedText, uri)
		delete(c.syncGen, uri)
	}
	c.mu.Unlock()
	if !already {
		return
	}

	c.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{
			"uri": uri,
		},
	})
}

// ---------------------------------------------------------------- positions

// toLSP converts a 1-based line and 0-based byte column into the offsets the
// server expects. The spec counts UTF-16 code units by default, which is not
// what Go gives us.
func (c *lspClient) toLSP(lineText string, line, byteCol int) lspPosition {
	if byteCol < 0 {
		byteCol = 0
	}
	if byteCol > len(lineText) {
		byteCol = len(lineText)
	}
	prefix := lineText[:byteCol]
	var ch int
	switch c.encoding {
	case "utf-8":
		ch = byteCol
	case "utf-32":
		ch = utf8.RuneCountInString(prefix)
	default:
		// Count UTF-16 code units without allocating: one unit per rune,
		// plus one more for astral runes (surrogate pairs). Ranging over the
		// string yields the same runes []rune(prefix) would — including
		// U+FFFD per invalid byte — so this matches utf16.Encode exactly.
		for _, r := range prefix {
			if r >= 0x10000 {
				ch += 2
			} else {
				ch++
			}
		}
	}
	return lspPosition{Line: line - 1, Character: ch}
}

// fromLSP converts a server position back into a 1-based line and 0-based byte
// column against the file we have on disk.
func (c *lspClient) fromLSP(lines []string, p lspPosition) (int, int) {
	line := p.Line + 1
	if p.Line < 0 || p.Line >= len(lines) {
		return line, 0
	}
	if p.Character <= 0 {
		return line, 0
	}
	text := lines[p.Line]
	switch c.encoding {
	case "utf-8":
		if p.Character > len(text) {
			return line, len(text)
		}
		return line, p.Character
	case "utf-32":
		// Sum rune widths directly instead of materializing []rune and
		// re-encoding the prefix to a throwaway string.
		n, bytes := 0, 0
		for _, r := range text {
			if n >= p.Character {
				break
			}
			n++
			bytes += utf8.RuneLen(r)
		}
		return line, bytes
	default:
		units, bytes := 0, 0
		for _, r := range text {
			if units >= p.Character {
				break
			}
			if r >= 0x10000 {
				units += 2 // surrogate pair, no utf16.Encode allocation
			} else {
				units++
			}
			bytes += utf8.RuneLen(r)
		}
		return line, bytes
	}
}

// ---------------------------------------------------------------- uris

func pathToURI(p string) string {
	p = filepath.ToSlash(p)
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
	}
	u := url.URL{Scheme: "file", Path: p}
	return u.String()
}

func uriToPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" && u.Scheme != "" {
		return "", fmt.Errorf("not a file uri: %s", uri)
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
	}
	return filepath.FromSlash(p), nil
}
