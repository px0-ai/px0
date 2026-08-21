"""Brain path resolution, awkward-file header parsing, and the CLI surface
around search/reindex/list -- all backend-agnostic. Real indexing and query
behavior now lives entirely inside the external qmd CLI (see
test_retrieval_qmd.py for the mocked-qmd coverage of that).

One property is load-bearing here: one bad file must not cost `read_header`
its ability to keep going -- `brain.path` can point at a hand-maintained
notes vault, so the library is not all px0's own output.
"""

import json

import pytest

from px0 import ask as ask_mod, brain, cli, config as config_mod, retrieval


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


# --- read_header survives whatever is in the folder -------------------------

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


# --- ask ----------------------------------------------------------------

def test_ask_says_when_nothing_matched(tmp_home, brain_config, monkeypatch):
    monkeypatch.setattr(retrieval, "retrieve", lambda *a, **k: [])

    with pytest.raises(ask_mod.AskError, match="no passages matched"):
        ask_mod.ask(tmp_home, brain_config, "zzzznothinglikethis")


def test_ask_grounds_the_prompt_in_the_retrieved_passages(tmp_home, brain_config, monkeypatch):
    """The whole contract of ask is answering from the user's own material."""
    passage = retrieval.Passage(
        path="docs/en.md", anchor="", text="Write-through caching keeps both stores in sync.",
        score=1.0, ingested_at=None, is_stub=False,
    )
    monkeypatch.setattr(retrieval, "retrieve", lambda *a, **k: [passage])

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


def test_ask_says_which_kind_found_nothing(tmp_home, brain_config, monkeypatch):
    monkeypatch.setattr(retrieval, "retrieve", lambda *a, **k: [])

    with pytest.raises(ask_mod.AskError, match="of kind 'video'"):
        ask_mod.ask(tmp_home, brain_config, "zzzznothinglikethis", kind="video")


# --- CLI surface ------------------------------------------------------------

def test_search_json_emits_objects_not_reprs(
    tmp_home, brain_config, monkeypatch, capsys, quiet_spinner
):
    """`--json` was a list of `Passage(...)` repr strings, unusable in a script."""
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))
    passage = retrieval.Passage(
        path="docs/en.md", anchor="sec", text="caching stuff", score=1.0,
        ingested_at=None, is_stub=False, kind=None,
    )
    monkeypatch.setattr(retrieval, "retrieve", lambda *a, **k: [passage])

    class _Args:
        query, k, json = "caching", None, True

    cli.cmd_search(_Args())

    payload = json.loads(capsys.readouterr().out)
    assert payload and all(isinstance(row, dict) for row in payload)
    assert set(payload[0]) == {
        "path", "anchor", "text", "score", "ingested_at", "is_stub", "kind",
    }


def test_search_k_falls_back_to_the_configured_default(
    tmp_home, brain_config, monkeypatch, quiet_spinner
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
    tmp_home, brain_config, monkeypatch, quiet_spinner
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


def test_brain_add_offers_work_as_a_destination():
    """Leaving it out of --to made the one private folder the unreachable one."""
    args = cli.build_parser().parse_args(["brain", "add", "x.md", "--to", "work"])

    assert args.to == "work"


def test_reindex_reports_what_it_indexed(tmp_home, brain_config, monkeypatch, capsys, quiet_spinner):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))
    monkeypatch.setattr(retrieval, "reindex", lambda *a, **k: 3)

    cli.cmd_reindex(object())

    assert "passages" in capsys.readouterr().out


def test_search_with_no_matches_points_at_reindex(
    tmp_home, brain_config, monkeypatch, capsys, quiet_spinner
):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))
    monkeypatch.setattr(retrieval, "retrieve", lambda *a, **k: [])

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


def test_search_explains_an_empty_kind_result(
    tmp_home, brain_config, monkeypatch, capsys, quiet_spinner
):
    """Otherwise a --kind that matches nothing looks like an empty brain."""
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, brain_config))
    monkeypatch.setattr(retrieval, "retrieve", lambda *a, **k: [])

    class _Args:
        query, k, json, kind = "zzzznothing", None, False, "video"

    cli.cmd_search(_Args())

    assert "carry no kind" in capsys.readouterr().out


# --- --kind -----------------------------------------------------------------

def test_kind_is_filterable_on_the_qmd_backend(tmp_home, brain_config, monkeypatch):
    base = retrieval.brain_path(tmp_home, brain_config)
    _write(base, "docs/p.md", "---\nsource: x\nkind: paper\n---\n\nA paper.\n")
    _write(base, "docs/b.md", "---\nsource: x\nkind: blog\n---\n\nA blog.\n")

    canned = ('[{"file": "qmd://px0-brain/docs/b.md", "score": 0.9, "snippet": "A blog."},'
              ' {"file": "qmd://px0-brain/docs/p.md", "score": 0.5, "snippet": "A paper."}]')
    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run", lambda config, *a, **k: canned)

    got = retrieval.retrieve(tmp_home, brain_config, "anything", k=5, kind="paper")

    assert [p.path for p in got] == ["docs/p.md"]


@pytest.mark.parametrize("kind", list(retrieval.KINDS))
def test_every_advertised_kind_is_accepted_by_the_cli(kind):
    args = cli.build_parser().parse_args(["brain", "search", "q", "--kind", kind])

    assert args.kind == kind


def test_an_unknown_kind_is_rejected_by_the_cli():
    with pytest.raises(SystemExit):
        cli.build_parser().parse_args(["brain", "search", "q", "--kind", "nonsense"])
