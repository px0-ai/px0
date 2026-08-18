# Phase 5: PyPI + pipx distribution and self-update

## Status quo this phase changes

- No `install.sh` exists anywhere in the repo. `README.md:14-24` installs px0 by cloning the repo and running `pip install -e .` -- a dev-only path.
- `px0/update.py:1-9` (module docstring): "The spec's self-update flow assumes a signed release manifest served from a real distribution channel. No such channel exists for this build... `update` and `update --check` report that plainly instead of fabricating a manifest fetch." `check()`/`run_update()` (`41-63`) always return `available_version: None`.
- `px0/cli.py:761-764`: `update rollback` always prints "rollback is not available" and exits `EXIT_USER_ERROR`.
- No store-schema migration mechanism exists anywhere; `.state/schema` (`px0/paths.py:67-69`) is written once at `init()` and never read except by `doctor._check_schema` (`px0/doctor.py:72-79`), which only compares, never migrates.
- `pyproject.toml` hardcodes `version = "0.1.0"` and `px0/__init__.py:7` separately hardcodes `__version__ = "0.1.0"` -- two sources of truth for the same fact.
- `px0/store.py:10` defines its own local `SCHEMA_VERSION = 1`, separate from `px0/__init__.py:8`'s `SCHEMA_VERSION = 1` -- a second duplicate-constant hazard: bumping the real schema version (in `__init__.py`, which `doctor.py`/`update.py` import) would silently leave `store.init()` stamping fresh stores with the stale value from `store.py`, since nothing currently keeps the two in sync.

## Why PyPI + pipx, and what it removes from spec's original flow (stated, not silently dropped)

Spec.md's installer (lines 46-69) detects OS/architecture to pick a matching signed binary. px0 has no compiled extensions (`pyproject.toml`'s `[tool.setuptools] packages = ["px0"]`, no C/Rust extension modules anywhere in the tree) -- a pure-Python wheel is already architecture-independent, and `pip`/`pipx` already resolve the correct wheel for the running Python. **OS/architecture detection and per-platform binary selection are not needed and are not built in this phase** -- this is a direct, structural consequence of staying pure Python, not an oversight. Checksum+signature verification against a pinned key is replaced by PyPI's own TLS-delivered package integrity plus (for later releases) GitHub Actions' Trusted Publishing OIDC flow, which is the standard, "boring" mechanism for this ecosystem -- not a custom signing scheme this phase would otherwise have to invent and maintain.

## Assumptions (stated explicitly)

1. **Distribution mechanism: PyPI package `px0`, installed via `pipx`.** Confirmed by the earlier scoping decision ("PyPI + pipx"). `pipx install px0` / `pipx upgrade px0` / `pipx uninstall px0` are pipx's own standard verbs (no px0-specific tooling needed for the mechanism itself).
2. **Publishing: `python -m build` + PyPI Trusted Publishing via GitHub Actions**, triggered on `v*` git tags. This is the current standard, credential-free publish path recommended for PyPI-hosted projects on GitHub. The very first publish (before the PyPI project exists) is a one-time manual step by whoever owns the PyPI account -- creating the project and configuring Trusted Publishing is a web-UI action on pypi.org that cannot be scripted from inside this repo, and is called out explicitly in Rollout below rather than glossed over.
3. **Version is a single source of truth: `px0/__init__.py:__version__`.** `pyproject.toml` switches to `dynamic = ["version"]` with `[tool.setuptools.dynamic] version = {attr = "px0.__version__"}` (standard, long-stable setuptools mechanism), and its hardcoded `version = "0.1.0"` line is removed. This phase also deletes `px0/store.py:10`'s duplicate `SCHEMA_VERSION` constant and makes `store.init()` import `SCHEMA_VERSION` from `px0/__init__.py` instead, closing the drift hazard described above.
4. **"Channel" maps to PyPI pre-release versions.** `update.channel = "beta"` installs/upgrades with `pipx install --pip-args="--pre" px0`; `"stable"` (default) installs the latest non-pre-release version. No separate release infrastructure is needed -- PyPI's own pre-release version semantics (`0.2.0b1`, etc.) already provide this distinction.
5. **`PX0_NO_RUNTIME` (spec.md:67) does not apply and is omitted.** Per Phase 2's scoping, px0 never installs qmd or a Bun/Node runtime itself -- there is nothing for this knob to skip. `install.sh` has one line of comment saying so, rather than silently dropping a spec-named knob without explanation.
6. **Rollback restores the binary only, not a reverse schema migration.** Spec.md:667 states migrations are forward-only. If the update being rolled back ran a migration, the restored old binary will detect `.state/schema` is newer than its own `SCHEMA_VERSION` via the existing `doctor._check_schema` (`px0/doctor.py:72-79`) and report a failing check -- this is the accepted, spec-consistent consequence of forward-only migrations, not a gap this phase silently leaves open.

## Engineering section

### Dependencies on prior phases

Depends on Phase 1 only for the shared pytest harness. Touches `px0/daemon.py` in the same file Phase 3 also touches (different functions -- `restart_if_running` here vs. `_log_event`/ingest-queue draining there), a merge-conflict risk noted in the index's Parallelization note, not a functional dependency. Independent of Phases 2, 3, 4, and 6.

### What already exists (reused, not rebuilt)

- `px0/daemon.py`'s `install()`/`systemd_unit()`/`launchd_plist()`/`crontab_block()` (`191-280`) -- `install.sh` calls `px0 daemon install` (the existing CLI command) rather than reimplementing unit-file generation.
- `px0/doctor.py`'s `_check_schema` (`72-79`) -- reused as-is; migrations write a new `.state/schema` value that this check already compares against.
- `px0/versioning.py`'s `record_change`/`FileChange` (used elsewhere as `versioning.record_change(home, actor, [FileChange(...)])`, e.g. `px0/builder.py:180-182`) -- reused so any files a migration touches are recorded as a change, per spec.md:667.
- `px0/harness.py`'s `HarnessError`-style "one clear exception type per failure class" convention -- mirrored for the new `UpdateError`.

### Components touched

| File | Change |
| --- | --- |
| `pyproject.toml` | `dynamic = ["version"]`, `[tool.setuptools.dynamic] version = {attr = "px0.__version__"}`; remove hardcoded `version = "0.1.0"`. Add `packaging` to `dependencies` (for correct semver comparison). Add project metadata needed for a real PyPI listing: `readme`, `license`, `classifiers`, `urls.Homepage`/`urls.Repository` (currently absent -- PyPI accepts a listing without these, but a real public package should have them; low-stakes, filled in directly). |
| `px0/__init__.py` | No change to `__version__`'s definition, just its role as sole source of truth (enforced by the pyproject.toml change above). |
| `px0/store.py` | Remove local `SCHEMA_VERSION = 1` (line 10); `import SCHEMA_VERSION` from `px0` (the package `__init__.py`) instead; `init()`'s use at line 87 is unchanged otherwise. |
| `px0/update.py` | Full rewrite: `check()`/`run_update()` become real (PyPI JSON API query, `packaging.version` comparison, pipx-vs-pip install-mechanism detection, migration runner). Add `MIGRATIONS: dict[int, Callable[[Path], list[versioning.FileChange]]]` (empty at this phase's completion -- see Rollout), `rollback()`, `_detect_install_mechanism(home)`, `_pypi_latest_version(channel)`. |
| `px0/daemon.py` | Add `restart_if_running(home, config)`: checks `status()`, sends `SIGTERM` + respawns if alive, no-op otherwise -- factored out of the inline logic already duplicated at `px0/cli.py:340-349` (daemon restart) so `update.py` can call it without importing `cli.py`. |
| `px0/paths.py` | Add `update_history_path(home)` -> `.state/update-history.json`. |
| `px0/cli.py` | `cmd_update` (`757-771`): wire the real `check()`/`run_update()`/`rollback()`. `cmd_daemon`'s `restart` branch (`340-349`): call the new `daemon.restart_if_running` instead of its inline duplicate (cleanup, not new behavior). |
| `install.sh` (new, repo root) | The installer script: bootstrap pipx if missing, `pipx install px0` (or a pinned version via `PX0_VERSION`), run `px0 init`, offer `px0 daemon install` interactively, print next steps. Honors `PX0_VERSION`, `PX0_CHANNEL`, `PX0_PREFIX` (mapped to pipx's `PIPX_BIN_DIR`), `PX0_NO_DAEMON`, and `--uninstall`. |
| `.github/workflows/publish.yml` (new) | Tag-triggered (`v*`) build + Trusted-Publishing upload to PyPI, using `pypa/gh-action-pypi-publish`. |
| `tests/test_update.py` (new) | Unit tests for version comparison, install-mechanism detection, and the migration runner against a fake PyPI JSON response. |

### Data model

`.state/update-history.json` (new, not versioned -- operational bookkeeping like `.state/schedule.json`):

```json
[
  { "from_version": "0.1.0", "to_version": "0.2.0", "at": "2026-08-18T09:00:00+00:00", "migrations_applied": [] }
]
```

Append-only list; `rollback()` reads the last entry's `from_version` as its target.

`px0/update.py`'s migration registry:

```python
MIGRATIONS: dict[int, Callable[[Path], list[versioning.FileChange]]] = {
    # 1: _migrate_v1_to_v2,   # populated by a future phase when the store layout actually changes
}
```

Kept empty in this phase -- there is no real schema-version-2 layout change to migrate to yet, and inventing a fake one to populate the dict would violate "don't design what already exists" in reverse (designing something nothing needs). The mechanism (below) is proven by a synthetic migration function defined only inside the test file, not shipped in `update.py`.

### API contract: PyPI JSON API (public, no auth, stable and long-documented)

`GET https://pypi.org/pypi/px0/json`

```json
{
  "info": { "version": "0.2.0" },
  "releases": { "0.1.0": [...], "0.2.0": [...], "0.2.0b1": [...] }
}
```

- `"stable"` channel: `info.version` (PyPI's own "latest stable" field, pre-releases already excluded by PyPI's own semantics).
- `"beta"` channel: the highest key in `releases` by `packaging.version.Version` ordering, pre-releases included.
- A 404 (package not yet published, e.g. before this phase's first real release) is treated as "no update available," not an error -- `check()` returns `available_version: None` with a message distinguishing "not yet published" from "up to date," so `px0 doctor` doesn't misreport a brand-new unpublished checkout as broken.

### Key flows

**`px0 update --check`:**

1. `check(config)` calls `_pypi_latest_version(channel)`.
2. Compares against `px0.__version__` via `packaging.version.Version(latest) > packaging.version.Version(px0.__version__)`.
3. Returns `{"channel", "current_version", "available_version", "message"}` -- same shape as today (`px0/update.py:41-52`), now with real values instead of a canned `None`.

**`px0 update` (apply):**

1. Same check as above; if no update available, prints "already up to date" and returns.
2. `_detect_install_mechanism(home)`: `shutil.which("pipx")` and, if found, `pipx list --json` parsed for an entry named `px0`; if present, mechanism is `"pipx"`. Otherwise, mechanism is `"pip"` (covers the `pip install -e .` dev path from `README.md`, and any non-pipx install) -- upgrade runs `sys.executable -m pip install --upgrade px0`.
3. Runs the upgrade subprocess (`pipx upgrade px0` or `pipx install --pip-args="--pre" --force px0` for beta / `pip install --upgrade px0`), capturing output; a non-zero exit raises `UpdateError` with the captured stderr.
4. Appends an entry to `.state/update-history.json` (`from_version` = the version this process started with, captured before step 3 -- the running process's own `__version__` is stale after step 3 swaps the installed package, which is exactly why it must be read *before*, not after).
5. Runs pending migrations: reads `.state/schema`, applies every `MIGRATIONS` key greater than the stored value up to the new `SCHEMA_VERSION`, in order, each wrapped in `versioning.record_change(home, "update", changes)`; writes the new value to `.state/schema` only after all pending migrations succeed (an interrupted migration leaves `.state/schema` at its pre-migration value, so a retry re-applies from the correct point rather than skipping a partially-applied step).
6. `daemon.restart_if_running(home, config)`.
7. `doctor.run(home, config, quick=True)`, printed as the "here's the result" confirmation (matches spec.md:668's "`px0 doctor --quick` confirms the result").

**`px0 update rollback`:**

1. Reads the last entry of `.state/update-history.json`; if empty, prints "nothing to roll back" and exits `EXIT_USER_ERROR` (replacing the current unconditional refusal at `px0/cli.py:761-764`).
2. Same install-mechanism detection as the apply flow, targeting `from_version` (`pipx install --force px0==<from_version>` / `pip install px0==<from_version>`).
3. Does **not** touch `.state/schema` (Assumption 6) -- prints a note if the rolled-back-to version's `SCHEMA_VERSION` (a constant baked into that specific old wheel, unknowable from the currently-running process) might be lower than what's on disk, directing the user to `px0 doctor` to confirm.
4. `daemon.restart_if_running(home, config)`.

**`install.sh` (new user, one command):**

1. `command -v pipx` -- if absent, `python3 -m pip install --user pipx && python3 -m pipx ensurepath`, matching pipx's own documented bootstrap.
2. `pipx install px0${PX0_VERSION:+==$PX0_VERSION}` (channel beta: add `--pip-args="--pre"`); `PX0_PREFIX` sets `PIPX_BIN_DIR` in the environment before this call.
3. `px0 init`.
4. Unless `PX0_NO_DAEMON` is set: prompt "Install the px0 scheduler daemon now? [y/N]"; on yes, run `px0 daemon install` and print its own returned `start_hint` (reusing `daemon.install()`'s existing return shape, `px0/daemon.py:263-265, 273-274, 277-278` -- no new daemon code).
5. Print what was installed, where (`pipx list` output for px0), and three next commands (`px0 list workflows`, `px0 doctor`, `px0 run pr-precheck --stdin < some.diff`), mirroring `cmd_init`'s existing "try next" block (`px0/cli.py:83-86`).

`install.sh --uninstall`: `pipx uninstall px0`; prints (does not run) `rm -rf ~/.px0` as the separate, explicit store-removal command, matching spec.md:67 exactly.

### Non-functional requirements

- `_pypi_latest_version` uses `timeout=10` (a public, typically-fast JSON endpoint; no existing latency budget in the codebase to match, so this follows the same "short timeout for a small JSON GET" convention already used at `px0/knowledge.py:142` for YouTube's oEmbed call).
- The daemon's existing weekly update check (`px0/daemon.py`'s nightly pass currently does not check for updates at all -- spec.md:581 says "the daemon checks weekly and surfaces an available update in `px0 doctor` and the runs TUI," which this phase's `run_nightly` does not yet call; wiring a weekly-not-nightly cadence into the daemon is a small addition: track a `last_update_check` timestamp in `.state/schedule.json` alongside per-workflow fire times, check it in `run_nightly`, call `update.check()` only if 7+ days have passed since the last check, and store the result where `px0 doctor` can read it (`.state/update-history.json`'s sibling, a new `.state/update-check.json` with `{"checked_at", "available_version"}`). This is included in this phase's scope since it's the mechanism spec.md:581 describes and `update.check()` already exists once this phase lands it -- not deferred.

### Failure modes

| Failure | Covered by test? | Error handling | Visible to caller? |
| --- | --- | --- | --- |
| PyPI unreachable during `check()` | Yes | `requests.RequestException` caught, `check()` returns `available_version: None` with a "could not reach PyPI" message, not a crash | Yes |
| `pipx upgrade` exits non-zero (e.g. dependency conflict) | Yes | `UpdateError` raised with captured stderr; `.state/update-history.json` is **not** appended (the entry records a completed upgrade, not an attempted one), so a failed attempt doesn't pollute rollback history with a version that was never actually running | Yes, CLI error |
| A migration function raises partway through a multi-migration run | Yes | `.state/schema` is not advanced past the last successfully-applied migration; the exception propagates as `UpdateError`; the next `px0 update` (or a manual re-run) resumes from the correct point since `MIGRATIONS` is re-scanned against the un-advanced `.state/schema` value | Yes |
| `rollback()` with no update history | Yes | Explicit "nothing to roll back" message, `EXIT_USER_ERROR`, replacing the old unconditional refusal | Yes |
| Both `pipx` and `pip` are unavailable (e.g. exotic environment) | No (would require simulating a broken Python env; documented as a known gap) | `_detect_install_mechanism` raises `UpdateError("neither pipx nor pip is available to perform the update")` before attempting any subprocess call | Yes |

### Test plan

Uses the pytest harness established in Phase 1. PyPI and `pipx`/`pip` subprocess calls are mocked (`requests` monkeypatched for the JSON API; `subprocess.run` monkeypatched for the install commands) -- no network or real package install in CI.

| Layer | What | Count |
| --- | --- | --- |
| Unit | `_pypi_latest_version` parses `info.version` for stable, highest pre-release for beta | +2 |
| Unit | Version comparison via `packaging.version` correctly orders `0.9.0 < 0.10.0` (regression against naive string comparison) | +1 |
| Unit | `_detect_install_mechanism` prefers pipx when `px0` appears in `pipx list --json` | +1 |
| Unit | `_detect_install_mechanism` falls back to pip otherwise | +1 |
| Unit | Migration runner applies migrations in order, advances `.state/schema` only after all succeed | +2 |
| Unit | Migration runner stops and leaves `.state/schema` unchanged on a mid-run failure | +1 |
| Unit | `rollback()` with empty history returns the "nothing to roll back" error | +1 |
| Integration | `px0 update` end-to-end (mocked) appends to `update-history.json`, calls `daemon.restart_if_running`, prints a doctor summary | +1 |
| Integration | `install.sh` (run in a throwaway container/venv in CI, not mocked -- the one place this phase tests against a real `pipx`) bootstraps pipx, installs, runs `px0 init` | +1 |

### Rollout

This phase ships `MIGRATIONS = {}` (no real migrations yet) and `SCHEMA_VERSION` staying at `1` -- there is nothing to migrate because the store layout hasn't changed. The mechanism itself is what's being delivered; it activates the first time a future phase actually changes the store layout and adds an entry to `MIGRATIONS`. Feature flag: none needed (this is infrastructure, not user-facing behavior toggle). Rollback of *this phase itself* (as opposed to `px0 update rollback`, the feature it ships): revert the commit; `.state/update-history.json` and `update-check.json` are additive files an older binary simply never reads.

**One explicitly manual, non-automatable step**, called out rather than hidden: creating the `px0` project on PyPI and configuring GitHub Actions as a Trusted Publisher happens once, by hand, on pypi.org, before `.github/workflows/publish.yml`'s first tag-triggered run can succeed. This phase delivers the workflow file and documents this one step in the PR description; it cannot be scripted from inside the repository.

## Product section

**Phase goal:** `curl -fsSL <install-script-url> | sh` is a real, working, one-command install, and `px0 update`/`px0 update rollback` do what their names say.

**User story:** a new user installs px0 without knowing or caring that it's Python underneath; an existing user runs `px0 update` and gets the next version with their daemon and schedule intact, or `px0 update rollback` if the new version misbehaves.

**In scope:**
- `install.sh`: pipx bootstrap, install, `px0 init`, daemon offer, all four spec-named env knobs that still apply (`PX0_VERSION`, `PX0_CHANNEL`, `PX0_PREFIX`, `PX0_NO_DAEMON`), `--uninstall`.
- `px0 update [--check|--channel]`: real PyPI-backed version check and upgrade.
- `px0 update rollback`: real, targeting the last successful update's prior version.
- Store-schema migration mechanism (empty registry, proven by tests), fixing the `SCHEMA_VERSION`/`version` duplicate-source-of-truth bugs found during this phase's own investigation.
- Weekly (not nightly) update-availability check surfaced in `px0 doctor`.

**Out of scope (deferred, no phase currently planned):**
- Checksum/signature verification against a pinned key (superseded by PyPI TLS + Trusted Publishing, per the stated distribution-model decision).
- `[update] auto_install` actually being read anywhere (it's already a config key, `px0/config.py:204-207`, but nothing currently checks it before an unattended update runs -- since this phase's `update.check()`/`run_update()` are only ever invoked manually via CLI or the new weekly daemon check-only path, not an unattended apply, wiring `auto_install` to actually trigger an unattended `run_update()` is a small follow-up once someone wants the daemon to auto-apply updates, not required to close this phase's stated gap).

**Acceptance criteria:**
1. `install.sh` run on a clean machine with neither pipx nor px0 installed results in a working `px0` command on `PATH` and an initialized store, verified by `px0 doctor` exiting 0 immediately after.
2. `px0 update --check` against a real (or mocked, in CI) PyPI response reports the correct `available_version` and does not change anything on disk.
3. `px0 update` against a newer mocked version: upgrades, appends to `update-history.json`, restarts a running daemon, and ends by printing a `doctor` summary.
4. `px0 update rollback` immediately after (3) restores the prior version and prints a schema-mismatch note if applicable.
5. `pyproject.toml`'s version and `px0/__init__.py:__version__` can never drift (single source of truth verified by a test that imports both and asserts equality after a build).

## Definition of done

- [ ] AC1-5 above pass.
- [ ] `pytest` green with the new tests; `install.sh`'s CI job (real pipx, throwaway environment) passes.
- [ ] `px0 version` (`px0/cli.py:774-780`, unchanged call site) reflects the single-source-of-truth version.
- [ ] PR description documents the one manual PyPI Trusted Publisher setup step.
