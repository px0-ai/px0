"""Run records and raw logs. Two artifacts per run, both under the
configurable log directory, both subject to retention -- never inside the
store, so raw prompts and connector responses stay out of any folder the
user might copy or sync."""

import json
import os
import secrets
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from px0 import config as config_mod


def resolve_logs_path(config: dict) -> Path:
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
    ts = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    return f"{prefix}_{ts}-{secrets.token_hex(2)}"


def _date_of(run_id: str) -> str:
    # run_YYYYMMDD-HHMMSS-xxxx -> YYYY-MM-DD
    ts = run_id.split("_", 1)[1].split("-")[0]
    return f"{ts[0:4]}-{ts[4:6]}-{ts[6:8]}"


def record_path(config: dict, run_id: str) -> Path:
    return resolve_logs_path(config) / "records" / _date_of(run_id) / f"{run_id}.json"


def log_path(config: dict, run_id: str) -> Path:
    return resolve_logs_path(config) / "runs" / _date_of(run_id) / f"{run_id}.log"


def write_record(config: dict, record: dict) -> None:
    path = record_path(config, record["id"])
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(record, indent=2, default=str))


def append_raw_log(config: dict, run_id: str, text: str) -> None:
    path = log_path(config, run_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "a") as f:
        f.write(text)
        if not text.endswith("\n"):
            f.write("\n")


def read_record(config: dict, run_id: str) -> dict:
    path = record_path(config, run_id)
    if not path.exists():
        raise FileNotFoundError(f"no run record for {run_id} (may have aged out)")
    return json.loads(path.read_text())


def read_raw_log(config: dict, run_id: str) -> str:
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
    base = resolve_logs_path(config) / "records"
    if not base.exists():
        return []
    records: list[dict] = []
    for date_dir in sorted(base.iterdir(), reverse=True):
        if not date_dir.is_dir():
            continue
        for f in sorted(date_dir.glob("*.json"), reverse=True):
            try:
                rec = json.loads(f.read_text())
            except (json.JSONDecodeError, OSError):
                continue
            if workflow and rec.get("workflow_id") != workflow:
                continue
            if failed and rec.get("outcome") != "failed":
                continue
            if since and rec.get("start_time", "") < since.isoformat():
                continue
            records.append(rec)
    return records


def apply_retention(config: dict) -> dict:
    """Delete artifacts past retention, per config, except runs that
    called a write tool -- those are exempt."""
    now = datetime.now(timezone.utc)
    retention_days = config_mod.get(config, "logs.retention_days", 14)
    retention_days_failed = config_mod.get(config, "logs.retention_days_failed", 60)
    record_retention_days = config_mod.get(config, "logs.record_retention_days", 365)

    removed = {"logs": 0, "records": 0}
    base = resolve_logs_path(config)

    for rec in list_records(config):
        wrote = any(c.get("is_write") for c in rec.get("tool_calls", []))
        if wrote:
            continue
        start = datetime.fromisoformat(rec["start_time"])
        age_days = (now - start).days
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
