# Browsing runs

Every run px0 performs -- manual, scheduled, or an `ask` -- leaves a
record: the inputs it resolved, the guideline versions it inlined, every
tool call with its timing, and the outcome. `px0 runs` opens an
interactive browser over those records so finding a run, reading what it
did, and rerunning it are one session instead of three commands with ids
copy-pasted between them.

```shell
px0 runs
```

## The list view

Newest first, one row per run:

```
 px0 runs · 3 of 3
 no filters
────────────────────────────────────────────────────────────
› run_20260820T090031Z  post-standup  schedule  success  [write]
  run_20260820T084512Z  pr-digest     manual    success
  run_20260819T160002Z  post-standup  schedule  failed
────────────────────────────────────────────────────────────
↑↓ move  enter detail  / workflow  f outcome  a writes  s since  c clear  q quit
```

`[write]` marks any run that called a write tool -- something that posted,
commented, or sent. It's the fastest way to find the run that touched the
outside world. Failed rows are the only ones coloured, so a screen of runs
shows you its problems without you reading a word.

The header counts visible rows against total, and the line under it names
whichever filters are actually in effect.

| Key | Action |
| --- | --- |
| `↑` `↓` or `k` `j` | Move the selection |
| `Enter` | Open the detail view |
| `/` | Filter by workflow id |
| `f` | Cycle outcome: all → success → failed |
| `a` | Toggle write-activity only |
| `s` | Filter by age, e.g. `-7d` |
| `c` | Clear every filter |
| `q` | Quit |

## The detail view

`Enter` on a row shows the whole record:

```
 run_20260820T090031Z
workflow: post-standup
trigger:  schedule
outcome:  success
duration: 12.4s

rendered prompt
  Summarize the following meetings in three bullets...

guidelines inlined (1)
  summarization.md @ a1b2c3d4

tool calls (2)
  calendar.list_events 0.83s -> 4 events
  slack.post_message 1.21s  [write] -> ok
────────────────────────────────────────────────────────────
r rerun  l log  o output  w why  esc back
```

Three things here are worth knowing:

- **Rendered prompt** is recovered from the run's raw log -- the actual
  text the model received, inputs interpolated and guidelines inlined. Run
  logs are deleted on the retention schedule (`logs.retention_days`), so
  for an old run you'll see `not available -- log retention removed it`
  instead. The record itself is kept far longer than the log.
- **Guidelines inlined** names each file *with the version* that was used,
  not just the filename. If a guideline changed after this run, that's
  visible here.
- **Timings** are per tool call, so a slow run tells you which connector
  was slow.

| Key | Action |
| --- | --- |
| `r` | Rerun this workflow; the view follows the new run |
| `l` | Page the full raw log through `$PAGER` |
| `o` | Show the run's output |
| `w` | Trace provenance -- the same thing `px0 runs why` prints |
| `Esc` or `q` | Back to the list |

## The same data without the TUI

Every view has a plain-text equivalent, which is what you want in a
script or a pipe:

```shell
px0 runs list                              # identical row text to the list view
px0 runs list --workflow post-standup
px0 runs list --failed --since 7d
px0 runs list --json

px0 runs show <run-id>                     # the full JSON record
px0 runs output <run-id>                   # just the output
px0 runs logs <run-id> [--follow]          # the raw log
px0 runs rerun <run-id>
px0 runs why <run-id>                           # the provenance chain
```

`px0 runs list` and the TUI's list view render row text from the same
formatter, so what you grep is what you saw on screen.

## Next

- [05-guidelines-and-provenance.md](05-guidelines-and-provenance.md) --
  what `w` / `px0 runs why` is walking.
- [06-scheduling-and-the-daemon.md](06-scheduling-and-the-daemon.md) --
  where scheduled runs come from.
