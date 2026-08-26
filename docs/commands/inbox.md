# `px0 inbox`

What your scheduled workflows produced, in a place you will actually look.

A workflow could route its output three ways, and on a schedule all three had
the same problem. `stdout` goes to a terminal nobody is sitting at. `file`
writes something you have to remember to open. A write tool posts somewhere
else, which is fine when that somewhere is where you already look and useless
when it is not. px0 did the work and then had nowhere to say so.

Implemented by `px0/inbox.py`.

```
px0 inbox [list] [--all] [--workflow ID] [--json]
px0 inbox read [entry-id] [--json]
px0 inbox archive <entry-id>
px0 inbox clear [--all]
```

## What gets delivered

Scheduled and watched runs, by default. Manual ones do not: you were there for
a manual run and have just read its output, where a nightly one produced
something at 6am that nothing has told you about. A rehearsal never delivers —
a dry run's output is a sample, not news.

A workflow can force either answer:

```yaml
output:
  target: inbox          # the inbox is where this goes
```

```yaml
output:
  target: file
  path: output/digest-{date}.md
  inbox: true            # write the file *and* say it arrived
```

`inbox: false` opts a scheduled workflow out. `px0 config set inbox.auto false`
turns automatic delivery off everywhere.

An entry is small on purpose — what produced it, a preview, and where the whole
thing is — because the inbox is a place to triage from, not a second copy of
the output. Its title comes from the output's own first heading, since a
workflow that already writes `## PRs you reviewed this week` has said what the
entry is better than any label px0 could synthesize.

## `px0 inbox` / `px0 inbox list`

What is waiting, newest first.

### `--all`

Include entries you have already read.

### `--workflow ID`

Only entries from one workflow.

## `px0 inbox read`

Reads one entry and marks it read. With no id, reads the oldest unread.

Where the entry announces a file, the file is read back from disk rather than
from the copy taken at delivery — so you see what is there now, which is what
matters when a later run has rewritten it.

## `px0 inbox archive`

Keeps an entry but stops listing it.

## `px0 inbox clear`

Deletes entries you have read. `--all` deletes unread ones too.

Read and archived entries age out on `inbox.keep_days`. **Unread entries are
never dropped** — an inbox that quietly forgets what you have not looked at is
worse than one that grows.

## Related configuration

| Key | Effect |
| --- | ------ |
| `inbox.auto` | Deliver scheduled and watched runs automatically |
| `inbox.keep_days` | How long read and archived entries are kept |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown entry id |
