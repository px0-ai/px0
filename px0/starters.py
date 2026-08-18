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
    "summarization.md": """\
## Lead with the takeaway

Open with the single most important point, not with framing like "this
piece discusses" or "the article is about". A reader who stops after the
first sentence should still walk away with the core idea.

## Match length to source, not to a fixed template

A short note gets a short summary. Do not pad a three-paragraph blog post
into five bullet points just to look thorough, and do not compress a dense
paper into a single line that loses the argument.

## Keep the source's own claims, not your commentary

Report what the source says, not whether you agree with it. Leave out
value judgments ("this is a great point") unless the user asked for an
opinion.

## Preserve concrete details

Numbers, names, and specific examples carry more information than
adjectives. When space is limited, cut adjectives before cutting a
concrete detail.
""",
}

WORKFLOWS: dict[str, str] = {}
