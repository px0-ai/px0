# Connections and tools

A workflow reaches outside px0 through a *tool*, and every tool routes
through [Composio](https://composio.dev). Two kinds exist:

- **Curated** (`slack.post_message`, `github.get_pr`) -- ten hand-written
  tools with stable argument names.
- **Discovered** (`composio:SLACK_SEND_MESSAGE`) -- anything `px0 workflows new`
  found by searching Composio's catalogue, which is thousands of tools
  across hundreds of toolkits.

They execute identically. The `composio:` prefix just tells you where the
tool came from.

There is no connect command. You set one API key, and after that **apps
authorize themselves**: the first time a workflow needs Gmail, px0
prepares Gmail's authorization and hands you the URL to approve. You
never have to know which services exist, or connect one you turn out not
to need.

## 1. Set up the Composio API key, once

```shell
px0 config composio <your-api-key>
px0 config composio               # prompts, and masks the existing key
```

`px0 init` asks for the same key, so on a fresh store this is already
done. Get the key from the Composio platform: **Get Started → Settings →
API Keys**. px0 verifies it against the live API before storing it, so a
typo'd or revoked key fails here rather than mid-workflow.

The key needs **write** access to `auth_configs` -- that's the permission
that lets px0 prepare an app's authorization on your behalf. A read-only
key gets as far as the first tool call and then reports Composio's own
refusal:

```
slack is not connected yet, and preparing its authorization failed:
Error code: 403 - This API key does not have the permissions required for
POST /api/v3/auth_configs. This route requires "auth_configs" write access,
but the key has read access.
```

That's a permission to grant on the key in Composio, not something px0
can work around. Note what px0 does *not* do there: it reports why it
couldn't prepare a link rather than printing a dead URL.

The key is stored in `config.toml` under `connectors.composio_api_key`
and in `.state/credentials.toml`, which px0 keeps at mode `0600` and
`px0 doctor` checks.

### Behind a TLS-intercepting proxy

If your network runs a TLS-inspecting proxy (Zscaler, Netskope, and
friends), setting the key detects it: the certificate chain won't
validate against the public CA bundle Python ships with. px0 looks for a
system CA bundle that *does* trust the interceptor, retries with it, and
records it as `connectors.ca_bundle` so later runs reuse it:

```
· TLS is intercepted on this network  verifying against /opt/homebrew/etc/ca-certificates/cert.pem
✓ Composio API key stored
```

If none of the bundles it knows about work, it tells you to point
`SSL_CERT_FILE` at your corporate root and stops -- it never silently
disables verification. An explicit `SSL_CERT_FILE` always wins over the
stored bundle.

## 2. Apps authorize themselves

Write a workflow that needs Slack, run it, and px0 does the rest:

```shell
px0 workflows run post-standup
```

```
✗ slack is not connected yet. Authorize it by opening:
    https://backend.composio.dev/s/...
```

Open the URL, approve the consent screen, and run it again. That's the
whole flow.

Usually you never reach it, because `px0 workflows new` asks first: once it knows
which tools the workflow needs, it checks what's authorized and offers to
prepare the rest, so a workflow is authorized before it ever runs. See
[02-building-a-workflow.md](02-building-a-workflow.md).

Preparing a link is idempotent: the underlying auth config is created
once and cached, so a second attempt reuses it instead of piling up
duplicates. And nothing is granted until a human consents in the
browser -- px0 minting a URL gives it no access by itself.

If an authorization later breaks (revoked in the provider, deleted in
Composio), the next run that needs it offers a fresh link. There is
nothing to remove or reset by hand.

## 3. What's available, and what's ready

```shell
px0 tools list
px0 tools list --status      # also ask Composio what's authorized
px0 tools list gmail         # one provider
```

```
  read   calendar.list_events          List calendar events in a window          not authorized
  write  github.create_review_comment  Post a review comment on a PR             not authorized
  read   github.get_pr                 Fetch one pull request by URL             not authorized
  read   github.get_pr_diff            Fetch the unified diff of a pull request  not authorized
  read   github.list_my_prs            PRs authored by the connected user        not authorized
  read   github.list_review_comments   List existing review comments on a PR     not authorized
  read   gmail.get_message             Fetch one gmail message                   ready
  read   gmail.search_messages         Search gmail messages                     ready
  write  gmail.send_message            Send a gmail message                      ready
  write  slack.post_message            Post a message to a slack channel         consent pending

3 of 10 tools can change things outside px0

not authorized yet: calendar, github, slack -- a workflow that needs one
prints its authorization URL on the first run
```

`--status` costs one API call per provider, which is why it's opt-in
rather than the default.

The `read` / `write` marker is the one that matters, and it's the only
thing on that screen px0 bothers to colour. A write tool changes
something outside px0 -- posts, comments, sends. px0 surfaces write tools
separately everywhere it can: `px0 workflows new` calls them out before generating
a workflow, `px0 workflows run --dry-run` stubs them instead of executing them, and
`px0 runs` marks any run that used one with `[write]`.

`px0 --json tools list` gives the same list with each tool's parameter
schema, for when you're writing a workflow's `args` by hand. Required
parameters are marked with a trailing `*` and listed first.

Four toolkits have curated tools: `calendar`, `github`, `gmail`, `slack`.
Any toolkit in Composio's catalogue can be reached through a discovered
tool -- `px0 workflows new` searches for what your task needs rather than limiting
you to that list. Discovered tools are recorded in the store, so they
appear here too once a workflow uses one.

| Status | Meaning |
| --- | --- |
| `ready` | Authorized and usable |
| `not authorized` | Never approved; the next run that needs it offers a link |
| `consent pending` | A link was opened but the browser consent wasn't finished |
| `authorization failed` | Composio rejected it |
| `authorization gone` | px0 holds an id Composio no longer knows about |

`px0 doctor` fails (exit `4`) on any stored authorization that isn't
active, so a health check catches a connection that lapsed:

```
✗ connections  gmail connected_account is INITIATED, not ACTIVE -- finish the browser consent
```

## 4. Use a tool in a workflow

Nothing special is required -- name it in the frontmatter and the runner
takes care of both auth and, if needed, asking you to authorize:

```yaml
---
id: post-standup
kind: workflow
description: post yesterday's calendar summary to #standup
inputs:
  - id: meetings
    tool: calendar.list_events
    args: {window: yesterday}
tools: [slack.post_message]
output: {target: stdout}
---
Summarize {{meetings}} in three bullets, then post it to #standup.
```

Inputs run before the prompt and their results are interpolated into it;
tools in the `tools:` list are callable by the model during the run.

## 5. When something isn't authorized mid-run

A tool call whose app isn't ready fails cleanly rather than crashing.
The run is recorded as `failed`, and the reason carries the URL:

```
slack is not connected yet. Authorize it by opening:
  https://backend.composio.dev/s/...
```

A consent that was started but abandoned says so instead of re-minting a
link you already have open:

```
slack authorization was started but never completed -- open the URL px0
printed for it and finish the browser consent
```

Transient failures (network, Composio 5xx) are retried per
`connectors.retries` (default `3`) before the run gives up, so a blip
doesn't fail a scheduled workflow.

## Next

- [02-building-a-workflow.md](02-building-a-workflow.md) -- the builder
  flow that picks tools for you and authorizes them up front.
- [06-scheduling-and-the-daemon.md](06-scheduling-and-the-daemon.md) --
  run an authorized workflow on a schedule.
- [07-browsing-runs.md](07-browsing-runs.md) -- see which tool calls a
  run made and how long each took.
