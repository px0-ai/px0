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

	"github.com/px0-ai/px0/internal/search"
	"github.com/px0-ai/px0/internal/searchfactory"
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
	// Note: It is intentional that name/slug reuse is blocked globally across all statuses (including archived).
	// This ensures `GET /v1/prompts/:slug` remains stable and never returns multiple rows or ambiguous results for historical lookups.
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

func TestListPrompts_Filters(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Create prompt 1: "billing"
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Support Bot","description":"Handles billing issues"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	// Create prompt 2: "technical"
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Tech Bot","description":"Handles technical issues"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)

	body := decodeBody(t, resp)
	techPromptID := body["prompt"].(map[string]any)["id"].(string)

	// 1. Search with Q=billing
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?q=billing", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	assert.Equal(t, "Support Bot", prompts[0].(map[string]any)["name"])

	// 2. Archive tech prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", techPromptID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 3. Default search (should only return active: Support Bot)
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts = body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	assert.Equal(t, "Support Bot", prompts[0].(map[string]any)["name"])

	// 4. Search with archived=true
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?archived=true", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts = body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	assert.Equal(t, "Tech Bot", prompts[0].(map[string]any)["name"])
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

func TestGetPrompt_BySlugAndVersion(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	slug := getPromptSlug(t, a, id, token)

	// Create a version and tag it
	setupVersion(t, a, token, id, "Hello {{.name}}")
	
	// Set tag "prod" on Version 1
	reqTag := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/tags", id),
		`{"tag":"prod"}`, token)
	respTag, err := a.Test(reqTag)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respTag.StatusCode)
	respTag.Body.Close()

	// 1. Get by Slug only
	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s", slug), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, slug, body["prompt"].(map[string]any)["slug"])
	assert.Nil(t, body["version"])
	resp.Body.Close()

	// 2. Get by Slug with version query parameter
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s?version=1", slug), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	assert.Equal(t, slug, body["prompt"].(map[string]any)["slug"])
	assert.NotNil(t, body["version"])
	assert.Equal(t, float64(1), body["version"].(map[string]any)["version"])
	assert.Equal(t, "Hello {{.name}}", body["version"].(map[string]any)["template"])
	resp.Body.Close()

	// 3. Get by Slug with tag query parameter
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s?tag=prod", slug), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	assert.Equal(t, slug, body["prompt"].(map[string]any)["slug"])
	assert.NotNil(t, body["version"])
	assert.Equal(t, float64(1), body["version"].(map[string]any)["version"])
	resp.Body.Close()

	// 4. Get by Slug with invalid version
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s?version=99", slug), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
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

func TestGetPrompt_InvalidIDAndSlugNotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodGet, "/v1/prompts/non-existent-slug", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
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
}

func TestUpdatePrompt_Handler(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// 1. Setup Admin Token & Team
	adminToken := setupUser(t, a)
	teamIDStr := setupUserTeam(t, adminToken)
	teamID := uuid.MustParse(teamIDStr)

	// Get admin session to match expiration
	adminSession, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)

	// 2. Setup Editor User & Token
	editorUser, err := store.CreateVerifiedUser(ctx, "prompteditortest@px0.dev", "Password123!")
	require.NoError(t, err)
	err = store.AddTeamMember(ctx, teamID, editorUser.ID)
	require.NoError(t, err)
	err = store.UpdateTeamMemberRole(ctx, teamID, editorUser.ID, "editor")
	require.NoError(t, err)

	editorSession, err := store.CreateSession(ctx, editorUser.ID, "sess_p_editor_test", adminSession.ExpiresAt)
	require.NoError(t, err)
	editorToken := editorSession.Token

	// 3. Setup Viewer User & Token
	viewerUser, err := store.CreateVerifiedUser(ctx, "promptviewertest@px0.dev", "Password123!")
	require.NoError(t, err)
	err = store.AddTeamMember(ctx, teamID, viewerUser.ID)
	require.NoError(t, err)
	err = store.UpdateTeamMemberRole(ctx, teamID, viewerUser.ID, "viewer")
	require.NoError(t, err)

	viewerSession, err := store.CreateSession(ctx, viewerUser.ID, "sess_p_viewer_test", adminSession.ExpiresAt)
	require.NoError(t, err)
	viewerToken := viewerSession.Token

	// 4. Create prompt as editor
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamIDStr),
		`{"name":"Shared Test Prompt","description":"Initial description"}`, editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	promptID := body["prompt"].(map[string]any)["id"].(string)
	resp.Body.Close()

	// 5. Viewer cannot update prompt (Forbidden)
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s", promptID),
		`{"description":"Updating description"}`, viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// 6. Editor can update prompt (Success)
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s", promptID),
		`{"description":"Updated description"}`, editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompt := body["prompt"].(map[string]any)
	assert.Equal(t, "Shared Test Prompt", prompt["name"]) // remains unchanged
	assert.Equal(t, "shared_test_prompt", prompt["slug"]) // remains unchanged
	assert.Equal(t, "Updated description", prompt["description"])
	resp.Body.Close()

	// 7. Try to update non-existent prompt ID
	nonExistentID := "00000000-0000-0000-0000-000000000001"
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s", nonExistentID),
		`{"description":"Updated description"}`, editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestDeletePrompt_Handler(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// 1. Setup Admin Token & Team
	adminToken := setupUser(t, a)
	teamIDStr := setupUserTeam(t, adminToken)
	teamID := uuid.MustParse(teamIDStr)

	// Get admin session to match expiration
	adminSession, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)

	// 2. Setup Editor User & Token
	editorUser, err := store.CreateVerifiedUser(ctx, "promptdeleteeditor@px0.dev", "Password123!")
	require.NoError(t, err)
	err = store.AddTeamMember(ctx, teamID, editorUser.ID)
	require.NoError(t, err)
	err = store.UpdateTeamMemberRole(ctx, teamID, editorUser.ID, "editor")
	require.NoError(t, err)

	editorSession, err := store.CreateSession(ctx, editorUser.ID, "sess_p_delete_editor", adminSession.ExpiresAt)
	require.NoError(t, err)
	editorToken := editorSession.Token

	// 3. Setup Viewer User & Token
	viewerUser, err := store.CreateVerifiedUser(ctx, "promptdeleteviewer@px0.dev", "Password123!")
	require.NoError(t, err)
	err = store.AddTeamMember(ctx, teamID, viewerUser.ID)
	require.NoError(t, err)
	err = store.UpdateTeamMemberRole(ctx, teamID, viewerUser.ID, "viewer")
	require.NoError(t, err)

	viewerSession, err := store.CreateSession(ctx, viewerUser.ID, "sess_p_delete_viewer", adminSession.ExpiresAt)
	require.NoError(t, err)
	viewerToken := viewerSession.Token

	// 4. Create prompt as editor
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamIDStr),
		`{"name":"Delete Test Prompt","description":"Initial description"}`, editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	promptID := body["prompt"].(map[string]any)["id"].(string)
	resp.Body.Close()

	// 5. Viewer cannot delete prompt (Forbidden)
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s", promptID), "", viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// 6. Editor can delete prompt (Success)
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s", promptID), "", editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// 7. Try to delete already deleted prompt (Not Found)
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s", promptID), "", editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestRestorePrompt(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	// Archive first
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Restore
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/restore", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	p := body["prompt"].(map[string]any)
	assert.Equal(t, "active", p["status"].(string))
	resp.Body.Close()
}

func TestMovePrompt(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	// Let's create a new team under the user's organization.
	reqOrgs := newReq(t, http.MethodGet, "/v1/me/orgs", "", token)
	respOrgs, err := a.Test(reqOrgs)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respOrgs.StatusCode)
	bodyOrgs := decodeBody(t, respOrgs)
	orgs := bodyOrgs["organizations"].([]any)
	require.NotEmpty(t, orgs)
	orgID := orgs[0].(map[string]any)["id"].(string)
	respOrgs.Body.Close()

	// Create a new target team under that org
	reqTeam := newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgID), `{"name":"Target Team"}`, token)
	respTeam, err := a.Test(reqTeam)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respTeam.StatusCode)
	bodyTeam := decodeBody(t, respTeam)
	targetTeamID := bodyTeam["team"].(map[string]any)["id"].(string)
	respTeam.Body.Close()

	// Move prompt to the target team
	reqMove := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/move", id), fmt.Sprintf(`{"team_id":"%s"}`, targetTeamID), token)
	respMove, err := a.Test(reqMove)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respMove.StatusCode)
	bodyMove := decodeBody(t, respMove)
	pMove := bodyMove["prompt"].(map[string]any)
	assert.Equal(t, targetTeamID, pMove["team_id"].(string))
	respMove.Body.Close()
}

func TestDiffVersions(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	// Create version 1
	v1 := setupVersion(t, a, token, id, "Hello {{ .name }}")
	// Create version 2
	v2 := setupVersion(t, a, token, id, "Hello {{ .name }}!\nWelcome to px0.")

	// Request diff
	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions/diff?from=%d&to=%d", id, v1, v2), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, float64(v1), body["from_version"])
	assert.Equal(t, float64(v2), body["to_version"])
	assert.Contains(t, body["diff"].(string), "Hello {{ .name }}")
	assert.Contains(t, body["diff"].(string), "Welcome to px0.")
	resp.Body.Close()
}

func TestSearchPrompts_FTS(t *testing.T) {
	// Enable postgres search provider for this test
	t.Setenv("SEARCH_PROVIDER", "postgres")

	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Build the real Postgres provider and wire it to search.Default
	ctx := context.Background()
	p, err := searchfactory.NewProvider(ctx)
	require.NoError(t, err)
	search.Init(p)

	// Clean up global state after test
	t.Cleanup(func() {
		search.Init(search.NoopProvider{})
	})

	// Create 3 prompts with differing names/descriptions/status
	// 1. Matches "database" in name (Highest rank)
	req1 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Relational Database Tutorial","description":"A guide to tables."}`, token)
	resp1, err := a.Test(req1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp1.StatusCode)
	resp1.Body.Close()

	// 2. Matches "database" in description (Medium rank)
	req2 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Active Record Guide","description":"How to query the database."}`, token)
	resp2, err := a.Test(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp2.StatusCode)
	resp2.Body.Close()

	// 3. Matches "database" in description, but is archived
	req3 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"Legacy System Docs","description":"Details about old database."}`, token)
	resp3, err := a.Test(req3)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp3.StatusCode)
	body3 := decodeBody(t, resp3)
	p3ID := body3["prompt"].(map[string]any)["id"].(string)
	resp3.Body.Close()

	// Archive the third prompt
	reqArch := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", p3ID), "", token)
	respArch, err := a.Test(reqArch)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respArch.StatusCode)
	respArch.Body.Close()

	// --- Query 0: Search "database" with NO status filter → defaults to active, so 2 matches returned ---
	reqSearch0 := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?q=database", teamID), "", token)
	respSearch0, err := a.Test(reqSearch0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respSearch0.StatusCode)
	bodySearch0 := decodeBody(t, respSearch0)
	prompts0 := bodySearch0["prompts"].([]any)
	// No status filter → defaults to active
	require.Len(t, prompts0, 2)
	respSearch0.Body.Close()

	// --- Query 1: Search "database" with status=active → only the 2 active prompts, rank-ordered ---
	reqSearch1 := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?q=database&status=active", teamID), "", token)
	respSearch1, err := a.Test(reqSearch1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respSearch1.StatusCode)

	bodySearch1 := decodeBody(t, respSearch1)
	prompts1 := bodySearch1["prompts"].([]any)
	require.Len(t, prompts1, 2)

	// Rank order: Prompt 1 ("Relational Database Tutorial") matches "database" in Name (Weight A).
	// Prompt 2 ("Active Record Guide") matches in Description (Weight B). Prompt 1 must rank first.
	assert.Equal(t, "Relational Database Tutorial", prompts1[0].(map[string]any)["name"])
	assert.Equal(t, "Active Record Guide", prompts1[1].(map[string]any)["name"])
	respSearch1.Body.Close()

	// --- Query 2: Search for "database" with status=archived (should return the archived matching prompt) ---
	reqSearch2 := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?q=database&status=archived", teamID), "", token)
	respSearch2, err := a.Test(reqSearch2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respSearch2.StatusCode)

	bodySearch2 := decodeBody(t, respSearch2)
	prompts2 := bodySearch2["prompts"].([]any)
	require.Len(t, prompts2, 1)
	assert.Equal(t, "Legacy System Docs", prompts2[0].(map[string]any)["name"])
	assert.Equal(t, p3ID, prompts2[0].(map[string]any)["id"].(string))
	respSearch2.Body.Close()

	// --- Query 3: Search for "database" with no matches ---
	reqSearch3 := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?q=nonexistentkeyword", teamID), "", token)
	respSearch3, err := a.Test(reqSearch3)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respSearch3.StatusCode)

	bodySearch3 := decodeBody(t, respSearch3)
	assert.Empty(t, bodySearch3["prompts"].([]any))
	respSearch3.Body.Close()
}

