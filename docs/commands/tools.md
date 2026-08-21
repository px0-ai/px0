# `px0 tools`

Tools are the external app actions a workflow may call — send a Slack message,
open a Linear issue, read a Google Sheet. Every toolkit currently routes through
Composio, which brokers the authorization.

Implemented by `px0/tools.py` (the catalogue and execution) and `px0/connect.py`
(authorization).

```
px0 tools list [service] [--status]
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

Authorization itself happens during `px0 workflows new`, when px0 discovers the
job needs an app you have not connected. To supply or replace the Composio key,
use [`px0 config composio`](config.md#px0-config-composio).

## Related configuration

| Key | Effect |
| --- | ------ |
| `connectors.composio_api_key` | Key used to authenticate app connections |
| `connectors.retries` | Per-run transient retries, with exponential backoff |
| `connectors.ca_bundle` | CA bundle for TLS verification behind an intercepting proxy |
| `connectors.provider` | Intended default broker; not yet enforced — everything routes through Composio |

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown service |
| `2` | Composio could not be reached, or the stored key was rejected |
