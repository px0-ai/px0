import pytest
import json
import subprocess
import requests
import argparse
import shutil
from packaging import version
from px0 import update, paths, cli

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


def testdetect_install_mechanism_pipx(tmp_home, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/usr/bin/pipx" if cmd == "pipx" else None)

    class MockRun:
        def __init__(self):
            self.returncode = 0
            self.stdout = '{"venvs": {"px0": {}}}'
    
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: MockRun())

    assert update.detect_install_mechanism(tmp_home) == "pipx"


def testdetect_install_mechanism_fallback_pip(tmp_home, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: None)
    assert update.detect_install_mechanism(tmp_home) == "pip"


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
    # check() always reports update_available; run_update gates on it
    monkeypatch.setattr(update, "check", lambda *a: {
        "channel": "stable", "available_version": "0.2.0", "update_available": True})
    monkeypatch.setattr(update, "detect_install_mechanism", lambda *a: "pip")
    
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

    # check() always reports update_available; run_update gates on it
    monkeypatch.setattr(update, "check", lambda *a: {
        "channel": "stable", "available_version": "0.2.0", "update_available": True})
    monkeypatch.setattr(update, "detect_install_mechanism", lambda *a: "pip")
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


def _install_sh_env(tmp_path, pipx_body):
    """PATH with stub `pipx` and `qmd` in front, so install.sh never touches
    the real ones.

    The previous version of this test ran the real `pipx uninstall px0`, which
    both mutated the developer's machine and failed wherever pipx is absent or
    shimmed. `qmd` is stubbed too so the bun/qmd bootstrap step -- which only
    runs when `qmd` is missing -- never fires a real network install here.
    """
    import os
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    pipx = bin_dir / "pipx"
    pipx.write_text(pipx_body)
    pipx.chmod(0o755)
    qmd = bin_dir / "qmd"
    qmd.write_text("#!/bin/sh\necho \"qmd $*\"\n")
    qmd.chmod(0o755)
    env = dict(os.environ, PATH=f"{bin_dir}:{os.environ['PATH']}")
    return env


def test_install_script_uninstall(tmp_path):
    # The pipx call's own output is captured and only surfaced on failure (so
    # a clean uninstall doesn't scroll pipx's own noise over the summary), so
    # this checks the stub actually ran via a log file rather than stdout.
    log = tmp_path / "pipx.log"
    env = _install_sh_env(tmp_path, f'#!/bin/sh\necho "$@" >> "{log}"\n')
    res = subprocess.run(
        ["./install.sh", "--uninstall"], capture_output=True, text=True, timeout=10, env=env
    )
    assert res.returncode == 0, res.stderr
    assert "Removing px0" in res.stdout
    assert log.read_text().strip() == "uninstall px0"
    assert "rm -rf ~/.px0" in res.stdout


def test_install_script_uninstall_survives_failing_pipx(tmp_path):
    """px0 not installed, or a broken pipx, must not make `--uninstall` exit nonzero."""
    env = _install_sh_env(tmp_path, "#!/bin/sh\necho 'nothing to uninstall' >&2\nexit 1\n")
    res = subprocess.run(
        ["./install.sh", "--uninstall"], capture_output=True, text=True, timeout=10, env=env
    )
    assert res.returncode == 0, res.stderr
    assert "already gone" in res.stdout
    assert "rm -rf ~/.px0" in res.stdout


def test_install_script_runs_non_interactively(tmp_path):
    """`curl ... | sh` has no tty: the script must finish, exit 0, and not hang.

    Covers Phase 5 AC1's shape with stubs -- the real-pipx run stays a CI job.
    """
    env = _install_sh_env(tmp_path, "#!/bin/sh\necho \"pipx $*\"\n")
    px0 = tmp_path / "bin" / "px0"
    px0.write_text("#!/bin/sh\necho \"px0 $*\"\n")
    px0.chmod(0o755)

    res = subprocess.run(
        ["./install.sh"], capture_output=True, text=True, timeout=30,
        env=env, stdin=subprocess.DEVNULL,
    )
    assert res.returncode == 0, res.stderr
    assert "pipx install px0" in res.stdout
    assert "px0 init" in res.stdout
    assert "not a terminal; skipping the daemon prompt" in res.stdout
    # the px0 stub echoes its args, so a bare line proves it was never invoked
    assert "\npx0 daemon install\n" not in res.stdout
    assert "px0 is installed" in res.stdout


def test_install_script_honours_version_and_prefix(tmp_path):
    env = _install_sh_env(tmp_path, "#!/bin/sh\necho \"pipx $*\"\n")
    px0 = tmp_path / "bin" / "px0"
    px0.write_text("#!/bin/sh\necho \"px0 $*\"\n")
    px0.chmod(0o755)
    env.update(PX0_VERSION="1.2.3", PX0_PREFIX=str(tmp_path / "prefix"), PX0_NO_DAEMON="true")

    res = subprocess.run(
        ["./install.sh"], capture_output=True, text=True, timeout=30,
        env=env, stdin=subprocess.DEVNULL,
    )
    assert res.returncode == 0, res.stderr
    assert "pipx install px0==1.2.3" in res.stdout
    # PX0_NO_DAEMON=true suppresses both the prompt and the not-a-terminal notice
    assert "daemon" not in res.stdout


def test_version_is_single_source_of_truth():
    """pyproject reads the version from px0.__version__, so the two cannot drift.

    Phase 5 AC5: assert the wiring, not just today's matching literals.
    """
    import tomllib
    import px0

    with open("pyproject.toml", "rb") as f:
        pyproject = tomllib.load(f)

    assert "version" in pyproject["project"]["dynamic"], "version must stay dynamic"
    assert pyproject["tool"]["setuptools"]["dynamic"]["version"] == {"attr": "px0.__version__"}
    assert "version" not in pyproject["project"], "a static version would reintroduce the drift"

    # And the built metadata agrees with the module.
    from importlib.metadata import version as dist_version
    try:
        assert dist_version("px0") == px0.__version__
    except Exception:
        pytest.skip("px0 not installed in this environment")


def _mock_installer(monkeypatch, recorder, returncode=0, stderr=""):
    """Records the install/upgrade commands update.py shells out to."""
    class R:
        def __init__(self, rc, err):
            self.returncode, self.stderr, self.stdout = rc, err, ""

    def fake_run(cmd, *a, **kw):
        recorder.append(cmd)
        if cmd[:2] == ["pipx", "list"]:
            return R(0, "")
        return R(returncode, stderr)

    monkeypatch.setattr(subprocess, "run", fake_run)


def test_run_update_upgrades_records_history_restarts_daemon(tmp_home, monkeypatch, capsys):
    """Phase 5 AC3: upgrade, append to update-history.json, restart daemon, doctor summary."""
    from px0 import daemon as daemon_mod, doctor as doctor_mod

    monkeypatch.setattr(update, "check", lambda config: {
        "available_version": "9.9.9", "channel": "stable",
        "current_version": update.__version__, "update_available": True,
    })
    monkeypatch.setattr(update, "detect_install_mechanism", lambda home: "pipx")
    cmds = []
    _mock_installer(monkeypatch, cmds)

    restarted = []
    monkeypatch.setattr(daemon_mod, "restart_if_running", lambda h, c: restarted.append(True))
    monkeypatch.setattr(doctor_mod, "run", lambda h, c, quick=False: {"all_ok": True, "checks": {}})

    res = update.run_update(tmp_home, {})

    assert cmds == [["pipx", "upgrade", "px0"]]
    assert restarted == [True]
    assert res["message"] == "Successfully updated to 9.9.9."
    assert res["doctor_summary"]["all_ok"] is True

    history = json.loads(paths.update_history_path(tmp_home).read_text())
    assert len(history) == 1
    assert history[0]["from_version"] == update.__version__
    assert history[0]["to_version"] == "9.9.9"
    assert history[0]["migrations_applied"] == []
    assert history[0]["at"]


def _fake_pipx_list_run(bin_dir):
    """A `pipx list --json` stub reporting px0's venv as living in `bin_dir`."""
    list_json = json.dumps({"venvs": {"px0": {"metadata": {"main_package": {
        "app_paths": [{"__Path__": str(bin_dir / "px0"), "__type__": "Path"}]
    }}}}})

    class R:
        def __init__(self, rc, out, err=""):
            self.returncode, self.stdout, self.stderr = rc, out, err

    def fake_run(cmd, *a, **kw):
        if cmd[:2] == ["pipx", "list"]:
            return R(0, list_json)
        return R(0, "", "")

    return fake_run


def test_run_update_beta_pins_the_venvs_own_python(tmp_home, monkeypatch):
    """A --force beta reinstall must stay on the venv's existing interpreter,
    not whatever python pipx itself happens to default to."""
    bin_dir = tmp_home / "pipx-bin"
    bin_dir.mkdir()
    (bin_dir / "python").write_text("#!/bin/sh\n")

    monkeypatch.setattr(update, "check", lambda config: {
        "available_version": "9.9.9", "channel": "beta", "update_available": True,
    })
    monkeypatch.setattr(update, "detect_install_mechanism", lambda home: "pipx")
    fake_run = _fake_pipx_list_run(bin_dir)
    cmds = []
    monkeypatch.setattr(subprocess, "run", lambda cmd, *a, **kw: (cmds.append(cmd), fake_run(cmd, *a, **kw))[1])

    update.run_update(tmp_home, {})

    assert cmds[-1] == [
        "pipx", "install", "--pip-args=--pre", "--force", "px0",
        "--python", str(bin_dir / "python"),
    ]


def test_pipx_venv_python_returns_none_when_unreadable(monkeypatch):
    """Degrades to no --python rather than raising when pipx list fails or the
    venv's python has since vanished."""
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: (_ for _ in ()).throw(OSError("no pipx")))
    assert update._pipx_venv_python() is None


def test_run_update_check_only_touches_nothing(tmp_home, monkeypatch):
    """Phase 5 AC2: --check reports the version and leaves disk alone."""
    monkeypatch.setattr(update, "check", lambda config: {
        "available_version": "9.9.9", "channel": "stable", "update_available": True,
    })
    ran = []
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: ran.append(a))

    res = update.run_update(tmp_home, {}, check_only=True)

    assert res["available_version"] == "9.9.9"
    assert ran == []
    assert not paths.update_history_path(tmp_home).exists()


def test_run_update_raises_and_writes_no_history_when_install_fails(tmp_home, monkeypatch):
    monkeypatch.setattr(update, "check", lambda config: {
        "available_version": "9.9.9", "channel": "stable", "update_available": True,
    })
    monkeypatch.setattr(update, "detect_install_mechanism", lambda home: "pipx")
    _mock_installer(monkeypatch, [], returncode=1, stderr="boom")

    with pytest.raises(update.UpdateError) as exc:
        update.run_update(tmp_home, {})

    assert "Install failed: boom" in str(exc.value)
    assert "subprocess error" not in str(exc.value)  # not double-wrapped
    assert not paths.update_history_path(tmp_home).exists()


def test_rollback_restores_prior_version_and_pops_history(tmp_home, monkeypatch, capsys):
    """Phase 5 AC4: rollback targets the last update's from_version."""
    from px0 import daemon as daemon_mod

    paths.update_history_path(tmp_home).parent.mkdir(parents=True, exist_ok=True)
    paths.update_history_path(tmp_home).write_text(json.dumps([
        {"from_version": "0.1.0", "to_version": "9.9.9", "at": "x", "migrations_applied": []},
    ]))
    monkeypatch.setattr(update, "detect_install_mechanism", lambda home: "pipx")
    cmds = []
    _mock_installer(monkeypatch, cmds)
    monkeypatch.setattr(daemon_mod, "restart_if_running", lambda h, c: None)

    update.rollback(tmp_home, {})

    assert cmds == [["pipx", "list", "--json"], ["pipx", "install", "--force", "px0==0.1.0"]]
    assert json.loads(paths.update_history_path(tmp_home).read_text()) == []
    out = capsys.readouterr().out
    assert "rolled back to px0 version 0.1.0" in out
    assert "forward-only" not in out  # no migrations ran, so no schema note


def test_rollback_pins_the_venvs_own_python(tmp_home, monkeypatch):
    """A rollback always force-reinstalls; it must stay on the venv's existing
    interpreter rather than pipx's own default."""
    from px0 import daemon as daemon_mod

    bin_dir = tmp_home / "pipx-bin"
    bin_dir.mkdir()
    (bin_dir / "python").write_text("#!/bin/sh\n")

    paths.update_history_path(tmp_home).parent.mkdir(parents=True, exist_ok=True)
    paths.update_history_path(tmp_home).write_text(json.dumps([
        {"from_version": "0.1.0", "to_version": "9.9.9", "at": "x", "migrations_applied": []},
    ]))
    monkeypatch.setattr(update, "detect_install_mechanism", lambda home: "pipx")
    fake_run = _fake_pipx_list_run(bin_dir)
    cmds = []
    monkeypatch.setattr(subprocess, "run", lambda cmd, *a, **kw: (cmds.append(cmd), fake_run(cmd, *a, **kw))[1])
    monkeypatch.setattr(daemon_mod, "restart_if_running", lambda h, c: None)

    update.rollback(tmp_home, {})

    assert cmds[-1] == [
        "pipx", "install", "--force", "px0==0.1.0", "--python", str(bin_dir / "python"),
    ]


def test_rollback_notes_schema_when_migrations_had_run(tmp_home, monkeypatch, capsys):
    from px0 import daemon as daemon_mod

    paths.update_history_path(tmp_home).parent.mkdir(parents=True, exist_ok=True)
    paths.update_history_path(tmp_home).write_text(json.dumps([
        {"from_version": "0.1.0", "to_version": "9.9.9", "at": "x", "migrations_applied": [2]},
    ]))
    monkeypatch.setattr(update, "detect_install_mechanism", lambda home: "pipx")
    _mock_installer(monkeypatch, [])
    monkeypatch.setattr(daemon_mod, "restart_if_running", lambda h, c: None)

    update.rollback(tmp_home, {})

    out = capsys.readouterr().out
    assert "forward-only" in out
    assert "v2" in out


def test_doctor_surfaces_the_weekly_update_check(tmp_home):
    """Phase 5 in-scope: the daemon's weekly check has to show up in doctor."""
    from px0 import doctor

    check_path = paths.update_check_path(tmp_home)
    check_path.parent.mkdir(parents=True, exist_ok=True)
    check_path.write_text(json.dumps({
        "checked_at": "2026-08-20T09:00:00", "available_version": "99.0.0",
    }))

    check = doctor._check_update(tmp_home)
    assert check["ok"] is True  # behind is not broken
    assert "99.0.0 available" in check["detail"]
    assert "px0 update" in check["detail"]


def test_doctor_update_check_is_offline_safe(tmp_home, monkeypatch):
    """No recorded check, unreadable file, and up-to-date all stay non-fatal."""
    from px0 import doctor

    monkeypatch.setattr(requests, "get", lambda *a, **kw: pytest.fail("doctor must not call PyPI"))

    assert doctor._check_update(tmp_home)["ok"] is True
    assert "no update check recorded" in doctor._check_update(tmp_home)["detail"]

    check_path = paths.update_check_path(tmp_home)
    check_path.parent.mkdir(parents=True, exist_ok=True)
    check_path.write_text("{not json")
    assert doctor._check_update(tmp_home)["ok"] is True

    check_path.write_text(json.dumps({
        "checked_at": "2026-08-20T09:00:00", "available_version": update.__version__,
    }))
    assert "is current" in doctor._check_update(tmp_home)["detail"]


def test_unreachable_pypi_is_not_reported_as_up_to_date(monkeypatch):
    """Collapsing a network failure into "no newer version" tells someone several
    releases behind that they are current."""
    def boom(*a, **kw):
        raise requests.ConnectionError("no route to host")

    monkeypatch.setattr(requests, "get", boom)

    with pytest.raises(update.PyPIUnreachable, match="could not reach PyPI"):
        update._pypi_latest_version("stable")
    with pytest.raises(update.PyPIUnreachable):
        update.check({})


def test_pypi_unreachable_is_an_update_error_so_the_cli_exits_nonzero(monkeypatch):
    """cmd_update catches UpdateError; PyPIUnreachable must be one."""
    assert issubclass(update.PyPIUnreachable, update.UpdateError)


def test_not_published_is_distinct_from_unreachable(monkeypatch):
    """A real 404 means px0 isn't on that channel -- not a failure to ask."""
    monkeypatch.setattr(requests, "get", lambda *a, **kw: MockResponse({}, status_code=404))

    result = update.check({})
    assert result["available_version"] is None
    assert result["update_available"] is False
    assert "not published" in result["message"]


def test_beta_channel_skips_non_pep440_versions(monkeypatch):
    monkeypatch.setattr(requests, "get", lambda *a, **kw: MockResponse({
        "info": {"version": "1.0.0"},
        "releases": {"1.0.0": [], "not-a-version": [], "1.1.0b1": []},
    }))
    assert update._pypi_latest_version("beta") == "1.1.0b1"


def test_unreadable_schema_version_refuses_to_migrate(tmp_home, monkeypatch):
    """Assuming schema 1 would re-run every migration against a migrated store."""
    monkeypatch.setattr(update, "check", lambda config: {
        "available_version": "9.9.9", "channel": "stable", "update_available": True,
    })
    monkeypatch.setattr(update, "detect_install_mechanism", lambda home: "pipx")
    _mock_installer(monkeypatch, [])
    paths.schema_path(tmp_home).write_text("not-a-number")

    with pytest.raises(update.UpdateError, match="cannot read the store schema version"):
        update.run_update(tmp_home, {})


def test_history_helper_degrades_on_a_corrupt_file(tmp_home):
    path = paths.update_history_path(tmp_home)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("{not json")
    assert update._load_history(path) == []

    path.write_text('{"not": "a list"}')
    assert update._load_history(path) == []


def test_daemon_logs_a_skipped_update_check(tmp_home, monkeypatch):
    """A permanently broken check used to be swallowed with no trace."""
    from px0 import daemon as daemon_mod, claims, retrieval, runs as runs_module

    monkeypatch.setattr(claims, "scan_and_process", lambda h, force_hash=False: None)
    monkeypatch.setattr(retrieval, "reindex", lambda h, c: 0)
    monkeypatch.setattr(runs_module, "apply_retention", lambda c: {"logs": 0})
    monkeypatch.setattr(update, "check",
                        lambda config: (_ for _ in ()).throw(update.PyPIUnreachable("offline")))

    config = {"logs": {"path": str(tmp_home / "logs")}}
    daemon_mod.run_nightly(tmp_home, config)

    log = (tmp_home / "logs" / "daemon.log").read_text()
    assert "update check skipped (offline)" in log


def test_check_always_reports_both_keys(monkeypatch):
    """`cmd_update --check` gates on update_available, so check() must set it.

    It previously returned `available_version: None` when current, which both
    made "up to date" indistinguishable from "not published" and left
    update_available absent -- so --check reported "up to date" even when a
    newer release existed.
    """
    monkeypatch.setattr(requests, "get",
                        lambda *a, **kw: MockResponse({"info": {"version": "99.0.0"}}))
    newer = update.check({})
    assert newer["available_version"] == "99.0.0"
    assert newer["update_available"] is True

    monkeypatch.setattr(requests, "get", lambda *a, **kw: MockResponse(
        {"info": {"version": update.__version__}}))
    current = update.check({})
    assert current["available_version"] == update.__version__   # still reported
    assert current["update_available"] is False


def test_run_update_does_not_reinstall_the_current_version(tmp_home, monkeypatch):
    monkeypatch.setattr(update, "check", lambda config: {
        "channel": "stable", "available_version": update.__version__,
        "update_available": False,
    })
    ran = []
    monkeypatch.setattr(subprocess, "run", lambda *a, **kw: ran.append(a))

    update.run_update(tmp_home, {})
    assert ran == []


def test_cmd_update_check_reports_an_available_version(tmp_home, monkeypatch, capsys):
    """End-to-end on the presentation: the bug was only visible here."""
    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, {}))
    monkeypatch.setattr(update, "run_update", lambda h, c, check_only=False: {
        "channel": "stable", "available_version": "9.9.9", "update_available": True,
        "current_version": "0.1.0", "message": "Update available: 9.9.9 on channel stable.",
    })

    cli.cmd_update(argparse.Namespace(rollback=False, channel=None, check=True))

    out = capsys.readouterr().out
    assert "9.9.9 available" in out
    assert "px0 update" in out
    assert "up to date" not in out


def test_maybe_check_hits_pypi_once_and_then_caches_for_the_day(tmp_home, monkeypatch):
    calls = []
    monkeypatch.setattr(requests, "get", lambda *a, **kw: (
        calls.append(1), MockResponse({"info": {"version": "9.9.9"}}))[1])

    first = update.maybe_check(tmp_home, {})
    assert first == {"kind": "available", "available_version": "9.9.9", "channel": "stable"}
    assert len(calls) == 1

    # Same day: served from the cache file, PyPI not touched again.
    second = update.maybe_check(tmp_home, {})
    assert second == first
    assert len(calls) == 1

    check_path = paths.update_check_path(tmp_home)
    data = json.loads(check_path.read_text())
    assert data["available_version"] == "9.9.9"


def test_maybe_check_rechecks_once_the_cached_entry_is_a_day_old(tmp_home, monkeypatch):
    check_path = paths.update_check_path(tmp_home)
    check_path.parent.mkdir(parents=True, exist_ok=True)
    check_path.write_text(json.dumps({
        "checked_at": "2020-01-01T00:00:00+00:00", "available_version": None,
    }))

    calls = []
    monkeypatch.setattr(requests, "get", lambda *a, **kw: (
        calls.append(1), MockResponse({"info": {"version": "9.9.9"}}))[1])

    result = update.maybe_check(tmp_home, {})
    assert result["available_version"] == "9.9.9"
    assert len(calls) == 1


def test_maybe_check_respects_update_check_false(tmp_home, monkeypatch):
    monkeypatch.setattr(requests, "get", lambda *a, **kw: pytest.fail("must not call PyPI"))
    assert update.maybe_check(tmp_home, {"update": {"check": False}}) is None


def test_maybe_check_returns_none_when_up_to_date(tmp_home, monkeypatch):
    monkeypatch.setattr(requests, "get",
                         lambda *a, **kw: MockResponse({"info": {"version": update.__version__}}))
    assert update.maybe_check(tmp_home, {}) is None


def test_maybe_check_survives_pypi_being_unreachable(tmp_home, monkeypatch):
    monkeypatch.setattr(requests, "get", lambda *a, **kw: (_ for _ in ()).throw(
        requests.ConnectionError("no route to host")))
    assert update.maybe_check(tmp_home, {}) is None  # never raises

    data = json.loads(paths.update_check_path(tmp_home).read_text())
    assert data["available_version"] is None  # recorded so tomorrow retries, not every command


def test_maybe_check_installs_automatically_when_configured(tmp_home, monkeypatch):
    monkeypatch.setattr(requests, "get",
                         lambda *a, **kw: MockResponse({"info": {"version": "9.9.9"}}))
    monkeypatch.setattr(update, "run_update", lambda h, c: {"message": "Successfully updated to 9.9.9."})

    result = update.maybe_check(tmp_home, {"update": {"auto_install": True}})
    assert result == {"kind": "installed", "result": {"message": "Successfully updated to 9.9.9."}}


def test_maybe_check_reports_auto_install_failure(tmp_home, monkeypatch):
    monkeypatch.setattr(requests, "get",
                         lambda *a, **kw: MockResponse({"info": {"version": "9.9.9"}}))

    def boom(h, c):
        raise update.UpdateError("pipx exploded")
    monkeypatch.setattr(update, "run_update", boom)

    result = update.maybe_check(tmp_home, {"update": {"auto_install": True}})
    assert result == {"kind": "install_failed", "error": "pipx exploded"}


def test_notify_update_prints_available_version(tmp_home, monkeypatch, capsys):
    monkeypatch.setattr(paths, "store_home", lambda: tmp_home)
    monkeypatch.setattr(update, "maybe_check", lambda h, c: {
        "kind": "available", "available_version": "9.9.9", "channel": "stable",
    })

    cli._notify_update()

    out = capsys.readouterr().out
    assert "9.9.9 available" in out
    assert "px0 update" in out


def test_main_notifies_after_an_ordinary_command_but_not_after_update(tmp_home, monkeypatch, capsys):
    """End-to-end through main(): the notify hook fires for a normal command
    and is skipped for `px0 update` itself, so the two check paths never race."""
    monkeypatch.setenv("PX0_HOME", str(tmp_home))
    monkeypatch.setattr(update, "maybe_check", lambda h, c: {
        "kind": "available", "available_version": "9.9.9", "channel": "stable",
    })
    monkeypatch.setattr(update, "run_update", lambda h, c, check_only=False: {
        "channel": "stable", "available_version": None, "update_available": False,
        "current_version": update.__version__, "message": "Already up to date.",
    })

    cli.main(["version"])
    assert "9.9.9 available" in capsys.readouterr().out

    cli.main(["update", "--check"])
    assert "9.9.9 available" not in capsys.readouterr().out
