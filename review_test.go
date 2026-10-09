package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseReviewWholeFileErrors(t *testing.T) {
	cases := map[string]string{
		"not json":      `{`,
		"no version":    `{"comments":[]}`,
		"wrong version": `{"version":2}`,
		"version type":  `{"version":"1"}`,
	}
	for name, in := range cases {
		if _, err := parseReview([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := parseReview([]byte(`{"version":1}`)); err != nil {
		t.Errorf("a review with no comments is valid: %v", err)
	}
}

func TestLoadReviewSizeLimit(t *testing.T) {
	big := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(big, []byte(`{"version":1,"summary":"`+strings.Repeat("x", reviewMaxFile)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReview(big); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("expected a size error, got %v", err)
	}
}

func TestParseReviewPerCommentRejection(t *testing.T) {
	in := `{"version":1,"comments":[
		{"id":"ok","body":"fine","path":"a.go","line":3,"endLine":5},
		{"id":"ok","body":"duplicate id"},
		{"id":"bad id!","body":"x"},
		{"id":"nobody","body":"  "},
		{"id":"badside","body":"x","path":"a.go","line":1,"side":"MIDDLE"},
		{"id":"badsev","body":"x","severity":"catastrophic"},
		{"id":"noline","body":"x","line":4},
		{"id":"backwards","body":"x","path":"a.go","line":9,"endLine":4},
		{"id":"endonly","body":"x","path":"a.go","endLine":4},
		{"id":"dotdot","body":"x","path":"../etc/passwd","line":1},
		{"id":"abs","body":"x","path":"/etc/passwd","line":1},
		{"id":"badreply","body":"x","inReplyTo":"missing"},
		{"id":"selfreply","body":"x","inReplyTo":"selfreply"},
		{"id":"badrefpath","body":"x","refs":[{"path":"../x"}]},
		{"id":"badrefrev","body":"x","refs":[{"path":"a.go","rev":"--upload-pack=x"}]},
		{"id":"typo","body":"x","line":"seven"},
		{"id":"claims","body":"y","status":"anchored","kind":"line","origLine":7,"outsideDiff":true},
		{"id":"reply","body":"re","inReplyTo":"ok"}
	]}`
	doc, err := parseReview([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range doc.Comments {
		got[c.ID] = true
	}
	for _, id := range []string{"ok", "claims", "reply"} {
		if !got[id] {
			t.Errorf("comment %q should be accepted", id)
		}
	}
	if len(doc.Comments) != 3 {
		t.Errorf("expected 3 accepted comments, got %d: %+v", len(doc.Comments), doc.Comments)
	}
	if len(doc.Rejected) != 15 {
		t.Errorf("expected 15 rejected comments, got %d: %+v", len(doc.Rejected), doc.Rejected)
	}
	for _, c := range doc.Comments {
		switch c.ID {
		case "ok":
			if c.Side != "RIGHT" || c.Severity != "minor" || c.Kind != "line" {
				t.Errorf("defaults not applied: %+v", c)
			}
		case "claims":
			if c.Status != "" || c.OrigLine != 0 || c.OutsideDiff || c.Kind != "general" {
				t.Errorf("a file must not be able to set computed fields: %+v", c)
			}
		case "reply":
			if !c.inherits {
				t.Errorf("a reply with no location should inherit its parent's")
			}
		}
	}
}

func TestParseReviewCommentLimit(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`{"version":1,"comments":[`)
	for i := 0; i < reviewMaxComments+5; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"id":"c` + strconv.Itoa(i) + `","body":"b"}`)
	}
	sb.WriteString(`]}`)
	doc, err := parseReview([]byte(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Comments) != reviewMaxComments {
		t.Errorf("expected %d comments, got %d", reviewMaxComments, len(doc.Comments))
	}
	if len(doc.Rejected) != 1 || !strings.Contains(doc.Rejected[0].Reason, "limit") {
		t.Errorf("expected one rejection noting the limit, got %+v", doc.Rejected)
	}
}

func TestParseReviewTopLevelLimits(t *testing.T) {
	in := `{"version":1,"title":"` + strings.Repeat("t", 300) + `","verdict":"maybe"}`
	doc, err := parseReview([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Title) != reviewMaxTitle || doc.Verdict != "" || len(doc.Warnings) != 2 {
		t.Errorf("title %d bytes, verdict %q, warnings %v", len(doc.Title), doc.Verdict, doc.Warnings)
	}
}

func TestParseReviewIgnoresMalformedSHAs(t *testing.T) {
	doc, err := parseReview([]byte(`{"version":1,"headSHA":"<full SHA of head>","baseSHA":"abc1234"}`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.HeadSHA != "" || doc.BaseSHA != "abc1234" || len(doc.Warnings) != 1 {
		t.Errorf("a placeholder SHA must be dropped with a warning, not make every review look stale: head=%q base=%q warnings=%v", doc.HeadSHA, doc.BaseSHA, doc.Warnings)
	}
}

func TestCleanReviewPath(t *testing.T) {
	good := []string{"a.go", "sub/dir/a.go", "./a.go"}
	bad := []string{"", "/a.go", "../a.go", "a/../b.go", "a//b.go", "a/", "a\\b.go", "a\nb", "."}
	for _, p := range good {
		if _, ok := cleanReviewPath(p); !ok {
			t.Errorf("%q should be accepted", p)
		}
	}
	for _, p := range bad {
		if _, ok := cleanReviewPath(p); ok {
			t.Errorf("%q should be rejected", p)
		}
	}
}

func TestLocateAnchor(t *testing.T) {
	file := []string{"package a", "", "func A() {", "\treturn", "}", "", "func B() {", "\treturn", "}"}
	norm := make([]string, len(file))
	for i, l := range file {
		norm[i] = normalizeAnchorLine(l)
	}
	cases := []struct {
		name   string
		line   int
		anchor string
		status string
		at     int
	}{
		{"exact, whitespace differs", 3, "  func   A() {  ", "anchored", 3},
		{"multi-line", 3, "func A() {\n  return\n}", "anchored", 3},
		{"moved", 1, "func A() {", "moved", 3},
		{"gone", 3, "func Nope() {", "unanchored", 3},
		{"ambiguous", 5, "return", "unanchored", 5}, // lines 4 and 8 both match within the window
		{"no anchor", 2, "", "unverified", 2},
		{"blank-only anchor", 2, " \n ", "unverified", 2},
	}
	for _, tc := range cases {
		status, at := locateAnchor(norm, tc.line, normalizeAnchor(tc.anchor))
		if status != tc.status || at != tc.at {
			t.Errorf("%s: got %s@%d, want %s@%d", tc.name, status, at, tc.status, tc.at)
		}
	}
}

// reviewRepo makes a repository whose "feature" branch edits a.go (shifting its
// lines), deletes b.go and adds c.go, relative to "main". It leaves main
// checked out, so reviewing feature needs a worktree.
func reviewRepo(t *testing.T) string {
	t.Helper()
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitTestRun(t, root, "init", "-b", "main")
	gitTestRun(t, root, "config", "user.email", "t@example.com")
	gitTestRun(t, root, "config", "user.name", "T")
	gitTestRun(t, root, "config", "commit.gpgsign", "false")
	write("a.go", "package a\n\nfunc A() int {\n\treturn 1\n}\n\nfunc Z() {}\n")
	write("b.go", "package b\n")
	write("keep.go", "package keep\n")
	gitTestRun(t, root, "add", "-A")
	gitTestRun(t, root, "commit", "-qm", "base")
	gitTestRun(t, root, "checkout", "-qb", "feature")
	write("a.go", "package a\n// added 1\n// added 2\n// added 3\n\nfunc A() int {\n\treturn 2\n}\n\nfunc Z() {}\n")
	write("c.go", "package c\n")
	gitTestRun(t, root, "rm", "-q", "b.go")
	gitTestRun(t, root, "add", "-A")
	gitTestRun(t, root, "commit", "-qm", "feature work")
	gitTestRun(t, root, "checkout", "-q", "main")
	return root
}

const reviewFixture = `{
  "version": 1,
  "title": "Review of feature",
  "summary": "Two things to look at.",
  "generatedBy": {"agent": "claude", "sessionId": "secret-session"},
  "comments": [
    {"id": "c1", "path": "a.go", "line": 1, "anchor": "package a", "severity": "nit", "body": "ok"},
    {"id": "c2", "path": "a.go", "line": 3, "anchor": "func A() int {", "severity": "blocker", "body": "moved"},
    {"id": "c3", "path": "a.go", "line": 99, "body": "past the end"},
    {"id": "c4", "path": "nope.go", "line": 1, "body": "no such file"},
    {"id": "c5", "path": "b.go", "side": "LEFT", "line": 1, "anchor": "package b", "body": "deleted file, old side"},
    {"id": "c5r", "path": "b.go", "line": 1, "body": "deleted file has nothing at head"},
    {"id": "c6", "inReplyTo": "c2", "body": "a reply"},
    {"id": "c7", "body": "general"},
    {"id": "c8", "path": "a.go", "line": 3, "anchor": "this text does not exist", "body": "unanchored"},
    {"id": "c9", "path": "keep.go", "line": 1, "body": "untouched file"},
    {"id": "c10", "path": "a.go", "body": "file level"}
  ]
}`

// reviewServer builds a server reviewing feature against main.
func reviewServer(t *testing.T, repo, review string) (*Server, *prSession, *reviewState, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(file, []byte(review), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := loadReview(file)
	if err != nil {
		t.Fatal(err)
	}
	p, err := prepareLocalReview(rs.doc, "main", "feature", repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	ix := NewIndex(p.Root())
	ix.Build()
	srv := NewServer(ix, newLSPManager(p.Root(), false))
	srv.SetPR(p)
	srv.SetReview(rs)
	return srv, p, rs, file
}

func waitResolved(t *testing.T, rs *reviewState) reviewSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := rs.snapshot(); s.Resolved {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("review did not resolve")
	return reviewSnapshot{}
}

func TestReviewResolveAgainstRepo(t *testing.T) {
	repo := reviewRepo(t)
	_, p, rs, _ := reviewServer(t, repo, reviewFixture)
	if p.inPlace || !p.local {
		t.Fatalf("main is checked out, so reviewing feature needs a worktree (inPlace=%v local=%v)", p.inPlace, p.local)
	}
	snap := waitResolved(t, rs)

	byID := map[string]reviewComment{}
	for _, c := range snap.Comments {
		byID[c.ID] = c
	}
	rejected := map[string]string{}
	for _, r := range snap.Rejected {
		rejected[r.ID] = r.Reason
	}

	if c := byID["c1"]; c.Status != "anchored" || c.Line != 1 || c.OutsideDiff {
		t.Errorf("c1: %+v", c)
	}
	if c := byID["c2"]; c.Status != "moved" || c.Line != 6 || c.OrigLine != 3 {
		t.Errorf("c2 should have moved from line 3 to 6: %+v", c)
	}
	if c := byID["c3"]; c.Status != "out_of_range" || c.Line != 0 || c.OrigLine != 99 || c.Kind != "file" {
		t.Errorf("c3 should be demoted to a file-level comment: %+v", c)
	}
	if !strings.Contains(rejected["c4"], "not in head") {
		t.Errorf("c4 should be rejected, got %q", rejected["c4"])
	}
	if c := byID["c5"]; c.Status != "anchored" || c.Side != "LEFT" {
		t.Errorf("c5 (old side of a deleted file) should anchor at the merge-base: %+v", c)
	}
	if !strings.Contains(rejected["c5r"], "not in head") {
		t.Errorf("c5r should be rejected, got %q", rejected["c5r"])
	}
	if c := byID["c6"]; c.Path != "a.go" || c.Line != 6 || c.Status != "moved" || c.Kind != "line" {
		t.Errorf("c6 should follow its parent to a.go:6: %+v", c)
	}
	if c := byID["c7"]; c.Kind != "general" {
		t.Errorf("c7: %+v", c)
	}
	if c := byID["c8"]; c.Status != "unanchored" || c.Line != 0 || c.OrigLine != 3 || c.Kind != "file" {
		t.Errorf("c8 should be shown at file level: %+v", c)
	}
	if c := byID["c9"]; !c.OutsideDiff {
		t.Errorf("c9 is in a file the diff does not touch: %+v", c)
	}
	if c := byID["c10"]; c.Kind != "file" || c.Status != "" {
		t.Errorf("c10: %+v", c)
	}
	if snap.Stale {
		t.Errorf("no headSHA in the file, so it cannot be stale")
	}
	if snap.GeneratedBy == nil || snap.GeneratedBy.SessionID != "" {
		t.Errorf("the session id must stay server-side: %+v", snap.GeneratedBy)
	}
}

func TestReviewStale(t *testing.T) {
	repo := reviewRepo(t)
	review := `{"version":1,"headSHA":"deadbeef","comments":[{"id":"a","body":"x"}]}`
	_, _, rs, _ := reviewServer(t, repo, review)
	snap := waitResolved(t, rs)
	if !snap.Stale || !strings.Contains(snap.StaleNote, "deadbeef") {
		t.Errorf("expected a stale review, got stale=%v note=%q", snap.Stale, snap.StaleNote)
	}
	head := strings.TrimSpace(gitTestRun(t, repo, "rev-parse", "feature"))
	review = `{"version":1,"headSHA":"` + head[:10] + `","comments":[]}`
	_, _, rs, _ = reviewServer(t, repo, review)
	if snap := waitResolved(t, rs); snap.Stale {
		t.Errorf("a headSHA that prefixes the real head is not stale: %q", snap.StaleNote)
	}
}

func TestHandleReviewETagAndReload(t *testing.T) {
	repo := reviewRepo(t)
	srv, _, rs, file := reviewServer(t, repo, `{"version":1,"comments":[{"id":"a","body":"first"}]}`)
	waitResolved(t, rs)

	get := func(etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/review", nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	w := get("")
	if w.Code != 200 || w.Header().Get("ETag") == "" {
		t.Fatalf("expected 200 with an ETag, got %d", w.Code)
	}
	etag := w.Header().Get("ETag")
	if w := get(etag); w.Code != 304 {
		t.Fatalf("an unchanged review should answer 304, got %d", w.Code)
	}

	next := `{"version":1,"comments":[{"id":"a","body":"first"},{"id":"b","body":"second, added later"}]}`
	if err := os.WriteFile(file, []byte(next), 0o644); err != nil {
		t.Fatal(err)
	}
	w = get(etag)
	if w.Code != 200 {
		t.Fatalf("a rewritten file should invalidate the ETag, got %d", w.Code)
	}
	var snap reviewSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.Resolved && len(snap.Comments) != 1 {
		t.Errorf("until the new file is checked, the last checked comments stay up, got %+v", snap.Comments)
	}
	waitResolved(t, rs)
	json.Unmarshal(get("").Body.Bytes(), &snap)
	if len(snap.Comments) != 2 {
		t.Errorf("expected the new comment, got %+v", snap.Comments)
	}

	// A file that stops parsing keeps the last good review and says so.
	if err := os.WriteFile(file, []byte(`{ not json, and a different size`), 0o644); err != nil {
		t.Fatal(err)
	}
	w = get("")
	snap = reviewSnapshot{}
	json.Unmarshal(w.Body.Bytes(), &snap)
	if len(snap.Comments) != 2 || snap.LoadError == "" {
		t.Errorf("expected the last good review plus a load error, got %d comments, error %q", len(snap.Comments), snap.LoadError)
	}

	req := httptest.NewRequest("POST", "/api/review", nil)
	pw := httptest.NewRecorder()
	srv.ServeHTTP(pw, req)
	if pw.Code != 405 {
		t.Errorf("there is no way to write a review through the API, got %d", pw.Code)
	}
}

func TestLocalReviewInPlaceNeverRemovesTheRepo(t *testing.T) {
	repo := reviewRepo(t)
	gitTestRun(t, repo, "checkout", "-q", "feature")
	doc := &reviewDoc{}
	p, err := prepareLocalReview(doc, "main", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	if !p.inPlace || p.worktree != repo || p.srcRepo != "" {
		t.Fatalf("head is checked out, so the repo should be used in place: %+v", p)
	}
	p.writeScopeFile("x.diff", "scratch")
	scope := p.scopeDir
	p.Close()
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("Close removed the user's repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "a.go")); err != nil {
		t.Fatalf("Close removed the user's files: %v", err)
	}
	if _, err := os.Stat(scope); !os.IsNotExist(err) {
		t.Errorf("the scratch diff directory should be removed")
	}
}

func TestLocalReviewWorktreeIsRemovedOnClose(t *testing.T) {
	repo := reviewRepo(t)
	p, err := prepareLocalReview(&reviewDoc{}, "main", "feature", repo)
	if err != nil {
		t.Fatal(err)
	}
	wt := p.worktree
	if wt == repo || !strings.Contains(filepath.Base(wt), "px0-review-") {
		t.Fatalf("expected a temp worktree, got %s", wt)
	}
	if _, err := os.Stat(filepath.Join(wt, "c.go")); err != nil {
		t.Fatalf("the worktree should hold the feature branch: %v", err)
	}
	if p.diffBase != strings.TrimSpace(gitTestRun(t, repo, "rev-parse", "main")) {
		t.Errorf("diffBase should be the merge-base (main here), got %s", p.diffBase)
	}
	p.Close()
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("the temp worktree should be gone")
	}
	if out := gitTestRun(t, repo, "worktree", "list"); strings.Contains(out, "px0-review-") {
		t.Errorf("the worktree registration should be gone:\n%s", out)
	}
}

func TestGitFileLines(t *testing.T) {
	repo := reviewRepo(t)
	gitTestRun(t, repo, "checkout", "-q", "feature")
	if err := os.WriteFile(filepath.Join(repo, "bin.dat"), []byte("a\x00b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, repo, "add", "bin.dat")
	gitTestRun(t, repo, "commit", "-qm", "binary")

	lines := gitFileLines(repo, "HEAD", "a.go", 1<<20)
	if len(lines) != 10 || lines[0] != "package a" || lines[len(lines)-1] != "func Z() {}" {
		t.Errorf("the trailing newline must not add an empty last line: %q", lines)
	}
	if gitFileLines(repo, "HEAD", "a.go", 10) != nil {
		t.Errorf("a file over the size cap must be skipped")
	}
	if gitFileLines(repo, "HEAD", "bin.dat", 1<<20) != nil {
		t.Errorf("a binary file must be skipped")
	}
	if gitFileLines(repo, "HEAD", "nope.go", 1<<20) != nil {
		t.Errorf("a missing file must be skipped")
	}
	if files := gitTreeFiles(repo, "HEAD"); !files["a.go"] || !files["c.go"] || files["b.go"] {
		t.Errorf("unexpected tree listing: %v", files)
	}
	if gitTreeFiles(repo, "no-such-rev") != nil {
		t.Errorf("an unknown revision lists nothing")
	}
}

func TestPrepareLocalReviewErrors(t *testing.T) {
	repo := reviewRepo(t)
	for name, tc := range map[string]struct{ base, head string }{
		"option as head": {"main", "--upload-pack=x"},
		"option as base": {"-x", "feature"},
		"unknown head":   {"main", "nope"},
		"unknown base":   {"nope", "feature"},
	} {
		if _, err := prepareLocalReview(&reviewDoc{}, tc.base, tc.head, repo); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := prepareLocalReview(&reviewDoc{}, "main", "feature", t.TempDir()); err == nil {
		t.Errorf("a directory outside any repository should be refused")
	}
	// base and head come from the file when no flag overrides them
	doc := &reviewDoc{reviewMeta: reviewMeta{Base: "main", Head: "feature"}}
	p, err := prepareLocalReview(doc, "", "", repo)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.meta.HeadRef != "feature" || p.meta.BaseRef != "main" {
		t.Errorf("refs: %s <- %s", p.meta.BaseRef, p.meta.HeadRef)
	}
}

func TestPRThreadContextLocalReview(t *testing.T) {
	repo := reviewRepo(t)
	srv, p, rs, _ := reviewServer(t, repo, reviewFixture)
	waitResolved(t, rs)
	if srv.forgePR() {
		t.Fatalf("a local review is not a forge PR: push and pull must stay plain-workspace")
	}
	ctx := srv.prThreadContext("")
	for _, want := range []string{
		"local review of feature against main",
		"the entire change under review",
		"An automated review of this change is loaded in px0",
		"Review of feature",
		"Two things to look at.",
		"not as ground truth",
		"git diff " + p.diffBase,
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context is missing %q:\n%s", want, ctx)
		}
	}
	if strings.Contains(ctx, "pull request #") {
		t.Errorf("a local review must not claim to be a pull request:\n%s", ctx)
	}
	if strings.Contains(ctx, "secret-session") {
		t.Errorf("the session id must never reach a prompt")
	}
}

func TestPRExistingCommentsLocalIsEmpty(t *testing.T) {
	repo := reviewRepo(t)
	srv, _, _, _ := reviewServer(t, repo, `{"version":1}`)
	req := httptest.NewRequest("GET", "/api/pr/existing-comments", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"issueComments":[]`) {
		t.Fatalf("a local review has no forge comments: %d %s", w.Code, w.Body.String())
	}
}

func TestReviewMetaFlags(t *testing.T) {
	repo := reviewRepo(t)
	srv, _, _, _ := reviewServer(t, repo, `{"version":1}`)
	req := httptest.NewRequest("GET", "/api/meta", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var meta map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	pr, _ := meta["pr"].(map[string]any)
	if meta["review"] != true || pr == nil || pr["local"] != true {
		t.Errorf("meta should flag a local review: review=%v pr=%v", meta["review"], pr)
	}
}

func TestParseReviewPR(t *testing.T) {
	ok := func(src string) *reviewDoc {
		t.Helper()
		doc, err := parseReview([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	if d := ok(`{"version":1,"pr":7}`); d.PR == nil || d.PR.Number != 7 || len(d.Warnings) != 0 {
		t.Errorf("a pr number must load: %+v %v", d.PR, d.Warnings)
	}
	if d := ok(`{"version":1,"pr":"https://github.com/acme/widgets/pull/3"}`); d.PR == nil || d.PR.URL == "" || len(d.Warnings) != 0 {
		t.Errorf("a pr URL must load: %+v %v", d.PR, d.Warnings)
	}
	for _, src := range []string{`{"version":1,"pr":0}`, `{"version":1,"pr":-2}`, `{"version":1,"pr":"https://example.com/x"}`} {
		if d := ok(src); d.PR != nil || len(d.Warnings) != 1 {
			t.Errorf("%s: a bad pr must be dropped with one warning: %+v %v", src, d.PR, d.Warnings)
		}
	}
	for _, src := range []string{`{"version":1,"pr":true}`, `{"version":1,"pr":{"n":1}}`, `{"version":1,"pr":1.5}`} {
		if _, err := parseReview([]byte(src)); err == nil {
			t.Errorf("%s: a pr of the wrong type is a whole-file error", src)
		}
	}
}

func TestReviewPRTarget(t *testing.T) {
	repo := reviewRepo(t)
	if _, _, err := reviewPRTarget(&reviewPRRef{Number: 12}, repo); err == nil {
		t.Error("a pr number with no origin remote must be an error")
	}
	gitTestRun(t, repo, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	_, target, err := reviewPRTarget(&reviewPRRef{Number: 12}, repo)
	if err != nil {
		t.Fatal(err)
	}
	if target.Owner != "acme" || target.Repo != "widgets" || target.Number != 12 {
		t.Errorf("target = %+v", target)
	}
	_, target, err = reviewPRTarget(&reviewPRRef{URL: "https://github.com/o/r/pull/5"}, repo)
	if err != nil || target.Owner != "o" || target.Number != 5 {
		t.Errorf("a URL must not need the remote: %+v %v", target, err)
	}
	if _, _, err := reviewPRTarget(&reviewPRRef{URL: "https://example.com/x"}, repo); err == nil {
		t.Error("an unrecognised URL must be an error")
	}
}

func TestReviewPRChanged(t *testing.T) {
	a, b := &reviewPRRef{Number: 1}, &reviewPRRef{Number: 1}
	if reviewPRChanged(nil, nil) || reviewPRChanged(a, b) {
		t.Error("equal refs are not a change")
	}
	if !reviewPRChanged(nil, a) || !reviewPRChanged(a, &reviewPRRef{Number: 2}) {
		t.Error("different refs are a change")
	}
}
