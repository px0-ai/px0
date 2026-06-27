# px0 Development Guidelines

## Testing is mandatory

Every change must ship with tests. No exceptions.

- New feature: tests covering the happy path and at least one failure path.
- Bug fix: a regression test that reproduces the bug before the fix and passes after.
- Refactor: existing tests must still pass; add tests if coverage was missing.
- OpenAPI Spec Alignment: Whenever tests are created or updated to cover new edge cases, scenarios, or validation behaviors, the OpenAPI specification (under `docs/openapi/`) must be updated. This ensures that the documentation remains perfectly aligned with the test suite and implementation.

Run before every push:

```bash
make test        # all tests (unit + integration)
make check       # lint + vet + test
```

## Test database

Integration tests default to:

```
postgres://px0:px0secret@localhost:5432/px0_test?sslmode=disable
```

Override with the `TEST_DATABASE_URL` environment variable. Tests skip automatically if postgres is unreachable - they do not fail the build.

Create the test database once against the dev container:

```bash
docker exec px0-postgres psql -U px0 -c "CREATE DATABASE px0_test;"
```

## Where tests live

```
internal/store/*_test.go    - store (database) layer tests
internal/handler/*_test.go  - HTTP handler tests (full request/response)
internal/testutil/db.go     - shared DB setup helper (not a test file)
```

## What to test in each layer

### Store tests

Store tests are located in `internal/store/`.

- Every exported function needs a test.
- Test the success path, `ErrNotFound`, `ErrDuplicate`, and `ErrConflict` where applicable.
- The version publish cycle (draft -> live, previous live -> archived) must be covered.

### Handler tests

Handler tests are located in `internal/handler/`.

- Test the HTTP status code, not just the absence of an error.
- Cover: success path, invalid/missing input (400), auth enforcement (401/403), and not-found (404).
- Use `newTestApp(t)` to get a wired-up Fiber app backed by the test database.
- Use the helpers in `helpers_test.go` (`setupUser`, `setupPrompt`, `setupVersion`, etc.) rather than duplicating setup logic.

## Test helpers

```go
// internal/testutil/db.go
testutil.SetupDB(t)   // connects, migrates, truncates on cleanup

// internal/handler/helpers_test.go (package handler_test)
newTestApp(t)                            // full app wired to test DB
newReq(t, method, url, body, token)      // build HTTP request with bearer token
newAPIKeyReq(t, method, url, body, key) // build request with X-API-Key
decodeBody(t, resp)                      // decode JSON response to map
setupUser(t, app)                        // register + login, returns token
setupPrompt(t, app, token)               // create prompt, returns ID
setupVersion(t, app, token, id, tmpl)   // create draft version, returns number
setupAPIKey(t, app, token)               // create API key, returns raw key
```

## Make targets

| Target | What it does |
|--------|--------------|
| `make test` | All tests with race detector |
| `make test-store` | Store tests only, verbose |
| `make test-handler` | Handler tests only, verbose |
| `make test-coverage` | Coverage report to coverage.html |
| `make check` | lint + vet + test (required before PR) |

## API Development and Contract Testing

This project uses OpenAPI specifications as the source of truth for public APIs. Automated contract testing guarantees that the Go Fiber implementation and the OpenAPI files never drift.

### API Architecture

API definitions are stored in modular files inside `docs/openapi/`:

- `openapi.yaml`: The central unified entry point referencing all modular domains.
- `health.yaml`: Health check spec.
- `auth.yaml`: User registration, session login, logout, and self profile specs.
- `api-keys.yaml`: Programmatic API keys CRUD specs.
- `prompts.yaml`: Prompts CRUD, draft version management, and template render specs.

### Contract Assertions

The test helper `AssertContract` is automatically executed within our custom in-memory testing wrapper `testApp.Test` inside `internal/handler/helpers_test.go`:

```go
func (a *testApp) Test(req *http.Request, _ ...int) (*http.Response, error) {
	resp, err := a.App.Test(req, -1)
	if err == nil && a.t != nil {
		AssertContract(a.t, resp)
	}
	return resp, err
}
```

Every integration test using `a.Test` performs these checks:

- Path Validation: Ensures the HTTP method and request URL are documented in the spec.
- Request Body Validation: Validates that payload parameters and types match success schemas for all requests returning status codes under 400.
- Response Schema Validation: Validates that response bodies, properties, and types match defined schema definitions for both success and error status codes.

### How to Add or Modify an API

Follow these sequential steps when making any changes to existing endpoints or introducing new API routes:

- Update Specification: Modify the relevant YAML file under `docs/openapi/` to document the path, parameters, request body, successful responses, error responses, enums, and examples.
- Update Go Implementation: Modify Go handlers, models, routers, and error definitions under `internal/` to implement the behavior.
- Run Integration Tests: Execute `go test ./internal/handler/...` to verify alignment. The tests will fail with a drift report if the Go payload format or status codes differ from the YAML spec.
- Resolve Drift: Fix either the Go implementation or the YAML spec until the test suite compiles and passes successfully.

### Specification Standards

Maintain the following standards in all OpenAPI documents to ensure correct code generation:

- Keep Schemas Reusable: Declare all models under `components/schemas` and reference them using `$ref`. Avoid inline schema declarations in request or response objects.
- Operation Identifiers: Assign a unique, camelCase `operationId` to every endpoint (e.g., `createPromptVersion`, `listAPIKeys`). Generators use this name for client SDK method names and CLI subcommands.
- Rich Descriptions: Provide descriptive explanations for all path variables, query parameters, and schema fields. These are parsed into CLI flag help menus and MCP server instructions.
- Custom Metadata Extensions: Always write natural language boundary conditions under `x-edge-cases`, and document the verifying tests under `x-test-coverage`.
- Test-Driven Specification Alignment: When new tests or test cases are added or updated to verify edge cases/scenarios, you must update the OpenAPI specification's `x-edge-cases` and `x-test-coverage` metadata to document those scenarios.
