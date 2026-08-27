# 7. The harness layer

Module: `px0/harness.py`

px0 has no model API client. It shells out to a coding agent CLI you already sign into, sends a prompt, and reads text back. This part covers what that costs and how the module absorbs it.

## Why a subprocess

Four consequences follow from having no direct API backend, and all of them are features:

- px0 never holds a provider key. Each CLI manages its own authentication and model choice, so there is nothing for px0 to store, rotate, or leak.
- The user's existing subscription and rate limits apply. There is no second bill.
- Switching backends is one config key.
- The failure mode of an unauthenticated backend is that CLI's own error, which the user can act on.

The cost is that the interface is text in, text out, over a process boundary, with no schema. Everything below is about making that reliable.

## The known harnesses

```python
KNOWN_HARNESSES = {
    "claude": "claude -p",
    "gemini": "gemini -p",
    "pi": "pi -p",
    "opencode": "opencode run",
}
```

Each entry is a command prefix that, with the prompt appended as the final argument, prints the reply to stdout and exits. Verified against each CLI's own `--help`.

`model.harness_cmd` accepts a known name, which `resolve_harness_cmd` expands, or any literal command. `harness_name` matches on the command's first word with the directory stripped, so `claude -p --model x`, a bare `claude`, and `/usr/local/bin/claude` all answer `claude`. A user who pinned the binary by path still gets its flags.

`AUTH_HINTS` carries one line per harness saying how to authenticate it, which is what `px0 doctor` prints when a harness does not respond.

## Capability tables

px0 adds flags to the invocation for things the base contract does not cover. Three tables say which harnesses support what, and all three are keyed by harness name:

| Table | What it adds | Currently |
| ----- | ------------ | --------- |
| `STRUCTURED_FLAGS` | A JSON envelope around the reply | `claude` |
| `VERBOSE_FLAGS` | Narration of what the backend is doing | `claude` |
| `MCP_FLAGS` | An MCP server plus a tool allowlist | `claude` |

Only harnesses whose flags are verified against their own `--help` appear. An entry that guessed wrong would break every run for that backend; a missing entry only costs the extra detail. A custom `harness_cmd` always lands in the empty case, because px0 will not invent flags for a command it does not recognize.

The structured envelope matters for more than tidiness. Without it, a run's cost is inferred from character counts. With it, the harness hands over the token counts it was actually billed for. That is the difference between `runs.daily_budget_usd` enforcing a bill and enforcing a guess.

## One invocation

`invoke_detailed(config, prompt, timeout, extra_flags)` returns a `Reply`:

| Field | What it carries |
| ----- | --------------- |
| `text` | The answer |
| `raw_stdout` | Everything the process printed |
| `stderr` | Everything it printed on the error stream |
| `exit_code` | The process's exit code |
| `elapsed_seconds` | Wall clock for the call |
| `argv` | The command, with the prompt stripped off the end |
| `output_format` | `text` or `json`, after any downgrade |
| `usage` | Token counts, when the harness reported them |
| `meta` | Session id, model, cost, and any note about what px0 had to work around |

`invoke` is the thin wrapper returning only `text`. Everything that just wants a reply -- the builder, `px0 ask`, brain summarization -- uses that. Runs use `invoke_detailed`, because a run records what the call cost.

## Three things that go wrong, and what happens

### The prompt is too long to exec

A run's conversation grows with every tool result folded back in. Past a certain size the OS refuses to exec the command at all, with `OSError: [Errno 7] Argument list too long`, well before any output limit is reached.

```python
except OSError as e:
    if e.errno != errno.E2BIG:
        raise HarnessError(...)
    argv = list(argv_prefix)
    done = _run(argv, prompt, timeout, harness_cmd)
    meta["stdin_prompt"] = True
```

The same prompt is piped to stdin with no positional argument instead, which is how `claude -p` and `pi -p` are documented to accept one. The fallback is recorded in `meta` rather than being silent.

### The harness is older than a flag px0 added

The process exits non-zero complaining about an unknown option. That is retried once with the reporting flags stripped, and the harness is remembered for the life of the process so the downgrade is paid for once rather than on every turn of every run.

```python
_UNKNOWN_FLAG_MARKERS = (
    "unknown option", "unknown argument", "unknown flag",
    "unrecognized option", "unrecognised option", "unrecognized argument",
    "invalid option", "bad flag", "unexpected argument",
)
```

`_looks_like_unknown_flag` is deliberately conservative. A false positive would retry a genuinely failed model call as if the flag were at fault, and report the second failure instead of the first.

Agent flags are excluded from this downgrade. A harness that rejects `--mcp-config` cannot run the agent loop at all, and silently retrying without it would run the workflow with no tools and report success.

### The envelope is a shape px0 cannot read

`_parse_structured` tolerates three shapes, because the envelope is another program's output and may change: a single JSON object, a JSON array, and newline-delimited JSON.

The answer is the last object that actually carries one, searched across `result`, `response`, `text`, `content`, and `output`. A stream ends with its result and the objects before it are progress narration, so reading backwards finds the answer rather than the first status line.

Usage is taken from the last object carrying a `usage` dict. Metadata is collected across all objects for a fixed set of keys: `session_id`, `num_turns`, `total_cost_usd`, `model`, `duration_ms`, `subtype`, `is_error`, `stop_reason`.

Anything unparseable returns `(None, ...)`, and the caller falls back to treating stdout as plain text with `meta["unparsed_envelope"] = True`. Telemetry is never worth failing a run over.

## Agent flags

`agent_flags(harness_cmd, config_path, tool_names)` renders the MCP flags for a harness, or returns `[]` for one px0 has no verified entry for -- which is the caller's signal to drive its own loop instead.

```python
MCP_FLAGS = {
    "claude": {
        "config": ["--mcp-config", "{config}"],
        "allow": ["--allowedTools", "{tools}"],
        "tool_prefix": "mcp__px0__",
        "separator": ",",
    },
}
```

Tool permissions are passed as one separated value in the `mcp__<server>__<tool>` form the client uses to name a server's tools. `supports_agent_loop` is just a membership test on that table.

## Durations

`parse_duration` handles `ms`, `s`, `m`, and `h` suffixes, treating a bare number as seconds. It is used for workflow timeouts and for `trigger.watch.every`, so both accept the same vocabulary.

## Failure surface

`HarnessError` is raised when the binary is missing, the call times out, or it exits non-zero for any reason other than a flag it did not recognize. `AgentLoopUnsupported` subclasses it for the one case a caller may want to catch specifically.

`cli.main` maps `HarnessError` to exit code 3, so a script can tell a model failure apart from a connector failure and from a bad argument.

## Next

[Part 8](08-tools.md) covers the other side of a run: what a workflow may call.
