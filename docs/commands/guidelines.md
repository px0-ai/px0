# `px0 guidelines`

Guidelines are Markdown files describing how you work — how you word a commit
message, what your Go reviews check. Workflows inline them verbatim, so output
comes back in your voice instead of the model's default.

px0 proposes guideline edits from what you read; you accept or reject them. Every
claim keeps its provenance, so you can ask why it says what it says.

Implemented by `px0/proposals.py` (proposing and applying) and `px0/claims.py`
(claim identity, history, aliases).

```
px0 guidelines list
px0 guidelines review [--list-only]
px0 guidelines consolidate [--list-only]
px0 guidelines log <claim_id>
px0 guidelines why <claim_id>
px0 guidelines revert <claim_id> --to VERSION
px0 guidelines alias list
px0 guidelines alias link <old> <new>
px0 guidelines alias unlink <old>
```

---

## `px0 guidelines list`

Every guideline file, relative to `guidelines/`.

- **Arguments:** none.
- Also printed as one section of `px0 store list`.

---

## `px0 guidelines review`

Walk the pending proposals — the guideline edits px0 drew from material you
ingested — and accept, edit, or dismiss each one. Accepted proposals are applied
together as a single change, so the store's history shows one reviewed batch.

Each proposal shows its target file, the action (`new`, `amend`, `retire`), the
claim, its body, and the evidence it came from.

### `--list-only`

Print the pending proposals without prompting for a decision.

- **Input:** flag, no value. Default off — the command is interactive.
- Use it to see what is waiting, or from a script where prompting would hang.

```shell
px0 guidelines review
px0 guidelines review --list-only
```

---

## `px0 guidelines consolidate`

Merge overlapping claims and surface stale files. Proposes the merges as
reviewable proposals rather than rewriting anything directly.

### `--list-only`

Print what consolidation would propose, without prompting. Default off.

Capped by `proposals.max_per_consolidation` (ships as `10`) so one session
surfaces a reviewable amount.

---

## `px0 guidelines log`

A claim's edit history: every version, when it changed, and what changed it.

### `claim_id` (required)

- **Input:** a claim id in the form `<file>#<slug>`, for example
  `commit-style.md#summary-line`. Aliases are accepted.

```shell
px0 guidelines log commit-style.md#summary-line
```

---

## `px0 guidelines why`

How a claim came to say what it says: the proposal that introduced it, the
material that proposed it, and the review that accepted it.

### `claim_id` (required)

- **Input:** a claim id, as for `log`.

```shell
px0 guidelines why commit-style.md#summary-line
```

`px0 runs why <run_id>` is the same verb for runs; each is listed under the group
whose ids it takes.

---

## `px0 guidelines revert`

Restore a claim to an earlier version.

### `claim_id` (required)

- **Input:** a claim id.

### `--to VERSION` (required)

Which version to restore.

- **Input:** a version number, with or without a leading `v` — `3` and `v3` are
  both accepted.

```shell
px0 guidelines revert commit-style.md#summary-line --to v2
```

---

## `px0 guidelines alias`

Claims are identified by file and heading slug, so rewording a heading would
otherwise orphan its history. An alias links the old id to the new one.

### `alias list`

Every alias, old id to new. No arguments.

### `alias link <old> <new>`

Point an old claim id at its current one.

- **Input:** two claim ids, old first.

```shell
px0 guidelines alias link commit-style.md#summary commit-style.md#summary-line
```

### `alias unlink <old>`

Remove an alias.

- **Input:** the old claim id.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown claim id, unknown version, nothing pending to review |
| `3` | The coding-agent harness failed while proposing or consolidating |
