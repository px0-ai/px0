package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/template"

	"github.com/gofiber/fiber/v2"
	"github.com/px0-ai/px0/internal/apierr"
	"github.com/px0-ai/px0/internal/model"
	"github.com/px0-ai/px0/internal/store"
)

type runRequest struct {
	Variables   map[string]any  `json:"variables"`
	Stream      bool            `json:"stream"`
	Model       *string         `json:"model"`
	ModelParams json.RawMessage `json:"model_params"`
}

type sseMessage struct {
	Delta string `json:"delta"`
	Done  bool   `json:"done"`
}

func RunLive(c *fiber.Ctx) error {
	prompt, ok := resolveProjectPromptBySlug(c)
	if !ok {
		return nil
	}

	version, err := store.GetLiveVersion(c.Context(), prompt.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrNoLiveVersionFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return executeRun(c, prompt, version)
}

func RunVersion(c *fiber.Ctx) error {
	prompt, ok := resolveProjectPromptBySlug(c)
	if !ok {
		return nil
	}

	version, err := resolveVersion(c.Context(), prompt.ID, c.Params("version"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrVersionNotFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	return executeRun(c, prompt, version)
}

func executeRun(c *fiber.Ctx, prompt *model.Prompt, version *model.PromptVersion) error {
	var req runRequest
	if err := c.BodyParser(&req); err != nil {
		if !errors.Is(err, fiber.ErrUnprocessableEntity) {
			req = runRequest{}
		}
	}
	if req.Variables == nil {
		req.Variables = map[string]any{}
	}

	// 1. Render prompt template
	tmpl, err := template.New("prompt").Option("missingkey=error").Parse(version.Template)
	if err != nil {
		return apierr.ErrTemplateParseError.Respond(c, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, req.Variables); err != nil {
		return apierr.ErrTemplateExecutionFailed.WithDetails(err.Error()).Respond(c, err)
	}
	userPrompt := buf.String()

	// 2. Resolve Model
	var modelName string
	if req.Model != nil && strings.TrimSpace(*req.Model) != "" {
		modelName = strings.TrimSpace(*req.Model)
	} else if version.Model != nil && strings.TrimSpace(*version.Model) != "" {
		modelName = strings.TrimSpace(*version.Model)
	}

	if modelName == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "no model configured for this prompt version and none provided in request").Respond(c)
	}

	// 3. Resolve Model Params
	var modelParams json.RawMessage
	if len(req.ModelParams) > 0 && string(req.ModelParams) != "null" {
		modelParams = req.ModelParams
	} else {
		modelParams = version.ModelParams
	}

	// 4. Resolve Provider & Protocol
	var provider string
	var protocol string
	var cleanModel string

	if strings.HasPrefix(modelName, "openai/") {
		provider = "openai"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "openai/")
	} else if strings.HasPrefix(modelName, "anthropic/") {
		provider = "anthropic"
		protocol = "anthropic"
		cleanModel = strings.TrimPrefix(modelName, "anthropic/")
	} else if strings.HasPrefix(modelName, "gemini/") {
		provider = "gemini"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "gemini/")
	} else if strings.HasPrefix(modelName, "deepseek/") {
		provider = "deepseek"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "deepseek/")
	} else if strings.HasPrefix(modelName, "groq/") {
		provider = "groq"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "groq/")
	} else if strings.HasPrefix(modelName, "openrouter/") {
		provider = "openrouter"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "openrouter/")
	} else {
		return apierr.NewAPIError(fiber.StatusBadRequest, "unsupported model provider. Model name must start with 'openai/', 'anthropic/', 'gemini/', 'deepseek/', 'groq/', or 'openrouter/'").Respond(c)
	}

	// 5. Resolve Base URL & API Key (headers take precedence over env)
	apiKey, baseURL, err := resolveProviderConfig(c, provider)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, err.Error()).Respond(c)
	}

	// 6. Construct Request Body for Provider
	providerBody, err := prepareProviderRequestBody(protocol, cleanModel, userPrompt, req.Stream, modelParams)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusInternalServerError, "failed to marshal provider request body").Respond(c, err)
	}

	// 7. Execute Request with context propagation
	var reqURL string
	if protocol == "openai" {
		reqURL = fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(baseURL, "/"))
	} else {
		reqURL = fmt.Sprintf("%s/messages", strings.TrimSuffix(baseURL, "/"))
	}

	httpReq, err := http.NewRequestWithContext(c.Context(), "POST", reqURL, bytes.NewReader(providerBody))
	if err != nil {
		return apierr.NewAPIError(fiber.StatusInternalServerError, "failed to create provider request").Respond(c, err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if protocol == "openai" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	} else {
		httpReq.Header.Set("x-api-key", apiKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadGateway, fmt.Sprintf("failed to connect to provider: %v", err)).Respond(c, err)
	}

	// Handle error codes returned from the provider
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return apierr.NewAPIError(fiber.StatusBadGateway, fmt.Sprintf("provider error (status %d): %s", resp.StatusCode, string(errBytes))).Respond(c)
	}

	// 8. Stream vs. Block responses
	if req.Stream {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")
		c.Set("Transfer-Encoding", "chunked")

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			defer resp.Body.Close()
			if protocol == "openai" {
				streamOpenAI(resp.Body, w)
			} else {
				streamAnthropic(resp.Body, w)
			}
		})
		return nil
	}

	// Non-streaming block response
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusInternalServerError, "failed to read provider response").Respond(c, err)
	}

	var completionText string
	if protocol == "openai" {
		var oaiResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(respBytes, &oaiResp); err != nil || len(oaiResp.Choices) == 0 {
			return apierr.NewAPIError(fiber.StatusBadGateway, "failed to parse OpenAI response").Respond(c, err)
		}
		completionText = oaiResp.Choices[0].Message.Content
	} else {
		var antResp struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(respBytes, &antResp); err != nil || len(antResp.Content) == 0 {
			return apierr.NewAPIError(fiber.StatusBadGateway, "failed to parse Anthropic response").Respond(c, err)
		}
		completionText = antResp.Content[0].Text
	}

	return c.JSON(fiber.Map{
		"response": completionText,
		"model":    modelName,
		"version":  version.Version,
		"slug":     prompt.Slug,
	})
}

type batchElement struct {
	Variables map[string]any `json:"variables"`
}

type batchRunRequest struct {
	Batch       []batchElement  `json:"batch"`
	Model       *string         `json:"model"`
	ModelParams json.RawMessage `json:"model_params"`
}

type batchResponseElement struct {
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

func executeSinglePayloadCall(ctx context.Context, protocol, cleanModel, userPrompt, apiKey, baseURL string, modelParams json.RawMessage) (string, error) {
	providerBody, err := prepareProviderRequestBody(protocol, cleanModel, userPrompt, false, modelParams)
	if err != nil {
		return "", fmt.Errorf("prepare request body: %w", err)
	}

	var reqURL string
	if protocol == "openai" {
		reqURL = fmt.Sprintf("%s/chat/completions", strings.TrimSuffix(baseURL, "/"))
	} else {
		reqURL = fmt.Sprintf("%s/messages", strings.TrimSuffix(baseURL, "/"))
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(providerBody))
	if err != nil {
		return "", fmt.Errorf("create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if protocol == "openai" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	} else {
		httpReq.Header.Set("x-api-key", apiKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("provider error (status %d): %s", resp.StatusCode, string(errBytes))
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var completionText string
	if protocol == "openai" {
		var oaiResp struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(respBytes, &oaiResp); err != nil || len(oaiResp.Choices) == 0 {
			return "", fmt.Errorf("failed to parse OpenAI response: %w", err)
		}
		completionText = oaiResp.Choices[0].Message.Content
	} else {
		var antResp struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(respBytes, &antResp); err != nil || len(antResp.Content) == 0 {
			return "", fmt.Errorf("failed to parse Anthropic response: %w", err)
		}
		completionText = antResp.Content[0].Text
	}

	return completionText, nil
}

func BatchRun(c *fiber.Ctx) error {
	prompt, ok := resolveProjectPromptBySlug(c)
	if !ok {
		return nil
	}

	version, err := store.GetLiveVersion(c.Context(), prompt.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return apierr.ErrNoLiveVersionFound.Respond(c)
		}
		return apierr.ErrInternalError.Respond(c, err)
	}

	var req batchRunRequest
	if err := c.BodyParser(&req); err != nil {
		return apierr.ErrInvalidRequestBody.Respond(c)
	}

	tmpl, err := template.New("prompt").Option("missingkey=error").Parse(version.Template)
	if err != nil {
		return apierr.ErrTemplateParseError.Respond(c, err)
	}

	var modelName string
	if req.Model != nil && strings.TrimSpace(*req.Model) != "" {
		modelName = strings.TrimSpace(*req.Model)
	} else if version.Model != nil && strings.TrimSpace(*version.Model) != "" {
		modelName = strings.TrimSpace(*version.Model)
	}

	if modelName == "" {
		return apierr.NewAPIError(fiber.StatusBadRequest, "no model configured for this prompt version and none provided in request").Respond(c)
	}

	var modelParams json.RawMessage
	if len(req.ModelParams) > 0 && string(req.ModelParams) != "null" {
		modelParams = req.ModelParams
	} else {
		modelParams = version.ModelParams
	}

	var provider string
	var protocol string
	var cleanModel string

	if strings.HasPrefix(modelName, "openai/") {
		provider = "openai"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "openai/")
	} else if strings.HasPrefix(modelName, "anthropic/") {
		provider = "anthropic"
		protocol = "anthropic"
		cleanModel = strings.TrimPrefix(modelName, "anthropic/")
	} else if strings.HasPrefix(modelName, "gemini/") {
		provider = "gemini"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "gemini/")
	} else if strings.HasPrefix(modelName, "deepseek/") {
		provider = "deepseek"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "deepseek/")
	} else if strings.HasPrefix(modelName, "groq/") {
		provider = "groq"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "groq/")
	} else if strings.HasPrefix(modelName, "openrouter/") {
		provider = "openrouter"
		protocol = "openai"
		cleanModel = strings.TrimPrefix(modelName, "openrouter/")
	} else {
		return apierr.NewAPIError(fiber.StatusBadRequest, "unsupported model provider").Respond(c)
	}

	apiKey, baseURL, err := resolveProviderConfig(c, provider)
	if err != nil {
		return apierr.NewAPIError(fiber.StatusBadRequest, err.Error()).Respond(c)
	}

	results := make([]batchResponseElement, len(req.Batch))
	var wg sync.WaitGroup

	for i, elem := range req.Batch {
		wg.Add(1)
		go func(idx int, vars map[string]any) {
			defer wg.Done()
			if vars == nil {
				vars = map[string]any{}
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, vars); err != nil {
				results[idx] = batchResponseElement{Error: fmt.Sprintf("template execution failed: %v", err)}
				return
			}
			userPrompt := buf.String()

			completion, err := executeSinglePayloadCall(context.Background(), protocol, cleanModel, userPrompt, apiKey, baseURL, modelParams)
			if err != nil {
				results[idx] = batchResponseElement{Error: err.Error()}
			} else {
				results[idx] = batchResponseElement{Response: completion}
			}
		}(i, elem.Variables)
	}
	wg.Wait()

	return c.JSON(fiber.Map{
		"results": results,
		"model":   modelName,
		"version": version.Version,
		"slug":    prompt.Slug,
	})
}

func resolveProviderConfig(c *fiber.Ctx, provider string) (apiKey string, baseURL string, err error) {
	switch provider {
	case "openai":
		apiKey = c.Get("X-OpenAI-Key", os.Getenv("OPENAI_API_KEY"))
		baseURL = c.Get("X-OpenAI-Base", os.Getenv("OPENAI_API_BASE"))
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
	case "anthropic":
		apiKey = c.Get("X-Anthropic-Key", os.Getenv("ANTHROPIC_API_KEY"))
		baseURL = c.Get("X-Anthropic-Base", os.Getenv("ANTHROPIC_API_BASE"))
		if baseURL == "" {
			baseURL = "https://api.anthropic.com/v1"
		}
	case "gemini":
		apiKey = c.Get("X-Gemini-Key", os.Getenv("GEMINI_API_KEY"))
		baseURL = c.Get("X-Gemini-Base", os.Getenv("GEMINI_API_BASE"))
		if baseURL == "" {
			baseURL = "https://generativelanguage.googleapis.com/v1beta/openai"
		}
	case "deepseek":
		apiKey = c.Get("X-DeepSeek-Key", os.Getenv("DEEPSEEK_API_KEY"))
		baseURL = c.Get("X-DeepSeek-Base", os.Getenv("DEEPSEEK_API_BASE"))
		if baseURL == "" {
			baseURL = "https://api.deepseek.com/v1"
		}
	case "groq":
		apiKey = c.Get("X-Groq-Key", os.Getenv("GROQ_API_KEY"))
		baseURL = c.Get("X-Groq-Base", os.Getenv("GROQ_API_BASE"))
		if baseURL == "" {
			baseURL = "https://api.groq.com/openapi/v1"
		}
	case "openrouter":
		apiKey = c.Get("X-OpenRouter-Key", os.Getenv("OPENROUTER_API_KEY"))
		baseURL = c.Get("X-OpenRouter-Base", os.Getenv("OPENROUTER_API_BASE"))
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}
	}

	if apiKey == "" {
		return "", "", fmt.Errorf("missing API key for provider: %s", provider)
	}
	return apiKey, baseURL, nil
}

func prepareProviderRequestBody(protocol, modelName, userPrompt string, stream bool, modelParams json.RawMessage) ([]byte, error) {
	bodyMap := make(map[string]any)

	// Merge model params if supplied
	if len(modelParams) > 0 && string(modelParams) != "null" {
		_ = json.Unmarshal(modelParams, &bodyMap)
	}

	bodyMap["model"] = modelName
	bodyMap["stream"] = stream

	if protocol == "openai" {
		bodyMap["messages"] = []map[string]any{
			{"role": "user", "content": userPrompt},
		}
	} else {
		bodyMap["messages"] = []map[string]any{
			{"role": "user", "content": userPrompt},
		}
		// Anthropic requires max_tokens to be set
		if _, ok := bodyMap["max_tokens"]; !ok {
			bodyMap["max_tokens"] = 4096
		}
	}

	return json.Marshal(bodyMap)
}

func streamOpenAI(rc io.Reader, w *bufio.Writer) {
	scanner := bufio.NewScanner(rc)
	for scanner.Scan() {
		line := scanner.Bytes()
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		data := bytes.TrimPrefix(line, []byte("data: "))
		if string(data) == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &chunk); err == nil && len(chunk.Choices) > 0 {
			deltaText := chunk.Choices[0].Delta.Content
			if deltaText != "" {
				sendSSEChunk(w, deltaText, false)
			}
		}
	}
	sendSSEChunk(w, "", true)
}

func streamAnthropic(rc io.Reader, w *bufio.Writer) {
	scanner := bufio.NewScanner(rc)
	var isDeltaEvent bool
	for scanner.Scan() {
		line := scanner.Bytes()
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, []byte("event: ")) {
			eventName := bytes.TrimPrefix(line, []byte("event: "))
			isDeltaEvent = string(eventName) == "content_block_delta"
			continue
		}

		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		if !isDeltaEvent {
			continue
		}

		data := bytes.TrimPrefix(line, []byte("data: "))
		var chunk struct {
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(data, &chunk); err == nil {
			if chunk.Delta.Text != "" {
				sendSSEChunk(w, chunk.Delta.Text, false)
			}
		}
	}
	sendSSEChunk(w, "", true)
}

func sendSSEChunk(w *bufio.Writer, delta string, done bool) {
	msg := sseMessage{Delta: delta, Done: done}
	bytesMsg, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", string(bytesMsg))
	_ = w.Flush()
}
