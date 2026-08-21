# `px0 runs`

Every execution — a workflow run, or a `brain ask` — is recorded: what triggered
it, which tools it called, what it produced, and whether it succeeded. Records
outlive their logs, so history stays inspectable after the logs age out.

Implemented by `px0/runs.py` (records and retention) and `px0/runs_tui.py` (the
interactive browser).

```
px0 runs                                 # interactive browser
px0 runs list [--workflow ID] [--failed] [--since WHEN] [--json]
px0 runs show <run_id>
px0 runs output <run_id>
px0 runs logs <run_id> [--follow|-f]
px0 runs rerun <run_id>
px0 runs why <run_id>
```

---

## `px0 runs` with no verb

Opens an interactive run browser. This is the one group where omitting the verb
is not an error.

Piped or redirected it prints the listing instead, so `px0 runs | head` behaves
like `px0 runs list` rather than failing for want of a terminal.

---

## `px0 runs list`

Past runs, newest first.

### `--workflow ID`

Only runs of one workflow.

- **Input:** a workflow id.

### `--failed`

Only runs that did not succeed.

- **Input:** flag, no value. Default off.

### `--since WHEN`

Only runs after a point in time.

- **Input:** a relative span — `<n>d`, `<n>w`, or `<n>h`. A leading minus is
  accepted, since "`-7d`" reads naturally as seven days back. Absolute dates are
  not accepted.

### `--json`

Print the records as JSON, and nothing else.

```shell
px0 runs list --failed --since 7d
px0 runs list --workflow friday-pr-digest --json | jq -r '.[].id'
```

---

## `px0 runs show`

One run's full record: trigger, timing, tool calls, outcome, and where its output
went.

### `run_id` (required)

- **Input:** a run id, as printed by `runs list` — for example
  `run_20260821-093000-a1b2`. A `brain ask` exchange has an `ask_`-prefixed id.

---

## `px0 runs output`

Print what a run produced.

### `run_id` (required)

- **Input:** a run id.
- For a run whose output went to a file, this prints the file's contents.

---

## `px0 runs logs`

The log for one run.

### `run_id` (required)

- **Input:** a run id.

### `--follow`, `-f`

Follow the log as it is written.

- **Input:** flag, no value. Default off.
- Useful against a run the daemon is executing now.

```shell
px0 runs logs run_20260821-093000-a1b2 -f
```

Logs live under `logs.path`, outside the versioned store, and are pruned on the
retention settings below.

---

## `px0 runs rerun`

Run a workflow again with the same inputs as a past run.

### `run_id` (required)

- **Input:** a run id.
- Produces a new record; the original is untouched.

---

## `px0 runs why`

How a run reached its result: the workflow version it used, the guidelines it
inlined, the passages it retrieved, and the tool calls it made.

### `run_id` (required)

- **Input:** a run id.

`px0 guidelines why <claim_id>` is the same verb for claims. One implementation
serves both — it branches on the id's shape — but each is listed under the group
whose ids it takes.

## Related configuration

| Key | Effect |
| --- | ------ |
| `logs.path` | Where run logs are written |
| `logs.retention_days` | Days to keep logs for successful runs |
| `logs.retention_days_failed` | Days to keep logs for failed runs |
| `logs.record_retention_days` | Days to keep run records, which outlive the logs |
| `logs.max_file_size_mb` | Single log file rotation size cap |

Retention is applied by the daemon's nightly pass.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown run id, or its log or output has been pruned |
