"""Workflow file model: YAML frontmatter as the machine contract, the
Markdown body as the prompt the model receives."""

from dataclasses import dataclass, field
from pathlib import Path

import yaml
from croniter import croniter

from px0 import paths, tools


class WorkflowError(Exception):
    """Raised when a workflow file fails to parse or fails validation."""
    pass


@dataclass
class InputSpec:
    """One entry in a workflow's `inputs` list: a tool call, retrieval query,
    static source, or sub-workflow used to gather context before the main
    prompt runs."""
    id: str
    tool: str | None = None
    args: dict = field(default_factory=dict)
    retrieve: dict | None = None
    source: str | None = None
    workflow: str | None = None
    optional: bool = False

    @property
    def kind(self) -> str:
        """Returns which of tool/retrieve/source/workflow this input is,
        inferred from which field is set. Raises WorkflowError if none are
        set."""
        if self.tool:
            return "tool"
        if self.retrieve is not None:
            return "retrieve"
        if self.source:
            return "source"
        if self.workflow:
            return "workflow"
        raise WorkflowError(f"input {self.id!r} has no tool/retrieve/source/workflow")


@dataclass
class Workflow:
    """Parsed representation of a workflow file: YAML frontmatter fields
    plus the Markdown body (the prompt)."""
    id: str
    path: Path
    version: int = 1
    description: str = ""
    trigger: dict = field(default_factory=dict)
    guidelines: list[str] = field(default_factory=list)
    inputs: list[InputSpec] = field(default_factory=list)
    tools: list[str] = field(default_factory=list)
    output: dict = field(default_factory=dict)
    timeout: str = "120s"
    pipeline: list[str] | None = None
    body: str = ""

    @property
    def rel_path(self) -> str | None:
        """Placeholder for a path relative to the store home; always None
        here and filled in by the caller when needed."""
        return None  # set by caller relative to home when needed


def parse(path: Path) -> Workflow:
    """Parses a workflow file into a Workflow, splitting YAML frontmatter
    from the Markdown body. Raises WorkflowError if the file has no
    frontmatter delimiters or the frontmatter section is malformed.
    Missing frontmatter keys fall back to their dataclass defaults."""
    text = path.read_text()
    if not text.startswith("---"):
        raise WorkflowError(f"{path}: missing frontmatter")
    parts = text.split("---", 2)  # ["", frontmatter, body]
    if len(parts) < 3:
        raise WorkflowError(f"{path}: malformed frontmatter")
    front = yaml.safe_load(parts[1]) or {}
    body = parts[2].lstrip("\n")

    inputs = [InputSpec(**i) for i in front.get("inputs", [])]

    return Workflow(
        id=front.get("id", path.stem),
        path=path,
        version=front.get("version", 1),
        description=front.get("description", ""),
        trigger=front.get("trigger", {}),
        guidelines=front.get("guidelines", []) or [],
        inputs=inputs,
        tools=front.get("tools", []) or [],
        output=front.get("output", {}),
        timeout=front.get("timeout", "120s"),
        pipeline=front.get("pipeline"),
        body=body,
    )


def load_all(home: Path) -> dict[str, Workflow]:
    """Loads every workflow file (*.md, recursively) under the store's
    workflows directory, keyed by workflow id. Returns an empty dict if the
    workflows directory doesn't exist. A duplicate id overwrites the
    previously loaded workflow with that id."""
    base = paths.workflows_dir(home)
    result = {}
    if not base.exists():
        return result
    for p in sorted(base.rglob("*.md")):
        wf = parse(p)
        result[wf.id] = wf
    return result


def load(home: Path, workflow_id: str) -> Workflow:
    """Loads a single workflow by id. Raises WorkflowError if no workflow
    with that id exists."""
    for wf_id, wf in load_all(home).items():
        if wf_id == workflow_id:
            return wf
    raise WorkflowError(f"no such workflow: {workflow_id}")


def validate(wf: Workflow, home: Path) -> list[str]:
    """Validates a parsed workflow's cross-references and structural
    constraints -- guideline files, tool references, pipeline stages, cron
    schedule syntax, and output target -- returning a list of
    human-readable error strings. An empty list means the workflow is
    valid."""
    errors: list[str] = []

    for g in wf.guidelines:
        if not (paths.guidelines_dir(home) / g).exists():
            errors.append(f"guidelines[] references missing file: {g}")

    for inp in wf.inputs:
        if inp.kind == "tool":
            # `home` lets tools discovered by `px0 new` resolve, not just curated ones
            if not tools.exists(inp.tool, home):
                errors.append(f"input {inp.id!r} references unknown tool: {inp.tool}")
            elif tools.is_write(inp.tool, home):
                errors.append(f"input {inp.id!r} tool {inp.tool!r} is a write tool; "
                               f"inputs must be read-only, use tools: instead")

    for t in wf.tools:
        if not tools.exists(t, home):
            errors.append(f"tools[] references unknown tool: {t}")

    if wf.pipeline:
        all_wf = load_all(home)
        for stage in wf.pipeline:
            if stage not in all_wf:
                errors.append(f"pipeline[] references unknown workflow: {stage}")
            elif all_wf[stage].pipeline:
                errors.append(f"pipeline[] stage {stage!r} is itself a pipeline; "
                               f"pipelines cannot nest")

    schedule = wf.trigger.get("schedule")
    if schedule:
        try:
            croniter(schedule)
        except (ValueError, KeyError) as e:
            errors.append(f"trigger.schedule {schedule!r} is not a valid cron expression: {e}")
        if wf.output.get("target") not in (None, "file"):
            errors.append("a scheduled workflow's output.target must be 'file'")

    target = wf.output.get("target")
    if target and target not in ("stdout", "file"):
        errors.append(f"output.target must be 'stdout' or 'file', got {target!r}")
    if target == "file" and not wf.output.get("path"):
        errors.append("output.target 'file' requires output.path")

    return errors
