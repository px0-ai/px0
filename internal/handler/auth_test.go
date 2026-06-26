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

func TestRegister_Success(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"new@test.com","password":"password123"}`, "")

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
		`{"email":"dup@test.com","password":"password123"}`, "")
	resp, _ := a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"dup@test.com","password":"password456"}`, "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestRegister_MissingFields(t *testing.T) {
	a := newTestApp(t)

	cases := []struct {
		body string
	}{
		{`{"email":"","password":"password123"}`},
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

func TestLogin_Success(t *testing.T) {
	a := newTestApp(t)

	// register first
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"login@test.com","password":"password123"}`, "")
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
		`{"email":"login@test.com","password":"password123"}`, "")
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
		`{"email":"wp@test.com","password":"password123"}`, "")
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
		`{"email":"wp@test.com","password":"wrongpassword"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestLogin_UnknownEmail(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"nobody@test.com","password":"password123"}`, "")

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
	req = newReq(t, http.MethodGet, "/v1/prompts", "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestMe_WithSession(t *testing.T) {
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

func TestRegister_BypassVerificationWithAdminKey(t *testing.T) {
	a := newTestApp(t)

	// Create a team first since admins MUST pass a team_id when registering a user
	ctx := context.Background()
	team, err := store.CreateTeam(ctx, "Bypass Verification Team")
	require.NoError(t, err)
	
	// register with admin key as bearer token and pass team_id
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"bypass-bearer@test.com","password":"password123","team_id":%q}`, team.ID), "test_admin_key")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	
	body := decodeBody(t, resp)
	user := body["user"].(map[string]any)
	assert.Equal(t, true, user["is_verified"])
	resp.Body.Close()

	// should be able to login directly
	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"bypass-bearer@test.com","password":"password123"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// register with admin key as X-API-Key header and pass team_id
	req = newAPIKeyReq(t, http.MethodPost, "/v1/auth/register",
		fmt.Sprintf(`{"email":"bypass-header@test.com","password":"password123","team_id":%q}`, team.ID), "test_admin_key")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	
	body = decodeBody(t, resp)
	user = body["user"].(map[string]any)
	assert.Equal(t, true, user["is_verified"])
	resp.Body.Close()
}

func TestRegister_AndVerifyFlow(t *testing.T) {
	a := newTestApp(t)

	// 1. Register publicly without admin key and without team_id (unverified admin)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"verify-flow@test.com","password":"password123"}`, "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	
	body := decodeBody(t, resp)
	userVal := body["user"].(map[string]any)
	assert.Equal(t, false, userVal["is_verified"])
	assert.Equal(t, true, userVal["is_admin"]) // Registered publicly as Admin
	resp.Body.Close()

	// 2. Login should fail (user is not verified)
	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"verify-flow@test.com","password":"password123"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// 3. Fetch verification code from DB
	user, err := store.GetUserByEmail(context.Background(), "verify-flow@test.com")
	require.NoError(t, err)
	
	code, _, err := store.GetLatestVerificationCode(context.Background(), user.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, code)

	// 4. Verify with incorrect code (should fail)
	req = newReq(t, http.MethodPost, "/v1/auth/verify",
		fmt.Sprintf(`{"email":"verify-flow@test.com","code":"invalid%s"}`, code), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// 5. Verify with correct code
	req = newReq(t, http.MethodPost, "/v1/auth/verify",
		fmt.Sprintf(`{"email":"verify-flow@test.com","code":%q}`, code), "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 6. Login should now succeed!
	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"verify-flow@test.com","password":"password123"}`, "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
