"""Composio's tool catalogue: searching it, and remembering what was found.

px0 ships a small set of curated tools (`tools.REGISTRY`), but Composio's
catalogue is thousands of tools across hundreds of toolkits. `px0 new`
searches it so a workflow can use the tool that actually fits the task
instead of the nearest curated approximation.

A discovered tool is *cached in the store* rather than looked up again at run
time. Two reasons: a workflow must keep working offline and unchanged after it
is written, and read-vs-write has to be knowable without a network call --
`px0 run --dry-run` decides what to stub from it.

Read/write comes from Composio's own MCP-style hints in each tool's `tags`:
`readOnlyHint` means it only reads; its absence means it can change something;
`destructiveHint` means it can delete or overwrite. Verified against the live
catalogue on 2026-08-20 (GMAIL_FETCH_EMAILS carries readOnlyHint,
GMAIL_SEND_EMAIL does not, GMAIL_DELETE_MESSAGE carries destructiveHint).
"""

import json
from dataclasses import dataclass, field, asdict
from pathlib import Path

from px0 import paths

# `composio:` prefixes every discovered tool id so it never collides with a
# curated `provider.action` id and is obvious in a workflow file.
ID_PREFIX = "composio:"

# Composio's tool search filters by substring and returns matches in alphabetical
# order, not by relevance -- so a narrow limit silently truncates before reaching
# the right tool ("list pull requests" needs ~10 results to reach GITHUB_LIST_*).
# The limit is generous and the *model* does the ranking.
SEARCH_LIMIT = 20


class CatalogueError(Exception):
    """Raised when Composio's catalogue can't be searched or a slug can't be read."""


@dataclass
class CatalogueTool:
    """One tool from Composio's catalogue, in px0's own terms."""
    slug: str
    toolkit: str
    name: str
    description: str
    is_write: bool
    is_destructive: bool = False
    params: dict[str, str] = field(default_factory=dict)

    @property
    def id(self) -> str:
        """The id a workflow file uses for this tool."""
        return f"{ID_PREFIX}{self.slug}"


def is_catalogue_id(tool_id: str) -> bool:
    """Whether `tool_id` names a discovered Composio tool rather than a curated one."""
    return tool_id.startswith(ID_PREFIX)


def slug_of(tool_id: str) -> str:
    """The Composio slug behind a `composio:` tool id."""
    return tool_id[len(ID_PREFIX):]


def _params_of(schema: dict) -> dict[str, str]:
    """Flattens a tool's input_parameters JSON Schema into {name: type}.

    Required fields come first so a generated `args` block leads with what the
    tool actually needs.
    """
    props = (schema or {}).get("properties") or {}
    required = set((schema or {}).get("required") or [])
    ordered = sorted(props, key=lambda n: (n not in required, n))
    out = {}
    for name in ordered:
        spec = props[name] or {}
        kind = spec.get("type", "any")
        if isinstance(kind, list):
            kind = "|".join(str(k) for k in kind)
        out[name] = f"{kind}*" if name in required else str(kind)
    return out


def _from_api(item: dict) -> CatalogueTool:
    """Builds a CatalogueTool from one Composio tools-API item."""
    tags = item.get("tags") or []
    return CatalogueTool(
        slug=item["slug"],
        toolkit=(item.get("toolkit") or {}).get("slug", ""),
        name=item.get("name") or item["slug"],
        description=(item.get("description") or "").strip(),
        # no readOnlyHint means it may change something -- assume write, which is
        # the safe direction: px0 gates writes behind explicit consent.
        is_write="readOnlyHint" not in tags,
        is_destructive="destructiveHint" in tags,
        params=_params_of(item.get("input_parameters")),
    )


def search(home: Path, query: str, limit: int = SEARCH_LIMIT,
           toolkit: str | None = None) -> list[CatalogueTool]:
    """Searches Composio's catalogue, newest-relevance first.

    Raises CatalogueError rather than returning nothing when the search itself
    failed -- "no tool matches" and "we could not ask" must not look alike.
    """
    params: dict = {"search": query, "limit": limit}
    if toolkit:
        params["toolkit_slug"] = toolkit  # the API's filter is singular

    try:
        data = _get(home, "/api/v3/tools", params)
    except Exception as e:
        raise CatalogueError(f"could not search Composio's catalogue: {e}") from e

    items = data.get("items") or []
    return [_from_api(i) for i in items if i.get("slug") and not i.get("is_deprecated")]


def fetch(home: Path, slug: str) -> CatalogueTool:
    """Reads one tool by slug, for confirming it exists and getting its schema."""
    try:
        item = _get(home, f"/api/v3/tools/{slug}", {})
    except Exception as e:
        raise CatalogueError(f"no Composio tool with slug {slug!r}: {e}") from e
    if not item.get("slug"):
        raise CatalogueError(f"no Composio tool with slug {slug!r}")
    return _from_api(item)


def _get(home: Path, path: str, params: dict) -> dict:
    """One authenticated GET against Composio's REST API.

    Goes through requests rather than the SDK: the SDK models connected
    accounts and executions, not catalogue browsing.
    """
    import os
    import requests
    from px0 import config as config_mod, connect as connect_mod, credentials as creds_mod, paths

    cfg = config_mod.load(paths.config_path(home))
    api_key = config_mod.get(cfg, "connectors.composio_api_key") or os.environ.get("COMPOSIO_API_KEY")
    if not api_key:
        creds = creds_mod.load(home)
        api_key = (creds.get("composio") or {}).get("api_key")
    if not api_key:
        raise CatalogueError(
            "Composio API key is not configured; run `px0 config composio <key>`"
        )
    # requests verifies against certifi unless told otherwise, so pass the bundle
    # explicitly rather than relying on the environment alone.
    bundle = connect_mod.apply_ca_bundle(home)
    url = f"https://{connect_mod.COMPOSIO_HOST}{path}"

    def fetch(verify):
        return requests.get(url, headers={"x-api-key": api_key}, params=params,
                            timeout=15, verify=verify)

    try:
        resp = fetch(bundle or True)
    except requests.exceptions.SSLError:
        # First contact from behind a TLS-intercepting proxy: find a bundle that
        # trusts it, remember it for next time, and retry once.
        bundle = connect_mod.recover_ca_bundle(home)
        if not bundle:
            raise
        resp = fetch(bundle)

    if resp.status_code >= 400:
        raise CatalogueError(f"{resp.status_code} {resp.text[:200]}")
    return resp.json()


# --- store-side cache ------------------------------------------------------

def cache_path(home: Path) -> Path:
    """Where discovered tool metadata lives."""
    return paths.state_dir(home) / "catalogue.json"


def load_cached(home: Path) -> dict[str, CatalogueTool]:
    """Every previously discovered tool, keyed by its px0 tool id.

    Never raises: a corrupt or missing cache means "nothing discovered yet",
    which degrades a workflow into an unknown-tool error rather than a crash.
    """
    path = cache_path(home)
    if not path.exists():
        return {}
    try:
        raw = json.loads(path.read_text())
    except (OSError, ValueError):
        return {}
    out = {}
    for entry in raw.get("tools", []):
        try:
            tool = CatalogueTool(**entry)
        except TypeError:
            continue  # written by a newer px0 with extra fields; skip it
        out[tool.id] = tool
    return out


def remember(home: Path, discovered: list[CatalogueTool]) -> None:
    """Adds tools to the cache, replacing any earlier entry for the same slug."""
    if not discovered:
        return
    merged = load_cached(home)
    for tool in discovered:
        merged[tool.id] = tool
    path = cache_path(home)
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = {"tools": [asdict(t) for t in sorted(merged.values(), key=lambda t: t.slug)]}
    path.write_text(json.dumps(payload, indent=2))
