"""Store location and path helpers."""

import os
from pathlib import Path


def store_home() -> Path:
    """Resolves the store root: `$PX0_HOME` if set, else `~/.px0`."""
    return Path(os.environ.get("PX0_HOME", "~/.px0")).expanduser()


def workflows_dir(home: Path | None = None) -> Path:
    """Path to the versioned workflows folder under `home` (or the default store)."""
    return (home or store_home()) / "workflows"


def guidelines_dir(home: Path | None = None) -> Path:
    """Path to the versioned guidelines folder under `home` (or the default store)."""
    return (home or store_home()) / "guidelines"


def outputs_dir(home: Path | None = None) -> Path:
    """Path to the tool-managed outputs folder under `home` (or the default store)."""
    return (home or store_home()) / "outputs"


def skills_dir(home: Path | None = None) -> Path:
    """Path to the derived skills build output under `home` (or the default store)."""
    return (home or store_home()) / "skills"


def state_dir(home: Path | None = None) -> Path:
    """Path to `.state/`, the runtime-internal folder not meant for hand-editing."""
    return (home or store_home()) / ".state"


def versions_dir(home: Path | None = None) -> Path:
    """Path to the version history store under `.state/`."""
    return state_dir(home) / "versions"


def proposals_dir(home: Path | None = None) -> Path:
    """Path to pending guideline-edit proposals awaiting review."""
    return state_dir(home) / "proposals"


def index_dir(home: Path | None = None) -> Path:
    """Path to the retrieval index over `knowledge/`."""
    return state_dir(home) / "index"


def ingest_dir(home: Path | None = None) -> Path:
    """Path to the knowledge ingest queue/workspace."""
    return state_dir(home) / "ingest"


def credentials_path(home: Path | None = None) -> Path:
    """Path to `credentials.toml`, mode 0600, holding connector secrets."""
    return state_dir(home) / "credentials.toml"


def lock_path(home: Path | None = None) -> Path:
    """Path to the store's process lock file."""
    return state_dir(home) / "lock"


def schema_path(home: Path | None = None) -> Path:
    """Path to the file recording the store's on-disk schema version."""
    return state_dir(home) / "schema"


def schedule_path(home: Path | None = None) -> Path:
    """Path to the daemon's persisted scheduling state."""
    return state_dir(home) / "schedule.json"


def config_path(home: Path | None = None) -> Path:
    """Path to the store's versioned `config.toml`."""
    return (home or store_home()) / "config.toml"
