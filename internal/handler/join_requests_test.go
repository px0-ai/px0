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

func TestJoinRequestsFlow(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// 1. Setup Admin user and Organization/Team
	adminToken := setupUser(t, a) // verified user on "Default Test Org" / "Test Setup Team"

	adminSession, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)
	adminUserID := adminSession.UserID

	// Create a second team in "Default Test Org"
	adminTeams, err := store.GetUserTeams(ctx, adminUserID)
	require.NoError(t, err)
	require.NotEmpty(t, adminTeams)
	orgID := adminTeams[0].OrgID
	require.NotNil(t, orgID)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgID.String()), `{"name":"Engineering Team"}`, adminToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	teamIDStr := body["team"].(map[string]any)["id"].(string)
	teamID, err := uuid.Parse(teamIDStr)
	require.NoError(t, err)

	// Since we created it, let's make adminUserID the team admin
	err = store.AddTeamMember(ctx, teamID, adminUserID)
	require.NoError(t, err)

	// Update admin role to admin on this team
	err = store.UpdateTeamMemberRole(ctx, teamID, adminUserID, "admin")
	require.NoError(t, err)

	// 2. Setup standard applicant user
	applicantEmail := "applicant@px0.dev"
	applicantPassword := "Applicant123!"

	// Register applicant
	req = newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, applicantEmail, applicantPassword), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	body = decodeBody(t, resp)
	applicantUser := body["user"].(map[string]any)
	applicantIDStr := applicantUser["id"].(string)
	applicantID, err := uuid.Parse(applicantIDStr)
	require.NoError(t, err)

	// Verify applicant
	err = store.VerifyUser(ctx, applicantID)
	require.NoError(t, err)

	// Add applicant to default team of organization so they belong to the org
	defaultTeamID := adminTeams[0].ID
	err = store.AddTeamMember(ctx, defaultTeamID, applicantID)
	require.NoError(t, err)

	// Login applicant
	req = newReq(t, http.MethodPost, "/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, applicantEmail, applicantPassword), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	applicantToken := body["token"].(string)

	// 3. List Teams within organization from the applicant user
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/orgs/%s/teams", orgID.String()), "", applicantToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	teamsList := body["teams"].([]any)
	assert.Len(t, teamsList, 2) // Test Setup Team & Engineering Team

	// 4. Submit join request to Engineering Team from applicant user
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/join-requests", teamIDStr), "", applicantToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	reqBody := decodeBody(t, resp)
	requestIDStr := reqBody["id"].(string)
	assert.Equal(t, "pending", reqBody["status"])

	// 5. Verify duplicate request fails
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/join-requests", teamIDStr), "", applicantToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()

	// 6. Get Admin's Inbox and verify the request is there
	req = newReq(t, http.MethodGet, "/v1/me/inbox", "", adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	inboxList := body["inbox"].([]any)
	assert.NotEmpty(t, inboxList)

	found := false
	for _, itemAny := range inboxList {
		item := itemAny.(map[string]any)
		if item["id"].(string) == requestIDStr {
			assert.Equal(t, "join_request", item["type"])
			assert.Equal(t, "pending", item["status"])

			payload, ok := item["payload"].(map[string]any)
			assert.True(t, ok, "payload object should be present")
			assert.Equal(t, applicantEmail, payload["user_email"])

			teamMap, ok := payload["team"].(map[string]any)
			assert.True(t, ok, "embedded team object should be present")
			assert.Equal(t, "Engineering Team", teamMap["name"])
			assert.Equal(t, teamIDStr, teamMap["id"])

			found = true
			break
		}
	}
	assert.True(t, found, "Join request not found in admin inbox")

	// 7. Approve join request
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/join-requests/%s", requestIDStr), `{"status":"approved"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	assert.Equal(t, "approved", body["status"])

	// 8. Verify applicant is now a member of Engineering Team
	isMember, err := store.IsTeamViewer(ctx, applicantID, teamID)
	require.NoError(t, err)
	assert.True(t, isMember, "Applicant should now be a member of the team")
}
