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
| `notify.channel` | str | `""` | tool id used for failure notifications when notify.on_failure is "tool", e.g. slack.post_message or gmail.send_message |
| `notify.target` | str | `""` | where the failure notification goes: a Slack channel for slack.post_message, an address for gmail.send_message |

## `[runs]`

How a failed run is retried. A workflow's own `retry` block overrides both.

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `runs.max_attempts` | int | `1` | how many times a run is attempted before it is recorded as failed; a workflow's own retry.max_attempts overrides this |
| `runs.retry_backoff_seconds` | int | `30` | seconds to wait before the second attempt, doubling for each attempt after |

Each attempt writes its own run record, so `px0 runs list` shows the failures
that led to a success rather than hiding them. The per-workflow cap is 10.

## `[retrieval]`

How the brain is searched. See [`px0 brain search`](../commands/brain.md#px0-brain-search).

| Key | Type | Default | What it does |
| --- | ---- | ------- | ------------ |
| `retrieval.backend` | str | `local` | retrieval backend; either 'local' (SQLite FTS5/BM25) or 'qmd' |
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
