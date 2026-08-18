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
from datetime import datetime, timedelta
from pathlib import Path

from px0 import (
    ask as ask_mod,
    builder as builder_mod,
    claims,
    config as config_mod,
    connect as connect_mod,
    consolidate as consolidate_mod,
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
        print(f"no px0 store at {home}; run `px0 init` first", file=sys.stderr)
        sys.exit(EXIT_USER_ERROR)
    config = config_mod.load(paths.config_path(home))
    return home, config


def _parse_since(text: str) -> datetime:
    """Parses a `--since` value like "7d" into an absolute datetime that many days ago."""
    m = re.fullmatch(r"(\d+)d", text)
    if not m:
        raise ValueError(f"unsupported --since format: {text!r} (use e.g. 7d)")
    return datetime.now() - timedelta(days=int(m.group(1)))


def _dump(args: argparse.Namespace, data) -> None:
    """Prints data to stdout as indented JSON, coercing non-JSON-serializable values via str()."""
    print(json.dumps(data, indent=2, default=str))


# --- init / new / run / ask ---------------------------------------------

def cmd_init(args: argparse.Namespace) -> None:
    """Handles `px0 init`: scaffolds a new store and prints suggested next commands."""
    home = Path(args.dir).expanduser() if args.dir else paths.store_home()
    harness_cmd = harness.KNOWN_HARNESSES[args.harness] if args.harness else None
    created = store_mod.init(home, harness_cmd=harness_cmd)
    for line in created:
        print(f"created {line}")
    print(f"\npx0 initialized at {home}")
    print("try next:")
    print("  px0 list workflows")
    print("  px0 run pr-precheck --stdin < some.diff")
    print("  px0 doctor")


def cmd_new(args: argparse.Namespace) -> None:
    """Handles `px0 new`: generates a workflow plan from a natural-language description,
    checks feasibility and required connections, then writes the workflow file after
    interactive confirmation (unless --yes is passed)."""
    home, config = _ctx()
    try:
        plan = builder_mod.generate_plan(config, args.description)
    except (builder_mod.BuilderError, harness.HarnessError) as e:
        print(str(e), file=sys.stderr)
        sys.exit(EXIT_MODEL_ERROR)

    print("Plan:")
    print(json.dumps(plan.raw, indent=2))

    writes = builder_mod.write_tools_named(plan)
    if writes:
        print(f"\nthis workflow would be granted write tools: {writes}")

    issues = builder_mod.check_feasibility(plan, home)
    if issues:
        print("\nfeasibility issues:")
        for i in issues:
            print(f"  - {i}")
        print("\ncannot proceed until these are resolved")
        sys.exit(EXIT_USER_ERROR)

    needed = builder_mod.required_connections(plan)
    existing = {c["service"] for c in connect_mod.list_connections(home)}
    missing = needed - existing
    if missing:
        print(f"\nconnections needed but not configured: {sorted(missing)}")
        for service in sorted(missing):
            if service in ("gmail", "slack", "calendar"):
                try:
                    res = connect_mod.connect_composio_app(home, service)
                    print(f"To connect {service} (Composio), open this URL and complete OAuth:")
                    print(f"  {res['redirect_url']}")
                except ValueError as e:
                    print(f"Error preparing Composio connection for {service}: {e}", file=sys.stderr)
            elif service == "github":
                print("To connect github, run: `px0 connect github --native --pat <token>`")
        sys.exit(EXIT_USER_ERROR)

    if not args.yes:
        confirm = input("\nGenerate this workflow? [y/N] ").strip().lower()
        if confirm != "y":
            print("cancelled")
            return

    # slugify the description into a default workflow id, capped to 40 chars
    default_id = re.sub(r"[^a-z0-9-]+", "-", plan.description.lower()).strip("-")[:40] or "new-workflow"
    workflow_id = args.id or (default_id if args.yes else input(f"workflow id [{default_id}]: ").strip() or default_id)

    guidelines = builder_mod.choose_guidelines(home, args.description)
    if guidelines:
        print(f"guidelines selected: {guidelines}")

    content = builder_mod.render_workflow_file(workflow_id, plan, guidelines)
    dest = builder_mod.save_workflow(home, workflow_id, content)
    print(f"saved {dest}")


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
        record = runner.run(
            home, config, args.workflow, trigger=trigger, cli_inputs=cli_inputs,
            dry_run=args.dry_run, output_override=output_override,
            late_scheduled_at=args.late_scheduled_at,
        )
    except runner.RunError as e:
        print(f"run failed: {e}", file=sys.stderr)
        sys.exit(EXIT_USER_ERROR)

    if not args.quiet:
        out = record.get("output", {})
        if out.get("target") == "file":
            print(f"[{record['id']}] {record['outcome']} -> {out.get('path')}", file=sys.stderr)
        else:
            print(f"[{record['id']}] {record['outcome']}", file=sys.stderr)

    if args.json:
        _dump(args, record)
    elif record.get("output", {}).get("target") == "stdout":
        print(record["output"].get("text", ""))


def cmd_ask(args: argparse.Namespace) -> None:
    """Handles `px0 ask`: answers a question via retrieval over guidelines/knowledge
    and prints the answer, optionally followed by source passages with --sources."""
    home, config = _ctx()
    try:
        result = ask_mod.ask(home, config, args.question, k=args.k)
    except ask_mod.AskError as e:
        print(str(e), file=sys.stderr)
        sys.exit(EXIT_USER_ERROR)
    print(result["answer"])
    if args.sources:
        print("\n--- sources ---")
        for p in result["passages"]:
            print(f"{p.path}#{p.anchor}")


def cmd_list(args: argparse.Namespace) -> None:
    """Handles `px0 list`: prints workflows, guidelines, and/or knowledge file paths.
    With no kind given, prints all three sections; otherwise prints just that section."""
    home, config = _ctx()
    kind = args.kind

    if kind in (None, "workflows"):
        if kind is None:
            print("# workflows")
        for wid, wf in sorted(workflow_mod.load_all(home).items()):
            print(f"{wid}\t{wf.description}")

    if kind in (None, "guidelines"):
        if kind is None:
            print("\n# guidelines")
        base = paths.guidelines_dir(home)
        for p in sorted(base.rglob("*.md")):
            print(str(p.relative_to(base)))

    if kind in (None, "knowledge"):
        if kind is None:
            print("\n# knowledge")
        base = retrieval.knowledge_path(home, config)
        if base.exists():
            for p in sorted(base.rglob("*.md")):
                print(str(p.relative_to(base)))


# --- connect / tools -----------------------------------------------------

def cmd_connect(args: argparse.Namespace) -> None:
    """Handles `px0 connect` and its sub-targets: setup-composio, list, remove, rotate,
    and connecting a new service (native github only in this build; anything else
    reports Composio auth-link creation as unimplemented)."""
    home, _ = _ctx()
    target = args.target

    if target == "setup-composio":
        key = args.service2 or args.api_key
        if not key:
            print("usage: px0 connect setup-composio <api-key>", file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        connect_mod.setup_composio(home, key)
        print("composio api key stored")
        return

    if target == "list":
        for c in connect_mod.list_connections(home):
            print(c)
        return

    if target == "remove":
        if not args.service2:
            print("usage: px0 connect remove <service>", file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        ok = connect_mod.remove_connection(home, args.service2)
        print("removed" if ok else "no such connection")
        return

    if target == "rotate":
        service = args.service2
        if service != "github":
            print("rotate is only wired for native github in this build", file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        if not args.pat:
            print("usage: px0 connect rotate github --pat <token>", file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        try:
            info = connect_mod.rotate_github(home, args.pat)
        except ValueError as e:
            print(str(e), file=sys.stderr)
            sys.exit(EXIT_CONNECTOR_ERROR)
        print(f"rotated github token for {info['login']}")
        return

    # otherwise: connecting a service
    service = target
    if service == "github" and args.native:
        if not args.pat:
            print("usage: px0 connect github --native --pat <token>", file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        try:
            info = connect_mod.connect_github_native(home, args.pat)
        except ValueError as e:
            print(str(e), file=sys.stderr)
            sys.exit(EXIT_CONNECTOR_ERROR)
        print(f"connected github as {info['login']}")
    elif service in ("gmail", "slack", "calendar"):
        try:
            res = connect_mod.connect_composio_app(home, service)
            print(f"To connect {service}, open the following URL in your browser and complete OAuth:")
            print(f"  {res['redirect_url']}")
        except ValueError as e:
            print(str(e), file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
    else:
        print(f"{service}: Composio auth-link creation is not implemented in this build.")
        print("Use `px0 connect github --native --pat <token>` for the native path.")
        sys.exit(EXIT_USER_ERROR)


def cmd_tools(args: argparse.Namespace) -> None:
    """Handles `px0 tools list`: prints each available tool with a read/write marker,
    its id, provider, description, and parameters, optionally filtered by service."""
    for t in tools.list_tools(args.service):
        marker = "write" if t.is_write else "read "
        print(f"[{marker}] {t.id}\t{t.provider}\t{t.description}\tparams={t.params}")


# --- daemon ----------------------------------------------------------------

def cmd_daemon(args: argparse.Namespace) -> None:
    """Handles `px0 daemon` subcommands: install, status, start, stop, restart, logs,
    serve. start/restart spawn `python -m px0.cli daemon serve` as a detached child
    process with PX0_HOME set; stop/restart send SIGTERM to the recorded pid."""
    home, config = _ctx()

    if args.daemon_cmd == "install":
        result = daemon_mod.install(home, fallback_cron=args.fallback_cron)
        print(f"platform: {result['platform']}")
        if result.get("path"):
            print(f"wrote {result['path']}")
        print()
        print(result["content"])
        print(f"start it with: {result['start_hint']}")
        if result.get("reduced_semantics"):
            print(f"note: {result['reduced_semantics']}")
        return

    if args.daemon_cmd == "status":
        _dump(args, daemon_mod.status(home, config))
        return

    if args.daemon_cmd == "start":
        # detached child inherits current env plus an explicit PX0_HOME so it targets the same store
        subprocess.Popen(
            [sys.executable, "-m", "px0.cli", "daemon", "serve"],
            env={**os.environ, "PX0_HOME": str(home)},
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        print("daemon starting")
        return

    if args.daemon_cmd == "stop":
        s = daemon_mod.status(home, config)
        if s["pid"] and s["alive"]:
            os.kill(s["pid"], signal.SIGTERM)
            print("daemon stopped")
        else:
            print("daemon not running")
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
        print("daemon restarted")
        return

    if args.daemon_cmd == "logs":
        daemon_log_path = runs_mod.resolve_logs_path(config) / "daemon.log"
        if not daemon_log_path.exists():
            print("no daemon log yet")
            return
        content = daemon_log_path.read_text(encoding="utf-8")
        if content:
            print(content, end="")
        if args.follow:
            try:
                for line in runs_mod.tail_lines(daemon_log_path):
                    print(line, end="", flush=True)
            except KeyboardInterrupt:
                pass
        return

    if args.daemon_cmd == "serve":
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
        for r in records:
            print(runs_tui.format_row(r))
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
            print("this run has no workflow to rerun (it was an ask)", file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        new_record = runner.run(home, config, wf_id, trigger="manual")
        print(f"reran as {new_record['id']}", file=sys.stderr)
        if new_record.get("output", {}).get("target") == "stdout":
            print(new_record["output"].get("text", ""))
        return

    if args.runs_cmd == "logs":
        log_path = runs_mod.log_path(config, args.run_id)
        if not log_path.exists():
            print(f"no log file for run {args.run_id}")
            return
        content = runs_mod.read_raw_log(config, args.run_id)
        if content:
            print(content, end="")
        if args.follow:
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
            result = knowledge_mod.add(
                home, config, args.source, to=args.to, no_propose=args.no_propose
            )
        except knowledge_mod.IngestError as e:
            print(str(e), file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        suffix = " (stub, no transcript yet)" if result.is_stub else ""
        print(f"ingested -> {result.path}{suffix}")
        return

    if args.knowledge_cmd == "refresh":
        try:
            result = knowledge_mod.refresh(home, config, Path(args.path))
        except knowledge_mod.IngestError as e:
            print(str(e), file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        print(f"refreshed -> {result.path}")
        return


# --- guidelines / consolidate --------------------------------------------

def _interactive_review(home: Path, proposal_list: list, non_interactive: bool) -> None:
    """Walks the user through each pending proposal, prompting accept/edit/dismiss
    unless non_interactive is set (in which case proposals are only printed, not
    acted on). Accepted/edited proposals are applied together as one change."""
    if not proposal_list:
        print("nothing pending")
        return
    decisions = []
    for p in proposal_list:
        print(f"\n[{p.target_file}] {p.action}: {p.claim}")
        print(p.body)
        print(f"evidence: {p.evidence_source}#{p.evidence_anchor}")
        if non_interactive:
            continue
        choice = input("accept/edit/dismiss? [a/e/d] ").strip().lower()
        if choice == "a":
            decisions.append({"proposal": p, "edited_body": None})
        elif choice == "e":
            print("enter the replacement body, blank line to finish:")
            lines = []
            while True:
                line = input()
                if not line:
                    break  # blank line terminates multi-line entry
                lines.append(line)
            decisions.append({"proposal": p, "edited_body": "\n".join(lines)})
        else:
            proposals_mod.dismiss(home, p.id)

    if decisions:
        change_id = proposals_mod.apply_many(home, "user:manual", decisions)
        print(f"\napplied as {change_id}")
    elif not non_interactive:
        print("\nnothing accepted")


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
            print(str(e), file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        print(f"reverted as {change_id}")
        return

    if args.guidelines_cmd == "alias":
        if args.alias_cmd == "list":
            for a in claims.list_aliases(home):
                print(f"{a['old_claim']} -> {a['new_claim']}")
        elif args.alias_cmd == "link":
            claims.add_alias(home, args.old, args.new)
            print("linked")
        elif args.alias_cmd == "unlink":
            claims.remove_alias(home, args.old)
            print("unlinked")
        return


def cmd_consolidate(args: argparse.Namespace) -> None:
    """Handles `px0 consolidate`: builds a consolidation session (pending proposals,
    decayed claims, contradictions, unreferenced guideline files), prints a summary,
    then runs the same interactive review flow as `guidelines review`."""
    home, config = _ctx()
    session = consolidate_mod.build_session(home, config)

    print(f"{len(session['proposals'])} proposal(s) pending review "
          f"({session['proposals_overflow']} deferred to next session)")
    for c in session["decayed_claims"]:
        print(f"  decayed: {c['claim']} ({c['days_since_reinforced']}d since last touched)")
    for c in session["contradictions"]:
        print(f"  contradiction: {c}")
    for f in session["unreferenced_files"]:
        print(f"  unreferenced: guidelines/{f} (no workflow lists it)")

    _interactive_review(home, session["proposals"], args.list_only)


# --- versions / changes --------------------------------------------------

def _parse_version_ref(ref: str) -> tuple[str, int]:
    """Splits a `<path>@v<N>` reference into (path, version number)."""
    if "@v" not in ref:
        raise ValueError(f"expected <path>@v<N>, got {ref!r}")
    path, v = ref.rsplit("@v", 1)
    return path, int(v)


def cmd_versions(args: argparse.Namespace) -> None:
    """Handles `px0 versions` subcommands: list, show, diff, revert, prune -- the
    per-file version history maintained by the tool's own versioning system."""
    home, config = _ctx()

    if args.versions_cmd == "list":
        entries = versioning.list_versions(home, args.path)
        if args.json:
            _dump(args, entries)
            return
        for v in entries:
            tag = " (deleted)" if v["deleted"] else ""
            print(f"v{v['version']}\t{v['actor']}\t{v['change_id']}\t{v['timestamp']}{tag}")
        return

    if args.versions_cmd == "show":
        path, v = _parse_version_ref(args.ref)
        content = versioning.show_version(home, path, v)
        print(content.decode() if content is not None else "(deleted at this version)")
        return

    if args.versions_cmd == "diff":
        print(versioning.diff_versions(home, args.path, args.v1, args.v2))
        return

    if args.versions_cmd == "revert":
        change_id = versioning.revert_file(home, args.path, args.to, "user:manual")
        print(f"reverted as {change_id}" if change_id else "already at that content")
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
        for c in changes:
            print(f"{c['id']}\t{c['actor']}\t{c['timestamp']}\t{len(c['files'])} file(s)")
        return

    if args.changes_cmd == "show":
        _dump(args, versioning.show_change(home, args.change_id))
        return

    if args.changes_cmd == "revert":
        new_id = versioning.revert_change(home, args.change_id, "user:manual")
        print(f"reverted as {new_id}" if new_id else "nothing to revert")
        return


# --- search / skills / why / store / update / version / doctor -----------

def cmd_search(args: argparse.Namespace) -> None:
    """Handles `px0 search`: rebuilds the retrieval index when the query is literally
    "reindex", otherwise retrieves and prints the top-k matching passages."""
    home, config = _ctx()
    if args.query == "reindex":
        count = retrieval.reindex(home, config)
        print(f"reindexed {count} passages")
        return
    passages = retrieval.retrieve(home, config, args.query, k=args.k)
    if args.json:
        _dump(args, passages)
        return
    for p in passages:
        print(f"{p.path}#{p.anchor}\t{round(p.score, 3)}")
        print(f"  {p.text[:200].strip()}")


def cmd_skills(args: argparse.Namespace) -> None:
    """Handles `px0 skills build`: builds the skills/ output directory and prints
    each file written."""
    home, config = _ctx()
    written = skills_mod.build(home)
    for w in written:
        print(f"built skills/{w}")


def cmd_why(args: argparse.Namespace) -> None:
    """Handles `px0 why <target_id>`: prints the provenance chain explaining how a
    claim, proposal, or other tracked entity came to be."""
    home, config = _ctx()
    try:
        result = provenance.why(home, config, args.target_id)
    except provenance.WhyError as e:
        print(str(e), file=sys.stderr)
        sys.exit(EXIT_USER_ERROR)
    _dump(args, result)


def cmd_store(args: argparse.Namespace) -> None:
    """Handles `px0 store export <dir>`: copies store content and version history to
    another directory, excluding credentials."""
    home, config = _ctx()
    if args.store_cmd == "export":
        store_mod.export(home, Path(args.dir))
        print(f"exported to {args.dir} (credentials excluded)")


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
            changed = "" if e["value"] == e["default"] else f"  (default: {e['default']!r})"
            choices = f"  choices={e['choices']}" if e["choices"] else ""
            print(f"{e['key']} = {e['value']!r}{changed}  [{e['type']}]{choices}")
            print(f"    {e['help']}")
        return

    if args.config_cmd == "get":
        try:
            value = config_mod.get_key(config, args.key)
        except ValueError as e:
            print(str(e), file=sys.stderr)
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
            print(str(e), file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        config_mod.save(paths.config_path(home), config)
        print(f"{args.key} = {value}")
        return

    if args.config_cmd == "model":
        _select_model(home, config)
        return


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
    print(f"current: model.harness_cmd = {current!r}\n")
    print("harnesses:")
    for i, name in enumerate(names, 1):
        mark = "installed" if installed[name] else "not found on PATH"
        print(f"  {i}. {name}  ({harness.KNOWN_HARNESSES[name]})  -- {mark}")
    print(f"  {len(names) + 1}. custom command")

    choice = input(f"pick [1-{len(names) + 1}]: ").strip()
    if not choice.isdigit() or not (1 <= int(choice) <= len(names) + 1):
        print("cancelled")
        return
    idx = int(choice)

    if idx == len(names) + 1:
        base_cmd = input("harness command (the prompt is appended as the final argument): ").strip()
        if not base_cmd:
            print("cancelled")
            return
        name = None
    else:
        name = names[idx - 1]
        base_cmd = harness.KNOWN_HARNESSES[name]

    model = input("model name (blank = harness default; appended as --model <name>): ").strip()
    harness_cmd = harness.with_model(base_cmd, model or None)

    trial_config = copy.deepcopy(config)
    trial_config.setdefault("model", {})["harness_cmd"] = harness_cmd
    print(f"\nverifying: {harness_cmd} ...")
    try:
        harness.invoke(trial_config, "reply with the single word: ok", timeout=20)
        print("harness responded OK")
    except harness.HarnessError as e:
        print(f"verification failed: {e}", file=sys.stderr)
        if name and name in harness.AUTH_HINTS:
            print(f"hint: {harness.AUTH_HINTS[name]}", file=sys.stderr)
        if input("save anyway? [y/N] ").strip().lower() != "y":
            print("not saved")
            return

    config.setdefault("model", {})["harness_cmd"] = harness_cmd
    config_mod.save(paths.config_path(home), config)
    print(f"model.harness_cmd = {harness_cmd}")


def cmd_update(args: argparse.Namespace) -> None:
    """Handles `px0 update`: switches the update channel, checks for/applies an
    update, or rolls back."""
    home, config = _ctx()
    if args.rollback:
        try:
            update_mod.rollback(home, config)
        except update_mod.UpdateError as e:
            print(str(e), file=sys.stderr)
            sys.exit(EXIT_USER_ERROR)
        return
    if args.channel:
        config.setdefault("update", {})["channel"] = args.channel
        config_mod.save(paths.config_path(home), config)
        print(f"channel set to {args.channel}")
        return
    try:
        result = update_mod.run_update(home, config, check_only=args.check)
        print(result["message"])
    except update_mod.UpdateError as e:
        print(str(e), file=sys.stderr)
        sys.exit(EXIT_USER_ERROR)


def cmd_version(args: argparse.Namespace) -> None:
    """Handles `px0 version`: prints version/build info. Works even without an
    initialized store (require_init=False)."""
    home, config = _ctx(require_init=False)
    info = update_mod.version_info(home, config)
    for k, v in info.items():
        print(f"{k}: {v}")


def cmd_doctor(args: argparse.Namespace) -> None:
    """Handles `px0 doctor`: runs integrity/health checks and prints pass/fail per
    check. Exits with EXIT_INTEGRITY_ERROR if any check failed."""
    home, config = _ctx()
    report = doctor_mod.run(home, config, quick=args.quick)
    for name, check in report["checks"].items():
        mark = "OK" if check["ok"] else "FAIL"
        print(f"[{mark}] {name}: {check['detail']}")
    if args.json:
        _dump(args, report)
    sys.exit(0 if report["all_ok"] else EXIT_INTEGRITY_ERROR)


# --- argument parser -----------------------------------------------------

def build_parser() -> argparse.ArgumentParser:
    """Builds the full px0 argparse tree: one subparser per top-level command, each
    wiring its own flags and a `func` default that cmd dispatches to in main()."""
    p = argparse.ArgumentParser(prog="px0")
    p.add_argument("--json", action="store_true", help="machine-readable output where supported")
    sub = p.add_subparsers(dest="command", required=True)

    sp = sub.add_parser("init")
    sp.add_argument("dir", nargs="?")
    sp.add_argument(
        "--harness",
        choices=sorted(harness.KNOWN_HARNESSES),
        help="coding agent CLI to use as the model backend (default: claude)",
    )
    sp.set_defaults(func=cmd_init)

    sp = sub.add_parser("new")
    sp.add_argument("description")
    sp.add_argument("--yes", action="store_true", help="skip confirmation prompts")
    sp.add_argument("--id", help="workflow id to save as")
    sp.set_defaults(func=cmd_new)

    sp = sub.add_parser("run")
    sp.add_argument("workflow")
    sp.add_argument("--quiet", action="store_true")
    sp.add_argument("--stdin", action="store_true")
    sp.add_argument("--output", choices=["stdout", "file"])
    sp.add_argument("--dry-run", action="store_true")
    sp.add_argument("--input", action="append", metavar="KEY=VALUE")
    sp.add_argument("--late-scheduled-at", help=argparse.SUPPRESS)  # internal-use only: hidden from --help, set by the daemon for backfilled runs
    sp.add_argument("--json", action="store_true")
    sp.set_defaults(func=cmd_run)

    sp = sub.add_parser("ask")
    sp.add_argument("question")
    sp.add_argument("--k", type=int, default=5)
    sp.add_argument("--sources", action="store_true")
    sp.set_defaults(func=cmd_ask)

    sp = sub.add_parser("list")
    sp.add_argument("kind", nargs="?", choices=["workflows", "guidelines", "knowledge"])
    sp.set_defaults(func=cmd_list)

    sp = sub.add_parser("connect")
    sp.add_argument("target")
    sp.add_argument("service2", nargs="?")
    sp.add_argument("--native", action="store_true")
    sp.add_argument("--pat")
    sp.add_argument("--api-key")
    sp.set_defaults(func=cmd_connect)

    sp = sub.add_parser("tools")
    tools_sub = sp.add_subparsers(dest="tools_cmd", required=True)
    tp = tools_sub.add_parser("list")
    tp.add_argument("service", nargs="?")
    sp.set_defaults(func=cmd_tools)

    sp = sub.add_parser("daemon")
    daemon_sub = sp.add_subparsers(dest="daemon_cmd", required=True)
    dp = daemon_sub.add_parser("install")
    dp.add_argument("--fallback-cron", action="store_true")
    daemon_sub.add_parser("status")
    daemon_sub.add_parser("start")
    daemon_sub.add_parser("stop")
    daemon_sub.add_parser("restart")
    daemon_logs_parser = daemon_sub.add_parser("logs")
    daemon_logs_parser.add_argument("--follow", "-f", action="store_true", help="follow daemon log tail")
    daemon_sub.add_parser("serve")
    sp.set_defaults(func=cmd_daemon)

    sp = sub.add_parser("runs")
    runs_sub = sp.add_subparsers(dest="runs_cmd", required=False)
    rp = runs_sub.add_parser("list")
    rp.add_argument("--workflow")
    rp.add_argument("--failed", action="store_true")
    rp.add_argument("--since")
    rp.add_argument("--json", action="store_true")
    for name in ("show", "output", "rerun"):
        rp2 = runs_sub.add_parser(name)
        rp2.add_argument("run_id")
    rp3 = runs_sub.add_parser("logs")
    rp3.add_argument("run_id")
    rp3.add_argument("--follow", "-f", action="store_true", help="follow run log tail")
    sp.set_defaults(func=cmd_runs)

    sp = sub.add_parser("knowledge")
    knowledge_sub = sp.add_subparsers(dest="knowledge_cmd", required=True)
    kp = knowledge_sub.add_parser("add")
    kp.add_argument("source")
    kp.add_argument("--to", choices=["docs", "blogs", "papers"])
    kp.add_argument("--wait", action="store_true", help="default in this build; kept for compatibility")
    kp.add_argument("--no-propose", action="store_true")
    kp2 = knowledge_sub.add_parser("refresh")
    kp2.add_argument("path")
    sp.set_defaults(func=cmd_knowledge)

    sp = sub.add_parser("guidelines")
    g_sub = sp.add_subparsers(dest="guidelines_cmd", required=True)
    gp = g_sub.add_parser("review")
    gp.add_argument("--list-only", action="store_true", help="print pending proposals without prompting")
    gp = g_sub.add_parser("log")
    gp.add_argument("claim_id")
    gp = g_sub.add_parser("revert")
    gp.add_argument("claim_id")
    gp.add_argument("--to", type=lambda s: int(s.lstrip("v")), required=True)
    gp = g_sub.add_parser("alias")
    alias_sub = gp.add_subparsers(dest="alias_cmd", required=True)
    alias_sub.add_parser("list")
    lp = alias_sub.add_parser("link")
    lp.add_argument("old")
    lp.add_argument("new")
    up = alias_sub.add_parser("unlink")
    up.add_argument("old")
    sp.set_defaults(func=cmd_guidelines)

    sp = sub.add_parser("consolidate")
    sp.add_argument("--list-only", action="store_true")
    sp.set_defaults(func=cmd_consolidate)

    sp = sub.add_parser("versions")
    v_sub = sp.add_subparsers(dest="versions_cmd", required=True)
    vp = v_sub.add_parser("list")
    vp.add_argument("path")
    vp.add_argument("--json", action="store_true")
    vp = v_sub.add_parser("show")
    vp.add_argument("ref", help="<path>@v<N>")
    vp = v_sub.add_parser("diff")
    vp.add_argument("path")
    vp.add_argument("v1", type=int)
    vp.add_argument("v2", type=int)
    vp = v_sub.add_parser("revert")
    vp.add_argument("path")
    vp.add_argument("--to", type=lambda s: int(s.lstrip("v")), required=True)
    vp = v_sub.add_parser("prune")
    vp.add_argument("--dry-run", action="store_true")
    sp.set_defaults(func=cmd_versions)

    sp = sub.add_parser("changes")
    c_sub = sp.add_subparsers(dest="changes_cmd", required=True)
    cp = c_sub.add_parser("list")
    cp.add_argument("--since")
    cp.add_argument("--actor")
    cp.add_argument("--json", action="store_true")
    cp = c_sub.add_parser("show")
    cp.add_argument("change_id")
    cp = c_sub.add_parser("revert")
    cp.add_argument("change_id")
    sp.set_defaults(func=cmd_changes)

    sp = sub.add_parser("search")
    sp.add_argument("query")
    sp.add_argument("--k", type=int, default=5)
    sp.add_argument("--json", action="store_true")
    sp.set_defaults(func=cmd_search)

    sp = sub.add_parser("skills")
    skills_sub = sp.add_subparsers(dest="skills_cmd", required=True)
    skills_sub.add_parser("build")
    sp.set_defaults(func=cmd_skills)

    sp = sub.add_parser("why")
    sp.add_argument("target_id")
    sp.set_defaults(func=cmd_why)

    sp = sub.add_parser("store")
    store_sub = sp.add_subparsers(dest="store_cmd", required=True)
    ep = store_sub.add_parser("export")
    ep.add_argument("dir")
    sp.set_defaults(func=cmd_store)

    sp = sub.add_parser("config")
    config_sub = sp.add_subparsers(dest="config_cmd", required=True)
    lp = config_sub.add_parser("list")
    lp.add_argument("--json", action="store_true")
    gp = config_sub.add_parser("get")
    gp.add_argument("key")
    gp.add_argument("--json", action="store_true")
    stp = config_sub.add_parser("set")
    stp.add_argument("key")
    stp.add_argument("value")
    config_sub.add_parser("model")
    sp.set_defaults(func=cmd_config)

    sp = sub.add_parser("update")
    sp.add_argument("--check", action="store_true")
    sp.add_argument("--channel")
    sp.add_argument("rollback", nargs="?", choices=["rollback"], default=None)
    sp.set_defaults(func=cmd_update)

    sp = sub.add_parser("version")
    sp.set_defaults(func=cmd_version)

    sp = sub.add_parser("doctor")
    sp.add_argument("--quick", action="store_true")
    sp.add_argument("--json", action="store_true")
    sp.set_defaults(func=cmd_doctor)

    return p


def main(argv: list[str] | None = None) -> None:
    """CLI entry point: parses args, dispatches to the selected subcommand's handler,
    and translates known exception types into the appropriate exit code."""
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        args.func(args)
    # each except maps a failure category to its own exit code so callers/scripts can branch on it
    except tools.ConnectorError as e:
        print(str(e), file=sys.stderr)
        sys.exit(EXIT_CONNECTOR_ERROR)
    except harness.HarnessError as e:
        print(str(e), file=sys.stderr)
        sys.exit(EXIT_MODEL_ERROR)
    except workflow_mod.WorkflowError as e:
        print(str(e), file=sys.stderr)
        sys.exit(EXIT_USER_ERROR)
    except (FileNotFoundError, ValueError) as e:
        print(str(e), file=sys.stderr)
        sys.exit(EXIT_USER_ERROR)
    except KeyboardInterrupt:
        print()
        sys.exit(EXIT_USER_ERROR)


if __name__ == "__main__":
    main()
