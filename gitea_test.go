package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDetectGiteaPRURL(t *testing.T) {
	cases := []struct {
		arg               string
		wantOK            bool
		base, owner, repo string
		num               int
	}{
		{arg: "https://gitea.example.com/acme/widgets/pulls/42", wantOK: true, base: "https://gitea.example.com", owner: "acme", repo: "widgets", num: 42},
		{arg: "http://10.0.0.5:3000/acme/widgets/pulls/7", wantOK: true, base: "http://10.0.0.5:3000", owner: "acme", repo: "widgets", num: 7},
		{arg: "https://example.com/git/acme/widgets/pulls/3/files", wantOK: true, base: "https://example.com/git", owner: "acme", repo: "widgets", num: 3},
		{arg: "https://codeberg.org/acme/widgets.git/pulls/9#issuecomment-1", wantOK: true, base: "https://codeberg.org", owner: "acme", repo: "widgets", num: 9},
		// A scheme is required, so relative local paths are never PRs.
		{arg: "gitea.example.com/acme/widgets/pulls/42", wantOK: false},
		{arg: "docs/acme/widgets/pulls/1", wantOK: false},
		// Not a single PR.
		{arg: "https://gitea.example.com/acme/widgets/pulls", wantOK: false},
		{arg: "https://gitea.example.com/acme/widgets/pulls/42abc", wantOK: false},
		{arg: "https://gitea.example.com/acme/widgets/issues/42", wantOK: false},
		{arg: "https://github.com/acme/widgets/pulls/42", wantOK: false},
	}
	for _, c := range cases {
		p, target, ok := DetectPRURL(c.arg)
		if ok != c.wantOK {
			t.Errorf("DetectPRURL(%q) ok = %v, want %v", c.arg, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if p.Name() != "gitea" {
			t.Errorf("DetectPRURL(%q) provider = %q, want gitea", c.arg, p.Name())
		}
		if target.BaseURL != c.base || target.Owner != c.owner || target.Repo != c.repo || target.Number != c.num {
			t.Errorf("DetectPRURL(%q) = (%q, %q, %q, %d), want (%q, %q, %q, %d)", c.arg,
				target.BaseURL, target.Owner, target.Repo, target.Number, c.base, c.owner, c.repo, c.num)
		}
	}

	// GitHub URLs still go to GitHub.
	if p, _, ok := DetectPRURL("https://github.com/px0-ai/px0/pull/42"); !ok || p.Name() != "github" {
		t.Errorf("GitHub PR URL no longer detected as github")
	}
}

func TestPRTargetURLs(t *testing.T) {
	gt := PRTarget{Provider: "gitea", BaseURL: "https://example.com/git", Owner: "o", Repo: "r", Number: 5}
	if got := gt.RepoCloneURL(); got != "https://example.com/git/o/r.git" {
		t.Errorf("gitea RepoCloneURL = %q", got)
	}
	if got := gt.WebURL(); got != "https://example.com/git/o/r/pulls/5" {
		t.Errorf("gitea WebURL = %q", got)
	}
	// A target with no BaseURL (older code paths, tests) is github.com.
	gh := PRTarget{Owner: "o", Repo: "r", Number: 5}
	if got := gh.RepoCloneURL(); got != "https://github.com/o/r.git" {
		t.Errorf("github RepoCloneURL = %q", got)
	}
	if got := gh.WebURL(); got != "https://github.com/o/r/pull/5" {
		t.Errorf("github WebURL = %q", got)
	}
}

func TestGitAuthCmdScopesHeaderToForge(t *testing.T) {
	envOf := func(target PRTarget) string {
		return strings.Join(gitAuthCmd(target, "tok", "status").Env, "\n")
	}
	if env := envOf(PRTarget{BaseURL: "https://gitea.example.com"}); !strings.Contains(env, "GIT_CONFIG_KEY_0=http.https://gitea.example.com/.extraheader") {
		t.Errorf("gitea target: auth header not scoped to the Gitea host:\n%s", env)
	}
	if env := envOf(PRTarget{}); !strings.Contains(env, "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader") {
		t.Errorf("default target: auth header not scoped to github.com:\n%s", env)
	}
}

// fakeTea puts a `tea` on PATH that answers `tea login helper get` with
// token for host (and fails for any other host), standing in for the CLI.
func fakeTea(t *testing.T, host, token string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake tea needs a POSIX shell")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"[ \"$1 $2 $3\" = \"login helper get\" ] || exit 2\n" +
		"while IFS= read -r line && [ -n \"$line\" ]; do echo \"$line\"; case \"$line\" in host=*) h=${line#host=};; esac; done\n" +
		"[ \"$h\" = \"" + host + "\" ] || { echo \"no login found for host '$h'\" >&2; exit 1; }\n" +
		"echo username=someone\necho password=" + token + "\n"
	if err := os.WriteFile(filepath.Join(dir, "tea"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestResolveGiteaToken(t *testing.T) {
	fakeTea(t, "gitea.example.com:3000", "tea-token")

	t.Setenv("GITEA_TOKEN", "env-token")
	settingsToken := "settings-token"
	if tok, src := resolveGiteaToken(settings{GiteaToken: &settingsToken}, "https://gitea.example.com:3000"); tok != "settings-token" || src != "settings" {
		t.Errorf("settings: got (%q, %q)", tok, src)
	}
	blank := "  "
	if tok, src := resolveGiteaToken(settings{GiteaToken: &blank}, "https://gitea.example.com:3000"); tok != "env-token" || src != "env" {
		t.Errorf("blank settings must fall through to env: got (%q, %q)", tok, src)
	}

	t.Setenv("GITEA_TOKEN", "")
	if tok, src := resolveGiteaToken(settings{}, "https://gitea.example.com:3000/sub"); tok != "tea-token" || src != "tea" {
		t.Errorf("tea: got (%q, %q)", tok, src)
	}
	// tea has no login for this host: read-only.
	if tok, src := resolveGiteaToken(settings{}, "https://other.example.com"); tok != "" || src != "" {
		t.Errorf("unknown host: got (%q, %q), want none", tok, src)
	}
}

func TestResolveGiteaTokenNoTea(t *testing.T) {
	t.Setenv("GITEA_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	if tok, src := resolveGiteaToken(settings{}, "https://gitea.example.com"); tok != "" || src != "" {
		t.Errorf("got (%q, %q), want none", tok, src)
	}
}

// giteaStub routes stubbed Gitea API calls by "METHOD path" (path without
// the /api/v1 prefix), records each request, and 404s anything unrouted.
type giteaStub struct {
	t      *testing.T
	routes map[string]string
	status map[string]int
	calls  []string
	bodies map[string][]byte
	auth   []string
}

func newGiteaStub(t *testing.T) *giteaStub {
	s := &giteaStub{t: t, routes: map[string]string{}, status: map[string]int{}, bodies: map[string][]byte{}}
	orig := giteaHTTPClient.Transport
	t.Cleanup(func() { giteaHTTPClient.Transport = orig })
	giteaHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "gitea.example.com" || !strings.HasPrefix(req.URL.Path, "/git/api/v1/") {
			t.Errorf("request outside the instance's API: %s", req.URL)
		}
		key := req.Method + " " + strings.TrimPrefix(req.URL.Path, "/git/api/v1")
		s.calls = append(s.calls, key)
		s.auth = append(s.auth, req.Header.Get("Authorization"))
		if req.Body != nil {
			s.bodies[key], _ = io.ReadAll(req.Body)
		}
		body, ok := s.routes[key]
		code := http.StatusOK
		if c, ok := s.status[key]; ok {
			code = c
		}
		if !ok {
			code, body = http.StatusNotFound, `{"message":"not found"}`
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	return s
}

var giteaTestTarget = PRTarget{Provider: "gitea", BaseURL: "https://gitea.example.com/git", Owner: "acme", Repo: "widgets", Number: 5}

func TestGiteaFetchPR(t *testing.T) {
	s := newGiteaStub(t)
	s.routes["GET /repos/acme/widgets/pulls/5"] = `{
		"number": 5, "title": "Add sprockets", "state": "open", "merged": false, "merged_at": null, "draft": true,
		"user": {"login": "alice"},
		"base": {"ref": "main"},
		"head": {"ref": "sprockets", "sha": "abc123", "repo": {"clone_url": "https://gitea.example.com/git/alice/widgets.git", "full_name": "alice/widgets"}}
	}`
	s.routes["GET /repos/acme/widgets"] = `{"permissions": {"admin": false, "push": true, "pull": true}}`

	g := &GiteaProvider{}
	meta, err := g.FetchPR(context.Background(), giteaTestTarget, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "Add sprockets" || meta.Author != "alice" || meta.BaseRef != "main" || meta.HeadRef != "sprockets" ||
		meta.HeadSHA != "abc123" || !meta.Draft || meta.Merged || !meta.HeadIsFork {
		t.Errorf("unexpected meta: %+v", meta)
	}
	if s.auth[0] != "token tok" {
		t.Errorf("Authorization = %q, want %q", s.auth[0], "token tok")
	}
	if !g.CheckPushAccess(context.Background(), giteaTestTarget, "tok") {
		t.Error("CheckPushAccess = false, want true")
	}
	if g.CheckPushAccess(context.Background(), giteaTestTarget, "") {
		t.Error("CheckPushAccess with no token must be false")
	}

	// No token on a private instance: the error says how to get one.
	s.status["GET /repos/acme/widgets/pulls/5"] = http.StatusForbidden
	if _, err := g.FetchPR(context.Background(), giteaTestTarget, ""); err == nil || !strings.Contains(err.Error(), "tea login add") {
		t.Errorf("err = %v, want a hint to run 'tea login add'", err)
	}
}

func TestGiteaSubmitReviewPayload(t *testing.T) {
	s := newGiteaStub(t)
	s.routes["POST /repos/acme/widgets/pulls/5/reviews"] = `{"id": 900}`

	comments := []prComment{
		{ID: 1, Path: "main.go", Line: 10, Side: "RIGHT", Body: "new side"},
		{ID: 2, Path: "old.go", Line: 5, Side: "LEFT", Body: "base side"},
	}
	if err := (&GiteaProvider{}).SubmitReview(context.Background(), giteaTestTarget, "tok", "abc123", comments, "APPROVE", "LGTM"); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		CommitID string `json:"commit_id"`
		Body     string `json:"body"`
		Event    string `json:"event"`
		Comments []struct {
			Path        string `json:"path"`
			Body        string `json:"body"`
			NewPosition int    `json:"new_position"`
			OldPosition int    `json:"old_position"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(s.bodies["POST /repos/acme/widgets/pulls/5/reviews"], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Event != "APPROVED" || payload.CommitID != "abc123" || payload.Body != "LGTM" {
		t.Errorf("unexpected review: %+v", payload)
	}
	if len(payload.Comments) != 2 ||
		payload.Comments[0].NewPosition != 10 || payload.Comments[0].OldPosition != 0 ||
		payload.Comments[1].OldPosition != 5 || payload.Comments[1].NewPosition != 0 {
		t.Errorf("unexpected comments: %+v", payload.Comments)
	}

	s.status["POST /repos/acme/widgets/pulls/5/reviews"] = http.StatusUnprocessableEntity
	if err := (&GiteaProvider{}).SubmitReview(context.Background(), giteaTestTarget, "tok", "abc123", nil, "COMMENT", ""); err == nil {
		t.Error("expected an error when Gitea rejects the review")
	}
}

func giteaCommentsStub(t *testing.T) *giteaStub {
	s := newGiteaStub(t)
	s.routes["GET /repos/acme/widgets/issues/5/comments"] = `[
		{"id": 10, "body": "top-level", "created_at": "2026-01-01T00:00:00Z", "html_url": "u10", "user": {"login": "bob", "avatar_url": "a"}}
	]`
	s.routes["GET /repos/acme/widgets/pulls/5/reviews"] = `[
		{"id": 1, "state": "COMMENT", "comments_count": 2},
		{"id": 2, "state": "APPROVED", "comments_count": 0},
		{"id": 3, "state": "PENDING", "comments_count": 1},
		{"id": 4, "state": "REQUEST_CHANGES", "comments_count": 1}
	]`
	s.routes["GET /repos/acme/widgets/pulls/5/reviews/1/comments"] = `[
		{"id": 20, "body": "first", "path": "a.go", "commit_id": "c1", "position": 7, "original_position": 0, "user": {"login": "bob"}},
		{"id": 21, "body": "on the base", "path": "a.go", "commit_id": "c1", "position": 0, "original_position": 7, "user": {"login": "bob"}}
	]`
	s.routes["GET /repos/acme/widgets/pulls/5/reviews/4/comments"] = `[
		{"id": 30, "body": "follow-up", "path": "a.go", "commit_id": "c2", "position": 7, "original_position": 0, "user": {"login": "carol"}}
	]`
	return s
}

func TestGiteaFetchCommentsThreads(t *testing.T) {
	s := giteaCommentsStub(t)
	issue, review, err := (&GiteaProvider{}).FetchComments(context.Background(), giteaTestTarget, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if len(issue) != 1 || issue[0].ID != 10 || issue[0].Kind != "issue" || issue[0].Author != "bob" {
		t.Errorf("unexpected issue comments: %+v", issue)
	}
	byID := map[int64]PRComment{}
	for _, c := range review {
		byID[c.ID] = c
	}
	if len(review) != 3 {
		t.Fatalf("got %d review comments, want 3: %+v", len(review), review)
	}
	if c := byID[20]; c.Side != "RIGHT" || c.Line != 7 || c.InReplyTo != 0 {
		t.Errorf("comment 20 = %+v, want a RIGHT:7 thread root", c)
	}
	if c := byID[21]; c.Side != "LEFT" || c.Line != 7 || c.InReplyTo != 0 {
		t.Errorf("comment 21 = %+v, want its own LEFT:7 thread", c)
	}
	if c := byID[30]; c.InReplyTo != 20 {
		t.Errorf("comment 30 InReplyTo = %d, want 20 (same path, side, line)", c.InReplyTo)
	}
	for _, call := range s.calls {
		if strings.Contains(call, "/reviews/2/") || strings.Contains(call, "/reviews/3/") {
			t.Errorf("fetched comments of an empty or pending review: %s", call)
		}
	}
}

func TestGiteaReplyToReviewComment(t *testing.T) {
	s := giteaCommentsStub(t)
	s.routes["POST /repos/acme/widgets/pulls/5/reviews"] = `{"id": 5}`
	s.routes["GET /repos/acme/widgets/pulls/5/reviews/5/comments"] = `[
		{"id": 40, "body": "thanks", "path": "a.go", "commit_id": "c2", "position": 7, "original_position": 0, "user": {"login": "me"}}
	]`

	reply, err := (&GiteaProvider{}).ReplyToReviewComment(context.Background(), giteaTestTarget, "tok", 30, "thanks")
	if err != nil {
		t.Fatal(err)
	}
	if reply.ID != 40 || reply.InReplyTo != 20 || reply.Author != "me" {
		t.Errorf("reply = %+v, want comment 40 threaded under 20", reply)
	}
	var payload struct {
		CommitID string `json:"commit_id"`
		Event    string `json:"event"`
		Comments []struct {
			Path        string `json:"path"`
			Body        string `json:"body"`
			NewPosition int    `json:"new_position"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(s.bodies["POST /repos/acme/widgets/pulls/5/reviews"], &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Event != "COMMENT" || payload.CommitID != "c2" || len(payload.Comments) != 1 ||
		payload.Comments[0].Path != "a.go" || payload.Comments[0].NewPosition != 7 || payload.Comments[0].Body != "thanks" {
		t.Errorf("unexpected reply review: %+v", payload)
	}

	if _, err := (&GiteaProvider{}).ReplyToReviewComment(context.Background(), giteaTestTarget, "tok", 999, "x"); err == nil {
		t.Error("expected an error replying to an unknown comment")
	}
}

func TestGiteaPostIssueComment(t *testing.T) {
	s := newGiteaStub(t)
	s.routes["POST /repos/acme/widgets/issues/5/comments"] = `{"id": 11, "body": "hi", "user": {"login": "me"}}`
	s.status["POST /repos/acme/widgets/issues/5/comments"] = http.StatusCreated
	c, err := (&GiteaProvider{}).PostIssueComment(context.Background(), giteaTestTarget, "tok", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != 11 || c.Kind != "issue" || c.Body != "hi" {
		t.Errorf("unexpected comment: %+v", c)
	}
	if got := string(s.bodies["POST /repos/acme/widgets/issues/5/comments"]); got != `{"body":"hi"}` {
		t.Errorf("request body = %s", got)
	}
}
