package main

// Regression tests for defects found while reviewing the bring-your-own-key and
// agent-auth work. Each one fails on the code as it was and passes as it is now,
// so none of them can quietly stop testing what it was written for.

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Regression tests for defects found in review of the BYOK/auth work. Each one
// fails on the code as it was and passes as it is now, so none of them can quietly
// stop testing what it was written for.

func TestRootFromGitIsUsableAsAWindowsPath(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(sub, "app.go")
	if err := os.WriteFile(f, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	run("config", "core.autocrlf", "false")

	// resolveTarget hands git's answer straight to NewIndex. Git for Windows
	// reports the toplevel with forward slashes, and every "is this path under
	// the root" test then fails, so the whole workspace is unservable.
	got, _, _, err := resolveTarget(f)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && strings.Contains(got, "/") {
		t.Errorf("root from git is slash-form on Windows and cannot be joined: %q", got)
	}

	ix := NewIndex(got)
	ix.Build()
	s := NewServer(ix, newLSPManager(got, false))
	if _, _, ok := s.safePath("pkg/app.go"); !ok {
		t.Errorf("safePath refused a path inside the root %q", got)
	}
	code, _ := get(t, s, "/api/file?path=pkg/app.go")
	if code != http.StatusOK {
		t.Errorf("GET /api/file = %d, want 200 (root=%q)", code, got)
	}
}

func TestSettingAnAuthModeKeepsTheHarnessAndModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	harness := "claude"
	if err := writeSettings(settingsPatch{
		Agent:  &harness,
		Models: map[string]string{"claude": "opus"},
	}); err != nil {
		t.Fatal(err)
	}

	m := &agentManager{models: map[string]string{}}
	if err := m.SetAuthMode("claude", authModeOAuth); err != nil {
		t.Fatal(err)
	}

	raw := readSettingsRawMap()
	if got, ok := raw["agent"]; !ok || got != "claude" {
		t.Errorf("selected harness lost when the auth mode changed: %v", raw["agent"])
	}
	if _, ok := raw["models"]; !ok {
		t.Errorf("per-harness models lost when the auth mode changed: %v", raw["models"])
	}
	if _, ok := raw["authModes"]; !ok {
		t.Errorf("auth mode was not recorded: %v", raw)
	}
}

func TestThreadTurnsReceiveHeldCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := setCredential("openai", "sk-openai-test"); err != nil {
		t.Fatal(err)
	}
	// A harness driven by a key, in key mode, is the case where injection is the
	// whole point: without it every edit fails "no API key".
	if err := (&agentManager{models: map[string]string{}}).SetAuthMode("aider", authModeKey); err != nil {
		t.Fatal(err)
	}

	env := (&agentManager{models: map[string]string{}}).childEnvFor("aider")
	if env == nil {
		t.Fatal("childEnvFor returned nil, so the harness inherits an environment with no key")
	}
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "OPENAI_API_KEY=") && !strings.HasSuffix(e, "=") {
			found = true
		}
	}
	if !found {
		t.Errorf("no non-empty OPENAI_API_KEY in the child environment")
	}
}

func TestPathspecMagicCannotRewriteTheIndex(t *testing.T) {
	if !gitInstalled() {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_SYSTEM="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// A hostile repository can legitimately contain a file whose name starts
	// with ":" -- it is a valid filename everywhere except in a pathspec, where
	// it means magic. ":!a.txt" in particular excludes everything but a.txt.
	// Windows rejects ':' in a filename outright, so a repository on Windows
	// cannot contain this name and the attack does not apply there. What is
	// still worth checking everywhere is that the pathspec px0 builds names the
	// file it was asked about, which is what the unit check below covers.
	if runtime.GOOS == "windows" {
		if got := literalPathspec(":!a.txt"); !strings.HasPrefix(got, ":(literal)") {
			t.Errorf("a magic pathspec was not neutralised: %q", got)
		}
		if got := literalPathspec("a.txt"); got != "a.txt" {
			t.Errorf("an ordinary path was rewritten: %q", got)
		}
		return
	}
	hostile := filepath.Join(root, ":!a.txt")
	if err := os.WriteFile(hostile, []byte("hostile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("init")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	run("config", "core.autocrlf", "false")
	run("add", "-A")
	run("commit", "-qm", "init")

	// Dirty two files, then stage only the hostile one. Staging it must not
	// sweep in the rest of the tree.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("b2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gitStage(root, ":!a.txt"); err != nil {
		t.Fatalf("gitStage: %v", err)
	}

	staged := gitStagedFiles(root)
	if len(staged) != 1 {
		t.Errorf("staging %q staged %v; pathspec magic escaped and rewrote the index", ":!a.txt", staged)
	}
}

func TestBareCarriageReturnDoesNotDesyncTheHighlighter(t *testing.T) {
	// Chroma counts a bare CR as a line break. px0 split on \n only, so a file
	// with one -- an old-Mac file, or a generated bundle assembled on Windows --
	// lexed to more lines than it had, and every chunk after the first bare CR
	// was rendered against the wrong source line.
	// Before the fix px0 kept the bare CR in the source, so the lexer counted a
	// line the line table did not: Total said 5 lines, the lexer produced 6, and
	// every chunk after the CR was rendered against the wrong source line.
	src := "package main\n\rfunc main() {}\nvar x = 1\nvar y = 2\n"
	d := newDoc(src, "a.go")
	raw := strings.Split(d.src, "\n")
	if d.Total != len(raw) {
		t.Errorf("Total = %d but the stored source splits into %d lines", d.Total, len(raw))
	}
	if strings.ContainsRune(d.src, '\r') {
		t.Errorf("a bare CR survived into the source the lexer sees")
	}
	full := d.tokenise(d.src, d.Total)
	// Every non-blank source line must have produced non-empty output.
	for i := range raw {
		if strings.TrimSpace(raw[i]) == "" {
			continue
		}
		if full[i] == "" {
			t.Errorf("line %d (%q) came back empty; the lexer and the line table disagree", i, raw[i])
		}
	}
}

func TestConcurrentCredentialWritesDoNotLoseAKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the write is a rename over an existing file, whose Windows semantics differ")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := setCredential("anthropic", "sk-ant-original"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 3)
	for _, p := range []string{"openai", "google", "github"} {
		go func(id string) {
			_ = setCredential(id, "key-"+id)
			done <- struct{}{}
		}(p)
	}
	for i := 0; i < 3; i++ {
		<-done
	}
	got := readCredentials()
	if len(got) != 4 {
		names := make([]string, 0, len(got))
		for k := range got {
			names = append(names, k)
		}
		t.Errorf("4 concurrent writes left %d providers %v; a read-modify-write lost an update", len(got), names)
	}
}

func TestCredentialNeverAppearsInSettingsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := setCredential("anthropic", "sk-ant-secret-value"); err != nil {
		t.Fatal(err)
	}
	// A provider key belongs in credentials.json. If it also lands in
	// settings.json it is exposed to everything that reads or backs up settings.
	data, err := os.ReadFile(settingsPath())
	if os.IsNotExist(err) {
		return // no settings file was written at all, which is the best case
	}
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(raw)
	if strings.Contains(string(b), "sk-ant-secret-value") {
		t.Errorf("a provider key leaked into settings.json")
	}
}

func TestHarnessDisplayNameDropsTheExecutableExtension(t *testing.T) {
	// The switches that pick a protocol are written against bare names, so a
	// pinned "claude.exe" has to resolve to "claude" or the harness is treated
	// as one px0 knows nothing about.
	if got := harnessDisplayName(`C:\bin\claude.exe`); got != "claude" {
		t.Errorf("harnessDisplayName(claude.exe) = %q, want claude", got)
	}
	if got := harnessDisplayName(`C:\bin\claude.cmd`); got != "claude" {
		t.Errorf("harnessDisplayName(claude.cmd) = %q, want claude", got)
	}
}
