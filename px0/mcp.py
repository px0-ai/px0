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
import sys

PROTOCOL_VERSION = "2024-11-05"
SERVER_NAME = "px0"


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


def handle(home, config, message: dict, allow_runs: bool) -> dict | None:
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
        return {"jsonrpc": "2.0", "id": msg_id,
                "result": {"tools": tool_definitions(allow_runs)}}
    if method == "tools/call":
        params = message.get("params") or {}
        result = call_tool(home, config, params.get("name", ""), params.get("arguments") or {},
                           allow_runs)
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


def serve(home, config, allow_runs: bool = False, stdin=None, stdout=None) -> None:
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
            response = handle(home, config, message, allow_runs)
        except Exception as e:
            response = {"jsonrpc": "2.0", "id": message.get("id"),
                        "error": {"code": -32603, "message": str(e)}}
        if response is not None:
            stdout.write(json.dumps(response, default=str) + "\n")
            stdout.flush()
