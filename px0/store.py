"""px0 init: scaffold the store."""

import shutil
import sqlite3
from pathlib import Path

from px0 import config as config_mod
from px0 import paths, starters, versioning, SCHEMA_VERSION
from px0.versioning import FileChange


def is_initialized(home: Path) -> bool:
    """Returns whether a store already exists at `home`, based on the
    presence of config.toml."""
    return paths.config_path(home).exists()


# Config keys holding a secret. Excluding `.state/credentials.toml` from an
# export is not enough on its own: the Composio key is written to config.toml
# too, and config.toml is versioned, so the raw key also sits in the history
# blobs. Both have to be scrubbed or "credentials excluded" is a false promise.
SECRET_CONFIG_KEYS = ("connectors.composio_api_key",)


def _redact_config(src: Path, target: Path) -> None:
    """Copies config.toml with every secret key blanked."""
    config = config_mod.load(src)
    for key in SECRET_CONFIG_KEYS:
        if config_mod.get(config, key):
            config_mod.set_key(config, key, "")
    config_mod.save(target, config)


def _purge_versioned_config(state_dest: Path) -> None:
    """Drops config.toml's version history from an exported manifest and deletes
    the blobs it alone referenced.

    config.toml's history holds every value the key ever had, so redacting only
    the live file would leave the secret one `px0 versions show` away. Blobs are
    content-addressed and shared, so a blob is removed only when no surviving
    version still points at it.
    """
    manifest = state_dest / "versions" / "manifest.sqlite"
    if not manifest.exists():
        return

    conn = sqlite3.connect(manifest)
    try:
        doomed = {r[0] for r in conn.execute(
            "SELECT hash FROM versions WHERE path = 'config.toml' AND hash IS NOT NULL"
        )}
        conn.execute("DELETE FROM versions WHERE path = 'config.toml'")
        conn.execute("DELETE FROM files WHERE path = 'config.toml'")
        # A change row with no surviving versions is an empty entry in the log.
        conn.execute(
            "DELETE FROM changes WHERE id NOT IN (SELECT DISTINCT change_id FROM versions)"
        )
        still_used = {r[0] for r in conn.execute(
            "SELECT DISTINCT hash FROM versions WHERE hash IS NOT NULL"
        )}
        conn.commit()
    finally:
        conn.close()

    objects = state_dest / "versions" / "objects"
    for h in doomed - still_used:
        blob = objects / h[:2] / h
        blob.unlink(missing_ok=True)


def export(home: Path, dest: Path) -> None:
    """Content plus version history, credentials excluded -- the supported
    way to move a store to another machine.

    config.toml is exported redacted and its version history dropped, so the
    export carries no API key in either the live file or the blobs.
    """
    dest.mkdir(parents=True, exist_ok=True)
    for name in ("workflows", "guidelines", "knowledge", "output", "outputs", "skills"):
        src = home / name
        if not src.exists():
            continue
        shutil.copytree(src, dest / name, dirs_exist_ok=True)

    if paths.config_path(home).exists():
        _redact_config(paths.config_path(home), dest / "config.toml")

    state_dest = dest / ".state"
    state_dest.mkdir(parents=True, exist_ok=True)
    for name in ("versions", "proposals", "schema", "schedule.json"):
        src = paths.state_dir(home) / name
        if not src.exists():
            continue
        target = state_dest / name
        if src.is_dir():
            shutil.copytree(src, target, dirs_exist_ok=True)
        else:
            shutil.copy2(src, target)

    _purge_versioned_config(state_dest)


def init(home: Path, harness_cmd: str | None = None) -> list[str]:
    """Scaffold a store at `home`. If `harness_cmd` is given, it overrides
    the default `model.harness_cmd` in the generated config.toml (e.g. to
    point a fresh store at gemini, pi, or opencode instead of claude).
    Returns a list of human-readable lines describing what was created."""
    created: list[str] = []

    for d in (
        paths.workflows_dir(home),
        paths.guidelines_dir(home),
        home / "knowledge" / "docs",
        home / "knowledge" / "blogs",
        home / "knowledge" / "papers",
        paths.output_dir(home),
        paths.state_dir(home),
        paths.proposals_dir(home),
        paths.index_dir(home),
        paths.ingest_dir(home),
    ):
        d.mkdir(parents=True, exist_ok=True)
    created.append(f"store at {home}")

    cfg_path = paths.config_path(home)
    if not cfg_path.exists():
        initial_config = {k: dict(v) for k, v in config_mod.DEFAULTS.items()}
        initial_config["knowledge"]["path"] = str(home / "knowledge")
        initial_config["output"]["path"] = str(home / "output")
        if harness_cmd:
            initial_config["model"]["harness_cmd"] = harness_cmd
        config_mod.save(cfg_path, initial_config)
        created.append("config.toml")

    schema_file = paths.schema_path(home)
    if not schema_file.exists():
        schema_file.write_text(str(SCHEMA_VERSION))

    file_changes = []  # track newly written starter files for the initial version snapshot
    # Both starter sets are scaffolded the same way. GUIDELINES was previously
    # declared but never read, so any content added to it silently did nothing.
    for subdir, entries in (
        ("workflows", starters.WORKFLOWS),
        ("guidelines", starters.GUIDELINES),
    ):
        base = paths.workflows_dir(home) if subdir == "workflows" else paths.guidelines_dir(home)
        for name, body in entries.items():
            dest = base / name
            if dest.exists():
                continue
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_text(body)
            file_changes.append(FileChange(str(dest.relative_to(home)), body.encode()))
            created.append(f"{subdir}/{name}")
    if cfg_path.exists():
        file_changes.append(
            FileChange(str(cfg_path.relative_to(home)), cfg_path.read_bytes())
        )

    versioning.record_change(home, "builder", file_changes)

    return created
