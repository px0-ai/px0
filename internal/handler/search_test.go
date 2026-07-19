package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
)

func TestSearchAcrossAllEntityTypesAndAccessibleProjects(t *testing.T) {
	a := newTestApp(t)
	roles := setupProjectRoles(t, a)
	projectA := uuid.MustParse(createProject(t, a, roles.editorToken, roles.teamID, "Search A"))
	projectB := uuid.MustParse(createProject(t, a, roles.editorToken, roles.teamID, "Search B"))
	ctx := context.Background()

	prompt, err := store.CreatePrompt(ctx, projectA, "refund-prompt", "Refund Prompt", "Handles support")
	require.NoError(t, err)
	skill, err := store.CreateSkill(ctx, projectB, "refund-skill", "Refund Skill", "Handles support")
	require.NoError(t, err)
	tool, err := store.CreateTool(ctx, projectA, "refund-tool", "Refund Tool", "Handles support", "")
	require.NoError(t, err)

	foreignTeam, err := store.CreateTeam(ctx, "Foreign Search Team")
	require.NoError(t, err)
	foreignProject, err := store.CreateProject(ctx, foreignTeam.ID, "foreign-search", "Foreign Search")
	require.NoError(t, err)
	_, err = store.CreateTool(ctx, foreignProject.ID, "private-refund", "Private Refund", "Must not leak", "")
	require.NoError(t, err)

	req := newReq(t, http.MethodGet, "/v1/search?q=refund", "", roles.viewerToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	results := decodeBody(t, resp)["results"].([]any)
	require.Len(t, results, 3)

	actual := make(map[string]string, len(results))
	for _, item := range results {
		result := item.(map[string]any)
		actual[result["type"].(string)] = result["id"].(string)
	}
	assert.Equal(t, prompt.ID.String(), actual["prompt"])
	assert.Equal(t, skill.ID.String(), actual["skill"])
	assert.Equal(t, tool.ID.String(), actual["tool"])
}

func TestSearchFiltersByEntityType(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := uuid.MustParse(setupProject(t, a, token))
	ctx := context.Background()

	_, err := store.CreatePrompt(ctx, projectID, "refund-prompt", "Refund Prompt", "")
	require.NoError(t, err)
	tool, err := store.CreateTool(ctx, projectID, "refund-tool", "Refund Tool", "", "")
	require.NoError(t, err)

	req := newReq(t, http.MethodGet, "/v1/search?q=refund&type=tool", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	results := decodeBody(t, resp)["results"].([]any)
	require.Len(t, results, 1)
	result := results[0].(map[string]any)
	assert.Equal(t, "tool", result["type"])
	assert.Equal(t, tool.ID.String(), result["id"])
}

func TestSearchRejectsMissingQueryAndInvalidType(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	for _, path := range []string{"/v1/search", "/v1/search?q=%20%20"} {
		req := newReq(t, http.MethodGet, path, "", token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "q query parameter is required", decodeBody(t, resp)["error"])
	}

	req := newReq(t, http.MethodGet, "/v1/search?q=refund&type=unknown", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "type must be one of: prompt, skill, tool", decodeBody(t, resp)["error"])
}

func TestSearchExcludesArchivedPrompts(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := uuid.MustParse(setupProject(t, a, token))
	ctx := context.Background()
	prompt, err := store.CreatePrompt(ctx, projectID, "refund", "Refund Prompt", "")
	require.NoError(t, err)
	require.NoError(t, store.ArchivePrompt(ctx, prompt.ID, []uuid.UUID{projectID}))

	req := newReq(t, http.MethodGet, "/v1/search?q=refund&type=prompt", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, decodeBody(t, resp)["results"].([]any))
}

func TestSearchRequiresAuthentication(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodGet, "/v1/search?q=refund", "", "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestSearchInlineTypeFilters(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := uuid.MustParse(setupProject(t, a, token))
	ctx := context.Background()

	prompt, err := store.CreatePrompt(ctx, projectID, "refund-prompt", "Refund Prompt", "Handles support")
	require.NoError(t, err)
	skill, err := store.CreateSkill(ctx, projectID, "refund-skill", "Refund Skill", "Handles support")
	require.NoError(t, err)
	tool, err := store.CreateTool(ctx, projectID, "refund-tool", "Refund Tool", "Handles support", "")
	require.NoError(t, err)

	// Case 1: type:prompt prefix
	{
		req := newReq(t, http.MethodGet, "/v1/search?q=type:prompt%20refund", "", token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		results := decodeBody(t, resp)["results"].([]any)
		require.Len(t, results, 1)
		result := results[0].(map[string]any)
		assert.Equal(t, "prompt", result["type"])
		assert.Equal(t, prompt.ID.String(), result["id"])
	}

	// Case 2: type:tool suffix
	{
		req := newReq(t, http.MethodGet, "/v1/search?q=refund%20type:tool", "", token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		results := decodeBody(t, resp)["results"].([]any)
		require.Len(t, results, 1)
		result := results[0].(map[string]any)
		assert.Equal(t, "tool", result["type"])
		assert.Equal(t, tool.ID.String(), result["id"])
	}

	// Case 3: type:skill case insensitivity and spacing
	{
		req := newReq(t, http.MethodGet, "/v1/search?q=type:%20%20%20SKILL%20refund", "", token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		results := decodeBody(t, resp)["results"].([]any)
		require.Len(t, results, 1)
		result := results[0].(map[string]any)
		assert.Equal(t, "skill", result["type"])
		assert.Equal(t, skill.ID.String(), result["id"])
	}

	// Case 4: invalid type filter
	{
		req := newReq(t, http.MethodGet, "/v1/search?q=type:unknown%20refund", "", token)
		resp, err := a.Test(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "type must be one of: prompt, skill, tool", decodeBody(t, resp)["error"])
	}
}
