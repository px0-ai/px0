# `px0 daemon`

The daemon fires scheduled workflows and does the nightly housekeeping: a
hand-edit checkpoint scan, a brain reindex, draining queued playlist ingests, log
retention, and a weekly update check.

Implemented by `px0/daemon.py`.

```
px0 daemon install [--fallback-cron]
px0 daemon status
px0 daemon start
px0 daemon stop
px0 daemon restart
px0 daemon logs [--follow|-f]
px0 daemon serve
```

---

## `px0 daemon install`

Register the daemon with the operating system's service manager so it starts on
login — `launchd` on macOS, `systemd --user` on Linux.

### `--fallback-cron`

Install a crontab entry instead of a service unit.

- **Input:** flag, no value. Default off.
- For systems without the expected service manager, or where you would rather
  cron owned the schedule.

```shell
px0 daemon install
px0 daemon install --fallback-cron
```

---

## `px0 daemon status`

Whether the daemon is running, its pid, and when it last fired. No arguments.

## `px0 daemon start`

Start the daemon in the background. On start it also catches up any fires missed
earlier the same day. No arguments.

## `px0 daemon stop`

Stop the running daemon. No arguments.

## `px0 daemon restart`

Stop and start again — how to pick up a changed schedule or a new px0 version.
No arguments.

---

## `px0 daemon logs`

The daemon's own log, distinct from any individual run's.

### `--follow`, `-f`

Follow the log as it is written. Flag, no value. Default off.

```shell
px0 daemon logs -f
```

---

## `px0 daemon serve`

Run the scheduler in the foreground, logging to the terminal. No arguments.

This is what the installed service invokes. Run it directly to watch the
scheduler work or to debug a schedule without installing anything.

## Related configuration

Workflow schedules live in each workflow's frontmatter, not in `config.toml`. The
daemon's own persisted state is `.state/schedule.json`.

| Key | Effect |
| --- | ------ |
| `update.check` | Whether the nightly pass checks weekly for an available update |
| `update.auto_install` | Install updates automatically rather than only reporting them |
| `logs.*` | Retention applied by the nightly pass — see [`px0 runs`](runs.md#related-configuration) |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | No service manager available, already running, or not running when told to stop |
