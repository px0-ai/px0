"""The knowledge -> brain rename, applied to a store written before it.

The rename moved a folder and a config key, so a v1 store cannot be read as-is.
`px0 update` runs the forward-only migration; these tests pin what it must do
to a real store rather than a mocked one.
"""

import json

import pytest

from px0 import SCHEMA_VERSION, config as config_mod, paths, retrieval, store, update


@pytest.fixture
def v1_store(tmp_path):
    """A store as px0 v1 wrote it: `knowledge/` on disk, `[knowledge] path` in config."""
    home = tmp_path / "old_store"
    home.mkdir()
    store.init(home)

    # Undo the v2 layout that init now produces, back to what v1 had.
    brain_dir = home / "brain"
    knowledge = home / "knowledge"
    brain_dir.rename(knowledge)
    for folder in ("docs", "blogs", "papers"):
        (knowledge / folder).mkdir(parents=True, exist_ok=True)
    (knowledge / "blogs" / "a-post.md").write_text(
        "---\nsource: https://x.test\nretrieved: 2026-01-01\n---\n\nA saved post.\n"
    )
    (knowledge / "work" / "internal.md").parent.mkdir(parents=True, exist_ok=True)
    (knowledge / "work" / "internal.md").write_text(
        "---\nsource: local\n---\n\nInternal note.\n"
    )

    config = config_mod.load(paths.config_path(home))
    config.pop("brain", None)
    config["knowledge"] = {"path": str(knowledge)}
    config_mod.save(paths.config_path(home), config)
    paths.schema_path(home).write_text("1")
    return home


def test_the_migration_is_keyed_by_the_version_it_produces():
    """The runner applies every key greater than the store's current version.

    Keyed 1, a v1 -> v2 migration would never run at all: `1 > 1` is false.
    """
    assert 2 in update.MIGRATIONS
    assert max(update.MIGRATIONS) <= SCHEMA_VERSION


def test_the_folder_is_moved_with_its_contents(v1_store):
    update._migrate_v1_to_v2(v1_store)

    assert not (v1_store / "knowledge").exists()
    assert (v1_store / "brain" / "blogs" / "a-post.md").is_file()
    assert "A saved post." in (v1_store / "brain" / "blogs" / "a-post.md").read_text()


def test_private_work_files_survive_the_move(v1_store):
    """work/ is the folder with a promise attached; losing it would be the worst
    possible outcome of a rename."""
    update._migrate_v1_to_v2(v1_store)

    assert (v1_store / "brain" / "work" / "internal.md").is_file()


def test_the_config_key_is_renamed_and_repointed(v1_store):
    update._migrate_v1_to_v2(v1_store)

    config = config_mod.load(paths.config_path(v1_store))
    assert "knowledge" not in config
    assert config["brain"]["path"] == str(v1_store / "brain")


def test_a_library_kept_outside_the_store_is_left_where_it_is(tmp_path):
    """`brain.path` is documented as pointable at an existing notes vault.

    Rewriting that path to `<store>/brain` would silently detach the user from
    their own vault, so only a path pointing at the store's own folder moves.
    """
    home = tmp_path / "store"
    home.mkdir()
    store.init(home)
    (home / "brain").rename(home / "knowledge")

    vault = tmp_path / "notes-vault"
    vault.mkdir()
    config = config_mod.load(paths.config_path(home))
    config.pop("brain", None)
    config["knowledge"] = {"path": str(vault)}
    config_mod.save(paths.config_path(home), config)

    update._migrate_v1_to_v2(home)

    assert config_mod.load(paths.config_path(home))["brain"]["path"] == str(vault)
    assert vault.is_dir()


def test_a_half_migrated_store_merges_without_clobbering(v1_store):
    """Both folders can exist if a previous run was interrupted."""
    existing = v1_store / "brain" / "blogs"
    existing.mkdir(parents=True, exist_ok=True)
    (existing / "already-there.md").write_text("---\nsource: y\n---\n\nAlready moved.\n")
    (existing / "a-post.md").write_text("---\nsource: z\n---\n\nNewer copy wins.\n")

    update._migrate_v1_to_v2(v1_store)

    assert not (v1_store / "knowledge").exists()
    assert (existing / "already-there.md").is_file()
    # The file already under brain/ is the one kept -- a migration must not
    # overwrite content that is already in its destination.
    assert "Newer copy wins." in (existing / "a-post.md").read_text()


def test_the_migration_records_the_config_change_for_version_history(v1_store):
    """Everything that edits a versioned file hands back a FileChange, so the
    store's own history shows the migration alongside every other change."""
    changes = update._migrate_v1_to_v2(v1_store)

    assert [c.rel_path for c in changes] == ["config.toml"]
    assert b"[brain]" in changes[0].content


def test_the_migrated_store_is_searchable(v1_store, monkeypatch):
    update._migrate_v1_to_v2(v1_store)

    canned = '[{"file": "qmd://px0-brain/blogs/a-post.md", "score": 0.9, "snippet": "A saved post."}]'
    monkeypatch.setattr("builtins.input", lambda *a: "n")
    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run",
                        lambda config, *a, **k: "1 passage indexed" if a and a[0] == "update" else canned)

    config = config_mod.load(paths.config_path(v1_store))
    assert retrieval.reindex(v1_store, config) > 0
    hits = retrieval.retrieve(v1_store, config, "saved post", k=5)
    assert [h.path for h in hits] == ["blogs/a-post.md"]


def test_a_store_with_no_knowledge_folder_migrates_harmlessly(tmp_path):
    """A store created fresh at v2 must not be damaged by the migration running."""
    home = tmp_path / "fresh"
    home.mkdir()
    store.init(home)

    update._migrate_v1_to_v2(home)

    assert (home / "brain").is_dir()
    assert config_mod.load(paths.config_path(home))["brain"]["path"] == str(home / "brain")


def test_the_stale_qmd_collection_is_dropped(v1_store, monkeypatch):
    """The rename renames the qmd collection too.

    Left behind, `px0-knowledge` points at a `knowledge/` folder that no longer
    exists, and qmd keeps it alongside the new `px0-brain` one.
    """
    calls = []

    def _fake_qmd(cfg, *args, **kw):
        calls.append(args)
        return "px0-knowledge\npx0-brain" if args == ("collection", "list") else ""

    monkeypatch.setattr(retrieval, "_qmd_run", _fake_qmd)

    update._migrate_v1_to_v2(v1_store)

    assert ("collection", "remove", "px0-knowledge") in calls


def test_a_broken_qmd_does_not_fail_the_store_migration(v1_store, monkeypatch):
    """Losing the folder move because an optional tool is unhappy would be far
    worse than leaving one stale collection behind."""
    monkeypatch.setattr(
        retrieval, "_qmd_run",
        lambda *a, **k: (_ for _ in ()).throw(retrieval.RetrievalBackendError("qmd is gone")),
    )

    update._migrate_v1_to_v2(v1_store)

    assert (v1_store / "brain" / "blogs" / "a-post.md").is_file()
    assert config_mod.load(paths.config_path(v1_store))["brain"]["path"] == str(v1_store / "brain")


def test_the_migration_runs_through_the_update_runner(v1_store, monkeypatch):
    """The registry keying is only correct if the runner actually applies it."""
    monkeypatch.setattr(update, "check", lambda config: {
        "update_available": True, "available_version": "0.2.0", "channel": "stable",
    })
    monkeypatch.setattr(update, "_detect_install_mechanism", lambda home: "pip")
    monkeypatch.setattr(update.subprocess, "run",
                        lambda *a, **k: type("_R", (), {"returncode": 0, "stderr": "", "stdout": ""})())
    monkeypatch.setattr(update.daemon_mod, "restart_if_running", lambda *a, **k: None)
    monkeypatch.setattr(update.doctor, "run", lambda *a, **k: {"all_ok": True, "checks": {}})
    monkeypatch.setattr("px0.versioning.record_change", lambda *a, **k: "chg_1")

    config = config_mod.load(paths.config_path(v1_store))
    update.run_update(v1_store, config)

    history = json.loads(paths.update_history_path(v1_store).read_text())
    # Every migration newer than the store's version runs, in order.
    assert history[-1]["migrations_applied"] == [2, 3]
    assert paths.schema_path(v1_store).read_text().strip() == "3"
    assert (v1_store / "brain").is_dir() and not (v1_store / "knowledge").exists()


def test_the_migration_completes_the_v2_layout(v1_store):
    """work/ postdates the folders an older store was scaffolded with.

    Renaming v1's folders is not enough: a migrated store should look like one
    `px0 init` would produce today, or the private folder is missing on exactly
    the stores that have been around longest.
    """
    import shutil
    shutil.rmtree(v1_store / "knowledge" / "work")   # an older store had no work/

    update._migrate_v1_to_v2(v1_store)

    for folder in ("docs", "blogs", "papers", "work"):
        assert (v1_store / "brain" / folder).is_dir(), folder
