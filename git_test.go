package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	run("add", "-A")
	run("commit", "-qm", "init")
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

	// Overlay onto tree nodes. Deleted/old-rename paths have no node on disk.
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

	// Meta reports git availability.
	_, meta := get(t, s, "/api/meta")
	if meta["git"] != true {
		t.Errorf("meta git = %v, want true", meta["git"])
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

func TestGitUpstreamLog(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}

	// Test case 1: No upstream configured (local-only repo)
	root := gitRepo(t)
	commits, upstream, hasUpstream := gitUpstreamLog(root, 50)
	if hasUpstream {
		t.Errorf("no upstream configured: hasUpstream=%v, want false", hasUpstream)
	}
	if commits != nil {
		t.Errorf("no upstream: commits=%v, want nil", commits)
	}
	if upstream != "" {
		t.Errorf("no upstream: upstream=%q, want empty", upstream)
	}

	// Test case 2: With upstream configured via git config
	// Create a simple setup where we use git config to set upstream without a real remote
	origin := t.TempDir()
	if r, err := filepath.EvalSymlinks(origin); err == nil {
		origin = r
	}
	run := func(root string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, root, err, out)
		}
	}

	// Initialize bare origin
	run(origin, "init", "--bare")

	// Add origin as remote and push
	run(root, "remote", "add", "origin", origin)
	run(root, "push", "-u", "origin", "master")

	// Fetch so origin/master exists locally
	run(root, "fetch", "origin")

	// Create commits ahead of upstream
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "new commit 1\n")
	run(root, "add", "a.go")
	run(root, "commit", "-qm", "commit 1 ahead")

	write("b.go", "new commit 2\n")
	run(root, "add", "b.go")
	run(root, "commit", "-qm", "commit 2 ahead")

	// Now query upstream log
	commits, upstream, hasUpstream = gitUpstreamLog(root, 50)
	if !hasUpstream {
		t.Errorf("with upstream: hasUpstream=%v, want true", hasUpstream)
	}
	if upstream != "origin/master" {
		t.Errorf("upstream ref=%q, want %q", upstream, "origin/master")
	}
	if len(commits) != 2 {
		t.Errorf("commits count=%d, want 2", len(commits))
	}
	if len(commits) > 0 {
		// Check newest-first order (second commit should be first)
		if !strings.Contains(commits[0].Subject, "commit 2") {
			t.Errorf("first (newest) commit subject=%q, want 'commit 2 ahead'", commits[0].Subject)
		}
		if !strings.Contains(commits[1].Subject, "commit 1") {
			t.Errorf("second commit subject=%q, want 'commit 1 ahead'", commits[1].Subject)
		}
		// Check that all fields are populated
		if commits[0].Hash == "" || commits[0].Short == "" || commits[0].Author == "" ||
			commits[0].RelTime == "" || commits[0].ISOTime == "" {
			t.Errorf("commit 0 missing fields: %+v", commits[0])
		}
	}

	// Test limit
	commits, _, _ = gitUpstreamLog(root, 1)
	if len(commits) != 1 {
		t.Errorf("with limit 1: count=%d, want 1", len(commits))
	}
}

func TestGitParent(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	// Get the initial commit hash
	initialHash := run("rev-parse", "HEAD")

	// gitParent of the initial commit should be empty-tree constant
	parent := gitParent(root, initialHash)
	if parent != emptyTreeHash {
		t.Errorf("root commit parent=%q, want %q", parent, emptyTreeHash)
	}

	// Create another commit and check it has the initial commit as parent
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("another.go", "new\n")
	run("add", "another.go")
	run("commit", "-qm", "second commit")

	newHash := run("rev-parse", "HEAD")
	parent = gitParent(root, newHash)
	if parent != initialHash {
		t.Errorf("second commit parent=%q, want %q", parent, initialHash)
	}

	// Test invalid hash
	parent = gitParent(root, "invalid")
	if parent != emptyTreeHash {
		t.Errorf("invalid hash parent=%q, want %q", parent, emptyTreeHash)
	}
}

func TestGitCommitFiles(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	// Get the initial commit hash (it has M/A/D/R files from gitRepo)
	initialHash := run("rev-parse", "HEAD")

	files := gitCommitFiles(root, initialHash)
	// The initial commit created keep.go, sub/mod.go, del.go, sub/ren.go
	// All should have status "A" (added)
	expectedFiles := map[string]string{
		"keep.go":    "A",
		"sub/mod.go": "A",
		"del.go":     "A",
		"sub/ren.go": "A",
	}
	for path, expectedStatus := range expectedFiles {
		if status, ok := files[path]; !ok {
			t.Errorf("file %q not in result", path)
		} else if status != expectedStatus {
			t.Errorf("file %q status=%q, want %q", path, status, expectedStatus)
		}
	}

	// Test invalid hash
	files = gitCommitFiles(root, "invalid")
	if len(files) != 0 {
		t.Errorf("invalid hash result=%v, want empty map", files)
	}

	// Test hash that's too short
	files = gitCommitFiles(root, "abc")
	if len(files) != 0 {
		t.Errorf("short hash result=%v, want empty map", files)
	}

	// Test hash with invalid characters
	files = gitCommitFiles(root, "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")
	if len(files) != 0 {
		t.Errorf("invalid chars result=%v, want empty map", files)
	}

	// A pure rename must come back as "R", not fall through to "M" -- git
	// reports it as "R100<TAB>old<TAB>new", which the switch on the bare
	// letter alone (without the trailing similarity digits) must still map
	// through mapXY the same way gitStatus does.
	run("add", "-A") // stage the mod/add/untr/del/rename left dirty by gitRepo
	run("commit", "-qm", "settle")
	renameHash := run("rev-parse", "HEAD")
	files = gitCommitFiles(root, renameHash)
	if got := files["sub/ren2.go"]; got != "R" {
		t.Errorf("renamed file status=%q, want %q (full: %v)", got, "R", files)
	}
}

func TestGitDiffRef(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	// Test ref="" (HEAD diff - working tree)
	diff := gitDiff(root, "", "sub/mod.go")
	if !strings.Contains(diff, "-line one") || !strings.Contains(diff, "+line two") {
		t.Errorf("working tree diff missing expected content: %q", diff)
	}

	// Get the initial commit hash
	initialHash := run("rev-parse", "HEAD")

	// Test with ref (commit diff - parent)
	diff = gitDiff(root, initialHash, "sub/mod.go")
	// For the initial commit, should show as "A" (added)
	if !strings.Contains(diff, "+line one") {
		t.Errorf("commit diff for initial add missing content: %q", diff)
	}

	// Test clean file with ref=""
	diff = gitDiff(root, "", "keep.go")
	if diff != "" {
		t.Errorf("clean file ref='' diff=%q, want empty", diff)
	}

	// Test clean file with ref (initial commit) should show added
	diff = gitDiff(root, initialHash, "keep.go")
	if !strings.Contains(diff, "+keep") {
		t.Errorf("clean file initial commit diff missing content: %q", diff)
	}

	// Test invalid ref
	diff = gitDiff(root, "invalid", "sub/mod.go")
	if diff != "" {
		t.Errorf("invalid ref diff=%q, want empty", diff)
	}

	// Test nonexistent file
	diff = gitDiff(root, "", "nonexistent.go")
	if diff != "" {
		t.Errorf("nonexistent file diff=%q, want empty", diff)
	}
}

func TestGitHunksRef(t *testing.T) {
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
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	// Set up a baseline file with multiple lines
	write("f.go", "alpha\nbravo\ncharlie\ndelta\necho\nfoxtrot\ngolf\nhotel\n")
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("add", "-A")
	run("commit", "-qm", "baseline")
	_ = run("rev-parse", "HEAD") // baseline commit, used for ref testing

	// Create a second commit with edits
	write("f.go", "alpha\nBRAVO\ncharlie\ndelta\nNEWLINE\necho\nfoxtrot\nhotel\n")
	run("add", "f.go")
	run("commit", "-qm", "edit")
	editHash := run("rev-parse", "HEAD")

	// Test ref="" (working tree) - but f.go is clean now so no diff
	added, modified, deleted := gitHunks(root, "", "f.go")
	if added != nil || modified != nil || deleted != nil {
		t.Errorf("clean file ref='': added=%v modified=%v deleted=%v, want all nil", added, modified, deleted)
	}

	// Dirty the file again for working-tree diff test - change line 8
	write("f.go", "alpha\nBRAVO\ncharlie\ndelta\nNEWLINE\necho\nfoxtrot\nMODIFIED\n")
	added, modified, deleted = gitHunks(root, "", "f.go")
	// We expect modified (line 8 changed)
	if modified == nil {
		t.Errorf("dirty file ref='': modified=%v, want non-nil", modified)
	}

	// Test with ref=editHash (commit against parent)
	added, modified, deleted = gitHunks(root, editHash, "f.go")
	if added == nil || modified == nil {
		t.Errorf("commit ref: added=%v modified=%v deleted=%v, want added/modified non-nil", added, modified, deleted)
	}
	// Line 2 is BRAVO (modified), line 5 is NEWLINE (added)
	if !contains(modified, 2) {
		t.Errorf("modified lines=%v, want to contain 2", modified)
	}
	if !contains(added, 5) {
		t.Errorf("added lines=%v, want to contain 5", added)
	}

	// Test invalid ref
	added, modified, deleted = gitHunks(root, "invalid", "f.go")
	if added != nil || modified != nil || deleted != nil {
		t.Errorf("invalid ref: added=%v modified=%v deleted=%v, want all nil", added, modified, deleted)
	}

	// Test nonexistent file
	added, modified, deleted = gitHunks(root, "", "nonexistent.go")
	if added != nil || modified != nil || deleted != nil {
		t.Errorf("nonexistent file: added=%v modified=%v deleted=%v, want all nil", added, modified, deleted)
	}
}

// contains checks if an int is in a slice
func contains(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

func TestGitCommitDetail(t *testing.T) {
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

	// Test 1: Commit with multi-line body and file changes
	write("f.go", "line1\nline2\n")
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("add", "f.go")
	run("commit", "-m", "initial")

	write("a.go", "new\n")
	run("add", "a.go")
	run("commit", "-m", "add a.go")

	// Commit with multi-line message: subject + blank line + body
	write("b.go", "new b\n")
	run("add", "b.go")
	commitMsg := `subject with body

This is the first line of the body.
This is the second line.`
	cmd := exec.Command("git", "-C", root, "commit", "-m", commitMsg)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if err := cmd.Run(); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	bodyCommitHash := run("rev-parse", "HEAD")

	body, files, ins, del, ok := gitCommitDetail(root, bodyCommitHash)
	if !ok {
		t.Errorf("multi-line commit ok=false, want true")
	}
	if !strings.Contains(body, "This is the first line") {
		t.Errorf("body missing expected content: %q", body)
	}
	if files != 1 {
		t.Errorf("files=%d, want 1", files)
	}
	if ins == 0 {
		t.Errorf("insertions=%d, want > 0", ins)
	}

	// Test 2: Commit with no body (subject only)
	write("c.go", "new c\n")
	run("add", "c.go")
	run("commit", "-m", "subject only")
	noBodyHash := run("rev-parse", "HEAD")

	body, files, ins, del, ok = gitCommitDetail(root, noBodyHash)
	if !ok {
		t.Errorf("no-body commit ok=false, want true")
	}
	if body != "" {
		t.Errorf("no-body commit body=%q, want empty", body)
	}
	if files != 1 {
		t.Errorf("files=%d, want 1", files)
	}

	// Test 3: Empty commit (--allow-empty, no file changes)
	run("commit", "--allow-empty", "-m", "empty commit")
	emptyHash := run("rev-parse", "HEAD")

	body, files, ins, del, ok = gitCommitDetail(root, emptyHash)
	if !ok {
		t.Errorf("empty commit ok=false, want true")
	}
	if body != "" {
		t.Errorf("empty commit body=%q, want empty", body)
	}
	if files != 0 {
		t.Errorf("empty commit files=%d, want 0", files)
	}
	if ins != 0 || del != 0 {
		t.Errorf("empty commit ins=%d del=%d, want 0/0", ins, del)
	}

	// Test 4: Invalid hash
	body, files, ins, del, ok = gitCommitDetail(root, "invalid")
	if ok {
		t.Errorf("invalid hash ok=true, want false")
	}

	// Test 5: Short invalid hash
	body, files, ins, del, ok = gitCommitDetail(root, "abc")
	if ok {
		t.Errorf("short hash ok=true, want false")
	}

	// Test 6: Hash with invalid characters
	body, files, ins, del, ok = gitCommitDetail(root, "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")
	if ok {
		t.Errorf("invalid chars ok=true, want false")
	}

	// Test 7: With git disabled
	gitDisabled = true
	defer func() { gitDisabled = false }()
	body, files, ins, del, ok = gitCommitDetail(root, bodyCommitHash)
	if ok {
		t.Errorf("git disabled ok=true, want false")
	}
}
