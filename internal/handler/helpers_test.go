package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/app"
	"github.com/px0-ai/px0/internal/store"
	"github.com/px0-ai/px0/internal/testutil"
)

// testApp wraps *fiber.App and overrides Test to use an unlimited timeout
// and automatically asserts that all request/response exchanges conform to
// the OpenAPI spec definition.
type testApp struct {
	*fiber.App
	t *testing.T
}

func (a *testApp) Test(req *http.Request, _ ...int) (*http.Response, error) {
	resp, err := a.App.Test(req, -1)
	if err == nil && a.t != nil {
		AssertContract(a.t, resp)
	}
	return resp, err
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	t.Setenv("RESEND_API_KEY", "")
	testutil.SetupDB(t)
	return &testApp{
		App: app.New(),
		t:   t,
	}
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

// newAPIKeyReq builds a request authenticated via API key as a Bearer token.
func newAPIKeyReq(t *testing.T, method, url, body, apiKey string) *http.Request {
	t.Helper()
	return newReq(t, method, url, body, apiKey)
}

// decodeBody decodes the response body as a generic map.
func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}

// setupUser registers a user and returns a valid access token.
func setupUser(t *testing.T, a *testApp) string {
	t.Helper()
	email := "test@px0.dev"
	password := "TestPassword123!"

	// Register publicly (without admin key and without team_id) to become an Admin user.
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "register failed")

	body := decodeBody(t, resp)
	userVal := body["user"].(map[string]any)
	userIDStr := userVal["id"].(string)
	userID, err := uuid.Parse(userIDStr)
	require.NoError(t, err)

	// Manually verify user and create a default team for them in the test DB
	ctx := context.Background()
	err = store.VerifyUser(ctx, userID)
	require.NoError(t, err)

	org, err := store.CreateOrganization(ctx, "Default Test Org")
	require.NoError(t, err)

	team, err := store.CreateTeamWithOrg(ctx, "Test Setup Team", org.ID)
	require.NoError(t, err)
	err = store.AddTeamMember(ctx, team.ID, userID)
	require.NoError(t, err)

	req = newReq(t, http.MethodPost, "/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "login failed")

	body = decodeBody(t, resp)
	return body["token"].(string)
}

// setupProject creates a project owned by the user's first team and returns its ID.
func setupProject(t *testing.T, a *testApp, token string) string {
	t.Helper()
	teamID := setupUserTeam(t, token)
	name := fmt.Sprintf("Test Project %s", uuid.New().String())
	req := newReq(t, http.MethodPost, "/v1/projects",
		fmt.Sprintf(`{"team_id":%q,"name":%q}`, teamID, name), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create project failed")

	body := decodeBody(t, resp)
	return body["project"].(map[string]any)["id"].(string)
}

// setupPromptInProject creates a prompt inside the given project and returns its ID.
func setupPromptInProject(t *testing.T, a *testApp, token, projectID string) string {
	t.Helper()
	uniqueName := fmt.Sprintf("Test Prompt %s", uuid.New().String())
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
		fmt.Sprintf(`{"name":%q,"description":"A test prompt"}`, uniqueName), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create prompt failed")

	body := decodeBody(t, resp)
	return body["prompt"].(map[string]any)["id"].(string)
}

// setupPrompt creates a project and a prompt inside it, returning the prompt ID.
func setupPrompt(t *testing.T, a *testApp, token string) string {
	t.Helper()
	projectID := setupProject(t, a, token)
	return setupPromptInProject(t, a, token, projectID)
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
	ctx := context.Background()
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, session.UserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	team := teams[0]
	orgID := team.OrgID

	req := newReq(t, http.MethodPost, "/v1/api-keys",
		fmt.Sprintf(`{"name":"test-key","org_id":%q,"operation":"all","team_ids":[%q]}`, orgID.String(), team.ID.String()), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create api key failed")

	body := decodeBody(t, resp)
	return body["key"].(string)
}

var globalRouter routers.Router

func initSpecRouter(t *testing.T) {
	if globalRouter != nil {
		return
	}
	t.Helper()

	path := filepath.Join("..", "..", "docs", "openapi", "openapi.yaml")
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	doc, err := loader.LoadFromFile(path)
	require.NoError(t, err, "failed to load openapi spec from %s", path)

	err = doc.Validate(loader.Context)
	require.NoError(t, err, "openapi spec validation failed")

	// Clear servers to bypass host-matching and port-matching during local integration tests
	doc.Servers = nil

	router, err := legacy.NewRouter(doc)
	require.NoError(t, err, "failed to initialize legacy router with doc")
	globalRouter = router
}

// AssertContract ensures that the given HTTP response and its originating request
// fully comply with the OpenAPI 3.1 schema defined in docs/openapi/.
func AssertContract(t *testing.T, resp *http.Response) {
	t.Helper()
	initSpecRouter(t)

	require.NotNil(t, resp.Request, "response has no associated request")

	// Find the route inside the spec
	route, pathParams, err := globalRouter.FindRoute(resp.Request)
	require.NoError(t, err, "Route not documented in OpenAPI spec: %s %s", resp.Request.Method, resp.Request.URL.Path)

	// Clone request body so kin-openapi can read it without exhausting the reader
	var reqBodyBytes []byte
	if resp.Request.Body != nil {
		reqBodyBytes, err = io.ReadAll(resp.Request.Body)
		require.NoError(t, err)
		resp.Request.Body = io.NopCloser(bytes.NewReader(reqBodyBytes))
	}

	// Validate Request only on successful responses (status < 400).
	// For intentional failure tests, the request body is designed to be invalid.
	var reqInput *openapi3filter.RequestValidationInput
	if resp.StatusCode < 400 {
		reqInput = &openapi3filter.RequestValidationInput{
			Request:    resp.Request,
			PathParams: pathParams,
			Route:      route,
			Options: &openapi3filter.Options{
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			},
		}
		err = openapi3filter.ValidateRequest(context.Background(), reqInput)
		require.NoError(t, err, "Request validation failed against OpenAPI spec")
	} else {
		// Even for failure codes, we construct RequestValidationInput for ValidateResponse to match response schemas
		reqInput = &openapi3filter.RequestValidationInput{
			Request:    resp.Request,
			PathParams: pathParams,
			Route:      route,
		}
	}

	// Clone response body so kin-openapi can read it without exhausting it for downstream assertions
	var respBodyBytes []byte
	if resp.Body != nil {
		respBodyBytes, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		resp.Body = io.NopCloser(bytes.NewReader(respBodyBytes))
	}

	// Validate Response (skip body validation for SSE streams as kin-openapi does not support decoding them)
	if resp.Header.Get("Content-Type") == "text/event-stream" {
		return
	}

	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 resp.StatusCode,
		Header:                 resp.Header,
		Body:                   io.NopCloser(bytes.NewReader(respBodyBytes)),
	}
	err = openapi3filter.ValidateResponse(context.Background(), respInput)
	require.NoError(t, err, "Response validation failed against OpenAPI spec (drift detected!)")
}

// setupUserTeam returns the first team ID for the user's session.
func setupUserTeam(t *testing.T, token string) string {
	t.Helper()
	ctx := context.Background()
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, session.UserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	return teams[0].ID.String()
}
