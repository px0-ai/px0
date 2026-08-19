import argparse
from pathlib import Path
import pytest
from px0 import cli, credentials as creds_mod, connect


def test_mask_key():
    assert cli._mask_key("") == ""
    assert cli._mask_key("1234") == "****"
    assert cli._mask_key("12345678") == "********"
    assert cli._mask_key("123456789") == "1234...6789"
    assert cli._mask_key("comp_live_1234567890abcdef") == "comp...cdef"


def test_cmd_init_with_existing_key_keeps_as_is_on_empty_input(tmp_path, monkeypatch):
    home = tmp_path / "test_store"
    # Pre-populate credentials
    creds_mod.set_service(home, "composio", {"api_key": "existing_secret_key_123"})

    # Mock store.init to avoid npx network calls during unit test
    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])

    # Mock input to return empty string (user presses Enter)
    prompt_displayed = []

    def mock_input(prompt=""):
        prompt_displayed.append(prompt)
        return ""

    monkeypatch.setattr("builtins.input", mock_input)

    args = argparse.Namespace(dir=str(home), harness=None, composio_key=None)
    cli.cmd_init(args)

    assert len(prompt_displayed) == 1
    assert "Composio API key [exis..._123 - press Enter to keep current]: " in prompt_displayed[0]

    # Verify key was kept as-is
    creds = creds_mod.load(home)
    assert creds["composio"]["api_key"] == "existing_secret_key_123"


def test_cmd_init_with_existing_key_updates_on_new_input(tmp_path, monkeypatch):
    home = tmp_path / "test_store"
    creds_mod.set_service(home, "composio", {"api_key": "existing_secret_key_123"})

    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])
    monkeypatch.setattr(connect, "setup_composio", lambda h, key: creds_mod.set_service(h, "composio", {"api_key": key}))

    prompt_displayed = []

    def mock_input(prompt=""):
        prompt_displayed.append(prompt)
        return "new_secret_key_456"

    monkeypatch.setattr("builtins.input", mock_input)

    args = argparse.Namespace(dir=str(home), harness=None, composio_key=None)
    cli.cmd_init(args)

    assert len(prompt_displayed) == 1
    assert "Composio API key [exis..._123 - press Enter to keep current]: " in prompt_displayed[0]

    # Verify key was updated
    creds = creds_mod.load(home)
    assert creds["composio"]["api_key"] == "new_secret_key_456"


def test_cmd_init_fresh_prompts_without_brackets(tmp_path, monkeypatch):
    home = tmp_path / "test_store"

    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])
    monkeypatch.setattr(connect, "setup_composio", lambda h, key: creds_mod.set_service(h, "composio", {"api_key": key}))

    prompt_displayed = []

    def mock_input(prompt=""):
        prompt_displayed.append(prompt)
        return "fresh_api_key_789"

    monkeypatch.setattr("builtins.input", mock_input)

    args = argparse.Namespace(dir=str(home), harness=None, composio_key=None)
    cli.cmd_init(args)

    assert len(prompt_displayed) == 1
    assert prompt_displayed[0] == "Composio API key: "

    creds = creds_mod.load(home)
    assert creds["composio"]["api_key"] == "fresh_api_key_789"


def test_cmd_init_retry_on_invalid_key(tmp_path, monkeypatch):
    home = tmp_path / "test_store"
    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])

    attempts = 0

    def mock_setup(h, key):
        nonlocal attempts
        attempts += 1
        if attempts == 1:
            raise ValueError("Invalid API key")
        creds_mod.set_service(h, "composio", {"api_key": key})

    monkeypatch.setattr(connect, "setup_composio", mock_setup)

    inputs = iter(["bad_key", "good_key"])
    monkeypatch.setattr("builtins.input", lambda prompt="": next(inputs))

    args = argparse.Namespace(dir=str(home), harness=None, composio_key=None)
    cli.cmd_init(args)

    assert attempts == 2
    creds = creds_mod.load(home)
    assert creds["composio"]["api_key"] == "good_key"


def test_cmd_init_with_cli_flag(tmp_path, monkeypatch):
    home = tmp_path / "test_store"
    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])
    monkeypatch.setattr(connect, "setup_composio", lambda h, key: creds_mod.set_service(h, "composio", {"api_key": key}))

    args = argparse.Namespace(dir=str(home), harness=None, composio_key="cli_passed_key")
    cli.cmd_init(args)

    creds = creds_mod.load(home)
    assert creds["composio"]["api_key"] == "cli_passed_key"

