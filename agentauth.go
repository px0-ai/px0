package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Who is allowed to sign a harness in, and px0 is not.
//
// Every harness px0 drives already knows how to authenticate itself, and the
// credential belongs to that tool: a token refreshed on its own schedule,
// stored in its own format, revocable from its own CLI. Reimplementing any of
// that would mean px0 holding a copy of a subscription session it cannot keep
// alive. So sign-in is delegated -- px0 runs the harness's real login command
// and shows the user what it printed -- and what px0 adds is the other half of
// the story: a key the user already has, for the harnesses that would rather be
// handed one than run an OAuth dance.

// authKind says how a harness is normally fed credentials.
type authKind string

const (
	// authOAuth means the harness is driven by a subscription login of its own.
	// A key is only injected when the user explicitly asks for one, because
	// quietly setting ANTHROPIC_API_KEY under a working Claude subscription
	// would silently start billing a different account.
	authOAuth authKind = "oauth"

	// authKey means the harness is built around a key it is handed, and has no
	// subscription login to fall back on. Anything px0 can resolve is injected.
	authKey authKind = "key"

	// authBoth means either path works and neither is surprising: the harness
	// reads a key if one is present and otherwise uses its own stored login.
	authBoth authKind = "both"
)

// authMode is the user's standing choice for one harness.
type authMode string

const (
	authModeAuto  authMode = "auto"  // follow the preset's preference
	authModeOAuth authMode = "oauth" // never inject a key; use the harness login
	authModeKey   authMode = "key"   // always inject whatever key resolves
)

// Auth status as the picker reports it. "unknown" is a real answer, not a
// gap: px0 does not read a harness's credential store, so where there is no
// reliable signal it says so rather than guessing and sending the user off to
// sign in again for a session that already exists.
const (
	authSignedIn  = "signed-in"
	authSignedOut = "signed-out"
	authKeySet    = "key"
	authLocal     = "local"
	authUnknown   = "unknown"
)

// authStatus is the browser-facing summary for one harness.
type authStatus struct {
	State    string   `json:"state"`
	Detail   string   `json:"detail,omitempty"`
	Kind     authKind `json:"kind"`
	Mode     authMode `json:"mode"`
	Login    string   `json:"login,omitempty"`     // the command that signs in, when there is one
	SignOut  string   `json:"signOut,omitempty"`   // the command that signs out, when there is one
	Keys     []string `json:"keys,omitempty"`        // providers whose keys this harness accepts
	FromEnv  []string `json:"keysFromEnv,omitempty"` // of those, the ones px0 would actually inject
}

// homePath resolves a path under the user's home directory, or returns "" when
// there is no home to look in. A probe with no home is a probe that cannot
// conclude, never one that concludes "signed out".
func homePath(rel string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, filepath.FromSlash(rel))
}

// anyFileExists reports the first of rels that exists under home, or "".
func anyFileExists(rels []string) string {
	for _, rel := range rels {
		if p := homePath(rel); p != "" {
			if _, err := os.Stat(p); err == nil {
				return rel
			}
		}
	}
	return ""
}

// authStatusFor works out where a harness stands, cheapest signal first: a key
// px0 can actually inject, then a credential file the harness is known to
// leave behind, then nothing.
func authStatusFor(p agentPreset, mode authMode) authStatus {
	st := authStatus{Kind: p.Auth, Mode: mode, Keys: p.KeyProviders}
	if p.LoginArgs != nil {
		st.Login = strings.Join(append(append([]string(nil), p.Args[0]), p.LoginArgs...), " ")
	}
	if p.LogoutArgs != nil {
		st.SignOut = strings.Join(append(append([]string(nil), p.Args[0]), p.LogoutArgs...), " ")
	}

	// Which of the harness's providers actually resolve to something today.
	var withKey []string
	for _, id := range p.KeyProviders {
		if c := resolveCredential(id); c.Key != "" {
			withKey = append(withKey, id)
		}
	}
	if willInjectKeys(p, mode) {
		st.FromEnv = withKey
	}

	// A local runtime needs nothing at all, which is worth saying plainly
	// rather than reporting as a missing credential.
	if len(p.KeyProviders) == 1 {
		if prov, ok := keyProviderByID(p.KeyProviders[0]); ok && prov.Local {
			st.State = authLocal
			st.Detail = "runs locally, no credential needed"
			return st
		}
	}

	if p.Auth == authKey && len(withKey) == 0 {
		st.State = authSignedOut
		st.Detail = "needs an API key; none is set"
		return st
	}

	// A credential file the harness is known to write on a successful login.
	// This is a presence check on purpose: reading and parsing somebody's
	// token store to report a status dot is not worth the blast radius.
	if hit := anyFileExists(p.CredFiles); hit != "" {
		st.State = authSignedIn
		st.Detail = "signed in (" + filepath.Base(hit) + ")"
		if len(withKey) > 0 && willInjectKeys(p, mode) {
			st.Detail += "; a key will also be injected"
		}
		return st
	}

	// A key only counts as an answer when this harness would actually be handed
	// one. In oauth mode the key is ignored, so reporting "API key" would put a
	// green tick next to a credential that is never used, and the run would then
	// fail on a login the user cannot see is missing.
	if len(withKey) > 0 && willInjectKeys(p, mode) {
		st.State = authKeySet
		st.Detail = "no saved login, but a key resolves"
		return st
	}

	if p.LoginArgs == nil {
		// No login subcommand to delegate to: these harnesses authenticate on
		// their first real prompt, so px0 has nothing it can check or run.
		st.State = authUnknown
		st.Detail = "signs in on first run; nothing for px0 to check"
		return st
	}

	st.State = authSignedOut
	st.Detail = "not signed in"
	return st
}

// willInjectKeys decides whether a run of this harness should carry a key.
//
// The default is deliberately conservative. For a subscription-driven harness
// nothing is injected, because the harness is already authenticated and a
// stray ANTHROPIC_API_KEY in the environment would quietly bill another
// account. The user has to opt in per harness.
func willInjectKeys(p agentPreset, mode authMode) bool {
	switch mode {
	case authModeKey:
		return true
	case authModeOAuth:
		return false
	default:
		return p.Auth == authKey || p.Auth == authBoth
	}
}

// authModesForPreset lists the choices a harness actually offers, so the UI
// does not present a "use my API key" toggle for a harness that has no
// subscription login to override in the first place.
func authModesForPreset(p agentPreset) []authMode {
	switch p.Auth {
	case authKey:
		return []authMode{authModeKey}
	case authBoth:
		return []authMode{authModeAuto, authModeKey, authModeOAuth}
	default:
		return []authMode{authModeAuto, authModeOAuth, authModeKey}
	}
}

func validAuthMode(m authMode, p agentPreset) bool {
	for _, cand := range authModesForPreset(p) {
		if cand == m {
			return true
		}
	}
	return false
}

// presetByName is defined in agent.go, next to the prompt-stdin helpers that
// resolve through it too. A name that is not a preset -- an arbitrary command
// template, whose display name is its binary -- simply yields false, which is
// the conservative answer everywhere below: no auth story to describe, and no
// assumption to make about a command px0 knows nothing about.

// authModeForSpec reads the remembered mode for a harness. An unset or invalid
// mode means "follow the preset", never an error: a stale settings file
// should leave the harness usable, not disabled.
func authModeForSpec(name string) authMode {
	raw := readSettings()
	m := authMode(strings.TrimSpace(raw.AuthModes[name]))
	if m == "" {
		return authModeAuto
	}
	if p, ok := presetByName(name); ok && !validAuthMode(m, p) {
		return authModeAuto
	}
	return m
}

// signInCommand is the argv that delegates sign-in to the harness itself. It is
// the real login command, not an approximation, so the flow a user goes through
// is the one that tool supports -- including any device code, browser handoff,
// or callback it wants.
func signInCommand(p agentPreset) ([]string, error) {
	if p.LoginArgs == nil {
		return nil, fmt.Errorf("%s has no login command; run it once in a terminal to sign in", p.Name)
	}
	bin, ok := lookPathIn(p.Args[0], lspBinDirs())
	if !ok {
		return nil, fmt.Errorf("%s is not installed", p.Args[0])
	}
	return append([]string{bin}, p.LoginArgs...), nil
}

func signOutCommand(p agentPreset) ([]string, error) {
	if p.LogoutArgs == nil {
		return nil, fmt.Errorf("%s has no logout command", p.Name)
	}
	bin, ok := lookPathIn(p.Args[0], lspBinDirs())
	if !ok {
		return nil, fmt.Errorf("%s is not installed", p.Args[0])
	}
	return append([]string{bin}, p.LogoutArgs...), nil
}

// SetAuthMode remembers how one harness should be credentialed, and is the only
// place that choice is persisted. A mode the harness does not offer is refused
// rather than stored: writing it anyway would leave the picker showing a choice
// that silently does nothing.
func (m *agentManager) SetAuthMode(harness string, mode authMode) error {
	p, ok := presetByName(harness)
	if !ok {
		return fmt.Errorf("unknown harness %q", harness)
	}
	if !validAuthMode(mode, p) {
		return fmt.Errorf("%s does not offer auth mode %q", harness, mode)
	}
	s := readSettings()
	modes := map[string]string{}
	for k, v := range s.AuthModes {
		modes[k] = v
	}
	if mode == authModeAuto {
		delete(modes, p.Name)
	} else {
		modes[p.Name] = string(mode)
	}
	// Only the auth modes are being changed here. The harness selection and the
	// per-harness models belong to the user and must survive this write.
	return writeSettings(settingsPatch{AuthModes: modes})
}

// AuthStatuses reports the credential state of every preset at once, so the
// picker can render the whole list in one pass instead of asking per row.
func (m *agentManager) AuthStatuses() map[string]authStatus {
	out := make(map[string]authStatus, len(agentPresets))
	for _, p := range agentPresets {
		out[p.Name] = authStatusFor(p, authModeForSpec(p.Name))
	}
	return out
}

// StartSignIn delegates sign-in to the harness's own login command and returns
// a job to poll, exactly like an edit. The user watches the real flow -- device
// code, browser handoff, callback -- and px0 only reports what the tool printed.
//
// Nothing here re-implements a token exchange, and px0 never ends up holding a
// session it would have to keep refreshed on somebody else's behalf.
func (m *agentManager) StartSignIn(name string) (*agentJob, error) {
	p, ok := presetByName(name)
	if !ok {
		return nil, fmt.Errorf("unknown harness %q", name)
	}
	argv, err := signInCommand(p)
	if err != nil {
		return nil, err
	}
	return m.startAuthJob(p.Name, "sign in", argv)
}

// StartSignOut is the counterpart, for the harnesses that have a logout.
func (m *agentManager) StartSignOut(name string) (*agentJob, error) {
	p, ok := presetByName(name)
	if !ok {
		return nil, fmt.Errorf("unknown harness %q", name)
	}
	argv, err := signOutCommand(p)
	if err != nil {
		return nil, err
	}
	return m.startAuthJob(p.Name, "sign out", argv)
}

// startAuthJob runs an auth command under the same job machinery an edit uses,
// so the browser polls one endpoint and renders one kind of output. A login
// touches no workspace file, so the job claims no file and reports no change.
func (m *agentManager) startAuthJob(name, label string, argv []string) (*agentJob, error) {
	ctx, cancel := context.WithCancel(context.Background())
	job := &agentJob{
		ID:       m.nextJobID(),
		Harness:  name,
		Path:     label,
		Lines:    "",
		Running:  true,
		Changed:  []string{},
		Tracked:  false,
		out:      &tailBuffer{max: agentLogBytes},
		stderr:   &tailBuffer{max: agentLogBytes},
		start:    time.Now(),
		cancel:   cancel,
	}

	m.mu.Lock()
	if m.jobs == nil {
		m.jobs = map[int64]*agentJob{}
	}
	m.jobs[job.ID] = job
	m.mu.Unlock()

	uiStatus("step", "agent", fmt.Sprintf("#%d %s · %s: %s", job.ID, name, label, strings.Join(argv, " ")), 0, os.Stdout)
	go m.run(ctx, cancel, job, argv, "")
	return m.Job(job.ID), nil
}

// handleAgentAuth reports every harness's credential state alongside the keys
// px0 can see. A GET is safe here precisely because nothing secret is in the
// response: a provider is described by whether a key resolves and where from,
// never by the key.
func (s *Server) handleAgentAuth(w http.ResponseWriter, r *http.Request) {
	if !s.agentOrFail(w) {
		return
	}
	writeJSON(w, map[string]any{
		"auth":        s.agent.AuthStatuses(),
		"credentials": credentialViews(),
		"credentialsPath": credentialsPath(),
		"settings":    settingsPath(),
	})
}

// handleAgentAuthMode records how one harness should be credentialed.
func (s *Server) handleAgentAuthMode(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	harness := r.URL.Query().Get("harness")
	mode := authMode(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = authModeAuto
	}
	if err := s.agent.SetAuthMode(harness, mode); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"auth": s.agent.AuthStatuses(),
	})
}

// handleAgentSignIn starts a delegated login. The returned job is polled
// through the same /api/agent/job an edit uses, so the user watches the
// harness's real sign-in flow rather than a spinner px0 invented.
func (s *Server) handleAgentSignIn(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	job, err := s.agent.StartSignIn(r.URL.Query().Get("harness"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, job)
}

func (s *Server) handleAgentSignOut(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	job, err := s.agent.StartSignOut(r.URL.Query().Get("harness"))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, job)
}

// credentialRequest is the body of a credential write. The key arrives in the
// body rather than the query string so it cannot end up in an access log.
type credentialRequest struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
}

// handleAgentCredential stores or clears one provider key. An empty key clears
// it, which is the delete: a DELETE would need relaxing localPost for no gain.
func (s *Server) handleAgentCredential(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	var req credentialRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil && err != io.EOF {
		fail(w, 400, "bad request body")
		return
	}
	if req.Provider == "" {
		req.Provider = r.URL.Query().Get("provider")
	}
	if err := setCredential(req.Provider, req.Key); err != nil {
		fail(w, 400, err.Error())
		return
	}
	// Drop the discovery cache: a newly resolvable key can change which models
	// a harness reports, and a stale answer would read as "not configured".
	invalidateDiscoveredModels()
	if s.agent != nil {
		writeJSON(w, map[string]any{
			"credentials": credentialViews(),
			"auth":        s.agent.AuthStatuses(),
		})
		return
	}
	writeJSON(w, map[string]any{"credentials": credentialViews()})
}
