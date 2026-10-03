package main

import (
	"testing"
	"time"
)

// A model id in a preset is a guess about somebody else's catalogue, and
// guesses expire. When this was written, eight of opencode's nine hardcoded
// models had been retired by the provider -- and one of them was the model that
// made a user's every run fail with "Model unavailable".
//
// Discovery is the authority. A preset that has discovery should therefore hold
// no catalogue of its own, and a preset that does have a static list should hold
// only ids the harness still reports.

func TestPromoteDefaultOnlyWhenDiscoveryFoundIt(t *testing.T) {
	list := []string{"a", "b", "c"}

	got := promoteDefault("b", list)
	if len(got) != 3 || got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Errorf("promoteDefault did not move a present default to the front: %v", got)
	}
}

// TestPromoteDefaultRefusesToInventADefault is the regression that matters. A
// default the harness never reported is a model that does not work, and putting
// it first makes it the top of the picker *and* the fallback for every retired
// model a user had remembered.
func TestPromoteDefaultRefusesToInventADefault(t *testing.T) {
	list := []string{"a", "b", "c"}
	got := promoteDefault("gone-from-the-provider", list)
	if len(got) != 3 {
		t.Fatalf("promoteDefault changed the list length: %v", got)
	}
	for _, m := range got {
		if m == "gone-from-the-provider" {
			t.Fatalf("promoteDefault injected a model discovery did not report: %v", got)
		}
	}
	// Discovery's own ordering is the best evidence there is; leave it alone.
	for i := range list {
		if got[i] != list[i] {
			t.Errorf("promoteDefault reordered a list with no default in it: %v", got)
			break
		}
	}
}

func TestPromoteDefaultEdgeCases(t *testing.T) {
	if got := promoteDefault("", []string{"a"}); len(got) != 1 || got[0] != "a" {
		t.Errorf("an empty default should leave the list alone, got %v", got)
	}
	if got := promoteDefault("a", nil); len(got) != 0 {
		t.Errorf("promoteDefault on a nil list = %v, want empty", got)
	}
	// A default that is the only entry must not be duplicated.
	got := promoteDefault("only", []string{"only"})
	if len(got) != 1 {
		t.Errorf("promoteDefault duplicated a single-entry default: %v", got)
	}
}

// TestDiscoveryBackedPresetsCarryNoCatalogue is the structural guard. For a
// preset with discovery, a static list is visible only in the window before
// discovery completes, or when discovery fails -- and in both cases it is a set
// of choices that may no longer exist. One default is the honest fallback.
func TestDiscoveryBackedPresetsCarryNoCatalogue(t *testing.T) {
	discovered := map[string]bool{
		"agy": true, "cursor-agent": true, "claude": true,
		"opencode": true, "crush": true,
	}
	for _, p := range agentPresets {
		if !discovered[p.Name] {
			continue
		}
		// claude is the exception, deliberately: its discovery shells out to
		// `-p /model` and needs a login, so an unauthenticated user would be
		// left with nothing. haiku/sonnet/opus are stable aliases rather than
		// versioned catalogue entries, which is why they have not rotted.
		if p.Name == "claude" {
			continue
		}
		if len(p.Models) != 0 {
			t.Errorf("preset %s has discovery but still hardcodes %d models (%v); "+
				"discovery replaces this list wholesale, so it can only rot",
				p.Name, len(p.Models), p.Models)
		}
	}
}

// runDiscovery runs the real discovery path for a preset and restores the
// package-level cache afterwards. The cache is shared by every test in the
// package, and a test that leaves a discovered list behind will silently change
// what usableModel concludes for the tests that run after it.
//
// available is false when the harness is not installed. That is reported rather
// than skipped, because a harness missing from the machine says nothing about
// the one that is present.
func runDiscovery(t *testing.T, p agentPreset) (models []string, complete, available bool) {
	t.Helper()
	bin, ok := lookPathIn(p.Args[0], lspBinDirs())
	if !ok {
		return nil, false, false
	}
	discoveredModelsMu.Lock()
	prevModels, hadModels := discoveredModels[p.Name]
	prevBusy := discoveringModels[p.Name]
	discoveredModelsMu.Unlock()
	t.Cleanup(func() {
		discoveredModelsMu.Lock()
		defer discoveredModelsMu.Unlock()
		if hadModels {
			discoveredModels[p.Name] = prevModels
		} else {
			delete(discoveredModels, p.Name)
		}
		discoveringModels[p.Name] = prevBusy
	})

	runModelDiscovery(p.Name, bin, nil)
	models, complete = discoveredModelsFor(p.Name)
	return models, complete, true
}

// TestStaticModelsSurviveDiscovery checks the presets that legitimately keep a
// static list against the live catalogue of whichever harnesses are installed.
// This is the test that would have caught the opencode rot on the day it was
// written.
//
// It asks discovery the same question discovery asks, by running the real
// discovery path rather than a second parser that could drift from the first.
// Harnesses that are not installed are skipped rather than assumed fine -- a skip
// is honest, a guess is not.
func TestStaticModelsSurviveDiscovery(t *testing.T) {
	checked, skipped := 0, 0
	for _, p := range agentPresets {
		if len(p.Models) == 0 {
			continue
		}
		models, complete, available := runDiscovery(t, p)
		if !available || !complete || len(models) == 0 {
			skipped++
			continue
		}
		checked++
		for _, want := range p.Models {
			if !contains(models, want) {
				t.Errorf("preset %s hardcodes the model %q but %s no longer reports it; "+
					"a dead id in a picker is a choice guaranteed to fail",
					p.Name, want, p.Args[0])
			}
		}
	}
	t.Logf("verified %d harnesses, skipped %d (not installed, or no listing command)", checked, skipped)
	if checked == 0 {
		t.Skip("no discovered harness is installed here, so nothing could be verified")
	}
}

// TestEveryPresetDefaultIsListed guards the fallback itself: a default that is
// not in the list the picker shows is a default the user cannot pick their way
// back to. A preset with no static list takes its default from the preset itself
// and lets discovery supply the rest, so there is nothing to be consistent with.
// TestDiscoveryActuallyCompletesForAnInstalledHarness catches the failure that
// has no error message: discovery that never finishes.
//
// The budget for one `harness models` call used to be five seconds, while a
// real CLI cold start is slower than that. Nothing failed, nothing was logged,
// and the harness simply reverted to its hardcoded list for the rest of the
// session -- a feature that looked like it worked and did not. This asserts
// that a harness px0 can actually list does produce a list.
func TestDiscoveryActuallyCompletesForAnInstalledHarness(t *testing.T) {
	ran := 0
	for _, name := range []string{"claude", "opencode", "cursor-agent", "agy"} {
		p, ok := presetByName(name)
		if !ok {
			continue
		}
		models, complete, available := runDiscovery(t, p)
		if !available {
			continue
		}
		ran++
		if !complete {
			t.Errorf("discovery for %s reported incomplete after running", name)
			continue
		}
		if len(models) == 0 {
			t.Errorf("discovery for %s finished but found no models; it will fall back "+
				"to a hardcoded list and never learn about anything new", name)
			continue
		}
		t.Logf("%s: discovered %d models, default promoted=%v", name, len(models), models[0] == p.DefaultModel)
	}
	if ran == 0 {
		t.Skip("no discoverable harness is installed here")
	}
}

func TestModelDiscoveryTimeoutIsNotShorterThanARealColdStart(t *testing.T) {
	// Not a tautology: five seconds was chosen as "generous" and was shorter
	// than `claude -p /model` takes. The bound is a guess about somebody else's
	// startup cost, so it is worth an assertion that it was not tightened back
	// to a value that silently disables discovery.
	if modelDiscoveryTimeout < 10*time.Second {
		t.Errorf("modelDiscoveryTimeout = %s; a real CLI cold start is around 7s, "+
			"and a budget under that turns discovery into a silent no-op",
			modelDiscoveryTimeout)
	}
}

func TestEveryPresetDefaultIsListed(t *testing.T) {
	for _, p := range agentPresets {
		if p.DefaultModel == "" || len(p.Models) == 0 {
			continue
		}
		if !contains(p.Models, p.DefaultModel) {
			t.Errorf("preset %s defaults to %q but does not list it in its models %v",
				p.Name, p.DefaultModel, p.Models)
		}
	}
}

// TestDefaultModelForReadsThePreset is what keeps the discovery path from
// drifting: it takes the default from the preset rather than repeating the id,
// so there is one place to change when a default does.
func TestDefaultModelForReadsThePreset(t *testing.T) {
	for _, p := range agentPresets {
		if got := defaultModelFor(p.Name); got != p.DefaultModel {
			t.Errorf("defaultModelFor(%q) = %q, want the preset's %q", p.Name, got, p.DefaultModel)
		}
	}
	if got := defaultModelFor("not-a-preset"); got != "" {
		t.Errorf("defaultModelFor on an unknown name = %q, want empty", got)
	}
}
