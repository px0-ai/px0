# Fuzzy File Search & Quick Open

Fuzzy file search is px0's primary mechanism for rapid, keyboard-driven file navigation. Pressing `Cmd/Ctrl+P` or `Cmd/Ctrl+K` brings up an instant overlay that allows you to jump to any file across tens of thousands of repository paths using minimal keystrokes.

---

## Overview & Core Purpose

In large-scale repositories and monorepos, expanding and hunting through deep directory hierarchies in a sidebar tree creates friction and disrupts focus. Developers frequently know part of a filename or path fragment (such as `user_test`, `auth/tok`, or `api.v2`) and want to navigate there immediately.

Fuzzy file search transforms repository navigation into a sub-millisecond keyboard interaction. Whether working in a small library of 100 files or a monorepo containing over 90,000 files (such as the Linux kernel), the fuzzy picker renders matching paths instantaneously as you type. Matches are intelligently weighted so that active files, exact basename matches, and camelCase or snake_case abbreviations bubble straight to the top.

---

## Key Capabilities

- **Sub-Millisecond Search Latency**: Searches over 60,000 files in under 6 ms. Results appear keystroke-by-keystroke without perceptible delay or typing lag.
- **Intelligent Scoring Matrix**: Matches are ranked based on path boundaries:
  - Exact prefix and whole-basename matches receive top priority.
  - Matches immediately following path separators (`/`), underscores (`_`), hyphens (`-`), or camelCase transitions are strongly rewarded.
  - Scattered or distant character matches are ranked lower to keep results intuitive and relevant.
- **Tab History & Recency Bias**: Files that you have previously opened or switched between receive an automatic priority boost, ensuring that frequently visited files appear after typing just one or two characters.
- **Path-Aware Filtering**: Typing directory fragments alongside filenames (e.g., `pkg/srv/cfg`) filters across the full directory hierarchy, allowing you to disambiguate identical filenames residing in different packages.
- **Matched Substring Highlighting**: The specific characters that satisfied your query are highlighted directly in the results list, making it immediately clear why each file matched.
- **Unified Quick Open & Command Palette**: `Cmd/Ctrl+P` opens the file finder directly. If you start your query with `>`, the picker smoothly transitions into the px0 Command Palette to execute workbench actions.

---

## Developer Workflows & Productivity Value

### Rapid Context Switching
When reading code or debugging an issue, you often need to jump between an implementation file and its corresponding unit test (e.g., `server.go` and `server_test.go`). With fuzzy search, pressing `Cmd/Ctrl+P` followed by `st` or `srv_t` immediately brings up the test file.

### Disambiguating Duplicate Filenames
In modern web applications or microservices architectures, repositories often have dozens of files named `index.ts`, `mod.rs`, or `types.go`. Typing only `index` produces an overwhelming list. In px0, typing `auth/idx` or `billing/types` isolates the exact module in a fraction of a second.

### Keyboard-First Ergonomics
The file picker is fully accessible from the keyboard:
1. Press `Cmd/Ctrl+P`.
2. Type a short abbreviation of the target file.
3. Use the `Up` and `Down` arrow keys to cycle through candidates.
4. Press `Enter` to open the file in the viewer.
5. Press `Esc` at any point to dismiss the modal and return to your exact caret position.

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `Cmd/Ctrl+P` | Global | Open Fuzzy File Picker |
| `Cmd/Ctrl+K` | Global | Open Universal Quick Open |
| `Up` / `Down` | In Picker | Navigate highlighted candidates |
| `Enter` | In Picker | Open selected file in active tab |
| `Esc` | In Picker | Close picker and return focus to editor |
| `>` (First character) | In Picker | Switch to Command Palette mode |

---

## Configuration & Exclusions

Fuzzy search respects your repository's `.gitignore` rules automatically. Files and directories excluded by `.gitignore` (such as `node_modules`, `target`, `vendor`, or `.git`) are never indexed or matched, keeping the search list clean and relevant.

You can further refine exclusions in your user settings:
- Open Settings via `Cmd/Ctrl+,`.
- Under **Files: Exclude** (`files.exclude`), add custom glob patterns to filter out build artifacts, temporary logs, or data dumps across all searches.

---

## Technical Architecture Deep Dive

For an explanation of the two-pass scoring algorithm, dynamic programming ranking matrix, and bounded parallel query slicing that power this feature, see [Fuzzy Path Matching Internals](../internals/fuzzy-search.md).
