# px0

Prompts are code. Treat them like it.

px0 is open-source prompt infrastructure for AI applications. It replaces hardcoded prompt strings with versioned Jinja2 templates, in-process caching, A/B testing, and OpenTelemetry observability - so teams can iterate on prompts without touching application code.

## Features

- Versioned prompt templates with a Draft-Review-Publish workflow and immutable published versions
- Easy templating with variables, conditionals, loops, filters, and macros rendered in a sandbox
- In-process caching with TTL, push-based invalidation, and background refresh before expiry
- A/B testing with traffic splitting by percentage
- OpenTelemetry observability for latency, cache hit rates, and errors
- Sub-2ms p99 render latency

## How it works

Call `px0.render()` with a template name and variables. px0 resolves the active version, renders the template, and returns the prompt string. Cache, A/B routing, and OTEL spans are handled automatically with no code changes required when a prompt is updated.

## Quickstart

```bash
# 1. Install dependencies and dev tools (golangci-lint)
make install

# 2. Copy env file and edit as needed
cp .env.example .env

# 3. Start dev server
make dev
```

Server is at `http://localhost:8000`.

## API

| Method | Path        | Description |
| ------ | ----------- | ----------- |
| GET    | `/v1/health` | Hello World |

## Development

```bash
make lint      # golangci-lint run
make format    # gofmt
make vet       # go vet
make test      # go test ./...
make check     # lint + vet + test (run before pushing)
```

## Production

Docker:

```bash
make docker-up    # build and start in background
make docker-down  # stop
```

Manual:

```bash
make build    # compile to bin/server
make run      # go run ./cmd/server
```
