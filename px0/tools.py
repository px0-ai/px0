"""The normalized tool namespace. Every input `tool:` and every workflow
`tools:` entry names something from here. The native GitHub adapter calls
the GitHub REST API directly with a stored PAT. Composio-backed tools
(calendar, gmail, slack) are listed in the namespace with the read/write
shape the spec describes, but this build does not implement a live
Composio client -- calling one raises ConnectorNotConfigured rather than
guessing at an unverified API shape."""

import re
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Callable

import requests

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


def _github_token(ctx: Context) -> str:
    """Loads the stored GitHub PAT, raising ConnectorNotConfigured if github is not connected."""
    creds = creds_mod.load(ctx.home)
    gh = creds.get("github")
    if not gh or not gh.get("token"):
        raise ConnectorNotConfigured(
            "github is not connected; run `px0 connect github --native --pat`"
        )
    return gh["token"]


def _github_headers(ctx: Context) -> dict:
    """Builds the standard bearer-auth headers used for every GitHub REST call."""
    return {
        "Authorization": f"Bearer {_github_token(ctx)}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }


def _github_request(ctx: Context, method: str, path: str, **kwargs) -> requests.Response:
    """Issues one authenticated GitHub API request and raises ConnectorError on network
    failure, a rejected token (401), or any other 4xx/5xx response."""
    headers = kwargs.pop("headers", {})
    headers = {**_github_headers(ctx), **headers}
    try:
        resp = requests.request(method, f"{GITHUB_API}{path}", headers=headers, timeout=15, **kwargs)
    except requests.RequestException as e:
        raise ConnectorError(f"github request failed: {e}") from e
    if resp.status_code == 401:
        raise ConnectorError("github token rejected (401); run `px0 connect rotate github`")
    if resp.status_code >= 400:
        raise ConnectorError(f"github {method} {path} -> {resp.status_code}: {resp.text[:200]}")
    return resp


_PR_URL_RE = re.compile(r"github\.com/(?P<owner>[^/]+)/(?P<repo>[^/]+)/pull/(?P<number>\d+)")


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


def _composio_execute(ctx: Context, app: str, tool_slug: str, arguments: dict) -> Any:
    """Executes a Composio tool using the stored API key and connected account ID."""
    creds = creds_mod.load(ctx.home)
    composio = creds.get("composio")
    if not composio or not composio.get("api_key"):
        raise ConnectorNotConfigured(
            "Composio API key is not configured; run `px0 connect setup-composio <key>`"
        )

    connected_accounts = composio.get("connected_accounts", {})
    if app not in connected_accounts:
        raise ConnectorNotConfigured(
            f"{app} is not connected; run `px0 connect {app}`"
        )

    connected_account_id = connected_accounts[app]
    api_key = composio["api_key"]
    headers = {
        "x-api-key": api_key,
        "Content-Type": "application/json"
    }

    try:
        status_resp = requests.get(
            f"https://backend.composio.dev/api/v3.1/connected_accounts/{connected_account_id}",
            headers=headers,
            timeout=15,
        )
        if status_resp.status_code == 404:
            raise ConnectorNotConfigured(f"{app} connection not found on Composio; run `px0 connect {app}`")
        if status_resp.status_code >= 400:
            raise ConnectorError(f"Composio status API error -> {status_resp.status_code}: {status_resp.text[:200]}")

        status = status_resp.json().get("status", "UNKNOWN")
        if status == "INITIATED":
            raise ConnectorNotConfigured(f"{app} connection is INITIATED, not ACTIVE -- finish the browser consent")
        if status != "ACTIVE":
            raise ConnectorNotConfigured(f"{app} connection is {status}, not ACTIVE -- run `px0 connect {app}` and complete OAuth")
    except requests.RequestException as e:
        raise ConnectorError(f"Composio API unreachable: {e}") from e

    payload = {
        "arguments": arguments,
        "connected_account_id": connected_account_id
    }
    try:
        resp = requests.post(
            f"https://backend.composio.dev/api/v3/tools/execute/{tool_slug}",
            headers=headers,
            json=payload,
            timeout=15,
        )
    except requests.RequestException as e:
        raise ConnectorError(f"Composio API unreachable: {e}") from e

    if resp.status_code >= 400:
        raise ConnectorError(f"Composio execution failed -> {resp.status_code}: {resp.text[:200]}")

    return resp.json()


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
    return _composio_execute(ctx, "calendar", "GOOGLECALENDAR_EVENTS_LIST", arguments)


def gmail_search_messages(args: dict, ctx: Context) -> Any:
    """Search gmail messages."""
    query = args.get("query", "")
    arguments = {
        "query": query,
    }
    return _composio_execute(ctx, "gmail", "GMAIL_FETCH_EMAILS", arguments)


def gmail_get_message(args: dict, ctx: Context) -> Any:
    """Fetch one gmail message."""
    msg_id = args.get("id", "")
    arguments = {
        "id": msg_id,
        "message_id": msg_id,
    }
    return _composio_execute(ctx, "gmail", "GMAIL_GET_EMAIL", arguments)


def gmail_send_message(args: dict, ctx: Context) -> Any:
    """Send a gmail message."""
    arguments = {
        "recipient_email": args.get("to", ""),
        "subject": args.get("subject", ""),
        "body": args.get("body", ""),
    }
    return _composio_execute(ctx, "gmail", "GMAIL_SEND_EMAIL", arguments)


def slack_post_message(args: dict, ctx: Context) -> Any:
    """Post a message to a slack channel."""
    arguments = {
        "channel": args.get("channel", ""),
        "message": args.get("text", ""),
    }
    return _composio_execute(ctx, "slack", "SLACK_SEND_MESSAGE", arguments)


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


def list_tools(service: str | None = None) -> list[ToolSpec]:
    """Returns all registered tools, or only those for one provider, sorted by id."""
    tools = list(REGISTRY.values())
    if service:
        tools = [t for t in tools if t.provider == service]
    return sorted(tools, key=lambda t: t.id)


def exists(tool_id: str) -> bool:
    """Whether tool_id is a known entry in the registry."""
    return tool_id in REGISTRY


def is_write(tool_id: str) -> bool:
    """Whether the given tool mutates external state (used to gate what a workflow may call)."""
    return REGISTRY[tool_id].is_write


def call(home, config: dict, tool_id: str, args: dict) -> Any:
    """Dispatches to a tool's handler by id. Raises ConnectorError for an unknown tool id;
    the handler itself may raise ConnectorError/ConnectorNotConfigured."""
    if tool_id not in REGISTRY:
        raise ConnectorError(f"no such tool: {tool_id}")
    ctx = Context(home=home, config=config)
    return REGISTRY[tool_id].handler(args, ctx)
