package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"px0/internal/render"
)

func TestReviewWorkflowDefaults(t *testing.T) {
	defs := defaultWorkflows()
	if len(defs) == 0 {
		t.Fatal("expected default workflows, got none")
	}

	standard := defs[0]
	if standard.ID != "standard" {
		t.Fatalf("expected first workflow to be 'standard', got %q", standard.ID)
	}
	if len(standard.Stages) != 4 {
		t.Fatalf("expected standard workflow to have 4 stages, got %d", len(standard.Stages))
	}
	for i, st := range standard.Stages {
		if st.Title == "" || st.Prompt == "" || st.Question == "" {
			t.Fatalf("stage %d in standard workflow is missing required fields: %+v", i, st)
		}
	}
}

func TestReviewWorkflowConfigEndpoints(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)

	// GET config
	req := httptest.NewRequest(http.MethodGet, "/api/review-workflow/config", nil)
	rec := httptest.NewRecorder()
	s.handleReviewWorkflowConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var cfg ReviewWorkflowConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}
	if len(cfg.Workflows) == 0 {
		t.Fatal("expected workflows in config response")
	}

	// POST new config
	customCfg := ReviewWorkflowConfig{
		Workflows: []ReviewWorkflowDef{
			{
				ID:   "custom",
				Name: "Custom Test Flow",
				Stages: []ReviewStageDef{
					{
						ID:       "s1",
						Title:    "Custom Stage 1",
						Question: "Question 1?",
						Prompt:   "Prompt 1",
					},
				},
			},
		},
		ActiveID: "custom",
	}
	data, _ := json.Marshal(customCfg)
	postReq := httptest.NewRequest(http.MethodPost, "/api/review-workflow/config", bytes.NewReader(data))
	postReq.Host = "127.0.0.1:7777"
	postReq.Header.Set("Origin", "http://127.0.0.1:7777")
	postRec := httptest.NewRecorder()
	s.handleReviewWorkflowConfig(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on config update, got %d: %s", postRec.Code, postRec.Body.String())
	}

	// Verify on-disk write
	p := filepath.Join(tmp, ".px0", "workflows.json")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		t.Fatalf("expected config file to be written at %s", p)
	}
}

func TestReviewWorkflowStatusAndStop(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)

	// Status before any run
	req := httptest.NewRequest(http.MethodGet, "/api/review-workflow/status", nil)
	rec := httptest.NewRecorder()
	s.handleReviewWorkflowStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Stop when idle returns 200
	stopReq := httptest.NewRequest(http.MethodPost, "/api/review-workflow/stop", nil)
	stopReq.Host = "127.0.0.1:7777"
	stopReq.Header.Set("Origin", "http://127.0.0.1:7777")
	stopRec := httptest.NewRecorder()
	s.handleReviewWorkflowStop(stopRec, stopReq)
	if stopRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on stop, got %d", stopRec.Code)
	}
}

func TestReviewWorkflowRunEndpoint(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)

	body, _ := json.Marshal(map[string]any{
		"workflowId": "standard",
		"stageIndex": 0,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/review-workflow/run", bytes.NewReader(body))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	rec := httptest.NewRecorder()
	s.handleReviewWorkflowRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on run, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReviewWorkflowConfigCorruptAndSaveValidation(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)

	// Create corrupt workflows.json
	workflowDir := filepath.Join(tmp, ".px0")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	corruptPath := filepath.Join(workflowDir, "workflows.json")
	if err := os.WriteFile(corruptPath, []byte("invalid json {{{"), 0644); err != nil {
		t.Fatalf("failed to write corrupt config: %v", err)
	}

	// Loading corrupt config should fallback to default workflows
	loaded := s.reviewFlow.loadConfig()
	if len(loaded.Workflows) == 0 || loaded.ActiveID != "standard" {
		t.Fatalf("expected fallback to default workflows on corrupt config, got %+v", loaded)
	}

	// Save empty workflow config should fail validation
	emptyCfg := ReviewWorkflowConfig{Workflows: []ReviewWorkflowDef{}}
	err := s.reviewFlow.saveConfig(emptyCfg)
	if err == nil {
		t.Fatal("expected error when saving empty workflow config")
	}
}

func TestReviewWorkflowRunAndOrchestration(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)
	a, err := newAgentManager(tmp, "echo {prompt}", nil)
	if err != nil {
		t.Fatalf("failed to create agent manager: %v", err)
	}
	s.SetAgent(a)

	customCfg := ReviewWorkflowConfig{
		Workflows: []ReviewWorkflowDef{
			{
				ID:   "test-flow",
				Name: "Test Flow",
				Stages: []ReviewStageDef{
					{
						ID:                  "stg1",
						Title:               "Stage 1",
						Question:            "Question 1?",
						Prompt:              "Prompt 1",
						PassPreviousContext: false,
					},
					{
						ID:                  "stg2",
						Title:               "Stage 2",
						Question:            "Question 2?",
						Prompt:              "Prompt 2",
						PassPreviousContext: true,
					},
				},
			},
		},
		ActiveID: "test-flow",
	}
	if err := s.reviewFlow.saveConfig(customCfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Start workflow run
	run, err := s.reviewFlow.Run("test-flow", 0)
	if err != nil {
		t.Fatalf("failed to start workflow: %v", err)
	}
	if run == nil || run.Status != "running" {
		t.Fatalf("expected running status, got %+v", run)
	}

	// Simulate completing thread turns in background for stages
	longReply := strings.Repeat("X", 2000)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			time.Sleep(20 * time.Millisecond)
			r := s.reviewFlow.getRun()
			if r == nil {
				continue
			}

			// Complete stage 0 turn
			if len(r.Stages) > 0 && r.Stages[0].ThreadID != "" && r.Stages[0].Status == "running" {
				stg0Thread := s.threads.Get(r.Stages[0].ThreadID)
				if stg0Thread != nil && stg0Thread.running() {
					s.threads.mu.Lock()
					stg0Thread.Turns[len(stg0Thread.Turns)-1].Running = false
					stg0Thread.Turns[len(stg0Thread.Turns)-1].Reply = longReply
					s.threads.publishThreadLocked(stg0Thread)
					s.threads.mu.Unlock()
				}
			}

			// Complete stage 1 turn
			if len(r.Stages) > 1 && r.Stages[1].ThreadID != "" && r.Stages[1].Status == "running" {
				stg1Thread := s.threads.Get(r.Stages[1].ThreadID)
				if stg1Thread != nil && stg1Thread.running() {
					s.threads.mu.Lock()
					stg1Thread.Turns[len(stg1Thread.Turns)-1].Running = false
					stg1Thread.Turns[len(stg1Thread.Turns)-1].Reply = "Stage 2 done"
					s.threads.publishThreadLocked(stg1Thread)
					s.threads.mu.Unlock()
				}
			}

			if r.Status == "completed" {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for workflow completion")
	}

	finalRun := s.reviewFlow.getRun()
	if finalRun.Status != "completed" {
		t.Fatalf("expected run status completed, got %s", finalRun.Status)
	}

	// Verify context passing in stage 2 prompt
	stg1Thread := s.threads.Get(finalRun.Stages[1].ThreadID)
	if stg1Thread == nil || len(stg1Thread.Turns) == 0 {
		t.Fatal("expected stage 2 thread to exist")
	}
	stg2Prompt := stg1Thread.Turns[0].Prompt
	if !strings.Contains(stg2Prompt, "Context from previous review stage (Stage 1):") {
		t.Fatalf("expected stage 2 prompt to include previous context, got %s", stg2Prompt)
	}
}

func TestReviewWorkflowRunUnknownIDAndNilThreads(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)

	// Run with unknown ID falls back to first workflow definition
	run, err := s.reviewFlow.Run("non-existent-id", 0)
	if err != nil {
		t.Fatalf("expected fallback for unknown workflow id, got error: %v", err)
	}
	if run.WorkflowID != "standard" {
		t.Fatalf("expected workflow standard, got %s", run.WorkflowID)
	}

	// Since s.threads is nil, orchestration should mark stage as failed
	time.Sleep(50 * time.Millisecond)
	updatedRun := s.reviewFlow.getRun()
	if updatedRun.Stages[0].Status != "failed" {
		t.Fatalf("expected stage status failed when threads system is nil, got %s", updatedRun.Stages[0].Status)
	}
	if !strings.Contains(updatedRun.Stages[0].Error, "AI thread system not available") {
		t.Fatalf("expected AI thread system not available error, got %s", updatedRun.Stages[0].Error)
	}
}

func TestReviewWorkflowStopActiveRun(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)
	a, err := newAgentManager(tmp, "echo {prompt}", nil)
	if err != nil {
		t.Fatalf("failed to create agent manager: %v", err)
	}
	s.SetAgent(a)

	run, err := s.reviewFlow.Run("standard", 0)
	if err != nil {
		t.Fatalf("failed to start workflow: %v", err)
	}
	if run.Status != "running" {
		t.Fatalf("expected run status running, got %s", run.Status)
	}

	time.Sleep(50 * time.Millisecond)

	stoppedRun, err := s.reviewFlow.Stop()
	if err != nil {
		t.Fatalf("failed to stop run: %v", err)
	}
	if stoppedRun.Status != "stopped" {
		t.Fatalf("expected run status stopped, got %s", stoppedRun.Status)
	}
}

func TestReviewWorkflowSSEStreamAndNilManager(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/review-workflow/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	go s.handleReviewWorkflowStream(rec, req)

	time.Sleep(50 * time.Millisecond)
	cancel()

	body := rec.Body.String()
	if !strings.Contains(body, "event: snapshot") {
		t.Fatalf("expected stream to contain initial event: snapshot, got: %s", body)
	}

	// Test nil reviewFlow handler guard
	sNil := &Server{}
	recNil := httptest.NewRecorder()
	reqNil := httptest.NewRequest(http.MethodGet, "/api/review-workflow/config", nil)
	sNil.handleReviewWorkflowConfig(recNil, reqNil)
	if recNil.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for nil reviewFlow, got %d", recNil.Code)
	}
}

func TestReviewWorkflowHTTPHandlerValidation(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)

	// Method not allowed on config handler
	reqMethod := httptest.NewRequest(http.MethodDelete, "/api/review-workflow/config", nil)
	recMethod := httptest.NewRecorder()
	s.handleReviewWorkflowConfig(recMethod, reqMethod)
	if recMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed, got %d", recMethod.Code)
	}

	// Post invalid JSON to config
	badJSONReq := httptest.NewRequest(http.MethodPost, "/api/review-workflow/config", strings.NewReader("bad json"))
	badJSONReq.Host = "127.0.0.1:7777"
	badJSONReq.Header.Set("Origin", "http://127.0.0.1:7777")
	badJSONRec := httptest.NewRecorder()
	s.handleReviewWorkflowConfig(badJSONRec, badJSONReq)
	if badJSONRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", badJSONRec.Code)
	}
}

func TestExtractCodeUnderstanding(t *testing.T) {
	hunks := []render.DiffHunk{
		{
			OldStart: 10,
			NewStart: 10,
			Section:  "func ProcessRequest",
			Rows: []render.DiffRow{
				{Type: "ctx", OldLine: 10, NewLine: 10, Text: "func ProcessRequest(req *Request) error {"},
				{Type: "add", NewLine: 11, Text: "\tif req == nil {"},
				{Type: "add", NewLine: 12, Text: "\t\treturn ErrNilRequest"},
				{Type: "add", NewLine: 13, Text: "\t}"},
				{Type: "add", NewLine: 14, Text: "\tif !user.HasPermission(\"admin\") {"},
				{Type: "add", NewLine: 15, Text: "\t\treturn ErrForbidden"},
				{Type: "add", NewLine: 16, Text: "\t}"},
				{Type: "add", NewLine: 17, Text: "\tres, err := db.Query()"},
				{Type: "add", NewLine: 18, Text: "\tif err != nil {"},
				{Type: "add", NewLine: 19, Text: "\t\treturn fmt.Errorf(\"failed to query: %w\", err)"},
				{Type: "add", NewLine: 20, Text: "\t}"},
				{Type: "add", NewLine: 21, Text: "\tcache.Set(res)"},
				{Type: "ctx", OldLine: 11, NewLine: 22, Text: "\treturn nil"},
			},
		},
	}

	und := extractCodeUnderstanding("service.go", hunks)
	if und == nil {
		t.Fatal("expected non-nil understanding")
	}

	// Should detect fast returns (nil check & auth check)
	if len(und.FastReturns) == 0 {
		t.Fatalf("expected fast returns to be detected, got 0")
	}
	hasNil := false
	hasAuth := false
	for _, fr := range und.FastReturns {
		if fr.Kind == "nil_check" {
			hasNil = true
		}
		if fr.Kind == "auth" {
			hasAuth = true
		}
	}
	if !hasNil {
		t.Errorf("expected nil_check fast return, got: %+v", und.FastReturns)
	}
	if !hasAuth {
		t.Errorf("expected auth fast return, got: %+v", und.FastReturns)
	}

	// Should detect edge case (error check)
	if len(und.EdgeCases) == 0 {
		t.Fatalf("expected edge cases to be detected, got 0")
	}
	hasErr := false
	for _, ec := range und.EdgeCases {
		if ec.Kind == "error" {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("expected error edge case, got: %+v", und.EdgeCases)
	}

	// Should have generated summary
	if und.Summary == "" {
		t.Errorf("expected non-empty summary")
	}
}

func TestReviewWalkthroughEndpoint(t *testing.T) {
	tmp := t.TempDir()
	ix := NewIndex(tmp)
	s := NewServer(ix, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/review/walkthrough", nil)
	rec := httptest.NewRecorder()
	s.handleReviewWalkthrough(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ReviewWalkthroughResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Files == nil {
		t.Fatalf("expected non-nil files array")
	}
}
