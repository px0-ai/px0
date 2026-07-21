"""px0 MCP server.

Generates MCP tools from the px0 OpenAPI specification using FastMCP's
OpenAPI integration. Each documented endpoint (operationId) becomes an MCP
tool that proxies HTTP requests to a running px0 API server.

Transport: streamable HTTP at http://<host>:<port>/mcp

Authentication:
    Each MCP client sends its own px0 credential as an `Authorization` header
    on the MCP HTTP connection (e.g. `Authorization: Bearer <api-key>`). The
    header is forwarded verbatim to the px0 API on every tool call, so one
    running server can serve many clients with different keys.

    PX0_API_KEY may be set as a server-wide fallback for clients that send
    no Authorization header.

Configuration (environment variables):
    PX0_BASE_URL   Base URL of the px0 API (default: http://localhost:8000)
    PX0_API_KEY    Fallback bearer credential (optional, see above)
    PX0_SPEC_PATH  Path to the OpenAPI spec (default: ../docs/openapi/openapi-bundled.yaml)
    PX0_MCP_HOST   Host to listen on (default: 127.0.0.1)
    PX0_MCP_PORT   Port to listen on (default: 8001)

Run:
    python server.py
"""

import os
from pathlib import Path

import httpx
import yaml
from fastmcp import FastMCP
from fastmcp.server.dependencies import get_http_headers

DEFAULT_SPEC = Path(__file__).resolve().parent.parent / "docs" / "openapi" / "openapi-bundled.yaml"


def load_spec() -> dict:
    spec_path = Path(os.environ.get("PX0_SPEC_PATH", DEFAULT_SPEC))
    with spec_path.open() as f:
        return yaml.safe_load(f)


class PassthroughAuth(httpx.Auth):
    """Forwards the MCP client's Authorization header to the px0 API.

    Falls back to PX0_API_KEY when the client did not send one.
    """

    def auth_flow(self, request):
        # Headers of the incoming MCP HTTP request ({} for non-HTTP transports).
        incoming = get_http_headers(include={"authorization"})
        auth = incoming.get("authorization")
        if not auth:
            api_key = os.environ.get("PX0_API_KEY")
            auth = f"Bearer {api_key}" if api_key else None
        if auth:
            request.headers["Authorization"] = auth
        yield request


def build_client() -> httpx.AsyncClient:
    base_url = os.environ.get("PX0_BASE_URL", "http://localhost:8000")
    return httpx.AsyncClient(base_url=base_url, auth=PassthroughAuth(), timeout=30.0)


def build_server() -> FastMCP:
    return FastMCP.from_openapi(
        openapi_spec=load_spec(),
        client=build_client(),
        name="px0",
    )


mcp = build_server()

if __name__ == "__main__":
    mcp.run(
        transport="http",
        host=os.environ.get("PX0_MCP_HOST", "127.0.0.1"),
        port=int(os.environ.get("PX0_MCP_PORT", "8001")),
    )
