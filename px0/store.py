"""px0 init: scaffold the store."""

import shutil
from pathlib import Path

from px0 import config as config_mod
from px0 import paths, starters, versioning, SCHEMA_VERSION
from px0.versioning import FileChange


def is_initialized(home: Path) -> bool:
    """Returns whether a store already exists at `home`, based on the
    presence of config.toml."""
    return paths.config_path(home).exists()


def export(home: Path, dest: Path) -> None:
    """Content plus version history, credentials excluded -- the supported
    way to move a store to another machine."""
    dest.mkdir(parents=True, exist_ok=True)
    for name in ("workflows", "guidelines", "knowledge", "outputs", "skills", "config.toml"):
        src = home / name
        if not src.exists():
            continue
        target = dest / name
        if src.is_dir():
            shutil.copytree(src, target, dirs_exist_ok=True)
        else:
            shutil.copy2(src, target)

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


def init(home: Path, harness_cmd: str | None = None) -> list[str]:
    """Scaffold a store at `home`. If `harness_cmd` is given, it overrides
    the default `model.harness_cmd` in the generated config.toml (e.g. to
    point a fresh store at gemini, pi, or opencode instead of claude).
    Returns a list of human-readable lines describing what was created."""
    created: list[str] = []

    for d in (
        paths.workflows_dir(home),
        paths.guidelines_dir(home) / "code-review",
        home / "knowledge" / "docs",
        home / "knowledge" / "blogs",
        home / "knowledge" / "papers",
        paths.outputs_dir(home),
        paths.skills_dir(home),
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
        initial_config["output"]["path"] = str(home / "outputs")
        if harness_cmd:
            initial_config["model"]["harness_cmd"] = harness_cmd
        config_mod.save(cfg_path, initial_config)
        created.append("config.toml")

    creds_path = paths.credentials_path(home)
    if not creds_path.exists():
        creds_path.write_text("")
        versioning.ensure_secure_permissions(creds_path)
        created.append(".state/credentials.toml (mode 0600)")

    schema_file = paths.schema_path(home)
    if not schema_file.exists():
        schema_file.write_text(str(SCHEMA_VERSION))

    file_changes = []  # track newly written starter files for the initial version snapshot
    for name, body in starters.WORKFLOWS.items():
        dest = paths.workflows_dir(home) / name
        if not dest.exists():
            dest.write_text(body)
            file_changes.append(FileChange(str(dest.relative_to(home)), body.encode()))
            created.append(f"workflows/{name}")
    for name, body in starters.GUIDELINES.items():
        dest = paths.guidelines_dir(home) / name
        dest.parent.mkdir(parents=True, exist_ok=True)
        if not dest.exists():
            dest.write_text(body)
            file_changes.append(FileChange(str(dest.relative_to(home)), body.encode()))
            created.append(f"guidelines/{name}")
    if cfg_path.exists():
        file_changes.append(
            FileChange(str(cfg_path.relative_to(home)), cfg_path.read_bytes())
        )

    versioning.record_change(home, "builder", file_changes)

    # Sync and update community skills
    skills_json = home / "skills.json"
    import shutil
    import subprocess
    agents_skill_lock = Path("~/.agents/.skill-lock.json").expanduser()

    if shutil.which("npx"):
        print("Updating AI skills...")
        try:
            # If we have a local backup, copy it to the global lock first to restore
            if skills_json.exists():
                agents_skill_lock.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy(str(skills_json), str(agents_skill_lock))
            
            # Update global skills
            subprocess.run(["npx", "--yes", "skills@latest", "update", "-g", "-y"], check=True)
            
            # Sync the final state back to .px0/skills.json
            if agents_skill_lock.exists():
                shutil.copy(str(agents_skill_lock), str(skills_json))
                created.append("skills synced to skills.json")
        except subprocess.CalledProcessError:
            created.append("warning: failed to sync/update skills using npx")
    else:
        created.append("warning: npx not found, skipping skill sync")

    return created
