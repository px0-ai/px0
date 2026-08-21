"""The `retrieve` interface over `brain/`: query, k, filters in;
ranked passages with file path and anchor out. Guidelines are never
retrieved by similarity -- only the brain.

Two backends sit behind `retrieve()`, selected by `retrieval.backend`:

- "local" (default): SQLite FTS5 with BM25 ranking, embedded, no server.
  Keyword matching only -- no vectors, no rerank, nothing to download.
- "qmd": shells out to the qmd CLI (`retrieval.qmd_cmd`) for hybrid
  keyword + vector search with reranking. Needs qmd installed separately
  and gates its ~2GB of GGUF models behind explicit, printed-size
  consent on the first reindex.

Either way `local_only=True` (the default at every call site) excludes
`brain/work/`, which never leaves the machine.
"""

import re
import sqlite3
from dataclasses import dataclass
from pathlib import Path

from px0 import config as config_mod
from px0 import paths

# CLI surface verified against a real install of qmd 2.8.3 (npm `@tobilu/qmd`,
# `qmd --version` -> "qmd 2.8.3 (facd35e)"), which is the current published
# release; 0.1.0 -- the placeholder this pin previously held -- was never
# published at all (the registry starts at 0.9.0), so every real install
# reported version drift. Verified from `qmd --help` at that version:
#   * `-n <num>`               max results
#   * `-c, --collection <name>` collection filter
#   * `--format <kind>`         cli | json | csv | md | xml | files
#     -- there is NO `--json` flag; JSON comes from `--format json`
#   * `collection add <path> --name <name> --mask <glob>`
#   * `update [--pull]`, `embed [-f] [-c <name>]`
# Not verified: the JSON result schema and `collection list` output format --
# every qmd subcommand segfaults (exit 139) on the darwin/node-22 box used for
# this pass, so `_parse_qmd_result` stays defensive about field names on purpose.
QMD_PINNED_VERSION = "2.8.3"

# The collection qmd indexes brain/ under. A constant because it appears in the
# path qmd hands back, so parsing and creating it must agree exactly.
QMD_COLLECTION = "px0-brain"


class RetrievalBackendError(Exception):
    """Raised when the retrieval backend is missing, times out, or errors."""
    pass


@dataclass
class Passage:
    """One retrieved chunk: source file and heading anchor, text, BM25 score, and
    provenance (when it was ingested, whether it's still a stub, and what kind of
    material it came from). `kind` is None for a file px0 did not write -- a note
    in someone's own vault has no px0 frontmatter to read it from."""
    path: str
    anchor: str
    text: str
    score: float
    ingested_at: str | None
    is_stub: bool
    kind: str | None = None


# Paths never indexed, whatever the brain points at. The defaults are what a
# real notes vault carries: tool state and deleted notes.
#
#   - any dot-directory covers .obsidian/ (Obsidian's own config and the
#     markdown its plugins ship), .trash/ (Obsidian's local trash -- a note the
#     user deleted must not stay searchable), .git/, .stversions/ (Syncthing)
#   - *.excalidraw.md is a drawing, not prose: a markdown wrapper around a JSON
#     blob that indexes as thousands of meaningless tokens
DEFAULT_IGNORE_GLOBS = ("*.excalidraw.md",)


def ignore_globs(config: dict) -> tuple[str, ...]:
    """The configured ignore patterns, falling back to the defaults."""
    configured = config_mod.get(config, "brain.ignore", None)
    if configured is None:
        return DEFAULT_IGNORE_GLOBS
    if isinstance(configured, str):
        # Tolerate a hand-edited config.toml holding a bare comma-separated string.
        configured = [item.strip() for item in configured.split(",") if item.strip()]
    return tuple(configured)


def is_ignored(rel_path: str, globs: tuple[str, ...]) -> bool:
    """Whether a brain-relative path should be kept out of the index."""
    from fnmatch import fnmatch

    parts = Path(rel_path).parts
    # A dot-directory anywhere in the path is tool state, not content. Checked
    # structurally rather than by glob so no pattern list has to enumerate every
    # sync tool and plugin that stores markdown beside the user's notes.
    if any(part.startswith(".") for part in parts[:-1]):
        return True
    name = parts[-1] if parts else rel_path
    return any(fnmatch(name, g) or fnmatch(rel_path, g) for g in globs)


def private_folder(config: dict) -> str:
    """The brain subfolder withheld from retrieval by default.

    Configurable, and disabled entirely when set to an empty string, because the
    default name collides with ordinary usage: `work/` means "never leaves this
    machine" to px0 and "my work notes" to every notes app, so a vault with a
    work folder had all of it silently dropped from every search.
    """
    configured = config_mod.get(config, "brain.private_folder", "work")
    return (configured or "").strip("/")


def is_private(rel_path: str, folder: str) -> bool:
    """Whether a brain-relative path sits inside the private folder."""
    if not folder:
        return False
    return Path(rel_path).parts[:1] == (folder,)


def brain_path(home: Path, config: dict) -> Path:
    """Resolves the configured brain/ directory, expanding ~.

    Falls back to `home / "brain"` rather than a hard-coded `~/.px0/brain`: the
    `home` argument was previously ignored outright, so any caller whose config
    lacked `brain.path` -- a partial config, a fresh store read before save, a
    test -- silently read and wrote the default store instead of the one it had
    explicitly been handed.
    """
    configured = config_mod.get(config, "brain.path", None)
    if not configured:
        return home / "brain"
    return Path(configured).expanduser()


def index_db_path(home: Path) -> Path:
    """Path to the SQLite FTS5 index file backing retrieval."""
    return paths.index_dir(home) / "index.sqlite"


# FTS5's default unicode61 tokenizer drops Unicode marks (categories Mn/Mc),
# which for an abugida is not punctuation but part of the word: "शार्दिंग" was
# indexed as the bare consonants श/र/द/ग, so it collided with any other word
# built from the same skeleton and could not be searched for as itself. Adding
# Mn/Mc keeps such words whole. Latin diacritic folding is unaffected -- café
# still indexes as "cafe" and matches either spelling.
_FTS_TOKENIZE = "unicode61 categories 'L* N* Co Mn Mc'"

_PASSAGES_DDL = (
    "CREATE VIRTUAL TABLE passages USING fts5("
    "text, path UNINDEXED, anchor UNINDEXED, ingested_at UNINDEXED, "
    "is_stub UNINDEXED, is_work UNINDEXED, kind UNINDEXED, "
    f"tokenize=\"{_FTS_TOKENIZE}\")"
)

# Frontmatter `kind` values px0 writes, and so the ones `--kind` can filter on.
# A file px0 did not write has no kind and is excluded by any --kind filter.
KINDS = ("blog", "paper", "doc", "video", "stub")


def _normalise_ddl(sql: str) -> str:
    """Whitespace-insensitive form of a CREATE statement, for drift comparison."""
    return " ".join(sql.split())


def _connect(home: Path) -> sqlite3.Connection:
    """Opens the index DB, creating the index directory and the FTS5 virtual table if needed.

    An index built by an older px0 with a different tokenizer is dropped and
    recreated rather than reused: the table's tokenizer is fixed at creation, so
    a stale one would keep answering queries with the old, worse segmentation.
    The index is derived data, so rebuilding costs nothing but a `reindex` --
    which `px0 doctor` already prompts for whenever the index is empty.
    """
    paths.index_dir(home).mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(index_db_path(home))
    existing = conn.execute(
        "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'passages'"
    ).fetchone()
    # Compared against the whole DDL, not just the tokenizer: the column list
    # changes too, and an index missing a column would fail every query that
    # names it rather than simply answering worse.
    if existing and _normalise_ddl(existing[0] or "") != _normalise_ddl(_PASSAGES_DDL):
        conn.execute("DROP TABLE passages")
        existing = None
    if not existing:
        conn.execute(_PASSAGES_DDL)
    return conn


# Anchors are a locator shown next to a result, not a summary.
ANCHOR_MAX_LEN = 80


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
            # Only the heading's own line, capped. A file with no blank lines --
            # an Excalidraw drawing is one long line of JSON behind a heading --
            # made the whole paragraph the "heading", producing an anchor
            # thousands of characters long.
            h_text = para.lstrip("#").strip().splitlines()[0] if para.strip("#").strip() else ""
            current_anchor = re.sub(r"[^a-z0-9-]+", "-", h_text.lower()).strip("-")[:ANCHOR_MAX_LEN].strip("-")
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
    """Idempotently adds the brain path to qmd's collections."""
    try:
        collections = _qmd_run(config, "collection", "list")
    except RetrievalBackendError:
        raise

    if QMD_COLLECTION not in collections:
        path = brain_path(home, config)
        _qmd_run(config, "collection", "add", str(path), "--name", QMD_COLLECTION, "--mask", "**/**.md")


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


def _as_text(value) -> str | None:
    """Coerces a frontmatter value to text, or None if it is absent.

    YAML parses an unquoted `retrieved: 2026-08-21` as a `datetime.date`, so a
    hand-written note yields a date where `Passage.ingested_at` promises a
    string. The qmd path handed that object straight through, and the local path
    only survived it because sqlite3's implicit date adapter stringified it --
    an adapter deprecated in 3.12 and slated for removal, while this package
    supports 3.11 and up. Normalising here settles both.
    """
    if value is None:
        return None
    if isinstance(value, str):
        return value
    return str(value)


def _qmd_relative_path(raw: str) -> str:
    """Normalises a qmd result path to one relative to the brain root.

    qmd reports `qmd://<collection>/docs/x.md`. Stripping only the `qmd://`
    scheme left the collection name on the front, so paths came back as
    `px0-brain/docs/x.md` -- which broke every consumer that reasons about where
    a passage sits. Most seriously, `local_only` decides what to withhold with
    `path.startswith("work/")`, so a prefixed path never matched and private
    `brain/work/` passages were returned by default, against the one guarantee
    that folder carries.
    """
    path = raw
    if "://" in path:
        path = path.split("://", 1)[1]
    prefix = f"{QMD_COLLECTION}/"
    if path.startswith(prefix):
        path = path[len(prefix):]
    return path.lstrip("/")


def _parse_qmd_result(home: Path, config: dict, raw_json: str) -> list[Passage]:
    """Parses JSON output of qmd and returns a list of Passage instances."""
    import json
    from px0 import brain as brain_mod  # avoid import cycle

    try:
        items = json.loads(raw_json)
    except json.JSONDecodeError as e:
        raise RetrievalBackendError(f"qmd query returned malformed JSON ({e}): {raw_json[:200]}")

    passages = []
    if isinstance(items, dict):
        items = items.get("items", items.get("results", []))

    base = brain_path(home, config)
    for item in items:
        path = _qmd_relative_path(item.get("file", item.get("path", "")))

        score = float(item.get("score", 0.0))
        text = item.get("snippet", item.get("content", item.get("text", "")))
        anchor = item.get("anchor", item.get("heading", ""))

        ingested_at = None
        is_stub = False
        item_kind = None

        full_path = base / path
        if full_path.exists():
            try:
                header, _ = brain_mod.read_header(full_path)
                ingested_at = _as_text(header.get("retrieved"))
                item_kind = _as_text(header.get("kind"))
                is_stub = (item_kind == "stub")
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
                kind=item_kind,
            )
        )
    return passages


def _qmd_has_consent(home: Path) -> bool:
    """Whether the local models were ever downloaded, without prompting."""
    import json as _json
    consent_path = paths.retrieval_consent_path(home)
    if not consent_path.exists():
        return False
    try:
        return bool(_json.loads(consent_path.read_text()).get("qmd_embed_consented"))
    except (OSError, ValueError):
        return False


def _qmd_retrieve(
    home: Path, config: dict, query: str, k: int, kind: str | None = None
) -> list[Passage]:
    """Retrieves passages from qmd.

    `qmd query` is the hybrid path: it expands the query and reranks with local
    LLMs, so it only works once the ~2GB of models have been downloaded. Calling
    it without them does not fail fast -- it hangs until px0's own subprocess
    timeout fires, which made the whole qmd backend look broken for anyone who
    declined the download. `qmd search` is the BM25-only command, needs no
    models, and answers in milliseconds, so that is what a no-consent store
    gets.
    """
    _qmd_ensure_collection(home, config)
    if _qmd_has_consent(home):
        # Reranking is the slow part, and it is doing real work; give it room.
        subcommand, timeout = "query", 300.0
    else:
        subcommand, timeout = "search", 60.0
    # qmd cannot filter on px0's frontmatter, so over-fetch and narrow here --
    # otherwise asking for k papers returns however many of the top k happened
    # to be papers.
    fetch = k * 5 if kind else k
    raw_json = _qmd_run(
        config, subcommand, query, "--format", "json", "-n", str(fetch),
        "-c", QMD_COLLECTION, timeout=timeout,
    )
    passages = _parse_qmd_result(home, config, raw_json)
    if kind:
        passages = [p for p in passages if p.kind == kind][:k]
    return passages


def reindex(home: Path, config: dict) -> int:
    """Rebuilds the passage index from scratch: wipes the table, walks every brain/*.md
    file, chunks it, and inserts each chunk as a row. Returns the number of passages indexed."""
    backend = config_mod.get(config, "retrieval.backend", "local")
    if backend == "qmd":
        _qmd_ensure_collection(home, config)
        consented = _qmd_ensure_embed_consent(home, config)
        
        # Run update
        update_out = _qmd_run(config, "update", "-c", QMD_COLLECTION, timeout=60)
        
        if consented:
            _qmd_run(config, "embed", "-c", QMD_COLLECTION, timeout=1800)
            
        digits = re.findall(r"\d+", update_out)
        return int(digits[0]) if digits else 0

    from px0 import brain as brain_mod  # avoid import cycle

    base = brain_path(home, config)
    globs = ignore_globs(config)
    private = private_folder(config)
    conn = _connect(home)
    try:
        conn.execute("DELETE FROM passages")
        count = 0
        if base.exists():
            for path in sorted(base.rglob("*.md")):
                # One unreadable file must not cost the whole index. A broken
                # symlink or an unreadable-permissions file used to abort the
                # walk partway through, leaving the brain silently half-indexed.
                try:
                    rel = str(path.relative_to(base))
                except ValueError:
                    continue
                if is_ignored(rel, globs):
                    continue
                try:
                    header, body = brain_mod.read_header(path)
                except OSError:
                    continue
                is_work = is_private(rel, private)
                for anchor, chunk_text in _chunk_file(body):
                    conn.execute(
                        "INSERT INTO passages (text, path, anchor, ingested_at, "
                        "is_stub, is_work, kind) VALUES (?, ?, ?, ?, ?, ?, ?)",
                        (chunk_text, rel, anchor, _as_text(header.get("retrieved")),
                         int(header.get("kind") == "stub"), int(is_work),
                         _as_text(header.get("kind"))),
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
    """Builds an FTS5 MATCH expression that OR-matches each whitespace-separated
    word of the query, as an FTS5 string so punctuation in the input can never
    be parsed as query syntax.

    Splitting on whitespace and letting FTS5 tokenize inside each quoted string
    is what keeps the query side and the index side in agreement. Extracting
    tokens here with a regex could not: `[A-Za-z0-9_]+` discarded every
    non-ASCII word outright -- a Devanagari or CJK query reached the index as an
    empty expression and matched nothing -- and even a Unicode-aware `\\w+`
    silently drops the combining marks that such words are partly made of.
    """
    terms = []
    for piece in query.split():
        # FTS5 escapes a double quote inside a string by doubling it. A piece
        # made only of punctuation stays in: it tokenizes to nothing and simply
        # matches nothing, which is the right answer for it.
        terms.append('"' + piece.replace('"', '""') + '"')
    if not terms:
        return '""'
    return " OR ".join(terms)


def retrieve(
    home: Path, config: dict, query: str, k: int = 5, local_only: bool = True,
    kind: str | None = None,
) -> list[Passage]:
    """Search brain/. `local_only=False` also returns brain/work/
    passages; a run whose output destination or tool set is not local must
    keep this True, per the work/ never-leaves-the-machine rule.

    `kind` restricts results to one frontmatter kind (see KINDS). Files px0 did
    not write carry no kind, so they are excluded by any such filter -- there is
    nothing to match them on.
    """
    backend = config_mod.get(config, "retrieval.backend", "local")
    if backend == "qmd":
        results = _qmd_retrieve(home, config, query, k, kind=kind)
    else:
        if not index_db_path(home).exists():
            return []
        conn = _connect(home)
        try:
            sql = (
                "SELECT path, anchor, text, ingested_at, is_stub, "
                "bm25(passages) AS score, kind FROM passages "
                "WHERE passages MATCH ?"
            )
            args: list = [_fts_query(query)]
            if local_only:
                # Exclude work/ inside the query, not after it: filtering post-LIMIT
                # would silently return fewer than k passages whenever work/ rows
                # rank highest.
                sql += " AND is_work = 0"
            if kind:
                # Same reasoning as the work/ exclusion: filter before LIMIT, or a
                # query whose top hits are all the wrong kind returns short.
                sql += " AND kind = ?"
                args.append(kind)
            sql += " ORDER BY score LIMIT ?"
            args.append(k)
            rows = conn.execute(sql, args).fetchall()
        finally:
            conn.close()
        results = [
            # sqlite's bm25() returns lower-is-better; negate so higher score means better match
            Passage(path=r[0], anchor=r[1], text=r[2], score=-r[5],
                    ingested_at=r[3], is_stub=bool(r[4]), kind=r[6])
            for r in rows
        ]

    if local_only:
        # Belt and braces: the local backend already excluded the private folder
        # in SQL, and qmd has no is_work column to filter on, so this is the qmd
        # path's only guard. Both sides go through `is_private` so the rule lives
        # in one place -- a guarantee that depends on matching a string prefix in
        # two files is one refactor away from silently lapsing.
        private = private_folder(config)
        results = [p for p in results if not is_private(p.path, private)]
    return results
