package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Editing through a coding harness. px0 never authors a change itself: it
// composes an instruction anchored to a line range, hands it to a harness
// already installed on this machine, and reloads whatever moved once that
// harness exits. The harness edits; px0 stays the reader that knows exactly
// when to look again.
//
// Harnesses are discovered the same way language servers are, and the one to
// use is chosen in the UI. Discovery alone never enables editing: running a
// general-purpose agent over a workspace is a decision the user makes once,
// and it is remembered in the settings file rather than a flag.

const (
	agentTimeout  = 10 * time.Minute
	agentLogBytes = 32 << 10

	// maxSnippetBytes bounds the quoted source handed to a harness for an
	// inline or batch edit. It is a backstop, not the primary defence: a
	// harness that takes its prompt on stdin has no ceiling at all, and one
	// that does not is clamped separately by maxArgvPromptBytes. This bounds
	// the case where a harness reads stdin but a user's own token window does
	// not, and the case of a selection spanning a generated file.
	maxSnippetBytes = 48 << 10

	// modelDiscoveryTimeout bounds one `harness models` call.
	//
	// This used to be five seconds, which is shorter than a real CLI cold
	// start: `claude -p /model` takes about seven on a warm machine. So claude's
	// discovery never once succeeded, and px0 silently served its hardcoded
	// three-model list forever, hiding models the user actually had.
	//
	// A generous bound is safe because discovery runs on its own goroutine -- it
	// never delays startup or the first Detect() call, it only decides when the
	// cache fills. Harnesses are also invoked with an empty stdin, so a CLI that
	// would otherwise sit waiting for input fails instead of parking here.
	modelDiscoveryTimeout = 20 * time.Second
)

// agentPreset is a harness px0 knows and the argv that runs it headless. Each
// of these starts an interactive session by default and would sit forever
// waiting for approval, so every preset carries the flag that turns that off
// and the one that lets it apply edits without asking.
type agentPreset struct {
	Name         string
	Args         []string
	ModelFlag    string
	DefaultModel string
	Models       []string

	// PromptStdin reports that this harness takes its instruction on stdin
	// rather than in argv. Every harness verified so far accepts a prompt as an
	// argument in its headless mode, so this stays false throughout; it exists
	// so the argv budget in maxArgvPromptBytes can be lifted for a harness that
	// genuinely reads stdin, rather than being paid by every tool.
	PromptStdin bool

	// Auth is how this harness is normally credentialed.
	Auth authKind
	// LoginArgs and LogoutArgs are the harness's own subcommands. They are
	// delegated verbatim, never reimplemented, and a nil value means the
	// harness has no such command (it signs in on first use instead).
	LoginArgs  []string
	LogoutArgs []string
	// CredFiles are paths, relative to the home directory, whose presence means
	// a login succeeded. They are only ever stat-ed, never opened: reading
	// somebody's token store to draw a status dot is not worth the blast radius.
	CredFiles []string
	// KeyProviders are the vendor ids whose keys this harness accepts as
	// environment variables.
	KeyProviders []string
}

// The order here is the order the picker shows. Harnesses that sign in with a
// subscription come first, because for most people that is the path they
// already have working; the bring-your-own-key tools follow.
//
// Every argv and login command below was read off that tool's own --help
// rather than assumed. A harness that changes its flags is then a one-line fix
// by the user through a command template, not a px0 release that has to wait on
// a known-good version of somebody else's CLI.
var agentPresets = []agentPreset{
	{
		Name:         "claude",
		Args:         []string{"claude", "--permission-mode", "acceptEdits", "-p", "{prompt}"},
		ModelFlag:    "--model",
		DefaultModel: "haiku",
		Models:       []string{"haiku", "sonnet", "opus"},
		PromptStdin:  true,
		Auth:         authOAuth,
		CredFiles:    []string{".claude/.credentials.json"},
		KeyProviders: []string{"anthropic"},
	},
	{
		Name:         "codex",
		Args:         []string{"codex", "exec", "--ask-for-approval", "never", "{prompt}"},
		ModelFlag:    "-m",
		DefaultModel: "gpt-5-codex",
		Models: []string{
			"gpt-5-codex",
			"gpt-5-mini",
			"gpt-5.1-codex",
			"gpt-5.1-codex-max",
			"gpt-5.1-codex-mini",
			"gpt-5.2-codex",
			"gpt-4.1",
			"o3-mini",
			"o1",
		},
		PromptStdin: true,
		Auth:        authOAuth,
		LoginArgs:   []string{"login"},
		LogoutArgs:  []string{"logout"},
		CredFiles:   []string{".codex/auth.json"},
		KeyProviders: []string{"openai"},
	},
	{
		Name:      "copilot",
		Args:      []string{"copilot", "--allow-all-tools", "--no-ask-user", "-p", "{prompt}"},
		ModelFlag: "--model",
		// Non-interactive mode is refused outright unless tools are allowed to
		// run unattended, and ask_user would otherwise block on a human.
		DefaultModel: "claude-sonnet-4.6",
		Models: []string{
			"claude-sonnet-4.6",
			"claude-sonnet-4.5",
			"claude-haiku-4.5",
			"claude-opus-4.6",
			"claude-opus-4.6-fast",
			"claude-opus-4.5",
			"claude-sonnet-4",
			"gemini-3-pro-preview",
			"gpt-5.3-codex",
			"gpt-5.2-codex",
			"gpt-5.2",
			"gpt-5.1-codex-max",
			"gpt-5.1-codex",
			"gpt-5.1",
			"gpt-5.1-codex-mini",
			"gpt-5-mini",
			"gpt-4.1",
		},
		Auth:         authOAuth,
		LoginArgs:    []string{"login"},
		CredFiles:    []string{".copilot/config.json"},
		KeyProviders: []string{"github"},
	},
	{
		Name:         "gemini",
		Args:         []string{"gemini", "--approval-mode", "auto_edit", "-p", "{prompt}"},
		ModelFlag:    "-m",
		DefaultModel: "gemini-2.5-flash-lite",
		Models:       []string{"gemini-2.5-flash-lite", "gemini-2.5-flash", "gemini-2.5-pro"},
		Auth:         authOAuth,
		CredFiles:    []string{".gemini/oauth_creds.json"},
		KeyProviders: []string{"google"},
	},
	{
		Name:      "qwen",
		Args:      []string{"qwen", "-p", "{prompt}"},
		ModelFlag: "-m",
		// qwen dropped its `auth` subcommand: it signs in on first run, so
		// there is no login command for px0 to delegate to here.
		DefaultModel: "gemini-2.5-flash",
		Models:       []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-3-pro-preview"},
		Auth:         authOAuth,
		CredFiles:    []string{".qwen/oauth_creds.json"},
		KeyProviders: []string{"google"},
	},
	{
		Name:         "droid",
		Args:         []string{"droid", "exec", "--auto", "high", "{prompt}"},
		ModelFlag:    "-m",
		DefaultModel: "claude-opus-4-8",
		Models:       []string{"claude-opus-4-8", "claude-sonnet-4-6", "gpt-5.2-codex"},
		Auth:         authOAuth,
		CredFiles:    []string{".factory/auth.v2.file"},
		KeyProviders: []string{"anthropic", "openai"},
	},
	{
		Name:         "cursor-agent",
		Args:         []string{"cursor-agent", "--force", "-p", "{prompt}"},
		ModelFlag:    "--model",
		DefaultModel: "gemini-3.6-flash-minimal",
		// No static list, for the same reason as agy: `cursor-agent
		// --list-models` is the authority and replaces this wholesale.
		Auth:      authOAuth,
		CredFiles: []string{".cursor/cli-config.json", ".cursor/mcp.json"},
	},
	{
		Name:         "agy",
		Args:         []string{"agy", "--dangerously-skip-permissions", "--mode", "accept-edits", "-p", "{prompt}"},
		ModelFlag:    "--model",
		DefaultModel: "gemini-3.6-flash-low",
		// No static list on purpose. `agy models` is the authority and replaces
		// this wholesale a second after startup, so a hardcoded list is visible
		// only in the window before that -- and there only as choices that may
		// no longer exist. One known-good default beats a catalogue of guesses.
		// TestStaticModelsSurviveDiscovery is what keeps that honest.
		Auth:      authOAuth,
		CredFiles: []string{".agy/oauth_creds.json", ".agy/credentials.json"},
	},
	{
		Name:         "opencode",
		Args:         []string{"opencode", "run", "{prompt}"},
		ModelFlag:    "-m",
		DefaultModel: "opencode/big-pickle",
		// No static list. `opencode models` returns several hundred models and
		// replaces this wholesale, so a hand-maintained list here is pure rot:
		// when this was last written, eight of its nine entries had already been
		// retired by the provider, and one of them was the model that made a
		// user's runs fail. Discovery is the only list that stays true.
		PromptStdin: true,
		Auth:        authBoth,
		LoginArgs:   []string{"auth", "login"},
		CredFiles:   []string{".local/share/opencode/auth.json"},
		KeyProviders: []string{
			"anthropic", "openai", "google", "github",
			"openrouter", "mistral", "deepseek", "ollama",
		},
	},
	{
		Name:      "crush",
		Args:      []string{"crush", "run", "-q", "{prompt}"},
		ModelFlag: "-m",
		Auth:      authKey,
		LoginArgs: []string{"login"},
		KeyProviders: []string{
			"anthropic", "openai", "google", "xai", "groq",
			"openrouter", "mistral", "deepseek", "ollama",
		},
	},
	{
		Name:      "cline",
		Args:      []string{"cline", "--auto-approve", "true", "{prompt}"},
		ModelFlag: "-m",
		Auth:      authBoth,
		LoginArgs: []string{"auth"},
		CredFiles: []string{".cline/data"},
		KeyProviders: []string{
			"anthropic", "openai", "google", "openrouter", "mistral",
			"deepseek", "groq", "xai", "ollama",
		},
	},
	{
		Name:       "cn",
		Args:       []string{"cn", "--auto", "-p", "{prompt}"},
		ModelFlag:  "--model",
		Auth:       authBoth,
		LoginArgs:  []string{"login"},
		LogoutArgs: []string{"logout"},
		CredFiles:  []string{".continue/config.yaml"},
		KeyProviders: []string{
			"anthropic", "openai", "google", "openrouter", "ollama",
		},
	},
	{
		Name:         "aider",
		Args:         []string{"aider", "--yes-always", "--no-auto-commits", "--message", "{prompt}"},
		ModelFlag:    "--model",
		DefaultModel: "claude-3-7-sonnet",
		Models: []string{
			"claude-3-7-sonnet",
			"claude-3-5-haiku",
			"claude-3-opus",
			"gpt-4o",
			"gpt-4o-mini",
			"o3-mini",
			"gemini/gemini-2.5-flash",
			"deepseek/deepseek-chat",
			"ollama/qwen2.5-coder",
		},
		Auth:         authKey,
		CredFiles:    []string{".aider.conf.yml", ".aider.model.settings.yml"},
		KeyProviders: []string{"anthropic", "openai", "google", "deepseek", "groq", "ollama"},
	},
	{
		Name:         "goose",
		Args:         []string{"goose", "run", "--no-session", "-t", "{prompt}"},
		ModelFlag:    "--model",
		DefaultModel: "gpt-4o",
		Models: []string{
			"gpt-4o",
			"gpt-4o-mini",
			"claude-3-5-sonnet",
			"claude-3-5-haiku",
			"gemini-2.5-flash",
		},
		Auth:         authKey,
		LoginArgs:    []string{"configure"},
		CredFiles:    []string{".config/goose/config.yaml", ".goose/config.yaml"},
		KeyProviders: []string{"anthropic", "openai", "google", "groq", "ollama"},
	},
}

var (
	discoveredModelsMu sync.Mutex
	discoveredModels   = map[string][]string{}
	discoveringModels  = map[string]bool{}
)

func discoverHarnessModels(name, bin string, staticModels []string) []string {
	discoveredModelsMu.Lock()
	if cached, ok := discoveredModels[name]; ok {
		discoveredModelsMu.Unlock()
		return cached
	}
	isDiscovering := discoveringModels[name]
	if !isDiscovering && bin != "" {
		discoveringModels[name] = true
		go runModelDiscovery(name, bin, staticModels)
	}
	discoveredModelsMu.Unlock()

	return staticModels
}

// invalidateDiscoveredModels drops the discovery cache so the next Detect
// re-probes. A harness's model list depends on what it is configured with, so
// setting or clearing a key has to be able to change the answer.
func invalidateDiscoveredModels() {
	discoveredModelsMu.Lock()
	discoveredModels = map[string][]string{}
	discoveringModels = map[string]bool{}
	discoveredModelsMu.Unlock()
}

// promoteDefault puts want first in list, but only when list actually contains
// it.
//
// The condition is the whole point. Discovery is the only thing that knows which
// models exist, so a default the harness did not report is a model that does not
// work. Prepending one anyway puts a dead entry at the top of the picker, and
// worse, makes it the fallback for any remembered model that has since been
// retired -- a default that is guaranteed to fail, offered first.
//
// When want is absent, discovery's own ordering is left alone. It is the best
// evidence available, and second-guessing it with a guess is how the guess wins.
func promoteDefault(want string, list []string) []string {
	if want == "" {
		return list
	}
	out := make([]string, 0, len(list))
	found := false
	for _, m := range list {
		if m == want {
			found = true
			continue
		}
		out = append(out, m)
	}
	if !found {
		return list
	}
	return append([]string{want}, out...)
}

// defaultModelFor is the preset's own default. Discovery reads it from here
// rather than repeating the id, so a preset whose default changes does not
// leave a fourth copy of the old one behind in the discovery path.
func defaultModelFor(name string) string {
	if p, ok := presetByName(name); ok {
		return p.DefaultModel
	}
	return ""
}

func runModelDiscovery(name, bin string, staticModels []string) {
	models := append([]string(nil), staticModels...)
	switch name {
	case "agy":
		ctx, cancel := context.WithTimeout(context.Background(), modelDiscoveryTimeout)
		out, err := exec.CommandContext(ctx, bin, "models").Output()
		cancel()
		if err == nil {
			var list []string
			scanner := bufio.NewScanner(bytes.NewReader(out))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "Fetching") || line == "" {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) > 0 && !strings.Contains(parts[0], " ") {
					list = append(list, parts[0])
				}
			}
			if len(list) > 0 {
				models = promoteDefault(defaultModelFor(name), list)
			}
		}
	case "cursor-agent":
		ctx, cancel := context.WithTimeout(context.Background(), modelDiscoveryTimeout)
		out, err := exec.CommandContext(ctx, bin, "--list-models").Output()
		cancel()
		if err == nil {
			var list []string
			scanner := bufio.NewScanner(bytes.NewReader(out))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "Tip:") {
					continue
				}
				parts := strings.SplitN(line, " - ", 2)
				if len(parts) > 0 {
					id := strings.TrimSpace(parts[0])
					if id != "" && !strings.Contains(id, " ") {
						list = append(list, id)
					}
				}
			}
			if len(list) > 0 {
				models = promoteDefault(defaultModelFor(name), list)
			}
		}
	case "claude":
		ctx, cancel := context.WithTimeout(context.Background(), modelDiscoveryTimeout)
		cmd := exec.CommandContext(ctx, bin, "-p", "/model")
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.Output()
		cancel()
		if err == nil {
			var list []string
			text := string(out)
			if idx := strings.Index(text, "Available:"); idx != -1 {
				avail := text[idx+len("Available:"):]
				if dot := strings.IndexByte(avail, '.'); dot != -1 {
					avail = avail[:dot]
				}
				for _, part := range strings.Split(avail, ",") {
					m := strings.TrimSpace(part)
					m = strings.TrimPrefix(m, "or ")
					if m != "" && !strings.Contains(m, " ") {
						list = append(list, m)
					}
				}
			}
			if len(list) > 0 {
				models = promoteDefault(defaultModelFor(name), list)
			}
		}
	case "opencode":
		ctx, cancel := context.WithTimeout(context.Background(), modelDiscoveryTimeout)
		out, err := exec.CommandContext(ctx, bin, "models").Output()
		cancel()
		if err == nil {
			var list []string
			scanner := bufio.NewScanner(bytes.NewReader(out))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.Contains(line, " ") {
					continue
				}
				list = append(list, line)
			}
			if len(list) > 0 {
				models = promoteDefault(defaultModelFor(name), list)
			}
		}
	case "crush":
		// crush lists the models of whatever providers the user has configured,
		// so an empty answer is a real signal: no key is set up yet. There is no
		// static fallback for this one, because inventing a model id for a tool
		// that will reject it would be worse than showing none.
		ctx, cancel := context.WithTimeout(context.Background(), modelDiscoveryTimeout)
		out, err := exec.CommandContext(ctx, bin, "models").Output()
		cancel()
		if err == nil {
			var list []string
			scanner := bufio.NewScanner(bytes.NewReader(out))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "ERROR") {
					continue
				}
				// Rows are indented and may carry a trailing description; the
				// first field is the id. Fields never contains whitespace, so
				// the id is simply the first one.
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				list = append(list, fields[0])
			}
			if len(list) > 0 {
				models = list
			}
		}
	}

	discoveredModelsMu.Lock()
	discoveredModels[name] = models
	discoveringModels[name] = false
	discoveredModelsMu.Unlock()
}

// agentHarness is one row of the picker.
type agentHarness struct {
	Name      string   `json:"name"`
	Cmd       string   `json:"cmd"`
	Installed bool     `json:"installed"`
	Path      string   `json:"path,omitempty"`
	Models    []string `json:"models,omitempty"`
	Model     string   `json:"model,omitempty"`

	// Auth is where this harness stands on credentials, and AuthModes is the
	// set of choices the user can actually make about it, so the UI never
	// offers a "use my key" toggle that could not mean anything here.
	Auth      authStatus `json:"auth"`
	AuthModes []authMode `json:"authModes,omitempty"`
}

// agentRange anchors a range on a file for overlap checks.
type agentRange struct {
	path string
	l1   int
	l2   int
}

// agentBatchItem represents one instruction anchored to a file range.
type agentBatchItem struct {
	Abs         string `json:"-"`
	Path        string `json:"path"`
	L1          int    `json:"l1"`
	L2          int    `json:"l2"`
	Instruction string `json:"instruction"`
}

// agentJob represents a single background editing task dispatched to an AI coding harness.
// It tracks real-time progress, log outputs, duration, affected files, and cancellation handlers.
type agentJob struct {
	ID         int64            `json:"id"`                   // Unique monotonic job identifier
	Harness    string           `json:"harness"`              // Name of the harness executing this job
	Path       string           `json:"path"`                 // Relative file path targeted for editing
	Lines      string           `json:"lines"`                // Line range formatted string (e.g. "L12-L30")
	Running    bool             `json:"running"`              // True while harness process is actively executing
	Error      string           `json:"error,omitempty"`      // Error message if the job failed or was aborted
	Log        string           `json:"log"`                  // Tail of merged stdout/stderr log output
	Stdout     string           `json:"stdout,omitempty"`     // Stdout log output tail
	Stderr     string           `json:"stderr,omitempty"`     // Stderr log output tail
	Changed    []string         `json:"changed"`              // Files detected as modified after job execution
	Ms         int64            `json:"ms"`                   // Elapsed runtime in milliseconds
	Tracked    bool             `json:"tracked"`              // Whether telemetry tracking has been recorded
	ThreadID   string           `json:"threadId,omitempty"`   // The thread this edit runs as, when it runs as one
	BatchCount int              `json:"batchCount,omitempty"` // Number of items in batch review edit
	Items      []agentBatchItem `json:"items,omitempty"`      // Detailed batch items if multi-file edit

	ranges []agentRange
	l1, l2 int
	cancel context.CancelFunc
	out    *tailBuffer
	stderr *tailBuffer
	start  time.Time

	// kind and model are captured at dispatch, not read back when the run
	// finishes. A job can outlive the selection that started it, and a guard
	// that attributes a failure to whatever happens to be selected now would
	// both miss real repeats and invent false ones.
	kind  string
	model string
}

var (
	// The refusals the UI reacts to rather than merely reporting.
	errAgentBusy  = errors.New("an edit is already running")
	errAgentDirty = errors.New("uncommitted")
	errAgentNone  = errors.New("no coding harness is selected")
	// errAgentRepeat is a dispatch refused because the identical dispatch has
	// just failed, identically, and nothing has changed that would make it work.
	errAgentRepeat = errors.New("the same dispatch has just failed")
)

// Job kinds, used only to keep the repeat guard from confusing one kind of
// dispatch with another. They are not part of the job API: a caller asking for
// job #7 does not care which of these produced it.
const (
	agentKindEdit   = "edit"
	agentKindPrompt = "prompt"
)

// The failure signature is the kind of dispatch, the harness, the model, and
// the error text. Anything that changes the outcome changes at least one of
// them, so an unchanged signature genuinely means a repeat of the same dead end
// rather than a new attempt at a live one.
//
// The kind matters because the same harness on the same model can fail for
// reasons that do not travel together. An inline edit carrying a 40KB diff and
// a commit-message prompt are different requests, and one of them failing says
// nothing about the other -- keying on harness and model alone would let an
// edit's failure refuse an unrelated prompt.
func failureSignature(kind, harness, model, errText string) string {
	return kind + "\x00" + harness + "\x00" + model + "\x00" + errText
}

// lineStreamer forwards complete lines to w with a prefix in real time,
// while also storing the raw bytes into a tailBuffer.
type lineStreamer struct {
	mu     sync.Mutex
	buf    *tailBuffer
	prefix string
	line   []byte
	w      io.Writer
}

func newLineStreamer(buf *tailBuffer, prefix string, w io.Writer) *lineStreamer {
	return &lineStreamer{buf: buf, prefix: prefix, w: w}
}

func (s *lineStreamer) Write(p []byte) (int, error) {
	if s.buf != nil {
		s.buf.Write(p)
	}
	if s.w == nil || uiQuiet {
		return len(p), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			if len(s.line) > 0 {
				fmt.Fprintf(s.w, "  %s %s\n", s.prefix, string(s.line))
				s.line = s.line[:0]
			}
		} else if b != '\r' {
			s.line = append(s.line, b)
		}
	}
	return len(p), nil
}

func (s *lineStreamer) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.line) > 0 && s.w != nil && !uiQuiet {
		fmt.Fprintf(s.w, "  %s %s\n", s.prefix, string(s.line))
		s.line = s.line[:0]
	}
}

// agentManager owns discovery, the current choice, and every edit currently in
// flight. Several harnesses can run at once as long as they touch disjoint
// line ranges: two harnesses rewriting the same lines produces a state nobody
// can review afterwards, so overlapping ranges are refused rather than queued.
type agentManager struct {
	root string
	lsp  *lspManager

	mu          sync.Mutex
	selected    string            // preset name, or the template itself when pinned
	args        []string          // resolved argv, nil when nothing is selected
	pinned      bool              // -agent was given, so the UI cannot change it
	promptStdin bool              // the selected harness takes its prompt on stdin
	models      map[string]string // harness name -> selected model
	jobs        map[int64]*agentJob
	seq         int64
	onEdit      func()

	// lastFail is the signature of the most recent failed run and how many
	// times it has now failed in a row. See recordFailure and repeatGuard.
	lastFail      string
	lastFailCount int

	// Set by the thread manager: inline and batch edits run as threads.
	threadJob    func(id int64) *agentJob // id 0 means the most recent
	threadCancel func(id int64) bool      // id 0 means every one running
}

// newAgentManager wires discovery and restores the remembered choice. A flag
// value pins a harness (or an arbitrary command template) for this run and is
// the only case that can fail: a bad -agent should stop startup, whereas a
// stale settings file should just leave nothing selected.
func newAgentManager(root, flagSpec string, lsp *lspManager) (*agentManager, error) {
	m := &agentManager{
		root:   root,
		lsp:    lsp,
		models: map[string]string{},
	}
	s := readSettings()
	if s.Models != nil {
		for k, v := range s.Models {
			m.models[k] = v
		}
	}

	if spec := strings.TrimSpace(flagSpec); spec != "" {
		name, args, chosenModel, stdin, err := resolveAgentSpec(spec, m.models[spec])
		if err != nil {
			return nil, err
		}
		m.selected, m.args, m.pinned, m.promptStdin = name, args, true, stdin
		if chosenModel != "" {
			m.models[name] = chosenModel
		}
		return m, nil
	}

	if s.Agent != "" {
		if name, args, chosenModel, stdin, err := resolveAgentSpec(s.Agent, m.models[s.Agent]); err == nil {
			m.selected, m.args, m.promptStdin = name, args, stdin
			if chosenModel != "" {
				m.models[name] = chosenModel
			}
		}
	}
	return m, nil
}

// presetArgv builds a preset's argv with the model flag already placed, without
// touching the filesystem. Separated from resolveAgentSpec because the picker
// needs the same argv for display and already knows where the binary is: doing
// the lookup twice per preset is what made listing harnesses cost what it did.
func presetArgv(p agentPreset, model string) (args []string, chosenModel string) {
	chosenModel = model
	if usable, _ := usableModel(p.Name, chosenModel); !usable {
		chosenModel = ""
	}
	if chosenModel == "" {
		chosenModel = p.DefaultModel
	}

	promptIdx := -1
	for i, arg := range p.Args {
		if arg == "{prompt}" {
			promptIdx = i
			break
		}
	}
	// The model flag goes before the flag that takes the prompt, so it lands as
	// an option of the run rather than after a positional argument.
	insertIdx := promptIdx
	if promptIdx > 0 && strings.HasPrefix(p.Args[promptIdx-1], "-") {
		insertIdx = promptIdx - 1
	}
	args = make([]string, 0, len(p.Args)+2)
	for i, arg := range p.Args {
		if i == insertIdx && p.ModelFlag != "" && chosenModel != "" {
			args = append(args, p.ModelFlag, chosenModel)
		}
		args = append(args, arg)
	}
	return args, chosenModel
}

// resolveAgentSpec turns a preset name or a command template into argv, and
// verifies the binary exists now rather than at first use.
//
// It also reports whether the resolved harness takes its instruction on stdin,
// so the caller does not have to re-derive which preset this was. The {prompt}
// token is deliberately left in the returned argv: what a run does with it
// depends on a prompt whose size is not known until the prompt is built, and
// buildArgv is where that decision belongs.
func resolveAgentSpec(spec, model string) (name string, args []string, chosenModel string, stdin bool, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", nil, "", false, errors.New("empty harness")
	}

	for _, p := range agentPresets {
		if strings.EqualFold(spec, p.Name) {
			name = p.Name
			stdin = p.PromptStdin
			args, chosenModel = presetArgv(p, model)
			bin, ok := lookPathIn(args[0], lspBinDirs())
			if !ok {
				return "", nil, "", false, fmt.Errorf("%s is not installed", args[0])
			}
			resolved := append([]string(nil), args...)
			resolved[0] = bin
			return name, resolved, chosenModel, stdin, nil
		}
	}

	// Not a preset: an arbitrary command template. Nothing is known about how it
	// reads its instruction, so it stays on argv and gets the argv budget.
	args = strings.Fields(spec)
	if len(args) == 0 {
		return "", nil, "", false, errors.New("empty harness command")
	}
	if !strings.Contains(spec, "{prompt}") {
		return "", nil, "", false, fmt.Errorf("a command template must contain {prompt} (known harnesses: %s)",
			strings.Join(agentPresetNames(), ", "))
	}
	// The display name is what the rest of px0 matches on to decide how to talk
	// to this harness (stream-json parsing, session resume, native threading),
	// and those switches are written against bare names. On Windows the base of
	// a resolved path carries the extension -- claude.exe, or the claude.cmd
	// shim an npm install produces -- so a pinned command template would be
	// treated as an unknown harness and get none of that behaviour.
	name = harnessDisplayName(args[0])
	chosenModel = model
	if chosenModel != "" {
		for i, arg := range args {
			args[i] = strings.ReplaceAll(arg, "{model}", chosenModel)
		}
	}

	bin, ok := lookPathIn(args[0], lspBinDirs())
	if !ok {
		return "", nil, "", false, fmt.Errorf("%s is not installed", args[0])
	}
	resolved := append([]string(nil), args...)
	resolved[0] = bin
	return name, resolved, chosenModel, false, nil
}

// harnessDisplayName is the bare name of a harness binary, without the
// executable extension Windows adds. It is what identifies a harness to the
// switches that pick a protocol, so "claude.exe" and "claude" have to be the
// same answer.
func harnessDisplayName(bin string) string {
	name := filepath.Base(bin)
	if runtime.GOOS != "windows" {
		return name
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".exe", ".cmd", ".bat", ".com":
		return name[:len(name)-len(filepath.Ext(name))]
	}
	return name
}

func agentPresetNames() []string {
	names := make([]string, len(agentPresets))
	for i, p := range agentPresets {
		names[i] = p.Name
	}
	return names
}

// presetByName finds a preset, case-insensitively. A name that is not a preset
// (an arbitrary command template, whose display name is its binary) simply
// yields false, which is the conservative answer for every caller: no stdin,
// because nothing about an unknown command's stdin handling is knowable from
// here, and no auth story, because px0 cannot describe credentials for a
// command it does not know.
func presetByName(name string) (agentPreset, bool) {
	for _, p := range agentPresets {
		if strings.EqualFold(name, p.Name) {
			return p, true
		}
	}
	return agentPreset{}, false
}

// harnessPromptStdin reports whether the named harness takes its instruction
// from stdin rather than argv.
func harnessPromptStdin(name string) bool {
	p, ok := presetByName(name)
	return ok && p.PromptStdin
}

// discoverableHarnesses are the presets whose model list px0 can ask the CLI
// for. Only these may retire a remembered model.
//
// The distinction matters. runModelDiscovery caches under the harness name for
// every preset, but for a harness with no discovery case it caches the
// hand-written static list, unchanged. Treating that as a discovered answer
// would let a guess retire a model the user is entitled to: a codex preset
// written months ago knows nine ids, and a user running a newer one would be
// silently switched back to gpt-5-codex on every dispatch. A guess is not
// evidence that a model is gone.
var discoverableHarnesses = map[string]bool{
	"agy":          true,
	"cursor-agent": true,
	"claude":       true,
	"opencode":     true,
	"crush":        true,
}

// discoveredModelsFor returns the models discovery settled on for a harness,
// and whether that answer is worth acting on.
//
// The second value is the whole point, and it is stricter than "is the key
// present". It is true only for a harness px0 can genuinely ask, and only when
// the answer is non-empty. Everything else -- a static list, an empty result, a
// discovery still running -- means "not known", which must never be read as
// "known to be gone".
func discoveredModelsFor(name string) (models []string, complete bool) {
	discoveredModelsMu.Lock()
	defer discoveredModelsMu.Unlock()
	models, ok := discoveredModels[name]
	if !ok || !discoverableHarnesses[name] || len(models) == 0 {
		return nil, false
	}
	return models, true
}

// agentDefaultModel is the preset's fallback, or "" for something that is not a
// preset and therefore has none px0 can speak for.
func agentDefaultModel(name string) string {
	if p, ok := presetByName(name); ok {
		return p.DefaultModel
	}
	return ""
}

// usableModel reports whether a remembered model can still be handed to a
// harness, and if not, why.
//
// A model id is a string in a settings file that nothing revalidates, and
// providers retire them. When one goes away the harness fails every single run
// with "Model unavailable: <id>", which looks like a px0 bug and is not one.
// Dropping to the default at selection time turns a permanent, silent failure
// into one visible message.
func usableModel(name, model string) (bool, string) {
	if strings.TrimSpace(model) == "" {
		return true, ""
	}
	p, ok := presetByName(name)
	if !ok {
		// An arbitrary command template. px0 has no list to check against, and
		// inventing a rule here would break every harness it does not ship with.
		return true, ""
	}
	models, complete := discoveredModelsFor(p.Name)
	if !complete {
		// Discovery is still running. Not knowing is not the same as knowing the
		// model is gone, so defer rather than override a deliberate choice.
		return true, ""
	}
	for _, m := range models {
		if m == model {
			return true, ""
		}
	}
	return false, fmt.Sprintf("%s no longer offers the model %q", p.Name, model)
}

// maxArgvPromptBytes is how much prompt text may travel in argv for a binary
// that cannot be fed on stdin.
//
// The binding constraint is Windows, and it is the one nobody expects. argv is
// marshalled into a single command line, and CreateProcessW caps that at 32,767
// characters. But when the resolved binary is a .cmd or .bat -- which is what
// every npm global install resolves to, via its shim -- the kernel launches
// cmd.exe /c around it, and cmd.exe's own limit is 8,191. Over that it does not
// report a truncation, it exits 1 having run nothing.
//
// The two failure modes are both silent in different ways, which is why this
// is enforced as a budget rather than discovered at runtime:
//   - 8,191 < n <= 32,767: cmd.exe runs, the harness never does, "exit status 1"
//   - n > 32,767:            CreateProcess fails outright, "filename or extension
//     is too long"
//
// A real .exe on Windows gets the larger ceiling. Linux caps a single argument
// at MAX_ARG_STRLEN (128 KiB on glibc), so the per-arg bound is well clear of
// that and the practical limit is the context window, not the kernel.
func maxArgvPromptBytes(bin string) int {
	if runtime.GOOS != "windows" {
		return 96 << 10
	}
	switch strings.ToLower(filepath.Ext(bin)) {
	case ".cmd", ".bat":
		// cmd.exe allows 8,191 for the whole line: the binary path, every flag,
		// the model id, and the quoting Go adds around every argument. 6,000
		// leaves room for all of that.
		return 6 << 10
	default:
		// A native binary skips cmd.exe entirely, so only CreateProcess applies.
		return 30 << 10
	}
}

// clampPrompt trims a prompt to what the binary's argv can carry, cutting on a
// line boundary so the harness never receives half a line of context. Returns
// the text and whether anything was dropped, so the caller can say so instead
// of letting the harness quietly reason about a diff it was never shown.
func clampPrompt(prompt, bin string) (string, bool) {
	limit := maxArgvPromptBytes(bin)
	if len(prompt) <= limit {
		return prompt, false
	}
	const notice = "\n\n[px0 truncated this prompt to fit the operating system's command-line limit. " +
		"The full content was too large to pass as an argument. Narrow the line range, " +
		"or stage less, and try again.]"
	cut := limit - len(notice)
	if cut < 0 {
		cut = 0
	}
	if idx := strings.LastIndexByte(prompt[:cut], '\n'); idx > 0 {
		cut = idx
	}
	return prompt[:cut] + notice, true
}

// buildArgv substitutes the prompt into a template, or drops the {prompt} token
// entirely when the harness is being fed on stdin. Every occurrence is dropped,
// not just the first: a template with two is a user error, but passing a
// literal "{prompt}" to a harness as though it were the instruction is worse
// than quietly removing it.
func buildArgv(template []string, prompt string, useStdin bool) []string {
	if useStdin {
		out := make([]string, 0, len(template))
		for _, tok := range template {
			if tok != "{prompt}" {
				out = append(out, tok)
			}
		}
		return out
	}
	out := make([]string, len(template))
	for i, tok := range template {
		out[i] = strings.ReplaceAll(tok, "{prompt}", prompt)
	}
	return out
}

// Detect reports every harness px0 knows and whether it is installed right
// now, so a tool installed since startup shows up without a restart.
func (m *agentManager) Detect() []agentHarness {
	m.mu.Lock()
	savedModels := make(map[string]string, len(m.models))
	for k, v := range m.models {
		savedModels[k] = v
	}
	m.mu.Unlock()

	out := make([]agentHarness, 0, len(agentPresets))
	for _, p := range agentPresets {
		bin, ok := lookPathIn(p.Args[0], lspBinDirs())
		models := discoverHarnessModels(p.Name, bin, p.Models)
		curModel := savedModels[p.Name]
		// A model the harness has dropped must not be handed back to the picker
		// as if it were selectable, or the user is offered the one choice that
		// is guaranteed to fail.
		if usable, _ := usableModel(p.Name, curModel); !usable {
			curModel = ""
		}
		if curModel == "" {
			curModel = p.DefaultModel
		}

		// The binary was resolved once at the top of this loop. Going back
		// through resolveAgentSpec to build the display string would resolve it
		// a second time for every preset, which on a machine with a long PATH is
		// the single most expensive thing the picker does.
		cmdStr := strings.Join(p.Args, " ")
		if args, _ := presetArgv(p, curModel); len(args) > 0 && ok {
			display := append([]string(nil), args...)
			display[0] = bin
			cmdStr = strings.Join(display, " ")
		}

		h := agentHarness{
			Name:      p.Name,
			Cmd:       cmdStr,
			Installed: ok,
			Path:      bin,
			Models:    models,
			Model:     curModel,
			Auth:      authStatusFor(p, authModeForSpec(p.Name)),
			AuthModes: authModesForPreset(p),
		}
		out = append(out, h)
	}
	return out
}

func (m *agentManager) Name() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.selected
}

func (m *agentManager) Model() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.selected == "" {
		return ""
	}
	return m.models[m.selected]
}

// current returns the selected harness, its headless argv and its model, read
// together so a run never mixes one harness's name with another's flags.
func (m *agentManager) current() (string, []string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.args == nil {
		return "", nil, ""
	}
	return m.selected, append([]string(nil), m.args...), m.models[m.selected]
}

func (m *agentManager) Pinned() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinned
}

// childEnv is the environment every harness run starts from: the caller's own,
// plus a variable per provider this harness takes a key from, but only when the
// user has actually asked for that harness to be driven by a key.
//
// The default matters. A subscription-driven harness is already signed in, and
// quietly exporting ANTHROPIC_API_KEY underneath it would start billing a
// different account for the same edit -- a failure the user would only notice
// on an invoice. So "auto" injects nothing for those, and everything for the
// harnesses that have no login to fall back on. A harness named only by a
// command template has no preset and therefore gets the caller's environment
// unchanged, which is also what a template author expects. Returning nil means
// "inherit", which is the right answer in every one of those cases.
func (m *agentManager) childEnv() []string {
	m.mu.Lock()
	name := m.selected
	m.mu.Unlock()
	return m.childEnvFor(name)
}

// childEnvFor is childEnv for a harness named explicitly, which is what a run
// dispatched to a particular harness needs.
//
// The name has to be passed in rather than read from m.selected because the
// selection can change between dispatch and exec: a run that was dispatched to
// one harness and then picks up another harness's key is both a wrong-account
// edit and a confusing failure. Every caller that already knows which harness it
// is running should use this.
func (m *agentManager) childEnvFor(name string) []string {
	p, ok := presetByName(name)
	if !ok {
		return nil
	}
	if !willInjectKeys(p, authModeForSpec(name)) {
		return nil
	}
	return harnessEnvironment(p.KeyProviders)
}

// Select remembers a harness for this workspace and every later run. Passing an
// empty name turns editing back off.
func (m *agentManager) Select(name string, modelOpt ...string) error {
	m.mu.Lock()
	if m.pinned {
		m.mu.Unlock()
		return errors.New("px0 was started with -agent, so the harness is fixed for this run")
	}
	m.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		m.mu.Lock()
		m.selected, m.args, m.promptStdin = "", nil, false
		m.mu.Unlock()
		// Turning editing off is an explicit "I am done with agent work", so it
		// does clear the remembered models along with the harness.
		empty := ""
		return writeSettings(settingsPatch{Agent: &empty, ClearModels: true})
	}

	reqModel := ""
	if len(modelOpt) > 0 {
		reqModel = strings.TrimSpace(modelOpt[0])
	}
	m.mu.Lock()
	if reqModel == "" {
		reqModel = m.models[name]
	}
	m.mu.Unlock()

	// Say so when a remembered model is dropped, because the alternative is a
	// silent substitution the user will notice as "why is it using a different
	// model than I picked" with nothing to connect it to the old choice.
	if usable, why := usableModel(name, reqModel); !usable {
		uiStatus("warn", "agent", fmt.Sprintf("%s; using %q instead", why, agentDefaultModel(name)), 0, os.Stdout)
		reqModel = ""
	}

	display, args, chosenModel, stdin, err := resolveAgentSpec(name, reqModel)
	if err != nil {
		uiStatus("err", fmt.Sprintf("agent: failed to select harness %q", name), err.Error(), 0, os.Stdout)
		return err
	}
	m.mu.Lock()
	m.selected, m.args, m.promptStdin = display, args, stdin
	if m.models == nil {
		m.models = map[string]string{}
	}
	if chosenModel != "" {
		m.models[display] = chosenModel
	}
	// Changing the harness or its model is the user saying "try again, but
	// differently", which is exactly what the repeat guard exists to permit.
	m.lastFail, m.lastFailCount = "", 0
	savedModels := make(map[string]string, len(m.models))
	for k, v := range m.models {
		savedModels[k] = v
	}
	m.mu.Unlock()

	modelNote := ""
	if chosenModel != "" {
		modelNote = fmt.Sprintf(" (%s)", chosenModel)
	}
	uiStatus("ok", "agent", fmt.Sprintf("%s%s", display, modelNote), 0, os.Stdout)
	// Persist the spec as given, not the display name: a command template
	// shortens to its binary for display and would not survive the round trip.
	return writeSettings(settingsPatch{Agent: &name, Models: savedModels})
}

// Job returns a snapshot of job id, or of the most recently started job when
// id is 0, or nil when there isn't one.
func (m *agentManager) Job(id int64) *agentJob {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	j := m.jobs[id]
	if id == 0 {
		for _, cand := range m.jobs {
			if j == nil || cand.ID > j.ID {
				j = cand
			}
		}
	}
	var cp *agentJob
	if j != nil {
		c := *j
		c.Log = j.out.String()
		c.Stdout = c.Log
		if j.stderr != nil {
			c.Stderr = j.stderr.String()
		}
		if c.Running {
			c.Ms = time.Since(j.start).Milliseconds()
		}
		cp = &c
	}
	lookup := m.threadJob
	m.mu.Unlock()

	// Inline and batch edits run as threads and share this id space, so one
	// poll endpoint serves both. Asked for the latest, the newer of the two wins.
	if lookup != nil {
		if tj := lookup(id); tj != nil && (cp == nil || tj.ID > cp.ID) {
			return tj
		}
	}
	return cp
}

// nextJobID hands out an id from the sequence every job shares.
func (m *agentManager) nextJobID() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	return m.seq
}

// anyRunningLocked reports whether any job is still in flight. Callers hold m.mu.
func (m *agentManager) anyRunningLocked() bool {
	for _, j := range m.jobs {
		if j.Running {
			return true
		}
	}
	return false
}

// overlapLocked reports whether a running job already touches rel within
// [l1,l2]. Different paths, or disjoint ranges on the same path, are free to
// run at the same time. Callers hold m.mu.
// findOverlappingJobLocked returns the running job that touches rel within [l1, l2],
// or nil if none overlaps. Callers hold m.mu.
func (m *agentManager) findOverlappingJobLocked(rel string, l1, l2 int) *agentJob {
	for _, j := range m.jobs {
		if !j.Running {
			continue
		}
		if len(j.ranges) > 0 {
			for _, r := range j.ranges {
				if r.path == rel && l1 <= r.l2 && r.l1 <= l2 {
					return j
				}
			}
		} else if j.Path == rel && l1 <= j.l2 && j.l1 <= l2 {
			return j
		}
	}
	return nil
}

// overlapLocked reports whether a running job already touches rel within
// [l1,l2]. Different paths, or disjoint ranges on the same path, are free to
// run at the same time. Callers hold m.mu.
func (m *agentManager) overlapLocked(rel string, l1, l2 int) bool {
	return m.findOverlappingJobLocked(rel, l1, l2) != nil
}

// Start dispatches an instruction anchored to abs:l1-l2. It returns as soon as
// the harness is running.
func (m *agentManager) Start(abs, rel string, l1, l2 int, instruction string, force bool) (*agentJob, error) {
	return m.StartBatch([]agentBatchItem{{
		Abs:         abs,
		Path:        rel,
		L1:          l1,
		L2:          l2,
		Instruction: instruction,
	}}, force)
}

// StartBatch dispatches a batch of instructions anchored to one or more file ranges.
func (m *agentManager) StartBatch(items []agentBatchItem, force bool) (*agentJob, error) {
	if len(items) == 0 {
		return nil, errors.New("no edits specified")
	}

	for i := range items {
		items[i].Instruction = strings.TrimSpace(items[i].Instruction)
		if items[i].Instruction == "" {
			return nil, errors.New("instruction is empty")
		}
		if items[i].L1 < 1 {
			items[i].L1 = 1
		}
		if items[i].L2 < items[i].L1 {
			items[i].L2 = items[i].L1
		}
		for j := 0; j < i; j++ {
			if items[i].Path == items[j].Path && items[i].L1 <= items[j].L2 && items[j].L1 <= items[i].L2 {
				uiStatus("warn", "agent", fmt.Sprintf("batch edit refused: overlapping edits on %s (%s and %s)", items[i].Path, lineRef(items[i].L1, items[i].L2), lineRef(items[j].L1, items[j].L2)), 0, os.Stdout)
				return nil, fmt.Errorf("overlapping edits in batch on %s (%s and %s)", items[i].Path, lineRef(items[i].L1, items[i].L2), lineRef(items[j].L1, items[j].L2))
			}
		}
	}

	m.mu.Lock()
	if m.args == nil {
		m.mu.Unlock()
		uiStatus("err", "agent", "edit dispatch refused: no coding harness selected", 0, os.Stdout)
		return nil, errAgentNone
	}
	for _, it := range items {
		if blocking := m.findOverlappingJobLocked(it.Path, it.L1, it.L2); blocking != nil {
			m.mu.Unlock()
			loc := fmt.Sprintf("%s:%s", it.Path, lineRef(it.L1, it.L2))
			msg := fmt.Sprintf("an edit is already running on %s (job #%d with %s)", loc, blocking.ID, blocking.Harness)
			uiStatus("warn", "agent", "edit dispatch refused: "+msg, 0, os.Stdout)
			return nil, fmt.Errorf("%w: %s", errAgentBusy, msg)
		}
	}
	args := m.args
	name := m.selected
	chosenModel := m.models[name]
	if n, blocked := m.repeatGuardLocked(agentKindEdit, name, chosenModel); blocked {
		m.mu.Unlock()
		msg := fmt.Sprintf("%s has now failed %d times in a row with the same error; not retrying", name, n)
		uiStatus("err", "agent", "edit dispatch refused: "+msg+" -- fix the harness, model, or prompt size, then try again", 0, os.Stdout)
		return nil, fmt.Errorf("%w: %s", errAgentRepeat, msg)
	}
	m.mu.Unlock()

	prepared := make([]itemWithSnippet, len(items))
	for i, it := range items {
		snippet, err := readLineRange(it.Abs, it.L1, it.L2)
		if err != nil {
			uiStatus("err", "agent", fmt.Sprintf("failed reading snippet for %s:%s: %s", it.Path, lineRef(it.L1, it.L2), err.Error()), 0, os.Stdout)
			return nil, err
		}
		prepared[i] = itemWithSnippet{item: it, snippet: snippet}
	}

	m.mu.Lock()
	// Re-check under lock: another dispatch may have raced between the check
	// above and here, while this one was reading the file and git status.
	for _, it := range items {
		if blocking := m.findOverlappingJobLocked(it.Path, it.L1, it.L2); blocking != nil {
			m.mu.Unlock()
			loc := fmt.Sprintf("%s:%s", it.Path, lineRef(it.L1, it.L2))
			msg := fmt.Sprintf("an edit is already running on %s (job #%d with %s)", loc, blocking.ID, blocking.Harness)
			uiStatus("warn", "agent", "edit dispatch refused: "+msg, 0, os.Stdout)
			return nil, fmt.Errorf("%w: %s", errAgentBusy, msg)
		}
	}
	m.seq++
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)

	ranges := make([]agentRange, len(items))
	for i, it := range items {
		ranges[i] = agentRange{path: it.Path, l1: it.L1, l2: it.L2}
	}

	allSameFile := true
	for _, it := range items {
		if it.Path != items[0].Path {
			allSameFile = false
			break
		}
	}

	var displayPath, displayLines string
	if allSameFile {
		displayPath = items[0].Path
		if len(items) == 1 {
			displayLines = lineRef(items[0].L1, items[0].L2)
		} else {
			displayLines = fmt.Sprintf("%d edits", len(items))
		}
	} else {
		uniqueFiles := make(map[string]bool)
		for _, it := range items {
			uniqueFiles[it.Path] = true
		}
		displayPath = fmt.Sprintf("%d files", len(uniqueFiles))
		displayLines = fmt.Sprintf("%d edits", len(items))
	}

	job := &agentJob{
		ID:         m.seq,
		Harness:    name,
		Path:       displayPath,
		Lines:      displayLines,
		kind:       agentKindEdit,
		model:      chosenModel,
		Running:    true,
		Changed:    []string{},
		Tracked:    gitAvailable(m.root),
		BatchCount: len(items),
		Items:      items,
		ranges:     ranges,
		l1:         items[0].L1,
		l2:         items[0].L2,
		out:        &tailBuffer{max: agentLogBytes},
		stderr:     &tailBuffer{max: agentLogBytes},
		start:      time.Now(),
		cancel:     cancel,
	}
	if m.jobs == nil {
		m.jobs = map[int64]*agentJob{}
	}
	m.jobs[job.ID] = job
	modelStr := ""
	if m.models != nil && m.models[name] != "" {
		modelStr = fmt.Sprintf(" (%s)", m.models[name])
	}
	m.mu.Unlock()

	if len(items) == 1 {
		uiStatus("step", "agent", fmt.Sprintf("#%d %s%s · %s:%s  %q", job.ID, name, modelStr, items[0].Path, lineRef(items[0].L1, items[0].L2), items[0].Instruction), 0, os.Stdout)
	} else {
		uiStatus("step", "agent", fmt.Sprintf("#%d %s%s · batch %d edits across %s", job.ID, name, modelStr, len(items), displayPath), 0, os.Stdout)
	}

	var prompt string
	if len(items) == 1 {
		prompt = agentPrompt(items[0].Path, items[0].L1, items[0].L2, prepared[0].snippet, items[0].Instruction)
	} else {
		prompt = agentBatchPrompt(prepared)
	}

	go m.run(ctx, cancel, job, args, prompt)
	return m.Job(job.ID), nil
}

// StartPrompt dispatches a one-shot prompt to the selected harness with no
// target file -- used for generating text (e.g. a commit message) rather
// than editing code. There is no file range to anchor an overlap check
// against, so a prompt job is never blocked by, or blocks, a file edit.
func (m *agentManager) StartPrompt(label, prompt string) (*agentJob, error) {
	m.mu.Lock()
	if m.args == nil {
		m.mu.Unlock()
		uiStatus("err", "agent", "prompt dispatch refused: no coding harness selected", 0, os.Stdout)
		return nil, errAgentNone
	}
	args := m.args
	name := m.selected
	chosenModel := m.models[name]
	if n, blocked := m.repeatGuardLocked(agentKindPrompt, name, chosenModel); blocked {
		m.mu.Unlock()
		msg := fmt.Sprintf("%s has now failed %d times in a row with the same error; not retrying", name, n)
		uiStatus("err", "agent", "prompt dispatch refused: "+msg+" -- fix the harness, model, or prompt size, then try again", 0, os.Stdout)
		return nil, fmt.Errorf("%w: %s", errAgentRepeat, msg)
	}
	m.seq++
	ctx, cancel := context.WithTimeout(context.Background(), agentTimeout)
	job := &agentJob{
		ID:      m.seq,
		Harness: name,
		Path:    label,
		kind:    agentKindPrompt,
		model:   chosenModel,
		Running: true,
		Changed: []string{},
		Tracked: gitAvailable(m.root),
		out:     &tailBuffer{max: agentLogBytes},
		stderr:  &tailBuffer{max: agentLogBytes},
		start:   time.Now(),
		cancel:  cancel,
	}
	if m.jobs == nil {
		m.jobs = map[int64]*agentJob{}
	}
	m.jobs[job.ID] = job
	modelStr := ""
	if m.models != nil && m.models[name] != "" {
		modelStr = fmt.Sprintf(" (%s)", m.models[name])
	}
	m.mu.Unlock()

	uiStatus("step", "agent", fmt.Sprintf("#%d %s%s · %s", job.ID, name, modelStr, label), 0, os.Stdout)

	go m.run(ctx, cancel, job, args, prompt)
	return m.Job(job.ID), nil
}

func (m *agentManager) run(ctx context.Context, cancel context.CancelFunc, job *agentJob, template []string, prompt string) {
	defer cancel()
	defer func() {
		m.mu.Lock()
		job.Running = false
		job.Ms = time.Since(job.start).Milliseconds()
		if job.cancel != nil {
			job.cancel = nil
		}
		m.mu.Unlock()
	}()

	if uiVerbose {
		uiVerbosePrompt(job.ID, job.Harness, prompt, os.Stdout)
	}

	before := worktreeSnapshot(m.root)

	m.mu.Lock()
	useStdin := m.promptStdin
	m.mu.Unlock()

	// A prompt carried in argv is subject to a hard command-line limit that
	// varies by platform and by whether the binary is an npm shim, so it is
	// clamped to what this binary can actually receive. On stdin there is no
	// limit worth defending against, and nothing is dropped.
	stdinText := prompt
	if !useStdin {
		var truncated bool
		stdinText, truncated = clampPrompt(prompt, template[0])
		if truncated {
			uiStatus("warn", "agent", fmt.Sprintf("#%d prompt trimmed to fit the command-line limit for %s",
				job.ID, template[0]), 0, os.Stdout)
		}
	}
	args := buildArgv(template, stdinText, useStdin)

	stdoutStreamer := newLineStreamer(job.out, uiFaint("│", os.Stdout), os.Stdout)
	stderrStreamer := newLineStreamer(job.stderr, uiDim("│", os.Stdout), os.Stdout)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = m.root
	cmd.Env = m.childEnvFor(job.Harness)
	cmd.Stdout = stdoutStreamer
	cmd.Stderr = stderrStreamer
	cmd.WaitDelay = 2 * time.Second
	setProcessGroup(cmd)
	// A harness fed on stdin gets the prompt there and nothing else; one fed on
	// argv gets its stdin closed immediately, so a harness that still tries to
	// ask something fails fast instead of hanging until the timeout with
	// nothing on screen.
	if useStdin {
		cmd.Stdin = strings.NewReader(prompt)
	}

	err := cmd.Run()
	stdoutStreamer.Flush()
	stderrStreamer.Flush()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			err = errors.New("cancelled")
		} else {
			err = fmt.Errorf("gave up after %s", agentTimeout)
		}
	}

	changed := changedSince(m.root, before)
	m.settle(changed)

	m.mu.Lock()
	job.Running = false
	job.Changed = changed
	job.Ms = time.Since(job.start).Milliseconds()
	if err != nil {
		job.Error = err.Error()
	}
	stdoutOutput := job.out.String()
	stderrOutput := job.stderr.String()
	m.mu.Unlock()

	// Remember how this ended. A success wipes the memory so the next dispatch
	// is always free to try; a failure is remembered against the kind, harness
	// and model the job was actually dispatched with, not whatever is selected
	// now.
	if err != nil {
		m.recordFailure(job.kind, job.Harness, job.model, err.Error())
	} else {
		m.clearFailure()
	}

	durStr := fmtDuration(time.Duration(job.Ms) * time.Millisecond)
	if err != nil {
		uiStatus("err", "agent", fmt.Sprintf("#%d %s failed in %s: %s", job.ID, job.Harness, durStr, err.Error()), 0, os.Stdout)
		if trimmedErr := strings.TrimSpace(stderrOutput); trimmedErr != "" {
			uiKV("harness stderr", trimmedErr, 0, os.Stdout)
		}
		if trimmedOut := strings.TrimSpace(stdoutOutput); trimmedOut != "" {
			uiKV("harness stdout", trimmedOut, 0, os.Stdout)
		}
	} else {
		var summary string
		switch len(changed) {
		case 0:
			summary = "no files changed"
		case 1:
			summary = fmt.Sprintf("1 file changed: %s", changed[0])
		default:
			summary = fmt.Sprintf("%d files changed: %s", len(changed), strings.Join(changed, ", "))
		}
		uiStatus("ok", "agent", fmt.Sprintf("#%d %s · %s  (%s)", job.ID, job.Harness, durStr, summary), 0, os.Stdout)
		if len(changed) == 0 && job.Tracked {
			if trimmedErr := strings.TrimSpace(stderrOutput); trimmedErr != "" {
				uiKV("harness stderr", trimmedErr, 0, os.Stdout)
			}
		}
	}
}

// settle drops every trace of the old bytes. Open memoises on path+mtime+size
// so a rewritten file already misses the cache, but a harness that truncates
// and writes in place can be read mid-write, and that torn copy would then sit
// under a key nothing invalidates. Evicting is cheaper than reasoning about it.
// Language servers hold their own copy of the file and never saw the write, so
// they are closed here too and reopen on the next request.
func (m *agentManager) settle(changed []string) {
	for _, rel := range changed {
		abs := filepath.Join(m.root, filepath.FromSlash(rel))
		Evict(abs)
		if m.lsp != nil {
			m.lsp.CloseDoc(abs, rel)
		}
	}
	if len(changed) > 0 && m.onEdit != nil {
		m.onEdit()
	}
}

// recordFailure notes how a run ended, so the next dispatch can recognise a
// repeat. A success clears the memory entirely: whatever was broken is not
// broken any more, and holding onto it would refuse a dispatch that should be
// allowed to try.
func (m *agentManager) recordFailure(kind, harness, model, errText string) {
	if m == nil {
		return
	}
	sig := failureSignature(kind, harness, model, errText)
	m.mu.Lock()
	defer m.mu.Unlock()
	if sig == m.lastFail {
		m.lastFailCount++
		return
	}
	m.lastFail, m.lastFailCount = sig, 1
}

// repeatGuardLocked reports whether this exact dispatch has just failed, and how
// many times. The caller holds m.mu.
//
// A failure this reproducible is a misconfiguration, not bad luck: a retired
// model, a missing key, a command line the OS refused. Re-running it produces
// the same error and costs the user another wait each time, so the second
// identical failure is refused with a pointer at the cause rather than
// dispatched again. It is a guard, not a lock -- changing the harness or the
// model changes the signature, and one successful run clears it.
func (m *agentManager) repeatGuardLocked(kind, harness, model string) (count int, blocked bool) {
	if m == nil || m.lastFail == "" {
		return 0, false
	}
	// A different kind, harness, or model is a different attempt, not a repeat,
	// even if the error text would come out identical.
	if !strings.HasPrefix(m.lastFail, kind+"\x00"+harness+"\x00"+model+"\x00") {
		return 0, false
	}
	if m.lastFailCount < 2 {
		return m.lastFailCount, false
	}
	return m.lastFailCount, true
}

// clearFailure forgets the last failure, so a fresh dispatch is always allowed
// to try. Called when the user changes the harness or its model.
func (m *agentManager) clearFailure() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.lastFail, m.lastFailCount = "", 0
	m.mu.Unlock()
}

// Cancel stops every harness currently running. Whatever each has already
// written stays.
func (m *agentManager) Cancel() bool {
	return m.CancelJob(0)
}

// CancelJob stops the harness run with the given id, or every running harness
// when id is 0.
func (m *agentManager) CancelJob(id int64) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	tc := m.threadCancel
	m.mu.Unlock()
	fromThreads := tc != nil && tc(id)
	if fromThreads && id != 0 {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != 0 {
		j := m.jobs[id]
		if j != nil && j.Running && j.cancel != nil {
			uiStatus("warn", "agent", fmt.Sprintf("cancelled in-flight run with %s (job %d)", j.Harness, j.ID), 0, os.Stdout)
			j.Running = false
			j.Error = "cancelled"
			cancel := j.cancel
			j.cancel = nil
			cancel()
			return true
		}
		return false
	}
	cancelled := false
	for _, j := range m.jobs {
		if !j.Running || j.cancel == nil {
			continue
		}
		uiStatus("warn", "agent", fmt.Sprintf("cancelled in-flight run with %s (job %d)", j.Harness, j.ID), 0, os.Stdout)
		j.Running = false
		j.Error = "cancelled"
		cancel := j.cancel
		j.cancel = nil
		cancel()
		cancelled = true
	}
	return cancelled || fromThreads
}

func (m *agentManager) Close() { m.Cancel() }

// changedSince reports the paths whose state differs from the snapshot taken
// before the run. Asking git is the only honest answer to "what did it touch":
// a harness routinely edits files nobody pointed it at.
func changedSince(root string, before map[string]string) []string {
	return changedSinceMaps(before, worktreeSnapshot(root))
}

// worktreeSnapshot is git status with each listed file's size and mtime folded
// into its entry. Status alone misses the common case of editing a file that is
// already modified: it reads "M" before and after, so the edit would go unseen.
// Files git lists as clean are left out, and those still surface through status.
func worktreeSnapshot(root string) map[string]string {
	st := gitStatus(root)
	for rel, code := range st {
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			st[rel] = code + " " + strconv.FormatInt(fi.Size(), 10) + " " + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
		}
	}
	return st
}

// changedSinceMaps compares two status snapshots in both directions. Outside a
// git repository both are nil and nothing is ever reported as changed, which is
// why a job carries Tracked for the client to fall back on.
func changedSinceMaps(before, after map[string]string) []string {
	out := []string{}
	for path, st := range after {
		if before[path] != st {
			out = append(out, path)
		}
	}
	// A file restored to its committed state leaves the status list entirely.
	for path := range before {
		if _, still := after[path]; !still {
			out = append(out, path)
		}
	}
	// Map iteration order is random; sort so the job's summary and API
	// response list the same files in the same order on every run.
	sort.Strings(out)
	return out
}

func lineRef(l1, l2 int) string {
	if l1 == l2 {
		return strconv.Itoa(l1)
	}
	return strconv.Itoa(l1) + "-" + strconv.Itoa(l2)
}

// readLineRange returns lines l1..l2 of a file, 1-based and inclusive.
//
// A selected range can span a whole file, and a whole file can be a
// megabyte-scale generated bundle. The text goes into a prompt, so it is
// clamped: an unbounded read here is a prompt nobody can send, and the
// resulting failure ("filename or extension is too long", or a bare
// "exit status 1" from a .cmd shim) says nothing about which line range caused
// it. Cutting at maxSnippetBytes keeps the selection anchored at l1, which is
// the line the user actually clicked, rather than at the top of the file.
func readLineRange(abs string, l1, l2 int) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if l1 < 1 {
		l1 = 1
	}
	if l2 < l1 {
		l2 = l1
	}
	if l1 > len(lines) {
		return "", fmt.Errorf("line %d is past the end of %s", l1, filepath.Base(abs))
	}
	if l2 > len(lines) {
		l2 = len(lines)
	}
	snippet := strings.Join(lines[l1-1:l2], "\n")
	if len(snippet) > maxSnippetBytes {
		cut := maxSnippetBytes
		if idx := strings.LastIndexByte(snippet[:cut], '\n'); idx > 0 {
			cut = idx
		}
		snippet = snippet[:cut] +
			fmt.Sprintf("\n[px0 showed the first %d bytes of lines %s. Select a narrower range.]",
				cut, lineRef(l1, l1+strings.Count(snippet[:cut], "\n")))
	}
	return snippet, nil
}

// agentPrompt composes what the harness is told. It deliberately matches the
// shape of the Copy for Agent snippet in web/src/selbar.js, which these tools
// already read well.
func agentPrompt(rel string, l1, l2 int, snippet, instruction string) string {
	ext := strings.TrimPrefix(filepath.Ext(rel), ".")
	var b strings.Builder
	lineStr := fmt.Sprintf("lines %d-%d", l1, l2)
	if l1 == l2 {
		lineStr = fmt.Sprintf("line %d", l1)
	}
	fmt.Fprintf(&b, "@%s %s\n```%s\n%s\n```\n\n", rel, lineStr, ext, snippet)
	fmt.Fprintf(&b, "### Instruction\n%s\n\n", instruction)
	b.WriteString("Edit the file in place to carry out that instruction. ")
	b.WriteString("Change only what it asks for, and do not explain the change afterwards.")
	return b.String()
}

type itemWithSnippet struct {
	item    agentBatchItem
	snippet string
}

func agentBatchPrompt(items []itemWithSnippet) string {
	var b strings.Builder
	b.WriteString("Batch Edit Request: Carry out all of the following instructions across the workspace.\n\n")
	for i, it := range items {
		ext := strings.TrimPrefix(filepath.Ext(it.item.Path), ".")
		lineStr := fmt.Sprintf("lines %d-%d", it.item.L1, it.item.L2)
		if it.item.L1 == it.item.L2 {
			lineStr = fmt.Sprintf("line %d", it.item.L1)
		}
		fmt.Fprintf(&b, "### Edit %d: @%s %s\n```%s\n%s\n```\n\n", i+1, it.item.Path, lineStr, ext, it.snippet)
		fmt.Fprintf(&b, "**Instruction**: %s\n\n", it.item.Instruction)
	}
	b.WriteString("Edit the file(s) in place to carry out all of the above instructions. ")
	b.WriteString("Change only what they ask for, coordinate changes cleanly, and do not explain the changes afterwards.")
	return b.String()
}

// commitMessagePrompt asks the harness to write a commit message for the
// staged changes. instruction is the user's git.commitMessageInstruction
// setting (empty when unset), appended verbatim so it can refine or override
// the base convention below.
func commitMessagePrompt(files []string, stat, diff, instruction string) string {
	var b strings.Builder
	b.WriteString("Write a git commit message for the staged changes below.\n")
	b.WriteString("Rules: imperative mood, a concise summary line under 72 characters, a blank line before an optional body, and explain why rather than just what changed.\n")
	b.WriteString("Output ONLY the commit message text -- no markdown code fences, no preamble, no explanation afterwards, and do not edit any files.\n")
	if instruction != "" {
		fmt.Fprintf(&b, "\nAdditional instructions from the user: %s\n", instruction)
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "\nChanged files (%d):\n", len(files))
		maxFiles := 100
		for i, f := range files {
			if i >= maxFiles {
				fmt.Fprintf(&b, "... and %d more files\n", len(files)-maxFiles)
				break
			}
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	if strings.TrimSpace(stat) != "" {
		b.WriteString("\nSummary of changes (diffstat):\n")
		b.WriteString(stat)
		b.WriteString("\n")
	}
	if strings.TrimSpace(diff) != "" {
		b.WriteString("\nStaged diff:\n")
		b.WriteString(diff)
		b.WriteString("\n")
	}
	return b.String()
}

// ---------------------------------------------------------------- HTTP

func (s *Server) agentOrFail(w http.ResponseWriter) bool {
	if s.agent == nil {
		fail(w, http.StatusNotFound, "editing is not available in this session")
		return false
	}
	return true
}

// handleAgentHarnesses backs the picker. It re-scans on every call so a harness
// installed since startup appears without a restart.
func (s *Server) handleAgentHarnesses(w http.ResponseWriter, r *http.Request) {
	if !s.agentOrFail(w) {
		return
	}
	writeJSON(w, map[string]any{
		"harnesses": s.agent.Detect(),
		"selected":  s.agent.Name(),
		"model":     s.agent.Model(),
		"pinned":    s.agent.Pinned(),
		"settings":  settingsPath(),
	})
}

func (s *Server) handleAgentSelect(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	name := r.URL.Query().Get("name")
	model := r.URL.Query().Get("model")
	if err := s.agent.Select(name, model); err != nil {
		code := 400
		if errors.Is(err, errAgentBusy) {
			code = http.StatusConflict
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"harnesses": s.agent.Detect(),
		"selected":  s.agent.Name(),
		"model":     s.agent.Model(),
		"pinned":    s.agent.Pinned(),
		"settings":  settingsPath(),
	})
}

func (s *Server) handleAgentEdit(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	q := r.URL.Query()
	if q.Get("path") == "" {
		s.handleAgentBatchEdit(w, r)
		return
	}
	abs, rel, ok := s.resolvePath(q.Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	l1, _ := strconv.Atoi(q.Get("l1"))
	l2, _ := strconv.Atoi(q.Get("l2"))

	job, err := s.threads.StartEdit([]agentBatchItem{{Abs: abs, Path: rel, L1: l1, L2: l2, Instruction: q.Get("instruction")}})
	if err != nil {
		code := 400
		if errors.Is(err, errAgentBusy) || errors.Is(err, errAgentDirty) {
			code = http.StatusConflict
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, job)
}

func (s *Server) handleAgentBatchEdit(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, 400, "failed reading body")
		return
	}

	var req struct {
		Edits []struct {
			Path        string `json:"path"`
			L1          int    `json:"l1"`
			L2          int    `json:"l2"`
			Instruction string `json:"instruction"`
		} `json:"edits"`
		Force bool `json:"force"`
	}

	if err := json.Unmarshal(bodyBytes, &req); err != nil || len(req.Edits) == 0 {
		var list []struct {
			Path        string `json:"path"`
			L1          int    `json:"l1"`
			L2          int    `json:"l2"`
			Instruction string `json:"instruction"`
		}
		var single struct {
			Path        string `json:"path"`
			L1          int    `json:"l1"`
			L2          int    `json:"l2"`
			Instruction string `json:"instruction"`
			Force       bool   `json:"force"`
		}
		q := r.URL.Query()
		if err2 := json.Unmarshal(bodyBytes, &list); err2 == nil && len(list) > 0 {
			req.Edits = list
		} else if err3 := json.Unmarshal(bodyBytes, &single); err3 == nil && single.Path != "" {
			req.Edits = []struct {
				Path        string `json:"path"`
				L1          int    `json:"l1"`
				L2          int    `json:"l2"`
				Instruction string `json:"instruction"`
			}{{Path: single.Path, L1: single.L1, L2: single.L2, Instruction: single.Instruction}}
			if single.Force {
				req.Force = true
			}
		} else if qEdits := q.Get("edits"); qEdits != "" {
			if err4 := json.Unmarshal([]byte(qEdits), &req.Edits); err4 != nil || len(req.Edits) == 0 {
				if err5 := json.Unmarshal([]byte(qEdits), &list); err5 == nil && len(list) > 0 {
					req.Edits = list
				}
			}
		} else if q.Get("path") != "" {
			l1, _ := strconv.Atoi(q.Get("l1"))
			l2, _ := strconv.Atoi(q.Get("l2"))
			req.Edits = []struct {
				Path        string `json:"path"`
				L1          int    `json:"l1"`
				L2          int    `json:"l2"`
				Instruction string `json:"instruction"`
			}{{Path: q.Get("path"), L1: l1, L2: l2, Instruction: q.Get("instruction")}}
			if q.Get("force") == "1" {
				req.Force = true
			}
		}

		if len(req.Edits) == 0 {
			fail(w, 400, "invalid or empty batch edits payload")
			return
		}
	}

	items := make([]agentBatchItem, len(req.Edits))
	for i, e := range req.Edits {
		abs, rel, ok := s.resolvePath(e.Path)
		if !ok {
			fail(w, 400, fmt.Sprintf("bad path: %s", e.Path))
			return
		}
		items[i] = agentBatchItem{
			Abs:         abs,
			Path:        rel,
			L1:          e.L1,
			L2:          e.L2,
			Instruction: e.Instruction,
		}
	}

	job, err := s.threads.StartEdit(items)
	if err != nil {
		code := 400
		if errors.Is(err, errAgentBusy) || errors.Is(err, errAgentDirty) {
			code = http.StatusConflict
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, job)
}

// handleAgentJob is polled while an edit runs, once per in-flight compose box.
// px0 dispatched the harness, so it knows when the work ended without
// watching the filesystem for it. id=0 (or missing) means the most recently
// started job.
func (s *Server) handleAgentJob(w http.ResponseWriter, r *http.Request) {
	if !s.agentOrFail(w) {
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	j := s.agent.Job(id)
	if j == nil {
		writeJSON(w, map[string]any{"idle": true})
		return
	}
	writeJSON(w, j)
}

func (s *Server) handleAgentCancel(w http.ResponseWriter, r *http.Request) {
	if !localPost(w, r) {
		return
	}
	if !s.agentOrFail(w) {
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id == 0 {
		var body struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.ID != 0 {
			id = body.ID
		}
	}
	writeJSON(w, map[string]any{"cancelled": s.agent.CancelJob(id)})
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r == '=' || r == ':' || r == ',') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}
