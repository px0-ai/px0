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

// createAPIKeyForTeam creates an "all" API key scoped to a single team and
// returns the raw key.
func createAPIKeyForTeam(t *testing.T, a *testApp, token, orgID, teamID string) string {
	t.Helper()
	req := newReq(t, http.MethodPost, "/v1/api-keys",
		fmt.Sprintf(`{"name":"scoped-key","org_id":%q,"operation":"all","team_ids":[%q]}`, orgID, teamID), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	return decodeBody(t, resp)["key"].(string)
}

// TestAPIKey_ReachesGrantedProject verifies that an API key scoped to a team
// reaches the prompts of a project granted to that team — and loses that reach
// when the grant is revoked or for a project the team cannot access.
func TestAPIKey_ReachesGrantedProject(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	adminToken := setupUser(t, a)
	adminSession, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)
	teams, err := store.GetUserTeams(ctx, adminSession.UserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	ownerTeam := teams[0]
	require.NotNil(t, ownerTeam.OrgID)
	orgID := *ownerTeam.OrgID

	// A grantee team in the same org, and an API key scoped to it.
	granteeTeam, err := store.CreateTeamWithOrg(ctx, "Key Grantee Team", orgID)
	require.NoError(t, err)
	apiKey := createAPIKeyForTeam(t, a, adminToken, orgID.String(), granteeTeam.ID.String())

	// A project owned by ownerTeam (not the key's team) with a prompt inside.
	project, err := store.CreateProject(ctx, ownerTeam.ID, "shared_with_key", "Shared With Key")
	require.NoError(t, err)
	_, err = store.CreatePrompt(ctx, project.ID, "greeting", "Greeting", "")
	require.NoError(t, err)

	listWithKey := func() int {
		req := newAPIKeyReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/prompts", project.ID), "", apiKey)
		resp, err := a.Test(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// 1. Before any grant, the key's team cannot reach the project.
	assert.Equal(t, http.StatusForbidden, listWithKey())

	// 2. After granting the key's team access, the key reaches the prompts.
	require.NoError(t, store.GrantProjectAccess(ctx, project.ID, granteeTeam.ID))
	req := newAPIKeyReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/prompts", project.ID), "", apiKey)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	prompts := decodeBody(t, resp)["prompts"].([]any)
	assert.Len(t, prompts, 1)

	// 3. After revoking the grant, reach is lost again.
	require.NoError(t, store.RevokeProjectAccess(ctx, project.ID, granteeTeam.ID))
	assert.Equal(t, http.StatusForbidden, listWithKey())
}

// TestAPIKey_DeniedForeignProject verifies an API key cannot reach a project
// owned by a team outside its scope with no grant.
func TestAPIKey_DeniedForeignProject(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	adminToken := setupUser(t, a)
	adminSession, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)
	teams, err := store.GetUserTeams(ctx, adminSession.UserID)
	require.NoError(t, err)
	ownerTeam := teams[0]
	orgID := *ownerTeam.OrgID

	// Key scoped to a team that owns and is granted nothing.
	keyTeam, err := store.CreateTeamWithOrg(ctx, "Lonely Key Team", orgID)
	require.NoError(t, err)
	apiKey := createAPIKeyForTeam(t, a, adminToken, orgID.String(), keyTeam.ID.String())

	// A project owned by ownerTeam, never shared with keyTeam.
	project, err := store.CreateProject(ctx, ownerTeam.ID, "private_project", "Private Project")
	require.NoError(t, err)

	req := newAPIKeyReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/prompts", project.ID), "", apiKey)
	resp, err := a.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
