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

func TestCreateVersion_WithModelConfig(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Hello, {{.name}}!","model":"openai/gpt-4.1","model_params":{"temperature":0.2,"response_format":{"type":"json_object"}}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "Hello, {{.name}}!", v["template"])
	assert.Equal(t, "openai/gpt-4.1", v["model"])
	assert.Equal(t, map[string]any{
		"temperature":     0.2,
		"response_format": map[string]any{"type": "json_object"},
	}, v["model_params"])
}

func TestCreateVersion_BlankModel(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Hello","model":"  "}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestCreateVersion_InvalidModelParams(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Hello","model_params":["invalid"]}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
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

	// Set tag "prod" on version 1
	reqTag1 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/tags", id), `{"tag":"prod"}`, token)
	respTag1, err := a.Test(reqTag1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respTag1.StatusCode)
	respTag1.Body.Close()

	// Set tag "dev" on version 2
	reqTag2 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/2/tags", id), `{"tag":"dev"}`, token)
	respTag2, err := a.Test(reqTag2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respTag2.StatusCode)
	respTag2.Body.Close()

	// Promote version 1 twice: draft -> stable -> live
	reqPromote1 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	respPromote1, err := a.Test(reqPromote1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respPromote1.StatusCode)
	respPromote1.Body.Close()

	reqPromote2 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	respPromote2, err := a.Test(reqPromote2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respPromote2.StatusCode)
	respPromote2.Body.Close()

	// 1. Verify with no query parameters
	reqAll := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions", id), "", token)
	respAll, err := a.Test(reqAll)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respAll.StatusCode)
	bodyAll := decodeBody(t, respAll)
	versionsAll := bodyAll["versions"].([]any)
	assert.Len(t, versionsAll, 2)

	// 2. Verify status=live
	reqLive := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions?status=live", id), "", token)
	respLive, err := a.Test(reqLive)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respLive.StatusCode)
	bodyLive := decodeBody(t, respLive)
	versionsLive := bodyLive["versions"].([]any)
	require.Len(t, versionsLive, 1)
	assert.Equal(t, "live", versionsLive[0].(map[string]any)["status"])

	// 3. Verify tag=dev
	reqDev := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions?tags=dev", id), "", token)
	respDev, err := a.Test(reqDev)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respDev.StatusCode)
	bodyDev := decodeBody(t, respDev)
	versionsDev := bodyDev["versions"].([]any)
	require.Len(t, versionsDev, 1)
	assert.Equal(t, float64(2), versionsDev[0].(map[string]any)["version"])
}

func TestGetVersion_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "template contents")

	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, float64(1), v["version"])
	assert.Equal(t, "draft", v["status"])
	assert.Equal(t, "template contents", v["template"])
}

func TestUpdateVersion_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "original template")

	req := newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s/versions/1", id),
		`{"template":"updated template"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "updated template", v["template"])
}

func TestUpdateVersion_ModelConfigOnly(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "original template")

	req := newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s/versions/1", id),
		`{"model":"openai/gpt-4.1-mini","model_params":{"temperature":0.5}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "original template", v["template"])
	assert.Equal(t, "openai/gpt-4.1-mini", v["model"])
	assert.Equal(t, map[string]any{"temperature": 0.5}, v["model_params"])
}

func TestUpdateVersion_NonDraftRejected(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "original template")

	// promote version 1 to stable
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	// try to update stable version
	req = newReq(t, http.MethodPut,
		fmt.Sprintf("/v1/prompts/%s/versions/1", id),
		`{"template":"should fail"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	resp.Body.Close()
}

func TestPromoteVersion(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "my template")

	// Promote 1: draft -> stable
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "stable", v["status"])
	assert.NotNil(t, v["published_at"])
	resp.Body.Close()

	// Promote 2: stable -> live
	req = newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = decodeBody(t, resp)
	v = body["version"].(map[string]any)
	assert.Equal(t, "live", v["status"])
	resp.Body.Close()
}

func TestPromoteVersion_DemotesPreviousLive(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "v1")
	setupVersion(t, a, token, id, "v2")

	// promote v1 to stable then live
	req1 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp1, _ := a.Test(req1)
	resp1.Body.Close()
	req2 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp2, _ := a.Test(req2)
	resp2.Body.Close()

	// promote v2 to stable then live - should demote v1 to stable
	req3 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/2/promote", id), "", token)
	resp3, _ := a.Test(req3)
	resp3.Body.Close()
	req4 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/2/promote", id), "", token)
	resp4, err := a.Test(req4)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp4.StatusCode)
	resp4.Body.Close()

	// v1 should be stable
	req := newReq(t, http.MethodGet,
		fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "stable", v["status"])
}

func TestPromoteVersion_AlreadyLive(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "template")

	// promote draft -> stable
	req1 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp1, _ := a.Test(req1)
	resp1.Body.Close()

	// promote stable -> live
	req2 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp2, _ := a.Test(req2)
	resp2.Body.Close()

	// try to promote again
	req3 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp3, err := a.Test(req3)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp3.StatusCode)
	resp3.Body.Close()
}

func TestDemoteVersion_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "template")

	// draft -> stable
	req1 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp1, _ := a.Test(req1)
	resp1.Body.Close()

	// stable -> live
	req2 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp2, _ := a.Test(req2)
	resp2.Body.Close()

	// demote live -> stable
	req3 := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/demote", id), "", token)
	resp3, err := a.Test(req3)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp3.StatusCode)

	body := decodeBody(t, resp3)
	v := body["version"].(map[string]any)
	assert.Equal(t, "stable", v["status"])
	resp3.Body.Close()
}

func TestArchiveVersion_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "template")

	// archive version 1
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/archive", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "archived", v["status"])
	resp.Body.Close()
}

func TestDeleteVersion_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "draft to delete")

	req := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// confirm it is deleted
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestDeleteVersion_NonDraftSoftArchived(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "template")

	// draft -> stable
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/promote", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	// try to delete stable version - should soft archive instead of fail
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// verify version still exists but status is archived
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, "archived", v["status"])
	resp.Body.Close()
}

func TestDeleteVersion_NotFound(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)

	req := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s/versions/99", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestDuplicateVersion_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "my source template content")

	// Set a tag on version 1
	reqTag := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/tags", id), `{"tag":"source-tag"}`, token)
	respTag, err := a.Test(reqTag)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respTag.StatusCode)
	respTag.Body.Close()

	// Duplicate version 1
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/duplicate", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, float64(2), v["version"])
	assert.Equal(t, "draft", v["status"])
	assert.Equal(t, "my source template content", v["template"])
	assert.Empty(t, v["tags"])
	resp.Body.Close()

	// Verify that original version's tag is still there
	reqOrig := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	respOrig, err := a.Test(reqOrig)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respOrig.StatusCode)
	bodyOrig := decodeBody(t, respOrig)
	vOrig := bodyOrig["version"].(map[string]any)
	assert.Equal(t, []any{"source-tag"}, vOrig["tags"])
	respOrig.Body.Close()
}

func TestDuplicateVersion_Errors(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "template content")

	// 1. Prompt not found
	req := newReq(t, http.MethodPost, "/v1/prompts/00000000-0000-0000-0000-000000000001/versions/1/duplicate", "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// 2. Version not found
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/99/duplicate", id), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// 3. Unauthorized (no token)
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/duplicate", id), "", "")
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}
