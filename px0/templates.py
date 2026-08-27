"""`px0 workflows templatize`: turning one person's workflow into one anyone can run.

A workflow in a store is full of one installation's facts: the repository it
reads, the channel it posts to, the folder it files things under, the teammate
it chases. That is exactly right for the person who built it, and exactly what
stops it being shareable -- the file describes a job, and the job is buried in
somebody else's account names.

Templatizing separates the two. The literals that belong to an installation
become `{{input.<name>}}` references, and each one is declared in a `vars:`
block saying what it is and what somebody else would plausibly put there. What
is left is the job.

Four rules shape this module, and each is a rule because the obvious
alternative is worse:

**The scan decides what is eligible; the model only names it.** Every candidate
comes from a deterministic walk of the file. The model chooses among them,
names them, and writes their descriptions -- it cannot introduce a literal of
its own, because a model inventing a string to replace is a model editing text
it misread, and the edit lands in a file the user is about to publish. A
proposed var whose literal is not in the candidate set is dropped on the way in.

**Only what a run can actually resolve is templatized.** `{{input.x}}` is
resolved in exactly two places: an input's `args`/`retrieve`, and the body. So
those are the only surfaces touched. A cron expression is validated when the
file loads and the daemon has no `--input` to hand it; an `output.path` accepts
clock placeholders and nothing else. Templatizing either would produce a file
that is shareable and unrunnable.

**A var is a value, not a location.** One literal that appears in three places
becomes one var substituted three times. The alternative -- a var per site --
means a stranger filling in the same repository name three times and getting it
wrong once.

**Nothing is applied without being shown.** The candidates are printed before
the model is asked anything, the proposal is printed before the file is
touched, and the result is diffed and validated before it is written. The write
goes through `authoring`, so `px0 changes revert` undoes it.
"""

import json
import re
import shlex
from dataclasses import dataclass, field
from pathlib import Path

import yaml

from px0 import builder, harness
from px0 import workflow as workflow_mod

# A literal shorter than this is not worth a var and is dangerous to substitute:
# a two-character string appears inside longer words, and the body substitution
# is a plain replace.
MIN_LITERAL = 3

# Past this, an argument is prose rather than a setting -- a message template, a
# retrieval query written as a sentence. Someone installing a template fills in
# a channel name, not three lines of instructions.
MAX_LITERAL = 120

# Enough for a stranger to recognize the shape of the value without the block
# turning into a data dump.
MAX_VALUES = 6

MAX_CANDIDATES = 40


class _Dumper(yaml.SafeDumper):
    """SafeDumper that indents block sequences under their key.

    `vars:` and `inputs:` are read by whoever installs the template, and
    PyYAML's default puts a list item at its parent's own indent -- valid YAML
    that reads as though the list were a sibling of the key. This is also the
    shape every workflow file in the docs is written in, so re-dumping a file
    does not reformat a store's whole frontmatter into a second style.
    """

    def increase_indent(self, flow=False, indentless=False):
        return super().increase_indent(flow, False)


class TemplateError(Exception):
    """Raised when a template cannot be proposed, read, or applied."""


# What a literal looks like when it plainly belongs to one installation. Used
# to label a candidate and to seed its name -- never to decide eligibility on
# its own, since every string argument is eligible regardless of shape.
_KINDS: tuple[tuple[str, re.Pattern], ...] = (
    ("channel", re.compile(r"^#[A-Za-z0-9][A-Za-z0-9._-]*$")),
    ("email", re.compile(r"^[^@\s]+@[^@\s]+\.[A-Za-z]{2,}$")),
    ("url", re.compile(r"^https?://\S+$")),
    ("handle", re.compile(r"^@[A-Za-z0-9._-]{2,}$")),
    ("repo", re.compile(r"^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$")),
    ("path", re.compile(r"^(?:~|\.{0,2}/)[^\s]+$")),
    ("date", re.compile(r"^\d{4}-\d{2}-\d{2}")),
)

# What may be lifted out of the *body*. Narrower than the argument rule on
# purpose: an argument's value is a setting by construction, while a body is
# prose, and replacing an arbitrary phrase in prose changes what the workflow
# says rather than what it is pointed at.
_BODY_PATTERNS: tuple[tuple[str, re.Pattern], ...] = (
    # `#eng-standup` but not a Markdown heading: a heading has a space after
    # the hash, and `##` is excluded by the lookbehind.
    ("channel", re.compile(r"(?<![\w#])#[A-Za-z0-9][A-Za-z0-9._-]{1,}")),
    ("email", re.compile(r"(?<![\w.])[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")),
    ("url", re.compile(r"https?://[^\s)>\]\"']+")),
    ("handle", re.compile(r"(?<![\w@/])@[A-Za-z][A-Za-z0-9._-]{1,}")),
)

# Punctuation a prose match drags in from the sentence around it.
_TRAILING = ".,;:!?)]}\"'"


@dataclass
class Candidate:
    """One literal the scan found, and where it sits.

    `locations` are dotted paths for reporting, not for substitution --
    substitution is by value, over every surface at once.
    """
    literal: str
    kind: str
    locations: list[str] = field(default_factory=list)
    suggested_name: str = ""
    occurrences: int = 0


@dataclass
class TemplateVar:
    """One accepted var: the literal it replaces, and what a stranger is told
    about it."""
    name: str
    description: str
    literal: str
    values: list[str] = field(default_factory=list)
    default: str | None = None
    why: str = ""
    sites: int = 0

    def token(self) -> str:
        return "{{input." + self.name + "}}"

    def as_frontmatter(self) -> dict:
        """The mapping written into the file's `vars:` list.

        Empty fields are omitted rather than written as nulls: this block is
        read by whoever installs the template, and a wall of `default: null` is
        noise in the one place the file has to be legible.
        """
        entry: dict = {"name": self.name, "description": self.description}
        if self.values:
            entry["values"] = list(self.values)
        if self.default is not None:
            entry["default"] = self.default
        return entry


@dataclass
class Proposal:
    """What the model came back with, after being read into a shape px0 trusts."""
    summary: str = ""
    vars: list[TemplateVar] = field(default_factory=list)
    skipped: list[dict] = field(default_factory=list)
    dropped: list[str] = field(default_factory=list)
    raw: str = ""


# --- the deterministic half ------------------------------------------------
#
# No model call and no network. What is eligible to become a var is decided
# here, so a user can read the candidate list and know that nothing outside it
# can be touched no matter what the model answers.

def _slug(text: str) -> str:
    """A var name from a seed: lowercase, underscores, starts with a letter."""
    out = re.sub(r"[^a-z0-9]+", "_", (text or "").lower()).strip("_")
    out = re.sub(r"_+", "_", out)
    if not out or not out[0].isalpha():
        out = f"v_{out}" if out else "value"
    return out[:32]


def _classify(literal: str) -> str:
    for kind, pattern in _KINDS:
        if pattern.match(literal):
            return kind
    return "value"


def eligible(literal: str) -> bool:
    """Whether a literal may become a var at all.

    Rejections, in order: too short to substitute safely, long enough to be
    prose, already a template reference, and the two shapes that are a workflow
    left unfinished rather than a value -- `<OWNER>` fill-me placeholders and
    the clock names the runner resolves itself.
    """
    text = (literal or "").strip()
    if len(text) < MIN_LITERAL or len(text) > MAX_LITERAL:
        return False
    if "{{" in text or "}}" in text:
        return False
    if text.startswith("<") and text.endswith(">"):
        return False
    if workflow_mod.is_time_placeholder(text.strip("{}")):
        return False
    return True


def _record(found: dict, literal: str, kind: str, location: str, seed: str) -> None:
    entry = found.get(literal)
    if entry is None:
        found[literal] = Candidate(literal=literal, kind=kind, locations=[location],
                                   suggested_name=_slug(seed if kind == "value" else kind))
        return
    if location not in entry.locations:
        entry.locations.append(location)


def count_sites(wf: workflow_mod.Workflow, literal: str) -> int:
    """How many times a literal occurs across the surfaces a run resolves.

    Counted rather than assumed, because it is what tells a user whether a var
    is one setting used everywhere or a coincidence of wording.
    """
    total = wf.body.count(literal)
    for inp in wf.inputs:
        for value in (inp.args, inp.retrieve):
            if not value:
                continue
            for _where, text in workflow_mod.walk_strings(value):
                total += text.count(literal)
    return total


def candidates(wf: workflow_mod.Workflow) -> list[Candidate]:
    """Every literal in this workflow that could become a var.

    Two rules, and the asymmetry between them is deliberate. Every string
    argument is a candidate, because an argument's value is a setting by
    construction -- that is what an argument is. In the body, only the shapes
    that are unmistakably an installation's own -- a channel, an address, a URL,
    a handle -- are candidates, because everything else in a body is the
    instructions, and lifting a phrase out of instructions changes what the
    workflow says.
    """
    found: dict[str, Candidate] = {}

    for inp in wf.inputs:
        for field_name in ("args", "retrieve"):
            value = getattr(inp, field_name)
            if not value:
                continue
            for where, text in workflow_mod.walk_strings(value):
                literal = text.strip()
                if not eligible(literal):
                    continue
                location = f"inputs.{inp.id}.{field_name}"
                if where:
                    location = f"{location}.{where}"
                seed = where.split(".")[-1].split("[")[0] if where else field_name
                _record(found, literal, _classify(literal), location, seed)

    for kind, pattern in _BODY_PATTERNS:
        for match in pattern.finditer(wf.body):
            literal = match.group(0).rstrip(_TRAILING)
            if not eligible(literal):
                continue
            _record(found, literal, kind, "body", kind)

    # A literal named in the arguments and repeated in the body is one var, and
    # the body site has to be listed or the report understates what a
    # substitution would touch.
    for entry in found.values():
        if entry.literal in wf.body and "body" not in entry.locations:
            entry.locations.append("body")
        entry.occurrences = count_sites(wf, entry.literal)

    # Longest first: a shorter literal that is a substring of a longer one must
    # not be substituted before it, or the longer one no longer exists to match.
    ordered = sorted(found.values(), key=lambda c: (-len(c.literal), c.literal))
    return ordered[:MAX_CANDIDATES]


def case(wf: workflow_mod.Workflow, found: list[Candidate]) -> dict:
    """The file, and the candidates, as the model is given them.

    Assembled as data rather than inside an f-string so the same structure can
    be printed back to a user who wants to know what the proposal was reasoning
    over.
    """
    return {
        "workflow": {
            "id": wf.id,
            "description": wf.description,
            "request": wf.request,
            "body": wf.body,
            "tools": list(wf.tools),
            "inputs": [
                {"id": i.id, "kind": i.kind, "tool": i.tool,
                 "args": i.args, "retrieve": i.retrieve}
                for i in wf.inputs
            ],
            "already_declared": [v.get("name") for v in wf.vars if isinstance(v, dict)],
        },
        "candidates": [
            {"literal": c.literal, "kind": c.kind, "locations": c.locations,
             "occurrences": c.occurrences, "suggested_name": c.suggested_name}
            for c in found
        ],
    }


# --- the model half --------------------------------------------------------

_INSTRUCTIONS = """\
You are turning one person's automation workflow into a template somebody else \
can install and run.

You are given the workflow, and a list of candidate literals found in it by a \
deterministic scan. Your job is to decide which of those candidates are facts \
about *this installation* rather than about the job, and to describe each one \
for the stranger who will have to fill it in.

Return ONE JSON object and nothing else:

{"summary": "<one sentence: what this template does, written for someone who \
has never seen it>",
 "vars": [{"literal": "<the candidate literal, copied exactly>",
           "name": "<lower_snake_case name for it>",
           "description": "<one line telling the installer what to put here>",
           "values": ["<2 to 4 realistic examples of what someone else would \
put, or the full set when the value is one of a fixed few>"],
           "default": "<omit unless a wrong value here is harmless>",
           "why": "<why this belongs to the installation and not to the job>"}],
 "skip": [{"literal": "<a candidate you are leaving alone>",
           "why": "<why it is part of the job>"}]}

Hold to these:

- Only literals from the candidate list may appear in `vars`. Anything else is \
discarded, so inventing one wastes the entry.
- Templatize what differs between installations: accounts, repositories, \
channels, addresses, people, teams, folders, boards, project keys, time \
windows somebody else would set differently.
- Leave alone what is part of the job. A retrieval query that defines what the \
workflow is *about* is the job. A status filter the job depends on is the job.
- `description` is the whole value of this block. Write it for someone who \
cannot see the rest of the file: say what the value is and where they would \
find theirs, not what the code does with it.
- `values` are examples, and they are read as examples. Give shapes a stranger \
can pattern-match against. Never present a made-up specific as though it were \
the right answer.
- `default` only where getting it wrong is harmless -- a lookback window, a \
local folder. Never for anything that decides who receives something: a \
channel, an address, a repository, a board. Those must be filled in \
deliberately.
- A name is read at the command line as `--input <name>=<value>`. Keep it \
short, lowercase, and obvious.
- Fewer, better vars beat more. Every var is a question the installer has to \
answer before the workflow runs at all.
"""


def propose(config: dict, payload: dict, timeout: float = 180) -> Proposal:
    """Asks the model which candidates are settings, and reads the answer strictly.

    A var whose literal is not in the candidate set is dropped and named in
    `dropped` rather than being trusted: the scan is what decides eligibility,
    and the whole safety property of this command is that the model cannot
    widen it. A var with no description is dropped too, because a var nobody
    can interpret is worse than a literal.
    """
    prompt = (f"{_INSTRUCTIONS}\n---\nWORKFLOW AND CANDIDATES\n"
              f"{json.dumps(payload, indent=2, default=str)}\n")
    try:
        reply = harness.invoke(config, prompt, timeout=timeout)
    except harness.HarnessError as e:
        raise TemplateError(str(e)) from e

    try:
        data = builder._extract_json(reply)
    except builder.BuilderError as e:
        raise TemplateError(f"the model's answer was not usable JSON: {e}") from e
    if not isinstance(data, dict):
        raise TemplateError("the model answered with a list where an object was expected")

    allowed = {c["literal"]: c for c in payload.get("candidates", [])}
    taken: set[str] = {str(n) for n in payload.get("workflow", {}).get("already_declared") or []}
    accepted: list[TemplateVar] = []
    dropped: list[str] = []

    for raw in data.get("vars") or []:
        if not isinstance(raw, dict):
            continue
        literal = str(raw.get("literal") or "")
        description = str(raw.get("description") or "").strip()
        if literal not in allowed:
            dropped.append(literal or "(unnamed)")
            continue
        if not description:
            dropped.append(literal)
            continue
        if any(v.literal == literal for v in accepted):
            continue  # one literal is one var, however many times it was proposed
        name = _slug(str(raw.get("name") or allowed[literal]["suggested_name"]))
        while name in taken:
            name = f"{name}_2" if not name.endswith("_2") else f"{name}x"
        taken.add(name)
        values = [str(v).strip() for v in (raw.get("values") or [])
                  if str(v).strip()][:MAX_VALUES]
        default = raw.get("default")
        default = str(default).strip() if default not in (None, "") else None
        accepted.append(TemplateVar(
            name=name, description=description, literal=literal, values=values,
            default=default, why=str(raw.get("why") or "").strip(),
            sites=int(allowed[literal].get("occurrences") or 1),
        ))

    skipped = [{"literal": str(s.get("literal") or ""), "why": str(s.get("why") or "")}
               for s in data.get("skip") or [] if isinstance(s, dict)]

    # Longest literal first, for the same reason the scan orders them that way:
    # substitution happens in this order.
    accepted.sort(key=lambda v: (-len(v.literal), v.literal))
    return Proposal(summary=str(data.get("summary") or "").strip(), vars=accepted,
                    skipped=skipped, dropped=dropped, raw=reply)


# --- applying --------------------------------------------------------------

def _split(text: str) -> tuple[str, str]:
    """A workflow file as (frontmatter, body), the body keeping its leading newline."""
    if not text.startswith("---"):
        raise TemplateError("the workflow file has no frontmatter to add vars to")
    parts = text.split("---", 2)
    if len(parts) < 3:
        raise TemplateError("the workflow file's frontmatter is malformed")
    return parts[1], parts[2]


def _substitute(value, literal: str, token: str, counter: list[int]):
    """Replaces a literal inside a nested args value, counting what it touched."""
    if isinstance(value, str):
        hits = value.count(literal)
        if hits:
            counter[0] += hits
            return value.replace(literal, token)
        return value
    if isinstance(value, list):
        return [_substitute(v, literal, token, counter) for v in value]
    if isinstance(value, dict):
        return {k: _substitute(v, literal, token, counter) for k, v in value.items()}
    return value


def _with_vars(front: dict, block: list[dict]) -> dict:
    """Frontmatter with the `vars:` list in place, high up and in file order.

    Placed just under what the file is -- id, description, request -- because
    `vars:` is the contract with whoever installs the template, and a contract
    below the tool list is a contract nobody reads. An existing block is
    extended in place rather than moved.
    """
    if "vars" in front:
        existing = [v for v in (front.get("vars") or []) if isinstance(v, dict)]
        names = {v.get("name") for v in existing}
        merged = existing + [v for v in block if v.get("name") not in names]
        out = dict(front)
        out["vars"] = merged
        return out

    anchor = next((k for k in ("request", "description", "kind", "id") if k in front), None)
    out: dict = {}
    for key, value in front.items():
        out[key] = value
        if key == anchor:
            out["vars"] = block
    if "vars" not in out:
        out["vars"] = block
    return out


def apply(text: str, template_vars: list[TemplateVar]) -> tuple[str, dict[str, int]]:
    """Rewrites a workflow file as a template: literals substituted, `vars:` added.

    Returns the new text and a per-var count of the sites it touched.

    Only `inputs[].args`, `inputs[].retrieve`, and the body are rewritten. The
    frontmatter is re-dumped rather than patched line by line, because changing
    a value nested three levels inside an argument by text surgery is how you
    corrupt YAML -- so a comment inside the frontmatter block does not survive
    this. That is the trade, it is why the result is diffed before it is
    written, and `px0 changes revert` puts the original back.
    """
    front_text, body = _split(text)
    try:
        front = yaml.safe_load(front_text) or {}
    except yaml.YAMLError as e:
        raise TemplateError(f"the workflow file's frontmatter is malformed: {e}") from e
    if not isinstance(front, dict):
        raise TemplateError("the workflow file's frontmatter is not a mapping")

    counts: dict[str, int] = {}
    inputs = front.get("inputs")
    # Longest literal first, always, whatever order the caller passed. A shorter
    # literal that sits inside a longer one has to go second or the longer one no
    # longer exists to match: substituting `acme` before `acme/api` leaves
    # `{{input.owner}}/api`, which is a repository nobody named.
    for var in sorted(template_vars, key=lambda v: (-len(v.literal), v.literal)):
        counter = [0]
        token = var.token()
        if isinstance(inputs, list):
            for entry in inputs:
                if not isinstance(entry, dict):
                    continue
                for field_name in ("args", "retrieve"):
                    if entry.get(field_name):
                        entry[field_name] = _substitute(
                            entry[field_name], var.literal, token, counter)
        if var.literal in body:
            counter[0] += body.count(var.literal)
            body = body.replace(var.literal, token)
        counts[var.name] = counter[0]

    # A var that replaced nothing is not written. It happens for a real reason:
    # two accepted literals where one contains the other, so substituting the
    # longer one first leaves the shorter with nowhere left to match. Declaring
    # it anyway would fail validation, since nothing would reference it -- and
    # the caller reads `counts` to say which ones went.
    applied = [v for v in template_vars if counts.get(v.name, 0) > 0]
    front = _with_vars(front, [v.as_frontmatter() for v in applied])
    front_yaml = yaml.dump(front, Dumper=_Dumper, sort_keys=False,
                           allow_unicode=True, width=100,
                           default_flow_style=False).strip()
    return f"---\n{front_yaml}\n---{body}", counts


# Characters that would change what a shell does with the argument, rather than
# being passed through as part of it. `~` is deliberately absent: expansion is
# what the reader wants there.
_NEEDS_QUOTING = re.compile(r"""[\s#;&|<>()$`'"*?\[\]!\\]""")


def _shell_safe(text: str) -> str:
    """One argument, quoted only if the shell would otherwise mangle it."""
    if "'" in text:
        return shlex.quote(text)
    return f"'{text}'" if _NEEDS_QUOTING.search(text) else text


def example_command(workflow_id: str, template_vars: list[TemplateVar]) -> str:
    """The run command a template needs, with every required var named.

    Printed after templatizing because the first thing anyone wants is proof the
    file still runs, and a template's run command is no longer just the workflow
    id.

    A value the shell would eat is quoted, and a var with no example reads as
    `NAME=VALUE` rather than `NAME=<name>`. Both because this line is printed to
    be copied: a Slack channel starts with `#`, which a shell treats as the
    start of a comment, and angle brackets are a redirect -- so the obvious
    version produced a command that ran without the argument it existed to
    demonstrate.

    Quoted only where it is needed, not everywhere: `~/code/api` is left bare so
    the shell still expands it to a real home directory.
    """
    parts = [f"px0 workflows run {workflow_id}"]
    for var in template_vars:
        if var.default is not None:
            continue
        sample = var.values[0] if var.values else "VALUE"
        parts.append(f"--input {_shell_safe(var.name + '=' + sample)}")
    return " ".join(parts)


def example_command_for(workflow_id: str, declared: list[dict]) -> str:
    """The run command for a workflow's whole `vars:` block.

    Templatizing a workflow that already had vars adds to them, and the command
    shown afterwards has to name every value the file now needs, not only the
    ones this pass discovered. Takes `workflow.declared_vars` output, which is
    the normalized form of what ended up on disk.
    """
    return example_command(workflow_id, [
        TemplateVar(name=v["name"], description=v["description"], literal="",
                    values=v["values"], default=v["default"])
        for v in declared])


def load_case(home: Path, workflow_id: str) -> tuple[workflow_mod.Workflow, list[Candidate], dict]:
    """The workflow, its candidates, and the payload built from both.

    One call, so the command cannot scan one thing and propose over another.
    """
    wf = workflow_mod.load(home, workflow_id)
    found = candidates(wf)
    return wf, found, case(wf, found)
