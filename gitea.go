package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// gitea.go talks to a Gitea (or Forgejo) instance's REST API (/api/v1) for PR
// review (pr.go) via the GiteaProvider implementation of GitProvider. Gitea is
// self-hosted, so the instance comes from the PR URL itself; the token comes
// from px0's settings, GITEA_TOKEN, or the tea CLI's login for that host. As in
// github.go: net/http plus a shell-out to tea, no SDK.

var giteaHTTPClient = &http.Client{Timeout: 15 * time.Second}

// giteaPRURLRe matches https://host[/subpath]/owner/repo/pulls/N. The scheme
// is required: without one, a relative local path like "a/b/pulls/1" would be
// taken for a PR.
var giteaPRURLRe = regexp.MustCompile(`^(https?://[^/?#]+(?:/[^?#]*?)?)/([^/?#]+)/([^/?#]+)/pulls/(\d+)(?:[/?#]|$)`)

// GiteaProvider implements GitProvider for Gitea and Forgejo instances.
type GiteaProvider struct{}

func (g *GiteaProvider) Name() string { return "gitea" }

func (g *GiteaProvider) Label() string { return "Gitea" }

func (g *GiteaProvider) TokenHint() string { return "set GITEA_TOKEN or run 'tea login add'" }

func (g *GiteaProvider) MatchURL(rawURL string) bool {
	m := giteaPRURLRe.FindStringSubmatch(strings.TrimSpace(rawURL))
	if m == nil {
		return false
	}
	// github.com/o/r/pulls is GitHub's PR list, never a Gitea PR.
	u, err := url.Parse(m[1])
	return err == nil && !strings.EqualFold(strings.TrimPrefix(u.Host, "www."), "github.com")
}

func (g *GiteaProvider) ParseURL(rawURL string) (PRTarget, error) {
	rawURL = strings.TrimSpace(rawURL)
	m := giteaPRURLRe.FindStringSubmatch(rawURL)
	if m == nil {
		return PRTarget{}, fmt.Errorf("invalid Gitea pull request URL: %q (expected format https://gitea.example.com/owner/repo/pulls/123)", rawURL)
	}
	n, _ := strconv.Atoi(m[4])
	return PRTarget{
		Provider: "gitea",
		BaseURL:  strings.TrimRight(m[1], "/"),
		Owner:    m[2],
		Repo:     strings.TrimSuffix(m[3], ".git"),
		Number:   n,
		URL:      rawURL,
	}, nil
}

func (g *GiteaProvider) ResolveToken(cfg settings, target PRTarget) (token, source string) {
	return resolveGiteaToken(cfg, target.WebBase())
}

func (g *GiteaProvider) FetchPR(ctx context.Context, target PRTarget, token string) (PRMeta, error) {
	return giteaFetchPRMeta(ctx, target, token)
}

func (g *GiteaProvider) CheckPushAccess(ctx context.Context, target PRTarget, token string) bool {
	return giteaCheckPushAccess(ctx, target, token)
}

func (g *GiteaProvider) SubmitReview(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
	_, err := giteaCreateReview(ctx, target, token, headSHA, comments, giteaReviewEvent(event), body)
	return err
}

func (g *GiteaProvider) FetchComments(ctx context.Context, target PRTarget, token string) ([]PRComment, []PRComment, error) {
	return giteaFetchComments(ctx, target, token)
}

func (g *GiteaProvider) PostIssueComment(ctx context.Context, target PRTarget, token, body string) (PRComment, error) {
	return giteaPostIssueComment(ctx, target, token, body)
}

func (g *GiteaProvider) ReplyToReviewComment(ctx context.Context, target PRTarget, token string, commentID int64, body string) (PRComment, error) {
	return giteaReplyToReviewComment(ctx, target, token, commentID, body)
}

// resolveGiteaToken looks for a token in order: the explicit px0 setting
// (gitea.token), the GITEA_TOKEN environment variable, then the tea CLI's
// login for base's host. An empty return means PR review stays read-only.
func resolveGiteaToken(cfg settings, base string) (token, source string) {
	if cfg.GiteaToken != nil {
		if t := strings.TrimSpace(*cfg.GiteaToken); t != "" {
			return t, "settings"
		}
	}
	if t := strings.TrimSpace(os.Getenv("GITEA_TOKEN")); t != "" {
		return t, "env"
	}
	if t := teaLoginToken(base); t != "" {
		return t, "tea"
	}
	return "", ""
}

// teaLoginToken asks tea's git credential helper (`tea login helper get`) for
// the token of the tea login whose URL is on base's host -- the same way tea
// hands its token to git, and so it finds tea's config wherever tea keeps it.
// "" when tea isn't installed or has no login for that host.
func teaLoginToken(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tea", "login", "helper", "get")
	cmd.Stdin = strings.NewReader("protocol=" + u.Scheme + "\nhost=" + u.Host + "\n\n")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "password="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// giteaRequest issues an authenticated (if token != "") Gitea REST call
// against base's /api/v1. path is either relative ("/repos/...") or an
// absolute URL, so a paginated Link header's "next" URL can be passed
// straight through.
func giteaRequest(ctx context.Context, method, base, path, token string, body any) (*http.Response, error) {
	u := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		u = strings.TrimRight(base, "/") + "/api/v1" + path
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return giteaHTTPClient.Do(req)
}

func giteaRepoPath(t PRTarget) string { return "/repos/" + t.Owner + "/" + t.Repo }

func giteaGetAllPages(ctx context.Context, target PRTarget, path, token string) ([]json.RawMessage, error) {
	return getAllPages("gitea", path, func(p string) (*http.Response, error) {
		return giteaRequest(ctx, http.MethodGet, target.WebBase(), p, token, nil)
	})
}

// giteaCheckPushAccess reports whether token has push access to the PR's
// repository. Fails closed, like checkPushAccess.
func giteaCheckPushAccess(ctx context.Context, target PRTarget, token string) bool {
	if token == "" {
		return false
	}
	resp, err := giteaRequest(ctx, http.MethodGet, target.WebBase(), giteaRepoPath(target), token, nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return decodePushPermission(resp.Body)
}

func giteaFetchPRMeta(ctx context.Context, target PRTarget, token string) (PRMeta, error) {
	resp, err := giteaRequest(ctx, http.MethodGet, target.WebBase(), fmt.Sprintf("%s/pulls/%d", giteaRepoPath(target), target.Number), token, nil)
	if err != nil {
		return PRMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		bodyMsg := strings.TrimSpace(string(b))
		if token == "" && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) {
			return PRMeta{}, fmt.Errorf("gitea: fetch PR #%d: %s (no Gitea token found for %s; set GITEA_TOKEN or run 'tea login add'): %s", target.Number, resp.Status, target.WebBase(), bodyMsg)
		}
		return PRMeta{}, fmt.Errorf("gitea: fetch PR #%d: %s: %s", target.Number, resp.Status, bodyMsg)
	}
	return decodePRMeta(resp.Body, target.Owner, target.Repo)
}

// giteaReviewEvent maps px0's GitHub-style verdicts to Gitea's review states,
// which spell approval "APPROVED".
func giteaReviewEvent(event string) string {
	if event == "APPROVE" {
		return "APPROVED"
	}
	return event
}

// giteaCreateReview posts one review carrying comments plus an overall
// verdict (a Gitea review state) and returns the new review's ID. Gitea
// addresses an inline comment by line number on one side: new_position for
// the head ("RIGHT"), old_position for the base ("LEFT").
func giteaCreateReview(ctx context.Context, target PRTarget, token, commitID string, comments []prComment, event, body string) (int64, error) {
	type reviewComment struct {
		Path        string `json:"path"`
		Body        string `json:"body"`
		NewPosition int    `json:"new_position,omitempty"`
		OldPosition int    `json:"old_position,omitempty"`
	}
	payload := struct {
		CommitID string          `json:"commit_id,omitempty"`
		Body     string          `json:"body,omitempty"`
		Event    string          `json:"event"`
		Comments []reviewComment `json:"comments,omitempty"`
	}{CommitID: commitID, Body: body, Event: event}
	for _, c := range comments {
		rc := reviewComment{Path: c.Path, Body: c.Body}
		if c.Side == "LEFT" {
			rc.OldPosition = c.Line
		} else {
			rc.NewPosition = c.Line
		}
		payload.Comments = append(payload.Comments, rc)
	}
	resp, err := giteaRequest(ctx, http.MethodPost, target.WebBase(), fmt.Sprintf("%s/pulls/%d/reviews", giteaRepoPath(target), target.Number), token, payload)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("gitea: submit review: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

type giteaReview struct {
	ID            int64  `json:"id"`
	State         string `json:"state"`
	CommentsCount int    `json:"comments_count"`
}

// giteaReviewComment is an inline comment as Gitea's review comments API
// returns it. Despite the names, position and original_position are line
// numbers, and only one is set: position on the head side, original_position
// on the base side.
type giteaReviewComment struct {
	ID               int64  `json:"id"`
	Body             string `json:"body"`
	Path             string `json:"path"`
	CommitID         string `json:"commit_id"`
	Position         int    `json:"position"`
	OriginalPosition int    `json:"original_position"`
	CreatedAt        string `json:"created_at"`
	HTMLURL          string `json:"html_url"`
	User             ghUser `json:"user"`
}

func (c giteaReviewComment) toPRComment() PRComment {
	line, side := c.Position, "RIGHT"
	if line == 0 {
		line, side = c.OriginalPosition, "LEFT"
	}
	return PRComment{
		ID: c.ID, Kind: "review", Path: c.Path, Line: line, Side: side,
		Author: c.User.Login, AvatarURL: c.User.AvatarURL, Body: c.Body, CreatedAt: c.CreatedAt, URL: c.HTMLURL,
	}
}

func decodeRaw[T any](raw []json.RawMessage) []T {
	out := make([]T, 0, len(raw))
	for _, r := range raw {
		var v T
		if err := json.Unmarshal(r, &v); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// giteaFetchReviewComments returns every inline comment on the PR's submitted
// reviews, oldest first. Gitea has no PR-wide endpoint for these, only one per
// review, so the reviews that have comments are fetched a few at a time.
// Pending reviews are someone's unsubmitted drafts and are skipped.
func giteaFetchReviewComments(ctx context.Context, target PRTarget, token string) ([]giteaReviewComment, error) {
	prPath := fmt.Sprintf("%s/pulls/%d", giteaRepoPath(target), target.Number)
	raw, err := giteaGetAllPages(ctx, target, prPath+"/reviews?limit=50", token)
	if err != nil {
		return nil, err
	}
	var reviews []giteaReview
	for _, r := range decodeRaw[giteaReview](raw) {
		if r.State != "PENDING" && r.CommentsCount > 0 {
			reviews = append(reviews, r)
		}
	}
	perReview := make([][]giteaReviewComment, len(reviews))
	errs := make([]error, len(reviews))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, r := range reviews {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			raw, err := giteaGetAllPages(ctx, target, fmt.Sprintf("%s/reviews/%d/comments", prPath, r.ID), token)
			perReview[i], errs[i] = decodeRaw[giteaReviewComment](raw), err
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var all []giteaReviewComment
	for _, cs := range perReview {
		all = append(all, cs...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all, nil
}

// giteaThreads converts review comments (oldest first) and links them into
// threads the way Gitea's UI shows conversations: every comment on the same
// path, side, and line is one thread, and each later one gets InReplyTo = the
// first one's ID -- which is also what GitHub's in_reply_to_id names.
func giteaThreads(cs []giteaReviewComment) []PRComment {
	type key struct {
		path, side string
		line       int
	}
	roots := map[key]int64{}
	out := make([]PRComment, 0, len(cs))
	for _, c := range cs {
		pc := c.toPRComment()
		k := key{pc.Path, pc.Side, pc.Line}
		if root, ok := roots[k]; ok {
			pc.InReplyTo = root
		} else {
			roots[k] = pc.ID
		}
		out = append(out, pc)
	}
	return out
}

func giteaFetchComments(ctx context.Context, target PRTarget, token string) (issue, review []PRComment, err error) {
	issueRaw, err := giteaGetAllPages(ctx, target, fmt.Sprintf("%s/issues/%d/comments", giteaRepoPath(target), target.Number), token)
	if err != nil {
		return nil, nil, err
	}
	reviewComments, err := giteaFetchReviewComments(ctx, target, token)
	if err != nil {
		return nil, nil, err
	}
	issue = make([]PRComment, 0, len(issueRaw))
	for _, c := range decodeRaw[ghIssueComment](issueRaw) {
		issue = append(issue, c.toPRComment())
	}
	return issue, giteaThreads(reviewComments), nil
}

func giteaPostIssueComment(ctx context.Context, target PRTarget, token, body string) (PRComment, error) {
	resp, err := giteaRequest(ctx, http.MethodPost, target.WebBase(), fmt.Sprintf("%s/issues/%d/comments", giteaRepoPath(target), target.Number), token, map[string]string{"body": body})
	if err != nil {
		return PRComment{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return PRComment{}, fmt.Errorf("gitea: post comment: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var c ghIssueComment
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return PRComment{}, err
	}
	return c.toPRComment(), nil
}

// giteaReplyToReviewComment answers an inline review comment. Gitea's API has
// no reply endpoint (its web UI posts a form instead), so this posts what that
// form amounts to: a COMMENT review carrying one comment on the same path,
// side, line, and commit, which Gitea shows in the same conversation since it
// groups conversations by line.
func giteaReplyToReviewComment(ctx context.Context, target PRTarget, token string, commentID int64, body string) (PRComment, error) {
	all, err := giteaFetchReviewComments(ctx, target, token)
	if err != nil {
		return PRComment{}, err
	}
	threads := giteaThreads(all)
	var orig giteaReviewComment
	var root int64
	for i, c := range all {
		if c.ID == commentID {
			orig, root = c, threads[i].InReplyTo
			if root == 0 {
				root = c.ID
			}
			break
		}
	}
	if root == 0 {
		return PRComment{}, fmt.Errorf("gitea: review comment %d not found on PR #%d", commentID, target.Number)
	}
	pc := orig.toPRComment()
	reviewID, err := giteaCreateReview(ctx, target, token, orig.CommitID,
		[]prComment{{Path: pc.Path, Line: pc.Line, Side: pc.Side, Body: body}}, "COMMENT", "")
	if err != nil {
		return PRComment{}, err
	}
	raw, err := giteaGetAllPages(ctx, target, fmt.Sprintf("%s/pulls/%d/reviews/%d/comments", giteaRepoPath(target), target.Number, reviewID), token)
	if err != nil {
		return PRComment{}, err
	}
	posted := decodeRaw[giteaReviewComment](raw)
	if len(posted) == 0 {
		return PRComment{}, fmt.Errorf("gitea: reply posted as review %d, but it has no comment", reviewID)
	}
	reply := posted[len(posted)-1].toPRComment()
	reply.InReplyTo = root
	return reply, nil
}
