# Command reference

Every command follows the same shape: a **group** naming the entity, then a
**verb** acting on it.

```
px0 <group> <verb> [arguments] [options]
```

`px0 brain add`, `px0 workflows run`, `px0 guidelines list`. The only flat
commands — no verb — are the ones that act on the install rather than on anything
in the store: `init`, `update`, `version`, `doctor`, `status`,
`completion`, and `uninstall`.

## Groups

| Group | What it manages | Module |
| ----- | --------------- | ------ |
| [`init`](init.md) | Scaffolding a new store | `px0/store.py` |
| [`workflows`](workflows.md) | Building, running, and editing workflows | `px0/workflow.py`, `px0/builder.py`, `px0/runner.py` |
| [`brain`](brain.md) | Ingesting, searching, and asking over your material | `px0/brain.py`, `px0/retrieval.py`, `px0/ask.py` |
| [`guidelines`](guidelines.md) | The conventions px0 follows when it works | `px0/authoring.py`, `px0/claims.py` |
| [`tools`](tools.md) | What workflows can call, and what is authorized | `px0/tools.py`, `px0/connect.py` |
| [`runs`](runs.md) | Inspecting and replaying past executions | `px0/runs.py`, `px0/runs_tui.py` |
| [`daemon`](daemon.md) | Running workflows on a schedule | `px0/daemon.py` |
| [`changes`](changes.md) | The store's change log, across files | `px0/versioning.py` |
| [`store`](store.md) | The store as a whole | `px0/store.py` |
| [`config`](config.md) | Reading and writing `config.toml` | `px0/config.py` |
| [`update`](update.md) | Upgrading px0 and migrating the store | `px0/update.py` |
| [`status`](status.md) | Whether anything needs attention | `px0/status.py` |
| [`completion`](completion.md) | Shell completion scripts | `px0/completion.py` |
| [`mcp`](mcp.md) | Serving the brain and workflows over MCP | `px0/mcp.py` |
| [`doctor`](doctor.md) | Checking that everything is wired up | `px0/doctor.py` |
| [`uninstall`](uninstall.md) | Removing px0 and its store entirely | `px0/cli.py` |

## Conventions

These hold across every command.

### Getting help

`--help` works at every level, and is the fastest way to see the current surface:

```shell
px0 --help
px0 brain --help
px0 brain add --help
```

### A group with no verb

Naming a group without a verb is an error, so the command always says what it
means to do — with one exception: `px0 runs` opens an interactive run browser.
Piped or redirected, it prints the listing instead, so `px0 runs | head` behaves
like `px0 runs list`.

### Global flags

Accepted before the group name, on any command:

| Flag | Effect |
| ---- | ------ |
| `--json` | Machine-readable output where the command supports it |
| `--no-color` | Plain output, no colour or animation. Same as `NO_COLOR=1` |
| `--help`, `-h` | Show help at whatever level it is given |

### `--json`

Where a command offers `--json`, it prints machine-readable output on stdout and
nothing else, so it can be piped into `jq`. Progress spinners are written to
stderr and never interleave with it.

Available on: `runs list`, `runs show`, `brain search`, `brain show`,
`changes list`, `config list`, `config get`, `config path`,
`doctor`, `status`, `store path`, `store verify`, `tools list`,
`tools search`, `tools call`, `workflows run`, `workflows show`,
`workflows validate`.

### Exit codes

| Code | Meaning |
| ---- | ------- |
| `0` | Success |
| `1` | User error: bad input, missing file, unknown id, failed validation |
| `2` | Connector error: an external app call failed or is not authorized |
| `3` | Model error: the coding-agent harness failed, timed out, or is missing |
| `4` | Integrity error: the store's version history or index is inconsistent |

### Confirmations

Every command that removes or revokes something asks first, and every one of them
takes `--yes` to skip the question. With stdin not a terminal and no `--yes`, the
command stops rather than assuming: `px0 workflows delete`, `px0 brain rm`,
`px0 guidelines rm`, `px0 tools disconnect`, and `px0 tools call` on a write tool.

### Environment

| Variable | Effect |
| -------- | ------ |
| `PX0_HOME` | Store location. Defaults to `~/.px0`. |
| `COMPOSIO_API_KEY` | Composio key. Overrides `connectors.composio_api_key`. |
| `REQUESTS_CA_BUNDLE`, `SSL_CERT_FILE` | Set by px0 from `connectors.ca_bundle` so every outbound HTTPS call trusts an intercepting proxy. |
| `NO_COLOR`, `FORCE_COLOR`, `TERM` | Control coloured output. |
| `PAGER` | Pager used for long output. |
