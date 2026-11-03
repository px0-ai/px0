package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeBin installs an executable shell script named name on PATH.
func fakeBin(t *testing.T, name, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fakes need a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestResolvePresetInsertsModelBeforePromptFlag(t *testing.T) {
	fakeBin(t, "claude", "")
	c, err := Resolve("Claude", "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--permission-mode", "acceptEdits", "--model", "sonnet", "-p", Placeholder}
	if !reflect.DeepEqual(c.Args[1:], want) {
		t.Fatalf("args = %v, want %v", c.Args[1:], want)
	}
	if c.Name != "claude" || c.Model != "sonnet" {
		t.Fatalf("got %+v", c)
	}
}

// With no model, the model flag is left out so the harness uses its own.
func TestResolveWithoutModelOmitsFlag(t *testing.T) {
	fakeBin(t, "opencode", "")
	c, err := Resolve("opencode", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "" {
		t.Fatalf("model = %q", c.Model)
	}
	if got := c.Args[1:]; !reflect.DeepEqual(got, []string{"run", Placeholder}) {
		t.Fatalf("args = %v", got)
	}
	// With a model and no prompt flag, the model goes right before the prompt.
	c, err = Resolve("opencode", "x/y")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Args[len(c.Args)-3:]; !reflect.DeepEqual(got, []string{"-m", "x/y", Placeholder}) {
		t.Fatalf("tail = %v", got)
	}
}

func TestResolveTemplate(t *testing.T) {
	fakeBin(t, "mytool", "")
	c, err := Resolve("mytool --m {model} --do {prompt}", "x1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "mytool" || !reflect.DeepEqual(c.Args[1:], []string{"--m", "x1", "--do", Placeholder}) {
		t.Fatalf("got %+v", c)
	}
	if _, err := Resolve("mytool --do", ""); err == nil || !strings.Contains(err.Error(), Placeholder) {
		t.Fatalf("missing placeholder error = %v", err)
	}
	// A template that needs a model must not run with a literal "{model}".
	if _, err := Resolve("mytool --m {model} {prompt}", ""); err == nil || !strings.Contains(err.Error(), ModelPlaceholder) {
		t.Fatalf("missing model error = %v", err)
	}
	// A template without {model} is fine with no model.
	if _, err := Resolve("mytool {prompt}", ""); err != nil {
		t.Fatal(err)
	}
}

func TestResolveErrors(t *testing.T) {
	if _, err := Resolve("  ", ""); err == nil {
		t.Fatal("want error for empty spec")
	}
	if _, err := Resolve("definitely-not-installed-xyz {prompt}", ""); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestDetect(t *testing.T) {
	fakeBin(t, "opencode", "")
	var found bool
	for _, h := range Detect(map[string]string{"opencode": "x/y"}) {
		if h.Name != "opencode" {
			continue
		}
		found = true
		if !h.Installed || h.Model != "x/y" || !strings.Contains(h.Cmd, "x/y") {
			t.Fatalf("opencode = %+v", h)
		}
	}
	if !found {
		t.Fatal("opencode not listed")
	}
	if len(Detect(nil)) != len(Presets()) {
		t.Fatal("Detect must list every preset")
	}
}

func TestPresetsReturnsCopy(t *testing.T) {
	p := Presets()
	p[0].Args[0] = "mutated"
	if Presets()[0].Args[0] == "mutated" {
		t.Fatal("Presets leaked internal state")
	}
}

// The samples below are real output from each harness's list command.
func TestParseModels(t *testing.T) {
	claude := "Current model: `Sonnet 5.5`\nUsage: /model <name>. Available: sonnet, opus, haiku, fable, best, sonnet[1m], opusplan, default, or a full model ID.\n"
	if got := parseClaudeModels(claude); !reflect.DeepEqual(got, []string{"sonnet", "opus", "haiku", "fable", "best", "sonnet[1m]", "opusplan", "default"}) {
		t.Fatalf("claude = %v", got)
	}
	cursor := "Available models\n\nauto - Auto (default)\ngpt-5.3-codex-low - Codex 5.3 Low\n\nTip: use --model <id>\n"
	if got := parseCursorModels(cursor); !reflect.DeepEqual(got, []string{"auto", "gpt-5.3-codex-low"}) {
		t.Fatalf("cursor = %v", got)
	}
	agy := "Fetching available models...\ngemini-3.8-flash-high\tGemini 3.8 Flash (High)\ngemini-3.1-pro-low\tGemini 3.1 Pro (Low)\n"
	if got := parseAgyModels(agy); !reflect.DeepEqual(got, []string{"gemini-3.8-flash-high", "gemini-3.1-pro-low"}) {
		t.Fatalf("agy = %v", got)
	}
	if got := parseOpencodeModels("opencode/big-pickle\ngoogle/gemini-2.5-pro\nsome error text\n"); !reflect.DeepEqual(got, []string{"opencode/big-pickle", "google/gemini-2.5-pro"}) {
		t.Fatalf("opencode = %v", got)
	}
	fxJSON := `{"kind":"models","count":3,"shown_count":2,"more_count":1,"ids":["openai/gpt-5","anthropic/claude-sonnet-4"],"models":[{"id":"openai/gpt-5"}]}`
	if got := parseFxModels(fxJSON); !reflect.DeepEqual(got, []string{"openai/gpt-5", "anthropic/claude-sonnet-4"}) {
		t.Fatalf("fx ids = %v", got)
	}
	if got := parseFxModels(`{"models":[{"id":"a/b"},"c/d",{"name":"x"}]}`); !reflect.DeepEqual(got, []string{"a/b", "c/d"}) {
		t.Fatalf("fx models = %v", got)
	}
	if got := parseFxModels("a/b\nc/d  (default)\n"); !reflect.DeepEqual(got, []string{"a/b", "c/d"}) {
		t.Fatalf("fx text = %v", got)
	}
	if got := parseFxModels(`{"kind":"models","error":"could not list models","code":"AuthenticationRejected"}`); len(got) != 0 {
		t.Fatalf("fx error doc = %v", got)
	}
}

// A listing harness is run for real (here a fake script), and what it prints
// is exactly what comes back, in order, with no fallback list.
func TestDiscoverModelsUsesHarnessOutput(t *testing.T) {
	modelsMu.Lock()
	delete(modelsCache, "agy")
	modelsMu.Unlock()
	fakeBin(t, "agy", `printf 'Fetching available models...\nm-b\tB\nm-a\tA\n'`)
	got, err := DiscoverModels(context.Background(), "agy")
	if err != nil || !reflect.DeepEqual(got, []string{"m-b", "m-a"}) {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestDiscoverModelsErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := DiscoverModels(ctx, "nope"); err == nil {
		t.Error("unknown harness should error")
	}
	// Can list, but is not installed.
	modelsMu.Lock()
	delete(modelsCache, "opencode")
	modelsMu.Unlock()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, err := DiscoverModels(ctx, "opencode"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("err = %v", err)
	}
	// Installed but the list command fails.
	fakeBin(t, "opencode", `exit 1`)
	if _, err := DiscoverModels(ctx, "opencode"); err == nil {
		t.Error("failing list command should error")
	}
}

func TestRunPassesPromptAndStreams(t *testing.T) {
	fakeBin(t, "fakeh", `echo "got: $1"; echo "err" >&2`)
	c, err := Resolve("fakeh {prompt}", "")
	if err != nil {
		t.Fatal(err)
	}
	var live strings.Builder
	res := Run(context.Background(), c, "hello world", Options{Dir: t.TempDir(), Stdout: &live})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !strings.Contains(res.Stdout, "got: hello world") || !strings.Contains(live.String(), "got: hello world") {
		t.Fatalf("stdout = %q live = %q", res.Stdout, live.String())
	}
	if strings.TrimSpace(res.Stderr) != "err" {
		t.Fatalf("stderr = %q", res.Stderr)
	}
}

func TestRunUsesDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	fakeBin(t, "fakeh", `pwd; echo "$HARNESS_TEST_VAR"`)
	c, _ := Resolve("fakeh {prompt}", "")
	res := Run(context.Background(), c, "x", Options{Dir: dir, Env: []string{"HARNESS_TEST_VAR=hi"}})
	real, _ := filepath.EvalSymlinks(dir)
	if res.Err != nil || !strings.Contains(res.Stdout, real) || !strings.Contains(res.Stdout, "hi") {
		t.Fatalf("%+v", res)
	}
}

func TestRunFailureAndTimeoutAndCancel(t *testing.T) {
	fakeBin(t, "failh", `echo boom >&2; exit 3`)
	c, _ := Resolve("failh {prompt}", "")
	if res := Run(context.Background(), c, "x", Options{}); res.Err == nil || !strings.Contains(res.Stderr, "boom") {
		t.Fatalf("%+v", res)
	}

	fakeBin(t, "slowh", `sleep 30`)
	c, _ = Resolve("slowh {prompt}", "")
	start := time.Now()
	res := Run(context.Background(), c, "x", Options{Timeout: 100 * time.Millisecond})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "gave up") || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout: %+v after %s", res, time.Since(start))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res = Run(ctx, c, "x", Options{})
	if !errors.Is(res.Err, ErrCancelled) {
		t.Fatalf("cancel err = %v", res.Err)
	}
}

func TestRunKillsProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survivor")
	fakeBin(t, "forkh", `(sleep 1; touch `+marker+`) & sleep 30`)
	c, _ := Resolve("forkh {prompt}", "")
	Run(context.Background(), c, "x", Options{Timeout: 100 * time.Millisecond})
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("child outlived the cancelled harness")
	}
}

func TestTailBuffer(t *testing.T) {
	b := &tailBuffer{max: 4}
	b.Write([]byte("abc"))
	b.Write([]byte("defg"))
	if b.String() != "defg" {
		t.Fatalf("got %q", b.String())
	}
}

// docRow is one row of the table in docs/harnesses.md.
type docRow struct{ name, binary, command string }

// readHarnessDoc parses the harness table, so the documented list, commands and
// are checked against the code rather than trusted.
func readHarnessDoc(t *testing.T) map[string]docRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("docs", "harnesses.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]docRow{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), "|")
		if len(cells) != 3 {
			t.Fatalf("harnesses.md row has %d cells, want 3: %q", len(cells), line)
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(strings.Trim(strings.TrimSpace(cells[i]), "`"))
		}
		rows[cells[0]] = docRow{cells[0], cells[1], strings.Join(strings.Fields(cells[2]), " ")}
	}
	return rows
}

// The documented harnesses must be exactly the ones in the code, with the same
// binary and command.
func TestHarnessDocMatchesCode(t *testing.T) {
	rows := readHarnessDoc(t)
	if len(rows) != len(Presets()) {
		t.Errorf("docs list %d harnesses, code has %d", len(rows), len(Presets()))
	}
	for _, p := range Presets() {
		row, ok := rows[p.Name]
		if !ok {
			t.Errorf("%s is in the code but not in docs/harnesses.md", p.Name)
			continue
		}
		if row.binary != p.Args[0] {
			t.Errorf("%s: docs binary %q, code %q", p.Name, row.binary, p.Args[0])
		}
		// Build the command exactly as Resolve does, with readable placeholders.
		cmd := strings.Join(presetArgs(p, "<model>"), " ")
		cmd = strings.ReplaceAll(cmd, Placeholder, "<prompt>")
		if row.command != cmd {
			t.Errorf("%s: docs command\n  %s\ncode builds\n  %s", p.Name, row.command, cmd)
		}
	}
}

// Every preset must work end to end: detected when installed, resolved, and
// run with the prompt delivered as a single argument. Without a model the
// model flag is absent; with one, the flag and model are passed together.
func TestEveryPresetDetectsResolvesAndRuns(t *testing.T) {
	const prompt = "fix the bug; echo $HOME"
	const model = "my-model"
	detect := func(name string, models map[string]string) Harness {
		for _, h := range Detect(models) {
			if h.Name == name {
				return h
			}
		}
		t.Fatalf("%s not listed by Detect", name)
		return Harness{}
	}

	for _, p := range Presets() {
		t.Run(p.Name, func(t *testing.T) {
			fakeBin(t, p.Args[0], `for a in "$@"; do echo "ARG:$a"; done`)
			run := func(h Harness) string {
				cmd, err := h.Resolve()
				if err != nil {
					t.Fatal(err)
				}
				res := Run(context.Background(), cmd, prompt, Options{Dir: t.TempDir()})
				if res.Err != nil {
					t.Fatal(res.Err)
				}
				if !strings.Contains(res.Stdout, "ARG:"+prompt+"\n") {
					t.Errorf("prompt not delivered as one argument:\n%s", res.Stdout)
				}
				return res.Stdout
			}

			plain := detect(p.Name, nil)
			if !plain.Installed || plain.Model != "" {
				t.Fatalf("Detect: %+v", plain)
			}
			if out := run(plain); strings.Contains(out, "ARG:"+p.ModelFlag+"\n") {
				t.Errorf("model flag passed with no model:\n%s", out)
			}

			chosen := detect(p.Name, map[string]string{p.Name: model})
			if chosen.Model != model {
				t.Fatalf("Detect model = %q", chosen.Model)
			}
			if out := run(chosen); !strings.Contains(out, "ARG:"+p.ModelFlag+"\nARG:"+model+"\n") {
				t.Errorf("model flag %s %s missing:\n%s", p.ModelFlag, model, out)
			}
		})
	}
}

// Every preset can list its models, and every lister belongs to a preset.
func TestEveryPresetHasLister(t *testing.T) {
	for _, p := range Presets() {
		if _, ok := listers[p.Name]; !ok {
			t.Errorf("preset %q has no lister", p.Name)
		}
	}
	for name := range listers {
		if _, ok := LookupPreset(name); !ok {
			t.Errorf("lister %q has no preset", name)
		}
	}
}

// Detect's Name and Model feed straight into Resolve, and Harness.Resolve is
// shorthand for that.
func TestDetectFeedsResolve(t *testing.T) {
	fakeBin(t, "agy", "")
	for _, h := range Detect(map[string]string{"agy": "m-1"}) {
		if h.Name != "agy" {
			continue
		}
		want, err := Resolve(h.Name, h.Model)
		if err != nil {
			t.Fatal(err)
		}
		got, err := h.Resolve()
		if err != nil || !reflect.DeepEqual(got, want) || got.Model != "m-1" {
			t.Fatalf("got %+v, want %+v (err %v)", got, want, err)
		}
		return
	}
	t.Fatal("agy not detected")
}
