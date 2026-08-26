# `px0 workflows`

A workflow is a Markdown file with YAML frontmatter describing a job px0 can run:
its trigger, the tools it may call, the guidelines it inlines, and where its
output goes. px0 interviews you for one and builds it.

Implemented by `px0/workflow.py` (the file format), `px0/builder.py` (building
from a description), and `px0/runner.py` (execution).

```
px0 workflows new
px0 workflows run [workflow] [--input K=V] [--output {stdout,file}] [--timeout DURATION] [--no-retry] [--dry-run] [--stdin] [--quiet] [--json]
px0 workflows edit [workflow] [--yes] [--no-clarify] [--no-discover]
px0 workflows health [workflow] [--since AGE] [--fix] [--yes] [--json]
px0 workflows improve [workflow] [--since AGE] [--dry-run] [--show-evidence] [--yes] [--no-clarify] [--no-discover] [--json]
px0 workflows recipes [--json]
px0 workflows replay <workflow> [--run ID] [--against FILE] [--fixtures] [--forget] [--json]
px0 workflows list
px0 workflows show <workflow> [--json]
px0 workflows validate [workflow] [--json]
px0 workflows delete [workflow] [--yes]
px0 workflows rename <workflow> <new-id>
px0 workflows copy <workflow> <new-id>
px0 workflows disable <workflow>
px0 workflows enable <workflow>
```

---

## `px0 workflows new`

Turn a description into a workflow file. px0 interviews you for it, asks what
is ambiguous, finds the tools the job needs, authorizes them, writes the
workflow, and saves it under `workflows/`.

- **Arguments:** none — `px0 workflows new` always opens the interview below.

```shell
px0 workflows new
```

### The interview

px0 asks for one thing at a time until it has what a workflow file must pin
down:

| | |
| --- | --- |
| The job | what should happen |
| The sources | what it reads: which service, account, repository, channel, folder, or your own notes |
| The delivery | what it produces and where that goes |
| The cadence | on demand, on a schedule, or when something happens |
| Done looks like | what makes the output right rather than merely produced |

That checklist is `builder.WORKFLOW_SPEC`, and it is handed to the model as part
of the prompt — so the questions you get are the fields the plan actually needs,
not whatever the model finds interesting.

```
› new workflow
  answer in your own words; press Enter on a blank line to stop
› What do you want px0 to do for you?
  digest our merged PRs each week
› Which repository, and whose PRs — yours or the whole team's?
  razorpay/api, the whole team's
› Where should the digest land: a Slack channel, a file, or just printed?
  #eng on slack

› the request
Every Friday afternoon, collect the pull requests merged in razorpay/api that
week and post a short digest to the #eng Slack channel.

› Build this? [Y/edit/n]
```

One question per turn, and the model sees every answer before writing the next
one — so answering "razorpay/api, every Friday" in one breath skips the two
questions that would have asked for those separately. Enter on a blank line ends
the interview early and the request is written from what you did say. The
interview stops after eight questions regardless.

The request it writes is what every later pass reads, so it is shown before the
build spends a planning call: `edit` asks what should change and folds that note
into the request with one more harness call, rather than replacing it outright
— so "and only my own PRs" changes just that, not the whole paragraph. `n`
cancels, anything else builds. Because the interview has already settled these
questions, the clarifying round is skipped — you are not asked twice.

The workflow's id, which is also its filename and how you run it, is derived
from the request by default; px0 prompts you to override it before saving.

What it made is reported as the same bulleted block a run prints:

```
created github-daily-commit-summary
  · workflow    ~/.px0/workflows/github-daily-commit-summary.md
  · guidelines  ~/.px0/guidelines/summarization.md
  · output      ~/.px0/output/logs/daily-commits-{{today}}.md
  · tools       composio:GITHUB_LIST_COMMITS
                composio:SLACK_SEND_MESSAGE
```

Paths are written from your home directory, so a row stays short and is still
something a shell will open. `output` is where a run will put it — under the
store's `output/` folder, which the frontmatter's `output.path` leaves implicit —
with its clock placeholders left as written, since the file does not exist yet
and today's date would be the wrong name tomorrow. A field holding several values
gets a line each, aligned under the first.

Needs a terminal to answer on: with stdin not a tty, there is nobody to
interview, so px0 says so and exits rather than building from nothing.

### Guidelines the build attaches

Near the end of a build, px0 decides which of your existing
[guidelines](guidelines.md) this workflow has to follow. It reads each one's
frontmatter `description` — never its rules — and picks the ones whose standard
the workflow's *output* is judged against, at most three. Whatever it picks is
listed in the new workflow's `guidelines:` and inlined verbatim into every run.

The bar is that following the guideline changes what the workflow produces.
Touching the same subject is not enough: a standup that summarizes commits is
not governed by your commit-message convention, and a workflow that lists open
pull requests is not governed by your PR-description convention. `[]` is a
normal and common answer, and it is also what a failed or unreadable model
response produces — a wrong guideline costs every run, so none is preferred to a
guess, and a build is never failed over this.

Descriptions are what this pass reads, so the way to change what a workflow gets
attached is to edit the description: `px0 guidelines edit <name>`. Anything under
`guidelines/work/` is never offered.

### Guidelines the build writes

Near the end of a build, px0 asks itself whether this workflow leans on a
durable convention — a review rubric, a commit format, a writing voice — that no
file in `guidelines/` covers. When it does, px0 drafts that guideline from the
workflow itself, prints it with the path it would take, and asks:

```
› guideline: Review rubric
  the workflow comments on PRs and has no rubric to comment against
     applies when  What a review comments on. Use when the workflow reviews code.
[..] would be saved as  guidelines/review-rubric.md

## Flag only real breakage
...

› Keep it? [Y/again/n]
```

`again` redraws it, `n` skips it, anything else saves it. Saved guidelines are
listed in the new workflow's `guidelines:`, so every run inlines them verbatim.
The path is printed when it is written, and `px0 guidelines edit <name>` is how
you make a draft yours.

The "applies when" line is the file's `description`, written into its
frontmatter. It is shown because it is not decoration: it is the line every
later build matches this guideline against, so it decides which future workflows
inherit the convention.

This is the only way a guideline gets created — there is no
`px0 guidelines new`. Asking someone to compose a convention from a blank page
is the step that stopped guidelines from being written at all, so the build
drafts a defensible version and leaves editing to you.

Skipped entirely under `edit --yes`: there is nobody to show a draft to, and a
convention nobody saw should not land in the store.

---

## `px0 workflows run`

Execute a workflow now.

When it ends, the run reports itself as a labelled block on stderr — the same
shape a build prints, one field per line:

```
success github-daily-commit-summary
  · run      run_20260824-093444-f88a
  · output   ~/.px0/output/logs/daily-commits-2026-08-24.md
  · tools    composio:GITHUB_LIST_COMMITS x2
             slack.post_message (stubbed)
  · took     39.6s
  · dry run  write tools were stubbed, not called

read it here:
  px0 runs open run_20260824-093444-f88a
```

`output` is written the same way a build writes it, so what was promised and what
was produced read alike. A stdout workflow says `printed below` instead, and the
text follows on stdout. `tools` gets a line per tool, counting repeats rather
than listing them twice and marking the write tools a `--dry-run` stubbed.
`attempt` appears only when a retry was needed. A store outside your home
directory — a `PX0_HOME` elsewhere — is printed in full, having no `~` to
abbreviate against.

The rows are bullets, not ticks: each one is a fact about the run rather than a
check that passed, and the verdict is the heading. A failure keeps the same
shape, adding an `error` row — coloured rather than glyphed, so it stays in the
same column as everything else — and pointing at `px0 runs logs`.

The block is on stderr and the output text on stdout, so `px0 workflows run x >
report.md` writes the report and leaves the summary on screen. `--quiet`
suppresses it.

### `workflow`

Which workflow to run.

- **Input:** a workflow id, as printed by `px0 workflows list`.
- **Default:** omit it to pick from a list interactively.

```shell
px0 workflows run friday-pr-digest
px0 workflows run
```

### `--input K=V`

Supply an input the workflow declares. Repeatable.

- **Input:** `key=value`. Pass the flag once per input.

```shell
px0 workflows run report --input since=2026-08-01 --input team=payments
```

### `--output {stdout,file}`

Where the result goes, overriding what the workflow declares.

- **Input:** `stdout` or `file`.
- **Default:** whatever the workflow's frontmatter says.
- `file` writes under `output.path`; the run record carries the path.

### `--dry-run`

Show what would happen without calling any tool or writing any output.

- **Input:** flag, no value. Default off.
- Use it to check a workflow's plan and tool set before letting it act.

### `--stdin`

Read the workflow's primary input from standard input.

- **Input:** flag, no value. Default off.

```shell
cat notes.txt | px0 workflows run summarize --stdin
```

### `--quiet`

Suppress the progress spinner and the outcome block on stderr. The output itself
still prints, and a failure still reports its error.

- **Input:** flag, no value. Default off.

### `--json`

Print the full run record as JSON, and nothing else.

- Includes the run id, outcome, tool calls, and output target.

```shell
px0 workflows run friday-pr-digest --json | jq -r '.outcome'
```

### `--timeout DURATION`

Override the workflow's own timeout for this run.

- **Input:** a duration with an optional suffix: `90s`, `10m`, `1h`. No suffix
  means seconds.
- **Default:** the workflow's `timeout`, itself defaulting to `120s`.
- For a one-off long report, instead of editing the file to run it once.

```shell
px0 workflows run monthly-review --timeout 15m
```

### `--no-retry`

Attempt once, ignoring the workflow's retry policy.

- **Input:** flag, no value. Default off.
- Useful while debugging: a failing workflow with `retry.max_attempts: 3` would
  otherwise fail three times before telling you.

```shell
px0 workflows run flaky-report --no-retry
```

### `--late-scheduled-at` (internal)

Hidden from `--help`, and not intended to be typed. The daemon passes it when
catching up a fire it missed, so the run is recorded with a `late` trigger and a
note saying when it was due against when it actually ran.

---

## `px0 workflows edit`

Revise a workflow's instructions and rebuild it. Same build pipeline as `new`,
starting from what the workflow already says. The previous version stays in the
store's history, so `px0 changes revert` can undo the rebuild.

### `workflow`

- **Input:** a workflow id. Omit to pick from a list.

### `--yes`

Skip every prompt: no clarifying questions, no confirmations.

- **Input:** flag, no value. Default off.
- For scripted or unattended use. px0 rebuilds from the new instructions as
  written and accepts its own choices.

### `--no-clarify`

Rebuild from the new instructions as written, without asking clarifying
questions.

- **Input:** flag, no value. Default off.
- Narrower than `--yes`: confirmations still happen, only the interrogation is
  skipped.

### `--no-discover`

Use only px0's curated tools; skip the Composio catalogue search.

- **Input:** flag, no value. Default off.
- Faster, offline-friendly, and predictable. Use it when the job only needs tools
  px0 already knows about.

```shell
px0 workflows edit friday-pr-digest
```

---

## `px0 workflows health`

What a workflow's own runs say about it.

Deterministic from end to end: it reads run records and does arithmetic, with no
model call and no network. Every finding is one you can check by reading the
same records yourself, which is what makes it safe to hand to
[`px0 workflows improve`](#px0-workflows-improve) as evidence — a proposal is
only as honest as the numbers under it.

Implemented by `px0/analysis.py`.

- **Arguments:** a workflow id, or none for one row per workflow.

```shell
px0 workflows health                        # a row per workflow
px0 workflows health friday-pr-digest       # one in detail
px0 workflows health friday-pr-digest --fix # apply what px0 can repair itself
```

### What it looks for

A **problem** is costing you output you wanted. A **note** is worth knowing and
may well be fine.

| Finding | What it means |
| ------- | ------------- |
| `failing` | Runs that failed, grouped by cause — five failures of one thing read as one finding, not five |
| `empty_output` | A run that succeeded and wrote nothing. Green in every listing there is, and useless |
| `success_despite_tool_errors` | A run recorded success with every tool call erroring: the output was written from nothing |
| `tool_refused` | The model reached for a tool this workflow may not use — either the instructions ask for work the allowlist cannot do, or the model is wandering |
| `tool_erroring` | One tool failing a third of its calls or more. Now and then is the network; a third of the time is the workflow |
| `dead_tools` | Allowlisted tools nothing has ever called. Each is described in the prompt on every run, so it is a bill paid for a capability nothing uses |
| `timing_out` | Runs the clock killed, with the timeout that would have survived them |
| `turn_cap` | Runs that used every tool-call turn available: the instructions ask for more steps than a run has |
| `input_always_empty` | An input that resolves to nothing every time. The run still succeeds, and the model writes a report around the hole |
| `input_degraded` | An optional input that keeps failing to resolve |
| `marked_bad` | Runs you marked bad, with your own notes. The strongest signal there is |
| `retry_pressure` | How often a run needs a second attempt |
| `spans_versions` | The window mixes a workflow with the one it replaced, so rates in it are not about one file |
| `cost_estimated` | Run cost here is px0's own estimate, and this backend could report real numbers |

Dry runs are counted separately and excluded from every rate: a rehearsal never
calls a write tool, so counting one in a tool's error rate would be counting a
call that never happened.

### `--since AGE`

Only learn from runs newer than this.

- **Input:** a relative span — `<n>d`, `<n>w`, or `<n>h`. Default: everything on
  record.

### `--fix`

Apply the repairs px0 can make by itself, one confirmation each.

Only two edits are ever made here, and both are narrow enough to describe in a
sentence before you agree to them:

- **Dropping a tool nothing has called.** This can only ever *reduce* what a
  workflow may do.
- **Raising a timeout** runs kept hitting, to half again above the slowest run
  that did finish.

Both touch the frontmatter only — never the instruction body, never adding a
tool, never reaching a model. Each is recorded as a versioned change, so
`px0 changes revert` undoes it. Anything that would change what a workflow
*says* is `px0 workflows improve`.

### `--yes`

Skip the per-repair confirmations.

### `--json`

The full report as JSON: counts, findings, evidence, and which findings are
fixable.

---

## `px0 workflows improve`

Revise a workflow from what its runs actually did.

The order is the point of the command. The deterministic report is computed and
printed **first**, so you see the evidence before you see an opinion about it.
Only then is the model asked for a revision, and what comes back is shown in
full — as a diff against the request you actually wrote — before anything is
applied.

Implemented by `px0/improve.py`.

- **Arguments:** a workflow id; omit it to pick one from a list.

```shell
px0 workflows improve friday-pr-digest --dry-run       # see the proposal, apply nothing
px0 workflows improve friday-pr-digest --show-evidence # see what the model is given
px0 workflows improve friday-pr-digest
```

### What it proposes, and what it may change by itself

A proposal is an edit to the workflow's **request**, not to its file. A
workflow's tools, inputs, and guideline list all follow from its request, so a
model that rewrote the file directly would leave frontmatter describing a
workflow that no longer exists. What comes back is a new request, applied
through the same rebuild `px0 workflows edit` performs.

Three rules hold, each because the obvious alternative is worse:

- **Nothing is applied without being shown.** An improvement pass that quietly
  rewrote a scheduled workflow would be the one place px0 stopped listing what
  it was about to do.
- **Tools are never widened on a model's say-so.** A proposal may argue for a
  new tool, and that argument is printed, but the tool only ever arrives
  through the confirm-and-authorize path `px0 workflows new` uses.
- **A complaint about form belongs in a guideline.** "The summary is too long"
  is not a fact about one workflow; it is a standard. A proposal may add rules
  to a guideline, and those are confirmed separately and appended — your own
  wording above them is never touched.

### Marks are what make this worth running

`px0 runs mark` is where the signal comes from. Without a marked run, a
proposal has only execution telemetry to reason over — it will optimize what it
can see (errors, latency) while the real defect goes untouched. When every run
executed cleanly and none is marked, px0 says so and asks whether you want a
proposal anyway.

### `--since AGE`

Only learn from runs newer than this.

- **Input:** a relative span — `<n>d`, `<n>w`, or `<n>h`.

### `--dry-run`

Print the proposal and apply none of it.

### `--show-evidence`

Print exactly what the model would be given, as JSON, and stop. No model call is
made. If you disagree with a proposal, this is what it was reasoning over.

### `--yes`

Skip every prompt, including the confirmation of the rebuild.

### `--no-clarify`, `--no-discover`

Passed through to the rebuild, as on `px0 workflows edit`.

### `--json`

The report and the proposal as JSON, applying nothing.

---

## `px0 workflows recipes`

Sentences to start an interview from.

px0 ships no workflows on purpose — a store full of things you did not ask for
is one you have to read before you can trust it. The cost of that was a blank
page: the hardest part of describing a job is knowing what sort of thing is
describable.

These are sentences, not files. Picking one answers the interview's first
question and nothing else, so every workflow in the store is still one you
asked for.

```shell
px0 workflows recipes
```

The full catalogue of 116 is in [Workflow use cases](../workflow_usecases.md).

---

## `px0 workflows replay`

Run a workflow's instructions against inputs it already had.

`px0 workflows improve` proposes a revision, you apply it, and you find out next
Friday whether it helped. The reason it could not be checked sooner is that a
workflow run twice compares two different worlds — the pull requests moved, the
inbox filled. A **fixture** holds the world still.

Neither the input tools nor the run's own tools are called: the comparison is
about what a workflow *says*, and letting it act would both change the world
and put back the variance the fixture removes.

### Capturing inputs first

Off by default, and deliberately so — a fixture is the content of your work.

```yaml
capture: true            # in the workflow's frontmatter
```

`runs.capture_inputs` turns it on store-wide for someone deliberately gathering
fixtures; a workflow's own `capture: false` still opts out. Fixtures live under
`.state/fixtures/`, never in the store proper, and age out on
`runs.fixture_keep_days` — it is the only place the content of a run's inputs
is written down.

### Comparing two versions

```shell
px0 workflows replay digest --fixtures                     # what has been kept
px0 workflows replay digest                                # today's instructions
px0 workflows replay digest --against ./new-body.md        # both, side by side
px0 workflows replay digest --forget                       # delete the fixtures
```

The diff is printed with a churn figure above it, because the first question
about a revision is whether it changed anything at all — and one that rewrites
every line of a working digest is worth looking at twice however good its
reasoning read.

One fixture is one data point. Replay a second captured run before trusting a
difference.

`px0 workflows improve` offers this in line: where a fixture exists, it shows
what its proposal would have written, before you are asked to accept anything.

---

## `px0 workflows list`

Every workflow id with its description.

- **Arguments:** none.
- A workflow file that fails to parse is reported on its own line rather than
  silently omitted — skipping broken files keeps the rest usable, not hidden.

```shell
px0 workflows list
```

Also printed as one section of `px0 store list`. A disabled workflow is marked.

---

## `px0 workflows show`

Print one workflow: where its file is, what version it is on, and the file
itself.

### `workflow` (required)

- **Input:** a workflow id.

### `--json`

Every frontmatter field plus the body, as one object.

- **Input:** flag, no value. Default off.

```shell
px0 workflows show friday-pr-digest
px0 workflows show friday-pr-digest --json | jq .tools
```

---

## `px0 workflows validate`

Check a workflow without running it: that its frontmatter parses, its tools and
guidelines exist, its inputs are read-only, every input argument resolves to a
real value, its cron expression is valid, and its output target is allowed.

The argument check is the one that catches an unfinished workflow. An argument
left as a placeholder (`owner: <OWNER>`) or referencing something nothing
provides (`author: {{github_username}}`) used to be sent to the connector as
written, and came back as the service's own error — GitHub answers
`<OWNER>/<REPO>` with a 404 "Not Found", which reads as a missing repository
rather than a workflow that was never finished. Both are now reported here, and
by every run before it touches the network.

An argument may reference:

| Reference | Resolves to |
| --------- | ----------- |
| `{{input.<name>}}` | a value passed as `--input <name>=<value>` |
| `{{config.<key>}}` | a key from the store's `config.toml` |
| `{{<earlier_input_id>}}` | the result of an input declared *above* this one |
| `{{now}}` | the current UTC time as an ISO 8601 timestamp |
| `{{today}}`, `{{date}}` | today's date, `YYYY-MM-DD` |
| `{{datetime}}` | the same instant as `now`, filename-safe |
| `{{now-<N><unit>}}` | `N` units ago as an ISO 8601 timestamp — unit `m`, `h`, `d`, or `w`, so `{{now-24h}}` is 24 hours ago |

The clock placeholders are how a workflow says "since yesterday": a scheduled run
cannot be handed a literal timestamp, and `since: {{now-24h}}` is what a
connector's `since` parameter wants. Anything outside this list is an error
rather than a silent empty value.

`output.path` takes the same clock placeholders and nothing else — no input ids,
since a filename cannot hold a tool result. Either brace style works
(`{date}` or `{{date}}`), and values are rendered filename-safe there: `{{now}}`
is `2026-08-24T09-23-53` in a path and `2026-08-24T09:23:53Z` in an argument.
An unknown one is reported here rather than when the run routes its output,
which happens after the model call — a typo in a filename used to cost a whole
run to find.

### `workflow`

- **Input:** a workflow id.
- **Default:** omit it to check every workflow in the store, including files
  that fail to parse at all.

### `--json`

One object per workflow with its errors.

- **Input:** flag, no value. Default off.

```shell
px0 workflows validate
px0 workflows validate friday-pr-digest
```

Exits `1` if anything is invalid, so it works in a pre-commit hook or CI.

---

## `px0 workflows delete`

Remove a workflow, keeping its history.

### `workflow`

- **Input:** a workflow id. Omit to pick from a list.

### `--yes`

Skip the confirmation.

- **Input:** flag, no value. Default off.
- The confirmation names the schedule, if it has one, since a scheduled
  workflow is the one you least want to remove by accident.

```shell
px0 workflows delete old-digest
px0 workflows delete
```

Removing through px0 rather than with `rm` is what keeps the store honest: the
content stays in the object store, the removal appears in `px0 changes list`,
and `px0 changes revert` puts the file back. A hand deletion does none of that.

---

## `px0 workflows rename`

Give a workflow a new id. Renames the file and rewrites the `id` in its
frontmatter, which is what the daemon and every run record use.

### `workflow` (required)

- **Input:** the current id.

### `new-id` (required)

- **Input:** a short slug: letters, digits, dots, dashes, underscores. Refused
  if a workflow already has it.

```shell
px0 workflows rename friday-digest weekly-digest
```

---

## `px0 workflows copy`

Copy a workflow to a new id — the way to fork one that works instead of
describing it again.

### `workflow` (required)

- **Input:** the id to copy.

### `new-id` (required)

- **Input:** the new id. Refused if it is taken.

```shell
px0 workflows copy friday-pr-digest monday-pr-digest
px0 workflows edit monday-pr-digest
```

---

## `px0 workflows disable`

Stop a workflow firing, without deleting it. Sets `enabled: false` in its
frontmatter; the daemon and the cron fallback both skip it.

### `workflow` (required)

- **Input:** a workflow id.

```shell
px0 workflows disable noisy-hourly-report
```

The schedule stays in the file, so enabling it again needs no memory of what the
cron expression was.

---

## `px0 workflows enable`

Let a disabled workflow fire again.

### `workflow` (required)

- **Input:** a workflow id.

```shell
px0 workflows enable noisy-hourly-report
```

---

## The workflow file

Frontmatter fields, beyond what the build writes for you.

| Field | What it does |
| ----- | ------------ |
| `enabled` | `false` parks the workflow: it keeps its file and history, and never fires. Set by `disable`/`enable` |
| `retry` | `{max_attempts: N, backoff_seconds: S}`. Each attempt writes its own run record; the cap is 10 |
| `on_failure` | `{notify: desktop\|tool\|none, channel: <tool id>, target: <where>}`. Overrides the `notify.*` config for this workflow |
| `trigger.schedule` | A cron expression. A scheduled workflow's `output.target` must be `file` |
| `trigger.watch` | `{tool: <read-only tool>, args: {...}, key: <field>, every: 15m}`. Polls the tool and runs when an item it has not seen before appears |
| `timeout` | A duration. Overridden for one run by `--timeout` |

### Holding a write back

Anything that speaks in your name can be drafted rather than sent:

```yaml
confirm: true            # every write this workflow makes waits for you
confirm:                 # or only these
  - slack.post_message
```

The run still happens and still produces its output; only the call that would
leave a mark waits, in [`px0 approvals`](approvals.md), with its arguments shown
in full. `tools.confirm_writes` sets the store-wide default, and a workflow's
own `confirm:` overrides it in both directions.

Naming a tool the workflow cannot call fails validation rather than being
ignored — the failure is silent in the dangerous direction, since a misspelling
would leave the tool you meant to hold back firing without asking.

### Where the output goes

```yaml
output:
  target: inbox          # deliver it to `px0 inbox`
```

```yaml
output:
  target: file
  path: output/digest-{date}.md
  inbox: true            # write the file *and* say it arrived
```

`stdout`, `file`, and `inbox` are the three targets. A scheduled or watched
workflow must use `file` or `inbox`, because nobody is watching a terminal at
6am — and scheduled runs deliver to the inbox automatically unless
`inbox: false` says otherwise.

### Pipelines that can skip a stage

A pipeline is a list of workflow ids, each piped into the next. A stage can say
when it should run:

```yaml
pipeline:
  - find-new-errors
  - workflow: open-tickets
    when: has_output
```

`always` (the default), `has_output`, and `no_output` — three conditions, all
facts about the previous stage's output that px0 can check itself. Anything
richer would be a small language living in frontmatter, and the place for
judgement about what to do next is a workflow body, which is written in English.

A stage whose condition is not met is **skipped, not failed**, and the text it
would have received passes through — so "post it only if there is something to
post" does not break every stage after it. A condition on the first stage fails
validation: there is no previous output to test, so it can only be a mistake
about which stage it belongs to.

### When px0 gives up on a workflow

An unattended workflow that fails the same way `runs.disable_after_failures`
times in a row (5 by default) is parked, and you are told through the same
channel failures use. A dead connector otherwise means an hourly failure and an
hourly notification for the rest of the week, with nothing learning that
nothing has changed.

It requires the *same* cause each time — a workflow failing three different
ways is one to look at, not one stuck. A manual run never trips it: you are
there, reading the error. The park is a versioned change like any other, so
`px0 changes revert` undoes it, and `px0 workflows enable` is the ordinary way
back.

### Which clock a schedule is read against

```yaml
trigger:
  schedule: "0 9 * * 1-5"
  timezone: Asia/Kolkata
```

Without one, schedules follow the machine — which is right until the machine
travels, and wrong twice a year without it moving at all. `schedule.timezone`
sets a store-wide default. A zone this machine does not know fails validation
rather than falling back silently, since a silent fallback looks like it worked
and fires at the wrong hour.

### Watching instead of scheduling

A watch fires on something happening rather than on the clock. px0 polls the
named read-only tool at `every` (at least 60s), identifies each item by `key`
(or by its own `id`, `url`, or `number` if no key is named), and runs the
workflow when something new turns up.

```yaml
trigger:
  watch:
    tool: github.list_my_prs
    args:
      since: "-1d"
    key: url
    every: 30m
    min_items: 1
```

What was new is piped to the run on stdin, so the workflow acts on the items
that triggered it rather than going back to look and getting a different
answer. `min_items` waits until that many have turned up; anything held back
still counts on the next poll.

The first poll only records a baseline, so adding a watch to a busy source does
not immediately fire on everything already there. Composio's own event triggers
need a public endpoint to deliver to, which a laptop does not have; this is what
a local-first tool can do instead.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown workflow id, malformed workflow file, bad `--input`, failed validation, refused confirmation |
| `2` | A tool call failed or its app is not authorized |
| `3` | The coding-agent harness failed, timed out, or is missing |
