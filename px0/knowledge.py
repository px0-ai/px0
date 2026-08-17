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
    pass


@dataclass
class IngestResult:
    path: Path
    kind: str  # docs | blogs | papers
    is_stub: bool


def read_header(path: Path) -> tuple[dict, str]:
    text = path.read_text()
    if not text.startswith("---"):
        return {}, text
    parts = text.split("---", 2)
    if len(parts) < 3:
        return {}, text
    header = yaml.safe_load(parts[1]) or {}
    return header, parts[2].lstrip("\n")


def write_file(dest: Path, header: dict, body: str) -> None:
    dest.parent.mkdir(parents=True, exist_ok=True)
    front = yaml.safe_dump(header, sort_keys=False).strip()
    dest.write_text(f"---\n{front}\n---\n{body}\n")


def _slug_from_source(source: str) -> str:
    slug = re.sub(r"^https?://", "", source)
    slug = re.sub(r"[^a-zA-Z0-9]+", "-", slug).strip("-").lower()
    return slug[:80] or "untitled"


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
    raise IngestError(f"unrecognized source: {source}")


def _extract_web(url: str) -> tuple[str, str]:
    resp = requests.get(url, timeout=20, headers={"User-Agent": "px0/0.1"})
    resp.raise_for_status()
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
    if not shutil.which("pdftotext"):
        raise IngestError("pdftotext not found; install poppler-utils")
    result = subprocess.run(
        ["pdftotext", "-layout", str(path), "-"], capture_output=True, text=True
    )
    if result.returncode != 0:
        raise IngestError(f"pdftotext failed: {result.stderr.strip()}")
    return result.stdout


def _extract_document(path: Path) -> str:
    if not shutil.which("pandoc"):
        raise IngestError("pandoc not found; install pandoc")
    result = subprocess.run(
        ["pandoc", str(path), "-t", "plain"], capture_output=True, text=True
    )
    if result.returncode != 0:
        raise IngestError(f"pandoc failed: {result.stderr.strip()}")
    return result.stdout


def _youtube_id(url: str) -> str:
    parsed = urlparse(url)
    if "youtu.be" in parsed.netloc:
        return parsed.path.strip("/")
    qs = dict(p.split("=", 1) for p in parsed.query.split("&") if "=" in p)
    return qs.get("v", "")


def _youtube_oembed(url: str) -> dict:
    try:
        resp = requests.get(
            "https://www.youtube.com/oembed", params={"url": url, "format": "json"}, timeout=10
        )
        if resp.status_code == 200:
            return resp.json()
    except requests.RequestException:
        pass
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
    resp = requests.get(url, timeout=20, headers={"User-Agent": "px0/0.1"})
    resp.raise_for_status()
    ids = re.findall(r'"videoId":"([a-zA-Z0-9_-]{11})"', resp.text)
    seen, ordered = set(), []
    for vid in ids:
        if vid not in seen:
            seen.add(vid)
            ordered.append(vid)
    return [f"https://www.youtube.com/watch?v={v}" for v in ordered]


def _dest_path(home: Path, config: dict, folder: str, source: str) -> Path:
    base = knowledge_path(home, config)
    return base / folder / f"{_slug_from_source(source)}.md"


def add(
    home: Path, config: dict, source: str, to: str | None = None, no_propose: bool = False
) -> IngestResult:
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


def refresh(home: Path, config: dict, path: Path) -> IngestResult:
    header, body = read_header(path)
    if header.get("kind") != "stub":
        raise IngestError(f"{path} is not a stub")
    source = header["source"]
    title, transcript, meta = _extract_youtube(source)
    if not transcript:
        raise IngestError(f"still no transcript for {source}")
    new_header = {"source": source, "retrieved": date.today().isoformat(),
                  "kind": "video", "title": title}
    write_file(path, new_header, transcript)
    try:
        from px0 import proposals as proposals_mod
        proposals_mod.propose_from_knowledge(home, config, path)
    except Exception:
        pass
    retrieval.reindex(home, config)
    return IngestResult(path, path.parent.name, False)
