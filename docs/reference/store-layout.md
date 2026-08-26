# Store layout

Everything px0 knows lives in one directory: `~/.px0`, or wherever `PX0_HOME`
points. Created by [`px0 init`](../commands/init.md).

```
<store>/
  config.toml
  workflows/
  guidelines/
  memory/
  brain/
    docs/
    blogs/
    papers/
    work/
  output/
  tools/
  .state/
```

## Content you edit

These are plain Markdown. Edit them by hand and px0 picks the change up — there
is no compile step, and a hand edit is detected and recorded in the store's
history like any other change.

| Path | What it holds |
| ---- | ------------- |
| `workflows/` | One Markdown file per workflow: YAML frontmatter for trigger, tools, and output, prose for the instructions |
| `guidelines/` | One Markdown file per topic: `name` and `description` frontmatter, then the rules. Each `##` heading is a claim with its own id and history |
| `memory/` | One Markdown file per fact px0 knows about you: `kind`, `subject`, and `learned` frontmatter over the fact itself. See [`px0 memory`](../commands/memory.md) |
| `brain/` | What you have read and kept, as Markdown with frontmatter recording `source`, `retrieved`, `kind`, and `title` |
| `tools/` | Tools you declared yourself: one TOML file each, read at run time. `example.toml.sample` ships as a worked example and is not loaded — the loader only reads `*.toml` |

### Inside `brain/`

| Folder | What lands there |
| ------ | ---------------- |
| `docs/` | Documents, transcripts, and plain text — the default for most sources |
| `blogs/` | Web pages, fetched or saved |
| `papers/` | PDFs |
| `work/` | Private material, withheld from retrieval and never sent anywhere |

The folders are organisational. Retrieval walks `brain/` recursively and does not
care about the structure — the one exception is the private folder. That means
you can move a file between folders, or add folders of your own, without
breaking anything.

`brain.path` can point outside the store entirely, at an existing notes vault.
See [pointing the brain at a vault](../commands/brain.md#pointing-the-brain-at-an-existing-vault).

## Derived output

| Path | What it holds |
| ---- | ------------- |
| `output/` | What runs produced, when a workflow's output target is `file` |

`output/` is where `output.path` points by default.

## `.state/` — runtime internals

Not meant for hand-editing. Everything here is either derived and rebuildable, or
history px0 manages itself.

| Path | What it holds | Rebuildable |
| ---- | ------------- | ----------- |
| `.state/versions/` | The version manifest (SQLite) and content-addressed blobs | No — this is the history |
| `.state/index/index.sqlite` | The retrieval index over `brain/` | Yes — `px0 brain reindex` |
| `.state/ingest/` | Queued playlist ingest jobs, drained by the daemon | Yes |
| `.state/ingest/failed/` | Ingest jobs given up on after repeated failures, with the reason | — |
| `.state/credentials.toml` | Connector authorizations, mode 0600 | No |
| `.state/schema` | The store's on-disk schema version | No |
| `.state/schedule.json` | The daemon's persisted scheduling state | Yes |
| `.state/update-history.json` | What was installed when, and which migrations ran | No |
| `.state/update-check.json` | When px0 last checked for an update | Yes |
| `.state/retrieval-consent.json` | Whether the qmd backend's local models were consented to | Yes |
| `.state/running/` | One file per run in flight, holding its pid so `px0 runs cancel` can signal it | Yes — a dead marker is dropped when it is next read |
| `.state/catalogue.json` | Composio tool definitions discovered by `px0 workflows new`, so a workflow keeps working offline | Yes — `px0 tools refresh` |
| `.state/lock` | Process lock, so two px0 runs cannot write at once | Yes |
| `.state/approvals/` | Write calls drafted and waiting for a decision — see [`px0 approvals`](../commands/approvals.md) | No — these are decisions nobody has made yet |
| `.state/inbox/` | What scheduled runs delivered — see [`px0 inbox`](../commands/inbox.md) | No — unread entries are the only copy of the news |
| `.state/sessions/` | Conversations in progress — see [`px0 ask`](../commands/ask.md) | Yes — what mattered from one is in `memory/` |
| `.state/fixtures/` | What captured runs read, for [`px0 workflows replay`](../commands/workflows.md#px0-workflows-replay) | Yes — capture another run |
| `.state/sync-id` | This store's identity for [`px0 store sync`](../commands/store.md#px0-store-sync) | Yes, at a cost — a new id makes the next sync see every file as a conflict |

The retrieval index is derived data and is dropped and rebuilt whenever its
schema changes — for instance when the tokenizer or a column changes between
releases. `px0 doctor` reports an empty index and names `px0 brain reindex`.

Two of these are not runtime internals in the usual sense: `.state/approvals/`
and `.state/inbox/` hold things waiting on **you** rather than on px0, which is
why neither is rebuildable and why the nightly pass never drops an unread inbox
entry or a pending approval.

`.state/fixtures/` deserves a second look: it is the only place in px0 where
the *content* of a run's inputs is written down. Capture is off unless a
workflow asks for it, the folder is excluded from both `px0 store export` and
`px0 store sync`, and entries age out on `runs.fixture_keep_days`.

## Outside the store

| Path | What it holds |
| ---- | ------------- |
| `logs.path` (default `/var/log/px0`) | Run artifacts, kept outside the versioned store and pruned on the `logs.*` retention settings |

Three subfolders, each partitioned by date:

| Folder | What it holds | Kept for |
| ------ | ------------- | -------- |
| `records/` | One JSON summary per run: outcome, tool calls, inputs, cost, and your own mark on it | `logs.record_retention_days` (a year) |
| `runs/` | The raw log — full prompts and replies | `logs.retention_days` (a fortnight), or `logs.retention_days_failed` for a failure |
| `events/` | The structured event stream, one JSON object per line | with the raw log |

Runs that called a write tool are exempt from retention entirely: what they did
cannot be undone by forgetting it. Records outlive logs by design, so
`px0 runs list` and `px0 workflows health` still answer for a run whose prompts
are long gone.

## What `px0 store export` copies

Content, history, and configuration — `workflows/`, `guidelines/`, `memory/`,
`brain/`, `output/`, `tools/`, `config.toml`, and the parts of `.state/` that
matter for continuity — with the Composio API key stripped from both the live
`config.toml` and its version history. Connector authorizations are never
copied: they live in `.state/credentials.toml`, which an export does not touch.
The retrieval index is not copied; reindex after importing.

Load one back with
[`px0 store import`](../commands/store.md#px0-store-import), and check what
arrived with [`px0 store verify`](../commands/store.md#px0-store-verify). See
[`px0 store export`](../commands/store.md#px0-store-export).
