"""Terminal presentation: colors, status glyphs, and the spinner.

Everything user-facing goes through here so the CLI has one voice. Two
rules shape the design:

1. **Subtle by default.** Colour marks meaning -- a failure, a value you
   can act on -- and nothing else. Labels and chrome are dim; values are
   plain. A screen of px0 output should read as mostly grey with a few
   deliberate accents, never as a colour test page.
2. **Plain when not a terminal.** Pipe px0 anywhere and every escape
   sequence disappears, glyphs fall back to ASCII (`[OK]`, `[FAIL]`), and
   the spinner goes silent. Output stays greppable, so scripts parsing it
   never see a byte of styling.

Honours `NO_COLOR` (any value disables), `FORCE_COLOR` (any value
enables, even when piped), `TERM=dumb`, and `--no-color`.
"""

import itertools
import os
import shutil
import sys
import threading
import time
from contextlib import contextmanager

# 256-colour codes. Deliberately narrow: an accent, four semantics, two greys.
_ACCENT = "208"   # amber -- px0's own voice: prompts, values worth acting on
_OK = "71"        # muted green, not the shouting default
_ERR = "167"      # muted red
_WARN = "179"     # muted amber-yellow
_INFO = "110"     # muted blue
_DIM = "245"      # labels, chrome, secondary text
_FAINT = "240"    # rules, the least important thing on screen

_forced: bool | None = None  # set by --no-color / FORCE_COLOR, overrides detection


def set_color(enabled: bool | None) -> None:
    """Forces colour on/off for the process. None restores auto-detection."""
    global _forced
    _forced = enabled


def is_tty(stream=None) -> bool:
    """True when `stream` is a real terminal, regardless of colour settings.

    Separate from `color_enabled` on purpose: FORCE_COLOR should add colour to
    piped output, but carriage-return redraws only make sense on a terminal, so
    the spinner gates on this instead.
    """
    stream = stream or sys.stdout
    try:
        return bool(stream.isatty())
    except (AttributeError, ValueError):
        return False


def color_enabled(stream=None) -> bool:
    """True when `stream` (default stdout) should receive escape sequences."""
    if _forced is not None:
        return _forced
    if os.environ.get("NO_COLOR") is not None:
        return False
    if os.environ.get("FORCE_COLOR") is not None:
        return True
    if os.environ.get("TERM") == "dumb":
        return False
    stream = stream or sys.stdout
    try:
        return bool(stream.isatty())
    except (AttributeError, ValueError):
        return False


def paint(text: str, code: str, *, bold: bool = False, stream=None) -> str:
    """Wraps text in an SGR sequence, or returns it untouched when colour is off."""
    if not text or not color_enabled(stream):
        return text
    prefix = "\033[1m" if bold else ""
    return f"{prefix}\033[38;5;{code}m{text}\033[0m"


# --- text roles ------------------------------------------------------------

def dim(text: str, **kw) -> str:
    """Secondary text: labels, units, anything the eye should skip."""
    return paint(text, _DIM, **kw)


def faint(text: str, **kw) -> str:
    """Chrome: rules and separators."""
    return paint(text, _FAINT, **kw)


def accent(text: str, **kw) -> str:
    """px0's own voice -- a value the user will act on."""
    return paint(text, _ACCENT, **kw)


def strong(text: str, **kw) -> str:
    """Emphasis without colour, for headings inside a plain block."""
    if not color_enabled(kw.get("stream")):
        return text
    return f"\033[1m{text}\033[0m"


# --- status lines ----------------------------------------------------------

_GLYPHS = {
    # role: (unicode glyph, ascii fallback, colour)
    "ok": ("✓", "[OK]", _OK),
    "err": ("✗", "[FAIL]", _ERR),
    "warn": ("!", "[WARN]", _WARN),
    "info": ("·", "[INFO]", _INFO),
    "step": ("›", ">", _ACCENT),
}


def glyph(role: str, stream=None) -> str:
    """The status marker for `role`, coloured on a terminal, bracketed ASCII when piped."""
    mark, fallback, code = _GLYPHS[role]
    if not color_enabled(stream):
        return fallback
    return paint(mark, code, stream=stream)


def _status(role: str, message: str, detail: str = "", *, width: int = 0, stream=None) -> None:
    stream = stream or sys.stdout
    line = f"{glyph(role, stream)} {message.ljust(width) if width else message}"
    if detail:
        line += f"  {dim(detail, stream=stream)}"
    print(line, file=stream, flush=True)


def ok(message: str, detail: str = "", **kw) -> None:
    """A check that passed, a thing that got created."""
    _status("ok", message, detail, **kw)


def err(message: str, detail: str = "", **kw) -> None:
    """A failure. Goes to stderr by default -- errors are not output."""
    kw.setdefault("stream", sys.stderr)
    _status("err", message, detail, **kw)


def warn(message: str, detail: str = "", **kw) -> None:
    """Something worth knowing that isn't a failure."""
    kw.setdefault("stream", sys.stderr)
    _status("warn", message, detail, **kw)


def info(message: str, detail: str = "", **kw) -> None:
    """Neutral progress narration."""
    _status("info", message, detail, **kw)


def step(message: str, detail: str = "", **kw) -> None:
    """One step in a multi-step flow."""
    _status("step", message, detail, **kw)


# --- structure -------------------------------------------------------------

def heading(text: str, stream=None) -> None:
    """A section title. One blank line above, never a box or a banner."""
    stream = stream or sys.stdout
    print(file=stream, flush=True)
    print(strong(text, stream=stream), file=stream, flush=True)


def rule(stream=None) -> None:
    """A full-width faint separator. Skipped entirely when not a terminal."""
    stream = stream or sys.stdout
    if not color_enabled(stream):
        return
    print(faint("─" * min(shutil.get_terminal_size((80, 24)).columns, 80), stream=stream),
          file=stream, flush=True)


def kv(label: str, value, *, width: int = 0, stream=None) -> None:
    """A dim label and a plain value, aligned when `width` is given."""
    stream = stream or sys.stdout
    text = f"{label}:".ljust(width) if width else f"{label}:"
    print(f"  {dim(text, stream=stream)} {value}", file=stream, flush=True)


def bullet(text: str, stream=None) -> None:
    """One item in a list."""
    stream = stream or sys.stdout
    print(f"  {faint('·', stream=stream)} {text}", file=stream, flush=True)


def hint(text: str, stream=None) -> None:
    """A next step. Dim, indented, always after a blank line."""
    stream = stream or sys.stdout
    print(file=stream)
    print(dim(text, stream=stream), file=stream, flush=True)


def command(text: str, stream=None) -> None:
    """A command the user can copy and run."""
    stream = stream or sys.stdout
    print(f"  {accent(text, stream=stream)}", file=stream, flush=True)


def prompt(text: str) -> str:
    """A styled input prompt. Returns what the user typed, stripped."""
    return input(f"{accent('›')} {text}").strip()


# --- spinner ---------------------------------------------------------------

_FRAMES = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
_INTERVAL = 0.08


class Spinner:
    """An animated progress indicator with an elapsed-seconds counter.

    A no-op unless stderr is a terminal: piped output gets one plain line
    at the start instead of a stream of redraws, and nothing at all when
    the caller asked for silence. Always writes to stderr so a spinner
    never lands in output the user is capturing.
    """

    def __init__(self, message: str, *, quiet: bool = False, stream=None):
        self.message = message
        self.stream = stream or sys.stderr
        # A terminal is required to redraw in place; colour alone is not enough.
        self.animated = not quiet and is_tty(self.stream)
        self.quiet = quiet
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None
        self._started = 0.0

    def _spin(self) -> None:
        for frame in itertools.cycle(_FRAMES):
            if self._stop.is_set():
                return
            elapsed = time.monotonic() - self._started
            # Hold the timer back for the first second: on a fast operation the
            # counter is noise, and "(0s)" reads as broken.
            timer = dim(f" ({elapsed:.0f}s)", stream=self.stream) if elapsed >= 1 else ""
            self.stream.write(f"\r{accent(frame, stream=self.stream)} {self.message}{timer}")
            self.stream.flush()
            self._stop.wait(_INTERVAL)

    def start(self) -> "Spinner":
        self._started = time.monotonic()
        if self.animated:
            self._thread = threading.Thread(target=self._spin, daemon=True)
            self._thread.start()
        elif not self.quiet:
            print(f"{self.message}...", file=self.stream, flush=True)
        return self

    def _erase(self) -> None:
        if not self.animated:
            return
        width = shutil.get_terminal_size((80, 24)).columns
        self.stream.write("\r" + " " * (width - 1) + "\r")
        self.stream.flush()

    def stop(self, final: str | None = None, role: str = "ok") -> None:
        """Stops the animation. `final` replaces the line with a status line."""
        self._stop.set()
        if self._thread is not None:
            self._thread.join(timeout=1.0)
        self._erase()
        if final and not self.quiet:
            elapsed = time.monotonic() - self._started
            detail = f"({elapsed:.1f}s)" if elapsed >= 1.0 else ""
            _status(role, final, detail, stream=self.stream)

    def update(self, message: str) -> None:
        """Changes the message mid-spin."""
        if self.animated:
            self._erase()
        self.message = message


@contextmanager
def spinner(message: str, *, done: str | None = None, quiet: bool = False, stream=None):
    """Runs a block under a spinner, clearing it on success or failure.

        with ui.spinner("Verifying key", done="key verified"):
            verify()

    On an exception the line is erased before it propagates, so a traceback
    or error message never lands on top of a half-drawn spinner.
    """
    sp = Spinner(message, quiet=quiet, stream=stream).start()
    try:
        yield sp
    except BaseException:
        sp.stop()
        raise
    else:
        sp.stop(done)
