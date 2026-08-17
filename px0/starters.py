"""Content for the store scaffolded by `px0 init`: built-in workflows and guidelines."""

GUIDELINES: dict[str, str] = {
    "commit-messages.md": """\
## Imperative mood summary line

Write the summary line in the imperative mood: "Add retry logic", not "Added"
or "Adds". Keep it under 72 characters and drop the trailing period.

## Explain why, not what

The diff already shows what changed. Use the body to explain why the change
was needed when that is not obvious from the diff alone.
""",
    "pr-descriptions.md": """\
## Lead with the problem

Open the description with the problem being solved, before the solution.
A reviewer who knows the problem can judge the solution faster.

## List a test plan

Every PR description ends with a checklist of how the change was verified.
""",
    "code-review/common.md": """\
## Flag missing error handling

Point out unhandled error returns and swallowed exceptions. Ask whether the
failure mode was considered, don't just assert that it wasn't.

## Prefer small, focused diffs

A PR that mixes an unrelated refactor with a bug fix is harder to review and
harder to revert. Call this out when seen.
""",
    "code-review/go.md": """\
## Wrap errors with %w

Wrap errors with `fmt.Errorf("...: %w", err)` so callers can use `errors.Is`
and `errors.As`. Bare `%v` wrapping discards the chain.

## Context is the first parameter

`context.Context` is always the first parameter and is never stored in a
struct.
""",
    "code-review/python.md": """\
## Type hints on public functions

Public functions and methods carry type hints on parameters and return
values. Internal helpers are exempt when the types are obvious from context.

## No bare except

`except:` and `except Exception:` without re-raising hide bugs. Catch the
specific exception type being handled.
""",
}

WORKFLOWS: dict[str, str] = {
    "standup-summary.md": """\
---
id: standup-summary
kind: workflow
version: 1
description: Draft yesterday's standup update from commits, PRs, and reviews.
trigger:
  manual: true
  schedule: "0 9 * * 1-5"
guidelines:
  - commit-messages.md
inputs:
  - id: activity
    tool: github.list_my_prs
    args:
      repos: "{{config.connectors.github.repos}}"
      since: -1d
  - id: events
    tool: calendar.list_events
    args:
      window: yesterday
    optional: true
output:
  target: file
  path: outputs/standup-{date}.md
  format: markdown
timeout: 120s
---
Write my standup update in first person, three sections: yesterday, today,
blockers. Yesterday comes from {{activity}}, with meetings from {{events}}.
Today comes from open PRs and assigned issues. Keep it under 120 words.
""",
    "pr-precheck.md": """\
---
id: pr-precheck
kind: workflow
version: 1
description: Run code-review guidelines against a local diff.
trigger:
  manual: true
guidelines:
  - code-review/common.md
inputs:
  - id: diff
    source: stdin
output:
  target: stdout
  format: markdown
timeout: 120s
---
Review {{diff}} against the guidelines above. List every violation found,
quoting the offending line and naming the guideline it breaks. Say "no
violations found" if there are none.
""",
    "review-pr.md": """\
---
id: review-pr
kind: workflow
version: 1
description: Draft review comments for a PR; posts only if granted.
trigger:
  manual: true
guidelines:
  - code-review/common.md
  - code-review/go.md
  - code-review/python.md
inputs:
  - id: pr
    tool: github.get_pr
    args:
      url: "{{input.url}}"
  - id: diff
    tool: github.get_pr_diff
    args:
      url: "{{input.url}}"
tools: [github.get_pr_diff]
output:
  target: file
  path: outputs/review-{date}.md
  format: markdown
timeout: 180s
---
Draft review comments for {{pr}} against {{diff}}, checked against the
guidelines above. Group comments by file and line. Do not post anything;
only draft.
""",
    "consolidate.md": """\
---
id: consolidate
kind: workflow
version: 1
description: The review session over proposals, decay, contradictions.
trigger:
  manual: true
  schedule: "0 9 * * 1"
output:
  target: file
  path: outputs/consolidate-{date}.md
  format: markdown
timeout: 300s
---
This workflow is a thin wrapper; `px0 consolidate` runs the actual
proposal-review session (see `px0 guidelines review`).
""",
    "skills-build.md": """\
---
id: skills-build
kind: workflow
version: 1
description: Compile guidelines into harness skill bundles.
trigger:
  manual: true
output:
  target: stdout
  format: markdown
timeout: 120s
---
This workflow is a thin wrapper; `px0 skills build` compiles
`guidelines/` into `skills/`.
""",
    "weekly-digest.md": """\
---
id: weekly-digest
kind: workflow
version: 1
description: "Week in review: merged, reviewed, learned."
trigger:
  manual: true
  schedule: "0 16 * * 5"
guidelines:
  - pr-descriptions.md
inputs:
  - id: activity
    tool: github.list_my_prs
    args:
      repos: "{{config.connectors.github.repos}}"
      since: -7d
output:
  target: file
  path: outputs/weekly-digest-{date}.md
  format: markdown
timeout: 120s
---
Summarize the week from {{activity}}: what merged, what was reviewed, and
one thing learned. Three short sections, under 200 words total.
""",
}
