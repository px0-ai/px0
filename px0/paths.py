"""Store location and path helpers."""

import os
from pathlib import Path


def store_home() -> Path:
    return Path(os.environ.get("PX0_HOME", "~/.px0")).expanduser()


def workflows_dir(home: Path | None = None) -> Path:
    return (home or store_home()) / "workflows"


def guidelines_dir(home: Path | None = None) -> Path:
    return (home or store_home()) / "guidelines"


def outputs_dir(home: Path | None = None) -> Path:
    return (home or store_home()) / "outputs"


def skills_dir(home: Path | None = None) -> Path:
    return (home or store_home()) / "skills"


def state_dir(home: Path | None = None) -> Path:
    return (home or store_home()) / ".state"


def versions_dir(home: Path | None = None) -> Path:
    return state_dir(home) / "versions"


def proposals_dir(home: Path | None = None) -> Path:
    return state_dir(home) / "proposals"


def index_dir(home: Path | None = None) -> Path:
    return state_dir(home) / "index"


def ingest_dir(home: Path | None = None) -> Path:
    return state_dir(home) / "ingest"


def credentials_path(home: Path | None = None) -> Path:
    return state_dir(home) / "credentials.toml"


def lock_path(home: Path | None = None) -> Path:
    return state_dir(home) / "lock"


def schema_path(home: Path | None = None) -> Path:
    return state_dir(home) / "schema"


def schedule_path(home: Path | None = None) -> Path:
    return state_dir(home) / "schedule.json"


def config_path(home: Path | None = None) -> Path:
    return (home or store_home()) / "config.toml"
