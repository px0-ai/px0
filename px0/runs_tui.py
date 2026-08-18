import curses
import os
import sys
import shutil
import json
import re
from datetime import datetime, timezone
from pathlib import Path

from px0 import runs as runs_mod, runner, provenance, config as config_mod, paths
from px0.cli import _parse_since

def format_row(r: dict) -> str:
    """Formats one run record into a single list row string, shared between CLI and TUI."""
    wrote = any(c.get("is_write") for c in r.get("tool_calls", []))
    marker = " [write]" if wrote else ""
    return f"{r['id']}\t{r.get('workflow_id')}\t{r['trigger']}\t{r.get('outcome')}{marker}"


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


def run(home: Path, config: dict) -> None:
    """Entry point for the px0 runs curses TUI."""
    try:
        curses.wrapper(_main, home, config)
    except KeyboardInterrupt:
        pass


def _main(stdscr, home: Path, config: dict) -> None:
    # Setup curses settings
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

        # Render Header
        filter_str = f"Filters: [W]orkflow: {workflow_filter or 'None'} | [F]outcome: {outcome_filter} | [A]ctivity (write): {'True' if write_only_filter else 'False'} | [S]ince: {since_filter_raw or 'None'}"
        stdscr.addstr(0, 0, "px0 runs TUI - List View".center(width)[:width], curses.A_REVERSE)
        stdscr.addstr(1, 0, filter_str[:width], curses.A_DIM)
        stdscr.addstr(2, 0, "=" * width, curses.A_DIM)

        # Render list rows
        max_rows = height - 5
        visible_records = apply_filters(all_records, workflow_filter, outcome_filter, write_only_filter, since_filter)
        
        # Clamp selection
        if not visible_records:
            selected_index = 0
            scroll_offset = 0
            stdscr.addstr(4, 2, "No run records match current filters.")
        else:
            selected_index = max(0, min(selected_index, len(visible_records) - 1))
            if selected_index < scroll_offset:
                scroll_offset = selected_index
            elif selected_index >= scroll_offset + max_rows:
                scroll_offset = selected_index - max_rows + 1

            for idx in range(scroll_offset, min(scroll_offset + max_rows, len(visible_records))):
                rec = visible_records[idx]
                row_str = format_row(rec)
                # truncate or pad
                row_str = row_str.replace("\t", "   ")[:width - 4]
                y = 4 + (idx - scroll_offset)
                if idx == selected_index:
                    stdscr.addstr(y, 2, f"> {row_str}", curses.A_REVERSE)
                else:
                    stdscr.addstr(y, 2, f"  {row_str}")

        # Footer / Hotkeys
        footer = "Keys: ↑/↓ Move | Enter Detail | / Workflow | f Outcome | a WriteOnly | s Since | c Clear | q Quit"
        stdscr.addstr(height - 1, 0, footer[:width], curses.A_REVERSE)

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
            ans = _prompt(stdscr, height - 1, "Filter Since (e.g. -7d): ")
            if ans:
                try:
                    since_filter = _parse_since(ans)
                    since_filter_raw = ans
                except ValueError:
                    _status_err(stdscr, height - 1, "Invalid since format. Use e.g. -7d")
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
    stdscr.addstr(y, 0, prompt_text, curses.A_REVERSE)
    
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
    stdscr.addstr(y, 0, f"Error: {text}", curses.A_REVERSE)
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

        stdscr.addstr(0, 0, f"px0 runs TUI - Detail View: {run_id}".center(width)[:width], curses.A_REVERSE)
        
        # Metadata
        lines = [
            f"Workflow ID: {record.get('workflow_id', 'None')}",
            f"Trigger:     {record.get('trigger', 'None')}",
            f"Outcome:     {record.get('outcome', 'None')}",
            f"Duration:    {record.get('duration_seconds', 'n/a')}s",
        ]
        
        # Render prompt
        prompt = extract_rendered_prompt(raw_log)
        if prompt:
            lines.append("Rendered Prompt:")
            lines.append("  " + prompt.replace("\n", "\n  ")[:500] + ("..." if len(prompt) > 500 else ""))
        else:
            lines.append("Rendered Prompt: not available (log retention expired)")

        # Guidelines inlined
        lines.append("Guidelines Inlined:")
        for g in record.get("guidelines_inlined", []):
            lines.append(f"  - {g[0]} @ {g[1][:8] if len(g) > 1 else 'latest'}")

        # Tool calls & timing
        lines.append("Tool Calls & Timings:")
        for tc in record.get("tool_calls", []):
            timing = f" ({tc['elapsed_seconds']}s)" if tc.get("elapsed_seconds") is not None else ""
            lines.append(f"  - {tc.get('tool')}{timing} -> {tc.get('result_summary', '')[:80]}")

        # Render detail pane
        max_y = height - 2
        for idx, line in enumerate(lines[:max_y]):
            try:
                stdscr.addstr(2 + idx, 2, line[:width - 4])
            except curses.error:
                pass

        footer = "[r] rerun | [l] log page | [o] output | [w] why provenance | Esc/q back"
        stdscr.addstr(height - 1, 0, footer[:width], curses.A_REVERSE)

        key = stdscr.getch()
        if key in (27, ord('q'), ord('Q')):
            break

        elif key in (ord('r'), ord('R')):
            # Rerun
            try:
                # Suspend curses
                curses.endwin()
                print(f"Rerunning workflow {record['workflow_id']}...")
                new_run_id = runner.run(home, config, record["workflow_id"], trigger="manual")
                print(f"Spawned rerun with run ID: {new_run_id}")
                print("\nPress any key to resume TUI...")
                sys.stdin.read(1)
            except Exception as e:
                print(f"Rerun failed: {e}")
                print("\nPress any key to resume...")
                sys.stdin.read(1)
            finally:
                # Resume curses
                stdscr = curses.initscr()
                stdscr.refresh()
                # Set run_id to new rerun
                if 'new_run_id' in locals() and new_run_id:
                    run_id = new_run_id

        elif key in (ord('l'), ord('L')):
            # Page full raw log
            curses.endwin()
            try:
                log_path = runs_mod.log_path(config, run_id)
                pager = os.environ.get("PAGER", "less")
                subprocess.run([pager, str(log_path)])
            except Exception as e:
                print(f"Error paging log: {e}")
                print("\nPress any key to resume...")
                sys.stdin.read(1)
            finally:
                stdscr = curses.initscr()
                stdscr.refresh()

        elif key in (ord('o'), ord('O')):
            # Print output
            curses.endwin()
            try:
                print("--- Output Text ---")
                text_out = record.get("output", {}).get("text", "")
                print(text_out)
                print("\nPress any key to resume...")
                sys.stdin.read(1)
            except Exception as e:
                print(f"Error printing output: {e}")
                print("\nPress any key to resume...")
                sys.stdin.read(1)
            finally:
                stdscr = curses.initscr()
                stdscr.refresh()

        elif key in (ord('w'), ord('W')):
            # Prove why
            curses.endwin()
            try:
                print("--- Provenance Chain ---")
                prov_text = provenance.why(home, config, run_id)
                print(prov_text)
                print("\nPress any key to resume...")
                sys.stdin.read(1)
            except Exception as e:
                print(f"Error checking provenance why: {e}")
                print("\nPress any key to resume...")
                sys.stdin.read(1)
            finally:
                stdscr = curses.initscr()
                stdscr.refresh()
