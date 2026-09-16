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
	rows := diffRowsOf(t, body)
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want the deleted and the added line", rows)
	}
	if got := rowKeys(rows); !reflect.DeepEqual(got, []string{"del:1:-", "add:-:1"}) {
		t.Errorf("rows = %v, want [del:1:- add:-:1]", got)
	}
	if !strings.Contains(rows[0]["html"].(string), "one") || !strings.Contains(rows[1]["html"].(string), "two") {
		t.Errorf("rows lost their content: %v", rows)
	}
	// The word ranges are the point of serving rows instead of text: only the
	// word that changed is marked, not the whole line.
	if got := wordsOf(rows[1]); !reflect.DeepEqual(got, [][2]int{{5, 8}}) {
		t.Errorf("added row words = %v, want [[5 8]] (\"two\" alone)", got)
	}

	// A clean, committed file yields no rows -> available:false.
	_, body = get(t, s, "/api/diff?path=keep.go")
	if body["available"] != false || len(body["hunks"].([]any)) != 0 {
		t.Errorf("clean file: available=%v hunks=%v, want false/empty", body["available"], body["hunks"])
	}

	// An untracked file is invisible to `git diff HEAD`; review serves it as a
	// whole-file addition instead, because a brand new file is the thing most
	// worth reading.
	code, body = get(t, s, "/api/diff?path=untr.go")
	if code != 200 || body["available"] != true {
		t.Fatalf("untracked: status=%d available=%v, want 200/true", code, body["available"])
	}
	if got := rowKeys(diffRowsOf(t, body)); !reflect.DeepEqual(got, []string{"add:-:1"}) {
		t.Errorf("untracked rows = %v, want the whole file as additions", got)
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

// diffRowsOf flattens an /api/diff body's hunks into one row list; the tests
// below care about the rows, not about where the hunk boundaries fell.
func diffRowsOf(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	hunks, ok := body["hunks"].([]any)
	if !ok {
		t.Fatalf("body has no hunks: %v", body)
	}
	var rows []map[string]any
	for _, h := range hunks {
		for _, r := range h.(map[string]any)["rows"].([]any) {
			rows = append(rows, r.(map[string]any))
		}
	}
	return rows
}

func rowKeys(rows []map[string]any) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		n := func(k string) string {
			v, ok := r[k].(float64)
			if !ok {
				return "-"
			}
			return itoa(int(v))
		}
		out[i] = r["type"].(string) + ":" + n("old") + ":" + n("new")
	}
	return out
}

func wordsOf(row map[string]any) [][2]int {
	var out [][2]int
	for _, w := range row["words"].([]any) {
		pair := w.([]any)
		out = append(out, [2]int{int(pair[0].(float64)), int(pair[1].(float64))})
	}
	return out
}

func TestGitShowHead(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)

	if src, ok := gitShowHead(root, "sub/mod.go"); !ok || src != "line one\n" {
		t.Errorf("gitShowHead(sub/mod.go) = %q, %v; want the committed content", src, ok)
	}
	// A file deleted from disk still has a HEAD side -- that is what lets the
	// deletion render as rows instead of as nothing.
	if src, ok := gitShowHead(root, "del.go"); !ok || src != "del\n" {
		t.Errorf("gitShowHead(del.go) = %q, %v; want the committed content", src, ok)
	}
	// No HEAD side: untracked, staged-new, and the new name of a rename.
	for _, rel := range []string{"untr.go", "add.go", "sub/ren2.go", "nope.go"} {
		if src, ok := gitShowHead(root, rel); ok {
			t.Errorf("gitShowHead(%s) = %q, true; want no HEAD side", rel, src)
		}
	}

	// Serving a subdirectory of a repo: git speaks repo-relative paths, so the
	// served root's offset has to come back off before asking.
	if src, ok := gitShowHead(filepath.Join(root, "sub"), "mod.go"); !ok || src != "line one\n" {
		t.Errorf("gitShowHead from a served subdir = %q, %v; want the committed content", src, ok)
	}

	gitDisabled = true
	defer func() { gitDisabled = false }()
	if _, ok := gitShowHead(root, "sub/mod.go"); ok {
		t.Error("gitShowHead succeeded with -no-git")
	}
}

func TestGitChangeset(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)

	files := gitChangeset(root)
	byPath := map[string]ChangedFile{}
	var order []string
	for _, f := range files {
		byPath[f.Path] = f
		order = append(order, f.Path)
	}
	want := []string{"add.go", "del.go", "sub/mod.go", "sub/ren2.go", "untr.go"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("changeset = %v, want %v sorted by path", order, want)
	}
	// keep.go is committed and untouched: it is not part of the changeset.
	if _, ok := byPath["keep.go"]; ok {
		t.Error("an unchanged file is in the changeset")
	}

	cases := []struct {
		path    string
		status  string
		old     string
		added   int
		deleted int
	}{
		{path: "sub/mod.go", status: "M", added: 1, deleted: 1},
		{path: "add.go", status: "A", added: 1},
		{path: "del.go", status: "D", deleted: 1},
		// `git diff -M` pairs the two halves of a rename; the old name has to
		// survive to the client or a rename reads as an unrelated add.
		{path: "sub/ren2.go", status: "R", old: "sub/ren.go"},
		// Untracked: numstat is blind to it, so it comes from porcelain status
		// and is counted off disk as a whole-file addition.
		{path: "untr.go", status: "U", added: 1},
	}
	for _, c := range cases {
		f := byPath[c.path]
		if f.Status != c.status || f.Old != c.old || f.Added != c.added || f.Deleted != c.deleted {
			t.Errorf("%s = %+v, want status %q old %q +%d -%d",
				c.path, f, c.status, c.old, c.added, c.deleted)
		}
	}

	gitDisabled = true
	defer func() { gitDisabled = false }()
	if got := gitChangeset(root); got != nil {
		t.Errorf("gitChangeset = %v with -no-git, want nil", got)
	}
}
