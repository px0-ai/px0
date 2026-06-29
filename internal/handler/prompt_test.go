package handler_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
)

func TestCreatePrompt_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"My Prompt","description":"Useful prompt"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	prompt := body["prompt"].(map[string]any)
	assert.NotEmpty(t, prompt["id"])
	assert.Equal(t, "My Prompt", prompt["name"])
	assert.Equal(t, "my_prompt", prompt["slug"])
	assert.Equal(t, teamID, prompt["team_id"])
	assert.Equal(t, "Useful prompt", prompt["description"])
}

func TestCreatePrompt_CustomSlug(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"My Prompt","slug":"My-Custom_Slug!","description":"Useful prompt"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	prompt := body["prompt"].(map[string]any)
	assert.Equal(t, "my_custom_slug", prompt["slug"])
}

func TestCreatePrompt_Conflict(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Create first prompt
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Unique Name","description":"desc"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 1. Conflict on name
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Unique Name","description":"different"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "prompt with this name or slug already exists; please provide a unique name", body["error"])
	resp.Body.Close()

	// 2. Conflict on slug
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Other Name","slug":"unique_name"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	body = decodeBody(t, resp)
	assert.Equal(t, "prompt with this name or slug already exists; please provide a unique name", body["error"])
	resp.Body.Close()
}

func TestCreatePrompt_MissingName(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"description":"no name"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestCreatePrompt_Unauthorized(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", uuid.New().String()),
		`{"name":"p"}`, "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestListPrompts(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	setupPrompt(t, a, token)
	setupPrompt(t, a, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Len(t, prompts, 2)
}

func TestListPrompts_Empty(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Empty(t, prompts)
}

func TestGetPrompt_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	prompt := body["prompt"].(map[string]any)
	assert.Equal(t, id, prompt["id"])
}

func TestGetPrompt_NotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodGet,
		"/v1/prompts/00000000-0000-0000-0000-000000000001", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestGetPrompt_InvalidID(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodGet, "/v1/prompts/not-a-uuid", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestArchivePrompt(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	p := body["prompt"].(map[string]any)
	assert.Equal(t, "archived", p["status"].(string))
	resp.Body.Close()

	// confirm it still exists and is archived
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body2 := decodeBody(t, resp)
	p2 := body2["prompt"].(map[string]any)
	assert.Equal(t, "archived", p2["status"].(string))
	resp.Body.Close()
}

func TestPrompts_APIKeyAuth(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	apiKey := setupAPIKey(t, a, token)

	// API key works for listing prompts
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(apiKey)))
	ctx := context.Background()
	apiKeyModel, err := store.GetAPIKeyByHash(ctx, hash)
	require.NoError(t, err)
	teamID := apiKeyModel.TeamID.String()

	req := newAPIKeyReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts", teamID), "", apiKey)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestListAllPrompts(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Create a prompt
	setupPrompt(t, a, token)

	// 1. By default with no team_id, nothing is shown
	req := newReq(t, http.MethodGet, "/v1/prompts", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Empty(t, prompts)
	resp.Body.Close()

	// 2. With allowed team_id query parameter, matching prompts are shown
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?team_id=%s", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts = body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	resp.Body.Close()

	// 3. With unallowed team_id query parameter, 403 Forbidden is returned
	unallowedTeamID := uuid.New().String()
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?team_id=%s", unallowedTeamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

func TestArchivePrompt_Permissions(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// 1. Setup Org/Team Admin
	adminToken := setupUser(t, a)
	adminSession, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)
	adminUserID := adminSession.UserID

	// Get the default team
	teams, err := store.GetUserTeams(ctx, adminUserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	teamID := teams[0].ID
	teamIDStr := teamID.String()

	// 2. Setup Editor User
	editorUser, err := store.CreateVerifiedUser(ctx, "prompteditor@px0.dev", "Password123!")
	require.NoError(t, err)
	err = store.AddTeamMember(ctx, teamID, editorUser.ID)
	require.NoError(t, err)
	err = store.UpdateTeamMemberRole(ctx, teamID, editorUser.ID, "editor")
	require.NoError(t, err)

	editorSession, err := store.CreateSession(ctx, editorUser.ID, "sess_p_editor", adminSession.ExpiresAt)
	require.NoError(t, err)
	editorToken := editorSession.Token

	// 3. Setup Viewer User
	viewerUser, err := store.CreateVerifiedUser(ctx, "promptviewer@px0.dev", "Password123!")
	require.NoError(t, err)
	err = store.AddTeamMember(ctx, teamID, viewerUser.ID)
	require.NoError(t, err)
	err = store.UpdateTeamMemberRole(ctx, teamID, viewerUser.ID, "viewer")
	require.NoError(t, err)

	viewerSession, err := store.CreateSession(ctx, viewerUser.ID, "sess_p_viewer", adminSession.ExpiresAt)
	require.NoError(t, err)
	viewerToken := viewerSession.Token

	// 4. Create prompt as editor
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamIDStr),
		`{"name":"Shared Prompt","description":"Prompt to test delete"}`, editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	promptID := body["prompt"].(map[string]any)["id"].(string)
	resp.Body.Close()

	// 5. Viewer (Reader) cannot archive the prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptID), "", viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// 6. Editor (Collaborator) cannot archive the prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptID), "", editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode) // Only Admins can archive prompts!
	resp.Body.Close()

	// 7. Admin (Owner) can archive the prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptID), "", adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body2 := decodeBody(t, resp)
	p := body2["prompt"].(map[string]any)
	assert.Equal(t, "archived", p["status"].(string))
	resp.Body.Close()
}

func TestListAllPrompts_Filters(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Create prompt A
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Prompt Alpha","description":"First test prompt"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	pAID := body["prompt"].(map[string]any)["id"].(string)
	resp.Body.Close()

	// Create prompt B
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Prompt Beta","description":"Second test prompt"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body = decodeBody(t, resp)
	pBID := body["prompt"].(map[string]any)["id"].(string)
	resp.Body.Close()

	// Add a version & set tag 'v1' on Prompt A
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", pAID),
		`{"template":"Alpha body"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/tags", pAID),
		`{"tag":"v1"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Archive Prompt B
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", pBID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Test 1: Query with team and archived=false
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?team=%s&archived=false", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pAID, prompts[0].(map[string]any)["id"].(string))
	resp.Body.Close()

	// Test 2: Query with team_id and archived=true
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?team_id=%s&archived=true", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts = body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pBID, prompts[0].(map[string]any)["id"].(string))
	resp.Body.Close()

	// Test 3: Query with tags=v1
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?team_id=%s&tags=v1", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts = body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pAID, prompts[0].(map[string]any)["id"].(string))
	resp.Body.Close()

	// Test 4: Query with non-existent tag
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?team_id=%s&tags=nonexistent", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts = body["prompts"].([]any)
	assert.Empty(t, prompts)
	resp.Body.Close()
}
