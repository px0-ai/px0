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

This is a Python package; install it into a virtualenv:

```shell
python -m venv venv
source venv/bin/activate
pip install -e .
```

This puts the `px0` command on your `PATH` via the `[project.scripts]`
entry point in `pyproject.toml`.

## Hello world

```shell
px0 init
px0 new "summarize a URL on stdin"
echo "https://example.com/some-post" | px0 run summarize --stdin
```

`px0 init` scaffolds the store directory. `px0 new` creates a workflow
from your prompt. `px0 run` executes it. 

```shell
px0 list workflows
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

- [docs/tutorials/](docs/tutorials/) -- step-by-step walkthroughs:
  getting started, building a workflow from a description, knowledge and
  `ask`, guideline provenance, and skills management.
- `python scripts/gen_docs.py` regenerates [docs/reference.md](docs/reference.md),
  an API reference generated from the docstrings in `px0/*.py`.
