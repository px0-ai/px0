"""`px0 uninstall` asks separately for each irreversible thing it can remove
(daemon/scheduler unit, the store, the package), so declining one doesn't
block the others."""

import argparse
import subprocess

import pytest

from px0 import cli, daemon as daemon_mod


def _answer_queue(answers):
    """A stand-in for `ui.prompt` that returns one answer per call, in order."""
    it = iter(answers)
    return lambda *a, **k: next(it)


def _stub_run(calls):
    return lambda cmd, **kw: (calls.append(cmd), subprocess.CompletedProcess(cmd, 0, "", ""))[1]


def test_uninstall_asks_separately_and_keeps_everything_declined(tmp_home, monkeypatch, capsys):
    monkeypatch.setattr(cli.sys.stdin, "isatty", lambda: True, raising=False)
    monkeypatch.setattr(cli.ui, "prompt", _answer_queue(["n", "n", "n"]))
    monkeypatch.setattr(cli.paths, "store_home", lambda: tmp_home)
    monkeypatch.setattr(cli.update_mod, "detect_install_mechanism", lambda home: "pipx")
    monkeypatch.setattr(daemon_mod, "uninstall", lambda home: pytest.fail("declined step ran"))
    monkeypatch.setattr(cli.subprocess, "run", lambda *a, **kw: pytest.fail("declined step ran"))

    cli.cmd_uninstall(argparse.Namespace(yes=False))

    assert tmp_home.exists()
    out = capsys.readouterr().out
    assert "kept" in out
    assert "daemon / scheduler unit" in out
    assert "the store" in out
    assert "the px0 package" in out


def test_uninstall_runs_each_confirmed_step(tmp_home, monkeypatch, capsys):
    monkeypatch.setattr(cli.sys.stdin, "isatty", lambda: True, raising=False)
    monkeypatch.setattr(cli.ui, "prompt", _answer_queue(["y", "y", "y"]))
    monkeypatch.setattr(cli.paths, "store_home", lambda: tmp_home)
    monkeypatch.setattr(cli.update_mod, "detect_install_mechanism", lambda home: "pipx")

    daemon_calls = []
    monkeypatch.setattr(daemon_mod, "uninstall", lambda home: (daemon_calls.append(home), {
        "stopped": True, "removed": ["/fake/unit"], "cron_note": None,
    })[1])
    run_calls = []
    monkeypatch.setattr(cli.subprocess, "run", _stub_run(run_calls))

    cli.cmd_uninstall(argparse.Namespace(yes=False))

    assert daemon_calls == [tmp_home]
    assert not tmp_home.exists()
    assert run_calls == [["pipx", "uninstall", "px0"]]
    out = capsys.readouterr().out
    assert "px0 is uninstalled" in out
    assert "kept" not in out


def test_uninstall_can_decline_the_store_but_still_remove_the_package(tmp_home, monkeypatch, capsys):
    monkeypatch.setattr(cli.sys.stdin, "isatty", lambda: True, raising=False)
    monkeypatch.setattr(cli.ui, "prompt", _answer_queue(["y", "n", "y"]))  # daemon, store, package
    monkeypatch.setattr(cli.paths, "store_home", lambda: tmp_home)
    monkeypatch.setattr(cli.update_mod, "detect_install_mechanism", lambda home: "pipx")
    monkeypatch.setattr(daemon_mod, "uninstall", lambda home: {
        "stopped": False, "removed": [], "cron_note": None,
    })
    run_calls = []
    monkeypatch.setattr(cli.subprocess, "run", _stub_run(run_calls))

    cli.cmd_uninstall(argparse.Namespace(yes=False))

    assert tmp_home.exists()  # declined
    assert run_calls == [["pipx", "uninstall", "px0"]]  # confirmed

    kept_lines = [line for line in capsys.readouterr().out.splitlines() if "kept" in line]
    assert len(kept_lines) == 1
    assert "the store" in kept_lines[0]
    assert "daemon" not in kept_lines[0]
    assert "px0 package" not in kept_lines[0]


def test_uninstall_with_yes_skips_every_prompt(tmp_home, monkeypatch, capsys):
    monkeypatch.setattr(cli.ui, "prompt", lambda *a, **k: pytest.fail("must not prompt with --yes"))
    monkeypatch.setattr(cli.paths, "store_home", lambda: tmp_home)
    monkeypatch.setattr(cli.update_mod, "detect_install_mechanism", lambda home: "pipx")
    monkeypatch.setattr(daemon_mod, "uninstall", lambda home: {
        "stopped": False, "removed": [], "cron_note": None,
    })
    monkeypatch.setattr(cli.subprocess, "run",
                         lambda cmd, **kw: subprocess.CompletedProcess(cmd, 0, "", ""))

    cli.cmd_uninstall(argparse.Namespace(yes=True))

    assert not tmp_home.exists()
    assert "px0 is uninstalled" in capsys.readouterr().out


def test_uninstall_without_a_store_only_asks_about_the_package(tmp_path, monkeypatch, capsys):
    missing_home = tmp_path / "no_such_store"
    monkeypatch.setattr(cli.paths, "store_home", lambda: missing_home)
    monkeypatch.setattr(cli.update_mod, "detect_install_mechanism", lambda home: "pip")
    monkeypatch.setattr(daemon_mod, "uninstall", lambda home: pytest.fail("no store to touch"))
    monkeypatch.setattr(cli.ui, "prompt", _answer_queue(["y"]))
    monkeypatch.setattr(cli.sys.stdin, "isatty", lambda: True, raising=False)
    run_calls = []
    monkeypatch.setattr(cli.subprocess, "run", _stub_run(run_calls))

    cli.cmd_uninstall(argparse.Namespace(yes=False))

    assert run_calls == [[cli.sys.executable, "-m", "pip", "uninstall", "-y", "px0"]]
    assert "px0 is uninstalled" in capsys.readouterr().out
