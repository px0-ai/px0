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

	// Publish version 1 to make its status "live"
	reqPublish := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/publish", id), "", token)
	respPublish, err := a.Test(reqPublish)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respPublish.StatusCode)
	respPublish.Body.Close()

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
	vLive := versionsLive[0].(map[string]any)
	assert.Equal(t, float64(1), vLive["version"])
	assert.Equal(t, "live", vLive["status"])

	// 3. Verify status=draft
	reqDraft := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions?status=draft", id), "", token)
	respDraft, err := a.Test(reqDraft)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respDraft.StatusCode)
	bodyDraft := decodeBody(t, respDraft)
	versionsDraft := bodyDraft["versions"].([]any)
	require.Len(t, versionsDraft, 1)
	vDraft := versionsDraft[0].(map[string]any)
	assert.Equal(t, float64(2), vDraft["version"])
	assert.Equal(t, "draft", vDraft["status"])

	// 4. Verify tags=dev
	reqDevTag := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions?tags=dev", id), "", token)
	respDevTag, err := a.Test(reqDevTag)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respDevTag.StatusCode)
	bodyDevTag := decodeBody(t, respDevTag)
	versionsDevTag := bodyDevTag["versions"].([]any)
	require.Len(t, versionsDevTag, 1)
	vDevTag := versionsDevTag[0].(map[string]any)
	assert.Equal(t, float64(2), vDevTag["version"])

	// 5. Verify tags=prod
	reqProdTag := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions?tags=prod", id), "", token)
	respProdTag, err := a.Test(reqProdTag)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respProdTag.StatusCode)
	bodyProdTag := decodeBody(t, respProdTag)
	versionsProdTag := bodyProdTag["versions"].([]any)
	require.Len(t, versionsProdTag, 1)
	vProdTag := versionsProdTag[0].(map[string]any)
	assert.Equal(t, float64(1), vProdTag["version"])

	// 6. Verify tags=nonexistent
	reqNonexistentTag := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions?tags=nonexistent", id), "", token)
	respNonexistentTag, err := a.Test(reqNonexistentTag)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respNonexistentTag.StatusCode)
	bodyNonexistentTag := decodeBody(t, respNonexistentTag)
	versionsNonexistentTag := bodyNonexistentTag["versions"].([]any)
	assert.Empty(t, versionsNonexistentTag)

	// 7. Verify combined status=live&tags=prod
	reqCombinedSuccess := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions?status=live&tags=prod", id), "", token)
	respCombinedSuccess, err := a.Test(reqCombinedSuccess)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respCombinedSuccess.StatusCode)
	bodyCombinedSuccess := decodeBody(t, respCombinedSuccess)
	versionsCombinedSuccess := bodyCombinedSuccess["versions"].([]any)
	require.Len(t, versionsCombinedSuccess, 1)
	vCombined := versionsCombinedSuccess[0].(map[string]any)
	assert.Equal(t, float64(1), vCombined["version"])

	// 8. Verify combined status=live&tags=dev
	reqCombinedFail := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions?status=live&tags=dev", id), "", token)
	respCombinedFail, err := a.Test(reqCombinedFail)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respCombinedFail.StatusCode)
	bodyCombinedFail := decodeBody(t, respCombinedFail)
	versionsCombinedFail := bodyCombinedFail["versions"].([]any)
	assert.Empty(t, versionsCombinedFail)
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

func TestDeleteVersion_LiveRejected(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	id := setupPrompt(t, a, token)
	setupVersion(t, a, token, id, "template")

	// publish version 1
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/publish", id), "", token)
	resp, _ := a.Test(req)
	resp.Body.Close()

	// try to delete live version - should fail
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s/versions/1", id), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
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
