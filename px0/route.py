"""`px0 ask`: one question, routed to whatever can answer it.

Everything px0 could do, it could only do if you already knew it was there.
`px0 brain ask` answered from material you had ingested and explicitly "never
touches connectors or guidelines", so it could tell you what a post said about
caching and not what was on your calendar. Running a workflow meant knowing a
workflow existed and typing its id. There was no way to simply ask.

This is that: a question in, and a decision about who should answer it.

- **memory** — px0 already knows, from what you have told it.
- **brain** — retrieval over what you have read, which is what `brain ask` did.
- **workflow** — you have built something that does exactly this; run it.
- **tool** — a single read-only call answers it, so make the call.
- **answer** — none of the above; just answer, with memory as context.

The routing is one cheap model call over an index the store already keeps:
workflow descriptions, tool descriptions, memory subjects. It is the same trick
`select_guidelines` plays for guidelines, and it works for the same reason --
a description written to be findable is a better index than the content is.

Two limits are deliberate. A workflow that can write is never run from here
without being confirmed, because "what's my standup look like" should not post
it. And a route is a suggestion px0 shows you when it is not obvious: the
decision is printed with the answer, so a wrong route is visible rather than
mysterious.
"""

import json
from dataclasses import dataclass, field
from pathlib import Path

from px0 import builder, harness, memory as memory_mod
from px0 import tools as tools_mod
from px0 import workflow as workflow_mod

ROUTES = ("memory", "brain", "workflow", "tool", "answer")

# How many candidates of each kind the router is shown. The index is cheap but
# the prompt is not: a store with two hundred workflows should not spend most
# of a routing call listing them.
MAX_CANDIDATES = 60


class RouteError(Exception):
    """Raised when a question cannot be routed."""
    pass


@dataclass
class Decision:
    """Where a question should go, and why."""
    route: str
    reason: str = ""
    workflow: str | None = None
    tool: str | None = None
    args: dict = field(default_factory=dict)
    inputs: dict = field(default_factory=dict)
    confidence: str = "unclear"

    def as_dict(self) -> dict:
        return {"route": self.route, "reason": self.reason,
                "workflow": self.workflow, "tool": self.tool, "args": self.args,
                "inputs": self.inputs, "confidence": self.confidence}


def candidates(home: Path, config: dict) -> dict:
    """The index the router chooses from.

    Only read tools are offered. A router that could reach a write tool would
    be one bad classification away from sending something, and the whole
    argument for a front door is that asking it a question is safe.
    """
    workflows = []
    for wf in sorted(workflow_mod.load_all(home).values(), key=lambda w: w.id):
        if not wf.enabled:
            continue
        # A template is not a candidate: the router has no way to supply the
        # values it declares, so routing a question to one can only ever end in
        # the run refusing. Offering it would spend a model call to reach a
        # dead end and read as px0 picking the wrong workflow.
        if any(v["required"] for v in workflow_mod.declared_vars(wf)):
            continue
        workflows.append({
            "id": wf.id,
            "description": wf.description or wf.request,
            "writes": any(_is_write(t, home) for t in wf.tools),
            "inputs": [i.id for i in wf.inputs if i.kind == "source"],
        })

    readable = []
    for spec in tools_mod.list_tools(home=home):
        if spec.is_write:
            continue
        readable.append({"id": spec.id, "description": spec.description,
                         "params": spec.params})

    return {
        "workflows": workflows[:MAX_CANDIDATES],
        "tools": readable[:MAX_CANDIDATES],
        "memory_subjects": [m.summary for m in memory_mod.load_all(home).values()][:MAX_CANDIDATES],
        "has_brain": _brain_has_content(home, config),
    }


def _is_write(tool_id: str, home: Path) -> bool:
    try:
        return tools_mod.is_write(tool_id, home)
    except KeyError:
        return False


def _brain_has_content(home: Path, config: dict) -> bool:
    """Whether there is anything to retrieve, so the router is not offered a
    route that can only fail."""
    from px0 import retrieval

    try:
        base = retrieval.brain_path(home, config)
        return any(base.rglob("*.md"))
    except (OSError, AttributeError):
        return False


_INSTRUCTIONS = """\
Route one question from the user of a personal automation tool to whoever can \
answer it. Reply with ONE JSON object and nothing else:

{"route": "memory|brain|workflow|tool|answer",
 "workflow": "<workflow id, only when route is workflow>",
 "tool": "<tool id, only when route is tool>",
 "args": {<arguments for that tool, only when route is tool>},
 "reason": "<one short sentence on why this route>",
 "confidence": "high|medium|low"}

Choose like this:

- "workflow" when one of the listed workflows plainly does what was asked. \
This is the best answer when it applies: the user built that workflow for \
this, and it knows things the question does not say.
- "tool" when one listed read-only tool answers it directly and you can fill \
in every argument from the question itself. Never invent an argument the \
question did not settle -- no placeholder owners, repositories, or channels.
- "brain" when the answer is in something the user has read and kept, rather \
than in live data. Questions about posts, papers, and notes.
- "memory" when px0 already knows this because the user told it: their \
preferences, their people, their projects.
- "answer" when none of the above fits -- general knowledge, reasoning, or \
writing that needs no lookup at all. This is a perfectly good answer; do not \
force a route that does not fit.

Prefer the cheapest route that actually answers the question. If two fit, \
prefer workflow over tool, and tool over answer.
"""


def decide(config: dict, question: str, index: dict, timeout: float = 60) -> Decision:
    """Asks the model where a question should go.

    A reply that names a route px0 does not have, or a workflow or tool that is
    not in the index it was shown, falls back to `answer` rather than raising:
    the worst outcome of a bad route should be a plain reply, never a failure
    to answer at all.
    """
    prompt = (f"{_INSTRUCTIONS}\n---\nAVAILABLE\n"
              f"{json.dumps(index, indent=2, default=str)}\n\n"
              f"---\nQUESTION\n{question}\n")
    try:
        raw = harness.invoke(config, prompt, timeout=timeout)
    except harness.HarnessError as e:
        raise RouteError(str(e)) from e

    try:
        data = builder._extract_json(raw)
    except builder.BuilderError:
        return Decision("answer", reason="the router did not answer in JSON")
    if not isinstance(data, dict):
        return Decision("answer", reason="the router answered with a list")

    route = str(data.get("route") or "").strip().lower()
    if route not in ROUTES:
        return Decision("answer", reason=f"unknown route {route!r}")

    workflow = str(data.get("workflow") or "").strip() or None
    tool = str(data.get("tool") or "").strip() or None
    known_workflows = {w["id"] for w in index.get("workflows", [])}
    known_tools = {t["id"] for t in index.get("tools", [])}

    if route == "workflow" and workflow not in known_workflows:
        return Decision("answer", reason=f"routed to unknown workflow {workflow!r}")
    if route == "tool" and tool not in known_tools:
        return Decision("answer", reason=f"routed to unknown tool {tool!r}")

    args = data.get("args")
    return Decision(
        route=route, reason=str(data.get("reason") or "").strip(),
        workflow=workflow, tool=tool,
        args=args if isinstance(args, dict) else {},
        confidence=str(data.get("confidence") or "unclear").strip().lower(),
    )


def answer_directly(config: dict, question: str, memories: list,
                    timeout: float = 90, context: str = "") -> str:
    """Answers from the model plus what px0 remembers, and nothing else.

    The memory block is included rather than left out because this is the route
    for questions that need no lookup, and "what did I decide about the release
    process" is exactly such a question when the decision is in memory.

    `context` carries the conversation so far, so a follow-up is answered as a
    follow-up -- and so a correction is accepted rather than argued with.
    """
    block = memory_mod.as_prompt_block(memories)
    prompt = ((f"{block}\n\n---\n" if block else "")
              + (f"{context}\n\n---\n" if context else "")
              + "Answer the user's question directly and briefly. If you do "
                "not know and nothing above tells you, say so plainly rather "
                "than guessing.\n\n"
                f"Question: {question}")
    try:
        return harness.invoke(config, prompt, timeout=timeout)
    except harness.HarnessError as e:
        raise RouteError(str(e)) from e


def summarize_tool_result(config: dict, question: str, tool_id: str,
                          result, timeout: float = 90) -> str:
    """Turns one tool's raw answer into a reply to the question that prompted it.

    Without this the route returns a wall of JSON, which is a worse answer than
    the one `answer` would have given -- and the point of routing to a tool is
    that live data makes for a *better* answer, not a rawer one.
    """
    payload = json.dumps(result, default=str)[:6000]
    prompt = ("Answer the question using this tool result. Be brief and "
              "concrete. If the result does not answer it, say so.\n\n"
              f"Question: {question}\n\nTool: {tool_id}\nResult:\n{payload}")
    try:
        return harness.invoke(config, prompt, timeout=timeout)
    except harness.HarnessError as e:
        raise RouteError(str(e)) from e


def repeated_questions(config: dict, question: str, threshold: int = 3) -> list[dict]:
    """Past asks that look like this one, once there are enough to mention.

    The observation worth surfacing is not that a question was asked twice, but
    that the user keeps doing by hand something px0 could have been doing on a
    schedule. Same records, read for a different question -- which is the same
    trick `px0 workflows health` plays.
    """
    from px0 import runs as runs_mod

    wanted = memory_mod._terms(question)
    if not wanted:
        return []
    similar = []
    for record in runs_mod.list_records(config):
        asked = record.get("question")
        if not asked or record.get("workflow_id"):
            continue
        theirs = memory_mod._terms(asked)
        if not theirs:
            continue
        overlap = len(wanted & theirs) / max(len(wanted | theirs), 1)
        if overlap >= 0.6:
            similar.append({"id": record.get("id"), "question": asked,
                            "when": record.get("start_time")})
    return similar if len(similar) + 1 >= threshold else []
