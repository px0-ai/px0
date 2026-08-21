# Store layout

Everything px0 knows lives in one directory: `~/.px0`, or wherever `PX0_HOME`
points. Created by [`px0 init`](../commands/init.md).

```
<store>/
  config.toml
  workflows/
  guidelines/
  brain/
    docs/
    blogs/
    papers/
    work/
  output/
  skills/
  .state/
```

## Content you edit

These are plain Markdown. Edit them by hand and px0 picks the change up — there
is no compile step, and a hand edit is detected and recorded in the store's
history like any other change.

| Path | What it holds |
| ---- | ------------- |
| `workflows/` | One Markdown file per workflow: YAML frontmatter for trigger, tools, and output, prose for the instructions |
| `guidelines/` | One Markdown file per topic; each `##` heading is a claim with its own id and history |
| `brain/` | What you have read and kept, as Markdown with frontmatter recording `source`, `retrieved`, `kind`, and `title` |

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
| `skills/` | Guidelines compiled into agent skill bundles by `px0 skills build` |

`skills/` is rebuilt from `guidelines/`, so edit the guideline rather than the
bundle. `output/` is where `output.path` points by default.

## `.state/` — runtime internals

Not meant for hand-editing. Everything here is either derived and rebuildable, or
history px0 manages itself.

| Path | What it holds | Rebuildable |
| ---- | ------------- | ----------- |
| `.state/versions/` | The version manifest (SQLite) and content-addressed blobs | No — this is the history |
| `.state/proposals/` | Guideline edit proposals awaiting review | No |
| `.state/index/index.sqlite` | The retrieval index over `brain/` | Yes — `px0 brain reindex` |
| `.state/ingest/` | Queued playlist ingest jobs, drained by the daemon | Yes |
| `.state/ingest/failed/` | Ingest jobs given up on after repeated failures, with the reason | — |
| `.state/credentials.toml` | Connector secrets, mode 0600 | No |
| `.state/schema` | The store's on-disk schema version | No |
| `.state/schedule.json` | The daemon's persisted scheduling state | Yes |
| `.state/update-history.json` | What was installed when, and which migrations ran | No |
| `.state/update-check.json` | When px0 last checked for an update | Yes |
| `.state/retrieval-consent.json` | Whether the qmd backend's local models were consented to | Yes |
| `.state/lock` | Process lock, so two px0 runs cannot write at once | Yes |

The retrieval index is derived data and is dropped and rebuilt whenever its
schema changes — for instance when the tokenizer or a column changes between
releases. `px0 doctor` reports an empty index and names `px0 brain reindex`.

## Outside the store

| Path | What it holds |
| ---- | ------------- |
| `logs.path` (default `/var/log/px0`) | Run logs, kept outside the versioned store and pruned on the `logs.*` retention settings |

## What `px0 store export` copies

Content, history, and configuration — with credentials stripped from both the
live `config.toml` and its version history. The retrieval index is not copied;
reindex after importing. See
[`px0 store export`](../commands/store.md#px0-store-export).
