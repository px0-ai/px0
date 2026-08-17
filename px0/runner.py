"""px0 run: the eight-stage execution pipeline.

Tool-calling mid-run is a simplified stand-in for the spec's per-run MCP
endpoint: the model is told which tools it may call and asked to emit a
`TOOL_CALL: {...}` line to request one, since the harness invoked here is a
plain non-interactive subprocess rather than something wired to a real MCP
transport. Every call is still bound by the workflow's allowlist and
recorded in the run record exactly as the spec asks.
"""

import fcntl
import json
import re
import time
from dataclasses import dataclass
from datetime import date, datetime, timezone
from pathlib import Path
from typing import Any

from px0 import claims, config as config_mod, harness, paths, retrieval
from px0 import runs as runs_mod
from px0 import tools, versioning
from px0 import workflow as workflow_mod

MAX_TOOL_TURNS = 5
_TOOL_CALL_RE = re.compile(r"TOOL_CALL:\s*(\{.*\})", re.DOTALL)
_TEMPLATE_RE = re.compile(r"\{\{\s*([\w.\-]+)\s*\}\}")


class RunError(Exception):
    def __init__(self, message: str, record: dict | None = None):
        super().__init__(message)
        self.record = record or {}


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _lookup(context: dict, dotted: str) -> Any:
    node: Any = context
    for part in dotted.split("."):
        if isinstance(node, dict) and part in node:
            node = node[part]
        else:
            return None
    return node


def render_value(value: Any, context: dict) -> Any:
    if isinstance(value, str):
        stripped = value.strip()
        whole = re.fullmatch(r"\{\{\s*([\w.\-]+)\s*\}\}", stripped)
        if whole:
            return _lookup(context, whole.group(1))

        def sub(m: re.Match) -> str:
            v = _lookup(context, m.group(1))
            return m.group(0) if v is None else str(v)

        return _TEMPLATE_RE.sub(sub, value)
    if isinstance(value, list):
        return [render_value(v, context) for v in value]
    if isinstance(value, dict):
        return {k: render_value(v, context) for k, v in value.items()}
    return value


def _with_retry(config: dict, fn, *args, **kwargs):
    retries = config_mod.get(config, "connectors.retries", 3)
    delay = 1.0
    last_err = None
    for attempt in range(retries + 1):
        try:
            return fn(*args, **kwargs)
        except tools.ConnectorNotConfigured:
            raise
        except tools.ConnectorError as e:
            last_err = e
            if attempt < retries:
                time.sleep(delay)
                delay *= 2
    raise last_err


def resolve_inputs(
    home: Path, config: dict, wf: workflow_mod.Workflow, cli_inputs: dict
) -> tuple[dict, list[dict]]:
    context: dict = {"config": config, "input": cli_inputs}
    meta: list[dict] = []

    for inp in wf.inputs:
        try:
            if inp.kind == "tool":
                args = render_value(inp.args, context)
                value = _with_retry(config, tools.call, home, config, inp.tool, args)
            elif inp.kind == "retrieve":
                spec = render_value(inp.retrieve, context)
                k = spec.get("k", config_mod.get(config, "retrieval.k_default", 5))
                passages = retrieval.retrieve(home, config, spec["query"], k)
                value = "\n\n".join(f"[{p.path}#{p.anchor}] {p.text}" for p in passages)
            elif inp.kind == "source":
                if inp.source != "stdin":
                    raise workflow_mod.WorkflowError(f"unsupported source: {inp.source}")
                value = cli_inputs.get("_stdin", "")
            elif inp.kind == "workflow":
                sub_cli = {"_stdin": cli_inputs.get("_stdin", "")}
                sub_record = run(home, config, inp.workflow, trigger="workflow",
                                  cli_inputs=sub_cli, output_override={"target": "memory"})
                value = sub_record["output"].get("text", "")
            else:
                raise workflow_mod.WorkflowError(f"input {inp.id!r} has no resolvable kind")
        except Exception as e:
            if inp.optional:
                context[inp.id] = None
                meta.append({"id": inp.id, "kind": inp.kind, "ok": False,
                             "error": str(e), "degraded": True})
                continue
            meta.append({"id": inp.id, "kind": inp.kind, "ok": False, "error": str(e)})
            raise RunError(f"required input {inp.id!r} failed: {e}", {"inputs_resolved": meta})
        context[inp.id] = value
        meta.append({"id": inp.id, "kind": inp.kind, "ok": True})

    return context, meta


def render_prompt(wf: workflow_mod.Workflow, guideline_texts: dict[str, str], context: dict) -> str:
    guidelines_block = "\n\n".join(
        f"# {name}\n\n{text}" for name, text in guideline_texts.items()
    )
    body = render_value(wf.body, context)
    if not isinstance(body, str):
        body = str(body)
    if "{{guidelines}}" in wf.body:
        return body.replace("{{guidelines}}", guidelines_block)
    if not guidelines_block:
        return body
    return f"{guidelines_block}\n\n{body}"


def _tool_call_loop(
    home: Path, config: dict, prompt: str, allowed_tools: list[str],
    dry_run: bool, timeout: float, run_id: str
) -> tuple[str, list[dict]]:
    tool_calls: list[dict] = []
    conversation = prompt
    if allowed_tools:
        descriptions = "\n".join(
            f"- {t}: {tools.REGISTRY[t].description} (params: {tools.REGISTRY[t].params})"
            for t in allowed_tools if t in tools.REGISTRY
        )
        conversation = (
            f"{prompt}\n\n---\nYou may call these tools, one at a time:\n{descriptions}\n\n"
            'To call a tool, respond with EXACTLY one line: '
            'TOOL_CALL: {"tool": "<id>", "args": {...}}\n'
            "When you have your final answer, respond with the answer text only."
        )

    output = ""
    for turn in range(MAX_TOOL_TURNS):
        runs_mod.append_raw_log(config, run_id, f"--- turn {turn + 1} PROMPT ---\n{conversation}")
        output = harness.invoke(config, conversation, timeout=timeout)
        runs_mod.append_raw_log(config, run_id, f"--- turn {turn + 1} OUTPUT ---\n{output}")

        match = _TOOL_CALL_RE.search(output)
        if not match or not allowed_tools:
            return output, tool_calls
        try:
            call = json.loads(match.group(1))
            tool_id, args = call["tool"], call.get("args", {})
        except (json.JSONDecodeError, KeyError):
            return output, tool_calls

        is_write = tools.exists(tool_id) and tools.is_write(tool_id)
        if tool_id not in allowed_tools:
            result: Any = {"error": f"{tool_id} is not in this workflow's tools: allowlist"}
        elif dry_run and is_write:
            result = {"stubbed": True, "success": True}
        else:
            try:
                result = _with_retry(config, tools.call, home, config, tool_id, args)
            except tools.ConnectorError as e:
                result = {"error": str(e)}

        tool_calls.append({
            "tool": tool_id, "args": args, "is_write": is_write,
            "stubbed": bool(dry_run and is_write),
            "timestamp": _now().isoformat(), "result_summary": str(result)[:500],
        })
        conversation += (
            f'\n\nTOOL_CALL: {json.dumps(call)}\n'
            f'TOOL_RESULT: {json.dumps(result, default=str)[:2000]}\n\nContinue.'
        )

    return output, tool_calls


def route_output(
    home: Path, output_spec: dict, text: str, note: str | None = None
) -> dict:
    """Writes the output where it belongs and returns a description of what
    happened. Does not print: stdout routing is a decision for the CLI
    layer, which also needs plain stdout free for `--json` output."""
    target = output_spec.get("target", "stdout")
    if note:
        text = f"<!-- {note} -->\n\n{text}"

    if target == "memory":
        return {"target": "memory", "text": text}
    if target == "stdout":
        return {"target": "stdout", "text": text}
    if target == "file":
        path_template = output_spec.get("path", "outputs/output-{date}.md")
        rendered = path_template.replace("{date}", date.today().isoformat())
        dest = home / rendered
        lock = paths.lock_path(home)
        lock.parent.mkdir(parents=True, exist_ok=True)
        with open(lock, "w") as lf:
            fcntl.flock(lf, fcntl.LOCK_EX)
            try:
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_text(text)
            finally:
                fcntl.flock(lf, fcntl.LOCK_UN)
        return {"target": "file", "path": str(dest.relative_to(home)), "text": text}
    raise RunError(f"unknown output target: {target}")


def run(
    home: Path,
    config: dict,
    workflow_id: str,
    trigger: str = "manual",
    cli_inputs: dict | None = None,
    dry_run: bool = False,
    output_override: dict | None = None,
    late_scheduled_at: str | None = None,
) -> dict:
    cli_inputs = cli_inputs or {}
    run_id = runs_mod.new_run_id()
    start = _now()
    record: dict = {
        "id": run_id, "workflow_id": workflow_id, "trigger": trigger,
        "start_time": start.isoformat(), "late": trigger == "late",
    }

    def fail(message: str, **extra) -> "RunError":
        end = _now()
        record.update(outcome="failed", error=message, end_time=end.isoformat(),
                       duration_seconds=(end - start).total_seconds(), **extra)
        runs_mod.write_record(config, record)
        return RunError(message, record)

    # Stage 1: load and validate
    try:
        wf = workflow_mod.load(home, workflow_id)
    except workflow_mod.WorkflowError as e:
        raise fail(str(e))
    errors = workflow_mod.validate(wf, home)
    if errors:
        raise fail("; ".join(errors))

    if wf.pipeline:
        return _run_pipeline(home, config, wf, trigger, dry_run, run_id, start, record)

    # Stage 2: lock, checkpoint hand edits, release
    lock = paths.lock_path(home)
    lock.parent.mkdir(parents=True, exist_ok=True)
    with open(lock, "w") as lf:
        fcntl.flock(lf, fcntl.LOCK_EX)
        try:
            claims.scan_and_process(home)
        finally:
            fcntl.flock(lf, fcntl.LOCK_UN)

    # Stage 3: resolve inputs
    try:
        context, inputs_meta = resolve_inputs(home, config, wf, cli_inputs)
    except RunError as e:
        raise fail(str(e), inputs_resolved=e.record.get("inputs_resolved", []))

    # Stage 4: render prompt
    guideline_texts, guidelines_inlined = {}, []
    for g in wf.guidelines:
        text = (paths.guidelines_dir(home) / g).read_text()
        guideline_texts[g] = text
        guidelines_inlined.append({
            "path": g, "version": versioning.latest_version_number(home, f"guidelines/{g}")
        })
    prompt = render_prompt(wf, guideline_texts, context)

    # Stage 5 + 6: per-run tool loop, invoke model backend
    timeout = harness.parse_duration(wf.timeout)
    try:
        output_text, tool_calls = _tool_call_loop(
            home, config, prompt, wf.tools, dry_run, timeout, run_id
        )
    except harness.HarnessError as e:
        raise fail(str(e), inputs_resolved=inputs_meta, guidelines_inlined=guidelines_inlined)

    # Stage 7: route output
    effective_output = output_override or wf.output or {"target": "stdout"}
    note = None
    if late_scheduled_at:
        note = f"scheduled {late_scheduled_at}, ran {_now().strftime('%H:%M')}"
    output_info = route_output(home, effective_output, output_text, note)

    # Stage 8: close the run record
    end = _now()
    record.update(
        inputs_resolved=inputs_meta,
        guidelines_inlined=guidelines_inlined,
        tool_calls=tool_calls,
        model=config_mod.get(config, "model.harness_cmd"),
        outcome="success",
        output=output_info,
        end_time=end.isoformat(),
        duration_seconds=(end - start).total_seconds(),
    )
    runs_mod.write_record(config, record)
    return record


def _run_pipeline(
    home: Path, config: dict, wf: workflow_mod.Workflow, trigger: str,
    dry_run: bool, run_id: str, start: datetime, record: dict
) -> dict:
    stages = []
    stdin_text = ""
    for i, stage_id in enumerate(wf.pipeline):
        is_last = i == len(wf.pipeline) - 1
        stage_override = (wf.output or {"target": "stdout"}) if is_last else {"target": "memory"}
        try:
            stage_record = run(
                home, config, stage_id, trigger="pipeline",
                cli_inputs={"_stdin": stdin_text}, dry_run=dry_run,
                output_override=stage_override,
            )
        except RunError as e:
            end = _now()
            record.update(outcome="failed", error=f"stage {stage_id!r} failed: {e}",
                           stages=stages, end_time=end.isoformat(),
                           duration_seconds=(end - start).total_seconds())
            runs_mod.write_record(config, record)
            raise RunError(str(e), record)
        stage_record["parent_run_id"] = run_id
        stages.append(stage_record)
        stdin_text = stage_record["output"].get("text", "")

    end = _now()
    record.update(
        outcome="success", stages=stages, output=stages[-1]["output"] if stages else {},
        end_time=end.isoformat(), duration_seconds=(end - start).total_seconds(),
    )
    runs_mod.write_record(config, record)
    return record
