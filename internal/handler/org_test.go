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

	// 6. Test Admin registration without team_id should error out
	req = newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"admin-error@test.com","password":"Password123!"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
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
	assert.NotEmpty(t, orgsVal)

	firstOrg := orgsVal[0].(map[string]any)
	assert.Equal(t, "Default Test Org", firstOrg["name"])
	assert.Equal(t, "ADMIN", firstOrg["role"])
}
