"""Guideline claims: `<path>#<heading-slug>` addressing, section-level
history, and rename aliasing by body similarity."""

import re
from dataclasses import dataclass
from pathlib import Path

from px0 import versioning

RENAME_THRESHOLD = 0.7
_HEADING_RE = re.compile(r"^(#{1,6})\s+(.*?)\s*$")


def slugify(heading: str) -> str:
    """Converts a Markdown heading into the URL/id-safe slug used to address it as a claim."""
    slug = heading.lower().strip()
    slug = re.sub(r"[`*_]", "", slug)
    slug = re.sub(r"[^a-z0-9\s-]", "", slug)
    slug = re.sub(r"\s+", "-", slug).strip("-")
    return slug


@dataclass
class Section:
    """One `##`-or-deeper section of a guideline file: its heading, slug, line range
    within the file, and raw lines (heading line included)."""
    heading: str
    slug: str
    start_line: int
    end_line: int  # exclusive
    lines: list[str]

    @property
    def text(self) -> str:
        """The section's full text, heading line included."""
        return "".join(self.lines)

    @property
    def body(self) -> str:
        """The section's text with the heading line stripped."""
        return "".join(self.lines[1:])


def extract_sections(content: str) -> list[Section]:
    """Splits a guideline file's content into sections, one per heading, each
    running up to the next heading (or end of file)."""
    lines = content.splitlines(keepends=True)
    headings: list[tuple[int, str]] = []
    for i, line in enumerate(lines):
        m = _HEADING_RE.match(line)
        if m:
            headings.append((i, m.group(2)))
    sections = []
    for idx, (start, heading) in enumerate(headings):
        end = headings[idx + 1][0] if idx + 1 < len(headings) else len(lines)
        sections.append(Section(heading, slugify(heading), start, end, lines[start:end]))
    return sections


def _normalize_tokens(text: str) -> set[str]:
    """Lowercases text, unwraps inline code spans, and splits into a set of word tokens."""
    text = text.lower()
    text = re.sub(r"`([^`]*)`", r"\1", text)
    text = re.sub(r"[^\w\s]", " ", text)
    return {t for t in text.split() if t}


def jaccard_similarity(a: str, b: str) -> float:
    """Token-level Jaccard similarity between two texts. Two empty texts are
    treated as identical (1.0); one empty and one non-empty are unrelated (0.0)."""
    ta, tb = _normalize_tokens(a), _normalize_tokens(b)
    if not ta and not tb:
        return 1.0
    if not ta or not tb:
        return 0.0
    return len(ta & tb) / len(ta | tb)


def detect_renames(old_content: str, new_content: str) -> list[tuple[str, str]]:
    """Compare two versions of one guideline file's sections. Returns
    (old_slug, new_slug) pairs whose bodies are similar enough (>= 0.7
    token-level Jaccard) to be recorded as a rename rather than a
    deletion plus a new claim."""
    old_sections = {s.slug: s for s in extract_sections(old_content)}
    new_sections = {s.slug: s for s in extract_sections(new_content)}
    disappeared = [s for slug, s in old_sections.items() if slug not in new_sections]
    appeared = [s for slug, s in new_sections.items() if slug not in old_sections]

    renames = []
    used_new = set()
    for old_s in disappeared:
        best_slug, best_score = None, 0.0
        for new_s in appeared:
            if new_s.slug in used_new:
                continue
            score = jaccard_similarity(old_s.body, new_s.body)
            if score > best_score:
                best_slug, best_score = new_s.slug, score
        if best_slug is not None and best_score >= RENAME_THRESHOLD:
            renames.append((old_s.slug, best_slug))
            used_new.add(best_slug)
    return renames


# --- claim ids ---------------------------------------------------------

class ClaimIdError(ValueError):
    """A claim id is not of the form `<file>.md#<slug>`."""
    pass


def parse_claim_id(claim_id: str) -> tuple[str, str]:
    """Splits a claim id into (file path, slug), validating its shape.

    Every consumer used to do `claim_id.split("#", 1)` bare, which turned a
    typo'd id into `not enough values to unpack` -- and let a malformed alias
    into the table, where it broke lineage lookups for unrelated claims.
    """
    if not isinstance(claim_id, str) or "#" not in claim_id:
        raise ClaimIdError(
            f"{claim_id!r} is not a claim id -- expected <file>.md#<section-slug>, "
            "e.g. commit-messages.md#summary-line"
        )
    path, slug = claim_id.split("#", 1)
    if not path or not slug:
        raise ClaimIdError(
            f"{claim_id!r} is not a claim id -- both the file and the section slug "
            "are required, e.g. commit-messages.md#summary-line"
        )
    return path, slug


# --- alias storage -----------------------------------------------------

def add_alias(home: Path, old_claim: str, new_claim: str) -> None:
    """Records (or updates) that old_claim now resolves to new_claim.

    Both ids are validated first: an unparseable alias is written once and then
    breaks every later lineage walk, including for well-formed claims.
    """
    parse_claim_id(old_claim)
    parse_claim_id(new_claim)
    conn = versioning.connect(home)
    try:
        conn.execute(
            "INSERT INTO aliases (old_claim, new_claim) VALUES (?, ?) "
            "ON CONFLICT(old_claim) DO UPDATE SET new_claim=excluded.new_claim",
            (old_claim, new_claim),
        )
        conn.commit()
    finally:
        conn.close()


def remove_alias(home: Path, old_claim: str) -> None:
    """Deletes an alias mapping; no-op if it doesn't exist."""
    conn = versioning.connect(home)
    try:
        conn.execute("DELETE FROM aliases WHERE old_claim = ?", (old_claim,))
        conn.commit()
    finally:
        conn.close()


def list_aliases(home: Path) -> list[dict]:
    """Returns every alias mapping, sorted by old_claim."""
    conn = versioning.connect(home)
    try:
        return [dict(r) for r in conn.execute(
            "SELECT old_claim, new_claim FROM aliases ORDER BY old_claim"
        ).fetchall()]
    finally:
        conn.close()


def resolve_claim(home: Path, claim_id: str, _seen: set | None = None) -> str:
    """Follow the alias chain forward to the current claim id."""
    _seen = _seen or set()
    if claim_id in _seen:
        return claim_id
    conn = versioning.connect(home)
    try:
        row = conn.execute(
            "SELECT new_claim FROM aliases WHERE old_claim = ?", (claim_id,)
        ).fetchone()
    finally:
        conn.close()
    if row is None:
        return claim_id
    _seen.add(claim_id)
    return resolve_claim(home, row["new_claim"], _seen)


def lineage_slugs(home: Path, path: str, claim_id: str) -> set[str]:
    """All slugs (past and present) belonging to this claim's identity,
    by walking the alias graph in both directions."""
    _, target_slug = parse_claim_id(claim_id)
    aliases = list_aliases(home)
    slugs = {target_slug}
    changed = True
    while changed:
        changed = False
        for a in aliases:
            # Rows written before add_alias validated are skipped, not fatal:
            # one bad row must not break lineage for every other claim.
            try:
                o_path, o_slug = parse_claim_id(a["old_claim"])
                n_path, n_slug = parse_claim_id(a["new_claim"])
            except ClaimIdError:
                continue
            if o_path != path or n_path != path:
                continue
            if o_slug in slugs and n_slug not in slugs:
                slugs.add(n_slug)
                changed = True
            elif n_slug in slugs and o_slug not in slugs:
                slugs.add(o_slug)
                changed = True
    return slugs


def process_change_for_renames(home: Path, change_id: str | None) -> None:
    """For each guideline file touched by a change, diffs it against its previous
    version and records any detected section renames as aliases. No-op for a
    None change_id (nothing was actually recorded)."""
    if change_id is None:
        return
    change = versioning.show_change(home, change_id)
    for f in change["files"]:
        path = f["path"]
        if not path.startswith("guidelines/") or f["deleted"]:
            continue
        rows = versioning.list_versions(home, path)
        prev_version = None
        for v in rows:
            if v["version"] < f["version"]:
                prev_version = v["version"]
        if prev_version is None:
            continue
        old_bytes = versioning.show_version(home, path, prev_version)
        new_bytes = versioning.show_version(home, path, f["version"])
        if old_bytes is None or new_bytes is None:
            continue
        renames = detect_renames(old_bytes.decode("utf-8"), new_bytes.decode("utf-8"))
        for old_slug, new_slug in renames:
            add_alias(home, f"{path}#{old_slug}", f"{path}#{new_slug}")


def scan_and_process(home: Path, actor: str = "user:manual", force_hash: bool = False) -> str | None:
    """The checkpoint scan plus rename detection over what it captured."""
    change_id = versioning.checkpoint_scan(home, actor, force_hash=force_hash)
    process_change_for_renames(home, change_id)
    return change_id


def capture_guideline_change(
    home: Path, actor: str, file_changes: list[versioning.FileChange]
) -> str | None:
    """Records a guideline edit as a version and immediately runs rename detection
    on it, so aliases stay in sync with every guideline write path."""
    change_id = versioning.record_change(home, actor, file_changes)
    process_change_for_renames(home, change_id)
    return change_id


# --- claim log / revert -------------------------------------------------

def guidelines_log(home: Path, claim_id: str) -> list[dict]:
    """Returns the version history of one claim, following its alias lineage so
    a section that was renamed still shows its full history under either name.

    Raises ClaimIdError if claim_id is not `<file>.md#<slug>`."""
    path, _ = parse_claim_id(claim_id)
    slugs = lineage_slugs(home, path, claim_id)
    entries = []
    for v in versioning.list_versions(home, path):
        if v["deleted"]:
            continue
        content = versioning.show_version(home, path, v["version"])
        if content is None:
            continue
        sections = {s.slug: s for s in extract_sections(content.decode("utf-8"))}
        present_slug = next((s for s in slugs if s in sections), None)
        if present_slug is None:
            continue
        entries.append({
            "version": v["version"],
            "slug": present_slug,
            "actor": v["actor"],
            "change_id": v["change_id"],
            "timestamp": v["timestamp"],
            "evidence": v["evidence"],
        })
    return entries


def guidelines_revert(home: Path, claim_id: str, to_version: int, actor: str) -> str | None:
    """Restores one claim's section text from an earlier file version, splicing it
    back into the current file content (replacing the section if it's still present
    under any alias, appending it otherwise) and recording the result as a new change.
    Raises ValueError if the target version has no content or lacks this claim,
    or ClaimIdError if claim_id is malformed."""
    path, _ = parse_claim_id(claim_id)
    slugs = lineage_slugs(home, path, claim_id)

    old_content = versioning.show_version(home, path, to_version)
    if old_content is None:
        raise ValueError(f"{path}@v{to_version} has no content to restore")
    old_sections = {s.slug: s for s in extract_sections(old_content.decode("utf-8"))}
    old_slug = next((s for s in slugs if s in old_sections), None)
    if old_slug is None:
        raise ValueError(f"claim not present in {path}@v{to_version}")
    restored_text = old_sections[old_slug].text

    current_path = Path(home) / path
    current_content = current_path.read_text() if current_path.exists() else ""
    current_sections = extract_sections(current_content)
    current_slug = next((s.slug for s in current_sections if s.slug in slugs), None)

    if current_slug is not None:
        parts = []
        for s in current_sections:
            parts.append(restored_text if s.slug == current_slug else s.text)
        new_content = "".join(parts)
    else:
        sep = "\n" if current_content and not current_content.endswith("\n") else ""
        new_content = current_content + sep + ("\n" if current_content else "") + restored_text

    current_path.parent.mkdir(parents=True, exist_ok=True)
    current_path.write_text(new_content)
    return capture_guideline_change(
        home, actor, [versioning.FileChange(path, new_content.encode())]
    )
