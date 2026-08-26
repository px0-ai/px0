"""The parts that close a loop: giving up, remembering, replaying, conversing.

Each of these existed as half a mechanism. px0 could see a workflow failing
identically forever and did nothing; it could hold memories nobody wrote; it
could propose a revision nobody could check; it could answer a question and
discard the correction. What follows is mostly about the *stopping* conditions,
because every one of these acts on its own initiative and the way each goes
wrong is by acting too eagerly.
"""

from datetime import datetime, timedelta, timezone

import pytest

from px0 import analysis, config as config_mod, harness, memory, paths, replay
from px0 import runner, session
from px0 import runs as runs_mod
from px0 import workflow as workflow_mod


@pytest.fixture
def config(tmp_home, tmp_path):
    cfg = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(cfg, "logs.path", str(tmp_path / "logs"))
    return cfg


def _write(tmp_home, wf_id="demo", extra="", body="Body."):
    (paths.workflows_dir(tmp_home) / f"{wf_id}.md").write_text(
        f"---\nid: {wf_id}\ndescription: A demo\n{extra}"
        f"output:\n  target: stdout\n---\n\n{body}\n")
    from px0 import versioning
    versioning.checkpoint_scan(tmp_home, actor="test")
    return workflow_mod.load(tmp_home, wf_id)


def _failure(config, error, minutes, wf_id="demo", outcome="failed"):
    when = datetime.now(timezone.utc) - timedelta(minutes=minutes)
    runs_mod.write_record(config, {
        "id": f"run_{when.strftime('%Y%m%d-%H%M%S')}-{minutes:04x}",
        "workflow_id": wf_id, "outcome": outcome, "error": error,
        "start_time": when.isoformat(), "dry_run": False, "tool_calls": [],
    })


# --- giving up ------------------------------------------------------------

def test_a_streak_of_one_cause_is_counted(tmp_home, config):
    for i in range(4):
        _failure(config, f"connector refused after {i}s", minutes=i)
    streak = analysis.consecutive_failures(config, "demo")
    assert streak["count"] == 4


def test_a_success_ends_the_streak(tmp_home, config):
    """Counted from the newest backwards, because the question is whether this
    is broken *now* -- a rate over a window cannot answer that."""
    _failure(config, "connector refused", minutes=1)
    _failure(config, "connector refused", minutes=2)
    _failure(config, "", minutes=3, outcome="success")
    _failure(config, "connector refused", minutes=4)
    assert analysis.consecutive_failures(config, "demo")["count"] == 2


def test_a_different_cause_ends_the_streak(tmp_home, config):
    """Failing three different ways is a workflow to look at, not one stuck in
    the way the breaker exists to stop."""
    _failure(config, "connector refused", minutes=1)
    _failure(config, "not authenticated", minutes=2)
    _failure(config, "connector refused", minutes=3)
    assert analysis.consecutive_failures(config, "demo")["count"] == 1


def test_a_rehearsal_neither_counts_nor_clears(tmp_home, config):
    when = datetime.now(timezone.utc)
    runs_mod.write_record(config, {
        "id": f"run_{when.strftime('%Y%m%d-%H%M%S')}-dead", "workflow_id": "demo",
        "outcome": "success", "start_time": when.isoformat(), "dry_run": True,
    })
    for i in range(3):
        _failure(config, "connector refused", minutes=i + 1)
    assert analysis.consecutive_failures(config, "demo")["count"] == 3


def test_the_breaker_trips_at_the_limit(tmp_home, config):
    config_mod.set_key(config, "runs.disable_after_failures", "3")
    for i in range(3):
        _failure(config, "connector refused", minutes=i)
    assert analysis.should_trip_breaker(config, "demo")


def test_the_breaker_does_not_trip_early(tmp_home, config):
    config_mod.set_key(config, "runs.disable_after_failures", "5")
    for i in range(4):
        _failure(config, "connector refused", minutes=i)
    assert analysis.should_trip_breaker(config, "demo") is None


def test_the_breaker_can_be_turned_off(tmp_home, config):
    config_mod.set_key(config, "runs.disable_after_failures", "0")
    for i in range(20):
        _failure(config, "connector refused", minutes=i)
    assert analysis.should_trip_breaker(config, "demo") is None


def test_a_scheduled_run_parks_the_workflow(tmp_home, config, monkeypatch):
    config_mod.set_key(config, "runs.disable_after_failures", "2")
    _write(tmp_home, extra="trigger:\n  schedule: '0 9 * * *'\n",)
    (paths.workflows_dir(tmp_home) / "demo.md").write_text(
        "---\nid: demo\ndescription: A demo\ntrigger:\n  schedule: '0 9 * * *'\n"
        "output:\n  target: file\n  path: out-{date}.md\n---\n\nBody.\n")
    # The same message the run itself will record, so these are one streak
    # rather than two causes.
    for i in range(2):
        _failure(config, "nope", minutes=i + 1)

    def boom(*a, **kw):
        raise harness.HarnessError("nope")

    monkeypatch.setattr(harness, "invoke_detailed", boom)
    with pytest.raises(runner.RunError):
        runner.run(tmp_home, config, "demo", trigger="schedule")
    assert workflow_mod.load(tmp_home, "demo").enabled is False


def test_a_manual_run_never_parks_it(tmp_home, config, monkeypatch):
    """A person is at the terminal reading the error. Parking their workflow
    underneath them takes a decision they are in the middle of making."""
    config_mod.set_key(config, "runs.disable_after_failures", "1")
    _write(tmp_home)
    _failure(config, "nope", minutes=1)

    def boom(*a, **kw):
        raise harness.HarnessError("nope")

    monkeypatch.setattr(harness, "invoke_detailed", boom)
    with pytest.raises(runner.RunError):
        runner.run(tmp_home, config, "demo", trigger="manual")
    assert workflow_mod.load(tmp_home, "demo").enabled is True


def test_parking_is_a_revertible_change(tmp_home, config):
    from px0 import versioning

    _write(tmp_home)
    change_id = analysis.set_enabled(tmp_home, "demo", False, "kept failing")
    assert change_id
    assert workflow_mod.load(tmp_home, "demo").enabled is False
    versioning.revert_change(tmp_home, change_id, actor="test")
    assert workflow_mod.load(tmp_home, "demo").enabled is True


def test_parking_a_workflow_already_parked_changes_nothing(tmp_home, config):
    _write(tmp_home, extra="enabled: false\n")
    assert analysis.set_enabled(tmp_home, "demo", False, "again") is None


def test_a_parked_workflow_is_a_health_problem(tmp_home, config):
    _write(tmp_home, extra="enabled: false\n")
    for i in range(3):
        _failure(config, "connector refused", minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    assert any(f["code"] == "parked" and f["severity"] == "problem"
               for f in report["findings"])


# --- remembering ----------------------------------------------------------

def test_corrections_are_gathered_from_marked_runs(tmp_home, config):
    when = datetime.now(timezone.utc)
    runs_mod.write_record(config, {
        "id": f"run_{when.strftime('%Y%m%d-%H%M%S')}-aaaa", "workflow_id": "demo",
        "outcome": "success", "start_time": when.isoformat(),
        "review": {"verdict": "bad", "note": "the week runs Monday to Friday"},
    })
    found = memory._correction_sources(config)
    assert found and "Monday to Friday" in found[0]["text"]


def test_a_run_marked_good_is_not_a_correction(tmp_home, config):
    when = datetime.now(timezone.utc)
    runs_mod.write_record(config, {
        "id": f"run_{when.strftime('%Y%m%d-%H%M%S')}-bbbb", "workflow_id": "demo",
        "outcome": "success", "start_time": when.isoformat(),
        "review": {"verdict": "good", "note": "spot on"},
    })
    assert memory._correction_sources(config) == []


def test_nothing_to_learn_from_means_no_model_call(tmp_home, config):
    """The autouse guard fails any unmocked harness call, so reaching one here
    would fail this test rather than pass it quietly."""
    assert memory.suggest(tmp_home, config) == []


def test_suggestions_are_read_from_the_model(tmp_home, config, monkeypatch):
    import json

    monkeypatch.setattr(harness, "invoke", lambda *a, **kw: json.dumps([
        {"text": "the working week runs Monday to Friday", "subject": "week",
         "kind": "fact", "why": "marked bad: wrong week"},
    ]))
    found = memory.suggest(tmp_home, config, extra=["no, the week is Mon-Fri"])
    assert found[0]["subject"] == "week"


def test_something_already_remembered_is_not_suggested_again(tmp_home, config, monkeypatch):
    import json

    memory.remember(tmp_home, "the week runs Monday to Friday", subject="week")
    monkeypatch.setattr(harness, "invoke", lambda *a, **kw: json.dumps([
        {"text": "the working week runs Monday to Friday", "subject": "week"},
    ]))
    assert memory.suggest(tmp_home, config, extra=["anything"]) == []


def test_a_bad_suggestion_reply_suggests_nothing(tmp_home, config, monkeypatch):
    """An empty list is a fine answer to "is there anything worth keeping".
    Failing the command over a malformed one would be worse than making none."""
    monkeypatch.setattr(harness, "invoke", lambda *a, **kw: "not json at all")
    assert memory.suggest(tmp_home, config, extra=["anything"]) == []


def test_suggesting_never_writes_on_its_own(tmp_home, config, monkeypatch):
    import json

    monkeypatch.setattr(harness, "invoke", lambda *a, **kw: json.dumps([
        {"text": "something", "subject": "thing"}]))
    memory.suggest(tmp_home, config, extra=["anything"])
    assert memory.load_all(tmp_home) == {}


# --- conversing -----------------------------------------------------------

@pytest.mark.parametrize("text", [
    "no, I meant last week", "actually it should be Friday",
    "that's wrong", "not the API repo", "I said Monday",
])
def test_a_correction_is_recognized(text):
    assert session.looks_like_correction(text) is True


@pytest.mark.parametrize("text", [
    "what did I review this week?", "summarize the release notes",
    "who is on the payments team",
])
def test_an_ordinary_question_is_not(text):
    assert session.looks_like_correction(text) is False


def test_the_first_turn_is_never_a_correction(tmp_home):
    """There is nothing yet to correct, and an opening question that happens to
    contain "not" is a question."""
    s = session.start(tmp_home)
    session.add_turn(tmp_home, s, "not sure what I reviewed", "an answer")
    assert s["turns"][0]["correction"] is False


def test_a_later_correction_is_marked_and_kept(tmp_home):
    s = session.start(tmp_home)
    session.add_turn(tmp_home, s, "what did I review?", "last week's PRs")
    session.add_turn(tmp_home, s, "no, I meant this week", "this week's PRs")
    assert s["turns"][1]["correction"] is True
    corrections = session.corrections(s)
    assert "this week" in corrections[0] and "what did I review?" in corrections[0]


def test_a_follow_up_carries_what_it_follows(tmp_home):
    """"and last week?" names no subject; alone it is unroutable."""
    s = session.start(tmp_home)
    session.add_turn(tmp_home, s, "which PRs did I review?", "these ones")
    resolved = session.resolve_question(s, "and last week?")
    assert "which PRs did I review?" in resolved


def test_the_first_question_is_left_alone(tmp_home):
    s = session.start(tmp_home)
    assert session.resolve_question(s, "what is on today?") == "what is on today?"


def test_continuing_finds_the_last_conversation(tmp_home):
    session.start(tmp_home)
    second = session.start(tmp_home)
    session.add_turn(tmp_home, second, "a question", "an answer")
    assert session.latest(tmp_home)["id"] == second["id"]


def test_sessions_are_pruned(tmp_home, config):
    """Scaffolding: what was worth keeping is in memory/ by then."""
    s = session.start(tmp_home)
    s["touched"] = (datetime.now(timezone.utc) - timedelta(days=30)).isoformat()
    session.save(tmp_home, s)
    s["touched"] = (datetime.now(timezone.utc) - timedelta(days=30)).isoformat()
    (session.sessions_dir(tmp_home) / f"{s['id']}.json").write_text(
        __import__("json").dumps(s))
    assert session.prune(tmp_home, config) == 1


# --- replaying ------------------------------------------------------------

def test_capture_is_off_unless_asked_for(tmp_home, config):
    wf = _write(tmp_home)
    assert replay.capture_enabled(config, wf) is False


def test_a_workflow_can_ask_for_capture(tmp_home, config):
    wf = _write(tmp_home, extra="capture: true\n")
    assert replay.capture_enabled(config, wf) is True


def test_a_workflow_can_opt_out_of_a_store_wide_capture(tmp_home, config):
    config_mod.set_key(config, "runs.capture_inputs", "true")
    wf = _write(tmp_home, extra="capture: false\n")
    assert replay.capture_enabled(config, wf) is False


def test_a_run_keeps_what_it_read(tmp_home, config, monkeypatch):
    _write(tmp_home, extra="capture: true\n")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="an answer"))
    record = runner.run(tmp_home, config, "demo", trigger="manual")
    assert record["captured_inputs"] is True
    fixture = replay.read(tmp_home, "demo", record["id"])
    assert fixture["prompt"]


def test_a_fixture_holds_no_config(tmp_home, config, monkeypatch):
    """The config carries the Composio key. A fixture is already the most
    sensitive thing px0 writes; it does not also need to be a credential."""
    _write(tmp_home, extra="capture: true\n")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="an answer"))
    record = runner.run(tmp_home, config, "demo", trigger="manual")
    fixture = replay.read(tmp_home, "demo", record["id"])
    assert "config" not in fixture["inputs"]


def test_replaying_without_a_fixture_says_so(tmp_home, config):
    _write(tmp_home)
    with pytest.raises(replay.ReplayError, match="capture"):
        replay.read(tmp_home, "demo", "run_20260101-000000-aaaa")


def test_two_bodies_render_against_the_same_inputs(tmp_home, config):
    wf = _write(tmp_home, body="Summarize {{items}}.")
    fixture = {"inputs": {"items": "three pull requests"}, "stdin": ""}
    before = replay.render_with(tmp_home, config, wf, fixture)
    after = replay.render_with(tmp_home, config, wf, fixture,
                               body="List {{items}} as bullets.")
    assert "three pull requests" in before and "three pull requests" in after
    assert "Summarize" in before and "bullets" in after


def test_rendering_an_alternative_does_not_touch_the_workflow(tmp_home, config):
    wf = _write(tmp_home, body="Original.")
    replay.render_with(tmp_home, config, wf, {"inputs": {}}, body="Replacement.")
    assert workflow_mod.load(tmp_home, "demo").body.strip() == "Original."


def test_identical_outputs_are_reported_as_such(tmp_home):
    summary = replay.summarize("same\ntext", "same\ntext")
    assert summary["identical"] is True and summary["added"] == 0


def test_a_diff_names_what_moved():
    changes = replay.diff("one\ntwo\nthree", "one\ntwo point five\nthree")
    assert any(m == "+" and "point five" in t for m, t in changes)
    assert any(m == "-" for m, _ in changes)


def test_fixtures_age_out(tmp_home, config):
    """The only place the content of a run's inputs is written down."""
    replay.capture(tmp_home, "demo", "run_20260101-000000-aaaa", {"x": "y"})
    path = replay.fixtures_dir(tmp_home) / "demo" / "run_20260101-000000-aaaa.json"
    import json as json_mod
    payload = json_mod.loads(path.read_text())
    payload["captured"] = (datetime.now(timezone.utc) - timedelta(days=60)).isoformat()
    path.write_text(json_mod.dumps(payload))
    assert replay.apply_retention(tmp_home, config) == 1
    assert not path.exists()


def test_forgetting_a_workflows_fixtures(tmp_home):
    for i in range(3):
        replay.capture(tmp_home, "demo", f"run_2026010{i+1}-000000-aaaa", {"x": i})
    assert replay.forget(tmp_home, "demo") == 3
    assert replay.listing(tmp_home, "demo") == []
