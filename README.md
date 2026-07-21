<p align="left">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://px0.ai/logo/px0-logo-dark.png">
    <img src="https://px0.ai/logo/px0-logo-light.png" alt="Project Logo" width="150">
  </picture>
</p>

px0 is an open-source prompt infrastructure toolkit that lets you version, update, and govern prompts in production, eliminating the need to hardcode prompts or redeploy your application for prompt changes.

## Benefits

- Decouple prompt strings from codebase definitions to manage templates in a single control plane.
- Promote new prompt versions instantly with zero CI/CD overhead or application redeployments.
- Share standardized prompt templates across teams to align engineering practices.
- Govern production environments by restricting version promotion to authorized administrators.

## Getting Started

Follow these steps to spin up the local services and send your first request. Refer to the [Getting Started Guide](get-started/get-started.md) for detailed verification and user registration instructions. For telemetry setup, metrics observation, and benchmarking details, refer to the [Telemetry and Benchmarking Guide](get-started/get-started-telemetry.md).

### 1. Start the Orchestration Services

Clone the repository, configure the environment, and boot the background containers in detached mode.

```bash
cp .env.example .env
docker compose up -d
```

This starts the Go API server, PostgreSQL, Redis, and the OpenTelemetry observability stack.

### 2. Verify Server Health

Send a health check request to confirm the API server is active and listening on port 8000.

```bash
curl -i http://localhost:8000/v1/health
```

### 3. Access Web Interfaces

Open the pre-configured observability dashboards in your browser.

- Grafana Dashboards: http://localhost:3000
- Prometheus Expression Browser: http://localhost:9090

## Performance Benchmarks

px0 includes a built-in concurrent load testing utility located at `cmd/loadtest/main.go`. This script automatically handles transaction-safe database setup, concurrent execution, and lock-free metric collection.

Refer to the [Benchmarking and Performance Guide](get-started/get-started-benchmarking.md) for execution flags, metric details, and latency percentiles.

## MCP Server

px0 ships an MCP (Model Context Protocol) server that exposes every documented API operation as an MCP tool, so AI assistants like Claude can manage prompts directly. Refer to the [MCP Server Guide](mcp-server/README.md) for startup instructions, per-client authentication, and testing with MCP Inspector.

## Examples and SDKs

You can find examples in the [px0 examples repository](https://github.com/px0-ai/examples). Get started with our SDK-based hello worlds:

- [Python SDK Hello World](https://docs.px0.ai/sdk/python)
- [TypeScript SDK Hello World](https://docs.px0.ai/sdk/typescript)
- [Go SDK Hello World](https://docs.px0.ai/sdk/go)

## License

This project is licensed under the [MIT License](LICENSE).

