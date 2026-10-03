package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func gitInstalled() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// gitRepo builds a real repository under a fresh temp dir: keep/mod/del/ren are
// committed, then the tree is dirtied (modify, stage-new, untrack, delete,
// rename) so every status code is exercised. Returns the served root.
func gitRepo(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	// macOS TempDir lives under /var -> /private/var; git reports the real path.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write("keep.go", "keep\n")
	write("sub/mod.go", "line one\n")
	write("del.go", "del\n")
	write("sub/ren.go", "old\n")
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("config", "core.hooksPath", "/dev/null")
	run("add", "-A")
	run("commit", "-qm", "test: init")
	// Dirty it: modify, stage a new file, leave one untracked, delete, rename.
	write("sub/mod.go", "line two\n")
	write("add.go", "added\n")
	run("add", "add.go")
	write("untr.go", "untracked\n")
	os.Remove(filepath.Join(root, "del.go"))
	run("mv", "sub/ren.go", "sub/ren2.go")
	return root
}

func TestGitStatus(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)

	if !gitAvailable(root) {
		t.Fatal("gitAvailable false for a real repo")
	}
	st := gitStatus(root)
	want := map[string]string{
		"sub/mod.go":  "M",
		"add.go":      "A",
		"untr.go":     "U",
		"del.go":      "D",
		"sub/ren2.go": "R",
	}
	for path, code := range want {
		if st[path] != code {
			t.Errorf("status[%q] = %q, want %q (full: %v)", path, st[path], code, st)
		}
	}
	if _, ok := st["keep.go"]; ok {
		t.Errorf("keep.go should have no status, got %q", st["keep.go"])
	}

	// Overlay onto tree nodes. Deleted files appear with status "D".
	ix := NewIndex(root)
	ix.Build()
	byName := map[string]Node{}
	kids, _ := ix.Children("")
	for _, k := range kids {
		byName[k.Name] = k
	}
	sub, _ := ix.Children("sub")
	for _, k := range sub {
		byName[k.Name] = k
	}
	nodeWant := map[string]string{
		"mod.go":  "M",
		"add.go":  "A",
		"untr.go": "U",
		"ren2.go": "R",
		"del.go":  "D",
		"keep.go": "",
	}
	for name, code := range nodeWant {
		if byName[name].Status != code {
			t.Errorf("node %q status = %q, want %q", name, byName[name].Status, code)
		}
	}
	// The sub/ dir holds changed files, so its (collapsed) folder node is dirty.
	if !byName["sub"].Dir || !byName["sub"].Dirty {
		t.Errorf("sub node = %+v, want dir with Dirty=true", byName["sub"])
	}
}

func TestGitDiff(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)

	code, body := get(t, s, "/api/diff?path=sub/mod.go")
	if code != 200 {
		t.Fatalf("diff status %d", code)
	}
	if body["available"] != true {
		t.Fatalf("available = %v, want true (%v)", body["available"], body)
	}
	diff := body["diff"].(string)
	if !strings.Contains(diff, "-line one") || !strings.Contains(diff, "+line two") {
		t.Errorf("diff missing expected +/- lines:\n%s", diff)
	}

	// A clean, committed file yields no diff -> available:false.
	_, body = get(t, s, "/api/diff?path=keep.go")
	if body["available"] != false || body["diff"] != "" {
		t.Errorf("clean file: available=%v diff=%q, want false/empty", body["available"], body["diff"])
	}

	// Meta reports git availability and git changes.
	_, meta := get(t, s, "/api/meta")
	if meta["git"] != true {
		t.Errorf("meta git = %v, want true", meta["git"])
	}
	if changes, ok := meta["gitChanges"].(float64); !ok || changes < 1 {
		t.Errorf("meta gitChanges = %v, want >= 1", meta["gitChanges"])
	}
	files, ok := meta["gitFiles"].([]any)
	found := false
	for _, f := range files {
		if f == "sub/mod.go" {
			found = true
			break
		}
	}
	if !ok || !found {
		t.Errorf("meta gitFiles = %v, want to contain sub/mod.go", meta["gitFiles"])
	}
}

func TestGitCleanRepo(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	runCmd("init")
	runCmd("config", "user.email", "test@test.com")
	runCmd("config", "user.name", "test")
	runCmd("config", "core.hooksPath", "/dev/null")
	if err := os.WriteFile(filepath.Join(dir, "clean.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd("add", "clean.go")
	runCmd("commit", "-m", "test: init")

	ix := NewIndex(dir)
	ix.Build()
	s := NewServer(ix, nil)

	_, meta := get(t, s, "/api/meta")
	if meta["git"] != true {
		t.Errorf("git = %v, want true", meta["git"])
	}
	if changes, ok := meta["gitChanges"].(float64); !ok || changes != 0 {
		t.Errorf("gitChanges = %v, want 0", meta["gitChanges"])
	}
	files, ok := meta["gitFiles"].([]any)
	if !ok || len(files) != 0 {
		t.Errorf("gitFiles = %v, want []", meta["gitFiles"])
	}
}

func TestGitDisabled(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	gitDisabled = true
	defer func() { gitDisabled = false }()

	root := gitRepo(t)
	if gitAvailable(root) {
		t.Fatal("gitAvailable true with -no-git")
	}
	if st := gitStatus(root); st != nil {
		t.Errorf("gitStatus returned %v with -no-git", st)
	}
	ix := NewIndex(root)
	ix.Build()
	kids, _ := ix.Children("sub")
	for _, k := range kids {
		if k.Status != "" {
			t.Errorf("node %q has status %q with -no-git", k.Name, k.Status)
		}
	}
	s := NewServer(ix, nil)
	_, body := get(t, s, "/api/diff?path=sub/mod.go")
	if body["available"] != false {
		t.Errorf("diff available = %v with -no-git, want false", body["available"])
	}
	_, meta := get(t, s, "/api/meta")
	if meta["git"] != false {
		t.Errorf("meta git = %v with -no-git, want false", meta["git"])
	}
	if meta["gitChanges"].(float64) != 0 {
		t.Errorf("meta gitChanges = %v with -no-git, want 0", meta["gitChanges"])
	}
}

func TestGitGutter(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Committed baseline.
	write("f.go", "alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\ngolf\nhotel\nindia\njuliet\n")
	write("clean.go", "stable\n")
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("add", "-A")
	run("commit", "-qm", "init")
	// Dirty f.go: replace line 2 (modify), insert a line before echo (pure add),
	// remove golf (pure delete). Leave clean.go untouched, add an untracked file.
	write("f.go", "alpha\nBRAVO\ncharlie\ndelta\nNEWLINE1\necho\nfoxtrot\nhotel\nindia\njuliet\n")
	write("untr.go", "new\n")

	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)

	ints := func(body map[string]any, key string) []int {
		var out []int
		for _, v := range body[key].([]any) {
			out = append(out, int(v.(float64)))
		}
		return out
	}

	code, body := get(t, s, "/api/gutter?path=f.go")
	if code != 200 {
		t.Fatalf("gutter status %d", code)
	}
	if body["available"] != true {
		t.Fatalf("available = %v, want true (%v)", body["available"], body)
	}
	if got := ints(body, "modified"); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("modified = %v, want [2]", got)
	}
	if got := ints(body, "added"); !reflect.DeepEqual(got, []int{5}) {
		t.Errorf("added = %v, want [5]", got)
	}
	if got := ints(body, "deleted"); !reflect.DeepEqual(got, []int{7}) {
		t.Errorf("deleted = %v, want [7] (marker on new line just before removed run)", got)
	}

	// A clean committed file: 200, available:false, empty (non-null) arrays.
	_, body = get(t, s, "/api/gutter?path=clean.go")
	if body["available"] != false {
		t.Errorf("clean file available = %v, want false", body["available"])
	}
	for _, key := range []string{"added", "modified", "deleted"} {
		if got := ints(body, key); len(got) != 0 {
			t.Errorf("clean file %s = %v, want empty", key, got)
		}
	}

	// An untracked file has no diff against HEAD -> available:false, never 500.
	code, body = get(t, s, "/api/gutter?path=untr.go")
	if code != 200 || body["available"] != false {
		t.Errorf("untracked: status=%d available=%v, want 200/false", code, body["available"])
	}

	// Verify /api/file also reports diffAvailable immediately on file load.
	_, fileBody := get(t, s, "/api/file?path=f.go")
	if fileBody["diffAvailable"] != true {
		t.Errorf("f.go diffAvailable = %v, want true", fileBody["diffAvailable"])
	}
	_, cleanBody := get(t, s, "/api/file?path=clean.go")
	if cleanBody["diffAvailable"] != false {
		t.Errorf("clean.go diffAvailable = %v, want false", cleanBody["diffAvailable"])
	}
}

func BenchmarkGitStatus(b *testing.B) {
	if !gitInstalled() {
		b.Skip("git not installed")
	}
	root := gitRepo(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gitStatus(root)
	}
}

func TestUpdateGitStatus(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	ix := NewIndex(root)
	ix.Build()

	count, files, changed, statuses, dirtyDirs, _, _, _ := ix.UpdateGitStatus()
	// Should be unchanged because Build() just ran
	if changed {
		t.Errorf("expected changed=false immediately after Build(), got true")
	}
	if count == 0 || len(files) == 0 {
		t.Errorf("expected non-zero git changes, got count=%d, files=%v", count, files)
	}
	if !dirtyDirs["sub"] {
		t.Errorf("expected 'sub' to be in dirtyDirs, got %v", dirtyDirs)
	}
	if statuses["add.go"] != "A" {
		t.Errorf("expected add.go status 'A', got %q", statuses["add.go"])
	}

	// Now modify another file
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	count2, _, changed2, statuses2, _, _, _, _ := ix.UpdateGitStatus()
	if !changed2 {
		t.Errorf("expected changed=true after modifying keep.go")
	}
	if count2 <= count {
		t.Errorf("expected count to increase, got count2=%d vs count=%d", count2, count)
	}
	if statuses2["keep.go"] != "M" {
		t.Errorf("expected keep.go to have status 'M', got %q", statuses2["keep.go"])
	}

	// Calling it again without changes should report changed=false
	_, _, changed3, _, _, _, _, _ := ix.UpdateGitStatus()
	if changed3 {
		t.Errorf("expected changed=false when worktree has not changed")
	}
}

func TestGitWatcherStreamAndRefresh(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	ix := NewIndex(root)
	ix.Build()

	s := NewServer(ix, nil)
	ts := httptest.NewServer(s.mux)
	defer ts.Close()

	// 1. Connect to SSE stream
	req, err := http.NewRequest("GET", ts.URL+"/api/git/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	readSSEEvent := func() (string, map[string]any) {
		var eventName string
		var data string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("failed reading SSE stream: %v", err)
			}
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			} else if line == "" && data != "" {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(data), &parsed); err != nil {
					t.Fatalf("malformed json in SSE event: %v", err)
				}
				return eventName, parsed
			}
		}
	}

	// Initial status should be sent immediately upon connection
	evName, initialData := readSSEEvent()
	if evName != "git-status" {
		t.Errorf("expected event 'git-status', got %q", evName)
	}
	if initialData["git"] != true {
		t.Errorf("expected git=true, got %v", initialData["git"])
	}

	// 2. Modify a file and call POST /api/git/refresh
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("streamed change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	refreshResp, err := http.Post(ts.URL+"/api/git/refresh", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var refreshData map[string]any
	json.NewDecoder(refreshResp.Body).Decode(&refreshData)
	refreshResp.Body.Close()

	if refreshData["git"] != true {
		t.Errorf("refresh expected git=true, got %v", refreshData["git"])
	}
	statuses, ok := refreshData["statuses"].(map[string]any)
	if !ok || statuses["keep.go"] != "M" {
		t.Errorf("expected keep.go 'M' in refresh response, got %v", refreshData)
	}

	// 3. SSE stream must receive the broadcasted update
	done := make(chan struct{})
	var streamedEv string
	var streamedData map[string]any
	go func() {
		streamedEv, streamedData = readSSEEvent()
		close(done)
	}()

	select {
	case <-done:
		if streamedEv != "git-status" {
			t.Errorf("expected event 'git-status', got %q", streamedEv)
		}
		streamedStatuses, _ := streamedData["statuses"].(map[string]any)
		if streamedStatuses["keep.go"] != "M" {
			t.Errorf("expected keep.go 'M' on stream, got %v", streamedData)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SSE update event")
	}
}

func TestGitWatcherCLICommitDetection(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	ix := NewIndex(root)
	ix.Build()

	gw := NewGitWatcher(ix)
	gitdir := gitDir(root)
	if !gw.recordGitMeta(gitdir) {
		// First recording initialized the metadata
	}

	// Commit existing changes via CLI
	cmd := exec.Command("git", "commit", "-am", "commit all")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}

	// Watcher's metadata stat check should instantly see the index/HEAD modtime update
	if !gw.recordGitMeta(gitdir) {
		t.Errorf("expected recordGitMeta to report changed=true after CLI commit")
	}
}

func TestGitWatcherAfterCommitNewChanges(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	ix := NewIndex(root)
	ix.Build()

	s := NewServer(ix, nil)
	ts := httptest.NewServer(s.mux)
	defer ts.Close()

	// 1. Commit everything so working tree is clean
	cmdAdd := exec.Command("git", "add", "-A")
	cmdAdd.Dir = root
	cmdAdd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v\n%s", err, out)
	}
	cmd := exec.Command("git", "commit", "-m", "commit all")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}

	count, _, changed, statuses, _, _, _, _ := ix.UpdateGitStatus()
	if count != 0 || len(statuses) != 0 {
		t.Fatalf("expected clean worktree after commit, got count=%d statuses=%v", count, statuses)
	}
	_ = changed

	// 2. Add a brand new untracked file and modify an existing file
	if err := os.WriteFile(filepath.Join(root, "brand_new.txt"), []byte("new file content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("modified keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. UpdateGitStatus should detect both and inject brand_new.txt into children
	count2, files2, changed2, statuses2, _, _, _, _ := ix.UpdateGitStatus()
	if !changed2 {
		t.Errorf("expected changed=true after modifying worktree")
	}
	if count2 != 2 {
		t.Errorf("expected count=2, got %d (files: %v)", count2, files2)
	}
	if statuses2["brand_new.txt"] != "U" {
		t.Errorf("expected brand_new.txt to have status 'U', got %q", statuses2["brand_new.txt"])
	}
	if statuses2["keep.go"] != "M" {
		t.Errorf("expected keep.go to have status 'M', got %q", statuses2["keep.go"])
	}

	// Verify ix.Children("") actually contains brand_new.txt
	kids, ok := ix.Children("")
	if !ok {
		t.Fatal("expected Children(\"\") to return true")
	}
	foundBrandNew := false
	for _, k := range kids {
		if k.Name == "brand_new.txt" && !k.Dir && k.Status == "U" {
			foundBrandNew = true
			break
		}
	}
	if !foundBrandNew {
		t.Errorf("expected Children(\"\") to contain brand_new.txt with status 'U'")
	}

	// 4. Connect to SSE stream and verify Subscribe sends gitChanges: 2
	req, err := http.NewRequest("GET", ts.URL+"/api/git/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	var initialData map[string]any
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed reading SSE stream: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(data), &initialData); err == nil && initialData["git"] == true {
				break
			}
		}
	}

	if gChanges, ok := initialData["gitChanges"].(float64); !ok || int(gChanges) != 2 {
		t.Errorf("expected Subscribe initial data gitChanges=2, got %v", initialData["gitChanges"])
	}
}

func TestGitStatusAgainstAndPRDiff(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("foo.go", "package main\n\nfunc Foo() int { return 1 }\n")
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("add", "-A")
	run("commit", "-m", "initial commit")
	baseSHA := run("rev-parse", "HEAD")

	// Commit a PR change: modifies foo.go
	write("foo.go", "package main\n\nfunc Foo() int { return 2 }\n")
	run("commit", "-am", "pr commit")

	// Working tree is clean relative to HEAD
	stHead := gitStatus(root)
	if stHead != nil && stHead["foo.go"] != "" {
		t.Fatalf("expected gitStatus(root) to be clean, got: %v", stHead)
	}

	// But gitStatusAgainst baseSHA must report foo.go as "M"
	stBase := gitStatusAgainst(root, baseSHA)
	if stBase == nil || stBase["foo.go"] != "M" {
		t.Fatalf("expected gitStatusAgainst(root, baseSHA) to report foo.go as M, got: %v", stBase)
	}

	// Index should also pick up the diffBase
	ix := NewIndex(root)
	ix.SetDiffBase(baseSHA)
	ix.Build()

	count, files, _, statuses, _, _, _, _ := ix.UpdateGitStatus()
	if count == 0 || statuses["foo.go"] != "M" {
		t.Fatalf("expected Index to report foo.go as M against diffBase, got count=%d statuses=%v files=%v", count, statuses, files)
	}

	// Server should mark diffAvailable=true for foo.go
	srv := NewServer(ix, newLSPManager(root, false))
	srv.diffBase = baseSHA

	req := httptest.NewRequest("GET", "/api/file?path=foo.go", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var fileResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fileResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if diffAvail, ok := fileResp["diffAvailable"].(bool); !ok || !diffAvail {
		t.Errorf("expected diffAvailable=true for foo.go against baseSHA, got %v", fileResp["diffAvailable"])
	}

	// /api/diff should return the diff against baseSHA
	diffReq := httptest.NewRequest("GET", "/api/diff?path=foo.go", nil)
	dw := httptest.NewRecorder()
	srv.ServeHTTP(dw, diffReq)
	if dw.Code != 200 {
		t.Fatalf("expected 200 from /api/diff, got %d: %s", dw.Code, dw.Body.String())
	}
	diffBody := dw.Body.String()
	if !strings.Contains(diffBody, "-func Foo() int { return 1 }") || !strings.Contains(diffBody, "+func Foo() int { return 2 }") {
		t.Errorf("unexpected diff against baseSHA:\n%s", diffBody)
	}

	// Now simulate an external terminal agent modifying foo.go
	write("foo.go", "package main\n\nfunc Foo() int { return 99 }\n")

	// ix.UpdateGitStatus() must catch the modification
	_, _, changed, statuses, _, _, _, _ := ix.UpdateGitStatus()
	if !changed && statuses["foo.go"] != "M" {
		t.Errorf("expected UpdateGitStatus to report foo.go as changed/M, got changed=%v statuses=%v", changed, statuses)
	}

	// Server /api/diff against baseSHA must reflect the external edit in real time
	diffReq2 := httptest.NewRequest("GET", "/api/diff?path=foo.go", nil)
	dw2 := httptest.NewRecorder()
	srv.ServeHTTP(dw2, diffReq2)
	if dw2.Code != 200 {
		t.Fatalf("expected 200 from /api/diff, got %d: %s", dw2.Code, dw2.Body.String())
	}
	diffBody2 := dw2.Body.String()
	if !strings.Contains(diffBody2, "-func Foo() int { return 1 }") || !strings.Contains(diffBody2, "+func Foo() int { return 99 }") {
		t.Errorf("unexpected diff against baseSHA after external edit:\n%s", diffBody2)
	}
}

// TestHandleDiffPRSplitsPRAndYourChanges is the regression test for the
// git-panel commit UX bug: in a PR review session, /api/diff used to return
// one diff (mergeBase..working-tree) that looked identical before and after
// the reviewer committed, since committing doesn't touch file contents.
// handleDiff now also splits the same range into prDiff (mergeBase..PR head,
// frozen) and yourDiff (PR head..working tree, what a local commit actually
// changes), so the two never conflate the author's diff with the reviewer's.
func TestHandleDiffPRSplitsPRAndYourChanges(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("foo.go", "package main\n\nfunc Foo() int { return 1 }\n")
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("add", "-A")
	run("commit", "-m", "initial commit")
	mergeBase := run("rev-parse", "HEAD")

	// The PR's own change, baked into history like a real checked-out PR head.
	write("foo.go", "package main\n\nfunc Foo() int { return 2 }\n")
	run("commit", "-am", "pr commit")
	prHead := run("rev-parse", "HEAD")

	ix := NewIndex(root)
	ix.Build()
	srv := NewServer(ix, newLSPManager(root, false))
	srv.SetPR(&prSession{
		target: PRTarget{Owner: "o", Repo: "r"},
		meta:   PRMeta{Number: 1, BaseRef: "main", HeadRef: "feature", HeadSHA: prHead},
		// checkoutPR would have set this to the real merge-base; a plain
		// commit SHA works identically as a diff boundary in this test.
		diffBase: mergeBase,
	})

	getDiff := func() map[string]any {
		req := httptest.NewRequest("GET", "/api/diff?path=foo.go", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("expected 200 from /api/diff, got %d: %s", w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		return resp
	}

	// Before any local edit: prDiff carries the PR's own change, yourDiff is empty.
	resp := getDiff()
	prDiff, _ := resp["prDiff"].(string)
	yourDiff, _ := resp["yourDiff"].(string)
	if !strings.Contains(prDiff, "-func Foo() int { return 1 }") || !strings.Contains(prDiff, "+func Foo() int { return 2 }") {
		t.Errorf("expected prDiff to contain the PR's own change, got:\n%s", prDiff)
	}
	if strings.TrimSpace(yourDiff) != "" {
		t.Errorf("expected yourDiff to be empty before any local edit, got:\n%s", yourDiff)
	}

	// Reviewer edits and commits in the worktree -- the exact action the bug
	// report was about. prDiff must stay byte-for-byte frozen; yourDiff must
	// pick up exactly the reviewer's commit, and only that.
	write("foo.go", "package main\n\nfunc Foo() int { return 99 }\n")
	run("commit", "-am", "reviewer's local commit")

	resp2 := getDiff()
	prDiff2, _ := resp2["prDiff"].(string)
	yourDiff2, _ := resp2["yourDiff"].(string)
	if prDiff2 != prDiff {
		t.Errorf("expected prDiff to stay frozen across the reviewer's commit, before:\n%s\nafter:\n%s", prDiff, prDiff2)
	}
	if !strings.Contains(yourDiff2, "-func Foo() int { return 2 }") || !strings.Contains(yourDiff2, "+func Foo() int { return 99 }") {
		t.Errorf("expected yourDiff to show the reviewer's commit, got:\n%s", yourDiff2)
	}
}

func TestGitStagedPaths(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	staged := gitStagedPaths(root)
	for _, p := range []string{"add.go", "sub/ren2.go"} {
		if !staged[p] {
			t.Errorf("expected %q to be staged, got %v", p, staged)
		}
	}
	for _, p := range []string{"sub/mod.go", "del.go", "untr.go"} {
		if staged[p] {
			t.Errorf("expected %q to not be staged, got %v", p, staged)
		}
	}
}

func TestGitStageUnstageCommit(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)

	// gitRepo leaves add.go staged, and sub/ren.go -> sub/ren2.go staged as a
	// rename (both the old and new path are index entries). Unstage all three
	// so this test controls what's staged from here.
	for _, p := range []string{"add.go", "sub/ren.go", "sub/ren2.go"} {
		if err := gitUnstage(root, p); err != nil {
			t.Fatalf("gitUnstage %s: %v", p, err)
		}
	}
	if staged := gitStagedPaths(root); len(staged) != 0 {
		t.Fatalf("expected nothing staged after unstaging, got %v", staged)
	}

	if err := gitCommit(root, "should fail"); err == nil {
		t.Fatal("expected gitCommit to fail with nothing staged")
	}

	if err := gitStage(root, "add.go"); err != nil {
		t.Fatalf("gitStage add.go: %v", err)
	}
	if staged := gitStagedPaths(root); !staged["add.go"] {
		t.Fatalf("expected add.go staged after gitStage, got %v", staged)
	}

	if err := gitCommit(root, "test: commit add.go"); err != nil {
		t.Fatalf("gitCommit: %v", err)
	}
	if staged := gitStagedPaths(root); staged["add.go"] {
		t.Fatalf("expected add.go no longer staged after commit, got %v", staged)
	}
	if !gitHasUncommittedChanges(root) {
		t.Fatal("expected the repo's other dirty files to still show uncommitted changes")
	}
}

// gitTestRun runs a git command in dir with a hermetic config, failing the
// test on error. Mirrors gitRepo's own run() closure for tests that need
// more than one working tree.
func gitTestRun(tb testing.TB, dir string, args ...string) string {
	tb.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %v (in %s): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// TestGitFFOnlyPull exercises gitFFOnlyPull against a bare "remote" shared by
// two clones: a clean fast-forward must succeed, and a diverged history (a
// local commit in cloneB that the remote doesn't have) must be refused with
// errNotFastForward, leaving cloneB's tree untouched.
func TestGitFFOnlyPull(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	remote := filepath.Join(base, "remote.git")
	cloneA := filepath.Join(base, "a")
	cloneB := filepath.Join(base, "b")

	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, remote, "init", "--bare", "-b", "main")

	gitTestRun(t, base, "clone", remote, "a")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, cloneA, "config", cfg[0], cfg[1])
	}
	if err := os.WriteFile(filepath.Join(cloneA, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, cloneA, "add", "f.txt")
	gitTestRun(t, cloneA, "commit", "-qm", "init")
	gitTestRun(t, cloneA, "push", "origin", "main")

	gitTestRun(t, base, "clone", remote, "b")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, cloneB, "config", cfg[0], cfg[1])
	}

	// cloneA pushes a second commit; cloneB fast-forward-pulls it cleanly.
	if err := os.WriteFile(filepath.Join(cloneA, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, cloneA, "commit", "-aqm", "second")
	gitTestRun(t, cloneA, "push", "origin", "main")

	if err := gitFFOnlyPull(cloneB, "origin", "main"); err != nil {
		t.Fatalf("expected a clean fast-forward pull, got %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(cloneB, "f.txt")); err != nil || string(got) != "two\n" {
		t.Fatalf("expected cloneB to fast-forward to %q, got %q, err=%v", "two\n", got, err)
	}

	// cloneB commits locally without pushing, then cloneA pushes again:
	// cloneB can no longer fast-forward and must refuse rather than merge.
	if err := os.WriteFile(filepath.Join(cloneB, "g.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, cloneB, "add", "g.txt")
	gitTestRun(t, cloneB, "commit", "-qm", "local only")

	if err := os.WriteFile(filepath.Join(cloneA, "f.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, cloneA, "commit", "-aqm", "third")
	gitTestRun(t, cloneA, "push", "origin", "main")

	if err := gitFFOnlyPull(cloneB, "origin", "main"); !errors.Is(err, errNotFastForward) {
		t.Fatalf("expected errNotFastForward for a diverged pull, got %v", err)
	}
	// Must not have touched cloneB's working tree on refusal.
	if got, err := os.ReadFile(filepath.Join(cloneB, "f.txt")); err != nil || string(got) != "two\n" {
		t.Fatalf("expected cloneB's f.txt untouched by the refused pull, got %q, err=%v", got, err)
	}
}

func TestGitRecentCommitsAndLog(t *testing.T) {
	dir := t.TempDir()
	gitTestRun(t, dir, "init", "-b", "main")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, dir, "config", cfg[0], cfg[1])
	}

	// Initially no commits
	commits := gitRecentCommits(dir, 5)
	if len(commits) != 0 {
		t.Fatalf("expected 0 commits in fresh repo, got %d", len(commits))
	}

	// Make 3 commits
	for i := 1; i <= 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(p, []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitTestRun(t, dir, "add", p)
		gitTestRun(t, dir, "commit", "-m", fmt.Sprintf("commit %d", i))
	}

	commits = gitRecentCommits(dir, 5)
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	if commits[0].Subject != "commit 3" {
		t.Fatalf("expected latest commit to be 'commit 3', got %q", commits[0].Subject)
	}

	// Test /api/git/log endpoint
	ix := NewIndex(dir)
	ix.Build()
	s := NewServer(ix, nil)
	ts := httptest.NewServer(s.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/git/log?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload struct {
		Commits    []GitCommit `json:"commits"`
		CommitsURL string      `json:"commitsUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Commits) != 2 {
		t.Fatalf("expected 2 commits from /api/git/log?limit=2, got %d", len(payload.Commits))
	}
	if payload.Commits[0].Subject != "commit 3" {
		t.Fatalf("expected 'commit 3', got %q", payload.Commits[0].Subject)
	}

	// Test gitCommitsWebURL with github origin
	gitTestRun(t, dir, "remote", "add", "origin", "git@github.com:alice/my-repo.git")
	commitsWebURL := gitCommitsWebURL(dir, "master")
	if !strings.Contains(commitsWebURL, "github.com/alice/my-repo/commits") {
		t.Fatalf("expected github commits URL, got %q", commitsWebURL)
	}
}

func TestGitAheadBehind(t *testing.T) {
	// Create bare remote repository
	remoteDir := t.TempDir()
	gitTestRun(t, remoteDir, "init", "--bare")

	// Create local repository
	localDir := t.TempDir()
	gitTestRun(t, localDir, "init")
	gitTestRun(t, localDir, "config", "user.email", "alice@example.com")
	gitTestRun(t, localDir, "config", "user.name", "Alice")
	gitTestRun(t, localDir, "checkout", "-b", "main")

	// Commit 1
	os.WriteFile(filepath.Join(localDir, "a.txt"), []byte("hello"), 0o644)
	gitTestRun(t, localDir, "add", "a.txt")
	gitTestRun(t, localDir, "commit", "-m", "init")

	// Add remote and push with upstream
	gitTestRun(t, localDir, "remote", "add", "origin", remoteDir)
	gitTestRun(t, localDir, "push", "-u", "origin", "main")

	// Initially in sync: ahead=0, behind=0
	ahead, behind, hasUpstream := gitAheadBehind(localDir)
	if !hasUpstream {
		t.Fatalf("expected hasUpstream=true")
	}
	if ahead != 0 || behind != 0 {
		t.Fatalf("expected ahead=0 behind=0, got ahead=%d behind=%d", ahead, behind)
	}

	// Make a new commit locally
	os.WriteFile(filepath.Join(localDir, "a.txt"), []byte("hello 2"), 0o644)
	gitTestRun(t, localDir, "add", "a.txt")
	gitTestRun(t, localDir, "commit", "-m", "update 1")

	// Now ahead=1, behind=0
	ahead, behind, _ = gitAheadBehind(localDir)
	if ahead != 1 || behind != 0 {
		t.Fatalf("expected ahead=1 behind=0, got ahead=%d behind=%d", ahead, behind)
	}

	// Push changes
	gitTestRun(t, localDir, "push")

	// Back in sync: ahead=0
	ahead, behind, _ = gitAheadBehind(localDir)
	if ahead != 0 || behind != 0 {
		t.Fatalf("expected ahead=0 behind=0 after push, got ahead=%d behind=%d", ahead, behind)
	}
}

func TestPRReviewerChangesInIndex(t *testing.T) {
	if !gitAvailable(".") {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	gitTestRun(t, root, "init", "-b", "main")
	gitTestRun(t, root, "config", "user.name", "test")
	gitTestRun(t, root, "config", "user.email", "test@example.com")

	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "foo.go"), []byte("package pkg\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, root, "add", ".")
	gitTestRun(t, root, "commit", "-m", "base commit")
	baseSHA := strings.TrimSpace(gitTestRun(t, root, "rev-parse", "HEAD"))

	// PR changes foo.go
	if err := os.WriteFile(filepath.Join(root, "pkg", "foo.go"), []byte("package pkg\nfunc A() {}\nfunc PR() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, root, "add", ".")
	gitTestRun(t, root, "commit", "-m", "pr head commit")
	prHeadSHA := strings.TrimSpace(gitTestRun(t, root, "rev-parse", "HEAD"))

	ix := NewIndex(root)
	ix.SetDiffBase(baseSHA)
	ix.SetPRHead(prHeadSHA)
	ix.Build()

	// Initially, working tree matches prHeadSHA:
	// Statuses should report foo.go as M (against diffBase), but yourStatuses must be empty.
	count, files, _, statuses, _, _, yourStatuses, yourDirtyDirs := ix.UpdateGitStatus()
	if count != 1 || len(files) != 1 || statuses["pkg/foo.go"] != "M" {
		t.Fatalf("expected 1 file in PR diff, got statuses=%v", statuses)
	}
	if len(yourStatuses) != 0 || len(yourDirtyDirs) != 0 {
		t.Fatalf("expected empty yourStatuses initially, got %v", yourStatuses)
	}

	// Now reviewer edits foo.go
	if err := os.WriteFile(filepath.Join(root, "pkg", "foo.go"), []byte("package pkg\nfunc A() {}\nfunc PR() {}\nfunc Reviewer() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, changed, statuses, _, _, yourStatuses, yourDirtyDirs := ix.UpdateGitStatus()
	if !changed {
		t.Errorf("expected changed=true after reviewer modification")
	}
	if statuses["pkg/foo.go"] != "M" {
		t.Errorf("expected statuses[pkg/foo.go] = M, got %q", statuses["pkg/foo.go"])
	}
	if yourStatuses["pkg/foo.go"] != "M" {
		t.Errorf("expected yourStatuses[pkg/foo.go] = M, got %q", yourStatuses["pkg/foo.go"])
	}
	if !yourDirtyDirs["pkg"] {
		t.Errorf("expected yourDirtyDirs[pkg] = true, got %v", yourDirtyDirs)
	}

	// Check ix.Children() to verify Node fields
	kids, ok := ix.Children("pkg")
	if !ok || len(kids) != 1 {
		t.Fatalf("expected 1 child in pkg, got %v", kids)
	}
	if kids[0].YourStatus != "M" {
		t.Errorf("expected node YourStatus=M, got %q", kids[0].YourStatus)
	}

	// Now reviewer reverts all changes (restore to prHeadSHA)
	gitTestRun(t, root, "checkout", "--", ".")

	_, _, changed2, statuses, _, _, yourStatuses, yourDirtyDirs := ix.UpdateGitStatus()
	if !changed2 {
		t.Errorf("expected changed=true after revert")
	}
	if statuses["pkg/foo.go"] != "M" {
		t.Errorf("expected PR change to remain in statuses, got %v", statuses)
	}
	if len(yourStatuses) != 0 {
		t.Errorf("expected yourStatuses to be empty after revert, got %v", yourStatuses)
	}
	if len(yourDirtyDirs) != 0 {
		t.Errorf("expected yourDirtyDirs to be empty after revert, got %v", yourDirtyDirs)
	}

	kids2, _ := ix.Children("pkg")
	if len(kids2) > 0 && kids2[0].YourStatus != "" {
		t.Errorf("expected node YourStatus to be cleared after revert, got %q", kids2[0].YourStatus)
	}
}

func TestGitStagedFilesStatAndDiff(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitTestRun(t, dir, "init", "-b", "main")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, dir, "config", cfg[0], cfg[1])
	}
	// Initial commit
	os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init\n"), 0o644)
	gitTestRun(t, dir, "add", "init.txt")
	gitTestRun(t, dir, "commit", "-m", "init")

	// Stage a code file and a lockfile
	os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte("{\n  \"name\": \"dummy-lockfile\",\n  \"version\": \"1.0.0\"\n}\n"), 0o644)
	gitTestRun(t, dir, "add", "app.go", "package-lock.json")

	files := gitStagedFiles(dir)
	if len(files) != 2 {
		t.Fatalf("expected 2 staged files, got %d: %v", len(files), files)
	}
	hasApp := false
	hasLock := false
	for _, f := range files {
		if f == "app.go" {
			hasApp = true
		}
		if f == "package-lock.json" {
			hasLock = true
		}
	}
	if !hasApp || !hasLock {
		t.Fatalf("staged files missing expected entries: %v", files)
	}

	stat := gitStagedStat(dir)
	if !strings.Contains(stat, "app.go") || !strings.Contains(stat, "package-lock.json") {
		t.Fatalf("diffstat missing expected files:\n%s", stat)
	}

	// Staged diff should exclude package-lock.json because app.go has changes
	diff := gitStagedDiff(dir)
	if !strings.Contains(diff, "app.go") {
		t.Fatalf("diff expected to contain app.go diff:\n%s", diff)
	}
	if strings.Contains(diff, "package-lock.json") {
		t.Fatalf("diff expected to exclude package-lock.json when other changes exist:\n%s", diff)
	}

	// Commit app.go, leaving only package-lock.json staged: should fall back to showing package-lock.json
	gitTestRun(t, dir, "commit", "-m", "commit app.go", "app.go")
	diffOnlyLock := gitStagedDiff(dir)
	if !strings.Contains(diffOnlyLock, "package-lock.json") {
		t.Fatalf("diff expected to fallback to package-lock.json when only lockfiles staged:\n%s", diffOnlyLock)
	}
}

func TestGitStagedDiffTruncation(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitTestRun(t, dir, "init", "-b", "main")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, dir, "config", cfg[0], cfg[1])
	}
	os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init\n"), 0o644)
	gitTestRun(t, dir, "add", "init.txt")
	gitTestRun(t, dir, "commit", "-m", "init")

	// Write a 50 KB file (> 32 KB limit)
	var large bytes.Buffer
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&large, "line %04d: lots of content to make the diff exceed the 32KB cap\n", i)
	}
	os.WriteFile(filepath.Join(dir, "large.txt"), large.Bytes(), 0o644)
	gitTestRun(t, dir, "add", "large.txt")

	diff := gitStagedDiff(dir)
	if !strings.Contains(diff, "[Diff truncated: showing first 32KB") {
		t.Fatalf("expected diff to be truncated with notice, got %d bytes without notice", len(diff))
	}
	// The diff output should be around 32KB + truncation message
	if len(diff) > 34*1024 {
		t.Fatalf("diff size %d exceeded expected bound", len(diff))
	}
}

func TestDeletedFileHandling(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitTestRun(t, dir, "init", "-b", "main")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, dir, "config", cfg[0], cfg[1])
	}
	// Create root file and nested file
	os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root file content\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub", "inner"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "inner", "nested.txt"), []byte("nested file content\n"), 0o644)
	gitTestRun(t, dir, "add", "-A")
	gitTestRun(t, dir, "commit", "-m", "initial commit")

	// Delete root.txt and the entire sub directory from working tree
	if err := os.Remove(filepath.Join(dir, "root.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}

	// 1. Verify Index.Build includes root.txt and sub/inner/nested.txt as "D"
	ix := NewIndex(dir)
	ix.Build()

	rootKids, ok := ix.Children("")
	if !ok {
		t.Fatal("ix.Children(\"\") failed")
	}
	var rootNode, subDirNode *Node
	for i := range rootKids {
		if rootKids[i].Name == "root.txt" {
			rootNode = &rootKids[i]
		}
		if rootKids[i].Name == "sub" {
			subDirNode = &rootKids[i]
		}
	}
	if rootNode == nil {
		t.Fatal("expected root.txt to be present in ix.Children(\"\")")
	}
	if rootNode.Dir || rootNode.Status != "D" {
		t.Fatalf("expected root.txt to have Dir=false, Status=\"D\", got %+v", rootNode)
	}
	if subDirNode == nil || !subDirNode.Dir || !subDirNode.Dirty {
		t.Fatalf("expected sub to be Dir=true and Dirty=true, got %+v", subDirNode)
	}

	subKids, ok := ix.Children("sub")
	if !ok {
		t.Fatal("ix.Children(\"sub\") failed")
	}
	var innerDirNode *Node
	for i := range subKids {
		if subKids[i].Name == "inner" {
			innerDirNode = &subKids[i]
		}
	}
	if innerDirNode == nil || !innerDirNode.Dir || !innerDirNode.Dirty {
		t.Fatalf("expected sub/inner to be Dir=true and Dirty=true, got %+v", innerDirNode)
	}

	innerKids, ok := ix.Children("sub/inner")
	if !ok {
		t.Fatal("ix.Children(\"sub/inner\") failed")
	}
	var nestedNode *Node
	for i := range innerKids {
		if innerKids[i].Name == "nested.txt" {
			nestedNode = &innerKids[i]
		}
	}
	if nestedNode == nil || nestedNode.Dir || nestedNode.Status != "D" {
		t.Fatalf("expected sub/inner/nested.txt to have Status=\"D\", got %+v", nestedNode)
	}

	// 2. Test /api/file and /api/diff endpoints for deleted file
	srv := NewServer(ix, newLSPManager(ix.Root(), false))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// /api/file for deleted root.txt
	resp, err := http.Get(ts.URL + "/api/file?path=root.txt")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/file?path=root.txt returned status %d, want 200", resp.StatusCode)
	}
	var fileData map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&fileData); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if fileData["deleted"] != true {
		t.Fatalf("expected deleted=true, got %v", fileData["deleted"])
	}
	if fileData["diffAvailable"] != true {
		t.Fatalf("expected diffAvailable=true, got %v", fileData["diffAvailable"])
	}
	if fileData["total"] != float64(0) {
		t.Fatalf("expected total=0, got %v", fileData["total"])
	}

	// /api/diff for deleted root.txt
	diffResp, err := http.Get(ts.URL + "/api/diff?path=root.txt")
	if err != nil {
		t.Fatal(err)
	}
	if diffResp.StatusCode != http.StatusOK {
		t.Fatalf("/api/diff?path=root.txt returned status %d, want 200", diffResp.StatusCode)
	}
	var diffData map[string]any
	if err := json.NewDecoder(diffResp.Body).Decode(&diffData); err != nil {
		t.Fatal(err)
	}
	diffResp.Body.Close()
	if diffData["available"] != true {
		t.Fatalf("expected diff available=true, got %v", diffData["available"])
	}
	diffStr, _ := diffData["diff"].(string)
	if !strings.Contains(diffStr, "-root file content") {
		t.Fatalf("expected diff to show deleted line, got:\n%s", diffStr)
	}

	// 3. Test restoring a file (git checkout -- root.txt)
	gitTestRun(t, dir, "checkout", "--", "root.txt")
	_, _, changed, statuses, _, _, _, _ := ix.UpdateGitStatus()
	if !changed {
		t.Fatal("expected UpdateGitStatus to report changed=true on restore")
	}
	if statuses["root.txt"] != "" {
		t.Fatalf("expected root.txt status to be clean, got %q", statuses["root.txt"])
	}
	rootKidsAfter, _ := ix.Children("")
	for _, k := range rootKidsAfter {
		if k.Name == "root.txt" {
			if k.Status != "" {
				t.Fatalf("expected restored file to have Status=\"\", got %q", k.Status)
			}
		}
	}

	// 4. Test committing deletion of sub/inner/nested.txt
	gitTestRun(t, dir, "rm", "-rf", "sub")
	gitTestRun(t, dir, "commit", "-m", "remove sub")
	ix.UpdateGitStatus()

	// sub and sub/inner should now be removed from ix.children because they are empty and not on disk
	if _, ok := ix.Children("sub"); ok {
		t.Fatal("expected sub directory to be removed from children after commit")
	}
	rootKidsFinal, _ := ix.Children("")
	for _, k := range rootKidsFinal {
		if k.Name == "sub" {
			t.Fatal("expected sub entry to be removed from root children after commit")
		}
	}
}

// unpushedRepo builds a clone with an upstream and two unpushed commits on top
// of it: the first modifies one file, deletes another and adds a third; the
// second appends a line. Returns the clone's root.
func unpushedRepo(tb testing.TB) string {
	tb.Helper()
	base := tb.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	remote := filepath.Join(base, "remote.git")
	clone := filepath.Join(base, "work")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		tb.Fatal(err)
	}
	gitTestRun(tb, remote, "init", "--bare", "-b", "main")
	gitTestRun(tb, base, "clone", remote, "work")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(tb, clone, "config", cfg[0], cfg[1])
	}
	write := func(rel, body string) {
		tb.Helper()
		p := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			tb.Fatal(err)
		}
	}

	write("a.txt", "one\ntwo\nthree\n")
	write("gone.txt", "bye\n")
	gitTestRun(tb, clone, "add", "-A")
	gitTestRun(tb, clone, "commit", "-qm", "pushed commit")
	gitTestRun(tb, clone, "push", "-q", "-u", "origin", "main")

	write("a.txt", "one\nTWO\nthree\n")
	write("added.txt", "new\n")
	gitTestRun(tb, clone, "rm", "-q", "gone.txt")
	gitTestRun(tb, clone, "add", "-A")
	gitTestRun(tb, clone, "commit", "-qm", "local one\n\nBody of the first local commit.")

	write("a.txt", "one\nTWO\nthree\nfour\n")
	gitTestRun(tb, clone, "add", "-A")
	gitTestRun(tb, clone, "commit", "-qm", "local two")
	return clone
}

func TestGitUnpushedCommits(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := unpushedRepo(t)

	upstream, commits := gitUnpushedCommits(root, 0)
	if upstream != "origin/main" {
		t.Fatalf("expected upstream origin/main, got %q", upstream)
	}
	if len(commits) != 2 {
		t.Fatalf("expected 2 unpushed commits, got %d: %+v", len(commits), commits)
	}
	// Newest first, and the pushed commit must not be in the list.
	if commits[0].Subject != "local two" || commits[1].Subject != "local one" {
		t.Fatalf("expected [local two, local one], got %q, %q", commits[0].Subject, commits[1].Subject)
	}
	if len(commits[0].Hash) != 40 || commits[0].Short == "" || commits[0].Author != "T" || commits[0].Date == "" {
		t.Fatalf("incomplete commit record: %+v", commits[0])
	}

	// Pushing empties the list without touching the upstream name.
	gitTestRun(t, root, "push", "-q")
	upstream, commits = gitUnpushedCommits(root, 0)
	if upstream != "origin/main" || len(commits) != 0 {
		t.Fatalf("expected an empty list after push, got upstream=%q, %d commits", upstream, len(commits))
	}
}

func TestGitUnpushedNoUpstream(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	// A repo with commits but no tracking branch: no list, and no guess at one.
	dir := t.TempDir()
	gitTestRun(t, dir, "init", "-b", "main")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, dir, "config", cfg[0], cfg[1])
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, dir, "add", "-A")
	gitTestRun(t, dir, "commit", "-qm", "only commit")

	if ref := gitUpstreamRef(dir); ref != "" {
		t.Fatalf("expected no upstream ref, got %q", ref)
	}
	upstream, commits := gitUnpushedCommits(dir, 0)
	if upstream != "" || commits != nil {
		t.Fatalf("expected nothing without an upstream, got %q / %+v", upstream, commits)
	}
}

func TestGitCommitFilesAndDetail(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := unpushedRepo(t)
	_, commits := gitUnpushedCommits(root, 0)
	first := commits[1] // "local one": modify + delete + add

	files := gitCommitFiles(root, first.Hash)
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = f.Status
	}
	want := map[string]string{"a.txt": "M", "added.txt": "A", "gone.txt": "D"}
	for path, status := range want {
		if got[path] != status {
			t.Fatalf("expected %s to be %q in the commit, got %q (all: %+v)", path, status, got[path], got)
		}
	}
	if len(files) != len(want) {
		t.Fatalf("expected exactly %d files, got %d: %+v", len(want), len(files), files)
	}

	d, ok := gitCommitDetail(root, first.Hash)
	if !ok {
		t.Fatal("expected commit detail for a known SHA")
	}
	if d.Subject != "local one" {
		t.Fatalf("expected subject %q, got %q", "local one", d.Subject)
	}
	if !strings.Contains(d.Message, "Body of the first local commit.") {
		t.Fatalf("expected the full message to carry the body, got %q", d.Message)
	}
	if d.Author != "T" || d.Email != "t@example.com" {
		t.Fatalf("expected author T <t@example.com>, got %q <%q>", d.Author, d.Email)
	}
	if d.Files != 3 {
		t.Fatalf("expected 3 files in the diffstat, got %d", d.Files)
	}
	// a.txt: +1/-1, added.txt: +1, gone.txt: -1
	if d.Insertions != 2 || d.Deletions != 2 {
		t.Fatalf("expected +2/-2, got +%d/-%d", d.Insertions, d.Deletions)
	}
	if d.DateISO == "" || d.Date == "" || d.Short == "" {
		t.Fatalf("incomplete detail record: %+v", d)
	}

	// An unknown or malformed SHA is refused rather than handed to git.
	if _, ok := gitCommitDetail(root, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); ok {
		t.Fatal("expected no detail for an unknown SHA")
	}
	if _, ok := gitCommitDetail(root, "--upload-pack=touch /tmp/pwn"); ok {
		t.Fatal("expected a non-hex revision to be refused")
	}
	if files := gitCommitFiles(root, "HEAD~1"); files != nil {
		t.Fatalf("expected only hex SHAs to be accepted, got %+v", files)
	}
}

func TestGitDiffCommitIsFrozenAgainstTheWorkingTree(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := unpushedRepo(t)
	_, commits := gitUnpushedCommits(root, 0)
	first := commits[1] // "local one" changed two -> TWO

	diff := gitDiffCommit(root, "a.txt", first.Hash)
	if !strings.Contains(diff, "-two") || !strings.Contains(diff, "+TWO") {
		t.Fatalf("expected the commit's own change to a.txt, got:\n%s", diff)
	}
	// The later commit's "four" belongs to that commit, not this one.
	if strings.Contains(diff, "+four") {
		t.Fatalf("commit diff leaked a later commit's change:\n%s", diff)
	}

	added, modified, deleted := gitHunksCommit(root, "a.txt", first.Hash)
	if len(modified) != 1 || modified[0] != 2 {
		t.Fatalf("expected line 2 modified, got added=%v modified=%v deleted=%v", added, modified, deleted)
	}

	// Editing the working tree must not move a commit's diff.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("wholly different\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := gitDiffCommit(root, "a.txt", first.Hash); after != diff {
		t.Fatalf("a commit diff changed with the working tree:\nbefore:\n%s\nafter:\n%s", diff, after)
	}

	// A file the commit deleted still has a diff at that commit.
	if d := gitDiffCommit(root, "gone.txt", first.Hash); !strings.Contains(d, "-bye") {
		t.Fatalf("expected the deletion of gone.txt in the commit diff, got:\n%s", d)
	}
	// A file it never touched has none.
	if d := gitDiffCommit(root, "a.txt", "notasha"); d != "" {
		t.Fatalf("expected an empty diff for a bad ref, got:\n%s", d)
	}
}

func TestGitDiffCommitRootCommit(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := unpushedRepo(t)
	first := strings.TrimSpace(gitTestRun(t, root, "rev-list", "--max-parents=0", "HEAD"))
	// The very first commit has no parent, so --root is what makes it diffable.
	if files := gitCommitFiles(root, first); len(files) != 2 {
		t.Fatalf("expected the root commit's 2 files, got %+v", files)
	}
	if d := gitDiffCommit(root, "a.txt", first); !strings.Contains(d, "+one") {
		t.Fatalf("expected the root commit to show a.txt as added, got:\n%s", d)
	}
}

func TestGithubAuthorURL(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitTestRun(t, dir, "init", "-b", "main")

	// No remote at all: no link, never a broken one.
	if got := githubAuthorURL(dir, "a@b.com", "A"); got != "" {
		t.Fatalf("expected no author URL without a remote, got %q", got)
	}

	gitTestRun(t, dir, "remote", "add", "origin", "git@github.com:alice/my-repo.git")
	// A noreply address is a login outright.
	if got := githubAuthorURL(dir, "12345+octocat@users.noreply.github.com", "Octo"); got != "https://github.com/octocat" {
		t.Fatalf("expected a profile link for a noreply address, got %q", got)
	}
	if got := githubAuthorURL(dir, "octocat@users.noreply.github.com", "Octo"); got != "https://github.com/octocat" {
		t.Fatalf("expected a profile link for a legacy noreply address, got %q", got)
	}
	// Anything else can only be "this repo's commits by this address".
	got := githubAuthorURL(dir, "alice@example.com", "Alice")
	if !strings.Contains(got, "github.com/alice/my-repo/commits?author=alice%40example.com") {
		t.Fatalf("expected an author-scoped commits link, got %q", got)
	}

	// A non-GitHub remote gets nothing rather than a GitHub URL for a GitLab repo.
	gitTestRun(t, dir, "remote", "set-url", "origin", "git@gitlab.com:alice/my-repo.git")
	if got := githubAuthorURL(dir, "alice@example.com", "Alice"); got != "" {
		t.Fatalf("expected no author URL for a non-GitHub remote, got %q", got)
	}
}

func TestUnpushedEndpoints(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := unpushedRepo(t)
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)
	ts := httptest.NewServer(s.mux)
	defer ts.Close()

	getJSON := func(path string, into any) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
	}

	var list struct {
		Available bool             `json:"available"`
		Upstream  string           `json:"upstream"`
		Commits   []UnpushedCommit `json:"commits"`
	}
	getJSON("/api/unpushed", &list)
	if !list.Available || list.Upstream != "origin/main" || len(list.Commits) != 2 {
		t.Fatalf("unexpected /api/unpushed payload: %+v", list)
	}
	sha := list.Commits[1].Hash // "local one"

	var files struct {
		SHA   string       `json:"sha"`
		Files []CommitFile `json:"files"`
	}
	getJSON("/api/commitfiles?sha="+sha, &files)
	if len(files.Files) != 3 {
		t.Fatalf("expected 3 files from /api/commitfiles, got %+v", files.Files)
	}

	var detail CommitDetail
	getJSON("/api/commitdetail?sha="+sha, &detail)
	if detail.Subject != "local one" || detail.Files != 3 {
		t.Fatalf("unexpected /api/commitdetail payload: %+v", detail)
	}

	// /api/diff and /api/gutter with a ref show the commit; without one they
	// show the working tree, which here is clean.
	var withRef struct {
		Available bool   `json:"available"`
		Diff      string `json:"diff"`
		Ref       string `json:"ref"`
	}
	getJSON("/api/diff?path=a.txt&ref="+sha, &withRef)
	if !withRef.Available || withRef.Ref != sha || !strings.Contains(withRef.Diff, "+TWO") {
		t.Fatalf("unexpected /api/diff?ref payload: %+v", withRef)
	}

	var noRef struct {
		Available bool `json:"available"`
	}
	getJSON("/api/diff?path=a.txt", &noRef)
	if noRef.Available {
		t.Fatal("expected no working-tree diff for a clean file when no ref is given")
	}

	var gut struct {
		Available bool  `json:"available"`
		Modified  []int `json:"modified"`
	}
	getJSON("/api/gutter?path=a.txt&ref="+sha, &gut)
	if !gut.Available || len(gut.Modified) != 1 || gut.Modified[0] != 2 {
		t.Fatalf("unexpected /api/gutter?ref payload: %+v", gut)
	}

	// A file the commit deleted has nothing on disk, but /api/file with the
	// ref still opens it so its diff can be read.
	var deleted struct {
		Deleted       bool `json:"deleted"`
		DiffAvailable bool `json:"diffAvailable"`
	}
	getJSON("/api/file?path=gone.txt&ref="+sha, &deleted)
	if !deleted.Deleted || !deleted.DiffAvailable {
		t.Fatalf("expected gone.txt to open as a deleted file at the commit, got %+v", deleted)
	}
	// Without the ref there is nothing to show: the file is gone and clean.
	resp, err := http.Get(ts.URL + "/api/file?path=gone.txt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for a long-deleted file with no ref, got %d", resp.StatusCode)
	}
}

func TestUnpushedEndpointWithoutUpstream(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	// gitRepo has no remote, so the section must report itself unavailable
	// rather than fall back to some other branch.
	root := gitRepo(t)
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)
	ts := httptest.NewServer(s.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/unpushed")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Available bool             `json:"available"`
		Upstream  string           `json:"upstream"`
		Commits   []UnpushedCommit `json:"commits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if list.Available || list.Upstream != "" || len(list.Commits) != 0 {
		t.Fatalf("expected an unavailable, empty list without an upstream, got %+v", list)
	}
}

func TestRemoteHostPath(t *testing.T) {
	cases := []struct {
		raw, host, path string
		ok              bool
	}{
		{"git@github.com:alice/repo.git", "github.com", "alice/repo", true},
		{"ssh://git@gitlab.com/alice/repo.git", "gitlab.com", "alice/repo", true},
		{"https://token@github.com/alice/repo.git", "github.com", "alice/repo", true},
		{"http://git.example.com/alice/repo", "git.example.com", "alice/repo", true},
		{"/srv/git/repo.git", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		host, path, ok := remoteHostPath(c.raw)
		if ok != c.ok || host != c.host || path != c.path {
			t.Fatalf("remoteHostPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.raw, host, path, ok, c.host, c.path, c.ok)
		}
	}
}
