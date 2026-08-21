"""The normalized tool namespace. Every input `tool:` and every workflow
`tools:` entry names something from here. Every tool executes through the
Composio SDK against a connected account -- the GitHub tools proxy the
GitHub REST API through Composio rather than holding their own PAT.
A tool whose app is not authorized yet prepares that app's authorization
itself and raises ConnectorNotConfigured carrying the URL to consent at --
there is no separate connect step to run first."""

import re
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Callable

from px0 import credentials as creds_mod

GITHUB_API = "https://api.github.com"


class ConnectorError(Exception):
    """A tool call failed against the external system."""


class ConnectorNotConfigured(ConnectorError):
    """The connection this tool needs is not set up."""


@dataclass
class ToolSpec:
    """Registry entry describing one callable tool: its id, provider, read/write shape, and handler."""
    id: str
    provider: str
    description: str
    params: dict[str, str]
    is_write: bool
    handler: Callable[[dict, "Context"], Any] = field(repr=False)


@dataclass
class Context:
    """Execution context passed to every tool handler: the store home and loaded config."""
    home: Any
    config: dict


_PR_URL_RE = re.compile(r"github\.com/(?P<owner>[^/]+)/(?P<repo>[^/]+)/pull/(?P<number>\d+)")


def _github_request(ctx: Context, method: str, path: str, **kwargs) -> Any:
    """Issues one authenticated GitHub API request via Composio's proxy."""
    composio = _composio_credentials(ctx.home)

    connected_accounts = composio.get("connected_accounts", {})
    if "github" not in connected_accounts:
        raise _needs_connection(ctx.home, "github", "is not connected yet")

    connected_account_id = connected_accounts["github"]
    
    from px0 import connect as connect_mod
    client = connect_mod.composio_client(ctx.home, composio["api_key"])

    endpoint = f"https://api.github.com{path}"
    
    parameters = []
    headers = {
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28"
    }
    headers.update(kwargs.pop("headers", {}))
    
    for k, v in headers.items():
        parameters.append({"name": k, "value": str(v), "type": "header"})
        
    if "params" in kwargs:
        for k, v in kwargs.pop("params").items():
            parameters.append({"name": k, "value": str(v), "type": "query"})
            
    body = kwargs.get("json", None)

    class FakeResponse:
        def __init__(self, proxy_resp):
            self.status_code = proxy_resp.status
            self._data = proxy_resp.data
            import json
            if isinstance(self._data, (dict, list)):
                self.text = json.dumps(self._data)
            else:
                self.text = str(self._data)
                
        def json(self):
            if isinstance(self._data, (dict, list)):
                return self._data
            import json
            return json.loads(self._data)

    try:
        resp = connect_mod.with_cert_recovery(ctx.home, lambda: client.tools.proxy(
            endpoint=endpoint,
            method=method.upper(), # type: ignore
            body=body,
            connected_account_id=connected_account_id,
            parameters=parameters # type: ignore
        ))
    except Exception as e:
        raise ConnectorError(
            f"github proxy request failed: {connect_mod.describe_api_error(e)}") from e

    fake_resp = FakeResponse(resp)
    if fake_resp.status_code == 401:
        raise ConnectorError("github rejected the request (401); its authorization "
                             "may have been revoked -- the next run will offer a fresh link")
    if fake_resp.status_code >= 400:
        raise ConnectorError(f"github {method} {path} -> {fake_resp.status_code}: {fake_resp.text[:200]}")
    return fake_resp


def _parse_pr_url(url: str) -> tuple[str, str, str]:
    """Extracts (owner, repo, pr number) from a github.com PR URL; raises ConnectorError if it doesn't match."""
    m = _PR_URL_RE.search(url)
    if not m:
        raise ConnectorError(f"not a github pull request url: {url}")
    return m.group("owner"), m.group("repo"), m.group("number")


def _since_to_date(since: str) -> str:
    """Converts a relative window like "-7d" into an ISO date; passes through anything else unchanged."""
    if since.startswith("-") and since.endswith("d"):
        days = int(since[1:-1])
        return (datetime.now(timezone.utc) - timedelta(days=days)).strftime("%Y-%m-%d")
    return since


def github_list_my_prs(args: dict, ctx: Context) -> list[dict]:
    """Lists PRs authored by the connected user, updated since args["since"]
    (default -7d), optionally scoped to args["repos"]. Read-only."""
    me = _github_request(ctx, "GET", "/user").json()["login"]
    since = _since_to_date(args.get("since", "-7d"))
    repos = args.get("repos") or []
    repo_q = " ".join(f"repo:{r}" for r in repos) if repos else ""
    query = f"is:pr author:{me} updated:>={since} {repo_q}".strip()
    resp = _github_request(ctx, "GET", "/search/issues", params={"q": query, "per_page": 30})
    items = resp.json().get("items", [])
    return [
        {"title": i["title"], "url": i["html_url"], "state": i["state"],
         "updated_at": i["updated_at"]}
        for i in items
    ]


def github_get_pr(args: dict, ctx: Context) -> dict:
    """Fetches one pull request's metadata by URL. Read-only."""
    owner, repo, number = _parse_pr_url(args["url"])
    pr = _github_request(ctx, "GET", f"/repos/{owner}/{repo}/pulls/{number}").json()
    return {
        "title": pr["title"], "body": pr.get("body") or "", "url": pr["html_url"],
        "state": pr["state"], "author": pr["user"]["login"],
        "base": pr["base"]["ref"], "head": pr["head"]["ref"],
    }


def github_get_pr_diff(args: dict, ctx: Context) -> str:
    """Fetches the unified diff text of a pull request by URL. Read-only."""
    owner, repo, number = _parse_pr_url(args["url"])
    resp = _github_request(
        ctx, "GET", f"/repos/{owner}/{repo}/pulls/{number}",
        headers={"Accept": "application/vnd.github.v3.diff"},
    )
    return resp.text


def github_list_review_comments(args: dict, ctx: Context) -> list[dict]:
    """Lists existing review comments on a pull request by URL. Read-only."""
    owner, repo, number = _parse_pr_url(args["url"])
    resp = _github_request(ctx, "GET", f"/repos/{owner}/{repo}/pulls/{number}/comments")
    return [
        {"path": c["path"], "line": c.get("line"), "body": c["body"], "author": c["user"]["login"]}
        for c in resp.json()
    ]


def github_create_review_comment(args: dict, ctx: Context) -> dict:
    """Posts a single-line review comment on a pull request. Write tool: mutates the PR
    on GitHub. Resolves the PR's head sha itself so the caller only needs the URL."""
    owner, repo, number = _parse_pr_url(args["url"])
    pr = _github_request(ctx, "GET", f"/repos/{owner}/{repo}/pulls/{number}").json()
    payload = {
        "body": args["body"],
        "commit_id": pr["head"]["sha"],
        "path": args["path"],
        "line": args["line"],
        "side": args.get("side", "RIGHT"),
    }
    resp = _github_request(ctx, "POST", f"/repos/{owner}/{repo}/pulls/{number}/comments", json=payload)
    return {"id": resp.json()["id"], "url": resp.json()["html_url"]}


# Composio tool slugs, each resolved against the live catalogue
# (GET /api/v3/tools/{slug} returning 200) rather than inferred from the tool
# name -- Composio's naming is not predictable from pattern. Verified 2026-08-20.
# Argument keys below come from each tool's own `input_parameters` schema.
_TOOL_SLUGS: dict[str, str] = {
    "calendar.list_events": "GOOGLECALENDAR_EVENTS_LIST",
    "gmail.search_messages": "GMAIL_FETCH_EMAILS",
    # not GMAIL_GET_EMAIL, which does not exist in the catalogue (404)
    "gmail.get_message": "GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID",
    "gmail.send_message": "GMAIL_SEND_EMAIL",
    "slack.post_message": "SLACK_SEND_MESSAGE",
}


def _needs_connection(home, app: str, reason: str) -> ConnectorNotConfigured:
    """Builds the error raised when `app` isn't usable yet, with a live auth link.

    There is no `px0 connect` to send the user to: a tool that needs an app
    triggers that app's authorization itself, so the only thing missing is the
    human opening the URL. Minting a link is idempotent -- the underlying auth
    config is created once and cached -- and grants nothing until someone
    consents in the browser.
    """
    from px0 import connect as connect_mod

    try:
        res = connect_mod.connect_composio_app(home, app)
    except Exception as e:
        # Couldn't even prepare the link: say why rather than dangling a dead URL.
        return ConnectorNotConfigured(
            f"{app} {reason}, and preparing its authorization failed: {e}"
        )
    return ConnectorNotConfigured(
        f"{app} {reason}. Authorize it by opening:\n  {res['redirect_url']}"
    )


def _composio_credentials(home):
    """The stored Composio credentials, or a ConnectorNotConfigured explaining how
    to set the API key up."""
    import os
    from px0 import config as config_mod, paths
    cfg = config_mod.load(paths.config_path(home))
    api_key = config_mod.get(cfg, "connectors.composio_api_key") or os.environ.get("COMPOSIO_API_KEY")
    creds = creds_mod.load(home)
    composio = creds.get("composio", {})
    if not api_key:
        api_key = composio.get("api_key")
    if not api_key:
        raise ConnectorNotConfigured(
            "Composio API key is not configured; run `px0 config composio <key>`"
        )
    composio["api_key"] = api_key
    return composio


def _composio_execute(ctx: Context, app: str, tool_slug: str, arguments: dict) -> Any:
    """Executes a Composio tool, authorizing the app on demand if it isn't yet."""
    composio = _composio_credentials(ctx.home)
    from px0 import connect as connect_mod

    connected_accounts = composio.get("connected_accounts", {})
    # Accounts are keyed by Composio's toolkit slug. A curated tool passes px0's
    # own name ("calendar"), a discovered tool passes the slug itself
    # ("googlecalendar"); both have to find the same account.
    key = connect_mod.account_key(app)
    if key not in connected_accounts:
        if app in connected_accounts:
            key = app  # store written before slug-keying
        else:
            raise _needs_connection(ctx.home, app, "is not connected yet")

    connected_account_id = connected_accounts[key]
    api_key = composio["api_key"]
    client = connect_mod.composio_client(ctx.home, api_key)

    try:
        account = connect_mod.with_cert_recovery(
            ctx.home, lambda: client.connected_accounts.get(connected_account_id))
        if account.status == "INITIATED":
            raise ConnectorNotConfigured(
                f"{app} authorization was started but never completed -- open the URL "
                "px0 printed for it and finish the browser consent"
            )
        if account.status != "ACTIVE":
            raise _needs_connection(ctx.home, app, f"connection is {account.status}, not ACTIVE")
    except ConnectorNotConfigured:
        raise
    except Exception as e:
        if "404" in str(e) or "not found" in str(e).lower():
            raise _needs_connection(ctx.home, app, "connection no longer exists on Composio")
        raise ConnectorError(
            f"Composio API error: {connect_mod.describe_api_error(e)}") from e

    try:
        result = connect_mod.with_cert_recovery(ctx.home, lambda: client.tools.execute(
            slug=tool_slug,
            connected_account_id=connected_account_id,
            user_id=connect_mod.COMPOSIO_USER_ID,
            arguments=arguments,
            dangerously_skip_version_check=True
        ))
    except Exception as e:
        raise ConnectorError(
            f"Composio execution failed: {connect_mod.describe_api_error(e)}") from e

    successful = result.get("successful", True) if isinstance(result, dict) else result.successful
    if not successful:
        error = result.get("error", "Unknown error") if isinstance(result, dict) else result.error
        raise ConnectorError(f"Composio execution failed -> {error}")

    data = result.get("data", result) if isinstance(result, dict) else result.data
    return data


def calendar_list_events(args: dict, ctx: Context) -> Any:
    """Lists calendar events in a window."""
    window = args.get("window", "")
    now = datetime.now(timezone.utc)
    if window == "yesterday":
        start_of_yesterday = (now - timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0)
        end_of_yesterday = start_of_yesterday + timedelta(days=1) - timedelta(microseconds=1)
        timeMin = start_of_yesterday.isoformat()
        timeMax = end_of_yesterday.isoformat()
    elif window.startswith("-") and window.endswith("d"):
        days = int(window[1:-1])
        start_of_window = (now - timedelta(days=days)).replace(hour=0, minute=0, second=0, microsecond=0)
        timeMin = start_of_window.isoformat()
        timeMax = now.isoformat()
    else:
        timeMin = (now - timedelta(days=1)).isoformat()
        timeMax = now.isoformat()

    arguments = {
        "calendarId": "primary",
        "timeMin": timeMin,
        "timeMax": timeMax,
        "singleEvents": True,
    }
    return _composio_execute(ctx, "calendar", _TOOL_SLUGS["calendar.list_events"], arguments)


def gmail_search_messages(args: dict, ctx: Context) -> Any:
    """Search gmail messages."""
    query = args.get("query", "")
    arguments = {
        "query": query,
    }
    return _composio_execute(ctx, "gmail", _TOOL_SLUGS["gmail.search_messages"], arguments)


def gmail_get_message(args: dict, ctx: Context) -> Any:
    """Fetch one gmail message."""
    arguments = {
        "message_id": args.get("id", ""),  # the schema's only required field
    }
    return _composio_execute(ctx, "gmail", _TOOL_SLUGS["gmail.get_message"], arguments)


def gmail_send_message(args: dict, ctx: Context) -> Any:
    """Send a gmail message."""
    arguments = {
        "recipient_email": args.get("to", ""),
        "subject": args.get("subject", ""),
        "body": args.get("body", ""),
    }
    return _composio_execute(ctx, "gmail", _TOOL_SLUGS["gmail.send_message"], arguments)


def slack_post_message(args: dict, ctx: Context) -> Any:
    """Post a message to a slack channel."""
    arguments = {
        "channel": args.get("channel", ""),
        "text": args.get("text", ""),  # SLACK_SEND_MESSAGE has no `message` field
    }
    return _composio_execute(ctx, "slack", _TOOL_SLUGS["slack.post_message"], arguments)


REGISTRY: dict[str, ToolSpec] = {
    "github.list_my_prs": ToolSpec(
        "github.list_my_prs", "github", "PRs authored by the connected user",
        {"repos": "list[str]", "since": "str"}, False, github_list_my_prs),
    "github.get_pr": ToolSpec(
        "github.get_pr", "github", "Fetch one pull request by URL",
        {"url": "str"}, False, github_get_pr),
    "github.get_pr_diff": ToolSpec(
        "github.get_pr_diff", "github", "Fetch the unified diff of a pull request",
        {"url": "str"}, False, github_get_pr_diff),
    "github.list_review_comments": ToolSpec(
        "github.list_review_comments", "github", "List existing review comments on a PR",
        {"url": "str"}, False, github_list_review_comments),
    "github.create_review_comment": ToolSpec(
        "github.create_review_comment", "github", "Post a review comment on a PR",
        {"url": "str", "body": "str", "path": "str", "line": "int"}, True,
        github_create_review_comment),
    "calendar.list_events": ToolSpec(
        "calendar.list_events", "calendar", "List calendar events in a window",
        {"window": "str"}, False, calendar_list_events),
    "gmail.search_messages": ToolSpec(
        "gmail.search_messages", "gmail", "Search gmail messages",
        {"query": "str"}, False, gmail_search_messages),
    "gmail.get_message": ToolSpec(
        "gmail.get_message", "gmail", "Fetch one gmail message",
        {"id": "str"}, False, gmail_get_message),
    "gmail.send_message": ToolSpec(
        "gmail.send_message", "gmail", "Send a gmail message",
        {"to": "str", "subject": "str", "body": "str"}, True, gmail_send_message),
    "slack.post_message": ToolSpec(
        "slack.post_message", "slack", "Post a message to a slack channel",
        {"channel": "str", "text": "str"}, True, slack_post_message),
}


def _discovered_spec(tool) -> ToolSpec:
    """Wraps a catalogue tool as a ToolSpec with a generic Composio handler.

    Every discovered tool executes through the same path the curated Composio
    tools use, so authorization-on-demand, retries, and dry-run stubbing all
    behave identically whether a tool was hand-written or found by `px0 workflows new`.
    """
    def handler(args: dict, ctx: Context, _tool=tool) -> Any:
        return _composio_execute(ctx, _tool.toolkit, _tool.slug, args)

    return ToolSpec(
        id=tool.id,
        provider=tool.toolkit,
        description=tool.description[:200],
        params=tool.params,
        is_write=tool.is_write,
        handler=handler,
    )


def _local_spec(tool_id: str) -> ToolSpec:
    """Wraps one built-in local tool as a ToolSpec."""
    from px0 import localtools

    provider, description, params, is_write, handler = localtools.BUILTINS[tool_id]

    def wrapped(args: dict, ctx: Context, _h=handler) -> Any:
        try:
            return _h(args, ctx)
        except localtools.LocalToolError as e:
            raise ConnectorError(str(e)) from e

    return ToolSpec(id=tool_id, provider=provider, description=description,
                    params=params, is_write=is_write, handler=wrapped)


def _user_spec(tool) -> ToolSpec:
    """Wraps one user-declared tool as a ToolSpec."""
    from px0 import localtools

    def handler(args: dict, ctx: Context, _t=tool) -> Any:
        try:
            return localtools.run_user_tool(_t, args, ctx)
        except localtools.LocalToolError as e:
            raise ConnectorError(str(e)) from e

    return ToolSpec(id=tool.id, provider=tool.id.split(".", 1)[0], description=tool.description,
                    params=tool.params, is_write=tool.is_write, handler=handler)


def local_specs() -> dict[str, ToolSpec]:
    """Every built-in local tool, keyed by id."""
    from px0 import localtools

    return {tid: _local_spec(tid) for tid in localtools.BUILTINS}


def user_specs(home) -> dict[str, ToolSpec]:
    """Every user-declared tool in the store, keyed by id. Malformed files are skipped."""
    if home is None:
        return {}
    from px0 import localtools

    found, _errors = localtools.load_user_tools(home)
    return {tid: _user_spec(t) for tid, t in found.items()}


def resolve(tool_id: str, home=None) -> ToolSpec | None:
    """The ToolSpec for a tool id -- curated, local, user-declared, or discovered --
    or None if unknown.

    `home` is needed to see the last two: their definitions live in the store,
    not in this module.
    """
    if tool_id in REGISTRY:
        return REGISTRY[tool_id]
    from px0 import localtools

    if tool_id in localtools.BUILTINS:
        return _local_spec(tool_id)
    if home is None:
        return None
    from px0 import catalogue

    if catalogue.is_catalogue_id(tool_id):
        tool = catalogue.load_cached(home).get(tool_id)
        return _discovered_spec(tool) if tool else None
    return user_specs(home).get(tool_id)


def list_tools(service: str | None = None, home=None) -> list[ToolSpec]:
    """Every usable tool -- curated and local, plus the user-declared and
    discovered ones when `home` is given -- optionally narrowed to one provider,
    sorted by id."""
    specs = list(REGISTRY.values()) + list(local_specs().values())
    if home is not None:
        from px0 import catalogue

        specs += [_discovered_spec(t) for t in catalogue.load_cached(home).values()]
        specs += list(user_specs(home).values())
    if service:
        specs = [t for t in specs if t.provider == service]
    return sorted(specs, key=lambda t: t.id)


def exists(tool_id: str, home=None) -> bool:
    """Whether tool_id names a usable tool."""
    return resolve(tool_id, home) is not None


def is_write(tool_id: str, home=None) -> bool:
    """Whether the given tool mutates external state (used to gate what a workflow may call).

    Raises KeyError for an unknown id, matching the previous registry-only
    behaviour -- callers check `exists` first.
    """
    spec = resolve(tool_id, home)
    if spec is None:
        raise KeyError(tool_id)
    return spec.is_write


def call(home, config: dict, tool_id: str, args: dict) -> Any:
    """Dispatches to a tool's handler by id. Raises ConnectorError for an unknown tool id;
    the handler itself may raise ConnectorError/ConnectorNotConfigured."""
    spec = resolve(tool_id, home)
    if spec is None:
        raise ConnectorError(f"no such tool: {tool_id}")
    ctx = Context(home=home, config=config)
    return spec.handler(args, ctx)
