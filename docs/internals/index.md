# px0 internals

How px0 is built, and why each piece is built that way.

This is a series. The command reference tells you what px0 does; this tells you what happens inside when you run it, which invariants hold, and which failure the design was chosen to avoid. Read it in order the first time. After that, each part stands alone.

Every part names the modules it covers, so you can read the code alongside it.

## The series

| Part | What it covers | Modules |
| ---- | -------------- | ------- |
| [1. Architecture](01-architecture.md) | The shape of the system, dependency direction, what is deliberately absent | all |
| [2. The store and configuration](02-store-and-config.md) | `~/.px0`, the config schema, path resolution, export and verify | `paths`, `store`, `config` |
| [3. Versioning and undo](03-versioning.md) | Content-addressed blobs, the SQLite manifest, checkpoint scans, claim aliasing | `versioning`, `claims`, `authoring` |
| [4. The workflow file](04-workflow-file.md) | Frontmatter as machine contract, the placeholder grammar, every validation rule | `workflow` |
| [5. Building a workflow](05-building.md) | Six harness passes, catalogue search, authorization before planning | `builder`, `catalogue` |
| [6. Running a workflow](06-running.md) | The eight stages, both agent loops, usage accounting, pipelines | `runner` |
| [7. The harness layer](07-harness.md) | Shelling out to a coding agent, capability tables, envelope parsing, downgrades | `harness` |
| [8. Tools and connectors](08-tools.md) | Four tool kinds in one namespace, Composio execution, authorization on demand | `tools`, `localtools`, `catalogue`, `connect` |
| [9. The brain and retrieval](09-brain.md) | Ingestion routes, extraction fallbacks, qmd, the local reranker | `brain`, `retrieval` |
| [10. Context assembly](10-context.md) | Guidelines, memory, and how a prompt is put together | `guidelines`, `memory`, `runner` |
| [11. The scheduler](11-daemon.md) | Cron evaluation, timezones, missed fires, watches, nightly housekeeping | `daemon` |
| [12. The trust model](12-trust.md) | Read/write split, allowlists, approvals, sandboxing, redaction | `approvals`, `localtools`, `mcp`, `runner` |
| [13. The feedback loop](13-feedback.md) | Deterministic health findings, marks, proposals, replay fixtures | `analysis`, `improve`, `replay`, `runs` |
| [14. Ask, routing, and sessions](14-ask.md) | One question, five destinations, and what a correction is worth | `route`, `ask`, `session`, `commands` |
| [15. The MCP surface](15-mcp.md) | px0 as a server, and the scoped server one run gets | `mcp` |
| [16. Sync and portability](16-sync.md) | Three-way agreement, conflicts, redacted exports | `sync`, `store` |
| [17. The CLI layer](17-cli.md) | The parser tree, the presentation module, completion, exit codes | `parser`, `cli`, `ui`, `completion`, `runs_tui` |
| [18. Release and diagnostics](18-release.md) | Versions, schema migrations, self-update, doctor, the test suite | `update`, `doctor`, `__init__` |

## Reading the code

The package is flat. Every module sits directly under `px0/`, and the file name is the concept: `runner.py` runs workflows, `brain.py` ingests material, `approvals.py` holds drafted writes.

Most modules carry a module-level docstring that argues for the design rather than restating the code. Those docstrings are the primary source for this series. Where a comment in the code explains why a branch exists, it is usually recording a bug that branch was written to fix.

`python scripts/gen_docs.py` regenerates `docs/reference.md` from every docstring in the package. That file is the API-level companion to this one.
