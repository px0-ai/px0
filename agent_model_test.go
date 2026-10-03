package main

import (
	"testing"
)

// A model id is a bare string in a settings file, and providers retire them.
// Nothing revalidating it is why "Model unavailable" came back on every single
// run, forever, with no way for px0 to explain it.

// seedDiscovered pretends discovery for a harness has finished with the given
// models. The cache is package-level and discovery is asynchronous, so a test
// that does not seed it is testing against whatever the static preset lists.
func seedDiscovered(t *testing.T, name string, models []string) {
	t.Helper()
	discoveredModelsMu.Lock()
	prevModels, hadModels := discoveredModels[name]
	prevBusy := discoveringModels[name]
	discoveredModels[name] = models
	discoveringModels[name] = false
	discoveredModelsMu.Unlock()
	t.Cleanup(func() {
		discoveredModelsMu.Lock()
		defer discoveredModelsMu.Unlock()
		if hadModels {
			discoveredModels[name] = prevModels
		} else {
			delete(discoveredModels, name)
		}
		discoveringModels[name] = prevBusy
	})
}

// clearDiscovered puts a harness back into the "discovery has not finished"
// state, so a test does not inherit another test's discovery result.
func clearDiscovered(t *testing.T, name string) {
	t.Helper()
	discoveredModelsMu.Lock()
	prev, had := discoveredModels[name]
	prevBusy := discoveringModels[name]
	delete(discoveredModels, name)
	discoveringModels[name] = true // in flight: not complete
	discoveredModelsMu.Unlock()
	t.Cleanup(func() {
		discoveredModelsMu.Lock()
		defer discoveredModelsMu.Unlock()
		if had {
			discoveredModels[name] = prev
		} else {
			delete(discoveredModels, name)
		}
		discoveringModels[name] = prevBusy
	})
}

func TestUsableModelRejectsARetiredModel(t *testing.T) {
	seedDiscovered(t, "opencode", []string{"opencode/big-pickle", "opencode/gpt-5-nano"})

	if ok, why := usableModel("opencode", "opencode/big-pickle"); !ok {
		t.Errorf("a model the harness does offer was rejected: %s", why)
	}
	ok, why := usableModel("opencode", "opencode/minimax-m2.5-free")
	if ok {
		t.Fatal("a retired model was accepted; every run would fail with Model unavailable")
	}
	// The message has to name the model, or the user cannot connect it to the
	// choice they made.
	if why == "" {
		t.Error("rejection gave no reason")
	}
}

func TestUsableModelDefersWhileDiscoveryIsRunning(t *testing.T) {
	// Discovery has not finished for this harness, so the only list is the
	// preset's. The cache is package-level and several other tests populate it,
	// so "has not finished" has to be established rather than assumed -- without
	// this the test passes alone and fails in a full run.
	clearDiscovered(t, "codex")

	// Overriding a deliberate choice on incomplete information is worse than
	// waiting, because a provider's newest models are never in the static list.
	ok, why := usableModel("codex", "some-model-released-yesterday")
	if !ok {
		t.Errorf("an unlisted model was rejected before discovery finished: %s", why)
	}
}

func TestUsableModelLeavesCommandTemplatesAlone(t *testing.T) {
	// A template is not a preset, so px0 has no list to check against. Inventing
	// a rule here would break every harness px0 does not ship with.
	ok, _ := usableModel("harness.sh", "whatever-i-want")
	if !ok {
		t.Error("a command template's model was rejected; px0 cannot know what it supports")
	}
	ok, _ = usableModel("harness.sh", "")
	if !ok {
		t.Error("an empty model should always be usable")
	}
}

// TestUsableModelDoesNotRetireOnAGuess is the over-reach guard. For a harness
// px0 cannot ask, the only list is the one hand-written in the preset, and that
// list goes stale in one direction only: it gains new ids nobody wrote down. A
// remembered model missing from it is not evidence the model is gone, and
// switching away from it would be px0 overruling the user on the strength of a
// guess.
func TestUsableModelDoesNotRetireOnAGuess(t *testing.T) {
	// codex has no discovery case, so its cached "answer" is just its static
	// list. Simulate that having happened.
	seedDiscovered(t, "codex", []string{"gpt-5-codex", "gpt-5-mini", "gpt-5.1-codex"})

	ok, why := usableModel("codex", "gpt-6-turbo")
	if !ok {
		t.Errorf("a model absent from a hand-written list was retired: %s", why)
	}
	// A genuinely discoverable harness is the opposite case and must still prune.
	seedDiscovered(t, "opencode", []string{"opencode/big-pickle"})
	if ok, _ := usableModel("opencode", "opencode/some-retired-model"); ok {
		t.Error("a discoverable harness did not prune a model it does not offer")
	}
}

func TestResolveAgentSpecFallsBackOffARetiredModel(t *testing.T) {
	seedDiscovered(t, "opencode", []string{"opencode/big-pickle", "opencode/gpt-5-nano"})
	skipIfHarnessMissing(t, "opencode")

	name, args, model, _, err := resolveAgentSpec("opencode", "opencode/minimax-m2.5-free")
	if err != nil {
		t.Skipf("opencode not usable in this environment: %v", err)
	}
	if name != "opencode" {
		t.Errorf("name = %q, want opencode", name)
	}
	if model != "opencode/big-pickle" {
		t.Errorf("model = %q, want the preset default opencode/big-pickle", model)
	}
	// Falling back in the returned model is not enough: the retired id must not
	// survive anywhere in the argv either, or the harness is still told to use it.
	for _, a := range args {
		if a == "opencode/minimax-m2.5-free" {
			t.Errorf("the retired model is still in argv: %v", args)
		}
	}
}

func TestResolveAgentSpecKeepsAModelThatStillExists(t *testing.T) {
	seedDiscovered(t, "opencode", []string{"opencode/big-pickle", "opencode/gpt-5-nano"})
	skipIfHarnessMissing(t, "opencode")

	_, _, model, _, err := resolveAgentSpec("opencode", "opencode/gpt-5-nano")
	if err != nil {
		t.Skipf("opencode not usable in this environment: %v", err)
	}
	if model != "opencode/gpt-5-nano" {
		t.Errorf("model = %q, want the requested opencode/gpt-5-nano", model)
	}
}

func TestDetectDoesNotOfferARetiredModel(t *testing.T) {
	seedDiscovered(t, "opencode", []string{"opencode/big-pickle", "opencode/gpt-5-nano"})

	dir := t.TempDir()
	isolateSettings(t)
	m, err := newAgentManager(dir, "opencode", nil)
	if err != nil {
		t.Skipf("opencode not usable in this environment: %v", err)
	}
	// Pretend the settings remembered a model the provider has since dropped.
	m.models["opencode"] = "opencode/minimax-m2.5-free"

	for _, h := range m.Detect() {
		if h.Name != "opencode" {
			continue
		}
		if h.Model == "opencode/minimax-m2.5-free" {
			t.Errorf("Detect still offers the retired model as the current one: %q", h.Model)
		}
		if h.Model != "opencode/big-pickle" {
			t.Errorf("Detect model = %q, want the default opencode/big-pickle", h.Model)
		}
		return
	}
	t.Skip("opencode was not reported as installed")
}

// ---------------------------------------------------------------- repeats

func TestRepeatGuardAllowsTheFirstTwoAndRefusesTheThird(t *testing.T) {
	m := &agentManager{models: map[string]string{}}

	// Nothing has failed yet.
	if _, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "big-pickle"); blocked {
		t.Fatal("a fresh manager refused a dispatch")
	}

	m.recordFailure(agentKindEdit, "opencode", "big-pickle", "Model unavailable: x")

	// One failure is not a pattern; the user may simply have tried again.
	if n, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "big-pickle"); blocked {
		t.Errorf("a single failure already blocked the next dispatch (n=%d)", n)
	}

	m.recordFailure(agentKindEdit, "opencode", "big-pickle", "Model unavailable: x")

	n, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "big-pickle")
	if !blocked {
		t.Error("two identical failures in a row did not block the third dispatch")
	}
	if n != 2 {
		t.Errorf("block reported n = %d, want 2", n)
	}
}

func TestRepeatGuardAllowsADifferentHarnessOrModel(t *testing.T) {
	m := &agentManager{models: map[string]string{}}
	m.recordFailure(agentKindEdit, "opencode", "bad-model", "exit status 1")
	m.recordFailure(agentKindEdit, "opencode", "bad-model", "exit status 1")

	if _, blocked := m.repeatGuardLocked(agentKindEdit, "claude", "bad-model"); blocked {
		t.Error("switching harness was treated as a repeat")
	}
	if _, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "good-model"); blocked {
		t.Error("switching model was treated as a repeat")
	}
	if _, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "bad-model"); !blocked {
		t.Error("the unchanged dispatch should still be refused")
	}
}

func TestRecordFailureResetsOnADifferentError(t *testing.T) {
	m := &agentManager{models: map[string]string{}}
	m.recordFailure(agentKindEdit, "opencode", "m", "exit status 1")
	m.recordFailure(agentKindEdit, "opencode", "m", "exit status 1")
	m.recordFailure(agentKindEdit, "opencode", "m", "fork/exec: too long")

	// A different error is a different problem, so the count starts over rather
	// than blocking a dispatch that has never been tried.
	if n, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "m"); blocked {
		t.Errorf("a new error was folded into the old count (n=%d)", n)
	}
}

func TestASuccessfulRunClearsTheRepeatGuard(t *testing.T) {
	m := &agentManager{models: map[string]string{}}
	m.recordFailure(agentKindEdit, "opencode", "m", "exit status 1")
	m.recordFailure(agentKindEdit, "opencode", "m", "exit status 1")
	if _, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "m"); !blocked {
		t.Fatal("setup: expected the guard to be armed")
	}

	m.clearFailure()
	if _, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "m"); blocked {
		t.Error("a success did not clear the guard; the next legitimate attempt is refused")
	}
}

// TestRepeatGuardDoesNotConfuseAnEditWithAPrompt is why the kind is in the
// signature at all. The same harness on the same model can fail for one request
// and be fine for another -- an edit carrying a huge diff is not the same
// request as a commit message, and neither is the other's failure.
func TestRepeatGuardDoesNotConfuseAnEditWithAPrompt(t *testing.T) {
	m := &agentManager{models: map[string]string{}}
	m.recordFailure(agentKindEdit, "opencode", "m", "exit status 1")
	m.recordFailure(agentKindEdit, "opencode", "m", "exit status 1")

	if _, blocked := m.repeatGuardLocked(agentKindPrompt, "opencode", "m"); blocked {
		t.Error("two failed edits refused an unrelated prompt dispatch")
	}
	if _, blocked := m.repeatGuardLocked(agentKindEdit, "opencode", "m"); !blocked {
		t.Error("the third identical edit should still be refused")
	}
}

func TestFailureSignatureSeparatesHarnessAndModel(t *testing.T) {
	if failureSignature(agentKindEdit, "a", "b", "c") == failureSignature(agentKindEdit, "a", "x", "c") {
		t.Error("changing the model did not change the signature")
	}
	if failureSignature(agentKindEdit, "a", "b", "c") == failureSignature(agentKindEdit, "z", "b", "c") {
		t.Error("changing the harness did not change the signature")
	}
	if failureSignature(agentKindEdit, "a", "b", "c") != failureSignature(agentKindEdit, "a", "b", "c") {
		t.Error("the same failure produced different signatures")
	}
	if failureSignature(agentKindEdit, "a", "b", "c") == failureSignature(agentKindPrompt, "a", "b", "c") {
		t.Error("changing the kind of dispatch did not change the signature")
	}
}

func skipIfHarnessMissing(t *testing.T, name string) {
	t.Helper()
	for _, p := range agentPresets {
		if p.Name == name {
			return
		}
	}
	t.Skipf("no preset named %q", name)
}
