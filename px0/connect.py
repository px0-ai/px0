"""px0 connect: creating and managing connections.

The native GitHub PAT path is fully wired (verifies the token against the
GitHub API before storing it). `setup-composio` stores the API key; actually
creating a Composio-hosted auth link is not implemented in this build (see
tools.py) so `connect <app>` without --native says so plainly rather than
faking a flow.
"""

from datetime import datetime, timezone
from pathlib import Path

import requests

from px0 import credentials as creds_mod


def setup_composio(home: Path, api_key: str) -> None:
    creds_mod.set_service(home, "composio", {"api_key": api_key})


def connect_github_native(home: Path, token: str) -> dict:
    resp = requests.get(
        "https://api.github.com/user",
        headers={"Authorization": f"Bearer {token}", "Accept": "application/vnd.github+json"},
        timeout=15,
    )
    if resp.status_code != 200:
        raise ValueError(f"github rejected this token ({resp.status_code}): {resp.text[:200]}")
    login = resp.json()["login"]
    creds_mod.set_service(home, "github", {
        "kind": "native-pat",
        "token": token,
        "login": login,
        "connected_at": datetime.now(timezone.utc).isoformat(),
    })
    return {"login": login}


def rotate_github(home: Path, token: str) -> dict:
    return connect_github_native(home, token)


def list_connections(home: Path) -> list[dict]:
    creds = creds_mod.load(home)
    out = []
    for service, values in creds.items():
        if service == "composio":
            out.append({"service": "composio", "kind": "api-key", "status": "configured"})
        else:
            out.append({
                "service": service,
                "kind": values.get("kind", "unknown"),
                "status": "configured",
                "login": values.get("login"),
                "expires_at": values.get("expires_at"),
            })
    return out


def remove_connection(home: Path, service: str) -> bool:
    return creds_mod.remove_service(home, service)
