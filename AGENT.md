# Operational Guidelines for AI Agents

Welcome agent! This document contains essential instructions and workflows for working productively and quickly in `px0`.

---

## 1. Quick Development Workflow (Fast Inner Loop)

To keep iteration fast and avoid unnecessary delays:

> [!IMPORTANT]
> **Do NOT run tests after every intermediate change.** Running test suites repeatedly after every small edit severely slows things down. Complete all planned code and asset changes first. Run tests only once all changes are in place, and then iterate on fixing any failures until everything passes cleanly.

| Task | Command | Typical Time | Notes |
| :--- | :--- | :--- | :--- |
| **Frontend Check** | `make web-check` or `npm run check` | **<100ms** | Validates JS syntax, relative imports, and runs frontend tests. |
| **Frontend Bundle** | `make web` or `node scripts/build-web.js` | **<50ms** | Run whenever modifying files under `web/src/`. |
| **Build Binary** | `make build` | **~2s** | Bundles frontend (`web/app.js`) and compiles local `./px0` binary. |
| **Fast Verification** | `make check` or `go test -short .` | **~7s** | Runs frontend validation, asset bundling, and fast Go unit tests. |
| **Full Test Suite** | `make test` or `go test .` | **~20s** | Run once all changes are done; iterate until all tests pass before concluding. |

> [!TIP]
> Isolated components in `internal/` can be tested in milliseconds without running the full test suite (e.g. `go test ./internal/table`, `go test ./internal/ignore`, `go test ./internal/fuzzy`, `go test ./internal/metrics` take ~5–10ms).

---

## 2. Codebase Organization

- **Backend Architecture**: Go source files live in the root package (`package main`), with decoupled subsystems extracted into `internal/` packages:
  - [`internal/table/`](internal/table/): Tabular formatting (CSV/TSV)
  - [`internal/ignore/`](internal/ignore/): `.gitignore` parsing, fnmatch globbing, and ignore trees
  - [`internal/fuzzy/`](internal/fuzzy/): Bounded two-pass fuzzy path finder and scoring algorithm
  - [`internal/metrics/`](internal/metrics/): System resource usage, RSS, CPU sampling, and OS-specific rusage
  Root files provide backward-compatible facades and type aliases. Assets are statically embedded via `//go:embed`.
- **Frontend Architecture**: Modular ES modules reside in [`web/src/`](web/src/) and are bundled into [`web/app.js`](web/app.js) via [`scripts/build-web.js`](scripts/build-web.js). Checked via [`scripts/check-web.js`](scripts/check-web.js).
- **Benchmark Corpus**: Heavy repositories for benchmarking live in `../bench-repos` (outside the project root) to keep workspace searches and indexers fast.
- **Detailed Map**: For a complete file catalog and UI element mapping, see [`docs/agents/README.md`](docs/agents/README.md).

---

## 3. Core Architectural Tenets

1. **Reads First, Edits via Harness**: `px0` is an ultra-fast code reader and reviewer. File modifications are delegated to external coding harnesses (`agent.go`, `thread.go`), never authored directly to disk by `px0` (with the exception of explicit user-initiated Git operations in the sidebar).
2. **Zero Runtime Dependencies**: The output must remain a single, standalone static binary. No CGO, no Node.js runtime requirement for the user, no external databases.
3. **No Workspace Litter**: `px0` never creates `.px0/` folders or cache files inside a workspace tree. Persistent configuration and thread transcripts live in `$XDG_CONFIG_HOME/px0/` or `~/.px0/`.
4. **Fast & Non-Blocking**: Operations must remain responsive. All background tasks, network calls, and git operations must respect timeouts and set `GIT_TERMINAL_PROMPT=0`.

---

## 4. Documentation Policy

- **During Iterative Work**: Focus on completing code changes first before running verifications. Do not block your workflow with premature documentation edits.
- **On Feature Completion**: If you introduce a new feature, CLI flag, shortcut, or major architectural change, update the corresponding documentation:
  - CLI flags / Shortcuts -> [`README.md`](README.md)
  - Major architectural patterns -> [`docs/internals/`](docs/internals/)
  - User features -> [`docs/features/`](docs/features/)

---

## 5. Version Bump & Release Protocol

When committing a version bump (triggered after updating the `VERSION` file):
1. **Verify Release Pipeline**:
   - Verify frontend bundling: `node ./scripts/build-web.js`
   - Verify full test suite: `make test`
   - Verify builds: `build.sh` / `.github/workflows/release.yml`
2. **Commit Release Fixes First**: If bundlers or scripts need fixes, commit those separately first.
3. **Commit Version Bump**: Stage `VERSION` with the new version number using the standard release commit message.
