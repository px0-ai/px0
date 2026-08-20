"""px0 update / px0 version: PyPI-backed version checks and self-update.

`check()` reads the published versions from PyPI's JSON API; `run_update()`
upgrades in place through whichever mechanism installed px0 (pipx or pip),
applies any pending store-schema MIGRATIONS, appends the result to
`.state/update-history.json`, restarts a running daemon, and finishes with
a quick doctor pass. `rollback()` reinstalls the last entry's from_version
and pops it; schema migrations are forward-only and are not undone.
"""

import json
import sys
import shutil
import subprocess
from pathlib import Path
from datetime import datetime, timezone
from typing import Callable, Any

import requests
from packaging import version

from px0 import __version__, SCHEMA_VERSION
from px0 import config as config_mod
from px0 import paths, harness, doctor
from px0 import daemon as daemon_mod


class UpdateError(Exception):
    """Raised when an update or rollback fails."""
    pass


# Registry for forward-only migrations
MIGRATIONS: dict[int, Callable[[Path], list[Any]]] = {
    # 1: _migrate_v1_to_v2,
}


def version_info(home: Path, config: dict) -> dict:
    """Reports installed px0/schema versions and whether the configured
    harness binary is actually on PATH."""
    schema_on_disk = None
    schema_file = paths.schema_path(home)
    if schema_file.exists():
        schema_on_disk = schema_file.read_text().strip()

    harness_cmd = harness.resolve_harness_cmd(config_mod.get(config, "model.harness_cmd", "claude -p"))
    harness_bin = harness_cmd.split()[0] if harness_cmd else None

    return {
        "px0_version": __version__,
        "schema_version_binary": SCHEMA_VERSION,
        "schema_version_store": schema_on_disk,
        "harness_cmd": harness_cmd,
        "harness_found": bool(harness_bin and shutil.which(harness_bin)),
    }


class PyPIUnreachable(UpdateError):
    """PyPI could not be queried. Distinct from "no newer version exists":
    reporting an unreachable index as "up to date" is a lie the user acts on."""


def _pypi_latest_version(channel: str) -> str | None:
    """The newest version published on `channel`, or None if px0 isn't on PyPI yet.

    Raises PyPIUnreachable when the index could not be read at all -- network
    down, proxy, TLS interception. Collapsing that into None would report
    "already up to date" to someone who is actually several releases behind.
    """
    url = "https://pypi.org/pypi/px0/json"
    try:
        resp = requests.get(url, timeout=10)
        if resp.status_code == 404:
            return None  # genuinely not published
        resp.raise_for_status()
        data = resp.json()
    except requests.RequestException as e:
        raise PyPIUnreachable(f"could not reach PyPI: {e}") from e
    except ValueError as e:
        raise PyPIUnreachable(f"PyPI returned a malformed response: {e}") from e

    if channel == "stable":
        return data.get("info", {}).get("version")

    # beta channel: highest release including pre-releases
    parsed_versions = []
    for ver_str in data.get("releases", {}):
        try:
            parsed_versions.append((version.parse(ver_str), ver_str))
        except version.InvalidVersion:
            continue  # PyPI can carry legacy non-PEP440 version strings
    if not parsed_versions:
        return None

    parsed_versions.sort()
    return parsed_versions[-1][1]


def check(config: dict) -> dict:
    """Reports update availability. Raises PyPIUnreachable if PyPI can't be read.

    The result always carries both keys, and they mean different things:
    `available_version` is the newest version published on the channel (None if
    px0 isn't published there at all), and `update_available` says whether that
    is newer than what's installed. Callers gate on `update_available` -- an
    earlier version of this returned `available_version: None` when current,
    which made "up to date" and "not published" indistinguishable.
    """
    channel = config_mod.get(config, "update.channel", "stable")
    latest = _pypi_latest_version(channel)
    if not latest:
        return {
            "channel": channel,
            "current_version": __version__,
            "available_version": None,
            "update_available": False,
            "message": f"px0 is not published on the {channel} channel yet.",
        }

    update_available = version.parse(latest) > version.parse(__version__)
    return {
        "channel": channel,
        "current_version": __version__,
        "available_version": latest,
        "update_available": update_available,
        "message": (f"Update available: {latest} on channel {channel}."
                    if update_available else "Already up to date."),
    }


def _load_history(path: Path) -> list:
    """The update history, or [] when it's missing or unreadable.

    An unreadable history costs a rollback target, not correctness, so it
    degrades rather than raising.
    """
    if not path.exists():
        return []
    try:
        loaded = json.loads(path.read_text())
    except (OSError, ValueError):
        return []
    return loaded if isinstance(loaded, list) else []


def _detect_install_mechanism(home: Path) -> str:
    """Detects whether px0 is installed via pipx or pip."""
    if shutil.which("pipx"):
        try:
            res = subprocess.run(["pipx", "list", "--json"], capture_output=True, text=True, timeout=15)
            if res.returncode == 0:
                data = json.loads(res.stdout)
                if "px0" in data.get("venvs", {}):
                    return "pipx"
        except Exception:
            pass
    return "pip"


def run_update(home: Path, config: dict, check_only: bool = False) -> dict:
    """Entry point for `px0 update`. Performs PyPI check and upgrades using pipx/pip."""
    result = check(config)
    if check_only:
        return result

    if not result.get("update_available"):
        return result  # nothing newer published; never "upgrade" to what's installed
    latest = result["available_version"]

    mechanism = _detect_install_mechanism(home)
    channel = result["channel"]

    history_path = paths.update_history_path(home)
    history = _load_history(history_path)

    # Run installer command
    if mechanism == "pipx":
        if channel == "beta":
            cmd = ["pipx", "install", "--pip-args=--pre", "--force", "px0"]
        else:
            cmd = ["pipx", "upgrade", "px0"]
    else:
        cmd = [sys.executable, "-m", "pip", "install", "--upgrade"]
        if channel == "beta":
            cmd.append("--pre")
        cmd.append("px0")

    try:
        sub_res = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
    except Exception as e:
        raise UpdateError(f"Upgrade subprocess error: {e}") from e
    if sub_res.returncode != 0:
        raise UpdateError(f"Install failed: {sub_res.stderr.strip()[:200]}")

    # Log successful update to history
    history_entry = {
        "from_version": __version__,
        "to_version": latest,
        "at": datetime.now(timezone.utc).isoformat(),
        "migrations_applied": []
    }

    # Run migrations
    schema_file = paths.schema_path(home)
    current_schema = 1
    if schema_file.exists():
        try:
            current_schema = int(schema_file.read_text().strip())
        except (OSError, ValueError) as e:
            # Assuming 1 here would re-run every migration against a store that
            # may already be migrated. Refuse instead.
            raise UpdateError(
                f"cannot read the store schema version from {schema_file}: {e}"
            ) from e

    applied_migrations = []
    for mig_ver in sorted(MIGRATIONS.keys()):
        if mig_ver > current_schema:
            try:
                from px0 import versioning
                changes = MIGRATIONS[mig_ver](home)
                versioning.record_change(home, "update", changes)
                current_schema = mig_ver
                schema_file.write_text(str(current_schema))
                applied_migrations.append(mig_ver)
            except Exception as e:
                raise UpdateError(f"Migration to v{mig_ver} failed: {e}")

    history_entry["migrations_applied"] = applied_migrations
    history.append(history_entry)
    history_path.write_text(json.dumps(history, indent=2))

    # Restart daemon
    daemon_mod.restart_if_running(home, config)

    # Confirmation via quick doctor
    doctor_res = doctor.run(home, config, quick=True)
    result["doctor_summary"] = doctor_res
    result["message"] = f"Successfully updated to {latest}."
    return result


def rollback(home: Path, config: dict) -> None:
    """Restores the previously installed px0 version from update history."""
    history_path = paths.update_history_path(home)
    history = _load_history(history_path)

    if not history:
        raise UpdateError("rollback is not available (no update history exists)")

    last_entry = history[-1]
    target_version = last_entry["from_version"]

    mechanism = _detect_install_mechanism(home)
    if mechanism == "pipx":
        cmd = ["pipx", "install", "--force", f"px0=={target_version}"]
    else:
        cmd = [sys.executable, "-m", "pip", "install", "--force-reinstall", f"px0=={target_version}"]

    try:
        sub_res = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
    except Exception as e:
        raise UpdateError(f"Rollback subprocess error: {e}") from e
    if sub_res.returncode != 0:
        raise UpdateError(f"Rollback install failed: {sub_res.stderr.strip()[:200]}")

    history.pop()
    history_path.write_text(json.dumps(history, indent=2))

    print(f"\nSuccessfully rolled back to px0 version {target_version}.")
    if last_entry.get("migrations_applied"):
        applied = ", ".join(f"v{m}" for m in last_entry["migrations_applied"])
        print(f"Note: schema migrations ({applied}) are forward-only and have NOT been "
              "rolled back; the restored version may see a newer store schema.")
    print("Run `px0 doctor` to confirm store layout integrity.")

    daemon_mod.restart_if_running(home, config)
