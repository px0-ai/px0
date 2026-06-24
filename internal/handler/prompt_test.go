package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatePrompt_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodPost, "/v1/prompts",
		`{"name":"My Prompt","description":"Useful prompt"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	prompt := body["prompt"].(map[string]any)
	assert.NotEmpty(t, prompt["id"])
	assert.Equal(t, "My Prompt", prompt["name"])
	assert.Equal(t, "Useful prompt", prompt["description"])
}

func TestCreatePrompt_MissingName(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodPost, "/v1/prompts",
		`{"description":"no name"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestCreatePrompt_Unauthorized(t *testing.T) {
	a := newTestApp(t)
	req := newReq(t, http.MethodPost, "/v1/prompts",
		`{"name":"p"}`, "")

	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestListPrompts(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	setupPrompt(t, a, token)
	setupPrompt(t, a, token)

	req := newReq(t, http.MethodGet, "/v1/prompts", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Len(t, prompts, 2)
}

func TestListPrompts_Empty(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodGet, "/v1/prompts", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	prompts := body["prompts"].([]any)
	assert.Empty(t, prompts)
}

func TestGetPrompt_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	prompt := body["prompt"].(map[string]any)
	assert.Equal(t, id, prompt["id"])
}

func TestGetPrompt_NotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodGet,
		"/v1/prompts/00000000-0000-0000-0000-000000000001", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestGetPrompt_InvalidID(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodGet, "/v1/prompts/not-a-uuid", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestDeletePrompt(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// confirm it's gone
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestPrompts_APIKeyAuth(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	apiKey := setupAPIKey(t, a, token)

	// API key works for listing prompts
	req := newAPIKeyReq(t, http.MethodGet, "/v1/prompts", "", apiKey)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}
