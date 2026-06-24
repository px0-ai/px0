# px0 Development Guidelines

## Testing is mandatory

Every change must ship with tests. No exceptions.

- New feature: tests covering the happy path and at least one failure path.
- Bug fix: a regression test that reproduces the bug before the fix and passes after.
- Refactor: existing tests must still pass; add tests if coverage was missing.

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

Override with the `TEST_DATABASE_URL` environment variable. Tests skip
automatically if postgres is unreachable - they do not fail the build.

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

**Store tests** (`internal/store/`)

- Every exported function needs a test.
- Test the success path, `ErrNotFound`, `ErrDuplicate`, and `ErrConflict` where applicable.
- The version publish cycle (draft -> live, previous live -> archived) must be covered.

**Handler tests** (`internal/handler/`)

- Test the HTTP status code, not just the absence of an error.
- Cover: success path, invalid/missing input (400), auth enforcement (401/403), and not-found (404).
- Use `newTestApp(t)` to get a wired-up Fiber app backed by the test database.
- Use the helpers in `helpers_test.go` (`setupUser`, `setupPrompt`, `setupVersion`, etc.)
  rather than duplicating setup logic.

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
| `make test-coverage` | Coverage report → `coverage.html` |
| `make check` | lint + vet + test (required before PR) |
