"""Running a workflow again against the inputs it actually had.

`px0 workflows improve` proposes a revision, you apply it, and you find out
next Friday whether it helped. That is a slow and expensive way to learn
something a model call could settle in a minute — and it is the reason the
improvement loop stopped one step short of being a loop.

What was missing is a fixture. A run resolves its inputs from live sources,
so running the same workflow twice compares two different worlds: the pull
requests moved, the calendar changed, the inbox filled. Nothing could be held
still long enough to say "this wording is better than that one".

So a run can be asked to keep what it read. With that on disk, the old request
and the new one can be rendered against **the same inputs** and their outputs
put side by side — which turns a proposal from an argument into a comparison.

Capture is off by default and per workflow, and that is not timidity. A
fixture is the content of your work: the emails, the diffs, the messages. It
belongs on disk only where someone decided it should, on a short retention,
outside the store that gets synced and exported.
"""

import difflib
import json
import shutil
from datetime import datetime, timedelta, timezone
from pathlib import Path

from px0 import config as config_mod, paths

# How long a fixture lives. Short: it exists to answer "is this revision
# better", which is a question asked days after a run, not months.
DEFAULT_KEEP_DAYS = 14


class ReplayError(Exception):
    """Raised when a run cannot be replayed."""
    pass


def fixtures_dir(home: Path) -> Path:
    """Where captured inputs live: under `.state/`, never in the store proper.

    Deliberately not beside `workflows/` or `output/`. Those are folders people
    sync, export, and open in an editor; a fixture is a copy of whatever a
    connector returned, and it should not travel by accident.
    """
    return paths.state_dir(home) / "fixtures"


def _path(home: Path, workflow_id: str, run_id: str) -> Path:
    return fixtures_dir(home) / workflow_id / f"{run_id}.json"


def capture_enabled(config: dict, wf) -> bool:
    """Whether this run should keep what it read.

    A workflow's own `capture: true` is the ordinary way to turn it on, for the
    one workflow being worked on. `runs.capture_inputs` turns it on across the
    store for someone deliberately gathering fixtures, and a workflow can still
    opt out of that with `capture: false`.
    """
    own = getattr(wf, "capture", None)
    if isinstance(own, bool):
        return own
    return bool(config_mod.get(config, "runs.capture_inputs", False))


def capture(home: Path, workflow_id: str, run_id: str, context: dict,
            prompt: str = "") -> Path | None:
    """Writes down what one run resolved, and returns where.

    The rendered prompt is kept beside the inputs because it is what the model
    actually saw: comparing two revisions means comparing two prompts, and
    reconstructing the old one from the inputs alone would mean reimplementing
    the renderer and hoping the two agree.

    Best-effort. A fixture that cannot be written must not fail the run that
    was producing real work.
    """
    payload = {
        "workflow_id": workflow_id,
        "run_id": run_id,
        "captured": datetime.now(timezone.utc).isoformat(),
        "inputs": {k: v for k, v in (context or {}).items()
                   if k not in ("config", "input")},
        "stdin": (context or {}).get("input", {}).get("_stdin", ""),
        "prompt": prompt,
    }
    try:
        dest = _path(home, workflow_id, run_id)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(json.dumps(payload, indent=2, default=str))
        return dest
    except (OSError, TypeError, ValueError):
        return None


def read(home: Path, workflow_id: str, run_id: str) -> dict:
    """One captured fixture. Raises ReplayError when there is none."""
    path = _path(home, workflow_id, run_id)
    if not path.exists():
        raise ReplayError(
            f"no captured inputs for {run_id} -- turn capture on with "
            f"`capture: true` in {workflow_id}, then run it once")
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise ReplayError(f"the fixture for {run_id} is unreadable: {e}") from e


def listing(home: Path, workflow_id: str | None = None) -> list[dict]:
    """Every fixture on disk, newest first."""
    base = fixtures_dir(home)
    if not base.exists():
        return []
    out = []
    for folder in sorted(base.iterdir()):
        if not folder.is_dir() or (workflow_id and folder.name != workflow_id):
            continue
        for path in sorted(folder.glob("*.json"), reverse=True):
            try:
                payload = json.loads(path.read_text())
            except (OSError, json.JSONDecodeError):
                continue
            out.append({"workflow_id": folder.name,
                        "run_id": payload.get("run_id", path.stem),
                        "captured": payload.get("captured", ""),
                        "inputs": sorted((payload.get("inputs") or {}).keys()),
                        "bytes": path.stat().st_size})
    return sorted(out, key=lambda f: f.get("captured", ""), reverse=True)


def latest_for(home: Path, workflow_id: str) -> dict | None:
    """The most recent fixture for a workflow, which is what a replay defaults
    to: the question is almost always about the last run."""
    found = listing(home, workflow_id)
    return found[0] if found else None


def forget(home: Path, workflow_id: str, run_id: str | None = None) -> int:
    """Deletes fixtures. Returns how many went."""
    base = fixtures_dir(home) / workflow_id
    if not base.exists():
        return 0
    if run_id:
        path = base / f"{run_id}.json"
        if path.exists():
            path.unlink()
            return 1
        return 0
    count = len(list(base.glob("*.json")))
    shutil.rmtree(base, ignore_errors=True)
    return count


def apply_retention(home: Path, config: dict) -> int:
    """Drops fixtures past their window. Returns how many went.

    Retention matters more here than anywhere else in px0, because a fixture is
    the only place the *content* of a run's inputs is written down.
    """
    days = int(config_mod.get(config, "runs.fixture_keep_days", DEFAULT_KEEP_DAYS) or 0)
    if days <= 0:
        return 0
    cutoff = datetime.now(timezone.utc) - timedelta(days=days)
    removed = 0
    base = fixtures_dir(home)
    if not base.exists():
        return 0
    for folder in base.iterdir():
        if not folder.is_dir():
            continue
        for path in folder.glob("*.json"):
            try:
                stamp = datetime.fromisoformat(
                    json.loads(path.read_text())["captured"])
            except (OSError, ValueError, KeyError, json.JSONDecodeError):
                continue
            if stamp.tzinfo is None:
                stamp = stamp.replace(tzinfo=timezone.utc)
            if stamp < cutoff:
                path.unlink(missing_ok=True)
                removed += 1
    return removed


def render_with(home: Path, config: dict, wf, fixture: dict,
                request: str | None = None, body: str | None = None) -> str:
    """Builds the prompt a workflow would send, against captured inputs.

    Neither the input tools nor the clock are touched: the whole point is that
    the world is held still. `body` replaces the instruction text, which is how
    two revisions are compared -- the same inputs through two sets of
    instructions.
    """
    from px0 import guidelines as guidelines_mod, memory as memory_mod, runner

    context = dict(fixture.get("inputs") or {})
    context["config"] = config
    context["input"] = {"_stdin": fixture.get("stdin", "")}

    guideline_texts = {g: guidelines_mod.body_of(home, g) for g in wf.guidelines}
    memory_block = ""
    if memory_mod.enabled(config):
        query = f"{wf.description} {request or wf.request} {body or wf.body}"
        memory_block = memory_mod.as_prompt_block(
            memory_mod.relevant(home, query, budget=memory_mod.budget_chars(config)))

    if body is None:
        return runner.render_prompt(wf, guideline_texts, context, memory_block)

    import copy

    variant = copy.copy(wf)
    variant.body = body
    return runner.render_prompt(variant, guideline_texts, context, memory_block)


def answer_for(config: dict, prompt: str, timeout: float = 180) -> str:
    """One model call with no tools, for comparing two prompts.

    Tools are deliberately absent. A replay compares what a workflow *says*
    against fixed inputs; letting it call anything would both change the world
    and reintroduce the variance the fixture exists to remove.
    """
    from px0 import harness

    return harness.invoke_detailed(config, prompt, timeout=timeout).text


def diff(before: str, after: str, context: int = 2) -> list[tuple[str, str]]:
    """Two outputs as a line diff, as (marker, text) pairs.

    Unified rather than full, because the useful question about a revision is
    what changed, and two digests that agree on nine paragraphs out of ten
    should not print nine paragraphs.
    """
    old = (before or "").splitlines()
    new = (after or "").splitlines()
    out: list[tuple[str, str]] = []
    for line in difflib.unified_diff(old, new, lineterm="", n=context):
        if line.startswith(("---", "+++")):
            continue
        if line.startswith("@@"):
            out.append(("@", line))
        elif line.startswith("-"):
            out.append(("-", line[1:]))
        elif line.startswith("+"):
            out.append(("+", line[1:]))
        else:
            out.append((" ", line[1:] if line.startswith(" ") else line))
    return out


def summarize(before: str, after: str) -> dict:
    """What changed between two outputs, in numbers.

    Printed above the diff because the first question about a revision is
    whether it changed anything at all -- and a proposal that rewrites every
    line of a working digest is one to look at twice, however good its
    reasoning read.
    """
    changes = diff(before, after)
    added = sum(1 for marker, _ in changes if marker == "+")
    removed = sum(1 for marker, _ in changes if marker == "-")
    old_lines = len((before or "").splitlines()) or 1
    return {
        "added": added,
        "removed": removed,
        "identical": added == 0 and removed == 0,
        "churn": round((added + removed) / old_lines, 2),
        "before_chars": len(before or ""),
        "after_chars": len(after or ""),
    }
