"""px0 doctor: credentials, daemon, harness, index, versions, locks, schema.

Every check returns `{"ok": bool, "detail": str}`. A check that can fail also
returns a `"fix"` string on the failing branch: the concrete next step that
clears it, phrased as something the user can run. The fix is attached where the
failure is detected rather than looked up by check name, because the same check
fails for different reasons (a missing qmd binary and a version-drifted one need
different commands) and only the failing branch knows which.
"""

import fcntl
import stat
import re
from pathlib import Path

from px0 import __version__, SCHEMA_VERSION
from px0 import connect as connect_mod
from px0 import config as config_mod
from px0 import daemon, harness, paths, retrieval, versioning


def _check_credentials(home: Path) -> dict:
    """Verifies credentials.toml is mode 0600 (or absent, which is also fine)."""
    path = paths.credentials_path(home)
    if not path.exists():
        return {"ok": True, "detail": "no credentials file yet"}
    mode = stat.S_IMODE(path.stat().st_mode)
    if mode == 0o600:
        return {"ok": True, "detail": f"mode {oct(mode)}"}
    return {
        "ok": False,
        "detail": f"mode {oct(mode)}, expected 0600",
        "fix": f"tighten the permissions: chmod 600 {path}",
    }


def _check_daemon(home: Path, config: dict) -> dict:
    """Reports daemon status. Always ok: a stopped daemon isn't an integrity failure."""
    s = daemon.status(home, config)
    return {"ok": True, "detail": "running" if s["alive"] else "not running", **s}


def _check_harness(config: dict) -> dict:
    """Sends a trivial prompt to the model backend to confirm it responds."""
    try:
        harness.invoke(config, "reply with the single word: ok", timeout=20)
        return {"ok": True, "detail": "harness responded"}
    except harness.HarnessError as e:
        cmd = config_mod.get(config, "model.harness_cmd", "claude -p")
        return {"ok": False, "detail": str(e), "fix": _harness_fix(str(e), cmd)}


def _harness_fix(error: str, harness_cmd: str) -> str:
    """The fix for a harness failure, chosen by which way it failed.

    `not found` is an install/PATH problem, a timeout is usually a slow or
    hanging backend, and a non-zero exit is the backend itself refusing --
    three different next steps, so don't collapse them into one hint.
    """
    binary = harness.resolve_harness_cmd(harness_cmd).split()[0]
    if "not found" in error:
        return (f"install the {binary} CLI and make sure it is on PATH, or point px0 at "
                f"another one: px0 config set model.harness_cmd '<cmd>'")
    if "timed out" in error:
        return (f"check `{binary}` responds on its own (it may be waiting on a login or a "
                f"network call), then re-run px0 doctor")
    return (f"run `{harness_cmd} 'hi'` by hand to see the backend's own error -- usually "
            f"an expired login or an unavailable model")


def _check_qmd_version(home: Path, config: dict) -> dict:
    """Runs qmd --version and compares against QMD_PINNED_VERSION."""
    try:
        out = retrieval._qmd_run(config, "--version")
        # qmd prints "qmd 2.8.3 (facd35e)", so compare the parsed number, not the
        # whole line -- the raw string never equals a bare pinned version.
        m = re.search(r"(\d+\.\d+\.\d+(?:[-.\w]*)?)", out)
        if not m:
            return {"ok": False,
                    "detail": f"could not parse qmd version from {out.strip()!r}",
                    "fix": _qmd_install_fix()}
        version = m.group(1)
        pinned = retrieval.QMD_PINNED_VERSION
        if version != pinned:
            return {"ok": False,
                    "detail": f"qmd version {version} does not match pinned {pinned}",
                    "fix": _qmd_install_fix()}
        return {"ok": True, "detail": f"qmd version matches {pinned}"}
    except retrieval.RetrievalBackendError as e:
        return {"ok": False, "detail": f"qmd check failed: {e}", "fix": _qmd_install_fix()}


def _qmd_install_fix() -> str:
    """The fix for any qmd problem: install (or reinstall) the pinned build."""
    return f"install the pinned build: npm install -g @tobilu/qmd@{retrieval.QMD_PINNED_VERSION}"


def _check_index(home: Path, config: dict) -> dict:
    """Flags a stale or misconfigured qmd retrieval index."""
    v_check = _check_qmd_version(home, config)
    if not v_check["ok"]:
        return v_check
    # Also check model download consent status
    consent_path = paths.retrieval_consent_path(home)
    import json
    consented = False
    if consent_path.exists():
        try:
            data = json.loads(consent_path.read_text())
            consented = bool(data.get("qmd_embed_consented"))
        except Exception:
            pass
    consent_str = "semantic search consented" if consented else "semantic search not consented"
    return {"ok": True, "detail": f"qmd backend configured (version: {retrieval.QMD_PINNED_VERSION}, {consent_str})"}


def _check_private_folder(home: Path, config: dict) -> dict:
    """Reports how much the private folder is holding back from retrieval.

    Worth its own line because the exclusion is invisible in normal use: the
    default name, `work/`, means "never leaves this machine" to px0 and "my work
    notes" to every notes app, so a brain pointed at an existing vault can have
    a whole folder quietly missing from every search.
    """
    folder = retrieval.private_folder(config)
    if not folder:
        return {"ok": True, "detail": "no private folder configured"}

    base = retrieval.brain_path(home, config)
    target = base / folder
    if not target.is_dir():
        return {"ok": True, "detail": f"{folder}/ (none yet)"}

    globs = retrieval.ignore_globs(config)
    held = sum(
        1 for p in target.rglob("*.md")
        if not retrieval.is_ignored(str(p.relative_to(base)), globs)
    )
    detail = f"{folder}/ holds {held} file(s) back from retrieval"
    if held == 0:
        return {"ok": True, "detail": detail}
    # Not a failure: this is the folder doing its job. But say so out loud.
    return {"ok": True, "detail": detail + " -- by design; "
            f"`px0 config set brain.private_folder \"\"` if {folder}/ is ordinary notes"}


def _check_versions(home: Path) -> dict:
    """Confirms the version manifest database opens and is queryable."""
    try:
        conn = versioning.connect(home)
        conn.execute("SELECT COUNT(*) FROM versions")
        conn.close()
        return {"ok": True, "detail": "manifest opens cleanly"}
    except Exception as e:
        return {"ok": False, "detail": str(e),
                "fix": f"the manifest at {versioning.manifest_path(home)} will not open; move "
                       f"it aside and re-run `px0 init` to rebuild it (past versions in "
                       f"{versioning.objects_dir(home)} are kept, but their history is lost)"}


def _check_locks(home: Path) -> dict:
    """Checks the store lock is currently free, i.e. no run is stuck holding it."""
    lock = paths.lock_path(home)
    if not lock.exists():
        return {"ok": True, "detail": "no lock file yet"}
    with open(lock, "w") as f:
        try:
            # non-blocking acquire-then-release: fails immediately if another process holds it
            fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
            fcntl.flock(f, fcntl.LOCK_UN)
            return {"ok": True, "detail": "lock is free"}
        except OSError:
            return {"ok": False, "detail": "lock is held; a run may be stuck",
                    "fix": f"wait for the in-flight px0 command to finish, then re-run; if "
                           f"nothing is running, the holder died -- delete {lock}"}


def _check_schema(home: Path) -> dict:
    """Confirms the store's on-disk schema version matches this binary's SCHEMA_VERSION."""
    schema_file = paths.schema_path(home)
    if not schema_file.exists():
        return {"ok": False, "detail": "no .state/schema; store not initialized",
                "fix": f"initialize the store: px0 init {home}"}
    on_disk = int(schema_file.read_text().strip())
    detail = f"store schema {on_disk}, binary schema {SCHEMA_VERSION}"
    if on_disk == SCHEMA_VERSION:
        return {"ok": True, "detail": detail}
    # Which side is behind decides the fix: an old store migrates forward, but a
    # store written by a newer px0 cannot be migrated backwards -- that install
    # has to catch up instead.
    if on_disk < SCHEMA_VERSION:
        fix = "migrate the store forward: px0 update"
    else:
        fix = (f"this store was written by a newer px0 (schema {on_disk}); upgrade this "
               f"install with `px0 update`, and don't run writes against it until then")
    return {"ok": False, "detail": detail, "fix": fix}


def _check_connections(home: Path) -> dict:
    """Reports configured connections. Checks if any Composio connection is not ACTIVE."""
    conns = connect_mod.list_connections(home)
    issues = []
    for c in conns:
        if c.get("service") in ("gmail", "slack", "calendar"):
            status = c.get("status")
            if status != "ACTIVE":
                issues.append(f"{c['service']} connected_account is {status}, not ACTIVE -- finish the browser consent")

    if issues:
        stale = sorted({c["service"] for c in conns
                        if c.get("service") in ("gmail", "slack", "calendar")
                        and c.get("status") != "ACTIVE"})
        return {
            "ok": False,
            "detail": "; ".join(issues),
            "fix": f"re-authorize {', '.join(stale)}: the next `px0 workflows new` or `px0 workflows run` "
                   f"needs one prints a consent link -- open it and finish the browser flow",
            "connections": conns,
        }
    return {"ok": True, "detail": f"{len(conns)} connection(s) configured", "connections": conns}


def _check_unreferenced_guidelines(home: Path) -> dict:
    """Counts guideline files that no workflow lists under its `guidelines:`.

    Informational, never a failure. An unreferenced guideline is not broken --
    it is a convention nothing currently calls for, which is what a guideline
    written for a workflow that was since deleted looks like. Failing here would
    also mean a store is unhealthy for keeping a file it does not use.
    """
    from px0 import workflow as workflow_mod

    base = paths.guidelines_dir(home)
    all_files = {str(p.relative_to(base)) for p in base.rglob("*.md")}
    referenced: set[str] = set()
    for wf in workflow_mod.load_all(home).values():
        referenced.update(wf.guidelines)
    files = sorted(all_files - referenced)
    detail = f"{len(files)} unreferenced file(s)"
    if files:
        detail += " -- nothing inlines them; remove or link them from a workflow"
    return {"ok": True, "detail": detail, "files": files}


def _check_guideline_descriptions(home: Path) -> dict:
    """Counts guideline files with no `description` in their frontmatter.

    Informational, never a failure: the file still works, and a run that names it
    still inlines it. What it loses is discoverability -- `px0 workflows new`
    matches a new workflow against descriptions, so an undescribed guideline
    falls back to its first rule and will rarely be picked. Files written before
    frontmatter was the format are exactly this case.
    """
    from px0 import guidelines as guidelines_mod

    files = sorted(rel for rel, g in guidelines_mod.load_all(home).items()
                   if not g.described)
    detail = f"{len(files)} file(s) without a description"
    if files:
        detail += " -- they fall back to their first rule when a build looks for one"
    check = {"ok": True, "detail": detail, "files": files}
    if files:
        check["fix"] = ("add `name` and `description` frontmatter with "
                        "`px0 guidelines edit <name>`")
    return check


def _check_workflows(home: Path) -> dict:
    """Reports workflow files that fail to parse.

    A real failure, unlike unreferenced guidelines: an unparseable file is a
    workflow that will never run, and it used to take every other workflow
    command down with it. Naming the file here is the whole point -- yaml's own
    error reports the position as "<unicode string>".
    """
    from px0 import workflow as workflow_mod

    errors = workflow_mod.load_errors(home)
    if not errors:
        return {"ok": True, "detail": "all workflow files parse"}
    return {
        "ok": False,
        "detail": f"{len(errors)} unreadable workflow file(s)",
        "errors": errors,
        "fix": "fix the frontmatter in the file(s) listed, or move them out of workflows/",
    }


def _check_update(home: Path) -> dict:
    """Reports the newer version the daemon's weekly check found, if any.

    Informational, never a failure -- being a release behind is not a broken
    store. Reads what the nightly pass recorded rather than calling PyPI, so
    `doctor` stays offline-safe.
    """
    import json

    check_path = paths.update_check_path(home)
    if not check_path.exists():
        return {"ok": True, "detail": f"{__version__} (no update check recorded yet)"}
    try:
        data = json.loads(check_path.read_text())
    except (OSError, ValueError):
        return {"ok": True, "detail": f"{__version__} (update check unreadable)"}

    available = data.get("available_version")
    checked_at = (data.get("checked_at") or "")[:10]
    if available and available != __version__:
        return {"ok": True,
                "detail": f"{__version__}; {available} available as of {checked_at} "
                          "-- run `px0 update`"}
    return {"ok": True, "detail": f"{__version__} is current as of {checked_at}"}


def run(home: Path, config: dict, quick: bool = False) -> dict:
    """Runs all health checks and returns a report with per-check results plus an
    overall all_ok flag. quick=True skips the slower checks (daemon, harness,
    retrieval index) that need a live subprocess or filesystem walk."""
    checks = {
        "credentials": _check_credentials(home),
        "versions": _check_versions(home),
        "locks": _check_locks(home),
        "schema": _check_schema(home),
        "connections": _check_connections(home),
        "workflows": _check_workflows(home),
        "unreferenced_guidelines": _check_unreferenced_guidelines(home),
        "guideline_descriptions": _check_guideline_descriptions(home),
        "update": _check_update(home),
    }
    if not quick:
        checks["daemon"] = _check_daemon(home, config)
        checks["harness"] = _check_harness(config)
        checks["index"] = _check_index(home, config)
        checks["private_folder"] = _check_private_folder(home, config)

    return {
        "px0_version": __version__,
        "all_ok": all(c["ok"] for c in checks.values()),
        "checks": checks,
    }
