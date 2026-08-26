"""Command handlers for the assistant surfaces: ask, approvals, inbox, memory.

Kept out of `cli.py` deliberately. That file is where every handler has landed
since the beginning and it is now the one file that fights any change to the
CLI; adding four more groups to it would have made that worse for no reason.

Each handler here takes the resolved store and config rather than fetching
them, so `cli.py` keeps one place where a store is opened -- and so these stay
callable and testable without a parsed argv.
"""

import json
import sys
from pathlib import Path

from px0 import approvals as approvals_mod
from px0 import inbox as inbox_mod
from px0 import memory as memory_mod
from px0 import route as route_mod
from px0 import runs as runs_mod
from px0 import session as session_mod
from px0 import tools as tools_mod
from px0 import ui

EXIT_USER_ERROR = 1
EXIT_CONNECTOR_ERROR = 2
EXIT_MODEL_ERROR = 3


def _dump(data) -> None:
    print(json.dumps(data, indent=2, default=str), flush=True)


def _age(stamp: str) -> str:
    """A timestamp as "3h ago", for a listing where the exact minute is noise."""
    from datetime import datetime, timezone

    try:
        when = datetime.fromisoformat(stamp)
    except (TypeError, ValueError):
        return ""
    if when.tzinfo is None:
        when = when.replace(tzinfo=timezone.utc)
    seconds = (datetime.now(timezone.utc) - when).total_seconds()
    if seconds < 90:
        return "just now"
    for size, unit in ((3600, "m"), (86400, "h"), (86400 * 7, "d")):
        if seconds < size:
            divisor = {"m": 60, "h": 3600, "d": 86400}[unit]
            return f"{int(seconds // divisor)}{unit} ago"
    return f"{int(seconds // 86400)}d ago"


# --- px0 ask --------------------------------------------------------------

def cmd_ask(home: Path, config: dict, args) -> None:
    """Handles `px0 ask`: one question, routed to whoever can answer it.

    The route is decided first and printed with the answer, so a question that
    went somewhere surprising says so. `--route` pins it when you already know
    where it should go, which is both an escape hatch and the thing to reach
    for when the router keeps getting one kind of question wrong.
    """
    question = (getattr(args, "question", None) or "").strip()
    if not question and not getattr(args, "json", False):
        # No question and a terminal to type into: a conversation, which is
        # where a follow-up can be understood and a correction can be kept.
        return _converse(home, config, args)
    if not question:
        ui.err("nothing to ask")
        sys.exit(EXIT_USER_ERROR)

    if getattr(args, "continue_", False):
        return _converse(home, config, args, first=question)

    forced = getattr(args, "route", None)
    as_json = getattr(args, "json", False)

    if forced:
        decision = route_mod.Decision(forced, reason="you asked for this route",
                                      confidence="high")
    else:
        index = route_mod.candidates(home, config)
        try:
            with ui.spinner("Thinking about who can answer that"):
                decision = route_mod.decide(config, question, index)
        except route_mod.RouteError as e:
            ui.err("could not route that", str(e))
            sys.exit(EXIT_MODEL_ERROR)

    if getattr(args, "explain", False):
        _dump(decision.as_dict())
        return

    memories = memory_mod.relevant(home, question,
                                   budget=memory_mod.budget_chars(config))
    answer, sources, run_id = _answer(home, config, question, decision, memories, args)

    if as_json:
        _dump({"question": question, "decision": decision.as_dict(),
               "answer": answer, "sources": sources, "run": run_id})
        return

    if not forced and decision.route != "answer":
        ui.kv("asked", decision.route + (f" · {decision.workflow or decision.tool}"
                                          if (decision.workflow or decision.tool) else ""),
              stream=sys.stderr)
    print(flush=True)
    ui.render_markdown(answer)
    if sources:
        # One dim line rather than a hint each: these say where the answer came
        # from, and a hint opens with a blank line, which turned three sources
        # into three paragraphs.
        print(flush=True)
        print(ui.dim("from  " + ", ".join(sources[:6])), flush=True)

    repeats = route_mod.repeated_questions(config, question)
    if repeats and decision.route != "workflow":
        # The observation is not that a question was asked twice, but that the
        # user keeps doing by hand something px0 could be doing on a schedule.
        ui.hint(f"you have asked this {len(repeats) + 1} times -- px0 can build it "
                "into a workflow and put it on a schedule:")
        ui.command("px0 workflows new")


def _converse(home: Path, config: dict, args, first: str | None = None) -> None:
    """A conversation, rather than a series of unrelated questions.

    Each turn is routed like a single `ask`, but with what came before folded
    into both the routing and the answer -- so a follow-up that names no
    subject still lands, and a correction is understood as correcting
    something rather than as a new question.

    What the session is *for* arrives at the end: the corrections the user made
    are read for standing facts, and anything worth keeping is offered to
    `memory/`. A conversation you have to have twice is one px0 learned
    nothing from.
    """
    session = None
    if getattr(args, "continue_", False):
        session = session_mod.latest(home)
        if session:
            ui.info("continuing", f"{len(session.get('turns') or [])} turn(s) so far")
    session = session or session_mod.start(home)

    if not first and not sys.stdin.isatty():
        ui.err("nothing to ask", "give a question, or run this in a terminal")
        sys.exit(EXIT_USER_ERROR)

    ui.remark("Ask anything. Blank line or Ctrl-C to finish.")
    pending = first
    while True:
        if pending is None:
            try:
                pending = ui.prompt("\n> ").strip()
            except (KeyboardInterrupt, EOFError):
                print(flush=True)
                break
        if not pending:
            break

        routed = session_mod.resolve_question(session, pending)
        index = route_mod.candidates(home, config)
        try:
            with ui.spinner("Thinking"):
                decision = route_mod.decide(config, routed, index)
        except route_mod.RouteError as e:
            ui.err("could not route that", str(e))
            break

        memories = memory_mod.relevant(home, routed,
                                       budget=memory_mod.budget_chars(config))
        answer, sources, run_id = _answer(
            home, config, pending, decision, memories, args,
            context=session_mod.context_block(session))

        print(flush=True)
        ui.render_markdown(answer)
        if sources:
            print(ui.dim("from  " + ", ".join(sources[:6])), flush=True)
        session_mod.add_turn(home, session, pending, answer,
                             decision.as_dict(), run_id)
        pending = None

    _close_session(home, config, session, args)


def _close_session(home: Path, config: dict, session: dict, args) -> None:
    """Ends a conversation by offering to keep what it taught px0.

    Only the corrections are read. A conversation that went well says nothing
    worth remembering -- it is the moments the user put px0 right that carry a
    standing fact, and those were previously discarded along with the rest.
    """
    corrections = session_mod.corrections(session)
    session_mod.prune(home, config)
    if not corrections or getattr(args, "no_remember", False):
        return

    try:
        with ui.spinner("Anything worth remembering?"):
            candidates = memory_mod.suggest(home, config, extra=corrections)
    except Exception:
        return
    if not candidates:
        return

    print(flush=True)
    ui.heading("worth remembering?")
    ui.remark("px0 only keeps what you agree to. Each of these becomes a file "
              "you can edit or delete.")
    kept = 0
    for candidate in candidates:
        ui.bullet(candidate["text"])
        if candidate.get("why"):
            ui.field("from", candidate["why"], width=4)
        if not sys.stdin.isatty():
            continue
        if ui.prompt("remember this? [y/N] ").strip().lower() in ("y", "yes"):
            memory_mod.remember(home, candidate["text"],
                                kind=candidate.get("kind", "fact"),
                                subject=candidate.get("subject", ""),
                                source="conversation", actor="ask")
            kept += 1
    if kept:
        ui.ok("remembered", f"{kept} thing(s)")
        ui.hint("every run from now on gets them as context:")
        ui.command("px0 memory list")


def _answer(home: Path, config: dict, question: str, decision, memories,
            args, context: str = "") -> tuple[str, list[str], str | None]:
    """Executes one route and returns (answer, source lines, run id)."""
    from px0 import ask as ask_mod

    sources: list[str] = []

    if decision.route == "brain":
        try:
            result = ask_mod.ask(home, config, question,
                                 k=getattr(args, "k", None) or 5)
        except ask_mod.AskError as e:
            ui.warn("nothing in your brain matched", str(e))
            decision.route = "answer"
        else:
            sources = [f"{p.path}#{p.anchor}" for p in result["passages"]]
            return result["answer"], sources, result["run_id"]

    if decision.route == "memory":
        if not memories:
            decision.route = "answer"
        else:
            lines = [f"- {m.subject or m.name}: {m.text}" for m in memories[:8]]
            answer = "\n".join(lines)
            return answer, [f"memory: {m.name}" for m in memories[:6]], _record_ask(
                config, question, answer, decision)

    if decision.route == "workflow" and decision.workflow:
        return _run_workflow_for(home, config, question, decision, args)

    if decision.route == "tool" and decision.tool:
        try:
            result = tools_mod.call(home, config, decision.tool, decision.args or {})
        except tools_mod.ConnectorError as e:
            ui.warn(f"{decision.tool} could not answer", str(e))
            decision.route = "answer"
        else:
            answer = route_mod.summarize_tool_result(
                config, question, decision.tool, result)
            return answer, [f"called {decision.tool}"], _record_ask(
                config, question, answer, decision)

    answer = route_mod.answer_directly(config, question, memories, context=context)
    return answer, [], _record_ask(config, question, answer, decision)


def _run_workflow_for(home: Path, config: dict, question: str, decision,
                      args) -> tuple[str, list[str], str | None]:
    """Runs the workflow a question routed to, confirming first if it writes.

    A question is not permission to act. "What does my standup look like"
    routing to the workflow that *posts* the standup must not post it, so a
    workflow with any write tool is confirmed by name before it runs -- and in
    a non-interactive shell it is refused rather than assumed.
    """
    from px0 import runner
    from px0 import workflow as workflow_mod

    wf = workflow_mod.load(home, decision.workflow)
    writes = [t for t in wf.tools if _writes(t, home)]
    if writes and not getattr(args, "yes", False):
        ui.warn(f"{wf.id} can write", ", ".join(writes))
        if not sys.stdin.isatty():
            ui.err("that needs a confirmation and stdin is not a terminal")
            ui.hint("run it deliberately, or pass --yes")
            ui.command(f"px0 workflows run {wf.id}")
            sys.exit(EXIT_USER_ERROR)
        answer = ui.prompt(f"run {wf.id}? [y/N] ").strip().lower()
        if answer not in ("y", "yes"):
            ui.info("not run")
            return "", [], None

    try:
        with ui.spinner(f"Running {wf.id}"):
            record = runner.run(home, config, wf.id, trigger="ask",
                                output_override={"target": "memory"})
    except runner.RunError as e:
        ui.err(f"{wf.id} failed", str(e))
        sys.exit(EXIT_CONNECTOR_ERROR)
    return (record.get("output", {}).get("text", ""),
            [f"ran {wf.id}"], record.get("id"))


def _writes(tool_id: str, home: Path) -> bool:
    try:
        return tools_mod.is_write(tool_id, home)
    except KeyError:
        return False


def _record_ask(config: dict, question: str, answer: str, decision) -> str | None:
    """Records an ask as a run, so it lands in the same history as everything
    else -- countable by `px0 runs stats`, and readable by the repeat detection
    that offers to turn a recurring question into a workflow."""
    from datetime import datetime, timezone

    run_id = runs_mod.new_run_id("ask")
    now = datetime.now(timezone.utc).isoformat()
    try:
        runs_mod.write_record(config, {
            "id": run_id, "workflow_id": None, "trigger": "ask",
            "start_time": now, "end_time": now, "tool_calls": [],
            "question": question, "answer": answer,
            "route": decision.as_dict(), "outcome": "success",
        })
    except (OSError, ValueError):
        return None
    return run_id


# --- px0 approvals --------------------------------------------------------

def cmd_approvals(home: Path, config: dict, args) -> None:
    """Handles `px0 approvals`: the queue of drafted write calls."""
    verb = getattr(args, "approvals_cmd", None) or "list"

    if verb == "list":
        status = None if getattr(args, "all", False) else approvals_mod.PENDING
        queue = approvals_mod.listing(home, config, status=status,
                                      workflow=getattr(args, "workflow", None))
        if getattr(args, "json", False):
            _dump(queue)
            return
        if not queue:
            ui.info("nothing waiting", "no drafted calls need a decision")
            return
        ui.heading("waiting for you")
        for item in queue:
            state = "" if item["status"] == approvals_mod.PENDING else f"  [{item['status']}]"
            ui.field(item["id"],
                     f"{item['workflow_id']} · {item['tool']}  "
                     f"{ui.dim(_age(item['created']))}{state}",
                     width=max(len(a["id"]) for a in queue))
        print(flush=True)
        ui.hint("see exactly what would be sent:")
        ui.command(f"px0 approvals show {queue[0]['id']}")
        return

    if verb == "show":
        try:
            item = approvals_mod.read(home, args.approval_id)
        except approvals_mod.ApprovalError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if getattr(args, "json", False):
            _dump(item)
            return
        _print_approval(item)
        if item["status"] == approvals_mod.PENDING:
            print(flush=True)
            ui.hint("send it, or throw it away:")
            ui.command(f"px0 approvals approve {item['id']}")
            ui.command(f"px0 approvals edit {item['id']}")
            ui.command(f'px0 approvals reject {item["id"]} --reason "why"')
        return

    if verb == "edit":
        try:
            item = approvals_mod.read(home, args.approval_id)
        except approvals_mod.ApprovalError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        current = dict(item.get("args") or {})
        pairs = getattr(args, "set", None) or []
        if pairs:
            for pair in pairs:
                if "=" not in pair:
                    ui.err(f"--set expects key=value, got {pair!r}")
                    sys.exit(EXIT_USER_ERROR)
                key, _, raw = pair.partition("=")
                current[key.strip()] = _coerce_arg(raw)
        else:
            edited = _edit_json(current)
            if edited is None:
                ui.info("unchanged")
                return
            current = edited
        try:
            item = approvals_mod.amend(home, args.approval_id, current,
                                       note=getattr(args, "note", "") or "")
        except approvals_mod.ApprovalError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        ui.ok("edited", item["id"])
        _print_approval(item)
        ui.hint("send what you just wrote:")
        ui.command(f"px0 approvals approve {item['id']}")
        return

    if verb == "approve":
        try:
            item = approvals_mod.read(home, args.approval_id)
        except approvals_mod.ApprovalError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if not getattr(args, "yes", False):
            _print_approval(item)
            print(flush=True)
            if not sys.stdin.isatty():
                ui.err("that needs a confirmation and stdin is not a terminal")
                ui.hint("pass --yes to send it without being asked")
                sys.exit(EXIT_USER_ERROR)
            if ui.prompt("send this? [y/N] ").strip().lower() not in ("y", "yes"):
                ui.info("not sent")
                return
        try:
            with ui.spinner(f"Calling {item['tool']}"):
                done = approvals_mod.approve(home, config, args.approval_id)
        except approvals_mod.ApprovalError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if done["status"] == approvals_mod.APPROVED:
            ui.ok("sent", f"{done['tool']} · {done['id']}")
            return
        ui.err("not sent", done.get("detail", "the tool call failed"))
        ui.hint("the draft is kept as failed; nothing was retried on its own")
        sys.exit(EXIT_CONNECTOR_ERROR)

    if verb == "reject":
        try:
            done = approvals_mod.reject(home, config, args.approval_id,
                                        reason=getattr(args, "reason", "") or "")
        except approvals_mod.ApprovalError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        ui.ok("discarded", done["id"])
        if not done.get("detail"):
            ui.hint("a reason is what tells `px0 workflows improve` why:")
            ui.command(f'px0 approvals reject {done["id"]} --reason "wrong channel"')
        return

    if verb == "purge":
        removed = approvals_mod.purge(home, config, keep_days=getattr(args, "days", None))
        ui.ok("purged", f"{removed} resolved approval(s)")
        return


def _coerce_arg(raw: str):
    """Reads a `--set key=value` value as JSON where it is JSON, text otherwise.

    So `--set count=3` sets a number and `--set channel=#ops` sets a string,
    without the user having to know which one the tool wants.
    """
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return raw


def _edit_json(value: dict) -> dict | None:
    """Opens a dict in the user's editor and reads it back.

    Returns None when nothing changed or the file came back unparseable -- a
    drafted call is about to be sent, so half-edited JSON is refused rather
    than guessed at.
    """
    import subprocess
    import tempfile
    from px0 import cli as cli_mod

    before = json.dumps(value, indent=2, default=str)
    with tempfile.NamedTemporaryFile("w+", suffix=".json", delete=False) as f:
        f.write(before + "\n")
        path = f.name
    if not cli_mod._open_in_editor(Path(path)):
        ui.err("no editor available", "pass --set key=value instead")
        return None
    try:
        after = Path(path).read_text()
    finally:
        Path(path).unlink(missing_ok=True)
    if after.strip() == before.strip():
        return None
    try:
        parsed = json.loads(after)
    except json.JSONDecodeError as e:
        ui.err("that is not valid JSON, so nothing was changed", str(e))
        return None
    if not isinstance(parsed, dict):
        ui.err("the arguments must stay a JSON object")
        return None
    return parsed


def _print_approval(item: dict) -> None:
    """Shows a drafted call the way a person needs to judge it: what would be
    sent, in full, and what produced it."""
    heading = "waiting" if item["status"] == approvals_mod.PENDING else item["status"]
    ui.heading(f"{heading} {ui.accent(item['tool'])}")
    ui.kv("from", f"{item['workflow_id']} · run {item['run_id']}")
    ui.kv("drafted", _age(item["created"]))
    if item.get("reason"):
        ui.kv("workflow", item["reason"])
    if item.get("detail"):
        ui.kv("note", item["detail"])
    if item.get("edits"):
        # What goes out is not what the run wrote, and the screen has to say so.
        ui.kv("edited", f"{len(item['edits'])} time(s) by you")
    print(flush=True)
    print(f"  {ui.dim('arguments')}", flush=True)
    for line in json.dumps(item.get("args") or {}, indent=2,
                           default=str).splitlines():
        print(f"    {line}", flush=True)
    if item.get("output_preview"):
        print(flush=True)
        print(f"  {ui.dim('the run produced')}", flush=True)
        for line in item["output_preview"].splitlines()[:20]:
            print(f"    {ui.dim(line)}", flush=True)


# --- px0 inbox ------------------------------------------------------------

def cmd_inbox(home: Path, config: dict, args) -> None:
    """Handles `px0 inbox`: what your scheduled workflows produced."""
    verb = getattr(args, "inbox_cmd", None) or "list"

    if verb == "list":
        status = None if getattr(args, "all", False) else inbox_mod.UNREAD
        entries = inbox_mod.listing(home, status=status,
                                    workflow=getattr(args, "workflow", None))
        if getattr(args, "json", False):
            _dump(entries)
            return
        if not entries:
            ui.info("nothing new", "your scheduled workflows have delivered nothing unread")
            return
        ui.heading(f"{len(entries)} waiting" if status else "everything delivered")
        width = max(len(e["id"]) for e in entries)
        for entry in entries:
            state = "" if entry["status"] == inbox_mod.UNREAD else f"  [{entry['status']}]"
            ui.field(entry["id"],
                     f"{entry['title']}  {ui.dim(entry['workflow_id'])} "
                     f"{ui.dim(_age(entry['created']))}{state}", width=width)
        print(flush=True)
        ui.hint("read one:")
        ui.command(f"px0 inbox read {entries[0]['id']}")
        return

    if verb == "read":
        try:
            entry = (inbox_mod.read_entry(home, args.entry_id) if args.entry_id
                     else _first_unread(home))
        except inbox_mod.InboxError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        if entry is None:
            ui.info("nothing new")
            return
        text = inbox_mod.body(home, config, entry)
        if getattr(args, "json", False):
            _dump({**entry, "body": text})
            return
        ui.heading(entry["title"])
        ui.kv("from", f"{entry['workflow_id']} · {_age(entry['created'])}")
        if entry.get("path"):
            ui.kv("file", entry["path"])
        print(flush=True)
        ui.render_markdown(text)
        inbox_mod.mark(home, entry["id"], inbox_mod.READ)
        return

    if verb == "archive":
        try:
            inbox_mod.mark(home, args.entry_id, inbox_mod.ARCHIVED)
        except inbox_mod.InboxError as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        ui.ok("archived", args.entry_id)
        return

    if verb == "clear":
        removed = inbox_mod.clear(home, status=None if getattr(args, "all", False)
                                  else inbox_mod.READ)
        ui.ok("cleared", f"{removed} entr{'y' if removed == 1 else 'ies'}")
        return


def _first_unread(home: Path):
    entries = inbox_mod.listing(home)
    return entries[-1] if entries else None


# --- px0 memory -----------------------------------------------------------

def cmd_memory(home: Path, config: dict, args) -> None:
    """Handles `px0 memory`: what px0 knows about you."""
    verb = getattr(args, "memory_cmd", None) or "list"

    if verb == "list":
        memories = sorted(memory_mod.load_all(home).values(), key=lambda m: m.name)
        if getattr(args, "json", False):
            _dump([{"name": m.name, "kind": m.kind, "subject": m.subject,
                    "text": m.text, "pinned": m.pinned} for m in memories])
            return
        if not memories:
            ui.info("px0 remembers nothing yet")
            ui.hint("tell it something:")
            ui.command('px0 memory add "standup goes out before 09:30"')
            return
        ui.heading(f"{len(memories)} remembered")
        width = max(len(m.name) for m in memories)
        kind_width = max(len(m.kind) for m in memories)
        for m in memories:
            pin = ui.accent(" *") if m.pinned else ""
            ui.field(m.name, f"{ui.dim(m.kind.ljust(kind_width))}  {m.summary}{pin}",
                     width=width)
        print(flush=True)
        ui.hint("these are plain files -- open, correct, or delete any of them:")
        ui.command("px0 memory show <name>")
        return

    if verb == "add":
        try:
            entry = memory_mod.remember(
                home, args.text, kind=getattr(args, "kind", None) or "fact",
                subject=getattr(args, "subject", None) or "",
                pinned=getattr(args, "pin", False), source="user")
        except memory_mod.MemoryError_ as e:
            ui.err(str(e))
            sys.exit(EXIT_USER_ERROR)
        ui.ok("remembered", entry.name)
        ui.hint("every run from now on gets this as context")
        return

    if verb == "suggest":
        try:
            with ui.spinner("Reading your corrections"):
                candidates = memory_mod.suggest(home, config)
        except Exception as e:
            ui.err("could not work that out", str(e))
            sys.exit(EXIT_MODEL_ERROR)
        if getattr(args, "json", False):
            _dump(candidates)
            return
        if not candidates:
            ui.info("nothing to suggest",
                    "px0 learns these from runs you mark bad and from corrections "
                    "you make in a conversation")
            ui.command('px0 runs mark <run-id> --bad "what was wrong"')
            return
        ui.heading(f"{len(candidates)} worth remembering?")
        ui.remark("px0 only keeps what you agree to. Each becomes a file you "
                  "can edit or delete.")
        kept = 0
        for candidate in candidates:
            ui.bullet(candidate["text"])
            if candidate.get("why"):
                ui.field("from", candidate["why"], width=4)
            if getattr(args, "yes", False) or (
                    sys.stdin.isatty()
                    and ui.prompt("remember this? [y/N] ").strip().lower() in ("y", "yes")):
                memory_mod.remember(home, candidate["text"],
                                    kind=candidate.get("kind", "fact"),
                                    subject=candidate.get("subject", ""),
                                    source="suggested", actor="suggest")
                kept += 1
        ui.ok("remembered", f"{kept} of {len(candidates)}")
        return

    if verb == "show":
        memories = memory_mod.load_all(home)
        entry = memories.get(args.name)
        if entry is None:
            ui.err(f"px0 remembers nothing called {args.name!r}")
            ui.hint("list them with: px0 memory list")
            sys.exit(EXIT_USER_ERROR)
        if getattr(args, "json", False):
            _dump({"name": entry.name, "kind": entry.kind, "subject": entry.subject,
                   "text": entry.text, "learned": entry.learned,
                   "source": entry.source, "pinned": entry.pinned})
            return
        ui.heading(entry.name)
        ui.kv("kind", entry.kind)
        if entry.subject:
            ui.kv("subject", entry.subject)
        if entry.learned:
            ui.kv("learned", entry.learned)
        if entry.source:
            ui.kv("from", entry.source)
        print(flush=True)
        ui.say(entry.text)
        return

    if verb == "forget":
        if memory_mod.forget(home, args.name, actor="user"):
            ui.ok("forgotten", args.name)
            ui.hint("it is still in history:")
            ui.command("px0 changes list")
            return
        ui.err(f"px0 remembers nothing called {args.name!r}")
        sys.exit(EXIT_USER_ERROR)

    if verb == "search":
        # Ranked by relevance alone: pinning says what a *run* should always
        # see, and letting it outrank the query here answers a question nobody
        # asked.
        found = memory_mod.relevant(home, args.query, pinned_first=False)
        if getattr(args, "json", False):
            _dump([{"name": m.name, "subject": m.subject, "text": m.text}
                   for m in found])
            return
        if not found:
            ui.info("nothing remembered matches that")
            return
        for m in found:
            ui.bullet(f"{ui.accent(m.name)}  {m.summary}")
        return
