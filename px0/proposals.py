"""Pending guideline edits. Ingestion, corrections, and verification never
touch guideline files directly -- each proposes a pending edit here, and
`consolidate` / `px0 guidelines review` is where the user disposes of them.
A proposal that is neither accepted nor edited is dismissed by deleting its
file; nothing is recorded about the dismissal."""

import json
import re
import secrets
from dataclasses import dataclass, asdict
from datetime import datetime, timezone
from pathlib import Path

from px0 import claims, harness
from px0 import paths, versioning
from px0 import workflow as workflow_mod


@dataclass
class Proposal:
    """One pending guideline edit awaiting user review, with the evidence that
    generated it (a knowledge source, or a manual correction)."""
    id: str
    target_file: str        # relative to guidelines/
    action: str              # new | amend | retire
    claim: str                # heading text
    body: str                 # full proposed section text (heading + body)
    evidence_source: str
    evidence_anchor: str
    evidence_quote: str
    created_at: str


def _proposal_path(home: Path, proposal_id: str) -> Path:
    """Path to a proposal's JSON file under .state/proposals/."""
    return paths.proposals_dir(home) / f"{proposal_id}.json"


def save_proposal(home: Path, p: Proposal) -> None:
    """Writes a proposal to disk as JSON."""
    paths.proposals_dir(home).mkdir(parents=True, exist_ok=True)
    _proposal_path(home, p.id).write_text(json.dumps(asdict(p), indent=2))


def list_proposals(home: Path) -> list[Proposal]:
    """Loads all pending proposals, skipping any file that fails to parse."""
    d = paths.proposals_dir(home)
    if not d.exists():
        return []
    out = []
    for f in sorted(d.glob("*.json")):
        try:
            out.append(Proposal(**json.loads(f.read_text())))
        except (json.JSONDecodeError, TypeError):
            continue
    return out


def dismiss(home: Path, proposal_id: str) -> None:
    """Deletes a proposal's file; no-op if it doesn't exist. Nothing is recorded
    about a dismissal beyond the file's absence."""
    p = _proposal_path(home, proposal_id)
    if p.exists():
        p.unlink()


_JSON_ARRAY_RE = re.compile(r"\[.*\]", re.DOTALL)


def propose_from_knowledge(home: Path, config: dict, knowledge_file: Path) -> list[Proposal]:
    """Asks the harness to read one knowledge file and propose zero or more
    guideline edits, saving each as a pending Proposal. Returns [] if the model
    response has no JSON array (rather than raising)."""
    from px0 import knowledge as knowledge_mod

    header, body = knowledge_mod.read_header(knowledge_file)
    guideline_files = sorted(
        str(p.relative_to(paths.guidelines_dir(home)))
        for p in paths.guidelines_dir(home).rglob("*.md")
    )
    prompt = (
        "You review material the user has read and propose guideline edits.\n"
        f"Existing guideline topic files: {guideline_files}\n\n"
        "Read the material below. Propose zero or more concrete, actionable "
        "claims the user might want as guidelines. Respond with ONLY a JSON "
        "array, each item: {\"target_file\": one of the existing topic files "
        "or a new short filename, \"action\": \"new\"|\"amend\"|\"retire\", "
        "\"claim\": short heading text, \"body\": 1-3 sentence claim body, "
        "\"evidence_anchor\": a heading or short locator in the material}.\n"
        "If nothing is worth proposing, respond with [].\n\n"
        f"--- material ({knowledge_file.name}) ---\n{body[:6000]}"
    )
    raw = harness.invoke(config, prompt, timeout=90)
    match = _JSON_ARRAY_RE.search(raw)
    if not match:
        return []
    items = json.loads(match.group(0))

    out = []
    for item in items:
        pid = f"prop_{datetime.now(timezone.utc).strftime('%Y%m%d%H%M%S')}_{secrets.token_hex(3)}"
        p = Proposal(
            id=pid,
            target_file=item["target_file"],
            action=item.get("action", "new"),
            claim=item["claim"],
            body=item["body"],
            evidence_source=str(knowledge_file),
            evidence_anchor=item.get("evidence_anchor", ""),
            evidence_quote=item.get("evidence_quote", ""),
            created_at=datetime.now(timezone.utc).isoformat(),
        )
        save_proposal(home, p)
        out.append(p)
    return out


def _apply_proposal_to_content(current: str, p: Proposal) -> str:
    """Splices one accepted proposal into a guideline file's current content:
    replaces the matching section for "amend", removes it for "retire" (no-op if
    already absent), replaces it if a same-slug section exists, otherwise appends
    a new section at the end."""
    heading_line = f"## {p.claim}\n"
    section_text = f"{heading_line}\n{p.body.strip()}\n"
    slug = claims.slugify(p.claim)
    sections = claims.extract_sections(current)
    match = next((s for s in sections if s.slug == slug), None)

    if p.action == "retire":
        if match is None:
            return current
        return "".join(s.text for s in sections if s.slug != slug)

    if match is not None:
        parts = [section_text if s.slug == slug else s.text for s in sections]
        return "".join(parts)

    sep = "\n" if current and not current.endswith("\n\n") else ""
    return current + sep + ("\n" if current and not current.endswith("\n") else "") + section_text


def apply_many(home: Path, actor: str, decisions: list[dict]) -> str | None:
    """decisions: [{"proposal": Proposal, "edited_body": str|None}]. Batches
    every accepted proposal into one change, grouped by target file."""
    by_file: dict[str, list[Proposal]] = {}
    for d in decisions:
        p = d["proposal"]
        if d.get("edited_body"):
            p = Proposal(**{**asdict(p), "body": d["edited_body"]})
        by_file.setdefault(p.target_file, []).append(p)

    file_changes = []
    for target_file, props in by_file.items():
        rel = f"guidelines/{target_file}"
        path = paths.guidelines_dir(home) / target_file
        current = path.read_text() if path.exists() else ""
        for p in props:
            current = _apply_proposal_to_content(current, p)
        evidence = json.dumps([
            {"claim": p.claim, "source": p.evidence_source, "anchor": p.evidence_anchor}
            for p in props
        ])
        file_changes.append(versioning.FileChange(rel, current.encode(), evidence))
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(current)

    change_id = claims.capture_guideline_change(home, actor, file_changes)
    for d in decisions:
        dismiss(home, d["proposal"].id)
    return change_id


def unreferenced_guideline_files(home: Path) -> list[str]:
    """Guideline files that no workflow lists under its `guidelines:`, sorted."""
    all_files = {
        str(p.relative_to(paths.guidelines_dir(home)))
        for p in paths.guidelines_dir(home).rglob("*.md")
    }
    referenced: set[str] = set()
    for wf in workflow_mod.load_all(home).values():
        referenced.update(wf.guidelines)
    return sorted(all_files - referenced)


def decayed_claims(home: Path, decay_days: int = 180) -> list[dict]:
    """Claims whose section has not changed in `decay_days`."""
    out = []
    now = datetime.now(timezone.utc)
    for path in paths.guidelines_dir(home).rglob("*.md"):
        rel = str(path.relative_to(home))
        content = path.read_text()
        for section in claims.extract_sections(content):
            claim_id = f"{rel}#{section.slug}"
            log = claims.guidelines_log(home, claim_id)
            if not log:
                continue
            last = log[-1]
            age = now - datetime.fromisoformat(last["timestamp"])
            if age.days >= decay_days:
                out.append({"claim": claim_id, "days_since_reinforced": age.days})
    return out


def find_contradictions(config: dict, home: Path) -> list[dict]:
    """Best-effort: asks the model backend to spot contradicting claims
    across guideline files. Returns [] (with the caller told why) if the
    harness is unavailable rather than fabricating a result."""
    all_claims = []
    for path in paths.guidelines_dir(home).rglob("*.md"):
        rel = str(path.relative_to(home))
        for section in claims.extract_sections(path.read_text()):
            all_claims.append(f"{rel}#{section.slug}: {section.body.strip()[:200]}")
    if len(all_claims) < 2:
        return []
    prompt = (
        "Here are guideline claims from a personal engineering knowledge base. "
        "Find pairs that contradict each other. Respond with ONLY a JSON array "
        "of {\"a\": claim_id, \"b\": claim_id, \"why\": short reason}. "
        "Empty array if none.\n\n" + "\n".join(all_claims)
    )
    try:
        raw = harness.invoke(config, prompt, timeout=90)
    except harness.HarnessError:
        return []
    match = _JSON_ARRAY_RE.search(raw)
    if not match:
        return []
    try:
        return json.loads(match.group(0))
    except json.JSONDecodeError:
        return []
