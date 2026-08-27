# 15. The MCP surface

Module: `px0/mcp.py`

One module, two servers with different purposes.

`px0 mcp serve` exposes the brain and the workflow list to any MCP client, so a coding agent can ask what you have read. The scoped server is started by a run, for a run, and exposes exactly that run's allowlisted tools so the harness can drive its own agent loop.

## Why no SDK

The protocol surface needed here is `initialize`, `tools/list`, and `tools/call`. A stdio server speaking JSON-RPC over those three is about a hundred lines, and a dependency for it would buy nothing.

```python
PROTOCOL_VERSION = "2024-11-05"
SERVER_NAME = "px0"
```

`serve` reads one JSON message per line, dispatches through `handle`, and writes one response per line. Anything unparseable is answered with a JSON-RPC parse error rather than crashed on: a client that gets no reply hangs, which is worse than an error it can report. Notifications (`notifications/initialized`, and anything with no id) return `None` and produce no output.

## The public server

`tool_definitions(allow_runs)` returns five tools, plus a sixth behind a flag.

| Tool | What it does |
| ---- | ------------ |
| `brain_ask` | Answer a question from the brain, citing the files used |
| `brain_search` | Return matching passages without asking a model to summarize them |
| `workflows_list` | List the workflows with their schedule and description |
| `guidelines_list` | List guidelines with what each one covers |
| `guideline_read` | Read one guideline verbatim, to follow it |
| `workflow_run` | Run a workflow; requires `--allow-runs` |

Writes stay behind an explicit flag. An agent should not be able to fire a workflow that posts to Slack because it was curious what px0 could do. Calling `workflow_run` on a server started without the flag returns an error naming the flag rather than pretending the tool does not exist.

Two details in the listing tools are worth noting.

`guidelines_list` returns path and description, the same pair a build chooses from. A caller deciding which convention applies needs the description, and a bare filename list also lost every guideline in a subfolder.

`brain_ask` appends the cited paths under a `From:` heading, because a client that gets an answer with no sources cannot check it.

Every handler is wrapped, so a failure comes back as an MCP error result rather than killing the server.

## The scoped server

px0's builtin loop drives the model turn by turn over a text protocol, which caps a run at `runs.max_tool_turns` and re-sends the whole conversation each time. The harness is itself an agent with a real tool-calling loop, so the alternative is to stop driving it: expose exactly this workflow's allowlisted tools over MCP and let it work.

`runner._agent_loop` writes a scope file and starts the harness with an MCP config pointing at `px0 mcp serve --scope <path>`.

```json
{
  "run_id": "run_20260827-090000-a1b2",
  "workflow_id": "friday-pr-digest",
  "reason": "Summarize the PRs I reviewed and post to #eng",
  "tools": ["github.list_my_prs", "slack.post_message"],
  "confirm_tools": ["slack.post_message"],
  "dry_run": false,
  "calls_path": "/tmp/px0-scope-xyz/calls.jsonl"
}
```

A scoped server serves one run and exposes nothing else -- not the brain, not other workflows -- so a run cannot reach past its own allowlist by way of the server that was started for it.

### Everything px0 enforces still holds

The enforcement did not disappear when the turn loop did. It moved into `call_scoped`, which is one place rather than one per turn:

```python
by_name = {mcp_name(t): t for t in scope.get("tools") or []}
tool_id = by_name.get(name)
if tool_id is None:
    ...refuse

if scope.get("dry_run") and is_write:
    ...stub

if is_write and tool_id in set(scope.get("confirm_tools") or []):
    ...queue for approval
```

The allowlist is checked against the ids in the scope, not against the name the client sent, so a client that invents an MCP tool name gets a refusal and not a call.

A held-back write returns text the model can act on:

> drafted and shown to the user for approval; it has not been sent. Write your answer as though it will be.

Same posture as the builtin loop: the run finishes and produces its output while the thing that would leave a mark waits for a person.

### Name mangling

MCP tool names are identifiers; px0 tool ids are dotted.

```python
_NAME_SAFE = re.compile(r"[^A-Za-z0-9_-]")

def mcp_name(tool_id: str) -> str:
    return _NAME_SAFE.sub("_", tool_id)
```

The mapping is deterministic in both directions and the scope carries it, so a run's records still name the tool the user allowlisted rather than the mangled form the protocol needed.

### Parameter schemas

`_json_schema` expands px0's compact notation into JSON Schema. A trailing `*` marks a parameter required, which is the only piece of the notation the type alone does not carry:

```python
{"path": "str*", "limit": "int"}
->
{"type": "object",
 "properties": {"path": {"type": "string"}, "limit": {"type": "integer"}},
 "required": ["path"]}
```

A tool held for approval gets a note appended to its description, so the model knows the call will not fire immediately.

### The sidecar

The server runs in a process the harness started, not px0. The run cannot observe its calls directly.

`_append_scope_call` appends one JSON line per call to `calls_path`, and `runner._read_scope_calls` reads it back. That file is the only account a run has of what its own tools did.

It is written for every outcome -- refused, stubbed, queued, failed, executed -- so the run record is complete regardless of what happened. And it is read even when the harness dies mid-loop, because retention exempts runs that called a write tool and losing the record of a post would let its log be pruned as though nothing had happened.

Writing to the sidecar swallows `OSError`, `TypeError`, and `ValueError`. A telemetry file that cannot be written must not fail the tool call it was recording.

## Next

[Part 16](16-sync.md) covers moving a store between machines.
