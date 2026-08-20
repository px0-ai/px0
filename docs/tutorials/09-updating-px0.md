# Updating px0

px0 is distributed on PyPI and installed with pipx, so updating is a
version check against PyPI's JSON API plus a `pipx upgrade`. `px0 update`
wraps both, plus the parts you'd forget: store-schema migrations,
restarting a running daemon, and a health check afterwards.

## 1. Check without changing anything

```shell
px0 version
px0 update --check
```

```
Update available: 0.1.2 on channel stable.
```

`--check` touches nothing on disk. It reads the published versions from
PyPI and compares them against the installed one.

The daemon also checks once a week during its nightly pass and records
the answer, which is what `px0 doctor` reports -- so neither command
hits the network on every invocation, and `doctor` stays usable offline:

```
✓ update  0.1.0; 0.1.2 available as of 2026-08-20 -- run `px0 update`
```

Being a release behind is reported, never treated as a failure.

## 2. Update

```shell
px0 update
```

In order, it:

1. Resolves the newest version on your channel.
2. Detects how px0 was installed -- pipx or pip -- and upgrades with that
   mechanism. `pipx upgrade px0`, or `pip install --upgrade px0`.
3. Applies any pending store-schema migrations, recording each as a
   change in the store's own history. A migration that fails stops the
   update there, with the store left at the last schema it fully
   reached -- it never half-migrates.
4. Appends the result to `.state/update-history.json`.
5. Restarts the daemon if it was running, so the new binary is what's
   scheduling.
6. Runs a quick `doctor` pass and prints the summary.

Nothing is written to the history unless the install itself succeeded, so
a failed update leaves no misleading breadcrumb.

## 3. Roll back

```shell
px0 update rollback
```

This reinstalls the version you were on before the last update -- read
from `update-history.json`, not guessed -- and pops that entry, so
repeated rollbacks walk back through your history one step at a time.
With no history, it says so and exits rather than doing something
arbitrary.

One caveat it prints when it applies: **schema migrations are
forward-only**. If the update you're undoing migrated the store, the
older binary is now looking at a newer store schema. `px0 doctor`'s
`schema` check is what tells you:

```
✗ schema  store schema 2, binary schema 1
```

The fix is to update forward again. Roll back for a misbehaving release,
not as a way to downgrade the store.

## 4. Channels

```shell
px0 update --check --channel beta
px0 config set update.channel beta
```

`stable` (the default) considers final releases only. `beta` includes
pre-releases, and installs them with `--pre`.

## 5. Uninstalling

```shell
sh install.sh --uninstall
```

This removes the binary and then *tells you* how to delete your store:

```
To remove all local configurations and history, run:
  rm -rf ~/.px0
```

It never deletes the store itself. `~/.px0` is a plain directory of
Markdown and TOML -- your guidelines, your library, your run history --
so removing it is your call, and it's yours to back up or move somewhere
else first.

```shell
px0 store export <dir>     # portable copy of the store
```

## Next

- [01-getting-started.md](01-getting-started.md) -- the install knobs
  (`PX0_VERSION`, `PX0_CHANNEL`, `PX0_PREFIX`, `PX0_NO_DAEMON`).
- [06-scheduling-and-the-daemon.md](06-scheduling-and-the-daemon.md) --
  the nightly pass that does the weekly update check.
