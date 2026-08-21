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
import signal
import subprocess
import sys
from datetime import datetime
from pathlib import Path
from typing import NamedTuple

from px0 import (
    ask as ask_mod,
    authoring,
    catalogue as catalogue_mod,
    completion as completion_mod,
    builder as builder_mod,
    claims,
    config as config_mod,
    connect as connect_mod,
    credentials as creds_mod,
    daemon as daemon_mod,
    localtools,
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


def _read_text_arg(path_value: str) -> str:
    """Reads a file given on the command line, with a message that names it."""
    path = Path(path_value).expanduser()
    try:
        return path.read_text()
    except OSError as e:
        ui.err(f"cannot read {path}", str(e))
        sys.exit(EXIT_USER_ERROR)


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

    ui.hint("try next:")
    ui.command("px0 doctor")
    ui.command('px0 workflows new "describe what you want"')

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
                answer = ui.prompt(f"{question}\n  ")
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


def _intake_loop(config: dict) -> str:
    """Interviews the user into a workflow request, when they named none.

    `px0 workflows new` with no description opens this. px0 asks for one thing
    at a time until every field a workflow file has to pin down is settled --
    the job, what it reads, where the result goes, when it runs, and what makes
    the output right -- then writes the request back for approval.

    The loop is the model's to drive: it sees the transcript and decides what is
    still missing, so answering "the razorpay/api repo, every Friday" in one
    breath skips the two questions that would have asked for those separately.
    A blank answer ends it early and the request is written from what there is,
    because the way out of an interview should always be Enter.
    """
    ui.heading("new workflow")
    ui.hint("answer in your own words; press Enter on a blank line to stop")

    transcript: list[tuple[str, str]] = []
    question = _OPENING_QUESTION
    wrap_up = False

    for _ in range(builder_mod.MAX_INTAKE_ROUNDS):
        try:
            answer = ui.prompt(f"{question}\n  ")
        except EOFError:
            print(file=sys.stderr)
            answer = ""
        if not answer:
            if not transcript:
                ui.err("nothing to build")
                ui.hint('describe it inline instead: px0 workflows new "..."')
                sys.exit(EXIT_USER_ERROR)
            wrap_up = True
        else:
            transcript.append((question, answer))

        try:
            with ui.spinner("Working out what else it needs"):
                step = builder_mod.intake(config, transcript, wrap_up=wrap_up)
        except (builder_mod.BuilderError, harness.HarnessError) as e:
            # The interview is the only way in when no description was given,
            # so a failed turn cannot fall through to a build with nothing.
            ui.err("could not continue the interview", str(e).strip())
            ui.hint('describe it inline instead: px0 workflows new "..."')
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
        ui.heading("the request")
        print(description, flush=True)
        choice = ui.prompt("Build this? [Y/edit/n] ").lower()
        if choice in ("n", "no"):
            ui.info("cancelled")
            sys.exit(0)
        if choice not in ("e", "edit"):
            return description
        edited = ui.paragraph("Rewrite it (blank keeps it as it is):")
        if edited:
            description = edited


def _describe_tool(spec_or_tool, width: int) -> str:
    """One aligned line for a tool being proposed: id, access, description."""
    is_destructive = getattr(spec_or_tool, "is_destructive", False)
    if is_destructive:
        access = ui.paint("destructive", "167")
    elif spec_or_tool.is_write:
        access = ui.paint("write", "179")
    else:
        access = ui.dim("read ")
    desc = getattr(spec_or_tool, "description", "")
    return f"  {access:<11}  {spec_or_tool.id.ljust(width)}  {ui.dim(desc[:70])}"


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
    width = max(len(t.id) for t in selected)
    ui.heading(f"tools selected ({len(selected)})")
    for i, tool in enumerate(selected, 1):
        # numbered so the drop-list below can refer to them
        print(f"  {ui.accent(str(i) + '.')}{_describe_tool(tool, width)}")

    writes = [t for t in selected if t.is_write]
    destructive = [t for t in selected if t.is_destructive]
    if destructive:
        ui.warn("destructive tools proposed",
                ", ".join(t.slug for t in destructive), stream=sys.stdout)
    elif writes:
        ui.warn("this workflow could change things outside px0",
                ", ".join(t.slug for t in writes), stream=sys.stdout)

    if assume_yes:
        return selected

    ui.hint("Enter accepts all; list numbers to drop (e.g. 2,3); n aborts")
    answer = ui.prompt("keep all? ").lower()

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
        ui.info("would be saved as", f"guidelines/{proposal.path}", stream=sys.stdout)
        print()
        print(content, flush=True)
        choice = ui.prompt("Keep it? [Y/again/n] ").lower()
        if choice in ("a", "again"):
            continue
        if choice in ("n", "no"):
            ui.info("skipped", stream=sys.stdout)
            return None

        dest = builder_mod.save_guideline(home, proposal.path, content)
        ui.ok("wrote", str(dest))
        ui.hint(f"reword it any time with `px0 guidelines edit {Path(proposal.path).stem}`")
        return proposal.path


def _description_arg(args: argparse.Namespace) -> str | None:
    """The workflow description, from the argument or from --from-file.

    None when neither was given, which `px0 workflows new` answers by asking.

    A carefully written description is a paragraph, and a paragraph does not
    want to survive shell quoting.
    """
    from_file = getattr(args, "from_file", None)
    if from_file:
        text = _read_text_arg(from_file).strip()
        if not text:
            ui.err(f"{from_file} is empty")
            sys.exit(EXIT_USER_ERROR)
        return text
    return args.description


def cmd_new(args: argparse.Namespace) -> None:
    """Handles `px0 workflows new`: builds a workflow from a sentence, or from an
    interview when no sentence was given.

    A description is the fast path for someone who already knows what to type.
    Without one, px0 asks -- which is the honest default, because "what should
    this read, and when does it run" are questions the user has to answer either
    way and a blank prompt is a worse place to answer them than a question is.
    """
    home, config = _ctx()
    description = _description_arg(args)
    if description is not None:
        _build_workflow(home, config, description, args, existing_id=None)
        return

    if getattr(args, "yes", False) or not sys.stdin.isatty():
        # Nobody to interview: --yes answers no questions, and a pipe has no
        # keystrokes to read.
        ui.err("no description given, and nothing to ask")
        ui.hint("describe it inline, or from a file:")
        ui.command('px0 workflows new "every Friday, post a PR digest to #eng"')
        ui.command("px0 workflows new --from-file ./request.txt")
        sys.exit(EXIT_USER_ERROR)

    # The interview settles what `--no-clarify` would otherwise re-ask.
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
    description = ui.paragraph("New instructions (blank to keep the current ones):").strip()
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
    except (builder_mod.BuilderError, harness.HarnessError) as e:
        ui.err(str(e))
        sys.exit(EXIT_MODEL_ERROR)

    ui.heading("plan")
    print(json.dumps(plan.raw, indent=2), flush=True)

    issues = builder_mod.check_feasibility(plan, home)
    if issues:
        ui.heading("feasibility")
        for i in issues:
            ui.err(i, stream=sys.stdout)
        ui.hint("cannot proceed until these are resolved")
        sys.exit(EXIT_USER_ERROR)

    writes = builder_mod.write_tools_named(plan, home)
    if writes:
        ui.heading("write access")
        ui.warn("this workflow would be granted write tools",
                ", ".join(writes), stream=sys.stdout)

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
        if ui.prompt(f"{verb} this workflow? [y/N] ").lower() != "y":
            ui.info("cancelled")
            return

    if existing_id:
        workflow_id = existing_id
    else:
        # slugify the description into a default workflow id, capped to 40 chars
        default_id = re.sub(r"[^a-z0-9-]+", "-",
                            plan.description.lower()).strip("-")[:40] or "new-workflow"
        if getattr(args, "id", None):
            workflow_id = args.id
        elif assume_yes:
            workflow_id = default_id
        else:
            workflow_id = ui.prompt(f"workflow id {ui.dim(f'[{default_id}]')}: ") or default_id

    guidelines = builder_mod.choose_guidelines(home, description)
    # After the commit to write, not before: nobody should be asked to author
    # a convention for a workflow they are about to cancel.
    guidelines += _author_guidelines(home, config, description, plan, guidelines, assume_yes)

    content = builder_mod.render_workflow_file(workflow_id, plan, guidelines, description)
    dest = builder_mod.save_workflow(home, workflow_id, content)

    ui.heading(f"{'updated' if existing_id else 'created'} {workflow_id}")
    ui.ok("workflow", str(dest))
    if guidelines:
        ui.ok("guidelines", ", ".join(f"guidelines/{g}" for g in guidelines))
        ui.hint("each is inlined verbatim into every run of this workflow")
    if plan.trigger.get("schedule"):
        ui.ok("schedule", plan.trigger["schedule"])
    if selected:
        ui.ok("tools", ", ".join(t.id for t in selected))
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
        ui.hint('describe one with `px0 workflows new "..."`')
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

    output_override = {"target": args.output} if args.output else None
    trigger = "late" if args.late_scheduled_at else "manual"

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
        ui.err("run failed", str(e))
        sys.exit(EXIT_USER_ERROR)

    if not args.quiet:
        out = record.get("output", {})
        detail = record["id"]
        if out.get("target") == "file":
            detail += f" -> {out.get('path')}"
        role = ui.ok if record["outcome"] == "success" else ui.err
        role(f"{workflow_id} {record['outcome']}", detail, stream=sys.stderr)

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
        print(f"  {wid.ljust(width)}  {ui.dim(wf.description)}")
    # Broken files are skipped by load_all, so they have to be reported here or
    # they vanish silently -- the whole point of skipping was to keep the rest
    # usable, not to hide the breakage.
    errors = workflow_mod.load_errors(home)
    for e in errors:
        ui.warn("unreadable workflow", e, stream=sys.stdout)
    if not workflows and not errors:
        ui.hint('none yet -- describe one with `px0 workflows new "..."`')


def _first_rule(path: Path) -> str:
    """A guideline's first `## ` heading, as the one-line detail beside its name.

    The headings are the rules, so the first one says more about what the file
    holds than a byte count or a claim tally would.
    """
    try:
        for line in path.read_text().splitlines():
            if line.startswith("## "):
                return line[3:].strip()
    except OSError:
        return "unreadable"
    return ""


def _print_guidelines(home: Path, heading: bool) -> None:
    """Every guideline, numbered the way `workflows run` numbers its picker.

    Same rows as the picker on purpose: guidelines are a short list you scan and
    then name, so it should look like the other short list px0 shows you.
    """
    base = paths.guidelines_dir(home)
    files = sorted(base.rglob("*.md"))
    if heading:
        ui.heading(f"guidelines {ui.dim(f'({len(files)})')}")
    ui.numbered([(str(p.relative_to(base)), _first_rule(p)) for p in files])
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
    print(wf.path.read_text(), end="")


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


def cmd_workflows_rm(args: argparse.Namespace) -> None:
    """Handles `px0 workflows rm`: remove a workflow, keeping its history."""
    home, config = _ctx()
    path = authoring.workflow_path(home, args.workflow)
    if not path.exists():
        ui.err(f"no such workflow: {args.workflow}")
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
    result = authoring.remove_file(home, path, evidence=f"px0 workflows rm {args.workflow}")
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
            "`px0 workflows new \"...\"`")


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
        role = ui.ok if new_record["outcome"] == "success" else ui.err
        role(f"reran as {new_record['id']}", new_record["outcome"], stream=sys.stderr)
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
    backend = config_mod.get(config, "retrieval.backend", "local")
    with ui.spinner(f"Reindexing brain ({backend})"):
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
                             + ui.dim("(the prompt is appended as the final argument)") + ": ")
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
    mcp_mod.serve(home, config, allow_runs=getattr(args, "allow_runs", False))


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
