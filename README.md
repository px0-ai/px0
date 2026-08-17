# px0

A local-first CLI where everything the system does is a workflow.
Everything it knows lives in two folders inside a plain directory
(`~/.px0` by default): `guidelines/` for how you work, `knowledge/` for
what you've read and kept. Workflows are Markdown files you can read,
edit, and run manually or on a schedule. Nothing is versioned by git --
px0 keeps its own history so the store works as a bare directory with
nothing else installed alongside it.

See [spec.md](spec.md) for the full design.

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
git diff | px0 run pr-precheck --stdin
```

`px0 init` scaffolds the store with a handful of starter workflows and
guidelines. `pr-precheck` is one of them: it reads a diff on stdin,
checks it against the code-review guidelines, and prints any violations
-- no external connection required, so it's the fastest way to see px0
do something real.

```shell
px0 list workflows
px0 runs list
```

## Documentation

- [docs/tutorials/](docs/tutorials/) -- step-by-step walkthroughs:
  getting started, building a workflow from a description, knowledge and
  `ask`, and guideline provenance.
- `python scripts/gen_docs.py` regenerates [docs/reference.md](docs/reference.md),
  an API reference generated from the docstrings in `px0/*.py`.
