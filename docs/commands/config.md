# `px0 config`

Read and write `config.toml` at the store root. Every key is validated against a
schema before it is saved, so a typo or a bad value is refused rather than
written.

Implemented by `px0/config.py`. The full key list is in
[Configuration keys](../reference/configuration.md).

```
px0 config list [--json]
px0 config get <KEY> [--json]
px0 config set <KEY> <VALUE>
px0 config unset <key>
px0 config edit
px0 config path
px0 config model
px0 config composio [key]
```

---

## `px0 config list`

Every key with its current value, its default, its type, and a one-line
description of what it does.

- **Arguments:** none.
- A value that differs from its default is accented and shows the default beside
  it, so what you have changed is visible at a glance.

### `--json`

Print the entries as JSON, and nothing else.

```shell
px0 config list
px0 config list --json | jq -r '.[].key'
```

---

## `px0 config get`

Print one key's current value.

### `KEY` (required)

- **Input:** a dotted key, for example `brain.path`. Unknown keys are refused
  with a pointer to `px0 config list`.

### `--json`

Print the value as JSON.

```shell
px0 config get brain.path
```

---

## `px0 config set`

Validate and save one key.

### `KEY` (required)

- **Input:** a dotted key.

### `VALUE` (required)

- **Input:** checked against the key's type and allowed values before saving.

| Type | How to write it |
| ---- | --------------- |
| string | as-is; quote it if it contains spaces |
| integer | digits — a non-integer is refused |
| boolean | `true` or `false` — nothing else is accepted |
| list | comma-separated: `"*.excalidraw.md,Templates/*"` |

```shell
px0 config set brain.path ~/Documents/MyVault
px0 config set retrieval.k_default 8
px0 config set versions.keep_all false
px0 config set brain.ignore "*.excalidraw.md,Templates/*"
```

Setting `brain.path` also reports what px0 found there — how many Markdown files,
how many were skipped as tool state, whether it looks like an Obsidian vault, and
whether anything will be held back by the private folder. See
[pointing the brain at a vault](brain.md#pointing-the-brain-at-an-existing-vault).

---

## `px0 config unset`

Drop a key's stored override so it falls back to its default.

### `key` (required)

Which key to clear.

- **Input:** a dotted key, as listed by `px0 config list`. `--help` lists them
  all.
- Unsetting a key that was never set is not an error: the result is the same
  either way. px0 prints the default the key now resolves to.
- The parent table is removed when it empties, so `config.toml` does not
  accumulate empty sections.

```shell
px0 config unset tools.allow_shell
px0 config unset brain.private_folder
```

---

## `px0 config edit`

Open `config.toml` in `$VISUAL`, `$EDITOR`, or the first of `nano`, `vim`, `vi`
that exists.

- **Arguments:** none.
- What you save is checked for parseability before px0 reports success. If it no
  longer parses, px0 says so and prints the `px0 versions revert` command that
  restores the last good version — `config.toml` is versioned like everything
  else.

```shell
px0 config edit
```

---

## `px0 config path`

Print where `config.toml` is.

- **Arguments:** none.
- `--json` prints it as an object.

```shell
px0 config path
$EDITOR "$(px0 config path)"
```

---

## `px0 config model`

Pick the coding-agent harness interactively, and write it to
`model.harness_cmd`.

- **Arguments:** none.
- Offers the harnesses px0 knows how to invoke — `claude`, `gemini`, `pi`,
  `opencode` — and expands your choice to its full non-interactive command. To
  use anything else, set `model.harness_cmd` directly.

```shell
px0 config model
px0 config set model.harness_cmd "my-agent --print"
```

---

## `px0 config composio`

Store the Composio API key, after verifying it against Composio.

### `key`

- **Input:** a Composio API key.
- **Default:** omit it and you are prompted, so the key does not land in your
  shell history.
- If the network intercepts TLS, px0 finds a CA bundle that trusts it, saves it
  to `connectors.ca_bundle`, and says so — every later outbound call uses it.

```shell
px0 config composio
px0 config composio ak_...
```

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Unknown key, wrong type, or a value outside the key's allowed set |
| `2` | Composio could not be reached, or rejected the key |
