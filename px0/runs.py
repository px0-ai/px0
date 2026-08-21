"""Run records and raw logs. Two artifacts per run, both under the
configurable log directory, both subject to retention -- never inside the
store, so raw prompts and connector responses stay out of any folder the
user might copy or sync."""

import json
import os
import re
import secrets
import signal
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path

from px0 import config as config_mod
from px0 import paths


_SINCE_RE = re.compile(r"-?(\d+)([dwh])")


def parse_since(text: str) -> datetime:
    """Parses an age like "7d", "-7d", "2w", or "12h" into an absolute datetime.

    The leading minus is optional because it reads naturally as "7 days back"
    and the TUI's own prompt suggests it; rejecting it was a bug. Lives here
    rather than in the CLI so `runs_tui` can use it without importing the CLI,
    which imports `runs_tui`.
    """
    match = _SINCE_RE.fullmatch(text.strip())
    if not match:
        raise ValueError(f"unsupported since format: {text!r} (use e.g. 7d, 2w, 12h)")
    amount, unit = int(match.group(1)), match.group(2)
    delta = {"h": timedelta(hours=amount),
             "d": timedelta(days=amount),
             "w": timedelta(weeks=amount)}[unit]
    return datetime.now() - delta


def resolve_logs_path(config: dict) -> Path:
    """Resolves the directory used for run logs and records, creating it if
    needed. Falls back to `~/.local/state/px0/logs` if the configured path
    (default `/var/log/px0`) isn't writable. Side effect: writes and
    removes a probe file to test writability."""
    configured = config_mod.get(config, "logs.path", "/var/log/px0")
    path = Path(configured).expanduser()
    try:
        path.mkdir(parents=True, exist_ok=True)
        probe = path / ".px0-write-test"
        probe.write_text("")
        probe.unlink()
        return path
    except OSError:
        fallback = Path("~/.local/state/px0/logs").expanduser()
        fallback.mkdir(parents=True, exist_ok=True)
        return fallback


def new_run_id(prefix: str = "run") -> str:
    """Generates a unique run id from a UTC timestamp plus a short random
    hex suffix."""
    ts = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    return f"{prefix}_{ts}-{secrets.token_hex(2)}"


class RunIdError(ValueError):
    """A run id is not of the form `<prefix>_YYYYMMDD-HHMMSS-xxxx`."""
    pass


def _date_of(run_id: str) -> str:
    """Extracts the run's date (YYYY-MM-DD) from its run id, used to
    partition records and logs into per-day directories.

    Raises RunIdError on anything that isn't a run id, so a mistyped argument
    reports itself instead of surfacing an IndexError from the split.
    """
    # run_YYYYMMDD-HHMMSS-xxxx -> YYYY-MM-DD
    _, _, rest = run_id.partition("_")
    ts = rest.split("-")[0]
    if not ts.isdigit() or len(ts) != 8:
        raise RunIdError(
            f"{run_id!r} is not a run id -- expected something like "
            "run_20260817-093000-ab12 (see `px0 runs list`)"
        )
    return f"{ts[0:4]}-{ts[4:6]}-{ts[6:8]}"


def record_path(config: dict, run_id: str) -> Path:
    """Returns the path to a run's JSON record file, partitioned by date
    under `records/`."""
    return resolve_logs_path(config) / "records" / _date_of(run_id) / f"{run_id}.json"


def log_path(config: dict, run_id: str) -> Path:
    """Returns the path to a run's raw log file, partitioned by date under
    `runs/`."""
    return resolve_logs_path(config) / "runs" / _date_of(run_id) / f"{run_id}.log"


def write_record(config: dict, record: dict) -> None:
    """Writes a run record as JSON to disk, creating parent directories as
    needed. Overwrites any existing record for the same run id.

    Stamps the owning store, because `logs.path` defaults to one directory
    shared by every store on the machine: without this, a second store's
    `px0 runs list` showed the first store's runs, and offered to rerun
    workflows it does not have.
    """
    record.setdefault("store", str(paths.store_home()))
    path = record_path(config, record["id"])
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(record, indent=2, default=str))


def append_raw_log(config: dict, run_id: str, text: str) -> None:
    """Appends text to a run's raw log file, creating parent directories
    (and the file) as needed. Ensures the appended text ends with a
    newline."""
    path = log_path(config, run_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "a") as f:
        f.write(text)
        if not text.endswith("\n"):
            f.write("\n")


def read_record(config: dict, run_id: str) -> dict:
    """Reads and parses a run's JSON record. Raises FileNotFoundError if
    the record is missing, e.g. because it aged out under retention."""
    path = record_path(config, run_id)
    if not path.exists():
        raise FileNotFoundError(f"no run record for {run_id} (may have aged out)")
    return json.loads(path.read_text())


def read_raw_log(config: dict, run_id: str) -> str:
    """Reads a run's raw log file. Returns an empty string if the log
    doesn't exist rather than raising."""
    path = log_path(config, run_id)
    if not path.exists():
        return ""
    return path.read_text()


def list_records(
    config: dict,
    workflow: str | None = None,
    failed: bool = False,
    since: datetime | None = None,
) -> list[dict]:
    """Lists run records matching the given filters (workflow id,
    failed-only, since a given time), newest first. Returns an empty list
    if the records directory doesn't exist. Record files that fail to
    parse as JSON are silently skipped.

    Only the current store's runs are listed. Records written before runs
    carried a store stamp have no owner recorded, so they are included rather
    than hidden -- on the single-store setup that is every one of them.
    """
    base = resolve_logs_path(config) / "records"
    if not base.exists():
        return []
    this_store = str(paths.store_home())
    records: list[dict] = []
    for date_dir in sorted(base.iterdir(), reverse=True):
        if not date_dir.is_dir():
            continue
        for f in sorted(date_dir.glob("*.json"), reverse=True):
            try:
                rec = json.loads(f.read_text())
            except (json.JSONDecodeError, OSError):
                continue
            owner = rec.get("store")
            if owner is not None and owner != this_store:
                continue
            if workflow and rec.get("workflow_id") != workflow:
                continue
            if failed and rec.get("outcome") != "failed":
                continue
            if since and rec.get("start_time", "") < since.isoformat():
                continue
            records.append(rec)
    return records


def running_dir(home: Path) -> Path:
    """Where a run in flight records its pid, so another process can cancel it."""
    return paths.state_dir(home) / "running"


def mark_running(home: Path, run_id: str, workflow_id: str, pid: int | None = None) -> None:
    """Records that a run is in flight. Best-effort: a store that cannot write
    this still runs the workflow, it only loses the ability to cancel it."""
    path = running_dir(home) / f"{run_id}.json"
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps({
            "id": run_id, "workflow_id": workflow_id,
            "pid": pid if pid is not None else os.getpid(),
            "started_at": datetime.now(timezone.utc).isoformat(),
        }))
    except OSError:
        pass


def clear_running(home: Path, run_id: str) -> None:
    """Removes a run's in-flight marker. Safe to call when there is none."""
    try:
        (running_dir(home) / f"{run_id}.json").unlink()
    except OSError:
        pass


def list_running(home: Path) -> list[dict]:
    """Every run currently in flight, newest first, with dead markers dropped.

    A crashed run leaves its marker behind, so each one is checked against the
    process table before being reported: a stale entry would otherwise look
    like a run that has been going for days.
    """
    base = running_dir(home)
    if not base.exists():
        return []
    out = []
    for path in sorted(base.glob("*.json")):
        try:
            rec = json.loads(path.read_text())
        except (OSError, ValueError):
            path.unlink(missing_ok=True)
            continue
        pid = rec.get("pid")
        alive = False
        if isinstance(pid, int):
            try:
                os.kill(pid, 0)
                alive = True
            except (ProcessLookupError, PermissionError):
                alive = False
            except OSError:
                alive = False
        if not alive:
            path.unlink(missing_ok=True)
            continue
        out.append(rec)
    return sorted(out, key=lambda r: r.get("started_at", ""), reverse=True)


def cancel(home: Path, run_id: str, force: bool = False) -> dict:
    """Signals a run in flight to stop. Returns what happened.

    SIGTERM by default so the run's own handlers can finalize the record;
    `force` sends SIGKILL, which leaves the record as it was last written.
    """
    for rec in list_running(home):
        if rec.get("id") == run_id:
            pid = rec.get("pid")
            try:
                os.kill(int(pid), signal.SIGKILL if force else signal.SIGTERM)
            except (ProcessLookupError, PermissionError, TypeError, ValueError) as e:
                clear_running(home, run_id)
                return {"cancelled": False, "detail": str(e)}
            return {"cancelled": True, "pid": pid, "signal": "SIGKILL" if force else "SIGTERM"}
    return {"cancelled": False, "detail": "not running"}


def apply_retention(config: dict) -> dict:
    """Delete artifacts past retention, per config, except runs that
    called a write tool -- those are exempt."""
    now = datetime.now(timezone.utc)
    retention_days = config_mod.get(config, "logs.retention_days", 14)
    retention_days_failed = config_mod.get(config, "logs.retention_days_failed", 60)
    record_retention_days = config_mod.get(config, "logs.record_retention_days", 365)

    removed = {"logs": 0, "records": 0}

    for rec in list_records(config):
        wrote = any(c.get("is_write") for c in rec.get("tool_calls", []))
        if wrote:  # runs that mutated something are kept regardless of age
            continue
        start = datetime.fromisoformat(rec["start_time"])
        age_days = (now - start).days
        # failed runs get a longer retention window than successful ones
        log_limit = retention_days_failed if rec.get("outcome") == "failed" else retention_days
        if age_days > log_limit:
            lp = log_path(config, rec["id"])
            if lp.exists():
                lp.unlink()
                removed["logs"] += 1
        if age_days > record_retention_days:
            rp = record_path(config, rec["id"])
            if rp.exists():
                rp.unlink()
                removed["records"] += 1

    return removed


def tail_lines(path: Path, poll_interval: float = 1.0):
    """Yields lines appended to `path` after this call starts, polling
    every poll_interval seconds. Never returns on its own -- the caller
    breaks out (e.g. on a terminal run outcome, or KeyboardInterrupt)."""
    with open(path, "r", encoding="utf-8") as f:
        f.seek(0, 2)  # start at current end-of-file
        while True:
            f.seek(f.tell())  # Clear EOF flag
            line = f.readline()
            if line:
                yield line
            else:
                time.sleep(poll_interval)
