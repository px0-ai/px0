# Building a workflow

`px0 init` ships no workflows at all -- the point of px0 is that you
describe what you want in plain English and get a working file back.

`px0 workflows new` runs four model passes, each with a job small enough to do
well, and asks you to confirm at the two points where a wrong answer
would cost you something:

1. **Clarify** -- what's ambiguous about the request?
2. **Discover** -- search Composio's catalogue for the tools it needs.
3. **Confirm** -- you approve the tool set before anything is authorized.
4. **Plan** -- write the workflow against exactly those tools.

```shell
px0 workflows new "every friday at 5pm summarize the github pull requests I reviewed this week and post it to our slack channel"
```

## 1. It asks what's ambiguous

```
✓ Checking the request for gaps

a few questions
› Which Slack channel should the digest go to?
  #eng-standup
› Which repositories should it look at?
  razorpay/api
```

Only things that would change the generated workflow get asked -- which
account, which channel, how often, where output goes. The model is told
not to ask about anything it can pick a sane default for, because an
interrogation is worse than an assumption.

Press Enter to skip a question. Skip everything in a round and the loop
ends, so you're never trapped. It asks at most three rounds regardless.
`--no-clarify` skips the pass entirely and builds from the description
as written.

Your answers are carried into every later pass, so the plan reflects
them rather than re-guessing.

## 2. It searches Composio's catalogue

```
  · github: list pull requests
  · github: list pull request reviews
  · slack: send message channel
✓ Searching Composio's catalogue (3 queries)
✓ Choosing from 18 candidates
```

The model writes *searches*, not tool names -- a toolkit plus a short
capability phrase. It can't know Composio's naming (there is no
`GMAIL_GET_EMAIL`, for instance), so it describes the action and px0 does
the lookup. Nothing is invented: a slug that isn't in the search results
is discarded.

This is why the workflow gets the tool that actually fits rather than the
nearest of px0's ten curated ones. Composio's catalogue is thousands of
tools; `px0 workflows new` searches all of it.

`--no-discover` skips the search and restricts the plan to px0's curated
tools.

## 3. You confirm the tools

```
tools selected (3)
  1.  read   composio:GITHUB_LIST_REVIEWS_FOR_A_PULL_REQUEST  Lists submitted reviews for a pull request
  2.  read   composio:GITHUB_LIST_PULL_REQUESTS               Lists pull requests for a repository
  3.  write  composio:SLACK_SEND_MESSAGE                      Posts a message to a slack channel
! this workflow could change things outside px0  SLACK_SEND_MESSAGE

Enter accepts all; list numbers to drop (e.g. 2,3); n aborts
› keep all?
```

This is the gate before anything is authorized or written. The model
chose these; picking up a write tool the request never asked for is
exactly the mistake worth catching here.

Access is stated per tool, and it comes from Composio's own metadata --
px0 never infers it from the name:

| Marker | Meaning |
| --- | --- |
| `read` | Only reads. Safe as a workflow input. |
| `write` | Can post, send, or change something outside px0. |
| `destructive` | Can delete or overwrite. Flagged separately and harder. |

The selection pass is told to prefer the fewest tools, prefer reads over
writes, include a write only when the request explicitly asks to change
something, and never include a destructive tool unless asked to delete.

Confirmed tools are recorded in the store, so the plan, its validation,
and every future run resolve them without another catalogue lookup -- a
workflow keeps working offline and unchanged after it's written.

## 4. It writes the plan

```
plan
{
  "trigger": {"manual": true, "schedule": "0 17 * * 5"},
  "inputs": [
    {"id": "recent_prs", "tool": "composio:GITHUB_LIST_PULL_REQUESTS",
     "args": {"owner": "razorpay", "repo": "api", "state": "all"}}
  ],
  "tools": ["composio:GITHUB_LIST_REVIEWS_FOR_A_PULL_REQUEST", "composio:SLACK_SEND_MESSAGE"],
  "output": {"target": "stdout"},
  "body": "...",
  "description": "Every Friday at 5pm, summarize the GitHub PRs I reviewed this week and post the digest to Slack."
}
```

Read tools land in `inputs` (they run before the prompt to gather
context); write tools land in `tools` (the model calls them during the
run). px0 enforces that split -- a write tool in `inputs` is a
validation error, since inputs run unconditionally.

Then two checks print:

- **Feasibility.** A tool that doesn't exist, an input with no tool, an
  invalid cron expression. These stop the build; nothing is saved.
- **Write access.** The write tools the workflow would be granted, named
  again now that the plan is concrete.

## 5. It authorizes what the plan needs

```
authorization needed (2)
! github  not authorized
! slack   not authorized
› Start authorization for github, slack? [Y/n]

› github  open this and complete the consent:
    https://backend.composio.dev/s/...
› slack   open this and complete the consent:
    https://backend.composio.dev/s/...
```

It asks before minting anything. Answer `n` and nothing is prepared --
the first run that needs the app will offer a link instead.

Anything already authorized is skipped:

```
✓ already authorized  github, slack
```

A pending consent does **not** throw away the plan. The workflow file is
valid either way, and re-running `px0 workflows new` would repeat the clarify,
search, selection, and planning passes to arrive at the same file. So
px0 writes it and tells you what's still waiting.

## 6. Confirm and name it

```
› Generate this workflow? [y/N] y
› workflow id [summarize-the-github-pull-requests-i-rev]:
```

Accept the suggested id or type your own. `--yes` skips every prompt in
this flow -- clarifying questions, tool confirmation, authorization, and
this one -- and `--id <id>` names it directly, for scripted use.

## 7. What you get

```
created friday-pr-digest
✓ workflow    ~/.px0/workflows/friday-pr-digest.md
✓ guidelines  summarization.md
✓ schedule    0 17 * * 5
✓ tools       composio:GITHUB_LIST_PULL_REQUESTS, composio:SLACK_SEND_MESSAGE
! authorization pending  github, slack

finish the consent in your browser, then:
  px0 workflows run friday-pr-digest --dry-run
```

Guidelines are matched to the task by topic, and a file only gets
attached if it genuinely matches -- every guideline is inlined verbatim
into the prompt, so an unrelated one costs tokens and misleads the model.
A commit-message workflow gets `commit-messages.md`; a workflow about
haikus gets none.

## The file it wrote

The generated file is plain Markdown: YAML frontmatter as the machine
contract, the body as the prompt the model receives.

```yaml
---
id: friday-pr-digest
kind: workflow
version: 1
description: Every Friday at 5pm, summarize the GitHub PRs I reviewed this week
trigger: {schedule: "0 17 * * 5"}
guidelines: [summarization.md]
inputs:
  - id: recent_prs
    tool: composio:GITHUB_LIST_PULL_REQUESTS
    args: {owner: razorpay, repo: api, state: all}
tools: [composio:SLACK_SEND_MESSAGE]
output: {target: stdout}
timeout: 120s
---
Summarize {{recent_prs}} as a short digest, then post it to #eng-standup.
```

| Field | What it does |
| --- | --- |
| `request` | The sentence you typed, kept verbatim for `px0 workflows edit` |
| `trigger.schedule` | Cron expression, evaluated in machine local time |
| `guidelines` | Files inlined into the prompt verbatim, by name -- never retrieved by similarity |
| `inputs` | Resolved before the prompt; each is a `tool`, `retrieve`, `source`, or `workflow` |
| `tools` | What the model may call during the run |
| `output` | `{target: stdout}` or `{target: file, ...}` |
| `timeout` | Wall-clock cap on the run |

A `composio:` prefix marks a tool discovered from the catalogue;
`provider.action` ids are px0's curated ones. Both execute the same way.

Edit any of it by hand -- there's no compile step, and the change is
picked up on the next run. px0 versions the file itself, so
`px0 versions list workflows/<id>.md` shows every edit, yours included.

## Guidelines it offers to write for you

Partway through the build, px0 may say something like:

```
guideline: PR comment voice and format
  · The workflow posts comments publicly under your account, so the tone
    and directness need to match how you normally write review feedback.
[INFO] would be saved as  guidelines/pr-comment-style.md
› Write it now? [y/N]
```

This is for standards px0 cannot guess -- what counts as worth flagging in a
review, how blunt your comments are, the voice a summary is written in. Answer
in your own words, a couple of lines is plenty, and finish with an empty line:

```
› How do you like your PR review comments written?
  (finish with an empty line)
  Blunt, no softening. Always say what to do instead of just naming the
  problem. Never comment on formatting, that is the linter's job.
```

px0 shapes that into `##` sections, shows you the draft, and takes `again` if
you want another pass. What gets saved is a normal guideline file: it has
version history from v1, so `px0 guidelines log` and `px0 guidelines revert`
work on it, and it is added to this workflow's `guidelines:` -- which means its
text is inlined into every run from now on.

Nothing is proposed for a workflow that already says what to do, and nothing on
a topic you already have a guideline for. `--yes` skips the step entirely, since
there is no sensible default for "what is your commit message convention".

## Inspect and run it

```shell
px0 workflows list
px0 workflows run friday-pr-digest --dry-run     # write tools are stubbed, not executed
```

Leave the id off and px0 lists what you have, to pick with the arrow keys:

```shell
px0 workflows run
```

```
› Which workflow to run?
  ↑/↓ to move, enter to select, q to cancel
 ›  1. friday-pr-digest  Post a Friday digest of my PRs to #eng-standup
    2. weekly-digest     Summarize the week
```

`j`/`k` move too, and a digit jumps straight to that row. Over a pipe it falls
back to a numbered prompt. `--stdin` needs an explicit id -- the picker would
otherwise read its keystrokes from the stream carrying your input.

## Change what it does

```shell
px0 workflows edit friday-pr-digest
```

px0 shows the sentence you originally typed and takes a new one:

```
editing friday-pr-digest
  original request: every friday afternoon, summarize the PRs I reviewed
                    this week and post it to #eng
  tools: composio:SLACK_SEND_MESSAGE
  guidelines: pr-comment-style.md

› New instructions (blank to keep the current ones):
```

This rebuilds the workflow rather than opening the file: the tools, inputs, and
guideline list all follow from the request, so revising the request and
regenerating keeps them consistent. Hand-editing the body is still fine for a
small wording change -- but it will not notice that your new instruction needs a
tool the workflow does not have.

The rebuild saves under the same id, and the old version stays in history:

```shell
px0 versions list workflows/friday-pr-digest.md
px0 versions revert workflows/friday-pr-digest.md --to v1
```

## Next

- [03-connections-and-tools.md](03-connections-and-tools.md) -- how
  authorization works, and `px0 tools list --status`.
- [04-knowledge-and-ask.md](04-knowledge-and-ask.md) -- build a personal
  knowledge library and query it.
- [06-scheduling-and-the-daemon.md](06-scheduling-and-the-daemon.md) --
  make `trigger.schedule` actually fire.
