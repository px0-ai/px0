"""px0's CLI surface. Argument parsing and interactive glue live here;
every subcommand delegates to the module that actually does the work."""

import argparse
import copy
import json
import os
import re
import signal
import subprocess
import sys
from datetime import datetime
from pathlib import Path

from px0 import (
    ask as ask_mod,
    catalogue as catalogue_mod,
    builder as builder_mod,
    claims,
    config as config_mod,
    connect as connect_mod,
    consolidate as consolidate_mod,
    credentials as creds_mod,
    daemon as daemon_mod,
    doctor as doctor_mod,
    harness,
    knowledge as knowledge_mod,
    paths,
    proposals as proposals_mod,
    provenance,
    retrieval,
    runner,
    runs as runs_mod,
    skills as skills_mod,
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


def _ctx(require_init: bool = True) -> tuple[Path, dict]:
    """Resolves the store home and loads its config for a subcommand.

    Exits the process with EXIT_USER_ERROR if the store hasn't been
    initialized and require_init is True.
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

    import shutil
    if not shutil.which("npx"):
        ui.warn("npx not found on PATH",
                "Node.js is required for `px0 skills` -- https://nodejs.org")

    ui.hint("try next:")
    ui.command("px0 doctor")
    ui.command('px0 new "describe what you want"')


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


def _authorize_toolkits(home: Path, toolkits: set[str], assume_yes: bool) -> list[str]:
    """Authorizes each toolkit the plan needs that isn't authorized yet, asking
    first. Returns the toolkits still waiting on a browser consent.

    Nothing is aborted over a pending consent: the workflow file is valid either
    way, and making the user re-run `px0 new` would repeat the clarify, search,
    selection, and planning passes just to reach the same file.
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
        return []

    ui.heading(f"authorization needed ({len(pending)})")
    for toolkit, status in pending:
        ui.warn(toolkit, _AUTH_STATE.get(status, status.lower()), stream=sys.stdout)

    if not assume_yes:
        names = ", ".join(t for t, _ in pending)
        if ui.prompt(f"Start authorization for {names}? [Y/n] ").lower() in ("n", "no"):
            ui.info("skipped; the first run that needs one will offer a link")
            return [t for t, _ in pending]

    waiting = []
    for toolkit, _ in pending:
        try:
            with ui.spinner(f"Preparing {toolkit} authorization"):
                res = connect_mod.connect_composio_app(home, toolkit)
        except ValueError as e:
            ui.err(toolkit, str(e).strip(), stream=sys.stdout)
            waiting.append(toolkit)
            continue
        ui.step(toolkit, "open this and complete the consent:", stream=sys.stdout)
        ui.command(res["redirect_url"])
        waiting.append(toolkit)
    return waiting


def cmd_new(args: argparse.Namespace) -> None:
    """Handles `px0 new`: clarifies the request, searches Composio's catalogue for
    the tools it needs, confirms them with the user, authorizes what isn't
    authorized yet, then plans and writes the workflow file."""
    home, config = _ctx()
    description = args.description

    try:
        qa = _clarify_loop(config, description, skip=args.yes or args.no_clarify)

        selected = [] if args.no_discover else _discover_tools(home, config, description, qa)
        if selected:
            selected = _confirm_tools(home, selected, args.yes)
            # Remember them before planning: the plan, its validation, and every
            # later run all resolve these ids out of the store's catalogue cache.
            catalogue_mod.remember(home, selected)

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

    waiting = _authorize_toolkits(home, builder_mod.required_connections(plan, home), args.yes)

    if not args.yes:
        if ui.prompt("Generate this workflow? [y/N] ").lower() != "y":
            ui.info("cancelled")
            return

    # slugify the description into a default workflow id, capped to 40 chars
    default_id = re.sub(r"[^a-z0-9-]+", "-", plan.description.lower()).strip("-")[:40] or "new-workflow"
    if args.id:
        workflow_id = args.id
    elif args.yes:
        workflow_id = default_id
    else:
        workflow_id = ui.prompt(f"workflow id {ui.dim(f'[{default_id}]')}: ") or default_id

    guidelines = builder_mod.choose_guidelines(home, description)

    content = builder_mod.render_workflow_file(workflow_id, plan, guidelines)
    dest = builder_mod.save_workflow(home, workflow_id, content)

    ui.heading(f"created {workflow_id}")
    ui.ok("workflow", str(dest))
    if guidelines:
        ui.ok("guidelines", ", ".join(guidelines))
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
    ui.command(f"px0 run {workflow_id} --dry-run")


def cmd_run(args: argparse.Namespace) -> None:
    """Handles `px0 run`: executes a workflow with inputs collected from --stdin and
    --input KEY=VALUE flags, then prints the outcome and, depending on --json/--quiet
    and the workflow's output target, the run's output text."""
    home, config = _ctx()
    cli_inputs: dict = {}
    if args.stdin:
        cli_inputs["_stdin"] = sys.stdin.read()
    for kv in args.input or []:
        key, _, value = kv.partition("=")
        cli_inputs[key] = value

    output_override = {"target": args.output} if args.output else None
    trigger = "late" if args.late_scheduled_at else "manual"

    try:
        with ui.spinner(f"Running {args.workflow}", quiet=args.quiet or args.json):
            record = runner.run(
                home, config, args.workflow, trigger=trigger, cli_inputs=cli_inputs,
                dry_run=args.dry_run, output_override=output_override,
                late_scheduled_at=args.late_scheduled_at,
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
        role(f"{args.workflow} {record['outcome']}", detail, stream=sys.stderr)

    if args.json:
        _dump(args, record)
    elif record.get("output", {}).get("target") == "stdout":
        print(record["output"].get("text", ""))


def cmd_ask(args: argparse.Namespace) -> None:
    """Handles `px0 ask`: answers a question via retrieval over guidelines/knowledge
    and prints the answer, optionally followed by source passages with --sources."""
    home, config = _ctx()
    try:
        with ui.spinner("Searching knowledge"):
            result = ask_mod.ask(home, config, args.question, k=args.k)
    except ask_mod.AskError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    print(result["answer"])
    if args.sources:
        ui.heading("sources")
        for p in result["passages"]:
            ui.bullet(f"{p.path}{ui.dim('#' + p.anchor)}")


def cmd_list(args: argparse.Namespace) -> None:
    """Handles `px0 list`: prints workflows, guidelines, and/or knowledge file paths.
    With no kind given, prints all three sections; otherwise prints just that section."""
    home, config = _ctx()
    kind = args.kind

    if kind in (None, "workflows"):
        workflows = sorted(workflow_mod.load_all(home).items())
        if kind is None:
            ui.heading(f"workflows {ui.dim(f'({len(workflows)})')}")
        width = max((len(w) for w, _ in workflows), default=0)
        for wid, wf in workflows:
            print(f"  {wid.ljust(width)}  {ui.dim(wf.description)}")
        if not workflows and kind is None:
            ui.hint('none yet -- describe one with `px0 new "..."`')

    if kind in (None, "guidelines"):
        base = paths.guidelines_dir(home)
        files = sorted(base.rglob("*.md"))
        if kind is None:
            ui.heading(f"guidelines {ui.dim(f'({len(files)})')}")
        for p in files:
            print(f"  {p.relative_to(base)}")

    if kind in (None, "knowledge"):
        base = retrieval.knowledge_path(home, config)
        files = sorted(base.rglob("*.md")) if base.exists() else []
        if kind is None:
            ui.heading(f"knowledge {ui.dim(f'({len(files)})')}")
        for p in files:
            print(f"  {p.relative_to(base)}")


# --- tools ---------------------------------------------------------------

# Composio's status vocabulary, in words that say what to do about it.
_AUTH_STATE = {
    "NOT_CONNECTED": "not authorized",
    "INITIATED": "consent pending",
    "FAILED": "authorization failed",
    "NOT_FOUND": "authorization gone",
}


def cmd_tools(args: argparse.Namespace) -> None:
    """Handles `px0 tools list`: prints each available tool with a read/write marker,
    its id, provider, description, and parameters, optionally filtered by service."""
    # Include tools discovered by `px0 new` when there is a store to read them
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
    unready = sorted(p for p, st in statuses.items() if st != "ACTIVE")
    if unready:
        ui.hint(f"not authorized yet: {', '.join(unready)} -- a workflow that needs "
                "one prints its authorization URL on the first run")


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

    if args.daemon_cmd == "serve":
        ui.info("scheduler started", "polling every 30s; Ctrl-C to stop")
        daemon_mod.serve(home, config)
        return


# --- runs --------------------------------------------------------------

def cmd_runs(args: argparse.Namespace) -> None:
    """Handles `px0 runs` subcommands: list, show, output, rerun, logs -- inspecting
    and replaying past workflow run records."""
    home, config = _ctx()

    if args.runs_cmd is None:
        from px0 import runs_tui
        runs_tui.run(home, config)
        return

    if args.runs_cmd == "list":
        since = _parse_since(args.since) if args.since else None
        records = runs_mod.list_records(config, workflow=args.workflow, failed=args.failed, since=since)
        if args.json:
            _dump(args, records)
            return
        from px0 import runs_tui
        if not records:
            ui.info("no runs recorded yet")
            return
        widths = runs_tui.column_widths(records)
        for r in records:
            print(runs_tui.format_row(r, widths))
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
        with ui.spinner(f"Rerunning {wf_id}"):
            new_record = runner.run(home, config, wf_id, trigger="manual")
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


# --- knowledge -----------------------------------------------------------

def cmd_knowledge(args: argparse.Namespace) -> None:
    """Handles `px0 knowledge add` and `refresh`: ingests a source (URL, file, etc.)
    into the knowledge library or re-fetches an already-ingested source."""
    home, config = _ctx()

    if args.knowledge_cmd == "add":
        try:
            with ui.spinner(f"Ingesting {args.source}"):
                result = knowledge_mod.add(
                    home, config, args.source, to=args.to, no_propose=args.no_propose
                )
        except knowledge_mod.IngestError as e:
            ui.err("ingest failed", str(e))
            sys.exit(EXIT_USER_ERROR)
        if result.is_stub:
            ui.warn("ingested as metadata only", str(result.path), stream=sys.stdout)
            ui.hint("no transcript published yet; retry later with:")
            ui.command(f"px0 knowledge refresh {result.path}")
        else:
            ui.ok("ingested", str(result.path))
        return

    if args.knowledge_cmd == "refresh":
        try:
            with ui.spinner(f"Refreshing {args.path}"):
                result = knowledge_mod.refresh(home, config, Path(args.path))
        except knowledge_mod.IngestError as e:
            ui.err("refresh failed", str(e))
            sys.exit(EXIT_USER_ERROR)
        ui.ok("refreshed", str(result.path))
        return


# --- guidelines / consolidate --------------------------------------------

def _interactive_review(home: Path, proposal_list: list, non_interactive: bool) -> None:
    """Walks the user through each pending proposal, prompting accept/edit/dismiss
    unless non_interactive is set (in which case proposals are only printed, not
    acted on). Accepted/edited proposals are applied together as one change."""
    if not proposal_list:
        ui.info("nothing pending")
        return
    decisions = []
    total = len(proposal_list)
    for n, p in enumerate(proposal_list, 1):
        ui.heading(f"{p.target_file} {ui.dim(f'({n}/{total})')}")
        ui.kv(p.action, p.claim)
        print()
        print(p.body)
        ui.kv("evidence", ui.dim(f"{p.evidence_source}#{p.evidence_anchor}"))
        if non_interactive:
            continue
        choice = ui.prompt("accept / edit / dismiss? [a/e/d] ").lower()
        if choice == "a":
            decisions.append({"proposal": p, "edited_body": None})
        elif choice == "e":
            ui.hint("type the replacement body; a blank line finishes")
            lines = []
            while True:
                line = input()
                if not line:
                    break  # blank line terminates multi-line entry
                lines.append(line)
            decisions.append({"proposal": p, "edited_body": "\n".join(lines)})
        else:
            proposals_mod.dismiss(home, p.id)
            ui.info("dismissed", p.claim)

    if decisions:
        change_id = proposals_mod.apply_many(home, "user:manual", decisions)
        print()
        ui.ok(f"applied {len(decisions)} change(s)", change_id)
    elif not non_interactive:
        print()
        ui.info("nothing accepted")


def cmd_guidelines(args: argparse.Namespace) -> None:
    """Handles `px0 guidelines` subcommands: review (pending proposals), log (claim
    history), revert (roll a claim back to an earlier version), and alias
    (list/link/unlink claim aliases)."""
    home, config = _ctx()

    if args.guidelines_cmd == "review":
        _interactive_review(home, proposals_mod.list_proposals(home), args.list_only)
        return

    if args.guidelines_cmd == "log":
        entries = claims.guidelines_log(home, args.claim_id)
        _dump(args, entries)
        return

    if args.guidelines_cmd == "revert":
        try:
            change_id = claims.guidelines_revert(home, args.claim_id, args.to, "user:manual")
        except ValueError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        ui.ok("reverted", change_id)
        return

    if args.guidelines_cmd == "alias":
        if args.alias_cmd == "list":
            aliases = claims.list_aliases(home)
            if not aliases:
                ui.info("no claim aliases")
                return
            for a in aliases:
                print(f"  {a['old_claim']} {ui.faint('->')} {a['new_claim']}")
        elif args.alias_cmd == "link":
            claims.add_alias(home, args.old, args.new)
            ui.ok("linked", f"{args.old} -> {args.new}")
        elif args.alias_cmd == "unlink":
            claims.remove_alias(home, args.old)
            ui.ok("unlinked", args.old)
        return


def cmd_consolidate(args: argparse.Namespace) -> None:
    """Handles `px0 consolidate`: builds a consolidation session (pending proposals,
    decayed claims, contradictions, unreferenced guideline files), prints a summary,
    then runs the same interactive review flow as `guidelines review`."""
    home, config = _ctx()
    with ui.spinner("Building consolidation session"):
        session = consolidate_mod.build_session(home, config)

    deferred = session["proposals_overflow"]
    ui.heading("consolidation")
    ui.ok(f"{len(session['proposals'])} proposal(s) pending",
          f"{deferred} deferred to the next session" if deferred else "")
    for c in session["decayed_claims"]:
        ui.warn(f"decayed: {c['claim']}",
                f"{c['days_since_reinforced']}d since last touched", stream=sys.stdout)
    for c in session["contradictions"]:
        ui.warn(f"contradiction: {c}", stream=sys.stdout)
    for f in session["unreferenced_files"]:
        ui.info(f"unreferenced: guidelines/{f}", "no workflow lists it", stream=sys.stdout)

    _interactive_review(home, session["proposals"], args.list_only)


# --- versions / changes --------------------------------------------------

def _parse_version_ref(ref: str) -> tuple[str, int]:
    """Splits a `<path>@v<N>` reference into (path, version number)."""
    if "@v" not in ref:
        raise ValueError(f"expected <path>@v<N>, got {ref!r}")
    path, v = ref.rsplit("@v", 1)
    return path, int(v)


def _color_diff(text: str) -> str:
    """Colours a unified diff the way a pager would: adds green, removes red."""
    if not ui.color_enabled():
        return text
    out = []
    for line in text.splitlines(keepends=True):
        if line.startswith("+++") or line.startswith("---"):
            out.append(ui.strong(line.rstrip("\n")) + "\n")
        elif line.startswith("@@"):
            out.append(ui.dim(line.rstrip("\n")) + "\n")
        elif line.startswith("+"):
            out.append(ui.paint(line.rstrip("\n"), "71") + "\n")
        elif line.startswith("-"):
            out.append(ui.paint(line.rstrip("\n"), "167") + "\n")
        else:
            out.append(line)
    return "".join(out)


def cmd_versions(args: argparse.Namespace) -> None:
    """Handles `px0 versions` subcommands: list, show, diff, revert, prune -- the
    per-file version history maintained by the tool's own versioning system."""
    home, config = _ctx()

    if args.versions_cmd == "list":
        entries = versioning.list_versions(home, args.path)
        if args.json:
            _dump(args, entries)
            return
        if not entries:
            ui.info(f"no versions recorded for {args.path}")
            return
        for v in entries:
            tag = ui.dim("  (deleted)") if v["deleted"] else ""
            print(f"  {ui.accent('v' + str(v['version'])):<6} {v['actor']:<14} "
                  f"{ui.dim(v['change_id'])}  {ui.dim(v['timestamp'])}{tag}")
        return

    if args.versions_cmd == "show":
        path, v = _parse_version_ref(args.ref)
        content = versioning.show_version(home, path, v)
        print(content.decode() if content is not None else "(deleted at this version)")
        return

    if args.versions_cmd == "diff":
        print(_color_diff(versioning.diff_versions(home, args.path, args.v1, args.v2)))
        return

    if args.versions_cmd == "revert":
        change_id = versioning.revert_file(home, args.path, args.to, "user:manual")
        if change_id:
            ui.ok("reverted", change_id)
        else:
            ui.info("already at that content")
        return

    if args.versions_cmd == "prune":
        result = versioning.prune(home, config, dry_run=args.dry_run)
        _dump(args, result)
        return


def cmd_changes(args: argparse.Namespace) -> None:
    """Handles `px0 changes` subcommands: list, show, revert -- multi-file changesets
    (as opposed to `versions`, which tracks a single file's history)."""
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


# --- search / skills / why / store / update / version / doctor -----------

def cmd_search(args: argparse.Namespace) -> None:
    """Handles `px0 search`: rebuilds the retrieval index when the query is literally
    "reindex", otherwise retrieves and prints the top-k matching passages."""
    home, config = _ctx()
    if args.query == "reindex":
        backend = config_mod.get(config, "retrieval.backend", "local")
        with ui.spinner(f"Reindexing knowledge ({backend})"):
            count = retrieval.reindex(home, config)
        ui.ok("reindexed", f"{count} passages")
        return
    with ui.spinner(f"Searching for {args.query!r}"):
        passages = retrieval.retrieve(home, config, args.query, k=args.k)
    if args.json:
        _dump(args, passages)
        return
    if not passages:
        ui.info("no matches")
        ui.hint("if the index looks stale: px0 search reindex")
        return
    for p in passages:
        print(f"  {p.path}{ui.dim('#' + p.anchor)}  {ui.dim(str(round(p.score, 3)))}")
        print(f"    {ui.dim(p.text[:200].strip())}")


def cmd_skills(args: argparse.Namespace) -> None:
    """Handles `px0 skills`: acts as a proxy for the `npx skills` utility to discover,
    install, list, update, and remove community agent skills, or runs local `build` to compile
    guidelines into Claude Code skill bundles (`SKILL.md`)."""
    home, config = _ctx()
    
    skills_args = getattr(args, "skills_args", [])
    if skills_args and skills_args[0] == "build":
        with ui.spinner("Compiling guidelines"):
            written = skills_mod.build(home)
        if not written:
            ui.info("no guidelines found to build")
            return
        for w in written:
            ui.ok("built", f"skills/{w}")
        return

    import subprocess
    import shutil

    if not shutil.which("npx"):
        ui.err("npx not found on PATH",
               "Node.js is required for `px0 skills` -- https://nodejs.org")
        sys.exit(EXIT_USER_ERROR)

    skills_json = home / "skills.json"
    agents_skill_lock = Path("~/.agents/.skill-lock.json").expanduser()

    # Sync local .px0/skills.json -> ~/.agents/.skill-lock.json before running npx
    if skills_json.exists():
        agents_skill_lock.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy(str(skills_json), str(agents_skill_lock))

    # We always want global mode (-g) for px0 skills proxies, because we're managing the user's AI state.
    run_args = ["npx", "--yes", "skills@latest"] + skills_args
    if "-g" not in skills_args and "--global" not in skills_args:
        run_args.append("-g")

    try:
        subprocess.run(run_args, check=True)
    except subprocess.CalledProcessError as e:
        sys.exit(e.returncode)
    except KeyboardInterrupt:
        sys.exit(130)
    finally:
        # Sync back ~/.agents/.skill-lock.json -> .px0/skills.json
        if agents_skill_lock.exists():
            shutil.copy(str(agents_skill_lock), str(skills_json))


def cmd_why(args: argparse.Namespace) -> None:
    """Handles `px0 why <target_id>`: prints the provenance chain explaining how a
    claim, proposal, or other tracked entity came to be."""
    home, config = _ctx()
    try:
        result = provenance.why(home, config, args.target_id)
    except provenance.WhyError as e:
        ui.err(str(e))
        sys.exit(EXIT_USER_ERROR)
    _dump(args, result)


def cmd_store(args: argparse.Namespace) -> None:
    """Handles `px0 store export <dir>`: copies store content and version history to
    another directory, excluding credentials."""
    home, config = _ctx()
    if args.store_cmd == "export":
        with ui.spinner(f"Exporting store to {args.dir}"):
            store_mod.export(home, Path(args.dir))
        ui.ok("exported", f"{args.dir}  (credentials excluded)")


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

    failed = [n for n, c in checks.items() if not c["ok"]]
    if failed:
        ui.hint(f"{len(failed)} check(s) failed: {', '.join(failed)}")
    sys.exit(0 if report["all_ok"] else EXIT_INTEGRITY_ERROR)


# --- argument parser -----------------------------------------------------

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
