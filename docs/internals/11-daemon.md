# 11. The scheduler

Module: `px0/daemon.py`

The daemon is deliberately dumb. It watches `workflows/`, evaluates cron schedules, spawns `px0 workflows run` as a detached subprocess, polls watched workflows, and runs one housekeeping pass a day. It holds no state a restart cannot rebuild.

## The loop

```python
recover_missed_fires(home, config)
last_nightly = None
while True:
    time.sleep(poll_interval)
    state = load_schedule_state(home)
    tick(home, config, state)
    today = datetime.now().date()
    if today != last_nightly:
        run_nightly(home, config)
        last_nightly = today
```

`POLL_INTERVAL_SECONDS` is 30. State lives in `.state/schedule.json` and is reloaded every tick rather than held in memory, so an external edit or a second process is picked up.

Three signals are handled. `SIGTERM` and `SIGINT` remove the pidfile and exit, so `status()` never reports a stale pid. `SIGCHLD` reaps spawned runs so they do not linger as zombies.

## Evaluating a schedule

`_due_fires(schedule, last_fire, now)` returns every cron fire time between the later of the last recorded fire or today's midnight, and now.

Starting at midnight rather than at the last fire bounds recovery to today. A daemon that has been down for a week should not wake up and fire seven days of backlog at once.

With no recorded fire it starts one second before midnight, so a fire exactly at midnight is still included.

## Timezones

Schedules used to be evaluated in whatever the machine's local time happened to be, which is right until the machine moves. A laptop taken two timezones over silently shifts every 9am report by two hours, and the same thing happens twice a year without moving at all.

`resolve_zone(config, wf)` takes the workflow's own `trigger.timezone`, falls back to `schedule.timezone`, and returns `None` when neither is set -- which keeps the old behaviour of following the machine, deliberately, because that is what an existing store expects.

A misspelled zone is refused at validation time rather than falling back silently. A schedule that quietly fires at the wrong hour looks like it worked, which is the exact failure the setting exists to prevent.

Mixing aware and naive timestamps raises rather than being wrong quietly, so `_due_fires` normalizes a stored fire from before zones existed onto whichever clock the caller is using:

```python
if (last_fire.tzinfo is None) != (now.tzinfo is None):
    last_fire = (last_fire.replace(tzinfo=now.tzinfo) if now.tzinfo
                 else last_fire.replace(tzinfo=None))
```

`status()` reads the next fire on the same clock the tick will use, or `px0 status` would promise a time the daemon has no intention of firing at.

## Missed fires

There is no portable way to hook OS sleep and wake from plain Python, so missed-fire detection is an approximation: the same poll-and-compare logic runs on every tick and at startup.

A fire discovered more than `LATE_THRESHOLD_SECONDS` (90) after it was due is recorded as late; anything inside that window is an ordinary on-time fire. A late run is spawned with `--late-scheduled-at`, which puts a note at the top of its output saying when it was scheduled and when it actually ran.

## Spawning a run

```python
px0_bin = shutil.which("px0") or sys.executable
args = [px0_bin] if px0_bin != sys.executable else [sys.executable, "-m", "px0.cli"]
args += ["workflows", "run", workflow_id, "--quiet"]
```

Detached, with stdout and stderr to `DEVNULL`. The run writes its own record and log; the daemon does not need its output and must not block on it.

`--trigger schedule` or `--trigger watch` is passed explicitly. Without it a spawned subprocess has no way of knowing what fired it, so it records itself as manual -- and everything that treats unattended runs differently was reading "manual" for every scheduled run there has ever been. That is four behaviours at once: inbox delivery, the circuit breaker, approval notices, and the budget.

Both `--trigger` and `--late-scheduled-at` are `argparse.SUPPRESS` in the parser. They are internal, so they stay out of `--help` and out of shell completion.

## Watches

A watch fires on something happening rather than on the clock.

This is what a local-first tool can offer instead of a webhook: Composio's own triggers need a public endpoint to deliver to, and there is not one on a laptop. So it is polling, with the interval floored at a minute.

`_tick_watches` polls each watched workflow's read tool, compares what came back against what it has seen, and spawns a run when there is something new.

### Item identity

A watch has to know what it has already acted on. `_watch_keys` unwraps the common envelope field names (`items`, `data`, `results`, `messages`, `events`) and then takes each item's identity from the configured `key`, or from the first of `id`, `url`, `html_url`, `number`, `message_id`, `key` that is present.

Position changes, and the whole payload changes when an unrelated field does. Falling back to a hash of the item still detects a genuinely new item, but the tool's own id field is the only honest answer.

### Priming

```python
if ws.get("primed"):
    ...
else:
    ws["primed"] = True
    _log_event(config, f"watch: primed {wf.id} with {len(keys)} existing items")
```

The first poll only learns the baseline. Otherwise adding a watch to an inbox with 2,000 messages fires immediately on all of it.

### Batching

`min_items` holds a watch back until enough new items have accumulated. Items below the threshold are deliberately kept out of `seen`, which is what lets them count again on the next poll -- held back rather than forgotten.

### Handing over what was new

```python
spawn_run(home, wf.id, late=False, fire_time=now,
          stdin_text="\n".join(fresh), trigger="watch")
```

The new item keys are piped to the run on stdin. A watch already fired once per poll however many items turned up, but the run it spawned was told nothing about them and had to go and look again -- which is both a second call and a different answer from the one that triggered it.

The write is fire-and-forget. The run is detached on purpose, and a daemon that blocked on one workflow's output would stop ticking every other schedule behind it.

`WATCH_SEEN_LIMIT` caps what is remembered at 500 keys. A busy watch would otherwise grow the state file forever.

## Approval replies

`_tick_approval_replies` polls the configured reply channel, but only when there is something waiting:

```python
if not approvals_mod.reply_config(config): return
if not approvals_mod.pending_count(home, config): return
```

A store with an empty queue should not poll a channel every minute to be told so. The whole function is wrapped so it cannot raise: a misconfigured reply channel must not stop the tick that fires everything else. See [part 12](12-trust.md).

## Nightly housekeeping

`run_nightly` is the only thing in px0 that ever tidies up, which is why every step is individually wrapped and reported rather than raised.

| Step | What it does |
| ---- | ------------ |
| Checkpoint | `claims.scan_and_process(force_hash=True)` -- catches what mtime tricks miss |
| Reindex | Rebuilds the retrieval index |
| Ingest queue | Drains queued playlist jobs |
| Retention | Deletes run logs, events, and records past their windows |
| Inbox | Drops read and archived entries past `inbox.keep_days` |
| Approvals | Purges resolved approvals past `approvals.keep_resolved_days` |
| Fixtures | Drops captured inputs past `runs.fixture_keep_days` |
| Sessions | Prunes conversations past `ask.session_days` |
| Update check | Once a week |

One unwritable folder must not stop the rest, because there is no second chance until tomorrow.

The update check advances `last_update_check` only on success, so a failure retries tomorrow rather than being skipped for a week. It logs rather than swallowing, because a permanently broken check is otherwise invisible.

## Installing

`detect_platform` picks launchd on macOS, systemd on Linux when a user session bus is present, and cron everywhere else.

```python
if sys.platform == "darwin":
    return "launchd"
if sys.platform.startswith("linux"):
    if Path("/run/systemd/system").exists() and shutil.which("systemctl"):
        return "systemd"
    return "cron"
return "cron"
```

systemd and launchd both get a unit written and a start hint printed. The cron path is different: it renders one crontab line per scheduled workflow and prints it for the user to install with `crontab -e`, because the user's crontab is theirs to own.

The cron path is also documented as having reduced semantics, and `install` says so in its return value:

```python
"reduced_semantics": "no missed-fire recovery, no log rotation, "
                     "no background ingest queue"
```

Those all live in the long-running loop, and cron has no long-running process to put them in.

`uninstall` is the inverse: it stops the running daemon, unloads and removes the launchd plist or the systemd unit, and reports -- rather than edits -- a crontab that still holds px0 entries. It leaves px0 and the store exactly as they are; only the firing stops.

Before `uninstall` existed, the only way to remove the scheduler was `install.sh --uninstall`, which also removes px0 itself.

## Status

`status(home, config)` reports whether the daemon is alive by signalling its pid with signal 0, plus the last recorded fire per workflow and each scheduled workflow's next upcoming fire.

`px0 status` folds that into a wider answer. See [part 17](17-cli.md).

## Next

[Part 12](12-trust.md) covers what a scheduled run is and is not allowed to do while nobody is watching.
