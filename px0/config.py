"""config.toml read/write. Read via stdlib tomllib; write with a small
hand-rolled writer since the schema is a fixed, shallow set of tables."""

import tomllib
from pathlib import Path
from typing import Any

DEFAULTS: dict[str, Any] = {
    "model": {
        "harness_cmd": "claude -p",
        "output_format": "auto",
        "verbose": False,
        "agent_loop": "builtin",
    },
    "brain": {
        "path": "~/.px0/brain",
        "private_folder": "work",
        "ignore": ["*.excalidraw.md"],
    },
    "output": {
        "path": "~/.px0/output",
    },
    "connectors": {
        "provider": "composio",
        "retries": 3,
        "composio_api_key": "",
        "ca_bundle": "",
    },
    "logs": {
        "path": "/var/log/px0",
        "retention_days": 14,
        "retention_days_failed": 60,
        "record_retention_days": 365,
        "max_file_size_mb": 20,
        "events": True,
    },
    "update": {
        "channel": "stable",
        "check": True,
        "auto_install": False,
    },
    "tools": {
        "allow_shell": False,
        "confirm_writes": False,
        "file_roots": [],
        "http_timeout": 20,
        "max_output_bytes": 20000,
    },
    "notify": {
        "on_failure": "",
        "on_approval": "",
        "channel": "",
        "target": "",
    },
    "runs": {
        "max_attempts": 1,
        "retry_backoff_seconds": 30,
        "max_tool_turns": 12,
        "disable_after_failures": 5,
        "capture_inputs": False,
        "fixture_keep_days": 14,
        "daily_budget_usd": 0.0,
        "daily_token_budget": 0,
    },
    "approvals": {
        "expire_days": 7,
        "keep_resolved_days": 30,
        "reply_tool": "",
        "reply_args": "",
        "reply_from": [],
        "reply_text_field": "",
        "reply_sender_field": "",
    },
    "inbox": {
        "auto": True,
        "keep_days": 30,
    },
    "ask": {
        "session_days": 7,
    },
    "memory": {
        "enabled": True,
        "budget_chars": 4000,
    },
    "schedule": {
        "timezone": "",
    },
    "retrieval": {
        "qmd_cmd": "qmd",
        "k_default": 5,
        "rerank": True,
    },
}


def _toml_value(v: Any) -> str:
    """Formats a Python value as a TOML scalar literal: bool, int, list
    (recursively), or a quoted/escaped string for anything else."""
    if isinstance(v, bool):  # must precede the int check: bool is a subclass of int
        return "true" if v else "false"
    if isinstance(v, (int, float)):
        # Floats included: without this a budget of 5.0 was written as the
        # string "5.0" and read back as one, so every comparison against it
        # was a comparison with text.
        return repr(v) if isinstance(v, float) else str(v)
    if isinstance(v, list):
        return "[" + ", ".join(_toml_value(x) for x in v) + "]"
    s = str(v).replace("\\", "\\\\").replace('"', '\\"')
    return f'"{s}"'


def dumps(config: dict[str, Any]) -> str:
    """Serializes a config dict to TOML text: root-level scalars first,
    then each top-level table (recursing into nested dicts as dotted
    `[a.b]` table headers). Assumes a shallow, well-formed structure --
    not a general-purpose TOML writer."""
    lines: list[str] = []
    # scalar/list keys at root first (none expected, but keep it general)
    root_scalars = {k: v for k, v in config.items() if not isinstance(v, dict)}
    for k, v in root_scalars.items():
        lines.append(f"{k} = {_toml_value(v)}")
    if root_scalars:
        lines.append("")

    def emit_table(prefix: str, table: dict[str, Any]) -> None:
        """Emits a `[prefix]` table header and its scalar keys, then
        recurses into nested tables using dotted prefixes. Mutates the
        enclosing `lines` list."""
        scalars = {k: v for k, v in table.items() if not isinstance(v, dict)}
        subtables = {k: v for k, v in table.items() if isinstance(v, dict)}
        lines.append(f"[{prefix}]")
        for k, v in scalars.items():
            lines.append(f"{k} = {_toml_value(v)}")
        lines.append("")
        for k, v in subtables.items():
            emit_table(f"{prefix}.{k}", v)

    for k, v in config.items():
        if isinstance(v, dict):
            emit_table(k, v)

    return "\n".join(lines).rstrip() + "\n"


def _deep_merge(base: dict[str, Any], overlay: dict[str, Any]) -> dict[str, Any]:
    """Recursively merges overlay onto base, returning a new dict; overlay
    values win. Nested dicts are merged key-by-key rather than replaced
    outright, but a non-dict overlay value replaces the base value as-is."""
    out = dict(base)
    for k, v in overlay.items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _deep_merge(out[k], v)
        else:
            out[k] = v
    return out


def load(path: Path) -> dict[str, Any]:
    """Loads config from `path`, deep-merged on top of DEFAULTS so any keys
    missing on disk fall back to their default values. Returns a fresh
    copy of DEFAULTS if `path` doesn't exist yet."""
    if not path.exists():
        return {k: dict(v) for k, v in DEFAULTS.items()}
    with open(path, "rb") as f:
        on_disk = tomllib.load(f)
    return _deep_merge(DEFAULTS, on_disk)


def save(path: Path, config: dict[str, Any]) -> None:
    """Writes `config` to `path` as TOML, overwriting any existing file."""
    path.write_text(dumps(config))


def get(config: dict[str, Any], dotted_key: str, default: Any = None) -> Any:
    """Looks up a dotted config key (e.g. "logs.path"), returning `default`
    if any segment along the path is missing or not a dict."""
    node: Any = config
    for part in dotted_key.split("."):
        if not isinstance(node, dict) or part not in node:
            return default
        node = node[part]
    return node


# Metadata for every recognized dotted key: its scalar type, the values it's
# restricted to (None if free-form), and a one-line description of what it
# does and how fully this build actually wires it up. Drives `px0 config
# get/set/list` validation and listing; load()/save() stay schema-agnostic.
SCHEMA: dict[str, dict[str, Any]] = {
    "model.harness_cmd": {
        "type": str, "choices": None,
        "help": "coding agent CLI invocation, e.g. 'claude -p'; a known harness name "
                "(claude, gemini, pi, opencode) expands to its full command, or pass any "
                "literal command. `px0 config model` sets this interactively.",
    },
    "model.output_format": {
        "type": str, "choices": ["auto", "text", "json"],
        "help": "whether to ask the harness for a structured envelope around its reply. "
                "\"auto\" (the default) uses one wherever px0 knows the flag for that "
                "backend, which is what makes a run's token counts real numbers rather "
                "than px0's own estimate; \"text\" runs the harness exactly as typed",
    },
    "model.agent_loop": {
        "type": str, "choices": ["builtin", "auto", "mcp"],
        "help": "who drives the tool calls. \"builtin\" (the default) is px0's own loop, "
                "capped at runs.max_tool_turns; \"mcp\" hands the workflow's tools to the "
                "harness over MCP and lets it run its own loop, which removes that cap and "
                "is what a workflow needing many steps wants; \"auto\" does that wherever "
                "px0 has verified flags for the configured harness and falls back otherwise",
    },
    "model.verbose": {
        "type": bool, "choices": None,
        "help": "ask the harness to narrate what it is doing, and keep what it prints on "
                "stderr in the run log -- useful when a workflow is behaving oddly and "
                "the reply alone does not say why",
    },
    "brain.path": {
        "type": str, "choices": None,
        "help": "directory the brain lives in -- point it at an Obsidian vault (or any "
                "folder of Markdown) and px0 reads what is already there; dot-folders "
                "like .obsidian/ and .trash/ are skipped",
    },
    "brain.private_folder": {
        "type": str, "choices": None,
        "help": "brain subfolder withheld from retrieval and never sent anywhere; "
                "set to \"\" to disable, or rename it if your vault already has a "
                "folder by this name that you do want searched",
    },
    "brain.ignore": {
        "type": list, "choices": None,
        "help": "glob patterns never indexed, on top of the always-skipped dot-folders",
    },
    "output.path": {
        "type": str, "choices": None,
        "help": "default directory for workflow file outputs",
    },
    "connectors.provider": {
        "type": str, "choices": ["composio", "native"],
        "help": "intended default for brokering tool connections; not yet enforced in this "
                "build -- every toolkit currently routes through Composio",
    },
    "connectors.retries": {
        "type": int, "choices": None,
        "help": "per-run transient connector retries, exponential backoff",
    },
    "connectors.composio_api_key": {
        "type": str, "choices": None,
        "help": "Composio API key used to authenticate external app connections",
    },
    "connectors.ca_bundle": {
        "type": str, "choices": None,
        "help": "CA bundle used to verify TLS for every outbound request; set automatically when an "
                "intercepting proxy (e.g. Zscaler) makes certifi's bundle insufficient",
    },
    "logs.path": {
        "type": str, "choices": None,
        "help": "run log directory, kept outside the versioned store",
    },
    "logs.retention_days": {
        "type": int, "choices": None,
        "help": "days to keep logs for successful runs",
    },
    "logs.retention_days_failed": {
        "type": int, "choices": None,
        "help": "days to keep logs for failed runs",
    },
    "logs.record_retention_days": {
        "type": int, "choices": None,
        "help": "days to keep run records (outlives the logs themselves)",
    },
    "logs.max_file_size_mb": {
        "type": int, "choices": None,
        "help": "single log file rotation size cap, in MB",
    },
    "logs.events": {
        "type": bool, "choices": None,
        "help": "write each run's structured event stream (one JSON object per turn, tool "
                "call, and outcome), which is what `px0 runs events` prints and what "
                "`px0 workflows health` reads; ages out with the run logs",
    },
    "update.channel": {
        "type": str, "choices": ["stable", "beta"],
        "help": "release channel; not functionally checked in this build -- "
                "`px0 update` reports that no release manifest is configured",
    },
    "update.check": {
        "type": bool, "choices": None,
        "help": "whether px0 checks for an available update -- once a day from the "
                "CLI, and weekly from the daemon if installed",
    },
    "update.auto_install": {
        "type": bool, "choices": None,
        "help": "install updates automatically (including from the CLI's daily check) "
                "instead of only surfacing them",
    },
    "tools.allow_shell": {
        "type": bool, "choices": None,
        "help": "allow the shell.run tool, which executes an arbitrary command locally; "
                "off by default because a workflow that can run a shell can do anything "
                "you can",
    },
    "tools.confirm_writes": {
        "type": bool, "choices": None,
        "help": "hold every write tool call for approval before it fires, across every "
                "workflow -- a workflow's own confirm: overrides this in both directions. "
                "The drafted call waits in `px0 approvals` with its arguments shown in full",
    },
    "tools.file_roots": {
        "type": list, "choices": None,
        "help": "extra directories the file.read and file.write tools may touch, on top of "
                "the store itself; a path outside every root is refused",
    },
    "tools.http_timeout": {
        "type": int, "choices": None,
        "help": "seconds before the http.get and http.post tools give up on a request",
    },
    "tools.max_output_bytes": {
        "type": int, "choices": None,
        "help": "cap on how much text a local tool returns to the model, so one large file "
                "or chatty script cannot fill the prompt",
    },
    "notify.on_failure": {
        "type": str, "choices": ["", "none", "desktop", "tool"],
        "help": "what happens when a scheduled run fails: \"desktop\" raises a local "
                "notification, \"tool\" sends through notify.channel, \"none\" (the "
                "default) stays silent",
    },
    "notify.on_approval": {
        "type": str, "choices": ["", "none", "desktop", "tool"],
        "help": "how you hear that an unattended run drafted a write and is waiting for "
                "your approval; empty follows notify.on_failure, so a store that already "
                "said how it wants to hear about failures does not say it twice",
    },
    "notify.channel": {
        "type": str, "choices": None,
        "help": "tool id used for failure notifications when notify.on_failure is \"tool\", "
                "e.g. slack.post_message or gmail.send_message",
    },
    "notify.target": {
        "type": str, "choices": None,
        "help": "where the failure notification goes: a Slack channel for "
                "slack.post_message, an address for gmail.send_message",
    },
    "runs.max_attempts": {
        "type": int, "choices": None,
        "help": "how many times a run is attempted before it is recorded as failed; a "
                "workflow's own retry.max_attempts overrides this",
    },
    "runs.retry_backoff_seconds": {
        "type": int, "choices": None,
        "help": "seconds to wait before the second attempt, doubling for each attempt after",
    },
    "runs.max_tool_turns": {
        "type": int, "choices": None,
        "help": "how many tool-call turns one run may take in px0's own loop; a run that "
                "needs more stops short with its work half done. Ignored when "
                "model.agent_loop is 'mcp', where the harness runs its own loop",
    },
    "runs.capture_inputs": {
        "type": bool, "choices": None,
        "help": "keep what every run's inputs resolved to, so a revision can be replayed "
                "against the same data. Off by default and deliberately so: a fixture is "
                "the content of your work. A workflow's own capture: overrides this",
    },
    "runs.fixture_keep_days": {
        "type": int, "choices": None,
        "help": "days a captured fixture is kept; it is the only place the content of a "
                "run's inputs is written down, so this window is short",
    },
    "runs.disable_after_failures": {
        "type": int, "choices": None,
        "help": "park an unattended workflow after this many consecutive failures of the "
                "same cause, announced through the notify channel; 0 lets it keep trying. "
                "A manual run never trips it -- you are there, reading the error",
    },
    "runs.daily_budget_usd": {
        "type": float, "choices": None,
        "help": "stop unattended runs once the day's measured cost reaches this; 0 is off. "
                "Only counts what the harness reported, so it needs model.output_format to "
                "be asking for it. Manual runs are never blocked",
    },
    "runs.daily_token_budget": {
        "type": int, "choices": None,
        "help": "the same ceiling against px0's own token estimate, for a harness that "
                "reports no costs; 0 is off",
    },
    "approvals.expire_days": {
        "type": int, "choices": None,
        "help": "days a drafted write call stays approvable before it goes stale -- a "
                "message written on Tuesday should not be sendable on Friday; 0 never expires",
    },
    "approvals.keep_resolved_days": {
        "type": int, "choices": None,
        "help": "days to keep approvals that were sent, rejected, or expired",
    },
    "approvals.reply_tool": {
        "type": str, "choices": None,
        "help": "read-only tool polled for replies that approve or reject a drafted call, "
                "e.g. slack.read_channel or gmail.search_messages; empty means approvals "
                "can only be answered at the terminal",
    },
    "approvals.reply_args": {
        "type": str, "choices": None,
        "help": "JSON arguments for approvals.reply_tool, e.g. {\"channel\": \"#ops\"}",
    },
    "approvals.reply_from": {
        "type": list, "choices": None,
        "help": "who may answer by reply -- usernames or addresses, comma-separated. "
                "Required: without it, anyone who can post in that channel could empty "
                "your approval queue",
    },
    "approvals.reply_text_field": {
        "type": str, "choices": None,
        "help": "which field of the reply tool's result holds the message text; empty "
                "tries the usual names (text, body, message, snippet)",
    },
    "approvals.reply_sender_field": {
        "type": str, "choices": None,
        "help": "which field holds who sent it; empty tries the usual names "
                "(user, from, sender, author)",
    },
    "inbox.auto": {
        "type": bool, "choices": None,
        "help": "deliver scheduled and watched runs to the inbox automatically; a workflow's "
                "own output.inbox overrides this either way",
    },
    "inbox.keep_days": {
        "type": int, "choices": None,
        "help": "days to keep inbox entries you have read or archived; unread entries are "
                "never dropped",
    },
    "ask.session_days": {
        "type": int, "choices": None,
        "help": "days a conversation is kept under .state/ before it is pruned; what was "
                "worth keeping from it is in memory/ by then. 0 keeps them indefinitely",
    },
    "memory.enabled": {
        "type": bool, "choices": None,
        "help": "inline what px0 remembers about you into every run's prompt; off keeps the "
                "memory/ folder and stops reading it",
    },
    "memory.budget_chars": {
        "type": int, "choices": None,
        "help": "how much remembered text a single run may inline, so a store that has been "
                "running for a year does not turn every prompt into a biography",
    },
    "schedule.timezone": {
        "type": str, "choices": None,
        "help": "zone every schedule is read against, e.g. 'Asia/Kolkata'; a workflow's own "
                "trigger.timezone wins. Empty follows the machine, which is right until the "
                "machine travels or the clocks change",
    },
    "retrieval.qmd_cmd": {
        "type": str, "choices": None,
        "help": "command prefix used to run the qmd CLI",
    },
    "retrieval.k_default": {
        "type": int, "choices": None,
        "help": "default number of passages retrieved per query",
    },
    "retrieval.rerank": {
        "type": bool, "choices": None,
        "help": "rescore retrieved passages by how much of the query each one covers, "
                "before trimming to k; local arithmetic, no model call",
    },
}


def _coerce(key: str, raw: str) -> Any:
    """Validates `key` against SCHEMA and converts the raw string `raw` (as
    typed on a command line) into the key's real type, checking choices
    where the key restricts them. Raises ValueError with a message meant to
    be printed as-is on a bad key, type, or choice."""
    spec = SCHEMA.get(key)
    if spec is None:
        raise ValueError(f"unknown config key: {key!r} (see `px0 config list`)")
    if spec["choices"] is not None and raw not in spec["choices"]:
        raise ValueError(f"{key} must be one of {spec['choices']}, got {raw!r}")
    t = spec["type"]
    if t is bool:
        low = raw.strip().lower()
        if low not in ("true", "false"):
            raise ValueError(f"{key} expects true or false, got {raw!r}")
        return low == "true"
    if t is int:
        try:
            return int(raw)
        except ValueError:
            raise ValueError(f"{key} expects an integer, got {raw!r}") from None
    if t is float:
        try:
            return float(raw)
        except ValueError:
            raise ValueError(f"{key} expects a number, got {raw!r}") from None
    if t is list:
        # A comma-separated list is what fits on a command line. Without this the
        # raw string was stored whole, so a multi-pattern value silently became
        # one nonsense pattern that matched nothing.
        return [item.strip() for item in raw.split(",") if item.strip()]
    return raw


def get_key(config: dict[str, Any], key: str) -> Any:
    """Looks up a SCHEMA-known dotted key. Raises ValueError for a key not
    in SCHEMA, unlike the more permissive `get`."""
    if key not in SCHEMA:
        raise ValueError(f"unknown config key: {key!r} (see `px0 config list`)")
    return get(config, key)


def set_key(config: dict[str, Any], key: str, raw: str) -> Any:
    """Validates and coerces `raw` per SCHEMA, then writes it into `config`
    at `key` (mutating the nested tables in place) and returns the coerced
    value. Does not save to disk -- callers persist via `save`."""
    value = _coerce(key, raw)
    node = config
    parts = key.split(".")
    for part in parts[:-1]:
        node = node.setdefault(part, {})
    node[parts[-1]] = value
    return value


def default_for(key: str) -> Any:
    """The built-in default for a SCHEMA key, as a Python value.

    Raises ValueError for an unknown key, matching `get_key`.
    """
    if key not in SCHEMA:
        raise ValueError(f"unknown config key: {key!r} (see `px0 config list`)")
    node: Any = DEFAULTS
    for part in key.split("."):
        if not isinstance(node, dict) or part not in node:
            return None
        node = node[part]
    return node


def unset_key(config: dict[str, Any], key: str) -> Any:
    """Removes a key's stored override so it falls back to its default.

    Mutates `config` in place, drops the parent table when it empties, and
    returns the default the key now resolves to. Unsetting a key that was
    never set is not an error: the result is the same either way.
    """
    if key not in SCHEMA:
        raise ValueError(f"unknown config key: {key!r} (see `px0 config list`)")
    parts = key.split(".")
    node = config
    trail = []
    for part in parts[:-1]:
        if not isinstance(node, dict) or part not in node:
            return default_for(key)
        trail.append((node, part))
        node = node[part]
    if isinstance(node, dict):
        node.pop(parts[-1], None)
    for parent, part in reversed(trail):
        if isinstance(parent.get(part), dict) and not parent[part]:
            parent.pop(part)
    return default_for(key)


def key_help(include_choices: bool = True) -> str:
    """Every settable key as an aligned block, for a subcommand's --help epilog.

    Keys only -- type, and the allowed values where a key restricts them. The
    descriptions and each key's *current* value live in `px0 config list`, which
    needs a loaded store; --help must render without one, and a 22-key listing
    with wrapped help for each would bury the usage text above it.

    Grouped by the leading section so the shape of the config is visible: the
    dotted keys mirror the TOML tables they are written to.
    """
    width = max(len(k) for k in SCHEMA)
    lines = ["config keys:"]
    section = None
    for key, spec in SCHEMA.items():
        head = key.split(".")[0]
        if head != section:
            lines.append("")
            section = head
        suffix = spec["type"].__name__
        if include_choices and spec["choices"]:
            suffix += "  " + "|".join(spec["choices"])
        lines.append(f"  {key.ljust(width)}  {suffix}")
    lines += ["", "`px0 config list` adds each key's description, current value, and default."]
    return "\n".join(lines)


def describe(config: dict[str, Any]) -> list[dict[str, Any]]:
    """Returns one entry per SCHEMA key: its current value, default, type
    name, allowed choices (or None), and help text. Used by `px0 config
    list`."""
    return [
        {
            "key": key,
            "value": get(config, key),
            "default": get(DEFAULTS, key),
            "type": spec["type"].__name__,
            "choices": spec["choices"],
            "help": spec["help"],
        }
        for key, spec in SCHEMA.items()
    ]
