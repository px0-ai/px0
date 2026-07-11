package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/px0-ai/px0/internal/store"
)

func TestLeaveTeam(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	adminToken := setupUser(t, a) // verified admin user on Default Test Org & Test Setup Team

	// Get organization and team IDs
	session, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)
	adminUserID := session.UserID

	teams, err := store.GetUserTeams(ctx, adminUserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	teamID := teams[0].ID

	// Create a standard (non-admin) user under this team
	email := "standard-member@px0.dev"
	password := "Password123!"
	reqReg := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q,"team_id":%q}`, email, password, teamID.String()), adminToken)
	respReg, err := a.Test(reqReg)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, respReg.StatusCode)
	bodyReg := decodeBody(t, respReg)
	userIDStr := bodyReg["user"].(map[string]any)["id"].(string)
	memberUserID, err := uuid.Parse(userIDStr)
	require.NoError(t, err)
	respReg.Body.Close()

	// Log in as standard user to get their token
	reqLogin := newReq(t, http.MethodPost, "/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), "")
	respLogin, err := a.Test(reqLogin)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, respLogin.StatusCode)
	memberToken := decodeBody(t, respLogin)["token"].(string)
	respLogin.Body.Close()

	// 1. Try to leave a non-existent team
	fakeTeamID := uuid.New()
	reqLeaveFake := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/me/teams/%s", fakeTeamID.String()), "", memberToken)
	respLeaveFake, err := a.Test(reqLeaveFake)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, respLeaveFake.StatusCode)
	respLeaveFake.Body.Close()

	// 2. Leave the team successfully
	reqLeave := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/me/teams/%s", teamID.String()), "", memberToken)
	respLeave, err := a.Test(reqLeave)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, respLeave.StatusCode)
	respLeave.Body.Close()

	// 3. Verify user is no longer in the team
	isMember, err := store.IsTeamViewer(ctx, memberUserID, teamID)
	require.NoError(t, err)
	assert.False(t, isMember)
}

func TestCreateTeam_Success(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	adminToken := setupUser(t, a) // verified admin user on Default Test Org & Test Setup Team

	// Get organization ID
	session, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)
	adminUserID := session.UserID

	teams, err := store.GetUserTeams(ctx, adminUserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	orgID := teams[0].OrgID
	require.NotNil(t, orgID)

	// Create a new team
	reqBody := `{"name":"New Engineering Team"}`
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgID.String()), reqBody, adminToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	teamData := body["team"].(map[string]any)
	assert.Equal(t, "New Engineering Team", teamData["name"])
	teamIDStr := teamData["id"].(string)
	teamID, err := uuid.Parse(teamIDStr)
	require.NoError(t, err)
	resp.Body.Close()

	// Verify the creator (adminUserID) is automatically added as an admin of the new team
	members, err := store.GetTeamMembers(ctx, teamID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, adminUserID, members[0].ID)

	// Verify their role is 'admin' (by checking that IsTeamViewer returns true)
	isViewer, err := store.IsTeamViewer(ctx, adminUserID, teamID)
	require.NoError(t, err)
	assert.True(t, isViewer)
}
