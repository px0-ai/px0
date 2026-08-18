# Phase 6: `px0 runs` TUI

## Status quo this phase changes

`px0/runs.py` and `px0/cli.py`'s `cmd_runs` (`364-405`) already implement every piece of data access spec.md's TUI needs (`list_records`, `read_record`, `read_raw_log`, plus `runner.run` for re-run) -- but only as flat CLI verbs. There is no interactive view: `grep -rn "curses" px0/*.py` finds nothing. `px0 runs` with no subcommand currently fails argparse validation (`runs_sub = sp.add_subparsers(dest="runs_cmd", required=True)`, `px0/cli.py:868`) instead of opening a TUI, contradicting spec.md:587 ("`px0 runs` opens a TUI over run history; every action has a CLI equivalent").

One real data gap underneath the UI gap: spec.md:590 wants the detail view to show "connector timings," but `px0/runner.py`'s `tool_calls.append(...)` (`218-222`) records `tool`, `args`, `is_write`, `stubbed`, `timestamp`, `result_summary` -- no duration. There is nothing to display because nothing is captured. Also, spec.md:590's "the rendered prompt" is not in the JSON run record at all -- the record (`px0/runner.py:283-286, 353-362`) has no `prompt` field; the rendered prompt only exists in the raw log, written by `_tool_call_loop` (`px0/runner.py:194`, `"--- turn 1 PROMPT ---\n{conversation}"`). The TUI's detail view must read both artifacts, not just the JSON record `px0 runs show` already dumps.

## Assumptions (stated explicitly, low-stakes)

1. **Curses, stdlib, no new dependency.** Python's `curses` is stdlib on Linux/macOS, and available in WSL2 (spec.md:803 already treats WSL2-with-systemd as Linux for daemon purposes; the same holds for a terminal UI). Native Windows is out of scope, consistent with every other platform-specific piece of this codebase (`daemon.py`'s platform detection never targets Windows either). This is the "boring, zero-new-dependency" choice per the stated tooling preference.
2. **Exact keybindings**, since spec.md describes *behavior* ("filterable by...", "one keystroke each to...") but not literal keys except the four explicitly named for the detail view (`r`, `l`, `o`, `w`). This phase pins the remaining ones:
   - **List view**: `↑`/`↓` or `j`/`k` move selection, `Enter` opens detail, `/` prompts for a workflow-id filter, `f` cycles the outcome filter (`all → success → failed → all`), `a` toggles write-activity-only, `s` prompts for a `--since`-style filter (e.g. `7d`), `c` clears all filters, `q` quits.
   - **Detail view**: `r` re-run, `l` page the raw log, `o` print the output, `w` open the provenance chain (all four exactly as spec.md:590 names them), `Esc`/`q` back to the list.
3. **"Print the output" suspends curses rather than writing inside the TUI pane.** Curses owns the terminal; `o` calls `curses.endwin()`, prints via the same logic `px0 runs output <run-id>` already uses (`px0/cli.py:386-389`), waits for one keypress, then resumes the curses screen -- rather than rendering the (potentially long) output text inside a fixed-size pane.
4. **Connector timing is wall-clock around the tool call**, captured the same way `duration_seconds` already brackets a whole run (`px0/runner.py:291-293, 361`) -- `time.monotonic()` before/after `_with_retry(config, tools.call, ...)` at `px0/runner.py:214`, not a network-level timing breakdown (nothing in the codebase measures sub-call latency more precisely than this today, and spec.md doesn't ask for more).

## Engineering section

### Dependencies on prior phases

Depends on Phase 1 only for the shared pytest harness. Independent of Phases 2, 3, 4, and 5.

### What already exists (reused, not rebuilt)

- `px0/runs.py`'s `list_records`, `read_record`, `read_raw_log`, `tail_lines` (the last added in Phase 3) -- the TUI is a rendering layer over these, no new data-access functions needed beyond what's listed in Components touched.
- `px0/cli.py`'s existing `rerun` logic (`391-398`) -- the TUI's `r` keystroke calls the same `runner.run(home, config, wf_id, trigger="manual")`.
- `px0/provenance.py`'s `why()` (full file, 30 lines) -- the TUI's `w` keystroke calls it unchanged.
- `px0/cli.py`'s `_parse_since` (`60-65`) -- reused for the TUI's `s` filter prompt, same `"7d"`-style parsing already used by `px0 runs list --since`.

### Components touched

| File | Change |
| --- | --- |
| `px0/runner.py` | `_tool_call_loop` (`167-228`): wrap the `_with_retry` call at line 214 with `time.monotonic()` before/after; add `"elapsed_seconds": round(elapsed, 3)` to the `tool_calls.append(...)` dict at `218-222`. |
| `px0/runs_tui.py` (new) | The TUI itself: `run(home, config)` (curses entry point via `curses.wrapper`), a `ListView` and `DetailView` pair of thin state-holding classes, and pure (non-curses, directly testable) helper functions: `format_row(record) -> str`, `apply_filters(records, workflow, outcome, write_only, since) -> list[dict]`, `extract_rendered_prompt(raw_log_text) -> str` (parses the first `"--- turn 1 PROMPT ---"` block). |
| `px0/cli.py` | `runs_sub = sp.add_subparsers(dest="runs_cmd", required=True)` (`868`) becomes `required=False`; `cmd_runs` (`364-405`): when `args.runs_cmd` is `None`, call `runs_tui.run(home, config)` instead of falling through with no match (today's `required=True` makes this branch unreachable; removing that constraint is the only argparse change needed). |
| `tests/test_runs_tui.py` (new) | Unit tests for the pure helper functions only (`format_row`, `apply_filters`, `extract_rendered_prompt`) -- the curses rendering loop itself is excluded from automated tests, per the Test plan below. |

No new public classes beyond `ListView`/`DetailView`, which hold only display state (selected index, scroll offset, active filters) -- thin, not a new subsystem; well within the sizing guideline.

### Data model

No new persisted data beyond `runner.py`'s `tool_calls[].elapsed_seconds` (a new float field on an existing list-of-dicts already written to `records/<date>/<id>.json`, no schema migration needed since run records are append-only JSON with no fixed schema enforced elsewhere in the codebase -- `runs_mod.read_record` is a plain `json.loads`).

### Key flows

**`px0 runs` (bare, no subcommand):**

1. `cmd_runs` detects `args.runs_cmd is None`, calls `runs_tui.run(home, config)`.
2. `run()` wraps `_main(stdscr, home, config)` in `curses.wrapper` (handles terminal setup/teardown, including on an uncaught exception, so a TUI crash never leaves the user's terminal in a broken state).
3. `_main` loads `runs_mod.list_records(config)` (all records, no filter), enters the list-view loop.

**List view loop:**

1. Renders visible rows via `format_row` (workflow id, trigger, start time, duration, outcome, `[write]` marker when any `tool_calls[].is_write` is true -- same logic already used by the CLI's plain-text listing, `px0/cli.py:375-378`, factored into the shared `format_row` so the CLI and TUI can't drift apart -- `cmd_runs`'s non-JSON `list` branch is refactored to call `format_row` too, in this same phase, since it's the same one-line change either way).
2. On a filter key (`/`, `f`, `a`, `s`, `c`): updates filter state, recomputes the visible list via `apply_filters`, re-renders.
3. On `Enter`: loads the selected record's raw log via `runs_mod.read_raw_log`, pushes the detail view.
4. On `q`: returns, `curses.wrapper` restores the terminal.

**Detail view loop:**

1. Renders: record metadata (id, trigger, start/end, duration, outcome, late flag), `extract_rendered_prompt(raw_log)`, `guidelines_inlined` (path@version pairs, straight from the record), `inputs_resolved`, `tool_calls` (now including `elapsed_seconds`), and `output`/`error`.
2. `r`: calls `runner.run(home, config, record["workflow_id"], trigger="manual")`; on return, reloads the new record and redraws detail view for the *new* run (matching `px0 runs rerun`'s existing behavior of producing a fresh run, `px0/cli.py:397-398` -- the TUI does not silently overwrite the old record, since `runner.run` already always creates a new run id).
3. `l`: suspends curses, pages the full raw log via the same content `px0 runs logs <id>` prints (`px0/cli.py:404`), using the system pager (`$PAGER`, falling back to `less` if set, else printing directly and waiting for a keypress if neither is available) since curses itself is not a good full-text pager and re-inventing one adds unwarranted scope; resumes curses on return.
4. `o`: per Assumption 3, suspends curses, prints `record["output"].get("text", "")` (same as `px0/cli.py:388`), waits for a keypress, resumes.
5. `w`: suspends curses, prints `provenance.why(home, config, record["id"])`'s formatted result (same as `px0 why <run-id>`, `px0/cli.py:630-639`), waits for a keypress, resumes.
6. `Esc`/`q`: pops back to the list view (list state, including filters and scroll position, is preserved).

**`extract_rendered_prompt(raw_log_text)`:**

```python
def extract_rendered_prompt(raw_log_text: str) -> str:
    """Pulls the first turn's rendered prompt out of a run's raw log, which
    interleaves `--- turn N PROMPT ---` / `--- turn N OUTPUT ---` blocks
    (px0/runner.py:194, 196). Returns "" if the log has no such block (e.g.
    the run failed before stage 5, or its raw log has aged out under
    retention)."""
    marker = "--- turn 1 PROMPT ---\n"
    start = raw_log_text.find(marker)
    if start == -1:
        return ""
    start += len(marker)
    end = raw_log_text.find("\n--- turn 1 OUTPUT ---", start)
    return raw_log_text[start:end if end != -1 else None]
```

### Non-functional requirements

- The list view loads all matching records into memory on open and on every filter change (`runs_mod.list_records` already does this for the CLI's `runs list`, `px0/cli.py:371`) -- no pagination/streaming is added, since the existing CLI path has the same property and spec.md states no row-count or latency budget for this view to improve on.
- Curses redraws only on key events (no polling/animation loop), so idle CPU usage is zero between keystrokes.

### Failure modes

| Failure | Covered by test? | Error handling | Visible to caller? |
| --- | --- | --- | --- |
| `apply_filters` with a `since` string that doesn't parse (e.g. malformed `/`-prompt input) | Yes | Reuses `cli._parse_since`'s existing `ValueError` on a bad format; the TUI catches it and shows an inline status-line error instead of crashing, then keeps the previous filter state | Yes, inline |
| `extract_rendered_prompt` on a run whose raw log aged out under retention (`px0/runs.py:132-162`) | Yes | Returns `""`; the detail view shows "prompt not available (log retention expired)" instead of a blank confusing gap | Yes |
| `r` (re-run) on a workflow that was since deleted | No (would require deleting a workflow mid-session; documented as a known gap, same failure `px0 runs rerun` already has via `runner.run` raising `workflow_mod.WorkflowError`) | `RunError`/`WorkflowError` caught, shown as an inline status-line error, detail view stays open on the original record | Yes, inline |
| Terminal too small to render the detail view's fields | No (would require simulating terminal geometry in tests; documented as a known gap) | Curses' own `curses.error` on an out-of-bounds `addstr` is caught around each render call and the TUI prints "terminal too small, resize and press any key" rather than crashing | Yes |

### Test plan

Uses the pytest harness established in Phase 1. Curses itself is excluded from automated testing -- it requires a real (or `curses`-emulated, e.g. via a pty) terminal, which is disproportionate infrastructure for what is fundamentally a thin rendering layer over already-tested data functions. This is a stated, deliberate test-pyramid gap, not an oversight: the pure logic is unit-tested; the rendering loop is manual-QA'd (Definition of done).

| Layer | What | Count |
| --- | --- | --- |
| Unit | `format_row` output for a normal run, a write-flagged run, a failed run | +3 |
| Unit | `apply_filters` by workflow id, by outcome, by write-only, by since, and combined | +5 |
| Unit | `extract_rendered_prompt` on a well-formed log, an empty log, a log with no matching marker | +3 |
| Unit | `runner._tool_call_loop`'s new `elapsed_seconds` field is present and positive on a successful tool call | +1 |
| Integration | `cmd_runs` with no subcommand calls `runs_tui.run` (mocked, asserts it was invoked with the right `home`/`config`, not the full curses loop) | +1 |

### Rollout

No data migration; `elapsed_seconds` is an additive field on new run records only (old records simply lack it, and the detail view shows "n/a" for a tool call missing the field -- `.get("elapsed_seconds")`, not `[...]`, throughout the rendering code). Rollback: revert the commit; `px0 runs` with no subcommand goes back to an argparse error, and old run records with `elapsed_seconds` present are silently ignored by the reverted code (extra JSON keys are always safe, per `runs_mod.read_record`'s plain `json.loads`).

## Product section

**Phase goal:** browsing run history, drilling into what a run actually did, and re-running it are all one interactive session instead of separate `runs list`/`runs show`/`runs rerun` calls with ids copy-pasted between them.

**User story:** the user's Friday digest posted something wrong; they run `px0 runs`, arrow down to it, hit Enter to see exactly which guidelines were inlined and which tool calls fired, hit `w` to see the provenance chain, fix the workflow file, and hit `r` to re-run it -- all without leaving the TUI.

**In scope:**
- List view: newest-first, filterable by workflow/outcome/write-activity/since, write-call marker.
- Detail view: full record plus the rendered prompt pulled from the raw log, with `r`/`l`/`o`/`w` keystroke actions exactly as spec.md:590 names them.
- `tool_calls[].elapsed_seconds`, closing the "connector timings" data gap underneath the detail view.

**Out of scope (deferred, no phase currently planned):**
- A `--replay` flag (spec.md:591 explicitly says this stays absent: "inputs are not cached, so an identical replay is impossible" -- not a gap, a stated design choice already true today).
- Mouse support, resizable split panes, or any curses feature beyond single-keystroke navigation -- spec.md's own description ("one keystroke each") sets the bar this phase meets, not exceeds.

**Acceptance criteria:**
1. `px0 runs` with no other arguments opens the TUI (verified by the mocked integration test; the visual result is confirmed in manual QA).
2. The list view's filters (`/`, `f`, `a`, `s`, `c`) each narrow or reset the visible rows correctly, per the `apply_filters` unit tests.
3. The detail view shows a non-empty rendered prompt for any run whose raw log is still retained, and a clear "not available" message otherwise.
4. `r` in the detail view produces a new run record and refreshes the view to show it.
5. A tool call made after this phase lands shows a non-`None` `elapsed_seconds` in both the JSON run record (`px0 runs show`) and the TUI detail view.

## Definition of done

- [ ] AC1-5 above pass (AC1's visual confirmation via manual QA, noted explicitly).
- [ ] `pytest` green with the new tests.
- [ ] Manual QA: a full session in a real terminal -- open, filter, drill into a run, follow the raw log with `l`, check provenance with `w`, re-run with `r` -- confirms no crashes and correct terminal restoration on quit.
- [ ] `cmd_runs`'s plain-text `list` branch (`px0/cli.py:375-378`) and the TUI's list view render identical row text for the same record (shared `format_row`, verified by a unit test comparing both call sites).
