"""Moving a store between your own machines, safely.

`px0 store export` and `import` copy a whole store, which is the right answer
once — setting up a second machine — and the wrong answer every time after,
because it overwrites. So people did the obvious thing and pointed Dropbox at
`~/.px0`, and that has a specific, quiet failure: the version history is a
SQLite database. Two machines writing it produce a file that is neither
machine's history, and the damage is invisible until something needs to be
reverted.

This is the deliberate version of what people were doing anyway. Content is
Markdown, so it merges file by file. History does not merge and is not
pretended to: each machine keeps its own, and what crosses between them is the
files plus a record of what came from where.

Three rules, each because the alternative loses work:

**Nothing is overwritten silently.** A file changed on both sides is written
beside its neighbour as `<name>.conflict-<machine>.md` and reported. px0 is not
in a position to know which version is right, and picking one would mean
deleting the other.

**A push never deletes.** A file missing here may be one this machine has not
pulled yet, and "absent" is not the same fact as "deleted".

**The remote is a directory.** Whatever puts that directory on both machines --
Dropbox, iCloud, a git repo, a USB stick -- is the user's business and none of
px0's.
"""

import hashlib
import json
import os
import platform
import shutil
from datetime import datetime, timezone
from pathlib import Path

from px0 import paths

# What travels. `.state/` is deliberately absent: it holds the version history,
# credentials, the retrieval index, and every queue -- all of them either
# machine-specific or unmergeable.
SYNCED = ("workflows", "guidelines", "memory", "brain", "tools")

# The manifest the remote keeps, so a pull can tell "changed there" from
# "changed here" rather than comparing timestamps across machines whose clocks
# and filesystems do not agree.
MANIFEST = "px0-sync.json"

# Scaffolding that every store has an identical copy of. Syncing it creates
# an opportunity for a conflict and carries no information.
SKIP_SUFFIXES = (".sample",)

# How the losing side of a conflict is named. Deliberately *after* the
# extension rather than before it: `x.conflict-laptop.md` is still a `.md` file
# in `workflows/`, so px0 loaded it as a second workflow carrying the same
# `id:` -- and `load_all` keys by id, so which of the two you got depended on
# directory order. Putting the marker last means no loader's `*.md` glob can
# see it, and the file still sits beside the one it disagrees with.
CONFLICT_MARKER = ".conflict-"


class SyncError(Exception):
    """Raised when a remote cannot be read or written."""
    pass


def machine_id() -> str:
    """A short, stable name for this machine, for labelling conflicts.

    The hostname alone, because a conflict file is read by a person deciding
    which of two versions they meant -- "laptop" tells them that, and a hash
    would not.
    """
    name = (platform.node() or "unknown").split(".")[0]
    return "".join(c for c in name if c.isalnum() or c in "-_")[:32] or "unknown"


def store_key(home: Path) -> str:
    """The identity a store's agreements are recorded under.

    The machine name alone is not enough, and not only in theory: two stores on
    one machine -- a real one and one under `PX0_HOME` for testing something --
    would share an agreement record and each conclude the other's edits were
    its own. So the key is the machine name plus a token minted once per store
    and kept in `.state/`, which never travels.

    The token is only an identifier. What a person reads on a conflict file is
    still the machine name, because "laptop" tells them which version they are
    looking at and a random token does not.
    """
    path = paths.state_dir(home) / "sync-id"
    try:
        token = path.read_text().strip()
    except OSError:
        token = ""
    if not token:
        import secrets

        token = secrets.token_hex(3)
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(token)
        except OSError:
            pass
    return f"{machine_id()}-{token}" if token else machine_id()


def _digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _walk(base: Path) -> dict[str, Path]:
    """Every syncable file under a store root, keyed by store-relative path."""
    found: dict[str, Path] = {}
    for folder in SYNCED:
        root = base / folder
        if not root.exists():
            continue
        for path in root.rglob("*"):
            if not path.is_file() or path.name.startswith("."):
                continue
            if path.name.endswith(SKIP_SUFFIXES) or CONFLICT_MARKER in path.name:
                continue  # a conflict copy is a local artifact, not content
            found[str(path.relative_to(base))] = path
    return found


def read_manifest(remote: Path) -> dict:
    """What the remote says it holds, or an empty manifest for a fresh one."""
    path = remote / MANIFEST
    if not path.exists():
        return {"files": {}, "machines": {}, "agreed": {}}
    try:
        loaded = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as e:
        raise SyncError(f"the remote's manifest is unreadable: {e}") from e
    loaded.setdefault("files", {})
    loaded.setdefault("machines", {})
    # What each machine last exchanged, per file. The remote's own hash cannot
    # stand in for this: once a second machine pushes, the remote agrees with
    # *them*, and a first machine reading it would conclude its own edit was
    # the only change and quietly overwrite theirs.
    loaded.setdefault("agreed", {})
    return loaded


def write_manifest(remote: Path, manifest: dict) -> None:
    manifest["updated"] = datetime.now(timezone.utc).isoformat()
    (remote / MANIFEST).write_text(json.dumps(manifest, indent=2, default=str))


def ensure_remote(remote: Path) -> None:
    """Makes sure the shared directory exists, or says why it will not.

    Created when its parent already exists, because the first sync is the
    common case and `px0 store sync ~/Dropbox/px0-shared` failing until you go
    and `mkdir` it is a step with no purpose. Refused when the *parent* is
    missing, which is what a typo actually looks like -- and the difference
    matters, since the wrong answer there is a store quietly syncing into a
    directory nothing else can see.
    """
    if remote.exists():
        if not remote.is_dir():
            raise SyncError(f"{remote} is a file, not a directory")
        return
    if not remote.parent.exists():
        raise SyncError(
            f"no such directory: {remote} -- and {remote.parent} does not exist "
            "either, so this looks like a typo rather than a new share")
    try:
        remote.mkdir()
    except OSError as e:
        raise SyncError(f"cannot create {remote}: {e}") from e


def status(home: Path, remote: Path) -> dict:
    """What a sync would do, without doing any of it.

    Computed from digests on both sides against the manifest's record of what
    each machine last agreed to, so "changed here", "changed there", and
    "changed in both places" are three different answers rather than one
    guess from timestamps.
    """
    ensure_remote(remote)
    manifest = read_manifest(remote)
    known = manifest["agreed"].get(store_key(home), {})
    here = {rel: _digest(path) for rel, path in _walk(home).items()}
    there = {rel: _digest(path) for rel, path in _walk(remote).items()}

    push, pull, conflict, unchanged, settled = [], [], [], [], []
    for rel in sorted(set(here) | set(there)):
        mine, theirs = here.get(rel), there.get(rel)
        agreed = known.get(rel)
        if mine == theirs:
            unchanged.append(rel)
            if agreed != mine:
                # Both sides hold the same bytes, so they agree -- whether they
                # arrived here by syncing or because the user resolved a
                # conflict by hand. Without recording it, a resolved conflict
                # stayed unresolved: `agreed` kept the pre-conflict hash, and
                # the next ordinary edit on either side was reported as a
                # conflict again, forever.
                settled.append(rel)
        elif theirs is None:
            push.append(rel)
        elif mine is None:
            pull.append(rel)
        elif agreed is None:
            conflict.append(rel)  # both have it, neither has ever agreed
        elif mine == agreed:
            pull.append(rel)      # only they moved
        elif theirs == agreed:
            push.append(rel)      # only we moved
        else:
            conflict.append(rel)  # both moved since the last agreement
    return {"remote": str(remote), "machine": machine_id(),
            "push": push, "pull": pull, "conflict": conflict,
            "settled": settled, "unchanged": len(unchanged)}


def sync(home: Path, remote: Path, *, dry_run: bool = False,
         pull_only: bool = False, push_only: bool = False) -> dict:
    """Brings a store and a remote directory into line, keeping both histories.

    Files are copied and the manifest records what both sides now agree on. A
    file changed on both sides is not merged and not chosen between: the
    remote's version lands beside yours as a `.conflict-<machine>` file and is
    reported, because the two versions are two decisions and only a person
    knows which one they meant.

    Nothing is ever deleted. A file absent on one side may be one that side has
    not pulled yet, and treating absence as deletion is how a sync loses work.
    """
    plan = status(home, remote)
    if dry_run:
        return {**plan, "applied": False}

    manifest = read_manifest(remote)
    mine = manifest["agreed"].setdefault(store_key(home), {})
    done = {"pushed": [], "pulled": [], "conflicts": []}
    now = datetime.now(timezone.utc).isoformat()

    if not pull_only:
        for rel in plan["push"]:
            source, dest = home / rel, remote / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, dest)
            digest = _digest(source)
            manifest["files"][rel] = {"hash": digest, "from": machine_id(), "at": now}
            mine[rel] = digest
            done["pushed"].append(rel)

    if not push_only:
        for rel in plan["pull"]:
            source, dest = remote / rel, home / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, dest)
            digest = _digest(source)
            manifest["files"].setdefault(rel, {"from": "remote"})
            manifest["files"][rel].update({"hash": digest, "at": now})
            # Agreeing to what we just took is what makes the *next* sync able
            # to tell our later edit apart from theirs.
            mine[rel] = digest
            done["pulled"].append(rel)

    # Agreement caught up with what both sides already hold. Recorded even on a
    # push-only or pull-only sync, because it is a statement about what is true
    # rather than about what moved.
    for rel in plan["settled"]:
        source = home / rel
        if source.exists():
            mine[rel] = _digest(source)

    if not push_only:
        for rel in plan["conflict"]:
            # Kept side by side rather than merged. The remote's version is
            # written under the name of the machine it came from, so the file
            # a person opens says whose it is.
            origin = manifest["files"].get(rel, {}).get("from", "remote")
            path = home / rel
            beside = path.with_name(f"{path.name}{CONFLICT_MARKER}{origin}")
            beside.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(remote / rel, beside)
            done["conflicts"].append({"path": rel, "theirs": str(beside.relative_to(home)),
                                      "from": origin})

    manifest["machines"][store_key(home)] = now
    # Written even on a pull-only sync: what this machine agreed to is a fact
    # about this machine, and losing it means the next sync sees every file as
    # a conflict.
    write_manifest(remote, manifest)
    return {**plan, "applied": True, **done}


def hazard(home: Path) -> str | None:
    """Whether this store looks like it is being synced by something else.

    Worth saying out loud, because the failure is silent and specific: the
    version history is SQLite, and a folder-syncing tool copying it while both
    machines write produces a file that is neither machine's history. Nothing
    reports that until a revert needs it.
    """
    for marker, name in ((".dropbox", "Dropbox"), (".dropbox.cache", "Dropbox"),
                         (".icloud", "iCloud")):
        if (home / marker).exists() or (home.parent / marker).exists():
            return name
    text = str(home).lower()
    for needle, name in (("dropbox", "Dropbox"), ("google drive", "Google Drive"),
                         ("onedrive", "OneDrive"),
                         ("mobile documents", "iCloud Drive")):
        if needle in text:
            return name
    if (paths.state_dir(home) / "versions").exists() and os.environ.get("PX0_SYNCED"):
        return "an external sync"
    return None
