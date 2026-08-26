"""A tool outside a workflow's allowlist must not run.

The allowlist is the whole of what a user agreed to when the workflow was
built, and it was decorative: the refusal branch wrote an error into `result`
and then fell through into the branch that executes the call, so `result` was
overwritten by the real answer and the tool ran anyway. Any tool id the model
emitted was executed, whether or not the workflow was allowed to use it.

These pin the two halves that matter -- the call does not happen, and the
refusal is on the record where an analysis can find it -- plus the neighbouring
paths that must keep working, because a fix that refuses everything would pass
the first assertion.
"""

import pytest

from px0 import harness, runner
from px0 import runs as runs_mod
from px0 import tools


@pytest.fixture
def loop(tmp_home, monkeypatch):
    """The tool-call loop with a recording tool layer and a scripted harness.

    Returns a callable taking the turns the model will emit and the allowlist,
    and handing back what the loop produced plus every tool actually executed.
    """
    executed: list[tuple[str, dict]] = []

    monkeypatch.setattr(tools, "exists", lambda t, home=None: True)
    monkeypatch.setattr(tools, "is_write", lambda t, home=None: t.endswith("post_message"))
    monkeypatch.setattr(runs_mod, "append_raw_log", lambda *a: None)

    def fake_call(home, config, tool_id, args):
        executed.append((tool_id, args))
        return {"ok": True}

    monkeypatch.setattr(tools, "call", fake_call)

    def _run(turns: list[str], allowed: list[str], dry_run: bool = False):
        script = list(turns)
        monkeypatch.setattr(
            harness, "invoke_detailed",
            lambda *a, **kw: harness.Reply(text=script.pop(0) if script else "Done"))
        output, calls, usage = runner._tool_call_loop(
            tmp_home, {}, "prompt", allowed, dry_run, 60.0, "run_20260101-000000-aaaa")
        return {"output": output, "calls": calls, "usage": usage, "executed": executed}

    return _run


def test_a_tool_outside_the_allowlist_is_never_executed(loop):
    result = loop(['TOOL_CALL: {"tool": "shell.run", "args": {"cmd": "rm -rf /"}}',
                   "Done"],
                  allowed=["github.list_prs"])
    assert result["executed"] == []


def test_the_refusal_is_recorded_on_the_call(loop):
    result = loop(['TOOL_CALL: {"tool": "shell.run", "args": {}}', "Done"],
                  allowed=["github.list_prs"])
    assert len(result["calls"]) == 1
    call = result["calls"][0]
    assert call["refused"] is True
    assert call["failed"] is True
    assert "allowlist" in call["result_summary"]


def test_the_model_is_told_it_was_refused_and_carries_on(loop):
    """The refusal has to reach the conversation, or the model asks again."""
    result = loop(['TOOL_CALL: {"tool": "shell.run", "args": {}}',
                   'TOOL_CALL: {"tool": "github.list_prs", "args": {}}',
                   "Done"],
                  allowed=["github.list_prs"])
    assert [t for t, _ in result["executed"]] == ["github.list_prs"]
    assert result["output"] == "Done"


def test_an_allowed_tool_still_runs(loop):
    result = loop(['TOOL_CALL: {"tool": "github.list_prs", "args": {"n": 5}}', "Done"],
                  allowed=["github.list_prs"])
    assert result["executed"] == [("github.list_prs", {"n": 5})]
    assert result["calls"][0]["refused"] is False


def test_a_dry_run_still_stubs_a_write_tool_rather_than_refusing_it(loop):
    """Stubbing and refusing are different outcomes and must stay so: a
    rehearsal of an allowed write is not an allowlist violation."""
    result = loop(['TOOL_CALL: {"tool": "slack.post_message", "args": {}}', "Done"],
                  allowed=["slack.post_message"], dry_run=True)
    assert result["executed"] == []
    assert result["calls"][0]["stubbed"] is True
    assert result["calls"][0]["refused"] is False
    assert result["calls"][0]["failed"] is False


def test_a_workflow_with_no_allowlist_calls_nothing(loop):
    """With no tools declared the loop never enters the protocol at all, so a
    `TOOL_CALL:` line is just text the model happened to write."""
    result = loop(['TOOL_CALL: {"tool": "shell.run", "args": {}}'], allowed=[])
    assert result["executed"] == []
    assert result["calls"] == []


def test_running_out_of_turns_is_recorded(loop):
    """A model that keeps asking for tools until the cap has not finished; the
    run has to say so, because 'always uses every turn' is what tells a user
    the workflow is asking for more steps than a run allows."""
    turns = ['TOOL_CALL: {"tool": "github.list_prs", "args": {}}'] * runner.MAX_TOOL_TURNS
    result = loop(turns, allowed=["github.list_prs"])
    assert result["usage"]["hit_turn_cap"] is True
    assert result["usage"]["turns"] == runner.MAX_TOOL_TURNS


def test_a_run_that_finishes_early_does_not_claim_the_cap(loop):
    result = loop(["Done"], allowed=["github.list_prs"])
    assert result["usage"]["hit_turn_cap"] is False
