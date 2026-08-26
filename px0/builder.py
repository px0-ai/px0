"""px0 workflows new: turn a sentence into a working workflow.

Building one runs four harness passes, each with a job small enough that the
model can do it well:

1. `clarify` -- ask what's ambiguous about the request. Repeated until the
   model has no questions left (or the user stops answering), because a plan
   built on a guess is worse than one more question.
2. `propose_queries` -- turn the settled request into Composio catalogue
   searches. The model knows what capabilities the task needs; it does not
   know Composio's tool names, so it writes queries rather than guessing slugs.
3. `select_tools` -- pick the few tools that actually fit from the candidates
   those searches returned. Raw relevance ranking is not good enough to trust
   blind (searching "post a message to a channel" surfaces a *delete* tool
   first), so a model with the task in hand chooses, and a human confirms.
4. `generate_plan` -- write the workflow against exactly those tools.

Two more decide what conventions the workflow works under: `select_guidelines`
picks the existing guidelines whose standard its output is judged against, and
`propose_guidelines` names one worth writing down when no file covers it.

Pure planning functions live here; every prompt, spinner, and confirmation
lives in the CLI, which is where user interaction belongs.
"""

import difflib
import json
import re
from dataclasses import dataclass, field
from pathlib import Path

import yaml

from px0 import catalogue, guidelines as guidelines_mod, harness, paths, tools
from px0 import workflow as workflow_mod

_JSON_OBJECT_RE = re.compile(r"\{.*\}", re.DOTALL)  # greedy match spans newlines to grab the whole object out of prose
_JSON_ARRAY_RE = re.compile(r"\[.*\]", re.DOTALL)

MAX_CLARIFY_ROUNDS = 3      # questions get diminishing; stop asking eventually
MAX_INTAKE_ROUNDS = 8       # an interview, not an interrogation
MAX_QUERIES = 4             # catalogue searches per build
MAX_CANDIDATES = 40         # tools shown to the selection pass

# What a workflow file has to pin down before it can be built, in the order the
# interview should reach for it. Both the intake interview and the clarify pass
# are handed this, so the questions a user answers are the fields the plan
# actually needs rather than whatever the model finds interesting -- and so
# "what is still missing" has one definition instead of two.
WORKFLOW_SPEC = """\
1. THE JOB -- what should happen, in a sentence or two.
2. THE SOURCES -- what it reads: which service, account, repository, channel,
   folder, or the user's own notes. The specific one, not the category.
3. THE DELIVERY -- what it produces and where that goes: a message to a named
   channel, a file, a ticket, or output printed for the user to read.
4. THE CADENCE -- when it runs: on demand, on a schedule (say when), or when
   something happens (say what).
5. DONE LOOKS LIKE -- what makes the output right rather than merely produced:
   length, tone, what to lead with, what to leave out."""


class BuilderError(Exception):
    """Raised when a workflow plan can't be generated or parsed from the harness response."""


def _extract_json(raw: str, want_array: bool = False):
    """Pulls the first JSON value out of a harness response.

    Harnesses narrate around their answers, so the JSON is located rather than
    assumed to be the whole reply.
    """
    pattern = _JSON_ARRAY_RE if want_array else _JSON_OBJECT_RE
    match = pattern.search(raw)
    if not match:
        raise BuilderError(
            f"the harness did not return {'a JSON array' if want_array else 'a JSON object'}:"
            f"\n{raw[:500]}"
        )
    try:
        return json.loads(match.group(0))
    except json.JSONDecodeError as e:
        raise BuilderError(f"the harness returned malformed JSON: {e}")


def _qa_block(qa: list[tuple[str, str]]) -> str:
    """Renders the clarification history for inclusion in a later prompt."""
    if not qa:
        return ""
    lines = "\n".join(f"Q: {q}\nA: {a}" for q, a in qa)
    return f"\n\nAlready clarified with the user:\n{lines}"


@dataclass
class Plan:
    """A workflow plan produced by the harness: trigger, inputs, tools, output shape,
    and the instruction body, plus the raw JSON the model returned."""
    trigger: dict
    inputs: list[dict]
    tools: list[str]
    output: dict
    body: str
    description: str = ""
    raw: dict = field(default_factory=dict)


def similar_workflows(home: Path, description: str, limit: int = 3) -> list[tuple[str, float]]:
    """Workflows that already look like what is being described.

    Local word overlap, not a model call: this runs before every build, and a
    round trip to be told "no, nothing like it" on the common case would be a
    tax on the ordinary path. Being approximate is fine because the result is
    shown to a person, who can see at a glance whether it is the same job.

    The failure this prevents is quiet: `px0 workflows new` never looked, so a
    store accumulated three near-identical digests that each fired on their own
    schedule and each cost a run.
    """
    from px0 import memory as memory_mod
    from px0 import workflow as workflow_mod

    wanted = memory_mod._terms(description)
    if not wanted:
        return []
    scored = []
    for wf in workflow_mod.load_all(home).values():
        theirs = memory_mod._terms(f"{wf.description} {wf.request}")
        if not theirs:
            continue
        overlap = len(wanted & theirs) / max(len(wanted | theirs), 1)
        if overlap >= 0.4:
            scored.append((wf.id, round(overlap, 2)))
    return sorted(scored, key=lambda pair: -pair[1])[:limit]


def clarify(config: dict, description: str, qa: list[tuple[str, str]]) -> list[str]:
    """Asks what is still ambiguous about the request.

    Returns up to three questions, or an empty list when the model considers the
    request buildable. Only things that would change the generated workflow
    count as ambiguous -- the model is told not to ask for detail it can pick a
    sane default for, because an interrogation is worse than an assumption.
    """
    prompt = (
        "You are about to turn a request into an automated workflow. Before "
        "planning it, decide whether anything is genuinely ambiguous.\n\n"
        "A workflow has to pin these down:\n\n"
        f"{WORKFLOW_SPEC}\n\n"
        "Ask ONLY where one of those is missing AND the answer would change the "
        "workflow. Do NOT ask about anything you can pick a reasonable default "
        "for, and do NOT ask for confirmation of what the request already "
        "says.\n\n"
        "Respond with ONLY a JSON array of question strings, at most 3. "
        "Return [] if the request is clear enough to build.\n\n"
        f"Request: {description}{_qa_block(qa)}"
    )
    raw = harness.invoke(config, prompt, timeout=60)
    questions = _extract_json(raw, want_array=True)
    if not isinstance(questions, list):
        raise BuilderError("the harness returned a non-list of questions")
    return [str(q).strip() for q in questions[:3] if str(q).strip()]


def _transcript_block(transcript: list[tuple[str, str]]) -> str:
    """Renders the intake interview so far for inclusion in a prompt."""
    return "\n".join(f"Q: {q}\nA: {a}" for q, a in transcript)


def intake(config: dict, transcript: list[tuple[str, str]],
           wrap_up: bool = False) -> dict:
    """One turn of the intake interview: the next question, or the request.

    Returns `{"question": str}` while a field of `WORKFLOW_SPEC` is still both
    unknown and load-bearing, and `{"description": str}` once the transcript
    settles enough to build from. `wrap_up` forces the second: the user has
    stopped answering, so the request is written from what they did say rather
    than the interview running on without them.

    One question per turn on purpose. Asking a batch means the third question is
    written before the first is answered, which is how an interview turns into a
    form -- and the answer to "which repository" is usually what determines
    whether the next question is worth asking at all.
    """
    closing = (
        "The user has stopped answering. Write the request from what they did "
        "say and fill the rest with the obvious default; do NOT ask anything "
        "else.\n\n"
        if wrap_up else
        "If a field above is still genuinely unknown AND knowing it would "
        "change the workflow, ask the single most valuable next question. One "
        "thing, one sentence, plain words -- name the likely options where "
        "there are few ('every morning, every Friday, or only when you ask?'). "
        "Never restate what they already told you, never ask for a field you "
        "can default sensibly, and skip field 5 unless this is the kind of "
        "output where taste shows.\n\n"
        "Otherwise stop asking and write the request.\n\n"
    )
    prompt = (
        "You are interviewing someone who wants to automate a job, to gather "
        "exactly what a workflow file needs and nothing more:\n\n"
        f"{WORKFLOW_SPEC}\n\n"
        f"{closing}"
        "The request you write becomes the workflow's own description and the "
        "input to every later pass. Write it as one paragraph in the "
        "imperative, in their words, naming the specific services, accounts, "
        "cadence, and destination they gave. Invent no detail they did not "
        "supply.\n\n"
        'Respond with ONLY one JSON object: {"question": "<the next question>"} '
        'or {"description": "<the finished request>"}.\n\n'
        f"Interview so far:\n{_transcript_block(transcript)}"
    )
    raw = harness.invoke(config, prompt, timeout=60)
    answer = _extract_json(raw)
    if not isinstance(answer, dict):
        raise BuilderError("the harness returned no intake object")

    description = str(answer.get("description") or "").strip()
    if description:
        return {"description": description}
    question = str(answer.get("question") or "").strip()
    if question and not wrap_up:
        return {"question": question}
    raise BuilderError(
        "the harness returned neither a question nor a request during intake")


def revise_request(config: dict, description: str, note: str,
                   transcript: list[tuple[str, str]] | None = None) -> str:
    """Folds a follow-up note into an already-written request, one harness call.

    Chosen over overwriting the paragraph outright: the "edit" step of
    confirming a request used to replace the whole thing with whatever the user
    typed next, so a two-word note like "and only my own PRs" silently dropped
    everything else the interview had settled. Reading it as a continuation of
    the same conversation instead means the note only has to say what changes.
    """
    transcript_block = (f"\n\nInterview it came from:\n{_transcript_block(transcript)}"
                        if transcript else "")
    prompt = (
        "You wrote this request for an automated workflow, from an interview "
        "with the user. They now want something changed -- fold their note "
        "into the request rather than replacing it: keep whatever the note "
        "does not touch, change what it does, and invent nothing it does not "
        "say.\n\n"
        "Write it as one paragraph in the imperative, the same shape as the "
        "current request.\n\n"
        f"Current request: {description}{transcript_block}\n\n"
        f"Their note: {note}\n\n"
        'Respond with ONLY one JSON object: {"description": "<the revised request>"}.'
    )
    raw = harness.invoke(config, prompt, timeout=60)
    answer = _extract_json(raw)
    if not isinstance(answer, dict):
        raise BuilderError("the harness returned no revision object")
    revised = str(answer.get("description") or "").strip()
    if not revised:
        raise BuilderError("the harness returned an empty revision")
    return revised


def propose_queries(config: dict, description: str,
                    qa: list[tuple[str, str]]) -> list[dict]:
    """Turns the settled request into Composio catalogue searches.

    Each search is a toolkit plus a short capability phrase, because Composio's
    search filters by substring within a toolkit rather than ranking by
    relevance: a whole sentence matches almost nothing, while
    `toolkit=github` + "list pull requests" lands on the right tool. The model
    names services and actions -- never slugs, which it cannot know and would
    invent.
    """
    prompt = (
        "This request will be automated with tools from Composio's catalogue. "
        "Say which searches would find them.\n\n"
        "Respond with ONLY a JSON array of objects, each "
        '{"toolkit": "<service slug or null>", "capability": "<2-4 keywords>"}.\n\n'
        "Rules: toolkit is the lowercase service name (github, slack, gmail, "
        "googlecalendar, notion, linear, ...) or null if the request names no "
        "service. capability is 2-4 keywords describing the ACTION, matched "
        "against tool names -- \"list pull requests\", \"send message\", "
        "\"search messages\", \"create event\". Do NOT write a sentence, and do "
        "NOT invent tool names or IDs. One entry per distinct capability the "
        f"request needs, at most {MAX_QUERIES}.\n\n"
        "Return [] if the request needs no external service at all.\n\n"
        f"Request: {description}{_qa_block(qa)}"
    )
    raw = harness.invoke(config, prompt, timeout=60)
    proposed = _extract_json(raw, want_array=True)
    if not isinstance(proposed, list):
        raise BuilderError("the harness returned a non-list of searches")

    queries = []
    for entry in proposed[:MAX_QUERIES]:
        if isinstance(entry, str):  # tolerate a bare string
            entry = {"toolkit": None, "capability": entry}
        if not isinstance(entry, dict):
            continue
        capability = str(entry.get("capability") or "").strip()
        if not capability:
            continue
        toolkit = entry.get("toolkit")
        toolkit = str(toolkit).strip().lower() or None if toolkit else None
        queries.append({"toolkit": toolkit, "capability": capability})
    return queries


def describe_query(query: dict) -> str:
    """A query as one readable line, for showing the user what px0 is searching for."""
    toolkit = query.get("toolkit")
    return f"{toolkit}: {query['capability']}" if toolkit else query["capability"]


def search_candidates(home: Path, queries: list[dict]) -> list[catalogue.CatalogueTool]:
    """Runs each search against Composio's catalogue and pools the results.

    A toolkit-scoped search that comes back empty is retried without the scope,
    since the model may have guessed a toolkit slug that doesn't exist.
    De-duplicated by slug and order-preserving, so the first search's matches
    stay near the top.
    """
    seen: dict[str, catalogue.CatalogueTool] = {}
    for query in queries:
        toolkit, capability = query.get("toolkit"), query["capability"]
        found = catalogue.search(home, capability, toolkit=toolkit)
        if not found and toolkit:
            found = catalogue.search(home, f"{toolkit} {capability}")
        for tool in found:
            seen.setdefault(tool.slug, tool)
        if len(seen) >= MAX_CANDIDATES:
            break
    return list(seen.values())[:MAX_CANDIDATES]


MAX_SELECT_ATTEMPTS = 3     # a real candidate list coming back empty is more
                            # often sampling noise than a genuine "nothing fits"


def select_tools(config: dict, description: str, qa: list[tuple[str, str]],
                 candidates: list[catalogue.CatalogueTool]) -> list[catalogue.CatalogueTool]:
    """Picks the minimal set of candidate tools the request actually needs.

    Relevance ranking alone is not trustworthy -- a search for "post a message"
    can rank a delete tool first -- so the model chooses with the task in hand,
    and is told to prefer fewer tools and to avoid writes it wasn't asked for.

    Retries a few times on an empty pick before accepting it: the same prompt
    against the same candidates can come back `[]` on one attempt and the
    obviously-right selection on the next, so one empty response is treated as
    noise rather than the model's final word -- otherwise the build falls
    through to inventing tools that don't exist in the candidate list at all.
    """
    if not candidates:
        return []

    listing = "\n".join(
        f"- {t.slug} [{'DESTRUCTIVE' if t.is_destructive else ('write' if t.is_write else 'read')}]"
        f" ({t.toolkit}): {t.description[:160]}"
        for t in candidates
    )
    prompt = (
        "Choose every candidate tool below that this request needs -- it is "
        "normal to need more than one.\n\n"
        "Rules: pick the fewest tools that accomplish the request, but do NOT "
        "omit a candidate that clearly satisfies part of the request just to "
        "keep the list short. Prefer a read tool over a write tool. Include a "
        "write tool ONLY if the request explicitly asks to post, send, "
        "comment, or otherwise change something. A DESTRUCTIVE tool is fine "
        "to include when the request calls for it, even without the words "
        "\"delete\" or \"overwrite\" -- the user reviews and confirms every "
        "tool before anything is built, so it is safe to propose one that fits. "
        "Return [] ONLY if nothing in the list is relevant to any part of the "
        "request -- if a candidate is a close match, include it rather than "
        "second-guess it.\n\n"
        "Respond with ONLY a JSON array of the chosen slugs, exactly as "
        "written below.\n\n"
        f"Candidates:\n{listing}\n\n"
        f"Request: {description}{_qa_block(qa)}"
    )
    by_slug = {t.slug: t for t in candidates}

    for attempt in range(MAX_SELECT_ATTEMPTS):
        raw = harness.invoke(config, prompt, timeout=90)
        chosen = _extract_json(raw, want_array=True)
        if not isinstance(chosen, list):
            raise BuilderError("the harness returned a non-list of tool slugs")

        # Silently drop hallucinated slugs: the candidate list is the contract,
        # and a slug that isn't in it would fail validation later anyway.
        selected = [by_slug[str(s)] for s in chosen if str(s) in by_slug]
        if selected or attempt == MAX_SELECT_ATTEMPTS - 1:
            return selected
    return []


def generate_plan(config: dict, description: str,
                  qa: list[tuple[str, str]] | None = None,
                  selected: list[catalogue.CatalogueTool] | None = None) -> Plan:
    """Asks the harness to turn the settled request into a JSON workflow plan.

    `selected` restricts it to the discovered tools the user confirmed; without
    it the plan may only use px0's curated registry. Raises BuilderError if the
    harness response has no JSON object or the JSON is malformed.
    """
    qa = qa or []
    if selected:
        tool_lines = "\n".join(
            f"- {t.id} [{'write' if t.is_write else 'read'}] params: {t.params}"
            for t in selected
        )
        tool_block = f"Use ONLY these tools, by the exact id shown:\n{tool_lines}"
    else:
        tool_block = f"Available tools: {[t.id for t in tools.list_tools()]}"

    prompt = (
        "Turn this request into a JSON workflow plan for a personal automation "
        "tool. Respond with ONLY a JSON object with keys: "
        '"trigger" ({"manual": bool, "schedule": five-field cron or null}), '
        '"inputs" (list of {"id", "tool", "args"} -- these run before the prompt '
        "to gather context, so they must be READ tools only. Every argument must "
        "be a value the request actually settled: NEVER write a placeholder like "
        "<OWNER>, <REPO>, YOUR_USERNAME or TODO, and never invent one -- if the "
        "request does not name the repository, channel, or account an argument "
        "needs, leave that input out entirely rather than stubbing it. For a time "
        "window use the clock placeholders {{now}}, {{today}}, and {{now-<N><unit>}} "
        "with unit m, h, d, or w -- {{now-24h}} is 24 hours ago, as an ISO 8601 "
        "timestamp -- because a scheduled run cannot use a literal date), "
        '"tools" (list of tool ids the model may call during the run, for '
        "actions like posting -- include a write tool here, never in inputs), "
        '"output" ({"target": "stdout"|"file", "path": templated path if file -- '
        "its only placeholders are the clock ones: {{today}}, {{date}}, {{now}}, "
        "{{datetime}}, {{time}}, {{now-<N><unit>}}; anything else in braces is an "
        'error, so never put an input id in a path}) -- '
        'if trigger.schedule is set, target MUST be "file" with a path, since '
        "nobody is watching stdout for a run that fires on a cron, even when "
        "the body also posts somewhere via a tool call, "
        '"body" (the instruction text the model receives at run time; reference '
        "each input by {{input_id}}. A run has nobody to answer questions, so "
        "NEVER write a step that asks the user for a value or stops until they "
        "supply one -- if the request left something unsettled, leave it out of "
        "the plan rather than deferring it to a run that cannot ask. "
        "Write it as clean, scannable markdown, not "
        "a single dense paragraph: a short lead-in sentence if needed, then "
        "numbered steps for anything sequential -- fetch, filter, look up, "
        "summarize, post -- and bullet points for enumerated items such as "
        "report sections. Put each tool id and template variable in backticks. "
        "Leave a blank line between steps and between sections. Both the human "
        "reviewing this plan and the model executing it read a short list of "
        "steps far more reliably than a run-on sentence), "
        '"description" (one line).\n\n'
        f"{tool_block}\n\n"
        f"Request: {description}{_qa_block(qa)}"
    )
    raw = harness.invoke(config, prompt, timeout=90)
    data = _extract_json(raw)

    return Plan(
        trigger=data.get("trigger", {"manual": True}),
        inputs=data.get("inputs", []),
        tools=data.get("tools", []),
        output=data.get("output", {"target": "stdout"}),
        body=data.get("body", description),
        description=data.get("description", description),
        raw=data,
    )


MAX_SLUG_LEN = 40  # kept short enough to type back at the id prompt without wrapping


def generate_slug(config: dict, description: str) -> str:
    """Asks the harness for a short id-shaped slug naming this workflow.

    A slug names the job -- "github-pr-digest" -- rather than being the first
    forty characters of the description sentence, which is what a mechanical
    slugify produces and which reads as noise once there are a dozen workflows
    to tell apart in `px0 workflows list`.
    """
    prompt = (
        "Name this automated workflow with a short id for a filename.\n\n"
        "Rules: lowercase letters, digits, and hyphens only, no spaces, at "
        f"most {MAX_SLUG_LEN} characters. Capture the service and the action "
        "in 2-5 words, not the whole sentence.\n\n"
        'Respond with ONLY one JSON object: {"slug": "<the id>"}.\n\n'
        f"Workflow: {description}"
    )
    raw = harness.invoke(config, prompt, timeout=30)
    answer = _extract_json(raw)
    if not isinstance(answer, dict):
        raise BuilderError("the harness returned no slug object")
    slug = re.sub(r"[^a-z0-9-]+", "-",
                 str(answer.get("slug") or "").strip().lower()).strip("-")
    return slug[:MAX_SLUG_LEN].strip("-") or "new-workflow"


def check_feasibility(plan: Plan, home: Path) -> list[str]:
    """Validates a plan against reality: unknown tool ids, write tools used as inputs
    (inputs must be read-only), arguments left as placeholders, an invalid cron
    schedule, and a scheduled plan that would fail `workflow.validate` the moment
    the daemon fires it. Returns a list of human-readable issue strings; empty
    means the plan can proceed."""
    issues = []

    # A plan that could not settle an argument writes `owner: <OWNER>` rather
    # than saying so, and the workflow then fails on its first run with the
    # connector's own error -- GitHub answers `<OWNER>/<REPO>` with a 404, which
    # reads as a missing repository. Caught before the file is written, because
    # the fix is a better request, not an edit to the file.
    issues.extend(workflow_mod.input_arg_errors(
        plan.inputs,
        fix="the plan could not settle it: re-run and name the real value in the "
            "request (which repository, which channel, which account)",
    ))
    known = [t.id for t in tools.list_tools(home=home)]

    def check_tool(tool_id: str, context: str):
        # records an issue with a did-you-mean suggestion when the id is close to a real one
        if tool_id in known:
            return
        close = difflib.get_close_matches(tool_id, known, n=1)
        suggestion = f"; closest available: {close[0]}" if close else ""
        issues.append(f"no tool exposes {tool_id!r} ({context}){suggestion}")

    for inp in plan.inputs:
        tool_id = inp.get("tool")
        if not tool_id:
            issues.append(f"input {inp.get('id')!r} has no tool")
            continue
        check_tool(tool_id, f"input {inp.get('id')!r}")
        if tool_id in known and tools.is_write(tool_id, home):
            issues.append(f"input {inp.get('id')!r} uses write tool {tool_id!r}; "
                           f"inputs must be read-only, move it to tools:")

    for tool_id in plan.tools:
        check_tool(tool_id, "tools[]")

    schedule = plan.trigger.get("schedule")
    if schedule:
        from croniter import croniter
        try:
            croniter(schedule)
        except (ValueError, KeyError) as e:
            issues.append(f"trigger.schedule {schedule!r} invalid: {e}")
        # Nobody is watching stdout for a run the cron fires unattended, and
        # `workflow.validate` enforces that at run time regardless of what the
        # body's tool calls post elsewhere -- catch it here instead of letting
        # the build succeed and the first scheduled run fail.
        if plan.output.get("target") not in (None, "file"):
            issues.append("a scheduled workflow's output.target must be 'file' "
                          "(add output.path -- posting via a tool call doesn't satisfy this)")

    return issues


def plan_tool_ids(plan: Plan) -> list[str]:
    """Every tool id the plan references, inputs and tools alike, in order."""
    ids = [inp.get("tool") for inp in plan.inputs if inp.get("tool")]
    return ids + [t for t in plan.tools]


def required_connections(plan: Plan, home: Path | None = None) -> set[str]:
    """The provider names (e.g. "github", "slack") the plan's inputs and tools touch."""
    providers = set()
    for tool_id in plan_tool_ids(plan):
        spec = tools.resolve(tool_id, home)
        if spec is not None:
            providers.add(spec.provider)
    return providers


def write_tools_named(plan: Plan, home: Path | None = None) -> list[str]:
    """The subset of plan.tools that are write tools, so the CLI can warn the user
    before granting them."""
    out = []
    for tool_id in plan.tools:
        spec = tools.resolve(tool_id, home)
        if spec is not None and spec.is_write:
            out.append(tool_id)
    return out


# How many existing guidelines one workflow may be handed. Every one is inlined
# verbatim into every run, so this is a prompt-budget ceiling as much as a
# relevance one: past three, the workflow's own instructions stop being the
# loudest thing in the prompt.
MAX_ATTACHED_GUIDELINES = 3


def select_guidelines(home: Path, config: dict, description: str,
                      plan: Plan | None = None) -> list[str]:
    """Picks the existing guidelines whose standard this workflow's output is
    actually judged against.

    Reads frontmatter descriptions, the way a skill is selected by its own
    description rather than by its contents. The keyword scorer this replaced
    matched on filename and body overlap, which cannot tell *writing* a commit
    message from *reading* one: a nightly standup that summarizes commits scored
    highest against `commit-messages.md` and inlined a commit-authoring rubric
    into every run.

    Returns [] freely, and on any harness failure. A wrong guideline is worse
    than none -- it spends prompt and pulls the output toward a convention the
    task never called for -- so nothing is attached on a guess, and the build
    is never failed over this.
    """
    available = guidelines_mod.attachable(home)
    if not available:
        return []

    listing = "\n".join(f"- {g.rel}: {g.summary or '(no description)'}"
                        for g in available)
    prompt = (
        "Choose which of these guidelines this workflow must follow.\n\n"
        "Each is a durable convention the user has written down. Attaching one "
        "inlines its full text into every run of the workflow, so choose a "
        "guideline ONLY if the workflow's own output is judged against it -- "
        "that is, following it changes what the workflow produces.\n\n"
        "Do NOT choose a guideline merely because the workflow touches the same "
        "subject. A workflow that reads, summarizes, or reports on something is "
        "not governed by the convention for authoring that thing: a standup "
        "that summarizes commits does not follow the commit-message convention, "
        "and a workflow that lists open pull requests does not follow the "
        "pull-request-description convention.\n\n"
        f"Choose at most {MAX_ATTACHED_GUIDELINES}. Respond with ONLY a JSON "
        "array of the chosen paths, exactly as written below. Return [] if none "
        "of them governs this workflow's output -- that is the common answer.\n\n"
        f"Guidelines:\n{listing}\n\n"
        f"Workflow: {(plan.description if plan else '') or description}\n"
        f"What the user asked for: {description}\n\n"
        f"What it does:\n{(plan.body if plan else '')[:2000]}"
    )
    try:
        chosen = _extract_json(harness.invoke(config, prompt, timeout=60), want_array=True)
    except (BuilderError, harness.HarnessError):
        return []
    if not isinstance(chosen, list):
        return []

    # Only paths that exist, in store order rather than the order the model
    # happened to list them, so the same picks render the same `guidelines:`.
    by_rel = {g.rel: g for g in available}
    picked = {c for c in chosen if isinstance(c, str) and c in by_rel}
    return [g.rel for g in available if g.rel in picked][:MAX_ATTACHED_GUIDELINES]


# --- proposing a guideline the store doesn't have yet ----------------------

@dataclass
class GuidelineProposal:
    """A durable standard this workflow leans on that the store has no file for.

    `path` is relative to `guidelines/`, `title` names the standard, `description`
    is the frontmatter line every later workflow is matched against, and `why`
    says what in the workflow depends on it. There is no question to ask: this
    is the only path by which a guideline gets written, so the draft is produced
    from the workflow itself and shown to the user to keep, redo, or skip.

    The description is settled here rather than after drafting, because it is the
    file's contract -- what it covers and when it applies -- and a proposal that
    cannot state that in one sentence is not a durable convention.
    """
    path: str
    title: str
    why: str = ""
    description: str = ""

    @property
    def name(self) -> str:
        """The frontmatter name: the filename, as with a skill."""
        return guidelines_mod.name_for(self.path)


def propose_guidelines(config: dict, description: str, plan: Plan,
                       existing: list[str]) -> list[GuidelineProposal]:
    """Names the standards this workflow depends on that no guideline file covers.

    Deliberately conservative. A guideline is a *durable, reusable* convention
    that outlives one workflow -- a review rubric, a commit message format, a
    writing voice. Anything the plan's own body already pins down is not one, and
    neither is generic advice with no choices in it, since inlining that into
    every run costs tokens and teaches px0 nothing.
    """
    existing_block = "\n".join(f"- {e}" for e in existing) or "- (none)"
    prompt = (
        "A workflow has just been planned. Decide whether it depends on any "
        "durable convention that no existing guideline file covers.\n\n"
        "A guideline is a reusable standard that outlives this one workflow: a "
        "code review rubric, a commit message format, a writing voice, a "
        "summarization style, a definition of done. It is worth proposing ONLY if "
        "(a) this workflow's own output is judged against it -- following it "
        "changes what the workflow produces, (b) it would apply again to the next "
        "workflow of this kind, and (c) no file listed below already covers it.\n\n"
        "Do NOT propose: anything the instruction body already specifies in full; "
        "generic best practice with no real choices in it; a restatement of what "
        "the workflow does; a convention for authoring something this workflow "
        "only reads or reports on; or a second file on a topic already listed.\n\n"
        "Respond with ONLY a JSON array, at most 2 entries, each:\n"
        '{"path": "<kebab-case>.md or <folder>/<kebab-case>.md", '
        '"title": "<short label>", '
        '"description": "<one sentence: what this convention covers and when a '
        'workflow should follow it, phrased so a later workflow can be matched '
        'against it>", '
        '"why": "<what in this workflow needs it, one sentence>"}\n\n'
        "Return [] if the workflow needs no new guideline -- that is the common case.\n\n"
        f"Existing guideline files:\n{existing_block}\n\n"
        f"Workflow description: {plan.description or description}\n\n"
        f"Instruction body:\n{plan.body[:2000]}"
    )
    raw = harness.invoke(config, prompt, timeout=60)
    proposed = _extract_json(raw, want_array=True)
    if not isinstance(proposed, list):
        raise BuilderError("the harness returned a non-list of guideline proposals")

    # Cap *after* filtering, not before: a junk first entry must not consume the
    # budget and silently drop the valid proposal behind it.
    out, seen = [], {e.lower() for e in existing}
    for entry in proposed:
        if len(out) == 2:
            break
        if not isinstance(entry, dict):
            continue
        path = _guideline_path(str(entry.get("path") or ""))
        title = str(entry.get("title") or "").strip()
        if not path or not title or path.lower() in seen:
            continue
        seen.add(path.lower())
        out.append(GuidelineProposal(
            path=path,
            title=title,
            why=str(entry.get("why") or "").strip(),
            # Falls back to the title so the file is never written without a
            # description: an undescribed guideline is invisible to every later
            # `select_guidelines`, which is a worse outcome than a terse line.
            description=str(entry.get("description") or "").strip() or title,
        ))
    return out


def _guideline_path(raw: str) -> str:
    """Normalizes a model-proposed guideline path into a safe store-relative one.

    The model picks this name, so it is untrusted input that becomes a filesystem
    path: strip any traversal or absolute component and keep it to one optional
    folder, which is as deep as `guidelines/` goes.
    """
    parts = [re.sub(r"[^a-z0-9.-]+", "-", p.strip().lower()).strip("-")
             for p in raw.replace("\\", "/").split("/")]
    parts = [p for p in parts if p and p not in (".", "..")]
    if not parts:
        return ""
    parts = parts[-2:]
    parts[-1] = parts[-1].removesuffix(".md") + ".md"
    return "/".join(parts)


def draft_guideline(config: dict, proposal: GuidelineProposal, description: str,
                   plan: Plan) -> str:
    """Drafts the body of the guideline the workflow needs, in px0's own shape.

    Written from the workflow rather than from an interview: the build already
    knows what the workflow does and what standard it leans on, and asking the
    user to type a convention from scratch was the step that stopped guidelines
    from ever getting written. The result is shown before it is saved, and it is
    an ordinary Markdown file afterwards, so a draft the user disagrees with is
    a redo or an edit rather than a dead end.

    Returns the rules only. `save_guideline` adds the `name`/`description`
    frontmatter, so the model is never asked to write YAML it could break -- and
    the description stays the one the proposal already committed to.

    Sections are `## ` headings because that is what makes each rule addressable
    as a claim by `px0 guidelines log`.
    """
    prompt = (
        "Write the rules for one durable convention a workflow leans on.\n\n"
        "Format: two to five `## ` sections. Each heading is a short "
        "prescriptive instruction (\"Lead with the takeaway\", not \"Takeaways\"). "
        "Under each, two or three lines of plain prose saying what to do and why. "
        "No preamble, no top-level title, no bullet lists, no closing summary.\n\n"
        "Write the version a careful practitioner would recognize as the "
        "conventional one, and make every rule concrete enough to follow. This "
        "file is inlined verbatim into every run of the workflow, so say nothing "
        "you cannot justify, do not restate what the workflow does, and do not "
        "pad to reach a section count.\n\n"
        f"Guideline: {proposal.title}\n"
        f"What it covers: {proposal.description or proposal.title}\n"
        f"What in the workflow needs it: {proposal.why or '(unstated)'}\n"
        f"Workflow: {plan.description or description}\n\n"
        f"Instruction body:\n{plan.body[:2000]}"
    )
    text = harness.invoke(config, prompt, timeout=90).strip()
    # A harness that narrates around the answer leaves prose above the first
    # heading; the file starts at the first section or it is not a guideline.
    idx = text.find("## ")
    if idx == -1:
        raise BuilderError("the harness did not return any `## ` guideline sections")
    return text[idx:].rstrip() + "\n"


def save_guideline(home: Path, rel_path: str, body: str, description: str = "",
                   actor: str = "builder") -> Path:
    """Writes a new guideline file and records it as a versioned guideline change.

    The file gets `name`/`description` frontmatter over the drafted rules, which
    is what makes it findable later: `select_guidelines` reads descriptions and
    nothing else, so a guideline saved without one can never be attached to the
    next workflow that needs it. A body that already carries frontmatter (a
    caller passing a whole file, or a re-save) keeps its own.

    Goes through `claims.capture_guideline_change` rather than writing the file
    directly, so the new claims get history from their first version and
    `px0 guidelines log` works on them immediately.
    """
    from px0 import claims, versioning  # deferred: both import builder-adjacent modules

    # Checked here as well as at every call site. This is the function that
    # touches the disk, so it is the one place a traversing path has to be
    # stopped for every caller to be safe -- callers that sanitize first pay
    # nothing, and one that forgets does not write outside the store.
    dest = (paths.guidelines_dir(home) / rel_path)
    base = paths.guidelines_dir(home).resolve()
    try:
        resolved = dest.resolve()
        escaped = resolved != base and base not in resolved.parents
    except OSError:
        escaped = True
    if escaped:
        raise BuilderError(
            f"refusing to write a guideline outside the store: {rel_path!r}")

    front, rules = guidelines_mod.split_frontmatter(body)
    if front is None:
        content = guidelines_mod.render(guidelines_mod.name_for(rel_path),
                                        description, rules)
    else:
        content = body if body.endswith("\n") else body + "\n"

    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(content)
    claims.capture_guideline_change(
        home, actor,
        [versioning.FileChange(f"guidelines/{rel_path}", content.encode(),
                               "drafted during `px0 workflows new`")],
    )
    return dest


def render_workflow_file(workflow_id: str, plan: Plan, guidelines: list[str],
                         request: str = "") -> str:
    """Renders a Plan into the workflow file's text: YAML frontmatter followed by the
    instruction body, in the same `---\\nfrontmatter\\n---\\nbody` shape workflow.py parses.

    `request` is the user's own sentence, stored verbatim next to the model's
    normalized `description` so `px0 workflows edit` can show back what they
    actually asked for rather than a paraphrase of it.
    """
    front = {
        "id": workflow_id,
        "kind": "workflow",
        "version": 1,
        "description": plan.description,
        "trigger": plan.trigger,
    }
    if request.strip():
        front["request"] = request.strip()
    if guidelines:
        front["guidelines"] = guidelines
    if plan.inputs:
        front["inputs"] = plan.inputs
    if plan.tools:
        front["tools"] = plan.tools
    front["output"] = plan.output
    front["timeout"] = "120s"

    front_yaml = yaml.safe_dump(front, sort_keys=False).strip()
    return f"---\n{front_yaml}\n---\n{plan.body.strip()}\n"


def save_workflow(home: Path, workflow_id: str, content: str) -> Path:
    """Writes a new workflow file to workflows/ and records it as a versioned change.
    Overwrites any existing file at the same id."""
    from px0 import versioning  # deferred: versioning imports builder-adjacent modules, avoid a cycle

    dest = paths.workflows_dir(home) / f"{workflow_id}.md"
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(content)
    versioning.record_change(
        home, "builder", [versioning.FileChange(str(dest.relative_to(home)), content.encode())]
    )
    return dest
