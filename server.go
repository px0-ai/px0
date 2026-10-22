package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"px0/internal/fuzzy"
	"px0/internal/metrics"
	"px0/internal/render"
	"px0/internal/settings"
	"px0/internal/symbols"
	"px0/internal/table"
	"px0/internal/telemetry"
	"px0/internal/ui"
)

//go:embed web/index.html web/style.css web/themes web/vendor
//go:embed web/workbench.html web/workbench.css web/workbench.js
//go:embed web/app.js
var embedded embed.FS

// assets is the embedded web/ directory.
var assets fs.FS = embedded

func cleanBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" || p == "." {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = path.Clean(p)
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// Server is the main px0 HTTP server handling the web UI, static assets,
// REST API endpoints, Server-Sent Events (SSE), and workspace services.
type Server struct {
	ix        *Index
	lsp       *lspManager
	tel       *telemetry.Service // nil in tests; Track is nil-safe
	agent     *agentManager      // nil unless main wires editing for this session
	threads   *threadManager     // nil unless editing is wired: threads run on the same harness
	pr        *prSession         // nil unless main launched this process as `px0 pr ...`
	diffBase  string             // ref /api/diff and /api/gutter diff against; "HEAD" unless in PR mode
	prHeadSHA string             // PR mode only: the checked-out PR head commit. Frozen boundary between
	// the PR's own diff (diffBase..prHeadSHA) and the reviewer's local edits
	// since checkout (prHeadSHA..working tree); refreshed on Pull.
	gitWatcher *GitWatcher
	mux        *http.ServeMux
	basePath   string
	session    *sessionManager

	workbenchURL string // the workbench's base path, when this px0 is one of its workspaces

	lastReq atomic.Int64 // unix nanos of the most recent request

	// streamsDone closes when the HTTP server starts shutting down, so event
	// streams end their responses cleanly instead of being cut when we exit.
	streamsDone chan struct{}
	endStreams  sync.Once
}

// EndStreams ends every open event stream. Register it with
// http.Server.RegisterOnShutdown: Shutdown does not cancel request contexts,
// so streams would otherwise hold it up until its deadline.
func (s *Server) EndStreams() {
	s.endStreams.Do(func() {
		if s.streamsDone != nil {
			close(s.streamsDone)
		}
	})
}

// BasePath returns the URL path prefix configured for this server (e.g. "/" or "/rev-123/").
func (s *Server) BasePath() string {
	if s.basePath == "" {
		return "/"
	}
	return s.basePath
}

func (s *Server) routePath(subpath string) string {
	if s.basePath == "" || s.basePath == "/" {
		return subpath
	}
	bp := strings.TrimSuffix(s.basePath, "/")
	if !strings.HasPrefix(subpath, "/") {
		return bp + "/" + subpath
	}
	return bp + subpath
}

func (s *Server) registerRoutes() {
	sub, _ := fs.Sub(assets, "web")
	staticPrefix := s.routePath("/static/")
	s.mux.Handle(staticPrefix, s.handleStatic(sub, staticPrefix))
	s.mux.HandleFunc(s.routePath("/static/themes.css"), s.handleThemes)

	if s.basePath != "/" && s.basePath != "" {
		s.mux.HandleFunc(s.basePath, s.handleIndex)
		trimmed := strings.TrimSuffix(s.basePath, "/")
		s.mux.HandleFunc(trimmed, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, s.basePath, http.StatusMovedPermanently)
		})
		s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, s.basePath, http.StatusFound)
				return
			}
			http.NotFound(w, r)
		})
	} else {
		s.mux.HandleFunc("/", s.handleIndex)
	}

	s.mux.HandleFunc(s.routePath("/api/meta"), s.handleMeta)
	s.mux.HandleFunc(s.routePath("/api/metrics"), s.handleMetrics)
	s.mux.HandleFunc(s.routePath("/api/tree"), s.handleTree)
	s.mux.HandleFunc(s.routePath("/api/find"), s.handleFind)
	s.mux.HandleFunc(s.routePath("/api/file"), s.handleFile)
	s.mux.HandleFunc(s.routePath("/api/folds"), s.handleFolds)
	s.mux.HandleFunc(s.routePath("/api/close"), s.handleClose)
	s.mux.HandleFunc(s.routePath("/api/raw"), s.handleRaw)
	s.mux.HandleFunc(s.routePath("/api/file/save"), s.handleFileSave)
	s.mux.HandleFunc(s.routePath("/api/fs/new"), s.handleFSNew)
	s.mux.HandleFunc(s.routePath("/api/markdown"), s.handleMarkdown)
	s.mux.HandleFunc(s.routePath("/api/table"), s.handleTable)
	s.mux.HandleFunc(s.routePath("/api/diff"), s.handleDiff)
	s.mux.HandleFunc(s.routePath("/api/gutter"), s.handleGutter)
	s.mux.HandleFunc(s.routePath("/api/stream"), s.handleEventStream)
	s.mux.HandleFunc(s.routePath("/api/git/stream"), s.handleEventStream)
	s.mux.HandleFunc(s.routePath("/api/git/refresh"), s.handleGitRefresh)
	s.mux.HandleFunc(s.routePath("/api/git/stage"), s.handleGitStage)
	s.mux.HandleFunc(s.routePath("/api/git/unstage"), s.handleGitUnstage)
	s.mux.HandleFunc(s.routePath("/api/git/hunk"), s.handleGitHunk)
	s.mux.HandleFunc(s.routePath("/api/git/commit"), s.handleGitCommit)
	s.mux.HandleFunc(s.routePath("/api/git/commit-message"), s.handleGitCommitMessage)
	s.mux.HandleFunc(s.routePath("/api/git/push"), s.handleGitPush)
	s.mux.HandleFunc(s.routePath("/api/git/pull"), s.handleGitPull)
	s.mux.HandleFunc(s.routePath("/api/git/sync-agent"), s.handleGitSyncAgent)
	s.mux.HandleFunc(s.routePath("/api/git/sync-state"), s.handleGitSyncState)
	s.mux.HandleFunc(s.routePath("/api/git/abort"), s.handleGitAbort)
	s.mux.HandleFunc(s.routePath("/api/git/log"), s.handleGitLog)
	s.mux.HandleFunc(s.routePath("/api/unpushed"), s.handleUnpushed)
	s.mux.HandleFunc(s.routePath("/api/commitfiles"), s.handleCommitFiles)
	s.mux.HandleFunc(s.routePath("/api/commitdetail"), s.handleCommitDetail)
	s.mux.HandleFunc(s.routePath("/api/search"), s.handleSearch)
	s.mux.HandleFunc(s.routePath("/api/outline"), s.handleOutline)
	s.mux.HandleFunc(s.routePath("/api/def"), s.handleDef)
	s.mux.HandleFunc(s.routePath("/api/reindex"), s.handleReindex)
	s.mux.HandleFunc(s.routePath("/api/lsp/def"), s.handleLSPDef)
	s.mux.HandleFunc(s.routePath("/api/lsp/refs"), s.handleLSPRefs)
	s.mux.HandleFunc(s.routePath("/api/lsp/calls"), s.handleLSPCalls)
	s.mux.HandleFunc(s.routePath("/api/lsp/symbols"), s.handleLSPSymbols)
	s.mux.HandleFunc(s.routePath("/api/lsp/hover"), s.handleLSPHover)
	s.mux.HandleFunc(s.routePath("/api/lsp/diagnostics"), s.handleLSPDiagnostics)
	s.mux.HandleFunc(s.routePath("/api/lsp/warm"), s.handleLSPWarm)
	s.mux.HandleFunc(s.routePath("/api/lsp/setup"), s.handleLSPSetup)
	s.mux.HandleFunc(s.routePath("/api/lsp/install"), s.handleLSPInstall)
	s.mux.HandleFunc(s.routePath("/api/lsp/start"), s.handleLSPStart)
	s.mux.HandleFunc(s.routePath("/api/lsp/stop"), s.handleLSPStop)
	s.mux.HandleFunc(s.routePath("/api/lsp/servers"), s.handleLSPServers)
	s.mux.HandleFunc(s.routePath("/api/agent/harnesses"), s.handleAgentHarnesses)
	s.mux.HandleFunc(s.routePath("/api/agent/select"), s.handleAgentSelect)
	s.mux.HandleFunc(s.routePath("/api/agent/edit"), s.handleAgentEdit)
	s.mux.HandleFunc(s.routePath("/api/agent/batch"), s.handleAgentBatchEdit)
	s.mux.HandleFunc(s.routePath("/api/agent/job"), s.handleAgentJob)
	s.mux.HandleFunc(s.routePath("/api/agent/cancel"), s.handleAgentCancel)
	s.mux.HandleFunc(s.routePath("/api/threads"), s.handleThreads)
	s.mux.HandleFunc(s.routePath("/api/threads/get"), s.handleThreadGet)
	s.mux.HandleFunc(s.routePath("/api/threads/create"), s.handleThreadCreate)
	s.mux.HandleFunc(s.routePath("/api/threads/send"), s.handleThreadSend)
	s.mux.HandleFunc(s.routePath("/api/threads/cancel"), s.handleThreadCancel)
	s.mux.HandleFunc(s.routePath("/api/threads/scope"), s.handleThreadScope)
	s.mux.HandleFunc(s.routePath("/api/threads/delete"), s.handleThreadDelete)
	s.mux.HandleFunc(s.routePath("/api/threads/revert"), s.handleThreadRevert)
	s.mux.HandleFunc(s.routePath("/api/threads/stream"), s.handleThreadStream)
	s.mux.HandleFunc(s.routePath("/api/settings"), s.handleSettings)
	s.mux.HandleFunc(s.routePath("/api/pr/meta"), s.handlePRMeta)
	s.mux.HandleFunc(s.routePath("/api/pr/comments"), s.handlePRComments)
	s.mux.HandleFunc(s.routePath("/api/pr/comments/delete"), s.handlePRCommentDelete)
	s.mux.HandleFunc(s.routePath("/api/pr/submit"), s.handlePRSubmit)
	s.mux.HandleFunc(s.routePath("/api/pr/launch"), s.handleLaunchPR)
	s.mux.HandleFunc(s.routePath("/api/pr/existing-comments"), s.handlePRExistingComments)
	s.mux.HandleFunc(s.routePath("/api/pr/comments/issue"), s.handlePRIssueCommentPost)
	s.mux.HandleFunc(s.routePath("/api/pr/comments/review-reply"), s.handlePRReviewCommentReply)
	s.mux.HandleFunc(s.routePath("/api/pr/merge-status"), s.handlePRMergeStatus)
	s.mux.HandleFunc(s.routePath("/api/pr/merge"), s.handlePRMerge)
	s.mux.HandleFunc(s.routePath("/api/pr/merge-message"), s.handlePRMergeMessage)
	s.mux.HandleFunc(s.routePath("/api/forge/connect"), s.handleForgeConnect)
	s.mux.HandleFunc(s.routePath("/api/session"), s.handleSession)
}

// NewServer creates and initializes a px0 Server instance, binding index and language servers,
// starting the background GitWatcher, and registering all HTTP and SSE routes.
func NewServer(ix *Index, lsp *lspManager, basePaths ...string) *Server {
	if lsp == nil {
		lsp = newLSPManager(ix.Root(), false)
	}
	bp := "/"
	if len(basePaths) > 0 && basePaths[0] != "" {
		bp = cleanBasePath(basePaths[0])
	}
	s := &Server{ix: ix, lsp: lsp, diffBase: "HEAD", basePath: bp, mux: http.NewServeMux(), streamsDone: make(chan struct{})}
	s.session = newSessionManager(s.basePath, ix.Root())
	s.gitWatcher = NewGitWatcher(ix)
	s.gitWatcher.Start(context.Background())
	s.registerRoutes()
	s.lastReq.Store(time.Now().UnixNano())
	go s.scavenge()
	return s
}

// scavenge hands freed pages back to the OS once nobody is asking for anything.
// Reading a large tree churns through a lot of short-lived memory, and the Go
// runtime is in no hurry to return it. That is harmless but it makes a process
// that is doing nothing look like it is holding hundreds of megabytes.
func (s *Server) scavenge() {
	const idleFor = 15 * time.Second
	const lspIdleFor = 10 * time.Minute
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	done := true // nothing to release before the first request
	for range tick.C {
		idle := time.Since(time.Unix(0, s.lastReq.Load()))
		if s.lsp != nil && idle >= lspIdleFor {
			s.lsp.ReapIdle(lspIdleFor)
		}
		if idle < idleFor {
			done = false
			continue
		}
		if done {
			continue // already released since the last burst of work
		}
		render.Scavenge()
		debug.FreeOSMemory()
		done = true
	}
}

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https: http:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'none';"

// ServeHTTP delegates incoming HTTP requests to the configured ServeMux,
// recording request timing and updating access timestamps.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	streamPath := s.routePath("/api/stream")
	gitStreamPath := s.routePath("/api/git/stream")
	metricsPath := s.routePath("/api/metrics")
	isSSE := r.URL.Path == streamPath || r.URL.Path == gitStreamPath || r.Header.Get("Accept") == "text/event-stream"
	if r.URL.Path != metricsPath && !isSSE {
		s.lastReq.Store(time.Now().UnixNano())
	}
	start := time.Now()

	rec := &statusRecorder{ResponseWriter: w}
	if ui.Verbose {
		defer func() {
			dur := ui.FmtDuration(time.Since(start))
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			role := "info"
			if status >= 500 {
				role = "err"
			} else if status >= 400 {
				role = "warn"
			}
			uri := r.RequestURI
			if uri == "" {
				uri = r.URL.RequestURI()
			}
			if uri == "" {
				uri = r.URL.Path
			}
			if uri == "" {
				uri = "/"
			}
			ui.Status(role, "http", fmt.Sprintf("%s %s · %d  (%s)", r.Method, uri, status, dur), 0, os.Stdout)
		}()
	}

	var out http.ResponseWriter = rec
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && !isSSE {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(rec)
		var disabled bool
		gw := &gzipWriter{ResponseWriter: rec, w: gz, disabled: &disabled}
		defer func() { gz.Close(); gzipPool.Put(gz) }()
		out = gw
	}

	s.mux.ServeHTTP(out, r)
}

type gzipBypasser interface {
	BypassGzip()
}

func bypassGzip(w http.ResponseWriter) {
	curr := w
	for curr != nil {
		if b, ok := curr.(gzipBypasser); ok {
			b.BypassGzip()
			return
		}
		if u, ok := curr.(interface{ Unwrap() http.ResponseWriter }); ok {
			curr = u.Unwrap()
		} else {
			break
		}
	}
}

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}}

type gzipWriter struct {
	http.ResponseWriter
	w        *gzip.Writer
	disabled *bool
}

func (g gzipWriter) BypassGzip() {
	if g.disabled != nil {
		*g.disabled = true
	}
	if g.w != nil {
		g.w.Reset(io.Discard)
	}
}

func (g gzipWriter) isDisabled() bool {
	return g.disabled != nil && *g.disabled
}

func (g gzipWriter) WriteHeader(status int) {
	if g.isDisabled() {
		g.ResponseWriter.WriteHeader(status)
		return
	}
	g.Header().Del("Content-Length")
	if status == http.StatusNotModified || status == http.StatusNoContent {
		g.Header().Del("Content-Encoding")
		if g.w != nil {
			g.w.Reset(io.Discard)
		}
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g gzipWriter) Write(b []byte) (int, error) {
	if g.isDisabled() || g.w == nil {
		return g.ResponseWriter.Write(b)
	}
	g.Header().Del("Content-Length")
	return g.w.Write(b)
}

func (g gzipWriter) Flush() {
	if !g.isDisabled() && g.w != nil {
		_ = g.w.Flush()
	}
	if flusher, ok := g.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (g gzipWriter) Unwrap() http.ResponseWriter {
	return g.ResponseWriter
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := r.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, errors.New("hijack unsupported")
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// safePath resolves a client-supplied relative path inside the root, refusing
// anything that escapes it.
func (s *Server) safePath(rel string) (string, string, bool) {
	rel = strings.TrimPrefix(strings.TrimSpace(rel), "/")
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." {
		return s.ix.Root(), "", true
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", "", false
	}
	abs := filepath.Join(s.ix.Root(), clean)
	if abs != s.ix.Root() && !strings.HasPrefix(abs, s.ix.Root()+string(filepath.Separator)) {
		return "", "", false
	}
	return abs, filepath.ToSlash(clean), true
}

// resolvePath is safePath plus the one documented exception: an absolute path a
// language server named as a definition target, such as a file in the standard
// library or the module cache. Nothing else outside the root is reachable.
func (s *Server) resolvePath(p string) (string, string, bool) {
	if filepath.IsAbs(filepath.FromSlash(p)) {
		abs := filepath.Clean(filepath.FromSlash(p))
		if s.lsp.Allowed(abs) {
			return abs, filepath.ToSlash(abs), true
		}
		return "", "", false
	}
	return s.safePath(p)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	// Highlighted lines are already HTML-escaped by the time they get here, so
	// the extra \u003c encoding only inflates the payload and makes the API
	// awkward to read with anything but a JSON parser.
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// SetAgent makes editing through a coding harness available. Unavailable
// unless main wires it; available still means nothing runs until a harness is
// picked, in the UI or with -agent.
func (s *Server) SetAgent(a *agentManager) {
	s.agent = a
	if a != nil {
		s.threads = newThreadManager(a, s.ix.Root())
		s.threads.prContext = s.prThreadContext
		a.onEdit = func() {
			if s.gitWatcher != nil {
				s.gitWatcher.Trigger()
			}
		}
	}
}

// diffFileMax caps the diff file handed to a thread; past it the harness is
// pointed at the git command instead.
const diffFileMax = 2 << 20

// prThreadContext tells a coding harness what a PR-review thread is about. A PR
// checkout is a plain worktree whose recent history is the PR's commits followed
// by the reviewer's own, so a harness asked about "the PR" left to itself looks
// at the last commit. Each scope names an exact range instead, and gets the
// matching diff as a file so a big change doesn't have to be re-derived:
//
//	pr        the whole pull request: merge-base..PR head
//	mine      the reviewer's own work on top: PR head..working tree
//	selection just the anchored code; the PR is background
//
// An unset scope means the whole PR. Empty outside PR review.
func (s *Server) prThreadContext(scope string) string {
	p := s.pr
	if p == nil {
		return ""
	}
	p.mu.Lock()
	num, title, base, head := p.meta.Number, p.meta.Title, p.meta.BaseRef, p.meta.HeadRef
	mb, headSHA, url := p.diffBase, p.meta.HeadSHA, p.target.URL
	p.mu.Unlock()
	root := s.ix.Root()
	short := func(x string) string { return x[:min(12, len(x))] }
	haveRange := mb != "" && mb != "HEAD" && headSHA != ""
	if scope == "" {
		scope = "pr"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "This workspace is a checkout of pull request #%d %q (%s ← %s)", num, title, base, head)
	if url != "" {
		fmt.Fprintf(&b, ", %s", url)
	}
	b.WriteString(". ")

	prRange := ""
	if haveRange {
		prRange = fmt.Sprintf("`git diff %s %s` (merge-base with %s to the PR head %s)", mb, headSHA, base, short(headSHA))
	}
	switch scope {
	case "mine":
		fmt.Fprintf(&b, "The user is asking about their OWN changes on top of the PR, not the PR itself. ")
		if headSHA == "" {
			b.WriteString("Look at `git status` and `git log` for them.")
			break
		}
		fmt.Fprintf(&b, "Those are exactly `git diff %s` (working tree against the PR head, staged and unstaged) and `git log --stat %s..HEAD` (their commits)", headSHA, headSHA)
		if un := gitUntracked(root); len(un) > 0 {
			if len(un) > 40 {
				un = append(un[:40], fmt.Sprintf("(+%d more)", len(un)-40))
			}
			fmt.Fprintf(&b, ", plus untracked files: %s", strings.Join(un, ", "))
		}
		b.WriteString(". ")
		if f := p.writeScopeFile("your-changes.diff", gitDiffFull(root, diffFileMax, headSHA)); f != "" {
			fmt.Fprintf(&b, "That diff is also saved at %s. ", f)
		}
		if prRange != "" {
			fmt.Fprintf(&b, "The PR's own change (%s) is background only; do not review it unless asked.", prRange)
		}
	case "selection":
		b.WriteString("The user is asking about the code they anchored this conversation to; the rest of the PR is background")
		if prRange != "" {
			fmt.Fprintf(&b, " (%s shows it if you need it)", prRange)
		}
		b.WriteString(".")
	default: // "pr"
		b.WriteString("When the user says \"the PR\" or \"this PR\", they mean the entire pull request, not the latest commit or the file they are looking at.")
		if !haveRange {
			break
		}
		fmt.Fprintf(&b, " The PR is exactly %s: read it with that and `git log --stat %s..%s`; do not review from `git show HEAD` or `git diff HEAD~1`.", prRange, mb, headSHA)
		if f := p.writeScopeFile("pr.diff", gitDiffFull(root, diffFileMax, mb, headSHA)); f != "" {
			fmt.Fprintf(&b, " The full diff is also saved at %s.", f)
		}
		if files := s.ix.PRFiles(); len(files) > 0 {
			names := make([]string, 0, len(files))
			for f, st := range files {
				names = append(names, st+" "+f)
			}
			sort.Strings(names)
			more := ""
			if len(names) > 60 {
				more = fmt.Sprintf(" (+%d more)", len(names)-60)
				names = names[:60]
			}
			fmt.Fprintf(&b, " Files it changes (%d)%s: %s.", len(files), more, strings.Join(names, ", "))
		}
		fmt.Fprintf(&b, " Anything after %s (`git diff %s`, or commits above it) is the reviewer's own work on top of the PR, not part of it.", short(headSHA), headSHA)
	}
	return b.String()
}

// SetPR marks this process as a PR review session: diffs are computed
// against the PR's merge-base instead of HEAD, and the /api/pr/* endpoints
// become live. Unset (nil) for a normal workspace.
func (s *Server) SetPR(p *prSession) {
	s.pr = p
	if s.threads != nil {
		s.threads.prContext = s.prThreadContext
	}
	if p != nil {
		s.diffBase = p.diffBase
		s.prHeadSHA = p.meta.HeadSHA
		if s.ix != nil {
			s.ix.SetDiffBase(p.diffBase)
			s.ix.SetPRHead(p.meta.HeadSHA)
			s.ix.SetPushedHead(p.meta.HeadSHA)
		}
		if s.gitWatcher != nil {
			s.gitWatcher.Trigger()
		}
		if p.target.Owner != "" && p.target.Repo != "" && p.target.Number > 0 {
			// Drafts are keyed on the PR, not the base path. Carry over any
			// drafts an older px0 saved under the base-path file.
			prs := newPRSessionManager(p.target)
			if old := s.session; old != nil && len(prs.Get().Drafts) == 0 && len(old.Get().Drafts) > 0 {
				drafts := old.Get().Drafts
				prs.Update(func(ws *WorkspaceSession) { ws.Drafts = drafts })
				old.Update(func(ws *WorkspaceSession) { ws.Drafts = nil })
			}
			s.session = prs
		}
		if s.session != nil {
			p.mu.Lock()
			if len(p.comments) == 0 && len(s.session.Get().Drafts) > 0 {
				p.comments = append([]prComment(nil), s.session.Get().Drafts...)
				for _, c := range p.comments {
					if c.ID > p.nextID {
						p.nextID = c.ID
					}
				}
			}
			p.mu.Unlock()
		}
	}
}

var baseHrefRe = regexp.MustCompile(`<base\s+href="[^"]*">`)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	expected := s.BasePath()
	if r.URL.Path != expected && r.URL.Path != strings.TrimSuffix(expected, "/") {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(assets, "web/index.html")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	html := string(b)
	baseTag := fmt.Sprintf(`<base href="%s">`, expected)
	if strings.Contains(html, "<base ") {
		html = baseHrefRe.ReplaceAllString(html, baseTag)
	} else if idx := strings.Index(html, "<head>"); idx != -1 {
		html = html[:idx+6] + "\n" + baseTag + html[idx+6:]
	} else {
		html = baseTag + "\n" + html
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, html)
}

var (
	themesCSSOnce sync.Once
	themesCSSBody string
	themesCSSErr  error
)

// handleThemes joins web/themes/*.css into one stylesheet in file name order, so
// adding a theme means adding a file: there is no list to keep in sync.
func (s *Server) handleThemes(w http.ResponseWriter, r *http.Request) {
	themesCSSOnce.Do(func() {
		names, err := fs.Glob(assets, "web/themes/*.css")
		if err != nil {
			themesCSSErr = err
			return
		}
		var css strings.Builder
		for _, name := range names {
			b, err := fs.ReadFile(assets, name)
			if err != nil {
				themesCSSErr = err
				return
			}
			css.WriteString("/* " + strings.TrimPrefix(name, "web/") + " */\n")
			css.Write(b)
			css.WriteString("\n")
		}
		themesCSSBody = css.String()
	})
	if themesCSSErr != nil {
		fail(w, 500, themesCSSErr.Error())
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	io.WriteString(w, themesCSSBody)
}

func staticContentType(p string) string {
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

var (
	vendorFetchMu sync.Mutex
	vendorCDNs    = map[string]string{
		"vendor/mermaid-12.0.0.min.js": "https://cdn.jsdelivr.net/npm/mermaid@12.0.0/dist/mermaid.min.js",
	}
)

func vendorCacheDir() string {
	if c := os.Getenv("XDG_CACHE_HOME"); c != "" {
		return filepath.Join(c, "px0", "vendor")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".px0", "cache", "vendor")
	}
	return filepath.Join(os.TempDir(), "px0-cache", "vendor")
}

type memGzFile struct {
	*bytes.Reader
	name    string
	size    int64
	modTime time.Time
}

func (m *memGzFile) Stat() (fs.FileInfo, error) { return m, nil }
func (m *memGzFile) Close() error               { return nil }
func (m *memGzFile) Name() string               { return m.name }
func (m *memGzFile) Size() int64                { return m.size }
func (m *memGzFile) Mode() fs.FileMode          { return 0644 }
func (m *memGzFile) ModTime() time.Time         { return m.modTime }
func (m *memGzFile) IsDir() bool                { return false }
func (m *memGzFile) Sys() any                   { return nil }

func openOrFetchVendorAsset(rel string) (fs.File, error) {
	gzName := path.Base(rel) + ".gz"
	cachedPath := filepath.Join(vendorCacheDir(), gzName)

	// 1. Check local persistent cache
	if f, err := os.Open(cachedPath); err == nil {
		return f, nil
	}

	// 2. Check local repository web/ directory (for dev/test fallback)
	localGz := filepath.Join("web", filepath.FromSlash(rel)+".gz")
	if f, err := os.Open(localGz); err == nil {
		return f, nil
	}

	cdnURL, ok := vendorCDNs[rel]
	if !ok {
		return nil, os.ErrNotExist
	}

	vendorFetchMu.Lock()
	defer vendorFetchMu.Unlock()

	// Double-check cache after acquiring lock
	if f, err := os.Open(cachedPath); err == nil {
		return f, nil
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(cdnURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CDN returned HTTP %d for %s", resp.StatusCode, rel)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Compress downloaded asset with gzip level 9
	var gzBuf bytes.Buffer
	gw, err := gzip.NewWriterLevel(&gzBuf, gzip.BestCompression)
	if err != nil {
		gw = gzip.NewWriter(&gzBuf)
	}
	if _, err := gw.Write(body); err != nil {
		gw.Close()
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}

	gzBytes := gzBuf.Bytes()

	// Cache to disk for offline and subsequent requests
	if err := os.MkdirAll(vendorCacheDir(), 0755); err == nil {
		tmpPath := cachedPath + fmt.Sprintf(".%d.tmp", time.Now().UnixNano())
		if err := os.WriteFile(tmpPath, gzBytes, 0644); err == nil {
			_ = os.Rename(tmpPath, cachedPath)
		}
	}

	if f, err := os.Open(cachedPath); err == nil {
		return f, nil
	}
	return &memGzFile{
		Reader:  bytes.NewReader(gzBytes),
		name:    gzName,
		size:    int64(len(gzBytes)),
		modTime: time.Now(),
	}, nil
}

func (s *Server) handleStatic(sub fs.FS, prefix string) http.Handler {
	fileServer := http.StripPrefix(prefix, http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, prefix)
		rel = path.Clean(strings.TrimPrefix(rel, "/"))
		if rel == "." || strings.HasPrefix(rel, "..") {
			http.NotFound(w, r)
			return
		}

		// Check if a pre-compressed .gz asset exists for this path.
		gzPath := rel + ".gz"
		gzFile, err := sub.Open(gzPath)
		if err != nil && strings.HasPrefix(rel, "vendor/") {
			gzFile, err = openOrFetchVendorAsset(rel)
		}
		if err == nil {
			defer gzFile.Close()
			fi, err := gzFile.Stat()
			if err == nil && !fi.IsDir() {
				mimeType := staticContentType(rel)
				if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
					bypassGzip(w)
					w.Header().Set("Content-Type", mimeType)
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Vary", "Accept-Encoding")
					if strings.HasPrefix(rel, "vendor/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					if rs, ok := gzFile.(io.ReadSeeker); ok {
						http.ServeContent(w, r, path.Base(rel), fi.ModTime(), rs)
						return
					}
					io.Copy(w, gzFile)
					return
				}

				// Client does not accept gzip: decompress on the fly.
				gzReader, err := gzip.NewReader(gzFile)
				if err == nil {
					defer gzReader.Close()
					w.Header().Set("Content-Type", mimeType)
					w.Header().Set("Vary", "Accept-Encoding")
					if strings.HasPrefix(rel, "vendor/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					io.Copy(w, gzReader)
					return
				}
			}
		}

		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	n, at, ms := s.ix.Stats()
	inGit := gitAvailable(s.ix.Root())
	if inGit && s.ix.Ready() {
		s.ix.mu.RLock()
		noStatus := s.ix.gitStatusMap == nil
		s.ix.mu.RUnlock()
		if noStatus {
			s.ix.UpdateGitStatus()
		}
	}
	gitCount, gitFiles := s.ix.GitChanges()
	meta := map[string]any{
		"root":        s.ix.Root(),
		"name":        filepath.Base(s.ix.Root()),
		"files":       n,
		"indexMs":     ms,
		"builtAt":     at,
		"ready":       s.ix.Ready(),
		"git":         inGit,
		"gitChanges":  gitCount,
		"gitFiles":    gitFiles,
		"forge":       s.forgeMeta(),
		"lspServers":  s.lsp.Available(),
		"metrics":     getProcessMetrics(s.lsp),
		"version":     version,
		"basePath":    s.BasePath(),
		"workbench":   s.workbenchURL,
		"agent":       s.agent.Name(),
		"agentModel":  s.agent.Model(),
		"agentPinned": s.agent.Pinned(),
		"agents":      []agentHarness{},
	}
	if s.pr != nil {
		meta["pr"] = s.pr.metaMap(s.ix.PRFiles())
	}
	writeJSON(w, meta)
}

func getProcessMetrics(lsp *lspManager) metrics.ProcessMetrics {
	if lsp == nil {
		return metrics.Collect(0, false)
	}
	return metrics.Collect(lsp.memBytes(), lsp.Enabled())
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, getProcessMetrics(s.lsp))
}

// lspCtx bounds how long a caller is willing to wait. Language servers can take
// tens of seconds to index a large workspace on first use, so the budget is
// generous but always finite.
func lspCtx(r *http.Request) (context.Context, context.CancelFunc) {
	ms, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	if ms <= 0 {
		ms = 10000
	}
	if ms > 120000 {
		ms = 120000
	}
	return context.WithTimeout(r.Context(), time.Duration(ms)*time.Millisecond)
}

// lspPos pulls the shared path/line/col arguments. col arrives in UTF-16 code
// units because that is what JavaScript string offsets count.
func (s *Server) lspPos(r *http.Request) (abs, rel string, line, col int, ok bool) {
	q := r.URL.Query()
	abs, rel, ok = s.resolvePath(q.Get("path"))
	if !ok {
		return
	}
	line, _ = strconv.Atoi(q.Get("line"))
	col, _ = strconv.Atoi(q.Get("col"))
	if line < 1 {
		line = 1
	}
	if col < 0 {
		col = 0
	}
	return abs, rel, line, col, true
}

func (s *Server) lspRespond(w http.ResponseWriter, rel string, hits []NavHit, err error) {
	state, server := s.lsp.State(rel)
	if err != nil {
		writeJSON(w, map[string]any{
			"hits": []NavHit{}, "state": string(state), "server": server,
			"error": err.Error(),
		})
		return
	}
	if hits == nil {
		hits = []NavHit{}
	}
	writeJSON(w, map[string]any{"hits": hits, "state": string(state), "server": server})
}

func (s *Server) handleLSPDef(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	abs, rel, line, col, ok := s.lspPos(r)
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ctx, cancel := lspCtx(r)
	defer cancel()
	hits, err := s.lsp.Definition(ctx, abs, rel, line, col)
	if ui.Verbose {
		dur := ui.FmtDuration(time.Since(start))
		if len(hits) == 1 {
			dest := fmt.Sprintf("%s:%d", hits[0].Path, hits[0].Line)
			ui.Status("info", "lsp def", fmt.Sprintf("%s:%d:%d -> %s  (%s)", rel, line, col, dest, dur), 0, os.Stdout)
		} else {
			ui.Status("info", "lsp def", fmt.Sprintf("%s:%d:%d · %d hits  (%s)", rel, line, col, len(hits), dur), 0, os.Stdout)
		}
	}
	s.lspRespond(w, rel, hits, err)
}

func (s *Server) handleLSPRefs(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	abs, rel, line, col, ok := s.lspPos(r)
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ctx, cancel := lspCtx(r)
	defer cancel()
	hits, err := s.lsp.References(ctx, abs, rel, line, col)
	if ui.Verbose {
		dur := ui.FmtDuration(time.Since(start))
		files := make(map[string]bool)
		for _, h := range hits {
			files[h.Path] = true
		}
		ui.Status("info", "lsp refs", fmt.Sprintf("%s:%d:%d · %d refs in %d files  (%s)", rel, line, col, len(hits), len(files), dur), 0, os.Stdout)
	}
	s.lspRespond(w, rel, hits, err)
}

// handleLSPCalls serves call trails. Without item it resolves the function at
// path/line/col into trail roots; with item (a node's opaque item, echoed back)
// it expands that node into callers, or callees when dir=out. path always names
// the file the trail started in, which picks the language server.
func (s *Server) handleLSPCalls(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	abs, rel, line, col, ok := s.lspPos(r)
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ctx, cancel := lspCtx(r)
	defer cancel()
	q := r.URL.Query()
	var nodes []CallNode
	var err error
	if item := q.Get("item"); item != "" {
		nodes, err = s.lsp.Calls(ctx, rel, item, q.Get("dir") == "out")
	} else {
		nodes, err = s.lsp.PrepareCalls(ctx, abs, rel, line, col)
	}
	if nodes == nil {
		nodes = []CallNode{}
	}
	state, server := s.lsp.State(rel)
	resp := map[string]any{"nodes": nodes, "state": string(state), "server": server}
	if err != nil {
		resp["error"] = err.Error()
	}
	if ui.Verbose {
		dir := "callers"
		if q.Get("dir") == "out" {
			dir = "callees"
		}
		ui.Status("info", "lsp calls", fmt.Sprintf("%s:%d:%d (%s) · %d nodes  (%s)", rel, line, col, dir, len(nodes), ui.FmtDuration(time.Since(start))), 0, os.Stdout)
	}
	writeJSON(w, resp)
}

// handleLSPWarm starts the server for this file type if it is not running and
// reports where it has got to. Opening a file calls this so the server is awake
// by the time the reader wants to hover or jump, and so the status indicator
// reflects reality without anyone having to ask a question first.
func (s *Server) handleLSPWarm(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ms, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	if ms <= 0 {
		ms = 1
	}
	if ms > 60000 {
		ms = 60000
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(ms)*time.Millisecond)
	defer cancel()
	// The spawn keeps going even when this call gives up waiting on it.
	_ = s.lsp.EnsureOpen(ctx, abs, rel)
	writeJSON(w, s.lspBrief(rel))
}

func (s *Server) handleLSPHover(w http.ResponseWriter, r *http.Request) {
	abs, rel, line, col, ok := s.lspPos(r)
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ctx, cancel := lspCtx(r)
	defer cancel()
	info, err := s.lsp.Hover(ctx, abs, rel, line, col)
	state, srv := s.lsp.State(rel)
	if err != nil || info == nil {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		writeJSON(w, map[string]any{"empty": true, "state": string(state), "server": srv, "error": msg})
		return
	}
	writeJSON(w, map[string]any{
		"signature": info.Signature, "doc": info.Doc, "empty": info.Empty,
		"state": string(state), "server": srv,
	})
}

func (s *Server) handleLSPSymbols(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ctx, cancel := lspCtx(r)
	defer cancel()
	syms, err := s.lsp.Symbols(ctx, abs, rel)
	state, server := s.lsp.State(rel)
	if err != nil {
		writeJSON(w, map[string]any{
			"symbols": []symbols.Symbol{}, "state": string(state), "server": server, "error": err.Error(),
		})
		return
	}
	if syms == nil {
		syms = []symbols.Symbol{}
	}
	writeJSON(w, map[string]any{"symbols": syms, "state": string(state), "server": server})
}

func (s *Server) handleLSPDiagnostics(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	diags := s.lsp.Diagnostics(abs, rel)
	state, server := s.lsp.State(rel)
	writeJSON(w, map[string]any{
		"path":        rel,
		"diagnostics": diags,
		"state":       string(state),
		"server":      server,
	})
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	dir := strings.Trim(r.URL.Query().Get("dir"), "/")
	kids, ok := s.ix.Children(dir)
	if !ok && !s.ix.Ready() {
		// If indexing is still in flight, wait up to 300ms for this directory to be scanned
		for i := 0; i < 30; i++ {
			time.Sleep(10 * time.Millisecond)
			if kids, ok = s.ix.Children(dir); ok {
				break
			}
			if s.ix.Ready() {
				kids, ok = s.ix.Children(dir)
				break
			}
		}
	}
	if !ok {
		fail(w, 404, "not indexed: "+dir)
		return
	}
	writeJSON(w, map[string]any{"dir": dir, "children": kids})
}

func (s *Server) handleFind(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if !s.ix.Ready() {
		s.ix.WaitReady(300 * time.Millisecond)
	}
	ready := s.ix.Ready()
	res := fuzzy.Find(s.ix.Files(), q, limit)
	if res == nil {
		res = []fuzzy.Result{}
	}
	if ui.Verbose && q != "" {
		dur := ui.FmtDuration(time.Since(start))
		ui.Status("info", "find", fmt.Sprintf("%q · %d files  (%s)", q, len(res), dur), 0, os.Stdout)
	}
	writeJSON(w, map[string]any{"results": res, "indexing": !ready})
}

var imageExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".svg": true, ".ico": true, ".bmp": true, ".avif": true,
}

var imageMime = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
	".ico":  "image/x-icon",
	".bmp":  "image/bmp",
	".avif": "image/avif",
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	abs, rel, ok := s.resolvePath(q.Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) && gitAvailable(s.ix.Root()) {
			diffAvail := false
			// A file a local commit deleted has nothing on disk, so the normal
			// working-tree check below can't vouch for it. Its commit diff can.
			if ref := q.Get("ref"); ref != "" {
				diffAvail = gitDiffCommit(s.ix.Root(), rel, ref) != ""
			} else if avail, ok := s.ix.HasDiffAvailable(rel, s.diffBase, s.pr != nil); ok {
				diffAvail = avail
			} else if s.pr != nil {
				diffAvail = gitDiffAgainst(s.ix.Root(), rel, s.diffBase) != "" ||
					gitDiffBetween(s.ix.Root(), rel, s.diffBase, s.prHeadSHA) != "" ||
					gitDiffAgainst(s.ix.Root(), rel, s.prHeadSHA) != ""
			} else {
				diffAvail = gitDiffAgainst(s.ix.Root(), rel, s.diffBase) != ""
			}
			if diffAvail {
				d := render.NewDoc("", rel)
				if ui.Verbose {
					ui.Status("info", "view", fmt.Sprintf("%s · deleted (0 bytes)", rel), 0, os.Stdout)
				}
				writeJSON(w, map[string]any{
					"path": rel, "lang": d.Lang, "total": 0, "maxCols": 0,
					"start": 0, "lines": []string{}, "size": 0,
					"exact": true, "refine": false,
					"markdown":      render.IsMarkdown(rel),
					"table":         table.IsTable(rel),
					"diffAvailable": true,
					"deleted":       true,
					"lsp":           s.lspBrief(rel),
				})
				return
			}
		}
		fail(w, 404, err.Error())
		return
	}
	if imageExt[strings.ToLower(filepath.Ext(rel))] {
		if ui.Verbose {
			ui.Status("info", "view", fmt.Sprintf("%s · image (%s)", rel, ui.FormatBytes(st.Size())), 0, os.Stdout)
		}
		writeJSON(w, map[string]any{"path": rel, "image": true, "size": st.Size()})
		return
	}

	d, err := render.Open(abs, rel)
	if err != nil {
		fail(w, 415, err.Error())
		return
	}
	start, _ := strconv.Atoi(q.Get("start"))
	count, _ := strconv.Atoi(q.Get("count"))
	if count <= 0 {
		count = render.ChunkLines
	}
	if start < 0 {
		start = 0
	}
	if start > d.Total {
		start = d.Total
	}
	if q.Get("text") == "1" {
		if version := q.Get("version"); version != "" && version != d.Version() {
			fail(w, http.StatusConflict, "file changed; reload before copying")
			return
		}
		end := d.Total
		if count < d.Total-start {
			end = start + count
		}
		writeJSON(w, map[string]any{"text": d.RawText(start, end), "version": d.Version()})
		return
	}
	if ui.Verbose && start == 0 {
		ui.Status("info", "view", fmt.Sprintf("%s · %d lines (%s)", rel, d.Total, ui.FormatBytes(st.Size())), 0, os.Stdout)
	}
	lines, exact := d.Lines(start, start+count)
	_, coming := d.Exact()
	diffAvail := false
	if gitAvailable(s.ix.Root()) {
		if ref := q.Get("ref"); ref != "" {
			if base := q.Get("base"); base != "" && validSHA(base) && validSHA(ref) {
				diffAvail = gitDiffBetween(s.ix.Root(), rel, base, ref) != ""
			} else {
				// Pinned to one commit: what matters is whether that commit touched
				// the file, not whether the working tree has since diverged.
				diffAvail = gitDiffCommit(s.ix.Root(), rel, ref) != ""
			}
		} else if avail, ok := s.ix.HasDiffAvailable(rel, s.diffBase, s.pr != nil); ok {
			diffAvail = avail
		} else if s.pr != nil {
			diffAvail = gitDiffAgainst(s.ix.Root(), rel, s.diffBase) != "" ||
				gitDiffBetween(s.ix.Root(), rel, s.diffBase, s.prHeadSHA) != "" ||
				gitDiffAgainst(s.ix.Root(), rel, s.prHeadSHA) != ""
		} else {
			diffAvail = gitDiffAgainst(s.ix.Root(), rel, s.diffBase) != ""
		}
	}
	resp := map[string]any{
		"path": rel, "lang": d.Lang, "total": d.Total, "maxCols": d.MaxCols,
		"version": d.Version(),
		"start":   start, "lines": lines, "size": st.Size(),
		"exact": exact, "refine": !exact && coming,
		"markdown":      render.IsMarkdown(rel),
		"table":         table.IsTable(rel),
		"diffAvailable": diffAvail,
		"lsp":           s.lspBrief(rel),
		"diagnostics":   s.lsp.Diagnostics(abs, rel),
	}
	if q.Get("raw") == "1" {
		rawLines := make([]string, len(lines))
		for i := range lines {
			rawLines[i] = d.Raw(start + i + 1)
		}
		resp["raw"] = rawLines
	}
	writeJSON(w, resp)
}

// Detection is requested after first paint. Two workers bound concurrent scans
// across tabs/workspaces; queued requests do not retain document source buffers.
var foldWorkers = make(chan struct{}, 2)

func (s *Server) handleFolds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	select {
	case foldWorkers <- struct{}{}:
		defer func() { <-foldWorkers }()
	case <-ctx.Done():
		fail(w, http.StatusRequestTimeout, "fold detection cancelled")
		return
	}
	d, err := render.Open(abs, rel)
	if err != nil {
		fail(w, 415, err.Error())
		return
	}
	if version := r.URL.Query().Get("version"); version != "" && version != d.Version() {
		fail(w, http.StatusConflict, "file changed; reload before folding")
		return
	}
	ranges, limited, err := d.Folds(ctx)
	if err != nil {
		fail(w, http.StatusRequestTimeout, "fold detection cancelled")
		return
	}
	if ranges == nil {
		ranges = []render.FoldRange{}
	}
	writeJSON(w, map[string]any{"ranges": ranges, "version": d.Version(), "limited": limited})
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	abs, rel, ok := s.resolvePath(q.Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	render.Evict(abs)
	s.lsp.CloseDoc(abs, rel)
	writeJSON(w, map[string]any{"ok": true, "path": rel})
}

func (s *Server) handleMarkdown(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	if !render.IsMarkdown(rel) {
		fail(w, 415, "not a Markdown file")
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	if st.IsDir() {
		fail(w, 415, "is a directory")
		return
	}
	if st.Size() > render.MaxMarkdownBytes {
		fail(w, 413, "too large to preview")
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	out, err := render.RenderMarkdown(data)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"path": rel, "html": out})
}

// handleTable serves a CSV or TSV file parsed into rows for the table view.
// Cells go back as plain strings; the client inserts them as text, never HTML.
func (s *Server) handleTable(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	delim, ok := table.Delim(rel)
	if !ok {
		fail(w, 415, "not a CSV or TSV file")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	if st.IsDir() {
		fail(w, 415, "is a directory")
		return
	}
	res, err := table.Render(f, st.Size(), delim)
	if err != nil {
		var pe *csv.ParseError
		if errors.As(err, &pe) {
			fail(w, 422, err.Error())
			return
		}
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, res)
}

func setRawHeaders(w http.ResponseWriter, rel string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	base := filepath.Base(rel)
	cd := mime.FormatMediaType("attachment", map[string]string{
		"filename": base,
	})
	if cd == "" {
		cd = fmt.Sprintf(`attachment; filename=%q`, base)
	}
	w.Header().Set("Content-Disposition", cd)

	ext := strings.ToLower(filepath.Ext(rel))
	ct := ""
	if imageExt[ext] {
		ct = mime.TypeByExtension(ext)
		if ct == "" {
			ct = imageMime[ext]
		}
	}
	if ct == "" || !strings.HasPrefix(ct, "image/") {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.safePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	setRawHeaders(w, rel)
	http.ServeFile(w, r, abs)
}

type fileSaveRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (s *Server) handleFileSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req fileSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	abs, rel, ok := s.safePath(req.Path)
	if !ok || rel == "" {
		fail(w, http.StatusBadRequest, "bad path")
		return
	}

	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, http.StatusInternalServerError, "failed to create directory: "+err.Error())
		return
	}

	if st, err := os.Stat(abs); err == nil && st.IsDir() {
		fail(w, http.StatusBadRequest, "target is a directory")
		return
	}

	if err := os.WriteFile(abs, []byte(req.Content), 0o644); err != nil {
		fail(w, http.StatusInternalServerError, "failed to write file: "+err.Error())
		return
	}

	render.Evict(abs)
	if s.lsp != nil {
		s.lsp.CloseDoc(abs, rel)
		s.lsp.RefreshOpenDocs()
	}
	s.ix.TouchPath(rel)
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}

	writeJSON(w, map[string]any{
		"ok":   true,
		"path": rel,
		"size": len(req.Content),
	})
}

type fsNewRequest struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

func (s *Server) handleFSNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req fsNewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	abs, rel, ok := s.safePath(req.Path)
	if !ok || rel == "" {
		fail(w, http.StatusBadRequest, "bad path")
		return
	}

	if _, err := os.Stat(abs); err == nil {
		fail(w, http.StatusConflict, "already exists: "+rel)
		return
	}

	if req.Type == "dir" {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			fail(w, http.StatusInternalServerError, "failed to create directory: "+err.Error())
			return
		}
	} else {
		dir := filepath.Dir(abs)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(w, http.StatusInternalServerError, "failed to create parent directory: "+err.Error())
			return
		}
		if err := os.WriteFile(abs, []byte(""), 0o644); err != nil {
			fail(w, http.StatusInternalServerError, "failed to create file: "+err.Error())
			return
		}
		render.Evict(abs)
	}

	s.ix.TouchPath(rel)
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}

	writeJSON(w, map[string]any{
		"ok":   true,
		"path": rel,
		"type": req.Type,
	})
}

// handleDiff returns the unified diff of a file against HEAD. available is false
// (with an empty diff and 200) when git is off/absent or the file is unchanged.
//
// In a PR review session, "diff" stays the full merge-base..working-tree diff
// for backward compatibility, but the response also splits it into prDiff
// (diffBase..prHeadSHA -- the PR's own, frozen change) and yourDiff
// (prHeadSHA..working-tree -- what the reviewer has edited/committed locally
// since checkout). Committing in that session only ever changes yourDiff, so
// the frontend can label the two apart instead of showing one blended diff
// that looks the same whether or not the reviewer has touched anything.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	_, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	// A ref names one local commit to read instead of the working tree: the
	// diff that commit itself made to this file, frozen regardless of what has
	// been edited since. Empty ref -> today's working-tree-vs-diffBase diff.
	if ref := r.URL.Query().Get("ref"); ref != "" {
		base := r.URL.Query().Get("base")
		var diff string
		if base != "" && validSHA(base) && validSHA(ref) {
			diff = gitDiffBetween(s.ix.Root(), rel, base, ref)
		} else {
			diff = gitDiffCommit(s.ix.Root(), rel, ref)
		}
		writeJSON(w, map[string]any{
			"path":      rel,
			"ref":       ref,
			"base":      base,
			"diff":      diff,
			"hunks":     render.HighlightDiff(rel, diff),
			"available": diff != "",
		})
		return
	}
	diff := gitDiffAgainst(s.ix.Root(), rel, s.diffBase)
	if ui.Verbose {
		status := "clean"
		if diff != "" {
			lines := strings.Count(diff, "\n")
			status = fmt.Sprintf("%d diff lines", lines)
		}
		ui.Status("info", "diff", fmt.Sprintf("%s · %s", rel, status), 0, os.Stdout)
	}
	avail := diff != ""
	resp := map[string]any{
		"path":  rel,
		"diff":  diff,
		"hunks": render.HighlightDiff(rel, diff),
	}
	if s.pr != nil {
		prDiff := gitDiffBetween(s.ix.Root(), rel, s.diffBase, s.prHeadSHA)
		yourDiff := gitDiffAgainst(s.ix.Root(), rel, s.prHeadSHA)
		resp["prDiff"] = prDiff
		resp["yourDiff"] = yourDiff
		resp["prHunks"] = render.HighlightDiff(rel, prDiff)
		resp["yourHunks"] = render.HighlightDiff(rel, yourDiff)
		avail = avail || prDiff != "" || yourDiff != ""
	}
	resp["available"] = avail
	writeJSON(w, resp)
}

// handleGutter returns per-file changed-line ranges (new-file line numbers) for
// a VS Code-style change gutter. available is false (200, empty arrays) when
// git is off/absent or the file is unchanged/untracked; never 500 for those.
func (s *Server) handleGutter(w http.ResponseWriter, r *http.Request) {
	_, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	var added, modified, deleted []int
	if ref := r.URL.Query().Get("ref"); ref != "" {
		added, modified, deleted = gitHunksCommit(s.ix.Root(), rel, ref)
	} else {
		added, modified, deleted = gitHunksAgainst(s.ix.Root(), rel, s.diffBase)
	}
	nz := func(v []int) []int { // marshal as [] not null
		if v == nil {
			return []int{}
		}
		return v
	}
	writeJSON(w, map[string]any{
		"path":      rel,
		"available": added != nil || modified != nil || deleted != nil,
		"added":     nz(added),
		"modified":  nz(modified),
		"deleted":   nz(deleted),
	})
}

// handleEventStream streams real-time workspace events (git status notifications and process metrics) via Server-Sent Events (SSE).
func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	includeMetrics := r.URL.Path != s.routePath("/api/git/stream")

	// 1. Immediately send initial metrics on connection (for unified stream)
	if includeMetrics {
		if mBytes, err := json.Marshal(getProcessMetrics(s.lsp)); err == nil {
			if _, err := fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", mBytes); err != nil {
				return
			}
			flusher.Flush()
		}
	}

	// 2. Subscribe to git watcher if available
	var gitCh <-chan []byte
	if s.gitWatcher != nil {
		var cancel func()
		gitCh, cancel = s.gitWatcher.Subscribe()
		defer cancel()
	}

	// 3. Periodic metrics ticker (2500ms) for unified stream
	var metricsTicker *time.Ticker
	var metricsC <-chan time.Time
	if includeMetrics {
		metricsTicker = time.NewTicker(2500 * time.Millisecond)
		defer metricsTicker.Stop()
		metricsC = metricsTicker.C
	}

	// 4. Heartbeat ticker (15s) in case gitWatcher is disabled or not ticking
	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.streamsDone:
			return

		case msg, ok := <-gitCh:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()

		case <-metricsC:
			if mBytes, err := json.Marshal(getProcessMetrics(s.lsp)); err == nil {
				if _, err := fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", mBytes); err != nil {
					return
				}
				flusher.Flush()
			}

		case <-heartbeatTicker.C:
			if _, err := fmt.Fprintf(w, "event: ping\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleGitRefresh triggers an immediate git status check and returns the latest git summary.
func (s *Server) handleGitRefresh(w http.ResponseWriter, r *http.Request) {
	if s.gitWatcher != nil {
		payload := s.gitWatcher.Refresh()
		writeJSON(w, payload)
		return
	}
	count, files := s.ix.GitChanges()
	writeJSON(w, map[string]any{
		"git":        gitAvailable(s.ix.Root()),
		"gitChanges": count,
		"gitFiles":   files,
		"statuses":   s.ix.GitStatusMap(),
	})
}

func (s *Server) decodeGitPath(w http.ResponseWriter, r *http.Request) (rel string, ok bool) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || body.Path == "" {
		fail(w, http.StatusBadRequest, "path is required")
		return "", false
	}
	_, rel, ok = s.safePath(body.Path)
	if !ok {
		fail(w, http.StatusBadRequest, "bad path")
		return "", false
	}
	return rel, true
}

// handleGitStage adds a file to the index (POST {path}).
func (s *Server) handleGitStage(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	rel, ok := s.decodeGitPath(w, r)
	if !ok {
		return
	}
	if err := gitStage(s.ix.Root(), rel); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if s.gitWatcher != nil {
		payload := s.gitWatcher.Refresh()
		writeJSON(w, payload)
		return
	}
	s.ix.UpdateGitStatus()
	writeJSON(w, map[string]any{"ok": true})
}

// handleGitUnstage removes a file from the index without touching the
// working tree (POST {path}).
func (s *Server) handleGitUnstage(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	rel, ok := s.decodeGitPath(w, r)
	if !ok {
		return
	}
	if err := gitUnstage(s.ix.Root(), rel); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if s.gitWatcher != nil {
		payload := s.gitWatcher.Refresh()
		writeJSON(w, payload)
		return
	}
	s.ix.UpdateGitStatus()
	writeJSON(w, map[string]any{"ok": true})
}

// handleGitHunk stages, unstages, or discards a single unified diff hunk
// (POST {path, hunk, action}).
func (s *Server) handleGitHunk(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var body struct {
		Path   string `json:"path"`
		Hunk   string `json:"hunk"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Path == "" || body.Hunk == "" || body.Action == "" {
		fail(w, http.StatusBadRequest, "path, hunk, and action are required")
		return
	}
	abs, rel, ok := s.safePath(body.Path)
	if !ok || rel == "" {
		fail(w, http.StatusBadRequest, "bad path")
		return
	}
	if err := gitApplyHunk(s.ix.Root(), rel, body.Hunk, body.Action); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if body.Action == "discard" {
		render.Evict(abs)
		if s.lsp != nil {
			s.lsp.CloseDoc(abs, rel)
			s.lsp.RefreshOpenDocs()
		}
		s.ix.TouchPath(rel)
	}
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}
	writeJSON(w, map[string]any{"ok": true, "path": rel, "action": body.Action})
}

// handleGitCommit commits whatever is currently staged (POST {message}).
func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		fail(w, http.StatusBadRequest, "message is required")
		return
	}
	root := s.ix.Root()
	if len(gitStagedPaths(root)) == 0 && gitHasUncommittedChanges(root) {
		_ = gitStage(root, ".")
	}
	if err := gitCommit(root, strings.TrimSpace(body.Message)); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}
	s.tel.Track("git_commit", nil)
	writeJSON(w, map[string]any{"ok": true})
}

// handleGitCommitMessage dispatches the selected coding harness to write a
// commit message for the currently staged diff, honoring the
// git.commitMessageInstruction setting. Returns an agent job the frontend
// polls via the existing /api/agent/job, the same as an inline edit.
func (s *Server) handleGitCommitMessage(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	root := s.ix.Root()
	files := gitStagedFiles(root)
	if len(files) == 0 {
		// Auto-stage all uncommitted changes if nothing is staged
		if gitHasUncommittedChanges(root) {
			_ = gitStage(root, ".")
			files = gitStagedFiles(root)
			if s.gitWatcher != nil {
				s.gitWatcher.Trigger()
			}
		}
	}
	if len(files) == 0 {
		fail(w, http.StatusBadRequest, "nothing staged to generate a message for")
		return
	}
	stat := gitStagedStat(root)
	diff := gitStagedDiff(root)
	instruction := ""
	if cfg := settings.Read(); cfg.GitCommitMessageInstruction != nil {
		instruction = strings.TrimSpace(*cfg.GitCommitMessageInstruction)
	}
	job, err := s.agent.StartPrompt("commit message", commitMessagePrompt(files, stat, diff, instruction))
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errAgentNone) {
			code = http.StatusBadRequest
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, job)
}

// handleGitPush pushes the current branch to its remote -- or, in a PR
// review session, pushes the worktree's HEAD to the PR's actual head branch
// (possibly a fork), which may itself be a fresh branch with no upstream.
func (s *Server) handleGitPush(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if s.pr != nil {
		// force is only sent after the reviewer confirmed pushing a rebased
		// PR branch; it is a lease, never a blind overwrite (prSession.Push).
		var body struct {
			Force bool `json:"force"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body)
		if op := gitOpInProgress(s.ix.Root()); op != "" {
			fail(w, http.StatusConflict, "a "+op+" is in progress; finish or abort it before pushing")
			return
		}
		if err := s.pr.Push(body.Force); err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		// Pushed commits are part of the PR now: move the PR-head boundary so
		// they stop being "yours" (and the PR's file set picks them up).
		s.syncPRRefs()
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	root := s.ix.Root()
	out, err := gitPush(root)
	if err != nil && strings.Contains(out, "has no upstream branch") {
		if branch := gitCurrentBranch(root); branch != "" && branch != "HEAD" {
			out, err = gitPushSetUpstream(root, "origin", branch)
		}
	}
	if err != nil {
		fail(w, http.StatusBadGateway, out)
		return
	}
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleGitPull fast-forwards onto the latest remote -- or, in a PR review
// session, the PR's current head. Never merges: a non-fast-forward is
// refused outright (409), since resolving a real conflict isn't supported.
func (s *Server) handleGitPull(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if s.pr != nil {
		if op := gitOpInProgress(s.ix.Root()); op != "" {
			st, _ := s.prSyncState(false)
			failSync(w, st, "a "+op+" is already in progress")
			return
		}
		info, err := s.pr.Pull()
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, errPRDiverged) {
				status = http.StatusConflict
			}
			fail(w, status, err.Error())
			return
		}
		s.syncPRRefs()
		writeJSON(w, map[string]any{"ok": true, "message": info})
		return
	}

	root := s.ix.Root()
	if op := gitOpInProgress(root); op != "" {
		failSync(w, s.gitSyncState(root), "a "+op+" is already in progress")
		return
	}
	if gitHasUncommittedChanges(root) {
		fail(w, http.StatusConflict, "commit or discard your local changes before pulling")
		return
	}
	branch := gitCurrentBranch(root)
	if branch == "" || branch == "HEAD" {
		fail(w, http.StatusBadRequest, "not on a branch")
		return
	}
	remote, remoteBranch, ok := gitUpstream(root, branch)
	if !ok {
		fail(w, http.StatusBadRequest, "no upstream branch configured for "+branch)
		return
	}
	if err := gitFFOnlyPull(root, remote, remoteBranch); err != nil {
		if errors.Is(err, errNotFastForward) {
			// The fetch already landed, so the counts reflect the remote as
			// it is now. The panel offers a rebase or merge from here.
			st := s.gitSyncState(root)
			st.Code = "diverged"
			failSync(w, st, fmt.Sprintf("can't fast-forward: %s has diverged from %s", branch, st.Upstream))
			return
		}
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}
	writeJSON(w, map[string]any{"ok": true, "message": "pulled the latest changes"})
}

// gitSyncState is where the current branch stands against its upstream:
// what the git panel needs to offer (or follow up on) a rebase or merge.
type gitSyncState struct {
	Code       string `json:"code,omitempty"` // "diverged" or "in-progress" on a refused pull
	Branch     string `json:"branch"`
	Upstream   string `json:"upstream"` // e.g. "origin/main"; empty when none is configured; a PR's base branch name
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
	InProgress string `json:"inProgress,omitempty"` // "rebase" or "merge" left half-done
	Dirty      bool   `json:"dirty"`

	// PR review sessions only: the PR number, and whether the checkout was
	// rewritten (rebased) past the PR head on the remote, so only a
	// force-push can update the PR.
	PR        int  `json:"pr,omitempty"`
	Rewritten bool `json:"rewritten,omitempty"`

	ontoRef string // the ref px0 fetched the PR's base into; the harness rebases onto it and never fetches
}

// syncPRRefs copies the PR session's head and merge-base into the server and
// index after anything moved them (push, pull), so diffs, the file tree and
// the ahead count all measure from the new boundary.
func (s *Server) syncPRRefs() {
	s.pr.mu.Lock()
	s.diffBase = s.pr.diffBase
	s.prHeadSHA = s.pr.meta.HeadSHA
	s.pr.mu.Unlock()
	if s.ix != nil {
		s.ix.SetDiffBase(s.diffBase)
		s.ix.SetPRHead(s.prHeadSHA)
		s.ix.SetPushedHead(s.prHeadSHA)
	}
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}
}

// prSyncState is gitSyncState for a PR review: the detached checkout
// against the PR's base branch rather than an upstream. With fetch it
// refreshes the base first (a network call), as a rebase must start from
// the base as it is now.
func (s *Server) prSyncState(fetch bool) (gitSyncState, error) {
	p := s.pr
	root := s.ix.Root()
	p.mu.Lock()
	st := gitSyncState{PR: p.meta.Number, Branch: p.meta.HeadRef, Upstream: p.meta.BaseRef, ontoRef: prBaseRef(p.meta.Number)}
	p.mu.Unlock()
	st.InProgress = gitOpInProgress(root)
	if st.InProgress != "" {
		st.Code = "in-progress"
	}
	var err error
	if fetch && st.InProgress == "" {
		_, err = p.FetchBase()
	}
	st.Ahead, st.Behind = gitAheadBehindRef(root, st.ontoRef)
	st.Rewritten = p.Rewritten()
	st.Dirty = gitHasUncommittedChanges(root)
	return st, err
}

func (s *Server) gitSyncState(root string) gitSyncState {
	st := gitSyncState{Branch: gitCurrentBranch(root), InProgress: gitOpInProgress(root)}
	if st.InProgress != "" {
		st.Code = "in-progress"
	}
	if st.InProgress == "rebase" {
		if b := gitRebasingBranch(root); b != "" {
			st.Branch = b
		}
	}
	if st.Branch != "" && st.Branch != "HEAD" {
		if remote, rb, ok := gitUpstream(root, st.Branch); ok {
			st.Upstream = remote + "/" + rb
		}
	}
	st.Ahead, st.Behind, _ = gitAheadBehind(root)
	st.Dirty = gitHasUncommittedChanges(root)
	return st
}

// failSync is a 409 that carries the sync state alongside the message, so
// the panel can render the next step instead of just the refusal.
func failSync(w http.ResponseWriter, st gitSyncState, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
		gitSyncState
	}{msg, st})
}

// handleGitSyncState reports the branch's standing against its upstream,
// read after an agent rebase/merge finishes to tell done from half-done.
func (s *Server) handleGitSyncState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.pr != nil {
		st, err := s.prSyncState(r.URL.Query().Get("fetch") == "1")
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, st)
		return
	}
	if !gitAvailable(s.ix.Root()) {
		writeJSON(w, gitSyncState{})
		return
	}
	writeJSON(w, s.gitSyncState(s.ix.Root()))
}

// handleGitSyncAgent dispatches the selected harness to rebase (or merge)
// the current branch onto its upstream after a pull refused to
// fast-forward. Only ever on an explicit click: px0 itself still never
// rewrites history, and the harness is told never to push. When a rebase or
// merge is already half-done (the last attempt stopped on a conflict), the
// harness is asked to finish that one instead of starting over.
func (s *Server) handleGitSyncAgent(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	var body struct {
		Strategy string `json:"strategy"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&body)
	if body.Strategy != "rebase" && body.Strategy != "merge" {
		fail(w, http.StatusBadRequest, "strategy must be rebase or merge")
		return
	}
	root := s.ix.Root()
	var st gitSyncState
	if s.pr != nil {
		// In a PR review, the branch is brought up to date with the PR's
		// base: fetched here, fresh, so the harness never fetches itself.
		var err error
		if st, err = s.prSyncState(true); err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
	} else {
		st = s.gitSyncState(root)
	}
	if st.InProgress == "" && st.Dirty {
		fail(w, http.StatusConflict, "commit or discard your local changes before a "+body.Strategy)
		return
	}
	if st.InProgress != "" && st.InProgress != body.Strategy {
		fail(w, http.StatusConflict, "a "+st.InProgress+" is already in progress; abort it first")
		return
	}
	if st.Upstream == "" {
		fail(w, http.StatusBadRequest, "no upstream branch configured for "+st.Branch)
		return
	}
	if st.InProgress == "" && st.PR > 0 && st.Behind == 0 {
		fail(w, http.StatusConflict, "the PR already contains everything on "+st.Upstream+"; nothing to "+body.Strategy)
		return
	}
	job, err := s.agent.StartPrompt(body.Strategy+" onto "+st.Upstream, gitSyncPrompt(body.Strategy, st))
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errAgentNone) {
			code = http.StatusBadRequest
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, job)
}

// handleGitAbort aborts a rebase or merge left in progress.
func (s *Server) handleGitAbort(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if err := gitAbortOp(s.ix.Root()); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleGitLog returns recent commits from HEAD (default 5).
func (s *Server) handleGitLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 5
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	branch := gitCurrentBranch(s.ix.Root())
	commits := gitRecentCommits(s.ix.Root(), limit)
	commitsURL := gitCommitsWebURL(s.ix.Root(), branch)
	writeJSON(w, map[string]any{
		"commits":    commits,
		"commitsUrl": commitsURL,
	})
}

// handleUnpushed lists the commits that exist locally but not on the tracking
// branch (@{u}..HEAD), newest first, plus the name of that tracking branch so
// the sidebar can say what the list is measured against. available is false
// (200, empty list) when git is off, no upstream is configured, or nothing is
// ahead -- there is no guessed fallback branch, since measuring against the
// wrong ref is worse than showing nothing.
func (s *Server) handleUnpushed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}
	var upstream string
	var commits []UnpushedCommit
	if s.pr != nil {
		// Detached PR checkout: your commits are everything past the PR head.
		s.pr.mu.Lock()
		head, branch := s.pr.remoteHead(), s.pr.meta.HeadRef
		s.pr.mu.Unlock()
		commits = gitCommitsSince(s.ix.Root(), head, limit)
		upstream = fmt.Sprintf("PR #%d head (%s)", s.pr.meta.Number, branch)
	} else {
		upstream, commits = gitUnpushedCommits(s.ix.Root(), limit)
	}
	if commits == nil {
		commits = []UnpushedCommit{}
	}
	writeJSON(w, map[string]any{
		"available": upstream != "" && len(commits) > 0,
		"upstream":  upstream,
		"commits":   commits,
	})
}

// handleCommitFiles returns the paths one commit touched, badged with git's
// name-status letter. Unknown SHAs come back as an empty list, not an error:
// the only way to ask for one is from a list px0 handed out, and a commit
// that has since been rewritten is a stale click, not a failure worth a toast.
func (s *Server) handleCommitFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sha := r.URL.Query().Get("sha")
	files := gitCommitFiles(s.ix.Root(), sha)
	if files == nil {
		files = []CommitFile{}
	}
	writeJSON(w, map[string]any{"sha": sha, "files": files})
}

// handleCommitDetail returns one commit's author, full message and diffstat
// for the hover card.
func (s *Server) handleCommitDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sha := r.URL.Query().Get("sha")
	d, ok := gitCommitDetail(s.ix.Root(), sha)
	if !ok {
		fail(w, http.StatusNotFound, "unknown commit")
		return
	}
	writeJSON(w, d)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	q := r.URL.Query()
	opts := SearchOpts{
		Query: q.Get("q"),
		Regex: q.Get("re") == "1",
		Case:  q.Get("case") == "1",
		Word:  q.Get("word") == "1",
		Glob:  q.Get("glob"),
	}
	res, truncated, err := SearchContext(r.Context(), s.ix, opts)
	if err != nil {
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			return
		}
		fail(w, 400, err.Error())
		return
	}
	total := 0
	for _, f := range res {
		total += len(f.Matches)
	}
	if res == nil {
		res = []FileMatches{} // an empty result is [], never null
	}
	if ui.Verbose && opts.Query != "" {
		dur := ui.FmtDuration(time.Since(start))
		matchStr := "matches"
		if total == 1 {
			matchStr = "match"
		}
		fileStr := "files"
		if len(res) == 1 {
			fileStr = "file"
		}
		truncStr := ""
		if truncated {
			truncStr = " (truncated)"
		}
		ui.Status("info", "search", fmt.Sprintf("%q · %d %s in %d %s%s  (%s)", opts.Query, total, matchStr, len(res), fileStr, truncStr, dur), 0, os.Stdout)
	}
	writeJSON(w, map[string]any{"results": res, "files": len(res), "total": total, "truncated": truncated})
}

func (s *Server) handleOutline(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	syms, err := symbols.Outline(abs, rel)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	if syms == nil {
		syms = []symbols.Symbol{}
	}
	if ui.Verbose {
		ui.Status("info", "outline", fmt.Sprintf("%s · %d symbols  (%s)", rel, len(syms), ui.FmtDuration(time.Since(start))), 0, os.Stdout)
	}
	writeJSON(w, map[string]any{"path": rel, "symbols": syms})
}

// handleDef approximates go-to-definition: a whole-word search across the
// index, with lines that look like declarations floated to the top.
func (s *Server) handleDef(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sym := strings.TrimSpace(r.URL.Query().Get("sym"))
	if sym == "" {
		fail(w, 400, "no symbol")
		return
	}
	res, _, err := SearchContext(r.Context(), s.ix, SearchOpts{
		Query: sym, Word: true, Case: true,
		MaxFiles: 400, MaxPerFil: 20, classifyDefs: true,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			return
		}
		fail(w, 400, err.Error())
		return
	}
	type hit struct {
		Path string `json:"path"`
		Match
	}
	var defs []hit
	refs := 0
	seen := map[string]bool{}
	for _, f := range res {
		for _, m := range f.Matches {
			if !m.Def {
				refs++
				continue
			}
			// One entry per declaring line, however often the name repeats on it.
			k := f.Path + ":" + strconv.Itoa(m.Line)
			if seen[k] {
				continue
			}
			seen[k] = true
			defs = append(defs, hit{f.Path, m})
		}
	}
	// Prefer declarations in files whose name echoes the symbol.
	low := strings.ToLower(sym)
	sort.SliceStable(defs, func(i, j int) bool {
		a := strings.Contains(strings.ToLower(filepath.Base(defs[i].Path)), low)
		b := strings.Contains(strings.ToLower(filepath.Base(defs[j].Path)), low)
		return a && !b
	})
	state, server := s.lsp.State(r.URL.Query().Get("path"))
	if defs == nil {
		defs = []hit{}
	}
	if ui.Verbose {
		dur := ui.FmtDuration(time.Since(start))
		defStr := "definitions"
		if len(defs) == 1 {
			defStr = "definition"
		}
		ui.Status("info", "def", fmt.Sprintf("%q · %d %s, %d refs  (%s)", sym, len(defs), defStr, refs, dur), 0, os.Stdout)
	}
	writeJSON(w, map[string]any{
		"symbol": sym, "defs": defs, "refCount": refs,
		"lsp": map[string]any{"state": string(state), "server": server},
	})
}

func (s *Server) handleReindex(w http.ResponseWriter, r *http.Request) {
	render.EvictAll()
	s.ix.Build()
	if s.gitWatcher != nil {
		s.gitWatcher.Trigger()
	}
	if s.lsp != nil {
		s.lsp.RefreshOpenDocs()
	}
	n, _, ms := s.ix.Stats()
	gitCount, gitFiles := s.ix.GitChanges()
	writeJSON(w, map[string]any{"files": n, "indexMs": ms, "gitChanges": gitCount, "gitFiles": gitFiles})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{
			"settings": settings.ReadMergedMap(),
			"defaults": settings.DefaultMap(),
			"schema":   settings.Schema,
			"raw":      settings.ReadRawJSON(),
			"path":     settings.Path(),
		})
	case http.MethodPost:
		if !localPost(w, r) {
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
		if err != nil {
			fail(w, 400, "failed to read body")
			return
		}
		var payload map[string]any
		if len(body) > 0 {
			if err := json.Unmarshal(body, &payload); err != nil {
				fail(w, 400, "invalid JSON: "+err.Error())
				return
			}
		} else {
			payload = make(map[string]any)
			for k, vs := range r.URL.Query() {
				if len(vs) > 0 {
					payload[k] = vs[0]
				}
			}
		}

		if rawStr, ok := payload["raw"].(string); ok {
			if err := settings.SaveRawJSON([]byte(rawStr)); err != nil {
				fail(w, 400, "invalid JSON in settings: "+err.Error())
				return
			}
		} else {
			if err := settings.UpdateMap(payload); err != nil {
				fail(w, 500, err.Error())
				return
			}
		}

		if s.agent != nil {
			currentSettings := settings.Read()
			if currentSettings.Agent != "" && currentSettings.Agent != s.agent.Name() {
				_ = s.agent.Select(currentSettings.Agent, s.agent.Model())
			}
		}

		writeJSON(w, map[string]any{
			"settings": settings.ReadMergedMap(),
			"raw":      settings.ReadRawJSON(),
			"path":     settings.Path(),
			"ok":       true,
		})
	default:
		fail(w, 405, "method not allowed")
	}
}

// CloseThreads stops every thread turn still running at shutdown.
func (s *Server) CloseThreads() {
	if s.threads != nil {
		s.threads.Close()
	}
}

// ---------------------------------------------------------------- LSP setup & control HTTP handlers

// localPost admits a request that changes the machine only when it is a POST
// from px0's own page. Browsers send Origin on every POST, so a page from another
// site cannot pass. Requiring the Host to be an IP address or localhost also
// shuts out DNS rebinding, where an attacker's domain is pointed at this machine
// and its Origin would otherwise match.
func localPost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host != "localhost" && net.ParseIP(host) == nil {
		fail(w, http.StatusForbidden, "open px0 by IP address or localhost to set up language servers")
		return false
	}
	if o, err := url.Parse(r.Header.Get("Origin")); err != nil || o.Host != r.Host {
		fail(w, http.StatusForbidden, "request did not come from px0")
		return false
	}
	return true
}

func (s *Server) handleLSPSetup(w http.ResponseWriter, r *http.Request) {
	if s.lsp == nil {
		fail(w, 503, "language servers are disabled")
		return
	}
	_, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	writeJSON(w, s.lsp.Setup(rel))
}

func (s *Server) handleLSPInstall(w http.ResponseWriter, r *http.Request) {
	if s.lsp == nil {
		fail(w, 503, "language servers are disabled")
		return
	}
	if !localPost(w, r) {
		return
	}
	q := r.URL.Query()
	option, _ := strconv.Atoi(q.Get("option"))
	j, err := s.lsp.Install(q.Get("server"), option)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, j)
}

func (s *Server) relevantLSPServers() []map[string]any {
	if s.lsp == nil || !s.lsp.Enabled() {
		return []map[string]any{}
	}

	extMap := map[string]bool{}
	if s.ix != nil {
		for _, f := range s.ix.Files() {
			ext := strings.ToLower(filepath.Ext(f.Path))
			if ext != "" {
				extMap[ext] = true
			}
		}
	}

	seen := map[string]bool{}
	var result []map[string]any

	// 1. Add servers matching extensions present in the workspace
	for i := range lspRegistry {
		def := &lspRegistry[i]
		matchesExt := false
		for _, ext := range def.Exts {
			if extMap[ext] {
				matchesExt = true
				break
			}
		}
		if matchesExt && !seen[def.Name] {
			seen[def.Name] = true
			result = append(result, s.lsp.ServerStatus(def))
		}
	}

	// 2. Also include any server that is currently running or starting.
	// Snapshot the names under RLock first: ServerStatus takes m.mu.RLock
	// itself, and Go's RWMutex is not reentrant, so holding the lock across
	// the call would deadlock.
	s.lsp.mu.RLock()
	var extra []string
	for name := range s.lsp.clients {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	for name := range s.lsp.starting {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	s.lsp.mu.RUnlock()
	for _, name := range extra {
		for i := range lspRegistry {
			if lspRegistry[i].Name == name {
				seen[name] = true
				result = append(result, s.lsp.ServerStatus(&lspRegistry[i]))
				break
			}
		}
	}

	return result
}

// handleLSPServers returns the list of all language servers relevant to the workspace
// along with their runtime states.
func (s *Server) handleLSPServers(w http.ResponseWriter, r *http.Request) {
	if s.lsp == nil {
		writeJSON(w, map[string]any{
			"enabled":    false,
			"anyRunning": false,
			"servers":    []any{},
		})
		return
	}
	servers := s.relevantLSPServers()
	anyRunning := false
	for _, srv := range servers {
		if r, ok := srv["running"].(bool); ok && r {
			anyRunning = true
			break
		}
	}
	if !anyRunning && s.lsp.AnyRunning() {
		anyRunning = true
	}
	writeJSON(w, map[string]any{
		"enabled":    s.lsp.Enabled(),
		"anyRunning": anyRunning,
		"servers":    servers,
	})
}

// handleLSPStop stops a running language server or all servers.
func (s *Server) handleLSPStop(w http.ResponseWriter, r *http.Request) {
	if s.lsp == nil {
		fail(w, 503, "language servers are disabled")
		return
	}
	if !localPost(w, r) {
		return
	}
	server := r.URL.Query().Get("server")
	if server == "" {
		var body struct {
			Server string `json:"server"`
		}
		if json.NewDecoder(r.Body).Decode(&body) == nil && body.Server != "" {
			server = body.Server
		}
	}
	s.lsp.Stop(server)
	s.handleLSPServers(w, r)
}

// handleLSPStart finds servers installed since startup, clears earlier start
// failures and starts the server for path or server name, reporting status.
func (s *Server) handleLSPStart(w http.ResponseWriter, r *http.Request) {
	if s.lsp == nil {
		fail(w, 503, "language servers are disabled")
		return
	}
	if !localPost(w, r) {
		return
	}
	server := r.URL.Query().Get("server")
	if server == "" {
		var body struct {
			Server string `json:"server"`
			All    bool   `json:"all"`
		}
		if json.NewDecoder(r.Body).Decode(&body) == nil {
			if body.All {
				server = "all"
			} else {
				server = body.Server
			}
		}
	}

	if server != "" {
		if server == "all" {
			servers := s.relevantLSPServers()
			for _, srv := range servers {
				if inst, _ := srv["installed"].(bool); inst {
					if name, _ := srv["name"].(string); name != "" {
						_ = s.lsp.StartServer(name)
					}
				}
			}
		} else {
			if err := s.lsp.StartServer(server); err != nil {
				fail(w, 400, err.Error())
				return
			}
		}
		s.handleLSPServers(w, r)
		return
	}

	_, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path or server required")
		return
	}
	s.lsp.Rescan()
	ctx, cancel := context.WithTimeout(r.Context(), 1500*time.Millisecond)
	defer cancel()
	s.lsp.client(ctx, rel) // the spawn continues if this gives up waiting
	writeJSON(w, s.lspBrief(rel))
}

// lspBrief is the language server summary sent with a file: its state, and the
// language that lacks a server when none is installed, so the UI can offer one.
func (s *Server) lspBrief(rel string) map[string]any {
	state, srv := s.lsp.State(rel)
	b := map[string]any{
		"state":      string(state),
		"server":     srv,
		"anyRunning": s.lsp.AnyRunning(),
	}
	if lang := s.lsp.MissingLang(rel); lang != "" {
		b["missing"] = lang
	}
	return b
}

// ---------------------------------------------------------------- Workspace Session

// SessionTab represents an open editor tab pointing to a file path. Ref, when
// set, is the commit SHA the tab is pinned to -- a tab opened from the
// Unpushed sidebar section, showing that commit's own diff rather than the
// working tree's. A commit that no longer exists simply restores as an
// ordinary tab.
type SessionTab struct {
	Path string `json:"path"`
	Ref  string `json:"ref,omitempty"`
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

// sessionDir returns the directory holding session files: XDG_STATE_HOME/px0/sessions
// if available, falling back to ~/.px0/sessions or os.TempDir()/px0-sessions.
func sessionDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "px0", "sessions")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".px0", "sessions")
	}
	return filepath.Join(os.TempDir(), "px0-sessions")
}

// sessionFilePath returns the path to the JSON file where workspace session state is saved.
// The file name is derived from the base path or an 8-byte SHA-256 hash of the cleaned
// workspace root path.
func sessionFilePath(basePath, root string) string {
	key := ""
	if basePath != "" && basePath != "/" {
		clean := strings.Trim(basePath, "/")
		clean = strings.ReplaceAll(clean, "/", "_")
		key = clean
	} else {
		h := sha256.Sum256([]byte(filepath.Clean(root)))
		key = hex.EncodeToString(h[:8])
	}
	return filepath.Join(sessionDir(), key+".json")
}

// prSessionFilePath returns the session file for a pull request, keyed on the PR
// itself (the workbench item ID) rather than the base path or checkout root. Every
// opening checks the PR out into a new temporary directory, so a root-keyed file
// would never be found again.
func prSessionFilePath(pt PRTarget) string {
	return filepath.Join(sessionDir(), "pr-"+wbPRItem(pt).ID+".json")
}

// newPRSessionManager opens the session file for a pull request.
func newPRSessionManager(pt PRTarget) *sessionManager {
	sm := &sessionManager{
		path: prSessionFilePath(pt),
		data: WorkspaceSession{Tabs: []SessionTab{}, OpenDirs: []string{}},
	}
	sm.load()
	return sm
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
