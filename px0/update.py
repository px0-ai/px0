"""px0 update / px0 version."""

import json
import os
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


def _pypi_latest_version(channel: str) -> str | None:
    """Queries PyPI JSON API and returns the latest available version for the channel."""
    url = "https://pypi.org/pypi/px0/json"
    try:
        resp = requests.get(url, timeout=10)
        if resp.status_code == 404:
            return None
        resp.raise_for_status()
        data = resp.json()
    except Exception:
        return None

    if channel == "stable":
        return data.get("info", {}).get("version")

    # beta channel: highest release including pre-releases
    releases = data.get("releases", {})
    if not releases:
        return None

    parsed_versions = []
    for ver_str in releases.keys():
        try:
            parsed_versions.append((version.parse(ver_str), ver_str))
        except Exception:
            pass

    if not parsed_versions:
        return None

    parsed_versions.sort()
    return parsed_versions[-1][1]


def check(config: dict) -> dict:
    """Reports update availability."""
    channel = config_mod.get(config, "update.channel", "stable")
    latest = _pypi_latest_version(channel)
    if not latest:
        return {
            "channel": channel,
            "current_version": __version__,
            "available_version": None,
            "message": "Package not published on PyPI or unreachable.",
        }

    current = version.parse(__version__)
    available = version.parse(latest)

    if available > current:
        msg = f"Update available: {latest} on channel {channel}."
    else:
        msg = "Already up to date."

    return {
        "channel": channel,
        "current_version": __version__,
        "available_version": latest if available > current else None,
        "message": msg,
    }


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

    latest = result["available_version"]
    if not latest:
        return result

    mechanism = _detect_install_mechanism(home)
    channel = result["channel"]

    history_path = paths.update_history_path(home)
    history = []
    if history_path.exists():
        try:
            history = json.loads(history_path.read_text())
        except Exception:
            pass

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
        if sub_res.returncode != 0:
            raise UpdateError(f"Install failed: {sub_res.stderr.strip()[:200]}")
    except Exception as e:
        raise UpdateError(f"Upgrade subprocess error: {e}")

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
        except Exception:
            pass

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
    history = []
    if history_path.exists():
        try:
            history = json.loads(history_path.read_text())
        except Exception:
            pass

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
        if sub_res.returncode != 0:
            raise UpdateError(f"Rollback install failed: {sub_res.stderr.strip()[:200]}")
    except Exception as e:
        raise UpdateError(f"Rollback subprocess error: {e}")

    history.pop()
    history_path.write_text(json.dumps(history, indent=2))

    print(f"\nSuccessfully rolled back to px0 version {target_version}.")
    print("Note: Store schema migrations are forward-only and have NOT been rolled back.")
    print("Run `px0 doctor` to confirm store layout integrity.")

    daemon_mod.restart_if_running(home, config)
