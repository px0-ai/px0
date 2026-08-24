"""Guideline file model: YAML frontmatter (`name`, `description`) as the index
a relevance pass reads, the Markdown body as the text a run inlines.

Shaped like a skill on purpose. A guideline is only worth attaching when the
workflow's output is judged against it, and that judgement needs one line
saying what the file covers and when it applies -- not the file's whole body.
So the description is written into the frontmatter when the guideline is
drafted, and selection reads nothing but those lines. Matching on the body was
what attached `commit-messages.md` to a workflow that merely *reads* commits.

Files written before frontmatter existed still parse: the name falls back to
the filename and the description to the first `## ` heading, which is enough to
list them and enough for a relevance pass to reject them.
"""

from dataclasses import dataclass
from pathlib import Path

import yaml

from px0 import paths

# Guidelines under this folder are never offered to a workflow automatically,
# matching `brain/work/`: the folder means "mine, and not something px0 hands
# to a model on its own initiative".
WORK_PREFIX = "work/"


@dataclass
class Guideline:
    """One guideline file: its frontmatter identity and the body a run inlines."""
    rel: str              # store-relative under guidelines/, e.g. "code-review/go.md"
    path: Path
    name: str
    description: str
    body: str
    # False for a file written before frontmatter was the format, or one a hand
    # edit stripped it from. It still works; `px0 doctor` says which files
    # would select better with a description.
    described: bool = True

    @property
    def summary(self) -> str:
        """The one line that stands for this file: its description, or its first
        rule when it has none. Used for both the listing and the relevance pass,
        so what the user reads is what the model is choosing from."""
        return self.description or first_rule(self.body)

    @property
    def is_work(self) -> bool:
        """Whether this file sits under `work/`, and so is never auto-attached."""
        return self.rel.startswith(WORK_PREFIX)


def first_rule(body: str) -> str:
    """The body's first `## ` heading. The headings are the rules, so the first
    one says more about the file than a byte count or a claim tally would."""
    for line in body.splitlines():
        if line.startswith("## "):
            return line[3:].strip()
    return ""


def parse(path: Path, rel: str | None = None) -> Guideline:
    """Reads one guideline file. Never raises on content: a guideline the store
    cannot fully understand is still a guideline the user can read and edit,
    and taking `guidelines list` down over a stray colon is not a trade worth
    making. Unreadable frontmatter degrades to no frontmatter."""
    text = path.read_text()
    rel = rel or path.name
    front, body = split_frontmatter(text)
    if front is None:
        return Guideline(rel=rel, path=path, name=Path(rel).stem, description="",
                         body=body, described=False)
    return Guideline(
        rel=rel,
        path=path,
        name=str(front.get("name") or Path(rel).stem).strip(),
        description=str(front.get("description") or "").strip(),
        body=body,
        described=bool(str(front.get("description") or "").strip()),
    )


def split_frontmatter(text: str) -> tuple[dict | None, str]:
    """Splits a guideline file into (frontmatter mapping, body).

    Returns `(None, text)` when there is no usable frontmatter, so the caller
    handles "written before this format" and "frontmatter is broken" the same
    way: as a file that is all body.
    """
    if not text.startswith("---"):
        return None, text
    parts = text.split("---", 2)
    if len(parts) < 3:
        return None, text
    try:
        front = yaml.safe_load(parts[1])
    except yaml.YAMLError:
        return None, text
    if not isinstance(front, dict):
        return None, text
    return front, parts[2].lstrip("\n")


def render(name: str, description: str, body: str) -> str:
    """Composes a guideline file: frontmatter, then the rules.

    `yaml.safe_dump` rather than an f-string, so a description containing a
    colon or a quote produces a file that still parses.
    """
    front = yaml.safe_dump({"name": name, "description": description},
                           sort_keys=False, default_flow_style=False,
                           allow_unicode=True, width=88).strip()
    return f"---\n{front}\n---\n\n{body.strip()}\n"


def name_for(rel: str) -> str:
    """The name a guideline at this relative path gets: the filename without its
    extension, the way a skill is named by its directory. Folders group topics
    (`code-review/go.md`) and are not part of the name, which is what
    `px0 guidelines edit go` already resolves by."""
    return Path(rel).stem


def load_all(home: Path, include_work: bool = True) -> dict[str, Guideline]:
    """Every guideline in the store, keyed by its store-relative path."""
    base = paths.guidelines_dir(home)
    out: dict[str, Guideline] = {}
    if not base.exists():
        return out
    for path in sorted(base.rglob("*.md")):
        rel = str(path.relative_to(base))
        if not include_work and rel.startswith(WORK_PREFIX):
            continue
        try:
            out[rel] = parse(path, rel)
        except OSError:
            continue
    return out


def attachable(home: Path) -> list[Guideline]:
    """The guidelines a build may attach to a workflow: everything but `work/`."""
    return list(load_all(home, include_work=False).values())


def body_of(home: Path, rel: str) -> str:
    """The text a run inlines for the guideline at `rel`: its body, never its
    frontmatter. The frontmatter is how px0 finds the file; inlining it would
    spend prompt on machinery the model has no use for."""
    return parse(paths.guidelines_dir(home) / rel, rel).body
