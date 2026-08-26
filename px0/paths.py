"""Store location and path helpers."""

import os
from pathlib import Path


def store_home() -> Path:
    """Resolves the store root: `$PX0_HOME` if set, else `~/.px0`."""
    return Path(os.environ.get("PX0_HOME", "~/.px0")).expanduser()


def display(path: Path | str) -> str:
    """A path the way a person reads it: `~/.px0/output/daily.md`.

    Shown instead of the full absolute form, which is mostly the reader's own
    home directory repeated on every row, and instead of a bare store-relative
    path, which does not say which store it is relative to. Still a path anyone
    can paste into an editor, since the shell expands the `~`.

    Left absolute when it is not under the home directory -- a `PX0_HOME`
    somewhere else, or an output path that escaped the store.
    """
    p = Path(path)
    try:
        rel = p.relative_to(Path.home())
    except ValueError:
        return str(p)
    # `relative_to` answers "." for the home directory itself, which would read
    # as `~/.` -- a path that works and looks like a mistake.
    return "~" if str(rel) == "." else f"~/{rel}"


def workflows_dir(home: Path | None = None) -> Path:
    """Path to the versioned workflows folder under `home` (or the default store)."""
    return (home or store_home()) / "workflows"


def guidelines_dir(home: Path | None = None) -> Path:
    """Path to the versioned guidelines folder under `home` (or the default store)."""
    return (home or store_home()) / "guidelines"


def memory_dir(home: Path | None = None) -> Path:
    """Path to the versioned memory folder: what px0 knows about the user."""
    return (home or store_home()) / "memory"


def output_dir(home: Path | None = None) -> Path:
    """Path to the tool-managed output folder under `home` (or the default store)."""
    return (home or store_home()) / "output"


def outputs_dir(home: Path | None = None) -> Path:
    """Alias for output_dir."""
    return output_dir(home)


def tools_dir(home: Path | None = None) -> Path:
    """Where user-declared tools live: one TOML file per tool."""
    return (home or store_home()) / "tools"


def state_dir(home: Path | None = None) -> Path:
    """Path to `.state/`, the runtime-internal folder not meant for hand-editing."""
    return (home or store_home()) / ".state"


def versions_dir(home: Path | None = None) -> Path:
    """Path to the version history store under `.state/`."""
    return state_dir(home) / "versions"


def index_dir(home: Path | None = None) -> Path:
    """Path to the retrieval index over `brain/`."""
    return state_dir(home) / "index"


def ingest_dir(home: Path | None = None) -> Path:
    """Path to the brain ingest queue/workspace."""
    return state_dir(home) / "ingest"


def ingest_failed_dir(home: Path | None = None) -> Path:
    """Path to the directory holding failed brain ingest jobs."""
    return ingest_dir(home) / "failed"


def credentials_path(home: Path | None = None) -> Path:
    """Path to `credentials.toml`, mode 0600, holding connector authorizations."""
    return state_dir(home) / "credentials.toml"


def lock_path(home: Path | None = None) -> Path:
    """Path to the store's process lock file."""
    return state_dir(home) / "lock"


def schema_path(home: Path | None = None) -> Path:
    """Path to the file recording the store's on-disk schema version."""
    return state_dir(home) / "schema"


def update_history_path(home: Path | None = None) -> Path:
    """Path to `.state/update-history.json` recording update history."""
    return state_dir(home) / "update-history.json"


def update_check_path(home: Path | None = None) -> Path:
    """Path to `.state/update-check.json` recording last update availability check."""
    return state_dir(home) / "update-check.json"


def schedule_path(home: Path | None = None) -> Path:
    """Path to the daemon's persisted scheduling state."""
    return state_dir(home) / "schedule.json"


def config_path(home: Path | None = None) -> Path:
    """Path to the store's versioned `config.toml`."""
    return (home or store_home()) / "config.toml"


def retrieval_consent_path(home: Path | None = None) -> Path:
    """Path to `.state/retrieval-consent.json` recording model download consent."""
    return state_dir(home) / "retrieval-consent.json"
