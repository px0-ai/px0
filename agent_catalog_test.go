package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentPresetCatalog(t *testing.T) {
	wantNames := []string{"claude", "gemini", "cursor-agent", "agy", "pi", "omp", "opencode", "codex", "aider", "goose"}
	if len(agentPresets) != len(wantNames) {
		t.Fatalf("agentPresets has %d rows, want %d", len(agentPresets), len(wantNames))
	}
	unsupported := map[string]bool{"claude": true, "gemini": true, "aider": true, "goose": true}
	for i, preset := range agentPresets {
		if preset.Name != wantNames[i] {
			t.Errorf("agentPresets[%d].Name = %q, want %q", i, preset.Name, wantNames[i])
		}
		if len(preset.Args) == 0 || preset.Args[0] != preset.Name {
			t.Errorf("%s executable argv = %v", preset.Name, preset.Args)
		}
		if gotNil := preset.Discover == nil; gotNil != unsupported[preset.Name] {
			t.Errorf("%s Discover nil = %v, want %v", preset.Name, gotNil, unsupported[preset.Name])
		}
	}
}

func TestModelCatalogParsers(t *testing.T) {
	tests := []struct {
		name  string
		parse func([]byte) []string
		input string
		want  string
	}{
		{
			name:  "cursor",
			parse: parseCursorModels,
			input: "\n alpha - Fast model \nTip: choose a model\ninvalid row without separator\nalpha - duplicate\n beta\n---\n",
			want:  "alpha,beta",
		},
		{
			name:  "antigravity",
			parse: parseAgyModels,
			input: "Fetching models...\n\nalpha Fast model\nalpha duplicate\n---\n beta Strong\n",
			want:  "alpha,beta",
		},
		{
			name:  "pi",
			parse: parsePiModels,
			input: "ignored before header\nprovider model context\nanthropic claude-sonnet 200k\n\nmalformed\nanthropic claude-sonnet 200k\n- - -\ngoogle gemini-flash 1m\n",
			want:  "anthropic/claude-sonnet,google/gemini-flash",
		},
		{
			name:  "omp",
			parse: parseOMPModels,
			input: `{"models":[{"selector":" openai/gpt-fast ","kind":"chat"},{"selector":"","kind":"chat"},{"selector":"embed/small","kind":"embedding"},{"selector":"openai/gpt-fast","kind":"chat"},{"kind":"chat"},{"selector":"anthropic/sonnet","kind":"chat"}]}`,
			want:  "openai/gpt-fast,anthropic/sonnet",
		},
		{
			name:  "omp malformed json",
			parse: parseOMPModels,
			input: `{"models":`,
			want:  "",
		},
		{
			name:  "opencode",
			parse: parseOpenCodeModels,
			input: "\nopenai/gpt-fast\nmissing-provider\n/bad\nbad/\nopenai/gpt-fast\nprovider/model with-space\nanthropic/sonnet\n---\n",
			want:  "openai/gpt-fast,anthropic/sonnet",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Join(tt.parse([]byte(tt.input)), ","); got != tt.want {
				t.Fatalf("parsed models = %q, want %q", got, tt.want)
			}
		})
	}
}

func writeCatalogScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("catalog executable fixtures use POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "model-fixture.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestModelProbeCommands(t *testing.T) {
	tests := []struct {
		name string
		args string
		out  string
		want string
		fn   modelProbe
	}{
		{name: "cursor", args: "models", out: "cursor-a - Fast\n", want: "cursor-a", fn: probeCursorModels},
		{name: "antigravity", args: "models", out: "agy-a Fast\n", want: "agy-a", fn: probeAgyModels},
		{name: "pi", args: "--list-models", out: "provider model context\nprovider-a model-a 1m\n", want: "provider-a/model-a", fn: probePiModels},
		{name: "omp", args: "models --json", out: `{"models":[{"selector":"omp-a","kind":"chat"}]}` + "\n", want: "omp-a", fn: probeOMPModels},
		{name: "opencode", args: "models", out: "provider-a/model-a\n", want: "provider-a/model-a", fn: probeOpenCodeModels},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "args")
			outputFile := filepath.Join(dir, "output")
			if err := os.WriteFile(outputFile, []byte(tt.out), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := writeCatalogScript(t, fmt.Sprintf("printf '%%s' \"$*\" > %q\ncat %q\n", argsFile, outputFile))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			models, err := tt.fn(ctx, bin)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(models, ","); got != tt.want {
				t.Fatalf("models = %q, want %q", got, tt.want)
			}
			args, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(args); got != tt.args {
				t.Fatalf("argv = %q, want %q", got, tt.args)
			}
		})
	}
}

func TestCodexModelProbeHandshakeAndPagination(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "requests.log")
	closedPath := filepath.Join(t.TempDir(), "stdin-closed")
	script := fmt.Sprintf("log=%q\nclosed=%q\n", logPath, closedPath) + `
printf '%s\n' "$*" > "$log"
IFS= read -r line || exit 10
printf '%s\n' "$line" >> "$log"
case "$line" in *'"method":"initialize"'*) ;; *) exit 11 ;; esac
printf '%s\n' '{"jsonrpc":"2.0","method":"account/updated","params":{}}'
printf '%s\n' '{"jsonrpc":"2.0","id":99,"result":{"ignored":true}}'
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"fixture"}}}'
IFS= read -r line || exit 12
printf '%s\n' "$line" >> "$log"
case "$line" in *'"method":"initialized"'*) ;; *) exit 13 ;; esac
IFS= read -r line || exit 14
printf '%s\n' "$line" >> "$log"
case "$line" in *'"method":"model/list"'*'"includeHidden":false'*) ;; *) exit 15 ;; esac
printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"data":[{"model":"alpha"},{"model":"hidden","hidden":true},{"model":"beta"}],"nextCursor":"page-2"}}'
IFS= read -r line || exit 16
printf '%s\n' "$line" >> "$log"
case "$line" in *'"method":"model/list"'*'"cursor":"page-2"'*) ;; *) exit 17 ;; esac
printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"data":[{"model":"beta"},{"model":"gamma"},{"model":"  "}],"nextCursor":""}}'
while IFS= read -r line; do :; done
printf closed > "$closed"
`
	bin := writeCatalogScript(t, script)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := probeCodexModels(ctx, bin)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(models, ","), "alpha,beta,gamma"; got != want {
		t.Fatalf("Codex models = %q, want %q", got, want)
	}
	if _, err := os.Stat(closedPath); err != nil {
		t.Fatalf("Codex app-server did not observe closed stdin: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 5 {
		t.Fatalf("Codex request log has %d lines, want 5: %q", len(lines), data)
	}
	if lines[0] != "app-server --listen stdio://" {
		t.Fatalf("Codex argv = %q", lines[0])
	}
	wantMethods := []string{"initialize", "initialized", "model/list", "model/list"}
	for i, want := range wantMethods {
		var request struct {
			ID     int                        `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(lines[i+1]), &request); err != nil {
			t.Fatalf("request %d is malformed: %v", i, err)
		}
		if request.Method != want {
			t.Errorf("request %d method = %q, want %q", i, request.Method, want)
		}
		if i >= 2 && string(request.Params["includeHidden"]) != "false" {
			t.Errorf("request %d includeHidden = %s, want false", i, request.Params["includeHidden"])
		}
	}
}

func TestCodexModelProbeMalformedOutputClosesProcess(t *testing.T) {
	closedPath := filepath.Join(t.TempDir(), "stdin-closed")
	script := fmt.Sprintf("closed=%q\n", closedPath) + `
IFS= read -r line || exit 10
printf '{malformed-json}\n'
while IFS= read -r line; do :; done
printf closed > "$closed"
`
	bin := writeCatalogScript(t, script)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := probeCodexModels(ctx, bin); err == nil {
		t.Fatal("malformed Codex output succeeded")
	}
	if _, err := os.Stat(closedPath); err != nil {
		t.Fatalf("malformed-output cleanup did not close stdin and wait: %v", err)
	}
}

func waitForCatalog(t *testing.T, catalog *modelCatalog, preset agentPreset, bin string, want modelDiscoveryStatus) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		models, status := catalog.Snapshot(preset, bin, false)
		if status == want {
			return models
		}
		if status != modelDiscoveryLoading {
			t.Fatalf("catalog status = %q while waiting for %q", status, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("catalog remained %q waiting for %q", status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForFileSize(t *testing.T, path string, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if info, err := os.Stat(path); err == nil && info.Size() == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not reach size %d", path, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestModelCatalogUsesFiveSecondProbeContext(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	deadlineSeen := make(chan time.Duration, 1)
	preset := agentPreset{Name: "deadline", Discover: func(ctx context.Context, _ string) ([]string, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("probe context has no deadline")
		}
		deadlineSeen <- time.Until(deadline)
		return []string{"model-a"}, nil
	}}
	var catalog modelCatalog
	if _, status := catalog.Snapshot(preset, bin, false); status != modelDiscoveryLoading {
		t.Fatalf("first status = %q, want loading", status)
	}
	select {
	case remaining := <-deadlineSeen:
		if remaining < 4*time.Second || remaining > modelProbeTimeout {
			t.Fatalf("probe deadline remaining = %s, want approximately %s", remaining, modelProbeTimeout)
		}
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	if models := waitForCatalog(t, &catalog, preset, bin, modelDiscoveryReady); strings.Join(models, ",") != "model-a" {
		t.Fatalf("models = %v, want model-a", models)
	}
}

func TestModelCatalogSingleflightAndRefresh(t *testing.T) {
	dir := t.TempDir()
	countPath := filepath.Join(dir, "count")
	firstStarted := filepath.Join(dir, "first-started")
	firstGate := filepath.Join(dir, "first-gate")
	secondGate := filepath.Join(dir, "second-gate")
	outputPath := filepath.Join(dir, "models")
	if err := os.WriteFile(outputPath, []byte("alpha - Fast\nbeta - Strong\nalpha - Duplicate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`
printf x >> %q
if [ ! -e %q ]; then
  : > %q
  gate=%q
else
  gate=%q
fi
while [ ! -e "$gate" ]; do sleep 0.01; done
cat %q
`, countPath, firstStarted, firstStarted, firstGate, secondGate, outputPath)
	bin := writeCatalogScript(t, body)
	preset := agentPreset{Name: "fixture", Discover: probeCursorModels}
	var catalog modelCatalog

	if models, status := catalog.Snapshot(preset, bin, false); status != modelDiscoveryLoading || len(models) != 0 {
		t.Fatalf("first snapshot = %v, %q; want empty loading", models, status)
	}
	waitForFileSize(t, countPath, 1)

	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func(refresh bool) {
			defer wg.Done()
			_, status := catalog.Snapshot(preset, bin, refresh)
			if status != modelDiscoveryLoading {
				t.Errorf("concurrent snapshot status = %q, want loading", status)
			}
		}(i%3 == 0)
	}
	wg.Wait()
	time.Sleep(30 * time.Millisecond)
	if info, err := os.Stat(countPath); err != nil || info.Size() != 1 {
		t.Fatalf("first generation process count = %v, err=%v; want 1", info, err)
	}
	if err := os.WriteFile(firstGate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(waitForCatalog(t, &catalog, preset, bin, modelDiscoveryReady), ","); got != "alpha,beta" {
		t.Fatalf("first generation models = %q", got)
	}

	if err := os.WriteFile(outputPath, []byte("gamma - New\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if models, status := catalog.Snapshot(preset, bin, true); status != modelDiscoveryLoading || len(models) != 0 {
		t.Fatalf("refresh snapshot = %v, %q; want empty loading", models, status)
	}
	waitForFileSize(t, countPath, 2)
	for range 10 {
		if _, status := catalog.Snapshot(preset, bin, true); status != modelDiscoveryLoading {
			t.Fatalf("in-flight refresh status = %q, want loading", status)
		}
	}
	time.Sleep(30 * time.Millisecond)
	if info, err := os.Stat(countPath); err != nil || info.Size() != 2 {
		t.Fatalf("refresh generation process count = %v, err=%v; want 2", info, err)
	}
	if err := os.WriteFile(secondGate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(waitForCatalog(t, &catalog, preset, bin, modelDiscoveryReady), ","); got != "gamma" {
		t.Fatalf("refreshed models = %q, want gamma", got)
	}
}

func TestModelCatalogExecutableChangeRejectsLateResult(t *testing.T) {
	oldStarted := filepath.Join(t.TempDir(), "old-started")
	oldGate := filepath.Join(t.TempDir(), "old-gate")
	oldBin := writeCatalogScript(t, fmt.Sprintf(": > %q\nwhile [ ! -e %q ]; do sleep 0.01; done\nprintf 'old - stale\\n'\n", oldStarted, oldGate))
	newBin := writeCatalogScript(t, "printf 'new - current\\n'\n")
	preset := agentPreset{Name: "same-harness", Discover: probeCursorModels}
	var catalog modelCatalog

	if _, status := catalog.Snapshot(preset, oldBin, false); status != modelDiscoveryLoading {
		t.Fatalf("old path status = %q, want loading", status)
	}
	waitForFileSize(t, oldStarted, 0)
	if _, status := catalog.Snapshot(preset, newBin, false); status != modelDiscoveryLoading {
		t.Fatalf("new path status = %q, want loading", status)
	}
	if got := strings.Join(waitForCatalog(t, &catalog, preset, newBin, modelDiscoveryReady), ","); got != "new" {
		t.Fatalf("new path models = %q", got)
	}
	if err := os.WriteFile(oldGate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	models, status := catalog.Snapshot(preset, newBin, false)
	if status != modelDiscoveryReady || strings.Join(models, ",") != "new" {
		t.Fatalf("late old result overwrote new path: %v, %q", models, status)
	}
}

func TestModelCatalogFailures(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		discover modelProbe
	}{
		{name: "timeout", body: "sleep 30\nprintf 'late - model\\n'\n", discover: probeCursorModels},
		{name: "exit error", body: "exit 7\n", discover: probeCursorModels},
		{name: "empty output", body: "exit 0\n", discover: probeCursorModels},
		{name: "malformed output", body: "printf '{not-json}\\n'\n", discover: probeOMPModels},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := writeCatalogScript(t, tt.body)
			catalog := modelCatalog{timeout: 60 * time.Millisecond}
			preset := agentPreset{Name: tt.name, Discover: tt.discover}
			if _, status := catalog.Snapshot(preset, bin, false); status != modelDiscoveryLoading {
				t.Fatalf("first status = %q, want loading", status)
			}
			models := waitForCatalog(t, &catalog, preset, bin, modelDiscoveryFailed)
			if len(models) != 0 {
				t.Fatalf("failed discovery exposed models %v", models)
			}
		})
	}
}

func TestModelCatalogUnsupportedAndMissingBinary(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	bin := writeCatalogScript(t, fmt.Sprintf(": > %q\nprintf 'unexpected\\n'\n", marker))
	var catalog modelCatalog
	if models, status := catalog.Snapshot(agentPreset{Name: "unsupported"}, bin, true); status != modelDiscoveryUnsupported || len(models) != 0 {
		t.Fatalf("nil probe snapshot = %v, %q", models, status)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nil probe launched a process: %v", err)
	}

	var calls atomic.Int32
	missingPreset := agentPreset{Name: "missing", Discover: func(context.Context, string) ([]string, error) {
		calls.Add(1)
		return []string{"unexpected"}, nil
	}}
	missing := filepath.Join(t.TempDir(), "not-installed")
	if models, status := catalog.Snapshot(missingPreset, missing, false); status != modelDiscoveryFailed || len(models) != 0 {
		t.Fatalf("missing binary snapshot = %v, %q", models, status)
	}
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("missing binary launched %d probes", calls.Load())
	}
}
