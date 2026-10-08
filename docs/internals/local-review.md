# Local Review: Agent-Authored Reviews in px0

This document describes `px0 -review`: showing a review that a coding agent wrote, as inline comments on the diff between two revisions (or on a pull request), with a path from each comment into a px0 [thread](threads.md). The file format is specified in [Review File Specification](review-file-spec.md). The code is [`review.go`](../../review.go), with changes in [`pr.go`](../../pr.go), [`server.go`](../../server.go) and [`main.go`](../../main.go), and the frontend in [`web/src/review.js`](../../web/src/review.js) and [`web/src/pr.js`](../../web/src/pr.js).

## 1. Problem

A common workflow is to ask a coding agent to review a pull request or the diff between two branches. The agent answers in the terminal with comments tied to specific lines, general comments, and references to code on other branches. That is hard to read next to the code.

px0 already had the review UI: a merge-base diff view, gutter comment markers, a comments panel with threads, draft comments, and PR-scoped chat threads. But it was gated on a GitHub pull request. `prSession` was built only by `checkoutPR` from a forge URL, and the only comments it could show came from GitHub or from the reviewer's own drafts. There was no way to review two local branches and no way for an agent to hand px0 its comments.

## 2. Constraints from px0's tenets

| Tenet ([agents guide](../agents/README.md)) | How this design keeps it |
|---|---|
| Edits go through a harness, never px0 | The review file is read-only data. No endpoint accepts file content or comments. A `suggestion` block is only previewed. |
| Nothing written into a working tree | The review file lives outside the repo. A checkout that is needed goes in the OS temp directory. |
| Single static binary, no runtime deps | `encoding/json` only. The JSON Schema is embedded with `go:embed`. |
| Performance budgets | The listener is up before the review is checked against the code. Checking runs in the background, bounded to `NumCPU` workers and 500 files. Nothing runs on the scroll path. |

## 3. How it works

### 3.1 Launch

```bash
px0 -review review.json                        # base and head come from the file
px0 -review review.json -base main -head feature
px0 -review review.json <pr-url>               # a real PR plus the agent's comments
px0 -review -                                  # read the review from stdin
px0 -review-schema                             # print the embedded JSON Schema
```

It is a flag and not a subcommand because `px0 pr` was removed on purpose. Flags go first: Go's `flag` package stops at the first non-flag argument.

`main.go` refuses `-review` with `-no-git`, with a path argument, or (with a PR URL) with `-base`/`-head`, and refuses `-base`/`-head` without `-review`. A file that cannot be parsed at all stops px0 before anything starts; per-comment problems are printed and shown instead.

### 3.2 Why a file, not a live API

`localPost` (`lspsetup.go`) requires an `Origin` header that matches the request host. That is the CSRF and DNS-rebinding guard for every mutating endpoint, and it means an agent's `curl` is rejected by design. A review channel should not weaken it. A file needs no port discovery either: px0 walks to a free port, so a live API would force the agent to scrape stdout.

Updates come from re-reading the file when its mtime or size changes. `reviewState.refresh` checks on every `/api/review` request, which the browser makes on focus and visibility change, so nothing polls while the tab is hidden.

### 3.3 Workspace and diff base (`prepareLocalReview`)

- The repository is the one containing the current directory. Revisions are resolved with `git rev-parse --verify <rev>^{commit}`; one starting with `-` is refused before git sees it.
- `base` defaults, in order, to the flag, the file, then the first of `origin/HEAD`, `origin/main`, `origin/master`, `main`, `master` that resolves. `head` defaults to the flag, the file, then `HEAD`.
- `diffBase` is the merge-base of the two, and it goes through the existing `Server.SetPR` path (`diffBase`, `ix.SetDiffBase`, `ix.SetPRHead`, `PRFiles`). The diff view's "PR changes" / "Your changes" split then works unchanged.
- If head is what is already checked out, the repository is used **in place**: no checkout, instant, and language servers see the real tree. The session has `inPlace` set, and `prSession.Close` returns early for it. This guard matters because `Close` otherwise ends with `os.RemoveAll(worktree)`.
- Otherwise head is checked out with `git worktree add --detach` into `px0-review-*` under the OS temp directory, and `Close` removes the worktree registration and the directory, exactly as for a PR.

### 3.4 Reusing `prSession`

A local review is a `prSession` with `local: true` and no provider, token or PR number. This was a smaller change than a new type. The places that assumed a forge were adjusted:

- `Server.forgePR()` (`s.pr != nil && !s.pr.local`) now guards the git panel's push, pull and unpushed list, which otherwise would try to reach a forge. A local review behaves like a plain workspace there.
- `handlePRExistingComments` returns empty lists when there is no provider. Posting, replying and submitting were already refused for a session with no token.
- `prThreadContext` says "a local review of `head` against `base`" and speaks of "the reviewed change" instead of a pull request, and appends the review block.
- `handleMeta` / `handlePRMeta` carry `local`, and `meta.review` is set when a review is loaded.

The coupling is debt: a later change should split the provider-backed parts out of `prSession`.

### 3.5 Loading and checking (`review.go`)

1. **Parse** (`parseReview`): size-capped read (2 MiB), decode into top-level fields plus raw comments, then validate each comment on its own (`validateReviewComment`). A bad one goes to `Rejected` with a reason. Fields the file must not set (`kind`, `status`, `origLine`, `outsideDiff`) are reset after decoding.
2. **Check** (`reviewState.resolve`), once per load, in the background: list the files at head (and at the merge-base for `LEFT` comments) with one `git ls-tree`, reject comments whose file is not there, read each distinct file once (`git cat-file -s` then `git show`, skipping files over 4 MiB or binary), and for each line comment verify the range and the `anchor` text (`locateAnchor`): matched at the stated line, or exactly once within ±25 lines (moved), or neither (shown at file level). Replies inherit their parent's final location. A `headSHA` that does not prefix the real head marks the review stale.
3. **Serve** (`GET /api/review`): a snapshot with an ETag. Comments are withheld until the first check finishes. After a reload the previous checked comments stay until the new file has been checked.

### 3.6 Frontend

`review.js` builds the comment markup; `pr.js` owns the state and the panel. The git shell-outs the check needs (`revParseCommit`, `gitTreeFiles`, `gitFileLines`) live in `git.go` with the others.

- Agent comments are mapped into the shape GitHub's inline comments have, so the existing gutter markers (`renderMarkersForActiveDoc`) and thread grouping apply to them. A file-level comment (no line) has no row to mark and appears only in the panel.
- The panel gets a Review section first: title, verdict, banners (checking, stale, load error, warnings, rejected comments), the summary, and comments with no file. The panel opens by itself the first time there is something in it.
- Comment text goes through the renderer the thread pane already uses (`thrMd`, [Threads](threads.md)), via `thrMdNoImages`: headings, lists, tables, quotes, code blocks (a `suggestion` fence is labelled as such), links, and `path:line` links that open the file. Images are rendered as links and never loaded, because the CSP allows `https:` images and an injected image URL could otherwise carry repository data out when the comment rendered. A code block's copy button and file links share `thrMdClick` with the thread pane rather than being wired twice.
- **Discuss** calls `newThread()` with a prefilled, quoted message. A head-side line comment is anchored to its lines; a general, file-level or `LEFT` comment starts an unanchored thread that says where the comment was.
- For a local review the bar shows a "Review" badge, and the controls that submit to a forge (Submit Review, the conversation composer) are hidden.

### 3.7 Skill

[`skills/px0-review/SKILL.md`](../../skills/px0-review/SKILL.md) tells an agent how to write the file (with an `anchor` on every line comment), where to put it (outside the repo), and how to launch px0. `px0 -review-schema` lets it check the format against the installed binary.

## 4. Security

- No new agent-facing write endpoint. The file is the only input channel, and `/api/review` rejects everything but GET.
- Review text is untrusted. It is escaped before anything is built from it, links are `http(s)` only with `rel="noopener noreferrer"`, and images never load.
- Comment paths are validated (`cleanReviewPath`: clean, relative, no `..`, no backslash) and then looked up in the git tree rather than opened from disk, so a path cannot leave the repository.
- `generatedBy.sessionId` stays on the server and never appears in a prompt or a response.
- The thread context labels the review as claims to verify.

## 5. Not built yet

- Reference snippets (`refs` are validated and listed, not resolved) and `GET /api/review/ref`.
- Same-session handoff through `generatedBy.sessionId` (`claude --resume … --fork-session`). Whether a session resumes from a different working directory is unverified.
- Saved triage (accepted / dismissed), and promoting an agent comment to a GitHub draft for `-review` with a PR URL.
- A range comment's `endLine` is only used for display and for the thread anchor; the source view does not map head lines to working-tree lines when the reviewer has local edits, so those comments appear in the diff view and the panel.
- Splitting the provider-backed parts out of `prSession`.

## 6. Tests

[`review_test.go`](../../review_test.go) covers whole-file errors, the size and comment limits, per-comment rejection (including a file trying to set its own status), path cleaning, anchor matching, the checks against a real repository (anchored, moved, out of range, unanchored, missing path, `LEFT` side, replies, files outside the diff), staleness, the ETag and live reload (including a file that stops parsing), in-place sessions never removing the repository, worktree cleanup on close, refused revisions, the thread context, and the embedded schema.

## 7. Open decisions

- Whether the skill should also live in `px0-ai/harness`.
- Whether the session handoff should ever be on by default.
