"""Creating, parking, and removing store files through px0 rather than with `rm`.

The point of these verbs is not convenience: it is that the version chain and
the change log stay true. A hand deletion leaves no record and cannot be undone
by the mechanism built for undoing things, so every test here asserts on the
history as much as on the filesystem.
"""

import argparse

import pytest

from px0 import authoring, cli, paths, versioning, workflow as wf_mod


def _workflow(home, wf_id="demo", schedule="0 9 * * 1", enabled=None):
    front = [f"id: {wf_id}", "description: A demo"]
    if schedule:
        front += ["trigger:", f"  schedule: \"{schedule}\""]
    if enabled is not None:
        front.append(f"enabled: {'true' if enabled else 'false'}")
    front += ["output:", "  target: file", "  path: out-{date}.md"]
    text = "---\n" + "\n".join(front) + "\n---\n\nBody.\n"
    path = paths.workflows_dir(home) / f"{wf_id}.md"
    path.write_text(text)
    versioning.record_change(home, "test", [
        versioning.FileChange(f"workflows/{wf_id}.md", text.encode())])
    return path


@pytest.fixture
def ctx(tmp_home, monkeypatch):
    from px0 import config as config_mod

    config = config_mod.load(paths.config_path(tmp_home))
    monkeypatch.setattr(cli, "_ctx", lambda *a, **k: (tmp_home, config))
    monkeypatch.setattr(cli.daemon_mod, "restart_if_running", lambda *a, **k: None)
    return tmp_home, config


# --- ids are filenames, so they cannot be arbitrary strings ----------------

@pytest.mark.parametrize("bad", ["../escape", "a/b", "", "..", "with space", "-"])
def test_a_bad_id_is_refused_before_it_reaches_the_filesystem(bad):
    if bad == "-":
        pytest.skip("a single dash is a legal id")
    with pytest.raises(authoring.AuthoringError):
        authoring.check_id(bad)


def test_a_trailing_md_is_accepted_because_that_is_what_list_prints():
    assert authoring.check_id("commit-messages.md") == "commit-messages"


# --- removal keeps the history -------------------------------------------

def test_removing_a_workflow_tombstones_it_and_keeps_its_content(ctx):
    home, _config = ctx
    path = _workflow(home)

    result = authoring.remove_file(home, path)

    assert not path.exists()
    assert result["change_id"]
    # the content is still reachable, which is the whole reason to remove
    # through px0 instead of with rm
    assert versioning.show_version(home, "workflows/demo.md", 1) is not None
    change = versioning.show_change(home, result["change_id"])
    assert change["files"]


def test_a_removed_workflow_can_be_reverted(ctx):
    home, _config = ctx
    path = _workflow(home)
    result = authoring.remove_file(home, path)

    versioning.revert_change(home, result["change_id"], "test")

    assert path.exists()
    assert wf_mod.parse(path).id == "demo"


def test_rm_refuses_without_a_confirmation_and_keeps_the_file(ctx, monkeypatch, capsys):
    home, _config = ctx
    path = _workflow(home)
    monkeypatch.setattr(cli.ui, "prompt", lambda *a, **k: "n")
    monkeypatch.setattr(cli.sys.stdin, "isatty", lambda: True, raising=False)

    cli.cmd_workflows_rm(argparse.Namespace(workflow="demo", yes=False))

    assert path.exists()
    assert "kept" in capsys.readouterr().out


# --- parking is not deleting ---------------------------------------------

def test_disable_sets_the_flag_and_leaves_the_schedule_alone(ctx):
    home, _config = ctx
    _workflow(home)

    cli.cmd_workflows_enable(argparse.Namespace(workflow="demo", workflows_cmd="disable"))

    wf = wf_mod.load(home, "demo")
    assert wf.enabled is False
    # the schedule is what you would otherwise have had to delete and remember
    assert wf.trigger["schedule"] == "0 9 * * 1"


def test_enable_restores_a_parked_workflow(ctx):
    home, _config = ctx
    _workflow(home, enabled=False)

    cli.cmd_workflows_enable(argparse.Namespace(workflow="demo", workflows_cmd="enable"))

    assert wf_mod.load(home, "demo").enabled is True


def test_disabling_twice_is_not_an_error(ctx, capsys):
    home, _config = ctx
    _workflow(home, enabled=False)

    cli.cmd_workflows_enable(argparse.Namespace(workflow="demo", workflows_cmd="disable"))

    assert "already disabled" in capsys.readouterr().out


def test_the_daemon_skips_a_disabled_workflow(tmp_home, monkeypatch):
    from px0 import daemon as daemon_mod, config as config_mod

    config = config_mod.load(paths.config_path(tmp_home))
    _workflow(tmp_home, "parked", enabled=False)
    _workflow(tmp_home, "live")
    spawned = []
    monkeypatch.setattr(daemon_mod, "spawn_run",
                        lambda home, wf_id, late, fire_time: spawned.append(wf_id))

    daemon_mod.tick(tmp_home, config, {})

    assert "parked" not in spawned


def test_the_cron_fallback_also_skips_a_disabled_workflow(tmp_home):
    from px0 import daemon as daemon_mod

    _workflow(tmp_home, "parked", enabled=False)
    _workflow(tmp_home, "live")

    block = daemon_mod.crontab_block(tmp_home, "/usr/bin/px0")

    assert "live" in block
    assert "parked" not in block


# --- rename and copy carry the id in the frontmatter too ------------------

def test_rename_changes_the_id_as_well_as_the_filename(ctx):
    home, _config = ctx
    _workflow(home)

    cli.cmd_workflows_rename(argparse.Namespace(workflow="demo", new_id="renamed"))

    assert wf_mod.load(home, "renamed").id == "renamed"
    assert "demo" not in wf_mod.load_all(home)


def test_copy_leaves_the_original_alone(ctx):
    home, _config = ctx
    _workflow(home)

    cli.cmd_workflows_copy(argparse.Namespace(workflow="demo", new_id="forked"))

    assert wf_mod.load(home, "demo").id == "demo"
    assert wf_mod.load(home, "forked").id == "forked"


def test_copy_onto_an_existing_id_is_refused(ctx):
    home, _config = ctx
    _workflow(home)
    _workflow(home, "other")

    with pytest.raises(SystemExit):
        cli.cmd_workflows_copy(argparse.Namespace(workflow="demo", new_id="other"))


# --- frontmatter edits are surgical --------------------------------------

def test_setting_a_key_leaves_the_rest_of_the_file_byte_identical():
    text = ("---\nid: x\n# a comment worth keeping\ntrigger:\n  schedule: \"0 9 * * *\"\n"
            "---\n\nBody with  odd   spacing.\n")

    out = authoring.set_frontmatter_key(text, "enabled", False)

    assert "# a comment worth keeping" in out
    assert "Body with  odd   spacing." in out
    assert "enabled: false" in out


def test_setting_a_key_that_exists_rewrites_it_once():
    text = "---\nid: x\nenabled: true\n---\n\nbody\n"

    out = authoring.set_frontmatter_key(text, "enabled", False)

    assert out.count("enabled:") == 1
    assert "enabled: false" in out


# --- guidelines: written by the build, edited by hand --------------------

def test_editing_a_guideline_records_the_new_version(ctx, monkeypatch, capsys):
    home, _config = ctx
    path = paths.guidelines_dir(home) / "voice.md"
    path.write_text("## Say it plainly\n\nShort sentences.\n")

    monkeypatch.setattr(cli, "_open_in_editor",
                        lambda p: bool(p.write_text("## Say it plainly\n\nShorter.\n")) or True)
    cli.cmd_guidelines_file(argparse.Namespace(guidelines_cmd="edit", name="voice"))

    assert "Shorter." in path.read_text()
    assert versioning.latest_version_number(home, "guidelines/voice.md") >= 1
    assert "saved" in capsys.readouterr().out


def test_a_guideline_the_build_filed_under_a_folder_is_editable_by_name(ctx, monkeypatch):
    """`px0 workflows new` may file one in a subfolder, and it is reported by
    that path -- so `edit` has to find it the way `workflows edit` does."""
    home, _config = ctx
    nested = paths.guidelines_dir(home) / "code-review" / "go.md"
    nested.parent.mkdir(parents=True)
    nested.write_text("## Flag real breakage\n\nOnly that.\n")

    monkeypatch.setattr(cli, "_open_in_editor",
                        lambda p: bool(p.write_text("## Changed\n\nnew.\n")) or True)
    cli.cmd_guidelines_file(argparse.Namespace(guidelines_cmd="edit", name="go"))

    assert nested.read_text() == "## Changed\n\nnew.\n"


def test_a_guideline_that_is_not_there_is_an_error_not_a_new_file(ctx):
    """With `new` gone, a typo must not quietly scaffold an empty guideline."""
    home, _config = ctx

    with pytest.raises(SystemExit):
        cli.cmd_guidelines_file(argparse.Namespace(guidelines_cmd="show", name="nope"))

    assert not (paths.guidelines_dir(home) / "nope.md").exists()


def test_removing_a_guideline_warns_about_the_workflows_naming_it(ctx, monkeypatch, capsys):
    home, _config = ctx
    (paths.guidelines_dir(home) / "voice.md").write_text("how I write")
    path = paths.workflows_dir(home) / "w.md"
    path.write_text("---\nid: w\nguidelines:\n  - voice.md\n---\n\nbody\n")

    cli.cmd_guidelines_file(argparse.Namespace(
        guidelines_cmd="rm", name="voice", yes=True))

    captured = capsys.readouterr()
    # the warning goes to stderr, so it survives `px0 guidelines rm ... > file`
    assert "in use by" in captured.err
    assert "removed" in captured.out
    assert not (paths.guidelines_dir(home) / "voice.md").exists()
