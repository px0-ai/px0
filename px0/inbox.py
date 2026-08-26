"""Where a scheduled run's output goes to be read.

A workflow could route its output three ways, and on a schedule all three had
the same problem. `stdout` goes to a terminal nobody is sitting at. `file`
writes something you have to remember to open. A write tool posts somewhere
else entirely, which is fine when that somewhere is where you already look and
useless when it is not.

So px0 did the work and then had nowhere to say so. `px0 status` was the
nearest thing and it answers a different question -- whether anything is
broken, not what arrived.

This is the missing half: a per-store inbox that scheduled runs deliver into,
so "what happened while I was away" is one command. An entry is small on
purpose -- what produced it, a preview, and where the whole thing is -- because
the inbox is a place to triage from, not a second copy of the output.
"""

import json
import secrets
from datetime import datetime, timedelta, timezone
from pathlib import Path

from px0 import config as config_mod, paths

UNREAD, READ, ARCHIVED = "unread", "read", "archived"

# How much of the output an entry carries. Enough to decide whether to open the
# rest from a listing; short enough that a month of dailies is still a small
# directory.
PREVIEW_CHARS = 600


class InboxError(Exception):
    """Raised when an entry cannot be found."""
    pass


def inbox_dir(home: Path) -> Path:
    """Where delivered entries live."""
    return paths.state_dir(home) / "inbox"


def _path(home: Path, entry_id: str) -> Path:
    return inbox_dir(home) / f"{entry_id}.json"


def new_id() -> str:
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    return f"box_{stamp}-{secrets.token_hex(2)}"


def title_for(text: str, fallback: str) -> str:
    """The line an entry is listed by.

    Taken from the output's own first heading or first non-empty line, because
    a workflow that already writes "## PRs you reviewed this week" has said
    what the entry is better than any label px0 could synthesize. Falls back to
    the workflow id when the output opens with nothing usable.
    """
    for raw in (text or "").splitlines():
        line = raw.strip().lstrip("#").strip()
        if line:
            return line[:100]
    return fallback


def deliver(home: Path, config: dict, *, workflow_id: str, run_id: str,
            text: str, path: str | None = None, trigger: str = "") -> dict:
    """Files one run's output in the inbox and returns the entry."""
    entry = {
        "id": new_id(),
        "status": UNREAD,
        "workflow_id": workflow_id,
        "run_id": run_id,
        "trigger": trigger,
        "title": title_for(text, workflow_id),
        "preview": (text or "")[:PREVIEW_CHARS],
        "chars": len(text or ""),
        "path": path,
        "created": datetime.now(timezone.utc).isoformat(),
    }
    dest = _path(home, entry["id"])
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(json.dumps(entry, indent=2, default=str))
    return entry


def should_deliver(config: dict, wf, trigger: str, dry_run: bool) -> bool:
    """Whether this run's output belongs in the inbox.

    Scheduled and watched runs deliver by default and manual ones do not: you
    were there for a manual run and have just read its output, where a nightly
    one produced something at 6am that nothing has told you about. A workflow
    can force either answer with `output.inbox`, and a rehearsal never
    delivers -- a dry run's output is a sample, not news.
    """
    if dry_run:
        return False
    explicit = (getattr(wf, "output", None) or {}).get("inbox")
    if isinstance(explicit, bool):
        return explicit
    if (getattr(wf, "output", None) or {}).get("target") == "inbox":
        return True
    if not config_mod.get(config, "inbox.auto", True):
        return False
    return trigger in ("schedule", "watch", "late")


def read_entry(home: Path, entry_id: str) -> dict:
    path = _path(home, entry_id)
    if not path.exists():
        raise InboxError(f"no inbox entry {entry_id!r} (see `px0 inbox`)")
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise InboxError(f"{entry_id} is unreadable: {e}") from e


def write_entry(home: Path, entry: dict) -> None:
    dest = _path(home, entry["id"])
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(json.dumps(entry, indent=2, default=str))


def listing(home: Path, status: str | None = UNREAD,
            workflow: str | None = None) -> list[dict]:
    """Entries matching the filters, newest first. `status=None` for everything."""
    base = inbox_dir(home)
    if not base.exists():
        return []
    out = []
    for path in sorted(base.glob("box_*.json"), reverse=True):
        try:
            entry = json.loads(path.read_text())
        except (OSError, json.JSONDecodeError):
            continue
        if status and entry.get("status") != status:
            continue
        if workflow and entry.get("workflow_id") != workflow:
            continue
        out.append(entry)
    return sorted(out, key=lambda e: e.get("created", ""), reverse=True)


def unread_count(home: Path) -> int:
    """How many entries are waiting. Cheap enough for `px0 status`."""
    return len(listing(home))


def mark(home: Path, entry_id: str, status: str) -> dict:
    entry = read_entry(home, entry_id)
    entry["status"] = status
    entry["touched"] = datetime.now(timezone.utc).isoformat()
    write_entry(home, entry)
    return entry


def body(home: Path, config: dict, entry: dict) -> str:
    """The whole of what an entry announces.

    Read back from the file the run wrote when there is one, so opening an
    entry shows what is on disk now rather than a copy frozen at delivery. An
    entry whose file has since been deleted falls back to its preview and says
    so, rather than showing nothing.
    """
    if entry.get("path"):
        # Store-relative, as `route_output` records it -- the same path
        # `px0 runs open` resolves against the store.
        target = Path(entry["path"])
        if not target.is_absolute():
            target = home / entry["path"]
        try:
            return target.read_text()
        except OSError:
            preview = entry.get("preview", "")
            return (f"{preview}\n\n[the file this announced is gone: "
                    f"{entry['path']}]") if preview else ""
    return entry.get("preview", "")


def clear(home: Path, older_than_days: int | None = None,
          status: str | None = None) -> int:
    """Deletes entries, optionally only the ones past an age or in one state.
    Returns how many went."""
    cutoff = None
    if older_than_days is not None:
        cutoff = datetime.now(timezone.utc) - timedelta(days=max(0, older_than_days))
    removed = 0
    for entry in listing(home, status=status):
        if cutoff is not None:
            try:
                created = datetime.fromisoformat(entry.get("created", ""))
            except ValueError:
                continue
            if created.tzinfo is None:
                created = created.replace(tzinfo=timezone.utc)
            if created >= cutoff:
                continue
        _path(home, entry["id"]).unlink(missing_ok=True)
        removed += 1
    return removed


def apply_retention(home: Path, config: dict) -> int:
    """Drops read and archived entries past the configured window. Unread
    entries are never dropped: an inbox that quietly forgets what you have not
    looked at is worse than one that grows."""
    days = int(config_mod.get(config, "inbox.keep_days", 30) or 0)
    if days <= 0:
        return 0
    removed = 0
    for state in (READ, ARCHIVED):
        removed += clear(home, older_than_days=days, status=state)
    return removed
