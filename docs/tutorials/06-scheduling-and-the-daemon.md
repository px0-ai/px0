# Scheduling and the daemon

A workflow with a `trigger.schedule` doesn't fire on its own -- something
has to be watching the clock. That something is `px0d`, a deliberately
dumb scheduler: it polls `workflows/`, evaluates cron expressions in
machine local time, spawns `px0 workflows run <id> --quiet` for anything due, and
runs one housekeeping pass a day.

## 1. Give a workflow a schedule

```yaml
trigger: {schedule: "0 16 * * 5"}      # Fridays at 16:00 local time
```

Standard five-field cron. Invalid expressions are rejected when the
workflow is validated, not silently ignored at fire time.

## 2. Install the daemon

```shell
px0 daemon install
```

px0 picks the mechanism for your OS: **launchd** on macOS, **systemd**
(user session) on Linux when one is available, **cron** everywhere else.
Force the cron fallback with `--fallback-cron` if you'd rather not use
the native one.

```shell
px0 daemon status
px0 daemon start | stop | restart
```

`status` reports whether the process is actually alive -- it signals the
recorded pid rather than trusting the pidfile -- plus the last fire per
workflow and each scheduled workflow's next upcoming fire.

To run it in the foreground instead (useful while you're debugging a
schedule):

```shell
px0 daemon serve
```

## 3. What it does while it runs

The loop polls every 30 seconds. On each tick it spawns anything due; on
the first tick of a new calendar day it also runs the nightly pass:

| Nightly step | What it does |
| --- | --- |
| Checkpoint scan | Records any hand edits you made to versioned files |
| Reindex | Rebuilds the retrieval index over `knowledge/` |
| Ingest queue | Drains queued YouTube playlist jobs into knowledge files |
| Retention | Deletes run logs past `logs.retention_days` |
| Update check | Once a week, checks PyPI for a newer px0 |

Every one of those steps is fault-isolated: a broken index or an
unreachable playlist is recorded in the report and the rest of
housekeeping still runs.

### Missed fires

If the machine was asleep or the daemon was down, the fires it missed
are not silently dropped. On startup and on every tick, px0 compares
each workflow's schedule against its last recorded fire and runs what it
owes you, marking anything discovered well after it was due as *late* in
the run record. So a laptop that was closed on Friday afternoon still
gets its Friday digest when it wakes.

## 4. Watch what it's doing

```shell
px0 daemon logs
px0 daemon logs --follow      # tail it live; Ctrl-C to stop
```

```
2026-08-20T09:00:00+00:00 start: serve started
2026-08-20T09:00:30+00:00 tick: spawned post-standup (on-time)
2026-08-20T09:30:00+00:00 nightly: started housekeeping
2026-08-20T09:30:04+00:00 nightly: checkpoint=2 changed, reindexed=418 passages, retention removed 3 logs
```

Timestamps are UTC; cron schedules are evaluated in machine local time.

The log lives under `logs.path`. There's no rotation yet, so if it ever
gets unwieldy, truncate it -- nothing reads it back.

For an individual run rather than the daemon:

```shell
px0 runs logs <run-id>
px0 runs logs <run-id> --follow
```

`--follow` on a run that has already finished prints what's there and
returns immediately rather than hanging, so it's safe to use on any run
id without checking first.

## 5. Scheduled runs are just runs

Everything a scheduled fire does is recorded exactly like a manual run,
with `trigger: schedule` on the record:

```shell
px0 runs list --workflow post-standup
px0 runs list --failed --since 7d
px0 runs why <run-id>
```

That's the loop for debugging a schedule that misbehaved: find the run,
read what it actually did, fix the workflow file, rerun it by hand.

## Next

- [07-browsing-runs.md](07-browsing-runs.md) -- the interactive way to do
  the last step.
- [04-knowledge-and-ask.md](04-knowledge-and-ask.md) -- what the nightly
  reindex is maintaining.
