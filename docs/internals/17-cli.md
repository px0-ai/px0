# 17. The CLI layer

Modules: `px0/parser.py`, `px0/cli.py`, `px0/commands.py`, `px0/ui.py`, `px0/completion.py`, `px0/runs_tui.py`, `px0/status.py`

## Entity first, then the verb

```
px0 <group> <verb> [arguments] [options]
```

`px0 workflows new`, `px0 brain search`, `px0 guidelines list`. The only flat commands are the ones acting on the install rather than on anything in the store: `init`, `doctor`, `version`, `update`, `uninstall`, plus `completion`, which emits a shell script.

Two flat commands are flat for a different reason. `status` reports across every group, and `ask` routes a question to whichever of them can answer it. Neither has a single entity to name.

## The parser is its own module

`parser.py` holds the whole argparse tree; `cli.py` holds the handlers. The dependency runs one way -- `cli` imports `parser`, never the reverse -- so `parser.build` is handed the module holding the handlers rather than importing them:

```python
def build_parser() -> argparse.ArgumentParser:
    return parser_mod.build(sys.modules[__name__])
```

Each leaf sets its own `func` default, so a group needs no dispatch table. Group `dest` values are still set, because several handlers serve more than one leaf and switch on it.

`_HelpFormatter` widens the help column to 32 characters and seeds `_action_max_length` to 24. The stdlib default of 24 wraps anything past about nine characters, which most of px0's subcommand names exceed. `_Parser` defaults to that formatter, and because `add_subparsers` takes its `parser_class` from `type(self)`, every subparser at every nesting level inherits it without passing `formatter_class=` at each leaf.

### Flags at the root

`--json` and `--no-color` are declared on the root parser so `px0 --json <cmd>` works. Every subcommand that repeats `--json` uses `default=argparse.SUPPRESS`, so an omitted sub-level flag leaves the root value alone instead of resetting it to `False`.

`--complete` takes `nargs=REMAINDER` and is `help=SUPPRESS`. It is for the generated completion scripts, not for people.

Two run flags are also suppressed. `--late-scheduled-at` and `--trigger` are set by the daemon; they stay out of `--help` and out of completion, because they are internal.

### Help that carries the schema

`px0 config get/set/unset` attach `config.key_help()` as their epilog with `RawDescriptionHelpFormatter`, so the aligned block is not re-wrapped into a paragraph.

`get` omits the choices column: allowed values constrain what you can write and say nothing about reading.

The full listing with descriptions and current values lives in `px0 config list`, because that needs a loaded store and `--help` must render without one.

## The store context

`cli._ctx(require_init, scan)` is the one place a store is opened. It resolves the home, exits with a usable message if there is no store, loads the config, exports the Composio key into the environment, applies the CA bundle, and runs the checkpoint scan.

The scan runs before nearly every command, not just runs. It used to run only inside a run and the daemon's nightly pass, so editing a file and then asking `px0 changes list` about it showed a log without the edit. It compares size and mtime over a few dozen files and hashes only what differs, so it is cheap enough to run unconditionally.

If it raises, `_ctx` swallows it. Bookkeeping must never block the command the user actually ran.

`apply_ca_bundle` runs for every command, not just Composio ones. Before that, `brain add <url>` failed on a TLS-intercepting network while Composio worked.

## Presentation

`ui.py` exists so the CLI has one voice, and two rules shape it.

Subtle by default. Colour marks meaning -- a failure, a value you can act on -- and nothing else. Labels and chrome are dim; values are plain. A screen of px0 output should read as mostly grey with a few deliberate accents, never as a colour test page.

```python
_ACCENT = "208"   # amber -- px0's own voice: prompts, values worth acting on
_OK = "71"        # muted green, not the shouting default
_ERR = "167"      # muted red
_WARN = "179"
_INFO = "110"
_DIM = "245"      # labels, chrome, secondary text
_FAINT = "240"    # rules, the least important thing on screen
```

Plain when not a terminal. Pipe px0 anywhere and every escape sequence disappears, glyphs fall back to ASCII (`[OK]`, `[FAIL]`), and the spinner goes silent. Output stays greppable, so scripts parsing it never see a byte of styling.

`NO_COLOR`, `FORCE_COLOR`, `TERM=dumb`, and `--no-color` are all honoured. `is_tty` is separate from `color_enabled` on purpose: `FORCE_COLOR` should add colour to piped output, but carriage-return redraws only make sense on a terminal, so the spinner gates on the former.

`--json` also disables colour, because `--json` output is data and never gets decorated.

### Stream discipline

Spinners write to stderr; content goes to stdout. `main` line-buffers stdout so the two stay in order:

```python
sys.stdout.reconfigure(line_buffering=True)
```

Without it, piping px0 anywhere reorders content against progress lines, because stdout is block-buffered while stderr is not. `_dump` flushes for the same reason.

### Confirmation

Every destructive verb goes through `_confirm`, so "are you sure" reads the same everywhere and `--yes` means the same thing everywhere. It defaults to no. On a non-terminal stdin it exits with a message naming `--yes` rather than hanging or guessing.

## Error mapping

`main` catches exception classes and maps each to an exit code, listed in [part 1](01-architecture.md).

Two of them are about ergonomics rather than categories.

`KeyboardInterrupt` prints a newline (the spinner has already cleared its line) and reports "interrupted".

`EOFError` means a prompt hit end of input: piped stdin, CI, or a `yes |` that ran out. Interactive commands say what to pass instead of dying on a traceback:

> this command needs an answer and stdin is exhausted
> run it interactively, or pass --yes to accept the defaults

## Completion

64 nodes in the command tree, and most arguments are ids you would otherwise have to remember. Completion is generated from the argparse tree rather than hand-written, so a new verb is completable the day it lands and a removed one stops being offered.

`walk` flattens the tree into `{command path: {verbs, options, dests}}`, skipping `SUPPRESS`-ed actions so internal flags stay out of completion the same way they stay out of `--help`.

The shell scripts are tiny, because they just call back into `px0 --complete`:

```bash
_px0_complete() {
    local IFS=$'\n'
    COMPREPLY=($(px0 --complete "${COMP_WORDS[@]:1}" 2>/dev/null))
}
```

That is what lets the dynamic parts come from the store. `DYNAMIC` maps an argparse dest to a kind of value:

```python
DYNAMIC = {
    "workflow": "workflows",
    "run_id": "runs",
    "key": "config_keys",
    "claim_id": "claims",
}
```

`_values` enumerates one kind and is wrapped in a bare `except Exception: return []`. Completion runs on every tab press, so every branch is cheap and silent -- a broken store must not print an error into the user's prompt.

## The runs browser

`runs_tui.py` is a curses list and detail view over run records: newest first, filterable by workflow, outcome, write activity, and age. The detail view adds the rendered prompt recovered from the raw log, the guideline versions inlined, and per-tool-call timings, with one keystroke each to rerun, page the log, show the output, and trace provenance.

`format_row` is shared with `px0 runs list`, so both render identically. `column_widths` is computed once for a whole batch and passed to every row; without it each row would be formatted in isolation and the columns would jitter.

A row carries markers for facts that would otherwise be invisible in a listing:

```python
marker = "  [write]" if wrote else ""
if r.get("dry_run"):  marker += "  [dry-run]"
if verdict:           marker += f"  [{verdict}]"
```

A rehearsal looked identical to a real run. A run someone judged reads differently from one nobody looked at, and the listing is where you go looking for the bad ones.

## Status

`status.collect` answers "is anything broken" in one command, and stays cheap enough to run constantly: no network, no model call.

Before it existed the answer took three commands: `px0 daemon status` for the scheduler, `px0 runs list --failed` for what went wrong, and `px0 doctor` for whether the install is sound.

It gathers workflows (scheduled, watched, disabled, unparseable), daemon liveness and next fires, recent runs and failures inside `--hours`, runs in flight, pending approvals, unread inbox entries, and the notify policy. Then it folds in the deterministic health pass over a wider window:

```python
HEALTH_DAYS = 14
```

Wider than the failure window on purpose: a workflow that has quietly produced nothing useful for a fortnight is not news in the last day, and it is exactly what this is meant to surface. Only problems are included, and `failing` findings are skipped because failures already have their own line.

The health pass is wrapped in a bare `except`. Status is what a person runs when something is already wrong, so it must not itself fail.

`problems` is the list that decides the exit code, and it includes things that are not faults:

- Scheduled workflows with no daemon running.
- Failed runs, with the command to investigate the first one.
- Failures plus `notify.on_failure = none`, which means a failed run tells you nothing.
- Pending approvals -- not a fault, but the one thing here blocked on the user rather than on px0. A drafted message nobody answers is a message that never goes out.
- Up to three health problems, then a count of the rest.
- Every workflow that fails to parse.

Everything is returned as data, so `--json` is the same information the printed report is drawn from and the exit code can be decided by what is in it.

## Update nudges

`_notify_update` runs after every successful command except `update` itself and anything with `--json`. It is a once-a-day cached check, and it is wrapped so it can never break or delay the command that triggered it.

## Next

[Part 18](18-release.md) covers versions, migrations, and the checks that confirm an install is sound.
