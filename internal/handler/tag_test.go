package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTags_HandlerLifecycle(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)
	setupVersion(t, a, token, id, "Hello, {{.name}}!") // Creates Version 1

	// 1. Set tag "prod" on Version 1
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/tags", id),
		`{"tag":"prod"}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	v := body["version"].(map[string]any)
	assert.Equal(t, float64(1), v["version"])
	tags := v["tags"].([]any)
	assert.Contains(t, tags, "prod")

	// 2. Set invalid tag format
	reqInvalid := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/versions/1/tags", id),
		`{"tag":"invalid tag!"}`, token)
	respInvalid, err := a.Test(reqInvalid)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, respInvalid.StatusCode)
	respInvalid.Body.Close()

	// 3. Resolve version by tag "prod" instead of version number "1"
	reqGetByTag := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions/prod", id), "", token)
	respGetByTag, err := a.Test(reqGetByTag)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respGetByTag.StatusCode)

	bodyGet := decodeBody(t, respGetByTag)
	vGet := bodyGet["version"].(map[string]any)
	assert.Equal(t, float64(1), vGet["version"])
	assert.Contains(t, vGet["tags"].([]any), "prod")

	// 4. Render version using tag "prod"
	reqRenderByTag := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/prompts/%s/versions/prod/render", projectID, slug),
		`{"variables":{"name":"Arpit"}}`, token)
	respRenderByTag, err := a.Test(reqRenderByTag)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respRenderByTag.StatusCode)

	bodyRender := decodeBody(t, respRenderByTag)
	assert.Equal(t, "Hello, Arpit!", bodyRender["rendered"])
	assert.Equal(t, float64(1), bodyRender["version"])

	// 5. List tags
	reqListTags := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/tags", id), "", token)
	respListTags, err := a.Test(reqListTags)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respListTags.StatusCode)

	bodyList := decodeBody(t, respListTags)
	tagsList := bodyList["tags"].([]any)
	require.Len(t, tagsList, 1)
	item := tagsList[0].(map[string]any)
	assert.Equal(t, "prod", item["tag"])
	assert.Equal(t, float64(1), item["version"])

	// 6. Delete tag "prod"
	reqDeleteTag := newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s/tags/prod", id), "", token)
	respDeleteTag, err := a.Test(reqDeleteTag)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, respDeleteTag.StatusCode)
	respDeleteTag.Body.Close()

	// 7. Verify version can no longer be resolved by tag "prod" (returns 404)
	reqGetDeleted := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/versions/prod", id), "", token)
	respGetDeleted, err := a.Test(reqGetDeleted)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, respGetDeleted.StatusCode)
	respGetDeleted.Body.Close()
}
