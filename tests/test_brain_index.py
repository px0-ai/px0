"""Indexing and querying the brain: every query shape, and every awkward file.

Two properties are load-bearing here. One bad file must not cost the whole
index -- `brain.path` can point at a hand-maintained notes vault, so the library
is not all px0's own output. And a query must reach the index in the language it
was typed in, not just in ASCII.
"""

import json

import pytest

from px0 import ask as ask_mod, brain, cli, config as config_mod, paths, retrieval


def _write(base, rel, text):
    path = base / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    return path


def _write_bytes(base, rel, raw):
    path = base / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(raw)
    return path


# --- the library root -------------------------------------------------------

def test_brain_path_honours_the_store_it_is_given(tmp_home):
    """The `home` argument used to be ignored, so a partial config read the
    default store instead of the one the caller passed.

    That is a correctness bug on its own, and it is also how the test suite
    ended up writing fixtures into the developer's real `~/.px0`.
    """
    assert retrieval.brain_path(tmp_home, {}) == tmp_home / "brain"


def test_an_explicit_config_path_still_wins(tmp_home, tmp_path):
    """A notes vault kept outside the store is the documented reason for the key."""
    vault = tmp_path / "my-vault"
    config = {"brain": {"path": str(vault)}}

    assert retrieval.brain_path(tmp_home, config) == vault


def test_a_tilde_in_the_configured_path_is_expanded(tmp_home):
    got = retrieval.brain_path(tmp_home, {"brain": {"path": "~/somewhere"}})

    assert "~" not in str(got) and got.is_absolute()


# --- reindex survives whatever is in the folder -----------------------------

@pytest.fixture
def awkward_library(tmp_home, brain_config):
    """A library holding every file shape that used to break the walk."""
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/good.md",
           "---\nsource: local\nretrieved: 2026-08-20\nkind: doc\n---\n\n"
           "Sharding splits data across nodes.\n")
    # A hand-written note using --- as a horizontal rule, twice, so the split
    # yields a bare scalar where a mapping was expected.
    _write(base, "docs/hrule.md",
           "---\n\nFirst section about caching.\n\n---\n\nSecond section.\n")
    _write(base, "docs/badyaml.md",
           "---\nthis: [is: not: valid: yaml\n---\n\nMalformed frontmatter, real body.\n")
    _write(base, "docs/listfront.md", "---\n- a\n- b\n---\n\nList frontmatter, real body.\n")
    _write(base, "docs/empty.md", "")
    _write(base, "docs/onlyfront.md", "---\nsource: x\n---\n")
    _write_bytes(base, "docs/latin1.md", "Café latency notes\n".encode("latin-1"))
    _write_bytes(base, "docs/binary.md", b"\xff\xfe\x00\x01garbage\x80 replicas\n")
    _write(base, "docs/unicode.md",
           "---\nsource: x\n---\n\nशार्दिंग means sharding. Café latency. レプリカ works.\n")
    return base


@pytest.mark.parametrize("name", [
    "hrule.md", "badyaml.md", "listfront.md", "empty.md", "onlyfront.md",
    "latin1.md", "binary.md",
])
def test_read_header_never_raises_on_an_awkward_file(awkward_library, name):
    header, body = brain.read_header(awkward_library / "docs" / name)

    assert isinstance(header, dict), "callers all call .get() on this"
    assert isinstance(body, str)


@pytest.mark.parametrize("name,expected", [
    ("hrule.md", "First section about caching."),
    ("badyaml.md", "Malformed frontmatter, real body."),
    ("listfront.md", "List frontmatter, real body."),
])
def test_an_unparseable_header_still_yields_the_body(awkward_library, name, expected):
    """Treating the file as headerless keeps its text searchable.

    Dropping the body instead would lose the note silently, which is worse than
    having no metadata for it.
    """
    _, body = brain.read_header(awkward_library / "docs" / name)

    assert expected in body


def test_one_broken_file_does_not_cost_the_whole_index(awkward_library, tmp_home, brain_config):
    """A single malformed file used to abort reindex, leaving nothing searchable."""
    count = retrieval.reindex(tmp_home, brain_config)

    assert count > 0
    assert retrieval.retrieve(tmp_home, brain_config, "sharding", k=5)


def test_a_file_in_another_encoding_is_still_indexed(awkward_library, tmp_home, brain_config):
    retrieval.reindex(tmp_home, brain_config)

    hits = retrieval.retrieve(tmp_home, brain_config, "latency", k=10)

    assert any("latin1.md" in p.path for p in hits)


def test_an_empty_library_indexes_to_nothing_without_complaint(tmp_home, brain_config):
    assert retrieval.reindex(tmp_home, brain_config) == 0
    assert retrieval.retrieve(tmp_home, brain_config, "anything") == []


def test_reindex_replaces_rather_than_accumulates(tmp_home, brain_config):
    """Reindexing twice must not double every passage."""
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/one.md", "---\nsource: x\n---\n\nOnly one passage here.\n")

    first = retrieval.reindex(tmp_home, brain_config)
    second = retrieval.reindex(tmp_home, brain_config)

    assert first == second == retrieval.index_count(tmp_home)


def test_a_deleted_file_leaves_the_index_on_the_next_reindex(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    doomed = _write(base, "docs/temp.md", "---\nsource: x\n---\n\nEphemeral passage.\n")
    retrieval.reindex(tmp_home, brain_config)
    assert retrieval.retrieve(tmp_home, brain_config, "ephemeral")

    doomed.unlink()
    retrieval.reindex(tmp_home, brain_config)

    assert retrieval.retrieve(tmp_home, brain_config, "ephemeral") == []


def test_an_unreadable_file_is_skipped_not_fatal(tmp_home, brain_config, monkeypatch):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/fine.md", "---\nsource: x\n---\n\nReadable passage about quorums.\n")
    _write(base, "docs/locked.md", "---\nsource: x\n---\n\nUnreadable.\n")

    real_read = brain.read_text_lossy

    def _explode(path):
        if path.name == "locked.md":
            raise PermissionError("nope")
        return real_read(path)

    monkeypatch.setattr(brain, "read_text_lossy", _explode)

    assert retrieval.reindex(tmp_home, brain_config) > 0
    assert retrieval.retrieve(tmp_home, brain_config, "quorums")


# --- chunking ---------------------------------------------------------------

def test_headings_become_anchors(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/anchored.md",
           "---\nsource: x\n---\n\n## Write Amplification\n\n"
           "Every update rewrites a whole page.\n")
    retrieval.reindex(tmp_home, brain_config)

    hits = retrieval.retrieve(tmp_home, brain_config, "amplification", k=5)

    assert hits and hits[0].anchor == "write-amplification"


def test_a_heading_starts_a_new_chunk(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/two.md",
           "---\nsource: x\n---\n\n## Alpha\n\nAlpha body.\n\n## Beta\n\nBeta body.\n")
    retrieval.reindex(tmp_home, brain_config)

    anchors = {p.anchor for p in retrieval.retrieve(tmp_home, brain_config, "body", k=10)}

    assert {"alpha", "beta"} <= anchors


def test_provenance_travels_with_the_passage(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/dated.md",
           "---\nsource: x\nretrieved: 2026-08-20\nkind: stub\n---\n\nStub body text.\n")
    retrieval.reindex(tmp_home, brain_config)

    hit = retrieval.retrieve(tmp_home, brain_config, "stub", k=1)[0]

    assert hit.ingested_at == "2026-08-20"
    assert hit.is_stub is True


# --- query shapes -----------------------------------------------------------

@pytest.fixture
def indexed(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/en.md",
           "---\nsource: x\n---\n\nWrite-through caching keeps both stores in sync.\n")
    _write(base, "docs/hi.md", "---\nsource: x\n---\n\nशार्दिंग वितरण के बारे में है।\n")
    _write(base, "docs/ja.md", "---\nsource: x\n---\n\nレプリカ は 過半数 が 必要 です。\n")
    _write(base, "docs/fr.md", "---\nsource: x\n---\n\nCafé naïve résumé latency.\n")
    retrieval.reindex(tmp_home, brain_config)
    return base


@pytest.mark.parametrize("query", [
    "caching",
    "write-through caching",
    "CACHING",
    "Caching",
    "???",
    "",
    "   ",
    '"quoted phrase"',
    "caching OR NOT AND NEAR",
    "'; DROP TABLE passages; --",
    "caching " * 300,
    "shard*",
    "source:local",
    "(unbalanced",
    "^caret",
    "emoji 🧠 query",
    "\\backslash",
    "tab\tseparated",
    "new\nline",
])
def test_no_query_shape_raises(indexed, tmp_home, brain_config, query):
    """A malformed query must come back empty, never as a traceback.

    Every one of these reaches SQLite's FTS5 MATCH, where a stray quote or
    operator would otherwise be a syntax error surfacing as a crash.
    """
    assert isinstance(retrieval.retrieve(tmp_home, brain_config, query, k=5), list)


@pytest.mark.parametrize("query,expected_file", [
    ("caching", "docs/en.md"),
    ("शार्दिंग", "docs/hi.md"),
    ("レプリカ", "docs/ja.md"),
    ("過半数", "docs/ja.md"),
    ("café", "docs/fr.md"),
])
def test_a_query_finds_its_document_in_its_own_script(
    indexed, tmp_home, brain_config, query, expected_file
):
    """Non-ASCII queries matched nothing at all.

    FTS5's unicode61 tokenizer indexes these scripts perfectly well -- the
    tokens were being thrown away by an ASCII-only regex before the index was
    ever consulted.
    """
    hits = retrieval.retrieve(tmp_home, brain_config, query, k=5)

    assert [p.path for p in hits] and hits[0].path == expected_file


@pytest.mark.parametrize("query", ["cafe", "café", "CAFÉ", "naive", "naïve"])
def test_diacritics_fold_in_both_directions(indexed, tmp_home, brain_config, query):
    """Typing an accent, or omitting one, must find the same document."""
    assert retrieval.retrieve(tmp_home, brain_config, query, k=5)


def test_any_query_word_is_enough_to_match(indexed, tmp_home, brain_config):
    """Tokens are OR-ed, so a query with one unknown word still finds the rest."""
    assert retrieval.retrieve(tmp_home, brain_config, "caching zzzzunknown", k=5)


def test_k_caps_the_result_count(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    for i in range(10):
        _write(base, f"docs/n{i}.md", f"---\nsource: x\n---\n\nreplication note {i}\n")
    retrieval.reindex(tmp_home, brain_config)

    assert len(retrieval.retrieve(tmp_home, brain_config, "replication", k=3)) == 3


def test_a_higher_score_means_a_better_match(indexed, tmp_home, brain_config):
    """bm25() is lower-is-better, so it is negated on the way out; results must
    still arrive best-first."""
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/strong.md",
           "---\nsource: x\n---\n\nquorum quorum quorum quorum quorum\n")
    _write(base, "docs/weak.md", "---\nsource: x\n---\n\nquorum mentioned once here\n")
    retrieval.reindex(tmp_home, brain_config)

    hits = retrieval.retrieve(tmp_home, brain_config, "quorum", k=5)

    assert [h.score for h in hits] == sorted((h.score for h in hits), reverse=True)


def test_fts_query_keeps_whole_words_whatever_the_script():
    """A regex tokenizer could not agree with the index tokenizer.

    ASCII-only patterns dropped non-Latin words entirely, and even a
    Unicode-aware `\\w+` drops the combining marks an abugida word is built
    from. Splitting on whitespace and letting FTS5 tokenize inside each quoted
    string keeps both sides in step.
    """
    assert retrieval._fts_query("café शार्दिंग") == '"café" OR "शार्दिंग"'
    assert retrieval._fts_query("") == '""'
    assert retrieval._fts_query("   ") == '""'


def test_fts_query_escapes_a_quote_instead_of_letting_it_break_the_syntax():
    # FTS5 escapes a double quote inside a string by doubling it.
    assert retrieval._fts_query('a"b') == '"a""b"'
    assert retrieval._fts_query('OR NOT') == '"OR" OR "NOT"'


# --- work/ stays local ------------------------------------------------------

def test_work_passages_are_excluded_by_default(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "work/internal.md", "---\nsource: x\n---\n\nInternal pricing model.\n")
    _write(base, "docs/public.md", "---\nsource: x\n---\n\nPublic pricing note.\n")
    retrieval.reindex(tmp_home, brain_config)

    default = retrieval.retrieve(tmp_home, brain_config, "pricing", k=10)

    assert default and all(not p.path.startswith("work/") for p in default)


def test_work_passages_are_reachable_when_asked_for(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "work/internal.md", "---\nsource: x\n---\n\nInternal pricing model.\n")
    retrieval.reindex(tmp_home, brain_config)

    assert retrieval.retrieve(tmp_home, brain_config, "pricing", k=10, local_only=False)


def test_the_work_exclusion_does_not_eat_into_k(tmp_home, brain_config):
    """Filtering after the SQL LIMIT silently degraded recall as well as
    excluding work/: a query whose top matches all live there returned short."""
    base = retrieval.brain_path(tmp_home, brain_config)
    for i in range(5):
        _write(base, f"work/secret{i}.md",
               "---\nsource: x\n---\n\npooling pooling pooling\n")
    for i in range(3):
        _write(base, f"docs/public{i}.md", "---\nsource: x\n---\n\npooling\n")
    retrieval.reindex(tmp_home, brain_config)

    got = retrieval.retrieve(tmp_home, brain_config, "pooling", k=3, local_only=True)

    assert len(got) == 3


# --- ask --------------------------------------------------------------------

def test_ask_refuses_before_the_index_exists(tmp_home, brain_config):
    with pytest.raises(ask_mod.AskError, match="reindex"):
        ask_mod.ask(tmp_home, brain_config, "what about caching?")


def test_ask_says_when_nothing_matched(indexed, tmp_home, brain_config):
    with pytest.raises(ask_mod.AskError, match="no passages matched"):
        ask_mod.ask(tmp_home, brain_config, "zzzznothinglikethis")


def test_ask_grounds_the_prompt_in_the_retrieved_passages(
    indexed, tmp_home, brain_config, monkeypatch
):
    """The whole contract of ask is answering from the user's own material."""
    seen = {}

    def _fake_invoke(config, prompt, timeout=120):
        seen["prompt"] = prompt
        return "Both stores stay in sync. [docs/en.md#]"

    monkeypatch.setattr("px0.ask.harness.invoke", _fake_invoke)
    monkeypatch.setattr("px0.runs.write_record", lambda *a, **k: None)

    result = ask_mod.ask(tmp_home, brain_config, "what does write-through do?")

    assert "Write-through caching" in seen["prompt"]
    assert "ONLY the passages below" in seen["prompt"]
    assert result["passages"] and result["run_id"].startswith("ask")


def test_ask_never_reaches_into_work(tmp_home, brain_config, monkeypatch):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "work/secret.md", "---\nsource: x\n---\n\nThe internal margin is 40%.\n")
    retrieval.reindex(tmp_home, brain_config)

    monkeypatch.setattr("px0.ask.harness.invoke", lambda *a, **k: "answer")
    monkeypatch.setattr("px0.runs.write_record", lambda *a, **k: None)

    with pytest.raises(ask_mod.AskError):
        ask_mod.ask(tmp_home, brain_config, "what is the internal margin?")


# --- CLI surface ------------------------------------------------------------

def test_search_json_emits_objects_not_reprs(
    indexed, tmp_home, brain_config, monkeypatch, capsys, quiet_spinner
):
    """`--json` was a list of `Passage(...)` repr strings, unusable in a script."""
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))

    class _Args:
        query, k, json = "caching", None, True

    cli.cmd_search(_Args())

    payload = json.loads(capsys.readouterr().out)
    assert payload and all(isinstance(row, dict) for row in payload)
    assert set(payload[0]) == {
        "path", "anchor", "text", "score", "ingested_at", "is_stub", "kind",
    }


def test_search_k_falls_back_to_the_configured_default(
    indexed, tmp_home, brain_config, monkeypatch, quiet_spinner
):
    """`retrieval.k_default` is documented as the default per query, but an
    argparse default of 5 meant the key was never consulted."""
    config_mod.set_key(brain_config, "retrieval.k_default", 2)
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))
    seen = {}

    def _spy(home, config, query, k=5, local_only=True, kind=None):
        seen["k"] = k
        return []

    monkeypatch.setattr(retrieval, "retrieve", _spy)

    class _Args:
        query, k, json = "caching", None, False

    cli.cmd_search(_Args())

    assert seen["k"] == 2


def test_an_explicit_k_still_wins_over_the_config(
    indexed, tmp_home, brain_config, monkeypatch, quiet_spinner
):
    config_mod.set_key(brain_config, "retrieval.k_default", 2)
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))
    seen = {}
    monkeypatch.setattr(
        retrieval, "retrieve",
        lambda home, config, query, k=5, local_only=True, kind=None: (
            seen.setdefault("k", k), [])[1],
    )

    class _Args:
        query, k, json = "caching", 7, False

    cli.cmd_search(_Args())

    assert seen["k"] == 7


def test_brain_refresh_takes_no_propose():
    """Refreshing fired a model call every time with no way to decline."""
    args = cli.build_parser().parse_args(["brain", "refresh", "blogs/x.md", "--no-propose"])

    assert args.no_propose is True


def test_refresh_honours_no_propose(tmp_home, brain_config, tmp_path, monkeypatch):
    src = tmp_path / "note.md"
    src.write_text("# Note\n\nbody\n")
    result = brain.add(tmp_home, brain_config, str(src), no_propose=True)

    called = []
    monkeypatch.setattr(
        "px0.proposals.propose_from_brain", lambda *a, **k: called.append(a)
    )

    brain.refresh(tmp_home, brain_config, result.path, no_propose=True)

    assert called == []


def test_brain_add_offers_work_as_a_destination():
    """Leaving it out of --to made the one private folder the unreachable one."""
    args = cli.build_parser().parse_args(["brain", "add", "x.md", "--to", "work"])

    assert args.to == "work"


def test_reindex_reports_what_it_indexed(indexed, tmp_home, brain_config, monkeypatch, capsys, quiet_spinner):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))

    cli.cmd_reindex(object())

    assert "passages" in capsys.readouterr().out


def test_search_with_no_matches_points_at_reindex(
    indexed, tmp_home, brain_config, monkeypatch, capsys, quiet_spinner
):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))

    class _Args:
        query, k, json = "zzzznothing", None, False

    cli.cmd_search(_Args())

    assert "px0 brain reindex" in capsys.readouterr().out


def test_brain_list_prints_paths_relative_to_the_library(
    tmp_home, brain_config, monkeypatch, capsys
):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "blogs/a-post.md", "---\nsource: x\n---\n\nbody\n")
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))

    cli.cmd_brain_list(object())

    out = capsys.readouterr().out
    assert "blogs/a-post.md" in out and str(base) not in out


def test_an_empty_brain_says_how_to_fill_it(tmp_home, brain_config, monkeypatch, capsys):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))

    cli.cmd_brain_list(object())

    assert "px0 brain add" in capsys.readouterr().out


def test_an_index_from_an_older_tokenizer_is_rebuilt_not_reused(tmp_home, brain_config):
    """A virtual table's tokenizer is fixed at creation.

    An index built before the Mn/Mc fix would keep answering queries with the
    old segmentation forever, so a drifted table is dropped and recreated. The
    index is derived data, and `px0 doctor` already points an empty one at
    `px0 brain reindex`.
    """
    import sqlite3

    paths.index_dir(tmp_home).mkdir(parents=True, exist_ok=True)
    stale = sqlite3.connect(retrieval.index_db_path(tmp_home))
    stale.execute(
        "CREATE VIRTUAL TABLE passages USING fts5("
        "text, path UNINDEXED, anchor UNINDEXED, ingested_at UNINDEXED, "
        "is_stub UNINDEXED, is_work UNINDEXED)"
    )
    stale.execute(
        "INSERT INTO passages (text, path, anchor, ingested_at, is_stub, is_work) "
        "VALUES ('old row', 'docs/old.md', '', '2026-01-01', 0, 0)"
    )
    stale.commit()
    stale.close()

    conn = retrieval._connect(tmp_home)
    try:
        ddl = conn.execute(
            "SELECT sql FROM sqlite_master WHERE name = 'passages'"
        ).fetchone()[0]
        rows = conn.execute("SELECT COUNT(*) FROM passages").fetchone()[0]
    finally:
        conn.close()

    assert retrieval._FTS_TOKENIZE in ddl
    assert rows == 0, "the stale index is dropped, ready for a reindex"


def test_a_current_index_is_left_alone(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/keep.md", "---\nsource: x\n---\n\nA passage worth keeping.\n")
    retrieval.reindex(tmp_home, brain_config)
    before = retrieval.index_count(tmp_home)

    retrieval._connect(tmp_home).close()

    assert retrieval.index_count(tmp_home) == before


@pytest.mark.parametrize("frontmatter,expected", [
    ("retrieved: 2026-08-21", "2026-08-21"),       # unquoted -> a datetime.date
    ("retrieved: '2026-08-21'", "2026-08-21"),     # quoted -> already a str
    ("retrieved: 2026-08-21 10:30:00", "2026-08-21 10:30:00"),
])
def test_a_date_in_frontmatter_arrives_as_text(tmp_home, brain_config, frontmatter, expected):
    """YAML turns an unquoted date into a `datetime.date`.

    `Passage.ingested_at` promises a string, and the local backend only survived
    the object via sqlite3's implicit date adapter -- deprecated in 3.12 and
    slated for removal, while this package supports 3.11 and up.
    """
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/dated.md", f"---\nsource: x\n{frontmatter}\n---\n\nA dated note.\n")
    retrieval.reindex(tmp_home, brain_config)

    hit = retrieval.retrieve(tmp_home, brain_config, "dated note", k=1)[0]

    assert isinstance(hit.ingested_at, str)
    assert hit.ingested_at.startswith(expected[:10])


def test_a_missing_date_stays_none(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/undated.md", "---\nsource: x\n---\n\nAn undated note.\n")
    retrieval.reindex(tmp_home, brain_config)

    assert retrieval.retrieve(tmp_home, brain_config, "undated", k=1)[0].ingested_at is None


# --- --kind ---------------------------------------------------------------

@pytest.fixture
def mixed_kinds(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    for name, kind in [("p", "paper"), ("b", "blog"), ("d", "doc"), ("v", "video")]:
        _write(base, f"docs/{name}.md",
               f"---\nsource: x\nretrieved: '2026-08-21'\nkind: {kind}\n---\n\n"
               f"consensus protocols discussed here\n")
    # a note px0 did not write: no frontmatter, so no kind
    _write(base, "docs/vault-note.md", "# Mine\n\nconsensus protocols discussed here\n")
    retrieval.reindex(tmp_home, brain_config)
    return base


@pytest.mark.parametrize("kind,expected", [
    ("paper", ["docs/p.md"]),
    ("blog", ["docs/b.md"]),
    ("doc", ["docs/d.md"]),
    ("video", ["docs/v.md"]),
])
def test_kind_restricts_results_to_that_kind(tmp_home, brain_config, mixed_kinds, kind, expected):
    """The folders were never queryable and neither was the frontmatter, so
    there was no way to ask for only papers. Now there is."""
    got = retrieval.retrieve(tmp_home, brain_config, "consensus protocols", k=10, kind=kind)

    assert [p.path for p in got] == expected


def test_no_kind_filter_returns_everything(tmp_home, brain_config, mixed_kinds):
    got = retrieval.retrieve(tmp_home, brain_config, "consensus protocols", k=10)

    assert len(got) == 5


def test_a_file_px0_did_not_write_has_no_kind(tmp_home, brain_config, mixed_kinds):
    """A vault note carries no px0 frontmatter, so nothing to match on."""
    got = retrieval.retrieve(tmp_home, brain_config, "consensus protocols", k=10)

    by_path = {p.path: p.kind for p in got}
    assert by_path["docs/vault-note.md"] is None
    assert by_path["docs/p.md"] == "paper"


def test_the_kind_filter_does_not_eat_into_k(tmp_home, brain_config):
    """Same trap as the work/ exclusion: filtering after the SQL LIMIT means a
    query whose top hits are all the wrong kind comes back short."""
    base = retrieval.brain_path(tmp_home, brain_config)
    for i in range(10):
        _write(base, f"docs/blog{i}.md",
               "---\nsource: x\nkind: blog\n---\n\npooling pooling pooling\n")
    for i in range(3):
        _write(base, f"docs/paper{i}.md", "---\nsource: x\nkind: paper\n---\n\npooling\n")
    retrieval.reindex(tmp_home, brain_config)

    got = retrieval.retrieve(tmp_home, brain_config, "pooling", k=3, kind="paper")

    assert len(got) == 3
    assert all(p.kind == "paper" for p in got)


def test_the_kind_filter_still_withholds_private_passages(tmp_home, brain_config):
    """A filter must never widen what retrieval will hand back."""
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "work/secret.md", "---\nsource: x\nkind: paper\n---\n\nInternal margin.\n")
    retrieval.reindex(tmp_home, brain_config)

    assert retrieval.retrieve(tmp_home, brain_config, "internal margin",
                              k=5, kind="paper") == []


def test_kind_is_filterable_on_the_qmd_backend(tmp_home, brain_config, monkeypatch):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/p.md", "---\nsource: x\nkind: paper\n---\n\nA paper.\n")
    _write(base, "docs/b.md", "---\nsource: x\nkind: blog\n---\n\nA blog.\n")

    canned = ('[{"file": "qmd://px0-brain/docs/b.md", "score": 0.9, "snippet": "A blog."},'
              ' {"file": "qmd://px0-brain/docs/p.md", "score": 0.5, "snippet": "A paper."}]')
    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run", lambda config, *a, **k: canned)

    config = dict(brain_config)
    config["retrieval"] = {"backend": "qmd"}
    got = retrieval.retrieve(tmp_home, config, "anything", k=5, kind="paper")

    assert [p.path for p in got] == ["docs/p.md"]


def test_the_index_is_rebuilt_when_a_column_is_added(tmp_home, brain_config):
    """The drift check compares the whole DDL, not just the tokenizer: an index
    missing the kind column would fail every query naming it."""
    import sqlite3

    paths.index_dir(tmp_home).mkdir(parents=True, exist_ok=True)
    stale = sqlite3.connect(retrieval.index_db_path(tmp_home))
    stale.execute(
        "CREATE VIRTUAL TABLE passages USING fts5("
        "text, path UNINDEXED, anchor UNINDEXED, ingested_at UNINDEXED, "
        f"is_stub UNINDEXED, is_work UNINDEXED, tokenize=\"{retrieval._FTS_TOKENIZE}\")"
    )
    stale.commit()
    stale.close()

    _write(retrieval.brain_path(tmp_home, brain_config), "docs/n.md",
           "---\nsource: x\nkind: paper\n---\n\nA paper about pooling.\n")
    retrieval.reindex(tmp_home, brain_config)

    assert retrieval.retrieve(tmp_home, brain_config, "pooling", k=1, kind="paper")


def test_ask_can_be_restricted_to_one_kind(tmp_home, brain_config, mixed_kinds, monkeypatch):
    from px0 import ask as ask_mod

    seen = {}
    monkeypatch.setattr("px0.ask.harness.invoke",
                        lambda config, prompt, timeout=120: seen.setdefault("prompt", prompt) or "answer")
    monkeypatch.setattr("px0.runs.write_record", lambda *a, **k: None)

    result = ask_mod.ask(tmp_home, brain_config, "what about consensus?", kind="paper")

    assert [p.path for p in result["passages"]] == ["docs/p.md"]


def test_ask_says_which_kind_found_nothing(tmp_home, brain_config, mixed_kinds):
    from px0 import ask as ask_mod

    with pytest.raises(ask_mod.AskError, match="of kind 'video'"):
        ask_mod.ask(tmp_home, brain_config, "zzzznothinglikethis", kind="video")


def test_search_explains_an_empty_kind_result(
    tmp_home, brain_config, mixed_kinds, monkeypatch, capsys, quiet_spinner
):
    """Otherwise a --kind that matches nothing looks like an empty brain."""
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))

    class _Args:
        query, k, json, kind = "zzzznothing", None, False, "video"

    cli.cmd_search(_Args())

    assert "carry no kind" in capsys.readouterr().out


@pytest.mark.parametrize("kind", list(retrieval.KINDS))
def test_every_advertised_kind_is_accepted_by_the_cli(kind):
    args = cli.build_parser().parse_args(["brain", "search", "q", "--kind", kind])

    assert args.kind == kind


def test_an_unknown_kind_is_rejected_by_the_cli():
    with pytest.raises(SystemExit):
        cli.build_parser().parse_args(["brain", "search", "q", "--kind", "nonsense"])
