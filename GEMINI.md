# px0 Development Guidelines

## Testing guidelines

We do not run or require tests for every single change. Trivial modifications, documentation updates, configuration changes, or simple string updates (such as updating error messages) do not require running the full test suite.

To optimize test execution, **only run the relevant tests affected by your changes** rather than running the entire test suite on every change:
- For database store changes: Run `make test-store` or `go test ./internal/store/...`
- For HTTP API and handler changes: Run `make test-handler` or `go test ./internal/handler/...`
- For highly targeted validation, run a specific test: `go test ./internal/handler/... -run TestMySpecificTest`

Only run the full test suite (`make test`) when validating a major feature, preparing for a pull request, or performing final checks.

For significant functional code changes:
- New feature: tests covering the happy path and at least one failure path.
- Bug fix: a regression test that reproduces the bug before the fix and passes after.
- Refactor: existing tests must still pass; add tests if coverage was missing.
- OpenAPI Spec Alignment: Whenever tests are created or updated to cover new edge cases, scenarios, or validation behaviors, the OpenAPI specification (under `docs/openapi/`) must be updated. This ensures that the documentation remains perfectly aligned with the test suite and implementation.
- Test Execution Sequence: Tests should run only after the changes are done and not before making the changes. Do not execute tests prior to making your code modifications.

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

## Database Migration Guidelines

To maintain data integrity and avoid application downtime, we strictly follow a **non-destructive database schema migration strategy**:

- **Never perform destructive migrations:** Do not write SQL migrations containing `DROP TABLE`, `DROP COLUMN`, `ALTER TABLE ... DROP CONSTRAINT`, or column type modifications that break compatibility.
- **Support blue-green/canary deployments:** Schema changes must always be backward-compatible with the currently running version of the application code. Use the "Expand and Contract" pattern for renaming columns/tables or changing data types:
  1. *Expand:* Add the new column/table, write to both, read from the old.
  2. *Backfill:* Copy/migrate historical data from the old to the new column/table.
  3. *Transition:* Update code to read and write from the new column/table.
  4. *Contract:* Drop the old column/table in a future, separate release after verifying stability.
- **No automatic migrations at server startup:** Migrations must not be executed during server startup (e.g., in `main.go`). This decouples application deployments from database migrations, allowing for safe rollbacks and orderly sequencing.
- **Run migrations explicitly:** Execute migrations manually or via your deployment pipeline using:
  ```bash
  make migrate
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
newAPIKeyReq(t, method, url, body, key) // build request with Bearer API Key
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
| `make spec-bundle` | Bundles multi-file OpenAPI specification into a single self-contained file |
| `make migrate` | Runs database migrations explicitly |
| `make check` | lint + vet + test (required before PR) |

## API Development and Contract Testing

This project uses OpenAPI specifications as the source of truth for public APIs. Automated contract testing guarantees that the Go Fiber implementation and the OpenAPI files never drift.

### API Architecture

API definitions are stored in modular files inside `docs/openapi/`:

- `openapi.yaml`: The central unified entry point referencing all modular domains via external `$ref`s.
- `health.yaml`: Health check spec.
- `auth.yaml`: User registration, session login, logout, and self profile specs.
- `api-keys.yaml`: Programmatic API keys CRUD specs.
- `prompts.yaml`: Prompts CRUD, draft version management, and template render specs.

#### Single-File Bundled Specification
Some external tools, generators, or older parsers do not support multi-file specifications with external `$ref`s. To resolve this:

- **`openapi-bundled.yaml`**: A fully self-contained, compiled version of the entire OpenAPI specification.
- To re-generate this bundle after modifying the modular specifications, run:
  ```bash
  make spec-bundle
  ```
  This command uses Redocly to compile all external references inline, ensuring absolute compatibility with Swagger UI, SDK generators, and external loaders.

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

## Authorization & Access Control Architecture

The project implements a centralized, declarative Role-Based Access Control (RBAC) architecture that unifies User Sessions and programmatic API Keys under a single paradigm.

### 1. The Unified `Subject` Principal
Upon successful authentication, the authentication middleware (`internal/middleware/auth.go`) constructs a unified `*model.Subject` struct and attaches it to `c.Locals("subject")`.

```go
type Subject struct {
	UserID         uuid.UUID
	IsUserAdmin    bool
	IsUserVerified bool

	IsAPIKey bool
	OrgID    *uuid.UUID
	IsOrgAdmin bool

	// TeamRoles maps team IDs to the role ("viewer", "editor", "admin")
	TeamRoles map[uuid.UUID]string
}
```

This unifies Users and API Keys:
- **Users**: Fetches actual team membership roles from `team_members` and determines org-admin capabilities.
- **API Keys**: Synthesizes roles for their scoped teams based on the key's operation (`read_render` -> `viewer`, `all` -> `editor`, `admin` -> `admin`).

### 2. Declarative RBAC Middleware
Authorization checks are handled at the router layer (`internal/app/app.go`) rather than inline within handlers. This is implemented via parameterized middlewares in `internal/middleware/rbac.go`:

- `RequireOrgAdmin()`: Asserts that the subject has Org Admin or system admin privileges.
- `RequireTeamRole(minRole string)`: Extracts `:teamID` from the path and asserts that the subject holds at least `minRole` on that team.
- `RequireProjectRole(minRole string)`: Extracts `:projectID` from the path, resolves the project's ownership and sharing grants from the database, and asserts that the subject holds at least `minRole` on any authorized team.

### 3. Database-Level RLS / Scoping
To prevent data leaks, store functions are designed to accept explicitly scoped project/team ID slices (e.g. resolved from `GetSubjectProjectIDs(ctx, subject, role)`). This acts as a "hard shell" around database queries, ensuring that handlers never fetch or modify unauthorized entities.

