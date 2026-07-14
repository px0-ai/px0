"""px0 MCP server.

Generates MCP tools from the px0 OpenAPI specification using FastMCP's
OpenAPI integration. Each documented endpoint (operationId) becomes an MCP
tool that proxies HTTP requests to a running px0 API server.

Configuration (environment variables):
    PX0_BASE_URL   Base URL of the px0 API (default: http://localhost:8000)
    PX0_API_KEY    Bearer token or API key sent as `Authorization: Bearer <key>`
    PX0_SPEC_PATH  Path to the OpenAPI spec (default: ../docs/openapi/openapi-bundled.yaml)

Run:
    python server.py
"""

import os
from pathlib import Path

import httpx
import yaml
from fastmcp import FastMCP

DEFAULT_SPEC = Path(__file__).resolve().parent.parent / "docs" / "openapi" / "openapi-bundled.yaml"


def load_spec() -> dict:
    spec_path = Path(os.environ.get("PX0_SPEC_PATH", DEFAULT_SPEC))
    with spec_path.open() as f:
        return yaml.safe_load(f)


def build_client() -> httpx.AsyncClient:
    base_url = os.environ.get("PX0_BASE_URL", "http://localhost:8000")
    headers = {}
    api_key = os.environ.get("PX0_API_KEY")
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    return httpx.AsyncClient(base_url=base_url, headers=headers, timeout=30.0)


def build_server() -> FastMCP:
    return FastMCP.from_openapi(
        openapi_spec=load_spec(),
        client=build_client(),
        name="px0",
    )


mcp = build_server()

if __name__ == "__main__":
    mcp.run()
