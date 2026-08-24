"""The CLI is entity-first: `px0 <entity> <verb>`, with five flat exceptions.

These tests pin the *shape*, not every flag -- the point is that a new command
can't quietly land as a flat verb again, and that the old flat names are gone
rather than silently doing something else.
"""

import argparse

import pytest

from px0 import cli


# `status` and `completion` act on the whole install rather than on one entity:
# status reports across every group, completion emits a shell script. Both read
# wrong as `px0 <entity> status`, the way `git status` would. `uninstall` is the
# same kind of exception -- it acts on the install itself, not on one entity's
# content, so `px0 <entity> uninstall` would read wrong the same way.
FLAT = {"init", "doctor", "version", "update", "status", "completion", "uninstall"}

# The verbs each entity answers to. Anything added to a group should be added
# here too, so the surface stays something you can read in one place.
GROUPS = {
    "workflows":  {"new", "run", "edit", "list", "show", "validate", "delete", "rename",
                   "copy", "disable", "enable"},
    "brain":      {"add", "refresh", "list", "search", "ask", "reindex", "show", "rm",
                   "export"},
    "guidelines": {"list", "log", "edit", "show", "rm"},
    "runs":       {"list", "show", "output", "rerun", "logs", "why", "cancel", "prune",
                   "open"},
    "changes":    {"list", "show", "revert"},
    "store":      {"export", "list", "import", "path", "verify"},
    "config":     {"list", "get", "set", "unset", "edit", "path", "model", "composio"},
    "tools":      {"list", "search", "call", "connect", "disconnect", "refresh"},
    "daemon":     {"install", "uninstall", "status", "start", "stop", "restart",
                   "logs", "serve"},
    "mcp":        {"serve"},
}


def _subparsers(parser):
    """The parser's subcommand action, or None if it has no subcommands.

    Matched on the action type rather than on `.choices`, because plenty of
    plain flags carry choices too -- `px0 init --harness` among them.
    """
    return next((a for a in parser._actions
                 if isinstance(a, argparse._SubParsersAction)), None)


def _top_level():
    return _subparsers(cli.build_parser()).choices


def _verbs(entity):
    sub = _subparsers(_top_level()[entity])
    return set(sub.choices) if sub else set()


def test_only_the_four_install_commands_are_flat():
    """Everything else must name its entity first."""
    top = set(_top_level())
    leaves = {name for name in top if not _verbs(name)}
    assert leaves == FLAT, leaves


@pytest.mark.parametrize("entity, verbs", sorted(GROUPS.items()))
def test_each_entity_exposes_exactly_its_verbs(entity, verbs):
    assert _verbs(entity) == verbs


@pytest.mark.parametrize("gone", ["new", "run", "list", "ask", "search", "why", "consolidate"])
def test_the_old_flat_verbs_are_gone(gone):
    """A hard break: these must error, not resolve to something unexpected."""
    assert gone not in _top_level()
    with pytest.raises(SystemExit):
        cli.build_parser().parse_args([gone, "x"])


@pytest.mark.parametrize("argv, handler", [
    (["workflows", "new"], "cmd_new"),
    (["workflows", "run", "wf"], "cmd_run"),
    (["workflows", "list"], "cmd_workflows_list"),
    (["brain", "add", "https://x"], "cmd_brain"),
    (["brain", "list"], "cmd_brain_list"),
    (["brain", "search", "q"], "cmd_search"),
    (["brain", "ask", "q"], "cmd_ask"),
    (["brain", "reindex"], "cmd_reindex"),
    (["guidelines", "list"], "cmd_guidelines_list"),
    (["guidelines", "log", "f.md#c1"], "cmd_guidelines"),
    (["guidelines", "edit", "style"], "cmd_guidelines_file"),
    (["runs", "why", "r_1"], "cmd_why"),
    (["runs", "list"], "cmd_runs"),
    (["store", "list"], "cmd_store_list"),
    (["store", "export", "/tmp/x"], "cmd_store"),
    (["doctor"], "cmd_doctor"),
])
def test_every_leaf_dispatches_to_its_own_handler(argv, handler):
    assert cli.build_parser().parse_args(argv).func.__name__ == handler


def test_why_is_a_runs_verb_only():
    """Claim provenance went with `guidelines why`; a run's is what is left."""
    p = cli.build_parser()
    assert p.parse_args(["runs", "why", "r_20260820"]).target_id == "r_20260820"
    with pytest.raises(SystemExit):
        p.parse_args(["guidelines", "why", "go.md#c1"])


@pytest.mark.parametrize("gone", [
    ["secrets", "list"],
    ["secrets", "set", "GITHUB_TOKEN", "x"],
])
def test_secrets_are_gone_now_that_every_credential_lives_at_composio(gone):
    """The only credential px0 holds is the Composio API key, in config.toml."""
    with pytest.raises(SystemExit):
        cli.build_parser().parse_args(gone)


@pytest.mark.parametrize("gone", [
    ["versions", "list", "workflows/x.md"],
    ["versions", "show", "workflows/x.md@v1"],
    ["versions", "diff", "workflows/x.md", "1", "2"],
    ["versions", "revert", "workflows/x.md", "--to", "v1"],
    ["versions", "prune"],
])
def test_per_file_version_verbs_are_gone(gone):
    """`changes` is the unit of undo now; the store still keeps its history."""
    with pytest.raises(SystemExit):
        cli.build_parser().parse_args(gone)


def test_a_guideline_cannot_be_created_by_hand():
    """`px0 workflows new` writes guidelines; there is no other way in."""
    with pytest.raises(SystemExit):
        cli.build_parser().parse_args(["guidelines", "new", "style"])


def test_a_group_with_no_verb_is_an_error_not_a_no_op():
    """`px0 brain` alone must say what it wants; only `runs` defaults."""
    with pytest.raises(SystemExit):
        cli.build_parser().parse_args(["brain"])
    assert cli.build_parser().parse_args(["runs"]).runs_cmd is None


def test_reindex_is_a_verb_so_reindex_is_still_a_searchable_word():
    """The old `px0 search reindex` made its own name unsearchable."""
    args = cli.build_parser().parse_args(["brain", "search", "reindex"])
    assert args.func.__name__ == "cmd_search" and args.query == "reindex"


# --- config: --help has to name the keys ------------------------------------

def _leaf(entity, verb):
    return _subparsers(_top_level()[entity]).choices[verb]


def test_config_get_and_set_help_list_every_settable_key():
    """A key you have to know is a key --help must name."""
    from px0 import config as config_mod

    for verb in ("get", "set"):
        epilog = _leaf("config", verb).epilog
        assert epilog, verb
        for key in config_mod.SCHEMA:
            assert key in epilog, (verb, key)


def test_set_shows_allowed_values_and_get_does_not():
    """Choices constrain what you can write; they say nothing about reading."""
    assert "stable|beta" in _leaf("config", "set").epilog
    assert "stable|beta" not in _leaf("config", "get").epilog


def test_the_key_list_is_grouped_and_typed():
    from px0 import config as config_mod

    text = config_mod.key_help()
    assert "  retrieval.qmd_cmd" in text and "bool" in text and "int" in text
    # a blank line separates each TOML table from the next
    assert "\n\n  logs.path" in text
    assert "px0 config list" in text, "should point at the fuller listing"


def test_an_unknown_key_is_a_user_error_not_an_argparse_error():
    """argparse `choices` would exit 2, which this CLI uses for connector
    failures -- so the key stays free-form and config.get_key validates it."""
    from px0 import config as config_mod

    args = cli.build_parser().parse_args(["config", "get", "not.a.key"])
    assert args.key == "not.a.key"
    with pytest.raises(ValueError, match="unknown config key"):
        config_mod.get_key({}, "not.a.key")
