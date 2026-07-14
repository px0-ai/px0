"""Tests for the px0 MCP server.

Tool-generation tests run purely from the OpenAPI spec (no network).
The end-to-end test calls the real px0 API and skips if it is unreachable.
"""

import httpx
import pytest
from fastmcp import Client

from server import build_server

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


@pytest.mark.asyncio
async def test_health_check_end_to_end(server):
    try:
        httpx.get(f"{PX0_URL}/v1/health", timeout=2.0)
    except httpx.HTTPError:
        pytest.skip("px0 API is not running")

    async with Client(server) as client:
        result = await client.call_tool("healthCheck")
        assert '"status":"OK"' in result.content[0].text.replace(" ", "")
