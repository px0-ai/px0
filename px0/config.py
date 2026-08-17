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
        "path": "~/.px0/outputs",
    },
    "connectors": {
        "provider": "composio",
        "retries": 3,
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
