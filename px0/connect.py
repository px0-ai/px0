"""Connections to external apps, all brokered through Composio.

There is no user-facing connect command: `setup_composio` stores the one API
key (via `px0 config composio`, or `px0 init`), and individual apps are
authorized on demand -- a tool that needs Gmail calls
`connect_composio_app("gmail")` itself and surfaces the returned URL, so the
only manual step left is the human consenting in a browser.
"""

import os
import ssl
from pathlib import Path

from px0 import credentials as creds_mod

TOOLKIT_SLUGS = {
    "gmail": "gmail",
    "slack": "slack",
    "calendar": "googlecalendar",
    "github": "github",
}

COMPOSIO_HOST = "backend.composio.dev"

# Bundles that tend to carry a corporate/MITM root (Zscaler, Netskope, ...) which
# certifi -- what httpx verifies against by default -- deliberately does not ship.
CA_BUNDLE_CANDIDATES = (
    "/opt/homebrew/etc/ca-certificates/cert.pem",
    "/usr/local/etc/ca-certificates/cert.pem",
    "/etc/ssl/cert.pem",
    "/etc/ssl/certs/ca-certificates.crt",
    "/etc/pki/tls/certs/ca-bundle.crt",
)


class ComposioUnreachable(RuntimeError):
    """The Composio API could not be reached. Distinct from a rejected API key:
    re-entering the key will not help, so callers must not re-prompt for one."""


def _is_cert_error(exc: BaseException) -> bool:
    """True if exc (or anything it wraps) is a TLS certificate verification failure."""
    seen = set()
    while exc is not None and id(exc) not in seen:
        seen.add(id(exc))
        if isinstance(exc, ssl.SSLCertVerificationError):
            return True
        if "CERTIFICATE_VERIFY_FAILED" in str(exc):
            return True
        exc = exc.__cause__ or exc.__context__
    return False


def _bundle_verifies(bundle: str, host: str = COMPOSIO_HOST) -> bool:
    """True if `host`'s certificate chain validates against `bundle`."""
    import socket

    try:
        ctx = ssl.create_default_context(cafile=bundle)
        with socket.create_connection((host, 443), timeout=10) as sock:
            with ctx.wrap_socket(sock, server_hostname=host):
                return True
    except Exception:
        return False


def find_ca_bundle(host: str = COMPOSIO_HOST) -> str | None:
    """Returns the first existing CA bundle that validates `host`, or None."""
    for bundle in CA_BUNDLE_CANDIDATES:
        if os.path.exists(bundle) and _bundle_verifies(bundle, host):
            return bundle
    return None


def ca_bundle(home: Path) -> str | None:
    """The CA bundle TLS verification should use, or None for the default.

    An explicit SSL_CERT_FILE always wins; otherwise the bundle a previous
    interception detection stored in `connectors.ca_bundle`.
    """
    explicit = os.environ.get("SSL_CERT_FILE")
    if explicit:
        return explicit
    from px0 import config as config_mod, paths

    bundle = config_mod.get(config_mod.load(paths.config_path(home)), "connectors.ca_bundle")
    return bundle if bundle and os.path.exists(bundle) else None


def apply_ca_bundle(home: Path) -> str | None:
    """Exports the stored CA bundle so HTTP clients pick it up, and returns it.

    Sets both names because the two clients px0 uses read different ones:
    httpx/OpenSSL honour SSL_CERT_FILE, while requests verifies against certifi
    unless REQUESTS_CA_BUNDLE says otherwise. A no-op when nothing is stored.
    """
    bundle = ca_bundle(home)
    if not bundle:
        return None
    os.environ.setdefault("SSL_CERT_FILE", bundle)
    os.environ.setdefault("REQUESTS_CA_BUNDLE", bundle)
    return bundle


def recover_ca_bundle(home: Path) -> str | None:
    """Called after a TLS verification failure: find a CA bundle that trusts
    whatever is intercepting the connection, persist it, and return it.

    Shared by every caller that talks to Composio, so an interception detected
    once is remembered for all of them. Returns None when no known bundle helps.
    """
    bundle = find_ca_bundle()
    if not bundle:
        return None
    _store_ca_bundle(home, bundle)
    os.environ["SSL_CERT_FILE"] = bundle
    os.environ["REQUESTS_CA_BUNDLE"] = bundle
    return bundle


def _store_ca_bundle(home: Path, bundle: str) -> None:
    from px0 import config as config_mod, paths

    cfg_path = paths.config_path(home)
    config = config_mod.load(cfg_path)
    config_mod.set_key(config, "connectors.ca_bundle", bundle)
    config_mod.save(cfg_path, config)


def _silence_sdk_logging() -> None:
    """Mutes the Composio/httpx INFO chatter ("Retrying request to ...").

    Those lines are the SDK narrating its own retries; they interleave with
    px0's progress output and tell the user nothing actionable. Warnings and
    errors still come through.
    """
    import logging

    for name in ("composio", "composio_client", "httpx", "httpcore"):
        logging.getLogger(name).setLevel(logging.WARNING)


def short_api_error(exc: BaseException) -> str:
    """Composio SDK errors stringify as `Error code: N - {...whole payload...}`.

    Keeps the parts a human acts on -- the message and the suggested fix -- so a
    permissions problem reads as one line instead of a wall of JSON.
    """
    import ast
    import json as json_mod
    import re as re_mod

    text = str(exc)
    match = re_mod.search(r"\{.*\}", text, re_mod.DOTALL)
    if not match:
        return text[:300]
    blob = match.group(0)
    for parse in (json_mod.loads, ast.literal_eval):
        try:
            data = parse(blob)
            break
        except Exception:
            continue
    else:
        return text[:300]

    err = data.get("error", data) if isinstance(data, dict) else {}
    message = str(err.get("message") or "").strip()
    if not message:
        return text[:300]
    fix = str(err.get("suggested_fix") or "").strip()
    status = err.get("status") or err.get("code")
    prefix = f"{status}: " if status else ""
    return f"{prefix}{message}" + (f" -- {fix}" if fix else "")


def _verify_key(api_key: str) -> None:
    """Hello world / healthcheck: fetch github toolkit info to verify the key."""
    from composio import Composio

    _silence_sdk_logging()
    Composio(api_key=api_key).toolkits.get("github")


def setup_composio(home: Path, api_key: str) -> dict:
    """Stores the Composio API key inside config.toml and credentials after validating it.

    Returns what the caller may want to report: {"ca_bundle": <path or None>},
    naming the CA bundle a TLS interception forced px0 onto. Prints nothing --
    presentation belongs to the CLI, which may be drawing a spinner over this.
    """
    apply_ca_bundle(home)
    used_bundle = None
    try:
        _verify_key(api_key)
    except Exception as e:
        if "401" in str(e) or "AuthenticationError" in str(type(e)):
            raise ValueError(
                "\nInvalid Composio API key.\n"
                "Please create one from the Composio platform:\n"
                "https://composio.dev > Get Started > Settings > API Keys\n"
                "Please put the key to proceed.\n"
            ) from e

        if not _is_cert_error(e):
            # Genuinely unreachable (offline, DNS, timeout): the key is unjudged, so
            # don't send the caller back to re-type it.
            raise ComposioUnreachable(
                f"\nCould not reach the Composio API ({COMPOSIO_HOST}): {e}\n"
                "Your API key was not verified. Check your network and retry.\n"
            ) from e

        # TLS interception (corporate proxy such as Zscaler). certifi -- the bundle
        # httpx verifies against -- has no such root, but a system bundle usually does.
        bundle = recover_ca_bundle(home)
        if bundle is None:
            raise ComposioUnreachable(
                f"\nTLS certificate verification failed talking to {COMPOSIO_HOST}.\n"
                "The connection is being intercepted (typically a corporate proxy like "
                "Zscaler) and none of the CA bundles px0 knows about trust the interceptor.\n"
                "Point px0 at a bundle that includes your corporate root, then retry:\n"
                "  export SSL_CERT_FILE=/path/to/corporate-ca-bundle.pem\n"
                "Your API key was not verified.\n"
            ) from e

        try:
            _verify_key(api_key)
        except Exception as retry_exc:
            if "401" in str(retry_exc) or "AuthenticationError" in str(type(retry_exc)):
                raise ValueError(
                    "\nInvalid Composio API key.\n"
                    "Please create one from the Composio platform:\n"
                    "https://composio.dev > Get Started > Settings > API Keys\n"
                    "Please put the key to proceed.\n"
                ) from retry_exc
            raise ComposioUnreachable(
                f"\nCould not reach the Composio API ({COMPOSIO_HOST}) even using the CA "
                f"bundle at {bundle}: {retry_exc}\n"
                "Your API key was not verified.\n"
            ) from retry_exc

        used_bundle = bundle  # recover_ca_bundle already persisted it

    # Store in config.toml
    from px0 import config as config_mod, paths
    cfg_path = paths.config_path(home)
    config = config_mod.load(cfg_path)
    config_mod.set_key(config, "connectors.composio_api_key", api_key)
    config_mod.save(cfg_path, config)

    return {"ca_bundle": used_bundle}


def _composio_client(home: Path):
    """Returns a Composio client configured with the stored Composio API key."""
    from px0 import config as config_mod, paths
    config = config_mod.load(paths.config_path(home))
    api_key = config_mod.get(config, "connectors.composio_api_key") or os.environ.get("COMPOSIO_API_KEY")
    if not api_key:
        creds = creds_mod.load(home)
        composio = creds.get("composio")
        if composio and composio.get("api_key"):
            api_key = composio["api_key"]
    if not api_key:
        raise ValueError(
            "Composio API key is not configured; run `px0 config composio <key>` first"
        )
    apply_ca_bundle(home)
    _silence_sdk_logging()
    from composio import Composio
    return Composio(api_key=api_key)


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
        raise ValueError(f"Composio could not prepare authorization -- {short_api_error(e)}")

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
        raise ValueError(f"Composio could not create an auth link -- {short_api_error(e)}")

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

