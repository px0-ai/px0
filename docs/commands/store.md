# `px0 store`

The store as a whole: everything px0 knows, in one directory.

Implemented by `px0/store.py`.

```
px0 store list
px0 store export <dir>
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

`workflows/`, `guidelines/`, `brain/`, `output/`, `skills/`, `config.toml`, and
the parts of `.state/` that matter for continuity: version history, pending
proposals, the schema marker, and the schedule.

### What is excluded

Credentials, deliberately and thoroughly:

- `.state/credentials.toml` is not copied.
- `config.toml` is exported with every secret key blanked.
- `config.toml`'s **version history is dropped**, and the blobs only it
  referenced are deleted.

That last point is the one that matters: the Composio key is written to
`config.toml`, and `config.toml` is versioned, so redacting only the live file
would leave the key one `px0 versions show` away. Both are scrubbed, or
"credentials excluded" would be a false promise.

The retrieval index is not exported. Run `px0 brain reindex` after importing.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | The destination is unwritable |
| `4` | The version manifest could not be scrubbed, so the export was not completed |
