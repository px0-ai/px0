# Pull Request Review & Agent Collaboration

px0 can check out a pull request's full source tree and review it like any local workspace: full codebase navigation, symbol outline, LSP intelligence, and search, alongside a diff scoped to the PR's merge-base, real-time coding agent synchronization, inline comment drafting, AI batch application, and the same git panel a plain workspace has — so you can commit and push fixes back to the PR's own branch, and pull in new commits someone else pushed while you were reviewing.

---

## Overview & Core Purpose

GitHub's web PR view shows you the isolated diff, but not the codebase around it: jumping to a caller three files away, or checking how a changed function is used elsewhere, means either trusting memory or manually cloning and switching branches.

With px0, you simply pass the pull request URL:
```bash
px0 https://github.com/owner/repo/pull/123
```

px0 automatically prepares a temporary worktree or clone, computes the merge-base diff against the target branch, and opens a lightweight, zero-latency code viewer in your browser.

---

## Opening a Pull Request

Pull request review is triggered by passing the full URL directly:

```bash
px0 https://github.com/owner/repo/pull/123
```

> [!NOTE]
> Bare PR numbers (e.g. `px0 123`) and the `px0 pr` subcommand have been deprecated in favor of explicit URL routing (`px0 <url>`). Running `px0 pr` provides a helpful reminder to pass the URL directly.

### Interactive Preparation Spinner
Because fetching metadata and checking out remote references takes a few moments, px0 displays an animated CLI spinner:
```text
⠋ Fetching PR #123 metadata from github...
⠙ Fetching PR #123 head and preparing worktree...
⠸ Computing merge base with main...
✔ PR #123 checked out (Refactor auth token resolution)
```

### Merged PR Handling
If the pull request is already merged:
- px0 detects its merged status from the API and opens it directly without blocking.
- In both the CLI checkout message and the browser review header, a prominent purple **`Merged`** pill badge is displayed.

### Multi-Session Isolation
Each PR review runs as its own isolated process on its own port. Running `px0 https://github.com/owner/repo/pull/456` while another PR review or local workspace is open will not disturb existing sessions.

From inside a running px0 browser session, the Command Palette (`Cmd/Ctrl+K` → **Git: Open Pull Request…**) launches a new review process in a fresh browser tab.

---

## Reviewing & Real-Time Agent Collaboration

### 1. Merge-Base Diff View
- The **Changes** toggle in the sidebar defaults to all files modified by the PR.
- Press **`Cmd/Ctrl+D`** on any file to open side-by-side or unified diffs.
- The diff is computed against the merge-base between the PR head and its target branch, exactly mirroring the diff shown on GitHub.

### 2. Live Agent Synchronization
When you or your background AI coding agents (Claude Code, Gemini CLI, Cursor Agent, Antigravity, Aider, etc.) make edits to the PR checkout from other terminals:
- px0's real-time file watcher immediately picks up modifications without full re-indexing.
- Gutter diffs, status badges, and open tabs reload live in the browser.

### 3. Line Comments & Hover Actions
When hovering over code lines or diff lines:
- A pencil icon (`✏`) appears next to the line number.
- Clicking the pencil icon opens a context menu with two actions:
  - **Leave Comment on GitHub**: Drafts an inline review comment.
  - **Leave Comment for Inline Edit**: Prompts your local AI coding agent to edit those lines directly.
- Alternatively, select any range of lines and press **`Alt+R`** (or click **Comment** on the selection bar) to open the review comment composer.

### 4. Batch Applying Comments Locally (`⚡ Batch Apply`)
Drafted review comments appear in the PR top bar and inline across files:
- **⚡ Batch Apply**: Lets you apply all drafted review comments across the entire pull request in one go using your configured coding agent harness. The agent reads your comments as instructions and modifies the code directly in the PR worktree.
- Comments remain drafts in memory until either batch-applied or formally submitted.

### 5. Submitting Formal Reviews
The PR review bar above the editor tabs hosts the overall review summary and verdict actions:
- **Comment**: Submit feedback without an approval status (available to all reviewers).
- **Approve** / **Request Changes**: Available when your authenticated token has repository push access.
- Submitting posts a single review payload containing all draft line comments and the review body.

### 6. Editing, Committing, and Pushing Back
A PR checkout is a real git worktree, and the git panel works inside it exactly as it does in a plain workspace:
- **Edit and commit**: Make a change (by hand, or by dispatching a coding agent on the checkout), stage it, and either write a commit message yourself or click **Commit with AI**.
- **Push**: Sends the checkout's `HEAD` to the pull request's *actual* head branch on its actual repository — a fork included — not wherever the checkout happens to live locally.
- **Pull**: Re-fetches the PR's current head. If new commits landed on it since you opened the review and your checkout can fast-forward onto them cleanly, px0 updates the worktree and refreshes the diff, the PR bar, and the comments panel automatically. If your checkout has diverged — you made local commits that aren't on the PR head yet, or the head was force-pushed — px0 refuses with a clear message rather than merging; commit and push first, or resolve it in a terminal.

> [!IMPORTANT]
> A PR checkout lives in a temporary directory for the life of the px0 process (see [Multi-Session Isolation](#multi-session-isolation) below). Any edits you make there — committed or not — are gone once the session closes, unless you've pushed them back to the PR's branch first.

---

## Authentication & Read-Only Review

px0 discovers forge credentials in the following order:

1. **`github.token`** in px0 Settings (`Cmd/Ctrl+,` → GitHub, or `~/.px0/settings.json`).
2. **`GITHUB_TOKEN`** environment variable.
3. **`GH_TOKEN`** environment variable.
4. **`gh auth token`** via the GitHub CLI if installed and authenticated.

### Unauthenticated & Read-Only Access
If no token is configured:
- Public repositories still check out and diff seamlessly.
- You can draft review comments in memory and use **⚡ Batch Apply** with local AI agents.
- Formal review submission back to the remote forge requires an auth token. The CLI banner displays:
  ```text
  access: read-only (no github token: set GITHUB_TOKEN or gh auth login to submit reviews)
  ```

---

## Extensible Forge Architecture (`GitProvider`)

px0 abstracts forge interactions through a clean, minimal `GitProvider` interface in [`provider.go`](file:///home/arpit/workspace/px0/px0/provider.go):
- **Provider Detection**: Matches input URLs against registered providers (GitHub, and in the future GitLab, Bitbucket, etc.).
- **Normalized Metadata**: Maps forge-specific PR/MR objects to standard `PRMeta` structures.
- **Push Access & Token Discovery**: Isolates provider-specific authentication mechanisms.

---

## Keyboard Shortcuts & Controls

| Shortcut / Control | Context | Action |
| :--- | :--- | :--- |
| `Alt+R` | Selection in editor or diff view | Open review comment composer |
| Line Hover (`✏`) | Hovering on editor line number | Choose between GitHub review comment or inline agent edit |
| `Cmd/Ctrl+D` | Active tab | Toggle side-by-side / unified diff against merge-base |
| Command Palette (`Cmd/Ctrl+K`) | Command Palette → **Git: Open Pull Request…** | Launch a new PR review tab |
| **`⚡ Batch Apply`** | PR header bar | Dispatch all drafted comments to local AI coding harness |
| **Submit Review** | PR header bar | Submit Approve / Request Changes / Comment to remote forge |
| **Pull** | Sidebar git panel | Fast-forward the checkout onto the PR's current head; refuses on divergence |
| **Push** | Sidebar git panel | Push the checkout's `HEAD` to the PR's actual head branch |
| **Commit with AI** | Sidebar git panel | Write and commit a message for the staged diff with your coding harness |
