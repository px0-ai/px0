package handler_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/search"
	"github.com/px0-ai/px0/internal/search/hf"
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

	// 1. Search with Q=billing. The default NoopProvider does not implement
	// FTS, so this must surface as 501 — the previous behaviour of
	// silently falling back to ILIKE is no longer reachable.
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?q=billing", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	body = decodeBody(t, resp)
	assert.Contains(t, body["error"], "fts search not implemented")
	resp.Body.Close()

	// 2. Archive tech prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", techPromptID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 3. Default search (no q, no vector) — plain listing, only active: Support Bot
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	assert.Equal(t, "Support Bot", prompts[0].(map[string]any)["name"])
	assert.Equal(t, "fts", body["engine"])

	// 4. Listing with archived=true — plain listing, only archived: Tech Bot
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
	// Enable postgres FTS provider for this test
	t.Setenv("SEARCH_FTS_PROVIDER", "postgres")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "noop")

	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Build the real Postgres FTS provider and a noop vector provider
	// and wire both to search.
	ctx := context.Background()
	fts, err := searchfactory.NewFTSProvider(ctx)
	require.NoError(t, err)
	vec, err := searchfactory.NewVectorProvider(ctx)
	require.NoError(t, err)
	search.Init(fts, vec)

	// Clean up global state after test
	t.Cleanup(func() {
		search.Init(search.NoopProvider{}, search.NoopProvider{})
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

func TestListPrompts_InvalidVector(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?vector=abc", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "invalid vector parameter")
}

func TestListPrompts_VectorUnsupportedProvider(t *testing.T) {
	// Active provider is NoopProvider by default in tests. A vector
	// request must surface the unavailability as 501, not silently
	// fall back to the FTS / listing path.
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	setupPrompt(t, a, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?vector=0.1,0.2,0.3", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "vector search not implemented")
}

func TestListPrompts_EngineFieldDefault(t *testing.T) {
	// No ?q= and no ?vector= → plain listing with engine=fts.
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	setupPrompt(t, a, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "fts", body["engine"], "default mode must report engine=fts")
}

func TestListPrompts_InvalidMode(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?mode=bogus", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "invalid mode")
}

func TestListPrompts_ModeHybridReturns501(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?mode=hybrid&q=hello", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "hybrid search not implemented")
}

func TestListPrompts_ModeVectorNoQueryNoVector(t *testing.T) {
	// mode=vector with neither vector= nor q= must 400 with a clear error,
	// not silently fall through to FTS.
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?mode=vector", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "vector mode requires")
}

func TestListPrompts_ModeVectorNoEmbedder(t *testing.T) {
	// mode=vector with a q but no embedder registered must 501,
	// not silently fall through to FTS or listing.
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/prompts?mode=vector&q=hello", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "vector search unavailable")
	assert.Contains(t, body["error"], "no embedder configured")
}

func TestListAllPrompts_EngineField(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	req := newReq(t, http.MethodGet, "/v1/prompts", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "fts", body["engine"], "no-team list should also report engine=fts")
	assert.Empty(t, body["prompts"])

	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?team_id=%s", teamID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	assert.Equal(t, "fts", body["engine"])
}

// TestListPrompts_FTSMode_DoesNotAutoEmbed is the regression test for
// the Phase 2 fix. Before the fix, mode=fts with an embedder registered
// would silently auto-embed the text query and pass it as a vector to
// the FTS provider, which would then return 501 (vector not supported).
// After the fix, mode=fts must NOT touch the embedder at all — the FTS
// provider receives a plain text SearchQuery with Vector unset.
//
// We assert this by:
//  1. Setting a noop vector provider (so any vector path would 501).
//  2. Registering a real (HF) embedder.
//  3. Sending mode=fts&q=hello and asserting we get a 200 from the FTS
//     path, not a 501 from the vector path. With the auto-embed bug
//     the query would reach the FTS provider as a vector and 501.
func TestListPrompts_FTSMode_DoesNotAutoEmbed(t *testing.T) {
	t.Setenv("EMBEDDER_PROVIDER", "huggingface")
	t.Setenv("HF_TOKEN", "fake-token-for-test") // any non-empty value
	t.Setenv("SEARCH_FTS_PROVIDER", "postgres")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "noop")

	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// Wire the real FTS provider and a noop vector provider.
	ctx := context.Background()
	fts, err := searchfactory.NewFTSProvider(ctx)
	require.NoError(t, err)
	vec, err := searchfactory.NewVectorProvider(ctx)
	require.NoError(t, err)
	search.Init(fts, vec)

	// Register an HF embedder (the auto-embed bug's trigger).
	search.SetEmbedder(hf.NewEmbedder())
	t.Cleanup(func() {
		search.SetEmbedder(nil)
		search.Init(search.NoopProvider{}, search.NoopProvider{})
	})

	// Create a prompt so the FTS query has something to find.
	createReq := newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/prompts", teamID),
		`{"name":"FTS Mode No Auto Embed","description":"hello world"}`, token)
	createResp, err := a.Test(createReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, createResp.StatusCode)
	createResp.Body.Close()

	// mode=fts with a text query. With the auto-embed bug, this would
	// silently embed the query and pass it as a vector to the FTS
	// provider, which would 501. After the fix, the FTS provider
	// receives a plain text query and does FTS.
	req := newReq(t, http.MethodGet,
		fmt.Sprintf("/v1/teams/%s/prompts?mode=fts&q=hello", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)

	// The response should be a real FTS result (200) or an empty FTS
	// result (200), NOT a 501 from the FTS provider receiving a vector.
	// An empty result is possible because the tsvector may not match
	// the user's plain text in a way that exceeds the rank threshold.
	assert.NotEqual(t, http.StatusNotImplemented, resp.StatusCode,
		"mode=fts must not auto-embed; got 501 (auto-embed bug still present)")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "fts", body["engine"])
}

// TestListPrompts_FTS_ORSemantics verifies that the Postgres FTS provider
// uses OR semantics (any term match) with a minimum ts_rank threshold,
// replacing the previous AND semantics that made multi-word natural-language
// queries return zero results when not every token appeared in the document.
func TestListPrompts_FTS_ORSemantics(t *testing.T) {
	// Wire the real Postgres FTS provider so search.GetFTS() is not Noop.
	t.Setenv("SEARCH_FTS_PROVIDER", "postgres")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "noop")
	t.Setenv("EMBEDDER_PROVIDER", "")

	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	ctx := context.Background()
	fts, err := searchfactory.NewFTSProvider(ctx)
	require.NoError(t, err)
	vec, err := searchfactory.NewVectorProvider(ctx)
	require.NoError(t, err)
	search.Init(fts, vec)
	t.Cleanup(func() {
		search.Init(search.NoopProvider{}, search.NoopProvider{})
	})

	create := func(name, desc string) string {
		req := newReq(t, http.MethodPost,
			fmt.Sprintf("/v1/teams/%s/prompts", teamID),
			fmt.Sprintf(`{"name":%q,"description":%q}`, name, desc), token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		body := decodeBody(t, resp)
		return body["prompt"].(map[string]any)["id"].(string)
	}

	create("Bug Tracker", "Helps users report software bugs, app crashes, and installation issues.")
	create("Crash Reporter", "Collects details on app crashes, bugs, and failed installs.")
	create("Glossary Lookup", "Defines technical terms, APIs, and acronyms used across the platform.")
	create("Leave Assistant", "Answers questions about sick leave, vacation days, and sick day policy.")
	create("Billing Support", "Assists customers with billing errors, payment failures, and refund requests.")
	create("Weather Widget", "Shows current temperature and forecast for the user location.")

	searchAndAssert := func(query string, wantNames ...string) {
		u := fmt.Sprintf("/v1/teams/%s/prompts?mode=fts&q=%s", teamID, url.QueryEscape(query))
		req := newReq(t, http.MethodGet, u, "", token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body := decodeBody(t, resp)
		assert.Equal(t, "fts", body["engine"])

		prompts := body["prompts"].([]any)
		got := make(map[string]bool)
		for _, p := range prompts {
			name := p.(map[string]any)["name"].(string)
			got[name] = true
		}
		for _, want := range wantNames {
			assert.True(t, got[want], "query %q: expected %q in results", query, want)
		}
	}

	searchAndAssert("app keeps crashing", "Bug Tracker", "Crash Reporter")
	searchAndAssert("define API", "Glossary Lookup")
	searchAndAssert("vacation days remaining", "Leave Assistant")
	searchAndAssert("refund for a failed payment", "Billing Support")
}

// TestListPrompts_FTS_ORSemantics_RegressionGuard verifies that OR
// semantics do not produce false positives for out-of-domain queries.
func TestListPrompts_FTS_ORSemantics_RegressionGuard(t *testing.T) {
	t.Setenv("SEARCH_FTS_PROVIDER", "postgres")
	t.Setenv("SEARCH_VECTOR_PROVIDER", "noop")
	t.Setenv("EMBEDDER_PROVIDER", "")

	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	ctx := context.Background()
	fts, err := searchfactory.NewFTSProvider(ctx)
	require.NoError(t, err)
	vec, err := searchfactory.NewVectorProvider(ctx)
	require.NoError(t, err)
	search.Init(fts, vec)
	t.Cleanup(func() {
		search.Init(search.NoopProvider{}, search.NoopProvider{})
	})

	create := func(name, desc string) {
		req := newReq(t, http.MethodPost,
			fmt.Sprintf("/v1/teams/%s/prompts", teamID),
			fmt.Sprintf(`{"name":%q,"description":%q}`, name, desc), token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		resp.Body.Close()
	}

	create("Bug Tracker", "Helps users report software bugs, app crashes, and installation issues.")
	create("Leave Assistant", "Answers questions about sick leave, vacation days, and sick day policy.")

	// "quantum physics homework help" must return no results.
	req := newReq(t, http.MethodGet,
		fmt.Sprintf("/v1/teams/%s/prompts?mode=fts&q=%s", teamID,
			url.QueryEscape("quantum physics homework help")), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Empty(t, prompts, "quantum physics homework help should return no results")

	// "crshing" (typo) — FTS dictionary cannot stem "crshing" to "crash".
	// Expected: no results. Typo-tolerance is out of scope.
	req = newReq(t, http.MethodGet,
		fmt.Sprintf("/v1/teams/%s/prompts?mode=fts&q=%s", teamID,
			url.QueryEscape("crshing")), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts = body["prompts"].([]any)
	assert.Empty(t, prompts, "crshing (typo) should return no results — typo tolerance out of scope")
}

// TestListPrompts_VectorMode_StillRequiresEmbedder confirms that the
// Phase 2 fix did not regress mode=vector: with no embedder, mode=vector
// + q= still 501s (resolveVectorFromQuery is still the sole path that
// embeds a text query into a vector).
func TestListPrompts_VectorMode_StillRequiresEmbedder(t *testing.T) {
	// Make sure no embedder is registered.
	t.Setenv("EMBEDDER_PROVIDER", "")
	t.Setenv("HF_TOKEN", "")

	a := newTestApp(t)
	token := setupUser(t, a)
	teamID := setupUserTeam(t, token)

	// mode=vector + q but no embedder should 501.
	req := newReq(t, http.MethodGet,
		fmt.Sprintf("/v1/teams/%s/prompts?mode=vector&q=hello", teamID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "no embedder configured")
}

