package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

func writeAgentExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func stubOtherAgentExecutables(t *testing.T, dir, keep string) {
	t.Helper()
	for _, preset := range agentPresets {
		if preset.Args[0] != keep {
			writeAgentExecutable(t, dir, preset.Args[0], "exit 1\n")
		}
	}
}

func harnessNamed(t *testing.T, rows []agentHarness, name string) agentHarness {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("harness %q not found in %+v", name, rows)
	return agentHarness{}
}

func waitHarnessStatus(t *testing.T, m *agentManager, name, want string) agentHarness {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		row := harnessNamed(t, m.Detect(false), name)
		if row.ModelStatus == want {
			return row
		}
		time.Sleep(10 * time.Millisecond)
	}
	row := harnessNamed(t, m.Detect(false), name)
	t.Fatalf("%s model status = %q, want %q", name, row.ModelStatus, want)
	return agentHarness{}
}

func responseHarnessNamed(t *testing.T, body map[string]any, name string) map[string]any {
	t.Helper()
	rows, ok := body["harnesses"].([]any)
	if !ok {
		t.Fatalf("harnesses response = %#v", body["harnesses"])
	}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if ok && row["name"] == name {
			return row
		}
	}
	t.Fatalf("harness %q not found in response %#v", name, body)
	return nil
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

func agentPostJSON(t *testing.T, s *Server, url string, body any) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
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
	t.Cleanup(func() {
		if s.gitWatcher != nil {
			s.gitWatcher.Stop()
		}
	})
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

func TestAgentDetectListsKnownHarnessesWithInstalledPaths(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	wantPaths := make(map[string]string, len(agentPresets))
	for _, preset := range agentPresets {
		wantPaths[preset.Name] = writeAgentExecutable(t, dir, preset.Args[0], "exit 1\n")
	}

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := m.Detect(false)
	if len(got) != len(agentPresets) {
		t.Fatalf("detected %d rows, want one per preset (%d)", len(got), len(agentPresets))
	}
	for i, h := range got {
		if h.Name != agentPresets[i].Name {
			t.Fatalf("row %d = %q, want %q", i, h.Name, agentPresets[i].Name)
		}
		if !h.Installed || h.Path != wantPaths[h.Name] {
			t.Errorf("%s installed/path = %v %q, want true %q", h.Name, h.Installed, h.Path, wantPaths[h.Name])
		}
		if !strings.Contains(h.Cmd, "{prompt}") {
			t.Errorf("%s cmd should show the template, got %q", h.Name, h.Cmd)
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

func TestPresetArgvOrder(t *testing.T) {
	for _, p := range agentPresets {
		n := len(p.Args)
		if n < 2 {
			t.Fatalf("preset %s args too short: %v", p.Name, p.Args)
		}
		if p.Args[n-1] != "{prompt}" {
			t.Fatalf("preset %s args %v: want {prompt} at the very end", p.Name, p.Args)
		}
	}
}

func TestSavedModelPromotesOnlyAfterReadyDiscovery(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeAgentExecutable(t, dir, "pi", `if [ "$1" = "--list-models" ]; then
	printf 'provider model context\nanthropic claude-sonnet 200k\n'
fi
`)
	stubOtherAgentExecutables(t, dir, "pi")
	if err := writeSettings(settings{
		Agent:  "pi",
		Models: map[string]string{"pi": "anthropic/claude-sonnet"},
	}); err != nil {
		t.Fatal(err)
	}

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name() != "pi" {
		t.Fatalf("restored harness = %q, want pi", m.Name())
	}
	if m.Model() != "" || strings.Contains(strings.Join(m.args, " "), "--model") {
		t.Fatalf("saved model was dispatched before validation: model=%q argv=%v", m.Model(), m.args)
	}
	first := harnessNamed(t, m.Detect(false), "pi")
	if first.ModelStatus != "loading" || first.Model != "" {
		t.Fatalf("first Pi snapshot = %+v, want loading with no effective model", first)
	}
	ready := waitHarnessStatus(t, m, "pi", "ready")
	if !reflect.DeepEqual(ready.Models, []string{"anthropic/claude-sonnet"}) {
		t.Fatalf("discovered models = %v", ready.Models)
	}
	if m.Model() != "anthropic/claude-sonnet" {
		t.Fatalf("promoted model = %q", m.Model())
	}
	if got := strings.Join(m.args, " "); !strings.Contains(got, "--model anthropic/claude-sonnet -p {prompt}") {
		t.Fatalf("promoted Pi argv = %q", got)
	}
}

func TestSavedModelIsPrunedAfterAuthoritativeDiscovery(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeAgentExecutable(t, dir, "pi", `printf 'provider model context\ngoogle gemini-flash 1m\n'`)
	stubOtherAgentExecutables(t, dir, "pi")
	if err := writeSettings(settings{
		Agent:  "pi",
		Models: map[string]string{"pi": "anthropic/removed"},
	}); err != nil {
		t.Fatal(err)
	}

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitHarnessStatus(t, m, "pi", "ready")
	if m.Name() != "pi" || m.Model() != "" || strings.Contains(strings.Join(m.args, " "), "--model") {
		t.Fatalf("invalid saved model affected selected harness: name=%q model=%q argv=%v", m.Name(), m.Model(), m.args)
	}
	if _, ok := readSettings().Models["pi"]; ok {
		t.Fatalf("absent model remains persisted: %v", readSettings().Models)
	}
}

func TestFailedDiscoveryRetainsPendingSavedModel(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeAgentExecutable(t, dir, "pi", "exit 7\n")
	stubOtherAgentExecutables(t, dir, "pi")
	if err := writeSettings(settings{
		Agent:  "pi",
		Models: map[string]string{"pi": "anthropic/retry-later"},
	}); err != nil {
		t.Fatal(err)
	}

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitHarnessStatus(t, m, "pi", "failed")
	if len(failed.Models) != 0 || m.Model() != "" || strings.Contains(strings.Join(m.args, " "), "--model") {
		t.Fatalf("failed discovery must use harness default: row=%+v model=%q argv=%v", failed, m.Model(), m.args)
	}
	if got := readSettings().Models["pi"]; got != "anthropic/retry-later" {
		t.Fatalf("pending model after failure = %q, want retained", got)
	}
}

func TestExplicitHarnessDefaultClearsSavedModel(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	writeAgentExecutable(t, dir, "pi", `printf 'provider model context\nanthropic claude-sonnet 200k\n'`)
	stubOtherAgentExecutables(t, dir, "pi")
	if err := writeSettings(settings{
		Agent:  "pi",
		Models: map[string]string{"pi": "anthropic/claude-sonnet"},
	}); err != nil {
		t.Fatal(err)
	}
	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitHarnessStatus(t, m, "pi", "ready")
	if err := m.Select("pi", ""); err != nil {
		t.Fatal(err)
	}
	if m.Model() != "" || strings.Contains(strings.Join(m.args, " "), "--model") {
		t.Fatalf("Harness default did not clear effective model: model=%q argv=%v", m.Model(), m.args)
	}
	if _, ok := readSettings().Models["pi"]; ok {
		t.Fatalf("Harness default did not clear persisted model: %v", readSettings().Models)
	}
}

func TestAgentSelectEndpointRejectsLoadingAndUnlistedModels(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
	writeAgentExecutable(t, dir, "pi", `sleep 0.1
printf 'provider model context\nanthropic listed 200k\n'
`)
	stubOtherAgentExecutables(t, dir, "pi")
	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(NewIndex(t.TempDir()), nil)
	s.SetAgent(m)
	t.Cleanup(func() {
		if s.gitWatcher != nil {
			s.gitWatcher.Stop()
		}
	})

	if code, _ := agentPost(t, s, "/api/agent/select?name=pi&model=anthropic/listed"); code != http.StatusConflict {
		t.Fatalf("select while discovering = %d, want 409", code)
	}
	waitHarnessStatus(t, m, "pi", "ready")
	if code, _ := agentPost(t, s, "/api/agent/select?name=pi&model=anthropic/missing"); code != http.StatusBadRequest {
		t.Fatalf("select unlisted model = %d, want 400", code)
	}
	if code, _ := agentPost(t, s, "/api/agent/select?name=pi&model=anthropic/listed"); code != http.StatusOK {
		t.Fatalf("select listed model = %d, want 200", code)
	}
}

func TestAgentHarnessEndpointReportsDiscoveryAndRefresh(t *testing.T) {
	isolateSettings(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
	modelsFile := filepath.Join(dir, "models")
	writeModels := func(model string) {
		t.Helper()
		body := "provider model context\nanthropic " + model + " 200k\n"
		if err := os.WriteFile(modelsFile, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeModels("first")
	writeAgentExecutable(t, dir, "pi", fmt.Sprintf(`sleep 0.05
while IFS= read -r line; do
	printf '%%s\n' "$line"
done < %q
`, modelsFile))
	stubOtherAgentExecutables(t, dir, "pi")

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(NewIndex(t.TempDir()), nil)
	s.SetAgent(m)
	t.Cleanup(func() {
		if s.gitWatcher != nil {
			s.gitWatcher.Stop()
		}
	})

	code, body := get(t, s, "/api/agent/harnesses")
	if code != http.StatusOK {
		t.Fatalf("initial harnesses = %d %v", code, body)
	}
	if row := responseHarnessNamed(t, body, "pi"); row["modelStatus"] != "loading" {
		t.Fatalf("initial Pi row = %#v, want loading", row)
	}
	waitEndpoint := func(wantModel string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			code, got := get(t, s, "/api/agent/harnesses")
			if code != http.StatusOK {
				t.Fatalf("harnesses = %d %v", code, got)
			}
			row := responseHarnessNamed(t, got, "pi")
			if row["modelStatus"] == "ready" {
				models, ok := row["models"].([]any)
				if !ok || len(models) != 1 || models[0] != wantModel {
					t.Fatalf("ready Pi models = %#v, want [%q]", row["models"], wantModel)
				}
				return row
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("Pi endpoint did not become ready with %q", wantModel)
		return nil
	}
	waitEndpoint("anthropic/first")

	writeModels("second")
	code, body = get(t, s, "/api/agent/harnesses?refresh=1")
	if code != http.StatusOK {
		t.Fatalf("refresh harnesses = %d %v", code, body)
	}
	if row := responseHarnessNamed(t, body, "pi"); row["modelStatus"] != "loading" {
		t.Fatalf("refresh Pi row = %#v, want loading", row)
	}
	waitEndpoint("anthropic/second")
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

// The job summary and API response list changed files, so their order must not
// follow Go's randomised map iteration. Repeated to catch a lucky ordering.
func TestChangedSinceIsSorted(t *testing.T) {
	before := map[string]string{"b.go": "M", "d.go": "M"}
	after := map[string]string{"c.go": "U", "a.go": "M", "d.go": "M"}
	for i := 0; i < 20; i++ {
		if got := strings.Join(changedSinceMaps(before, after), ","); got != "a.go,b.go,c.go" {
			t.Fatalf("changed = %q, want a.go,b.go,c.go", got)
		}
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

func TestCodexExecUsesSupportedApprovalAndWorkspaceSandbox(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, argv, _, err := resolveAgentSpec("codex", "gpt-5-mini")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "codex"), "-a", "never", "exec", "--sandbox", "workspace-write", "-m", "gpt-5-mini", "{prompt}"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("codex argv = %q, want %q", argv, want)
	}
}

func TestAllPresetDefaultsOmitModelFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, p := range agentPresets {
		writeAgentExecutable(t, dir, p.Args[0], "exit 0\n")
	}

	for _, p := range agentPresets {
		name, resolved, model, err := resolveAgentSpec(p.Name, "")
		if err != nil {
			t.Fatalf("resolveAgentSpec(%q): %v", p.Name, err)
		}
		if name != p.Name || model != "" {
			t.Errorf("%s resolved name/model = %q %q, want %q and Harness default", p.Name, name, model, p.Name)
		}
		if resolved[len(resolved)-1] != "{prompt}" {
			t.Errorf("%s final arg = %q, want {prompt}", p.Name, resolved[len(resolved)-1])
		}
		for _, arg := range resolved {
			if p.ModelFlag != "" && arg == p.ModelFlag {
				t.Errorf("%s default argv %v contains model flag %q", p.Name, resolved, p.ModelFlag)
			}
		}
	}

	_, explicit, model, err := resolveAgentSpec("pi", "anthropic/claude-sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if model != "anthropic/claude-sonnet" {
		t.Fatalf("explicit Pi model = %q", model)
	}
	if got, want := strings.Join(explicit[1:], " "), "--approve --model anthropic/claude-sonnet -p {prompt}"; got != want {
		t.Errorf("Pi explicit-model argv = %q, want %q", got, want)
	}
}

func TestAgentBatchPromptFormatting(t *testing.T) {
	items := []itemWithSnippet{
		{
			item: agentBatchItem{
				Path:        "a/b.go",
				L1:          10,
				L2:          12,
				Instruction: "rename foo to bar",
			},
			snippet: "func foo() {\n\treturn\n}",
		},
		{
			item: agentBatchItem{
				Path:        "c/d.go",
				L1:          5,
				L2:          5,
				Instruction: "add doc comment",
			},
			snippet: "type User struct{}",
		},
	}

	got := agentBatchPrompt(items)
	if !strings.Contains(got, "Batch Edit Request:") {
		t.Errorf("prompt missing batch header: %s", got)
	}
	if !strings.Contains(got, "### Edit 1: @a/b.go lines 10-12") {
		t.Errorf("prompt missing edit 1 header: %s", got)
	}
	if !strings.Contains(got, "**Instruction**: rename foo to bar") {
		t.Errorf("prompt missing instruction 1: %s", got)
	}
	if !strings.Contains(got, "### Edit 2: @c/d.go line 5") {
		t.Errorf("prompt missing edit 2 header: %s", got)
	}
	if !strings.Contains(got, "**Instruction**: add doc comment") {
		t.Errorf("prompt missing instruction 2: %s", got)
	}
}

func TestAgentBatchEditRejectsIntraBatchOverlap(t *testing.T) {
	isolateSettings(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nvar X = 1\nvar Y = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := newAgentManager(root, "echo {prompt}", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Overlapping ranges on the same file: lines 1-2 and lines 2-3
	items := []agentBatchItem{
		{Abs: filepath.Join(root, "a.go"), Path: "a.go", L1: 1, L2: 2, Instruction: "edit 1"},
		{Abs: filepath.Join(root, "a.go"), Path: "a.go", L1: 2, L2: 3, Instruction: "edit 2"},
	}
	if _, err := m.StartBatch(items, false); err == nil {
		t.Fatal("expected overlapping batch edits on same file to be rejected, got nil error")
	}

	// Disjoint ranges on the same file: lines 1-1 and lines 3-3 (should succeed)
	validItems := []agentBatchItem{
		{Abs: filepath.Join(root, "a.go"), Path: "a.go", L1: 1, L2: 1, Instruction: "edit 1"},
		{Abs: filepath.Join(root, "a.go"), Path: "a.go", L1: 3, L2: 3, Instruction: "edit 2"},
	}
	job, err := m.StartBatch(validItems, false)
	if err != nil {
		t.Fatalf("expected disjoint batch edits to succeed, got %v", err)
	}
	if job.BatchCount != 2 {
		t.Errorf("job.BatchCount = %d, want 2", job.BatchCount)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if j := m.Job(job.ID); j != nil && !j.Running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAgentBatchEditRunsHarness(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)

	s := agentServer(t, root, writeHarness(t,
		`echo "// edited 1" >> keep.go
echo "// edited 2" >> sub/mod.go
`))

	payload := map[string]any{
		"edits": []map[string]any{
			{"path": "keep.go", "l1": 1, "l2": 1, "instruction": "edit keep"},
			{"path": "sub/mod.go", "l1": 1, "l2": 1, "instruction": "edit mod"},
		},
	}
	code, body := agentPostJSON(t, s, "/api/agent/batch", payload)
	if code != 200 {
		t.Fatalf("batch edit = %d, want 200 (error: %v)", code, body["error"])
	}
	id := int64(body["id"].(float64))
	j := waitIdleID(t, s, id)
	if j.Error != "" {
		t.Fatalf("batch job failed: %s", j.Error)
	}
	if len(j.Changed) != 2 {
		t.Fatalf("changed = %v, want 2 files", j.Changed)
	}
}

func TestAgentAllowsIndividualAndBatchEditsConcurrently(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)

	s := agentServer(t, root, writeHarness(t,
		`sleep 0.2
echo "// edited" >> keep.go
`))

	// Start individual edit on keep.go line 1
	code1, body1 := agentPost(t, s, "/api/agent/edit?path=keep.go&l1=1&l2=1&instruction=one")
	if code1 != 200 {
		t.Fatalf("edit 1 = %d, want 200 (error: %v)", code1, body1["error"])
	}
	id1 := int64(body1["id"].(float64))

	// Attempting an overlapping batch edit on keep.go:1-2 must be rejected
	overlapPayload := map[string]any{
		"edits": []map[string]any{
			{"path": "keep.go", "l1": 1, "l2": 2, "instruction": "overlap"},
		},
	}
	overlapCode, _ := agentPostJSON(t, s, "/api/agent/batch", overlapPayload)
	if overlapCode != 409 {
		t.Fatalf("overlapping batch edit = %d, want 409", overlapCode)
	}

	// Non-overlapping batch edit on keep.go:2-2 and sub/mod.go:1-1 must be accepted concurrently
	batchPayload := map[string]any{
		"edits": []map[string]any{
			{"path": "keep.go", "l1": 2, "l2": 2, "instruction": "non-overlap keep"},
			{"path": "sub/mod.go", "l1": 1, "l2": 1, "instruction": "non-overlap mod"},
		},
	}
	code2, body2 := agentPostJSON(t, s, "/api/agent/batch", batchPayload)
	if code2 != 200 {
		t.Fatalf("batch edit 2 = %d, want 200 (error: %v)", code2, body2["error"])
	}
	id2 := int64(body2["id"].(float64))

	j1 := waitIdleID(t, s, id1)
	j2 := waitIdleID(t, s, id2)
	if j1.Error != "" {
		t.Fatalf("job 1 failed: %s", j1.Error)
	}
	if j2.Error != "" {
		t.Fatalf("job 2 failed: %s", j2.Error)
	}
}

func TestAgentBatchEditPayloadVariations(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := gitRepo(t)
	s := agentServer(t, root, writeHarness(t, "exit 0\n"))

	// 1. Direct array JSON body
	directArray := []map[string]any{
		{"path": "keep.go", "l1": 1, "l2": 1, "instruction": "arr 1"},
	}
	c1, b1 := agentPostJSON(t, s, "/api/agent/batch", directArray)
	if c1 != 200 {
		t.Fatalf("direct array batch = %d, want 200 (err: %v)", c1, b1["error"])
	}
	waitIdleID(t, s, int64(b1["id"].(float64)))

	// 2. Query parameter edits JSON string
	c2, b2 := agentPost(t, s, "/api/agent/batch?edits=%5B%7B%22path%22%3A%22keep.go%22%2C%22l1%22%3A1%2C%22l2%22%3A1%2C%22instruction%22%3A%22query%22%7D%5D")
	if c2 != 200 {
		t.Fatalf("query param batch = %d, want 200 (err: %v)", c2, b2["error"])
	}
	waitIdleID(t, s, int64(b2["id"].(float64)))

	// 3. Single edit object JSON body
	singleObj := map[string]any{
		"path": "keep.go", "l1": 1, "l2": 1, "instruction": "single",
	}
	c3, b3 := agentPostJSON(t, s, "/api/agent/batch", singleObj)
	if c3 != 200 {
		t.Fatalf("single obj batch = %d, want 200 (err: %v)", c3, b3["error"])
	}
	waitIdleID(t, s, int64(b3["id"].(float64)))

	// 4. Empty payload -> 400
	c4, b4 := agentPostJSON(t, s, "/api/agent/batch", map[string]any{"edits": []any{}})
	if c4 != 400 {
		t.Fatalf("empty batch = %d, want 400", c4)
	}
	if b4["error"] != "invalid or empty batch edits payload" {
		t.Fatalf("empty batch error = %v", b4["error"])
	}
}

func TestCommitMessagePrompt(t *testing.T) {
	files := []string{"web/src/gitpanel.js", "server.go"}
	stat := " web/src/gitpanel.js | 15 +++\n server.go           | 40 ++-\n 2 files changed, 45 insertions(+), 10 deletions(-)"
	diff := "diff --git a/server.go b/server.go\n--- a/server.go\n+++ b/server.go\n@@ -1 +1 @@\n-old\n+new"
	instruction := "Follow conventional commits format"

	p := commitMessagePrompt(files, stat, diff, instruction)
	if !strings.Contains(p, "Changed files (2):") {
		t.Errorf("prompt missing changed files header:\n%s", p)
	}
	if !strings.Contains(p, "- web/src/gitpanel.js") || !strings.Contains(p, "- server.go") {
		t.Errorf("prompt missing changed file paths:\n%s", p)
	}
	if !strings.Contains(p, "Summary of changes (diffstat):") || !strings.Contains(p, stat) {
		t.Errorf("prompt missing diffstat summary:\n%s", p)
	}
	if !strings.Contains(p, "Staged diff:") || !strings.Contains(p, diff) {
		t.Errorf("prompt missing staged diff:\n%s", p)
	}
	if !strings.Contains(p, "Additional instructions from the user: "+instruction) {
		t.Errorf("prompt missing user instruction:\n%s", p)
	}

	// Test truncation when there are > 100 files
	manyFiles := make([]string, 125)
	for i := range manyFiles {
		manyFiles[i] = fmt.Sprintf("file%d.go", i)
	}
	p2 := commitMessagePrompt(manyFiles, "", "", "")
	if !strings.Contains(p2, "Changed files (125):") {
		t.Errorf("prompt missing total count:\n%s", p2)
	}
	if !strings.Contains(p2, "... and 25 more files") {
		t.Errorf("prompt missing truncation notice:\n%s", p2)
	}
	if !strings.Contains(p2, "- file0.go") || !strings.Contains(p2, "- file99.go") {
		t.Errorf("prompt missing head files:\n%s", p2)
	}
	if strings.Contains(p2, "- file100.go") {
		t.Errorf("prompt contains file beyond 100:\n%s", p2)
	}
}
