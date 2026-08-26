"""What a run writes down about itself.

A run used to leave two things behind: a summary record, and a raw log of
prompts and replies that ages out in a fortnight. Neither could answer "which
turn called which tool, and what did it cost" without a person reading prose.

These cover the three additions -- the structured event stream, the harness's
own token counts where it reports them, and the graceful paths when it does not
-- with particular attention to the failure modes, because every one of them
sits inside the run loop and none may be allowed to cost a user a run.
"""

import json

import pytest

from px0 import config as config_mod, harness, paths, runner
from px0 import runs as runs_mod
from px0 import tools


@pytest.fixture
def config(tmp_home, tmp_path):
    """A config whose logs go somewhere this test owns.

    Pinned explicitly rather than left at the default: `logs.path` falls back
    to `~/.local/state/px0/logs` when `/var/log/px0` is not writable, and a
    test that writes there is writing into the machine's real store.
    """
    cfg = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(cfg, "logs.path", str(tmp_path / "logs"))
    return cfg


RUN_ID = "run_20260101-120000-abcd"


# --- the event stream -----------------------------------------------------

def test_an_event_is_written_and_read_back(config):
    runs_mod.append_event(config, RUN_ID, "tool_call", tool="github.list_prs", failed=False)
    events = runs_mod.read_events(config, RUN_ID)
    assert len(events) == 1
    assert events[0]["kind"] == "tool_call"
    assert events[0]["tool"] == "github.list_prs"
    assert events[0]["run"] == RUN_ID
    assert events[0]["ts"]


def test_events_keep_the_order_they_happened_in(config):
    for i in range(5):
        runs_mod.append_event(config, RUN_ID, "model_call", turn=i)
    assert [e["turn"] for e in runs_mod.read_events(config, RUN_ID)] == [0, 1, 2, 3, 4]


def test_a_run_with_no_stream_reads_as_empty_rather_than_raising(config):
    assert runs_mod.read_events(config, "run_20990101-000000-ffff") == []


def test_a_truncated_stream_is_read_up_to_the_truncation(config):
    """A crash mid-write leaves half a line. The events before it are still
    worth having, so the reader skips the ruin rather than refusing the file."""
    runs_mod.append_event(config, RUN_ID, "run_started")
    path = runs_mod.events_path(config, RUN_ID)
    with open(path, "a") as f:
        f.write('{"kind": "tool_call", "tool": "githu')
    assert [e["kind"] for e in runs_mod.read_events(config, RUN_ID)] == ["run_started"]


def test_a_value_that_will_not_serialize_does_not_raise(config):
    """Telemetry that can fail a run is worse than no telemetry."""
    runs_mod.append_event(config, RUN_ID, "odd", value=object())
    # It either serialized via str() or was dropped; what matters is the
    # absence of an exception, and that the file is still readable.
    runs_mod.read_events(config, RUN_ID)


def test_a_bad_run_id_does_not_raise(config):
    runs_mod.append_event(config, "not-a-run-id", "run_started")


def test_events_can_be_turned_off(config):
    config_mod.set_key(config, "logs.events", "false")
    runs_mod.append_event(config, RUN_ID, "run_started")
    assert runs_mod.read_events(config, RUN_ID) == []


def test_retention_takes_the_event_stream_with_the_log(config):
    """The stream is the log's machine-readable half and must not outlive it,
    or a pruned run leaves half a record behind."""
    from datetime import datetime, timedelta, timezone
    old = datetime.now(timezone.utc) - timedelta(days=90)
    run_id = f"run_{old.strftime('%Y%m%d-%H%M%S')}-dead"
    runs_mod.write_record(config, {
        "id": run_id, "workflow_id": "demo", "outcome": "success",
        "start_time": old.isoformat(), "tool_calls": [],
    })
    runs_mod.append_raw_log(config, run_id, "some prompt")
    runs_mod.append_event(config, run_id, "run_started")
    assert runs_mod.events_path(config, run_id).exists()

    removed = runs_mod.apply_retention(config)
    assert removed["events"] == 1
    assert not runs_mod.events_path(config, run_id).exists()


def test_a_run_that_wrote_something_keeps_its_stream(config):
    """Runs that called a write tool are exempt from retention. The exemption
    has to cover the stream too, or the surviving record points at nothing."""
    from datetime import datetime, timedelta, timezone
    old = datetime.now(timezone.utc) - timedelta(days=90)
    run_id = f"run_{old.strftime('%Y%m%d-%H%M%S')}-keep"
    runs_mod.write_record(config, {
        "id": run_id, "workflow_id": "demo", "outcome": "success",
        "start_time": old.isoformat(),
        "tool_calls": [{"tool": "slack.post_message", "is_write": True}],
    })
    runs_mod.append_event(config, run_id, "run_started")
    runs_mod.apply_retention(config)
    assert runs_mod.events_path(config, run_id).exists()


# --- what the loop emits --------------------------------------------------

@pytest.fixture
def loop(tmp_home, config, monkeypatch):
    monkeypatch.setattr(tools, "exists", lambda t, home=None: True)
    monkeypatch.setattr(tools, "is_write", lambda t, home=None: False)
    monkeypatch.setattr(tools, "call", lambda *a: {"ok": True})
    monkeypatch.setattr(runs_mod, "append_raw_log", lambda *a: None)

    def _run(replies: list, allowed=("github.list_prs",)):
        script = list(replies)
        monkeypatch.setattr(
            harness, "invoke_detailed",
            lambda *a, **kw: script.pop(0) if script else harness.Reply(text="Done"))
        out = runner._tool_call_loop(
            tmp_home, config, "prompt", list(allowed), False, 60.0, RUN_ID)
        return out, runs_mod.read_events(config, RUN_ID)

    return _run


def test_every_turn_and_call_lands_in_the_stream(loop):
    (_out, _calls, _usage), events = loop([
        harness.Reply(text='TOOL_CALL: {"tool": "github.list_prs", "args": {"n": 1}}'),
        harness.Reply(text="Done"),
    ])
    kinds = [e["kind"] for e in events]
    assert kinds.count("model_call") == 2
    assert "prompt" in kinds
    assert "tool_call" in kinds
    call = next(e for e in events if e["kind"] == "tool_call")
    assert call["tool"] == "github.list_prs"
    assert call["arg_keys"] == ["n"]
    assert call["failed"] is False


def test_a_refused_call_gets_its_own_event(loop):
    _out, events = loop([
        harness.Reply(text='TOOL_CALL: {"tool": "shell.run", "args": {}}'),
        harness.Reply(text="Done"),
    ])
    refusal = next(e for e in events if e["kind"] == "tool_refused")
    assert refusal["tool"] == "shell.run"
    assert refusal["allowed"] == ["github.list_prs"]


def test_arguments_are_never_written_to_the_stream(loop):
    """Only the argument *names*. A tool's arguments routinely carry the
    content of the work -- a message body, a file path, a search string -- and
    the event stream is meant to be the part of a run that is safe to keep."""
    _out, events = loop([
        harness.Reply(text='TOOL_CALL: {"tool": "github.list_prs", '
                           '"args": {"token": "secret-value"}}'),
        harness.Reply(text="Done"),
    ])
    assert "secret-value" not in json.dumps(events)


# --- what a run cost ------------------------------------------------------

def test_reported_token_counts_replace_the_estimate(loop):
    (_out, _calls, usage), _events = loop([
        harness.Reply(text="Done", usage={"input_tokens": 1200, "output_tokens": 300,
                                          "reported": True},
                      meta={"total_cost_usd": 0.02}, output_format="json"),
    ])
    assert usage["reported"] is True
    assert usage["estimated"] is False
    assert usage["input_tokens"] == 1200
    assert usage["cost_usd"] == 0.02


def test_counts_are_summed_across_turns(loop):
    reply = lambda: harness.Reply(
        text='TOOL_CALL: {"tool": "github.list_prs", "args": {}}',
        usage={"input_tokens": 100, "output_tokens": 10}, meta={"total_cost_usd": 0.01})
    (_out, _calls, usage), _events = loop([
        reply(),
        harness.Reply(text="Done", usage={"input_tokens": 200, "output_tokens": 20},
                      meta={"total_cost_usd": 0.01}),
    ])
    assert usage["input_tokens"] == 300
    assert usage["output_tokens"] == 30
    assert usage["cost_usd"] == 0.02


def test_a_harness_that_reports_nothing_still_estimates(loop):
    (_out, _calls, usage), _events = loop([harness.Reply(text="Done")])
    assert usage["estimated"] is True
    assert usage["reported"] is False
    assert usage["estimated_tokens"] > 0


def test_an_unfamiliar_usage_field_is_kept_rather_than_dropped(loop):
    """The shape of the envelope belongs to the harness, not to px0."""
    (_out, _calls, usage), _events = loop([
        harness.Reply(text="Done", usage={"reasoning_tokens": 42}),
    ])
    assert usage["reasoning_tokens"] == 42


# --- the harness's own modes ----------------------------------------------

def test_a_known_harness_advertises_its_flags():
    caps = harness.capabilities("claude -p")
    assert caps["name"] == "claude"
    assert caps["structured"]


def test_a_harness_pinned_by_path_is_still_recognized():
    assert harness.harness_name("/opt/homebrew/bin/claude -p") == "claude"


def test_px0_invents_no_flags_for_a_command_it_does_not_know():
    """The cost of guessing wrong is every run for that backend failing."""
    caps = harness.capabilities("my-own-agent --run")
    assert caps["name"] is None
    assert caps["structured"] == [] and caps["verbose"] == []


@pytest.mark.parametrize("raw, expected", [
    ('{"result": "the answer", "usage": {"input_tokens": 5}}', "the answer"),
    ('[{"type": "start"}, {"result": "the answer"}]', "the answer"),
    ('{"type":"start"}\n{"result":"the answer"}\n', "the answer"),
    ('{"response": "the answer"}', "the answer"),
])
def test_the_envelope_is_read_in_every_shape_it_arrives_in(raw, expected):
    text, _usage, _meta = harness._parse_structured(raw)
    assert text == expected


@pytest.mark.parametrize("raw", ["", "not json at all", "{}", "[]", '{"nothing": 1}'])
def test_an_unreadable_envelope_reports_no_text_rather_than_raising(raw):
    text, _usage, _meta = harness._parse_structured(raw)
    assert text is None


def test_usage_from_the_envelope_is_marked_as_reported():
    _text, usage, _meta = harness._parse_structured(
        '{"result": "x", "usage": {"input_tokens": 7}}')
    assert usage["reported"] is True and usage["input_tokens"] == 7


@pytest.mark.parametrize("stderr, expected", [
    ("error: unknown option '--output-format'", True),
    ("Unknown argument: output-format", True),
    ("Error: not authenticated", False),
    ("", False),
])
def test_only_a_flag_complaint_is_treated_as_one(stderr, expected):
    """A false positive here would retry a genuinely failed model call as if
    the flag were at fault, and report the second failure instead of the
    first."""
    assert harness._looks_like_unknown_flag(stderr) is expected


@pytest.mark.allow_harness
def test_a_harness_older_than_the_flags_is_retried_without_them(monkeypatch, config):
    """px0 adds reporting flags on its own initiative, so it carries the cost
    of being wrong about them -- not the user's run."""
    calls: list[list[str]] = []

    class Done:
        def __init__(self, code, out, err):
            self.returncode, self.stdout, self.stderr = code, out, err

    def fake_run(cmd, input_text, timeout, harness_cmd):
        calls.append(list(cmd))
        if "--output-format" in cmd:
            return Done(1, "", "error: unknown option '--output-format'")
        return Done(0, "the answer", "")

    monkeypatch.setattr(harness, "_run", fake_run)
    harness._UNSUPPORTED.clear()
    reply = harness.invoke_detailed(config, "hello")

    assert reply.text == "the answer"
    assert len(calls) == 2
    assert "--output-format" not in calls[1]
    assert "downgraded" in reply.meta
    harness._UNSUPPORTED.clear()


@pytest.mark.allow_harness
def test_a_real_failure_is_not_retried_as_a_flag_problem(monkeypatch, config):
    class Done:
        def __init__(self, code, out, err):
            self.returncode, self.stdout, self.stderr = code, out, err

    monkeypatch.setattr(harness, "_run",
                        lambda *a, **k: Done(1, "", "Error: not authenticated"))
    harness._UNSUPPORTED.clear()
    with pytest.raises(harness.HarnessError, match="not authenticated"):
        harness.invoke_detailed(config, "hello")


@pytest.mark.allow_harness
def test_an_unparseable_envelope_falls_back_to_the_raw_text(monkeypatch, config):
    class Done:
        def __init__(self, code, out, err):
            self.returncode, self.stdout, self.stderr = code, out, err

    monkeypatch.setattr(harness, "_run",
                        lambda *a, **k: Done(0, "just some prose", ""))
    harness._UNSUPPORTED.clear()
    reply = harness.invoke_detailed(config, "hello")
    assert reply.text == "just some prose"
    assert reply.output_format == "text"


@pytest.mark.allow_harness
def test_text_mode_adds_no_flags(monkeypatch, config):
    config_mod.set_key(config, "model.output_format", "text")
    seen: list[list[str]] = []

    class Done:
        returncode, stdout, stderr = 0, "answer", ""

    def fake_run(cmd, input_text, timeout, harness_cmd):
        seen.append(list(cmd))
        return Done()

    monkeypatch.setattr(harness, "_run", fake_run)
    harness._UNSUPPORTED.clear()
    harness.invoke_detailed(config, "hello")
    assert "--output-format" not in seen[0]


@pytest.mark.allow_harness
def test_verbose_asks_the_harness_to_narrate(monkeypatch, config):
    config_mod.set_key(config, "model.verbose", "true")
    seen: list[list[str]] = []

    class Done:
        returncode, stdout, stderr = 0, '{"result": "answer"}', ""

    def fake_run(cmd, input_text, timeout, harness_cmd):
        seen.append(list(cmd))
        return Done()

    monkeypatch.setattr(harness, "_run", fake_run)
    harness._UNSUPPORTED.clear()
    harness.invoke_detailed(config, "hello")
    assert "--verbose" in seen[0]


@pytest.mark.allow_harness
def test_invoke_still_answers_with_plain_text(monkeypatch, config):
    """Every other caller in px0 wants the reply and nothing else."""
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **k: harness.Reply(text="hello"))
    assert harness.invoke(config, "x") == "hello"


# --- inputs that resolve to nothing ---------------------------------------

@pytest.mark.parametrize("value, empty", [
    ("", True), ("   ", True), (None, True), ([], True), ({}, True),
    ({"items": []}, True), ({"successful": True, "data": []}, True),
    ("something", False), (["a"], False), ({"items": ["a"]}, False),
])
def test_an_input_that_resolved_to_nothing_is_recognized(value, empty):
    """A tool answering `{"items": []}` has resolved successfully and returned
    nothing; the envelope is not the content."""
    assert runner._is_empty(value) is empty


# --- pipelines ------------------------------------------------------------

def test_a_pipeline_releases_its_in_flight_marker(tmp_home, config, monkeypatch):
    """A pipeline never cleared its marker. From a one-shot CLI that hid --
    `list_running` drops markers whose process is gone -- but inside the
    daemon, whose process outlives the run, the run showed as in flight
    forever.
    """
    from px0 import paths as paths_mod, workflow as wf_mod

    for stage in ("one", "two"):
        (paths_mod.workflows_dir(tmp_home) / f"{stage}.md").write_text(
            f"---\nid: {stage}\ndescription: A stage\noutput:\n  target: stdout\n"
            "---\n\nBody.\n")
    (paths_mod.workflows_dir(tmp_home) / "chain.md").write_text(
        "---\nid: chain\ndescription: A pipeline\npipeline:\n  - one\n  - two\n"
        "output:\n  target: stdout\n---\n\nBody.\n")

    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="stage output"))
    record = runner.run(tmp_home, config, "chain", trigger="manual")

    assert record["outcome"] == "success"
    assert [r["id"] for r in runs_mod.list_running(tmp_home)] == []


def test_a_pipeline_records_each_stage_as_it_finishes(tmp_home, config, monkeypatch):
    from px0 import paths as paths_mod

    for stage in ("one", "two"):
        (paths_mod.workflows_dir(tmp_home) / f"{stage}.md").write_text(
            f"---\nid: {stage}\ndescription: A stage\noutput:\n  target: stdout\n"
            "---\n\nBody.\n")
    (paths_mod.workflows_dir(tmp_home) / "chain.md").write_text(
        "---\nid: chain\ndescription: A pipeline\npipeline:\n  - one\n  - two\n"
        "output:\n  target: stdout\n---\n\nBody.\n")

    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="stage output"))
    record = runner.run(tmp_home, config, "chain", trigger="manual")

    events = runs_mod.read_events(config, record["id"])
    kinds = [e["kind"] for e in events]
    assert kinds[0] == "run_started"
    assert "pipeline_started" in kinds
    assert kinds.count("stage_finished") == 2
    assert kinds[-1] == "run_finished"
    # Each event says which run it belongs to -- the parent, not the stage.
    assert {e["run"] for e in events} == {record["id"]}


# --- windows over recorded time -------------------------------------------

def test_a_since_window_is_compared_as_time_not_text(config):
    """Records stamp UTC; `parse_since` hands back naive local. Compared as
    strings, the offset suffix sorts an in-window record before the cutoff, and
    the naive value is a different clock -- so `--since` was displaced by this
    machine's offset. In IST that dropped five and a half hours of runs from
    every window, including the one the daily budget reads.
    """
    from datetime import datetime as dt, timedelta, timezone as tz

    recent = dt.now(tz.utc) - timedelta(hours=3)
    runs_mod.write_record(config, {
        "id": f"run_{recent.strftime('%Y%m%d-%H%M%S')}-aaaa",
        "workflow_id": "demo", "outcome": "success",
        "start_time": recent.isoformat(),
    })
    assert len(runs_mod.list_records(config, since=runs_mod.parse_since("6h"))) == 1
    assert runs_mod.list_records(config, since=runs_mod.parse_since("1h")) == []


@pytest.mark.parametrize("value, expected_hour", [
    ("2026-08-26T03:00:00+00:00", 3),
    ("2026-08-26T08:30:00+05:30", 3),
])
def test_a_stamp_is_normalized_to_utc(value, expected_hour):
    assert runs_mod.as_utc(value).hour == expected_hour


def test_a_naive_stamp_is_read_as_this_machines_clock():
    """Which is what `datetime.now()` means, and what `parse_since` returns."""
    from datetime import datetime as dt, timezone as tz

    naive = dt(2026, 8, 26, 12, 0)
    assert runs_mod.as_utc(naive) == naive.astimezone().astimezone(tz.utc)


@pytest.mark.parametrize("value", [None, "", "not a date", 12345])
def test_an_unreadable_stamp_is_not_guessed_at(value):
    assert runs_mod.as_utc(value) is None


def test_a_record_with_no_readable_time_is_left_out_of_a_window(config):
    """A window is a claim about time, and a record with none cannot satisfy
    it -- but with no window at all it is still listed."""
    runs_mod.write_record(config, {
        "id": "run_20260826-120000-bbbb", "workflow_id": "demo",
        "outcome": "success", "start_time": "whenever",
    })
    assert runs_mod.list_records(config, since=runs_mod.parse_since("7d")) == []
    assert len(runs_mod.list_records(config)) == 1
