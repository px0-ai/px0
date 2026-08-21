"""Workflow file model: YAML frontmatter as the machine contract, the
Markdown body as the prompt the model receives."""

from dataclasses import dataclass, field
from pathlib import Path

import yaml
from croniter import croniter

from px0 import paths, tools


# A retry policy exists to survive a blip, not to hammer a broken system all
# night; the daemon would otherwise be stuck on one workflow.
MAX_ATTEMPTS = 10

# The floor on how often a watch may poll. Anything faster burns API quota and
# model calls for a job that, by definition, is waiting for something.
MIN_WATCH_SECONDS = 60


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
    # The sentence the user typed into `px0 workflows new`, kept verbatim so
    # `px0 workflows edit` can show it back to them. `description` is the
    # model's normalized restatement, which is not what they wrote.
    request: str = ""
    trigger: dict = field(default_factory=dict)
    # False parks a workflow: it stays in the store, keeps its history, and the
    # daemon skips it. `px0 workflows disable` sets this rather than making you
    # delete trigger.schedule and remember it later.
    enabled: bool = True
    # What happens when a run fails: {"notify": "desktop"|"tool"|"none",
    # "channel": <tool id>, "target": <where>}. Overrides the notify.* config.
    on_failure: dict = field(default_factory=dict)
    # {"max_attempts": N, "backoff_seconds": S}: how many times a failed run is
    # retried before it is recorded as failed. Overrides runs.* config.
    retry: dict = field(default_factory=dict)
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


def _yaml_problem(e: Exception) -> str:
    """Condenses a yaml error into one line naming the problem and its line.

    yaml's own str() is a multi-line block that reports the position as
    "<unicode string>" -- useless in a list of workflows, and it buries the one
    detail that matters. Frontmatter line numbers are offset by one for the
    opening `---`.
    """
    problem = getattr(e, "problem", None)
    mark = getattr(e, "problem_mark", None)
    if problem and mark is not None:
        return f"invalid frontmatter, line {mark.line + 1}: {problem}"
    if problem:
        return f"invalid frontmatter: {problem}"
    return f"invalid frontmatter: {str(e).splitlines()[0]}"


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
    try:
        front = yaml.safe_load(parts[1]) or {}
    except yaml.YAMLError as e:
        raise WorkflowError(f"{path.name}: {_yaml_problem(e)}") from e
    if not isinstance(front, dict):
        raise WorkflowError(f"{path}: frontmatter is not a mapping")
    body = parts[2].lstrip("\n")

    raw_inputs = front.get("inputs") or []
    try:
        inputs = [InputSpec(**i) for i in raw_inputs]
    except TypeError as e:
        raise WorkflowError(f"{path}: bad inputs entry: {e}") from e

    return Workflow(
        id=front.get("id", path.stem),
        path=path,
        version=front.get("version", 1),
        description=front.get("description", ""),
        request=front.get("request", ""),
        trigger=front.get("trigger", {}) or {},
        enabled=front.get("enabled", True),
        on_failure=front.get("on_failure", {}) or {},
        retry=front.get("retry", {}) or {},
        guidelines=front.get("guidelines", []) or [],
        inputs=inputs,
        tools=front.get("tools", []) or [],
        output=front.get("output", {}),
        timeout=front.get("timeout", "120s"),
        pipeline=front.get("pipeline"),
        body=body,
    )


def load_all(home: Path, strict: bool = False) -> dict[str, Workflow]:
    """Loads every workflow file (*.md, recursively) under the store's
    workflows directory, keyed by workflow id. Returns an empty dict if the
    workflows directory doesn't exist. A duplicate id overwrites the
    previously loaded workflow with that id.

    Unparseable files are skipped rather than raised, so one bad file cannot
    hide every other workflow -- `load_errors` reports them, and `strict=True`
    restores the raising behaviour for callers that want it. Without this, a
    single YAML typo took down `workflows list`, `doctor`, and the daemon.
    """
    base = paths.workflows_dir(home)
    result = {}
    if not base.exists():
        return result
    for p in sorted(base.rglob("*.md")):
        try:
            wf = parse(p)
        except WorkflowError:
            if strict:
                raise
            continue
        result[wf.id] = wf
    return result


def load_errors(home: Path) -> list[str]:
    """Returns one message per workflow file that failed to parse, so callers
    can surface broken files that `load_all` skipped."""
    base = paths.workflows_dir(home)
    if not base.exists():
        return []
    errors = []
    for p in sorted(base.rglob("*.md")):
        try:
            parse(p)
        except WorkflowError as e:
            errors.append(str(e))
    return errors


def load(home: Path, workflow_id: str) -> Workflow:
    """Loads a single workflow by id. Raises WorkflowError if no workflow
    with that id exists."""
    for wf_id, wf in load_all(home).items():
        if wf_id == workflow_id:
            return wf
    # Distinguish "not there" from "there but unparseable": the file whose stem
    # matches gets its own parse error reported instead of a misleading absence.
    candidate = paths.workflows_dir(home) / f"{workflow_id}.md"
    if candidate.exists():
        parse(candidate)  # raises the real WorkflowError
    raise WorkflowError(f"no such workflow: {workflow_id}")


def _validate_on_failure(wf: "Workflow") -> list[str]:
    """Checks an on_failure block: a known channel, and a tool that can carry a message."""
    from px0 import notify

    spec = wf.on_failure
    if not spec:
        return []
    if not isinstance(spec, dict):
        return ["on_failure must be a mapping, e.g. {notify: desktop}"]
    errors = []
    channel = str(spec.get("notify", "") or "").strip()
    if channel and channel not in notify.CHANNELS:
        errors.append(f"on_failure.notify must be one of {list(notify.CHANNELS)}, got {channel!r}")
    tool_id = str(spec.get("channel", "") or "").strip()
    if tool_id and tool_id not in notify.MESSAGE_TOOLS:
        allowed = ", ".join(sorted(notify.MESSAGE_TOOLS))
        errors.append(f"on_failure.channel {tool_id!r} cannot carry a message; use one of {allowed}")
    if channel == "tool" and not tool_id:
        errors.append("on_failure.notify is 'tool' but on_failure.channel names no tool")
    if channel == "tool" and tool_id and not str(spec.get("target", "") or "").strip():
        errors.append("on_failure.notify is 'tool' but on_failure.target is empty")
    return errors


def _validate_retry(wf: "Workflow") -> list[str]:
    """Checks a retry block: positive whole numbers, and a sane ceiling."""
    spec = wf.retry
    if not spec:
        return []
    if not isinstance(spec, dict):
        return ["retry must be a mapping, e.g. {max_attempts: 3}"]
    errors = []
    attempts = spec.get("max_attempts")
    if attempts is not None:
        if not isinstance(attempts, int) or isinstance(attempts, bool) or attempts < 1:
            errors.append(f"retry.max_attempts must be a whole number >= 1, got {attempts!r}")
        elif attempts > MAX_ATTEMPTS:
            errors.append(f"retry.max_attempts is capped at {MAX_ATTEMPTS}, got {attempts}")
    backoff = spec.get("backoff_seconds")
    if backoff is not None and (not isinstance(backoff, (int, float))
                                 or isinstance(backoff, bool) or backoff < 0):
        errors.append(f"retry.backoff_seconds must be a number >= 0, got {backoff!r}")
    return errors


def _validate_watch(wf: "Workflow", home: Path) -> list[str]:
    """Checks a trigger.watch block: a read-only tool, and a field to dedupe on.

    A watch is how a workflow fires on something happening rather than on the
    clock. The daemon polls the named tool and runs the workflow when an item
    it has not seen before shows up.
    """
    spec = wf.trigger.get("watch")
    if not spec:
        return []
    if not isinstance(spec, dict):
        return ["trigger.watch must be a mapping with a tool: and a key:"]
    errors = []
    tool_id = spec.get("tool")
    if not tool_id:
        errors.append("trigger.watch needs a tool: to poll")
    elif not tools.exists(tool_id, home):
        errors.append(f"trigger.watch references unknown tool: {tool_id}")
    elif tools.is_write(tool_id, home):
        errors.append(f"trigger.watch tool {tool_id!r} is a write tool; a watch only reads")
    if spec.get("args") is not None and not isinstance(spec["args"], dict):
        errors.append("trigger.watch.args must be a mapping")
    every = spec.get("every")
    if every is not None:
        try:
            from px0 import harness
            if harness.parse_duration(str(every)) < MIN_WATCH_SECONDS:
                errors.append(f"trigger.watch.every must be at least {MIN_WATCH_SECONDS}s")
        except ValueError:
            errors.append(f"trigger.watch.every {every!r} is not a duration like '15m'")
    if wf.output.get("target") not in (None, "file"):
        errors.append("a watched workflow's output.target must be 'file'")
    return errors


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
            # `home` lets tools discovered by `px0 workflows new` resolve, not just curated ones
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

    if not isinstance(wf.enabled, bool):
        errors.append(f"enabled must be true or false, got {wf.enabled!r}")

    errors.extend(_validate_on_failure(wf))
    errors.extend(_validate_retry(wf))

    schedule = wf.trigger.get("schedule")
    if schedule:
        try:
            croniter(schedule)
        except (ValueError, KeyError) as e:
            errors.append(f"trigger.schedule {schedule!r} is not a valid cron expression: {e}")
        if wf.output.get("target") not in (None, "file"):
            errors.append("a scheduled workflow's output.target must be 'file'")

    errors.extend(_validate_watch(wf, home))

    target = wf.output.get("target")
    if target and target not in ("stdout", "file"):
        errors.append(f"output.target must be 'stdout' or 'file', got {target!r}")
    if target == "file" and not wf.output.get("path"):
        errors.append("output.target 'file' requires output.path")

    return errors


def retry_policy(wf: Workflow, config: dict) -> tuple[int, float]:
    """(max_attempts, backoff_seconds) for a workflow, its own block winning over config."""
    from px0 import config as config_mod

    attempts = wf.retry.get("max_attempts")
    if not isinstance(attempts, int) or isinstance(attempts, bool) or attempts < 1:
        attempts = config_mod.get(config, "runs.max_attempts", 1)
    try:
        attempts = max(1, min(int(attempts), MAX_ATTEMPTS))
    except (TypeError, ValueError):
        attempts = 1
    backoff = wf.retry.get("backoff_seconds")
    if not isinstance(backoff, (int, float)) or isinstance(backoff, bool) or backoff < 0:
        backoff = config_mod.get(config, "runs.retry_backoff_seconds", 30)
    try:
        backoff = max(0.0, float(backoff))
    except (TypeError, ValueError):
        backoff = 0.0
    return attempts, backoff


def watch_spec(wf: Workflow) -> dict | None:
    """A workflow's trigger.watch block, or None. Normalizes `every` to seconds."""
    spec = wf.trigger.get("watch")
    if not isinstance(spec, dict) or not spec.get("tool"):
        return None
    from px0 import harness

    every = spec.get("every", "15m")
    try:
        seconds = max(MIN_WATCH_SECONDS, harness.parse_duration(str(every)))
    except ValueError:
        seconds = 900.0
    return {"tool": spec["tool"], "args": spec.get("args") or {},
            "key": spec.get("key"), "every_seconds": seconds}
