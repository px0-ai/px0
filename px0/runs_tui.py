"""The `px0 runs` interactive browser: a curses list/detail view over run
records. The list is newest-first and filterable by workflow, outcome,
write activity, and age; the detail view adds the rendered prompt recovered
from the raw log, the guideline versions inlined, and per-tool-call
timings, with one keystroke each to rerun, page the log, show the output,
and trace provenance. Row text comes from `format_row`, shared with
`px0 runs list` so both render identically.
"""

import curses
import os
import re
import subprocess
import sys
from contextlib import contextmanager
from pathlib import Path

from px0 import runs as runs_mod, runner, provenance

def column_widths(records: list[dict]) -> dict[str, int]:
    """Widths that align a whole batch of rows into columns.

    Computed once by the caller and passed to every `format_row` so both the
    plain listing and the TUI lay out identically; without it each row would be
    formatted in isolation and the columns would jitter.
    """
    return {
        "id": max((len(r.get("id", "")) for r in records), default=0),
        "workflow_id": max((len(str(r.get("workflow_id") or "")) for r in records), default=0),
        "trigger": max((len(str(r.get("trigger") or "")) for r in records), default=0),
    }


def format_row(r: dict, widths: dict[str, int] | None = None) -> str:
    """Formats one run record into a single list row, shared between CLI and TUI.

    Columns are separated by two spaces and padded to `widths` when given, so a
    listing reads as a table; without widths the fields are simply joined.
    """
    widths = widths or {}
    wrote = any(c.get("is_write") for c in r.get("tool_calls", []))
    marker = "  [write]" if wrote else ""
    if r.get("dry_run"):
        # A rehearsal looked identical to a real run in the listing.
        marker += "  [dry-run]"
    verdict = (r.get("review") or {}).get("verdict")
    if verdict:
        # A run someone judged reads differently from one nobody looked at, and
        # the listing is where you go looking for the bad ones.
        marker += f"  [{verdict}]"
    fields = [
        r.get("id", "").ljust(widths.get("id", 0)),
        str(r.get("workflow_id") or "").ljust(widths.get("workflow_id", 0)),
        str(r.get("trigger") or "").ljust(widths.get("trigger", 0)),
        str(r.get("outcome") or ""),
    ]
    return ("  ".join(fields) + marker).rstrip()


def extract_rendered_prompt(raw_log_text: str) -> str:
    """Pulls the first turn's rendered prompt out of a run's raw log, which
    interleaves `--- turn N PROMPT ---` / `--- turn N OUTPUT ---` blocks.
    Returns "" if the log has no such block (e.g. the run failed before stage 5,
    or its raw log has aged out under retention)."""
    marker = "--- turn 1 PROMPT ---\n"
    start = raw_log_text.find(marker)
    if start == -1:
        return ""
    start += len(marker)
    end = raw_log_text.find("\n--- turn 1 OUTPUT ---", start)
    return raw_log_text[start:end if end != -1 else None]


def apply_filters(records: list[dict], workflow: str | None, outcome: str | None, write_only: bool, since: str | None) -> list[dict]:
    """Filters the list of run records based on the TUI parameters."""
    filtered = list(records)
    if workflow:
        pat = re.compile(re.escape(workflow), re.IGNORECASE)
        filtered = [r for r in filtered if r.get("workflow_id") and pat.search(r["workflow_id"])]

    if outcome and outcome != "all":
        filtered = [r for r in filtered if r.get("outcome") == outcome]

    if write_only:
        filtered = [r for r in filtered if any(c.get("is_write") for c in r.get("tool_calls", []))]

    if since:
        # since is an ISO date string (e.g. '2026-08-11')
        filtered = [r for r in filtered if r.get("start") and r["start"] >= since]

    return filtered


class NoTerminalError(RuntimeError):
    """Raised when the TUI is asked to start without a terminal to draw on."""
    pass


def run(home: Path, config: dict) -> None:
    """Entry point for the px0 runs curses TUI.

    Raises NoTerminalError when stdin or stdout is not a terminal -- curses
    otherwise emits escape sequences and dies inside `cbreak()`, which made
    `px0 runs | head` unusable. The caller falls back to the plain listing.
    """
    if not (sys.stdin.isatty() and sys.stdout.isatty()):
        raise NoTerminalError("no terminal available for the interactive browser")
    try:
        curses.wrapper(_main, home, config)
    except KeyboardInterrupt:
        pass


# curses colour-pair ids, mirroring ui.py's palette so the TUI and the plain
# commands read as one program.
_P_ACCENT, _P_OK, _P_ERR, _P_WARN, _P_DIM, _P_FAINT = range(1, 7)


def _init_palette() -> bool:
    """Sets up colour pairs. False on a terminal without colour, so callers fall
    back to A_DIM/A_BOLD attributes instead."""
    try:
        curses.start_color()
        curses.use_default_colors()
    except curses.error:
        return False
    if not curses.has_colors():
        return False
    for pair, fg in ((_P_ACCENT, 208), (_P_OK, 71), (_P_ERR, 167),
                     (_P_WARN, 179), (_P_DIM, 245), (_P_FAINT, 240)):
        try:
            curses.init_pair(pair, fg, -1)  # -1 keeps the terminal's own background
        except curses.error:
            return False
    return True


def _attr(pair: int, fallback: int = 0) -> int:
    """The attribute for a palette entry, or `fallback` when colour is unavailable."""
    return curses.color_pair(pair) if _HAS_COLOR else fallback


_HAS_COLOR = False


@contextmanager
def _suspended(prompt: str = "\nPress any key to resume..."):
    """Drops out of curses for a block that writes to the real terminal.

    Restores curses on the way out whatever happened, so an exception in a
    keystroke handler can never leave the terminal in raw mode with no cursor.
    Errors are shown to the user and swallowed: a failed pager or a missing
    record should return you to the list, not tear the TUI down.
    """
    curses.endwin()
    try:
        yield
    except Exception as e:
        print(f"\n{e}")
    finally:
        print(prompt)
        try:
            sys.stdin.read(1)
        except (OSError, ValueError):
            pass
        curses.initscr().refresh()


def _dim_sep() -> str:
    """The separator between header fields."""
    return "·"


def _filter_summary(workflow, outcome, write_only, since_raw) -> str:
    """One dim line naming only the filters actually in effect."""
    parts = []
    if workflow:
        parts.append(f"workflow={workflow}")
    if outcome and outcome != "all":
        parts.append(f"outcome={outcome}")
    if write_only:
        parts.append("writes only")
    if since_raw:
        parts.append(f"since={since_raw}")
    return "  ".join(parts) if parts else "no filters"


def _outcome_attr(record: dict) -> int:
    """Colours a row by its outcome: failures red, everything else plain."""
    if record.get("outcome") == "failed":
        return _attr(_P_ERR)
    if record.get("outcome") not in ("success", "failed"):
        return _attr(_P_DIM, curses.A_DIM)  # still running, or an old record
    return 0


def _addkeys(stdscr, y: int, width: int, keys: list[tuple[str, str]]) -> None:
    """Renders the key hints: each key accented, its label dim."""
    x = 1
    for key, label in keys:
        chunk = len(key) + len(label) + 3
        if x + chunk >= width:
            break
        stdscr.addstr(y, x, key, _attr(_P_ACCENT, curses.A_BOLD))
        x += len(key) + 1
        stdscr.addstr(y, x, label, _attr(_P_DIM, curses.A_DIM))
        x += len(label) + 2


def _main(stdscr, home: Path, config: dict) -> None:
    global _HAS_COLOR
    # Setup curses settings
    _HAS_COLOR = _init_palette()
    curses.curs_set(0)
    stdscr.nodelay(False)
    stdscr.keypad(True)

    # Initial state
    workflow_filter = None
    outcome_filter = "all"  # cycles: all -> success -> failed -> all
    write_only_filter = False
    since_filter = None
    since_filter_raw = None

    selected_index = 0
    scroll_offset = 0

    all_records = runs_mod.list_records(config)
    visible_records = apply_filters(all_records, workflow_filter, outcome_filter, write_only_filter, since_filter)

    while True:
        stdscr.clear()
        height, width = stdscr.getmaxyx()

        # Render Header: title left, count right, active filters below, then a rule
        title = f" px0 runs {_dim_sep()} {len(visible_records)} of {len(all_records)}"
        stdscr.addstr(0, 0, title[:width], _attr(_P_ACCENT, curses.A_BOLD) | curses.A_BOLD)
        active = _filter_summary(workflow_filter, outcome_filter,
                                write_only_filter, since_filter_raw)
        stdscr.addstr(1, 1, active[:width - 1], _attr(_P_DIM, curses.A_DIM))
        stdscr.addstr(2, 0, "─" * width, _attr(_P_FAINT, curses.A_DIM))

        # Render list rows
        max_rows = height - 5
        visible_records = apply_filters(all_records, workflow_filter, outcome_filter, write_only_filter, since_filter)
        
        # Clamp selection
        if not visible_records:
            selected_index = 0
            scroll_offset = 0
            stdscr.addstr(4, 2, "no runs match these filters",
                          _attr(_P_DIM, curses.A_DIM))
        else:
            selected_index = max(0, min(selected_index, len(visible_records) - 1))
            if selected_index < scroll_offset:
                scroll_offset = selected_index
            elif selected_index >= scroll_offset + max_rows:
                scroll_offset = selected_index - max_rows + 1

            widths = column_widths(visible_records)
            for idx in range(scroll_offset, min(scroll_offset + max_rows, len(visible_records))):
                rec = visible_records[idx]
                row_str = format_row(rec, widths)[:width - 4]
                y = 4 + (idx - scroll_offset)
                selected = idx == selected_index
                # the selection is a pointer, not a highlight bar -- less flicker,
                # and the row's own outcome colour stays readable
                marker = "›" if selected else " "
                row_attr = curses.A_BOLD if selected else _outcome_attr(rec)
                stdscr.addstr(y, 1, marker, _attr(_P_ACCENT, curses.A_BOLD))
                stdscr.addstr(y, 3, row_str, row_attr)

        # Footer / Hotkeys
        stdscr.addstr(height - 2, 0, "─" * width, _attr(_P_FAINT, curses.A_DIM))
        _addkeys(stdscr, height - 1, width, [
            ("↑↓", "move"), ("enter", "detail"), ("/", "workflow"), ("f", "outcome"),
            ("a", "writes"), ("s", "since"), ("c", "clear"), ("q", "quit"),
        ])

        # Handle keyboard input
        try:
            key = stdscr.getch()
        except KeyboardInterrupt:
            break

        if key in (ord('q'), ord('Q')):
            break

        elif key in (curses.KEY_UP, ord('k'), ord('K')):
            if selected_index > 0:
                selected_index -= 1

        elif key in (curses.KEY_DOWN, ord('j'), ord('J')):
            if selected_index < len(visible_records) - 1:
                selected_index += 1

        elif key == ord('/'):
            # Prompt for workflow id
            workflow_filter = _prompt(stdscr, height - 1, "Filter by Workflow ID: ")
            selected_index = 0

        elif key in (ord('f'), ord('F')):
            # cycle outcome
            if outcome_filter == "all":
                outcome_filter = "success"
            elif outcome_filter == "success":
                outcome_filter = "failed"
            else:
                outcome_filter = "all"
            selected_index = 0

        elif key in (ord('a'), ord('A')):
            write_only_filter = not write_only_filter
            selected_index = 0

        elif key in (ord('s'), ord('S')):
            ans = _prompt(stdscr, height - 1, "since (e.g. 7d, 2w, 12h): ")
            if ans:
                try:
                    since_filter = runs_mod.parse_since(ans)
                    since_filter_raw = ans
                except ValueError:
                    _status_err(stdscr, height - 1,
                                "invalid age -- use e.g. 7d, 2w, 12h")
            selected_index = 0

        elif key in (ord('c'), ord('C')):
            workflow_filter = None
            outcome_filter = "all"
            write_only_filter = False
            since_filter = None
            since_filter_raw = None
            selected_index = 0

        elif key in (curses.KEY_ENTER, 10, 13):
            if visible_records:
                _detail_view(stdscr, home, config, visible_records[selected_index])
                # Reload records list after return (might have run a rerun)
                all_records = runs_mod.list_records(config)


def _prompt(stdscr, y, prompt_text) -> str | None:
    curses.curs_set(1)
    height, width = stdscr.getmaxyx()
    stdscr.move(y, 0)
    stdscr.clrtoeol()
    stdscr.addstr(y, 0, prompt_text, _attr(_P_ACCENT, curses.A_BOLD))
    
    stdscr.nodelay(False)
    stdscr.keypad(True)
    curses.echo()
    try:
        val = stdscr.getstr(y, len(prompt_text), width - len(prompt_text) - 1).decode("utf-8").strip()
    except Exception:
        val = None
    curses.noecho()
    curses.curs_set(0)
    return val if val else None


def _status_err(stdscr, y, text) -> None:
    stdscr.move(y, 0)
    stdscr.clrtoeol()
    stdscr.addstr(y, 0, f"✗ {text}", _attr(_P_ERR, curses.A_BOLD))
    stdscr.getch()


def _detail_view(stdscr, home: Path, config: dict, record_brief: dict) -> None:
    run_id = record_brief["id"]
    
    while True:
        stdscr.clear()
        height, width = stdscr.getmaxyx()

        try:
            record = runs_mod.read_record(config, run_id)
        except Exception as e:
            stdscr.addstr(2, 2, f"Failed to read record for {run_id}: {e}")
            stdscr.getch()
            return

        raw_log = ""
        try:
            raw_log = runs_mod.read_raw_log(config, run_id)
        except Exception:
            pass

        stdscr.addstr(0, 0, f" {run_id}"[:width],
                      _attr(_P_ACCENT, curses.A_BOLD) | curses.A_BOLD)
        
        # Metadata. Labels stay lowercase and aligned to match the plain commands.
        review = record.get("review") or {}
        lines = [
            f"workflow: {record.get('workflow_id') or '-'}",
            f"trigger:  {record.get('trigger') or '-'}",
            f"outcome:  {record.get('outcome') or '-'}",
            f"duration: {record.get('duration_seconds', 'n/a')}s",
        ]
        if review.get("verdict"):
            note = f" -- {review['note']}" if review.get("note") else ""
            lines.append(f"marked:   {review['verdict']}{note}")

        # Render prompt
        prompt = extract_rendered_prompt(raw_log)
        lines.append("")
        if prompt:
            lines.append("rendered prompt")
            lines.append("  " + prompt.replace("\n", "\n  ")[:500] + ("..." if len(prompt) > 500 else ""))
        else:
            lines.append("rendered prompt")
            lines.append("  not available -- log retention removed it")

        guidelines = record.get("guidelines_inlined", [])
        lines.append("")
        lines.append(f"guidelines inlined ({len(guidelines)})")
        for g in guidelines:
            lines.append(f"  {g[0]} @ {g[1][:8] if len(g) > 1 else 'latest'}")
        if not guidelines:
            lines.append("  none")

        calls = record.get("tool_calls", [])
        lines.append("")
        lines.append(f"tool calls ({len(calls)})")
        for tc in calls:
            timing = f" {tc['elapsed_seconds']}s" if tc.get("elapsed_seconds") is not None else ""
            write = "  [write]" if tc.get("is_write") else ""
            lines.append(f"  {tc.get('tool')}{timing}{write} -> {tc.get('result_summary', '')[:80]}")
        if not calls:
            lines.append("  none")

        # Render detail pane. A line is either "Label: value" (label dimmed so the
        # value reads first) or a section title / indented continuation.
        max_y = height - 4
        for idx, line in enumerate(lines[:max_y]):
            y = 2 + idx
            try:
                if line.startswith("  "):
                    stdscr.addstr(y, 2, line[:width - 4], _attr(_P_DIM, curses.A_DIM))
                elif ":" in line and not line.endswith(":"):
                    label, _, value = line.partition(":")
                    stdscr.addstr(y, 2, f"{label}:", _attr(_P_DIM, curses.A_DIM))
                    stdscr.addstr(y, 2 + len(label) + 2, value.strip()[:width - 6],
                                  _outcome_attr(record) if label == "outcome" else 0)
                else:
                    stdscr.addstr(y, 2, line[:width - 4], curses.A_BOLD)
            except curses.error:
                pass

        stdscr.addstr(height - 2, 0, "─" * width, _attr(_P_FAINT, curses.A_DIM))
        _addkeys(stdscr, height - 1, width, [
            ("r", "rerun"), ("l", "log"), ("o", "output"),
            ("w", "why"), ("m", "mark"), ("esc", "back"),
        ])

        key = stdscr.getch()
        if key in (27, ord('q'), ord('Q')):
            break

        elif key in (ord('r'), ord('R')):
            new_run_id = None
            wf_id = record.get("workflow_id")
            was_dry = bool(record.get("dry_run"))
            with _suspended():
                if not wf_id:
                    # An ask run names no workflow. Without this the key handed
                    # the runner None, which wrote a phantom "no such workflow:
                    # None" record into the history before failing.
                    print("nothing to rerun: this run was an ask, not a workflow")
                elif was_dry:
                    # A rehearsal reruns as a rehearsal, as `px0 runs rerun`
                    # does: replaying a --dry-run record live would fire the
                    # write tools the original deliberately stubbed.
                    print(f"Rerunning {wf_id} with --dry-run (the original was one)...")
                    # runner.run returns the whole record, not an id
                    new_run_id = runner.run(home, config, wf_id, trigger="manual",
                                            dry_run=True)["id"]
                    print(f"Spawned {new_run_id}")
                else:
                    print(f"Rerunning {wf_id}...")
                    new_run_id = runner.run(home, config, wf_id,
                                            trigger="manual")["id"]
                    print(f"Spawned {new_run_id}")
            if new_run_id:
                run_id = new_run_id  # follow the rerun, so the view shows the new record

        elif key in (ord('l'), ord('L')):
            with _suspended():
                pager = os.environ.get("PAGER", "less")
                subprocess.run([pager, str(runs_mod.log_path(config, run_id))])

        elif key in (ord('o'), ord('O')):
            with _suspended():
                print("--- output ---")
                print(record.get("output", {}).get("text", ""))

        elif key in (ord('w'), ord('W')):
            with _suspended():
                print("--- provenance ---")
                print(provenance.why(config, run_id))

        elif key in (ord('m'), ord('M')):
            # Marking belongs here more than anywhere: this is the screen where
            # a person has just read what a run produced, which is the only
            # moment they know whether it was any good.
            with _suspended():
                # Everything in here can fail in ways that must not take the
                # browser down with them: the record can have aged out between
                # the listing and the keystroke, and Ctrl-D at either prompt
                # raises rather than answering.
                try:
                    print("--- mark this run ---")
                    print("g = good, b = bad, c = clear, anything else cancels")
                    answer = input("verdict: ").strip().lower()[:1]
                    verdict = {"g": "good", "b": "bad"}.get(answer)
                    if answer == "c":
                        runs_mod.mark(config, run_id, None)
                        print("cleared")
                    elif verdict:
                        note = input("what was right or wrong (optional): ").strip()
                        runs_mod.mark(config, run_id, verdict, note=note)
                        print(f"marked {verdict}")
                    else:
                        print("cancelled")
                except (EOFError, KeyboardInterrupt):
                    print("\ncancelled")
                except (FileNotFoundError, ValueError, OSError) as e:
                    print(f"could not mark it: {e}")
