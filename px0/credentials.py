"""Local credential storage: `.state/credentials.toml`, mode 0600, plain text
by design (see spec's Security posture)."""

from pathlib import Path

from px0 import config as config_mod
from px0 import paths, versioning


def load(home: Path) -> dict:
    path = paths.credentials_path(home)
    if not path.exists() or path.stat().st_size == 0:
        return {}
    with open(path, "rb") as f:
        import tomllib
        return tomllib.load(f)


def save(home: Path, creds: dict) -> None:
    path = paths.credentials_path(home)
    config_mod.save(path, creds)
    versioning.ensure_secure_permissions(path)


def set_service(home: Path, service: str, values: dict) -> None:
    creds = load(home)
    creds[service] = values
    save(home, creds)


def remove_service(home: Path, service: str) -> bool:
    creds = load(home)
    if service not in creds:
        return False
    del creds[service]
    save(home, creds)
    return True
