import pytest
import yaml
import shutil
from pathlib import Path
from px0 import skills, paths, config as config_mod

def test_description_derivation_and_truncation(tmp_home, monkeypatch):
    # Set up config to be non-claude so we don't mess with ~/.claude/skills in this test
    config_path = paths.config_path(tmp_home)
    config = config_mod.load(config_path)
    config["model"]["harness_cmd"] = "gemini"
    config_mod.save(paths.config_path(tmp_home), config)

    src_dir = paths.guidelines_dir(tmp_home)
    src_dir.mkdir(parents=True, exist_ok=True)
    
    # Guideline with headings
    g_file = src_dir / "test-guideline.md"
    headings = [f"## Heading {i}" for i in range(1, 40)]
    body = "\n\nContent\n\n".join(headings)
    g_file.write_text(body)

    written = skills.build(tmp_home)
    assert "test-guideline.md" in written

    # Check SKILL.md and description
    skill_file = paths.skills_dir(tmp_home) / "test-guideline" / "SKILL.md"
    assert skill_file.exists()
    content = skill_file.read_text()
    
    # Split frontmatter
    parts = content.split("---")
    frontmatter = yaml.safe_load(parts[1])
    assert frontmatter["name"] == "px0-test-guideline"
    desc = frontmatter["description"]
    assert desc.startswith("Guidelines: Heading 1; Heading 2")
    assert desc.endswith("...")
    assert len(desc) == 300


def test_description_fallback_no_headings(tmp_home):
    # Set non-claude
    config = config_mod.load(paths.config_path(tmp_home))
    config["model"]["harness_cmd"] = "gemini"
    config_mod.save(paths.config_path(tmp_home), config)

    src_dir = paths.guidelines_dir(tmp_home)
    g_file = src_dir / "no-headings.md"
    g_file.write_text("No headings here at all, just plain text.")

    skills.build(tmp_home)
    skill_file = paths.skills_dir(tmp_home) / "no-headings" / "SKILL.md"
    parts = skill_file.read_text().split("---")
    frontmatter = yaml.safe_load(parts[1])
    assert frontmatter["description"] == "Guidelines from no-headings.md"


def test_work_folder_exclusion(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    config["model"]["harness_cmd"] = "gemini"
    config_mod.save(paths.config_path(tmp_home), config)

    src_dir = paths.guidelines_dir(tmp_home)
    
    # Create work file
    work_dir = src_dir / "work"
    work_dir.mkdir(parents=True, exist_ok=True)
    work_file = work_dir / "secret.md"
    work_file.write_text("## Secret Heading\nsecret text")

    written = skills.build(tmp_home)
    assert "work/secret.md" not in written
    assert not (paths.skills_dir(tmp_home) / "work" / "secret").exists()


def test_prune_stale_removes_orphaned_skills_and_symlinks(tmp_home, monkeypatch, tmp_path):
    # Setup Claude harness and a custom claude_skills_dir to avoid real ~/.claude/skills
    config = config_mod.load(paths.config_path(tmp_home))
    config["model"]["harness_cmd"] = "claude"
    config_mod.save(paths.config_path(tmp_home), config)

    fake_claude_skills = tmp_path / "fake_claude_skills"
    fake_claude_skills.mkdir()
    monkeypatch.setattr(Path, "expanduser", lambda self: fake_claude_skills if "skills" in str(self) or ".claude" in str(self) else self)

    src_dir = paths.guidelines_dir(tmp_home)
    (src_dir / "active.md").write_text("## Active\ntext")

    # Pre-create a stale skill directory and symlink
    stale_skill_dir = paths.skills_dir(tmp_home) / "stale"
    stale_skill_dir.mkdir(parents=True, exist_ok=True)
    (stale_skill_dir / "SKILL.md").write_text("old")
    
    stale_symlink = fake_claude_skills / "px0-stale"
    stale_symlink.symlink_to(stale_skill_dir.resolve())

    # Build
    skills.build(tmp_home)

    # Stale skill should be gone from store and fake claude skills
    assert not stale_skill_dir.exists()
    assert not stale_symlink.exists()

    # Active skill should exist
    assert (paths.skills_dir(tmp_home) / "active" / "SKILL.md").exists()
    assert (fake_claude_skills / "px0-active").exists()


def test_symlink_behavior_with_different_harnesses(tmp_home, monkeypatch, tmp_path):
    fake_claude_skills = tmp_path / "fake_claude_skills"
    fake_claude_skills.mkdir()
    monkeypatch.setattr(Path, "expanduser", lambda self: fake_claude_skills if "skills" in str(self) or ".claude" in str(self) else self)

    src_dir = paths.guidelines_dir(tmp_home)
    (src_dir / "g.md").write_text("## Title\ntext")

    # 1. Harness is gemini (no symlink)
    config = config_mod.load(paths.config_path(tmp_home))
    config["model"]["harness_cmd"] = "gemini"
    config_mod.save(paths.config_path(tmp_home), config)

    skills.build(tmp_home)
    assert not (fake_claude_skills / "px0-g").exists()

    # 2. Harness is claude (creates symlink)
    config["model"]["harness_cmd"] = "claude"
    config_mod.save(paths.config_path(tmp_home), config)

    skills.build(tmp_home)
    assert (fake_claude_skills / "px0-g").is_symlink()
    assert (fake_claude_skills / "px0-g").resolve() == (paths.skills_dir(tmp_home) / "g").resolve()


def test_warning_when_non_symlink_exists(tmp_home, monkeypatch, tmp_path, capsys):
    fake_claude_skills = tmp_path / "fake_claude_skills"
    fake_claude_skills.mkdir()
    monkeypatch.setattr(Path, "expanduser", lambda self: fake_claude_skills if "skills" in str(self) or ".claude" in str(self) else self)

    src_dir = paths.guidelines_dir(tmp_home)
    (src_dir / "g.md").write_text("## Title\ntext")

    # Create a real file where the symlink wants to go
    conflict_path = fake_claude_skills / "px0-g"
    conflict_path.write_text("real conflict file")

    config = config_mod.load(paths.config_path(tmp_home))
    config["model"]["harness_cmd"] = "claude"
    config_mod.save(paths.config_path(tmp_home), config)

    skills.build(tmp_home)

    captured = capsys.readouterr()
    assert "Warning: path" in captured.out
    assert "exists and is not a symlink" in captured.out
    assert conflict_path.read_text() == "real conflict file" # Left untouched


def test_skills_build_integration_valid_yaml(tmp_home, monkeypatch, tmp_path):
    # Set gemini harness to skip symlinks
    config = config_mod.load(paths.config_path(tmp_home))
    config["model"]["harness_cmd"] = "gemini"
    config_mod.save(paths.config_path(tmp_home), config)

    # Let's populate the starter guidelines
    skills.build(tmp_home)

    # Pick one of the built skills and load it back
    skill_file = paths.skills_dir(tmp_home) / "commit-messages" / "SKILL.md"
    assert skill_file.exists()
    content = skill_file.read_text()
    
    parts = content.split("---")
    frontmatter = yaml.safe_load(parts[1])
    assert frontmatter["name"] == "px0-commit-messages"
    assert "Guidelines: " in frontmatter["description"]
