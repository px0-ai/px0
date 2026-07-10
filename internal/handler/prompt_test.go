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
	projectID := setupProject(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
		`{"name":"My Prompt","description":"Useful prompt"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	prompt := body["prompt"].(map[string]any)
	assert.NotEmpty(t, prompt["id"])
	assert.Equal(t, "My Prompt", prompt["name"])
	assert.Equal(t, "my_prompt", prompt["slug"])
	assert.Equal(t, projectID, prompt["project_id"])
	assert.Equal(t, "Useful prompt", prompt["description"])
}

func TestCreatePrompt_CustomSlug(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
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
	projectID := setupProject(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
		`{"name":"Unique Name","description":"desc"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 1. Conflict on name
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
		`{"name":"Unique Name","description":"different"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "prompt with this name or slug already exists; please provide a unique name", body["error"])
	resp.Body.Close()

	// 2. Conflict on slug
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
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
	projectID := setupProject(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
		`{"description":"no name"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestCreatePrompt_ForbiddenForViewer(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	projectID := createProject(t, a, r.editorToken, r.teamID, "Evals")

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
		`{"name":"Blocked"}`, r.viewerToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

func TestCreatePrompt_UnknownProject(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", uuid.New().String()),
		`{"name":"Orphan"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestCreatePrompt_Unauthorized(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", uuid.New().String()),
		`{"name":"p"}`, "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestListPrompts(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	setupPromptInProject(t, a, token, projectID)
	setupPromptInProject(t, a, token, projectID)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/prompts", projectID), "", token)
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
	projectID := setupProject(t, a, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/prompts", projectID), "", token)
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

	// A project owned by the API key's team, reachable by the key.
	projectID := setupProject(t, a, token)

	req := newAPIKeyReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/prompts", projectID), "", apiKey)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Sanity: the API key model is scoped to a team (unchanged).
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(apiKey)))
	apiKeyModel, err := store.GetAPIKeyByHash(context.Background(), hash)
	require.NoError(t, err)
	assert.NotNil(t, apiKeyModel.TeamID)
}

func TestListAllPrompts(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	setupPromptInProject(t, a, token, projectID)

	// 1. By default with no project, nothing is shown
	req := newReq(t, http.MethodGet, "/v1/prompts", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Empty(t, prompts)
	resp.Body.Close()

	// 2. With allowed project_id query parameter, matching prompts are shown
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?project_id=%s", projectID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts = body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	resp.Body.Close()

	// 3. With unallowed project_id query parameter, 403 Forbidden is returned
	unallowedProjectID := uuid.New().String()
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?project_id=%s", unallowedProjectID), "", token)
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

	// A project owned by the team, plus a prompt created by the editor.
	projectID := setupProject(t, a, adminToken)
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
		`{"name":"Shared Prompt","description":"Prompt to test archive"}`, editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	promptID := body["prompt"].(map[string]any)["id"].(string)
	resp.Body.Close()

	// 5. Viewer cannot archive the prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptID), "", viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// 6. Editor cannot archive the prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptID), "", editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode) // Only Admins can archive prompts!
	resp.Body.Close()

	// 7. Admin can archive the prompt
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
	projectID := setupProject(t, a, token)

	// Create prompt A
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
		`{"name":"Prompt Alpha","description":"First test prompt"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	pAID := body["prompt"].(map[string]any)["id"].(string)
	resp.Body.Close()

	// Create prompt B
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
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

	// Test 1: Query with project and archived=false
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?project=%s&archived=false", projectID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Len(t, prompts, 1)
	assert.Equal(t, pAID, prompts[0].(map[string]any)["id"].(string))
	resp.Body.Close()

	// Test 2: Query with project_id and archived=true
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts?project_id=%s&archived=true", projectID), "", token)
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

	// 4. Create project and prompt as editor
	projectID := setupProject(t, a, adminToken)
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
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

	sourceProjectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, sourceProjectID)
	targetProjectID := setupProject(t, a, token)

	reqMove := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/move", id),
		fmt.Sprintf(`{"project_id":%q}`, targetProjectID), token)
	respMove, err := a.Test(reqMove)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respMove.StatusCode)
	bodyMove := decodeBody(t, respMove)
	pMove := bodyMove["prompt"].(map[string]any)
	assert.Equal(t, targetProjectID, pMove["project_id"].(string))
	respMove.Body.Close()
}

func TestMovePrompt_TargetCollision(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	sourceProjectID := setupProject(t, a, token)
	targetProjectID := setupProject(t, a, token)

	// Same name/slug in both projects.
	makePrompt := func(projectID string) string {
		req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectID),
			`{"name":"Greeting"}`, token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		return decodeBody(t, resp)["prompt"].(map[string]any)["id"].(string)
	}
	id := makePrompt(sourceProjectID)
	makePrompt(targetProjectID)

	reqMove := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/move", id),
		fmt.Sprintf(`{"project_id":%q}`, targetProjectID), token)
	resp, err := a.Test(reqMove)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
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
