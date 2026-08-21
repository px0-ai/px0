# px0

Describe a recurring job in plain English and px0 writes it as a workflow you can run, schedule, and edit.

Everything happens on your machine. Your workflows, your notes, and your conventions are plain Markdown files in one directory you own. There is no server, no account, and no hosted state to sign up for.

Workflows do not stop at your laptop, though. Through [Composio](https://composio.dev), a workflow can reach hundreds of apps you already use: GitHub, Slack, Gmail, Google Calendar, Notion, Linear, Jira, Google Sheets, Salesforce, Stripe, Zendesk, Sentry, and over a thousand more.

## What px0 is for

- Recurring reports you assemble by hand every week.
- Chores that follow the same steps every time: draft the release notes, precheck a pull request, triage overnight alerts, file the standup update.
- Work that has to sound like you, because px0 follows conventions you write down once.
- A searchable library of everything you read, so you can ask questions across it later.

## Workflows you could build

Each of these is one sentence you type into `px0 workflows new`, and each becomes a file you can edit and put on a schedule.

| What you want | Apps it touches |
| --------------------------------------------------------------------- | ------------------------------ |
| Every Friday, summarize the pull requests I reviewed and post it to #eng-standup | GitHub, Slack |
| Each morning, brief me on today's meetings and the emails I have not replied to | Google Calendar, Gmail |
| Turn last night's error spike into a triaged bug ticket | Sentry, Linear |
| Post a Monday sprint status from our issue tracker to the team channel | Jira, Slack |
| Draft release notes from the commits since the last tag and file them as a page | GitHub, Notion |
| Log this week's revenue and refunds into the finance sheet | Stripe, Google Sheets |
| Group this week's support tickets by theme and open issues for the top three | Zendesk, Linear |
| Save every newsletter I star to my reading library | Gmail, px0 knowledge |

## Install

You will need:

- Python 3.11 or newer.
- A coding agent CLI that px0 uses as its model backend: `claude`, `gemini`, `pi`, or `opencode`. px0 reuses that CLI's own login, so pick whichever one you already sign into.
- A [Composio](https://composio.dev) API key, for workflows that reach other apps. You can skip this and add it later.

Then run:

```shell
curl -fsSL https://raw.githubusercontent.com/px0/px0/master/install.sh | sh
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

px0 asks about anything genuinely ambiguous, finds the tools the job needs, and shows you the list before authorizing anything. Tools that can post or send get called out, so you can drop the ones you did not ask for. Then it writes the workflow file and prints its id. Pass `--id <name>` to choose the id yourself.

### 3. Run it

```shell
px0 workflows run friday-pr-digest --dry-run   # resolve inputs, call nothing
px0 workflows run friday-pr-digest             # for real
```

The first time a workflow needs Slack or Gmail, px0 hands you a URL to approve. You only authorize the apps you actually use.

### 4. Put it on a schedule

Your workflow already carries the schedule from the sentence you typed. Install the scheduler so it fires on its own:

```shell
px0 daemon install
px0 daemon status
```

### 5. See what happened

```shell
px0 workflows list      # what you can run
px0 runs                # browse past runs
px0 runs why <run-id>   # how a run reached its result
```

## Try it without setting up any app

The knowledge library needs nothing beyond `px0 init`:

```shell
px0 knowledge add https://example.com/some-post
px0 knowledge ask "what did that post say about caching?"
```

Add posts, papers, and internal docs as you read them, then ask across all of them.

## Teach it how you work

Guidelines are Markdown files describing your conventions: how you word a commit message, what your Go reviews check. Workflows that need a guideline inline it verbatim, so output comes back in your voice instead of the model's default.

```shell
px0 guidelines list
$EDITOR ~/.px0/guidelines/commit-messages.md
```

px0 notices patterns in what you read and how you edit its output, then proposes guideline updates. It never edits your files on its own. You accept or reject each one:

```shell
px0 guidelines review
```

## Where your data lives

Your store is `~/.px0`. Set `PX0_HOME` to move it.

| Folder       | What is in it              |
| ------------ | -------------------------- |
| `workflows/` | The jobs px0 can run       |
| `guidelines/`| How you work               |
| `knowledge/` | What you have read and kept|
| `output/`    | What runs produced         |

All of it is plain Markdown you can open in any editor. Edit a workflow by hand and the next run picks it up, with no compile step. px0 keeps its own history, so you can see what changed and undo it:

```shell
px0 versions list workflows/friday-pr-digest.md
px0 changes list
```

## Everyday commands

| Command                            | What it does                                  |
| ---------------------------------- | --------------------------------------------- |
| `px0 workflows new`                | Turn a sentence into a workflow               |
| `px0 workflows run`                | Run one now                                   |
| `px0 workflows edit`               | Revise a workflow and rebuild it              |
| `px0 knowledge add`                | Save a URL or file to your library            |
| `px0 knowledge ask`                | Ask a question across your library            |
| `px0 guidelines review`            | Accept or reject proposed conventions         |
| `px0 runs`                         | Browse past runs                              |
| `px0 tools list --status`          | What workflows can call, and what is connected|
| `px0 daemon install`               | Run workflows on a schedule                   |
| `px0 config list`                  | Every setting, with its default               |
| `px0 doctor`                       | Check that everything is wired up             |
| `px0 update`                       | Upgrade px0                                   |

Add `--help` to any of them.

## Uninstall

```shell
sh install.sh --uninstall
```

That removes px0 and leaves your store alone. Delete `~/.px0` yourself when you want it gone.

## License

px0 is released under the [MIT License](LICENSE).
