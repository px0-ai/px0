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
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, int):
        return str(v)
    if isinstance(v, list):
        return "[" + ", ".join(_toml_value(x) for x in v) + "]"
    s = str(v).replace("\\", "\\\\").replace('"', '\\"')
    return f'"{s}"'


def dumps(config: dict[str, Any]) -> str:
    lines: list[str] = []
    # scalar/list keys at root first (none expected, but keep it general)
    root_scalars = {k: v for k, v in config.items() if not isinstance(v, dict)}
    for k, v in root_scalars.items():
        lines.append(f"{k} = {_toml_value(v)}")
    if root_scalars:
        lines.append("")

    def emit_table(prefix: str, table: dict[str, Any]) -> None:
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
    out = dict(base)
    for k, v in overlay.items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _deep_merge(out[k], v)
        else:
            out[k] = v
    return out


def load(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {k: dict(v) for k, v in DEFAULTS.items()}
    with open(path, "rb") as f:
        on_disk = tomllib.load(f)
    return _deep_merge(DEFAULTS, on_disk)


def save(path: Path, config: dict[str, Any]) -> None:
    path.write_text(dumps(config))


def get(config: dict[str, Any], dotted_key: str, default: Any = None) -> Any:
    node: Any = config
    for part in dotted_key.split("."):
        if not isinstance(node, dict) or part not in node:
            return default
        node = node[part]
    return node
