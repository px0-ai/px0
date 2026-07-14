package handler_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunLive_Success_OpenAI_Block(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)

	// Create and promote version with OpenAI configuration
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Explain {{.topic}} in one sentence.","model":"openai/gpt-4.5","model_params":{"temperature":0.7}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	promoteToLive(t, a, token, id)

	// Setup mock OpenAI block completion server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer oai-test-key", r.Header.Get("Authorization"))

		var reqBody map[string]any
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		// Assert merged parameters
		assert.Equal(t, "gpt-4.5", reqBody["model"])
		assert.Equal(t, false, reqBody["stream"])
		assert.Equal(t, 0.7, reqBody["temperature"])

		messages := reqBody["messages"].([]any)
		assert.Len(t, messages, 1)
		assert.Equal(t, "Explain prompt-engineering in one sentence.", messages[0].(map[string]any)["content"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-123",
			"object": "chat.completion",
			"created": 1677652288,
			"model": "gpt-4.5",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "Prompt engineering is the art of structuring instructions to get optimal results from AI."
				},
				"finish_reason": "stop"
			}]
		}`))
	}))
	defer mockServer.Close()

	// Perform run request
	runReq := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/prompts/%s/run", projectID, slug),
		`{"variables":{"topic":"prompt-engineering"},"stream":false}`, token)
	runReq.Header.Set("X-OpenAI-Base", mockServer.URL)
	runReq.Header.Set("X-OpenAI-Key", "oai-test-key")

	resp, err = a.Test(runReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "Prompt engineering is the art of structuring instructions to get optimal results from AI.", body["response"])
	assert.Equal(t, "openai/gpt-4.5", body["model"])
	assert.Equal(t, float64(1), body["version"])
	assert.Equal(t, slug, body["slug"])
}

func TestRunLive_Success_Gemini_Block(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)

	// Create and promote version with Gemini configuration
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Tell me about {{.thing}}.","model":"gemini/gemini-2.5-flash","model_params":{"temperature":0.4}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	promoteToLive(t, a, token, id)

	// Setup mock Gemini OpenAI-compatible server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer gemini-test-key", r.Header.Get("Authorization"))

		var reqBody map[string]any
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		assert.Equal(t, "gemini-2.5-flash", reqBody["model"])
		assert.Equal(t, false, reqBody["stream"])
		assert.Equal(t, 0.4, reqBody["temperature"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-gemini",
			"object": "chat.completion",
			"model": "gemini-2.5-flash",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "Gemini is Google's next-generation family of multimodal AI models."
				}
			}]
		}`))
	}))
	defer mockServer.Close()

	runReq := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/prompts/%s/run", projectID, slug),
		`{"variables":{"thing":"Gemini"},"stream":false}`, token)
	runReq.Header.Set("X-Gemini-Base", mockServer.URL)
	runReq.Header.Set("X-Gemini-Key", "gemini-test-key")

	resp, err = a.Test(runReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "Gemini is Google's next-generation family of multimodal AI models.", body["response"])
	assert.Equal(t, "gemini/gemini-2.5-flash", body["model"])
	assert.Equal(t, float64(1), body["version"])
	assert.Equal(t, slug, body["slug"])
}

func TestRunLive_Success_OpenAI_Stream(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Tell me {{.thing}}.","model":"openai/gpt-4o","model_params":{"temperature":0.1}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	promoteToLive(t, a, token, id)

	// Setup mock OpenAI stream completion server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer oai-test-key", r.Header.Get("Authorization"))

		var reqBody map[string]any
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)
		assert.Equal(t, true, reqBody["stream"])

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []string{
			`data: {"choices":[{"delta":{"content":"Let"}}]}`,
			`data: {"choices":[{"delta":{"content":" there"}}]}`,
			`data: {"choices":[{"delta":{"content":" be"}}]}`,
			`data: {"choices":[{"delta":{"content":" light."}}]}`,
			`data: [DONE]`,
		}

		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "%s\n", chunk)
			flusher.Flush()
		}
	}))
	defer mockServer.Close()

	// Perform run request
	runReq := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/prompts/%s/run", projectID, slug),
		`{"variables":{"thing":"light"},"stream":true}`, token)
	runReq.Header.Set("X-OpenAI-Base", mockServer.URL)
	runReq.Header.Set("X-OpenAI-Key", "oai-test-key")

	resp, err = a.Test(runReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Read stream output
	scanner := bufio.NewScanner(resp.Body)
	defer resp.Body.Close()

	var collected []string
	var doneReached bool

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			var msg struct {
				Delta string `json:"delta"`
				Done  bool   `json:"done"`
			}
			err := json.Unmarshal(data, &msg)
			require.NoError(t, err)

			if msg.Done {
				doneReached = true
				break
			}
			collected = append(collected, msg.Delta)
		}
	}

	assert.True(t, doneReached)
	assert.Equal(t, []string{"Let", " there", " be", " light."}, collected)
}

func TestRunLive_Success_Anthropic_Block(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Greet {{.user}}.","model":"anthropic/claude-3","model_params":{"max_tokens":100}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	promoteToLive(t, a, token, id)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/messages", r.URL.Path)
		assert.Equal(t, "ant-test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))

		var reqBody map[string]any
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)
		assert.Equal(t, float64(100), reqBody["max_tokens"]) // model_params merged successfully!

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "msg_123",
			"type": "message",
			"role": "assistant",
			"content": [{
				"type": "text",
				"text": "Hello, Alice!"
			}]
		}`))
	}))
	defer mockServer.Close()

	runReq := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/prompts/%s/run", projectID, slug),
		`{"variables":{"user":"Alice"},"stream":false}`, token)
	runReq.Header.Set("X-Anthropic-Base", mockServer.URL)
	runReq.Header.Set("X-Anthropic-Key", "ant-test-key")

	resp, err = a.Test(runReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "Hello, Alice!", body["response"])
}

func TestRunLive_Success_Anthropic_Stream(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Explain {{.concept}}.","model":"anthropic/claude-3-sonnet","model_params":{}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	promoteToLive(t, a, token, id)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []string{
			"event: message_start\ndata: {}\n\n",
			"event: content_block_start\ndata: {}\n\n",
			"event: content_block_delta\ndata: {\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"Claude \"}}\n\n",
			"event: content_block_delta\ndata: {\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"is \"}}\n\n",
			"event: content_block_delta\ndata: {\"type\": \"content_block_delta\", \"index\": 0, \"delta\": {\"type\": \"text_delta\", \"text\": \"ready.\"}}\n\n",
			"event: message_delta\ndata: {}\n\n",
			"event: message_stop\ndata: {}\n\n",
		}

		for _, chunk := range chunks {
			_, _ = w.Write([]byte(chunk))
			flusher.Flush()
		}
	}))
	defer mockServer.Close()

	runReq := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/prompts/%s/run", projectID, slug),
		`{"variables":{"concept":"Claude"},"stream":true}`, token)
	runReq.Header.Set("X-Anthropic-Base", mockServer.URL)
	runReq.Header.Set("X-Anthropic-Key", "ant-test-key")

	resp, err = a.Test(runReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	defer resp.Body.Close()

	var collected []string
	var doneReached bool

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			var msg struct {
				Delta string `json:"delta"`
				Done  bool   `json:"done"`
			}
			err := json.Unmarshal(data, &msg)
			require.NoError(t, err)

			if msg.Done {
				doneReached = true
				break
			}
			collected = append(collected, msg.Delta)
		}
	}

	assert.True(t, doneReached)
	assert.Equal(t, []string{"Claude ", "is ", "ready."}, collected)
}

func TestRunLive_AdhocOverrides(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)

	// DB version has openai/gpt-4o with temperature 0.1
	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Tell a joke.","model":"openai/gpt-4o","model_params":{"temperature":0.1}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	promoteToLive(t, a, token, id)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		// Assert overridden values!
		assert.Equal(t, "gpt-4-override", reqBody["model"])
		assert.Equal(t, 0.9, reqBody["temperature"])

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "Why did the programmer cross the road? To escape the bugs."
				}
			}]
		}`))
	}))
	defer mockServer.Close()

	// Override model and model_params in the request body
	runReq := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/prompts/%s/run", projectID, slug),
		`{"model":"openai/gpt-4-override","model_params":{"temperature":0.9}}`, token)
	runReq.Header.Set("X-OpenAI-Base", mockServer.URL)
	runReq.Header.Set("X-OpenAI-Key", "oai-test-key")

	resp, err = a.Test(runReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Equal(t, "Why did the programmer cross the road? To escape the bugs.", body["response"])
	assert.Equal(t, "openai/gpt-4-override", body["model"])
}

func TestRunLive_ProviderErrorHandling(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Tell a joke.","model":"openai/gpt-4o","model_params":{}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	promoteToLive(t, a, token, id)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"message": "Invalid API Key"}}`))
	}))
	defer mockServer.Close()

	runReq := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/prompts/%s/run", projectID, slug),
		`{}`, token)
	runReq.Header.Set("X-OpenAI-Base", mockServer.URL)
	runReq.Header.Set("X-OpenAI-Key", "invalid-key")

	resp, err = a.Test(runReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "provider error (status 401)")
	assert.Contains(t, body["error"], "Invalid API Key")
}

func TestRunLive_MissingAPIKey(t *testing.T) {
	a := newTestApp(t)
	token := setupUser(t, a)
	projectID := setupProject(t, a, token)
	id := setupPromptInProject(t, a, token, projectID)
	slug := getPromptSlug(t, a, id, token)

	req := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/prompts/%s/versions", id),
		`{"template":"Tell a joke.","model":"openai/gpt-4o","model_params":{}}`, token)
	resp, err := a.Test(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	promoteToLive(t, a, token, id)

	runReq := newReq(t, http.MethodPost,
		fmt.Sprintf("/v1/projects/%s/prompts/%s/run", projectID, slug),
		`{}`, token)
	// Do not set X-OpenAI-Key header or environment variable!

	resp, err = a.Test(runReq)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body := decodeBody(t, resp)
	assert.Contains(t, body["error"], "missing API key for provider: openai")
}
