package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// bitbucket.go talks to the Bitbucket Cloud REST 2.0 API for PR review
// via the BitbucketProvider implementation of GitProvider.

const bitbucketAPIBase = "https://api.bitbucket.org/2.0"

var bitbucketHTTPClient = &http.Client{Timeout: 15 * time.Second}

var bitbucketPRURLRe = regexp.MustCompile(`^(?i)(?:https?://)?(?:www\.)?bitbucket\.org/([^/]+)/([^/]+)/pull-requests/(\d+)(?:[/?#].*)?$`)

// BitbucketProvider implements GitProvider for Bitbucket Cloud.
type BitbucketProvider struct{}

var _ GitProvider = (*BitbucketProvider)(nil)

func (b *BitbucketProvider) Name() string { return "bitbucket" }

func (b *BitbucketProvider) MatchURL(rawURL string) bool {
	return bitbucketPRURLRe.MatchString(strings.TrimSpace(rawURL))
}

func (b *BitbucketProvider) ParseURL(rawURL string) (PRTarget, error) {
	trimmed := strings.TrimSpace(rawURL)
	m := bitbucketPRURLRe.FindStringSubmatch(trimmed)
	if m == nil {
		return PRTarget{}, fmt.Errorf("invalid Bitbucket pull request URL: %q (expected format https://bitbucket.org/workspace/repo/pull-requests/123)", rawURL)
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return PRTarget{}, fmt.Errorf("invalid Bitbucket pull request number in %q: %w", rawURL, err)
	}
	return PRTarget{
		Provider: "bitbucket",
		Owner:    strings.ToLower(m[1]),
		Repo:     strings.ToLower(strings.TrimSuffix(m[2], ".git")),
		Number:   n,
		URL:      rawURL,
	}, nil
}

func (b *BitbucketProvider) ResolveToken(cfg settings) (token, source string) {
	return resolveBitbucketToken(cfg)
}

func (b *BitbucketProvider) SSHURL(target PRTarget) string {
	return fmt.Sprintf("git@bitbucket.org:%s/%s.git", target.Owner, target.Repo)
}

func (b *BitbucketProvider) TokenHint() string {
	return "set BITBUCKET_TOKEN"
}

func (b *BitbucketProvider) FetchPR(ctx context.Context, target PRTarget, token string) (PRMeta, error) {
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d", target.Owner, target.Repo, target.Number)
	resp, err := bitbucketRequest(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return PRMeta{}, fmt.Errorf("bitbucket: fetch PR #%d: %w", target.Number, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return PRMeta{}, bitbucketHTTPError(resp, fmt.Sprintf("fetch PR #%d", target.Number), token)
	}

	var out struct {
		ID        int    `json:"id"`
		Title     string `json:"title"`
		State     string `json:"state"`
		CreatedOn string `json:"created_on"`
		UpdatedOn string `json:"updated_on"`
		ClosedOn  string `json:"closed_on"`
		Draft     bool   `json:"draft"`
		IsDraft   bool   `json:"is_draft"`
		Author    struct {
			DisplayName string `json:"display_name"`
			Nickname    string `json:"nickname"`
			Username    string `json:"username"`
		} `json:"author"`
		Destination struct {
			Branch struct {
				Name string `json:"name"`
			} `json:"branch"`
			Commit struct {
				Hash string `json:"hash"`
			} `json:"commit"`
			Repository struct {
				FullName string `json:"full_name"`
				Name     string `json:"name"`
			} `json:"repository"`
		} `json:"destination"`
		Source struct {
			Branch struct {
				Name string `json:"name"`
			} `json:"branch"`
			Commit struct {
				Hash string `json:"hash"`
			} `json:"commit"`
			Repository struct {
				FullName string `json:"full_name"`
				Name     string `json:"name"`
				Links    struct {
					Clone []struct {
						Name string `json:"name"`
						HRef string `json:"href"`
					} `json:"clone"`
				} `json:"links"`
			} `json:"repository"`
		} `json:"source"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return PRMeta{}, fmt.Errorf("bitbucket: decode PR #%d: %w", target.Number, err)
	}

	author := out.Author.Nickname
	if author == "" {
		author = out.Author.DisplayName
	}
	if author == "" {
		author = out.Author.Username
	}

	var state string
	var merged bool
	switch strings.ToUpper(strings.TrimSpace(out.State)) {
	case "OPEN":
		state = "open"
		merged = false
	case "MERGED":
		state = "closed"
		merged = true
	case "DECLINED", "SUPERSEDED":
		state = "closed"
		merged = false
	default:
		state = strings.ToLower(out.State)
		merged = false
	}

	var mergedAt string
	if merged {
		mergedAt = out.ClosedOn
		if mergedAt == "" {
			mergedAt = out.UpdatedOn
		}
	}

	targetFullName := fmt.Sprintf("%s/%s", target.Owner, target.Repo)
	srcFullName := strings.TrimSpace(out.Source.Repository.FullName)
	headIsFork := srcFullName != "" && !strings.EqualFold(srcFullName, targetFullName)

	var cloneURL string
	for _, l := range out.Source.Repository.Links.Clone {
		if strings.EqualFold(l.Name, "ssh") && l.HRef != "" {
			cloneURL = l.HRef
			break
		}
	}
	if cloneURL == "" {
		repoPath := srcFullName
		if repoPath == "" {
			repoPath = targetFullName
		}
		cloneURL = fmt.Sprintf("git@bitbucket.org:%s.git", strings.TrimSuffix(repoPath, ".git"))
	}

	num := out.ID
	if num == 0 {
		num = target.Number
	}

	return PRMeta{
		Number:           num,
		Title:            out.Title,
		Author:           author,
		State:            state,
		Merged:           merged,
		MergedAt:         mergedAt,
		Draft:            out.Draft || out.IsDraft,
		BaseRef:          out.Destination.Branch.Name,
		HeadRef:          out.Source.Branch.Name,
		HeadSHA:          out.Source.Commit.Hash,
		HeadRepoCloneURL: cloneURL,
		HeadIsFork:       headIsFork,
	}, nil
}

func (b *BitbucketProvider) CheckPushAccess(ctx context.Context, target PRTarget, token string) bool {
	if token == "" {
		return false
	}

	targetFull := strings.ToLower(fmt.Sprintf("%s/%s", target.Owner, target.Repo))
	targetRepo := strings.ToLower(target.Repo)

	nextURL := fmt.Sprintf("/user/workspaces/%s/permissions/repositories?q=repository.slug=%q", target.Owner, target.Repo)
	// Try query filter first; if nothing returned or endpoint unsupported, try paginated workspace list
	for attempts := 0; attempts < 10 && nextURL != ""; attempts++ {
		resp, err := bitbucketRequest(ctx, http.MethodGet, nextURL, token, nil)
		if err != nil {
			// Fail closed: transport error means write access unknown.
			return false
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if attempts == 0 && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound) {
				nextURL = fmt.Sprintf("/user/workspaces/%s/permissions/repositories", target.Owner)
				continue
			}
			// Fail closed: HTTP error means write access unknown.
			return false
		}

		var out struct {
			Values []struct {
				Permission string `json:"permission"`
				Repository struct {
					FullName string `json:"full_name"`
					Name     string `json:"name"`
					Slug     string `json:"slug"`
				} `json:"repository"`
			} `json:"values"`
			Permission string `json:"permission"`
			Next       string `json:"next"`
		}
		err = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if err != nil {
			// Fail closed: decode error means write access unknown.
			return false
		}

		var matchedPerm string
		found := false
		for _, v := range out.Values {
			fn := strings.ToLower(v.Repository.FullName)
			nm := strings.ToLower(v.Repository.Name)
			sl := strings.ToLower(v.Repository.Slug)
			if fn == targetFull || nm == targetRepo || sl == targetRepo {
				matchedPerm = strings.ToLower(v.Permission)
				found = true
				break
			}
		}
		if !found && len(out.Values) == 1 && out.Values[0].Repository.FullName == "" && out.Values[0].Repository.Name == "" {
			matchedPerm = strings.ToLower(out.Values[0].Permission)
			found = true
		}
		if !found && out.Permission != "" {
			matchedPerm = strings.ToLower(out.Permission)
			found = true
		}

		if found {
			if matchedPerm == "write" || matchedPerm == "admin" {
				return true
			}
			return false
		}

		if attempts == 0 && strings.Contains(nextURL, "?q=") && len(out.Values) == 0 {
			nextURL = fmt.Sprintf("/user/workspaces/%s/permissions/repositories", target.Owner)
			continue
		}

		nextURL = out.Next
	}

	// Fail closed: permission not found in any response.
	return false
}

type bitbucketCommentItem struct {
	ID        int64  `json:"id"`
	Deleted   bool   `json:"deleted"`
	CreatedOn string `json:"created_on"`
	UpdatedOn string `json:"updated_on"`
	Content   struct {
		Raw  string `json:"raw"`
		HTML string `json:"html"`
	} `json:"content"`
	User struct {
		DisplayName string `json:"display_name"`
		Nickname    string `json:"nickname"`
		Username    string `json:"username"`
		Links       struct {
			Avatar struct {
				HRef string `json:"href"`
			} `json:"avatar"`
		} `json:"links"`
	} `json:"user"`
	Inline *struct {
		Path string `json:"path"`
		To   *int   `json:"to"`
		From *int   `json:"from"`
	} `json:"inline"`
	Parent *struct {
		ID int64 `json:"id"`
	} `json:"parent"`
	Links struct {
		HTML struct {
			HRef string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

func (c bitbucketCommentItem) toPRComment() PRComment {
	body := c.Content.Raw
	if body == "" && c.Content.HTML != "" {
		body = c.Content.HTML
	}
	createdAt := c.CreatedOn
	if createdAt == "" {
		createdAt = c.UpdatedOn
	}
	author := c.User.Nickname
	if author == "" {
		author = c.User.DisplayName
	}
	if author == "" {
		author = c.User.Username
	}
	prc := PRComment{
		ID:        c.ID,
		Author:    author,
		AvatarURL: c.User.Links.Avatar.HRef,
		Body:      body,
		CreatedAt: createdAt,
		URL:       c.Links.HTML.HRef,
	}
	if c.Parent != nil && c.Parent.ID != 0 {
		prc.InReplyTo = c.Parent.ID
	}
	if c.Inline != nil {
		prc.Kind = "review"
		prc.Path = c.Inline.Path
		if c.Inline.To != nil {
			prc.Side = "RIGHT"
			prc.Line = *c.Inline.To
		} else if c.Inline.From != nil {
			prc.Side = "LEFT"
			prc.Line = *c.Inline.From
		}
	} else if c.Parent != nil && c.Parent.ID != 0 {
		prc.Kind = "review"
	} else {
		prc.Kind = "issue"
	}
	return prc
}

func (b *BitbucketProvider) SubmitReview(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error {
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/comments", target.Owner, target.Repo, target.Number)
	var postedIDs []int64

	// Step 1: Inline drafts with KTD7 RIGHT->inline.to/LEFT->inline.from
	for _, c := range comments {
		inline := map[string]any{
			"path": c.Path,
		}
		if strings.EqualFold(c.Side, "LEFT") {
			inline["from"] = c.Line
		} else {
			inline["to"] = c.Line
		}
		payload := map[string]any{
			"content": map[string]string{
				"raw": c.Body,
			},
			"inline": inline,
		}
		resp, err := bitbucketRequest(ctx, http.MethodPost, path, token, payload)
		if err != nil {
			return &PartialSubmitError{
				PostedIDs: postedIDs,
				Step:      "draft",
				Err:       fmt.Errorf("bitbucket: post draft comment: %w", err),
			}
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			httpErr := bitbucketHTTPError(resp, fmt.Sprintf("post draft comment for PR #%d", target.Number), token)
			resp.Body.Close()
			return &PartialSubmitError{
				PostedIDs: postedIDs,
				Step:      "draft",
				Err:       httpErr,
			}
		}
		resp.Body.Close()
		postedIDs = append(postedIDs, c.ID)
	}

	// Step 2: Body as issue comment if non-empty
	if trimmedBody := strings.TrimSpace(body); trimmedBody != "" {
		if _, err := b.PostIssueComment(ctx, target, token, trimmedBody); err != nil {
			return &PartialSubmitError{
				PostedIDs: postedIDs,
				Step:      "body",
				Err:       err,
			}
		}
	}

	// Step 3: Verdict endpoint /approve or /request-changes if APPROVE/REQUEST_CHANGES
	eventUpper := strings.ToUpper(strings.TrimSpace(event))
	var verdictPath string
	switch eventUpper {
	case "APPROVE":
		verdictPath = fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/approve", target.Owner, target.Repo, target.Number)
	case "REQUEST_CHANGES":
		verdictPath = fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/request-changes", target.Owner, target.Repo, target.Number)
	case "COMMENT", "":
		return nil
	default:
		return &PartialSubmitError{
			PostedIDs: postedIDs,
			Step:      "verdict",
			Err:       fmt.Errorf("bitbucket: unsupported review event: %q", event),
		}
	}

	if verdictPath != "" {
		resp, err := bitbucketRequest(ctx, http.MethodPost, verdictPath, token, nil)
		if err != nil {
			return &PartialSubmitError{
				PostedIDs: postedIDs,
				Step:      "verdict",
				Err:       fmt.Errorf("bitbucket: submit verdict: %w", err),
			}
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
			httpErr := bitbucketHTTPError(resp, fmt.Sprintf("submit review verdict (%s) for PR #%d", strings.ToLower(eventUpper), target.Number), token)
			return &PartialSubmitError{
				PostedIDs: postedIDs,
				Step:      "verdict",
				Err:       httpErr,
			}
		}
	}

	return nil
}

func (b *BitbucketProvider) FetchComments(ctx context.Context, target PRTarget, token string) ([]PRComment, []PRComment, error) {
	nextURL := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/comments", target.Owner, target.Repo, target.Number)

	var all []bitbucketCommentItem
	for nextURL != "" {
		resp, err := bitbucketRequest(ctx, http.MethodGet, nextURL, token, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("bitbucket: fetch comments: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			err := bitbucketHTTPError(resp, fmt.Sprintf("fetch comments for PR #%d", target.Number), token)
			resp.Body.Close()
			return nil, nil, err
		}

		var page struct {
			Values []bitbucketCommentItem `json:"values"`
			Next   string                 `json:"next"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("bitbucket: decode comments: %w", err)
		}

		all = append(all, page.Values...)
		nextURL = page.Next
	}

	type inlinePos struct {
		Path string
		To   *int
		From *int
	}
	isReviewMap := make(map[int64]bool)
	inlineMap := make(map[int64]inlinePos)

	for _, c := range all {
		if c.Inline != nil {
			isReviewMap[c.ID] = true
			inlineMap[c.ID] = inlinePos{
				Path: c.Inline.Path,
				To:   c.Inline.To,
				From: c.Inline.From,
			}
		}
	}

	for i := 0; i < len(all); i++ {
		for _, c := range all {
			if !isReviewMap[c.ID] && c.Parent != nil && isReviewMap[c.Parent.ID] {
				isReviewMap[c.ID] = true
				if c.Inline == nil {
					inlineMap[c.ID] = inlineMap[c.Parent.ID]
				}
			}
		}
	}

	issue := make([]PRComment, 0)
	review := make([]PRComment, 0)

	for _, c := range all {
		if c.Deleted {
			continue
		}

		body := c.Content.Raw
		if body == "" && c.Content.HTML != "" {
			body = c.Content.HTML
		}

		createdAt := c.CreatedOn
		if createdAt == "" {
			createdAt = c.UpdatedOn
		}

		author := c.User.Nickname
		if author == "" {
			author = c.User.DisplayName
		}
		if author == "" {
			author = c.User.Username
		}

		prc := PRComment{
			ID:        c.ID,
			Author:    author,
			AvatarURL: c.User.Links.Avatar.HRef,
			Body:      body,
			CreatedAt: createdAt,
			URL:       c.Links.HTML.HRef,
		}

		if c.Parent != nil && c.Parent.ID != 0 {
			prc.InReplyTo = c.Parent.ID
		}

		var inlineData *inlinePos
		if c.Inline != nil {
			pos := inlinePos{
				Path: c.Inline.Path,
				To:   c.Inline.To,
				From: c.Inline.From,
			}
			inlineData = &pos
		} else if c.Parent != nil {
			if parentPos, ok := inlineMap[c.Parent.ID]; ok {
				inlineData = &parentPos
			}
		}

		if inlineData != nil || isReviewMap[c.ID] {
			prc.Kind = "review"
			if inlineData != nil {
				prc.Path = inlineData.Path
				if inlineData.To != nil {
					prc.Side = "RIGHT"
					prc.Line = *inlineData.To
				} else if inlineData.From != nil {
					prc.Side = "LEFT"
					prc.Line = *inlineData.From
				}
			}
			review = append(review, prc)
		} else {
			prc.Kind = "issue"
			issue = append(issue, prc)
		}
	}

	return issue, review, nil
}

func (b *BitbucketProvider) PostIssueComment(ctx context.Context, target PRTarget, token, body string) (PRComment, error) {
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/comments", target.Owner, target.Repo, target.Number)
	payload := map[string]any{
		"content": map[string]string{
			"raw": body,
		},
	}
	resp, err := bitbucketRequest(ctx, http.MethodPost, path, token, payload)
	if err != nil {
		return PRComment{}, fmt.Errorf("bitbucket: post comment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return PRComment{}, bitbucketHTTPError(resp, fmt.Sprintf("post comment on PR #%d", target.Number), token)
	}
	var c bitbucketCommentItem
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return PRComment{}, fmt.Errorf("bitbucket: decode comment response: %w", err)
	}
	prc := c.toPRComment()
	prc.Kind = "issue"
	return prc, nil
}

func (b *BitbucketProvider) ReplyToReviewComment(ctx context.Context, target PRTarget, token string, commentID int64, body string) (PRComment, error) {
	path := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/comments", target.Owner, target.Repo, target.Number)
	payload := map[string]any{
		"content": map[string]string{
			"raw": body,
		},
		"parent": map[string]int64{
			"id": commentID,
		},
	}
	resp, err := bitbucketRequest(ctx, http.MethodPost, path, token, payload)
	if err != nil {
		return PRComment{}, fmt.Errorf("bitbucket: reply to review comment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return PRComment{}, bitbucketHTTPError(resp, fmt.Sprintf("reply to comment #%d for PR #%d", commentID, target.Number), token)
	}
	var c bitbucketCommentItem
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return PRComment{}, fmt.Errorf("bitbucket: decode reply response: %w", err)
	}
	prc := c.toPRComment()
	prc.Kind = "review"
	if prc.InReplyTo == 0 {
		prc.InReplyTo = commentID
	}

	// Inherit path, line, and side from parent comment so UI threads it correctly
	parentPath := fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/comments/%d", target.Owner, target.Repo, target.Number, commentID)
	if parentResp, err := bitbucketRequest(ctx, http.MethodGet, parentPath, token, nil); err == nil {
		if parentResp.StatusCode == http.StatusOK {
			var parentComment bitbucketCommentItem
			if err := json.NewDecoder(parentResp.Body).Decode(&parentComment); err == nil && parentComment.Inline != nil {
				prc.Path = parentComment.Inline.Path
				if parentComment.Inline.To != nil {
					prc.Side = "RIGHT"
					prc.Line = *parentComment.Inline.To
				} else if parentComment.Inline.From != nil {
					prc.Side = "LEFT"
					prc.Line = *parentComment.Inline.From
				}
			}
		}
		parentResp.Body.Close()
	}

	return prc, nil
}

// resolveBitbucketToken looks for a token in order: the explicit px0 setting
// (bitbucket.token), then the BITBUCKET_TOKEN environment variable. An empty return
// means PR review stays read-only.
func resolveBitbucketToken(cfg settings) (token, source string) {
	if cfg.BitbucketToken != nil {
		if t := strings.TrimSpace(*cfg.BitbucketToken); t != "" {
			return t, "settings"
		}
	}
	if t := strings.TrimSpace(os.Getenv("BITBUCKET_TOKEN")); t != "" {
		return t, "env"
	}
	return "", ""
}

// bitbucketAuthHeader returns the Authorization header value for a Bitbucket token.
// Per KTD4, tokens containing ':' use HTTP Basic auth (username:app_password).
// Otherwise, tokens use HTTP Bearer auth.
func bitbucketAuthHeader(token string) string {
	if strings.Contains(token, ":") {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(token))
	}
	return "Bearer " + token
}

// redactBitbucketToken ensures tokens, passwords, and their base64 encodings
// never appear in logs or error messages.
func redactBitbucketToken(s, token string) string {
	if token == "" {
		return s
	}
	s = strings.ReplaceAll(s, token, "***")
	if strings.Contains(token, ":") {
		parts := strings.SplitN(token, ":", 2)
		if parts[1] != "" {
			s = strings.ReplaceAll(s, parts[1], "***")
		}
		b64 := base64.StdEncoding.EncodeToString([]byte(token))
		s = strings.ReplaceAll(s, b64, "***")
	}
	return s
}

// bitbucketRequest issues an authenticated Bitbucket REST API call.
// It applies Basic auth if the token contains ':', or Bearer auth otherwise.
// Transport errors are sanitized to prevent token leakage.
func bitbucketRequest(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	var reqURL string
	if strings.HasPrefix(path, "https://") {
		u, err := url.Parse(path)
		if err != nil || !strings.EqualFold(u.Hostname(), "api.bitbucket.org") {
			return nil, fmt.Errorf("bitbucket: invalid api URL host: %s", redactBitbucketToken(path, token))
		}
		reqURL = path
	} else if strings.HasPrefix(path, "/") {
		reqURL = bitbucketAPIBase + path
	} else {
		return nil, fmt.Errorf("bitbucket: invalid request path: %s", redactBitbucketToken(path, token))
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, rdr)
	if err != nil {
		return nil, errors.New(redactBitbucketToken(err.Error(), token))
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", bitbucketAuthHeader(token))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := bitbucketHTTPClient.Do(req)
	if err != nil {
		return nil, errors.New(redactBitbucketToken(err.Error(), token))
	}
	return resp, nil
}

// bitbucketHTTPError formats Bitbucket API error responses (specifically
// 401, 403, 404, and 429) as distinct, provider-named errors while guaranteeing
// tokens and credentials are never echoed.
func bitbucketHTTPError(resp *http.Response, action, token string) error {
	if resp == nil {
		return fmt.Errorf("bitbucket: %s: unknown error", action)
	}
	var bodyMsg string
	if resp.Body != nil {
		b, _ := io.ReadAll(resp.Body)
		bodyMsg = strings.TrimSpace(string(b))
	}
	if token != "" {
		bodyMsg = redactBitbucketToken(bodyMsg, token)
	}

	var detail string
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		detail = "401 unauthorized (invalid or missing Bitbucket token; " + (&BitbucketProvider{}).TokenHint() + ")"
	case http.StatusForbidden:
		detail = "403 forbidden (access denied or insufficient permissions)"
	case http.StatusNotFound:
		detail = "404 not found (repository or pull request not found, or token lacks access)"
	case http.StatusTooManyRequests:
		detail = "429 rate limit exceeded (too many requests to Bitbucket API)"
	default:
		detail = resp.Status
	}

	var errStr string
	if bodyMsg != "" {
		errStr = fmt.Sprintf("bitbucket: %s: %s: %s", action, detail, bodyMsg)
	} else {
		errStr = fmt.Sprintf("bitbucket: %s: %s", action, detail)
	}
	return errors.New(redactBitbucketToken(errStr, token))
}
