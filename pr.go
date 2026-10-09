package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pr.go handles git forge pull/merge request reviews: checking out a PR's
// source tree into a throwaway git worktree, diffing it against the merge-base
// with its target branch instead of HEAD, and letting the reviewer leave draft
// comments and submit reviews or batch apply them with AI agents.
//
// Nothing persists past the process. The worktree lives in a system temp dir
// and is removed in prSession.Close.

// prSession is one checked-out PR review. Draft comments live only in
// memory (mu-guarded), same lifetime as an agentJob -- never written to
// disk, never surviving a restart.
type prSession struct {
	mu sync.Mutex

	provider        GitProvider
	target          PRTarget
	meta            PRMeta
	token           string
	writeAccess     bool
	diffBase        string // merge-base(head, base branch), or "HEAD" if the base couldn't be resolved
	diffBaseWarning string // set when diffBase fell back to "HEAD"; surfaced in the UI so an empty diff doesn't read as "no changes"

	worktree string // temp checkout, removed in Close
	srcRepo  string // the repo the worktree was registered against ("" for a plain clone)

	// pushedSHA is what the PR's branch holds on the remote after this session
	// pushed to it ("" until the first push: the head the PR was checked out
	// at). It bounds "unpushed", and nothing else. meta.HeadSHA deliberately
	// stays at the head the review started from, so a push moves your commits
	// out of "Unpushed" without folding them into "PR changes".
	pushedSHA string

	scopeDir string // temp dir holding the diff files handed to threads; removed in Close

	comments []prComment
	nextID   int64
}

// remoteHead returns the commit the PR's branch is at on the remote, as far as
// this session knows. Callers hold p.mu.
func (p *prSession) remoteHead() string {
	if p.pushedSHA != "" {
		return p.pushedSHA
	}
	return p.meta.HeadSHA
}

// ErrPRMergedCancelled is returned when opening an already-merged PR is cancelled.
var ErrPRMergedCancelled = errors.New("PR is already merged; opening cancelled")

// computeDiffBase fetches the PR's base branch into worktree and returns a
// merge-base with HEAD to diff against, so review diffs show exactly what
// the PR changes rather than the head's full HEAD diff. Best-effort: if it
// can't be resolved (e.g. the base branch was force-pushed away, or the
// fetch itself failed), diffBase falls back to "HEAD" -- which diffs the
// checkout against its own HEAD and looks empty -- with a warning explaining
// why, so callers can surface it and a blank diff never reads as "no
// changes". Shared by checkoutPR (initial checkout) and prSession.Pull
// (re-sync after new commits land on the PR).
func computeDiffBase(worktree, srcRepo, token string, target PRTarget, baseRef string, num int, onProgress func(string)) (diffBase, diffBaseWarning string) {
	if onProgress != nil {
		onProgress(fmt.Sprintf("Computing merge base with %s...", baseRef))
	}
	diffBase = "HEAD"
	var fetchErr string
	baseRefspec := fmt.Sprintf("refs/heads/%s:refs/px0/base/%d", baseRef, num)
	baseRemote := "origin"
	if srcRepo == "" {
		baseRemote = target.RepoCloneURL()
	}
	if out, err := gitAuthCmd(target, token, "-C", worktree, "fetch", "--no-tags", baseRemote, baseRefspec).CombinedOutput(); err != nil {
		fetchErr = strings.TrimSpace(string(out))
		if fetchErr == "" {
			fetchErr = err.Error()
		}
	} else if mb := gitMergeBase(worktree, "HEAD", fmt.Sprintf("refs/px0/base/%d", num)); mb != "" {
		diffBase = mb
	}
	if diffBase == "HEAD" && srcRepo != "" {
		if mb := gitMergeBase(worktree, "HEAD", "origin/"+baseRef); mb != "" {
			diffBase = mb
			fetchErr = "" // recovered via the local clone's own remote-tracking ref
		}
	}
	if diffBase == "HEAD" {
		diffBaseWarning = fmt.Sprintf("could not resolve a merge-base with %s; diff will show no changes", baseRef)
		if fetchErr != "" {
			diffBaseWarning = fmt.Sprintf("%s (%s)", diffBaseWarning, fetchErr)
		}
		fmt.Fprintln(os.Stderr, "px0: warning:", diffBaseWarning)
		if onProgress != nil {
			onProgress("Warning: " + diffBaseWarning)
		}
	}
	return diffBase, diffBaseWarning
}

// checkoutPR fetches a PR's head ref and checks it out into a system temp
// directory: a git worktree of cwd's origin when cwd is already a clone of
// the same repo (the common case -- opened inside the repo), or a shallow
// single-branch clone of the PR head otherwise (a bare URL opened from an
// unrelated directory).
func checkoutPR(ctx context.Context, provider GitProvider, target PRTarget, cwd string, onProgress func(string)) (*prSession, error) {
	cfg := readSettings()
	token, _ := provider.ResolveToken(cfg, target)

	if onProgress != nil {
		onProgress(fmt.Sprintf("Fetching PR #%d metadata from %s...", target.Number, provider.Name()))
	}
	meta, err := provider.FetchPR(ctx, target, token)
	if err != nil {
		return nil, err
	}

	tmp, err := os.MkdirTemp("", "px0-pr-*")
	if err != nil {
		return nil, err
	}
	// macOS TempDir lives under /var -> /private/var; git rev-parse
	// --show-toplevel reports the resolved path, so leaving tmp unresolved
	// makes gitStatusAgainst's toplevel-relative prefix check fail for every
	// file, silently emptying the PR's diff/status view.
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = resolved
	}
	cleanup := func() { os.RemoveAll(tmp) }

	srcRepo := ""
	if info := gitProbe(cwd); info.ok {
		if originURL, err := exec.Command("git", "-C", info.toplevel, "remote", "get-url", "origin").Output(); err == nil {
			orig := strings.ToLower(strings.TrimSpace(string(originURL)))
			if target.Owner != "" && target.Repo != "" &&
				strings.Contains(orig, strings.ToLower(target.Owner)) &&
				strings.Contains(orig, strings.ToLower(target.Repo)) {
				srcRepo = info.toplevel
			}
		}
	}

	num := target.Number
	if srcRepo != "" {
		if onProgress != nil {
			onProgress(fmt.Sprintf("Fetching PR #%d head and preparing worktree...", num))
		}
		headRefspec := fmt.Sprintf("refs/pull/%d/head:refs/px0/pr/%d", num, num)
		if out, err := exec.Command("git", "-C", srcRepo, "fetch", "--no-tags", "origin", headRefspec).CombinedOutput(); err != nil {
			cleanup()
			return nil, fmt.Errorf("git fetch PR head: %w: %s", err, strings.TrimSpace(string(out)))
		}
		if out, err := exec.Command("git", "-C", srcRepo, "worktree", "add", "--detach", tmp, fmt.Sprintf("refs/px0/pr/%d", num)).CombinedOutput(); err != nil {
			cleanup()
			return nil, fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
		}
	} else {
		if onProgress != nil {
			onProgress(fmt.Sprintf("Cloning PR #%d (%s)...", num, meta.HeadRef))
		}
		cloneURL := meta.HeadRepoCloneURL
		if cloneURL == "" {
			cloneURL = target.RepoCloneURL()
		}
		if out, err := gitAuthCmd(target, token, "clone", "--filter=blob:none", "--branch", meta.HeadRef, "--single-branch", cloneURL, tmp).CombinedOutput(); err != nil {
			cleanup()
			return nil, fmt.Errorf("git clone PR head: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	diffBase, diffBaseWarning := computeDiffBase(tmp, srcRepo, token, target, meta.BaseRef, num, onProgress)

	writeAccess := provider.CheckPushAccess(ctx, target, token)

	return &prSession{
		provider:        provider,
		target:          target,
		meta:            meta,
		token:           token,
		writeAccess:     writeAccess,
		diffBase:        diffBase,
		diffBaseWarning: diffBaseWarning,
		worktree:        tmp,
		srcRepo:         srcRepo,
	}, nil
}

func (p *prSession) Root() string { return p.worktree }

// Close removes the worktree registration (if any), cleans up temporary
// references, and deletes the temp checkout. Safe on a nil receiver.
func (p *prSession) Close() {
	if p == nil {
		return
	}
	if p.scopeDir != "" {
		os.RemoveAll(p.scopeDir)
	}
	if p.srcRepo != "" {
		exec.Command("git", "-C", p.srcRepo, "worktree", "remove", "--force", p.worktree).Run()
		exec.Command("git", "-C", p.srcRepo, "update-ref", "-d", fmt.Sprintf("refs/px0/pr/%d", p.meta.Number)).Run()
		exec.Command("git", "-C", p.srcRepo, "update-ref", "-d", fmt.Sprintf("refs/px0/base/%d", p.meta.Number)).Run()
	}
	os.RemoveAll(p.worktree)
}

// httpsRemoteURL rewrites an ssh-style remote (git@github.com:o/r.git,
// ssh://git@github.com/o/r.git) to its https form; anything else is returned as is.
func httpsRemoteURL(raw string) string {
	switch {
	case strings.HasPrefix(raw, "ssh://"):
		rest := strings.TrimPrefix(raw, "ssh://")
		if i := strings.IndexByte(rest, '@'); i >= 0 {
			rest = rest[i+1:]
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			host := rest[:i]
			if j := strings.IndexByte(host, ':'); j >= 0 { // ssh port means nothing to https
				host = host[:j]
			}
			return "https://" + host + rest[i:]
		}
	case strings.Contains(raw, "@") && !strings.Contains(raw, "://"):
		if at := strings.IndexByte(raw, '@'); at >= 0 {
			if host, path, ok := strings.Cut(raw[at+1:], ":"); ok {
				return "https://" + host + "/" + strings.TrimPrefix(path, "/")
			}
		}
	}
	return raw
}

// gitAuthCmd builds a git command that authenticates to target's forge over
// https with the forge token -- what a PAT-based URL does, minus the token in
// the URL (and so in argv, in git's remote config, and in error text). The
// header travels in the environment, is scoped to the forge's web root (e.g.
// https://github.com/), and terminal prompts are off so a bad or missing token
// fails fast instead of hanging on a password prompt. GitHub and Gitea both
// take the token as the Basic password. With no token it is plain git.
func gitAuthCmd(target PRTarget, token string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+target.WebBase()+"/.extraheader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basic,
		)
	}
	cmd.Env = env
	return cmd
}

func redactToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

// writeScopeFile puts content in a per-session temp dir (outside the worktree, so
// it never shows up as an untracked change) and returns its path, or "" if it
// could not be written.
func (p *prSession) writeScopeFile(name, content string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scopeDir == "" {
		dir, err := os.MkdirTemp("", "px0-prdiff-*")
		if err != nil {
			return ""
		}
		p.scopeDir = dir
	}
	path := filepath.Join(p.scopeDir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return ""
	}
	return path
}

// errPRDiverged is returned by Pull when the checkout can't be fast-forwarded
// onto the PR's current head -- local commits, or a force-pushed head, that
// don't share a straight-line history with what was fetched. Resolving that
// is not supported: Pull never invokes git's merge machinery, only a reset
// --hard onto an ancestor-verified fast-forward.
var errPRDiverged = errors.New("local checkout has diverged from the PR head; resolve manually")

// Pull re-fetches the PR's current head and, if it's a clean fast-forward,
// resets the worktree onto it and refreshes meta.HeadSHA/diffBase to match.
// Refuses outright when the worktree has uncommitted changes (nothing here
// stashes) or when the fast-forward check fails, in which case the caller
// should surface errPRDiverged as "not supported, resolve manually".
func (p *prSession) Pull() (info string, err error) {
	p.mu.Lock()
	worktree, srcRepo, num := p.worktree, p.srcRepo, p.meta.Number
	target, baseRef := p.target, p.meta.BaseRef
	headRepoCloneURL, headRef := p.meta.HeadRepoCloneURL, p.meta.HeadRef
	token := p.token
	p.mu.Unlock()

	if gitHasUncommittedChanges(worktree) {
		return "", errors.New("commit or discard your local changes before pulling")
	}

	// Fetch the PR branch itself, forced: it is the source of truth for the
	// PR's head (refs/pull/N/head can lag behind a fresh push), and a
	// force-pushed branch must not be refused as a non-fast-forward ref update.
	// A local clone with no token keeps using its own origin instead, which is
	// the remote that clone can already authenticate to.
	newRef := "FETCH_HEAD"
	if srcRepo != "" && token == "" {
		newRef = fmt.Sprintf("refs/px0/pr/%d", num)
		headRefspec := fmt.Sprintf("+refs/pull/%d/head:%s", num, newRef)
		if out, err := exec.Command("git", "-C", srcRepo, "fetch", "--no-tags", "origin", headRefspec).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git fetch PR head: %w: %s", err, strings.TrimSpace(string(out)))
		}
	} else {
		cloneURL := headRepoCloneURL
		if cloneURL == "" {
			cloneURL = target.RepoCloneURL()
		}
		if out, err := gitAuthCmd(target, token, "-C", worktree, "fetch", "--no-tags", cloneURL, "+refs/heads/"+headRef).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git fetch PR head: %w: %s", err, redactToken(strings.TrimSpace(string(out)), token))
		}
	}

	headOut, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	head := strings.TrimSpace(string(headOut))
	p.mu.Lock()
	knownHead := p.meta.HeadSHA
	p.mu.Unlock()
	nothingOfMine := head == knownHead // no local commits past the PR head we checked out

	info = "pulled the latest changes"
	upToDate := exec.Command("git", "-C", worktree, "merge-base", "--is-ancestor", newRef, "HEAD").Run() == nil
	switch {
	case upToDate:
		info = "already up to date"
	case exec.Command("git", "-C", worktree, "merge-base", "--is-ancestor", "HEAD", newRef).Run() == nil:
		// plain fast-forward
	case nothingOfMine:
		// The PR branch was rewritten (force-push or rebase) and there is
		// nothing of yours to lose: follow it.
		info = "pulled the latest changes (the PR branch was force-pushed)"
	default:
		return "", errPRDiverged
	}
	if !upToDate {
		if out, err := exec.Command("git", "-C", worktree, "reset", "--hard", newRef).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git reset: %w: %s", err, strings.TrimSpace(string(out)))
		}
		shaOut, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
		if err != nil {
			return "", fmt.Errorf("git rev-parse HEAD: %w", err)
		}
		p.mu.Lock()
		p.meta.HeadSHA = strings.TrimSpace(string(shaOut))
		p.pushedSHA = ""
		p.mu.Unlock()
	}

	// Title, state, merged, base branch: refresh what the forge says about the PR
	// itself, so the bar doesn't keep showing the state from session start.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if p.provider == nil {
		// nothing to ask
	} else if fresh, err := p.provider.FetchPR(ctx, target, token); err == nil {
		p.mu.Lock()
		p.meta.Title, p.meta.State, p.meta.Merged, p.meta.MergedAt = fresh.Title, fresh.State, fresh.Merged, fresh.MergedAt
		p.meta.Draft, p.meta.BaseRef = fresh.Draft, fresh.BaseRef
		baseRef = p.meta.BaseRef
		p.mu.Unlock()
	}
	diffBase, diffBaseWarning := computeDiffBase(worktree, srcRepo, token, target, baseRef, num, nil)
	p.mu.Lock()
	p.diffBase = diffBase
	p.diffBaseWarning = diffBaseWarning
	p.mu.Unlock()

	return info, nil
}

// Push pushes the worktree's current commit to the PR's actual head branch
// on its head repo (which may be a fork). Never force: a rejection means the
// head moved since this checkout or since the last Pull, and the caller
// should Pull before trying again.
func (p *prSession) Push() error {
	p.mu.Lock()
	worktree := p.worktree
	cloneURL, headRef := p.meta.HeadRepoCloneURL, p.meta.HeadRef
	target, token := p.target, p.token
	p.mu.Unlock()

	if cloneURL == "" {
		cloneURL = target.RepoCloneURL()
	}
	// However this checkout was cloned (https or ssh), the push goes over https
	// with the forge token, so it never stops to ask for a password.
	pushURL := cloneURL
	if token != "" {
		pushURL = httpsRemoteURL(cloneURL)
	}
	refspec := fmt.Sprintf("HEAD:refs/heads/%s", headRef)
	out, err := gitAuthCmd(target, token, "-C", worktree, "push", pushURL, refspec).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push: %w: %s", err, redactToken(strings.TrimSpace(string(out)), token))
	}
	// What you pushed is now part of the PR: the PR head moves up to it, so
	// those commits leave "Your changes" and join "PR changes".
	if sha, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output(); err == nil {
		p.mu.Lock()
		p.meta.HeadSHA = strings.TrimSpace(string(sha))
		p.pushedSHA = ""
		p.mu.Unlock()
	}
	return nil
}

// ---------------------------------------------------------------- HTTP

// forgeMeta describes the session's forge to the UI: which provider (whose
// token setting is "<provider>.token"), its display name, how to supply a
// token, and the PR's web page.
func (p *prSession) forgeMeta() map[string]any {
	m := map[string]any{"provider": "github", "providerLabel": "GitHub", "tokenHint": "", "webUrl": ""}
	if p.provider != nil {
		m["provider"], m["providerLabel"], m["tokenHint"] = p.provider.Name(), p.provider.Label(), p.provider.TokenHint()
	}
	if p.target.Owner != "" {
		m["webUrl"] = p.target.WebURL()
	}
	return m
}

// tokenRequired is the error text for a write attempted without a token.
func (p *prSession) tokenRequired(what string) string {
	if p.provider == nil {
		return "no auth token configured; " + what + " requires a forge token"
	}
	return fmt.Sprintf("no auth token configured; %s requires a %s token (%s)", what, p.provider.Label(), p.provider.TokenHint())
}

func (s *Server) prOrFail(w http.ResponseWriter) bool {
	if s.pr == nil {
		fail(w, http.StatusNotFound, "not a PR review session")
		return false
	}
	return true
}

func (s *Server) handlePRMeta(w http.ResponseWriter, r *http.Request) {
	if !s.prOrFail(w) {
		return
	}
	p := s.pr
	p.mu.Lock()
	defer p.mu.Unlock()
	m := map[string]any{
		"number":          p.meta.Number,
		"title":           p.meta.Title,
		"author":          p.meta.Author,
		"base":            p.meta.BaseRef,
		"head":            p.meta.HeadRef,
		"state":           p.meta.State,
		"merged":          p.meta.Merged,
		"mergedAt":        p.meta.MergedAt,
		"writeAccess":     p.writeAccess,
		"readOnly":        p.token == "",
		"draftCount":      len(p.comments),
		"diffBaseWarning": p.diffBaseWarning,
		"headSHA":         p.meta.HeadSHA,
		"url":             p.target.URL,
		"files":           s.ix.PRFiles(),
	}
	for k, v := range p.forgeMeta() {
		m[k] = v
	}
	writeJSON(w, m)
}

// handlePRExistingComments fetches every comment already posted on the PR
// (top-level and inline) directly from the forge -- always live, never
// cached, since another reviewer may have commented since the page loaded.
func (s *Server) handlePRExistingComments(w http.ResponseWriter, r *http.Request) {
	if !s.prOrFail(w) {
		return
	}
	p := s.pr
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	issue, review, err := p.provider.FetchComments(ctx, p.target, p.token)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if issue == nil {
		issue = []PRComment{}
	}
	if review == nil {
		review = []PRComment{}
	}
	writeJSON(w, map[string]any{"issueComments": issue, "reviewComments": review})
}

// handlePRIssueCommentPost posts a new top-level PR comment immediately (not
// part of the draft-then-submit review flow below, since a forge's issue
// comments aren't tied to a review). Used both for starting a new top-level
// comment and for "replying" to one, since forges don't thread these.
func (s *Server) handlePRIssueCommentPost(w http.ResponseWriter, r *http.Request) {
	if !s.prOrFail(w) {
		return
	}
	if !localPost(w, r) {
		return
	}
	p := s.pr
	if p.token == "" {
		fail(w, http.StatusForbidden, p.tokenRequired("posting comments"))
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || strings.TrimSpace(body.Body) == "" {
		fail(w, http.StatusBadRequest, "body is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	c, err := p.provider.PostIssueComment(ctx, p.target, p.token, strings.TrimSpace(body.Body))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, c)
}

// handlePRReviewCommentReply posts an immediate, threaded reply to an
// existing inline review comment (not a new draft: this bypasses the
// draft-then-submit review flow, matching GitHub's own dedicated reply
// endpoint, which posts right away; Gitea has none, see giteaReplyToReviewComment).
func (s *Server) handlePRReviewCommentReply(w http.ResponseWriter, r *http.Request) {
	if !s.prOrFail(w) {
		return
	}
	if !localPost(w, r) {
		return
	}
	p := s.pr
	if p.token == "" {
		fail(w, http.StatusForbidden, p.tokenRequired("posting comments"))
		return
	}
	var body struct {
		CommentID int64  `json:"commentId"`
		Body      string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || body.CommentID <= 0 || strings.TrimSpace(body.Body) == "" {
		fail(w, http.StatusBadRequest, "commentId and body are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	c, err := p.provider.ReplyToReviewComment(ctx, p.target, p.token, body.CommentID, strings.TrimSpace(body.Body))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, c)
}

func (s *Server) handlePRComments(w http.ResponseWriter, r *http.Request) {
	if !s.prOrFail(w) {
		return
	}
	p := s.pr
	switch r.Method {
	case http.MethodGet:
		p.mu.Lock()
		defer p.mu.Unlock()
		comments := p.comments
		if comments == nil {
			comments = []prComment{}
		}
		writeJSON(w, map[string]any{"comments": comments})
	case http.MethodPost:
		if !localPost(w, r) {
			return
		}
		var body struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Side string `json:"side"`
			Body string `json:"body"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil ||
			body.Path == "" || body.Line <= 0 || strings.TrimSpace(body.Body) == "" {
			fail(w, http.StatusBadRequest, "path, line, and body are required")
			return
		}
		side := strings.ToUpper(body.Side)
		if side != "LEFT" {
			side = "RIGHT"
		}
		p.mu.Lock()
		p.nextID++
		c := prComment{ID: p.nextID, Path: body.Path, Line: body.Line, Side: side, Body: strings.TrimSpace(body.Body)}
		p.comments = append(p.comments, c)
		if s.session != nil {
			s.session.Update(func(ws *WorkspaceSession) { ws.Drafts = p.comments })
		}
		p.mu.Unlock()
		writeJSON(w, c)
	default:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handlePRCommentDelete(w http.ResponseWriter, r *http.Request) {
	if !s.prOrFail(w) {
		return
	}
	if !localPost(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	p := s.pr
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, c := range p.comments {
		if c.ID == id {
			p.comments = append(p.comments[:i], p.comments[i+1:]...)
			break
		}
	}
	if s.session != nil {
		s.session.Update(func(ws *WorkspaceSession) { ws.Drafts = p.comments })
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handlePRSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.prOrFail(w) {
		return
	}
	if !localPost(w, r) {
		return
	}
	p := s.pr
	if p.token == "" {
		fail(w, http.StatusForbidden, p.tokenRequired("submitting a review"))
		return
	}
	var body struct {
		Event string `json:"event"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	event := strings.ToUpper(body.Event)
	if event != "APPROVE" && event != "REQUEST_CHANGES" && event != "COMMENT" {
		fail(w, http.StatusBadRequest, "event must be APPROVE, REQUEST_CHANGES, or COMMENT")
		return
	}
	if (event == "APPROVE" || event == "REQUEST_CHANGES") && !p.writeAccess {
		fail(w, http.StatusForbidden, "no push access on this repository; only Comment reviews are allowed")
		return
	}
	p.mu.Lock()
	comments := append([]prComment(nil), p.comments...)
	p.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := p.provider.SubmitReview(ctx, p.target, p.token, p.meta.HeadSHA, comments, event, body.Body); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	p.mu.Lock()
	p.comments = nil
	if s.session != nil {
		s.session.Update(func(ws *WorkspaceSession) { ws.Drafts = nil })
	}
	p.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

// handleLaunchPR lets an already-running px0 open another PR without
// disturbing its own session: it re-execs itself as a brand new process on
// a new port with "px0 -y <url>". The call returns as soon as the child starts.
func (s *Server) handleLaunchPR(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var body struct {
		Target string `json:"target"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || strings.TrimSpace(body.Target) == "" {
		fail(w, http.StatusBadRequest, "target is required")
		return
	}
	targetURL := strings.TrimSpace(body.Target)
	if _, _, ok := DetectPRURL(targetURL); !ok {
		fail(w, http.StatusBadRequest, "target must be a valid pull request URL (e.g. https://github.com/owner/repo/pull/123 or https://gitea.example.com/owner/repo/pulls/123)")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	cmd := exec.Command(exe, "-y", targetURL)
	cmd.Dir = s.ix.Root()
	if err := cmd.Start(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	go cmd.Wait() // reap without blocking the handler
	writeJSON(w, map[string]any{"ok": true})
}
