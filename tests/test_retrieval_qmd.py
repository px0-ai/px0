import pytest
import subprocess
import json
from pathlib import Path
from px0 import retrieval, doctor, paths

def test_qmd_ensure_collection_skips_when_exists(tmp_home, monkeypatch):
    called = []
    def mock_run(config, *args, **kwargs):
        called.append(args)
        if args == ("collection", "list"):
            return "px0-knowledge\nother-collection"
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
    assert called[1][0:3] == ("collection", "add", str(retrieval.knowledge_path(tmp_home, {})))


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


def test_local_only_drops_work_prefixed_passages_both_backends(tmp_home, monkeypatch):
    # Setup SQLite index with a work and non-work passage
    conn = retrieval._connect(tmp_home)
    conn.execute("DELETE FROM passages")
    conn.execute(
        "INSERT INTO passages (text, path, anchor, ingested_at, is_stub, is_work) "
        "VALUES ('work text', 'work/some-doc.md', 'sec', '2026', 0, 1)"
    )
    conn.execute(
        "INSERT INTO passages (text, path, anchor, ingested_at, is_stub, is_work) "
        "VALUES ('public text', 'blogs/some-doc.md', 'sec', '2026', 0, 0)"
    )
    conn.commit()
    conn.close()

    # 1. Local backend - local_only = True (default)
    res_local = retrieval.retrieve(tmp_home, {"retrieval": {"backend": "local"}}, "text", local_only=True)
    assert len(res_local) == 1
    assert res_local[0].path == "blogs/some-doc.md"

    # 2. Local backend - local_only = False
    res_local_all = retrieval.retrieve(tmp_home, {"retrieval": {"backend": "local"}}, "text", local_only=False)
    assert len(res_local_all) == 2

    # 3. QMD backend - local_only = True
    canned_passages = [
        retrieval.Passage(path="work/doc.md", anchor="a", text="work", score=0.9, ingested_at=None, is_stub=False),
        retrieval.Passage(path="blogs/doc.md", anchor="a", text="blogs", score=0.8, ingested_at=None, is_stub=False),
    ]
    monkeypatch.setattr(retrieval, "_qmd_retrieve", lambda *a: canned_passages)
    res_qmd = retrieval.retrieve(tmp_home, {"retrieval": {"backend": "qmd"}}, "text", local_only=True)
    assert len(res_qmd) == 1
    assert res_qmd[0].path == "blogs/doc.md"


def test_missing_qmd_binary_raises_retrieval_backend_error(tmp_home):
    with pytest.raises(retrieval.RetrievalBackendError, match="qmd not found on PATH"):
        # run with an invalid cmd that definitely doesn't exist
        retrieval._qmd_run({"retrieval": {"qmd_cmd": "non_existent_command_1234"}}, "--version")


def test_retrieve_dispatches_correctly(tmp_home, monkeypatch):
    called = []
    monkeypatch.setattr(retrieval, "_qmd_retrieve", lambda *a: called.append("qmd") or [])
    
    # local
    retrieval.retrieve(tmp_home, {"retrieval": {"backend": "local"}}, "test")
    assert called == []

    # qmd
    retrieval.retrieve(tmp_home, {"retrieval": {"backend": "qmd"}}, "test")
    assert called == ["qmd"]


def test_doctor_check_qmd_version_mismatch(tmp_home, monkeypatch):
    def mock_run(config, *args):
        return "0.2.0" # mismatched
    monkeypatch.setattr(retrieval, "_qmd_run", mock_run)

    v_check = doctor._check_qmd_version(tmp_home, {"retrieval": {"backend": "qmd"}})
    assert v_check["ok"] is False
    assert "does not match pinned" in v_check["detail"]
