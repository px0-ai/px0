"""Write calls that wait for a person.

The gate has to hold in exactly one direction: a held-back write must never
fire on its own, and an approved one must send precisely what was shown. Most
of what follows is about the second half, because the first is easy to get
right and the second is where an approval queue stops being trustworthy -- an
"approve" that re-runs the workflow sends something the user never read.
"""

from datetime import datetime, timedelta, timezone

import pytest

from px0 import approvals, config as config_mod, harness, paths, runner
from px0 import runs as runs_mod
from px0 import tools
from px0 import workflow as workflow_mod


@pytest.fixture
def config(tmp_home, tmp_path):
    cfg = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(cfg, "logs.path", str(tmp_path / "logs"))
    return cfg


def _write(tmp_home, wf_id="demo", extra="", tools_block="tools:\n  - http.post\n"):
    (paths.workflows_dir(tmp_home) / f"{wf_id}.md").write_text(
        "---\n"
        f"id: {wf_id}\n"
        "description: A demo that posts\n"
        f"{tools_block}"
        f"{extra}"
        "output:\n  target: stdout\n"
        "---\n\nBody.\n")
    return workflow_mod.load(tmp_home, wf_id)


@pytest.fixture
def loop(tmp_home, config, monkeypatch):
    """The tool loop with a recording tool layer and a scripted harness."""
    executed = []
    monkeypatch.setattr(tools, "call",
                        lambda home, cfg, tid, args: executed.append((tid, args)) or {"ok": True})
    monkeypatch.setattr(runs_mod, "append_raw_log", lambda *a: None)

    def _run(wf, turns, dry_run=False):
        script = list(turns)
        monkeypatch.setattr(
            harness, "invoke_detailed",
            lambda *a, **kw: harness.Reply(text=script.pop(0) if script else "Done"))
        out, calls, usage = runner._tool_call_loop(
            tmp_home, config, "prompt", list(wf.tools), dry_run, 60.0,
            "run_20260101-000000-aaaa", wf=wf)
        return {"output": out, "calls": calls, "usage": usage, "executed": executed}

    return _run


POST = 'TOOL_CALL: {"tool": "http.post", "args": {"url": "https://x/y", "body": {"t": 1}}}'


# --- what needs approval --------------------------------------------------

def test_a_read_tool_never_waits(tmp_home, config):
    """A queue that fills up with searches is one nobody reads."""
    wf = _write(tmp_home, tools_block="tools:\n  - http.get\n", extra="confirm: true\n")
    assert approvals.needs_approval(wf, config, "http.get", is_write=False) is False


def test_a_workflow_can_ask_even_when_the_store_does_not(tmp_home, config):
    wf = _write(tmp_home, extra="confirm: true\n")
    assert approvals.needs_approval(wf, config, "http.post", is_write=True) is True


def test_a_workflow_can_be_exempt_when_the_store_asks(tmp_home, config):
    """The override has to work in both directions, or a store-wide default is
    a setting nobody can turn on."""
    config_mod.set_key(config, "tools.confirm_writes", "true")
    wf = _write(tmp_home, extra="confirm: false\n")
    assert approvals.needs_approval(wf, config, "http.post", is_write=True) is False


def test_a_workflow_can_name_only_some_of_its_writes(tmp_home, config):
    wf = _write(tmp_home,
                tools_block="tools:\n  - http.post\n  - file.write\n",
                extra="confirm:\n  - http.post\n")
    assert approvals.needs_approval(wf, config, "http.post", True) is True
    assert approvals.needs_approval(wf, config, "file.write", True) is False


def test_confirming_a_tool_the_workflow_cannot_call_is_refused(tmp_home):
    """Silent in the dangerous direction: a misspelled tool would leave the one
    the user meant to hold back firing without asking, and nothing would say so."""
    wf = _write(tmp_home, extra="confirm:\n  - slack.post_message\n")
    errors = workflow_mod.validate(wf, tmp_home)
    assert any("not in this workflow's tools" in e for e in errors)


@pytest.mark.parametrize("value", ["maybe", "3"])
def test_a_nonsense_confirm_value_fails_validation(tmp_home, value):
    wf = _write(tmp_home, extra=f"confirm: {value}\n")
    assert workflow_mod.validate(wf, tmp_home)


# --- the gate holds -------------------------------------------------------

def test_a_held_back_write_is_not_executed(tmp_home, config, loop):
    wf = _write(tmp_home, extra="confirm: true\n")
    result = loop(wf, [POST, "Done"])
    assert result["executed"] == []


def test_the_call_is_queued_in_full(tmp_home, config, loop):
    wf = _write(tmp_home, extra="confirm: true\n")
    loop(wf, [POST, "Done"])
    queue = approvals.listing(tmp_home, config)
    assert len(queue) == 1
    assert queue[0]["tool"] == "http.post"
    assert queue[0]["args"]["url"] == "https://x/y"
    assert queue[0]["workflow_id"] == "demo"


def test_the_run_records_what_it_queued(tmp_home, config, loop):
    wf = _write(tmp_home, extra="confirm: true\n")
    result = loop(wf, [POST, "Done"])
    assert result["usage"]["approvals"][0]["status"] == approvals.PENDING
    assert result["calls"][0]["queued"] is True


def test_the_run_still_finishes_and_produces_output(tmp_home, config, loop):
    """The point of drafting rather than failing: the work still happens, and
    only the part that leaves a mark waits."""
    wf = _write(tmp_home, extra="confirm: true\n")
    result = loop(wf, [POST, "the digest"])
    assert result["output"] == "the digest"


def test_an_unconfirmed_write_still_fires(tmp_home, config, loop):
    wf = _write(tmp_home)
    result = loop(wf, [POST, "Done"])
    assert [t for t, _ in result["executed"]] == ["http.post"]
    assert approvals.listing(tmp_home, config) == []


def test_a_dry_run_stubs_rather_than_queueing(tmp_home, config, loop):
    """Two different ideas: a rehearsal is not a decision waiting to be made,
    and filling the queue from one would leave drafts nobody asked to send."""
    wf = _write(tmp_home, extra="confirm: true\n")
    result = loop(wf, [POST, "Done"], dry_run=True)
    assert result["calls"][0]["stubbed"] is True
    assert approvals.listing(tmp_home, config) == []


# --- acting on the queue --------------------------------------------------

def test_approving_sends_exactly_what_was_shown(tmp_home, config, monkeypatch):
    """It calls the tool with the recorded arguments rather than re-running the
    workflow, which would draft something else."""
    sent = []
    monkeypatch.setattr(tools, "call",
                        lambda home, cfg, tid, args: sent.append((tid, args)) or {"ok": True})
    item = approvals.queue(tmp_home, run_id="run_20260101-000000-aaaa",
                           workflow_id="demo", tool="http.post",
                           args={"url": "https://x/y"})
    done = approvals.approve(tmp_home, config, item["id"])
    assert done["status"] == approvals.APPROVED
    assert sent == [("http.post", {"url": "https://x/y"})]


def test_rejecting_sends_nothing_and_keeps_the_reason(tmp_home, config, monkeypatch):
    monkeypatch.setattr(tools, "call",
                        lambda *a: pytest.fail("a rejected call must not fire"))
    item = approvals.queue(tmp_home, run_id="r", workflow_id="demo",
                           tool="http.post", args={})
    done = approvals.reject(tmp_home, config, item["id"], reason="wrong channel")
    assert done["status"] == approvals.REJECTED
    assert done["detail"] == "wrong channel"


def test_a_failed_call_does_not_return_to_the_queue(tmp_home, config, monkeypatch):
    """Back in `pending` would mean it gets approved twice; retrying is the
    user's decision to make."""
    def boom(*a):
        raise tools.ConnectorError("slack is down")
    monkeypatch.setattr(tools, "call", boom)
    item = approvals.queue(tmp_home, run_id="r", workflow_id="demo",
                           tool="http.post", args={})
    done = approvals.approve(tmp_home, config, item["id"])
    assert done["status"] == approvals.FAILED
    assert "slack is down" in done["detail"]
    assert approvals.listing(tmp_home, config) == []


@pytest.mark.parametrize("verb", [approvals.approve, approvals.reject])
def test_acting_twice_is_refused(tmp_home, config, monkeypatch, verb):
    monkeypatch.setattr(tools, "call", lambda *a: {"ok": True})
    item = approvals.queue(tmp_home, run_id="r", workflow_id="demo",
                           tool="http.post", args={})
    verb(tmp_home, config, item["id"])
    with pytest.raises(approvals.ApprovalError):
        verb(tmp_home, config, item["id"])


def test_acting_on_something_that_does_not_exist_says_so(tmp_home, config):
    with pytest.raises(approvals.ApprovalError):
        approvals.approve(tmp_home, config, "apr_20260101-000000-zzzz")


def test_the_outcome_lands_back_on_the_run(tmp_home, config, monkeypatch):
    """Without this a run says a call was queued and never says what came of
    it, so `px0 runs why` stops short of the answer."""
    monkeypatch.setattr(tools, "call", lambda *a: {"ok": True})
    run_id = runs_mod.new_run_id()
    item = approvals.queue(tmp_home, run_id=run_id, workflow_id="demo",
                           tool="http.post", args={})
    runs_mod.write_record(config, {
        "id": run_id, "workflow_id": "demo", "outcome": "success",
        "start_time": datetime.now(timezone.utc).isoformat(),
        "approvals": [{"id": item["id"], "tool": "http.post",
                       "status": approvals.PENDING}],
    })
    approvals.approve(tmp_home, config, item["id"])
    record = runs_mod.read_record(config, run_id)
    assert record["approvals"][0]["status"] == approvals.APPROVED


# --- staleness ------------------------------------------------------------

def test_an_old_draft_stops_being_sendable(tmp_home, config):
    """A message written on Tuesday should not go out on Friday because it was
    still sitting in the queue."""
    item = approvals.queue(tmp_home, run_id="r", workflow_id="demo",
                           tool="http.post", args={})
    item["created"] = (datetime.now(timezone.utc) - timedelta(days=30)).isoformat()
    approvals.write(tmp_home, item)

    assert approvals.listing(tmp_home, config) == []
    assert approvals.read(tmp_home, item["id"])["status"] == approvals.EXPIRED
    with pytest.raises(approvals.ApprovalError):
        approvals.approve(tmp_home, config, item["id"])


def test_expiry_can_be_turned_off(tmp_home, config):
    config_mod.set_key(config, "approvals.expire_days", "0")
    item = approvals.queue(tmp_home, run_id="r", workflow_id="demo",
                           tool="http.post", args={})
    item["created"] = (datetime.now(timezone.utc) - timedelta(days=400)).isoformat()
    approvals.write(tmp_home, item)
    assert len(approvals.listing(tmp_home, config)) == 1


def test_purge_keeps_what_is_still_waiting(tmp_home, config):
    """The queue is the point; a purge that emptied it would be a purge that
    silently cancelled decisions."""
    waiting = approvals.queue(tmp_home, run_id="r", workflow_id="demo",
                              tool="http.post", args={})
    old = approvals.queue(tmp_home, run_id="r", workflow_id="demo",
                          tool="http.post", args={})
    old.update(status=approvals.REJECTED,
               resolved=(datetime.now(timezone.utc) - timedelta(days=90)).isoformat())
    approvals.write(tmp_home, old)

    assert approvals.purge(tmp_home, config) == 1
    assert [a["id"] for a in approvals.listing(tmp_home, config)] == [waiting["id"]]


def test_pending_count_is_what_status_reports(tmp_home, config):
    approvals.queue(tmp_home, run_id="r", workflow_id="demo", tool="http.post", args={})
    assert approvals.pending_count(tmp_home, config) == 1


# --- being told about it --------------------------------------------------

def test_an_unattended_run_says_it_is_waiting(tmp_home, config, monkeypatch):
    """A drafted call waits indefinitely by definition, and nobody was there to
    see the run happen -- so silence here is a message the user believes went
    out."""
    from px0 import notify as notify_mod

    told = {}

    def record_notice(home, cfg, rec, queued, wf_on_failure=None):
        told["queued"] = queued
        return {"notified": True, "channel": "desktop"}

    monkeypatch.setattr(notify_mod, "on_approval", record_notice)
    _write(tmp_home, extra="confirm: true\n")
    monkeypatch.setattr(tools, "call", lambda *a: {"ok": True})
    script = [POST, "the digest"]
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text=script.pop(0) if script else "done"))

    record = runner.run(tmp_home, config, "demo", trigger="schedule")
    assert record["approval_notified"]["notified"] is True
    assert told["queued"][0]["tool"] == "http.post"


def test_a_manual_run_is_not_notified_about(tmp_home, config, monkeypatch):
    """You are sitting there, and `px0 workflows run` already prints it."""
    from px0 import notify as notify_mod

    monkeypatch.setattr(notify_mod, "on_approval",
                        lambda *a, **kw: pytest.fail("no notification for a manual run"))
    _write(tmp_home, extra="confirm: true\n")
    monkeypatch.setattr(tools, "call", lambda *a: {"ok": True})
    script = [POST, "the digest"]
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text=script.pop(0) if script else "done"))
    runner.run(tmp_home, config, "demo", trigger="manual")


def test_the_approval_notice_falls_back_to_the_failure_policy(tmp_home, config):
    """A store that already said how it wants to hear about failures should not
    have to say it twice."""
    from px0 import notify as notify_mod

    config_mod.set_key(config, "notify.on_failure", "none")
    result = notify_mod.on_approval(tmp_home, config, {"workflow_id": "demo"},
                                    [{"id": "a", "tool": "http.post"}])
    assert result["channel"] == "none"


def test_nothing_queued_means_nothing_said(tmp_home, config):
    from px0 import notify as notify_mod

    assert notify_mod.on_approval(tmp_home, config, {}, [])["notified"] is False


def test_the_output_is_attached_to_the_draft_after_the_run(tmp_home, config, monkeypatch):
    """A write is drafted before the run has an answer, and the answer is what a
    person needs in order to judge the call."""
    _write(tmp_home, extra="confirm: true\n")
    monkeypatch.setattr(tools, "call", lambda *a: {"ok": True})
    script = [POST, "## Standup\n\n- shipped the queue"]
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text=script.pop(0) if script else "done"))
    runner.run(tmp_home, config, "demo", trigger="manual")
    queued = approvals.listing(tmp_home, config)
    assert "shipped the queue" in queued[0]["output_preview"]


# --- reading replies from a channel ---------------------------------------

@pytest.mark.parametrize("text, verdict", [
    ("approve apr_20260826-101010-ab12", approvals.APPROVED),
    ("yes apr_20260826-101010-ab12", approvals.APPROVED),
    ("@px0 approve apr_20260826-101010-ab12", approvals.APPROVED),
    ("reject apr_20260826-101010-ab12", approvals.REJECTED),
    ("no apr_20260826-101010-ab12", approvals.REJECTED),
])
def test_a_reply_is_read(text, verdict):
    spec = {"senders": {"arpit"}, "text_field": "", "sender_field": ""}
    got = approvals.parse_replies([{"text": text, "user": "arpit"}], spec)
    assert got[0]["verdict"] == verdict


@pytest.mark.parametrize("text", [
    "do not approve apr_20260826-101010-ab12",
    "I don't think we should approve apr_20260826-101010-ab12 yet",
    "did anyone approve apr_20260826-101010-ab12?",
])
def test_a_reply_that_merely_mentions_approving_is_not_one(text):
    """This matched anywhere in the message, so a trusted person writing "do
    not approve" sent the message they had just declined. Anchoring means an
    ambiguous sentence matches nothing, and matching nothing does nothing."""
    spec = {"senders": {"arpit"}, "text_field": "", "sender_field": ""}
    assert approvals.parse_replies([{"text": text, "user": "arpit"}], spec) == []


def test_a_reply_from_an_untrusted_sender_is_not_acted_on(tmp_home, config):
    spec = {"senders": {"arpit"}, "text_field": "", "sender_field": ""}
    got = approvals.parse_replies(
        [{"text": "approve apr_20260826-101010-ab12", "user": "someone-else"}], spec)
    assert got[0]["verdict"] is None


def test_replies_need_both_a_tool_and_a_sender_list(config):
    """Half-configured is an approval queue anyone able to post there could
    empty, so it is refused rather than run."""
    config_mod.set_key(config, "approvals.reply_tool", "slack.read_channel")
    assert approvals.reply_config(config) is None
    config_mod.set_key(config, "approvals.reply_from", "arpit")
    assert approvals.reply_config(config)["tool"] == "slack.read_channel"
