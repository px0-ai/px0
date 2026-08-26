"""What px0 knows about you, as opposed to what you have read.

The store already had two kinds of knowledge and neither was this one.
`brain/` is material you deliberately ingested -- posts, papers, docs -- and it
answers questions about the world. `guidelines/` is conventions you wrote down
-- how to word a commit, what a Go review checks -- and it shapes how output
reads. Between them there was nothing that remembered *you*: that standup goes
out before 09:30, that "the API repo" means one particular repository, that a
person you keep mentioning is on your team.

So every run started from nothing, and every correction had to be made again
the next time. That is the difference between a tool you operate and an
assistant: the assistant remembers.

The shape is deliberately the same as everything else in the store -- one
Markdown file per fact, in a folder you own, versioned so you can see what px0
learned and when, and revertible when it learns something wrong. Two things
follow from that and are worth being explicit about:

**A memory is editable, because it will be wrong.** px0 writes these as a side
effect of conversations, and an assistant that silently accumulates unreviewable
beliefs about you is the failure mode to design against, not a feature. Every
one of them is a file you can open, correct, or delete.

**Memory never leaves the machine on its own.** It is inlined into prompts the
same way guidelines are -- which is to say, into the harness you already
trust -- and goes nowhere else. `px0 store export` carries it, because an
export is how a store reaches your other machine and an assistant that forgets
everything on the new one is not one.
"""

import re
from dataclasses import dataclass, replace
from datetime import datetime, timezone
from pathlib import Path

import yaml

from px0 import config as config_mod, paths

# What a memory can be about. Kinds are a filing aid, not a schema: they make a
# listing scannable and let a run ask for only the kinds it needs.
KINDS = ("fact", "preference", "person", "project", "place")

# How much memory a single run is allowed to inline. A store that has been
# running for a year should not quietly turn every prompt into a biography, so
# past this the most recently useful memories win and the rest wait to be
# retrieved rather than pushed.
DEFAULT_BUDGET_CHARS = 4000

# Below this a clipped memory has stopped saying anything, so it is left out
# rather than included as a stub.
MIN_MEMORY_CHARS = 80

_SLUG_RE = re.compile(r"[^a-z0-9]+")


class MemoryError_(Exception):
    """Raised when a memory cannot be written or found."""
    pass


@dataclass
class Memory:
    """One remembered fact: its file, its frontmatter, and its text."""
    name: str
    path: Path
    kind: str
    subject: str
    text: str
    learned: str = ""
    source: str = ""
    pinned: bool = False

    @property
    def summary(self) -> str:
        """The one line this memory is listed and matched by."""
        first = next((l.strip() for l in self.text.splitlines() if l.strip()), "")
        return (self.subject or first)[:120]


def memory_dir(home: Path | None = None) -> Path:
    """Where remembered facts live: a folder of Markdown, beside the rest."""
    return paths.memory_dir(home)


def slugify(text: str, fallback: str = "note") -> str:
    """A filename from a fact's own words.

    Named rather than numbered so the directory is browsable: a folder of
    `mem-0001.md` is a database with the ergonomics of a folder, and the whole
    point of keeping these as files is that a person can find one.
    """
    slug = _SLUG_RE.sub("-", (text or "").lower()).strip("-")
    slug = "-".join(slug.split("-")[:8])
    return slug[:60] or fallback


def parse(path: Path) -> Memory:
    """Reads one memory file. Never raises on content: a file px0 cannot fully
    understand is still one the user can read, and taking `px0 memory list`
    down over a stray colon is not a trade worth making."""
    text = path.read_text()
    front: dict = {}
    body = text
    if text.startswith("---"):
        parts = text.split("---", 2)
        if len(parts) >= 3:
            try:
                loaded = yaml.safe_load(parts[1])
                front = loaded if isinstance(loaded, dict) else {}
            except yaml.YAMLError:
                front = {}
            body = parts[2].lstrip("\n")
    kind = str(front.get("kind") or "fact")
    return Memory(
        name=path.stem,
        path=path,
        kind=kind if kind in KINDS else "fact",
        subject=str(front.get("subject") or "").strip(),
        text=body.strip(),
        learned=str(front.get("learned") or "").strip(),
        source=str(front.get("source") or "").strip(),
        pinned=bool(front.get("pinned")),
    )


def render(kind: str, subject: str, text: str, learned: str = "",
           source: str = "", pinned: bool = False) -> str:
    """Composes a memory file: frontmatter, then the fact in plain words."""
    front = {"kind": kind, "subject": subject,
             "learned": learned or datetime.now(timezone.utc).date().isoformat()}
    if source:
        front["source"] = source
    if pinned:
        front["pinned"] = True
    head = yaml.safe_dump(front, sort_keys=False, allow_unicode=True,
                          default_flow_style=False, width=88).strip()
    return f"---\n{head}\n---\n\n{text.strip()}\n"


def load_all(home: Path) -> dict[str, Memory]:
    """Every memory in the store, keyed by name."""
    base = memory_dir(home)
    if not base.exists():
        return {}
    out: dict[str, Memory] = {}
    for path in sorted(base.rglob("*.md")):
        try:
            out[path.stem] = parse(path)
        except OSError:
            continue
    return out


def remember(home: Path, text: str, *, kind: str = "fact", subject: str = "",
             source: str = "", name: str | None = None,
             pinned: bool = False, actor: str = "user") -> Memory:
    """Writes down one fact and returns it.

    Recorded as a versioned change like anything else in the store, so
    `px0 changes list` shows what px0 learned and when, and
    `px0 changes revert` unlearns it. That history is what makes an assistant
    that writes to its own memory reviewable rather than merely convenient.

    Writing to a name that already exists replaces that memory rather than
    appending: a fact that has changed is not two facts, and a memory folder
    that accumulates every past belief about the same subject is one that will
    contradict itself inside a prompt.
    """
    from px0 import versioning  # deferred: versioning reaches back into the store

    if not (text or "").strip():
        raise MemoryError_("a memory needs something to remember")
    if kind not in KINDS:
        raise MemoryError_(f"kind must be one of {', '.join(KINDS)}")

    # Slugified whether it was derived or handed in: either way it becomes a
    # filename, and `slugify` is what keeps it one path component.
    name = slugify(name) if name else slugify(subject or text)
    content = render(kind, subject or text.strip().split("\n")[0][:80],
                     text, source=source, pinned=pinned)
    dest = memory_dir(home) / f"{name}.md"
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(content)
    versioning.record_change(
        home, f"memory:{actor}",
        [versioning.FileChange(f"memory/{name}.md", content.encode(),
                               f"remembered: {subject or text[:60]}")])
    return parse(dest)


def forget(home: Path, name: str, actor: str = "user") -> bool:
    """Removes one memory, keeping it in history. Returns whether it existed."""
    from px0 import versioning

    dest = memory_dir(home) / f"{name}.md"
    if not dest.exists():
        return False
    dest.unlink()
    versioning.record_change(
        home, f"memory:{actor}",
        [versioning.FileChange(f"memory/{name}.md", None, "forgotten")])
    return True


def _terms(text: str) -> set[str]:
    """The words a match is made on, minus the ones every sentence has."""
    stop = {"the", "a", "an", "and", "or", "of", "to", "in", "on", "for", "my",
            "me", "i", "is", "are", "was", "it", "this", "that", "with", "at",
            "by", "from", "as", "be", "do", "does", "what", "when", "which"}
    words = re.findall(r"[a-z0-9]+", (text or "").lower())
    return {w for w in words if len(w) > 2 and w not in stop}


def relevant(home: Path, query: str, budget: int = DEFAULT_BUDGET_CHARS,
             kinds: tuple[str, ...] | None = None,
             pinned_first: bool = True) -> list[Memory]:
    """The memories worth putting in front of a model for this query.

    Local arithmetic, not a model call and not the retrieval index: memories
    are short, few, and read on every run, so paying for an embedding pass to
    choose between forty lines of text would cost more than inlining all of
    them. Pinned memories rank first, which is how "never crowded out" is kept
    -- they are offered room before anything competes for it -- then whatever
    shares the most words with the query, then the rest until the budget runs
    out. A single memory longer than the whole budget is clipped rather than
    admitted, so the ceiling holds whatever is in the folder.
    """
    memories = [m for m in load_all(home).values()
                if kinds is None or m.kind in kinds]
    if not memories:
        return []
    wanted = _terms(query)

    def score(m: Memory) -> tuple[int, int]:
        overlap = len(wanted & _terms(f"{m.subject} {m.text}"))
        return ((1 if m.pinned else 0) if pinned_first else 0, overlap)

    ranked = sorted(memories, key=score, reverse=True)
    chosen, spent = [], 0
    for m in ranked:
        overhead = len(m.subject) + 4
        room = budget - spent - overhead
        if room < MIN_MEMORY_CHARS and chosen:
            continue
        if len(m.text) > room:
            # Clipped, not skipped and not admitted whole. Admitting it whole
            # is what the first-item exemption used to do, and one long memory
            # then put fifty thousand characters into every prompt from a
            # setting that says four thousand.
            keep = max(room, MIN_MEMORY_CHARS)
            m = replace(m, text=m.text[:keep].rstrip() + " [...]")
        chosen.append(m)
        spent += len(m.text) + overhead
    return chosen


def as_prompt_block(memories: list[Memory]) -> str:
    """The memories as one block of text for a prompt.

    Labelled as things px0 was told rather than things it worked out, so a
    model treats a remembered preference as standing instruction and a
    remembered fact as context it may still check -- and so a wrong memory
    reads as a wrong belief rather than as ground truth.
    """
    if not memories:
        return ""
    lines = ["# What px0 knows about you",
             "",
             "These are things you have told px0, or that it recorded from your "
             "own corrections. Treat them as standing context. If one conflicts "
             "with what you are given now, prefer what you are given now and "
             "say the memory looks stale.",
             ""]
    for m in memories:
        subject = f"**{m.subject}** — " if m.subject else ""
        lines.append(f"- {subject}{m.text.strip()}")
    return "\n".join(lines)


# --- memories px0 proposes for itself -------------------------------------
#
# Everything above is memory as a notepad: it exists, and someone has to
# remember to write in it, which is exactly the failure it was built to fix.
# What follows is the other half -- px0 reading the corrections it already has
# and offering to keep them.
#
# The line held throughout: px0 *proposes*, and a person accepts. An assistant
# that quietly accumulates unreviewed beliefs about you is the thing to design
# against, and one confirmation is what keeps that true while still meaning you
# say a thing once rather than never.

_SUGGEST_INSTRUCTIONS = """\
You are deciding what a personal automation tool should remember about its user.

You are given corrections the user made: notes they left on runs that came back
wrong, and things they said while correcting an answer. Some of those are
one-off bugs. Some are standing facts about the user, their work, or how they
want things done -- and those are worth keeping, because otherwise the same
correction has to be made again next week.

Return ONE JSON array, and nothing else. Each entry:

{"text": "<the fact, in one plain sentence, written as a standing truth rather \
than as a complaint>",
 "subject": "<what it is about, two or three words>",
 "kind": "fact|preference|person|project|place",
 "why": "<the correction this came from, quoted or closely paraphrased>"}

Hold to these:

- Keep only what will still be true next month. "The digest covered the wrong \
week" is a bug; "the week runs Monday to Friday" is a fact.
- One fact per entry, in the user's own terms.
- Never invent a fact that is not supported by what you were given.
- An empty array is the right answer when nothing here is worth keeping.
"""


def _correction_sources(config: dict, limit: int = 20) -> list[dict]:
    """The corrections px0 already has on disk, newest first.

    Two places a standing fact tends to be sitting in plain sight: the note on
    a run someone marked bad, and what they said while correcting an answer in
    a conversation. Both were already being recorded and neither was being read
    for this.
    """
    from px0 import runs as runs_mod

    found = []
    for record in runs_mod.list_records(config):
        review = record.get("review") or {}
        if review.get("verdict") == "bad" and review.get("note"):
            found.append({"kind": "run_marked_bad",
                          "workflow": record.get("workflow_id"),
                          "run": record.get("id"),
                          "text": review["note"],
                          "when": record.get("start_time")})
        for correction in record.get("corrections") or []:
            found.append({"kind": "conversation",
                          "run": record.get("id"),
                          "text": str(correction)[:400],
                          "when": record.get("start_time")})
        if len(found) >= limit:
            break
    return found[:limit]


def suggest(home: Path, config: dict, extra: list[str] | None = None,
            limit: int = 20) -> list[dict]:
    """Proposes memories from the corrections px0 already has.

    Returns candidates, never writes. Anything already remembered under the
    same subject is dropped here rather than shown and then discarded on
    accept, so the list is what would actually be new.

    Raises nothing on a bad model reply: an empty list is a perfectly good
    answer to "is there anything worth keeping", and failing the command over
    a malformed suggestion would be worse than making none.
    """
    import json as json_mod

    from px0 import builder, harness

    corrections = _correction_sources(config, limit=limit)
    for text in extra or []:
        corrections.append({"kind": "said", "text": text})
    if not corrections:
        return []

    known = {m.subject.lower().strip() for m in load_all(home).values() if m.subject}
    prompt = (f"{_SUGGEST_INSTRUCTIONS}\n---\nCORRECTIONS\n"
              f"{json_mod.dumps(corrections, indent=2, default=str)}\n\n"
              f"---\nALREADY REMEMBERED\n{sorted(known) or 'nothing yet'}\n")
    try:
        raw = harness.invoke(config, prompt, timeout=90)
        data = builder._extract_json(raw, want_array=True)
    except (harness.HarnessError, builder.BuilderError):
        return []
    if not isinstance(data, list):
        return []

    out = []
    for entry in data:
        if not isinstance(entry, dict):
            continue
        text = str(entry.get("text") or "").strip()
        subject = str(entry.get("subject") or "").strip()
        if not text or subject.lower() in known:
            continue
        kind = str(entry.get("kind") or "fact").strip()
        out.append({"text": text, "subject": subject,
                    "kind": kind if kind in KINDS else "fact",
                    "why": str(entry.get("why") or "").strip()})
    return out


def budget_chars(config: dict) -> int:
    """How much memory this store lets a single run inline."""
    try:
        return max(0, int(config_mod.get(config, "memory.budget_chars",
                                         DEFAULT_BUDGET_CHARS)))
    except (TypeError, ValueError):
        return DEFAULT_BUDGET_CHARS


def enabled(config: dict) -> bool:
    """Whether runs inline memory at all. Off means px0 keeps the folder and
    stops reading it, which is the setting for someone who wants to write
    memories by hand and control exactly where they land."""
    return bool(config_mod.get(config, "memory.enabled", True))
