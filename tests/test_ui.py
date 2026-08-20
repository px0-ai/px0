import io
import re
import time

import pytest

from px0 import ui


ESC = re.compile(r"\x1b\[[0-9;]*m")


class FakeTTY(io.StringIO):
    """A StringIO that claims to be a terminal."""

    def isatty(self):
        return True


@pytest.fixture(autouse=True)
def reset_ui(monkeypatch):
    """Colour detection is process-global; keep every test independent."""
    monkeypatch.delenv("NO_COLOR", raising=False)
    monkeypatch.delenv("FORCE_COLOR", raising=False)
    monkeypatch.delenv("TERM", raising=False)
    ui.set_color(None)
    yield
    ui.set_color(None)


# --- colour detection ------------------------------------------------------

def test_no_color_env_disables_colour(monkeypatch):
    monkeypatch.setenv("NO_COLOR", "1")
    assert ui.color_enabled(FakeTTY()) is False
    assert ui.paint("x", "208") == "x"


def test_no_color_honours_empty_value(monkeypatch):
    """The NO_COLOR spec says presence disables, whatever the value."""
    monkeypatch.setenv("NO_COLOR", "")
    assert ui.color_enabled(FakeTTY()) is False


def test_force_color_enables_on_a_pipe(monkeypatch):
    monkeypatch.setenv("FORCE_COLOR", "1")
    assert ui.color_enabled(io.StringIO()) is True


def test_term_dumb_disables_colour(monkeypatch):
    monkeypatch.setenv("TERM", "dumb")
    assert ui.color_enabled(FakeTTY()) is False


def test_pipe_is_plain_and_tty_is_coloured():
    assert ui.color_enabled(io.StringIO()) is False
    assert ui.color_enabled(FakeTTY()) is True


def test_set_color_overrides_everything(monkeypatch):
    monkeypatch.setenv("NO_COLOR", "1")
    ui.set_color(True)
    assert ui.color_enabled(io.StringIO()) is True
    ui.set_color(False)
    assert ui.color_enabled(FakeTTY()) is False


def test_closed_stream_does_not_raise():
    """isatty() on a closed file raises ValueError; detection must absorb it."""
    stream = io.StringIO()
    stream.close()
    assert ui.color_enabled(stream) is False


# --- output shape ----------------------------------------------------------

def test_piped_output_has_no_escape_sequences_and_ascii_glyphs():
    """Scripts parsing px0 must never see styling."""
    out = io.StringIO()
    ui.ok("credentials", "mode 0o600", stream=out)
    ui.err("connections", "gmail is INITIATED", stream=out)
    text = out.getvalue()

    assert "\x1b" not in text
    assert "[OK] credentials" in text
    assert "[FAIL] connections" in text


def test_tty_output_is_coloured_and_uses_glyphs():
    out = FakeTTY()
    ui.ok("credentials", "mode 0o600", stream=out)
    text = out.getvalue()

    assert "\x1b[38;5;" in text
    assert "✓" in ESC.sub("", text)
    assert "[OK]" not in text


def test_status_alignment_pads_the_message_only():
    out = io.StringIO()
    ui.ok("a", "detail-a", width=10, stream=out)
    ui.ok("bbbb", "detail-b", width=10, stream=out)
    first, second = [ESC.sub("", l) for l in out.getvalue().splitlines()]

    assert first.index("detail-a") == second.index("detail-b")


def test_rule_is_skipped_when_not_a_terminal():
    out = io.StringIO()
    ui.rule(stream=out)
    assert out.getvalue() == ""

    tty = FakeTTY()
    ui.rule(stream=tty)
    assert "─" in ESC.sub("", tty.getvalue())


def test_kv_dims_the_label_not_the_value():
    out = FakeTTY()
    ui.kv("harness", "claude -p", stream=out)
    raw = out.getvalue()

    assert "\x1b[38;5;245mharness:" in raw          # label dim
    assert "claude -p\x1b" not in raw               # value unstyled
    assert "claude -p" in ESC.sub("", raw)


def test_errors_and_warnings_default_to_stderr(capsys):
    ui.err("boom")
    ui.warn("careful")
    ui.ok("fine")
    captured = capsys.readouterr()

    assert "boom" in captured.err and "boom" not in captured.out
    assert "careful" in captured.err
    assert "fine" in captured.out


# --- spinner ---------------------------------------------------------------

def test_spinner_is_silent_on_a_pipe_but_announces_once():
    """No redraw stream in a pipe -- one plain line, so logs stay readable."""
    out = io.StringIO()
    sp = ui.Spinner("Working", stream=out).start()
    sp.stop()

    assert out.getvalue() == "Working...\n"
    assert "\r" not in out.getvalue()


def test_spinner_quiet_emits_nothing():
    out = io.StringIO()
    sp = ui.Spinner("Working", quiet=True, stream=out).start()
    sp.stop("done")
    assert out.getvalue() == ""


def test_spinner_animates_on_a_tty_and_erases_itself():
    out = FakeTTY()
    with ui.spinner("Working", done="finished", stream=out) as sp:
        assert sp.animated is True
        time.sleep(0.25)
    text = out.getvalue()

    assert "\r" in text                                  # redrew in place
    assert any(f in text for f in "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")          # animated
    assert ESC.sub("", text).rstrip().endswith("finished")


def test_spinner_erases_before_an_exception_propagates():
    """A traceback must never land on top of a half-drawn spinner line."""
    out = FakeTTY()
    with pytest.raises(RuntimeError):
        with ui.spinner("Working", stream=out):
            raise RuntimeError("boom")

    # last thing written is the erase, not a spinner frame
    assert out.getvalue().rstrip(" ").endswith("\r")


def test_spinner_thread_does_not_outlive_the_block():
    out = FakeTTY()
    with ui.spinner("Working", stream=out) as sp:
        time.sleep(0.15)
    assert sp._thread is not None and not sp._thread.is_alive()


def test_spinner_holds_the_timer_back_for_the_first_second():
    """`(0s)` reads as broken; the counter only earns its place after a second."""
    out = FakeTTY()
    with ui.spinner("Working", stream=out):
        time.sleep(0.2)
    assert "(0s)" not in ESC.sub("", out.getvalue())


def test_is_tty_is_independent_of_forced_colour(monkeypatch):
    """FORCE_COLOR adds colour to a pipe but must not make the spinner redraw."""
    monkeypatch.setenv("FORCE_COLOR", "1")
    pipe = io.StringIO()
    assert ui.color_enabled(pipe) is True
    assert ui.is_tty(pipe) is False
    assert ui.Spinner("x", stream=pipe).animated is False


# --- CLI integration -------------------------------------------------------

def test_global_json_flag_survives_subcommand_parsing():
    """`px0 --json <cmd>` must work, not just `px0 <cmd> --json`.

    Each subcommand that declares its own --json would otherwise reset the
    root parser's value to False when the flag is omitted there.
    """
    from px0 import cli

    parser = cli.build_parser()
    for argv in (["--json", "doctor"], ["doctor", "--json"],
                 ["--json", "runs", "list"], ["runs", "list", "--json"],
                 ["--json", "tools", "list"]):
        assert parser.parse_args(argv).json is True, argv

    assert parser.parse_args(["doctor"]).json is False
    assert parser.parse_args(["runs", "list"]).json is False


def test_no_color_flag_is_accepted_and_disables_colour():
    from px0 import cli

    args = cli.build_parser().parse_args(["--no-color", "doctor"])
    assert args.no_color is True


def test_json_output_is_never_decorated(monkeypatch, capsys):
    """--json data must stay parseable even on a terminal."""
    import argparse
    import json as json_mod
    from px0 import cli, doctor as doctor_mod

    monkeypatch.setenv("FORCE_COLOR", "1")
    ui.set_color(None)
    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (None, {}))
    monkeypatch.setattr(doctor_mod, "run",
                        lambda h, c, quick=False: {"all_ok": True, "checks": {}})

    # main() is what applies the flag, so drive it the way a user would
    monkeypatch.setattr(cli, "build_parser", _parser_returning(
        argparse.Namespace(json=True, no_color=False, quick=True, func=cli.cmd_doctor)))
    with pytest.raises(SystemExit):
        cli.main([])

    out = capsys.readouterr().out
    assert "\x1b" not in out
    assert json_mod.loads(out) == {"all_ok": True, "checks": {}}


def _parser_returning(namespace):
    class P:
        def parse_args(self, argv):
            return namespace
    return lambda: P()
