"""`px0 workflows new` should offer to write down the standards it depends on.

A review rubric or a writing voice is the user's own preference -- px0 cannot
infer it, so the build asks, saves the answer as a guideline, lists it on the
workflow, and the runner inlines it into every run.
"""

import pytest

from px0 import builder as builder_mod, claims, runner, versioning, workflow as wf_mod


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
      [{"path": "commit-messages.md", "title": "Commit style", "why": "w", "ask": "a"},
       {"path": "review-rubric.md",   "title": "Review rubric", "why": "w", "ask": "a"}]
    """)

    out = builder_mod.propose_guidelines({}, "d", _plan(), existing=["commit-messages.md"])

    assert [p.path for p in out] == ["review-rubric.md"]


def test_two_proposals_for_the_same_file_collapse_to_one(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: """
      [{"path": "style.md", "title": "Style", "why": "w", "ask": "a"},
       {"path": "Style.MD", "title": "Style again", "why": "w", "ask": "a"}]
    """)

    assert len(builder_mod.propose_guidelines({}, "d", _plan(), [])) == 1


def test_a_proposal_missing_a_path_or_title_is_dropped(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: """
      [{"path": "", "title": "No path", "why": "w", "ask": "a"},
       {"path": "ok.md", "title": "", "why": "w", "ask": "a"},
       "just a string",
       {"path": "good.md", "title": "Good", "why": "w", "ask": "a"}]
    """)

    assert [p.path for p in builder_mod.propose_guidelines({}, "d", _plan(), [])] == ["good.md"]


def test_no_proposals_is_a_normal_answer(monkeypatch):
    """Most workflows hold no opinion worth writing down."""
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: "[]")
    assert builder_mod.propose_guidelines({}, "d", _plan(), []) == []


def test_a_proposal_with_no_ask_still_gets_a_usable_question(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k:
                        '[{"path": "x.md", "title": "Commit style"}]')

    assert builder_mod.propose_guidelines({}, "d", _plan(), [])[0].ask


def test_at_most_two_proposals_reach_the_user(monkeypatch):
    """Authoring is interactive; a build must not turn into an interview."""
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: str(
        [{"path": f"g{i}.md", "title": f"T{i}", "why": "w", "ask": "a"} for i in range(5)]
    ).replace("'", '"'))

    assert len(builder_mod.propose_guidelines({}, "d", _plan(), [])) == 2


# --- drafting ---------------------------------------------------------------

_PROPOSAL = builder_mod.GuidelineProposal(
    path="review-rubric.md", title="Review rubric", why="w", ask="What do you look for?")


def test_a_draft_is_trimmed_to_start_at_its_first_section(monkeypatch):
    """Harnesses narrate; a guideline file starts at a heading."""
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k:
                        "Sure! Here's the guideline:\n\n## Flag only real breakage\n\nBody.\n")

    content = builder_mod.draft_guideline({}, _PROPOSAL, "only real breakage")

    assert content.startswith("## Flag only real breakage")
    assert "Sure!" not in content


def test_a_draft_with_no_sections_is_refused(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke",
                        lambda *a, **k: "I could not write that guideline.")

    with pytest.raises(builder_mod.BuilderError, match="`## `"):
        builder_mod.draft_guideline({}, _PROPOSAL, "something")


def test_the_users_answer_is_what_reaches_the_model(monkeypatch):
    """The user's words are the authority; the pass only shapes them."""
    seen = {}
    monkeypatch.setattr(builder_mod.harness, "invoke",
                        lambda cfg, prompt, **k: seen.setdefault("p", prompt) or "## H\n\nb\n")

    builder_mod.draft_guideline({}, _PROPOSAL, "never comment on formatting")

    assert "never comment on formatting" in seen["p"]
    assert "What do you look for?" in seen["p"], "the question gives the answer context"


# --- saving -----------------------------------------------------------------

def test_a_saved_guideline_gets_version_and_claim_history(tmp_home):
    """Written through the guideline change path, not as a bare file, so
    `guidelines log` / `why` / `revert` work on it from version 1."""
    content = "## Flag only real breakage\n\nOnly production breakage.\n"

    dest = builder_mod.save_guideline(tmp_home, "review-rubric.md", content)

    assert dest.read_text() == content
    versions = versioning.list_versions(tmp_home, "guidelines/review-rubric.md")
    assert len(versions) == 1 and versions[0]["actor"] == "builder"
    claim = "guidelines/review-rubric.md#flag-only-real-breakage"
    assert claims.guidelines_log(tmp_home, claim), "the claim must have history"


def test_a_nested_guideline_creates_its_folder(tmp_home):
    dest = builder_mod.save_guideline(tmp_home, "code-review/go.md", "## H\n\nb\n")
    assert dest.exists() and dest.parent.name == "code-review"


# --- the whole loop: authored file -> workflow -> run prompt -----------------

def test_an_authored_guideline_is_listed_and_then_inlined_at_run_time(tmp_home):
    content = "## Leave formatting to the linter\n\nNever comment on spacing.\n"
    builder_mod.save_guideline(tmp_home, "review-rubric.md", content)

    file_text = builder_mod.render_workflow_file(
        "review", _plan(), ["review-rubric.md"], "review my PRs")
    builder_mod.save_workflow(tmp_home, "review", file_text)

    wf = wf_mod.load_all(tmp_home)["review"]
    assert wf.guidelines == ["review-rubric.md"]
    assert wf_mod.validate(wf, tmp_home) == [], "a listed guideline must resolve"

    prompt = runner.render_prompt(wf, {"review-rubric.md": content}, {})
    assert "Never comment on spacing." in prompt, "the content must reach the run"
