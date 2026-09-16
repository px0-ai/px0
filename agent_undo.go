package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Undo for the most recent agent edit. A harness writes straight to disk, and
// when the file already held uncommitted work git cannot give the old bytes
// back. So before each run px0 keeps a copy of every file git lists as dirty,
// and after it builds a plan that puts back exactly the paths the run changed:
//
//   - dirty before: the copy taken before the run
//   - clean before: the blob at the HEAD the run started from
//   - absent before: removed, including a directory the run created
//
// Only the last edit can be undone, and only once. Undo refuses when a path has
// moved on since the run ended, so it never throws away work done afterwards.

const (
	undoFileBytes  = 8 << 20  // a dirty file larger than this is not copied
	undoTotalBytes = 64 << 20 // nor is anything past this much in one snapshot
)

var (
	errUndoNone  = errors.New("there is no agent edit to undo")
	errUndoStale = errors.New("changed since the agent edit")
)

// preEdit is the tree as it stood before a run.
type preEdit struct {
	status  map[string]string // worktreeSnapshot
	head    string            // HEAD commit, empty in a repository with no commits
	saved   map[string]savedFile
	missing map[string]bool // listed by git but not on disk, e.g. deleted
}

type savedFile struct {
	data []byte
	mode fs.FileMode
}

// undoStep restores one path.
type undoStep struct {
	rel    string
	remove bool // did not exist before the run
	data   []byte
	mode   fs.FileMode
}

type agentUndo struct {
	steps  []undoStep
	stamps map[string]string // each path as the run left it
}

func capturePreEdit(root string) *preEdit {
	pre := &preEdit{
		status:  worktreeSnapshot(root),
		saved:   map[string]savedFile{},
		missing: map[string]bool{},
	}
	if out, err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "HEAD").Output(); err == nil {
		pre.head = strings.TrimSpace(string(out))
	}
	total := 0
	for rel := range pre.status {
		if strings.HasSuffix(rel, "/") {
			continue // an untracked directory: too open-ended to copy
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		fi, err := os.Lstat(abs)
		if errors.Is(err, fs.ErrNotExist) {
			pre.missing[rel] = true
			continue
		}
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > undoFileBytes || total+int(fi.Size()) > undoTotalBytes {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		total += len(data)
		pre.saved[rel] = savedFile{data: data, mode: fi.Mode().Perm()}
	}
	return pre
}

// planUndo returns how to reverse changed, or a reason when some path cannot be
// put back. A partial undo would leave a tree nobody asked for, so it is all or
// nothing.
func planUndo(root string, pre *preEdit, changed []string) (*agentUndo, string) {
	u := &agentUndo{stamps: map[string]string{}}
	for _, rel := range changed {
		step := undoStep{rel: rel}
		_, wasDirty := pre.status[rel]
		switch {
		case strings.HasSuffix(rel, "/"):
			if wasDirty {
				return nil, "files were added to the untracked directory " + rel
			}
			step.remove = true
		case pre.missing[rel]:
			step.remove = true
		case wasDirty:
			f, ok := pre.saved[rel]
			if !ok {
				return nil, rel + " was too large to keep a copy of"
			}
			step.data, step.mode = f.data, f.mode
		default:
			data, mode, found, err := headBlob(root, pre.head, rel)
			if err != nil {
				return nil, "could not read " + rel + " from HEAD"
			}
			if !found {
				step.remove = true
			} else {
				step.data, step.mode = data, mode
			}
		}
		u.steps = append(u.steps, step)
		u.stamps[rel] = pathStamp(filepath.Join(root, filepath.FromSlash(rel)))
	}
	return u, ""
}

// headBlob reads rel as committed at head. found is false when head does not
// have it, which for a path that was clean before the run means it is new.
func headBlob(root, head, rel string) (data []byte, mode fs.FileMode, found bool, err error) {
	if head == "" {
		return nil, 0, false, nil
	}
	// Both commands resolve rel against -C, which is the served root and may sit
	// below the repository's top level.
	out, err := exec.Command("git", "-C", root, "ls-tree", "-z", head, "--", rel).Output()
	if err != nil {
		return nil, 0, false, err
	}
	if len(bytes.TrimRight(out, "\x00")) == 0 {
		return nil, 0, false, nil
	}
	mode = 0o644
	if bytes.HasPrefix(out, []byte("100755 ")) {
		mode = 0o755
	} else if !bytes.HasPrefix(out, []byte("100644 ")) {
		return nil, 0, false, fmt.Errorf("%s is not a regular file at HEAD", rel)
	}
	data, err = exec.Command("git", "-C", root, "cat-file", "blob", head+":./"+rel).Output()
	if err != nil {
		return nil, 0, false, err
	}
	return data, mode, true, nil
}

// pathStamp is enough to tell whether a path moved on since it was taken.
func pathStamp(abs string) string {
	fi, err := os.Lstat(abs)
	if err != nil {
		return "-"
	}
	return strconv.FormatInt(fi.Size(), 10) + " " + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

// Undo reverses the last edit. Unless forced it refuses when any of the paths
// changed after the run ended.
func (m *agentManager) Undo(force bool) ([]string, error) {
	if m == nil {
		return nil, errUndoNone
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job != nil && m.job.Running {
		return nil, errAgentBusy
	}
	if m.undo == nil {
		return nil, errUndoNone
	}
	u := m.undo

	paths := make([]string, 0, len(u.steps))
	for _, st := range u.steps {
		paths = append(paths, st.rel)
	}
	if !force {
		var stale []string
		for _, st := range u.steps {
			if pathStamp(filepath.Join(m.root, filepath.FromSlash(st.rel))) != u.stamps[st.rel] {
				stale = append(stale, st.rel)
			}
		}
		if len(stale) > 0 {
			uiStatus("warn", "agent: undo refused", strings.Join(stale, ", ")+" changed since the edit", 0, os.Stdout)
			return nil, fmt.Errorf("%s %w", strings.Join(stale, ", "), errUndoStale)
		}
	}

	// Single use from here on: a failure halfway leaves a tree the plan no
	// longer describes.
	m.undo = nil
	if m.job != nil {
		m.job.Undoable = false
	}

	var failed []string
	for _, st := range u.steps {
		abs := filepath.Join(m.root, filepath.FromSlash(st.rel))
		var err error
		if st.remove {
			err = os.RemoveAll(abs)
		} else {
			err = restoreFile(abs, st.data, st.mode)
		}
		if err != nil {
			failed = append(failed, st.rel+": "+err.Error())
		}
	}
	m.settle(paths)

	if len(failed) > 0 {
		uiStatus("err", "agent: undo incomplete", strings.Join(failed, "; "), 0, os.Stdout)
		return paths, errors.New("could not restore " + strings.Join(failed, "; "))
	}
	uiStatus("ok", fmt.Sprintf("agent: undid edit to %d file(s)", len(paths)), strings.Join(paths, ", "), 0, os.Stdout)
	return paths, nil
}

func restoreFile(abs string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	// Whatever sits at the path now, a directory or a link, gives way to the file.
	if fi, err := os.Lstat(abs); err == nil && !fi.Mode().IsRegular() {
		if err := os.RemoveAll(abs); err != nil {
			return err
		}
	}
	if err := os.WriteFile(abs, data, mode); err != nil {
		return err
	}
	return os.Chmod(abs, mode)
}

func (s *Server) handleAgentUndo(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	paths, err := s.agent.Undo(r.URL.Query().Get("force") == "1")
	if err != nil {
		code := 400
		switch {
		case errors.Is(err, errAgentBusy), errors.Is(err, errUndoStale):
			code = http.StatusConflict
		case errors.Is(err, errUndoNone):
			code = http.StatusNotFound
		case paths != nil:
			code = http.StatusInternalServerError
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, map[string]any{"undone": paths})
}
