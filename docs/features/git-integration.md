# Git Awareness & Visual Diff Viewer

px0 includes built-in Git awareness and an interactive visual diff viewer. It highlights changes across your file tree and editor gutters, and lets you toggle between source code and an interactive side-by-side or unified diff against `HEAD` with `Cmd/Ctrl+D`. Pass `-diff <ref>` to review all branch and working-tree changes since that ref diverged from `HEAD`.

---

## Overview & Core Purpose

In modern software engineering, coding agents, background formatters, and compilers continuously generate or modify files on disk. Developers spend a significant portion of their time verifying what changed, ensuring unintended edits were not introduced, and auditing modifications prior to staging or committing.

px0 provides non-destructive, zero-latency Git awareness. It queries Git status asynchronously in the background without staging files, mutating index locks, or slowing down viewer startup. With visual badges, ancestor dirty propagation, gutter indicators, and full split/unified diffs, you can review changes with complete confidence without leaving the browser.

---

## Key Capabilities

- **Real-Time Live Status Synchronization**: px0 establishes a lightweight Server-Sent Events (SSE) connection (`/api/git/stream`) to push working-tree status changes directly to the browser. You do not need to refresh the browser or click manual reindex buttons when files change on disk.
- **Sub-Millisecond CLI Change Awareness**: When you execute Git operations in your terminal (`git checkout`, `git reset`, `git add`, `git commit`, `git restore`, `git stash`), px0 detects the operation in sub-milliseconds by checking metadata timestamps on Git control files (`.git/index`, `.git/HEAD`, `.git/packed-refs`), instantly updating your view without scanning files on disk.
- **File Tree Status Badges**: The file explorer decorates changed files with colored badges indicating their Git working-tree status:
  - `M` (Modified): File differs from the active comparison base.
  - `A` (Added / Staged): Newly added file staged in the index.
  - `D` (Deleted): File removed from the working tree.
  - `U` (Untracked): New file not yet tracked by Git.
  - `R` (Renamed): File renamed or moved.
- **Dirty Ancestor Folder Propagation**: When a nested file is modified (e.g., `src/core/auth/token.go`), all parent directories in the tree (`auth/`, `core/`, `src/`) display a subtle dirty indicator badge. This allows you to spot modifications even when folder branches are collapsed.
- **File Explorer vs. Git Changes Toggle**: A dedicated segmented toggle in the sidebar header allows you to switch between the full project directory tree and the Git changes view. In Git changes mode, px0 collapses untouched folders and presents only files with uncommitted additions, modifications, or deletions.
- **Automatic Explorer Fallback**: If all uncommitted changes are discarded or committed while you are in Git changes mode, px0 automatically switches back to standard file explorer mode so you are never left viewing an empty tree.
- **Auto-Closing Discarded Diff Tabs**: When you discard changes to a file from the terminal (`git checkout -- file` or `git reset`), any tab opened in diff view for that file automatically closes in reverse index order, keeping the active tab index stable and preventing stale diff errors.
- **Visual Gutter Diff Indicators**: The code viewer gutter places colored indicator bars alongside line numbers to mark edits in real time:
  - Green bar for added lines.
  - Blue bar for modified lines.
  - Red triangle or marker for deleted lines.
- **Interactive Diff Viewer (`Cmd/Ctrl+D`)**: Toggle between normal source view and full Git diff with a single keystroke.
- **Side-by-Side & Unified Diff Modes**:
  - **Side-by-Side (Split)**: View code from the comparison base on the left and the active working tree on the right with synchronized scrolling.
  - **Unified**: View changes inline with consecutive additions and deletions.
- **Whitespace Diff Filtering**: Toggle whitespace trimming to hide trivial indentation and trailing space differences when reviewing significant logic changes.
- **Direct Agent Editing from Diffs**: Select any modified or added line in the diff view and trigger an AI agent edit (`Alt+E`) to refine or correct the change on the spot.
- **Battery and Focus Awareness**: The live stream automatically suspends when the browser tab is hidden (`document.visibilityState === 'hidden'`), conserving CPU cycles and laptop battery. When you switch back to px0, it instantly reconnects and queries `/api/git/refresh` to catch any changes made while the window was in the background.

---

## Developer Workflows & Practical Value

### Continuous Auditing of AI Agent Edits
When an AI coding agent (Claude Code, Gemini CLI, Cursor Agent, Antigravity, Aider) edits your code in the background:
1. Switch to the **Git Changes** view in the sidebar to isolate touched files.
2. Status badges and gutter markers update in real time as the agent writes to disk.
3. Open any modified file and press **`Cmd/Ctrl+D`** to review side-by-side changes against the active comparison base.
4. If an edit needs refinement, select the relevant lines directly inside the diff view and press `Alt+E` to prompt the agent with a targeted correction.

### Terminal Interaction Without Stale Views
When managing branches or staging files from your terminal:
1. Stage or reset files in the terminal (`git add file.go` or `git checkout -- file.go`).
2. px0 immediately catches the `.git/index` modification and patches the file tree badges in place without resetting your scroll position or collapsing expanded folders.
3. Tabs displaying diffs for discarded files close automatically, keeping your workspace clean and focused.

### Pre-Commit Visual Review Station
Before committing code from your terminal, open px0 to perform a visual walk-through of all pending changes. The Git changes view isolates your work, ensuring you do not commit debug logs, temporary comments, or unintended formatting tweaks.

---

## Keyboard Shortcuts & Controls

| Shortcut / Control | Context | Action |
| :--- | :--- | :--- |
| `Cmd/Ctrl+D` | Editor | Toggle Git Diff View (Split / Unified vs. the comparison base) |
| Toggle Segment (`Files` / `Changes`) | Sidebar Header | Switch between File Explorer and Changed Files Only |
| Toggle Icon | Diff Header | Switch between Side-by-Side and Unified Diff |
| Space Icon | Diff Header | Toggle Ignore Leading/Trailing Whitespace |
| `Mod+Shift+R` | Global | Force Workspace and Git Status Refresh |

---

## Configuration & Preferences

Git behavior can be customized in Settings (`Cmd/Ctrl+,`):

- **Git: Gutter Indicators** (`git.gutterIndicators`): Enable or disable real-time change indicator bars in the editor gutter (defaults to `true`).
- **Diff Editor: Render Side-by-Side** (`diffEditor.renderSideBySide`): Default layout for the diff view (`true` for split, `false` for unified).
- **Diff Editor: Ignore Trim Whitespace** (`diffEditor.ignoreTrimWhitespace`): Ignore leading and trailing whitespace diffs (defaults to `true`).
- **CLI Flag `-no-git`**: Launch px0 with Git features completely disabled (`px0 -no-git`) for environments where Git is not installed or when viewing plain directory archives.
- **CLI Flag `-diff <ref>`**: Compare the current branch and working tree with `merge-base(<ref>, HEAD)`. For example, `px0 -diff main` shows the complete reviewable branch diff while preserving untracked-file and conflict badges. An invalid ref emits a warning and falls back to `HEAD`.

---

## Technical Architecture Deep Dive

For an explanation of how px0 executes read-only `git status --porcelain=v2` and `git diff` commands concurrently with directory indexing, how `.git/index` stat cache fast-paths achieve sub-millisecond CLI detection, and how Server-Sent Events stream diffs to the DOM, see [Git Awareness & Diffing Internals](../internals/git-integration.md).
