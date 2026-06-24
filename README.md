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

# 2. Start postgres
docker run -d \
  --name px0-postgres \
  -e POSTGRES_DB=px0 \
  -e POSTGRES_USER=px0 \
  -e POSTGRES_PASSWORD=px0secret \
  -p 5432:5432 \
  -v px0_postgres_data:/var/lib/postgresql/data \
  postgres:16-alpine

# 3. Copy env file and edit as needed
cp .env.example .env

# 4. Start dev server (migrations run automatically on startup)
make dev
```

Server is at `http://localhost:8000`.

## Database and migrations

px0 uses PostgreSQL. The connection is configured via `DATABASE_URL` in `.env`:

```
DATABASE_URL=postgres://px0:px0secret@localhost:5432/px0?sslmode=disable
```

Migrations are embedded SQL files (`internal/db/migrations/`) and run automatically every time the server starts. They are tracked in a `schema_migrations` table so each migration is applied exactly once. There is no separate migrate command — starting the server is sufficient.

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
