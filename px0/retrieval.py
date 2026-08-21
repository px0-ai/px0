"""The `retrieve` interface over `brain/`: query, k, filters in;
ranked passages with file path and anchor out. Guidelines are never
retrieved by similarity -- only the brain.

`retrieve()` shells out to the qmd CLI (`retrieval.qmd_cmd`) for hybrid
keyword + vector search with reranking. Needs qmd installed separately
and gates its ~2GB of GGUF models behind explicit, printed-size consent
on the first reindex.

`local_only=True` (the default at every call site) excludes
`brain/work/`, which never leaves the machine.
"""

import re
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


# Frontmatter `kind` values px0 writes, and so the ones `--kind` can filter on.
# A file px0 did not write has no kind and is excluded by any --kind filter.
KINDS = ("blog", "paper", "doc", "video", "stub")


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
            "(requires Node.js) or `bun install -g @tobilu/qmd` (requires Bun)."
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
    string, and qmd's parsing handed that object straight through.
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
    """Rebuilds the qmd index: ensures the collection exists, asks for model-download
    consent if not already given, then runs `qmd update` (and `qmd embed` if consented).
    Returns the number of passages indexed."""
    _qmd_ensure_collection(home, config)
    consented = _qmd_ensure_embed_consent(home, config)

    update_out = _qmd_run(config, "update", "-c", QMD_COLLECTION, timeout=60)

    if consented:
        _qmd_run(config, "embed", "-c", QMD_COLLECTION, timeout=1800)

    digits = re.findall(r"\d+", update_out)
    return int(digits[0]) if digits else 0


# How many candidates the rerank stage looks at before trimming back to k.
# Wide enough that a passage BM25 ranked eighth can win on term coverage,
# narrow enough to stay a local, instant pass.
RERANK_FACTOR = 4
RERANK_MAX_CANDIDATES = 60

_WORD_RE = re.compile(r"[A-Za-z0-9_]+")


def _terms(query: str) -> list[str]:
    """The distinct lowercase words in a query, longest first."""
    seen, out = set(), []
    for word in _WORD_RE.findall(query.lower()):
        if len(word) > 1 and word not in seen:
            seen.add(word)
            out.append(word)
    return sorted(out, key=len, reverse=True)


def rerank(query: str, passages: list[Passage], k: int) -> list[Passage]:
    """Reorders candidates by how well each one actually covers the query.

    BM25 rewards a passage that says one query term many times. A question with
    three terms in it usually wants the passage that mentions all three, even
    if each only once -- so coverage leads, then how close the terms sit to
    each other, then the original score as the tie-break. Pure local
    arithmetic: no model call, no network, and stable for the same input.
    """
    terms = _terms(query)
    if not terms or not passages:
        return passages[:k]

    scored = []
    for rank, passage in enumerate(passages):
        text = (passage.text or "").lower()
        positions = []
        hits = 0
        for term in terms:
            index = text.find(term)
            if index >= 0:
                hits += 1
                positions.append(index)
        coverage = hits / len(terms)
        if len(positions) > 1:
            spread = max(positions) - min(positions)
            # Terms within a paragraph of each other are usually one thought.
            proximity = 1.0 / (1.0 + spread / 400.0)
        else:
            proximity = 0.5 if positions else 0.0
        # Long passages match more terms by accident; a mild length penalty
        # keeps a whole chapter from outranking the paragraph that answers.
        length_penalty = min(1.0, 1200.0 / max(len(text), 1)) ** 0.15
        combined = (coverage * 3.0) + proximity + (length_penalty * 0.25)
        scored.append((combined, -rank, passage))

    scored.sort(key=lambda t: (t[0], t[1]), reverse=True)
    return [p for _score, _rank, p in scored[:k]]


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
    reranking = bool(config_mod.get(config, "retrieval.rerank", True))
    # Reranking can only reorder what it is given, so ask for more than k when
    # it is on. With rerank off, k rows in means k rows out, as before.
    fetch_k = min(max(k * RERANK_FACTOR, k), RERANK_MAX_CANDIDATES) if reranking else k
    results = _qmd_retrieve(home, config, query, fetch_k, kind=kind)

    if local_only:
        # qmd has no is_work column to filter on, so this is the only guard
        # against a private passage leaking out -- it goes through `is_private`
        # so the rule lives in one place.
        private = private_folder(config)
        results = [p for p in results if not is_private(p.path, private)]

    if reranking:
        return rerank(query, results, k)
    return results[:k]
