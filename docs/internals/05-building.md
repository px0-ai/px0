# 5. Building a workflow

Modules: `px0/builder.py`, `px0/catalogue.py`, and `cli._build_workflow`

`px0 workflows new` turns a sentence into a working file. It does that with six harness passes, each with a job small enough that a model can do it well, and it stops to ask a person twice.

## Why six passes and not one

One pass asking for a complete workflow file fails in a specific way: the model does not know Composio's tool slugs, so it invents them. The invented slugs then fail validation, or worse, fail at run time against an API that answers with a 404 about something else entirely.

Splitting the work fixes that by giving each pass only what it can actually know.

| Pass | Function | What it decides |
| ---- | -------- | --------------- |
| Intake | `intake` | The next interview question, or the finished request |
| Clarify | `clarify` | What is still genuinely ambiguous |
| Query | `propose_queries` | What to search Composio's catalogue for |
| Select | `select_tools` | Which of the returned candidates actually fit |
| Plan | `generate_plan` | The workflow, against exactly those tools |
| Name | `generate_slug` | A short id for the file |

Two more decide the conventions the workflow works under: `select_guidelines` and `propose_guidelines`, covered below.

Every one of those is a pure function taking a config and returning data. Every prompt, spinner, and confirmation lives in the CLI, because that is where user interaction belongs.

## The intake interview

`WORKFLOW_SPEC` is the five things a workflow file has to pin down, and it is the shared definition both the interview and the clarify pass are handed:

```
1. THE JOB          what should happen
2. THE SOURCES      what it reads: the specific account, repo, channel
3. THE DELIVERY     what it produces and where that goes
4. THE CADENCE      on demand, on a schedule, or when something happens
5. DONE LOOKS LIKE  what makes the output right rather than merely produced
```

One definition rather than two means the questions you answer are the fields the plan actually needs.

`intake` returns either `{"question": ...}` or `{"description": ...}`. One question per turn, deliberately. Asking a batch means the third question is written before the first is answered, which is how an interview becomes a form -- and the answer to "which repository" is usually what determines whether the next question is worth asking at all.

The prompt tells the model to name the likely options where there are few ("every morning, every Friday, or only when you ask?"), never to restate what it already knows, and to skip field 5 unless this is the kind of output where taste shows.

`MAX_INTAKE_ROUNDS` is 8, and a blank line ends the questions early. Either path sets `wrap_up=True`, which forces the model to write the request from what the user did say and fill the rest with obvious defaults.

### Revising rather than replacing

When the finished request is shown for approval and the user wants a change, `revise_request` folds their note into the paragraph with one harness call.

The obvious implementation -- replace the paragraph with whatever they typed next -- was what this replaced. A two-word note like "and only my own PRs" silently dropped everything else the interview had settled. Reading the note as a continuation of the same conversation means it only has to say what changes.

## Clarify

`clarify` returns up to three questions, or an empty list when the request is buildable. It is skipped when the description came out of the intake interview, since that has just settled the same questions; running both would put the user through the interrogation twice.

The prompt is explicit that only things that would change the generated workflow count as ambiguous. A model asked to find ambiguity will always find some. `MAX_CLARIFY_ROUNDS` is 3, because questions get diminishing.

## Finding tools

### Proposing searches

Composio's search filters by substring within a toolkit rather than ranking by relevance. A whole sentence matches almost nothing; `toolkit=github` plus `list pull requests` lands on the right tool.

So `propose_queries` asks for `{"toolkit": <slug or null>, "capability": "<2-4 keywords>"}` objects, at most `MAX_QUERIES` (4) of them. The model names services and actions and is told explicitly never to write slugs, which it cannot know and would invent.

### Searching

`search_candidates` runs each query through `catalogue.search`, pools results, de-duplicates by slug, and preserves order so the first search's matches stay near the top. `MAX_CANDIDATES` is 40.

A toolkit-scoped search that comes back empty is retried without the scope, since the model may have guessed a toolkit slug that does not exist.

`catalogue.SEARCH_LIMIT` is 20 per query, which is generous on purpose. The API returns matches in alphabetical order, not by relevance, so a narrow limit silently truncates before reaching the right tool. The model does the ranking, not the API.

### Selecting

`select_tools` shows the candidate list with each tool's read/write/destructive marking and asks for the ones this request needs.

Ranking alone is not trustworthy. A search for "post a message to a channel" can rank a delete tool first. So a model with the task in hand chooses, and a human confirms after that.

The prompt holds a specific balance: pick the fewest tools that accomplish the request, but do not omit a candidate that clearly satisfies part of it just to keep the list short. Prefer a read tool over a write tool. Include a write tool only when the request explicitly asks to post, send, or comment. A destructive tool is fine to propose when the request calls for it, because the user reviews every tool before anything is built.

`MAX_SELECT_ATTEMPTS` is 3, and an empty pick is retried. The same prompt against the same candidates can return `[]` on one attempt and the obviously-right selection on the next, so one empty response is treated as noise rather than a final answer. Without the retry, the build falls through to a planning pass with no tools, which then invents them.

Hallucinated slugs are dropped silently. The candidate list is the contract, and a slug that is not in it would fail validation anyway.

## The catalogue cache

`catalogue.py` reads Composio's REST API directly with `requests` rather than through the SDK, because the SDK models connected accounts and executions, not catalogue browsing.

Read/write classification comes from Composio's own MCP-style hints in each tool's `tags`:

```python
is_write="readOnlyHint" not in tags,
is_destructive="destructiveHint" in tags,
```

Absence of `readOnlyHint` means write. That is the safe direction: px0 gates writes behind explicit consent, so a mislabelled read tool costs a confirmation, while a mislabelled write tool costs a message nobody approved.

Discovered tools are cached in `.state/catalogue.json` rather than looked up at run time, for two reasons. A workflow must keep working offline and unchanged after it is written. And read-versus-write has to be knowable without a network call, because `--dry-run` decides what to stub from it.

The cache only ever grew until `forget` and `refresh` were added. `refresh` re-reads each cached tool and drops any that Composio has since deleted, rather than keeping a schema that no longer describes anything.

Discovered tool ids are prefixed `composio:`, so they never collide with a curated `provider.action` id and are obvious in a workflow file.

## Ordering: authorize before plan

`cli._build_workflow` runs authorization before the planning call. That ordering is deliberate.

The plan can only draw on the tools the user just confirmed. If a toolkit cannot be authorized, the workflow is unbuildable. Finding that out first avoids spending a planning call and printing a plan the user is then asked to commit to anyway.

The full sequence in `_build_workflow`:

1. Check for a near-duplicate with `similar_workflows`, and offer to edit that one instead.
2. Clarify, unless skipped.
3. Discover candidate tools, unless `--no-discover`.
4. Confirm the tool list with the user, calling out writes.
5. Cache the confirmed tools with `catalogue.remember`, before planning, so the plan and every later run resolve the same ids.
6. Authorize every toolkit those tools belong to.
7. Generate the plan.
8. Name it, unless the id is already pinned.
9. Show the rendered file and run `check_feasibility`.
10. Authorize anything the plan needs that discovery did not cover.
11. Ask for the commit, and settle the id, refusing to silently overwrite an existing one.
12. Select and author guidelines.
13. Write the file and record it as a versioned change.

### Duplicate detection

`similar_workflows` scores the new description against every existing workflow using word-set overlap, and reports anything at or above 0.4.

Local arithmetic, not a model call. This runs before every build, and a round trip to be told "no, nothing like it" on the common case would be a tax on the ordinary path. Being approximate is fine because the result is shown to a person, who can see at a glance whether it is the same job.

The failure it prevents is quiet: nothing looked, so a store accumulated three near-identical digests, each firing on its own schedule and each costing a run.

## The planning pass

`generate_plan` is one long, specific prompt. The specificity is the point, and most clauses in it are a failure that was observed once:

- Inputs must be read tools only.
- Never write a placeholder like `<OWNER>` or `TODO`; if the request does not name the repository an argument needs, leave the input out entirely rather than stubbing it.
- For a time window use the clock placeholders, because a scheduled run cannot use a literal date.
- Write tools go in `tools`, never in `inputs`.
- If `trigger.schedule` is set, `output.target` must be `file` with a path, even when the body also posts somewhere.
- The body must never ask the user for a value. A run has nobody to answer, so an unsettled detail is left out rather than deferred to a run that cannot ask.
- The body is scannable Markdown: numbered steps for anything sequential, bullets for enumerated sections, tool ids and template variables in backticks. Both the human reviewing the plan and the model executing it read a short list of steps far more reliably than a run-on sentence.

`_extract_json` locates the first JSON object or array in the reply rather than assuming the whole reply is JSON, because harnesses narrate around their answers.

## Feasibility

`check_feasibility(plan, home)` validates the plan against reality before the file is written:

- Unfinished arguments, through the same `input_arg_errors` a run uses.
- Unknown tool ids, with a did-you-mean from `difflib.get_close_matches`.
- Write tools used as inputs.
- An invalid cron expression.
- A scheduled plan whose output target is not `file`.

The last two duplicate rules in `workflow.validate` on purpose. Catching them here means the build fails with an actionable message instead of succeeding and letting the first scheduled run fail unattended.

## Naming

`generate_slug` asks for a short id-shaped name: lowercase, hyphens, at most 40 characters, capturing the service and the action in two to five words.

A mechanical slugify of the description produces the first forty characters of a sentence, which reads as noise once there are a dozen workflows to tell apart. The output is sanitized with a regex regardless of what comes back, and falls back to `new-workflow`.

## Guidelines

Two passes, doing opposite jobs.

### Selecting existing ones

`select_guidelines` reads the frontmatter descriptions of every attachable guideline and asks which ones this workflow's output is judged against.

Descriptions only, never bodies. The keyword scorer this replaced matched on filename and body overlap, which cannot tell writing a commit message from reading one: a nightly standup that summarizes commits scored highest against `commit-messages.md` and inlined a commit-authoring rubric into every run.

The prompt states that difference outright: a workflow that reads, summarizes, or reports on something is not governed by the convention for authoring that thing.

`MAX_ATTACHED_GUIDELINES` is 3. Every attached guideline is inlined verbatim into every run, so this is a prompt-budget ceiling as much as a relevance one. Past three, the workflow's own instructions stop being the loudest thing in the prompt.

The function returns `[]` freely, and on any harness failure. A wrong guideline is worse than none -- it spends prompt and pulls the output toward a convention the task never called for -- so nothing is attached on a guess and the build is never failed over this.

### Proposing a new one

`propose_guidelines` names standards the workflow depends on that no file covers, at most two.

A guideline is worth proposing only if the workflow's output is judged against it, it would apply again to the next workflow of this kind, and no existing file covers it. The prompt lists what not to propose: anything the body already specifies in full, generic best practice with no real choices in it, a restatement of what the workflow does, or a convention for authoring something this workflow only reads.

The cap is applied after filtering, not before. A junk first entry must not consume the budget and silently drop the valid proposal behind it.

`draft_guideline` then writes the body: two to five `## ` sections, each heading a short prescriptive instruction, each with two or three lines of prose. Sections are `## ` headings because that is what makes each rule addressable as a claim by `px0 guidelines log`. The model is told the file is inlined verbatim into every run, so it should say nothing it cannot justify and not pad to reach a section count.

The result is shown before it is saved, and it is an ordinary Markdown file afterwards, so a draft the user disagrees with is a redo or an edit rather than a dead end. This path exists because asking someone to type a convention from scratch was the step that stopped guidelines from ever getting written.

## Path safety

The model picks the guideline filename, so it is untrusted input on its way to becoming a filesystem path.

`_guideline_path` strips every character outside `[a-z0-9.-]`, drops `.` and `..` components, keeps at most the last two path segments, and forces a `.md` suffix.

`save_guideline` checks again at the point of writing:

```python
dest = (paths.guidelines_dir(home) / rel_path)
base = paths.guidelines_dir(home).resolve()
resolved = dest.resolve()
escaped = resolved != base and base not in resolved.parents
```

Checked in both places on purpose. `save_guideline` is the function that touches the disk, so it is the one place a traversing path has to be stopped for every caller to be safe. Callers that sanitize first pay nothing; one that forgets does not write outside the store.

## Editing is rebuilding

`px0 workflows edit` shows the original request, takes a new one, and runs the same pipeline with `existing_id` pinned.

A rebuild, not a text edit. The file is generated: its tools, inputs, and guideline list all follow from the request. Editing the request and regenerating keeps those consistent, where hand-editing the body would leave the frontmatter describing a workflow that no longer exists. The old version stays in the store's history either way.

Pinning the id is what makes an edit replace the workflow rather than forking a near-duplicate under a slightly different name.

## Next

[Part 6](06-running.md) covers what happens when one of these files runs.
