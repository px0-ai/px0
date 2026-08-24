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


def alert(text: str, **kw) -> str:
    """Something that went wrong, inline: the counterpart to `accent`.

    For a value inside a block that is otherwise neutral, where a `✗` status line
    would break the block's alignment to say what the colour already says.
    """
    return paint(text, _ERR, **kw)


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

def heading(text: str, *, color: str | None = None, stream=None) -> None:
    """A section title. One blank line above, never a box or a banner.

    Plain bold by default; `color` paints it instead (still bold), for the
    rare heading that should stand out as more than structure -- e.g. a
    question back to the user rather than a status section.
    """
    stream = stream or sys.stdout
    print(file=stream, flush=True)
    styled = paint(text, color, bold=True, stream=stream) if color else strong(text, stream=stream)
    print(styled, file=stream, flush=True)


def say(text: str, stream=None) -> None:
    """Something px0 itself is telling the user -- a finding, a plan, a summary
    handed back for review -- as opposed to `heading`'s plain section titles or
    `prompt`'s request for an answer.

    One blank line above, a glyph that marks it as px0's own voice, both in the
    same muted blue everywhere so "here is what I found" always reads the same
    regardless of which command said it.
    """
    stream = stream or sys.stdout
    print(file=stream, flush=True)
    mark = paint("◆", _INFO, stream=stream) if color_enabled(stream) else "*"
    print(f"{mark} {paint(text, _INFO, bold=True, stream=stream)}", file=stream, flush=True)


def remark(text: str, *, color: str | None = None, stream=None) -> None:
    """A line in px0's own voice styled exactly like a follow-up question --
    the same "› " marker, the same weight -- but with nothing to answer.

    For the moment in an interview where what follows is the answer itself
    (the request just written up) rather than another question: matching
    `prompt`'s look keeps that moment reading as the same back-and-forth
    instead of switching to a status voice mid-conversation.
    """
    stream = stream or sys.stdout
    print(file=stream, flush=True)
    print(paint(f"› {text}", color or _ACCENT, stream=stream), file=stream, flush=True)


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


# "  · " before the label, two spaces after it: what a continuation line has to
# clear to sit under the first value rather than under the label.
_FIELD_LEAD = 4
_FIELD_GAP = 2


def field(label: str, value, *, width: int = 0, stream=None) -> None:
    """One field of a report: a bullet, a dim aligned label, and its value.

    `kv` with a bullet instead of a colon, for a block of fields rather than a
    couple of lines -- and deliberately not a status line. A run's `output` or
    `took` is a fact being reported, not a check that passed, and a column of
    ticks down the side of facts reads as noise while making the one line that
    *is* a verdict harder to find.

    A list value prints one item per line, each under the first, so a field with
    four tool ids in it stays readable at a glance instead of becoming a comma
    run-on that wraps wherever the terminal happens to end.
    """
    stream = stream or sys.stdout
    text = label.ljust(width) if width else label
    line = f"  {faint('·', stream=stream)} {dim(text, stream=stream)}"

    values = list(value) if isinstance(value, (list, tuple)) else [value]
    values = [v for v in values if v != ""]
    if values:
        line += f"{' ' * _FIELD_GAP}{values[0]}"
    print(line, file=stream, flush=True)
    indent = " " * (_FIELD_LEAD + max(width, len(label)) + _FIELD_GAP)
    for extra in values[1:]:
        print(f"{indent}{extra}", file=stream, flush=True)


def hint(text: str, stream=None) -> None:
    """A next step. Dim, indented, always after a blank line."""
    stream = stream or sys.stdout
    print(file=stream)
    print(dim(text, stream=stream), file=stream, flush=True)


def remedy(text: str, stream=None) -> None:
    """The fix for a failed check: an arrow-led step, indented under its line."""
    stream = stream or sys.stdout
    mark = paint("\u2192", _ACCENT, stream=stream) if color_enabled(stream) else "->"
    print(f"    {mark} {dim(text, stream=stream)}", file=stream, flush=True)


def command(text: str, stream=None) -> None:
    """A command the user can copy and run."""
    stream = stream or sys.stdout
    print(f"  {accent(text, stream=stream)}", file=stream, flush=True)


def prompt(text: str, *, color: str | None = None) -> str:
    """A styled input prompt. Returns what the user typed, stripped.

    Accent (px0's own voice) by default -- the colour that marks the one
    question opening an interview, or a plain yes/no confirmation. Pass
    `color` for a question that is a follow-up to one already asked, so the
    two read as different weight rather than an unbroken wall of accent.
    """
    print()
    return input(paint(f"› {text}", color or _ACCENT)).strip()


def select(label: str, options: list[tuple[str, str]], stream=None) -> int | None:
    """Single-select from a short list. Returns the chosen index, or None if cancelled.

    `options` is [(name, detail)]. Arrows or j/k move, Enter chooses, a digit
    jumps straight to that row, q or Ctrl-C cancels.

    Drawn in place on the lines it already occupies rather than in a full-screen
    curses app: picking one item out of a handful should leave the scrollback
    intact, the way a prompt does. When stdin is not a terminal there are no
    keystrokes to read, so it degrades to a numbered prompt -- which is what
    keeps this usable over a pipe and in tests.
    """
    stream = stream or sys.stdout
    if not options:
        return None
    if not (is_tty(stream) and sys.stdin.isatty()):
        return _select_numbered(label, options, stream)

    import termios
    import tty

    print(file=stream)
    width = max(len(name) for name, _ in options)
    cursor = 0

    def draw(first: bool) -> None:
        if not first:
            # Back up over the rows written last time and overwrite them. This is
            # only correct because every row is truncated to one physical line --
            # a wrapped row would put the cursor mid-row and each redraw would
            # append instead of replace.
            stream.write(f"\x1b[{len(options)}A")
        cols = shutil.get_terminal_size((80, 24)).columns
        for i, (name, detail) in enumerate(options):
            row = select_row(i, name, detail, selected=(i == cursor),
                             name_width=width, cols=cols, stream=stream)
            stream.write(f"\x1b[2K{row}\r\n")
        stream.flush()

    print(accent(f"› {label}", stream=stream), file=stream, flush=True)
    print(dim("  ↑/↓ to move, enter to select, q to cancel", stream=stream),
          file=stream, flush=True)

    fd = sys.stdin.fileno()
    saved = termios.tcgetattr(fd)
    chosen: int | None = None
    try:
        tty.setraw(fd)
        draw(first=True)
        while True:
            previous = cursor
            cursor, action = select_action(_read_key(sys.stdin), cursor, len(options))
            if action == "choose":
                chosen = cursor
                break
            if action == "cancel":
                break
            if action == "move" and cursor != previous:
                draw(first=False)
    finally:
        termios.tcsetattr(fd, termios.TCSADRAIN, saved)
        # The last draw left the cursor below the rows; land on a clean line.
        stream.write("\r")
        stream.flush()
    return chosen


def _read_key(stream) -> str:
    """Reads one keypress as a name: "up", "down", "enter", "cancel", or the char.

    Arrow keys arrive as a three-byte `ESC [ A` sequence, so the escape has to be
    consumed here rather than surfacing as a bare character.
    """
    ch = stream.read(1)
    if ch == "\x1b":
        if stream.read(1) != "[":
            return "escape"
        return {"A": "up", "B": "down", "C": "right", "D": "left"}.get(
            stream.read(1), "escape")
    if ch in ("\r", "\n"):
        return "enter"
    if ch in ("\x03", "\x04"):  # Ctrl-C, Ctrl-D
        return "cancel"
    return ch


def select_row(index: int, name: str, detail: str, *, selected: bool,
               name_width: int, cols: int, stream=None) -> str:
    """One `select` row, truncated to sit on a single physical line of `cols`.

    Truncation is load-bearing, not cosmetic: `select` redraws by moving the
    cursor up one line per option, so a row long enough to wrap makes the cursor
    land in the middle of it and every redraw appends a fresh copy instead of
    replacing the old one.

    The plain text is measured and cut *before* colour is applied, because escape
    sequences take no columns but do take characters.
    """
    marker_plain = "\u203a" if selected else " "
    num_plain = f"{index + 1:>2}."
    head = f" {marker_plain} {num_plain} "
    # -1 so nothing lands in the final column, which wraps on some terminals.
    if cols - 1 < len(head):
        # Narrower than the row's own prefix. Degenerate, but it still must not
        # wrap, so give up the colour and return a plain stub that fits.
        return head[:max(cols - 1, 0)]
    budget = cols - 1 - len(head)

    name_plain = _ellipsize(name, budget)
    name_plain = name_plain.ljust(min(name_width, budget))
    rest = budget - len(name_plain)
    detail_plain = "  " + _ellipsize(detail, rest - 2) if detail and rest > 3 else ""

    marker = paint("\u203a", _ACCENT, stream=stream) if selected else " "
    shown = paint(name_plain, _ACCENT, bold=True, stream=stream) if selected \
        else dim(name_plain, stream=stream)
    row = f" {marker} {faint(num_plain, stream=stream)} {shown}"
    return row + (dim(detail_plain, stream=stream) if detail_plain else "")


def _ellipsize(text: str, budget: int) -> str:
    """`text` cut to `budget` columns, marking the cut with a single character."""
    if budget <= 0:
        return ""
    if len(text) <= budget:
        return text
    return text[:budget - 1] + "\u2026" if budget > 1 else text[:budget]


def numbered(items: list[tuple[str, str]], stream=None) -> None:
    """Prints `items` as the rows `select` would draw, without the cursor.

    Shares `select_row` with the picker deliberately: a listing and the picker
    over the same things should read as the same list, so the number beside a
    row here is the number that picks it there.
    """
    stream = stream or sys.stdout
    if not items:
        return
    width = max(len(name) for name, _ in items)
    cols = shutil.get_terminal_size((80, 24)).columns
    for i, (name, detail) in enumerate(items):
        print(select_row(i, name, detail, selected=False,
                         name_width=width, cols=cols, stream=stream), file=stream)


def select_action(key: str, cursor: int, count: int) -> tuple[int, str]:
    """Maps a key name to (new cursor, action) for `select`.

    Actions: "move", "choose", "cancel", "ignore". Movement wraps -- with a
    handful of options, going up from the first to reach the last is quicker than
    hitting the ceiling. Pure, so the key handling is testable without a pty.
    """
    if key in ("up", "k", "K"):
        return (cursor - 1) % count, "move"
    if key in ("down", "j", "J"):
        return (cursor + 1) % count, "move"
    if key == "enter":
        return cursor, "choose"
    if key in ("cancel", "q", "Q"):
        return cursor, "cancel"
    if len(key) == 1 and key.isdigit() and key != "0" and int(key) <= count:
        return int(key) - 1, "choose"
    return cursor, "ignore"


def _select_numbered(label: str, options: list[tuple[str, str]], stream) -> int | None:
    """The no-terminal fallback: print the list, read a number."""
    print(file=stream)
    print(accent(f"› {label}", stream=stream), file=stream, flush=True)
    width = max(len(name) for name, _ in options)
    for i, (name, detail) in enumerate(options, 1):
        line = f"  {faint(f'{i:>2}.', stream=stream)} {name.ljust(width)}"
        if detail:
            line += f"  {dim(detail, stream=stream)}"
        print(line, file=stream, flush=True)
    try:
        answer = input("  number: ").strip()
    except EOFError:
        return None
    if not answer.isdigit() or not 1 <= int(answer) <= len(options):
        return None
    return int(answer) - 1


# --- markdown / yaml preview -------------------------------------------------
#
# Handed off to rich rather than hand-rolled: a real markdown renderer and a
# Pygments-backed YAML highlighter give proper multi-colour syntax highlighting
# (keys, strings, punctuation each their own colour) instead of a single
# accent colour standing in for every kind of token. Imported lazily -- this
# is the only place in the CLI that needs it, and every other command should
# not pay for the import.


def _rich_console(stream, file):
    from rich.console import Console
    from rich.theme import Theme

    enabled = color_enabled(stream)
    return Console(
        file=file,
        no_color=not enabled,
        force_terminal=enabled or None,
        width=shutil.get_terminal_size((80, 24)).columns,
        highlight=False,
        # rich's own markdown styles put inline code on a background box (a
        # broken black chip on a light terminal theme) and lean on magenta/cyan
        # for headings, numbered markers, and quotes -- colours from outside
        # px0's own narrow palette. A workflow body is mostly numbered steps and
        # tool ids in backticks (builder.py asks the model to write it that way),
        # so painting those the same accent as every prompt and CTA read as the
        # whole body being orange. Re-themed to px0's own palette instead: code
        # gets the quiet info blue, list markers match the grey bullets already
        # use, and headings/quotes drop their colour and lean on weight, so a
        # plan reads as prose with a little emphasis, not a colour test page.
        theme=Theme({
            "markdown.code": f"color({_INFO})",
            "markdown.code_block": f"color({_INFO})",
            "markdown.item.bullet": f"color({_FAINT})",
            "markdown.item.number": f"color({_FAINT})",
            "markdown.h2": "bold underline",
            "markdown.h3": "bold",
            "markdown.h4": "italic",
            "markdown.block_quote": f"italic color({_DIM})",
        }),
    )


def _print_rich(renderables: list, stream=None) -> None:
    """Prints rich renderables to `stream`, one line at a time, with trailing
    padding stripped.

    Rich pads list items and tables out to the full console width so a
    background style stays consistent across the line -- with no background
    set, that padding is just literal trailing spaces on every list line in a
    workflow preview. Rendered into a buffer first so each line can be
    trimmed before it reaches the real stream.
    """
    import io

    stream = stream or sys.stdout
    buf = io.StringIO()
    console = _rich_console(stream, buf)
    for renderable in renderables:
        console.print(renderable)
    for line in buf.getvalue().split("\n"):
        print(line.rstrip(), file=stream)


def render_markdown(text: str, stream=None) -> None:
    """Prints markdown via rich: real headings, lists, bold, and code spans."""
    from rich.markdown import Markdown

    _print_rich([Markdown(text)], stream=stream)


def render_workflow_markdown(content: str, stream=None) -> None:
    """Renders a workflow file's `---\\nyaml\\n---\\nbody` text: the frontmatter
    syntax-highlighted as YAML, the body rendered as markdown. Falls back to
    plain markdown rendering if there is no frontmatter.
    """
    end = content.find("\n---\n", 4) if content.startswith("---\n") else -1
    if end == -1:
        render_markdown(content, stream=stream)
        return

    from rich.markdown import Markdown
    from rich.syntax import Syntax
    from rich.text import Text

    _print_rich([
        Text("---", style="dim"),
        Syntax(content[4:end], "yaml", background_color="default", word_wrap=True),
        Text("---", style="dim"),
        Text(""),
        Markdown(content[end + 5:]),
    ], stream=stream)


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
            self.stream.write(f"\r{accent(frame, stream=self.stream)} "
                              f"{dim(self.message, stream=self.stream)}{timer}")
            self.stream.flush()
            self._stop.wait(_INTERVAL)

    def start(self) -> "Spinner":
        self._started = time.monotonic()
        if self.animated:
            self._thread = threading.Thread(target=self._spin, daemon=True)
            self._thread.start()
        elif not self.quiet:
            print(dim(f"{self.message}...", stream=self.stream), file=self.stream, flush=True)
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
