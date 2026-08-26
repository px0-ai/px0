"""The inbox a scheduled run delivers to, and the memory a run reads from.

Both are places px0 writes without being asked, so both are tested mostly for
restraint: the inbox for what it does *not* deliver, and memory for staying
reviewable, bounded, and revertible. An assistant that silently accumulates
unreadable beliefs about you is the failure mode here, not the feature.
"""

from datetime import datetime, timedelta, timezone

import pytest

from px0 import config as config_mod, harness, inbox, memory, paths, runner
from px0 import runs as runs_mod
from px0 import versioning
from px0 import workflow as workflow_mod


@pytest.fixture
def config(tmp_home, tmp_path):
    cfg = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(cfg, "logs.path", str(tmp_path / "logs"))
    return cfg


def _write(tmp_home, wf_id="demo", extra="", body="Body."):
    (paths.workflows_dir(tmp_home) / f"{wf_id}.md").write_text(
        f"---\nid: {wf_id}\ndescription: A demo\n{extra}"
        "output:\n  target: stdout\n---\n\n" + body + "\n")
    return workflow_mod.load(tmp_home, wf_id)


def _capture_prompt(monkeypatch) -> dict:
    """Records the prompt a run builds, and answers with something harmless."""
    seen: dict = {}

    def capture(cfg, prompt, timeout=120, extra_flags=None):
        seen["prompt"] = prompt
        return harness.Reply(text="done")

    monkeypatch.setattr(harness, "invoke_detailed", capture)
    return seen


# --- the inbox ------------------------------------------------------------

def test_a_scheduled_run_is_delivered(tmp_home, config, monkeypatch):
    _write(tmp_home)
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="## Friday digest\n\nthings"))
    runner.run(tmp_home, config, "demo", trigger="schedule")
    entries = inbox.listing(tmp_home)
    assert len(entries) == 1
    assert entries[0]["title"] == "Friday digest"


def test_a_manual_run_is_not(tmp_home, config, monkeypatch):
    """You were there for a manual run and have just read its output. A nightly
    one produced something at 6am that nothing has told you about."""
    _write(tmp_home)
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="output"))
    runner.run(tmp_home, config, "demo", trigger="manual")
    assert inbox.listing(tmp_home) == []


def test_a_rehearsal_is_never_delivered(tmp_home, config, monkeypatch):
    """A dry run's output is a sample, not news."""
    _write(tmp_home)
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="output"))
    runner.run(tmp_home, config, "demo", trigger="schedule", dry_run=True)
    assert inbox.listing(tmp_home) == []


def test_a_workflow_can_ask_to_be_delivered_from_a_manual_run(tmp_home, config, monkeypatch):
    _write(tmp_home, extra="")
    (paths.workflows_dir(tmp_home) / "demo.md").write_text(
        "---\nid: demo\ndescription: A demo\noutput:\n  target: stdout\n"
        "  inbox: true\n---\n\nBody.\n")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="output"))
    runner.run(tmp_home, config, "demo", trigger="manual")
    assert len(inbox.listing(tmp_home)) == 1


def test_a_workflow_can_opt_out_of_a_scheduled_delivery(tmp_home, config, monkeypatch):
    (paths.workflows_dir(tmp_home) / "demo.md").write_text(
        "---\nid: demo\ndescription: A demo\noutput:\n  target: stdout\n"
        "  inbox: false\n---\n\nBody.\n")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="output"))
    runner.run(tmp_home, config, "demo", trigger="schedule")
    assert inbox.listing(tmp_home) == []


def test_delivery_can_be_turned_off_store_wide(tmp_home, config, monkeypatch):
    _write(tmp_home)
    config_mod.set_key(config, "inbox.auto", "false")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="output"))
    runner.run(tmp_home, config, "demo", trigger="schedule")
    assert inbox.listing(tmp_home) == []


def test_inbox_is_a_valid_output_target(tmp_home):
    wf = _write(tmp_home)
    (paths.workflows_dir(tmp_home) / "demo.md").write_text(
        "---\nid: demo\ndescription: A demo\noutput:\n  target: inbox\n---\n\nBody.\n")
    wf = workflow_mod.load(tmp_home, "demo")
    assert workflow_mod.validate(wf, tmp_home) == []


@pytest.mark.parametrize("text, expected", [
    ("## PRs you reviewed\n\nbody", "PRs you reviewed"),
    ("plain first line\nmore", "plain first line"),
    ("\n\n   \n# Heading", "Heading"),
    ("", "demo"),
])
def test_the_title_comes_from_the_output_itself(text, expected):
    """A workflow that already writes a heading has said what the entry is
    better than any label px0 could synthesize."""
    assert inbox.title_for(text, "demo") == expected


def test_reading_an_entry_marks_it_read(tmp_home, config):
    entry = inbox.deliver(tmp_home, config, workflow_id="demo", run_id="r",
                          text="something")
    inbox.mark(tmp_home, entry["id"], inbox.READ)
    assert inbox.listing(tmp_home) == []
    assert len(inbox.listing(tmp_home, status=None)) == 1


def test_an_entry_reads_the_file_as_it_is_now(tmp_home, config):
    """Not a copy frozen at delivery: opening an entry should show what is on
    disk, since a later run may have rewritten it."""
    target = tmp_home / "output" / "digest.md"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text("the current text")
    entry = inbox.deliver(tmp_home, config, workflow_id="demo", run_id="r",
                          text="the text at delivery", path="output/digest.md")
    assert inbox.body(tmp_home, config, entry) == "the current text"


def test_an_entry_whose_file_is_gone_falls_back_to_its_preview(tmp_home, config):
    entry = inbox.deliver(tmp_home, config, workflow_id="demo", run_id="r",
                          text="what it said", path="output/vanished.md")
    body = inbox.body(tmp_home, config, entry)
    assert "what it said" in body and "gone" in body


def test_retention_never_drops_what_you_have_not_read(tmp_home, config):
    """An inbox that quietly forgets unread mail is worse than one that grows."""
    old = datetime.now(timezone.utc) - timedelta(days=90)
    for status in (inbox.UNREAD, inbox.READ):
        entry = inbox.deliver(tmp_home, config, workflow_id="demo", run_id="r",
                              text=status)
        entry.update(status=status, created=old.isoformat())
        inbox.write_entry(tmp_home, entry)
    inbox.apply_retention(tmp_home, config)
    remaining = inbox.listing(tmp_home, status=None)
    assert [e["status"] for e in remaining] == [inbox.UNREAD]


# --- memory ---------------------------------------------------------------

def test_a_memory_is_a_file_you_can_read(tmp_home):
    entry = memory.remember(tmp_home, "standup goes out before 09:30",
                            kind="preference", subject="standup timing")
    assert entry.path.exists()
    assert "standup goes out before 09:30" in entry.path.read_text()
    assert entry.kind == "preference"


def test_a_memory_is_versioned_and_revertible(tmp_home):
    """px0 writes these on its own initiative, so what it has come to believe
    about you has to be as revertible as anything you wrote yourself."""
    entry = memory.remember(tmp_home, "the API repo is acme/api", subject="the API repo")
    versions = versioning.list_versions(tmp_home, f"memory/{entry.name}.md")
    assert versions


def test_forgetting_keeps_the_history(tmp_home):
    entry = memory.remember(tmp_home, "something wrong", subject="a mistake")
    assert memory.forget(tmp_home, entry.name) is True
    assert not entry.path.exists()
    assert versioning.list_versions(tmp_home, f"memory/{entry.name}.md")


def test_forgetting_what_was_never_remembered_is_not_an_error(tmp_home):
    assert memory.forget(tmp_home, "never-existed") is False


def test_remembering_the_same_subject_replaces_rather_than_accumulates(tmp_home):
    """A fact that has changed is not two facts, and a folder holding both will
    contradict itself inside a prompt."""
    first = memory.remember(tmp_home, "standup is at 9", subject="standup timing")
    second = memory.remember(tmp_home, "standup is at 9:30", subject="standup timing")
    assert first.name == second.name
    assert len(memory.load_all(tmp_home)) == 1
    assert "9:30" in memory.load_all(tmp_home)[first.name].text


@pytest.mark.parametrize("text", ["", "   ", "\n"])
def test_an_empty_memory_is_refused(tmp_home, text):
    with pytest.raises(memory.MemoryError_):
        memory.remember(tmp_home, text)


def test_an_unknown_kind_is_refused(tmp_home):
    with pytest.raises(memory.MemoryError_):
        memory.remember(tmp_home, "something", kind="vibes")


def test_a_hand_edited_memory_still_parses(tmp_home):
    """These are files the user is invited to correct, so a stray colon must
    not take `px0 memory list` down."""
    path = paths.memory_dir(tmp_home) / "handmade.md"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("---\nnot: [valid: yaml\n---\n\nthe fact itself\n")
    entry = memory.parse(path)
    assert "the fact" in entry.text


def test_relevance_prefers_what_shares_words_with_the_query(tmp_home):
    memory.remember(tmp_home, "the API repo is acme/api", subject="API repo")
    memory.remember(tmp_home, "I take lunch at one", subject="lunch")
    found = memory.relevant(tmp_home, "which repo is the API in?")
    assert found[0].subject == "API repo"


def test_a_pinned_memory_is_never_crowded_out(tmp_home):
    """That is what pinning is for."""
    memory.remember(tmp_home, "always sign off as Arpit", subject="sign-off", pinned=True)
    for i in range(40):
        memory.remember(tmp_home, f"unrelated fact number {i} about kubernetes",
                        subject=f"filler {i}")
    found = memory.relevant(tmp_home, "kubernetes", budget=200)
    assert any(m.pinned for m in found)


def test_the_budget_bounds_what_a_run_inlines(tmp_home):
    """A store running for a year should not turn every prompt into a biography."""
    for i in range(50):
        memory.remember(tmp_home, "x" * 200, subject=f"fact {i}")
    found = memory.relevant(tmp_home, "anything", budget=1000)
    spent = sum(len(m.text) + len(m.subject) + 4 for m in found)
    assert spent <= 1000 + memory.MIN_MEMORY_CHARS


def test_one_oversized_memory_cannot_blow_the_budget(tmp_home):
    """The first memory used to be admitted whole whatever its length, so a
    single long one put fifty thousand characters into every prompt from a
    setting that says four thousand."""
    memory.remember(tmp_home, "z" * 50_000, subject="huge")
    found = memory.relevant(tmp_home, "huge", budget=200)
    assert sum(len(m.text) for m in found) <= 200
    assert found[0].text.endswith("[...]")


def test_clipping_does_not_touch_the_file(tmp_home):
    """What is inlined is bounded; what is stored is whatever the user wrote."""
    entry = memory.remember(tmp_home, "z" * 5000, subject="long")
    memory.relevant(tmp_home, "long", budget=100)
    assert len(memory.load_all(tmp_home)[entry.name].text) == 5000


def test_a_pinned_memory_still_gets_room_first(tmp_home):
    memory.remember(tmp_home, "p" * 300, subject="pinned one", pinned=True)
    for i in range(10):
        memory.remember(tmp_home, "y" * 300, subject=f"filler {i}")
    found = memory.relevant(tmp_home, "filler", budget=400)
    assert any(m.pinned for m in found)


def test_memory_reaches_the_prompt(tmp_home, config, monkeypatch):
    memory.remember(tmp_home, "standup goes out before 09:30", subject="standup timing")
    _write(tmp_home, body="Write the standup.")
    seen = _capture_prompt(monkeypatch)
    runner.run(tmp_home, config, "demo", trigger="manual")
    assert "before 09:30" in seen["prompt"]


def test_a_workflow_can_place_the_memory_block_itself(tmp_home, config, monkeypatch):
    memory.remember(tmp_home, "sign off as Arpit", subject="sign-off")
    _write(tmp_home, body="Before:\n{{memory}}\nAfter.")
    seen = _capture_prompt(monkeypatch)
    runner.run(tmp_home, config, "demo", trigger="manual")
    assert seen["prompt"].index("sign off as Arpit") < seen["prompt"].index("After.")


def test_memory_can_be_turned_off(tmp_home, config, monkeypatch):
    memory.remember(tmp_home, "standup goes out before 09:30", subject="standup")
    config_mod.set_key(config, "memory.enabled", "false")
    _write(tmp_home)
    seen = _capture_prompt(monkeypatch)
    runner.run(tmp_home, config, "demo", trigger="manual")
    assert "09:30" not in seen["prompt"]


def test_the_run_records_which_memories_it_used(tmp_home, config, monkeypatch):
    """The first thing you want when a run behaves in a way the instructions
    alone do not explain."""
    memory.remember(tmp_home, "standup goes out before 09:30", subject="standup timing")
    _write(tmp_home)
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="done"))
    record = runner.run(tmp_home, config, "demo", trigger="manual")
    assert record["memories_inlined"]
    assert record["memories_inlined"][0]["subject"] == "standup timing"


def test_a_run_can_write_a_memory_through_the_tool(tmp_home, config):
    from px0 import tools

    result = tools.call(tmp_home, config, "memory.remember",
                        {"text": "the release goes out on Thursdays",
                         "subject": "release day"})
    assert result["subject"] == "release day"
    assert len(memory.load_all(tmp_home)) == 1


def test_a_run_can_look_one_up_through_the_tool(tmp_home, config):
    from px0 import tools

    memory.remember(tmp_home, "the API repo is acme/api", subject="API repo")
    found = tools.call(tmp_home, config, "memory.recall", {"query": "API repo"})
    assert found and found[0]["subject"] == "API repo"


def test_search_ranks_by_relevance_not_by_pinning(tmp_home):
    """Pinning is a claim about what a *run* should always see. Letting it
    outrank the query in a search answers a question nobody asked."""
    memory.remember(tmp_home, "always sign off as Arpit", subject="sign-off", pinned=True)
    memory.remember(tmp_home, "the API repo is acme/api", subject="the API repo")
    found = memory.relevant(tmp_home, "which repo is the API in?", pinned_first=False)
    assert found[0].subject == "the API repo"


def test_a_run_still_sees_pinned_memory_first(tmp_home):
    memory.remember(tmp_home, "always sign off as Arpit", subject="sign-off", pinned=True)
    memory.remember(tmp_home, "the API repo is acme/api", subject="the API repo")
    found = memory.relevant(tmp_home, "which repo is the API in?")
    assert found[0].pinned is True


def test_a_scheduled_pipeline_delivers_its_output(tmp_home, config, monkeypatch):
    """A pipeline routes its last stage's output and then had nowhere to say
    so -- and it is the longest-running thing px0 does, so the least likely to
    have anyone watching when it finishes."""
    for stage in ("one", "two"):
        (paths.workflows_dir(tmp_home) / f"{stage}.md").write_text(
            f"---\nid: {stage}\ndescription: A stage\noutput:\n  target: stdout\n"
            "---\n\nBody.\n")
    (paths.workflows_dir(tmp_home) / "chain.md").write_text(
        "---\nid: chain\ndescription: A pipeline\npipeline:\n  - one\n  - two\n"
        "output:\n  target: stdout\n---\n\nBody.\n")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="## Nightly\n\nall done"))

    runner.run(tmp_home, config, "chain", trigger="schedule")
    entries = inbox.listing(tmp_home)
    assert [e["workflow_id"] for e in entries] == ["chain"]
    assert entries[0]["title"] == "Nightly"


def test_a_manual_pipeline_does_not_deliver(tmp_home, config, monkeypatch):
    for stage in ("one",):
        (paths.workflows_dir(tmp_home) / f"{stage}.md").write_text(
            "---\nid: one\ndescription: A stage\noutput:\n  target: stdout\n"
            "---\n\nBody.\n")
    (paths.workflows_dir(tmp_home) / "chain.md").write_text(
        "---\nid: chain\ndescription: A pipeline\npipeline:\n  - one\n"
        "output:\n  target: stdout\n---\n\nBody.\n")
    monkeypatch.setattr(harness, "invoke_detailed",
                        lambda *a, **kw: harness.Reply(text="output"))
    runner.run(tmp_home, config, "chain", trigger="manual")
    assert inbox.listing(tmp_home) == []
