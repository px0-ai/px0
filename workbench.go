package main

// The workbench is what a bare `px0` opens: a launcher page listing the
// directories and pull requests opened before, with a folder browser and a box
// for a path or PR URL. It holds no index and no language servers of its own.
// Every workspace opened from it runs as a child px0 process bound to
// 127.0.0.1 on a free port under the base path /w/<id>/, and the workbench
// reverse-proxies that path to it, so the whole thing is reachable on one port.
//
// Children are tied to the workbench through their stdin: when the pipe closes,
// because the workbench stopped them or died, they shut down the same way a
// Ctrl-C would, which removes PR checkouts and stops language servers. A child
// nobody has had open for wbIdleAfter is stopped to free its memory; opening it
// again starts a new one.
//
// What was opened is recorded in workbench.json (see workbenchStorePath). It
// holds what to reopen, never live state: process IDs and ports are not saved.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"px0/internal/metrics"
	"px0/internal/settings"
	"px0/internal/ui"
)

const (
	wbReadyPrefix  = "px0-ready "     // a child prints this, then its address, once it is listening
	wbStartTimeout = 3 * time.Minute  // a PR checkout may clone a large repository
	wbIdleAfter    = 30 * time.Minute // stop a child nobody has had open for this long
	wbStopGrace    = 5 * time.Second  // how long a child gets to clean up before it is killed
	wbMaxRecent    = 50               // unpinned entries kept in workbench.json
	wbMaxBrowse    = 5000             // directory entries listed by the folder browser
	wbLogLines     = 20               // child output lines kept for error reports
	wbPingTimeout  = 400 * time.Millisecond
)

// wbItem is one entry in workbench.json: a directory or a pull request.
type wbItem struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // "dir" or "pr"
	Path       string `json:"path,omitempty"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	Repo       string `json:"repo,omitempty"` // owner/repo, PRs only
	Number     int    `json:"number,omitempty"`
	Pinned     bool   `json:"pinned,omitempty"`
	LastOpened int64  `json:"lastOpened"` // unix milliseconds
}

type wbFile struct {
	Version int      `json:"version"`
	Items   []wbItem `json:"items"`
	// Clones maps a lowercased "owner/repo" to the local clone its pull
	// requests are checked out from (see workbench_clone.go).
	Clones map[string]string `json:"clones,omitempty"`
}

// workbenchStateDir is where workbench.json and the instance lock live:
// $XDG_STATE_HOME/px0 when set, otherwise ~/.px0, like the session files.
func workbenchStateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "px0")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".px0")
	}
	return filepath.Join(os.TempDir(), "px0")
}

func workbenchStorePath() string { return filepath.Join(workbenchStateDir(), "workbench.json") }
func workbenchLockPath() string  { return filepath.Join(workbenchStateDir(), "workbench.lock") }

// wbItemID is stable for a directory or PR, so its /w/<id>/ URL and the
// session file keyed on that base path (review drafts) survive restarts.
func wbItemID(kind, key string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + key))
	return hex.EncodeToString(h[:6])
}

func wbDirItem(root string) wbItem {
	root = filepath.Clean(root)
	return wbItem{ID: wbItemID("dir", root), Kind: "dir", Path: root, Title: filepath.Base(root)}
}

func wbPRItem(pt PRTarget) wbItem {
	key := strings.ToLower(fmt.Sprintf("%s/%s/%s#%d", pt.Provider, pt.Owner, pt.Repo, pt.Number))
	return wbItem{
		ID:     wbItemID("pr", key),
		Kind:   "pr",
		URL:    pt.URL,
		Repo:   pt.Owner + "/" + pt.Repo,
		Number: pt.Number,
	}
}

// wbStore reads and writes workbench.json. It keeps no copy in memory: every
// change re-reads the file first, so entries recorded meanwhile by a plain
// `px0 <dir>` in another terminal are not overwritten.
type wbStore struct {
	mu   sync.Mutex
	path string
}

func (st *wbStore) load() wbFile {
	f := wbFile{Version: 1}
	b, err := os.ReadFile(st.path)
	if err != nil {
		return f
	}
	if err := json.Unmarshal(b, &f); err != nil {
		// Keep the unreadable file for inspection rather than overwriting it.
		os.Rename(st.path, st.path+".bad")
		return wbFile{Version: 1}
	}
	f.Version = 1
	return f
}

func (st *wbStore) Load() wbFile {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.load()
}

func (st *wbStore) Update(fn func(*wbFile)) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	f := st.load()
	fn(&f)
	trimRecent(&f)
	return writeFileAtomic(st.path, f)
}

func (st *wbStore) Get(id string) (wbItem, bool) {
	for _, it := range st.Load().Items {
		if it.ID == id {
			return it, true
		}
	}
	return wbItem{}, false
}

// Record adds an item or refreshes an existing one, marking it opened now.
// Pinned state survives; a title is only replaced by a non-empty one.
func (st *wbStore) Record(it wbItem) error {
	return st.Update(func(f *wbFile) {
		it.LastOpened = time.Now().UnixMilli()
		for i := range f.Items {
			if f.Items[i].ID == it.ID {
				it.Pinned = f.Items[i].Pinned
				if it.Title == "" {
					it.Title = f.Items[i].Title
				}
				f.Items[i] = it
				return
			}
		}
		f.Items = append(f.Items, it)
	})
}

// trimRecent orders items most recent first and drops the oldest unpinned ones.
func trimRecent(f *wbFile) {
	sort.SliceStable(f.Items, func(i, j int) bool { return f.Items[i].LastOpened > f.Items[j].LastOpened })
	kept := f.Items[:0]
	unpinned := 0
	for _, it := range f.Items {
		if !it.Pinned {
			if unpinned >= wbMaxRecent {
				continue
			}
			unpinned++
		}
		kept = append(kept, it)
	}
	f.Items = kept
}

func writeFileAtomic(p string, v any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".workbench-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// recordRecent notes a workspace opened outside the workbench (`px0 <dir>`,
// `px0 <pr-url>`) so it shows up in the workbench later. Best effort.
func recordRecent(it wbItem) {
	st := &wbStore{path: workbenchStorePath()}
	_ = st.Record(it)
}

/* ---------- child processes ---------- */

type wbChild struct {
	id       string
	title    string // for the holding page, which may outlive the item (see forget)
	gone     bool   // failed, and the item was taken out of the list with it
	state    string // "starting", "running", "failed", "stopped"
	err      string
	progress string // what a starting child is waiting on, e.g. a clone
	noClone  bool   // failed because no local clone of the PR's repository is known
	addr     string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	proxy    *httputil.ReverseProxy
	done     chan struct{}

	// cancel abandons a start still preparing (cloning), before there is a
	// process to stop.
	ctx    context.Context
	cancel context.CancelFunc

	active     int       // proxied requests in flight, open event streams included
	lastActive time.Time // when the last proxied request finished

	logMu sync.Mutex
	log   []string
}

func (c *wbChild) appendLog(line string) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	c.log = append(c.log, line)
	if len(c.log) > wbLogLines {
		c.log = c.log[len(c.log)-wbLogLines:]
	}
}

// lineWriter hands each complete line written to it to fn. A spinner redraws
// its line with \r, so only the text after the last \r counts.
type lineWriter struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.buf = append(lw.buf, p...)
	for {
		i := strings.IndexByte(string(lw.buf), '\n')
		if i < 0 {
			break
		}
		lw.emit(string(lw.buf[:i]))
		lw.buf = lw.buf[i+1:]
	}
	return len(p), nil
}

// Flush hands over a last line that did not end in a newline.
func (lw *lineWriter) Flush() {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if len(lw.buf) > 0 {
		lw.emit(string(lw.buf))
		lw.buf = nil
	}
}

func (lw *lineWriter) emit(line string) {
	line = strings.TrimRight(line, "\r")
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	lw.fn(line)
}

// failure picks the line that best explains why a child exited: the last
// "px0: ..." line fatal() wrote, otherwise the last thing it said.
func (c *wbChild) failure() string {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	for i := len(c.log) - 1; i >= 0; i-- {
		if strings.HasPrefix(c.log[i], "px0: ") {
			msg := c.log[i]
			for strings.HasPrefix(msg, "px0: ") {
				msg = strings.TrimPrefix(msg, "px0: ")
			}
			return msg
		}
	}
	for i := len(c.log) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(c.log[i]); s != "" {
			return s
		}
	}
	return "px0 exited unexpectedly"
}

type workbench struct {
	store     *wbStore
	basePath  string
	cwd       string
	exe       string
	childArgs []string // flags handed on to every child (-no-lsp, -agent, ...)

	mu       sync.Mutex
	children map[string]*wbChild
	closing  bool

	mux *http.ServeMux
}

func newWorkbench(basePath, cwd, exe string, childArgs []string) *workbench {
	wb := &workbench{
		store:     &wbStore{path: workbenchStorePath()},
		basePath:  cleanBasePath(basePath),
		cwd:       cwd,
		exe:       exe,
		childArgs: childArgs,
		children:  map[string]*wbChild{},
		mux:       http.NewServeMux(),
	}
	wb.routes()
	go wb.reapIdle()
	return wb
}

func (wb *workbench) route(sub string) string {
	return strings.TrimSuffix(wb.basePath, "/") + sub
}

func (wb *workbench) childBase(id string) string { return wb.route("/w/" + id + "/") }

func (wb *workbench) routes() {
	sub, _ := fs.Sub(assets, "web")
	staticPrefix := wb.route("/static/")
	wb.mux.Handle(staticPrefix, (&Server{}).handleStatic(sub, staticPrefix))
	wb.mux.HandleFunc(wb.route("/static/themes.css"), (&Server{}).handleThemes)
	wb.mux.HandleFunc(wb.route("/api/wb/ping"), wb.handlePing)
	wb.mux.HandleFunc(wb.route("/api/wb/state"), wb.handleState)
	wb.mux.HandleFunc(wb.route("/api/wb/browse"), wb.handleBrowse)
	wb.mux.HandleFunc(wb.route("/api/wb/complete"), wb.handleComplete)
	wb.mux.HandleFunc(wb.route("/api/metrics"), wb.handleMetrics)
	wb.mux.HandleFunc(wb.route("/api/settings"), wb.handleSettings)
	wb.mux.HandleFunc(wb.route("/api/wb/open"), wb.handleOpen)
	wb.mux.HandleFunc(wb.route("/api/wb/stop"), wb.handleStop)
	wb.mux.HandleFunc(wb.route("/api/wb/pin"), wb.handlePin)
	wb.mux.HandleFunc(wb.route("/api/wb/remove"), wb.handleRemove)
	wb.mux.HandleFunc(wb.route("/w/"), wb.handleWorkspace)
	wb.mux.HandleFunc(wb.route("/"), wb.handleIndex)
	if wb.basePath != "/" {
		trimmed := strings.TrimSuffix(wb.basePath, "/")
		wb.mux.HandleFunc(trimmed, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, wb.basePath, http.StatusMovedPermanently)
		})
	}
}

func (wb *workbench) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, wb.route("/w/")) {
		// Proxied responses carry the child's own headers.
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	}
	if ui.Verbose {
		start := time.Now()
		defer func() {
			ui.Status("info", "http", fmt.Sprintf("%s %s  (%s)", r.Method, r.URL.RequestURI(), ui.FmtDuration(time.Since(start))), 0, os.Stdout)
		}()
	}
	wb.mux.ServeHTTP(w, r)
}

// start launches the child for it unless one is already starting or running.
// With forget set, a child that fails before it is ready also takes the item
// out of the list: a mistyped PR URL should not linger there.
func (wb *workbench) start(it wbItem, forget bool) *wbChild {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	if c := wb.children[it.ID]; c != nil && (c.state == "starting" || c.state == "running") {
		return c
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &wbChild{id: it.ID, title: itemTitle(it), state: "starting", done: make(chan struct{}),
		ctx: ctx, cancel: cancel, lastActive: time.Now()}
	wb.children[it.ID] = c
	if wb.closing {
		c.state, c.err = "failed", "px0 is shutting down"
		cancel()
		close(c.done)
		return c
	}
	go wb.run(c, it, forget)
	return c
}

// run prepares what the child needs (a PR's local clone), starts it, waits
// for it to report its address, then waits for it to exit.
func (wb *workbench) run(c *wbChild, it wbItem, forget bool) {
	defer close(c.done)
	defer c.cancel()
	failed := func(msg string) {
		wb.setState(c, "failed", msg)
		if forget {
			wb.mu.Lock()
			c.gone = true
			wb.mu.Unlock()
			_ = wb.store.Update(func(f *wbFile) { f.Items = withoutItem(f.Items, it.ID) })
		}
	}

	args := append([]string{}, wb.childArgs...)
	args = append(args, "-workbench-child", "-host", "127.0.0.1", "-port", "0",
		"-no-open", "-no-update", "-no-color", "-base-path", wb.childBase(it.ID))
	dir := wb.cwd
	if it.Kind == "pr" {
		// checkoutPR makes a worktree of the clone in its working directory,
		// so start the child in the PR repository's local clone.
		clone, err := wb.prClone(c, it)
		if err != nil {
			if c.ctx.Err() != nil {
				wb.setState(c, "stopped", "")
				return
			}
			if errors.Is(err, errNoClone) {
				wb.mu.Lock()
				c.noClone = true
				wb.mu.Unlock()
			}
			failed(err.Error())
			return
		}
		dir = clone
		args = append(args, it.URL)
	} else {
		args = append(args, it.Path)
	}

	cmd := exec.Command(wb.exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	ready := make(chan string, 1)
	out := &lineWriter{fn: func(line string) {
		if i := strings.Index(line, wbReadyPrefix); i >= 0 {
			select {
			case ready <- strings.TrimSpace(line[i+len(wbReadyPrefix):]):
			default:
			}
			return
		}
		c.appendLog(line)
	}}
	cmd.Stdout = out
	cmd.Stderr = &lineWriter{fn: c.appendLog}
	// A language server or harness the child started could hold its output
	// open after it exits; do not let that keep Wait from returning.
	cmd.WaitDelay = wbStopGrace
	stdin, err := cmd.StdinPipe()
	if err == nil {
		wb.mu.Lock()
		if c.ctx.Err() != nil {
			err = context.Canceled
		} else if err = cmd.Start(); err == nil {
			c.cmd, c.stdin, c.progress = cmd, stdin, ""
		}
		wb.mu.Unlock()
	}
	if err == context.Canceled {
		wb.setState(c, "stopped", "")
		return
	}
	if err != nil {
		failed(err.Error())
		return
	}

	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		out.Flush()
		exited <- err
	}()

	timer := time.NewTimer(wbStartTimeout)
	defer timer.Stop()
	select {
	case addr := <-ready:
		target, err := url.Parse("http://" + addr)
		if err != nil {
			cmd.Process.Kill()
			<-exited
			failed("bad address from child: " + addr)
			return
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				// The child checks Host and Origin on every POST; keep the
				// browser's so they still agree with each other.
				pr.Out.Host = pr.In.Host
			},
			FlushInterval: -1, // event streams must not sit in a buffer
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				fail(w, http.StatusBadGateway, "workspace is not responding: "+err.Error())
			},
		}
		wb.mu.Lock()
		c.addr, c.proxy, c.state = addr, proxy, "running"
		wb.mu.Unlock()
		go wb.learnTitle(c, it)
	case err := <-exited:
		msg := c.failure()
		if err == nil && msg == "" {
			msg = "px0 exited before it was ready"
		}
		failed(msg)
		return
	case <-timer.C:
		cmd.Process.Kill()
		<-exited
		failed("timed out while starting: " + c.failure())
		return
	}

	err = <-exited
	if err != nil && !wb.isClosing() {
		wb.setState(c, "stopped", c.failure())
		return
	}
	wb.setState(c, "stopped", "")
}

func (wb *workbench) setState(c *wbChild, state, msg string) {
	wb.mu.Lock()
	c.state, c.err, c.proxy, c.progress = state, msg, nil, ""
	wb.mu.Unlock()
}

func (wb *workbench) setProgress(c *wbChild, msg string) {
	wb.mu.Lock()
	c.progress = msg
	wb.mu.Unlock()
}

func (wb *workbench) isClosing() bool {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	return wb.closing
}

// learnTitle asks a freshly started child for its PR title so the list shows
// more than a number.
func (wb *workbench) learnTitle(c *wbChild, it wbItem) {
	if it.Kind != "pr" {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + c.addr + wb.childBase(it.ID) + "api/meta")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var meta struct {
		PR *struct {
			Title string `json:"title"`
		} `json:"pr"`
	}
	if json.NewDecoder(resp.Body).Decode(&meta) != nil || meta.PR == nil || meta.PR.Title == "" {
		return
	}
	_ = wb.store.Update(func(f *wbFile) {
		for i := range f.Items {
			if f.Items[i].ID == it.ID {
				f.Items[i].Title = meta.PR.Title
			}
		}
	})
}

// stop closes the child's stdin, which it takes as the signal to shut down,
// and kills it if it is still around after wbStopGrace. A child still
// preparing (cloning) is cancelled instead.
func (wb *workbench) stop(id string) {
	wb.mu.Lock()
	c := wb.children[id]
	var cmd *exec.Cmd
	if c != nil {
		cmd = c.cmd
		c.cancel()
	}
	wb.mu.Unlock()
	if c == nil {
		return
	}
	if cmd != nil {
		c.stdin.Close()
	}
	select {
	case <-c.done:
	case <-time.After(wbStopGrace):
		if cmd != nil {
			cmd.Process.Kill()
		}
		<-c.done
	}
}

// Close stops every child, in parallel, and waits for them.
func (wb *workbench) Close() {
	wb.mu.Lock()
	wb.closing = true
	ids := make([]string, 0, len(wb.children))
	for id := range wb.children {
		ids = append(ids, id)
	}
	wb.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); wb.stop(id) }()
	}
	wg.Wait()
}

func (wb *workbench) reapIdle() {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for range tick.C {
		var idle []string
		wb.mu.Lock()
		for id, c := range wb.children {
			if c.state == "running" && c.active == 0 && time.Since(c.lastActive) > wbIdleAfter {
				idle = append(idle, id)
			}
		}
		wb.mu.Unlock()
		for _, id := range idle {
			ui.Status("info", "stopped idle workspace", id, 0, os.Stdout)
			wb.stop(id)
		}
	}
}

/* ---------- HTTP ---------- */

// trustedGet refuses reads whose Host is a name other than localhost: the
// folder browser lists the whole disk, and a DNS-rebound page must not read it.
func trustedGet(w http.ResponseWriter, r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host != "localhost" && net.ParseIP(host) == nil {
		fail(w, http.StatusForbidden, "open px0 by IP address or localhost")
		return false
	}
	return true
}

func (wb *workbench) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != wb.basePath {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(assets, "web/workbench.html")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	html := strings.Replace(string(b), `<base href="/">`, fmt.Sprintf(`<base href="%s">`, template.HTMLEscapeString(wb.basePath)), 1)
	html = strings.Replace(html, `data-theme="catppuccin-mocha"`, fmt.Sprintf(`data-theme="%s"`, template.HTMLEscapeString(wbTheme())), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, html)
}

func wbTheme() string {
	if t := settings.Read().WorkbenchColorTheme; t != nil && *t != "" {
		return *t
	}
	return "catppuccin-mocha"
}

func (wb *workbench) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"workbench": true, "version": version})
}

type wbItemView struct {
	wbItem
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
	Progress string `json:"progress,omitempty"`
	Href     string `json:"href"`
}

func (wb *workbench) view(it wbItem) wbItemView {
	v := wbItemView{wbItem: it, State: "stopped", Href: wb.childBase(it.ID)}
	wb.mu.Lock()
	if c := wb.children[it.ID]; c != nil {
		v.State, v.Error, v.Progress = c.state, c.err, c.progress
	}
	wb.mu.Unlock()
	return v
}

func (wb *workbench) handleState(w http.ResponseWriter, r *http.Request) {
	if !trustedGet(w, r) {
		return
	}
	items := wb.store.Load().Items
	views := make([]wbItemView, 0, len(items))
	for _, it := range items {
		views = append(views, wb.view(it))
	}
	home, _ := os.UserHomeDir()
	writeJSON(w, map[string]any{
		"cwd":     wb.cwd,
		"home":    home,
		"sep":     string(filepath.Separator),
		"version": version,
		"items":   views,
	})
}

type wbEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Git  bool   `json:"git,omitempty"`
}

// hasGitMarker reports whether dir has a .git directory or file.
func hasGitMarker(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

func (wb *workbench) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if !trustedGet(w, r) {
		return
	}
	dir, err := expandPath(r.URL.Query().Get("path"), wb.cwd)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	showHidden := r.URL.Query().Get("hidden") == "1"
	out := []wbEntry{}
	for _, e := range ents {
		if len(out) >= wbMaxBrowse {
			break
		}
		name := e.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil {
				isDir = st.IsDir()
			}
		}
		if !isDir {
			continue
		}
		out = append(out, wbEntry{Name: name, Path: full, Git: hasGitMarker(full)})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}
	writeJSON(w, map[string]any{"path": dir, "parent": parent, "git": hasGitMarker(dir) || gitAvailable(dir), "entries": out})
}

// handleComplete completes the folder name being typed: q's last path
// element is a case-insensitive prefix of folders in its parent. A q ending
// in a separator lists the folder itself. Each value keeps the q's own
// spelling of the parent (~, relative), so the input does not jump.
func (wb *workbench) handleComplete(w http.ResponseWriter, r *http.Request) {
	if !trustedGet(w, r) {
		return
	}
	q := r.URL.Query().Get("q")
	out := []wbEntry{}
	if strings.TrimSpace(q) == "" || strings.Contains(q, "://") {
		writeJSON(w, map[string]any{"entries": out})
		return
	}
	typedDir, prefix := q, ""
	if !strings.HasSuffix(q, "/") && !strings.HasSuffix(q, string(filepath.Separator)) {
		if i := strings.LastIndexAny(q, `/`+string(filepath.Separator)); i >= 0 {
			typedDir, prefix = q[:i+1], q[i+1:]
		} else if q == "~" {
			typedDir, prefix = "~/", ""
		} else {
			typedDir, prefix = "", q
		}
	}
	dir, err := expandPath(typedDir, wb.cwd)
	if err != nil {
		writeJSON(w, map[string]any{"entries": out})
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, map[string]any{"entries": out})
		return
	}
	lp := strings.ToLower(prefix)
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(strings.ToLower(name), lp) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".")) {
			continue
		}
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil {
				isDir = st.IsDir()
			}
		}
		if !isDir {
			continue
		}
		out = append(out, wbEntry{Name: name, Path: typedDir + name + "/", Git: hasGitMarker(full)})
	}
	sort.Slice(out, func(i, j int) bool {
		// Exact-case prefix matches first, then alphabetical.
		ai, aj := strings.HasPrefix(out[i].Name, prefix), strings.HasPrefix(out[j].Name, prefix)
		if ai != aj {
			return ai
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > 50 {
		out = out[:50]
	}
	writeJSON(w, map[string]any{"dir": dir, "entries": out})
}

// handleMetrics sums the workbench and every running workspace, the way the
// app's status bar shows one process, and lists each part for the menu.
func (wb *workbench) handleMetrics(w http.ResponseWriter, r *http.Request) {
	type part struct {
		Name string `json:"name"`
		metrics.ProcessMetrics
	}
	self := getProcessMetrics(nil)
	parts := []part{{Name: "workbench", ProcessMetrics: self}}

	type running struct{ name, url string }
	var targets []running
	wb.mu.Lock()
	for id, c := range wb.children {
		if c.state == "running" {
			targets = append(targets, running{c.title, "http://" + c.addr + wb.childBase(id) + "api/metrics"})
		}
	}
	wb.mu.Unlock()

	client := &http.Client{Timeout: time.Second}
	results := make([]*part, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(t.url)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			var m metrics.ProcessMetrics
			if json.NewDecoder(resp.Body).Decode(&m) == nil {
				results[i] = &part{Name: t.name, ProcessMetrics: m}
			}
		}()
	}
	wg.Wait()

	total := self
	for _, p := range results {
		if p == nil {
			continue
		}
		parts = append(parts, *p)
		total.RSSBytes += p.RSSBytes
		total.CPUUsage += p.CPUUsage
		total.Goroutine += p.Goroutine
		total.LSPMemBytes += p.LSPMemBytes
		total.LSPEnabled = total.LSPEnabled || p.LSPEnabled
	}
	writeJSON(w, map[string]any{
		"rssBytes":    total.RSSBytes,
		"cpuUsage":    total.CPUUsage,
		"goroutines":  total.Goroutine,
		"lspEnabled":  total.LSPEnabled,
		"lspMemBytes": total.LSPMemBytes,
		"parts":       parts,
	})
}

// handleSettings saves the theme picked on the workbench page. Only the
// theme: the app's full settings editor lives in each workspace.
func (wb *workbench) handleSettings(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var body map[string]any
	if readJSONBody(r, &body) != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	theme, ok := body["workbench.colorTheme"].(string)
	if !ok || len(body) != 1 {
		fail(w, http.StatusBadRequest, "only workbench.colorTheme can be set here")
		return
	}
	if err := settings.UpdateMap(map[string]any{"workbench.colorTheme": theme}); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// expandPath resolves ~ and relative paths (against base) to a clean absolute path.
func expandPath(p, base string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = base
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p), nil
}

func readJSONBody(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(v)
}

// handleOpen records a directory or PR URL and starts its child. The page
// then navigates to href, which shows progress until the child is ready.
func (wb *workbench) handleOpen(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var body struct {
		Target string `json:"target"`
		ID     string `json:"id"`
		// For a PR: the folder of a local clone of its repository, or
		// "managed" to let px0 clone it. Asked for when none is found.
		Clone string `json:"clone"`
	}
	if err := readJSONBody(r, &body); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	var it wbItem
	if body.ID != "" {
		known, ok := wb.store.Get(body.ID)
		if !ok {
			fail(w, http.StatusNotFound, "unknown workspace")
			return
		}
		it = known
	} else {
		var err error
		if it, err = wb.resolveTarget(body.Target); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if it.Kind == "pr" {
		switch body.Clone {
		case "":
			if wb.findClone(r.Context(), it.Repo) == "" {
				// Let the page ask where the clone is, before anything is recorded.
				writeJSON(w, map[string]any{"needClone": true, "repo": it.Repo, "managed": tildePath(managedCloneDir(it.Repo))})
				return
			}
		case "managed":
			wb.rememberClone(it.Repo, managedCloneDir(it.Repo))
		default:
			dir, err := expandPath(body.Clone, wb.cwd)
			if err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
			top := cloneOf(dir, it.Repo)
			if top == "" {
				fail(w, http.StatusBadRequest, fmt.Sprintf("%s is not a clone of %s: none of its remotes points there", tildePath(dir), it.Repo))
				return
			}
			wb.rememberClone(it.Repo, top)
		}
	}
	_, known := wb.store.Get(it.ID)
	if err := wb.store.Record(it); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	wb.start(it, !known)
	writeJSON(w, map[string]any{"id": it.ID, "href": wb.childBase(it.ID)})
}

func (wb *workbench) resolveTarget(target string) (wbItem, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return wbItem{}, errors.New("enter a directory path or a pull request URL")
	}
	if _, pt, ok := DetectPRURL(target); ok {
		if gitDisabled {
			return wbItem{}, errors.New("git is required for PR review; restart px0 without -no-git")
		}
		return wbPRItem(pt), nil
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return wbItem{}, errors.New("not a pull or merge request URL (e.g. " + prURLExamples + ")")
	}
	p, err := expandPath(target, wb.cwd)
	if err != nil {
		return wbItem{}, err
	}
	root, _, _, err := resolveTarget(p)
	if err != nil {
		return wbItem{}, err
	}
	return wbDirItem(root), nil
}

func (wb *workbench) handleStop(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if readJSONBody(r, &body) != nil || body.ID == "" {
		fail(w, http.StatusBadRequest, "id is required")
		return
	}
	wb.stop(body.ID)
	writeJSON(w, map[string]any{"ok": true})
}

func (wb *workbench) handlePin(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var body struct {
		ID     string `json:"id"`
		Pinned bool   `json:"pinned"`
	}
	if readJSONBody(r, &body) != nil || body.ID == "" {
		fail(w, http.StatusBadRequest, "id is required")
		return
	}
	err := wb.store.Update(func(f *wbFile) {
		for i := range f.Items {
			if f.Items[i].ID == body.ID {
				f.Items[i].Pinned = body.Pinned
			}
		}
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (wb *workbench) handleRemove(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if readJSONBody(r, &body) != nil || body.ID == "" {
		fail(w, http.StatusBadRequest, "id is required")
		return
	}
	wb.stop(body.ID)
	wb.mu.Lock()
	delete(wb.children, body.ID)
	wb.mu.Unlock()
	err := wb.store.Update(func(f *wbFile) { f.Items = withoutItem(f.Items, body.ID) })
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleWorkspace serves /w/<id>/...: proxied to the running child, or, for
// the page itself, a holding page that starts the child and refreshes until
// it is ready. A bookmarked or reloaded review therefore just reopens.
func (wb *workbench) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, wb.route("/w/"))
	id, _, hasSlash := strings.Cut(rest, "/")
	if id == "" {
		http.Redirect(w, r, wb.basePath, http.StatusFound)
		return
	}
	if !hasSlash {
		http.Redirect(w, r, wb.childBase(id), http.StatusFound)
		return
	}

	wb.mu.Lock()
	c := wb.children[id]
	var proxy *httputil.ReverseProxy
	if c != nil && c.state == "running" {
		proxy = c.proxy
		c.active++
	}
	wb.mu.Unlock()
	if proxy != nil {
		defer func() {
			wb.mu.Lock()
			c.active--
			c.lastActive = time.Now()
			wb.mu.Unlock()
		}()
		proxy.ServeHTTP(w, r)
		return
	}

	isPage := r.URL.Path == wb.childBase(id) && r.Method == http.MethodGet
	if !isPage {
		fail(w, http.StatusServiceUnavailable, "workspace is not running; reload the page to start it")
		return
	}
	if !trustedGet(w, r) {
		return
	}
	var snap wbChild
	if c != nil {
		wb.mu.Lock()
		snap = wbChild{state: c.state, err: c.err, title: c.title, gone: c.gone, noClone: c.noClone}
		wb.mu.Unlock()
	}
	if snap.state == "failed" && r.URL.Query().Get("retry") == "" {
		p := holdingPage{Title: snap.title, Error: snap.err}
		if it, ok := wb.store.Get(id); ok && snap.noClone {
			p.Choose = wb.basePath + "?open=" + url.QueryEscape(it.URL)
		} else if !snap.gone {
			p.Retry = wb.childBase(id) + "?retry=1"
		}
		wb.renderHolding(w, p)
		return
	}
	it, ok := wb.store.Get(id)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		wb.renderHolding(w, holdingPage{Title: "Not found", Error: "This workspace is not in the workbench any more."})
		return
	}
	// The refresh drops ?retry, so a second failure stops on the error page.
	if snap.state != "starting" {
		_ = wb.store.Record(it)
		c = wb.start(it, false)
	}
	wb.mu.Lock()
	progress := c.progress
	wb.mu.Unlock()
	// The refresh keeps the file and line a hand-off asked for.
	self := wb.childBase(id)
	if q := viewQuery(r.URL.Query()); q != "" {
		self += "?" + q
	}
	wb.renderHolding(w, holdingPage{Title: itemTitle(it), Self: self, Progress: progress, Starting: true})
}

// viewQuery keeps only the parameters that position a workspace's viewer on a
// file, so a holding page can refresh without dropping them.
func viewQuery(q url.Values) string {
	keep := url.Values{}
	for _, k := range []string{"path", "line"} {
		if v := q.Get(k); v != "" {
			keep.Set(k, v)
		}
	}
	return keep.Encode()
}

// startBackgroundWorkbench starts a workbench with no window or terminal of
// its own, and waits for it to answer. It returns the workbench's URL, or ""
// if none came up.
func startBackgroundWorkbench(exe string, args []string) string {
	cmd := exec.Command(exe, append([]string{"-no-open"}, args...)...)
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		return ""
	}
	_ = cmd.Process.Release()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if u := runningWorkbench(); u != "" {
			return u
		}
	}
	return ""
}

// workbenchPassthrough returns the flags a workbench started for -r takes from
// the command that asked for it. set holds the flags given on that command line.
func workbenchPassthrough(noLSP, noGit, noAgent, noTelemetry, noUpdate bool, agent string, set map[string]string) []string {
	var args []string
	for _, f := range []struct {
		on   bool
		flag string
	}{{noLSP, "-no-lsp"}, {noGit, "-no-git"}, {noAgent, "-no-agent"}, {noTelemetry, "-no-telemetry"}, {noUpdate, "-no-update"}} {
		if f.on {
			args = append(args, f.flag)
		}
	}
	if agent != "" {
		args = append(args, "-agent", agent)
	}
	for _, name := range []string{"host", "port", "base-path"} {
		if v, ok := set[name]; ok {
			args = append(args, "-"+name, v)
		}
	}
	return args
}

// reuseWorkbench opens root in the workbench that running reports, starting
// one with start first if none is running, and returns the page's address.
func reuseWorkbench(running, start func() string, root, file string, line int) (string, error) {
	u := running()
	if u == "" {
		u = start()
	}
	if u == "" {
		return "", errors.New("no workbench is running and none could be started")
	}
	return handOffToWorkbench(u, root, file, line)
}

// handOffToWorkbench opens root in the workbench serving at wbURL, instead of
// starting another server, and returns the address of its page, positioned on
// file and line.
func handOffToWorkbench(wbURL, root, file string, line int) (string, error) {
	base, err := url.Parse(wbURL)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{"target": root})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(wbURL, "/")+"/api/wb/open", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// The workbench takes writes only from its own pages (see localPost).
	req.Header.Set("Origin", base.Scheme+"://"+base.Host)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var res struct {
		Href  string `json:"href"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&res); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK || res.Href == "" {
		if res.Error == "" {
			res.Error = resp.Status
		}
		return "", errors.New(res.Error)
	}
	return viewerURL(base.Host, file, line, res.Href), nil
}

func withoutItem(items []wbItem, id string) []wbItem {
	kept := items[:0]
	for _, it := range items {
		if it.ID != id {
			kept = append(kept, it)
		}
	}
	return kept
}

func itemTitle(it wbItem) string {
	if it.Kind == "pr" {
		t := fmt.Sprintf("%s#%d", it.Repo, it.Number)
		if it.Title != "" {
			t += " " + it.Title
		}
		return t
	}
	return it.Path
}

type holdingPage struct {
	Base, Theme, Title, Error, Retry, Self, Progress string
	Choose                                           string // workbench URL that asks where the PR's clone is
	Starting                                         bool
}

var holdingTmpl = template.Must(template.New("holding").Parse(`<!doctype html>
<html lang="en" data-theme="{{.Theme}}">
<head>
<meta charset="utf-8">
<base href="{{.Base}}">
<meta name="viewport" content="width=device-width,initial-scale=1">
{{if .Starting}}<meta http-equiv="refresh" content="1; url={{.Self}}">{{end}}
<title>{{if .Starting}}Opening…{{else}}px0{{end}}</title>
<link rel="icon" type="image/x-icon" href="https://px0.ai/favicon.ico">
<link rel="stylesheet" href="static/style.css">
<link rel="stylesheet" href="static/themes.css">
<link rel="stylesheet" href="static/workbench.css">
</head>
<body class="wb">
<main class="wb-holding">
  <div class="empty-logo">
    <img class="logo-dark" src="https://px0.ai/logo/px0-logo-dark.png" alt="px0" width="160" height="60">
    <img class="logo-light" src="https://px0.ai/logo/px0-logo-light.png" alt="px0" width="160" height="60">
  </div>
  {{if .Starting}}
  <p class="wb-holding-msg"><span class="wb-spin" aria-hidden="true"></span>Opening <b>{{.Title}}</b>…</p>
  <p class="wb-faint">{{if .Progress}}{{.Progress}}…{{else}}Pull requests are checked out first; large repositories take a while.{{end}}</p>
  {{else}}
  <p class="wb-holding-msg">Could not open <b>{{.Title}}</b></p>
  <pre class="wb-error">{{.Error}}</pre>
  {{end}}
  <p class="wb-holding-actions">{{if .Retry}}<a class="wb-btn wb-primary" href="{{.Retry}}">Retry</a>{{end}}{{if .Choose}}<a class="wb-btn wb-primary" href="{{.Choose}}">Choose a clone</a>{{end}}<a class="wb-btn" href="{{.Base}}">Back to workbench</a></p>
</main>
</body>
</html>
`))

func (wb *workbench) renderHolding(w http.ResponseWriter, p holdingPage) {
	p.Base, p.Theme = wb.basePath, wbTheme()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	holdingTmpl.Execute(w, p)
}

/* ---------- single instance ---------- */

type wbLock struct {
	PID int    `json:"pid"`
	URL string `json:"url"`
}

// runningWorkbench returns the URL of a workbench that is already serving,
// so a second bare `px0` opens it instead of starting another.
func runningWorkbench() string {
	b, err := os.ReadFile(workbenchLockPath())
	if err != nil {
		return ""
	}
	var l wbLock
	if json.Unmarshal(b, &l) != nil || l.URL == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), wbPingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(l.URL, "/")+"/api/wb/ping", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var ping struct {
		Workbench bool `json:"workbench"`
	}
	if json.NewDecoder(resp.Body).Decode(&ping) != nil || !ping.Workbench {
		return ""
	}
	return l.URL
}

func writeWorkbenchLock(u string) {
	_ = writeFileAtomic(workbenchLockPath(), wbLock{PID: os.Getpid(), URL: u})
}

// removeWorkbenchLock deletes the lock only if it is still this process's.
func removeWorkbenchLock() {
	b, err := os.ReadFile(workbenchLockPath())
	if err != nil {
		return
	}
	var l wbLock
	if json.Unmarshal(b, &l) == nil && l.PID == os.Getpid() {
		os.Remove(workbenchLockPath())
	}
}

/* ---------- PR local clones ---------- */

// Pull requests opened from the workbench are always checked out as a git
// worktree of a local clone of their repository, never as a fresh clone per
// PR: a worktree shares the clone's objects, so it costs a checkout and
// nothing else. The clone for each repository is found once and remembered
// in workbench.json:
//
//  1. the clone remembered for owner/repo, if it is still one;
//  2. a recorded directory (or the workbench's own directory) inside one;
//  3. a bounded scan of the usual places people keep code (~/workspace,
//     ~/code, ~/src, ...) and the folders around recorded directories.
//
// When none is found the page asks: point at a clone, or let px0 make one,
// once, under the state directory (repos/<owner>/<repo>), to be reused by
// every later PR of that repository.

const (
	wbScanDepth   = 3                       // folder levels below each scan root
	wbScanBudget  = 20000                   // folders looked at per scan, at most
	wbScanTimeout = 1500 * time.Millisecond // wall-clock cap on one scan
)

// wbScanSkip are folder names never worth descending into for a clone.
var wbScanSkip = map[string]bool{
	"node_modules": true, "vendor": true, "target": true, "build": true, "dist": true,
	"Library": true, "Applications": true, "AppData": true, "venv": true, "__pycache__": true,
}

// wbScanRoots are where people usually keep clones, relative to home.
var wbScanRoots = []string{
	"workspace", "workspaces", "code", "src", "projects", "dev", "repos", "git",
	"github", "work", "Developer", "Documents/GitHub", "go/src/github.com", "",
}

func repoKey(repo string) string { return strings.ToLower(repo) }

func splitRepo(repo string) (owner, name string) {
	owner, name, _ = strings.Cut(repo, "/")
	return owner, name
}

// managedCloneDir is where px0 makes a clone when the user asks it to.
func managedCloneDir(repo string) string {
	owner, name := splitRepo(repo)
	return filepath.Join(workbenchStateDir(), "repos", owner, name)
}

// cloneOf returns the top level of dir's repository when one of its remotes
// is repo ("owner/name"), or "".
func cloneOf(dir, repo string) string {
	owner, name := splitRepo(repo)
	top, _ := findPRRemote(dir, owner, name)
	return top
}

// knownClone looks only where it is cheap to: the remembered clone, the
// recorded directories, and extra (the current directory).
func knownClone(f wbFile, repo string, extra ...string) string {
	if p := f.Clones[repoKey(repo)]; p != "" {
		if top := cloneOf(p, repo); top != "" {
			return top
		}
	}
	seen := map[string]bool{}
	var dirs []string
	for _, it := range f.Items {
		if it.Kind == "dir" {
			dirs = append(dirs, it.Path)
		}
	}
	for _, d := range append(dirs, extra...) {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		if top := cloneOf(d, repo); top != "" {
			return top
		}
	}
	return ""
}

// scanForClone walks roots breadth-first, a few levels deep, for a folder
// named like the repository that is a clone of it. Bounded in folders and time.
func scanForClone(ctx context.Context, roots []string, repo string) string {
	_, name := splitRepo(repo)
	ctx, cancel := context.WithTimeout(ctx, wbScanTimeout)
	defer cancel()
	type node struct {
		path  string
		depth int
	}
	seen := map[string]bool{}
	var queue []node
	for _, r := range roots {
		if r != "" && !seen[r] {
			seen[r] = true
			queue = append(queue, node{r, 0})
		}
	}
	budget := wbScanBudget
	for len(queue) > 0 && budget > 0 && ctx.Err() == nil {
		n := queue[0]
		queue = queue[1:]
		ents, err := os.ReadDir(n.path)
		if err != nil {
			continue
		}
		for _, e := range ents {
			budget--
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || wbScanSkip[e.Name()] {
				continue
			}
			p := filepath.Join(n.path, e.Name())
			if seen[p] {
				continue
			}
			seen[p] = true
			if strings.EqualFold(e.Name(), name) {
				if hasGitMarker(p) {
					if top := cloneOf(p, repo); top != "" {
						return top
					}
				}
			}
			if n.depth+1 < wbScanDepth {
				queue = append(queue, node{p, n.depth + 1})
			}
		}
	}
	return ""
}

// scanRoots lists the usual code folders under home plus the parents of the
// recorded directories, which is where other clones tend to sit.
func scanRoots(f wbFile, cwd string) []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, r := range wbScanRoots {
			roots = append(roots, filepath.Join(home, r))
		}
	}
	for _, it := range f.Items {
		if it.Kind == "dir" {
			roots = append(roots, filepath.Dir(it.Path))
		}
	}
	if cwd != "" {
		roots = append(roots, cwd, filepath.Dir(cwd))
	}
	return roots
}

// findClone finds and remembers the local clone of repo, or returns "".
func (wb *workbench) findClone(ctx context.Context, repo string) string {
	f := wb.store.Load()
	top := knownClone(f, repo, wb.cwd)
	if top == "" {
		top = scanForClone(ctx, scanRoots(f, wb.cwd), repo)
	}
	if top != "" {
		wb.rememberClone(repo, top)
	}
	return top
}

func (wb *workbench) rememberClone(repo, dir string) {
	_ = wb.store.Update(func(f *wbFile) {
		if f.Clones == nil {
			f.Clones = map[string]string{}
		}
		f.Clones[repoKey(repo)] = dir
	})
}

var errNoClone = errors.New("no local clone")

// prClone returns the clone a PR's child should start in. The remembered
// clone wins; a remembered px0-made clone that is missing is made again.
func (wb *workbench) prClone(c *wbChild, it wbItem) (string, error) {
	f := wb.store.Load()
	remembered := f.Clones[repoKey(it.Repo)]
	if remembered != "" && remembered == managedCloneDir(it.Repo) {
		if top := cloneOf(remembered, it.Repo); top != "" {
			return top, nil
		}
		return wb.makeClone(c, it)
	}
	wb.setProgress(c, "Looking for a local clone of "+it.Repo)
	if top := wb.findClone(c.ctx, it.Repo); top != "" {
		return top, nil
	}
	return "", fmt.Errorf("%w of %s found; open the pull request again from the workbench to choose one", errNoClone, it.Repo)
}

// cloneLocks serializes makeClone per repository, so two PRs of one repository
// opened together do not both clone it.
var cloneLocks sync.Map

// makeClone clones the PR's repository into managedCloneDir, once. The clone
// is partial (blobs on demand) and has no checkout of its own: worktrees are
// all it is for.
func (wb *workbench) makeClone(c *wbChild, it wbItem) (string, error) {
	mu, _ := cloneLocks.LoadOrStore(repoKey(it.Repo), &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	dest := managedCloneDir(it.Repo)
	if top := cloneOf(dest, it.Repo); top != "" {
		return top, nil // made while this one waited
	}
	provider, pt, ok := DetectPRURL(it.URL)
	if !ok {
		return "", fmt.Errorf("not a pull request URL: %s", it.URL)
	}
	token, _ := provider.ResolveToken(settings.Read(), pt)
	wb.setProgress(c, fmt.Sprintf("Cloning %s into %s (once; later pull requests reuse it)", it.Repo, tildePath(dest)))

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	// Clone beside dest and rename, so an interrupted clone never looks done.
	tmp, err := os.MkdirTemp(filepath.Dir(dest), "."+filepath.Base(dest)+"-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	auth := provider.GitAuth(pt, token)
	cmd := gitAuthCmd(auth, "clone", "--filter=blob:none", "--no-checkout", provider.CloneURL(pt), tmp)
	if out, err := runCtx(c.ctx, cmd); err != nil {
		return "", fmt.Errorf("git clone %s: %w: %s", it.Repo, err, auth.redact(strings.TrimSpace(string(out))))
	}
	os.RemoveAll(dest) // a broken earlier attempt
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	wb.rememberClone(it.Repo, dest)
	return dest, nil
}

// runCtx runs cmd and kills it if ctx ends first.
func runCtx(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return []byte(out.String()), err
	case <-ctx.Done():
		cmd.Process.Kill()
		<-done
		return []byte(out.String()), ctx.Err()
	}
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}
