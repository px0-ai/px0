package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderLive_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "Hello, {{.name}}! Count: {{.count}}.")

	// promote version 1 to stable and then live
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, _ = a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/render", id),
		`{"variables":{"name":"Alice","count":5}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "Hello, Alice! Count: 5.", body["rendered"])
	assert.Equal(t, float64(1), body["version"])
}

func TestRenderLive_NoLiveVersion(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "draft only") // not published

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/render", id),
		`{"variables":{}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestRenderLive_NoVariables(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "Static prompt with no variables.")

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, _ = a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/render", id),
		`{}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "Static prompt with no variables.", body["rendered"])
}

func TestRenderVersion_Draft(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "Draft: {{.msg}}")

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/render", id),
		`{"variables":{"msg":"hello"}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "Draft: hello", body["rendered"])
}

func TestRenderVersion_NotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/99/render", id),
		`{"variables":{}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestRenderLive_WithAPIKey(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	apiKey := setupAPIKey(t, a, token)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "Hi {{.user}}!")

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	req = newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, _ = a.Test(req)
	resp.Body.Close()

	// render using API key (not session)
	req = newAPIKeyReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/render", id),
		`{"variables":{"user":"Bob"}}`, apiKey)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "Hi Bob!", body["rendered"])
}
