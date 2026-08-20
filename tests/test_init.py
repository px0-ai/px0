import argparse
import pytest
from px0 import cli, credentials as creds_mod, connect


def _fake_setup_composio(home, key):
    """Stand-in for the real thing, matching its return contract."""
    from px0 import config as config_mod, paths
    cfg_path = paths.config_path(home)
    cfg_path.parent.mkdir(parents=True, exist_ok=True)
    config = config_mod.load(cfg_path)
    config_mod.set_key(config, "connectors.composio_api_key", key)
    config_mod.save(cfg_path, config)
    return {"ca_bundle": None}


def test_mask_key():
    assert cli._mask_key("") == ""
    assert cli._mask_key("1234") == "****"
    assert cli._mask_key("12345678") == "********"
    assert cli._mask_key("123456789") == "1234...6789"
    assert cli._mask_key("comp_live_1234567890abcdef") == "comp...cdef"


def test_cmd_init_with_existing_key_keeps_as_is_on_empty_input(tmp_path, monkeypatch):
    from px0 import config as config_mod, paths
    home = tmp_path / "test_store"
    # Pre-populate config
    home.mkdir(parents=True, exist_ok=True)
    cfg_path = paths.config_path(home)
    config = config_mod.load(cfg_path)
    config_mod.set_key(config, "connectors.composio_api_key", "existing_secret_key_123")
    config_mod.save(cfg_path, config)

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
    assert "Composio API key" in prompt_displayed[0]
    assert "exis..._123" in prompt_displayed[0]      # masked, never the whole key
    assert "existing_secret_key_123" not in prompt_displayed[0]

    # Verify key was kept as-is
    cfg = config_mod.load(paths.config_path(home))
    assert cfg["connectors"]["composio_api_key"] == "existing_secret_key_123"


def test_cmd_init_with_existing_key_updates_on_new_input(tmp_path, monkeypatch):
    from px0 import config as config_mod, paths
    home = tmp_path / "test_store"
    home.mkdir(parents=True, exist_ok=True)
    cfg_path = paths.config_path(home)
    config = config_mod.load(cfg_path)
    config_mod.set_key(config, "connectors.composio_api_key", "existing_secret_key_123")
    config_mod.save(cfg_path, config)

    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])
    monkeypatch.setattr(connect, "setup_composio", _fake_setup_composio)

    prompt_displayed = []

    def mock_input(prompt=""):
        prompt_displayed.append(prompt)
        return "new_secret_key_456"

    monkeypatch.setattr("builtins.input", mock_input)

    args = argparse.Namespace(dir=str(home), harness=None, composio_key=None)
    cli.cmd_init(args)

    assert len(prompt_displayed) == 1
    assert "Composio API key" in prompt_displayed[0]
    assert "exis..._123" in prompt_displayed[0]      # masked, never the whole key
    assert "existing_secret_key_123" not in prompt_displayed[0]

    # Verify key was updated
    cfg = config_mod.load(paths.config_path(home))
    assert cfg["connectors"]["composio_api_key"] == "new_secret_key_456"


def test_cmd_init_fresh_prompts_without_brackets(tmp_path, monkeypatch):
    from px0 import config as config_mod, paths
    home = tmp_path / "test_store"

    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])
    monkeypatch.setattr(connect, "setup_composio", _fake_setup_composio)

    prompt_displayed = []

    def mock_input(prompt=""):
        prompt_displayed.append(prompt)
        return "fresh_api_key_789"

    monkeypatch.setattr("builtins.input", mock_input)

    args = argparse.Namespace(dir=str(home), harness=None, composio_key=None)
    cli.cmd_init(args)

    assert len(prompt_displayed) == 1
    # no [brackets] when there is no existing key to keep
    assert prompt_displayed[0].endswith("Composio API key: ")
    assert "[" not in prompt_displayed[0]

    cfg = config_mod.load(paths.config_path(home))
    assert cfg["connectors"]["composio_api_key"] == "fresh_api_key_789"


def test_cmd_init_retry_on_invalid_key(tmp_path, monkeypatch):
    from px0 import config as config_mod, paths
    home = tmp_path / "test_store"
    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])

    attempts = 0

    def mock_setup(h, key):
        nonlocal attempts
        attempts += 1
        if attempts == 1:
            raise ValueError("Invalid API key")
        return _fake_setup_composio(h, key)

    monkeypatch.setattr(connect, "setup_composio", mock_setup)

    inputs = iter(["bad_key", "good_key"])
    monkeypatch.setattr("builtins.input", lambda prompt="": next(inputs))

    args = argparse.Namespace(dir=str(home), harness=None, composio_key=None)
    cli.cmd_init(args)

    assert attempts == 2
    cfg = config_mod.load(paths.config_path(home))
    assert cfg["connectors"]["composio_api_key"] == "good_key"


def test_cmd_init_with_cli_flag(tmp_path, monkeypatch):
    from px0 import config as config_mod, paths
    home = tmp_path / "test_store"
    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: ["store at " + str(h)])
    monkeypatch.setattr(connect, "setup_composio", _fake_setup_composio)

    args = argparse.Namespace(dir=str(home), harness=None, composio_key="cli_passed_key")
    cli.cmd_init(args)

    cfg = config_mod.load(paths.config_path(home))
    assert cfg["connectors"]["composio_api_key"] == "cli_passed_key"


def test_store_init_does_not_create_skills_folder(tmp_path):
    from px0 import store

    home = tmp_path / "test_store"
    store.init(home)

    assert not (home / "skills").exists()
    assert not (home / "skills.json").exists()


def test_store_init_does_not_create_guidelines_files(tmp_path):
    from px0 import store, paths

    home = tmp_path / "test_store"
    store.init(home)

    assert paths.guidelines_dir(home).exists()
    assert list(paths.guidelines_dir(home).rglob("*")) == []


def test_store_init_does_not_create_credentials_file(tmp_path):
    from px0 import store, paths

    home = tmp_path / "test_store"
    store.init(home)

    assert not paths.credentials_path(home).exists()


def test_store_init_does_not_touch_skills(tmp_path, monkeypatch):
    from px0 import store
    import subprocess

    home = tmp_path / "test_store"
    home.mkdir()

    ran_commands = []
    monkeypatch.setattr(subprocess, "run", lambda args, **kw: ran_commands.append(args))

    store.init(home)

    assert ran_commands == []
    assert not (home / "skills.json").exists()


def _isolate_ssl_env(monkeypatch):
    """Unsets SSL_CERT_FILE for the test and guarantees it is restored afterwards.

    setenv first so monkeypatch records the original value; delenv on an already-unset
    var records nothing, so a value written during the test would leak into later ones.
    """
    monkeypatch.setenv("SSL_CERT_FILE", "")
    monkeypatch.delenv("SSL_CERT_FILE")


def _fake_cert_error():
    import ssl
    inner = ssl.SSLCertVerificationError(
        "[SSL: CERTIFICATE_VERIFY_FAILED] certificate verify failed: unable to get local issuer certificate"
    )
    outer = RuntimeError("Connection error.")
    outer.__cause__ = inner
    return outer


def test_setup_composio_recovers_from_intercepted_tls(tmp_path, monkeypatch):
    """A corporate MITM proxy must be detected and worked around, not reported as a bad key."""
    import os
    from px0 import connect, store, config as config_mod, paths

    home = tmp_path / "store"
    store.init(home)
    _isolate_ssl_env(monkeypatch)

    bundle = str(tmp_path / "corp-ca.pem")
    open(bundle, "w").close()
    monkeypatch.setattr(connect, "CA_BUNDLE_CANDIDATES", (bundle,))
    monkeypatch.setattr(connect, "_bundle_verifies", lambda b, host=None: b == bundle)

    calls = []

    def fake_verify(api_key):
        calls.append(os.environ.get("SSL_CERT_FILE"))
        if os.environ.get("SSL_CERT_FILE") != bundle:
            raise _fake_cert_error()

    monkeypatch.setattr(connect, "_verify_key", fake_verify)

    connect.setup_composio(home, "ak_test")

    assert calls == [None, bundle]  # failed on certifi, retried with the corporate bundle
    cfg = config_mod.load(paths.config_path(home))
    assert config_mod.get(cfg, "connectors.ca_bundle") == bundle


def test_setup_composio_raises_unreachable_when_no_bundle_helps(tmp_path, monkeypatch):
    from px0 import connect, store

    home = tmp_path / "store"
    store.init(home)
    _isolate_ssl_env(monkeypatch)
    monkeypatch.setattr(connect, "CA_BUNDLE_CANDIDATES", ())
    monkeypatch.setattr(connect, "_verify_key", lambda k: (_ for _ in ()).throw(_fake_cert_error()))

    with pytest.raises(connect.ComposioUnreachable):
        connect.setup_composio(home, "ak_test")


def test_setup_composio_offline_is_unreachable_not_invalid_key(tmp_path, monkeypatch):
    from px0 import connect, store

    home = tmp_path / "store"
    store.init(home)
    monkeypatch.setattr(
        connect, "_verify_key", lambda k: (_ for _ in ()).throw(RuntimeError("Connection error."))
    )

    with pytest.raises(connect.ComposioUnreachable):
        connect.setup_composio(home, "ak_test")


def test_cmd_init_exits_instead_of_reprompting_when_unreachable(tmp_path, monkeypatch):
    """Re-typing the key cannot fix a network fault, so init must not loop on it."""
    from px0 import connect

    home = tmp_path / "store"
    monkeypatch.setattr("px0.store.init", lambda h, harness_cmd=None: [])
    monkeypatch.setattr(
        connect, "setup_composio",
        lambda h, k: (_ for _ in ()).throw(connect.ComposioUnreachable("no route")),
    )

    args = argparse.Namespace(dir=str(home), harness=None, composio_key="ak_test")
    with pytest.raises(SystemExit) as exc:
        cli.cmd_init(args)
    assert exc.value.code == cli.EXIT_USER_ERROR


def test_cmd_init_survives_non_interactive_stdin(tmp_path, monkeypatch, capsys):
    """install.sh runs `px0 init` under `curl | sh`, where stdin has no terminal.

    An unhandled EOFError there aborted the installer with a traceback and left
    the user without the "you still need a Composio key" instruction.
    """
    home = tmp_path / "store"
    monkeypatch.setattr("builtins.input", lambda *a: (_ for _ in ()).throw(EOFError()))

    args = argparse.Namespace(dir=str(home), harness=None, composio_key=None)
    cli.cmd_init(args)  # must not raise

    err = capsys.readouterr().err
    assert "skipping Composio setup" in err
    assert (home / "config.toml").exists()


def test_doctor_passes_on_a_freshly_initialized_store(tmp_path, monkeypatch):
    """Phase 5 AC1: `px0 doctor` exits 0 right after install."""
    from px0 import store, doctor, config as config_mod, paths

    home = tmp_path / "store"
    store.init(home)
    config = config_mod.load(paths.config_path(home))

    report = doctor.run(home, config, quick=True)

    assert report["checks"]["unreferenced_guidelines"]["ok"] is True
    assert report["all_ok"] is True, report["checks"]
