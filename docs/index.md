# px0

A local-first CLI where everything the system does is a workflow.
Everything it knows lives in two folders inside a plain directory
(`~/.px0` by default): `guidelines/` for how you work, `knowledge/` for
what you've read and kept. Workflows are Markdown files you can read,
edit, and run manually or on a schedule. Nothing is versioned by git --
px0 keeps its own history so the store works as a bare directory with
nothing else installed alongside it.

## Install

```shell
curl -fsSL https://raw.githubusercontent.com/arpitbbhayani/px0/main/install.sh | sh
```

The installer bootstraps pipx, installs px0, and runs `px0 init`. For a
development install, or to see every environment knob the installer
takes, see [Getting started](tutorials/01-getting-started.md).

## Hello world

`px0 init` scaffolds the store and a set of starter guidelines. It does
**not** ship starter workflows -- you describe what you want and px0
writes the workflow file:

```shell
px0 init
px0 workflows new "post a summary of my open pull requests to slack every morning"
```

`px0 workflows new` asks what's ambiguous, searches Composio's catalogue for the
tools the task needs, shows you what it picked before authorizing
anything, and writes the workflow file.

The fastest path that needs no workflow at all is the knowledge library:

```shell
px0 knowledge add https://example.com/some-post
px0 knowledge ask "what did that post say about caching?"
```

```shell
px0 workflows list      # what you can run
px0 runs                # interactive browser over past runs
px0 doctor              # is everything wired up
```

## Model backend

px0 shells out to a coding agent CLI in non-interactive mode as its
model backend, reusing that CLI's own auth, model choice, and rate
limits -- there is no direct-API backend. `claude -p` is the default;
`px0 init --harness <name>` picks the right invocation for `claude`,
`gemini`, `pi`, or `opencode` instead. Any other command works too, set
directly as `model.harness_cmd` in `config.toml`.

## Connections

Tools that reach outside px0 -- GitHub, Gmail, Slack, Google Calendar --
all route through [Composio](https://composio.dev). There is one API key
to set up, and after that apps authorize themselves: the first time a
workflow needs Gmail, px0 prepares Gmail's authorization and prints the
URL to approve.

```shell
px0 config composio <composio-api-key>   # once; px0 init also asks
px0 tools list --status                  # what a workflow can call, and what's ready
```

There is no per-app connect command -- nothing to look up, and nothing to
connect that you turn out not to need. See
[Connections and tools](tutorials/03-connections-and-tools.md).

## Where to go next

| If you want to | Read |
| --- | --- |
| Install and run something end to end | [Getting started](tutorials/01-getting-started.md) |
| Turn a sentence into a workflow file | [Building a workflow](tutorials/02-building-a-workflow.md) |
| Understand how tools get chosen | [Building a workflow](tutorials/02-building-a-workflow.md) |
| Reach Gmail, Slack, GitHub, Calendar | [Connections and tools](tutorials/03-connections-and-tools.md) |
| Build a searchable library and query it | [Knowledge and ask](tutorials/04-knowledge-and-ask.md) |
| Understand how guidelines evolve | [Guidelines and provenance](tutorials/05-guidelines-and-provenance.md) |
| Run workflows on a schedule | [Scheduling and the daemon](tutorials/06-scheduling-and-the-daemon.md) |
| Inspect what a past run actually did | [Browsing runs](tutorials/07-browsing-runs.md) |
| Load your guidelines into `claude` sessions | [Skills and agent bundles](tutorials/08-skills.md) |
| Upgrade or roll back px0 | [Updating px0](tutorials/09-updating-px0.md) |

The [API reference](reference.md) is generated from the source
docstrings and covers every module, class, and function.
