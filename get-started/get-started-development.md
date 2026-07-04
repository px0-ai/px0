# Development and Testing Guide

This guide describes how to configure your local machine, run tests, and maintain code quality when developing the px0 Go application.

## Prerequisites

Ensure you have the following installed locally:

- Go (v1.21+)
- Docker and Docker Compose
- golangci-lint (installed automatically via `make install`)

## Running Local Dependencies

If you want to run the Go application directly on your host machine for development instead of running it inside a container, follow these steps.

### 1. Copy Environment Variables

Copy the example environment file to create your local `.env` configuration:

```bash
cp .env.example .env
```

Ensure the environment variables are configured to connect to the host-mapped ports (which is the default in `.env.example`):

- `DATABASE_URL=postgres://px0:px0secret@localhost:5432/px0?sslmode=disable`
- `REDIS_URL=redis://localhost:6379`

### 2. Boot Dependencies and App

Start the background dependency containers, install local tools, and start the local development server:

```bash
docker compose up -d postgres redis otel-collector prometheus grafana
make install
make dev
```

The server starts listening on port 8000 by default.

## Database and Migrations

Migrations are embedded SQL files under `internal/db/migrations/` and run automatically every time the server starts. The schema is tracked in a `schema_migrations` table to apply each migration exactly once. There is no separate migration CLI tool required.

## Running Tests

Integration tests run against a separate test database and skip automatically if PostgreSQL is unreachable.

### 1. Create the Test Database

To create the dedicated test database inside your running PostgreSQL container, run:

```bash
docker exec -it $(docker ps -f "name=postgres" --format "{{.Names}}") psql -U px0 -c "CREATE DATABASE px0_test;"
```

### 2. Run Test Targets

Execute the unit and integration tests using one of the following commands:

```bash
# Run all tests (unit and integration)
TEST_DATABASE_URL="postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable" make test

# Run only database store layer integration tests
TEST_DATABASE_URL="postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable" make test-store

# Run only HTTP handler integration tests
TEST_DATABASE_URL="postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable" make test-handler

# Run tests and generate an HTML coverage report
TEST_DATABASE_URL="postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable" make test-coverage
```

## Code Quality and Formatting

Execute these commands before committing code changes to ensure compatibility and consistency:

```bash
make format    # Formats all Go files using gofmt
make lint      # Runs static analysis via golangci-lint
make vet       # Runs go vet
make check     # Runs lint, vet, and tests sequentially
```
