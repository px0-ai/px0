package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/px0-ai/px0/internal/store"
)

// projectRoles holds tokens for users at each capability level on a shared team,
// plus an outsider who is a member of no team.
type projectRoles struct {
	adminToken    string
	editorToken   string
	viewerToken   string
	outsiderToken string
	teamID        string
	orgID         uuid.UUID
}

// setupProjectRoles builds a team (via the admin from setupUser) with an editor,
// a viewer, and an unrelated outsider, returning a token for each.
func setupProjectRoles(t *testing.T, a *testApp) projectRoles {
	t.Helper()
	ctx := context.Background()

	adminToken := setupUser(t, a)
	adminSession, err := store.GetSessionByToken(ctx, adminToken)
	require.NoError(t, err)

	teams, err := store.GetUserTeams(ctx, adminSession.UserID)
	require.NoError(t, err)
	require.NotEmpty(t, teams)
	teamID := teams[0].ID
	require.NotNil(t, teams[0].OrgID)
	orgID := *teams[0].OrgID

	newMember := func(email, role string, join bool) string {
		hash, err := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
		require.NoError(t, err)
		u, err := store.CreateVerifiedUser(ctx, email, string(hash))
		require.NoError(t, err)
		if join {
			require.NoError(t, store.AddTeamMember(ctx, teamID, u.ID))
			if role != "" {
				require.NoError(t, store.UpdateTeamMemberRole(ctx, teamID, u.ID, role))
			}
		}
		sess, err := store.CreateSession(ctx, u.ID, "sess_"+uuid.NewString(), adminSession.ExpiresAt)
		require.NoError(t, err)
		return sess.Token
	}

	return projectRoles{
		adminToken:    adminToken,
		editorToken:   newMember("proj-editor@px0.dev", "editor", true),
		viewerToken:   newMember("proj-viewer@px0.dev", "viewer", true),
		outsiderToken: newMember("proj-outsider@px0.dev", "", false),
		teamID:        teamID.String(),
		orgID:         orgID,
	}
}

// makeTeamMember creates a verified user in the given team (defaulting to the
// role AddTeamMember assigns) and returns a session token for them.
func makeTeamMember(t *testing.T, teamID uuid.UUID, email string, expiresAt time.Time) string {
	t.Helper()
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	require.NoError(t, err)
	u, err := store.CreateVerifiedUser(ctx, email, string(hash))
	require.NoError(t, err)
	require.NoError(t, store.AddTeamMember(ctx, teamID, u.ID))
	sess, err := store.CreateSession(ctx, u.ID, "sess_"+uuid.NewString(), expiresAt)
	require.NoError(t, err)
	return sess.Token
}

func sessionExpiry(t *testing.T, token string) time.Time {
	t.Helper()
	sess, err := store.GetSessionByToken(context.Background(), token)
	require.NoError(t, err)
	return sess.ExpiresAt
}

func createProjectBody(teamID, name string) string {
	return fmt.Sprintf(`{"team_id":%q,"name":%q}`, teamID, name)
}

func TestCreateProject_Success(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodPost, "/v1/projects", createProjectBody(r.teamID, "Evals"), r.editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	project := decodeBody(t, resp)["project"].(map[string]any)
	assert.NotEmpty(t, project["id"])
	assert.Equal(t, r.teamID, project["owning_team_id"])
	assert.Equal(t, "Evals", project["name"])
	assert.Equal(t, "evals", project["slug"])
}

func TestCreateProject_ForbiddenForViewer(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodPost, "/v1/projects", createProjectBody(r.teamID, "Evals"), r.viewerToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestCreateProject_ForbiddenForNonMember(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodPost, "/v1/projects", createProjectBody(r.teamID, "Evals"), r.outsiderToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestCreateProject_MissingName(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodPost, "/v1/projects", fmt.Sprintf(`{"team_id":%q}`, r.teamID), r.editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestCreateProject_Duplicate(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodPost, "/v1/projects", createProjectBody(r.teamID, "Evals"), r.editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	req = newReq(t, http.MethodPost, "/v1/projects", createProjectBody(r.teamID, "Evals"), r.editorToken)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestCreateProject_Unauthorized(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodPost, "/v1/projects", createProjectBody(r.teamID, "Evals"), "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// createProject is a small helper that creates a project and returns its ID.
func createProject(t *testing.T, a *testApp, token, teamID, name string) string {
	t.Helper()
	req := newReq(t, http.MethodPost, "/v1/projects", createProjectBody(teamID, name), token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	return decodeBody(t, resp)["project"].(map[string]any)["id"].(string)
}

func TestGetProject_Success(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	req := newReq(t, http.MethodGet, "/v1/projects/"+id, "", r.viewerToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, id, decodeBody(t, resp)["project"].(map[string]any)["id"])
}

func TestGetProject_NotFoundForNonMember(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	req := newReq(t, http.MethodGet, "/v1/projects/"+id, "", r.outsiderToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetProject_NotFound(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodGet, "/v1/projects/"+uuid.NewString(), "", r.editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeleteProject_Success(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	req := newReq(t, http.MethodDelete, "/v1/projects/"+id, "", r.adminToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Confirm it is gone.
	_, err = store.GetProjectByID(context.Background(), uuid.MustParse(id))
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteProject_ForbiddenForNonAdmin(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	req := newReq(t, http.MethodDelete, "/v1/projects/"+id, "", r.editorToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestDeleteProject_NotFound(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodDelete, "/v1/projects/"+uuid.NewString(), "", r.adminToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestListTeamProjects_Success(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	createProject(t, a, r.editorToken, r.teamID, "Evals")
	createProject(t, a, r.editorToken, r.teamID, "Prod")

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/projects", r.teamID), "", r.viewerToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	projects := decodeBody(t, resp)["projects"].([]any)
	assert.Len(t, projects, 2)
}

func TestListTeamProjects_ForbiddenForNonMember(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/teams/%s/projects", r.teamID), "", r.outsiderToken)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func grantAccessBody(teamID string) string {
	return fmt.Sprintf(`{"team_id":%q}`, teamID)
}

func TestGrantProjectAccess_Success(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	ctx := context.Background()
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	// A second team in the same org, with a member who is not on the owning team.
	grantee, err := store.CreateTeamWithOrg(ctx, "Grantee Team", r.orgID)
	require.NoError(t, err)
	granteeToken := makeTeamMember(t, grantee.ID, "grantee-member@px0.dev", sessionExpiry(t, r.adminToken))

	// Before the grant, the grantee member cannot reach the project.
	resp, err := a.Test(newReq(t, http.MethodGet, "/v1/projects/"+id, "", granteeToken))
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Owning-team admin grants access.
	resp, err = a.Test(newReq(t, http.MethodPost, "/v1/projects/"+id+"/access", grantAccessBody(grantee.ID.String()), r.adminToken))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Now the grantee member can reach the project.
	resp, err = a.Test(newReq(t, http.MethodGet, "/v1/projects/"+id, "", granteeToken))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGrantProjectAccess_ForbiddenForNonAdmin(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	ctx := context.Background()
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	grantee, err := store.CreateTeamWithOrg(ctx, "Grantee Team", r.orgID)
	require.NoError(t, err)

	resp, err := a.Test(newReq(t, http.MethodPost, "/v1/projects/"+id+"/access", grantAccessBody(grantee.ID.String()), r.editorToken))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestGrantProjectAccess_RejectsTeamOutsideOrg(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	ctx := context.Background()
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	// A team in a different organization.
	foreignOrg, err := store.CreateOrganization(ctx, "Foreign Org")
	require.NoError(t, err)
	foreign, err := store.CreateTeamWithOrg(ctx, "Foreign Team", foreignOrg.ID)
	require.NoError(t, err)

	resp, err := a.Test(newReq(t, http.MethodPost, "/v1/projects/"+id+"/access", grantAccessBody(foreign.ID.String()), r.adminToken))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestGrantProjectAccess_RejectsOrgLessOwner(t *testing.T) {
	a := newTestApp(t)
	setupProjectRoles(t, a) // establishes an admin/session baseline
	ctx := context.Background()

	// An org-less owning team with an admin member and a private project.
	orgLess, err := store.CreateTeam(ctx, "Org-less Team")
	require.NoError(t, err)
	ownerToken := makeTeamMember(t, orgLess.ID, "orgless-admin@px0.dev", time.Now().Add(time.Hour))
	project, err := store.CreateProject(ctx, orgLess.ID, "private", "Private")
	require.NoError(t, err)

	// Some other team to attempt to share with.
	other, err := store.CreateTeam(ctx, "Some Other Team")
	require.NoError(t, err)

	resp, err := a.Test(newReq(t, http.MethodPost, "/v1/projects/"+project.ID.String()+"/access", grantAccessBody(other.ID.String()), ownerToken))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestGrantProjectAccess_UnknownProject(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)

	resp, err := a.Test(newReq(t, http.MethodPost, "/v1/projects/"+uuid.NewString()+"/access", grantAccessBody(r.teamID), r.adminToken))
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRevokeProjectAccess_Success(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	ctx := context.Background()
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	grantee, err := store.CreateTeamWithOrg(ctx, "Grantee Team", r.orgID)
	require.NoError(t, err)
	granteeToken := makeTeamMember(t, grantee.ID, "grantee-member@px0.dev", sessionExpiry(t, r.adminToken))

	require.NoError(t, store.GrantProjectAccess(ctx, uuid.MustParse(id), grantee.ID))

	// Revoke, then the grantee loses reachability.
	resp, err := a.Test(newReq(t, http.MethodDelete, fmt.Sprintf("/v1/projects/%s/access/%s", id, grantee.ID), "", r.adminToken))
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	resp, err = a.Test(newReq(t, http.MethodGet, "/v1/projects/"+id, "", granteeToken))
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRevokeProjectAccess_ForbiddenForNonAdmin(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	ctx := context.Background()
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	grantee, err := store.CreateTeamWithOrg(ctx, "Grantee Team", r.orgID)
	require.NoError(t, err)
	require.NoError(t, store.GrantProjectAccess(ctx, uuid.MustParse(id), grantee.ID))

	resp, err := a.Test(newReq(t, http.MethodDelete, fmt.Sprintf("/v1/projects/%s/access/%s", id, grantee.ID), "", r.editorToken))
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestRevokeProjectAccess_NotFound(t *testing.T) {
	a := newTestApp(t)
	r := setupProjectRoles(t, a)
	id := createProject(t, a, r.editorToken, r.teamID, "Evals")

	// No grant exists for this team.
	resp, err := a.Test(newReq(t, http.MethodDelete, fmt.Sprintf("/v1/projects/%s/access/%s", id, uuid.NewString()), "", r.adminToken))
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
