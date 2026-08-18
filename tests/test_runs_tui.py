import pytest
import argparse
from unittest.mock import MagicMock
from px0 import runs_tui, cli, runner, tools, harness, runs as runs_mod

def test_format_row():
    # Normal run
    r1 = {"id": "run_1", "workflow_id": "test-wf", "trigger": "manual", "outcome": "success", "tool_calls": []}
    assert "run_1" in runs_tui.format_row(r1)
    assert "[write]" not in runs_tui.format_row(r1)

    # Write run
    r2 = {"id": "run_2", "workflow_id": "test-wf", "trigger": "manual", "outcome": "success", "tool_calls": [{"is_write": True}]}
    assert "[write]" in runs_tui.format_row(r2)

    # Failed run
    r3 = {"id": "run_3", "workflow_id": "test-wf", "trigger": "manual", "outcome": "failed", "tool_calls": []}
    assert "failed" in runs_tui.format_row(r3)


def test_apply_filters():
    records = [
        {"id": "r1", "workflow_id": "wf-a", "outcome": "success", "start": "2026-08-15T09:00:00", "tool_calls": []},
        {"id": "r2", "workflow_id": "wf-b", "outcome": "failed", "start": "2026-08-16T10:00:00", "tool_calls": [{"is_write": True}]},
        {"id": "r3", "workflow_id": "wf-a", "outcome": "failed", "start": "2026-08-17T11:00:00", "tool_calls": []},
    ]

    # No filter
    assert len(runs_tui.apply_filters(records, None, "all", False, None)) == 3

    # Workflow filter
    assert len(runs_tui.apply_filters(records, "wf-a", "all", False, None)) == 2

    # Outcome filter
    assert len(runs_tui.apply_filters(records, None, "failed", False, None)) == 2

    # Write only filter
    assert len(runs_tui.apply_filters(records, None, "all", True, None)) == 1

    # Since filter
    assert len(runs_tui.apply_filters(records, None, "all", False, "2026-08-16")) == 2

    # Combined filter
    res = runs_tui.apply_filters(records, "wf-b", "failed", True, "2026-08-16")
    assert len(res) == 1
    assert res[0]["id"] == "r2"


def test_extract_rendered_prompt():
    # Well-formed
    log = """
Some raw stdout.
--- turn 1 PROMPT ---
This is the rendered prompt text here.
--- turn 1 OUTPUT ---
Answer from assistant.
"""
    assert runs_tui.extract_rendered_prompt(log) == "This is the rendered prompt text here."

    # Empty log
    assert runs_tui.extract_rendered_prompt("") == ""

    # No matching marker
    assert runs_tui.extract_rendered_prompt("random raw logs\n") == ""


def test_tool_call_loop_elapsed_seconds(tmp_home, monkeypatch):
    # Mock tools.exists and tools.is_write
    monkeypatch.setattr(tools, "exists", lambda t: True)
    monkeypatch.setattr(tools, "is_write", lambda t: False)
    
    # Mock the tool call
    monkeypatch.setattr(tools, "call", lambda *a: "success_result")

    # Mock runs_mod.append_raw_log
    monkeypatch.setattr(runs_mod, "append_raw_log", lambda *a: None)

    # Mock harness.invoke
    turns = [
        'TOOL_CALL: {"tool": "slack.post_message", "args": {}}',
        'Final Answer'
    ]
    monkeypatch.setattr(harness, "invoke", lambda *a, **kw: turns.pop(0) if turns else "Final Answer")

    # Run tool call loop
    output, tool_calls = runner._tool_call_loop(
        tmp_home, {}, "Initial prompt", ["slack.post_message"], False, 60.0, "run_123"
    )

    assert len(tool_calls) == 1
    assert "elapsed_seconds" in tool_calls[0]
    assert tool_calls[0]["elapsed_seconds"] >= 0.0


def test_cmd_runs_bare_opens_tui(tmp_home, monkeypatch):
    # Mock cli._ctx
    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, {}))

    called = []
    monkeypatch.setattr(runs_tui, "run", lambda home, config: called.append((home, config)))

    args = argparse.Namespace(runs_cmd=None)
    cli.cmd_runs(args)

    assert len(called) == 1
    assert called[0][0] == tmp_home
