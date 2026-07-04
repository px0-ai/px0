package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
)

func TestCreateAPIKey_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	ctx := context.Background()
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, session.UserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	team := teams[0]
	orgID := team.OrgID

	req := newReq(t, http.MethodPost, "/v1/api-keys",
		fmt.Sprintf(`{"name":"ci-pipeline","org_id":%q,"team_ids":[%q],"operation":"all"}`, orgID.String(), team.ID.String()), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "ci-pipeline", body["name"])
	assert.NotEmpty(t, body["key"])
	key := body["key"].(string)
	assert.True(t, len(key) > 8, "key should be long enough to be a real key")
}

func TestCreateAPIKey_MissingName(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	ctx := context.Background()
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, session.UserID)
	require.NoError(t, err)
	orgID := teams[0].OrgID

	req := newReq(t, http.MethodPost, "/v1/api-keys", fmt.Sprintf(`{"org_id":%q}`, orgID.String()), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestCreateAPIKey_RequiresAccessToken(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	apiKey := setupAPIKey(t, a, token)

	ctx := context.Background()
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, session.UserID)
	require.NoError(t, err)
	orgID := teams[0].OrgID

	// Creating an API key via an existing API key (even if it has 'all' operation) is not allowed because it is not an access token (session token) as per RequireAccessToken, or wait!
	// Wait, RequireAccessToken accepts only access tokens OR API keys with 'all' operation.
	// But in TestCreateAPIKey_RequiresAccessToken, the comment says "Creating an API key via an existing API key must be rejected."
	// Wait, if RequireAccessToken accepts API keys with 'all' operation, can they create API keys?
	// The comment in TestCreateAPIKey_RequiresAccessToken says: "Creating an API key via an existing API key must be rejected."
	// Wait, to satisfy this test, we can check inside CreateAPIKey handler that the request must NOT be authenticated via API key! Or that RequireAccessToken should enforce session token only for API Key CRUD?
	// Let's re-read: "Must be called with standard access token; attempts to escalate using an existing API Key will return 401 Unauthorized."
	// Ah! "Must be called with standard access token; attempts to escalate using an existing API Key will return 401 Unauthorized" in openapi spec!
	// So only a session token (standard access token) is allowed to perform API Key CRUD!
	// Yes! In `RequireAccessToken` we can enforce that for `/v1/api-keys` endpoints, they can only be accessed with a session token (i.e. API keys are not allowed to manage API keys themselves!).
	// Wait, how can we do this?
	// We can define a middleware `RequireSessionToken` which only allows session tokens!
	// In `internal/middleware/auth.go`:
	// ```go
	// func RequireSessionToken(c *fiber.Ctx) error {
	//     if tryAccessTokenAuth(c) {
	//         // Ensure it's not an API key (meaning LocalsUserID != uuid.Nil)
	//         userID, ok := c.Locals(LocalsUserID).(uuid.UUID)
	//         if ok && userID != uuid.Nil {
	//             return c.Next()
	//         }
	//     }
	//     return apierr.ErrUnauthorized.Respond(c)
	// }
	// ```
	// Let's check: yes! If we use `RequireSessionToken` for `/api-keys` endpoints, it will perfectly block API keys from managing API keys, and return 401 Unauthorized!
	// Let's implement this!

	req := newAPIKeyReq(t, http.MethodPost, "/v1/api-keys",
		fmt.Sprintf(`{"name":"escalated","org_id":%q}`, orgID.String()), apiKey)
	resp, err := a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestListAPIKeys(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	ctx := context.Background()
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, session.UserID)
	require.NoError(t, err)
	orgID := teams[0].OrgID

	setupAPIKey(t, a, token)
	setupAPIKey(t, a, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/api-keys?org_id=%s", orgID.String()), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	keys := body["api_keys"].([]any)
	assert.Len(t, keys, 2)

	// Full key must not be returned in list.
	first := keys[0].(map[string]any)
	assert.Nil(t, first["key"])
}

func TestDeleteAPIKey(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	ctx := context.Background()
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, session.UserID)
	require.NoError(t, err)
	team := teams[0]
	orgID := team.OrgID

	// create a key and note its ID
	req := newReq(t, http.MethodPost, "/v1/api-keys",
		fmt.Sprintf(`{"name":"temp","org_id":%q,"team_ids":[%q]}`, orgID.String(), team.ID.String()), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	body := decodeBody(t, resp)
	id := body["id"].(string)

	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/api-keys/%s", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// confirm it no longer appears in the list
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/api-keys?org_id=%s", orgID.String()), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	body = decodeBody(t, resp)
	assert.Empty(t, body["api_keys"].([]any))
}

func TestDeleteAPIKey_NotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodDelete,
		"/v1/api-keys/00000000-0000-0000-0000-000000000001", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestCreateAPIKey_GlobalScope(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	// Get organization and team IDs
	session, err := store.GetSessionByToken(context.Background(), token)
	require.NoError(t, err)
	userID := session.UserID

	teams, err := store.GetUserTeams(context.Background(), userID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	orgID := teams[0].OrgID

	// 1. Create a Global API key (empty team_ids)
	req := newReq(t, http.MethodPost, "/v1/api-keys",
		fmt.Sprintf(`{"name":"global-key","org_id":%q,"team_ids":[],"operation":"read_render"}`, orgID.String()), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	apiKey := body["key"].(string)
	resp.Body.Close()

	// 2. Use the Global API key to list prompts (should succeed and resolve teams globally)
	reqPrompts := newAPIKeyReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts", teams[0].ID.String()), "", apiKey)
	respPrompts, err := a.Test(reqPrompts)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respPrompts.StatusCode)
	respPrompts.Body.Close()
}

func TestCreateAPIKey_AdminScope(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	// Get organization and team IDs
	session, err := store.GetSessionByToken(context.Background(), token)
	require.NoError(t, err)
	userID := session.UserID

	teams, err := store.GetUserTeams(context.Background(), userID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	orgID := teams[0].OrgID

	// 1. Create an Admin API key
	req := newReq(t, http.MethodPost, "/v1/api-keys",
		fmt.Sprintf(`{"name":"admin-key","org_id":%q,"team_ids":[],"operation":"admin"}`, orgID.String()), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	apiKey := body["key"].(string)
	resp.Body.Close()

	// 2. Use the Admin API Key to perform an admin action: Create a new custom team
	reqTeam := newAPIKeyReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgID.String()), `{"name":"Admin Key Team"}`, apiKey)
	respTeam, err := a.Test(reqTeam)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respTeam.StatusCode)
	respTeam.Body.Close()
}

func TestCreateAPIKey_TeamNotInOrg(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a) // system admin, org admin of "Default Test Org"

	ctx := context.Background()
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, session.UserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	orgID := teams[0].OrgID

	// Create a second, unrelated organization and team.
	otherOrg, err := store.CreateOrganization(ctx, "Other Org")
	require.NoError(t, err)
	otherTeam, err := store.CreateTeamWithOrg(ctx, "Other Team", otherOrg.ID)
	require.NoError(t, err)

	// Attempt to create an API key for the caller's own org, but scoped to a
	// team that belongs to the unrelated org.
	req := newReq(t, http.MethodPost, "/v1/api-keys",
		fmt.Sprintf(`{"name":"cross-org","org_id":%q,"team_ids":[%q]}`, orgID.String(), otherTeam.ID.String()), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	AssertContract(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "team does not belong to the specified organization", body["error"])
}
