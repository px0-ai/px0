"""px0 connect: creating and managing connections.

The native GitHub PAT path is fully wired (verifies the token against the
GitHub API before storing it). `setup-composio` stores the API key; actually
creating a Composio-hosted auth link is fully supported (see tools.py).
"""

from datetime import datetime, timezone
from pathlib import Path

import requests

from px0 import credentials as creds_mod

TOOLKIT_SLUGS = {
    "gmail": "gmail",
    "slack": "slack",
    "calendar": "googlecalendar",
}


def setup_composio(home: Path, api_key: str) -> None:
    """Stores the Composio API key as a credential. Does not validate the key."""
    creds_mod.set_service(home, "composio", {"api_key": api_key})


def _composio_client(home: Path) -> requests.Session:
    """Returns a requests.Session configured with the stored Composio API key."""
    creds = creds_mod.load(home)
    composio = creds.get("composio")
    if not composio or not composio.get("api_key"):
        raise ValueError(
            "Composio API key is not configured; run `px0 connect setup-composio <key>` first"
        )
    session = requests.Session()
    session.headers.update({
        "x-api-key": composio["api_key"],
        "Content-Type": "application/json"
    })
    return session


def _ensure_auth_config(home: Path, toolkit: str) -> str:
    """Checks [composio.auth_configs].<toolkit> in credentials; if absent,
    creates it via Composio API and caches the returned ID."""
    creds = creds_mod.load(home)
    composio_creds = creds.get("composio", {})
    auth_configs = composio_creds.setdefault("auth_configs", {})
    if toolkit in auth_configs:
        return auth_configs[toolkit]

    session = _composio_client(home)
    payload = {
        "toolkit": {"slug": toolkit},
        "auth_config": {"type": "use_composio_managed_auth"}
    }
    resp = session.post("https://backend.composio.dev/api/v3.1/auth_configs", json=payload, timeout=15)
    if resp.status_code >= 400:
        raise ValueError(f"Composio auth_configs API -> {resp.status_code}: {resp.text[:200]}")

    data = resp.json()
    auth_config_id = data["auth_config"]["id"]
    auth_configs[toolkit] = auth_config_id
    creds_mod.set_service(home, "composio", composio_creds)
    return auth_config_id


def connect_composio_app(home: Path, app: str) -> dict:
    """Creates (or reuses) an auth config, creates an auth link session for the app,
    caches the connected_account_id, and returns the redirect_url."""
    if app not in TOOLKIT_SLUGS:
        raise ValueError(f"Unsupported app: {app}")

    toolkit_slug = TOOLKIT_SLUGS[app]
    auth_config_id = _ensure_auth_config(home, toolkit_slug)

    session = _composio_client(home)
    payload = {
        "auth_config_id": auth_config_id,
        "user_id": "px0-local"
    }
    resp = session.post("https://backend.composio.dev/api/v3/connected_accounts/link", json=payload, timeout=15)
    if resp.status_code >= 400:
        raise ValueError(f"Composio linked_accounts API -> {resp.status_code}: {resp.text[:200]}")

    data = resp.json()
    redirect_url = data["redirect_url"]
    connected_account_id = data["connected_account_id"]

    creds = creds_mod.load(home)
    composio_creds = creds.get("composio", {})
    connected_accounts = composio_creds.setdefault("connected_accounts", {})
    connected_accounts[app] = connected_account_id
    creds_mod.set_service(home, "composio", composio_creds)

    return {"redirect_url": redirect_url, "connected_account_id": connected_account_id}


def connected_account_status(home: Path, app: str) -> str:
    """Polls the status of the cached connected account from the Composio API."""
    creds = creds_mod.load(home)
    composio = creds.get("composio", {})
    connected_accounts = composio.get("connected_accounts", {})
    if app not in connected_accounts:
        return "NOT_CONNECTED"

    connected_account_id = connected_accounts[app]
    try:
        session = _composio_client(home)
        resp = session.get(f"https://backend.composio.dev/api/v3.1/connected_accounts/{connected_account_id}", timeout=15)
        if resp.status_code == 404:
            return "NOT_FOUND"
        if resp.status_code >= 400:
            return f"ERROR ({resp.status_code})"
        return resp.json().get("status", "UNKNOWN")
    except Exception as e:
        return f"ERROR ({str(e)})"


def connect_github_native(home: Path, token: str) -> dict:
    """Verifies a GitHub PAT against the GitHub API and stores it on success.

    Raises ValueError if GitHub rejects the token. Returns the resolved login."""
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
    """Replaces the stored GitHub token; rotation is just a re-verify-and-store."""
    return connect_github_native(home, token)


def list_connections(home: Path) -> list[dict]:
    """Returns one summary dict per configured connection (service, kind, login, expiry)."""
    creds = creds_mod.load(home)
    out = []
    for service, values in creds.items():
        if service == "composio":
            out.append({"service": "composio", "kind": "api-key", "status": "configured"})
            connected_accounts = values.get("connected_accounts", {})
            for app in sorted(connected_accounts.keys()):
                status = connected_account_status(home, app)
                out.append({
                    "service": app,
                    "kind": f"composio-{app}",
                    "status": status,
                })
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
    """Deletes a stored connection. Returns False if the service wasn't configured."""
    return creds_mod.remove_service(home, service)
