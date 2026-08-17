# Guidelines and provenance

Guidelines are prescriptive prose -- how commit messages are written,
what a Go review checks. Workflows declare which guideline files they
use, and those files are inlined into the prompt deterministically at
run time (guidelines are never retrieved by similarity the way
`knowledge/` is).

## 1. Guidelines change themselves -- carefully

px0 never edits a file under `guidelines/` automatically. When a
workflow run or a knowledge ingest turns up something that looks like a
new rule or a correction to an existing one, it's filed as a pending
proposal instead:

```shell
px0 guidelines review
```

Each proposal shows the target file, the claim, the proposed body, and
the evidence it's based on (`path#anchor`). For each one, choose
**accept**, **edit** (type a replacement body), or **dismiss**. Nothing
is written to `guidelines/` until you accept or edit -- a dismissal just
deletes the pending proposal and leaves no trace.

```shell
px0 guidelines review --list-only    # print pending proposals, don't prompt
```

## 2. Consolidate periodically

```shell
px0 consolidate
```

`consolidate` is a wider sweep than `review`: it surfaces the same
pending proposals, plus claims that haven't been reinforced in a while
(decayed), pairs of guidelines that contradict each other, and guideline
files no workflow actually lists. It ends with the same accept/edit/
dismiss loop as `review`.

## 3. Trace a claim's history

Every guideline is addressed as `<path>#<heading-slug>`. Since guideline
files are versioned, a single claim's history can be walked independent
of the rest of the file:

```shell
px0 guidelines log guidelines/commit-messages.md#imperative-mood
px0 guidelines revert guidelines/commit-messages.md#imperative-mood --to v3
```

`log` prints when the section first appeared, every version that changed
it, and the evidence behind each change. `revert` restores just that
section to an older version, producing a new file version that combines
the old section with the current everything-else.

If the same idea got captured under two different headings, link them
rather than losing history:

```shell
px0 guidelines alias list
px0 guidelines alias link <old-claim-id> <new-claim-id>
```

## 4. Ask why

```shell
px0 why <run-id>
```

`px0 why` walks the full chain behind any run, answer, output, or claim:
which guideline versions were inlined, which knowledge passages were
retrieved, which tools were called, and what evidence justified any
guideline change made along the way. It works on a bare store with
nothing else installed -- there's no external history to consult.

For the file-level view (rather than the section-level view `guidelines
log` gives you), the same idea applies to any versioned file:

```shell
px0 versions list workflows/pr-precheck.md
px0 versions diff workflows/pr-precheck.md 2 3
px0 changes list --since 7d
```

## Next

Back to [01-getting-started.md](01-getting-started.md), or see
[02-building-a-workflow.md](02-building-a-workflow.md) if you haven't
generated a workflow of your own yet.
