import pytest
from px0 import connect, credentials as creds_mod

def test_ensure_auth_config_creates_once_and_reuses(tmp_home, fake_composio):
    # Setup API key
    connect.setup_composio(tmp_home, "test_key")

    # First call - should create and cache
    ac_id_1 = connect._ensure_auth_config(tmp_home, "gmail")
    assert ac_id_1 == "ac_testconfig"

    # Modify the fake_composio so we can verify if it's reused without another API call
    fake_composio.auth_config_id = "ac_new_config"

    # Second call - should return cached id
    ac_id_2 = connect._ensure_auth_config(tmp_home, "gmail")
    assert ac_id_2 == "ac_testconfig" # Still the old cached one!


def test_connect_composio_app_happy_path(tmp_home, fake_composio):
    # Setup API key
    connect.setup_composio(tmp_home, "test_api_key")

    res = connect.connect_composio_app(tmp_home, "gmail")
    assert res["redirect_url"] == "https://backend.composio.dev/redirect-mock"
    assert res["connected_account_id"] == "ca_testaccount"

    # Check cached credentials
    creds = creds_mod.load(tmp_home)
    assert creds["composio"]["connected_accounts"]["gmail"] == "ca_testaccount"


def test_connect_composio_app_raises_on_missing_api_key(tmp_home, fake_composio, monkeypatch):
    # Ensure no composio key is configured
    monkeypatch.delenv("COMPOSIO_API_KEY", raising=False)
    from px0 import config as config_mod, paths
    cfg_path = paths.config_path(tmp_home)
    config = config_mod.load(cfg_path)
    config["connectors"]["composio_api_key"] = ""
    config_mod.save(cfg_path, config)
    creds_mod.remove_service(tmp_home, "composio")

    with pytest.raises(ValueError, match="Composio API key is not configured"):
        connect.connect_composio_app(tmp_home, "gmail")


def test_connected_account_status_mapping(tmp_home, fake_composio):
    connect.setup_composio(tmp_home, "test_api_key")

    # Set up fake connection
    creds = creds_mod.load(tmp_home)
    creds["composio"]["connected_accounts"] = {"gmail": "ca_testaccount"}
    creds_mod.save(tmp_home, creds)

    # ACTIVE
    fake_composio.status = "ACTIVE"
    assert connect.connected_account_status(tmp_home, "gmail") == "ACTIVE"

    # INITIATED
    fake_composio.status = "INITIATED"
    assert connect.connected_account_status(tmp_home, "gmail") == "INITIATED"

    # FAILED
    fake_composio.status = "FAILED"
    assert connect.connected_account_status(tmp_home, "gmail") == "FAILED"


def test_setup_composio_writes_to_config_toml(tmp_home, fake_composio):
    from px0 import config as config_mod, paths, cli
    import os

    # Ensure the key does not exist yet in environment or config
    os.environ.pop("COMPOSIO_API_KEY", None)
    cfg_path = paths.config_path(tmp_home)
    config = config_mod.load(cfg_path)
    assert config.get("connectors", {}).get("composio_api_key") == ""

    # Setup composio
    connect.setup_composio(tmp_home, "config_test_api_key_abc")

    # Verify it is written to config.toml
    config = config_mod.load(cfg_path)
    assert config["connectors"]["composio_api_key"] == "config_test_api_key_abc"

    # Verify it is dynamically loaded into credentials
    creds = creds_mod.load(tmp_home)
    assert creds["composio"]["api_key"] == "config_test_api_key_abc"

    # Verify running _ctx sets COMPOSIO_API_KEY in environment
    os.environ.pop("COMPOSIO_API_KEY", None)
    # Monkeypatch paths.store_home to return tmp_home so _ctx reads our tmp_home store
    from unittest.mock import patch
    with patch("px0.paths.store_home", return_value=tmp_home):
        cli._ctx()
        assert os.environ.get("COMPOSIO_API_KEY") == "config_test_api_key_abc"


def _connect_gmail(home, fake_composio, status):
    connect.setup_composio(home, "test_api_key")
    creds = creds_mod.load(home)
    creds["composio"]["connected_accounts"] = {"gmail": "ca_testaccount"}
    creds_mod.save(home, creds)
    fake_composio.status = status


def test_doctor_flags_a_connection_stuck_in_initiated(tmp_home, fake_composio):
    """Phase 1 AC5: a non-ACTIVE Composio connection makes doctor unhealthy."""
    from px0 import doctor

    _connect_gmail(tmp_home, fake_composio, "INITIATED")

    check = doctor._check_connections(tmp_home)
    assert check["ok"] is False
    assert "gmail" in check["detail"] and "INITIATED" in check["detail"]
    assert "finish the browser consent" in check["detail"]


def test_doctor_passes_when_connection_is_active(tmp_home, fake_composio):
    from px0 import doctor

    _connect_gmail(tmp_home, fake_composio, "ACTIVE")

    check = doctor._check_connections(tmp_home)
    assert check["ok"] is True


def test_cmd_doctor_exits_integrity_error_on_stuck_connection(tmp_home, fake_composio, monkeypatch):
    """AC5 names the exit code, so assert the CLI's, not just the check's."""
    import argparse
    from px0 import cli, config as config_mod, paths

    _connect_gmail(tmp_home, fake_composio, "FAILED")
    config = config_mod.load(paths.config_path(tmp_home))
    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, config))

    args = argparse.Namespace(quick=True, json=False)
    with pytest.raises(SystemExit) as exc:
        cli.cmd_doctor(args)
    assert exc.value.code == cli.EXIT_INTEGRITY_ERROR
