"""The model backend: the user's coding agent CLI, shelled out to in
non-interactive mode (`harness_cmd` in config.toml, e.g. `claude -p`).
Text and tool-calls in, text out -- there is no direct-API backend."""

import errno
import shlex
import shutil
import subprocess

from px0 import config as config_mod

# Non-interactive invocation for each supported coding-agent CLI: a command
# prefix that, with the prompt appended as the final argument, prints the
# reply to stdout and exits. Verified against each CLI's own --help output.
# `claude` and `pi` also accept the prompt piped to stdin in place of that
# argument (confirmed by running each, not just reading its --help); `invoke`
# falls back to that when the argument form is too big for the OS to exec.
KNOWN_HARNESSES: dict[str, str] = {
    "claude": "claude -p",
    "gemini": "gemini -p",
    "pi": "pi -p",
    "opencode": "opencode run",
}

# What to tell the user when a harness fails to respond, most likely because
# it isn't authenticated yet. Each of these CLIs manages its own auth and
# model choice -- px0 has no direct-API backend and never stores a
# provider key itself -- so this only points at that CLI's own setup path.
AUTH_HINTS: dict[str, str] = {
    "claude": "authenticate it by running `claude` once (OAuth login), or set ANTHROPIC_API_KEY",
    "gemini": "set GEMINI_API_KEY, or run `gemini` once to authenticate interactively",
    "pi": "set the provider's API key env var, or pass --api-key (see `pi --help`)",
    "opencode": "run `opencode auth login`, or set a provider API key env var "
                "(e.g. ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY)",
}


class HarnessError(Exception):
    """Raised when the harness command is missing, times out, or exits non-zero."""
    pass


def installed_harnesses() -> dict[str, bool]:
    """Reports, for each name in KNOWN_HARNESSES, whether its binary is
    found on PATH right now."""
    return {name: shutil.which(cmd.split()[0]) is not None for name, cmd in KNOWN_HARNESSES.items()}


def with_model(harness_cmd: str, model: str | None) -> str:
    """Appends a `--model <name>` flag to a harness command. All four known
    harnesses accept `--model` for non-interactive model selection (verified
    against each CLI's own docs); a custom command gets the same flag
    appended on the same convention. Returns `harness_cmd` unchanged if
    `model` is falsy."""
    if not model:
        return harness_cmd
    return f"{harness_cmd} --model {shlex.quote(model)}"


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


def _run(cmd: list[str], input_text: str | None, timeout: float,
         harness_cmd: str) -> subprocess.CompletedProcess:
    """Runs one harness subprocess, translating its failure modes into HarnessError."""
    try:
        return subprocess.run(
            cmd, input=input_text, capture_output=True, text=True, timeout=timeout
        )
    except FileNotFoundError as e:
        raise HarnessError(f"harness command not found: {harness_cmd!r} ({e})") from e
    except subprocess.TimeoutExpired as e:
        raise HarnessError(f"harness timed out after {timeout}s") from e


def invoke(config: dict, prompt: str, timeout: float = 120) -> str:
    """Shells out to the configured harness command (e.g. `claude -p`) with
    the prompt as its final argument and returns stdout.

    A run's conversation grows with every tool result folded back in, and
    once the prompt is long enough the OS refuses to exec the command at all
    (`OSError: [Errno 7] Argument list too long`) well before any output
    limit does. That case is retried once with the same prompt piped to
    stdin and no positional argument instead, which is how `claude -p` and
    `pi -p` are documented to accept a piped prompt -- if the configured
    harness doesn't support that either, the retry fails too and surfaces as
    an ordinary HarnessError instead of a second crash.

    Raises HarnessError if the binary is missing, the call times out, or it
    exits non-zero."""
    harness_cmd = resolve_harness_cmd(config_mod.get(config, "model.harness_cmd", "claude -p"))
    cmd = shlex.split(harness_cmd)
    try:
        result = _run(cmd + [prompt], None, timeout, harness_cmd)
    except OSError as e:
        if e.errno != errno.E2BIG:
            raise HarnessError(f"failed to run harness {harness_cmd!r}: {e}") from e
        result = _run(cmd, prompt, timeout, harness_cmd)
    if result.returncode != 0:
        raise HarnessError(
            f"harness exited {result.returncode}: {result.stderr.strip()[:500]}"
        )
    return result.stdout
