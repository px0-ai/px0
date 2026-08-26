# `px0 memory`

What px0 knows about you, as opposed to what you have read.

The store already held two kinds of knowledge and neither was this one.
`brain/` is material you deliberately ingested — posts, papers, docs — and it
answers questions about the world. `guidelines/` is conventions you wrote down
and it shapes how output reads. Between them there was nothing that remembered
*you*: that standup goes out before 09:30, that "the API repo" means one
particular repository, that a person you keep mentioning is on your team.

So every run started from nothing, and every correction had to be made again
the next time. That is the difference between a tool you operate and an
assistant.

Implemented by `px0/memory.py`.

```
px0 memory [list] [--json]
px0 memory add "<text>" [--kind KIND] [--subject WHAT] [--pin]
px0 memory show <name> [--json]
px0 memory forget <name>
px0 memory search "<query>" [--json]
px0 memory suggest [--yes] [--json]
```

## What it looks like on disk

One Markdown file per fact, under `memory/`, in the same shape as everything
else in the store:

```markdown
---
kind: preference
subject: standup timing
learned: 2026-08-26
---

Standup goes out before 09:30, and never mentions unfinished work.
```

Kinds are `fact`, `preference`, `person`, `project`, and `place`. They are a
filing aid, not a schema.

## Two things follow from that

**A memory is editable, because it will be wrong.** px0 writes these as a side
effect of conversations, and an assistant that silently accumulates
unreviewable beliefs about you is the failure mode to design against, not a
feature. Every one is a file you can open, correct, or delete — and every write
is a versioned change, so `px0 changes list` shows what px0 learned and when,
and `px0 changes revert` unlearns it.

**Memory never leaves the machine on its own.** It is inlined into prompts the
same way guidelines are — into the harness you already trust — and goes nowhere
else. `px0 store export` carries it, because an export is how a store reaches
your other machine and an assistant that forgets everything on the new one is
not one.

## How a run uses it

Every run gets the memories relevant to its own instructions, chosen by local
arithmetic rather than a model call or the retrieval index — memories are
short, few, and read on every run, so paying for an embedding pass to choose
between forty lines of text would cost more than inlining all of them.

Pinned memories come first and are never crowded out. Past `memory.budget_chars`
the rest wait, so a store that has been running for a year does not turn every
prompt into a biography.

A workflow can place the block itself with `{{memory}}` in its body; otherwise
it goes above the guidelines and the instructions. `px0 runs why` reports which
memories a run used, which is the first thing you want when a run behaves in a
way the instructions alone do not explain.

## How a run writes to it

Two tools, which a workflow can be given like any others:

| Tool | What it does |
| ---- | ------------ |
| `memory.remember` | Write one fact down. A write tool, so it can be held for approval |
| `memory.recall` | Look up what px0 remembers about something mid-run |

`recall` is for when what needs looking up depends on what the run found — a
name in a pull request, a project mentioned in an email.

## `px0 memory list`

Everything px0 remembers, with its kind and what it is about. A pinned memory
is marked `*`.

```shell
px0 memory list
px0 memory list --json
```

---

## `px0 memory show`

One memory in full: its frontmatter and the fact itself.

```shell
px0 memory show standup-timing
```

The name is the filename without its extension, which is what `list` prints.

---

## `px0 memory search`

What px0 remembers about something.

```shell
px0 memory search "which repo is the API in"
```

Ranked by relevance alone — unlike what a *run* is given, where pinned memories
come first. Pinning is a claim about what every run should see, and letting it
outrank the query here would answer a question nobody asked.

---

## `px0 memory add`

### `--kind KIND`

One of `fact`, `preference`, `person`, `project`, `place`.

### `--subject WHAT`

What it is about, in a few words. Also what the file is named after.

### `--pin`

Always include this, whatever else is competing for room in the budget.

Writing to a subject that already exists **replaces** that memory rather than
adding a second: a fact that has changed is not two facts, and a folder holding
both will contradict itself inside a prompt.

## `px0 memory suggest`

What px0 thinks it should remember, from corrections you have already made.

Memory that only fills up when you remember to fill it is a notepad, which is
the failure it was built to fix. Two places a standing fact tends to be sitting
in plain sight, both of which px0 was already recording and neither of which it
was reading:

- A run you marked bad, with its note. "It covered last week" is a bug; "my
  week runs Monday to Friday" is a fact, and people write the second while
  reporting the first.
- A correction inside a conversation — see [`px0 ask`](ask.md).

```shell
px0 runs mark run_20260826-090000-a1b2 --bad "it covered last week; mine is Mon-Fri"
px0 memory suggest
```

px0 **proposes** and you accept. Nothing is written without a yes, anything
already remembered under the same subject is dropped before you see it, and a
model reply it cannot read suggests nothing rather than failing the command.
`--yes` keeps all of them, for someone reviewing a batch they already trust.

## `px0 memory forget`

Removes it. The history stays, so `px0 changes revert` brings it back.

## Related configuration

| Key | Effect |
| --- | ------ |
| `memory.enabled` | Whether runs inline memory at all |
| `memory.budget_chars` | How much remembered text one run may inline |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Empty text, an unknown kind, or a name px0 does not have |
