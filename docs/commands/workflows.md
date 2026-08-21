# `px0 workflows`

A workflow is a Markdown file with YAML frontmatter describing a job px0 can run:
its trigger, the tools it may call, the guidelines it inlines, and where its
output goes. You describe one in a sentence and px0 builds it.

Implemented by `px0/workflow.py` (the file format), `px0/builder.py` (building
from a description), and `px0/runner.py` (execution).

```
px0 workflows new <description> [--id ID] [--yes] [--no-clarify] [--no-discover]
px0 workflows run [workflow] [--input K=V] [--output {stdout,file}] [--dry-run] [--stdin] [--quiet] [--json]
px0 workflows edit [workflow] [--yes] [--no-clarify] [--no-discover]
px0 workflows list
```

---

## `px0 workflows new`

Turn a description into a workflow file. px0 asks what is ambiguous, finds the
tools the job needs, authorizes them, writes the workflow, and saves it under
`workflows/`.

### `description` (required)

What you want the workflow to do.

- **Input:** a sentence, quoted. Write it the way you would ask a colleague —
  what should happen, to what, and when.

```shell
px0 workflows new "every Friday, summarize merged PRs and post to Slack"
```

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

### `--late-scheduled-at` (internal)

Hidden from `--help`, and not intended to be typed. The daemon passes it when
catching up a fire it missed, so the run is recorded with a `late` trigger and a
note saying when it was due against when it actually ran.

---

## `px0 workflows edit`

Revise a workflow's instructions and rebuild it. Same build pipeline as `new`,
starting from what the workflow already says. The previous version stays in the
store's history, so `px0 versions` can show or revert it.

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

Also printed as one section of `px0 store list`.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown workflow id, malformed workflow file, bad `--input` |
| `2` | A tool call failed or its app is not authorized |
| `3` | The coding-agent harness failed, timed out, or is missing |
