# px0

Prompts are code. Treat them like it.

px0 is open-source prompt infrastructure for AI applications. It replaces hardcoded prompt strings with versioned templates, in-process caching, A/B testing, and OpenTelemetry observability - so teams can iterate on prompts without touching application code.

## Benefits

Using px0 brings significant operational and governance benefits to your AI systems:

- No prompt hardcoding: Decouple prompt strings from code. Manage and edit them in one place.
- No deployment overhead: Publish new versions instantly. No CI/CD runs or redeployments needed.
- [WIP] Dynamic A/B testing: Split production traffic between templates. Find the best version using live metrics.
- Prompt sharing: Share templates across teams. Reduce duplication and align message standards.
- RBAC governance: Protect production prompts. Only authorized admins can promote changes.

## Features

### Implemented Features

- Version-controlled prompt templates: Store and manage prompts in the database instead of hardcoding strings in application code.
- Draft, review, and publish workflow: Support creating, promoting, demoting, archiving, and visually diffing versioned prompts.
- Atomic promotions and instant rollouts: Go live instantly without recompiling services or running deployment pipelines.
- Template execution: Render templates containing variables, conditionals, and loops using Go's template engine.
- OpenTelemetry observability: HTTP request counters, server error rates, and response latency to any compliant backend.
- Role-based access control (RBAC): Secure prompt actions using Viewer, Editor, Team Admin, and Org Admin roles.

## How it works

Call `px0.render()` with a template name and variables. px0 resolves the active version, renders the template, and returns the prompt string. Cache, A/B routing, and OTEL spans are handled automatically with no code changes required when a prompt is updated.

## Getting Started

To set up the full stack locally with Docker Compose, send test requests, and explore metrics in Grafana and Prometheus, refer to our detailed [Getting Started and Telemetry Guide](get-started.md).

## Development and Testing

For instructions on configuring your local development environment, running unit and integration tests, and executing linting/vetting checks, refer to our [Development and Testing Guide](development.md).

## Benchmarking and Performance

For instructions on evaluating prompt rendering latency and throughput under concurrent load using our built-in load testing utility, refer to our [Benchmarking and Performance Guide](benchmarking.md).

## Production

Start all orchestrated production services in the background:

```bash
make docker-up    # Build and start all services (including app) in the background
make docker-down  # Stop all services
```
