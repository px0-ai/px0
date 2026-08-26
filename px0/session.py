"""A conversation with px0, rather than a series of unrelated questions.

`px0 ask` answered one question and forgot it. Every follow-up re-routed from
nothing, so "no, I meant last week" started over — and, worse, the correction
itself was thrown away the moment the command exited. The user had told px0
something true about their work and px0 had no place to put it.

A session is that place. It holds the turns, so a follow-up can be understood
in terms of what came before; and it marks which turns were **corrections**,
because those are the ones worth keeping. What a person says when an answer is
wrong is the highest-signal thing they will say all day, and it was being
dropped on the floor.

Sessions are short-lived and disposable — they live under `.state/` and age
out. What survives a session is what you agreed to remember, which goes into
`memory/` as an ordinary versioned file. The conversation is scaffolding; the
memory is the point.
"""

import json
import re
import secrets
from datetime import datetime, timedelta, timezone
from pathlib import Path

from px0 import config as config_mod, paths

# How many past turns a follow-up is understood against. Long enough to carry a
# thread, short enough that a rambling session does not quietly become the most
# expensive prompt in the store.
CONTEXT_TURNS = 6

# What a correction sounds like. Deliberately a small, literal list rather than
# a model call: this runs on every turn, and being occasionally wrong about
# whether something was a correction costs a suggestion the user can decline,
# where a model call would cost a round trip on every message.
_CORRECTION_MARKERS = (
    "no,", "no ", "not ", "actually", "i meant", "that's wrong", "thats wrong",
    "that is wrong", "wrong", "incorrect", "should be", "it should",
    "i said", "rather than", "instead of", "correction",
)


class SessionError(Exception):
    """Raised when a session cannot be found or read."""
    pass


def sessions_dir(home: Path) -> Path:
    """Where conversations live while they are still going."""
    return paths.state_dir(home) / "sessions"


def _path(home: Path, session_id: str) -> Path:
    return sessions_dir(home) / f"{session_id}.json"


def new_id() -> str:
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    return f"ses_{stamp}-{secrets.token_hex(2)}"


def start(home: Path) -> dict:
    """Opens a new conversation and returns it."""
    session = {"id": new_id(), "turns": [],
               "started": datetime.now(timezone.utc).isoformat()}
    save(home, session)
    return session


def save(home: Path, session: dict) -> None:
    session["touched"] = datetime.now(timezone.utc).isoformat()
    path = _path(home, session["id"])
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(session, indent=2, default=str))


def read(home: Path, session_id: str) -> dict:
    path = _path(home, session_id)
    if not path.exists():
        raise SessionError(f"no session {session_id!r}")
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise SessionError(f"{session_id} is unreadable: {e}") from e


def latest(home: Path) -> dict | None:
    """The most recently touched conversation, for `--continue`."""
    base = sessions_dir(home)
    if not base.exists():
        return None
    best, best_stamp = None, ""
    for path in base.glob("ses_*.json"):
        try:
            session = json.loads(path.read_text())
        except (OSError, json.JSONDecodeError):
            continue
        stamp = session.get("touched") or session.get("started") or ""
        if stamp > best_stamp:
            best, best_stamp = session, stamp
    return best


def looks_like_correction(text: str) -> bool:
    """Whether a turn reads as the user putting px0 right.

    Only meaningful mid-conversation -- an opening question that happens to
    contain "not" is a question, not a correction -- so callers check that
    there is something to correct before asking.
    """
    low = f" {(text or '').strip().lower()} "
    return any(marker in low for marker in _CORRECTION_MARKERS)


def add_turn(home: Path, session: dict, question: str, answer: str,
             route: dict | None = None, run_id: str | None = None) -> dict:
    """Records one exchange, marking it a correction where it reads as one."""
    correction = bool(session.get("turns")) and looks_like_correction(question)
    session.setdefault("turns", []).append({
        "question": question,
        "answer": (answer or "")[:4000],
        "route": route or {},
        "run": run_id,
        "correction": correction,
        "at": datetime.now(timezone.utc).isoformat(),
    })
    save(home, session)
    return session


def corrections(session: dict) -> list[str]:
    """What the user said when px0 got something wrong.

    Paired with the question that preceded it, because "no, last week" means
    nothing on its own and a great deal next to what it was answering.
    """
    out = []
    turns = session.get("turns") or []
    for i, turn in enumerate(turns):
        if not turn.get("correction"):
            continue
        asked = turns[i - 1]["question"] if i else ""
        out.append(f"asked: {asked}\ncorrected with: {turn['question']}"
                   if asked else turn["question"])
    return out


def context_block(session: dict, limit: int = CONTEXT_TURNS) -> str:
    """The conversation so far, as text for a prompt.

    Answers are truncated harder than questions: what the user said is what a
    follow-up refers back to, where px0's own earlier answer is mostly there
    to stop it contradicting itself.
    """
    turns = (session.get("turns") or [])[-limit:]
    if not turns:
        return ""
    lines = ["# The conversation so far", ""]
    for turn in turns:
        lines.append(f"You were asked: {turn['question']}")
        lines.append(f"You answered: {str(turn.get('answer') or '')[:600]}")
        lines.append("")
    lines.append("Answer the next question in that context. When the user is "
                 "correcting you, accept the correction rather than defending "
                 "the earlier answer.")
    return "\n".join(lines)


def resolve_question(session: dict, question: str) -> str:
    """The question as the router should see it, given what came before.

    A follow-up is often unintelligible alone -- "and last week?" names no
    subject. Rather than a second model call to rewrite it, the previous
    question is prepended as context, which is enough for a router choosing
    between five destinations and costs nothing.
    """
    turns = session.get("turns") or []
    if not turns:
        return question
    return f"(following on from: {turns[-1]['question']}) {question}"


def prune(home: Path, config: dict) -> int:
    """Deletes conversations past their retention window.

    Short by default. A session is scaffolding: what was worth keeping from it
    is in `memory/` by then, and what was not should not sit in `.state/`
    forever.
    """
    days = int(config_mod.get(config, "ask.session_days", 7) or 0)
    if days <= 0:
        return 0
    cutoff = datetime.now(timezone.utc) - timedelta(days=days)
    removed = 0
    base = sessions_dir(home)
    if not base.exists():
        return 0
    for path in base.glob("ses_*.json"):
        try:
            session = json.loads(path.read_text())
            stamp = datetime.fromisoformat(
                session.get("touched") or session.get("started"))
        except (OSError, ValueError, json.JSONDecodeError, TypeError):
            continue
        if stamp.tzinfo is None:
            stamp = stamp.replace(tzinfo=timezone.utc)
        if stamp < cutoff:
            path.unlink(missing_ok=True)
            removed += 1
    return removed
