# `px0 secrets`

Values a workflow may use without storing them in its file: an API token, an
internal hostname, a repository name you would rather not publish.

A workflow file is content. It is versioned, diffed, exported, and sometimes
written by a model, so a token does not belong in one. Secrets live beside the
connector credentials instead, and a workflow reaches them as `{{secrets.NAME}}`
in any templated value or in its body.

Implemented by `px0/secrets.py`, with redaction applied in `px0/runner.py`.

```
px0 secrets set <name> [value]
px0 secrets list [--json]
px0 secrets unset <name>
```

---

## `px0 secrets set`

Store a secret, replacing any earlier value.

### `name` (required)

What to call it.

- **Input:** uppercase letters, digits, and underscores, starting with a letter:
  `GITHUB_TOKEN`, `DEPLOY_URL`. Anything else is refused, so a placeholder always
  reads as a constant and can never be confused with an input id.

### `value`

The value itself.

- **Input:** any text.
- **Default:** omit it and px0 prompts, without echoing. Prefer this: a value
  typed as an argument lands in your shell history.

```shell
px0 secrets set GITHUB_TOKEN
px0 secrets set DEPLOY_URL https://internal.example.com/deploy
```

Then use it in a workflow:

```yaml
inputs:
  - id: builds
    tool: http.get
    args:
      url: "{{secrets.DEPLOY_URL}}/status"
      headers:
        Authorization: "Bearer {{secrets.GITHUB_TOKEN}}"
```

---

## `px0 secrets list`

Every secret name. Values are never printed.

- **Arguments:** none.
- `--json` prints `{"secrets": [...]}`.

```shell
px0 secrets list
```

---

## `px0 secrets unset`

Remove a secret.

### `name` (required)

- **Input:** the secret name.
- Removing something that is not there is not an error.
- Workflows whose files still mention the secret are named, since they will
  render an empty value from now on.

```shell
px0 secrets unset GITHUB_TOKEN
```

---

## What keeps a secret out of the record

Two things, both automatic:

- Every secret value is replaced with `[redacted]` in run records and run logs,
  including inside tool arguments, tool results, and the prompt sent to the
  model. A value shorter than four characters is left alone, since redacting
  `ab` would mangle unrelated text without protecting anything.
- `list` prints names only, and `set` prompts without echoing.

Secrets are stored in `.state/credentials.toml`, which `px0 store export` never
copies.

## Related configuration

None. Secrets are per-store and live with credentials, not in `config.toml`,
which is versioned and exported.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | Invalid name, or no value given |
