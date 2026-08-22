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

Each of these is one sentence you type into `px0 workflows new`, and each becomes a file you can edit and put on a schedule.

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

px0 ships no workflows. You describe what you want:

```shell
px0 workflows new "every friday at 5pm, summarize the github pull requests I reviewed this week and post it to #eng-standup"
```

Or run it with nothing and be asked instead:

```shell
px0 workflows new
```

That opens an interview - one question at a time, until px0 has the job, what it reads, where the result goes, when it runs, and what makes the output right. It writes the request back for you to approve or reword before anything is built. Enter on a blank line ends the questions early.

Either way, px0 asks about anything still genuinely ambiguous, finds the tools the job needs, and shows you the list before authorizing anything. Tools that can post or send get called out, so you can drop the ones you did not ask for. Then it writes the workflow file and prints its id. Pass `--id <name>` to choose the id yourself.

### 3. Run it

```shell
px0 workflows run friday-pr-digest --dry-run   # resolve inputs, call nothing
px0 workflows run friday-pr-digest             # for real
```

The first time a workflow needs Slack or Gmail, px0 hands you a URL to approve. You only authorize the apps you actually use.

### 4. Put it on a schedule, or on a watch

Your workflow already carries the schedule from the sentence you typed. Install the scheduler so it fires on its own:

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

Guidelines are Markdown files describing your conventions: how you word a commit message, what your Go reviews check. Workflows that need a guideline inline it verbatim, so output comes back in your voice instead of the model's default.

You never write one from scratch. When `px0 workflows new` finds that a workflow leans on a convention you have no file for, it drafts that guideline from the workflow, shows it to you, and lists it on the workflow so every run inlines it. Editing the draft is how it becomes yours:

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
| `px0 workflows new`       | Turn a sentence into a workflow, or be asked for one |
| `px0 workflows run`       | Run one now                                    |
| `px0 workflows edit`      | Revise a workflow and rebuild it               |
| `px0 workflows disable`   | Park one without deleting it                   |
| `px0 status`              | Whether anything needs attention               |
| `px0 brain add`           | Save a URL or file to your brain               |
| `px0 brain ask`           | Ask a question across your brain               |
| `px0 guidelines list`     | The conventions px0 follows                     |
| `px0 guidelines edit`     | Reword one in your own words                    |
| `px0 runs`                | Browse past runs                               |
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
