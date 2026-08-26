"""The model-assisted half: proposing a revision from what runs actually did.

The risk this carries is not that a proposal is wrong -- it is that a wrong
proposal is applied without the user seeing it, or that it widens what a
workflow may reach on nothing but a model's say-so. So the assertions here are
mostly about restraint: what the evidence contains, what a proposal is allowed
to change by itself, and what happens when the model answers with rubbish.
"""

import json
from datetime import datetime, timedelta, timezone

import pytest

from px0 import analysis, builder, config as config_mod, guidelines as guidelines_mod
from px0 import harness, improve, paths, versioning
from px0 import runs as runs_mod
from px0 import workflow as workflow_mod


@pytest.fixture
def config(tmp_home, tmp_path):
    cfg = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(cfg, "logs.path", str(tmp_path / "logs"))
    return cfg


@pytest.fixture
def workflow(tmp_home):
    (paths.workflows_dir(tmp_home) / "demo.md").write_text(
        "---\n"
        "id: demo\n"
        "description: Summarize pull requests\n"
        "request: every friday summarize the PRs I reviewed\n"
        "tools:\n  - file.read\n"
        "output:\n  target: stdout\n"
        "---\n\nWrite the digest.\n"
    )
    versioning.checkpoint_scan(tmp_home, actor="test")
    return workflow_mod.load(tmp_home, "demo")


def make_run(config, **kw):
    when = datetime.now(timezone.utc) - timedelta(minutes=kw.pop("age_minutes", 0))
    record = {
        "id": f"run_{when.strftime('%Y%m%d-%H%M%S')}-{abs(hash(str(kw))) % 65536:04x}",
        "workflow_id": "demo", "outcome": kw.pop("outcome", "success"),
        "start_time": when.isoformat(), "duration_seconds": 2.0,
        "dry_run": False, "attempt": 1, "tool_calls": kw.pop("tool_calls", []),
        "output": {"target": "stdout", "text": kw.pop("output", "a digest")},
        "usage": {"estimated": True, "turns": 1}, "inputs_resolved": [],
    }
    record.update(kw)
    runs_mod.write_record(config, record)
    return record


def reply_with(payload: dict):
    """A harness that answers with one JSON proposal."""
    return lambda *a, **kw: json.dumps(payload)


GOOD = {
    "diagnosis": "It summarizes the wrong week.",
    "request": "every friday summarize the PRs I reviewed that week, not last week",
    "reasoning": "Two runs were marked bad for covering the previous week.",
    "confidence": "high",
    "tool_drops": [],
    "tool_adds": [],
    "guideline_edits": [],
}


# --- the evidence ---------------------------------------------------------

def test_the_evidence_carries_the_marks_and_their_notes(tmp_home, config, workflow):
    make_run(config, age_minutes=1, output="last week's PRs",
             review={"verdict": "bad", "note": "wrong week", "at": "now"})
    _wf, _report, case = improve.load_case(tmp_home, config, "demo")
    assert case["marked_runs"][0]["note"] == "wrong week"
    assert "last week's PRs" in case["marked_runs"][0]["output_excerpt"]


def test_the_evidence_carries_the_deterministic_findings(tmp_home, config, workflow):
    for i in range(3):
        make_run(config, age_minutes=i, output="")
    _wf, _report, case = improve.load_case(tmp_home, config, "demo")
    assert any(f["code"] == "empty_output" for f in case["findings"])


def test_unmarked_output_is_not_shipped_wholesale(tmp_home, config, workflow):
    """A run nobody complained about is evidence that things are fine, and one
    line saying so carries that. Sending every output would be most of the
    prompt and mostly noise."""
    make_run(config, age_minutes=1, output="a very long digest " * 200)
    _wf, _report, case = improve.load_case(tmp_home, config, "demo")
    assert "a very long digest" not in json.dumps(case)
    assert case["recent_runs"][0]["output_chars"] > 0


def test_an_output_excerpt_is_capped(tmp_home, config, workflow):
    make_run(config, age_minutes=1, output="x" * 5000,
             review={"verdict": "bad", "note": "too long"})
    _wf, _report, case = improve.load_case(tmp_home, config, "demo")
    assert len(case["marked_runs"][0]["output_excerpt"]) <= improve.OUTPUT_EXCERPT


def test_the_evidence_names_the_guidelines_available_to_attach(tmp_home, config, workflow):
    builder.save_guideline(tmp_home, "tone.md", "## Be terse\n\nNo filler.",
                           description="How output should read")
    _wf, _report, case = improve.load_case(tmp_home, config, "demo")
    assert any(g["path"] == "tone.md" for g in case["available_guidelines"])


# --- reading the answer ---------------------------------------------------

def test_a_well_formed_proposal_is_read(config, monkeypatch):
    monkeypatch.setattr(harness, "invoke", reply_with(GOOD))
    proposal = improve.propose(config, {})
    assert proposal.confidence == "high"
    assert "that week" in proposal.request


def test_a_proposal_wrapped_in_prose_is_still_read(config, monkeypatch):
    monkeypatch.setattr(
        harness, "invoke",
        lambda *a, **kw: f"Here is what I found:\n```json\n{json.dumps(GOOD)}\n```\n")
    assert improve.propose(config, {}).diagnosis.startswith("It summarizes")


@pytest.mark.parametrize("reply", [
    "not json at all",
    "{}",
    '{"diagnosis": "something", "request": ""}',
    '{"diagnosis": "nothing found"}',
])
def test_an_unusable_answer_raises_rather_than_being_patched_up(config, monkeypatch, reply):
    """This is about to be shown to a user as a considered recommendation.
    Half of one, read through a lenient parser, is worse than none.

    Note what is *not* on this list: an object wrapped in narration, or in an
    array. px0 locates the JSON in a harness reply rather than assuming the
    reply is nothing else, and that leniency is shared with the builder -- it
    is about where the answer sits, not about accepting a partial one.
    """
    monkeypatch.setattr(harness, "invoke", lambda *a, **kw: reply)
    with pytest.raises(improve.ImproveError):
        improve.propose(config, {})


def test_a_proposal_inside_an_array_is_still_found(config, monkeypatch):
    monkeypatch.setattr(harness, "invoke", lambda *a, **kw: f"[{json.dumps(GOOD)}]")
    assert improve.propose(config, {}).request == GOOD["request"]


def test_a_harness_failure_surfaces_as_an_improve_error(config, monkeypatch):
    def boom(*a, **kw):
        raise harness.HarnessError("not authenticated")
    monkeypatch.setattr(harness, "invoke", boom)
    with pytest.raises(improve.ImproveError, match="not authenticated"):
        improve.propose(config, {})


def test_a_guideline_edit_missing_its_body_is_dropped(config, monkeypatch):
    payload = dict(GOOD, guideline_edits=[
        {"path": "tone.md", "addition": ""},
        {"path": "", "addition": "## Something"},
        {"path": "style", "addition": "## Be terse", "why": "asked for"},
    ])
    monkeypatch.setattr(harness, "invoke", reply_with(payload))
    edits = improve.propose(config, {}).guideline_edits
    assert [e.path for e in edits] == ["style.md"]


def test_a_proposal_that_only_rewraps_the_request_counts_as_no_change(config, monkeypatch):
    """Rebuilding for it would spend a model call and a version to arrive
    exactly where it started."""
    current = "every friday summarize the PRs I reviewed"
    monkeypatch.setattr(harness, "invoke", reply_with(
        dict(GOOD, request="every friday   summarize\nthe PRs I reviewed")))
    proposal = improve.propose(config, {})
    assert proposal.changes_request(current) is False
    assert proposal.is_empty(current) is True


def test_a_proposal_with_only_a_guideline_edit_is_not_empty(config, monkeypatch):
    current = "every friday summarize the PRs I reviewed"
    monkeypatch.setattr(harness, "invoke", reply_with(dict(
        GOOD, request=current,
        guideline_edits=[{"path": "tone.md", "addition": "## Be terse"}])))
    assert improve.propose(config, {}).is_empty(current) is False


# --- guideline edits ------------------------------------------------------

def test_the_disk_decides_whether_a_guideline_is_new(tmp_home):
    """Trusting the model's own flag meant a proposal that misremembered a path
    could replace a ten-rule guideline with a two-rule one."""
    builder.save_guideline(tmp_home, "tone.md", "## Be terse", description="tone")
    edits = [improve.GuidelineEdit(path="tone.md", addition="## Also", is_new=True),
             improve.GuidelineEdit(path="fresh.md", addition="## New", is_new=False)]
    improve.reconcile_guideline_edits(tmp_home, edits)
    assert edits[0].is_new is False
    assert edits[1].is_new is True


def test_an_edit_to_an_existing_guideline_appends_rather_than_replaces(tmp_home):
    builder.save_guideline(tmp_home, "tone.md", "## Be terse\n\nNo filler.",
                           description="How output should read")
    improve.apply_guideline_edit(
        tmp_home, improve.GuidelineEdit(path="tone.md",
                                        addition="## Lead with the number"))
    body = guidelines_mod.body_of(tmp_home, "tone.md")
    assert "Be terse" in body and "Lead with the number" in body


def test_an_appended_edit_keeps_the_files_own_description(tmp_home):
    """The frontmatter description is what makes a guideline findable at all;
    losing it on an edit would quietly unhook the file from selection."""
    builder.save_guideline(tmp_home, "tone.md", "## Be terse",
                           description="How output should read")
    improve.apply_guideline_edit(
        tmp_home, improve.GuidelineEdit(path="tone.md", addition="## More"))
    parsed = guidelines_mod.parse(tmp_home / "guidelines" / "tone.md", "tone.md")
    assert parsed.description == "How output should read"


def test_a_new_guideline_is_written_with_a_description(tmp_home):
    improve.apply_guideline_edit(tmp_home, improve.GuidelineEdit(
        path="digest-style.md", addition="## Lead with the count",
        why="two runs were marked bad for burying it", is_new=True,
        description="How a digest should read"))
    parsed = guidelines_mod.parse(tmp_home / "guidelines" / "digest-style.md",
                                  "digest-style.md")
    assert parsed.described is True
    assert "Lead with the count" in parsed.body


def test_a_guideline_edit_is_versioned(tmp_home):
    builder.save_guideline(tmp_home, "tone.md", "## Be terse", description="tone")
    improve.apply_guideline_edit(
        tmp_home, improve.GuidelineEdit(path="tone.md", addition="## More"))
    assert len(versioning.list_versions(tmp_home, "guidelines/tone.md")) >= 2


# --- the diff shown to the user -------------------------------------------

def test_the_diff_marks_what_changed():
    diff = improve.request_diff("summarize the PRs", "summarize the PRs I reviewed")
    assert any(m == "+" for m, _ in diff)


def test_the_diff_of_an_unchanged_request_has_no_edits():
    diff = improve.request_diff("same text", "same text")
    assert all(m == " " for m, _ in diff)


def test_the_diff_handles_an_empty_original():
    """Workflows written before requests were stored have nothing verbatim."""
    diff = improve.request_diff("", "a new request")
    assert any(m == "+" for m, _ in diff)


# --- the whole path, through the CLI --------------------------------------

def test_improve_shows_a_proposal_and_applies_nothing_on_dry_run(
        tmp_home, config, workflow, monkeypatch, capsys, quiet_spinner):
    import argparse
    from px0 import cli

    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, config))
    monkeypatch.setattr(harness, "invoke", reply_with(GOOD))
    make_run(config, age_minutes=1, review={"verdict": "bad", "note": "wrong week"})

    cli.cmd_workflows_improve(argparse.Namespace(
        workflow="demo", since=None, dry_run=True, show_evidence=False,
        yes=False, no_clarify=False, no_discover=False, json=False))

    out = capsys.readouterr().out
    assert "wrong week" in out or "summarizes the wrong week" in out
    # Nothing applied: the file on disk still says what it said.
    assert workflow_mod.load(tmp_home, "demo").request == \
        "every friday summarize the PRs I reviewed"


def test_show_evidence_prints_the_case_and_calls_no_model(
        tmp_home, config, workflow, monkeypatch, capsys):
    import argparse
    from px0 import cli

    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, config))
    make_run(config, age_minutes=1)

    # harness.invoke is refused by the autouse guard, so reaching a model here
    # would fail the test rather than pass it quietly.
    cli.cmd_workflows_improve(argparse.Namespace(
        workflow="demo", since=None, dry_run=False, show_evidence=True,
        yes=False, no_clarify=False, no_discover=False, json=False))

    case = json.loads(capsys.readouterr().out)
    assert case["workflow"]["id"] == "demo"


def test_improve_refuses_when_there_is_nothing_to_learn_from(
        tmp_home, config, workflow, monkeypatch, capsys):
    import argparse
    from px0 import cli

    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, config))
    with pytest.raises(SystemExit):
        cli.cmd_workflows_improve(argparse.Namespace(
            workflow="demo", since=None, dry_run=False, show_evidence=False,
            yes=False, no_clarify=False, no_discover=False, json=False))
    assert "no runs" in capsys.readouterr().err.lower()


def test_health_fix_needs_a_confirmation(tmp_home, config, workflow, monkeypatch, capsys):
    """`--fix` without `--yes` must ask, and a refusal must change nothing."""
    import argparse
    from px0 import cli

    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, config))
    monkeypatch.setattr(cli, "_confirm", lambda *a, **kw: False)
    for i in range(6):
        make_run(config, age_minutes=i, tool_calls=[])

    cli.cmd_workflows_health(argparse.Namespace(
        workflow="demo", since=None, fix=True, yes=False, json=False))

    assert workflow_mod.load(tmp_home, "demo").tools == ["file.read"]


def test_health_fix_applies_once_confirmed(tmp_home, config, workflow, monkeypatch):
    import argparse
    from px0 import cli

    monkeypatch.setattr(cli, "_ctx", lambda *a, **kw: (tmp_home, config))
    monkeypatch.setattr(cli, "_confirm", lambda *a, **kw: True)
    for i in range(6):
        make_run(config, age_minutes=i, tool_calls=[])

    cli.cmd_workflows_health(argparse.Namespace(
        workflow="demo", since=None, fix=True, yes=False, json=False))

    assert workflow_mod.load(tmp_home, "demo").tools == []


# --- a model-chosen path is a filesystem path -----------------------------

@pytest.mark.parametrize("proposed, expected", [
    ("../../.bashrc", ".bashrc.md"),
    ("/etc/passwd", "etc/passwd.md"),
    ("a/../../../out.md", "a/out.md"),
    ("code-review/go.md", "code-review/go.md"),
])
def test_a_proposed_guideline_path_cannot_escape_the_store(
        config, monkeypatch, proposed, expected):
    """The model picks this name, so it is untrusted input on its way to being
    a path. Stripping a leading slash -- which is all this used to do -- leaves
    `../../.bashrc` entirely intact."""
    payload = dict(GOOD, guideline_edits=[{"path": proposed, "addition": "## x"}])
    monkeypatch.setattr(harness, "invoke", reply_with(payload))
    assert improve.propose(config, {}).guideline_edits[0].path == expected


def test_writing_a_guideline_outside_the_store_is_refused(tmp_home):
    """Checked in the function that touches the disk, so a caller that forgets
    to sanitize cannot write outside the store."""
    with pytest.raises(builder.BuilderError):
        builder.save_guideline(tmp_home, "../../escaped.md", "## x")


def test_an_ordinary_nested_guideline_still_writes(tmp_home):
    path = builder.save_guideline(tmp_home, "code-review/go.md", "## Check errors")
    assert path.exists()
    assert path.parent.name == "code-review"
