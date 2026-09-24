# @px0/sdk

Typed TypeScript client for a running px0 server. Zero dependencies — uses the global `fetch` (Node 18+, Bun, Deno, browsers). ESM only.

```ts
import { Px0 } from "@px0/sdk";

const px0 = new Px0({ url: "http://127.0.0.1:7777/" }); // include -base-path if set
await px0.ready(); // waits through startup and indexing

const hits = await px0.search("TODO", { glob: "*.go" });
const refs = await px0.lsp.references("server.go", 42, 10, { wait: 30_000 });

// Dispatch the selected coding harness at a line range and wait for it to finish
const job = await px0.agent.edit({ path: "server.go", l1: 40, l2: 60, instruction: "handle the nil case" });
const done = await px0.agent.wait(job.id);
console.log(done.error || done.changed);

for await (const ev of px0.events({ metrics: false })) {
  if (ev.type === "git-status") console.log(ev.data.gitChanges, "changed");
}
```

## Surface

| Area | Methods |
| --- | --- |
| Workspace | `meta` `ready` `metrics` `tree` `find` `file` `close` `raw` `text` `markdown` `diff` `gutter` `search` `outline` `def` `reindex` `events` |
| Settings / session | `settings` `updateSettings` `session` `updateSession` |
| `lsp` | `definition` `references` `hover` `symbols` `calls` `expandCalls` `warm` `setup` `install` `start` |
| `git` | `status` `log` `stage` `unstage` `commit` `pull` `push` `commitMessage` |
| `agent` | `harnesses` `select` `edit` `job` `wait` `cancel` |
| `pr` (px0 launched with a PR URL) | `meta` `comments` `drafts` `addDraft` `deleteDraft` `submit` `comment` `reply` `launch` |

Required arguments are positional; everything optional goes in a trailing options object, which always accepts `signal` and `timeout`. Anything not wrapped: `px0.request(method, path, { query, body })`.

Paths are workspace-relative. Lines are 1-based; LSP columns are 0-based UTF-16 offsets.

## Failure modes

| What happened | Rejects with |
| --- | --- |
| px0 answered non-2xx | `Px0Error` — `.status`, `.endpoint`, server's message |
| px0 not reachable | `Px0Error` with `.status === 0`, original error in `.cause` |
| Call exceeded its timeout | `DOMException` named `TimeoutError` |
| Your `signal` aborted | the signal's reason (`AbortError` by default) |

- **Timeouts**: 30s per call by default (`new Px0({ timeout })`, `0` disables). LSP calls stretch to cover their `wait`. `git.pull`/`git.push` default to 5 min since they talk to the remote. `agent.wait` and `events` have no overall timeout; stop them with a signal.
- **Cancellation**: aborting a `search` also stops the search on the server.
- **Events**: breaking out of the loop or aborting closes the connection. A stream that drops after connecting reconnects with backoff (250ms → 5s) and gets a fresh `git-status` on reconnect; a failed *first* connect throws. `reconnect: false` turns this off.
- LSP endpoints report language-server trouble in the body (`error`, `state`) with HTTP 200, not as a rejection.

## Performance

- **No gzip on loopback.** px0 compresses whenever asked and Node's fetch always asks; on 127.0.0.1 that is pure CPU. For loopback URLs the SDK sends `Accept-Encoding: identity` (override with `compress`). Median round trips against a 57k-file workspace:

  | Call | gzip | identity |
  | --- | --- | --- |
  | `text` (113 KB) | 0.77 ms | 0.20 ms |
  | `file` (1000 lines) | 0.83 ms | 0.34 ms |
  | `tree` | 0.25 ms | 0.15 ms |
  | `find` (500 results) | 2.69 ms | 2.14 ms |

- **Linear SSE parsing.** Each chunk is scanned once; a 3.1 MB `git-status` event (100k changed files) parses in ~12 ms.
- `events({ metrics: false })` uses the git-only stream and spares the server a metrics sample every 2.5s.
- `agent.wait` polls from 50ms, backing off to 500ms.
- Use `text()` rather than `file()` when you don't need highlighted HTML, and `close()` files you are done with to free server caches.

## Writes and browsers

px0 refuses POSTs whose `Origin` isn't px0 itself. The SDK sets it, which works from Node/Bun/Deno. In a browser it only works same-origin (e.g. a script served alongside px0) — that restriction is the CSRF guard, not a bug. Browsers also ignore `Accept-Encoding`, so `compress` has no effect there.

## Develop

```bash
npm run build                    # tsc -> dist/
PX0_BIN=../px0 npm test          # unit + e2e against a real binary (Node 23.6+); e2e runs when ../px0 or PX0_BIN exists
```
