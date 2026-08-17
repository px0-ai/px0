"""The model backend: the user's coding agent CLI, shelled out to in
non-interactive mode (`harness_cmd` in config.toml, e.g. `claude -p`).
Text and tool-calls in, text out -- there is no direct-API backend."""

import shlex
import subprocess

from px0 import config as config_mod


class HarnessError(Exception):
    pass


def parse_duration(s: str) -> float:
    s = s.strip()
    if s.endswith("ms"):
        return float(s[:-2]) / 1000
    if s.endswith("s"):
        return float(s[:-1])
    if s.endswith("m"):
        return float(s[:-1]) * 60
    if s.endswith("h"):
        return float(s[:-1]) * 3600
    return float(s)


def invoke(config: dict, prompt: str, timeout: float = 120) -> str:
    harness_cmd = config_mod.get(config, "model.harness_cmd", "claude -p")
    cmd = shlex.split(harness_cmd) + [prompt]
    try:
        result = subprocess.run(
            cmd, capture_output=True, text=True, timeout=timeout
        )
    except FileNotFoundError as e:
        raise HarnessError(f"harness command not found: {harness_cmd!r} ({e})") from e
    except subprocess.TimeoutExpired as e:
        raise HarnessError(f"harness timed out after {timeout}s") from e
    if result.returncode != 0:
        raise HarnessError(
            f"harness exited {result.returncode}: {result.stderr.strip()[:500]}"
        )
    return result.stdout
