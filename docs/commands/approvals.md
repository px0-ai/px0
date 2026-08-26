# `px0 approvals`

Write tool calls that wait for a person.

px0's trust model was binary: a tool either mutates something or it does not,
and a workflow either may call it or may not. `--dry-run` stubs every write,
which rehearses a workflow but never does the work. There was no middle — no
way to say *draft it and ask me* — so anything that speaks in your name had to
be handed the real capability up front, on the strength of a plan you read once.

This is that middle. A held-back call is not executed: it is written down in
full — tool, arguments, the run that drafted it, and what that run produced —
and the model is told it has been queued, so the run still finishes and still
produces its output. You see exactly what would be sent, and approving it makes
the call for real.

Implemented by `px0/approvals.py`.

```
px0 approvals [list] [--all] [--workflow ID] [--json]
px0 approvals show <approval-id> [--json]
px0 approvals edit <approval-id> [--set KEY=VALUE] [--note WHY]
px0 approvals approve <approval-id> [--yes]
px0 approvals reject <approval-id> [--reason WHY]
px0 approvals purge [--days N]
```

## Turning it on

Per workflow, in its frontmatter — this wins over the store-wide default in
both directions:

```yaml
confirm: true                  # every write this workflow makes
confirm:                       # or only these
  - slack.post_message
```

Store-wide, for everything that does not say otherwise:

```shell
px0 config set tools.confirm_writes true
```

Read tools never wait. A queue that fills up with searches is one nobody reads,
and the point of the gate is the calls that leave a mark.

## `px0 approvals` / `px0 approvals list`

What is waiting, oldest first — unlike every run listing in px0, because this
is a queue rather than a history and the thing most likely to have gone stale
is the thing you most need to see.

### `--all`

Include ones already sent, rejected, or expired.

### `--workflow ID`

Only drafts from one workflow.

## `px0 approvals show`

Exactly what one call would send: the arguments in full, and the output the run
produced. Both, because a Slack message shown without the digest it announces
is a decision made blind.

## `px0 approvals approve`

Sends it.

**This calls the tool with the recorded arguments — it does not re-run the
workflow.** A re-run would draft something else against a later hour or a
changed source, and the thing you read would never have been sent.

A call that fails here is left as `failed` rather than returned to the queue:
retrying is your decision, and one that silently went back to `pending` would
be approved twice.

### `--yes`

Skip the confirmation.

## `px0 approvals edit`

Changes what a drafted call would send, before sending it.

"Right message, wrong channel" had only one answer before this: reject it and
run the workflow again, which drafts something else against a later hour. The
message you actually wanted was never sendable.

```shell
px0 approvals edit apr_20260826-090000-a1b2 --set channel=#ops
px0 approvals edit apr_20260826-090000-a1b2          # opens your editor
```

`--set` takes `KEY=VALUE` and is repeatable; a value that parses as JSON is
read as JSON, so `--set count=3` sets a number and `--set channel=#ops` sets a
string. With no `--set`, the arguments open in `$EDITOR` as JSON — and JSON
that comes back unparseable changes nothing rather than being guessed at.

Every edit is stamped on the approval and shown on its screen, because what
goes out should never be silently different from what the run produced.

## `px0 approvals reject`

Throws the draft away. Nothing is sent.

### `--reason WHY`

Worth giving. "Wrong channel" and "we decided not to announce this" are the
same rejection to the queue and different facts about the workflow — and
[`px0 workflows improve`](workflows.md#px0-workflows-improve) reads them.

## `px0 approvals purge`

Deletes resolved approvals past `approvals.keep_resolved_days`. Pending ones
are never purged: the queue is the point.

## Going stale

A drafted message is written against a moment. Sending last Tuesday's standup
on Friday because it was still sitting in the queue is worse than not sending
it, so a draft older than `approvals.expire_days` (7 by default) stops being
approvable. Set it to `0` to never expire.

`px0 status` counts what is waiting, because an approval nobody knows about is
worse than no approval. An **unattended** run that drafts something also tells
you through the same channels a failure uses — desktop notification or a
message tool — since nobody was there to see it happen and silence would leave
you believing the message went out. It follows `notify.on_approval`, which
falls back to `notify.on_failure` so a store that already said how it wants to
hear about failures does not have to say it twice. A manual run is not notified
about: you are sitting there, and it prints the draft.

## Answering from somewhere else

The queue notified you wherever you asked and could only be answered at the
machine px0 runs on — the wrong shape for drafted writes, since approvals
happen when you are away from the desk.

px0 has no server, so this is polling: a read tool is asked what came back, and
replies naming an approval are acted on.

```shell
px0 config set approvals.reply_tool slack.read_channel
px0 config set approvals.reply_args '{"channel": "#px0"}'
px0 config set approvals.reply_from arpit
```

A reply is recognized as `approve apr_...`, `reject apr_...`, or just
`yes apr_...` / `no apr_...`, since that is what people actually type in answer
to a message asking them a question.

**Both settings are required.** A reply channel with no sender list is an
approval queue that anyone able to post there could empty, so px0 refuses to
run half-configured. A reply from anyone not on the list is logged and ignored.

Every reply goes through the same path a person at the terminal takes: an
expired draft stays expired, an answered one is not answered twice, and the
call that goes out is the one that was drafted. The daemon polls only while
something is actually waiting.

## Related configuration

| Key | Effect |
| --- | ------ |
| `tools.confirm_writes` | Hold every write for approval, across every workflow |
| `approvals.expire_days` | How long a draft stays sendable |
| `approvals.keep_resolved_days` | How long resolved approvals are kept |
| `notify.on_approval` | How you hear that an unattended run is waiting |
| `approvals.reply_tool` | Read tool polled for replies |
| `approvals.reply_from` | Who may answer by reply — required alongside the tool |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown approval id, one already resolved, or a refused confirmation |
| `2` | The tool call failed when approved |
