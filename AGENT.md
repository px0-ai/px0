# Operational Guidelines for AI Agents

All operational instructions, architectural tenets, documentation maintenance requirements, and codebase mappings for AI coding agents have been consolidated under the [`docs/agents/`](docs/agents/README.md) directory.

## Quick Reference

- Comprehensive Guidelines: See [`docs/agents/README.md`](docs/agents/README.md) for:
  1. Core Architectural Tenets: Reads-first design with edits delegated to a coding harness, zero-runtime static binary footprint, zero disk state, bounded concurrency budgets.
  1. Mandatory Documentation Maintenance Matrix: Protocols for keeping documentation in sync whenever code is changed.
  1. Pre-Commit Verification Checklist: Test suites, web bundling, and architecture synchronization.
  1. Frontend Architecture & Code Map: Section index of `web/index.html` and ES module catalog of `web/src/`.
- Internal Architecture Documentation: See [`docs/internals/README.md`](docs/internals/README.md) for deep-dive technical write-ups covering server lifecycle, indexing, fuzzy matching, search, syntax highlighting, DOM virtualization, and LSP.
