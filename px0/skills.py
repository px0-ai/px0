"""px0 skills build: compile guidelines/ into skills/, the harness-facing
bundle. `work/` guideline folders are excluded, per the never-leaves-the-
machine rule -- they still reach the model at run time (inlined into
prompts), but are not written into a bundle a coding agent might carry
into a repository."""

from pathlib import Path

from px0 import paths


def build(home: Path) -> list[str]:
    """Copies every guidelines/*.md file into skills/, mirroring the relative
    path, except files under a top-level work/ folder. Overwrites existing
    files in skills/. Returns the list of relative paths written."""
    written = []
    src_base = paths.guidelines_dir(home)
    dest_base = paths.skills_dir(home)
    for path in sorted(src_base.rglob("*.md")):
        rel = path.relative_to(src_base)
        if rel.parts and rel.parts[0] == "work":
            continue
        dest = dest_base / rel
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(path.read_text())
        written.append(str(rel))
    return written
