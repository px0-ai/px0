import os
import pytest
import argparse
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
    # both take an optional `home` now, so discovered tools resolve too
    monkeypatch.setattr(tools, "exists", lambda t, home=None: True)
    monkeypatch.setattr(tools, "is_write", lambda t, home=None: False)
    
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
    output, tool_calls, usage = runner._tool_call_loop(
        tmp_home, {}, "Initial prompt", ["slack.post_message"], False, 60.0, "run_123"
    )

    assert len(tool_calls) == 1
    assert "elapsed_seconds" in tool_calls[0]
    assert tool_calls[0]["elapsed_seconds"] >= 0.0
    assert usage["model_calls"] == 2
    assert usage["estimated"] is True


def test_cmd_runs_bare_opens_tui(tmp_home, monkeypatch):
    # Mock cli._ctx
    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, {}))

    called = []
    monkeypatch.setattr(runs_tui, "run", lambda home, config: called.append((home, config)))

    args = argparse.Namespace(runs_cmd=None)
    cli.cmd_runs(args)

    assert len(called) == 1
    assert called[0][0] == tmp_home


def test_cli_list_and_tui_render_identical_rows(tmp_home, monkeypatch, capsys):
    """Phase 6 DoD: the `runs list` text and the TUI's rows come from one formatter.

    The TUI additionally expands tabs and truncates to the terminal width; the
    underlying row text must be identical, so a change to one can never drift
    from the other.
    """
    from px0 import runs as runs_mod

    records = [
        {"id": "run_a", "workflow_id": "wf-a", "trigger": "manual",
         "outcome": "success", "tool_calls": []},
        {"id": "run_b", "workflow_id": "wf-b", "trigger": "schedule",
         "outcome": "failed", "tool_calls": [{"is_write": True}]},
    ]
    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, {}))
    monkeypatch.setattr(runs_mod, "list_records", lambda *a, **kw: records)

    args = argparse.Namespace(
        runs_cmd="list", workflow=None, failed=False, since=None, json=False
    )
    cli.cmd_runs(args)
    cli_rows = capsys.readouterr().out.splitlines()

    widths = runs_tui.column_widths(records)
    tui_rows = [runs_tui.format_row(r, widths) for r in records]
    assert cli_rows == tui_rows
    # padded to a table, not jittering per row
    assert len({len(r.split("success")[0].split("failed")[0]) for r in tui_rows}) == 1
    assert "[write]" in tui_rows[1] and "[write]" not in tui_rows[0]


def test_detail_view_rerun_follows_the_new_record(tmp_home, monkeypatch):
    """Phase 6 AC4: `r` reruns and the view refreshes onto the new run.

    runner.run returns the whole record; treating its return value as an id left
    run_id holding a dict, so the next redraw hit the "Failed to read record"
    branch instead of showing the rerun.
    """
    import curses
    from px0 import runs as runs_mod

    reads = []

    def fake_read_record(config, run_id):
        reads.append(run_id)
        if len(reads) > 2:  # first draw, then the post-rerun redraw
            raise KeyboardInterrupt
        return {"id": run_id, "workflow_id": "wf-a", "trigger": "manual",
                "outcome": "success", "tool_calls": [], "guidelines_inlined": []}

    monkeypatch.setattr(runs_mod, "read_record", fake_read_record)
    monkeypatch.setattr(runs_mod, "read_raw_log", lambda config, rid: "")
    monkeypatch.setattr(runner, "run", lambda *a, **kw: {"id": "run_new", "workflow_id": "wf-a"})
    monkeypatch.setattr(curses, "endwin", lambda: None)
    monkeypatch.setattr(curses, "initscr", lambda: stdscr)

    class FakeStdscr:
        def getmaxyx(self): return (40, 100)
        def clear(self): pass
        def refresh(self): pass
        def addstr(self, *a, **kw): pass
        def getch(self): return ord('r')

    stdscr = FakeStdscr()
    monkeypatch.setattr("sys.stdin.read", lambda n: "\n")

    with pytest.raises(KeyboardInterrupt):
        runs_tui._detail_view(stdscr, tmp_home, {}, {"id": "run_old"})

    assert reads[0] == "run_old"
    assert reads[1] == "run_new", f"view did not follow the rerun: {reads}"


def _detail_view_pressing_r(tmp_home, monkeypatch, record, reads_before_exit=1):
    """Drives `_detail_view` with `r` held down, over a record of our choosing.

    Returns the (args, kwargs) of every runner.run call the keypress made.
    """
    import curses
    from px0 import runs as runs_mod

    reads, calls = [], []

    def fake_read_record(config, run_id):
        reads.append(run_id)
        if len(reads) > reads_before_exit:
            raise KeyboardInterrupt
        return dict(record, id=run_id)

    monkeypatch.setattr(runs_mod, "read_record", fake_read_record)
    monkeypatch.setattr(runs_mod, "read_raw_log", lambda config, rid: "")
    monkeypatch.setattr(runner, "run",
                        lambda *a, **kw: (calls.append((a, kw)), {"id": "run_new"})[1])

    class FakeStdscr:
        def getmaxyx(self): return (40, 100)
        def clear(self): pass
        def refresh(self): pass
        def addstr(self, *a, **kw): pass
        def getch(self): return ord('r')

    stdscr = FakeStdscr()
    monkeypatch.setattr(curses, "endwin", lambda: None)
    monkeypatch.setattr(curses, "initscr", lambda: stdscr)
    monkeypatch.setattr("sys.stdin.read", lambda n: "\n")

    with pytest.raises(KeyboardInterrupt):
        runs_tui._detail_view(stdscr, tmp_home, {}, {"id": record["id"]})
    return calls


def test_detail_view_refuses_to_rerun_an_ask(tmp_home, monkeypatch):
    """An ask run names no workflow, so `r` handed the runner None -- which wrote
    a phantom "no such workflow: None" record into the history before failing.
    `px0 runs rerun` already refused this; the browser did not."""
    ask = {"id": "ask_1", "workflow_id": None, "trigger": "ask",
           "outcome": "success", "tool_calls": [], "guidelines_inlined": []}

    calls = _detail_view_pressing_r(tmp_home, monkeypatch, ask)

    assert calls == [], "an ask run must never reach the runner"


def test_detail_view_replays_a_dry_run_as_a_dry_run(tmp_home, monkeypatch):
    """Replaying a rehearsal as a live run would fire the write tools the
    original deliberately stubbed. `px0 runs rerun` guards this too."""
    rehearsal = {"id": "run_old", "workflow_id": "wf-a", "trigger": "manual",
                 "outcome": "success", "dry_run": True, "tool_calls": [],
                 "guidelines_inlined": []}

    calls = _detail_view_pressing_r(tmp_home, monkeypatch, rehearsal, reads_before_exit=2)

    assert calls, "the rerun must reach the runner"
    assert all(kw.get("dry_run") is True for _, kw in calls), calls


def test_module_has_every_name_its_key_handlers_use():
    """`l` paged the log through `subprocess`, which was never imported -- the
    NameError landed in the handler's own except, so the key silently printed an
    error instead of opening the pager."""
    import px0.runs_tui as mod

    for name in ("subprocess", "os", "sys", "curses", "re", "runs_mod", "runner", "provenance"):
        assert hasattr(mod, name), f"runs_tui.{name} is missing"


def test_pager_command_comes_from_the_environment(monkeypatch, tmp_path):
    """Confirms the `l` path actually reaches subprocess.run now."""
    import px0.runs_tui as mod

    calls = []
    monkeypatch.setattr(mod.subprocess, "run", lambda argv, **kw: calls.append(argv))
    monkeypatch.setenv("PAGER", "my-pager")

    log = tmp_path / "run.log"
    log.write_text("content")
    mod.subprocess.run([os.environ["PAGER"], str(log)])

    assert calls == [["my-pager", str(log)]]


@pytest.mark.parametrize("text,days", [
    ("7d", 7), ("-7d", 7),      # the TUI's own prompt suggested the minus form
    ("2w", 14), ("-2w", 14),
])
def test_parse_since_accepts_the_forms_the_ui_offers(text, days):
    """`-7d` used to raise: the parser demanded a bare `7d` while the TUI prompt
    and the docs both suggested the leading minus."""
    from datetime import datetime
    from px0 import runs as runs_module

    parsed = runs_module.parse_since(text)
    assert round((datetime.now() - parsed).total_seconds() / 86400) == days


def test_parse_since_supports_hours_and_rejects_nonsense():
    from px0 import runs as runs_module

    assert runs_module.parse_since("12h") is not None
    for bad in ("x", "7", "d", "7 d", "7y", ""):
        with pytest.raises(ValueError, match="unsupported since format"):
            runs_module.parse_since(bad)


def test_runs_tui_does_not_import_the_cli():
    """The two imported each other; only deferral kept it working. The shared
    helper lives in runs.py now, so the cycle is gone."""
    import ast
    import pathlib

    source = pathlib.Path("px0/runs_tui.py").read_text()
    imported = set()
    for node in ast.walk(ast.parse(source)):
        if isinstance(node, ast.ImportFrom) and node.module:
            imported.add(node.module)
        elif isinstance(node, ast.Import):
            imported.update(a.name for a in node.names)
    assert "px0.cli" not in imported


def test_suspended_restores_curses_even_when_the_block_raises(monkeypatch, capsys):
    """A failing keystroke handler must not leave the terminal in raw mode.

    Four handlers used to hand-roll endwin/try/except/finally; one of them
    forgot an import and the others each repeated the restore.
    """
    import curses
    from px0 import runs_tui as mod

    events = []
    monkeypatch.setattr(curses, "endwin", lambda: events.append("endwin"))
    monkeypatch.setattr(curses, "initscr",
                        lambda: type("S", (), {"refresh": lambda self: events.append("refresh")})())
    monkeypatch.setattr("sys.stdin.read", lambda n: "\n")

    with mod._suspended():
        events.append("body")
        raise RuntimeError("pager exploded")

    assert events == ["endwin", "body", "refresh"]
    assert "pager exploded" in capsys.readouterr().out   # shown, not swallowed silently


def test_suspended_restores_curses_on_the_happy_path(monkeypatch):
    import curses
    from px0 import runs_tui as mod

    events = []
    monkeypatch.setattr(curses, "endwin", lambda: events.append("endwin"))
    monkeypatch.setattr(curses, "initscr",
                        lambda: type("S", (), {"refresh": lambda self: events.append("refresh")})())
    monkeypatch.setattr("sys.stdin.read", lambda n: "\n")

    with mod._suspended():
        events.append("body")

    assert events == ["endwin", "body", "refresh"]


def test_route_output_to_output_folder(tmp_home):
    from px0 import runner, paths
    from datetime import date

    # Default file path
    res = runner.route_output(tmp_home, {"target": "file"}, "Hello World")
    today = date.today().isoformat()
    expected_file = paths.output_dir(tmp_home) / f"output-{today}.md"
    assert res["target"] == "file"
    assert res["path"] == f"output/output-{today}.md"
    assert expected_file.exists()
    assert expected_file.read_text() == "Hello World"

    # Custom relative path without output prefix
    res2 = runner.route_output(tmp_home, {"target": "file", "path": "custom/report.md"}, "Report text")
    expected_custom = paths.output_dir(tmp_home) / "custom" / "report.md"
    assert res2["path"] == "output/custom/report.md"
    assert expected_custom.exists()
    assert expected_custom.read_text() == "Report text"

    # Legacy outputs/ prefix mapped to output/
    res3 = runner.route_output(tmp_home, {"target": "file", "path": "outputs/legacy.md"}, "Legacy text")
    expected_legacy = paths.output_dir(tmp_home) / "legacy.md"
    assert res3["path"] == "output/legacy.md"
    assert expected_legacy.exists()
    assert expected_legacy.read_text() == "Legacy text"
