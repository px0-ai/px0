# 1. Architecture

px0 is a local-first CLI. It has no server, no account, and no cloud state. Everything it knows sits in one directory you own, as plain Markdown and TOML you can open in any editor.

That constraint is not a limitation to work around. It is the thing every other decision follows from.

## The three-sentence description

You describe a recurring chore in English. px0 interviews you, finds the tools the job needs, and writes a workflow file. From then on you can run it, schedule it, inspect what it did, and revise it from evidence about how it behaved.

## What px0 is not

Knowing what is absent explains more of the code than knowing what is present.

- There is no model API client. px0 has no provider key and never talks to Anthropic, OpenAI, or Google. It shells out to a coding agent CLI you already sign into (`claude -p`, `gemini -p`, `pi -p`, `opencode run`) and reads text back. See [part 7](07-harness.md).
- There is no daemon you must run. The scheduler is optional. Every command works without it.
- There is no database of record. SQLite appears once, as an index over content-addressed blobs, and it is derived from files that already exist on disk.
- There is no webhook endpoint. A laptop has no public address, so event triggers are polling. See [part 11](11-daemon.md).
- There is no server-side tool registry. Tool definitions are cached into the store so a workflow keeps working offline and unchanged after it is written.

## The layers

```
  cli.py / commands.py        what a person types, and what gets printed
  parser.py                   the argparse tree, kept separate from the handlers
  ui.py                       colour, glyphs, spinners, one voice

  builder.py  improve.py      turning English into a workflow, and revising one
  route.py    ask.py          turning a question into a destination
  runner.py                   executing a workflow, eight stages
  daemon.py                   deciding when a workflow should execute

  workflow.py                 the file format and its validation rules
  tools.py    localtools.py   what a workflow may call
  catalogue.py  connect.py    discovering and authorizing external tools
  brain.py    retrieval.py    what you have read, and finding parts of it
  guidelines.py memory.py     conventions, and what px0 knows about you
  approvals.py  notify.py     writes that wait for a person, and telling them

  versioning.py claims.py     history for everything the store holds
  runs.py     analysis.py     what happened, and what it means
  store.py    config.py       the store itself
  paths.py                    where everything lives
```

The dependency direction runs downward. `cli` imports everything; `paths` imports nothing but the standard library. Where a lower layer needs something from a higher one -- `runner` reaching into `analysis` for the budget check, `versioning` being called from `store` -- the import is deferred inside the function that needs it, with a comment saying why.

## The split between deciding and doing

Two pairs of modules exist because of one rule: the part that computes an answer must not be the part that prints it.

`builder.py` holds pure planning functions. Every prompt, spinner, and confirmation for `px0 workflows new` lives in `cli.py`. That is why `builder.generate_plan` takes a config and a description and returns a `Plan`, and never asks anything.

`analysis.py` computes findings from run records with no model call and no network. `improve.py` takes those findings and asks a model what to do about them. The split matters because a proposal is only as honest as the numbers under it, and numbers a model produced would be circular. See [part 13](13-feedback.md).

`runner.route_output` writes files and returns a description of what happened. It never prints, because stdout routing is a CLI decision and because `--json` needs plain stdout.

## Everything is a file

The store holds five kinds of content, and all five are Markdown with YAML frontmatter:

| Folder | What it is | Written by |
| ------ | ---------- | ---------- |
| `workflows/` | Jobs px0 can run | `px0 workflows new`, or you |
| `guidelines/` | Conventions px0 follows | The builder, or you |
| `memory/` | What px0 knows about you | Conversations and runs, or you |
| `brain/` | What you have read and kept | `px0 brain add`, or your notes vault |
| `output/` | What runs produced | Runs |

The frontmatter is the machine contract and the body is the prose. A workflow's frontmatter says which tools it may call; its body is the prompt the model receives. A guideline's frontmatter says what the file covers; its body is inlined verbatim into a run. A brain file's frontmatter records where the content came from and when.

Nothing needs compiling. Edit a file by hand and the next command picks it up, records the edit in the version history, and carries on. See [part 3](03-versioning.md).

## Failure posture

Three rules recur across the codebase, and each one is a rule because the alternative was observed to be worse.

One bad file must not hide the good ones. `workflow.load_all` skips unparseable files rather than raising, and `load_errors` reports them separately. Before that, a single YAML typo took down `workflows list`, `doctor`, and the daemon at once.

Telemetry must never cost a run. `runs.append_event` swallows every exception. `replay.capture` returns `None` on failure. The harness downgrades its own reporting flags rather than failing a call. A run that produced real work must not be lost to a log directory that could not be written.

Refuse rather than silently do the wrong thing. A misspelled timezone is refused, because falling back to machine local time would fire at the wrong hour and look like it worked. A `confirm:` entry naming a tool the workflow cannot call is refused, because ignoring it would send a message the user thought was held back.

## Exit codes

`cli.main` maps exception classes to exit codes so a script can branch on the kind of failure.

| Code | Meaning | Raised by |
| ---- | ------- | --------- |
| `0` | Success | |
| `1` | User error | `WorkflowError`, `AuthoringError`, `StoreError`, `LocalToolError`, `ValueError`, interruption |
| `2` | Connector error | `ConnectorError`, `CatalogueError` |
| `3` | Model error | `HarnessError` |
| `4` | Integrity error | `px0 doctor` failing a check |

## Where to go next

[Part 2](02-store-and-config.md) covers the store those files live in, and the configuration schema that drives most of the behaviour described in the rest of the series.
