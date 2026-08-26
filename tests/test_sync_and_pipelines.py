"""Moving a store between machines, and pipelines that can skip a stage.

The sync tests are almost all about what it refuses to do. A sync that
overwrites is how a person loses an afternoon's work, and the reason people
were pointing Dropbox at `~/.px0` in the first place is that px0 offered them
nothing better -- so the bar here is not "it copies files", it is "it never
silently picks one of two versions".
"""

import json

import pytest

from px0 import config as config_mod, harness, paths, runner, sync
from px0 import workflow as workflow_mod


@pytest.fixture
def config(tmp_home, tmp_path):
    cfg = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(cfg, "logs.path", str(tmp_path / "logs"))
    return cfg


@pytest.fixture
def remote(tmp_path):
    d = tmp_path / "shared"
    d.mkdir()
    return d


def _wf(home, wf_id, body="Body."):
    (paths.workflows_dir(home) / f"{wf_id}.md").write_text(
        f"---\nid: {wf_id}\ndescription: A demo\noutput:\n  target: stdout\n"
        f"---\n\n{body}\n")


# --- what a sync moves ----------------------------------------------------

def test_a_first_sync_sends_everything(tmp_home, remote):
    _wf(tmp_home, "alpha")
    result = sync.sync(tmp_home, remote)
    assert result["pushed"] == ["workflows/alpha.md"]
    assert (remote / "workflows" / "alpha.md").exists()


def test_a_second_machine_takes_what_it_lacks(tmp_home, tmp_path, remote):
    from px0 import store

    _wf(tmp_home, "alpha")
    sync.sync(tmp_home, remote)

    other = tmp_path / "second"
    other.mkdir()
    store.init(other)
    result = sync.sync(other, remote)
    assert result["pulled"] == ["workflows/alpha.md"]
    assert (other / "workflows" / "alpha.md").exists()


def test_an_unchanged_file_moves_neither_way(tmp_home, remote):
    _wf(tmp_home, "alpha")
    sync.sync(tmp_home, remote)
    again = sync.sync(tmp_home, remote)
    assert again["pushed"] == [] and again["pulled"] == []


def test_state_never_travels(tmp_home, remote):
    """`.state/` holds the version history, credentials, and every queue. It is
    either machine-specific or unmergeable, and the SQLite history is the exact
    thing a folder-syncing tool corrupts."""
    (paths.state_dir(tmp_home) / "credentials.toml").write_text("secret = 1")
    _wf(tmp_home, "alpha")
    sync.sync(tmp_home, remote)
    assert not (remote / ".state").exists()
    assert "secret" not in json.dumps(sync.read_manifest(remote))


def test_memory_and_guidelines_travel(tmp_home, remote):
    from px0 import builder, memory

    memory.remember(tmp_home, "the API repo is acme/api", subject="api repo")
    builder.save_guideline(tmp_home, "tone.md", "## Be terse", description="tone")
    result = sync.sync(tmp_home, remote)
    assert any(p.startswith("memory/") for p in result["pushed"])
    assert any(p.startswith("guidelines/") for p in result["pushed"])


# --- what a sync refuses to do --------------------------------------------

def test_a_file_changed_on_both_sides_is_not_overwritten(tmp_home, tmp_path, remote):
    """Two versions are two decisions. px0 is not in a position to know which
    one was meant, and picking one means deleting the other."""
    from px0 import store

    _wf(tmp_home, "alpha", body="mine")
    sync.sync(tmp_home, remote)

    other = tmp_path / "second"
    other.mkdir()
    store.init(other)
    sync.sync(other, remote)

    _wf(tmp_home, "alpha", body="changed here")
    _wf(other, "alpha", body="changed there")
    sync.sync(other, remote)          # theirs reaches the remote first
    result = sync.sync(tmp_home, remote)

    assert result["conflicts"]
    assert "changed here" in (tmp_home / "workflows" / "alpha.md").read_text()
    beside = tmp_home / result["conflicts"][0]["theirs"]
    assert "changed there" in beside.read_text()


def test_a_conflict_file_names_where_it_came_from(tmp_home, tmp_path, remote):
    from px0 import store

    _wf(tmp_home, "alpha", body="mine")
    sync.sync(tmp_home, remote)
    other = tmp_path / "second"
    other.mkdir()
    store.init(other)
    sync.sync(other, remote)

    _wf(tmp_home, "alpha", body="here")
    _wf(other, "alpha", body="there")
    sync.sync(other, remote)
    result = sync.sync(tmp_home, remote)
    assert ".conflict-" in result["conflicts"][0]["theirs"]


def test_a_sync_never_deletes(tmp_home, remote):
    """A file absent on one side may be one that side has not pulled yet, and
    treating absence as deletion is how a sync loses work."""
    _wf(tmp_home, "alpha")
    _wf(tmp_home, "beta")
    sync.sync(tmp_home, remote)
    (paths.workflows_dir(tmp_home) / "beta.md").unlink()
    sync.sync(tmp_home, remote)
    assert (remote / "workflows" / "beta.md").exists()


def test_a_dry_run_moves_nothing(tmp_home, remote):
    _wf(tmp_home, "alpha")
    result = sync.sync(tmp_home, remote, dry_run=True)
    assert result["applied"] is False
    assert result["push"] == ["workflows/alpha.md"]
    assert not (remote / "workflows").exists()


def test_pull_only_sends_nothing(tmp_home, remote):
    _wf(tmp_home, "alpha")
    sync.sync(tmp_home, remote, pull_only=True)
    assert not (remote / "workflows" / "alpha.md").exists()


def test_a_share_is_created_on_first_use(tmp_home, tmp_path):
    """The first sync is the common case, and failing until the user goes and
    creates the directory is a step with no purpose."""
    fresh = tmp_path / "shared-later"
    assert not fresh.exists()
    sync.status(tmp_home, fresh)
    assert fresh.is_dir()


def test_a_missing_parent_is_refused_as_a_typo(tmp_home, tmp_path):
    """The difference matters: the wrong answer here is a store quietly syncing
    into a directory nothing else can see."""
    with pytest.raises(sync.SyncError, match="typo"):
        sync.status(tmp_home, tmp_path / "nowhere" / "deeper" / "shared")


def test_a_file_where_the_share_should_be_is_refused(tmp_home, tmp_path):
    target = tmp_path / "not-a-dir"
    target.write_text("x")
    with pytest.raises(sync.SyncError, match="not a directory"):
        sync.status(tmp_home, target)


def test_an_unreadable_manifest_is_an_error(tmp_home, remote):
    (remote / sync.MANIFEST).write_text("{not json")
    with pytest.raises(sync.SyncError):
        sync.status(tmp_home, remote)


@pytest.mark.parametrize("path_part, expected", [
    ("Dropbox/px0", "Dropbox"),
    ("Library/Mobile Documents/px0", "iCloud Drive"),
    ("OneDrive/px0", "OneDrive"),
    ("code/px0", None),
])
def test_a_store_inside_a_syncing_folder_is_spotted(tmp_path, path_part, expected):
    """The corruption is silent and specific, and nothing reports it until a
    revert needs the history that is already gone."""
    home = tmp_path / path_part
    home.mkdir(parents=True)
    assert sync.hazard(home) == expected


# --- pipelines that can skip a stage --------------------------------------

def _pipeline(home, stages: str):
    (paths.workflows_dir(home) / "chain.md").write_text(
        f"---\nid: chain\ndescription: A pipeline\npipeline:\n{stages}"
        "output:\n  target: stdout\n---\n\nBody.\n")
    for stage in ("finder", "poster"):
        (paths.workflows_dir(home) / f"{stage}.md").write_text(
            f"---\nid: {stage}\ndescription: A stage\noutput:\n  target: stdout\n"
            "---\n\nBody.\n")
    return workflow_mod.load(home, "chain")


def test_a_plain_list_of_ids_still_works(tmp_home):
    """Every existing pipeline in every store is written this way."""
    wf = _pipeline(tmp_home, "  - finder\n  - poster\n")
    stages = workflow_mod.pipeline_stages(wf)
    assert [s["workflow"] for s in stages] == ["finder", "poster"]
    assert all(s["when"] == "always" for s in stages)


def test_a_stage_can_say_when_it_runs(tmp_home):
    wf = _pipeline(tmp_home, "  - finder\n  - workflow: poster\n    when: has_output\n")
    assert workflow_mod.pipeline_stages(wf)[1]["when"] == "has_output"
    assert workflow_mod.validate(wf, tmp_home) == []


@pytest.mark.parametrize("when, previous, runs", [
    ("always", "", True),
    ("always", "something", True),
    ("has_output", "something", True),
    ("has_output", "", False),
    ("has_output", "   \n ", False),
    ("no_output", "", True),
    ("no_output", "something", False),
])
def test_a_condition_reads_the_previous_output(when, previous, runs):
    assert runner._stage_should_run(when, previous) is runs


def test_an_unknown_condition_fails_validation(tmp_home):
    wf = _pipeline(tmp_home, "  - finder\n  - workflow: poster\n    when: maybe\n")
    assert any("when" in e for e in workflow_mod.validate(wf, tmp_home))


def test_a_condition_on_the_first_stage_fails_validation(tmp_home):
    """There is no previous output to test, so it can only be a mistake about
    which stage the condition belongs to."""
    wf = _pipeline(tmp_home, "  - workflow: finder\n    when: has_output\n  - poster\n")
    assert any("no previous stage" in e for e in workflow_mod.validate(wf, tmp_home))


def test_a_stage_naming_no_workflow_fails_validation(tmp_home):
    wf = _pipeline(tmp_home, "  - finder\n  - when: has_output\n")
    assert any("names no workflow" in e for e in workflow_mod.validate(wf, tmp_home))


def test_a_skipped_stage_does_not_fail_the_pipeline(tmp_home, config, monkeypatch):
    """"post it only if there is something to post" must not also break every
    stage after it."""
    _pipeline(tmp_home, "  - finder\n  - workflow: poster\n    when: has_output\n")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text=""))
    record = runner.run(tmp_home, config, "chain", trigger="manual")
    assert record["outcome"] == "success"
    assert [s["workflow"] for s in record["skipped"]] == ["poster"]


def test_a_met_condition_runs_the_stage(tmp_home, config, monkeypatch):
    _pipeline(tmp_home, "  - finder\n  - workflow: poster\n    when: has_output\n")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="found three"))
    record = runner.run(tmp_home, config, "chain", trigger="manual")
    assert record["skipped"] == []
    assert len(record["stages"]) == 2


# --- credentials for user-declared tools ----------------------------------

def test_a_tool_declaring_nothing_sees_the_whole_environment(tmp_home):
    from px0 import localtools

    tool = localtools.UserTool(id="local.x", description="", command=["true"],
                               params={}, is_write=False, timeout=10)
    assert localtools._tool_env(tool) is None


def test_a_tool_declaring_a_variable_gets_a_narrow_one(tmp_home, monkeypatch):
    """A token meant for one command should not be handed to every other command
    a workflow can reach."""
    from px0 import localtools

    monkeypatch.setenv("MY_TOKEN", "secret")
    monkeypatch.setenv("UNRELATED_TOKEN", "other")
    tool = localtools.UserTool(id="local.x", description="", command=["true"],
                               params={}, is_write=False, timeout=10,
                               env=["MY_TOKEN"])
    env = localtools._tool_env(tool)
    assert env["MY_TOKEN"] == "secret"
    assert "UNRELATED_TOKEN" not in env


def test_a_missing_declared_variable_is_refused_before_running(tmp_home, monkeypatch):
    """Better than a command that fails halfway with whatever error the far end
    gives an unauthenticated request."""
    from px0 import localtools

    monkeypatch.delenv("MY_TOKEN", raising=False)
    tool = localtools.UserTool(id="local.x", description="", command=["true"],
                               params={}, is_write=False, timeout=10,
                               env=["MY_TOKEN"])
    with pytest.raises(localtools.LocalToolError, match="MY_TOKEN"):
        localtools._tool_env(tool)


def test_a_declared_env_that_is_not_a_list_is_refused(tmp_home):
    from px0 import localtools

    with pytest.raises(localtools.LocalToolError):
        localtools._validate_user_tool(
            {"id": "local.x", "command": ["true"], "env": {"a": "b"}},
            paths.tools_dir(tmp_home) / "x.toml")
