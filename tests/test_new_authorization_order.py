"""`px0 workflows new` must verify authorization before it plans, and stop when it can't.

The reported failure: Composio refused to start the GitHub flow (the API key
lacked `auth_configs` write access), and px0 printed the plan and then asked
"Generate this workflow?" anyway -- offering to write a file whose first run
could only hit the same refusal.
"""

import pytest

from px0 import builder as builder_mod, catalogue, cli, ui


class _Tool:
    """The bits of a CatalogueTool that cmd_new touches."""
    def __init__(self, slug, toolkit):
        self.slug, self.toolkit = slug, toolkit
        self.id = catalogue.ID_PREFIX + slug
        self.name = slug
        self.description = "does a thing"
        self.is_write = False
        self.is_destructive = False
        self.params = {}


class _Args:
    def __init__(self, **kw):
        self.yes = True          # skip clarify and every confirmation prompt
        self.id = "assigned-prs"
        self.no_clarify = True
        self.no_discover = False
        self.__dict__.update(kw)


class _quiet_spinner:
    def __init__(self, *a, **k): pass
    def __enter__(self): return self
    def __exit__(self, *a): return False


@pytest.fixture
def new_flow(monkeypatch, tmp_home):
    """Stubs cmd_new's collaborators and records what actually got called."""
    calls = []
    monkeypatch.setattr(ui, "spinner", _quiet_spinner)
    monkeypatch.setattr(cli.ui, "spinner", _quiet_spinner)
    monkeypatch.setattr(cli, "_ctx", lambda: (tmp_home, {}))
    monkeypatch.setattr(cli, "_discover_tools",
                        lambda *a, **k: [_Tool("GITHUB_LIST_ISSUES", "github")])
    monkeypatch.setattr(cli, "_confirm_tools", lambda home, sel, yes: sel)
    monkeypatch.setattr(catalogue, "remember", lambda *a, **k: None)
    monkeypatch.setattr(cli.catalogue_mod, "remember", lambda *a, **k: None)

    def _plan(*a, **k):
        calls.append("plan")
        raise AssertionError("generate_plan should not run when auth is blocked")
    monkeypatch.setattr(cli.builder_mod, "generate_plan", _plan)
    monkeypatch.setattr(cli.builder_mod, "save_workflow",
                        lambda *a, **k: calls.append("save"))
    return calls


def _refuse(monkeypatch, message):
    """Composio answering 'no' to starting the flow, the way connect.py raises it."""
    monkeypatch.setattr(cli.connect_mod, "connected_account_status",
                        lambda home, toolkit: "not authorized")

    def boom(home, toolkit):
        raise ValueError(message)
    monkeypatch.setattr(cli.connect_mod, "connect_composio_app", boom)


_KEY_REFUSAL = (
    '403: This API key does not have the permissions required for '
    'POST /api/v3/auth_configs.'
)


def test_a_refused_authorization_stops_before_planning(new_flow, monkeypatch, tmp_home, capsys):
    _refuse(monkeypatch, _KEY_REFUSAL)

    with pytest.raises(SystemExit) as exc:
        cli._build_workflow(tmp_home, {}, "list my assigned PRs", _Args(), existing_id=None)

    assert exc.value.code == cli.EXIT_CONNECTOR_ERROR
    assert new_flow == [], "nothing should have been planned or saved"


def test_the_refusal_and_the_fact_nothing_was_written_are_both_reported(
        new_flow, monkeypatch, tmp_home, capsys):
    _refuse(monkeypatch, _KEY_REFUSAL)

    with pytest.raises(SystemExit):
        cli._build_workflow(tmp_home, {}, "list my assigned PRs", _Args(), existing_id=None)

    out = capsys.readouterr().out
    assert "auth_configs" in out, "the user needs Composio's own reason"
    assert "nothing was written" in out


def test_a_pending_consent_is_not_treated_as_a_refusal(monkeypatch, tmp_home):
    """A printed consent link still builds: that is the documented design."""
    monkeypatch.setattr(ui, "spinner", _quiet_spinner)
    monkeypatch.setattr(cli.ui, "spinner", _quiet_spinner)
    monkeypatch.setattr(cli.connect_mod, "connected_account_status",
                        lambda home, toolkit: "not authorized")
    monkeypatch.setattr(cli.connect_mod, "connect_composio_app",
                        lambda home, toolkit: {"redirect_url": "https://consent"})

    outcome = cli._authorize_toolkits(tmp_home, {"github"}, True)

    assert outcome.waiting == ["github"] and outcome.blocked == []
    cli._abort_if_blocked(outcome)  # must not exit


def test_declining_to_start_authorization_leaves_it_pending_not_blocked(
        monkeypatch, tmp_home):
    """Saying no defers the consent; it does not make the workflow unbuildable."""
    monkeypatch.setattr(ui, "spinner", _quiet_spinner)
    monkeypatch.setattr(cli.ui, "spinner", _quiet_spinner)
    monkeypatch.setattr(cli.connect_mod, "connected_account_status",
                        lambda home, toolkit: "not authorized")
    monkeypatch.setattr(ui, "prompt", lambda text: "n")
    monkeypatch.setattr(cli.ui, "prompt", lambda text: "n")

    outcome = cli._authorize_toolkits(tmp_home, {"github"}, False)

    assert outcome.waiting == ["github"] and outcome.blocked == []


def test_blocked_toolkits_survive_being_merged_across_both_passes():
    """cmd_new runs two passes; a refusal in either must still abort."""
    first = cli._AuthOutcome(["slack"], [])
    second = cli._AuthOutcome([], [("github", "403")])

    merged = first + second

    assert merged.waiting == ["slack"] and merged.blocked == [("github", "403")]
    with pytest.raises(SystemExit):
        cli._abort_if_blocked(merged)
