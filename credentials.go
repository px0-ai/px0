package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Bring your own key. A harness already knows how to sign itself in -- that
// login belongs to the tool that owns the credential, and px0 does not
// reimplement any of it. What px0 adds is the other half: a key the user
// already has, for a harness that would rather be handed one than run an
// OAuth dance.
//
// Keys live beside the settings file, never in a working tree, in a file only
// the user can read. They are handed to a harness the way a shell would hand
// them over -- as an environment variable in the child process -- and are
// never appended to argv, never echoed into a job log, and never returned to
// the browser in full.

// keyProvider is a vendor a key can belong to, and the environment variable
// name its ecosystem already reads. Several providers accept more than one
// name, so a key someone exported the conventional way is picked up without
// being retyped.
type keyProvider struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	EnvVars []string `json:"envVars"`
	OAuth   bool     `json:"oauth"`   // a subscription login exists alongside the key
	Local   bool     `json:"local"`   // needs no credential at all (a local runtime)
}

// keyProviders is the set of vendors px0 can hold a key for. The environment
// variable names are the ones these CLIs already document, so a key that
// works in a terminal works here unchanged.
var keyProviders = []keyProvider{
	{ID: "anthropic", Label: "Anthropic", EnvVars: []string{"ANTHROPIC_API_KEY"}, OAuth: true},
	{ID: "openai", Label: "OpenAI", EnvVars: []string{"OPENAI_API_KEY"}, OAuth: true},
	{ID: "google", Label: "Google", EnvVars: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, OAuth: true},
	{ID: "github", Label: "GitHub", EnvVars: []string{"GITHUB_TOKEN", "GH_TOKEN"}, OAuth: true},
	{ID: "xai", Label: "xAI", EnvVars: []string{"XAI_API_KEY"}},
	{ID: "groq", Label: "Groq", EnvVars: []string{"GROQ_API_KEY"}},
	{ID: "openrouter", Label: "OpenRouter", EnvVars: []string{"OPENROUTER_API_KEY"}},
	{ID: "mistral", Label: "Mistral", EnvVars: []string{"MISTRAL_API_KEY"}},
	{ID: "deepseek", Label: "DeepSeek", EnvVars: []string{"DEEPSEEK_API_KEY"}},
	{ID: "ollama", Label: "Ollama (local)", Local: true},
}

func keyProviderByID(id string) (keyProvider, bool) {
	for _, p := range keyProviders {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return keyProvider{}, false
}

// primaryEnv is the variable a key is published under. The first name is the
// one that tool documents first, so it is the one worth setting.
func (p keyProvider) primaryEnv() string {
	if len(p.EnvVars) == 0 {
		return ""
	}
	return p.EnvVars[0]
}

var credentialsMu sync.Mutex

// credentialsPath mirrors settingsPath: XDG when set, otherwise ~/.px0.
func credentialsPath() string {
	if p := settingsPath(); p != "" {
		return filepath.Join(filepath.Dir(p), "credentials.json")
	}
	return ""
}

// readCredentials returns the stored keys by provider id. It never fails: a
// missing or corrupt file is the same as no keys, which is what a fresh
// install looks like anyway.
//
// Entries are returned exactly as stored, including ids px0 does not
// recognise. That is deliberate: a key written by an older build, or for a
// provider since renamed, is somebody's real credential, and dropping it here
// would destroy it silently the next time they saved an unrelated key. A
// provider px0 no longer offers is simply not surfaced in the UI.
func readCredentials() map[string]string {
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	return readCredentialsLocked()
}

func readCredentialsLocked() map[string]string {
	p := credentialsPath()
	if p == "" {
		return map[string]string{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]string{}
	}
	// An empty value is not a credential. Anything else is kept as-is.
	for id, key := range out {
		if strings.TrimSpace(key) == "" {
			delete(out, id)
		}
	}
	return out
}

// writeCredentials replaces the stored keys. The file is created with owner
// only permissions and written through a temporary file, so a crash mid-write
// cannot leave a half-written file, and a key is never briefly world-readable.
func writeCredentials(keys map[string]string) error {
	credentialsMu.Lock()
	defer credentialsMu.Unlock()
	return writeCredentialsLocked(keys)
}

func writeCredentialsLocked(keys map[string]string) error {
	p := credentialsPath()
	if p == "" {
		return errors.New("no home directory to save credentials in")
	}
	clean := make(map[string]string, len(keys))
	for id, key := range keys {
		if k := strings.TrimSpace(key); k != "" {
			clean[id] = k
		}
	}

	dir := filepath.Dir(p)
	created := false
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		created = true
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Only tighten the directory when px0 is the one that made it. An existing
	// directory is the user's, and silently changing its mode is a side effect
	// of saving a key that nobody asked for.
	if created {
		_ = os.Chmod(dir, 0o700) // best effort; Windows does not model this bit
	}

	if len(clean) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600) // best effort: Windows does not model this bit
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// setCredential stores one provider's key, or clears it when the key is empty.
//
// The whole read-modify-write happens under the lock. Two saves racing would
// otherwise both read the same map and the second write would drop the first
// one's key, which is a plausible thing to hit with a picker open in two tabs.
func setCredential(providerID, key string) error {
	if _, ok := keyProviderByID(providerID); !ok {
		return errors.New("unknown provider " + providerID)
	}
	credentialsMu.Lock()
	defer credentialsMu.Unlock()

	keys := readCredentialsLocked()
	if strings.TrimSpace(key) == "" {
		delete(keys, providerID)
	} else {
		keys[providerID] = key
	}
	return writeCredentialsLocked(keys)
}

// credentialState is the one place a key is looked up, so every caller -- a
// harness launch, the status probe, the picker -- agrees on where a key comes
// from and in what order.
type credentialState struct {
	Key    string
	Source string // "px0", "env", or "" when nothing resolves
	EnvVar string // the variable that carried it, for the environment injection
}

// resolveCredential prefers a key px0 is holding and falls back to the user's
// own environment, so exporting ANTHROPIC_API_KEY once keeps working with no
// setup at all. A local runtime resolves to a "key" that is really just a
// confirmation, since it never needed a credential.
func resolveCredential(providerID string) credentialState {
	p, ok := keyProviderByID(providerID)
	if !ok {
		return credentialState{}
	}
	if p.Local {
		return credentialState{Key: "", Source: "local"}
	}
	if k := strings.TrimSpace(readCredentials()[providerID]); k != "" {
		return credentialState{Key: k, Source: "px0", EnvVar: p.primaryEnv()}
	}
	for _, env := range p.EnvVars {
		if k := strings.TrimSpace(os.Getenv(env)); k != "" {
			return credentialState{Key: k, Source: "env", EnvVar: env}
		}
	}
	return credentialState{}
}

// harnessEnvironment builds the child process environment: the caller's own,
// plus one variable per provider this harness accepts a key for. Nothing is
// removed, so a key exported in the terminal still reaches the harness even
// when px0 holds nothing for that provider.
//
// A variable the caller has already set to something non-empty always wins.
// A variable that is merely present but empty does not: a shell that exports
// ANTHROPIC_API_KEY= , or a CI runner that exports every known name as empty,
// would otherwise suppress a key the user deliberately stored and expect to be
// used.
func harnessEnvironment(providers []string) []string {
	env := os.Environ()
	provided := map[string]bool{}
	for _, entry := range env {
		i := strings.IndexByte(entry, '=')
		if i <= 0 {
			continue
		}
		if entry[i+1:] != "" {
			provided[entry[:i]] = true
		}
	}
	for _, id := range providers {
		st := resolveCredential(id)
		if st.Key == "" || st.EnvVar == "" || provided[st.EnvVar] {
			continue
		}
		env = append(env, st.EnvVar+"="+st.Key)
		provided[st.EnvVar] = true
	}
	return env
}

// maskKey reduces a key to something safe to render: enough survives to tell
// two keys apart in a list, and not enough to be useful to anyone reading over
// a shoulder or looking at a screenshot.
//
// The length floor is the whole point. Revealing four characters from each end
// is only safe when there is a lot left hidden: applied to a 9-character key it
// would hand back 8 of the 9, which is not a mask at all. So a key too short to
// hide anything behind is shown as nothing but stars. Every key format px0
// accepts is comfortably past this floor.
func maskKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	const minMaskable = 24
	if len(key) < minMaskable {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

// credentialView is the browser-facing shape of one provider's credential.
// The key itself is never included: the UI needs to know whether one is set
// and which provider it belongs to, and neither needs the secret.
type credentialView struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	EnvVars []string `json:"envVars"`
	OAuth   bool     `json:"oauth"`
	Local   bool     `json:"local"`
	HasKey  bool     `json:"hasKey"`
	Source  string   `json:"source,omitempty"` // "px0" or "env"
	Masked  string   `json:"masked,omitempty"`
}

// credentialViews reports the state of every provider, sorted for a stable UI.
func credentialViews() []credentialView {
	out := make([]credentialView, 0, len(keyProviders))
	for _, p := range keyProviders {
		st := resolveCredential(p.ID)
		v := credentialView{
			ID:      p.ID,
			Label:   p.Label,
			EnvVars: p.EnvVars,
			OAuth:   p.OAuth,
			Local:   p.Local,
			Source:  st.Source,
			HasKey:  st.Key != "" || p.Local,
		}
		if st.Key != "" {
			v.Masked = maskKey(st.Key)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
