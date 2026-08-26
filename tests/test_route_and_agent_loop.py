"""The front door, the harness-driven loop, and the clock.

Three separate things, tested together because each is about px0 handing
control somewhere else and needing the guarantees to survive the handover: to
a router that picks who answers, to a harness that runs its own tool loop, and
to a timezone that is not the machine's.

The recurring assertion is that a bad answer from the other side degrades
rather than escapes -- a router that names a tool nobody has falls back to
answering, and a scoped MCP server refuses a call the workflow never
allowlisted.
"""

import json

import pytest

from px0 import config as config_mod, harness, mcp, memory, paths, route, runner
from px0 import runs as runs_mod
from px0 import tools
from px0 import workflow as workflow_mod


@pytest.fixture
def config(tmp_home, tmp_path):
    cfg = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(cfg, "logs.path", str(tmp_path / "logs"))
    return cfg


def _write(tmp_home, wf_id="demo", tools_block="", extra=""):
    (paths.workflows_dir(tmp_home) / f"{wf_id}.md").write_text(
        f"---\nid: {wf_id}\ndescription: Summarize my pull requests\n"
        f"{tools_block}{extra}output:\n  target: stdout\n---\n\nBody.\n")
    return workflow_mod.load(tmp_home, wf_id)


def _reply(payload):
    return lambda *a, **kw: json.dumps(payload)


# --- what the router is shown ---------------------------------------------

def test_only_read_tools_are_offered(tmp_home, config):
    """A router one bad classification away from sending something would undo
    the whole argument for a front door being safe to ask."""
    index = route.candidates(tmp_home, config)
    offered = {t["id"] for t in index["tools"]}
    assert "file.read" in offered
    assert "http.post" not in offered
    assert "shell.run" not in offered


def test_disabled_workflows_are_not_offered(tmp_home, config):
    _write(tmp_home, "parked", extra="enabled: false\n")
    _write(tmp_home, "live")
    ids = {w["id"] for w in route.candidates(tmp_home, config)["workflows"]}
    assert ids == {"live"}


def test_a_workflow_that_writes_is_flagged_as_such(tmp_home, config):
    _write(tmp_home, "poster", tools_block="tools:\n  - http.post\n")
    row = next(w for w in route.candidates(tmp_home, config)["workflows"]
               if w["id"] == "poster")
    assert row["writes"] is True


# --- reading the router's answer ------------------------------------------

def test_a_clean_decision_is_read(config, monkeypatch):
    monkeypatch.setattr(harness, "invoke", _reply(
        {"route": "brain", "reason": "it is in something you read",
         "confidence": "high"}))
    decision = route.decide(config, "what did that post say?", {"workflows": [], "tools": []})
    assert decision.route == "brain"
    assert decision.confidence == "high"


@pytest.mark.parametrize("payload, why", [
    ({"route": "teleport"}, "a route px0 does not have"),
    ({"route": "workflow", "workflow": "nonexistent"}, "a workflow nobody has"),
    ({"route": "tool", "tool": "nonexistent.thing"}, "a tool nobody has"),
])
def test_a_bad_decision_degrades_to_answering(config, monkeypatch, payload, why):
    """The worst outcome of a bad route should be a plain reply, never a
    failure to answer at all."""
    monkeypatch.setattr(harness, "invoke", _reply(payload))
    decision = route.decide(config, "anything", {"workflows": [], "tools": []})
    assert decision.route == "answer", why


def test_an_answer_that_is_not_json_degrades_to_answering(config, monkeypatch):
    monkeypatch.setattr(harness, "invoke", lambda *a, **kw: "I think the brain?")
    assert route.decide(config, "anything", {}).route == "answer"


def test_a_harness_failure_is_raised_rather_than_guessed_at(config, monkeypatch):
    def boom(*a, **kw):
        raise harness.HarnessError("not authenticated")
    monkeypatch.setattr(harness, "invoke", boom)
    with pytest.raises(route.RouteError):
        route.decide(config, "anything", {})


def test_a_known_workflow_and_tool_survive_the_check(config, monkeypatch):
    index = {"workflows": [{"id": "digest"}], "tools": [{"id": "file.read"}]}
    monkeypatch.setattr(harness, "invoke", _reply(
        {"route": "workflow", "workflow": "digest"}))
    assert route.decide(config, "q", index).workflow == "digest"
    monkeypatch.setattr(harness, "invoke", _reply(
        {"route": "tool", "tool": "file.read", "args": {"path": "x"}}))
    decision = route.decide(config, "q", index)
    assert decision.tool == "file.read" and decision.args == {"path": "x"}


# --- noticing a question you keep asking ----------------------------------

def test_a_question_asked_repeatedly_is_noticed(tmp_home, config):
    """The observation worth surfacing is not that a question repeats, but that
    the user keeps doing by hand what px0 could do on a schedule."""
    from datetime import datetime, timezone

    for i in range(3):
        runs_mod.write_record(config, {
            "id": runs_mod.new_run_id("ask"), "workflow_id": None,
            "trigger": "ask", "outcome": "success",
            "start_time": datetime.now(timezone.utc).isoformat(),
            "question": "which pull requests did I review this week",
        })
    found = route.repeated_questions(config, "which pull requests did I review this week")
    assert len(found) >= 2


def test_a_one_off_question_is_not(tmp_home, config):
    from datetime import datetime, timezone

    runs_mod.write_record(config, {
        "id": runs_mod.new_run_id("ask"), "workflow_id": None, "trigger": "ask",
        "outcome": "success", "start_time": datetime.now(timezone.utc).isoformat(),
        "question": "something else entirely about kubernetes",
    })
    assert route.repeated_questions(config, "which pull requests did I review") == []


# --- the scoped MCP server ------------------------------------------------

def _scope(tmp_path, tools_list, **kw):
    scope = {"run_id": "run_20260101-000000-aaaa", "workflow_id": "demo",
             "tools": list(tools_list), "confirm_tools": [], "dry_run": False,
             "calls_path": str(tmp_path / "calls.jsonl")}
    scope.update(kw)
    return scope


def test_the_scoped_server_offers_only_this_runs_tools(tmp_home, tmp_path):
    scope = _scope(tmp_path, ["file.read"])
    names = {d["name"] for d in mcp.scope_tool_definitions(tmp_home, scope)}
    assert names == {"file_read"}


def test_the_scoped_server_describes_parameters(tmp_home, tmp_path):
    defs = mcp.scope_tool_definitions(tmp_home, _scope(tmp_path, ["file.read"]))
    schema = defs[0]["inputSchema"]
    assert schema["properties"]["path"]["type"] == "string"
    assert schema["required"] == ["path"]


def test_a_tool_outside_the_scope_is_refused(tmp_home, config, tmp_path, monkeypatch):
    """The allowlist moved into the server when the loop moved out; it has to
    still be the thing that decides."""
    monkeypatch.setattr(tools, "call", lambda *a: pytest.fail("must not be called"))
    scope = _scope(tmp_path, ["file.read"])
    result = mcp.call_scoped(tmp_home, config, scope, "http_post", {"url": "x"})
    assert result["isError"] is True
    assert "allowlist" in result["content"][0]["text"]


def test_a_scoped_call_runs_and_is_recorded(tmp_home, config, tmp_path, monkeypatch):
    monkeypatch.setattr(tools, "call", lambda home, cfg, tid, args: {"ok": tid})
    scope = _scope(tmp_path, ["file.read"])
    result = mcp.call_scoped(tmp_home, config, scope, "file_read", {"path": "x"})
    assert result["isError"] is False
    recorded = [json.loads(l) for l in
                (tmp_path / "calls.jsonl").read_text().splitlines()]
    assert recorded[0]["tool"] == "file.read"


def test_a_scoped_dry_run_stubs_a_write(tmp_home, config, tmp_path, monkeypatch):
    monkeypatch.setattr(tools, "call", lambda *a: pytest.fail("dry runs call nothing"))
    scope = _scope(tmp_path, ["http.post"], dry_run=True)
    result = mcp.call_scoped(tmp_home, config, scope, "http_post", {"url": "x"})
    assert "stubbed" in result["content"][0]["text"]


def test_a_scoped_held_back_write_is_queued(tmp_home, config, tmp_path, monkeypatch):
    from px0 import approvals

    monkeypatch.setattr(tools, "call", lambda *a: pytest.fail("must wait for a person"))
    scope = _scope(tmp_path, ["http.post"], confirm_tools=["http.post"])
    mcp.call_scoped(tmp_home, config, scope, "http_post", {"url": "x"})
    assert len(approvals.listing(tmp_home, config)) == 1


def test_a_scoped_tool_failure_is_reported_not_raised(tmp_home, config, tmp_path, monkeypatch):
    def boom(*a):
        raise tools.ConnectorError("github is down")
    monkeypatch.setattr(tools, "call", boom)
    result = mcp.call_scoped(tmp_home, config, _scope(tmp_path, ["file.read"]),
                             "file_read", {"path": "x"})
    assert result["isError"] is True


def test_the_full_server_is_not_reachable_from_a_scope(tmp_home, config, tmp_path):
    """A scoped server serves one run. Exposing the brain and every workflow to
    a client that asked for one workflow's tools would widen the run past its
    own allowlist."""
    scope = _scope(tmp_path, ["file.read"])
    listed = mcp.handle(tmp_home, config,
                        {"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
                        allow_runs=True, scope=scope)
    names = {t["name"] for t in listed["result"]["tools"]}
    assert names == {"file_read"}
    assert "brain_ask" not in names


# --- choosing a loop ------------------------------------------------------

def test_px0_drives_its_own_loop_by_default(config):
    assert runner.agent_loop_mode(config) == "builtin"


@pytest.mark.parametrize("value, expected", [
    ("mcp", "mcp"), ("auto", "auto"), ("builtin", "builtin"), ("nonsense", "builtin"),
])
def test_the_loop_setting_is_read_defensively(config, value, expected):
    config["model"]["agent_loop"] = value
    assert runner.agent_loop_mode(config) == expected


def test_a_harness_with_no_verified_flags_cannot_be_handed_tools():
    assert harness.supports_agent_loop("claude -p") is True
    assert harness.supports_agent_loop("my-own-agent") is False
    assert harness.agent_flags("my-own-agent", "/tmp/c.json", ["x"]) == []


def test_asking_for_the_mcp_loop_on_an_unsupported_harness_fails_loudly(
        tmp_home, config, monkeypatch):
    """`auto` means "where it works". An explicit `mcp` is a request, and
    silently running a weaker loop would hide that the setting does nothing."""
    _write(tmp_home, tools_block="tools:\n  - file.read\n")
    config_mod.set_key(config, "model.harness_cmd", "my-own-agent --run")
    config["model"]["agent_loop"] = "mcp"
    with pytest.raises(runner.RunError, match="no verified way"):
        runner.run(tmp_home, config, "demo", trigger="manual")


def test_auto_falls_back_to_the_builtin_loop(tmp_home, config, monkeypatch):
    _write(tmp_home, tools_block="tools:\n  - file.read\n")
    config_mod.set_key(config, "model.harness_cmd", "my-own-agent --run")
    config["model"]["agent_loop"] = "auto"
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="answered"))
    record = runner.run(tmp_home, config, "demo", trigger="manual")
    assert record["outcome"] == "success"


def test_the_agent_loop_reads_back_what_the_server_recorded(tmp_home, config, monkeypatch):
    """px0 cannot see the calls directly -- the harness starts the server -- so
    the sidecar is how a run learns what its own tools did."""
    _write(tmp_home, tools_block="tools:\n  - file.read\n")
    config["model"]["agent_loop"] = "mcp"

    def fake(cfg, prompt, timeout=120, extra_flags=None):
        # Stand in for the harness: find the scope it was handed and write the
        # call the real server would have written.
        path = extra_flags[extra_flags.index("--mcp-config") + 1]
        server = json.loads(open(path).read())
        scope_path = server["mcpServers"]["px0"]["args"][-1]
        scope = json.loads(open(scope_path).read())
        with open(scope["calls_path"], "a") as f:
            f.write(json.dumps({"tool": "file.read", "is_write": False,
                                "failed": False, "args": {"path": "x"},
                                "result_summary": "contents"}) + "\n")
        return harness.Reply(text="the answer")

    monkeypatch.setattr(harness, "invoke_detailed", fake)
    record = runner.run(tmp_home, config, "demo", trigger="manual")
    assert record["outcome"] == "success"
    assert [c["tool"] for c in record["tool_calls"]] == ["file.read"]
    assert record["usage"]["loop"] == "mcp"


def test_a_workflow_with_no_tools_never_needs_the_agent_loop(tmp_home, config, monkeypatch):
    _write(tmp_home)
    config["model"]["agent_loop"] = "mcp"
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="answered"))
    assert runner.run(tmp_home, config, "demo", trigger="manual")["outcome"] == "success"


# --- the turn ceiling -----------------------------------------------------

def test_the_turn_ceiling_is_configurable(config):
    config_mod.set_key(config, "runs.max_tool_turns", "3")
    assert runner._max_turns(config) == 3


@pytest.mark.parametrize("value", ["0", "-4", "nonsense"])
def test_a_nonsense_ceiling_does_not_produce_a_run_that_cannot_act(config, value):
    config["runs"]["max_tool_turns"] = value
    assert runner._max_turns(config) >= 1


# --- redaction ------------------------------------------------------------

@pytest.mark.parametrize("secret", [
    "Bearer abcdefghijklmnop",
    "ghp_abcdefghijklmnopqrst",
    "xoxb_abcdefghijklmnop",
    "sk-abcdefghijklmnopqrst",
    "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
])
def test_a_credential_does_not_reach_a_record_kept_for_a_year(secret):
    assert secret not in runner.redact(f"the response was {secret} ok")


def test_ordinary_text_is_left_alone():
    text = "posted to #eng-standup with 4 items"
    assert runner.redact(text) == text


# --- what survives a harness that dies mid-run ----------------------------

def test_calls_already_made_are_recorded_when_the_harness_then_fails(
        tmp_home, config, monkeypatch):
    """Retention exempts runs that called a write tool. A timeout after posting
    to Slack must not record `tool_calls: []`, or the log of that post is
    pruned as though nothing happened.
    """
    _write(tmp_home, tools_block="tools:\n  - http.post\n")
    config["model"]["agent_loop"] = "mcp"

    def fake(cfg, prompt, timeout=120, extra_flags=None):
        path = extra_flags[extra_flags.index("--mcp-config") + 1]
        scope = json.loads(open(json.loads(open(path).read())
                                ["mcpServers"]["px0"]["args"][-1]).read())
        with open(scope["calls_path"], "a") as f:
            f.write(json.dumps({"tool": "http.post", "is_write": True,
                                "failed": False, "args": {},
                                "result_summary": "posted"}) + "\n")
        raise harness.HarnessError("timed out after it had already posted")

    monkeypatch.setattr(harness, "invoke_detailed", fake)
    with pytest.raises(runner.RunError) as caught:
        runner.run(tmp_home, config, "demo", trigger="manual")
    assert [c["tool"] for c in caught.value.record["tool_calls"]] == ["http.post"]


def test_the_builtin_loop_keeps_its_calls_too(tmp_home, config, monkeypatch):
    _write(tmp_home, tools_block="tools:\n  - file.read\n")
    monkeypatch.setattr(tools, "call", lambda *a: {"ok": True})
    script = ['TOOL_CALL: {"tool": "file.read", "args": {"path": "x"}}']

    def fake(*a, **kw):
        if script:
            return harness.Reply(text=script.pop(0))
        raise harness.HarnessError("died on the second turn")

    monkeypatch.setattr(harness, "invoke_detailed", fake)
    with pytest.raises(runner.RunError) as caught:
        runner.run(tmp_home, config, "demo", trigger="manual")
    assert [c["tool"] for c in caught.value.record["tool_calls"]] == ["file.read"]


def test_the_agent_loop_leaves_no_temp_directory_behind(tmp_home, config, monkeypatch):
    import tempfile
    from pathlib import Path as P

    _write(tmp_home, tools_block="tools:\n  - file.read\n")
    config["model"]["agent_loop"] = "mcp"
    before = set(P(tempfile.gettempdir()).glob("px0-scope-*"))

    def boom(*a, **kw):
        raise harness.HarnessError("nope")

    monkeypatch.setattr(harness, "invoke_detailed", boom)
    with pytest.raises(runner.RunError):
        runner.run(tmp_home, config, "demo", trigger="manual")
    assert set(P(tempfile.gettempdir()).glob("px0-scope-*")) == before
