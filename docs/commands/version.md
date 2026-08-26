# `px0 version`

What is installed, and whether it can run.

```
px0 version
px0 --json version
```

One of the flat commands: it reports on the install rather than on anything in
the store, so there is no entity to put in front of it. It is also the one
command that works before `px0 init` — a store it cannot find is reported as
`schema_version_store: None` rather than an error.

More than a version string:

| Field | What it tells you |
| ----- | ----------------- |
| `px0_version` | The installed release |
| `schema_version_binary` | The store layout this build expects |
| `schema_version_store` | The layout the store on disk actually has. A mismatch is what `px0 update` migrates |
| `harness_cmd` | The coding agent px0 shells out to |
| `harness_found` | Whether that command is on `PATH` right now |

`harness_found: false` is the usual reason a run fails immediately, and it is
worth checking here before anywhere else.

## `--json`

`version` takes no flags of its own. `--json` is the global one, so it goes
**before** the subcommand:

```shell
px0 --json version
```

`px0 version --json` is an error, which is true of every global flag.

## Related

- [`px0 update`](update.md) — what a newer release would change, and migrating the store to it.
- [`px0 doctor`](doctor.md) — the same version alongside every other health check.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Always |
