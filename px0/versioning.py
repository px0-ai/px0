"""The versioning layer: content-addressed blobs plus a sqlite manifest.

Covers workflows/, guidelines/, and config.toml only, per spec. A version is
an immutable snapshot of one file's bytes; a change groups the versions
produced by one session. Revert always writes a new version; history is
never rewritten.
"""

import difflib
import hashlib
import os
import sqlite3
import stat
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

import zstandard

from px0 import paths

SCHEMA = """
CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    latest_version INTEGER NOT NULL,
    size INTEGER,
    mtime REAL,
    deleted INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS versions (
    path TEXT NOT NULL,
    version INTEGER NOT NULL,
    hash TEXT,
    actor TEXT NOT NULL,
    change_id TEXT NOT NULL,
    timestamp TEXT NOT NULL,
    deleted INTEGER NOT NULL DEFAULT 0,
    evidence TEXT,
    PRIMARY KEY (path, version)
);
CREATE TABLE IF NOT EXISTS changes (
    id TEXT PRIMARY KEY,
    actor TEXT NOT NULL,
    timestamp TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS aliases (
    old_claim TEXT PRIMARY KEY,
    new_claim TEXT NOT NULL
);
"""


@dataclass
class FileChange:
    """One file's new content (or deletion) waiting to be recorded as a version."""
    rel_path: str
    content: bytes | None  # None means delete (tombstone)
    evidence: str | None = None


def _now() -> str:
    """Current UTC timestamp as an ISO 8601 string, for storing in the manifest."""
    return datetime.now(timezone.utc).isoformat()


def manifest_path(home: Path) -> Path:
    """Path to the sqlite manifest that indexes all versions."""
    return paths.versions_dir(home) / "manifest.sqlite"


def objects_dir(home: Path) -> Path:
    """Path to the content-addressed blob store (zstd-compressed file contents)."""
    return paths.versions_dir(home) / "objects"


def connect(home: Path) -> sqlite3.Connection:
    """Opens the manifest db, creating the versions directory and schema if needed.
    Caller is responsible for closing the connection."""
    paths.versions_dir(home).mkdir(parents=True, exist_ok=True)
    objects_dir(home).mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(manifest_path(home))
    conn.row_factory = sqlite3.Row
    conn.executescript(SCHEMA)
    return conn


def store_blob(home: Path, content: bytes) -> str:
    """Writes content to the blob store under its sha256 digest, compressed with zstd.
    No-op if a blob with that digest already exists (content-addressed dedup).
    Returns the hex digest."""
    digest = hashlib.sha256(content).hexdigest()
    blob_dir = objects_dir(home) / digest[:2]  # two-char fanout to avoid huge flat dirs
    blob_dir.mkdir(parents=True, exist_ok=True)
    blob_path = blob_dir / digest
    if not blob_path.exists():
        compressed = zstandard.ZstdCompressor().compress(content)
        blob_path.write_bytes(compressed)
    return digest


def read_blob(home: Path, digest: str) -> bytes:
    """Reads and decompresses a blob by its digest."""
    blob_path = objects_dir(home) / digest[:2] / digest
    compressed = blob_path.read_bytes()
    return zstandard.ZstdDecompressor().decompress(compressed)


def new_change_id(conn: sqlite3.Connection, actor: str) -> str:
    """Allocates and inserts a new change id of the form chg_YYYY-MM-DD-NNN,
    sequential per day. Caller must commit the transaction."""
    date = datetime.now().strftime("%Y-%m-%d")
    row = conn.execute(
        "SELECT id FROM changes WHERE id LIKE ? ORDER BY id DESC LIMIT 1",
        (f"chg_{date}-%",),
    ).fetchone()
    seq = int(row["id"].rsplit("-", 1)[1]) + 1 if row else 1
    change_id = f"chg_{date}-{seq:03d}"
    conn.execute(
        "INSERT INTO changes (id, actor, timestamp) VALUES (?, ?, ?)",
        (change_id, actor, _now()),
    )
    return change_id


def record_change(
    home: Path, actor: str, file_changes: list[FileChange]
) -> str | None:
    """Write one or more file versions as a single atomic change.
    Returns the change id, or None if nothing actually changed."""
    if not file_changes:
        return None
    conn = connect(home)
    try:
        change_id = None
        for fc in file_changes:
            row = conn.execute(
                "SELECT latest_version, deleted FROM files WHERE path = ?",
                (fc.rel_path,),
            ).fetchone()
            next_version = (row["latest_version"] + 1) if row else 1

            if fc.content is None:
                if row is None or row["deleted"]:
                    continue  # nothing to tombstone
                digest = None
            else:
                digest = store_blob(home, fc.content)
                if row is not None and not row["deleted"]:
                    prev = conn.execute(
                        "SELECT hash FROM versions WHERE path = ? AND version = ?",
                        (fc.rel_path, row["latest_version"]),
                    ).fetchone()
                    if prev and prev["hash"] == digest:
                        continue  # unchanged, nothing to record

            if change_id is None:
                # allocate the change id lazily, only once we know at least one file actually changed
                change_id = new_change_id(conn, actor)

            conn.execute(
                "INSERT INTO versions (path, version, hash, actor, change_id, "
                "timestamp, deleted, evidence) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
                (
                    fc.rel_path,
                    next_version,
                    digest,
                    actor,
                    change_id,
                    _now(),
                    0 if fc.content is not None else 1,
                    fc.evidence,
                ),
            )
            size = len(fc.content) if fc.content is not None else 0
            conn.execute(
                "INSERT INTO files (path, latest_version, size, mtime, deleted) "
                "VALUES (?, ?, ?, ?, ?) "
                "ON CONFLICT(path) DO UPDATE SET latest_version=excluded.latest_version, "
                "size=excluded.size, mtime=excluded.mtime, deleted=excluded.deleted",
                (fc.rel_path, next_version, size, datetime.now().timestamp(),
                 0 if fc.content is not None else 1),
            )
        conn.commit()
        return change_id
    finally:
        conn.close()


def list_versions(home: Path, rel_path: str) -> list[dict]:
    """Returns every recorded version of a file, oldest first, as dicts with
    version/actor/change_id/timestamp/deleted/evidence."""
    conn = connect(home)
    try:
        rows = conn.execute(
            "SELECT version, actor, change_id, timestamp, deleted, evidence "
            "FROM versions WHERE path = ? ORDER BY version",
            (rel_path,),
        ).fetchall()
        return [dict(r) for r in rows]
    finally:
        conn.close()


def show_version(home: Path, rel_path: str, version: int) -> bytes | None:
    """Returns the raw bytes of a file at a specific version, or None if that
    version was a deletion. Raises ValueError if the version doesn't exist."""
    conn = connect(home)
    try:
        row = conn.execute(
            "SELECT hash, deleted FROM versions WHERE path = ? AND version = ?",
            (rel_path, version),
        ).fetchone()
        if row is None:
            raise ValueError(f"no such version: {rel_path}@v{version}")
        if row["deleted"] or row["hash"] is None:
            return None
        return read_blob(home, row["hash"])
    finally:
        conn.close()


def latest_version_number(home: Path, rel_path: str) -> int | None:
    """Returns the newest version number for a file, or None if it has no history."""
    conn = connect(home)
    try:
        row = conn.execute(
            "SELECT latest_version FROM files WHERE path = ?", (rel_path,)
        ).fetchone()
        return row["latest_version"] if row else None
    finally:
        conn.close()


def diff_versions(home: Path, rel_path: str, v1: int, v2: int) -> str:
    """Returns a unified diff string between two versions of a file.
    A deleted version is treated as empty content."""
    a = (show_version(home, rel_path, v1) or b"").decode("utf-8", "replace")
    b = (show_version(home, rel_path, v2) or b"").decode("utf-8", "replace")
    diff = difflib.unified_diff(
        a.splitlines(keepends=True),
        b.splitlines(keepends=True),
        fromfile=f"{rel_path}@v{v1}",
        tofile=f"{rel_path}@v{v2}",
    )
    return "".join(diff)


def _write_to_disk(home: Path, rel_path: str, content: bytes | None) -> None:
    """Puts a version's content back on disk, or removes the file for a tombstone.

    `record_change` is the history writer and touches nothing in the working
    tree, which is right for capturing an edit that has already happened and
    wrong for a revert: reverting used to record the old content as a new
    version and leave the file alone, so `px0 changes revert` reported success
    and changed nothing. The next checkpoint scan then captured the untouched
    file again, quietly discarding the revert.

    Paths come out of the manifest, so they are confined to the store before
    anything is written -- a manifest row is data, and data does not get to
    name a path outside the store.
    """
    target = (home / rel_path).resolve()
    root = home.resolve()
    if root != target and root not in target.parents:
        raise ValueError(f"refusing to write outside the store: {rel_path}")
    if content is None:
        target.unlink(missing_ok=True)
        return
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(content)


def list_changes(home: Path, since: datetime | None = None, actor: str | None = None) -> list[dict]:
    """Returns changes newest-first, optionally filtered by timestamp and actor,
    each annotated with the list of (path, version) pairs it touched."""
    conn = connect(home)
    try:
        query = "SELECT id, actor, timestamp FROM changes WHERE 1=1"
        args: list = []
        if since is not None:
            query += " AND timestamp >= ?"
            args.append(since.isoformat())
        if actor is not None:
            query += " AND actor = ?"
            args.append(actor)
        query += " ORDER BY id DESC"
        changes = [dict(r) for r in conn.execute(query, args).fetchall()]
        for c in changes:
            files = conn.execute(
                "SELECT path, version FROM versions WHERE change_id = ?", (c["id"],)
            ).fetchall()
            c["files"] = [dict(f) for f in files]
        return changes
    finally:
        conn.close()


def show_change(home: Path, change_id: str) -> dict:
    """Returns a change's metadata plus a per-file unified diff against each
    file's previous version (or a diff from /dev/null for a first version).
    Raises ValueError if the change id doesn't exist."""
    conn = connect(home)
    try:
        change = conn.execute(
            "SELECT id, actor, timestamp FROM changes WHERE id = ?", (change_id,)
        ).fetchone()
        if change is None:
            raise ValueError(f"no such change: {change_id}")
        change = dict(change)
        rows = conn.execute(
            "SELECT path, version, deleted FROM versions WHERE change_id = ?",
            (change_id,),
        ).fetchall()
    finally:
        conn.close()

    files = []
    for r in rows:
        prev_rows = list_versions(home, r["path"])
        prev_version = None
        # rows are version-ascending, so this ends up holding the version immediately before r
        for v in prev_rows:
            if v["version"] < r["version"]:
                prev_version = v["version"]
        diff = ""
        if prev_version is not None:
            diff = diff_versions(home, r["path"], prev_version, r["version"])
        else:
            new_content = show_version(home, r["path"], r["version"]) or b""
            diff = "".join(
                difflib.unified_diff(
                    [], new_content.decode("utf-8", "replace").splitlines(keepends=True),
                    fromfile="/dev/null", tofile=f"{r['path']}@v{r['version']}",
                )
            )
        files.append({"path": r["path"], "version": r["version"],
                       "deleted": bool(r["deleted"]), "diff": diff})
    change["files"] = files
    return change


def revert_change(home: Path, change_id: str, actor: str) -> str | None:
    """Reverts every file touched by a change back to its version immediately
    prior (or deletes it, if the file had no earlier version). Returns the
    new change id, or None if there was nothing to revert."""
    change = show_change(home, change_id)
    file_changes = []
    for f in change["files"]:
        rows = list_versions(home, f["path"])
        prev_version = None
        # rows are version-ascending, so this ends up holding the version immediately before f
        for v in rows:
            if v["version"] < f["version"]:
                prev_version = v["version"]
        content = show_version(home, f["path"], prev_version) if prev_version else None
        _write_to_disk(home, f["path"], content)
        file_changes.append(FileChange(f["path"], content))
    return record_change(home, actor, file_changes)


def _walk_versioned_files(home: Path) -> list[Path]:
    """Lists every file on disk that falls under version control: all
    markdown under workflows/ and guidelines/, plus config.toml."""
    files = []
    for base in (paths.workflows_dir(home), paths.guidelines_dir(home)):
        if base.exists():
            files.extend(p for p in base.rglob("*.md") if p.is_file())
    cfg = paths.config_path(home)
    if cfg.exists():
        files.append(cfg)
    return files


def checkpoint_scan(home: Path, actor: str = "user:manual", force_hash: bool = False) -> str | None:
    """Scan workflows/, guidelines/, and config.toml for changes made
    outside the tool (hand edits), and capture them as new versions.
    `force_hash` skips the mtime/size shortcut (the daemon's nightly pass,
    which catches what mtime tricks miss)."""
    conn = connect(home)
    try:
        known = {
            r["path"]: dict(r)
            for r in conn.execute("SELECT path, size, mtime, deleted FROM files").fetchall()
        }
    finally:
        conn.close()

    on_disk = _walk_versioned_files(home)
    on_disk_rel = set()
    file_changes: list[FileChange] = []

    for path in on_disk:
        rel = str(path.relative_to(home))
        on_disk_rel.add(rel)
        st = path.stat()
        prev = known.get(rel)
        if (not force_hash and prev and not prev["deleted"]
                and prev["size"] == st.st_size and prev["mtime"] == st.st_mtime):
            continue  # unchanged by mtime/size heuristic
        content = path.read_bytes()
        digest = hashlib.sha256(content).hexdigest()
        if prev and not prev["deleted"]:
            latest = latest_version_number(home, rel)
            existing_hash = None
            if latest:
                conn = connect(home)
                try:
                    row = conn.execute(
                        "SELECT hash FROM versions WHERE path = ? AND version = ?",
                        (rel, latest),
                    ).fetchone()
                    existing_hash = row["hash"] if row else None
                finally:
                    conn.close()
            if existing_hash == digest:
                continue
        file_changes.append(FileChange(rel, content))

    for rel, prev in known.items():
        if not prev["deleted"] and rel not in on_disk_rel:
            file_changes.append(FileChange(rel, None))

    return record_change(home, actor, file_changes)


def ensure_secure_permissions(path: Path) -> None:
    """Restricts a file to owner read/write only (mode 0600); no-op if it
    doesn't exist yet. Used for credentials.toml."""
    if path.exists():
        os.chmod(path, stat.S_IRUSR | stat.S_IWUSR)
