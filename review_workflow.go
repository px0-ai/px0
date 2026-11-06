package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"px0/internal/render"
)

// ReviewWorkflowDef defines a customizable multi-stage review pipeline.
type ReviewWorkflowDef struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Stages      []ReviewStageDef `json:"stages"`
}

// ReviewStageDef represents a single review stage / question in the pipeline.
type ReviewStageDef struct {
	ID                  string `json:"id"`
	Title               string `json:"title"`
	Question            string `json:"question"`
	Prompt              string `json:"prompt"`
	PassPreviousContext bool   `json:"passPreviousContext"`
	Harness             string `json:"harness,omitempty"`
	Model               string `json:"model,omitempty"`
}

// ReviewStageState tracks the live execution of a single stage.
type ReviewStageState struct {
	Def      ReviewStageDef `json:"def"`
	ThreadID string         `json:"threadId,omitempty"`
	Status   string         `json:"status"` // "pending", "running", "completed", "failed", "stopped"
	Error    string         `json:"error,omitempty"`
	Started  int64          `json:"started,omitempty"`
	Finished int64          `json:"finished,omitempty"`
}

// ReviewWorkflowRun represents an active or past execution of a workflow.
type ReviewWorkflowRun struct {
	ID          string              `json:"id"`
	WorkflowID  string              `json:"workflowId"`
	Status      string              `json:"status"` // "idle", "running", "completed", "stopped", "failed"
	ActiveStage int                 `json:"activeStage"`
	Stages      []*ReviewStageState `json:"stages"`
	Created     int64               `json:"created"`
	Updated     int64               `json:"updated"`
}

// ReviewWorkflowConfig holds the review workflow stages and metadata.
type ReviewWorkflowConfig struct {
	Name      string              `json:"name"`
	Stages    []ReviewStageDef    `json:"stages"`
	Workflows []ReviewWorkflowDef `json:"workflows,omitempty"`
	ActiveID  string              `json:"activeId,omitempty"`
}

// defaultWorkflows returns the single default review workflow.
func defaultWorkflows() []ReviewWorkflowDef {
	return []ReviewWorkflowDef{
		{
			ID:          "standard",
			Name:        "PR Review Workflow",
			Description: "Sequential review workflow answering key PR questions: intent, performance, quality, and testing.",
			Stages: []ReviewStageDef{
				{
					ID:                  "summary",
					Title:               "What this PR does",
					Question:            "What does this PR do and what problem does it solve?",
					Prompt:              "Analyze this pull request's diff and description. Provide a concise explanation of what this PR accomplishes, what problem it solves, and list the key components and files changed. Cite file paths and line ranges where relevant.",
					PassPreviousContext: false,
				},
				{
					ID:                  "performance",
					Title:               "Performance Impact",
					Question:            "Are there any performance bottlenecks, lock contentions, or leaks?",
					Prompt:              "Review the changes for performance implications: computational complexity, memory allocations, potential blocking I/O operations, database/network bottlenecks, or mutex/lock contention. Identify any hotspots with exact file:line references.",
					PassPreviousContext: true,
				},
				{
					ID:                  "architecture",
					Title:               "Architecture & Quality",
					Question:            "How does this impact design modularity, edge cases, and maintainability?",
					Prompt:              "Evaluate the software architecture, design modularity, readability, maintainability, and error handling. Note any anti-patterns, edge cases that might fail silently, or breaking API changes.",
					PassPreviousContext: true,
				},
				{
					ID:                  "testing",
					Title:               "Testing & Edge Cases",
					Question:            "Are there missing tests or verification steps?",
					Prompt:              "Check test coverage for the changes. Are new code paths, error states, and edge conditions tested? Suggest specific tests or verification checks if coverage is incomplete.",
					PassPreviousContext: true,
				},
			},
		},
	}
}

// ReviewWorkflowManager coordinates workflow definitions and sequential multi-stage execution.
type ReviewWorkflowManager struct {
	server    *Server
	mu        sync.Mutex
	config    ReviewWorkflowConfig
	run       *ReviewWorkflowRun
	cancelFn  context.CancelFunc
	subs      map[chan reviewWorkflowEvent]struct{}
}

type reviewWorkflowEvent struct {
	Name string `json:"event"`
	Data string `json:"data"`
}

func newReviewWorkflowManager(s *Server) *ReviewWorkflowManager {
	mgr := &ReviewWorkflowManager{
		server: s,
		subs:   make(map[chan reviewWorkflowEvent]struct{}),
	}
	mgr.loadConfig()
	return mgr
}

func (m *ReviewWorkflowManager) configFilePath() string {
	if m.server == nil || m.server.ix == nil {
		return ""
	}
	return filepath.Join(m.server.ix.Root(), ".px0", "workflows.json")
}

func (m *ReviewWorkflowManager) loadConfig() ReviewWorkflowConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	defaults := defaultWorkflows()
	cfg := ReviewWorkflowConfig{
		Name:      defaults[0].Name,
		Stages:    defaults[0].Stages,
		Workflows: defaults,
		ActiveID:  defaults[0].ID,
	}

	p := m.configFilePath()
	if p != "" {
		if data, err := os.ReadFile(p); err == nil {
			var loaded ReviewWorkflowConfig
			if err := json.Unmarshal(data, &loaded); err == nil {
				if len(loaded.Stages) > 0 {
					cfg.Stages = loaded.Stages
					if loaded.Name != "" {
						cfg.Name = loaded.Name
					}
					cfg.Workflows = []ReviewWorkflowDef{{ID: "standard", Name: cfg.Name, Stages: cfg.Stages}}
					cfg.ActiveID = "standard"
				} else if len(loaded.Workflows) > 0 {
					cfg.Workflows = loaded.Workflows
					cfg.Name = loaded.Workflows[0].Name
					cfg.Stages = loaded.Workflows[0].Stages
					cfg.ActiveID = loaded.Workflows[0].ID
				}
			}
		}
	}
	m.config = cfg
	return cfg
}

func (m *ReviewWorkflowManager) saveConfig(cfg ReviewWorkflowConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(cfg.Stages) > 0 {
		if cfg.Name == "" {
			cfg.Name = "PR Review Workflow"
		}
		cfg.Workflows = []ReviewWorkflowDef{
			{
				ID:     "standard",
				Name:   cfg.Name,
				Stages: cfg.Stages,
			},
		}
		cfg.ActiveID = "standard"
	} else if len(cfg.Workflows) > 0 {
		cfg.Name = cfg.Workflows[0].Name
		cfg.Stages = cfg.Workflows[0].Stages
		cfg.ActiveID = cfg.Workflows[0].ID
	} else {
		return errors.New("at least one stage is required in the review workflow")
	}
	m.config = cfg

	// Sync active run's stage definitions if a run exists
	if m.run != nil {
		activeStages := cfg.Stages
		newStages := make([]*ReviewStageState, len(activeStages))
		for i, st := range activeStages {
			found := false
			for _, existing := range m.run.Stages {
				if existing.Def.ID == st.ID {
					existingCopy := *existing
					existingCopy.Def = st
					newStages[i] = &existingCopy
					found = true
					break
				}
			}
			if !found {
				newStages[i] = &ReviewStageState{
					Def:    st,
					Status: "pending",
				}
			}
		}
		m.run.Stages = newStages
		if m.run.ActiveStage >= len(newStages) {
			if len(newStages) > 0 {
				m.run.ActiveStage = len(newStages) - 1
			} else {
				m.run.ActiveStage = 0
			}
		}
		m.broadcastLocked("run_started", m.run)
	}

	p := m.configFilePath()
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0644)
}

func (m *ReviewWorkflowManager) getConfig() ReviewWorkflowConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.config
}

func (m *ReviewWorkflowManager) getRun() *ReviewWorkflowRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run == nil {
		return nil
	}
	// Return a copy
	c := *m.run
	c.Stages = make([]*ReviewStageState, len(m.run.Stages))
	for i, st := range m.run.Stages {
		stCopy := *st
		c.Stages[i] = &stCopy
	}
	return &c
}

func (m *ReviewWorkflowManager) broadcastLocked(name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	ev := reviewWorkflowEvent{Name: name, Data: string(data)}
	for ch := range m.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Run starts executing the chosen workflow sequentially stage by stage.
func (m *ReviewWorkflowManager) Run(workflowID string, startStageIndex int) (*ReviewWorkflowRun, error) {
	m.mu.Lock()

	// Find the workflow definition
	var targetDef *ReviewWorkflowDef
	if len(m.config.Stages) > 0 {
		targetDef = &ReviewWorkflowDef{
			ID:     "standard",
			Name:   m.config.Name,
			Stages: m.config.Stages,
		}
	} else if len(m.config.Workflows) > 0 {
		targetDef = &m.config.Workflows[0]
	} else {
		m.mu.Unlock()
		return nil, errors.New("no stages defined in review workflow")
	}

	// Cancel previous run if still active
	if m.cancelFn != nil {
		m.cancelFn()
		m.cancelFn = nil
	}

	if startStageIndex < 0 || startStageIndex >= len(targetDef.Stages) {
		startStageIndex = 0
	}

	stages := make([]*ReviewStageState, len(targetDef.Stages))
	for i, st := range targetDef.Stages {
		status := "pending"
		var prevThreadID string
		// Preserve previous stages if resuming from a mid-point
		if m.run != nil && m.run.WorkflowID == targetDef.ID && i < len(m.run.Stages) {
			if i < startStageIndex {
				status = m.run.Stages[i].Status
				prevThreadID = m.run.Stages[i].ThreadID
			}
		}
		stages[i] = &ReviewStageState{
			Def:      st,
			Status:   status,
			ThreadID: prevThreadID,
		}
	}

	run := &ReviewWorkflowRun{
		ID:          fmt.Sprintf("wf-%d", time.Now().UnixMilli()),
		WorkflowID:  targetDef.ID,
		Status:      "running",
		ActiveStage: startStageIndex,
		Stages:      stages,
		Created:     time.Now().UnixMilli(),
		Updated:     time.Now().UnixMilli(),
	}
	m.run = run

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFn = cancel

	m.broadcastLocked("run_started", run)
	m.mu.Unlock()

	go m.orchestrate(ctx, run, startStageIndex)

	return m.getRun(), nil
}

// Stop cancels the running stage and pauses the workflow.
func (m *ReviewWorkflowManager) Stop() (*ReviewWorkflowRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.run == nil || m.run.Status != "running" {
		return m.run, nil
	}

	if m.cancelFn != nil {
		m.cancelFn()
		m.cancelFn = nil
	}

	m.run.Status = "stopped"
	m.run.Updated = time.Now().UnixMilli()
	if m.run.ActiveStage >= 0 && m.run.ActiveStage < len(m.run.Stages) {
		curr := m.run.Stages[m.run.ActiveStage]
		if curr.Status == "running" {
			curr.Status = "stopped"
			curr.Finished = time.Now().UnixMilli()
			if curr.ThreadID != "" && m.server != nil && m.server.threads != nil {
				m.server.threads.Cancel(curr.ThreadID)
			}
		}
	}
	m.broadcastLocked("run_stopped", m.run)
	return m.run, nil
}

func (m *ReviewWorkflowManager) orchestrate(ctx context.Context, run *ReviewWorkflowRun, startIdx int) {
	defer func() {
		m.mu.Lock()
		if m.run != nil && m.run.ID == run.ID && m.run.Status == "running" {
			m.run.Status = "completed"
			m.run.Updated = time.Now().UnixMilli()
			m.broadcastLocked("run_completed", m.run)
		}
		m.mu.Unlock()
	}()

	for idx := startIdx; idx < len(run.Stages); idx++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stage := run.Stages[idx]
		m.mu.Lock()
		run.ActiveStage = idx
		stage.Status = "running"
		stage.Started = time.Now().UnixMilli()
		run.Updated = stage.Started
		m.broadcastLocked("stage_started", map[string]any{
			"runId":      run.ID,
			"stageIndex": idx,
			"stage":      stage,
		})
		m.mu.Unlock()

		// Build prompt with context if requested
		fullPrompt := stage.Def.Prompt
		if stage.Def.PassPreviousContext && idx > 0 {
			prevStage := run.Stages[idx-1]
			if prevStage.ThreadID != "" && m.server != nil && m.server.threads != nil {
				if prevT := m.server.threads.Get(prevStage.ThreadID); prevT != nil && len(prevT.Turns) > 0 {
					prevReply := strings.TrimSpace(prevT.Turns[len(prevT.Turns)-1].Reply)
					if len(prevReply) > 1800 {
						prevReply = prevReply[:1800] + "..."
					}
					if prevReply != "" {
						fullPrompt = fmt.Sprintf("Context from previous review stage (%s):\n\"\"\"\n%s\n\"\"\"\n\nReview Question for this stage:\n%s\n\nInstructions:\n%s",
							prevStage.Def.Title, prevReply, stage.Def.Question, stage.Def.Prompt)
					}
				}
			}
		}

		// Determine scope: if PR review session, scope is "pr", else ""
		scope := ""
		if m.server != nil && m.server.pr != nil {
			scope = "pr"
		}

		if m.server == nil || m.server.threads == nil {
			m.mu.Lock()
			stage.Status = "failed"
			stage.Error = "AI thread system not available"
			stage.Finished = time.Now().UnixMilli()
			m.broadcastLocked("stage_failed", stage)
			m.mu.Unlock()
			return
		}

		// Create thread and trigger turn execution
		t, err := m.server.threads.Create("", "", 0, 0, fullPrompt, scope)
		if err != nil {
			m.mu.Lock()
			stage.Status = "failed"
			stage.Error = err.Error()
			stage.Finished = time.Now().UnixMilli()
			m.broadcastLocked("stage_failed", stage)
			m.mu.Unlock()
			return
		}

		m.mu.Lock()
		stage.ThreadID = t.ID
		m.broadcastLocked("stage_updated", map[string]any{
			"runId":      run.ID,
			"stageIndex": idx,
			"stage":      stage,
		})
		m.mu.Unlock()

		// Wait for thread turn to complete
		err = m.waitForThreadTurn(ctx, t.ID)
		if err != nil {
			m.mu.Lock()
			if errors.Is(err, context.Canceled) {
				stage.Status = "stopped"
			} else {
				stage.Status = "failed"
				stage.Error = err.Error()
			}
			stage.Finished = time.Now().UnixMilli()
			m.broadcastLocked("stage_failed", stage)
			m.mu.Unlock()
			return
		}

		m.mu.Lock()
		stage.Status = "completed"
		stage.Finished = time.Now().UnixMilli()
		run.Updated = stage.Finished
		m.broadcastLocked("stage_completed", map[string]any{
			"runId":      run.ID,
			"stageIndex": idx,
			"stage":      stage,
		})
		m.mu.Unlock()

		// Brief pause between stages
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (m *ReviewWorkflowManager) waitForThreadTurn(ctx context.Context, threadID string) error {
	ch, initial, unsub, ok := m.server.threads.subscribe(threadID)
	if !ok {
		return errors.New("failed to subscribe to thread")
	}
	defer unsub()

	// Check initial state
	if initial.name == "thread" {
		var t thread
		if err := json.Unmarshal(initial.data, &t); err == nil {
			if !t.running() {
				return nil
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			m.server.threads.Cancel(threadID)
			return context.Canceled
		case ev, open := <-ch:
			if !open {
				return errors.New("thread event feed closed prematurely")
			}
			if ev.name == "thread" {
				var t thread
				if err := json.Unmarshal(ev.data, &t); err == nil {
					if !t.running() {
						if len(t.Turns) > 0 {
							lastTurn := t.Turns[len(t.Turns)-1]
							if lastTurn.Error != "" && lastTurn.Reply == "" {
								return errors.New(lastTurn.Error)
							}
						}
						return nil
					}
				}
			}
		}
	}
}

func (m *ReviewWorkflowManager) subscribe() (chan reviewWorkflowEvent, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := make(chan reviewWorkflowEvent, 64)
	m.subs[ch] = struct{}{}
	unsub := func() {
		m.mu.Lock()
		delete(m.subs, ch)
		close(ch)
		m.mu.Unlock()
	}
	return ch, unsub
}

// ---------------------------------------------------------------- HTTP Handlers

func (s *Server) handleReviewWorkflowConfig(w http.ResponseWriter, r *http.Request) {
	if s.reviewFlow == nil {
		fail(w, http.StatusServiceUnavailable, "review workflow unavailable")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, s.reviewFlow.getConfig())
		return
	}
	if r.Method == http.MethodPost {
		if !localPost(w, r) {
			return
		}
		var cfg ReviewWorkflowConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			fail(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if err := s.reviewFlow.saveConfig(cfg); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true, "config": s.reviewFlow.getConfig()})
		return
	}
	fail(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *Server) handleReviewWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) || s.reviewFlow == nil {
		return
	}
	var body struct {
		WorkflowID string `json:"workflowId"`
		StageIndex int    `json:"stageIndex"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	run, err := s.reviewFlow.Run(body.WorkflowID, body.StageIndex)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "run": run})
}

func (s *Server) handleReviewWorkflowStop(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) || s.reviewFlow == nil {
		return
	}
	run, err := s.reviewFlow.Stop()
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "run": run})
}

func (s *Server) handleReviewWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	if s.reviewFlow == nil {
		fail(w, http.StatusServiceUnavailable, "review workflow unavailable")
		return
	}
	writeJSON(w, map[string]any{
		"run":    s.reviewFlow.getRun(),
		"config": s.reviewFlow.getConfig(),
	})
}

func (s *Server) handleReviewWorkflowStream(w http.ResponseWriter, r *http.Request) {
	if s.reviewFlow == nil {
		fail(w, http.StatusServiceUnavailable, "review workflow unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch, unsub := s.reviewFlow.subscribe()
	defer unsub()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send initial state snapshot
	currentRun := s.reviewFlow.getRun()
	if runData, err := json.Marshal(currentRun); err == nil {
		fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", runData)
		flusher.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.streamsDone:
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ---------------------------------------------------------------- Review Walkthrough & Understanding

// ReviewWalkthroughFile holds one file's diff hunks and extracted cognitive understanding.
type ReviewWalkthroughFile struct {
	Path          string               `json:"path"`
	Status        string               `json:"status"` // "M", "A", "D"
	Additions     int                  `json:"additions"`
	Deletions     int                  `json:"deletions"`
	Diff          string               `json:"diff"`
	Hunks         []render.DiffHunk    `json:"hunks"`
	Understanding *ReviewUnderstanding `json:"understanding"`
}

// ReviewUnderstanding aggregates fast returns, edge cases, and core logic blocks.
type ReviewUnderstanding struct {
	Summary     string            `json:"summary"`
	FastReturns []CodeInsightItem `json:"fastReturns"`
	EdgeCases   []CodeInsightItem `json:"edgeCases"`
	CoreLogic   []CodeInsightItem `json:"coreLogic"`
}

// CodeInsightItem represents a surfaced guard, edge case, or core code block.
type CodeInsightItem struct {
	Line        int    `json:"line"`
	EndLine     int    `json:"endLine,omitempty"`
	Kind        string `json:"kind"` // "guard", "nil_check", "auth", "validation", "cache", "error", "timeout", "panic", "fallback", "core", "function"
	Text        string `json:"text"`
	Explanation string `json:"explanation"`
}

// ReviewWalkthroughResponse is the response payload for /api/review/walkthrough.
type ReviewWalkthroughResponse struct {
	Files          []ReviewWalkthroughFile `json:"files"`
	TotalFiles     int                     `json:"totalFiles"`
	TotalAdditions int                     `json:"totalAdditions"`
	TotalDeletions int                     `json:"totalDeletions"`
	BaseRef        string                  `json:"baseRef"`
	HeadRef        string                  `json:"headRef"`
	PRMode         bool                    `json:"prMode"`
}

var (
	reReturn     = regexp.MustCompile(`(?i)\b(?:return|continue|break|throw|reject|goto)\b`)
	reNilCheck   = regexp.MustCompile(`(?i)(?:==\s*nil|!=\s*nil|===?\s*null|!==?\s*null|===?\s*undefined|!==?\s*undefined|!\w+)`)
	reAuth       = regexp.MustCompile(`(?i)(?:auth|permission|forbidden|unauthorized|token|credential|denied|role|secret|allow)`)
	reValidation = regexp.MustCompile(`(?i)(?:validate|invalid|required|missing|empty|len\(|<=?\s*0|>=?\s*0|bounds|overflow)`)
	reCache      = regexp.MustCompile(`(?i)(?:cache|hit|cached|memo|idempotent|existing|found)`)
	reError      = regexp.MustCompile(`(?i)(?:err\s*!=\s*nil|errors\.New|fmt\.Errorf|new\s+Error|Exception|failed|failure)`)
	reTimeout    = regexp.MustCompile(`(?i)(?:timeout|timed out|ctx\.Done\(\)|DeadlineExceeded|Canceled|expired)`)
	reFallback   = regexp.MustCompile(`(?i)(?:fallback|default:|recover\(\)|except\s*:|catch\s*\()`)
	rePanic      = regexp.MustCompile(`(?i)(?:panic\(|fatal\(|fatalf\(|assert\()`)
	reFuncDef    = regexp.MustCompile(`(?i)\b(?:func\s+|function\s+|class\s+|def\s+|type\s+\w+\s+(?:struct|interface))\b`)
)

func truncateInsightText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// extractCodeUnderstanding parses diff hunks to surface guard bailouts, edge cases, and core logic.
func extractCodeUnderstanding(rel string, hunks []render.DiffHunk) *ReviewUnderstanding {
	und := &ReviewUnderstanding{
		FastReturns: []CodeInsightItem{},
		EdgeCases:   []CodeInsightItem{},
		CoreLogic:   []CodeInsightItem{},
	}
	if len(hunks) == 0 {
		und.Summary = "No modifications in this file."
		return und
	}

	type rowInfo struct {
		row render.DiffRow
	}
	var addedRows []rowInfo
	for _, h := range hunks {
		for _, r := range h.Rows {
			if r.Type == "add" {
				addedRows = append(addedRows, rowInfo{row: r})
			}
		}
	}

	if len(addedRows) == 0 {
		und.Summary = "Only line deletions or unmodified context in this file."
		return und
	}

	for i := 0; i < len(addedRows); i++ {
		cur := addedRows[i].row
		txt := strings.TrimSpace(cur.Text)
		if txt == "" || strings.HasPrefix(txt, "//") || strings.HasPrefix(txt, "/*") || strings.HasPrefix(txt, "*") {
			continue
		}

		hasReturnInside := reReturn.MatchString(txt)
		blockHasReturn := hasReturnInside
		if !hasReturnInside && (strings.HasPrefix(txt, "if ") || strings.HasPrefix(txt, "if(")) {
			for j := i + 1; j < len(addedRows) && j <= i+3; j++ {
				nextTxt := strings.TrimSpace(addedRows[j].row.Text)
				if reReturn.MatchString(nextTxt) {
					blockHasReturn = true
					break
				}
				if strings.Contains(nextTxt, "}") {
					break
				}
			}
		}

		// 1. Error & failure edge cases
		if reError.MatchString(txt) || (blockHasReturn && strings.Contains(txt, "err")) {
			expl := "Error check and failure propagation"
			if strings.Contains(txt, "fmt.Errorf") || strings.Contains(txt, "errors.New") {
				expl = "Constructs and returns explicit error"
			}
			und.EdgeCases = append(und.EdgeCases, CodeInsightItem{
				Line:        cur.NewLine,
				Kind:        "error",
				Text:        truncateInsightText(txt, 90),
				Explanation: expl,
			})
			continue
		}

		if reTimeout.MatchString(txt) {
			und.EdgeCases = append(und.EdgeCases, CodeInsightItem{
				Line:        cur.NewLine,
				Kind:        "timeout",
				Text:        truncateInsightText(txt, 90),
				Explanation: "Context deadline or timeout handler",
			})
			continue
		}

		if rePanic.MatchString(txt) {
			und.EdgeCases = append(und.EdgeCases, CodeInsightItem{
				Line:        cur.NewLine,
				Kind:        "panic",
				Text:        truncateInsightText(txt, 90),
				Explanation: "Assertion failure or unrecoverable panic exit",
			})
			continue
		}

		if reFallback.MatchString(txt) {
			und.EdgeCases = append(und.EdgeCases, CodeInsightItem{
				Line:        cur.NewLine,
				Kind:        "fallback",
				Text:        truncateInsightText(txt, 90),
				Explanation: "Fallback resilience path on unexpected state",
			})
			continue
		}

		// 2. Fast returns & bailouts
		if blockHasReturn {
			kind := "guard"
			expl := "Early return guard clause"

			if reAuth.MatchString(txt) {
				kind = "auth"
				expl = "Authorization or access check guard"
			} else if reNilCheck.MatchString(txt) {
				kind = "nil_check"
				expl = "Nil / null guard preventing panic on invalid reference"
			} else if reCache.MatchString(txt) {
				kind = "cache"
				expl = "Fast-path cache or idempotent shortcut"
			} else if reValidation.MatchString(txt) {
				kind = "validation"
				expl = "Input boundary or parameter validation bailout"
			}

			und.FastReturns = append(und.FastReturns, CodeInsightItem{
				Line:        cur.NewLine,
				Kind:        kind,
				Text:        truncateInsightText(txt, 90),
				Explanation: expl,
			})
			continue
		}

		// 3. Core logic declarations
		if reFuncDef.MatchString(txt) {
			und.CoreLogic = append(und.CoreLogic, CodeInsightItem{
				Line:        cur.NewLine,
				Kind:        "function",
				Text:        truncateInsightText(txt, 90),
				Explanation: "Introduces or updates functional declaration",
			})
		}
	}

	// If no function definition was caught, capture main contiguous block
	if len(und.CoreLogic) == 0 && len(addedRows) > 0 {
		firstLine := addedRows[0].row.NewLine
		lastLine := addedRows[len(addedRows)-1].row.NewLine
		snippet := truncateInsightText(strings.TrimSpace(addedRows[0].row.Text), 90)
		und.CoreLogic = append(und.CoreLogic, CodeInsightItem{
			Line:        firstLine,
			EndLine:     lastLine,
			Kind:        "core",
			Text:        snippet,
			Explanation: fmt.Sprintf("Primary modification block (Lines %d–%d)", firstLine, lastLine),
		})
	}

	parts := []string{}
	if len(und.FastReturns) > 0 {
		parts = append(parts, fmt.Sprintf("%d fast return%s", len(und.FastReturns), pluralS(len(und.FastReturns))))
	}
	if len(und.EdgeCases) > 0 {
		parts = append(parts, fmt.Sprintf("%d edge case%s", len(und.EdgeCases), pluralS(len(und.EdgeCases))))
	}
	if len(und.CoreLogic) > 0 {
		parts = append(parts, fmt.Sprintf("%d core change%s", len(und.CoreLogic), pluralS(len(und.CoreLogic))))
	}
	if len(parts) > 0 {
		und.Summary = fmt.Sprintf("Surfaces %s across modified hunks.", strings.Join(parts, ", "))
	} else {
		und.Summary = fmt.Sprintf("Modified %d line%s in this file.", len(addedRows), pluralS(len(addedRows)))
	}

	return und
}

// handleReviewWalkthrough delivers file diffs and extracted understanding for the Review Walkthrough.
func (s *Server) handleReviewWalkthrough(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	root := s.ix.Root()
	var (
		walkthroughFiles = []ReviewWalkthroughFile{}
		baseRef          = s.diffBase
		headRef          = "working-tree"
		isPR             = s.pr != nil && s.prHeadSHA != ""
		totalAdditions   = 0
		totalDeletions   = 0
	)

	type targetFile struct {
		path   string
		status string
	}
	var targets []targetFile

	if isPR {
		baseRef = s.diffBase
		headRef = s.prHeadSHA
		for path, status := range s.ix.PRFiles() {
			targets = append(targets, targetFile{path: path, status: status})
		}
	} else {
		statusMap := s.ix.GitStatusMap()
		for path, status := range statusMap {
			targets = append(targets, targetFile{path: path, status: status})
		}

		// If working tree is clean, fallback to HEAD~1..HEAD so the reviewer has immediate diff to inspect
		if len(targets) == 0 && gitAvailable(root) {
			if headOut, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD").Output(); err == nil {
				headSHA := strings.TrimSpace(string(headOut))
				if cfList := gitCommitFiles(root, headSHA); len(cfList) > 0 {
					baseRef = "HEAD~1"
					if len(headSHA) > 7 {
						headRef = headSHA[:7]
					} else {
						headRef = headSHA
					}
					for _, cf := range cfList {
						targets = append(targets, targetFile{path: cf.Path, status: cf.Status})
					}
				}
			}
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].path < targets[j].path
	})

	for _, target := range targets {
		var diffText string
		if isPR {
			diffText = gitDiffBetween(root, target.path, s.diffBase, s.prHeadSHA)
			if diffText == "" {
				diffText = gitDiffAgainst(root, target.path, s.diffBase)
			}
		} else if baseRef == "HEAD~1" {
			diffText = gitDiffBetween(root, target.path, "HEAD~1", "HEAD")
		} else {
			diffText = gitDiffAgainst(root, target.path, baseRef)
		}

		if diffText == "" {
			continue
		}

		hunks := render.HighlightDiff(target.path, diffText)
		addCount, delCount := 0, 0
		for _, h := range hunks {
			for _, row := range h.Rows {
				if row.Type == "add" {
					addCount++
				} else if row.Type == "del" {
					delCount++
				}
			}
		}

		totalAdditions += addCount
		totalDeletions += delCount
		und := extractCodeUnderstanding(target.path, hunks)

		walkthroughFiles = append(walkthroughFiles, ReviewWalkthroughFile{
			Path:          target.path,
			Status:        target.status,
			Additions:     addCount,
			Deletions:     delCount,
			Diff:          diffText,
			Hunks:         hunks,
			Understanding: und,
		})
	}

	writeJSON(w, ReviewWalkthroughResponse{
		Files:          walkthroughFiles,
		TotalFiles:     len(walkthroughFiles),
		TotalAdditions: totalAdditions,
		TotalDeletions: totalDeletions,
		BaseRef:        baseRef,
		HeadRef:        headRef,
		PRMode:         isPR,
	})
}
