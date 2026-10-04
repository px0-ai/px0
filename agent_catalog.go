package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const modelProbeTimeout = 5 * time.Second

// modelProbe asks an installed harness for the models available to the current
// account and configuration. The executable path has already been resolved.
type modelProbe func(context.Context, string) ([]string, error)

// agentPreset is a reviewed unattended-edit invocation. Arbitrary executables
// are deliberately not discovered: px0 must know the arguments that make a
// harness non-interactive before it can safely offer that harness in the UI.
type agentPreset struct {
	Name      string
	Args      []string
	ModelFlag string
	Discover  modelProbe
}

var agentPresets = []agentPreset{
	{
		Name:      "claude",
		Args:      []string{"claude", "--permission-mode", "acceptEdits", "-p", "{prompt}"},
		ModelFlag: "--model",
	},
	{
		Name:      "gemini",
		Args:      []string{"gemini", "--approval-mode", "auto_edit", "-p", "{prompt}"},
		ModelFlag: "-m",
	},
	{
		Name:      "cursor-agent",
		Args:      []string{"cursor-agent", "--force", "-p", "{prompt}"},
		ModelFlag: "--model",
		Discover:  probeCursorModels,
	},
	{
		Name:      "agy",
		Args:      []string{"agy", "--dangerously-skip-permissions", "--mode", "accept-edits", "-p", "{prompt}"},
		ModelFlag: "--model",
		Discover:  probeAgyModels,
	},
	{
		Name:      "pi",
		Args:      []string{"pi", "--approve", "-p", "{prompt}"},
		ModelFlag: "--model",
		Discover:  probePiModels,
	},
	{
		Name:      "omp",
		Args:      []string{"omp", "--no-session", "--approval-mode", "yolo", "-p", "{prompt}"},
		ModelFlag: "--model",
		Discover:  probeOMPModels,
	},
	{
		Name:      "opencode",
		Args:      []string{"opencode", "run", "--agent", "build", "--auto", "{prompt}"},
		ModelFlag: "-m",
		Discover:  probeOpenCodeModels,
	},
	{
		Name:      "codex",
		Args:      []string{"codex", "-a", "never", "exec", "--sandbox", "workspace-write", "{prompt}"},
		ModelFlag: "-m",
		Discover:  probeCodexModels,
	},
	{
		Name:      "aider",
		Args:      []string{"aider", "--yes-always", "--no-auto-commits", "--message", "{prompt}"},
		ModelFlag: "--model",
	},
	{
		Name:      "goose",
		Args:      []string{"goose", "run", "--no-session", "-t", "{prompt}"},
		ModelFlag: "--model",
	},
}

func normalizeModels(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	out := models[:0]
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
}

func validModelToken(token string) bool {
	return token != "" && strings.Trim(token, "-=") != "" && !strings.ContainsAny(token, " \t\r\n")
}

func parseCursorModels(data []byte) []string {
	var models []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(strings.ToLower(line), "tip:") {
			continue
		}
		id := line
		if before, _, ok := strings.Cut(line, " - "); ok {
			id = strings.TrimSpace(before)
		}
		if validModelToken(id) {
			models = append(models, id)
		}
	}
	return normalizeModels(models)
}

func parseAgyModels(data []byte) []string {
	var models []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(strings.ToLower(line), "fetching") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && validModelToken(fields[0]) {
			models = append(models, fields[0])
		}
	}
	return normalizeModels(models)
}

func parsePiModels(data []byte) []string {
	var models []string
	seenHeader := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.EqualFold(fields[0], "provider") && strings.EqualFold(fields[1], "model") {
			seenHeader = true
			continue
		}
		if !seenHeader || len(fields) < 2 || !validModelToken(fields[0]) || !validModelToken(fields[1]) {
			continue
		}
		models = append(models, fields[0]+"/"+fields[1])
	}
	return normalizeModels(models)
}

func parseOMPModels(data []byte) []string {
	var catalog struct {
		Models []struct {
			Selector string `json:"selector"`
			Kind     string `json:"kind"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil
	}
	models := make([]string, 0, len(catalog.Models))
	for _, entry := range catalog.Models {
		if entry.Kind == "chat" {
			models = append(models, entry.Selector)
		}
	}
	return normalizeModels(models)
}

func parseOpenCodeModels(data []byte) []string {
	var models []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		selector := strings.TrimSpace(scanner.Text())
		provider, model, ok := strings.Cut(selector, "/")
		if !ok || !validModelToken(provider) || !validModelToken(model) {
			continue
		}
		models = append(models, selector)
	}
	return normalizeModels(models)
}

func withModelProbeTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, modelProbeTimeout)
}

func runModelCommand(parent context.Context, bin string, args ...string) ([]byte, error) {
	ctx, cancel := withModelProbeTimeout(parent)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	setProcessGroup(cmd)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return out, nil
}

func probeCursorModels(ctx context.Context, bin string) ([]string, error) {
	out, err := runModelCommand(ctx, bin, "models")
	if err != nil {
		return nil, err
	}
	return parseCursorModels(out), nil
}

func probeAgyModels(ctx context.Context, bin string) ([]string, error) {
	out, err := runModelCommand(ctx, bin, "models")
	if err != nil {
		return nil, err
	}
	return parseAgyModels(out), nil
}

func probePiModels(ctx context.Context, bin string) ([]string, error) {
	out, err := runModelCommand(ctx, bin, "--list-models")
	if err != nil {
		return nil, err
	}
	return parsePiModels(out), nil
}

func probeOMPModels(ctx context.Context, bin string) ([]string, error) {
	out, err := runModelCommand(ctx, bin, "models", "--json")
	if err != nil {
		return nil, err
	}
	return parseOMPModels(out), nil
}

func probeOpenCodeModels(ctx context.Context, bin string) ([]string, error) {
	out, err := runModelCommand(ctx, bin, "models")
	if err != nil {
		return nil, err
	}
	return parseOpenCodeModels(out), nil
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func jsonRPCIDMatches(raw json.RawMessage, want int) bool {
	var got int
	return len(raw) != 0 && json.Unmarshal(raw, &got) == nil && got == want
}

func readJSONRPCResponse(ctx context.Context, decoder *json.Decoder, id int) (json.RawMessage, error) {
	for {
		var msg jsonRPCMessage
		if err := decoder.Decode(&msg); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("codex app-server closed before response %d", id)
			}
			return nil, fmt.Errorf("decode codex app-server response: %w", err)
		}
		if !jsonRPCIDMatches(msg.ID, id) {
			// Notifications and replies to unrelated request IDs are not part of
			// this exchange and must not disturb its request/response ordering.
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("codex app-server error %d: %s", msg.Error.Code, msg.Error.Message)
		}
		if len(msg.Result) == 0 {
			return nil, fmt.Errorf("codex app-server response %d has no result", id)
		}
		return msg.Result, nil
	}
}

// stopCodexProcess first gives the server a chance to observe EOF and exit,
// then forcibly terminates it. Wait is always called, including every error
// and cancellation path, so no app-server child can be left behind.
func stopCodexProcess(cmd *exec.Cmd, stdin io.Closer) error {
	_ = stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(100 * time.Millisecond):
		if cmd.Cancel != nil {
			_ = cmd.Cancel()
		} else if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return nil
	}
}

func probeCodexModels(parent context.Context, bin string) (models []string, err error) {
	ctx, cancel := withModelProbeTimeout(parent)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "app-server", "--listen", "stdio://")
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	defer func() {
		waitErr := stopCodexProcess(cmd, stdin)
		if err == nil && waitErr != nil {
			err = fmt.Errorf("codex app-server exited: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
			models = nil
		}
	}()

	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(stdout)
	request := func(req jsonRPCRequest) error {
		if err := encoder.Encode(req); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("write codex app-server request: %w", err)
		}
		return nil
	}

	initialize := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"clientInfo":   map[string]string{"name": "px0", "title": "px0", "version": "0.0.0"},
			"capabilities": map[string]any{},
		},
	}
	if err := request(initialize); err != nil {
		return nil, err
	}
	if _, err := readJSONRPCResponse(ctx, decoder, initialize.ID); err != nil {
		return nil, err
	}
	if err := request(jsonRPCRequest{JSONRPC: "2.0", Method: "initialized", Params: map[string]any{}}); err != nil {
		return nil, err
	}

	cursor := ""
	seenCursors := make(map[string]struct{})
	for id := 2; ; id++ {
		params := map[string]any{"includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := request(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: "model/list", Params: params}); err != nil {
			return nil, err
		}
		result, err := readJSONRPCResponse(ctx, decoder, id)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data       json.RawMessage `json:"data"`
			NextCursor string          `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, fmt.Errorf("decode codex model/list result: %w", err)
		}
		if len(page.Data) == 0 || bytes.Equal(bytes.TrimSpace(page.Data), []byte("null")) {
			return nil, errors.New("codex model/list result has no data")
		}
		var entries []struct {
			Model    string `json:"model"`
			Hidden   bool   `json:"hidden"`
			IsHidden bool   `json:"isHidden"`
		}
		if err := json.Unmarshal(page.Data, &entries); err != nil {
			return nil, fmt.Errorf("decode codex model/list entries: %w", err)
		}
		for _, entry := range entries {
			if !entry.Hidden && !entry.IsHidden {
				models = append(models, entry.Model)
			}
		}
		cursor = strings.TrimSpace(page.NextCursor)
		if cursor == "" {
			return normalizeModels(models), nil
		}
		if _, repeated := seenCursors[cursor]; repeated {
			return nil, fmt.Errorf("codex model/list repeated cursor %q", cursor)
		}
		seenCursors[cursor] = struct{}{}
	}
}

type modelDiscoveryStatus string

const (
	modelDiscoveryUnsupported modelDiscoveryStatus = "unsupported"
	modelDiscoveryLoading     modelDiscoveryStatus = "loading"
	modelDiscoveryReady       modelDiscoveryStatus = "ready"
	modelDiscoveryFailed      modelDiscoveryStatus = "failed"
)

type modelCatalogEntry struct {
	bin        string
	status     modelDiscoveryStatus
	models     []string
	generation uint64
}

// modelCatalog is a generation-aware, per-manager cache. Its zero value is
// ready for use. A changed executable path replaces the active generation;
// late completion from the old path is ignored.
type modelCatalog struct {
	mu      sync.Mutex
	entries map[string]*modelCatalogEntry

	// Tests can shorten the bound without changing the production zero-value
	// contract. A zero duration always means modelProbeTimeout.
	timeout time.Duration
}

func (c *modelCatalog) Snapshot(preset agentPreset, bin string, refresh bool) ([]string, modelDiscoveryStatus) {
	if preset.Discover == nil {
		return nil, modelDiscoveryUnsupported
	}

	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]*modelCatalogEntry)
	}
	entry := c.entries[preset.Name]

	if bin == "" || !executableFileExists(bin) {
		generation := uint64(1)
		if entry != nil {
			generation = entry.generation + 1
		}
		if entry == nil || entry.bin != bin || entry.status != modelDiscoveryFailed {
			entry = &modelCatalogEntry{bin: bin, status: modelDiscoveryFailed, generation: generation}
			c.entries[preset.Name] = entry
		}
		c.mu.Unlock()
		return nil, modelDiscoveryFailed
	}

	start := false
	if entry == nil || entry.bin != bin {
		generation := uint64(1)
		if entry != nil {
			generation = entry.generation + 1
		}
		entry = &modelCatalogEntry{bin: bin, status: modelDiscoveryLoading, generation: generation}
		c.entries[preset.Name] = entry
		start = true
	} else if refresh && entry.status != modelDiscoveryLoading {
		entry.generation++
		entry.status = modelDiscoveryLoading
		entry.models = nil
		start = true
	}

	models := append([]string(nil), entry.models...)
	status := entry.status
	generation := entry.generation
	timeout := c.timeout
	if timeout <= 0 {
		timeout = modelProbeTimeout
	}
	c.mu.Unlock()

	if start {
		go c.runProbe(preset, bin, generation, timeout)
	}
	return models, status
}

func executableFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (c *modelCatalog) runProbe(preset agentPreset, bin string, generation uint64, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	models, err := preset.Discover(ctx, bin)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	cancel()
	models = normalizeModels(models)

	status := modelDiscoveryReady
	if err != nil || len(models) == 0 {
		status = modelDiscoveryFailed
		models = nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[preset.Name]
	if entry == nil || entry.bin != bin || entry.generation != generation || entry.status != modelDiscoveryLoading {
		return
	}
	entry.status = status
	entry.models = models
}
