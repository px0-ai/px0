# `px0 tools`

Tools are the external app actions a workflow may call — send a Slack message,
open a Linear issue, read a Google Sheet. Every toolkit currently routes through
Composio, which brokers the authorization.

Implemented by `px0/tools.py` (the catalogue and execution) and `px0/connect.py`
(authorization).

```
px0 tools list [service] [--status] [--json]
px0 tools search [query] [--toolkit SLUG] [--toolkits] [--limit N] [--json]
px0 tools call <tool> [--arg KEY=VALUE]... [--yes] [--json]
px0 tools connect <app> [--reconnect]
px0 tools disconnect <app> [--yes]
px0 tools refresh [tool...] [--forget]
```

---

## `px0 tools list`

What workflows can call, grouped by service.

### `service`

Narrow the listing to one service.

- **Input:** a service slug, for example `github`, `slack`, `gmail`.
- **Default:** omit it to list every service px0 knows about.

```shell
px0 tools list
px0 tools list github
```

### `--status`

Also show whether each tool's app is authorized.

- **Input:** flag, no value. Default off.
- Costs one API call per app, which is why it is opt-in rather than always on.

Statuses are reported in terms of what to do about them:

| Status | Meaning |
| ------ | ------- |
| not authorized | No connection yet |
| consent pending | Authorization started but not completed in the browser |
| authorization failed | The attempt was rejected; start again |
| connected | Ready to use |

```shell
px0 tools list --status
px0 tools list slack --status
```

Authorization also happens on demand: `px0 workflows new` authorizes what the job
needs, and a tool whose app is not connected prints its own authorization URL on
the first run. `px0 tools connect` is for doing it deliberately. To supply or
replace the Composio key, use [`px0 config composio`](config.md#px0-config-composio).

### `--json`

Machine-readable output: one object per tool, with `status` included when
`--status` is also given.

- **Input:** flag, no value. Default off.

```shell
px0 tools list --json | jq '.[] | select(.is_write)'
```

### What is in the listing

Four kinds of tool, all callable the same way:

| Kind | Where it comes from | Example |
| ---- | ------------------- | ------- |
| curated | Hand-written in `px0/tools.py` | `github.list_my_prs` |
| local | Runs on this machine, `px0/localtools.py` | `file.read`, `shell.run`, `brain.add` |
| user-declared | A TOML file in the store's `tools/` folder | `local.deploy_status` |
| discovered | Found in Composio's catalogue by `px0 workflows new` | `composio:LINEAR_CREATE_ISSUE` |

A malformed file in `tools/` is reported as a warning here and skipped, so one
bad declaration never hides the rest.

---

## `px0 tools search`

Search Composio's catalogue: thousands of tools across more than 1,300 toolkits.
This is how to find out what px0 could reach before describing a job to
`px0 workflows new`, which is the only other thing that searches the catalogue.

### `query`

What the tool should do.

- **Input:** a few words, matched as a substring by Composio. Fewer words match
  more.
- **Default:** required, unless `--toolkits` is given.

```shell
px0 tools search "create issue"
```

### `--toolkit SLUG`

Restrict the search to one toolkit.

- **Input:** a toolkit slug, for example `github`, `linear`, `stripe`.
- **Default:** every toolkit.

```shell
px0 tools search "issue" --toolkit linear
```

### `--toolkits`

List matching toolkits instead of individual tools, with the number of tools and
event triggers each one publishes.

- **Input:** flag, no value. Default off.
- With no `query`, lists the largest toolkits.

```shell
px0 tools search --toolkits
px0 tools search accounting --toolkits
```

### `--limit N`

How many results to print.

- **Input:** a whole number, capped at 500 for toolkits.
- **Default:** 20 for tools, 40 for toolkits.

### `--json`

Machine-readable output.

- **Input:** flag, no value. Default off.

```shell
px0 tools search "send message" --json | jq '.[].slug'
```

Results are marked `read`, `write`, or `destroy`, from Composio's own hints. A
`destroy` tool can delete or overwrite.

---

## `px0 tools call`

Call one tool with one set of arguments and look at what comes back. A dry run
stubs every write, so without this the first real call a tool ever makes is
inside a live run.

### `tool` (required)

Which tool to call.

- **Input:** a tool id as printed by `px0 tools list`, including a
  `composio:`-prefixed discovered one.

### `--arg KEY=VALUE`

One argument. Repeatable.

- **Input:** `key=value`. A value that parses as JSON is passed as JSON, so
  `--arg limit=5` sends a number and `--arg labels='["bug"]'` sends a list.
- **Default:** none. A missing required parameter is refused before the call.

```shell
px0 tools call github.get_pr --arg url=https://github.com/px0/px0/pull/1
px0 tools call file.read --arg path=brain/docs/note.md
```

### `--yes`

Skip the confirmation a write tool asks for.

- **Input:** flag, no value. Default off.
- A write tool changes something outside px0, so it confirms first unless told
  not to.

### `--json`

Print the result as JSON rather than formatted text.

- **Input:** flag, no value. Default off.

Stored secrets are redacted out of the result before it is printed.

---

## `px0 tools connect`

Authorize an app. Any of Composio's toolkits can be named, not a fixed list.

### `app` (required)

Which app.

- **Input:** a toolkit slug (`slack`, `linear`, `google_drive`), or one of px0's
  own names (`calendar`, which is Composio's `googlecalendar`).

```shell
px0 tools connect linear
```

px0 prints a URL. Open it, complete the consent, and confirm with
`px0 tools list --status`.

### `--reconnect`

Drop the existing authorization first.

- **Input:** flag, no value. Default off.
- The fix for a token that has expired or been revoked: without it, an app that
  is already recorded reports that it is authorized and stops.

```shell
px0 tools connect gmail --reconnect
```

---

## `px0 tools disconnect`

Revoke an app's authorization: deleted at Composio, and removed from the store's
credentials.

### `app` (required)

Which app. Same forms as `connect`.

### `--yes`

Skip the confirmation.

- **Input:** flag, no value. Default off.
- Workflows that use the app are named before the confirmation, since they stop
  working.

```shell
px0 tools disconnect slack
```

If Composio refuses the delete, the local record is still removed and px0 says
so — the account it pointed at is unusable either way.

---

## `px0 tools refresh`

Re-read cached tool definitions from Composio. A discovered tool's schema is
cached in the store so a workflow keeps working offline; the cache is what goes
stale when Composio reshapes or retires a tool.

### `tool`

Which tools to re-read.

- **Input:** any number of tool ids or Composio slugs.
- **Default:** every cached tool.

```shell
px0 tools refresh
px0 tools refresh composio:LINEAR_CREATE_ISSUE
```

A tool that no longer exists in the catalogue is dropped rather than kept with a
schema that describes nothing.

### `--forget`

Drop the cached definitions instead of re-reading them.

- **Input:** flag, no value. Default off.
- A workflow naming a forgotten tool will not validate until
  `px0 workflows edit` finds it again.

```shell
px0 tools refresh --forget
```

## Related configuration

| Key | Effect |
| --- | ------ |
| `tools.allow_shell` | Whether the `shell.run` tool may run at all. Off by default |
| `tools.file_roots` | Extra directories `file.read` and `file.write` may touch |
| `tools.http_timeout` | Seconds before `http.get` and `http.post` give up |
| `tools.max_output_bytes` | Cap on how much text a local tool returns to the model |
| `connectors.composio_api_key` | Key used to authenticate app connections |
| `connectors.retries` | Per-run transient retries, with exponential backoff |
| `connectors.ca_bundle` | CA bundle for TLS verification behind an intercepting proxy |
| `connectors.provider` | Intended default broker; not yet enforced — everything routes through Composio |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown service, unknown tool, a bad `--arg`, or a refused confirmation |
| `2` | Composio could not be reached, the stored key was rejected, or a tool call failed |
