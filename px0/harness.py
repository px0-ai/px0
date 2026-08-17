"""The model backend: the user's coding agent CLI, shelled out to in
non-interactive mode (`harness_cmd` in config.toml, e.g. `claude -p`).
Text and tool-calls in, text out -- there is no direct-API backend."""

import shlex
import subprocess

from px0 import config as config_mod

# Non-interactive invocation for each supported coding-agent CLI: a command
# prefix that, with the prompt appended as the final argument, prints the
# reply to stdout and exits. Verified against each CLI's own --help output.
KNOWN_HARNESSES: dict[str, str] = {
    "claude": "claude -p",
    "gemini": "gemini -p",
    "pi": "pi -p",
    "opencode": "opencode run",
}


class HarnessError(Exception):
    """Raised when the harness command is missing, times out, or exits non-zero."""
    pass


def resolve_harness_cmd(value: str) -> str:
    """Expands a known harness name (e.g. "gemini") to its full invocation
    command. A value that isn't a known name is returned unchanged, since
    `model.harness_cmd` also accepts an arbitrary literal command."""
    return KNOWN_HARNESSES.get(value.strip(), value)


def parse_duration(s: str) -> float:
    """Parses a duration string with an optional ms/s/m/h suffix into seconds.
    No suffix is treated as seconds."""
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
    """Shells out to the configured harness command (e.g. `claude -p`) with
    the prompt as its final argument and returns stdout.

    Raises HarnessError if the binary is missing, the call times out, or it
    exits non-zero."""
    harness_cmd = resolve_harness_cmd(config_mod.get(config, "model.harness_cmd", "claude -p"))
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
