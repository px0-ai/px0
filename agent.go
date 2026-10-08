package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/px0-ai/harness"
)

// Editing through a coding harness. px0 never authors a change itself: it
// composes an instruction anchored to a line range, hands it to a harness
// already installed on this machine, and reloads whatever moved once that
// harness exits. The harness edits; px0 stays the reader that knows exactly
// when to look again.
//
// Harnesses are discovered and executed using github.com/px0-ai/harness.
// Discovery alone never enables editing: running a general-purpose agent
// over a workspace is a decision the user makes once, and it is remembered
// in the settings file rather than a flag.

const (
	agentTimeout  = harness.DefaultTimeout
	agentLogBytes = harness.DefaultLogBytes
)

// agentHarness is one row of the picker.
type agentHarness struct {
	Name      string   `json:"name"`
	Cmd       string   `json:"cmd"`
	Installed bool     `json:"installed"`
	Path      string   `json:"path,omitempty"`
	Models    []string `json:"models,omitempty"`
	Model     string   `json:"model,omitempty"`
}

// agentRange anchors a range on a file for overlap checks.
type agentRange struct {
	path string
	l1   int
	l2   int
}

// agentBatchItem represents one instruction anchored to a file range.
type agentBatchItem struct {
	Abs         string `json:"-"`
	Path        string `json:"path"`
	L1          int    `json:"l1"`
	L2          int    `json:"l2"`
	Instruction string `json:"instruction"`
}

// agentJob represents a single background editing task dispatched to an AI coding harness.
// It tracks real-time progress, log outputs, duration, affected files, and cancellation handlers.
type agentJob struct {
	ID         int64            `json:"id"`                   // Unique monotonic job identifier
	Harness    string           `json:"harness"`              // Name of the harness executing this job
	Path       string           `json:"path"`                 // Relative file path targeted for editing
	Lines      string           `json:"lines"`                // Line range formatted string (e.g. "L12-L30")
	Running    bool             `json:"running"`              // True while harness process is actively executing
	Error      string           `json:"error,omitempty"`      // Error message if the job failed or was aborted
	Log        string           `json:"log"`                  // Tail of merged stdout/stderr log output
	Stdout     string           `json:"stdout,omitempty"`     // Stdout log output tail
	Stderr     string           `json:"stderr,omitempty"`     // Stderr log output tail
	Changed    []string         `json:"changed"`              // Files detected as modified after job execution
	Ms         int64            `json:"ms"`                   // Elapsed runtime in milliseconds
	Tracked    bool             `json:"tracked"`              // Whether telemetry tracking has been recorded
	ThreadID   string           `json:"threadId,omitempty"`   // The thread this edit runs as, when it runs as one
	BatchCount int              `json:"batchCount,omitempty"` // Number of items in batch review edit
	Items      []agentBatchItem `json:"items,omitempty"`      // Detailed batch items if multi-file edit

	ranges []agentRange
	l1, l2 int
	cancel context.CancelFunc
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

// lineStreamer forwards complete lines to w with a prefix in real time,
// while also storing the raw bytes into a tailBuffer.
type lineStreamer struct {
	mu     sync.Mutex
	buf    *tailBuffer
	prefix string
	line   []byte
	w      io.Writer
}

func newLineStreamer(buf *tailBuffer, prefix string, w io.Writer) *lineStreamer {
	return &lineStreamer{buf: buf, prefix: prefix, w: w}
}

func (s *lineStreamer) Write(p []byte) (int, error) {
	if s.buf != nil {
		s.buf.Write(p)
	}
	if s.w == nil || uiQuiet {
		return len(p), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			if len(s.line) > 0 {
				fmt.Fprintf(s.w, "  %s %s\n", s.prefix, string(s.line))
				s.line = s.line[:0]
			}
		} else if b != '\r' {
			s.line = append(s.line, b)
		}
	}
	return len(p), nil
}

func (s *lineStreamer) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.line) > 0 && s.w != nil && !uiQuiet {
		fmt.Fprintf(s.w, "  %s %s\n", s.prefix, string(s.line))
		s.line = s.line[:0]
	}
}

// agentManager owns discovery, the current choice, and every edit currently in
// flight. Several harnesses can run at once as long as they touch disjoint
// line ranges: two harnesses rewriting the same lines produces a state nobody
// can review afterwards, so overlapping ranges are refused rather than queued.
type agentManager struct {
	root string
	lsp  *lspManager

	mu       sync.Mutex
	selected string            // preset name, or the template itself when pinned
	args     []string          // resolved argv, nil when nothing is selected
	pinned   bool              // -agent was given, so the UI cannot change it
	models   map[string]string // harness name -> selected model
	jobs     map[int64]*agentJob
	seq      int64
	onEdit   func()

	// Set by the thread manager: inline and batch edits run as threads.
	threadJob    func(id int64) *agentJob // id 0 means the most recent
	threadCancel func(id int64) bool      // id 0 means every one running
}

// newAgentManager wires discovery and restores the remembered choice. A flag
// value pins a harness (or an arbitrary command template) for this run and is
// the only case that can fail: a bad -agent should stop startup, whereas a
// stale settings file should just leave nothing selected.
func newAgentManager(root, flagSpec string, lsp *lspManager) (*agentManager, error) {
	m := &agentManager{
		root:   root,
		lsp:    lsp,
		models: map[string]string{},
	}
	s := readSettings()
	if s.Models != nil {
		for k, v := range s.Models {
			m.models[k] = v
		}
	}

	if spec := strings.TrimSpace(flagSpec); spec != "" {
		name, args, chosenModel, err := resolveAgentSpec(spec, m.models[spec])
		if err != nil {
			return nil, err
		}
		m.selected, m.args, m.pinned = name, args, true
		if chosenModel != "" {
			m.models[name] = chosenModel
		}
		return m, nil
	}

	if s.Agent != "" {
		if name, args, chosenModel, err := resolveAgentSpec(s.Agent, m.models[s.Agent]); err == nil {
			m.selected, m.args = name, args
			if chosenModel != "" {
				m.models[name] = chosenModel
			}
		}
	}
	return m, nil
}

// resolveAgentSpec turns a preset name or a command template into argv, and
// verifies the binary exists now rather than at first use.
func resolveAgentSpec(spec, model string) (string, []string, string, error) {
	cmd, err := harness.Resolve(spec, model)
	if err != nil {
		return "", nil, "", err
	}
	return cmd.Name, cmd.Args, cmd.Model, nil
}

func agentPresetNames() []string {
	return harness.PresetNames()
}

// Detect reports every harness known and whether it is installed right
// now, so a tool installed since startup shows up without a restart.
// Models are only discovered for the currently selected harness to avoid
// executing external commands (such as `agy models`) on startup for unused tools.
func (m *agentManager) Detect() []agentHarness {
	m.mu.Lock()
	savedModels := make(map[string]string, len(m.models))
	for k, v := range m.models {
		savedModels[k] = v
	}
	selected := m.selected
	m.mu.Unlock()

	detected := harness.Detect(savedModels)
	out := make([]agentHarness, 0, len(detected))
	for _, h := range detected {
		var models []string
		if h.Installed && h.Name == selected {
			if list, err := harness.DiscoverModels(context.Background(), h.Name); err == nil {
				models = list
			}
		}
		curModel := h.Model
		if curModel == "" && len(models) > 0 {
			curModel = models[0]
		}
		out = append(out, agentHarness{
			Name:      h.Name,
			Cmd:       h.Cmd,
			Installed: h.Installed,
			Path:      h.Path,
			Models:    models,
			Model:     curModel,
		})
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

func (m *agentManager) Model() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.selected == "" {
		return ""
	}
	return m.models[m.selected]
}

// current returns the selected harness, its headless argv and its model, read
// together so a run never mixes one harness's name with another's flags.
func (m *agentManager) current() (string, []string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.args == nil {
		return "", nil, ""
	}
	return m.selected, append([]string(nil), m.args...), m.models[m.selected]
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
func (m *agentManager) Select(name string, modelOpt ...string) error {
	m.mu.Lock()
	if m.pinned {
		m.mu.Unlock()
		return errors.New("px0 was started with -agent, so the harness is fixed for this run")
	}
	m.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		m.mu.Lock()
		m.selected, m.args = "", nil
		m.mu.Unlock()
		return writeSettings(settings{})
	}

	reqModel := ""
	if len(modelOpt) > 0 {
		reqModel = strings.TrimSpace(modelOpt[0])
	}
	m.mu.Lock()
	if reqModel == "" {
		reqModel = m.models[name]
	}
	m.mu.Unlock()

	display, args, chosenModel, err := resolveAgentSpec(name, reqModel)
	if err != nil {
		uiStatus("err", fmt.Sprintf("agent: failed to select harness %q", name), err.Error(), 0, os.Stdout)
		return err
	}
	m.mu.Lock()
	m.selected, m.args = display, args
	if m.models == nil {
		m.models = map[string]string{}
	}
	if chosenModel != "" {
		m.models[display] = chosenModel
	}
	savedModels := make(map[string]string, len(m.models))
	for k, v := range m.models {
		savedModels[k] = v
	}
	m.mu.Unlock()

	modelNote := ""
	if chosenModel != "" {
		modelNote = fmt.Sprintf(" (%s)", chosenModel)
	}
	uiStatus("ok", "agent", fmt.Sprintf("%s%s", display, modelNote), 0, os.Stdout)
	// Persist the spec as given, not the display name: a command template
	// shortens to its binary for display and would not survive the round trip.
	return writeSettings(settings{Agent: name, Models: savedModels})
}

// Job returns a snapshot of job id, or of the most recently started job when
// id is 0, or nil when there isn't one.
func (m *agentManager) Job(id int64) *agentJob {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	j := m.jobs[id]
	if id == 0 {
		for _, cand := range m.jobs {
			if j == nil || cand.ID > j.ID {
				j = cand
			}
		}
	}
	var cp *agentJob
	if j != nil {
		c := *j
		c.Log = j.out.String()
		c.Stdout = c.Log
		if j.stderr != nil {
			c.Stderr = j.stderr.String()
		}
		if c.Running {
			c.Ms = time.Since(j.start).Milliseconds()
		}
		cp = &c
	}
	lookup := m.threadJob
	m.mu.Unlock()

	// Inline and batch edits run as threads and share this id space, so one
	// poll endpoint serves both. Asked for the latest, the newer of the two wins.
	if lookup != nil {
		if tj := lookup(id); tj != nil && (cp == nil || tj.ID > cp.ID) {
			return tj
		}
	}
	return cp
}

// nextJobID hands out an id from the sequence every job shares.
func (m *agentManager) nextJobID() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return m.seq
}

// anyRunningLocked reports whether any job is still in flight. Callers hold m.mu.
func (m *agentManager) anyRunningLocked() bool {
	for _, j := range m.jobs {
		if j.Running {
			return true
		}
	}
	return false
}

// overlapLocked reports whether a running job already touches rel within
// [l1,l2]. Different paths, or disjoint ranges on the same path, are free to
// run at the same time. Callers hold m.mu.
// findOverlappingJobLocked returns the running job that touches rel within [l1, l2],
// or nil if none overlaps. Callers hold m.mu.
func (m *agentManager) findOverlappingJobLocked(rel string, l1, l2 int) *agentJob {
	for _, j := range m.jobs {
		if !j.Running {
			continue
		}
		if len(j.ranges) > 0 {
			for _, r := range j.ranges {
				if r.path == rel && l1 <= r.l2 && r.l1 <= l2 {
					return j
				}
			}
		} else if j.Path == rel && l1 <= j.l2 && j.l1 <= l2 {
			return j
		}
	}
	return nil
}

// overlapLocked reports whether a running job already touches rel within
// [l1,l2]. Different paths, or disjoint ranges on the same path, are free to
// run at the same time. Callers hold m.mu.
func (m *agentManager) overlapLocked(rel string, l1, l2 int) bool {
	return m.findOverlappingJobLocked(rel, l1, l2) != nil
}

// Start dispatches an instruction anchored to abs:l1-l2. It returns as soon as
// the harness is running.
func (m *agentManager) Start(abs, rel string, l1, l2 int, instruction string, force bool) (*agentJob, error) {
	return m.StartBatch([]agentBatchItem{{
		Abs:         abs,
		Path:        rel,
		L1:          l1,
		L2:          l2,
		Instruction: instruction,
	}}, force)
}

// StartBatch dispatches a batch of instructions anchored to one or more file ranges.
func (m *agentManager) StartBatch(items []agentBatchItem, force bool) (*agentJob, error) {
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
				uiStatus("warn", "agent", fmt.Sprintf("batch edit refused: overlapping edits on %s (%s and %s)", items[i].Path, lineRef(items[i].L1, items[i].L2), lineRef(items[j].L1, items[j].L2)), 0, os.Stdout)
				return nil, fmt.Errorf("overlapping edits in batch on %s (%s and %s)", items[i].Path, lineRef(items[i].L1, items[i].L2), lineRef(items[j].L1, items[j].L2))
			}
		}
	}

	m.mu.Lock()
	if m.args == nil {
		m.mu.Unlock()
		uiStatus("err", "agent", "edit dispatch refused: no coding harness selected", 0, os.Stdout)
		return nil, errAgentNone
	}
	for _, it := range items {
		if blocking := m.findOverlappingJobLocked(it.Path, it.L1, it.L2); blocking != nil {
			m.mu.Unlock()
			loc := fmt.Sprintf("%s:%s", it.Path, lineRef(it.L1, it.L2))
			msg := fmt.Sprintf("an edit is already running on %s (job #%d with %s)", loc, blocking.ID, blocking.Harness)
			uiStatus("warn", "agent", "edit dispatch refused: "+msg, 0, os.Stdout)
			return nil, fmt.Errorf("%w: %s", errAgentBusy, msg)
		}
	}
	args := m.args
	name := m.selected
	m.mu.Unlock()

	prepared := make([]itemWithSnippet, len(items))
	for i, it := range items {
		snippet, err := readLineRange(it.Abs, it.L1, it.L2)
		if err != nil {
			uiStatus("err", "agent", fmt.Sprintf("failed reading snippet for %s:%s: %s", it.Path, lineRef(it.L1, it.L2), err.Error()), 0, os.Stdout)
			return nil, err
		}
		prepared[i] = itemWithSnippet{item: it, snippet: snippet}
	}

	m.mu.Lock()
	// Re-check under lock: another dispatch may have raced between the check
	// above and here, while this one was reading the file and git status.
	for _, it := range items {
		if blocking := m.findOverlappingJobLocked(it.Path, it.L1, it.L2); blocking != nil {
			m.mu.Unlock()
			loc := fmt.Sprintf("%s:%s", it.Path, lineRef(it.L1, it.L2))
			msg := fmt.Sprintf("an edit is already running on %s (job #%d with %s)", loc, blocking.ID, blocking.Harness)
			uiStatus("warn", "agent", "edit dispatch refused: "+msg, 0, os.Stdout)
			return nil, fmt.Errorf("%w: %s", errAgentBusy, msg)
		}
	}
	m.seq++
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)

	ranges := make([]agentRange, len(items))
	for i, it := range items {
		ranges[i] = agentRange{path: it.Path, l1: it.L1, l2: it.L2}
	}

	allSameFile := true
	for _, it := range items {
		if it.Path != items[0].Path {
			allSameFile = false
			break
		}
	}

	var displayPath, displayLines string
	if allSameFile {
		displayPath = items[0].Path
		if len(items) == 1 {
			displayLines = lineRef(items[0].L1, items[0].L2)
		} else {
			displayLines = fmt.Sprintf("%d edits", len(items))
		}
	} else {
		uniqueFiles := make(map[string]bool)
		for _, it := range items {
			uniqueFiles[it.Path] = true
		}
		displayPath = fmt.Sprintf("%d files", len(uniqueFiles))
		displayLines = fmt.Sprintf("%d edits", len(items))
	}

	job := &agentJob{
		ID:         m.seq,
		Harness:    name,
		Path:       displayPath,
		Lines:      displayLines,
		Running:    true,
		Changed:    []string{},
		Tracked:    gitAvailable(m.root),
		BatchCount: len(items),
		Items:      items,
		ranges:     ranges,
		l1:         items[0].L1,
		l2:         items[0].L2,
		out:        &tailBuffer{max: agentLogBytes},
		stderr:     &tailBuffer{max: agentLogBytes},
		start:      time.Now(),
		cancel:     cancel,
	}
	if m.jobs == nil {
		m.jobs = map[int64]*agentJob{}
	}
	m.jobs[job.ID] = job
	modelStr := ""
	if m.models != nil && m.models[name] != "" {
		modelStr = fmt.Sprintf(" (%s)", m.models[name])
	}
	m.mu.Unlock()

	if len(items) == 1 {
		uiStatus("step", "agent", fmt.Sprintf("#%d %s%s · %s:%s  %q", job.ID, name, modelStr, items[0].Path, lineRef(items[0].L1, items[0].L2), items[0].Instruction), 0, os.Stdout)
	} else {
		uiStatus("step", "agent", fmt.Sprintf("#%d %s%s · batch %d edits across %s", job.ID, name, modelStr, len(items), displayPath), 0, os.Stdout)
	}

	var prompt string
	if len(items) == 1 {
		prompt = agentPrompt(items[0].Path, items[0].L1, items[0].L2, prepared[0].snippet, items[0].Instruction)
	} else {
		prompt = agentBatchPrompt(prepared)
	}

	go m.run(ctx, cancel, job, args, prompt)
	return m.Job(job.ID), nil
}

// StartPrompt dispatches a one-shot prompt to the selected harness with no
// target file -- used for generating text (e.g. a commit message) rather
// than editing code. There is no file range to anchor an overlap check
// against, so a prompt job is never blocked by, or blocks, a file edit.
func (m *agentManager) StartPrompt(label, prompt string) (*agentJob, error) {
	m.mu.Lock()
	if m.args == nil {
		m.mu.Unlock()
		uiStatus("err", "agent", "prompt dispatch refused: no coding harness selected", 0, os.Stdout)
		return nil, errAgentNone
	}
	args := m.args
	name := m.selected
	m.seq++
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	job := &agentJob{
		ID:      m.seq,
		Harness: name,
		Path:    label,
		Running: true,
		Changed: []string{},
		Tracked: gitAvailable(m.root),
		out:     &tailBuffer{max: agentLogBytes},
		stderr:  &tailBuffer{max: agentLogBytes},
		start:   time.Now(),
		cancel:  cancel,
	}
	if m.jobs == nil {
		m.jobs = map[int64]*agentJob{}
	}
	m.jobs[job.ID] = job
	modelStr := ""
	if m.models != nil && m.models[name] != "" {
		modelStr = fmt.Sprintf(" (%s)", m.models[name])
	}
	m.mu.Unlock()

	uiStatus("step", "agent", fmt.Sprintf("#%d %s%s · %s", job.ID, name, modelStr, label), 0, os.Stdout)

	go m.run(ctx, cancel, job, args, prompt)
	return m.Job(job.ID), nil
}

func (m *agentManager) run(ctx context.Context, cancel context.CancelFunc, job *agentJob, template []string, prompt string) {
	defer cancel()
	defer func() {
		m.mu.Lock()
		job.Running = false
		job.Ms = time.Since(job.start).Milliseconds()
		if job.cancel != nil {
			job.cancel = nil
		}
		m.mu.Unlock()
	}()

	if uiVerbose {
		uiVerbosePrompt(job.ID, job.Harness, prompt, os.Stdout)
	}

	before := worktreeSnapshot(m.root)

	stdoutStreamer := newLineStreamer(job.out, uiFaint("│", os.Stdout), os.Stdout)
	stderrStreamer := newLineStreamer(job.stderr, uiDim("│", os.Stdout), os.Stdout)

	res := harness.Run(ctx, harness.Command{Name: job.Harness, Args: template}, prompt, harness.Options{
		Dir:      m.root,
		Timeout:  agentTimeout,
		LogBytes: agentLogBytes,
		Stdout:   stdoutStreamer,
		Stderr:   stderrStreamer,
	})
	stdoutStreamer.Flush()
	stderrStreamer.Flush()
	err := res.Err

	changed := changedSince(m.root, before)
	m.settle(changed)

	m.mu.Lock()
	job.Running = false
	job.Changed = changed
	job.Ms = res.Duration.Milliseconds()
	if err != nil {
		job.Error = err.Error()
	}
	stdoutOutput := job.out.String()
	stderrOutput := job.stderr.String()
	m.mu.Unlock()

	durStr := fmtDuration(time.Duration(job.Ms) * time.Millisecond)
	if err != nil {
		uiStatus("err", "agent", fmt.Sprintf("#%d %s failed in %s: %s", job.ID, job.Harness, durStr, err.Error()), 0, os.Stdout)
		if trimmedErr := strings.TrimSpace(stderrOutput); trimmedErr != "" {
			uiKV("harness stderr", trimmedErr, 0, os.Stdout)
		}
		if trimmedOut := strings.TrimSpace(stdoutOutput); trimmedOut != "" {
			uiKV("harness stdout", trimmedOut, 0, os.Stdout)
		}
	} else {
		var summary string
		switch len(changed) {
		case 0:
			summary = "no files changed"
		case 1:
			summary = fmt.Sprintf("1 file changed: %s", changed[0])
		default:
			summary = fmt.Sprintf("%d files changed: %s", len(changed), strings.Join(changed, ", "))
		}
		uiStatus("ok", "agent", fmt.Sprintf("#%d %s · %s  (%s)", job.ID, job.Harness, durStr, summary), 0, os.Stdout)
		if len(changed) == 0 && job.Tracked {
			if trimmedErr := strings.TrimSpace(stderrOutput); trimmedErr != "" {
				uiKV("harness stderr", trimmedErr, 0, os.Stdout)
			}
		}
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
	if len(changed) > 0 && m.onEdit != nil {
		m.onEdit()
	}
}

// Cancel stops every harness currently running. Whatever each has already
// written stays.
func (m *agentManager) Cancel() bool {
	return m.CancelJob(0)
}

// CancelJob stops the harness run with the given id, or every running harness
// when id is 0.
func (m *agentManager) CancelJob(id int64) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	tc := m.threadCancel
	m.mu.Unlock()
	fromThreads := tc != nil && tc(id)
	if fromThreads && id != 0 {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != 0 {
		j := m.jobs[id]
		if j != nil && j.Running && j.cancel != nil {
			uiStatus("warn", "agent", fmt.Sprintf("cancelled in-flight run with %s (job %d)", j.Harness, j.ID), 0, os.Stdout)
			j.Running = false
			j.Error = "cancelled"
			cancel := j.cancel
			j.cancel = nil
			cancel()
			return true
		}
		return false
	}
	cancelled := false
	for _, j := range m.jobs {
		if !j.Running || j.cancel == nil {
			continue
		}
		uiStatus("warn", "agent", fmt.Sprintf("cancelled in-flight run with %s (job %d)", j.Harness, j.ID), 0, os.Stdout)
		j.Running = false
		j.Error = "cancelled"
		cancel := j.cancel
		j.cancel = nil
		cancel()
		cancelled = true
	}
	return cancelled || fromThreads
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
		if stamp := fileStamp(root, rel); stamp != "" {
			st[rel] = code + " " + stamp
		}
	}
	return st
}

// fileStamp is a file's size and mtime, or "" when it can't be stat'd (a
// deleted file, say). An edit changes it even when the status letter doesn't.
func fileStamp(root, rel string) string {
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	return strconv.FormatInt(fi.Size(), 10) + " " + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

// fileStamps is fileStamp for every path git status listed.
func fileStamps(root string, status map[string]string) map[string]string {
	out := make(map[string]string, len(status))
	for rel := range status {
		out[rel] = fileStamp(root, rel)
	}
	return out
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
	// Map iteration order is random; sort so the job's summary and API
	// response list the same files in the same order on every run.
	sort.Strings(out)
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
	lineStr := fmt.Sprintf("lines %d-%d", l1, l2)
	if l1 == l2 {
		lineStr = fmt.Sprintf("line %d", l1)
	}
	fmt.Fprintf(&b, "@%s %s\n```%s\n%s\n```\n\n", rel, lineStr, ext, snippet)
	fmt.Fprintf(&b, "### Instruction\n%s\n\n", instruction)
	b.WriteString("Edit the file in place to carry out that instruction. ")
	b.WriteString("Change only what it asks for, and do not explain the change afterwards.")
	return b.String()
}

type itemWithSnippet struct {
	item    agentBatchItem
	snippet string
}

func agentBatchPrompt(items []itemWithSnippet) string {
	var b strings.Builder
	b.WriteString("Batch Edit Request: Carry out all of the following instructions across the workspace.\n\n")
	for i, it := range items {
		ext := strings.TrimPrefix(filepath.Ext(it.item.Path), ".")
		lineStr := fmt.Sprintf("lines %d-%d", it.item.L1, it.item.L2)
		if it.item.L1 == it.item.L2 {
			lineStr = fmt.Sprintf("line %d", it.item.L1)
		}
		fmt.Fprintf(&b, "### Edit %d: @%s %s\n```%s\n%s\n```\n\n", i+1, it.item.Path, lineStr, ext, it.snippet)
		fmt.Fprintf(&b, "**Instruction**: %s\n\n", it.item.Instruction)
	}
	b.WriteString("Edit the file(s) in place to carry out all of the above instructions. ")
	b.WriteString("Change only what they ask for, coordinate changes cleanly, and do not explain the changes afterwards.")
	return b.String()
}

// commitMessagePrompt asks the harness to write a commit message for the
// staged changes. instruction is the user's git.commitMessageInstruction
// setting (empty when unset), appended verbatim so it can refine or override
// the base convention below.
func commitMessagePrompt(files []string, stat, diff, instruction string) string {
	var b strings.Builder
	b.WriteString("Write a git commit message for the staged changes below.\n")
	b.WriteString("Rules: imperative mood, a concise summary line under 72 characters, a blank line before an optional body, and explain why rather than just what changed.\n")
	b.WriteString("Output ONLY the commit message text -- no markdown code fences, no preamble, no explanation afterwards, and do not edit any files.\n")
	if instruction != "" {
		fmt.Fprintf(&b, "\nAdditional instructions from the user: %s\n", instruction)
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "\nChanged files (%d):\n", len(files))
		maxFiles := 100
		for i, f := range files {
			if i >= maxFiles {
				fmt.Fprintf(&b, "... and %d more files\n", len(files)-maxFiles)
				break
			}
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	if strings.TrimSpace(stat) != "" {
		b.WriteString("\nSummary of changes (diffstat):\n")
		b.WriteString(stat)
		b.WriteString("\n")
	}
	if strings.TrimSpace(diff) != "" {
		b.WriteString("\nStaged diff:\n")
		b.WriteString(diff)
		b.WriteString("\n")
	}
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
		"model":     s.agent.Model(),
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
	name := r.URL.Query().Get("name")
	model := r.URL.Query().Get("model")
	if err := s.agent.Select(name, model); err != nil {
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
		"model":     s.agent.Model(),
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
	if q.Get("path") == "" {
		s.handleAgentBatchEdit(w, r)
		return
	}
	abs, rel, ok := s.resolvePath(q.Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	l1, _ := strconv.Atoi(q.Get("l1"))
	l2, _ := strconv.Atoi(q.Get("l2"))

	job, err := s.threads.StartEdit([]agentBatchItem{{Abs: abs, Path: rel, L1: l1, L2: l2, Instruction: q.Get("instruction")}})
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

func (s *Server) handleAgentBatchEdit(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, 400, "failed reading body")
		return
	}

	var req struct {
		Edits []struct {
			Path        string `json:"path"`
			L1          int    `json:"l1"`
			L2          int    `json:"l2"`
			Instruction string `json:"instruction"`
		} `json:"edits"`
		Force bool `json:"force"`
	}

	if err := json.Unmarshal(bodyBytes, &req); err != nil || len(req.Edits) == 0 {
		var list []struct {
			Path        string `json:"path"`
			L1          int    `json:"l1"`
			L2          int    `json:"l2"`
			Instruction string `json:"instruction"`
		}
		var single struct {
			Path        string `json:"path"`
			L1          int    `json:"l1"`
			L2          int    `json:"l2"`
			Instruction string `json:"instruction"`
			Force       bool   `json:"force"`
		}
		q := r.URL.Query()
		if err2 := json.Unmarshal(bodyBytes, &list); err2 == nil && len(list) > 0 {
			req.Edits = list
		} else if err3 := json.Unmarshal(bodyBytes, &single); err3 == nil && single.Path != "" {
			req.Edits = []struct {
				Path        string `json:"path"`
				L1          int    `json:"l1"`
				L2          int    `json:"l2"`
				Instruction string `json:"instruction"`
			}{{Path: single.Path, L1: single.L1, L2: single.L2, Instruction: single.Instruction}}
			if single.Force {
				req.Force = true
			}
		} else if qEdits := q.Get("edits"); qEdits != "" {
			if err4 := json.Unmarshal([]byte(qEdits), &req.Edits); err4 != nil || len(req.Edits) == 0 {
				if err5 := json.Unmarshal([]byte(qEdits), &list); err5 == nil && len(list) > 0 {
					req.Edits = list
				}
			}
		} else if q.Get("path") != "" {
			l1, _ := strconv.Atoi(q.Get("l1"))
			l2, _ := strconv.Atoi(q.Get("l2"))
			req.Edits = []struct {
				Path        string `json:"path"`
				L1          int    `json:"l1"`
				L2          int    `json:"l2"`
				Instruction string `json:"instruction"`
			}{{Path: q.Get("path"), L1: l1, L2: l2, Instruction: q.Get("instruction")}}
			if q.Get("force") == "1" {
				req.Force = true
			}
		}

		if len(req.Edits) == 0 {
			fail(w, 400, "invalid or empty batch edits payload")
			return
		}
	}

	items := make([]agentBatchItem, len(req.Edits))
	for i, e := range req.Edits {
		abs, rel, ok := s.resolvePath(e.Path)
		if !ok {
			fail(w, 400, fmt.Sprintf("bad path: %s", e.Path))
			return
		}
		items[i] = agentBatchItem{
			Abs:         abs,
			Path:        rel,
			L1:          e.L1,
			L2:          e.L2,
			Instruction: e.Instruction,
		}
	}

	job, err := s.threads.StartEdit(items)
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

// handleAgentJob is polled while an edit runs, once per in-flight compose box.
// px0 dispatched the harness, so it knows when the work ended without
// watching the filesystem for it. id=0 (or missing) means the most recently
// started job.
func (s *Server) handleAgentJob(w http.ResponseWriter, r *http.Request) {
	if !s.agentOrFail(w) {
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	j := s.agent.Job(id)
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
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id == 0 {
		var body struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.ID != 0 {
			id = body.ID
		}
	}
	writeJSON(w, map[string]any{"cancelled": s.agent.CancelJob(id)})
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r == '=' || r == ':' || r == ',') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}
