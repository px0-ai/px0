package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// github.go talks to the GitHub REST API for PR review (pr.go) via the
// GitHubProvider implementation of GitProvider. Like git.go, it avoids
// third-party SDKs: net/http plus a shell-out to the gh CLI as one of three
// token sources, nothing more.

const githubAPIBase = "https://api.github.com"

var githubHTTPClient = &http.Client{Timeout: 15 * time.Second}

var githubPRURLRe = regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/]+)/pull/(\d+)`)

// GitHubProvider implements GitProvider for GitHub.
type GitHubProvider struct{}

func (g *GitHubProvider) Name() string { return "github" }

func (g *GitHubProvider) MatchURL(rawURL string) bool {
	return githubPRURLRe.MatchString(strings.TrimSpace(rawURL))
}

func (g *GitHubProvider) ParseURL(rawURL string) (PRTarget, error) {
	m := githubPRURLRe.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return PRTarget{}, fmt.Errorf("invalid GitHub pull request URL: %q (expected format https://github.com/owner/repo/pull/123)", rawURL)
	}
	n, _ := strconv.Atoi(m[3])
	return PRTarget{
		Provider: "github",
		Owner:    m[1],
		Repo:     strings.TrimSuffix(m[2], ".git"),
		Number:   n,
		URL:      rawURL,
	}, nil
}

func (g *GitHubProvider) ResolveToken(cfg settings) (token, source string) {
	return resolveGitHubToken(cfg)
}

func (g *GitHubProvider) FetchPR(ctx context.Context, target PRTarget, token string) (PRMeta, error) {
	return fetchPRMeta(ctx, target.Owner, target.Repo, target.Number, token)
}

func (g *GitHubProvider) CheckPushAccess(ctx context.Context, target PRTarget, token string) bool {
	return checkPushAccess(ctx, target.Owner, target.Repo, token)
}

func (g *GitHubProvider) SubmitReview(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
	return submitReview(ctx, target.Owner, target.Repo, target.Number, token, headSHA, comments, event, body)
}

func (g *GitHubProvider) FetchComments(ctx context.Context, target PRTarget, token string) ([]PRComment, []PRComment, error) {
	return fetchComments(ctx, target.Owner, target.Repo, target.Number, token)
}

func (g *GitHubProvider) PostIssueComment(ctx context.Context, target PRTarget, token, body string) (PRComment, error) {
	return postIssueComment(ctx, target.Owner, target.Repo, target.Number, token, body)
}

func (g *GitHubProvider) ReplyToReviewComment(ctx context.Context, target PRTarget, token string, commentID int64, body string) (PRComment, error) {
	return replyToReviewComment(ctx, target.Owner, target.Repo, target.Number, token, commentID, body)
}

// resolveGitHubToken looks for a token in order: the explicit px0 setting
// (github.token), the GITHUB_TOKEN environment variable, GH_TOKEN, then the gh CLI
// if installed and logged in. An empty return means PR review stays read-only.
func resolveGitHubToken(cfg settings) (token, source string) {
	if cfg.GitHubToken != nil {
		if t := strings.TrimSpace(*cfg.GitHubToken); t != "" {
			return t, "settings"
		}
	}
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t, "env"
	}
	if t := strings.TrimSpace(os.Getenv("GH_TOKEN")); t != "" {
		return t, "env"
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t, "gh"
		}
	}
	return "", ""
}

// githubRequest issues an authenticated (if token != "") GitHub REST call.
// path is either relative ("/repos/...", resolved against githubAPIBase) or
// an absolute URL, so a paginated Link header's "next" URL can be passed
// straight through.
func githubRequest(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	url := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		url = githubAPIBase + path
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return githubHTTPClient.Do(req)
}

// checkPushAccess reports whether token has push access to owner/repo. Fails
// closed: any transport error, non-200, or missing field yields false, never
// true, so a broken check can only ever hide the write UI, not expose it.
func checkPushAccess(ctx context.Context, owner, repo, token string) bool {
	if token == "" {
		return false
	}
	resp, err := githubRequest(ctx, http.MethodGet, "/repos/"+owner+"/"+repo, token, nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var out struct {
		Permissions struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	return out.Permissions.Push
}

func fetchPRMeta(ctx context.Context, owner, repo string, num int, token string) (PRMeta, error) {
	resp, err := githubRequest(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, num), token, nil)
	if err != nil {
		return PRMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		bodyMsg := strings.TrimSpace(string(b))
		if token == "" && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) {
			return PRMeta{}, fmt.Errorf("github: fetch PR #%d: %s (no GitHub token found; for private repos or rate limits, set GITHUB_TOKEN or run 'gh auth login'): %s", num, resp.Status, bodyMsg)
		}
		return PRMeta{}, fmt.Errorf("github: fetch PR #%d: %s: %s", num, resp.Status, bodyMsg)
	}
	var out struct {
		Number   int    `json:"number"`
		Title    string `json:"title"`
		State    string `json:"state"`
		Merged   bool   `json:"merged"`
		MergedAt string `json:"merged_at"`
		Draft    bool   `json:"draft"`
		User     struct {
			Login string `json:"login"`
		} `json:"user"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				CloneURL string `json:"clone_url"`
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return PRMeta{}, err
	}
	m := PRMeta{
		Number:           out.Number,
		Title:            out.Title,
		Author:           out.User.Login,
		State:            out.State,
		Merged:           out.Merged || out.MergedAt != "",
		MergedAt:         out.MergedAt,
		Draft:            out.Draft,
		BaseRef:          out.Base.Ref,
		HeadRef:          out.Head.Ref,
		HeadSHA:          out.Head.SHA,
		HeadRepoCloneURL: out.Head.Repo.CloneURL,
	}
	m.HeadIsFork = out.Head.Repo.FullName != "" && !strings.EqualFold(out.Head.Repo.FullName, owner+"/"+repo)
	return m, nil
}

// prComment is a review comment held in memory only for the life of the
// process (pr.go's prSession) until submitReview posts it. Side matches
// GitHub's review-comment API: "LEFT" (the base) or "RIGHT" (the PR head).
type prComment struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// submitReview posts one review carrying every draft comment plus an overall
// verdict in a single call, mirroring GitHub's own draft-then-submit model
// so px0 never needs a per-comment endpoint.
func submitReview(ctx context.Context, owner, repo string, num int, token, commitID string, comments []prComment, event, body string) error {
	type reviewComment struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Side string `json:"side"`
		Body string `json:"body"`
	}
	payload := struct {
		CommitID string          `json:"commit_id,omitempty"`
		Body     string          `json:"body,omitempty"`
		Event    string          `json:"event"`
		Comments []reviewComment `json:"comments,omitempty"`
	}{CommitID: commitID, Body: body, Event: event}
	for _, c := range comments {
		payload.Comments = append(payload.Comments, reviewComment{Path: c.Path, Line: c.Line, Side: c.Side, Body: c.Body})
	}
	resp, err := githubRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", owner, repo, num), token, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github: submit review: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

var githubLinkNextRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// githubGetAllPages follows a GitHub list endpoint's Link header until
// exhausted, decoding each page as a raw JSON array so callers can unmarshal
// elements into their own shape.
func githubGetAllPages(ctx context.Context, path, token string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	next := path
	for next != "" {
		resp, err := githubRequest(ctx, http.MethodGet, next, token, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("github: get %s: %s: %s", path, resp.Status, strings.TrimSpace(string(b)))
		}
		var page []json.RawMessage
		err = json.NewDecoder(resp.Body).Decode(&page)
		next = ""
		if link := resp.Header.Get("Link"); link != "" {
			if m := githubLinkNextRe.FindStringSubmatch(link); m != nil {
				next = m[1]
			}
		}
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
	}
	return all, nil
}

type ghUser struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

type ghIssueComment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url"`
	User      ghUser `json:"user"`
}

func (c ghIssueComment) toPRComment() PRComment {
	return PRComment{
		ID: c.ID, Kind: "issue", Author: c.User.Login, AvatarURL: c.User.AvatarURL,
		Body: c.Body, CreatedAt: c.CreatedAt, URL: c.HTMLURL,
	}
}

type ghReviewComment struct {
	ID           int64  `json:"id"`
	Body         string `json:"body"`
	Path         string `json:"path"`
	Line         int    `json:"line"`
	OriginalLine int    `json:"original_line"`
	Side         string `json:"side"`
	InReplyToID  int64  `json:"in_reply_to_id"`
	CreatedAt    string `json:"created_at"`
	HTMLURL      string `json:"html_url"`
	User         ghUser `json:"user"`
}

func (c ghReviewComment) toPRComment() PRComment {
	// An outdated review comment (its line since edited elsewhere in the
	// diff) has line == null; original_line still says where it was.
	line := c.Line
	if line == 0 {
		line = c.OriginalLine
	}
	return PRComment{
		ID: c.ID, Kind: "review", Path: c.Path, Line: line, Side: c.Side, InReplyTo: c.InReplyToID,
		Author: c.User.Login, AvatarURL: c.User.AvatarURL, Body: c.Body, CreatedAt: c.CreatedAt, URL: c.HTMLURL,
	}
}

// fetchComments returns every comment already posted on the PR: top-level
// ("issue") comments from the issues API, and inline ("review") comments
// (which may be replies, linked via InReplyTo) from the pulls API.
func fetchComments(ctx context.Context, owner, repo string, num int, token string) (issue, review []PRComment, err error) {
	issueRaw, err := githubGetAllPages(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", owner, repo, num), token)
	if err != nil {
		return nil, nil, err
	}
	reviewRaw, err := githubGetAllPages(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments?per_page=100", owner, repo, num), token)
	if err != nil {
		return nil, nil, err
	}
	issue = make([]PRComment, 0, len(issueRaw))
	for _, raw := range issueRaw {
		var c ghIssueComment
		if err := json.Unmarshal(raw, &c); err == nil {
			issue = append(issue, c.toPRComment())
		}
	}
	review = make([]PRComment, 0, len(reviewRaw))
	for _, raw := range reviewRaw {
		var c ghReviewComment
		if err := json.Unmarshal(raw, &c); err == nil {
			review = append(review, c.toPRComment())
		}
	}
	return issue, review, nil
}

// postIssueComment posts a new top-level PR conversation comment. GitHub's
// issue comments have no reply/threading concept, so this is also how a
// "reply" to one is implemented: just a new comment.
func postIssueComment(ctx context.Context, owner, repo string, num int, token, body string) (PRComment, error) {
	resp, err := githubRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, num), token, map[string]string{"body": body})
	if err != nil {
		return PRComment{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return PRComment{}, fmt.Errorf("github: post comment: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var c ghIssueComment
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return PRComment{}, err
	}
	return c.toPRComment(), nil
}

// replyToReviewComment posts an immediate, properly threaded reply to an
// existing inline review comment via GitHub's dedicated replies endpoint.
func replyToReviewComment(ctx context.Context, owner, repo string, num int, token string, commentID int64, body string) (PRComment, error) {
	resp, err := githubRequest(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments/%d/replies", owner, repo, num, commentID), token, map[string]string{"body": body})
	if err != nil {
		return PRComment{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return PRComment{}, fmt.Errorf("github: reply to review comment: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var c ghReviewComment
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return PRComment{}, err
	}
	return c.toPRComment(), nil
}
