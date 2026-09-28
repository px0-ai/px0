# Git Awareness, Diff Viewer & Stage/Commit/Push/Pull

px0 includes built-in Git awareness, an interactive visual diff viewer, and a sidebar panel for the rest of the everyday git loop. It highlights working-tree modifications across your file tree and editor gutters, lets you toggle between source code and an interactive side-by-side or unified diff against `HEAD` with `Cmd/Ctrl+D`, and stages, commits, pushes, and pulls without leaving the browser.

---

## Overview & Core Purpose

In modern software engineering, coding agents, background formatters, and compilers continuously generate or modify files on disk. Developers spend a significant portion of their time verifying what changed, ensuring unintended edits were not introduced, and auditing modifications prior to staging or committing.

px0's git status and diffing are read-only and zero-latency by default: querying `git status` asynchronously in the background never stages files, mutates index locks, or slows down viewer startup. With visual badges, ancestor dirty propagation, gutter indicators, and full split/unified diffs, you can review changes with complete confidence without leaving the browser.

The sidebar's git panel is the one part of this feature that *does* write to the repository — but only in direct response to a click: staging, committing, pushing, and pulling all happen exactly when you ask for them, never automatically. Pull is deliberately conservative (fast-forward only) so it can never leave the working tree mid-conflict.

---

## Key Capabilities

- **Real-Time Live Status Synchronization**: px0 establishes a lightweight Server-Sent Events (SSE) connection (`/api/stream`, aliased by `/api/git/stream`) to push working-tree status changes directly to the browser. You do not need to refresh the browser or click manual reindex buttons when files change on disk.
- **Sub-Millisecond CLI Change Awareness**: When you execute Git operations in your terminal (`git checkout`, `git reset`, `git add`, `git commit`, `git restore`, `git stash`), px0 detects the operation in sub-milliseconds by checking metadata timestamps on Git control files (`.git/index`, `.git/HEAD`, `.git/packed-refs`), instantly updating your view without scanning files on disk.
- **File Tree Status Badges**: The file explorer decorates changed files with colored badges indicating their Git working-tree status:
  - `M` (Modified): Working tree file differs from `HEAD`.
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
  - **Side-by-Side (Split)**: View original `HEAD` code on the left and active working-tree code on the right with synchronized scrolling.
  - **Unified**: View changes inline with consecutive additions and deletions.
- **Whitespace Diff Filtering**: Toggle whitespace trimming to hide trivial indentation and trailing space differences when reviewing significant logic changes.
- **Direct Agent Editing from Diffs**: Select any modified or added line in the diff view and trigger an AI agent edit (`Alt+E`) to refine or correct the change on the spot.
- **Battery and Focus Awareness**: The live stream automatically suspends when the browser tab is hidden (`document.visibilityState === 'hidden'`), conserving CPU cycles and laptop battery. When you switch back to px0, it instantly reconnects and queries `/api/git/refresh` to catch any changes made while the window was in the background.
- **Per-File Stage Tick**: Every changed row in the file tree carries a small tick button next to its status badge. Clicking it stages or unstages that file (`git add` / `git reset`) without opening a terminal; the tick updates live as the same status stream that drives the badges reconciles it.
- **Git Panel (Stage, Commit, Push, Pull)**: A resizable panel at the bottom of the sidebar shows the current branch, a live `staged / changed` count, a monospace commit message box, and Stage All / Commit / Pull / Push buttons.
- **Commit with AI**: A harness and model picker (the same one used for inline edits) sits above the commit box. **Commit with AI** dispatches the selected harness with the staged diff — plus any instructions from `git.commitMessageInstruction` in Settings — asks it to write only the commit message text, drops that into the box, and commits with it in the same action.
- **Fast-Forward-Only Pull**: Pull always tries a clean fast-forward onto the remote (or, in a PR review session, the pull request's current head). If history has diverged at all, it refuses immediately with a clear message rather than ever starting a merge — there is never a conflict state to clean up.
- **Push to the Right Place**: In a plain workspace, Push pushes the current branch to its configured upstream (offering to set one up on a first push). In a PR review session, Push targets the pull request's actual head branch on its actual repository — a fork included — never wherever the checkout happens to be sitting.

---

## Developer Workflows & Practical Value

### Continuous Auditing of AI Agent Edits
When an AI coding agent (Claude Code, Gemini CLI, Cursor Agent, Antigravity, Aider) edits your code in the background:
1. Switch to the **Git Changes** view in the sidebar to isolate touched files.
2. Status badges and gutter markers update in real time as the agent writes to disk.
3. Open any modified file and press **`Cmd/Ctrl+D`** to review side-by-side changes against `HEAD`.
4. If an edit needs refinement, select the relevant lines directly inside the diff view and press `Alt+E` to prompt the agent with a targeted correction.

### Terminal Interaction Without Stale Views
When managing branches or staging files from your terminal:
1. Stage or reset files in the terminal (`git add file.go` or `git checkout -- file.go`).
2. px0 immediately catches the `.git/index` modification and patches the file tree badges in place without resetting your scroll position or collapsing expanded folders.
3. Tabs displaying diffs for discarded files close automatically, keeping your workspace clean and focused.

### Pre-Commit Visual Review Station
Before committing code from your terminal, open px0 to perform a visual walk-through of all pending changes. The Git changes view isolates your work, ensuring you do not commit debug logs, temporary comments, or unintended formatting tweaks.

### Stage, Write, and Commit Without Leaving the Browser
Once you've reviewed a change in the diff view, finish the commit right there:
1. Tick the files you want in this commit from the file tree (or click **Stage All**).
2. Write a message in the git panel's commit box, or click **Commit with AI** to have your configured coding harness write one from the staged diff and commit with it directly.
3. Click **Push** to send the branch to its remote. If it's the first push on a new branch, px0 offers to set the upstream for you.

### Catching Up With a Moving Remote
When a teammate (or CI) has pushed new commits to the branch you're reviewing:
1. Click **Pull** in the git panel.
2. If your local branch can fast-forward cleanly onto the new commits, px0 updates it and the diff view refreshes automatically.
3. If your branch has diverged — you have local commits the remote doesn't, or the remote history was rewritten — px0 refuses with a clear message instead of attempting a merge. Resolve it in a terminal, then pull again.

---

## Keyboard Shortcuts & Controls

| Shortcut / Control | Context | Action |
| :--- | :--- | :--- |
| `Cmd/Ctrl+D` | Editor | Toggle Git Diff View (Split / Unified vs. `HEAD`) |
| Toggle Segment (`Files` / `Changes`) | Sidebar Header | Switch between File Explorer and Changed Files Only |
| Toggle Icon | Diff Header | Switch between Side-by-Side and Unified Diff |
| Space Icon | Diff Header | Toggle Ignore Leading/Trailing Whitespace |
| `Mod+Shift+R` | Global | Force Workspace and Git Status Refresh |
| Stage Tick | File Tree Row | Stage / unstage that file |
| **Stage All** | Git Panel | Stage every changed file |
| **Commit** | Git Panel | Commit whatever is currently staged with the written message |
| **Commit with AI** | Git Panel | Write a commit message from the staged diff with the selected harness, then commit with it |
| **Pull** | Git Panel | Fast-forward onto the remote (or, in PR review, the PR's current head); refuses on divergence |
| **Push** | Git Panel | Push the current branch (or, in PR review, to the PR's head branch) |

---

## Configuration & Preferences

Git behavior can be customized in Settings (`Cmd/Ctrl+,`):

- **Git: Gutter Indicators** (`git.gutterIndicators`): Enable or disable real-time change indicator bars in the editor gutter (defaults to `true`).
- **Diff Editor: Render Side-by-Side** (`diffEditor.renderSideBySide`): Default layout for the diff view (`true` for split, `false` for unified).
- **Diff Editor: Ignore Trim Whitespace** (`diffEditor.ignoreTrimWhitespace`): Ignore leading and trailing whitespace diffs (defaults to `true`).
- **Commit Message Instructions** (`git.commitMessageInstruction`): Free-text instructions appended to the prompt **Commit with AI** sends to your coding harness (e.g. *"Follow Conventional Commits"* or *"Reference the ticket number in the branch name"*). Defaults to empty. This setting renders as a multi-line textarea in the Settings UI.
- **Coding Harness & Model**: **Commit with AI** uses the same globally selected harness and model as inline agent edits (`agent.harness`, and its persisted model) — see [Editing with Coding Agents](agent-editing.md).
- **CLI Flag `-no-git`**: Launch px0 with Git features completely disabled (`px0 -no-git`) for environments where Git is not installed or when viewing plain directory archives.
- **CLI Flag `-no-agent`**: Disables **Commit with AI** along with every other coding-harness feature; Stage/Commit/Push/Pull remain available since they never invoke a harness.

---

## Technical Architecture Deep Dive

For an explanation of how px0 executes read-only `git status --porcelain=v2` and `git diff` commands concurrently with directory indexing, how `.git/index` stat cache fast-paths achieve sub-millisecond CLI detection, and how Server-Sent Events stream diffs to the DOM, see [Git Awareness & Diffing Internals](../internals/git-integration.md).
