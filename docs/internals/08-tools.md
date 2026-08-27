# 8. Tools and connectors

Modules: `px0/tools.py`, `px0/localtools.py`, `px0/catalogue.py`, `px0/connect.py`

Every `tool:` in an input and every entry in a workflow's `tools:` names something in one flat namespace. Four different kinds of thing live in that namespace, and a workflow file cannot tell them apart.

## The four kinds

| Kind | Id shape | Defined in | Runs |
| ---- | -------- | ---------- | ---- |
| Curated | `github.get_pr` | `tools.REGISTRY` | Through Composio |
| Local built-in | `file.read` | `localtools.BUILTINS` | On this machine |
| User-declared | `local.deploy` | `<store>/tools/*.toml` | On this machine |
| Discovered | `composio:GMAIL_SEND_EMAIL` | `.state/catalogue.json` | Through Composio |

`tools.resolve(tool_id, home)` is the single entry point, and its order is the resolution order:

```python
if tool_id in REGISTRY:            return REGISTRY[tool_id]
if tool_id in localtools.BUILTINS: return _local_spec(tool_id)
if home is None:                   return None
if catalogue.is_catalogue_id(tool_id):
    tool = catalogue.load_cached(home).get(tool_id)
    return _discovered_spec(tool) if tool else None
return user_specs(home).get(tool_id)
```

The `home` argument is what makes the last two visible: their definitions live in the store, not in the module. A caller that omits it sees only the two built-in sets, which is why validation and the run loop both pass it.

Every kind ends up as the same `ToolSpec`:

```python
@dataclass
class ToolSpec:
    id: str
    provider: str
    description: str
    params: dict[str, str]
    is_write: bool
    handler: Callable[[dict, Context], Any]
```

`exists`, `is_write`, `list_tools`, and `call` all go through `resolve`, so authorization on demand, retries, dry-run stubbing, and the approval gate behave identically whether a tool was hand-written or found by a catalogue search.

Parameter types use a compact notation: `{"path": "str*"}`, where a trailing `*` marks the parameter required. `mcp._json_schema` expands that into JSON Schema when a tool is exposed over MCP.

## Curated tools

`tools.REGISTRY` holds ten hand-written tools across GitHub, Google Calendar, Gmail, and Slack. They exist so a fresh store can do something useful before any catalogue search happens, and so px0's own features -- failure notifications, approval replies -- have named tools to reach for.

The GitHub tools proxy the GitHub REST API through Composio rather than holding their own token, so there is still exactly one credential in the store. `_github_request` builds the request as Composio proxy parameters and wraps the response in a small adapter so the handlers read like ordinary `requests` code.

`_TOOL_SLUGS` maps px0's names onto Composio slugs, and each one was resolved against the live catalogue rather than inferred:

```python
"gmail.get_message": "GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID",
# not GMAIL_GET_EMAIL, which does not exist in the catalogue (404)
```

Composio's naming is not predictable from pattern, which is exactly why the builder makes a model search the catalogue instead of guessing.

## Local tools

`localtools.BUILTINS` covers what Composio cannot reach: the machine px0 is running on.

| Tool | Write | What it does |
| ---- | ----- | ------------ |
| `file.read` | no | Read a text file inside an allowed root |
| `file.list` | no | List files matching a glob inside an allowed root |
| `file.write` | yes | Write a text file inside an allowed root |
| `shell.run` | yes | Run one local command; disabled by default |
| `http.get` | no | Fetch a URL |
| `http.post` | yes | Send a request with a body |
| `brain.add` | yes | Ingest a URL or file into the brain |
| `memory.remember` | yes | Remember one fact |
| `memory.recall` | no | Look up what px0 remembers |

Two rules hold across all of them. Anything that can change the machine is a write tool, so it is declared in `tools:` and shown as a write at build time. And nothing reads or writes outside an allowed root. The sandboxing is covered in [part 12](12-trust.md).

`memory.recall` is worth a note on why it exists at all. A run already gets the memories relevant to its own instructions inlined. This tool is for the case where what it needs to look up depends on what it found: a name in a pull request, a project mentioned in an email.

## User-declared tools

One TOML file per tool under `<store>/tools/`, read at run time, so a new file is usable immediately with no restart.

```toml
id = "local.deploy"
description = "Run the deploy script and report what it printed"
command = ["./scripts/deploy.sh", "{environment}"]
params = { environment = "str*" }
is_write = true
env = ["DEPLOY_TOKEN"]
timeout = 300
cwd = "/Users/me/work/service"
```

`command` is argv, not a shell line. `_substitute` fills `{placeholder}` tokens one argv element at a time and never re-splits, so a value containing a space stays one argument and a value containing a semicolon is still just text. There is no shell to interpret it.

`env` narrows rather than adds. A tool that declares nothing inherits the whole environment, which is what every existing tool relies on. A tool that names its variables gets `PATH`, `HOME`, `LANG`, `TZ`, and those names only -- so a credential meant for one command is not handed to every other command a workflow can reach. A declared variable that is not set is refused before the command runs, because the alternative is a tool failing halfway with whatever error the far end gives an unauthenticated request.

`load_user_tools` never raises. It returns `(tools_by_id, errors)`, so one malformed file cannot hide the others or take down `px0 tools list`. Validation rejects an id that is not `group.name`, an id that shadows a built-in, a non-list command, and a duplicate.

## Discovered tools

Covered in [part 5](05-building.md) from the build side. From the run side, the important property is that a discovered tool executes through the same `_composio_execute` path the curated Composio tools use:

```python
def _discovered_spec(tool) -> ToolSpec:
    def handler(args, ctx, _tool=tool):
        return _composio_execute(ctx, _tool.toolkit, _tool.slug, args)
    return ToolSpec(id=tool.id, provider=tool.toolkit, ...)
```

One execution path means authorization on demand, retries, and dry-run stubbing all behave the same for both.

## Executing through Composio

`_composio_execute(ctx, app, tool_slug, arguments)` does four things in order: find the connected account, check it is `ACTIVE`, execute, and unwrap the result.

### Finding the account

Accounts are keyed by Composio's toolkit slug. A curated tool passes px0's own name (`calendar`); a discovered tool passes the slug (`googlecalendar`). Both have to find the same account, so `connect.account_key` resolves the alias first:

```python
TOOLKIT_ALIASES = {"calendar": "googlecalendar"}
```

That map used to be a whitelist, which capped authorization at four apps while the builder happily discovered tools from the other thousand-odd toolkits and then failed to authorize them. Now anything matching `^[a-z0-9][a-z0-9_]*$` is accepted as a slug, and only the aliases are listed.

Stores written before slug-keying fall back to the alias key, and `connect.migrate_account_keys` rewrites them during the v2-to-v3 schema migration.

### Authorization on demand

There is no `px0 connect` step to run first. A tool whose app is not authorized prepares that app's authorization itself and raises `ConnectorNotConfigured` carrying the URL to consent at:

```python
def _needs_connection(home, app: str, reason: str) -> ConnectorNotConfigured:
    res = connect_mod.connect_composio_app(home, app)
    return ConnectorNotConfigured(
        f"{app} {reason}. Authorize it by opening:\n  {res['redirect_url']}")
```

Minting a link is idempotent -- the underlying auth config is created once and cached -- and grants nothing until someone consents in the browser. If preparing the link itself fails, the error says why rather than dangling a dead URL.

Account status is checked before every execution, and each state gets its own message. `INITIATED` means the browser consent was started and never finished. A 404 means the connection no longer exists on Composio. Anything else that is not `ACTIVE` is reported as the status it actually is.

### The one API key

`connectors.composio_api_key` in `config.toml`, with `COMPOSIO_API_KEY` in the environment and `.state/credentials.toml` as fallbacks. `credentials.load` also reads the config key back into the credentials dict, so both lookup paths agree.

`credentials.toml` is mode 0600, re-asserted on every save, and `px0 doctor` checks it.

## TLS interception

This is the part of `connect.py` that exists entirely because of corporate networks, and it is worth reading if you build anything that talks HTTPS from a laptop.

Behind an intercepting proxy such as Zscaler, the certificate a client sees is signed by a corporate root that `certifi` -- the bundle both `httpx` and `requests` verify against by default -- deliberately does not ship. The Composio SDK collapses every transport failure into the string `"Connection error."`, so the real reason appears only in the exception chain.

`_is_cert_error` walks the `__cause__` and `__context__` chain looking for `SSLCertVerificationError` or `CERTIFICATE_VERIFY_FAILED`.

`find_ca_bundle` then tries known system bundle locations and, for each, actually opens a TLS connection to `backend.composio.dev` to see whether it verifies. Existence is not enough; the question is whether that bundle trusts the interceptor.

```python
CA_BUNDLE_CANDIDATES = (
    "/opt/homebrew/etc/ca-certificates/cert.pem",
    "/usr/local/etc/ca-certificates/cert.pem",
    "/etc/ssl/cert.pem",
    "/etc/ssl/certs/ca-certificates.crt",
    "/etc/pki/tls/certs/ca-bundle.crt",
)
```

A bundle that works is persisted to `connectors.ca_bundle` and exported into the environment. `apply_ca_bundle` sets both `SSL_CERT_FILE` and `REQUESTS_CA_BUNDLE`, because the two clients px0 uses read different names.

`with_cert_recovery(home, call)` wraps a call, heals a certificate failure once, and retries. `catalogue._get` does the same thing for its plain `requests` calls. First contact from behind a new proxy should find a bundle that trusts it and remember it, rather than telling the user to go hunt one down.

`cli._ctx` calls `apply_ca_bundle` on every command, not just Composio ones. Before that, `brain add <url>` failed on an intercepting network while Composio worked, because only the Composio paths applied it.

`describe_api_error` is the reporting half. When the cause is TLS interception it says so and names the fix outright, because that is the case a user cannot guess from the surface text. Otherwise it unwraps the SDK's `Error code: N - {...whole payload...}` string down to the message and suggested fix, so a permissions problem reads as one line instead of a wall of JSON.

## Errors

| Exception | Meaning |
| --------- | ------- |
| `ConnectorError` | A tool call failed against the external system |
| `ConnectorNotConfigured` | The connection this tool needs is not set up; carries the auth URL |
| `LocalToolError` | A local tool refused to run, or failed while running |
| `CatalogueError` | Composio's catalogue could not be searched or read |

`ConnectorNotConfigured` subclasses `ConnectorError`, and `runner._with_retry` catches that distinction explicitly:

```python
except tools.ConnectorNotConfigured:
    raise
except tools.ConnectorError as e:
    ...retry with backoff
```

Retrying an unconfigured connector is three attempts at a certainty.

`LocalToolError` is wrapped into `ConnectorError` at the `ToolSpec` boundary, so the run loop has one exception class to handle regardless of where a tool ran.

## Next

[Part 9](09-brain.md) covers the other thing a run reads from: the brain.
