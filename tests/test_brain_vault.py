"""Pointing `brain.path` at an existing notes vault.

An Obsidian vault and a px0 brain are the same shape -- a folder of Markdown --
so `brain.path` should be able to point straight at one. What makes that work is
not the reading, which was always fine, but everything a real vault carries
beside the notes: the app's own config, its local trash, drawings stored as
Markdown, and a top-level folder called `work` that means something else here.
"""

import pytest

from px0 import brain, cli, config as config_mod, doctor, paths, retrieval


def _write(base, rel, text):
    path = base / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    return path


@pytest.fixture
def vault(tmp_path):
    """A vault with the things a real one has that a px0 store does not."""
    v = tmp_path / "MyVault"
    # Obsidian's own config, and the Markdown its plugins ship
    _write(v, ".obsidian/plugins/dataview/README.md", "# Dataview\n\nUse TABLE FROM.\n")
    (v / ".obsidian" / "app.json").write_text('{"theme":"obsidian"}')
    # a note the user deleted: Obsidian moves it here, it must not stay searchable
    _write(v, ".trash/deleted.md", "# Old Salary Notes\n\nI asked for forty percent.\n")
    # a drawing: Markdown wrapping a JSON blob, all on one line
    _write(v, "Attachments/diagram.excalidraw.md",
           "---\nexcalidraw-plugin: parsed\n---\n# Excalidraw Data\n## Drawing\n"
           '```json\n{"elements":[' + ",".join(
               f'{{"id":"el{i}","seed":123456789}}' for i in range(200)) + "]}\n```\n")
    # ordinary notes, with wikilinks and tags
    _write(v, "Personal/Reading/consistent-hashing.md",
           "---\ntags: [distributed-systems]\naliases: [\"ring hashing\"]\n---\n"
           "# Consistent Hashing\n\nSee [[Sharding]] for related ideas. #important\n")
    _write(v, "Daily Notes/2026-08-21.md",
           "# 2026-08-21\n\n- Met the platform team about backpressure.\n")
    # the collision: an ordinary work-notes folder
    _write(v, "work/quarterly-planning.md",
           "---\ntags: [work]\n---\n# Quarterly Planning\n\nWe own the gateway migration.\n")
    return v


@pytest.fixture
def vault_config(tmp_home, vault):
    config = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(config, "brain.path", str(vault))
    return config


# --- what a vault carries that must not be indexed --------------------------

@pytest.mark.parametrize("rel", [
    ".obsidian/app.json",
    ".obsidian/plugins/dataview/README.md",
    ".trash/deleted.md",
    ".git/objects/whatever.md",
    ".stversions/old.md",             # Syncthing
    ".smart-env/cache.md",            # a plugin cache
    "Attachments/diagram.excalidraw.md",
])
def test_tool_state_and_deleted_notes_are_ignored(rel):
    assert retrieval.is_ignored(rel, retrieval.DEFAULT_IGNORE_GLOBS) is True


@pytest.mark.parametrize("rel", [
    "Personal/Reading/consistent-hashing.md",
    "Daily Notes/2026-08-21.md",
    "work/quarterly-planning.md",
    "Templates/meeting.md",
    "note.md",
    "Some.Folder/note.md",            # a dot inside a name is not a dot-folder
])
def test_ordinary_notes_are_not_ignored(rel):
    assert retrieval.is_ignored(rel, retrieval.DEFAULT_IGNORE_GLOBS) is False


def test_a_deleted_note_does_not_stay_searchable(tmp_home, vault_config):
    """Obsidian deletes into `.trash/`. A note the user deleted showing up in
    search results is the worst of these, not merely noise."""
    retrieval.reindex(tmp_home, vault_config)

    assert retrieval.retrieve(tmp_home, vault_config, "salary forty percent", k=5) == []


def test_the_notes_apps_own_docs_do_not_pollute_results(tmp_home, vault_config):
    retrieval.reindex(tmp_home, vault_config)

    assert retrieval.retrieve(tmp_home, vault_config, "dataview TABLE FROM", k=5) == []


def test_a_drawing_does_not_pollute_results(tmp_home, vault_config):
    """An .excalidraw.md is a JSON blob in Markdown clothing."""
    retrieval.reindex(tmp_home, vault_config)

    assert retrieval.retrieve(tmp_home, vault_config, "elements seed 123456789", k=5) == []


def test_the_real_notes_are_searchable(tmp_home, vault_config):
    retrieval.reindex(tmp_home, vault_config)

    hits = retrieval.retrieve(tmp_home, vault_config, "consistent hashing", k=5)

    assert [h.path for h in hits][:1] == ["Personal/Reading/consistent-hashing.md"]


def test_a_folder_with_a_space_in_its_name_works(tmp_home, vault_config):
    """`Daily Notes/` is Obsidian's default, and spaces in paths are everywhere."""
    retrieval.reindex(tmp_home, vault_config)

    hits = retrieval.retrieve(tmp_home, vault_config, "platform team backpressure", k=5)

    assert [h.path for h in hits] == ["Daily Notes/2026-08-21.md"]


def test_obsidian_frontmatter_does_not_confuse_the_header_parser(tmp_home, vault_config):
    """Vault frontmatter holds tags and aliases, not px0's own keys."""
    header, body = brain.read_header(
        retrieval.brain_path(tmp_home, vault_config) / "Personal/Reading/consistent-hashing.md"
    )

    assert header["tags"] == ["distributed-systems"]
    assert "source" not in header          # px0 did not write this file
    assert "Consistent Hashing" in body


def test_wikilinks_and_tags_survive_into_the_index(tmp_home, vault_config):
    """px0 does not rewrite the vault, so its syntax comes along as text."""
    retrieval.reindex(tmp_home, vault_config)

    hit = retrieval.retrieve(tmp_home, vault_config, "consistent hashing", k=1)[0]

    assert "[[Sharding]]" in hit.text


def test_reading_a_vault_writes_nothing_into_it(tmp_home, vault_config, vault):
    """The whole proposition is that px0 reads a vault in place."""
    before = {p: p.stat().st_mtime_ns for p in sorted(vault.rglob("*")) if p.is_file()}

    retrieval.reindex(tmp_home, vault_config)
    retrieval.retrieve(tmp_home, vault_config, "consistent hashing", k=5)

    after = {p: p.stat().st_mtime_ns for p in sorted(vault.rglob("*")) if p.is_file()}
    assert before == after, "reindex/retrieve must not touch the vault"


# --- brain.ignore is configurable -------------------------------------------

def test_ignore_patterns_can_be_extended(tmp_home, vault_config):
    """A vault owner may not want their templates searched."""
    _write(retrieval.brain_path(tmp_home, vault_config), "Templates/meeting.md",
           "# {{title}}\n\nAttendees:\n")
    config_mod.set_key(vault_config, "brain.ignore", "*.excalidraw.md,Templates/*")
    retrieval.reindex(tmp_home, vault_config)

    assert retrieval.retrieve(tmp_home, vault_config, "attendees", k=5) == []


def test_a_comma_separated_string_is_read_as_a_list():
    """`px0 config set` takes one string; a hand-edited config.toml may too."""
    got = retrieval.ignore_globs({"brain": {"ignore": "*.a.md, *.b.md"}})

    assert got == ("*.a.md", "*.b.md")


def test_config_set_coerces_a_list_key(tmp_home):
    """Stored as a bare string, a multi-pattern value became one dead pattern."""
    config = config_mod.load(paths.config_path(tmp_home))

    value = config_mod.set_key(config, "brain.ignore", "*.x.md,*.y.md")

    assert value == ["*.x.md", "*.y.md"]


def test_an_empty_ignore_list_indexes_everything_but_dot_folders(tmp_home, vault_config):
    """Dot-folders are structural, not a pattern the user can switch off: they
    hold the notes app's state and its trash."""
    config_mod.set_key(vault_config, "brain.ignore", "")
    retrieval.reindex(tmp_home, vault_config)

    paths_found = {p.path for p in retrieval.retrieve(tmp_home, vault_config, "seed elements", k=10)}
    assert any("excalidraw" in p for p in paths_found)   # now indexed
    assert retrieval.retrieve(tmp_home, vault_config, "salary forty percent", k=5) == []


# --- the private-folder collision -------------------------------------------

def test_the_private_folder_holds_vault_work_notes_back_by_default(tmp_home, vault_config):
    """Documenting the trap, not endorsing it: `work/` is px0's private folder,
    and a vault that happens to have one loses it from every search."""
    retrieval.reindex(tmp_home, vault_config)

    assert retrieval.retrieve(tmp_home, vault_config, "quarterly planning gateway", k=5) == []


def test_the_private_folder_can_be_disabled(tmp_home, vault_config):
    config_mod.set_key(vault_config, "brain.private_folder", "")
    retrieval.reindex(tmp_home, vault_config)

    hits = retrieval.retrieve(tmp_home, vault_config, "quarterly planning gateway", k=5)

    assert [h.path for h in hits] == ["work/quarterly-planning.md"]


def test_the_private_folder_can_be_renamed(tmp_home, vault_config):
    config_mod.set_key(vault_config, "brain.private_folder", "px0-private")
    _write(retrieval.brain_path(tmp_home, vault_config), "px0-private/secret.md",
           "---\nsource: x\n---\n\nThe internal margin is forty percent.\n")
    retrieval.reindex(tmp_home, vault_config)

    # the vault's own work/ is now searchable...
    assert retrieval.retrieve(tmp_home, vault_config, "quarterly planning", k=5)
    # ...and the renamed folder is what gets held back
    assert retrieval.retrieve(tmp_home, vault_config, "internal margin", k=5) == []


@pytest.mark.parametrize("rel,folder,expected", [
    ("work/x.md", "work", True),
    ("work/nested/x.md", "work", True),
    ("Personal/work/x.md", "work", False),   # only at the top level
    ("workshop/x.md", "work", False),        # not a prefix match
    ("work.md", "work", False),
    ("work/x.md", "", False),                # disabled
    ("px0-private/x.md", "px0-private", True),
])
def test_is_private_matches_a_path_component_not_a_prefix(rel, folder, expected):
    """`startswith("work/")` was the old rule; `workshop/` is not private and a
    guarantee that hinges on string formatting is one refactor from lapsing."""
    assert retrieval.is_private(rel, folder) is expected


def test_a_renamed_private_folder_is_withheld_on_the_qmd_backend_too(tmp_home, monkeypatch):
    """Both backends read the rule from the same place."""
    canned = ('[{"file": "qmd://px0-brain/vault-private/s.md", "score": 0.9,'
              ' "snippet": "secret"},'
              ' {"file": "qmd://px0-brain/docs/p.md", "score": 0.5, "snippet": "public"}]')
    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run", lambda config, *a, **k: canned)

    got = retrieval.retrieve(
        tmp_home,
        {"retrieval": {"backend": "qmd"}, "brain": {"private_folder": "vault-private"}},
        "anything", k=5, local_only=True,
    )

    assert [p.path for p in got] == ["docs/p.md"]


# --- anchors ---------------------------------------------------------------

def test_a_heading_with_no_blank_line_after_it_does_not_produce_a_giant_anchor(
    tmp_home, vault_config
):
    """An Excalidraw file is a heading followed by one enormous line, so the
    whole blob was treated as the heading and slugified into the anchor."""
    config_mod.set_key(vault_config, "brain.ignore", "")
    retrieval.reindex(tmp_home, vault_config)

    for hit in retrieval.retrieve(tmp_home, vault_config, "seed elements", k=10):
        assert len(hit.anchor) <= retrieval.ANCHOR_MAX_LEN


# --- what the user is told --------------------------------------------------

def test_setting_the_path_reports_what_was_found(tmp_home, vault, monkeypatch, capsys):
    config = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(config, "brain.path", str(vault))
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, config))

    cli._report_brain_path(tmp_home, config)

    out = capsys.readouterr().out
    assert "Markdown file(s) found" in out
    assert "skipped as tool state" in out
    assert "Obsidian vault" in out


def test_setting_the_path_warns_about_the_private_folder_collision(
    tmp_home, vault, capsys
):
    """The one real trap, surfaced where it is actionable."""
    config = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(config, "brain.path", str(vault))

    cli._report_brain_path(tmp_home, config)

    out = capsys.readouterr().out
    assert "held back from every search" in out
    assert 'px0 config set brain.private_folder ""' in out


def test_a_path_that_does_not_exist_yet_says_so(tmp_home, tmp_path, capsys):
    config = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(config, "brain.path", str(tmp_path / "not-here"))

    cli._report_brain_path(tmp_home, config)

    assert "no such directory yet" in capsys.readouterr().out


def test_brain_list_marks_private_files_rather_than_hiding_them(
    tmp_home, vault_config, monkeypatch, capsys
):
    """Hidden, the file is a mystery; marked, the exclusion explains itself."""
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, vault_config))

    cli.cmd_brain_list(object())

    out = capsys.readouterr().out
    assert "work/quarterly-planning.md" in out and "(private)" in out


def test_brain_list_does_not_list_tool_state(tmp_home, vault_config, monkeypatch, capsys):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, vault_config))

    cli.cmd_brain_list(object())

    out = capsys.readouterr().out
    assert ".trash" not in out and ".obsidian" not in out
    assert "skipped as tool state" in out


# --- doctor ---------------------------------------------------------------

def test_doctor_counts_only_what_would_be_indexed(tmp_home, vault_config):
    """The raw .md count includes the app's state and its trash, so a vault of
    nothing but ignored files demanded a reindex that could not help."""
    retrieval.reindex(tmp_home, vault_config)

    res = doctor._check_index(tmp_home, vault_config)

    assert res["ok"] is True
    # 3 real notes: the reading note, the daily note, and the work note.
    assert res["detail"].startswith("3 brain files")


def test_doctor_reports_what_the_private_folder_holds_back(tmp_home, vault_config):
    res = doctor._check_private_folder(tmp_home, vault_config)

    assert res["ok"] is True
    assert "work/ holds 1 file(s) back" in res["detail"]


def test_doctor_says_nothing_alarming_when_there_is_no_private_folder(tmp_home, vault_config):
    config_mod.set_key(vault_config, "brain.private_folder", "")

    res = doctor._check_private_folder(tmp_home, vault_config)

    assert res["ok"] is True and "no private folder" in res["detail"]
