import pytest
import json
import subprocess
import requests
import argparse
import shutil
from pathlib import Path
from packaging import version
from px0 import update, paths, config as config_mod, cli, store

class MockResponse:
    def __init__(self, json_data, status_code=200):
        self.json_data = json_data
        self.status_code = status_code

    def json(self):
        return self.json_data

    def raise_for_status(self):
        if self.status_code >= 400:
            raise requests.HTTPError("error")


def test_pypi_latest_version_parsing(monkeypatch):
    canned_pypi_response = {
        "info": {"version": "0.1.0"},
        "releases": {
            "0.1.0": [],
            "0.2.0b1": [],
            "0.1.5": []
        }
    }

    def mock_get(*a, **kw):
        return MockResponse(canned_pypi_response)
    monkeypatch.setattr(requests, "get", mock_get)

    # Stable channel
    assert update._pypi_latest_version("stable") == "0.1.0"

    # Beta channel (should return highest, including pre-releases)
    assert update._pypi_latest_version("beta") == "0.2.0b1"


def test_version_comparison():
    assert version.parse("0.9.0") < version.parse("0.10.0")


def test_detect_install_mechanism_pipx(tmp_home, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/usr/bin/pipx" if cmd == "pipx" else None)

    class MockRun:
        def __init__(self):
            self.returncode = 0
            self.stdout = '{"venvs": {"px0": {}}}'
    
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: MockRun())

    assert update._detect_install_mechanism(tmp_home) == "pipx"


def test_detect_install_mechanism_fallback_pip(tmp_home, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: None)
    assert update._detect_install_mechanism(tmp_home) == "pip"


def test_migration_runner_advances_schema_on_success(tmp_home, monkeypatch):
    # Setup migration registry with test migrations
    mig_1_called = []
    mig_2_called = []

    def mock_mig_1(home):
        mig_1_called.append(home)
        return []

    def mock_mig_2(home):
        mig_2_called.append(home)
        return []

    # Mock migration registry
    monkeypatch.setattr(update, "MIGRATIONS", {
        2: mock_mig_1,
        3: mock_mig_2
    })

    # Set initial schema to 1
    schema_file = paths.schema_path(tmp_home)
    schema_file.write_text("1")

    # Set up canned PyPI update check response
    monkeypatch.setattr(update, "check", lambda *a: {"channel": "stable", "available_version": "0.2.0"})
    monkeypatch.setattr(update, "_detect_install_mechanism", lambda *a: "pip")
    
    # Mock subprocess run to simulate successful pip upgrade
    class MockSubprocess:
        def __init__(self):
            self.returncode = 0
            self.stderr = ""
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: MockSubprocess())

    # We also mock record_change to avoid committing migrations to git versions in tests
    monkeypatch.setattr("px0.versioning.record_change", lambda *a: "ch_mock")

    # Run update
    update.run_update(tmp_home, {})

    assert len(mig_1_called) == 1
    assert len(mig_2_called) == 1
    assert schema_file.read_text().strip() == "3"


def test_migration_runner_halts_and_preserves_on_failure(tmp_home, monkeypatch):
    def mock_mig_1(home):
        return []

    def mock_mig_2(home):
        raise RuntimeError("failed migration 2")

    monkeypatch.setattr(update, "MIGRATIONS", {
        2: mock_mig_1,
        3: mock_mig_2
    })

    schema_file = paths.schema_path(tmp_home)
    schema_file.write_text("1")

    monkeypatch.setattr(update, "check", lambda *a: {"channel": "stable", "available_version": "0.2.0"})
    monkeypatch.setattr(update, "_detect_install_mechanism", lambda *a: "pip")
    class MockSubprocess:
        def __init__(self):
            self.returncode = 0
            self.stderr = ""
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: MockSubprocess())
    monkeypatch.setattr("px0.versioning.record_change", lambda *a: "ch_mock")

    with pytest.raises(update.UpdateError, match="Migration to v3 failed"):
        update.run_update(tmp_home, {})

    # Should have stopped at 2, schema is 2 because mig_1 succeeded but mig_2 failed!
    assert schema_file.read_text().strip() == "2"


def test_rollback_raises_on_empty_history(tmp_home):
    history_file = paths.update_history_path(tmp_home)
    if history_file.exists():
        history_file.unlink()

    with pytest.raises(update.UpdateError, match="no update history exists"):
        update.rollback(tmp_home, {})


def test_cmd_update_rollback_error_exit(tmp_home, monkeypatch, capsys):
    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, {}))

    args = argparse.Namespace(rollback=True)
    with pytest.raises(SystemExit) as exc_info:
        cli.cmd_update(args)

    assert exc_info.value.code == cli.EXIT_USER_ERROR
    captured = capsys.readouterr()
    assert "no update history exists" in captured.err


def test_install_script_uninstall():
    # Verify we can execute install.sh with --uninstall and it runs successfully
    # Since install.sh has chmod +x, let's run it
    res = subprocess.run(["./install.sh", "--uninstall"], capture_output=True, text=True, timeout=10)
    assert res.returncode == 0
    assert "Uninstalling px0..." in res.stdout
    assert "rm -rf ~/.px0" in res.stdout
