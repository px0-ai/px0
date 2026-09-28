# Threads

A thread is a long-running conversation with your coding harness about your code. Where an inline edit ([Agent Editing](agent-editing.md)) is a single instruction that runs once and is forgotten, a thread keeps its transcript and continues the same conversation with every message. Use it to ask what code does, follow up on the answer, ask whether a change affects other places, and make edits across as many files as the conversation needs.

Threads live in the right sidebar, on the **Threads** tab, next to References, Symbols, Calls and Search.

## Starting a Thread

| Way | How |
| --- | --- |
| Selection | Select code and press `Alt+T`, click **Thread** in the footer selection bar, or right-click and choose **Start Thread**. |
| Line | Click the thread icon that appears beside a line number and choose **Start Thread**. |
| Current line | With nothing selected, `Alt+T` anchors the thread to the cursor line. |
| Workspace | On the Threads tab, click **+ New**, or run **Threads: Start New Thread** from the palette. The thread is not tied to any file. |

The thread opens as a draft anchored to the selected lines, shown as `path:lines` under the title. Click the anchor to jump back to that code. Nothing is created until you send the first message.

The anchor is where the conversation started, not a limit. The harness can read any file and change any file, in one turn or across many.

## The Conversation

Type in the compose box and press `Enter` to send (`Shift+Enter` for a new line). The reply streams in as the harness writes it:

- **Steps**: a collapsible list of what the harness did, such as `Read calc.go` or `Edit server.go`. It is open while the turn runs.
- **Reply**: the harness's written answer, with code blocks, inline code, bold and bullet lists rendered.
- **Changed files**: after each turn, a chip for every file that turn touched. Click one to open it. Open tabs reload in place when a turn that changed files finishes.

Each message continues the same conversation, so "and what about the tests?" works. While a reply is being written you can press **Stop**; whatever the harness already wrote to disk stays.

The **Model** row under the transcript is the same harness and model choice used everywhere else in px0. You can change it mid-thread: px0 starts a fresh session with the new harness and hands it the earlier transcript.

There is no overlap guard. Threads and inline edits can run at the same time, on the same files, and the last write wins.

## Inline Edits Are Threads Too

[Inline edit](agent-editing.md) comment boxes live at the top of this same pane (the tab shows how many are waiting to be applied), so composing, applying and following up all happen in one place. An inline edit or batch you apply is recorded as a thread, labelled `inline` or `batch` in the list. Open it to read the agent's summary and the files it changed, or send a follow-up such as "also update the tests". These threads keep the overlap check inline edits have always had: an edit on lines another edit is still changing is refused. A conversation you start yourself is never blocked this way.

## The Thread List

The list shows every thread for the workspace, most recently active first, with its anchor, turn count and age. A spinner marks a thread that is working, and `!` marks one whose last reply failed. **This file** filters to threads started in the open file. The **Threads** tab shows a pulsing dot while any thread is working, so you can leave it and come back.

Threads are stored on disk and survive restarts. A reply that was still running when px0 exited is marked as interrupted. Delete a thread with the trash button in its header.

## Requirements

Threads use the coding harness you have selected, the same as inline edits, and are unavailable with `-no-agent`. Harnesses that px0 can hand a session id (Claude Code, and Cursor Agent) keep their own memory of the conversation. With any other harness, px0 sends the earlier transcript along with each message, which works but uses more tokens on long threads.
