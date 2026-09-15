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

func TestGitBlame(t *testing.T) {
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
	// Two commits so the parser has to dedupe across more than one hash, then
	// an uncommitted edit so a line comes back as "Not Committed Yet". Line
	// numbers must match the file as written to disk -- no remapping needed.
	write("f.go", "alpha\nbravo\ncharlie\n")
	write("untr.go", "new\n")
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("add", "f.go")
	run("commit", "-qm", "first")
	write("f.go", "alpha\nBRAVO\ncharlie\n")
	run("add", "f.go")
	run("commit", "-qm", "second")
	write("f.go", "alpha\nBRAVO\ncharlie-dirty\n")

	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)

	code, body := get(t, s, "/api/blame?path=f.go")
	if code != 200 {
		t.Fatalf("blame status %d", code)
	}
	if body["available"] != true {
		t.Fatalf("available = %v, want true (%v)", body["available"], body)
	}
	commits := body["commits"].([]any)
	lines := body["lines"].([]any)
	if len(lines) != 3 {
		t.Fatalf("lines = %v, want 3 entries", lines)
	}
	if len(commits) != 3 {
		t.Fatalf("commits = %v, want 3 distinct entries (first, second, uncommitted)", commits)
	}
	commitAt := func(i int) map[string]any {
		return commits[int(lines[i].(float64))].(map[string]any)
	}
	if commitAt(0)["author"] != "T" || commitAt(0)["summary"] != "first" {
		t.Errorf("line 1 = %v, want author T / summary %q", commitAt(0), "first")
	}
	if commitAt(1)["author"] != "T" || commitAt(1)["summary"] != "second" {
		t.Errorf("line 2 = %v, want author T / summary %q", commitAt(1), "second")
	}
	if commitAt(2)["author"] != "Not Committed Yet" || commitAt(2)["hash"] != zeroBlameHash {
		t.Errorf("line 3 (dirty) = %v, want author %q / hash %q", commitAt(2), "Not Committed Yet", zeroBlameHash)
	}

	// An untracked file has no blame -> available:false, never 500.
	code, body = get(t, s, "/api/blame?path=untr.go")
	if code != 200 || body["available"] != false {
		t.Errorf("untracked: status=%d available=%v, want 200/false", code, body["available"])
	}

	// git disabled -> available:false, no shell-out.
	gitDisabled = true
	_, body = get(t, s, "/api/blame?path=f.go")
	gitDisabled = false
	if body["available"] != false {
		t.Errorf("blame available = %v with -no-git, want false", body["available"])
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
