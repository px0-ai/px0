# Supported Harnesses

These are the built-in presets. Each runs non-interactively and applies edits
without asking. The command is what `Resolve` builds; `<model>` is replaced by
the chosen model and `<prompt>` by the prompt passed to `Run`. When no model is
chosen, the model flag is left out and the harness uses its own configured model.

| Name           | Binary         | Command                                                                  |
| -------------- | -------------- | ------------------------------------------------------------------------ |
| `claude`       | `claude`       | `claude --permission-mode acceptEdits --model <model> -p <prompt>`       |
| `cursor-agent` | `cursor-agent` | `cursor-agent --force --model <model> -p <prompt>`                       |
| `agy`          | `agy`          | `agy --dangerously-skip-permissions --mode accept-edits --model <model> -p <prompt>` |
| `opencode`     | `opencode`     | `opencode run -m <model> <prompt>`                                       |
| `codex`        | `codex`        | `codex exec --sandbox workspace-write --skip-git-repo-check -m <model> -- <prompt>` |

Every one of these can list the models it offers, using its own command, so
`DiscoverModels` works for all of them. A harness is supported only if it can.

## Notes

- A harness counts as installed when its binary is on `PATH` or in a common
  installer folder (see `BinDirs`). Detection never runs it.
- `agy` is run with `--dangerously-skip-permissions`. Use it only on code you
  are willing to let an agent modify.
- The package keeps no model names, not even defaults, because they go out of
  date as vendors release models. Pick one from `DiscoverModels`, or leave the
  model empty to use the harness's own setting.

- `codex` runs `codex exec` with the `workspace-write` sandbox, so it can edit
  files in the workspace but nothing outside it. Models come from
  `codex debug models` (those Codex shows in its own picker).

## Any Other CLI

Use a command template with a `{prompt}` placeholder:

```go
cmd, err := harness.Resolve("mytool --task {prompt}", "")
```

See [usage](usage.md) for the template rules, and [how it works](design.md)
for adding a built-in preset.
