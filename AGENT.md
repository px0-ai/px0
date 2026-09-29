# Operational Guidelines for AI Agents

All operational instructions, architectural tenets, documentation maintenance requirements, and codebase mappings for AI coding agents have been consolidated under the [`docs/agents/`](docs/agents/README.md) directory.

## Quick Reference

- Comprehensive Guidelines: See [`docs/agents/README.md`](docs/agents/README.md) for:
  1. Core Architectural Tenets: Reads-first design with edits delegated to a coding harness, zero-runtime static binary footprint, bounded concurrency budgets.
  1. Mandatory Documentation Maintenance Matrix: Protocols for keeping documentation in sync whenever code is changed.
  1. Pre-Commit Verification Checklist: Test suites and architecture synchronization.
  1. Version Bump & Release Verification Protocol: Workflow for verifying release scripts and sequencing commits when bumping versions.
  1. Frontend Architecture & Code Map: Section index of `web/index.html` and ES module catalog of `web/src/`.
- Internal Architecture Documentation: See [`docs/internals/README.md`](docs/internals/README.md) for deep-dive technical write-ups covering server lifecycle, indexing, fuzzy matching, search, syntax highlighting, DOM virtualization, and LSP.

## Version Bump & Release Verification Protocol

When committing a version bump (triggered after the user updates the `VERSION` file):
1. **Verify Release Pipeline First**: Before creating the version bump commit, verify that all GitHub release scripts and build workflows are working cleanly without errors:
   - Frontend bundling: Verify `node ./scripts/build-web.js` passes with zero identifier collisions under both Bun and Node fallback.
   - Build & Cross-Compilation: Verify `go test ./...` passes and target builds in `build.sh` / `.github/workflows/release.yml` compile cleanly.
   - Release Configuration: Verify checksums, release workflow definitions, and installer compatibility.
2. **Commit Release Fixes First**: If any release scripts, bundlers, or workflow configurations need updates or fixes, make those changes and commit them in a separate commit first.
3. **Commit Version Bump**: Finally, create the version bump commit staging `VERSION` with the updated version number that the user started with (following the `ape-commit` format).

