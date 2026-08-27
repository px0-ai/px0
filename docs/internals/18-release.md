# 18. Release and diagnostics

Modules: `px0/update.py`, `px0/doctor.py`, `px0/__init__.py`, `scripts/`, `tests/`

## Two version numbers

```python
__version__      # the installed px0, from the VERSION file or package metadata
SCHEMA_VERSION   # the on-disk store layout, currently 3
```

They move independently. Most releases change only the first. `SCHEMA_VERSION` is bumped when the store layout changes in a way an older px0 cannot read, and the store records what it was written for in `.state/schema`.

`__init__.py` reads `VERSION` from the repository root when present -- the checkout case -- and falls back to `importlib.metadata` for an installed package. It raises rather than defaulting if neither works, because a px0 that does not know its own version cannot honestly report an update.

## Migrations

`MIGRATIONS` maps each schema version to the function that produces it:

```python
MIGRATIONS: dict[int, Callable[[Path], list[Any]]] = {
    2: _migrate_v1_to_v2,
    3: _migrate_v2_to_v3,
}
```

Keyed by the version each one produces, not the one it starts from, so the runner applies every key greater than the store's current version. A v1-to-v2 migration is keyed 2.

Each returns a list of `FileChange` objects, which the runner records as one versioned change with actor `update`. A migration that touches nothing versioned -- `_migrate_v2_to_v3` rewrites credentials, which are deliberately outside the version chain -- returns an empty list.

The two shipped so far:

`v1 to v2` renamed `knowledge/` to `brain/` and `knowledge.path` to `brain.path`. It moves the folder and rewrites the config key. The retrieval index needs no fixing, because indexed paths are relative to the library root and that is what moved. It also drops the stale `px0-knowledge` qmd collection, best-effort, because that collection points at a path that no longer exists and qmd would keep serving results from it.

`v2 to v3` keys connected accounts by Composio's toolkit slug rather than px0's own name for an app, and scaffolds `tools/`. See [part 8](08-tools.md).

A schema file that cannot be read is a hard failure:

```python
raise UpdateError(
    f"cannot read the store schema version from {schema_file}: {e}")
```

Assuming 1 would re-run every migration against a store that may already be migrated.

Migrations are forward-only. `rollback` reinstalls the previous version's package and pops the history entry; it does not undo a migration.

## Updating

`check(config)` reads the published versions from PyPI's JSON API.

`PyPIUnreachable` is a distinct exception from "no newer version exists", and that distinction matters:

> Collapsing that into None would report "already up to date" to someone who is actually several releases behind.

A genuine 404 -- px0 not published on that index -- returns `None`. A network failure raises.

`run_update` upgrades in place through whichever mechanism installed px0. `detect_install_mechanism` picks pipx or pip, and the beta channel gets `--pip-args=--pre` or `--pre` respectively.

Then, in order: apply pending migrations, append to `.state/update-history.json`, restart a running daemon, and finish with a quick doctor pass whose summary is returned. An update that leaves the daemon running the old code, or leaves the store on an old schema, is not finished.

`maybe_check` is the cached once-a-day version that `cli._notify_update` calls after every successful command. It never raises. An update nudge is never worth breaking or delaying the command that triggered it.

The daemon does its own check weekly during nightly housekeeping, and advances `last_update_check` only on success, so a failure retries tomorrow rather than being skipped for a week.

## Doctor

Every check returns `{"ok": bool, "detail": str}`, and a check that can fail also returns a `fix` string: the concrete next step, phrased as something the user can run.

The fix is attached where the failure is detected rather than looked up by check name. The same check fails for different reasons -- a missing `qmd` binary and a version-drifted one need different commands -- and only the failing branch knows which.

`_harness_fix` is the clearest case. `not found` is an install or PATH problem, a timeout is a slow or hanging backend, and a non-zero exit is the backend itself refusing. Three different next steps, so they are not collapsed into one hint.

| Check | Asks |
| ----- | ---- |
| `credentials` | Is `credentials.toml` mode 0600 |
| `versions` | Does the version manifest open and query |
| `locks` | Is the store lock free, or is a run stuck holding it |
| `schema` | Does the store's schema match this binary's |
| `connections` | Is every Composio connection `ACTIVE` |
| `workflows` | Does every workflow file parse |
| `unreferenced_guidelines` | Which guidelines no workflow lists |
| `guideline_descriptions` | Which guidelines have no frontmatter description |
| `update` | Did the weekly check find something newer |
| `sync_hazard` | Is the store inside a folder-syncing service |
| `agent_loop` | Can the configured harness actually run the configured loop |
| `daemon` | Is it running |
| `harness` | Does the model backend respond to a trivial prompt |
| `index` | Is the retrieval index stale or misconfigured |
| `private_folder` | How much is the private folder holding back |

`--quick` skips the last four, which need a live subprocess or a filesystem walk. That is the set `run_update` runs after an upgrade.

`_check_daemon` is always `ok`. A stopped daemon is not an integrity failure; `px0 status` is where that becomes a problem, and only when something is actually scheduled.

`_check_guideline_descriptions` is worth calling out because it protects a mechanism rather than a file. A guideline with no description is invisible to `select_guidelines`, which reads nothing else. The file works; it will simply never be attached to the next workflow that needs it.

`px0 store verify` is the sibling command, covered in [part 2](02-store-and-config.md). Doctor asks whether the install is wired up; verify asks whether the store's contents hang together.

## The test suite

Forty test files under `tests/`, one per area, named for what they cover: `test_route_and_agent_loop.py`, `test_allowlist_refusal.py`, `test_brain_migration.py`.

Two fixtures in `conftest.py` do most of the isolation work.

### Nothing shells out to a real model

```python
@pytest.fixture(autouse=True)
def _no_real_harness_calls(monkeypatch, request):
```

Autouse, so it applies to everything. `harness.invoke` runs `claude -p` as a subprocess; anything reaching it unmocked turns a unit test into a live model call -- slow, non-deterministic, and dependent on whoever's machine is running the suite.

Both `invoke` and `invoke_detailed` are patched. Guarding only the former left the runner free to shell out to the real binary, because runs go through the latter.

The refusal message names the escape hatch, and the marker is declared in `pyproject.toml`:

```toml
markers = [
    "allow_harness: test may call harness.invoke (the real coding-agent subprocess)",
]
```

### Nothing depends on what is installed

Another fixture stubs `shutil.which` for `pdftotext`, `pandoc`, and `yt-dlp`, so the fallback extraction paths are what gets tested. Without it the tests would pass or fail depending on what happens to be installed on the machine running them.

`FakeComposio` in `conftest.py` answers the whole Composio REST surface the code touches: toolkits, auth configs, connected account links, status queries, and tool execution. That is what makes the connector paths testable without a network or an API key.

`quiet_spinner` silences `ui.spinner` so CLI-level assertions see only real output.

## Build and release

```
make test      pytest
make build     python -m build, after clearing dist/ and build/
make publish   build, then scripts/publish.sh
make clean
```

`VERSION` at the repository root is the single source of truth. `pyproject.toml` reads it through `[tool.setuptools.dynamic] version = {attr = "px0.__version__"}`.

`install.sh` is the curl-pipe-sh installer, and carries its own `--uninstall`.

## Generated reference

`scripts/gen_docs.py` walks every module under `px0/` with `ast` and emits each module docstring, top-level function signature, and class into `docs/reference.md`.

It uses `ast.unparse` rather than importing the package, so it never executes code and never needs dependencies installed. Nested helpers are skipped as implementation detail.

Run it after changing any docstring:

```shell
python scripts/gen_docs.py
```

## Optional dependencies

```toml
[project.optional-dependencies]
playlists = ["yt-dlp>=2024.1.1"]
```

The pattern throughout px0: the stock install works, and an external tool improves one path when present. Without `yt-dlp` a playlist enumerates only its first page; without poppler a PDF goes through `pypdf`; without pandoc a `.docx` goes through the stdlib zip reader.

One dependency pin carries a comment explaining itself, and it is worth reading as an example of what a pin comment should say:

```toml
# >=1.0, not >=0.6: the instance-based `.fetch()` this code calls did not
# exist before 1.0, and the 0.6 static `get_transcript` is gone in 1.x. A
# resolver landing on 0.6 would make every video ingest as a stub.
"youtube-transcript-api>=1.0",
```

## The end of the series

Back to the [index](index.md).
