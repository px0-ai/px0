# Selection Toolbar & Context Actions

px0 features a dedicated footer selection toolbar and contextual right-click menu designed specifically for AI-assisted engineering and code review. When code is selected, px0 replaces standard footer clutter with purposeful actions: Copy Reference (`Alt+C`), Copy for Agent (`Alt+A`), Find Usages (`Alt+U`), Edit with Agent (`Alt+E`), and Start Thread (`Alt+T`, see [Threads](threads.md)).

---

## Overview & Core Purpose

In typical developer workflows, sharing code snippets with teammates or pasting code into external AI chat interfaces (such as ChatGPT, Claude, or terminal coding agents) involves repetitive manual labor: selecting lines, copying them, manually writing down the file path and line numbers, and formatting markdown code fences.

px0 turns code selection into a high-ergonomics launchpad. Instead of popping up obstructive floating tooltips that obscure adjacent lines of code, px0 smoothly transitions the left section of the fixed bottom status bar into an action bar the moment text is highlighted. The same actions are simultaneously accessible via a clean right-click context menu and direct keyboard shortcuts.

---

## Key Actions & Capabilities

### 1. Copy Reference (`Alt+C`)
Copies a concise, standardized pointer to the selected code, formatted as:
`path/to/file.go#L42-L68` (or `path/to/file.go:42` for a single line).
- **Practical Use**: Paste directly into pull request review comments, Slack messages, or GitHub issues so teammates can immediately open the exact lines.

### 2. Copy for Agent / Copy with Context (`Alt+A`)
Copies a rich, self-contained Markdown block specifically structured for AI models and LLM prompts:
- Includes the full repository-relative file path.
- Includes exact line number markers for every row.
- Encloses the snippet in language-tagged fenced code blocks.
- Provides surrounding context lines so the receiving AI model understands variable scope, indentation, and parent functions without asking for clarification.

### 3. Find Usages (`Alt+U`)
Immediately searches the workspace for all occurrences and references of the selected symbol, opening the results cleanly in the right-hand Inspector pane.

### 4. Edit with Agent (`Alt+E`)
Opens the inline coding agent composer directly above the selection, allowing you to instruct an AI coding agent (Claude Code, Gemini CLI, Cursor Agent, Antigravity, etc.) to modify the highlighted code in place.

---

## Split Diff Selection Intelligence

When reviewing changes in the split (side-by-side) git diff viewer, selecting code on either the left (original `HEAD`) or right (working tree) side requires special care. Traditional editors sweep up line numbers, diff markers (`+`/`-`), and column gutters into the clipboard.

px0 automatically normalizes diff selections:
- Gutter line numbers and diff signs are cleanly excluded.
- The selected text is accurately mapped to working-tree coordinates.
- Copying or dispatching an agent edit from a diff operates directly on the underlying files on disk.

---

## Keyboard Shortcuts & Interaction Matrix

| Action | Shortcut | Context Menu | Status Bar Button | Description |
| :--- | :--- | :--- | :--- | :--- |
| **Copy Reference** | `Alt+C` | Right-Click | `[Copy Ref]` | Copies `path#L10-L25` pointer |
| **Copy for Agent** | `Alt+A` | Right-Click | `[Copy Context]` | Copies formatted markdown with lines & context |
| **Find Usages** | `Alt+U` | Right-Click | `[Find Usages]` | Finds references across workspace |
| **Edit with Agent** | `Alt+E` | Right-Click | `[Edit Agent]` | Opens agent edit composer |
| **Start Thread** | `Alt+T` | Right-Click | `[Thread]` | Opens a long-running conversation anchored to the selection |

### Line Actions

Hovering a line number shows a thread icon. Clicking it opens the same menu as a right click, aimed at that line: Start Thread, Edit Inline, Copy Ref and Copy with Context (plus Add Review Comment on a pull request diff line).

---

## Non-Intrusive Ergonomics

- **No Code Occlusion**: Floating toolbars often pop up directly over the line above or below your selection, hiding the very code you are trying to read. px0 mounts action buttons in the bottom status bar, leaving the editor viewport 100% unobstructed.
- **Automatic State Restoration**: As soon as you click elsewhere or collapse the selection, the status bar smoothly restores normal file coordinates, line/column counters, and Git branch details.
