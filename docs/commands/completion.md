# `px0 completion`

Print a shell completion script.

px0 has just over a hundred command nodes, and most arguments are ids you would
otherwise have to remember: a workflow id, a guideline name, a config key, a run
id. The scripts are generated from the argparse tree, so a new verb is
completable the day it lands and a removed one stops being offered.

Implemented by `px0/completion.py`.

```
px0 completion {bash,zsh,fish}
```

---

## `shell` (required)

Which shell to print a script for.

- **Input:** `bash`, `zsh`, or `fish`.

### bash

```shell
eval "$(px0 completion bash)"                                   # this session
px0 completion bash > /usr/local/etc/bash_completion.d/px0      # every session
```

### zsh

```shell
eval "$(px0 completion zsh)"              # this session
px0 completion zsh > "${fpath[1]}/_px0"   # every session
```

### fish

```shell
px0 completion fish > ~/.config/fish/completions/px0.fish
```

## What gets completed

- Every group and verb, from the parser itself. Flags hidden from `--help` stay
  hidden here too.
- Workflow ids, for the commands that take one.
- Config keys, for `px0 config get|set|unset`.
- Run ids, for the commands that take one, newest 50.

Each script calls back into `px0 --complete`, which keeps the shell side small
and lets the dynamic values come from the store.

## `px0 --complete` (internal)

Hidden from `--help`, and not intended to be typed. Takes a partly typed command
line and prints one candidate per line. It never prints an error: completion runs
on every tab press, so a broken store must produce nothing rather than noise in
the middle of your prompt.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | An unsupported shell was named |
