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

func TestOrg_CreateAndEdit(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a) // verified admin user

	// 1. Create Organization
	req := newReq(t, http.MethodPost, "/v1/orgs", `{"name":"Acme Corp"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	org := body["org"].(map[string]any)
	assert.Equal(t, "Acme Corp", org["name"])
	orgIDStr := org["id"].(string)

	// 2. Update Organization
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/orgs/%s", orgIDStr), `{"name":"Acme Industries"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	org = body["org"].(map[string]any)
	assert.Equal(t, "Acme Industries", org["name"])
	assert.Equal(t, orgIDStr, org["id"])

	// 3. Create Team with Org reference
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgIDStr), `{"name":"Dev Team"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body = decodeBody(t, resp)
	team := body["team"].(map[string]any)
	assert.Equal(t, "Dev Team", team["name"])
	assert.Equal(t, orgIDStr, team["org_id"])
	teamIDStr := team["id"].(string)

	// 4. Update Team (change name & org reference)
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/teams/%s", teamIDStr), fmt.Sprintf(`{"name":"Engineers Team","org_id":%q}`, orgIDStr), token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	team = body["team"].(map[string]any)
	assert.Equal(t, "Engineers Team", team["name"])
	assert.Equal(t, orgIDStr, team["org_id"])

	// Add the admin user to the newly created team so they belong to the organization
	adminUser, err := store.GetUserByEmail(context.Background(), "test@px0.dev")
	require.NoError(t, err)

	teamID, err := uuid.Parse(teamIDStr)
	require.NoError(t, err)

	err = store.AddTeamMember(context.Background(), teamID, adminUser.ID)
	require.NoError(t, err)

	// 5. Test Register standard user by Admin with team_id
	req = newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"standard-user@test.com","password":"Password123!","team_id":%q}`, teamIDStr), token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body = decodeBody(t, resp)
	userVal := body["user"].(map[string]any)
	assert.Equal(t, "standard-user@test.com", userVal["email"])
	assert.Equal(t, true, userVal["is_verified"])
	assert.Equal(t, false, userVal["is_admin"])

	// 6. Test Admin registration without team_id should succeed and create a Default Org and Default Team
	req = newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"admin-no-team-id@test.com","password":"Password123!"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 7. Test Non-Admin registration WITH team_id should fail (only admins can pass team_id)
	req = newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"non-admin-error@test.com","password":"Password123!","team_id":%q}`, teamIDStr), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

func TestMe_Orgs(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodGet, "/v1/me/orgs", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	orgsVal := body["organizations"].([]any)
	assert.Len(t, orgsVal, 2)

	firstOrg := orgsVal[0].(map[string]any)
	assert.Equal(t, "Default Org", firstOrg["name"])
	assert.Equal(t, "ADMIN", firstOrg["role"])

	secondOrg := orgsVal[1].(map[string]any)
	assert.Equal(t, "Default Test Org", secondOrg["name"])
	assert.Equal(t, "ADMIN", secondOrg["role"])
}

func TestOrg_People(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a) // verified user on Default Test Org

	// Get organization ID
	req := newReq(t, http.MethodGet, "/v1/me/orgs", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	orgsVal := body["organizations"].([]any)
	require.NotEmpty(t, orgsVal)
	orgIDStr := orgsVal[0].(map[string]any)["id"].(string)

	// Fetch org people
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/orgs/%s/people?page=1&limit=5", orgIDStr), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	peopleVal := body["people"].([]any)
	assert.Len(t, peopleVal, 1) // only 1 user exists so far
	assert.Equal(t, float64(1), body["page"])
	assert.Equal(t, float64(5), body["limit"])
	assert.Equal(t, float64(1), body["total"])

	firstPerson := peopleVal[0].(map[string]any)
	assert.Equal(t, "test@px0.dev", firstPerson["email"])
}

func TestTeam_Delete(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	token := setupUser(t, a) // admin on Default Test Org & Test Setup Team

	// Retrieve session and user info
	session, err := store.GetSessionByToken(ctx, token)
	require.NoError(t, err)
	userID := session.UserID

	// Create a new team
	teams, err := store.GetUserTeams(ctx, userID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	orgID := teams[0].OrgID

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgID.String()), `{"name":"Temp Team"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	teamIDStr := body["team"].(map[string]any)["id"].(string)
	teamID, err := uuid.Parse(teamIDStr)
	require.NoError(t, err)

	// Join team as admin
	err = store.AddTeamMember(ctx, teamID, userID)
	require.NoError(t, err)
	err = store.UpdateTeamMemberRole(ctx, teamID, userID, "admin")
	require.NoError(t, err)

	// Delete team via API
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/teams/%s", teamIDStr), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// Verify team is deleted
	_, err = store.GetTeamByID(ctx, teamID)
	assert.ErrorIs(t, err, store.ErrNotFound)

	// Verify that the user still exists in database
	user, err := store.GetUserByID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, userID, user.ID)
}

func TestOrg_RemoveMember(t *testing.T) {
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
	orgID := teams[0].OrgID
	teamID := teams[0].ID

	// 1. Create a second standard user using register endpoint with adminToken
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"standard@test.com","password":"Password123!","team_id":%q}`, teamID.String()), adminToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	userVal := body["user"].(map[string]any)
	standardUserIDStr := userVal["id"].(string)
	standardUserID, err := uuid.Parse(standardUserIDStr)
	require.NoError(t, err)

	// Verify standard user is in the org now
	inOrgAfter, err := store.IsUserInOrg(ctx, standardUserID, *orgID)
	require.NoError(t, err)
	assert.True(t, inOrgAfter)

	// Log in as standard user to get their token
	req = newReq(t, http.MethodPost, "/v1/auth/login", `{"email":"standard@test.com","password":"Password123!"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	standardToken := decodeBody(t, resp)["token"].(string)

	// 2. Try to remove the user from the org as a non-admin (forbidden)
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/orgs/%s/members/%s", orgID.String(), standardUserID.String()), "", standardToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// 3. Try to remove a non-existent member from the org as admin (not found)
	fakeUserID := uuid.New()
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/orgs/%s/members/%s", orgID.String(), fakeUserID.String()), "", adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// 4. Successfully remove standard user from the org as admin
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/orgs/%s/members/%s", orgID.String(), standardUserID.String()), "", adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// 5. Verify the user has been removed from the organization
	inOrgFinal, err := store.IsUserInOrg(ctx, standardUserID, *orgID)
	require.NoError(t, err)
	assert.False(t, inOrgFinal)
}

func TestDeleteOrganization(t *testing.T) {
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
	orgID := teams[0].OrgID

	// Try to delete with fake ID (not found)
	fakeID := uuid.New()
	req := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/orgs/%s", fakeID.String()), "", adminToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Delete organization successfully
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/orgs/%s", orgID.String()), "", adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify organization is gone
	_, err = store.GetOrganizationByID(ctx, *orgID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}
