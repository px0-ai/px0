package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	resp, _ := a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"login@test.com","password":"password123"}`, "")
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.NotEmpty(t, body["token"])
	assert.NotEmpty(t, body["expires_at"])
	assert.NotEmpty(t, body["user"])
}

func TestLogin_WrongPassword(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/auth/register",
		`{"email":"wp@test.com","password":"password123"}`, "")
	resp, _ := a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost, "/v1/auth/login",
		`{"email":"wp@test.com","password":"wrongpassword"}`, "")
	resp, err := a.Test(req)
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
