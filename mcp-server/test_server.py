"""Tests for the px0 MCP server.

Tool-generation tests run purely from the OpenAPI spec (no network).
The end-to-end test calls the real px0 API and skips if it is unreachable.
"""

import httpx
import pytest
from fastmcp import Client

import server as server_module
from server import PassthroughAuth, build_client, build_server

PX0_URL = "http://localhost:8000"


@pytest.fixture(scope="module")
def server():
    return build_server()


@pytest.mark.asyncio
async def test_tools_generated_from_spec(server):
    tools = await server.list_tools()
    names = {t.name for t in tools}
    assert len(tools) > 0
    # Spot-check known operationIds from docs/openapi.
    for expected in ("healthCheck", "createPrompt", "listAPIKeys", "createVersion"):
        assert expected in names, f"missing tool {expected}"


@pytest.mark.asyncio
async def test_tools_have_descriptions(server):
    tools = await server.list_tools()
    missing = [t.name for t in tools if not (t.description or "").strip()]
    assert not missing, f"tools without descriptions: {missing}"


def apply_auth(auth: PassthroughAuth) -> httpx.Request:
    """Run the auth flow over a fresh request and return the modified request."""
    request = httpx.Request("GET", "http://px0.test/v1/prompts")
    return next(auth.auth_flow(request))


def test_auth_forwards_client_authorization_header(monkeypatch):
    monkeypatch.setattr(
        server_module,
        "get_http_headers",
        lambda include=None: {"authorization": "Bearer client-key"},
    )
    monkeypatch.setenv("PX0_API_KEY", "fallback-key")

    request = apply_auth(PassthroughAuth())
    # Client credential wins over the env fallback.
    assert request.headers["Authorization"] == "Bearer client-key"


def test_auth_falls_back_to_env_api_key(monkeypatch):
    monkeypatch.setattr(server_module, "get_http_headers", lambda include=None: {})
    monkeypatch.setenv("PX0_API_KEY", "fallback-key")

    request = apply_auth(PassthroughAuth())
    assert request.headers["Authorization"] == "Bearer fallback-key"


def test_auth_no_header_and_no_env_sends_no_authorization(monkeypatch):
    monkeypatch.setattr(server_module, "get_http_headers", lambda include=None: {})
    monkeypatch.delenv("PX0_API_KEY", raising=False)

    request = apply_auth(PassthroughAuth())
    assert "Authorization" not in request.headers


def test_build_client_uses_passthrough_auth(monkeypatch):
    monkeypatch.setenv("PX0_BASE_URL", "http://px0.test")
    client = build_client()
    assert isinstance(client.auth, PassthroughAuth)
    assert str(client.base_url) == "http://px0.test"


@pytest.mark.asyncio
async def test_health_check_end_to_end(server):
    try:
        httpx.get(f"{PX0_URL}/v1/health", timeout=2.0)
    except httpx.HTTPError:
        pytest.skip("px0 API is not running")

    async with Client(server) as client:
        result = await client.call_tool("healthCheck")
        assert '"status":"OK"' in result.content[0].text.replace(" ", "")
