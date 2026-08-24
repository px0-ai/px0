# `px0 doctor`

Check that everything is wired up, and for anything that is not, print the exact
command that fixes it. A red line you cannot act on is the whole complaint
`doctor` exists to answer.

Implemented by `px0/doctor.py`.

```
px0 doctor [--quick] [--json]
```

---

## Options

### `--quick`

Skip the checks that need a live subprocess or a filesystem walk.

- **Input:** flag, no value. Default off.
- Use it in a hook or a prompt where the full pass would be too slow. This is the
  mode `px0 update` runs to confirm an upgrade.

### `--json`

Print the results as JSON, and nothing else.

- **Input:** flag, no value. Default off.
- Each check carries `ok`, `detail`, and — when it failed — `fix`.

```shell
px0 doctor
px0 doctor --quick
px0 doctor --json | jq -r '.checks | to_entries[] | select(.value.ok == false) | .key'
```

---

## The checks

| Check | What it confirms |
| ----- | ---------------- |
| `credentials` | `.state/credentials.toml` is mode 0600 — names the `chmod` and the path if not |
| `versions` | The version manifest opens and is consistent |
| `locks` | No stale process lock is left behind |
| `schema` | The store's schema version matches this binary's |
| `connections` | How many connector connections are configured |
| `workflows` | Every workflow file parses |
| `unreferenced_guidelines` | Guideline files no workflow inlines |
| `guideline_descriptions` | Guideline files with no `description` in their frontmatter, which a build cannot match a new workflow against |
| `update` | Whether a newer px0 is available |
| `daemon` | Whether the daemon is running |
| `harness` | The configured coding agent is on `PATH` and responds |
| `index` | qmd's version against the pinned one, and whether its local models have been consented to |
| `private_folder` | How much the private folder is holding back from retrieval |

`private_folder` is reported even when healthy, because the exclusion is
invisible in normal use: a brain pointed at a vault with its own `work/` folder
can have all of it quietly missing from every search. See
[`px0 brain`](brain.md#private-material).

## Output

One line per check. A failing line is followed by its fix, indented beneath it,
so what to run is next to what went wrong. A passing check gets no extra line.

## Exit codes

| Code | When |
| ---- | ---- |
| `0` | Every check passed |
| `4` | At least one check failed — the fix is printed under each failing line |
