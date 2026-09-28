package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Threads are long-running, multi-turn conversations with a coding harness,
// anchored to a range of code but free to read and change anything in the
// workspace. Where an inline edit is a single dispatch that is forgotten once
// it exits, a thread keeps its transcript on disk and continues the same
// harness session on every new message.
//
// Continuity works two ways. A harness with a session id px0 can control
// (claude, cursor-agent) resumes its own session, so the harness keeps its own
// context. Every other harness, and any harness switched to mid-thread, is
// replayed the earlier transcript inside the prompt.
//
// There is no overlap guard between threads or against inline edits: two
// harnesses writing the same lines is last write wins.

const (
	threadTurnTimeout = 30 * time.Minute
	threadSnippetMax  = 8000
	threadReplayMax   = 16000
	threadToolsMax    = 200
	threadSaveEvery   = 2 * time.Second
)

var (
	errThreadBusy = errors.New("this thread is still working on a reply")
	errThreadGone = errors.New("no such thread")
)

// threadTurn is one user message and the harness's answer to it.
type threadTurn struct {
	ID      int      `json:"id"`
	Prompt  string   `json:"prompt"`
	Reply   string   `json:"reply"`
	Tools   []string `json:"tools,omitempty"`   // what the harness did, one line each
	Changed []string `json:"changed,omitempty"` // files this turn touched
	Tracked bool     `json:"tracked"`           // false outside git: Changed is unknown, not empty
	Error   string   `json:"error,omitempty"`
	Running bool     `json:"running"`
	Harness string   `json:"harness"`
	Model   string   `json:"model,omitempty"`
	Started int64    `json:"started"` // unix ms
	Ms      int64    `json:"ms"`
}

// thread is the persisted conversation. Path and lines are where it was
// started, which is context for the harness and a jump target for the reader,
// not a limit on what it may change.
type thread struct {
	ID             string        `json:"id"`
	Title          string        `json:"title"`
	Kind           string        `json:"kind,omitempty"` // "edit" or "batch" when started from inline edits; empty for a conversation
	Path           string        `json:"path,omitempty"`
	L1             int           `json:"l1,omitempty"`
	L2             int           `json:"l2,omitempty"`
	Snippet        string        `json:"snippet,omitempty"`
	Created        int64         `json:"created"`
	Updated        int64         `json:"updated"`
	SessionID      string        `json:"sessionId,omitempty"`
	SessionHarness string        `json:"sessionHarness,omitempty"` // harness that owns SessionID
	SessionLive    bool          `json:"sessionLive,omitempty"`    // that harness has really started the session
	Turns          []*threadTurn `json:"turns"`
}

type threadSummary struct {
	ID      string `json:"id"`
	Kind    string `json:"kind,omitempty"`
	Title   string `json:"title"`
	Path    string `json:"path,omitempty"`
	L1      int    `json:"l1,omitempty"`
	L2      int    `json:"l2,omitempty"`
	Updated int64  `json:"updated"`
	Turns   int    `json:"turns"`
	Running bool   `json:"running"`
	Failed  bool   `json:"failed,omitempty"`
	// Touched: the last finished turn changed files, or could not say (no git),
	// so open tabs need a reload.
	Touched bool `json:"touched,omitempty"`
}

type threadStore struct {
	Root    string    `json:"root"`
	Threads []*thread `json:"threads"`
}

type threadEvent struct {
	name string
	data []byte
}

type threadManager struct {
	agent *agentManager
	root  string
	file  string // empty when there is nowhere to persist

	mu       sync.Mutex
	threads  map[string]*thread
	cancels  map[string]context.CancelFunc // one running turn per thread
	jobs     map[int64]*threadJobRef       // inline/batch edits, polled as agent jobs
	subs     map[string]map[chan threadEvent]struct{}
	lastSave time.Time
}

// threadStorePath keeps every workspace's threads beside settings.json, never
// inside the working tree.
func threadStorePath(root string) string {
	sp := settingsPath()
	if sp == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(filepath.Dir(sp), "threads", hex.EncodeToString(sum[:8])+".json")
}

func newThreadManager(a *agentManager, root string) *threadManager {
	tm := &threadManager{
		agent:   a,
		root:    root,
		file:    threadStorePath(root),
		threads: map[string]*thread{},
		cancels: map[string]context.CancelFunc{},
		jobs:    map[int64]*threadJobRef{},
		subs:    map[string]map[chan threadEvent]struct{}{},
	}
	tm.load()
	a.threadJob = tm.job
	a.threadCancel = tm.cancelJob
	return tm
}

// load restores saved threads. A turn that was running when px0 exited can
// never finish, so it is closed out as interrupted rather than left spinning.
func (tm *threadManager) load() {
	if tm.file == "" {
		return
	}
	data, err := os.ReadFile(tm.file)
	if err != nil {
		return
	}
	var st threadStore
	if json.Unmarshal(data, &st) != nil {
		return
	}
	for _, t := range st.Threads {
		if t == nil || t.ID == "" {
			continue
		}
		for _, tr := range t.Turns {
			if tr.Running {
				tr.Running = false
				if tr.Error == "" {
					tr.Error = "interrupted: px0 exited before this reply finished"
				}
			}
		}
		tm.threads[t.ID] = t
	}
}

// saveLocked writes the store atomically. Callers hold tm.mu.
func (tm *threadManager) saveLocked() {
	if tm.file == "" {
		return
	}
	st := threadStore{Root: tm.root}
	for _, t := range tm.threads {
		st.Threads = append(st.Threads, t)
	}
	sort.Slice(st.Threads, func(i, j int) bool { return st.Threads[i].Created < st.Threads[j].Created })
	data, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(tm.file), 0o755); err != nil {
		return
	}
	tmp := tm.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, tm.file); err != nil {
		os.Remove(tmp)
		return
	}
	tm.lastSave = time.Now()
}

func newThreadID() string {
	var b [5]byte
	rand.Read(b[:])
	return "t" + hex.EncodeToString(b[:])
}

// newUUID returns a random version 4 UUID, the form claude's --session-id wants.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func threadTitle(msg string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0])
	r := []rune(line)
	if len(r) > 60 {
		line = string(r[:60]) + "…"
	}
	if line == "" {
		line = "Untitled thread"
	}
	return line
}

func (t *thread) running() bool {
	return len(t.Turns) > 0 && t.Turns[len(t.Turns)-1].Running
}

func (t *thread) summary() threadSummary {
	s := threadSummary{ID: t.ID, Kind: t.Kind, Title: t.Title, Path: t.Path, L1: t.L1, L2: t.L2, Updated: t.Updated, Turns: len(t.Turns), Running: t.running()}
	if n := len(t.Turns); n > 0 && !s.Running {
		last := t.Turns[n-1]
		s.Failed = last.Error != ""
		s.Touched = len(last.Changed) > 0 || !last.Tracked
	}
	return s
}

// List returns every thread, most recently active first.
func (tm *threadManager) List() []threadSummary {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	out := make([]threadSummary, 0, len(tm.threads))
	for _, t := range tm.threads {
		out = append(out, t.summary())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	return out
}

func (tm *threadManager) Get(id string) *thread {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	t := tm.threads[id]
	if t == nil {
		return nil
	}
	return cloneThread(t)
}

func cloneThread(t *thread) *thread {
	c := *t
	c.Turns = make([]*threadTurn, len(t.Turns))
	for i, tr := range t.Turns {
		tc := *tr
		tc.Tools = append([]string(nil), tr.Tools...)
		tc.Changed = append([]string(nil), tr.Changed...)
		c.Turns[i] = &tc
	}
	return &c
}

// Create opens a thread anchored at path:l1-l2 (path may be empty for a thread
// about the workspace as a whole) and starts its first turn.
func (tm *threadManager) Create(abs, rel string, l1, l2 int, message string) (*thread, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, errors.New("message is empty")
	}
	t := &thread{
		ID:      newThreadID(),
		Title:   threadTitle(message),
		Path:    rel,
		Created: time.Now().UnixMilli(),
	}
	t.Updated = t.Created
	if rel != "" {
		if l1 < 1 {
			l1 = 1
		}
		if l2 < l1 {
			l2 = l1
		}
		t.L1, t.L2 = l1, l2
		t.Snippet = anchorSnippet(abs, l1, l2)
	}
	tm.mu.Lock()
	tm.threads[t.ID] = t
	tm.mu.Unlock()
	if _, err := tm.Send(t.ID, message); err != nil {
		tm.mu.Lock()
		delete(tm.threads, t.ID)
		tm.mu.Unlock()
		return nil, err
	}
	return tm.Get(t.ID), nil
}

// threadNative reports whether px0 can hand this harness its own session.
func threadNative(name string) bool {
	return name == "claude" || name == "cursor-agent" || name == "agy" || name == "gemini"
}

// threadArgv adds the session and output flags a thread turn needs to the
// harness's headless argv. Flag order does not matter to these CLIs, so the
// extras go right after the binary. Every other harness runs unchanged and is
// replayed the transcript instead.
func threadArgv(name string, template []string, sessionID string, resume bool) []string {
	var extra []string
	switch name {
	case "claude":
		extra = []string{"--output-format", "stream-json", "--verbose"}
		if resume {
			extra = append(extra, "--resume", sessionID)
		} else {
			extra = append(extra, "--session-id", sessionID)
		}
	case "cursor-agent":
		if sessionID != "" {
			extra = []string{"--resume", sessionID}
		}
	case "agy":
		extra = []string{"--output-format", "stream-json"}
		if resume && sessionID != "" {
			extra = append(extra, "--conversation", sessionID)
		}
	case "gemini":
		extra = []string{"--output-format", "stream-json"}
		if resume && sessionID != "" {
			extra = append(extra, "--resume", sessionID)
		} else if sessionID != "" {
			extra = append(extra, "--session-id", sessionID)
		}
	}
	out := make([]string, 0, len(template)+len(extra))
	out = append(out, template[0])
	out = append(out, extra...)
	out = append(out, template[1:]...)
	return out
}

// threadPrompt composes what the harness is told for one turn. The anchor and
// ground rules go in only when the harness has no memory of them: the first
// turn, or a replay after a harness switch.
func threadPrompt(t *thread, prior []*threadTurn, message string, replay bool) string {
	var b strings.Builder
	if len(prior) == 0 || replay {
		if t.Kind == "edit" || t.Kind == "batch" {
			b.WriteString("Carry out the edit request below by editing files in place. ")
			b.WriteString("Change only what it asks for; you may touch other files if the change needs it. ")
			b.WriteString("When done, reply in a sentence or two saying what you changed and in which files. ")
			b.WriteString("The user may follow up in this conversation.\n\n")
		} else {
			b.WriteString("You are helping with a long-running conversation about the code in this workspace. ")
			b.WriteString("You may read any file and edit any file needed to carry out what is asked; make edits directly. ")
			b.WriteString("When you finish a request, reply with a concise summary: what you found or changed, and in which files.\n\n")
		}
		if t.Path != "" && t.Kind != "batch" { // a batch carries its own snippets
			ext := strings.TrimPrefix(filepath.Ext(t.Path), ".")
			lineStr := fmt.Sprintf("lines %d-%d", t.L1, t.L2)
			if t.L1 == t.L2 {
				lineStr = fmt.Sprintf("line %d", t.L1)
			}
			fmt.Fprintf(&b, "The conversation started at @%s %s (the file may have changed since):\n```%s\n%s\n```\n\n", t.Path, lineStr, ext, t.Snippet)
		}
	}
	if replay && len(prior) > 0 {
		var h strings.Builder
		for _, tr := range prior {
			fmt.Fprintf(&h, "User: %s\n\nAssistant: %s\n\n", tr.Prompt, strings.TrimSpace(tr.Reply))
			if len(tr.Changed) > 0 {
				fmt.Fprintf(&h, "(files changed in that turn: %s)\n\n", strings.Join(tr.Changed, ", "))
			}
		}
		hist := h.String()
		if len(hist) > threadReplayMax {
			hist = "…" + hist[len(hist)-threadReplayMax:]
		}
		b.WriteString("### Earlier in this conversation\n")
		b.WriteString(hist)
		b.WriteString("### Current request\n")
	}
	if t.Kind == "edit" && len(prior) == 0 {
		b.WriteString("### Instruction\n")
	}
	b.WriteString(message)
	return b.String()
}

// threadRun is everything a turn's goroutine needs, decided under the lock.
type threadRun struct {
	tid     string
	turn    *threadTurn
	name    string
	model   string
	base    []string // the selected harness's headless argv
	message string
	prior   []*threadTurn
}

// Send appends a message to the thread and starts the harness on it.
func (tm *threadManager) Send(id, message string) (*thread, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, errors.New("message is empty")
	}
	name, base, model := tm.agent.current()
	if base == nil {
		uiStatus("err", "thread", "dispatch refused: no coding harness selected", 0, os.Stdout)
		return nil, errAgentNone
	}

	tm.mu.Lock()
	t := tm.threads[id]
	if t == nil {
		tm.mu.Unlock()
		return nil, errThreadGone
	}
	if t.running() {
		tm.mu.Unlock()
		return nil, errThreadBusy
	}
	// A session id belongs to the harness that made it. Switching harness
	// starts a fresh session there, primed by replaying the transcript.
	if t.SessionHarness != name {
		t.SessionID, t.SessionLive, t.SessionHarness = "", false, name
	}
	prior := make([]*threadTurn, 0, len(t.Turns))
	for _, tr := range t.Turns {
		c := *tr
		prior = append(prior, &c)
	}
	turn := &threadTurn{
		ID: len(t.Turns) + 1, Prompt: message, Running: true, Tracked: gitAvailable(tm.root),
		Harness: name, Model: model, Started: time.Now().UnixMilli(),
	}
	t.Turns = append(t.Turns, turn)
	t.Updated = turn.Started
	ctx, cancel := context.WithTimeout(context.Background(), threadTurnTimeout)
	tm.cancels[id] = cancel
	tm.saveLocked()
	tm.publishThreadLocked(t)
	tm.mu.Unlock()

	modelNote := ""
	if model != "" {
		modelNote = " (" + model + ")"
	}
	uiStatus("step", "thread", fmt.Sprintf("%s turn %d · %s%s · %s", id, turn.ID, name, modelNote, t.Title), 0, os.Stdout)

	go tm.runTurn(ctx, cancel, &threadRun{tid: id, turn: turn, name: name, model: model, base: base, message: message, prior: prior})
	return tm.Get(id), nil
}

// createCursorChat asks cursor-agent for a chat id it will accept on --resume.
func (tm *threadManager) createCursorChat(ctx context.Context, bin string) string {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, bin, "create-chat")
	cmd.Dir = tm.root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

func (tm *threadManager) runTurn(ctx context.Context, cancel context.CancelFunc, r *threadRun) {
	defer cancel()

	// Settle the session before composing the prompt: whether the harness
	// remembers the conversation decides whether it must be replayed.
	tm.mu.Lock()
	t := tm.threads[r.tid]
	if t == nil {
		tm.mu.Unlock()
		return
	}
	sessionID, live := t.SessionID, t.SessionLive
	tm.mu.Unlock()

	if sessionID == "" {
		switch r.name {
		case "claude", "gemini":
			sessionID = newUUID()
		case "cursor-agent":
			sessionID = tm.createCursorChat(ctx, r.base[0])
		}
	}
	native := threadNative(r.name) && (sessionID != "" || r.name == "agy")
	// Only a session the harness really started remembers the conversation.
	// Anything else, including a session whose first turn failed, is replayed.
	replay := len(r.prior) > 0 && !(native && live)

	tm.mu.Lock()
	if t := tm.threads[r.tid]; t != nil && native && sessionID != "" {
		t.SessionID = sessionID
	}
	tm.mu.Unlock()

	prompt := threadPrompt(t, r.prior, r.message, replay)
	argv := r.base
	if native {
		argv = threadArgv(r.name, r.base, sessionID, live)
	}
	args := make([]string, len(argv))
	for i, tok := range argv {
		args[i] = strings.ReplaceAll(tok, "{prompt}", prompt)
	}

	if uiVerbose {
		uiVerbosePrompt(0, r.name, prompt, os.Stdout)
	}

	before := worktreeSnapshot(tm.root)
	started := time.Now()
	stderr := &tailBuffer{max: agentLogBytes}
	sink := &threadSink{tm: tm, tid: r.tid, turn: r.turn, harness: r.name}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = tm.root
	cmd.Stdout = sink
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	setProcessGroup(cmd)
	// stdin stays empty for the same reason as an inline edit: a harness that
	// wants to ask something should fail fast, not hang.

	err := cmd.Run()
	sink.flush()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			err = errors.New("cancelled")
		} else {
			err = fmt.Errorf("gave up after %s", threadTurnTimeout)
		}
	}
	if err == nil && sink.failure != "" {
		err = errors.New(sink.failure)
	}

	changed := changedSince(tm.root, before)
	tm.agent.settle(changed)

	tm.mu.Lock()
	if t := tm.threads[r.tid]; t != nil {
		if t.SessionID == "" && sink.sessionID != "" {
			t.SessionID = sink.sessionID
		}
		if (sink.started && (r.name == "claude" || r.name == "agy" || r.name == "gemini")) || (r.name == "cursor-agent" && native && err == nil) {
			t.SessionLive = true
		}
		r.turn.Running = false
		r.turn.Ms = time.Since(started).Milliseconds()
		r.turn.Changed = changed
		if err != nil {
			msg := err.Error()
			if tail := strings.TrimSpace(stderr.String()); tail != "" && strings.TrimSpace(r.turn.Reply) == "" {
				if len(tail) > 2000 {
					tail = tail[len(tail)-2000:]
				}
				msg += "\n" + tail
			}
			r.turn.Error = msg
		}
		t.Updated = time.Now().UnixMilli()
		delete(tm.cancels, r.tid)
		tm.saveLocked()
		tm.publishThreadLocked(t)
	}
	tm.mu.Unlock()

	dur := fmtDuration(time.Since(started))
	if err != nil {
		uiStatus("err", "thread", fmt.Sprintf("%s turn %d failed in %s: %s", r.tid, r.turn.ID, dur, err.Error()), 0, os.Stdout)
	} else {
		uiStatus("ok", "thread", fmt.Sprintf("%s turn %d · %s  (%d files changed)", r.tid, r.turn.ID, dur, len(changed)), 0, os.Stdout)
	}
}

// Cancel stops the turn running in a thread. What it already wrote stays.
func (tm *threadManager) Cancel(id string) bool {
	tm.mu.Lock()
	cancel := tm.cancels[id]
	tm.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// Delete removes a thread, stopping its turn first.
func (tm *threadManager) Delete(id string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.threads[id] == nil {
		return false
	}
	if cancel := tm.cancels[id]; cancel != nil {
		cancel()
	}
	delete(tm.threads, id)
	tm.saveLocked()
	tm.publishLocked(id, "deleted", map[string]string{"id": id})
	tm.publishLocked("", "deleted", map[string]string{"id": id})
	return true
}

func (tm *threadManager) Close() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for _, cancel := range tm.cancels {
		cancel()
	}
}

// ---------------------------------------------------------------- output

// threadSink turns a harness's stdout into the turn's reply as it arrives.
// claude, agy, and gemini speak stream-json, one event per line, from which the
// assistant's text deltas and tool calls are pulled and streamed live to the UI.
// Anything else is treated as the reply itself, line by line.
type threadSink struct {
	tm        *threadManager
	tid       string
	turn      *threadTurn
	harness   string
	buf       []byte
	started   bool   // harness reported a session id, so --resume will work next
	sessionID string // session id discovered from stream
	failure   string // harness's own error result
}

func (s *threadSink) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := s.buf[:i]
		s.buf = s.buf[i+1:]
		s.line(string(bytes.TrimRight(line, "\r")))
	}
	return len(p), nil
}

func (s *threadSink) flush() {
	if len(bytes.TrimSpace(s.buf)) > 0 {
		s.line(string(s.buf))
	}
	s.buf = nil
}

func (s *threadSink) line(l string) {
	switch s.harness {
	case "claude":
		for _, ev := range parseClaudeEvent(l) {
			s.handleEvent(ev)
		}
	case "agy":
		for _, ev := range parseAgyEvent(l) {
			s.handleEvent(ev)
		}
	case "gemini":
		for _, ev := range parseGeminiEvent(l) {
			s.handleEvent(ev)
		}
	default:
		s.text(l+"\n", false)
	}
}

func (s *threadSink) handleEvent(ev claudeEvent) {
	switch ev.kind {
	case "session":
		s.started = true
		if ev.text != "" {
			s.sessionID = ev.text
		}
	case "text":
		s.text(ev.text, ev.block)
	case "tool":
		s.tool(ev.text)
	case "error":
		s.failure = ev.text
	case "result":
		if strings.TrimSpace(s.reply()) == "" {
			s.text(ev.text, true)
		}
	}
}

func (s *threadSink) reply() string {
	s.tm.mu.Lock()
	defer s.tm.mu.Unlock()
	return s.turn.Reply
}

// text appends to the reply. block marks a whole assistant message, which is
// set off from the previous one by a blank line.
func (s *threadSink) text(text string, block bool) {
	if text == "" {
		return
	}
	s.tm.mu.Lock()
	defer s.tm.mu.Unlock()
	if block && s.turn.Reply != "" && !strings.HasSuffix(s.turn.Reply, "\n\n") {
		text = "\n\n" + text
	}
	s.turn.Reply += text
	s.tm.publishLocked(s.tid, "delta", map[string]any{"turn": s.turn.ID, "text": text})
	s.tm.maybeSaveLocked()
}

func (s *threadSink) tool(text string) {
	s.tm.mu.Lock()
	defer s.tm.mu.Unlock()
	if len(s.turn.Tools) < threadToolsMax {
		s.turn.Tools = append(s.turn.Tools, text)
	}
	s.tm.publishLocked(s.tid, "tool", map[string]any{"turn": s.turn.ID, "text": text})
}

// maybeSaveLocked checkpoints a long reply so an exit mid-turn loses seconds,
// not the whole answer.
func (tm *threadManager) maybeSaveLocked() {
	if time.Since(tm.lastSave) >= threadSaveEvery {
		tm.saveLocked()
	}
}

type claudeEvent struct {
	kind  string // session | text | tool | result | error
	text  string
	block bool
}

// parseClaudeEvent reads one line of `claude --output-format stream-json`.
// Lines that are not events px0 cares about, or not JSON at all, yield nothing.
func parseClaudeEvent(line string) []claudeEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var ev struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		IsError   bool   `json:"is_error"`
		Result    string `json:"result"`
		Message   struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return nil
	}
	var out []claudeEvent
	if ev.SessionID != "" {
		out = append(out, claudeEvent{kind: "session"})
	}
	switch ev.Type {
	case "assistant":
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if strings.TrimSpace(c.Text) != "" {
					out = append(out, claudeEvent{kind: "text", text: c.Text, block: true})
				}
			case "tool_use":
				out = append(out, claudeEvent{kind: "tool", text: toolLabel(c.Name, c.Input)})
			}
		}
	case "result":
		if ev.IsError {
			msg := strings.TrimSpace(ev.Result)
			if msg == "" {
				msg = "the harness reported an error"
			}
			out = append(out, claudeEvent{kind: "error", text: msg})
		} else if strings.TrimSpace(ev.Result) != "" {
			out = append(out, claudeEvent{kind: "result", text: ev.Result})
		}
	}
	return out
}

// parseAgyEvent reads one line of `agy --output-format stream-json`.
func parseAgyEvent(line string) []claudeEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var ev struct {
		Event          string `json:"event"`
		ConversationID string `json:"conversation_id"`
		StepUpdate     *struct {
			StepType string `json:"step_type"`
			State    string `json:"state"`
			ToolName string `json:"tool_name"`
			ToolInfo *struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"tool_info"`
			TextDelta string `json:"text_delta"`
		} `json:"step_update"`
		Result *struct {
			Status   string `json:"status"`
			Response string `json:"response"`
			Error    string `json:"error"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return nil
	}
	var out []claudeEvent
	if ev.Event == "init" && ev.ConversationID != "" {
		out = append(out, claudeEvent{kind: "session", text: ev.ConversationID})
	}
	if ev.Event == "step_update" && ev.StepUpdate != nil {
		su := ev.StepUpdate
		switch su.StepType {
		case "tool":
			if su.State == "ACTIVE" || su.State == "" {
				name := su.ToolName
				var params json.RawMessage
				if su.ToolInfo != nil {
					if su.ToolInfo.Name != "" {
						name = su.ToolInfo.Name
					}
					params = su.ToolInfo.Parameters
				}
				out = append(out, claudeEvent{kind: "tool", text: toolLabel(name, params)})
			}
		case "agent_response":
			if su.TextDelta != "" {
				out = append(out, claudeEvent{kind: "text", text: su.TextDelta, block: false})
			}
		}
	}
	if ev.Event == "result" && ev.Result != nil {
		r := ev.Result
		if r.Status != "" && r.Status != "SUCCESS" {
			errText := r.Error
			if errText == "" {
				errText = "the harness reported an error"
			}
			out = append(out, claudeEvent{kind: "error", text: errText})
		} else if strings.TrimSpace(r.Response) != "" {
			out = append(out, claudeEvent{kind: "result", text: r.Response})
		}
	}
	return out
}

// parseGeminiEvent reads one line of `gemini --output-format stream-json`.
func parseGeminiEvent(line string) []claudeEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var ev struct {
		Type       string          `json:"type"`
		SessionID  string          `json:"session_id"`
		Role       string          `json:"role"`
		Content    string          `json:"content"`
		Delta      bool            `json:"delta"`
		ToolName   string          `json:"tool_name"`
		Parameters json.RawMessage `json:"parameters"`
		Status     string          `json:"status"`
		Error      string          `json:"error"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return nil
	}
	var out []claudeEvent
	if ev.Type == "init" && ev.SessionID != "" {
		out = append(out, claudeEvent{kind: "session", text: ev.SessionID})
	}
	if ev.Type == "tool_use" {
		out = append(out, claudeEvent{kind: "tool", text: toolLabel(ev.ToolName, ev.Parameters)})
	}
	if ev.Type == "message" && ev.Role == "assistant" && ev.Content != "" {
		out = append(out, claudeEvent{kind: "text", text: ev.Content, block: false})
	}
	if ev.Type == "result" {
		if ev.Status != "" && ev.Status != "success" {
			errText := ev.Error
			if errText == "" {
				errText = "gemini reported an error"
			}
			out = append(out, claudeEvent{kind: "error", text: errText})
		}
	}
	return out
}

// toolLabel is "Edit server.go" or "Bash go test ./...": the tool and the one
// argument that says what it acted on.
func toolLabel(name string, input json.RawMessage) string {
	var in map[string]any
	json.Unmarshal(input, &in)
	for _, k := range []string{"file_path", "path", "AbsolutePath", "TargetFile", "command", "CommandLine", "pattern", "query", "url", "description"} {
		if v, ok := in[k].(string); ok && v != "" {
			v = strings.Join(strings.Fields(v), " ")
			if r := []rune(v); len(r) > 120 {
				v = string(r[:120]) + "…"
			}
			return name + " " + v
		}
	}
	return name
}

// ---------------------------------------------------------------- events

// publishLocked delivers an event to the subscribers of one thread ("" is the
// list channel). A subscriber that cannot keep up is dropped, and its client
// reconnects to a fresh snapshot, so nothing is ever delivered out of order.
func (tm *threadManager) publishLocked(id, name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	for ch := range tm.subs[id] {
		select {
		case ch <- threadEvent{name, data}:
		default:
			delete(tm.subs[id], ch)
			close(ch)
		}
	}
}

func (tm *threadManager) publishThreadLocked(t *thread) {
	tm.publishLocked(t.ID, "thread", t)
	tm.publishLocked("", "summary", t.summary())
}

// subscribe registers for events on id ("" for the list) and returns the
// current state taken under the same lock, so nothing falls between the two.
func (tm *threadManager) subscribe(id string) (chan threadEvent, threadEvent, func(), bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	var first threadEvent
	if id == "" {
		list := make([]threadSummary, 0, len(tm.threads))
		for _, t := range tm.threads {
			list = append(list, t.summary())
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Updated > list[j].Updated })
		data, _ := json.Marshal(list)
		first = threadEvent{"list", data}
	} else {
		t := tm.threads[id]
		if t == nil {
			return nil, first, nil, false
		}
		data, _ := json.Marshal(t)
		first = threadEvent{"thread", data}
	}
	ch := make(chan threadEvent, 256)
	if tm.subs[id] == nil {
		tm.subs[id] = map[chan threadEvent]struct{}{}
	}
	tm.subs[id][ch] = struct{}{}
	return ch, first, func() {
		tm.mu.Lock()
		defer tm.mu.Unlock()
		if _, ok := tm.subs[id][ch]; ok {
			delete(tm.subs[id], ch)
			close(ch)
		}
	}, true
}

// ---------------------------------------------------------------- HTTP

func (s *Server) threadsOrFail(w http.ResponseWriter) bool {
	if s.threads == nil {
		fail(w, http.StatusNotFound, "threads are not available in this session")
		return false
	}
	return true
}

func threadStatus(err error) int {
	switch {
	case errors.Is(err, errThreadBusy):
		return http.StatusConflict
	case errors.Is(err, errThreadGone):
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func (s *Server) handleThreads(w http.ResponseWriter, r *http.Request) {
	if !s.threadsOrFail(w) {
		return
	}
	writeJSON(w, map[string]any{"threads": s.threads.List()})
}

func (s *Server) handleThreadGet(w http.ResponseWriter, r *http.Request) {
	if !s.threadsOrFail(w) {
		return
	}
	t := s.threads.Get(r.URL.Query().Get("id"))
	if t == nil {
		fail(w, http.StatusNotFound, errThreadGone.Error())
		return
	}
	writeJSON(w, t)
}

func (s *Server) handleThreadCreate(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) || !s.threadsOrFail(w) {
		return
	}
	var body struct {
		Path    string `json:"path"`
		L1      int    `json:"l1"`
		L2      int    `json:"l2"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "bad request")
		return
	}
	var abs, rel string
	if body.Path != "" {
		var ok bool
		if abs, rel, ok = s.resolvePath(body.Path); !ok {
			fail(w, 400, "bad path")
			return
		}
	}
	t, err := s.threads.Create(abs, rel, body.L1, body.L2, body.Message)
	if err != nil {
		fail(w, threadStatus(err), err.Error())
		return
	}
	writeJSON(w, t)
}

func (s *Server) handleThreadSend(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) || !s.threadsOrFail(w) {
		return
	}
	var body struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "bad request")
		return
	}
	t, err := s.threads.Send(body.ID, body.Message)
	if err != nil {
		fail(w, threadStatus(err), err.Error())
		return
	}
	writeJSON(w, t)
}

func (s *Server) handleThreadCancel(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) || !s.threadsOrFail(w) {
		return
	}
	writeJSON(w, map[string]any{"cancelled": s.threads.Cancel(r.URL.Query().Get("id"))})
}

func (s *Server) handleThreadDelete(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) || !s.threadsOrFail(w) {
		return
	}
	writeJSON(w, map[string]any{"deleted": s.threads.Delete(r.URL.Query().Get("id"))})
}

// handleThreadStream is the SSE feed: one thread's snapshot and live deltas
// with ?id=, or the thread list's changes without it.
func (s *Server) handleThreadStream(w http.ResponseWriter, r *http.Request) {
	if !s.threadsOrFail(w) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch, first, unsub, ok := s.threads.subscribe(r.URL.Query().Get("id"))
	if !ok {
		fail(w, http.StatusNotFound, errThreadGone.Error())
		return
	}
	defer unsub()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	write := func(ev threadEvent) bool {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !write(first) {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open || !write(ev) {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ---------------------------------------------------------------- inline edits

// threadJobRef ties an agent-style job id, which the inline and batch edit UI
// polls, to the thread turn doing the work.
type threadJobRef struct {
	tid   string
	turn  int
	items []agentBatchItem
}

// StartEdit runs inline and batch edits as a thread, so every edit leaves a
// transcript that can be followed up. It keeps the guarantee those flows have
// always had: an edit on lines another edit is still changing is refused.
// Conversations started from the Threads pane are never guarded this way.
func (tm *threadManager) StartEdit(items []agentBatchItem) (*agentJob, error) {
	if len(items) == 0 {
		return nil, errors.New("no edits specified")
	}
	for i := range items {
		items[i].Instruction = strings.TrimSpace(items[i].Instruction)
		if items[i].Instruction == "" {
			return nil, errors.New("instruction is empty")
		}
		if items[i].L1 < 1 {
			items[i].L1 = 1
		}
		if items[i].L2 < items[i].L1 {
			items[i].L2 = items[i].L1
		}
		for j := 0; j < i; j++ {
			if items[i].Path == items[j].Path && items[i].L1 <= items[j].L2 && items[j].L1 <= items[i].L2 {
				return nil, fmt.Errorf("overlapping edits in batch on %s (%s and %s)", items[i].Path, lineRef(items[i].L1, items[i].L2), lineRef(items[j].L1, items[j].L2))
			}
		}
	}

	if busy := tm.overlappingEdit(items); busy != "" {
		uiStatus("warn", "agent", "edit dispatch refused: "+busy, 0, os.Stdout)
		return nil, fmt.Errorf("%w: %s", errAgentBusy, busy)
	}

	t := &thread{ID: newThreadID(), Created: time.Now().UnixMilli()}
	t.Updated = t.Created
	var message string
	if len(items) == 1 {
		it := items[0]
		t.Kind, t.Path, t.L1, t.L2 = "edit", it.Path, it.L1, it.L2
		t.Snippet = anchorSnippet(it.Abs, it.L1, it.L2)
		t.Title = "Edit: " + threadTitle(it.Instruction)
		message = it.Instruction
	} else {
		t.Kind, t.Path, t.L1, t.L2 = "batch", items[0].Path, items[0].L1, items[0].L2
		t.Title = fmt.Sprintf("Batch edit: %d changes", len(items))
		var b strings.Builder
		fmt.Fprintf(&b, "Apply these %d edits together.\n\n", len(items))
		for i, it := range items {
			snip, err := readLineRange(it.Abs, it.L1, it.L2)
			if err != nil {
				return nil, err
			}
			if len(snip) > threadSnippetMax {
				snip = snip[:threadSnippetMax] + "\n…"
			}
			fmt.Fprintf(&b, "### Edit %d: @%s %s\n```%s\n%s\n```\n%s\n\n", i+1, it.Path, lineRef(it.L1, it.L2),
				strings.TrimPrefix(filepath.Ext(it.Path), "."), snip, it.Instruction)
		}
		message = strings.TrimSpace(b.String())
	}

	tm.mu.Lock()
	tm.threads[t.ID] = t
	tm.mu.Unlock()
	if _, err := tm.Send(t.ID, message); err != nil {
		tm.mu.Lock()
		delete(tm.threads, t.ID)
		tm.mu.Unlock()
		return nil, err
	}
	id := tm.agent.nextJobID()
	tm.mu.Lock()
	tm.jobs[id] = &threadJobRef{tid: t.ID, turn: 1, items: items}
	tm.mu.Unlock()
	return tm.job(id), nil
}

// overlappingEdit describes an edit still running on lines these items touch,
// or returns "" when there is none.
func (tm *threadManager) overlappingEdit(items []agentBatchItem) string {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	for id, ref := range tm.jobs {
		t := tm.threads[ref.tid]
		if t == nil || !t.running() {
			continue
		}
		for _, run := range ref.items {
			for _, it := range items {
				if run.Path == it.Path && it.L1 <= run.L2 && run.L1 <= it.L2 {
					return fmt.Sprintf("an edit is already running on %s:%s (job #%d with %s)", it.Path, lineRef(it.L1, it.L2), id, t.Turns[len(t.Turns)-1].Harness)
				}
			}
		}
	}
	return ""
}

// job presents an inline or batch edit's turn in the shape the edit UI polls.
// id 0 means the most recent one.
func (tm *threadManager) job(id int64) *agentJob {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if id == 0 {
		for cand := range tm.jobs {
			if cand > id {
				id = cand
			}
		}
	}
	ref := tm.jobs[id]
	if ref == nil {
		return nil
	}
	t := tm.threads[ref.tid]
	if t == nil || ref.turn > len(t.Turns) {
		return nil
	}
	turn := t.Turns[ref.turn-1]
	j := &agentJob{
		ID: id, ThreadID: t.ID, Harness: turn.Harness, Path: ref.items[0].Path, Lines: lineRef(ref.items[0].L1, ref.items[0].L2),
		Running: turn.Running, Log: turn.Reply, Stdout: turn.Reply,
		Changed: append([]string{}, turn.Changed...), Ms: turn.Ms, Tracked: turn.Tracked,
	}
	if len(ref.items) > 1 {
		j.BatchCount, j.Items = len(ref.items), ref.items
	}
	if turn.Running {
		j.Ms = time.Now().UnixMilli() - turn.Started
	}
	if turn.Error != "" {
		first, rest, _ := strings.Cut(turn.Error, "\n")
		j.Error, j.Stderr = first, rest
	}
	return j
}

// cancelJob stops the edit with this job id, or every running edit for id 0.
func (tm *threadManager) cancelJob(id int64) bool {
	tm.mu.Lock()
	var tids []string
	for jid, ref := range tm.jobs {
		if id == 0 || jid == id {
			tids = append(tids, ref.tid)
		}
	}
	tm.mu.Unlock()
	cancelled := false
	for _, tid := range tids {
		if tm.Cancel(tid) {
			cancelled = true
		}
	}
	return cancelled
}

func anchorSnippet(abs string, l1, l2 int) string {
	snip, err := readLineRange(abs, l1, l2)
	if err != nil {
		return ""
	}
	if len(snip) > threadSnippetMax {
		snip = snip[:threadSnippetMax] + "\n…"
	}
	return snip
}
