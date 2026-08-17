"""px0 init: scaffold the store."""

from pathlib import Path

from px0 import config as config_mod
from px0 import paths, starters, versioning
from px0.versioning import FileChange

SCHEMA_VERSION = 1


def is_initialized(home: Path) -> bool:
    return paths.config_path(home).exists()


def init(home: Path) -> list[str]:
    """Scaffold a store at `home`. Returns a list of human-readable lines
    describing what was created."""
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
        config_mod.save(cfg_path, config_mod.DEFAULTS)
        created.append("config.toml")

    creds_path = paths.credentials_path(home)
    if not creds_path.exists():
        creds_path.write_text("")
        versioning.ensure_secure_permissions(creds_path)
        created.append(".state/credentials.toml (mode 0600)")

    schema_file = paths.schema_path(home)
    if not schema_file.exists():
        schema_file.write_text(str(SCHEMA_VERSION))

    file_changes = []
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

    return created
