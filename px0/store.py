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

# What an export carries, and therefore what an import looks for.
EXPORT_CONTENT = ("workflows", "guidelines", "memory", "brain", "output",
                  "outputs", "tools")
EXPORT_STATE = ("versions", "schema", "schedule.json")


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
    the live file would leave the secret one change-log entry away. Blobs are
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
    for name in EXPORT_CONTENT:
        src = home / name
        if not src.exists():
            continue
        shutil.copytree(src, dest / name, dirs_exist_ok=True)

    if paths.config_path(home).exists():
        _redact_config(paths.config_path(home), dest / "config.toml")

    state_dest = dest / ".state"
    state_dest.mkdir(parents=True, exist_ok=True)
    for name in EXPORT_STATE:
        src = paths.state_dir(home) / name
        if not src.exists():
            continue
        target = state_dest / name
        if src.is_dir():
            shutil.copytree(src, target, dirs_exist_ok=True)
        else:
            shutil.copy2(src, target)

    _purge_versioned_config(state_dest)


class StoreError(Exception):
    """An import or a check could not proceed."""


def looks_like_export(src: Path) -> bool:
    """Whether `src` looks like something `px0 store export` produced."""
    if not src.is_dir():
        return False
    if (src / "config.toml").exists():
        return True
    return any((src / name).exists() for name in EXPORT_CONTENT)


def import_store(home: Path, src: Path, force: bool = False, merge: bool = False) -> dict:
    """Loads an exported store into `home`: the inverse of `export`.

    Three rules keep this from being a footgun:

    - An import into an existing store stops unless `merge` or `force` is
      given, because the alternative is silently overwriting the workflows
      someone is running.
    - `merge` keeps whatever is already there and adds only files the store
      does not have. `force` lets the import win on a collision.
    - Credentials are never read from an export -- it does not contain them --
      and the imported config.toml never overwrites a live one, so importing
      does not blank the API key on the machine you are importing into.

    Returns counts of what came in and what was skipped.
    """
    src = Path(src).expanduser()
    if not looks_like_export(src):
        raise StoreError(
            f"{src} does not look like a px0 export; expected config.toml or a "
            f"workflows/ directory (see `px0 store export`)")
    if is_initialized(home) and not (force or merge):
        raise StoreError(
            f"a store already exists at {home}; pass --merge to add what is missing "
            "or --force to let the import win on collisions")

    report = {"imported": [], "skipped": [], "files": 0, "skipped_files": 0}

    for name in EXPORT_CONTENT:
        source = src / name
        if not source.is_dir():
            continue
        for path in sorted(source.rglob("*")):
            if not path.is_file():
                continue
            target = home / name / path.relative_to(source)
            if target.exists() and not force:
                report["skipped_files"] += 1
                continue
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target)
            report["files"] += 1
        report["imported"].append(name)

    cfg_src = src / "config.toml"
    if cfg_src.exists():
        if not paths.config_path(home).exists():
            # A fresh store: take the exported config, but leave the redacted
            # key blank rather than writing an empty string over a default.
            shutil.copy2(cfg_src, paths.config_path(home))
            report["imported"].append("config.toml")
        else:
            report["skipped"].append("config.toml (kept the one already here)")

    state_src = src / ".state"
    state_dest = paths.state_dir(home)
    if state_src.is_dir():
        state_dest.mkdir(parents=True, exist_ok=True)
        for name in EXPORT_STATE:
            source = state_src / name
            if not source.exists():
                continue
            target = state_dest / name
            if target.exists() and not force:
                report["skipped"].append(f".state/{name}")
                continue
            if source.is_dir():
                shutil.copytree(source, target, dirs_exist_ok=True)
            else:
                shutil.copy2(source, target)
            report["imported"].append(f".state/{name}")

    if not paths.config_path(home).exists():
        init(home)
        report["imported"].append("scaffolding")

    return report


def verify(home: Path) -> dict:
    """Checks the store's own consistency, and says what to run for each problem found.

    Cheap, read-only, and separate from `px0 doctor`: doctor asks whether the
    install is wired up, this asks whether the store's contents still hang
    together -- every version blob present, every workflow parsing, every
    guideline a workflow references existing.
    """
    problems: list[dict] = []
    checks = 0

    checks += 1
    for path in sorted(paths.workflows_dir(home).rglob("*.md")) if paths.workflows_dir(home).exists() else []:
        try:
            from px0 import workflow as workflow_mod

            wf = workflow_mod.parse(path)
        except Exception as e:
            problems.append({"kind": "workflow", "detail": str(e),
                             "fix": f"edit {path.relative_to(home)}, or undo the change that broke it "
                                    f"with `px0 changes revert <id>`"})
            continue
        for name in wf.guidelines:
            if not (paths.guidelines_dir(home) / name).exists():
                problems.append({
                    "kind": "guideline", "detail": f"{wf.id} references missing guidelines/{name}",
                    "fix": f"restore guidelines/{name}, or rebuild the workflow with "
                           f"`px0 workflows edit {wf.id}` so it stops naming it",
                })

    manifest = paths.versions_dir(home) / "manifest.sqlite"
    if manifest.exists():
        checks += 1
        conn = sqlite3.connect(manifest)
        try:
            rows = conn.execute(
                "SELECT path, version, hash FROM versions WHERE hash IS NOT NULL").fetchall()
        finally:
            conn.close()
        objects = paths.versions_dir(home) / "objects"
        missing = [(r[0], r[1]) for r in rows
                   if not (objects / r[2][:2] / r[2]).exists()]
        for path, version in missing[:20]:
            problems.append({
                "kind": "blob", "detail": f"{path}@v{version} has no stored content",
                "fix": "that version's content is gone; the file on disk and its later "
                       "versions are unaffected",
            })
        if len(missing) > 20:
            problems.append({"kind": "blob",
                             "detail": f"and {len(missing) - 20} more missing version blobs",
                             "fix": "history this old cannot be read back; current content is intact"})

    from px0 import localtools

    _tools, tool_errors = localtools.load_user_tools(home)
    checks += 1
    for err in tool_errors:
        problems.append({"kind": "tool", "detail": err,
                         "fix": f"fix or remove the file in {paths.tools_dir(home)}"})

    return {"checks": checks, "problems": problems, "ok": not problems}


def init(home: Path, harness_cmd: str | None = None) -> list[str]:
    """Scaffold a store at `home`. If `harness_cmd` is given, it overrides
    the default `model.harness_cmd` in the generated config.toml (e.g. to
    point a fresh store at gemini, pi, or opencode instead of claude).
    Returns a list of human-readable lines describing what was created."""
    created: list[str] = []

    for d in (
        paths.workflows_dir(home),
        paths.guidelines_dir(home),
        home / "brain" / "docs",
        home / "brain" / "blogs",
        home / "brain" / "papers",
        # work/ is scaffolded like the rest: retrieval already treats it as the
        # never-leaves-this-machine folder, so it should exist to be filed into
        # rather than having to be guessed at and created by hand.
        home / "brain" / "work",
        paths.output_dir(home),
        # Scaffolded like guidelines/: px0 writes memories here as a side
        # effect of conversations, and a folder that appears the first time
        # that happens is one the user meets by surprise.
        paths.memory_dir(home),
        paths.state_dir(home),
        paths.index_dir(home),
        paths.ingest_dir(home),
        paths.tools_dir(home),
    ):
        d.mkdir(parents=True, exist_ok=True)
    created.append(f"store at {home}")

    # A worked example, with a suffix the loader ignores: copy it to
    # <name>.toml to make it real. Scaffolding a live tool into every store
    # would put a tool nobody asked for in `px0 tools list`.
    sample = paths.tools_dir(home) / "example.toml.sample"
    if not sample.exists():
        from px0 import localtools

        sample.write_text(localtools.EXAMPLE_TOOL)
        created.append("tools/example.toml.sample")

    cfg_path = paths.config_path(home)
    if not cfg_path.exists():
        initial_config = {k: dict(v) for k, v in config_mod.DEFAULTS.items()}
        initial_config["brain"]["path"] = str(home / "brain")
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
