"""config.toml read/write. Read via stdlib tomllib; write with a small
hand-rolled writer since the schema is a fixed, shallow set of tables."""

import tomllib
from pathlib import Path
from typing import Any

DEFAULTS: dict[str, Any] = {
    "model": {
        "harness_cmd": "claude -p",
    },
    "knowledge": {
        "path": "~/.px0/knowledge",
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
    "proposals": {
        "max_per_consolidation": 10,
    },
    "versions": {
        "keep_all": True,
        "max_versions_per_file": 200,
    },
    "logs": {
        "path": "/var/log/px0",
        "retention_days": 14,
        "retention_days_failed": 60,
        "record_retention_days": 365,
        "max_file_size_mb": 20,
    },
    "update": {
        "channel": "stable",
        "check": True,
        "auto_install": False,
    },
    "retrieval": {
        "backend": "local",
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
    if isinstance(v, int):
        return str(v)
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
    "knowledge.path": {
        "type": str, "choices": None,
        "help": "directory the knowledge library lives in (any folder, e.g. an existing notes vault)",
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
    "proposals.max_per_consolidation": {
        "type": int, "choices": None,
        "help": "cap on proposals surfaced per consolidation session",
    },
    "versions.keep_all": {
        "type": bool, "choices": None,
        "help": "true keeps every version forever; false enables the max_versions_per_file cap",
    },
    "versions.max_versions_per_file": {
        "type": int, "choices": None,
        "help": "per-file version cap applied by `px0 versions prune` when keep_all is false",
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
    "update.channel": {
        "type": str, "choices": ["stable", "beta"],
        "help": "release channel; not functionally checked in this build -- "
                "`px0 update` reports that no release manifest is configured",
    },
    "update.check": {
        "type": bool, "choices": None,
        "help": "whether the daemon checks weekly for an available update",
    },
    "update.auto_install": {
        "type": bool, "choices": None,
        "help": "install updates automatically instead of only surfacing them",
    },
    "retrieval.backend": {
        "type": str, "choices": ["local", "qmd"],
        "help": "retrieval backend; either 'local' (SQLite FTS5/BM25) or 'qmd'",
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
        "help": "reserved for a future rerank stage; not yet wired in this build",
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
