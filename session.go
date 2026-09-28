package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SessionTab represents an open editor tab pointing to a file path.
type SessionTab struct {
	Path string `json:"path"`
}

// WorkspaceSession stores the persistent UI state for a workspace:
// the list of open tabs, the currently active tab index, expanded directory tree paths,
// and any unsaved PR review comment drafts.
type WorkspaceSession struct {
	Tabs     []SessionTab `json:"tabs"`
	Active   int          `json:"active"`
	OpenDirs []string     `json:"openDirs"`
	Drafts   []prComment  `json:"drafts,omitempty"`
}

// sessionFilePath returns the path to the JSON file where workspace session state is saved.
// It uses XDG_STATE_HOME/px0/sessions if available, falling back to ~/.px0/sessions or
// os.TempDir()/px0-sessions. The file name is derived from the base path or an 8-byte
// SHA-256 hash of the cleaned workspace root path.
func sessionFilePath(basePath, root string) string {
	dir := ""
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		dir = filepath.Join(xdg, "px0", "sessions")
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		dir = filepath.Join(home, ".px0", "sessions")
	} else {
		dir = filepath.Join(os.TempDir(), "px0-sessions")
	}

	key := ""
	if basePath != "" && basePath != "/" {
		clean := strings.Trim(basePath, "/")
		clean = strings.ReplaceAll(clean, "/", "_")
		key = clean
	} else {
		h := sha256.Sum256([]byte(filepath.Clean(root)))
		key = hex.EncodeToString(h[:8])
	}
	return filepath.Join(dir, key+".json")
}

// sessionManager coordinates concurrent thread-safe access and persistence
// for workspace session data on disk.
type sessionManager struct {
	mu   sync.Mutex
	path string
	data WorkspaceSession
}

// newSessionManager initializes a session manager for the given workspace,
// loading any existing session JSON file from disk.
func newSessionManager(basePath, root string) *sessionManager {
	p := sessionFilePath(basePath, root)
	sm := &sessionManager{
		path: p,
		data: WorkspaceSession{
			Tabs:     []SessionTab{},
			OpenDirs: []string{},
		},
	}
	sm.load()
	return sm
}

// load reads the session state from disk into memory, initializing empty slices if nil.
func (sm *sessionManager) load() {
	if sm.path == "" {
		return
	}
	data, err := os.ReadFile(sm.path)
	if err != nil {
		return
	}
	var s WorkspaceSession
	if err := json.Unmarshal(data, &s); err == nil {
		if s.Tabs == nil {
			s.Tabs = []SessionTab{}
		}
		if s.OpenDirs == nil {
			s.OpenDirs = []string{}
		}
		sm.data = s
	}
}

// Get returns a snapshot of the current workspace session.
func (sm *sessionManager) Get() WorkspaceSession {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.data
}

// Update executes a mutation function on the session state under mutex protection
// and persists the resulting state formatted as JSON to disk.
func (sm *sessionManager) Update(fn func(*WorkspaceSession)) WorkspaceSession {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	fn(&sm.data)
	if sm.path != "" {
		if err := os.MkdirAll(filepath.Dir(sm.path), 0o755); err == nil {
			if b, err := json.MarshalIndent(sm.data, "", "  "); err == nil {
				_ = os.WriteFile(sm.path, append(b, '\n'), 0o644)
			}
		}
	}
	return sm.data
}

// handleSession handles GET and POST requests for /api/session.
// GET returns the current workspace session state.
// POST updates tab list, active tab, and open directories, saving changes to disk.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.session.Get())
	case http.MethodPost:
		if !localPost(w, r) {
			return
		}
		var payload struct {
			Tabs     *[]SessionTab `json:"tabs"`
			Active   *int          `json:"active"`
			OpenDirs *[]string     `json:"openDirs"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
			fail(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		updated := s.session.Update(func(ws *WorkspaceSession) {
			if payload.Tabs != nil {
				ws.Tabs = *payload.Tabs
			}
			if payload.Active != nil {
				ws.Active = *payload.Active
			}
			if payload.OpenDirs != nil {
				ws.OpenDirs = *payload.OpenDirs
			}
		})
		writeJSON(w, updated)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
