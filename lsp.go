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
	"time"
	"unicode/utf16"
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

type lspClient struct {
	def  lspServerDef
	root string

	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	logf func(string, ...any)

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcMessage
	opened  map[string]int // uri -> document version
	dead    error

	// encoding is how the server counts Character offsets: "utf-8", "utf-16"
	// (the spec default) or "utf-32".
	encoding string

	readyCh chan struct{}
	once    sync.Once

	// indexing tracks $/progress tokens so callers can tell "no result" from
	// "the server has not finished indexing yet".
	indexMu  sync.Mutex
	indexing int
}

func newLSPClient(def lspServerDef, root string) *lspClient {
	return &lspClient{
		def: def, root: root,
		pending:  map[int64]chan rpcMessage{},
		opened:   map[string]int{},
		encoding: "utf-16",
		readyCh:  make(chan struct{}),
		logf:     func(string, ...any) {},
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

	go c.readLoop()
	go func() {
		c.cmd.Wait()
		c.fail(fmt.Errorf("%s exited", c.def.Name))
	}()

	return c.initialize(ctx)
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
	c.once.Do(func() { close(c.readyCh) })
}

func (c *lspClient) alive() error {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.write(body)
}

func (c *lspClient) onNotification(msg rpcMessage) {
	if msg.Method != "$/progress" {
		return
	}
	var p struct {
		Value struct {
			Kind string `json:"kind"`
		} `json:"value"`
	}
	if json.Unmarshal(msg.Params, &p) != nil {
		return
	}
	c.indexMu.Lock()
	switch p.Value.Kind {
	case "begin":
		c.indexing++
	case "end":
		if c.indexing > 0 {
			c.indexing--
		}
	}
	c.indexMu.Unlock()
}

func (c *lspClient) busy() bool {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	return c.indexing > 0
}

func readFrame(r *bufio.Reader) (rpcMessage, error) {
	var length int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return rpcMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, val, ok := strings.Cut(line, ":"); ok &&
			strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, _ = strconv.Atoi(strings.TrimSpace(val))
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

func (c *lspClient) write(body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead != nil {
		return c.dead
	}
	if c.in == nil {
		return fmt.Errorf("lsp client stdin closed")
	}
	if _, err := fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err := c.in.Write(body)
	return err
}

func (c *lspClient) notify(method string, params any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	return c.write(body)
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

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	if err := c.write(body); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// Tell the server to stop working on something nobody is waiting for.
		c.notify("$/cancelRequest", map[string]any{"id": id})
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
				"synchronization": map[string]any{"didSave": false, "dynamicRegistration": false},
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
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		return err
	}
	if res.Capabilities.PositionEncoding != "" {
		c.encoding = res.Capabilities.PositionEncoding
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		return err
	}
	c.once.Do(func() { close(c.readyCh) })
	return nil
}

func (c *lspClient) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.call(ctx, "shutdown", nil, nil)
	c.notify("exit", nil)
	if c.in != nil {
		c.in.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		time.AfterFunc(time.Second, func() { c.cmd.Process.Kill() })
	}
}

// ensureOpen tells the server about a file. Most servers refuse to answer
// questions about a document they were never handed.
func (c *lspClient) ensureOpen(abs, rel string) error {
	uri := pathToURI(abs)
	c.mu.Lock()
	_, already := c.opened[uri]
	c.mu.Unlock()
	if already {
		return nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	if err := c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": c.def.LanguageID(rel),
			"version":    1,
			"text":       string(data),
		},
	}); err != nil {
		return err
	}
	c.mu.Lock()
	c.opened[uri] = 1
	c.mu.Unlock()
	return nil
}

// closeDoc notifies the server that the file was closed, allowing the server
// to free ASTs and file memory.
func (c *lspClient) closeDoc(abs string) {
	uri := pathToURI(abs)
	c.mu.Lock()
	_, already := c.opened[uri]
	if !already {
		c.mu.Unlock()
		return
	}
	delete(c.opened, uri)
	c.mu.Unlock()
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
		ch = len(utf16.Encode([]rune(prefix)))
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
		r := []rune(text)
		if p.Character > len(r) {
			return line, len(text)
		}
		return line, len(string(r[:p.Character]))
	default:
		units, bytes := 0, 0
		for _, r := range text {
			if units >= p.Character {
				break
			}
			units += len(utf16.Encode([]rune{r}))
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
