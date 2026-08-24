"""`px0 workflows new` is the only thing that writes a guideline.

There is no `px0 guidelines new`, so the build has to notice that a workflow
leans on a durable convention, draft it from the workflow itself, save it with
the `name`/`description` frontmatter that makes it findable again, list it on
the workflow, and let the runner inline its body into every run.
"""

import re

import pytest

from px0 import (builder as builder_mod, claims, cli, guidelines as guidelines_mod,
                 paths, runner, ui, versioning, workflow as wf_mod)


def _plan(body="Review each open PR and comment.", description="Review my PRs"):
    return builder_mod.Plan(trigger={"type": "manual"}, inputs=[], tools=[],
                            output={"target": "stdout"}, body=body, description=description)


# --- the proposed path is untrusted input that becomes a filesystem path ----

@pytest.mark.parametrize("raw, expected", [
    ("code-review/python.md", "code-review/python.md"),
    ("Commit Messages", "commit-messages.md"),
    ("style.MD", "style.md"),
    ("../../etc/passwd", "etc/passwd.md"),
    ("/absolute/Path.md", "absolute/path.md"),
    ("a/b/c/deep.md", "c/deep.md"),          # guidelines/ is one folder deep
    ("", ""),
    ("../..", ""),
])
def test_a_model_proposed_path_is_normalized_and_contained(raw, expected):
    assert builder_mod._guideline_path(raw) == expected


def test_a_traversal_attempt_cannot_escape_the_guidelines_directory(tmp_home):
    from px0 import paths

    rel = builder_mod._guideline_path("../../../../etc/shadow")
    dest = (paths.guidelines_dir(tmp_home) / rel).resolve()

    assert dest.is_relative_to(paths.guidelines_dir(tmp_home).resolve())


# --- proposals ---------------------------------------------------------------

def test_proposals_never_duplicate_a_guideline_the_store_already_has(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: """
      [{"path": "commit-messages.md", "title": "Commit style", "why": "w"},
       {"path": "review-rubric.md",   "title": "Review rubric", "why": "w"}]
    """)

    out = builder_mod.propose_guidelines({}, "d", _plan(), existing=["commit-messages.md"])

    assert [p.path for p in out] == ["review-rubric.md"]


def test_two_proposals_for_the_same_file_collapse_to_one(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: """
      [{"path": "style.md", "title": "Style", "why": "w"},
       {"path": "Style.MD", "title": "Style again", "why": "w"}]
    """)

    assert len(builder_mod.propose_guidelines({}, "d", _plan(), [])) == 1


def test_a_proposal_missing_a_path_or_title_is_dropped(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: """
      [{"path": "", "title": "No path", "why": "w"},
       {"path": "ok.md", "title": "", "why": "w"},
       "just a string",
       {"path": "good.md", "title": "Good", "why": "w"}]
    """)

    assert [p.path for p in builder_mod.propose_guidelines({}, "d", _plan(), [])] == ["good.md"]


def test_no_proposals_is_a_normal_answer(monkeypatch):
    """Most workflows hold no opinion worth writing down."""
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: "[]")
    assert builder_mod.propose_guidelines({}, "d", _plan(), []) == []


def test_a_proposal_needs_only_a_path_and_a_title(monkeypatch):
    """`why` is explanatory; a proposal without one is still worth drafting."""
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k:
                        '[{"path": "x.md", "title": "Commit style"}]')

    out = builder_mod.propose_guidelines({}, "d", _plan(), [])

    assert [(p.path, p.title, p.why) for p in out] == [("x.md", "Commit style", "")]


def test_a_proposal_carries_the_description_later_builds_match_against(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: """
      [{"path": "review-rubric.md", "title": "Review rubric",
        "description": "What a code review comments on. Use when the workflow reviews code.",
        "why": "w"}]
    """)

    proposal = builder_mod.propose_guidelines({}, "d", _plan(), [])[0]

    assert proposal.description.startswith("What a code review comments on")
    assert proposal.name == "review-rubric", "the name is the filename, as with a skill"


def test_a_proposal_without_a_description_falls_back_to_its_title(monkeypatch):
    """An undescribed guideline is invisible to every later selection pass, which
    is worse than a terse line."""
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k:
                        '[{"path": "x.md", "title": "Commit style"}]')

    assert builder_mod.propose_guidelines({}, "d", _plan(), [])[0].description == "Commit style"


def test_at_most_two_proposals_reach_the_user(monkeypatch):
    """Every draft costs a model call and a decision; a build is not an interview."""
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: str(
        [{"path": f"g{i}.md", "title": f"T{i}", "why": "w"} for i in range(5)]
    ).replace("'", '"'))

    assert len(builder_mod.propose_guidelines({}, "d", _plan(), [])) == 2


# --- drafting ---------------------------------------------------------------

_PROPOSAL = builder_mod.GuidelineProposal(
    path="review-rubric.md", title="Review rubric",
    why="the workflow comments on PRs and has no rubric to comment against")


def test_a_draft_is_trimmed_to_start_at_its_first_section(monkeypatch):
    """Harnesses narrate; a guideline file starts at a heading."""
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k:
                        "Sure! Here's the guideline:\n\n## Flag only real breakage\n\nBody.\n")

    content = builder_mod.draft_guideline({}, _PROPOSAL, "review my PRs", _plan())

    assert content.startswith("## Flag only real breakage")
    assert "Sure!" not in content


def test_a_draft_with_no_sections_is_refused(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke",
                        lambda *a, **k: "I could not write that guideline.")

    with pytest.raises(builder_mod.BuilderError, match="`## `"):
        builder_mod.draft_guideline({}, _PROPOSAL, "review my PRs", _plan())


def test_the_workflow_is_what_reaches_the_model(monkeypatch):
    """Nobody is interviewed, so the workflow itself has to carry the context."""
    seen = {}
    monkeypatch.setattr(builder_mod.harness, "invoke",
                        lambda cfg, prompt, **k: seen.setdefault("p", prompt) or "## H\n\nb\n")

    builder_mod.draft_guideline({}, _PROPOSAL, "review my PRs",
                                _plan(body="Review each open PR and comment."))

    assert "Review rubric" in seen["p"]
    assert "Review each open PR and comment." in seen["p"], "the plan body is the context"
    assert _PROPOSAL.why in seen["p"], "why it is needed narrows what to write"


# --- saving -----------------------------------------------------------------

def test_a_saved_guideline_gets_version_and_claim_history(tmp_home):
    """Written through the guideline change path, not as a bare file, so
    `px0 guidelines log` works on it from version 1."""
    body = "## Flag only real breakage\n\nOnly production breakage.\n"

    dest = builder_mod.save_guideline(tmp_home, "review-rubric.md", body)

    assert body in dest.read_text(), "the rules are kept verbatim"
    versions = versioning.list_versions(tmp_home, "guidelines/review-rubric.md")
    assert len(versions) == 1 and versions[0]["actor"] == "builder"
    claim = "guidelines/review-rubric.md#flag-only-real-breakage"
    assert claims.guidelines_log(tmp_home, claim), "the claim must have history"


def test_a_nested_guideline_creates_its_folder(tmp_home):
    dest = builder_mod.save_guideline(tmp_home, "code-review/go.md", "## H\n\nb\n")
    assert dest.exists() and dest.parent.name == "code-review"


def test_a_saved_guideline_is_named_and_described_in_its_frontmatter(tmp_home):
    """The frontmatter is what makes the file findable: `select_guidelines` reads
    descriptions and nothing else."""
    dest = builder_mod.save_guideline(
        tmp_home, "code-review/go.md", "## Wrap errors with %w\n\nAlways.\n",
        description="What a Go review checks. Use when the workflow reviews Go code.")

    g = guidelines_mod.parse(dest, "code-review/go.md")
    assert g.name == "go", "the folder groups the topic; the file names it"
    assert g.description.startswith("What a Go review checks")
    assert g.body.startswith("## Wrap errors with %w")
    assert g.described


def test_a_description_with_a_colon_still_parses(tmp_home):
    """The frontmatter is dumped as YAML, not formatted by hand."""
    dest = builder_mod.save_guideline(
        tmp_home, "voice.md", "## H\n\nb\n",
        description="Voice: plain, short sentences. Use when the workflow writes prose.")

    assert guidelines_mod.parse(dest, "voice.md").description.startswith("Voice: plain")


def test_a_body_that_already_carries_frontmatter_keeps_its_own(tmp_home):
    """A re-save must not wrap one file's frontmatter in another."""
    text = guidelines_mod.render("voice", "how I write", "## Say it plainly\n\nShort.\n")

    dest = builder_mod.save_guideline(tmp_home, "voice.md", text, description="ignored")

    assert dest.read_text().count("---") == 2
    assert guidelines_mod.parse(dest, "voice.md").description == "how I write"


# --- the whole loop: authored file -> workflow -> run prompt -----------------

def test_an_authored_guideline_is_listed_and_then_inlined_at_run_time(tmp_home):
    body = "## Leave formatting to the linter\n\nNever comment on spacing.\n"
    builder_mod.save_guideline(tmp_home, "review-rubric.md", body,
                              description="What a review comments on.")

    file_text = builder_mod.render_workflow_file(
        "review", _plan(), ["review-rubric.md"], "review my PRs")
    builder_mod.save_workflow(tmp_home, "review", file_text)

    wf = wf_mod.load_all(tmp_home)["review"]
    assert wf.guidelines == ["review-rubric.md"]
    assert wf_mod.validate(wf, tmp_home) == [], "a listed guideline must resolve"

    prompt = runner.render_prompt(
        wf, {"review-rubric.md": guidelines_mod.body_of(tmp_home, "review-rubric.md")}, {})
    assert "Never comment on spacing." in prompt, "the content must reach the run"
    assert "description:" not in prompt, "the frontmatter is px0's index, not the model's"
    assert "# review-rubric\n" in prompt, "headed by its name"


# --- the build's authoring pass: draft, show, keep / again / skip ------------

_ANSI = re.compile(r"\x1b\[[0-9;]*m")


def _proposal(path="review-rubric.md", title="Review rubric"):
    return builder_mod.GuidelineProposal(
        path=path, title=title, why="no rubric yet",
        description="What a review comments on. Use when the workflow reviews code.")


def test_the_build_drafts_a_needed_guideline_without_interviewing_anyone(
        tmp_home, monkeypatch, capsys):
    """The whole point of dropping `guidelines new`: nobody is asked to compose
    a convention from a blank page, so the draft comes from the workflow."""
    monkeypatch.setattr(builder_mod, "propose_guidelines",
                        lambda *a, **k: [_proposal()])
    monkeypatch.setattr(builder_mod, "draft_guideline",
                        lambda *a, **k: "## Flag only real breakage\n\nOnly that.\n")
    monkeypatch.setattr(ui, "prompt", lambda *a, **k: "")   # empty answer keeps it

    created = cli._author_guidelines(tmp_home, {}, "review my PRs", _plan(), [], False)

    assert created == ["review-rubric.md"]
    dest = paths.guidelines_dir(tmp_home) / "review-rubric.md"
    assert "Only that." in dest.read_text()
    g = guidelines_mod.parse(dest, "review-rubric.md")
    assert g.description.startswith("What a review comments on"), \
        "the proposal's description is what the file is matched against later"
    out = capsys.readouterr().out
    assert "guidelines/review-rubric.md" in out, "the user is told where it landed"
    assert "What a review comments on" in out, "and shown what it will apply to"


def test_a_draft_the_user_rejects_is_not_written(tmp_home, monkeypatch):
    monkeypatch.setattr(builder_mod, "propose_guidelines",
                        lambda *a, **k: [_proposal()])
    monkeypatch.setattr(builder_mod, "draft_guideline", lambda *a, **k: "## H\n\nb\n")
    monkeypatch.setattr(ui, "prompt", lambda *a, **k: "n")

    created = cli._author_guidelines(tmp_home, {}, "d", _plan(), [], False)

    assert created == []
    assert not (paths.guidelines_dir(tmp_home) / "review-rubric.md").exists()


def test_again_redraws_before_anything_is_saved(tmp_home, monkeypatch):
    drafts = iter(["## First\n\na\n", "## Second\n\nb\n"])
    answers = iter(["again", ""])
    monkeypatch.setattr(builder_mod, "propose_guidelines",
                        lambda *a, **k: [_proposal()])
    monkeypatch.setattr(builder_mod, "draft_guideline", lambda *a, **k: next(drafts))
    monkeypatch.setattr(ui, "prompt", lambda *a, **k: next(answers))

    cli._author_guidelines(tmp_home, {}, "d", _plan(), [], False)

    assert guidelines_mod.parse(
        paths.guidelines_dir(tmp_home) / "review-rubric.md", "review-rubric.md"
    ).body.startswith("## Second")


def test_a_non_interactive_build_writes_no_guideline(tmp_home, monkeypatch):
    """Under --yes there is nobody to show a draft to, so nothing is guessed at."""
    monkeypatch.setattr(builder_mod, "propose_guidelines",
                        lambda *a, **k: pytest.fail("must not even ask"))

    assert cli._author_guidelines(tmp_home, {}, "d", _plan(), [], True) == []


def test_a_failed_draft_does_not_fail_the_build(tmp_home, monkeypatch, capsys):
    monkeypatch.setattr(builder_mod, "propose_guidelines",
                        lambda *a, **k: [_proposal()])
    monkeypatch.setattr(builder_mod, "draft_guideline",
                        lambda *a, **k: (_ for _ in ()).throw(builder_mod.BuilderError("nope")))

    assert cli._author_guidelines(tmp_home, {}, "d", _plan(), [], False) == []
    # routed to stdout with the rest of the build's narration, not to stderr
    assert "could not draft it" in capsys.readouterr().out


# --- the listing ------------------------------------------------------------

def test_guidelines_are_listed_with_the_description_a_build_matches_on(
        tmp_home, monkeypatch, capsys):
    """Same rows as the `workflows run` picker, and the same line the build
    chooses from -- so what decides an attachment is what the user reads."""
    monkeypatch.setattr(ui, "_forced", False)
    base = paths.guidelines_dir(tmp_home)
    (base / "voice.md").write_text(
        guidelines_mod.render("voice", "How I write prose", "## Say it plainly\n\nShort.\n"))
    (base / "review-rubric.md").write_text(
        guidelines_mod.render("review-rubric", "What a review flags", "## Flag breakage\n\nOnly.\n"))

    cli._print_guidelines(tmp_home, heading=False)

    lines = [_ANSI.sub("", ln).strip() for ln in capsys.readouterr().out.splitlines() if ln.strip()]
    assert lines == [
        "1. review-rubric.md  What a review flags",
        "2. voice.md          How I write prose",
    ]


def test_a_guideline_without_frontmatter_is_listed_by_its_first_rule(
        tmp_home, monkeypatch, capsys):
    """Files written before frontmatter was the format still read back."""
    monkeypatch.setattr(ui, "_forced", False)
    (paths.guidelines_dir(tmp_home) / "voice.md").write_text(
        "## Say it plainly\n\nShort sentences.\n")

    cli._print_guidelines(tmp_home, heading=False)

    out = _ANSI.sub("", capsys.readouterr().out)
    assert "voice.md" in out and "Say it plainly" in out


def test_an_empty_store_says_where_guidelines_come_from(tmp_home, capsys):
    cli._print_guidelines(tmp_home, heading=False)
    assert "px0 workflows new" in capsys.readouterr().out
