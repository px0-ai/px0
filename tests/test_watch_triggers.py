"""Firing on something happening, rather than on the clock.

Composio publishes event triggers for 42 toolkits, and every one of them
delivers to a public endpoint a laptop does not have. A watch is what a
local-first tool can offer instead: poll a read-only tool, fire when an item
turns up that has not been seen before.

The two behaviours worth pinning are that the first poll only learns a baseline
(otherwise adding a watch to a busy inbox fires on all of it at once), and that
identity comes from the item's own id rather than its position.
"""

from datetime import timedelta

import pytest

from px0 import config as config_mod, daemon as daemon_mod, paths, workflow as wf_mod


def _watched(home, wf_id="watcher", tool="github.list_my_prs", every="15m", key=None,
             enabled=True, extra=""):
    lines = ["---", f"id: {wf_id}", "description: A watcher",
             "trigger:", "  watch:", f"    tool: {tool}", f"    every: {every}"]
    if key:
        lines.append(f"    key: {key}")
    if extra:
        lines.extend(extra.rstrip("\n").split("\n"))
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
    """Records what the daemon would have launched.

    A list of workflow ids, plus `.stdin` holding what the last spawn was
    piped -- which is how a watch hands the run it triggered the items that
    were actually new.
    """
    class Spawns(list):
        stdin = None
        trigger = None

    seen = Spawns()

    def _spawn(home, wf_id, late, fire_time, stdin_text=None, trigger="schedule"):
        seen.append(wf_id)
        seen.stdin = stdin_text
        seen.trigger = trigger

    monkeypatch.setattr(daemon_mod, "spawn_run", _spawn)
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


# --- what a watch tells the run it triggered ------------------------------

def test_a_watch_hands_the_run_what_was_new(tmp_home, config, monkeypatch, spawns):
    """A watch fired and told the run nothing, so the workflow had to go and
    look again -- a second call, and a different answer from the one that
    triggered it."""
    _watched(tmp_home)
    _poll(monkeypatch, [{"id": 1}])
    state = {}
    daemon_mod.tick(tmp_home, config, state)

    state["_watches"]["watcher"]["last_poll"] = None
    _poll(monkeypatch, [{"id": 1}, {"id": 2}, {"id": 3}])
    daemon_mod.tick(tmp_home, config, state)

    assert spawns == ["watcher"]
    assert set(spawns.stdin.split("\n")) == {"2", "3"}


def test_a_watch_can_wait_for_enough_of_them(tmp_home, config, monkeypatch, spawns):
    _watched(tmp_home, extra="    min_items: 3\n")
    _poll(monkeypatch, [{"id": 1}])
    state = {}
    daemon_mod.tick(tmp_home, config, state)

    state["_watches"]["watcher"]["last_poll"] = None
    _poll(monkeypatch, [{"id": 1}, {"id": 2}])
    daemon_mod.tick(tmp_home, config, state)
    assert spawns == []

    # Held back rather than forgotten: the two already seen still count.
    state["_watches"]["watcher"]["last_poll"] = None
    _poll(monkeypatch, [{"id": 1}, {"id": 2}, {"id": 3}, {"id": 4}])
    daemon_mod.tick(tmp_home, config, state)
    assert spawns == ["watcher"]


def test_a_bad_min_items_fails_validation(tmp_home):
    from px0 import workflow as wf_mod

    _watched(tmp_home, extra="    min_items: 0\n")
    wf = wf_mod.load(tmp_home, "watcher")
    assert any("min_items" in e for e in wf_mod.validate(wf, tmp_home))


# --- the clock a schedule is read against ---------------------------------

def test_a_workflow_can_pin_its_own_timezone(tmp_home, config):
    from px0 import workflow as wf_mod

    (paths.workflows_dir(tmp_home) / "zoned.md").write_text(
        "---\nid: zoned\ndescription: A demo\ntrigger:\n  schedule: '0 9 * * *'\n"
        "  timezone: Asia/Kolkata\noutput:\n  target: file\n  path: output/x.md\n"
        "---\n\nBody.\n")
    wf = wf_mod.load(tmp_home, "zoned")
    assert wf_mod.validate(wf, tmp_home) == []
    assert str(daemon_mod.resolve_zone(config, wf)) == "Asia/Kolkata"


def test_a_zone_this_machine_does_not_know_fails_validation(tmp_home):
    """Ignoring it would fall back to machine time, which looks like it worked
    and fires at the wrong hour -- exactly what the setting exists to prevent."""
    from px0 import workflow as wf_mod

    (paths.workflows_dir(tmp_home) / "zoned.md").write_text(
        "---\nid: zoned\ndescription: A demo\ntrigger:\n  schedule: '0 9 * * *'\n"
        "  timezone: Mars/Olympus\noutput:\n  target: file\n  path: output/x.md\n"
        "---\n\nBody.\n")
    wf = wf_mod.load(tmp_home, "zoned")
    assert any("zone" in e for e in wf_mod.validate(wf, tmp_home))


def test_the_store_default_applies_when_a_workflow_names_none(tmp_home, config):
    from px0 import config as config_module, workflow as wf_mod

    config_module.set_key(config, "schedule.timezone", "Europe/London")
    _watched(tmp_home)
    wf = wf_mod.load(tmp_home, "watcher")
    assert str(daemon_mod.resolve_zone(config, wf)) == "Europe/London"


def test_naming_no_zone_anywhere_follows_the_machine(tmp_home, config):
    from px0 import workflow as wf_mod

    _watched(tmp_home)
    assert daemon_mod.resolve_zone(config, wf_mod.load(tmp_home, "watcher")) is None


def test_fires_stored_before_zones_existed_still_compare(tmp_home):
    """State on disk is naive local time. Comparing it against an aware `now`
    raises rather than being quietly wrong, so it is read on the same clock."""
    from datetime import datetime
    from zoneinfo import ZoneInfo

    zone = ZoneInfo("Asia/Kolkata")
    now = datetime.now(zone)
    naive_last = datetime.now().replace(tzinfo=None) - timedelta(hours=3)
    fires = daemon_mod._due_fires("*/30 * * * *", naive_last, now)
    assert isinstance(fires, list)


def test_a_watch_says_what_fired_the_run(tmp_home, config, monkeypatch, spawns):
    """A spawned run is a subprocess and has no other way to know what started
    it. Without being told it recorded itself as `manual`, and everything that
    treats unattended runs differently -- the inbox, the circuit breaker,
    approval notices, the budget -- read that and stood down.
    """
    _watched(tmp_home)
    _poll(monkeypatch, [{"id": 1}])
    state = {}
    daemon_mod.tick(tmp_home, config, state)
    state["_watches"]["watcher"]["last_poll"] = None
    _poll(monkeypatch, [{"id": 1}, {"id": 2}])
    daemon_mod.tick(tmp_home, config, state)
    assert spawns.trigger == "watch"


def test_a_scheduled_fire_says_so_too(tmp_home, config, monkeypatch, spawns):
    (paths.workflows_dir(tmp_home) / "nightly.md").write_text(
        "---\nid: nightly\ndescription: A demo\ntrigger:\n  schedule: '* * * * *'\n"
        "output:\n  target: file\n  path: out-{date}.md\n---\n\nBody.\n")
    daemon_mod.tick(tmp_home, config, {})
    assert "nightly" in spawns
    assert spawns.trigger == "schedule"


def test_the_spawned_command_carries_the_trigger(tmp_home, monkeypatch):
    """The flag has to actually reach argv, or the run still says `manual`."""
    from datetime import datetime

    seen = {}

    class FakeProc:
        stdin = None

    def fake_popen(args, **kwargs):
        seen["args"] = args
        return FakeProc()

    monkeypatch.setattr(daemon_mod.subprocess, "Popen", fake_popen)
    daemon_mod.spawn_run(tmp_home, "demo", late=False, fire_time=datetime.now(),
                         trigger="watch")
    assert "--trigger" in seen["args"]
    assert seen["args"][seen["args"].index("--trigger") + 1] == "watch"


def test_a_late_fire_is_labelled_late_not_scheduled(tmp_home, monkeypatch):
    """`late` already tells the record it ran behind, and passing both would
    have the CLI choose between two labels for one run."""
    from datetime import datetime

    seen = {}

    class FakeProc:
        stdin = None

    monkeypatch.setattr(daemon_mod.subprocess, "Popen",
                        lambda args, **kw: seen.setdefault("args", args) and None
                        or FakeProc())
    daemon_mod.spawn_run(tmp_home, "demo", late=True, fire_time=datetime.now())
    assert "--late-scheduled-at" in seen["args"]
    assert "--trigger" not in seen["args"]
