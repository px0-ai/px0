import json

import pytest

from px0 import builder as builder_mod, catalogue, cli, harness, tools


# --- catalogue: turning Composio's API shape into px0's ---------------------

def _api_item(slug, tags, toolkit="slack", required=("channel",), props=("channel", "text")):
    return {
        "slug": slug,
        "name": slug.title(),
        "description": f"does {slug}",
        "toolkit": {"slug": toolkit},
        "tags": list(tags),
        "input_parameters": {
            "required": list(required),
            "properties": {p: {"type": "string"} for p in props},
        },
    }


def test_read_write_and_destructive_come_from_composios_tags():
    """px0 never guesses access from the tool's name -- Composio states it."""
    read = catalogue._from_api(_api_item("X_LIST", ["important", "readOnlyHint"]))
    write = catalogue._from_api(_api_item("X_SEND", ["important"]))
    destructive = catalogue._from_api(_api_item("X_DELETE", ["destructiveHint"]))

    assert read.is_write is False and read.is_destructive is False
    assert write.is_write is True and write.is_destructive is False
    assert destructive.is_write is True and destructive.is_destructive is True


def test_missing_tags_are_treated_as_write():
    """Unknown access must fail safe: px0 gates writes behind consent."""
    assert catalogue._from_api(_api_item("X_MYSTERY", [])).is_write is True


def test_params_put_required_fields_first_and_mark_them():
    tool = catalogue._from_api(
        _api_item("X_SEND", [], required=("channel",), props=("text", "channel", "as_user"))
    )
    # required first, then the optional ones alphabetically -- deterministic, so a
    # generated `args` block leads with what the tool actually needs
    assert list(tool.params) == ["channel", "as_user", "text"]
    assert tool.params["channel"].endswith("*")      # required
    assert not tool.params["text"].endswith("*")


def test_tool_ids_are_namespaced_and_reversible():
    tool = catalogue._from_api(_api_item("SLACK_SEND_MESSAGE", []))
    assert tool.id == "composio:SLACK_SEND_MESSAGE"
    assert catalogue.is_catalogue_id(tool.id)
    assert catalogue.slug_of(tool.id) == "SLACK_SEND_MESSAGE"
    assert not catalogue.is_catalogue_id("slack.post_message")


def test_cache_round_trips_and_merges(tmp_home):
    first = catalogue._from_api(_api_item("A_ONE", ["readOnlyHint"]))
    second = catalogue._from_api(_api_item("B_TWO", []))

    catalogue.remember(tmp_home, [first])
    catalogue.remember(tmp_home, [second])
    cached = catalogue.load_cached(tmp_home)

    assert set(cached) == {first.id, second.id}
    assert cached[first.id].is_write is False
    assert cached[second.id].params == second.params


def test_corrupt_cache_reads_as_empty(tmp_home):
    """A bad cache degrades into unknown-tool errors, never a crash."""
    path = catalogue.cache_path(tmp_home)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("{not json")
    assert catalogue.load_cached(tmp_home) == {}


def test_cache_ignores_entries_from_a_newer_px0(tmp_home):
    path = catalogue.cache_path(tmp_home)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"tools": [
        {"slug": "OK", "toolkit": "t", "name": "n", "description": "d", "is_write": False},
        {"slug": "FUTURE", "toolkit": "t", "name": "n", "description": "d",
         "is_write": False, "unknown_field": 1},
    ]}))
    assert list(catalogue.load_cached(tmp_home)) == ["composio:OK"]


# --- discovered tools become usable tools ----------------------------------

def test_discovered_tools_resolve_execute_and_report_access(tmp_home, monkeypatch):
    tool = catalogue._from_api(_api_item("SLACK_SEND_MESSAGE", []))
    catalogue.remember(tmp_home, [tool])

    assert tools.exists(tool.id, tmp_home)
    assert tools.is_write(tool.id, tmp_home) is True
    assert tool.id in [t.id for t in tools.list_tools(home=tmp_home)]
    assert tool.id not in [t.id for t in tools.list_tools()]  # not without a store

    seen = {}
    monkeypatch.setattr(tools, "_composio_execute",
                        lambda ctx, app, slug, args: seen.update(app=app, slug=slug, args=args))
    tools.call(tmp_home, {}, tool.id, {"channel": "#dev"})

    assert seen == {"app": "slack", "slug": "SLACK_SEND_MESSAGE", "args": {"channel": "#dev"}}


def test_unknown_discovered_tool_is_an_error_not_a_crash(tmp_home):
    assert tools.exists("composio:NOPE", tmp_home) is False
    with pytest.raises(tools.ConnectorError, match="no such tool"):
        tools.call(tmp_home, {}, "composio:NOPE", {})


def test_workflow_validation_accepts_discovered_tools(tmp_home):
    from px0 import paths, workflow as workflow_mod

    read = catalogue._from_api(_api_item("X_LIST", ["readOnlyHint"]))
    write = catalogue._from_api(_api_item("X_SEND", []))
    catalogue.remember(tmp_home, [read, write])

    path = paths.workflows_dir(tmp_home) / "w.md"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        "---\nid: w\ninputs:\n  - id: a\n    tool: composio:X_LIST\n"
        "tools: [composio:X_SEND]\noutput: {target: stdout}\n---\nbody\n"
    )
    assert workflow_mod.validate(workflow_mod.parse(path), tmp_home) == []


def test_workflow_validation_still_rejects_a_write_tool_as_an_input(tmp_home):
    """Inputs run before the prompt unconditionally, so they must be read-only."""
    from px0 import paths, workflow as workflow_mod

    catalogue.remember(tmp_home, [catalogue._from_api(_api_item("X_SEND", []))])
    path = paths.workflows_dir(tmp_home) / "w.md"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        "---\nid: w\ninputs:\n  - id: a\n    tool: composio:X_SEND\n"
        "output: {target: stdout}\n---\nbody\n"
    )
    errors = workflow_mod.validate(workflow_mod.parse(path), tmp_home)
    assert any("write tool" in e for e in errors)


# --- the builder passes ----------------------------------------------------

def _harness_returning(*replies):
    """Feeds the harness's replies in order, one per invoke()."""
    queue = list(replies)
    return lambda config, prompt, timeout=None: queue.pop(0)


def test_clarify_returns_questions_then_nothing(monkeypatch):
    monkeypatch.setattr(harness, "invoke", _harness_returning(
        'Sure: ["Which channel?", "How often?"]',
        "Nothing to ask: []",
    ))
    assert builder_mod.clarify({}, "post to slack", []) == ["Which channel?", "How often?"]
    assert builder_mod.clarify({}, "post to slack", [("Which channel?", "#eng")]) == []


def test_clarify_caps_at_three_questions(monkeypatch):
    monkeypatch.setattr(harness, "invoke",
                        _harness_returning('["a","b","c","d","e"]'))
    assert len(builder_mod.clarify({}, "x", [])) == 3


def test_answers_are_carried_into_later_prompts(monkeypatch):
    """Every pass sees the clarifications, so nothing re-guesses what was asked."""
    prompts = []

    def record(config, prompt, timeout=None):
        prompts.append(prompt)
        return "[]"

    monkeypatch.setattr(harness, "invoke", record)
    builder_mod.propose_queries({}, "post to slack", [("Which channel?", "#eng")])

    assert "Which channel?" in prompts[0]
    assert "#eng" in prompts[0]


def test_propose_queries_returns_toolkit_scoped_searches(monkeypatch):
    monkeypatch.setattr(harness, "invoke", _harness_returning(
        '[{"toolkit": "GitHub", "capability": "list pull requests"},'
        ' {"toolkit": null, "capability": "summarize"},'
        ' "send message"]'
    ))
    queries = builder_mod.propose_queries({}, "x", [])

    assert queries[0] == {"toolkit": "github", "capability": "list pull requests"}
    assert queries[1] == {"toolkit": None, "capability": "summarize"}
    assert queries[2] == {"toolkit": None, "capability": "send message"}  # bare string tolerated


def test_propose_queries_drops_entries_with_no_capability(monkeypatch):
    monkeypatch.setattr(harness, "invoke", _harness_returning(
        '[{"toolkit": "slack"}, {"capability": "  "}, {"capability": "send message"}]'
    ))
    assert builder_mod.propose_queries({}, "x", []) == [
        {"toolkit": None, "capability": "send message"}
    ]


def test_malformed_harness_reply_is_a_builder_error(monkeypatch):
    monkeypatch.setattr(harness, "invoke", _harness_returning("I cannot help with that"))
    with pytest.raises(builder_mod.BuilderError, match="did not return a JSON array"):
        builder_mod.clarify({}, "x", [])


def test_search_candidates_scopes_by_toolkit_and_dedupes(tmp_home, monkeypatch):
    calls = []

    def fake_search(home, query, limit=None, toolkit=None):
        calls.append((query, toolkit))
        return [catalogue._from_api(_api_item("A_ONE", [])),
                catalogue._from_api(_api_item("B_TWO", []))]

    monkeypatch.setattr(catalogue, "search", fake_search)
    found = builder_mod.search_candidates(tmp_home, [
        {"toolkit": "slack", "capability": "send message"},
        {"toolkit": "slack", "capability": "post message"},
    ])

    assert calls == [("send message", "slack"), ("post message", "slack")]
    assert [t.slug for t in found] == ["A_ONE", "B_TWO"]  # deduped across queries


def test_search_candidates_retries_without_the_toolkit_when_it_matches_nothing(
        tmp_home, monkeypatch):
    """The model can invent a toolkit slug; an empty scoped search shouldn't end it."""
    calls = []

    def fake_search(home, query, limit=None, toolkit=None):
        calls.append((query, toolkit))
        return [] if toolkit else [catalogue._from_api(_api_item("A_ONE", []))]

    monkeypatch.setattr(catalogue, "search", fake_search)
    found = builder_mod.search_candidates(
        tmp_home, [{"toolkit": "slackk", "capability": "send message"}])

    assert calls == [("send message", "slackk"), ("slackk send message", None)]
    assert [t.slug for t in found] == ["A_ONE"]


def test_select_tools_keeps_only_real_candidates(monkeypatch):
    """A hallucinated slug would fail validation later; drop it here."""
    candidates = [catalogue._from_api(_api_item("A_ONE", ["readOnlyHint"])),
                  catalogue._from_api(_api_item("B_TWO", []))]
    monkeypatch.setattr(harness, "invoke",
                        _harness_returning('["A_ONE", "MADE_UP_SLUG"]'))

    chosen = builder_mod.select_tools({}, "x", [], candidates)
    assert [t.slug for t in chosen] == ["A_ONE"]


def test_select_tools_is_skipped_entirely_without_candidates(monkeypatch):
    monkeypatch.setattr(harness, "invoke",
                        lambda *a, **kw: pytest.fail("must not ask the model"))
    assert builder_mod.select_tools({}, "x", [], []) == []


def test_selection_prompt_states_the_access_of_each_candidate(monkeypatch):
    """The model can only avoid unrequested writes if it's told which they are."""
    prompts = []
    monkeypatch.setattr(harness, "invoke",
                        lambda c, p, timeout=None: (prompts.append(p), "[]")[1])
    builder_mod.select_tools({}, "x", [], [
        catalogue._from_api(_api_item("A_ONE", ["readOnlyHint"])),
        catalogue._from_api(_api_item("B_TWO", [])),
        catalogue._from_api(_api_item("C_DEL", ["destructiveHint"])),
    ])

    assert "A_ONE [read]" in prompts[0]
    assert "B_TWO [write]" in prompts[0]
    assert "C_DEL [DESTRUCTIVE]" in prompts[0]


def test_generate_plan_restricts_itself_to_the_selected_tools(monkeypatch):
    prompts = []

    def record(config, prompt, timeout=None):
        prompts.append(prompt)
        return json.dumps({"trigger": {"manual": True}, "inputs": [],
                           "tools": ["composio:B_TWO"], "output": {"target": "stdout"},
                           "body": "b", "description": "d"})

    monkeypatch.setattr(harness, "invoke", record)
    selected = [catalogue._from_api(_api_item("B_TWO", []))]
    plan = builder_mod.generate_plan({}, "x", [], selected)

    assert "Use ONLY these tools" in prompts[0]
    assert "composio:B_TWO" in prompts[0]
    assert plan.tools == ["composio:B_TWO"]


def test_generate_plan_falls_back_to_curated_tools(monkeypatch):
    prompts = []
    monkeypatch.setattr(harness, "invoke", lambda c, p, timeout=None: (
        prompts.append(p), json.dumps({"body": "b", "description": "d"}))[1])

    builder_mod.generate_plan({}, "x", [], [])
    assert "Available tools:" in prompts[0]
    assert "slack.post_message" in prompts[0]


def test_plan_helpers_understand_discovered_tools(tmp_home):
    read = catalogue._from_api(_api_item("X_LIST", ["readOnlyHint"], toolkit="github"))
    write = catalogue._from_api(_api_item("X_SEND", [], toolkit="slack"))
    catalogue.remember(tmp_home, [read, write])

    plan = builder_mod.Plan(
        trigger={}, inputs=[{"id": "a", "tool": read.id}], tools=[write.id],
        output={}, body="b", description="d",
    )

    assert builder_mod.required_connections(plan, tmp_home) == {"github", "slack"}
    assert builder_mod.write_tools_named(plan, tmp_home) == [write.id]
    assert builder_mod.check_feasibility(plan, tmp_home) == []


def test_feasibility_flags_a_tool_that_was_never_discovered(tmp_home):
    plan = builder_mod.Plan(trigger={}, inputs=[], tools=["composio:NEVER_SEEN"],
                            output={}, body="b", description="d")
    issues = builder_mod.check_feasibility(plan, tmp_home)
    assert any("NEVER_SEEN" in i for i in issues)


# --- guideline relevance ---------------------------------------------------

@pytest.mark.parametrize("description,expected", [
    ("draft a commit message from my staged diff", {"commit-messages.md"}),
    ("write a pull request description from the branch diff", {"pr-descriptions.md"}),
    ("summarize every blog post I saved this week", {"summarization.md"}),
    ("review my go code changes for style violations", {"code-review/go.md"}),
    ("check my python code for review issues", {"code-review/python.md"}),
])
def test_guidelines_match_the_task(tmp_home, description, expected):
    from px0 import paths
    g_dir = paths.guidelines_dir(tmp_home)
    (g_dir / "commit-messages.md").write_text("## Imperative mood summary line\n\nCommit messages body.\n")
    (g_dir / "pr-descriptions.md").write_text("## Lead with the problem\n\nPR descriptions.\n")
    (g_dir / "summarization.md").write_text("## Lead with the takeaway\n\nSummarization body.\n")
    (g_dir / "code-review").mkdir(parents=True, exist_ok=True)
    (g_dir / "code-review" / "go.md").write_text("## Wrap errors with %w\n\nGo code review.\n")
    (g_dir / "code-review" / "python.md").write_text("## Type hints on public functions\n\nPython code review.\n")
    assert set(builder_mod.choose_guidelines(tmp_home, description)) == expected


@pytest.mark.parametrize("description", [
    "post a daily haiku about the weather to slack",
    "email me the weather forecast each morning",
])
def test_unrelated_tasks_get_no_guidelines(tmp_home, description):
    """Every attached guideline is inlined verbatim into the prompt, so a wrong
    one costs tokens and misleads the model. None is better."""
    assert builder_mod.choose_guidelines(tmp_home, description) == []


def test_work_folder_guidelines_are_never_auto_attached(tmp_home):
    from px0 import paths

    work = paths.guidelines_dir(tmp_home) / "work"
    work.mkdir(parents=True, exist_ok=True)
    (work / "commit-messages.md").write_text("## Commit messages\ncommit message rules\n")

    chosen = builder_mod.choose_guidelines(tmp_home, "draft a commit message")
    assert all(not c.startswith("work/") for c in chosen)


# --- the interactive flow --------------------------------------------------

def test_clarify_loop_asks_records_and_stops(monkeypatch, capsys):
    rounds = [["Which channel?", "How often?"], []]
    monkeypatch.setattr(builder_mod, "clarify", lambda c, d, qa: rounds.pop(0))
    answers = iter(["#eng", ""])
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: next(answers))

    qa = cli._clarify_loop({}, "post to slack", skip=False)

    assert qa == [("Which channel?", "#eng")]     # the skipped one is not recorded
    assert "skipped" in capsys.readouterr().out


def test_clarify_loop_stops_when_nothing_is_answered(monkeypatch):
    """Pressing Enter through the questions must not loop forever."""
    calls = []

    def clarify(config, description, qa):
        calls.append(1)
        return ["Q?"]

    monkeypatch.setattr(builder_mod, "clarify", clarify)
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "")

    assert cli._clarify_loop({}, "x", skip=False) == []
    assert len(calls) == 1


def test_clarify_loop_is_bounded(monkeypatch):
    monkeypatch.setattr(builder_mod, "clarify", lambda c, d, qa: ["Q?"])
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "an answer")

    qa = cli._clarify_loop({}, "x", skip=False)
    assert len(qa) == builder_mod.MAX_CLARIFY_ROUNDS


def test_clarify_loop_skipped_asks_nothing(monkeypatch):
    monkeypatch.setattr(builder_mod, "clarify",
                        lambda *a: pytest.fail("must not ask the model"))
    assert cli._clarify_loop({}, "x", skip=True) == []


# --- intake: `px0 workflows new` with nothing to go on ---------------------

def test_intake_asks_while_something_is_missing(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke",
                        lambda *a, **k: '{"question": "Which repository?"}')

    assert builder_mod.intake({}, [("What do you want?", "digest my PRs")]) == {
        "question": "Which repository?"}


def test_intake_writes_the_request_once_it_has_enough(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k:
                        '{"description": "Every Friday, digest merged PRs in '
                        'razorpay/api and post them to #eng."}')

    step = builder_mod.intake({}, [("q", "a")])

    assert step["description"].startswith("Every Friday")
    assert "question" not in step


def test_a_wrap_up_turn_will_not_accept_another_question(monkeypatch):
    """The user has stopped answering; asking again is not an option."""
    monkeypatch.setattr(builder_mod.harness, "invoke",
                        lambda *a, **k: '{"question": "One more thing?"}')

    with pytest.raises(builder_mod.BuilderError, match="neither a question nor a request"):
        builder_mod.intake({}, [("q", "a")], wrap_up=True)


def test_an_unusable_intake_answer_is_an_error_not_an_empty_request(monkeypatch):
    monkeypatch.setattr(builder_mod.harness, "invoke", lambda *a, **k: "sorry, no")

    with pytest.raises(builder_mod.BuilderError):
        builder_mod.intake({}, [("q", "a")])


def _recording_harness(monkeypatch, reply):
    """Patches harness.invoke to return `reply` and hand back the prompt it saw."""
    seen = {}

    def invoke(config, prompt, **kw):
        seen["p"] = prompt
        return reply

    monkeypatch.setattr(builder_mod.harness, "invoke", invoke)
    return seen


def test_the_interview_is_driven_by_what_a_workflow_file_needs(monkeypatch):
    """The checklist is the spec, so the questions are the plan's own fields."""
    seen = _recording_harness(monkeypatch, '{"question": "q"}')

    builder_mod.intake({}, [("What do you want px0 to do?", "watch my inbox")])

    assert builder_mod.WORKFLOW_SPEC in seen["p"]
    assert "watch my inbox" in seen["p"], "the transcript has to reach the model"


def test_clarify_drives_at_the_same_checklist(monkeypatch):
    """Two definitions of "what is missing" would ask two different interviews."""
    seen = _recording_harness(monkeypatch, "[]")

    builder_mod.clarify({}, "digest my PRs", [])

    assert builder_mod.WORKFLOW_SPEC in seen["p"]


def test_intake_loop_asks_until_the_request_is_written(monkeypatch, capsys):
    steps = [{"question": "Which repository?"},
             {"question": "How often?"},
             {"description": "Digest razorpay/api PRs every Friday."}]
    seen_transcripts = []

    def fake_intake(config, transcript, wrap_up=False):
        seen_transcripts.append(list(transcript))
        return steps.pop(0)

    monkeypatch.setattr(builder_mod, "intake", fake_intake)
    answers = iter(["digest my PRs", "razorpay/api", "every Friday", ""])
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: next(answers))

    description = cli._intake_loop({})

    assert description == "Digest razorpay/api PRs every Friday."
    # every answer is carried forward, so a later question can build on it
    assert seen_transcripts[-1] == [
        ("What do you want px0 to do for you?", "digest my PRs"),
        ("Which repository?", "razorpay/api"),
        ("How often?", "every Friday"),
    ]


def test_a_blank_answer_wraps_the_interview_up_rather_than_asking_on(monkeypatch):
    """Enter is the way out of any interview px0 puts up."""
    calls = []

    def fake_intake(config, transcript, wrap_up=False):
        calls.append(wrap_up)
        if wrap_up:
            return {"description": "what there was"}
        return {"question": "Which repository?"}

    monkeypatch.setattr(builder_mod, "intake", fake_intake)
    # the job, then Enter on the follow-up, then Enter to accept the write-up
    answers = iter(["digest my PRs", "", ""])
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: next(answers))

    assert cli._intake_loop({}) == "what there was"
    assert calls == [False, True], calls


def test_a_blank_first_answer_builds_nothing(monkeypatch):
    monkeypatch.setattr(builder_mod, "intake",
                        lambda *a, **k: pytest.fail("nothing to work from"))
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "")

    with pytest.raises(SystemExit):
        cli._intake_loop({})


def test_the_interview_is_bounded(monkeypatch):
    """A model that never stops asking must not hold the user forever."""
    rounds = []

    def fake_intake(config, transcript, wrap_up=False):
        rounds.append(wrap_up)
        return {"description": "settled"} if wrap_up else {"question": "another?"}

    monkeypatch.setattr(builder_mod, "intake", fake_intake)
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "an answer")

    assert cli._intake_loop({}) == "settled"
    assert rounds.count(False) == builder_mod.MAX_INTAKE_ROUNDS
    assert rounds[-1] is True, "the last turn writes up what there is"


def test_the_request_can_be_rewritten_before_anything_is_built(monkeypatch):
    monkeypatch.setattr(builder_mod, "intake",
                        lambda *a, **k: {"description": "not quite right"})
    answers = iter(["digest my PRs", "edit", "only my own PRs", ""])
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: next(answers))
    seen = {}
    monkeypatch.setattr(builder_mod, "revise_request",
                        lambda config, description, note, transcript=None: seen.update(
                            description=description, note=note) or "exactly right")

    assert cli._intake_loop({}) == "exactly right"
    assert seen == {"description": "not quite right", "note": "only my own PRs"}


def test_a_blank_edit_note_keeps_the_request_and_asks_again(monkeypatch):
    monkeypatch.setattr(builder_mod, "intake",
                        lambda *a, **k: {"description": "not quite right"})
    answers = iter(["digest my PRs", "edit", "", ""])
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: next(answers))
    monkeypatch.setattr(builder_mod, "revise_request",
                        lambda *a, **k: pytest.fail("must not revise on a blank note"))

    assert cli._intake_loop({}) == "not quite right"


def test_a_failed_revision_keeps_the_request_and_asks_again(monkeypatch, capsys):
    monkeypatch.setattr(builder_mod, "intake",
                        lambda *a, **k: {"description": "not quite right"})
    answers = iter(["digest my PRs", "edit", "only my own PRs", ""])
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: next(answers))

    def fail(*a, **k):
        raise builder_mod.BuilderError("the harness is unreachable")
    monkeypatch.setattr(builder_mod, "revise_request", fail)

    assert cli._intake_loop({}) == "not quite right"
    assert "could not revise" in capsys.readouterr().err


def test_declining_the_request_builds_nothing(monkeypatch):
    monkeypatch.setattr(builder_mod, "intake", lambda *a, **k: {"description": "d"})
    answers = iter(["digest my PRs", "n"])
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: next(answers))

    with pytest.raises(SystemExit) as e:
        cli._intake_loop({})
    assert e.value.code == 0, "cancelling is not a failure"


class _NewArgs:
    """A stand-in for the argparse namespace `cmd_new` receives -- empty, since
    `workflows new` takes no flags."""

    def __init__(self, **kw):
        self.__dict__.update(kw)


def _record_build(monkeypatch, seen):
    monkeypatch.setattr(cli, "_build_workflow",
                        lambda home, config, desc, args, existing_id=None, **kw:
                        seen.update(desc=desc, kw=kw))


def test_new_always_interviews_and_does_not_ask_twice(monkeypatch, tmp_home):
    """The intake settles exactly what clarify asks, so clarify must be skipped."""
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(cli, "_intake_loop", lambda config: "the settled request")
    monkeypatch.setattr("sys.stdin.isatty", lambda: True)
    seen = {}
    _record_build(monkeypatch, seen)

    cli.cmd_new(_NewArgs())

    assert seen["desc"] == "the settled request"
    assert seen["kw"]["already_clarified"] is True


def test_no_terminal_to_interview_on_is_a_user_error(monkeypatch, tmp_home):
    """A pipe has no keystrokes to read, and nobody to ask on the other end."""
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr("sys.stdin.isatty", lambda: False)
    monkeypatch.setattr(cli, "_intake_loop",
                        lambda config: pytest.fail("nobody to interview"))
    monkeypatch.setattr(cli, "_build_workflow",
                        lambda *a, **k: pytest.fail("nothing to build from"))

    with pytest.raises(SystemExit) as exc:
        cli.cmd_new(_NewArgs())
    assert exc.value.code == 1


def test_confirm_tools_accepts_all_on_empty_answer(tmp_home, monkeypatch, capsys):
    selected = [catalogue._from_api(_api_item("A_ONE", ["readOnlyHint"])),
                catalogue._from_api(_api_item("B_TWO", []))]
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "")

    kept = cli._confirm_tools(tmp_home, selected, assume_yes=False)

    assert kept == selected
    out = capsys.readouterr().out
    assert "Tools selected (2)" in out


def test_confirm_tools_drops_the_numbers_given(tmp_home, monkeypatch):
    selected = [catalogue._from_api(_api_item(s, ["readOnlyHint"]))
                for s in ("A_ONE", "B_TWO", "C_THREE")]
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "2,3")

    kept = cli._confirm_tools(tmp_home, selected, assume_yes=False)
    assert [t.slug for t in kept] == ["A_ONE"]


def test_confirm_tools_aborts_on_n(tmp_home, monkeypatch):
    selected = [catalogue._from_api(_api_item("A_ONE", []))]
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "n")

    with pytest.raises(SystemExit) as exc:
        cli._confirm_tools(tmp_home, selected, assume_yes=False)
    assert exc.value.code == 0


def test_confirm_tools_refuses_to_continue_with_nothing_left(tmp_home, monkeypatch):
    selected = [catalogue._from_api(_api_item("A_ONE", []))]
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "1")

    with pytest.raises(SystemExit) as exc:
        cli._confirm_tools(tmp_home, selected, assume_yes=False)
    assert exc.value.code == cli.EXIT_USER_ERROR


def test_confirm_tools_warns_harder_about_destructive_tools(tmp_home, monkeypatch, capsys):
    selected = [catalogue._from_api(_api_item("A_DEL", ["destructiveHint"]))]
    monkeypatch.setattr(cli.ui, "prompt", lambda text, **k: "")

    cli._confirm_tools(tmp_home, selected, assume_yes=False)
    assert "destructive tools proposed" in capsys.readouterr().out


def test_confirm_tools_still_reports_when_assuming_yes(tmp_home, capsys):
    """--yes skips the question, not the disclosure."""
    selected = [catalogue._from_api(_api_item("A_ONE", []))]

    kept = cli._confirm_tools(tmp_home, selected, assume_yes=True)

    assert kept == selected
    assert "Tools selected (1)" in capsys.readouterr().out
