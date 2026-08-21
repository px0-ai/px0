# `px0 status`

Whether anything needs attention, in one screen.

Answering that used to take three commands: [`px0 daemon status`](daemon.md) for
whether the scheduler is alive, [`px0 runs list --failed`](runs.md) for what went
wrong, and [`px0 doctor`](doctor.md) for whether the install is sound. This
assembles the parts of those that matter, and makes no network or model call, so
it is cheap enough to run whenever you wonder.

Implemented by `px0/status.py`.

```
px0 status [--hours N] [--json]
```

---

## `--hours N`

How far back a failure still counts as news.

- **Input:** a whole number of hours.
- **Default:** 24.

```shell
px0 status --hours 72
```

## `--json`

The same information as one object: daemon state, workflow counts, next fire
times, recent runs, failures, the notification policy, and the problems found.

- **Input:** flag, no value. Default off.

```shell
px0 status --json | jq '.problems'
```

## What it reports

| Line | What it tells you |
| ---- | ----------------- |
| scheduler | Whether the daemon is running, and on which platform |
| workflows | How many exist, are scheduled, are watched, are disabled |
| next | The next fire time per scheduled workflow, soonest first |
| runs | How many ran in the window, how many failed, how many are in flight |
| failures | Each failed run with its first line of error |

## The problems it looks for

| Problem | Fix it prints |
| ------- | ------------- |
| Workflows are scheduled but the daemon is not running | `px0 daemon start` |
| A run failed in the window | `px0 runs why <run-id>` |
| A run failed and `notify.on_failure` is `none` | `px0 config set notify.on_failure desktop` |
| A workflow file does not parse | `px0 workflows validate` |

## Related configuration

| Key | Effect |
| --- | ------ |
| `notify.on_failure` | Whether a failed run tells you anything. `status` flags it when failures happened and this is `none` |
| `logs.path` | Where the run records it reads live |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Nothing needs attention |
| `1` | At least one problem was found |
