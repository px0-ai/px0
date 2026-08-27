# 6. Running a workflow

Module: `px0/runner.py`

A run is eight stages. Each one can fail, and every failure writes a run record describing what happened before it raises.

## The stages

| Stage | What happens |
| ----- | ------------ |
| 0 | Budget gate: is this store allowed to spend anything more today |
| 1 | Load and validate the workflow; record which version this run used |
| 2 | Take the store lock, checkpoint any hand edits, release |
| 3 | Resolve every declared input into a template context |
| 4 | Render the prompt: memory, guidelines, body |
| 5 and 6 | Drive the model and its tool calls |
| 7 | Route the output, deliver to the inbox, notify about held writes |
| 8 | Close the run record |

`_run_once` is that sequence. `run` wraps it with the retry policy.

## Stage 0: the budget

```python
exhausted = analysis_mod.over_budget(config)
if exhausted and trigger != "manual":
    raise fail(f"daily budget reached: {exhausted}", stage="budget")
```

A manual run is never blocked. A budget exists to stop unattended spending, and refusing a command the user just typed -- while they are sitting there, able to see the cost -- is the wrong half of that.

The ceiling is off by default. A watch on a busy source, a short poll interval, and a workflow that takes several model calls spends real money with nobody watching, which is the combination this guards.

## Stage 1: load and validate

`workflow.load` then `workflow.validate`. Validation errors are joined and recorded with `stage="validate"`.

The record then gets `workflow_version`, read from the version manifest. Without it, a week of runs reads as one population when an edit halfway through means it is two -- and `px0 workflows health` would blame the current file for failures that belong to the file it replaced.

A workflow with `pipeline:` set is handed to `_run_pipeline` here.

## Stage 2: the lock and the checkpoint

```python
with open(lock, "w") as lf:
    fcntl.flock(lf, fcntl.LOCK_EX)
    try:
        claims.scan_and_process(home)
    finally:
        fcntl.flock(lf, fcntl.LOCK_UN)
```

The run captures hand edits before reading anything, so a file edited five seconds ago is versioned before the run acts on it. The lock is held only for the scan, not for the whole run.

## Stage 3: resolving inputs

`resolve_inputs` starts by settling the workflow's declared vars, then walks every `InputSpec` in order and builds a context dict seeded with `{"config": config, "input": {**cli_inputs, **defaults}}`.

The var check comes first, and it comes before the network:

```python
filled, missing = workflow_mod.var_values(wf, cli_inputs)
if missing:
    raise RunError(workflow_mod.missing_vars_message(wf, missing),
                   {"inputs_resolved": []})
```

A [template](04-workflow-file.md#vars-and-what-makes-a-workflow-a-template) with a required var nobody supplied is refused here rather than resolving `{{input.repo}}` to `None` and asking a connector for a repository called nothing. GitHub answers that with a 404, which reads as a missing repository rather than as a template nobody filled in -- the same failure `input_arg_errors` was written to prevent, arriving by a different route.

A var with a `default` contributes it here, and a value passed with `--input` always wins over the default. Both reach the body as well as the arguments, because the body is rendered against this same context in stage 4.

`cli._fill_template_vars` asks for missing vars before the runner is called, but only when there is a person at a terminal: not with `--stdin` (which is already reading the stream the answers would come from), not with `--json` or `--quiet`, and not for a run the daemon spawned, which carries `--trigger`. The refusal above is what covers all of those, and the prompt only spares an interactive user having to read it once to learn what the flags were.

Each input's arguments are rendered against the context built so far, so an input may reference the ones above it. `_with_retry` wraps connector calls with exponential backoff up to `connectors.retries`, and never retries `ConnectorNotConfigured` -- that means the connector is not set up, not that the call failed transiently.

An optional input that fails resolves to `None` and is marked degraded. A required one raises `RunError` carrying the partial metadata.

Two facts are recorded per input that the obvious version omits:

```python
meta.append({"id": inp.id, "kind": inp.kind, "ok": True,
             "size": _value_size(value), "empty": _is_empty(value)})
```

"The input resolved" and "the input resolved to anything" are different facts, and only the first was ever written down. A digest whose GitHub query has quietly returned nothing for a month succeeds every time, with an empty prompt section the model then invents around. `_check_empty_inputs` in `analysis` reads these fields.

`_is_empty` understands envelopes. A tool that answers `{"successful": true, "items": []}` counts as empty, because the envelope is not the content:

```python
payloads = [v for k, v in value.items()
            if k not in ("successful", "success", "error", "ok", "status")]
```

### Sub-workflow inputs

An input with `workflow:` runs another workflow with `output_override={"target": "memory"}` and `retry=False`. The parent owns the retry decision; without that, a three-stage pipeline with three attempts each could run nine times.

## Stage 4: the prompt

`render_prompt` assembles three blocks in a fixed order:

```
memory       standing context about the user
guidelines   the rules the output is judged against
body         the job
```

Standing context about the user belongs above the rules for judging output, which belong above the job.

Both blocks can be placed explicitly. A body containing `{{memory}}` or `{{guidelines}}` gets the block substituted exactly there and not prepended.

Guideline bodies only are inlined. The frontmatter is how px0 finds the file; spending prompt on it would be paying for the index alongside the content. Guidelines are keyed by store-relative path (what provenance records) and headed by the guideline's name (what reads as a heading).

Memory is selected against this workflow's own words:

```python
query = f"{wf.description} {wf.request} {wf.body}"
memories = memory_mod.relevant(home, query, budget=memory_mod.budget_chars(config))
```

Both the guidelines and the memories are recorded on the run, with versions, so `px0 runs why` can say a run acted on a memory. That is the first thing you want when a run behaves in a way the instructions alone do not explain.

If capture is enabled, the resolved context and the rendered prompt are written to a fixture here. See [part 13](13-feedback.md).

## Stages 5 and 6: the two loops

px0 can drive the tool calls itself, or hand the tools to the harness and let it drive. `model.agent_loop` chooses:

| Value | Behaviour |
| ----- | --------- |
| `builtin` | px0's own turn loop, capped at `runs.max_tool_turns` |
| `mcp` | The harness runs its own loop over a scoped MCP server |
| `auto` | `mcp` where px0 has verified flags for this harness, `builtin` otherwise |

The default is `builtin`. The MCP loop removes the turn ceiling and is the better answer for anything taking more than a handful of steps, but it depends on flags belonging to another program, so it is opted into rather than assumed.

### The builtin loop

`_tool_call_loop` drives the model turn by turn over a text protocol. The harness is a plain non-interactive subprocess, not something wired to an MCP transport, so tool calls are requested as a line the model emits:

```
TOOL_CALL: {"tool": "<id>", "args": {...}}
```

Before the first turn, the allowlisted tools are described into the conversation with their parameters. A tool that is allowlisted but cannot be resolved is never mentioned to the model and gets a `tools_unresolved` event, because from every other angle it reads as "the model ignores that tool".

Each turn appends the call and its result back into the conversation and asks the model to continue. When a reply carries no `TOOL_CALL` line, that reply is the answer.

Four things can happen to a requested call:

- Refused, because the tool is not in the workflow's allowlist. This branch used to fall through into execution, so the refusal was only a message in the transcript while the call went ahead. The allowlist is the whole of what the user approved when the workflow was built, so it has to be the thing that decides, not a string in the conversation.
- Stubbed, because this is a dry run and the tool is a write.
- Queued, because the workflow holds this write for approval. The model is told plainly, so it writes its final answer as though the call will happen rather than reporting a failure the user would then have to interpret.
- Executed, with retries and a wall-clock measurement.

A malformed `TOOL_CALL` payload is treated as the final answer, with a `tool_call_malformed` event recorded rather than the run failing.

`MAX_TOOL_TURNS` defaults to 12. It was raised from 5, which was low enough that ordinary work hit it. Every turn resends the whole conversation, so the cost of a high ceiling is paid only by runs that use it, while the cost of a low one was paid by every run that needed a sixth step and silently stopped short.

Falling out of the loop sets `hit_turn_cap` and records a `turn_cap_reached` event. Recorded rather than inferred, because "always burns every turn" is the clearest sign a workflow is underspecified.

### The MCP agent loop

`_agent_loop` stops being the agent and becomes the tool provider.

It writes a scope file naming this run, its workflow, its allowlisted tools, which of them need approval, and whether this is a dry run. It writes an MCP config pointing at `px0 mcp serve --scope <file>`. It starts the harness once with that config and an allowlist of exactly this workflow's tools. Then it reads back what was called.

```python
server = {"mcpServers": {"px0": {
    "command": sys_mod.executable,
    "args": ["-m", "px0.cli", "mcp", "serve", "--scope", str(scope_path)],
    "env": {"PX0_HOME": str(home)},
}}}
```

`sys.executable -m px0.cli` rather than a bare `px0`, because a store driven from a virtualenv or a checkout may have no `px0` on the harness's PATH.

Every enforcement that lived in the turn loop moves into `mcp.call_scoped`: the allowlist, dry-run stubbing, held-back writes, and the event stream. One place, on every call. See [part 15](15-mcp.md).

The scoped server runs in a process the harness started, not px0, so the run cannot observe its calls directly. `_read_scope_calls` reads them back from a JSONL sidecar the server appends to. That file is the only account a run has of what its own tools did.

When the harness dies mid-loop, the calls it made before dying are attached to the exception:

```python
except harness.HarnessError as e:
    e.tool_calls, _ = _read_scope_calls(calls_path)
```

Losing that would understate what the run did in the one place it matters most: retention exempts runs that called a write tool, so a timeout after posting to Slack would let the log of that post be pruned as though nothing had happened.

If the harness has no verified way to be handed tools, `AgentLoopUnsupported` is raised. Under `auto` the run falls back to the builtin loop; under an explicit `mcp` it propagates, because silently running a weaker loop would hide that the user's setting does nothing.

## Usage accounting

Both loops return a usage block, and it is honest about where its numbers came from.

```python
usage = {"model_calls": 0, "prompt_chars": 0, "output_chars": 0,
         "estimated_tokens": 0, "estimated": True, "reported": False,
         "input_tokens": 0, "output_tokens": 0,
         "cost_usd": 0.0, "turns": 0, "hit_turn_cap": False}
```

The harness is another program, and only some of them report token counts. When one does -- `model.output_format` puts `claude -p` into its JSON envelope -- those counts are summed and the block says `reported`. When one does not, the cost is approximated at roughly four characters per token and labelled `estimated`, so a number nobody measured is never passed off as one that was.

`_fold_reported_usage` sums whatever integer fields the reply carried, under their own names:

```python
for key, value in reply.usage.items():
    if key == "reported" or isinstance(value, bool):
        continue
    if isinstance(value, (int, float)):
        usage[key] = usage.get(key, 0) + value
```

The shape is the harness's, not px0's, so a backend reporting something px0 has never heard of still gets it recorded rather than dropped. The moment any call reports real counts, the block stops calling itself an estimate.

## Stage 7: routing the output

`route_output` writes the output where it belongs and returns a description. It never prints, because stdout routing is a CLI decision and `--json` needs plain stdout free.

| Target | Behaviour |
| ------ | --------- |
| `stdout` | Returns the text; the CLI prints it |
| `memory` | Returns the text without writing; used by pipeline stages and sub-workflow inputs |
| `inbox` | Returns the text; the delivery below files it |
| `file` | Renders the path, confines it, writes under the store lock |

File writes take an exclusive `flock` so two concurrent runs cannot race on the same path.

### Confining the output path

Two functions guard the path, and both exist because of a specific failure.

`output_rel` puts a rendered path under `output/`. A plan writes `logs/daily.md` and means `output/logs/daily.md`; one that already said `outputs/` meant the same folder by a name the store does not use.

`_resolve_output_dest` then refuses anything that escapes:

```python
dest = (home / rendered).resolve()
root = paths.output_dir(home).resolve()
if root not in dest.parents:
    raise RunError(f"output.path escapes the store's output directory: ...")
```

An absolute path or a `..` segment used to escape the store entirely -- a run could write anywhere the user could, from a path a model wrote into the plan.

`output_destination` is the reporting half of the same logic: it answers where a run will put its output before the workflow has ever run, so what a build promises is where the run puts it.

### Approvals and the inbox

Drafted writes are queued mid-run, before the run has an answer. Once it has one, `approvals.attach_output` fills it in on every pending draft from this run. A Slack message shown without the digest it announces is a decision made blind.

If the run was not manual, an approval notice is sent. A drafted call waits indefinitely by definition, and nobody was there to see this run happen, so an approval nobody is told about is a message the user believes went out.

Inbox delivery is on top of routing, not instead of it. A nightly digest still writes its file, and the inbox is what tells you the file exists. `inbox.should_deliver` decides: scheduled and watched runs deliver by default, manual ones do not, a dry run never does, and `output.inbox` forces either answer.

## Stage 8: the record

The record is closed with the resolved inputs, the guidelines and memories inlined, every tool call, the approvals queued, the model command, the usage block, the outcome, the output description, and the duration. `clear_running` drops the in-flight marker, `write_record` persists it, and a final `run_finished` event goes to the stream.

### Redaction

Anything bound for a record passes through `redact`:

```python
_SECRET_RE = re.compile(
    r"(?i)\b(?:bearer\s+[A-Za-z0-9._\-]{12,}"
    r"|(?:gh[pousr]|xox[baprs])_[A-Za-z0-9]{8,}"
    r"|sk-[A-Za-z0-9\-_]{12,}"
    r"|eyJ[A-Za-z0-9._\-]{20,})")
```

Deliberately pattern-based and short. It catches the shapes that turn up in connector responses -- bearer headers, provider key prefixes, JWTs -- and makes no claim to catch a secret that looks like prose. The raw log is the unredacted account, lives outside the store, and ages out in a fortnight; this is about what survives for a year.

## Retries

`run` wraps `_run_once` with the workflow's retry policy, sleeping `backoff * 2 ** (attempt - 1)` between attempts.

Each attempt writes its own run record, so `px0 runs list` shows the failures that led to a success rather than hiding them. The last attempt's record is returned, or its error raised.

`retry=False` is passed for nested runs -- a pipeline stage, a sub-workflow input -- because the parent owns the retry decision.

When every attempt fails, two things happen after the record is written.

`_notify_failure` sends the notification and records the result back onto the run, so a silent failure to notify is still visible in `px0 runs show`.

`_trip_breaker_if_stuck` parks a workflow that keeps failing the same way. Only for unattended runs: a manual run that fails is a person at a terminal reading the error, and parking their workflow underneath them would be taking a decision they are in the middle of making. An unattended one has nobody to notice, which is the case this exists for -- a dead connector otherwise means an hourly failure and an hourly notification, forever, with nothing learning that nothing has changed. The park is announced through the same channel failures use, because a workflow that silently stops firing is worse than one that fails loudly.

Both are wrapped so they cannot raise. A breaker that can fail a run is worse than one that misses a case.

## Pipelines

`_run_pipeline` runs each stage in sequence, piping the previous stage's output text in as stdin.

Intermediate stages route to `memory`; only the last stage routes to the pipeline's real output. Every stage runs with `retry=False`.

A stage whose `when` condition is not met is skipped, not failed, and the text it would have received passes through to the next one. That is what makes "post it only if there is something to post" not also break every stage after it.

A failed stage aborts the pipeline and writes a record carrying the stages completed so far. `clear_running` is called on that path too -- a pipeline never cleared its in-flight marker, and inside the daemon, a process that outlives the run, the pid stayed alive, so the run showed in `px0 runs list --running` forever. A one-shot CLI hid the bug, because `list_running` drops markers whose process is gone.

## Next

[Part 7](07-harness.md) covers the model backend the loops above are calling.
