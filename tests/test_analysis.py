"""The deterministic half of the improvement loop.

Nothing here calls a model, and that is the point: a finding has to be
something a person can check by reading the same records. So each test builds
a population of runs with one defect in it and asserts that exactly that
finding comes out -- and, as often as not, asserts the *absence* of a finding
from a healthy population, because a report that cries wolf is one nobody
reads twice.

The repair half is held to a stricter line still. `apply_fixes` is the only
thing in this module that writes, and the only edits it may ever make are ones
that narrow what a workflow can do or give it more time to do it.
"""

from datetime import datetime, timedelta, timezone

import pytest

from px0 import analysis, config as config_mod, paths, versioning
from px0 import runs as runs_mod
from px0 import workflow as workflow_mod


@pytest.fixture
def config(tmp_home, tmp_path):
    cfg = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(cfg, "logs.path", str(tmp_path / "logs"))
    return cfg


@pytest.fixture
def workflow(tmp_home):
    """A workflow on disk for the report to be about.

    Checkpointed into the store's version history the way a run's stage 2
    checkpoints a hand edit, so a repair has a previous version to be reverted
    to rather than being the file's first.
    """
    def _write(wf_id="demo", tools=("file.read",), timeout="120s", extra=""):
        tools_block = ("tools:\n" + "".join(f"  - {t}\n" for t in tools)) if tools else ""
        (paths.workflows_dir(tmp_home) / f"{wf_id}.md").write_text(
            "---\n"
            f"id: {wf_id}\n"
            "description: A demo\n"
            "request: summarize my pull requests\n"
            f"timeout: {timeout}\n"
            f"{tools_block}"
            f"{extra}"
            "output:\n  target: stdout\n"
            "---\n\nBody.\n"
        )
        versioning.checkpoint_scan(tmp_home, actor="test")
        return workflow_mod.load(tmp_home, wf_id)
    return _write


def make_run(config, wf_id="demo", *, outcome="success", tool_calls=None,
             error=None, dry_run=False, output="a fine digest", usage=None,
             inputs=None, review=None, attempt=1, seconds=3.0, version=1,
             age_minutes=0):
    """Writes one run record and returns it.

    Records carry a timestamp inside their id, which is also what partitions
    them on disk, so `age_minutes` moves both together rather than leaving a
    record whose id disagrees with its own start_time.
    """
    when = datetime.now(timezone.utc) - timedelta(minutes=age_minutes)
    run_id = f"run_{when.strftime('%Y%m%d-%H%M%S')}-{abs(hash((wf_id, age_minutes, outcome, str(tool_calls), str(review)))) % 65536:04x}"
    record = {
        "id": run_id, "workflow_id": wf_id, "trigger": "manual",
        "outcome": outcome, "start_time": when.isoformat(),
        "duration_seconds": seconds, "dry_run": dry_run, "attempt": attempt,
        "tool_calls": tool_calls or [], "workflow_version": version,
        "output": {"target": "stdout", "text": output},
        "usage": usage or {"estimated": True, "turns": 1},
        "inputs_resolved": inputs or [],
    }
    if error:
        record["error"] = error
    if review:
        record["review"] = review
    runs_mod.write_record(config, record)
    return record


def call(tool="file.read", failed=False, refused=False, is_write=False,
         stubbed=False):
    return {"tool": tool, "is_write": is_write, "failed": failed,
            "refused": refused, "stubbed": stubbed,
            "result_summary": "{'error': 'nope'}" if failed else "{'ok': True}",
            "elapsed_seconds": 0.1}


def codes(report):
    return {f["code"] for f in report["findings"]}


def finding(report, code):
    return next(f for f in report["findings"] if f["code"] == code)


# --- a healthy workflow says nothing --------------------------------------

def test_a_healthy_workflow_reports_no_problems(tmp_home, config, workflow):
    workflow()
    for i in range(6):
        make_run(config, tool_calls=[call()], age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    assert report["ok"] is True
    assert not [f for f in report["findings"] if f["severity"] == "problem"]


def test_a_workflow_with_no_runs_says_so(tmp_home, config, workflow):
    workflow()
    report = analysis.health(tmp_home, config, "demo")
    assert "no_runs" in codes(report)


def test_a_workflow_only_ever_rehearsed_says_so(tmp_home, config, workflow):
    workflow()
    for i in range(3):
        make_run(config, dry_run=True, age_minutes=i)
    assert "dry_run_only" in codes(analysis.health(tmp_home, config, "demo"))


# --- failures -------------------------------------------------------------

def test_failures_of_one_cause_group_into_one_finding(tmp_home, config, workflow):
    workflow()
    for i in range(4):
        make_run(config, outcome="failed", age_minutes=i,
                 error=f"connector timed out after {i}s at 2026-08-0{i+1}T10:00:00")
    report = analysis.health(tmp_home, config, "demo")
    failures = [f for f in report["findings"] if f["code"] == "failing"]
    assert len(failures) == 1
    assert failures[0]["evidence"]["count"] == 4


def test_failures_of_different_causes_stay_separate(tmp_home, config, workflow):
    workflow()
    make_run(config, outcome="failed", error="connector timed out", age_minutes=1)
    make_run(config, outcome="failed", error="connector timed out", age_minutes=2)
    make_run(config, outcome="failed", error="not authenticated", age_minutes=3)
    report = analysis.health(tmp_home, config, "demo")
    assert len([f for f in report["findings"] if f["code"] == "failing"]) == 2


@pytest.mark.parametrize("message", ["", None, "   "])
def test_a_failure_with_no_message_still_reports(tmp_home, config, workflow, message):
    workflow()
    make_run(config, outcome="failed", error=message, age_minutes=1)
    make_run(config, outcome="failed", error=message, age_minutes=2)
    assert "failing" in codes(analysis.health(tmp_home, config, "demo"))


# --- the expensive kind of failure ----------------------------------------

def test_a_run_that_succeeded_and_wrote_nothing_is_a_problem(tmp_home, config, workflow):
    """Green in every listing there is, and useless."""
    workflow()
    for i in range(3):
        make_run(config, output="", age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    assert "empty_output" in codes(report)
    assert finding(report, "empty_output")["severity"] == "problem"


def test_success_with_every_tool_call_erroring_is_a_problem(tmp_home, config, workflow):
    workflow()
    for i in range(3):
        make_run(config, tool_calls=[call(failed=True)], age_minutes=i)
    assert "success_despite_tool_errors" in codes(analysis.health(tmp_home, config, "demo"))


def test_a_rehearsal_is_not_mistaken_for_a_run_that_did_nothing(tmp_home, config, workflow):
    """A dry run stubs its write tools by design; that is not tool failure."""
    workflow()
    for i in range(3):
        make_run(config, dry_run=True, age_minutes=i,
                 tool_calls=[call(is_write=True, stubbed=True)])
    assert "success_despite_tool_errors" not in codes(analysis.health(tmp_home, config, "demo"))


# --- the allowlist --------------------------------------------------------

def test_a_refused_tool_call_is_always_a_problem(tmp_home, config, workflow):
    workflow()
    make_run(config, tool_calls=[call(tool="shell.run", refused=True, failed=True)],
             age_minutes=1)
    report = analysis.health(tmp_home, config, "demo")
    assert "tool_refused" in codes(report)
    assert finding(report, "tool_refused")["severity"] == "problem"


# --- tools ----------------------------------------------------------------

def test_a_tool_erroring_a_third_of_the_time_is_reported(tmp_home, config, workflow):
    workflow()
    for i in range(6):
        make_run(config, tool_calls=[call(failed=i % 2 == 0)], age_minutes=i)
    assert "tool_erroring" in codes(analysis.health(tmp_home, config, "demo"))


def test_one_bad_call_in_many_is_not(tmp_home, config, workflow):
    workflow()
    for i in range(10):
        make_run(config, tool_calls=[call(failed=i == 0)], age_minutes=i)
    assert "tool_erroring" not in codes(analysis.health(tmp_home, config, "demo"))


def test_a_tool_nobody_calls_is_reported_and_fixable(tmp_home, config, workflow):
    workflow(tools=("file.read", "http.get"))
    for i in range(6):
        make_run(config, tool_calls=[call()], age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    dead = finding(report, "dead_tools")
    assert dead["payload"]["drop_tools"] == ["http.get"]
    assert dead["fixable"] is True


def test_a_tool_unused_over_too_few_runs_is_left_alone(tmp_home, config, workflow):
    """Two quiet weeks is not evidence. The threshold exists so a fix is never
    offered on the strength of a handful of runs."""
    workflow(tools=("file.read", "http.get"))
    for i in range(2):
        make_run(config, tool_calls=[call()], age_minutes=i)
    assert "dead_tools" not in codes(analysis.health(tmp_home, config, "demo"))


def test_a_tool_used_only_in_a_rehearsal_still_counts_as_used(tmp_home, config, workflow):
    workflow(tools=("file.read", "http.post"))
    for i in range(6):
        make_run(config, tool_calls=[call()], age_minutes=i)
    make_run(config, dry_run=True, age_minutes=99,
             tool_calls=[call(tool="http.post", is_write=True, stubbed=True)])
    assert "dead_tools" not in codes(analysis.health(tmp_home, config, "demo"))


# --- turns, retries, timeouts ---------------------------------------------

def test_running_out_of_turns_most_of_the_time_is_a_problem(tmp_home, config, workflow):
    workflow()
    for i in range(4):
        make_run(config, age_minutes=i, usage={"hit_turn_cap": True, "turns": 5})
    report = analysis.health(tmp_home, config, "demo")
    assert finding(report, "turn_cap")["severity"] == "problem"


def test_running_out_of_turns_occasionally_is_only_a_note(tmp_home, config, workflow):
    workflow()
    make_run(config, age_minutes=1, usage={"hit_turn_cap": True, "turns": 5})
    for i in range(2, 8):
        make_run(config, age_minutes=i, usage={"hit_turn_cap": False, "turns": 2})
    report = analysis.health(tmp_home, config, "demo")
    assert finding(report, "turn_cap")["severity"] == "note"


def test_frequent_retries_are_noted(tmp_home, config, workflow):
    workflow()
    for i in range(4):
        make_run(config, age_minutes=i, attempt=2 if i < 2 else 1)
    assert "retry_pressure" in codes(analysis.health(tmp_home, config, "demo"))


def test_timeouts_are_a_problem_with_a_computed_repair(tmp_home, config, workflow):
    workflow(timeout="60s")
    make_run(config, outcome="failed", error="harness timed out after 60s", age_minutes=1)
    make_run(config, outcome="failed", error="harness timed out after 60s", age_minutes=2)
    make_run(config, seconds=55.0, age_minutes=3)
    report = analysis.health(tmp_home, config, "demo")
    fix = finding(report, "timing_out")
    assert fix["fixable"] is True
    assert fix["payload"]["set_timeout"].endswith("s")
    assert analysis.harness.parse_duration(fix["payload"]["set_timeout"]) > 60


# --- inputs ---------------------------------------------------------------

def test_an_input_that_always_resolves_empty_is_a_problem(tmp_home, config, workflow):
    workflow()
    for i in range(4):
        make_run(config, age_minutes=i,
                 inputs=[{"id": "prs", "kind": "tool", "ok": True, "empty": True, "size": 0}])
    report = analysis.health(tmp_home, config, "demo")
    assert "input_always_empty" in codes(report)
    assert finding(report, "input_always_empty")["evidence"]["input"] == "prs"


def test_an_input_that_is_sometimes_empty_is_not(tmp_home, config, workflow):
    workflow()
    for i in range(4):
        make_run(config, age_minutes=i,
                 inputs=[{"id": "prs", "kind": "tool", "ok": True,
                          "empty": i % 2 == 0, "size": i}])
    assert "input_always_empty" not in codes(analysis.health(tmp_home, config, "demo"))


def test_older_records_that_never_recorded_emptiness_report_nothing(tmp_home, config, workflow):
    """A window predating the field must report nothing, not report zero."""
    workflow()
    for i in range(4):
        make_run(config, age_minutes=i, inputs=[{"id": "prs", "kind": "tool", "ok": True}])
    assert "input_always_empty" not in codes(analysis.health(tmp_home, config, "demo"))


def test_an_optional_input_that_keeps_failing_is_noted(tmp_home, config, workflow):
    workflow()
    for i in range(4):
        make_run(config, age_minutes=i,
                 inputs=[{"id": "extra", "kind": "tool", "ok": False,
                          "degraded": True, "error": "nope"}])
    assert "input_degraded" in codes(analysis.health(tmp_home, config, "demo"))


# --- what the person said -------------------------------------------------

def test_a_run_marked_bad_is_a_problem_however_many_succeeded(tmp_home, config, workflow):
    """The strongest signal there is: no count of clean executions outweighs a
    person saying the output was wrong."""
    workflow()
    for i in range(10):
        make_run(config, age_minutes=i)
    make_run(config, age_minutes=99,
             review={"verdict": "bad", "note": "missed the two PRs I reviewed"})
    report = analysis.health(tmp_home, config, "demo")
    assert report["ok"] is False
    assert "missed the two PRs" in finding(report, "marked_bad")["detail"]


def test_a_run_marked_good_raises_nothing(tmp_home, config, workflow):
    workflow()
    make_run(config, age_minutes=1, review={"verdict": "good", "note": "spot on"})
    assert "marked_bad" not in codes(analysis.health(tmp_home, config, "demo"))


def test_marking_and_clearing_a_run(tmp_home, config, workflow):
    workflow()
    rec = make_run(config, age_minutes=1)
    runs_mod.mark(config, rec["id"], "bad", note="wrong week")
    assert runs_mod.read_record(config, rec["id"])["review"]["note"] == "wrong week"
    runs_mod.mark(config, rec["id"], None)
    assert "review" not in runs_mod.read_record(config, rec["id"])


def test_an_unknown_verdict_is_refused(tmp_home, config, workflow):
    workflow()
    rec = make_run(config, age_minutes=1)
    with pytest.raises(ValueError):
        runs_mod.mark(config, rec["id"], "meh")


def test_marking_a_run_that_does_not_exist_says_so(config):
    with pytest.raises(FileNotFoundError):
        runs_mod.mark(config, "run_20260101-000000-zzzz", "bad")


# --- honesty about the window ---------------------------------------------

def test_runs_spanning_two_versions_of_the_workflow_are_flagged(tmp_home, config, workflow):
    workflow()
    make_run(config, age_minutes=1, version=1)
    make_run(config, age_minutes=2, version=2)
    assert "spans_versions" in codes(analysis.health(tmp_home, config, "demo"))


def test_estimated_cost_is_flagged_only_when_it_could_be_measured(tmp_home, config, workflow):
    workflow()
    make_run(config, age_minutes=1)
    config_mod.set_key(config, "model.output_format", "text")
    assert "cost_estimated" in codes(analysis.health(tmp_home, config, "demo"))

    config_mod.set_key(config, "model.harness_cmd", "my-own-agent --run")
    assert "cost_estimated" not in codes(analysis.health(tmp_home, config, "demo"))


def test_measured_costs_are_summed_and_labelled(tmp_home, config, workflow):
    workflow()
    for i in range(3):
        make_run(config, age_minutes=i, usage={
            "reported": True, "estimated": False,
            "input_tokens": 100, "output_tokens": 20, "cost_usd": 0.01})
    report = analysis.health(tmp_home, config, "demo")
    assert report["runs"]["cost_measured"] is True
    assert report["runs"]["input_tokens"] == 300
    assert report["runs"]["cost_usd"] == 0.03


def test_a_window_narrows_what_is_reported(tmp_home, config, workflow):
    workflow()
    make_run(config, outcome="failed", error="old failure", age_minutes=60 * 24 * 10)
    make_run(config, age_minutes=1)
    recent = analysis.health(tmp_home, config, "demo",
                             since=datetime.now() - timedelta(days=2))
    assert "failing" not in codes(recent)


# --- reading records px0 wrote before any of this existed -----------------

def test_a_record_without_the_new_fields_still_reports(tmp_home, config, workflow):
    """Records written before calls carried a `failed` flag are read the way
    the result was actually stored, so an older window is not silently
    all-clear."""
    workflow()
    for i in range(4):
        runs_mod.write_record(config, {
            "id": f"run_2026010{i+1}-100000-old{i}",
            "workflow_id": "demo", "outcome": "success",
            "start_time": (datetime.now(timezone.utc) - timedelta(minutes=i)).isoformat(),
            "tool_calls": [{"tool": "file.read",
                            "result_summary": "{'error': 'gone'}"}],
        })
    assert "success_despite_tool_errors" in codes(analysis.health(tmp_home, config, "demo"))


def test_a_workflow_that_no_longer_exists_reports_its_error(tmp_home, config):
    report = analysis.health(tmp_home, config, "ghost")
    assert report["error"]
    assert report["findings"] == []


# --- across every workflow ------------------------------------------------

def test_the_overview_has_a_row_per_workflow(tmp_home, config, workflow):
    workflow("alpha")
    workflow("beta")
    make_run(config, "alpha", age_minutes=1)
    overview = analysis.overview(tmp_home, config)
    assert {r["workflow"] for r in overview["workflows"]} == {"alpha", "beta"}


def test_runs_of_a_deleted_workflow_are_still_counted(tmp_home, config, workflow):
    """They are runs the user paid for; dropping them from the listing was
    confusing."""
    workflow("alpha")
    make_run(config, "deleted-one", age_minutes=1)
    overview = analysis.overview(tmp_home, config)
    assert overview["orphan_runs"] == {"deleted-one": 1}


# --- deterministic repair -------------------------------------------------

def test_dropping_a_dead_tool_rewrites_the_allowlist(tmp_home, config, workflow):
    workflow(tools=("file.read", "http.get"))
    for i in range(6):
        make_run(config, tool_calls=[call()], age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    result = analysis.apply_fixes(tmp_home, config, "demo", analysis.fixable(report))
    assert result["changed"] is True
    assert workflow_mod.load(tmp_home, "demo").tools == ["file.read"]


def test_a_repair_is_recorded_as_a_revertible_change(tmp_home, config, workflow):
    workflow(tools=("file.read", "http.get"))
    for i in range(6):
        make_run(config, tool_calls=[call()], age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    result = analysis.apply_fixes(tmp_home, config, "demo", analysis.fixable(report))

    versions = versioning.list_versions(tmp_home, "workflows/demo.md")
    assert versions, "the repair left no history to revert to"
    versioning.revert_change(tmp_home, result["change_id"], actor="test")
    assert "http.get" in workflow_mod.load(tmp_home, "demo").tools


def test_a_repair_leaves_the_instruction_body_alone(tmp_home, config, workflow):
    workflow(tools=("file.read", "http.get"))
    for i in range(6):
        make_run(config, tool_calls=[call()], age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    analysis.apply_fixes(tmp_home, config, "demo", analysis.fixable(report))
    wf = workflow_mod.load(tmp_home, "demo")
    assert wf.body.strip() == "Body."
    assert wf.request == "summarize my pull requests"


def test_failures_under_an_older_shorter_timeout_are_not_blamed_on_the_new_one(
        tmp_home, config, workflow):
    """Once the timeout has been raised, the failures that prompted the raise
    must stop reading as a live problem -- otherwise the repair looks like it
    did not work, when it has simply not been tried yet."""
    workflow(timeout="120s")
    make_run(config, outcome="failed", error="harness timed out after 60s", age_minutes=1)
    make_run(config, outcome="failed", error="harness timed out after 60s", age_minutes=2)
    report = analysis.health(tmp_home, config, "demo")
    assert finding(report, "timing_out")["severity"] == "note"
    assert analysis.fixable(report) == []


def test_a_timeout_still_at_the_current_limit_is_a_live_problem(tmp_home, config, workflow):
    workflow(timeout="60s")
    make_run(config, outcome="failed", error="harness timed out after 60s", age_minutes=1)
    make_run(config, outcome="failed", error="harness timed out after 60s", age_minutes=2)
    report = analysis.health(tmp_home, config, "demo")
    assert finding(report, "timing_out")["severity"] == "problem"


def test_a_connector_timeout_is_an_ordinary_failure(tmp_home, config, workflow):
    """Raising the workflow's own timeout would not help it, so it must not be
    diverted into the check that exists to offer that repair."""
    workflow(timeout="60s")
    for i in range(3):
        make_run(config, outcome="failed", error="connector timed out", age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    assert "failing" in codes(report)
    assert "timing_out" not in codes(report)


def test_a_timeout_is_reported_once_not_twice(tmp_home, config, workflow):
    """It used to appear as a failure and again as a timeout; the second was
    the useful one."""
    workflow(timeout="60s")
    for i in range(3):
        make_run(config, outcome="failed", error="harness timed out after 60s",
                 age_minutes=i)
    assert "failing" not in codes(analysis.health(tmp_home, config, "demo"))


def test_a_failure_report_shows_a_real_message_not_the_grouping_shape(
        tmp_home, config, workflow):
    workflow()
    for i in range(3):
        make_run(config, outcome="failed", error=f"connector refused after {i}s",
                 age_minutes=i)
    detail = finding(analysis.health(tmp_home, config, "demo"), "failing")["detail"]
    assert "<n>" not in detail
    assert "connector refused" in detail


def test_raising_a_timeout_is_applied(tmp_home, config, workflow):
    workflow(timeout="60s")
    make_run(config, outcome="failed", error="harness timed out after 60s", age_minutes=1)
    make_run(config, outcome="failed", error="harness timed out after 60s", age_minutes=2)
    report = analysis.health(tmp_home, config, "demo")
    analysis.apply_fixes(tmp_home, config, "demo", analysis.fixable(report))
    assert workflow_mod.load(tmp_home, "demo").timeout != "60s"


def test_applying_nothing_changes_nothing(tmp_home, config, workflow):
    workflow()
    before = (paths.workflows_dir(tmp_home) / "demo.md").read_text()
    result = analysis.apply_fixes(tmp_home, config, "demo", [])
    assert result["changed"] is False
    assert (paths.workflows_dir(tmp_home) / "demo.md").read_text() == before


def test_a_repaired_workflow_still_validates(tmp_home, config, workflow):
    workflow(tools=("file.read", "http.get"))
    for i in range(6):
        make_run(config, tool_calls=[call()], age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    analysis.apply_fixes(tmp_home, config, "demo", analysis.fixable(report))
    wf = workflow_mod.load(tmp_home, "demo")
    assert workflow_mod.validate(wf, tmp_home) == []


def test_dropping_every_tool_removes_the_key_rather_than_leaving_it_empty(
        tmp_home, config, workflow):
    workflow(tools=("http.get",))
    for i in range(6):
        make_run(config, tool_calls=[], age_minutes=i)
    report = analysis.health(tmp_home, config, "demo")
    analysis.apply_fixes(tmp_home, config, "demo", analysis.fixable(report))
    assert workflow_mod.load(tmp_home, "demo").tools == []


def test_repairing_a_workflow_that_is_gone_raises(tmp_home, config):
    with pytest.raises(workflow_mod.WorkflowError):
        analysis.apply_fixes(tmp_home, config, "ghost",
                             [{"payload": {"drop_tools": ["x"]}}])


# --- error grouping -------------------------------------------------------

@pytest.mark.parametrize("a, b", [
    ("timed out after 30s", "timed out after 90s"),
    ("run abc123def456 failed", "run 987654abcdef failed"),
    ("failed at 2026-08-01T10:00:00", "failed at 2026-08-02T11:30:00"),
    ("no such file 'a.md'", "no such file 'b.md'"),
])
def test_two_instances_of_one_error_normalize_together(a, b):
    assert analysis.normalize_error(a) == analysis.normalize_error(b)


def test_two_different_errors_stay_apart():
    assert (analysis.normalize_error("timed out")
            != analysis.normalize_error("not authenticated"))
