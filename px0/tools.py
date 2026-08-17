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
    id: str
    provider: str
    description: str
    params: dict[str, str]
    is_write: bool
    handler: Callable[[dict, "Context"], Any] = field(repr=False)


@dataclass
class Context:
    home: Any
    config: dict


def _github_token(ctx: Context) -> str:
    creds = creds_mod.load(ctx.home)
    gh = creds.get("github")
    if not gh or not gh.get("token"):
        raise ConnectorNotConfigured(
            "github is not connected; run `px0 connect github --native --pat`"
        )
    return gh["token"]


def _github_headers(ctx: Context) -> dict:
    return {
        "Authorization": f"Bearer {_github_token(ctx)}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }


def _github_request(ctx: Context, method: str, path: str, **kwargs) -> requests.Response:
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
    m = _PR_URL_RE.search(url)
    if not m:
        raise ConnectorError(f"not a github pull request url: {url}")
    return m.group("owner"), m.group("repo"), m.group("number")


def _since_to_date(since: str) -> str:
    if since.startswith("-") and since.endswith("d"):
        days = int(since[1:-1])
        return (datetime.now(timezone.utc) - timedelta(days=days)).strftime("%Y-%m-%d")
    return since


def github_list_my_prs(args: dict, ctx: Context) -> list[dict]:
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
    owner, repo, number = _parse_pr_url(args["url"])
    pr = _github_request(ctx, "GET", f"/repos/{owner}/{repo}/pulls/{number}").json()
    return {
        "title": pr["title"], "body": pr.get("body") or "", "url": pr["html_url"],
        "state": pr["state"], "author": pr["user"]["login"],
        "base": pr["base"]["ref"], "head": pr["head"]["ref"],
    }


def github_get_pr_diff(args: dict, ctx: Context) -> str:
    owner, repo, number = _parse_pr_url(args["url"])
    resp = _github_request(
        ctx, "GET", f"/repos/{owner}/{repo}/pulls/{number}",
        headers={"Accept": "application/vnd.github.v3.diff"},
    )
    return resp.text


def github_list_review_comments(args: dict, ctx: Context) -> list[dict]:
    owner, repo, number = _parse_pr_url(args["url"])
    resp = _github_request(ctx, "GET", f"/repos/{owner}/{repo}/pulls/{number}/comments")
    return [
        {"path": c["path"], "line": c.get("line"), "body": c["body"], "author": c["user"]["login"]}
        for c in resp.json()
    ]


def github_create_review_comment(args: dict, ctx: Context) -> dict:
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


def _composio_unconfigured(args: dict, ctx: Context) -> Any:
    raise ConnectorNotConfigured(
        "this build wires GitHub natively only; Composio-backed tools "
        "(calendar, gmail, slack) are listed for shape but not executed. "
        "Connect the native GitHub PAT path, or wire a Composio client "
        "against verified API docs before relying on this tool."
    )


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
        {"window": "str"}, False, _composio_unconfigured),
    "gmail.search_messages": ToolSpec(
        "gmail.search_messages", "gmail", "Search gmail messages",
        {"query": "str"}, False, _composio_unconfigured),
    "gmail.get_message": ToolSpec(
        "gmail.get_message", "gmail", "Fetch one gmail message",
        {"id": "str"}, False, _composio_unconfigured),
    "gmail.send_message": ToolSpec(
        "gmail.send_message", "gmail", "Send a gmail message",
        {"to": "str", "subject": "str", "body": "str"}, True, _composio_unconfigured),
    "slack.post_message": ToolSpec(
        "slack.post_message", "slack", "Post a message to a slack channel",
        {"channel": "str", "text": "str"}, True, _composio_unconfigured),
}


def list_tools(service: str | None = None) -> list[ToolSpec]:
    tools = list(REGISTRY.values())
    if service:
        tools = [t for t in tools if t.provider == service]
    return sorted(tools, key=lambda t: t.id)


def exists(tool_id: str) -> bool:
    return tool_id in REGISTRY


def is_write(tool_id: str) -> bool:
    return REGISTRY[tool_id].is_write


def call(home, config: dict, tool_id: str, args: dict) -> Any:
    if tool_id not in REGISTRY:
        raise ConnectorError(f"no such tool: {tool_id}")
    ctx = Context(home=home, config=config)
    return REGISTRY[tool_id].handler(args, ctx)
