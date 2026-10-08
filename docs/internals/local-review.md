# Local Review: Agent-Authored Reviews in px0

> **Status: proposed. Not implemented.** This document describes the design for `px0 -review`. The file format is specified separately in [Review File Specification](review-file-spec.md). Behaviour described here as "today" refers to the code at the time of writing.

## 1. Problem

A common workflow is to ask a coding agent to review a pull request or the diff between two branches. The agent answers in the terminal with comments tied to specific lines, general comments, and references to code on other branches. That is hard to read next to the code.

px0 already has the review UI: a merge-base diff view, gutter comment markers, a comments panel with threads, draft comments, and PR-scoped chat [threads](threads.md). But it is gated on a GitHub pull request:

- `prSession` is built only by `checkoutPR` from a forge URL (`pr.go`, `main.go`).
- The only comments it can show come from GitHub (`handlePRExistingComments`) or from the reviewer's own in-memory drafts.
- There is no way to review two local branches, and no way for an agent to hand px0 its comments.

Goal: an agent finishes a review, writes the comments to a file, and launches `px0 -review <file>`. The author reads the comments inline on the diff, follows references to other branches, and discusses any comment in a px0 thread that has the review's context.

## 2. Constraints from px0's tenets

| Tenet ([agents guide](../agents/README.md)) | Consequence for this design |
|---|---|
| Edits go through a harness, never px0 | The review file is read-only data. No endpoint accepts file content. Suggestions are previews until a harness applies them. |
| Nothing written into a working tree | The review file lives outside the repo. A needed checkout goes in the OS temp directory (the `checkoutPR` pattern). Triage state goes in the existing session store under the config directory. |
| Single static binary, no runtime deps | `encoding/json` only. The JSON Schema is embedded with `go:embed`. |
| Performance budgets | Parsing and anchor resolution run after the listener is up, in the background. Per-path lookups, lazy ref snippets, capped sizes, nothing on the scroll path. |

## 3. Design

### 3.1 Launch

```bash
px0 -review review.json                       # base/head come from the file
px0 -review review.json -base main -head feature
px0 <pr-url> -review review.json              # a real PR plus the agent's comments
px0 -review-schema                            # print the embedded JSON Schema
```

It is a flag and not a subcommand because `px0 pr` was removed on purpose (`main.go`).

### 3.2 Why a file, not a live API

`localPost` (`lspsetup.go`) requires an `Origin` header that matches the request host. That is the CSRF and DNS-rebinding guard for every mutating endpoint, and it means an agent's `curl` is rejected by design. A review channel should not weaken it. A file needs no port discovery either: px0 walks to a free port, so a live API would force the agent to scrape stdout.

Updates come from re-reading the file when its mtime or size changes. Agents write atomically (temp file plus rename).

### 3.3 Workspace and diff base

- If `head` resolves to the checked-out `HEAD`, px0 uses the repo in place: no checkout, instant, language servers work on the real tree.
- Otherwise it runs `git worktree add --detach` into an `os.MkdirTemp("px0-review-*")` directory and removes it on exit, as `prSession.Close` does today.
- `diffBase = gitMergeBase(root, head, base)`.
- That value goes through the existing `Server.SetPR` path: `diffBase`, `ix.SetDiffBase`, `ix.SetPRHead`, `PRFiles`. The diff view's "PR changes" vs "Your changes" split then works unchanged.

### 3.4 Reusing `prSession` with no provider

A local review is a `prSession` with `provider == nil`. This is a smaller change than a new type, and `Pull` already tolerates a nil provider. The handlers that call `p.provider` must gain nil guards: `handlePRExistingComments`, `handlePRIssueCommentPost`, `handlePRReviewCommentReply`, `handlePRSubmit`. `handlePRMeta` must report `readOnly: true`. The coupling is debt: a later change should split the provider-backed parts out of `prSession`.

### 3.5 Server

New file `review.go`:

- **Loader**: size-capped read, strict decode, per-comment validation, `rejected[]`, a stderr summary.
- **Anchor pass**: background, bounded, memoised (spec §6). A ready signal tells `/api/review` whether statuses are final.
- **`GET /api/review`**: the normalised review with an ETag from mtime and size.
- **`GET /api/review/ref?id=&i=`**: a read-only snippet. The server resolves only refs listed in the loaded file.
- **Thread context**: `prThreadContext` (`server.go`) appends the review block from the spec's [§9](review-file-spec.md#9-in-chat-discussion).

### 3.6 Frontend

New `web/src/review.js` plus hooks into existing modules:

- Map review comments into the `reviewComments` shape in `pr.js`, with the agent as `author` and added `severity` and `status`. The gutter markers (`renderMarkersForActiveDoc`) and the comments panel then work as they do for GitHub comments.
- Add a severity chip, stale and rejected banners, and a Summary card for `summary` and path-less comments.
- **Discuss** on a comment opens a thread through the existing `setThreadHandler` in `selbar.js`.
- **Promote to draft** posts to the existing `/api/pr/comments`. It is only useful when a provider exists (`px0 <pr-url> -review …`). A range is promoted as a single-line draft at `endLine`, because `prComment` has no range field yet.
- Check `/api/review` on window focus and while the tab is visible, using the ETag. No timers while hidden, the same rule as the git stream.

Known limitation: comments belong to the review diff (base..head). If the reviewer has local edits that shift lines, the source view cannot map head lines to working-tree lines in v1; comments appear in the diff view and the panel.

### 3.7 Skill

A skill shipped with the repo (proposed: `skills/px0-review/SKILL.md`) tells an agent to:

1. Resolve `base` and `head` and pin `baseSHA` / `headSHA`.
2. Do the review with its normal tools.
3. Write `review.json` atomically to its scratch directory (outside the repo), including an `anchor` for every line comment.
4. Run `px0 -review <file>` in the background and tell the user the URL.
5. For later updates, rewrite the file. For discussion, point the user at **Discuss**.

`px0 -review-schema` lets the skill stay in sync with the installed binary.

## 4. Security

- No new agent-facing write endpoint. The file is the only input channel.
- Review text is untrusted and rendered through the sanitizer. Images are never loaded, because the CSP's `img-src https:` would otherwise let an injected image URL carry repo data out ([spec §7](review-file-spec.md#7-rendering-and-safety)).
- Client-supplied paths go through `safePath`. Refs are resolved on the server from the loaded file only, with `--end-of-options` and a `^{commit}` peel, so a rev such as `--upload-pack=…` cannot reach git as an option.
- The thread context labels the review as a claim to verify.

## 5. Phasing

1. **MVP**: `-review` flag, local base/head, loader and validation, inline comments, summary, severity, Discuss with the context block.
2. **Next**: anchor relocation and staleness, cross-branch refs, live reload.
3. **Later**: session-fork handoff, promote-to-draft bridge, splitting the provider coupling out of `prSession`, triage state in the thread context.

## 6. Test plan

- **Go unit tests** (`review_test.go`): size and count limits, per-comment rejection, duplicate ids, anchor exact / moved / unanchored, staleness, `endLine < line`, traversal paths, ref sandbox (leading `-`, unknown rev, path escape), nil-provider PR handlers, `/api/review` ETag.
- **Build checks**: `go test ./...`, `go build -o px0 .`, `node ./scripts/build-web.js`.
- **Manual, scratch repo with two branches**: a review with line, file-level, general, `LEFT`, suggestion and cross-branch-ref comments. Check markers, panel, chips and the ref snippet. Edit the file and confirm live reload keeps expanded state. Change `headSHA` for the stale banner. Click **Discuss** and confirm (with `-verbose`) that the prompt carries the context block. Confirm the repo's `git status` is clean and the temp worktree is gone after Ctrl+C.
- **Security**: a comment containing `![](https://example.com/x.png)` and `<script>` renders inert and makes no network request.
- **Performance**: with a 2000-comment file, time to listen is unchanged and RSS stays near 20–30 MB.

## 7. Docs to update when implemented

Per the [documentation matrix](../agents/README.md): [`github-pr-review.md`](github-pr-review.md), [`threads.md`](threads.md) (context block), the internals and features indexes, `README.md` and [`settings-and-configuration.md`](../features/settings-and-configuration.md) for the new flags, a user-facing `docs/features/local-review.md`, and the frontend code map in `docs/agents/README.md`.

## 8. Open decisions

- Flag name: `-review` (matches the flag-over-subcommand precedent).
- Skill home: the px0 repo, since the schema is embedded in the binary; the alternative is `px0-ai/harness`.
- Handoff default: the context block always; session fork only when explicitly enabled, until fork-and-working-directory behaviour is verified.
