# `px0 mcp`

Expose the brain and the workflow list over the Model Context Protocol, so a
coding agent can use them without being told how the CLI works.

px0 already holds two things an agent wants: a brain that can answer questions
from what you have read, and a set of workflows that can be run. A CLI can be
shelled out to; MCP means the agent can discover what is there and what each
thing takes.

Implemented by `px0/mcp.py`. No MCP SDK is involved — the surface needed is
`initialize`, `tools/list`, and `tools/call` over stdio.

```
px0 mcp serve [--allow-runs]
```

---

## `px0 mcp serve`

Speak MCP on stdin and stdout, one JSON-RPC message per line, until stdin
closes.

### `--allow-runs`

Let a client run workflows.

- **Input:** flag, no value. Default off.
- Off by default because a workflow can post, send, and file things. Without it
  `workflow_run` is not even listed, and calling it returns an error saying how
  to enable it.

```shell
px0 mcp serve
px0 mcp serve --allow-runs
```

## What it exposes

| Tool | What it does | Needs `--allow-runs` |
| ---- | ------------ | -------------------- |
| `brain_ask` | Answer a question from the brain, citing the files used | no |
| `brain_search` | Return matching passages, without a model call | no |
| `workflows_list` | Every workflow, with its schedule and whether it is disabled | no |
| `guidelines_list` | Every guideline file, with what each one covers | no |
| `guideline_read` | One guideline verbatim, to follow it | no |
| `workflow_run` | Run a workflow, optionally as a dry run | yes |

`brain_ask` and `brain_search` take an optional `kind` (`blog`, `paper`, `doc`,
`video`, `stub`) and `k`. Nothing under the brain's private folder is ever
returned.

## Registering it with a client

Claude Code, and most MCP clients, take a command and its arguments:

```json
{
  "mcpServers": {
    "px0": {
      "command": "px0",
      "args": ["mcp", "serve"],
      "env": { "PX0_HOME": "/Users/you/.px0" }
    }
  }
}
```

Set `PX0_HOME` when the client's environment differs from your shell's, since
that is what selects the store.

## Related configuration

| Key | Effect |
| --- | ------ |
| `retrieval.qmd_cmd` | Command prefix used to run the qmd CLI |
| `retrieval.k_default` | Passages retrieved when a call names no `k` |
| `model.harness_cmd` | The backend `brain_ask` asks to write the answer |

## Serving one run instead of the store

`px0 mcp serve --scope <file>` is a second mode, and it is not for people to
run: a workflow using `model.agent_loop` starts px0 through it so the harness
can be handed exactly that workflow's tools.

It changes what the server *is*. The store-wide server exposes the brain,
workflow listings, and guidelines. A scoped one exposes nothing but the tools
one workflow allowlisted — because a run reaching past its own allowlist by way
of the server started for it would defeat the allowlist entirely.

Everything px0 enforces still holds, moved into the server from the loop it
replaces:

- A tool outside the scope is refused, not called.
- A write is stubbed on a dry run.
- A held-back write is queued for [approval](approvals.md).
- Every call lands in the run's event stream, readable with `px0 runs events`.

See [`model.agent_loop`](../reference/configuration.md#model) for when to use it.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | stdin closed and the server stopped |
| `1` | No store, or an unknown subcommand |
