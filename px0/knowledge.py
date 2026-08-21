"""px0 knowledge add: filing outside material into the library.

Ingestion is text only. Extraction always runs locally: web pages via
requests + BeautifulSoup, PDFs via `pdftotext`, documents via `pandoc`,
YouTube via `youtube-transcript-api` (no API key needed) with an oEmbed
metadata fallback when no transcript is published.
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
from px0.retrieval import knowledge_path


class IngestError(Exception):
    """A knowledge source could not be ingested (unrecognized, extraction tool
    missing, extraction failed, etc.)."""


@dataclass
class IngestResult:
    """Where an ingested (or refreshed) source landed, and whether it's still
    a stub awaiting a transcript."""
    path: Path
    kind: str  # docs | blogs | papers
    is_stub: bool


def read_header(path: Path) -> tuple[dict, str]:
    """Splits a knowledge file into its YAML frontmatter dict and body text.
    Returns ({}, full_text) if the file has no frontmatter block."""
    text = path.read_text()
    if not text.startswith("---"):
        return {}, text
    parts = text.split("---", 2)
    if len(parts) < 3:
        return {}, text
    header = yaml.safe_load(parts[1]) or {}
    return header, parts[2].lstrip("\n")


def write_file(dest: Path, header: dict, body: str) -> None:
    """Writes a knowledge file as YAML frontmatter followed by the body text,
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
    if suffix == ".pdf":
        return "pdf", "papers"
    if suffix in (".docx", ".doc", ".odt"):
        return "document", "docs"
    # Markdown and plain text need no extraction step, and a notes vault is a
    # documented use for `knowledge.path` -- rejecting the obvious local file
    # made the library URL-only in practice.
    if suffix in (".md", ".markdown", ".txt", ".text", ".rst", ".org"):
        return "text", "docs"
    if not urlparse(source).scheme and not suffix:
        raise IngestError(
            f"unrecognized source: {source} -- give a URL, or a file ending in "
            ".md, .txt, .pdf, .docx, or .odt"
        )
    raise IngestError(
        f"unrecognized source: {source} -- supported: URLs, YouTube links, and "
        ".md/.txt/.rst/.pdf/.docx/.odt files"
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


def _extract_web(url: str) -> tuple[str, str]:
    """Fetches a web page and extracts its readable text: strips script/style/nav/
    footer/header/aside, prefers <article> or <main> if present. Returns (title, text)."""
    resp = _fetch(url)
    soup = BeautifulSoup(resp.text, "html.parser")
    title = soup.title.string.strip() if soup.title and soup.title.string else url
    for tag in soup(["script", "style", "nav", "footer", "header", "aside"]):
        tag.decompose()
    main = soup.find("article") or soup.find("main") or soup.body or soup
    text = "\n\n".join(
        line.strip() for line in main.get_text("\n").splitlines() if line.strip()
    )
    return title, text


def _extract_pdf(path: Path) -> str:
    """Extracts text from a PDF via the `pdftotext` CLI. Raises IngestError if the
    tool is missing or exits non-zero."""
    if not shutil.which("pdftotext"):
        raise IngestError("pdftotext not found; install poppler-utils")
    result = subprocess.run(
        ["pdftotext", "-layout", str(path), "-"], capture_output=True, text=True
    )
    if result.returncode != 0:
        raise IngestError(f"pdftotext failed: {result.stderr.strip()}")
    return result.stdout


def _extract_document(path: Path) -> str:
    """Extracts plain text from a document (.docx/.doc/.odt) via the `pandoc` CLI.
    Raises IngestError if the tool is missing or exits non-zero."""
    if not shutil.which("pandoc"):
        raise IngestError("pandoc not found; install pandoc")
    result = subprocess.run(
        ["pandoc", str(path), "-t", "plain"], capture_output=True, text=True
    )
    if result.returncode != 0:
        raise IngestError(f"pandoc failed: {result.stderr.strip()}")
    return result.stdout


def _youtube_id(url: str) -> str:
    """Extracts the 11-char video id from a youtube.com or youtu.be URL."""
    parsed = urlparse(url)
    if "youtu.be" in parsed.netloc:
        return parsed.path.strip("/")
    qs = dict(p.split("=", 1) for p in parsed.query.split("&") if "=" in p)
    return qs.get("v", "")


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
    """Returns (title, transcript_text_or_None, metadata)."""
    video_id = _youtube_id(url)
    meta = _youtube_oembed(url)
    title = meta.get("title", url)
    transcript_text = None
    try:
        from youtube_transcript_api import YouTubeTranscriptApi
        transcript = YouTubeTranscriptApi().fetch(video_id)
        transcript_text = "\n".join(seg.text for seg in transcript)
    except Exception:
        transcript_text = None
    return title, transcript_text, meta


def enumerate_playlist(url: str) -> list[str]:
    """Scrapes a YouTube playlist page's HTML for video ids and returns their
    watch URLs in playlist order, deduplicated."""
    resp = _fetch(url)
    ids = re.findall(r'"videoId":"([a-zA-Z0-9_-]{11})"', resp.text)
    seen, ordered = set(), []
    for vid in ids:
        if vid not in seen:
            seen.add(vid)
            ordered.append(vid)
    return [f"https://www.youtube.com/watch?v={v}" for v in ordered]


def _dest_path(home: Path, config: dict, folder: str, source: str) -> Path:
    """Resolves the destination path for an ingested source under knowledge/<folder>/."""
    base = knowledge_path(home, config)
    return base / folder / f"{_slug_from_source(source)}.md"


def add(
    home: Path, config: dict, source: str, to: str | None = None, no_propose: bool = False
) -> IngestResult:
    """Ingests one source into the knowledge library: detects its kind, extracts
    text (or queues a playlist for background processing), writes the knowledge
    file, best-effort proposes guideline edits from it (unless no_propose), and
    reindexes retrieval. A YouTube video with no published transcript is written
    as a stub rather than failing."""
    kind, default_folder = _detect_kind(source)
    folder = to or default_folder
    today = date.today().isoformat()

    if kind == "youtube-playlist":
        job_path = paths.ingest_dir(home) / f"{_slug_from_source(source)}.json"
        job_path.parent.mkdir(parents=True, exist_ok=True)
        job_path.write_text(json.dumps({"source": source, "kind": "youtube-playlist",
                                         "to": to, "queued_at": today}, indent=2))
        raise IngestError(
            f"playlist queued at {job_path}; run `px0 daemon start` to process it "
            f"in the background, or ingest individual video URLs directly"
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
    elif kind == "text":
        src_path = Path(source).expanduser()
        if not src_path.exists():
            raise IngestError(f"no such file: {src_path}")
        try:
            body = src_path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as e:
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
                                      f"Run `px0 knowledge refresh {dest}` later to check again.")
            is_stub = True
    else:
        raise IngestError(f"unhandled kind: {kind}")

    if not is_stub and not no_propose:
        try:
            from px0 import proposals as proposals_mod
            proposals_mod.propose_from_knowledge(home, config, dest)
        except Exception:
            pass  # proposal pass is best-effort; ingestion itself already succeeded

    retrieval.reindex(home, config)
    return IngestResult(dest, folder, is_stub)


def resolve_knowledge_path(home: Path, config: dict, path: str | Path) -> Path:
    """Resolves a user-supplied knowledge path to a real file.

    Accepts what the user is likely to have in hand: an absolute path, a
    store-relative one (`knowledge/blogs/x.md`, the form the docs use), a
    library-relative one (`blogs/x.md`, the form `px0 knowledge list` prints),
    or a bare filename. Previously only a path relative to the current working
    directory worked, so neither the listed nor the documented form did.
    """
    raw = Path(path).expanduser()
    base = knowledge_path(home, config)
    candidates = [raw] if raw.is_absolute() else [
        Path.cwd() / raw,
        base / raw,
        home / raw,
    ]
    for candidate in candidates:
        if candidate.is_file():
            return candidate
    # Last resort: a bare name, matched anywhere in the library.
    matches = sorted(base.rglob(raw.name)) if raw.name else []
    if len(matches) == 1:
        return matches[0]
    if len(matches) > 1:
        rels = ", ".join(str(m.relative_to(base)) for m in matches[:5])
        raise IngestError(f"{raw.name} is ambiguous -- matches {rels}")
    raise IngestError(f"no knowledge file at {path} (see `px0 knowledge list`)")


def refresh(home: Path, config: dict, path: Path) -> IngestResult:
    """Re-fetches an already-ingested source and rewrites the file in place.

    Handles each kind the library holds: a YouTube stub retries transcript
    extraction, a web page is fetched again, and a local file is re-read. Only
    stubs used to be supported, which made the command reject every other file
    with "is not a stub" despite advertising a re-fetch.
    """
    path = resolve_knowledge_path(home, config, path)
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
        elif kind == "text":
            src_path = Path(source).expanduser()
            if not src_path.is_file():
                raise IngestError(f"original file is gone: {src_path}")
            new_body = src_path.read_text(encoding="utf-8")
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
    try:
        from px0 import proposals as proposals_mod
        proposals_mod.propose_from_knowledge(home, config, path)
    except Exception:
        pass
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
            for url in urls:
                dest = _dest_path(home, config, folder, url)
                if dest.exists():
                    continue  # Idempotent skip
                try:
                    add(home, config, url, to=folder)
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

    return {
        "jobs_processed": jobs_processed,
        "videos_ingested": videos_ingested,
        "jobs_given_up": jobs_given_up,
    }
