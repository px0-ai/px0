# `px0 store`

The store as a whole: everything px0 knows, in one directory.

Implemented by `px0/store.py`.

```
px0 store list
px0 store export <dir>
px0 store import <dir> [--merge] [--force]
px0 store path [--json]
px0 store verify [--json]
```

---

## `px0 store list`

Workflows, guidelines, and brain files in one pass, each under its own heading.

- **Arguments:** none.
- The same printers as [`px0 workflows list`](workflows.md#px0-workflows-list),
  [`px0 guidelines list`](guidelines.md#px0-guidelines-list), and
  [`px0 brain list`](brain.md#px0-brain-list). A single-entity listing prints no
  heading, because the command you typed already said what you are looking at.

```shell
px0 store list
```

---

## `px0 store export`

Copy the store's content and version history somewhere else — the supported way
to move a store to another machine.

### `dir` (required)

Where to write the export.

- **Input:** a directory path. Created if it does not exist.

```shell
px0 store export ~/backups/px0-2026-08-21
```

### What is included

`workflows/`, `guidelines/`, `brain/`, `output/`, `config.toml`, and
the parts of `.state/` that matter for continuity: version history, the schema
marker, and the schedule.

### What is excluded

Credentials, deliberately and thoroughly:

- `.state/credentials.toml` is not copied.
- `config.toml` is exported with every secret key blanked.
- `config.toml`'s **version history is dropped**, and the blobs only it
  referenced are deleted.

That last point is the one that matters: the Composio key is written to
`config.toml`, and `config.toml` is versioned, so redacting only the live file
would leave the key one change-log entry away. Both are scrubbed, or
"credentials excluded" would be a false promise.

The retrieval index is not exported. Run `px0 brain reindex` after importing.

## `px0 store sync`

Bring this store and a shared folder into line, on more than one machine.

`export` and `import` copy a whole store, which is the right answer once —
setting up a second machine — and the wrong answer every time after, because it
overwrites. So people pointed Dropbox at `~/.px0` instead, and that has a
specific, quiet failure: the version history is a SQLite database, and two
machines writing it produce a file that is neither machine's history. Nothing
notices until a revert needs it.

This is the deliberate version. Content is Markdown, so it merges file by file.
History does not merge and is not pretended to: each machine keeps its own.

```shell
px0 store sync ~/Dropbox/px0-shared --dry-run   # what would move
px0 store sync ~/Dropbox/px0-shared
px0 store sync ~/Dropbox/px0-shared --pull      # take only
px0 store sync ~/Dropbox/px0-shared --push      # send only
```

`workflows/`, `guidelines/`, `memory/`, `brain/`, and `tools/` travel.
`.state/` never does — it holds the history, credentials, the retrieval index,
and every queue, all of them either machine-specific or unmergeable.

### Three rules, each because the alternative loses work

**Nothing is overwritten silently.** A file changed on both sides is written
beside yours as `<name>.md.conflict-<machine>` and reported. Two versions are
two decisions, and px0 is not in a position to know which one you meant. The
marker goes after the extension on purpose: a `.md` file in `workflows/`
carrying the same `id:` would be loaded as a second workflow, and which of the
two you got would depend on directory order.

Once both sides hold the same bytes — because you resolved it, or because they
converged — that is recorded as agreement, so the next ordinary edit syncs
rather than reporting the same conflict again.

**A sync never deletes.** A file missing on one side may be one that side has
not pulled yet, and treating absence as deletion is how a sync loses work.

**The remote is just a directory.** Whatever puts it on both machines —
Dropbox, iCloud, a git repo, a USB stick — is your business and none of px0's.
It is created for you on the first sync if its parent exists; a path whose
parent is also missing is refused, because that is what a typo looks like and
the wrong answer would be a store syncing somewhere nothing else can see.

`px0 doctor` reports when the store itself looks like it sits inside a syncing
folder, since that is the case this command exists to replace.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | The destination is unwritable |
| `4` | The version manifest could not be scrubbed, so the export was not completed |

---

## `px0 store import`

Load an exported store into this one: the inverse of `export`.

### `dir` (required)

Where the export is.

- **Input:** a directory produced by `px0 store export`. px0 refuses anything
  that does not look like one.

### `--merge`

Add what is missing and keep everything already here.

- **Input:** flag, no value. Default off.
- Without `--merge` or `--force`, importing into an existing store stops rather
  than silently overwriting workflows you are running.

### `--force`

Let the import win on a collision.

- **Input:** flag, no value. Default off.

```shell
px0 store import ~/backups/px0-2026-08-21
px0 store import ~/backups/px0-2026-08-21 --merge
```

An export contains no credentials, so importing never blanks the API key on the
machine you are importing into: a `config.toml` already present is kept, and the
Composio key is set separately with
[`px0 config composio`](config.md#px0-config-composio).

---

## `px0 store path`

Print where the store is.

- **Arguments:** none.
- `--json` prints every path px0 uses: config, workflows, guidelines, brain,
  output, tools, state, and logs.

```shell
px0 store path
px0 store path --json | jq .brain
```

---

## `px0 store verify`

Check the store's own consistency, and say what fixes each problem found.

- **Arguments:** none.
- `--json` prints the report as one object.

Separate from [`px0 doctor`](doctor.md), which asks whether the install is wired
up. This asks whether the store's contents still hang together:

| Check | Problem it finds |
| ----- | ---------------- |
| workflows parse | A file that no longer has valid frontmatter |
| guideline references | A workflow naming a `guidelines/` file that is gone |
| version blobs | A recorded version whose content is missing from the object store |
| user tools | A malformed TOML file in `tools/` |

```shell
px0 store verify
```

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success, and `verify` found nothing |
| `1` | The import source is not an export, or a store already exists and neither `--merge` nor `--force` was given |
| `4` | `verify` found a problem |
