package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sign-in belongs to the harness, not to px0. These tests pin the two things
// that makes safe: a subscription-driven harness is never quietly handed a key,
// and a harness that cannot be checked says so rather than guessing.

// The default has to be conservative. A working Claude subscription silently
// billed to somebody else's API key is a failure nobody would notice until an
// invoice, so "auto" must inject nothing for a subscription harness.
func TestWillInjectKeysDefaultsConservative(t *testing.T) {
	cases := []struct {
		preset string
		mode   authMode
		want   bool
	}{
		{"claude", authModeAuto, false},
		{"codex", authModeAuto, false},
		{"copilot", authModeAuto, false},
		{"gemini", authModeAuto, false},
		{"droid", authModeAuto, false},
		// A tool with no login to fall back on has nothing else to use.
		{"crush", authModeAuto, true},
		{"aider", authModeAuto, true},
		{"goose", authModeAuto, true},
		{"opencode", authModeAuto, true},
		// Opting in, either way, is always honoured.
		{"claude", authModeKey, true},
		{"claude", authModeOAuth, false},
		{"crush", authModeKey, true},
		{"crush", authModeOAuth, false},
	}
	for _, c := range cases {
		p, ok := presetByName(c.preset)
		if !ok {
			t.Fatalf("no preset named %q", c.preset)
		}
		if got := willInjectKeys(p, c.mode); got != c.want {
			t.Errorf("willInjectKeys(%s, %s) = %v, want %v", c.preset, c.mode, got, c.want)
		}
	}
}

// A harness with no subscription login should not be offered the choice, and
// one that has a login should be offered exactly the two that mean something.
func TestAuthModesOnlyOfferWhatMatters(t *testing.T) {
	for _, p := range agentPresets {
		modes := authModesForPreset(p)
		if len(modes) == 0 {
			t.Errorf("%s offers no auth mode at all", p.Name)
		}
		seen := map[authMode]bool{}
		for _, m := range modes {
			if seen[m] {
				t.Errorf("%s repeats auth mode %q", p.Name, m)
			}
			seen[m] = true
			if !validAuthMode(m, p) {
				t.Errorf("%s offers %q but rejects it", p.Name, m)
			}
		}
		// Nothing is valid unless it is offered.
		for _, m := range []authMode{authModeAuto, authModeOAuth, authModeKey} {
			if validAuthMode(m, p) && !seen[m] {
				t.Errorf("%s accepts %q without offering it", p.Name, m)
			}
		}
	}
}

// Every preset must state its credential story. A harness left blank would
// report nothing useful and could be handed a key by accident.
func TestEveryPresetDeclaresItsAuthStory(t *testing.T) {
	for _, p := range agentPresets {
		switch p.Auth {
		case authOAuth, authKey, authBoth:
		default:
			t.Errorf("%s has an unknown auth kind %q", p.Name, p.Auth)
		}
		if p.Auth != authOAuth && len(p.KeyProviders) == 0 {
			t.Errorf("%s is key-driven but names no provider", p.Name)
		}
		if p.Auth == authKey && p.LoginArgs != nil {
			t.Logf("%s: key-driven with a login command", p.Name)
		}
		for _, id := range p.KeyProviders {
			if _, ok := keyProviderByID(id); !ok {
				t.Errorf("%s names unknown provider %q", p.Name, id)
			}
		}
	}
}

// Status must never claim a harness is signed in on the strength of a key the
// user merely exported, nor signed out on the strength of px0 failing to find a
// file it was never told about.
func TestAuthStatusReportsUnknownWhenUnknowable(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	p, ok := presetByName("qwen")
	if !ok {
		t.Fatal("no qwen preset")
	}
	// A harness with no login command and no credential file present has
	// nothing px0 can check.
	if p.LoginArgs != nil || len(p.CredFiles) > 0 {
		t.Skip("qwen gained a probe; this case no longer applies")
	}
	st := authStatusFor(p, authModeAuto)
	if st.State != authUnknown {
		t.Errorf("state = %q, want %q", st.State, authUnknown)
	}
	if st.Detail == "" {
		t.Error("an unknown state must still say why")
	}
}

func TestAuthStatusKeyDrivenWithoutKey(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	p, _ := presetByName("crush")
	st := authStatusFor(p, authModeAuto)
	if st.State != authSignedOut {
		t.Errorf("crush with no key: state = %q, want %q", st.State, authSignedOut)
	}
}

// A badge must never advertise a key that the chosen mode means will not be
// used. In oauth mode the key is ignored, so showing "API key" puts a green tick
// next to a credential that never reaches the harness, and the run then fails on
// a missing login the user was told was fine.
func TestAuthStatusDoesNotAdvertiseAnUnusedKey(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)
	if err := setCredential("anthropic", "sk-ant-present"); err != nil {
		t.Fatal(err)
	}

	// A probe preset with a key and nothing on disk to find.
	probe := agentPreset{
		Name:         "probekey",
		Args:         []string{"probekey", "-p", "{prompt}"},
		ModelFlag:    "-m",
		Auth:         authOAuth,
		LoginArgs:    []string{"login"},
		KeyProviders: []string{"anthropic"},
	}
	if st := authStatusFor(probe, authModeKey); st.State != authKeySet {
		t.Errorf("key mode with a key present: state = %q, want %q", st.State, authKeySet)
	}
	if st := authStatusFor(probe, authModeOAuth); st.State == authKeySet {
		t.Errorf("oauth mode advertised a key that will not be injected: %+v", st)
	}
}

func TestAuthStatusLocalRuntimeNeedsNothing(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	probe := agentPreset{
		Name:         "localprobe",
		Args:         []string{"localprobe", "{prompt}"},
		Auth:         authKey,
		KeyProviders: []string{"ollama"},
	}
	st := authStatusFor(probe, authModeAuto)
	if st.State != authLocal {
		t.Errorf("state = %q, want %q", st.State, authLocal)
	}
}

// The rendered command must be the harness's real one, not an approximation.
func TestSignInCommandUsesTheHarnessOwnSubcommand(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	p, _ := presetByName("copilot")
	argv, err := signInCommand(p)
	if err != nil {
		t.Skipf("copilot is not installed here: %v", err)
	}
	if len(argv) < 2 || argv[1] != "login" {
		t.Errorf("argv = %v, want the harness login subcommand", argv)
	}
}

// A harness that signs itself in on first run has no login command to delegate
// to, and saying so plainly beats a button that cannot work.
func TestSignInRefusesWhenThereIsNoLoginCommand(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	p, _ := presetByName("claude")
	if _, err := signInCommand(p); err == nil {
		t.Fatal("expected an error for a harness with no login command")
	} else if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("error should point the user at a terminal, got %q", err)
	}
}

// Recording an auth mode must not disturb anything else the user has chosen.
//
// This is the regression test for a bug that shipped in the first draft: the
// settings writer took the same struct for reading and writing, so a caller
// that only meant to record an auth mode passed a zero Agent and a nil Models,
// and the writer read those as "clear the selected harness" and "clear every
// per-harness model". Changing one dropdown silently discarded the others.
//
// It drives the public API rather than the writer, because the bug was never
// about the writer's signature: it was about what a user loses.
func TestSetAuthModePreservesHarnessAndModels(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	// A user who has a harness selected, with a model chosen for it, plus a
	// model choice for a different harness they are not currently using.
	//
	// Seeded by writing settings.json directly rather than through Select(),
	// which would need those two harnesses actually installed -- and which
	// would be testing the writer this test exists to hold to account.
	dir := isolateSettings(t)
	cfgDir := filepath.Join(dir, "px0")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"agent":"claude","models":{"claude":"sonnet","codex":"gpt-5-codex"}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readSettings()
	if before.Agent != "claude" || len(before.Models) != 2 {
		t.Fatalf("could not seed settings: %+v", before)
	}

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetAuthMode("claude", authModeKey); err != nil {
		t.Fatal(err)
	}

	after := readSettings()
	if after.Agent != before.Agent {
		t.Errorf("selected harness = %q, want %q: recording an auth mode cleared it", after.Agent, before.Agent)
	}
	if len(after.Models) != len(before.Models) {
		t.Fatalf("model choices = %v, want %v: recording an auth mode discarded them", after.Models, before.Models)
	}
	for k, v := range before.Models {
		if after.Models[k] != v {
			t.Errorf("model for %q = %q, want %q", k, after.Models[k], v)
		}
	}
	if after.AuthModes["claude"] != string(authModeKey) {
		t.Errorf("auth mode was not recorded: %v", after.AuthModes)
	}
}

func TestSetAuthModeRefusesUnknowableCombinations(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetAuthMode("not-a-harness", authModeKey); err == nil {
		t.Error("accepted an unknown harness")
	}
	// crush has no subscription login, so "oauth" is not a thing it can do.
	if err := m.SetAuthMode("crush", authModeOAuth); err == nil {
		t.Error("accepted an auth mode crush does not offer")
	}
	if err := m.SetAuthMode("claude", authModeKey); err != nil {
		t.Errorf("refused a mode claude does offer: %v", err)
	}
	// And it round-trips.
	if got := authModeForSpec("claude"); got != authModeKey {
		t.Errorf("mode did not persist: got %q, want %q", got, authModeKey)
	}
	// "auto" is the absence of a choice, so it is stored by removing the entry.
	if err := m.SetAuthMode("claude", authModeAuto); err != nil {
		t.Fatal(err)
	}
	if got := authModeForSpec("claude"); got != authModeAuto {
		t.Errorf("mode did not reset: got %q", got)
	}
}

// A harness remembered by a command template has no preset, so it must be
// reported as unknown rather than described with somebody else's story.
func TestUnknownHarnessHasNoAuthStory(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	if _, ok := presetByName("my-own-wrapper"); ok {
		t.Fatal("a command template must not resolve to a preset")
	}
	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Inheriting the caller's environment unchanged is what a template author
	// expects, and nil means exactly that to os/exec.
	if env := m.childEnv(); env != nil {
		t.Errorf("childEnv for an unknown harness = %v, want nil (inherit)", env)
	}
}

// The endpoints must never hand a secret to the browser, and must refuse a
// cross-origin write.
func TestAuthEndpointsNeverLeakKeys(t *testing.T) {
	isolateSettings(t)
	const secret = "sk-ant-endpoint-secret-9999"
	if err := setCredential("anthropic", secret); err != nil {
		t.Fatal(err)
	}

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(t.TempDir())
	ix.Build()
	s := NewServer(ix, nil)
	s.SetAgent(m)

	code, body := get(t, s, "/api/agent/auth")
	if code != http.StatusOK {
		t.Fatalf("GET /api/agent/auth = %d, want 200", code)
	}
	blob, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), secret) {
		t.Fatalf("/api/agent/auth leaked the key: %s", blob)
	}
	// It must still say the provider is configured, or the UI cannot render it.
	creds, _ := body["credentials"].([]any)
	found := false
	for _, c := range creds {
		if cv, ok := c.(map[string]any); ok && cv["id"] == "anthropic" && cv["hasKey"] == true {
			found = true
		}
	}
	if !found {
		t.Errorf("/api/agent/auth did not report the stored key as set: %s", blob)
	}
}

// localPost is the only thing standing between a website and a shell, so the
// credential write has to be behind it like every other mutation.
func TestCredentialEndpointRejectsCrossOrigin(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(t.TempDir())
	ix.Build()
	s := NewServer(ix, nil)
	s.SetAgent(m)

	req := httptest.NewRequest(http.MethodPost, "/api/agent/credential",
		strings.NewReader(`{"provider":"anthropic","key":"sk-evil"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin credential write = %d, want 403", rec.Code)
	}
	if got := readCredentials()["anthropic"]; got != "" {
		t.Fatalf("a refused write still stored the key: %q", got)
	}
}

// A stored key must survive a round trip through the endpoint, and clearing it
// through the same endpoint must remove it.
func TestCredentialEndpointStoresAndClears(t *testing.T) {
	isolateSettings(t)
	isolateCredentials(t)

	m, err := newAgentManager(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(t.TempDir())
	ix.Build()
	s := NewServer(ix, nil)
	s.SetAgent(m)

	if code, _ := agentPostJSON(t, s, "/api/agent/credential",
		map[string]string{"provider": "anthropic", "key": "sk-round-trip"}); code != http.StatusOK {
		t.Fatalf("store = %d, want 200", code)
	}
	if got := readCredentials()["anthropic"]; got != "sk-round-trip" {
		t.Fatalf("stored key = %q", got)
	}
	if code, _ := agentPostJSON(t, s, "/api/agent/credential",
		map[string]string{"provider": "anthropic", "key": ""}); code != http.StatusOK {
		t.Fatalf("clear = %d, want 200", code)
	}
	if got := readCredentials()["anthropic"]; got != "" {
		t.Fatalf("key survived a clear: %q", got)
	}
}
