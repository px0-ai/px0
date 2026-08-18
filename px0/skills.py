"""px0 skills build: compile guidelines/ into skills/, the harness-facing
bundle. `work/` guideline folders are excluded, per the never-leaves-the-
machine rule -- they still reach the model at run time (inlined into
prompts), but are not written into a bundle a coding agent might carry
into a repository."""

import json
import shutil
from pathlib import Path

from px0 import paths, claims, config as config_mod, harness as harness_mod


def _sync_claude_symlink(skill_dir: Path, name: str, claude_skills_dir: Path, create: bool) -> None:
    """Manages ~/.claude/skills/px0-<name> symlink.
    If create=True, ensures symlink points to skill_dir.
    If create=False, removes the symlink if it exists and points to skill_dir."""
    symlink_path = claude_skills_dir / f"px0-{name}"

    if create:
        claude_skills_dir.mkdir(parents=True, exist_ok=True)
        if symlink_path.exists() or symlink_path.is_symlink():
            if symlink_path.is_symlink():
                try:
                    target = symlink_path.readlink()
                    if Path(target).resolve() != skill_dir.resolve():
                        print(f"Warning: symlink {symlink_path} exists and points to {target}, leaving it alone.")
                        return
                    else:
                        return
                except OSError:
                    pass
            else:
                print(f"Warning: path {symlink_path} exists and is not a symlink, leaving it alone.")
                return
        try:
            symlink_path.symlink_to(skill_dir.resolve())
        except OSError as e:
            print(f"Warning: failed to create symlink {symlink_path}: {e}")
    else:
        if symlink_path.is_symlink():
            try:
                target = symlink_path.readlink()
                if Path(target).resolve() == skill_dir.resolve():
                    symlink_path.unlink()
            except OSError:
                pass


def build(home: Path) -> list[str]:
    """Compiles every guidelines/*.md file into Claude Code skill bundles (skills/<name>/SKILL.md),
    prunes stale bundles, and manages ~/.claude/skills/ symlinks if the configured harness is Claude."""
    config = config_mod.load(paths.config_path(home))
    harness_cmd = config_mod.get(config, "model.harness_cmd", "claude -p")
    resolved_cmd = harness_mod.resolve_harness_cmd(harness_cmd)
    is_claude = resolved_cmd.lower().strip().startswith("claude")

    src_base = paths.guidelines_dir(home)
    dest_base = paths.skills_dir(home)
    dest_base.mkdir(parents=True, exist_ok=True)

    claude_skills_dir = Path("~/.claude/skills").expanduser()

    active_names = set()
    written = []

    for path in sorted(src_base.rglob("*.md")):
        rel = path.relative_to(src_base)
        if rel.parts and rel.parts[0] == "work":
            continue

        name = "-".join(rel.parts).rsplit(".", 1)[0]
        active_names.add(name)

        content = path.read_text(encoding="utf-8")
        sections = claims.extract_sections(content)
        if sections:
            description = "Guidelines: " + "; ".join(s.heading for s in sections)
        else:
            description = "Guidelines from " + str(rel)

        if len(description) > 300:
            description = description[:297] + "..."

        skill_dir = dest_base / name
        skill_dir.mkdir(parents=True, exist_ok=True)
        skill_file = skill_dir / "SKILL.md"

        frontmatter = f"---\nname: px0-{name}\ndescription: {json.dumps(description)}\n---\n\n"
        skill_file.write_text(frontmatter + content, encoding="utf-8")
        written.append(str(rel))

        if is_claude:
            _sync_claude_symlink(skill_dir, name, claude_skills_dir, create=True)

    # Prune stale bundles/symlinks
    if dest_base.exists():
        for child in dest_base.iterdir():
            if child.is_dir() and child.name not in active_names:
                _sync_claude_symlink(child, child.name, claude_skills_dir, create=False)
                shutil.rmtree(child)

    return written
