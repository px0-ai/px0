"""px0d: the scheduler. Deliberately dumb -- it watches workflows/,
evaluates cron schedules in machine local time, spawns `px0 run <id>
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

from px0 import claims, config as config_mod, paths, retrieval
from px0 import runner, runs as runs_mod
from px0 import workflow as workflow_mod

POLL_INTERVAL_SECONDS = 30
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


def _due_fires(schedule: str, last_fire: datetime | None, now: datetime) -> list[datetime]:
    """Returns every cron fire time for `schedule` between the later of last_fire
    or today's midnight, and now. With no last_fire, starts one second before
    midnight so a fire exactly at midnight is still included."""
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
    """Check every scheduled workflow once; spawn `px0 run` for anything
    due. Returns the updated schedule state."""
    now = datetime.now()
    for wf in workflow_mod.load_all(home).values():
        schedule = wf.trigger.get("schedule")
        if not schedule or wf.pipeline:
            continue
        last_fire_iso = state.get(wf.id)
        last_fire = datetime.fromisoformat(last_fire_iso) if last_fire_iso else None
        fires = _due_fires(schedule, last_fire, now)
        for fire_time in fires:
            late = (now - fire_time).total_seconds() > LATE_THRESHOLD_SECONDS
            spawn_run(home, wf.id, late, fire_time)
            _log_event(config, f"tick: spawned {wf.id} ({'late' if late else 'on-time'})")
            state[wf.id] = fire_time.isoformat()
    save_schedule_state(home, state)
    return state


def spawn_run(home: Path, workflow_id: str, late: bool, fire_time: datetime) -> None:
    """Launches `px0 run <workflow_id> --quiet` as a detached subprocess, passing
    --late-scheduled-at when the fire was recovered rather than on-time."""
    px0_bin = shutil.which("px0") or sys.executable
    args = [px0_bin] if px0_bin != sys.executable else [sys.executable, "-m", "px0.cli"]
    args += ["run", workflow_id, "--quiet"]
    if late:
        args += ["--late-scheduled-at", fire_time.strftime("%H:%M")]
    env = {**os.environ, "PX0_HOME": str(home)}
    subprocess.Popen(args, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def recover_missed_fires(home: Path, config: dict) -> None:
    """On start or wake: catch up fires from today only."""
    state = load_schedule_state(home)
    tick(home, config, state)


def run_nightly(home: Path, config: dict) -> dict:
    """Runs the once-a-day housekeeping pass: hand-edit checkpoint scan, knowledge
    reindex, and run-log retention. Reindex failures are captured in the report
    rather than raised, so one broken index doesn't block the rest of housekeeping."""
    _log_event(config, "nightly: started housekeeping")
    report = {}
    report["checkpoint"] = claims.scan_and_process(home, force_hash=True)
    try:
        report["reindexed"] = retrieval.reindex(home, config)
    except Exception as e:
        report["reindex_error"] = str(e)
    try:
        from px0 import knowledge as knowledge_mod
        report["ingest_queue"] = knowledge_mod.process_ingest_queue(home, config)
    except Exception as e:
        report["ingest_error"] = str(e)
    report["retention"] = runs_mod.apply_retention(config)

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
    except Exception:
        pass

    cp_val = 0
    if report.get("checkpoint"):
        try:
            ch = versioning.show_change(home, report["checkpoint"])
            cp_val = len(ch.get("files", []))
        except Exception:
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
        # reaps spawned `px0 run` children so they don't linger as zombies
        try:
            while os.waitpid(-1, os.WNOHANG)[0] > 0:
                pass
        except ChildProcessError:
            pass

    signal.signal(signal.SIGTERM, handle_stop)
    signal.signal(signal.SIGINT, handle_stop)
    signal.signal(signal.SIGCHLD, reap_children)

    try:
        state = load_schedule_state(home)
        tick(home, config, state)  # recover missed fires on start/wake
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
        itr = croniter(schedule, datetime.now())
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
        if schedule and not wf.pipeline:
            lines.append(f"{schedule} PX0_HOME={home} {px0_bin} run {wf.id} --quiet")
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
