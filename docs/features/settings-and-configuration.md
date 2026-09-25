# Settings & Configuration System

px0 includes a built-in visual Settings manager modeled after VS Code. Accessible via `Cmd/Ctrl+,` or the gear icon in the status bar, it enables complete customization of editor ergonomics, typography, themes, diff views, and coding agents with live real-time synchronization.

---

## Overview & Core Purpose

Developer environments are deeply personal: engineers have strong preferences regarding font sizes, line heights, font families, cursor blink styles, tab spacing, and keybindings. However, managing configuration files manually in text editors or dealing with fragmented config directories can be cumbersome. Furthermore, storing settings inside a workspace repository risks polluting Git commits with personal editor preferences.

px0 stores all configuration in a single per-user global file (`~/.px0/settings.json` or `$XDG_CONFIG_HOME/px0/settings.json`), completely isolated from your repository files. The visual Settings editor offers a dual-mode experience: an intuitive graphical form with interactive pill buttons for quick toggling, and a synchronized raw JSON editor with schema validation. Changes apply immediately in real time without refreshing the browser.

---

## Key Capabilities

- **Dual UI & Raw JSON Modes**: Switch instantly between the graphical form editor and raw JSON mode with a single click. Changes made in either view synchronize bidirectionally in real time.
- **Interactive Attribute Pills**: Every setting displays metadata badges (category, type, default value) along with interactive pill buttons for quick selection (e.g., `[line]`, `[block]`, `[underline]` for cursor styles; `[true]`, `[false]` for boolean toggles). Clicking any pill applies that value immediately.
- **Instant Live Preview Without Reload**: Adjusting font sizes, line heights, themes, cursor animations, or diff modes takes effect immediately across all open tabs without requiring a page reload.
- **One-Click Factory Reset**: Any setting that has been customized displays an amber `Modified` badge and an inline `Reset` button, allowing you to restore default settings individually.
- **Clean Workspace Separation**: All configuration lives in your user directory. Your repository working trees, `.git` configs, and tracked files remain 100% pristine.

---

## Key Configurable Settings

| Setting Key | Category | Default | Allowed Values / Range | Description |
| :--- | :--- | :--- | :--- | :--- |
| `editor.fontSize` | Text Editor | `13.5` | `9.0` – `32.0` (px) | Font size in the code viewer |
| `editor.fontFamily` | Text Editor | JetBrains Mono stack | CSS font stack string | Font family stack for viewer |
| `editor.lineHeight` | Text Editor | `21.0` | `14.0` – `48.0` (px) | Line height for viewer rows |
| `editor.tabSize` | Text Editor | `4` | `2`, `4`, `8` | Number of spaces per tab |
| `editor.wordWrap` | Text Editor | `"on"` | `"on"`, `"off"` | Soft line wrapping at editor boundary |
| `editor.lineNumbers` | Text Editor | `"on"` | `"on"`, `"off"` | Line numbers display in gutter |
| `editor.cursorStyle` | Text Editor | `"line"` | `"line"`, `"block"`, `"underline"` | Cursor rendering style |
| `editor.cursorBlinking` | Text Editor | `"smooth"` | `"blink"`, `"smooth"`, `"solid"` | Cursor blinking animation style |
| `editor.renderLineHighlight` | Text Editor | `"line"` | `"line"`, `"none"` | Highlight style for the active line |
| `editor.occurrencesHighlight` | Text Editor | `true` | `true`, `false` | Highlight matches of selected identifier |
| `editor.scrollBeyondLastLine` | Text Editor | `true` | `true`, `false` | Enable scrolling beyond document end |
| `editor.bracketPairColorization` | Text Editor | `true` | `true`, `false` | Rainbow bracket pairs & matching |
| `editor.vimMode` | Text Editor | `false` | `true`, `false` | Modal Vim navigation keybindings |
| `workbench.colorTheme` | Workbench | `"github-dark"` | 14 built-in theme IDs | Active color theme |
| `diffEditor.renderSideBySide` | Diff Editor | `true` | `true`, `false` | Split vs. unified diff view default |
| `diffEditor.ignoreTrimWhitespace` | Diff Editor | `true` | `true`, `false` | Ignore whitespace differences in diffs |
| `git.gutterIndicators` | Git | `true` | `true`, `false` | Visual change markers in gutter |
| `git.commitMessageInstruction` | Git & Diff | `""` | any string (multi-line textarea) | Extra instructions given to the coding harness when the git panel's **Commit with AI** writes a commit message |
| `explorer.compactFolders` | Explorer | `true` | `true`, `false` | Compact single-child directory chains |
| `explorer.autoReveal` | Explorer | `true` | `true`, `false` | Auto-scroll to active file in tree |
| `files.exclude` | Files | Default globs | Array of glob patterns | Exclude patterns from trees and searches |
| `search.smartCase` | Search | `true` | `true`, `false` | Case-insensitive unless query has uppercase |
| `search.maxResults` | Search | `1000` | `50` – `10,000` | Maximum search results returned |
| `lsp.enabled` | LSP | `true` | `true`, `false` | Master toggle for Language Servers |
| `lsp.hover.enabled` | LSP | `true` | `true`, `false` | Hover documentation cards |
| `agent.harness` | Coding Agent | `""` | `claude`, `gemini`, `agy`, etc. | Preferred CLI coding harness |
| `agent.timeoutSeconds` | Coding Agent | `120` | `10` – `600` (seconds) | Max runtime for agent edits |
| `github.token` | GitHub | `""` | any string | Personal access token for `px0 pr` review; takes precedence over `GITHUB_TOKEN` and `gh auth token`. Masked in the Settings UI. |
| `server.basePath` | Server | `"/"` | any path prefix (e.g. `"/rev-123/"`) | Base URL path prefix to serve endpoints and assets from. Overridden by the `-base-path` CLI flag. |

---

## Accessing Settings

- **Keyboard Shortcut**: Press **`Cmd+,`** (macOS) or **`Ctrl+,`** (Linux/Windows).
- **Status Bar**: Click the **⚙️ Settings** icon in the bottom-right corner.
- **Command Palette**: Press `Cmd/Ctrl+Shift+P` and choose:
  - `Preferences: Open Settings (UI)`
  - `Preferences: Open Settings (JSON)`

---

## Direct JSON File Configuration

For automated machine setup, dotfile repositories, or scripting, you can directly edit the JSON configuration file:

```json
{
  "editor.fontSize": 14,
  "editor.fontFamily": "\"JetBrains Mono\", monospace",
  "editor.lineHeight": 22,
  "workbench.colorTheme": "tokyo-night",
  "diffEditor.renderSideBySide": true,
  "agent.harness": "claude",
  "agent.timeoutSeconds": 180
}
```

The file is stored at:
- `~/.px0/settings.json`
- Or `$XDG_CONFIG_HOME/px0/settings.json` (if `XDG_CONFIG_HOME` is set).
