"""px0 brain add: filing outside material into the brain.

Ingestion is text only, and extraction always runs locally -- no API keys, and
nothing leaves the machine. Every format has a route that works on a stock
install, with an external tool used only when it is present and does the job
better:

  web              requests + BeautifulSoup
  html/htm         the same reader, over a page already saved to disk
  pdf              `pdftotext -layout` when poppler is installed, else pypdf
  docx/odt         `pandoc` when installed, else a stdlib zip+XML reader
  doc              `pandoc` only (no stdlib route for the legacy binary format)
  md/txt/rst/org   read as-is
  youtube          youtube-transcript-api, with oEmbed metadata as a fallback
                   when no transcript is published (the file lands as a stub)
"""

import json
import re
import shutil
import subprocess
from dataclasses import dataclass
from datetime import date
from pathlib import Path
from urllib.parse import urlparse

import requests
import yaml
from bs4 import BeautifulSoup

from px0 import paths, retrieval
from px0.retrieval import brain_path


class IngestError(Exception):
    """A source could not be ingested into the brain (unrecognized, extraction tool
    missing, extraction failed, etc.)."""


@dataclass
class IngestResult:
    """Where an ingested (or refreshed) source landed, and whether it's still
    a stub awaiting a transcript."""
    path: Path
    kind: str  # docs | blogs | papers | work
    is_stub: bool


def read_text_lossy(path: Path) -> str:
    """Reads a file as UTF-8, replacing undecodable bytes instead of raising.

    `brain.path` is documented as being pointable at any folder -- an existing
    notes vault -- so the library is not all px0's own output. One file saved in
    another encoding used to abort the caller with UnicodeDecodeError, and since
    `reindex` walks every file, that meant a single stray byte anywhere made the
    whole brain unsearchable.
    """
    return path.read_text(encoding="utf-8", errors="replace")


def read_header(path: Path) -> tuple[dict, str]:
    """Splits a brain file into its YAML frontmatter dict and body text.
    Returns ({}, full_text) if the file has no parseable frontmatter block.

    Anything that isn't a clean `---`-delimited YAML mapping is treated as a
    file with no frontmatter, so the body still gets indexed. A hand-written
    note that merely opens with a horizontal rule -- or one whose frontmatter
    is malformed -- would otherwise raise out of here and take `reindex` down
    with it.
    """
    text = read_text_lossy(path)
    if not text.startswith("---"):
        return {}, text
    parts = text.split("---", 2)
    if len(parts) < 3:
        return {}, text
    try:
        header = yaml.safe_load(parts[1]) or {}
    except yaml.YAMLError:
        return {}, text
    if not isinstance(header, dict):
        # `---` used as a horizontal rule parses as a bare scalar (or a list).
        # Callers all expect a mapping, so hand back the file unsplit rather
        # than a header they will crash calling .get() on.
        return {}, text
    return header, parts[2].lstrip("\n")


def write_file(dest: Path, header: dict, body: str) -> None:
    """Writes a brain file as YAML frontmatter followed by the body text,
    creating parent directories as needed."""
    dest.parent.mkdir(parents=True, exist_ok=True)
    front = yaml.safe_dump(header, sort_keys=False).strip()
    dest.write_text(f"---\n{front}\n---\n{body}\n")


def _slug_from_source(source: str) -> str:
    """Turns a URL or file path into a filesystem-safe filename stem, capped at 80 chars.

    A local file slugs from its own name, not its absolute path: slugging the
    full path and truncating to 80 chars made every file under a long directory
    collide on the same stem, so ingesting a second one silently overwrote the
    first.
    """
    if not urlparse(source).scheme in ("http", "https"):
        candidate = Path(source).name or source
        # A dotted filename keeps its stem; the suffix is noise in a slug.
        stem = Path(candidate).stem or candidate
        slug = re.sub(r"[^a-zA-Z0-9]+", "-", stem).strip("-").lower()
        if slug:
            return slug[:80]
    slug = re.sub(r"^https?://", "", source)
    slug = re.sub(r"[^a-zA-Z0-9]+", "-", slug).strip("-").lower()
    return slug[:80] or "untitled"


def _title_from_text(body: str, path: Path) -> str:
    """Uses the first markdown heading as the title, falling back to the stem."""
    for line in body.splitlines():
        stripped = line.strip()
        if stripped.startswith("#"):
            heading = stripped.lstrip("#").strip()
            if heading:
                return heading
        if stripped:
            break
    return path.stem


# The one source of truth for what a local file can be, mapping suffix to
# (extraction kind, folder it routes to). Kept as a table so the "unrecognized
# source" message can list exactly what is accepted -- it used to be a
# hand-written string that had already drifted out of step with the code,
# omitting .rst, .org, .markdown, and .text.
_SUFFIX_KINDS: dict[str, tuple[str, str]] = {
    ".pdf": ("pdf", "papers"),
    ".docx": ("document", "docs"),
    ".doc": ("document", "docs"),
    ".odt": ("document", "docs"),
    # Markdown and plain text need no extraction step, and a notes vault is a
    # documented use for `brain.path` -- rejecting the obvious local file
    # made the brain URL-only in practice.
    ".md": ("text", "docs"),
    ".markdown": ("text", "docs"),
    ".txt": ("text", "docs"),
    ".text": ("text", "docs"),
    ".rst": ("text", "docs"),
    ".org": ("text", "docs"),
    # A saved web page is a local file, not a URL, so it needs its own route.
    ".html": ("html", "blogs"),
    ".htm": ("html", "blogs"),
}


def _supported_suffixes() -> str:
    """The accepted file extensions, for error messages."""
    return ", ".join(sorted(_SUFFIX_KINDS))


def _detect_kind(source: str) -> tuple[str, str]:
    """Returns (kind, routed folder)."""
    parsed = urlparse(source)
    host = parsed.netloc.lower()
    if "youtube.com" in host or "youtu.be" in host:
        if "list=" in source and "watch" not in source:
            return "youtube-playlist", "docs"
        return "youtube", "docs"
    if parsed.scheme in ("http", "https"):
        return "web", "blogs"
    suffix = Path(source).suffix.lower()
    if suffix in _SUFFIX_KINDS:
        return _SUFFIX_KINDS[suffix]
    if not suffix:
        raise IngestError(
            f"unrecognized source: {source} -- give a URL, or a file with one of "
            f"these extensions: {_supported_suffixes()}"
        )
    raise IngestError(
        f"unrecognized source: {source} -- px0 cannot read {suffix} files; "
        f"supported: URLs, YouTube links, and {_supported_suffixes()}"
    )


def _fetch(url: str, timeout: int = 20, **kwargs) -> "requests.Response":
    """GETs a URL, turning every network fault into IngestError.

    Without this, an expired cert, a 404, or a dropped connection escaped as a
    raw traceback. TLS verification honours REQUESTS_CA_BUNDLE, which the CLI
    exports from `connectors.ca_bundle` so intercepting proxies work here the
    same way they already do for Composio.
    """
    headers = {"User-Agent": "px0/0.1", **kwargs.pop("headers", {})}
    try:
        resp = requests.get(url, timeout=timeout, headers=headers, **kwargs)
        resp.raise_for_status()
    except requests.exceptions.SSLError as e:
        raise IngestError(
            f"TLS verification failed for {url}. If your network intercepts TLS, set the "
            f"CA bundle with `px0 config set connectors.ca_bundle /path/to/ca-bundle.pem`. ({e})"
        ) from e
    except requests.exceptions.HTTPError as e:
        code = e.response.status_code if e.response is not None else "?"
        raise IngestError(f"{url} returned HTTP {code}") from e
    except requests.exceptions.Timeout as e:
        raise IngestError(f"{url} timed out after {timeout}s") from e
    except requests.exceptions.RequestException as e:
        raise IngestError(f"could not fetch {url}: {e}") from e
    return resp


def _tidy_inline_spacing(text: str) -> str:
    """Collapses whitespace and closes the gaps left by inline-tag separators.

    Joining a block with a space separator is what keeps a sentence containing
    links on one line, but it also inserts a space either side of every inline
    element -- so a footnote reads "hashing [ 1 ] is" and a clause ends " , ".
    """
    text = " ".join(text.split())
    text = re.sub(r"\s+([,.;:!?%)\]}])", r"\1", text)
    text = re.sub(r"([(\[{])\s+", r"\1", text)
    return text


# Block-level tags whose text forms one paragraph of the extracted document.
_BLOCK_TAGS = ["p", "h1", "h2", "h3", "h4", "h5", "h6",
               "li", "pre", "blockquote", "dd", "dt", "figcaption", "td", "th"]


def _html_to_text(html: str, fallback_title: str) -> tuple[str, str]:
    """Extracts readable text from an HTML document: strips script/style/nav/
    footer/header/aside, prefers <article> or <main> if present.
    Returns (title, text).

    Split out of `_extract_web` so a page saved to disk goes through exactly the
    same reader as one fetched over the network.
    """
    soup = BeautifulSoup(html, "html.parser")
    title = soup.title.string.strip() if soup.title and soup.title.string else fallback_title
    for tag in soup(["script", "style", "nav", "footer", "header", "aside"]):
        tag.decompose()
    main = soup.find("article") or soup.find("main") or soup.body or soup

    # Join within a block, split between blocks. Taking get_text("\n") over the
    # whole subtree instead put every inline element on its own line, so one
    # sentence containing two links arrived as three "paragraphs" -- which reads
    # badly and, worse, chops a sentence across chunk boundaries at index time.
    blocks = []
    for el in main.find_all(_BLOCK_TAGS):
        # Skip a block that merely contains other blocks; its children speak for it.
        if el.find(_BLOCK_TAGS) is not None:
            continue
        line = _tidy_inline_spacing(el.get_text(" ", strip=True))
        if line:
            blocks.append(line)
    if not blocks:
        # No recognisable block structure: fall back to the whole subtree.
        blocks = [
            " ".join(line.split())
            for line in main.get_text("\n").splitlines() if line.strip()
        ]

    deduped, seen = [], set()
    for line in blocks:
        if line not in seen:
            seen.add(line)
            deduped.append(line)
    return title, "\n\n".join(deduped)


def _extract_web(url: str) -> tuple[str, str]:
    """Fetches a web page and extracts its readable text. Returns (title, text)."""
    return _html_to_text(_fetch(url).text, url)


def _extract_local_html(path: Path) -> tuple[str, str]:
    """Reads a saved web page off disk and extracts its readable text."""
    if not path.is_file():
        raise IngestError(f"no such file: {path}")
    return _html_to_text(read_text_lossy(path), path.stem)


def _extract_pdf(path: Path) -> str:
    """Extracts text from a PDF.

    Prefers `pdftotext -layout` when poppler is installed, because it keeps
    multi-column papers readable, and falls back to pypdf otherwise. The
    fallback is what makes `papers/` usable out of the box: requiring poppler
    meant `brain add paper.pdf` failed on a stock machine with nothing but an
    "install poppler-utils" message.
    """
    if not path.is_file():
        raise IngestError(f"no such file: {path}")
    if shutil.which("pdftotext"):
        result = subprocess.run(
            ["pdftotext", "-layout", str(path), "-"], capture_output=True, text=True
        )
        # Fall through to pypdf on failure rather than giving up: pdftotext
        # rejects some PDFs that pypdf reads fine.
        if result.returncode == 0 and result.stdout.strip():
            # pdftotext separates pages with a form feed. Left in, it lands in
            # the stored file as a stray control character and glues the last
            # paragraph of one page to the first of the next for chunking.
            return result.stdout.replace("\f", "\n\n")

    try:
        from pypdf import PdfReader
    except ImportError as e:  # pragma: no cover - pypdf is a declared dependency
        raise IngestError(
            "cannot read PDFs: neither pdftotext (install poppler-utils) nor "
            "pypdf (pip install pypdf) is available"
        ) from e

    try:
        reader = PdfReader(str(path))
        pages = [page.extract_text() or "" for page in reader.pages]
    except Exception as e:
        raise IngestError(f"could not read {path.name} as a PDF: {e}") from e

    text = "\n\n".join(p for p in pages if p.strip())
    if not text.strip():
        raise IngestError(
            f"{path.name} has no extractable text -- it is probably a scan; "
            f"OCR it first, or save a text version"
        )
    return text


# Office/OpenDocument XML namespaces, for the stdlib extraction fallback.
_DOCX_NS = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"
_ODF_TEXT_NS = "{urn:oasis:names:tc:opendocument:xmlns:text:1.0}"


def _extract_zip_xml_document(path: Path) -> str:
    """Pulls the text out of a .docx or .odt with nothing but the stdlib.

    Both formats are zip archives holding one XML document, so paragraph text
    is reachable without pandoc. That matters because pandoc is a large
    external install, and its absence used to make every .docx unreadable.
    """
    import xml.etree.ElementTree as ET
    import zipfile

    suffix = path.suffix.lower()
    if suffix == ".docx":
        member = "word/document.xml"
        para_tags = (f"{_DOCX_NS}p",)
        text_tags = (f"{_DOCX_NS}t",)
    else:
        member = "content.xml"
        para_tags = (f"{_ODF_TEXT_NS}p", f"{_ODF_TEXT_NS}h")
        text_tags = None  # ODF keeps the text inline in the paragraph subtree

    try:
        with zipfile.ZipFile(path) as zf:
            xml_bytes = zf.read(member)
    except (zipfile.BadZipFile, KeyError, OSError) as e:
        raise IngestError(f"could not read {path.name} as {suffix}: {e}") from e

    try:
        root = ET.fromstring(xml_bytes)
    except ET.ParseError as e:
        raise IngestError(f"{path.name} holds malformed XML: {e}") from e

    paragraphs = []
    for para in root.iter():
        if para.tag not in para_tags:
            continue
        if text_tags:
            chunks = [n.text or "" for n in para.iter() if n.tag in text_tags]
        else:
            chunks = list(para.itertext())
        line = "".join(chunks).strip()
        if line:
            paragraphs.append(line)
    return "\n\n".join(paragraphs)


def _extract_document(path: Path) -> str:
    """Extracts plain text from a document (.docx/.doc/.odt).

    Prefers `pandoc` when it is installed, since it renders tables and lists
    better, and otherwise falls back to a stdlib zip+XML reader for .docx/.odt.
    Legacy binary .doc has no stdlib route and still needs pandoc.
    """
    if not path.is_file():
        raise IngestError(f"no such file: {path}")
    suffix = path.suffix.lower()

    if shutil.which("pandoc"):
        result = subprocess.run(
            ["pandoc", str(path), "-t", "plain"], capture_output=True, text=True
        )
        if result.returncode == 0:
            return result.stdout
        if suffix == ".doc":
            raise IngestError(f"pandoc failed: {result.stderr.strip()}")
        # else: fall through to the stdlib reader

    if suffix == ".doc":
        raise IngestError(
            "legacy .doc needs pandoc (install pandoc), or re-save the file as .docx"
        )
    return _extract_zip_xml_document(path)


# Path-style YouTube URLs: the id is the segment after the marker rather than a
# `v=` query parameter. Only `watch?v=` was handled before, so a Shorts link --
# or any URL copied out of an embed or a livestream page -- yielded an empty id,
# fetched no transcript, and silently landed as a metadata-only stub.
_YOUTUBE_PATH_MARKERS = ("shorts", "embed", "live", "v")


def _youtube_id(url: str) -> str:
    """Extracts the 11-char video id from any youtube.com or youtu.be URL shape:
    `watch?v=`, `youtu.be/`, `/shorts/`, `/embed/`, `/live/`, and `/v/`."""
    parsed = urlparse(url)
    if "youtu.be" in parsed.netloc:
        # youtu.be/<id>, possibly with a trailing path (youtu.be/<id>/extra).
        return parsed.path.strip("/").split("/")[0]
    qs = dict(p.split("=", 1) for p in parsed.query.split("&") if "=" in p)
    if qs.get("v"):
        return qs["v"]
    segments = [s for s in parsed.path.split("/") if s]
    for i, seg in enumerate(segments[:-1]):
        if seg in _YOUTUBE_PATH_MARKERS:
            return segments[i + 1]
    return ""


def _youtube_oembed(url: str) -> dict:
    """Fetches YouTube's public oEmbed metadata (title, author) for a video URL,
    no API key required. Returns {} on any failure."""
    try:
        resp = _fetch(
            "https://www.youtube.com/oembed", timeout=10, params={"url": url, "format": "json"}
        )
        if resp.status_code == 200:
            return resp.json()
    except (IngestError, ValueError):
        pass  # title metadata is a nicety; a video with no oembed still ingests
    return {}


def _extract_youtube(url: str) -> tuple[str, str | None, dict]:
    """Returns (title, transcript_text_or_None, metadata).

    A missing transcript is an ordinary outcome -- plenty of videos have none,
    and the caller files those as a stub. A *broken* transcript library is not,
    so AttributeError and ImportError are re-raised as IngestError instead of
    being folded into "no transcript": swallowing everything meant that a
    dependency whose API had shifted under us turned every single video into a
    metadata-only stub, with nothing anywhere saying why.
    """
    video_id = _youtube_id(url)
    meta = _youtube_oembed(url)
    title = meta.get("title", url)
    if not video_id:
        return title, None, meta

    try:
        from youtube_transcript_api import YouTubeTranscriptApi
    except ImportError as e:  # pragma: no cover - a declared dependency
        raise IngestError(f"youtube-transcript-api is not installed: {e}") from e

    api = YouTubeTranscriptApi()
    if not hasattr(api, "fetch"):
        raise IngestError(
            "the installed youtube-transcript-api is too old for px0 "
            "(no .fetch method); upgrade with `pip install -U youtube-transcript-api`"
        )
    try:
        transcript = api.fetch(video_id)
        return title, "\n".join(seg.text for seg in transcript), meta
    except AttributeError as e:
        # The segment shape changed, not "this video has no captions".
        raise IngestError(
            f"youtube-transcript-api returned an unexpected transcript shape: {e}"
        ) from e
    except Exception:
        # Everything else -- no captions, captions disabled, video private,
        # region blocked, a transient network fault -- is a legitimate "no
        # transcript right now", which the caller records as a stub to retry.
        return title, None, meta


# YouTube renders only the first page of a playlist into the HTML; the rest sits
# behind a continuation token fetched by its own JS. Verified against a real
# uploads playlist: 100 ids in the page, a continuation token, and nothing else.
PLAYLIST_FIRST_PAGE_LIMIT = 100


def _enumerate_playlist_ytdlp(url: str) -> list[str] | None:
    """Full playlist enumeration via `yt-dlp`, or None if it is not usable.

    YouTube renders only the first page of a playlist into the HTML and loads
    the rest through a continuation API whose token a plain GET does not get.
    Rather than reverse-engineer that, use the tool that tracks it for a living
    when the user happens to have it -- the same "external tool when present"
    arrangement as pdftotext and pandoc.

    Note that yt-dlp verifies TLS against its own bundled certifi and offers no
    CA-bundle option, so it cannot be pointed at `connectors.ca_bundle`. On a
    network that intercepts TLS it fails outright, and this returns None so the
    scrape still runs. Forcing it with --no-check-certificates would trade a
    partial playlist for unverified HTTPS, which is not a trade worth making.
    """
    if not shutil.which("yt-dlp"):
        return None
    try:
        result = subprocess.run(
            ["yt-dlp", "--flat-playlist", "--print", "%(id)s",
             "--no-warnings", "--ignore-config", url],
            capture_output=True, text=True, timeout=180,
        )
    except (OSError, subprocess.TimeoutExpired):
        return None
    if result.returncode != 0:
        return None
    ids = [line.strip() for line in result.stdout.splitlines() if line.strip()]
    if not ids:
        return None
    seen, ordered = set(), []
    for vid in ids:
        if vid not in seen:
            seen.add(vid)
            ordered.append(vid)
    return [f"https://www.youtube.com/watch?v={v}" for v in ordered]


def enumerate_playlist(url: str) -> list[str]:
    """Returns the playlist's video watch URLs, in order, deduplicated.

    Prefers `yt-dlp` when it is installed, which returns the whole playlist.
    Without it, this falls back to scraping the playlist page, which YouTube
    only renders the first `PLAYLIST_FIRST_PAGE_LIMIT` videos into -- so a long
    playlist comes back partial. Callers report that as a truncated result
    rather than as a fully drained playlist.
    """
    via_ytdlp = _enumerate_playlist_ytdlp(url)
    if via_ytdlp is not None:
        return via_ytdlp

    resp = _fetch(url)
    ids = re.findall(r'"videoId":"([a-zA-Z0-9_-]{11})"', resp.text)
    seen, ordered = set(), []
    for vid in ids:
        if vid not in seen:
            seen.add(vid)
            ordered.append(vid)
    return [f"https://www.youtube.com/watch?v={v}" for v in ordered]


# The folders px0 routes into by default. Not a closed set -- `--to` takes any
# relative path -- but these are what the suffix table maps onto, and what the
# CLI suggests.
DEFAULT_FOLDERS = ("docs", "blogs", "papers", "work")


def resolve_folder(home: Path, config: dict, folder: str) -> str:
    """Validates a destination folder and returns it normalised.

    `--to` accepts any relative subfolder, not just the four px0 routes into by
    default, so a brain pointed at someone's own vault can file into the
    structure that vault already has (`--to "Personal/Reading"`).

    Anything that would land outside the brain is refused: an absolute path, or
    one that climbs out with `..`. Resolution is textual rather than via
    `Path.resolve()` so a folder that does not exist yet still validates, and a
    symlinked brain root is not mistaken for an escape.
    """
    raw = (folder or "").strip()
    if not raw:
        raise IngestError("--to needs a folder name")
    candidate = Path(raw)
    if candidate.is_absolute() or candidate.drive or raw.startswith("~"):
        raise IngestError(
            f"--to takes a folder inside the brain, not an absolute path: {folder}"
        )
    parts = [p for p in candidate.parts if p not in (".",)]
    if any(p == ".." for p in parts):
        raise IngestError(f"--to must stay inside the brain: {folder}")
    if not parts:
        raise IngestError("--to needs a folder name")
    return "/".join(parts)


def _dest_path(home: Path, config: dict, folder: str, source: str) -> Path:
    """Resolves the destination path for an ingested source under brain/<folder>/."""
    base = brain_path(home, config)
    dest = base / resolve_folder(home, config, folder) / f"{_slug_from_source(source)}.md"
    # Belt and braces against a folder name that slips past the textual checks
    # on some platform: the file must end up under the brain root.
    if not str(dest.parent).startswith(str(base)):
        raise IngestError(f"--to must stay inside the brain: {folder}")
    return dest


def add(
    home: Path, config: dict, source: str, to: str | None = None,
    no_propose: bool = False, reindex: bool = True,
) -> IngestResult:
    """Ingests one source into the brain: detects its kind, extracts
    text (or queues a playlist for background processing), writes the brain
    file, best-effort proposes guideline edits from it (unless no_propose), and
    reindexes retrieval. A YouTube video with no published transcript is written
    as a stub rather than failing.

    `reindex=False` skips the index rebuild, leaving it to the caller. Reindexing
    rewrites the whole table, so doing it per file made a batch quadratic in the
    size of the library: draining a 100-video playlist into an established brain
    spent almost all its time rebuilding an index it was about to discard.
    """
    kind, default_folder = _detect_kind(source)
    folder = to or default_folder
    today = date.today().isoformat()

    if kind == "youtube-playlist":
        job_path = paths.ingest_dir(home) / f"{_slug_from_source(source)}.json"
        job_path.parent.mkdir(parents=True, exist_ok=True)
        job_path.write_text(json.dumps({"source": source, "kind": "youtube-playlist",
                                         "to": to, "queued_at": today}, indent=2))
        hint = ""
        if not shutil.which("yt-dlp"):
            # Otherwise the first-page cap looks like px0 losing videos.
            hint = (f"; only the first ~{PLAYLIST_FIRST_PAGE_LIMIT} videos are "
                    f"reachable without yt-dlp installed")
        raise IngestError(
            f"playlist queued at {job_path}; run `px0 daemon start` to process it "
            f"in the background, or ingest individual video URLs directly{hint}"
        )

    if kind == "web":
        title, body = _extract_web(source)
        header = {"source": source, "retrieved": today, "kind": "blog", "title": title}
        dest = _dest_path(home, config, folder, source)
        write_file(dest, header, body)
        is_stub = False
    elif kind == "pdf":
        body = _extract_pdf(Path(source).expanduser())
        header = {"source": str(Path(source).expanduser()), "retrieved": today,
                   "kind": "paper", "title": Path(source).stem}
        dest = _dest_path(home, config, folder, source)
        write_file(dest, header, body)
        is_stub = False
    elif kind == "html":
        src_path = Path(source).expanduser()
        title, body = _extract_local_html(src_path)
        header = {"source": str(src_path), "retrieved": today,
                  "kind": "blog", "title": title}
        dest = _dest_path(home, config, folder, source)
        write_file(dest, header, body)
        is_stub = False
    elif kind == "text":
        src_path = Path(source).expanduser()
        if not src_path.exists():
            raise IngestError(f"no such file: {src_path}")
        try:
            # Lossy, not strict: a note saved in another encoding should still
            # land in the brain with its readable characters intact rather than
            # being refused outright over a handful of stray bytes.
            body = read_text_lossy(src_path)
        except OSError as e:
            raise IngestError(f"could not read {src_path}: {e}") from e
        header = {"source": str(src_path), "retrieved": today,
                  "kind": "doc", "title": _title_from_text(body, src_path)}
        dest = _dest_path(home, config, folder, source)
        write_file(dest, header, body)
        is_stub = False
    elif kind == "document":
        body = _extract_document(Path(source).expanduser())
        header = {"source": str(Path(source).expanduser()), "retrieved": today,
                   "kind": "doc", "title": Path(source).stem}
        dest = _dest_path(home, config, folder, source)
        write_file(dest, header, body)
        is_stub = False
    elif kind == "youtube":
        title, transcript, meta = _extract_youtube(source)
        dest = _dest_path(home, config, folder, source)
        if transcript:
            header = {"source": source, "retrieved": today, "kind": "video", "title": title}
            write_file(dest, header, transcript)
            is_stub = False
        else:
            header = {"source": source, "retrieved": today, "kind": "stub",
                       "title": title, "channel": meta.get("author_name"),
                       "note": "metadata only; no published transcript"}
            write_file(dest, header, f"# {title}\n\nNo transcript is available for this video yet. "
                                      f"Run `px0 brain refresh {dest}` later to check again.")
            is_stub = True
    else:
        raise IngestError(f"unhandled kind: {kind}")

    if not is_stub and not no_propose:
        try:
            from px0 import proposals as proposals_mod
            proposals_mod.propose_from_brain(home, config, dest)
        except Exception:
            pass  # proposal pass is best-effort; ingestion itself already succeeded

    if reindex:
        retrieval.reindex(home, config)
    return IngestResult(dest, folder, is_stub)


def resolve_brain_path(home: Path, config: dict, path: str | Path) -> Path:
    """Resolves a user-supplied brain path to a real file.

    Accepts what the user is likely to have in hand: an absolute path, a
    store-relative one (`brain/blogs/x.md`, the form the docs use), a
    library-relative one (`blogs/x.md`, the form `px0 brain list` prints),
    or a bare filename. Previously only a path relative to the current working
    directory worked, so neither the listed nor the documented form did.
    """
    raw = Path(path).expanduser()
    base = brain_path(home, config)
    candidates = [raw] if raw.is_absolute() else [
        Path.cwd() / raw,
        base / raw,
        home / raw,
    ]
    for candidate in candidates:
        if candidate.is_file():
            return candidate
    # Last resort: a bare name, matched anywhere in the library. Directories are
    # excluded -- `brain refresh docs` used to resolve to the folder itself and
    # then fail deep inside with IsADirectoryError instead of a usable message.
    matches = sorted(p for p in base.rglob(raw.name) if p.is_file()) if raw.name else []
    if len(matches) == 1:
        return matches[0]
    if len(matches) > 1:
        rels = ", ".join(str(m.relative_to(base)) for m in matches[:5])
        raise IngestError(f"{raw.name} is ambiguous -- matches {rels}")
    raise IngestError(f"no brain file at {path} (see `px0 brain list`)")


def refresh(
    home: Path, config: dict, path: Path, no_propose: bool = False, reindex: bool = True,
) -> IngestResult:
    """Re-fetches an already-ingested source and rewrites the file in place.

    Handles each kind the library holds: a YouTube stub retries transcript
    extraction, a web page is fetched again, and a local file is re-read. Only
    stubs used to be supported, which made the command reject every other file
    with "is not a stub" despite advertising a re-fetch.
    """
    path = resolve_brain_path(home, config, path)
    header, body = read_header(path)
    source = header.get("source")
    if not source:
        raise IngestError(f"{path} records no source to re-fetch")
    today = date.today().isoformat()

    if header.get("kind") == "stub":
        title, transcript, meta = _extract_youtube(source)
        if not transcript:
            raise IngestError(f"still no transcript for {source}")
        new_header = {"source": source, "retrieved": today,
                      "kind": "video", "title": title}
        write_file(path, new_header, transcript)
    else:
        kind, _ = _detect_kind(source)
        if kind == "web":
            title, new_body = _extract_web(source)
            new_header = {"source": source, "retrieved": today,
                          "kind": header.get("kind", "blog"), "title": title}
        elif kind == "youtube":
            title, transcript, meta = _extract_youtube(source)
            if not transcript:
                raise IngestError(f"no transcript published for {source}")
            new_header = {"source": source, "retrieved": today,
                          "kind": "video", "title": title}
            new_body = transcript
        elif kind == "html":
            src_path = Path(source).expanduser()
            title, new_body = _extract_local_html(src_path)
            new_header = {"source": source, "retrieved": today,
                          "kind": header.get("kind", "blog"), "title": title}
        elif kind == "text":
            src_path = Path(source).expanduser()
            if not src_path.is_file():
                raise IngestError(f"original file is gone: {src_path}")
            new_body = read_text_lossy(src_path)
            new_header = {"source": source, "retrieved": today,
                          "kind": header.get("kind", "doc"),
                          "title": _title_from_text(new_body, src_path)}
        elif kind == "pdf":
            new_body = _extract_pdf(Path(source).expanduser())
            new_header = {"source": source, "retrieved": today,
                          "kind": "paper", "title": header.get("title", Path(source).stem)}
        elif kind == "document":
            new_body = _extract_document(Path(source).expanduser())
            new_header = {"source": source, "retrieved": today,
                          "kind": "doc", "title": header.get("title", Path(source).stem)}
        else:
            raise IngestError(f"cannot re-fetch a {kind} source")
        write_file(path, new_header, new_body)
    # Same opt-out as `add`: refreshing used to fire a model call every time
    # with no way to decline, which made re-fetching a page cost a round trip
    # to the harness whether or not anything new was worth proposing.
    if not no_propose:
        try:
            from px0 import proposals as proposals_mod
            proposals_mod.propose_from_brain(home, config, path)
        except Exception:
            pass
    if reindex:
        retrieval.reindex(home, config)
    return IngestResult(path, path.parent.name, False)


MAX_INGEST_ATTEMPTS = 3


def process_ingest_queue(home: Path, config: dict) -> dict:
    """Processes any queued YouTube playlist ingest jobs under .state/ingest/."""
    import json

    ingest_dir = paths.ingest_dir(home)
    failed_dir = paths.ingest_failed_dir(home)

    jobs_processed = 0
    videos_ingested = 0
    jobs_given_up = 0
    playlists_truncated = 0

    job_paths = sorted(ingest_dir.glob("*.json"))

    for jp in job_paths:
        if not jp.is_file():
            continue

        try:
            job = json.loads(jp.read_text())
        except Exception:
            continue

        source = job.get("source")
        folder = job.get("to") or "docs"
        attempts = job.get("attempts", 0)

        failures = []
        try:
            urls = enumerate_playlist(source)
            if not urls:
                # An empty enumeration is a failure, not a finished job. It means
                # the page shape changed, the playlist is private, or the fetch
                # was rate-limited -- treating it as success deleted the job and
                # made the playlist disappear without a word.
                raise IngestError(
                    f"no videos found in {source}; the playlist may be private, "
                    f"empty, or the fetch was blocked"
                )
            if len(urls) >= PLAYLIST_FIRST_PAGE_LIMIT:
                # Reported, not hidden: a playlist cut off at the first page has
                # not been fully ingested, and "job done" would say otherwise.
                playlists_truncated += 1
            for url in urls:
                dest = _dest_path(home, config, folder, url)
                if dest.exists():
                    continue  # Idempotent skip
                try:
                    # One rebuild for the whole batch, below -- not one per video.
                    add(home, config, url, to=folder, reindex=False)
                    videos_ingested += 1
                except IngestError as ie:
                    failures.append(f"{url}: {ie}")
                except Exception as e:
                    failures.append(f"{url}: {e}")
        except Exception as e:
            failures.append(f"Playlist enumeration failed: {e}")

        jobs_processed += 1
        if not failures:
            try:
                jp.unlink()
            except OSError:
                pass
        else:
            attempts += 1
            last_error = f"{len(failures)} item(s) failed: " + "; ".join(failures)
            if attempts >= MAX_INGEST_ATTEMPTS:
                failed_dir.mkdir(parents=True, exist_ok=True)
                failed_jp = failed_dir / jp.name
                job["attempts"] = attempts
                job["last_error"] = last_error
                try:
                    failed_jp.write_text(json.dumps(job, indent=2))
                    jp.unlink()
                except OSError:
                    pass
                jobs_given_up += 1
            else:
                job["attempts"] = attempts
                job["last_error"] = last_error
                try:
                    jp.write_text(json.dumps(job, indent=2))
                except OSError:
                    pass

    if videos_ingested:
        retrieval.reindex(home, config)

    return {
        "jobs_processed": jobs_processed,
        "videos_ingested": videos_ingested,
        "jobs_given_up": jobs_given_up,
        "playlists_truncated": playlists_truncated,
    }


def list_files(home: Path, config: dict) -> list[Path]:
    """Every brain file retrieval would consider, ignore patterns applied.

    Listing raw `rglob` output misreports a brain pointed at a vault: most of
    what it finds is the notes app's own state.
    """
    base = brain_path(home, config)
    if not base.exists():
        return []
    globs = retrieval.ignore_globs(config)
    return [p for p in sorted(base.rglob("*.md"))
            if p.is_file() and not retrieval.is_ignored(str(p.relative_to(base)), globs)]


def private_folder(config: dict) -> str:
    """The brain subfolder held back from retrieval, per config."""
    return retrieval.private_folder(config)


def remove(home: Path, config: dict, path: str | Path, reindex: bool = True) -> dict:
    """Deletes one brain file and drops its passages from the index.

    Deleting the file by hand works too, right up until you search: the
    passages stay in the index until something rebuilds it. This does both, and
    reports what it removed so the caller can say so.
    """
    target = resolve_brain_path(home, config, path)
    base = brain_path(home, config)
    header, _body = ({}, "")
    try:
        header, _body = read_header(target)
    except Exception:
        pass  # a file px0 did not write has no frontmatter; it can still be removed
    rel = str(target.relative_to(base)) if target.is_relative_to(base) else str(target)
    target.unlink()
    passages = None
    if reindex:
        from px0 import retrieval

        try:
            passages = retrieval.reindex(home, config)
        except Exception:
            passages = None
    return {"path": rel, "kind": header.get("kind"), "source": header.get("source"),
            "reindexed": passages}


def show(home: Path, config: dict, path: str | Path) -> dict:
    """One brain file: its frontmatter, its body, and where it came from."""
    target = resolve_brain_path(home, config, path)
    base = brain_path(home, config)
    try:
        header, body = read_header(target)
    except Exception:
        header, body = {}, read_text_lossy(target)
    rel = str(target.relative_to(base)) if target.is_relative_to(base) else str(target)
    private = private_folder(config)
    return {
        "path": rel,
        "absolute": str(target),
        "header": header,
        "body": body,
        "private": bool(private) and rel.split("/", 1)[0] == private,
        "bytes": target.stat().st_size,
    }


def stale(home: Path, config: dict, days: int = 30) -> list[Path]:
    """Brain files whose `retrieved` date is older than `days`, plus every stub.

    A stub is a YouTube video whose transcript was not published yet, so it is
    always worth another try regardless of age.
    """
    from datetime import timedelta

    cutoff = date.today() - timedelta(days=max(0, days))
    out = []
    for path in list_files(home, config):
        try:
            header, _ = read_header(path)
        except Exception:
            continue
        if not header.get("source"):
            continue  # nothing to re-fetch
        if header.get("kind") == "stub":
            out.append(path)
            continue
        retrieved = header.get("retrieved")
        try:
            when = date.fromisoformat(str(retrieved))
        except (TypeError, ValueError):
            out.append(path)  # undated: treat as stale rather than never refreshing it
            continue
        if when < cutoff:
            out.append(path)
    return out


def refresh_many(home: Path, config: dict, targets: list[Path], no_propose: bool = True) -> dict:
    """Re-fetches several files, reindexing once at the end.

    Reindexing rewrites the whole table, so doing it per file makes a batch
    quadratic in the size of the library -- the same reason `add` takes a
    reindex flag.
    """
    done, failed = [], []
    for path in targets:
        try:
            refresh(home, config, path, no_propose=no_propose, reindex=False)
            done.append(str(path))
        except Exception as e:
            failed.append({"path": str(path), "error": str(e)})
    passages = None
    if done:
        from px0 import retrieval

        try:
            passages = retrieval.reindex(home, config)
        except Exception:
            passages = None
    return {"refreshed": done, "failed": failed, "reindexed": passages}


def add_many(home: Path, config: dict, sources: list[str], to: str | None = None,
             no_propose: bool = True) -> dict:
    """Ingests several sources, reindexing once at the end.

    A reading backlog is a list, not one URL at a time.
    """
    added, failed = [], []
    for source in sources:
        try:
            result = add(home, config, source, to=to, no_propose=no_propose, reindex=False)
            added.append(result)
        except Exception as e:
            failed.append({"source": source, "error": str(e)})
    passages = None
    if added:
        from px0 import retrieval

        try:
            passages = retrieval.reindex(home, config)
        except Exception:
            passages = None
    return {"added": added, "failed": failed, "reindexed": passages}


def read_sources(path: Path) -> list[str]:
    """Reads a list of sources from a text file: one per line, # comments ignored."""
    lines = []
    for raw in Path(path).expanduser().read_text().splitlines():
        line = raw.strip()
        if line and not line.startswith("#"):
            lines.append(line)
    return lines


def export_library(home: Path, config: dict, dest: Path, include_private: bool = False) -> dict:
    """Copies the brain to `dest`, keeping its folder structure.

    The private folder is held back unless asked for, because that folder's
    whole promise is that it does not leave the machine by accident.
    """
    import shutil

    base = brain_path(home, config)
    dest = Path(dest).expanduser()
    private = private_folder(config)
    copied, held = 0, 0
    for path in list_files(home, config):
        rel = path.relative_to(base)
        if private and rel.parts and rel.parts[0] == private and not include_private:
            held += 1
            continue
        target = dest / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, target)
        copied += 1
    return {"dest": str(dest), "copied": copied, "held_back": held}
