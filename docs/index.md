# px0

A local-first CLI where everything the system does is a workflow.

Everything px0 knows lives in two folders inside the store: `guidelines/` for how
you work, and `brain/` for what you have read and kept. Both are plain Markdown
you can open in any editor.

## Documentation map

| Section | What is in it |
| ------- | ------------- |
| [Command reference](commands/index.md) | Every command group, verb, and option |
| [Internals](internals/index.md) | How px0 is built, in 18 parts, and the reasoning behind each piece |
| [Workflow use cases](workflow_usecases.md) | 116 jobs to build, and the apps each one touches |
| [Configuration keys](reference/configuration.md) | Every `config.toml` key, its type and effect |
| [Store layout](reference/store-layout.md) | What each folder and state file holds |

## Asking it things

| Command | What it is |
| ------- | ---------- |
| [`px0 ask`](commands/ask.md) | One question, routed to memory, your brain, a workflow, a tool, or none of them |
| [`px0 inbox`](commands/inbox.md) | What your scheduled workflows produced, somewhere you will actually look |
| [`px0 approvals`](commands/approvals.md) | Write calls drafted in full and waiting for you |
| [`px0 memory`](commands/memory.md) | What px0 has been told about you, as files you can correct |

## The improvement loop

A workflow you wrote once was right once. px0 keeps enough about every run to
say what has happened to it since, in two halves:

| Command | What it is |
| ------- | ---------- |
| [`px0 workflows health`](commands/workflows.md#px0-workflows-health) | Arithmetic over your own run records. No model call, no network — every finding is one you can check yourself |
| [`px0 runs mark`](commands/runs.md#px0-runs-mark) | Whether a run's output was any *good* — the one thing no record can infer |
| [`px0 workflows improve`](commands/workflows.md#px0-workflows-improve) | A revision argued from that evidence, shown as a diff before anything is applied |
| [`px0 workflows replay`](commands/workflows.md#px0-workflows-replay) | The old wording and the new one over the same captured inputs, so a revision is checked rather than hoped for |
| [`px0 memory suggest`](commands/memory.md#px0-memory-suggest) | Standing facts px0 spotted in your corrections, offered rather than assumed |

## Install

```shell
pipx install px0
px0 init
px0 doctor
```

`px0 init` scaffolds the store, `px0 doctor` confirms everything is wired up.

## The store

Your store is `~/.px0` unless `PX0_HOME` says otherwise.

| Folder        | What is in it                                          |
| ------------- | ------------------------------------------------------ |
| `workflows/`  | The jobs px0 can run                                   |
| `guidelines/` | How you work                                           |
| `memory/`     | What px0 knows about you                               |
| `brain/`      | What you have read and kept                            |
| `output/`     | What runs produced                                     |
| `tools/`      | Tools you declared yourself, one TOML file each         |
| `.state/`     | Runtime internals: version history, index, credentials |

`config.toml` sits at the store root. See [Store layout](reference/store-layout.md).
