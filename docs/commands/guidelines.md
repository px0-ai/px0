# `px0 guidelines`

Guidelines are Markdown files describing how you work — how you word a commit
message, what your Go reviews check. Workflows inline them verbatim, so output
comes back in your voice instead of the model's default.

You do not create them. `px0 workflows new` decides whether the workflow it is
building leans on a durable convention the store has no file for, drafts it, and
lists it on the workflow — see
[guidelines the build writes](workflows.md#guidelines-the-build-writes). What is
here are the operations on a file that already exists.

Implemented by `px0/authoring.py` (the files) and `px0/claims.py` (claim
identity and history).

```
px0 guidelines list
px0 guidelines edit <name>
px0 guidelines show <name>
px0 guidelines rm <name> [--yes]
px0 guidelines log <claim_id>
```

---

## `px0 guidelines list`

Every guideline, numbered, with its first rule beside it:

```
    1. code-review/go.md  Prefer table-driven tests
    2. review-rubric.md   Flag only real breakage
    3. voice.md           Lead with the takeaway
```

The same rows `px0 workflows run` uses for its picker, because this is the same
kind of list: a handful of things you scan and then name.

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

Print one guideline verbatim — the same text a workflow inlines.

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
