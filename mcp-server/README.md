# px0 MCP Server

An MCP (Model Context Protocol) server that exposes the px0 API as MCP tools. Tools are generated automatically from the OpenAPI specification (`docs/openapi/openapi-bundled.yaml`) — every documented `operationId` becomes an MCP tool that proxies requests to a running px0 API server.

The server runs over **streamable HTTP** at `http://<host>:<port>/mcp`. Each MCP client can send its own px0 credential as an `Authorization` header on the MCP connection; the header is forwarded verbatim to the px0 API on every tool call, so a single running server can serve many clients with different keys.

## Prerequisites

- Python 3.11 (pinned in `.python-version`)
- [uv](https://docs.astral.sh/uv/) for dependency management
- A running px0 API server (see the [root README](../README.md)), default `http://localhost:8000`

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|---|---|---|
| `PX0_BASE_URL` | `http://localhost:8000` | Base URL of the px0 API |
| `PX0_API_KEY` | _(unset)_ | Optional server-wide fallback bearer credential, used only when the MCP client sends no `Authorization` header |
| `PX0_SPEC_PATH` | `../docs/openapi/openapi-bundled.yaml` | Path to the OpenAPI spec |
| `PX0_MCP_HOST` | `127.0.0.1` | Host the MCP server listens on |
| `PX0_MCP_PORT` | `8001` | Port the MCP server listens on |

## Start the server

```bash
cd mcp-server
uv sync                 # install dependencies (first time only)
uv run python server.py
```

The server is now listening at `http://127.0.0.1:8001/mcp`.

To bind a different host/port:

```bash
PX0_MCP_HOST=0.0.0.0 PX0_MCP_PORT=9001 uv run python server.py
```

## Test with MCP Inspector

[MCP Inspector](https://github.com/modelcontextprotocol/inspector) is an interactive UI for exercising MCP servers. It requires Node.js.

1. With the px0 API and the MCP server both running, launch the inspector:

   ```bash
   npx @modelcontextprotocol/inspector
   ```

   This opens the inspector UI in your browser (typically `http://localhost:6274`).

2. In the inspector, configure the connection:
   - **Transport Type**: `Streamable HTTP`
   - **URL**: `http://127.0.0.1:8001/mcp`

3. To authenticate as a specific px0 user, add a custom header under **Authentication**:
   - Header name: `Authorization`
   - Value: `Bearer <your-px0-api-key>`

   (If omitted, the server falls back to `PX0_API_KEY` when set.)

4. Click **Connect**, then open the **Tools** tab and click **List Tools**. You should see one tool per px0 API operation (e.g. `healthCheck`, `createPrompt`, `listAPIKeys`, `createVersion`).

5. Try a tool: select `healthCheck` and click **Run Tool**. A healthy setup returns `{"status": "OK"}`. Authenticated tools like `listAPIKeys` require the `Authorization` header from step 3.

## Run the tests

```bash
cd mcp-server
uv run pytest
```

Tool-generation and auth tests run offline from the spec; the end-to-end health check test skips automatically if the px0 API is not reachable.
