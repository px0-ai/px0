# px0

A local-first CLI where everything the system does is a workflow.
Everything it knows lives in two folders inside a plain directory
(`~/.px0` by default): `guidelines/` for how you work, `knowledge/` for
what you've read and kept. Workflows are Markdown files you can read,
edit, and run manually or on a schedule. Nothing is versioned by git --
px0 keeps its own history so the store works as a bare directory with
nothing else installed alongside it.

See [spec.md](spec.md) for the full design.

## Prerequisites

- **Python 3.10+** (with `pip` and `venv`)
- **Node.js and npx**: Required for managing community agent skills with `px0 skills`.
  To install Node.js (which includes `npx`):
  - **macOS (Homebrew):** `brew install node`
  - **Ubuntu / Debian:** `sudo apt update && sudo apt install -y nodejs npm`
  - **nvm (Node Version Manager):** `nvm install --lts`
  - **Official installer / other platforms:** Download from [nodejs.org](https://nodejs.org)
- **Composio API key**: px0 uses Composio for tool integrations.

## Install

```shell
curl -fsSL https://raw.githubusercontent.com/arpitbbhayani/px0/main/install.sh | sh
```

The installer bootstraps pipx, installs px0 from PyPI, and initializes a
store. For a development install from a clone:

```shell
python -m venv venv
source venv/bin/activate
pip install -e '.[dev]'
pytest
```

This puts the `px0` command on your `PATH` via the `[project.scripts]`
entry point in `pyproject.toml`.

## Hello world

`px0 init` ships no workflows -- you describe one and px0 writes the file:

```shell
px0 init
px0 workflows new "summarize a URL on stdin" --id summarize
px0 workflows run summarize --stdin <<< "https://example.com/some-post"
```

Or skip workflows entirely and use the knowledge library:

```shell
px0 knowledge add https://example.com/some-post
px0 knowledge ask "what did that post say about caching?"
```

`px0 init` scaffolds the store directory. `px0 workflows new` creates a workflow
from your prompt (`--id` names it; otherwise px0 suggests one from the
description). `px0 workflows run` executes it.

```shell
px0 workflows list
px0 runs list
```

## Model backend

px0 shells out to a coding agent CLI in non-interactive mode as its
model backend, reusing that CLI's own auth, model choice, and rate
limits -- there is no direct-API backend. `claude -p` is the default;
`px0 init --harness <name>` picks the right invocation for `claude`,
`gemini`, `pi`, or `opencode` instead. Any other command works too, set
directly as `model.harness_cmd` in `config.toml`.

## Skills

px0 supports managing agent skills via the `px0 skills` command, which acts as a proxy for the `npx skills` utility (`skills@latest`):

```shell
px0 skills search "github"        # search community skills (same as `npx skills search`)
px0 skills add composio/github    # install a skill (same as `npx skills add`)
px0 skills list                   # list installed skills (same as `npx skills list`)
px0 skills update                 # update installed skills (same as `npx skills update`)
px0 skills remove <skill>         # remove an installed skill (same as `npx skills remove`)
```

To compile your local `guidelines/` into agent skill bundles (`skills/<name>/SKILL.md`) and automatically symlink them into `~/.claude/skills/` (when using Claude Code):

```shell
px0 skills build
```

## Documentation

Step-by-step walkthroughs live in [docs/tutorials/](docs/tutorials/):

1. [Getting started](docs/tutorials/01-getting-started.md)
2. [Building a workflow](docs/tutorials/02-building-a-workflow.md)
3. [Connections and tools](docs/tutorials/03-connections-and-tools.md)
4. [Knowledge and ask](docs/tutorials/04-knowledge-and-ask.md)
5. [Guidelines and provenance](docs/tutorials/05-guidelines-and-provenance.md)
6. [Scheduling and the daemon](docs/tutorials/06-scheduling-and-the-daemon.md)
7. [Browsing runs](docs/tutorials/07-browsing-runs.md)
8. [Skills and agent bundles](docs/tutorials/08-skills.md)
9. [Updating px0](docs/tutorials/09-updating-px0.md)

- `python scripts/gen_docs.py` regenerates [docs/reference.md](docs/reference.md),
  an API reference generated from the docstrings in `px0/*.py`.
