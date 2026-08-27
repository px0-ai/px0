# 12. The trust model

Modules: `px0/approvals.py`, `px0/localtools.py`, `px0/mcp.py`, `px0/runner.py`, `px0/notify.py`

px0 runs a model with tools that can post, send, file, and delete, sometimes at 6am with nobody watching. This part is the whole of what stops that being reckless.

## The read/write split

Every tool carries `is_write`, and it is the axis everything else hangs off.

| Consequence | Read tool | Write tool |
| ----------- | --------- | ---------- |
| Usable as an `inputs:` entry | yes | refused by validation |
| Stubbed by `--dry-run` | no | yes |
| Can be held for approval | no | yes |
| Exempts a run's log from retention | no | yes |
| Offered to `px0 ask`'s router | yes | no |
| Called out at build time | no | yes |

For a discovered tool the flag comes from Composio's `readOnlyHint` tag, and its absence means write. That is the safe direction: a mislabelled read tool costs a confirmation, while a mislabelled write tool costs a message nobody approved.

## The allowlist

A workflow's `tools:` list is the whole of what the user approved when the workflow was built. It has to be the thing that decides, not a string in a conversation.

Both loops enforce it, in one place each.

In the builtin loop:

```python
refused = tool_id not in allowed_tools
if refused:
    result = {"error": f"{tool_id} is not in this workflow's tools: allowlist"}
    runs_mod.append_event(config, run_id, "tool_refused", ...)
```

That branch used to fall through into the execution below, so the refusal was only a message written into the transcript while the call itself went ahead. A model that named any tool at all got it run.

In the MCP loop, `mcp.call_scoped` checks the ids in the scope rather than the name the client sent:

```python
by_name = {mcp_name(t): t for t in scope.get("tools") or []}
tool_id = by_name.get(name)
if tool_id is None:
    ...refuse, log the event, record it in the sidecar
```

A client that invents an MCP tool name gets a refusal, not a call.

A refusal is never silent. `analysis._check_refused_tools` reports it as a problem, because either the instructions describe work the allowlist cannot do, or the model is wandering.

## Dry runs

`--dry-run` resolves inputs for real and stubs every write:

```python
elif dry_run and is_write:
    result = {"stubbed": True, "success": True}
```

The run record carries `dry_run: true`, and that flag propagates. `px0 runs list` labels it, `runs rerun` refuses to replay it as a live run without being told to, `analysis` excludes rehearsals from every rate that would be distorted by them, and the inbox never delivers one -- a dry run's output is a sample, not news.

`analysis._check_dry_run_only` reports the opposite case too: "all runs here were rehearsals, this has never run for real" is worth saying.

## Approvals: the missing middle

The trust model above is binary. A tool either mutates something or it does not, and a workflow either may call it or may not. `--dry-run` rehearses a workflow but never does the work. There was no way to say draft it and ask me, so anything speaking in the user's name had to be handed the real capability up front, on the strength of a plan read once.

An approval is that middle. The call is not executed. It is written down in full -- tool, arguments, the run that drafted it, and what that run was for -- and the model is told it has been queued.

### Two properties that matter more than the feature

A queued call is never a silent one. It is recorded on the run, counted by `px0 status`, and notified on the same policy as a failure. An approval nobody knows about is worse than no approval.

Approving executes; it does not re-run. The arguments the user read are the arguments that go out:

```python
result = tools.call(home, config, approval["tool"], approval.get("args") or {})
```

Re-running the workflow to do it for real would draft something else -- a later hour, a changed source -- and the thing they approved would never have been sent.

### What decides

```python
def needs_approval(wf, config, tool_id, is_write) -> bool:
    if not is_write:
        return False
    setting = getattr(wf, "confirm", None)
    if isinstance(setting, bool):
        return setting
    if isinstance(setting, (list, tuple, set)):
        return tool_id in setting
    return bool(config_mod.get(config, "tools.confirm_writes", False))
```

Read tools never queue. An approval queue that fills up with searches is one nobody reads, and the point of the gate is the calls that leave a mark.

A workflow's own `confirm:` wins in both directions, so the one workflow posting to a public channel can ask even when nothing else does, and the trusted nightly job can be exempted without turning the setting off everywhere.

### Lifecycle

```
pending --> approved   the call was made
        --> rejected   thrown away, with a note
        --> expired    no answer inside approvals.expire_days
        --> failed     the tool errored when it fired
```

`failed` is a distinct terminal state rather than a return to `pending`. Retrying is the user's decision, and an approval that silently returned to the queue would be approved twice.

Expiry is applied on read rather than by a sweep, so a store whose daemon never runs does not accumulate week-old drafts that still look actionable. A message written on Tuesday should not be sendable on Friday.

Listing is oldest first, unlike every run listing in px0, because this is a queue rather than a history: the thing most likely to have gone stale is the thing you most need to see.

### Editing a draft

"Right message, wrong channel" had exactly one answer before `amend`: reject it and run the workflow again, which drafts something else against a later hour. The message the user actually wanted was never sendable.

`amend` replaces the arguments and stamps the edit onto the approval with the previous value and an optional note. An approval whose history says only "approved" would hide that a person changed it.

### Showing the output alongside

A write is drafted mid-run, before the run has an answer, so at the moment of queueing there is nothing to show but the model's own protocol line.

`attach_output` fills in the finished output on every pending draft from that run once it exists. A Slack message shown without the digest it announces is a decision made blind.

### Recording the outcome on the run

`_record_on_run` writes the resolution back onto the run record and appends an `approval_resolved` event. Without it a run's record says a call was queued and never says what happened to it, so `px0 runs why` stops short of the answer and `px0 workflows health` cannot tell a workflow whose drafts are always approved from one whose drafts are always thrown away.

Best-effort: the approval itself is already saved, and a pruned run must not fail it.

## Answering an approval from elsewhere

The queue notifies you wherever you asked to be notified, and could only be answered at the machine px0 runs on. That is the wrong shape: approvals happen when you are away from the desk, which is exactly when you cannot reach the terminal.

px0 has no server by choice, so this is polling. A read tool is asked what came back, and replies naming an approval are acted on. Off unless configured.

### The two guards

The verb has to open the reply:

```python
_REPLY_RE = re.compile(
    r"^[\s>*_`\"']*(?:@[\w.-]+[\s:,]*)?"
    r"(approve|reject|ok|yes|no)\b[\s:,]*"
    r"(apr_\d{8}-\d{6}-[0-9a-f]{4})", re.I | re.M)
```

Matching the verb anywhere in the message read "do not approve apr_x" as an approval and sent it -- the exact opposite of what a trusted person had just said, with a message going out as the consequence. Anchoring means an ambiguous sentence matches nothing, and matching nothing does nothing.

The sender must be trusted. `reply_config` returns `None` unless both a tool and at least one sender are configured. Refusing to run half-configured is deliberate: a reply channel with no sender list is an approval queue that anyone who can post there is able to empty.

`_field` reads the sender and the text out of whatever shape the connector returned, preferring a configured field name and then trying the usual names. Anything not found reads as empty, which fails closed -- a reply whose sender cannot be identified is not acted on.

`"yes"`, `"ok"`, and `"no"` are accepted alongside `"approve"` and `"reject"`, because that is what people actually type in reply to a message asking them a question. Requiring the exact word would mean a queue that mostly ignores its answers.

### Nothing new is possible by reply

Every decision goes through the same `approve` and `reject` a person at the terminal calls. An expired draft stays expired, an already-answered one is not answered twice, and the call that goes out is the one that was drafted.

## Local tool sandboxing

### File roots

`resolve_within_roots` confines the file tools to the store plus anything in `tools.file_roots`:

```python
resolved = candidate.resolve()
for root in roots:
    if resolved == root or root in resolved.parents:
        break
else:
    raise LocalToolError(f"{resolved} is outside every allowed root ...")
```

Symlinks are resolved first, so a link inside a root pointing outside one is refused rather than followed. The error names the roots and the config key, because the fix is either a different path or another entry.

### Protected store paths

The store is an allowed root -- that is what lets a workflow write into `output/`. Without a second rule, that also meant a workflow given `file.write` could rewrite its own `tools:` allowlist to grant itself more tools on the next run, turn `confirm_writes` off in `config.toml`, or read-modify-write `.state/credentials.toml`.

Those are different powers from "write a file", and nothing distinguished them:

```python
PROTECTED_STORE_PATHS = ("workflows", "guidelines", "memory", ".state")
PROTECTED_STORE_FILES = ("config.toml",)
```

Everything on that list has a purpose-built tool already: `memory.remember` for memory, `brain.add` for the brain, `px0 workflows edit` for a workflow. The refusal message says so.

### The shell

`shell.run` is off until `tools.allow_shell` is true, and the message says why:

> a workflow using it can run anything you can

When it is on, the command is argv, not a string handed to `sh`, so nothing in an argument is interpreted as a pipe, a redirect, or another command. A string is split with `shlex` and gets the same treatment.

### Output caps

`tools.max_output_bytes` caps what any local tool returns to the model, and `_truncate` says so in the returned text rather than lying by omission. One large file or chatty script cannot fill the prompt.

### HTTP schemes

```python
HTTP_SCHEMES = ("http://", "https://")
```

A workflow resolving `file://` through the HTTP tool would sidestep the root allowlist entirely.

## Redaction and retention

`runner.redact` masks credential-shaped strings on the way into a run record. The record is kept for a year; a connector that echoes a bearer token back in its response should not be what puts one on disk for that long.

Retention has one exemption:

```python
wrote = any(c.get("is_write") for c in rec.get("tool_calls", []))
if wrote:
    continue  # runs that mutated something are kept regardless of age
```

A run that changed something outside px0 is the one you might need to look up in a year.

Failed runs get a longer log window than successful ones (`logs.retention_days_failed`, 60, against `logs.retention_days`, 14), because a failure is what you go back and read.

Logs live under `logs.path`, outside the store, so raw prompts and connector responses stay out of any folder the user might copy or sync.

Fixtures get the shortest window of all, `runs.fixture_keep_days` at 14, and are off by default. A fixture is the only place the content of a run's inputs is written down.

## Notification

Three channels, chosen by `notify.on_failure`:

| Channel | Behaviour |
| ------- | --------- |
| `none` | Silent, the default, right for a manual run |
| `desktop` | A local notification; needs nothing configured and cannot leak anything off the machine |
| `tool` | Sent through `notify.channel` to `notify.target`, using a write tool already authorized |

`MESSAGE_TOOLS` is a closed map of tool ids that can carry a message to the arguments each one needs. Anything else named as a channel is refused with that list, rather than producing a schema error from the far end of a Composio call.

`notify.on_approval` falls back to the failure policy when unset, so a store that already said how it wants to hear about failures does not have to say it twice.

Both `on_failure` and `on_approval` are wrapped so they cannot raise. A failed notification must never mask the failure it reports.

`px0 status` reports the gap directly:

> a failed run tells you nothing: notify.on_failure is 'none'

## What a run's record proves

Put together, a run record answers: which version of the workflow ran, which guidelines and memories were in the prompt, every tool called with its arguments and whether it was refused, stubbed, queued, or executed, what each call returned in summary, what was drafted for approval and what became of it, what the run cost, and where its output went.

That is the audit trail, and it is what `px0 runs why` prints.

## Next

[Part 13](13-feedback.md) covers what px0 does with all of that afterwards.
