package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

func TestRolesAndPermissions(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// Setup Admin User
	adminToken := setupUser(t, a) // creates user and verified session

	adminSession, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)

	// Create Organization
	req := newReq(t, http.MethodPost, "/v1/orgs", `{"name":"Duplicate Org Name"}`, adminToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	body := decodeBody(t, resp)
	orgIDStr := body["org"].(map[string]any)["id"].(string)

	// 1. Check duplicate organization name creation
	req = newReq(t, http.MethodPost, "/v1/orgs", `{"name":"Duplicate Org Name"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()

	// 2. Check update organization to duplicate name
	req = newReq(t, http.MethodPost, "/v1/orgs", `{"name":"Other Org Name"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	bodyOther := decodeBody(t, resp)
	otherOrgIDStr := bodyOther["org"].(map[string]any)["id"].(string)

	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/orgs/%s", otherOrgIDStr), `{"name":"Duplicate Org Name"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()

	// Create Team 1
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgIDStr), `{"name":"Engineering"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	bodyTeam := decodeBody(t, resp)
	teamIDStr := bodyTeam["team"].(map[string]any)["id"].(string)
	teamID, _ := uuid.Parse(teamIDStr)

	// 3. Check duplicate team name creation in same org
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgIDStr), `{"name":"Engineering"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()

	// Create Team 2
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgIDStr), `{"name":"Product"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	bodyTeam2 := decodeBody(t, resp)
	team2IDStr := bodyTeam2["team"].(map[string]any)["id"].(string)

	// 4. Check update team to duplicate name in same org
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/teams/%s", team2IDStr), `{"name":"Engineering"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()

	// Setup Editor User
	editorPassword := "EditorPassword123!"
	editorHash, _ := bcrypt.GenerateFromPassword([]byte(editorPassword), bcrypt.DefaultCost)
	editorUser, err := store.CreateVerifiedUser(ctx, "editor@px0.dev", string(editorHash))
	require.NoError(t, err)

	// Add Editor to Team 1. Since count is 1 (admin is there), they join as 'editor'.
	err = store.AddTeamMember(ctx, teamID, editorUser.ID)
	require.NoError(t, err)

	editorSession, err := store.CreateSession(ctx, editorUser.ID, "sess_editor-token", adminSession.ExpiresAt)
	require.NoError(t, err)
	editorToken := editorSession.Token

	// Setup Viewer User
	viewerPassword := "ViewerPassword123!"
	viewerHash, _ := bcrypt.GenerateFromPassword([]byte(viewerPassword), bcrypt.DefaultCost)
	viewerUser, err := store.CreateVerifiedUser(ctx, "viewer@px0.dev", string(viewerHash))
	require.NoError(t, err)

	// Add Viewer to Team 1. Since count is 2, they join as 'editor' by default.
	err = store.AddTeamMember(ctx, teamID, viewerUser.ID)
	require.NoError(t, err)

	// Elevating viewer user to 'viewer' role via API (only admin can do this)
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/teams/%s/members/%s/role", teamIDStr, viewerUser.ID), `{"role":"viewer"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	viewerSession, err := store.CreateSession(ctx, viewerUser.ID, "sess_viewer-token", adminSession.ExpiresAt)
	require.NoError(t, err)
	viewerToken := viewerSession.Token

	// Check Paginated Member Listing API (Viewer, Editor, and Admin can list)
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/members?page=1", teamIDStr), "", viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	membersBody := decodeBody(t, resp)
	assert.Equal(t, float64(1), membersBody["page"])
	assert.Equal(t, float64(10), membersBody["limit"])
	assert.Equal(t, float64(3), membersBody["total"]) // admin, editor, viewer

	membersList := membersBody["members"].([]any)
	assert.Len(t, membersList, 3)

	// Find each member in list and verify role
	var foundAdmin, foundEditor, foundViewer bool
	for _, mAny := range membersList {
		m := mAny.(map[string]any)
		role := m["role"].(string)
		email := m["email"].(string)
		if email == "test@px0.dev" {
			assert.Equal(t, "admin", role)
			foundAdmin = true
		} else if email == "editor@px0.dev" {
			assert.Equal(t, "editor", role)
			foundEditor = true
		} else if email == "viewer@px0.dev" {
			assert.Equal(t, "viewer", role)
			foundViewer = true
		}
	}
	assert.True(t, foundAdmin)
	assert.True(t, foundEditor)
	assert.True(t, foundViewer)

	// 5. Test Permissions on Prompt Actions
	// A project owned by the team; capability on its prompts is the team role.
	promptProject, err := store.CreateProject(ctx, teamID, "roles_project", "Roles Project")
	require.NoError(t, err)
	projectIDStr := promptProject.ID.String()

	// A. Editor can Create Prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectIDStr), `{"name":"Editor Prompt","description":"Prompt by editor"}`, editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	bodyPrompt := decodeBody(t, resp)
	promptIDStr := bodyPrompt["prompt"].(map[string]any)["id"].(string)
	promptSlugStr := bodyPrompt["prompt"].(map[string]any)["slug"].(string)
	promptID, _ := uuid.Parse(promptIDStr)

	// B. Viewer cannot Create Prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts", projectIDStr), `{"name":"Viewer Prompt","description":"Prompt by viewer"}`, viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// C. Viewer can List Prompts
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/projects/%s/prompts", projectIDStr), "", viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	promptsBody := decodeBody(t, resp)
	promptsList := promptsBody["prompts"].([]any)
	assert.NotEmpty(t, promptsList)

	// D. Viewer cannot Create Version
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", promptIDStr), `{"template":"Hello {{.name}}"}`, viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// E. Editor can Create Version
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", promptIDStr), `{"template":"Hello {{.name}}"}`, editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	bodyVersion := decodeBody(t, resp)
	versionNum := int(bodyVersion["version"].(map[string]any)["version"].(float64))

	// F. Viewer cannot Update Version
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s/versions/%d", promptIDStr, versionNum), `{"template":"Updated Hello {{.name}}"}`, viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// G. Editor can Update Version
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s/versions/%d", promptIDStr, versionNum), `{"template":"Updated Hello {{.name}}"}`, editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// H. Viewer cannot Promote Version
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/%d/promote", promptIDStr, versionNum), "", viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// I. Editor can Promote Version
	// First promotion: draft -> stable
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/%d/promote", promptIDStr, versionNum), "", editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Second promotion: stable -> live
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/%d/promote", promptIDStr, versionNum), "", editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// J. Viewer can Render Live
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts/%s/render", projectIDStr, promptSlugStr), `{"variables":{"name":"John"}}`, viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	bodyRender := decodeBody(t, resp)
	assert.Equal(t, "Updated Hello John", bodyRender["rendered"])

	// K. Editor cannot Add Team Member
	randomUser, _ := store.CreateUser(ctx, "random@test.com", "Password123!")
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/teams/%s/members", teamIDStr), fmt.Sprintf(`{"user_id":%q}`, randomUser.ID), editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// L. Editor cannot Elevate Member
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/teams/%s/members/%s/role", teamIDStr, viewerUser.ID), `{"role":"admin"}`, editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// M. Editor cannot Archive Prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptIDStr), "", viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// N. Editor cannot Archive Prompt (GitHub model)
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptIDStr), "", editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// Admin can Archive Prompt
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/archive", promptIDStr), "", adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Verify prompt is archived
	gotPrompt, err := store.GetPromptByID(ctx, promptID, []uuid.UUID{promptProject.ID})
	require.NoError(t, err)
	assert.Equal(t, model.PromptStatusArchived, gotPrompt.Status)

	// O. Try to remove a user from a team who is not part of the organization (returns 400 Bad Request)
	nonOrgUser, _ := store.CreateUser(ctx, "nonorguser@test.com", "Password123!")
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/teams/%s/members/%s", teamIDStr, nonOrgUser.ID), "", adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	bodyRemoveErr := decodeBody(t, resp)
	assert.Equal(t, "user is not a member of this organization", bodyRemoveErr["error"])

	// P. Team Update/Delete Permission Tests
	// 1. Viewer (Team Member) cannot edit team
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/teams/%s", teamIDStr), `{"name":"Viewer Attempted Rename"}`, viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// 2. Viewer (Team Member) cannot delete team
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/teams/%s", teamIDStr), "", viewerToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// 3. Editor (Team Editor) can edit team
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/teams/%s", teamIDStr), `{"name":"Editor Rename"}`, editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	bodyTeamRename := decodeBody(t, resp)
	assert.Equal(t, "Editor Rename", bodyTeamRename["team"].(map[string]any)["name"].(string))

	// 4. Create another temporary team to test editor deletion, because we don't want to delete the main team yet
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgIDStr), `{"name":"Temp Team Editor Del"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	bodyTempTeam := decodeBody(t, resp)
	tempTeamIDStr := bodyTempTeam["team"].(map[string]any)["id"].(string)
	tempTeamID, _ := uuid.Parse(tempTeamIDStr)

	// Add editorUser as an 'editor' to that team
	err = store.AddTeamMember(ctx, tempTeamID, editorUser.ID)
	require.NoError(t, err)
	err = store.UpdateTeamMemberRole(ctx, tempTeamID, editorUser.ID, "editor")
	require.NoError(t, err)

	// Editor can delete the team they are editor of
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/teams/%s", tempTeamIDStr), "", editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// Q. Team Admin of a custom team (not Default Team) cannot create a new team, but Org Admin can
	// First, let's elevate editorUser to 'admin' role on custom team Engineering (teamID)
	// (Only the admin can update roles)
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/teams/%s/members/%s/role", teamIDStr, editorUser.ID), `{"role":"admin"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// editorToken is now a Team Admin of Engineering. They should NOT be able to create a new team in the organization.
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgIDStr), `{"name":"Custom Team Admin Created"}`, editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode) // Rejected! Not an Org Admin.
	resp.Body.Close()

	// But adminToken (Org Admin of Default Team) CAN create a team
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/orgs/%s/teams", orgIDStr), `{"name":"Org Admin Created"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 5. Admin can edit team
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/teams/%s", teamIDStr), `{"name":"Admin Rename"}`, adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	bodyAdminRename := decodeBody(t, resp)
	assert.Equal(t, "Admin Rename", bodyAdminRename["team"].(map[string]any)["name"].(string))

	// 6. Admin can delete team
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/teams/%s", teamIDStr), "", adminToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()
}
