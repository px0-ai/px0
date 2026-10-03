package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// The credential store keeps secrets in one place with one set of rules, so
// these tests are about the properties that must not regress: a key is never
// written world-readable, a key never comes back out in full, and a key px0
// does not hold is one somebody exported in their own environment.

func isolateCredentials(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Every provider's canonical variable is cleared, so a key in the real
	// environment cannot make a "no key is set" assertion pass by accident.
	for _, p := range keyProviders {
		for _, env := range p.EnvVars {
			t.Setenv(env, "")
		}
	}
	return filepath.Join(dir, "px0", "credentials.json")
}

func TestCredentialsRoundTripAndPermissions(t *testing.T) {
	path := isolateCredentials(t)

	if err := setCredential("anthropic", "sk-ant-secret-value"); err != nil {
		t.Fatalf("setCredential: %v", err)
	}
	if err := setCredential("openai", "sk-openai-secret"); err != nil {
		t.Fatalf("setCredential: %v", err)
	}

	got := readCredentials()
	if got["anthropic"] != "sk-ant-secret-value" || got["openai"] != "sk-openai-secret" {
		t.Fatalf("readCredentials = %v", got)
	}

	if runtime.GOOS != "windows" {
		if mode := fileMode(t, path); mode&0o077 != 0 {
			t.Errorf("credentials file mode = %o, want no group or other access", mode)
		}
	}
}

// fileMode reports a file's permission bits. On Windows these are emulated and
// the check is meaningless, so it is skipped rather than reported as a failure.
func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// Clearing the last key removes the file rather than leaving an empty one
// lying around, so "no keys stored" is unambiguous on disk.
func TestCredentialsRemoveWhenEmpty(t *testing.T) {
	path := isolateCredentials(t)

	if err := setCredential("anthropic", "sk-ant-x"); err != nil {
		t.Fatal(err)
	}
	if err := setCredential("anthropic", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("credentials file still present after clearing the last key: %v", err)
	}
}

func TestCredentialsRejectUnknownProvider(t *testing.T) {
	isolateCredentials(t)
	if err := setCredential("not-a-vendor", "x"); err == nil {
		t.Fatal("setCredential accepted an unknown provider")
	}
}

// A key written for a provider px0 no longer offers is still somebody's real
// credential. Filtering it out on read turned every later save of an unrelated
// key into a silent deletion of it, which is the worst possible failure for a
// secret: the user is never told, and the key is gone.
func TestCredentialsPreserveKeysForUnknownProviders(t *testing.T) {
	path := isolateCredentials(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(map[string]string{"anthropic": "sk-ant-x", "retired-vendor": "sk-old"})
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := readCredentials()["retired-vendor"]; got != "sk-old" {
		t.Errorf("readCredentials dropped a key for an unrecognised provider: %q", got)
	}

	// And it survives the read-modify-write of an unrelated save.
	if err := setCredential("openai", "sk-openai-new"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "sk-old") {
		t.Errorf("saving an unrelated key destroyed an unrecognised one: %s", after)
	}
}

// A key with no value is not a credential and must not linger.
func TestCredentialsDropEmptyValues(t *testing.T) {
	path := isolateCredentials(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"anthropic":"   "}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readCredentials(); len(got) != 0 {
		t.Errorf("readCredentials kept an empty value: %v", got)
	}
}

// px0 prefers a key it holds, then falls back to the caller's environment, so
// exporting a variable once keeps working with no setup at all.
func TestResolveCredentialPrefersPx0ThenEnv(t *testing.T) {
	isolateCredentials(t)

	if st := resolveCredential("anthropic"); st.Key != "" {
		t.Fatalf("expected no key, got %+v", st)
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-from-env")
	st := resolveCredential("anthropic")
	if st.Key != "sk-ant-from-env" || st.Source != "env" || st.EnvVar != "ANTHROPIC_API_KEY" {
		t.Fatalf("env fallback = %+v", st)
	}

	if err := setCredential("anthropic", "sk-ant-from-px0"); err != nil {
		t.Fatal(err)
	}
	st = resolveCredential("anthropic")
	if st.Key != "sk-ant-from-px0" || st.Source != "px0" {
		t.Fatalf("px0 key should win over env, got %+v", st)
	}
}

// A second name for the same vendor is honoured, because these CLIs document
// more than one spelling and a key exported the other way still works.
func TestResolveCredentialAcceptsAlternateEnvName(t *testing.T) {
	isolateCredentials(t)
	t.Setenv("GOOGLE_API_KEY", "goog-key")
	st := resolveCredential("google")
	if st.Key != "goog-key" || st.EnvVar != "GOOGLE_API_KEY" {
		t.Fatalf("alternate env name not honoured: %+v", st)
	}
}

func TestResolveCredentialLocalNeedsNothing(t *testing.T) {
	isolateCredentials(t)
	st := resolveCredential("ollama")
	if st.Source != "local" {
		t.Fatalf("ollama should resolve as local, got %+v", st)
	}
}

// The single most important property: a key must never reach the browser, the
// terminal, or a job log in full.
func TestCredentialViewsNeverExposeTheKey(t *testing.T) {
	isolateCredentials(t)
	const secret = "sk-ant-supersecret-value-1234"
	if err := setCredential("anthropic", secret); err != nil {
		t.Fatal(err)
	}

	for _, v := range credentialViews() {
		blob, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(blob), secret) {
			t.Fatalf("credential view leaked the key: %s", blob)
		}
	}
}

func TestMaskKey(t *testing.T) {
	const long = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"
	cases := []struct{ in, want string }{
		{"", ""},
		{"short", "*****"},
		// Long enough to hide something behind, so the ends survive and the
		// hidden run is everything in between.
		{long, "sk-a" + strings.Repeat("*", len(long)-8) + "wxyz"},
	}
	for _, c := range cases {
		if got := maskKey(c.in); got != c.want {
			t.Errorf("maskKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Revealing four characters from each end is only a mask when there is enough
// left hidden. Applied to a short key it hands back most of the secret: a
// 9-character key came back as 8 characters revealed. Everything too short to
// hide anything behind must come back as nothing but stars.
func TestMaskKeyRevealsNothingWhenTooShortToHide(t *testing.T) {
	for _, key := range []string{
		"123456789",
		"abcdefghi",
		"sk-1234567",
		"0123456789abcdef",
		"0123456789abcde",
		"0123456789abcd",
	} {
		masked := maskKey(key)
		for i := 0; i < len(masked); i++ {
			if masked[i] == '*' {
				continue
			}
			if i < len(key) && masked[i] == key[i] {
				t.Errorf("maskKey(%q) = %q leaked character %d of %d", key, masked, i, len(key))
			}
		}
	}
}

// Two saves racing must not lose one. setCredential is a read-modify-write, and
// with the lock held only inside the write the second writer's snapshot simply
// did not contain the first writer's key.
func TestConcurrentCredentialWritesDoNotLoseKeys(t *testing.T) {
	isolateCredentials(t)

	providers := []string{"anthropic", "openai", "google", "xai", "groq", "openrouter"}
	var wg sync.WaitGroup
	for i, id := range providers {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if err := setCredential(id, strings.Repeat("x", i+1)+"-key"); err != nil {
					t.Errorf("setCredential(%s): %v", id, err)
					return
				}
			}
		}(i, id)
	}
	wg.Wait()

	got := readCredentials()
	for _, id := range providers {
		if got[id] == "" {
			t.Errorf("provider %q lost its key to a concurrent write: %v", id, got)
		}
	}
}

// A key px0 holds is published under the variable the harness reads, and the
// caller's own environment survives untouched.
func TestHarnessEnvironmentInjectsAndPreserves(t *testing.T) {
	isolateCredentials(t)
	t.Setenv("PX0_TEST_MARKER", "kept")

	if err := setCredential("anthropic", "sk-ant-injected"); err != nil {
		t.Fatal(err)
	}
	env := harnessEnvironment([]string{"anthropic"})

	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "ANTHROPIC_API_KEY=sk-ant-injected") {
		t.Errorf("key was not injected: %v", env)
	}
	if !strings.Contains(joined, "PX0_TEST_MARKER=kept") {
		t.Errorf("caller environment was not preserved: %v", env)
	}
}

// An environment value the user exported themselves is never overwritten: they
// set it deliberately, and a stored key losing to it is the least surprising
// outcome.
func TestHarnessEnvironmentDoesNotOverrideExistingEnv(t *testing.T) {
	isolateCredentials(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-mine")

	if err := setCredential("anthropic", "sk-ant-px0"); err != nil {
		t.Fatal(err)
	}
	env := harnessEnvironment([]string{"anthropic"})
	if strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_API_KEY=sk-ant-px0") {
		t.Errorf("a stored key overrode the caller's own environment: %v", env)
	}
}
