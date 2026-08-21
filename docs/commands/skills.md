# `px0 skills`

Skills are agent skill bundles — a `SKILL.md` and its supporting files — that a
coding agent can load. px0 both compiles your guidelines into bundles and proxies
the community `skills` utility for discovering and installing others.

Implemented by `px0/skills.py`.

```
px0 skills build
px0 skills <anything else>
```

---

## `px0 skills build`

Compile the store's guidelines into skill bundles under `skills/`.

- **Arguments:** none.
- Handled locally, not proxied.
- Output is derived: it is rebuilt from `guidelines/`, so edit the guidelines and
  build again rather than editing a bundle.

```shell
px0 skills build
```

---

## `px0 skills <anything else>`

Every other argument list is passed straight through to `npx skills`, so the
community utility's own verbs work unchanged — discovering, installing, listing,
updating, and removing skills.

### `skills_args`

- **Input:** all remaining arguments, forwarded verbatim.
- Requires Node.js on `PATH`. `px0 init` warns when `npx` is missing, and
  `px0 doctor` reports it.

```shell
px0 skills list
px0 skills search commit
px0 skills install some-skill
```

Because arguments are forwarded as given, consult `npx skills --help` for that
tool's surface — px0 does not wrap or re-document it.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Success |
| `1` | `npx` is not available, or the proxied command failed |

A proxied command's own exit code is passed through where it sets one.
