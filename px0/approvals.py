"""Write tool calls that wait for a person.

px0's trust model was binary: a tool either mutates something or it does not,
and a workflow either may call it or may not. `--dry-run` stubs every write,
which rehearses a workflow but never does the work. There was no middle -- no
way to say *draft it and ask me* -- so anything that speaks in the user's name
had to be handed the real capability up front, on the strength of a plan they
read once.

This is that middle. A call that needs approval is not executed: it is written
down in full -- tool, arguments, the run that drafted it, and what that run was
for -- and the model is told it has been queued. The user sees exactly what
would be sent, and approving it makes the call for real, then and there.

Two properties matter more than the feature:

**A queued call is never a silent one.** It is recorded on the run, counted by
`px0 status`, and notified on the same policy as a failure. An approval nobody
knows about is worse than no approval.

**Approving executes; it does not re-run.** The arguments the user read are the
arguments that go out. Re-running the workflow to "do it for real" would draft
something else -- a later hour, a changed source -- and the thing they approved
would never have been sent.
"""

import json
import re
import secrets
from datetime import datetime, timedelta, timezone
from pathlib import Path

from px0 import config as config_mod, paths

# Terminal states an approval can reach, and the one it starts in.
PENDING, APPROVED, REJECTED, EXPIRED, FAILED = (
    "pending", "approved", "rejected", "expired", "failed")


class ApprovalError(Exception):
    """Raised when an approval cannot be found, or is acted on twice."""
    pass


def approvals_dir(home: Path) -> Path:
    """Where drafted calls wait. Under `.state/` because a pending approval is
    live runtime state, not content the user authors -- but it is plain JSON,
    so nothing stops them reading one."""
    return paths.state_dir(home) / "approvals"


def _path(home: Path, approval_id: str) -> Path:
    return approvals_dir(home) / f"{approval_id}.json"


def new_id() -> str:
    """A short id a person can retype without resenting it."""
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    return f"apr_{stamp}-{secrets.token_hex(2)}"


def needs_approval(wf, config: dict, tool_id: str, is_write: bool) -> bool:
    """Whether this call must wait for a person.

    Read tools never do: an approval queue that fills up with searches is one
    nobody reads, and the point of the gate is the calls that leave a mark.

    A workflow's own `confirm:` wins over the global default in both
    directions, so the one workflow that posts to a public channel can ask
    even when nothing else does, and the trusted nightly job can be exempted
    without turning the setting off everywhere.
    """
    if not is_write:
        return False
    setting = getattr(wf, "confirm", None)
    if isinstance(setting, bool):
        return setting
    if isinstance(setting, (list, tuple, set)):
        return tool_id in setting
    return bool(config_mod.get(config, "tools.confirm_writes", False))


def queue(home: Path, *, run_id: str, workflow_id: str, tool: str, args: dict,
          reason: str = "", output_preview: str = "") -> dict:
    """Writes down a drafted call and returns it.

    `reason` is what the workflow was for, and `output_preview` is what the run
    had produced by the time it drafted this. Both exist so the approval can be
    judged on its own screen: a Slack message shown without the digest it is
    announcing is a decision made blind.
    """
    approval = {
        "id": new_id(),
        "status": PENDING,
        "run_id": run_id,
        "workflow_id": workflow_id,
        "tool": tool,
        "args": args,
        "reason": reason,
        "output_preview": output_preview[:2000],
        "created": datetime.now(timezone.utc).isoformat(),
        "store": str(home),
    }
    path = _path(home, approval["id"])
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(approval, indent=2, default=str))
    return approval


def attach_output(home: Path, run_id: str, text: str) -> int:
    """Gives every pending draft from one run the output that run produced.

    A write is drafted mid-run, before the run has an answer, so at the moment
    of queueing there is nothing to show but the model's own protocol line. The
    finished output is what a person actually needs in order to judge the call
    -- a Slack message shown without the digest it announces is a decision made
    blind -- so it is filled in once the run has one.
    """
    filled = 0
    for approval in listing(home, config=None, status=PENDING):
        if approval.get("run_id") != run_id:
            continue
        approval["output_preview"] = (text or "")[:2000]
        write(home, approval)
        filled += 1
    return filled


def read(home: Path, approval_id: str) -> dict:
    """One approval by id. Raises ApprovalError if there is no such file."""
    path = _path(home, approval_id)
    if not path.exists():
        raise ApprovalError(f"no approval {approval_id!r} (see `px0 approvals`)")
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise ApprovalError(f"{approval_id} is unreadable: {e}") from e


def write(home: Path, approval: dict) -> None:
    """Persists an approval, overwriting the file for its id."""
    path = _path(home, approval["id"])
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(approval, indent=2, default=str))


def listing(home: Path, config: dict | None = None, status: str | None = PENDING,
            workflow: str | None = None) -> list[dict]:
    """Approvals matching the filters, oldest first.

    Oldest first, unlike every run listing in px0, because this is a queue
    rather than a history: the thing most likely to have gone stale is the
    thing you most need to see.

    Expiry is applied on read rather than by a sweep, so a store whose daemon
    never runs does not accumulate week-old drafts that still look actionable.
    """
    base = approvals_dir(home)
    if not base.exists():
        return []
    out = []
    for path in sorted(base.glob("apr_*.json")):
        try:
            approval = json.loads(path.read_text())
        except (OSError, json.JSONDecodeError):
            continue
        if config is not None and approval.get("status") == PENDING:
            approval = _expire_if_stale(home, config, approval)
        if status and approval.get("status") != status:
            continue
        if workflow and approval.get("workflow_id") != workflow:
            continue
        out.append(approval)
    return sorted(out, key=lambda a: a.get("created", ""))


def _expire_if_stale(home: Path, config: dict, approval: dict) -> dict:
    """Marks an approval expired once it is older than the configured window.

    A drafted message is written against a moment. Sending last Tuesday's
    standup on Friday because it was still sitting in the queue is worse than
    not sending it, so old drafts stop being approvable rather than waiting
    indefinitely.
    """
    days = int(config_mod.get(config, "approvals.expire_days", 7) or 0)
    if days <= 0:
        return approval
    try:
        created = datetime.fromisoformat(approval["created"])
    except (KeyError, ValueError):
        return approval
    if created.tzinfo is None:
        created = created.replace(tzinfo=timezone.utc)
    if datetime.now(timezone.utc) - created <= timedelta(days=days):
        return approval
    approval["status"] = EXPIRED
    approval["resolved"] = datetime.now(timezone.utc).isoformat()
    approval["detail"] = f"no answer within {days} day(s)"
    write(home, approval)
    return approval


def pending_count(home: Path, config: dict | None = None) -> int:
    """How many calls are waiting. Cheap enough for `px0 status`."""
    return len(listing(home, config))


def approve(home: Path, config: dict, approval_id: str) -> dict:
    """Executes a drafted call and records what came of it.

    The arguments that were read are the arguments that go out -- this calls
    the tool directly rather than re-running the workflow, because a re-run
    would draft something else and the thing the user approved would never
    have been sent.

    A tool that fails here leaves the approval in `failed` rather than back in
    `pending`: retrying is the user's decision, and an approval that silently
    returned to the queue would be approved twice.
    """
    from px0 import tools  # deferred: tools imports config, which imports nothing here

    approval = read(home, approval_id)
    if approval.get("status") != PENDING:
        raise ApprovalError(
            f"{approval_id} is already {approval.get('status')}, nothing to approve")

    approval["resolved"] = datetime.now(timezone.utc).isoformat()
    try:
        result = tools.call(home, config, approval["tool"], approval.get("args") or {})
    except tools.ConnectorError as e:
        approval["status"] = FAILED
        approval["detail"] = str(e)[:500]
        write(home, approval)
        return approval

    approval["status"] = APPROVED
    approval["edited"] = bool(approval.get("edits"))
    approval["result_summary"] = str(result)[:500]
    write(home, approval)
    _record_on_run(home, config, approval)
    return approval


def amend(home: Path, approval_id: str, args: dict, note: str = "") -> dict:
    """Replaces a drafted call's arguments before it is sent.

    "Right message, wrong channel" had only one answer: reject it and run the
    workflow again, which drafts something else against a later hour. The
    message the user actually wanted was never sendable.

    The edit is stamped on the approval, so what goes out is not silently
    different from what the run produced -- an approval whose history says
    only "approved" would hide that a person changed it.
    """
    approval = read(home, approval_id)
    if approval.get("status") != PENDING:
        raise ApprovalError(
            f"{approval_id} is already {approval.get('status')}, nothing to edit")
    if not isinstance(args, dict):
        raise ApprovalError("arguments must be a mapping")
    edits = approval.setdefault("edits", [])
    edits.append({"at": datetime.now(timezone.utc).isoformat(),
                  "was": approval.get("args"), "note": note.strip()})
    approval["args"] = args
    write(home, approval)
    return approval


def reject(home: Path, config: dict, approval_id: str, reason: str = "") -> dict:
    """Discards a drafted call. Nothing is sent, and the note is kept.

    The note is worth asking for: "wrong channel" and "we decided not to
    announce this" are the same rejection to the queue and different facts
    about the workflow, and `px0 workflows improve` reads them.
    """
    approval = read(home, approval_id)
    if approval.get("status") != PENDING:
        raise ApprovalError(
            f"{approval_id} is already {approval.get('status')}, nothing to reject")
    approval["status"] = REJECTED
    approval["detail"] = reason.strip()
    approval["resolved"] = datetime.now(timezone.utc).isoformat()
    write(home, approval)
    _record_on_run(home, config, approval)
    return approval


def _record_on_run(home: Path, config: dict, approval: dict) -> None:
    """Writes the outcome back onto the run that drafted the call.

    Without this a run's record says a call was queued and never says what
    happened to it, so `px0 runs why` stops short of the answer and
    `px0 workflows health` cannot tell a workflow whose drafts are always
    approved from one whose drafts are always thrown away. Best-effort: the
    approval itself is already saved, and a pruned run must not fail it.
    """
    from px0 import runs as runs_mod

    run_id = approval.get("run_id")
    if not run_id:
        return
    try:
        record = runs_mod.read_record(config, run_id)
    except (FileNotFoundError, runs_mod.RunIdError):
        return
    for entry in record.get("approvals") or []:
        if entry.get("id") == approval["id"]:
            entry["status"] = approval["status"]
            entry["detail"] = approval.get("detail", "")
            entry["resolved"] = approval.get("resolved")
    try:
        runs_mod.write_record(config, record)
        runs_mod.append_event(config, run_id, "approval_resolved",
                              approval=approval["id"], tool=approval.get("tool"),
                              status=approval["status"],
                              detail=approval.get("detail", "")[:200] or None)
    except OSError:
        pass


# --- answering from somewhere other than the terminal ---------------------
#
# The queue notifies you wherever you asked to be notified and could only be
# answered at the machine px0 runs on. That is the wrong shape for a
# drafted-write model: approvals happen when you are away from the desk, which
# is exactly when you cannot reach the terminal.
#
# px0 has no server by choice, so this is polling rather than a callback: a
# read tool is asked what came back, and replies naming an approval are acted
# on. Off unless configured, and gated on who sent the reply -- anyone in a
# Slack channel can type "approve apr_...", and without the sender check that
# would be a queue anybody could empty.

# The verb has to *open* the reply, after nothing but whitespace, a quote, or
# an @mention. Matching it anywhere in the message read "do not approve apr_x"
# as an approval and sent it -- the exact opposite of what a trusted person had
# just said, with a message going out as the consequence. Anchoring means an
# ambiguous sentence matches nothing, and matching nothing does nothing.
_REPLY_RE = re.compile(
    r"^[\s>*_`\"']*(?:@[\w.-]+[\s:,]*)?"
    r"(approve|reject|ok|yes|no)\b[\s:,]*"
    r"(apr_\d{8}-\d{6}-[0-9a-f]{4})", re.I | re.M)

# What each word means. "yes"/"ok" and "no" are here because that is what
# people actually type in reply to a message asking them a question; requiring
# the exact word "approve" would mean a queue that mostly ignores its answers.
_REPLY_VERBS = {"approve": APPROVED, "ok": APPROVED, "yes": APPROVED,
                "reject": REJECTED, "no": REJECTED}


def reply_config(config: dict) -> dict | None:
    """How this store reads replies, or None when it does not.

    Requires both a tool to poll and at least one sender to trust. Refusing to
    run half-configured is deliberate: a reply channel with no sender list is
    an approval queue that anyone who can post there is able to empty.
    """
    tool = (config_mod.get(config, "approvals.reply_tool", "") or "").strip()
    senders = config_mod.get(config, "approvals.reply_from", []) or []
    if not tool or not senders:
        return None
    raw_args = config_mod.get(config, "approvals.reply_args", "") or ""
    try:
        args = json.loads(raw_args) if raw_args.strip() else {}
    except json.JSONDecodeError:
        args = {}
    return {
        "tool": tool,
        "args": args if isinstance(args, dict) else {},
        "senders": {str(s).strip().lower() for s in senders if str(s).strip()},
        "text_field": (config_mod.get(config, "approvals.reply_text_field", "")
                       or "").strip(),
        "sender_field": (config_mod.get(config, "approvals.reply_sender_field", "")
                         or "").strip(),
    }


def _field(item: dict, named: str, candidates: tuple[str, ...]) -> str:
    """One field out of whatever shape a connector returned.

    Connectors disagree about what a message looks like, so a configured name
    wins and a short list of the usual ones is tried after it. Anything not
    found reads as empty, which fails closed: a reply whose sender cannot be
    identified is not acted on.
    """
    if named and named in item:
        return str(item.get(named) or "")
    for key in candidates:
        if key in item:
            value = item[key]
            if isinstance(value, dict):
                value = value.get("email") or value.get("address") or value.get("name")
            return str(value or "")
    return ""


def parse_replies(items, spec: dict) -> list[dict]:
    """Reads a connector's messages into approval decisions.

    Returns one entry per recognized reply from a trusted sender. A reply that
    names no approval, or comes from anyone else, is not an error and not a
    decision -- it is somebody talking in a channel.
    """
    if isinstance(items, dict):
        for key in ("items", "messages", "data", "results"):
            if isinstance(items.get(key), list):
                items = items[key]
                break
    if not isinstance(items, list):
        return []

    out = []
    for item in items:
        if not isinstance(item, dict):
            continue
        text = _field(item, spec.get("text_field", ""),
                      ("text", "body", "message", "snippet", "content"))
        sender = _field(item, spec.get("sender_field", ""),
                        ("user", "from", "sender", "author", "from_email"))
        match = _REPLY_RE.search(text or "")
        if not match:
            continue
        if sender.strip().lower() not in spec["senders"]:
            out.append({"approval_id": match.group(2), "verdict": None,
                        "sender": sender, "ignored": "sender not trusted"})
            continue
        out.append({"approval_id": match.group(2),
                    "verdict": _REPLY_VERBS[match.group(1).lower()],
                    "sender": sender, "text": (text or "")[:200]})
    return out


def scan_replies(home: Path, config: dict) -> dict:
    """Polls the reply channel and acts on what it finds.

    Every decision goes through `approve` and `reject`, so a reply cannot do
    anything a person at the terminal could not: an expired draft stays
    expired, an already-answered one is not answered twice, and the call that
    goes out is the one that was drafted.
    """
    spec = reply_config(config)
    if not spec:
        return {"polled": False}
    from px0 import tools

    try:
        items = tools.call(home, config, spec["tool"], spec["args"])
    except Exception as e:
        return {"polled": True, "error": str(e)[:300], "acted": []}

    waiting = {a["id"] for a in listing(home, config)}
    acted, ignored = [], []
    for reply in parse_replies(items, spec):
        if reply["approval_id"] not in waiting:
            continue  # already answered, expired, or from another store
        if not reply.get("verdict"):
            ignored.append(reply)
            continue
        try:
            if reply["verdict"] == APPROVED:
                result = approve(home, config, reply["approval_id"])
            else:
                result = reject(home, config, reply["approval_id"],
                                reason=f"replied by {reply['sender']}")
        except ApprovalError:
            continue
        result["answered_by"] = reply["sender"]
        write(home, result)
        acted.append({"id": result["id"], "status": result["status"],
                      "by": reply["sender"]})
    return {"polled": True, "acted": acted, "ignored": ignored}


def purge(home: Path, config: dict, keep_days: int | None = None) -> int:
    """Deletes resolved approvals older than the retention window. Returns how
    many went. Pending approvals are never purged: the queue is the point."""
    days = keep_days if keep_days is not None else int(
        config_mod.get(config, "approvals.keep_resolved_days", 30) or 30)
    cutoff = datetime.now(timezone.utc) - timedelta(days=max(0, days))
    removed = 0
    for approval in listing(home, config=None, status=None):
        if approval.get("status") == PENDING:
            continue
        stamp = approval.get("resolved") or approval.get("created") or ""
        try:
            when = datetime.fromisoformat(stamp)
        except ValueError:
            continue
        if when.tzinfo is None:
            when = when.replace(tzinfo=timezone.utc)
        if when < cutoff:
            _path(home, approval["id"]).unlink(missing_ok=True)
            removed += 1
    return removed
