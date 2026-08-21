"""Authorizing any toolkit, and the rest of the new surface.

The headline here is that `TOOLKIT_SLUGS` is no longer a whitelist: px0 could
discover a tool from any of Composio's toolkits, confirm it, cache it, and then
refuse to authorize it because the app was not one of four. The other half is
the alias/slug mismatch that made discovered Google Calendar tools unusable
even once authorized.
"""

import argparse
import io
import json

import pytest

from px0 import (catalogue, cli, completion, connect as connect_mod, credentials as creds_mod,
                 config as config_mod, mcp, paths, retrieval, status as status_mod,
                 store as store_mod, tools)


# --- any toolkit can be named ---------------------------------------------

@pytest.mark.parametrize("app, slug", [
    ("github", "github"),
    ("calendar", "googlecalendar"),      # px0's own alias
    ("googlecalendar", "googlecalendar"),
    ("linear", "linear"),                 # never authorizable before
    ("google_drive", "google_drive"),
    ("SLACK", "slack"),
])
def test_a_toolkit_name_resolves_to_its_slug(app, slug):
    assert connect_mod.toolkit_slug(app) == slug


@pytest.mark.parametrize("bad", ["", "not a slug", "Has-Dash", "../etc", "x/y"])
def test_something_that_cannot_be_a_slug_is_refused_locally(bad):
    with pytest.raises(ValueError):
        connect_mod.toolkit_slug(bad)


def test_connecting_a_toolkit_outside_the_old_whitelist_works(tmp_home, fake_composio):
    connect_mod.setup_composio(tmp_home, "cmp_testkey")

    result = connect_mod.connect_composio_app(tmp_home, "linear")

    assert result["redirect_url"]
    assert "linear" in connect_mod.connected_accounts(tmp_home)


def test_accounts_are_keyed_by_slug_not_by_alias(tmp_home, fake_composio):
    connect_mod.setup_composio(tmp_home, "cmp_testkey")

    connect_mod.connect_composio_app(tmp_home, "calendar")

    accounts = connect_mod.connected_accounts(tmp_home)
    # A discovered GOOGLECALENDAR_* tool executes with the slug, so the account
    # has to be findable under it.
    assert "googlecalendar" in accounts
    assert "calendar" not in accounts


def test_a_store_written_before_slug_keying_is_migrated(tmp_home):
    creds_mod.set_service(tmp_home, "composio",
                          {"api_key": "k", "connected_accounts": {"calendar": "ca_1"}})

    moved = connect_mod.migrate_account_keys(tmp_home)

    assert moved == {"calendar": "googlecalendar"}
    assert connect_mod.connected_accounts(tmp_home) == {"googlecalendar": "ca_1"}


def test_migration_is_idempotent(tmp_home):
    creds_mod.set_service(tmp_home, "composio",
                          {"api_key": "k", "connected_accounts": {"calendar": "ca_1"}})
    connect_mod.migrate_account_keys(tmp_home)
    assert connect_mod.migrate_account_keys(tmp_home) == {}


def test_a_curated_calendar_tool_and_a_discovered_one_find_the_same_account(tmp_home, fake_composio):
    connect_mod.setup_composio(tmp_home, "cmp_testkey")
    connect_mod.connect_composio_app(tmp_home, "calendar")
    config = config_mod.load(paths.config_path(tmp_home))

    # curated: passes px0's own name
    tools.call(tmp_home, config, "calendar.list_events", {"window": "today"})
    # discovered: passes the toolkit slug
    catalogue.remember(tmp_home, [catalogue.CatalogueTool(
        slug="GOOGLECALENDAR_EVENTS_LIST", toolkit="googlecalendar",
        name="List events", description="d", is_write=False)])
    tools.call(tmp_home, config, "composio:GOOGLECALENDAR_EVENTS_LIST", {})


def test_disconnect_removes_the_local_record(tmp_home, fake_composio):
    connect_mod.setup_composio(tmp_home, "cmp_testkey")
    connect_mod.connect_composio_app(tmp_home, "slack")

    result = connect_mod.disconnect_composio_app(tmp_home, "slack")

    assert result["removed"] is True
    assert "slack" not in connect_mod.connected_accounts(tmp_home)


def test_disconnecting_what_was_never_connected_says_so(tmp_home):
    assert connect_mod.disconnect_composio_app(tmp_home, "slack")["removed"] is False


# --- the catalogue cache can now be evicted -------------------------------

def _cached(slug="GITHUB_LIST_X", toolkit="github"):
    return catalogue.CatalogueTool(slug=slug, toolkit=toolkit, name=slug,
                                    description="d", is_write=False)


def test_forgetting_one_tool_leaves_the_others(tmp_home):
    catalogue.remember(tmp_home, [_cached("A"), _cached("B")])

    removed = catalogue.forget(tmp_home, ["A"])

    assert removed == 1
    assert set(catalogue.load_cached(tmp_home)) == {"composio:B"}


def test_forgetting_everything_empties_the_cache(tmp_home):
    catalogue.remember(tmp_home, [_cached("A"), _cached("B")])
    assert catalogue.forget(tmp_home) == 2
    assert catalogue.load_cached(tmp_home) == {}


def test_refresh_drops_a_tool_the_catalogue_no_longer_has(tmp_home, monkeypatch):
    catalogue.remember(tmp_home, [_cached("GONE"), _cached("STAYS")])

    def fetch(home, slug):
        if slug == "GONE":
            raise catalogue.CatalogueError("404 no Composio tool with slug 'GONE'")
        return _cached(slug)

    monkeypatch.setattr(catalogue, "fetch", fetch)

    result = catalogue.refresh(tmp_home)

    assert result["dropped"] == ["GONE"]
    assert set(catalogue.load_cached(tmp_home)) == {"composio:STAYS"}


def test_refresh_notices_a_changed_schema(tmp_home, monkeypatch):
    catalogue.remember(tmp_home, [_cached("T")])
    changed = catalogue.CatalogueTool(slug="T", toolkit="github", name="T",
                                       description="new words", is_write=True)
    monkeypatch.setattr(catalogue, "fetch", lambda home, slug: changed)

    result = catalogue.refresh(tmp_home)

    assert result["refreshed"] == 1
    assert catalogue.load_cached(tmp_home)["composio:T"].is_write is True


# --- store import / verify ------------------------------------------------

def test_an_export_can_be_imported_into_a_fresh_store(tmp_home, tmp_path):
    (paths.workflows_dir(tmp_home) / "w.md").write_text("---\nid: w\n---\n\nbody\n")
    store_mod.export(tmp_home, tmp_path / "exp")

    target = tmp_path / "fresh"
    report = store_mod.import_store(target, tmp_path / "exp")

    assert (target / "workflows" / "w.md").exists()
    assert report["files"] >= 1


def test_importing_into_an_existing_store_stops_by_default(tmp_home, tmp_path):
    store_mod.export(tmp_home, tmp_path / "exp")
    with pytest.raises(store_mod.StoreError):
        store_mod.import_store(tmp_home, tmp_path / "exp")


def test_merge_keeps_what_is_already_there(tmp_home, tmp_path):
    (paths.workflows_dir(tmp_home) / "w.md").write_text("theirs")
    store_mod.export(tmp_home, tmp_path / "exp")
    (paths.workflows_dir(tmp_home) / "w.md").write_text("mine")

    store_mod.import_store(tmp_home, tmp_path / "exp", merge=True)

    assert (paths.workflows_dir(tmp_home) / "w.md").read_text() == "mine"


def test_force_lets_the_import_win(tmp_home, tmp_path):
    (paths.workflows_dir(tmp_home) / "w.md").write_text("theirs")
    store_mod.export(tmp_home, tmp_path / "exp")
    (paths.workflows_dir(tmp_home) / "w.md").write_text("mine")

    store_mod.import_store(tmp_home, tmp_path / "exp", force=True)

    assert (paths.workflows_dir(tmp_home) / "w.md").read_text() == "theirs"


def test_importing_something_that_is_not_an_export_is_refused(tmp_path):
    (tmp_path / "random").mkdir()
    with pytest.raises(store_mod.StoreError):
        store_mod.import_store(tmp_path / "target", tmp_path / "random")


def test_a_live_config_is_never_overwritten_by_an_import(tmp_home, tmp_path):
    config_mod.save(paths.config_path(tmp_home),
                    {"connectors": {"composio_api_key": "keep-me"}})
    store_mod.export(tmp_home, tmp_path / "exp")

    store_mod.import_store(tmp_home, tmp_path / "exp", merge=True)

    config = config_mod.load(paths.config_path(tmp_home))
    assert config_mod.get(config, "connectors.composio_api_key") == "keep-me"


def test_verify_is_clean_on_a_fresh_store(tmp_home):
    assert store_mod.verify(tmp_home)["ok"] is True


def test_verify_reports_a_workflow_naming_a_missing_guideline(tmp_home):
    (paths.workflows_dir(tmp_home) / "w.md").write_text(
        "---\nid: w\nguidelines:\n  - gone.md\n---\n\nbody\n")

    report = store_mod.verify(tmp_home)

    assert report["ok"] is False
    assert any(p["kind"] == "guideline" for p in report["problems"])


def test_verify_reports_an_unparseable_workflow(tmp_home):
    (paths.workflows_dir(tmp_home) / "broken.md").write_text("no frontmatter here")
    assert store_mod.verify(tmp_home)["ok"] is False


# --- config unset ---------------------------------------------------------

def test_unset_returns_a_key_to_its_default():
    config = {"tools": {"allow_shell": True}}
    assert config_mod.unset_key(config, "tools.allow_shell") is False
    assert config == {}


def test_unset_on_a_key_that_was_never_set_is_not_an_error():
    assert config_mod.unset_key({}, "runs.max_attempts") == 1


def test_unset_refuses_an_unknown_key():
    with pytest.raises(ValueError):
        config_mod.unset_key({}, "not.a.key")


# --- completion -----------------------------------------------------------

def test_completion_offers_verbs_for_a_group():
    assert "validate" in completion.complete(cli.build_parser(), ["workflows", ""])


def test_completion_offers_flags_when_a_dash_is_typed():
    out = completion.complete(cli.build_parser(), ["workflows", "run", "--"])
    assert "--dry-run" in out


def test_completion_hides_internal_flags():
    out = completion.complete(cli.build_parser(), ["workflows", "run", "--"])
    assert "--late-scheduled-at" not in out


def test_completion_offers_config_keys():
    out = completion.complete(cli.build_parser(), ["config", "set", "brain."])
    assert "brain.path" in out


def test_completion_offers_workflow_ids_from_the_store(tmp_home):
    (paths.workflows_dir(tmp_home) / "friday.md").write_text("---\nid: friday\n---\n\nx\n")
    out = completion.complete(cli.build_parser(), ["workflows", "run", ""], home=tmp_home)
    assert "friday" in out


@pytest.mark.parametrize("shell", ["bash", "zsh", "fish"])
def test_every_supported_shell_has_a_script(shell):
    assert "px0" in completion.script(shell)


def test_an_unsupported_shell_is_refused():
    with pytest.raises(ValueError):
        completion.script("csh")


# --- mcp ------------------------------------------------------------------

def _rpc(home, config, messages, allow_runs=False):
    out = io.StringIO()
    mcp.serve(home, config, allow_runs=allow_runs,
              stdin=io.StringIO("\n".join(json.dumps(m) for m in messages)), stdout=out)
    return [json.loads(line) for line in out.getvalue().splitlines()]


def test_initialize_reports_the_protocol_and_the_server(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    [reply] = _rpc(tmp_home, config, [{"jsonrpc": "2.0", "id": 1, "method": "initialize"}])
    assert reply["result"]["protocolVersion"] == mcp.PROTOCOL_VERSION
    assert reply["result"]["serverInfo"]["name"] == "px0"


def test_running_a_workflow_is_not_offered_unless_it_is_enabled(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    [reply] = _rpc(tmp_home, config, [{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}])
    names = {t["name"] for t in reply["result"]["tools"]}
    assert "workflow_run" not in names

    [reply] = _rpc(tmp_home, config, [{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}],
                   allow_runs=True)
    assert "workflow_run" in {t["name"] for t in reply["result"]["tools"]}


def test_calling_workflow_run_without_the_flag_is_refused_not_ignored(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    [reply] = _rpc(tmp_home, config, [{
        "jsonrpc": "2.0", "id": 1, "method": "tools/call",
        "params": {"name": "workflow_run", "arguments": {"workflow": "x"}}}])
    assert reply["result"]["isError"] is True
    assert "--allow-runs" in reply["result"]["content"][0]["text"]


def test_a_notification_gets_no_reply(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    assert _rpc(tmp_home, config,
                [{"jsonrpc": "2.0", "method": "notifications/initialized"}]) == []


def test_unparseable_input_is_answered_rather_than_crashed_on(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    out = io.StringIO()
    mcp.serve(tmp_home, config, stdin=io.StringIO("not json\n"), stdout=out)
    assert json.loads(out.getvalue())["error"]["code"] == -32700


def test_workflows_list_marks_a_disabled_workflow(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    (paths.workflows_dir(tmp_home) / "w.md").write_text(
        "---\nid: w\ndescription: d\nenabled: false\n---\n\nbody\n")

    [reply] = _rpc(tmp_home, config, [{
        "jsonrpc": "2.0", "id": 1, "method": "tools/call",
        "params": {"name": "workflows_list", "arguments": {}}}])

    assert "(disabled)" in reply["result"]["content"][0]["text"]


# --- status ---------------------------------------------------------------

def test_status_flags_a_scheduled_workflow_with_no_daemon(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    (paths.workflows_dir(tmp_home) / "w.md").write_text(
        "---\nid: w\ntrigger:\n  schedule: \"0 9 * * *\"\noutput:\n  target: file\n"
        "  path: o.md\n---\n\nbody\n")

    report = status_mod.collect(tmp_home, config)

    assert report["ok"] is False
    assert any("daemon is not running" in p["detail"] for p in report["problems"])


def test_status_counts_a_disabled_workflow_separately(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    (paths.workflows_dir(tmp_home) / "w.md").write_text(
        "---\nid: w\nenabled: false\ntrigger:\n  schedule: \"0 9 * * *\"\n"
        "output:\n  target: file\n  path: o.md\n---\n\nbody\n")

    report = status_mod.collect(tmp_home, config)

    assert report["workflows"]["disabled"] == ["w"]
    assert report["workflows"]["scheduled"] == []


def test_status_reports_an_unparseable_workflow(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    (paths.workflows_dir(tmp_home) / "bad.md").write_text("no frontmatter")

    report = status_mod.collect(tmp_home, config)

    assert report["workflows"]["unparseable"] == 1
    assert report["ok"] is False


# --- rerank ---------------------------------------------------------------

def _passage(path, text, score):
    return retrieval.Passage(path=path, anchor="1", text=text, score=score,
                              ingested_at="2026-01-01", is_stub=False, kind="blog")


def test_rerank_prefers_the_passage_that_covers_every_term():
    passages = [
        _passage("a.md", "caching caching caching caching", 9.0),
        _passage("b.md", "caching with invalidation in a distributed cache", 1.0),
    ]

    out = retrieval.rerank("caching invalidation distributed", passages, 2)

    assert out[0].path == "b.md"


def test_rerank_is_stable_when_nothing_matches():
    passages = [_passage("a.md", "unrelated", 1.0), _passage("b.md", "also unrelated", 2.0)]
    assert [p.path for p in retrieval.rerank("zzz", passages, 2)] == ["a.md", "b.md"]


def test_rerank_returns_at_most_k():
    passages = [_passage(f"{i}.md", "caching", float(i)) for i in range(10)]
    assert len(retrieval.rerank("caching", passages, 3)) == 3


def test_an_empty_query_does_not_reorder():
    passages = [_passage("a.md", "x", 1.0), _passage("b.md", "y", 2.0)]
    assert [p.path for p in retrieval.rerank("", passages, 5)] == ["a.md", "b.md"]


# --- the v2 -> v3 migration ----------------------------------------------

def test_the_v3_migration_reskeys_accounts_and_scaffolds_tools(tmp_home):
    from px0 import update

    creds_mod.set_service(tmp_home, "composio",
                          {"api_key": "k", "connected_accounts": {"calendar": "ca_1"}})
    (paths.tools_dir(tmp_home) / "example.toml.sample").unlink()

    changes = update._migrate_v2_to_v3(tmp_home)

    assert changes == []  # credentials are deliberately outside the version chain
    assert connect_mod.connected_accounts(tmp_home) == {"googlecalendar": "ca_1"}
    assert (paths.tools_dir(tmp_home) / "example.toml.sample").exists()


def test_the_v3_migration_is_safe_to_run_twice(tmp_home):
    from px0 import update

    update._migrate_v2_to_v3(tmp_home)
    update._migrate_v2_to_v3(tmp_home)
