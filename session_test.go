package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionAPIEndpoints(t *testing.T) {
	// Isolate session path to a temp dir
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	root := t.TempDir()
	ix := NewIndex(root)
	s := NewServer(ix, nil, "/rev-999/")

	// 1. GET /rev-999/api/session initially
	req := httptest.NewRequest(http.MethodGet, "/rev-999/api/session", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /rev-999/api/session failed: code %d, body %s", rec.Code, rec.Body.String())
	}
	var initResp WorkspaceSession
	if err := json.Unmarshal(rec.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("unmarshal session failed: %v", err)
	}
	if len(initResp.Tabs) != 0 {
		t.Errorf("expected 0 tabs initially, got %d", len(initResp.Tabs))
	}

	// 2. POST /rev-999/api/session with tabs and openDirs
	active := 1
	payload := map[string]any{
		"tabs": []map[string]string{
			{"path": "foo.go"},
			{"path": "bar.go"},
		},
		"active":   active,
		"openDirs": []string{"pkg", "cmd"},
	}
	b, _ := json.Marshal(payload)
	postReq := httptest.NewRequest(http.MethodPost, "/rev-999/api/session", bytes.NewReader(b))
	postReq.Host = "127.0.0.1:7777"
	postReq.Header.Set("Origin", "http://127.0.0.1:7777")
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	s.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /rev-999/api/session failed: code %d, body %s", postRec.Code, postRec.Body.String())
	}

	// 3. Verify file was written to disk under rev-999.json
	sessFile := filepath.Join(tmpDir, "px0", "sessions", "rev-999.json")
	if _, err := os.Stat(sessFile); err != nil {
		t.Fatalf("session file not found on disk: %v", err)
	}

	// 4. Create a new server instance with same basePath and check that state is restored
	s2 := NewServer(ix, nil, "/rev-999/")
	sess2 := s2.session.Get()
	if len(sess2.Tabs) != 2 {
		t.Fatalf("expected 2 tabs in restored session, got %d", len(sess2.Tabs))
	}
	if sess2.Tabs[0].Path != "foo.go" || sess2.Tabs[1].Path != "bar.go" {
		t.Errorf("unexpected tab paths: %+v", sess2.Tabs)
	}
	if sess2.Active != 1 {
		t.Errorf("expected active tab 1, got %d", sess2.Active)
	}
	if len(sess2.OpenDirs) != 2 || sess2.OpenDirs[0] != "pkg" || sess2.OpenDirs[1] != "cmd" {
		t.Errorf("unexpected openDirs: %+v", sess2.OpenDirs)
	}
}

func TestPRDraftSessionPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	root := t.TempDir()
	ix := NewIndex(root)
	s := NewServer(ix, nil, "/rev-42/")
	pr := &prSession{
		diffBase: "HEAD",
	}
	s.SetPR(pr)

	// Add a draft comment via POST /rev-42/api/pr/comments
	payload := map[string]any{
		"path": "main.go",
		"line": 25,
		"side": "RIGHT",
		"body": "Check this line",
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/rev-42/api/pr/comments", bytes.NewReader(b))
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /rev-42/api/pr/comments failed: code %d, body %s", rec.Code, rec.Body.String())
	}

	// Create a second server instance simulating a restart
	s2 := NewServer(ix, nil, "/rev-42/")
	pr2 := &prSession{
		diffBase: "HEAD",
	}
	s2.SetPR(pr2)

	// Verify drafts were restored in pr2
	pr2.mu.Lock()
	defer pr2.mu.Unlock()
	if len(pr2.comments) != 1 {
		t.Fatalf("expected 1 restored draft comment, got %d", len(pr2.comments))
	}
	if pr2.comments[0].Path != "main.go" || pr2.comments[0].Body != "Check this line" {
		t.Errorf("unexpected restored comment: %+v", pr2.comments[0])
	}
}
