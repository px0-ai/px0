"""px0 connect: creating and managing connections.

All external app connections are managed through Composio.
"""

from datetime import datetime, timezone
from pathlib import Path

import requests

from px0 import credentials as creds_mod

TOOLKIT_SLUGS = {
    "gmail": "gmail",
    "slack": "slack",
    "calendar": "googlecalendar",
    "github": "github",
}


def setup_composio(home: Path, api_key: str) -> None:
    """Stores the Composio API key as a credential. Does not validate the key."""
    creds_mod.set_service(home, "composio", {"api_key": api_key})


def _composio_client(home: Path):
    """Returns a Composio client configured with the stored Composio API key."""
    creds = creds_mod.load(home)
    composio = creds.get("composio")
    if not composio or not composio.get("api_key"):
        raise ValueError(
            "Composio API key is not configured; run `px0 connect setup-composio <key>` first"
        )
    from composio import Composio
    return Composio(api_key=composio["api_key"])


def _ensure_auth_config(home: Path, toolkit: str) -> str:
    """Checks [composio.auth_configs].<toolkit> in credentials; if absent,
    creates it via Composio API and caches the returned ID."""
    creds = creds_mod.load(home)
    composio_creds = creds.get("composio", {})
    auth_configs = composio_creds.setdefault("auth_configs", {})
    if toolkit in auth_configs:
        return auth_configs[toolkit]

    client = _composio_client(home)
    try:
        auth_config = client.auth_configs.create(toolkit=toolkit, options={"type": "use_composio_managed_auth"})
        auth_config_id = auth_config.id
    except Exception as e:
        raise ValueError(f"Composio auth_configs API error: {e}")

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

    client = _composio_client(home)
    try:
        connection_request = client.connected_accounts.link(
            user_id="px0-local",
            auth_config_id=auth_config_id
        )
    except Exception as e:
        raise ValueError(f"Composio linked_accounts API error: {e}")

    redirect_url = getattr(connection_request, "redirectUrl", getattr(connection_request, "redirect_url", None))
    connected_account_id = getattr(connection_request, "id", None)

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
        client = _composio_client(home)
        account = client.connected_accounts.get(connected_account_id)
        return account.status
    except Exception as e:
        if "404" in str(e) or "not found" in str(e).lower():
            return "NOT_FOUND"
        return f"ERROR ({str(e)})"


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
