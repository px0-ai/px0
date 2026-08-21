"""The px0 argparse tree: what the CLI accepts, separated from what it does.

Kept out of `cli.py` so the declarative flag wiring doesn't sit in the middle of
the command handlers. The dependency runs one way -- `cli` imports this, never
the reverse -- so `build` is handed the module holding the handlers rather than
importing them.

Shape: **entity first, then the verb acting on it** -- `px0 workflows new`,
`px0 brain search`, `px0 guidelines review`. The only flat commands are the
four that act on the install rather than on anything in the store: `init`,
`doctor`, `version`, `update`.

Each leaf sets its own `func`, so a group needs no dispatch table -- but the
group's `dest` is still set, because several handlers serve more than one leaf
and switch on it.
"""

import argparse

from px0 import config as config_mod
from px0 import harness
from px0 import retrieval


def build(handlers) -> argparse.ArgumentParser:
    """Builds the full px0 argparse tree: one subparser per top-level command, each
    wiring its own flags and a `func` default that main() dispatches to.

    `handlers` is the module providing the `cmd_*` functions.
    """
    p = argparse.ArgumentParser(prog="px0")
    # Declared on the root parser so `px0 --json <cmd>` works; every subcommand
    # that repeats it uses default=SUPPRESS so an omitted sub-level flag leaves
    # this value alone instead of resetting it to False.
    p.add_argument("--json", action="store_true", help="machine-readable output where supported")
    p.add_argument("--no-color", action="store_true",
                   help="plain output with no colour or animation (also: NO_COLOR=1)")
    sub = p.add_subparsers(dest="command", required=True)

    sp = sub.add_parser("init")
    sp.add_argument("dir", nargs="?")
    sp.add_argument(
        "--harness",
        choices=sorted(harness.KNOWN_HARNESSES),
        help="coding agent CLI to use as the model backend (default: claude)",
    )
    sp.add_argument("--composio-key", help="Composio API key")
    sp.set_defaults(func=handlers.cmd_init)

    sp = sub.add_parser("workflows", help="build, run, and list workflows")
    wf_sub = sp.add_subparsers(dest="workflows_cmd", required=True)
    wp = wf_sub.add_parser("new", help="describe a workflow and have px0 build it")
    wp.add_argument("description")
    wp.add_argument("--yes", action="store_true",
                    help="skip every prompt: no clarifying questions, no confirmations")
    wp.add_argument("--id", help="workflow id to save as")
    wp.add_argument("--no-clarify", action="store_true",
                    help="build from the description as written, without asking questions")
    wp.add_argument("--no-discover", action="store_true",
                    help="use only px0's curated tools; skip the Composio catalogue search")
    wp.set_defaults(func=handlers.cmd_new)

    wp = wf_sub.add_parser("run", help="execute a workflow")
    # Optional: with no id, `cmd_run` puts up a picker. Not available with
    # --stdin, which is already reading the stream the keystrokes would come from.
    wp.add_argument("workflow", nargs="?",
                    help="workflow id; omit to pick one from a list")
    wp.add_argument("--quiet", action="store_true")
    wp.add_argument("--stdin", action="store_true")
    wp.add_argument("--output", choices=["stdout", "file"])
    wp.add_argument("--dry-run", action="store_true")
    wp.add_argument("--input", action="append", metavar="KEY=VALUE")
    wp.add_argument("--late-scheduled-at", help=argparse.SUPPRESS)  # internal-use only: hidden from --help, set by the daemon for backfilled runs
    wp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    wp.set_defaults(func=handlers.cmd_run)

    wp = wf_sub.add_parser("edit", help="revise a workflow's instructions and rebuild it")
    wp.add_argument("workflow", nargs="?",
                    help="workflow id; omit to pick one from a list")
    wp.add_argument("--yes", action="store_true",
                    help="skip every prompt: no clarifying questions, no confirmations")
    wp.add_argument("--no-clarify", action="store_true",
                    help="rebuild from the new instructions as written, without asking questions")
    wp.add_argument("--no-discover", action="store_true",
                    help="use only px0's curated tools; skip the Composio catalogue search")
    wp.set_defaults(func=handlers.cmd_workflows_edit)

    wp = wf_sub.add_parser("list", help="every workflow in the store")
    wp.set_defaults(func=handlers.cmd_workflows_list)

    sp = sub.add_parser("tools")
    tools_sub = sp.add_subparsers(dest="tools_cmd", required=True)
    tp = tools_sub.add_parser("list")
    tp.add_argument("service", nargs="?")
    tp.add_argument("--status", action="store_true",
                    help="also show whether each tool's app is authorized (one API call per app)")
    sp.set_defaults(func=handlers.cmd_tools)

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
    sp.set_defaults(func=handlers.cmd_daemon)

    sp = sub.add_parser("runs", help="inspect and re-run past executions")
    runs_sub = sp.add_subparsers(dest="runs_cmd", required=False)
    rp = runs_sub.add_parser("list")
    rp.add_argument("--workflow")
    rp.add_argument("--failed", action="store_true")
    rp.add_argument("--since")
    rp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    for name in ("show", "output", "rerun"):
        rp2 = runs_sub.add_parser(name)
        rp2.add_argument("run_id")
    rp3 = runs_sub.add_parser("logs")
    rp3.add_argument("run_id")
    rp3.add_argument("--follow", "-f", action="store_true", help="follow run log tail")
    # `why` reads a run id here and a claim id under `guidelines`. One handler
    # serves both -- provenance.why already branches on the id's shape -- but it
    # is listed under each entity so the help says which id that group takes.
    rp4 = runs_sub.add_parser("why", help="how a run reached its result")
    rp4.add_argument("target_id", metavar="run_id")
    rp4.set_defaults(func=handlers.cmd_why)
    sp.set_defaults(func=handlers.cmd_runs)

    sp = sub.add_parser("brain", help="ingest, search, and ask over your brain")
    brain_sub = sp.add_subparsers(dest="brain_cmd", required=True)
    kp = brain_sub.add_parser("add", help="ingest a URL or file into your brain")
    kp.add_argument("source")
    # Any relative subfolder, not a fixed set: a brain pointed at an existing
    # vault should be able to file into the structure that vault already has
    # (`--to "Personal/Reading"`). `work` is included in the suggestion because
    # brain/work/ never leaves the machine, and while it was absent from a
    # closed choices list it was the one folder with a privacy guarantee that
    # nothing could be filed into.
    kp.add_argument(
        "--to", metavar="FOLDER",
        help="subfolder of the brain to file into, e.g. docs, blogs, papers, work, "
             "or any path of your own like \"Personal/Reading\"",
    )
    kp.add_argument("--no-propose", action="store_true")
    kp.set_defaults(func=handlers.cmd_brain)

    kp = brain_sub.add_parser("refresh", help="re-fetch an already-ingested source")
    kp.add_argument("path")
    kp.add_argument("--no-propose", action="store_true")
    kp.set_defaults(func=handlers.cmd_brain)

    kp = brain_sub.add_parser("list", help="every file in your brain")
    kp.set_defaults(func=handlers.cmd_brain_list)

    kp = brain_sub.add_parser("search", help="retrieve matching passages")
    kp.add_argument("query")
    # Defaulted in the handler, not here, so `retrieval.k_default` is actually
    # consulted -- an argparse default would silently win over the config key.
    kp.add_argument("--k", type=int, default=None)
    kp.add_argument("--kind", choices=list(retrieval.KINDS), default=None,
                    help="only passages from material of this kind")
    kp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    kp.set_defaults(func=handlers.cmd_search)

    kp = brain_sub.add_parser("ask", help="answer a question from your brain")
    kp.add_argument("question")
    kp.add_argument("--k", type=int, default=None)
    kp.add_argument("--kind", choices=list(retrieval.KINDS), default=None,
                    help="only answer from material of this kind")
    kp.add_argument("--sources", action="store_true")
    kp.set_defaults(func=handlers.cmd_ask)

    # Its own verb rather than the old magic `search reindex` query value, which
    # made "reindex" a word you could not search for.
    kp = brain_sub.add_parser("reindex", help="rebuild the retrieval index")
    kp.set_defaults(func=handlers.cmd_reindex)

    sp = sub.add_parser("guidelines", help="review, trace, and consolidate guidelines")
    g_sub = sp.add_subparsers(dest="guidelines_cmd", required=True)
    gp = g_sub.add_parser("list", help="every guideline file in the store")
    gp.set_defaults(func=handlers.cmd_guidelines_list)

    gp = g_sub.add_parser("review", help="accept or reject pending proposals")
    gp.add_argument("--list-only", action="store_true", help="print pending proposals without prompting")
    gp.set_defaults(func=handlers.cmd_guidelines)

    gp = g_sub.add_parser("log", help="a claim's edit history")
    gp.add_argument("claim_id")
    gp.set_defaults(func=handlers.cmd_guidelines)

    gp = g_sub.add_parser("revert", help="restore a claim to an earlier version")
    gp.add_argument("claim_id")
    gp.add_argument("--to", type=lambda s: int(s.lstrip("v")), required=True)
    gp.set_defaults(func=handlers.cmd_guidelines)

    gp = g_sub.add_parser("why", help="how a claim came to say what it says")
    gp.add_argument("target_id", metavar="claim_id")
    gp.set_defaults(func=handlers.cmd_why)

    gp = g_sub.add_parser("consolidate", help="merge overlap and surface stale files")
    gp.add_argument("--list-only", action="store_true")
    gp.set_defaults(func=handlers.cmd_consolidate)

    gp = g_sub.add_parser("alias", help="manage claim-id aliases")
    alias_sub = gp.add_subparsers(dest="alias_cmd", required=True)
    alias_sub.add_parser("list")
    lp = alias_sub.add_parser("link")
    lp.add_argument("old")
    lp.add_argument("new")
    up = alias_sub.add_parser("unlink")
    up.add_argument("old")
    gp.set_defaults(func=handlers.cmd_guidelines)

    sp = sub.add_parser("versions")
    v_sub = sp.add_subparsers(dest="versions_cmd", required=True)
    vp = v_sub.add_parser("list")
    vp.add_argument("path")
    vp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
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
    sp.set_defaults(func=handlers.cmd_versions)

    sp = sub.add_parser("changes")
    c_sub = sp.add_subparsers(dest="changes_cmd", required=True)
    cp = c_sub.add_parser("list")
    cp.add_argument("--since")
    cp.add_argument("--actor")
    cp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    cp = c_sub.add_parser("show")
    cp.add_argument("change_id")
    cp = c_sub.add_parser("revert")
    cp.add_argument("change_id")
    sp.set_defaults(func=handlers.cmd_changes)

    sp = sub.add_parser("skills")
    sp.add_argument("skills_args", nargs=argparse.REMAINDER, help="Arguments to pass to npx skills")
    sp.set_defaults(func=handlers.cmd_skills)

    sp = sub.add_parser("store", help="the store as a whole")
    store_sub = sp.add_subparsers(dest="store_cmd", required=True)
    ep = store_sub.add_parser("export", help="copy content and history elsewhere")
    ep.add_argument("dir")
    ep.set_defaults(func=handlers.cmd_store)
    # Where the flat `px0 list` overview went: workflows, guidelines, and
    # brain in one pass. The per-entity `list` verbs print one section each.
    lp2 = store_sub.add_parser("list", help="workflows, guidelines, and brain at once")
    lp2.set_defaults(func=handlers.cmd_store_list)

    sp = sub.add_parser("config", help="read and write store configuration")
    config_sub = sp.add_subparsers(dest="config_cmd", required=True)
    lp = config_sub.add_parser("list", help="every key with its value, default, and help")
    lp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)

    # `get` and `set` take a key the user has to know, so --help lists them.
    # RawDescription keeps the aligned block from being re-wrapped into a
    # paragraph. `get` omits the choices column: allowed values constrain what
    # you can write, and say nothing about reading.
    gp = config_sub.add_parser(
        "get", help="print one key's current value",
        epilog=config_mod.key_help(include_choices=False),
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    gp.add_argument("key", metavar="KEY", help="dotted key; see the list below")
    gp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)

    stp = config_sub.add_parser(
        "set", help="validate and save one key",
        epilog=config_mod.key_help(),
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    stp.add_argument("key", metavar="KEY", help="dotted key; see the list below")
    stp.add_argument("value", metavar="VALUE",
                     help="checked against the key's type and allowed values before saving")
    config_sub.add_parser("model")
    cop = config_sub.add_parser("composio")
    cop.add_argument("key", nargs="?", help="Composio API key; prompted for if omitted")
    sp.set_defaults(func=handlers.cmd_config)

    sp = sub.add_parser("update")
    sp.add_argument("--check", action="store_true")
    sp.add_argument("--channel")
    sp.add_argument("rollback", nargs="?", choices=["rollback"], default=None)
    sp.set_defaults(func=handlers.cmd_update)

    sp = sub.add_parser("version")
    sp.set_defaults(func=handlers.cmd_version)

    sp = sub.add_parser("doctor")
    sp.add_argument("--quick", action="store_true")
    sp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    sp.set_defaults(func=handlers.cmd_doctor)

    return p
