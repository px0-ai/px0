# px0

A local-first CLI where everything the system does is a workflow.

Everything px0 knows lives in two folders inside the store: `guidelines/` for how
you work, and `brain/` for what you have read and kept. Both are plain Markdown
you can open in any editor.

## Documentation map

| Section | What is in it |
| ------- | ------------- |
| [Command reference](commands/index.md) | Every command group, verb, and option |
| [Workflow use cases](workflow_usecases.md) | 116 jobs to build, and the apps each one touches |
| [Configuration keys](reference/configuration.md) | Every `config.toml` key, its type and effect |
| [Store layout](reference/store-layout.md) | What each folder and state file holds |

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
| `brain/`      | What you have read and kept                            |
| `output/`     | What runs produced                                     |
| `tools/`      | Tools you declared yourself, one TOML file each         |
| `skills/`     | Guidelines compiled into agent skill bundles           |
| `.state/`     | Runtime internals: version history, index, credentials |

`config.toml` sits at the store root. See [Store layout](reference/store-layout.md).
