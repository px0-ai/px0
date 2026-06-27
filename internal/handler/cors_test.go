package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCORS_AllowedOrigin(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:3001")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "http://localhost:3001", resp.Header.Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
}

func TestCORS_Preflight(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodOptions, "/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:3001")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type, Authorization")

	// Preflight requests might not be documented under OPTIONS in the API spec,
	// so we can test against raw app.App directly to bypass AssertContract validation.
	resp, err := app.App.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Fiber's CORS preflight returns 204 No Content or 200 OK
	require.True(t, resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK)
	require.Equal(t, "http://localhost:3001", resp.Header.Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
	require.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), "GET")
	require.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), "POST")
}

func TestCORS_DisallowedOrigin(t *testing.T) {
	app := newTestApp(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Origin", "http://disallowed.com")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	// For disallowed origins, Access-Control-Allow-Origin should not match the input origin.
	require.NotEqual(t, "http://disallowed.com", resp.Header.Get("Access-Control-Allow-Origin"))
}
