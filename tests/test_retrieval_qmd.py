import pytest
from px0 import retrieval, doctor, paths

def test_qmd_ensure_collection_skips_when_exists(tmp_home, monkeypatch):
    called = []
    def mock_run(config, *args, **kwargs):
        called.append(args)
        if args == ("collection", "list"):
            return f"{retrieval.QMD_COLLECTION}\nother-collection"
        return ""
    monkeypatch.setattr(retrieval, "_qmd_run", mock_run)

    retrieval._qmd_ensure_collection(tmp_home, {})
    assert called == [("collection", "list")] # Only lists, doesn't add


def test_qmd_ensure_collection_adds_when_absent(tmp_home, monkeypatch):
    called = []
    def mock_run(config, *args, **kwargs):
        called.append(args)
        if args == ("collection", "list"):
            return "empty"
        return ""
    monkeypatch.setattr(retrieval, "_qmd_run", mock_run)

    retrieval._qmd_ensure_collection(tmp_home, {})
    assert len(called) == 2
    assert called[0] == ("collection", "list")
    assert called[1][0:3] == ("collection", "add", str(retrieval.brain_path(tmp_home, {})))


def test_qmd_ensure_embed_consent_prompts_and_persists(tmp_home, monkeypatch):
    consent_file = paths.retrieval_consent_path(tmp_home)
    assert not consent_file.exists()

    # Consented happy path
    monkeypatch.setattr("builtins.input", lambda *a: "y")
    assert retrieval._qmd_ensure_embed_consent(tmp_home, {}) is True
    assert consent_file.exists()

    # Re-running shouldn't prompt again (it will return True immediately without calling input)
    monkeypatch.setattr("builtins.input", lambda *a: exec('raise RuntimeError("Should not be called")'))
    assert retrieval._qmd_ensure_embed_consent(tmp_home, {}) is True


def test_qmd_ensure_embed_consent_declined(tmp_home, monkeypatch):
    consent_file = paths.retrieval_consent_path(tmp_home)
    if consent_file.exists():
        consent_file.unlink()

    monkeypatch.setattr("builtins.input", lambda *a: "n")
    assert retrieval._qmd_ensure_embed_consent(tmp_home, {}) is False
    assert not consent_file.exists()


def test_parse_qmd_result(tmp_home):
    canned_json = """
    [
      {
        "file": "blogs/test.md",
        "score": 0.95,
        "snippet": "Test content snippet",
        "anchor": "section-1"
      }
    ]
    """
    res = retrieval._parse_qmd_result(tmp_home, {}, canned_json)
    assert len(res) == 1
    assert res[0].path == "blogs/test.md"
    assert res[0].score == 0.95
    assert res[0].text == "Test content snippet"
    assert res[0].anchor == "section-1"


def test_local_only_drops_work_prefixed_passages(tmp_home, monkeypatch):
    canned_passages = [
        retrieval.Passage(path="work/doc.md", anchor="a", text="work", score=0.9, ingested_at=None, is_stub=False),
        retrieval.Passage(path="blogs/doc.md", anchor="a", text="blogs", score=0.8, ingested_at=None, is_stub=False),
    ]
    monkeypatch.setattr(retrieval, "_qmd_retrieve", lambda *a, **k: canned_passages)
    res_qmd = retrieval.retrieve(tmp_home, {}, "text", local_only=True)
    assert len(res_qmd) == 1
    assert res_qmd[0].path == "blogs/doc.md"


def test_missing_qmd_binary_raises_retrieval_backend_error(tmp_home):
    with pytest.raises(retrieval.RetrievalBackendError, match="qmd not found on PATH"):
        # run with an invalid cmd that definitely doesn't exist
        retrieval._qmd_run({"retrieval": {"qmd_cmd": "non_existent_command_1234"}}, "--version")


def test_doctor_check_qmd_version_mismatch(tmp_home, monkeypatch):
    def mock_run(config, *args):
        return "0.2.0" # mismatched
    monkeypatch.setattr(retrieval, "_qmd_run", mock_run)

    v_check = doctor._check_qmd_version(tmp_home, {})
    assert v_check["ok"] is False
    assert "does not match pinned" in v_check["detail"]


def test_qmd_query_uses_format_json_flag(tmp_home, monkeypatch):
    """qmd has no `--json`; JSON output comes from `--format json`.

    Verified against qmd 2.8.3's own `--help`. The old `--json` made the whole
    qmd backend fail at the first query with an unknown-flag exit.
    """
    seen = []

    def mock_run(config, *args, **kwargs):
        seen.append(args)
        return "[]"

    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run", mock_run)

    retrieval._qmd_retrieve(tmp_home, {}, "pooling", 7)

    args = seen[0]
    assert "--json" not in args
    assert args[1] == "pooling"
    assert "--format" in args and args[args.index("--format") + 1] == "json"
    assert args[args.index("-n") + 1] == "7"
    assert args[args.index("-c") + 1] == retrieval.QMD_COLLECTION


def test_without_the_models_qmd_uses_the_bm25_command(tmp_home, monkeypatch):
    """`qmd query` expands and reranks with local LLMs.

    Run without those models it does not fail fast, it hangs until px0's own
    subprocess timeout fires -- so the whole backend looked broken to anyone who
    declined the ~2GB download. `qmd search` is BM25-only and needs nothing.
    """
    seen = []
    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run",
                        lambda config, *args, **kw: seen.append(args) or "[]")

    retrieval._qmd_retrieve(tmp_home, {}, "pooling", 5)

    assert seen[0][0] == "search"


def test_with_the_models_qmd_uses_the_hybrid_command(tmp_home, monkeypatch):
    import json as _json

    consent = paths.retrieval_consent_path(tmp_home)
    consent.parent.mkdir(parents=True, exist_ok=True)
    consent.write_text(_json.dumps({"qmd_embed_consented": True}))

    seen = []
    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run",
                        lambda config, *args, **kw: seen.append((args, kw)) or "[]")

    retrieval._qmd_retrieve(tmp_home, {}, "pooling", 5)

    assert seen[0][0][0] == "query"
    # Reranking is slow but real work; it must not inherit the short timeout.
    assert seen[0][1].get("timeout", 0) > 60


@pytest.mark.parametrize("reported,expected", [
    ("qmd://px0-brain/docs/x.md", "docs/x.md"),
    ("qmd://px0-brain/work/secret.md", "work/secret.md"),
    ("px0-brain/docs/x.md", "docs/x.md"),
    ("docs/x.md", "docs/x.md"),
    ("qmd://px0-brain/nested/deep/x.md", "nested/deep/x.md"),
])
def test_qmd_paths_are_normalised_to_the_brain_root(reported, expected):
    """qmd reports `qmd://<collection>/...`; only the scheme used to be stripped."""
    assert retrieval._qmd_relative_path(reported) == expected


def test_a_work_passage_from_qmd_is_actually_withheld(tmp_home, monkeypatch):
    """The privacy guarantee, checked through the real parser.

    `local_only` withholds passages with `path.startswith("work/")`. Because the
    parser left the collection name on the front, that test never matched and
    private brain/work/ passages were returned by default. The previous test for
    this fed in pre-cleaned paths, so it asserted the filter while stepping over
    the bug that defeated it.
    """
    canned = (
        '[{"file": "qmd://px0-brain/work/secret.md", "score": 0.9,'
        ' "snippet": "The internal margin is forty percent."},'
        ' {"file": "qmd://px0-brain/docs/public.md", "score": 0.5,'
        ' "snippet": "A public note."}]'
    )
    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run", lambda config, *a, **k: canned)

    got = retrieval.retrieve(
        tmp_home, {}, "margin", k=5, local_only=True
    )

    assert [p.path for p in got] == ["docs/public.md"]
    assert all("forty percent" not in p.text for p in got)


def test_qmd_work_passages_are_still_reachable_when_asked_for(tmp_home, monkeypatch):
    canned = ('[{"file": "qmd://px0-brain/work/secret.md", "score": 0.9,'
              ' "snippet": "Internal only."}]')
    monkeypatch.setattr(retrieval, "_qmd_ensure_collection", lambda h, c: None)
    monkeypatch.setattr(retrieval, "_qmd_run", lambda config, *a, **k: canned)

    got = retrieval.retrieve(
        tmp_home, {}, "internal", k=5, local_only=False
    )

    assert [p.path for p in got] == ["work/secret.md"]


def test_doctor_check_qmd_version_parses_real_version_output(tmp_home, monkeypatch):
    """`qmd --version` prints "qmd 2.8.3 (facd35e)", not a bare version number."""
    monkeypatch.setattr(
        retrieval, "_qmd_run",
        lambda config, *a, **kw: f"qmd {retrieval.QMD_PINNED_VERSION} (facd35e)\n",
    )

    v_check = doctor._check_qmd_version(tmp_home, {})
    assert v_check["ok"] is True, v_check["detail"]


def test_doctor_check_qmd_version_reports_unparseable_output(tmp_home, monkeypatch):
    monkeypatch.setattr(retrieval, "_qmd_run", lambda config, *a, **kw: "not a version\n")

    v_check = doctor._check_qmd_version(tmp_home, {})
    assert v_check["ok"] is False
    assert "could not parse" in v_check["detail"]


@pytest.mark.parametrize("value,expected", [
    (None, None),
    ("2026-08-21", "2026-08-21"),
])
def test_as_text_passes_through_strings_and_none(value, expected):
    assert retrieval._as_text(value) == expected


def test_as_text_coerces_a_yaml_date_to_a_string():
    """YAML turns an unquoted `retrieved: 2026-08-21` into a `datetime.date`,
    but `Passage.ingested_at` promises a string."""
    import datetime

    assert retrieval._as_text(datetime.date(2026, 8, 21)) == "2026-08-21"


def test_qmd_pinned_version_is_a_published_release():
    """0.1.0 was never published; guard against another placeholder pin."""
    from packaging import version
    assert version.parse(retrieval.QMD_PINNED_VERSION) >= version.parse("0.9.0")
