"""A scheduled run that fails has to tell someone.

Before this, a failure wrote a record and stopped: no notification, no retry,
and nothing to cancel a run with. These tests pin the three halves of that --
the retry policy, the notification, and the in-flight marker -- because each one
is only useful if it holds without anyone watching.
"""

import argparse

import pytest

from px0 import (config as config_mod, notify as notify_mod, paths, runner,
                 runs as runs_mod, workflow as wf_mod)


def _write(home, wf_id="demo", extra=""):
    text = ("---\n"
            f"id: {wf_id}\n"
            "description: A demo\n"
            f"{extra}"
            "output:\n  target: stdout\n"
            "---\n\nBody.\n")
    (paths.workflows_dir(home) / f"{wf_id}.md").write_text(text)
    return wf_mod.load(home, wf_id)


@pytest.fixture
def config(tmp_home):
    return config_mod.load(paths.config_path(tmp_home))


# --- retry policy --------------------------------------------------------

def test_a_workflow_with_no_policy_is_attempted_once(tmp_home, config):
    wf = _write(tmp_home)
    assert wf_mod.retry_policy(wf, config) == (1, 30)


def test_a_workflows_own_policy_beats_the_config(tmp_home, config):
    wf = _write(tmp_home, extra="retry:\n  max_attempts: 3\n  backoff_seconds: 5\n")
    assert wf_mod.retry_policy(wf, config) == (3, 5.0)


def test_the_config_supplies_the_default_when_a_workflow_says_nothing(tmp_home, config):
    config_mod.set_key(config, "runs.max_attempts", "4")
    wf = _write(tmp_home)
    assert wf_mod.retry_policy(wf, config)[0] == 4


@pytest.mark.parametrize("attempts", [0, -1, "many", 99])
def test_a_nonsense_attempt_count_does_not_become_an_infinite_loop(tmp_home, config, attempts):
    wf = _write(tmp_home, extra=f"retry:\n  max_attempts: {attempts}\n")
    resolved, _backoff = wf_mod.retry_policy(wf, config)
    assert 1 <= resolved <= wf_mod.MAX_ATTEMPTS


def test_a_capped_attempt_count_fails_validation(tmp_home):
    wf = _write(tmp_home, extra=f"retry:\n  max_attempts: {wf_mod.MAX_ATTEMPTS + 1}\n")
    assert any("capped" in e for e in wf_mod.validate(wf, tmp_home))


def test_a_failing_run_is_retried_and_each_attempt_is_recorded(tmp_home, config, monkeypatch):
    _write(tmp_home, extra="retry:\n  max_attempts: 3\n  backoff_seconds: 0\n")
    attempts = []

    def fail(*a, **k):
        attempts.append(1)
        raise runner.RunError("nope", {"id": f"r{len(attempts)}", "workflow_id": "demo"})

    monkeypatch.setattr(runner, "_run_once", fail)
    monkeypatch.setattr(runner.time, "sleep", lambda *_: None)

    with pytest.raises(runner.RunError):
        runner.run(tmp_home, config, "demo")

    assert len(attempts) == 3


def test_no_retry_stops_after_one_attempt(tmp_home, config, monkeypatch):
    _write(tmp_home, extra="retry:\n  max_attempts: 3\n  backoff_seconds: 0\n")
    attempts = []
    monkeypatch.setattr(runner, "_run_once",
                        lambda *a, **k: attempts.append(1) or (_ for _ in ()).throw(
                            runner.RunError("nope", {})))

    with pytest.raises(runner.RunError):
        runner.run(tmp_home, config, "demo", retry=False)

    assert len(attempts) == 1


def test_a_succeeding_attempt_stops_the_loop(tmp_home, config, monkeypatch):
    _write(tmp_home, extra="retry:\n  max_attempts: 3\n  backoff_seconds: 0\n")
    calls = []

    def flaky(*a, **k):
        calls.append(1)
        if len(calls) == 1:
            raise runner.RunError("transient", {})
        return {"id": "r2", "outcome": "success", "output": {}}

    monkeypatch.setattr(runner, "_run_once", flaky)
    monkeypatch.setattr(runner.time, "sleep", lambda *_: None)

    record = runner.run(tmp_home, config, "demo")

    assert record["outcome"] == "success"
    assert len(calls) == 2


# --- notification --------------------------------------------------------

def test_the_default_policy_stays_silent(tmp_home, config):
    result = notify_mod.on_failure(tmp_home, config, {"workflow_id": "demo", "id": "r1"})
    assert result["notified"] is False
    assert result["channel"] == "none"


def test_a_workflows_block_overrides_the_config(tmp_home, config, monkeypatch):
    sent = []
    monkeypatch.setattr(notify_mod, "_desktop", lambda t, b: (sent.append((t, b)), (True, "desktop"))[1])

    result = notify_mod.on_failure(tmp_home, config, {"workflow_id": "demo", "id": "r1"},
                                    {"notify": "desktop"})

    assert result["notified"] is True
    assert sent


def test_a_tool_channel_sends_through_the_named_write_tool(tmp_home, config, monkeypatch):
    from px0 import tools

    calls = []
    monkeypatch.setattr(tools, "call", lambda home, cfg, tool, args: calls.append((tool, args)))
    config_mod.set_key(config, "notify.on_failure", "tool")
    config_mod.set_key(config, "notify.channel", "slack.post_message")
    config_mod.set_key(config, "notify.target", "#alerts")

    result = notify_mod.on_failure(tmp_home, config,
                                   {"workflow_id": "demo", "id": "r1", "error": "boom"})

    assert result["notified"] is True
    assert calls[0][0] == "slack.post_message"
    assert calls[0][1]["channel"] == "#alerts"
    assert "boom" in calls[0][1]["text"]


def test_a_tool_that_cannot_carry_a_message_is_refused_with_the_list(tmp_home, config):
    config_mod.set_key(config, "notify.on_failure", "tool")
    config_mod.set_key(config, "notify.channel", "github.get_pr")
    config_mod.set_key(config, "notify.target", "x")

    result = notify_mod.on_failure(tmp_home, config, {"workflow_id": "d", "id": "r"})

    assert result["notified"] is False
    assert "slack.post_message" in result["detail"]


def test_a_failing_notification_never_masks_the_failure_it_reports(tmp_home, config, monkeypatch):
    from px0 import tools

    monkeypatch.setattr(tools, "call",
                        lambda *a, **k: (_ for _ in ()).throw(tools.ConnectorError("slack down")))
    config_mod.set_key(config, "notify.on_failure", "tool")
    config_mod.set_key(config, "notify.channel", "slack.post_message")
    config_mod.set_key(config, "notify.target", "#alerts")

    result = notify_mod.on_failure(tmp_home, config, {"workflow_id": "d", "id": "r"})

    assert result["notified"] is False
    assert "slack down" in result["detail"]


def test_an_unknown_channel_in_frontmatter_fails_validation(tmp_home):
    wf = _write(tmp_home, extra="on_failure:\n  notify: carrier-pigeon\n")
    assert any("on_failure.notify" in e for e in wf_mod.validate(wf, tmp_home))


def test_a_tool_channel_with_no_target_fails_validation(tmp_home):
    wf = _write(tmp_home,
                extra="on_failure:\n  notify: tool\n  channel: slack.post_message\n")
    assert any("target" in e for e in wf_mod.validate(wf, tmp_home))


def test_the_last_failed_attempt_notifies_exactly_once(tmp_home, config, monkeypatch):
    _write(tmp_home, extra="retry:\n  max_attempts: 2\n  backoff_seconds: 0\n"
                            "on_failure:\n  notify: desktop\n")
    sent = []
    monkeypatch.setattr(notify_mod, "_desktop", lambda t, b: (sent.append(t), (True, "desktop"))[1])
    monkeypatch.setattr(runner, "_run_once",
                        lambda *a, **k: (_ for _ in ()).throw(
                            runner.RunError("nope", {"id": "r", "workflow_id": "demo"})))
    monkeypatch.setattr(runner.time, "sleep", lambda *_: None)

    with pytest.raises(runner.RunError):
        runner.run(tmp_home, config, "demo")

    assert len(sent) == 1


# --- cancelling a run in flight ------------------------------------------

def test_an_in_flight_run_is_listed_and_a_dead_marker_is_dropped(tmp_home):
    runs_mod.mark_running(tmp_home, "run_live", "demo")
    runs_mod.mark_running(tmp_home, "run_dead", "demo", pid=999999)

    listed = {r["id"] for r in runs_mod.list_running(tmp_home)}

    assert "run_live" in listed
    assert "run_dead" not in listed
    assert not (runs_mod.running_dir(tmp_home) / "run_dead.json").exists()


def test_cancelling_something_that_is_not_running_says_so(tmp_home):
    assert runs_mod.cancel(tmp_home, "nope")["cancelled"] is False


def test_cancelling_signals_the_recorded_pid(tmp_home, monkeypatch):
    runs_mod.mark_running(tmp_home, "run_live", "demo")
    signalled = []
    monkeypatch.setattr(runs_mod.os, "kill",
                        lambda pid, sig: signalled.append((pid, sig)) if sig else None)

    result = runs_mod.cancel(tmp_home, "run_live")

    assert result["cancelled"] is True
    assert signalled and signalled[-1][1] == runs_mod.signal.SIGTERM


def test_force_sends_sigkill(tmp_home, monkeypatch):
    runs_mod.mark_running(tmp_home, "run_live", "demo")
    signalled = []
    monkeypatch.setattr(runs_mod.os, "kill",
                        lambda pid, sig: signalled.append((pid, sig)) if sig else None)

    runs_mod.cancel(tmp_home, "run_live", force=True)

    assert signalled[-1][1] == runs_mod.signal.SIGKILL


def test_retention_is_reachable_without_the_daemon(tmp_home, config):
    # apply_retention had exactly one caller -- the daemon's nightly pass -- so a
    # store that never installs the daemon kept every log forever.
    assert runs_mod.apply_retention(config) == {"logs": 0, "records": 0}
