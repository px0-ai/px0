"""px0 doctor: credentials, daemon, harness, index, versions, locks, schema."""

import fcntl
import stat
from pathlib import Path

from px0 import __version__, SCHEMA_VERSION
from px0 import connect as connect_mod
from px0 import config as config_mod
from px0 import daemon, harness, paths, proposals, retrieval, versioning


def _check_credentials(home: Path) -> dict:
    """Verifies credentials.toml is mode 0600 (or absent, which is also fine)."""
    path = paths.credentials_path(home)
    if not path.exists():
        return {"ok": True, "detail": "no credentials file yet"}
    mode = stat.S_IMODE(path.stat().st_mode)
    ok = mode == 0o600
    return {"ok": ok, "detail": f"mode {oct(mode)}" + ("" if ok else ", expected 0600")}


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
        return {"ok": False, "detail": str(e)}


def _check_qmd_version(home: Path, config: dict) -> dict:
    """Runs qmd --version and compares against QMD_PINNED_VERSION."""
    try:
        out = retrieval._qmd_run(config, "--version")
        version = out.strip()
        pinned = retrieval.QMD_PINNED_VERSION
        if version != pinned:
            return {"ok": False, "detail": f"qmd version {version} does not match pinned {pinned}"}
        return {"ok": True, "detail": f"qmd version matches {pinned}"}
    except retrieval.RetrievalBackendError as e:
        return {"ok": False, "detail": f"qmd check failed: {e}"}


def _check_index(home: Path, config: dict) -> dict:
    """Flags a stale retrieval index: knowledge files exist but nothing is indexed."""
    backend = config_mod.get(config, "retrieval.backend", "local")
    if backend == "qmd":
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

    base = retrieval.knowledge_path(home, config)
    file_count = len(list(base.rglob("*.md"))) if base.exists() else 0
    indexed = retrieval.index_count(home)
    ok = indexed > 0 or file_count == 0
    return {"ok": ok, "detail": f"{file_count} knowledge files, {indexed} indexed passages"}


def _check_versions(home: Path) -> dict:
    """Confirms the version manifest database opens and is queryable."""
    try:
        conn = versioning.connect(home)
        conn.execute("SELECT COUNT(*) FROM versions")
        conn.close()
        return {"ok": True, "detail": "manifest opens cleanly"}
    except Exception as e:
        return {"ok": False, "detail": str(e)}


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
            return {"ok": False, "detail": "lock is held; a run may be stuck"}


def _check_schema(home: Path) -> dict:
    """Confirms the store's on-disk schema version matches this binary's SCHEMA_VERSION."""
    schema_file = paths.schema_path(home)
    if not schema_file.exists():
        return {"ok": False, "detail": "no .state/schema; store not initialized"}
    on_disk = int(schema_file.read_text().strip())
    return {"ok": on_disk == SCHEMA_VERSION,
            "detail": f"store schema {on_disk}, binary schema {SCHEMA_VERSION}"}


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
        return {
            "ok": False,
            "detail": "; ".join(issues),
            "connections": conns,
        }
    return {"ok": True, "detail": f"{len(conns)} connection(s) configured", "connections": conns}


def _check_unreferenced_guidelines(home: Path) -> dict:
    """Flags guideline files that no workflow lists, since they're inlined into
    prompts by reference and are otherwise dead weight."""
    files = proposals.unreferenced_guideline_files(home)
    return {"ok": len(files) == 0, "detail": f"{len(files)} unreferenced file(s)", "files": files}


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
        "unreferenced_guidelines": _check_unreferenced_guidelines(home),
    }
    if not quick:
        checks["daemon"] = _check_daemon(home, config)
        checks["harness"] = _check_harness(config)
        checks["index"] = _check_index(home, config)

    return {
        "px0_version": __version__,
        "all_ok": all(c["ok"] for c in checks.values()),
        "checks": checks,
    }
