package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Editing through a coding harness. px0 never authors a change itself: it
// composes an instruction anchored to a line range, hands it to a harness
// already installed on this machine, and reloads whatever moved once that
// harness exits. The harness edits; px0 stays the reader that knows exactly
// when to look again. The one write px0 makes is putting back what a harness
// changed, when asked to undo it (agent_undo.go).
//
// Harnesses are discovered the same way language servers are, and the one to
// use is chosen in the UI. Discovery alone never enables editing: running a
// general-purpose agent over a workspace is a decision the user makes once,
// and it is remembered in the settings file rather than a flag.

const (
	agentTimeout  = 10 * time.Minute
	agentLogBytes = 32 << 10
)

// agentPreset is a harness px0 knows and the argv that runs it headless. Each
// of these starts an interactive session by default and would sit forever
// waiting for approval, so every preset carries the flag that turns that off
// and the one that lets it apply edits without asking.
type agentPreset struct {
	Name string
	Args []string
}

var agentPresets = []agentPreset{
	{"claude", []string{"claude", "-p", "--permission-mode", "acceptEdits", "{prompt}"}},
	{"gemini", []string{"gemini", "--approval-mode", "auto_edit", "-p", "{prompt}"}},
	{"cursor-agent", []string{"cursor-agent", "-p", "--force", "{prompt}"}},
}

// agentHarness is one row of the picker.
type agentHarness struct {
	Name      string `json:"name"`
	Cmd       string `json:"cmd"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

// agentJob is one dispatch, snapshot-able while it runs.
type agentJob struct {
	ID      int64    `json:"id"`
	Harness string   `json:"harness"`
	Path    string   `json:"path"`
	Lines   string   `json:"lines"`
	Running bool     `json:"running"`
	Error   string   `json:"error,omitempty"`
	Log     string   `json:"log"`
	Stdout  string   `json:"stdout,omitempty"`
	Stderr  string   `json:"stderr,omitempty"`
	Changed []string `json:"changed"`
	Ms      int64    `json:"ms"`
	// Undoable says the changes can still be reversed through /api/agent/undo.
	// UndoNote says why not when the run changed files but no undo is possible.
	Undoable bool   `json:"undoable"`
	UndoNote string `json:"undoNote,omitempty"`
	// Tracked is false outside a git repository, where px0 cannot tell which
	// files a harness touched. An empty Changed then means "unknown", not
	// "nothing", and the client reloads regardless.
	Tracked bool `json:"tracked"`

	out    *tailBuffer
	stderr *tailBuffer
	start  time.Time
}

var (
	// The refusals the UI reacts to rather than merely reporting.
	errAgentBusy  = errors.New("an edit is already running")
	errAgentDirty = errors.New("uncommitted")
	errAgentNone  = errors.New("no coding harness is selected")
)

// agentManager owns discovery, the current choice, and the single in-flight
// edit. One edit at a time for the whole workspace: two harnesses rewriting one
// tree concurrently produces a state nobody can review afterwards.
type agentManager struct {
	root string
	lsp  *lspManager

	mu       sync.Mutex
	selected string   // preset name, or the template itself when pinned
	args     []string // resolved argv, nil when nothing is selected
	pinned   bool     // -agent was given, so the UI cannot change it
	job      *agentJob
	undo     *agentUndo // reverses the last job's changes, nil once used or unavailable
	cancel   context.CancelFunc
	seq      int64
}

// newAgentManager wires discovery and restores the remembered choice. A flag
// value pins a harness (or an arbitrary command template) for this run and is
// the only case that can fail: a bad -agent should stop startup, whereas a
// stale settings file should just leave nothing selected.
func newAgentManager(root, flagSpec string, lsp *lspManager) (*agentManager, error) {
	m := &agentManager{root: root, lsp: lsp}

	if spec := strings.TrimSpace(flagSpec); spec != "" {
		name, args, err := resolveAgentSpec(spec)
		if err != nil {
			return nil, err
		}
		m.selected, m.args, m.pinned = name, args, true
		return m, nil
	}

	if saved := readSettings().Agent; saved != "" {
		if name, args, err := resolveAgentSpec(saved); err == nil {
			m.selected, m.args = name, args
		}
	}
	return m, nil
}

// resolveAgentSpec turns a preset name or a command template into argv, and
// verifies the binary exists now rather than at first use.
func resolveAgentSpec(spec string) (string, []string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", nil, errors.New("empty harness")
	}

	var name string
	var args []string
	for _, p := range agentPresets {
		if strings.EqualFold(spec, p.Name) {
			name, args = p.Name, p.Args
			break
		}
	}
	if args == nil {
		args = strings.Fields(spec)
		if len(args) == 0 {
			return "", nil, errors.New("empty harness command")
		}
		if !strings.Contains(spec, "{prompt}") {
			return "", nil, fmt.Errorf("a command template must contain {prompt} (known harnesses: %s)",
				strings.Join(agentPresetNames(), ", "))
		}
		name = filepath.Base(args[0])
	}

	bin, ok := lookPathIn(args[0], lspBinDirs())
	if !ok {
		return "", nil, fmt.Errorf("%s is not installed", args[0])
	}
	resolved := append([]string(nil), args...)
	resolved[0] = bin
	return name, resolved, nil
}

func agentPresetNames() []string {
	names := make([]string, len(agentPresets))
	for i, p := range agentPresets {
		names[i] = p.Name
	}
	return names
}

// Detect reports every harness px0 knows and whether it is installed right
// now, so a tool installed since startup shows up without a restart.
func (m *agentManager) Detect() []agentHarness {
	out := make([]agentHarness, 0, len(agentPresets))
	for _, p := range agentPresets {
		h := agentHarness{Name: p.Name, Cmd: strings.Join(p.Args, " ")}
		if bin, ok := lookPathIn(p.Args[0], lspBinDirs()); ok {
			h.Installed, h.Path = true, bin
		}
		out = append(out, h)
	}
	return out
}

func (m *agentManager) Name() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selected
}

func (m *agentManager) Pinned() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinned
}

// Select remembers a harness for this workspace and every later run. Passing an
// empty name turns editing back off.
func (m *agentManager) Select(name string) error {
	m.mu.Lock()
	if m.pinned {
		m.mu.Unlock()
		return errors.New("px0 was started with -agent, so the harness is fixed for this run")
	}
	if m.job != nil && m.job.Running {
		m.mu.Unlock()
		return errAgentBusy
	}
	m.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		m.mu.Lock()
		m.selected, m.args = "", nil
		m.mu.Unlock()
		return writeSettings(settings{})
	}

	display, args, err := resolveAgentSpec(name)
	if err != nil {
		uiStatus("err", fmt.Sprintf("agent: failed to select harness %q", name), err.Error(), 0, os.Stdout)
		return err
	}
	m.mu.Lock()
	m.selected, m.args = display, args
	m.mu.Unlock()
	uiStatus("ok", fmt.Sprintf("agent: selected harness %s", display), strings.Join(args, " "), 0, os.Stdout)
	// Persist the spec as given, not the display name: a command template
	// shortens to its binary for display and would not survive the round trip.
	return writeSettings(settings{Agent: name})
}

// Job returns a snapshot of the current or most recent run, or nil.
func (m *agentManager) Job() *agentJob {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil {
		return nil
	}
	cp := *m.job
	cp.Log = m.job.out.String()
	cp.Stdout = cp.Log
	if m.job.stderr != nil {
		cp.Stderr = m.job.stderr.String()
	}
	if cp.Running {
		cp.Ms = time.Since(m.job.start).Milliseconds()
	}
	return &cp
}

// Start dispatches an instruction anchored to abs:l1-l2. It returns as soon as
// the harness is running.
func (m *agentManager) Start(abs, rel string, l1, l2 int, instruction string, force bool) (*agentJob, error) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return nil, errors.New("instruction is empty")
	}

	m.mu.Lock()
	if m.args == nil {
		m.mu.Unlock()
		uiStatus("err", "agent: edit dispatch refused", "no coding harness selected", 0, os.Stdout)
		return nil, errAgentNone
	}
	if m.job != nil && m.job.Running {
		m.mu.Unlock()
		uiStatus("warn", "agent: edit dispatch refused", "an edit is already running", 0, os.Stdout)
		return nil, errAgentBusy
	}
	args := m.args
	name := m.selected
	m.mu.Unlock()

	// The harness rewrites the file in place. px0 can undo the last edit, but
	// only that one: a later edit replaces the copy, and then uncommitted work
	// is out of reach. That needs saying once before it happens.
	if !force && gitAvailable(m.root) {
		if st := gitStatus(m.root); st != nil {
			if _, dirty := st[rel]; dirty {
				uiStatus("warn", fmt.Sprintf("agent: edit refused on uncommitted file %s", rel), "use force to override", 0, os.Stdout)
				return nil, fmt.Errorf("%s has %w changes that this edit would write over", rel, errAgentDirty)
			}
		}
	}

	snippet, err := readLineRange(abs, l1, l2)
	if err != nil {
		uiStatus("err", fmt.Sprintf("agent: failed reading snippet for %s:%s", rel, lineRef(l1, l2)), err.Error(), 0, os.Stdout)
		return nil, err
	}

	m.mu.Lock()
	m.seq++
	m.undo = nil // a new run makes the previous plan unsafe to apply
	job := &agentJob{
		ID:      m.seq,
		Harness: name,
		Path:    rel,
		Lines:   lineRef(l1, l2),
		Running: true,
		Changed: []string{},
		Tracked: gitAvailable(m.root),
		out:     &tailBuffer{max: agentLogBytes},
		stderr:  &tailBuffer{max: agentLogBytes},
		start:   time.Now(),
	}
	m.job = job
	m.mu.Unlock()

	uiStatus("step", fmt.Sprintf("agent: dispatching edit with %s", name), fmt.Sprintf("%s:%s %q", rel, lineRef(l1, l2), instruction), 0, os.Stdout)
	go m.run(job, args, agentPrompt(rel, l1, l2, snippet, instruction))
	return m.Job(), nil
}

func (m *agentManager) run(job *agentJob, template []string, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	defer cancel()

	pre := capturePreEdit(m.root)

	args := make([]string, len(template))
	for i, tok := range template {
		args[i] = strings.ReplaceAll(tok, "{prompt}", prompt)
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = m.root
	cmd.Stdout = job.out
	cmd.Stderr = job.stderr
	// stdin stays empty: a harness that still wants to ask something fails
	// fast instead of hanging until the timeout with nothing on screen.

	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("gave up after %s", agentTimeout)
	}

	changed := changedSince(m.root, pre.status)
	m.settle(changed)
	// Planned even for a failed run: whatever it wrote before failing still counts.
	var undo *agentUndo
	undoNote := ""
	if len(changed) > 0 {
		undo, undoNote = planUndo(m.root, pre, changed)
	}

	m.mu.Lock()
	job.Running = false
	job.Changed = changed
	job.Ms = time.Since(job.start).Milliseconds()
	m.undo = undo
	job.Undoable = undo != nil
	job.UndoNote = undoNote
	if err != nil {
		job.Error = err.Error()
	}
	stdoutOutput := job.out.String()
	stderrOutput := job.stderr.String()
	m.mu.Unlock()

	if err != nil {
		uiStatus("err", fmt.Sprintf("agent: harness %s failed (%dms)", job.Harness, job.Ms), err.Error(), 0, os.Stdout)
		if trimmedErr := strings.TrimSpace(stderrOutput); trimmedErr != "" {
			fmt.Fprintf(os.Stdout, "  %s %s\n", uiDim("harness stderr:", os.Stdout), trimmedErr)
		}
		if trimmedOut := strings.TrimSpace(stdoutOutput); trimmedOut != "" {
			fmt.Fprintf(os.Stdout, "  %s %s\n", uiDim("harness stdout:", os.Stdout), trimmedOut)
		}
	} else {
		summary := fmt.Sprintf("%d file(s) changed", len(changed))
		if len(changed) > 0 {
			summary += ": " + strings.Join(changed, ", ")
		}
		uiStatus("ok", fmt.Sprintf("agent: harness %s finished (%dms)", job.Harness, job.Ms), summary, 0, os.Stdout)
	}
}

// settle drops every trace of the old bytes. Open memoises on path+mtime+size
// so a rewritten file already misses the cache, but a harness that truncates
// and writes in place can be read mid-write, and that torn copy would then sit
// under a key nothing invalidates. Evicting is cheaper than reasoning about it.
// Language servers hold their own copy of the file and never saw the write, so
// they are closed here too and reopen on the next request.
func (m *agentManager) settle(changed []string) {
	for _, rel := range changed {
		abs := filepath.Join(m.root, filepath.FromSlash(rel))
		Evict(abs)
		if m.lsp != nil {
			m.lsp.CloseDoc(abs, rel)
		}
	}
}

// Cancel stops a running harness. Whatever it has already written stays.
func (m *agentManager) Cancel() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil || !m.job.Running || m.cancel == nil {
		return false
	}
	uiStatus("warn", fmt.Sprintf("agent: cancelling in-flight run with %s", m.job.Harness), fmt.Sprintf("job %d", m.job.ID), 0, os.Stdout)
	m.cancel()
	return true
}

func (m *agentManager) Close() { m.Cancel() }

// changedSince reports the paths whose state differs from the snapshot taken
// before the run. Asking git is the only honest answer to "what did it touch":
// a harness routinely edits files nobody pointed it at.
func changedSince(root string, before map[string]string) []string {
	return changedSinceMaps(before, worktreeSnapshot(root))
}

// worktreeSnapshot is git status with each listed file's size and mtime folded
// into its entry. Status alone misses the common case of editing a file that is
// already modified: it reads "M" before and after, so the edit would go unseen.
// Files git lists as clean are left out, and those still surface through status.
func worktreeSnapshot(root string) map[string]string {
	st := gitStatus(root)
	for rel, code := range st {
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			st[rel] = code + " " + strconv.FormatInt(fi.Size(), 10) + " " + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
		}
	}
	return st
}

// changedSinceMaps compares two status snapshots in both directions. Outside a
// git repository both are nil and nothing is ever reported as changed, which is
// why a job carries Tracked for the client to fall back on.
func changedSinceMaps(before, after map[string]string) []string {
	out := []string{}
	for path, st := range after {
		if before[path] != st {
			out = append(out, path)
		}
	}
	// A file restored to its committed state leaves the status list entirely.
	for path := range before {
		if _, still := after[path]; !still {
			out = append(out, path)
		}
	}
	return out
}

func lineRef(l1, l2 int) string {
	if l1 == l2 {
		return strconv.Itoa(l1)
	}
	return strconv.Itoa(l1) + "-" + strconv.Itoa(l2)
}

// readLineRange returns lines l1..l2 of a file, 1-based and inclusive.
func readLineRange(abs string, l1, l2 int) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if l1 < 1 {
		l1 = 1
	}
	if l2 < l1 {
		l2 = l1
	}
	if l1 > len(lines) {
		return "", fmt.Errorf("line %d is past the end of %s", l1, filepath.Base(abs))
	}
	if l2 > len(lines) {
		l2 = len(lines)
	}
	return strings.Join(lines[l1-1:l2], "\n"), nil
}

// agentPrompt composes what the harness is told. It deliberately matches the
// shape of the Copy for Agent snippet in web/src/selbar.js, which these tools
// already read well.
func agentPrompt(rel string, l1, l2 int, snippet, instruction string) string {
	ext := strings.TrimPrefix(filepath.Ext(rel), ".")
	var b strings.Builder
	fmt.Fprintf(&b, "### Reference: %s:%s\n```%s\n%s\n```\n\n", rel, lineRef(l1, l2), ext, snippet)
	fmt.Fprintf(&b, "### Instruction\n%s\n\n", instruction)
	b.WriteString("Edit the file in place to carry out that instruction. ")
	b.WriteString("Change only what it asks for, and do not explain the change afterwards.")
	return b.String()
}

// ---------------------------------------------------------------- HTTP

func (s *Server) agentOrFail(w http.ResponseWriter) bool {
	if s.agent == nil {
		fail(w, http.StatusNotFound, "editing is not available in this session")
		return false
	}
	return true
}

// handleAgentHarnesses backs the picker. It re-scans on every call so a harness
// installed since startup appears without a restart.
func (s *Server) handleAgentHarnesses(w http.ResponseWriter, r *http.Request) {
	if !s.agentOrFail(w) {
		return
	}
	writeJSON(w, map[string]any{
		"harnesses": s.agent.Detect(),
		"selected":  s.agent.Name(),
		"pinned":    s.agent.Pinned(),
		"settings":  settingsPath(),
	})
}

func (s *Server) handleAgentSelect(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	if err := s.agent.Select(r.URL.Query().Get("name")); err != nil {
		code := 400
		if errors.Is(err, errAgentBusy) {
			code = http.StatusConflict
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"harnesses": s.agent.Detect(),
		"selected":  s.agent.Name(),
		"pinned":    s.agent.Pinned(),
		"settings":  settingsPath(),
	})
}

func (s *Server) handleAgentEdit(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	q := r.URL.Query()
	abs, rel, ok := s.resolvePath(q.Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	l1, _ := strconv.Atoi(q.Get("l1"))
	l2, _ := strconv.Atoi(q.Get("l2"))

	job, err := s.agent.Start(abs, rel, l1, l2, q.Get("instruction"), q.Get("force") == "1")
	if err != nil {
		code := 400
		if errors.Is(err, errAgentBusy) || errors.Is(err, errAgentDirty) {
			code = http.StatusConflict
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, job)
}

// handleAgentJob is polled while an edit runs. px0 dispatched the harness, so
// it knows when the work ended without watching the filesystem for it.
func (s *Server) handleAgentJob(w http.ResponseWriter, r *http.Request) {
	if !s.agentOrFail(w) {
		return
	}
	j := s.agent.Job()
	if j == nil {
		writeJSON(w, map[string]any{"idle": true})
		return
	}
	writeJSON(w, j)
}

func (s *Server) handleAgentCancel(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	writeJSON(w, map[string]any{"cancelled": s.agent.Cancel()})
}
