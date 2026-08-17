"""px0 update / px0 version.

The spec's self-update flow assumes a signed release manifest served from a
real distribution channel. No such channel exists for this build (there is
no px0.sh release infrastructure to check against), so `update` and
`update --check` report that plainly instead of fabricating a manifest
fetch against a URL nobody verified. Everything else here -- reading the
installed component versions -- is real.
"""

import shutil
import subprocess
from pathlib import Path

from px0 import __version__, SCHEMA_VERSION
from px0 import config as config_mod
from px0 import harness
from px0 import store


def version_info(home: Path, config: dict) -> dict:
    """Reports installed px0/schema versions and whether the configured
    harness binary is actually on PATH."""
    schema_on_disk = None
    schema_file = home / ".state" / "schema"
    if schema_file.exists():
        schema_on_disk = schema_file.read_text().strip()

    harness_cmd = config_mod.get(config, "model.harness_cmd", "claude -p")
    harness_bin = harness_cmd.split()[0] if harness_cmd else None

    return {
        "px0_version": __version__,
        "schema_version_binary": SCHEMA_VERSION,
        "schema_version_store": schema_on_disk,
        "harness_cmd": harness_cmd,
        "harness_found": bool(harness_bin and shutil.which(harness_bin)),
    }


def check(config: dict) -> dict:
    """Reports update availability. Always says no manifest exists in this
    build rather than fabricating a version check."""
    return {
        "channel": config_mod.get(config, "update.channel", "stable"),
        "current_version": __version__,
        "available_version": None,
        "message": (
            "no release channel is configured in this build; there is no "
            "px0.sh manifest to check against. Update this checkout with "
            "your normal package/version-control workflow instead."
        ),
    }


def run_update(config: dict, check_only: bool = False) -> dict:
    """Entry point for `px0 update`. With check_only, same as check(); otherwise
    still performs no action, since there's no manifest to update against."""
    result = check(config)
    if check_only:
        return result
    result["message"] += " `px0 update` performs no action here."
    return result
