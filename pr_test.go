package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetectPRURL(t *testing.T) {
	cases := []struct {
		arg         string
		wantOK      bool
		owner, repo string
		num         int
	}{
		{arg: "https://github.com/px0-ai/px0/pull/42", wantOK: true, owner: "px0-ai", repo: "px0", num: 42},
		{arg: "http://github.com/px0-ai/px0/pull/7", wantOK: true, owner: "px0-ai", repo: "px0", num: 7},
		{arg: "github.com/px0-ai/px0/pull/1", wantOK: true, owner: "px0-ai", repo: "px0", num: 1},
		{arg: "https://github.com/px0-ai/px0.git/pull/99", wantOK: true, owner: "px0-ai", repo: "px0", num: 99},
		// Bare numbers must NOT be accepted as PR targets
		{arg: "42", wantOK: false},
		{arg: "123", wantOK: false},
		// File paths must NOT be accepted
		{arg: "src/main.go", wantOK: false},
		{arg: ".", wantOK: false},
		// Non-PR GitHub URLs must NOT be accepted
		{arg: "https://github.com/px0-ai/px0", wantOK: false},
		{arg: "https://github.com/px0-ai/px0/issues/42", wantOK: false},
		// Invalid / empty
		{arg: "not a url", wantOK: false},
		{arg: "", wantOK: false},
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
		if p.Name() != "github" {
			t.Errorf("DetectPRURL(%q) provider = %q, want github", c.arg, p.Name())
		}
		if target.Owner != c.owner || target.Repo != c.repo || target.Number != c.num {
			t.Errorf("DetectPRURL(%q) = (%q, %q, %d), want (%q, %q, %d)", c.arg, target.Owner, target.Repo, target.Number, c.owner, c.repo, c.num)
		}
	}
}

func TestParsePRURLUnsupported(t *testing.T) {
	if _, _, err := ParsePRURL("42"); err == nil {
		t.Error("expected error parsing bare number as PR URL")
	}
	if _, _, err := ParsePRURL("https://example.com/foo/bar"); err == nil {
		t.Error("expected error for unsupported forge URL")
	} else {
		if !strings.Contains(err.Error(), "GitHub") || !strings.Contains(err.Error(), "Bitbucket") {
			t.Errorf("expected error to mention GitHub and Bitbucket, got: %v", err)
		}
	}
}

func TestParsePRURL_U1Scenarios(t *testing.T) {
	orig := defaultProviders
	defaultProviders = []GitProvider{&GitHubProvider{}}
	defer func() { defaultProviders = orig }()

	tmp := t.TempDir()
	someDir := filepath.Join(tmp, "some", "dir")
	if err := os.MkdirAll(someDir, 0755); err != nil {
		t.Fatal(err)
	}
	literalHTTPSDir := filepath.Join(tmp, "https:")
	if err := os.MkdirAll(literalHTTPSDir, 0755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		arg           string
		wantIsURL     bool
		wantDetect    bool
		wantErr       bool
		errContains   []string
		errNotContain []string
		isFSTarget    bool
	}{
		{
			name:        "Scenario 1: Bitbucket PR URL without registered provider returns unsupported error, not path error",
			arg:         "https://bitbucket.org/blgtech/hrms/pull-requests/371",
			wantIsURL:   true,
			wantDetect:  false,
			wantErr:     true,
			errContains: []string{"unsupported or unrecognized PR URL", "GitHub", "Bitbucket"},
			errNotContain: []string{
				"lstat",
				"no such file or directory",
				"invalid target",
			},
			isFSTarget: false,
		},
		{
			name:        "Scenario 2: GitLab MR URL returns unsupported error naming GitHub and Bitbucket shapes",
			arg:         "https://gitlab.com/a/b/-/merge_requests/1",
			wantIsURL:   true,
			wantDetect:  false,
			wantErr:     true,
			errContains: []string{"unsupported or unrecognized PR URL", "GitHub", "Bitbucket", "https://github.com/", "https://bitbucket.org/"},
			errNotContain: []string{
				"lstat",
				"no such file or directory",
			},
			isFSTarget: false,
		},
		{
			name:       "Scenario 3a: Dot resolves as filesystem target",
			arg:        ".",
			wantIsURL:  false,
			wantDetect: false,
			wantErr:    true,
			isFSTarget: true,
		},
		{
			name:       "Scenario 3b: Relative dir resolves as filesystem target",
			arg:        someDir,
			wantIsURL:  false,
			wantDetect: false,
			wantErr:    true,
			isFSTarget: true,
		},
		{
			name:       "Scenario 3c: Absolute path resolves as filesystem target",
			arg:        tmp,
			wantIsURL:  false,
			wantDetect: false,
			wantErr:    true,
			isFSTarget: true,
		},
		{
			name:        "Scenario 4: Argument containing :// is intercepted by IsURL and never reaches literal https: dir",
			arg:         "https://bitbucket.org/blgtech/hrms/pull-requests/371",
			wantIsURL:   true,
			wantDetect:  false,
			wantErr:     true,
			errContains: []string{"unsupported or unrecognized PR URL"},
			errNotContain: []string{
				"lstat",
				"no such file or directory",
			},
			isFSTarget: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotIsURL := IsURL(tc.arg)
			if gotIsURL != tc.wantIsURL {
				t.Errorf("IsURL(%q) = %v, want %v", tc.arg, gotIsURL, tc.wantIsURL)
			}

			_, _, gotDetect := DetectPRURL(tc.arg)
			if gotDetect != tc.wantDetect {
				t.Errorf("DetectPRURL(%q) = %v, want %v", tc.arg, gotDetect, tc.wantDetect)
			}

			_, _, err := ParsePRURL(tc.arg)
			if (err != nil) != tc.wantErr {
				t.Errorf("ParsePRURL(%q) err = %v, wantErr = %v", tc.arg, err, tc.wantErr)
			}
			if err != nil {
				errMsg := err.Error()
				for _, sub := range tc.errContains {
					if !strings.Contains(errMsg, sub) {
						t.Errorf("ParsePRURL(%q) error %q does not contain %q", tc.arg, errMsg, sub)
					}
				}
				for _, sub := range tc.errNotContain {
					if strings.Contains(errMsg, sub) {
						t.Errorf("ParsePRURL(%q) error %q unexpectedly contains %q", tc.arg, errMsg, sub)
					}
				}
			}

			if tc.isFSTarget {
				root, _, _, resolveErr := resolveTarget(tc.arg)
				if resolveErr != nil {
					t.Errorf("resolveTarget(%q) failed: %v", tc.arg, resolveErr)
				}
				if root == "" {
					t.Errorf("resolveTarget(%q) returned empty root", tc.arg)
				}
			}

			if tc.wantIsURL && !tc.wantDetect {
				if !gotIsURL {
					t.Errorf("guard failed: %q containing :// must have IsURL == true", tc.arg)
				}
			}
		})
	}
}

func TestResolveGitHubTokenPrecedence(t *testing.T) {
	// settings.json wins over everything, including the environment.
	t.Setenv("GITHUB_TOKEN", "env-token")
	settingsToken := "settings-token"
	token, source := resolveGitHubToken(settings{GitHubToken: &settingsToken})
	if token != "settings-token" || source != "settings" {
		t.Errorf("got (%q, %q), want (settings-token, settings)", token, source)
	}

	// With no settings token, the environment variable wins.
	token, source = resolveGitHubToken(settings{})
	if token != "env-token" || source != "env" {
		t.Errorf("got (%q, %q), want (env-token, env)", token, source)
	}
}

func TestResolveGitHubTokenNoneAvailable(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	// Force `gh` to be unresolvable so the outcome is deterministic
	// regardless of whether the test machine happens to have it installed.
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	token, source := resolveGitHubToken(settings{})
	if token != "" || source != "" {
		t.Errorf("got (%q, %q), want (\"\", \"\")", token, source)
	}
}

func TestResolveGitHubTokenBlankSettingsFallsThrough(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-token")
	blank := "   "
	token, source := resolveGitHubToken(settings{GitHubToken: &blank})
	if token != "env-token" || source != "env" {
		t.Errorf("a blank settings token must fall through to the env var; got (%q, %q)", token, source)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSubmitReviewPayload(t *testing.T) {
	orig := githubHTTPClient.Transport
	defer func() { githubHTTPClient.Transport = orig }()

	var capturedBody []byte
	var capturedPath string
	githubHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		capturedPath = req.URL.Path
		capturedBody, _ = io.ReadAll(req.Body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{}")),
			Header:     make(http.Header),
		}, nil
	})

	ctx := context.Background()
	comments := []prComment{
		{ID: 1, Path: "main.go", Line: 10, Side: "RIGHT", Body: "looks good"},
		{ID: 2, Path: "old.go", Line: 5, Side: "LEFT", Body: "deleted line comment"},
	}
	err := submitReview(ctx, "px0-ai", "px0", 42, "dummy-token", "abc123sha", comments, "APPROVE", "Overall LGTM")
	if err != nil {
		t.Fatalf("submitReview failed: %v", err)
	}
	if capturedPath != "/repos/px0-ai/px0/pulls/42/reviews" {
		t.Errorf("unexpected path: %q", capturedPath)
	}
	var payload struct {
		CommitID string `json:"commit_id"`
		Body     string `json:"body"`
		Event    string `json:"event"`
		Comments []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Side string `json:"side"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to parse captured payload: %v", err)
	}
	if payload.CommitID != "abc123sha" {
		t.Errorf("payload.CommitID = %q, want abc123sha", payload.CommitID)
	}
	if payload.Event != "APPROVE" || payload.Body != "Overall LGTM" {
		t.Errorf("unexpected event or body: %+v", payload)
	}
	if len(payload.Comments) != 2 || payload.Comments[1].Side != "LEFT" {
		t.Errorf("unexpected comments in payload: %+v", payload.Comments)
	}
}

func TestPRSessionCloseRefCleanup(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.name", "test")
	run("config", "user.email", "test@test.local")
	run("commit", "--allow-empty", "-m", "test: initial")

	run("update-ref", "refs/px0/pr/99", "HEAD")
	run("update-ref", "refs/px0/base/99", "HEAD")

	tmpWT := t.TempDir()
	p := &prSession{
		srcRepo:  root,
		worktree: tmpWT,
		meta:     PRMeta{Number: 99},
	}
	p.Close()

	checkRef := exec.Command("git", "-C", root, "rev-parse", "--verify", "refs/px0/pr/99")
	if err := checkRef.Run(); err == nil {
		t.Errorf("refs/px0/pr/99 was not deleted on Close")
	}
	checkBase := exec.Command("git", "-C", root, "rev-parse", "--verify", "refs/px0/base/99")
	if err := checkBase.Run(); err == nil {
		t.Errorf("refs/px0/base/99 was not deleted on Close")
	}
}

// TestPRSessionPullFastForwardAndDiverge exercises prSession.Pull against a
// fake "upstream" (a bare repo standing in for GitHub) with a srcRepo/
// worktree pair set up exactly like checkoutPR's worktree case. A clean
// fast-forward onto a new PR commit must succeed and update meta.HeadSHA; a
// local commit in the worktree that then diverges from a further PR push
// must be refused with errPRDiverged, leaving the worktree untouched.
func TestPRSessionPullFastForwardAndDiverge(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	upstream := filepath.Join(base, "upstream.git")
	srcRepo := filepath.Join(base, "src")

	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, upstream, "init", "--bare", "-b", "main")

	gitTestRun(t, base, "clone", upstream, "src")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, srcRepo, "config", cfg[0], cfg[1])
	}
	if err := os.WriteFile(filepath.Join(srcRepo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, srcRepo, "add", "base.txt")
	gitTestRun(t, srcRepo, "commit", "-qm", "base commit")
	gitTestRun(t, srcRepo, "push", "origin", "main")

	// A "PR branch" pushed to upstream as refs/pull/99/head, the way GitHub does.
	gitTestRun(t, srcRepo, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(srcRepo, "feature.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, srcRepo, "add", "feature.txt")
	gitTestRun(t, srcRepo, "commit", "-qm", "pr commit 1")
	gitTestRun(t, srcRepo, "push", "origin", "feature:refs/pull/99/head")
	gitTestRun(t, srcRepo, "checkout", "-q", "main")

	// Check out the PR into a worktree, same as checkoutPR does.
	gitTestRun(t, srcRepo, "fetch", "--no-tags", "origin", "refs/pull/99/head:refs/px0/pr/99")
	worktree := filepath.Join(base, "wt")
	gitTestRun(t, srcRepo, "worktree", "add", "--detach", worktree, "refs/px0/pr/99")

	p := &prSession{
		worktree: worktree,
		srcRepo:  srcRepo,
		target:   PRTarget{Owner: "o", Repo: "r"},
		meta:     PRMeta{Number: 99, BaseRef: "main", HeadRef: "feature"},
	}

	// Someone pushes a second commit to the PR head -- Pull should fast-forward cleanly.
	gitTestRun(t, srcRepo, "checkout", "-q", "feature")
	if err := os.WriteFile(filepath.Join(srcRepo, "feature.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, srcRepo, "commit", "-aqm", "pr commit 2")
	gitTestRun(t, srcRepo, "push", "origin", "feature:refs/pull/99/head")
	gitTestRun(t, srcRepo, "checkout", "-q", "main")

	info, err := p.Pull(context.Background())
	if err != nil {
		t.Fatalf("expected a clean fast-forward Pull, got %v", err)
	}
	if info == "" {
		t.Error("expected a non-empty info message")
	}
	if got, err := os.ReadFile(filepath.Join(worktree, "feature.txt")); err != nil || string(got) != "two\n" {
		t.Fatalf("expected worktree to fast-forward to %q, got %q, err=%v", "two\n", got, err)
	}
	if p.meta.HeadSHA == "" {
		t.Error("expected Pull to record the new HeadSHA")
	}

	// A local commit in the worktree (as if the reviewer committed a fix)
	// that then diverges from a further PR push must be refused, not merged.
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, worktree, "commit", "-aqm", "reviewer's local commit")

	gitTestRun(t, srcRepo, "checkout", "-q", "feature")
	if err := os.WriteFile(filepath.Join(srcRepo, "feature.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, srcRepo, "commit", "-aqm", "pr commit 3")
	gitTestRun(t, srcRepo, "push", "origin", "feature:refs/pull/99/head")
	gitTestRun(t, srcRepo, "checkout", "-q", "main")

	if _, err := p.Pull(context.Background()); !errors.Is(err, errPRDiverged) {
		t.Fatalf("expected errPRDiverged, got %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(worktree, "feature.txt")); err != nil || string(got) != "local edit\n" {
		t.Fatalf("expected worktree untouched by the refused pull, got %q, err=%v", got, err)
	}
}

// TestPRSessionPush confirms Push sends the worktree's HEAD to the PR's
// actual head branch (meta.HeadRepoCloneURL/HeadRef), not wherever the
// worktree happens to be checked out from.
func TestPRSessionPush(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	upstream := filepath.Join(base, "upstream.git")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, upstream, "init", "--bare", "-b", "main")

	worktree := filepath.Join(base, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, worktree, "init", "-q", "-b", "feature/pr-1")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, worktree, "config", cfg[0], cfg[1])
	}
	if err := os.WriteFile(filepath.Join(worktree, "f.txt"), []byte("pushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, worktree, "add", "f.txt")
	gitTestRun(t, worktree, "commit", "-qm", "reviewer commit")

	p := &prSession{
		worktree: worktree,
		target:   PRTarget{Owner: "o", Repo: "r"},
		meta:     PRMeta{Number: 1, HeadRef: "feature/pr-1", HeadRepoCloneURL: upstream},
	}
	if err := p.Push(context.Background()); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	out := gitTestRun(t, upstream, "log", "--oneline", "-1", "refs/heads/feature/pr-1")
	if !strings.Contains(out, "reviewer commit") {
		t.Fatalf("expected upstream's refs/heads/feature to carry the pushed commit, got %q", out)
	}
	pushed := strings.TrimSpace(gitTestRun(t, worktree, "rev-parse", "HEAD"))
	if p.meta.HeadSHA != pushed {
		t.Fatalf("Push should move the PR head (meta.HeadSHA) to %s, got %q", pushed, p.meta.HeadSHA)
	}
	if p.remoteHead() != pushed {
		t.Fatalf("remoteHead = %q, want %s", p.remoteHead(), pushed)
	}
}

// TestPRAheadCountsFromPRHead covers the detached-HEAD case: a PR checkout has
// no upstream, so commits made in the IDE must still count as unpushed
// (measured from the PR head), and stop counting once that head moves.
func TestPRAheadCountsFromPRHead(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	gitTestRun(t, root, "init", "-q", "-b", "main")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, root, "config", cfg[0], cfg[1])
	}
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("1\n"), 0o644)
	gitTestRun(t, root, "add", ".")
	gitTestRun(t, root, "commit", "-qm", "pr commit")
	head := strings.TrimSpace(gitTestRun(t, root, "rev-parse", "HEAD"))
	gitTestRun(t, root, "checkout", "-q", "--detach")

	if n := gitCountSince(root, head); n != 0 {
		t.Fatalf("fresh checkout: ahead = %d, want 0", n)
	}
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("2\n"), 0o644)
	gitTestRun(t, root, "commit", "-qam", "my change")
	if n := gitCountSince(root, head); n != 1 {
		t.Fatalf("after IDE commit: ahead = %d, want 1", n)
	}
	cs := gitCommitsSince(root, head, 0)
	if len(cs) != 1 || cs[0].Subject != "my change" {
		t.Fatalf("unpushed = %+v", cs)
	}
	head = strings.TrimSpace(gitTestRun(t, root, "rev-parse", "HEAD"))
	if n := gitCountSince(root, head); n != 0 {
		t.Fatalf("after push moved the head: ahead = %d, want 0", n)
	}
}

func TestFetchPRMetaMerged(t *testing.T) {
	orig := githubHTTPClient.Transport
	defer func() { githubHTTPClient.Transport = orig }()

	githubHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := `{
			"number": 55,
			"title": "Fix login race condition",
			"state": "closed",
			"merged": true,
			"merged_at": "2026-09-20T10:00:00Z",
			"user": {"login": "alice"},
			"base": {"ref": "main"},
			"head": {
				"ref": "fix-race",
				"sha": "fedcba987654",
				"repo": {"clone_url": "https://github.com/alice/px0.git", "ssh_url": "git@github.com:alice/px0.git", "full_name": "alice/px0"}
			}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	meta, err := fetchPRMeta(context.Background(), "px0-ai", "px0", 55, "")
	if err != nil {
		t.Fatalf("fetchPRMeta failed: %v", err)
	}
	if !meta.Merged {
		t.Errorf("meta.Merged = false, want true")
	}
	if meta.State != "closed" {
		t.Errorf("meta.State = %q, want closed", meta.State)
	}
	if meta.MergedAt != "2026-09-20T10:00:00Z" {
		t.Errorf("meta.MergedAt = %q, want timestamp", meta.MergedAt)
	}
	if meta.HeadRepoCloneURL != "https://github.com/alice/px0.git" {
		t.Errorf("meta.HeadRepoCloneURL = %q, want https://github.com/alice/px0.git", meta.HeadRepoCloneURL)
	}
}

func TestCheckoutPRMergedAlwaysProceeds(t *testing.T) {
	orig := githubHTTPClient.Transport
	defer func() { githubHTTPClient.Transport = orig }()

	githubHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"number": 77, "title": "Already merged PR", "state": "closed", "merged": true, "merged_at": "2026-09-21T08:00:00Z", "user": {"login": "bob"}, "base": {"ref": "main"}, "head": {"ref": "feature-x", "sha": "1234567890ab", "repo": {"clone_url": "https://github.com/px0-ai/px0.git", "full_name": "px0-ai/px0"}}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	target := PRTarget{Provider: "github", Owner: "px0-ai", Repo: "px0", Number: 77}
	_, err := checkoutPR(context.Background(), &GitHubProvider{}, target, t.TempDir(), nil)
	// Must not be ErrPRMergedCancelled; merged PRs are always opened without blocking.
	if errors.Is(err, ErrPRMergedCancelled) {
		t.Errorf("err = %v, did not want ErrPRMergedCancelled", err)
	}
}

func TestGitHubProviderInterface(t *testing.T) {
	var gp GitProvider = &GitHubProvider{}
	if gp.Name() != "github" {
		t.Errorf("gp.Name() = %q, want github", gp.Name())
	}
	if !gp.MatchURL("https://github.com/foo/bar/pull/12") {
		t.Error("MatchURL should be true for valid github PR URL")
	}
	if gp.MatchURL("https://gitlab.com/foo/bar/-/merge_requests/12") {
		t.Error("MatchURL should be false for gitlab URL")
	}
	tgt, err := gp.ParseURL("https://github.com/foo/bar.git/pull/99")
	if err != nil {
		t.Fatalf("ParseURL failed: %v", err)
	}
	if tgt.Owner != "foo" || tgt.Repo != "bar" || tgt.Number != 99 || tgt.Provider != "github" {
		t.Errorf("unexpected target: %+v", tgt)
	}
	if ssh := gp.SSHURL(PRTarget{Owner: "a", Repo: "b"}); ssh != "git@github.com:a/b.git" {
		t.Errorf("gp.SSHURL = %q, want git@github.com:a/b.git", ssh)
	}
}

// TestGitFilesBetween is the PR's own file set: what changed between the
// merge-base and the PR head, never what the reviewer committed afterwards.
func TestGitFilesBetween(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	gitTestRun(t, root, "init", "-q", "-b", "main")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, root, "config", cfg[0], cfg[1])
	}
	write := func(name string) { os.WriteFile(filepath.Join(root, name), []byte(name+"\n"), 0o644) }
	write("base.txt")
	gitTestRun(t, root, "add", ".")
	gitTestRun(t, root, "commit", "-qm", "base")
	base := strings.TrimSpace(gitTestRun(t, root, "rev-parse", "HEAD"))
	write("pr.txt")
	gitTestRun(t, root, "add", ".")
	gitTestRun(t, root, "commit", "-qm", "pr")
	head := strings.TrimSpace(gitTestRun(t, root, "rev-parse", "HEAD"))
	write("mine.txt")
	gitTestRun(t, root, "add", ".")
	gitTestRun(t, root, "commit", "-qm", "mine")

	got := gitFilesBetween(root, base, head)
	if len(got) != 1 || got["pr.txt"] != "A" {
		t.Fatalf("PR files = %v, want map[pr.txt:A]", got)
	}
}

// mockSSHProvider implements GitProvider for testing checkoutPR with custom URLs.
type mockSSHProvider struct {
	GitHubProvider
	sshURL string
	meta   PRMeta
}

func (m *mockSSHProvider) SSHURL(target PRTarget) string {
	if m.sshURL != "" {
		return m.sshURL
	}
	return m.GitHubProvider.SSHURL(target)
}

func (m *mockSSHProvider) FetchPR(ctx context.Context, target PRTarget, token string) (PRMeta, error) {
	return m.meta, nil
}

func (m *mockSSHProvider) CheckPushAccess(ctx context.Context, target PRTarget, token string) bool {
	return true
}

func TestSSH_GitHubProviderSSHURL(t *testing.T) {
	var gp GitProvider = &GitHubProvider{}
	got := gp.SSHURL(PRTarget{Owner: "a", Repo: "b"})
	want := "git@github.com:a/b.git"
	if got != want {
		t.Errorf("provider.SSHURL = %q, want %q", got, want)
	}
}

func TestSSH_FetchPRMetaExtractsSSHURL(t *testing.T) {
	orig := githubHTTPClient.Transport
	defer func() { githubHTTPClient.Transport = orig }()

	githubHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		body := `{
			"number": 12,
			"title": "SSH test",
			"state": "open",
			"user": {"login": "charlie"},
			"base": {"ref": "main"},
			"head": {
				"ref": "ssh-feature",
				"sha": "aaa111",
				"repo": {
					"clone_url": "https://github.com/charlie/px0.git",
					"ssh_url": "git@github.com:charlie/px0.git",
					"full_name": "charlie/px0"
				}
			}
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	meta, err := fetchPRMeta(context.Background(), "px0-ai", "px0", 12, "")
	if err != nil {
		t.Fatalf("fetchPRMeta failed: %v", err)
	}
	if meta.HeadRepoCloneURL != "https://github.com/charlie/px0.git" {
		t.Errorf("HeadRepoCloneURL = %q, want https://github.com/charlie/px0.git", meta.HeadRepoCloneURL)
	}
}

// U2 Scenario 1: Clone path with HeadRepoCloneURL set to a local file:// upstream
// still checks out and computes the diff base.
func TestPRSession_U2_CloneWithFileUpstreamDiffBase(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	upstream := filepath.Join(base, "upstream.git")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, upstream, "init", "--bare", "-b", "main")

	initClone := filepath.Join(base, "init")
	gitTestRun(t, base, "clone", upstream, "init")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, initClone, "config", cfg[0], cfg[1])
	}
	if err := os.WriteFile(filepath.Join(initClone, "base.txt"), []byte("base commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, initClone, "add", "base.txt")
	gitTestRun(t, initClone, "commit", "-qm", "initial base commit")
	gitTestRun(t, initClone, "push", "origin", "main")

	baseSHA := strings.TrimSpace(gitTestRun(t, initClone, "rev-parse", "HEAD"))

	gitTestRun(t, initClone, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(initClone, "feature.txt"), []byte("feature commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, initClone, "add", "feature.txt")
	gitTestRun(t, initClone, "commit", "-qm", "feature work commit")
	gitTestRun(t, initClone, "push", "origin", "feature")

	headSHA := strings.TrimSpace(gitTestRun(t, initClone, "rev-parse", "HEAD"))

	upstreamURL := "file://" + upstream
	mock := &mockSSHProvider{
		sshURL: upstreamURL,
		meta: PRMeta{
			Number:           101,
			BaseRef:          "main",
			HeadRef:          "feature",
			HeadSHA:          headSHA,
			HeadRepoCloneURL: upstreamURL,
		},
	}
	target := PRTarget{Provider: "test", Owner: "owner", Repo: "repo", Number: 101}
	emptyCwd := t.TempDir()

	sess, err := checkoutPR(context.Background(), mock, target, emptyCwd, nil)
	if err != nil {
		t.Fatalf("checkoutPR failed: %v", err)
	}
	defer sess.Close()

	if sess.diffBase != baseSHA {
		t.Errorf("sess.diffBase = %q, want baseSHA %q", sess.diffBase, baseSHA)
	}
	if sess.diffBaseWarning != "" {
		t.Errorf("sess.diffBaseWarning = %q, want empty", sess.diffBaseWarning)
	}
	if sess.Root() == "" {
		t.Errorf("sess.Root() is empty")
	}
}

// U2 Scenario 2: A PRMeta with an empty HeadRepoCloneURL falls back to git@github.com:owner/repo.git.
func TestSSH_U2_EmptyHeadRepoCloneURLFallback(t *testing.T) {
	gp := &GitHubProvider{}
	target := PRTarget{Owner: "owner", Repo: "repo"}

	wantFallback := "git@github.com:owner/repo.git"
	if got := gp.SSHURL(target); got != wantFallback {
		t.Errorf("gp.SSHURL(%+v) = %q, want %q", target, got, wantFallback)
	}

	p := &prSession{
		target:   target,
		provider: gp,
		meta:     PRMeta{HeadRepoCloneURL: "", HeadRef: "main"},
		worktree: t.TempDir(),
	}
	err := p.Push(context.Background())
	if err == nil {
		t.Fatal("expected push to fail on empty/uninitialized worktree")
	}
	if strings.Contains(err.Error(), "https://") {
		t.Errorf("push should not use https: %v", err)
	}

	diffBase, diffBaseWarning := computeDiffBase(context.Background(), gp, t.TempDir(), "", target, "", "main", 1, nil)
	if diffBase != "HEAD" {
		t.Errorf("diffBase = %q, want HEAD on failed fetch", diffBase)
	}
	if !strings.Contains(diffBaseWarning, "could not resolve a merge-base") {
		t.Errorf("diffBaseWarning = %q, want merge-base warning", diffBaseWarning)
	}
}

// U2 Scenario 3: Push with a token converts an SSH remote to HTTPS
// (restored master's httpsRemoteURL conversion).
func TestPush_U2_HTTPSRewriteWithToken(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	worktree := filepath.Join(base, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, worktree, "init", "-q", "-b", "feature/test")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, worktree, "config", cfg[0], cfg[1])
	}
	os.WriteFile(filepath.Join(worktree, "f.txt"), []byte("data\n"), 0o644)
	gitTestRun(t, worktree, "add", "f.txt")
	gitTestRun(t, worktree, "commit", "-qm", "commit")

	for _, sshURL := range []string{
		"git@127.0.0.1:owner/repo.git",
		"ssh://git@127.0.0.1:1/owner/repo.git",
		"ssh://git@127.0.0.1:2222/owner/repo.git",
	} {
		p := &prSession{
			worktree: worktree,
			target:   PRTarget{Owner: "owner", Repo: "repo"},
			meta:     PRMeta{HeadRepoCloneURL: sshURL, HeadRef: "feature/test"},
			token:    "dummy-token",
			provider: &GitHubProvider{},
		}
		err := p.Push(context.Background())
		if err == nil {
			t.Fatalf("expected push to fail for %s", sshURL)
		}
		errMsg := err.Error()
		if !strings.Contains(errMsg, "https://") {
			t.Errorf("Push() with SSH remote %s and token should convert to HTTPS, got: %v", sshURL, errMsg)
		}
	}
}

// U2 Scenario 4: With no SSH key available, the git command fails within the
// command timeout and the error mentions SSH, not a token.
func TestSSH_U2_NoKeyFailsFastMentionsSSHNotToken(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}

	cmd := gitAuthCmd(context.Background(), "", "ls-remote", "git@nonexistent.invalid:owner/repo.git")
	emptyHome := t.TempDir()
	cmd.Env = append(cmd.Env, "HOME="+emptyHome, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")

	var hasPrompt0 bool
	for _, env := range cmd.Env {
		if env == "GIT_TERMINAL_PROMPT=0" {
			hasPrompt0 = true
		}
	}
	if !hasPrompt0 {
		t.Errorf("cmd.Env missing GIT_TERMINAL_PROMPT=0")
	}

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected git command to fail, output: %s", string(out))
	}

	outStr := strings.ToLower(string(out))
	if !strings.Contains(outStr, "ssh") && !strings.Contains(outStr, "host") && !strings.Contains(outStr, "fatal") {
		t.Errorf("expected error output to mention ssh/host/fatal, got: %s", string(out))
	}
	if strings.Contains(outStr, "token") || strings.Contains(outStr, "x-access-token") || strings.Contains(outStr, "extraheader") {
		t.Errorf("error output should not mention token: %s", string(out))
	}
}

func TestHTTPSRemoteURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:o/r.git":          "https://github.com/o/r.git",
		"ssh://git@github.com/o/r.git":    "https://github.com/o/r.git",
		"ssh://git@github.com:22/o/r.git": "https://github.com/o/r.git",
		"https://github.com/o/r.git":      "https://github.com/o/r.git",
		"/tmp/some/local/upstream.git":    "/tmp/some/local/upstream.git",
	} {
		if got := httpsRemoteURL(in); got != want {
			t.Errorf("httpsRemoteURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// U2 Scenario 5: gitAuthCmd with a token sets the extraheader; without a token
// it does not.
func TestSSH_U2_GitHubTokenDoesNotChangeGitAuthCmdEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_secret_token_123456789")

	// With no token parameter: no extraheader should be set.
	cmdNoToken := gitAuthCmd(context.Background(), "", "status")
	for _, env := range cmdNoToken.Env {
		if strings.Contains(env, "extraheader") {
			t.Errorf("cmdNoToken.Env contains extraheader: %s", env)
		}
	}

	// With a token parameter: the extraheader must be set.
	token := "my-test-token"
	cmdWithToken := gitAuthCmd(context.Background(), token, "status")
	var hasExtraheader, hasExpectedAuth bool
	wantAuth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	for _, env := range cmdWithToken.Env {
		if env == "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader" {
			hasExtraheader = true
		}
		if env == "GIT_CONFIG_VALUE_0="+wantAuth {
			hasExpectedAuth = true
		}
	}
	if !hasExtraheader {
		t.Errorf("cmdWithToken.Env missing extraheader key for token auth")
	}
	if !hasExpectedAuth {
		t.Errorf("cmdWithToken.Env missing expected auth header value")
	}

	var hasPrompt0 bool
	for _, e := range cmdWithToken.Env {
		if e == "GIT_TERMINAL_PROMPT=0" {
			hasPrompt0 = true
		}
	}
	if !hasPrompt0 {
		t.Errorf("cmdWithToken.Env missing GIT_TERMINAL_PROMPT=0")
	}
}

// TestPRSessionPullFollowsForcePush: a PR branch rewritten upstream is not a
// fast-forward, but with nothing of the reviewer's in the checkout Pull follows
// it instead of leaving stale content behind; with local commits it still refuses.
func TestPRSessionPullFollowsForcePush(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	upstream := filepath.Join(base, "upstream.git")
	os.MkdirAll(upstream, 0o755)
	gitTestRun(t, upstream, "init", "--bare", "-b", "feature")

	work := filepath.Join(base, "work")
	os.MkdirAll(work, 0o755)
	gitTestRun(t, work, "init", "-q", "-b", "feature")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, work, "config", cfg[0], cfg[1])
	}
	os.WriteFile(filepath.Join(work, "f.txt"), []byte("one\n"), 0o644)
	gitTestRun(t, work, "add", ".")
	gitTestRun(t, work, "commit", "-qm", "one")
	gitTestRun(t, work, "push", "-q", upstream, "feature")
	gitTestRun(t, upstream, "branch", "main", "feature")

	wt := filepath.Join(base, "wt")
	gitTestRun(t, base, "clone", "-q", upstream, "wt")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, wt, "config", cfg[0], cfg[1])
	}
	head := strings.TrimSpace(gitTestRun(t, wt, "rev-parse", "HEAD"))
	p := &prSession{
		worktree: wt,
		target:   PRTarget{Owner: "o", Repo: "r"},
		meta:     PRMeta{Number: 1, BaseRef: "main", HeadRef: "feature", HeadRepoCloneURL: upstream, HeadSHA: head},
		provider: &mockSSHProvider{sshURL: upstream},
	}

	// Rewrite the PR branch: amend and force-push.
	os.WriteFile(filepath.Join(work, "f.txt"), []byte("rewritten\n"), 0o644)
	gitTestRun(t, work, "commit", "-aqm", "one (amended)", "--amend")
	gitTestRun(t, work, "push", "-qf", upstream, "feature")

	if _, err := p.Pull(context.Background()); err != nil {
		t.Fatalf("Pull should follow a force-push when nothing local is at stake: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "f.txt")); string(got) != "rewritten\n" {
		t.Fatalf("worktree still has stale content: %q", got)
	}
	if p.meta.HeadSHA == head {
		t.Fatal("HeadSHA should have moved to the rewritten head")
	}

	// With a local commit in the way, the same situation is refused.
	os.WriteFile(filepath.Join(wt, "f.txt"), []byte("mine\n"), 0o644)
	gitTestRun(t, wt, "commit", "-aqm", "mine")
	os.WriteFile(filepath.Join(work, "f.txt"), []byte("rewritten again\n"), 0o644)
	gitTestRun(t, work, "commit", "-aqm", "again", "--amend")
	gitTestRun(t, work, "push", "-qf", upstream, "feature")
	if _, err := p.Pull(context.Background()); !errors.Is(err, errPRDiverged) {
		t.Fatalf("expected errPRDiverged with a local commit, got %v", err)
	}
}

type mockReviewSubmitProvider struct {
	BitbucketProvider
	submitFunc func(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error
}

func (m *mockReviewSubmitProvider) SubmitReview(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
	if m.submitFunc != nil {
		return m.submitFunc(ctx, target, token, headSHA, comments, event, body)
	}
	return nil
}

// U5 Scenario: failure on 3rd of 5 drafts keeps unposted drafts in session
func TestPRSubmit_U5_FailureOn3rdDraftKeepsUnpostedDraftsInSession(t *testing.T) {
	tempDir := t.TempDir()
	sessionMgr := newSessionManager("", tempDir)

	mockProv := &mockReviewSubmitProvider{
		submitFunc: func(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
			// Simulate failure on 3rd draft of 5:
			// first 2 succeeded (IDs 1, 2), 3rd failed
			return &PartialSubmitError{
				PostedIDs: []int64{1, 2},
				Step:      "draft",
				Err:       errors.New("API error on 3rd draft"),
			}
		},
	}

	initialDrafts := []prComment{
		{ID: 1, Path: "a.go", Line: 10, Side: "RIGHT", Body: "d1"},
		{ID: 2, Path: "b.go", Line: 20, Side: "RIGHT", Body: "d2"},
		{ID: 3, Path: "c.go", Line: 30, Side: "RIGHT", Body: "d3"},
		{ID: 4, Path: "d.go", Line: 40, Side: "RIGHT", Body: "d4"},
		{ID: 5, Path: "e.go", Line: 50, Side: "RIGHT", Body: "d5"},
	}

	sessionMgr.Update(func(ws *WorkspaceSession) {
		ws.Drafts = append([]prComment(nil), initialDrafts...)
	})

	sess := &prSession{
		provider:    mockProv,
		target:      PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 10},
		token:       "token-abc",
		writeAccess: true,
		meta:        PRMeta{Number: 10, HeadSHA: "headsha"},
		comments:    append([]prComment(nil), initialDrafts...),
	}

	srv := &Server{
		pr:      sess,
		session: sessionMgr,
		mux:     http.NewServeMux(),
	}
	srv.registerRoutes()

	bodyJSON := `{"event": "COMMENT", "body": "feedback"}`
	req := httptest.NewRequest(http.MethodPost, "/api/pr/submit", strings.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("HTTP status = %d, want %d; body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}

	// Verify unposted drafts (IDs 3, 4, 5) remain in p.comments
	sess.mu.Lock()
	remainingComments := append([]prComment(nil), sess.comments...)
	sess.mu.Unlock()

	if len(remainingComments) != 3 {
		t.Fatalf("len(p.comments) = %d, want 3", len(remainingComments))
	}
	wantIDs := []int64{3, 4, 5}
	for i, c := range remainingComments {
		if c.ID != wantIDs[i] {
			t.Errorf("remaining comment[%d].ID = %d, want %d", i, c.ID, wantIDs[i])
		}
	}

	// Verify unposted drafts (IDs 3, 4, 5) remain in s.session.Drafts
	sessionDrafts := sessionMgr.Get().Drafts
	if len(sessionDrafts) != 3 {
		t.Fatalf("len(s.session.Drafts) = %d, want 3", len(sessionDrafts))
	}
	for i, c := range sessionDrafts {
		if c.ID != wantIDs[i] {
			t.Errorf("session draft[%d].ID = %d, want %d", i, c.ID, wantIDs[i])
		}
	}
}

// U5 Scenario: failure on verdict clears all drafts and reports verdict step
func TestPRSubmit_U5_FailureOnVerdictClearsAllDraftsAndReportsVerdictStep(t *testing.T) {
	tempDir := t.TempDir()
	sessionMgr := newSessionManager("", tempDir)

	mockProv := &mockReviewSubmitProvider{
		submitFunc: func(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
			// All 3 drafts succeeded, but verdict failed
			return &PartialSubmitError{
				PostedIDs: []int64{10, 20, 30},
				Step:      "verdict",
				Err:       errors.New("bitbucket: submit review verdict: 403 forbidden"),
			}
		},
	}

	initialDrafts := []prComment{
		{ID: 10, Path: "a.go", Line: 1, Side: "RIGHT", Body: "d10"},
		{ID: 20, Path: "b.go", Line: 2, Side: "RIGHT", Body: "d20"},
		{ID: 30, Path: "c.go", Line: 3, Side: "RIGHT", Body: "d30"},
	}

	sessionMgr.Update(func(ws *WorkspaceSession) {
		ws.Drafts = append([]prComment(nil), initialDrafts...)
	})

	sess := &prSession{
		provider:    mockProv,
		target:      PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 10},
		token:       "token-abc",
		writeAccess: true,
		meta:        PRMeta{Number: 10, HeadSHA: "headsha"},
		comments:    append([]prComment(nil), initialDrafts...),
	}

	srv := &Server{
		pr:      sess,
		session: sessionMgr,
		mux:     http.NewServeMux(),
	}
	srv.registerRoutes()

	bodyJSON := `{"event": "APPROVE", "body": "looks great"}`
	req := httptest.NewRequest(http.MethodPost, "/api/pr/submit", strings.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("HTTP status = %d, want %d; body = %s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}

	// Verify error response reports the verdict step
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if !strings.Contains(resp.Error, "verdict") {
		t.Errorf("response error %q does not report verdict step", resp.Error)
	}

	// Verify all drafts were cleared from p.comments
	sess.mu.Lock()
	remComments := sess.comments
	sess.mu.Unlock()
	if len(remComments) != 0 {
		t.Errorf("len(p.comments) = %d, want 0 (cleared)", len(remComments))
	}

	// Verify all drafts were cleared from s.session.Drafts
	sessionDrafts := sessionMgr.Get().Drafts
	if len(sessionDrafts) != 0 {
		t.Errorf("len(s.session.Drafts) = %d, want 0 (cleared)", len(sessionDrafts))
	}
}

func TestPRSubmit_U5_SuccessfulSubmitClearsAllDrafts(t *testing.T) {
	tempDir := t.TempDir()
	sessionMgr := newSessionManager("", tempDir)

	mockProv := &mockReviewSubmitProvider{
		submitFunc: func(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
			return nil
		},
	}

	initialDrafts := []prComment{
		{ID: 1, Path: "a.go", Line: 10, Side: "RIGHT", Body: "note"},
	}

	sessionMgr.Update(func(ws *WorkspaceSession) {
		ws.Drafts = append([]prComment(nil), initialDrafts...)
	})

	sess := &prSession{
		provider:    mockProv,
		target:      PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 10},
		token:       "token-abc",
		writeAccess: true,
		meta:        PRMeta{Number: 10, HeadSHA: "headsha"},
		comments:    append([]prComment(nil), initialDrafts...),
	}

	srv := &Server{
		pr:      sess,
		session: sessionMgr,
		mux:     http.NewServeMux(),
	}
	srv.registerRoutes()

	bodyJSON := `{"event": "COMMENT", "body": "overall"}`
	req := httptest.NewRequest(http.MethodPost, "/api/pr/submit", strings.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	sess.mu.Lock()
	remComments := sess.comments
	sess.mu.Unlock()
	if len(remComments) != 0 {
		t.Errorf("len(p.comments) = %d, want 0", len(remComments))
	}
	if len(sessionMgr.Get().Drafts) != 0 {
		t.Errorf("len(s.session.Drafts) = %d, want 0", len(sessionMgr.Get().Drafts))
	}
}

// U6 Scenario 1: Bitbucket missing token error mentions BITBUCKET_TOKEN and not GITHUB_TOKEN
func TestU6_BitbucketMissingTokenErrors(t *testing.T) {
	sess := &prSession{
		provider: &BitbucketProvider{},
		target:   PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 10},
		token:    "",
	}
	srv := &Server{
		pr:  sess,
		mux: http.NewServeMux(),
	}
	srv.registerRoutes()

	// 1. Issue comment post
	reqIssue := httptest.NewRequest(http.MethodPost, "/api/pr/comments/issue", strings.NewReader(`{"body": "test comment"}`))
	reqIssue.Header.Set("Content-Type", "application/json")
	reqIssue.Host = "127.0.0.1:7777"
	reqIssue.Header.Set("Origin", "http://127.0.0.1:7777")
	recIssue := httptest.NewRecorder()
	srv.ServeHTTP(recIssue, reqIssue)

	if recIssue.Code != http.StatusForbidden {
		t.Fatalf("issue comment code = %d, want %d; body = %s", recIssue.Code, http.StatusForbidden, recIssue.Body.String())
	}
	var resIssue struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recIssue.Body.Bytes(), &resIssue); err != nil {
		t.Fatalf("failed to decode issue comment response: %v", err)
	}
	wantBitbucketMsg := "no auth token configured; posting comments requires a Bitbucket token (set BITBUCKET_TOKEN)"
	if resIssue.Error != wantBitbucketMsg {
		t.Errorf("issue comment error = %q, want %q", resIssue.Error, wantBitbucketMsg)
	}
	if !strings.Contains(resIssue.Error, "BITBUCKET_TOKEN") {
		t.Errorf("issue comment error does not mention BITBUCKET_TOKEN: %q", resIssue.Error)
	}
	if strings.Contains(resIssue.Error, "GITHUB_TOKEN") || strings.Contains(resIssue.Error, "GitHub") {
		t.Errorf("issue comment error mentions GitHub: %q", resIssue.Error)
	}

	// 2. Review comment reply
	reqReply := httptest.NewRequest(http.MethodPost, "/api/pr/comments/review-reply", strings.NewReader(`{"commentId": 42, "body": "test reply"}`))
	reqReply.Header.Set("Content-Type", "application/json")
	reqReply.Host = "127.0.0.1:7777"
	reqReply.Header.Set("Origin", "http://127.0.0.1:7777")
	recReply := httptest.NewRecorder()
	srv.ServeHTTP(recReply, reqReply)

	if recReply.Code != http.StatusForbidden {
		t.Fatalf("review reply code = %d, want %d; body = %s", recReply.Code, http.StatusForbidden, recReply.Body.String())
	}
	var resReply struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recReply.Body.Bytes(), &resReply); err != nil {
		t.Fatalf("failed to decode review reply response: %v", err)
	}
	if resReply.Error != wantBitbucketMsg {
		t.Errorf("review reply error = %q, want %q", resReply.Error, wantBitbucketMsg)
	}
	if !strings.Contains(resReply.Error, "BITBUCKET_TOKEN") {
		t.Errorf("review reply error does not mention BITBUCKET_TOKEN: %q", resReply.Error)
	}
	if strings.Contains(resReply.Error, "GITHUB_TOKEN") || strings.Contains(resReply.Error, "GitHub") {
		t.Errorf("review reply error mentions GitHub: %q", resReply.Error)
	}
}

// U6 Scenario 2: GitHub missing token error is unchanged
func TestU6_GitHubMissingTokenErrors(t *testing.T) {
	sess := &prSession{
		provider: &GitHubProvider{},
		target:   PRTarget{Provider: "github", Owner: "px0-ai", Repo: "px0", Number: 10},
		token:    "",
	}
	srv := &Server{
		pr:  sess,
		mux: http.NewServeMux(),
	}
	srv.registerRoutes()

	wantGitHubMsg := "no auth token configured; posting comments requires a GitHub token"

	// 1. Issue comment post
	reqIssue := httptest.NewRequest(http.MethodPost, "/api/pr/comments/issue", strings.NewReader(`{"body": "test comment"}`))
	reqIssue.Header.Set("Content-Type", "application/json")
	reqIssue.Host = "127.0.0.1:7777"
	reqIssue.Header.Set("Origin", "http://127.0.0.1:7777")
	recIssue := httptest.NewRecorder()
	srv.ServeHTTP(recIssue, reqIssue)

	if recIssue.Code != http.StatusForbidden {
		t.Fatalf("issue comment code = %d, want %d; body = %s", recIssue.Code, http.StatusForbidden, recIssue.Body.String())
	}
	var resIssue struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recIssue.Body.Bytes(), &resIssue); err != nil {
		t.Fatalf("failed to decode issue comment response: %v", err)
	}
	if resIssue.Error != wantGitHubMsg {
		t.Errorf("issue comment error = %q, want %q", resIssue.Error, wantGitHubMsg)
	}

	// 2. Review comment reply
	reqReply := httptest.NewRequest(http.MethodPost, "/api/pr/comments/review-reply", strings.NewReader(`{"commentId": 42, "body": "test reply"}`))
	reqReply.Header.Set("Content-Type", "application/json")
	reqReply.Host = "127.0.0.1:7777"
	reqReply.Header.Set("Origin", "http://127.0.0.1:7777")
	recReply := httptest.NewRecorder()
	srv.ServeHTTP(recReply, reqReply)

	if recReply.Code != http.StatusForbidden {
		t.Fatalf("review reply code = %d, want %d; body = %s", recReply.Code, http.StatusForbidden, recReply.Body.String())
	}
	var resReply struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recReply.Body.Bytes(), &resReply); err != nil {
		t.Fatalf("failed to decode review reply response: %v", err)
	}
	if resReply.Error != wantGitHubMsg {
		t.Errorf("review reply error = %q, want %q", resReply.Error, wantGitHubMsg)
	}
}

// U6 Scenario 3: handleLaunchPR error mentions both GitHub and Bitbucket
func TestU6_HandleLaunchPRErrorMentionsBoth(t *testing.T) {
	srv := &Server{
		mux: http.NewServeMux(),
	}
	srv.registerRoutes()

	req := httptest.NewRequest(http.MethodPost, "/api/pr/launch", strings.NewReader(`{"target": "https://example.com/not-a-pr"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("launch code = %d, want %d; body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var res struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode launch response: %v", err)
	}
	wantMsg := "target must be a valid pull request URL (e.g. https://github.com/owner/repo/pull/123 or https://bitbucket.org/workspace/repo/pull-requests/123)"
	if res.Error != wantMsg {
		t.Errorf("launch error = %q, want %q", res.Error, wantMsg)
	}
	if !strings.Contains(res.Error, "github.com") || !strings.Contains(res.Error, "bitbucket.org") {
		t.Errorf("launch error does not mention both github and bitbucket: %q", res.Error)
	}
}

// U6 Scenario 4: TokenHint returns expected hint per provider
func TestU6_ProviderTokenHints(t *testing.T) {
	gp := &GitHubProvider{}
	if got := gp.TokenHint(); got != "set GITHUB_TOKEN or gh auth login" {
		t.Errorf("GitHubProvider.TokenHint() = %q, want %q", got, "set GITHUB_TOKEN or gh auth login")
	}

	bp := &BitbucketProvider{}
	if got := bp.TokenHint(); got != "set BITBUCKET_TOKEN" {
		t.Errorf("BitbucketProvider.TokenHint() = %q, want %q", got, "set BITBUCKET_TOKEN")
	}
}

// TestBitbucket_CheckoutAndPullInLocalClone verifies that checkoutPR and Pull
// for a Bitbucket PR work inside a local clone of the repository (srcRepo != "")
// by fetching refs/heads/<HeadRef> instead of GitHub's refs/pull/<num>/head.
func TestBitbucket_CheckoutAndPullInLocalClone(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}

	// 1. Set up bare upstream "remote" named blgtech/hrms
	upstream := filepath.Join(base, "upstream.git")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, upstream, "init", "--bare", "-b", "main")

	// 2. Initial commit on main and feature branch bugs/leave
	seedClone := filepath.Join(base, "seed")
	gitTestRun(t, base, "clone", upstream, "seed")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, seedClone, "config", cfg[0], cfg[1])
	}
	if err := os.WriteFile(filepath.Join(seedClone, "main.txt"), []byte("main commit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, seedClone, "add", "main.txt")
	gitTestRun(t, seedClone, "commit", "-qm", "initial main")
	gitTestRun(t, seedClone, "push", "origin", "main")
	baseSHA := strings.TrimSpace(gitTestRun(t, seedClone, "rev-parse", "HEAD"))

	gitTestRun(t, seedClone, "checkout", "-qb", "bugs/leave")
	if err := os.WriteFile(filepath.Join(seedClone, "fix.txt"), []byte("bug fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, seedClone, "add", "fix.txt")
	gitTestRun(t, seedClone, "commit", "-qm", "fix bug")
	gitTestRun(t, seedClone, "push", "origin", "bugs/leave")
	headSHA := strings.TrimSpace(gitTestRun(t, seedClone, "rev-parse", "HEAD"))

	// 3. User's local clone where origin matches blgtech/hrms
	localClone := filepath.Join(base, "local_hrms")
	gitTestRun(t, base, "clone", upstream, "local_hrms")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, localClone, "config", cfg[0], cfg[1])
	}

	upstreamURL := "file://" + upstream
	mockProv := &mockReviewSubmitProvider{
		BitbucketProvider: BitbucketProvider{},
	}
	// We wrap mockProv to return our desired PRMeta
	type mockBB struct {
		*BitbucketProvider
		meta PRMeta
	}
	prov := &struct {
		GitProvider
		meta PRMeta
	}{
		GitProvider: mockProv,
		meta: PRMeta{
			Number:           371,
			Title:            "Bugs/leave",
			BaseRef:          "main",
			HeadRef:          "bugs/leave",
			HeadSHA:          headSHA,
			HeadRepoCloneURL: upstreamURL,
			HeadIsFork:       false,
		},
	}

	// Create provider adapter
	testProv := &localBitbucketMock{
		BitbucketProvider: &BitbucketProvider{},
		meta:              prov.meta,
	}

	target := PRTarget{Provider: "bitbucket", Owner: "upstream", Repo: "git", Number: 371}
	// Make origin match target.Owner and target.Repo
	// git remote get-url origin will return upstream path containing "upstream" and "git"
	sess, err := checkoutPR(context.Background(), testProv, target, localClone, nil)
	if err != nil {
		t.Fatalf("checkoutPR failed in local clone: %v", err)
	}
	defer sess.Close()

	if sess.srcRepo != localClone {
		t.Errorf("sess.srcRepo = %q, want %q", sess.srcRepo, localClone)
	}
	if sess.diffBase != baseSHA {
		t.Errorf("sess.diffBase = %q, want %q", sess.diffBase, baseSHA)
	}
	if sess.meta.HeadSHA != headSHA {
		t.Errorf("sess.meta.HeadSHA = %q, want %q", sess.meta.HeadSHA, headSHA)
	}

	// 4. Test Pull when new commit is pushed to bugs/leave upstream
	if err := os.WriteFile(filepath.Join(seedClone, "fix2.txt"), []byte("second fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, seedClone, "add", "fix2.txt")
	gitTestRun(t, seedClone, "commit", "-qm", "second fix")
	gitTestRun(t, seedClone, "push", "origin", "bugs/leave")
	newHeadSHA := strings.TrimSpace(gitTestRun(t, seedClone, "rev-parse", "HEAD"))

	info, err := sess.Pull(context.Background())
	if err != nil {
		t.Fatalf("sess.Pull() failed: %v", err)
	}
	if !strings.Contains(info, "Updated") && !strings.Contains(info, "Fast-forward") && sess.meta.HeadSHA != newHeadSHA {
		t.Errorf("sess.Pull() did not update HeadSHA to %q, got %q", newHeadSHA, sess.meta.HeadSHA)
	}
}

type localBitbucketMock struct {
	*BitbucketProvider
	meta PRMeta
}

func (l *localBitbucketMock) FetchPR(ctx context.Context, target PRTarget, token string) (PRMeta, error) {
	return l.meta, nil
}

func (l *localBitbucketMock) CheckPushAccess(ctx context.Context, target PRTarget, token string) bool {
	return true
}

func TestGitSSHCmdSanitization(t *testing.T) {
	origSSH := os.Getenv("GIT_SSH_COMMAND")
	origPrompt := os.Getenv("GIT_TERMINAL_PROMPT")
	defer func() {
		os.Setenv("GIT_SSH_COMMAND", origSSH)
		os.Setenv("GIT_TERMINAL_PROMPT", origPrompt)
	}()

	os.Setenv("GIT_SSH_COMMAND", "ssh -i /path/to/key -o CustomPrompt=yes")
	os.Setenv("GIT_TERMINAL_PROMPT", "1")

	cmd := gitSSHCmd(context.Background(), "status")
	var sshCmds []string
	var promptVals []string
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "GIT_SSH_COMMAND=") {
			sshCmds = append(sshCmds, env)
		}
		if strings.HasPrefix(env, "GIT_TERMINAL_PROMPT=") {
			promptVals = append(promptVals, env)
		}
		if strings.Contains(env, "CustomPrompt=yes") {
			t.Errorf("cmd.Env contains host GIT_SSH_COMMAND: %s", env)
		}
	}

	if len(sshCmds) != 1 || sshCmds[0] != "GIT_SSH_COMMAND=ssh -o BatchMode=yes" {
		t.Errorf("expected exactly 1 GIT_SSH_COMMAND=ssh -o BatchMode=yes, got: %v", sshCmds)
	}
	if len(promptVals) != 1 || promptVals[0] != "GIT_TERMINAL_PROMPT=0" {
		t.Errorf("expected exactly 1 GIT_TERMINAL_PROMPT=0, got: %v", promptVals)
	}
}

// U4: A cancelled context terminates a git command and returns an error promptly.
func TestGitSubprocessContextCancellation(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	cmd := gitAuthCmd(ctx, "", "fetch", "https://example.com/repo.git")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", ctx.Err())
	}

	cmdSSH := gitSSHCmd(ctx, "fetch", "git@example.com:o/r.git")
	errSSH := cmdSSH.Run()
	if errSSH == nil {
		t.Fatal("expected error with cancelled context")
	}
}

// U4: A live context lets a normal git call finish.
func TestGitSubprocessLiveContext(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := gitAuthCmd(ctx, "", "version")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("expected git version to succeed, got %v", err)
	}
	if !strings.Contains(string(out), "git version") {
		t.Fatalf("unexpected git version output: %s", string(out))
	}
}

// U4: Pull and Push terminate when context is cancelled.
func TestPRSessionPullPushCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	p := &prSession{
		worktree: t.TempDir(),
		target:   PRTarget{Owner: "o", Repo: "r"},
		meta:     PRMeta{HeadRepoCloneURL: "https://example.com/repo.git", HeadRef: "main"},
		provider: &GitHubProvider{},
	}

	err := p.Push(ctx)
	if err == nil {
		t.Fatal("expected Push to fail on cancelled context")
	}

	_, err = p.Pull(ctx)
	if err == nil {
		t.Fatal("expected Pull to fail on cancelled context")
	}
}

// U5: On SSH failure matching "Permission denied (publickey)" or "Host key verification failed",
// append one fixed sentence telling the user to add an SSH key. Unrelated errors pass through.
func TestAppendSSHHint(t *testing.T) {
	cases := []struct {
		input    string
		wantHint bool
	}{
		{
			input:    "git@github.com: Permission denied (publickey).",
			wantHint: true,
		},
		{
			input:    "Host key verification failed.\nfatal: Could not read from remote repository.",
			wantHint: true,
		},
		{
			input:    "fatal: repository 'https://github.com/foo/bar.git' not found",
			wantHint: false,
		},
		{
			input:    "fatal: remote error: upload-pack not permitted",
			wantHint: false,
		},
	}
	for _, tc := range cases {
		got := appendSSHHint(tc.input)
		hasHint := strings.Contains(got, "add an SSH key to your git host account")
		if hasHint != tc.wantHint {
			t.Errorf("appendSSHHint(%q) hasHint = %v, want %v; got: %q", tc.input, hasHint, tc.wantHint, got)
		}
		if !tc.wantHint && got != tc.input {
			t.Errorf("unrelated error modified: got %q, want %q", got, tc.input)
		}
	}
}

// U5: When no token is set, the SSH attempt runs first and only.
func TestGitRunStepNoTokenRunsSSHOnly(t *testing.T) {
	var triedRemotes []string
	provider := &GitHubProvider{}
	target := PRTarget{Owner: "owner", Repo: "repo"}

	_, err := gitRunStep(context.Background(), provider, target, "", "https://github.com/owner/repo.git", "git@github.com:owner/repo.git", func(remote string) []string {
		triedRemotes = append(triedRemotes, remote)
		return []string{"version"}
	})
	if err != nil {
		t.Fatalf("expected git version to succeed: %v", err)
	}
	if len(triedRemotes) != 1 || triedRemotes[0] != "git@github.com:owner/repo.git" {
		t.Errorf("expected only SSH remote tried, got: %v", triedRemotes)
	}
}

// U5: Token set and HTTPS step succeeds: no SSH attempt.
// Token set and HTTPS step fails: SSH attempt runs with SSH URL, token redacted.
func TestGitRunStepTokenHTTPSFirstFallbackSSH(t *testing.T) {
	var triedRemotes []string
	provider := &GitHubProvider{}
	target := PRTarget{Owner: "owner", Repo: "repo"}
	token := "secret-pat-12345"

	// When HTTPS succeeds, SSH should NOT be attempted.
	triedRemotes = nil
	_, err := gitRunStep(context.Background(), provider, target, token, "https://github.com/owner/repo.git", "git@github.com:owner/repo.git", func(remote string) []string {
		triedRemotes = append(triedRemotes, remote)
		return []string{"version"}
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if len(triedRemotes) != 1 || triedRemotes[0] != "https://github.com/owner/repo.git" {
		t.Errorf("expected only HTTPS remote tried on success, got: %v", triedRemotes)
	}

	// When HTTPS fails, it falls back to SSH.
	triedRemotes = nil
	_, err = gitRunStep(context.Background(), provider, target, token, "https://github.com/owner/repo.git", "git@github.com:owner/repo.git", func(remote string) []string {
		triedRemotes = append(triedRemotes, remote)
		if remote == "https://github.com/owner/repo.git" {
			// Fail HTTPS attempt with token in error
			return []string{"nonexistent-subcommand-to-fail-https", token}
		}
		// Second attempt (SSH)
		return []string{"nonexistent-subcommand-to-fail-ssh"}
	})
	if err == nil {
		t.Fatal("expected failure on bad commands")
	}
	if len(triedRemotes) != 2 {
		t.Errorf("expected 2 attempts (HTTPS then SSH), got: %v", triedRemotes)
	}
	if len(triedRemotes) >= 2 && (triedRemotes[0] != "https://github.com/owner/repo.git" || triedRemotes[1] != "git@github.com:owner/repo.git") {
		t.Errorf("unexpected remote order: %v", triedRemotes)
	}
	// Verify token is redacted from final error
	if strings.Contains(err.Error(), token) {
		t.Errorf("final error contains unredacted token: %v", err)
	}
}

// U5: Bitbucket is SSH-only per KTD5 even when token is set.
func TestGitRunStepBitbucketIsSSHOnly(t *testing.T) {
	var triedRemotes []string
	provider := &BitbucketProvider{}
	target := PRTarget{Provider: "bitbucket", Owner: "owner", Repo: "repo"}

	_, err := gitRunStep(context.Background(), provider, target, "my-bb-token", "https://bitbucket.org/owner/repo.git", "git@bitbucket.org:owner/repo.git", func(remote string) []string {
		triedRemotes = append(triedRemotes, remote)
		return []string{"version"}
	})
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if len(triedRemotes) != 1 || triedRemotes[0] != "git@bitbucket.org:owner/repo.git" {
		t.Errorf("expected only SSH remote for Bitbucket, got: %v", triedRemotes)
	}
}

func TestHandlePRSubmitConcurrentDraftsPreserved(t *testing.T) {
	mockProv := &mockReviewSubmitProvider{
		submitFunc: func(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
			return nil
		},
	}

	sessionMgr := newSessionManager("", t.TempDir())
	sess := &prSession{
		provider:    mockProv,
		target:      PRTarget{Owner: "o", Repo: "r", Number: 10},
		token:       "token",
		writeAccess: true,
		meta:        PRMeta{Number: 10, HeadSHA: "headsha"},
		comments: []prComment{
			{ID: 1, Path: "a.go", Line: 10, Side: "RIGHT", Body: "c1"},
			{ID: 2, Path: "b.go", Line: 20, Side: "RIGHT", Body: "c2"},
		},
	}

	srv := &Server{
		pr:      sess,
		session: sessionMgr,
		mux:     http.NewServeMux(),
	}
	srv.registerRoutes()

	// Simulate concurrent draft added while SubmitReview is in progress
	mockProv.submitFunc = func(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
		sess.mu.Lock()
		sess.comments = append(sess.comments, prComment{ID: 3, Path: "c.go", Line: 30, Side: "RIGHT", Body: "concurrent draft"})
		sess.mu.Unlock()
		return nil
	}

	bodyJSON := `{"event": "COMMENT", "body": "overall comment"}`
	req := httptest.NewRequest(http.MethodPost, "/api/pr/submit", strings.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	sess.mu.Lock()
	remaining := append([]prComment(nil), sess.comments...)
	sess.mu.Unlock()

	if len(remaining) != 1 {
		t.Fatalf("len(sess.comments) = %d, want 1 (concurrent draft should be preserved)", len(remaining))
	}
	if remaining[0].ID != 3 || remaining[0].Body != "concurrent draft" {
		t.Errorf("remaining[0] = %+v, want concurrent draft with ID 3", remaining[0])
	}
}

func TestBitbucket_CheckoutDeletedSourceBranchFallbackToHeadSHA(t *testing.T) {
	upstream := t.TempDir()
	gitTestRun(t, upstream, "init", "--bare", "-b", "main")

	seedClone := t.TempDir()
	gitTestRun(t, seedClone, "clone", upstream, ".")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, seedClone, "config", cfg[0], cfg[1])
	}
	gitTestRun(t, seedClone, "checkout", "-b", "main")
	if err := os.WriteFile(filepath.Join(seedClone, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, seedClone, "add", "file.txt")
	gitTestRun(t, seedClone, "commit", "-qm", "initial commit")
	gitTestRun(t, seedClone, "push", "origin", "main")

	// Branch with fix
	gitTestRun(t, seedClone, "checkout", "-b", "feature-branch")
	if err := os.WriteFile(filepath.Join(seedClone, "file.txt"), []byte("feature edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTestRun(t, seedClone, "add", "file.txt")
	gitTestRun(t, seedClone, "commit", "-qm", "feature commit")
	gitTestRun(t, seedClone, "push", "origin", "feature-branch")
	headSHA := strings.TrimSpace(gitTestRun(t, seedClone, "rev-parse", "HEAD"))

	// Delete branch upstream to simulate deleted branch on merged PR
	gitTestRun(t, seedClone, "push", "origin", "--delete", "feature-branch")

	localClone := t.TempDir()
	gitTestRun(t, localClone, "clone", upstream, ".")
	for _, cfg := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitTestRun(t, localClone, "config", cfg[0], cfg[1])
	}

	testProv := &localBitbucketMock{
		BitbucketProvider: &BitbucketProvider{},
		meta: PRMeta{
			Number:           99,
			Title:            "Merged PR with deleted branch",
			BaseRef:          "main",
			HeadRef:          "", // Empty branch name!
			HeadSHA:          headSHA,
			HeadRepoCloneURL: upstream,
			HeadIsFork:       false,
		},
	}

	target := PRTarget{Provider: "bitbucket", Owner: "upstream", Repo: "git", Number: 99}
	sess, err := checkoutPR(context.Background(), testProv, target, localClone, nil)
	if err != nil {
		t.Fatalf("checkoutPR should succeed with HeadSHA fallback when HeadRef is empty: %v", err)
	}
	defer sess.Close()

	if sess.meta.HeadSHA != headSHA {
		t.Errorf("sess.meta.HeadSHA = %q, want %q", sess.meta.HeadSHA, headSHA)
	}
}
