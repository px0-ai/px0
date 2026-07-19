package handler_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolInvokeAndHistory(t *testing.T) {
	// 1. Create mock destination HTTP server for the tool
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var payload map[string]any
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		query, _ := payload["query"].(string)
		if query == "bad" {
			// Violates output_schema by returning "error" instead of "results"
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"bad query value"}`))
			return
		}

		// Success path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"results":["http result 1", "http result 2"]}`))
	}))
	defer mockServer.Close()

	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)

	// 2. Create tool with URL configured
	reqCreate := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/tools", projectID),
		fmt.Sprintf(`{"name":"Invokable Tool","slug":"invokable-tool","description":"Test Invoke","url":"%s"}`, mockServer.URL), token)
	respCreate, err := a.App.Test(reqCreate)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respCreate.StatusCode)

	tool := decodeBody(t, respCreate)["tool"].(map[string]any)
	toolID := tool["id"].(string)

	// 3. Create Tool Version with schemas
	reqVer := newReq(t, http.MethodPost, fmt.Sprintf("/v1/tools/%s/versions", toolID),
		`{
			"input_schema": {
				"type": "object",
				"properties": {
					"query": { "type": "string" }
				},
				"required": ["query"]
			},
			"output_schema": {
				"type": "object",
				"properties": {
					"results": {
						"type": "array",
						"items": { "type": "string" }
					}
				},
				"required": ["results"]
			}
		}`, token)
	respVer, err := a.App.Test(reqVer)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, respVer.StatusCode)

	// Promote draft -> stable -> live
	reqPromote := newReq(t, http.MethodPost, fmt.Sprintf("/v1/tools/%s/versions/1/promote", toolID), "", token)
	respStable, err := a.App.Test(reqPromote)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respStable.StatusCode)

	respLive, err := a.App.Test(reqPromote)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respLive.StatusCode)

	// 4. Test tool invoke with valid payload (conforming to schemas)
	reqInvokeSuccess := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/tools/invokable-tool/invoke", projectID),
		`{"query":"hello"}`, token)
	respInvokeSuccess, err := a.App.Test(reqInvokeSuccess)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respInvokeSuccess.StatusCode)

	bodySuccess := decodeBody(t, respInvokeSuccess)
	assert.Equal(t, float64(200), bodySuccess["status_code"])
	respData := bodySuccess["response"].(map[string]any)
	results := respData["results"].([]any)
	assert.Len(t, results, 2)
	assert.Equal(t, "http result 1", results[0])

	// 5. Test tool invoke with request schema violation (missing required "query" property)
	reqInvokeInvalidReq := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/tools/invokable-tool/invoke", projectID),
		`{"other_key":"hello"}`, token)
	respInvokeInvalidReq, err := a.App.Test(reqInvokeInvalidReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, respInvokeInvalidReq.StatusCode)
	bodyInvalidReq := decodeBody(t, respInvokeInvalidReq)
	assert.Contains(t, bodyInvalidReq["error"], "Request validation failed")

	// 6. Test tool invoke with response schema violation (target returns error payload instead of results list)
	reqInvokeInvalidResp := newReq(t, http.MethodPost, fmt.Sprintf("/v1/projects/%s/tools/invokable-tool/invoke", projectID),
		`{"query":"bad"}`, token)
	respInvokeInvalidResp, err := a.App.Test(reqInvokeInvalidResp)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, respInvokeInvalidResp.StatusCode)
	bodyInvalidResp := decodeBody(t, respInvokeInvalidResp)
	assert.Contains(t, bodyInvalidResp["error"], "Response validation failed")

	// 7. Get Tool Execution History
	reqHistory := newReq(t, http.MethodGet, fmt.Sprintf("/v1/tools/%s/invocations?limit=10", toolID), "", token)
	respHistory, err := a.App.Test(reqHistory)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respHistory.StatusCode)

	bodyHistory := decodeBody(t, respHistory)
	invocations := bodyHistory["invocations"].([]any)
	assert.Len(t, invocations, 3) // 3 runs: 1 success, 1 invalid request, 1 invalid response

	// Invocations are returned newest to oldest:
	// 1st item: invalid response
	inv1 := invocations[0].(map[string]any)
	assert.Contains(t, inv1["error"], "Response validation failed")
	assert.Equal(t, float64(400), inv1["status_code"]) // target HTTP response status was 400

	// 2nd item: invalid request
	inv2 := invocations[1].(map[string]any)
	assert.Contains(t, inv2["error"], "Request validation failed")
	assert.Equal(t, float64(400), inv2["status_code"])

	// 3rd item: successful execution
	inv3 := invocations[2].(map[string]any)
	assert.Nil(t, inv3["error"])
	assert.Equal(t, float64(200), inv3["status_code"])

	// Test cursor pagination
	nextCursor := bodyHistory["next_cursor"].(string)
	assert.NotEmpty(t, nextCursor)

	// Fetch next page starting after the last invocation
	reqHistoryPage := newReq(t, http.MethodGet, fmt.Sprintf("/v1/tools/%s/invocations?cursor=%s&limit=2", toolID, nextCursor), "", token)
	respHistoryPage, err := a.App.Test(reqHistoryPage)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, respHistoryPage.StatusCode)
	bodyHistoryPage := decodeBody(t, respHistoryPage)
	assert.Empty(t, bodyHistoryPage["invocations"].([]any))
}
