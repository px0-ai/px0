package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// px0 keeps no state inside a workspace. The user's preferences and choices
// are stored with the other per-user files px0 writes (~/.px0/settings.json),
// never in the working tree.

// Settings stores user configuration written to ~/.px0/settings.json (or XDG_CONFIG_HOME/px0/settings.json).
// All settings are optional pointers so omitted values fall back to application defaults.
type Settings struct {
	Agent  string            `json:"agent,omitempty"`
	Models map[string]string `json:"models,omitempty"`

	EditorFontSize              *float64 `json:"editor.fontSize,omitempty"`
	EditorFontFamily            *string  `json:"editor.fontFamily,omitempty"`
	EditorLineHeight            *float64 `json:"editor.lineHeight,omitempty"`
	EditorTabSize               *int     `json:"editor.tabSize,omitempty"`
	EditorWordWrap              *string  `json:"editor.wordWrap,omitempty"`
	EditorLineNumbers           *string  `json:"editor.lineNumbers,omitempty"`
	EditorVimMode               *bool    `json:"editor.vimMode,omitempty"`
	EditorRenderWhitespace      *string  `json:"editor.renderWhitespace,omitempty"`
	EditorMinimapEnabled        *bool    `json:"editor.minimap.enabled,omitempty"`
	WorkbenchColorTheme         *string  `json:"workbench.colorTheme,omitempty"`
	DiffOpenByDefault           *bool    `json:"diff.openByDefault,omitempty"`
	DiffEditorRenderSideBySide  *bool    `json:"diffEditor.renderSideBySide,omitempty"`
	MarkdownPreviewOpen         *bool    `json:"markdown.preview.open,omitempty"`
	TablePreviewOpen            *bool    `json:"table.preview.open,omitempty"`
	TelemetryEnabled            *bool    `json:"telemetry.enabled,omitempty"`
	GitHubToken                 *string  `json:"github.token,omitempty"`
	GitLabToken                 *string  `json:"gitlab.token,omitempty"`
	GitLabHost                  *string  `json:"gitlab.host,omitempty"`
	GiteaToken                  *string  `json:"gitea.token,omitempty"`
	GiteaHost                   *string  `json:"gitea.host,omitempty"`
	BitbucketToken              *string  `json:"bitbucket.token,omitempty"`
	BitbucketEmail              *string  `json:"bitbucket.email,omitempty"`
	GitCommitMessageInstruction *string  `json:"git.commitMessageInstruction,omitempty"`
	ServerBasePath              *string  `json:"server.basePath,omitempty"`
	ExplorerAutoReveal          *bool    `json:"explorer.autoReveal,omitempty"`
	TipsEnabled                 *bool    `json:"tips.enabled,omitempty"`
}

var settingsMu sync.Mutex

// Path mirrors stateFilePath in update.go: honour the XDG location when
// it is set, otherwise fall back to ~/.px0.
func Path() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "px0", "settings.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".px0", "settings.json")
}

// SchemaItem describes a configurable setting for dynamic rendering in the settings modal.
type SchemaItem struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Type        string   `json:"type"` // "string", "number", "boolean", "select"
	Default     any      `json:"default"`
	Options     []string `json:"options,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Step        *float64 `json:"step,omitempty"`
	Secret      bool     `json:"secret,omitempty"` // render as a masked input; still returned in plaintext by /api/settings, same trust model as every other local setting
}

func numPtr(v float64) *float64 { return &v }

var Schema = []SchemaItem{
	{
		Key:         "editor.fontSize",
		Title:       "Font Size",
		Description: "Controls the font size in pixels for the code viewer.",
		Category:    "Text Editor",
		Type:        "number",
		Default:     13.5,
		Min:         numPtr(9.0),
		Max:         numPtr(32.0),
		Step:        numPtr(0.5),
	},
	{
		Key:         "editor.fontFamily",
		Title:       "Font Family",
		Description: "Controls the font family used in the code viewer.",
		Category:    "Text Editor",
		Type:        "string",
		Default:     `"JetBrains Mono", "Fira Code", "Cascadia Code", "SF Mono", Menlo, Consolas, ui-monospace, monospace`,
	},
	{
		Key:         "editor.lineHeight",
		Title:       "Line Height",
		Description: "Controls the line height in pixels for the code viewer.",
		Category:    "Text Editor",
		Type:        "number",
		Default:     21.0,
		Min:         numPtr(14.0),
		Max:         numPtr(48.0),
		Step:        numPtr(1.0),
	},
	{
		Key:         "editor.tabSize",
		Title:       "Tab Size",
		Description: "The number of spaces a tab is equal to.",
		Category:    "Text Editor",
		Type:        "select",
		Default:     4,
		Options:     []string{"2", "4", "8"},
	},
	{
		Key:         "editor.wordWrap",
		Title:       "Word Wrap",
		Description: "Controls whether lines should wrap around or scroll horizontally.",
		Category:    "Text Editor",
		Type:        "select",
		Default:     "on",
		Options:     []string{"on", "off"},
	},
	{
		Key:         "editor.lineNumbers",
		Title:       "Line Numbers",
		Description: "Controls the display of line numbers in the gutter.",
		Category:    "Text Editor",
		Type:        "select",
		Default:     "on",
		Options:     []string{"on", "off"},
	},
	{
		Key:         "editor.vimMode",
		Title:       "Vim Keybindings",
		Description: "Enable Vim modal navigation (Normal mode, Visual mode, motions, search, and LSP shortcuts).",
		Category:    "Text Editor",
		Type:        "boolean",
		Default:     false,
	},
	{
		Key:         "editor.renderWhitespace",
		Title:       "Render Whitespace",
		Description: "Controls how whitespace characters are rendered in the viewer.",
		Category:    "Text Editor",
		Type:        "select",
		Default:     "selection",
		Options:     []string{"none", "boundary", "selection", "all"},
	},
	{
		Key:         "editor.minimap.enabled",
		Title:       "Minimap Hits",
		Description: "Controls whether search hit indicators are shown in the scroll minimap gutter.",
		Category:    "Text Editor",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "workbench.colorTheme",
		Title:       "Color Theme",
		Description: "Specifies the color theme used in the workbench.",
		Category:    "Workbench",
		Type:        "select",
		Default:     "catppuccin-mocha",
		Options: []string{
			"catppuccin-mocha", "catppuccin-latte",
			"github-dark", "dark", "light",
			"dracula", "gruvbox-dark", "gruvbox-light",
			"monokai", "nord", "one-dark", "rose-pine",
			"solarized-dark", "solarized-light",
		},
	},
	{
		Key:         "diff.openByDefault",
		Title:       "Open Changed Files in Diff View",
		Description: "Open changed files in diff view by default instead of the file view.",
		Category:    "Git & Diff",
		Type:        "boolean",
		Default:     false,
	},
	{
		Key:         "diffEditor.renderSideBySide",
		Title:       "Diff Side By Side",
		Description: "Controls whether the diff editor shows changes in split (side-by-side) or unified mode.",
		Category:    "Workbench",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "markdown.preview.open",
		Title:       "Markdown Preview",
		Description: "Controls whether Markdown files open in rendered preview by default.",
		Category:    "Workbench",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "table.preview.open",
		Title:       "Table View",
		Description: "Controls whether CSV and TSV files open as a table by default.",
		Category:    "Workbench",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "editor.cursorStyle",
		Title:       "Cursor Style",
		Description: "Controls the cursor style in the code viewer.",
		Category:    "Text Editor",
		Type:        "select",
		Default:     "line",
		Options:     []string{"line", "block", "underline"},
	},
	{
		Key:         "editor.cursorBlinking",
		Title:       "Cursor Blinking",
		Description: "Controls the cursor animation style.",
		Category:    "Text Editor",
		Type:        "select",
		Default:     "smooth",
		Options:     []string{"blink", "smooth", "solid"},
	},
	{
		Key:         "editor.renderLineHighlight",
		Title:       "Render Line Highlight",
		Description: "Controls how the editor should render the current line highlight.",
		Category:    "Text Editor",
		Type:        "select",
		Default:     "line",
		Options:     []string{"line", "none"},
	},
	{
		Key:         "editor.occurrencesHighlight",
		Title:       "Occurrences Highlight",
		Description: "Controls whether the editor should highlight occurrences of the selected word.",
		Category:    "Text Editor",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "editor.scrollBeyondLastLine",
		Title:       "Scroll Beyond Last Line",
		Description: "Controls whether the editor will scroll beyond the last line of the file.",
		Category:    "Text Editor",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "editor.bracketPairColorization",
		Title:       "Bracket Pair Colorization",
		Description: "Controls whether bracket pair colorization and matching is enabled.",
		Category:    "Text Editor",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "explorer.compactFolders",
		Title:       "Compact Folders",
		Description: "Controls whether the file tree renders single-child directory chains compactly.",
		Category:    "Files & Explorer",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "explorer.autoReveal",
		Title:       "Auto Reveal",
		Description: "Controls whether the file explorer automatically scrolls to and reveals active tabs.",
		Category:    "Files & Explorer",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "files.exclude",
		Title:       "Files Exclude Patterns",
		Description: "Configure glob patterns for excluding files and folders from search and trees.",
		Category:    "Files & Explorer",
		Type:        "string",
		Default:     "**/.git, **/node_modules, **/target, **/.DS_Store",
	},
	{
		Key:         "search.smartCase",
		Title:       "Smart Case Search",
		Description: "Searches case-insensitively when query is lowercase, and case-sensitively when uppercase characters exist.",
		Category:    "Search",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "search.maxResults",
		Title:       "Max Search Results",
		Description: "Controls the maximum number of results returned in workspace-wide searches.",
		Category:    "Search",
		Type:        "number",
		Default:     1000.0,
		Min:         numPtr(50.0),
		Max:         numPtr(10000.0),
		Step:        numPtr(50.0),
	},
	{
		Key:         "diffEditor.ignoreTrimWhitespace",
		Title:       "Diff: Ignore Trim Whitespace",
		Description: "Controls whether the diff viewer ignores changes in leading or trailing whitespace.",
		Category:    "Git & Diff",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "git.gutterIndicators",
		Title:       "Git Gutter Indicators",
		Description: "Controls whether changed line indicators are shown in the editor gutter.",
		Category:    "Git & Diff",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "lsp.enabled",
		Title:       "Language Server Protocol (LSP)",
		Description: "Master switch for language server integrations (definitions, references, diagnostics).",
		Category:    "LSP & Intelligence",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "lsp.hover.enabled",
		Title:       "Hover Documentation",
		Description: "Controls whether hovercards with documentation and type signatures appear on hover.",
		Category:    "LSP & Intelligence",
		Type:        "boolean",
		Default:     true,
	},
	{
		Key:         "agent.harness",
		Title:       "Coding Harness",
		Description: "Coding agent harness invoked for code edits (e.g. claude, gemini, cursor-agent, agy, opencode, codex, aider, goose).",
		Category:    "Agent / AI",
		Type:        "string",
		Default:     "",
	},
	{
		Key:         "agent.timeoutSeconds",
		Title:       "Agent Timeout (Seconds)",
		Description: "Controls the maximum execution time in seconds for agent edits before canceling.",
		Category:    "Agent / AI",
		Type:        "number",
		Default:     120.0,
		Min:         numPtr(10.0),
		Max:         numPtr(600.0),
		Step:        numPtr(10.0),
	},
	{
		Key:         "agent.autoAcceptEdits",
		Title:       "Auto Accept Agent Edits",
		Description: "Controls whether agent-generated code diffs are accepted without manual confirmation.",
		Category:    "Agent / AI",
		Type:        "boolean",
		Default:     false,
	},
	{
		Key:         "git.commitMessageInstruction",
		Title:       "Commit Message Instructions",
		Description: "Extra instructions given to the coding harness when it writes a commit message for the staged diff (e.g. \"Follow Conventional Commits\" or \"Reference the ticket number in the branch name\").",
		Category:    "Git & Diff",
		Type:        "textarea",
		Default:     "",
	},
	{
		Key:         "github.token",
		Title:       "GitHub Token",
		Description: "Personal access token used to check out and review pull requests (px0 <url>). Takes precedence over the GITHUB_TOKEN environment variable and 'gh auth token'.",
		Category:    "GitHub",
		Type:        "string",
		Default:     "",
		Secret:      true,
	},
	{
		Key:         "gitlab.token",
		Title:       "GitLab Token",
		Description: "Personal access token (api scope) used to check out, review and merge GitLab merge requests, on gitlab.com or a self-hosted instance. Sent only to the GitLab Host below; other self-hosted instances use the token 'glab auth login' stored for them. Takes precedence over the GITLAB_TOKEN environment variable.",
		Category:    "GitLab",
		Type:        "string",
		Default:     "",
		Secret:      true,
	},
	{
		Key:         "gitlab.host",
		Title:       "GitLab Host",
		Description: "The GitLab instance the GitLab token belongs to (e.g. gitlab.example.com). Merge requests on any other host are opened without it. Falls back to GITLAB_HOST, then gitlab.com.",
		Category:    "GitLab",
		Type:        "string",
		Default:     "",
	},
	{
		Key:         "gitea.token",
		Title:       "Gitea Token",
		Description: "Access token (repository and issue read/write scopes) for Gitea, Forgejo or Codeberg pull request review. Sent only to the Gitea Host below. Takes precedence over the GITEA_TOKEN environment variable.",
		Category:    "Gitea",
		Type:        "string",
		Default:     "",
		Secret:      true,
	},
	{
		Key:         "gitea.host",
		Title:       "Gitea Host",
		Description: "The Gitea or Forgejo instance the Gitea token belongs to (e.g. gitea.example.com). Pull requests on any other host are opened without it. Falls back to GITEA_HOST, then codeberg.org.",
		Category:    "Gitea",
		Type:        "string",
		Default:     "",
	},
	{
		Key:         "bitbucket.token",
		Title:       "Bitbucket Token",
		Description: "Bitbucket Cloud token for pull request review: a repository, project or workspace access token, or an Atlassian API token (then also set Bitbucket Email). Takes precedence over the BITBUCKET_TOKEN environment variable.",
		Category:    "Bitbucket",
		Type:        "string",
		Default:     "",
		Secret:      true,
	},
	{
		Key:         "bitbucket.email",
		Title:       "Bitbucket Email",
		Description: "The Atlassian account email an API token belongs to. Leave empty when Bitbucket Token is an access token. Falls back to BITBUCKET_EMAIL. Checking push access (and so Approve and Merge) needs an API token.",
		Category:    "Bitbucket",
		Type:        "string",
		Default:     "",
	},
	{
		Key:         "server.basePath",
		Title:       "Base Path",
		Description: "Base URL path prefix for the px0 server and web interface (e.g. /rev-123/).",
		Category:    "Server",
		Type:        "string",
		Default:     "/",
	},
	{
		Key:         "tips.enabled",
		Title:       "Show Tips in Interface",
		Description: "Controls whether px0 periodically shows helpful tips and tricks in the interface.",
		Category:    "General",
		Type:        "boolean",
		Default:     true,
	},
}

func DefaultMap() map[string]any {
	res := make(map[string]any, len(Schema)+3)
	for _, item := range Schema {
		res[item.Key] = item.Default
	}
	res["explorer.autoRelveal"] = true
	res["agent"] = ""
	res["models"] = map[string]string{}
	return res
}

// ReadRawMap returns the raw JSON contents unmarshaled into a map.
// It never fails: a missing or corrupt file returns an empty map.
func ReadRawMap() map[string]any {
	p := Path()
	if p == "" {
		return map[string]any{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// Read never fails: a missing or corrupt file simply means no choice
// has been made yet, which is the same as a fresh install.
func Read() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return readSettingsLocked()
}

func readSettingsLocked() Settings {
	var s Settings
	p := Path()
	if p == "" {
		return s
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		return s
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil || len(raw) == 0 {
		return s
	}

	// Unmarshal known typed fields directly from data without re-marshaling raw
	_ = json.Unmarshal(data, &s)

	// Bi-directional bridge between agent <-> agent.harness
	if s.Agent == "" {
		if h, ok := raw["agent.harness"].(string); ok && h != "" {
			s.Agent = h
		}
	}
	// Support server.basePath and basePath fallback
	if s.ServerBasePath == nil {
		if bp, ok := raw["server.basePath"].(string); ok && bp != "" {
			s.ServerBasePath = &bp
		} else if bp, ok := raw["basePath"].(string); ok && bp != "" {
			s.ServerBasePath = &bp
		}
	}
	// Bi-directional bridge between models <-> agent.models
	if s.Models == nil || len(s.Models) == 0 {
		if am, ok := raw["agent.models"].(map[string]any); ok {
			s.Models = make(map[string]string, len(am))
			for k, v := range am {
				if vs, ok := v.(string); ok {
					s.Models[k] = vs
				}
			}
		}
	}
	// Support explorer.autoReveal and explorer.autoRelveal
	if s.ExplorerAutoReveal == nil {
		if ar, ok := raw["explorer.autoReveal"].(bool); ok {
			s.ExplorerAutoReveal = &ar
		} else if ar, ok := raw["explorer.autoRelveal"].(bool); ok {
			s.ExplorerAutoReveal = &ar
		} else if ar, ok := raw["autoReveal"].(bool); ok {
			s.ExplorerAutoReveal = &ar
		} else if ar, ok := raw["autoRelveal"].(bool); ok {
			s.ExplorerAutoReveal = &ar
		}
	}

	return s
}

// ReadMergedMap returns all settings, overlaying stored settings onto defaults.
func ReadMergedMap() map[string]any {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	res := DefaultMap()
	raw := ReadRawMap()

	for k, v := range raw {
		res[k] = v
	}

	// Synchronize agent / agent.harness
	if ag, ok := raw["agent"].(string); ok && ag != "" {
		res["agent.harness"] = ag
	} else if ah, ok := raw["agent.harness"].(string); ok && ah != "" {
		res["agent"] = ah
	}

	// Synchronize models / agent.models
	if m, ok := raw["models"].(map[string]any); ok && len(m) > 0 {
		res["agent.models"] = m
	} else if am, ok := raw["agent.models"].(map[string]any); ok && len(am) > 0 {
		res["models"] = am
	}

	// Synchronize server.basePath / basePath
	if bp, ok := raw["server.basePath"].(string); ok && bp != "" {
		res["server.basePath"] = bp
	} else if bp, ok := raw["basePath"].(string); ok && bp != "" {
		res["server.basePath"] = bp
	}

	// Synchronize explorer.autoReveal / explorer.autoRelveal
	if ar, ok := raw["explorer.autoReveal"].(bool); ok {
		res["explorer.autoReveal"] = ar
		res["explorer.autoRelveal"] = ar
	} else if ar, ok := raw["explorer.autoRelveal"].(bool); ok {
		res["explorer.autoReveal"] = ar
		res["explorer.autoRelveal"] = ar
	} else if ar, ok := raw["autoReveal"].(bool); ok {
		res["explorer.autoReveal"] = ar
		res["explorer.autoRelveal"] = ar
	} else if ar, ok := raw["autoRelveal"].(bool); ok {
		res["explorer.autoReveal"] = ar
		res["explorer.autoRelveal"] = ar
	} else {
		res["explorer.autoReveal"] = true
		res["explorer.autoRelveal"] = true
	}

	return res
}

// ReadRawJSON returns formatted settings.json file content as string.
func ReadRawJSON() string {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	p := Path()
	if p == "" {
		return "{}\n"
	}
	data, err := os.ReadFile(p)
	if err != nil || len(data) == 0 {
		return "{\n}\n"
	}
	// Pretty format if possible
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err == nil {
		if formatted, err := json.MarshalIndent(raw, "", "  "); err == nil {
			return string(formatted) + "\n"
		}
	}
	return string(data)
}

// Write saves the agent and models choices while preserving other settings.
func Write(s Settings) error {
	p := Path()
	if p == "" {
		return errors.New("no home directory to save settings in")
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()

	raw := ReadRawMap()
	if s.Agent != "" {
		raw["agent"] = s.Agent
		raw["agent.harness"] = s.Agent
	} else {
		delete(raw, "agent")
		delete(raw, "agent.harness")
	}

	if s.Models != nil && len(s.Models) > 0 {
		raw["models"] = s.Models
		raw["agent.models"] = s.Models
	} else if s.Agent == "" {
		delete(raw, "models")
		delete(raw, "agent.models")
	}

	return writeRawMapLocked(p, raw)
}

// UpdateMap merges key-value pairs into settings.json without losing existing keys.
func UpdateMap(updates map[string]any) error {
	p := Path()
	if p == "" {
		return errors.New("no home directory to save settings in")
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()

	raw := ReadRawMap()
	for k, v := range updates {
		if v == nil {
			delete(raw, k)
		} else {
			raw[k] = v
		}

		// Keep agent / agent.harness in sync
		if k == "agent" {
			if v == nil || v == "" {
				delete(raw, "agent.harness")
			} else {
				raw["agent.harness"] = v
			}
		} else if k == "agent.harness" {
			if v == nil || v == "" {
				delete(raw, "agent")
			} else {
				raw["agent"] = v
			}
		}

		// Keep models / agent.models in sync
		if k == "models" {
			if v == nil {
				delete(raw, "agent.models")
			} else {
				raw["agent.models"] = v
			}
		} else if k == "agent.models" {
			if v == nil {
				delete(raw, "models")
			} else {
				raw["models"] = v
			}
		}

		// Keep server.basePath / basePath in sync
		if k == "server.basePath" {
			if v == nil || v == "" {
				delete(raw, "server.basePath")
				delete(raw, "basePath")
			} else {
				raw["server.basePath"] = v
			}
		} else if k == "basePath" {
			if v == nil || v == "" {
				delete(raw, "server.basePath")
				delete(raw, "basePath")
			} else {
				raw["server.basePath"] = v
				raw["basePath"] = v
			}
		}

		// Keep explorer.autoReveal / explorer.autoRelveal in sync
		if k == "explorer.autoReveal" || k == "explorer.autoRelveal" || k == "autoReveal" || k == "autoRelveal" {
			if v == nil {
				delete(raw, "explorer.autoReveal")
				delete(raw, "explorer.autoRelveal")
				delete(raw, "autoReveal")
				delete(raw, "autoRelveal")
			} else {
				raw["explorer.autoReveal"] = v
				raw["explorer.autoRelveal"] = v
			}
		}
	}

	return writeRawMapLocked(p, raw)
}

// SaveRawJSON parses and validates raw JSON text and writes it formatted.
func SaveRawJSON(rawJSON []byte) error {
	var m map[string]any
	if err := json.Unmarshal(rawJSON, &m); err != nil {
		return err
	}

	p := Path()
	if p == "" {
		return errors.New("no home directory to save settings in")
	}
	settingsMu.Lock()
	defer settingsMu.Unlock()

	// Sync agent bridges if present
	if ag, ok := m["agent"].(string); ok && ag != "" {
		m["agent.harness"] = ag
	} else if ah, ok := m["agent.harness"].(string); ok && ah != "" {
		m["agent"] = ah
	}

	return writeRawMapLocked(p, m)
}

func writeRawMapLocked(p string, raw map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}
