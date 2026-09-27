package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// GitStatusPayload contains the complete serialized git status event sent to the browser
// over Server-Sent Events (SSE). It includes working tree status, staged files, dirty directories,
// current branch, commit ahead/behind counts, and recent commits.
type GitStatusPayload struct {
	Git           bool              `json:"git"`                     // True if root is inside a git repository
	GitChanges    int               `json:"gitChanges"`              // Total count of modified, added, deleted, or untracked files
	GitFiles      []string          `json:"gitFiles"`                // List of modified file paths relative to root
	Statuses      map[string]string `json:"statuses"`                // Relative path -> status code (e.g. "M", "A", "D", "U")
	DirtyDirs     map[string]bool   `json:"dirtyDirs,omitempty"`     // Relative directory path -> true if any descendant is modified
	Staged        map[string]bool   `json:"staged,omitempty"`        // Relative path -> true if file has staged changes
	YourStatuses  map[string]string `json:"yourStatuses,omitempty"`  // PR mode: relative path -> reviewer modification status
	YourDirtyDirs map[string]bool   `json:"yourDirtyDirs,omitempty"` // PR mode: directory path -> contains reviewer changes
	Touched       []string          `json:"touched,omitempty"`       // Content changed since the last check, whatever the status code
	Branch        string            `json:"branch,omitempty"`        // Active branch name
	RecentCommits []GitCommit       `json:"recentCommits,omitempty"` // Recent git commits for commit list UI
	CommitsURL    string            `json:"commitsUrl,omitempty"`    // Web URL to view commits on GitHub/forge
	Ahead         int               `json:"ahead"`                   // Commits ahead of upstream tracking branch
	Behind        int               `json:"behind"`                  // Commits behind upstream tracking branch
}

// GitWatcher monitors git state changes in the background and broadcasts updates
// to connected browser clients over SSE (/api/stream). It uses a hybrid detection strategy:
//  1. Fast path: 1-second polling of .git metadata files (HEAD, index, packed-refs) to instantly
//     detect CLI operations (git commit, git checkout, git add).
//  2. Worktree path: adaptive polling of working tree file modifications.
//  3. Manual triggers: immediate re-evaluations triggered on user UI actions.
type GitWatcher struct {
	ix   *Index
	root string

	mu      sync.Mutex
	clients map[chan []byte]struct{}
	active  atomic.Int32

	triggerCh chan struct{}
	stopCh    chan struct{}

	lastIndexMod   time.Time
	lastIndexSize  int64
	lastHeadMod    time.Time
	lastPackedMod  time.Time
	lastHeadCommit string
	lastAhead      int
	lastBehind     int
}

// NewGitWatcher constructs a new GitWatcher attached to the given workspace index.
func NewGitWatcher(ix *Index) *GitWatcher {
	gw := &GitWatcher{
		ix:             ix,
		root:           ix.Root(),
		clients:        make(map[chan []byte]struct{}),
		triggerCh:      make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
		lastHeadCommit: gitHeadCommit(ix.Root()),
	}
	return gw
}

// Start begins the background monitoring goroutine.
func (gw *GitWatcher) Start(ctx context.Context) {
	if gitDisabled || !gitAvailable(gw.root) {
		return
	}

	gitdir := gitDir(gw.root)
	gw.recordGitMeta(gitdir)

	go gw.loop(ctx, gitdir)
}

func (gw *GitWatcher) Stop() {
	select {
	case <-gw.stopCh:
	default:
		close(gw.stopCh)
	}
}

// Trigger requests an immediate status check and broadcast.
func (gw *GitWatcher) Trigger() {
	select {
	case gw.triggerCh <- struct{}{}:
	default:
	}
}

func (gw *GitWatcher) recordGitMeta(gitdir string) bool {
	if gitdir == "" {
		return false
	}
	changed := false

	indexPath := filepath.Join(gitdir, "index")
	if fi, err := os.Stat(indexPath); err == nil {
		if fi.ModTime() != gw.lastIndexMod || fi.Size() != gw.lastIndexSize {
			gw.lastIndexMod = fi.ModTime()
			gw.lastIndexSize = fi.Size()
			changed = true
		}
	}

	headPath := filepath.Join(gitdir, "HEAD")
	if fi, err := os.Stat(headPath); err == nil {
		if fi.ModTime() != gw.lastHeadMod {
			gw.lastHeadMod = fi.ModTime()
			changed = true
		}
	}

	packedPath := filepath.Join(gitdir, "packed-refs")
	if fi, err := os.Stat(packedPath); err == nil {
		if fi.ModTime() != gw.lastPackedMod {
			gw.lastPackedMod = fi.ModTime()
			changed = true
		}
	}

	return changed
}

func (gw *GitWatcher) loop(ctx context.Context, gitdir string) {
	metaTicker := time.NewTicker(1 * time.Second)
	defer metaTicker.Stop()

	adaptiveInterval := 1 * time.Second
	worktreeTicker := time.NewTicker(adaptiveInterval)
	defer worktreeTicker.Stop()

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-gw.stopCh:
			return

		case <-metaTicker.C:
			// Fast path: check .git metadata (HEAD, index, packed-refs).
			// If touched by git CLI (commit, checkout, add, etc.), refresh immediately.
			if gw.recordGitMeta(gitdir) {
				gw.checkAndBroadcast()
			}

		case <-worktreeTicker.C:
			// Worktree path: check for modifications in files outside git CLI.
			// Only run when at least one client is actively listening.
			if gw.active.Load() > 0 {
				dur := gw.checkAndBroadcast()
				// Adapt interval based on execution speed: min 1s, max 10s.
				newInterval := dur * 5
				if newInterval < 1*time.Second {
					newInterval = 1 * time.Second
				} else if newInterval > 10*time.Second {
					newInterval = 10 * time.Second
				}
				if newInterval != adaptiveInterval {
					adaptiveInterval = newInterval
					worktreeTicker.Reset(adaptiveInterval)
				}
			}

		case <-gw.triggerCh:
			// Debounce rapid triggers
			time.Sleep(50 * time.Millisecond)
			// Drain any pending triggers queued during debounce
			select {
			case <-gw.triggerCh:
			default:
			}
			gw.checkAndBroadcast()

		case <-heartbeatTicker.C:
			gw.broadcast([]byte(": ping\n\n"))
		}
	}
}

// Refresh runs an immediate UpdateGitStatus, broadcasts to subscribers if changed,
// and returns the latest git status payload.
func (gw *GitWatcher) Refresh() GitStatusPayload {
	count, files, changed, statuses, dirtyDirs, staged, yourStatuses, yourDirtyDirs, touched := gw.ix.UpdateGitStatus()
	var recentCommits []GitCommit
	var ahead, behind int
	if gitAvailable(gw.root) {
		recentCommits = gitRecentCommits(gw.root, 5)
		headCommit := ""
		if len(recentCommits) > 0 {
			headCommit = recentCommits[0].Hash
		}
		ahead, behind, _ = gitAheadBehind(gw.root)
		gw.mu.Lock()
		if headCommit != gw.lastHeadCommit || ahead != gw.lastAhead || behind != gw.lastBehind {
			gw.lastHeadCommit = headCommit
			gw.lastAhead = ahead
			gw.lastBehind = behind
			changed = true
		}
		gw.mu.Unlock()
	}

	branch := gitCurrentBranch(gw.root)
	payload := GitStatusPayload{
		Git:           gitAvailable(gw.root),
		GitChanges:    count,
		GitFiles:      files,
		Statuses:      statuses,
		DirtyDirs:     dirtyDirs,
		Staged:        staged,
		YourStatuses:  yourStatuses,
		YourDirtyDirs: yourDirtyDirs,
		Touched:       touched,
		Branch:        branch,
		RecentCommits: recentCommits,
		CommitsURL:    gitCommitsWebURL(gw.root, branch),
		Ahead:         ahead,
		Behind:        behind,
	}
	if changed {
		data, err := json.Marshal(payload)
		if err == nil {
			msg := []byte(fmt.Sprintf("event: git-status\ndata: %s\n\n", data))
			gw.broadcast(msg)
		}
	}
	return payload
}

// checkAndBroadcast runs UpdateGitStatus and broadcasts to subscribers if changed.
// Returns duration of the status check.
func (gw *GitWatcher) checkAndBroadcast() time.Duration {
	start := time.Now()
	gw.Refresh()
	return time.Since(start)
}

func (gw *GitWatcher) broadcast(msg []byte) {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	for ch := range gw.clients {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Subscribe registers a new SSE listener channel and returns an unregister cancel function.
func (gw *GitWatcher) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 16)

	gw.mu.Lock()
	gw.clients[ch] = struct{}{}
	gw.active.Add(1)
	gw.mu.Unlock()

	// Immediately send current state to newly connected client
	inGit := gitAvailable(gw.root)
	var initialMsg []byte
	if inGit {
		count, files := gw.ix.GitChanges()
		statuses := gw.ix.GitStatusMap()
		staged := gw.ix.GitStagedMap()
		yourStatuses := gw.ix.GitYourStatusMap()
		dirtyDirs := map[string]bool{}
		for p := range statuses {
			for i := strings.LastIndexByte(p, '/'); i >= 0; i = strings.LastIndexByte(p, '/') {
				p = p[:i]
				dirtyDirs[p] = true
			}
		}
		yourDirtyDirs := map[string]bool{}
		for p := range yourStatuses {
			for i := strings.LastIndexByte(p, '/'); i >= 0; i = strings.LastIndexByte(p, '/') {
				p = p[:i]
				yourDirtyDirs[p] = true
			}
		}
		branch := gitCurrentBranch(gw.root)
		ahead, behind, _ := gitAheadBehind(gw.root)
		payload := GitStatusPayload{
			Git:           true,
			GitChanges:    count,
			GitFiles:      files,
			Statuses:      statuses,
			DirtyDirs:     dirtyDirs,
			Staged:        staged,
			YourStatuses:  yourStatuses,
			YourDirtyDirs: yourDirtyDirs,
			Branch:        branch,
			RecentCommits: gitRecentCommits(gw.root, 5),
			CommitsURL:    gitCommitsWebURL(gw.root, branch),
			Ahead:         ahead,
			Behind:        behind,
		}
		if data, err := json.Marshal(payload); err == nil {
			initialMsg = []byte(fmt.Sprintf("event: git-status\ndata: %s\n\n", data))
		}
	} else {
		payload := GitStatusPayload{
			Git:        false,
			GitChanges: 0,
			GitFiles:   []string{},
			Statuses:   map[string]string{},
		}
		if data, err := json.Marshal(payload); err == nil {
			initialMsg = []byte(fmt.Sprintf("event: git-status\ndata: %s\n\n", data))
		}
	}

	if len(initialMsg) > 0 {
		ch <- initialMsg
	}

	cancel := func() {
		gw.mu.Lock()
		if _, ok := gw.clients[ch]; ok {
			delete(gw.clients, ch)
			gw.active.Add(-1)
			close(ch)
		}
		gw.mu.Unlock()
	}

	return ch, cancel
}
