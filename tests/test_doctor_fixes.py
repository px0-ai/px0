"""Every red line in `px0 doctor` must carry the step that clears it.

These tests assert the `fix` text exists and names the right command, not its
exact wording -- the point is that a failing check is actionable, and pinning
prose would make every copy edit a test failure.
"""

import fcntl
import json

import pytest

from px0 import SCHEMA_VERSION, cli, doctor, paths, retrieval, ui


# --- checks that fail with a fix -------------------------------------------

def test_loose_credentials_name_the_chmod_and_the_path(tmp_home):
    path = paths.credentials_path(tmp_home)
    path.write_text('[gmail]\nkind = "composio"\n')
    path.chmod(0o644)

    res = doctor._check_credentials(tmp_home)

    assert res["ok"] is False
    assert "chmod 600" in res["fix"] and str(path) in res["fix"]


def test_tight_credentials_carry_no_fix(tmp_home):
    """A passing check must not suggest anything -- there is nothing to do."""
    path = paths.credentials_path(tmp_home)
    path.write_text("[gmail]\n")
    path.chmod(0o600)

    res = doctor._check_credentials(tmp_home)

    assert res["ok"] is True and "fix" not in res


def test_empty_index_over_existing_knowledge_points_at_reindex(tmp_home):
    """The reported case: files on disk, nothing indexed, no hint what to run."""
    (tmp_home / "knowledge").mkdir(exist_ok=True)
    (tmp_home / "knowledge" / "note.md").write_text("# note\n\nbody\n")

    res = doctor._check_index(tmp_home, {})

    assert res["ok"] is False
    assert "px0 knowledge reindex" in res["fix"]


def test_a_populated_index_passes_with_no_fix(tmp_home):
    (tmp_home / "knowledge").mkdir(exist_ok=True)
    (tmp_home / "knowledge" / "note.md").write_text("# note\n\nbody\n")
    retrieval.reindex(tmp_home, {})

    res = doctor._check_index(tmp_home, {})

    assert res["ok"] is True and "fix" not in res


def test_an_uninitialized_store_is_told_to_init(tmp_home):
    paths.schema_path(tmp_home).unlink()

    res = doctor._check_schema(tmp_home)

    assert res["ok"] is False and "px0 init" in res["fix"]


def test_an_older_store_migrates_forward_but_a_newer_one_cannot():
    """Direction decides the fix: only one of the two can be migrated."""
    import tempfile
    from pathlib import Path
    from px0 import store

    for offset, expected in ((-1, "px0 update"), (+1, "newer px0")):
        with tempfile.TemporaryDirectory() as d:
            home = Path(d) / "h"
            home.mkdir()
            store.init(home)
            paths.schema_path(home).write_text(str(SCHEMA_VERSION + offset))

            res = doctor._check_schema(home)

            assert res["ok"] is False
            assert expected in res["fix"], (offset, res["fix"])


def test_a_held_lock_says_wait_then_names_the_file_to_delete(tmp_home):
    lock = paths.lock_path(tmp_home)
    lock.parent.mkdir(parents=True, exist_ok=True)
    lock.write_text("")
    holder = open(lock, "w")
    fcntl.flock(holder, fcntl.LOCK_EX | fcntl.LOCK_NB)
    try:
        res = doctor._check_locks(tmp_home)
    finally:
        holder.close()

    assert res["ok"] is False
    assert "wait" in res["fix"] and str(lock) in res["fix"]


def test_a_stale_connection_is_told_which_service_to_reauthorize(tmp_home):
    path = paths.credentials_path(tmp_home)
    path.write_text('[gmail]\nkind = "composio"\nstatus = "INITIATED"\n')
    path.chmod(0o600)

    res = doctor._check_connections(tmp_home)

    assert res["ok"] is False and "gmail" in res["fix"]


def test_a_qmd_problem_offers_the_pin_and_the_way_out():
    """Both halves matter: the local backend needs no install at all."""
    fix = doctor._qmd_install_fix()

    assert retrieval.QMD_PINNED_VERSION in fix
    assert "retrieval.backend local" in fix


@pytest.mark.parametrize("error, expected", [
    ("harness command not found: 'claude -p'", "on PATH"),
    ("harness timed out after 20s", "responds on its own"),
    ("harness exited 1: bad model", "by hand"),
])
def test_each_harness_failure_mode_gets_its_own_fix(error, expected):
    assert expected in doctor._harness_fix(error, "claude -p")


# --- the checks that are informational stay quiet ---------------------------

def test_checks_that_never_fail_never_carry_a_fix(tmp_home):
    """update / unreferenced_guidelines / daemon are notes, not failures."""
    for res in (doctor._check_update(tmp_home),
                doctor._check_unreferenced_guidelines(tmp_home)):
        assert res["ok"] is True and "fix" not in res


# --- rendering --------------------------------------------------------------

def test_the_fix_is_printed_under_the_line_that_failed(monkeypatch, capsys):
    """A red line the reader can't act on is the whole complaint about doctor."""
    monkeypatch.setattr(ui, "spinner", _quiet_spinner)
    monkeypatch.setattr(cli, "_ctx", lambda: (paths.store_home(), {}))
    monkeypatch.setattr(doctor, "run", lambda *a, **k: {
        "px0_version": "0.0.0",
        "all_ok": False,
        "checks": {
            "index": {"ok": False, "detail": "8 files, 0 passages",
                      "fix": "build the index: px0 knowledge reindex"},
            "locks": {"ok": True, "detail": "lock is free"},
        },
    })

    with pytest.raises(SystemExit) as exc:
        cli.cmd_doctor(_Args())

    out = capsys.readouterr().out
    lines = [l for l in out.splitlines() if l.strip()]
    failing = next(i for i, l in enumerate(lines) if "8 files" in l)
    assert "px0 knowledge reindex" in lines[failing + 1]
    assert exc.value.code != 0


def test_a_passing_check_gets_no_extra_line(monkeypatch, capsys):
    monkeypatch.setattr(ui, "spinner", _quiet_spinner)
    monkeypatch.setattr(cli, "_ctx", lambda: (paths.store_home(), {}))
    monkeypatch.setattr(doctor, "run", lambda *a, **k: {
        "px0_version": "0.0.0", "all_ok": True,
        "checks": {"locks": {"ok": True, "detail": "lock is free"}},
    })

    with pytest.raises(SystemExit) as exc:
        cli.cmd_doctor(_Args())

    assert "→" not in capsys.readouterr().out
    assert exc.value.code == 0


def test_json_output_carries_the_fix_for_machine_callers(monkeypatch, capsys):
    monkeypatch.setattr(cli, "_ctx", lambda: (paths.store_home(), {}))
    monkeypatch.setattr(doctor, "run", lambda *a, **k: {
        "px0_version": "0.0.0", "all_ok": False,
        "checks": {"index": {"ok": False, "detail": "d", "fix": "px0 knowledge reindex"}},
    })

    with pytest.raises(SystemExit):
        cli.cmd_doctor(_Args(json=True))

    payload = json.loads(capsys.readouterr().out)
    assert payload["checks"]["index"]["fix"] == "px0 knowledge reindex"


class _Args:
    def __init__(self, json=False, quick=True):
        self.json = json
        self.quick = quick


class _quiet_spinner:
    def __init__(self, *a, **k): pass
    def __enter__(self): return self
    def __exit__(self, *a): return False
