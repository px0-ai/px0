# 2. The store and configuration

Modules: `px0/paths.py`, `px0/store.py`, `px0/config.py`

Everything px0 knows lives in one directory. This part covers how that directory is located, scaffolded, configured, checked, and moved.

## Locating the store

`paths.store_home()` is the only place that answers "where is the store".

```python
def store_home() -> Path:
    return Path(os.environ.get("PX0_HOME", "~/.px0")).expanduser()
```

Every other path is a function of that one, and every such function takes an optional `home` so a caller working on a different store never falls back to the default. That parameter is load-bearing: `retrieval.brain_path` once ignored its `home` argument and hard-coded `~/.px0/brain`, so any caller whose config lacked `brain.path` silently read and wrote the wrong store.

`paths.display()` renders a path the way a person reads it, as `~/.px0/output/daily.md`. Absolute paths repeat the reader's home directory on every row; store-relative paths do not say which store they are relative to. The `~` form is both short and pasteable, and a path outside the home directory is left absolute rather than mangled.

## What `px0 init` creates

`store.init(home, harness_cmd)` scaffolds the directory tree, writes `config.toml` from `config.DEFAULTS`, stamps the on-disk schema version, and records the whole thing as the store's first versioned change.

Two details in that function are deliberate and easy to miss.

`brain/work/` is scaffolded even though nothing writes to it by default. Retrieval already treats that folder as never-leaves-this-machine, so it should exist to be filed into rather than being a folder the user has to guess at and create.

`tools/example.toml.sample` is written with a suffix the loader ignores. Scaffolding a live tool into every store would put something nobody asked for into `px0 tools list`; a `.sample` is a worked example you copy to make real.

px0 ships no workflows and no guidelines. `starters.WORKFLOWS` and `starters.GUIDELINES` are both empty dicts. What `starters` actually ships is sentences: `RECIPES` is a list of things people build, phrased the way you would say them during the interview. Picking one fills in the first answer, so the interview proceeds exactly as if you had typed it, and every workflow in a store is still something the user asked for.

## The configuration schema

`config.py` holds two structures that must agree.

`DEFAULTS` is a nested dict of every table and key with its default value. `SCHEMA` is a flat dict keyed by dotted name, giving each key its Python type, its allowed values where it has a closed set, and a help string.

`load()` reads `config.toml` with the standard library `tomllib` and deep-merges it over `DEFAULTS`, so a key missing on disk resolves to its default and a store written by an older px0 keeps working. `save()` uses a hand-rolled writer rather than a TOML library, because the schema is a fixed, shallow set of tables and a full round-trip dependency would buy nothing.

The writer has one subtlety worth copying:

```python
if isinstance(v, bool):  # must precede the int check: bool is a subclass of int
    return "true" if v else "false"
```

Floats are also handled explicitly. Without that branch a budget of `5.0` was written as the string `"5.0"` and read back as text, so every comparison against it compared a number with a string.

### Reading and writing keys

`get()` walks a dotted path and returns a default for anything missing. `get_key()` is the stricter version that rejects a key not in `SCHEMA`, which is what `px0 config get` uses so a typo reports itself.

`set_key()` validates and coerces before writing. `_coerce` turns command-line text into the key's real type: `true`/`false` for booleans, `int()` and `float()` with typed error messages, and comma-splitting for lists. That last one is not cosmetic. Before it existed, `px0 config set brain.ignore "*.a,*.b"` stored the raw string, which became one nonsense glob matching nothing.

`unset_key()` removes an override and returns the default the key now resolves to, dropping the parent table if it empties. Unsetting a key that was never set is not an error, because the result is the same either way.

### Where the schema surfaces

`config.key_help()` renders the key list as an aligned block for the `--help` epilog of `px0 config get/set/unset`, grouped by leading section so the shape of the TOML is visible. `config.describe()` returns the same keys with their current value, default, and help text, which is what `px0 config list` prints and what `--json` emits. Shell completion enumerates `SCHEMA` directly.

One structure, four surfaces. Adding a key means adding it to `DEFAULTS` and `SCHEMA`, and it appears everywhere.

### Keys that change behaviour elsewhere in this series

| Key | Effect | Covered in |
| --- | ------ | ---------- |
| `model.harness_cmd` | Which coding agent CLI runs the prompts | [7](07-harness.md) |
| `model.output_format` | Whether to ask for a structured envelope, which is what makes token counts real | [7](07-harness.md) |
| `model.agent_loop` | Whether px0 drives the tool calls or the harness does | [6](06-running.md) |
| `runs.max_tool_turns` | The ceiling on px0's own loop | [6](06-running.md) |
| `runs.capture_inputs` | Whether a run keeps what it read, for replay | [13](13-feedback.md) |
| `runs.disable_after_failures` | The circuit breaker threshold | [13](13-feedback.md) |
| `runs.daily_budget_usd` | Spend ceiling for unattended runs | [13](13-feedback.md) |
| `tools.allow_shell` | Whether `shell.run` exists at all | [12](12-trust.md) |
| `tools.confirm_writes` | Whether writes wait for a person | [12](12-trust.md) |
| `tools.file_roots` | Where the file tools may reach | [12](12-trust.md) |
| `brain.private_folder` | The folder retrieval never returns | [9](09-brain.md) |
| `schedule.timezone` | The clock schedules are read against | [11](11-daemon.md) |
| `approvals.reply_from` | Who may answer an approval by message | [12](12-trust.md) |

## Store consistency

`store.verify(home)` is a cheap, read-only, offline check of whether the store's contents still hang together. It parses every workflow, confirms every guideline a workflow references exists, checks that every version row in the manifest still has its blob on disk, and loads every user-declared tool file.

It is deliberately separate from `px0 doctor`. Doctor asks whether the install is wired up: is the harness responding, is the index fresh, is the daemon running. Verify asks whether the store's own contents are consistent. Those questions fail for different reasons and have different fixes.

Every problem carries a `fix` string naming what to run. A missing version blob is reported as history that cannot be read back, with an explicit note that the file on disk and its later versions are unaffected -- because that distinction is exactly what a person needs and cannot infer.

## Export and import

`store.export(home, dest)` copies content plus version history and excludes credentials. Getting that promise right takes more than skipping `.state/credentials.toml`.

The Composio API key is also written into `config.toml`, and `config.toml` is versioned, so the raw key sits in the history blobs as well. So export does three things: it copies `config.toml` with every key in `SECRET_CONFIG_KEYS` blanked, it deletes `config.toml`'s rows from the exported manifest, and it removes the blobs that no surviving version still references. Blobs are content-addressed and shared, so the last step is a set difference rather than a delete of everything the config ever pointed at.

`store.import_store` is the inverse, with three rules that keep it from being a footgun:

- An import into an existing store stops unless `--merge` or `--force` is given. The alternative is silently overwriting the workflows someone is running.
- `--merge` adds only what is missing. `--force` lets the import win on a collision.
- An imported `config.toml` never overwrites a live one, so importing does not blank the API key on the machine you are importing into.

For the ongoing case -- two machines you use every day -- export and import are the wrong tool, because they overwrite. See [part 16](16-sync.md).

## Schema versioning

`px0/__init__.py` holds `SCHEMA_VERSION`, currently `3`, and `.state/schema` holds what the store on disk was written for. `px0 update` applies every migration keyed above the store's version. Migrations are forward-only and are not undone by a rollback. See [part 18](18-release.md).

## Next

[Part 3](03-versioning.md) covers the version history that `export` was so careful about, and the undo log built on top of it.
