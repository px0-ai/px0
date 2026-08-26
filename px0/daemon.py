"""px0d: the scheduler. Deliberately dumb -- it watches workflows/,
evaluates cron schedules in machine local time, spawns `px0 workflows run <id>
--quiet`, recovers missed fires, and runs the nightly housekeeping pass.

Missed-fire detection here is a practical approximation: the same
poll-and-compare-to-last-fire logic runs on every tick and at startup. A
fire discovered more than LATE_THRESHOLD_SECONDS after it was due is
recorded as late (the machine was asleep or the daemon was down); a fire
discovered within that window is an ordinary on-time fire. There is no
separate OS sleep/wake hook, since none is available portably from plain
Python.
"""

import json
import os
import shutil
import signal
import subprocess
import sys
import time
from datetime import datetime, timedelta
from pathlib import Path

from croniter import croniter
from zoneinfo import ZoneInfo

from px0 import claims, config as config_mod, paths, retrieval, versioning
from px0 import runs as runs_mod
from px0 import workflow as workflow_mod

POLL_INTERVAL_SECONDS = 30

# How many item identities a watch remembers. Enough to span several polls of a
# busy source without letting the schedule state grow without bound.
WATCH_SEEN_LIMIT = 500
LATE_THRESHOLD_SECONDS = 90


def _log_event(config: dict, message: str) -> None:
    """Appends a timestamped message to daemon.log, swallowing OSError."""
    try:
        from datetime import timezone
        dt = datetime.now(timezone.utc).isoformat(timespec="seconds")
        log_dir = runs_mod.resolve_logs_path(config)
        log_dir.mkdir(parents=True, exist_ok=True)
        log_file = log_dir / "daemon.log"
        with open(log_file, "a", encoding="utf-8") as f:
            f.write(f"{dt} {message}\n")
    except OSError:
        pass


def pidfile_path(home: Path) -> Path:
    """Path to the file holding the running daemon's pid."""
    return paths.state_dir(home) / "daemon.pid"


def load_schedule_state(home: Path) -> dict:
    """Loads the last-fire-time-per-workflow-id map, or {} if none recorded yet."""
    p = paths.schedule_path(home)
    if not p.exists():
        return {}
    return json.loads(p.read_text())


def save_schedule_state(home: Path, state: dict) -> None:
    """Persists the last-fire-time-per-workflow-id map."""
    p = paths.schedule_path(home)
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(json.dumps(state, indent=2))


def resolve_zone(config: dict, wf) -> "ZoneInfo | None":
    """The clock a workflow's schedule is read against.

    Schedules were evaluated in whatever the machine's local time happened to
    be, which is right until the machine moves. A laptop taken two timezones
    over silently shifts every "9am" report by two hours, and the same thing
    happens twice a year without moving at all. A workflow can now pin its own
    zone, with `schedule.timezone` as the store-wide default; naming none keeps
    the old behaviour of following the machine.
    """
    name = (getattr(wf, "trigger", None) or {}).get("timezone") \
        or config_mod.get(config, "schedule.timezone", "")
    if not name:
        return None
    try:
        return ZoneInfo(str(name))
    except Exception:
        return None


def _in_zone(when: datetime, zone) -> datetime:
    """A timestamp read on a given clock, tolerating the naive ones already on
    disk: state written before zones existed is local time, which is what it
    was compared against then."""
    if zone is None:
        return when.replace(tzinfo=None) if when.tzinfo else when
    return (when.replace(tzinfo=zone) if when.tzinfo is None
            else when.astimezone(zone))


def _due_fires(schedule: str, last_fire: datetime | None, now: datetime) -> list[datetime]:
    """Returns every cron fire time for `schedule` between the later of last_fire
    or today's midnight, and now. With no last_fire, starts one second before
    midnight so a fire exactly at midnight is still included.

    `now` carries the clock: pass an aware datetime to read the schedule in
    that zone, and croniter keeps the offset through every fire it yields.
    """
    if last_fire is not None:
        # A stored fire from before zones existed is naive; comparing it with
        # an aware `now` raises rather than being wrong quietly, so it is read
        # on the same clock the caller is using.
        if (last_fire.tzinfo is None) != (now.tzinfo is None):
            last_fire = (last_fire.replace(tzinfo=now.tzinfo) if now.tzinfo
                         else last_fire.replace(tzinfo=None))
    midnight = now.replace(hour=0, minute=0, second=0, microsecond=0)
    start = max(last_fire, midnight) if last_fire else midnight - timedelta(seconds=1)
    itr = croniter(schedule, start)
    fires = []
    while True:
        nxt = itr.get_next(datetime)
        if nxt > now:
            break
        fires.append(nxt)
    return fires


def tick(home: Path, config: dict, state: dict) -> dict:
    """Check every scheduled workflow once; spawn `px0 workflows run` for anything
    due. Also polls every watched workflow, which fires on a new item rather
    than on the clock. Returns the updated schedule state."""
    now = datetime.now()
    for wf in workflow_mod.load_all(home).values():
        if not wf.enabled:
            continue  # parked by `px0 workflows disable`; keeps its file and history
        schedule = wf.trigger.get("schedule")
        if not schedule or wf.pipeline:
            continue
        zone = resolve_zone(config, wf)
        wf_now = datetime.now(zone) if zone else now
        last_fire_iso = state.get(wf.id)
        last_fire = datetime.fromisoformat(last_fire_iso) if last_fire_iso else None
        fires = _due_fires(schedule, last_fire, wf_now)
        for fire_time in fires:
            late = (wf_now - fire_time).total_seconds() > LATE_THRESHOLD_SECONDS
            spawn_run(home, wf.id, late, fire_time)
            _log_event(config, f"tick: spawned {wf.id} ({'late' if late else 'on-time'})")
            state[wf.id] = fire_time.isoformat()

    _tick_watches(home, config, state, now)
    _tick_approval_replies(home, config)
    save_schedule_state(home, state)
    return state


def _inbox_retention(home: Path, config: dict) -> int:
    from px0 import inbox as inbox_mod

    return inbox_mod.apply_retention(home, config)


def _approvals_retention(home: Path, config: dict) -> int:
    from px0 import approvals as approvals_mod

    return approvals_mod.purge(home, config)


def _fixture_retention(home: Path, config: dict) -> int:
    from px0 import replay as replay_mod

    return replay_mod.apply_retention(home, config)


def _session_retention(home: Path, config: dict) -> int:
    from px0 import session as session_mod

    return session_mod.prune(home, config)


def _tick_approval_replies(home: Path, config: dict) -> None:
    """Acts on approvals answered from somewhere other than the terminal.

    Only when there is something waiting: a store with an empty queue should
    not be polling a channel every minute to be told so. Never raises -- a
    misconfigured reply channel must not stop the tick that fires everything
    else.
    """
    from px0 import approvals as approvals_mod

    try:
        if not approvals_mod.reply_config(config):
            return
        if not approvals_mod.pending_count(home, config):
            return
        result = approvals_mod.scan_replies(home, config)
        for entry in result.get("acted") or []:
            _log_event(config, f"approvals: {entry['id']} {entry['status']} "
                               f"by reply from {entry['by']}")
        for entry in result.get("ignored") or []:
            _log_event(config, f"approvals: ignored a reply for {entry['approval_id']} "
                               f"from untrusted sender {entry.get('sender')!r}")
        if result.get("error"):
            _log_event(config, f"approvals: reply poll failed: {result['error']}")
    except Exception as e:
        _log_event(config, f"approvals: reply poll raised: {e}")


def _watch_state(state: dict, wf_id: str) -> dict:
    """The bookkeeping a watch keeps between polls: when it last looked, and what
    it had already seen."""
    watches = state.setdefault("_watches", {})
    return watches.setdefault(wf_id, {"last_poll": None, "seen": []})


def _watch_keys(items, key: str | None) -> list[str]:
    """The identity of each item a watch returned.

    A watch has to know what it has already acted on, and the tool's own id
    field is the only honest answer -- position changes, and the whole payload
    changes when an unrelated field does. Falls back to a hash of the item when
    no key is named, which still detects a genuinely new item.
    """
    import hashlib

    if isinstance(items, dict):
        for field in ("items", "data", "results", "messages", "events"):
            if isinstance(items.get(field), list):
                items = items[field]
                break
        else:
            items = [items]
    if not isinstance(items, list):
        items = [items]
    keys = []
    for item in items:
        value = None
        if isinstance(item, dict):
            if key:
                value = item.get(key)
            else:
                for field in ("id", "url", "html_url", "number", "message_id", "key"):
                    if item.get(field) is not None:
                        value = item[field]
                        break
        if value is None:
            value = hashlib.sha256(
                json.dumps(item, sort_keys=True, default=str).encode()).hexdigest()[:16]
        keys.append(str(value))
    return keys


def _tick_watches(home: Path, config: dict, state: dict, now: datetime) -> None:
    """Polls each watched workflow's read tool and fires on anything new.

    This is what a local-first tool can offer instead of a webhook: Composio's
    own triggers need a public endpoint to deliver to, and there isn't one on a
    laptop. The poll interval is the workflow's, floored at a minute.
    """
    from px0 import tools as tools_mod

    for wf in workflow_mod.load_all(home).values():
        if not wf.enabled or wf.pipeline:
            continue
        spec = workflow_mod.watch_spec(wf)
        if not spec:
            continue
        ws = _watch_state(state, wf.id)
        last = ws.get("last_poll")
        if last:
            try:
                if (now - datetime.fromisoformat(last)).total_seconds() < spec["every_seconds"]:
                    continue
            except ValueError:
                pass
        ws["last_poll"] = now.isoformat()
        try:
            result = tools_mod.call(home, config, spec["tool"], spec["args"])
        except Exception as e:
            _log_event(config, f"watch: {wf.id} poll failed: {e}")
            continue
        keys = _watch_keys(result, spec.get("key"))
        seen = set(ws.get("seen") or [])
        fresh = [k for k in keys if k not in seen]
        # First poll only learns the baseline. Otherwise adding a watch to an
        # inbox with 2,000 messages fires immediately on all of it.
        minimum = max(1, int(spec.get("min_items") or 1))
        if ws.get("primed"):
            if len(fresh) >= minimum:
                # What was new is handed to the run on stdin. A watch already
                # fired once per poll however many items turned up, but the run
                # it spawned was told nothing about them and had to go and look
                # again -- which is both a second call and a different answer
                # from the one that triggered it.
                spawn_run(home, wf.id, late=False, fire_time=now,
                          stdin_text="\n".join(fresh), trigger="watch")
                _log_event(config, f"watch: spawned {wf.id} ({len(fresh)} new)")
            elif fresh:
                _log_event(config, f"watch: {wf.id} holding {len(fresh)} of "
                                   f"{minimum} new item(s)")
                # Held back rather than forgotten: leaving these out of `seen`
                # is what lets them count again on the next poll.
                keys = [k for k in keys if k not in set(fresh)]
        else:
            ws["primed"] = True
            _log_event(config, f"watch: primed {wf.id} with {len(keys)} existing items")
        # Cap what is remembered: a busy watch would grow the state file forever.
        ws["seen"] = (keys + [k for k in seen if k not in set(keys)])[:WATCH_SEEN_LIMIT]


def spawn_run(home: Path, workflow_id: str, late: bool, fire_time: datetime,
              stdin_text: str | None = None, trigger: str = "schedule") -> None:
    """Launches `px0 workflows run <workflow_id> --quiet` as a detached subprocess,
    passing --late-scheduled-at when the fire was recovered rather than on-time.

    `stdin_text` is piped to the run, which is how a watch tells the workflow
    it triggered what was actually new.
    """
    px0_bin = shutil.which("px0") or sys.executable
    args = [px0_bin] if px0_bin != sys.executable else [sys.executable, "-m", "px0.cli"]
    args += ["workflows", "run", workflow_id, "--quiet"]
    if late:
        args += ["--late-scheduled-at", fire_time.strftime("%H:%M")]
    else:
        # Without this the run records itself as manual, because a spawned
        # subprocess has no other way of knowing what fired it.
        args += ["--trigger", trigger]
    if stdin_text is not None:
        args += ["--stdin"]
    env = {**os.environ, "PX0_HOME": str(home)}
    proc = subprocess.Popen(
        args, env=env,
        stdin=subprocess.PIPE if stdin_text is not None else subprocess.DEVNULL,
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if stdin_text is not None and proc.stdin is not None:
        # Written and closed without waiting: the run is detached on purpose,
        # and a daemon that blocked on one workflow's output would stop
        # ticking every other schedule behind it.
        try:
            proc.stdin.write(stdin_text.encode())
        except (BrokenPipeError, OSError):
            pass
        finally:
            try:
                proc.stdin.close()
            except OSError:
                pass


def recover_missed_fires(home: Path, config: dict) -> None:
    """On start or wake: catch up fires from today only."""
    state = load_schedule_state(home)
    tick(home, config, state)


def run_nightly(home: Path, config: dict) -> dict:
    """Runs the once-a-day housekeeping pass: hand-edit checkpoint scan, brain
    reindex, draining queued playlist ingest jobs, run-log retention, and a
    once-a-week update-availability check. Every fallible step is captured in the
    report rather than raised, so one broken index or unreachable playlist doesn't
    block the rest of housekeeping."""
    _log_event(config, "nightly: started housekeeping")
    report = {}
    report["checkpoint"] = claims.scan_and_process(home, force_hash=True)
    try:
        report["reindexed"] = retrieval.reindex(home, config)
    except Exception as e:
        report["reindex_error"] = str(e)
    try:
        from px0 import brain as brain_mod
        report["ingest_queue"] = brain_mod.process_ingest_queue(home, config)
    except Exception as e:
        report["ingest_error"] = str(e)
    try:
        report["retention"] = runs_mod.apply_retention(config)
    except Exception as e:
        report["retention_error"] = str(e)[:200]
    # Everything else px0 accumulates and would otherwise keep forever. Each is
    # wrapped, because one unwritable folder should not stop the rest of
    # housekeeping -- the nightly pass is the only thing that ever tidies up.
    for name, sweep in (
        ("inbox_pruned", lambda: _inbox_retention(home, config)),
        ("approvals_purged", lambda: _approvals_retention(home, config)),
        ("fixtures_pruned", lambda: _fixture_retention(home, config)),
        ("sessions_pruned", lambda: _session_retention(home, config)),
    ):
        try:
            report[name] = sweep()
        except Exception as e:
            report[f"{name}_error"] = str(e)[:200]

    # Weekly update check
    try:
        from px0 import update as update_mod
        state = load_schedule_state(home)
        last_check_str = state.get("last_update_check")
        last_check = datetime.fromisoformat(last_check_str) if last_check_str else None

        if not last_check or (datetime.now() - last_check).days >= 7:
            check_res = update_mod.check(config)
            state["last_update_check"] = datetime.now().isoformat()
            save_schedule_state(home, state)

            up_check_path = paths.update_check_path(home)
            up_check_path.parent.mkdir(parents=True, exist_ok=True)
            up_check_path.write_text(json.dumps({
                "checked_at": datetime.now().isoformat(),
                "available_version": check_res.get("available_version")
            }, indent=2))
    except Exception as e:
        # An update check is never worth failing housekeeping over, but a silent
        # swallow means a permanently broken check is invisible -- log it. Note
        # last_update_check is only advanced on success, so this retries tomorrow.
        _log_event(config, f"nightly: update check skipped ({e})")

    cp_val = 0
    if report.get("checkpoint"):
        try:
            ch = versioning.show_change(home, report["checkpoint"])
            cp_val = len(ch.get("files", []))
        except (OSError, ValueError, KeyError):
            # The change exists (scan_and_process returned its id) but couldn't be
            # read back; report it as one rather than losing the fact entirely.
            cp_val = 1

    reindexed_val = report.get("reindexed", 0)
    ret_removed = report.get("retention", {}).get("logs", 0)
    _log_event(config, f"nightly: checkpoint={cp_val} changed, reindexed={reindexed_val} passages, retention removed {ret_removed} logs")
    return report


def serve(home: Path, config: dict, poll_interval: float = POLL_INTERVAL_SECONDS) -> None:
    """Runs the daemon's main loop until SIGTERM/SIGINT: writes a pidfile, recovers
    missed fires from earlier today, then polls every poll_interval seconds,
    ticking the schedule and running the nightly pass once per calendar day."""
    pidfile = pidfile_path(home)
    pidfile.parent.mkdir(parents=True, exist_ok=True)
    pidfile.write_text(str(os.getpid()))

    _log_event(config, "start: serve started")

    def handle_stop(signum, frame):
        # removes the pidfile before exiting so status() doesn't report a stale pid
        if pidfile.exists():
            pidfile.unlink()
        _log_event(config, "stop: SIGTERM received")
        os._exit(0)

    def reap_children(signum, frame):
        # reaps spawned `px0 workflows run` children so they don't linger as zombies
        try:
            while os.waitpid(-1, os.WNOHANG)[0] > 0:
                pass
        except ChildProcessError:
            pass

    signal.signal(signal.SIGTERM, handle_stop)
    signal.signal(signal.SIGINT, handle_stop)
    signal.signal(signal.SIGCHLD, reap_children)

    try:
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
    finally:
        if pidfile.exists():
            pidfile.unlink()


def status(home: Path, config: dict) -> dict:
    """Reports whether the daemon is alive (by signaling its pid with signal 0),
    plus the last recorded fire per workflow and each scheduled workflow's next
    upcoming fire time."""
    pidfile = pidfile_path(home)
    alive = False
    pid = None
    if pidfile.exists():
        pid = int(pidfile.read_text().strip())
        try:
            os.kill(pid, 0)
            alive = True
        except (ProcessLookupError, PermissionError):
            alive = False

    state = load_schedule_state(home)
    next_fires = {}
    for wf in workflow_mod.load_all(home).values():
        schedule = wf.trigger.get("schedule")
        if not schedule:
            continue
        # Read on the same clock the tick will use, or `px0 status` promises a
        # time the daemon has no intention of firing at.
        zone = resolve_zone(config, wf)
        itr = croniter(schedule, datetime.now(zone) if zone else datetime.now())
        next_fires[wf.id] = itr.get_next(datetime).isoformat()

    return {"alive": alive, "pid": pid, "last_fires": state, "next_fires": next_fires}


# --- install: platform detection and unit generation --------------------

def detect_platform() -> str:
    """Picks the scheduling mechanism for this OS: launchd on macOS, systemd on
    Linux when a user session bus is available, cron as the fallback everywhere else."""
    if sys.platform == "darwin":
        return "launchd"
    if sys.platform.startswith("linux"):
        if Path("/run/systemd/system").exists() and shutil.which("systemctl"):
            return "systemd"
        return "cron"
    return "cron"


def systemd_unit(home: Path, px0_bin: str) -> str:
    """Renders a systemd user-service unit file that runs `px0 daemon serve`."""
    return f"""[Unit]
Description=px0 scheduler

[Service]
ExecStart={px0_bin} daemon serve
Environment=PX0_HOME={home}
Restart=on-failure

[Install]
WantedBy=default.target
"""


def launchd_plist(home: Path, px0_bin: str) -> str:
    """Renders a launchd plist that runs `px0 daemon serve` at load and keeps it alive."""
    return f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>sh.px0.daemon</string>
  <key>ProgramArguments</key>
  <array><string>{px0_bin}</string><string>daemon</string><string>serve</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PX0_HOME</key><string>{home}</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
"""


def crontab_block(home: Path, px0_bin: str) -> str:
    """Renders one crontab line per scheduled, non-pipeline workflow, for the
    cron fallback path which has no long-running daemon process."""
    lines = ["# BEGIN px0-managed"]
    for wf in workflow_mod.load_all(home).values():
        schedule = wf.trigger.get("schedule")
        if schedule and not wf.pipeline and wf.enabled:
            lines.append(
                f"{schedule} PX0_HOME={home} {px0_bin} workflows run {wf.id} --quiet")
    lines.append("# END px0-managed")
    return "\n".join(lines) + "\n"


def install(home: Path, fallback_cron: bool = False) -> dict:
    """Writes the platform-appropriate scheduler unit (systemd/launchd) or, on cron
    fallback, only renders the crontab block without writing anything (the caller
    installs it with `crontab -e`). Returns platform, path written (if any), the
    rendered content, and a human hint for how to start it."""
    px0_bin = shutil.which("px0") or f"{sys.executable} -m px0.cli"
    platform = "cron" if fallback_cron else detect_platform()

    if platform == "systemd":
        unit_dir = Path("~/.config/systemd/user").expanduser()
        unit_dir.mkdir(parents=True, exist_ok=True)
        unit_path = unit_dir / "px0d.service"
        unit_path.write_text(systemd_unit(home, px0_bin))
        return {"platform": "systemd", "path": str(unit_path),
                "content": unit_path.read_text(),
                "start_hint": "systemctl --user enable --now px0d.service"}

    if platform == "launchd":
        plist_dir = Path("~/Library/LaunchAgents").expanduser()
        plist_dir.mkdir(parents=True, exist_ok=True)
        plist_path = plist_dir / "sh.px0.daemon.plist"
        plist_path.write_text(launchd_plist(home, px0_bin))
        return {"platform": "launchd", "path": str(plist_path),
                "content": plist_path.read_text(),
                "start_hint": f"launchctl load {plist_path}"}

    block = crontab_block(home, px0_bin)
    return {"platform": "cron", "path": None, "content": block,
            "start_hint": "add the printed block with `crontab -e`",
            "reduced_semantics": "no missed-fire recovery, no log rotation, "
                                  "no background ingest queue"}


def uninstall(home: Path) -> dict:
    """Removes whatever `install` put in place, and stops the daemon if it is running.

    `install` had no inverse: the only way to remove the scheduler unit was
    `install.sh --uninstall`, which also removes px0 itself. This leaves px0 and
    the store exactly as they are, and only stops work from firing on its own.

    Reports the crontab case rather than editing the user's crontab, which is
    theirs to own.
    """
    removed = []
    stopped = False

    pidfile = pidfile_path(home)
    if pidfile.exists():
        try:
            pid = int(pidfile.read_text().strip())
            os.kill(pid, signal.SIGTERM)
            stopped = True
            for _ in range(20):
                time.sleep(0.1)
                try:
                    os.kill(pid, 0)
                except (ProcessLookupError, PermissionError):
                    break
        except (ValueError, ProcessLookupError, PermissionError, OSError):
            pass
        pidfile.unlink(missing_ok=True)

    plist_path = Path("~/Library/LaunchAgents/sh.px0.daemon.plist").expanduser()
    if plist_path.exists():
        subprocess.run(["launchctl", "unload", str(plist_path)],
                       capture_output=True, check=False)
        plist_path.unlink()
        removed.append(str(plist_path))

    unit_path = Path("~/.config/systemd/user/px0d.service").expanduser()
    if unit_path.exists():
        subprocess.run(["systemctl", "--user", "disable", "--now", "px0d.service"],
                       capture_output=True, check=False)
        unit_path.unlink()
        subprocess.run(["systemctl", "--user", "daemon-reload"],
                       capture_output=True, check=False)
        removed.append(str(unit_path))

    cron_note = None
    try:
        listing = subprocess.run(["crontab", "-l"], capture_output=True, text=True, check=False)
        if "px0-managed" in (listing.stdout or "") or "px0 workflows run" in (listing.stdout or ""):
            cron_note = ("px0 entries are still in your crontab; remove the "
                         "px0-managed block with `crontab -e`")
    except (OSError, FileNotFoundError):
        pass

    return {"removed": removed, "stopped": stopped, "cron_note": cron_note}


def restart_if_running(home: Path, config: dict) -> None:
    """Checks daemon status, and if it is running/alive, sends SIGTERM and respawns it."""
    s = status(home, config)
    if s.get("pid") and s.get("alive"):
        try:
            os.kill(s["pid"], signal.SIGTERM)
            for _ in range(20):
                time.sleep(0.1)
                if not status(home, config)["alive"]:
                    break
        except Exception:
            pass
        subprocess.Popen(
            [sys.executable, "-m", "px0.cli", "daemon", "serve"],
            env={**os.environ, "PX0_HOME": str(home)},
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
