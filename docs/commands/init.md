# `px0 init`

Scaffold a store: create the folder layout, write `config.toml`, install the
starter workflows and guidelines, and take the first version snapshot.

Implemented by `px0/store.py` (`store.init`), driven by `cli.cmd_init`.

```
px0 init [dir] [--harness {claude,gemini,opencode,pi}] [--composio-key KEY]
```

## Arguments

### `dir`

Optional. Where to create the store.

- **Input:** a filesystem path.
- **Default:** `$PX0_HOME` if set, else `~/.px0`.
- Created if it does not exist. Running against an existing store is safe: it
  fills in anything missing and leaves everything else alone.

```shell
px0 init                      # ~/.px0
px0 init ~/work/px0-store     # somewhere else
PX0_HOME=/tmp/scratch px0 init
```

## Options

### `--harness {claude,gemini,opencode,pi}`

Which coding-agent CLI to use as the model backend. Written to
`model.harness_cmd` in the generated `config.toml`.

- **Input:** one of `claude`, `gemini`, `opencode`, `pi`. Each expands to that
  agent's full non-interactive invocation.
- **Default:** `claude`, which expands to `claude -p`.
- To use something not on the list, set the full command afterwards with
  `px0 config set model.harness_cmd "<command>"`.

```shell
px0 init --harness gemini
```

### `--composio-key KEY`

Composio API key, used to authorize the external apps workflows call. Verified
against Composio before being saved.

- **Input:** a Composio API key string.
- **Default:** none. If omitted, `init` prompts for one interactively; you can
  skip the prompt and add it later with `px0 config composio`.
- Stored in `connectors.composio_api_key`. `px0 store export` redacts it, along
  with its version history.

```shell
px0 init --composio-key ak_...
```

## What it creates

```
<store>/
  workflows/          starter workflows
  guidelines/         starter guidelines
  brain/{docs,blogs,papers,work}/
  output/
  .state/{proposals,index,ingest}/
  .state/schema       the on-disk schema version
  config.toml
```

## Output

Lists what was created, then points at the next step. If Node.js is missing it
says so, because `px0 skills` needs `npx`. It closes by mentioning that
`brain.path` can point at an existing Markdown folder — see
[`px0 brain`](brain.md#pointing-the-brain-at-an-existing-vault).

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Store created or already complete |
| `1` | The path is unusable, or a supplied Composio key was rejected |
