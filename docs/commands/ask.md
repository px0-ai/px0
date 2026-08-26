# `px0 ask`

One question, routed to whoever can answer it.

Everything px0 could do, it could only do if you already knew it was there.
`px0 brain ask` answers from material you have ingested and, by design, never
touches connectors or guidelines — so it can tell you what a post said about
caching and not what is on your calendar. Running a workflow meant knowing a
workflow existed and typing its id.

This is the front door. It decides who should answer, then answers.

Implemented by `px0/route.py` and `px0/commands.py`.

```
px0 ask ["<question>"] [--route ROUTE] [--explain] [--k N] [--yes] [--json]
px0 ask [--continue] [--no-remember]
```

## Where a question can go

| Route | When |
| ----- | ---- |
| `memory` | px0 already knows, because you told it — see [`px0 memory`](memory.md) |
| `brain` | The answer is in something you have read and kept |
| `workflow` | You have built something that does exactly this; run it |
| `tool` | One read-only call answers it, with every argument settled by the question |
| `answer` | None of the above — general knowledge, reasoning, or writing |

The route px0 chose is printed above the answer, so a question that went
somewhere surprising says so rather than leaving you guessing.

```shell
px0 ask "what did that post say about backpressure?"
px0 ask "which pull requests did I review this week?"
px0 ask "when does my standup go out?"
```

## Conversations

With no question and a terminal to type into, `px0 ask` opens a conversation
instead of answering once and forgetting:

```shell
px0 ask                  # ask, follow up, correct
px0 ask --continue       # carry on the last one
```

Two things change once there is a thread. A follow-up is understood in terms of
what came before, so "and last week?" — which names no subject and is
unroutable alone — still lands. And a turn that reads as putting px0 right is
marked as a **correction**.

Corrections are the reason this exists. What you say when an answer is wrong is
the highest-signal thing you will say all day, and it used to be discarded the
moment the command exited. At the end of a conversation px0 reads them for
standing facts and offers to keep the ones worth keeping:

```
worth remembering?
  · My working week runs Monday to Friday.
    from  no, I meant this week — my week is Mon-Fri
remember this? [y/N]
```

Nothing is kept without you saying so, and each one becomes an ordinary file
under `memory/` you can edit or delete. `--no-remember` skips the offer.

The conversation itself is scaffolding: it lives under `.state/` and is pruned
after `ask.session_days`. What survives it is what you agreed to remember.

## Two limits, on purpose

**A question is not permission to act.** A workflow with any write tool is
confirmed by name before `ask` will run it — "what does my standup look like"
must not post it — and in a non-interactive shell it is refused rather than
assumed. `--yes` waives that.

**The router is only ever shown read-only tools.** A router one bad
classification away from sending something would undo the whole argument for a
front door being safe to ask.

## `--route ROUTE`

Skip the router and answer this way.

- **Input:** one of `memory`, `brain`, `workflow`, `tool`, `answer`.

Both an escape hatch and the thing to reach for when the router keeps getting
one kind of question wrong.

## `--explain`

Print where the question would go, and stop. Nothing is run and nothing is
answered.

```shell
px0 ask "did the nightly deploy go out?" --explain
```

## `--k N`

Passages to retrieve, when the answer comes from your brain. Defaults to
`retrieval.k_default`.

## `--yes`

Run a workflow that can write without confirming it first.

## `--json`

The question, the routing decision, the answer, and its sources.

## Questions you keep asking

Every ask is recorded as a run. When the same question comes round for the
third time, px0 says so and offers to build it into a workflow you can put on a
schedule — the point being not that you repeated yourself, but that you keep
doing by hand something that could have been waiting for you.

## Related

- [`px0 brain ask`](brain.md) — the narrow, brain-only ask. Still there, still
  means exactly that.
- [`px0 memory`](memory.md) — what px0 knows about you, which every route can draw on.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Empty question, or a workflow that needed a confirmation with no terminal to ask in |
| `2` | The workflow or tool it routed to failed |
| `3` | The harness failed, timed out, or is missing |
