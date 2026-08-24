"""Creating, renaming, copying, and removing the store's Markdown files.

Every one of these operations was previously something you did in a shell and a
text editor. That works for the file and not for what surrounds it: the store
keeps a version chain and a change log, so a hand deletion leaves no record and
cannot be undone by the mechanism built for undoing things. Going through here
means `px0 changes list` sees it and `px0 changes revert` can put it back.

The functions are deliberately dumb about content -- they move bytes and record
changes. Validation is the caller's job, so `px0 workflows delete` does not refuse
to remove a workflow that no longer parses.
"""

import re
import shutil
from pathlib import Path

from px0 import paths, versioning
from px0.versioning import FileChange

ACTOR = "user:cli"

# A workflow or guideline id: what can be a filename and an argument without
# quoting, and cannot escape its directory.
ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")


class AuthoringError(Exception):
    """The operation was refused: a bad name, a missing file, or a collision."""


def check_id(name: str, kind: str = "id") -> str:
    """Validates a workflow or guideline id, stripping a trailing `.md`."""
    candidate = (name or "").strip()
    if candidate.endswith(".md"):
        candidate = candidate[:-3]
    if not ID_RE.match(candidate) or "/" in candidate or ".." in candidate:
        raise AuthoringError(
            f"{name!r} is not a valid {kind}; use letters, digits, dots, dashes, "
            "and underscores")
    return candidate


def _rel(home: Path, path: Path) -> str:
    return str(path.relative_to(home))


def workflow_path(home: Path, workflow_id: str) -> Path:
    """Where a workflow with this id lives, whether or not it exists yet.

    Workflows may sit in subdirectories, so an existing file is found by search
    before falling back to the top-level path a new one would take.
    """
    base = paths.workflows_dir(home)
    direct = base / f"{check_id(workflow_id, 'workflow id')}.md"
    if direct.exists():
        return direct
    matches = [p for p in sorted(base.rglob(f"{workflow_id}.md")) if p.is_file()]
    if len(matches) == 1:
        return matches[0]
    return direct


def guideline_path(home: Path, name: str) -> Path:
    """Where a guideline file with this name lives, whether or not it exists yet.

    Searched the same way as `workflow_path`, because guidelines sit in
    subdirectories too: `px0 workflows new` may file a drafted guideline under a
    folder, and a file the build wrote has to be addressable by the name it was
    reported under.
    """
    base = paths.guidelines_dir(home)
    direct = base / f"{check_id(name, 'guideline name')}.md"
    if direct.exists():
        return direct
    matches = [p for p in sorted(base.rglob(f"{name}.md")) if p.is_file()]
    if len(matches) == 1:
        return matches[0]
    return direct


def remove_file(home: Path, path: Path, evidence: str = "") -> dict:
    """Deletes a versioned file and tombstones it in the version chain.

    The content is still in the object store, so `px0 changes revert` keeps
    working after a removal. That is the whole reason to remove through px0
    rather than with `rm`.
    """
    if not path.exists():
        raise AuthoringError(f"no such file: {path}")
    rel = _rel(home, path)
    version = versioning.latest_version_number(home, rel)
    path.unlink()
    change_id = versioning.record_change(
        home, ACTOR, [FileChange(rel_path=rel, content=None, evidence=evidence or "removed")])
    return {"path": rel, "change_id": change_id, "last_version": version}


def write_file(home: Path, path: Path, content: str, evidence: str = "") -> dict:
    """Writes a versioned file and records the change."""
    rel = _rel(home, path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    change_id = versioning.record_change(
        home, ACTOR, [FileChange(rel_path=rel, content=content.encode(), evidence=evidence)])
    return {"path": rel, "change_id": change_id}


def move_file(home: Path, src: Path, dest: Path, evidence: str = "") -> dict:
    """Renames a versioned file: one change that tombstones the old path and
    creates the new one, so the log reads as a rename rather than as an
    unexplained deletion next to an unexplained addition."""
    if not src.exists():
        raise AuthoringError(f"no such file: {src}")
    if dest.exists():
        raise AuthoringError(f"{_rel(home, dest)} already exists")
    content = src.read_bytes()
    src_rel, dest_rel = _rel(home, src), _rel(home, dest)
    dest.parent.mkdir(parents=True, exist_ok=True)
    shutil.move(str(src), str(dest))
    change_id = versioning.record_change(home, ACTOR, [
        FileChange(rel_path=src_rel, content=None, evidence=evidence or f"renamed to {dest_rel}"),
        FileChange(rel_path=dest_rel, content=content, evidence=evidence or f"renamed from {src_rel}"),
    ])
    return {"from": src_rel, "to": dest_rel, "change_id": change_id}


def copy_file(home: Path, src: Path, dest: Path, evidence: str = "") -> dict:
    """Copies a versioned file to a new path and records the new file."""
    if not src.exists():
        raise AuthoringError(f"no such file: {src}")
    if dest.exists():
        raise AuthoringError(f"{_rel(home, dest)} already exists")
    content = src.read_text()
    return write_file(home, dest, content, evidence or f"copied from {_rel(home, src)}")


def set_frontmatter_key(text: str, key: str, value) -> str:
    """Sets one scalar key in a Markdown file's YAML frontmatter, in place.

    Rewrites the line if the key is there and appends it to the end of the
    block if it is not, so the rest of the file -- comments, key order, the body
    -- comes back byte for byte. A full YAML round-trip would reformat the
    whole document to change one flag.
    """
    import yaml

    literal = yaml.safe_dump(value, default_flow_style=True).strip().rstrip("...").strip()
    if not text.startswith("---"):
        return f"---\n{key}: {literal}\n---\n\n{text}"
    parts = text.split("---", 2)
    if len(parts) < 3:
        raise AuthoringError("malformed frontmatter")
    front, body = parts[1], parts[2]
    lines = front.split("\n")
    pattern = re.compile(rf"^{re.escape(key)}\s*:")
    for i, line in enumerate(lines):
        if pattern.match(line):
            lines[i] = f"{key}: {literal}"
            break
    else:
        while lines and not lines[-1].strip():
            lines.pop()
        lines.append(f"{key}: {literal}")
        lines.append("")
    return "---" + "\n".join(lines) + "---" + body
