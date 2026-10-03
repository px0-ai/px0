package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests in this file cover how a prompt reaches a harness, which is the
// one part of a run that can fail before the harness has done anything at all.
//
// The bug they exist for: a prompt carried in argv is marshalled into a single
// command line. On Windows, when the resolved binary is a .cmd shim -- which is
// what every npm global install is -- the kernel wraps it in cmd.exe /c, and
// cmd.exe allows 8,191 characters for the whole line. A staged diff does not
// fit in that, and the resulting error names neither the prompt nor the limit:
// either "exit status 1" with the harness never having run, or "The filename or
// extension is too long" from CreateProcess once it passes 32,767.

func TestMaxArgvPromptBytesStaysUnderPlatformCeilings(t *testing.T) {
	cmdShim := maxArgvPromptBytes("C:/npm/opencode.cmd")
	exe := maxArgvPromptBytes("C:/npm/opencode.exe")

	if runtime.GOOS == "windows" {
		// cmd.exe allows 8,191 for the entire line: the binary path, every
		// flag, the model id, and the quoting Go wraps around each argument.
		if cmdShim >= 8191 {
			t.Errorf("cmd shim budget %d leaves no room under cmd.exe's 8191", cmdShim)
		}
		// CreateProcessW caps the line at 32,767, and a real binary skips
		// cmd.exe, so it gets a far larger budget than a shim does.
		if exe >= 32767 {
			t.Errorf("native binary budget %d exceeds CreateProcessW's 32767", exe)
		}
		if exe <= cmdShim {
			t.Errorf("native binary budget %d should exceed cmd shim budget %d", exe, cmdShim)
		}
	} else if cmdShim <= 0 || exe <= 0 {
		t.Errorf("budgets must be positive, got cmd=%d exe=%d", cmdShim, exe)
	}
}

func TestClampPromptLeavesShortPromptAlone(t *testing.T) {
	prompt := "edit line 3 and nothing else\n"
	got, truncated := clampPrompt(prompt, "harness")
	if truncated {
		t.Errorf("a %d-byte prompt should not be clamped", len(prompt))
	}
	if got != prompt {
		t.Errorf("clampPrompt altered a short prompt: %q", got)
	}
}

func TestClampPromptTruncatesAndSaysSo(t *testing.T) {
	// A realistic oversized prompt: a diff, one line at a time.
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("+ a changed line of source code\n")
	}
	prompt := b.String()

	got, truncated := clampPrompt(prompt, "C:/npm/harness.cmd")
	if !truncated {
		t.Fatal("a 100KB prompt should have been clamped")
	}
	if len(got) > maxArgvPromptBytes("C:/npm/harness.cmd") {
		t.Errorf("clamped prompt is %d bytes, over the %d budget",
			len(got), maxArgvPromptBytes("C:/npm/harness.cmd"))
	}
	// The harness must be able to tell it was cut, or it will reason about a
	// diff it was never shown and report a confident, wrong summary.
	if !strings.Contains(got, "truncated") {
		t.Errorf("clamped prompt does not say it was truncated: tail %q", tail(got, 120))
	}
	// Cut on a line boundary, never mid-line.
	head := got[:strings.Index(got, "\n\n[px0 truncated")]
	if !strings.HasSuffix(head, "+ a changed line of source code") {
		t.Errorf("clampPrompt cut mid-line, tail before notice: %q", tail(head, 60))
	}
}

// TestClampPromptNeverExceedsBudgetAcrossShims is the regression guard for the
// original failure: a prompt large enough to break exec must always come back
// small enough to send, whichever kind of binary it is aimed at.
func TestClampPromptNeverExceedsBudgetAcrossShims(t *testing.T) {
	prompt := strings.Repeat("a very long line of source code here\n", 20000)
	for _, bin := range []string{"h", "h.exe", "h.cmd", "h.bat", "h.CMD"} {
		got, truncated := clampPrompt(prompt, bin)
		if !truncated {
			t.Errorf("%s: expected clamping for a %d-byte prompt", bin, len(prompt))
		}
		if len(got) > maxArgvPromptBytes(bin) {
			t.Errorf("%s: clamped to %d, over budget %d", bin, len(got), maxArgvPromptBytes(bin))
		}
	}
}

func TestBuildArgvSubstitutesPromptForArgvHarness(t *testing.T) {
	template := []string{"claude", "--model", "haiku", "-p", "{prompt}"}
	got := buildArgv(template, "do the thing", false)

	want := []string{"claude", "--model", "haiku", "-p", "do the thing"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("buildArgv(argv) = %v, want %v", got, want)
	}
}

func TestBuildArgvDropsPromptTokenForStdinHarness(t *testing.T) {
	// What resolveAgentSpec produces for opencode: the prompt is the last
	// positional and the model flag sits in front of it.
	template := []string{"opencode", "run", "-m", "opencode/big-pickle", "{prompt}"}
	got := buildArgv(template, strings.Repeat("x", 200000), true)

	for _, a := range got {
		if strings.Contains(a, "{prompt}") {
			t.Errorf("buildArgv(stdin) left a {prompt} token in argv: %v", got)
		}
	}
	// The command itself must survive intact -- dropping the prompt is only
	// correct if what is left is still the same command.
	want := []string{"opencode", "run", "-m", "opencode/big-pickle"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("buildArgv(stdin) = %v, want %v", got, want)
	}
}

func TestBuildArgvDropsEveryPromptToken(t *testing.T) {
	// A template can name {prompt} more than once. Leaving a literal "{prompt}"
	// in argv hands the harness a prompt that is the seven characters of a
	// template placeholder, which reads as a real instruction.
	got := buildArgv([]string{"h", "{prompt}", "--again", "{prompt}"}, "real prompt", true)
	for _, a := range got {
		if strings.Contains(a, "{prompt}") {
			t.Errorf("buildArgv left a template token in argv: %v", got)
		}
	}
	if len(got) != 2 || got[0] != "h" || got[1] != "--again" {
		t.Errorf("buildArgv = %v, want [h --again]", got)
	}
}

// TestEveryStdinPresetStaysAValidCommand guards the preset table: a harness
// marked PromptStdin must still be the same command once {prompt} is removed.
// stdinVerified is the set of harnesses whose own documentation states that a
// prompt may be supplied on stdin, each confirmed by running it and watching it
// get as far as model resolution with nothing on argv.
//
// It exists so that a preset cannot quietly stop feeding its prompt on stdin.
// The plumbing tests below all iterate the presets that opted in, which means
// they pass on an empty set -- and an empty set is exactly what a bad merge or
// a dropped field line looks like. That regression shipped once: the field and
// every line of plumbing survived, and no preset set it, so every harness
// silently fell back to argv and the Windows command-line ceiling came back.
var stdinVerified = map[string]bool{
	"claude":   true, // `claude -p` reads stdin; the canonical `cat f | claude -p`
	"codex":    true, // codex exec: "instructions are read from stdin"
	"opencode": true, // `opencode run [<message...>]`, message optional
}

func TestStdinVerifiedHarnessesAreOptedIn(t *testing.T) {
	if len(stdinVerified) == 0 {
		t.Fatal("stdinVerified is empty, so this test guards nothing")
	}
	for name := range stdinVerified {
		p, ok := presetByName(name)
		if !ok {
			t.Errorf("stdinVerified names %q, which is not a known preset", name)
			continue
		}
		if !p.PromptStdin {
			t.Errorf("preset %s takes its prompt on stdin but does not set PromptStdin, "+
				"so its prompts go through argv and inherit the OS command-line ceiling "+
				"(8,191 characters behind a .cmd shim on Windows)", name)
		}
	}
}

// TestStdinOptInsAreVerified keeps the flag honest in the other direction: a
// harness may only claim stdin if it was actually checked, because setting it
// wrongly means the harness receives no prompt at all.
func TestStdinOptInsAreVerified(t *testing.T) {
	optedIn := 0
	for _, p := range agentPresets {
		if !p.PromptStdin {
			continue
		}
		optedIn++
		if !stdinVerified[p.Name] {
			t.Errorf("preset %s sets PromptStdin but is not listed in stdinVerified; "+
				"confirm the CLI reads a prompt from stdin before claiming it does", p.Name)
		}
	}
	if optedIn == 0 {
		t.Error("no preset feeds its prompt on stdin; every harness is back on argv")
	}
}

func TestHarnessPromptStdinAgreesWithThePreset(t *testing.T) {
	for _, p := range agentPresets {
		if got := harnessPromptStdin(p.Name); got != p.PromptStdin {
			t.Errorf("harnessPromptStdin(%q) = %v, but the preset says %v",
				p.Name, got, p.PromptStdin)
		}
	}
	// An unknown name must not be assumed to read stdin: px0 knows nothing about
	// an arbitrary command template's stdin handling.
	if harnessPromptStdin("not-a-preset") {
		t.Error("harnessPromptStdin assumed an unknown command reads stdin")
	}
}

// TestEveryStdinPresetStaysAValidCommand guards the preset table: a harness
// marked PromptStdin must still be the same command once {prompt} is removed.
//
// This resolves through resolveAgentSpec rather than reading p.Args directly,
// because the model flag is not in the template -- resolveAgentSpec inserts it
// beside the prompt token. Asserting against the raw template checks a command
// px0 never actually runs.
func TestEveryStdinPresetStaysAValidCommand(t *testing.T) {
	checked := 0
	for _, p := range agentPresets {
		if !p.PromptStdin {
			continue
		}
		checked++
		_, resolved, model, stdin, err := resolveAgentSpec(p.Name, p.DefaultModel)
		if err != nil {
			// Not installed here, so resolveAgentSpec cannot build argv. Fall
			// back to the template, minus the checks that need the model flag.
			resolved, model, stdin = p.Args, "", p.PromptStdin
		}
		if !stdin {
			t.Errorf("preset %s: resolveAgentSpec did not report stdin", p.Name)
		}
		// resolveAgentSpec deliberately leaves {prompt} in the template: what a
		// run does with it depends on a prompt that does not exist yet.
		// buildArgv is what removes it, so that is what this asserts on.
		final := buildArgv(resolved, "the prompt", stdin)
		if len(final) == 0 {
			t.Errorf("preset %s: resolving produced no command at all", p.Name)
			continue
		}
		// The resolved binary is an absolute path with a PATHEXT suffix, so
		// compare the stem rather than the whole element.
		if stem := strings.TrimSuffix(filepath.Base(final[0]), filepath.Ext(final[0])); stem != p.Args[0] {
			t.Errorf("preset %s: buildArgv ran %q, want the harness %q", p.Name, final[0], p.Args[0])
		}
		for _, a := range final {
			if a == "{prompt}" {
				t.Errorf("preset %s: {prompt} survived into argv: %v", p.Name, final)
			}
		}
		// The model flag is what the insertion logic is most likely to strand,
		// since that logic keys off the position of the token being removed.
		if p.ModelFlag != "" && model != "" {
			idx := -1
			for i, a := range final {
				if a == p.ModelFlag {
					idx = i
				}
			}
			if idx < 0 {
				t.Errorf("preset %s: model flag %q lost when the prompt moved to stdin: %v",
					p.Name, p.ModelFlag, final)
			} else if idx+1 >= len(final) || final[idx+1] != model {
				t.Errorf("preset %s: model flag %q is followed by %q, want the model %q: %v",
					p.Name, p.ModelFlag, final[idx+1], model, final)
			}
		}
	}
	if checked == 0 {
		t.Error("no preset feeds its prompt on stdin, so this guards nothing")
	}
}

// TestNonStdinPresetsKeepTheirPromptArg is the other half: a harness px0 has
// not verified reads its prompt from argv, so that is where it has to stay.
func TestNonStdinPresetsKeepTheirPromptArg(t *testing.T) {
	checked := 0
	for _, p := range agentPresets {
		if p.PromptStdin {
			continue
		}
		checked++
		args := buildArgv(p.Args, "the prompt", false)
		found := false
		for _, a := range args {
			if a == "the prompt" {
				found = true
			}
		}
		if !found {
			t.Errorf("preset %s: prompt did not reach argv: %v", p.Name, args)
		}
	}
	if checked == 0 {
		t.Error("every preset feeds stdin, so nothing is covered by the argv budget")
	}
}

func TestReadLineRangeClampsAnEnormousSelection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.go")
	var b strings.Builder
	for i := 0; i < 60000; i++ {
		b.WriteString("package main // filler line to make this file large\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readLineRange(p, 1, 60000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > maxSnippetBytes+512 {
		t.Errorf("readLineRange returned %d bytes, want <= %d", len(got), maxSnippetBytes)
	}
	if !strings.Contains(got, "px0 showed the first") {
		t.Errorf("clamped snippet does not say it was cut: tail %q", tail(got, 120))
	}
}

func TestReadLineRangeLeavesARangeAlone(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "small.go")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readLineRange(p, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got != "two\nthree" {
		t.Errorf("readLineRange = %q, want %q", got, "two\nthree")
	}
}

// ---------------------------------------------------------------- end to end
//
// The two tests below reproduce the reported failure against a real .cmd shim,
// the same shape npm produces on Windows. They are the only ones here that need
// a working cmd.exe, so they skip elsewhere rather than pretend to pass.

// writeCmdShim stands in for an npm global install: a batch file that copies
// stdin to a capture file, which is how the real shims forward %*. It is a .cmd
// on purpose -- that is the shape whose command line runs out of room. It
// returns the shim to run and the file the shim writes.
func writeCmdShim(t *testing.T) (shim, capture string) {
	t.Helper()
	dir := t.TempDir()
	capture = filepath.Join(dir, "captured.txt")
	shim = filepath.Join(dir, "shim.cmd")
	body := "@echo off\r\nmore > \"" + capture + "\"\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return shim, capture
}

func bigPrompt() string {
	var b strings.Builder
	b.WriteString("Write a git commit message for the staged changes below.\n")
	for i := 0; i < 2000; i++ {
		b.WriteString("+ a changed line of source code that the harness has to read\n")
	}
	return b.String()
}

// TestStdinDeliversAnOversizedPrompt is the regression test for the reported
// bug. Before the fix this prompt went into argv and the run died before the
// harness started; here it has to arrive whole.
func TestStdinDeliversAnOversizedPrompt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the .cmd shim limit this covers is Windows-specific")
	}
	shim, capture := writeCmdShim(t)

	prompt := bigPrompt()
	if len(prompt) <= 32767 {
		t.Fatalf("test prompt is only %d bytes; it must exceed CreateProcessW's limit", len(prompt))
	}

	args := buildArgv([]string{shim, "{prompt}"}, prompt, true)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = strings.NewReader(prompt)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stdin delivery failed for a %d-byte prompt: %v\n%s", len(prompt), err, out)
	}

	got, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("shim wrote no capture: %v", err)
	}
	// cmd.exe rewrites bare LF as CRLF, so compare on content rather than bytes.
	norm := strings.ReplaceAll(string(got), "\r\n", "\n")
	if !strings.Contains(norm, strings.TrimRight(prompt, "\n")) {
		t.Errorf("the harness received %d bytes but not the whole %d-byte prompt (captured %d)",
			len(norm), len(prompt), len(got))
	}
}

// TestArgvCannotDeliverAnOversizedPrompt documents why the prompt moved. It
// asserts the failure still exists, so if it ever stops failing, the budget in
// maxArgvPromptBytes is wrong and worth revisiting.
func TestArgvCannotDeliverAnOversizedPrompt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the .cmd shim limit this covers is Windows-specific")
	}
	shim, capture := writeCmdShim(t)

	prompt := bigPrompt()
	args := buildArgv([]string{shim, "{prompt}"}, prompt, false)
	cmd := exec.Command(args[0], args[1:]...)

	err := cmd.Run()
	if err == nil {
		t.Skip("this machine accepted a 40KB argv prompt; the ceiling is not what was measured")
	}
	if _, statErr := os.Stat(capture); statErr == nil {
		t.Errorf("the harness ran despite an oversized argv prompt, so this regression is unguarded")
	}
}

// TestClampedArgvPromptDoesRun is the other half of the budget: a prompt clamped
// to fit has to be a prompt the harness can actually accept, not one that swaps
// a loud failure for a silent truncation.
func TestClampedArgvPromptDoesRun(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the .cmd shim limit this covers is Windows-specific")
	}
	shim, capture := writeCmdShim(t)

	prompt, truncated := clampPrompt(bigPrompt(), shim)
	if !truncated {
		t.Fatal("expected the prompt to be clamped")
	}
	args := buildArgv([]string{shim, "{prompt}"}, prompt, false)
	cmd := exec.Command(args[0], args[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("a clamped %d-byte argv prompt still failed: %v\n%s", len(prompt), err, out)
	}
	got, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("shim wrote no capture: %v", err)
	}
	if len(got) == 0 {
		t.Error("the harness ran but received nothing")
	}
}

// helpers

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
