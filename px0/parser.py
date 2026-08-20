"""The px0 argparse tree: what the CLI accepts, separated from what it does.

Kept out of `cli.py` so the ~200 lines of declarative flag wiring don't sit in
the middle of the command handlers. The dependency runs one way -- `cli`
imports this, never the reverse -- so `build` is handed the module holding the
handlers rather than importing them.
"""

import argparse

from px0 import harness


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

    sp = sub.add_parser("new")
    sp.add_argument("description")
    sp.add_argument("--yes", action="store_true",
                    help="skip every prompt: no clarifying questions, no confirmations")
    sp.add_argument("--id", help="workflow id to save as")
    sp.add_argument("--no-clarify", action="store_true",
                    help="build from the description as written, without asking questions")
    sp.add_argument("--no-discover", action="store_true",
                    help="use only px0's curated tools; skip the Composio catalogue search")
    sp.set_defaults(func=handlers.cmd_new)

    sp = sub.add_parser("run")
    sp.add_argument("workflow")
    sp.add_argument("--quiet", action="store_true")
    sp.add_argument("--stdin", action="store_true")
    sp.add_argument("--output", choices=["stdout", "file"])
    sp.add_argument("--dry-run", action="store_true")
    sp.add_argument("--input", action="append", metavar="KEY=VALUE")
    sp.add_argument("--late-scheduled-at", help=argparse.SUPPRESS)  # internal-use only: hidden from --help, set by the daemon for backfilled runs
    sp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    sp.set_defaults(func=handlers.cmd_run)

    sp = sub.add_parser("ask")
    sp.add_argument("question")
    sp.add_argument("--k", type=int, default=5)
    sp.add_argument("--sources", action="store_true")
    sp.set_defaults(func=handlers.cmd_ask)

    sp = sub.add_parser("list")
    sp.add_argument("kind", nargs="?", choices=["workflows", "guidelines", "knowledge"])
    sp.set_defaults(func=handlers.cmd_list)

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

    sp = sub.add_parser("runs")
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
    sp.set_defaults(func=handlers.cmd_runs)

    sp = sub.add_parser("knowledge")
    knowledge_sub = sp.add_subparsers(dest="knowledge_cmd", required=True)
    kp = knowledge_sub.add_parser("add")
    kp.add_argument("source")
    kp.add_argument("--to", choices=["docs", "blogs", "papers"])
    kp.add_argument("--no-propose", action="store_true")
    kp2 = knowledge_sub.add_parser("refresh")
    kp2.add_argument("path")
    sp.set_defaults(func=handlers.cmd_knowledge)

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
    sp.set_defaults(func=handlers.cmd_guidelines)

    sp = sub.add_parser("consolidate")
    sp.add_argument("--list-only", action="store_true")
    sp.set_defaults(func=handlers.cmd_consolidate)

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

    sp = sub.add_parser("search")
    sp.add_argument("query")
    sp.add_argument("--k", type=int, default=5)
    sp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    sp.set_defaults(func=handlers.cmd_search)

    sp = sub.add_parser("skills")
    sp.add_argument("skills_args", nargs=argparse.REMAINDER, help="Arguments to pass to npx skills")
    sp.set_defaults(func=handlers.cmd_skills)

    sp = sub.add_parser("why")
    sp.add_argument("target_id")
    sp.set_defaults(func=handlers.cmd_why)

    sp = sub.add_parser("store")
    store_sub = sp.add_subparsers(dest="store_cmd", required=True)
    ep = store_sub.add_parser("export")
    ep.add_argument("dir")
    sp.set_defaults(func=handlers.cmd_store)

    sp = sub.add_parser("config")
    config_sub = sp.add_subparsers(dest="config_cmd", required=True)
    lp = config_sub.add_parser("list")
    lp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    gp = config_sub.add_parser("get")
    gp.add_argument("key")
    gp.add_argument("--json", action="store_true", default=argparse.SUPPRESS)
    stp = config_sub.add_parser("set")
    stp.add_argument("key")
    stp.add_argument("value")
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
