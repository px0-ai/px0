# Editing with Coding Agents

px0 is intentionally designed as a read-optimized viewer rather than a traditional text editor. When code needs to be modified, px0 allows you to select lines in either the source viewer or the visual diff view and delegate the edit directly to your local CLI coding agent.

---

## Overview & Core Purpose

In modern AI-native development workflows, developers spend less time typing repetitive syntax and more time reviewing, directing, and guiding intelligent agents. Traditional IDEs carry massive authoring baggage—gigabytes of Electron RAM, plugin conflicts, and heavy text manipulation engines.

px0 decouples code viewing from code authoring. It provides a sub-millisecond, low-resource reading experience (~20-30 MB RSS) while integrating with local CLI coding agents such as Claude Code, Gemini CLI, Cursor Agent, Antigravity, OpenCode, Codex, Aider, Goose, Pi, and OMP. You select the code, provide an instruction, and px0 coordinates the background execution, streams the progress to your terminal, and automatically refreshes modified files upon completion.

---

## Supported Harnesses & Models

px0 auto-detects ten supported terminal coding harnesses and the models exposed by each installed, authenticated CLI:

| Harness | Default Model | Runtime Model Catalog | Execution Template |
| :--- | :--- | :--- | :--- |
| **Claude Code** | Harness default | Default only | `claude --permission-mode acceptEdits -p {prompt}` |
| **Gemini CLI** | Harness default | Default only | `gemini --approval-mode auto_edit -p {prompt}` |
| **Cursor Agent** | Harness default | `cursor-agent models` | `cursor-agent --force -p {prompt}` |
| **Antigravity** | Harness default | `agy models` | `agy --dangerously-skip-permissions --mode accept-edits -p {prompt}` |
| **OpenCode** | Harness default | `opencode models` | `opencode run --agent build --auto {prompt}` |
| **OpenAI Codex** | Harness default | Codex app-server `model/list` | `codex -a never exec --sandbox workspace-write {prompt}` |
| **Aider** | Harness default | Default only | `aider --yes-always --no-auto-commits --message {prompt}` |
| **Goose** | Harness default | Default only | `goose run --no-session -t {prompt}` |
| **Pi** | Harness default | `pi --list-models` | `pi --approve -p {prompt}` |
| **OMP** | Harness default | `omp models --json` | `omp --no-session --approval-mode yolo -p {prompt}` |

The model dropdown always includes `Harness default`, which omits the model flag and delegates selection to the harness. For Cursor Agent, Antigravity, OpenCode, Codex, Pi, and OMP, the dropdown adds only IDs reported by the installed CLI for the current account and configuration. Discovery runs asynchronously with a five-second limit. Opening the picker refreshes the catalogs, so account or configuration changes do not require restarting px0.

Claude Code, Gemini CLI, Aider, and Goose remain default-only until their CLIs expose a safe account-aware, non-interactive model catalog. Failed discovery also offers only `Harness default`. Saved overrides are used only after a successful catalog reports the exact model ID; an unavailable saved model never reaches the harness argv. Choosing `Harness default` clears the saved override.

---

## How an Agent Edit Works

1. **Select Code**: Highlight the lines of code you wish to change in either the source code viewer or the visual git diff view.
2. **Trigger Composer**: Press **`Alt+E`**, right-click to open the context menu, click **Edit Inline** in the footer selection bar, or click the thread icon that appears beside a line number and choose **Edit Inline**. The comment box opens at the top of the **Threads** pane in the right sidebar, which slides open if it was closed.
3. **Configure Harness (First Time)**: Choose your preferred coding harness and model. Your choice is saved globally in `~/.px0/settings.json`, never in your repository files.
4. **Write a Comment**: Type what needs to change (e.g., *"Handle nil pointer return in error check"* or *"Refactor to use sync.Once"*). Then either **Add comment** (`Enter`) to keep it for a batch, or **Apply now** (`Ctrl/Cmd+Enter`) to run just that one straight away.
5. **Real-Time Streaming**: px0 sends the file path, line range, selected code, and prompt to the harness. Progress and agent thought output stream in real time to the terminal stdout where px0 was launched.
6. **Saved as a Thread**: Every edit you apply is recorded as a [thread](threads.md), labelled `inline` or `batch` in the Threads list. While an edit runs, its box and the batch bar offer **View thread** to watch it live. Open it later to see what the agent replied and which files it changed, or to ask a follow-up.
7. **Automatic Document Reload**: When the harness finishes writing to disk, px0 automatically detects the modified files, refreshes the open tabs in place, updates git gutters, and preserves your scroll position. Source views stay source views; diff views stay diff views.

---

## Batching Comments

You can queue several comments and apply them together, in one coordinated run:

1. Write a comment and press **Add comment** (`Enter`). The box folds down to a single line showing your text. Click it to edit it again.
2. Select other code (or click the thread icon beside another line, then **Edit Inline**) and add another comment.
3. The **Batch** bar at the top of the Threads pane counts your comments and offers **Apply all (N)** (`Ctrl/Cmd+Shift+Enter`). Its harness and model selectors apply to the whole batch. **Clear All** discards every comment.

The bar appears as soon as the first box opens, so the batch is always in view. A batch runs as one thread whose first message lists every comment with its code, so the agent can coordinate the changes.

---

## Multi-Edit Concurrency & Safety Guards

- **Non-Overlapping Concurrency**: Multiple agent edits can run simultaneously across different files or non-overlapping line ranges within the same file. Each edit runs in its own isolated composer box.
- **Overlap Prevention**: If you attempt to dispatch an edit that overlaps with lines already being edited by an in-flight agent, px0 blocks the request with a warning. This prevents competing harnesses from producing conflicted or unreviewable code.
- **Tab Close Guard**: Closing a browser tab while an agent edit is actively running prompts for confirmation, ensuring harnesses are never abandoned mid-execution.
- **Inline Error Recovery**: If an agent process fails (for example, due to an expired API key or invalid CLI argument), the error message along with process stdout and stderr is rendered inline beneath your instruction. Your instruction remains preserved so you can tweak and retry without retyping.
- **Network Sandboxing**: Agent execution is only accepted when px0 is accessed via `localhost` or direct IP address. Access through remote hostnames or public tunnel domains rejects agent commands by default to prevent unauthorized execution.

---

## Beyond Code: Commit Messages

The same harness and model you pick here are also used by the sidebar git panel's **Commit with AI** button, which asks the harness to write a commit message for your staged changes instead of editing code — see [Git Awareness: Stage, Commit, Push, Pull](git-integration.md).

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `Alt+E` | Code Selected | Open Agent Edit Composer |
| Right-Click | Code Selected | Choose "Edit with Agent" from context menu |
| Selection Bar | Code Selected | Click "Edit with Agent" button in footer |
| `Enter` | Composer Input | Dispatch instruction to agent harness |
| `Esc` | Composer Input | Cancel composer and dismiss prompt |
| Footer Badge | Status Bar | Click to switch active harness or model |

---

## Configuration Options

Agent preferences are managed in Settings (`Cmd/Ctrl+,`) or stored in `~/.px0/settings.json`:

- **Agent: Harness** (`agent.harness`): The default CLI tool to execute (`claude`, `gemini`, `cursor-agent`, `agy`, `opencode`, `codex`, `aider`, `goose`, `pi`, `omp`).
- **Agent: Timeout Seconds** (`agent.timeoutSeconds`): Maximum execution time before px0 terminates the background process (defaults to `120` seconds, range `10`–`600`).
- **CLI Flags**:
  - `px0 -agent <name>`: Force a specific agent harness for the current session.
  - `px0 -no-agent`: Completely disable agent editing capabilities.

---

## Technical Architecture Deep Dive

For in-depth details on process spawning, headless argument construction, PID tracking, process group termination, and in-place document reconciliation, see [Harness Editing & Agent Dispatch Internals](../internals/agent-editing.md).
