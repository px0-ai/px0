# `px0 runs`

Every execution — a workflow run, or a `brain ask` — is recorded: what triggered
it, which tools it called, what it produced, and whether it succeeded. Records
outlive their logs, so history stays inspectable after the logs age out.

A run leaves three artifacts behind, all under `logs.path`:

| Artifact | Kept for | What it is for |
| -------- | -------- | -------------- |
| The **record** (`records/`) | `logs.record_retention_days`, a year by default | The run's summary. What every listing and `px0 workflows health` reads |
| The **raw log** (`runs/`) | `logs.retention_days`, a fortnight | The full prompts and replies, for a person reading one run |
| The **event stream** (`events/`) | with the raw log | One JSON object per turn, tool call, and outcome — the machine-readable account |

Nothing derives a verdict from the raw log, because the raw log is usually gone.

Implemented by `px0/runs.py` (records, events, and retention) and
`px0/runs_tui.py` (the interactive browser).

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
px0 runs mark <run-id> [--good [NOTE] | --bad [NOTE] | --clear] [--note NOTE]
px0 runs events <run-id> [--json]
px0 runs stats [--since WHEN] [--json]
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

## `px0 runs mark`

Say whether a run's output was any good.

This is the one thing about a run that px0 cannot work out for itself. A record
says whether a run *executed* cleanly — not whether the digest it wrote was
worth reading. A workflow that succeeds every Friday and produces something
useless looks perfect in every other field there is, so without a mark
`px0 workflows improve` would be guessing from execution telemetry alone.

The mark is stored on the run's own record, shows up in `px0 runs list` as
`[good]` or `[bad]`, and is the strongest evidence a proposal is argued from.

### `run-id` (required)

- **Input:** a run id.

### `--good [NOTE]`, `--bad [NOTE]`

The verdict, and optionally what was right or wrong about it in a sentence.

- **Input:** an optional note. `--bad` on its own records the verdict with no
  note; `--bad "it missed the two PRs I reviewed"` records both.

A bare `--bad` says a run was wrong. The note says *how*, which is the part an
improvement pass can act on — so px0 asks for one when you leave it out.

### `--note NOTE`

The same note, as its own flag, for when a script would rather not attach a
value to the verdict.

### `--clear`

Remove an earlier mark, for one made in haste.

```shell
px0 runs mark run_20260821-091500-a1b2 --bad "it summarized last week, not this week"
px0 runs mark run_20260821-091500-a1b2 --good
px0 runs mark run_20260821-091500-a1b2 --clear
```

You can also mark from the interactive browser: press `m` on a run's detail
screen, which is where you have just read what it produced.

---

## `px0 runs events`

One run's structured event stream, oldest first: the prompt it built, each model
call and what it cost, each tool call and whether it was allowed, and how the
run ended.

Tool **argument names** are recorded, never their values — a tool's arguments
routinely carry the content of the work, and the event stream is meant to be
the part of a run that is safe to keep.

### `run-id` (required)

- **Input:** a run id.

### `--json`

Print the events as a JSON array.

```shell
px0 runs events run_20260821-091500-a1b2
px0 runs events run_20260821-091500-a1b2 --json | jq 'select(.kind == "tool_call")'
```

A run that predates event logging, or whose stream has aged out, prints a line
saying so rather than an error. Set `logs.events = false` to stop writing them.

---

## `px0 runs stats`

Runs rolled up by workflow: how many, how many failed, how many you marked bad,
and the median duration. Arithmetic over the records — no model call, no
network.

### `--since WHEN`

Only runs after a point in time.

- **Input:** a relative span — `<n>d`, `<n>w`, or `<n>h`.

### `--json`

The full rollup, including each workflow's findings.

```shell
px0 runs stats --since 30d
```

For one workflow in detail, and for what its runs say is wrong with it, see
[`px0 workflows health`](workflows.md).

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
| `logs.events` | Whether each run writes a structured event stream |

Retention is applied by the daemon's nightly pass.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown run id, or its log or output has been pruned |
