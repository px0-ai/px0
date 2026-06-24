package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateVersion_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Hello, {{.name}}!"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, float64(1), v["version"])
	assert.Equal(t, "draft", v["status"])
	assert.Equal(t, "Hello, {{.name}}!", v["template"])
}

func TestCreateVersion_InvalidTemplate(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"{{.unclosed"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestCreateVersion_MissingTemplate(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

func TestCreateVersion_PromptNotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)

	req := newReq(t, http.MethodPost,
		"/v1/prompts/00000000-0000-0000-0000-000000000001/versions",
		`{"template":"hello"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestListVersions(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "v1 template")
	setupVersion(t, a, token, id, "v2 template")

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	versions := body["versions"].([]any)
	assert.Len(t, versions, 2)
}

func TestGetVersion(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "my template")

	req := newReq(t, http.MethodGet,
		fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "my template", v["template"])
}

func TestGetVersion_NotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodGet,
		fmt.Sprintf("/v1/prompts/%s/versions/99", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestUpdateVersion_Draft(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "original template")

	req := newReq(t, http.MethodPut,
		fmt.Sprintf("/v1/prompts/%s/versions/1", id),
		`{"template":"updated template"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "updated template", v["template"])
}

func TestUpdateVersion_LiveVersionRejected(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "original template")

	// publish version 1
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/publish", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	// try to update live version
	req = newReq(t, http.MethodPut,
		fmt.Sprintf("/v1/prompts/%s/versions/1", id),
		`{"template":"should fail"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	resp.Body.Close()
}

func TestPublishVersion(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "my template")

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/publish", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "live", v["status"])
	assert.NotNil(t, v["published_at"])
}

func TestPublishVersion_ArchivesPreviousLive(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "v1")
	setupVersion(t, a, token, id, "v2")

	// publish v1
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/publish", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	// publish v2 - should archive v1
	req = newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/2/publish", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// v1 should be archived
	req = newReq(t, http.MethodGet,
		fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "archived", v["status"])
}

func TestPublishVersion_AlreadyLive(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "template")

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/publish", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	// try to publish again
	req = newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/publish", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	resp.Body.Close()
}
