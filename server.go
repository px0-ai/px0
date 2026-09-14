package main

import (
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed web
var embedded embed.FS

// assets is the embedded web/ directory, or the one on disk under -dev.
var assets fs.FS = embedded

// useDiskAssets serves web/ from the filesystem so the UI can be edited without
// rebuilding. Development convenience only.
func useDiskAssets(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "web", "index.html")); err != nil {
		return err
	}
	assets = os.DirFS(dir)
	return nil
}

type Server struct {
	ix  *Index
	lsp *lspManager
	mux *http.ServeMux

	lastReq atomic.Int64 // unix nanos of the most recent request
}

func NewServer(ix *Index, lsp *lspManager) *Server {
	if lsp == nil {
		lsp = newLSPManager(ix.Root(), false)
	}
	s := &Server{ix: ix, lsp: lsp, mux: http.NewServeMux()}
	sub, _ := fs.Sub(assets, "web")
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	s.mux.HandleFunc("/static/themes.css", s.handleThemes)
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/meta", s.handleMeta)
	s.mux.HandleFunc("/api/metrics", s.handleMetrics)
	s.mux.HandleFunc("/api/tree", s.handleTree)
	s.mux.HandleFunc("/api/find", s.handleFind)
	s.mux.HandleFunc("/api/file", s.handleFile)
	s.mux.HandleFunc("/api/close", s.handleClose)
	s.mux.HandleFunc("/api/raw", s.handleRaw)
	s.mux.HandleFunc("/api/markdown", s.handleMarkdown)
	s.mux.HandleFunc("/api/diff", s.handleDiff)
	s.mux.HandleFunc("/api/review", s.handleReview)
	s.mux.HandleFunc("/api/gutter", s.handleGutter)
	s.mux.HandleFunc("/api/search", s.handleSearch)
	s.mux.HandleFunc("/api/outline", s.handleOutline)
	s.mux.HandleFunc("/api/def", s.handleDef)
	s.mux.HandleFunc("/api/reindex", s.handleReindex)
	s.mux.HandleFunc("/api/lsp/def", s.handleLSPDef)
	s.mux.HandleFunc("/api/lsp/refs", s.handleLSPRefs)
	s.mux.HandleFunc("/api/lsp/calls", s.handleLSPCalls)
	s.mux.HandleFunc("/api/lsp/symbols", s.handleLSPSymbols)
	s.mux.HandleFunc("/api/lsp/hover", s.handleLSPHover)
	s.mux.HandleFunc("/api/lsp/warm", s.handleLSPWarm)
	s.mux.HandleFunc("/api/lsp/setup", s.handleLSPSetup)
	s.mux.HandleFunc("/api/lsp/install", s.handleLSPInstall)
	s.mux.HandleFunc("/api/lsp/start", s.handleLSPStart)
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
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	done := true // nothing to release before the first request
	for range tick.C {
		idle := time.Since(time.Unix(0, s.lastReq.Load()))
		if idle < idleFor {
			done = false
			continue
		}
		if done {
			continue // already released since the last burst of work
		}
		debug.FreeOSMemory()
		done = true
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.lastReq.Store(time.Now().UnixNano())
	w.Header().Set("Cache-Control", "no-store")
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		s.mux.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(w)
	defer func() { gz.Close(); gzipPool.Put(gz) }()
	s.mux.ServeHTTP(gzipWriter{ResponseWriter: w, w: gz}, r)
}

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return w
}}

type gzipWriter struct {
	http.ResponseWriter
	w *gzip.Writer
}

func (g gzipWriter) Write(b []byte) (int, error) { return g.w.Write(b) }

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

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(assets, "web/index.html")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

// handleThemes joins web/themes/*.css into one stylesheet in file name order, so
// adding a theme means adding a file: there is no list to keep in sync.
func (s *Server) handleThemes(w http.ResponseWriter, r *http.Request) {
	names, err := fs.Glob(assets, "web/themes/*.css")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	var css strings.Builder
	for _, name := range names {
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		css.WriteString("/* " + strings.TrimPrefix(name, "web/") + " */\n")
		css.Write(b)
		css.WriteString("\n")
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	io.WriteString(w, css.String())
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	n, at, ms := s.ix.Stats()
	writeJSON(w, map[string]any{
		"root":       s.ix.Root(),
		"name":       filepath.Base(s.ix.Root()),
		"files":      n,
		"indexMs":    ms,
		"builtAt":    at,
		"ready":      s.ix.Ready(),
		"git":        gitAvailable(s.ix.Root()),
		"lspServers": s.lsp.Available(),
		"metrics":    getProcessMetrics(),
		"version":    version,
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, getProcessMetrics())
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
	abs, rel, line, col, ok := s.lspPos(r)
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ctx, cancel := lspCtx(r)
	defer cancel()
	hits, err := s.lsp.Definition(ctx, abs, rel, line, col)
	s.lspRespond(w, rel, hits, err)
}

func (s *Server) handleLSPRefs(w http.ResponseWriter, r *http.Request) {
	abs, rel, line, col, ok := s.lspPos(r)
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	ctx, cancel := lspCtx(r)
	defer cancel()
	hits, err := s.lsp.References(ctx, abs, rel, line, col)
	s.lspRespond(w, rel, hits, err)
}

// handleLSPCalls serves call trails. Without item it resolves the function at
// path/line/col into trail roots; with item (a node's opaque item, echoed back)
// it expands that node into callers, or callees when dir=out. path always names
// the file the trail started in, which picks the language server.
func (s *Server) handleLSPCalls(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, resp)
}

// handleLSPWarm starts the server for this file type if it is not running and
// reports where it has got to. Opening a file calls this so the server is awake
// by the time the reader wants to hover or jump, and so the status indicator
// reflects reality without anyone having to ask a question first.
func (s *Server) handleLSPWarm(w http.ResponseWriter, r *http.Request) {
	_, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
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
	s.lsp.client(ctx, rel)
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
			"symbols": []Symbol{}, "state": string(state), "server": server, "error": err.Error(),
		})
		return
	}
	if syms == nil {
		syms = []Symbol{}
	}
	writeJSON(w, map[string]any{"symbols": syms, "state": string(state), "server": server})
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
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	res := FuzzyFind(s.ix.Files(), q, limit)
	if res == nil {
		res = []FuzzyResult{}
	}
	writeJSON(w, map[string]any{"results": res})
}

var imageExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".svg": true, ".ico": true, ".bmp": true, ".avif": true,
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
		fail(w, 404, err.Error())
		return
	}
	if imageExt[strings.ToLower(filepath.Ext(rel))] {
		writeJSON(w, map[string]any{"path": rel, "image": true, "size": st.Size()})
		return
	}

	d, err := Open(abs, rel)
	if err != nil {
		fail(w, 415, err.Error())
		return
	}
	start, _ := strconv.Atoi(q.Get("start"))
	count, _ := strconv.Atoi(q.Get("count"))
	if count <= 0 {
		count = hlChunk
	}
	if start < 0 {
		start = 0
	}
	if start > d.Total {
		start = d.Total
	}
	lines, exact := d.Lines(start, start+count)
	_, coming := d.Exact()
	writeJSON(w, map[string]any{
		"path": rel, "lang": d.Lang, "total": d.Total, "maxCols": d.MaxCols,
		"start": start, "lines": lines, "size": st.Size(),
		"exact": exact, "refine": !exact && coming,
		"markdown": isMarkdown(rel),
		"lsp":      s.lspBrief(rel),
	})
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	abs, rel, ok := s.resolvePath(q.Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	Evict(abs)
	s.lsp.CloseDoc(abs, rel)
	debug.FreeOSMemory()
	writeJSON(w, map[string]any{"ok": true, "path": rel})
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.safePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(rel)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	http.ServeFile(w, r, abs)
}

// handleDiff returns a file's diff against HEAD as tokenised rows: every row
// carries the highlighted HTML of its line and the intra-line ranges that
// changed, so the client lays out what it is given instead of re-deriving it.
// available is false (200, no hunks) when git is off/absent or the file is
// unchanged.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	root := s.ix.Root()
	newSrc := ""
	if d, err := Open(abs, rel); err == nil {
		newSrc = d.src // shares the highlighter's cache: the file is usually already read
	}
	oldSrc, inHead := gitShowHead(root, rel)

	var hunks []DiffHunk
	switch {
	case !gitAvailable(root) || newSrc == "":
		// no git, or the file is unreadable: nothing to render
	case inHead:
		if diff := gitDiff(root, rel); diff != "" {
			hunks = diffRows(rel, oldSrc, newSrc, diff)
		}
	default:
		// Absent from HEAD: an untracked file, a tracked addition, or the new name
		// of a rename -- indistinguishable from the path alone. git diff HEAD is
		// NOT silent on a rename, it reports the whole file as added, so asking
		// has to come first or /api/diff contradicts the rename /api/review found.
		if src := gitRenameSource(root, rel); src != "" {
			oldSrc, _ = gitShowHead(root, src)
			hunks = diffRows(rel, oldSrc, newSrc, gitDiffRenamed(root, src, rel))
		} else if diff := gitDiff(root, rel); diff != "" {
			hunks = diffRows(rel, oldSrc, newSrc, diff)
		} else {
			hunks = addedFileHunks(rel, newSrc) // untracked: git diff HEAD is silent here
		}
	}
	if hunks == nil {
		hunks = []DiffHunk{}
	}
	writeJSON(w, map[string]any{"path": rel, "available": len(hunks) > 0, "hunks": hunks})
}

// ChangedSymbol is an outline entry the changeset touched, plus how many of its
// lines changed.
type ChangedSymbol struct {
	Symbol
	Changed int `json:"changed"`
}

type reviewFile struct {
	ChangedFile
	Symbols []ChangedSymbol `json:"symbols"`
}

// handleReview returns the whole changeset against HEAD: one entry per changed
// file with its status, line counts and the symbols the change lands in.
//
// The symbols are crossed with the outline here rather than in the browser
// because the alternative is one request per file, and a dense changeset is
// dozens of files. A file with no outline (no rule for its language, or deleted
// from disk) comes back with an empty symbol list and the client falls back to
// files and hunks -- review must not need a language server to exist.
func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	root := s.ix.Root()
	files := gitChangeset(root)
	changed := gitChangedLines(root)
	out := make([]reviewFile, 0, len(files))
	for _, f := range files {
		rf := reviewFile{ChangedFile: f, Symbols: []ChangedSymbol{}}
		lines := changed[f.Path]
		if f.Status == "U" {
			lines = lineRange(f.Added) // untracked: the whole file is the change
		}
		if abs, rel, ok := s.safePath(f.Path); ok {
			rf.Symbols = changedSymbols(abs, rel, lines)
		}
		out = append(out, rf)
	}
	writeJSON(w, map[string]any{"available": gitAvailable(root), "files": out})
}

func lineRange(n int) []int {
	lines := make([]int, n)
	for i := range lines {
		lines[i] = i + 1
	}
	return lines
}

// changedSymbols attributes changed line numbers to the outline symbols that
// contain them.
//
// The outline carries no end line, so a line belongs to the last symbol
// declared at or before it. That is exact for the declaration-per-line shape
// the outline recognises and approximate elsewhere -- the outline never claimed
// to be a parser, and a rail that is right often enough to navigate by is the
// whole point.
func changedSymbols(abs, rel string, lines []int) []ChangedSymbol {
	out := []ChangedSymbol{}
	if len(lines) == 0 {
		return out
	}
	syms, err := Outline(abs, rel)
	if err != nil || len(syms) == 0 {
		return out
	}
	counts := make([]int, len(syms))
	for _, ln := range lines {
		// Outline emits symbols in line order, so this is a plain bisect.
		if i := sort.Search(len(syms), func(k int) bool { return syms[k].Line > ln }) - 1; i >= 0 {
			counts[i]++
		}
	}
	for i, n := range counts {
		if n > 0 {
			out = append(out, ChangedSymbol{Symbol: syms[i], Changed: n})
		}
	}
	return out
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
	added, modified, deleted := gitHunks(s.ix.Root(), rel)
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

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := SearchOpts{
		Query: q.Get("q"),
		Regex: q.Get("re") == "1",
		Case:  q.Get("case") == "1",
		Word:  q.Get("word") == "1",
		Glob:  q.Get("glob"),
	}
	res, truncated, err := Search(s.ix, opts)
	if err != nil {
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
	writeJSON(w, map[string]any{"results": res, "files": len(res), "total": total, "truncated": truncated})
}

func (s *Server) handleOutline(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	syms, err := Outline(abs, rel)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	if syms == nil {
		syms = []Symbol{}
	}
	writeJSON(w, map[string]any{"path": rel, "symbols": syms})
}

// handleDef approximates go-to-definition: a whole-word search across the
// index, with lines that look like declarations floated to the top.
func (s *Server) handleDef(w http.ResponseWriter, r *http.Request) {
	sym := strings.TrimSpace(r.URL.Query().Get("sym"))
	if sym == "" {
		fail(w, 400, "no symbol")
		return
	}
	res, _, err := Search(s.ix, SearchOpts{
		Query: sym, Word: true, Case: true,
		MaxFiles: 400, MaxPerFil: 20, classifyDefs: true,
	})
	if err != nil {
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
	writeJSON(w, map[string]any{
		"symbol": sym, "defs": defs, "refCount": refs,
		"lsp": map[string]any{"state": string(state), "server": server},
	})
}

func (s *Server) handleReindex(w http.ResponseWriter, r *http.Request) {
	s.ix.Build()
	n, _, ms := s.ix.Stats()
	writeJSON(w, map[string]any{"files": n, "indexMs": ms})
}
