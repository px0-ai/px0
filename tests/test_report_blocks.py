"""The aligned report blocks px0 prints: what a run did, and what a build made.

A run used to report itself as one line -- `github-daily success  run_2026... ->
output/logs/x.md` -- which is the shape that reads worst when you want to act on
it: the run id and the path are welded into one string, and the path is relative
to a store root that went unnamed.

Both blocks are now bulleted and aligned: one field per row, every path written
`~/.px0/...` so it is short and still openable, a list value one item per line,
and no status glyph on any row, since the verdict is the heading and the rows are
facts.
"""

import argparse
import re
from pathlib import Path

import pytest

from px0 import cli, paths, runner, ui

_ANSI = re.compile(r"\x1b\[[0-9;]*m")
_ROW_RE = re.compile(r"^  · (.+?)(?:\s{2,}(.*))?$")


@pytest.fixture(autouse=True)
def _plain(monkeypatch):
    """No colour, so the assertions are about text and not escape codes."""
    monkeypatch.setattr(ui, "_forced", False)


class _no_spinner:
    """`ui.spinner` stands in as a no-op context manager under capture."""
    def __init__(self, *a, **k):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *a):
        return False


def _parse(text: str):
    """The block as (label, [values]) pairs, continuation lines folded in.

    A continuation is a line indented to the row above's value column, which is
    what "one item per line, each under the first" means in text.
    """
    out = _ANSI.sub("", text)
    rows: list[tuple[str, list[str]]] = []
    column = None
    for line in out.splitlines():
        m = _ROW_RE.match(line)
        if m:
            label, value = m.group(1).strip(), (m.group(2) or "").strip()
            column = line.index(value, 4) if value else None
            rows.append((label, [value] if value else []))
        elif column and rows and line.startswith(" " * column) and line.strip():
            rows[-1][1].append(line.strip())
        elif line.strip():
            column = None
    return out, rows


def _run_block(capsys):
    return _parse(capsys.readouterr().err)


def _values(rows) -> dict:
    return {label: values for label, values in rows}


def _record(**over):
    record = {
        "id": "run_20260824-093444-f88a",
        "workflow_id": "github-daily-commit-summary",
        "outcome": "success",
        "duration_seconds": 39.6,
        "output": {"target": "file", "path": "output/logs/daily-commits-2026-08-24.md"},
        "tool_calls": [],
    }
    record.update(over)
    return record


# --- how a path is written ---------------------------------------------------

def test_an_output_path_is_written_from_the_home_directory(monkeypatch, capsys, tmp_path):
    """`~/.px0/...`: no store row to cross-reference, no home directory repeated
    on every row, and still a path a shell will open."""
    fake_home = tmp_path / "home"
    store = fake_home / ".px0"
    store.mkdir(parents=True)
    monkeypatch.setattr(Path, "home", classmethod(lambda cls: fake_home))

    cli._print_run_outcome(store, "github-daily-commit-summary", _record())

    out, rows = _run_block(capsys)
    assert _values(rows)["output"] == ["~/.px0/output/logs/daily-commits-2026-08-24.md"]
    assert "store" not in _values(rows), "the row it replaced"
    assert "->" not in out, "the run id and the path are no longer one string"


def test_a_store_outside_the_home_directory_stays_absolute(tmp_home, capsys):
    """A `PX0_HOME` elsewhere has no `~` to abbreviate against."""
    cli._print_run_outcome(tmp_home, "wf", _record())

    _, rows = _run_block(capsys)
    assert _values(rows)["output"] == [
        str(tmp_home / "output/logs/daily-commits-2026-08-24.md")]


def test_a_run_that_wrote_nothing_names_no_path_at_all(tmp_home, capsys):
    cli._print_run_outcome(tmp_home, "wf", _record(output={"target": "stdout", "text": "hi"}))

    _, rows = _run_block(capsys)
    assert _values(rows)["output"] == ["printed below"]


def test_every_field_is_its_own_labelled_row(tmp_home, capsys):
    cli._print_run_outcome(tmp_home, "github-daily-commit-summary", _record())

    out, rows = _run_block(capsys)
    assert "success github-daily-commit-summary" in out, "the heading names the workflow"
    assert [label for label, _ in rows] == ["run", "output", "took"]
    assert _values(rows)["run"] == ["run_20260824-093444-f88a"]
    assert _values(rows)["took"] == ["39.6s"]


def test_the_rows_are_aligned_on_one_column(tmp_home, capsys):
    """`dry run` is the longest label here, so every value lines up past it."""
    cli._print_run_outcome(tmp_home, "wf", _record(
        tool_calls=[{"tool": "composio:GITHUB_LIST_COMMITS"}], dry_run=True))

    out, rows = _parse(capsys.readouterr().err)
    lines = [ln for ln in _ANSI.sub("", out).splitlines() if _ROW_RE.match(ln)]
    assert len(lines) == len(rows) == 5
    columns = {ln.index(values[0], 4) for ln, (_, values) in zip(lines, rows) if values}
    assert len(columns) == 1, f"one value column, got {columns}"


# --- lists get a line each ---------------------------------------------------

def test_each_tool_a_run_called_gets_its_own_aligned_line(tmp_home, capsys):
    cli._print_run_outcome(tmp_home, "wf", _record(tool_calls=[
        {"tool": "composio:GITHUB_LIST_COMMITS"},
        {"tool": "composio:GITHUB_LIST_COMMITS"},
        {"tool": "composio:GITHUB_LIST_PULL_REQUESTS"},
        {"tool": "slack.post_message", "is_write": True, "stubbed": True},
    ]))

    out, rows = _parse(capsys.readouterr().err)
    assert _values(rows)["tools"] == [
        "composio:GITHUB_LIST_COMMITS x2",
        "composio:GITHUB_LIST_PULL_REQUESTS",
        "slack.post_message (stubbed)",
    ]
    assert "," not in out.split("tools")[1].split("took")[0], "lines, not a comma run-on"


def test_a_continuation_line_sits_under_the_first_value_not_the_label(tmp_home, capsys):
    cli._print_run_outcome(tmp_home, "wf", _record(tool_calls=[
        {"tool": "a.one"}, {"tool": "b.two"}]))

    out, _ = _parse(capsys.readouterr().err)
    lines = _ANSI.sub("", out).splitlines()
    first = next(ln for ln in lines if "a.one" in ln)
    second = next(ln for ln in lines if "b.two" in ln)
    assert first.index("a.one") == second.index("b.two")
    assert second.strip() == "b.two", "no bullet, no repeated label"


# --- what else the block reports --------------------------------------------

def test_a_rehearsal_says_so_rather_than_looking_like_a_real_run(tmp_home, capsys):
    cli._print_run_outcome(tmp_home, "wf", _record(dry_run=True))

    _, rows = _run_block(capsys)
    assert "stubbed" in _values(rows)["dry run"][0]


def test_a_retried_run_reports_which_attempt_succeeded(tmp_home, capsys):
    cli._print_run_outcome(tmp_home, "wf", _record(attempt=3, attempts=5))

    _, rows = _run_block(capsys)
    assert _values(rows)["attempt"] == ["3 of 5"]


def test_a_first_attempt_is_not_worth_a_row(tmp_home, capsys):
    cli._print_run_outcome(tmp_home, "wf", _record(attempt=1, attempts=3))

    _, rows = _run_block(capsys)
    assert "attempt" not in _values(rows)


def test_a_file_run_offers_the_command_that_prints_it(tmp_home, capsys):
    cli._print_run_outcome(tmp_home, "wf", _record())

    out, _ = _run_block(capsys)
    assert "px0 runs open run_20260824-093444-f88a" in out


# --- no glyphs, on either outcome -------------------------------------------

def test_no_row_carries_a_status_glyph(tmp_home, capsys):
    """A tick against `took` claims a check passed; nothing was checked."""
    cli._print_run_outcome(tmp_home, "wf", _record(dry_run=True))

    out, rows = _run_block(capsys)
    assert rows, "the rows still parse as bulleted rows"
    assert "[OK]" not in out and "✓" not in out
    assert "[FAIL]" not in out and "✗" not in out


def test_a_failure_keeps_the_same_shape_and_names_the_error(tmp_home, capsys):
    cli._print_run_outcome(
        tmp_home, "pr-review", {"id": "run_x", "outcome": "failed"},
        error="required input 'review_comments' failed: 404")

    out, rows = _run_block(capsys)
    assert "failed pr-review" in out
    assert _values(rows)["error"][0].endswith("404")
    assert "[FAIL]" not in out and "✗" not in out, \
        "a glyph would sit two columns left of every other label"
    assert "px0 runs logs run_x" in out, "the log is where the detail is"


def test_a_failure_before_a_run_record_exists_still_prints(tmp_home, capsys):
    """A workflow that fails validation has no id yet; the block must not blow up."""
    cli._print_run_outcome(tmp_home, "wf", {}, error="guidelines[] references missing file")

    out, rows = _run_block(capsys)
    assert "failed wf" in out
    assert [label for label, _ in rows] == ["error"]


# --- streams -----------------------------------------------------------------

def test_the_block_stays_on_stderr(tmp_home, capsys):
    """stdout belongs to the run's own output text and to `--json`."""
    cli._print_run_outcome(tmp_home, "wf", _record())

    captured = capsys.readouterr()
    assert captured.out == ""
    assert "run_20260824-093444-f88a" in captured.err


# --- ui.field, which both blocks are made of --------------------------------

def test_a_field_is_a_bullet_a_dim_label_and_a_plain_value(capsys):
    ui.field("output", "/tmp/x.md", width=8)

    line = _ANSI.sub("", capsys.readouterr().out).rstrip("\n")
    assert line == "  · output    /tmp/x.md"


def test_a_field_with_no_value_prints_just_its_label(capsys):
    ui.field("output", "")

    assert _ANSI.sub("", capsys.readouterr().out).rstrip() == "  · output"


def test_a_field_given_an_empty_list_prints_just_its_label(capsys):
    ui.field("tools", [])

    assert _ANSI.sub("", capsys.readouterr().out).rstrip() == "  · tools"


# --- the build's own block --------------------------------------------------

def test_a_build_reports_every_path_the_way_a_run_does(
        tmp_home, monkeypatch, capsys):
    """`workflows new` used to tick each row and print `output.path` as the plan
    wrote it -- a fragment missing the `output/` folder a run files it under."""
    from px0 import builder as builder_mod

    plan = builder_mod.Plan(
        trigger={"manual": True}, inputs=[], tools=[], body="do it",
        description="Summarize today's commits",
        output={"target": "file", "path": "logs/daily-commits-{{today}}.md"})

    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(cli.ui, "spinner", _no_spinner)
    monkeypatch.setattr(cli, "_discover_tools", lambda *a, **k: [])
    monkeypatch.setattr(cli, "_select_guidelines", lambda *a, **k: ["summarization.md"])
    monkeypatch.setattr(cli, "_author_guidelines", lambda *a, **k: [])
    monkeypatch.setattr(cli.builder_mod, "generate_plan", lambda *a, **k: plan)
    monkeypatch.setattr(cli.builder_mod, "check_feasibility", lambda *a, **k: [])
    monkeypatch.setattr(cli.catalogue_mod, "remember", lambda *a, **k: None)
    (paths.guidelines_dir(tmp_home) / "summarization.md").write_text("## H\n\nb\n")

    args = argparse.Namespace(yes=True, id="github-daily-commit-summary",
                              no_clarify=True, no_discover=True)
    cli._build_workflow(tmp_home, {}, "summarize today's commits", args,
                        existing_id=None, already_clarified=True)

    out, rows = _parse(capsys.readouterr().out)
    values = _values(rows)
    assert "created github-daily-commit-summary" in out
    assert "store" not in values
    assert values["workflow"] == [paths.display(
        paths.workflows_dir(tmp_home) / "github-daily-commit-summary.md")]
    assert values["guidelines"] == [paths.display(
        paths.guidelines_dir(tmp_home) / "summarization.md")]
    assert values["output"] == [paths.display(
        paths.output_dir(tmp_home) / "logs/daily-commits-{{today}}.md")], \
        "where a run will put it, `output/` included, placeholders intact"
    assert "[OK]" not in out and "✓" not in out, "bullets, not ticks"


def test_the_output_row_promises_where_a_run_actually_writes(tmp_home):
    """The build's row and the run's destination come from one function."""
    assert runner.output_rel("logs/daily.md") == "output/logs/daily.md"
    assert runner.output_rel("outputs/daily.md") == "output/daily.md"
    assert runner.output_rel("output/daily.md") == "output/daily.md"
    assert runner.output_destination(tmp_home, "logs/daily.md") == \
        (tmp_home / "output/logs/daily.md")


# --- paths.display -----------------------------------------------------------

def test_a_path_under_the_home_directory_is_written_with_a_tilde(monkeypatch, tmp_path):
    monkeypatch.setattr(Path, "home", classmethod(lambda cls: tmp_path))

    assert paths.display(tmp_path / ".px0/output/x.md") == "~/.px0/output/x.md"
    assert paths.display(tmp_path) == "~", "not `~/.`, which reads as a mistake"


def test_a_path_outside_the_home_directory_is_left_alone(monkeypatch, tmp_path):
    monkeypatch.setattr(Path, "home", classmethod(lambda cls: tmp_path / "home"))

    assert paths.display("/srv/px0/output/x.md") == "/srv/px0/output/x.md"
