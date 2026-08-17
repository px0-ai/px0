"""Local credential storage: `.state/credentials.toml`, mode 0600, plain text
by design (see spec's Security posture)."""

from pathlib import Path

from px0 import config as config_mod
from px0 import paths, versioning


def load(home: Path) -> dict:
    """Reads all stored credentials keyed by service. Returns {} if the file
    is missing or empty (fresh store, nothing connected yet)."""
    path = paths.credentials_path(home)
    if not path.exists() or path.stat().st_size == 0:
        return {}
    with open(path, "rb") as f:
        import tomllib
        return tomllib.load(f)


def save(home: Path, creds: dict) -> None:
    """Writes the full credentials dict and re-asserts mode 0600 on the file."""
    path = paths.credentials_path(home)
    config_mod.save(path, creds)
    versioning.ensure_secure_permissions(path)


def set_service(home: Path, service: str, values: dict) -> None:
    """Stores/overwrites credentials for one service, leaving the rest untouched."""
    creds = load(home)
    creds[service] = values
    save(home, creds)


def remove_service(home: Path, service: str) -> bool:
    """Deletes a service's stored credentials. Returns False if it wasn't present."""
    creds = load(home)
    if service not in creds:
        return False
    del creds[service]
    save(home, creds)
    return True
