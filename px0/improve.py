"""`px0 workflows improve`: read a workflow's own runs, propose a revision.

The deterministic half of this loop lives in `analysis`, and it is the half
that decides what is true. This module only takes that report, plus the runs
behind it, and asks the model one question: given what these runs did, what
should this workflow say instead?

Three rules shape everything here, and each of them is a rule because the
obvious alternative is worse:

**The proposal edits the request, not the file.** A workflow's tools, inputs,
and guideline list all follow from its request -- `px0 workflows edit` argues
this in its own docstring -- so a model that rewrote the body directly would
leave frontmatter describing a workflow that no longer exists. What comes back
here is a new request, and it is rebuilt through exactly the path a hand-typed
edit takes.

**Nothing is applied without being shown.** px0's posture everywhere else is
to list what it is about to do and wait. An improvement pass that quietly
rewrote a scheduled workflow would be the one place that stopped being true.

**Tools are never widened by a model's say-so.** A proposal may argue for a new
tool, and that argument is printed, but the tool itself only ever arrives
through the same confirm-and-authorize path `px0 workflows new` uses.
"""

import json
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path

from px0 import analysis, builder, guidelines as guidelines_mod, harness
from px0 import workflow as workflow_mod

# How much of a run's own output a proposal is allowed to see. Enough to judge
# whether a digest was any good; short enough that twenty of them still leave
# room for the instructions.
OUTPUT_EXCERPT = 700
MAX_MARKED_RUNS = 6
MAX_RECENT_RUNS = 12
MAX_ERROR_SHAPES = 5


class ImproveError(Exception):
    """Raised when a proposal cannot be produced or cannot be read."""
    pass


@dataclass
class GuidelineEdit:
    """One proposed change to a guideline, which may be a file that exists or a
    file the proposal wants written.

    Guidelines get their own field rather than being folded into the request
    because they are the right home for a whole class of complaint. "The
    summary is too long" is not a fact about this workflow -- it is a standard,
    and writing it into a guideline fixes every workflow that shares it, where
    writing it into one request fixes exactly one.
    """
    path: str
    addition: str
    why: str = ""
    is_new: bool = False
    description: str = ""


@dataclass
class Proposal:
    """What the model came back with, after being read into a shape px0 trusts."""
    diagnosis: str = ""
    request: str = ""
    reasoning: str = ""
    confidence: str = "unclear"
    # The revised instruction text, when the model offered one. Only used to
    # *show* what a revision would produce, against captured inputs -- never
    # written to the file, because the file is regenerated from the request.
    body: str = ""
    tool_adds: list[str] = field(default_factory=list)
    tool_drops: list[str] = field(default_factory=list)
    guideline_edits: list[GuidelineEdit] = field(default_factory=list)
    raw: str = ""

    def changes_request(self, current: str) -> bool:
        """Whether the revised request actually differs from the one on file.

        Compared on collapsed whitespace, because a proposal that returns the
        same sentence rewrapped is a proposal that found nothing, and rebuilding
        a workflow for it would spend a model call and a version to arrive
        exactly where it started.
        """
        return " ".join(self.request.split()) != " ".join((current or "").split())

    def is_empty(self, current: str) -> bool:
        return (not self.changes_request(current)
                and not self.guideline_edits and not self.tool_drops)


def evidence(home: Path, config: dict, wf: workflow_mod.Workflow, report: dict,
             records: list[dict]) -> dict:
    """The case file a proposal is argued from.

    Assembled here rather than in the prompt string so that `--show-evidence`
    can print the very thing the model was given. A user who disagrees with a
    proposal should be able to see what it was reasoning over, and a case file
    that only exists inside an f-string cannot be shown.

    Run output is included only where it was asked for -- the runs a person
    marked, and the runs that failed. Including every output would be both the
    bulk of the prompt and, mostly, noise: a run nobody complained about is
    evidence that things are fine, and one line saying so carries that.
    """
    live = [r for r in records if not r.get("dry_run")]

    marked = []
    for rec in records:
        review = rec.get("review") or {}
        if not review.get("verdict"):
            continue
        marked.append({
            "run": rec.get("id"),
            "verdict": review["verdict"],
            "note": review.get("note", ""),
            "output_excerpt": str((rec.get("output") or {}).get("text") or "")[:OUTPUT_EXCERPT],
            "tools_called": [c.get("tool") for c in rec.get("tool_calls") or []],
        })
        if len(marked) >= MAX_MARKED_RUNS:
            break

    shapes: dict[str, dict] = {}
    for rec in live:
        if rec.get("outcome") != "failed":
            continue
        key = analysis.normalize_error(rec.get("error", ""))
        entry = shapes.setdefault(key, {"shape": key, "count": 0, "stage": rec.get("stage"),
                                        "example": str(rec.get("error", ""))[:300]})
        entry["count"] += 1

    recent = []
    for rec in live[:MAX_RECENT_RUNS]:
        calls = rec.get("tool_calls") or []
        recent.append({
            "run": rec.get("id"),
            "outcome": rec.get("outcome"),
            "seconds": rec.get("duration_seconds"),
            "turns": (rec.get("usage") or {}).get("turns"),
            "hit_turn_cap": (rec.get("usage") or {}).get("hit_turn_cap", False),
            "tools": [
                {"tool": c.get("tool"), "failed": analysis._call_failed(c),
                 "refused": bool(c.get("refused"))}
                for c in calls
            ],
            "empty_inputs": [m.get("id") for m in rec.get("inputs_resolved") or []
                             if m.get("empty")],
            "output_chars": len(str((rec.get("output") or {}).get("text") or "")),
        })

    return {
        "workflow": {
            "id": wf.id,
            "request": wf.request,
            "description": wf.description,
            "body": wf.body,
            "tools": list(wf.tools),
            "guidelines": [
                {"path": g, "summary": _guideline_summary(home, g)} for g in wf.guidelines
            ],
            "inputs": [{"id": i.id, "kind": i.kind} for i in wf.inputs],
            "output": wf.output,
            "trigger": wf.trigger,
            "timeout": wf.timeout,
        },
        "window": report.get("runs", {}),
        "findings": [
            {k: f[k] for k in ("code", "severity", "detail", "evidence")}
            for f in report.get("findings", [])
        ],
        "failures": sorted(shapes.values(), key=lambda s: -s["count"])[:MAX_ERROR_SHAPES],
        "marked_runs": marked,
        "recent_runs": recent,
        "available_guidelines": [
            {"path": g.rel, "summary": g.summary}
            for g in guidelines_mod.attachable(home)
        ][:40],
    }


def _guideline_summary(home: Path, rel: str) -> str:
    try:
        return guidelines_mod.parse(
            (home / "guidelines" / rel), rel).summary
    except OSError:
        return ""


_INSTRUCTIONS = """\
You are improving one automation workflow by reading what its own runs did.

You are given: the workflow as it stands, a deterministic health report over \
its recent runs, the runs themselves, and -- where the user left them -- their \
own verdicts on what those runs produced.

Return ONE JSON object and nothing else:

{"diagnosis": "<what is actually wrong, in one or two sentences; say 'nothing \
found' if the runs do not support a change>",
 "request": "<the full revised request for this workflow, written as the user \
would say it, in their voice; return the current request unchanged if no \
change is warranted>",
 "reasoning": "<why this revision follows from the evidence, citing what you \
saw>",
 "confidence": "high|medium|low",
 "body": "<the revised instruction text, if you would change it -- this is \
shown to the user against a real past run so they can see the difference, and \
is not written to the file>",
 "tool_drops": ["<tool id the evidence shows is not needed>"],
 "tool_adds": ["<tool id the work plainly requires but the workflow lacks>"],
 "guideline_edits": [{"path": "<existing guidelines path, or a new one like \
pr-digest-style.md>", "addition": "<markdown rules to add, using '## ' \
headings>", "why": "<what in the evidence calls for it>", "is_new": true, \
"description": "<one line, only when is_new>"}]}

Hold to these:

- The user's verdicts are the strongest evidence there is. A run marked bad is \
a fact about the output that no counter of successes can outweigh.
- Prefer the smallest change that addresses the evidence. Rewriting a working \
request because it could be phrased better is a regression.
- A complaint about form -- length, tone, ordering, what to include -- belongs \
in a guideline, not in the request. Guidelines apply to every workflow that \
carries them; a request applies to one.
- Only name a tool in tool_adds when the runs show the work needs it, such as \
the model repeatedly reaching for a tool it was refused. The user has to \
authorize any new tool by hand, so a speculative one costs them a decision.
- tool_drops is for tools the evidence shows are never used. Do not drop a \
tool merely because a recent window did not need it.
- If the runs support no change, say so in the diagnosis and return the \
current request verbatim. Finding nothing is a valid answer.
- Never invent a run, an error, or a number that is not in the evidence.
"""


def propose(config: dict, case: dict, timeout: float = 180) -> Proposal:
    """Asks the model for a revision, and reads the answer strictly.

    A reply that is not JSON, or that omits the request, raises rather than
    being patched up into something plausible: this proposal is about to be
    shown to a user as a considered recommendation, and half of one read
    through a lenient parser is worse than none.
    """
    prompt = (f"{_INSTRUCTIONS}\n---\nEVIDENCE\n"
              f"{json.dumps(case, indent=2, default=str)}\n")
    try:
        reply = harness.invoke(config, prompt, timeout=timeout)
    except harness.HarnessError as e:
        raise ImproveError(str(e)) from e

    try:
        data = builder._extract_json(reply)
    except builder.BuilderError as e:
        raise ImproveError(f"the model's answer was not usable JSON: {e}") from e
    if not isinstance(data, dict):
        raise ImproveError("the model answered with a list where an object was expected")

    request = str(data.get("request") or "").strip()
    if not request:
        raise ImproveError("the proposal carried no revised request")

    edits = []
    for raw in data.get("guideline_edits") or []:
        if not isinstance(raw, dict):
            continue
        addition = str(raw.get("addition") or "").strip()
        # Through the builder's own sanitizer, not a local approximation of
        # one. The model picks this name, so it is untrusted input on its way
        # to becoming a filesystem path -- and stripping a leading slash, which
        # is all this used to do, leaves `../../.bashrc` intact.
        path = builder._guideline_path(str(raw.get("path") or ""))
        if not path or not addition:
            continue
        edits.append(GuidelineEdit(
            path=path, addition=addition,
            why=str(raw.get("why") or "").strip(),
            is_new=bool(raw.get("is_new")),
            description=str(raw.get("description") or "").strip(),
        ))

    def _ids(key: str) -> list[str]:
        return [str(t).strip() for t in (data.get(key) or []) if str(t).strip()]

    return Proposal(
        diagnosis=str(data.get("diagnosis") or "").strip(),
        request=request,
        body=str(data.get("body") or "").strip(),
        reasoning=str(data.get("reasoning") or "").strip(),
        confidence=str(data.get("confidence") or "unclear").strip().lower(),
        tool_adds=_ids("tool_adds"),
        tool_drops=_ids("tool_drops"),
        guideline_edits=edits,
        raw=reply,
    )


def reconcile_guideline_edits(home: Path, edits: list[GuidelineEdit]) -> list[GuidelineEdit]:
    """Corrects each edit's claim about whether its file already exists.

    The model says `is_new`; the disk decides. Trusting the flag meant a
    proposal that misremembered a path would overwrite an existing guideline
    with two rules where it had ten.
    """
    base = home / "guidelines"
    for edit in edits:
        edit.is_new = not (base / edit.path).exists()
    return edits


def apply_guideline_edit(home: Path, edit: GuidelineEdit) -> Path:
    """Writes one guideline edit, appending to a file rather than replacing it.

    An addition is added to the end of the existing rules, under the headings
    the proposal wrote. The user's own wording above it is never touched --
    a guideline is theirs, and an improvement pass earns the right to add a
    rule, not to rewrite the ones already there.
    """
    base = home / "guidelines"
    target = base / edit.path
    if target.exists():
        existing = guidelines_mod.parse(target, edit.path)
        body = f"{existing.body.rstrip()}\n\n{edit.addition.strip()}\n"
        content = guidelines_mod.render(existing.name, existing.description, body)
        return builder.save_guideline(home, edit.path, content,
                                      description=existing.description,
                                      actor="improve")
    description = edit.description or edit.why or f"Conventions for {edit.path}"
    return builder.save_guideline(home, edit.path, edit.addition,
                                  description=description, actor="improve")


def load_case(home: Path, config: dict, workflow_id: str,
              since: datetime | None = None) -> tuple[workflow_mod.Workflow, dict, dict]:
    """Everything a proposal needs: the workflow, its health report, and the
    evidence assembled from both. One call so the CLI cannot accidentally
    report over one window and propose over another."""
    wf = workflow_mod.load(home, workflow_id)
    report = analysis.health(home, config, workflow_id, since=since)
    records = analysis.gather(config, workflow_id, since=since)
    return wf, report, evidence(home, config, wf, report, records)


def request_diff(current: str, revised: str) -> list[tuple[str, str]]:
    """The two requests as a line-level diff, for printing.

    Returns (marker, text) pairs with markers "-", "+", and " ". Written here
    rather than shelling out to `diff` so it works identically wherever px0
    runs, and returned as data so the CLI decides the colours.
    """
    import difflib
    old = (current or "").strip().splitlines() or [""]
    new = (revised or "").strip().splitlines() or [""]
    out: list[tuple[str, str]] = []
    for line in difflib.ndiff(old, new):
        marker, text = line[:1], line[2:]
        if marker in ("-", "+", " "):
            out.append((marker, text))
    return out
