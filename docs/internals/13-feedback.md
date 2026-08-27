# 13. The feedback loop

Modules: `px0/analysis.py`, `px0/improve.py`, `px0/replay.py`, `px0/runs.py`

A workflow you wrote once was right once. This is the machinery for finding out what has happened to it since, and doing something about it.

The loop has four moves, and they are separate on purpose:

```
runs.py       record what happened
analysis.py   compute what it means, deterministically
runs mark     add the one thing no record can infer
improve.py    argue for a revision from that evidence
replay.py     check the revision against the same inputs
```

## Three artifacts per run

| Artifact | Retention | What it is for |
| -------- | --------- | -------------- |
| Record | 365 days | The run's summary; every listing and every analysis reads this |
| Raw log | 14 days, 60 if failed | Full prompt and reply text, for a person reading one run |
| Event stream | ages out with the log | One JSON object per turn, tool call, and outcome |

All three live under `logs.path`, outside the store, and all three are partitioned by date so a `--since` query can skip whole days without opening them.

Nothing derives a verdict from the raw log, because the raw log is usually gone. The event stream is the machine-readable half that survives being read after the fact:

```python
"kinds are the vocabulary `px0 runs events` and `px0 workflows health` read,
 so they are stable strings rather than free text: run_started, inputs,
 prompt, model_call, tool_call, tool_refused, output, run_finished"
```

`append_event` is best-effort and silent on failure. Every call site is inside the run loop, so an unwritable log directory or a value that will not serialize has to cost the run nothing.

### Two bugs worth knowing about

Records are stamped with the store that owns them. `logs.path` defaults to one directory shared by every store on the machine, so without the stamp a second store's `px0 runs list` showed the first store's runs and offered to rerun workflows it does not have. Records written before the stamp existed have no owner and are included rather than hidden -- on a single-store setup that is all of them.

`as_utc` exists because a record stamps `start_time` in UTC and `parse_since` hands back a naive local datetime, and the two were being compared as strings. That is wrong twice over: the offset suffix makes an in-window record sort before a naive cutoff character by character, and the naive value is local wall clock while the record is UTC. In IST that quietly dropped five and a half hours of runs from every `--since` query, including the one the daily budget is computed from.

### In-flight markers

A run writes its pid to `.state/running/<id>.json` at the start and clears it at the end, so `px0 runs cancel` can signal it.

`list_running` checks every marker against the process table and drops the dead ones, because a crashed run leaves its marker behind and a stale entry looks like a run that has been going for days.

`cancel` sends `SIGTERM` by default so the run's own handlers can finalize the record, and `SIGKILL` with `--force`, which leaves the record as it was last written.

## Deterministic analysis

Everything in `analysis.py` is arithmetic over run records. No model call, no network, nothing that can answer differently twice for the same input.

That is what makes it usable as evidence. `px0 workflows improve` is handed this report, and a proposal is only as honest as the numbers under it -- numbers a model produced would be circular.

### The vocabulary

Deliberately small. A problem is costing the user output they wanted. A note is worth knowing and may well be fine. A finding is fixable when px0 can repair it mechanically, which in practice means narrowing an allowlist or raising a timeout -- never anything that changes what a workflow says or widens what it may reach.

```python
@dataclass
class Finding:
    code: str
    severity: str          # "problem" | "note"
    detail: str
    evidence: dict
    fix: str               # what the user should run
    fixable: bool          # px0 can do it itself
    payload: dict          # what apply_fix needs, as data
```

`payload` is data rather than a closure, so a report survives being serialized to JSON and read back by something else.

### Grouping errors

Five failures of one cause should be one finding, not five. `normalize_error` strips what makes two instances look different:

```python
_NOISE = [
    (re.compile(r"\b[0-9a-f]{8,}\b", re.I), "<id>"),
    (re.compile(r"\b\d{4}-\d{2}-\d{2}[T ][\d:.+]+"), "<time>"),
    (re.compile(r"\d+"), "<n>"),
    (re.compile(r"'[^']{0,80}'"), "'<v>'"),
    (re.compile(r'"[^"]{0,80}"'), '"<v>"'),
    (re.compile(r"\s+"), " "),
]
```

The digit rule is deliberately not anchored to word boundaries. "timed out after 30s" and "after 90s" differ inside a token, and a bounded `\b\d+\b` left them as two separate findings of one cause.

The normalized shape goes in the evidence; the finding's sentence quotes one real message, because `<n>` placeholders are not something a person can act on.

### The checks

Each takes the same arguments and returns a list of findings, so the caller is a loop rather than a wall of conditionals and a new check is one function plus one name in a tuple.

| Code | Severity | What it catches |
| ---- | -------- | --------------- |
| `no_runs` | note | Nothing on record; says whether it is scheduled |
| `dry_run_only` | note | Every run here was a rehearsal |
| `failing` | problem or note | Failures, grouped by normalized cause |
| `empty_output` | problem | A run succeeded and wrote nothing |
| `success_despite_tool_errors` | problem | Every tool call errored and it still wrote something |
| `tool_refused` | problem | The model reached for a tool it may not use |
| `tool_erroring` | problem | One tool failing at least a third of its calls |
| `dead_tools` | note, fixable | Allowlisted tools never once called |
| `turn_cap` | problem or note | Runs using every tool-call turn available |
| `retry_pressure` | note | Runs needing a second attempt |
| `timing_out` | problem, fixable | Runs the clock killed |
| `input_always_empty` | problem | An input that always resolves to nothing |
| `input_degraded` | note | An optional input that keeps failing |
| `marked_bad` | problem | Runs a person judged bad |
| `parked` | problem | The circuit breaker disabled it |
| `spans_versions` | note | These runs mix a file with the one it replaced |
| `cost_estimated` | note | Cost here is a guess, and need not be |

Thresholds live at the top of the module: `MIN_RUNS_FOR_RATES` is 3, `MIN_RUNS_FOR_DEAD_TOOL` is 5, `TOOL_ERROR_RATE` is 0.34. Below the minimums the report says so rather than calling two failures out of three a crisis.

### Three checks worth reading closely

`_check_silent_success` catches the most expensive kind of failure px0 has, because every listing shows it green. Two shapes count: an empty output, and a run whose every tool call errored but which still wrote whatever the model said around the errors.

`_check_dead_tools` is the only repair px0 will make mechanically without argument, because narrowing an allowlist can only ever reduce what a workflow may do. Every allowlisted tool is described in the prompt on every run, so a dead tool is a bill paid on every run for a capability nothing uses.

`_check_timeouts` reads the limit each run actually hit out of its own error message, rather than trusting the workflow's current `timeout:`:

```python
limits = [t for t in (_timed_out_at(r) for r in timed_out) if t is not None]
if limits and max(limits) < current:
    return [Finding("timing_out", "note", "...before the timeout was raised...")]
```

A window of runs can straddle a change to the timeout. Blaming a raised timeout for failures that happened under the old one reads as "the fix did not work" when what happened is that the fix has not been tested yet.

`_is_timeout` matches the harness's own wording rather than "timed out" anywhere in the message, because a connector that times out is an ordinary failure. Raising the workflow's timeout would not help it, so it must not be diverted into the check whose whole purpose is to offer that repair.

### Repair

`apply_fixes` makes exactly two edits -- dropping never-called tools and raising a timeout -- as one change through `versioning.record_change`, so `px0 changes revert` undoes it like any other edit.

Nothing here touches the instruction body, adds a tool, or reaches a model. That is what `px0 workflows improve` is for, and it asks first.

## Marks

```python
def mark(config, run_id, verdict, note=""):
```

This is the one signal nothing else in px0 can infer. A record says whether a run executed cleanly, not whether the digest it wrote was any good. A workflow that succeeds every Friday and produces something useless looks perfect in every other field there is.

`verdict=None` clears a mark, so one made in haste is undoable. The note is what makes the mark worth having: "bad" says a run was wrong, "missed the two PRs I actually reviewed" says how.

Marks show up in `px0 runs list`, feed `_check_marks`, are the strongest evidence in an improvement proposal, and are one of the two sources `memory.suggest` reads.

## The circuit breaker

`consecutive_failures` counts from the newest record backwards and stops at the first success.

That is the only reading that answers "is this broken now". A rate over a window cannot: a workflow that failed thirty times last week and has worked every day since has a terrible rate and nothing wrong with it.

It also requires the same normalized cause each time. A workflow failing three different ways is one someone should look at, but it is not stuck in the way this exists to stop.

`should_trip_breaker` compares that streak against `runs.disable_after_failures`, defaulting to 5, and `runner._trip_breaker_if_stuck` acts on it -- but only for unattended runs, and always with an announcement. See [part 6](06-running.md).

## Budgets

`spend_today` sums the day's records and returns both numbers rather than one blended figure:

```python
return {"cost_usd": ..., "estimated_tokens": ..., "measured": measured, ...}
```

A budget enforced against an estimate and a budget enforced against a bill are different promises, and the caller should know which it is making.

`over_budget` returns a sentence saying why this store should not start another run, or `None`. Off by default -- a tool that refuses to work because of a number the user never set would be worse. Manual runs are never blocked.

## Improvement

`improve.py` takes the deterministic report plus the runs behind it and asks a model one question: given what these runs did, what should this workflow say instead?

Three rules shape it, and each is a rule because the obvious alternative is worse.

### The proposal edits the request, not the file

A workflow's tools, inputs, and guideline list all follow from its request. A model that rewrote the body directly would leave frontmatter describing a workflow that no longer exists.

So what comes back is a new request, and applying it rebuilds through exactly the path a hand-typed edit takes.

### Nothing is applied without being shown

px0's posture everywhere else is to list what it is about to do and wait. An improvement pass that quietly rewrote a scheduled workflow would be the one place that stopped being true.

### Tools are never widened by a model's say-so

A proposal may argue for a new tool, and that argument is printed, but the tool itself only ever arrives through the same confirm-and-authorize path `px0 workflows new` uses.

### The case file

`evidence()` assembles what the model sees, and it is assembled as data rather than built inside an f-string so that `--show-evidence` can print the very thing the model was given. A user who disagrees with a proposal should be able to see what it was reasoning over.

| Section | Contents |
| ------- | -------- |
| `workflow` | The file as it stands: request, description, body, tools, guidelines with summaries, inputs, output, trigger, timeout |
| `window` | The run counts and rates from the health report |
| `findings` | The deterministic findings |
| `failures` | Error shapes with counts, at most 5 |
| `marked_runs` | Up to 6 judged runs, with the note, an output excerpt, and the tools called |
| `recent_runs` | Up to 12 runs: outcome, duration, turns, per-tool results, empty inputs, output size |
| `available_guidelines` | Path and summary for up to 40 |

Run output is included only where it was asked for -- runs a person marked, and runs that failed. Including every output would be both the bulk of the prompt and, mostly, noise: a run nobody complained about is evidence that things are fine, and one line saying so carries that.

### The contract

The prompt asks for one JSON object with `diagnosis`, `request`, `reasoning`, `confidence`, `body`, `tool_drops`, `tool_adds`, and `guideline_edits`, and holds five rules:

- The user's verdicts are the strongest evidence there is. A run marked bad is a fact about the output that no counter of successes can outweigh.
- Prefer the smallest change that addresses the evidence. Rewriting a working request because it could be phrased better is a regression.
- A complaint about form -- length, tone, ordering, what to include -- belongs in a guideline, not in the request. Guidelines apply to every workflow that carries them; a request applies to one.
- Only name a tool in `tool_adds` when the runs show the work needs it, such as the model repeatedly reaching for a tool it was refused.
- If the runs support no change, say so and return the current request verbatim. Finding nothing is a valid answer.

`propose` reads the answer strictly. A reply that is not JSON, or that omits the request, raises rather than being patched up into something plausible: this is about to be shown to a user as a considered recommendation, and half of one read through a lenient parser is worse than none.

`Proposal.changes_request` compares on collapsed whitespace, because a proposal returning the same sentence rewrapped is a proposal that found nothing, and rebuilding for it would spend a model call and a version to arrive exactly where it started.

### Guideline edits

Guidelines get their own field rather than being folded into the request, because they are the right home for a whole class of complaint. "The summary is too long" is not a fact about this workflow -- it is a standard, and writing it into a guideline fixes every workflow that shares it, where writing it into one request fixes exactly one.

`reconcile_guideline_edits` corrects each edit's claim about whether its file exists. The model says `is_new`; the disk decides. Trusting the flag meant a proposal that misremembered a path would overwrite an existing guideline holding ten rules with one holding two.

`apply_guideline_edit` appends to an existing file rather than replacing it. The user's own wording above stays untouched -- a guideline is theirs, and an improvement pass earns the right to add a rule, not to rewrite the ones already there.

The path goes through `builder._guideline_path`, the same sanitizer the builder uses. The model picks this name, so it is untrusted input on its way to becoming a filesystem path, and stripping a leading slash -- which is all this used to do -- leaves `../../.bashrc` intact.

## Replay

Propose a revision, apply it, and find out next Friday whether it helped. That is a slow and expensive way to learn something a model call could settle in a minute, and it is why the improvement loop stopped one step short of being a loop.

What was missing is a fixture. A run resolves its inputs from live sources, so running the same workflow twice compares two different worlds: the pull requests moved, the calendar changed, the inbox filled. Nothing could be held still long enough to say "this wording is better than that one".

### Capturing

`capture` writes the resolved inputs, the stdin, and the rendered prompt to `.state/fixtures/<workflow>/<run>.json`.

The rendered prompt is kept beside the inputs because it is what the model actually saw. Reconstructing the old one from the inputs alone would mean reimplementing the renderer and hoping the two agree.

Fixtures live under `.state/`, never in the store proper. Those are folders people sync, export, and open in an editor; a fixture is a copy of whatever a connector returned and should not travel by accident.

Capture is off by default and per workflow, and that is not timidity. A fixture is the content of your work: the emails, the diffs, the messages. It belongs on disk only where someone decided it should, on a short retention, outside the store that gets synced. `capture: true` in a workflow is the ordinary way to turn it on; `runs.capture_inputs` turns it on store-wide and a workflow can still opt out.

`capture` is best-effort. A fixture that cannot be written must not fail the run that was producing real work.

### Replaying

`render_with` rebuilds the prompt against captured inputs. Neither the input tools nor the clock are touched: the whole point is that the world is held still. Passing `body` replaces the instruction text, which is how two revisions are compared -- the same inputs through two sets of instructions.

`answer_for` makes one model call with no tools. A replay compares what a workflow says against fixed inputs; letting it call anything would both change the world and reintroduce the variance the fixture exists to remove.

`diff` returns unified output rather than full, because the useful question about a revision is what changed, and two digests that agree on nine paragraphs out of ten should not print nine paragraphs.

`summarize` prints above the diff, because the first question about a revision is whether it changed anything at all -- and a proposal that rewrites every line of a working digest is one to look at twice, however good its reasoning read.

## The whole loop

```
px0 workflows health <id>      arithmetic over your own records
px0 runs mark <run> --bad "..."   the one thing no record can infer
px0 workflows improve <id>     a revision argued from that evidence
px0 workflows replay <id>      the old wording and the new one, same inputs
px0 changes revert <chg>       if it was worse
px0 memory suggest             standing facts spotted in your corrections
```

Every step is inspectable, and every step that writes is revertible.

## Next

[Part 14](14-ask.md) covers the front door, which reuses several of these pieces for a different question.
