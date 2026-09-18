# Git Awareness & Visual Diff Viewer

px0 includes built-in Git awareness and an interactive visual diff viewer. It highlights working-tree modifications across your file tree and editor gutters, and lets you toggle between source code and an interactive side-by-side or unified diff against `HEAD` with `Cmd/Ctrl+D`.

---

## Overview & Core Purpose

In modern software engineering, coding agents, background formatters, and compilers continuously generate or modify files on disk. Developers spend a significant portion of their time verifying what changed, ensuring unintended edits were not introduced, and auditing modifications prior to staging or committing.

px0 provides non-destructive, zero-latency Git awareness. It queries Git status asynchronously in the background without staging files, mutating index locks, or slowing down viewer startup. With visual badges, ancestor dirty propagation, gutter indicators, and full split/unified diffs, you can review changes with complete confidence without leaving the browser.

---

## Key Capabilities

- **File Tree Status Badges**: The file explorer decorates changed files with colored badges indicating their Git working-tree status:
  - `M` (Modified): Working tree file differs from `HEAD`.
  - `A` (Added / Staged): Newly added file staged in the index.
  - `D` (Deleted): File removed from the working tree.
  - `U` (Untracked): New file not yet tracked by Git.
  - `R` (Renamed): File renamed or moved.
- **Dirty Ancestor Folder Propagation**: When a nested file is modified (e.g., `src/core/auth/token.go`), all parent directories in the tree (`auth/`, `core/`, `src/`) display a subtle dirty indicator badge. This allows you to spot modifications even when folder branches are collapsed.
- **Uncommitted Changes Filter**: A dedicated toggle in the explorer header lets you collapse all clean files and view only files that currently have uncommitted changes.
- **Visual Gutter Diff Indicators**: The code viewer gutter places colored indicator bars alongside line numbers to mark edits in real time:
  - Green bar for added lines.
  - Blue bar for modified lines.
  - Red triangle or marker for deleted lines.
- **Interactive Diff Viewer (`Cmd/Ctrl+D`)**: Toggle between normal source view and full Git diff with a single keystroke.
- **Side-by-Side & Unified Diff Modes**:
  - **Side-by-Side (Split)**: View original `HEAD` code on the left and active working-tree code on the right with synchronized scrolling.
  - **Unified**: View changes inline with consecutive additions and deletions.
- **Whitespace Diff Filtering**: Toggle whitespace trimming to hide trivial indentation and trailing space differences when reviewing significant logic changes.
- **Direct Agent Editing from Diffs**: Select any modified or added line in the diff view and trigger an AI agent edit (`Alt+E`) to refine or correct the change on the spot.

---

## Developer Workflows & Practical Value

### Auditing AI Agent Edits
When an AI coding agent finishes updating a component or fixing a bug:
1. Glance at the file explorer to see which files were touched.
2. Open any modified file. The gutter immediately highlights the altered lines.
3. Press **`Cmd/Ctrl+D`** to open the split diff view.
4. Review the exact additions and deletions against `HEAD`.
5. If something needs adjustment, select the code directly in the diff view and press `Alt+E` to prompt the agent with a targeted correction.

### Pre-Commit Review
Before committing code from your terminal, open px0 to perform a visual walk-through of all pending changes. The uncommitted changes filter isolates your work, ensuring you don't commit debug logs, temporary comments, or unintended formatting tweaks.

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `Cmd/Ctrl+D` | Editor | Toggle Git Diff View (Split / Unified vs. `HEAD`) |
| Toggle Icon | Diff Header | Switch between Side-by-Side and Unified Diff |
| Space Icon | Diff Header | Toggle Ignore Leading/Trailing Whitespace |
| Filter Icon | File Explorer | Show Only Files with Uncommitted Changes |

---

## Configuration & Preferences

Git behavior can be customized in Settings (`Cmd/Ctrl+,`):

- **Git: Gutter Indicators** (`git.gutterIndicators`): Enable or disable real-time change indicator bars in the editor gutter (defaults to `true`).
- **Diff Editor: Render Side-by-Side** (`diffEditor.renderSideBySide`): Default layout for the diff view (`true` for split, `false` for unified).
- **Diff Editor: Ignore Trim Whitespace** (`diffEditor.ignoreTrimWhitespace`): Ignore leading and trailing whitespace diffs (defaults to `true`).
- **CLI Flag `-no-git`**: Launch px0 with Git features completely disabled (`px0 -no-git`) for environments where Git is not installed or when viewing plain directory archives.

---

## Technical Architecture Deep Dive

For an explanation of how px0 executes read-only `git status --porcelain=v2` and `git diff` commands concurrently with directory indexing, see [Git Awareness & Diffing Internals](../internals/git-integration.md).
