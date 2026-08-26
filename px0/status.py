"""One glance at whether anything needs attention.

Answering "is anything broken?" took three commands: `px0 daemon status` for
whether the scheduler is alive, `px0 runs list --failed` for what went wrong,
and `px0 doctor` for whether the install is sound. This assembles the parts of
those that matter into one answer, and stays cheap enough to run constantly:
no network, no model call.
"""

from datetime import datetime, timedelta, timezone
from pathlib import Path

from px0 import analysis as analysis_mod
from px0 import approvals as approvals_mod
from px0 import config as config_mod, daemon as daemon_mod, inbox as inbox_mod, paths
from px0 import runs as runs_mod
from px0 import workflow as workflow_mod

# How far back a failure still counts as news.
RECENT_HOURS = 24

# How far back the health pass looks. Wider than the failure window on purpose:
# a workflow that has quietly produced nothing useful for a fortnight is not
# news in the last day, and it is exactly what this is meant to surface.
HEALTH_DAYS = 14


def _parse(value) -> datetime | None:
    try:
        stamp = datetime.fromisoformat(str(value))
    except (TypeError, ValueError):
        return None
    return stamp if stamp.tzinfo else stamp.replace(tzinfo=timezone.utc)


def collect(home: Path, config: dict, hours: int = RECENT_HOURS) -> dict:
    """Everything `px0 status` reports, as data.

    Structured rather than printed so `--json` is the same information, and so
    the exit code can be decided by what is in here.
    """
    now = datetime.now(timezone.utc)
    cutoff = now - timedelta(hours=max(1, hours))

    workflows = workflow_mod.load_all(home)
    parse_errors = workflow_mod.load_errors(home)

    scheduled, disabled, watched = [], [], []
    for wf in sorted(workflows.values(), key=lambda w: w.id):
        if not wf.enabled:
            disabled.append(wf.id)
            continue
        if (wf.trigger or {}).get("schedule"):
            scheduled.append(wf.id)
        if workflow_mod.watch_spec(wf):
            watched.append(wf.id)

    daemon = daemon_mod.status(home, config)
    next_fires = daemon.get("next_fires") or {}

    recent, failures = [], []
    for rec in runs_mod.list_records(config):
        started = _parse(rec.get("start_time"))
        if started is None or started < cutoff:
            continue
        entry = {
            "id": rec.get("id"),
            "workflow": rec.get("workflow_id"),
            "outcome": rec.get("outcome", "unknown"),
            "started": rec.get("start_time"),
            "dry_run": bool(rec.get("dry_run")),
            "attempt": rec.get("attempt"),
        }
        recent.append(entry)
        if rec.get("outcome") == "failed" and not rec.get("dry_run"):
            entry = dict(entry)
            entry["error"] = str(rec.get("error") or "")[:200]
            entry["notified"] = (rec.get("notified") or {}).get("notified")
            failures.append(entry)

    running = runs_mod.list_running(home)
    waiting = approvals_mod.pending_count(home, config)
    unread = inbox_mod.unread_count(home)

    notify_policy = (config_mod.get(config, "notify.on_failure", "") or "none").strip() or "none"

    # The deterministic health pass, over a wider window than the failure list.
    # Kept to problems only: `px0 status` answers "is anything broken", and a
    # note about a tool nobody calls is not that. Cheap enough to belong here --
    # records are read once and the rest is arithmetic -- but never allowed to
    # take the command down, since status is what a person runs when something
    # is already wrong.
    health: list[dict] = []
    try:
        overview = analysis_mod.overview(
            home, config, since=now - timedelta(days=HEALTH_DAYS))
        for row in overview["workflows"]:
            for finding in row["findings"]:
                if finding["severity"] != "problem" or finding["code"] == "failing":
                    continue  # failures already have their own line, above
                health.append({"workflow": row["workflow"], **finding})
    except Exception:
        overview = {"workflows": [], "problems": 0}

    problems = []
    if scheduled and not daemon.get("alive"):
        problems.append({
            "detail": f"{len(scheduled)} workflow(s) are scheduled but the daemon is not running",
            "fix": "px0 daemon start",
        })
    if failures:
        problems.append({
            "detail": f"{len(failures)} run(s) failed in the last {hours}h",
            "fix": f"px0 runs why {failures[0]['id']}",
        })
    if failures and notify_policy == "none":
        problems.append({
            "detail": "a failed run tells you nothing: notify.on_failure is 'none'",
            "fix": "px0 config set notify.on_failure desktop",
        })
    if waiting:
        # Not a fault, but the one thing here that is blocked on the user
        # rather than on px0 -- a drafted message nobody answers is a message
        # that never goes out.
        problems.append({
            "detail": f"{waiting} drafted call(s) are waiting for you to approve or reject",
            "fix": "px0 approvals",
        })
    for entry in health[:3]:
        problems.append({
            "detail": f"{entry['workflow']}: {entry['detail']}",
            "fix": entry.get("fix") or f"px0 workflows health {entry['workflow']}",
        })
    if len(health) > 3:
        problems.append({
            "detail": f"{len(health) - 3} more workflow problem(s) not listed",
            "fix": "px0 workflows health",
        })
    for err in parse_errors:
        problems.append({"detail": err, "fix": "px0 workflows validate"})

    return {
        "store": str(home),
        "hours": hours,
        "daemon": {"alive": bool(daemon.get("alive")), "pid": daemon.get("pid"),
                   "platform": daemon_mod.detect_platform()},
        "workflows": {"total": len(workflows), "scheduled": scheduled,
                      "watched": watched, "disabled": disabled,
                      "unparseable": len(parse_errors)},
        "next_fires": next_fires,
        "runs": {"recent": len(recent), "failed": len(failures),
                 "running": running, "failures": failures[:5]},
        "health": {"days": HEALTH_DAYS, "problems": health},
        "approvals": waiting,
        "inbox": unread,
        "notify": notify_policy,
        "problems": problems,
        "ok": not problems,
    }
