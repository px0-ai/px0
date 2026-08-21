"""Firing on something happening, rather than on the clock.

Composio publishes event triggers for 42 toolkits, and every one of them
delivers to a public endpoint a laptop does not have. A watch is what a
local-first tool can offer instead: poll a read-only tool, fire when an item
turns up that has not been seen before.

The two behaviours worth pinning are that the first poll only learns a baseline
(otherwise adding a watch to a busy inbox fires on all of it at once), and that
identity comes from the item's own id rather than its position.
"""

import pytest

from px0 import config as config_mod, daemon as daemon_mod, paths, workflow as wf_mod


def _watched(home, wf_id="watcher", tool="github.list_my_prs", every="15m", key=None,
             enabled=True):
    lines = ["---", f"id: {wf_id}", "description: A watcher",
             "trigger:", "  watch:", f"    tool: {tool}", f"    every: {every}"]
    if key:
        lines.append(f"    key: {key}")
    if not enabled:
        lines.append("enabled: false")
    lines += ["output:", "  target: file", "  path: out-{date}.md", "---", "", "Body.", ""]
    (paths.workflows_dir(home) / f"{wf_id}.md").write_text("\n".join(lines))
    return wf_mod.load(home, wf_id)


@pytest.fixture
def config(tmp_home):
    return config_mod.load(paths.config_path(tmp_home))


@pytest.fixture
def spawns(monkeypatch):
    seen = []
    monkeypatch.setattr(daemon_mod, "spawn_run",
                        lambda home, wf_id, late, fire_time: seen.append(wf_id))
    return seen


def _poll(monkeypatch, items):
    from px0 import tools

    monkeypatch.setattr(tools, "call", lambda home, config, tool, args: items)


# --- validation -----------------------------------------------------------

def test_a_watch_needs_a_tool(tmp_home):
    (paths.workflows_dir(tmp_home) / "w.md").write_text(
        "---\nid: w\ntrigger:\n  watch:\n    every: 15m\n---\n\nbody\n")
    wf = wf_mod.load(tmp_home, "w")
    assert any("needs a tool" in e for e in wf_mod.validate(wf, tmp_home))


def test_a_watch_may_not_poll_a_write_tool(tmp_home):
    wf = _watched(tmp_home, tool="slack.post_message")
    assert any("write tool" in e for e in wf_mod.validate(wf, tmp_home))


def test_a_watch_may_not_poll_faster_than_the_floor(tmp_home):
    wf = _watched(tmp_home, every="5s")
    assert any("at least" in e for e in wf_mod.validate(wf, tmp_home))


def test_a_watched_workflow_must_write_to_a_file(tmp_home):
    (paths.workflows_dir(tmp_home) / "w.md").write_text(
        "---\nid: w\ntrigger:\n  watch:\n    tool: github.list_my_prs\n"
        "output:\n  target: stdout\n---\n\nbody\n")
    wf = wf_mod.load(tmp_home, "w")
    assert any("output.target" in e for e in wf_mod.validate(wf, tmp_home))


def test_a_valid_watch_passes(tmp_home):
    assert wf_mod.validate(_watched(tmp_home), tmp_home) == []


def test_an_unparseable_interval_falls_back_rather_than_crashing(tmp_home):
    wf = _watched(tmp_home, every="soon")
    assert wf_mod.watch_spec(wf)["every_seconds"] >= wf_mod.MIN_WATCH_SECONDS


# --- identity -------------------------------------------------------------

def test_items_are_identified_by_their_own_id():
    keys = daemon_mod._watch_keys([{"id": 7, "title": "a"}, {"id": 8, "title": "b"}], None)
    assert keys == ["7", "8"]


def test_a_named_key_wins_over_the_default():
    keys = daemon_mod._watch_keys([{"id": 1, "url": "u"}], "url")
    assert keys == ["u"]


def test_a_wrapped_list_is_unwrapped():
    keys = daemon_mod._watch_keys({"items": [{"id": 1}, {"id": 2}]}, None)
    assert keys == ["1", "2"]


def test_an_item_with_no_id_is_hashed_not_skipped():
    keys = daemon_mod._watch_keys([{"title": "no id here"}], None)
    assert len(keys) == 1 and keys[0]


def test_the_same_item_hashes_the_same_way_twice():
    item = [{"title": "stable", "n": 1}]
    assert daemon_mod._watch_keys(item, None) == daemon_mod._watch_keys(item, None)


# --- polling behaviour ----------------------------------------------------

def test_the_first_poll_only_learns_a_baseline(tmp_home, config, monkeypatch, spawns):
    _watched(tmp_home)
    _poll(monkeypatch, [{"id": 1}, {"id": 2}])

    daemon_mod.tick(tmp_home, config, {})

    assert spawns == []


def test_a_new_item_after_the_baseline_fires_once(tmp_home, config, monkeypatch, spawns):
    _watched(tmp_home)
    _poll(monkeypatch, [{"id": 1}])
    state = {}
    daemon_mod.tick(tmp_home, config, state)

    state["_watches"]["watcher"]["last_poll"] = None  # pretend the interval elapsed
    _poll(monkeypatch, [{"id": 1}, {"id": 2}])
    daemon_mod.tick(tmp_home, config, state)

    assert spawns == ["watcher"]


def test_the_same_items_again_do_not_fire(tmp_home, config, monkeypatch, spawns):
    _watched(tmp_home)
    _poll(monkeypatch, [{"id": 1}])
    state = {}
    daemon_mod.tick(tmp_home, config, state)
    state["_watches"]["watcher"]["last_poll"] = None
    daemon_mod.tick(tmp_home, config, state)

    assert spawns == []


def test_the_interval_is_respected(tmp_home, config, monkeypatch, spawns):
    _watched(tmp_home, every="1h")
    _poll(monkeypatch, [{"id": 1}])
    state = {}
    daemon_mod.tick(tmp_home, config, state)
    first_poll = state["_watches"]["watcher"]["last_poll"]

    _poll(monkeypatch, [{"id": 1}, {"id": 2}])
    daemon_mod.tick(tmp_home, config, state)

    # too soon: the poll did not happen, so nothing fired
    assert state["_watches"]["watcher"]["last_poll"] == first_poll
    assert spawns == []


def test_a_disabled_watch_is_not_polled(tmp_home, config, monkeypatch, spawns):
    _watched(tmp_home, enabled=False)
    called = []

    from px0 import tools

    monkeypatch.setattr(tools, "call", lambda *a, **k: called.append(1) or [])

    daemon_mod.tick(tmp_home, config, {})

    assert called == []


def test_a_failing_poll_is_logged_and_does_not_stop_the_tick(tmp_home, config, monkeypatch, spawns):
    _watched(tmp_home)
    from px0 import tools

    monkeypatch.setattr(tools, "call",
                        lambda *a, **k: (_ for _ in ()).throw(tools.ConnectorError("down")))

    daemon_mod.tick(tmp_home, config, {})  # must not raise

    assert spawns == []


def test_what_a_watch_remembers_is_capped(tmp_home, config, monkeypatch, spawns):
    _watched(tmp_home)
    state = {}
    _poll(monkeypatch, [{"id": i} for i in range(daemon_mod.WATCH_SEEN_LIMIT + 200)])

    daemon_mod.tick(tmp_home, config, state)

    assert len(state["_watches"]["watcher"]["seen"]) <= daemon_mod.WATCH_SEEN_LIMIT
