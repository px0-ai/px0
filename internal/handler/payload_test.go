package handler_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreatePromptPayload_Success(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	promptID := setupPrompt(t, a, token)

	bodyStr := `{"variables":{"user":"Arpit","role":"Admin"}}`
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/payloads", promptID), bodyStr, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	payload := body["payload"].(map[string]any)
	assert.NotEmpty(t, payload["id"])
	assert.Equal(t, promptID, payload["prompt_id"])
	assert.Nil(t, payload["name"])
	assert.Equal(t, map[string]any{"user": "Arpit", "role": "Admin"}, payload["variables"])
}

func TestCreatePromptPayload_ValidationErrors(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	promptID := setupPrompt(t, a, token)

	// 1. Missing variables
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/payloads", promptID), `{}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "payload variables are required", body["error"])

	// 2. Invalid variables (not valid JSON)
	req = newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/payloads", promptID), `{"variables":{invalid}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body = decodeBody(t, resp)
	assert.Equal(t, "invalid request body", body["error"])
}

func TestPromptPayload_CRUD_Integration(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	promptID := setupPrompt(t, a, token)

	// 1. Create Payload
	req := newReq(t, http.MethodPost, fmt.Sprintf("/v1/prompts/%s/payloads", promptID),
		`{"variables":{"test":"ok"}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	body := decodeBody(t, resp)
	payloadID := body["payload"].(map[string]any)["id"].(string)

	// 2. Get Payload
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/payloads/%s", promptID, payloadID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	payload := body["payload"].(map[string]any)
	assert.Nil(t, payload["name"])
	assert.Equal(t, map[string]any{"test": "ok"}, payload["variables"])

	// 3. Update Payload (Optional Name & Variables)
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s/payloads/%s", promptID, payloadID),
		`{"name":"Updated Sample Payload","variables":{"updated_key":"updated_val"}}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	payload = body["payload"].(map[string]any)
	assert.Equal(t, "Updated Sample Payload", payload["name"])
	assert.Equal(t, map[string]any{"updated_key": "updated_val"}, payload["variables"])

	// 4. List Payloads
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/payloads", promptID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body = decodeBody(t, resp)
	payloads := body["payloads"].([]any)
	assert.Len(t, payloads, 1)
	assert.Equal(t, "Updated Sample Payload", payloads[0].(map[string]any)["name"])

	// 5. Delete Payload
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s/payloads/%s", promptID, payloadID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Get after delete should return 404
	req = newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/payloads/%s", promptID, payloadID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPromptPayload_NotFound_Errors(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	promptID := setupPrompt(t, a, token)
	randomID := uuid.New().String()

	// GET non-existent
	req := newReq(t, http.MethodGet, fmt.Sprintf("/v1/prompts/%s/payloads/%s", promptID, randomID), "", token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// PUT non-existent
	req = newReq(t, http.MethodPut, fmt.Sprintf("/v1/prompts/%s/payloads/%s", promptID, randomID), `{"name":"test"}`, token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// DELETE non-existent
	req = newReq(t, http.MethodDelete, fmt.Sprintf("/v1/prompts/%s/payloads/%s", promptID, randomID), "", token)
	resp, err = a.Test(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
