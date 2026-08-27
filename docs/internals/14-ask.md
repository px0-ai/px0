# 14. Ask, routing, and sessions

Modules: `px0/route.py`, `px0/session.py`, `px0/commands.py`, `px0/ask.py`

Everything px0 could do, it could only do if you already knew it was there. `px0 brain ask` answered from material you had ingested and explicitly never touched connectors, so it could tell you what a post said about caching and not what was on your calendar. Running a workflow meant knowing a workflow existed and typing its id.

`px0 ask` is the front door: a question in, and a decision about who should answer it.

## Five destinations

| Route | When |
| ----- | ---- |
| `workflow` | You have built something that does exactly this; run it |
| `tool` | One read-only call answers it, with every argument settled by the question |
| `brain` | The answer is in something you read and kept |
| `memory` | px0 already knows, because you told it |
| `answer` | None of the above; just answer, with memory as context |

The prompt orders them by preference: workflow over tool, tool over answer, cheapest route that actually answers.

`answer` is explicitly described to the model as a perfectly good answer, with an instruction not to force a route that does not fit. A router given five options will otherwise use all five.

## The index

Routing is one cheap model call over an index the store already keeps: workflow descriptions, tool descriptions, memory subjects. Same trick `select_guidelines` plays for guidelines, and it works for the same reason -- a description written to be findable is a better index than the content is.

```python
def candidates(home, config) -> dict:
    ...
    readable = []
    for spec in tools_mod.list_tools(home=home):
        if spec.is_write:
            continue
```

Only read tools are offered. A router that could reach a write tool would be one bad classification away from sending something, and the whole argument for a front door is that asking it a question is safe.

Disabled workflows are excluded. `has_brain` is checked so the router is never offered a route that can only fail. `MAX_CANDIDATES` is 60 per kind: the index is cheap but the prompt is not, and a store with two hundred workflows should not spend most of a routing call listing them.

Each workflow entry carries a `writes` flag, so the CLI knows to confirm before running one.

## Failing to a plain answer

`decide` validates hard and degrades softly.

```python
if route == "workflow" and workflow not in known_workflows:
    return Decision("answer", reason=f"routed to unknown workflow {workflow!r}")
if route == "tool" and tool not in known_tools:
    return Decision("answer", reason=f"routed to unknown tool {tool!r}")
```

A reply that is not JSON, is a list, names a route px0 does not have, or names a workflow or tool that is not in the index it was shown all fall back to `answer` rather than raising. The worst outcome of a bad route should be a plain reply, never a failure to answer at all.

Every fallback carries a reason, and the decision is printed with the answer. A route is a suggestion px0 shows you when it is not obvious, so a wrong route is visible rather than mysterious.

`--explain` prints where the question would go and stops. `--route` skips the router entirely.

## What each route does

`answer_directly` includes the memory block. This is the route for questions needing no lookup, and "what did I decide about the release process" is exactly such a question when the decision is in memory. It also takes the conversation so far, so a follow-up is answered as a follow-up and a correction is accepted rather than argued with.

`summarize_tool_result` turns one tool's raw answer into a reply to the question that prompted it. Without it the route returns a wall of JSON, which is a worse answer than `answer` would have given -- and the point of routing to a tool is that live data makes for a better answer, not a rawer one.

`brain` goes to `ask.ask`, covered in [part 9](09-brain.md).

`workflow` runs it, with a confirmation first if it can write.

## Sessions

`px0 ask` answered one question and forgot it. Every follow-up re-routed from nothing, so "no, I meant last week" started over. Worse, the correction itself was thrown away the moment the command exited. The user had told px0 something true about their work and px0 had no place to put it.

A session is that place. It holds the turns, so a follow-up can be understood in terms of what came before, and it marks which turns were corrections, because those are the ones worth keeping.

### Understanding a follow-up

```python
def resolve_question(session, question) -> str:
    turns = session.get("turns") or []
    if not turns:
        return question
    return f"(following on from: {turns[-1]['question']}) {question}"
```

A follow-up is often unintelligible alone -- "and last week?" names no subject. Rather than a second model call to rewrite it, the previous question is prepended as context, which is enough for a router choosing between five destinations and costs nothing.

`context_block` carries the last `CONTEXT_TURNS` (6) exchanges into the answering prompt. Answers are truncated harder than questions: what the user said is what a follow-up refers back to, where px0's own earlier answer is mostly there to stop it contradicting itself. The block closes with an instruction to accept a correction rather than defend the earlier answer.

### Detecting a correction

```python
_CORRECTION_MARKERS = (
    "no,", "no ", "not ", "actually", "i meant", "that's wrong", ...
)
```

A small literal list rather than a model call. This runs on every turn, and being occasionally wrong about whether something was a correction costs a suggestion the user can decline, where a model call would cost a round trip on every message.

It only applies mid-conversation. An opening question that happens to contain "not" is a question, not a correction, so `add_turn` checks that there is something to correct first.

`corrections()` pairs each correction with the question that preceded it, because "no, last week" means nothing on its own and a great deal next to what it was answering.

### Lifespan

Sessions live under `.state/sessions/` and age out at `ask.session_days`, seven by default.

They are scaffolding. What survives a session is what you agreed to remember, which goes into `memory/` as an ordinary versioned file. The conversation is the mechanism; the memory is the point.

## Noticing a habit

`repeated_questions` reads run records -- `ask.ask` writes one per question -- and finds past asks with at least 0.6 word overlap with this one.

The observation worth surfacing is not that a question was asked twice, but that the user keeps doing by hand something px0 could have been doing on a schedule. Same records, read for a different question, which is the same trick `px0 workflows health` plays.

The threshold is 3 including the current ask, so a coincidence does not trigger it.

## Where the handlers live

`commands.py` holds the handlers for `ask`, `approvals`, `inbox`, and `memory`, kept out of `cli.py` deliberately. That file is where every handler has landed since the beginning and it is now the one file that fights any change to the CLI; adding four more groups to it would have made that worse for no reason.

Each handler takes the resolved store and config rather than fetching them, so `cli.py` keeps one place where a store is opened, and so these stay callable and testable without a parsed argv.

## The inbox

A workflow could route its output three ways, and on a schedule all three had the same problem. `stdout` goes to a terminal nobody is sitting at. `file` writes something you have to remember to open. A write tool posts somewhere else, which is fine when that somewhere is where you already look and useless when it is not.

So px0 did the work and then had nowhere to say so. `px0 status` was the nearest thing and it answers a different question -- whether anything is broken, not what arrived.

The inbox is the missing half: a per-store queue that scheduled runs deliver into, so "what happened while I was away" is one command.

An entry is small on purpose -- what produced it, a `PREVIEW_CHARS` (600) preview, and where the whole thing is -- because the inbox is a place to triage from, not a second copy of the output.

`title_for` takes the line an entry is listed by from the output's own first heading or first non-empty line. A workflow that already writes "## PRs you reviewed this week" has said what the entry is better than any label px0 could synthesize.

`body` reads the file back rather than showing the stored preview, so opening an entry shows what is on disk now. An entry whose file has since been deleted falls back to the preview and says so, rather than showing nothing.

Retention drops read and archived entries past `inbox.keep_days`. Unread entries are never dropped: an inbox that quietly forgets what you have not looked at is worse than one that grows.

## Next

[Part 15](15-mcp.md) covers the other way into px0: from another agent.
