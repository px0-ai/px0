# Phase 1: Composio connections and tools, for real

## Status quo this phase changes

px0 (`/home/arpit/workspace/px0/px0`) is a working Python CLI that already implements most of `spec.md`, but Composio -- the spec's default connector provider -- is explicitly stubbed:

- `px0/connect.py:1-8` (module docstring): "actually creating a Composio-hosted auth link is not implemented in this build."
- `px0/tools.py:1-7` (module docstring): "this build does not implement a live Composio client -- calling one raises `ConnectorNotConfigured` rather than guessing at an unverified API shape."
- `px0/tools.py:166-174`: every Composio-shaped tool (`calendar.list_events`, `gmail.search_messages`, `gmail.get_message`, `gmail.send_message`, `slack.post_message`) dispatches to `_composio_unconfigured`, which always raises.
- `px0/cli.py:283-286`: `px0 connect <app>` (without `--native`) prints "Composio auth-link creation is not implemented in this build" and exits `EXIT_USER_ERROR`.
- `px0/cli.py:118-121`: the builder's Connect phase (`px0 new`) only prints which connections are missing; it never attempts to create them.

Only native GitHub (`px0/connect.py:23-46`) and its five tools (`px0/tools.py:102-163`) are real.

## Assumptions (stated explicitly, low-stakes)

1. **`user_id`.** Composio's auth model is multi-tenant (`connected_accounts.link` requires a `user_id`). px0 has no multi-tenant concept -- one store, one person. Every Composio call in px0 uses the constant `user_id = "px0-local"`.
2. **Managed auth, not bring-your-own-app.** Auth configs are created with Composio's managed OAuth (`type: "use_composio_managed_auth"`), matching the spec's "one API key, any app in the catalog" framing (spec.md:348-358) and avoiding a per-toolkit OAuth app registration step that spec.md never asks the user to do.
3. **Scope: the three toolkits px0 already declares.** `px0/tools.py`'s `REGISTRY` already names three Composio-backed providers -- `calendar`, `gmail`, `slack` -- with five tools between them. This phase wires exactly those three toolkits (Composio slugs `googlecalendar`, `gmail`, `slack` -- confirmed live at `docs.composio.dev/toolkits/{slug}`) and does not add new providers or tools. GitHub stays native-only, unchanged.
4. **Auth configs are created lazily and cached.** `px0 connect gmail` creates (or reuses) exactly one auth config for `gmail`, not all three toolkits up front. The created `auth_config_id` is cached in `.state/credentials.toml` under `[composio.auth_configs]` so a second `px0 connect gmail` (e.g. after `remove`) doesn't create a duplicate.

## One item this phase cannot fully pin without a live API key (state explicitly, not a guess)

Two of Composio's five tool slugs are confirmed directly from Composio's own docs pages:

- `calendar.list_events` -> `GOOGLECALENDAR_EVENTS_LIST`
- `gmail.send_message` -> `GMAIL_SEND_EMAIL`

The other three (`gmail.search_messages`, `gmail.get_message`, `slack.post_message`) are not directly confirmed in the documentation surfaced during this planning pass, and Composio's naming isn't fully predictable from pattern alone (e.g. Gmail's fetch-by-id action name is not obviously `GMAIL_GET_MESSAGE`). Guessing these would violate "do not guess APIs." Instead: **the first implementation step below is a live lookup**, not a design decision -- there is nothing to decide, only a command to run and a JSON field to copy.

## Engineering section

### Dependencies on prior phases

None. This is the phase that establishes the pytest test harness (`tests/conftest.py`, `pyproject.toml`'s `dev` extra) that Phases 2-6 build on; it has no dependency of its own.

### What already exists (reused, not rebuilt)

- `px0/tools.py`'s `ToolSpec`/`Context`/`REGISTRY`/`call()` dispatch (`px0/tools.py:29-45, 177-236`) -- this phase only replaces five handler function bodies and adds one small HTTP helper; the registry shape, `ConnectorError`/`ConnectorNotConfigured` exceptions, and `runner.py`'s generic `tools.call(home, config, tool_id, args)` dispatch (`px0/runner.py:119, 214`) need no changes.
- `px0/credentials.py`'s `load`/`save`/`set_service`/`remove_service` (full file, 43 lines) -- reused as-is for storing the Composio API key and cached auth-config ids.
- `px0/connect.py`'s existing native-GitHub functions and `list_connections`/`remove_connection` (`px0/connect.py:48-69`) -- unchanged; Composio functions are added alongside them.
- `px0/doctor.py`'s `_check_connections` (`px0/doctor.py:82-85`) -- extended, not replaced.
- `px0/cli.py`'s `cmd_connect` dispatch structure (`px0/cli.py:226-286`) -- the `else` branch at 283-286 is replaced; `setup-composio`/`list`/`remove`/`rotate` branches are untouched.

### Components touched

| File | Change |
| --- | --- |
| `pyproject.toml` | Add `composio` to `dependencies`. Add a new `[project.optional-dependencies]` table: `dev = ["pytest"]` -- this phase also establishes the test harness (see Test plan). |
| `px0/connect.py` | Add `_composio_client(home)`, `_ensure_auth_config(home, toolkit)`, `connect_composio_app(home, app)`, `connected_account_status(home, app)`. |
| `px0/tools.py` | Replace `_composio_unconfigured` handlers for the 5 Composio tools with real ones; add `_composio_execute(ctx, tool_slug, arguments)`. |
| `px0/credentials.py` | No signature changes; `[composio]` table in `credentials.toml` grows two new keys (`auth_configs`, see Data model). |
| `px0/cli.py` | `cmd_connect` (`226-286`): real Composio branch. `cmd_new` (`89-141`): after printing missing connections (`115-121`), call `connect_mod.connect_composio_app` for each missing Composio-backed service and print the returned auth link (native GitHub stays manual, per spec.md:517 "existing connections are reused, never re-authed" -- but a *missing* native GitHub connection still requires a manual PAT, since Composio doesn't hold a git PAT). |
| `px0/doctor.py` | `_check_connections` (`82-85`): for each stored Composio connected account, call `connected_account_status` and report `ok=False` if any is not `ACTIVE`. |
| `tests/conftest.py` (new) | Pytest fixtures: temp `PX0_HOME`, initialized store, a `FakeComposio` HTTP transport (see Test plan). |
| `tests/test_connect_composio.py` (new) | Unit tests for connect.py's new functions. |
| `tests/test_tools_composio.py` (new) | Unit tests for the 5 tool handlers against `FakeComposio`. |

No new classes beyond one small internal client wrapper (`_composio_client`, a thin `requests.Session` factory, not a new public class) -- this stays within the "no more than 2 new services/classes" sizing guideline.

### API contract (Composio, wire-level, verified against `docs.composio.dev`)

All calls: header `x-api-key: <stored composio api_key>`, `Content-Type: application/json`, base `https://backend.composio.dev`.

**1. Create (or reuse) an auth config for a toolkit** -- `POST /api/v3.1/auth_configs`

```json
// request
{
  "toolkit": { "slug": "gmail" },
  "auth_config": { "type": "use_composio_managed_auth" }
}
// response (201)
{
  "toolkit": { "slug": "gmail" },
  "auth_config": { "id": "ac_...", "auth_scheme": "OAUTH2", "is_composio_managed": true }
}
```

**2. Create an auth link session** -- `POST /api/v3/connected_accounts/link`

```json
// request
{ "auth_config_id": "ac_...", "user_id": "px0-local" }
// response (201)
{ "link_token": "...", "redirect_url": "https://...", "expires_at": "2026-...", "connected_account_id": "ca_..." }
```

`redirect_url` is what `px0 connect <app>` prints for the user to open in a browser.

**3. Poll connected-account status** -- `GET /api/v3.1/connected_accounts/{connected_account_id}`

Response includes `status`, one of `INITIATED` | `ACTIVE` | `FAILED` (per `docs.composio.dev/reference/api-reference/connected-accounts`).

**4. Execute a tool** -- `POST /api/v3/tools/execute/{TOOL_SLUG}`

```json
// request
{ "arguments": { /* tool-specific */ }, "connected_account_id": "ca_..." }
```

Response body shape is tool-specific; treat the top-level JSON as the tool's return value (mirrors how `github_*` handlers already return parsed JSON fields, `px0/tools.py:112-116`).

**5. Resolve exact tool slugs (implementation step 0, run once, live)**

```shell
curl -s "https://backend.composio.dev/api/v3/tools?toolkit_slugs=gmail&search=search" \
  -H "x-api-key: $COMPOSIO_API_KEY" | jq '.items[] | {slug, name}'
curl -s "https://backend.composio.dev/api/v3/tools?toolkit_slugs=gmail&search=fetch" \
  -H "x-api-key: $COMPOSIO_API_KEY" | jq '.items[] | {slug, name}'
curl -s "https://backend.composio.dev/api/v3/tools?toolkit_slugs=slack&search=message" \
  -H "x-api-key: $COMPOSIO_API_KEY" | jq '.items[] | {slug, name}'
```

Record the three returned slugs (for `gmail.search_messages`, `gmail.get_message`, `slack.post_message`) as literal constants alongside the two already confirmed (`GOOGLECALENDAR_EVENTS_LIST`, `GMAIL_SEND_EMAIL`) in a `_TOOL_SLUGS: dict[str, str]` map at the top of `px0/tools.py`'s Composio section. This is a lookup, not a design choice -- there is exactly one correct answer per Composio's live registry.

### Data model

`.state/credentials.toml`, `[composio]` table gains two keys (not versioned, per spec.md's versioning scope -- credentials never are):

```toml
[composio]
api_key = "cmp_..."

[composio.auth_configs]
gmail = "ac_abc123"
slack = "ac_def456"
googlecalendar = "ac_ghi789"

[composio.connected_accounts]
gmail = "ca_xyz111"
```

`auth_configs` and `connected_accounts` are both dicts keyed by toolkit slug, populated incrementally as the user connects each app.

### Key flows

**`px0 connect gmail` (happy path):**

1. `cmd_connect` (`px0/cli.py:271-286`, replaced) calls `connect_mod.connect_composio_app(home, "gmail")`.
2. `connect_composio_app` loads the stored `composio.api_key` (raises `ValueError` -> `EXIT_USER_ERROR` if absent, telling the user to run `setup-composio` first -- mirrors the existing PAT-missing message pattern at `px0/tools.py:52-54`).
3. Checks `[composio.auth_configs].gmail` in credentials; if absent, calls API 1 above and caches the returned `id`.
4. Calls API 2 above with that `auth_config_id`, caches `connected_account_id` under `[composio.connected_accounts].gmail`.
5. Prints `redirect_url` and instructions to open it in a browser and finish the OAuth consent.
6. Does **not** block waiting for the user to finish (`px0 connect` is a single synchronous CLI call, not a long poll) -- `px0 connect list` and `px0 doctor` are how status is checked afterward (flow below).

**Checking connection status later (`px0 connect list`, `px0 doctor`):**

1. For each cached `connected_accounts` entry, `connected_account_status(home, app)` calls API 3.
2. `px0 connect list` (`px0/cli.py:242-245`, extended) prints the live status alongside `service`/`kind`.
3. `px0 doctor`'s `_check_connections` (`px0/doctor.py:82-85`) sets `ok=False` and names the app if status is not `ACTIVE`, e.g. `"gmail connected_account is INITIATED, not ACTIVE -- finish the browser consent"`.

**A workflow calling `gmail.send_message` at run time:**

1. `runner.py` calls `tools.call(home, config, "gmail.send_message", args)` exactly as it does today for GitHub (`px0/runner.py:119, 214` -- no runner change needed).
2. The new handler loads `[composio.connected_accounts].gmail` from credentials; raises `ConnectorNotConfigured` (same exception class already used at `px0/tools.py:169`) if missing, with the message `"gmail is not connected; run `px0 connect gmail`"`.
3. Calls API 4 with `tool_slug=_TOOL_SLUGS["gmail.send_message"]` and `arguments` built from px0's `args` dict (`{"to": ..., "subject": ..., "body": ...}` -> whatever field names the live schema from step 0's discovery call actually uses -- record those field names next to the slug).
4. On a non-2xx response or network failure, raises `ConnectorError` (matching the GitHub handler's pattern at `px0/tools.py:74-80`), which `runner.py`'s existing retry wrapper (`_with_retry`, `px0/runner.py:214-215`) already retries per `[connectors] retries` -- no new retry logic needed.

**Builder auto-connect (`px0 new`):**

1. After `cmd_new` (`px0/cli.py:115-121`) computes `missing = needed - existing`, for each `service in missing` that is one of `{gmail, slack, calendar}`, call `connect_mod.connect_composio_app(home, service)` and print the returned `redirect_url` inline, replacing the current static message at line 121.
2. `github` stays manual (native PAT has no auth-link flow to automate); the printed instruction for a missing `github` connection is unchanged.
3. This mirrors spec.md:517 exactly: "For each required service without a connection, the builder runs the same flow as `px0 connect`."

### Non-functional requirements

- Every Composio HTTP call uses `timeout=15` seconds, matching the existing GitHub pattern (`px0/tools.py:73`).
- No new retry logic: reuse `runner.py`'s existing `_with_retry` for tool calls made during a run; `px0 connect` itself (not run-scoped) does not retry -- a failed connect attempt is a plain CLI error, matching how `connect_github_native` behaves today (`px0/connect.py:23-32`, no retry).

### Failure modes

| Failure | Covered by test? | Error handling | Visible to caller? |
| --- | --- | --- | --- |
| No `composio.api_key` stored | Yes (`test_connect_composio.py`) | `ValueError` -> `EXIT_USER_ERROR`, message points at `setup-composio` | Yes, CLI stderr |
| Composio API returns 401 (bad key) | Yes | `ConnectorError` with the response body's first 200 chars, mirroring `px0/tools.py:79` | Yes |
| `connected_account_id` cached but account never finished OAuth (`status=INITIATED`) | Yes | Tool call raises `ConnectorNotConfigured` with an explicit "finish the browser consent" message rather than a raw 4xx from the execute call | Yes |
| Composio backend unreachable (network error) | Yes | `ConnectorError`, retried 3x by `runner.py`'s existing wrapper, then surfaces as a failed run per spec.md:362 | Yes, in the run record |
| Tool slug lookup (step 0) was done against a stale Composio catalog and a slug 404s at call time | No (would require a live account; documented as a known gap) | `ConnectorError` from the non-2xx response | Yes -- surfaces as a run failure, not silent |

### Test plan

This phase also establishes the test harness: `pytest`, `tests/` mirroring the `px0/` package, a `conftest.py` fixture for a temp `PX0_HOME` initialized via `store.init()`. Composio's HTTP calls are tested against a `FakeComposio` fixture (a `requests_mock`-style monkeypatch of `requests.request`/`requests.post`/`requests.get` inside `px0.connect`/`px0.tools`, returning the exact JSON shapes documented above) -- no live account is contacted in CI.

| Layer | What | Count |
| --- | --- | --- |
| Unit | `connect._ensure_auth_config` creates once, reuses on second call | +2 |
| Unit | `connect.connect_composio_app` happy path returns a redirect_url and caches ids | +1 |
| Unit | `connect.connect_composio_app` with no api_key raises `ValueError` | +1 |
| Unit | `connect.connected_account_status` maps `ACTIVE`/`INITIATED`/`FAILED` correctly | +1 |
| Unit | Each of the 5 Composio tool handlers: happy path (mocked 200) | +5 |
| Unit | Each of the 5 handlers: `ConnectorNotConfigured` when not connected | +5 |
| Unit | A tool handler on mocked 500 raises `ConnectorError` | +1 |
| Integration | `cli.cmd_connect(["connect", "gmail"])` end-to-end against `FakeComposio`, asserts printed redirect_url and credentials.toml contents | +1 |
| Integration | `cli.cmd_new` auto-connect path: a plan needing `slack.post_message` with no slack connection triggers `connect_composio_app` and prints the link | +1 |

### Rollout

No data migration (credentials aren't versioned; new keys are additive to an existing TOML table). Rollback: revert the commit -- nothing written to `.state/credentials.toml` by this phase is read by any other phase, so an old binary against a store that has `[composio.auth_configs]`/`[composio.connected_accounts]` populated simply ignores the new keys (`credentials.py:load` is a plain `tomllib.load`, tolerant of extra keys).

## Product section

**Phase goal:** a workflow can actually read a Slack channel, search Gmail, check a calendar, or post/send/comment through Composio -- not just through native GitHub.

**User story:** the user runs `px0 connect slack`, approves the OAuth consent Composio's hosted page shows them, and a workflow with `tools: [slack.post_message]` posts a real message the next time it runs.

**In scope:**
- `px0 connect setup-composio <key>` (already works, unchanged).
- `px0 connect gmail|slack|calendar` creates a real Composio auth config + auth link, prints the link.
- `px0 connect list` shows live connected-account status for Composio apps.
- `px0 doctor` flags a Composio connection stuck in `INITIATED`.
- All 5 Composio-backed tools (`calendar.list_events`, `gmail.search_messages`, `gmail.get_message`, `gmail.send_message`, `slack.post_message`) execute for real against a connected account.
- `px0 new`'s builder auto-triggers the connect flow for any missing Composio-backed service the plan needs.

**Out of scope (deferred, no later phase currently planned for these -- flag if wanted):**
- Composio toolkits beyond `gmail`/`slack`/`googlecalendar` (the registry doesn't declare others; adding one is a follow-up in the same shape as this phase).
- Waiting/polling inside `px0 connect` for the OAuth consent to complete -- status is checked separately via `connect list`/`doctor`, per spec.md's async connect flow.
- Bring-your-own OAuth-app auth configs (custom client id/secret) -- managed auth only.

**Acceptance criteria:**
1. `px0 connect setup-composio <key>` followed by `px0 connect gmail` prints a `https://` redirect URL and exits 0.
2. After the printed URL is opened and consent is completed out-of-band, `px0 connect list --json` shows `"status": "ACTIVE"` for `gmail`.
3. A workflow with `tools: [slack.post_message]`, run against an `ACTIVE` slack connection, results in a message actually posted to the named channel (verified via Slack's own UI/API in manual QA, since CI uses `FakeComposio`).
4. Calling any of the 5 tools with no connected account raises `ConnectorNotConfigured` and the run record shows `outcome: "failed"` with that message, not a stack trace.
5. `px0 doctor` exits `EXIT_INTEGRITY_ERROR` (4) when any stored Composio connection's live status is not `ACTIVE`.

## Definition of done

- [ ] AC1-5 above pass, exercised by the integration tests in the Test plan.
- [ ] `pytest` runs green in CI (new `tests/` directory, `pip install -e '.[dev]'`).
- [ ] `px0 doctor` and `px0 connect list` both reflect live Composio status, not just "configured."
- [ ] `px0 new` no longer prints the static "only native github executes in this build" message for a plan needing gmail/slack/calendar.
