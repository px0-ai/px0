"""The model backend: the user's coding agent CLI, shelled out to in
non-interactive mode (`harness_cmd` in config.toml, e.g. `claude -p`).
Text and tool-calls in, text out -- there is no direct-API backend."""

import errno
import json
import shlex
import shutil
import subprocess
import time
from dataclasses import dataclass, field

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


# Flags that make a harness report a structured envelope instead of bare text,
# and flags that make it narrate what it is doing. Only harnesses whose flags
# are verified against their own `--help` appear here: an entry that guessed
# wrong would break every run for that backend, where a missing entry only
# costs the extra detail. `capabilities()` is what everything else asks.
#
# The structured envelope matters for more than tidiness. Without it a run's
# cost is inferred from character counts (see `runner._tool_call_loop`); with
# it the harness hands over the token counts it was actually billed for.
STRUCTURED_FLAGS: dict[str, list[str]] = {
    "claude": ["--output-format", "json"],
}

VERBOSE_FLAGS: dict[str, list[str]] = {
    "claude": ["--verbose"],
}

# How to hand a harness an MCP server and tell it which of that server's tools
# it may use. Only harnesses whose flags are verified belong here, for the same
# reason as the tables above -- and the consequence of a missing entry is
# milder: px0 falls back to driving its own tool-call loop, which is what it
# did before this existed.
#
# `{config}` is replaced with a path to the MCP config file. Tool permissions
# are passed as one comma-separated value, in the `mcp__<server>__<tool>` form
# the client uses to name a server's tools.
MCP_FLAGS: dict[str, dict] = {
    "claude": {
        "config": ["--mcp-config", "{config}"],
        "allow": ["--allowedTools", "{tools}"],
        "tool_prefix": "mcp__px0__",
        "separator": ",",
    },
}


def supports_agent_loop(harness_cmd: str) -> bool:
    """Whether this harness can be handed tools and left to run its own loop."""
    return harness_name(harness_cmd) in MCP_FLAGS

# Harness commands observed to reject a flag we added, remembered for the life
# of the process so the downgrade is paid for once rather than on every turn of
# every run.
_UNSUPPORTED: set[tuple[str, str]] = set()

# What a CLI says when it does not know a flag. Matched case-insensitively
# against stderr to tell "this harness is older than the flag" apart from "the
# model call itself failed", which must not be silently retried as plain text.
_UNKNOWN_FLAG_MARKERS = (
    "unknown option", "unknown argument", "unknown flag",
    "unrecognized option", "unrecognised option", "unrecognized argument",
    "invalid option", "bad flag", "unexpected argument",
)


def harness_name(harness_cmd: str) -> str | None:
    """The known-harness name behind a resolved command, or None for a custom one.

    Matched on the command's first word, so `claude -p --model x` and a bare
    `claude` both answer "claude", and a path like `/usr/local/bin/claude` does
    too -- a user who pinned the binary by path still gets its flags.
    """
    first = (shlex.split(harness_cmd) or [""])[0]
    binary = first.rsplit("/", 1)[-1]
    return binary if binary in KNOWN_HARNESSES else None


def capabilities(harness_cmd: str) -> dict:
    """What extra reporting this harness command supports.

    Returns the structured-output and verbose flags to add, empty when the
    backend is one px0 has no verified flags for. A custom `harness_cmd`
    always lands here: px0 will not invent flags for a command it does not
    recognize, because the cost of guessing wrong is every run failing.
    """
    name = harness_name(harness_cmd)
    return {
        "name": name,
        "structured": list(STRUCTURED_FLAGS.get(name or "", [])),
        "verbose": list(VERBOSE_FLAGS.get(name or "", [])),
    }


@dataclass
class Reply:
    """One harness invocation, with everything worth recording about it.

    `invoke` returns only `text`, which is all a caller composing a prompt
    needs. A run's telemetry wants the rest: what the process printed on
    stderr, how long it took, and -- when the harness reported a structured
    envelope -- the token counts and cost it was actually billed, rather than
    px0's own estimate of them.
    """
    text: str
    raw_stdout: str = ""
    stderr: str = ""
    exit_code: int = 0
    elapsed_seconds: float = 0.0
    argv: list[str] = field(default_factory=list)
    output_format: str = "text"
    usage: dict | None = None
    meta: dict = field(default_factory=dict)


def _looks_like_unknown_flag(stderr: str) -> bool:
    """Whether a non-zero exit reads as "I do not know that flag" rather than a
    real failure. Deliberately conservative: a false positive would retry a
    genuinely failed model call as if the flag were at fault, and report the
    second failure instead of the first."""
    low = (stderr or "").lower()
    return any(marker in low for marker in _UNKNOWN_FLAG_MARKERS)


def _parse_structured(raw: str) -> tuple[str | None, dict | None, dict]:
    """Pulls the reply text, token usage, and run metadata out of a harness's
    structured envelope.

    Written to tolerate three shapes rather than one, because the envelope is
    another program's output and may change under us: a single JSON object, a
    JSON array, and newline-delimited JSON where the last object carrying a
    result is the answer. Anything it cannot read returns `(None, ...)`, and
    the caller falls back to treating stdout as plain text -- telemetry is
    never worth failing a run over.
    """
    raw = (raw or "").strip()
    if not raw:
        return None, None, {}

    objects: list[dict] = []
    try:
        parsed = json.loads(raw)
        objects = parsed if isinstance(parsed, list) else [parsed]
    except json.JSONDecodeError:
        for line in raw.splitlines():
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(obj, dict):
                objects.append(obj)

    objects = [o for o in objects if isinstance(o, dict)]
    if not objects:
        return None, None, {}

    # The answer is the last object that actually carries one: a stream ends
    # with its result, and the objects before it are progress narration.
    text = None
    carrier: dict = objects[-1]
    for obj in reversed(objects):
        for key in ("result", "response", "text", "content", "output"):
            value = obj.get(key)
            if isinstance(value, str) and value.strip():
                text, carrier = value, obj
                break
        if text is not None:
            break

    usage = None
    for obj in reversed(objects):
        if isinstance(obj.get("usage"), dict):
            usage = dict(obj["usage"])
            break
    if usage is not None:
        usage["reported"] = True

    meta: dict = {}
    for obj in objects:
        for key in ("session_id", "num_turns", "total_cost_usd", "model",
                    "duration_ms", "subtype", "is_error", "stop_reason"):
            if key in obj and obj[key] is not None:
                meta[key] = obj[key]
    if carrier.get("is_error"):
        meta["is_error"] = True
    return text, usage, meta


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


def _extra_flags(config: dict, harness_cmd: str) -> tuple[list[str], str]:
    """The reporting flags to add to this invocation, and the output format
    they ask for.

    `model.output_format` is "auto" by default, which means: use the structured
    envelope wherever px0 knows the flag for it, and plain text everywhere else.
    "text" pins it off for someone who would rather the harness behave exactly
    as they type it themselves.
    """
    caps = capabilities(harness_cmd)
    want_format = str(config_mod.get(config, "model.output_format", "auto") or "auto")
    verbose = bool(config_mod.get(config, "model.verbose", False))

    flags: list[str] = []
    output_format = "text"
    if want_format in ("auto", "json") and caps["structured"]:
        if (harness_cmd, "structured") not in _UNSUPPORTED:
            flags += caps["structured"]
            output_format = "json"
    if verbose and caps["verbose"] and (harness_cmd, "verbose") not in _UNSUPPORTED:
        flags += caps["verbose"]
    return flags, output_format


def agent_flags(harness_cmd: str, config_path: str, tool_names: list[str]) -> list[str]:
    """The flags that hand this harness an MCP server and its permitted tools.

    Returns an empty list for a harness px0 has no verified flags for, which is
    the caller's signal to drive its own loop instead.
    """
    spec = MCP_FLAGS.get(harness_name(harness_cmd) or "")
    if not spec:
        return []
    permitted = spec["separator"].join(
        f"{spec['tool_prefix']}{name}" for name in tool_names)
    flags = [f.replace("{config}", config_path) for f in spec["config"]]
    if permitted:
        flags += [f.replace("{tools}", permitted) for f in spec["allow"]]
    return flags


def invoke_detailed(config: dict, prompt: str, timeout: float = 120,
                    extra_flags: list[str] | None = None) -> Reply:
    """Shells out to the configured harness command (e.g. `claude -p`) and
    reports everything the call produced, not just its answer.

    A run's conversation grows with every tool result folded back in, and once
    the prompt is long enough the OS refuses to exec the command at all
    (`OSError: [Errno 7] Argument list too long`) well before any output limit
    does. That case is retried once with the same prompt piped to stdin and no
    positional argument instead, which is how `claude -p` and `pi -p` are
    documented to accept a piped prompt.

    Two things can go wrong with the reporting flags px0 adds, and neither may
    be allowed to cost the user a run:

    - The harness is older than the flag and exits complaining about it. That
      is retried once with the flags stripped, and the harness is remembered as
      not supporting them so the rest of the process skips straight to plain
      text.
    - The harness accepts the flag but prints something px0 cannot parse. The
      raw stdout is then used as the answer, exactly as in text mode.

    Raises HarnessError if the binary is missing, the call times out, or it
    exits non-zero for any reason other than a flag it did not recognize.
    """
    harness_cmd = resolve_harness_cmd(config_mod.get(config, "model.harness_cmd", "claude -p"))
    base = shlex.split(harness_cmd)
    flags, output_format = _extra_flags(config, harness_cmd)
    # Agent flags come last and are not part of the reporting downgrade below:
    # a harness that rejects `--mcp-config` cannot run the loop at all, and
    # silently retrying without it would run the workflow with no tools and
    # report success.
    agent = list(extra_flags or [])
    meta: dict = {}

    def _call(argv_prefix: list[str]) -> tuple[subprocess.CompletedProcess, list[str], float]:
        argv = argv_prefix + [prompt]
        started = time.monotonic()
        try:
            done = _run(argv, None, timeout, harness_cmd)
        except OSError as e:
            if e.errno != errno.E2BIG:
                raise HarnessError(f"failed to run harness {harness_cmd!r}: {e}") from e
            # Too long to pass as an argument: the same prompt, piped instead.
            argv = list(argv_prefix)
            done = _run(argv, prompt, timeout, harness_cmd)
            meta["stdin_prompt"] = True
        return done, argv, time.monotonic() - started

    result, argv, elapsed = _call(base + flags + agent)

    if result.returncode != 0 and flags and not agent and _looks_like_unknown_flag(result.stderr):
        # This harness predates the flags px0 added. Remember that, drop them,
        # and try once more -- a run must not fail over telemetry.
        for kind in ("structured", "verbose"):
            _UNSUPPORTED.add((harness_cmd, kind))
        meta["downgraded"] = "harness rejected px0's reporting flags; retried as plain text"
        output_format = "text"
        result, argv, elapsed = _call(base + agent)

    if result.returncode != 0:
        raise HarnessError(
            f"harness exited {result.returncode}: {result.stderr.strip()[:500]}"
        )

    raw = result.stdout
    text, usage = raw, None
    if output_format == "json":
        parsed_text, usage, parsed_meta = _parse_structured(raw)
        meta.update(parsed_meta)
        if parsed_text is None:
            # It answered in some shape px0 does not know. The text is still
            # the text; only the extra detail is lost.
            meta["unparsed_envelope"] = True
            output_format = "text"
        else:
            text = parsed_text

    return Reply(
        text=text, raw_stdout=raw, stderr=result.stderr or "",
        exit_code=result.returncode, elapsed_seconds=round(elapsed, 3),
        argv=argv[:-1] if argv and argv[-1:] == [prompt] else argv,
        output_format=output_format, usage=usage, meta=meta,
    )


class AgentLoopUnsupported(HarnessError):
    """Raised when a run asked for the harness's own agent loop and this
    harness has no verified way to be handed tools."""
    pass


def invoke(config: dict, prompt: str, timeout: float = 120) -> str:
    """The harness's answer as plain text.

    Kept as the whole interface for every caller that just wants a reply --
    the builder, `px0 ask`, brain summarization. Runs go through
    `invoke_detailed` instead, because a run records what the call cost.
    """
    return invoke_detailed(config, prompt, timeout=timeout).text
