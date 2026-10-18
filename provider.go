package main

import (
	"context"
	"fmt"
	"strings"
)

// PRTarget identifies a pull request or merge request across any git forge.
type PRTarget struct {
	Provider string // "github", "gitea", etc.
	BaseURL  string // forge web root, e.g. "https://gitea.example.com" ("" means https://github.com)
	Owner    string // namespace/owner
	Repo     string // project/repo name
	Number   int    // PR/MR number
	URL      string // original URL
}

// WebBase returns the forge's web root with no trailing slash.
func (t PRTarget) WebBase() string {
	if t.BaseURL == "" {
		return "https://github.com"
	}
	return strings.TrimRight(t.BaseURL, "/")
}

// WebURL is the PR's canonical web page; both GitHub and Gitea serve its
// commit list at WebURL()+"/commits".
func (t PRTarget) WebURL() string {
	seg := "pull"
	if t.Provider == "gitea" {
		seg = "pulls"
	}
	return fmt.Sprintf("%s/%s/%s/%s/%d", t.WebBase(), t.Owner, t.Repo, seg, t.Number)
}

// RepoCloneURL is the https clone URL of the PR's base repository.
func (t PRTarget) RepoCloneURL() string {
	return fmt.Sprintf("%s/%s/%s.git", t.WebBase(), t.Owner, t.Repo)
}

// PRMeta holds the normalized metadata px0 needs to check out a PR,
// compute diffs, and label the review UI.
type PRMeta struct {
	Number           int
	Title            string
	Author           string
	State            string
	Merged           bool
	MergedAt         string
	Draft            bool
	BaseRef          string
	HeadRef          string
	HeadSHA          string
	HeadRepoCloneURL string
	HeadIsFork       bool
}

// PRComment is a comment already posted on the pull request, fetched
// read-only from the forge -- distinct from pr.go's prComment, which is a
// draft held in memory until a review is submitted. Kind is "issue" (a
// top-level PR conversation comment) or "review" (anchored to a diff line).
type PRComment struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Path      string `json:"path,omitempty"`
	Line      int    `json:"line,omitempty"`
	Side      string `json:"side,omitempty"`
	InReplyTo int64  `json:"inReplyTo,omitempty"`
	Author    string `json:"author"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	URL       string `json:"url"`
}

// GitProvider abstracts forge-specific operations (GitHub, GitLab, etc.)
// for pull/merge request reviews.
type GitProvider interface {
	// Name returns the provider name (e.g. "github", "gitea"). It also names
	// the provider's token setting: "<name>.token".
	Name() string

	// Label returns the forge's display name (e.g. "GitHub", "Gitea").
	Label() string

	// TokenHint tells the user how to give px0 a token for this forge.
	TokenHint() string

	// MatchURL reports whether this provider recognizes and handles the given URL.
	MatchURL(rawURL string) bool

	// ParseURL extracts the PR target from the URL.
	ParseURL(rawURL string) (PRTarget, error)

	// ResolveToken looks for an auth token for target's forge across settings,
	// env vars, and CLI tools. An empty return means the session stays read-only.
	ResolveToken(cfg settings, target PRTarget) (token, source string)

	// FetchPR fetches pull/merge request metadata from the forge API.
	FetchPR(ctx context.Context, target PRTarget, token string) (PRMeta, error)

	// CheckPushAccess reports whether the authenticated user has push access to the repository.
	CheckPushAccess(ctx context.Context, target PRTarget, token string) bool

	// SubmitReview posts draft comments and the overall review verdict back to the forge.
	SubmitReview(ctx context.Context, target PRTarget, token, headSHA string, comments []prComment, event, body string) error

	// FetchComments returns every comment already posted on the PR: top-level
	// ("issue") comments and inline ("review") comments anchored to a diff line.
	FetchComments(ctx context.Context, target PRTarget, token string) (issue, review []PRComment, err error)

	// PostIssueComment posts a new top-level PR comment immediately. Forges have
	// no threading for these, so "replying" to one is just posting a new one.
	PostIssueComment(ctx context.Context, target PRTarget, token, body string) (PRComment, error)

	// ReplyToReviewComment posts an immediate, threaded reply to an existing
	// inline review comment.
	ReplyToReviewComment(ctx context.Context, target PRTarget, token string, commentID int64, body string) (PRComment, error)
}

var defaultProviders = []GitProvider{
	&GitHubProvider{},
	&GiteaProvider{},
}

// RegisterProvider registers a custom or additional GitProvider.
func RegisterProvider(p GitProvider) {
	defaultProviders = append(defaultProviders, p)
}

// DetectPRURL checks if rawURL is a recognized pull request URL for any supported provider.
// If matched and valid, returns the matching provider, parsed target, and true.
func DetectPRURL(rawURL string) (GitProvider, PRTarget, bool) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, PRTarget{}, false
	}
	for _, p := range defaultProviders {
		if p.MatchURL(rawURL) {
			target, err := p.ParseURL(rawURL)
			if err == nil {
				return p, target, true
			}
		}
	}
	return nil, PRTarget{}, false
}

// ParsePRURL attempts to parse a PR URL, returning an error if no provider matches
// or if the URL format is invalid.
func ParsePRURL(rawURL string) (GitProvider, PRTarget, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, PRTarget{}, fmt.Errorf("empty PR URL")
	}
	for _, p := range defaultProviders {
		if p.MatchURL(rawURL) {
			target, err := p.ParseURL(rawURL)
			if err != nil {
				return nil, PRTarget{}, err
			}
			return p, target, nil
		}
	}
	return nil, PRTarget{}, fmt.Errorf("unsupported or unrecognized PR URL: %q (expected a full GitHub or Gitea URL like https://github.com/owner/repo/pull/123 or https://gitea.example.com/owner/repo/pulls/123)", rawURL)
}
