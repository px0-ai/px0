# px0 gap-closing phases

Source spec: [`../spec.md`](../spec.md). This is not a greenfield build -- the repo at `/home/arpit/workspace/px0/px0` is a working Python implementation (`px0/*.py`, ~5,200 lines) that already covers most of the spec: versioning, the workflow runner, the builder, the daemon's scheduling, consolidation/proposals, and doctor. What's phased here are the specific gaps a codebase audit found, each one a place where the shipped code deliberately and honestly stops short of the spec (documented in the code's own module docstrings) or falls short of what its own CLI promises. Four of these gaps were resolved by explicit decisions before this plan was written: distribution via PyPI + pipx (not a signed binary), a real qmd integration (not permanent BM25-only), a real Composio integration (not GitHub-only), and the existing simplified tool-call text protocol staying as-is (not a real per-run MCP server).

## Overview

**Phase 1 -- Composio connections and tools, for real.** Closes the biggest functional gap: `calendar`/`gmail`/`slack` tools are wired in the code's tool registry but every call raises `ConnectorNotConfigured` by design (`px0/tools.py:166-174`). This phase makes `px0 connect <app>` create a real Composio auth link and makes the five Composio-backed tools actually call Composio's API, unlocking any workflow that reads a calendar, searches/sends email, or posts to Slack. It also establishes the repo's first automated test harness (pytest), since none exists today.

**Phase 2 -- Real qmd retrieval backend.** Closes the retrieval gap: `px0/retrieval.py` implements only the keyword/BM25 half of what the spec calls "qmd" (`px0/retrieval.py:5-11`). This phase adds qmd (`github.com/tobi/qmd`, shelled out to as an external binary, the same pattern already used for the model harness) as a real, selectable backend behind the existing `retrieve()` interface, giving `px0 search`/`px0 ask`/workflow `retrieve:` inputs hybrid keyword+semantic search with reranking.

**Phase 3 -- Daemon and log follow-through.** Closes three small, related gaps where the daemon or its CLI promises something it doesn't do: queued YouTube playlists sit in `.state/ingest/` forever since nothing drains them (`px0/knowledge.py:198-206`, `px0/daemon.py:107-118`); `px0 daemon logs` is a canned stub message (`px0/cli.py:352-355`); and `px0 runs logs --follow` is declared but explicitly documented as "not supported" (`px0/cli.py:879`). All three become real in one phase since they're the same underlying theme: operational visibility and background processing that actually work.

**Phase 4 -- Real `skills build` compilation.** Closes the compile gap: `px0 skills build` is currently a flat file copy with no bundling format (`px0/skills.py`, 27 lines). This phase compiles guidelines into real Claude Code skill bundles (`SKILL.md` with `name`/`description` frontmatter, verified against Claude Code's documented format) and symlinks them into `~/.claude/skills/` when the configured harness is Claude, so the user's guidelines are available in their everyday interactive coding sessions, not just inside px0 workflow runs.

**Phase 5 -- PyPI + pipx distribution and self-update.** Closes the installer/self-update gap: there is no `install.sh` anywhere in the repo, and `px0/update.py` is an honest no-op ("no such channel exists for this build," `px0/update.py:1-9`). This phase ships a real `install.sh` (pipx-based, no OS/arch binary detection needed since px0 is pure Python), a real PyPI publish pipeline, and a real `px0 update`/`px0 update rollback` backed by the public PyPI JSON API -- plus a store-schema migration mechanism and a fix for two pre-existing duplicate-source-of-truth version constants found during this phase's own investigation.

**Phase 6 -- `px0 runs` TUI.** Closes the interactivity gap: every data function the spec's TUI needs already exists (`px0/runs.py`), but there's no interactive view -- `px0 runs` with no subcommand is currently an argparse error. This phase adds a curses-based list/detail TUI (list filterable by workflow/outcome/write-activity/since; detail view with the rendered prompt, guidelines-with-versions, tool calls, and the `r`/`l`/`o`/`w` keystrokes spec.md names), plus one small data addition (`tool_calls[].elapsed_seconds`) the detail view needs and the run record didn't previously capture.

### Dependency diagram

```
Phase 1 (Composio tools, + pytest harness)
   |
   |-- shared harness only --> Phase 2 (qmd retrieval)
   |-- shared harness only --> Phase 3 (daemon follow-through)
   |-- shared harness only --> Phase 4 (skills build)
   |-- shared harness only --> Phase 5 (PyPI distribution)
   `-- shared harness only --> Phase 6 (runs TUI)
```

Every arrow is "needs the pytest scaffolding Phase 1 sets up," not "needs Phase 1's application code." Phases 2-6 touch disjoint application code and could ship in any order, or all at once, relative to each other.

### Sequencing rationale

Phases 2-6 have no functional dependency on one another or on Phase 1 beyond the shared test harness -- this is a gap-closing pass over an already-working system, not a walking-skeleton build where later phases need earlier ones' interfaces. The order above is a risk/value ordering, not a dependency ordering: Composio (Phase 1) and qmd (Phase 2) carry the most external-API uncertainty (verified against current docs during planning, but both are live third-party surfaces that can drift) and the most user-visible capability, so they go first, while daemon follow-through, skills-build, distribution, and the TUI are lower-risk, more self-contained gaps that go later. If the order were changed -- say, the TUI shipped first -- nothing would break, since it only reads data every other phase leaves untouched; the only real constraint is that Phase 1 (or an equivalent minimal `tests/conftest.py` setup) lands before any other phase's tests can run.

### Parallelization note

All six phases can be handed to separate agents/worktrees in parallel. The only coordination points are files touched by more than one phase at different, non-overlapping functions -- a merge-conflict risk, not a design dependency:

- `px0/cli.py`: touched by Phases 1, 3, 5, and 6 (different command handlers each).
- `px0/doctor.py`: touched by Phases 1 and 2 (different check functions).
- `px0/daemon.py`: touched by Phases 3 and 5 (different functions -- see Phase 5's Dependencies section).
- `pyproject.toml`: touched by Phases 1 (adds `composio`, adds the `dev` extra) and 5 (adds `packaging`, switches to dynamic versioning, adds metadata) -- straightforward to merge, just worth sequencing those two specifically if hand-merging is undesirable.

Suggested lanes if run concurrently: `Lane A: Phase 1` (also produces the shared test scaffolding others should branch from once it lands), `Lane B: Phase 2`, `Lane C: Phase 3`, `Lane D: Phase 4`, `Lane E: Phase 5`, `Lane F: Phase 6` -- all independent, with Phase 1 landing first purely so the other five have `tests/conftest.py` to write against rather than each inventing their own.

## Phase documents

1. [Phase 1: Composio connections and tools, for real](phase-1-composio-tools.md)
2. [Phase 2: Real qmd retrieval backend](phase-2-qmd-retrieval.md)
3. [Phase 3: Daemon and log follow-through](phase-3-daemon-followthrough.md)
4. [Phase 4: Real `skills build` compilation](phase-4-skills-build.md)
5. [Phase 5: PyPI + pipx distribution and self-update](phase-5-pypi-distribution.md)
6. [Phase 6: `px0 runs` TUI](phase-6-runs-tui.md)
