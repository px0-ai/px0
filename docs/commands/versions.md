# `px0 versions`

px0 keeps its own history of every file in the store, so you can see what changed
and undo it without needing git. `versions` is the per-file view;
[`changes`](changes.md) is the cross-file view of the same history.

Implemented by `px0/versioning.py`. History lives in `.state/versions/`: a
SQLite manifest plus content-addressed blobs.

```
px0 versions list <path> [--json]
px0 versions show <path>@v<N>
px0 versions diff <path> <v1> <v2>
px0 versions revert <path> --to VERSION
px0 versions prune [--dry-run]
```

---

## `px0 versions list`

Every recorded version of one file: its number, when it was written, and which
actor wrote it.

### `path` (required)

- **Input:** a store-relative path, for example
  `workflows/friday-pr-digest.md` or `guidelines/commit-style.md`.

### `--json`

Print the versions as JSON, and nothing else.

```shell
px0 versions list workflows/friday-pr-digest.md
```

---

## `px0 versions show`

Print one version's full content.

### `ref` (required)

- **Input:** `<path>@v<N>` — the path, `@`, and the version number.

```shell
px0 versions show workflows/friday-pr-digest.md@v3
```

---

## `px0 versions diff`

A unified diff between two versions of one file.

### `path` (required)

- **Input:** a store-relative path.

### `v1` (required) and `v2` (required)

- **Input:** two version numbers, as integers. `v1` is the left side.

```shell
px0 versions diff workflows/friday-pr-digest.md 2 3
```

---

## `px0 versions revert`

Restore a file to an earlier version. Recorded as a new version rather than
rewriting history, so the revert is itself undoable.

### `path` (required)

- **Input:** a store-relative path.

### `--to VERSION` (required)

- **Input:** a version number, with or without a leading `v` — `3` and `v3` are
  both accepted.

```shell
px0 versions revert workflows/friday-pr-digest.md --to v2
```

---

## `px0 versions prune`

Drop versions beyond the configured cap and delete the blobs nothing references
any more. Blobs are content-addressed and shared, so one is removed only when no
surviving version points at it.

Does nothing while `versions.keep_all` is `true`, which is the default.

### `--dry-run`

Report what would be dropped without deleting anything.

- **Input:** flag, no value. Default off.

```shell
px0 config set versions.keep_all false
px0 config set versions.max_versions_per_file 50
px0 versions prune --dry-run
px0 versions prune
```

## Related configuration

| Key | Effect |
| --- | ------ |
| `versions.keep_all` | `true` keeps every version forever; `false` enables the cap |
| `versions.max_versions_per_file` | Per-file cap applied by `prune` when `keep_all` is `false` |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown path, unknown version, malformed `@v` reference |
| `4` | The version manifest is unreadable or inconsistent |
