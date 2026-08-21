# `px0 update` and `px0 version`

Upgrade px0 in place, apply any pending store-schema migrations, and roll back if
an upgrade goes wrong.

Implemented by `px0/update.py`.

```
px0 update [--check] [--channel CHANNEL]
px0 update rollback
px0 version
```

---

## `px0 update`

Check PyPI for a newer release and install it through whichever mechanism
installed px0 — pipx or pip, detected automatically.

A successful update then, in order: applies pending schema migrations, appends
the result to `.state/update-history.json`, restarts a running daemon, and
finishes with a quick `doctor` pass.

### `--check`

Report whether an update is available and stop. Installs nothing.

- **Input:** flag, no value. Default off.

```shell
px0 update --check
```

### `--channel CHANNEL`

Which release channel to consult.

- **Input:** `stable` or `beta`.
- **Default:** `update.channel`, which ships as `stable`.
- Not functionally enforced in this build; `px0 update` reports when no release
  manifest is configured.

```shell
px0 update
px0 update --channel beta
```

### Schema migrations

The store has an on-disk schema version, recorded in `.state/schema`. When a px0
release changes the layout in a way an older store cannot read, it ships a
migration, and `px0 update` applies every one newer than the store's version, in
order.

Migrations are **forward-only**. They are recorded in the update history, and
each is committed to the store's own version history so `px0 changes` shows it.

If the schema marker cannot be read, the update refuses rather than assuming
version 1 — assuming would re-run every migration against a store that may
already be migrated.

The current schema is **2**. Version 2 renamed `knowledge/` to `brain/` and
`knowledge.path` to `brain.path`. Its migration moves the folder, rewrites the
config key, completes the folder layout, and drops the stale qmd collection the
rename would otherwise orphan. A brain kept outside the store — a notes vault —
is left exactly where it is.

---

## `px0 update rollback`

Reinstall the version recorded in the last update-history entry, and pop that
entry.

- **Arguments:** none.
- Schema migrations are **not** undone. When the update being rolled back had run
  migrations, the command says so explicitly, naming them — a store migrated
  forward and then run by an older px0 is the case to be careful about.

```shell
px0 update rollback
```

---

## `px0 version`

Print the installed px0 version, the schema version this binary expects, the
schema version on disk, the configured harness command, and whether that harness
is actually on `PATH`.

- **Arguments:** none.

```shell
px0 version
```

```
px0_version:           0.1.0
schema_version_binary: 2
schema_version_store:  2
harness_cmd:           claude -p
harness_found:         True
```

A store version behind the binary means a migration is pending: run `px0 update`.
A store version ahead means it was written by a newer px0, which cannot be
migrated backwards — install the newer px0 instead.

## Related configuration

| Key | Effect |
| --- | ------ |
| `update.channel` | Release channel |
| `update.check` | Whether the daemon checks weekly for an available update |
| `update.auto_install` | Install updates automatically instead of only surfacing them |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success, including "already up to date" |
| `1` | PyPI unreachable, the install failed, a migration failed, or nothing to roll back |
| `4` | The store's schema version could not be read |
