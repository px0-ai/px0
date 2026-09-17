package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeHarness creates an executable stand-in for a coding harness, outside the
// workspace so that it does not show up as a change the run made.
func writeHarness(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "harness.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// isolateSettings points the settings file at a temp dir, so a test never reads
// or overwrites the choice the developer running it has made.
func isolateSettings(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

// agentPost speaks the way the browser does: POST, with an Origin that matches
// a Host localPost will accept.
func agentPost(t *testing.T, s *Server, url string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, url, nil)
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var m map[string]any
	json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

func agentServer(t *testing.T, root, harness string) *Server {
	t.Helper()
	isolateSettings(t)
	m, err := newAgentManager(root, harness+" {prompt}", nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)
	s.SetAgent(m)
	return s
}

// waitIdle waits for the most recently started job to finish. Tests that keep
// several jobs in flight at once poll a specific id instead.
func waitIdle(t *testing.T, s *Server) *agentJob {
	t.Helper()
	return waitIdleID(t, s, 0)
}

func waitIdleID(t *testing.T, s *Server, id int64) *agentJob {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if j := s.agent.Job(id); j != nil && !j.Running {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("harness did not finish")
	return nil
}

func TestAgentSpecResolution(t *testing.T) {
	isolateSettings(t)
	root := t.TempDir()

	if _, err := newAgentManager(root, "echo hello", nil); err == nil {
		t.Fatal("a template without {prompt} should be refused")
	}
	if _, err := newAgentManager(root, "px0-not-a-real-binary {prompt}", nil); err == nil {
		t.Fatal("a missing binary should be refused at startup, not on first use")
	}

	m, err := newAgentManager(root, "echo {prompt}", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name() != "echo" {
		t.Fatalf("name = %q, want echo", m.Name())
	}
	if !m.Pinned() {
		t.Fatal("-agent should pin the harness")
	}
	if err := m.Select("echo {prompt}"); err == nil {
		t.Fatal("a pinned harness must not be changeable from the UI")
	}

	// No flag and no saved choice: editing is available, nothing is selected.
	idle, err := newAgentManager(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if idle.Name() != "" || idle.Pinned() {
		t.Fatalf("fresh manager = %q pinned=%v, want unselected and unpinned", idle.Name(), idle.Pinned())
	}
}

func TestAgentDetectListsKnownHarnesses(t *testing.T) {
	isolateSettings(t)
	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := m.Detect()
	if len(got) != len(agentPresets) {
		t.Fatalf("detected %d rows, want one per preset (%d)", len(got), len(agentPresets))
	}
	for i, h := range got {
		if h.Name != agentPresets[i].Name {
			t.Fatalf("row %d = %q, want %q", i, h.Name, agentPresets[i].Name)
		}
		if !strings.Contains(h.Cmd, "{prompt}") {
			t.Fatalf("%s cmd should show the template, got %q", h.Name, h.Cmd)
		}
		if h.Installed && h.Path == "" {
			t.Fatalf("%s reported installed with no path", h.Name)
		}
	}
}

func TestAgentSelectPersistsOutsideWorkspace(t *testing.T) {
	cfg := isolateSettings(t)
	root := t.TempDir()

	m, err := newAgentManager(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Select("px0-not-a-real-binary"); err == nil {
		t.Fatal("selecting something that is not installed should fail")
	}

	// echo stands in for a harness binary that exists on every machine.
	if err := m.Select("echo {prompt}"); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "echo" {
		t.Fatalf("selected = %q, want echo", m.Name())
	}

	if _, err := os.Stat(filepath.Join(cfg, "px0", "settings.json")); err != nil {
		t.Fatalf("settings file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "settings.json")); !os.IsNotExist(err) {
		t.Fatal("settings must never be written into the workspace")
	}

	// A later run restores the choice, template and all.
	restored, err := newAgentManager(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Name() != "echo" {
		t.Fatalf("restored = %q, want echo", restored.Name())
	}

	if err := restored.Select(""); err != nil {
		t.Fatal(err)
	}
	if restored.Name() != "" {
		t.Fatalf("cleared = %q, want empty", restored.Name())
	}
	if again, _ := newAgentManager(root, "", nil); again.Name() != "" {
		t.Fatalf("clearing did not persist, got %q", again.Name())
	}
}

func TestAgentHarnessEndpointsRequireAvailability(t *testing.T) {
	s, _ := newTestServer(t) // no SetAgent: editing unavailable

	if code, _ := get(t, s, "/api/agent/job"); code != http.StatusNotFound {
		t.Fatalf("job = %d, want 404", code)
	}
	if code, _ := get(t, s, "/api/agent/harnesses"); code != http.StatusNotFound {
		t.Fatalf("harnesses = %d, want 404", code)
	}
	if code, _ := agentPost(t, s, "/api/agent/edit?path=main.go&l1=1&l2=1&instruction=hi"); code != http.StatusNotFound {
		t.Fatalf("edit = %d, want 404", code)
	}

	_, meta := get(t, s, "/api/meta")
	if meta["agent"] != "" {
		t.Fatalf("meta agent = %v, want empty", meta["agent"])
	}
	if got, ok := meta["agents"].([]any); !ok || len(got) != 0 {
		t.Fatalf("meta agents = %v, want an empty list", meta["agents"])
	}
}

func TestAgentEditRefusedUntilHarnessChosen(t *testing.T) {
	isolateSettings(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := newAgentManager(root, "", nil) // available, nothing selected
	if err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)
	s.SetAgent(m)

	code, body := agentPost(t, s, "/api/agent/edit?path=a.go&l1=1&l2=1&instruction=hi")
	if code != 400 {
		t.Fatalf("edit with nothing selected = %d, want 400", code)
	}
	if !strings.Contains(body["error"].(string), "no coding harness") {
		t.Fatalf("error = %q", body["error"])
	}

	// Picking one over HTTP is enough to make editing work.
	if code, _ = agentPost(t, s, "/api/agent/select?name="+"echo+%7Bprompt%7D"); code != 200 {
		t.Fatalf("select = %d, want 200", code)
	}
	if code, _ = agentPost(t, s, "/api/agent/edit?path=a.go&l1=1&l2=1&instruction=hi"); code != 200 {
		t.Fatalf("edit after select = %d, want 200", code)
	}
	waitIdle(t, s)
}

func TestAgentEditRunsHarnessAndReportsChange(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	out := t.TempDir()
	// keep.go is committed and clean, so no force is needed.
	s := agentServer(t, root, writeHarness(t,
		"printf 'touched\\n' >> keep.go\nprintf '%s' \"$1\" > "+filepath.Join(out, "prompt.txt")+"\n"))

	code, _ := agentPost(t, s, "/api/agent/edit?path=keep.go&l1=1&l2=1&instruction=add+a+line")
	if code != 200 {
		t.Fatalf("edit = %d, want 200", code)
	}

	job := waitIdle(t, s)
	if job.Error != "" {
		t.Fatalf("harness failed: %s (log: %s)", job.Error, job.Log)
	}

	body, err := os.ReadFile(filepath.Join(root, "keep.go"))
	if err != nil || !strings.Contains(string(body), "touched") {
		t.Fatalf("harness did not edit the file: %q %v", body, err)
	}
	if len(job.Changed) != 1 || job.Changed[0] != "keep.go" {
		t.Fatalf("changed = %v, want [keep.go]", job.Changed)
	}
	if !job.Tracked {
		t.Fatal("tracked should be true inside a repository")
	}

	// The prompt must carry the anchor and the instruction the user wrote.
	prompt, err := os.ReadFile(filepath.Join(out, "prompt.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"@keep.go line 1", "### Instruction", "add a line"} {
		if !strings.Contains(string(prompt), want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// Outside a repository px0 cannot name what a harness touched. The job must say
// so, because an empty change list would otherwise read as "nothing happened"
// and the client would skip the reload after a real edit.
func TestAgentOutsideGitReportsUnknownChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := agentServer(t, root, writeHarness(t, "printf 'touched\\n' >> a.go\n"))

	// With no git there is also no uncommitted-work guard to satisfy.
	if code, _ := agentPost(t, s, "/api/agent/edit?path=a.go&l1=1&l2=1&instruction=hi"); code != 200 {
		t.Fatalf("edit = %d, want 200", code)
	}
	job := waitIdle(t, s)
	if job.Error != "" {
		t.Fatalf("run failed: %s (log: %s)", job.Error, job.Log)
	}
	if job.Tracked {
		t.Fatal("tracked should be false outside a repository")
	}
	if len(job.Changed) != 0 {
		t.Fatalf("changed = %v, want empty outside a repository", job.Changed)
	}
	body, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if !strings.Contains(string(body), "touched") {
		t.Fatalf("harness did not edit the file: %q", body)
	}
}

func TestAgentRefusesSecondEditWhileRunning(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	s := agentServer(t, root, writeHarness(t, "sleep 2\n"))

	if code, _ := agentPost(t, s, "/api/agent/edit?path=keep.go&l1=1&l2=1&instruction=one"); code != 200 {
		t.Fatalf("first edit = %d, want 200", code)
	}
	code, body := agentPost(t, s, "/api/agent/edit?path=keep.go&l1=1&l2=1&instruction=two")
	if code != http.StatusConflict {
		t.Fatalf("second edit = %d, want 409", code)
	}
	if !strings.Contains(body["error"].(string), "already running") {
		t.Fatalf("error = %q", body["error"])
	}

	s.agent.Cancel()
	waitIdle(t, s)
}

func TestAgentAllowsEditOverUncommittedFileWithoutForce(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	// sub/mod.go is modified but not committed by gitRepo.
	s := agentServer(t, root, writeHarness(t, "printf 'touched\\n' >> keep.go\n"))

	code, _ := agentPost(t, s, "/api/agent/edit?path=sub/mod.go&l1=1&l2=1&instruction=hi")
	if code != http.StatusOK {
		t.Fatalf("edit over uncommitted work = %d, want 200", code)
	}
	if job := waitIdle(t, s); job.Error != "" {
		t.Fatalf("run failed: %s", job.Error)
	}
}

func TestAgentModelSelectionAndDefaults(t *testing.T) {
	isolateSettings(t)
	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := m.Detect()
	for _, h := range rows {
		if len(h.Models) == 0 {
			t.Fatalf("%s should list models", h.Name)
		}
		if h.Model == "" {
			t.Fatalf("%s should have a default model", h.Name)
		}
		if h.Model != h.Models[0] {
			t.Fatalf("%s default model %q != least capable model %q", h.Name, h.Model, h.Models[0])
		}
	}
}

func TestPresetArgvOrder(t *testing.T) {
	for _, p := range agentPresets {
		n := len(p.Args)
		if n < 2 {
			t.Fatalf("preset %s args too short: %v", p.Name, p.Args)
		}
		if p.Args[n-1] != "{prompt}" {
			t.Fatalf("preset %s args %v: want {prompt} at the very end", p.Name, p.Args)
		}

		// When resolved with default model, {prompt} must remain at the very end
		_, resolved, _, err := resolveAgentSpec(p.Name, "")
		if err != nil {
			continue // tool may not be installed in test env
		}
		rn := len(resolved)
		if rn < 2 || resolved[rn-1] != "{prompt}" {
			t.Fatalf("resolved %s args %v: want {prompt} at the very end", p.Name, resolved)
		}
	}
}

func TestClaudeModelDiscovery(t *testing.T) {
	dir := t.TempDir()
	fakeClaude := filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho 'Current model: Sonnet 5'\necho 'Usage: /model <name>. Available: sonnet, opus, haiku, fable, best, sonnet[1m], opus[1m], fable[1m], opusplan, default, or a full model ID.'\n"
	if err := os.WriteFile(fakeClaude, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	discoveredModelsMu.Lock()
	delete(discoveredModels, "claude")
	delete(discoveringModels, "claude")
	discoveredModelsMu.Unlock()

	runModelDiscovery("claude", fakeClaude, []string{"haiku", "sonnet", "opus"})

	discoveredModelsMu.Lock()
	models := discoveredModels["claude"]
	discoveredModelsMu.Unlock()

	if len(models) == 0 {
		t.Fatal("expected discovered models for claude, got none")
	}
	if models[0] != "haiku" {
		t.Fatalf("expected least capable default 'haiku' at index 0, got %q", models[0])
	}
	// Check that fable, best, sonnet[1m] etc are parsed
	foundFable := false
	for _, m := range models {
		if m == "fable" {
			foundFable = true
			break
		}
	}
	if !foundFable {
		t.Fatalf("expected 'fable' in discovered models: %v", models)
	}
}

// Editing in the diff view means editing a file that is already modified. Its
// git status reads M before and after, so the change has to be seen some other way.
func TestAgentReportsEditToAlreadyModifiedFile(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	s := agentServer(t, root, writeHarness(t, "printf 'touched\\n' >> sub/mod.go\n"))

	if code, _ := agentPost(t, s, "/api/agent/edit?path=sub/mod.go&l1=1&l2=1&instruction=hi&force=1"); code != 200 {
		t.Fatalf("edit = %d, want 200", code)
	}
	job := waitIdle(t, s)
	if job.Error != "" {
		t.Fatalf("harness failed: %s", job.Error)
	}
	if len(job.Changed) != 1 || job.Changed[0] != "sub/mod.go" {
		t.Fatalf("changed = %v, want [sub/mod.go]", job.Changed)
	}
}

// Two edits on disjoint line ranges of the same file run at the same time;
// only an overlapping range is refused.
func TestAgentAllowsNonOverlappingEditsInParallel(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	s := agentServer(t, root, writeHarness(t, "sleep 1\n"))

	code, first := agentPost(t, s, "/api/agent/edit?path=keep.go&l1=1&l2=1&instruction=one")
	if code != 200 {
		t.Fatalf("first edit = %d, want 200", code)
	}
	code, _ = agentPost(t, s, "/api/agent/edit?path=keep.go&l1=2&l2=2&instruction=two")
	if code != 200 {
		t.Fatalf("disjoint-range edit = %d, want 200 (should run in parallel)", code)
	}
	code, _ = agentPost(t, s, "/api/agent/edit?path=keep.go&l1=1&l2=2&instruction=three")
	if code != http.StatusConflict {
		t.Fatalf("edit overlapping both = %d, want 409", code)
	}

	firstID := int64(first["id"].(float64))
	if job := waitIdleID(t, s, firstID); job.Error != "" {
		t.Fatalf("first edit failed: %s", job.Error)
	}
}

func TestAgentMutationsRejectCrossOriginPost(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	s := agentServer(t, root, writeHarness(t, "printf 'touched\\n' >> keep.go\n"))

	for _, path := range []string{
		"/api/agent/edit?path=keep.go&l1=1&l2=1&instruction=hi",
		"/api/agent/select?name=claude",
		"/api/agent/cancel",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Host = "127.0.0.1:7777"
		req.Header.Set("Origin", "http://evil.example.com")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-origin %s = %d, want 403", path, rec.Code)
		}

		if code, _ := get(t, s, path); code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s = %d, want 405", path, code)
		}
	}
}

func TestAgentCancelJob(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	// Harness that sleeps to simulate a long-running edit
	s := agentServer(t, root, writeHarness(t, "sleep 5\n"))

	code, first := agentPost(t, s, "/api/agent/edit?path=keep.go&l1=1&l2=1&instruction=first")
	if code != http.StatusOK {
		t.Fatalf("edit first = %d, want 200", code)
	}
	id := int64(first["id"].(float64))

	// Cancel by id
	cancelCode, cancelBody := agentPost(t, s, fmt.Sprintf("/api/agent/cancel?id=%d", id))
	if cancelCode != http.StatusOK {
		t.Fatalf("cancel = %d, want 200", cancelCode)
	}
	if cancelled, _ := cancelBody["cancelled"].(bool); !cancelled {
		t.Fatalf("cancelled = false, want true")
	}

	// Job should be stopped
	time.Sleep(100 * time.Millisecond)
	code, job := get(t, s, fmt.Sprintf("/api/agent/job?id=%d", id))
	if code != http.StatusOK {
		t.Fatalf("job = %d, want 200", code)
	}
	if running, _ := job["running"].(bool); running {
		t.Fatalf("job still running after cancel")
	}
}

func TestReadLineRange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		l1, l2 int
		want   string
	}{
		{2, 3, "two\nthree"},
		{1, 1, "one"},
		{3, 99, "three\nfour\n"}, // clamped to the end, trailing blank line included
		{0, 1, "one"},            // l1 below 1 is clamped
	} {
		got, err := readLineRange(p, tc.l1, tc.l2)
		if err != nil {
			t.Fatalf("%d-%d: %v", tc.l1, tc.l2, err)
		}
		if got != tc.want {
			t.Fatalf("%d-%d = %q, want %q", tc.l1, tc.l2, got, tc.want)
		}
	}
	if _, err := readLineRange(p, 50, 60); err == nil {
		t.Fatal("a range past the end should fail")
	}
}

func TestChangedSinceReportsBothDirections(t *testing.T) {
	before := map[string]string{"stays.go": "M", "reverted.go": "M"}
	after := map[string]string{"stays.go": "M", "new.go": "U"}

	got := map[string]bool{}
	for _, p := range changedSinceMaps(before, after) {
		got[p] = true
	}
	if got["stays.go"] {
		t.Fatal("an unchanged status should not be reported")
	}
	if !got["new.go"] {
		t.Fatal("a newly dirty file should be reported")
	}
	if !got["reverted.go"] {
		t.Fatal("a file restored to its committed state should be reported")
	}
}

func TestLineRefAndPrompt(t *testing.T) {
	if lineRef(4, 4) != "4" {
		t.Fatalf("single line ref = %q", lineRef(4, 4))
	}
	if lineRef(4, 9) != "4-9" {
		t.Fatalf("range ref = %q", lineRef(4, 9))
	}
	p := agentPrompt("web/src/app.js", 2, 5, "const x = 1;", "rename x to count")
	for _, want := range []string{"@web/src/app.js lines 2-5", "```js", "const x = 1;", "rename x to count"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
	pSingle := agentPrompt("a.go", 4, 4, "pkg a", "fix")
	if !strings.Contains(pSingle, "@a.go line 4") {
		t.Fatalf("single line prompt missing @a.go line 4:\n%s", pSingle)
	}
}

func TestShellQuoteAndCommand(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{
			in:   []string{"agy", "--mode", "accept-edits", "-p", "hello world"},
			want: "agy --mode accept-edits -p 'hello world'",
		},
		{
			in:   []string{"echo", "it's working"},
			want: "echo 'it'\\''s working'",
		},
		{
			in:   []string{"tool", ""},
			want: "tool ''",
		},
	}
	for _, tc := range cases {
		got := shellCommand(tc.in)
		if got != tc.want {
			t.Errorf("shellCommand(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAllPresetArgvFormatting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	for _, p := range agentPresets {
		binPath := filepath.Join(dir, p.Args[0])
		if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	for _, p := range agentPresets {
		name, resolved, model, err := resolveAgentSpec(p.Name, "")
		if err != nil {
			t.Fatalf("resolveAgentSpec(%q) error: %v", p.Name, err)
		}
		if name != p.Name {
			t.Errorf("name = %q, want %q", name, p.Name)
		}
		if model != p.DefaultModel {
			t.Errorf("model = %q, want %q", model, p.DefaultModel)
		}
		if resolved[len(resolved)-1] != "{prompt}" {
			t.Errorf("%s final arg = %q, want {prompt}", p.Name, resolved[len(resolved)-1])
		}
		// Ensure model flag was inserted properly
		hasModel := false
		for i, a := range resolved {
			if a == p.ModelFlag && i+1 < len(resolved) && resolved[i+1] == p.DefaultModel {
				hasModel = true
				break
			}
		}
		if !hasModel {
			t.Errorf("%s resolved args %v missing model flag %q %q", p.Name, resolved, p.ModelFlag, p.DefaultModel)
		}
	}
}
