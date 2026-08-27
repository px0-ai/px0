"""Turning one store's workflow into a template anybody can fill in.

The risk here is not that a template comes out imperfect. It is that this
command rewrites a file the user is likely to hand to somebody else, so the
assertions below are mostly about what cannot happen: a literal the scan never
offered cannot become a var, a var nobody could interpret is refused, a rewrite
that would not load is never written, and a template no unattended run could
ever fill in does not pass validation.
"""

import json

import pytest

from px0 import cli, paths, templates
from px0 import runner as runner_mod
from px0 import tools as tools_mod
from px0 import workflow as workflow_mod


def write(home, wid: str, text: str):
    path = paths.workflows_dir(home) / f"{wid}.md"
    path.write_text(text)
    return workflow_mod.parse(path)


DIGEST = """\
---
id: digest
kind: workflow
description: Summarize a folder and say where it went
inputs:
  - id: files
    tool: file.list
    args:
      path: /Users/someone/code/api
      author: octocat
tools:
  - file.write
output:
  target: stdout
timeout: 120s
---
Summarize /Users/someone/code/api and post it to #eng-standup.
"""


@pytest.fixture
def digest(tmp_home):
    return write(tmp_home, "digest", DIGEST)


def reply_with(payload: dict):
    """A harness that answers with one JSON proposal."""
    return lambda *a, **kw: json.dumps(payload)


# --- the scan -------------------------------------------------------------
#
# What is eligible is decided here and nowhere else. Every test in this section
# is really one assertion: the model cannot widen this set.

def test_every_string_argument_is_a_candidate(digest):
    literals = {c.literal for c in templates.candidates(digest)}
    assert "/Users/someone/code/api" in literals
    assert "octocat" in literals, "an argument's value is a setting by construction"


def test_the_body_offers_a_channel_but_not_a_phrase(digest):
    found = {c.literal: c for c in templates.candidates(digest)}
    assert "#eng-standup" in found
    assert not any(c.literal == "Summarize" for c in found.values())
    assert not any("post it to" in c.literal for c in found.values())


def test_a_markdown_heading_is_not_a_channel(tmp_home):
    wf = write(tmp_home, "headed",
               "---\nid: headed\ndescription: d\noutput:\n  target: stdout\n---\n"
               "## The digest\n\nWrite it.\n")
    assert templates.candidates(wf) == []


def test_a_fill_me_placeholder_is_never_a_candidate(tmp_home):
    wf = write(tmp_home, "unfinished",
               "---\nid: unfinished\ndescription: d\ninputs:\n  - id: f\n"
               "    tool: file.list\n    args:\n      path: <FOLDER>\n"
               "output:\n  target: stdout\n---\nDo it.\n")
    assert [c.literal for c in templates.candidates(wf)] == []


def test_an_existing_template_reference_is_never_a_candidate(tmp_home):
    wf = write(tmp_home, "already",
               "---\nid: already\ndescription: d\ninputs:\n  - id: f\n"
               "    tool: file.list\n    args:\n      path: '{{input.folder}}'\n"
               "output:\n  target: stdout\n---\nDo it.\n")
    assert [c.literal for c in templates.candidates(wf)] == []


def test_one_literal_in_two_places_is_one_candidate(digest):
    matches = [c for c in templates.candidates(digest)
               if c.literal == "/Users/someone/code/api"]
    assert len(matches) == 1, "a var is a value, not a location"
    assert "body" in matches[0].locations
    assert matches[0].occurrences == 2


def test_candidates_come_back_longest_first(digest):
    lengths = [len(c.literal) for c in templates.candidates(digest)]
    assert lengths == sorted(lengths, reverse=True), (
        "substitution happens in this order; a shorter literal inside a longer "
        "one must not go first")


# --- reading the model's answer -------------------------------------------

def _payload(wf):
    found = templates.candidates(wf)
    return templates.case(wf, found)


def test_a_literal_the_scan_never_offered_is_dropped(monkeypatch, digest):
    monkeypatch.setattr(templates.harness, "invoke", reply_with({
        "summary": "s",
        "vars": [{"literal": "some-other-repo", "name": "repo",
                  "description": "which repo"}],
    }))
    proposal = templates.propose({}, _payload(digest))
    assert proposal.vars == []
    assert proposal.dropped == ["some-other-repo"]


def test_a_var_with_no_description_is_dropped(monkeypatch, digest):
    monkeypatch.setattr(templates.harness, "invoke", reply_with({
        "summary": "s",
        "vars": [{"literal": "#eng-standup", "name": "channel", "description": "  "}],
    }))
    assert templates.propose({}, _payload(digest)).vars == []


def test_a_name_is_slugified_and_kept_unique(monkeypatch, digest):
    monkeypatch.setattr(templates.harness, "invoke", reply_with({
        "summary": "s",
        "vars": [
            {"literal": "#eng-standup", "name": "The Channel!", "description": "a"},
            {"literal": "octocat", "name": "the channel", "description": "b"},
        ],
    }))
    names = [v.name for v in templates.propose({}, _payload(digest)).vars]
    assert names[0] == "the_channel"
    assert names[1] == "the_channel_2"


def test_the_same_literal_proposed_twice_is_one_var(monkeypatch, digest):
    monkeypatch.setattr(templates.harness, "invoke", reply_with({
        "summary": "s",
        "vars": [
            {"literal": "#eng-standup", "name": "channel", "description": "a"},
            {"literal": "#eng-standup", "name": "room", "description": "b"},
        ],
    }))
    assert len(templates.propose({}, _payload(digest)).vars) == 1


def test_an_answer_that_is_not_json_is_refused(monkeypatch, digest):
    monkeypatch.setattr(templates.harness, "invoke", lambda *a, **k: "sure, here you go")
    with pytest.raises(templates.TemplateError):
        templates.propose({}, _payload(digest))


# --- rewriting the file ---------------------------------------------------

def _var(name, literal, **kw):
    return templates.TemplateVar(name=name, literal=literal,
                                 description=kw.pop("description", "what it is"), **kw)


def test_a_var_is_substituted_in_the_arguments_and_the_body(digest):
    text, counts = templates.apply(
        DIGEST, [_var("folder", "/Users/someone/code/api")])
    assert counts["folder"] == 2
    assert "/Users/someone/code/api" not in text
    assert "path: '{{input.folder}}'" in text
    assert "Summarize {{input.folder}} and post it" in text


def test_the_longest_literal_is_substituted_first(tmp_home):
    source = ("---\nid: nested\ndescription: d\ninputs:\n  - id: f\n"
              "    tool: file.list\n    args:\n      repo: acme/api\n"
              "      owner: acme\noutput:\n  target: stdout\n---\nRead acme/api.\n")
    write(tmp_home, "nested", source)
    text, counts = templates.apply(source, [_var("owner", "acme"),
                                            _var("repo", "acme/api")])
    assert "repo: '{{input.repo}}'" in text
    assert "owner: '{{input.owner}}'" in text
    assert "Read {{input.repo}}." in text


def test_a_var_that_replaced_nothing_is_not_declared(tmp_home):
    source = ("---\nid: solo\ndescription: d\ninputs:\n  - id: f\n"
              "    tool: file.list\n    args:\n      repo: acme/api\n"
              "output:\n  target: stdout\n---\nRead acme/api.\n")
    text, counts = templates.apply(source, [_var("repo", "acme/api"),
                                            _var("owner", "acme")])
    assert counts["owner"] == 0
    assert "name: owner" not in text, (
        "declaring it would fail validation, since nothing would reference it")


def test_the_rewrite_still_parses_and_validates(tmp_home, digest):
    text, _counts = templates.apply(DIGEST, [
        _var("folder", "/Users/someone/code/api",
             description="The folder to summarize", values=["~/code/api"]),
        _var("channel", "#eng-standup", description="Where the digest goes"),
    ])
    rewritten = workflow_mod.parse_text(text, digest.path)
    assert workflow_mod.validate(rewritten, tmp_home) == []
    assert [v["name"] for v in workflow_mod.declared_vars(rewritten)] == [
        "folder", "channel"]


def test_the_vars_block_sits_above_the_machinery(digest):
    text, _ = templates.apply(DIGEST, [_var("channel", "#eng-standup")])
    assert text.index("vars:") < text.index("inputs:"), (
        "the block whoever installs this has to read must not sit under the "
        "tool list")


def test_an_existing_vars_block_is_extended_not_replaced(tmp_home):
    source = ("---\nid: partial\ndescription: d\nvars:\n  - name: folder\n"
              "    description: The folder\ninputs:\n  - id: f\n"
              "    tool: file.list\n    args:\n      path: '{{input.folder}}'\n"
              "      author: octocat\noutput:\n  target: stdout\n---\nRead it.\n")
    text, _ = templates.apply(source, [_var("author", "octocat")])
    names = [v["name"] for v in
             workflow_mod.declared_vars(workflow_mod.parse_text(text, tmp_home / "x.md"))]
    assert names == ["folder", "author"]


def test_the_body_is_otherwise_untouched(digest):
    text, _ = templates.apply(DIGEST, [_var("channel", "#eng-standup")])
    body = text.split("---", 2)[2]
    assert body.startswith("\nSummarize /Users/someone/code/api")
    assert body.endswith(".\n")


def test_a_file_without_frontmatter_is_refused():
    with pytest.raises(templates.TemplateError):
        templates.apply("just a body\n", [_var("channel", "#eng-standup")])


def test_the_run_command_names_every_var_without_a_default():
    command = templates.example_command("digest", [
        _var("folder", "/x", values=["~/code/api"]),
        _var("channel", "#eng-standup", default="#general"),
    ])
    assert "--input folder=~/code/api" in command
    assert "channel" not in command, "a var with a default needs no flag"


def test_the_run_command_survives_being_pasted_into_a_shell():
    command = templates.example_command("digest", [
        _var("channel", "#eng-standup", values=["#eng-standup"]),
        _var("folder", "/x"),
    ])
    assert "'channel=#eng-standup'" in command, (
        "unquoted, the shell reads # as the start of a comment and the flag "
        "vanishes from the command this line exists to demonstrate")
    assert "folder=VALUE" in command, "angle brackets would be a redirect"


def test_a_value_the_shell_would_not_mangle_is_left_bare():
    command = templates.example_command(
        "digest", [_var("folder", "/x", values=["~/code/api"])])
    assert "--input folder=~/code/api" in command, (
        "quoting this one would stop the shell expanding it to a real directory")


# --- what the file format now refuses -------------------------------------

def _errors(tmp_home, wid, text):
    return workflow_mod.validate(write(tmp_home, wid, text), tmp_home)


def test_a_var_needs_a_description(tmp_home):
    errors = _errors(tmp_home, "nodesc",
                     "---\nid: nodesc\ndescription: d\nvars:\n  - name: repo\n"
                     "output:\n  target: stdout\n---\nRead {{input.repo}}.\n")
    assert any("no description" in e for e in errors)


def test_a_var_nothing_references_is_refused(tmp_home):
    errors = _errors(tmp_home, "unused",
                     "---\nid: unused\ndescription: d\nvars:\n  - name: repo\n"
                     "    description: which repo\noutput:\n  target: stdout\n---\nRead it.\n")
    assert any("nothing in this workflow references" in e for e in errors)


def test_a_var_referenced_only_in_the_description_does_not_count(tmp_home):
    errors = _errors(tmp_home, "described",
                     "---\nid: described\ndescription: covers {{input.repo}}\n"
                     "vars:\n  - name: repo\n    description: which repo\n"
                     "output:\n  target: stdout\n---\nRead it.\n")
    assert any("nothing in this workflow references" in e for e in errors), (
        "only what a run renders counts as a reference")


def test_a_duplicate_var_is_refused(tmp_home):
    errors = _errors(tmp_home, "dupe",
                     "---\nid: dupe\ndescription: d\nvars:\n  - name: repo\n"
                     "    description: a\n  - name: repo\n    description: b\n"
                     "output:\n  target: stdout\n---\nRead {{input.repo}}.\n")
    assert any("twice" in e for e in errors)


def test_a_name_that_could_never_resolve_is_refused(tmp_home):
    errors = _errors(tmp_home, "dotted",
                     "---\nid: dotted\ndescription: d\nvars:\n  - name: a.b\n"
                     "    description: a\noutput:\n  target: stdout\n---\nRead it.\n")
    assert any("must start with a letter" in e for e in errors)


def _scheduled(extra: str = "") -> str:
    return ("---\nid: nightly\ndescription: d\ntrigger:\n  schedule: '0 9 * * 5'\n"
            "vars:\n  - name: repo\n    description: which repo\n" + extra +
            "output:\n  target: file\n  path: output/x.md\n---\n"
            "Read {{input.repo}}.\n")


def test_a_scheduled_workflow_cannot_require_a_var(tmp_home):
    errors = _errors(tmp_home, "nightly", _scheduled())
    assert any("nothing can pass it --input" in e for e in errors), (
        "it would not run badly, it would fail every fire, unattended")


def test_a_scheduled_workflow_whose_vars_have_defaults_is_fine(tmp_home):
    errors = _errors(tmp_home, "nightly", _scheduled("    default: acme/api\n"))
    assert errors == []


# --- what a run does with them -------------------------------------------

TEMPLATE = """\
---
id: filled
description: d
vars:
  - name: folder
    description: The folder to summarize
  - name: label
    description: What to call it
    default: weekly
inputs:
  - id: files
    tool: file.list
    args:
      path: '{{input.folder}}'
output:
  target: stdout
---
Summarize {{input.folder}} as the {{input.label}} digest.
"""


def test_a_default_fills_in_and_a_supplied_value_wins(tmp_home):
    wf = write(tmp_home, "filled", TEMPLATE)
    filled, missing = workflow_mod.var_values(wf, {"folder": "/tmp"})
    assert filled == {"label": "weekly"}
    assert missing == []
    filled, _ = workflow_mod.var_values(wf, {"folder": "/tmp", "label": "daily"})
    assert "label" not in filled, "a supplied value is never overwritten by a default"


def test_an_empty_value_counts_as_missing(tmp_home):
    wf = write(tmp_home, "filled", TEMPLATE)
    _filled, missing = workflow_mod.var_values(wf, {"folder": "   "})
    assert missing == ["folder"], (
        "nothing useful is ever named by the empty string")


def test_a_missing_var_stops_the_run_before_any_tool_is_called(monkeypatch, tmp_home):
    wf = write(tmp_home, "filled", TEMPLATE)
    called = []
    monkeypatch.setattr(tools_mod, "call",
                        lambda *a, **k: called.append(a) or {"items": []})

    with pytest.raises(runner_mod.RunError) as exc:
        runner_mod.resolve_inputs(tmp_home, {}, wf, {})

    assert called == [], "the refusal has to come before the network does"
    assert "folder" in str(exc.value)
    assert "--input folder=" in str(exc.value), "say how to fix it"


def test_a_filled_template_resolves_its_inputs(monkeypatch, tmp_home):
    wf = write(tmp_home, "filled", TEMPLATE)
    seen = {}

    def fake_call(home, config, tool_id, args):
        seen.update(args)
        return {"items": ["a.md"]}

    monkeypatch.setattr(tools_mod, "call", fake_call)
    context, meta = runner_mod.resolve_inputs(tmp_home, {}, wf, {"folder": "/tmp/x"})

    assert seen["path"] == "/tmp/x"
    assert meta[0]["ok"]
    assert context["input"]["label"] == "weekly", "the default reaches the prompt too"


def test_the_body_is_rendered_with_the_vars(tmp_home):
    wf = write(tmp_home, "filled", TEMPLATE)
    prompt = runner_mod.render_prompt(
        wf, {}, {"config": {}, "input": {"folder": "/tmp/x", "label": "weekly"}})
    assert "Summarize /tmp/x as the weekly digest." in prompt


# --- the command ---------------------------------------------------------

class _Args:
    def __init__(self, workflow="digest", **kw):
        self.workflow = workflow
        self.to = kw.get("to")
        self.candidates = kw.get("candidates", False)
        self.dry_run = kw.get("dry_run", False)
        self.yes = kw.get("yes", True)
        self.json = kw.get("json", False)


ACCEPTED = {
    "summary": "Summarize a folder into a channel.",
    "vars": [
        {"literal": "/Users/someone/code/api", "name": "folder",
         "description": "The folder to summarize", "values": ["~/code/api"]},
        {"literal": "#eng-standup", "name": "channel",
         "description": "Where the digest goes", "values": ["#team"]},
    ],
    "skip": [],
}


def test_candidates_prints_the_scan_and_calls_no_model(monkeypatch, tmp_home, digest,
                                                      capsys):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    # harness.invoke is refused by the autouse fixture, so reaching it fails here.
    cli.cmd_workflows_templatize(_Args(candidates=True))
    out = capsys.readouterr().out
    assert "#eng-standup" in out
    assert "vars:" not in digest.path.read_text()


def test_templatizing_rewrites_the_same_file(monkeypatch, tmp_home, digest,
                                             quiet_spinner, capsys):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(templates.harness, "invoke", reply_with(ACCEPTED))

    cli.cmd_workflows_templatize(_Args())

    text = digest.path.read_text()
    assert "name: folder" in text
    assert "{{input.folder}}" in text
    assert "#eng-standup" not in text


def test_to_leaves_the_original_alone(monkeypatch, tmp_home, digest, quiet_spinner):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(templates.harness, "invoke", reply_with(ACCEPTED))

    cli.cmd_workflows_templatize(_Args(to="digest-template"))

    assert digest.path.read_text() == DIGEST, "the working workflow is untouched"
    shared = (paths.workflows_dir(tmp_home) / "digest-template.md").read_text()
    assert "id: digest-template" in shared
    assert "{{input.channel}}" in shared


def test_a_dry_run_writes_nothing(monkeypatch, tmp_home, digest, quiet_spinner):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(templates.harness, "invoke", reply_with(ACCEPTED))

    cli.cmd_workflows_templatize(_Args(dry_run=True))

    assert digest.path.read_text() == DIGEST


def test_nothing_is_written_when_the_rewrite_would_not_validate(monkeypatch, tmp_home,
                                                                quiet_spinner, capsys):
    # A scheduled workflow plus a required var is the one combination that can
    # never run, so the write has to be refused rather than left to fail at 6am.
    scheduled = ("---\nid: nightly\nkind: workflow\ndescription: d\n"
                 "trigger:\n  schedule: '0 9 * * 5'\ninputs:\n  - id: f\n"
                 "    tool: file.list\n    args:\n      path: /Users/someone/code/api\n"
                 "output:\n  target: file\n  path: output/x.md\n---\nRead it.\n")
    wf = write(tmp_home, "nightly", scheduled)
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(templates.harness, "invoke", reply_with({
        "summary": "s",
        "vars": [{"literal": "/Users/someone/code/api", "name": "folder",
                  "description": "The folder"}],
    }))

    with pytest.raises(SystemExit) as exc:
        cli.cmd_workflows_templatize(_Args("nightly"))

    assert exc.value.code == cli.EXIT_USER_ERROR
    assert wf.path.read_text() == scheduled
    assert "nothing can pass it --input" in capsys.readouterr().out


def test_an_error_the_workflow_already_had_does_not_block_it(monkeypatch, tmp_home,
                                                             quiet_spinner):
    # The tool does not exist, so this workflow is already invalid -- and that
    # is not a reason to refuse the one thing the user asked for.
    broken = ("---\nid: broken\nkind: workflow\ndescription: d\ninputs:\n  - id: f\n"
              "    tool: nosuch.tool\n    args:\n      path: /Users/someone/code/api\n"
              "output:\n  target: stdout\n---\nRead it.\n")
    wf = write(tmp_home, "broken", broken)
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(templates.harness, "invoke", reply_with({
        "summary": "s",
        "vars": [{"literal": "/Users/someone/code/api", "name": "folder",
                  "description": "The folder"}],
    }))

    cli.cmd_workflows_templatize(_Args("broken"))

    assert "{{input.folder}}" in wf.path.read_text()


def test_a_workflow_with_nothing_to_lift_out_says_so(monkeypatch, tmp_home, capsys):
    write(tmp_home, "plain",
          "---\nid: plain\ndescription: d\noutput:\n  target: stdout\n---\nWrite it.\n")
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))

    cli.cmd_workflows_templatize(_Args("plain"))

    assert "nothing to templatize" in capsys.readouterr().out


def test_the_run_command_names_vars_the_file_already_had(monkeypatch, tmp_home,
                                                        quiet_spinner, capsys):
    source = ("---\nid: partial\nkind: workflow\ndescription: d\nvars:\n"
              "  - name: folder\n    description: The folder\ninputs:\n  - id: f\n"
              "    tool: file.list\n    args:\n      path: '{{input.folder}}'\n"
              "output:\n  target: stdout\n---\nRead it, then say so in #eng-standup.\n")
    write(tmp_home, "partial", source)
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(templates.harness, "invoke", reply_with({
        "summary": "s",
        "vars": [{"literal": "#eng-standup", "name": "channel",
                  "description": "Where it goes"}],
    }))

    cli.cmd_workflows_templatize(_Args("partial"))

    out = capsys.readouterr().out
    assert "--input folder=" in out, (
        "the command has to name every value the file now needs, not only the "
        "ones this pass found")
    assert "--input channel=" in out


def test_the_json_report_names_the_run_command(monkeypatch, tmp_home, digest,
                                               quiet_spinner, capsys):
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(templates.harness, "invoke", reply_with(ACCEPTED))

    cli.cmd_workflows_templatize(_Args(json=True))

    report = json.loads(capsys.readouterr().out)
    assert report["applied"] is False, "the write is the interactive path"
    assert "--input folder=" in report["run_command"]
    assert digest.path.read_text() == DIGEST


# --- what will not offer you a template ----------------------------------

def test_a_template_is_not_offered_to_the_router(tmp_home, digest):
    from px0 import route

    text, _ = templates.apply(DIGEST, [
        _var("folder", "/Users/someone/code/api", description="The folder")])
    digest.path.write_text(text)

    offered = {w["id"] for w in route.candidates(tmp_home, {})["workflows"]}
    assert "digest" not in offered, (
        "the router cannot supply a var, so offering one can only end in a refusal")


def test_a_template_whose_vars_all_have_defaults_is_still_offered(tmp_home, digest):
    from px0 import route

    text, _ = templates.apply(DIGEST, [
        _var("folder", "/Users/someone/code/api", description="The folder",
             default="~/code")])
    digest.path.write_text(text)

    offered = {w["id"] for w in route.candidates(tmp_home, {})["workflows"]}
    assert "digest" in offered
