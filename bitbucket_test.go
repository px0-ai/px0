package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBitbucketMatchAndParseURL(t *testing.T) {
	p := &BitbucketProvider{}

	cases := []struct {
		name      string
		rawURL    string
		wantOwner string
		wantRepo  string
		wantNum   int
	}{
		{
			name:      "plain",
			rawURL:    "bitbucket.org/workspace/repo/pull-requests/12",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "http",
			rawURL:    "http://bitbucket.org/workspace/repo/pull-requests/12",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "https",
			rawURL:    "https://bitbucket.org/workspace/repo/pull-requests/12",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "www plain",
			rawURL:    "www.bitbucket.org/workspace/repo/pull-requests/12",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "www http",
			rawURL:    "http://www.bitbucket.org/workspace/repo/pull-requests/12",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "www https",
			rawURL:    "https://www.bitbucket.org/workspace/repo/pull-requests/12",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "diff suffix",
			rawURL:    "https://bitbucket.org/workspace/repo/pull-requests/12/diff",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "overview suffix",
			rawURL:    "https://bitbucket.org/workspace/repo/pull-requests/12/overview",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "query param ?w=1",
			rawURL:    "https://bitbucket.org/workspace/repo/pull-requests/12?w=1",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "diff with query param",
			rawURL:    "https://bitbucket.org/workspace/repo/pull-requests/12/diff?w=1",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "fragment #comment-1",
			rawURL:    "https://bitbucket.org/workspace/repo/pull-requests/12#comment-1",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "diff query and fragment",
			rawURL:    "https://bitbucket.org/workspace/repo/pull-requests/12/diff?w=1#comment-1",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   12,
		},
		{
			name:      "mixed-case slugs normalized to lowercase",
			rawURL:    "https://bitbucket.org/WorkSpace/MyRepo/pull-requests/12",
			wantOwner: "workspace",
			wantRepo:  "myrepo",
			wantNum:   12,
		},
		{
			name:      "repo ending with .git",
			rawURL:    "https://bitbucket.org/workspace/repo.git/pull-requests/42",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   42,
		},
		{
			name:      "leading and trailing whitespace",
			rawURL:    "   https://bitbucket.org/workspace/repo/pull-requests/99   ",
			wantOwner: "workspace",
			wantRepo:  "repo",
			wantNum:   99,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !p.MatchURL(tc.rawURL) {
				t.Fatalf("MatchURL(%q) = false, want true", tc.rawURL)
			}
			target, err := p.ParseURL(tc.rawURL)
			if err != nil {
				t.Fatalf("ParseURL(%q) error: %v", tc.rawURL, err)
			}
			if target.Provider != "bitbucket" {
				t.Errorf("Provider = %q, want bitbucket", target.Provider)
			}
			if target.Owner != tc.wantOwner {
				t.Errorf("Owner = %q, want %q", target.Owner, tc.wantOwner)
			}
			if target.Repo != tc.wantRepo {
				t.Errorf("Repo = %q, want %q", target.Repo, tc.wantRepo)
			}
			if target.Number != tc.wantNum {
				t.Errorf("Number = %d, want %d", target.Number, tc.wantNum)
			}
			if target.URL != tc.rawURL {
				t.Errorf("URL = %q, want %q", target.URL, tc.rawURL)
			}
		})
	}
}

func TestBitbucketParseURLRejections(t *testing.T) {
	p := &BitbucketProvider{}

	rejections := []struct {
		name   string
		rawURL string
	}{
		{name: "missing number without slash", rawURL: "https://bitbucket.org/workspace/repo/pull-requests"},
		{name: "missing number with slash", rawURL: "https://bitbucket.org/workspace/repo/pull-requests/"},
		{name: "non-numeric number", rawURL: "https://bitbucket.org/workspace/repo/pull-requests/abc"},
		{name: "alphanumeric suffix on number", rawURL: "https://bitbucket.org/workspace/repo/pull-requests/12abc"},
		{name: "github pull request URL", rawURL: "https://github.com/owner/repo/pull/123"},
		{name: "gitlab merge request URL", rawURL: "https://gitlab.com/owner/repo/-/merge_requests/123"},
		{name: "missing repository", rawURL: "https://bitbucket.org/workspace/pull-requests/123"},
		{name: "empty workspace", rawURL: "https://bitbucket.org//repo/pull-requests/123"},
		{name: "empty string", rawURL: ""},
		{name: "arbitrary string", rawURL: "not a valid pr url"},
		{name: "bitbucket commit URL", rawURL: "https://bitbucket.org/workspace/repo/commits/abc123"},
	}

	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			if p.MatchURL(tc.rawURL) {
				t.Errorf("MatchURL(%q) = true, want false", tc.rawURL)
			}
			_, err := p.ParseURL(tc.rawURL)
			if err == nil {
				t.Errorf("ParseURL(%q) succeeded, want error", tc.rawURL)
			}
		})
	}
}

func TestBitbucketResolveTokenPrecedence(t *testing.T) {
	p := &BitbucketProvider{}

	// Settings takes precedence over BITBUCKET_TOKEN env var
	t.Setenv("BITBUCKET_TOKEN", "env-secret-token")
	settingsVal := "settings-secret-token"
	token, source := p.ResolveToken(settings{BitbucketToken: &settingsVal})
	if token != "settings-secret-token" || source != "settings" {
		t.Errorf("expected (settings-secret-token, settings), got (%q, %q)", token, source)
	}

	// Falls through to BITBUCKET_TOKEN env var when settings unset (nil)
	token, source = p.ResolveToken(settings{BitbucketToken: nil})
	if token != "env-secret-token" || source != "env" {
		t.Errorf("expected (env-secret-token, env), got (%q, %q)", token, source)
	}

	// Falls through to BITBUCKET_TOKEN env var when settings is blank whitespace
	blankVal := "   "
	token, source = p.ResolveToken(settings{BitbucketToken: &blankVal})
	if token != "env-secret-token" || source != "env" {
		t.Errorf("expected (env-secret-token, env) for blank settings, got (%q, %q)", token, source)
	}

	// Returns empty when both are unset
	t.Setenv("BITBUCKET_TOKEN", "")
	token, source = p.ResolveToken(settings{})
	if token != "" || source != "" {
		t.Errorf("expected (\"\", \"\") when unset, got (%q, %q)", token, source)
	}

	// Returns empty when both are blank
	t.Setenv("BITBUCKET_TOKEN", "   ")
	token, source = p.ResolveToken(settings{BitbucketToken: &blankVal})
	if token != "" || source != "" {
		t.Errorf("expected (\"\", \"\") when both blank, got (%q, %q)", token, source)
	}
}

func TestBitbucketAuthHeader(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "basic auth with username:app_password",
			token: "myuser:app_password_secret",
			want:  "Basic " + base64.StdEncoding.EncodeToString([]byte("myuser:app_password_secret")),
		},
		{
			name:  "basic auth with multiple colons",
			token: "user:pass:extra",
			want:  "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass:extra")),
		},
		{
			name:  "bearer auth without colon",
			token: "oauth2_access_token_12345",
			want:  "Bearer oauth2_access_token_12345",
		},
		{
			name:  "bearer auth alphanumeric with special chars",
			token: "bb.token.xyz-987_abc",
			want:  "Bearer bb.token.xyz-987_abc",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bitbucketAuthHeader(tc.token)
			if got != tc.want {
				t.Errorf("bitbucketAuthHeader(%q) = %q, want %q", tc.token, got, tc.want)
			}
		})
	}
}

func TestBitbucketTransportErrorRedaction(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	// Test 1: Bearer token in transport error
	bearerToken := "super_secret_bearer_token_xyz"
	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection failed for %s on host", bearerToken)
	})

	ctx := context.Background()
	_, err := bitbucketRequest(ctx, http.MethodGet, "/test", bearerToken, nil)
	if err == nil {
		t.Fatal("expected bitbucketRequest to return error")
	}
	if strings.Contains(err.Error(), bearerToken) {
		t.Errorf("transport error leaked bearer token: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("transport error should contain *** redaction: %v", err)
	}

	// Test 2: Basic auth user:pass token in transport error
	userPassToken := "myuser:secret_app_pw_999"
	b64Token := base64.StdEncoding.EncodeToString([]byte(userPassToken))
	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial failed with auth header Basic %s and password secret_app_pw_999", b64Token)
	})

	_, err = bitbucketRequest(ctx, http.MethodGet, "/test", userPassToken, nil)
	if err == nil {
		t.Fatal("expected bitbucketRequest to return error")
	}
	if strings.Contains(err.Error(), userPassToken) {
		t.Errorf("transport error leaked full userPassToken: %v", err)
	}
	if strings.Contains(err.Error(), "secret_app_pw_999") {
		t.Errorf("transport error leaked password part: %v", err)
	}
	if strings.Contains(err.Error(), b64Token) {
		t.Errorf("transport error leaked base64 encoded token: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("transport error should contain *** redaction: %v", err)
	}

	// Test 3: Direct redaction helper tests
	if got := redactBitbucketToken("", "token"); got != "" {
		t.Errorf("redactBitbucketToken(\"\", token) = %q, want \"\"", got)
	}
	msg := "failed with token secret123"
	if got := redactBitbucketToken(msg, "secret123"); strings.Contains(got, "secret123") || !strings.Contains(got, "***") {
		t.Errorf("redactBitbucketToken failed: %q", got)
	}
}

func TestBitbucketSettingsSchema(t *testing.T) {
	var found *settingSchemaItem
	for i := range settingsSchema {
		if settingsSchema[i].Key == "bitbucket.token" {
			found = &settingsSchema[i]
			break
		}
	}

	if found == nil {
		t.Fatal("bitbucket.token setting not found in settingsSchema")
	}
	if !found.Secret {
		t.Errorf("bitbucket.token Secret = false, want true")
	}
	if found.Category != "Bitbucket" {
		t.Errorf("bitbucket.token Category = %q, want Bitbucket", found.Category)
	}
	if found.Type != "string" {
		t.Errorf("bitbucket.token Type = %q, want string", found.Type)
	}
}

func TestBitbucketProviderInterface(t *testing.T) {
	var p GitProvider = &BitbucketProvider{}

	if p.Name() != "bitbucket" {
		t.Errorf("p.Name() = %q, want bitbucket", p.Name())
	}

	ssh := p.SSHURL(PRTarget{Owner: "myws", Repo: "myrepo"})
	if ssh != "git@bitbucket.org:myws/myrepo.git" {
		t.Errorf("p.SSHURL() = %q, want git@bitbucket.org:myws/myrepo.git", ssh)
	}

	hint := p.TokenHint()
	if !strings.Contains(hint, "BITBUCKET_TOKEN") {
		t.Errorf("TokenHint() = %q, expected mention of BITBUCKET_TOKEN", hint)
	}

	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 1}

	// Empty token fails closed
	if p.CheckPushAccess(ctx, target, "") {
		t.Error("CheckPushAccess with empty token should return false")
	}

	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()
	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(strings.NewReader(`{"error": {"message": "not found"}}`)),
			Header:     make(http.Header),
		}, nil
	})

	if _, err := p.FetchPR(ctx, target, "dummy"); err == nil {
		t.Error("FetchPR on 404 should return error")
	}

	if _, _, err := p.FetchComments(ctx, target, "dummy"); err == nil {
		t.Error("FetchComments on 404 should return error")
	}

	if err := p.SubmitReview(ctx, target, "dummy", "headsha", nil, "APPROVE", "body"); err == nil {
		t.Error("stub SubmitReview should return error")
	}

	if _, err := p.PostIssueComment(ctx, target, "dummy", "body"); err == nil {
		t.Error("stub PostIssueComment should return error")
	}

	if _, err := p.ReplyToReviewComment(ctx, target, "dummy", 1, "body"); err == nil {
		t.Error("stub ReplyToReviewComment should return error")
	}
}

func TestBitbucketInDefaultProviders(t *testing.T) {
	var found bool
	for _, p := range defaultProviders {
		if p.Name() == "bitbucket" {
			found = true
			break
		}
	}
	if !found {
		t.Error("BitbucketProvider is not registered in defaultProviders")
	}
}

func TestBitbucketParsePRURLIntegration(t *testing.T) {
	rawURL := "https://bitbucket.org/blgtech/hrms/pull-requests/371"

	p, target, ok := DetectPRURL(rawURL)
	if !ok {
		t.Fatalf("DetectPRURL(%q) ok = false, want true", rawURL)
	}
	if p.Name() != "bitbucket" {
		t.Errorf("provider name = %q, want bitbucket", p.Name())
	}
	if target.Owner != "blgtech" || target.Repo != "hrms" || target.Number != 371 {
		t.Errorf("DetectPRURL parsed target = %+v, want blgtech/hrms#371", target)
	}

	p2, target2, err := ParsePRURL(rawURL)
	if err != nil {
		t.Fatalf("ParsePRURL(%q) error: %v", rawURL, err)
	}
	if p2.Name() != "bitbucket" {
		t.Errorf("provider name = %q, want bitbucket", p2.Name())
	}
	if target2.Owner != "blgtech" || target2.Repo != "hrms" || target2.Number != 371 {
		t.Errorf("ParsePRURL parsed target = %+v, want blgtech/hrms#371", target2)
	}
}

func TestBitbucketFetchPR(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 10}

	tests := []struct {
		name             string
		status           int
		responseJSON     string
		wantNum          int
		wantTitle        string
		wantAuthor       string
		wantState        string
		wantMerged       bool
		wantBaseRef      string
		wantHeadRef      string
		wantHeadSHA      string
		wantCloneURL     string
		wantHeadIsFork   bool
		wantDraft        bool
		wantErrSubstring string
	}{
		{
			name:   "open PR with ssh clone link and nickname",
			status: http.StatusOK,
			responseJSON: `{
				"id": 10,
				"title": "Add feature X",
				"state": "OPEN",
				"draft": false,
				"author": {
					"nickname": "alice",
					"display_name": "Alice Smith"
				},
				"destination": {
					"branch": { "name": "main" }
				},
				"source": {
					"branch": { "name": "feat-x" },
					"commit": { "hash": "abc12345" },
					"repository": {
						"full_name": "myws/myrepo",
						"links": {
							"clone": [
								{ "name": "https", "href": "https://bitbucket.org/myws/myrepo.git" },
								{ "name": "ssh", "href": "git@bitbucket.org:myws/myrepo.git" }
							]
						}
					}
				}
			}`,
			wantNum:        10,
			wantTitle:      "Add feature X",
			wantAuthor:     "alice",
			wantState:      "open",
			wantMerged:     false,
			wantBaseRef:    "main",
			wantHeadRef:    "feat-x",
			wantHeadSHA:    "abc12345",
			wantCloneURL:   "git@bitbucket.org:myws/myrepo.git",
			wantHeadIsFork: false,
			wantDraft:      false,
		},
		{
			name:   "merged PR maps to state closed and merged true",
			status: http.StatusOK,
			responseJSON: `{
				"id": 10,
				"title": "Merged PR",
				"state": "MERGED",
				"closed_on": "2026-10-01T12:00:00Z",
				"author": { "display_name": "Bob" },
				"destination": { "branch": { "name": "main" } },
				"source": {
					"branch": { "name": "fix-1" },
					"commit": { "hash": "fff999" },
					"repository": { "full_name": "myws/myrepo" }
				}
			}`,
			wantNum:        10,
			wantTitle:      "Merged PR",
			wantAuthor:     "Bob",
			wantState:      "closed",
			wantMerged:     true,
			wantBaseRef:    "main",
			wantHeadRef:    "fix-1",
			wantHeadSHA:    "fff999",
			wantCloneURL:   "git@bitbucket.org:myws/myrepo.git",
			wantHeadIsFork: false,
		},
		{
			name:   "declined PR maps to state closed and merged false",
			status: http.StatusOK,
			responseJSON: `{
				"id": 10,
				"title": "Declined PR",
				"state": "DECLINED",
				"author": { "nickname": "charlie" },
				"destination": { "branch": { "name": "main" } },
				"source": {
					"branch": { "name": "wontfix" },
					"commit": { "hash": "111222" },
					"repository": { "full_name": "myws/myrepo" }
				}
			}`,
			wantNum:        10,
			wantTitle:      "Declined PR",
			wantAuthor:     "charlie",
			wantState:      "closed",
			wantMerged:     false,
			wantBaseRef:    "main",
			wantHeadRef:    "wontfix",
			wantHeadSHA:    "111222",
			wantCloneURL:   "git@bitbucket.org:myws/myrepo.git",
			wantHeadIsFork: false,
		},
		{
			name:   "superseded PR maps to state closed and merged false",
			status: http.StatusOK,
			responseJSON: `{
				"id": 10,
				"title": "Superseded PR",
				"state": "SUPERSEDED",
				"author": { "nickname": "david" },
				"destination": { "branch": { "name": "main" } },
				"source": {
					"branch": { "name": "old-approach" },
					"commit": { "hash": "333444" },
					"repository": { "full_name": "myws/myrepo" }
				}
			}`,
			wantNum:        10,
			wantTitle:      "Superseded PR",
			wantAuthor:     "david",
			wantState:      "closed",
			wantMerged:     false,
			wantBaseRef:    "main",
			wantHeadRef:    "old-approach",
			wantHeadSHA:    "333444",
			wantCloneURL:   "git@bitbucket.org:myws/myrepo.git",
			wantHeadIsFork: false,
		},
		{
			name:           "fork PR sets HeadIsFork true and uses fork SSH clone link",
			status:         http.StatusOK,
			responseJSON:   `{"id": 10, "title": "Fork PR", "state": "OPEN", "draft": true, "author": {"nickname": "eve"}, "destination": {"branch": {"name": "main"}}, "source": {"branch": {"name": "fork-feature"}, "commit": {"hash": "fork123"}, "repository": {"full_name": "forkws/myrepo", "links": {"clone": [{"name": "ssh", "href": "git@bitbucket.org:forkws/myrepo.git"}]}}}}`,
			wantNum:        10,
			wantTitle:      "Fork PR",
			wantAuthor:     "eve",
			wantState:      "open",
			wantMerged:     false,
			wantBaseRef:    "main",
			wantHeadRef:    "fork-feature",
			wantHeadSHA:    "fork123",
			wantCloneURL:   "git@bitbucket.org:forkws/myrepo.git",
			wantHeadIsFork: true,
			wantDraft:      true,
		},
		{
			name:   "no clone links falls back to git@bitbucket.org:{workspace}/{repo}.git",
			status: http.StatusOK,
			responseJSON: `{
				"id": 10,
				"title": "Fallback URL PR",
				"state": "OPEN",
				"author": { "nickname": "frank" },
				"destination": { "branch": { "name": "main" } },
				"source": {
					"branch": { "name": "patch-1" },
					"commit": { "hash": "999888" },
					"repository": {
						"full_name": "myws/myrepo",
						"links": {
							"clone": []
						}
					}
				}
			}`,
			wantNum:        10,
			wantTitle:      "Fallback URL PR",
			wantAuthor:     "frank",
			wantState:      "open",
			wantMerged:     false,
			wantBaseRef:    "main",
			wantHeadRef:    "patch-1",
			wantHeadSHA:    "999888",
			wantCloneURL:   "git@bitbucket.org:myws/myrepo.git",
			wantHeadIsFork: false,
		},
		{
			name:   "fork with no clone links falls back to fork SSH url",
			status: http.StatusOK,
			responseJSON: `{
				"id": 10,
				"title": "Fork Fallback URL PR",
				"state": "OPEN",
				"author": { "nickname": "grace" },
				"destination": { "branch": { "name": "main" } },
				"source": {
					"branch": { "name": "fork-patch" },
					"commit": { "hash": "444555" },
					"repository": {
						"full_name": "otherws/forkrepo"
					}
				}
			}`,
			wantNum:        10,
			wantTitle:      "Fork Fallback URL PR",
			wantAuthor:     "grace",
			wantState:      "open",
			wantMerged:     false,
			wantBaseRef:    "main",
			wantHeadRef:    "fork-patch",
			wantHeadSHA:    "444555",
			wantCloneURL:   "git@bitbucket.org:otherws/forkrepo.git",
			wantHeadIsFork: true,
		},
		{
			name:             "404 PR not found returns provider error",
			status:           http.StatusNotFound,
			responseJSON:     `{"error": {"message": "Pull request not found"}}`,
			wantErrSubstring: "404 not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				expectedPath := "/2.0/repositories/myws/myrepo/pullrequests/10"
				if req.URL.Path != expectedPath {
					t.Errorf("FetchPR request path = %q, want %q", req.URL.Path, expectedPath)
				}
				return &http.Response{
					StatusCode: tc.status,
					Status:     fmt.Sprintf("%d Status", tc.status),
					Body:       io.NopCloser(strings.NewReader(tc.responseJSON)),
					Header:     make(http.Header),
				}, nil
			})

			meta, err := p.FetchPR(ctx, target, "test-token")
			if tc.wantErrSubstring != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSubstring)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstring) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSubstring)
				}
				if !strings.HasPrefix(err.Error(), "bitbucket: ") {
					t.Errorf("error %q should start with bitbucket: prefix", err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if meta.Number != tc.wantNum {
				t.Errorf("Number = %d, want %d", meta.Number, tc.wantNum)
			}
			if meta.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", meta.Title, tc.wantTitle)
			}
			if meta.Author != tc.wantAuthor {
				t.Errorf("Author = %q, want %q", meta.Author, tc.wantAuthor)
			}
			if meta.State != tc.wantState {
				t.Errorf("State = %q, want %q", meta.State, tc.wantState)
			}
			if meta.Merged != tc.wantMerged {
				t.Errorf("Merged = %v, want %v", meta.Merged, tc.wantMerged)
			}
			if meta.BaseRef != tc.wantBaseRef {
				t.Errorf("BaseRef = %q, want %q", meta.BaseRef, tc.wantBaseRef)
			}
			if meta.HeadRef != tc.wantHeadRef {
				t.Errorf("HeadRef = %q, want %q", meta.HeadRef, tc.wantHeadRef)
			}
			if meta.HeadSHA != tc.wantHeadSHA {
				t.Errorf("HeadSHA = %q, want %q", meta.HeadSHA, tc.wantHeadSHA)
			}
			if meta.HeadRepoCloneURL != tc.wantCloneURL {
				t.Errorf("HeadRepoCloneURL = %q, want %q", meta.HeadRepoCloneURL, tc.wantCloneURL)
			}
			if meta.HeadIsFork != tc.wantHeadIsFork {
				t.Errorf("HeadIsFork = %v, want %v", meta.HeadIsFork, tc.wantHeadIsFork)
			}
			if meta.Draft != tc.wantDraft {
				t.Errorf("Draft = %v, want %v", meta.Draft, tc.wantDraft)
			}
		})
	}
}

func TestBitbucketCheckPushAccess(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 1}

	// 1. Empty token fails closed
	if p.CheckPushAccess(ctx, target, "") {
		t.Error("CheckPushAccess with empty token must return false")
	}

	tests := []struct {
		name       string
		statusCode int
		body       string
		netErr     error
		wantResult bool
	}{
		{
			name:       "write permission returns true",
			statusCode: http.StatusOK,
			body: `{
				"values": [
					{
						"permission": "write",
						"repository": { "full_name": "myws/myrepo", "name": "myrepo" }
					}
				]
			}`,
			wantResult: true,
		},
		{
			name:       "admin permission returns true",
			statusCode: http.StatusOK,
			body: `{
				"values": [
					{
						"permission": "admin",
						"repository": { "full_name": "myws/myrepo", "name": "myrepo" }
					}
				]
			}`,
			wantResult: true,
		},
		{
			name:       "read permission returns false",
			statusCode: http.StatusOK,
			body: `{
				"values": [
					{
						"permission": "read",
						"repository": { "full_name": "myws/myrepo", "name": "myrepo" }
					}
				]
			}`,
			wantResult: false,
		},
		{
			name:       "repo not in permissions list returns false (fail closed)",
			statusCode: http.StatusOK,
			body: `{
				"values": [
					{
						"permission": "write",
						"repository": { "full_name": "myws/otherrepo", "name": "otherrepo" }
					}
				]
			}`,
			wantResult: false,
		},
		{
			name:       "401 Unauthorized returns false (fail closed)",
			statusCode: http.StatusUnauthorized,
			body:       `{"error": {"message": "Unauthorized"}}`,
			wantResult: false,
		},
		{
			name:       "403 Forbidden returns false (fail closed)",
			statusCode: http.StatusForbidden,
			body:       `{"error": {"message": "Forbidden"}}`,
			wantResult: false,
		},
		{
			name:       "404 Not Found returns false (fail closed)",
			statusCode: http.StatusNotFound,
			body:       `{"error": {"message": "Not Found"}}`,
			wantResult: false,
		},
		{
			name:       "500 Internal Server Error returns false (fail closed)",
			statusCode: http.StatusInternalServerError,
			body:       `{"error": {"message": "Internal error"}}`,
			wantResult: false,
		},
		{
			name:       "transport network error returns false (fail closed)",
			netErr:     fmt.Errorf("connection reset by peer"),
			wantResult: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				expectedPath := "/2.0/user/workspaces/myws/permissions/repositories"
				if req.URL.Path != expectedPath {
					t.Errorf("CheckPushAccess request path = %q, want %q", req.URL.Path, expectedPath)
				}
				if tc.netErr != nil {
					return nil, tc.netErr
				}
				return &http.Response{
					StatusCode: tc.statusCode,
					Status:     fmt.Sprintf("%d Status", tc.statusCode),
					Body:       io.NopCloser(strings.NewReader(tc.body)),
					Header:     make(http.Header),
				}, nil
			})

			got := p.CheckPushAccess(ctx, target, "valid-token")
			if got != tc.wantResult {
				t.Errorf("CheckPushAccess() = %v, want %v", got, tc.wantResult)
			}
		})
	}
}

func TestBitbucketFetchComments(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 42}

	page1JSON := `{
		"next": "https://api.bitbucket.org/2.0/repositories/myws/myrepo/pullrequests/42/comments?page=2",
		"values": [
			{
				"id": 101,
				"deleted": false,
				"created_on": "2026-10-01T10:00:00Z",
				"content": { "raw": "Top-level PR discussion" },
				"user": { "nickname": "reviewer1", "links": { "avatar": { "href": "https://avatar/1" } } },
				"links": { "html": { "href": "https://bitbucket.org/myws/myrepo/pull-requests/42#comment-101" } }
			},
			{
				"id": 102,
				"deleted": true,
				"content": { "raw": "This comment was deleted" }
			},
			{
				"id": 103,
				"deleted": false,
				"created_on": "2026-10-01T10:05:00Z",
				"content": { "raw": "Looks good on head" },
				"user": { "nickname": "reviewer2", "display_name": "Reviewer Two" },
				"inline": {
					"path": "main.go",
					"to": 42
				},
				"links": { "html": { "href": "https://bitbucket.org/myws/myrepo/pull-requests/42#comment-103" } }
			}
		]
	}`

	page2JSON := `{
		"next": "",
		"values": [
			{
				"id": 104,
				"deleted": false,
				"created_on": "2026-10-01T10:10:00Z",
				"content": { "raw": "Old base code issue" },
				"user": { "display_name": "Reviewer Three" },
				"inline": {
					"path": "utils.go",
					"from": 15
				},
				"links": { "html": { "href": "https://bitbucket.org/myws/myrepo/pull-requests/42#comment-104" } }
			},
			{
				"id": 105,
				"deleted": false,
				"created_on": "2026-10-01T10:15:00Z",
				"content": { "raw": "Replying to your comment on main.go" },
				"user": { "nickname": "author1" },
				"parent": { "id": 103 },
				"links": { "html": { "href": "https://bitbucket.org/myws/myrepo/pull-requests/42#comment-105" } }
			}
		]
	}`

	pageCount := 0
	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		pageCount++
		var body string
		if pageCount == 1 {
			if !strings.Contains(req.URL.Path, "/comments") {
				t.Errorf("unexpected initial path: %q", req.URL.Path)
			}
			body = page1JSON
		} else if pageCount == 2 {
			if req.URL.RawQuery != "page=2" {
				t.Errorf("unexpected page 2 query: %q", req.URL.RawQuery)
			}
			body = page2JSON
		} else {
			t.Fatalf("unexpected request page %d", pageCount)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	issue, review, err := p.FetchComments(ctx, target, "test-token")
	if err != nil {
		t.Fatalf("FetchComments failed: %v", err)
	}

	if pageCount != 2 {
		t.Errorf("expected 2 pages requested, got %d", pageCount)
	}

	// 1 issue comment (101)
	if len(issue) != 1 {
		t.Fatalf("len(issue) = %d, want 1", len(issue))
	}
	c101 := issue[0]
	if c101.ID != 101 || c101.Kind != "issue" || c101.Body != "Top-level PR discussion" || c101.Author != "reviewer1" {
		t.Errorf("unexpected issue comment 101: %+v", c101)
	}
	if c101.AvatarURL != "https://avatar/1" {
		t.Errorf("c101.AvatarURL = %q, want https://avatar/1", c101.AvatarURL)
	}

	// Deleted comment 102 should be omitted completely
	for _, c := range append(issue, review...) {
		if c.ID == 102 {
			t.Errorf("deleted comment 102 was not omitted")
		}
	}

	// 3 review comments (103, 104, 105)
	if len(review) != 3 {
		t.Fatalf("len(review) = %d, want 3", len(review))
	}

	// 103: inline.to -> RIGHT, line 42
	c103 := review[0]
	if c103.ID != 103 || c103.Kind != "review" || c103.Path != "main.go" || c103.Line != 42 || c103.Side != "RIGHT" {
		t.Errorf("unexpected review comment 103: %+v", c103)
	}

	// 104: inline.from -> LEFT, line 15
	c104 := review[1]
	if c104.ID != 104 || c104.Kind != "review" || c104.Path != "utils.go" || c104.Line != 15 || c104.Side != "LEFT" || c104.Author != "Reviewer Three" {
		t.Errorf("unexpected review comment 104: %+v", c104)
	}

	// 105: reply to 103 -> InReplyTo 103, inherits Kind review and Path main.go
	c105 := review[2]
	if c105.ID != 105 || c105.Kind != "review" || c105.InReplyTo != 103 || c105.Path != "main.go" {
		t.Errorf("unexpected reply comment 105: %+v", c105)
	}
}

func TestBitbucketFetchCommentsPaginationCap(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 42}

	var requestCount int
	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requestCount++
		// Always return a next URL to simulate infinite pagination
		body := `{
			"next": "https://api.bitbucket.org/2.0/repositories/myws/myrepo/pullrequests/42/comments?page=` + fmt.Sprintf("%d", requestCount+1) + `",
			"values": [
				{
					"id": ` + fmt.Sprintf("%d", requestCount) + `,
					"deleted": false,
					"created_on": "2026-10-01T10:00:00Z",
					"content": { "raw": "comment" },
					"user": { "nickname": "reviewer", "links": { "avatar": { "href": "https://avatar/x" } } },
					"links": { "html": { "href": "https://bitbucket.org/myws/myrepo/pull-requests/42#comment-1" } }
				}
			]
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})

	_, _, err := p.FetchComments(ctx, target, "test-token")
	if err == nil {
		t.Error("FetchComments should return error when pagination cap exceeded")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("error message should contain 'exceeded', got: %v", err)
	}
	if requestCount != 100 {
		t.Errorf("expected exactly 100 requests, got %d", requestCount)
	}
}

func TestBitbucketHTTPErrorHelper(t *testing.T) {
	secretToken := "secret_token_12345"
	userPassToken := "myuser:super_secret_pw_999"

	statusCases := []struct {
		code             int
		wantStatusSubstr string
		wantDetailSubstr string
	}{
		{http.StatusUnauthorized, "401", "unauthorized"},
		{http.StatusForbidden, "403", "forbidden"},
		{http.StatusNotFound, "404", "not found"},
		{http.StatusTooManyRequests, "429", "rate limit"},
	}

	var generatedErrors []string

	for _, sc := range statusCases {
		resp := &http.Response{
			StatusCode: sc.code,
			Status:     fmt.Sprintf("%d Error", sc.code),
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error": "failed for %s"}`, secretToken))),
		}
		err := bitbucketHTTPError(resp, "test action", secretToken)
		if err == nil {
			t.Fatalf("expected error for status %d", sc.code)
		}
		errStr := err.Error()
		generatedErrors = append(generatedErrors, errStr)

		if !strings.HasPrefix(errStr, "bitbucket: ") {
			t.Errorf("status %d error %q does not start with bitbucket: prefix", sc.code, errStr)
		}
		if !strings.Contains(errStr, sc.wantStatusSubstr) {
			t.Errorf("status %d error %q does not contain status %q", sc.code, errStr, sc.wantStatusSubstr)
		}
		if !strings.Contains(strings.ToLower(errStr), sc.wantDetailSubstr) {
			t.Errorf("status %d error %q does not contain detail %q", sc.code, errStr, sc.wantDetailSubstr)
		}
		if strings.Contains(errStr, secretToken) {
			t.Errorf("status %d error leaked secret token: %q", sc.code, errStr)
		}
		if !strings.Contains(errStr, "***") {
			t.Errorf("status %d error should contain *** redaction: %q", sc.code, errStr)
		}
	}

	// Verify all 4 errors are distinct from each other
	for i := 0; i < len(generatedErrors); i++ {
		for j := i + 1; j < len(generatedErrors); j++ {
			if generatedErrors[i] == generatedErrors[j] {
				t.Errorf("error %d and %d are identical: %q", i, j, generatedErrors[i])
			}
		}
	}

	// Test Basic auth password redaction
	b64 := base64.StdEncoding.EncodeToString([]byte(userPassToken))
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Status:     "401 Unauthorized",
		Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error": "failed for user %s Basic %s with pw super_secret_pw_999"}`, userPassToken, b64))),
	}
	err := bitbucketHTTPError(resp, "auth check", userPassToken)
	errStr := err.Error()
	if strings.Contains(errStr, userPassToken) {
		t.Errorf("error leaked full token: %q", errStr)
	}
	if strings.Contains(errStr, "super_secret_pw_999") {
		t.Errorf("error leaked password: %q", errStr)
	}
	if strings.Contains(errStr, b64) {
		t.Errorf("error leaked base64 encoded token: %q", errStr)
	}
	if !strings.Contains(errStr, "***") {
		t.Errorf("error should contain *** redaction: %q", errStr)
	}
}

func TestBitbucket_U5_PostIssueComment(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 42}

	var reqMethod, reqPath string
	var reqBody []byte
	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		reqMethod = req.Method
		reqPath = req.URL.Path
		reqBody, _ = io.ReadAll(req.Body)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body: io.NopCloser(strings.NewReader(`{
				"id": 501,
				"created_on": "2026-10-02T10:00:00Z",
				"content": { "raw": "LGTM from review" },
				"user": { "nickname": "alice", "links": { "avatar": { "href": "https://avatar/alice" } } },
				"links": { "html": { "href": "https://bitbucket.org/myws/myrepo/pull-requests/42#comment-501" } }
			}`)),
			Header: make(http.Header),
		}, nil
	})

	comment, err := p.PostIssueComment(ctx, target, "test-token", "LGTM from review")
	if err != nil {
		t.Fatalf("PostIssueComment failed: %v", err)
	}
	if reqMethod != http.MethodPost {
		t.Errorf("Method = %q, want POST", reqMethod)
	}
	if reqPath != "/2.0/repositories/myws/myrepo/pullrequests/42/comments" {
		t.Errorf("Path = %q, want /2.0/repositories/myws/myrepo/pullrequests/42/comments", reqPath)
	}

	var parsed struct {
		Content struct {
			Raw string `json:"raw"`
		} `json:"content"`
	}
	if err := json.Unmarshal(reqBody, &parsed); err != nil {
		t.Fatalf("failed to decode request body: %v", err)
	}
	if parsed.Content.Raw != "LGTM from review" {
		t.Errorf("request body raw = %q, want 'LGTM from review'", parsed.Content.Raw)
	}

	if comment.Kind != "issue" {
		t.Errorf("comment.Kind = %q, want issue", comment.Kind)
	}
	if comment.ID != 501 {
		t.Errorf("comment.ID = %d, want 501", comment.ID)
	}
	if comment.Body != "LGTM from review" {
		t.Errorf("comment.Body = %q, want 'LGTM from review'", comment.Body)
	}
	if comment.Author != "alice" {
		t.Errorf("comment.Author = %q, want alice", comment.Author)
	}
}

func TestBitbucket_U5_ReplyToReviewComment(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 42}

	var reqBody []byte
	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			// Parent comment fetch
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"id": 103,
					"inline": { "path": "main.go", "to": 15 },
					"content": { "raw": "parent comment" }
				}`)),
				Header: make(http.Header),
			}, nil
		}
		if req.Body != nil {
			reqBody, _ = io.ReadAll(req.Body)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body: io.NopCloser(strings.NewReader(`{
				"id": 502,
				"created_on": "2026-10-02T10:05:00Z",
				"content": { "raw": "I addressed your comment" },
				"user": { "nickname": "bob" },
				"parent": { "id": 103 },
				"links": { "html": { "href": "https://bitbucket.org/myws/myrepo/pull-requests/42#comment-502" } }
			}`)),
			Header: make(http.Header),
		}, nil
	})

	comment, err := p.ReplyToReviewComment(ctx, target, "test-token", 103, "I addressed your comment")
	if err != nil {
		t.Fatalf("ReplyToReviewComment failed: %v", err)
	}

	var parsed struct {
		Content struct {
			Raw string `json:"raw"`
		} `json:"content"`
		Parent struct {
			ID int64 `json:"id"`
		} `json:"parent"`
	}
	if err := json.Unmarshal(reqBody, &parsed); err != nil {
		t.Fatalf("failed to decode request body: %v", err)
	}
	// "reply sends parent.id"
	if parsed.Parent.ID != 103 {
		t.Errorf("parsed.Parent.ID = %d, want 103", parsed.Parent.ID)
	}
	if parsed.Content.Raw != "I addressed your comment" {
		t.Errorf("parsed.Content.Raw = %q, want 'I addressed your comment'", parsed.Content.Raw)
	}

	if comment.Kind != "review" {
		t.Errorf("comment.Kind = %q, want review", comment.Kind)
	}
	if comment.ID != 502 {
		t.Errorf("comment.ID = %d, want 502", comment.ID)
	}
	if comment.InReplyTo != 103 {
		t.Errorf("comment.InReplyTo = %d, want 103", comment.InReplyTo)
	}
	if comment.Path != "main.go" {
		t.Errorf("comment.Path = %q, want main.go", comment.Path)
	}
	if comment.Line != 15 {
		t.Errorf("comment.Line = %d, want 15", comment.Line)
	}
	if comment.Side != "RIGHT" {
		t.Errorf("comment.Side = %q, want RIGHT", comment.Side)
	}
}

func TestBitbucket_U5_SubmitReview_Scenarios(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 42}

	type recordedReq struct {
		Method string
		Path   string
		Body   map[string]any
	}

	t.Run("COMMENT with 2 drafts + body calls no verdict; RIGHT sends inline.to; LEFT sends inline.from", func(t *testing.T) {
		var reqs []recordedReq
		bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			b, _ := io.ReadAll(req.Body)
			var parsed map[string]any
			if len(b) > 0 {
				_ = json.Unmarshal(b, &parsed)
			}
			reqs = append(reqs, recordedReq{Method: req.Method, Path: req.URL.Path, Body: parsed})
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"id": 1}`)),
				Header:     make(http.Header),
			}, nil
		})

		drafts := []prComment{
			{ID: 1, Path: "main.go", Line: 15, Side: "RIGHT", Body: "check right"},
			{ID: 2, Path: "util.go", Line: 30, Side: "LEFT", Body: "check left"},
		}

		err := p.SubmitReview(ctx, target, "token", "headsha", drafts, "COMMENT", "Overall feedback body")
		if err != nil {
			t.Fatalf("SubmitReview failed: %v", err)
		}

		if len(reqs) != 3 {
			t.Fatalf("expected exactly 3 requests (2 drafts + 1 body), got %d: %+v", len(reqs), reqs)
		}

		// Req 0: Draft 1 (RIGHT sends inline.to)
		r0 := reqs[0]
		if r0.Path != "/2.0/repositories/myws/myrepo/pullrequests/42/comments" {
			t.Errorf("req 0 path = %q", r0.Path)
		}
		inline0, _ := r0.Body["inline"].(map[string]any)
		if inline0 == nil || inline0["path"] != "main.go" || inline0["to"] != float64(15) || inline0["from"] != nil {
			t.Errorf("req 0 inline expected path=main.go, to=15, from=nil; got %+v", inline0)
		}
		cnt0, _ := r0.Body["content"].(map[string]any)
		if cnt0 == nil || cnt0["raw"] != "check right" {
			t.Errorf("req 0 content raw = %v, want 'check right'", cnt0)
		}

		// Req 1: Draft 2 (LEFT sends inline.from)
		r1 := reqs[1]
		inline1, _ := r1.Body["inline"].(map[string]any)
		if inline1 == nil || inline1["path"] != "util.go" || inline1["from"] != float64(30) || inline1["to"] != nil {
			t.Errorf("req 1 inline expected path=util.go, from=30, to=nil; got %+v", inline1)
		}
		cnt1, _ := r1.Body["content"].(map[string]any)
		if cnt1 == nil || cnt1["raw"] != "check left" {
			t.Errorf("req 1 content raw = %v, want 'check left'", cnt1)
		}

		// Req 2: Body as issue comment
		r2 := reqs[2]
		if r2.Body["inline"] != nil {
			t.Errorf("req 2 (body) should have no inline, got %+v", r2.Body["inline"])
		}
		cnt2, _ := r2.Body["content"].(map[string]any)
		if cnt2 == nil || cnt2["raw"] != "Overall feedback body" {
			t.Errorf("req 2 content raw = %v, want 'Overall feedback body'", cnt2)
		}

		// Verify NO verdict endpoint called
		for i, r := range reqs {
			if strings.Contains(r.Path, "/approve") || strings.Contains(r.Path, "/request-changes") {
				t.Errorf("req %d unexpectedly called verdict endpoint: %s", i, r.Path)
			}
		}
	})

	t.Run("APPROVE calls /approve", func(t *testing.T) {
		var reqs []recordedReq
		bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			var parsed map[string]any
			if req.Body != nil {
				b, _ := io.ReadAll(req.Body)
				if len(b) > 0 {
					_ = json.Unmarshal(b, &parsed)
				}
			}
			reqs = append(reqs, recordedReq{Method: req.Method, Path: req.URL.Path, Body: parsed})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"approved": true}`)),
				Header:     make(http.Header),
			}, nil
		})

		drafts := []prComment{
			{ID: 1, Path: "main.go", Line: 5, Side: "RIGHT", Body: "looks fine"},
		}
		err := p.SubmitReview(ctx, target, "token", "headsha", drafts, "APPROVE", "Ship it!")
		if err != nil {
			t.Fatalf("SubmitReview failed: %v", err)
		}

		if len(reqs) != 3 {
			t.Fatalf("expected 3 requests (1 draft + 1 body + 1 verdict), got %d: %+v", len(reqs), reqs)
		}
		last := reqs[2]
		if last.Method != http.MethodPost || last.Path != "/2.0/repositories/myws/myrepo/pullrequests/42/approve" {
			t.Errorf("last req = %s %s, want POST /2.0/repositories/myws/myrepo/pullrequests/42/approve", last.Method, last.Path)
		}
	})

	t.Run("REQUEST_CHANGES calls /request-changes", func(t *testing.T) {
		var reqs []recordedReq
		bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			var parsed map[string]any
			if req.Body != nil {
				b, _ := io.ReadAll(req.Body)
				if len(b) > 0 {
					_ = json.Unmarshal(b, &parsed)
				}
			}
			reqs = append(reqs, recordedReq{Method: req.Method, Path: req.URL.Path, Body: parsed})
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status": "changes_requested"}`)),
				Header:     make(http.Header),
			}, nil
		})

		err := p.SubmitReview(ctx, target, "token", "headsha", nil, "REQUEST_CHANGES", "Needs revision")
		if err != nil {
			t.Fatalf("SubmitReview failed: %v", err)
		}

		if len(reqs) != 2 {
			t.Fatalf("expected 2 requests (1 body + 1 verdict), got %d: %+v", len(reqs), reqs)
		}
		last := reqs[1]
		if last.Method != http.MethodPost || last.Path != "/2.0/repositories/myws/myrepo/pullrequests/42/request-changes" {
			t.Errorf("last req = %s %s, want POST /2.0/repositories/myws/myrepo/pullrequests/42/request-changes", last.Method, last.Path)
		}
	})

	t.Run("empty body posts no issue comment", func(t *testing.T) {
		for _, emptyBody := range []string{"", "   \t\n  "} {
			var reqs []recordedReq
			bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				reqs = append(reqs, recordedReq{Method: req.Method, Path: req.URL.Path})
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			})

			drafts := []prComment{
				{ID: 10, Path: "a.go", Line: 1, Side: "RIGHT", Body: "note"},
			}
			err := p.SubmitReview(ctx, target, "token", "headsha", drafts, "APPROVE", emptyBody)
			if err != nil {
				t.Fatalf("SubmitReview with empty body %q failed: %v", emptyBody, err)
			}

			// Only 2 requests: draft comment and approve endpoint
			if len(reqs) != 2 {
				t.Fatalf("expected exactly 2 requests with empty body %q, got %d: %+v", emptyBody, len(reqs), reqs)
			}
			if reqs[0].Path != "/2.0/repositories/myws/myrepo/pullrequests/42/comments" {
				t.Errorf("req 0 path = %q, want comments", reqs[0].Path)
			}
			if reqs[1].Path != "/2.0/repositories/myws/myrepo/pullrequests/42/approve" {
				t.Errorf("req 1 path = %q, want approve", reqs[1].Path)
			}
		}
	})

	t.Run("failure on 3rd of 5 drafts reports step draft and returns PartialSubmitError with posted IDs", func(t *testing.T) {
		callCount := 0
		bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 3 {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Status:     "500 Internal Server Error",
					Body:       io.NopCloser(strings.NewReader(`{"error": "something went wrong on 3rd draft"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"id": 100}`)),
				Header:     make(http.Header),
			}, nil
		})

		drafts := []prComment{
			{ID: 101, Path: "f1.go", Line: 1, Side: "RIGHT", Body: "c1"},
			{ID: 102, Path: "f2.go", Line: 2, Side: "RIGHT", Body: "c2"},
			{ID: 103, Path: "f3.go", Line: 3, Side: "RIGHT", Body: "c3"},
			{ID: 104, Path: "f4.go", Line: 4, Side: "RIGHT", Body: "c4"},
			{ID: 105, Path: "f5.go", Line: 5, Side: "RIGHT", Body: "c5"},
		}

		err := p.SubmitReview(ctx, target, "token", "headsha", drafts, "COMMENT", "")
		if err == nil {
			t.Fatal("expected SubmitReview to fail on 3rd draft")
		}

		var partial *PartialSubmitError
		if !errors.As(err, &partial) {
			t.Fatalf("expected PartialSubmitError, got %T: %v", err, err)
		}
		if partial.Step != "draft" {
			t.Errorf("partial.Step = %q, want draft", partial.Step)
		}
		if len(partial.PostedIDs) != 2 || partial.PostedIDs[0] != 101 || partial.PostedIDs[1] != 102 {
			t.Errorf("partial.PostedIDs = %v, want [101, 102]", partial.PostedIDs)
		}
		if !errors.Is(partial, partial.Err) {
			t.Errorf("errors.Is(partial, partial.Err) should be true via Unwrap")
		}
	})

	t.Run("failure on verdict reports verdict step and returns PartialSubmitError with all posted IDs", func(t *testing.T) {
		bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/approve") {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Status:     "403 Forbidden",
					Body:       io.NopCloser(strings.NewReader(`{"error": "cannot approve own PR"}`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(strings.NewReader(`{"id": 200}`)),
				Header:     make(http.Header),
			}, nil
		})

		drafts := []prComment{
			{ID: 201, Path: "f1.go", Line: 1, Side: "RIGHT", Body: "c1"},
			{ID: 202, Path: "f2.go", Line: 2, Side: "RIGHT", Body: "c2"},
		}

		err := p.SubmitReview(ctx, target, "token", "headsha", drafts, "APPROVE", "Looks great!")
		if err == nil {
			t.Fatal("expected SubmitReview to fail on approve verdict")
		}

		var partial *PartialSubmitError
		if !errors.As(err, &partial) {
			t.Fatalf("expected PartialSubmitError, got %T: %v", err, err)
		}
		if partial.Step != "verdict" {
			t.Errorf("partial.Step = %q, want verdict", partial.Step)
		}
		if len(partial.PostedIDs) != 2 || partial.PostedIDs[0] != 201 || partial.PostedIDs[1] != 202 {
			t.Errorf("partial.PostedIDs = %v, want [201, 202]", partial.PostedIDs)
		}
		if !strings.Contains(partial.Error(), "verdict") {
			t.Errorf("partial.Error() %q does not contain 'verdict'", partial.Error())
		}
	})
}

func TestBitbucketRequestSecurity(t *testing.T) {
	ctx := context.Background()

	// 1. Cleartext HTTP rejected
	_, err := bitbucketRequest(ctx, http.MethodGet, "http://api.bitbucket.org/2.0/user", "secret-token", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid request path") {
		t.Fatalf("expected cleartext http rejection, got: %v", err)
	}

	// 2. SSRF external hostname rejected
	_, err = bitbucketRequest(ctx, http.MethodGet, "https://evil.attacker.com/steal", "secret-token", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid api URL host") {
		t.Fatalf("expected external hostname rejection, got: %v", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Errorf("token leaked in error message: %v", err)
	}

	// 3. Path without leading slash rejected
	_, err = bitbucketRequest(ctx, http.MethodGet, "repositories/ws/repo", "token", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid request path") {
		t.Fatalf("expected relative path rejection without slash, got: %v", err)
	}
}

func TestBitbucketContextLineCommentsMapToRight(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "myrepo", Number: 42}

	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"values": [
					{
						"id": 101,
						"created_on": "2026-10-02T10:00:00Z",
						"content": { "raw": "context comment" },
						"user": { "nickname": "reviewer" },
						"inline": {
							"path": "main.go",
							"from": 20,
							"to": 20
						}
					}
				]
			}`)),
			Header: make(http.Header),
		}, nil
	})

	_, reviews, err := p.FetchComments(ctx, target, "test-token")
	if err != nil {
		t.Fatalf("FetchComments failed: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("expected 1 review comment, got %d", len(reviews))
	}
	if reviews[0].Side != "RIGHT" {
		t.Errorf("reviews[0].Side = %q, want RIGHT for context line comment", reviews[0].Side)
	}
	if reviews[0].Line != 20 {
		t.Errorf("reviews[0].Line = %d, want 20", reviews[0].Line)
	}
}

func TestBitbucketCheckPushAccessPagination(t *testing.T) {
	orig := bitbucketHTTPClient.Transport
	defer func() { bitbucketHTTPClient.Transport = orig }()

	p := &BitbucketProvider{}
	ctx := context.Background()
	target := PRTarget{Provider: "bitbucket", Owner: "myws", Repo: "repo2", Number: 42}

	pageRequests := 0
	bitbucketHTTPClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		pageRequests++
		if strings.Contains(req.URL.RawQuery, "q=") {
			// Query filter returns empty to trigger pagination fallback
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{ "values": [] }`)),
				Header:     make(http.Header),
			}, nil
		}
		if pageRequests == 2 {
			// Page 1
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"values": [
						{
							"permission": "read",
							"repository": { "full_name": "myws/repo1", "slug": "repo1" }
						}
					],
					"next": "https://api.bitbucket.org/2.0/user/workspaces/myws/permissions/repositories?page=2"
				}`)),
				Header: make(http.Header),
			}, nil
		}
		// Page 2
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"values": [
					{
						"permission": "write",
						"repository": { "full_name": "myws/repo2", "slug": "repo2" }
					}
				]
			}`)),
			Header: make(http.Header),
		}, nil
	})

	hasPush := p.CheckPushAccess(ctx, target, "valid-token")
	if !hasPush {
		t.Errorf("CheckPushAccess() = false, want true for repo found on page 2 with write permission")
	}
}
