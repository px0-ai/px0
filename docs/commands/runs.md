# `px0 runs`

Every execution — a workflow run, or a `brain ask` — is recorded: what triggered
it, which tools it called, what it produced, and whether it succeeded. Records
outlive their logs, so history stays inspectable after the logs age out.

Implemented by `px0/runs.py` (records and retention) and `px0/runs_tui.py` (the
interactive browser).

```
px0 runs                                 # interactive browser
px0 runs list [--workflow ID] [--failed] [--since WHEN] [--running] [--json] [--workflow ID] [--failed] [--since WHEN] [--json]
px0 runs show <run_id>
px0 runs output <run_id>
px0 runs logs <run_id> [--follow|-f]
px0 runs rerun <run_id>
px0 runs why <run_id>
px0 runs cancel <run-id> [--force]
px0 runs prune [--dry-run]
px0 runs open <run-id>
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

## `px0 runs list --running`

Only the runs in flight right now, with the workflow each one is running and its
pid.

- **Input:** flag, no value. Default off.
- A run that crashed leaves its marker behind, so each one is checked against the
  process table first; a dead marker is dropped rather than reported as a run
  that has been going for days.

```shell
px0 runs list --running
```

---

## `px0 runs cancel`

Stop a run in flight. Without this the only bound on a run is its own `timeout`.

### `run-id` (required)

- **Input:** a run id, as printed by `px0 runs list --running`.

### `--force`

Send `SIGKILL` instead of `SIGTERM`.

- **Input:** flag, no value. Default off.
- `SIGTERM` lets the run finalize its record as failed. `SIGKILL` leaves the
  record as it was last written, so use it only when the run is not responding.

```shell
px0 runs cancel run_2026-08-21-091500-a1b2
px0 runs cancel run_2026-08-21-091500-a1b2 --force
```

---

## `px0 runs prune`

Delete logs and records that are past retention.

- The daemon's nightly pass does this too. This is how a store that never
  installs the daemon applies its retention settings at all.
- Runs that called a write tool are never pruned, regardless of age.

### `--dry-run`

Print the retention windows and how many records they apply to, and delete
nothing.

- **Input:** flag, no value. Default off.

```shell
px0 runs prune --dry-run
px0 runs prune
```

---

## `px0 runs open`

Print what a run produced: the file it wrote, or its text if it wrote no file.

### `run-id` (required)

- **Input:** a run id.

```shell
px0 runs open run_2026-08-21-091500-a1b2
```

Differs from `px0 runs output`, which prints the text recorded on the run.
`open` reads the file on disk, so it shows what is there now.

---

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
