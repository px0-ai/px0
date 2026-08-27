# 10. Context assembly

Modules: `px0/guidelines.py`, `px0/memory.py`, and `runner.render_prompt`

A store holds three kinds of knowledge, and they answer three different questions.

| Folder | Question it answers | How it reaches a run |
| ------ | ------------------- | -------------------- |
| `brain/` | What is true about the world | Retrieved by similarity, per query |
| `guidelines/` | How output should read | Attached at build time, inlined verbatim |
| `memory/` | What is true about you | Selected per run by word overlap |

Confusing them is the failure mode this part is mostly about.

## Guidelines

A guideline is a durable convention: a review rubric, a commit message format, a writing voice, a definition of done.

The file is shaped like a skill on purpose:

```markdown
---
name: digest-style
description: How a weekly digest should read; apply when a workflow writes a summary for other people
---

## Lead with the takeaway

State the conclusion in the first line...

## Name people, not roles

...
```

The frontmatter is the index a relevance pass reads. The body is the text a run inlines.

### Why the description is the index

`builder.select_guidelines` reads descriptions and nothing else. The keyword scorer it replaced matched on filename and body overlap, which cannot tell writing a commit message from reading one: a nightly standup that summarized commits scored highest against `commit-messages.md` and inlined a commit-authoring rubric into every run.

A description states what the file covers and when it applies. That is a claim about applicability, which is exactly the question being asked. A body is a claim about content, which correlates with applicability and is not the same thing.

The description is settled when a guideline is proposed, before it is drafted, because it is the file's contract. A proposal that cannot state in one sentence what it covers is not a durable convention.

### Degrading gracefully

`parse` never raises on content. A guideline px0 cannot fully understand is still one the user can read and edit, and taking `guidelines list` down over a stray colon is not a trade worth making. Unreadable frontmatter degrades to no frontmatter.

`split_frontmatter` returns `(None, text)` for anything that is not a clean YAML mapping, so "written before this format existed" and "frontmatter is broken" are handled the same way: as a file that is all body.

`Guideline.summary` falls back to the first `## ` heading when there is no description. The headings are the rules, so the first one says more about the file than a byte count would. `described` records which case a file is in, and `px0 doctor` reports files that would select better with a description.

### `work/`

Guidelines under `guidelines/work/` are never offered to a workflow automatically. `attachable` excludes them.

The folder means the same thing it means in `brain/`: mine, and not something px0 hands to a model on its own initiative. One name, one meaning, in both places.

### Names and paths

`name_for(rel)` is the filename without its extension, the way a skill is named by its directory. Folders group topics (`code-review/go.md`) and are not part of the name, which is what `px0 guidelines edit go` already resolves by.

Prompts head each guideline with its name; provenance records its path. Two identifiers for two audiences.

## Memory

The store had two kinds of knowledge and neither was this one. `brain/` answers questions about the world. `guidelines/` shapes how output reads. Between them there was nothing that remembered you: that standup goes out before 09:30, that "the API repo" means one particular repository, that a person you keep mentioning is on your team.

So every run started from nothing, and every correction had to be made again the next time. That is the difference between a tool you operate and an assistant.

### The shape

One Markdown file per fact, in a folder you own, versioned.

```markdown
---
kind: preference
subject: standup timing
learned: 2026-08-14
---

The standup goes out before 09:30 IST, so anything scheduled after that is too late.
```

`kind` is one of `fact`, `preference`, `person`, `project`, `place`. Kinds are a filing aid, not a schema: they make a listing scannable and let a run ask for only the kinds it needs.

Filenames are slugified from the fact's own words rather than numbered. A folder of `mem-0001.md` is a database with the ergonomics of a folder, and the whole point of keeping these as files is that a person can find one.

### Two properties worth stating outright

A memory is editable, because it will be wrong. px0 writes these as a side effect of conversations, and an assistant that silently accumulates unreviewable beliefs about you is the failure mode to design against. Every one is a file you can open, correct, or delete, and every write is a versioned change, so `px0 changes list` shows what px0 learned and when and `px0 changes revert` unlearns it.

Memory never leaves the machine on its own. It is inlined into prompts the same way guidelines are -- into the harness you already trust -- and goes nowhere else. `px0 store export` carries it, because an export is how a store reaches your other machine and an assistant that forgets everything on the new one is not one.

### Writing to a name that exists

`remember` replaces rather than appends. A fact that has changed is not two facts, and a memory folder accumulating every past belief about the same subject is one that will contradict itself inside a prompt.

### Selection

`relevant(home, query, budget, kinds, pinned_first)` chooses what a run sees.

Local arithmetic, not a model call and not the retrieval index. Memories are short, few, and read on every run, so paying for an embedding pass to choose between forty lines of text would cost more than inlining all of them.

The ranking is: pinned first, then word overlap with the query, then the rest until the budget runs out. `_terms` drops a small stop list and anything under three characters.

Pinning is how "never crowded out" is kept. A pinned memory is offered room before anything competes for it.

### The budget, and the clip

`memory.budget_chars` defaults to 4000. A store that has been running for a year should not quietly turn every prompt into a biography.

The subtle case is a single memory longer than the whole budget:

```python
if len(m.text) > room:
    keep = max(room, MIN_MEMORY_CHARS)
    m = replace(m, text=m.text[:keep].rstrip() + " [...]")
```

Clipped, not skipped and not admitted whole. Admitting it whole is what a first-item exemption used to do, and one long memory then put fifty thousand characters into every prompt from a setting that says four thousand. Below `MIN_MEMORY_CHARS` a clipped memory has stopped saying anything, so it is left out rather than included as a stub.

`replace` from `dataclasses` is used so the clip never touches the file on disk.

### The prompt block

```python
lines = ["# What px0 knows about you",
         "",
         "These are things you have told px0, or that it recorded from your "
         "own corrections. Treat them as standing context. If one conflicts "
         "with what you are given now, prefer what you are given now and "
         "say the memory looks stale.",
         ""]
```

Labelled as things px0 was told rather than things it worked out. That framing makes a model treat a remembered preference as standing instruction and a remembered fact as context it may still check -- and it makes a wrong memory read as a wrong belief rather than as ground truth.

### Memories px0 proposes for itself

Everything above is memory as a notepad: it exists, and someone has to remember to write in it, which is exactly the failure it was built to fix.

`memory.suggest` is the other half. `_correction_sources` reads two places a standing fact tends to be sitting in plain sight: the note on a run someone marked bad, and what they said while correcting an answer in a conversation. Both were already being recorded and neither was being read for this.

The prompt draws one distinction and holds it:

> Keep only what will still be true next month. "The digest covered the wrong week" is a bug; "the week runs Monday to Friday" is a fact.

`suggest` returns candidates and never writes. Anything already remembered under the same subject is dropped here rather than shown and then discarded on accept, so the list is what would actually be new.

It raises nothing on a bad model reply. An empty list is a perfectly good answer to "is there anything worth keeping", and failing the command over a malformed suggestion would be worse than making none.

The line held throughout: px0 proposes, a person accepts. One confirmation is what keeps an assistant's beliefs about you reviewable while still meaning you say a thing once rather than never.

## Assembling the prompt

`runner.render_prompt` puts the three pieces together:

```
[memory block]

[guidelines block]

[body, with its own templates rendered]
```

Standing context about the user, then the rules for judging output, then the job.

A body containing `{{memory}}` or `{{guidelines}}` gets that block substituted exactly there instead. A workflow that knows where its conventions belong should be able to say so.

Guideline text is keyed by store-relative path and headed by name:

```python
guidelines_block = "\n\n".join(
    f"# {guidelines_mod.name_for(rel)}\n\n{text}"
    for rel, text in guideline_texts.items()
)
```

Bodies only. `body_of` strips the frontmatter, because the frontmatter is how px0 finds the file and inlining it would spend prompt on machinery.

## What the record keeps

Both blocks are recorded on the run:

```python
guidelines_inlined=[{"path": g, "version": ...}, ...],
memories_inlined=[{"name": m.name, "subject": m.subject}, ...],
```

Versions, not just paths, for guidelines -- a run that behaved oddly may have used a version of the rules that no longer exists.

That record is what lets `px0 runs why` say a run acted on a memory, which is the first thing you want when a run behaves in a way the instructions alone do not explain.

## Next

[Part 11](11-daemon.md) covers what decides that a run should happen at all.
