package handler_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/arpitbhayani/px0/internal/app"
	"github.com/arpitbhayani/px0/internal/testutil"
)

// testApp wraps *fiber.App and overrides Test to use an unlimited timeout.
// This prevents spurious timeouts when the race detector slows down bcrypt
// and avoids leaving handler goroutines running past their deadline.
type testApp struct{ *fiber.App }

func (a *testApp) Test(req *http.Request, _ ...int) (*http.Response, error) {
	return a.App.Test(req, -1)
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	testutil.SetupDB(t)
	return &testApp{app.New()}
}

// newReq builds an HTTP request with optional JSON body and bearer token.
func newReq(t *testing.T, method, url, body, token string) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, url, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// newAPIKeyReq builds a request authenticated via API key header.
func newAPIKeyReq(t *testing.T, method, url, body, apiKey string) *http.Request {
	t.Helper()
	req := newReq(t, method, url, body, "")
	req.Header.Set("X-API-Key", apiKey)
	return req
}

// decodeBody decodes the response body as a generic map.
func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

// setupUser registers a user and returns a valid session token.
func setupUser(t *testing.T, a *testApp) string {
	t.Helper()
	email := "test@px0.dev"
	password := "testpassword"

	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "register failed")
	resp.Body.Close()

	req = newReq(t, http.MethodPost, "/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "login failed")

	body := decodeBody(t, resp)
	return body["token"].(string)
}

// setupPrompt creates a prompt and returns its ID.
func setupPrompt(t *testing.T, a *testApp, token string) string {
	t.Helper()
	req := newReq(t, http.MethodPost, "/v1/prompts",
		`{"name":"Test Prompt","description":"A test prompt"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create prompt failed")

	body := decodeBody(t, resp)
	return body["prompt"].(map[string]any)["id"].(string)
}

// setupVersion creates a draft version and returns its version number.
func setupVersion(t *testing.T, a *testApp, token, promptID, template string) int {
	t.Helper()
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", promptID),
		fmt.Sprintf(`{"template":%q}`, template), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create version failed")

	body := decodeBody(t, resp)
	return int(body["version"].(map[string]any)["version"].(float64))
}

// setupAPIKey creates an API key and returns the raw key string.
func setupAPIKey(t *testing.T, a *testApp, token string) string {
	t.Helper()
	req := newReq(t, http.MethodPost, "/v1/api-keys",
		`{"name":"test-key"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create api key failed")

	body := decodeBody(t, resp)
	return body["key"].(string)
}
