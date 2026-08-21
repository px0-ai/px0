"""Shell completion for px0.

64 nodes in the command tree, and most arguments are ids you would otherwise
have to remember: a workflow id, a guideline name, a config key, a run id.
Completion is generated from the argparse tree rather than hand-written, so a
new verb is completable the day it lands and a removed one stops being offered.

The scripts call back into `px0 --complete`, which keeps the shell side small
and lets the dynamic parts -- your actual workflow ids -- come from the store.
"""

import argparse
from pathlib import Path

SHELLS = ("bash", "zsh", "fish")

# Arguments whose values px0 can enumerate. The key is the argparse dest, which
# is stable across help text changes.
DYNAMIC = {
    "workflow": "workflows",
    "run_id": "runs",
    "key": "config_keys",
    "claim_id": "claims",
}


def _subparsers(parser: argparse.ArgumentParser):
    for action in parser._actions:
        if isinstance(action, argparse._SubParsersAction):
            return action
    return None


def walk(parser: argparse.ArgumentParser, prefix: str = "px0") -> dict[str, dict]:
    """Flattens the argparse tree into {command path: {verbs, options, dests}}."""
    out: dict[str, dict] = {}
    sub = _subparsers(parser)
    options, dests = [], []
    for action in parser._actions:
        if isinstance(action, (argparse._HelpAction, argparse._SubParsersAction)):
            continue
        if action.help == argparse.SUPPRESS:
            continue  # internal flags stay out of completion, as they are out of --help
        options.extend(o for o in action.option_strings if o.startswith("--"))
        if not action.option_strings:
            dests.append(action.dest)
    out[prefix] = {
        "verbs": sorted(sub.choices) if sub else [],
        "options": sorted(options),
        "dests": dests,
    }
    if sub:
        for name, child in sub.choices.items():
            out.update(walk(child, f"{prefix} {name}"))
    return out


def complete(parser: argparse.ArgumentParser, words: list[str], home: Path | None = None) -> list[str]:
    """Candidate completions for a partly typed command line.

    `words` is everything after `px0`, with the word being completed last (and
    possibly empty). Verbs come from the tree; values come from the store when
    the position takes one of the enumerable arguments.
    """
    tree = walk(parser)
    typed = list(words)
    current = typed.pop() if typed else ""

    # Walk as deep into the tree as the complete words allow.
    path = "px0"
    consumed = 0
    for word in typed:
        candidate = f"{path} {word}"
        if candidate in tree:
            path = candidate
            consumed += 1
        elif word.startswith("-"):
            continue
        else:
            break
    node = tree.get(path, tree["px0"])

    if current.startswith("-"):
        return [o for o in node["options"] if o.startswith(current)]

    candidates = list(node["verbs"])
    positional = [d for d in node["dests"] if d in DYNAMIC]
    if positional and not node["verbs"]:
        candidates += _values(DYNAMIC[positional[0]], home)
    elif positional and len(typed) > consumed:
        candidates += _values(DYNAMIC[positional[0]], home)
    return sorted({c for c in candidates if c.startswith(current)})


def _values(kind: str, home: Path | None) -> list[str]:
    """Enumerates one kind of dynamic value, or nothing if it cannot be read.

    Completion runs on every tab press, so every branch here is cheap and
    silent: a broken store must not print an error into the user's prompt.
    """
    try:
        if kind == "config_keys":
            from px0 import config as config_mod

            return sorted(config_mod.SCHEMA)
        if home is None:
            from px0 import paths

            home = paths.store_home()
        if kind == "workflows":
            from px0 import workflow as workflow_mod

            return sorted(workflow_mod.load_all(home))
        if kind == "runs":
            from px0 import config as config_mod, paths, runs as runs_mod

            config = config_mod.load(paths.config_path(home))
            return [r["id"] for r in runs_mod.list_records(config)[:50]]
        if kind == "claims":
            from px0 import claims

            listing = getattr(claims, "list_claims", None)
            return sorted(listing(home)) if callable(listing) else []
    except Exception:
        return []
    return []


BASH = r"""# px0 completion for bash. Install with:
#   px0 completion bash > /usr/local/etc/bash_completion.d/px0
# or source it from ~/.bashrc:
#   eval "$(px0 completion bash)"
_px0_complete() {
    local IFS=$'\n'
    COMPREPLY=($(px0 --complete "${COMP_WORDS[@]:1}" 2>/dev/null))
}
complete -o default -F _px0_complete px0
"""

ZSH = r"""#compdef px0
# px0 completion for zsh. Install with:
#   px0 completion zsh > "${fpath[1]}/_px0"
# or source it from ~/.zshrc:
#   eval "$(px0 completion zsh)"
_px0() {
    local -a candidates
    candidates=(${(f)"$(px0 --complete ${words[2,$CURRENT]} 2>/dev/null)"})
    if (( ${#candidates} )); then
        compadd -- $candidates
    else
        _files
    fi
}
compdef _px0 px0
"""

FISH = r"""# px0 completion for fish. Install with:
#   px0 completion fish > ~/.config/fish/completions/px0.fish
function __px0_complete
    set -l tokens (commandline -opc) (commandline -ct)
    px0 --complete $tokens[2..-1] 2>/dev/null
end
complete -c px0 -f -a '(__px0_complete)'
"""


def script(shell: str) -> str:
    """The completion script for one shell."""
    scripts = {"bash": BASH, "zsh": ZSH, "fish": FISH}
    if shell not in scripts:
        raise ValueError(f"unsupported shell: {shell!r}; expected one of {list(SHELLS)}")
    return scripts[shell]
