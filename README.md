<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://px0.ai/logo/px0-logo-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="https://px0.ai/logo/px0-logo-light.png">
  <img alt="Your descriptive image alt text" src="https://px0.ai" width="160">
</picture>

An agent that works the way you work.

---

You already have tools, habits, and conventions. px0 automates them. You describe a recurring chore in plain English, and px0 writes it as a workflow you can run, schedule, and edit directly from your terminal. Everything stays on your machine. Your workflows, your notes, and your conventions are plain Markdown files in a directory you own.

There is no server, no account, and no state stored in the cloud. But your workflows don't stop at your laptop. Through [Composio](https://composio.dev), px0 reaches the apps you already use: GitHub, Slack, Gmail, Notion, Linear, Jira, and over a thousand more.

## What px0 is for

- Recurring reports you assemble by hand every week.
- Chores that follow the same steps every time: draft the release notes, precheck a pull request, triage overnight alerts, file the standup update.
- Work that has to sound like you, because px0 follows conventions you write down once.
- A searchable library of everything you read, so you can ask questions across it later.

## Workflows you could build

Each of these is one sentence you tell `px0 workflows new` during its interview, and each becomes a file you can edit and put on a schedule.

| What you want                                                                    | Apps it touches        |
| -------------------------------------------------------------------------------- | ---------------------- |
| Every Friday, summarize the pull requests I reviewed and post it to #eng-standup | GitHub, Slack          |
| Each morning, brief me on today's meetings and the emails I have not replied to  | Google Calendar, Gmail |
| Turn last night's error spike into a triaged bug ticket                          | Sentry, Linear         |
| Post a Monday sprint status from our issue tracker to the team channel           | Jira, Slack            |
| Draft release notes from the commits since the last tag and file them as a page  | GitHub, Notion         |
| Log this week's revenue and refunds into the finance sheet                       | Stripe, Google Sheets  |
| Group this week's support tickets by theme and open issues for the top three     | Zendesk, Linear        |
| Save every newsletter I star to my reading library                               | Gmail, px0 brain       |
| Watch for a new production error and open a ticket the moment one appears        | Sentry, Linear         |
| Run our deploy script and post what it printed                                   | shell, Slack           |

## Install

You will need:

- Python 3.11 or newer.
- A coding agent CLI that px0 uses as its model backend: `claude`, `gemini`, `pi`, or `opencode`. px0 reuses that CLI's own login, so pick whichever one you already sign into.
- A [Composio](https://composio.dev) API key, for workflows that reach other apps. You can skip this and add it later.

Then run:

```shell
curl -fsSL https://px0.ai/install.sh | sh
```

Confirm it landed:

```shell
px0 doctor
```

To install from a clone instead:

```shell
python -m venv venv
source venv/bin/activate
pip install -e .
```

## First steps

### 1. Set up your store

```shell
px0 init
```

This creates `~/.px0` and asks for your Composio API key. Skip the key if you do not have one yet, and set it later with `px0 config composio <key>`.

Using a backend other than `claude`? Point px0 at it:

```shell
px0 init --harness gemini      # or pi, or opencode
px0 config model               # switch backend or pick a model, later
```

### 2. Build your first workflow

px0 ships no workflows. Describe what you want:

```shell
px0 workflows new
```

That opens an interview - one question at a time, until px0 has the job, what it reads, where the result goes, when it runs, and what makes the output right. It writes the request back for you to approve or reword before anything is built. Enter on a blank line ends the questions early.

px0 then asks about anything still genuinely ambiguous, finds the tools the job needs, and shows you the list before authorizing anything. Tools that can post or send get called out, so you can drop the ones you did not ask for. Then it writes the workflow file and prints its id, which you can override when it asks.

### 3. Run it

```shell
px0 workflows run friday-pr-digest --dry-run   # resolve inputs, call nothing
px0 workflows run friday-pr-digest             # for real
```

The first time a workflow needs Slack or Gmail, px0 hands you a URL to approve. You only authorize the apps you actually use.

### 4. Put it on a schedule, or on a watch

Your workflow already carries the schedule you described. Install the scheduler so it fires on its own:

```shell
px0 daemon install
px0 daemon status
```

A workflow can also wait for something to happen instead of watching the clock.
Give it a read-only tool to poll and px0 runs it when something new turns up:

```yaml
trigger:
  watch:
    tool: github.list_my_prs
    key: url
    every: 30m
```

### 5. See what happened

```shell
px0 status              # is anything broken
px0 workflows list      # what you can run
px0 runs                # browse past runs
px0 runs why <run-id>   # how a run reached its result
px0 runs events <run-id># every turn, tool call, and what it cost
```

A scheduled workflow that fails is silent unless you ask it not to be. Pick how
it tells you:

```shell
px0 config set notify.on_failure desktop     # a local notification
px0 config set notify.on_failure tool        # or send it somewhere
px0 config set notify.channel slack.post_message
px0 config set notify.target "#ops"
```

Per workflow, an `on_failure` block in its frontmatter wins over both, so the
noisy hourly job can stay quiet while the nightly report shouts. A `retry` block
decides how many times a failed run is attempted first.

## Just ask it

You do not have to know what px0 has. Ask, and it works out who should answer —
what you have told it, what you have read, a workflow you already built, a
single read-only call, or nothing at all:

```shell
px0 ask "when does my standup go out?"
px0 ask "what did that post say about backpressure?"
px0 ask "which pull requests did I review this week?"
```

The route it chose is printed above the answer, so a question that went
somewhere surprising says so. A question is never permission to act: a workflow
that can write is confirmed by name first, and the router is only ever shown
read-only tools.

Ask the same thing three times and px0 offers to make it a workflow and put it
on a schedule — the point being not that you repeated yourself, but that you
keep doing by hand something that could be waiting for you.

## It holds a conversation

```shell
px0 ask                  # ask, follow up, correct
px0 ask --continue       # carry on the last one
```

A follow-up is understood in terms of what came before, so "and last week?"
still lands. And when you put px0 right, that correction is not thrown away
when the command exits — which is what the next section is about.

## It remembers

Tell px0 something once:

```shell
px0 memory add "standup goes out before 09:30, and never mentions unfinished work"
```

Every run from then on gets that as context. Memories are one Markdown file
each under `memory/`, versioned like everything else — so what px0 has come to
believe about you is something you can read, correct, and revert.

You mostly will not type them, though. When you mark a run bad, or correct px0
in a conversation, it reads what you said for the part that will still be true
next month and offers to keep it:

```shell
px0 runs mark <run-id> --bad "it covered last week; my week is Mon-Fri"
px0 memory suggest
```

It proposes; you accept. Nothing is remembered without a yes.

## Draft it and ask me

An assistant is only as useful as what you dare let it do. px0 can hold back
any call that leaves a mark:

```yaml
confirm: true            # in the workflow's frontmatter
```

```shell
px0 config set tools.confirm_writes true    # or for everything
```

The run still happens and still produces its output — only the write waits. You
see exactly what would be sent, next to the thing it is announcing:

```shell
px0 approvals                     # what is waiting
px0 approvals show <id>           # the arguments in full, and the digest
px0 approvals approve <id>        # sends precisely that, not a fresh run
```

Approving calls the tool with the arguments you read. It does not re-run the
workflow, which would draft something else against a later hour. Wrong channel
rather than wrong message? Fix it in place:

```shell
px0 approvals edit <id> --set channel=#ops
```

And since approvals happen when you are away from the desk, px0 can watch a
channel for replies — `approve apr_...` — from senders you name. It refuses to
run with a reply channel and no sender list, because that would be a queue
anyone able to post there could empty.

## Somewhere to read what it produced

A nightly workflow used to write a file you had to remember to open. Now it
delivers:

```shell
px0 inbox                # what arrived while you were away
px0 inbox read           # the oldest unread
```

Scheduled and watched runs deliver automatically; manual ones do not, since you
were there. Unread entries are never aged out.

## Workflows that get better

A workflow you wrote once is a workflow that was right once. px0 keeps enough
about every run to say what has happened to it since, and it does that in two
halves — one that needs no model at all, and one that does.

### What the runs say, computed not guessed

```shell
px0 workflows health                        # a row per workflow
px0 workflows health friday-pr-digest       # one in detail
```

This is arithmetic over your own run records. No model call, no network. It
finds the things that are invisible from any single run: a tool that has been
failing a third of its calls, an allowlisted tool nothing has ever used and
every run still pays to describe, an input that has quietly resolved to nothing
for a month while the model wrote a report around the hole, runs that succeeded
and produced nothing at all.

Two of those px0 can repair on its own, each behind its own confirmation:

```shell
px0 workflows health friday-pr-digest --fix
```

It will only ever drop a tool nothing has called, or raise a timeout runs kept
hitting. Both touch the frontmatter and nothing else, and both land as versioned
changes, so `px0 changes revert` undoes them.

### Tell it when the output was wrong

The one thing no record can infer is whether what a run produced was any good. A
Friday digest that runs green every week and comes back useless looks perfect in
every field px0 has:

```shell
px0 runs mark <run-id> --bad "it summarized last week, not this week"
```

One sentence, stored on the run. It is what the next step actually learns from,
and `px0 runs` shows you which runs carry one.

### Have it revise the workflow

```shell
px0 workflows improve friday-pr-digest
```

px0 prints the evidence first, then asks the model what the workflow should say
instead, then shows you the answer as a diff against the request *you* wrote —
before anything is applied. What it proposes is a new request, rebuilt through
exactly the path `px0 workflows edit` takes, so the tools and inputs stay
consistent with it. A complaint about how output reads becomes a guideline
instead, which fixes every workflow that carries it rather than just this one.

It will never widen what a workflow can reach on its own: a new tool still goes
through the same confirm-and-authorize step as when you first built it. Use
`--dry-run` to see a proposal and apply none of it, and `--show-evidence` to see
exactly what the model was given.

## Workflows somebody else can run

A workflow that works is full of your own facts: your repository, your channel,
your folder. That is what stops anyone else running it. px0 can lift those out:

```shell
px0 workflows templatize friday-pr-digest --to pr-digest-template
```

It scans the file for every literal that belongs to one installation rather than
to the job, shows you the list, and declares each one as a var with a
description and the values somebody else would plausibly put there:

```yaml
vars:
  - name: repo
    description: The repository whose pull requests the digest covers, as owner/name
    values:
      - vercel/next.js
  - name: channel
    description: The Slack channel the digest is posted to
```

The literals become `{{input.repo}}` and `{{input.channel}}` wherever a run can
resolve them, and the workflow is then run by naming them:

```shell
px0 workflows run pr-digest-template --input repo=vercel/next.js --input channel=#eng
```

Run it without them and it refuses before calling anything, naming what is
missing. At a terminal it asks you instead, showing each description — which is
what makes the first run of somebody else's workflow something other than a
guessing game.

The rewrite is shown as a diff and validated as a workflow before it is written,
`--to` leaves the workflow doing your actual work alone, and `px0 changes revert`
undoes it either way.

## Checking a change before trusting it

A revision used to mean waiting until Friday to find out. Let a workflow keep
what it read, and both versions can be run against the same world:

```yaml
capture: true            # in the workflow's frontmatter
```

```shell
px0 workflows replay digest --against ./new-body.md
```

`px0 workflows improve` offers this in line — you see what its proposal would
have written last Friday before deciding anything. Fixtures stay under
`.state/`, never travel, and age out: capture is off by default because a
fixture is the content of your work.

## It stops trying

An unattended workflow that fails the same way five times running is parked and
you are told. A dead connector used to mean an hourly failure and an hourly
notification for the rest of the week, with nothing noticing that nothing had
changed. A manual run never trips it — you are there, reading the error — and
`px0 workflows enable` is the way back.

## More than one machine

```shell
px0 store sync ~/Dropbox/px0-shared
```

Your workflows, guidelines, memory, and brain travel; the version history stays
put, because it is a SQLite database and pointing a folder-syncing tool at it
is how people were quietly corrupting it. A file changed in both places is kept
beside yours rather than overwritten — two versions are two decisions, and px0
does not know which one you meant.

## Try it without setting up any app

The brain needs nothing beyond `px0 init`:

```shell
px0 brain add https://example.com/some-post
px0 brain ask "what did that post say about caching?"
```

- **Local extraction**: Ingests text from web pages, local documents, PDFs, and YouTube transcripts directly on your machine.
- **Workflow-ready**: Workflows can query your knowledge base to summarize recent reads, look up reference material, or draft content backed by your own sources.

`brain add` takes a URL, a YouTube link, or a local file - `.md`, `.txt`, `.rst`, `.org`, `.pdf`, `.docx`, `.odt`, or a saved `.html` page. Extraction runs on your machine and needs no API key. `pdftotext` and `pandoc` are used when installed, but nothing depends on them being there.

Ingests are filed by what they are - `papers/` for PDFs, `blogs/` for web pages, `docs/` for everything else - and `--to` overrides that with any folder you like, including one your own vault already uses:

```shell
px0 brain add ./paper.pdf --to "Personal/Reading"
```

Each file records what it came from in its frontmatter, so you can narrow a search or a question to one kind of material:

```shell
px0 brain search "quorum" --kind paper
px0 brain ask "what did I read about backpressure?" --kind blog
```

The kinds are `blog`, `paper`, `doc`, `video`, and `stub`. Files px0 did not write carry no kind, so `--kind` never matches them.

Anything filed under `brain/work/` is excluded from retrieval by default and never leaves the machine:

```shell
px0 brain add ./internal-pricing.md --to work
```

### Already keep notes somewhere? Point px0 at them

A px0 brain and an Obsidian vault are the same thing on disk - a folder of Markdown - so you can point one at the other and keep writing where you already write:

```shell
px0 config set brain.path ~/Documents/MyVault
px0 brain reindex
```

Any folder of Markdown works: an Obsidian vault, a Logseq graph, a `notes/` directory in a repo. px0 reads it in place - `reindex`, `search`, and `ask` never write to it.

It skips what a real vault carries beside the notes: every dot-folder (`.obsidian/`, `.trash/`, `.git/`, `.stversions/`) and drawings stored as Markdown (`*.excalidraw.md`). Add your own patterns if you want:

```shell
px0 config set brain.ignore "*.excalidraw.md,Templates/*"
```

**One thing to know.** `work/` is px0's never-leaves-this-machine folder, so if your vault already has a top-level `work/`, those notes are held back from every search. `px0 config set brain.path` tells you when it spots this, `px0 brain list` marks such files `(private)`, and `px0 doctor` reports the count. To turn it off, or move it somewhere that will not collide:

```shell
px0 config set brain.private_folder ""            # nothing is held back
px0 config set brain.private_folder px0-private   # hold back this folder instead
```

## Reaching this machine, and your own tools

Beyond the apps Composio brokers, a workflow can use what is already on your
laptop:

| Tool | What it does |
| ---- | ------------ |
| `file.read`, `file.write`, `file.list` | Read and write files, inside the store and any directory you allow |
| `http.get`, `http.post` | Fetch or post to a URL that is not an app px0 has a connector for |
| `brain.add` | File something into your brain, so "save what I read" is a workflow |
| `memory.remember`, `memory.recall` | Keep and look up a fact about you or your work |
| `shell.run` | Run one local command. Off until you turn it on |

```shell
px0 config set tools.allow_shell true            # a workflow can then run anything you can
px0 config set tools.file_roots ~/code/my-repo   # and read files there
```

Anything else you want a workflow to do, you can declare yourself. One TOML file
per tool in `~/.px0/tools/`, read at run time:

```toml
id = "local.deploy_status"
description = "Print the deploy status for an environment"
command = ["./scripts/deploy-status.sh", "{env}"]
params = { env = "str*" }
is_write = false
```

It shows up in `px0 tools list` immediately, and `px0 workflows new` can use it.
Arguments are substituted into argv, never into a shell, so a value with a
semicolon in it stays a value.

## Teach it how you work

Guidelines are Markdown files describing your conventions: how you word a commit message, what your Go reviews check. Each one is `name` and `description` frontmatter over its rules, the same shape as a skill:

```markdown
---
name: commit-messages
description: How to word a commit message. Use when the workflow writes or rewrites one.
---

## Imperative mood summary line

Write the summary line in the imperative mood: "Add retry logic", not "Added".
```

The description is what makes it findable. When you build a workflow, px0 reads the descriptions and attaches only the guidelines whose standard that workflow's output is judged against — a nightly standup that summarizes commits does not inherit your commit-message convention. What it attaches is inlined verbatim into every run, so output comes back in your voice instead of the model's default.

You never write one from scratch. When `px0 workflows new` finds that a workflow leans on a convention you have no file for, it drafts that guideline from the workflow, shows it to you, and lists it on the workflow. Editing the draft is how it becomes yours:

```shell
px0 guidelines list
px0 guidelines edit commit-messages
```

## Where your data lives

Your store is `~/.px0`. Set `PX0_HOME` to move it.

| Folder        | What is in it                                                         |
| ------------- | --------------------------------------------------------------------- |
| `workflows/`  | The jobs px0 can run                                                  |
| `guidelines/` | How you work                                                          |
| `memory/`     | What px0 knows about you                                              |
| `brain/`      | What you have read and kept (or point `brain.path` at your own vault) |
| `output/`     | What runs produced                                                    |
| `tools/`      | Tools you wrote yourself, one small TOML file each                     |

All of it is plain Markdown you can open in any editor. Edit a workflow by hand and the next run picks it up, with no compile step. px0 keeps its own history, so you can see what changed and undo it:

```shell
px0 changes list
px0 changes show <change-id>
```

## Everyday commands

| Command                   | What it does                                   |
| ------------------------- | ---------------------------------------------- |
| `px0 workflows new`       | Interview you, then turn what you say into a workflow |
| `px0 workflows run`       | Run one now                                    |
| `px0 workflows edit`      | Revise a workflow and rebuild it               |
| `px0 ask`                 | Ask anything; px0 routes it                    |
| `px0 workflows disable`   | Park one without deleting it                   |
| `px0 workflows health`    | What a workflow's own runs say about it        |
| `px0 workflows improve`   | Revise a workflow from what its runs did       |
| `px0 workflows templatize`| Lift your own values out, so others can run it |
| `px0 status`              | Whether anything needs attention               |
| `px0 brain add`           | Save a URL or file to your brain               |
| `px0 brain ask`           | Ask a question across your brain               |
| `px0 guidelines list`     | The conventions px0 follows                     |
| `px0 guidelines edit`     | Reword one in your own words                    |
| `px0 runs`                | Browse past runs                               |
| `px0 runs mark`           | Say whether a run's output was any good        |
| `px0 runs stats`          | Runs rolled up by workflow                     |
| `px0 inbox`               | What your scheduled workflows produced         |
| `px0 approvals`           | Write calls waiting for you to send            |
| `px0 memory`              | What px0 knows about you                       |
| `px0 memory suggest`      | What it thinks it should remember              |
| `px0 workflows replay`    | Check a revision against inputs it already had |
| `px0 workflows recipes`   | Sentences to start from, if the page is blank  |
| `px0 store sync`          | Share a store between your machines            |
| `px0 tools search`        | Find a tool in Composio's catalogue            |
| `px0 tools connect`       | Authorize an app                               |
| `px0 tools list --status` | What workflows can call, and what is connected |
| `px0 daemon install`      | Run workflows on a schedule                    |
| `px0 config list`         | Every setting, with its default                |
| `px0 completion zsh`      | Shell completion                               |
| `px0 doctor`              | Check that everything is wired up              |
| `px0 update`              | Upgrade px0                                    |

Add `--help` to any of them.

## Uninstall

```shell
sh install.sh --uninstall
```

That removes px0 and leaves your store alone. Delete `~/.px0` yourself when you want it gone.

## License

px0 is released under the [MIT License](LICENSE).
