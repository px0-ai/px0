package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAPIKey_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodPost, "/v1/api-keys",
		`{"name":"ci-pipeline"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "ci-pipeline", body["name"])
	assert.NotEmpty(t, body["key"])
	key := body["key"].(string)
	assert.True(t, len(key) > 8, "key should be long enough to be a real key")
	// Full key is only returned on creation; subsequent list should not include it
	assert.NotEmpty(t, body["key_prefix"])
}

func TestCreateAPIKey_MissingName(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodPost, "/v1/api-keys", `{}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestCreateAPIKey_RequiresSession(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	apiKey := setupAPIKey(t, a, token)

	// Creating an API key via an existing API key must be rejected.
	req := newAPIKeyReq(t, http.MethodPost, "/v1/api-keys",
		`{"name":"escalated"}`, apiKey)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestListAPIKeys(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	setupAPIKey(t, a, token)
	setupAPIKey(t, a, token)

	req := newReq(t, http.MethodGet, "/v1/api-keys", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	keys := body["api_keys"].([]any)
	assert.Len(t, keys, 2)

	// Full key must not be returned in list.
	first := keys[0].(map[string]any)
	assert.Nil(t, first["key"])
	assert.NotEmpty(t, first["key_prefix"])
}

func TestDeleteAPIKey(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	// create a key and note its ID
	req := newReq(t, http.MethodPost, "/v1/api-keys", `{"name":"temp"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	body := decodeBody(t, resp)
	id := body["id"].(string)

	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/api-keys/%s", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// confirm it no longer appears in the list
	req = newReq(t, http.MethodGet, "/v1/api-keys", "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	body = decodeBody(t, resp)
	assert.Empty(t, body["api_keys"].([]any))
}

func TestDeleteAPIKey_NotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodDelete,
		"/v1/api-keys/00000000-0000-0000-0000-000000000001", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}
