"""The `retrieve` interface over `knowledge/`: query, k, filters in;
ranked passages with file path and anchor out. Guidelines are never
retrieved by similarity -- only knowledge.

Backend: SQLite FTS5 with BM25 ranking, embedded, no server. This is a
pure-python-reachable subset of what the spec asks of qmd (hybrid keyword
+ vector search, rerank): only the keyword/BM25 half is implemented here,
since the vector and rerank stages need GGUF embedding models the spec
gates behind explicit, printed-size consent. `[retrieval] backend` names
this the "local" backend so a real qmd integration can be swapped in later
behind this same function signature.
"""

import re
import sqlite3
from dataclasses import dataclass
from pathlib import Path

from px0 import config as config_mod
from px0 import paths


@dataclass
class Passage:
    path: str
    anchor: str
    text: str
    score: float
    ingested_at: str | None
    is_stub: bool


def knowledge_path(home: Path, config: dict) -> Path:
    configured = config_mod.get(config, "knowledge.path", "~/.px0/knowledge")
    return Path(configured).expanduser()


def index_db_path(home: Path) -> Path:
    return paths.index_dir(home) / "index.sqlite"


def _connect(home: Path) -> sqlite3.Connection:
    paths.index_dir(home).mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(index_db_path(home))
    conn.execute(
        "CREATE VIRTUAL TABLE IF NOT EXISTS passages USING fts5("
        "text, path UNINDEXED, anchor UNINDEXED, ingested_at UNINDEXED, "
        "is_stub UNINDEXED, is_work UNINDEXED)"
    )
    return conn


_CHUNK_TARGET_CHARS = 1000


def _chunk_by_paragraph(text: str) -> list[tuple[str, str]]:
    """Fallback for material with no Markdown headings (extracted web
    pages, transcripts): group paragraphs into ~1000-char chunks."""
    paragraphs = [p for p in re.split(r"\n\s*\n", text) if p.strip()]
    chunks: list[tuple[str, str]] = []
    buf: list[str] = []
    buf_len = 0
    for p in paragraphs:
        buf.append(p)
        buf_len += len(p)
        if buf_len >= _CHUNK_TARGET_CHARS:
            chunks.append((f"p{len(chunks) + 1}", "\n\n".join(buf)))
            buf, buf_len = [], 0
    if buf:
        chunks.append((f"p{len(chunks) + 1}", "\n\n".join(buf)))
    return chunks or [("", text)]


def _chunk_file(text: str) -> list[tuple[str, str]]:
    """Split a knowledge file's body into (anchor, text) chunks by heading,
    falling back to paragraph grouping when there are no headings."""
    heading_re = re.compile(r"^(#{1,6})\s+(.*?)\s*$", re.MULTILINE)
    matches = list(heading_re.finditer(text))
    if not matches:
        return _chunk_by_paragraph(text)
    chunks = []
    for i, m in enumerate(matches):
        start = m.start()
        end = matches[i + 1].start() if i + 1 < len(matches) else len(text)
        anchor = re.sub(r"[^a-z0-9\s-]", "", m.group(2).lower())
        anchor = re.sub(r"\s+", "-", anchor).strip("-")
        chunks.append((anchor, text[start:end]))
    return chunks


def reindex(home: Path, config: dict) -> int:
    from px0 import knowledge as knowledge_mod  # avoid import cycle

    base = knowledge_path(home, config)
    conn = _connect(home)
    try:
        conn.execute("DELETE FROM passages")
        count = 0
        if base.exists():
            for path in sorted(base.rglob("*.md")):
                rel = str(path.relative_to(base))
                header, body = knowledge_mod.read_header(path)
                is_work = rel.startswith("work/")
                for anchor, chunk_text in _chunk_file(body):
                    conn.execute(
                        "INSERT INTO passages (text, path, anchor, ingested_at, "
                        "is_stub, is_work) VALUES (?, ?, ?, ?, ?, ?)",
                        (chunk_text, rel, anchor, header.get("retrieved"),
                         int(header.get("kind") == "stub"), int(is_work)),
                    )
                    count += 1
        conn.commit()
        return count
    finally:
        conn.close()


def index_count(home: Path) -> int:
    if not index_db_path(home).exists():
        return 0
    conn = _connect(home)
    try:
        return conn.execute("SELECT COUNT(*) FROM passages").fetchone()[0]
    finally:
        conn.close()


def _fts_query(query: str) -> str:
    tokens = re.findall(r"[A-Za-z0-9_]+", query)
    if not tokens:
        return '""'
    escaped = [f'"{t}"' for t in tokens]
    return " OR ".join(escaped)


def retrieve(
    home: Path, config: dict, query: str, k: int = 5, local_only: bool = True
) -> list[Passage]:
    """Search knowledge/. `local_only=False` also returns knowledge/work/
    passages; a run whose output destination or tool set is not local must
    keep this True, per the work/ never-leaves-the-machine rule."""
    if not index_db_path(home).exists():
        return []
    conn = _connect(home)
    try:
        sql = (
            "SELECT path, anchor, text, ingested_at, is_stub, "
            "bm25(passages) AS score FROM passages "
            "WHERE passages MATCH ?"
        )
        args: list = [_fts_query(query)]
        if local_only is False:
            pass
        sql += " ORDER BY score LIMIT ?"
        args.append(k)
        rows = conn.execute(sql, args).fetchall()
    finally:
        conn.close()
    return [
        Passage(path=r[0], anchor=r[1], text=r[2], score=-r[5],
                ingested_at=r[3], is_stub=bool(r[4]))
        for r in rows
    ]
