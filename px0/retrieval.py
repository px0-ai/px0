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

QMD_PINNED_VERSION = "0.1.0"


class RetrievalBackendError(Exception):
    """Raised when the retrieval backend is missing, times out, or errors."""
    pass


@dataclass
class Passage:
    """One retrieved chunk: source file and heading anchor, text, BM25 score, and
    provenance flags (when it was ingested, whether it's still a stub)."""
    path: str
    anchor: str
    text: str
    score: float
    ingested_at: str | None
    is_stub: bool


def knowledge_path(home: Path, config: dict) -> Path:
    """Resolves the configured knowledge/ directory, expanding ~."""
    configured = config_mod.get(config, "knowledge.path", "~/.px0/knowledge")
    return Path(configured).expanduser()


def index_db_path(home: Path) -> Path:
    """Path to the SQLite FTS5 index file backing retrieval."""
    return paths.index_dir(home) / "index.sqlite"


def _connect(home: Path) -> sqlite3.Connection:
    """Opens the index DB, creating the index directory and the FTS5 virtual table if needed."""
    paths.index_dir(home).mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(index_db_path(home))
    conn.execute(
        "CREATE VIRTUAL TABLE IF NOT EXISTS passages USING fts5("
        "text, path UNINDEXED, anchor UNINDEXED, ingested_at UNINDEXED, "
        "is_stub UNINDEXED, is_work UNINDEXED)"
    )
    return conn


def _chunk_by_paragraph(text: str) -> list[tuple[str, str]]:
    """Chunks text by splitting on two or more newlines, grouping paragraphs
    until the chunk is at least 150 words (or we hit a heading/EOF), and
    finding the nearest preceding markdown heading (e.g. `## Section`) to
    use as the anchor."""
    paragraphs = re.split(r"\n{2,}", text)
    chunks = []
    current_chunk = []
    current_words = 0
    current_anchor = ""

    for para in paragraphs:
        para = para.strip()
        if not para:
            continue

        # If it's a heading, it starts a new chunk immediately and updates the anchor
        if para.startswith("#"):
            if current_chunk:
                chunks.append((current_anchor, "\n\n".join(current_chunk)))
                current_chunk = []
                current_words = 0
            # Extract heading text (strip leading # and spaces, slugify)
            h_text = para.lstrip("#").strip()
            current_anchor = re.sub(r"[^a-z0-9-]+", "-", h_text.lower()).strip("-")
            # Also keep the heading in the text
            current_chunk.append(para)
            current_words += len(para.split())
            continue

        current_chunk.append(para)
        current_words += len(para.split())

        if current_words >= 150:
            chunks.append((current_anchor, "\n\n".join(current_chunk)))
            current_chunk = []
            current_words = 0

    if current_chunk:
        chunks.append((current_anchor, "\n\n".join(current_chunk)))

    return chunks


def _chunk_file(text: str) -> list[tuple[str, str]]:
    """Standard file-chunking entry point. Returns a list of (anchor, text) tuples."""
    return _chunk_by_paragraph(text)


def _qmd_run(config: dict, *args, timeout: float = 60) -> str:
    """Shells out to the qmd command configured in retrieval.qmd_cmd with args."""
    import shlex
    import subprocess
    qmd_cmd = config_mod.get(config, "retrieval.qmd_cmd", "qmd")
    cmd = shlex.split(qmd_cmd) + list(args)
    try:
        result = subprocess.run(
            cmd, capture_output=True, text=True, timeout=timeout
        )
    except FileNotFoundError as e:
        raise RetrievalBackendError(
            "qmd not found on PATH; install with `npm install -g @tobilu/qmd` "
            "(requires Node.js) or `bun install -g @tobilu/qmd` (requires Bun), "
            "or set `retrieval.backend` back to `local`."
        ) from e
    except subprocess.TimeoutExpired as e:
        raise RetrievalBackendError(f"qmd timed out after {timeout}s") from e

    if result.returncode != 0:
        raise RetrievalBackendError(
            f"qmd exited {result.returncode}: {result.stderr.strip()[:500]}"
        )
    return result.stdout


def _qmd_ensure_collection(home: Path, config: dict):
    """Idempotently adds the knowledge path to qmd's collections."""
    try:
        collections = _qmd_run(config, "collection", "list")
    except RetrievalBackendError:
        raise

    if "px0-knowledge" not in collections:
        path = knowledge_path(home, config)
        _qmd_run(config, "collection", "add", str(path), "--name", "px0-knowledge", "--mask", "**/**.md")


def _qmd_ensure_embed_consent(home: Path, config: dict) -> bool:
    """Checks and prompts for model download consent if not already given."""
    import json
    from datetime import datetime, timezone
    consent_path = paths.retrieval_consent_path(home)
    if consent_path.exists():
        try:
            data = json.loads(consent_path.read_text())
            if data.get("qmd_embed_consented"):
                return True
        except Exception:
            pass

    # Print table
    print("\nLocal models needed for semantic search & reranking:")
    print("--------------------------------------------------")
    print("embeddinggemma-300M       ~300MB  (Embeddings)")
    print("qwen3-reranker-0.6b       ~640MB  (Reranking)")
    print("qmd-query-expansion-1.7B  ~1.1GB  (Expansion)")
    print("--------------------------------------------------")
    print("Total Download Size:      ~2.04GB")
    print("--------------------------------------------------")

    try:
        ans = input("Download ~2.04GB of local models for semantic search? [y/N] ").strip().lower()
    except (KeyboardInterrupt, EOFError):
        ans = "n"

    if ans.startswith("y"):
        consent_data = {
            "qmd_embed_consented": True,
            "consented_at": datetime.now(timezone.utc).isoformat()
        }
        consent_path.parent.mkdir(parents=True, exist_ok=True)
        consent_path.write_text(json.dumps(consent_data))
        return True
    else:
        print("Semantic search degraded to keyword-only until consent is given.")
        return False


def _parse_qmd_result(home: Path, config: dict, raw_json: str) -> list[Passage]:
    """Parses JSON output of qmd and returns a list of Passage instances."""
    import json
    from px0 import knowledge as knowledge_mod  # avoid import cycle

    try:
        items = json.loads(raw_json)
    except json.JSONDecodeError as e:
        raise RetrievalBackendError(f"qmd query returned malformed JSON ({e}): {raw_json[:200]}")

    passages = []
    if isinstance(items, dict):
        items = items.get("items", items.get("results", []))

    base = knowledge_path(home, config)
    for item in items:
        path = item.get("file", item.get("path", ""))
        if path.startswith("qmd://"):
            path = path.split("://", 1)[-1]

        score = float(item.get("score", 0.0))
        text = item.get("snippet", item.get("content", item.get("text", "")))
        anchor = item.get("anchor", item.get("heading", ""))

        ingested_at = None
        is_stub = False

        full_path = base / path
        if full_path.exists():
            try:
                header, _ = knowledge_mod.read_header(full_path)
                ingested_at = header.get("retrieved")
                is_stub = (header.get("kind") == "stub")
            except Exception:
                pass

        passages.append(
            Passage(
                path=path,
                anchor=anchor,
                text=text,
                score=score,
                ingested_at=ingested_at,
                is_stub=is_stub,
            )
        )
    return passages


def _qmd_retrieve(home: Path, config: dict, query: str, k: int) -> list[Passage]:
    """Retrieves passages using the qmd query command."""
    _qmd_ensure_collection(home, config)
    raw_json = _qmd_run(config, "query", query, "--json", "-n", str(k), "-c", "px0-knowledge")
    return _parse_qmd_result(home, config, raw_json)


def reindex(home: Path, config: dict) -> int:
    """Rebuilds the passage index from scratch: wipes the table, walks every knowledge/*.md
    file, chunks it, and inserts each chunk as a row. Returns the number of passages indexed."""
    backend = config_mod.get(config, "retrieval.backend", "local")
    if backend == "qmd":
        _qmd_ensure_collection(home, config)
        consented = _qmd_ensure_embed_consent(home, config)
        
        # Run update
        update_out = _qmd_run(config, "update", "-c", "px0-knowledge", timeout=60)
        
        if consented:
            _qmd_run(config, "embed", "-c", "px0-knowledge", timeout=1800)
            
        digits = re.findall(r"\d+", update_out)
        return int(digits[0]) if digits else 0

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
    """Number of indexed passages, or 0 if the index doesn't exist yet."""
    if not index_db_path(home).exists():
        return 0
    conn = _connect(home)
    try:
        return conn.execute("SELECT COUNT(*) FROM passages").fetchone()[0]
    finally:
        conn.close()


def _fts_query(query: str) -> str:
    """Builds an FTS5 MATCH expression that OR-matches every word token in the query,
    quoting each token so punctuation in the input can't break the query syntax."""
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
    backend = config_mod.get(config, "retrieval.backend", "local")
    if backend == "qmd":
        results = _qmd_retrieve(home, config, query, k)
    else:
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
            sql += " ORDER BY score LIMIT ?"
            args.append(k)
            rows = conn.execute(sql, args).fetchall()
        finally:
            conn.close()
        results = [
            # sqlite's bm25() returns lower-is-better; negate so higher score means better match
            Passage(path=r[0], anchor=r[1], text=r[2], score=-r[5],
                    ingested_at=r[3], is_stub=bool(r[4]))
            for r in rows
        ]

    if local_only:
        results = [p for p in results if not p.path.startswith("work/")]
    return results
