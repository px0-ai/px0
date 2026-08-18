# Phase 2: Real qmd retrieval backend

## Status quo this phase changes

`px0/retrieval.py:1-12` (module docstring): "Backend: SQLite FTS5 with BM25 ranking, embedded, no server. This is a pure-python-reachable subset of what the spec asks of qmd (hybrid keyword + vector search, rerank): only the keyword/BM25 half is implemented here... `[retrieval] backend` names this the 'local' backend so a real qmd integration can be swapped in later behind this same function signature."

`px0/config.py:208-211` restricts `retrieval.backend` to `choices: ["local"]` only. `px0/retrieval.py:146-174`'s `retrieve()` is the only implementation, and it is pure SQLite -- no external process is ever shelled out to from this module today. This phase adds `qmd` as a second, real backend behind the same `retrieve(home, config, query, k, local_only)` signature, selected by `config.retrieval.backend`.

## Why the CLI, not a library call (not a tradeoff -- a constraint)

qmd (`github.com/tobi/qmd`, published as `@tobilu/qmd` on npm) is a Bun/Node.js project; its programmatic API (`createStore` etc.) is a TypeScript library, unreachable from Python. Spec.md:289 anticipates exactly this: "prefer its programmatic API where one is exposed, fall back to its CLI with JSON output otherwise." For px0 (Python), the CLI-with-JSON path is the only reachable one -- there is no "prefer the library" choice to make here.

## Implementation step 0: verify the installed qmd CLI surface (live, before writing code)

The qmd README (fetched during planning, `github.com/tobi/qmd`) documents `qmd collection add`, `qmd query --json`, and `qmd embed`, but three details were not independently confirmed against a running instance and must not be guessed:

1. **Query JSON schema.** Run `qmd query "test query" --json -n 1 -c <collection>` against a populated collection and record the exact top-level keys (expected candidates based on the CLI's own flags: a path/source field, a score field, a text/content field, an anchor/heading field -- `--full` implies text is truncated by default so a text field exists; `--explain` implies a nested score-breakdown object exists). Write the confirmed field names into `_parse_qmd_result()` (see Data model below) -- do not assume the guessed names above are correct.
2. **Collection existence check.** Run `qmd collection list` (no `--json` was explicitly documented for this subcommand) and confirm whether it's parseable plain text or accepts `--json` too. This phase's design (below) parses plain-text output defensively either way, so this step only needs to confirm the collection name appears verbatim in the output.
3. **Version flag.** Run `qmd --help` and confirm the exact flag that prints qmd's version (assumed `--version` by CLI convention; confirm before wiring the doctor check).

None of these affect the phase's architecture -- they only fill in literal strings inside already-fully-specified functions.

## Assumptions (stated explicitly, low-stakes)

1. **One collection, name `px0-knowledge`**, pointed at the configured `knowledge.path`, matching spec.md:297 ("One qmd collection is pointed at `knowledge/`, all subfolders included").
2. **`retrieval.qmd_cmd` config key**, default `"qmd"`, added alongside `model.harness_cmd` as the invocation prefix -- same pattern as `harness.py`'s `KNOWN_HARNESSES`/`resolve_harness_cmd`, so a user who installed qmd as `npx @tobilu/qmd` or `bunx @tobilu/qmd` can override it.
3. **Model-download consent is px0's responsibility, not qmd's.** qmd auto-downloads its three GGUF models (embeddinggemma-300M ~300MB, qwen3-reranker-0.6b ~640MB, qmd-query-expansion-1.7B ~1.1GB; confirmed sizes from the qmd README, total ~2.04GB) on first `qmd embed` call with no prompt of its own. Spec.md:65, 299 require "explicit consent and a printed size" before any such download. px0 gates its *own first call* to `qmd embed` behind a one-time y/N prompt printing this table; the consent is recorded in `.state/retrieval-consent.json` so it is asked exactly once per store.
4. **MCP harness registration is out of scope for this phase.** Spec.md:314 wants qmd's `qmd mcp` mode registered as a connector so the harness subprocess can query it mid-run. Wiring that depends on each of the four harnesses' (`claude`/`gemini`/`pi`/`opencode`) own MCP-config flags, none of which are verified in this codebase (`px0/harness.py` never mentions MCP). This phase implements `px0 search`, `px0 ask`, and workflow `retrieve:` inputs against qmd directly; harness-mid-session MCP access to qmd is a follow-up, noted but not phased here since it has no phase to attach to without inventing harness-flag claims this pass can't verify.

## Engineering section

### Dependencies on prior phases

Depends on Phase 1 (Composio tools) only for the shared pytest harness (`tests/conftest.py`, the `dev` extra in `pyproject.toml`) -- no interface from Phase 1's own application code (`connect.py`/`tools.py`) is consumed here. Every function this phase adds or changes (`retrieval.py`, `config.py`, `doctor.py`) is otherwise independent of Phases 1, 3, 4, 5, and 6.

### What already exists (reused, not rebuilt)

- `px0/retrieval.py`'s `Passage` dataclass (`23-32`), `knowledge_path()` (`35-38`), and the `retrieve()` function signature (`146-148`) -- the qmd backend returns the same `Passage` objects; every caller (`px0/ask.py:26`, `px0/cli.py:612`, workflow `retrieve:` inputs) needs zero changes.
- `px0/knowledge.py`'s `read_header()` (`40-50`) -- reused to read `ingested_at`/`is_stub` metadata for passages qmd returns, since qmd indexes raw file text and knows nothing about px0's YAML-frontmatter convention.
- `px0/harness.py`'s shell-out pattern (`invoke()`, `78-98`; `installed_harnesses()`, `39-42`) -- copied, not imported (qmd isn't a harness), as the template for `_qmd_run()`.
- `px0/doctor.py`'s existing `_check_index` (`37-43`) and `_check_harness`-style live-subprocess check pattern -- extended.

### Components touched

| File | Change |
| --- | --- |
| `px0/config.py` | `retrieval.backend` choices: `["local"]` -> `["local", "qmd"]` (`208-211`). New key `retrieval.qmd_cmd`, default `"qmd"`, free-form string (same shape as `model.harness_cmd`). |
| `px0/retrieval.py` | Add `_qmd_run(config, *args) -> str` (subprocess wrapper), `_qmd_ensure_collection(home, config)`, `_qmd_ensure_embed_consent(home, config)`, `_qmd_retrieve(home, config, query, k)`, `_parse_qmd_result(raw_json) -> list[Passage]`. `retrieve()` (`146-174`) becomes a 3-line dispatch: if `config.retrieval.backend == "qmd"`, call `_qmd_retrieve`; else the existing SQLite path, unchanged. `reindex()` (`96-122`) gets the same dispatch: `qmd` backend calls `_qmd_ensure_collection` + `qmd update` instead of the SQLite rebuild. |
| `px0/doctor.py` | New `_check_qmd_version(home, config)`: when backend is `qmd`, runs `qmd <version-flag>` and compares against a `QMD_PINNED_VERSION` constant (see Rollout); `_check_index` (`37-43`) is extended to call this when applicable. |
| `px0/paths.py` | Add `retrieval_consent_path(home)` -> `.state/retrieval-consent.json`. |
| `tests/test_retrieval_qmd.py` (new) | Unit tests against a fake `qmd` subprocess (monkeypatched `subprocess.run`). |

No new public classes; `_qmd_run` etc. are private module functions, matching the file's existing style (`retrieval.py` has no classes besides the `Passage` dataclass already there).

### Data model

`.state/retrieval-consent.json` (new, not versioned -- it lives under `.state/`, same non-versioned bucket as the index itself):

```json
{ "qmd_embed_consented": true, "consented_at": "2026-08-18T12:00:00+00:00" }
```

`config.toml` additions (versioned, since `config.toml` is versioned per spec.md:125):

```toml
[retrieval]
backend = "qmd"        # was "local"; choices now ["local", "qmd"]
qmd_cmd = "qmd"         # new
k_default = 5
rerank = true            # now meaningful: qmd's hybrid mode always reranks when this is true
```

`Passage` (`px0/retrieval.py:23-32`) is unchanged -- the qmd backend populates the same five fields by joining qmd's JSON output with `read_header()` on the matched file for `ingested_at`/`is_stub` (qmd's own JSON has no concept of px0's frontmatter).

### API / CLI contract (qmd, from the confirmed README plus step-0 verification)

```shell
# one-time collection setup (idempotent: checked before adding, see Key flows)
qmd collection add <knowledge_path> --name px0-knowledge --mask "**/**.md"

# one-time (post-consent) embedding generation, re-run after every reindex
qmd embed -c px0-knowledge

# hybrid query, per retrieve()
qmd query "<query>" --json -n <k> -c px0-knowledge

# incremental reindex, per reindex()
qmd update -c px0-knowledge
```

### Key flows

**`px0 search reindex` / nightly daemon reindex (`px0/daemon.py:114`, unchanged call site):**

1. `reindex()` checks `config.retrieval.backend`.
2. `qmd` path: `_qmd_ensure_collection` runs `qmd collection list`, checks (plain-text `in` check, per step-0 finding) whether `px0-knowledge` is present; if not, runs `qmd collection add`.
3. `_qmd_ensure_embed_consent`: if `.state/retrieval-consent.json` doesn't exist or `qmd_embed_consented` is false, print the model-size table from Assumption 3 and prompt `Download ~2.04GB of local models for semantic search? [y/N]`. On "N", the reindex still runs `qmd update` (BM25-only within qmd, no `qmd embed`) and prints that semantic search is degraded to keyword-only until consent is given. On "y", write the consent file.
4. Runs `qmd update -c px0-knowledge` always; runs `qmd embed -c px0-knowledge` only if consented.
5. Returns the passage count qmd reports (parsed from its own summary output, or 0 with a note if it prints none -- confirm in step 0).

**`px0 ask` / `px0 search "<query>"` (`px0/ask.py:26`, `px0/cli.py:612`, unchanged call sites):**

1. `retrieve()` dispatches to `_qmd_retrieve(home, config, query, k)`.
2. `_qmd_retrieve` calls `_qmd_run(config, "query", query, "--json", "-n", str(k), "-c", "px0-knowledge")`.
3. `_parse_qmd_result` parses the JSON per the schema confirmed in step 0, builds `Passage` objects, joining each result's file path against `read_header()` for `ingested_at`/`is_stub`.
4. `local_only` filtering (currently dead code at `px0/retrieval.py:162-163` -- `is_work` is computed but never filtered on) is fixed **in this phase for both backends**: when `local_only=True`, passages whose `path` starts with `work/` are dropped after retrieval, for both the SQLite and qmd paths, closing the pre-existing bug the audit found alongside adding the new backend (same file, same function, trivial to fix together -- not a second phase).

**A qmd binary that isn't installed:**

1. `_qmd_run` catches `FileNotFoundError` (mirrors `px0/harness.py:90-91`) and raises a new `retrieval.RetrievalBackendError` with: `"qmd not found on PATH; install with `npm install -g @tobilu/qmd` (requires Node.js) or `bun install -g @tobilu/qmd` (requires Bun), or set `retrieval.backend` back to `local`."`
2. `px0 search`/`px0 ask`/`px0 doctor` all catch this and print it plainly rather than a raw traceback (same pattern as `harness.HarnessError` handling in `px0/cli.py:96-98, 187-188`).

### Non-functional requirements

- `_qmd_run` uses `timeout=60` for `query`/`update` (qmd is local and fast per its own design goals) and `timeout=1800` for `embed` (first-run model download can be large; 30 minutes is generous headroom, not a measured number -- there is no existing latency budget for this operation anywhere in the codebase or spec to derive one from, so this is stated as a conservative placeholder the implementer should revisit once step 0's live testing shows actual `qmd embed` wall-clock time on a real knowledge library).
- No change to `[retrieval] k_default` semantics or the `--k`/`-n` flag passed through.

### Failure modes

| Failure | Covered by test? | Error handling | Visible to caller? |
| --- | --- | --- | --- |
| `qmd` binary missing | Yes | `RetrievalBackendError`, printed plainly | Yes |
| `qmd query --json` returns malformed JSON | Yes | `json.JSONDecodeError` caught, re-raised as `RetrievalBackendError` with the raw output's first 200 chars | Yes |
| User declines model-download consent | Yes | Reindex proceeds keyword-only; `px0 doctor` reports "semantic search not consented" as an informational (not failing) check | Yes, via doctor |
| `qmd --version` reports a version different from the pinned one | Yes | `doctor` reports mismatch, matching spec.md:291's "px0 doctor detects a version mismatch" -- does not block, only warns (this build has no automated qmd-version pinning/install step, since px0 doesn't manage qmd's install per the "stay Python" decision; only `px0 doctor` surfaces drift) | Yes |
| `qmd embed` times out on a very large library | No (would need a large fixture; documented as a known gap) | `subprocess.TimeoutExpired` -> `RetrievalBackendError`; the daemon's nightly pass (`px0/daemon.py:113-116`) already catches all `Exception` from `reindex()` and records it as `reindex_error` without crashing the nightly pass | Yes, in the nightly report |

### Test plan

Uses the pytest harness established in Phase 1. All qmd subprocess calls are monkeypatched (`subprocess.run` replaced with a fixture returning canned stdout matching step 0's confirmed schema) -- no real qmd binary required in CI.

| Layer | What | Count |
| --- | --- | --- |
| Unit | `_qmd_ensure_collection` skips `collection add` when already listed | +1 |
| Unit | `_qmd_ensure_collection` adds when absent | +1 |
| Unit | `_qmd_ensure_embed_consent` prompts once, persists, doesn't re-prompt | +2 |
| Unit | `_parse_qmd_result` builds correct `Passage` list from canned JSON | +1 |
| Unit | `local_only=True` drops `work/`-prefixed passages (both backends) | +2 |
| Unit | Missing `qmd` binary raises `RetrievalBackendError` with install hint | +1 |
| Integration | `retrieve()` dispatches to qmd vs local based on `config.retrieval.backend` | +2 |
| Integration | `doctor._check_qmd_version` flags a version mismatch | +1 |

### Rollout

`QMD_PINNED_VERSION` is a new constant in `px0/retrieval.py` (mirrors `SCHEMA_VERSION` in `px0/__init__.py:8`), bumped deliberately, not floated, per spec.md:289. Switching `retrieval.backend` from `local` to `qmd` does not migrate the existing SQLite FTS5 index -- `_qmd_ensure_collection` builds qmd's own index from scratch on first use, exactly as spec.md:291 accepts ("no index migration path is written or maintained"). Rollback: setting `retrieval.backend` back to `local` (a versioned `config.toml` edit, revertible via `px0 versions revert config.toml --to <N>` like any other config change) immediately restores the SQLite path; the SQLite index file is never deleted by this phase, so no reindex is needed to roll back.

## Product section

**Phase goal:** `px0 search`, `px0 ask`, and workflow `retrieve:` inputs get real hybrid keyword+semantic search with reranking over `knowledge/`, closing the gap between the spec's "qmd" backend and the shipped "local" (BM25-only) one.

**User story:** the user has hundreds of ingested blog posts and papers; a keyword search for "connection pooling" currently only matches posts using that exact phrase. With qmd, a semantically related passage that says "reusing database connections" also surfaces.

**In scope:**
- `retrieval.backend = "qmd"` as a fully working alternative to `local`.
- One-time collection setup, consent-gated model download, incremental `qmd update` on every reindex trigger (same call sites as today: `px0 search reindex`, `knowledge.add()`'s post-ingest reindex at `px0/knowledge.py:252, 275`, the nightly daemon pass at `px0/daemon.py:114`).
- `px0 doctor` reports qmd version drift and unconsented-model status.
- Fixes the pre-existing `local_only` no-op bug (`px0/retrieval.py:162-163`) for both backends, since work-folder exclusion is a stated security property (spec.md:250, 305) currently silently broken.

**Out of scope (deferred, no phase currently planned):**
- Registering qmd's `qmd mcp` stdio server as an MCP connector for the harness subprocess (spec.md:314) -- blocked on unverified per-harness MCP-config flags.
- Automated qmd install/version management by px0 itself (the installer/update story is Phase 5's territory, and Phase 5 does not manage qmd either, per the Composio/qmd/MCP scoping decisions made before this plan was written -- qmd stays a user-installed, `doctor`-checked external binary, same as the harness).

**Acceptance criteria:**
1. With `retrieval.backend = "qmd"` and a populated `knowledge/`, `px0 search "connection pooling" --json` returns passages `px0 search` with `local` backend does not, when the match is semantic rather than lexical (manually verified in QA against a live qmd install; not asserted in the mocked unit tests).
2. First reindex after switching to `qmd` prints the model-size table and requires an explicit `y` before downloading anything.
3. `px0 doctor` reports `[FAIL] index: qmd version 0.x.y does not match pinned 0.x.z` when the installed qmd's `--version` output differs from `QMD_PINNED_VERSION`.
4. A workflow's `retrieve: {query, k}` input (`px0/runner.py`'s input-resolution path, unchanged) returns qmd-backed passages when the store is configured for `qmd`, with zero changes to the workflow file format.
5. `local_only=True` retrieval never returns a passage whose path starts with `work/`, for both backends (regression test for the pre-existing bug).

## Definition of done

- [ ] AC1-5 above pass.
- [ ] Step-0 verification results (query JSON schema, collection-list format, version flag) are recorded as code comments in `retrieval.py` next to where they're used, citing the qmd version tested against.
- [ ] `pytest` green with the new qmd tests, no live qmd binary required.
- [ ] `px0 config list` shows the new `retrieval.qmd_cmd` key with its default and help text.
