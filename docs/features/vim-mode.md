# Vim Keybindings & Modal Navigation

px0 includes an optional Vim modal navigation mode. When enabled, it provides standard modal navigation—Normal mode, Visual mode, and intuitive motions—allowing engineers accustomed to Vim, Neovim, or Helix to navigate documents entirely from the home row.

---

## Overview & Core Purpose

Many developers maintain decades of muscle memory built around modal text editing. Moving your hand away from the keyboard to grab a mouse or repeatedly pressing arrow keys disrupts cognitive momentum.

px0's Vim mode brings the speed and ergonomics of modal motions into the code viewer. Because px0 is focused on code reading and auditing, Vim mode focuses on navigation, scrolling, line jumping, and range selection. When you highlight text in Visual mode, the selection immediately bridges into px0's context actions—allowing you to press `Alt+C` to copy a reference, `Alt+A` to copy formatted context for an LLM, or `Alt+E` to dispatch an edit to your coding agent.

---

## Supported Modes & Keybindings

### 1. Normal Mode (Navigation)
Active by default when Vim mode is enabled.
- **Directional Navigation**: `h` (left), `j` (down), `k` (up), `l` (right).
- **Word Motions**:
  - `w`: Move forward to start of next word.
  - `b`: Move backward to start of previous word.
  - `e`: Move forward to end of current word.
- **Line Motions**:
  - `0`: Jump to beginning of line.
  - `^`: Jump to first non-whitespace character.
  - `$`: Jump to end of line.
- **Document Jumps**:
  - `gg`: Jump to top of file (line 1).
  - `G`: Jump to bottom of file.
  - `[count]G` or `:[count]`: Jump directly to line number.
- **Half-Page & Page Scrolling**:
  - `Ctrl+u`: Scroll half page up.
  - `Ctrl+d`: Scroll half page down.
  - `Ctrl+b`: Scroll full page up.
  - `Ctrl+f`: Scroll full page down.
- **In-File Search**:
  - `/`: Start in-file search.
  - `n`: Advance to next search match.
  - `N`: Step backward to previous search match.

### 2. Visual Mode (Selection)
- Press **`v`** to enter character-wise Visual mode.
- Press **`V`** to enter line-wise Visual mode.
- Move the caret using any motion (`j`, `k`, `w`, `$`, etc.) to extend the selection.
- Press **`Esc`** to collapse selection and return to Normal mode.
- While text is selected in Visual mode:
  - `y`: Yank (copy) selected text to clipboard.
  - `Alt+C`: Copy reference pointer (`path#L10-L25`).
  - `Alt+A`: Copy formatted context for AI prompts.
  - `Alt+E`: Open coding agent composer on selected range.

---

## Enabling Vim Mode

Vim mode can be enabled in Settings:
1. Press **`Cmd/Ctrl+,`** to open Settings.
2. Locate **Editor: Vim Mode** (`editor.vimMode`).
3. Click the `[true]` pill button to activate it immediately.

Alternatively, set `"editor.vimMode": true` in `~/.px0/settings.json`.

---

## Status Bar Indicator

When Vim mode is active, the left side of the bottom status bar renders a distinct mode pill:
- `-- NORMAL --`
- `-- VISUAL --`
- `-- VISUAL LINE --`

This provides constant visual feedback of the active modal state.
