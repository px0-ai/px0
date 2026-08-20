# Getting started

This walks through installing px0, scaffolding a store, and getting
your first real output end to end.

## 1. Install

```shell
curl -fsSL https://raw.githubusercontent.com/arpitbbhayani/px0/main/install.sh | sh
```

The installer bootstraps pipx if it's missing, installs px0 from PyPI,
runs `px0 init`, and offers to install the scheduler daemon. Four
environment variables change what it does:

| Variable | Effect |
| --- | --- |
| `PX0_VERSION` | Pin a version: `PX0_VERSION=0.1.2 sh install.sh` |
| `PX0_CHANNEL` | `beta` installs pre-releases (`pipx install --pip-args=--pre`) |
| `PX0_PREFIX` | Directory for the `px0` binary (sets `PIPX_BIN_DIR`) |
| `PX0_NO_DAEMON` | `true` skips the daemon offer entirely |

Piping into `sh` leaves no terminal on stdin, so the daemon prompt is
skipped automatically -- run `px0 daemon install` yourself afterwards.
`sh install.sh --uninstall` removes the binary and tells you how to
delete the store (it never deletes your data for you).

For a development install from a clone:

```shell
python -m venv venv
source venv/bin/activate
pip install -e '.[dev]'      # the dev extra adds pytest
pytest
```

## 2. Initialize a store

```shell
px0 init
```

This scaffolds `~/.px0` (or `$PX0_HOME` if set): `workflows/`,
`guidelines/`, `knowledge/`, `outputs/`, `skills/`, plus `config.toml`
and a set of starter guideline files you can read, edit, or delete like
any other file. Nothing here is hidden or binary.

`init` also asks for a [Composio](https://composio.dev) API key, which
is what px0 uses to reach Gmail, Slack, GitHub, and Google Calendar. You
can pass it directly, or skip it and set it later:

```shell
px0 init --composio-key <your-api-key>   # non-interactive
px0 config composio <your-api-key>       # or set it up afterwards
```

There is no prompt when stdin isn't a terminal, so `px0 init` is safe in
scripts and installers -- it creates the store and tells you to run
`px0 config composio` when you have a key.

That key is all the connection setup there is. Individual apps authorize
themselves the first time a workflow needs one, so you never connect a
service you turn out not to use.

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

## 3. Confirm it's wired up

```shell
px0 doctor
```

Every line should be a `✓`. `doctor` exits `0` when everything passes,
`4` when any check fails, so it works in a health-check script. On a
freshly initialized store you'll see something like:

```
✓ credentials              mode 0o600
✓ versions                 manifest opens cleanly
✓ locks                    no lock file yet
✓ schema                   store schema 1, binary schema 1
✓ connections              0 connection(s) configured
✓ unreferenced_guidelines  6 unreferenced file(s) -- see `px0 consolidate`
✓ update                   0.1.0 (no update check recorded yet)
✓ daemon                   not running
✓ harness                  harness responded
✓ index                    0 knowledge files, 0 indexed passages
```

The starter guidelines are reported as unreferenced because no workflow
lists them yet -- that's expected on a new store, not a fault.
`harness  harness responded` means px0 actually invoked your coding
agent CLI and got something back; if that line fails, fix it before
anything else, because every workflow depends on it.

### Colour, animation, and pipes

On a terminal px0 uses colour sparingly (a failure, a value you can act
on) and shows a spinner while something slow is happening. Pipe it
anywhere and all of that disappears: no escape sequences, no spinner
redraws, and status glyphs become `[OK]` / `[FAIL]` so output stays
greppable.

```shell
px0 doctor | grep FAIL          # plain automatically
px0 --no-color doctor           # plain on a terminal too
NO_COLOR=1 px0 doctor           # same, via the environment
FORCE_COLOR=1 px0 doctor | less -R   # keep colour through a pipe
```

`--json` implies plain output everywhere -- machine-readable data is
never decorated.

## 4. Get your first output

`px0 init` deliberately ships **no** starter workflows -- the point of
px0 is that you describe what you want and it writes the file. Two paths
give you something real in one command.

**The knowledge library**, which needs no workflow and no connection:

```shell
px0 knowledge add https://example.com/some-post
px0 ask "what did that post say about caching?"
```

`knowledge add` extracts the text locally and files it under
`knowledge/`; `ask` retrieves the relevant passages and answers from
them, citing what it used with `--sources`. See
[04-knowledge-and-ask.md](04-knowledge-and-ask.md).

**A generated workflow**, which is the main loop:

```shell
px0 new "summarize any URL I paste and print the summary"
```

px0 plans it, shows you the plan, asks for confirmation, and writes a
Markdown file under `workflows/`. Then:

```shell
px0 run summarize-any-url --stdin <<< "https://example.com/some-post"
```

See [02-building-a-workflow.md](02-building-a-workflow.md) for the whole
flow, including what happens when the plan needs a connection you don't
have yet.

## 5. See what's there

```shell
px0 list workflows
px0 list guidelines
px0 list knowledge
px0 tools list          # every tool a workflow can call, read vs write
```

## 6. Check the run

```shell
px0 runs                # interactive browser: filter, drill in, rerun
px0 runs list           # same rows, plain text
px0 runs show <run-id>  # the full JSON record
```

Every run is recorded -- inputs, guideline versions used, tool calls with
timings, and outcome -- so `px0 why <run-id>` can later explain exactly
how an output came to be. See
[07-browsing-runs.md](07-browsing-runs.md) and
[05-guidelines-and-provenance.md](05-guidelines-and-provenance.md).

## Next

- [02-building-a-workflow.md](02-building-a-workflow.md) -- generate a new
  workflow from a plain-English description.
- [03-connections-and-tools.md](03-connections-and-tools.md) -- connect
  Gmail, Slack, GitHub, or Calendar so workflows can reach them.
- [09-updating-px0.md](09-updating-px0.md) -- upgrading and rolling back.
