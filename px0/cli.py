"""px0's CLI surface. Argument parsing and interactive glue live here;
every subcommand delegates to the module that actually does the work."""

import argparse
import copy
import dataclasses
import json
import os
import re
import shlex
import shutil
import textwrap
import signal
import subprocess
import sys
from datetime import datetime
from pathlib import Path
from typing import NamedTuple

from px0 import (
    analysis as analysis_mod,
    approvals as approvals_mod,
    ask as ask_mod,
    authoring,
    commands as commands_mod,
    catalogue as catalogue_mod,
    completion as completion_mod,
    builder as builder_mod,
    claims,
    config as config_mod,
    connect as connect_mod,
    credentials as creds_mod,
    daemon as daemon_mod,
    guidelines as guidelines_mod,
    improve as improve_mod,
    inbox as inbox_mod,
    replay as replay_mod,
    localtools,
    memory as memory_mod,
    mcp as mcp_mod,
    notify as notify_mod,
    status as status_mod,
    doctor as doctor_mod,
    harness,
    brain as brain_mod,
    paths,
    provenance,
    retrieval,
    runner,
    runs as runs_mod,
    store as store_mod,
    templates as templates_mod,
    tools,
    parser as parser_mod,
    ui,
    update as update_mod,
    versioning,
    workflow as workflow_mod,
)

EXIT_USER_ERROR = 1
EXIT_CONNECTOR_ERROR = 2
EXIT_MODEL_ERROR = 3
EXIT_INTEGRITY_ERROR = 4


def _ctx(require_init: bool = True, scan: bool = True) -> tuple[Path, dict]:
    """Resolves the store home and loads its config for a subcommand.

    Exits the process with EXIT_USER_ERROR if the store hasn't been
    initialized and require_init is True.

    Also captures hand edits as versions before the command reads anything
    (spec.md: "before any command that reads store content"). It used to run
    only inside a workflow run and the daemon's nightly pass, so editing a file
    and then asking `px0 changes list` about it showed a log without the edit.
    The scan compares size and mtime over the few dozen versioned files and
    hashes only what differs, so it is cheap enough to run unconditionally.
    """
    home = paths.store_home()
    if require_init and not store_mod.is_initialized(home):
        ui.err(f"no px0 store at {home}")
        ui.hint("create one with:")
        ui.command("px0 init")
        sys.exit(EXIT_USER_ERROR)
    config = config_mod.load(paths.config_path(home))

    # Load Composio key from config at startup when any command is loaded
    composio_api_key = config.get("connectors", {}).get("composio_api_key")
    if composio_api_key:
        os.environ["COMPOSIO_API_KEY"] = composio_api_key

    # Every outbound HTTPS call gets the stored bundle, not just the Composio
    # ones: on a TLS-intercepting network `brain add <url>` failed while
    # Composio worked, because only the Composio paths applied it.
    connect_mod.apply_ca_bundle(home)

    if scan and store_mod.is_initialized(home):
        try:
            claims.scan_and_process(home)
        except Exception:
            # Never let bookkeeping block the command the user actually ran;
            # `px0 doctor` reports a wedged version store.
            pass

    return home, config


def _parse_since(text: str) -> datetime:
    """Parses a `--since` value like "7d" into an absolute datetime."""
    return runs_mod.parse_since(text)


def _dump(args: argparse.Namespace, data) -> None:
    """Prints data to stdout as indented JSON, coercing non-JSON-serializable values via str().

    Flushed, because spinners write to stderr and block-buffered stdout would
    let the two interleave out of order when piped.
    """
    print(json.dumps(data, indent=2, default=str), flush=True)


def _confirm(question: str, assume_yes: bool = False) -> bool:
    """Asks a yes/no question, defaulting to no.

    Every destructive verb goes through here, so "are you sure" reads the same
    everywhere and `--yes` means the same thing everywhere.
    """
    if assume_yes:
        return True
    if not sys.stdin.isatty():
        ui.err("this needs a confirmation and stdin is not a terminal")
        ui.hint("pass --yes to proceed without being asked")
        sys.exit(EXIT_USER_ERROR)
    answer = ui.prompt(f"{question} [y/N] ").strip().lower()
    return answer in ("y", "yes")


def _open_in_editor(path: Path) -> bool:
    """Opens a file in $VISUAL, $EDITOR, or a sensible fallback.

    Returns False when there is no editor to open and no terminal to run one
    in, so the caller can say where the file is instead of failing.
    """
    editor = os.environ.get("VISUAL") or os.environ.get("EDITOR")
    if not editor:
        for candidate in ("nano", "vim", "vi"):
            if shutil.which(candidate):
                editor = candidate
                break
    if not editor or not sys.stdin.isatty():
        return False
    try:
        subprocess.call(shlex.split(editor) + [str(path)])
    except OSError as e:
        ui.err(f"could not run {editor}", str(e))
        return False
    return True


# --- init / new / run / ask ---------------------------------------------

def _mask_key(key: str) -> str:
    """Returns a masked version of an API key (e.g. 'abcd...1234' or '****')."""
    if not key:
        return ""
    if len(key) <= 8:
        return "*" * len(key)
    return f"{key[:4]}...{key[-4:]}"


def cmd_init(args: argparse.Namespace) -> None:
    """Handles `px0 init`: scaffolds a new store and prints suggested next commands."""
    home = Path(args.dir).expanduser() if args.dir else paths.store_home()
    harness_cmd = harness.KNOWN_HARNESSES[args.harness] if args.harness else None

    created = store_mod.init(home, harness_cmd=harness_cmd)

    composio_key = args.composio_key
    cfg = config_mod.load(paths.config_path(home))
    existing_key = cfg.get("connectors", {}).get("composio_api_key") or os.environ.get("COMPOSIO_API_KEY")

    if composio_key is None:
        ui.hint("needs \"read all\" and \"write all\" privileges on Composio -- workflows "
                "authorize individual toolkits later, but the key itself must be able to grant them")

    while True:
        if composio_key is None:
            if existing_key:
                label = f"Composio API key {ui.dim(f'[{_mask_key(existing_key)}, Enter to keep]')}: "
            else:
                label = "Composio API key: "
            try:
                user_input = ui.prompt(label)
            except EOFError:
                # Non-interactive stdin (install.sh's `curl | sh`, CI, cron). The store
                # is already built; Composio is the only thing left, and it can be set
                # up later, so finish cleanly instead of dying on the prompt.
                print(file=sys.stderr)
                ui.warn("no terminal to prompt on; skipping Composio setup")
                ui.hint("set it up later with:")
                ui.command("px0 config composio <key>")
                break
            if not user_input:
                if existing_key:
                    break
                ui.warn("a Composio API key is required", "enter one, or Ctrl-C to abort")
                continue
            composio_key = user_input

        if not composio_key:
            ui.warn("a Composio API key is required", "enter one, or Ctrl-C to abort")
            composio_key = None
            continue

        try:
            with ui.spinner("Verifying Composio API key"):
                result = connect_mod.setup_composio(home, composio_key)
            created.append("composio credentials")
            if result.get("ca_bundle"):
                ui.info("TLS is intercepted on this network",
                        f"verifying against {result['ca_bundle']}")
            break
        except connect_mod.ComposioUnreachable as e:
            # The key was never judged, so re-prompting would just loop. Bail out and
            # let the user fix connectivity, then re-run.
            ui.err(str(e).strip())
            sys.exit(EXIT_USER_ERROR)
        except ValueError as e:
            ui.err(str(e).strip())
            composio_key = None  # clear it so we prompt again on the next iteration

    ui.heading(f"initialized {home}")
    for line in created:
        ui.ok(line)

    # Most of these folders are empty right after init -- starter content only
    # exists once you write a workflow or read something into the brain -- so
    # this is the one place that tells you what each is for before you go
    # looking. tools/ is deliberately left off: it is for a TOML file you
    # write yourself, not somewhere to look right after init.
    folders = [
        ("workflows", "one Markdown file per workflow px0 runs"),
        ("guidelines", "claims px0 follows, one file per topic"),
        ("memory", "what px0 knows about you, one file per fact"),
        ("brain", "what you've read and kept"),
        ("output", "what runs produce"),
    ]
    width = max(len(name) for name, _ in folders) + 1
    ui.hint(f"inside {home}:")
    for name, desc in folders:
        ui.kv(name, desc, width=width)

    ui.hint("try next:")
    ui.command('px0 workflows new')
    # Offered beside it because a fresh store has no workflows, and `ask` is
    # the one thing that already works on an empty one.
    ui.command('px0 ask "what can you do?"')

    # Surfaced here because a fresh store is exactly when someone who already
    # keeps notes somewhere would want to know they need not move them.
    ui.hint("already keep notes in Obsidian, Logseq, or any folder of Markdown?")
    ui.command("px0 config set brain.path ~/path/to/your/vault")


def _clarify_loop(config: dict, description: str, skip: bool) -> list[tuple[str, str]]:
    """Asks the model what's ambiguous and the user to resolve it, until nothing
    is left to ask (or the user stops answering).

    Returns the question/answer pairs, which every later pass is given so the
    plan reflects the answers rather than re-guessing them. A blank answer skips
    one question; an empty round ends the loop, because pressing Enter through
    an interrogation should not block the build.
    """
    qa: list[tuple[str, str]] = []
    if skip:
        return qa

    for round_no in range(builder_mod.MAX_CLARIFY_ROUNDS):
        with ui.spinner("Checking the request for gaps"):
            questions = builder_mod.clarify(config, description, qa)
        if not questions:
            if round_no == 0:
                ui.ok("the request is clear", "nothing to clarify")
            break

        ui.heading("a few questions" if round_no == 0 else "follow-ups")
        answered = 0
        for question in questions:
            try:
                answer = ui.prompt(f"{question}\n  ", color=_FOLLOWUP)
            except EOFError:
                return qa
            if answer:
                qa.append((question, answer))
                answered += 1
            else:
                ui.info("skipped", stream=sys.stdout)
        if not answered:
            break  # the user is done answering; build with what we have
    return qa


# The one question px0 can ask without help: with an empty transcript there is
# nothing for the model to reason about, so spending a call to have it ask
# "what do you want?" only adds latency.
_OPENING_QUESTION = "What do you want px0 to do for you?"

# The opening question of an interview gets `ui.prompt`'s own accent -- it is
# the one thing every build starts from. Every question after it, here and in
# `_clarify_loop`, is a follow-up and gets this quieter blue instead, so an
# eight-question interview doesn't read as eight equally loud demands.
_FOLLOWUP = "110"


def _intake_loop(config: dict) -> str:
    """Interviews the user into a workflow request.

    `px0 workflows new` always opens this. px0 asks for one thing
    at a time until every field a workflow file has to pin down is settled --
    the job, what it reads, where the result goes, when it runs, and what makes
    the output right -- then writes the request back for approval.

    The loop is the model's to drive: it sees the transcript and decides what is
    still missing, so answering "the razorpay/api repo, every Friday" in one
    breath skips the two questions that would have asked for those separately.
    A blank answer ends it early and the request is written from what there is,
    because the way out of an interview should always be Enter.
    """
    transcript: list[tuple[str, str]] = []
    question = _OPENING_QUESTION
    wrap_up = False

    for _ in range(builder_mod.MAX_INTAKE_ROUNDS):
        color = None if question == _OPENING_QUESTION else _FOLLOWUP
        try:
            answer = ui.prompt(f"{question}\n  ", color=color)
        except EOFError:
            print(file=sys.stderr)
            answer = ""
        if not answer:
            if not transcript:
                ui.err("nothing to build")
                ui.hint("run `px0 workflows new` again when you know what you want")
                sys.exit(EXIT_USER_ERROR)
            wrap_up = True
        else:
            transcript.append((question, answer))

        try:
            with ui.spinner("Working out what else it needs"):
                step = builder_mod.intake(config, transcript, wrap_up=wrap_up)
        except (builder_mod.BuilderError, harness.HarnessError) as e:
            # The interview is the only way in, so a failed turn cannot fall
            # through to a build with nothing.
            ui.err("could not continue the interview", str(e).strip())
            ui.hint("try `px0 workflows new` again")
            sys.exit(EXIT_MODEL_ERROR)

        if "description" in step:
            return _confirm_request(config, step["description"], transcript)
        question = step["question"]

    # Out of rounds with the model still asking. Settle for what was gathered
    # rather than asking a ninth question.
    try:
        with ui.spinner("Writing up the request"):
            step = builder_mod.intake(config, transcript, wrap_up=True)
    except (builder_mod.BuilderError, harness.HarnessError) as e:
        ui.err("could not write up the request", str(e).strip())
        sys.exit(EXIT_MODEL_ERROR)
    return _confirm_request(config, step["description"], transcript)


def _confirm_request(config: dict, description: str,
                     transcript: list[tuple[str, str]]) -> str:
    """Shows the request the interview produced and lets the user fix it.

    Printed before the build spends a single planning call, because this
    paragraph is what every later pass reads -- and the one thing the user is
    better placed than the model to judge is whether it says what they meant.
    """
    while True:
        ui.remark("Here's what I got from your request -- let me know if it looks right.",
                 color=_FOLLOWUP)
        print()
        print(description, flush=True)
        choice = ui.prompt("Build this? [Y/edit/n] ").lower()
        if choice in ("n", "no"):
            ui.info("cancelled")
            sys.exit(0)
        if choice not in ("e", "edit"):
            return description
        note = ui.prompt("What should change (leave it blank to keep it as is):\n  ", color=_FOLLOWUP)
        if not note:
            continue
        try:
            with ui.spinner("Working that change in"):
                description = builder_mod.revise_request(config, description, note, transcript)
        except (builder_mod.BuilderError, harness.HarnessError) as e:
            ui.err("could not revise the request", str(e).strip())


def _print_tool(spec_or_tool, index: int) -> None:
    """One tool being proposed: number, access, and id on their own line, with
    the full description wrapped on an indented line below it.

    Two lines rather than one truncated one -- the description is what tells
    the user whether a tool actually does what its name implies, and cutting
    it off mid-word defeats that.
    """
    is_destructive = getattr(spec_or_tool, "is_destructive", False)
    if is_destructive:
        access_plain = "destructive"
        access = ui.paint(access_plain, "167")
    elif spec_or_tool.is_write:
        access_plain = "write"
        access = ui.paint(access_plain, "179")  # yellow -- can change things outside px0
    else:
        access_plain = "read "
        access = ui.paint(access_plain, "110")  # blue -- looks only
    prefix_plain = f"  {index}. {access_plain}  "
    print(f"  {ui.accent(f'{index}.')} {access}  {spec_or_tool.id}")
    desc = getattr(spec_or_tool, "description", "")
    if desc:
        cols = shutil.get_terminal_size((80, 24)).columns
        indent = " " * len(prefix_plain)
        wrapped = textwrap.fill(desc, width=max(cols - len(prefix_plain) - 1, 20),
                                initial_indent=indent, subsequent_indent=indent)
        print(ui.dim(wrapped))


def _discover_tools(home: Path, config: dict, description: str,
                    qa: list[tuple[str, str]]) -> list:
    """Searches Composio's catalogue for the task and returns the chosen tools.

    Returns [] when the task needs no external service, which is a valid answer
    -- plenty of useful workflows only summarize their own input.
    """
    with ui.spinner("Working out what capabilities this needs"):
        queries = builder_mod.propose_queries(config, description, qa)
    if not queries:
        ui.ok("no external service needed", "this runs on its input alone")
        return []

    for query in queries:
        ui.bullet(ui.dim(builder_mod.describe_query(query)))

    try:
        with ui.spinner(f"Searching Composio's catalogue ({len(queries)} queries)"):
            candidates = builder_mod.search_candidates(home, queries)
    except catalogue_mod.CatalogueError as e:
        ui.err("catalogue search failed", str(e))
        ui.hint("px0 cannot pick tools without it; fix the above and retry")
        sys.exit(EXIT_CONNECTOR_ERROR)

    if not candidates:
        ui.warn("no catalogue tool matched", "; ".join(queries))
        return []

    with ui.spinner(f"Choosing from {len(candidates)} candidates"):
        selected = builder_mod.select_tools(config, description, qa, candidates)
    if not selected:
        ui.warn("none of the candidates fit the request")
        return []
    return selected


def _confirm_tools(home: Path, selected: list, assume_yes: bool) -> list:
    """Shows the chosen tools and their access, and gets explicit agreement.

    This is the gate before anything is authorized or written: the model chose
    these, and choosing a write tool the request didn't ask for is exactly the
    mistake a human should catch here.
    """
    ui.say(f"Tools selected ({len(selected)}).")
    print()
    for i, tool in enumerate(selected, 1):
        # numbered so the drop-list below can refer to them
        _print_tool(tool, i)

    destructive = [t for t in selected if t.is_destructive]
    if destructive:
        ui.warn("destructive tools proposed",
                ", ".join(t.slug for t in destructive), stream=sys.stdout)

    if assume_yes:
        return selected

    ui.hint("Enter accepts all; list numbers to drop (e.g. 2,3); n aborts")
    answer = ui.prompt("keep all? [Y/n/#,#,...] ").lower()

    if answer in ("n", "no"):
        ui.info("cancelled")
        sys.exit(0)
    if not answer:
        return selected

    drop = {int(p) for p in re.findall(r"\d+", answer)}
    kept = [t for i, t in enumerate(selected, 1) if i not in drop]
    if not kept:
        ui.err("every tool was dropped; nothing left to build with")
        sys.exit(EXIT_USER_ERROR)
    ui.ok(f"kept {len(kept)} of {len(selected)}")
    return kept


class _AuthOutcome(NamedTuple):
    """What an authorization pass ended up with, split by whether it can still finish.

    `waiting` is fine to build on -- a consent link is open, or the user chose to
    deal with it later, and the workflow file is valid either way. `blocked` is
    not: px0 asked Composio to start the flow and Composio refused, so no amount
    of clicking finishes it, and a workflow written against those toolkits could
    never run.
    """
    waiting: list[str]
    blocked: list[tuple[str, str]]

    def __add__(self, other):
        return _AuthOutcome(self.waiting + other.waiting, self.blocked + other.blocked)


def _authorize_toolkits(home: Path, toolkits: set[str], assume_yes: bool) -> _AuthOutcome:
    """Authorizes each toolkit that isn't authorized yet, asking first.

    A pending consent never aborts: the workflow file is valid either way, and
    making the user re-run `px0 workflows new` would repeat the clarify, search, selection,
    and planning passes just to reach the same file. A toolkit Composio *refuses*
    to start is reported as blocked instead -- see `_abort_if_blocked`.
    """
    pending = []
    with ui.spinner("Checking authorizations"):
        for toolkit in sorted(toolkits):
            status = connect_mod.connected_account_status(home, toolkit)
            if status != "ACTIVE":
                pending.append((toolkit, status))

    if not pending:
        if toolkits:
            ui.ok("already authorized", ", ".join(sorted(toolkits)))
        return _AuthOutcome([], [])

    ui.heading(f"authorization needed ({len(pending)})")
    for toolkit, status in pending:
        ui.warn(toolkit, _AUTH_STATE.get(status, status.lower()), stream=sys.stdout)

    if not assume_yes:
        names = ", ".join(t for t, _ in pending)
        if ui.prompt(f"Start authorization for {names}? [Y/n] ").lower() in ("n", "no"):
            ui.info("skipped; the first run that needs one will offer a link")
            return _AuthOutcome([t for t, _ in pending], [])

    waiting, blocked = [], []
    for toolkit, _ in pending:
        try:
            with ui.spinner(f"Preparing {toolkit} authorization"):
                res = connect_mod.connect_composio_app(home, toolkit)
        except ValueError as e:
            blocked.append((toolkit, str(e).strip()))
            continue
        ui.step(toolkit, "open this and complete the consent:", stream=sys.stdout)
        ui.command(res["redirect_url"])
        waiting.append(toolkit)

    # All the links are up before asking -- rechecking after one confirmation
    # is cheaper than interrupting the user once per toolkit, and it lets an
    # app the user actually finished drop out of `waiting` instead of nagging
    # about it again at the end.
    if waiting and not assume_yes:
        ui.prompt("Press Enter once you've connected all of the above: ")
        with ui.spinner("Rechecking authorizations"):
            still_pending = [t for t in waiting
                             if connect_mod.connected_account_status(home, t) != "ACTIVE"]
        if still_pending:
            ui.warn("still not connected", ", ".join(still_pending), stream=sys.stdout)
        else:
            ui.ok("all connected", ", ".join(waiting))
        waiting = still_pending

    return _AuthOutcome(waiting, blocked)


def _abort_if_blocked(outcome: _AuthOutcome) -> None:
    """Stops `px0 workflows new` when a toolkit cannot be authorized at all.

    Continuing would mean planning, prompting for, and writing a workflow whose
    first run is guaranteed to fail on the same refusal -- so this exits rather
    than folding the failure into the pending list, where it read as "finish this
    in your browser" for something no browser step can finish.
    """
    if not outcome.blocked:
        return
    ui.heading(f"authorization failed ({len(outcome.blocked)})")
    for toolkit, error in outcome.blocked:
        ui.err(toolkit, error, stream=sys.stdout)
    ui.hint("nothing was written -- resolve the above, then re-run this command")
    sys.exit(EXIT_CONNECTOR_ERROR)


def _select_guidelines(home: Path, config: dict, description: str, plan) -> list[str]:
    """Attaches the guidelines this workflow's output is judged against.

    A model reads each guideline's frontmatter description and picks the ones
    that govern this workflow, which is the same question a person would answer
    off the listing. Skipped without a spinner when the store has none, so an
    empty store does not narrate a decision there was nothing to decide.
    """
    if not guidelines_mod.attachable(home):
        return []
    with ui.spinner("Checking which guidelines apply"):
        return builder_mod.select_guidelines(home, config, description, plan)


def _author_guidelines(home: Path, config: dict, description: str, plan,
                       attached: list[str], assume_yes: bool) -> list[str]:
    """Writes the guidelines this workflow depends on that the store lacks.

    There is no `px0 guidelines new`: this is where a guideline comes from. The
    build decides whether the workflow leans on a durable convention, drafts it,
    and links it -- so the standard is written once, here, instead of being
    restated in the body of every workflow that needs it.

    Nothing lands unseen. Each draft is printed with the path it would take, and
    the user keeps it, redraws it, or skips it. Under --yes there is nobody to
    show it to, so the pass is skipped rather than writing a file the user never
    had a chance to read.

    Returns the relative paths actually created, for the workflow's `guidelines:`.
    """
    if assume_yes:
        return []
    try:
        with ui.spinner("Checking for standards worth writing down"):
            proposals = builder_mod.propose_guidelines(config, description, plan, attached)
    except (builder_mod.BuilderError, harness.HarnessError) as e:
        # A workflow is perfectly valid without this; never fail the build over it.
        ui.warn("could not check for new guidelines", str(e).strip(), stream=sys.stdout)
        return []

    created = []
    for proposal in proposals:
        path = _write_one_guideline(home, config, proposal, description, plan)
        if path:
            created.append(path)
    return created


def _write_one_guideline(home: Path, config: dict, proposal, description: str,
                         plan) -> str | None:
    """Drafts, shows, and confirms one guideline. Returns its path, or None.

    Loops on "again" rather than making the user live with a first draft: the
    file is about to be inlined into every run of this workflow, so it is worth
    another pass here instead of an edit later.
    """
    while True:
        try:
            with ui.spinner(f"Drafting {proposal.title}"):
                content = builder_mod.draft_guideline(config, proposal, description, plan)
        except (builder_mod.BuilderError, harness.HarnessError) as e:
            ui.err("could not draft it", str(e).strip(), stream=sys.stdout)
            return None

        ui.heading(f"guideline: {proposal.title}")
        if proposal.why:
            ui.bullet(ui.dim(proposal.why))
        # The description is shown because it is not decoration: it is the line
        # every later `px0 workflows new` matches this file against, so a user
        # who disagrees with it should see it before the file lands.
        if proposal.description:
            ui.kv("applies when", proposal.description)
        ui.info("would be saved as", f"guidelines/{proposal.path}", stream=sys.stdout)
        print()
        print(content, flush=True)
        choice = ui.prompt("Keep it? [Y/again/n] ").lower()
        if choice in ("a", "again"):
            continue
        if choice in ("n", "no"):
            ui.info("skipped", stream=sys.stdout)
            return None

        dest = builder_mod.save_guideline(home, proposal.path, content,
                                          description=proposal.description)
        ui.ok("wrote", str(dest))
        ui.hint(f"reword it any time with `px0 guidelines edit {Path(proposal.path).stem}`")
        return proposal.path


def cmd_new(args: argparse.Namespace) -> None:
    """Handles `px0 workflows new`: interviews the user and builds a workflow
    from what they say.

    px0 asks -- which is the honest default, because "what should this read,
    and when does it run" are questions the user has to answer either way, and
    a blank prompt is a worse place to answer them than a question is.
    """
    home, config = _ctx()
    if not sys.stdin.isatty():
        # No keystrokes to read on a pipe, and nobody to ask on the other end.
        ui.err("workflows new needs a terminal to interview you")
        sys.exit(EXIT_USER_ERROR)

    _build_workflow(home, config, _intake_loop(config), args,
                    existing_id=None, already_clarified=True)


def cmd_workflows_edit(args: argparse.Namespace) -> None:
    """Handles `px0 workflows edit`: shows the original request, takes a new one,
    and rebuilds the workflow in place.

    A rebuild rather than a text edit. The file is generated -- its tools, inputs,
    and guideline list all follow from the request -- so editing the request and
    regenerating keeps those consistent, where hand-editing the body would leave
    them describing a workflow that no longer exists. The old version stays in
    the store's history either way, so `px0 changes revert` undoes this.
    """
    home, config = _ctx()
    workflow_id = args.workflow or _pick_workflow(home, for_stdin=False, verb="edit")

    workflows = workflow_mod.load_all(home)
    if workflow_id not in workflows:
        ui.err(f"no workflow {workflow_id!r}")
        ui.hint("list them with: px0 workflows list")
        sys.exit(EXIT_USER_ERROR)
    wf = workflows[workflow_id]

    ui.heading(f"editing {workflow_id}")
    # Workflows written before `request:` existed have nothing verbatim to show;
    # the model's description is the closest thing on file, and saying which one
    # the user is looking at matters more than hiding the difference.
    if wf.request:
        # Short requests sit on the label line; a multi-sentence one gets its own
        # indented block, because wrapping it after a label makes it hard to read.
        if len(wf.request) <= 68 and "\n" not in wf.request:
            ui.kv("original request", wf.request)
        else:
            print(f"  {ui.dim('original request:')}", flush=True)
            for line in wf.request.splitlines():
                print(f"    {line}", flush=True)
    else:
        ui.kv("description", wf.description)
        ui.hint("this workflow predates stored requests, so that is px0's own "
                "restatement rather than your wording")
    if wf.tools:
        ui.kv("tools", ", ".join(wf.tools))
    if wf.guidelines:
        ui.kv("guidelines", ", ".join(wf.guidelines))

    print(flush=True)
    description = ui.prompt("New instructions (blank to keep the current ones):\n  ").strip()
    if not description:
        ui.info("unchanged")
        return

    _build_workflow(home, config, description, args, existing_id=workflow_id)


def _build_workflow(home: Path, config: dict, description: str,
                    args: argparse.Namespace, existing_id: str | None,
                    already_clarified: bool = False) -> None:
    """The build pipeline behind both `workflows new` and `workflows edit`.

    Clarifies the request, discovers and confirms tools, authorizes them, plans,
    checks feasibility, offers to author missing guidelines, and writes the file.

    Authorization runs *before* planning. The plan can only draw on the tools the
    user just confirmed, so a toolkit that cannot be authorized makes the workflow
    unbuildable -- finding that out first avoids spending a planning call and
    printing a plan the user is then asked to commit to anyway.

    `existing_id` is set when rebuilding: it pins the id instead of prompting, so
    an edit replaces the workflow rather than forking a near-duplicate under a
    slightly different name.

    `already_clarified` is set when the description came out of the intake
    interview, which has just settled the same questions the clarify pass asks.
    Running both would put the user through the interrogation twice.
    """
    assume_yes = getattr(args, "yes", False)

    # Before spending a build: does this already exist? Nothing looked, so a
    # store could accumulate three near-identical digests, each on its own
    # schedule, each costing a run.
    if existing_id is None:
        near = builder_mod.similar_workflows(home, description)
        if near:
            ui.warn("you may already have this",
                    ", ".join(f"{wf_id}" for wf_id, _ in near))
            for wf_id, _score in near:
                wf = workflow_mod.load_all(home).get(wf_id)
                if wf:
                    ui.field(wf_id, wf.description or wf.request, width=0)
            print(flush=True)
            ui.hint("editing the one you have keeps its history and its schedule:")
            ui.command(f"px0 workflows edit {near[0][0]}")
            if not _confirm("build a new one anyway?", assume_yes):
                return

    try:
        qa = _clarify_loop(config, description,
                           skip=assume_yes or already_clarified
                           or getattr(args, "no_clarify", False))

        selected = [] if getattr(args, "no_discover", False) else \
            _discover_tools(home, config, description, qa)
        if selected:
            selected = _confirm_tools(home, selected, assume_yes)
            # Remember them before planning: the plan, its validation, and every
            # later run all resolve these ids out of the store's catalogue cache.
            catalogue_mod.remember(home, selected)

        # Every provider the plan could possibly reach is a provider of a tool
        # confirmed above, so this pass covers the discovered case in full.
        # `_confirm_tools` has already shown which of them are write tools.
        pre_authorized = {t.toolkit for t in selected}
        auth = _authorize_toolkits(home, pre_authorized, assume_yes)
        _abort_if_blocked(auth)

        with ui.spinner("Writing the workflow plan"):
            plan = builder_mod.generate_plan(config, description, qa, selected)

        # Asked for now so the preview below can show it, and reused for the
        # actual id prompt further down instead of recomputing it. Skipped
        # whenever an id is already pinned -- a rebuild's existing_id, or an
        # explicit --id -- since neither needs a name invented for it.
        if existing_id:
            default_id = existing_id
        elif getattr(args, "id", None):
            default_id = args.id
        else:
            with ui.spinner("Naming the workflow"):
                default_id = builder_mod.generate_slug(config, plan.description)
    except (builder_mod.BuilderError, harness.HarnessError) as e:
        ui.err(str(e))
        sys.exit(EXIT_MODEL_ERROR)

    ui.say("Here's the plan -- let me know what you think.")
    print()
    # id and guidelines aren't picked yet, so this previews against a
    # placeholder id -- everything the plan itself determined, in the same
    # shape the saved file will take.
    preview = builder_mod.render_workflow_file(
        existing_id or default_id, plan, guidelines=[], request=description)
    ui.render_workflow_markdown(preview)

    issues = builder_mod.check_feasibility(plan, home)
    if issues:
        ui.heading("feasibility")
        for i in issues:
            ui.err(i, stream=sys.stdout)
        ui.hint("cannot proceed until these are resolved")
        sys.exit(EXIT_USER_ERROR)

    # Whatever the plan needs that discovery didn't cover: px0's curated tools,
    # and everything under --no-discover, where there was nothing to pre-authorize.
    residue = builder_mod.required_connections(plan, home) - pre_authorized
    if residue:
        rest = _authorize_toolkits(home, residue, assume_yes)
        _abort_if_blocked(rest)
        auth = auth + rest
    waiting = auth.waiting

    verb = "Rebuild" if existing_id else "Generate"
    if not assume_yes:
        if ui.prompt(f"{verb} this workflow? [Y/n] ").lower() in ("n", "no"):
            ui.info("cancelled")
            return

    if existing_id:
        workflow_id = existing_id
    else:
        if getattr(args, "id", None):
            workflow_id = args.id
        elif assume_yes:
            workflow_id = default_id
        else:
            workflow_id = ui.prompt(f"workflow id {ui.dim(f'[{default_id}]')}: ") or default_id

        # A hand-typed id or a harness slug can both collide with a workflow
        # already on disk; `save_workflow` overwrites without asking, so this
        # is the last chance to catch it before that file is gone.
        while workflow_id in workflow_mod.load_all(home):
            ui.warn(f"{workflow_id} already exists")
            if assume_yes:
                break
            choice = ui.prompt("overwrite it, or generate a new id? [o/N] ").strip().lower()
            if choice in ("o", "overwrite"):
                break
            with ui.spinner("Naming the workflow"):
                workflow_id = builder_mod.generate_slug(config, plan.description)
            ui.info("new id", workflow_id)

    guidelines = _select_guidelines(home, config, description, plan)
    # After the commit to write, not before: nobody should be asked to author
    # a convention for a workflow they are about to cancel.
    guidelines += _author_guidelines(home, config, description, plan, guidelines, assume_yes)

    content = builder_mod.render_workflow_file(workflow_id, plan, guidelines, description)
    dest = builder_mod.save_workflow(home, workflow_id, content)

    ui.heading(f"{'updated' if existing_id else 'created'} {ui.accent(workflow_id)}")
    # Every path as `~/.px0/...`, the same way a run reports what it wrote.
    rows = [("workflow", paths.display(dest))]
    if plan.trigger.get("schedule"):
        rows.append(("schedule", plan.trigger["schedule"]))
    if guidelines:
        rows.append(("guidelines",
                     [paths.display(paths.guidelines_dir(home) / g) for g in guidelines]))
    if plan.output.get("target") == "file" and plan.output.get("path"):
        # Where a run will actually put it, not the fragment the plan wrote: a run
        # files `output.path` under `output/`, which the frontmatter leaves
        # implicit. Placeholders stay unrendered -- the file does not exist yet,
        # and today's date would be the wrong name tomorrow.
        rows.append(("output",
                     paths.display(runner.output_destination(home, plan.output["path"]))))
    if selected:
        rows.append(("tools", [t.id for t in selected]))
    # Bullets rather than ticks, as in a run's own block: these are the workflow's
    # fields, and the one thing that happened -- it was created -- is the heading.
    width = max(len(label) for label, _ in rows)
    for label, detail in rows:
        ui.field(label, detail, width=width)

    if guidelines:
        ui.hint("each is inlined verbatim into every run of this workflow")
    if waiting:
        ui.warn("authorization pending", ", ".join(waiting),
                stream=sys.stdout)
        ui.hint("finish the consent in your browser, then:")
    else:
        ui.hint("try next:")
    ui.command(f"px0 workflows run {workflow_id} --dry-run")


def _pick_workflow(home: Path, for_stdin: bool, verb: str = "run") -> str:
    """Resolves the workflow to run when none was named, by asking.

    Refuses when --stdin is in play: the picker reads keystrokes from the same
    stdin the workflow's input is coming from, so one would consume the other.
    """
    workflows = sorted(workflow_mod.load_all(home).items())
    if not workflows:
        ui.err("no workflows yet")
        ui.hint("describe one with `px0 workflows new`")
        sys.exit(EXIT_USER_ERROR)
    if for_stdin:
        ui.err("--stdin needs an explicit workflow id",
               "the picker would read the keystrokes from that same stream")
        ui.hint("list them with: px0 workflows list")
        sys.exit(EXIT_USER_ERROR)

    choice = ui.select(f"Which workflow to {verb}?",
                       [(wid, wf.description) for wid, wf in workflows])
    if choice is None:
        ui.info("cancelled")
        sys.exit(0)
    return workflows[choice][0]


def _tool_call_summary(tool_calls: list[dict]) -> list[str]:
    """Which tools a run actually called, with repeats counted and stubs marked.

    A run's own record of what it touched, one tool per line. `x2` rather than
    two identical lines, because the interesting part is which tools ran, not how
    long the list is.
    """
    counts: dict[str, int] = {}
    stubbed: set[str] = set()
    for call in tool_calls:
        tool = call.get("tool") or "?"
        counts[tool] = counts.get(tool, 0) + 1
        if call.get("stubbed"):
            stubbed.add(tool)
    lines = []
    for tool, n in counts.items():
        label = tool if n == 1 else f"{tool} x{n}"
        lines.append(f"{label} (stubbed)" if tool in stubbed else label)
    return lines


def _print_run_outcome(home: Path, workflow_id: str, record: dict,
                       error: str = "") -> None:
    """Reports a finished run as an aligned block, the way a build reports itself.

    The one line this replaced put the run id and an output path in one string --
    `run_2026... -> output/logs/daily.md` -- which is the shape that reads worst
    when you want to act on it: neither half can be copied without editing, and
    the path was relative to a store root that went unnamed. Every field is its
    own bulleted row now, and every path is written `~/.px0/...`: short, and
    still a path a shell will open.

    Written to stderr, like the line before it: stdout belongs to the run's own
    output text and to `--json`.
    """
    ok = not error and record.get("outcome") == "success"
    verdict = "success" if ok else ui.alert("failed", stream=sys.stderr)
    ui.heading(f"{verdict} {ui.accent(workflow_id, stream=sys.stderr)}", stream=sys.stderr)

    rows = []
    out = record.get("output") or {}
    if record.get("id"):
        rows.append(("run", record["id"]))
    if out.get("target") == "file" and out.get("path"):
        # `~/.px0/...`: short enough to read, and still a path the shell expands,
        # which a bare `output/logs/daily.md` relative to an unnamed store is not.
        rows.append(("output", paths.display(home / out["path"])))
    elif out.get("target") == "stdout":
        rows.append(("output", "printed below"))
    if record.get("tool_calls"):
        rows.append(("tools", _tool_call_summary(record["tool_calls"])))
    if record.get("attempt", 1) > 1:
        rows.append(("attempt", f"{record['attempt']} of {record.get('attempts', '?')}"))
    if record.get("duration_seconds") is not None:
        rows.append(("took", f"{record['duration_seconds']:.1f}s"))
    if record.get("dry_run"):
        rows.append(("dry run", "write tools were stubbed, not called"))
    if error:
        rows.append(("error", ui.alert(error, stream=sys.stderr)))

    # Bullets, not ticks: every row here is a fact about the run, and the one
    # verdict -- success or failed -- is the heading. The error carries its own
    # colour rather than a `✗`, which would sit two columns left of every other
    # label and break the block it is part of.
    width = max(len(label) for label, _ in rows) if rows else 0
    for label, detail in rows:
        ui.field(label, detail, width=width, stream=sys.stderr)

    if ok and out.get("target") == "file":
        ui.hint("read it here:", stream=sys.stderr)
        ui.command(f"px0 runs open {record['id']}", stream=sys.stderr)
    elif not ok and record.get("id"):
        ui.hint("what the run did before it failed:", stream=sys.stderr)
        ui.command(f"px0 runs logs {record['id']}", stream=sys.stderr)
    if ok and record.get("id") and not record.get("dry_run"):
        # Whether the output was any *good* is the one thing no record can
        # infer, and the moment the user has just read it is the only moment
        # they know. Offered here rather than asked, so a scripted run is not
        # blocked on an answer.
        ui.hint("if it came back wrong, say so -- it is what `improve` learns from:",
                stream=sys.stderr)
        ui.command(f'px0 runs mark {record["id"]} --bad "what was wrong"',
                   stream=sys.stderr)


def _fill_template_vars(home: Path, workflow_id: str, cli_inputs: dict,
                        args: argparse.Namespace) -> None:
    """Asks for a template's vars, when there is somebody there to ask.

    A workflow with `vars:` is usually one somebody else wrote, so the first run
    of it is exactly the moment nobody knows what to pass. The runner refuses a
    missing required var on its own, and that refusal is what covers the daemon
    and every scripted run; this only spares an interactive user having to read
    it once to learn what the flags were.

    Skipped for anything that is not a person at a terminal: `--stdin` is
    already reading the stream the answers would come from, `--json` and
    `--quiet` are asking for machine output, and a run the daemon spawned
    carries `--trigger`.
    """
    if (args.stdin or getattr(args, "json", False) or args.quiet
            or getattr(args, "trigger", None) or not sys.stdin.isatty()):
        return
    try:
        wf = workflow_mod.load(home, workflow_id)
    except workflow_mod.WorkflowError:
        return  # the runner reports it, and writes a record while doing so
    _filled, missing = workflow_mod.var_values(wf, cli_inputs)
    if not missing:
        return
    specs = {v["name"]: v for v in workflow_mod.declared_vars(wf)}
    ui.info(f"{workflow_id} is a template",
            f"it needs {len(missing)} value{'s' if len(missing) != 1 else ''}")
    for name in missing:
        spec = specs.get(name) or {}
        if spec.get("description"):
            ui.field(name, spec["description"])
        if spec.get("values"):
            ui.hint("for example: " + ", ".join(spec["values"]))
        answer = ui.prompt(f"  {name}: ").strip()
        if answer:
            cli_inputs[name] = answer
    print(flush=True)


def cmd_run(args: argparse.Namespace) -> None:
    """Handles `px0 workflows run`: executes a workflow with inputs collected from --stdin and
    --input KEY=VALUE flags, then prints the outcome and, depending on --json/--quiet
    and the workflow's output target, the run's output text."""
    home, config = _ctx()
    workflow_id = args.workflow or _pick_workflow(home, args.stdin)
    cli_inputs: dict = {}
    if args.stdin:
        cli_inputs["_stdin"] = sys.stdin.read()
    for kv in args.input or []:
        key, _, value = kv.partition("=")
        cli_inputs[key] = value
    _fill_template_vars(home, workflow_id, cli_inputs, args)

    output_override = {"target": args.output} if args.output else None
    # "late" wins over the spawner's own label: a backfilled fire is still a
    # scheduled one, and the record needs to say it ran behind.
    trigger = ("late" if args.late_scheduled_at
               else getattr(args, "trigger", None) or "manual")

    try:
        with ui.spinner(f"Running {workflow_id}", quiet=args.quiet or args.json):
            record = runner.run(
                home, config, workflow_id, trigger=trigger, cli_inputs=cli_inputs,
                dry_run=args.dry_run, output_override=output_override,
                late_scheduled_at=args.late_scheduled_at,
                timeout_override=getattr(args, "timeout", None),
                retry=not getattr(args, "no_retry", False),
            )
    except runner.RunError as e:
        if args.quiet:
            ui.err("run failed", str(e))
        else:
            _print_run_outcome(home, workflow_id, e.record, error=str(e))
        sys.exit(EXIT_USER_ERROR)

    if not args.quiet:
        _print_run_outcome(home, workflow_id, record)

    if args.json:
        _dump(args, record)
    elif record.get("output", {}).get("target") == "stdout":
        print(record["output"].get("text", ""))


def cmd_ask(args: argparse.Namespace) -> None:
    """Handles `px0 brain ask`: answers a question via retrieval over brain/
    and prints the answer, optionally followed by source passages with --sources."""
    home, config = _ctx()
    k = args.k if args.k is not None else config_mod.get(config, "retrieval.k_default", 5)
    try:
        with ui.spinner("Searching your brain"):
            result = ask_mod.ask(home, config, args.question, k=k,
                                 kind=getattr(args, "kind", None))
    except ask_mod.AskError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    print(result["answer"])
    if args.sources:
        ui.heading("sources")
        for p in result["passages"]:
            ui.bullet(f"{p.path}{ui.dim('#' + p.anchor)}")


# --- listing --------------------------------------------------------------
#
# One printer per entity, each reachable both as its own verb
# (`px0 workflows list`) and as a section of `px0 store list`. The `heading`
# flag is what differs: a single-entity listing needs no title, because the
# command the user typed already said what they are looking at.

def _print_workflows(home: Path, heading: bool) -> None:
    workflows = sorted(workflow_mod.load_all(home).items())
    if heading:
        ui.heading(f"workflows {ui.dim(f'({len(workflows)})')}")
    width = max((len(w) for w, _ in workflows), default=0)
    for wid, wf in workflows:
        # A template is a different kind of thing to run -- it has to be given
        # values first -- so the listing says so rather than letting the first
        # attempt be the way you find out.
        mark = ui.faint(f"  [template: {len(workflow_mod.declared_vars(wf))} vars]") \
            if workflow_mod.is_template(wf) else ""
        print(f"  {wid.ljust(width)}  {ui.dim(wf.description)}{mark}")
    # Broken files are skipped by load_all, so they have to be reported here or
    # they vanish silently -- the whole point of skipping was to keep the rest
    # usable, not to hide the breakage.
    errors = workflow_mod.load_errors(home)
    for e in errors:
        ui.warn("unreadable workflow", e, stream=sys.stdout)
    if not workflows and not errors:
        ui.hint("none yet -- describe one with `px0 workflows new`")


def _print_guidelines(home: Path, heading: bool) -> None:
    """Every guideline, numbered the way `workflows run` numbers its picker.

    Same rows as the picker on purpose: guidelines are a short list you scan and
    then name, so it should look like the other short list px0 shows you.

    The detail is the file's frontmatter description -- the same line a build
    matches a new workflow against -- so what decides an attachment is what the
    user reads here. Files written before frontmatter existed fall back to their
    first rule rather than showing a blank column.
    """
    files = guidelines_mod.load_all(home)
    if heading:
        ui.heading(f"guidelines {ui.dim(f'({len(files)})')}")
    ui.numbered([(rel, g.summary) for rel, g in files.items()])
    if not files:
        ui.hint("none yet -- `px0 workflows new` drafts one when a workflow needs it")


def _report_brain_path(home: Path, config: dict) -> None:
    """Says what px0 found after `brain.path` is pointed somewhere new.

    Printed at the moment of setting because that is when the answer is useful.
    The collision it warns about is the one real trap in pointing the brain at an
    existing vault: `work/` means "never leaves this machine" to px0 and "my work
    notes" to every notes app, so without this the user's work folder simply
    stops appearing in searches and nothing says why.
    """
    base = retrieval.brain_path(home, config)
    if not base.exists():
        ui.warn("no such directory yet", str(base), stream=sys.stdout)
        ui.hint("it will be created on the first `px0 brain add`")
        return

    globs = retrieval.ignore_globs(config)
    private = retrieval.private_folder(config)
    found = skipped = private_count = 0
    for p in base.rglob("*.md"):
        rel = str(p.relative_to(base))
        if retrieval.is_ignored(rel, globs):
            skipped += 1
        elif retrieval.is_private(rel, private):
            private_count += 1
        else:
            found += 1

    ui.info(f"{found} Markdown file(s) found", str(base))
    if skipped:
        ui.hint(f"{skipped} skipped as tool state (.obsidian/, .trash/, ...) or by brain.ignore")
    if (base / ".obsidian").is_dir():
        ui.info("this looks like an Obsidian vault", "px0 reads it in place and writes nothing you did not ask for")
    if private_count:
        ui.warn(f"{private_count} file(s) under {private}/ will be held back from every search",
                stream=sys.stdout)
        ui.hint(f"{private}/ is the never-leaves-this-machine folder. If that is not what "
                f"you meant by it:")
        ui.command('px0 config set brain.private_folder ""')
    if found:
        ui.hint("build the index: px0 brain reindex")


def _print_brain(home: Path, config: dict, heading: bool) -> None:
    base = retrieval.brain_path(home, config)
    globs = retrieval.ignore_globs(config)
    private = retrieval.private_folder(config)

    rows, skipped = [], 0
    for p in sorted(base.rglob("*.md")) if base.exists() else []:
        rel = str(p.relative_to(base))
        # Listing what retrieval ignores would misreport the brain: pointed at a
        # vault, most of what `rglob` finds is the notes app's own state.
        if retrieval.is_ignored(rel, globs):
            skipped += 1
            continue
        rows.append((rel, retrieval.is_private(rel, private)))

    if heading:
        ui.heading(f"brain {ui.dim(f'({len(rows)})')}")
    for rel, priv in rows:
        # Marked, not hidden. A file silently absent from every search is the
        # single most confusing thing about pointing the brain at a vault that
        # already has a folder named like the private one.
        print(f"  {rel}" + (ui.dim("  (private)") if priv else ""))
    if skipped:
        ui.hint(f"{skipped} file(s) skipped as tool state or by brain.ignore")
    if not rows:
        ui.hint("none yet -- add something with `px0 brain add <url-or-file>`")


def cmd_workflows_list(args: argparse.Namespace) -> None:
    """Handles `px0 workflows list`: every workflow id with its description."""
    home, _ = _ctx()
    _print_workflows(home, heading=False)



def cmd_workflows_show(args: argparse.Namespace) -> None:
    """Handles `px0 workflows show`: print one workflow, file and all."""
    home, config = _ctx()
    wf = workflow_mod.load(home, args.workflow)
    if getattr(args, "json", False):
        _dump(args, {
            "id": wf.id, "path": str(wf.path.relative_to(home)), "version": wf.version,
            "description": wf.description, "request": wf.request, "enabled": wf.enabled,
            "trigger": wf.trigger, "guidelines": wf.guidelines, "tools": wf.tools,
            "vars": workflow_mod.declared_vars(wf),
            "inputs": [dataclasses.asdict(i) for i in wf.inputs],
            "output": wf.output, "timeout": wf.timeout, "pipeline": wf.pipeline,
            "on_failure": wf.on_failure, "retry": wf.retry, "body": wf.body,
        })
        return
    ui.kv("file", str(wf.path.relative_to(home)))
    ui.kv("version", f"v{versioning.latest_version_number(home, str(wf.path.relative_to(home))) or 1}")
    if not wf.enabled:
        ui.warn("disabled", "it will not fire until `px0 workflows enable` runs")
    print()
    ui.render_workflow_markdown(wf.path.read_text())


def _validate_one(home: Path, wf) -> list[str]:
    return workflow_mod.validate(wf, home)


def cmd_workflows_validate(args: argparse.Namespace) -> None:
    """Handles `px0 workflows validate`: check workflows without running them.

    Validation used to be reachable only by firing a workflow or running a full
    `px0 doctor`, so a hand edit could not be checked before it mattered.
    """
    home, config = _ctx()
    results = []
    if getattr(args, "workflow", None):
        targets = [workflow_mod.load(home, args.workflow)]
    else:
        targets = sorted(workflow_mod.load_all(home).values(), key=lambda w: w.id)
        for message in workflow_mod.load_errors(home):
            results.append({"workflow": None, "ok": False, "errors": [message]})
    for wf in targets:
        errors = _validate_one(home, wf)
        results.append({"workflow": wf.id, "ok": not errors, "errors": errors})

    if getattr(args, "json", False):
        _dump(args, {"checked": len(results), "results": results,
                     "ok": all(r["ok"] for r in results)})
        return

    bad = 0
    for result in results:
        name = result["workflow"] or "unparseable file"
        if result["ok"]:
            ui.ok(name, "valid")
        else:
            bad += 1
            ui.err(name)
            for message in result["errors"]:
                ui.bullet(message)
    if not results:
        ui.info("no workflows to check", "write one with `px0 workflows new`")
    if bad:
        sys.exit(EXIT_USER_ERROR)


def cmd_workflows_delete(args: argparse.Namespace) -> None:
    """Handles `px0 workflows delete`: remove a workflow, keeping its history."""
    home, config = _ctx()
    workflow_id = args.workflow or _pick_workflow(home, for_stdin=False, verb="delete")
    path = authoring.workflow_path(home, workflow_id)
    if not path.exists():
        ui.err(f"no such workflow: {workflow_id}")
        ui.hint("see what there is with `px0 workflows list`")
        sys.exit(EXIT_USER_ERROR)
    rel = str(path.relative_to(home))
    scheduled = ""
    try:
        wf = workflow_mod.parse(path)
        if (wf.trigger or {}).get("schedule"):
            scheduled = f" (scheduled {wf.trigger['schedule']})"
    except workflow_mod.WorkflowError:
        pass
    if not _confirm(f"Remove {rel}{scheduled}?", getattr(args, "yes", False)):
        ui.info("kept", rel)
        return
    result = authoring.remove_file(home, path, evidence=f"px0 workflows delete {workflow_id}")
    ui.ok("removed", rel)
    if result.get("change_id"):
        ui.hint(f"undo with `px0 changes revert {result['change_id']}`")
    daemon_mod.restart_if_running(home, config)


def cmd_workflows_rename(args: argparse.Namespace) -> None:
    """Handles `px0 workflows rename`: new id, new filename, same history."""
    home, config = _ctx()
    src = authoring.workflow_path(home, args.workflow)
    if not src.exists():
        ui.err(f"no such workflow: {args.workflow}")
        sys.exit(EXIT_USER_ERROR)
    new_id = authoring.check_id(args.new_id, "workflow id")
    if new_id in workflow_mod.load_all(home):
        ui.err(f"{new_id} already exists")
        sys.exit(EXIT_USER_ERROR)
    dest = src.parent / f"{new_id}.md"
    # The id lives in the frontmatter as well as the filename; a rename that
    # only moved the file left the workflow running under its old id.
    text = authoring.set_frontmatter_key(src.read_text(), "id", new_id)
    src.write_text(text)
    result = authoring.move_file(home, src, dest, evidence=f"renamed {args.workflow} to {new_id}")
    ui.ok("renamed", f"{result['from']} -> {result['to']}")
    daemon_mod.restart_if_running(home, config)


def cmd_workflows_copy(args: argparse.Namespace) -> None:
    """Handles `px0 workflows copy`: fork a working workflow instead of rewriting it."""
    home, config = _ctx()
    src = authoring.workflow_path(home, args.workflow)
    if not src.exists():
        ui.err(f"no such workflow: {args.workflow}")
        sys.exit(EXIT_USER_ERROR)
    new_id = authoring.check_id(args.new_id, "workflow id")
    if new_id in workflow_mod.load_all(home):
        ui.err(f"{new_id} already exists")
        sys.exit(EXIT_USER_ERROR)
    dest = src.parent / f"{new_id}.md"
    body = authoring.set_frontmatter_key(src.read_text(), "id", new_id)
    authoring.write_file(home, dest, body, evidence=f"copied from {args.workflow}")
    ui.ok("copied", f"{args.workflow} -> {new_id}")
    ui.hint(f"edit it with `px0 workflows edit {new_id}`")


def _print_candidates(found: list) -> None:
    """What the scan found, before the model is asked anything about it.

    Printed first for the same reason `px0 workflows improve` prints its health
    report first: the deterministic half decides what may be touched at all, and
    a user should read that before reading an opinion about it. Nothing outside
    this list can end up as a var, whatever the model answers.
    """
    ui.heading(f"values found in this workflow {ui.dim(f'({len(found)})')}")
    width = max((len(c.kind) for c in found), default=0)
    for cand in found:
        where = ", ".join(cand.locations[:3])
        if len(cand.locations) > 3:
            where += f", +{len(cand.locations) - 3} more"
        print(f"  {ui.dim(cand.kind.ljust(width))}  {cand.literal}", flush=True)
        print(f"  {' ' * width}  {ui.faint(where)}", flush=True)


def _print_template_proposal(wf, proposal, counts: dict) -> None:
    """The vars in full, before the file is touched."""
    ui.heading(f"template for {ui.accent(wf.id)}")
    if proposal.summary:
        ui.say(proposal.summary)
        print(flush=True)
    for var in proposal.vars:
        ui.kv(var.name, f"{var.literal}  {ui.dim('->')}  {var.token()}")
        ui.field("what it is", var.description, width=12)
        if var.values:
            ui.field("for example", ", ".join(var.values), width=12)
        if var.default is not None:
            ui.field("default", var.default, width=12)
        else:
            ui.field("required", "no default; every run has to be given one", width=12)
        touched = counts.get(var.name, 0)
        ui.field("replaces", f"{touched} occurrence(s) in this file", width=12)
        print(flush=True)
    for skip in proposal.skipped:
        if skip.get("literal"):
            ui.field(f"left alone: {skip['literal']}", skip.get("why", ""), width=0)
    if proposal.dropped:
        # A literal the model asked for that the scan never offered. Named
        # rather than swallowed: it is usually the model having misread the file,
        # and that is worth seeing.
        ui.warn("not in the scan, so dropped", ", ".join(proposal.dropped))


def cmd_workflows_templatize(args: argparse.Namespace) -> None:
    """Handles `px0 workflows templatize`: lift this installation's values into vars.

    The order is the point of the command, and it is the same order
    `px0 workflows improve` uses. The deterministic scan runs and is printed
    first, so the user sees the complete set of literals that could possibly be
    touched. Only then is the model asked which of them belong to the
    installation rather than to the job. What comes back is printed in full,
    diffed against the file, and validated as a workflow before a byte is
    written -- because the output of this command is a file the user is likely
    to hand to somebody else.
    """
    home, config = _ctx()
    workflow_id = args.workflow or _pick_workflow(home, for_stdin=False, verb="templatize")
    as_json = getattr(args, "json", False)

    try:
        wf, found, payload = templates_mod.load_case(home, workflow_id)
    except workflow_mod.WorkflowError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)

    new_id = None
    if getattr(args, "to", None):
        try:
            new_id = authoring.check_id(args.to, "workflow id")
        except authoring.AuthoringError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if new_id in workflow_mod.load_all(home):
            ui.err(f"{new_id} already exists")
            sys.exit(EXIT_USER_ERROR)

    if getattr(args, "candidates", False):
        if as_json:
            _dump(args, payload)
            return
        if not found:
            ui.info("nothing found", "this workflow carries no literal worth lifting out")
            return
        _print_candidates(found)
        return

    if not found:
        ui.info("nothing to templatize",
                "no literal in this workflow's inputs or body belongs to one installation")
        ui.hint("a workflow becomes shareable when it reads a named repository, "
                "channel, or folder -- this one names none")
        return

    # Said before the model call, not after: a scheduled workflow templatized in
    # place is a workflow that fails every fire, and finding that out after
    # paying for a proposal is the wrong order.
    unattended = ("scheduled" if (wf.trigger or {}).get("schedule")
                  else "watched" if (wf.trigger or {}).get("watch") else "")
    if unattended and not new_id and not as_json:
        ui.warn(f"{workflow_id} is {unattended}",
                "nothing passes --input to an unattended fire, so a var with no "
                "default would fail every run")
        ui.hint("keep the template beside the working workflow instead:")
        ui.command(f"px0 workflows templatize {workflow_id} --to {workflow_id}-template")
        if not _confirm("templatize it in place anyway?", getattr(args, "yes", False)):
            return
        print(flush=True)

    if not as_json:
        _print_candidates(found)
        print(flush=True)

    try:
        with ui.spinner("Working out what belongs to this installation"):
            proposal = templates_mod.propose(config, payload)
    except templates_mod.TemplateError as e:
        ui.err("no template", str(e))
        sys.exit(EXIT_MODEL_ERROR)

    if not proposal.vars:
        if as_json:
            _dump(args, {"workflow": workflow_id, "vars": [], "applied": False,
                         "summary": proposal.summary, "dropped": proposal.dropped})
            return
        ui.info("no vars proposed", "everything here reads as part of the job")
        if proposal.dropped:
            ui.warn("not in the scan, so dropped", ", ".join(proposal.dropped))
        return

    original = wf.path.read_text()
    try:
        new_text, counts = templates_mod.apply(original, proposal.vars)
    except templates_mod.TemplateError as e:
        ui.err("could not rewrite the file", str(e))
        sys.exit(EXIT_USER_ERROR)

    # A literal that contains another means the shorter one has nowhere left to
    # match once the longer is substituted, so it is dropped from the block
    # rather than declared and unused. Named, because the user just read it in
    # the proposal.
    unused = [v.name for v in proposal.vars if counts.get(v.name, 0) == 0]
    proposal.vars = [v for v in proposal.vars if counts.get(v.name, 0) > 0]
    if not proposal.vars:
        ui.info("nothing was substituted",
                "every proposed value was already covered by a longer one")
        return

    dest = wf.path if new_id is None else wf.path.parent / f"{new_id}.md"
    if new_id is not None:
        new_text = authoring.set_frontmatter_key(new_text, "id", new_id)

    # Validated before it is written, never after. The result of this command is
    # a file meant to leave the machine, and a template that does not parse is
    # worse than no template: it fails for its next reader, in their store,
    # over a mistake made here.
    try:
        rewritten = workflow_mod.parse_text(new_text, dest)
        errors = workflow_mod.validate(rewritten, home)
    except workflow_mod.WorkflowError as e:
        errors = [str(e)]
    # Only what this rewrite introduced. A workflow can already be invalid for
    # reasons that have nothing to do with templatizing it -- a tool whose app
    # was disconnected, a guideline someone deleted -- and refusing to write the
    # template over a fault that was there before would leave the user unable to
    # do the one thing they asked for.
    existing = set(workflow_mod.validate(wf, home))
    inherited = [e for e in errors if e in existing]
    errors = [e for e in errors if e not in existing]

    if as_json:
        _dump(args, {
            "workflow": workflow_id,
            "to": new_id,
            "summary": proposal.summary,
            "vars": [dataclasses.asdict(v) | {"sites": counts.get(v.name, 0)}
                     for v in proposal.vars],
            "skipped": proposal.skipped,
            "dropped": proposal.dropped,
            "errors": errors,
            "inherited_errors": inherited,
            "run_command": templates_mod.example_command_for(
                new_id or workflow_id, workflow_mod.declared_vars(rewritten)),
            # Reporting only, as with `px0 workflows improve --json`: the write
            # is the interactive path, because it is shown as a diff first.
            "applied": False,
        })
        return

    _print_template_proposal(wf, proposal, counts)
    if unused:
        ui.info("covered by a longer value, so not declared", ", ".join(unused))
    for message in inherited:
        ui.warn("already true of this workflow", message)

    if errors:
        ui.err("the template would not be a valid workflow", "nothing was written")
        for message in errors:
            ui.bullet(message)
        sys.exit(EXIT_USER_ERROR)

    print(f"  {ui.dim(str(dest.relative_to(home)))}", flush=True)
    _print_diff(replay_mod.diff(original, new_text), limit=60)
    print(flush=True)

    if getattr(args, "dry_run", False):
        ui.info("dry run", "nothing written")
        ui.hint("write it with:")
        ui.command(f"px0 workflows templatize {workflow_id}"
                   + (f" --to {new_id}" if new_id else ""))
        return

    question = (f"Write this template to {dest.relative_to(home)}?" if new_id
                else f"Rewrite {dest.relative_to(home)} as a template?")
    if not _confirm(question, getattr(args, "yes", False)):
        ui.info("nothing changed")
        return

    result = authoring.write_file(
        home, dest, new_text,
        evidence=(f"templatized from {workflow_id}" if new_id
                  else f"templatized {workflow_id}"))
    ui.ok("templatized", str(dest.relative_to(home)))
    ui.hint("run it by naming each var:")
    ui.command(templates_mod.example_command_for(
        new_id or workflow_id, workflow_mod.declared_vars(rewritten)))
    if (wf.trigger or {}).get("schedule"):
        # The schedule is deliberately never templatized -- a cron expression is
        # validated when the file loads, and the daemon has no `--input` to give
        # it -- so whoever installs this has to set their own.
        ui.hint(f"the schedule stays as {wf.trigger['schedule']}; whoever installs "
                "this edits it with `px0 workflows edit`")
    if result.get("change_id"):
        ui.hint(f"undo with `px0 changes revert {result['change_id']}`")
    if new_id:
        daemon_mod.restart_if_running(home, config)


def cmd_workflows_enable(args: argparse.Namespace) -> None:
    """Handles `px0 workflows disable` and `enable`: park a workflow, or unpark it.

    Parking used to mean deleting `trigger.schedule` and remembering it later.
    """
    home, config = _ctx()
    enable = args.workflows_cmd == "enable"
    path = authoring.workflow_path(home, args.workflow)
    if not path.exists():
        ui.err(f"no such workflow: {args.workflow}")
        sys.exit(EXIT_USER_ERROR)
    wf = workflow_mod.parse(path)
    if wf.enabled == enable:
        ui.info(args.workflow, "already " + ("enabled" if enable else "disabled"))
        return
    text = authoring.set_frontmatter_key(path.read_text(), "enabled", enable)
    authoring.write_file(home, path, text,
                          evidence=("enabled" if enable else "disabled") + " via cli")
    ui.ok("enabled" if enable else "disabled", args.workflow)
    schedule = (wf.trigger or {}).get("schedule")
    if schedule and not enable:
        ui.hint(f"it will not fire on {schedule} until you run `px0 workflows enable {args.workflow}`")
    daemon_mod.restart_if_running(home, config)


def cmd_guidelines_list(args: argparse.Namespace) -> None:
    """Handles `px0 guidelines list`: every guideline file, store-relative."""
    home, _ = _ctx()
    _print_guidelines(home, heading=False)


def cmd_brain_show(args: argparse.Namespace) -> None:
    """Handles `px0 brain show`: one file, with what it came from."""
    home, config = _ctx()
    try:
        info = brain_mod.show(home, config, args.path)
    except brain_mod.IngestError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    if getattr(args, "json", False):
        _dump(args, info)
        return
    ui.kv("path", info["path"])
    for key in ("source", "kind", "title", "retrieved"):
        if info["header"].get(key):
            ui.kv(key, str(info["header"][key]))
    if info["private"]:
        ui.warn("private", "held back from every search, and never sent anywhere")
    print()
    print(info["body"], end="" if info["body"].endswith("\n") else "\n")


def cmd_brain_rm(args: argparse.Namespace) -> None:
    """Handles `px0 brain rm`: remove a file and drop its passages."""
    home, config = _ctx()
    try:
        info = brain_mod.show(home, config, args.path)
    except brain_mod.IngestError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    if not _confirm(f"Remove {info['path']} from the brain?", getattr(args, "yes", False)):
        ui.info("kept", info["path"])
        return
    result = brain_mod.remove(home, config, args.path)
    ui.ok("removed", result["path"])
    if result.get("reindexed") is not None:
        ui.hint(f"index rebuilt: {result['reindexed']} passages")


def cmd_brain_export(args: argparse.Namespace) -> None:
    """Handles `px0 brain export`: copy the library out, private folder held back."""
    home, config = _ctx()
    result = brain_mod.export_library(home, config, Path(args.dir),
                                      include_private=args.include_private)
    ui.ok("exported", f"{result['copied']} file(s) to {result['dest']}")
    if result["held_back"]:
        ui.hint(f"{result['held_back']} private file(s) held back; "
                "pass --include-private to include them")


def cmd_brain_list(args: argparse.Namespace) -> None:
    """Handles `px0 brain list`: every brain file, store-relative."""
    home, config = _ctx()
    _print_brain(home, config, heading=False)


def cmd_store_list(args: argparse.Namespace) -> None:
    """Handles `px0 store list`: all three entities at once, each under a heading."""
    home, config = _ctx()
    _print_workflows(home, heading=True)
    _print_guidelines(home, heading=True)
    _print_brain(home, config, heading=True)


# --- tools ---------------------------------------------------------------

# Composio's status vocabulary, in words that say what to do about it.
_AUTH_STATE = {
    "NOT_CONNECTED": "not authorized",
    "INITIATED": "consent pending",
    "FAILED": "authorization failed",
    "NOT_FOUND": "authorization gone",
}


def cmd_tools(args: argparse.Namespace) -> None:
    """Handles the `px0 tools` group: list, search, call, connect, disconnect, refresh."""
    verb = getattr(args, "tools_cmd", "list")
    if verb == "search":
        return _tools_search(args)
    if verb == "call":
        return _tools_call(args)
    if verb == "connect":
        return _tools_connect(args)
    if verb == "disconnect":
        return _tools_disconnect(args)
    if verb == "refresh":
        return _tools_refresh(args)
    return _tools_list(args)


def _tools_list(args: argparse.Namespace) -> None:
    """Handles `px0 tools list`: prints each available tool with a read/write marker,
    its id, provider, description, and parameters, optionally filtered by service."""
    # Include tools discovered by `px0 workflows new` when there is a store to read them
    # from, but never require one: this listing is how you find out what px0 can
    # do before `px0 init`.
    home = paths.store_home()
    listed = tools.list_tools(args.service,
                              home=home if store_mod.is_initialized(home) else None)

    # Connection state per provider, so this one screen answers both "what can a
    # workflow call" and "what would work right now". Live status is one API call
    # per connected app, so only ask about providers actually being listed -- and
    # only when asked, since the plain listing must work before `px0 init`.
    statuses = {}
    if args.status:
        home, _ = _ctx()  # status needs credentials, so this one does need a store
        with ui.spinner("Checking connections"):
            for provider in sorted({t.provider for t in listed}):
                statuses[provider] = connect_mod.connected_account_status(home, provider)

    if args.json:
        _dump(args, [{"id": t.id, "provider": t.provider, "is_write": t.is_write,
                      "description": t.description, "params": t.params,
                      **({"status": statuses[t.provider]} if statuses else {})}
                     for t in listed])
        return

    if not listed:
        if args.service:
            ui.info(f"no tools for {args.service!r}")
            known = sorted({t.provider for t in tools.list_tools(
                None, home=home if store_mod.is_initialized(home) else None)})
            if known:
                ui.hint(f"available: {', '.join(known)}")
        else:
            ui.info("no tools available")
        return

    width = max((len(t.id) for t in listed), default=0)
    desc_width = max((len(t.description) for t in listed), default=0) if statuses else 0
    for t in listed:
        # write access is the one thing worth colour here
        marker = ui.paint("write", "179") if t.is_write else ui.dim("read ")
        state = ""
        if statuses:
            status = statuses[t.provider]
            state = ("  " + ui.dim("ready") if status == "ACTIVE"
                     else "  " + ui.paint(_AUTH_STATE.get(status, status.lower()), "179"))
        desc = ui.dim(t.description.ljust(desc_width))
        print(f"  {marker}  {t.id.ljust(width)}  {desc}{state}")

    writes = sum(1 for t in listed if t.is_write)
    if writes:
        ui.hint(f"{writes} of {len(listed)} tools can change things outside px0")
    if store_mod.is_initialized(home):
        _user_tools, tool_errors = localtools.load_user_tools(home)
        for message in tool_errors:
            ui.warn("user tool skipped", message)
    if not config_mod.get(config_mod.load(paths.config_path(home)), "tools.allow_shell", False):
        if any(t.id == "shell.run" for t in listed):
            ui.hint("shell.run is listed but disabled; enable it with "
                    "`px0 config set tools.allow_shell true`")
    unready = sorted(p for p, st in statuses.items() if st != "ACTIVE")
    if unready:
        ui.hint(f"not authorized yet: {', '.join(unready)} -- a workflow that needs "
                "one prints its authorization URL on the first run")


def _catalogue_failed(error: Exception) -> None:
    """Reports a catalogue failure in one line and says what fixes it."""
    message = str(error)
    ui.err("could not reach Composio's catalogue", message.split("\n")[0][:200])
    if "401" in message or "Invalid API key" in message:
        ui.remedy("px0 config composio <key>")
    else:
        ui.hint("check the network, then try again")
    sys.exit(EXIT_CONNECTOR_ERROR)


def _tools_search(args: argparse.Namespace) -> None:
    """Handles `px0 tools search`: browse Composio's catalogue before writing a workflow.

    The catalogue was searchable only from inside `px0 workflows new`, so the
    first question anyone has -- what can this reach? -- had no command.
    """
    home, config = _ctx()
    limit = args.limit or (40 if args.toolkits else catalogue_mod.SEARCH_LIMIT)

    if args.toolkits:
        try:
            with ui.spinner("Searching Composio's toolkits"):
                found = catalogue_mod.toolkits(home, args.query, limit=limit)
        except catalogue_mod.CatalogueError as e:
            _catalogue_failed(e)
        if getattr(args, "json", False):
            _dump(args, found)
            return
        if not found:
            ui.info("no toolkit matches", args.query or "")
            return
        width = max(len(t["slug"]) for t in found)
        for toolkit in found:
            counts = ui.dim(f"{toolkit['tools']:>4} tools")
            triggers = ui.dim(f"  {toolkit['triggers']} triggers") if toolkit["triggers"] else ""
            print(f"  {toolkit['slug'].ljust(width)}  {counts}{triggers}  "
                  f"{ui.dim(toolkit['description'][:70])}")
        ui.hint(f"{len(found)} toolkit(s); authorize one with `px0 tools connect <slug>`")
        return

    if not args.query:
        ui.err("nothing to search for")
        ui.hint("name what the tool should do, or pass --toolkits to list toolkits")
        sys.exit(EXIT_USER_ERROR)

    try:
        with ui.spinner("Searching Composio's catalogue"):
            found = catalogue_mod.search(home, args.query, limit=limit, toolkit=args.toolkit)
    except catalogue_mod.CatalogueError as e:
        _catalogue_failed(e)
    if getattr(args, "json", False):
        _dump(args, [dataclasses.asdict(t) | {"id": t.id} for t in found])
        return
    if not found:
        ui.info("no tool matches", args.query)
        ui.hint("try fewer words, or `px0 tools search --toolkits <name>` first")
        return
    width = max(len(t.slug) for t in found)
    for tool in found:
        marker = ui.paint("write", "179") if tool.is_write else ui.dim("read ")
        if tool.is_destructive:
            marker = ui.paint("destroy", "203")
        print(f"  {marker}  {tool.slug.ljust(width)}  {ui.dim(tool.description[:70])}")
    ui.hint(f"{len(found)} tool(s). A workflow gets one by naming the job: "
            "`px0 workflows new`")


def _parse_tool_args(pairs: list[str] | None) -> dict:
    """Parses repeated --arg KEY=VALUE into a dict, JSON-decoding what parses as JSON."""
    out: dict = {}
    for raw in pairs or []:
        if "=" not in raw:
            ui.err(f"--arg needs KEY=VALUE, got {raw!r}")
            sys.exit(EXIT_USER_ERROR)
        key, value = raw.split("=", 1)
        try:
            out[key.strip()] = json.loads(value)
        except json.JSONDecodeError:
            out[key.strip()] = value
    return out


def _tools_call(args: argparse.Namespace) -> None:
    """Handles `px0 tools call`: fire one tool and look at the result.

    A dry run stubs every write, so before this the first real call a tool ever
    made was inside a live workflow.
    """
    home, config = _ctx()
    spec = tools.resolve(args.tool, home)
    if spec is None:
        ui.err(f"no such tool: {args.tool}")
        ui.hint("see what there is with `px0 tools list`")
        sys.exit(EXIT_USER_ERROR)
    call_args = _parse_tool_args(getattr(args, "arg", None))

    missing = [name for name, kind in spec.params.items()
               if str(kind).endswith("*") and name not in call_args]
    if missing:
        ui.err(f"{spec.id} needs {', '.join(missing)}")
        ui.hint("pass each one as --arg name=value")
        sys.exit(EXIT_USER_ERROR)

    if spec.is_write and not _confirm(
            f"{spec.id} can change things outside px0. Call it for real?",
            getattr(args, "yes", False)):
        ui.info("not called", spec.id)
        return

    try:
        with ui.spinner(f"Calling {spec.id}"):
            result = tools.call(home, config, spec.id, call_args)
    except tools.ConnectorNotConfigured as e:
        ui.err(str(e))
        sys.exit(EXIT_CONNECTOR_ERROR)
    except tools.ConnectorError as e:
        ui.err(f"{spec.id} failed", str(e))
        sys.exit(EXIT_CONNECTOR_ERROR)

    if getattr(args, "json", False):
        _dump(args, result)
        return
    ui.ok("called", spec.id)
    print(result if isinstance(result, str)
          else json.dumps(result, indent=2, default=str))


def _tools_connect(args: argparse.Namespace) -> None:
    """Handles `px0 tools connect`: authorize an app deliberately.

    Authorization on demand covers the common case, but a token that expires or
    is revoked left no way to repair it except running a workflow and watching
    it fail.
    """
    home, config = _ctx()
    try:
        slug = connect_mod.toolkit_slug(args.app)
    except ValueError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)

    if getattr(args, "reconnect", False):
        connect_mod.disconnect_composio_app(home, slug)

    status = connect_mod.connected_account_status(home, slug)
    if status == "ACTIVE":
        ui.ok("already authorized", slug)
        ui.hint(f"start again with `px0 tools connect {slug} --reconnect`")
        return

    try:
        with ui.spinner(f"Preparing {slug} authorization"):
            result = connect_mod.connect_composio_app(home, slug)
    except (ValueError, connect_mod.ComposioUnreachable) as e:
        ui.err(f"could not prepare {slug} authorization", str(e))
        sys.exit(EXIT_CONNECTOR_ERROR)

    ui.step(slug, "open this and complete the consent:", stream=sys.stdout)
    ui.command(result["redirect_url"])
    ui.hint(f"then confirm it with `px0 tools list --status`")


def _tools_disconnect(args: argparse.Namespace) -> None:
    """Handles `px0 tools disconnect`: revoke an app's authorization."""
    home, config = _ctx()
    slug = connect_mod.account_key(args.app)
    if slug not in connect_mod.connected_accounts(home):
        ui.info("not connected", slug)
        return
    users = sorted({wf.id for wf in workflow_mod.load_all(home).values()
                    for t in list(wf.tools) + [i.tool for i in wf.inputs if i.tool]
                    if (spec := tools.resolve(t, home)) and spec.provider in (slug, args.app)})
    if users:
        ui.warn("used by", ", ".join(users))
    if not _confirm(f"Revoke {slug} authorization?", getattr(args, "yes", False)):
        ui.info("kept", slug)
        return
    result = connect_mod.disconnect_composio_app(home, slug)
    ui.ok("disconnected", slug)
    if not result["revoked"]:
        ui.warn("removed locally only", result.get("detail") or
                "Composio still lists the account; revoke it there if it matters")


def _tools_refresh(args: argparse.Namespace) -> None:
    """Handles `px0 tools refresh`: re-read cached tool definitions, or drop them.

    The cache only ever grew, so a tool Composio has since reshaped kept its
    old schema for as long as the store existed.
    """
    home, config = _ctx()
    targets = list(getattr(args, "tool", None) or [])
    if getattr(args, "forget", False):
        removed = catalogue_mod.forget(home, targets or None)
        ui.ok("forgot", f"{removed} cached tool definition(s)")
        ui.hint("a workflow naming a forgotten tool will not validate until "
                "`px0 workflows edit` finds it again")
        return
    try:
        with ui.spinner("Re-reading tool definitions"):
            result = catalogue_mod.refresh(home, targets or None)
    except catalogue_mod.CatalogueError as e:
        ui.err(str(e))
        sys.exit(EXIT_CONNECTOR_ERROR)
    ui.ok("refreshed", f"{result['refreshed']} changed, {result['unchanged']} unchanged")
    for slug in result["dropped"]:
        ui.warn("no longer in the catalogue", slug)
    for failure in result["failed"]:
        ui.err(failure["slug"], failure["error"])


# --- daemon ----------------------------------------------------------------

_LOG_TS_RE = re.compile(r"^(\S+)( .*)$")


def _dim_log(text: str) -> str:
    """Dims the leading timestamp on each log line so the message reads first."""
    if not ui.color_enabled():
        return text
    out = []
    for line in text.splitlines(keepends=True):
        m = _LOG_TS_RE.match(line)
        out.append(f"{ui.faint(m.group(1))}{m.group(2)}" if m else line)
    return "".join(out)


def cmd_daemon(args: argparse.Namespace) -> None:
    """Handles `px0 daemon` subcommands: install, status, start, stop, restart, logs,
    serve. start/restart spawn `python -m px0.cli daemon serve` as a detached child
    process with PX0_HOME set; stop/restart send SIGTERM to the recorded pid."""
    home, config = _ctx()

    if args.daemon_cmd == "install":
        result = daemon_mod.install(home, fallback_cron=args.fallback_cron)
        ui.ok("scheduler installed", result["platform"])
        if result.get("path"):
            ui.kv("wrote", result["path"])
        if result.get("reduced_semantics"):
            ui.warn("reduced semantics", result["reduced_semantics"])
        ui.heading("unit")
        print(ui.dim(result["content"].rstrip()))
        ui.hint("start it with:")
        ui.command(result["start_hint"])
        return

    if args.daemon_cmd == "status":
        st = daemon_mod.status(home, config)
        if args.json:
            _dump(args, st)
            return
        if st.get("alive"):
            ui.ok("daemon running", f"pid {st['pid']}")
        else:
            ui.info("daemon not running")
        upcoming = st.get("next_fires") or st.get("next") or {}
        if isinstance(upcoming, dict) and upcoming:
            ui.heading("next fires")
            width = max(len(k) for k in upcoming)
            for wid, when in sorted(upcoming.items()):
                ui.kv(wid, when, width=width + 1)
        last = st.get("last_fires") or {}
        if isinstance(last, dict) and last:
            ui.heading("last fires")
            width = max(len(k) for k in last)
            for wid, when in sorted(last.items()):
                ui.kv(wid, when, width=width + 1)
        return

    if args.daemon_cmd == "start":
        # detached child inherits current env plus an explicit PX0_HOME so it targets the same store
        subprocess.Popen(
            [sys.executable, "-m", "px0.cli", "daemon", "serve"],
            env={**os.environ, "PX0_HOME": str(home)},
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        ui.ok("daemon starting")
        return

    if args.daemon_cmd == "stop":
        s = daemon_mod.status(home, config)
        if s["pid"] and s["alive"]:
            os.kill(s["pid"], signal.SIGTERM)
            ui.ok("daemon stopped")
        else:
            ui.info("daemon not running")
        return

    if args.daemon_cmd == "restart":
        s = daemon_mod.status(home, config)
        if s["pid"] and s["alive"]:
            os.kill(s["pid"], signal.SIGTERM)
        subprocess.Popen(
            [sys.executable, "-m", "px0.cli", "daemon", "serve"],
            env={**os.environ, "PX0_HOME": str(home)},
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        ui.ok("daemon restarted")
        return

    if args.daemon_cmd == "logs":
        daemon_log_path = runs_mod.resolve_logs_path(config) / "daemon.log"
        if not daemon_log_path.exists():
            ui.info("no daemon log yet", "the daemon writes one once it runs")
            return
        content = daemon_log_path.read_text(encoding="utf-8")
        if content:
            print(_dim_log(content), end="")
        if args.follow:
            ui.hint("following; Ctrl-C to stop")
            try:
                for line in runs_mod.tail_lines(daemon_log_path):
                    print(_dim_log(line), end="", flush=True)
            except KeyboardInterrupt:
                pass
        return

    if args.daemon_cmd == "uninstall":
        result = daemon_mod.uninstall(home)
        if result["stopped"]:
            ui.ok("stopped", "the running daemon")
        for path in result["removed"]:
            ui.ok("removed", path)
        if not result["removed"] and not result["stopped"]:
            ui.info("nothing to remove", "no scheduler unit is installed")
        if result.get("cron_note"):
            ui.hint(result["cron_note"])
        ui.hint("workflows stay in the store; nothing fires until "
                "`px0 daemon install` runs again")
        return

    if args.daemon_cmd == "serve":
        ui.info("scheduler started", "polling every 30s; Ctrl-C to stop")
        daemon_mod.serve(home, config)
        return


# --- runs --------------------------------------------------------------

def _print_runs(config: dict, records: list, as_json: bool) -> None:
    """Prints run records as the aligned plain listing, or as JSON."""
    if as_json:
        print(json.dumps(records, indent=2, default=str), flush=True)
        return
    from px0 import runs_tui
    if not records:
        ui.info("no runs recorded yet")
        return
    widths = runs_tui.column_widths(records)
    for r in records:
        print(runs_tui.format_row(r, widths))


SEVERITY_LABEL = {"problem": "problem", "note": "note"}


def _print_findings(findings: list[dict], *, indent: str = "  ") -> None:
    """Prints a health report's findings, problems first.

    A problem is coloured and a note is not, because the whole point of the
    split is that a reader can skim past the notes. Each finding's fix sits
    under it as a command rather than being folded into the sentence, so it can
    be copied without editing.
    """
    if not findings:
        ui.ok("nothing to report", "these runs look healthy")
        return
    for finding in findings:
        detail = finding["detail"]
        if finding["severity"] == "problem":
            print(f"{indent}{ui.alert('problem')}  {detail}", flush=True)
        else:
            print(f"{indent}{ui.dim('note')}     {detail}", flush=True)
        # `remedy`, not `hint`: a hint opens with a blank line, which is right
        # for a closing next step and wrong under every row of a list.
        if finding.get("fixable"):
            ui.remedy(f"px0 can fix this: {analysis_mod.describe_fix(finding)}")
        elif finding.get("fix"):
            ui.command(f"{indent}{finding['fix']}")


def _print_health(home: Path, report: dict) -> None:
    """The full report for one workflow: what the window held, then what it says."""
    runs = report.get("runs", {})
    ui.heading(f"health {ui.accent(report['workflow'])}")
    rows = [("runs", f"{runs.get('live', 0)} live"
                     + (f", {runs['dry_runs']} dry" if runs.get("dry_runs") else ""))]
    if runs.get("live"):
        rows.append(("outcome", f"{runs.get('success', 0)} ok, {runs.get('failed', 0)} failed"))
    if runs.get("median_seconds") is not None:
        rows.append(("duration", f"{runs['median_seconds']}s median, "
                                 f"{runs['slowest_seconds']}s slowest"))
    if runs.get("cost_measured"):
        cost = f"{runs.get('input_tokens', 0):,} in / {runs.get('output_tokens', 0):,} out"
        if runs.get("cost_usd"):
            cost += f", ${runs['cost_usd']}"
        rows.append(("tokens", cost))
    elif runs.get("estimated_tokens"):
        rows.append(("tokens", f"~{runs['estimated_tokens']:,} (estimated, not measured)"))
    if report.get("tools"):
        rows.append(("tools", ", ".join(report["tools"])))
    width = max(len(label) for label, _ in rows)
    for label, value in rows:
        ui.field(label, value, width=width)
    print(flush=True)
    _print_findings(report.get("findings", []))


def cmd_workflows_health(args: argparse.Namespace) -> None:
    """Handles `px0 workflows health`: what a workflow's own runs say about it.

    Deterministic from end to end -- it reads run records and does arithmetic,
    with no model call and no network -- so it is cheap enough to run whenever,
    and its findings are ones the user can check against the same records.

    `--fix` applies the repairs px0 can make by itself. Those are deliberately
    only ever narrowing (dropping a tool nothing has called) or a timeout the
    records show is too short, each confirmed one at a time and each recorded as
    a versioned change. Anything that would change what a workflow *says* is
    `px0 workflows improve`, which asks a model and then asks the user.
    """
    home, config = _ctx()
    since = _parse_since(args.since) if getattr(args, "since", None) else None

    if not getattr(args, "workflow", None):
        overview = analysis_mod.overview(home, config, since=since)
        if getattr(args, "json", False):
            _dump(args, overview)
            return
        rows = overview["workflows"]
        if not rows:
            ui.info("no workflows yet")
            ui.command("px0 workflows new")
            return
        ui.heading("workflow health")
        width = max(len(r["workflow"]) for r in rows)
        for row in rows:
            if row["problems"]:
                state = ui.alert(f"{row['problems']} problem"
                                 + ("s" if row["problems"] > 1 else ""))
            elif not row["runs"]:
                state = ui.dim("no runs")
            else:
                state = "ok"
            detail = f"{row['runs']} run(s)"
            if row["marked_bad"]:
                detail += f", {row['marked_bad']} marked bad"
            if row["headline"]:
                detail += f" -- {row['headline']}"
            ui.field(row["workflow"], f"{state}  {ui.dim(detail)}", width=width)
        if overview["orphan_runs"]:
            print(flush=True)
            ui.info("runs recorded for workflows no longer in this store",
                    ", ".join(f"{k} ({v})" for k, v in overview["orphan_runs"].items()))
        print(flush=True)
        ui.hint("look at one in detail:")
        ui.command("px0 workflows health <workflow>")
        return

    workflow_id = args.workflow
    report = analysis_mod.health(home, config, workflow_id, since=since)
    if report.get("error"):
        ui.err(report["error"])
        sys.exit(EXIT_USER_ERROR)
    if getattr(args, "json", False):
        _dump(args, report)
        return

    _print_health(home, report)

    repairs = analysis_mod.fixable(report)
    if not getattr(args, "fix", False):
        if repairs:
            # `hint` opens with its own blank line; a second one here left a gap.
            ui.hint(f"{len(repairs)} of these px0 can fix itself:")
            ui.command(f"px0 workflows health {workflow_id} --fix")
        if any(f["severity"] == "problem" and not f.get("fixable")
               for f in report.get("findings", [])):
            ui.hint("for the rest, have px0 revise the workflow from these runs:")
            ui.command(f"px0 workflows improve {workflow_id}")
        return

    if not repairs:
        ui.info("nothing here px0 can fix on its own")
        ui.hint("a change to what the workflow says goes through:")
        ui.command(f"px0 workflows improve {workflow_id}")
        return

    print(flush=True)
    ui.heading("proposed repairs")
    ui.remark("Frontmatter only -- what the workflow says is left alone. Each one "
              "is recorded as a change, so `px0 changes revert` undoes it.")
    chosen = []
    for finding in repairs:
        ui.bullet(analysis_mod.describe_fix(finding))
        ui.field("because", finding["detail"], width=7)
        if _confirm("apply this?", getattr(args, "yes", False)):
            chosen.append(finding)
    if not chosen:
        ui.info("nothing applied")
        return

    result = analysis_mod.apply_fixes(home, config, workflow_id, chosen)
    if not result["changed"]:
        ui.info("nothing applied", "the workflow already says this")
        return
    ui.ok("applied", "; ".join(result["applied"]))
    if result.get("change_id"):
        ui.kv("change", result["change_id"])
        ui.hint("undo it with:")
        ui.command(f"px0 changes revert {result['change_id']}")
    errors = workflow_mod.validate(workflow_mod.load(home, workflow_id), home)
    if errors:
        # Only reachable if a repair collided with something else in the file;
        # better to say so immediately than to let the next scheduled run find it.
        ui.warn("the workflow no longer validates", "; ".join(errors))
        ui.command(f"px0 changes revert {result['change_id']}")


def _print_diff(changes: list[tuple[str, str]], limit: int = 60) -> None:
    """Prints a unified diff, coloured by side and truncated."""
    for marker, text in changes[:limit]:
        if marker == "-":
            print("    " + ui.alert(f"- {text}"), flush=True)
        elif marker == "+":
            print("    " + ui.accent(f"+ {text}"), flush=True)
        elif marker == "@":
            print("    " + ui.dim(text), flush=True)
        else:
            print("    " + ui.dim(f"  {text}"), flush=True)
    if len(changes) > limit:
        ui.hint(f"{len(changes) - limit} more line(s) not shown")


def cmd_workflows_recipes(args: argparse.Namespace) -> None:
    """Handles `px0 workflows recipes`: sentences to start an interview from.

    px0 ships no workflows on purpose, and the cost of that was a blank page:
    the hardest part of describing a job is knowing what sort of thing is
    describable. These are sentences, not files -- picking one answers the
    interview's first question and nothing else, so every workflow in the store
    is still one the user asked for.
    """
    from px0 import starters

    home, config = _ctx()
    existing = set(workflow_mod.load_all(home))
    rows = [(rid, sentence, touches) for rid, sentence, touches in starters.RECIPES]
    if getattr(args, "json", False):
        _dump(args, [{"id": r, "sentence": s_, "touches": t, "built": r in existing}
                     for r, s_, t in rows])
        return

    ui.heading("things people build")
    ui.remark("These are sentences, not workflows. Pick one and px0 asks you "
              "the rest -- or say something else entirely.")
    options = []
    for rid, sentence, touches in rows:
        mark = ui.dim("  (you have this)") if rid in existing else ""
        options.append((sentence, f"{ui.dim(touches)}{mark}"))

    choice = ui.select("Start from one of these", options)
    if choice is None:
        ui.hint("or describe your own:")
        ui.command("px0 workflows new")
        return
    _build_workflow(home, config, rows[choice][1], args, existing_id=None)


def cmd_workflows_replay(args: argparse.Namespace) -> None:
    """Handles `px0 workflows replay`: run a workflow's instructions against
    inputs it already had.

    A workflow run twice compares two different worlds -- the pull requests
    moved, the inbox filled -- so nothing could be held still long enough to
    say whether a change to the wording helped. A fixture holds it still.

    Neither the input tools nor the run's own tools are called here. The
    comparison is about what a workflow *says*; letting it act would both
    change the world and put back the variance the fixture removes.
    """
    home, config = _ctx()
    workflow_id = args.workflow

    if getattr(args, "forget", False):
        removed = replay_mod.forget(home, workflow_id, getattr(args, "run", None))
        ui.ok("forgotten", f"{removed} fixture(s)")
        return

    fixtures = replay_mod.listing(home, workflow_id)
    if getattr(args, "fixtures", False):
        if getattr(args, "json", False):
            _dump(args, fixtures)
            return
        if not fixtures:
            ui.info("nothing captured for this workflow")
            ui.hint("keep what a run reads, so a revision can be compared against it:")
            ui.command(f"px0 workflows show {workflow_id}   # add `capture: true`")
            return
        width = max(len(f["run_id"]) for f in fixtures)
        for fixture in fixtures:
            ui.field(fixture["run_id"],
                     f"{', '.join(fixture['inputs']) or 'no inputs'}  "
                     f"{ui.dim(str(fixture['bytes']) + ' bytes')}", width=width)
        return

    try:
        wf = workflow_mod.load(home, workflow_id)
        run_id = getattr(args, "run", None)
        if not run_id:
            latest = replay_mod.latest_for(home, workflow_id)
            if latest is None:
                raise replay_mod.ReplayError(
                    f"nothing captured for {workflow_id} -- add `capture: true` to it "
                    "and run it once")
            run_id = latest["run_id"]
        fixture = replay_mod.read(home, workflow_id, run_id)
    except (workflow_mod.WorkflowError, replay_mod.ReplayError) as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)

    alternative = None
    if getattr(args, "against", None):
        try:
            alternative = Path(args.against).read_text()
        except OSError as e:
            ui.err(f"could not read {args.against}", str(e))
            sys.exit(EXIT_USER_ERROR)

    ui.heading(f"replaying {ui.accent(workflow_id)}")
    ui.kv("inputs from", run_id)
    ui.kv("resolved", ", ".join(sorted((fixture.get("inputs") or {}).keys())) or "none")

    with ui.spinner("Running the current instructions"):
        before = replay_mod.answer_for(
            config, replay_mod.render_with(home, config, wf, fixture))

    if alternative is None:
        if getattr(args, "json", False):
            _dump(args, {"workflow": workflow_id, "run": run_id, "output": before})
            return
        print(flush=True)
        ui.render_markdown(before)
        ui.hint("compare a rewrite against the same inputs:")
        ui.command(f"px0 workflows replay {workflow_id} --against ./new-body.md")
        return

    with ui.spinner("Running the alternative"):
        after = replay_mod.answer_for(
            config, replay_mod.render_with(home, config, wf, fixture, body=alternative))

    summary = replay_mod.summarize(before, after)
    if getattr(args, "json", False):
        _dump(args, {"workflow": workflow_id, "run": run_id,
                     "before": before, "after": after, "summary": summary})
        return

    print(flush=True)
    if summary["identical"]:
        ui.info("identical output", "the two sets of instructions agree on this input")
        return
    ui.kv("changed", f"+{summary['added']} / -{summary['removed']} lines "
                     f"({summary['churn']:.0%} of the original)")
    print(flush=True)
    _print_diff(replay_mod.diff(before, after))
    print(flush=True)
    ui.remark("One input is one data point. Replay a second captured run before "
              "trusting a difference this shows.")


def _print_proposal(wf, proposal: "improve_mod.Proposal") -> None:
    """Shows a proposal in full before anything is asked of the user.

    The diff comes first and everything else hangs off it, because the revised
    request is the change: tools and guidelines follow from it when the
    workflow is rebuilt.
    """
    ui.heading(f"proposal for {ui.accent(wf.id)}")
    if proposal.diagnosis:
        ui.say(proposal.diagnosis)
        print(flush=True)

    if proposal.changes_request(wf.request):
        print(f"  {ui.dim('request')}", flush=True)
        for marker, text in improve_mod.request_diff(wf.request, proposal.request):
            if marker == "-":
                print("    " + ui.alert(f"- {text}"), flush=True)
            elif marker == "+":
                print("    " + ui.accent(f"+ {text}"), flush=True)
            else:
                print("    " + ui.dim(f"  {text}"), flush=True)
        print(flush=True)
    else:
        ui.kv("request", "unchanged")

    if proposal.tool_drops:
        ui.kv("tools to drop", ", ".join(proposal.tool_drops))
    if proposal.tool_adds:
        ui.kv("tools it argues for", ", ".join(proposal.tool_adds))
        ui.hint("a new tool is never added on this say-so -- the rebuild asks you "
                "to confirm and authorize it, as `px0 workflows new` does")
    for edit in proposal.guideline_edits:
        label = "new guideline" if edit.is_new else "guideline"
        ui.kv(label, edit.path)
        if edit.why:
            ui.field("because", edit.why, width=10)
        for line in edit.addition.strip().splitlines():
            print(f"      {ui.dim(line)}", flush=True)
    if proposal.reasoning:
        print(flush=True)
        ui.field("reasoning", proposal.reasoning, width=10)
    ui.field("confidence", proposal.confidence, width=10)


def _replay_proposal(home: Path, config: dict, wf, proposal, args) -> None:
    """Runs the current instructions and the proposed ones over one fixture.

    Shown before the user is asked to accept anything, because "here is what it
    would have written last Friday" settles a question that no amount of
    reasoning about the records can. Failures here are reported and stepped
    over: a replay is evidence, and being unable to gather it is not a reason
    to refuse the proposal.
    """
    fixture_meta = replay_mod.latest_for(home, wf.id)
    try:
        fixture = replay_mod.read(home, wf.id, fixture_meta["run_id"])
        with ui.spinner("Replaying both against real inputs"):
            before = replay_mod.answer_for(
                config, replay_mod.render_with(home, config, wf, fixture))
            after = replay_mod.answer_for(
                config, replay_mod.render_with(home, config, wf, fixture,
                                                body=proposal.body))
    except (replay_mod.ReplayError, harness.HarnessError) as e:
        ui.warn("could not replay", str(e))
        return

    summary = replay_mod.summarize(before, after)
    ui.heading(f"what changes, on {fixture_meta['run_id']}")
    if summary["identical"]:
        ui.info("identical output",
                "on this input the revision changes nothing at all")
        return
    ui.kv("changed", f"+{summary['added']} / -{summary['removed']} lines "
                     f"({summary['churn']:.0%} of the original)")
    print(flush=True)
    _print_diff(replay_mod.diff(before, after), limit=40)


def cmd_workflows_improve(args: argparse.Namespace) -> None:
    """Handles `px0 workflows improve`: revise a workflow from what its runs did.

    The order matters and is the point of the command. The deterministic report
    is computed and printed first, so the user sees the evidence before they see
    an opinion about it. Only then is the model asked for a revision, and what
    comes back is shown in full -- as a diff against the request the user
    actually wrote -- before anything is applied.

    Applying goes through `_build_workflow`, the same path `px0 workflows edit`
    takes. That is what keeps a revision honest: the tools, inputs, and guideline
    list are regenerated from the new request and confirmed by the user, rather
    than being asserted by the proposal.
    """
    home, config = _ctx()
    workflow_id = args.workflow or _pick_workflow(home, for_stdin=False, verb="improve")
    since = _parse_since(args.since) if getattr(args, "since", None) else None

    try:
        wf, report, case = improve_mod.load_case(home, config, workflow_id, since=since)
    except workflow_mod.WorkflowError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)

    if getattr(args, "show_evidence", False):
        # Exactly what the model is handed, so a user can disagree with a
        # proposal by reading what it was reasoning over.
        _dump(args, case)
        return

    if not getattr(args, "json", False):
        _print_health(home, report)
        print(flush=True)

    if not report["runs"].get("records"):
        ui.err("nothing to learn from", f"{workflow_id} has no runs on record in this window")
        ui.hint("run it a few times first, and mark what it produces:")
        ui.command(f"px0 workflows run {workflow_id}")
        sys.exit(EXIT_USER_ERROR)

    marked = sum(1 for r in case.get("marked_runs", []))
    if not marked and report.get("ok"):
        ui.info("these runs all executed cleanly and none is marked",
                "a proposal would be guessing at what to change")
        ui.hint("tell px0 what was wrong with one, and it has something to work from:")
        ui.command("px0 runs mark <run-id> --bad \"what was wrong\"")
        if not _confirm("ask for a proposal anyway?", getattr(args, "yes", False)):
            return

    try:
        with ui.spinner("Reading the runs"):
            proposal = improve_mod.propose(config, case)
    except improve_mod.ImproveError as e:
        ui.err("no proposal", str(e))
        sys.exit(EXIT_MODEL_ERROR)

    improve_mod.reconcile_guideline_edits(home, proposal.guideline_edits)

    if getattr(args, "json", False):
        _dump(args, {"report": report, "proposal": {
            "diagnosis": proposal.diagnosis, "request": proposal.request,
            "reasoning": proposal.reasoning, "confidence": proposal.confidence,
            "tool_adds": proposal.tool_adds, "tool_drops": proposal.tool_drops,
            "guideline_edits": [dataclasses.asdict(e) for e in proposal.guideline_edits],
            "changes_request": proposal.changes_request(wf.request),
        }})
        return

    _print_proposal(wf, proposal)
    print(flush=True)

    # A proposal argued from records is still an argument. Where the workflow
    # has kept a fixture, it can be settled instead: the same inputs through
    # the old instructions and the new ones, side by side.
    if proposal.body and replay_mod.latest_for(home, workflow_id):
        if getattr(args, "yes", False) or _confirm(
                "compare the two against a run's real inputs?", False):
            _replay_proposal(home, config, wf, proposal, args)
            print(flush=True)

    if proposal.is_empty(wf.request):
        ui.info("no change proposed", "these runs do not support one")
        return

    if getattr(args, "dry_run", False):
        ui.info("dry run", "nothing applied")
        ui.hint("apply it with:")
        ui.command(f"px0 workflows improve {workflow_id}")
        return

    # The guideline edits are settled first and separately. They are the
    # cheaper, more reusable half of most proposals -- a rule about how output
    # should read helps every workflow that carries the file -- and a user who
    # rejects the rebuild should still be able to keep them.
    for edit in proposal.guideline_edits:
        verb = "write" if edit.is_new else "add these rules to"
        if _confirm(f"{verb} {edit.path}?", getattr(args, "yes", False)):
            path = improve_mod.apply_guideline_edit(home, edit)
            ui.ok("guideline", paths.display(path))

    if not proposal.changes_request(wf.request):
        ui.info("the request itself is unchanged", "nothing left to rebuild")
        return

    request = proposal.request
    if not getattr(args, "yes", False):
        choice = ui.select("The revised request", [
            ("rebuild with it", "regenerate the workflow, confirming tools as usual"),
            ("edit it first", "reword the request, then rebuild"),
            ("cancel", "change nothing"),
        ])
        if choice is None or choice == 2:
            ui.info("nothing changed")
            return
        if choice == 1:
            print(flush=True)
            ui.say(request)
            print(flush=True)
            typed = ui.prompt("Your wording (blank to keep the above):\n  ").strip()
            if typed:
                request = typed

    # `already_clarified`: the proposal was written against this workflow's own
    # runs and the user has just approved its wording. Putting them through the
    # clarify interview here would ask about ambiguity that the evidence, not a
    # question, already settled.
    _build_workflow(home, config, request, args, existing_id=workflow_id,
                    already_clarified=True)


def cmd_ask(args: argparse.Namespace) -> None:
    """Handles `px0 ask`: see `commands.cmd_ask`."""
    home, config = _ctx()
    commands_mod.cmd_ask(home, config, args)


def cmd_approvals(args: argparse.Namespace) -> None:
    """Handles `px0 approvals`: see `commands.cmd_approvals`."""
    home, config = _ctx()
    commands_mod.cmd_approvals(home, config, args)


def cmd_inbox(args: argparse.Namespace) -> None:
    """Handles `px0 inbox`: see `commands.cmd_inbox`."""
    home, config = _ctx()
    commands_mod.cmd_inbox(home, config, args)


def cmd_memory(args: argparse.Namespace) -> None:
    """Handles `px0 memory`: see `commands.cmd_memory`."""
    home, config = _ctx()
    commands_mod.cmd_memory(home, config, args)


def cmd_runs(args: argparse.Namespace) -> None:
    """Handles `px0 runs` subcommands: list, show, output, rerun, logs -- inspecting
    and replaying past workflow run records."""
    home, config = _ctx()

    if args.runs_cmd is None:
        from px0 import runs_tui
        try:
            runs_tui.run(home, config)
        except runs_tui.NoTerminalError:
            # Piped or scripted: print the listing instead of failing, so
            # `px0 runs | head` behaves like `px0 runs list`.
            _print_runs(config, runs_mod.list_records(config), as_json=args.json)
        return

    if args.runs_cmd == "list":
        if getattr(args, "running", False):
            running = runs_mod.list_running(home)
            if args.json:
                _dump(args, running)
                return
            if not running:
                ui.info("nothing running")
                return
            for entry in running:
                ui.kv(entry["id"], f"{entry['workflow_id']}  pid {entry['pid']}  "
                                    f"since {entry['started_at']}")
            ui.hint("stop one with `px0 runs cancel <run-id>`")
            return
        since = _parse_since(args.since) if args.since else None
        records = runs_mod.list_records(config, workflow=args.workflow, failed=args.failed, since=since)
        _print_runs(config, records, as_json=args.json)
        return

    if args.runs_cmd == "mark":
        # Each verdict flag carries its own optional note, so both
        # `--bad "it missed X"` and `--bad --note "it missed X"` work.
        good, bad = getattr(args, "good", None), getattr(args, "bad", None)
        note = (getattr(args, "note", None) or good or bad or "").strip()
        verdict = None
        if good is not None:
            verdict = "good"
        elif bad is not None:
            verdict = "bad"
        elif not getattr(args, "clear", False):
            ui.err("say what you thought of it", "pass --good or --bad, or --clear")
            ui.command('px0 runs mark <run-id> --bad "what was wrong"')
            sys.exit(EXIT_USER_ERROR)
        try:
            record = runs_mod.mark(config, args.run_id, verdict, note=note)
        except (FileNotFoundError, runs_mod.RunIdError, ValueError) as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if verdict is None:
            ui.ok("cleared", f"{args.run_id} is unmarked again")
            return
        ui.ok(f"marked {verdict}", args.run_id)
        if verdict == "bad" and not note:
            # A bare "bad" says a run was wrong. A note says how, which is the
            # part an improvement pass can actually act on.
            ui.hint("a sentence on what was wrong makes this worth much more:")
            ui.command(f'px0 runs mark {args.run_id} --bad "it missed X"')
        wf_id = record.get("workflow_id")
        if wf_id and verdict == "bad":
            ui.hint("when a few of these have piled up:")
            ui.command(f"px0 workflows improve {wf_id}")
        return

    if args.runs_cmd == "events":
        try:
            events = runs_mod.read_events(config, args.run_id)
        except runs_mod.RunIdError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if args.json:
            _dump(args, events)
            return
        if not events:
            ui.info(f"no event stream for {args.run_id}",
                    "the run predates event logging, or retention removed it")
            ui.hint("logs.events controls whether new runs write one")
            return
        for event in events:
            fields = {k: v for k, v in event.items()
                      if k not in ("ts", "run", "kind") and v is not None}
            stamp = str(event.get("ts", ""))[11:19]
            detail = "  ".join(f"{k}={json.dumps(v, default=str)}"
                               if not isinstance(v, str) else f"{k}={v}"
                               for k, v in fields.items())
            print(f"{ui.dim(stamp)}  {event.get('kind', '?'):<20} {detail}"[:2000],
                  flush=True)
        return

    if args.runs_cmd == "stats":
        since = _parse_since(args.since) if args.since else None
        overview = analysis_mod.overview(home, config, since=since)
        if args.json:
            _dump(args, overview)
            return
        rows = overview["workflows"]
        if not rows:
            ui.info("no workflows yet")
            return
        ui.heading("runs by workflow")
        width = max(len(r["workflow"]) for r in rows)
        for row in rows:
            parts = [f"{row['runs']} run(s)"]
            if row["failed"]:
                parts.append(ui.alert(f"{row['failed']} failed"))
            if row["marked_bad"]:
                parts.append(f"{row['marked_bad']} marked bad")
            if row["median_seconds"] is not None:
                parts.append(f"{row['median_seconds']}s median")
            ui.field(row["workflow"], "  ".join(parts), width=width)
        print(flush=True)
        ui.kv("total runs", overview["total_runs"])
        if overview["problems"]:
            ui.hint(f"{overview['problems']} problem(s) across these workflows:")
            ui.command("px0 workflows health")
        return

    if args.runs_cmd == "cancel":
        result = runs_mod.cancel(home, args.run_id, force=getattr(args, "force", False))
        if result["cancelled"]:
            ui.ok("signalled", f"{args.run_id} ({result['signal']} to pid {result['pid']})")
            ui.hint("the run records itself as failed once it stops")
            return
        ui.err(f"not cancelled: {result.get('detail', 'unknown')}")
        ui.hint("see what is in flight with `px0 runs list --running`")
        sys.exit(EXIT_USER_ERROR)

    if args.runs_cmd == "prune":
        if args.dry_run:
            records = runs_mod.list_records(config)
            ui.info("retention applies to", f"{len(records)} record(s)")
            ui.kv("logs kept for", f"{config_mod.get(config, 'logs.retention_days', 14)} days")
            ui.kv("failed logs kept for",
                  f"{config_mod.get(config, 'logs.retention_days_failed', 60)} days")
            ui.kv("records kept for",
                  f"{config_mod.get(config, 'logs.record_retention_days', 365)} days")
            ui.hint("runs that called a write tool are never pruned")
            return
        removed = runs_mod.apply_retention(config)
        ui.ok("pruned", f"{removed['logs']} log(s), {removed['records']} record(s)")
        return

    if args.runs_cmd == "open":
        record = runs_mod.read_record(config, args.run_id)
        output = record.get("output") or {}
        if output.get("target") == "file" and output.get("path"):
            path = home / output["path"]
            if not path.exists():
                ui.err(f"the file this run wrote is gone: {output['path']}")
                sys.exit(EXIT_USER_ERROR)
            ui.kv("file", output["path"])
            print()
            print(path.read_text(), end="")
            return
        text = output.get("text")
        if text:
            print(text, end="" if text.endswith("\n") else "\n")
            return
        ui.info("this run produced no output file", record.get("outcome", ""))
        return

    if args.runs_cmd == "show":
        record = runs_mod.read_record(config, args.run_id)
        _dump(args, record)
        return

    if args.runs_cmd == "output":
        record = runs_mod.read_record(config, args.run_id)
        print(record.get("output", {}).get("text", ""))
        return

    if args.runs_cmd == "rerun":
        record = runs_mod.read_record(config, args.run_id)
        wf_id = record.get("workflow_id")
        if not wf_id:
            ui.err("nothing to rerun", "this run was an ask, not a workflow")
            sys.exit(EXIT_USER_ERROR)
        # A rehearsal reruns as a rehearsal: replaying a --dry-run record as a
        # live run would fire the write tools the original deliberately stubbed.
        was_dry = bool(record.get("dry_run"))
        if was_dry:
            ui.info("original was a dry run", "rerunning with --dry-run; "
                    "run it directly to execute for real")
        with ui.spinner(f"Rerunning {wf_id}"):
            new_record = runner.run(home, config, wf_id, trigger="manual", dry_run=was_dry)
        # The same block `workflows run` prints: a rerun is a run, and the new
        # run id is in the `run` row rather than folded into a sentence.
        _print_run_outcome(home, wf_id, new_record)
        if new_record.get("output", {}).get("target") == "stdout":
            print(new_record["output"].get("text", ""))
        return

    if args.runs_cmd == "logs":
        log_path = runs_mod.log_path(config, args.run_id)
        if not log_path.exists():
            ui.info(f"no log for {args.run_id}", "retention may have removed it")
            return
        content = runs_mod.read_raw_log(config, args.run_id)
        if content:
            print(content, end="")
        if args.follow:
            ui.hint("following until the run finishes; Ctrl-C to stop")
            import time
            try:
                with open(log_path, "r", encoding="utf-8") as f:
                    f.seek(0, 2)
                    while True:
                        line = f.readline()
                        if line:
                            print(line, end="", flush=True)
                        else:
                            try:
                                rec = runs_mod.read_record(config, args.run_id)
                                if rec.get("outcome") in ("success", "failed"):
                                    line = f.readline()
                                    while line:
                                        print(line, end="", flush=True)
                                        line = f.readline()
                                    break
                            except FileNotFoundError:
                                pass
                            time.sleep(1.0)
            except KeyboardInterrupt:
                pass
        return


# --- brain -----------------------------------------------------------

def _records_source(path: Path) -> bool:
    """Whether a brain file records where it came from, and so can be re-fetched."""
    try:
        header, _ = brain_mod.read_header(path)
    except Exception:
        return False
    return bool(header.get("source"))


def cmd_brain(args: argparse.Namespace) -> None:
    """Handles `px0 brain add` and `refresh`: ingests a source (URL, file, etc.)
    into the brain or re-fetches an already-ingested source."""
    home, config = _ctx()

    if args.brain_cmd == "add" and getattr(args, "from_file", None):
        sources = brain_mod.read_sources(Path(args.from_file))
        if not sources:
            ui.err(f"{args.from_file} lists no sources")
            sys.exit(EXIT_USER_ERROR)
        with ui.spinner(f"Ingesting {len(sources)} source(s)"):
            result = brain_mod.add_many(home, config, sources, to=args.to)
        ui.ok("ingested", f"{len(result['added'])} of {len(sources)}")
        for failure in result["failed"]:
            ui.err(failure["source"], failure["error"])
        if result.get("reindexed") is not None:
            ui.hint(f"index rebuilt: {result['reindexed']} passages")
        if result["failed"]:
            sys.exit(EXIT_USER_ERROR)
        return

    if args.brain_cmd == "refresh" and (getattr(args, "all", False) or getattr(args, "stale", False)):
        if args.all:
            targets = [p for p in brain_mod.list_files(home, config)]
            targets = [p for p in targets if _records_source(p)]
        else:
            targets = brain_mod.stale(home, config, days=args.days or 30)
        if not targets:
            ui.info("nothing to refresh", "no file records a source that has gone stale")
            return
        with ui.spinner(f"Re-fetching {len(targets)} file(s)"):
            result = brain_mod.refresh_many(home, config, targets)
        ui.ok("refreshed", f"{len(result['refreshed'])} of {len(targets)}")
        for failure in result["failed"]:
            ui.err(Path(failure["path"]).name, failure["error"])
        if result.get("reindexed") is not None:
            ui.hint(f"index rebuilt: {result['reindexed']} passages")
        return

    if args.brain_cmd == "refresh" and not args.path:
        ui.err("nothing named to refresh")
        ui.hint("name a file, or pass --all or --stale")
        sys.exit(EXIT_USER_ERROR)

    if args.brain_cmd == "add":
        try:
            with ui.spinner(f"Ingesting {args.source}"):
                result = brain_mod.add(home, config, args.source, to=args.to)
        except brain_mod.IngestError as e:
            ui.err("ingest failed", str(e))
            sys.exit(EXIT_USER_ERROR)
        if result.is_stub:
            ui.warn("ingested as metadata only", str(result.path), stream=sys.stdout)
            ui.hint("no transcript published yet; retry later with:")
            ui.command(f"px0 brain refresh {result.path}")
        else:
            ui.ok("ingested", str(result.path))
        return

    if args.brain_cmd == "refresh":
        try:
            with ui.spinner(f"Refreshing {args.path}"):
                result = brain_mod.refresh(home, config, Path(args.path))
        except brain_mod.IngestError as e:
            ui.err("refresh failed", str(e))
            sys.exit(EXIT_USER_ERROR)
        ui.ok("refreshed", str(result.path))
        return


# --- guidelines ----------------------------------------------------------

def cmd_guidelines_file(args: argparse.Namespace) -> None:
    """Handles `px0 guidelines edit`, `show`, and `rm`.

    There is no `new`: a guideline is written by `px0 workflows new`, when the
    build finds the workflow leaning on a convention the store has no file for.
    These are the operations on what is already there, with the store's version
    history kept in step -- which is the whole reason to go through px0 rather
    than an editor and `rm`.
    """
    home, config = _ctx()
    verb = args.guidelines_cmd
    try:
        path = authoring.guideline_path(home, args.name)
    except authoring.AuthoringError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    rel = str(path.relative_to(home))

    if not path.exists():
        ui.err(f"no guideline named {args.name}")
        ui.hint("see what there is with `px0 guidelines list`")
        sys.exit(EXIT_USER_ERROR)

    if verb == "show":
        print(path.read_text(), end="")
        return

    if verb == "edit":
        before = path.read_text()
        if not _open_in_editor(path):
            ui.info("no editor", f"set $EDITOR, or edit {path} directly")
            return
        after = path.read_text()
        if after == before:
            ui.info("unchanged", rel)
            return
        authoring.write_file(home, path, after, evidence="edited via cli")
        ui.ok("saved", rel)
        return

    if verb == "rm":
        users = [wf.id for wf in workflow_mod.load_all(home).values()
                 if path.name in wf.guidelines]
        if users:
            ui.warn("in use by", ", ".join(sorted(users)))
            ui.hint("those workflows will fail validation until they stop naming it")
        if not _confirm(f"Remove {rel}?", getattr(args, "yes", False)):
            ui.info("kept", rel)
            return
        result = authoring.remove_file(home, path, evidence="removed via cli")
        ui.ok("removed", rel)
        if result.get("change_id"):
            ui.hint(f"undo with `px0 changes revert {result['change_id']}`")
        return


def cmd_guidelines(args: argparse.Namespace) -> None:
    """Handles `px0 guidelines log`: one claim's edit history.

    A `## ` heading in a guideline is a claim with its own id and version chain,
    so a rule that changed can be read back on its own rather than as a diff of
    the whole file.
    """
    home, config = _ctx()
    entries = claims.guidelines_log(home, args.claim_id)
    _dump(args, entries)


# --- changes -------------------------------------------------------------

def cmd_changes(args: argparse.Namespace) -> None:
    """Handles `px0 changes` subcommands: list, show, revert.

    A change is the unit of undo: one atomic write across the store, reverted
    whole. `show` prints a per-file diff against what each file held before.
    """
    home, config = _ctx()

    if args.changes_cmd == "list":
        since = _parse_since(args.since) if args.since else None
        changes = versioning.list_changes(home, since=since, actor=args.actor)
        if args.json:
            _dump(args, changes)
            return
        if not changes:
            ui.info("no changes recorded")
            return
        for c in changes:
            files = ui.dim(f"{len(c['files'])} file(s)")
            print(f"  {c['id']}  {c['actor']:<14} {ui.dim(c['timestamp'])}  {files}")
        return

    if args.changes_cmd == "show":
        _dump(args, versioning.show_change(home, args.change_id))
        return

    if args.changes_cmd == "revert":
        new_id = versioning.revert_change(home, args.change_id, "user:manual")
        if new_id:
            ui.ok("reverted", new_id)
        else:
            ui.info("nothing to revert")
        return


# --- brain search / why / store / update / version / doctor ---------

def cmd_reindex(args: argparse.Namespace) -> None:
    """Handles `px0 brain reindex`: rebuilds the retrieval index from disk."""
    home, config = _ctx()
    with ui.spinner("Reindexing brain"):
        count = retrieval.reindex(home, config)
    ui.ok("reindexed", f"{count} passages")


def cmd_search(args: argparse.Namespace) -> None:
    """Handles `px0 brain search`: prints the top-k passages matching the query."""
    home, config = _ctx()
    k = args.k if args.k is not None else config_mod.get(config, "retrieval.k_default", 5)
    kind = getattr(args, "kind", None)
    with ui.spinner(f"Searching for {args.query!r}"):
        passages = retrieval.retrieve(home, config, args.query, k=k, kind=kind)
    if args.json:
        # dataclasses.asdict, not the raw Passage: `_dump`'s default=str would
        # otherwise stringify each one into its own repr, making --json a list
        # of unparseable strings rather than objects a script can read.
        _dump(args, [dataclasses.asdict(p) for p in passages])
        return
    if not passages:
        ui.info("no matches")
        if kind:
            # Otherwise a --kind that matches nothing looks like an empty brain.
            ui.hint(f"nothing of kind {kind!r} matched; files px0 did not write "
                    f"carry no kind and are never matched by --kind")
        ui.hint("if the index looks stale: px0 brain reindex")
        return
    for p in passages:
        print(f"  {p.path}{ui.dim('#' + p.anchor)}  {ui.dim(str(round(p.score, 3)))}")
        print(f"    {ui.dim(p.text[:200].strip())}")


def cmd_why(args: argparse.Namespace) -> None:
    """Handles `px0 runs why`: prints the provenance chain explaining how a run
    reached the result it did."""
    home, config = _ctx()
    try:
        result = provenance.why(config, args.target_id)
    except provenance.WhyError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    _dump(args, result)


def cmd_store(args: argparse.Namespace) -> None:
    """Handles the `px0 store` group: export, import, path, and verify."""
    if args.store_cmd == "import":
        # An import can create the store, so it must not require one first.
        home = paths.store_home()
        try:
            with ui.spinner(f"Importing {args.dir}"):
                report = store_mod.import_store(home, Path(args.dir),
                                                force=args.force, merge=args.merge)
        except store_mod.StoreError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        ui.ok("imported", f"{report['files']} file(s) into {home}")
        for name in report["imported"]:
            ui.bullet(name)
        if report["skipped_files"]:
            ui.hint(f"{report['skipped_files']} file(s) already here were kept; "
                    "pass --force to let the import win")
        for skipped in report["skipped"]:
            ui.hint(f"kept: {skipped}")
        ui.hint("credentials are never in an export -- set the Composio key with "
                "`px0 config composio <key>`")
        return

    home, config = _ctx()

    if args.store_cmd == "export":
        with ui.spinner(f"Exporting store to {args.dir}"):
            store_mod.export(home, Path(args.dir))
        ui.ok("exported", f"{args.dir}  (credentials excluded)")
        ui.hint(f"load it elsewhere with `px0 store import {args.dir}`")
        return

    if args.store_cmd == "sync":
        from px0 import sync as sync_mod

        remote = Path(args.dir).expanduser()
        try:
            result = sync_mod.sync(home, remote,
                                   dry_run=getattr(args, "dry_run", False),
                                   pull_only=getattr(args, "pull", False),
                                   push_only=getattr(args, "push", False))
        except sync_mod.SyncError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if getattr(args, "json", False):
            _dump(args, result)
            return

        if not result["applied"]:
            ui.heading("what a sync would do")
            ui.kv("send", f"{len(result['push'])} file(s)")
            ui.kv("take", f"{len(result['pull'])} file(s)")
            ui.kv("conflict", f"{len(result['conflict'])} file(s)")
            for rel in result["conflict"][:10]:
                ui.field("both changed", rel, width=12)
            return

        ui.ok("synced", f"{len(result['pushed'])} sent, {len(result['pulled'])} taken")
        if result["conflicts"]:
            # Never merged and never chosen between: two versions are two
            # decisions, and only the person who made them knows which one
            # they meant.
            print(flush=True)
            ui.warn(f"{len(result['conflicts'])} file(s) changed in both places",
                    "nothing was overwritten")
            for entry in result["conflicts"]:
                ui.field(entry["path"], f"theirs kept as {entry['theirs']}", width=0)
            ui.hint("open both, keep what you meant, and delete the other")
        hazard = sync_mod.hazard(home)
        if hazard:
            ui.warn(f"this store looks like it is inside {hazard}",
                    "px0's history is a SQLite database, which a folder-syncing "
                    "tool will corrupt if two machines write it")
            ui.hint("move the store out of it and use this command instead:")
            ui.command("px0 config path")
        return

    if args.store_cmd == "path":
        if getattr(args, "json", False):
            _dump(args, {
                "home": str(home),
                "config": str(paths.config_path(home)),
                "workflows": str(paths.workflows_dir(home)),
                "guidelines": str(paths.guidelines_dir(home)),
                "brain": str(retrieval.brain_path(home, config)),
                "output": str(paths.output_dir(home)),
                "tools": str(paths.tools_dir(home)),
                "state": str(paths.state_dir(home)),
                "logs": str(runs_mod.resolve_logs_path(config)),
            })
            return
        print(home)
        return

    if args.store_cmd == "verify":
        with ui.spinner("Checking the store"):
            report = store_mod.verify(home)
        if getattr(args, "json", False):
            _dump(args, report)
            if not report["ok"]:
                sys.exit(EXIT_INTEGRITY_ERROR)
            return
        if report["ok"]:
            ui.ok("store is consistent", f"{report['checks']} check(s), nothing to fix")
            return
        for problem in report["problems"]:
            ui.err(problem["kind"], problem["detail"])
            ui.remedy(problem["fix"])
        sys.exit(EXIT_INTEGRITY_ERROR)


def cmd_config(args: argparse.Namespace) -> None:
    """Handles `px0 config` subcommands: list (every recognized key with its
    current value, default, type, and allowed choices), get <key>, set <key>
    <value>, and model (an interactive harness/model picker, see
    _select_model)."""
    home, config = _ctx()

    if args.config_cmd == "list":
        entries = config_mod.describe(config)
        if args.json:
            _dump(args, entries)
            return
        for e in entries:
            # a value that differs from the default is the interesting one: accent it
            rendered = repr(e["value"])
            if e["value"] != e["default"]:
                rendered = ui.accent(rendered) + ui.dim(f"  (default: {e['default']!r})")
            choices = ui.dim(f"  choices={e['choices']}") if e["choices"] else ""
            print(f"{e['key']} = {rendered}  {ui.dim('[' + e['type'] + ']')}{choices}")
            print(f"    {ui.dim(e['help'])}")
        return

    if args.config_cmd == "get":
        try:
            value = config_mod.get_key(config, args.key)
        except ValueError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if args.json:
            _dump(args, {args.key: value})
        else:
            print(value)
        return

    if args.config_cmd == "set":
        try:
            value = config_mod.set_key(config, args.key, args.value)
        except ValueError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        config_mod.save(paths.config_path(home), config)
        ui.ok(args.key, f"= {value!r}")
        if args.key == "brain.path":
            _report_brain_path(home, config)
        return

    if args.config_cmd == "unset":
        try:
            value = config_mod.unset_key(config, args.key)
        except ValueError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        config_mod.save(paths.config_path(home), config)
        ui.ok(args.key, f"back to its default: {value!r}")
        return

    if args.config_cmd == "edit":
        path = paths.config_path(home)
        before = path.read_text()
        if not _open_in_editor(path):
            ui.info("no editor", f"set $EDITOR, or edit {path} directly")
            return
        after = path.read_text()
        if after == before:
            ui.info("unchanged", str(path))
            return
        try:
            config_mod.load(path)
        except Exception as e:
            ui.err("config.toml no longer parses", str(e))
            # The edit is already on disk and the parse error names the line,
            # so the fix is another pass over the same file.
            ui.remedy(f"fix the syntax in {path}, then `px0 config edit` to check it")
            sys.exit(EXIT_USER_ERROR)
        ui.ok("saved", str(path))
        return

    if args.config_cmd == "path":
        if getattr(args, "json", False):
            _dump(args, {"config": str(paths.config_path(home))})
            return
        print(paths.config_path(home))
        return

    if args.config_cmd == "model":
        _select_model(home, config)
        return

    if args.config_cmd == "composio":
        _set_composio_key(home, args.key)
        return


def _set_composio_key(home: Path, key: str | None) -> None:
    """Stores the Composio API key after verifying it against the live API.

    This is the whole of connection setup: individual apps authorize themselves
    when a workflow first needs them, so there is nothing else to configure
    per service.
    """
    cfg = config_mod.load(paths.config_path(home))
    existing = cfg.get("connectors", {}).get("composio_api_key") or os.environ.get("COMPOSIO_API_KEY")

    if not key:
        label = "Composio API key"
        if existing:
            label += f" {ui.dim(f'[{_mask_key(existing)}, Enter to keep]')}"
        try:
            key = ui.prompt(label + ": ")
        except EOFError:
            key = ""
        if not key:
            if existing:
                ui.info("kept the existing key")
                return
            ui.err("a Composio API key is required")
            sys.exit(EXIT_USER_ERROR)

    try:
        with ui.spinner("Verifying Composio API key"):
            result = connect_mod.setup_composio(home, key)
    except connect_mod.ComposioUnreachable as e:
        ui.err(str(e).strip())
        sys.exit(EXIT_USER_ERROR)
    except ValueError as e:
        ui.err(str(e).strip())
        sys.exit(EXIT_USER_ERROR)

    if result.get("ca_bundle"):
        ui.info("TLS is intercepted on this network",
                f"verifying against {result['ca_bundle']}")
    ui.ok("Composio API key stored")
    ui.hint("apps authorize themselves when a workflow first needs them; see:")
    ui.command("px0 tools list")


def _select_model(home: Path, config: dict) -> None:
    """Interactive `px0 config model`: lists known harnesses with their PATH
    status, lets the user pick one (or type a custom command) and an
    optional model name, then verifies the resulting harness_cmd actually
    responds before saving -- surfacing that CLI's own auth error (with a
    hint from harness.AUTH_HINTS) rather than guessing why it failed.

    px0 has no direct-API backend: it never asks for or stores a provider
    API key itself. Authentication is entirely the chosen harness's own
    (an env var it reads, or its own interactive login), same as every
    other px0-invoked run of it."""
    installed = harness.installed_harnesses()
    names = sorted(harness.KNOWN_HARNESSES)

    current = config_mod.get(config, "model.harness_cmd", "")
    ui.kv("current", repr(current))
    ui.heading("harnesses")
    for i, name in enumerate(names, 1):
        cmd = ui.dim(harness.KNOWN_HARNESSES[name])
        mark = ui.dim("installed") if installed[name] else ui.paint("not on PATH", "179")
        print(f"  {ui.accent(str(i) + '.')} {name:<10} {cmd}  {mark}")
    print(f"  {ui.accent(str(len(names) + 1) + '.')} custom command")
    print()

    choice = ui.prompt(f"pick [1-{len(names) + 1}]: ")
    if not choice.isdigit() or not (1 <= int(choice) <= len(names) + 1):
        ui.info("cancelled")
        return
    idx = int(choice)

    if idx == len(names) + 1:
        base_cmd = ui.prompt("harness command "
                             + ui.dim("(the prompt is appended as the final argument, "
                                     "or piped to stdin if that gets too long)") + ": ")
        if not base_cmd:
            ui.info("cancelled")
            return
        name = None
    else:
        name = names[idx - 1]
        base_cmd = harness.KNOWN_HARNESSES[name]

    model = ui.prompt("model name " + ui.dim("(blank = harness default)") + ": ")
    harness_cmd = harness.with_model(base_cmd, model or None)

    trial_config = copy.deepcopy(config)
    trial_config.setdefault("model", {})["harness_cmd"] = harness_cmd
    try:
        with ui.spinner(f"Verifying {harness_cmd}"):
            harness.invoke(trial_config, "reply with the single word: ok", timeout=20)
        ui.ok("harness responded")
    except harness.HarnessError as e:
        ui.err("verification failed", str(e))
        if name and name in harness.AUTH_HINTS:
            ui.info("hint", harness.AUTH_HINTS[name], stream=sys.stderr)
        if ui.prompt("save anyway? [y/N] ").lower() != "y":
            ui.info("not saved")
            return

    config.setdefault("model", {})["harness_cmd"] = harness_cmd
    config_mod.save(paths.config_path(home), config)
    ui.ok("model.harness_cmd", harness_cmd)


def cmd_update(args: argparse.Namespace) -> None:
    """Handles `px0 update`: switches the update channel, checks for/applies an
    update, or rolls back."""
    home, config = _ctx()
    if args.rollback:
        try:
            with ui.spinner("Rolling back"):
                update_mod.rollback(home, config)
        except update_mod.UpdateError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        return
    if args.channel:
        config.setdefault("update", {})["channel"] = args.channel
        config_mod.save(paths.config_path(home), config)
        ui.ok("channel", args.channel)
        return
    try:
        label = "Checking for updates" if args.check else "Updating px0"
        with ui.spinner(label):
            result = update_mod.run_update(home, config, check_only=args.check)
    except update_mod.UpdateError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)

    if args.check:
        available = result.get("available_version")
        if available and result.get("update_available"):
            ui.info(f"{available} available", f"on channel {result.get('channel', 'stable')}")
            ui.hint("install it with:")
            ui.command("px0 update")
        else:
            ui.ok("up to date", result.get("current_version", ""))
        return

    ui.ok(result.get("message", "updated"))
    summary = result.get("doctor_summary") or {}
    if summary and not summary.get("all_ok", True):
        broken = [n for n, c in summary.get("checks", {}).items() if not c["ok"]]
        ui.warn("post-update checks failed", ", ".join(broken))
        ui.hint("see the detail with: px0 doctor")


def cmd_version(args: argparse.Namespace) -> None:
    """Handles `px0 version`: prints version/build info. Works even without an
    initialized store (require_init=False)."""
    home, config = _ctx(require_init=False)
    info = update_mod.version_info(home, config)
    if args.json:
        _dump(args, info)
        return
    width = max(len(k) for k in info) + 1
    for k, v in info.items():
        ui.kv(k, v, width=width)


def cmd_doctor(args: argparse.Namespace) -> None:
    """Handles `px0 doctor`: runs integrity/health checks and prints pass/fail per
    check. Exits with EXIT_INTEGRITY_ERROR if any check failed."""
    home, config = _ctx()
    if args.json:
        report = doctor_mod.run(home, config, quick=args.quick)
        _dump(args, report)
        sys.exit(0 if report["all_ok"] else EXIT_INTEGRITY_ERROR)

    with ui.spinner("Running checks"):
        report = doctor_mod.run(home, config, quick=args.quick)

    checks = report["checks"]
    width = max(len(n) for n in checks) if checks else 0
    for name, check in checks.items():
        (ui.ok if check["ok"] else ui.err)(
            name, check["detail"], width=width, stream=sys.stdout
        )
        # The fix goes straight under the line that failed, not in a footer: a
        # red line the reader can't act on is the whole complaint about doctor.
        if not check["ok"] and check.get("fix"):
            ui.remedy(check["fix"])

    failed = [n for n, c in checks.items() if not c["ok"]]
    if failed:
        ui.hint(f"{len(failed)} check(s) failed: {', '.join(failed)}")
    sys.exit(0 if report["all_ok"] else EXIT_INTEGRITY_ERROR)


def cmd_uninstall(args: argparse.Namespace) -> None:
    """Handles `px0 uninstall`: stops the daemon, removes the scheduler unit,
    deletes the entire store, and uninstalls the px0 package itself.

    Irreversible: every workflow, guideline, brain file, credential, and run
    record lives under the store home, so removing it removes px0's data in
    full. `install.sh --uninstall` stops here at the package and scheduler
    unit, deliberately leaving the store for the user to remove by hand; this
    command is the other half. Each of the three things below is its own
    irreversible action, so each is confirmed (and can be declined) on its
    own instead of one blanket yes covering all of them -- reported live as
    it happens rather than previewed upfront, the way `install.sh` narrates
    its steps: a tick the moment there's nothing to do, or the moment the
    confirmed action is actually done.
    """
    home = paths.store_home()
    store_exists = home.exists()
    mechanism = update_mod.detect_install_mechanism(home)
    assume_yes = getattr(args, "yes", False)

    kept = []

    if store_exists:
        if _confirm("Stop the daemon and remove its scheduler unit?", assume_yes):
            result = daemon_mod.uninstall(home)
            if result["stopped"]:
                ui.ok("stopped", "the running daemon")
            for path in result["removed"]:
                ui.ok("removed", path)
            if result.get("cron_note"):
                ui.hint(result["cron_note"])
        else:
            kept.append("daemon / scheduler unit")

        if _confirm(f"Delete the entire store at {home}? This cannot be undone.", assume_yes):
            shutil.rmtree(home)
            ui.ok("deleted", str(home))
        else:
            kept.append("the store")
    else:
        ui.ok("no store to remove", str(home))

    if mechanism:
        if _confirm(f"Uninstall the px0 package (via {mechanism})?", assume_yes):
            cmd = ["pipx", "uninstall", "px0"] if mechanism == "pipx" else \
                [sys.executable, "-m", "pip", "uninstall", "-y", "px0"]
            try:
                result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
            except OSError as e:
                ui.err("could not uninstall the px0 package", str(e))
                ui.hint(f"remove it yourself: {' '.join(cmd)}")
                sys.exit(EXIT_USER_ERROR)
            if result.returncode != 0:
                ui.err("could not uninstall the px0 package", result.stderr.strip()[:200])
                ui.hint(f"remove it yourself: {' '.join(cmd)}")
                sys.exit(EXIT_USER_ERROR)
            ui.ok("uninstalled", "the px0 package")
        else:
            kept.append("the px0 package")

    if kept:
        ui.info("kept", ", ".join(kept))
    else:
        ui.ok("px0 is uninstalled")


# --- argument parser -----------------------------------------------------

# --- status, completion, mcp ---------------------------------------------

def cmd_status(args: argparse.Namespace) -> None:
    """Handles `px0 status`: is anything broken, in one screen.

    Assembles what mattered from `daemon status`, `runs list --failed`, and
    `doctor`, and stays cheap enough to run whenever you wonder.
    """
    home, config = _ctx()
    report = status_mod.collect(home, config, hours=getattr(args, "hours", None) or
                                status_mod.RECENT_HOURS)

    if getattr(args, "json", False):
        _dump(args, report)
        if not report["ok"]:
            sys.exit(EXIT_USER_ERROR)
        return

    daemon_state = "running" if report["daemon"]["alive"] else "not running"
    role = ui.ok if report["daemon"]["alive"] or not report["workflows"]["scheduled"] else ui.warn
    role("scheduler", f"{daemon_state} ({report['daemon']['platform']})")

    wf = report["workflows"]
    ui.kv("workflows", f"{wf['total']} total, {len(wf['scheduled'])} scheduled"
                        + (f", {len(wf['watched'])} watched" if wf["watched"] else "")
                        + (f", {len(wf['disabled'])} disabled" if wf["disabled"] else ""))

    for wf_id, when in sorted((report["next_fires"] or {}).items())[:5]:
        ui.kv(f"next  {wf_id}", str(when))

    runs = report["runs"]
    ui.kv(f"runs (last {report['hours']}h)",
          f"{runs['recent']} run, {runs['failed']} failed"
          + (f", {len(runs['running'])} in flight" if runs["running"] else ""))
    for entry in runs["running"]:
        ui.kv(f"running  {entry['id']}", entry["workflow_id"])
    for failure in runs["failures"]:
        ui.err(f"{failure['workflow']}  {failure['id']}", failure["error"][:120])

    # The two queues that are waiting on the person rather than on px0.
    if report.get("inbox"):
        ui.kv("inbox", f"{report['inbox']} unread")
    if report.get("approvals"):
        ui.kv("approvals", f"{report['approvals']} waiting")

    if report["problems"]:
        print()
        for problem in report["problems"]:
            ui.warn(problem["detail"])
            ui.remedy(problem["fix"])
        sys.exit(EXIT_USER_ERROR)
    ui.ok("nothing needs attention")


def cmd_completion(args: argparse.Namespace) -> None:
    """Handles `px0 completion <shell>`: print the completion script to install."""
    try:
        print(completion_mod.script(args.shell), end="")
    except ValueError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)


def cmd_mcp(args: argparse.Namespace) -> None:
    """Handles `px0 mcp serve`: speak MCP over stdin and stdout.

    Writes stay behind --allow-runs: an agent should not be able to fire a
    workflow that posts to Slack because it was curious what px0 could do.
    """
    home, config = _ctx()
    if args.mcp_cmd != "serve":
        ui.err(f"unknown mcp command: {args.mcp_cmd}")
        sys.exit(EXIT_USER_ERROR)
    # Progress output would corrupt the protocol stream, which is stdout.
    ui.set_color(False)
    scope = None
    scope_path = getattr(args, "scope", None)
    if scope_path:
        # Started by a run, to serve that run and nothing else. A bad scope
        # file must not degrade into the full server, which would expose the
        # brain and every workflow to a client that asked for one workflow's
        # tools.
        try:
            scope = json.loads(Path(scope_path).read_text())
        except (OSError, json.JSONDecodeError) as e:
            ui.err(f"unreadable run scope: {e}")
            sys.exit(EXIT_USER_ERROR)
    mcp_mod.serve(home, config, allow_runs=getattr(args, "allow_runs", False),
                  scope=scope)


def _run_completion(argv: list[str]) -> None:
    """Answers the hidden `--complete` used by the generated shell scripts.

    Silent by design: completion runs on every tab press, so a broken store
    must produce no output rather than an error in the middle of a prompt.
    """
    try:
        home = paths.store_home()
        for candidate in completion_mod.complete(build_parser(), argv, home=home):
            print(candidate)
    except Exception:
        pass


def build_parser() -> argparse.ArgumentParser:
    """The px0 argparse tree, built against this module's own `cmd_*` handlers.

    The tree itself lives in `px0.parser`; this wrapper stays here because it is
    the name callers and tests reach for.
    """
    return parser_mod.build(sys.modules[__name__])


def _notify_update() -> None:
    """Runs the once-a-day cached update check and prints the result, if any.

    Called after every successful command. Best-effort and silent on any
    failure -- an update nudge is never worth breaking or delaying the
    command that triggered it (`update_mod.maybe_check` itself never raises,
    but this still guards against a bad store/config on the way in).
    """
    try:
        home = paths.store_home()
        if not store_mod.is_initialized(home):
            return
        config = config_mod.load(paths.config_path(home))
        result = update_mod.maybe_check(home, config)
    except Exception:
        return

    if not result:
        return
    if result["kind"] == "available":
        ui.info(f"{result['available_version']} available",
                f"on channel {result.get('channel', 'stable')}")
        ui.hint("install it with:")
        ui.command("px0 update")
    elif result["kind"] == "installed":
        ui.ok(result["result"].get("message", "auto-updated"))
    elif result["kind"] == "install_failed":
        ui.warn("auto-update failed", result.get("error", ""))
        ui.hint("try it by hand with:")
        ui.command("px0 update")


def main(argv: list[str] | None = None) -> None:
    """CLI entry point: parses args, dispatches to the selected subcommand's handler,
    and translates known exception types into the appropriate exit code."""
    # Line-buffer stdout so it stays in order against the spinner's stderr writes.
    # Without this, piping px0 anywhere reorders content against progress lines,
    # because stdout is block-buffered while stderr is not.
    try:
        sys.stdout.reconfigure(line_buffering=True)
    except (AttributeError, ValueError):
        pass  # not a real stream (captured in tests, or already closed)

    raw = list(sys.argv[1:] if argv is None else argv)
    if raw and raw[0] == "--complete":
        _run_completion(raw[1:])
        return

    parser = build_parser()
    args = parser.parse_args(argv)
    if getattr(args, "no_color", False):
        ui.set_color(False)
    elif getattr(args, "json", False):
        ui.set_color(False)  # --json output is data; never decorate it
    try:
        args.func(args)
        if args.func is not cmd_update and not getattr(args, "json", False):
            _notify_update()
    # each except maps a failure category to its own exit code so callers/scripts can branch on it
    except tools.ConnectorError as e:
        ui.err(str(e))
        sys.exit(EXIT_CONNECTOR_ERROR)
    except catalogue_mod.CatalogueError as e:
        ui.err(str(e))
        sys.exit(EXIT_CONNECTOR_ERROR)
    except (authoring.AuthoringError, store_mod.StoreError,
            localtools.LocalToolError) as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    except harness.HarnessError as e:
        ui.err(str(e))
        sys.exit(EXIT_MODEL_ERROR)
    except workflow_mod.WorkflowError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    except (FileNotFoundError, ValueError) as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    except KeyboardInterrupt:
        # the spinner, if any, has already cleared its line
        print(file=sys.stderr)
        ui.warn("interrupted")
        sys.exit(EXIT_USER_ERROR)
    except EOFError:
        # A prompt hit end-of-input: piped stdin, CI, or a `yes |` that ran out.
        # Interactive commands say what to pass instead of dying on a traceback.
        print(file=sys.stderr)
        ui.err("this command needs an answer and stdin is exhausted")
        ui.hint("run it interactively, or pass --yes to accept the defaults")
        sys.exit(EXIT_USER_ERROR)


if __name__ == "__main__":
    main()
