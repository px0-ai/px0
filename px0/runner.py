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
import shutil
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from px0 import approvals, claims, config as config_mod, guidelines as guidelines_mod
from px0 import inbox as inbox_mod
from px0 import mcp as mcp_mod
from px0 import memory as memory_mod
from px0 import replay as replay_mod
from px0 import harness, paths, retrieval
from px0 import runs as runs_mod
from px0 import tools, versioning
from px0 import workflow as workflow_mod

# The default ceiling on tool-call turns in px0's own loop. Raised from 5,
# which was low enough that ordinary work hit it: every turn resends the whole
# conversation, so the cost of a high ceiling is paid only by runs that use it,
# while the cost of a low one was paid by every run that needed a sixth step
# and silently stopped short. `runs.max_tool_turns` moves it per store, and
# `model.agent_loop = "mcp"` removes the ceiling entirely by letting the
# harness run its own loop.
MAX_TOOL_TURNS = 12

# Values a run must never write into its record. The record is kept for a year;
# a connector that echoes a bearer token back in its response should not be
# what puts one on disk for that long.
_SECRET_RE = re.compile(
    r"(?i)\b(?:bearer\s+[A-Za-z0-9._\-]{12,}"
    r"|(?:gh[pousr]|xox[baprs])_[A-Za-z0-9]{8,}"
    r"|sk-[A-Za-z0-9\-_]{12,}"
    r"|eyJ[A-Za-z0-9._\-]{20,})")


def redact(text: str) -> str:
    """Masks anything that looks like a credential in text bound for a record.

    Deliberately pattern-based and short: it catches the shapes that actually
    turn up in connector responses -- bearer headers, provider key prefixes,
    JWTs -- and makes no claim to catch a secret that looks like prose. The
    raw log is the unredacted account and lives outside the store on a
    fortnight's retention; this is about what survives for a year.
    """
    return _SECRET_RE.sub("<redacted>", text or "")


def _max_turns(config: dict) -> int:
    """How many tool-call turns this store allows a run, floored at one."""
    try:
        return max(1, int(config_mod.get(config, "runs.max_tool_turns", MAX_TOOL_TURNS)))
    except (TypeError, ValueError):
        return MAX_TOOL_TURNS
# Matches a `TOOL_CALL: {...}` line and captures the JSON payload.
_TOOL_CALL_RE = re.compile(r"TOOL_CALL:\s*(\{.*\})", re.DOTALL)
# Matches `{{dotted.path}}` template placeholders.
_TEMPLATE_RE = re.compile(r"\{\{\s*([\w.\-]+)\s*\}\}")


class RunError(Exception):
    """A run failed. Carries the partial run record (if any) so callers can
    still write/report it before propagating the failure."""
    def __init__(self, message: str, record: dict | None = None):
        """Stores the failure message and the partial run record, if any."""
        super().__init__(message)
        self.record = record or {}


def _now() -> datetime:
    """Current time in UTC, used for all run timestamps."""
    return datetime.now(timezone.utc)


# Units accepted in a relative clock placeholder like `{{now-24h}}`.
_TIME_UNITS = {"m": "minutes", "h": "hours", "d": "days", "w": "weeks"}
_RELATIVE_TIME_RE = re.compile(r"^now-(\d+)([mhdw])$")


def _time_value(name: str, filename_safe: bool = False) -> str | None:
    """Resolves one clock placeholder, or None if `name` is not one.

    The grammar these names come from is `workflow.TIME_PLACEHOLDER_RE`, which is
    also what validation accepts, so a workflow can never name a placeholder that
    passes validation and then resolves to nothing here.

    Every digest workflow needs a window ("commits since yesterday") and a
    scheduled one cannot be handed a literal timestamp, so `{{now-24h}}` is the
    only way to write it. An argument gets ISO 8601 with a `Z`, which is what the
    connectors' `since`/`until` parameters take; a path gets the same instant
    with the colons swapped out, since `filename_safe` is the difference between
    a timestamp and a filename.
    """
    now = _now()
    stamp = "%Y-%m-%dT%H-%M-%S" if filename_safe else "%Y-%m-%dT%H:%M:%SZ"
    if name == "now":
        return now.strftime(stamp)
    if name in ("today", "date"):
        return now.date().isoformat()
    if name == "datetime":
        return now.strftime("%Y-%m-%dT%H-%M-%S")
    if name == "time":
        return now.strftime("%H-%M-%S")
    m = _RELATIVE_TIME_RE.match(name)
    if m:
        delta = timedelta(**{_TIME_UNITS[m.group(2)]: int(m.group(1))})
        return (now - delta).strftime(stamp)
    return None


def _lookup(context: dict, dotted: str) -> Any:
    """Resolves a dotted path like `input.foo` against a nested dict context.

    Falls through to the clock placeholders (`now`, `today`, `now-24h`) when the
    context has no such key, so a workflow can express a time window without one
    being passed in. The context wins, so a real input named `now` still shadows
    the placeholder.

    Returns None if the name is neither, rather than raising.
    """
    node: Any = context
    for part in dotted.split("."):
        if isinstance(node, dict) and part in node:
            node = node[part]
        else:
            return _time_value(dotted)
    return node


def render_value(value: Any, context: dict) -> Any:
    """Recursively resolves `{{dotted.path}}` template placeholders against context.
    A string that is entirely one placeholder returns the looked-up value as-is
    (preserving its type); a placeholder embedded in a larger string is
    stringified in place. Lists and dicts are walked recursively; other types
    pass through unchanged."""
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
    """Calls fn with exponential backoff on ConnectorError, up to
    connectors.retries attempts. ConnectorNotConfigured is never retried --
    it means the connector isn't set up, not that the call failed transiently.
    Re-raises the last error if all attempts are exhausted."""
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
                delay *= 2  # exponential backoff
    raise last_err


def resolve_inputs(
    home: Path, config: dict, wf: workflow_mod.Workflow, cli_inputs: dict
) -> tuple[dict, list[dict]]:
    """Resolves every declared input of a workflow (tool call, retrieval query,
    stdin source, or nested sub-workflow run) into a template context dict.
    Returns (context, meta) where meta is a per-input list of resolution
    outcomes for the run record. An optional input that fails resolves to None
    and is marked degraded rather than aborting the run; a required input that
    fails raises RunError."""
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
                                  cli_inputs=sub_cli, output_override={"target": "memory"},
                                  retry=False)
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
        meta.append({"id": inp.id, "kind": inp.kind, "ok": True,
                     "size": _value_size(value), "empty": _is_empty(value)})

    return context, meta


def _value_size(value: Any) -> int | None:
    """How much an input actually resolved to, for the run record.

    Recorded because "the input resolved" and "the input resolved to anything"
    are different facts, and only the first was ever written down. A digest
    whose GitHub query has quietly returned nothing for a month succeeds every
    time -- with an empty prompt section the model then invents around.
    """
    if isinstance(value, str):
        return len(value)
    if isinstance(value, (list, tuple, dict, set)):
        return len(value)
    return None


def _is_empty(value: Any) -> bool:
    """Whether a resolved input carries nothing worth putting in the prompt.
    A tool that answers `{"items": []}` counts as empty: the envelope is not
    the content."""
    if value is None:
        return True
    if isinstance(value, str):
        return not value.strip()
    if isinstance(value, (list, tuple, set)):
        return len(value) == 0
    if isinstance(value, dict):
        if not value:
            return True
        payloads = [v for k, v in value.items()
                    if k not in ("successful", "success", "error", "ok", "status")]
        if payloads and all(
            v in (None, "", [], {}) or (isinstance(v, (list, dict, str)) and len(v) == 0)
            for v in payloads
        ):
            return True
    return False


def render_prompt(wf: workflow_mod.Workflow, guideline_texts: dict[str, str],
                  context: dict, memory_block: str = "") -> str:
    """Builds the final prompt: renders the workflow body's templates against
    context, then inlines guideline text either at an explicit `{{guidelines}}`
    placeholder or, if none is present, prepended before the body.

    Keyed by store-relative path, headed by the guideline's name: the path is
    what provenance records, the name is what reads as a heading in a prompt.
    """
    guidelines_block = "\n\n".join(
        f"# {guidelines_mod.name_for(rel)}\n\n{text}"
        for rel, text in guideline_texts.items()
    )
    body = render_value(wf.body, context)
    if not isinstance(body, str):
        body = str(body)
    if "{{guidelines}}" in wf.body:
        body = body.replace("{{guidelines}}", guidelines_block)
        guidelines_block = ""
    # Memory first, then conventions, then the instructions. Standing context
    # about the user belongs above the rules for judging output, which belong
    # above the job -- and a workflow that places `{{memory}}` itself gets it
    # exactly there instead.
    blocks = []
    if "{{memory}}" in body:
        body = body.replace("{{memory}}", memory_block)
    elif memory_block:
        blocks.append(memory_block)
    if guidelines_block:
        blocks.append(guidelines_block)
    blocks.append(body)
    return "\n\n".join(b for b in blocks if b)


def _tool_call_loop(
    home: Path, config: dict, prompt: str, allowed_tools: list[str],
    dry_run: bool, timeout: float, run_id: str, wf=None
) -> tuple[str, list[dict], dict]:
    """Drives the model through up to MAX_TOOL_TURNS turns, feeding it a
    `TOOL_CALL: {...}` protocol line-by-line since the harness backend is a
    plain non-interactive subprocess rather than a real MCP transport. Each
    call is checked against the workflow's tool allowlist; write tools are
    stubbed out (never executed) when dry_run is set. Returns the model's
    final text output and the list of tool calls actually made, each recorded
    for the run's audit trail, plus what the run cost.

    On cost: the harness is another program, and only some of them report token
    counts. When one does -- `model.output_format` puts `claude -p` into its
    JSON envelope, for instance -- those counts are summed and the usage block
    says `reported`. When one does not, the cost is approximated from the
    characters sent and received and labelled `estimated`, so a number nobody
    measured is never passed off as one that was.

    Every turn also writes to the run's event stream: what the model was asked,
    what it cost, which tool it reached for, and whether that call was allowed.
    That stream, not the raw log, is what `px0 workflows health` reads later.

    A write the workflow holds back (see `approvals.needs_approval`) is drafted
    rather than executed: it is written to the approval queue in full and the
    model is told so, which lets the run finish and produce its output while
    the thing that would leave a mark waits for a person.
    """
    usage = {"model_calls": 0, "prompt_chars": 0, "output_chars": 0,
             "estimated_tokens": 0, "estimated": True, "reported": False,
             "input_tokens": 0, "output_tokens": 0,
             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0,
             "cost_usd": 0.0, "turns": 0, "hit_turn_cap": False}
    tool_calls: list[dict] = []
    queued: list[dict] = []
    max_turns = _max_turns(config)
    conversation = prompt
    offered: list[str] = []
    if allowed_tools:
        # resolve() covers tools discovered by `px0 workflows new`, not just the curated registry
        specs = [(t, tools.resolve(t, home)) for t in allowed_tools]
        descriptions = "\n".join(
            f"- {t}: {spec.description} (params: {spec.params})"
            for t, spec in specs if spec is not None
        )
        offered = [t for t, spec in specs if spec is not None]
        unresolved = [t for t, spec in specs if spec is None]
        if unresolved:
            # Allowlisted but undescribable: the model is never told these
            # exist, so they can only ever show up as a refused call. Worth an
            # event, because it reads as "the model ignores that tool" from
            # every other angle.
            runs_mod.append_event(config, run_id, "tools_unresolved", tools=unresolved)
        conversation = (
            f"{prompt}\n\n---\nYou may call these tools, one at a time:\n{descriptions}\n\n"
            'To call a tool, respond with EXACTLY one line: '
            'TOOL_CALL: {"tool": "<id>", "args": {...}}\n'
            "When you have your final answer, respond with the answer text only."
        )

    runs_mod.append_event(config, run_id, "prompt",
                          prompt_chars=len(conversation),
                          tools_offered=offered, dry_run=bool(dry_run))

    output = ""
    for turn in range(max_turns):
        runs_mod.append_raw_log(config, run_id,
                                 f"--- turn {turn + 1} PROMPT ---\n{conversation}")
        try:
            reply = harness.invoke_detailed(config, conversation, timeout=timeout)
        except harness.HarnessError as e:
            # Same reason as the agent loop: calls already made really happened,
            # and a record that omits them understates what the run did.
            e.tool_calls = list(tool_calls)
            raise
        output = reply.text
        runs_mod.append_raw_log(config, run_id,
                                 f"--- turn {turn + 1} OUTPUT ---\n{output}")
        if reply.stderr.strip():
            # Only kept when the harness was asked to narrate; otherwise this is
            # the odd warning line, and it belongs with the turn that produced it.
            runs_mod.append_raw_log(config, run_id,
                                     f"--- turn {turn + 1} HARNESS STDERR ---\n"
                                     f"{reply.stderr.strip()}")
        usage["model_calls"] += 1
        usage["turns"] = turn + 1
        usage["prompt_chars"] += len(conversation)
        usage["output_chars"] += len(output or "")
        # ~4 characters per token is the rule of thumb across these tokenizers:
        # close enough to compare one nightly against another, not a bill.
        usage["estimated_tokens"] = (usage["prompt_chars"] + usage["output_chars"]) // 4
        _fold_reported_usage(usage, reply)

        runs_mod.append_event(
            config, run_id, "model_call", turn=turn + 1,
            prompt_chars=len(conversation), output_chars=len(output or ""),
            elapsed_seconds=reply.elapsed_seconds,
            output_format=reply.output_format,
            reported_usage=reply.usage or None,
            harness=reply.meta.get("model") or harness.harness_name(
                harness.resolve_harness_cmd(
                    config_mod.get(config, "model.harness_cmd", "claude -p"))),
            stderr_excerpt=reply.stderr.strip()[:500] or None,
            note=reply.meta.get("downgraded") or reply.meta.get("unparsed_envelope"),
        )

        match = _TOOL_CALL_RE.search(output)
        if not match or not allowed_tools:
            usage["approvals"] = queued
            return output, tool_calls, usage  # model gave a final answer, not a tool request
        try:
            call = json.loads(match.group(1))
            tool_id, args = call["tool"], call.get("args", {})
        except (json.JSONDecodeError, KeyError):
            # malformed tool call: treat the raw output as the final answer
            runs_mod.append_event(config, run_id, "tool_call_malformed",
                                  turn=turn + 1, excerpt=match.group(1)[:200])
            usage["approvals"] = queued
            return output, tool_calls, usage

        is_write = tools.exists(tool_id, home) and tools.is_write(tool_id, home)
        refused = tool_id not in allowed_tools
        elapsed = 0.0
        if refused:
            # A tool outside the workflow's allowlist is refused, not called.
            # This branch used to fall through into the execution below, so the
            # refusal was only ever a message written into the transcript while
            # the call itself went ahead -- a model that named any tool at all
            # got it run. The allowlist is the whole of what the user approved
            # when the workflow was built, so it has to be the thing that
            # decides, not a string in the conversation.
            result: Any = {"error": f"{tool_id} is not in this workflow's tools: allowlist"}
            runs_mod.append_event(config, run_id, "tool_refused", turn=turn + 1,
                                  tool=tool_id, is_write=is_write,
                                  allowed=list(allowed_tools))
        elif dry_run and is_write:
            result = {"stubbed": True, "success": True}  # dry runs never execute side effects
        elif wf is not None and approvals.needs_approval(wf, config, tool_id, is_write):
            # Drafted, not sent. The model is told plainly, so it writes its
            # final answer as though the call will happen rather than reporting
            # a failure the user would then have to interpret.
            approval = approvals.queue(
                home, run_id=run_id, workflow_id=getattr(wf, "id", ""),
                tool=tool_id, args=args,
                reason=getattr(wf, "description", ""),
                output_preview=output or "")
            queued.append({"id": approval["id"], "tool": tool_id,
                           "status": approvals.PENDING})
            result = {"queued_for_approval": True, "approval_id": approval["id"],
                      "note": "drafted and shown to the user for approval; "
                              "it has not been sent"}
            runs_mod.append_event(config, run_id, "approval_queued", turn=turn + 1,
                                  tool=tool_id, approval=approval["id"],
                                  arg_keys=sorted(args.keys())
                                  if isinstance(args, dict) else None)
        else:
            import time as time_mod
            t0 = time_mod.monotonic()
            try:
                result = _with_retry(config, tools.call, home, config, tool_id, args)
            except tools.ConnectorError as e:
                result = {"error": str(e)}
            elapsed = time_mod.monotonic() - t0

        failed = isinstance(result, dict) and "error" in result
        awaiting = isinstance(result, dict) and result.get("queued_for_approval")
        tool_calls.append({
            "tool": tool_id, "args": args, "is_write": is_write,
            "stubbed": bool(dry_run and is_write),
            "refused": refused,
            "queued": bool(awaiting),
            "failed": bool(failed),
            "timestamp": _now().isoformat(),
            "result_summary": redact(str(result))[:500],
            "elapsed_seconds": round(elapsed, 3),
        })
        runs_mod.append_event(
            config, run_id, "tool_call", turn=turn + 1, tool=tool_id,
            is_write=is_write, refused=refused, failed=bool(failed),
            stubbed=bool(dry_run and is_write),
            arg_keys=sorted(args.keys()) if isinstance(args, dict) else None,
            elapsed_seconds=round(elapsed, 3),
            error=str(result.get("error"))[:300] if failed else None,
        )
        conversation += (
            f'\n\nTOOL_CALL: {json.dumps(call)}\n'
            f'TOOL_RESULT: {json.dumps(result, default=str)[:2000]}\n\nContinue.'
        )

    # Falling out of the loop means the model was still asking for tools on its
    # last allowed turn. Recorded rather than inferred, because "always burns
    # every turn" is the clearest sign a workflow is underspecified.
    usage["hit_turn_cap"] = True
    usage["approvals"] = queued
    runs_mod.append_event(config, run_id, "turn_cap_reached", turns=max_turns)
    return output, tool_calls, usage


def agent_loop_mode(config: dict) -> str:
    """Which loop this store runs: px0's own, or the harness's.

    Defaults to px0's own. The MCP loop removes the turn ceiling and is the
    better answer for anything that takes more than a handful of steps, but it
    depends on flags belonging to another program, so it is opted into rather
    than assumed. `auto` splits the difference: use it wherever px0 has
    verified flags for the configured harness, and fall back silently
    otherwise.
    """
    mode = str(config_mod.get(config, "model.agent_loop", "builtin") or "builtin")
    return mode if mode in ("builtin", "auto", "mcp") else "builtin"


def _agent_loop(
    home: Path, config: dict, prompt: str, allowed_tools: list[str],
    dry_run: bool, timeout: float, run_id: str, wf
) -> tuple[str, list[dict], dict]:
    """Hands the workflow's tools to the harness and lets it run its own loop.

    px0 stops being the agent here and becomes the tool provider. It writes an
    MCP config naming a scoped `px0 mcp serve`, starts the harness once with
    that config and an allowlist of exactly this workflow's tools, and reads
    back what was called from the sidecar the scoped server wrote.

    The enforcement that used to live in the turn loop moves into that server
    (`mcp.call_scoped`): the allowlist, dry-run stubbing, held-back writes, and
    the event stream all still happen, on every call, in one place.

    Raises HarnessError like the builtin loop does, so a failure here reaches
    the same stage-6 handler and is recorded the same way.
    """
    import tempfile

    scope_dir = Path(tempfile.mkdtemp(prefix="px0-scope-"))
    calls_path = scope_dir / "calls.jsonl"
    confirm_tools = [t for t in allowed_tools
                     if approvals.needs_approval(
                         wf, config, t, tools.exists(t, home) and tools.is_write(t, home))]
    scope = {
        "run_id": run_id,
        "workflow_id": getattr(wf, "id", ""),
        "reason": getattr(wf, "description", ""),
        "tools": list(allowed_tools),
        "confirm_tools": confirm_tools,
        "dry_run": bool(dry_run),
        "calls_path": str(calls_path),
    }
    scope_path = scope_dir / "scope.json"
    scope_path.write_text(json.dumps(scope, default=str))

    # The harness starts the server, so this names a command rather than an
    # address. `sys.executable -m px0.cli` rather than a bare `px0`, because a
    # store being driven from a virtualenv or a checkout may have no `px0` on
    # the harness's PATH.
    import sys as sys_mod

    server = {"mcpServers": {"px0": {
        "command": sys_mod.executable,
        "args": ["-m", "px0.cli", "mcp", "serve", "--scope", str(scope_path)],
        "env": {"PX0_HOME": str(home)},
    }}}
    mcp_config_path = scope_dir / "mcp.json"
    mcp_config_path.write_text(json.dumps(server))

    names = [mcp_mod.mcp_name(t) for t in allowed_tools
             if tools.resolve(t, home) is not None]
    harness_cmd = harness.resolve_harness_cmd(
        config_mod.get(config, "model.harness_cmd", "claude -p"))
    flags = harness.agent_flags(harness_cmd, str(mcp_config_path), names)
    if not flags:
        raise harness.AgentLoopUnsupported(
            f"{harness_cmd!r} has no verified way to be handed tools; "
            "set model.agent_loop = 'builtin'")

    runs_mod.append_event(config, run_id, "agent_loop_started",
                          tools_offered=list(allowed_tools),
                          confirm=confirm_tools, dry_run=bool(dry_run))
    runs_mod.append_raw_log(config, run_id, f"--- agent loop PROMPT ---\n{prompt}")
    try:
        reply = harness.invoke_detailed(config, prompt, timeout=timeout,
                                        extra_flags=flags)
    except harness.HarnessError as e:
        # The harness died, but the tools it reached before dying really ran.
        # Losing that would understate what the run did in the one place it
        # matters most: retention exempts runs that called a write tool, so a
        # timeout after posting to Slack would let the log of that post be
        # pruned as though nothing had happened.
        e.tool_calls, _ = _read_scope_calls(calls_path)
        shutil.rmtree(scope_dir, ignore_errors=True)
        raise
    runs_mod.append_raw_log(config, run_id, f"--- agent loop OUTPUT ---\n{reply.text}")
    if reply.stderr.strip():
        runs_mod.append_raw_log(config, run_id,
                                f"--- agent loop HARNESS STDERR ---\n"
                                f"{reply.stderr.strip()[:20000]}")

    tool_calls, queued = _read_scope_calls(calls_path)

    usage = {"model_calls": 1, "prompt_chars": len(prompt),
             "output_chars": len(reply.text or ""),
             "estimated_tokens": (len(prompt) + len(reply.text or "")) // 4,
             "estimated": True, "reported": False,
             "input_tokens": 0, "output_tokens": 0,
             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0,
             "cost_usd": 0.0, "turns": 1, "hit_turn_cap": False,
             "loop": "mcp", "approvals": queued}
    _fold_reported_usage(usage, reply)

    runs_mod.append_event(config, run_id, "agent_loop_finished",
                          tool_calls=len(tool_calls),
                          output_chars=len(reply.text or ""),
                          reported_usage=reply.usage or None)
    shutil.rmtree(scope_dir, ignore_errors=True)
    return reply.text, tool_calls, usage


def _read_scope_calls(calls_path: Path) -> tuple[list[dict], list[dict]]:
    """What the scoped MCP server recorded, and which of it is awaiting approval.

    Read from a sidecar rather than observed directly because the server runs
    in a process the *harness* started, not px0 -- so this file is the only
    account a run has of what its own tools did.
    """
    tool_calls, queued = [], []
    if not calls_path.exists():
        return tool_calls, queued
    for line in calls_path.read_text().splitlines():
        try:
            entry = json.loads(line)
        except json.JSONDecodeError:
            continue
        entry.setdefault("refused", False)
        entry.setdefault("stubbed", False)
        entry.setdefault("queued", False)
        entry.setdefault("timestamp", _now().isoformat())
        entry["result_summary"] = redact(str(entry.get("result_summary", "")))[:500]
        tool_calls.append(entry)
        if entry.get("approval"):
            queued.append({"id": entry["approval"], "tool": entry.get("tool"),
                           "status": approvals.PENDING})
    return tool_calls, queued


def _fold_reported_usage(usage: dict, reply: "harness.Reply") -> None:
    """Adds one harness reply's own token counts into the run's usage block.

    Kept apart from the loop because the shape is the harness's, not px0's:
    whatever integer fields it reports are summed under their own names, so a
    backend that reports something px0 has never heard of still gets it
    recorded rather than dropped. The moment any call reports real counts, the
    block stops calling itself an estimate.
    """
    if not reply.usage:
        return
    usage["estimated"] = False
    usage["reported"] = True
    for key, value in reply.usage.items():
        if key == "reported" or isinstance(value, bool):
            continue
        if isinstance(value, (int, float)):
            usage[key] = usage.get(key, 0) + value
    cost = reply.meta.get("total_cost_usd")
    if isinstance(cost, (int, float)):
        usage["cost_usd"] = round(usage.get("cost_usd", 0.0) + cost, 6)


def _render_output_path(template: str) -> str:
    """Substitutes the clock placeholders in an output path template.

    Both brace styles are accepted: the prompt body references inputs as
    {{input_id}}, and a plan that carried that habit into the path produced a
    file literally named `report-{2026-08-17}.md` -- the inner {date}
    substituted, the outer braces left behind.

    The vocabulary is `workflow`'s, the same one an argument may use, and every
    value is rendered filename-safe. Raises RunError on an unknown placeholder
    rather than writing a filename with braces in it -- though `validate` reports
    that before a run starts, so reaching this is a workflow edited mid-run.
    """
    def sub(m: re.Match) -> str:
        value = _time_value(m.group(1), filename_safe=True)
        return m.group(0) if value is None else value

    rendered = workflow_mod._OUTPUT_TEMPLATE_RE.sub(sub, template)
    errors = workflow_mod.output_path_errors(rendered)
    if errors:
        raise RunError(errors[0])
    return rendered


def output_rel(rendered: str) -> str:
    """Puts a rendered output path under the store's `output/` directory.

    A plan writes `logs/daily.md` and means `output/logs/daily.md`, and one that
    already said `outputs/` meant the same folder by a name the store does not
    use. Every run's output lives under one root, whatever the plan called it.
    """
    if rendered.startswith("outputs/"):
        return "output/" + rendered.removeprefix("outputs/")
    if not rendered.startswith("output/"):
        return f"output/{rendered}"
    return rendered


def output_destination(home: Path, path_template: str) -> Path:
    """The absolute file an `output.path` will be written to, placeholders intact.

    For reporting a workflow's destination before it has ever run. The same
    normalization a run applies, so what a build promises is where the run puts
    it -- and `output_rel` is the store-relative half of the same answer, which
    is what a report shows once it has named the store it is relative to.
    """
    return (home / output_rel(path_template)).resolve()


def _resolve_output_dest(home: Path, rendered: str) -> Path:
    """Resolves a rendered output path inside the store's output directory.

    An absolute path or a `..` segment used to escape the store entirely -- a
    run could write anywhere the user could, from a path a model wrote into the
    plan. Everything is now confined under the output directory.
    """
    dest = (home / rendered).resolve()
    root = paths.output_dir(home).resolve()
    if root not in dest.parents:
        raise RunError(
            f"output.path escapes the store's output directory: {rendered!r} "
            f"resolves outside {root}"
        )
    return dest


def route_output(
    home: Path, output_spec: dict, text: str, note: str | None = None
) -> dict:
    """Writes the output where it belongs and returns a description of what
    happened. Does not print: stdout routing is a decision for the CLI
    layer, which also needs plain stdout free for `--json` output.
    File writes are serialized with a store-wide lock to avoid two concurrent
    runs racing on the same output path."""
    target = output_spec.get("target", "stdout")
    if note:
        text = f"<!-- {note} -->\n\n{text}"

    if target == "memory":
        return {"target": "memory", "text": text}
    if target in ("stdout", "inbox"):
        # `inbox` carries no destination of its own: the delivery below files
        # it, and the text still comes back so a manual run prints it too.
        return {"target": target, "text": text}
    if target == "file":
        path_template = output_spec.get("path", "output/output-{date}.md")
        rendered = output_rel(_render_output_path(path_template))
        dest = _resolve_output_dest(home, rendered)
        lock = paths.lock_path(home)
        lock.parent.mkdir(parents=True, exist_ok=True)
        with open(lock, "w") as lf:
            fcntl.flock(lf, fcntl.LOCK_EX)
            try:
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_text(text)
            finally:
                fcntl.flock(lf, fcntl.LOCK_UN)
        rel_path = str(dest.relative_to(home)) if dest.is_relative_to(home) else str(dest)
        return {"target": "file", "path": rel_path, "text": text}
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
    timeout_override: str | None = None,
    retry: bool = True,
) -> dict:
    """Runs a workflow, retrying per its policy, and notifies if it ends failed.

    Each attempt writes its own run record, so `px0 runs list` shows the
    failures that led to a success instead of hiding them. The last attempt's
    record is returned, or its RunError raised.

    `retry=False` is for nested runs -- a pipeline stage or a sub-workflow
    input -- where the parent owns the retry decision. Without it, a three-stage
    pipeline with three attempts each could run nine times.
    """
    try:
        wf = workflow_mod.load(home, workflow_id)
    except workflow_mod.WorkflowError:
        wf = None  # let _run_once produce the real error and its record

    attempts, backoff = (1, 0.0)
    if retry and wf is not None:
        attempts, backoff = workflow_mod.retry_policy(wf, config)

    last_error: RunError | None = None
    for attempt in range(1, attempts + 1):
        try:
            record = _run_once(
                home, config, workflow_id, trigger=trigger, cli_inputs=cli_inputs,
                dry_run=dry_run, output_override=output_override,
                late_scheduled_at=late_scheduled_at, timeout_override=timeout_override,
                attempt=attempt, attempts=attempts,
            )
        except RunError as e:
            last_error = e
            if attempt < attempts:
                time.sleep(backoff * (2 ** (attempt - 1)))
                continue
            _notify_failure(home, config, wf, e.record)
            _trip_breaker_if_stuck(home, config, wf, trigger)
            raise
        return record

    if last_error is not None:  # unreachable in practice; keeps the contract explicit
        raise last_error
    raise RunError(f"{workflow_id} produced no run")


def _trip_breaker_if_stuck(home: Path, config: dict, wf, trigger: str) -> None:
    """Parks a workflow that keeps failing the same way. Never raises.

    Only for runs nobody asked for. A manual run that fails is a person at a
    terminal reading the error; parking their workflow underneath them would be
    taking a decision they are in the middle of making. An unattended one has
    nobody to notice, which is the case this exists for -- a dead connector
    otherwise means an hourly failure and an hourly notification, forever,
    with nothing learning that nothing has changed.

    The park is announced through the same channel failures use, because a
    workflow that silently stops firing is worse than one that fails loudly.
    """
    if trigger == "manual" or wf is None:
        return
    try:
        from px0 import analysis as analysis_mod

        streak = analysis_mod.should_trip_breaker(config, wf.id)
        if not streak:
            return
        reason = (f"parked automatically after {streak['count']} consecutive "
                  f"failures: {streak['shape'][:120]}")
        change_id = analysis_mod.set_enabled(home, wf.id, False, reason,
                                             actor="breaker")
        if change_id is None:
            return  # already parked; nothing to announce twice
        from px0 import notify as notify_mod

        title = f"px0: {wf.id} has been disabled"
        body = (f"{reason}\n"
                f"It will not fire again until you re-enable it: "
                f"`px0 workflows enable {wf.id}`")
        notify_mod._send(home, config,
                         notify_mod._policy(config, wf.on_failure), title, body)
    except Exception:
        # A breaker that can fail a run is worse than one that misses a case.
        pass


def _notify_failure(home: Path, config: dict, wf, record: dict | None) -> None:
    """Sends the failure notification and records what came of it. Never raises."""
    if not isinstance(record, dict):
        return
    try:
        from px0 import notify as notify_mod

        result = notify_mod.on_failure(home, config, record,
                                       wf.on_failure if wf is not None else None)
        record["notified"] = result
        runs_mod.write_record(config, record)
    except Exception:
        pass


def _run_once(
    home: Path,
    config: dict,
    workflow_id: str,
    trigger: str = "manual",
    cli_inputs: dict | None = None,
    dry_run: bool = False,
    output_override: dict | None = None,
    late_scheduled_at: str | None = None,
    timeout_override: str | None = None,
    attempt: int = 1,
    attempts: int = 1,
) -> dict:
    """Runs one workflow end to end through its eight stages: load/validate,
    checkpoint hand edits under lock, resolve inputs, render the prompt,
    run the model/tool-call loop, route the output, and write the run record.
    A pipeline workflow (wf.pipeline set) is delegated to _run_pipeline instead.
    Raises RunError on any stage failure, with a run record already persisted
    describing the failure. Returns the completed run record on success."""
    cli_inputs = cli_inputs or {}
    run_id = runs_mod.new_run_id()
    start = _now()
    record: dict = {
        "id": run_id, "workflow_id": workflow_id, "trigger": trigger,
        "start_time": start.isoformat(), "late": trigger == "late",
        # Marked on the record so a rehearsal is never mistaken for the real
        # thing: `runs list` labels it, and `runs rerun` refuses to replay it as
        # a live run without being told to.
        "dry_run": bool(dry_run),
        "attempt": attempt,
        "attempts": attempts,
    }
    runs_mod.mark_running(home, run_id, workflow_id)
    runs_mod.append_event(config, run_id, "run_started", workflow=workflow_id,
                          trigger=trigger, dry_run=bool(dry_run),
                          attempt=attempt, attempts=attempts)

    def fail(message: str, **extra) -> "RunError":
        # finalizes and persists the run record as a failure, then hands back
        # a RunError carrying that record for the caller to raise
        runs_mod.clear_running(home, run_id)
        end = _now()
        record.update(outcome="failed", error=message, end_time=end.isoformat(),
                       duration_seconds=(end - start).total_seconds(), **extra)
        runs_mod.write_record(config, record)
        runs_mod.append_event(config, run_id, "run_finished", outcome="failed",
                              error=message[:500],
                              duration_seconds=(end - start).total_seconds(),
                              stage=extra.get("stage"))
        return RunError(message, record)

    # Stage 0: is this store allowed to spend anything more today
    from px0 import analysis as analysis_mod  # deferred: analysis reads runs

    exhausted = analysis_mod.over_budget(config)
    if exhausted and trigger != "manual":
        # Manual runs are never blocked. A budget is there to stop unattended
        # spending, and refusing a command the user just typed -- while they
        # are sitting there, able to see the cost -- is the wrong half of that.
        raise fail(f"daily budget reached: {exhausted}", stage="budget")

    # Stage 1: load and validate
    try:
        wf = workflow_mod.load(home, workflow_id)
    except workflow_mod.WorkflowError as e:
        raise fail(str(e))
    errors = workflow_mod.validate(wf, home)
    if errors:
        raise fail("; ".join(errors), stage="validate")
    # Which version of the workflow this run used. Without it a week of runs
    # reads as one population, when an edit halfway through means it is two --
    # and `px0 workflows health` would blame the current file for failures that
    # belong to the file it replaced.
    record["workflow_version"] = versioning.latest_version_number(
        home, f"workflows/{workflow_id}.md")

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
        raise fail(str(e), inputs_resolved=e.record.get("inputs_resolved", []),
                   stage="inputs")
    runs_mod.append_event(config, run_id, "inputs", resolved=inputs_meta)

    # Stage 4: render prompt
    # The body only: a guideline's frontmatter is how px0 finds the file, not
    # something the model needs, and spending prompt on it would be paying for
    # the index alongside the content.
    guideline_texts, guidelines_inlined = {}, []
    for g in wf.guidelines:
        guideline_texts[g] = guidelines_mod.body_of(home, g)
        guidelines_inlined.append({
            "path": g, "version": versioning.latest_version_number(home, f"guidelines/{g}")
        })
    # What px0 remembers about the user, chosen against this workflow's own
    # words. Recorded on the run like guidelines are, so `px0 runs why` can
    # say a run acted on a memory -- which is the first thing you want when a
    # run behaves in a way the instructions alone do not explain.
    memories, memory_block = [], ""
    if memory_mod.enabled(config):
        query = f"{wf.description} {wf.request} {wf.body}"
        memories = memory_mod.relevant(home, query,
                                       budget=memory_mod.budget_chars(config))
        memory_block = memory_mod.as_prompt_block(memories)
    prompt = render_prompt(wf, guideline_texts, context, memory_block)
    # Keep what this run read, where the workflow asked for it. That fixture is
    # what lets a later revision be judged against the same world rather than
    # against next week's.
    if replay_mod.capture_enabled(config, wf):
        captured = replay_mod.capture(home, workflow_id, run_id, context, prompt)
        record["captured_inputs"] = bool(captured)
        runs_mod.append_event(config, run_id, "inputs_captured",
                              ok=bool(captured))

    # Stage 5 + 6: per-run tool loop, invoke model backend
    try:
        timeout = harness.parse_duration(timeout_override or wf.timeout)
    except ValueError:
        raise fail(f"invalid timeout: {timeout_override or wf.timeout!r}", stage="timeout")
    mode = agent_loop_mode(config)
    harness_cmd = harness.resolve_harness_cmd(
        config_mod.get(config, "model.harness_cmd", "claude -p"))
    use_agent = bool(wf.tools) and (
        mode == "mcp" or (mode == "auto" and harness.supports_agent_loop(harness_cmd)))
    try:
        if use_agent:
            try:
                output_text, tool_calls, usage = _agent_loop(
                    home, config, prompt, wf.tools, dry_run, timeout, run_id, wf)
            except harness.AgentLoopUnsupported:
                # `auto` means "where it works"; an explicit 'mcp' means the
                # user asked for it, and silently running a weaker loop would
                # hide that their setting does nothing.
                if mode == "mcp":
                    raise
                output_text, tool_calls, usage = _tool_call_loop(
                    home, config, prompt, wf.tools, dry_run, timeout, run_id, wf=wf)
        else:
            output_text, tool_calls, usage = _tool_call_loop(
                home, config, prompt, wf.tools, dry_run, timeout, run_id, wf=wf
            )
    except harness.HarnessError as e:
        raise fail(str(e), inputs_resolved=inputs_meta,
                   guidelines_inlined=guidelines_inlined, stage="model",
                   tool_calls=getattr(e, "tool_calls", []))

    # Stage 7: route output
    effective_output = output_override or wf.output or {"target": "stdout"}
    note = None
    if late_scheduled_at:
        note = f"scheduled {late_scheduled_at}, ran {_now().strftime('%H:%M')}"
    output_info = route_output(home, effective_output, output_text, note)
    # The drafts this run queued were written before it had an answer; now it
    # has one, and that is what a person needs in order to judge them.
    queued_approvals = usage.get("approvals") or []
    if queued_approvals:
        approvals.attach_output(home, run_id, output_text or "")
        if trigger != "manual":
            # A drafted call waits indefinitely by definition. Nobody was there
            # to see this run happen, so an approval nobody is told about is a
            # message the user believes went out.
            from px0 import notify as notify_mod

            record["approval_notified"] = notify_mod.on_approval(
                home, config, {**record, "workflow_id": workflow_id},
                queued_approvals, wf.on_failure)
    # Delivery is on top of routing, not instead of it: a nightly digest still
    # writes its file, and the inbox is what tells you the file exists.
    if inbox_mod.should_deliver(config, wf, trigger, dry_run):
        entry = inbox_mod.deliver(
            home, config, workflow_id=workflow_id, run_id=run_id,
            text=output_text or "", path=output_info.get("path"), trigger=trigger)
        output_info["inbox_id"] = entry["id"]
    runs_mod.append_event(config, run_id, "output",
                          target=output_info.get("target"),
                          path=output_info.get("path"),
                          inbox=output_info.get("inbox_id"),
                          chars=len(output_text or ""))

    # Stage 8: close the run record
    end = _now()
    record.update(
        inputs_resolved=inputs_meta,
        guidelines_inlined=guidelines_inlined,
        memories_inlined=[{"name": m.name, "subject": m.subject} for m in memories],
        tool_calls=tool_calls,
        approvals=usage.pop("approvals", []),
        model=config_mod.get(config, "model.harness_cmd"),
        usage=usage,
        outcome="success",
        output=output_info,
        end_time=end.isoformat(),
        duration_seconds=(end - start).total_seconds(),
    )
    runs_mod.clear_running(home, run_id)
    runs_mod.write_record(config, record)
    runs_mod.append_event(config, run_id, "run_finished", outcome="success",
                          duration_seconds=record["duration_seconds"],
                          usage=usage, tool_calls=len(tool_calls))
    return record


def _stage_should_run(when: str, previous_output: str) -> bool:
    """Whether a pipeline stage's condition is met by what came before it.

    Two conditions, both facts px0 can check itself. Anything richer would be
    a small language living in frontmatter, and the place for judgement about
    what to do next is a workflow body, which is written in English and read
    by a model.
    """
    if when == "has_output":
        return bool((previous_output or "").strip())
    if when == "no_output":
        return not (previous_output or "").strip()
    return True


def _run_pipeline(
    home: Path, config: dict, wf: workflow_mod.Workflow, trigger: str,
    dry_run: bool, run_id: str, start: datetime, record: dict
) -> dict:
    """Runs each workflow in wf.pipeline in sequence, piping one stage's
    output text into the next stage's stdin, with only the final stage's
    output routed to its real destination (intermediate stages route to
    memory). Any stage failure aborts the pipeline and persists a failed
    record carrying the stages completed so far. Returns the parent run
    record with `stages` set to the list of child run records."""
    stages = []
    stdin_text = ""
    planned = workflow_mod.pipeline_stages(wf)
    runs_mod.append_event(config, run_id, "pipeline_started",
                          stages=[s["workflow"] for s in planned],
                          dry_run=bool(dry_run))
    skipped = []
    for i, stage in enumerate(planned):
        stage_id = stage["workflow"]
        is_last = i == len(planned) - 1
        if not _stage_should_run(stage["when"], stdin_text):
            # A stage whose condition is not met is skipped, not failed, and
            # the text it would have received passes through to the next one --
            # so "post it only if there is something to post" does not also
            # break every stage after it.
            skipped.append({"workflow": stage_id, "when": stage["when"]})
            runs_mod.append_event(config, run_id, "stage_skipped",
                                  stage=stage_id, when=stage["when"])
            continue
        stage_override = (wf.output or {"target": "stdout"}) if is_last else {"target": "memory"}
        try:
            stage_record = run(
                home, config, stage_id, trigger="pipeline",
                cli_inputs={"_stdin": stdin_text}, dry_run=dry_run,
                output_override=stage_override, retry=False,
            )
        except RunError as e:
            end = _now()
            record.update(outcome="failed", error=f"stage {stage_id!r} failed: {e}",
                           stages=stages, end_time=end.isoformat(),
                           duration_seconds=(end - start).total_seconds())
            # Released here as well as on the success path: a pipeline never
            # cleared its in-flight marker, and inside the daemon -- a process
            # that outlives the run -- the pid stayed alive, so the run showed
            # in `px0 runs list --running` forever. A one-shot CLI hid this,
            # because `list_running` drops markers whose process is gone.
            runs_mod.clear_running(home, run_id)
            runs_mod.write_record(config, record)
            runs_mod.append_event(config, run_id, "run_finished", outcome="failed",
                                  error=str(e)[:500], stage=stage_id,
                                  stages_completed=len(stages))
            raise RunError(str(e), record)
        stage_record["parent_run_id"] = run_id
        stages.append(stage_record)
        stdin_text = stage_record["output"].get("text", "")
        runs_mod.append_event(config, run_id, "stage_finished", stage=stage_id,
                              stage_run=stage_record.get("id"),
                              chars=len(stdin_text or ""))

    end = _now()
    final_output = stages[-1]["output"] if stages else {}
    # A pipeline routes its last stage's output and then had nowhere to say so.
    # Every reason the inbox exists applies here more than anywhere -- a
    # pipeline is the longest-running thing px0 does and the least likely to
    # have anyone watching when it finishes.
    if inbox_mod.should_deliver(config, wf, trigger, dry_run):
        entry = inbox_mod.deliver(
            home, config, workflow_id=wf.id, run_id=run_id,
            text=final_output.get("text", ""), path=final_output.get("path"),
            trigger=trigger)
        final_output = {**final_output, "inbox_id": entry["id"]}
    record.update(
        outcome="success", stages=stages, skipped=skipped,
        output=final_output,
        end_time=end.isoformat(), duration_seconds=(end - start).total_seconds(),
    )
    runs_mod.clear_running(home, run_id)
    runs_mod.write_record(config, record)
    runs_mod.append_event(config, run_id, "run_finished", outcome="success",
                          duration_seconds=record["duration_seconds"],
                          stages_completed=len(stages))
    return record
