# Configuration keys

`config.toml` lives at the store root. Read and write it with
[`px0 config`](../commands/config.md) — every key is validated against its type
and allowed values before it is saved.

```shell
px0 config list          # every key, with its value, default, and help
px0 config get <KEY>
px0 config set <KEY> <VALUE>
```

Writing booleans, integers, and lists is covered in
[`px0 config set`](../commands/config.md#px0-config-set).

## `[model]`

Which coding agent px0 shells out to.

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `model.harness_cmd` | str | `claude -p` | coding agent CLI invocation, e.g. 'claude -p'; a known harness name (claude, gemini, pi, opencode) expands to its full command, or pass any literal command. `px0 config model` sets this interactively. |
| `model.agent_loop` | str | `builtin` | who drives the tool calls. "builtin" (the default) is px0's own loop, capped at runs.max_tool_turns; "mcp" hands the workflow's tools to the harness over MCP and lets it run its own loop, which removes that cap and is what a workflow needing many steps wants; "auto" does that wherever px0 has verified flags for the configured harness and falls back otherwise |
| `model.output_format` | str | `auto` | whether to ask the harness for a structured envelope around its reply. "auto" (the default) uses one wherever px0 knows the flag for that backend, which is what makes a run's token counts real numbers rather than px0's own estimate; "text" runs the harness exactly as typed |
| `model.verbose` | bool | `false` | ask the harness to narrate what it is doing, and keep what it prints on stderr in the run log -- useful when a workflow is behaving oddly and the reply alone does not say why |

**On `model.output_format`.** A harness is another program, and only some of
them report what a call cost. `auto` asks for a structured envelope wherever px0
knows the flag for that backend, which is what turns a run's token counts from
px0's own estimate into the numbers the backend was actually billed —
`px0 workflows health` says which it is showing you. px0 adds no flags to a
harness command it does not recognize, because the cost of guessing wrong is
every run for that backend failing; and if a backend turns out to be older than
the flag, the call is retried once without it rather than failing.

## `[brain]`

Where the brain lives and what is read from it. See [`px0 brain`](../commands/brain.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `brain.path` | str | `~/.px0/brain` | directory the brain lives in -- point it at an Obsidian vault (or any folder of Markdown) and px0 reads what is already there; dot-folders like .obsidian/ and .trash/ are skipped |
| `brain.private_folder` | str | `work` | brain subfolder withheld from retrieval and never sent anywhere; set to "" to disable, or rename it if your vault already has a folder by this name that you do want searched |
| `brain.ignore` | list | `["*.excalidraw.md"]` | glob patterns never indexed, on top of the always-skipped dot-folders |

## `[output]`

Where workflow file outputs are written.

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `output.path` | str | `~/.px0/output` | default directory for workflow file outputs |

## `[connectors]`

How external app calls are brokered and authorized. See [`px0 tools`](../commands/tools.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `connectors.provider` | str — `composio` / `native` | `composio` | intended default for brokering tool connections; not yet enforced in this build -- every toolkit currently routes through Composio |
| `connectors.retries` | int | `3` | per-run transient connector retries, exponential backoff |
| `connectors.composio_api_key` | str | `""` | Composio API key used to authenticate external app connections |
| `connectors.ca_bundle` | str | `""` | CA bundle used to verify TLS for every outbound request; set automatically when an intercepting proxy (e.g. Zscaler) makes certifi's bundle insufficient |

## `[logs]`

Run logs and record retention. See [`px0 runs`](../commands/runs.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `logs.path` | str | `/var/log/px0` | run log directory, kept outside the versioned store |
| `logs.retention_days` | int | `14` | days to keep logs for successful runs |
| `logs.retention_days_failed` | int | `60` | days to keep logs for failed runs |
| `logs.record_retention_days` | int | `365` | days to keep run records (outlives the logs themselves) |
| `logs.max_file_size_mb` | int | `20` | single log file rotation size cap, in MB |
| `logs.events` | bool | `true` | write each run's structured event stream (one JSON object per turn, tool call, and outcome), which is what `px0 runs events` prints and what `px0 workflows health` reads; ages out with the run logs |

## `[update]`

Upgrades and the weekly availability check. See [`px0 update`](../commands/update.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `update.channel` | str — `stable` / `beta` | `stable` | release channel; not functionally checked in this build -- `px0 update` reports that no release manifest is configured |
| `update.check` | bool | `true` | whether the daemon checks weekly for an available update |
| `update.auto_install` | bool | `false` | install updates automatically instead of only surfacing them |

## `[tools]`

What the tools that run on this machine may do. See [`px0 tools`](../commands/tools.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `tools.allow_shell` | bool | `false` | allow the shell.run tool, which executes an arbitrary command locally; off by default because a workflow that can run a shell can do anything you can |
| `tools.confirm_writes` | bool | `false` | hold every write tool call for approval before it fires, across every workflow -- a workflow's own confirm: overrides this in both directions. The drafted call waits in `px0 approvals` with its arguments shown in full |
| `tools.file_roots` | list | `[]` | extra directories the file.read and file.write tools may touch, on top of the store itself; a path outside every root is refused |
| `tools.http_timeout` | int | `20` | seconds before the http.get and http.post tools give up on a request |
| `tools.max_output_bytes` | int | `20000` | cap on how much text a local tool returns to the model, so one large file or chatty script cannot fill the prompt |

`file.read`, `file.write`, and `file.list` may touch the store and nothing else
until `tools.file_roots` says otherwise. `shell.run` is listed by
`px0 tools list` even when disabled, so it is discoverable, and refuses to run
until `tools.allow_shell` is true.

## `[notify]`

What happens when a scheduled run fails. A workflow's own `on_failure` block
overrides all three. See [`px0 status`](../commands/status.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `notify.on_failure` | str | `""` | what happens when a scheduled run fails: "desktop" raises a local notification, "tool" sends through notify.channel, "none" (the default) stays silent |
| `notify.on_approval` | str | `` | how you hear that an unattended run drafted a write and is waiting for your approval; empty follows notify.on_failure, so a store that already said how it wants to hear about failures does not say it twice |
| `notify.channel` | str | `""` | tool id used for failure notifications when notify.on_failure is "tool", e.g. slack.post_message or gmail.send_message |
| `notify.target` | str | `""` | where the failure notification goes: a Slack channel for slack.post_message, an address for gmail.send_message |

## `[runs]`

How a run is retried, how far it may go, and what it may spend.

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `runs.max_attempts` | int | `1` | how many times a run is attempted before it is recorded as failed; a workflow's own retry.max_attempts overrides this |
| `runs.retry_backoff_seconds` | int | `30` | seconds to wait before the second attempt, doubling for each attempt after |
| `runs.max_tool_turns` | int | `12` | how many tool-call turns one run may take in px0's own loop; a run that needs more stops short with its work half done. Ignored when model.agent_loop is 'mcp', where the harness runs its own loop |
| `runs.disable_after_failures` | int | `5` | park an unattended workflow after this many consecutive failures of the same cause, announced through the notify channel; 0 lets it keep trying. A manual run never trips it -- you are there, reading the error |
| `runs.capture_inputs` | bool | `false` | keep what every run's inputs resolved to, so a revision can be replayed against the same data. Off by default and deliberately so: a fixture is the content of your work. A workflow's own capture: overrides this |
| `runs.fixture_keep_days` | int | `14` | days a captured fixture is kept; it is the only place the content of a run's inputs is written down, so this window is short |
| `runs.daily_budget_usd` | float | `false` | stop unattended runs once the day's measured cost reaches this; 0 is off. Only counts what the harness reported, so it needs model.output_format to be asking for it. Manual runs are never blocked |
| `runs.daily_token_budget` | int | `false` | the same ceiling against px0's own token estimate, for a harness that reports no costs; 0 is off |

Each attempt writes its own run record, so `px0 runs list` shows the failures
that led to a success rather than hiding them. The per-workflow cap is 10.

**On the turn ceiling.** In px0's own loop, every turn resends the whole
conversation, so the cost of a high ceiling is paid only by the runs that use
it — where the cost of a low one was paid by every run that needed one more
step and silently stopped short. `model.agent_loop = "mcp"` removes the ceiling
entirely by letting the harness run its own loop.

**On the budget.** Off by default: a tool that refuses to work because of a
number you never set would be worse than one that spends. It stops *unattended*
runs only — a command you just typed is never blocked, since you are sitting
there and can see what it costs. `daily_budget_usd` is exact and needs the
harness to be reporting its costs; `daily_token_budget` works against px0's own
estimate for a harness that reports nothing.

## `[approvals]`

Write calls held for a person. See [`px0 approvals`](../commands/approvals.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `approvals.expire_days` | int | `7` | days a drafted write call stays approvable before it goes stale -- a message written on Tuesday should not be sendable on Friday; 0 never expires |
| `approvals.keep_resolved_days` | int | `30` | days to keep approvals that were sent, rejected, or expired |

| `approvals.reply_tool` | str | `` | read-only tool polled for replies that approve or reject a drafted call, e.g. slack.read_channel or gmail.search_messages; empty means approvals can only be answered at the terminal |
| `approvals.reply_args` | str | `` | JSON arguments for approvals.reply_tool, e.g. {"channel": "#ops"} |
| `approvals.reply_from` | list | `` | who may answer by reply -- usernames or addresses, comma-separated. Required: without it, anyone who can post in that channel could empty your approval queue |
| `approvals.reply_text_field` | str | `` | which field of the reply tool's result holds the message text; empty tries the usual names (text, body, message, snippet) |
| `approvals.reply_sender_field` | str | `` | which field holds who sent it; empty tries the usual names (user, from, sender, author) |

**Answering from elsewhere.** With a reply tool and at least one trusted sender
configured, px0 polls that channel for replies naming an approval —
`approve apr_...`, or just `yes apr_...` — and acts on them. Both settings are
required together: a reply channel with no sender list would be an approval
queue that anyone able to post there could empty. Every reply still goes
through the same path a person at the terminal takes, so an expired draft stays
expired and nothing is answered twice.

## `[inbox]`

Where scheduled output is delivered. See [`px0 inbox`](../commands/inbox.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `inbox.auto` | bool | `true` | deliver scheduled and watched runs to the inbox automatically; a workflow's own output.inbox overrides this either way |
| `inbox.keep_days` | int | `30` | days to keep inbox entries you have read or archived; unread entries are never dropped |

## `[ask]`

Conversations. See [`px0 ask`](../commands/ask.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `ask.session_days` | int | `7` | days a conversation is kept under .state/ before it is pruned; what was worth keeping from it is in memory/ by then. 0 keeps them indefinitely |

## `[memory]`

What px0 knows about you. See [`px0 memory`](../commands/memory.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `memory.enabled` | bool | `true` | inline what px0 remembers about you into every run's prompt; off keeps the memory/ folder and stops reading it |
| `memory.budget_chars` | int | `4000` | how much remembered text a single run may inline, so a store that has been running for a year does not turn every prompt into a biography |

## `[schedule]`

The clock schedules are read against. See [`px0 daemon`](../commands/daemon.md).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `schedule.timezone` | str | `` | zone every schedule is read against, e.g. 'Asia/Kolkata'; a workflow's own trigger.timezone wins. Empty follows the machine, which is right until the machine travels or the clocks change |

## `[retrieval]`

How the brain is searched. See [`px0 brain search`](../commands/brain.md#px0-brain-search).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `retrieval.qmd_cmd` | str | `qmd` | command prefix used to run the qmd CLI |
| `retrieval.k_default` | int | `5` | default number of passages retrieved per query |
| `retrieval.rerank` | bool | `true` | rescore retrieved passages by how much of the query each one covers, before trimming to k; local arithmetic, no model call |

## Keys that are declared but not yet wired

Named here so the list is honest about what the current build actually does:

- `connectors.provider` — every toolkit routes through Composio regardless.
- `update.channel` — not functionally checked; `px0 update` reports when no
  release manifest is configured.

## Reranking

With `retrieval.rerank` on, a search asks the backend for more candidates than
`k` and then reorders them: how much of the query each passage covers first, how
close the matched terms sit to each other second, the backend's own score as the
tie-break. BM25 rewards a passage that repeats one query term; a question with
three terms in it usually wants the passage that mentions all three.

Turn it off and `k` rows in are `k` rows out, ranked by the backend alone.

```shell
px0 config set retrieval.rerank false
```
