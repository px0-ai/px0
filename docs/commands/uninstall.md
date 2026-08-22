# `px0 uninstall`

Stop the daemon, remove its scheduler unit, delete the entire store, and
uninstall the px0 package itself. This is the one command that removes px0
completely rather than changing what it does.

Implemented by `px0/cli.py`, reusing `px0/daemon.py`'s `uninstall` and
`px0/update.py`'s install-mechanism detection.

```
px0 uninstall [--yes]
```

---

## What it does

1. Stops the running daemon and removes its scheduler unit (the same as
   [`px0 daemon uninstall`](daemon.md#px0-daemon-uninstall)).
2. Deletes the store at `PX0_HOME` (`~/.px0` by default) — every workflow,
   guideline, brain file, credential, run record, and version history.
3. Uninstalls the px0 package itself, via `pipx` or `pip`, whichever
   installed it.

Each step only runs if there is something to do: a store that was never
initialized is skipped, and the package step still runs even without a
store.

## Options

### `--yes`

Skip the confirmation.

- **Input:** flag, no value. Default off.
- Without it, `px0 uninstall` prints exactly what it is about to do and asks
  once before touching anything. There is no partial undo: once the store is
  deleted, only a backup made with `px0 store export` can bring it back.

```shell
px0 uninstall
px0 uninstall --yes
```

## Relationship to `install.sh --uninstall`

`install.sh --uninstall` stops at the package and the scheduler unit,
deliberately leaving the store behind and telling you to run `rm -rf ~/.px0`
yourself. `px0 uninstall` is the same operation carried the rest of the way:
it removes the store too, with a confirmation in between.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Uninstalled, or cancelled at the confirmation |
| `1` | The confirmation could not be asked (no terminal, no `--yes`), or the package uninstall failed |
