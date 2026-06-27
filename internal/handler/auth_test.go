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

func TestRegister_Success(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"new@test.com","password":"Password123!"}`, "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	user := body["user"].(map[string]any)
	assert.Equal(t, "new@test.com", user["email"])
	assert.NotEmpty(t, user["id"])
	assert.Equal(t, false, user["is_verified"])
	assert.Equal(t, true, user["is_admin"]) // Public registers as Admin
}

func TestRegister_DuplicateEmail(t *testing.T) {
	a := newTestApp(t)

	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"dup@test.com","password":"Password123!"}`, "")
	resp, _ := a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"dup@test.com","password":"Password456!"}`, "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestRegister_MissingFields(t *testing.T) {
	a := newTestApp(t)

	cases := []struct {
		body string
	}{
		{`{"email":"","password":"Password123!"}`},
		{`{"email":"a@b.com","password":""}`},
		{`{}`},
	}
	for _, tc := range cases {
		req := newReq(t, http.MethodPost, "/v1/auth/register", tc.body, "")
		resp, err := a.Test(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %s", tc.body)
		resp.Body.Close()
	}
}

func TestRegister_ShortPassword(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"a@b.com","password":"short"}`, "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestRegister_InvalidEmail(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"invalid-email","password":"Password123!"}`, "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "invalid email format", body["error"])
}

func TestRegister_WeakPassword(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"weak-pwd@test.com","password":"password123"}`, "") // lacks uppercase and special char

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "password must contain at least one uppercase letter")
}

func TestRegister_AdminSuccess(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// Set up Organization
	org, err := store.CreateOrganization(ctx, "Admin Org")
	require.NoError(t, err)

	// Set up Team
	team, err := store.CreateTeamWithOrg(ctx, "Admin Team", org.ID)
	require.NoError(t, err)

	// Set up Admin caller user
	pwdHash, _ := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	adminUser, err := store.CreateAdminUser(ctx, "admin-caller@test.com", string(pwdHash), true)
	require.NoError(t, err)

	// Associate admin user to team (making them part of the organization)
	err = store.AddTeamMember(ctx, team.ID, adminUser.ID)
	require.NoError(t, err)

	// Set up Session for Admin
	session, err := store.CreateSession(ctx, adminUser.ID, "valid-admin-token", time.Now().Add(1*time.Hour))
	require.NoError(t, err)

	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"standard-user@test.com","password":"Password456!","team_id":%q}`, team.ID), session.Token)

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	userVal := body["user"].(map[string]any)
	assert.Equal(t, "standard-user@test.com", userVal["email"])
	assert.Equal(t, true, userVal["is_verified"])
	assert.Equal(t, false, userVal["is_admin"])

	members, err := store.GetTeamMembers(ctx, team.ID)
	require.NoError(t, err)
	found := false
	for _, m := range members {
		if m.Email == "standard-user@test.com" {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestRegister_AdminInvalidTeamID(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	pwdHash, _ := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	adminUser, err := store.CreateAdminUser(ctx, "admin-caller2@test.com", string(pwdHash), true)
	require.NoError(t, err)

	session, err := store.CreateSession(ctx, adminUser.ID, "valid-admin-token-2", time.Now().Add(1*time.Hour))
	require.NoError(t, err)

	randomUUID := uuid.New().String()
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"standard-user@test.com","password":"Password456!","team_id":%q}`, randomUUID), session.Token)

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "team not found", body["error"])
}

func TestRegister_AdminTeamNoOrg(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	team, err := store.CreateTeam(ctx, "Team No Org")
	require.NoError(t, err)

	pwdHash, _ := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	adminUser, err := store.CreateAdminUser(ctx, "admin-caller3@test.com", string(pwdHash), true)
	require.NoError(t, err)

	err = store.AddTeamMember(ctx, team.ID, adminUser.ID)
	require.NoError(t, err)

	session, err := store.CreateSession(ctx, adminUser.ID, "valid-admin-token-3", time.Now().Add(1*time.Hour))
	require.NoError(t, err)

	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"standard-user@test.com","password":"Password456!","team_id":%q}`, team.ID), session.Token)

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "team does not belong to any organization", body["error"])
}

func TestRegister_AdminDifferentOrg(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	org1, err := store.CreateOrganization(ctx, "Org One")
	require.NoError(t, err)
	org2, err := store.CreateOrganization(ctx, "Org Two")
	require.NoError(t, err)

	team1, err := store.CreateTeamWithOrg(ctx, "Team One", org1.ID)
	require.NoError(t, err)
	team2, err := store.CreateTeamWithOrg(ctx, "Team Two", org2.ID)
	require.NoError(t, err)

	pwdHash, _ := bcrypt.GenerateFromPassword([]byte("Password123!"), bcrypt.DefaultCost)
	adminUser, err := store.CreateAdminUser(ctx, "admin-caller4@test.com", string(pwdHash), true)
	require.NoError(t, err)

	// Admin belongs to team1 (Org One)
	err = store.AddTeamMember(ctx, team1.ID, adminUser.ID)
	require.NoError(t, err)

	session, err := store.CreateSession(ctx, adminUser.ID, "valid-admin-token-4", time.Now().Add(1*time.Hour))
	require.NoError(t, err)

	// Attempts to register user to team2 (Org Two)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"standard-user@test.com","password":"Password456!","team_id":%q}`, team2.ID), session.Token)

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "user does not belong to the organization of the specified team", body["error"])
}

func TestRegister_PublicForbiddenTeamID(t *testing.T) {
	a := newTestApp(t)
	randomUUID := uuid.New().String()
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"standard-user@test.com","password":"Password456!","team_id":%q}`, randomUUID), "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, "only admins can register users with a team_id", body["error"])
}

func TestRegister_InvalidToken(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"standard-user@test.com","password":"Password456!"}`, "invalid-token-format-or-value")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestLogin_Success(t *testing.T) {
	a := newTestApp(t)

	// register first
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"login@test.com","password":"Password123!"}`, "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	
	body := decodeBody(t, resp)
	userVal := body["user"].(map[string]any)
	userIDStr := userVal["id"].(string)
	resp.Body.Close()

	// Manually verify so login succeeds
	user, err := store.GetUserByEmail(context.Background(), "login@test.com")
	require.NoError(t, err)
	err = store.VerifyUser(context.Background(), user.ID)
	require.NoError(t, err)

	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"login@test.com","password":"Password123!"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	assert.NotEmpty(t, body["token"])
	assert.NotEmpty(t, body["expires_at"])
	assert.NotEmpty(t, body["user"])
	assert.Equal(t, userIDStr, body["user"].(map[string]any)["id"])
}

func TestLogin_WrongPassword(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"wp@test.com","password":"Password123!"}`, "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Manually verify
	user, err := store.GetUserByEmail(context.Background(), "wp@test.com")
	require.NoError(t, err)
	err = store.VerifyUser(context.Background(), user.ID)
	require.NoError(t, err)

	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"wp@test.com","password":"WrongPassword123!"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestLogin_UnknownEmail(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"nobody@test.com","password":"Password123!"}`, "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestLogout(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	// logout invalidates the token
	req := newReq(t, http.MethodDelete, "/v1/auth/session", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// token no longer works
	req = newReq(t, http.MethodGet, "/v1/prompts/00000000-0000-0000-0000-000000000001", "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestMe_WithAccessToken(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodGet, "/v1/auth/me", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	user := body["user"].(map[string]any)
	assert.Equal(t, "test@px0.dev", user["email"])
}

func TestMe_Unauthorized(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodGet, "/v1/auth/me", "", "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestRegister_AndVerifyFlow(t *testing.T) {
	a := newTestApp(t)

	// 1. Register publicly without admin key and without team_id (unverified admin)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"verify-flow@test.com","password":"Password123!"}`, "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	
	body := decodeBody(t, resp)
	userVal := body["user"].(map[string]any)
	assert.Equal(t, false, userVal["is_verified"])
	assert.Equal(t, true, userVal["is_admin"]) // Registered publicly as Admin
	resp.Body.Close()

	// 2. Login should fail (user is not verified, returning generic invalid credentials)
	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"verify-flow@test.com","password":"Password123!"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()

	// 3. Fetch verification code from DB
	user, err := store.GetUserByEmail(context.Background(), "verify-flow@test.com")
	require.NoError(t, err)
	
	code, _, err := store.GetLatestVerificationCode(context.Background(), user.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, code)

	// 4. Verify with incorrect code (should fail)
	req = newReq(t, http.MethodPost, "/v1/auth/verify-email",
		fmt.Sprintf(`{"email":"verify-flow@test.com","code":"invalid%s"}`, code), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 5. Verify with correct code
	req = newReq(t, http.MethodPost, "/v1/auth/verify-email",
		fmt.Sprintf(`{"email":"verify-flow@test.com","code":%q}`, code), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 6. Login should now succeed!
	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"verify-flow@test.com","password":"Password123!"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestTriggerVerification(t *testing.T) {
	a := newTestApp(t)

	// 1. Missing email parameter
	req := newReq(t, http.MethodGet, "/v1/auth/verify-email", "", "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 2. Non-existent email
	req = newReq(t, http.MethodGet, "/v1/auth/verify-email?email=nonexistent@test.com", "", "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// 3. Register unverified user
	req = newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"trigger-flow@test.com","password":"Password123!"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// 4. Trigger verification email (should succeed)
	req = newReq(t, http.MethodGet, "/v1/auth/verify-email?email=trigger-flow@test.com", "", "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 5. Fetch code from DB
	user, err := store.GetUserByEmail(context.Background(), "trigger-flow@test.com")
	require.NoError(t, err)
	
	code, _, err := store.GetLatestVerificationCode(context.Background(), user.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, code)

	// 6. Verify with the code
	req = newReq(t, http.MethodPost, "/v1/auth/verify-email",
		fmt.Sprintf(`{"email":"trigger-flow@test.com","code":%q}`, code), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 7. Trigger again (should fail because user is already verified)
	req = newReq(t, http.MethodGet, "/v1/auth/verify-email?email=trigger-flow@test.com", "", "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}
