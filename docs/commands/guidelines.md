# `px0 guidelines`

Guidelines are Markdown files describing how you work — how you word a commit
message, what your Go reviews check. Workflows inline them verbatim, so output
comes back in your voice instead of the model's default.

You do not create them. `px0 workflows new` decides whether the workflow it is
building leans on a durable convention the store has no file for, drafts it, and
lists it on the workflow — see
[guidelines the build writes](workflows.md#guidelines-the-build-writes). What is
here are the operations on a file that already exists.

Implemented by `px0/guidelines.py` (the file format), `px0/authoring.py` (the
files) and `px0/claims.py` (claim identity and history).

## The file

A guideline is frontmatter over rules, the same shape as a skill:

```markdown
---
name: commit-messages
description: How to word a commit message. Use when the workflow writes or rewrites one.
---

## Imperative mood summary line

Write the summary line in the imperative mood: "Add retry logic", not "Added".
Keep it under 72 characters and drop the trailing period.

## Explain why, not what

The diff already shows what changed. Use the body to explain why.
```

| Field | What it is for |
| ----- | -------------- |
| `name` | The guideline's name, which is its filename. A folder groups a topic (`code-review/go.md` is named `go`) and is not part of the name |
| `description` | One sentence: what the convention covers and when a workflow should follow it. This is the only thing `px0 workflows new` matches a new workflow against, so it decides whether the file is ever attached |
| body | The rules, as `## ` sections. This is what a run inlines — the frontmatter never reaches the model |

The description is the whole reason this file has frontmatter. Matching on the
body instead attached `commit-messages.md` to a nightly standup that only
*reads* commits: the words overlapped, the conventions did not.

A file with no frontmatter still works — the name falls back to its filename and
the description to its first rule — but it will rarely be picked, and
`px0 doctor` lists it under `guideline_descriptions`. Add the two lines with
`px0 guidelines edit <name>`.

Guidelines under `guidelines/work/` are listed but never offered to a build, the
same as `brain/work/`.

```
px0 guidelines list
px0 guidelines edit <name>
px0 guidelines show <name>
px0 guidelines rm <name> [--yes]
px0 guidelines log <claim_id>
```

---

## `px0 guidelines list`

Every guideline, numbered, with its description beside it:

```
    1. code-review/go.md  What a Go review checks. Use when the workflow reviews Go code.
    2. review-rubric.md   What a review comments on. Use when the workflow reviews code.
    3. voice.md           How I write prose. Use when the workflow writes for a reader.
```

The same rows `px0 workflows run` uses for its picker, because this is the same
kind of list: a handful of things you scan and then name.

The detail is the frontmatter `description` — the same line a build matches a
new workflow against, so what decides an attachment is what you read here. A
file without one falls back to its first rule.

- **Arguments:** none.
- The number is positional, not an id — commands take the name.
- Also printed as one section of `px0 store list`.

---

## `px0 guidelines edit`

Open a guideline in `$VISUAL`, `$EDITOR`, or the first of `nano`, `vim`, `vi`
that exists. What you save is recorded as a change, so `px0 changes show` diffs
it and `px0 changes revert` undoes it.

This is where a drafted guideline becomes yours: the build writes a defensible
first version from the workflow, and an edit here is how it stops being generic.

Editing the `description` changes which future workflows get this guideline
attached, so it is worth as much care as the rules themselves. Keep the "use
when …" half of it: that is the part a build matches on.

### `name` (required)

- **Input:** the guideline name, with or without `.md`. A file in a subfolder is
  found by its bare name, so `px0 guidelines edit go` opens
  `guidelines/code-review/go.md`.

```shell
px0 guidelines edit commit-messages
```

An unchanged file records nothing.

---

## `px0 guidelines show`

Print one guideline file verbatim, frontmatter included. A run inlines the body
only.

### `name` (required)

- **Input:** the guideline name.

```shell
px0 guidelines show commit-messages
```

---

## `px0 guidelines rm`

Remove a guideline, keeping its history.

### `name` (required)

- **Input:** the guideline name.

### `--yes`

Skip the confirmation.

- **Input:** flag, no value. Default off.
- Workflows that name the guideline are listed first: they will fail validation
  until they stop naming it.

```shell
px0 guidelines rm outdated-voice
```

The content stays in the object store, so `px0 changes revert` puts it back.

---

## `px0 guidelines log`

A claim's edit history: every version, when it changed, and what changed it.

Each `## ` heading in a guideline is a claim with its own id and version chain,
so a rule that changed reads back on its own rather than as a diff of the whole
file. Hand edits are picked up too — px0 notices them on the next command and
records them.

### `claim_id` (required)

- **Input:** a claim id in the form `<file>#<slug>`, for example
  `commit-style.md#summary-line`.

```shell
px0 guidelines log commit-style.md#summary-line
```

Prints JSON.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | No guideline by that name, or an unusable claim id |
