"""Local credential storage: `.state/credentials.toml`, mode 0600, plain text
by design (see spec's Security posture)."""

from pathlib import Path

from px0 import config as config_mod
from px0 import paths, versioning


def load(home: Path) -> dict:
    """Reads all stored credentials keyed by service. Returns {} if the file
    is missing or empty (fresh store, nothing connected yet)."""
    path = paths.credentials_path(home)
    creds = {}
    if path.exists() and path.stat().st_size > 0:
        with open(path, "rb") as f:
            import tomllib
            creds = tomllib.load(f)

    # Dynamic fallback to load Composio API key from config.toml
    try:
        cfg_path = paths.config_path(home)
        if cfg_path.exists():
            config = config_mod.load(cfg_path)
            api_key = config.get("connectors", {}).get("composio_api_key")
            if api_key:
                creds.setdefault("composio", {})["api_key"] = api_key
    except Exception:
        pass

    return creds


def save(home: Path, creds: dict) -> None:
    """Writes the full credentials dict and re-asserts mode 0600 on the file."""
    path = paths.credentials_path(home)
    path.parent.mkdir(parents=True, exist_ok=True)
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
