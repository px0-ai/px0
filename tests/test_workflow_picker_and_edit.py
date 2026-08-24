"""`workflows run` with no id puts up a picker; `workflows edit` rebuilds from a
revised request.

The picker's key handling is tested through `ui.select_action`, which is the
whole of it -- pulled out as a pure function so arrow keys don't need a pty.
"""

import io
import os
import re

import pytest

from px0 import builder as builder_mod, cli, ui, workflow as wf_mod


# --- key handling -----------------------------------------------------------

@pytest.mark.parametrize("key, expected", [
    ("up",    (2, "move")),      # wraps to the last row
    ("down",  (1, "move")),
    ("k",     (2, "move")),
    ("j",     (1, "move")),
    ("enter", (0, "choose")),
    ("q",     (0, "cancel")),
    ("cancel", (0, "cancel")),   # Ctrl-C / Ctrl-D
    ("3",     (2, "choose")),    # digit jumps straight there
    ("x",     (0, "ignore")),
    ("0",     (0, "ignore")),    # there is no row zero
    ("9",     (0, "ignore")),    # out of range
])
def test_each_key_maps_to_one_action(key, expected):
    assert ui.select_action(key, cursor=0, count=3) == expected


def test_movement_wraps_at_both_ends():
    assert ui.select_action("down", cursor=2, count=3)[0] == 0
    assert ui.select_action("up", cursor=0, count=3)[0] == 2


@pytest.mark.parametrize("raw, expected", [
    ("\x1b[A", "up"),
    ("\x1b[B", "down"),
    ("\r", "enter"),
    ("\n", "enter"),
    ("\x03", "cancel"),
    ("j", "j"),
    ("\x1bOP", "escape"),   # a non-CSI escape must not read as a character
])
def test_arrow_sequences_are_consumed_as_one_key(raw, expected):
    assert ui._read_key(io.StringIO(raw)) == expected


# --- row rendering: a wrapped row corrupts the redraw ----------------------

_ANSI = re.compile(r"\x1b\[[0-9;]*m")

_LONG_NAME = "on-demand-list-all-open-github-pull-requ"
_LONG_DETAIL = ("On demand, list all open GitHub pull requests assigned to the "
                "authenticated user, grouped by repository.")


def _visible(row: str) -> int:
    """Columns the row actually occupies -- escape codes take none."""
    return len(_ANSI.sub("", row))


@pytest.mark.parametrize("cols", [200, 120, 80, 60, 40, 24, 12, 8, 7, 3, 1, 0])
@pytest.mark.parametrize("selected", [True, False])
def test_a_row_never_exceeds_the_terminal_width(cols, selected, monkeypatch):
    """The reported bug: rows wider than the terminal wrapped, so the redraw's
    one-line-per-option cursor-up landed mid-row and appended a fresh copy of
    the entry on every keypress."""
    monkeypatch.setattr(ui, "_forced", True)   # colour on, so escapes are present
    row = ui.select_row(0, _LONG_NAME, _LONG_DETAIL, selected=selected,
                        name_width=len(_LONG_NAME), cols=cols)

    assert _visible(row) <= max(cols - 1, 0), repr(row)


def test_a_row_is_always_a_single_line():
    """Anything that emits a newline breaks the cursor-up arithmetic outright."""
    row = ui.select_row(0, _LONG_NAME, _LONG_DETAIL, selected=True,
                        name_width=len(_LONG_NAME), cols=40)
    assert "\n" not in row and "\r" not in row


def test_truncation_is_marked_so_a_cut_name_is_not_mistaken_for_the_whole_one():
    row = ui.select_row(0, _LONG_NAME, "", selected=False, name_width=80, cols=30)
    assert "\u2026" in _ANSI.sub("", row)


def test_a_row_that_fits_is_left_alone():
    plain = _ANSI.sub("", ui.select_row(0, "short", "Tiny.", selected=False,
                                       name_width=5, cols=80))
    assert plain.strip() == "1. short  Tiny."
    assert "\u2026" not in plain


def test_the_name_column_stays_aligned_across_rows():
    """Rows are padded to a shared width so the details line up."""
    rows = [_ANSI.sub("", ui.select_row(i, n, "d", selected=False,
                                       name_width=8, cols=80))
            for i, n in enumerate(["a", "bbbbbbbb"])]
    assert all(r.index("d") == rows[0].index("d") for r in rows)


def test_a_detail_is_dropped_rather_than_squeezed_to_nothing():
    """With no room for it, the name gets the space instead of a stray gap."""
    plain = _ANSI.sub("", ui.select_row(0, "abcdefghij", "some detail",
                                        selected=False, name_width=10, cols=20))
    assert "some" not in plain


def test_the_redraw_moves_up_exactly_as_many_lines_as_it_wrote(monkeypatch):
    """The invariant behind the whole picker: one physical line per option, and
    a cursor-up of exactly that many before overwriting them."""
    import shutil

    options = [(_LONG_NAME, _LONG_DETAIL),
               ("friday-afternoon-review-digest-to-eng", "Every Friday afternoon…"),
               ("short-one", "Tiny.")]
    written = _drive_select(monkeypatch, options, keys=["down", "down", "enter"], cols=80)

    frames = written.split("\x1b[3A")
    assert len(frames) == 3, "two moves should mean two cursor-ups of 3 lines"
    for frame in frames:
        assert frame.count("\r\n") == len(options), "each frame is one line per option"
    assert "\x1b[2A" not in written and "\x1b[4A" not in written


def test_a_move_that_changes_nothing_does_not_redraw(monkeypatch):
    """With one option, every arrow key is a no-op; redrawing would flicker."""
    written = _drive_select(monkeypatch, [("only-one", "d")],
                            keys=["down", "down", "up", "enter"], cols=80)
    assert "\x1b[1A" not in written, "nothing moved, so nothing should be redrawn"
    assert written.count("\r\n") == 1


def _drive_select(monkeypatch, options, keys, cols):
    """Runs `ui.select` with stubbed terminal plumbing; returns what it wrote."""
    import io
    import shutil

    out = io.StringIO()
    out.isatty = lambda: True
    monkeypatch.setattr(ui, "is_tty", lambda stream=None: True)
    monkeypatch.setattr(shutil, "get_terminal_size", lambda fallback=None: os.terminal_size((cols, 24)))

    fake_stdin = io.StringIO()
    fake_stdin.isatty = lambda: True
    fake_stdin.fileno = lambda: 0
    monkeypatch.setattr(ui.sys, "stdin", fake_stdin)

    supplied = iter(keys)
    monkeypatch.setattr(ui, "_read_key", lambda stream: next(supplied))

    import termios
    import tty
    monkeypatch.setattr(termios, "tcgetattr", lambda fd: None)
    monkeypatch.setattr(termios, "tcsetattr", lambda fd, when, attrs: None)
    monkeypatch.setattr(tty, "setraw", lambda fd: None)

    ui.select("Which?", options, stream=out)
    # drop the label and hint, which are printed before the redraw region
    return out.getvalue().split("to cancel", 1)[-1]


# --- the no-terminal fallback ----------------------------------------------

def test_without_a_terminal_the_picker_reads_a_number(monkeypatch, capsys):
    monkeypatch.setattr(ui, "is_tty", lambda stream=None: False)
    monkeypatch.setattr("builtins.input", lambda *a: "2")

    assert ui.select("Which?", [("a", "A"), ("b", "B")]) == 1
    assert "1." in capsys.readouterr().out, "the list still has to be shown"


@pytest.mark.parametrize("answer", ["", "0", "9", "abc"])
def test_a_bad_number_cancels_rather_than_guessing(monkeypatch, answer):
    monkeypatch.setattr(ui, "is_tty", lambda stream=None: False)
    monkeypatch.setattr("builtins.input", lambda *a: answer)

    assert ui.select("Which?", [("a", ""), ("b", "")]) is None


def test_eof_cancels(monkeypatch):
    monkeypatch.setattr(ui, "is_tty", lambda stream=None: False)
    def eof(*a):
        raise EOFError
    monkeypatch.setattr("builtins.input", eof)

    assert ui.select("Which?", [("a", "")]) is None


def test_an_empty_list_has_nothing_to_pick():
    assert ui.select("Which?", []) is None


# --- resolving the workflow to run -----------------------------------------

def _add(home, wid, description="does a thing"):
    plan = builder_mod.Plan(trigger={"type": "manual"}, inputs=[], tools=[],
                            output={"target": "stdout"}, body="do it",
                            description=description)
    builder_mod.save_workflow(home, wid,
                              builder_mod.render_workflow_file(wid, plan, [], f"make {wid}"))


def test_the_picker_offers_every_workflow_and_returns_the_chosen_id(monkeypatch, tmp_home):
    _add(tmp_home, "alpha")
    _add(tmp_home, "beta")
    offered = {}
    monkeypatch.setattr(cli.ui, "select",
                        lambda label, options: offered.setdefault("o", options) and None or 1)

    assert cli._pick_workflow(tmp_home, for_stdin=False) == "beta"
    assert [name for name, _ in offered["o"]] == ["alpha", "beta"]


def test_cancelling_the_picker_exits_cleanly(monkeypatch, tmp_home):
    _add(tmp_home, "alpha")
    monkeypatch.setattr(cli.ui, "select", lambda *a, **k: None)

    with pytest.raises(SystemExit) as exc:
        cli._pick_workflow(tmp_home, for_stdin=False)
    assert exc.value.code == 0, "cancelling is not an error"


def test_stdin_input_needs_an_explicit_id(tmp_home, monkeypatch):
    """The picker would read the keystrokes off the stream carrying the input."""
    _add(tmp_home, "alpha")
    monkeypatch.setattr(cli.ui, "select",
                        lambda *a, **k: pytest.fail("must not prompt when --stdin"))

    with pytest.raises(SystemExit) as exc:
        cli._pick_workflow(tmp_home, for_stdin=True)
    assert exc.value.code == cli.EXIT_USER_ERROR


def test_an_empty_store_says_so_instead_of_showing_an_empty_picker(tmp_home):
    with pytest.raises(SystemExit) as exc:
        cli._pick_workflow(tmp_home, for_stdin=False)
    assert exc.value.code == cli.EXIT_USER_ERROR


def test_run_accepts_no_workflow_argument():
    assert cli.build_parser().parse_args(["workflows", "run"]).workflow is None


# --- the original request, stored and shown back --------------------------

def test_the_users_own_sentence_survives_the_round_trip(tmp_home):
    """`description` is the model's restatement; `request` is what they typed."""
    plan = builder_mod.Plan(trigger={"type": "manual"}, inputs=[], tools=[],
                            output={"target": "stdout"}, body="b",
                            description="Summarize open pull requests")
    text = builder_mod.render_workflow_file(
        "prs", plan, [], "every friday, tell me what I reviewed")
    builder_mod.save_workflow(tmp_home, "prs", text)

    wf = wf_mod.load_all(tmp_home)["prs"]
    assert wf.request == "every friday, tell me what I reviewed"
    assert wf.description == "Summarize open pull requests"


def test_no_request_field_is_written_when_there_is_nothing_to_store(tmp_home):
    plan = builder_mod.Plan(trigger={}, inputs=[], tools=[], output={}, body="b",
                            description="d")
    assert "request:" not in builder_mod.render_workflow_file("x", plan, [], "   ")


def test_a_workflow_written_before_request_existed_still_loads(tmp_home):
    """Older files simply have no `request:`; that must not be an error."""
    path = tmp_home / "workflows" / "old.md"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("---\nid: old\ndescription: an old one\n---\nbody\n")

    assert wf_mod.parse(path).request == ""


# --- edit -------------------------------------------------------------------

class _EditArgs:
    def __init__(self, workflow=None):
        self.workflow = workflow
        self.yes = False
        self.no_clarify = False
        self.no_discover = False


def test_edit_shows_the_original_request_back(monkeypatch, tmp_home, capsys):
    _add(tmp_home, "alpha", "Summarize things")
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "")   # keep it unchanged

    cli.cmd_workflows_edit(_EditArgs("alpha"))

    out = capsys.readouterr().out
    assert "make alpha" in out, "the stored request must be shown"
    assert "unchanged" in out


def test_an_empty_answer_leaves_the_workflow_alone(monkeypatch, tmp_home):
    _add(tmp_home, "alpha")
    before = (tmp_home / "workflows" / "alpha.md").read_text()
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "  ")
    monkeypatch.setattr(cli, "_build_workflow",
                        lambda *a, **k: pytest.fail("must not rebuild"))

    cli.cmd_workflows_edit(_EditArgs("alpha"))

    assert (tmp_home / "workflows" / "alpha.md").read_text() == before


def test_edit_rebuilds_under_the_same_id(monkeypatch, tmp_home):
    """An edit replaces the workflow; it must not fork a near-duplicate."""
    _add(tmp_home, "alpha")
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "do it differently")
    seen = {}
    monkeypatch.setattr(cli, "_build_workflow",
                        lambda home, config, desc, args, existing_id: seen.update(
                            desc=desc, existing_id=existing_id))

    cli.cmd_workflows_edit(_EditArgs("alpha"))

    assert seen == {"desc": "do it differently", "existing_id": "alpha"}


def test_editing_an_unknown_workflow_is_a_user_error(monkeypatch, tmp_home):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))

    with pytest.raises(SystemExit) as exc:
        cli.cmd_workflows_edit(_EditArgs("nope"))
    assert exc.value.code == cli.EXIT_USER_ERROR


def test_edit_with_no_id_picks_one(monkeypatch, tmp_home):
    _add(tmp_home, "alpha")
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(cli.ui, "select", lambda label, options: 0)
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "")

    cli.cmd_workflows_edit(_EditArgs(None))   # must not raise
