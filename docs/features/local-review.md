# Local Review: Read an Agent's Review Next to the Code

When you ask Claude Code or another coding agent to review a branch or a pull request, the comments arrive as terminal text: hard to read, and detached from the code they are about. px0 can show that review where you read code: inline on the diff, with the file tree, search and language intelligence around it, and a **Discuss** button on each comment that opens a px0 thread about it.

The agent writes its review to a JSON file and launches px0 on it. px0 only reads the file.

---

## Opening a review

```bash
# Review two local revisions; base and head come from the file
px0 -review review.json

# Name them explicitly (these override the file)
px0 -review review.json -base main -head feature/refresh

# Add the agent's comments to a GitHub pull request session
px0 -review review.json https://github.com/owner/repo/pull/123

# The same, with the pull request named in the file ("pr": 123 or "pr": "<url>")
px0 -review review.json

# Read the review from stdin
agent-output | px0 -review -
```

Flags go before the pull request URL. Run it from inside the repository.

When the file has a `pr` (a number on the repository's `origin` remote, or a full URL), px0 opens that pull request exactly as if its URL had been given: the real pull request view, with the comments already on GitHub, and the agent's comments added. A URL on the command line wins over the file's `pr`. `-base` and `-head` do not apply to a pull request.

- If **head is what you have checked out**, px0 uses your repository as it is: nothing is checked out and nothing is copied.
- Otherwise px0 checks head out into a temporary worktree under your system temp directory and removes it when you stop px0.
- The diff is against the **merge-base** of base and head, the same way a pull request is shown.
- If the base is not given anywhere, px0 tries `origin/HEAD`, `origin/main`, `origin/master`, `main`, `master`, in that order.

px0 never writes the review file, and the agent should keep it outside the repository.

## What you see

- **A "Review" bar** with the title and `base ← head`. In a local review the controls that submit to GitHub are hidden, since there is no pull request to submit to. With a pull request they are the usual ones.
- **A Review section at the top of the comments panel**: the title, the agent's suggested verdict, the summary, and comments that belong to no file. The panel opens on its own the first time there is something in it.
- **Threads inline in the diff**, directly under the commented line (under the last line of a range), in both the split and unified layouts, like GitHub's "Files changed". A thread is open when you arrive; click its header to fold it, and px0 remembers what you folded. A marker in the gutter marks each commented line; click it to jump to the thread. Comments on a whole file (no line), comments on a line the diff does not show, and GitHub comments that are outdated appear in a strip at the top of the file's diff. The comments panel still lists everything.
- **GitHub's comments next to the agent's.** When a pull request is open, the comments already on it are shown inline in the same way, with their Reply box, and the agent's comments sit in their own threads beside them (with **Discuss**). An agent comment never becomes the start of a GitHub thread. A GitHub comment whose line has since changed is marked *outdated* and shown at file level, not next to unrelated code.
- **In the Source view**, a line with comments has a marker in the line-number gutter. Click it to open the line's threads in a card pinned beside the line (below it, or above if there is no room); click it again, or the card's close button, to put it away. Only lines on the new side are marked, since the file on disk has no deleted lines.
- **"not posted"** on every comment from the agent's review, and on your own unsubmitted drafts, when there is a GitHub pull request. Those comments exist only in px0 until you act on them, so they are also drawn with a dashed border, to tell them from the comments already on GitHub.
- **Severity chips** on every comment: Blocker, Major, Minor, Nit, Question, Praise.
- **Suggestions** (a fenced `suggestion` block) are shown as a code block labelled *suggestion*. px0 does not apply them; to act on one, use **Discuss** and ask your coding harness.
- **Notes where px0 had to adjust a comment**: *moved* (the agent's line number was off, but the quoted code was found nearby), *this code could not be found any more* (shown at file level), *past the end of the file*, and *this file is not part of the diff*.
- **Banners** when the review was written against a different head than the one you are looking at (the comments are placed by matching their code, so check them), when a rewritten file no longer parses (the last good review stays up), and when comments were skipped, with the reasons.

## Discussing a comment

**Discuss** on any comment opens a new thread in the right sidebar with the comment already quoted in the message box. Type your question after it and send.

The thread's harness is told that this is a local review, given the exact range under review (the merge-base diff, also saved as a file) and the changed files, and given the review's title, summary and file path. It is told to treat the review as claims to check against the code, not as ground truth. A comment on a head-side line anchors the thread to those lines, so the harness also gets the snippet; a general comment, a file-level comment, or one on the old side of a change starts a thread with no anchor that says where the comment was.

Threads work as they always do: the harness can read and edit files, and px0 reloads what changed.

## While px0 is open

The agent can rewrite the review file. px0 notices when the browser tab regains focus and shows the new comments; comment ids stay the same, so an expanded comment stays expanded. Comments are shown only after they have been checked against the code, which takes a moment on a large review. If px0 opens in a background browser tab the review still loads; only later refreshes wait until the tab is visible.

## For agents

A ready-made skill is in [`skills/px0-review/SKILL.md`](../../skills/px0-review/SKILL.md). The full format is in the [Review File Specification](../internals/review-file-spec.md), and its [JSON Schema](../internals/review.schema.json) can validate a file.

## Limits and not yet supported

- 2 MiB per file, 2000 comments, 16 KiB per comment.
- References to code on other branches are listed under the comment as `rev:path:line` but their code is not fetched or opened yet.
- There is no accepted / dismissed state for comments, and no way yet to turn an agent's comment into a draft on a GitHub pull request.
- Changing `pr` in a file that is already open needs a restart of px0; a banner says so.
- GitHub comment bodies are shown as plain text, not Markdown.
- Pushing, pulling and the unpushed-commits list behave as in a plain workspace, not as in a pull request review.

See [Local Review internals](../internals/local-review.md) for how it works and why.
