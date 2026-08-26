"""Run records, raw logs, and the event stream. Three artifacts per run, all
under the configurable log directory, all subject to retention -- never inside
the store, so raw prompts and connector responses stay out of any folder the
user might copy or sync.

The three are deliberately different things. The **record** is the run's
summary, kept for a year, and is what every listing and every analysis reads.
The **raw log** is the full prompt and reply text, kept for a fortnight,
and is for a person reading one run. The **event stream** is one JSON object
per thing that happened, kept alongside the log, and is what makes a run
machine-readable after the fact: which turn called which tool, what it cost,
what the harness said on stderr. Nothing derives a verdict from the raw log,
because the raw log is usually gone.
"""

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


def events_path(config: dict, run_id: str) -> Path:
    """Returns the path to a run's event stream, partitioned by date under
    `events/`. One JSON object per line, appended as the run proceeds."""
    return resolve_logs_path(config) / "events" / _date_of(run_id) / f"{run_id}.jsonl"


def append_event(config: dict, run_id: str, kind: str, **fields) -> None:
    """Appends one structured event to a run's event stream.

    Best-effort by design, and silent on failure. Every call site is inside the
    run loop, so an unwritable log directory or a value that will not serialize
    has to cost the run nothing -- telemetry that can fail a run is worse than
    no telemetry. Turned off wholesale with `logs.events = false`.

    Kinds are the vocabulary `px0 runs events` and `px0 workflows health` read,
    so they are stable strings rather than free text: run_started, inputs,
    prompt, model_call, tool_call, tool_refused, output, run_finished.
    """
    if not config_mod.get(config, "logs.events", True):
        return
    event = {"ts": datetime.now(timezone.utc).isoformat(), "run": run_id, "kind": kind}
    event.update(fields)
    try:
        path = events_path(config, run_id)
        path.parent.mkdir(parents=True, exist_ok=True)
        with open(path, "a") as f:
            f.write(json.dumps(event, default=str) + "\n")
    except (OSError, ValueError, TypeError, RunIdError):
        pass


def read_events(config: dict, run_id: str) -> list[dict]:
    """A run's event stream, oldest first. Returns an empty list when the run
    predates event logging or its stream has aged out. Unparseable lines are
    skipped rather than raising: a stream truncated by a crash mid-write is
    still worth reading up to the truncation."""
    path = events_path(config, run_id)
    if not path.exists():
        return []
    events = []
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return events


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


VERDICTS = ("good", "bad")


def mark(config: dict, run_id: str, verdict: str | None, note: str = "") -> dict:
    """Records what the person thought of what a run produced, on the run's own
    record. Returns the updated record.

    This is the one signal nothing else in px0 can infer. A record says whether
    a run *executed* cleanly -- not whether the digest it wrote was any good. A
    workflow that succeeds every Friday and produces something useless looks
    perfect in every other field there is, so `px0 workflows improve` would be
    guessing from execution telemetry alone without this.

    `verdict` of None clears a previous mark, so a mark made in haste is
    undoable. The note is what makes the mark worth having: "bad" says a run
    was wrong, "missed the two PRs I actually reviewed" says how.
    """
    if verdict is not None and verdict not in VERDICTS:
        raise ValueError(f"verdict must be one of {', '.join(VERDICTS)}, or cleared")
    record = read_record(config, run_id)
    if verdict is None:
        record.pop("review", None)
    else:
        record["review"] = {
            "verdict": verdict,
            "note": note.strip(),
            "at": datetime.now(timezone.utc).isoformat(),
        }
    write_record(config, record)
    return record


def as_utc(value) -> datetime | None:
    """Any recorded timestamp as an aware UTC datetime, or None if unreadable.

    Records stamp `start_time` in UTC, and `parse_since` hands back a naive
    local datetime -- so the two were being compared as *strings*, which is
    wrong twice over. The offset suffix makes an in-window record sort before a
    naive cutoff character by character, and the naive value is local wall
    clock while the record is UTC, so the window was displaced by whatever this
    machine's offset happens to be. In IST that quietly dropped five and a half
    hours of runs from every `--since` query, including the one the daily
    budget is computed from.

    A naive value is read as local time, which is what `datetime.now()` means.
    """
    try:
        stamp = datetime.fromisoformat(str(value))
    except (TypeError, ValueError):
        return None
    if stamp.tzinfo is None:
        stamp = stamp.astimezone()  # naive means this machine's wall clock
    return stamp.astimezone(timezone.utc)


def _date_dir_is_before(name: str, cutoff: datetime) -> bool:
    """Whether a whole `YYYY-MM-DD` partition falls before a cutoff.

    Compared against the *end* of that day in UTC, and only skipped when the
    entire day is behind the window -- a directory is named for a local-ish
    calendar day while the cutoff is an instant, so anything less careful would
    drop the boundary day and the runs the caller actually asked for. A name
    that is not a date is never skipped.
    """
    try:
        day = datetime.strptime(name, "%Y-%m-%d").replace(tzinfo=timezone.utc)
    except ValueError:
        return False
    return day + timedelta(days=2) < cutoff


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
    cutoff = as_utc(since) if since is not None else None
    this_store = str(paths.store_home())
    records: list[dict] = []
    for date_dir in sorted(base.iterdir(), reverse=True):
        if not date_dir.is_dir():
            continue
        if cutoff is not None and _date_dir_is_before(date_dir.name, cutoff):
            # Records are partitioned by date, so a whole day older than the
            # window can be skipped without opening any of it. Without this,
            # every `--since` query read a year of JSON to answer a question
            # about the last hour -- and `px0 status` did it twice per run.
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
            if cutoff is not None:
                started = as_utc(rec.get("start_time"))
                # A record whose time cannot be read cannot be shown to fall
                # inside a window, and a window is a claim about time.
                if started is None or started < cutoff:
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

    removed = {"logs": 0, "events": 0, "records": 0}

    for rec in list_records(config):
        wrote = any(c.get("is_write") for c in rec.get("tool_calls", []))
        if wrote:  # runs that mutated something are kept regardless of age
            continue
        # One unreadable record used to take the whole pass down, and this pass
        # is the only thing that ever tidies up -- so a single stray file meant
        # logs, events, and records all grew forever, silently.
        start = as_utc(rec.get("start_time"))
        if start is None:
            continue
        try:
            _date_of(rec.get("id", ""))
        except RunIdError:
            continue
        age_days = (now - start).days
        # failed runs get a longer retention window than successful ones
        log_limit = retention_days_failed if rec.get("outcome") == "failed" else retention_days
        if age_days > log_limit:
            lp = log_path(config, rec["id"])
            if lp.exists():
                lp.unlink()
                removed["logs"] += 1
            # The event stream is the log's machine-readable half and ages out
            # with it, so a pruned run leaves no half-record behind.
            ep = events_path(config, rec["id"])
            if ep.exists():
                ep.unlink()
                removed["events"] += 1
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
