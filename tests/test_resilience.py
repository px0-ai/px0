"""Regression tests for the failure modes found by walking the whole CLI surface:
a malformed workflow taking down every command, secrets riding along in an
export, unvalidated ids corrupting the alias table, and output paths escaping
the store.
"""

import sqlite3

import pytest

from px0 import claims, config as config_mod, knowledge, paths, runner, runs, store
from px0 import workflow as workflow_mod


VALID_WF = """---
id: good
kind: workflow
version: 1
description: A working workflow
trigger:
  manual: true
output:
  target: stdout
---
Say OK.
"""

# `{` unquoted inside a YAML flow mapping is a parse error -- the form spec.md
# itself uses for a templated output path.
BROKEN_WF = """---
id: broken
kind: workflow
output: {target: file, path: outputs/report-{date}.md}
---
Say OK.
"""


# --- one bad file must not hide the good ones --------------------------

def test_load_all_skips_unparseable_file(tmp_home):
    (paths.workflows_dir(tmp_home) / "good.md").write_text(VALID_WF)
    (paths.workflows_dir(tmp_home) / "broken.md").write_text(BROKEN_WF)

    loaded = workflow_mod.load_all(tmp_home)

    assert "good" in loaded, "a broken sibling must not hide a valid workflow"
    assert "broken" not in loaded


def test_load_errors_names_the_file_and_line(tmp_home):
    (paths.workflows_dir(tmp_home) / "broken.md").write_text(BROKEN_WF)

    errors = workflow_mod.load_errors(tmp_home)

    assert len(errors) == 1
    # The file name is the one thing yaml's own message omits.
    assert "broken.md" in errors[0]
    assert "line 4" in errors[0]


def test_load_all_strict_still_raises(tmp_home):
    (paths.workflows_dir(tmp_home) / "broken.md").write_text(BROKEN_WF)

    with pytest.raises(workflow_mod.WorkflowError):
        workflow_mod.load_all(tmp_home, strict=True)


def test_loading_a_broken_workflow_by_id_reports_the_parse_error(tmp_home):
    (paths.workflows_dir(tmp_home) / "broken.md").write_text(BROKEN_WF)

    with pytest.raises(workflow_mod.WorkflowError) as e:
        workflow_mod.load(tmp_home, "broken")

    # Not "no such workflow": the file is right there, it just does not parse.
    assert "no such workflow" not in str(e.value)
    assert "broken.md" in str(e.value)


def test_doctor_reports_unreadable_workflows(tmp_home):
    from px0 import doctor

    (paths.workflows_dir(tmp_home) / "broken.md").write_text(BROKEN_WF)
    config = config_mod.load(paths.config_path(tmp_home))

    report = doctor.run(tmp_home, config, quick=True)

    assert report["checks"]["workflows"]["ok"] is False
    assert report["all_ok"] is False


# --- export must not carry the api key --------------------------------

def test_export_redacts_api_key_and_its_history(tmp_home, tmp_path):
    secret = "ak_super_secret_value"
    cfg_path = paths.config_path(tmp_home)
    config = config_mod.load(cfg_path)
    config_mod.set_key(config, "connectors.composio_api_key", secret)
    config_mod.save(cfg_path, config)
    # Version config.toml so the secret also lands in the history blobs.
    claims.scan_and_process(tmp_home)

    dest = tmp_path / "exported"
    store.export(tmp_home, dest)

    exported_config = (dest / "config.toml").read_text()
    assert secret not in exported_config
    assert 'composio_api_key = ""' in exported_config

    leaked = [p for p in dest.rglob("*") if p.is_file() and secret.encode() in p.read_bytes()]
    assert leaked == [], f"secret still present in {leaked}"

    manifest = dest / ".state" / "versions" / "manifest.sqlite"
    if manifest.exists():
        conn = sqlite3.connect(manifest)
        try:
            rows = conn.execute(
                "SELECT COUNT(*) FROM versions WHERE path = 'config.toml'"
            ).fetchone()[0]
        finally:
            conn.close()
        assert rows == 0, "config.toml history must not travel with an export"


def test_export_keeps_workflow_history(tmp_home, tmp_path):
    (paths.workflows_dir(tmp_home) / "good.md").write_text(VALID_WF)
    claims.scan_and_process(tmp_home)

    dest = tmp_path / "exported"
    store.export(tmp_home, dest)

    assert (dest / "workflows" / "good.md").exists()
    conn = sqlite3.connect(dest / ".state" / "versions" / "manifest.sqlite")
    try:
        rows = conn.execute(
            "SELECT COUNT(*) FROM versions WHERE path = 'workflows/good.md'"
        ).fetchone()[0]
    finally:
        conn.close()
    assert rows >= 1


# --- claim ids ---------------------------------------------------------

@pytest.mark.parametrize("bad", ["nope", "", "#slug", "file.md#", "no-hash-here"])
def test_parse_claim_id_rejects_malformed(bad):
    with pytest.raises(claims.ClaimIdError):
        claims.parse_claim_id(bad)


def test_parse_claim_id_accepts_well_formed():
    assert claims.parse_claim_id("commit-messages.md#summary") == (
        "commit-messages.md", "summary")


def test_alias_link_rejects_malformed_ids(tmp_home):
    with pytest.raises(claims.ClaimIdError):
        claims.add_alias(tmp_home, "a", "b")
    assert claims.list_aliases(tmp_home) == []


def test_a_legacy_bad_alias_does_not_break_lineage(tmp_home):
    """A row written before validation existed must not poison other claims."""
    conn = claims.versioning.connect(tmp_home)
    try:
        conn.execute("INSERT INTO aliases (old_claim, new_claim) VALUES ('a', 'b')")
        conn.commit()
    finally:
        conn.close()

    # Used to raise "not enough values to unpack" for a perfectly good id.
    assert claims.lineage_slugs(tmp_home, "real.md", "real.md#slug") == {"slug"}


def test_guidelines_log_reports_a_malformed_id(tmp_home):
    with pytest.raises(claims.ClaimIdError):
        claims.guidelines_log(tmp_home, "nope")


# --- run ids -----------------------------------------------------------

@pytest.mark.parametrize("bad", ["nope", "run_", "run_notadate", "x"])
def test_date_of_rejects_non_run_ids(bad):
    with pytest.raises(runs.RunIdError):
        runs._date_of(bad)


def test_date_of_parses_a_real_run_id():
    assert runs._date_of("run_20260817-093000-ab12") == "2026-08-17"


def test_why_explains_an_unusable_id(tmp_home):
    from px0 import provenance

    config = config_mod.load(paths.config_path(tmp_home))
    with pytest.raises(provenance.WhyError) as e:
        provenance.why(tmp_home, config, "nope")
    # Should name both shapes rather than surfacing an IndexError.
    assert "claim" in str(e.value)


# --- output paths ------------------------------------------------------

def test_output_path_accepts_both_brace_styles(tmp_home):
    single = runner._render_output_path("report-{date}.md")
    double = runner._render_output_path("report-{{date}}.md")
    assert single == double
    assert "{" not in double and "}" not in double


def test_output_path_rejects_unknown_placeholder():
    with pytest.raises(runner.RunError):
        runner._render_output_path("report-{week}.md")


@pytest.mark.parametrize("escape", [
    "output/../../etc/px0-escape.md",
    "output/../../../px0-escape.md",
])
def test_output_path_cannot_escape_the_store(tmp_home, escape):
    with pytest.raises(runner.RunError):
        runner._resolve_output_dest(tmp_home, escape)


def test_output_path_inside_the_store_is_fine(tmp_home):
    dest = runner._resolve_output_dest(tmp_home, "output/report.md")
    assert dest.is_relative_to(paths.output_dir(tmp_home))


def test_route_output_contains_an_absolute_path(tmp_home):
    result = runner.route_output(
        tmp_home, {"target": "file", "path": "/tmp/px0-should-not-escape.md"}, "body")
    written = tmp_home / result["path"]
    assert written.is_relative_to(paths.output_dir(tmp_home))


# --- knowledge ---------------------------------------------------------

@pytest.mark.parametrize("name,kind", [
    ("note.md", "text"),
    ("note.markdown", "text"),
    ("note.txt", "text"),
    ("paper.pdf", "pdf"),
    ("doc.docx", "document"),
])
def test_detect_kind_accepts_local_files(name, kind):
    assert knowledge._detect_kind(name)[0] == kind


def test_local_files_do_not_collide_on_a_long_path(tmp_home, tmp_path):
    """Slugging the full path truncated to 80 chars made siblings overwrite."""
    deep = tmp_path / ("d" * 90)
    deep.mkdir()
    a, b = deep / "alpha.md", deep / "beta.md"
    a.write_text("# Alpha\n")
    b.write_text("# Beta\n")

    assert knowledge._slug_from_source(str(a)) != knowledge._slug_from_source(str(b))


def test_add_reads_a_markdown_file(tmp_home, tmp_path, monkeypatch):
    monkeypatch.setattr("px0.retrieval.reindex", lambda *a, **k: None)
    src = tmp_path / "caching.md"
    src.write_text("# Caching\n\nWrite-through keeps both in sync.\n")
    config = config_mod.load(paths.config_path(tmp_home))

    result = knowledge.add(tmp_home, config, str(src), no_propose=True)

    assert result.path.exists()
    assert "Write-through" in result.path.read_text()


def test_fetch_turns_http_errors_into_ingest_error(monkeypatch):
    import requests

    class Boom:
        status_code = 404
        def raise_for_status(self):
            raise requests.exceptions.HTTPError(response=self)

    monkeypatch.setattr(requests, "get", lambda *a, **k: Boom())
    with pytest.raises(knowledge.IngestError) as e:
        knowledge._fetch("https://example.com/missing")
    assert "404" in str(e.value)


def test_resolve_knowledge_path_accepts_library_relative(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    base = knowledge.knowledge_path(tmp_home, config)
    target = base / "blogs" / "post.md"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text("---\nsource: https://x.test\n---\nbody\n")

    for form in ("blogs/post.md", "knowledge/blogs/post.md", "post.md", str(target)):
        assert knowledge.resolve_knowledge_path(tmp_home, config, form) == target


def test_resolve_knowledge_path_reports_a_miss(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    with pytest.raises(knowledge.IngestError):
        knowledge.resolve_knowledge_path(tmp_home, config, "nope.md")


# --- run records ------------------------------------------------------

def test_records_are_scoped_to_their_store(tmp_home, tmp_path, monkeypatch):
    config = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(config, "logs.path", str(tmp_path / "logs"))

    monkeypatch.setenv("PX0_HOME", str(tmp_home))
    runs.write_record(config, {"id": "run_20260817-093000-aaaa", "workflow_id": "mine"})

    other = tmp_path / "other_store"
    other.mkdir()
    monkeypatch.setenv("PX0_HOME", str(other))
    runs.write_record(config, {"id": "run_20260817-093001-bbbb", "workflow_id": "theirs"})

    ids = [r["id"] for r in runs.list_records(config)]
    assert "run_20260817-093001-bbbb" in ids
    assert "run_20260817-093000-aaaa" not in ids, "another store's runs must not show"


def test_unstamped_legacy_records_still_list(tmp_home, tmp_path, monkeypatch):
    config = config_mod.load(paths.config_path(tmp_home))
    config_mod.set_key(config, "logs.path", str(tmp_path / "logs"))
    monkeypatch.setenv("PX0_HOME", str(tmp_home))

    path = runs.record_path(config, "run_20260817-093000-cccc")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text('{"id": "run_20260817-093000-cccc", "workflow_id": "old"}')

    ids = [r["id"] for r in runs.list_records(config)]
    assert "run_20260817-093000-cccc" in ids
