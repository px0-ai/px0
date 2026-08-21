"""Every document type the brain accepts, ingested for real.

The point of this file is that `px0 brain add` works on a stock machine: no
poppler, no pandoc, no network. Where an external tool would normally be
preferred, `no_external_tools` hides it so the built-in route is what runs.
"""

import zipfile

import pytest

from px0 import brain, paths, retrieval
from tests.conftest import build_docx, build_minimal_pdf, build_odt


def _add(tmp_home, brain_config, source, **kw):
    kw.setdefault("no_propose", True)
    return brain.add(tmp_home, brain_config, str(source), **kw)


def _body_of(result):
    """The ingested file's body, without its frontmatter."""
    return brain.read_header(result.path)[1]


# --- kind detection ---------------------------------------------------------

@pytest.mark.parametrize("name,kind,folder", [
    ("note.md", "text", "docs"),
    ("note.markdown", "text", "docs"),
    ("note.txt", "text", "docs"),
    ("note.text", "text", "docs"),
    ("note.rst", "text", "docs"),
    ("note.org", "text", "docs"),
    ("paper.pdf", "pdf", "papers"),
    ("doc.docx", "document", "docs"),
    ("doc.doc", "document", "docs"),
    ("doc.odt", "document", "docs"),
    ("page.html", "html", "blogs"),
    ("page.htm", "html", "blogs"),
])
def test_every_supported_suffix_detects_its_kind_and_folder(name, kind, folder):
    assert brain._detect_kind(name) == (kind, folder)


@pytest.mark.parametrize("suffix", [".md", ".MD", ".Md", ".PDF", ".DocX", ".HTML"])
def test_suffix_detection_is_case_insensitive(suffix):
    """Files come off other people's machines with shouty extensions."""
    assert brain._detect_kind(f"thing{suffix}")[0] in ("text", "pdf", "document", "html")


@pytest.mark.parametrize("url,kind", [
    ("https://example.com/post", "web"),
    ("http://example.com/post", "web"),
    ("https://www.youtube.com/watch?v=abcdefghijk", "youtube"),
    ("https://youtu.be/abcdefghijk", "youtube"),
    ("https://www.youtube.com/playlist?list=PL123", "youtube-playlist"),
])
def test_url_shapes_detect_their_kind(url, kind):
    assert brain._detect_kind(url)[0] == kind


def test_an_unreadable_suffix_names_itself_and_lists_what_works():
    """"unrecognized source" is useless without saying what would be recognized."""
    with pytest.raises(brain.IngestError) as e:
        brain._detect_kind("photo.png")
    message = str(e.value)
    assert ".png" in message
    # The list is generated from _SUFFIX_KINDS, so it cannot drift out of step
    # with what the code actually handles the way a hand-written string did.
    for expected in (".md", ".rst", ".org", ".pdf", ".docx", ".odt", ".html"):
        assert expected in message


def test_a_bare_name_with_no_suffix_lists_what_works():
    with pytest.raises(brain.IngestError) as e:
        brain._detect_kind("some-random-name")
    assert ".md" in str(e.value)


# --- plain text formats -----------------------------------------------------

@pytest.mark.parametrize("name", ["a.md", "b.markdown", "c.txt", "d.text", "e.rst", "f.org"])
def test_text_formats_ingest_with_their_body_intact(tmp_home, brain_config, tmp_path, name):
    src = tmp_path / name
    src.write_text("# Sharding\n\nSharding splits data across nodes.\n")

    result = _add(tmp_home, brain_config, src)

    assert result.kind == "docs"
    assert "Sharding splits data across nodes." in _body_of(result)


def test_a_markdown_heading_becomes_the_title(tmp_home, brain_config, tmp_path):
    src = tmp_path / "caching.md"
    src.write_text("# Write-Through Caching\n\nBoth stores stay in sync.\n")

    result = _add(tmp_home, brain_config, src)

    assert brain.read_header(result.path)[0]["title"] == "Write-Through Caching"


def test_a_text_file_in_another_encoding_still_ingests(tmp_home, brain_config, tmp_path):
    """A latin-1 note should land with its readable text, not be refused.

    Strict utf-8 decoding rejected the whole file over a handful of bytes, which
    matters because `brain.path` is documented as pointable at an existing notes
    vault that px0 did not write.
    """
    src = tmp_path / "accented.txt"
    src.write_bytes("Café latency and naïve retries\n".encode("latin-1"))

    result = _add(tmp_home, brain_config, src)

    body = _body_of(result)
    assert "latency" in body and "retries" in body


def test_a_missing_file_says_so(tmp_home, brain_config, tmp_path):
    with pytest.raises(brain.IngestError, match="no such file"):
        _add(tmp_home, brain_config, tmp_path / "absent.md")


# --- PDF --------------------------------------------------------------------

def test_a_pdf_ingests_with_no_poppler_installed(
    tmp_home, brain_config, tmp_path, no_external_tools
):
    """Requiring poppler made `papers/` unusable on a stock machine."""
    src = tmp_path / "consensus.pdf"
    src.write_bytes(build_minimal_pdf("Consensus requires a quorum of replicas"))

    result = _add(tmp_home, brain_config, src)

    assert result.kind == "papers"
    assert "quorum of replicas" in _body_of(result)


def test_pdftotext_is_preferred_when_it_is_installed(
    tmp_home, brain_config, tmp_path, monkeypatch
):
    """poppler keeps multi-column papers readable, so it wins when present."""
    src = tmp_path / "layout.pdf"
    src.write_bytes(build_minimal_pdf("fallback text"))

    monkeypatch.setattr("px0.brain.shutil.which", lambda name, *a, **k: "/usr/bin/pdftotext")

    class _Ran:
        returncode = 0
        stdout = "text from pdftotext\n"
        stderr = ""

    monkeypatch.setattr("px0.brain.subprocess.run", lambda *a, **k: _Ran())

    assert "text from pdftotext" in _body_of(_add(tmp_home, brain_config, src))


def test_a_failing_pdftotext_falls_back_instead_of_giving_up(
    tmp_home, brain_config, tmp_path, monkeypatch
):
    """pdftotext rejects some PDFs that pypdf reads fine."""
    src = tmp_path / "awkward.pdf"
    src.write_bytes(build_minimal_pdf("readable by pypdf"))

    monkeypatch.setattr("px0.brain.shutil.which", lambda name, *a, **k: "/usr/bin/pdftotext")

    class _Failed:
        returncode = 1
        stdout = ""
        stderr = "Syntax Error: could not read xref"

    monkeypatch.setattr("px0.brain.subprocess.run", lambda *a, **k: _Failed())

    assert "readable by pypdf" in _body_of(_add(tmp_home, brain_config, src))


def test_a_scanned_pdf_says_it_needs_ocr(tmp_home, brain_config, tmp_path, no_external_tools):
    """A valid PDF with no text layer is a scan; "no text" alone is not a fix."""
    src = tmp_path / "scan.pdf"
    src.write_bytes(build_minimal_pdf(""))

    with pytest.raises(brain.IngestError, match="OCR"):
        _add(tmp_home, brain_config, src)


def test_a_file_that_is_not_really_a_pdf_reports_that(
    tmp_home, brain_config, tmp_path, no_external_tools
):
    src = tmp_path / "notreally.pdf"
    src.write_text("this is plain text wearing a .pdf suffix")

    with pytest.raises(brain.IngestError):
        _add(tmp_home, brain_config, src)


# --- docx / odt -------------------------------------------------------------

def test_a_docx_ingests_with_no_pandoc_installed(
    tmp_home, brain_config, tmp_path, no_external_tools
):
    """pandoc is a large install; .docx must not depend on it."""
    src = tmp_path / "design.docx"
    src.write_bytes(build_docx(["Quorum Writes", "A write needs a majority of replicas."]))

    result = _add(tmp_home, brain_config, src)

    body = _body_of(result)
    assert "Quorum Writes" in body
    assert "majority of replicas" in body


def test_an_odt_ingests_with_no_pandoc_installed(
    tmp_home, brain_config, tmp_path, no_external_tools
):
    src = tmp_path / "notes.odt"
    src.write_bytes(build_odt(["Vector Clocks", "Each node keeps its own counter."]))

    body = _body_of(_add(tmp_home, brain_config, src))
    assert "Vector Clocks" in body and "own counter" in body


def test_pandoc_is_preferred_when_it_is_installed(
    tmp_home, brain_config, tmp_path, monkeypatch
):
    src = tmp_path / "design.docx"
    src.write_bytes(build_docx(["stdlib would read this"]))

    monkeypatch.setattr("px0.brain.shutil.which", lambda name, *a, **k: "/usr/bin/pandoc")

    class _Ran:
        returncode = 0
        stdout = "text from pandoc\n"
        stderr = ""

    monkeypatch.setattr("px0.brain.subprocess.run", lambda *a, **k: _Ran())

    assert "text from pandoc" in _body_of(_add(tmp_home, brain_config, src))


def test_a_failing_pandoc_falls_back_to_the_stdlib_reader(
    tmp_home, brain_config, tmp_path, monkeypatch
):
    src = tmp_path / "design.docx"
    src.write_bytes(build_docx(["read by the stdlib reader"]))

    monkeypatch.setattr("px0.brain.shutil.which", lambda name, *a, **k: "/usr/bin/pandoc")

    class _Failed:
        returncode = 1
        stdout = ""
        stderr = "pandoc: unsupported"

    monkeypatch.setattr("px0.brain.subprocess.run", lambda *a, **k: _Failed())

    assert "read by the stdlib reader" in _body_of(_add(tmp_home, brain_config, src))


def test_legacy_doc_without_pandoc_says_what_to_do(
    tmp_home, brain_config, tmp_path, no_external_tools
):
    """The binary .doc format has no stdlib route, so the error has to be useful."""
    src = tmp_path / "old.doc"
    src.write_bytes(b"\xd0\xcf\x11\xe0legacy word blob")

    with pytest.raises(brain.IngestError) as e:
        _add(tmp_home, brain_config, src)
    detail = str(e.value)
    assert "pandoc" in detail and ".docx" in detail


def test_a_corrupt_docx_reports_the_file_not_a_traceback(
    tmp_home, brain_config, tmp_path, no_external_tools
):
    src = tmp_path / "broken.docx"
    src.write_bytes(b"not a zip at all")

    with pytest.raises(brain.IngestError, match="broken.docx"):
        _add(tmp_home, brain_config, src)


def test_a_docx_missing_its_document_part_reports_the_file(
    tmp_home, brain_config, tmp_path, no_external_tools
):
    src = tmp_path / "empty.docx"
    with zipfile.ZipFile(src, "w") as zf:
        zf.writestr("[Content_Types].xml", "<Types/>")

    with pytest.raises(brain.IngestError, match="empty.docx"):
        _add(tmp_home, brain_config, src)


# --- local HTML -------------------------------------------------------------

def test_a_saved_web_page_ingests_from_disk(tmp_home, brain_config, tmp_path):
    """A page saved to disk is a local file, not a URL, and needs its own route."""
    src = tmp_path / "post.html"
    src.write_text(
        "<html><head><title>Backpressure</title></head><body>"
        "<nav>site menu</nav>"
        "<article><h1>Backpressure</h1><p>Queues need bounded depth.</p></article>"
        "<footer>copyright</footer></body></html>"
    )

    result = _add(tmp_home, brain_config, src)

    assert result.kind == "blogs"
    header, body = brain.read_header(result.path)
    assert header["title"] == "Backpressure"
    assert "Queues need bounded depth." in body
    # The same chrome-stripping as a fetched page: nav/footer are dropped and
    # <article> wins over the rest of the body.
    assert "site menu" not in body and "copyright" not in body


# --- YouTube ----------------------------------------------------------------

@pytest.mark.parametrize("url", [
    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    "https://youtube.com/watch?list=PL1&v=dQw4w9WgXcQ&t=30",
    "https://youtu.be/dQw4w9WgXcQ",
    "https://youtu.be/dQw4w9WgXcQ?t=42",
    "https://www.youtube.com/shorts/dQw4w9WgXcQ",
    "https://www.youtube.com/embed/dQw4w9WgXcQ",
    "https://www.youtube.com/live/dQw4w9WgXcQ",
    "https://www.youtube.com/v/dQw4w9WgXcQ",
])
def test_every_youtube_url_shape_yields_the_video_id(url):
    """Only `watch?v=` used to parse; a Shorts link silently became a stub."""
    assert brain._youtube_id(url) == "dQw4w9WgXcQ"


def test_a_video_with_a_transcript_lands_as_a_video(tmp_home, brain_config, monkeypatch):
    monkeypatch.setattr(
        brain, "_extract_youtube",
        lambda url: ("Raft Explained", "Leaders send heartbeats.", {"author_name": "ch"}),
    )

    result = _add(tmp_home, brain_config, "https://youtu.be/dQw4w9WgXcQ")

    assert result.is_stub is False
    header, body = brain.read_header(result.path)
    assert header["kind"] == "video" and "heartbeats" in body


def test_a_video_with_no_transcript_lands_as_a_stub(tmp_home, brain_config, monkeypatch):
    monkeypatch.setattr(
        brain, "_extract_youtube",
        lambda url: ("Untranscribed Talk", None, {"author_name": "some channel"}),
    )

    result = _add(tmp_home, brain_config, "https://youtu.be/dQw4w9WgXcQ")

    assert result.is_stub is True
    header, body = brain.read_header(result.path)
    assert header["kind"] == "stub"
    assert header["channel"] == "some channel"
    # The stub has to say how to finish the job later.
    assert "px0 brain refresh" in body


def test_a_stub_is_not_sent_to_the_proposal_pass(tmp_home, brain_config, monkeypatch):
    """There is nothing to learn from metadata, and the pass costs a model call."""
    monkeypatch.setattr(brain, "_extract_youtube", lambda url: ("T", None, {}))
    called = []
    monkeypatch.setattr(
        "px0.proposals.propose_from_brain", lambda *a, **k: called.append(a)
    )

    brain.add(tmp_home, brain_config, "https://youtu.be/dQw4w9WgXcQ", no_propose=False)

    assert called == []


# --- web --------------------------------------------------------------------

def test_a_web_page_ingests_its_readable_text(tmp_home, brain_config, monkeypatch):
    class _Resp:
        text = ("<html><head><title>On Queues</title></head><body>"
                "<script>tracker()</script><main><p>Bound every queue.</p></main>"
                "</body></html>")

    monkeypatch.setattr(brain, "_fetch", lambda url, **k: _Resp())

    result = _add(tmp_home, brain_config, "https://example.com/on-queues")

    assert result.kind == "blogs"
    header, body = brain.read_header(result.path)
    assert header["title"] == "On Queues"
    assert "Bound every queue." in body and "tracker()" not in body


# --- destination folders ----------------------------------------------------

def test_init_scaffolds_the_work_folder(tmp_home, brain_config):
    """work/ carries the never-leaves-this-machine guarantee, so it should exist."""
    assert (retrieval.brain_path(tmp_home, brain_config) / "work").is_dir()


def test_a_source_can_be_filed_into_work(tmp_home, brain_config, tmp_path):
    src = tmp_path / "internal.md"
    src.write_text("# Internal\n\nNot for anyone else.\n")

    result = _add(tmp_home, brain_config, src, to="work")

    assert result.kind == "work"
    assert result.path.parent.name == "work"


@pytest.mark.parametrize("folder", ["docs", "blogs", "papers", "work"])
def test_to_overrides_the_default_routing(tmp_home, brain_config, tmp_path, folder):
    src = tmp_path / "note.md"
    src.write_text("# Note\n\nbody\n")

    result = _add(tmp_home, brain_config, src, to=folder)

    assert result.path.parent.name == folder


def test_siblings_under_a_long_path_do_not_collide(tmp_home, brain_config, tmp_path):
    """Slugging the full path and truncating to 80 chars made siblings overwrite."""
    deep = tmp_path / ("d" * 90)
    deep.mkdir()
    (deep / "alpha.md").write_text("# Alpha\n\nfirst\n")
    (deep / "beta.md").write_text("# Beta\n\nsecond\n")

    a = _add(tmp_home, brain_config, deep / "alpha.md")
    b = _add(tmp_home, brain_config, deep / "beta.md")

    assert a.path != b.path
    assert "first" in _body_of(a) and "second" in _body_of(b)


# --- refresh ----------------------------------------------------------------

def test_refresh_re_reads_a_local_text_file(tmp_home, brain_config, tmp_path):
    src = tmp_path / "evolving.md"
    src.write_text("# V1\n\nfirst version\n")
    result = _add(tmp_home, brain_config, src)

    src.write_text("# V2\n\nsecond version\n")
    refreshed = brain.refresh(tmp_home, brain_config, result.path, no_propose=True)

    body = _body_of(refreshed)
    assert "second version" in body and "first version" not in body


def test_refresh_re_reads_a_local_html_file(tmp_home, brain_config, tmp_path):
    src = tmp_path / "page.html"
    src.write_text("<html><title>T</title><body><p>before</p></body></html>")
    result = _add(tmp_home, brain_config, src)

    src.write_text("<html><title>T</title><body><p>after</p></body></html>")
    refreshed = brain.refresh(tmp_home, brain_config, result.path, no_propose=True)

    assert "after" in _body_of(refreshed)


def test_refresh_re_extracts_a_pdf(tmp_home, brain_config, tmp_path, no_external_tools):
    src = tmp_path / "paper.pdf"
    src.write_bytes(build_minimal_pdf("first draft"))
    result = _add(tmp_home, brain_config, src)

    src.write_bytes(build_minimal_pdf("revised draft"))
    refreshed = brain.refresh(tmp_home, brain_config, result.path, no_propose=True)

    assert "revised draft" in _body_of(refreshed)


def test_refresh_re_extracts_a_docx(tmp_home, brain_config, tmp_path, no_external_tools):
    src = tmp_path / "spec.docx"
    src.write_bytes(build_docx(["original wording"]))
    result = _add(tmp_home, brain_config, src)

    src.write_bytes(build_docx(["amended wording"]))
    refreshed = brain.refresh(tmp_home, brain_config, result.path, no_propose=True)

    assert "amended wording" in _body_of(refreshed)


def test_refresh_re_fetches_a_web_page(tmp_home, brain_config, monkeypatch):
    pages = iter([
        "<html><title>P</title><body><p>old copy</p></body></html>",
        "<html><title>P</title><body><p>new copy</p></body></html>",
    ])

    class _Resp:
        def __init__(self):
            self.text = next(pages)

    monkeypatch.setattr(brain, "_fetch", lambda url, **k: _Resp())
    result = _add(tmp_home, brain_config, "https://example.com/p")

    refreshed = brain.refresh(tmp_home, brain_config, result.path, no_propose=True)

    assert "new copy" in _body_of(refreshed)


def test_refresh_promotes_a_stub_once_a_transcript_appears(
    tmp_home, brain_config, monkeypatch
):
    monkeypatch.setattr(brain, "_extract_youtube", lambda url: ("Talk", None, {}))
    result = _add(tmp_home, brain_config, "https://youtu.be/dQw4w9WgXcQ")
    assert result.is_stub

    monkeypatch.setattr(
        brain, "_extract_youtube", lambda url: ("Talk", "now transcribed", {})
    )
    refreshed = brain.refresh(tmp_home, brain_config, result.path, no_propose=True)

    header, body = brain.read_header(refreshed.path)
    assert header["kind"] == "video" and "now transcribed" in body
    assert refreshed.is_stub is False


def test_refresh_on_a_still_untranscribed_stub_says_so(tmp_home, brain_config, monkeypatch):
    monkeypatch.setattr(brain, "_extract_youtube", lambda url: ("Talk", None, {}))
    result = _add(tmp_home, brain_config, "https://youtu.be/dQw4w9WgXcQ")

    with pytest.raises(brain.IngestError, match="still no transcript"):
        brain.refresh(tmp_home, brain_config, result.path, no_propose=True)


def test_refresh_needs_a_recorded_source(tmp_home, brain_config):
    hand_written = retrieval.brain_path(tmp_home, brain_config) / "docs" / "mine.md"
    hand_written.parent.mkdir(parents=True, exist_ok=True)
    hand_written.write_text("# Mine\n\nwritten by hand, fetched from nowhere\n")

    with pytest.raises(brain.IngestError, match="no source"):
        brain.refresh(tmp_home, brain_config, hand_written, no_propose=True)


def test_refresh_reports_a_vanished_original(tmp_home, brain_config, tmp_path):
    src = tmp_path / "temporary.md"
    src.write_text("# T\n\nbody\n")
    result = _add(tmp_home, brain_config, src)
    src.unlink()

    with pytest.raises(brain.IngestError, match="gone"):
        brain.refresh(tmp_home, brain_config, result.path, no_propose=True)


# --- resolving a path the user typed ----------------------------------------

def test_a_brain_path_resolves_in_every_form_the_user_might_have(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    target = base / "blogs" / "post.md"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text("---\nsource: https://x.test\n---\nbody\n")

    for form in ("blogs/post.md", "brain/blogs/post.md", "post.md", str(target)):
        assert brain.resolve_brain_path(tmp_home, brain_config, form) == target


def test_a_directory_is_not_a_brain_file(tmp_home, brain_config):
    """`brain refresh docs` used to resolve the folder, then fail deep inside."""
    with pytest.raises(brain.IngestError):
        brain.resolve_brain_path(tmp_home, brain_config, "docs")


def test_an_ambiguous_bare_name_lists_the_candidates(tmp_home, brain_config):
    base = retrieval.brain_path(tmp_home, brain_config)
    for folder in ("docs", "blogs"):
        (base / folder).mkdir(parents=True, exist_ok=True)
        (base / folder / "same.md").write_text("body\n")

    with pytest.raises(brain.IngestError, match="ambiguous"):
        brain.resolve_brain_path(tmp_home, brain_config, "same.md")


def test_a_miss_points_at_the_listing_command(tmp_home, brain_config):
    with pytest.raises(brain.IngestError, match="px0 brain list"):
        brain.resolve_brain_path(tmp_home, brain_config, "nope.md")


# --- fetch failures ---------------------------------------------------------

def test_http_errors_become_ingest_errors(monkeypatch):
    import requests

    class _Boom:
        status_code = 404

        def raise_for_status(self):
            raise requests.exceptions.HTTPError(response=self)

    monkeypatch.setattr(requests, "get", lambda *a, **k: _Boom())

    with pytest.raises(brain.IngestError, match="404"):
        brain._fetch("https://example.com/missing")


def test_a_tls_failure_names_the_ca_bundle_setting(monkeypatch):
    """On an intercepting network this is the one thing the user needs told."""
    import requests

    def _boom(*a, **k):
        raise requests.exceptions.SSLError("cert verify failed")

    monkeypatch.setattr(requests, "get", _boom)

    with pytest.raises(brain.IngestError, match="connectors.ca_bundle"):
        brain._fetch("https://example.com/intercepted")


def test_a_timeout_becomes_an_ingest_error(monkeypatch):
    import requests

    def _boom(*a, **k):
        raise requests.exceptions.Timeout()

    monkeypatch.setattr(requests, "get", _boom)

    with pytest.raises(brain.IngestError, match="timed out"):
        brain._fetch("https://example.com/slow")


# --- playlists --------------------------------------------------------------

def test_a_playlist_is_queued_for_the_daemon(tmp_home, brain_config):
    with pytest.raises(brain.IngestError, match="queued"):
        _add(tmp_home, brain_config, "https://www.youtube.com/playlist?list=PL123")

    jobs = list(paths.ingest_dir(tmp_home).glob("*.json"))
    assert len(jobs) == 1


def test_a_playlist_that_enumerates_nothing_is_kept_for_a_retry(
    tmp_home, brain_config, monkeypatch
):
    """Treating an empty enumeration as success deleted the job silently.

    A private playlist, a blocked fetch, or a changed page shape all look like
    "zero videos" from here -- none of them mean the work is done.
    """
    import json

    job = paths.ingest_dir(tmp_home) / "playlist.json"
    job.parent.mkdir(parents=True, exist_ok=True)
    job.write_text(json.dumps({
        "source": "https://youtube.com/playlist?list=PLX", "kind": "youtube-playlist",
    }))

    monkeypatch.setattr(brain, "enumerate_playlist", lambda *a: [])

    result = brain.process_ingest_queue(tmp_home, brain_config)

    assert result["videos_ingested"] == 0
    assert job.exists(), "the job must survive so the daemon can try again"
    assert json.loads(job.read_text())["attempts"] == 1


def test_enumerate_playlist_reads_ids_in_order_without_duplicates(
    monkeypatch, no_external_tools
):
    """The fallback path: scraping the page px0 can fetch on its own."""
    class _Resp:
        text = ('junk "videoId":"aaaaaaaaaaa" more "videoId":"bbbbbbbbbbb" '
                'and again "videoId":"aaaaaaaaaaa"')

    monkeypatch.setattr(brain, "_fetch", lambda url, **k: _Resp())

    assert brain.enumerate_playlist("https://youtube.com/playlist?list=PL1") == [
        "https://www.youtube.com/watch?v=aaaaaaaaaaa",
        "https://www.youtube.com/watch?v=bbbbbbbbbbb",
    ]


def test_yt_dlp_enumerates_the_whole_playlist_when_installed(monkeypatch):
    """YouTube serves only the first ~100 videos to a plain GET, so a long
    playlist needs the tool that follows the continuation API."""
    monkeypatch.setattr("px0.brain.shutil.which", lambda name, *a, **k: "/usr/bin/yt-dlp")

    class _Ran:
        returncode = 0
        stdout = "aaaaaaaaaaa\nbbbbbbbbbbb\naaaaaaaaaaa\n\n"
        stderr = ""

    monkeypatch.setattr("px0.brain.subprocess.run", lambda *a, **k: _Ran())

    def _no_fetch(*a, **k):
        raise AssertionError("yt-dlp answered; the page must not be fetched as well")

    monkeypatch.setattr(brain, "_fetch", _no_fetch)

    assert brain.enumerate_playlist("https://youtube.com/playlist?list=PL1") == [
        "https://www.youtube.com/watch?v=aaaaaaaaaaa",
        "https://www.youtube.com/watch?v=bbbbbbbbbbb",
    ]


def test_a_failing_yt_dlp_falls_back_to_scraping(monkeypatch):
    monkeypatch.setattr("px0.brain.shutil.which", lambda name, *a, **k: "/usr/bin/yt-dlp")

    class _Failed:
        returncode = 1
        stdout = ""
        stderr = "ERROR: unable to download"

    monkeypatch.setattr("px0.brain.subprocess.run", lambda *a, **k: _Failed())

    class _Resp:
        text = '"videoId":"ccccccccccc"'

    monkeypatch.setattr(brain, "_fetch", lambda url, **k: _Resp())

    assert brain.enumerate_playlist("https://youtube.com/playlist?list=PL1") == [
        "https://www.youtube.com/watch?v=ccccccccccc"
    ]


def test_yt_dlp_timing_out_falls_back_to_scraping(monkeypatch):
    """A huge playlist must not hang the daemon's housekeeping pass forever."""
    import subprocess as sp

    monkeypatch.setattr("px0.brain.shutil.which", lambda name, *a, **k: "/usr/bin/yt-dlp")

    def _timeout(*a, **k):
        raise sp.TimeoutExpired(cmd="yt-dlp", timeout=180)

    monkeypatch.setattr("px0.brain.subprocess.run", _timeout)

    class _Resp:
        text = '"videoId":"ddddddddddd"'

    monkeypatch.setattr(brain, "_fetch", lambda url, **k: _Resp())

    assert brain.enumerate_playlist("https://youtube.com/playlist?list=PL1") == [
        "https://www.youtube.com/watch?v=ddddddddddd"
    ]


# --- the transcript dependency ---------------------------------------------

def test_the_installed_transcript_api_has_the_method_the_code_calls():
    """A guard on the dependency's shape, not on our own logic.

    `youtube-transcript-api` replaced the static `get_transcript` with an
    instance `fetch` in 1.0. Calling the wrong one raises AttributeError, and
    that used to be swallowed as "no transcript" -- so a resolver landing on an
    older release would have turned every video into a stub, silently.
    """
    from youtube_transcript_api import YouTubeTranscriptApi

    assert hasattr(YouTubeTranscriptApi(), "fetch")


def test_an_old_transcript_api_is_reported_not_disguised_as_a_missing_transcript(
    tmp_home, brain_config, monkeypatch
):
    class _Ancient:
        """The pre-1.0 surface: no .fetch at all."""

    monkeypatch.setitem(
        __import__("sys").modules, "youtube_transcript_api",
        type("_M", (), {"YouTubeTranscriptApi": _Ancient})(),
    )

    with pytest.raises(brain.IngestError, match="too old"):
        brain._extract_youtube("https://youtu.be/dQw4w9WgXcQ")


def test_a_video_with_no_captions_is_still_just_a_stub(tmp_home, brain_config, monkeypatch):
    """The ordinary case must stay ordinary: no captions is not an error."""
    class _Api:
        def fetch(self, video_id):
            raise RuntimeError("Could not retrieve a transcript for this video")

    monkeypatch.setitem(
        __import__("sys").modules, "youtube_transcript_api",
        type("_M", (), {"YouTubeTranscriptApi": _Api})(),
    )
    monkeypatch.setattr(brain, "_youtube_oembed", lambda url: {"title": "Silent Talk"})

    title, transcript, _ = brain._extract_youtube("https://youtu.be/dQw4w9WgXcQ")

    assert title == "Silent Talk" and transcript is None


def test_a_url_with_no_parseable_video_id_does_not_call_the_api(
    tmp_home, brain_config, monkeypatch
):
    monkeypatch.setattr(brain, "_youtube_oembed", lambda url: {})

    def _explode():
        raise AssertionError("the transcript API must not be consulted without an id")

    monkeypatch.setitem(
        __import__("sys").modules, "youtube_transcript_api",
        type("_M", (), {"YouTubeTranscriptApi": lambda: _explode()})(),
    )

    _, transcript, _ = brain._extract_youtube("https://www.youtube.com/feed/subscriptions")

    assert transcript is None


def test_a_playlist_cut_off_at_the_first_page_is_reported_as_such(
    tmp_home, brain_config, monkeypatch
):
    """YouTube renders only ~100 videos into the playlist HTML; the rest sits
    behind a continuation token this does not follow.

    Ingesting 100 of 3000 and reporting the job as done would read as full
    coverage, so the partial result is counted instead.
    """
    import json

    job = paths.ingest_dir(tmp_home) / "long.json"
    job.parent.mkdir(parents=True, exist_ok=True)
    job.write_text(json.dumps({
        "source": "https://youtube.com/playlist?list=PLLONG", "kind": "youtube-playlist",
    }))

    full_page = [
        f"https://www.youtube.com/watch?v=vid{i:08d}"
        for i in range(brain.PLAYLIST_FIRST_PAGE_LIMIT)
    ]
    monkeypatch.setattr(brain, "enumerate_playlist", lambda *a: full_page)
    monkeypatch.setattr(brain, "add", lambda home, cfg, source, to=None: None)

    result = brain.process_ingest_queue(tmp_home, brain_config)

    assert result["playlists_truncated"] == 1


def test_a_short_playlist_is_not_flagged_as_truncated(tmp_home, brain_config, monkeypatch):
    import json

    job = paths.ingest_dir(tmp_home) / "short.json"
    job.parent.mkdir(parents=True, exist_ok=True)
    job.write_text(json.dumps({
        "source": "https://youtube.com/playlist?list=PLSHORT", "kind": "youtube-playlist",
    }))

    monkeypatch.setattr(
        brain, "enumerate_playlist", lambda *a: ["https://www.youtube.com/watch?v=only1video"]
    )
    monkeypatch.setattr(brain, "add", lambda home, cfg, source, to=None: None)

    result = brain.process_ingest_queue(tmp_home, brain_config)

    assert result["playlists_truncated"] == 0


# --- HTML extraction quality -----------------------------------------------

def test_a_sentence_with_links_stays_one_paragraph():
    """Splitting on every inline element shredded prose.

    `get_text("\\n")` over the whole subtree put each `<a>` on its own line, so
    one sentence containing two links arrived as three "paragraphs" -- which
    also chopped it across chunk boundaries at index time.
    """
    html = ("<html><body><article><p>See <a href='#'>Raft</a> and "
            "<a href='#'>Paxos</a> for consensus.</p></article></body></html>")

    _, text = brain._html_to_text(html, "fallback")

    assert text == "See Raft and Paxos for consensus."


def test_separate_blocks_stay_separate():
    html = ("<html><body><article><h2>Heading</h2><p>First para.</p>"
            "<p>Second para.</p></article></body></html>")

    _, text = brain._html_to_text(html, "fallback")

    assert text.split("\n\n") == ["Heading", "First para.", "Second para."]


def test_a_container_does_not_duplicate_its_children():
    """A block wrapping other blocks must not emit their text twice."""
    html = "<html><body><article><div><p>Only once.</p></div></article></body></html>"

    _, text = brain._html_to_text(html, "fallback")

    assert text.count("Only once.") == 1


def test_inline_separators_do_not_leave_gaps_before_punctuation():
    html = ("<html><body><article><p>Consistent hashing<sup>[1]</sup> is useful"
            "<em>,</em> mostly.</p></article></body></html>")

    _, text = brain._html_to_text(html, "fallback")

    assert " ," not in text and " ." not in text
    assert "hashing [1]" in text


def test_list_items_become_their_own_paragraphs():
    html = "<html><body><article><ul><li>One</li><li>Two</li></ul></article></body></html>"

    _, text = brain._html_to_text(html, "fallback")

    assert text.split("\n\n") == ["One", "Two"]


def test_a_page_with_no_block_markup_still_yields_its_text():
    """Not every page uses <p>; falling back beats returning nothing."""
    html = "<html><body><article>Bare text with no block tags at all.</article></body></html>"

    _, text = brain._html_to_text(html, "fallback")

    assert "Bare text with no block tags" in text


def test_the_title_falls_back_when_the_page_has_none():
    _, _ = brain._html_to_text("<html><body><p>x</p></body></html>", "the-fallback")
    title, _ = brain._html_to_text("<html><body><p>x</p></body></html>", "the-fallback")

    assert title == "the-fallback"


# --- a more realistic .docx -------------------------------------------------

def test_a_word_style_docx_with_tables_and_hyperlinks_extracts(
    tmp_home, brain_config, tmp_path, no_external_tools
):
    """Real Word files split a sentence across several runs and nest text in
    tables; the fixtures elsewhere in this file are simpler than that."""
    import zipfile

    w = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    body = (
        # one sentence split across three runs, as Word does after editing
        '<w:p><w:r><w:t xml:space="preserve">Replication factor </w:t></w:r>'
        '<w:r><w:t xml:space="preserve">is set to </w:t></w:r>'
        '<w:r><w:t>three.</w:t></w:r></w:p>'
        # a hyperlink run
        '<w:p><w:hyperlink r:id="rId9"><w:r><w:t>See the design doc</w:t></w:r>'
        '</w:hyperlink></w:p>'
        # a table
        '<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Region</w:t></w:r></w:p></w:tc>'
        '<w:tc><w:p><w:r><w:t>Replicas</w:t></w:r></w:p></w:tc></w:tr>'
        '<w:tr><w:tc><w:p><w:r><w:t>eu-west</w:t></w:r></w:p></w:tc>'
        '<w:tc><w:p><w:r><w:t>5</w:t></w:r></w:p></w:tc></w:tr></w:tbl>'
    )
    document = (
        f'<?xml version="1.0" encoding="UTF-8"?>'
        f'<w:document xmlns:w="{w}" '
        f'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
        f"<w:body>{body}</w:body></w:document>"
    )
    src = tmp_path / "realistic.docx"
    with zipfile.ZipFile(src, "w") as zf:
        zf.writestr("[Content_Types].xml", "<Types/>")
        zf.writestr("word/document.xml", document)

    text = _body_of(_add(tmp_home, brain_config, src))

    # Runs inside one paragraph rejoin into one sentence.
    assert "Replication factor is set to three." in text
    assert "See the design doc" in text
    # Table cells are reachable, each as its own paragraph.
    for cell in ("Region", "Replicas", "eu-west"):
        assert cell in text


# --- --to takes any subfolder ----------------------------------------------

@pytest.mark.parametrize("folder", [
    "docs", "blogs", "papers", "work",           # the defaults
    "Personal/Reading",                          # a vault's own structure
    "Daily Notes",                               # a space in the name
    "a/b/c/d",                                   # arbitrarily deep
    "./docs",                                    # a redundant leading dot
])
def test_to_accepts_any_subfolder_of_the_brain(tmp_home, brain_config, tmp_path, folder):
    """A brain pointed at someone's vault should file into that vault's own
    structure, not be limited to the four folders px0 routes into."""
    src = tmp_path / "note.md"
    src.write_text("# Note\n\nbody\n")

    result = _add(tmp_home, brain_config, src, to=folder)

    base = retrieval.brain_path(tmp_home, brain_config)
    assert result.path.is_file()
    assert result.path.is_relative_to(base)


@pytest.mark.parametrize("folder", [
    "../outside",
    "../../etc",
    "docs/../../..",
    "/tmp/absolute",
    "~/homedir",
    "   ",
    ".",
])
def test_to_refuses_anything_that_would_land_outside_the_brain(
    tmp_home, brain_config, tmp_path, folder
):
    """Free-form means the traversal check has to be real."""
    src = tmp_path / "note.md"
    src.write_text("# Note\n\nbody\n")

    with pytest.raises(brain.IngestError):
        _add(tmp_home, brain_config, src, to=folder)


def test_an_empty_to_means_no_to_at_all(tmp_home, brain_config, tmp_path):
    """`--to ""` is absence, not an error: it falls back to the routed default."""
    src = tmp_path / "note.md"
    src.write_text("# Note\n\nbody\n")

    assert _add(tmp_home, brain_config, src, to="").path.parent.name == "docs"


def test_a_refused_folder_writes_nothing(tmp_home, brain_config, tmp_path):
    src = tmp_path / "note.md"
    src.write_text("# Note\n\nbody\n")
    base = retrieval.brain_path(tmp_home, brain_config)
    before = {str(p) for p in base.rglob("*")}

    with pytest.raises(brain.IngestError):
        _add(tmp_home, brain_config, src, to="../escaped")

    assert {str(p) for p in base.rglob("*")} == before
    assert not (base.parent / "escaped").exists()


@pytest.mark.parametrize("raw,expected", [
    ("docs", "docs"),
    ("./docs", "docs"),
    ("Personal/Reading", "Personal/Reading"),
    ("a//b", "a/b"),
])
def test_resolve_folder_normalises(tmp_home, brain_config, raw, expected):
    assert brain.resolve_folder(tmp_home, brain_config, raw) == expected


def test_the_default_folders_are_still_what_kinds_route_to(tmp_home, brain_config, tmp_path):
    """Relaxing --to must not change where things go when --to is absent."""
    src = tmp_path / "note.md"
    src.write_text("# Note\n\nbody\n")

    assert _add(tmp_home, brain_config, src).path.parent.name == "docs"


def test_the_cli_still_suggests_the_default_folders():
    from px0 import cli

    parser = cli.build_parser()
    action = next(
        a for a in parser.parse_known_args(["brain", "add", "x.md"])[0].__dict__ if a == "to"
    )
    assert action == "to"
    # and a free-form value now parses, where a closed choices list rejected it
    args = parser.parse_args(["brain", "add", "x.md", "--to", "Personal/Reading"])
    assert args.to == "Personal/Reading"
