"""An argument a plan could not settle must never reach a connector, and the
clock vocabulary is one vocabulary.

`owner: <OWNER>` used to be sent as written, and GitHub answered the resulting
`/repos/<OWNER>/<REPO>/pulls/comments` with a 404 "Not Found" -- an error about
a missing repository, for a workflow that was simply unfinished. Same for a
template nothing provides, which resolved to None and was sent as a missing
value. Both are now caught before the network: at build time by
`check_feasibility`, and at run time by `validate`, which runs first.
"""

import pytest

from px0 import builder as builder_mod, runner, workflow as wf_mod


def _wf(tmp_home, inputs, wid="w"):
    text = f"---\nid: {wid}\ninputs:\n{inputs}---\n\nbody\n"
    dest = tmp_home / "workflows" / f"{wid}.md"
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(text)
    return wf_mod.parse(dest)


def _errors(tmp_home, inputs):
    return wf_mod.validate(_wf(tmp_home, inputs), tmp_home)


# A curated read tool, so these exercise the argument rule and not tool resolution.
_READ_TOOL = "github.list_my_prs"


# --- the reported failure ----------------------------------------------------

def test_a_placeholder_argument_is_named_before_any_call_is_made(tmp_home):
    errors = _errors(tmp_home, f"""\
  - id: review_comments
    tool: {_READ_TOOL}
    args:
      owner: <OWNER>
      repo: <REPO>
      since: <YYYY-MM-01T00:00:00Z>
""")

    assert len(errors) == 3
    assert "args.owner is still the placeholder '<OWNER>'" in errors[0]
    assert "px0 workflows edit w" in errors[0], "the error says how to fix it"
    assert all("review_comments" in e for e in errors), "and which input it is"


def test_a_template_nothing_provides_is_an_error_not_a_none_argument(tmp_home):
    errors = _errors(tmp_home, f"""\
  - id: my_commits
    tool: {_READ_TOOL}
    args:
      author: '{{{{github_username}}}}'
""")

    assert len(errors) == 1
    assert "references {{github_username}}" in errors[0]
    assert "--input github_username=<value>" in errors[0]


def test_a_valid_workflow_reports_nothing(tmp_home):
    assert _errors(tmp_home, f"""\
  - id: my_commits
    tool: {_READ_TOOL}
    args:
      owner: px0-ai
      repo: px0
      since: '{{{{now-24h}}}}'
      per_page: 100
""") == []


# --- what a template may reference ------------------------------------------

@pytest.mark.parametrize("value", [
    "{{input.repo}}",              # passed with --input
    "{{config.connectors.retries}}",
    "{{now}}", "{{today}}", "{{date}}", "{{datetime}}",
    "{{now-30m}}", "{{now-24h}}", "{{now-7d}}", "{{now-2w}}",
    "since {{now-1d}} until {{now}}",
])
def test_references_a_run_can_resolve_are_accepted(tmp_home, value):
    assert _errors(tmp_home, f"""\
  - id: a
    tool: {_READ_TOOL}
    args:
      q: '{value}'
""") == []


@pytest.mark.parametrize("value", ["{{now-24}}", "{{now-h}}", "{{now-24y}}", "{{tomorrow}}"])
def test_a_reference_that_only_looks_like_a_clock_is_rejected(tmp_home, value):
    """The grammar is closed: anything outside it resolves to None at run time."""
    errors = _errors(tmp_home, f"""\
  - id: a
    tool: {_READ_TOOL}
    args:
      q: '{value}'
""")
    assert len(errors) == 1 and "which nothing provides" in errors[0]


def test_an_earlier_inputs_id_may_be_referenced_but_a_later_one_may_not(tmp_home):
    """Inputs resolve top to bottom, so only what is above is available."""
    errors = _errors(tmp_home, f"""\
  - id: first
    tool: {_READ_TOOL}
    args:
      q: '{{{{second}}}}'
  - id: second
    tool: {_READ_TOOL}
    args:
      q: '{{{{first}}}}'
""")

    assert len(errors) == 1
    assert "input 'first' args.q references {{second}}" in errors[0]


# --- where placeholders hide -------------------------------------------------

def test_a_placeholder_nested_in_a_list_or_object_is_found(tmp_home):
    errors = _errors(tmp_home, f"""\
  - id: a
    tool: {_READ_TOOL}
    args:
      filters:
        repos:
          - px0-ai/px0
          - <OTHER_REPO>
        window:
          from: <START>
""")

    locations = sorted(e.split(" is still ")[0].split()[-1] for e in errors)
    assert locations == ["args.filters.repos[1]", "args.filters.window.from"]


def test_a_retrieval_query_is_checked_too(tmp_home):
    errors = _errors(tmp_home, """\
  - id: notes
    retrieve:
      query: notes about <TOPIC>
      k: 5
""")

    assert errors == [], "a placeholder inside a longer string is not the whole value"

    errors = _errors(tmp_home, """\
  - id: notes
    retrieve:
      query: <TOPIC>
      k: 5
""")
    assert len(errors) == 1 and "retrieve.query" in errors[0]


def test_slack_style_angle_brackets_inside_a_string_are_left_alone(tmp_home):
    """`<@U123>` and `<https://url|text>` are Slack's own syntax, not a stub."""
    assert _errors(tmp_home, f"""\
  - id: a
    tool: {_READ_TOOL}
    args:
      q: 'raised by <@U123> in <https://example.com|the thread>'
""") == []


# --- the same rule at build time --------------------------------------------

def test_the_build_refuses_a_plan_that_left_an_argument_unsettled(tmp_home):
    plan = builder_mod.Plan(
        trigger={"manual": True}, output={"target": "stdout"}, body="b", description="d",
        tools=[], inputs=[{"id": "c", "tool": _READ_TOOL,
                           "args": {"owner": "<OWNER>", "repo": "px0"}}])

    issues = builder_mod.check_feasibility(plan, tmp_home)

    assert len(issues) == 1
    assert "<OWNER>" in issues[0]
    assert "re-run and name the real value" in issues[0], \
        "at build time the fix is a better request, not an edit"


def test_the_build_accepts_a_plan_whose_arguments_are_all_settled(tmp_home):
    plan = builder_mod.Plan(
        trigger={"manual": True}, output={"target": "stdout"}, body="b", description="d",
        tools=[], inputs=[{"id": "c", "tool": _READ_TOOL,
                           "args": {"owner": "px0-ai", "repo": "px0",
                                    "since": "{{now-24h}}"}}])

    assert builder_mod.check_feasibility(plan, tmp_home) == []


# --- what the clock placeholders resolve to ---------------------------------

def test_the_clock_placeholders_resolve_to_timestamps_a_connector_accepts(tmp_home):
    ctx = {"config": {}, "input": {}}

    now = runner.render_value("{{now}}", ctx)
    day_ago = runner.render_value("{{now-24h}}", ctx)

    assert now.endswith("Z") and "T" in now, "ISO 8601, which `since`/`until` take"
    assert day_ago < now
    assert runner.render_value("{{today}}", ctx) == now[:10]


def test_an_input_named_now_still_shadows_the_clock(tmp_home):
    """Context first: a real value never loses to a built-in."""
    assert runner.render_value("{{now}}", {"now": "mine"}) == "mine"


def test_every_name_validation_accepts_resolves_to_a_value():
    """The grammar validation checks and the one the runner implements are the
    same grammar, so a workflow cannot pass validation and then resolve to None."""
    for name in ("now", "today", "date", "datetime", "now-1m", "now-24h", "now-7d", "now-2w"):
        assert wf_mod.is_time_placeholder(name)
        assert runner._time_value(name) is not None

    for name in ("now-24", "tomorrow", "github_username"):
        assert not wf_mod.is_time_placeholder(name)
        assert runner._time_value(name) is None


# --- output.path: the same vocabulary, checked before the model runs ---------

def test_a_path_placeholder_an_argument_may_use_works_in_a_path_too(tmp_home):
    """The two vocabularies used to differ: arguments took `{{today}}`, paths
    took only `{date}`/`{datetime}`/`{time}`, so `logs/daily-{{today}}.md` was
    accepted everywhere except where it was used."""
    for name in wf_mod.TIME_PLACEHOLDER_NAMES + ("now-24h", "now-7d"):
        assert wf_mod.output_path_errors(f"logs/daily-{{{{{name}}}}}.md") == []
        assert "{" not in runner._render_output_path(f"logs/daily-{{{{{name}}}}}.md")


def test_an_unknown_path_placeholder_fails_validation_not_the_run(tmp_home):
    """It used to surface from stage 7, after the model call: a typo in a
    filename cost a whole run to discover."""
    wf = _wf(tmp_home, f"""\
  - id: a
    tool: {_READ_TOOL}
    args:
      owner: px0-ai
""")
    wf.output = {"target": "file", "path": "logs/daily-{week}.md"}

    errors = wf_mod.validate(wf, tmp_home)

    assert len(errors) == 1
    assert "unknown placeholder(s): week" in errors[0]
    assert "{now-<N><m|h|d|w>}" in errors[0], "the error names what is allowed"


def test_an_input_id_in_a_path_is_reported_rather_than_written_into_a_filename(tmp_home):
    assert wf_mod.output_path_errors("logs/{my_commits}.md")


def test_a_path_with_no_placeholders_is_fine():
    assert wf_mod.output_path_errors("logs/daily.md") == []


def test_a_path_renders_filename_safe_while_an_argument_renders_iso_8601():
    """Same instant, two contexts: `since` wants a timestamp, a filename does
    not want colons in it."""
    in_path = runner._render_output_path("r-{{now}}.md")
    in_arg = runner.render_value("{{now}}", {})

    assert ":" not in in_path
    assert in_arg.endswith("Z") and ":" in in_arg
    assert in_path[2:12] == in_arg[:10], "the same day, formatted for its place"
