"""Tools that run on this machine rather than through Composio.

Everything in `tools.REGISTRY` reaches an external app. That left a whole
class of chore unreachable: read a file in a repository, run the script that
already does the job, fetch a URL that is not an app px0 has a connector for,
file something into the brain. These are those tools, plus the loader for the
ones a user declares themselves.

Two rules hold across all of them:

- Anything that can change the machine is a write tool, so it is declared in a
  workflow's `tools:` and shown as a write at build time.
- Nothing reads or writes outside an allowed root, and the shell is off until
  the store's config turns it on. A workflow file is content the model wrote;
  it does not get unbounded access to the filesystem by default.
"""

import json
import os
import shlex
import subprocess
import tomllib
from dataclasses import dataclass, field
from pathlib import Path

from px0 import config as config_mod, paths

# Only these schemes are fetched. A workflow that resolves file:// through the
# HTTP tool would sidestep the root allowlist entirely.
HTTP_SCHEMES = ("http://", "https://")

ID_RE = r"^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$"


class LocalToolError(Exception):
    """A local tool refused to run, or failed while running."""


def _cap(config: dict) -> int:
    return max(256, int(config_mod.get(config, "tools.max_output_bytes", 20000)))


def _truncate(text: str, config: dict) -> str:
    """Caps text at tools.max_output_bytes, saying so rather than lying by omission."""
    limit = _cap(config)
    if len(text) <= limit:
        return text
    return text[:limit] + f"\n[truncated at {limit} bytes]"


def allowed_roots(home: Path, config: dict) -> list[Path]:
    """Directories the file tools may touch: the store, plus tools.file_roots."""
    roots = [Path(home).expanduser().resolve()]
    for raw in config_mod.get(config, "tools.file_roots", []) or []:
        try:
            roots.append(Path(str(raw)).expanduser().resolve())
        except OSError:
            continue
    return roots


def resolve_within_roots(home: Path, config: dict, raw: str, *, must_exist: bool) -> Path:
    """Resolves `raw` and confirms it lands inside an allowed root.

    Resolves symlinks first, so a link inside a root pointing outside one is
    refused rather than followed. Raises LocalToolError naming the roots, since
    the fix is either a different path or another entry in tools.file_roots.
    """
    if not str(raw or "").strip():
        raise LocalToolError("no path given")
    roots = allowed_roots(home, config)
    candidate = Path(str(raw)).expanduser()
    if not candidate.is_absolute():
        candidate = Path(home).expanduser() / candidate
    try:
        resolved = candidate.resolve()
    except OSError as e:
        raise LocalToolError(f"cannot resolve {raw!r}: {e}") from e
    for root in roots:
        if resolved == root or root in resolved.parents:
            break
    else:
        listed = ", ".join(str(r) for r in roots)
        raise LocalToolError(
            f"{resolved} is outside every allowed root ({listed}); add its directory "
            "with `px0 config set tools.file_roots <dir>`"
        )
    if must_exist and not resolved.exists():
        raise LocalToolError(f"no such file: {resolved}")
    if must_exist and resolved.is_dir():
        raise LocalToolError(f"{resolved} is a directory, not a file")
    return resolved


# --- built-in local tools -------------------------------------------------

def file_read(args: dict, ctx) -> str:
    """Reads a text file from inside an allowed root. Read-only."""
    path = resolve_within_roots(ctx.home, ctx.config, args.get("path", ""), must_exist=True)
    try:
        text = path.read_text(errors="replace")
    except OSError as e:
        raise LocalToolError(f"cannot read {path}: {e}") from e
    return _truncate(text, ctx.config)


# Parts of the store a workflow may never write through `file.write`, however
# broad its roots. The store itself is an allowed root -- that is what lets a
# workflow write into `output/` -- and without this that also meant a workflow
# given `file.write` could rewrite its own `tools:` allowlist to grant itself
# more tools on the next run, turn `confirm_writes` off in `config.toml`, or
# read-modify-write `.state/credentials.toml`. Those are different powers from
# "write a file", and nothing distinguished them.
#
# Everything here has a purpose-built tool already: `memory.remember` for
# memory, `brain.add` for the brain, `px0 workflows edit` for a workflow.
PROTECTED_STORE_PATHS = ("workflows", "guidelines", "memory", ".state")
PROTECTED_STORE_FILES = ("config.toml",)


def _refuse_if_control_surface(home: Path, path: Path) -> None:
    """Refuses a write that would edit the store's own control files."""
    store = Path(home).expanduser().resolve()
    try:
        rel = path.relative_to(store)
    except ValueError:
        return  # outside the store entirely: an ordinary allowed root
    head = rel.parts[0] if rel.parts else ""
    if head in PROTECTED_STORE_PATHS or str(rel) in PROTECTED_STORE_FILES:
        raise LocalToolError(
            f"file.write may not touch {rel} -- that is px0's own configuration, "
            "and a workflow able to edit it could widen what it is allowed to do. "
            "Use the command or tool for it instead (`px0 workflows edit`, "
            "`memory.remember`, `brain.add`)")


def file_write(args: dict, ctx) -> dict:
    """Writes a text file inside an allowed root, creating parent directories.

    Write tool: it replaces whatever was at that path.
    """
    path = resolve_within_roots(ctx.home, ctx.config, args.get("path", ""), must_exist=False)
    _refuse_if_control_surface(ctx.home, path)
    content = args.get("content")
    if content is None:
        raise LocalToolError("file.write needs content")
    if path.is_dir():
        raise LocalToolError(f"{path} is a directory")
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        text = content if isinstance(content, str) else json.dumps(content, indent=2, default=str)
        path.write_text(text)
    except OSError as e:
        raise LocalToolError(f"cannot write {path}: {e}") from e
    return {"path": str(path), "bytes": path.stat().st_size}


def file_list(args: dict, ctx) -> list[str]:
    """Lists files matching a glob inside an allowed root. Read-only."""
    base = resolve_within_roots(ctx.home, ctx.config, args.get("path", "."), must_exist=False)
    if not base.is_dir():
        raise LocalToolError(f"{base} is not a directory")
    pattern = args.get("pattern") or "*"
    if pattern.startswith("/") or ".." in pattern:
        raise LocalToolError(f"pattern {pattern!r} must be relative and may not contain '..'")
    limit = int(args.get("limit") or 200)
    out = []
    for p in sorted(base.glob(pattern)):
        if p.is_file():
            out.append(str(p))
        if len(out) >= limit:
            break
    return out


def shell_run(args: dict, ctx) -> dict:
    """Runs one command locally, without a shell. Write tool, and off by default.

    The command is argv, not a string handed to `sh`, so nothing in an
    argument is interpreted as a pipe, a redirect, or another command. A
    string is split with shlex for convenience and gets the same treatment.
    """
    if not config_mod.get(ctx.config, "tools.allow_shell", False):
        raise LocalToolError(
            "shell.run is disabled; enable it with `px0 config set tools.allow_shell true` "
            "and understand that a workflow using it can run anything you can"
        )
    raw = args.get("command")
    if isinstance(raw, str):
        argv = shlex.split(raw)
    elif isinstance(raw, (list, tuple)):
        argv = [str(a) for a in raw]
    else:
        raise LocalToolError("shell.run needs a command")
    if not argv:
        raise LocalToolError("shell.run needs a command")

    cwd = None
    if args.get("cwd"):
        cwd = resolve_within_roots(ctx.home, ctx.config, args["cwd"], must_exist=False)
        if not cwd.is_dir():
            raise LocalToolError(f"cwd {cwd} is not a directory")
    timeout = int(args.get("timeout") or 120)
    try:
        proc = subprocess.run(argv, cwd=str(cwd) if cwd else None, capture_output=True,
                              text=True, timeout=timeout)
    except FileNotFoundError:
        raise LocalToolError(f"command not found: {argv[0]}") from None
    except subprocess.TimeoutExpired:
        raise LocalToolError(f"{argv[0]} did not finish within {timeout}s") from None
    except OSError as e:
        raise LocalToolError(f"could not run {argv[0]}: {e}") from e
    return {
        "exit_code": proc.returncode,
        "stdout": _truncate(proc.stdout or "", ctx.config),
        "stderr": _truncate(proc.stderr or "", ctx.config),
    }


def _http(args: dict, ctx, method: str) -> dict:
    import requests
    from px0 import connect as connect_mod

    url = str(args.get("url") or "")
    if not url.startswith(HTTP_SCHEMES):
        raise LocalToolError(f"url must start with http:// or https://, got {url!r}")
    timeout = int(args.get("timeout") or config_mod.get(ctx.config, "tools.http_timeout", 20))
    headers = args.get("headers") or {}
    if not isinstance(headers, dict):
        raise LocalToolError("headers must be a mapping")
    body = args.get("body")
    verify = connect_mod.apply_ca_bundle(ctx.home) or True
    try:
        resp = requests.request(method, url, headers={str(k): str(v) for k, v in headers.items()},
                                json=body if isinstance(body, (dict, list)) else None,
                                data=None if isinstance(body, (dict, list)) else body,
                                timeout=timeout, verify=verify, allow_redirects=True)
    except requests.RequestException as e:
        raise LocalToolError(f"{method} {url} failed: {e}") from e
    return {
        "status": resp.status_code,
        "headers": dict(resp.headers),
        "body": _truncate(resp.text or "", ctx.config),
    }


def http_get(args: dict, ctx) -> dict:
    """Fetches a URL. Read-only."""
    return _http(args, ctx, "GET")


def http_post(args: dict, ctx) -> dict:
    """Posts to a URL. Write tool: the other end decides what that means."""
    return _http(args, ctx, args.get("method", "POST").upper())


def brain_add(args: dict, ctx) -> dict:
    """Ingests a URL or file into the brain. Write tool: it adds a file to the store.

    This is what makes "save what I read" a workflow rather than a thing you
    run by hand afterwards.
    """
    from px0 import brain as brain_mod

    source = str(args.get("source") or "").strip()
    if not source:
        raise LocalToolError("brain.add needs a source")
    try:
        result = brain_mod.add(ctx.home, ctx.config, source, to=args.get("to"))
    except Exception as e:
        raise LocalToolError(f"could not ingest {source}: {e}") from e
    fields = ("path", "kind", "title", "source", "note")
    if isinstance(result, dict):
        return {k: str(v) for k, v in result.items() if k in fields}
    return {k: str(getattr(result, k)) for k in fields if getattr(result, k, None)}


def memory_remember(args: dict, ctx) -> dict:
    """Writes one fact into the store's memory. Write tool: it adds a file.

    This is what lets a run keep something it learned -- that a repository is
    the one you meant, that a report should go to a different channel -- rather
    than rediscovering it every time. It writes to the store, not to a service,
    and every write is a versioned change, so what an assistant has come to
    believe about you stays something you can read and revert.
    """
    from px0 import memory as memory_mod

    text = str(args.get("text") or "").strip()
    if not text:
        raise LocalToolError("memory.remember needs text to remember")
    try:
        entry = memory_mod.remember(
            ctx.home, text,
            kind=str(args.get("kind") or "fact"),
            subject=str(args.get("subject") or "").strip(),
            source=str(args.get("source") or "workflow"),
            actor="workflow")
    except memory_mod.MemoryError_ as e:
        raise LocalToolError(str(e)) from e
    return {"name": entry.name, "kind": entry.kind, "subject": entry.subject}


def memory_recall(args: dict, ctx) -> list[dict]:
    """Looks up what px0 remembers about something. Read tool.

    A run already gets the memories relevant to its own instructions inlined;
    this is for the case where what it needs to look up depends on what it
    found -- a name in a pull request, a project mentioned in an email.
    """
    from px0 import memory as memory_mod

    query = str(args.get("query") or "").strip()
    if not query:
        raise LocalToolError("memory.recall needs a query")
    limit = args.get("limit")
    try:
        limit = max(1, int(limit)) if limit is not None else 5
    except (TypeError, ValueError):
        limit = 5
    found = memory_mod.relevant(ctx.home, query)[:limit]
    return [{"subject": m.subject, "kind": m.kind, "text": m.text} for m in found]


# id -> (provider, description, params, is_write, handler)
BUILTINS = {
    "file.read": ("file", "Read a text file from an allowed root",
                  {"path": "str*"}, False, file_read),
    "file.list": ("file", "List files matching a glob inside an allowed root",
                  {"path": "str*", "pattern": "str", "limit": "int"}, False, file_list),
    "file.write": ("file", "Write a text file inside an allowed root",
                   {"path": "str*", "content": "str*"}, True, file_write),
    "shell.run": ("shell", "Run one local command (disabled until tools.allow_shell is true)",
                  {"command": "str*", "cwd": "str", "timeout": "int"}, True, shell_run),
    "http.get": ("http", "Fetch a URL",
                 {"url": "str*", "headers": "object", "timeout": "int"}, False, http_get),
    "http.post": ("http", "Send a request with a body to a URL",
                  {"url": "str*", "body": "object", "headers": "object",
                   "method": "str"}, True, http_post),
    "brain.add": ("brain", "Ingest a URL or file into the brain",
                  {"source": "str*", "to": "str"}, True, brain_add),
    "memory.remember": ("memory", "Remember one fact about the user or their work",
                        {"text": "str*", "subject": "str", "kind": "str"},
                        True, memory_remember),
    "memory.recall": ("memory", "Look up what px0 remembers about something",
                      {"query": "str*", "limit": "int"}, False, memory_recall),
}


# --- user-declared tools --------------------------------------------------

@dataclass
class UserTool:
    """One tool declared by a TOML file under the store's `tools/` folder."""
    id: str
    description: str
    command: list[str]
    params: dict[str, str]
    is_write: bool
    timeout: int
    cwd: str | None = None
    # Environment variables this tool needs, by name. Values are never written
    # here: px0 passes through what the environment already holds, and reports
    # the ones that are missing rather than running a command that will fail
    # halfway with a confusing error from the far end.
    env: list[str] = field(default_factory=list)


def _validate_user_tool(data: dict, source: Path) -> UserTool:
    import re

    tool_id = str(data.get("id") or "").strip()
    if not re.match(ID_RE, tool_id):
        raise LocalToolError(
            f"{source.name}: id must look like 'group.name' (lowercase, underscores), got {tool_id!r}")
    if tool_id in BUILTINS:
        raise LocalToolError(f"{source.name}: {tool_id} is a built-in tool id")
    command = data.get("command")
    if isinstance(command, str):
        command = shlex.split(command)
    if not isinstance(command, list) or not command or not all(isinstance(c, str) for c in command):
        raise LocalToolError(f"{source.name}: command must be a non-empty list of strings")
    params = data.get("params") or {}
    if not isinstance(params, dict) or not all(isinstance(v, str) for v in params.values()):
        raise LocalToolError(f"{source.name}: params must be a table of name = \"type\"")
    env = data.get("env") or []
    if isinstance(env, str):
        env = [env]
    if not isinstance(env, list) or not all(isinstance(e, str) for e in env):
        raise LocalToolError(f"{source.name}: env must be a list of variable names")
    return UserTool(
        id=tool_id,
        description=str(data.get("description") or tool_id),
        command=[str(c) for c in command],
        params={str(k): str(v) for k, v in params.items()},
        is_write=bool(data.get("is_write", True)),
        timeout=int(data.get("timeout") or 120),
        cwd=str(data["cwd"]) if data.get("cwd") else None,
        env=[str(e) for e in env],
    )


def user_tool_files(home: Path) -> list[Path]:
    """Every TOML file declaring a user tool, sorted by name."""
    base = paths.tools_dir(home)
    if not base.exists():
        return []
    return sorted(p for p in base.glob("*.toml") if p.is_file())


def load_user_tools(home: Path) -> tuple[dict[str, UserTool], list[str]]:
    """Loads every user-declared tool, returning (tools_by_id, errors).

    Never raises: one malformed file must not hide the others, and it must not
    take down `px0 tools list` or a run that does not use it.
    """
    out: dict[str, UserTool] = {}
    errors: list[str] = []
    for path in user_tool_files(home):
        try:
            data = tomllib.loads(path.read_text())
        except (OSError, tomllib.TOMLDecodeError) as e:
            errors.append(f"{path.name}: {e}")
            continue
        try:
            tool = _validate_user_tool(data, path)
        except LocalToolError as e:
            errors.append(str(e))
            continue
        if tool.id in out:
            errors.append(f"{path.name}: duplicate tool id {tool.id}")
            continue
        out[tool.id] = tool
    return out, errors


def _substitute(argv: list[str], args: dict) -> list[str]:
    """Fills {param} placeholders in argv from args, one argument at a time.

    Substitution happens per argv element and never re-splits, so a value
    containing a space stays one argument and a value containing a semicolon is
    still just text -- there is no shell to interpret it.
    """
    out = []
    for token in argv:
        rendered = token
        for key, value in args.items():
            rendered = rendered.replace("{" + str(key) + "}", "" if value is None else str(value))
        if "{" in rendered and "}" in rendered:
            missing = rendered[rendered.index("{"): rendered.index("}") + 1]
            raise LocalToolError(f"no value given for {missing}")
        out.append(rendered)
    return out


def _tool_env(tool: UserTool) -> dict | None:
    """The environment one declared tool runs in.

    A tool that declares nothing inherits the whole environment, which is what
    every existing tool already relies on. A tool that *does* declare its
    variables gets a deliberately narrow one instead -- PATH, HOME, and what it
    named -- so a credential meant for one command is not handed to every other
    command a workflow can reach.

    A declared variable that is not set is refused before the command runs,
    because the alternative is a tool that fails halfway with whatever error
    the far end gives an unauthenticated request.
    """
    import os

    if not tool.env:
        return None
    missing = [name for name in tool.env if not os.environ.get(name)]
    if missing:
        raise LocalToolError(
            f"{tool.id} needs {', '.join(missing)} in the environment, and "
            f"{'they are' if len(missing) > 1 else 'it is'} not set")
    base = {key: os.environ[key] for key in ("PATH", "HOME", "LANG", "TZ")
            if key in os.environ}
    base.update({name: os.environ[name] for name in tool.env})
    return base


def run_user_tool(tool: UserTool, args: dict, ctx) -> dict:
    """Runs a user-declared tool: argv substitution, no shell, capped output."""
    argv = _substitute(tool.command, args)
    required = [k for k, v in tool.params.items() if v.endswith("*")]
    missing = [k for k in required if not str(args.get(k, "")).strip()]
    if missing:
        raise LocalToolError(f"{tool.id} needs {', '.join(missing)}")
    cwd = None
    if tool.cwd:
        cwd = resolve_within_roots(ctx.home, ctx.config, tool.cwd, must_exist=False)
    env = _tool_env(tool)
    try:
        proc = subprocess.run(argv, cwd=str(cwd) if cwd else None, capture_output=True,
                              text=True, timeout=tool.timeout, env=env)
    except FileNotFoundError:
        raise LocalToolError(f"{tool.id}: command not found: {argv[0]}") from None
    except subprocess.TimeoutExpired:
        raise LocalToolError(f"{tool.id} did not finish within {tool.timeout}s") from None
    except OSError as e:
        raise LocalToolError(f"{tool.id} could not run: {e}") from e
    if proc.returncode != 0:
        raise LocalToolError(
            f"{tool.id} exited {proc.returncode}: {_truncate(proc.stderr or proc.stdout or '', ctx.config)}")
    return {"stdout": _truncate(proc.stdout or "", ctx.config), "exit_code": proc.returncode}


EXAMPLE_TOOL = '''# One TOML file per tool, in this folder. px0 reads them at run time, so a new
# file is usable immediately -- no restart, no reinstall.
#
# id           group.name, lowercase. Names the tool in a workflow.
# command      argv, not a shell line. {placeholders} come from params.
# params       name = "type"; a trailing * marks it required.
# is_write     true if the command changes anything. Defaults to true.
# env          names of environment variables the command needs. Declaring any
#              of them narrows what the command can see to PATH, HOME and those
#              -- so a token meant for this tool is not handed to every other.
#              px0 refuses to run the tool when one of them is unset.
# timeout      seconds. Defaults to 120.
# cwd          optional working directory, inside an allowed file root.

id = "local.example"
description = "Print the store's path, as an example of a user-declared tool"
command = ["echo", "{message}"]
params = { message = "str*" }
is_write = false
timeout = 30
'''
