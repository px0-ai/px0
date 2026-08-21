"""Tools that run on this machine.

Two things are being pinned here: the file tools must not read or write outside
an allowed root, however the path is spelled -- and the shell must stay off
until the store says otherwise, because a workflow that can run a shell can do
anything its user can.
"""

import pytest

from px0 import config as config_mod, localtools, paths, tools


@pytest.fixture
def ctx(tmp_home):
    config = config_mod.load(paths.config_path(tmp_home))
    return tools.Context(home=tmp_home, config=config)


# --- the root allowlist --------------------------------------------------

def test_a_file_inside_the_store_is_readable(ctx):
    (ctx.home / "brain" / "docs" / "note.md").write_text("hello")
    assert localtools.file_read({"path": "brain/docs/note.md"}, ctx) == "hello"


@pytest.mark.parametrize("attempt", [
    "/etc/passwd",
    "../../../../etc/passwd",
    "brain/../../../../etc/passwd",
])
def test_a_path_outside_every_root_is_refused(ctx, attempt):
    with pytest.raises(localtools.LocalToolError) as e:
        localtools.file_read({"path": attempt}, ctx)
    assert "outside every allowed root" in str(e.value)


def test_a_symlink_pointing_out_of_the_store_is_refused(ctx, tmp_path):
    outside = tmp_path / "secret.txt"
    outside.write_text("not yours")
    link = ctx.home / "escape.md"
    link.symlink_to(outside)

    with pytest.raises(localtools.LocalToolError):
        localtools.file_read({"path": "escape.md"}, ctx)


def test_an_extra_root_can_be_allowed_explicitly(ctx, tmp_path):
    allowed = tmp_path / "repo"
    allowed.mkdir()
    (allowed / "readme.md").write_text("in the repo")
    config_mod.set_key(ctx.config, "tools.file_roots", str(allowed))

    assert localtools.file_read({"path": str(allowed / "readme.md")}, ctx) == "in the repo"


def test_output_is_capped_and_says_so(ctx):
    config_mod.set_key(ctx.config, "tools.max_output_bytes", "300")
    (ctx.home / "big.md").write_text("x" * 5000)

    out = localtools.file_read({"path": "big.md"}, ctx)

    assert "truncated" in out
    assert len(out) < 5000


def test_writing_creates_parent_directories(ctx):
    result = localtools.file_write({"path": "output/deep/here.md", "content": "hi"}, ctx)
    assert (ctx.home / "output" / "deep" / "here.md").read_text() == "hi"
    assert result["bytes"] == 2


def test_listing_refuses_a_pattern_that_climbs_out(ctx):
    with pytest.raises(localtools.LocalToolError):
        localtools.file_list({"path": ".", "pattern": "../*"}, ctx)


# --- the shell ------------------------------------------------------------

def test_the_shell_is_off_by_default(ctx):
    with pytest.raises(localtools.LocalToolError) as e:
        localtools.shell_run({"command": "echo hi"}, ctx)
    assert "tools.allow_shell" in str(e.value)


def test_an_enabled_shell_runs_one_command(ctx):
    config_mod.set_key(ctx.config, "tools.allow_shell", "true")
    result = localtools.shell_run({"command": "echo hi"}, ctx)
    assert result["exit_code"] == 0
    assert result["stdout"].strip() == "hi"


def test_the_shell_never_interprets_a_pipe_or_a_semicolon(ctx, tmp_path):
    config_mod.set_key(ctx.config, "tools.allow_shell", "true")
    canary = tmp_path / "pwned"

    result = localtools.shell_run({"command": ["echo", f"hi; touch {canary}"]}, ctx)

    assert not canary.exists()
    assert str(canary) in result["stdout"]


def test_a_missing_binary_is_a_clean_error(ctx):
    config_mod.set_key(ctx.config, "tools.allow_shell", "true")
    with pytest.raises(localtools.LocalToolError) as e:
        localtools.shell_run({"command": "px0-definitely-not-a-binary"}, ctx)
    assert "command not found" in str(e.value)


# --- user-declared tools -------------------------------------------------

def _declare(home, text, name="mine.toml"):
    path = paths.tools_dir(home) / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    return path


def test_a_declared_tool_becomes_callable(tmp_home):
    _declare(tmp_home, 'id = "local.greet"\ncommand = ["echo", "hello {name}"]\n'
                        'params = { name = "str*" }\nis_write = false\n')

    spec = tools.resolve("local.greet", tmp_home)

    assert spec is not None
    assert spec.is_write is False
    assert "local.greet" in [t.id for t in tools.list_tools(home=tmp_home)]


def test_a_declared_tool_substitutes_arguments_without_a_shell(tmp_home):
    from px0 import config as cm

    _declare(tmp_home, 'id = "local.greet"\ncommand = ["echo", "hello {name}"]\n'
                        'params = { name = "str*" }\nis_write = false\n')
    config = cm.load(paths.config_path(tmp_home))

    result = tools.call(tmp_home, config, "local.greet", {"name": "Arpit; rm -rf /"})

    assert result["stdout"].strip() == "hello Arpit; rm -rf /"


def test_a_missing_required_argument_is_refused(tmp_home):
    from px0 import config as cm

    _declare(tmp_home, 'id = "local.greet"\ncommand = ["echo", "hi {name}"]\n'
                        'params = { name = "str*" }\n')
    config = cm.load(paths.config_path(tmp_home))

    with pytest.raises(tools.ConnectorError):
        tools.call(tmp_home, config, "local.greet", {})


def test_a_declared_tool_defaults_to_being_a_write_tool(tmp_home):
    _declare(tmp_home, 'id = "local.deploy"\ncommand = ["true"]\n')
    assert tools.resolve("local.deploy", tmp_home).is_write is True


@pytest.mark.parametrize("bad, why", [
    ('id = "nope"\ncommand = ["true"]\n', "group.name"),
    ('id = "local.x"\n', "command"),
    ('id = "file.read"\ncommand = ["true"]\n', "built-in"),
    ('id = "local.x"\ncommand = ["true"]\nparams = { n = 3 }\n', "params"),
])
def test_a_malformed_declaration_is_reported_and_skipped(tmp_home, bad, why):
    _declare(tmp_home, bad)
    found, errors = localtools.load_user_tools(tmp_home)
    assert not found
    assert any(why in e for e in errors)


def test_one_broken_file_does_not_hide_the_others(tmp_home):
    _declare(tmp_home, 'id = "local.good"\ncommand = ["true"]\n', "good.toml")
    _declare(tmp_home, "this is not toml = = =", "bad.toml")

    found, errors = localtools.load_user_tools(tmp_home)

    assert "local.good" in found
    assert errors


def test_the_scaffolded_example_is_not_loaded_as_a_tool(tmp_home):
    # It ships as .toml.sample precisely so a fresh store has no tool nobody asked for.
    assert (paths.tools_dir(tmp_home) / "example.toml.sample").exists()
    assert localtools.load_user_tools(tmp_home) == ({}, [])
