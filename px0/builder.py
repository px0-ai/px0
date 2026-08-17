"""px0 new: turn a sentence into a working workflow. Pure planning/
generation functions live here; the interactive plan/confirm/connect/
generate loop lives in the CLI, which is where user prompts belong."""

import difflib
import json
import re
from dataclasses import dataclass, field
from pathlib import Path

import yaml

from px0 import harness, paths, tools
from px0 import workflow as workflow_mod

_JSON_OBJECT_RE = re.compile(r"\{.*\}", re.DOTALL)


class BuilderError(Exception):
    pass


@dataclass
class Plan:
    trigger: dict
    inputs: list[dict]
    tools: list[str]
    output: dict
    body: str
    description: str = ""
    raw: dict = field(default_factory=dict)


def generate_plan(config: dict, description: str) -> Plan:
    tool_ids = [t.id for t in tools.list_tools()]
    prompt = (
        "Turn this request into a JSON workflow plan for a personal automation "
        "tool. Respond with ONLY a JSON object with keys: "
        '"trigger" ({"manual": bool, "schedule": five-field cron or null}), '
        '"inputs" (list of {"id", "tool", "args"} using only tools from the '
        "list below, read-only), "
        '"tools" (list of tool ids the model may call while generating, for '
        "actions like posting -- omit unless the request asks to post/send/"
        "comment), "
        '"output" ({"target": "stdout"|"file", "path": templated path if file}), '
        '"body" (the instruction text the model receives at run time), '
        '"description" (one line).\n\n'
        f"Available tools: {tool_ids}\n\n"
        f"Request: {description}"
    )
    raw = harness.invoke(config, prompt, timeout=90)
    match = _JSON_OBJECT_RE.search(raw)
    if not match:
        raise BuilderError(f"the harness did not return a JSON plan:\n{raw[:500]}")
    try:
        data = json.loads(match.group(0))
    except json.JSONDecodeError as e:
        raise BuilderError(f"the harness returned malformed JSON: {e}")

    return Plan(
        trigger=data.get("trigger", {"manual": True}),
        inputs=data.get("inputs", []),
        tools=data.get("tools", []),
        output=data.get("output", {"target": "stdout"}),
        body=data.get("body", description),
        description=data.get("description", description),
        raw=data,
    )


def check_feasibility(plan: Plan, home: Path) -> list[str]:
    issues = []
    known = [t.id for t in tools.list_tools()]

    def check_tool(tool_id: str, context: str):
        if tool_id in known:
            return
        close = difflib.get_close_matches(tool_id, known, n=1)
        suggestion = f"; closest available: {close[0]}" if close else ""
        issues.append(f"no tool exposes {tool_id!r} ({context}){suggestion}")

    for inp in plan.inputs:
        tool_id = inp.get("tool")
        if not tool_id:
            issues.append(f"input {inp.get('id')!r} has no tool")
            continue
        check_tool(tool_id, f"input {inp.get('id')!r}")
        if tool_id in known and tools.is_write(tool_id):
            issues.append(f"input {inp.get('id')!r} uses write tool {tool_id!r}; "
                           f"inputs must be read-only, move it to tools:")

    for tool_id in plan.tools:
        check_tool(tool_id, "tools[]")

    schedule = plan.trigger.get("schedule")
    if schedule:
        from croniter import croniter
        try:
            croniter(schedule)
        except (ValueError, KeyError) as e:
            issues.append(f"trigger.schedule {schedule!r} invalid: {e}")

    return issues


def required_connections(plan: Plan) -> set[str]:
    providers = set()
    for inp in plan.inputs:
        tool_id = inp.get("tool")
        if tool_id and tool_id in tools.REGISTRY:
            providers.add(tools.REGISTRY[tool_id].provider)
    for tool_id in plan.tools:
        if tool_id in tools.REGISTRY:
            providers.add(tools.REGISTRY[tool_id].provider)
    return providers


def write_tools_named(plan: Plan) -> list[str]:
    return [t for t in plan.tools if t in tools.REGISTRY and tools.is_write(t)]


def choose_guidelines(home: Path, description: str, top_n: int = 3) -> list[str]:
    """Match the task against topic files present in the store by simple
    keyword overlap between the description and each file's headings."""
    words = set(re.findall(r"[a-z]+", description.lower()))
    scored = []
    for path in sorted(paths.guidelines_dir(home).rglob("*.md")):
        rel = str(path.relative_to(paths.guidelines_dir(home)))
        text = path.read_text().lower()
        file_words = set(re.findall(r"[a-z]+", text))
        overlap = len(words & file_words)
        if overlap:
            scored.append((overlap, rel))
    scored.sort(key=lambda x: (-x[0], x[1]))
    return [rel for _, rel in scored[:top_n]]


def render_workflow_file(workflow_id: str, plan: Plan, guidelines: list[str]) -> str:
    front = {
        "id": workflow_id,
        "kind": "workflow",
        "version": 1,
        "description": plan.description,
        "trigger": plan.trigger,
    }
    if guidelines:
        front["guidelines"] = guidelines
    if plan.inputs:
        front["inputs"] = plan.inputs
    if plan.tools:
        front["tools"] = plan.tools
    front["output"] = plan.output
    front["timeout"] = "120s"

    front_yaml = yaml.safe_dump(front, sort_keys=False).strip()
    return f"---\n{front_yaml}\n---\n{plan.body.strip()}\n"


def save_workflow(home: Path, workflow_id: str, content: str) -> Path:
    from px0 import versioning

    dest = paths.workflows_dir(home) / f"{workflow_id}.md"
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(content)
    versioning.record_change(
        home, "builder", [versioning.FileChange(str(dest.relative_to(home)), content.encode())]
    )
    return dest
