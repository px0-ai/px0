# `px0 changes`

A change is one atomic write across the store — a workflow build, a reviewed
batch of guideline proposals, a migration. Where [`versions`](versions.md) shows
one file's history, `changes` shows the log of what happened, and lets you undo a
whole change at once.

Implemented by `px0/versioning.py`.

```
px0 changes list [--since WHEN] [--actor ACTOR] [--json]
px0 changes show <change_id>
px0 changes revert <change_id>
```

---

## `px0 changes list`

The change log, newest first: id, when, actor, and how many files each touched.

### `--since WHEN`

Only changes after a point in time.

- **Input:** a relative span — `<n>d`, `<n>w`, or `<n>h`. A leading minus is
  accepted, since "`-7d`" reads naturally as seven days back. Absolute dates are
  not accepted.

### `--actor ACTOR`

Only changes made by one actor.

- **Input:** an actor name. px0 records `builder` for workflow and guideline
  builds, `update` for schema migrations, and `user:manual` for hand edits it
  detects on its checkpoint scan.

### `--json`

Print the log as JSON, and nothing else.

```shell
px0 changes list
px0 changes list --since 7d --actor builder
```

---

## `px0 changes show`

One change in full: every file it touched, with the diff for each.

### `change_id` (required)

- **Input:** a change id, as printed by `changes list`.

```shell
px0 changes show chg_20260821-093000-a1b2
```

---

## `px0 changes revert`

Undo an entire change — every file it touched, together, in one new change. The
revert is itself recorded, so it can be undone in turn.

### `change_id` (required)

- **Input:** a change id.

```shell
px0 changes revert chg_20260821-093000-a1b2
```

Schema migrations are forward-only and are not undone by this. `px0 update
rollback` reinstalls a previous px0 version and says so explicitly when the
update it is rolling back had run migrations.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown change id |
| `4` | The version manifest is unreadable or inconsistent |
