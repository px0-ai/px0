# `px0 workflows`

A workflow is a Markdown file with YAML frontmatter describing a job px0 can run:
its trigger, the tools it may call, the guidelines it inlines, and where its
output goes. You describe one in a sentence and px0 builds it.

Implemented by `px0/workflow.py` (the file format), `px0/builder.py` (building
from a description), and `px0/runner.py` (execution).

```
px0 workflows new [description] [--id ID] [--from-file PATH] [--yes] [--no-clarify] [--no-discover]
px0 workflows run [workflow] [--input K=V] [--output {stdout,file}] [--timeout DURATION] [--no-retry] [--dry-run] [--stdin] [--quiet] [--json]
px0 workflows edit [workflow] [--yes] [--no-clarify] [--no-discover]
px0 workflows list
px0 workflows show <workflow> [--json]
px0 workflows validate [workflow] [--json]
px0 workflows rm <workflow> [--yes]
px0 workflows rename <workflow> <new-id>
px0 workflows copy <workflow> <new-id>
px0 workflows disable <workflow>
px0 workflows enable <workflow>
```

---

## `px0 workflows new`

Turn a description into a workflow file. px0 asks what is ambiguous, finds the
tools the job needs, authorizes them, writes the workflow, and saves it under
`workflows/`.

Run it with no description and px0 asks for one — see
[Starting with no description](#starting-with-no-description).

### `description` (optional)

What you want the workflow to do.

- **Input:** a sentence, quoted. Write it the way you would ask a colleague —
  what should happen, to what, and when.
- **Default:** omitted, px0 interviews you for it.

```shell
px0 workflows new "every Friday, summarize merged PRs and post to Slack"
px0 workflows new
```

### Starting with no description

`px0 workflows new` on its own opens an interview. px0 asks for one thing at a
time until it has what a workflow file must pin down:

| | |
| --- | --- |
| The job | what should happen |
| The sources | what it reads: which service, account, repository, channel, folder, or your own notes |
| The delivery | what it produces and where that goes |
| The cadence | on demand, on a schedule, or when something happens |
| Done looks like | what makes the output right rather than merely produced |

That checklist is `builder.WORKFLOW_SPEC`, and it is handed to the model as part
of the prompt — so the questions you get are the fields the plan actually needs,
not whatever the model finds interesting. The same list drives the clarifying
questions asked when you *do* type a description, so there is one definition of
"what is still missing".

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
build spends a planning call: `edit` rewrites it, `n` cancels, anything else
builds. Because the interview has already settled these questions, the clarifying
round is skipped — you are not asked twice.

Not available with `--yes`, or when stdin is not a terminal: there is nobody to
interview, so px0 says so and exits rather than building from nothing.

### `--id ID`

The workflow id, which is also its filename and how you run it.

- **Input:** a short slug.
- **Default:** derived from the description.

```shell
px0 workflows new "..." --id friday-pr-digest
```

### `--yes`

Skip every prompt: no clarifying questions, no confirmations.

- **Input:** flag, no value. Default off.
- For scripted or unattended use. px0 builds from the description as written and
  accepts its own choices.

### `--no-clarify`

Build from the description as written, without asking clarifying questions.

- **Input:** flag, no value. Default off.
- Narrower than `--yes`: confirmations still happen, only the interrogation is
  skipped.

### `--no-discover`

Use only px0's curated tools; skip the Composio catalogue search.

- **Input:** flag, no value. Default off.
- Faster, offline-friendly, and predictable. Use it when the job only needs tools
  px0 already knows about.

### `--from-file PATH`

Read the description from a file instead of the command line.

- **Input:** a path to a text file. Its whole contents become the description.
- **Default:** the `description` argument is used.
- A description worth writing carefully is a paragraph, and a paragraph does not
  survive shell quoting well.

```shell
px0 workflows new "" --from-file ./friday-digest.txt
```

### Guidelines the build writes

Near the end of a build, px0 asks itself whether this workflow leans on a
durable convention — a review rubric, a commit format, a writing voice — that no
file in `guidelines/` covers. When it does, px0 drafts that guideline from the
workflow itself, prints it with the path it would take, and asks:

```
› guideline: Review rubric
  the workflow comments on PRs and has no rubric to comment against
[..] would be saved as  guidelines/review-rubric.md

## Flag only real breakage
...

› Keep it? [Y/again/n]
```

`again` redraws it, `n` skips it, anything else saves it. Saved guidelines are
listed in the new workflow's `guidelines:`, so every run inlines them verbatim.
The path is printed when it is written, and `px0 guidelines edit <name>` is how
you make a draft yours.

This is the only way a guideline gets created — there is no
`px0 guidelines new`. Asking someone to compose a convention from a blank page
is the step that stopped guidelines from being written at all, so the build
drafts a defensible version and leaves editing to you.

Skipped entirely under `--yes`: there is nobody to show a draft to, and a
convention nobody saw should not land in the store.

---

## `px0 workflows run`

Execute a workflow now.

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

Suppress the progress line on stderr. The output itself still prints.

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

### `--yes`, `--no-clarify`, `--no-discover`

As for `new`. `--no-clarify` rebuilds from the new instructions as written.

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
guidelines exist, its inputs are read-only, its cron expression is valid, and
its output target is allowed.

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

## `px0 workflows rm`

Remove a workflow, keeping its history.

### `workflow` (required)

- **Input:** a workflow id.

### `--yes`

Skip the confirmation.

- **Input:** flag, no value. Default off.
- The confirmation names the schedule, if it has one, since a scheduled
  workflow is the one you least want to remove by accident.

```shell
px0 workflows rm old-digest
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
