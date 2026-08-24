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
```

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
