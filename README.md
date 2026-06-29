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

### Upcoming Features

- Native multi-language client SDKs: Provide lightweight, idiomatic packages for Python, Node.js, and Go to easily execute prompt rendering.
- Smart in-process caching: Cache active templates in memory with automatic warming, background async refresh, and push-based invalidation.
- Traffic-split A/B testing: Route traffic between prompt versions using custom percentage weights.
- Template-level observability: Collect deep metrics and tracing for individual template execution and cache hit rates.

## How it works

Call `px0.render()` with a template name and variables. px0 resolves the active version, renders the template, and returns the prompt string. Cache, A/B routing, and OTEL spans are handled automatically with no code changes required when a prompt is updated.

## Quickstart

You can choose to run the entire application stack inside Docker or run only the dependencies in Docker and run the Go server locally.

### Option A: Run everything in Docker (Easiest)

This builds and starts the app along with PostgreSQL, Redis, and the full OpenTelemetry monitoring stack (Prometheus and Grafana).

```bash
# 1. Copy the environment variables template
cp .env.example .env

# 2. Build and start all services in the background
make docker-up
```

- **App Server**: `http://localhost:8000`
- **Grafana (Dashboards)**: `http://localhost:3000` (preconfigured anonymous Admin access)
- **Prometheus**: `http://localhost:9090`

To tear down all running services, run:
```bash
make docker-down
```

---

### Option B: Run only dependencies in Docker, run App on host (Best for fast development)

This starts postgres, redis, and monitoring tools in Docker, but allows you to run the Go server locally for a faster write-compile feedback loop.

```bash
# 1. Install local dependencies and Go dev tools
make install

# 2. Copy the environment variables template
cp .env.example .env

# 3. Start local dependencies in the background
docker compose up -d postgres redis otel-collector prometheus grafana

# 4. Edit your `.env` file to use the host-mapped ports:
# DATABASE_URL=postgres://px0:px0secret@localhost:5432/px0?sslmode=disable
# REDIS_URL=redis://localhost:6379

# 5. Start the Go dev server on your host (port 8000)
make dev
```

## Database and migrations

px0 uses PostgreSQL. The connection is configured via `DATABASE_URL` in `.env`.

Depending on your setup, configure your connection string as follows:

- **When running on the host (Option B)**, connect to the mapped Postgres port `5432`:
  ```
  DATABASE_URL=postgres://px0:px0secret@localhost:5432/px0?sslmode=disable
  ```
- **When running entirely inside Docker (Option A)**, the service resolves internally at:
  ```
  DATABASE_URL=postgres://px0:px0secret@postgres:5432/px0?sslmode=disable
  ```

Migrations are embedded SQL files (`internal/db/migrations/`) and run automatically every time the server starts. They are tracked in a `schema_migrations` table so each migration is applied exactly once. There is no separate migrate command — starting the server is sufficient.

## Development

This section guides you through developing and testing `px0` locally on your machine.

### 1. Prerequisites
Ensure you have the following installed locally:
- **Go** (v1.21+)
- **Docker & Docker Compose**
- **golangci-lint** (optional, installed automatically via `make install`)

### 2. Environment Variables (`.env`)
Copy `.env.example` to `.env` and configure it:
```bash
cp .env.example .env
```
For local development where dependencies run in Docker, configure your local variables to point to the host-mapped ports:
- `DATABASE_URL=postgres://px0:px0secret@localhost:5432/px0?sslmode=disable`
- `REDIS_URL=redis://localhost:6379`

### 3. Running the App Locally
Start the database, cache, and monitoring tools in the background:
```bash
docker compose up -d postgres redis otel-collector prometheus grafana
```
Run the application locally on your host machine (starts the server on port `8000`):
```bash
make dev
```

### 4. Running Tests
The project features unit and integration tests. Integration tests run against a separate test database and will skip automatically if Postgres is unreachable.

#### Step 4a: Create the Test Database
Create a dedicated `px0_test` database inside your running Docker Postgres container:
```bash
docker exec -it $(docker ps -f "name=postgres" --format "{{.Names}}") psql -U px0 -c "CREATE DATABASE px0_test;"
```

#### Step 4b: Execute Tests
To run all tests (unit + integration), configure the `TEST_DATABASE_URL` to match your host-mapped Postgres port (`5432`) and run `make test`:
```bash
TEST_DATABASE_URL="postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable" make test
```

You can also run target-specific tests:
```bash
# Run only database store layer integration tests
TEST_DATABASE_URL="postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable" make test-store

# Run only HTTP handler integration tests
TEST_DATABASE_URL="postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable" make test-handler

# Run tests and generate HTML coverage report
TEST_DATABASE_URL="postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable" make test-coverage
```

### 5. Code Quality & Formatting
Run the following make targets before committing your code:
```bash
make format    # Formats all Go files using gofmt
make lint      # Runs static analysis via golangci-lint
make vet       # Runs go vet
make check     # Runs lint + vet + tests sequentially
```

### 6. Local Monitoring & Dashboards
When running locally, OpenTelemetry metrics are automatically emitted to the `otel-collector` (on port `4317`). You can monitor metrics such as latency, HTTP requests, and system behavior via:
- **Grafana**: `http://localhost:3000` (Pre-configured dashboard included)
- **Prometheus**: `http://localhost:9090`

---

## Production

### Docker Setup

```bash
make docker-up    # Build and start all services (including app) in the background
make docker-down  # Stop all services
```

### Manual Setup

```bash
make build    # Compile to bin/server
make run      # Run the built binary via go run ./cmd/server
```
