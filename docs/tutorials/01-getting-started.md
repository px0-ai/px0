# Getting started

This walks through installing px0, scaffolding a store, and running your
first workflow end to end.

## 1. Install

```shell
python -m venv venv
source venv/bin/activate
pip install -e .
```

This installs the `px0` command into your virtualenv (via the
`[project.scripts]` entry point in `pyproject.toml`).

## 2. Initialize a store

```shell
px0 init
```

This scaffolds `~/.px0` (or `$PX0_HOME` if set): `workflows/`,
`guidelines/`, `knowledge/`, `outputs/`, plus a handful of starter
workflows and guideline files you can read, edit, or delete like any other
file. Nothing here is hidden or binary.

During initialization, it will also prompt you for a Composio API key. Setting this up allows px0 to orchestrate tool connections. You can also provide it directly:

```shell
px0 init --composio-key <your-api-key>
```

By default the model backend is `claude -p`. If you use a different
coding agent CLI as your backend, pass `--harness` at init time:

```shell
px0 init --harness gemini     # or pi, or opencode
```

This just picks the right non-interactive invocation for that CLI and
writes it to `model.harness_cmd` in `config.toml`; you can also edit
`harness_cmd` there directly to point at any other command.

To switch later, or to pick a specific model (not just the harness), run
`px0 config model`. It lists the harnesses it knows about with their PATH
status, takes a model name to select, and actually invokes the result
before saving so a typo'd model name or a missing API key gets caught
there instead of mid-workflow. It never asks for or stores a key itself --
that stays with the harness CLI's own login or environment variable. For
everything else, `px0 config list|get|set` reads and writes any key in
`config.toml`, validated against that key's type and allowed values.

## 3. Run your first workflow

`summarize` is one of the starters. It takes a URL, a local file path, or
raw pasted text on stdin and summarizes it -- no external connection
required, which makes it the fastest way to see px0 actually do
something.

```shell
echo "https://example.com/some-post" | px0 run summarize --stdin
```

`pr-precheck` is another connection-free starter: it takes a diff on
stdin, checks it against the code-review guidelines, and prints
violations to stdout.

```shell
git diff | px0 run pr-precheck --stdin
```

You'll see either `no violations found` or a list of flagged lines, each
naming the guideline it broke. That's the whole loop: a workflow file
declares inputs, guidelines, and an output target; `px0 run` resolves the
inputs, inlines the guidelines into a prompt, and hands it to the model
harness.

## 4. See what's there

```shell
px0 list workflows
px0 list guidelines
```

## 5. Check the run

```shell
px0 runs list
px0 runs show <run-id>
```

Every run is recorded -- inputs, guideline versions used, tool calls, and
outcome -- so `px0 why <run-id>` can later explain exactly how an output
came to be. See [04-guidelines-and-provenance.md](04-guidelines-and-provenance.md).

## Next

- [02-building-a-workflow.md](02-building-a-workflow.md) -- generate a new
  workflow from a plain-English description.
- [03-knowledge-and-ask.md](03-knowledge-and-ask.md) -- build a personal
  knowledge library and query it.
- [05-skills.md](05-skills.md) -- manage agent skills and compile guideline bundles.
