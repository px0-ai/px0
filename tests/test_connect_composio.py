import pytest
from pathlib import Path
from px0 import connect, credentials as creds_mod

def test_ensure_auth_config_creates_once_and_reuses(tmp_home, fake_composio):
    # Setup credentials with api_key
    creds_mod.set_service(tmp_home, "composio", {"api_key": "test_key"})

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


def test_connect_composio_app_raises_on_missing_api_key(tmp_home, fake_composio):
    # Ensure no composio key is configured
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
