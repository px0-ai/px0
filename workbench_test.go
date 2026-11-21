package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *wbStore {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return &wbStore{path: workbenchStorePath()}
}

func TestWorkbenchItemIDsAreStable(t *testing.T) {
	a := wbDirItem("/work/repo/")
	b := wbDirItem("/work/repo")
	if a.ID != b.ID || len(a.ID) != 12 {
		t.Fatalf("dir ids differ or wrong length: %q %q", a.ID, b.ID)
	}
	p1 := wbPRItem(PRTarget{Provider: "github", Owner: "Px0-AI", Repo: "px0", Number: 7, URL: "https://github.com/Px0-AI/px0/pull/7"})
	p2 := wbPRItem(PRTarget{Provider: "github", Owner: "px0-ai", Repo: "PX0", Number: 7, URL: "https://github.com/px0-ai/px0/pull/7/files"})
	if p1.ID != p2.ID {
		t.Fatalf("same PR got different ids: %q %q", p1.ID, p2.ID)
	}
	if p1.ID == wbDirItem("px0-ai/px0#7").ID {
		t.Fatal("dir and PR ids must not collide")
	}
}

func TestWorkbenchStoreRecordKeepsPinAndTitle(t *testing.T) {
	st := newTestStore(t)
	it := wbPRItem(PRTarget{Provider: "github", Owner: "o", Repo: "r", Number: 1, URL: "u"})
	it.Title = "Fix things"
	if err := st.Record(it); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(f *wbFile) { f.Items[0].Pinned = true }); err != nil {
		t.Fatal(err)
	}
	it.Title = "" // reopened before the title is known again
	if err := st.Record(it); err != nil {
		t.Fatal(err)
	}
	got, ok := st.Get(it.ID)
	if !ok || !got.Pinned || got.Title != "Fix things" || got.LastOpened == 0 {
		t.Fatalf("got %+v", got)
	}
	if n := len(st.Load().Items); n != 1 {
		t.Fatalf("want 1 item, got %d", n)
	}
}

func TestWorkbenchStoreTrimsOldUnpinned(t *testing.T) {
	st := newTestStore(t)
	err := st.Update(func(f *wbFile) {
		for i := 0; i < wbMaxRecent+10; i++ {
			it := wbDirItem(filepath.Join("/d", string(rune('a'+i%26)), strings.Repeat("x", i)))
			it.LastOpened = int64(i)
			it.Pinned = i == 0 // the oldest, but pinned
			f.Items = append(f.Items, it)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	items := st.Load().Items
	if len(items) != wbMaxRecent+1 {
		t.Fatalf("want %d items, got %d", wbMaxRecent+1, len(items))
	}
	if items[0].LastOpened != wbMaxRecent+9 {
		t.Fatalf("not most recent first: %d", items[0].LastOpened)
	}
	pinned := false
	for _, it := range items {
		pinned = pinned || it.Pinned
	}
	if !pinned {
		t.Fatal("pinned item was dropped")
	}
}

func TestWorkbenchStoreSetsAsideCorruptFile(t *testing.T) {
	st := newTestStore(t)
	os.MkdirAll(filepath.Dir(st.path), 0o700)
	os.WriteFile(st.path, []byte("{not json"), 0o600)
	if n := len(st.Load().Items); n != 0 {
		t.Fatalf("want empty, got %d", n)
	}
	if _, err := os.Stat(st.path + ".bad"); err != nil {
		t.Fatalf("corrupt file not kept: %v", err)
	}
}

func TestRemoteMatchesRepo(t *testing.T) {
	cases := []struct {
		origin string
		want   bool
	}{
		{"git@github.com:px0-ai/px0.git\n", true},
		{"https://github.com/PX0-AI/px0", true},
		{"ssh://git@github.com/px0-ai/px0.git", true},
		{"https://github.com/px0-ai/px0-docs.git", false},
		{"https://github.com/other/px0.git", false},
	}
	for _, c := range cases {
		if got := remoteMatchesRepo(c.origin, "px0-ai", "px0"); got != c.want {
			t.Errorf("%q: got %v", c.origin, got)
		}
	}
}

func TestWorkbenchBaseOf(t *testing.T) {
	cases := map[string]string{
		"/w/abc/":          "/",
		"/rev-1/w/abc/":    "/rev-1/",
		"/a/w/b/w/abc123/": "/a/w/b/",
		"/":                "/",
	}
	for in, want := range cases {
		if got := workbenchBaseOf(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := map[string]string{
		"":       "/base",
		"sub/":   "/base/sub",
		"/abs/x": "/abs/x",
		"~":      home,
		"~/code": filepath.Join(home, "code"),
	}
	for in, want := range cases {
		got, err := expandPath(in, "/base")
		if err != nil || got != want {
			t.Errorf("%q: got %q (%v), want %q", in, got, err, want)
		}
	}
}

func TestLineWriterSplitsLinesAndSpinnerFrames(t *testing.T) {
	var got []string
	lw := &lineWriter{fn: func(s string) { got = append(got, s) }}
	io.WriteString(lw, "one\r\ntw")
	io.WriteString(lw, "o\n⠋ Cloning\r⠙ Cloning\rpx0-ready 127.0.0.1:1\nlast")
	lw.Flush()
	want := []string{"one", "two", "px0-ready 127.0.0.1:1", "last"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
}

func TestWorkbenchResolveTarget(t *testing.T) {
	newTestStore(t)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	wb := &workbench{cwd: dir}

	it, err := wb.resolveTarget("sub")
	if err != nil || it.Kind != "dir" || filepath.Base(it.Path) != "sub" {
		t.Fatalf("relative dir: %+v %v", it, err)
	}
	it, err = wb.resolveTarget("https://github.com/px0-ai/px0/pull/12")
	if err != nil || it.Kind != "pr" || it.Repo != "px0-ai/px0" || it.Number != 12 {
		t.Fatalf("pr: %+v %v", it, err)
	}
	for _, bad := range []string{"", "https://github.com/px0-ai/px0", filepath.Join(dir, "missing")} {
		if _, err := wb.resolveTarget(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestWorkbenchBrowseListsOnlyFolders(t *testing.T) {
	newTestStore(t)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "repo", ".git"), 0o755)
	os.Mkdir(filepath.Join(dir, "plain"), 0o755)
	os.Mkdir(filepath.Join(dir, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(dir, "file.txt"), nil, 0o644)
	wb := newWorkbench("/", dir, "", nil)

	get := func(target, host string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r.Host = host
		w := httptest.NewRecorder()
		wb.ServeHTTP(w, r)
		return w
	}
	w := get("/api/wb/browse?path="+dir, "127.0.0.1:7777")
	var res struct {
		Entries []wbEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if len(res.Entries) != 2 || res.Entries[0].Name != "plain" || res.Entries[1].Name != "repo" || !res.Entries[1].Git {
		t.Fatalf("got %+v", res.Entries)
	}
	if w := get("/api/wb/browse?path="+dir, "evil.example:7777"); w.Code != http.StatusForbidden {
		t.Fatalf("rebound host: got %d", w.Code)
	}
}

func TestWorkbenchOpenRequiresLocalPost(t *testing.T) {
	newTestStore(t)
	wb := newWorkbench("/", t.TempDir(), "", nil)
	r := httptest.NewRequest("POST", "/api/wb/open", strings.NewReader(`{"target":"."}`))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	wb.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d", w.Code)
	}
	if n := len(wb.store.Load().Items); n != 0 {
		t.Fatalf("recorded %d items", n)
	}
}

func TestWorkbenchUnknownWorkspace(t *testing.T) {
	newTestStore(t)
	wb := newWorkbench("/", t.TempDir(), "", nil)
	r := httptest.NewRequest("GET", "/w/000000000000/", nil)
	r.Host = "127.0.0.1:7777"
	w := httptest.NewRecorder()
	wb.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
	r = httptest.NewRequest("GET", "/w/000000000000/api/meta", nil)
	w = httptest.NewRecorder()
	wb.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("api on stopped workspace: got %d", w.Code)
	}
}

// End to end: a real px0 binary opened as a workspace, reached through the
// workbench's proxy, then stopped through its stdin.
func TestWorkbenchRunsChildThroughProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the px0 binary")
	}
	newTestStore(t)
	exe := filepath.Join(t.TempDir(), "px0")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ws := t.TempDir()
	os.WriteFile(filepath.Join(ws, "hello.go"), []byte("package hello\n"), 0o644)

	wb := newWorkbench("/base/", ws, exe, []string{"-no-lsp", "-no-agent", "-no-telemetry"})
	srv := httptest.NewServer(wb)
	defer srv.Close()
	defer wb.Close()

	post := func(path, body string) map[string]any {
		r, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		r.Header.Set("Origin", srv.URL)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	opened := post("/base/api/wb/open", `{"target":"`+ws+`"}`)
	id, _ := opened["id"].(string)
	if id == "" || opened["href"] != "/base/w/"+id+"/" {
		t.Fatalf("open: %v", opened)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		wb.mu.Lock()
		c := wb.children[id]
		state, msg := c.state, c.err
		wb.mu.Unlock()
		if state == "running" {
			break
		}
		if state == "failed" || time.Now().After(deadline) {
			t.Fatalf("child %s: %s", state, msg)
		}
		time.Sleep(50 * time.Millisecond)
	}

	resp, err := http.Get(srv.URL + "/base/w/" + id + "/api/meta")
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Root, Workbench, BasePath string
	}
	json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()
	if meta.Workbench != "/base/" || meta.BasePath != "/base/w/"+id+"/" {
		t.Fatalf("meta: %+v", meta)
	}
	if resolved, _ := filepath.EvalSymlinks(ws); meta.Root != resolved {
		t.Fatalf("root %q, want %q", meta.Root, resolved)
	}
	// The status bar's totals cover the workbench and the running child.
	resp, err = http.Get(srv.URL + "/base/api/metrics")
	if err != nil {
		t.Fatal(err)
	}
	var metrics struct {
		RSSBytes uint64 `json:"rssBytes"`
		Parts    []struct {
			Name     string `json:"name"`
			RSSBytes uint64 `json:"rssBytes"`
		} `json:"parts"`
	}
	json.NewDecoder(resp.Body).Decode(&metrics)
	resp.Body.Close()
	if len(metrics.Parts) != 2 || metrics.RSSBytes != metrics.Parts[0].RSSBytes+metrics.Parts[1].RSSBytes || metrics.Parts[1].RSSBytes == 0 {
		t.Fatalf("metrics: %+v", metrics)
	}

	if m := post("/base/api/wb/stop", `{"id":"`+id+`"}`); m["ok"] != true {
		t.Fatalf("stop: %v", m)
	}
	wb.mu.Lock()
	state := wb.children[id].state
	wb.mu.Unlock()
	if state != "stopped" {
		t.Fatalf("after stop: %s", state)
	}
}

// A target that fails on its first open leaves the list; the holding page
// still explains why, without a Retry that could not work.
func TestWorkbenchForgetsNewItemThatFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the child")
	}
	newTestStore(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-px0")
	os.WriteFile(exe, []byte("#!/bin/sh\necho 'px0: px0: github: fetch PR #9: 404 Not Found' >&2\nexit 1\n"), 0o755)
	wb := newWorkbench("/", dir, exe, nil)
	defer wb.Close()

	it := wbPRItem(PRTarget{Provider: "github", Owner: "o", Repo: "r", Number: 9, URL: "https://github.com/o/r/pull/9"})
	wb.store.Record(it)
	wb.rememberClone(it.Repo, gitRepoWithRemotes(t, map[string]string{"origin": "https://github.com/o/r.git"}))
	c := wb.start(it, true)
	select {
	case <-c.done:
	case <-time.After(10 * time.Second):
		t.Fatal("child never exited")
	}
	if c.state != "failed" || c.err != "github: fetch PR #9: 404 Not Found" {
		t.Fatalf("state %q err %q", c.state, c.err)
	}
	if _, ok := wb.store.Get(it.ID); ok {
		t.Fatal("failed new item still listed")
	}
	r := httptest.NewRequest("GET", "/w/"+it.ID+"/", nil)
	r.Host = "127.0.0.1:7777"
	w := httptest.NewRecorder()
	wb.ServeHTTP(w, r)
	body := w.Body.String()
	if !strings.Contains(body, "404 Not Found") || strings.Contains(body, "retry=1") {
		t.Fatalf("holding page:\n%s", body)
	}
}

// gitRepoWithRemotes makes an empty git repository with the given remotes.
func gitRepoWithRemotes(t *testing.T, remotes map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	for name, u := range remotes {
		if out, err := exec.Command("git", "-C", dir, "remote", "add", name, u).CombinedOutput(); err != nil {
			t.Fatalf("git remote add: %v %s", err, out)
		}
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	return resolved
}

func TestFindPRRemotePrefersOriginThenUpstream(t *testing.T) {
	fork := gitRepoWithRemotes(t, map[string]string{
		"origin":   "git@github.com:me/px0.git",
		"upstream": "https://github.com/px0-ai/px0.git",
	})
	if top, remote := findPRRemote(fork, "px0-ai", "px0"); top != fork || remote != "upstream" {
		t.Fatalf("fork: %q %q", top, remote)
	}
	both := gitRepoWithRemotes(t, map[string]string{
		"origin":   "https://github.com/px0-ai/px0",
		"upstream": "https://github.com/px0-ai/px0.git",
	})
	if _, remote := findPRRemote(both, "px0-ai", "px0"); remote != "origin" {
		t.Fatalf("both: %q", remote)
	}
	other := gitRepoWithRemotes(t, map[string]string{"origin": "https://github.com/px0-ai/px0-docs.git"})
	if top, _ := findPRRemote(other, "px0-ai", "px0"); top != "" {
		t.Fatalf("other repo matched: %q", top)
	}
}

func TestScanForCloneFindsNestedClone(t *testing.T) {
	root := t.TempDir()
	clone := filepath.Join(root, "work", "px0")
	os.MkdirAll(filepath.Join(root, "work", "node_modules", "px0"), 0o755)
	if out, err := exec.Command("git", "init", "-q", clone).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	exec.Command("git", "-C", clone, "remote", "add", "origin", "git@github.com:px0-ai/px0.git").Run()
	want, _ := filepath.EvalSymlinks(clone)
	if got := scanForClone(context.Background(), []string{root}, "px0-ai/px0"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := scanForClone(context.Background(), []string{root}, "px0-ai/other"); got != "" {
		t.Fatalf("found a clone of the wrong repo: %q", got)
	}
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Host = "127.0.0.1:7777"
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Opening a PR with no known clone asks for one and records nothing; giving
// a folder that is not a clone is refused; a real clone is remembered.
func TestWorkbenchOpenPRAsksForClone(t *testing.T) {
	newTestStore(t)
	t.Setenv("HOME", t.TempDir()) // keep the scan away from real code folders
	wb := newWorkbench("/", t.TempDir(), "/nonexistent/px0", nil)
	defer wb.Close()
	pr := "https://github.com/acme/widget/pull/5"

	w := postJSON(t, wb, "/api/wb/open", `{"target":"`+pr+`"}`)
	var res map[string]any
	json.Unmarshal(w.Body.Bytes(), &res)
	if res["needClone"] != true || res["repo"] != "acme/widget" {
		t.Fatalf("got %s", w.Body.String())
	}
	if n := len(wb.store.Load().Items); n != 0 {
		t.Fatalf("recorded %d items before a clone was chosen", n)
	}

	notClone := t.TempDir()
	if w := postJSON(t, wb, "/api/wb/open", `{"target":"`+pr+`","clone":"`+notClone+`"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("non-clone accepted: %d %s", w.Code, w.Body.String())
	}

	clone := gitRepoWithRemotes(t, map[string]string{"origin": "git@github.com:acme/widget.git"})
	if w := postJSON(t, wb, "/api/wb/open", `{"target":"`+pr+`","clone":"`+clone+`"}`); w.Code != http.StatusOK {
		t.Fatalf("clone refused: %d %s", w.Code, w.Body.String())
	}
	if got := wb.store.Load().Clones["acme/widget"]; got != clone {
		t.Fatalf("remembered %q, want %q", got, clone)
	}
	// Next time it is found without asking.
	w = postJSON(t, wb, "/api/wb/open", `{"target":"https://github.com/acme/widget/pull/6"}`)
	if strings.Contains(w.Body.String(), "needClone") {
		t.Fatalf("asked again: %s", w.Body.String())
	}
}

func TestWorkbenchCompleteFolders(t *testing.T) {
	newTestStore(t)
	dir := t.TempDir()
	for _, d := range []string{"Projects", "proto", "public", ".private", "other"} {
		os.Mkdir(filepath.Join(dir, d), 0o755)
	}
	os.WriteFile(filepath.Join(dir, "profile.txt"), nil, 0o644)
	os.MkdirAll(filepath.Join(dir, "proto", ".git"), 0o755)
	wb := newWorkbench("/", dir, "", nil)

	complete := func(q string) []wbEntry {
		r := httptest.NewRequest("GET", "/api/wb/complete?q="+url.QueryEscape(q), nil)
		r.Host = "127.0.0.1:7777"
		w := httptest.NewRecorder()
		wb.ServeHTTP(w, r)
		var res struct{ Entries []wbEntry }
		json.Unmarshal(w.Body.Bytes(), &res)
		return res.Entries
	}
	names := func(es []wbEntry) string {
		var n []string
		for _, e := range es {
			n = append(n, e.Path)
		}
		return strings.Join(n, ",")
	}
	if got := names(complete("pr")); got != "proto/,Projects/" {
		t.Fatalf("relative prefix: %s", got)
	}
	if got := names(complete(dir + "/P")); got != dir+"/Projects/,"+dir+"/proto/,"+dir+"/public/" {
		t.Fatalf("absolute, case-insensitive: %s", got)
	}
	if got := names(complete(dir + "/.p")); got != dir+"/.private/" {
		t.Fatalf("hidden only when typed: %s", got)
	}
	if es := complete("proto"); len(es) != 1 || !es[0].Git {
		t.Fatalf("git flag: %+v", es)
	}
	if got := complete("https://github.com/x"); len(got) != 0 {
		t.Fatalf("URL completed: %v", got)
	}
}

func TestWorkbenchSettingsOnlyTakesTheme(t *testing.T) {
	newTestStore(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	wb := newWorkbench("/", t.TempDir(), "", nil)
	if w := postJSON(t, wb, "/api/settings", `{"github.token":"x"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("token accepted: %d", w.Code)
	}
	if w := postJSON(t, wb, "/api/settings", `{"workbench.colorTheme":"rose-pine"}`); w.Code != http.StatusOK {
		t.Fatalf("theme refused: %d %s", w.Code, w.Body.String())
	}
	if got := wbTheme(); got != "rose-pine" {
		t.Fatalf("theme %q", got)
	}
}

func TestWorkbenchPinAndRemove(t *testing.T) {
	st := newTestStore(t)
	wb := newWorkbench("/", t.TempDir(), "", nil)
	wb.store = st

	it := wbDirItem("/work/test-repo")
	if err := st.Record(it); err != nil {
		t.Fatal(err)
	}

	// Pin item
	w := postJSON(t, wb, "/api/wb/pin", `{"id":"`+it.ID+`","pinned":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("pin failed: %d %s", w.Code, w.Body.String())
	}
	got, ok := st.Get(it.ID)
	if !ok || !got.Pinned {
		t.Fatalf("item not pinned: %+v", got)
	}

	// Unpin item
	w = postJSON(t, wb, "/api/wb/pin", `{"id":"`+it.ID+`","pinned":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("unpin failed: %d", w.Code)
	}
	got, _ = st.Get(it.ID)
	if got.Pinned {
		t.Fatal("item should be unpinned")
	}

	// Remove item
	w = postJSON(t, wb, "/api/wb/remove", `{"id":"`+it.ID+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("remove failed: %d", w.Code)
	}
	if _, ok := st.Get(it.ID); ok {
		t.Fatal("item was not removed")
	}
}

func TestWorkbenchLockLifecycle(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// No lock initially
	if url := runningWorkbench(); url != "" {
		t.Errorf("expected empty runningWorkbench, got %q", url)
	}

	// Write lock with a mock server that answers ping
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/wb/ping" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"workbench":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	writeWorkbenchLock(ts.URL)

	// Verify runningWorkbench finds it
	if u := runningWorkbench(); u != ts.URL {
		t.Errorf("runningWorkbench = %q, want %q", u, ts.URL)
	}

	// Remove lock
	removeWorkbenchLock()
	if u := runningWorkbench(); u != "" {
		t.Errorf("runningWorkbench after remove = %q, want empty", u)
	}
}


func TestViewQueryKeepsOnlyFileAndLine(t *testing.T) {
	q := url.Values{"path": {"a b/c.md"}, "line": {"7"}, "retry": {"1"}, "x": {"y"}}
	if got, want := viewQuery(q), "line=7&path=a+b%2Fc.md"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := viewQuery(url.Values{"retry": {"1"}}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestHandOffToWorkbench(t *testing.T) {
	newTestStore(t)
	wb := newWorkbench("/", t.TempDir(), "", nil)
	srv := httptest.NewServer(wb)
	defer srv.Close()
	defer wb.Close() // its workspaces fail to start here and rewrite the store as they do

	dir := t.TempDir()
	dest, err := handOffToWorkbench(srv.URL+"/", dir, "docs/a.md", 3)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dest)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != strings.TrimPrefix(srv.URL, "http://") || !strings.HasPrefix(u.Path, "/w/") {
		t.Fatalf("got %s", dest)
	}
	if q := u.Query(); q.Get("path") != "docs/a.md" || q.Get("line") != "3" {
		t.Fatalf("file and line lost: %s", dest)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/w/" + wbDirItem(resolved).ID + "/"; u.Path != want {
		t.Fatalf("workspace %s, want %s", u.Path, want)
	}

	// Handing the same directory off again reuses its workspace.
	again, err := handOffToWorkbench(srv.URL+"/", dir, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(again, srv.URL+u.Path) {
		t.Fatalf("second hand-off went elsewhere: %s", again)
	}

	if _, err := handOffToWorkbench(srv.URL+"/", filepath.Join(dir, "missing"), "", 0); err == nil {
		t.Fatal("a target that does not exist was accepted")
	}
}

// TestMain lets a test start this binary as a workbench, or as a workspace
// that never becomes ready, standing in for px0.
func TestMain(m *testing.M) {
	if os.Getenv("PX0_TEST_WORKBENCH") == "1" {
		runWorkbench("127.0.0.1", 0, "/", false, false, nil)
		return
	}
	if os.Getenv("PX0_TEST_HANG") == "1" {
		io.Copy(io.Discard, os.Stdin) // the workbench closes this to stop a workspace
		return
	}
	os.Exit(m.Run())
}

func TestStartBackgroundWorkbench(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("PX0_TEST_WORKBENCH", "1")
	if runningWorkbench() != "" {
		t.Fatal("a workbench is running before the test started one")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	u := startBackgroundWorkbench(exe, nil)
	b, _ := os.ReadFile(workbenchLockPath())
	var l wbLock
	_ = json.Unmarshal(b, &l)
	t.Cleanup(func() {
		if p, err := os.FindProcess(l.PID); err == nil && l.PID != 0 {
			_ = p.Kill()
			time.Sleep(200 * time.Millisecond) // let it release the state directory
		}
	})
	if u == "" {
		t.Fatal("no workbench came up")
	}
	if l.PID == 0 || l.PID == os.Getpid() || l.URL != u {
		t.Fatalf("lock %+v does not describe the workbench at %s", l, u)
	}
	if got := runningWorkbench(); got != u {
		t.Fatalf("runningWorkbench = %q, want %q", got, u)
	}

	// A target handed to it opens as one of its workspaces.
	dest, err := handOffToWorkbench(u, t.TempDir(), "x.go", 5)
	if err != nil || !strings.Contains(dest, "/w/") {
		t.Fatalf("hand-off to the background workbench: %q, %v", dest, err)
	}
}

func TestStartBackgroundWorkbenchGivesUp(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if u := startBackgroundWorkbench(filepath.Join(t.TempDir(), "no-such-px0"), nil); u != "" {
		t.Fatalf("got %q from a binary that does not exist", u)
	}
}

func BenchmarkViewQuery(b *testing.B) {
	q := url.Values{"path": {"docs/a.md"}, "line": {"7"}, "retry": {"1"}}
	for i := 0; i < b.N; i++ {
		viewQuery(q)
	}
}

func TestWorkbenchPassthrough(t *testing.T) {
	if got := workbenchPassthrough(false, false, false, false, false, "", nil); got != nil {
		t.Fatalf("nothing given, got %v", got)
	}
	set := map[string]string{"port": "0", "base-path": "/x/", "host": "0.0.0.0", "no-open": "true", "r": "true"}
	got := workbenchPassthrough(true, true, true, true, true, "claude", set)
	want := []string{"-no-lsp", "-no-git", "-no-agent", "-no-telemetry", "-no-update",
		"-agent", "claude", "-host", "0.0.0.0", "-port", "0", "-base-path", "/x/"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReuseWorkbench(t *testing.T) {
	newTestStore(t)
	wb := newWorkbench("/", t.TempDir(), "", nil)
	srv := httptest.NewServer(wb)
	defer srv.Close()
	defer wb.Close()
	root := t.TempDir()
	never := func() string { t.Helper(); t.Fatal("started a workbench although one is running"); return "" }

	// One is running: it is used and none is started.
	dest, err := reuseWorkbench(func() string { return srv.URL + "/" }, never, root, "a.go", 2)
	if err != nil || !strings.Contains(dest, "/w/") || !strings.Contains(dest, "path=a.go") {
		t.Fatalf("running workbench: %q, %v", dest, err)
	}

	// None is running: one is started, once, and used.
	started := 0
	dest, err = reuseWorkbench(func() string { return "" }, func() string { started++; return srv.URL + "/" }, root, "", 0)
	if err != nil || started != 1 || !strings.Contains(dest, "/w/") {
		t.Fatalf("started workbench: %q, %v (started %d times)", dest, err, started)
	}

	// None is running and none starts: an error, so the caller serves the target itself.
	if dest, err = reuseWorkbench(func() string { return "" }, func() string { return "" }, root, "", 0); err == nil {
		t.Fatalf("got %q without a workbench", dest)
	}

	// The workbench is gone by the time it is called: also an error.
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	if dest, err = reuseWorkbench(func() string { return gone.URL + "/" }, never, root, "", 0); err == nil {
		t.Fatalf("got %q from a workbench that is down", dest)
	}
}

// A workspace that is still starting shows a page that refreshes itself; the
// file and line a hand-off asked for must survive those refreshes.
func TestWorkbenchHoldingPageKeepsFileAndLine(t *testing.T) {
	newTestStore(t)
	t.Setenv("PX0_TEST_HANG", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wb := newWorkbench("/", t.TempDir(), exe, nil)
	defer wb.Close()
	it := wbDirItem(t.TempDir())
	if err := wb.store.Record(it); err != nil {
		t.Fatal(err)
	}

	get := func(target string) string {
		r := httptest.NewRequest("GET", target, nil)
		r.Host = "127.0.0.1:7777"
		w := httptest.NewRecorder()
		wb.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", target, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	base := "/w/" + it.ID + "/"

	body := get(base + "?path=docs%2Fa.md&line=3&retry=&other=x")
	if !strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "url="+base+"?line=3&amp;path=docs%2Fa.md\"") {
		t.Fatalf("refresh drops the file and line:\n%s", body)
	}
	if strings.Contains(body, "other=x") {
		t.Fatal("kept a parameter that does not position the viewer")
	}
	if body = get(base); !strings.Contains(body, "url="+base+"\"") {
		t.Fatalf("a plain open gained a query:\n%s", body)
	}
}
