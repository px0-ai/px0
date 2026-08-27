"""Workflow file model: YAML frontmatter as the machine contract, the
Markdown body as the prompt the model receives."""

import re
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

# An argument the plan left for a human to fill in: `<OWNER>`, `<REPO>`,
# `<YYYY-MM-01T00:00:00Z>`. Matched only when it is the entire value, so Slack's
# own `<@U123>` and `<https://url|text>` syntax inside a longer string is left
# alone -- angle brackets are only a placeholder when they are the whole answer.
_PLACEHOLDER_ARG_RE = re.compile(r"^<[^<>]+>$")

# A `{{name}}` reference inside an argument, matched the way the runner matches
# it, so what validation accepts and what a run can resolve cannot drift.
_ARG_TEMPLATE_RE = re.compile(r"\{\{\s*([\w.\-]+)\s*\}\}")

# What an argument's template may reference: the store's config, the values
# passed with `--input`, and any input resolved before this one. The runner
# builds its context from exactly these, so anything else resolves to None and
# is sent to the connector as a missing value.
_ARG_TEMPLATE_ROOTS = frozenset({"config", "input"})

# The clock placeholders a workflow may use, on top of the roots above. Every
# "what happened since yesterday" workflow needs one, and a scheduled run cannot
# be handed a literal timestamp -- so the grammar is fixed here, as part of what
# a workflow file may say, and `runner` turns each name into a value.
#
#   {{now}}       this instant
#   {{today}}     today's date, YYYY-MM-DD (also `{{date}}`)
#   {{datetime}}  this instant, always filename-safe
#   {{time}}      the time of day
#   {{now-24h}}   24 hours ago -- units m, h, d, w
#
# One vocabulary for arguments and for `output.path` alike. They used to be two:
# arguments took `now`/`today`, paths took only `date`/`datetime`/`time`, and a
# plan that wrote `logs/daily-{{today}}.md` was accepted everywhere except the
# one place it was used. Formatting still differs by context -- a path gets a
# filename-safe form, an argument gets ISO 8601 -- but what you may *name* does
# not.
TIME_PLACEHOLDER_NAMES = ("now", "today", "date", "datetime", "time")
TIME_PLACEHOLDER_RE = re.compile(
    r"^(?:" + "|".join(TIME_PLACEHOLDER_NAMES) + r"|now-\d+[mhdw])$")

# A `{{name}}` or `{name}` reference in an output path. Both styles, because a
# plan that learned `{{input_id}}` from the body carries the habit into the path,
# and a file literally named `report-{2026-08-17}.md` is nobody's intent.
_OUTPUT_TEMPLATE_RE = re.compile(r"\{\{?\s*([^{}]*?)\s*\}?\}")


def is_time_placeholder(name: str) -> bool:
    """Whether `name` is one of the clock placeholders the runner resolves."""
    return bool(TIME_PLACEHOLDER_RE.match(name))


def supported_placeholders() -> str:
    """The clock vocabulary, for an error message that says what *is* allowed."""
    return ", ".join(["{" + n + "}" for n in TIME_PLACEHOLDER_NAMES] + ["{now-<N><m|h|d|w>}"])


def output_path_errors(path: str) -> list[str]:
    """Reports placeholders in an `output.path` that no run can resolve.

    Checked here, at validation time, because the path is only rendered once the
    run is routing its output -- after the model call. A typo in a filename used
    to be discovered at the most expensive possible moment, and reported as a
    failed run rather than as a workflow that could never have succeeded.
    """
    unknown = sorted({name for name in _OUTPUT_TEMPLATE_RE.findall(path)
                      if not is_time_placeholder(name)})
    if not unknown:
        return []
    return [f"output.path has unknown placeholder(s): {', '.join(unknown)} "
            f"-- supported: {supported_placeholders()}"]


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
    # True, False, or a list of tool ids: which of this workflow's write tools
    # must wait for a person before they fire. None means "whatever
    # tools.confirm_writes says", so a store-wide default still reaches the
    # workflows that never mention it.
    confirm: bool | list | None = None
    # Whether a run keeps what its inputs resolved to, so a later revision can
    # be compared against the same world. None follows `runs.capture_inputs`.
    capture: bool | None = None
    guidelines: list[str] = field(default_factory=list)
    # Declared run-time values: one mapping per var, each with a name, a
    # description, and the values somebody else might plausibly put there. A
    # workflow that has them is a template -- the literals belonging to one
    # installation have been lifted out of it. See `px0/templates.py`.
    vars: list[dict] = field(default_factory=list)
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
    return parse_text(path.read_text(), path)


def parse_text(text: str, path: Path) -> Workflow:
    """`parse` over text that is not on disk yet.

    Split out so a rewrite can be validated before it is written: `px0
    workflows templatize` builds a new file in memory and has to know it still
    parses and still validates before it replaces the one that worked. `path`
    is carried for the error messages and for the Workflow's own field.
    """
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
        confirm=front.get("confirm"),
        capture=front.get("capture"),
        guidelines=front.get("guidelines", []) or [],
        vars=front.get("vars", []) or [],
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


# What a pipeline stage may be conditional on. Kept to two, because both are
# facts about the previous stage's output that px0 can check itself -- anything
# richer would be a language, and a workflow body is where judgement belongs.
PIPELINE_CONDITIONS = ("always", "has_output", "no_output")


def pipeline_stages(wf: "Workflow") -> list[dict]:
    """A pipeline's stages, normalized to `{workflow, when}`.

    Accepts both shapes a pipeline can be written in: a plain list of ids, and
    a list of mappings that say when each stage should run. The plain form is
    every existing pipeline in every store, so it stays the default reading and
    means `when: always`.
    """
    stages = []
    for entry in wf.pipeline or []:
        if isinstance(entry, str):
            stages.append({"workflow": entry, "when": "always"})
        elif isinstance(entry, dict):
            stages.append({
                "workflow": str(entry.get("workflow") or entry.get("id") or ""),
                "when": str(entry.get("when") or "always"),
            })
    return stages


def _validate_pipeline(wf: "Workflow", home: Path) -> list[str]:
    """Checks a pipeline's stages exist and their conditions are ones px0 knows."""
    if not wf.pipeline:
        return []
    errors = []
    known = load_all(home)
    for i, stage in enumerate(pipeline_stages(wf)):
        if not stage["workflow"]:
            errors.append(f"pipeline[{i}] names no workflow")
            continue
        if stage["workflow"] not in known:
            errors.append(f"pipeline[{i}] references unknown workflow: {stage['workflow']}")
        elif known[stage["workflow"]].pipeline:
            errors.append(f"pipeline[{i}] stage {stage['workflow']!r} is itself a "
                          "pipeline; pipelines cannot nest")
        if stage["when"] not in PIPELINE_CONDITIONS:
            errors.append(
                f"pipeline[{i}].when must be one of {', '.join(PIPELINE_CONDITIONS)}, "
                f"got {stage['when']!r}")
        if i == 0 and stage["when"] != "always":
            # There is no previous output for the first stage to be conditional
            # on, so a condition there can only ever mean one thing and is
            # almost certainly a mistake about which stage it belongs to.
            errors.append("pipeline[0].when has no previous stage to test; "
                          "put the condition on the stage that depends on it")
    return errors


def _validate_timezone(wf: "Workflow") -> list[str]:
    """Checks `trigger.timezone` names a zone this machine knows.

    Refused rather than ignored: a misspelled zone that silently fell back to
    machine local time would look like it worked and fire at the wrong hour,
    which is the exact failure the setting exists to prevent.
    """
    name = (wf.trigger or {}).get("timezone")
    if name is None:
        return []
    if not isinstance(name, str) or not name.strip():
        return ["trigger.timezone must be a zone name like 'Asia/Kolkata'"]
    try:
        from zoneinfo import ZoneInfo

        ZoneInfo(name)
    except Exception:
        return [f"trigger.timezone {name!r} is not a zone this machine knows"]
    return []


def _validate_confirm(wf: "Workflow", home: Path) -> list[str]:
    """Checks a `confirm:` block: a bool, or a list of this workflow's own tools.

    A named tool that the workflow cannot call is refused rather than ignored,
    because the failure is silent and dangerous in one direction: a user who
    misspells the tool they wanted held back gets a workflow that sends it
    without asking, and nothing anywhere says so.
    """
    if wf.confirm is None or isinstance(wf.confirm, bool):
        return []
    if not isinstance(wf.confirm, list):
        return ["confirm: must be true, false, or a list of tool ids"]
    errors = []
    for tool_id in wf.confirm:
        if not isinstance(tool_id, str):
            errors.append(f"confirm[] entries must be tool ids, got {tool_id!r}")
        elif tool_id not in wf.tools:
            errors.append(
                f"confirm[] names {tool_id!r}, which is not in this workflow's tools")
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
    minimum = spec.get("min_items")
    if minimum is not None:
        try:
            if int(minimum) < 1:
                errors.append("trigger.watch.min_items must be at least 1")
        except (TypeError, ValueError):
            errors.append(f"trigger.watch.min_items must be a number, got {minimum!r}")
    every = spec.get("every")
    if every is not None:
        try:
            from px0 import harness
            if harness.parse_duration(str(every)) < MIN_WATCH_SECONDS:
                errors.append(f"trigger.watch.every must be at least {MIN_WATCH_SECONDS}s")
        except ValueError:
            errors.append(f"trigger.watch.every {every!r} is not a duration like '15m'")
    if wf.output.get("target") not in (None, "file", "inbox"):
        # The rule exists because nobody is watching stdout when a poll fires.
        # An inbox delivery answers that as well as a file does.
        errors.append("a watched workflow's output.target must be 'file' or 'inbox'")
    return errors


def walk_strings(value, trail: str = ""):
    """Yields every (dotted location, string) pair inside a nested args value, so
    a placeholder buried in a list or a sub-object is found too.

    Public because `templates` walks the same values looking for literals to
    lift out, and two walkers over one structure would drift.
    """
    if isinstance(value, str):
        yield trail, value
    elif isinstance(value, dict):
        for k, v in value.items():
            yield from walk_strings(v, f"{trail}.{k}" if trail else str(k))
    elif isinstance(value, list):
        for i, v in enumerate(value):
            yield from walk_strings(v, f"{trail}[{i}]")


def input_arg_errors(inputs: list[dict], fix: str) -> list[str]:
    """Reports arguments a run cannot turn into a real value.

    Two ways a plan leaves one behind, and both used to reach the connector as
    written: a literal fill-me placeholder (`owner: <OWNER>`), and a template
    referencing something nothing provides (`author: {{github_username}}`), which
    the runner resolves to None. Both produced an error from the far end of the
    API describing a request nobody meant to make -- a `<OWNER>/<REPO>` fetch
    comes back as GitHub's 404 "Not Found", which reads as a missing repository
    rather than as an unfinished workflow.

    Takes plain dicts (`{"id", "args", "retrieve"}`) so the same rule can run
    over a plan the build has not saved yet and over a workflow on disk; `fix`
    is the caller's advice, since the build can regenerate and a run cannot.
    """
    errors: list[str] = []
    resolved = set(_ARG_TEMPLATE_ROOTS)

    for inp in inputs:
        input_id = inp.get("id")
        for field_name in ("args", "retrieve"):
            value = inp.get(field_name)
            if not value:
                continue
            for where, text in walk_strings(value):
                location = f"{field_name}.{where}" if where else field_name
                if _PLACEHOLDER_ARG_RE.match(text.strip()):
                    errors.append(
                        f"input {input_id!r} {location} is still the placeholder "
                        f"{text.strip()!r} -- {fix}"
                    )
                    continue
                for name in _ARG_TEMPLATE_RE.findall(text):
                    root = name.split(".")[0]
                    if root in resolved or is_time_placeholder(name):
                        continue
                    errors.append(
                        f"input {input_id!r} {location} references {{{{{name}}}}}, "
                        f"which nothing provides -- write the value into the file, "
                        f"or pass it at run time as `{{{{input.{root}}}}}` with "
                        f"`--input {root}=<value>`"
                    )
        # Only inputs *above* this one are resolved when its args are rendered.
        resolved.add(input_id)

    return errors


def _validate_input_args(wf: Workflow) -> list[str]:
    """`input_arg_errors` over a parsed workflow.

    Caught by `validate`, which runs before a run touches the network, so an
    unfinished workflow is named as one instead of a connector being asked for a
    repository called `<REPO>`.
    """
    return input_arg_errors(
        [{"id": inp.id, "args": inp.args, "retrieve": inp.retrieve} for inp in wf.inputs],
        fix=f"rebuild the workflow with `px0 workflows edit {wf.id}`, naming the real value",
    )


# A var name, as it is typed at the command line: `--input repo=owner/name`.
# Dots are excluded because `{{input.a.b}}` reads as a nested lookup, and a var
# called `a.b` would be a name that can never resolve.
_VAR_NAME_RE = re.compile(r"^[A-Za-z][A-Za-z0-9_-]*$")


def is_template(wf: Workflow) -> bool:
    """Whether this workflow declares vars, and so has to be filled in to run."""
    return bool(declared_vars(wf))


def declared_vars(wf: Workflow) -> list[dict]:
    """A workflow's `vars:` block, normalized to
    `{name, description, values, default, required}`.

    A var is required unless it carries a `default` or says `required: false`.
    That way round because the common case for a shared template is a value
    only the installer can know -- their repository, their channel -- and the
    safe reading of a value nobody supplied is to refuse the run, not to send
    something somewhere with a blank in it.
    """
    out: list[dict] = []
    for entry in wf.vars or []:
        if not isinstance(entry, dict):
            continue
        name = str(entry.get("name") or "").strip()
        if not name:
            continue
        default = entry.get("default")
        default = None if default is None else str(default)
        out.append({
            "name": name,
            "description": str(entry.get("description") or "").strip(),
            "values": [str(v) for v in (entry.get("values") or [])],
            "default": default,
            "required": default is None and entry.get("required", True) is not False,
        })
    return out


def _var_reference(name: str) -> re.Pattern:
    """Matches `{{input.<name>}}` the way the runner matches it."""
    return re.compile(r"\{\{\s*input\." + re.escape(name) + r"\s*\}\}")


def references_var(wf: Workflow, name: str) -> bool:
    """Whether anything a run renders actually reads this var.

    Only the two surfaces a run renders count: an input's `args`/`retrieve`, and
    the body. A var mentioned anywhere else -- in the description, in a comment
    -- is a var that will never be substituted into anything.
    """
    pattern = _var_reference(name)
    if pattern.search(wf.body or ""):
        return True
    for inp in wf.inputs:
        for value in (inp.args, inp.retrieve):
            if not value:
                continue
            for _where, text in walk_strings(value):
                if pattern.search(text):
                    return True
    return False


def _validate_vars(wf: Workflow) -> list[str]:
    """Checks a `vars:` block: named, described, unique, and actually used.

    An undescribed var is refused rather than allowed through, because the
    description is the entire point of declaring one. A var is only worth
    having when a stranger can read it and know what to put there; without that
    line, a template is a file that fails with a name they have never seen.

    A var nothing references is refused for the mirror-image reason: it is a
    knob the file advertises and no run reads, so the installer supplies a value
    and watches it change nothing.
    """
    if wf.vars in (None, []):
        return []
    if not isinstance(wf.vars, list):
        return ["vars: must be a list of mappings, each with a name and a description"]

    errors: list[str] = []
    seen: set[str] = set()
    for i, entry in enumerate(wf.vars):
        if not isinstance(entry, dict):
            errors.append(f"vars[{i}] must be a mapping with a name and a description")
            continue
        name = str(entry.get("name") or "").strip()
        if not name:
            errors.append(f"vars[{i}] has no name")
            continue
        if not _VAR_NAME_RE.match(name):
            errors.append(f"vars[{i}] name {name!r} must start with a letter and use "
                          "only letters, digits, underscores, and dashes")
            continue
        if name in seen:
            errors.append(f"vars[] declares {name!r} twice")
            continue
        seen.add(name)
        if not str(entry.get("description") or "").strip():
            errors.append(f"vars[] entry {name!r} has no description; it is what "
                          "tells whoever installs this what to put there")
        values = entry.get("values")
        if values is not None and (not isinstance(values, list)
                                   or any(isinstance(v, (list, dict)) for v in values)):
            errors.append(f"vars[] entry {name!r}: values must be a list of plain values")
        if isinstance(entry.get("default"), (list, dict)):
            errors.append(f"vars[] entry {name!r}: default must be a single value")
        if "required" in entry and not isinstance(entry["required"], bool):
            errors.append(f"vars[] entry {name!r}: required must be true or false")
        if not references_var(wf, name):
            errors.append(f"vars[] declares {name!r}, which nothing in this workflow "
                          f"references -- use it as {{{{input.{name}}}}} in an input's "
                          "args or in the body, or drop it")

    # A schedule and a required var cannot both be true. Nothing supplies
    # `--input` to a fire the daemon starts, so such a workflow does not run
    # badly, it fails every time -- and it fails at 6am, unattended, having
    # looked valid when it was written. Refused here instead, where the person
    # who can fix it is reading.
    unattended = "scheduled" if (wf.trigger or {}).get("schedule") else (
        "watched" if (wf.trigger or {}).get("watch") else "")
    if unattended:
        required = [v["name"] for v in declared_vars(wf) if v["required"]]
        if required:
            errors.append(
                f"this workflow is {unattended}, so nothing can pass it "
                f"--input, but vars[] requires {', '.join(required)} -- give each "
                "one a default, or keep the template as a separate workflow "
                "(`px0 workflows templatize <id> --to <id>-template`)")
    return errors


def var_values(wf: Workflow, cli_inputs: dict) -> tuple[dict, list[str]]:
    """(defaults to contribute, required vars nobody supplied) for a run.

    An empty string counts as not supplied. `--input channel=` is a mistake
    every time -- nothing useful is ever named by the empty string -- and the
    alternative is a connector being asked to post to a channel called nothing.
    """
    filled: dict = {}
    missing: list[str] = []
    seen: set[str] = set()
    for spec in declared_vars(wf):
        if spec["name"] in seen:
            continue  # a duplicate is refused by validation; do not repeat it here
        seen.add(spec["name"])
        supplied = cli_inputs.get(spec["name"])
        if isinstance(supplied, str) and not supplied.strip():
            supplied = None
        if supplied is not None:
            continue
        if spec["default"] is not None:
            filled[spec["name"]] = spec["default"]
        elif spec["required"]:
            missing.append(spec["name"])
        else:
            filled[spec["name"]] = ""
    return filled, missing


def missing_vars_message(wf: Workflow, missing: list[str]) -> str:
    """One line naming what a template still needs, and how to pass it."""
    flags = " ".join(f"--input {name}=<value>" for name in missing)
    noun = "a value" if len(missing) == 1 else "values"
    return (f"{wf.id} needs {noun} for {', '.join(missing)} -- "
            f"run it as `px0 workflows run {wf.id} {flags}`")


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

    if not isinstance(wf.enabled, bool):
        errors.append(f"enabled must be true or false, got {wf.enabled!r}")

    errors.extend(_validate_input_args(wf))
    if wf.output.get("path"):
        errors.extend(output_path_errors(wf.output["path"]))
    errors.extend(_validate_on_failure(wf))
    errors.extend(_validate_retry(wf))

    schedule = wf.trigger.get("schedule")
    if schedule:
        try:
            croniter(schedule)
        except (ValueError, KeyError) as e:
            errors.append(f"trigger.schedule {schedule!r} is not a valid cron expression: {e}")
        if wf.output.get("target") not in (None, "file", "inbox"):
            # The rule exists because nobody is watching stdout at 6am. An
            # inbox delivery answers that as well as a file does -- better,
            # since it also says the output arrived.
            errors.append(
                "a scheduled workflow's output.target must be 'file' or 'inbox'")

    errors.extend(_validate_watch(wf, home))
    errors.extend(_validate_confirm(wf, home))
    errors.extend(_validate_vars(wf))
    errors.extend(_validate_timezone(wf))
    errors.extend(_validate_pipeline(wf, home))
    if wf.capture is not None and not isinstance(wf.capture, bool):
        errors.append("capture: must be true or false")

    target = wf.output.get("target")
    if target and target not in ("stdout", "file", "inbox"):
        errors.append(
            f"output.target must be 'stdout', 'file', or 'inbox', got {target!r}")
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
    try:
        minimum = max(1, int(spec.get("min_items") or 1))
    except (TypeError, ValueError):
        minimum = 1
    return {"tool": spec["tool"], "args": spec.get("args") or {},
            "key": spec.get("key"), "every_seconds": seconds,
            "min_items": minimum}
