"""px0 as an MCP server, over stdio.

px0 already holds two things a coding agent wants: a brain that can answer
questions from what you have read, and a set of workflows that can be run. A
CLI can be shelled out to, but nothing tells an agent that these exist or what
they take. This does, speaking the Model Context Protocol over stdin and
stdout, so any MCP client can list and call them.

Deliberately small: no dependency on an MCP SDK, because the protocol surface
needed here is `initialize`, `tools/list`, and `tools/call`, and a stdio server
that speaks JSON-RPC is a hundred lines. Writes stay behind an explicit flag --
an agent should not be able to fire a workflow that posts to Slack because it
was curious what px0 could do.
"""

import json
import re
import sys

PROTOCOL_VERSION = "2024-11-05"
SERVER_NAME = "px0"

# MCP tool names are identifiers; px0 tool ids are dotted (`github.list_prs`).
# The mapping is deterministic in both directions, and the scope carries it, so
# a run's records still name the tool the user allowlisted rather than the
# mangled form the protocol needed.
_NAME_SAFE = re.compile(r"[^A-Za-z0-9_-]")


def mcp_name(tool_id: str) -> str:
    """The MCP-safe name for a px0 tool id."""
    return _NAME_SAFE.sub("_", tool_id)


def _json_schema(params: dict) -> dict:
    """Turns px0's compact param spec (`{"path": "str*"}`) into JSON Schema.

    A trailing `*` marks a required parameter, which is the only piece of the
    notation a schema needs that the type alone does not carry.
    """
    types = {"str": "string", "int": "integer", "number": "number",
             "bool": "boolean", "object": "object", "list": "array",
             "array": "array"}
    properties, required = {}, []
    for name, raw in (params or {}).items():
        spec = str(raw)
        if spec.endswith("*"):
            required.append(name)
            spec = spec[:-1]
        properties[name] = {"type": types.get(spec.strip(), "string")}
    schema = {"type": "object", "properties": properties}
    if required:
        schema["required"] = required
    return schema


def _text(content: str) -> dict:
    return {"content": [{"type": "text", "text": content}], "isError": False}


def _error(message: str) -> dict:
    return {"content": [{"type": "text", "text": message}], "isError": True}


def tool_definitions(allow_runs: bool) -> list[dict]:
    """The tools this server exposes, as MCP tool descriptors."""
    defs = [
        {
            "name": "brain_ask",
            "description": "Answer a question from the user's px0 brain: what they have read "
                           "and kept. Cites the files it used.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "question": {"type": "string", "description": "The question to answer"},
                    "kind": {"type": "string",
                             "description": "Restrict to one kind: blog, paper, doc, video, stub"},
                    "k": {"type": "integer", "description": "How many passages to retrieve"},
                },
                "required": ["question"],
            },
        },
        {
            "name": "brain_search",
            "description": "Retrieve matching passages from the px0 brain without asking a "
                           "model to summarize them.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "query": {"type": "string"},
                    "kind": {"type": "string"},
                    "k": {"type": "integer"},
                },
                "required": ["query"],
            },
        },
        {
            "name": "workflows_list",
            "description": "List the px0 workflows in this store, with their schedule and "
                           "what each one does.",
            "inputSchema": {"type": "object", "properties": {}},
        },
        {
            "name": "guidelines_list",
            "description": "List the user's px0 guidelines with what each one covers, "
                           "so you can tell which conventions apply before reading one.",
            "inputSchema": {"type": "object", "properties": {}},
        },
        {
            "name": "guideline_read",
            "description": "Read one px0 guideline verbatim, to follow it.",
            "inputSchema": {
                "type": "object",
                "properties": {"name": {"type": "string"}},
                "required": ["name"],
            },
        },
    ]
    if allow_runs:
        defs.append({
            "name": "workflow_run",
            "description": "Run a px0 workflow. This can post, send, and file things through "
                           "the tools that workflow declares.",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "workflow": {"type": "string"},
                    "dry_run": {"type": "boolean",
                                "description": "Resolve inputs and call nothing"},
                },
                "required": ["workflow"],
            },
        })
    return defs


def call_tool(home, config, name: str, args: dict, allow_runs: bool) -> dict:
    """Runs one exposed tool and returns an MCP tool result."""
    from px0 import ask as ask_mod, retrieval, workflow as workflow_mod

    args = args or {}
    try:
        if name == "brain_ask":
            question = str(args.get("question") or "").strip()
            if not question:
                return _error("brain_ask needs a question")
            k = int(args.get("k") or 0) or 5
            answer = ask_mod.ask(home, config, question, k=k, kind=args.get("kind"))
            text = str(answer.get("answer") or "").strip()
            cited = sorted({p.path for p in answer.get("passages") or []})
            if cited:
                text = f"{text}\n\nFrom:\n" + "\n".join(f"- {c}" for c in cited)
            return _text(text or "nothing in the brain answers that")

        if name == "brain_search":
            query = str(args.get("query") or "").strip()
            if not query:
                return _error("brain_search needs a query")
            k = int(args.get("k") or 0) or 5
            passages = retrieval.retrieve(home, config, query, k, kind=args.get("kind"))
            if not passages:
                return _text("no matching passages")
            return _text("\n\n".join(f"[{p.path}#{p.anchor}]\n{p.text}" for p in passages))

        if name == "workflows_list":
            rows = []
            for wf in sorted(workflow_mod.load_all(home).values(), key=lambda w: w.id):
                schedule = (wf.trigger or {}).get("schedule") or "on demand"
                state = "" if wf.enabled else " (disabled)"
                rows.append(f"- {wf.id}{state}: {wf.description or 'no description'} [{schedule}]")
            return _text("\n".join(rows) or "no workflows in this store")

        if name == "guidelines_list":
            # Path and description, the same pair a build chooses from: a caller
            # deciding which convention applies needs the description, and a
            # bare filename list also lost every guideline in a subfolder.
            from px0 import guidelines as guidelines_mod

            rows = [f"- {rel}: {g.summary or 'no description'}"
                    for rel, g in guidelines_mod.load_all(home).items()]
            return _text("\n".join(rows) or "no guidelines in this store")

        if name == "guideline_read":
            raw = str(args.get("name") or "").strip()
            if not raw:
                return _error("guideline_read needs a name")
            from px0 import authoring

            path = authoring.guideline_path(home, raw)
            if not path.exists():
                return _error(f"no guideline named {raw}")
            return _text(path.read_text())

        if name == "workflow_run":
            if not allow_runs:
                return _error("running workflows is not enabled on this server; "
                              "start it with `px0 mcp serve --allow-runs`")
            from px0 import runner

            workflow_id = str(args.get("workflow") or "").strip()
            if not workflow_id:
                return _error("workflow_run needs a workflow id")
            record = runner.run(home, config, workflow_id, trigger="mcp",
                                 dry_run=bool(args.get("dry_run")),
                                 output_override={"target": "memory"})
            return _text(record.get("output", {}).get("text") or "(no output)")
    except Exception as e:
        return _error(f"{name} failed: {e}")

    return _error(f"unknown tool: {name}")


# --- run scope: px0's own tools, handed to the harness's agent loop --------
#
# px0's builtin loop drives the model turn by turn over a text protocol, which
# caps a run at `runs.max_tool_turns` and re-sends the whole conversation each
# time. The harness is itself an agent with a real tool-calling loop, so the
# alternative is to stop driving it: expose exactly this workflow's allowlisted
# tools over MCP and let it work.
#
# Everything px0 enforces still holds, because it is enforced here rather than
# in the loop that was removed: a tool outside the allowlist is refused, a
# write is stubbed on a dry run, a held-back write is queued for approval, and
# every call lands in the run's event stream.


def scope_tool_definitions(home, scope: dict) -> list[dict]:
    """MCP descriptors for exactly the tools one run may call."""
    from px0 import tools as tools_mod

    defs = []
    for tool_id in scope.get("tools") or []:
        spec = tools_mod.resolve(tool_id, home)
        if spec is None:
            continue
        note = " (this call is held for the user's approval before it fires)" \
            if tool_id in set(scope.get("confirm_tools") or []) else ""
        defs.append({
            "name": mcp_name(tool_id),
            "description": f"{spec.description}{note}",
            "inputSchema": _json_schema(spec.params),
        })
    return defs


def _append_scope_call(scope: dict, entry: dict) -> None:
    """Records one call where the run that spawned this server can read it back.

    The server is a separate process -- the harness starts it, not px0 -- so
    the run cannot observe these calls directly. This sidecar is how a run
    reconstructs what its own tools did.
    """
    path = scope.get("calls_path")
    if not path:
        return
    try:
        with open(path, "a") as f:
            f.write(json.dumps(entry, default=str) + "\n")
    except (OSError, TypeError, ValueError):
        pass


def call_scoped(home, config, scope: dict, name: str, args: dict) -> dict:
    """Runs one tool on behalf of a scoped run, enforcing everything px0 does.

    The allowlist is checked against the ids in the scope rather than against
    the name the client sent, so a client that invents an MCP tool name gets a
    refusal and not a call.
    """
    from px0 import approvals as approvals_mod
    from px0 import runs as runs_mod
    from px0 import tools as tools_mod

    args = args or {}
    by_name = {mcp_name(t): t for t in scope.get("tools") or []}
    tool_id = by_name.get(name)
    run_id = scope.get("run_id")

    if tool_id is None:
        if run_id:
            runs_mod.append_event(config, run_id, "tool_refused", tool=name,
                                  allowed=list(scope.get("tools") or []))
        _append_scope_call(scope, {"tool": name, "refused": True, "failed": True,
                                   "is_write": False,
                                   "result_summary": "not in this workflow's tools"})
        return _error(f"{name} is not in this workflow's tools: allowlist")

    try:
        is_write = tools_mod.is_write(tool_id, home)
    except KeyError:
        is_write = False

    if scope.get("dry_run") and is_write:
        _append_scope_call(scope, {"tool": tool_id, "is_write": True,
                                   "stubbed": True, "failed": False,
                                   "args": args, "result_summary": "stubbed"})
        return _text("stubbed: this is a dry run, so the call was not made")

    if is_write and tool_id in set(scope.get("confirm_tools") or []):
        approval = approvals_mod.queue(
            home, run_id=run_id or "", workflow_id=scope.get("workflow_id", ""),
            tool=tool_id, args=args, reason=scope.get("reason", ""))
        if run_id:
            runs_mod.append_event(config, run_id, "approval_queued", tool=tool_id,
                                  approval=approval["id"])
        _append_scope_call(scope, {"tool": tool_id, "is_write": True,
                                   "queued": True, "failed": False, "args": args,
                                   "approval": approval["id"],
                                   "result_summary": "queued for approval"})
        return _text("drafted and shown to the user for approval; it has not "
                     "been sent. Write your answer as though it will be.")

    try:
        result = tools_mod.call(home, config, tool_id, args)
    except Exception as e:
        if run_id:
            runs_mod.append_event(config, run_id, "tool_call", tool=tool_id,
                                  is_write=is_write, failed=True,
                                  error=str(e)[:300])
        _append_scope_call(scope, {"tool": tool_id, "is_write": is_write,
                                   "failed": True, "args": args,
                                   "result_summary": f"error: {e}"[:500]})
        return _error(f"{tool_id} failed: {e}")

    if run_id:
        runs_mod.append_event(config, run_id, "tool_call", tool=tool_id,
                              is_write=is_write, failed=False,
                              arg_keys=sorted(args.keys())
                              if isinstance(args, dict) else None)
    _append_scope_call(scope, {"tool": tool_id, "is_write": is_write,
                               "failed": False, "args": args,
                               "result_summary": str(result)[:500]})
    return _text(json.dumps(result, default=str)[:20000])


def handle(home, config, message: dict, allow_runs: bool, scope: dict | None = None) -> dict | None:
    """Handles one JSON-RPC message. Returns the response, or None for a notification."""
    method = message.get("method")
    msg_id = message.get("id")

    if method == "initialize":
        return {
            "jsonrpc": "2.0", "id": msg_id,
            "result": {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {"tools": {}},
                "serverInfo": {"name": SERVER_NAME, "version": _version()},
            },
        }
    if method in ("notifications/initialized", "initialized"):
        return None
    if method == "ping":
        return {"jsonrpc": "2.0", "id": msg_id, "result": {}}
    if method == "tools/list":
        tools = (scope_tool_definitions(home, scope) if scope
                 else tool_definitions(allow_runs))
        return {"jsonrpc": "2.0", "id": msg_id, "result": {"tools": tools}}
    if method == "tools/call":
        params = message.get("params") or {}
        name, arguments = params.get("name", ""), params.get("arguments") or {}
        # A scoped server serves one run and exposes nothing else -- not the
        # brain, not other workflows -- so a run cannot reach past its own
        # allowlist by way of the server that was started for it.
        result = (call_scoped(home, config, scope, name, arguments) if scope
                  else call_tool(home, config, name, arguments, allow_runs))
        return {"jsonrpc": "2.0", "id": msg_id, "result": result}
    if msg_id is None:
        return None
    return {"jsonrpc": "2.0", "id": msg_id,
            "error": {"code": -32601, "message": f"method not found: {method}"}}


def _version() -> str:
    try:
        from px0 import __version__

        return str(__version__)
    except Exception:
        return "0"


def serve(home, config, allow_runs: bool = False, stdin=None, stdout=None,
          scope: dict | None = None) -> None:
    """Reads JSON-RPC messages from stdin and writes responses to stdout.

    One message per line, which is what MCP's stdio transport sends. Anything
    unparseable is answered rather than crashed on: a client that gets no reply
    hangs, which is worse than an error it can report.
    """
    stdin = stdin or sys.stdin
    stdout = stdout or sys.stdout

    for line in stdin:
        line = line.strip()
        if not line:
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            stdout.write(json.dumps({
                "jsonrpc": "2.0", "id": None,
                "error": {"code": -32700, "message": "parse error"},
            }) + "\n")
            stdout.flush()
            continue
        try:
            response = handle(home, config, message, allow_runs, scope)
        except Exception as e:
            response = {"jsonrpc": "2.0", "id": message.get("id"),
                        "error": {"code": -32603, "message": str(e)}}
        if response is not None:
            stdout.write(json.dumps(response, default=str) + "\n")
            stdout.flush()
