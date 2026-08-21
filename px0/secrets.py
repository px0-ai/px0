"""Secrets a workflow can reference without them living in the workflow file.

A workflow file is content: it is versioned, diffed, exported, and sometimes
written by a model. An API token, an internal hostname, or a repository name
you would rather not publish does not belong in one. These live beside the
connector credentials instead, and a workflow reaches them as
`{{secrets.NAME}}` in any `args` value or in the prompt body.

Two guarantees make that safe to use:

- The value never reaches disk through a run record or a run log. The runner
  redacts every secret value out of both.
- `px0 secrets list` prints names, never values.
"""

import re
from pathlib import Path

from px0 import credentials as creds_mod

SERVICE = "secrets"

# Uppercase, so a placeholder reads as a constant at a glance and cannot be
# confused with an input id.
NAME_RE = re.compile(r"^[A-Z][A-Z0-9_]*$")

REDACTED = "[redacted]"


class SecretError(Exception):
    """A secret name is invalid, or a secret that must exist does not."""


def check_name(name: str) -> str:
    """Validates and normalizes a secret name. Raises SecretError if it cannot be one."""
    candidate = (name or "").strip()
    if not NAME_RE.match(candidate):
        raise SecretError(
            f"{name!r} is not a valid secret name; use uppercase letters, digits, and "
            "underscores, e.g. GITHUB_TOKEN"
        )
    return candidate


def all_secrets(home: Path) -> dict[str, str]:
    """Every secret in the store, as {name: value}."""
    values = creds_mod.load(home).get(SERVICE) or {}
    return {str(k): str(v) for k, v in values.items() if NAME_RE.match(str(k))}


def names(home: Path) -> list[str]:
    """Every secret name, sorted. Values are never returned here."""
    return sorted(all_secrets(home))


def set_secret(home: Path, name: str, value: str) -> str:
    """Stores a secret, replacing any earlier value. Returns the name it was stored under."""
    name = check_name(name)
    if value is None or value == "":
        raise SecretError(f"{name} needs a value; to remove it use `px0 secrets unset {name}`")
    current = all_secrets(home)
    current[name] = str(value)
    creds_mod.set_service(home, SERVICE, current)
    return name


def unset_secret(home: Path, name: str) -> bool:
    """Removes a secret. Returns False if there was nothing to remove."""
    name = check_name(name)
    current = all_secrets(home)
    if name not in current:
        return False
    current.pop(name)
    creds_mod.set_service(home, SERVICE, current)
    return True


def redactor(home: Path):
    """Returns a function that replaces every stored secret value with a marker.

    Values shorter than four characters are left alone: redacting "1" would
    mangle unrelated text without protecting anything worth protecting.
    """
    values = [v for v in all_secrets(home).values() if len(v) >= 4]
    values.sort(key=len, reverse=True)  # longest first, so overlaps redact fully

    def redact(obj):
        if not values:
            return obj
        if isinstance(obj, str):
            out = obj
            for v in values:
                if v in out:
                    out = out.replace(v, REDACTED)
            return out
        if isinstance(obj, list):
            return [redact(o) for o in obj]
        if isinstance(obj, tuple):
            return tuple(redact(o) for o in obj)
        if isinstance(obj, dict):
            return {k: redact(v) for k, v in obj.items()}
        return obj

    return redact
