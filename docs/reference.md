# px0 API reference

Generated from docstrings by `scripts/gen_docs.py`. Do not edit by hand.

## `px0.ask`

px0 ask: retrieval plus generation over knowledge/, nothing else. Never
touches connectors or guidelines.

### `class AskError`

Raised when ask() cannot answer: empty/missing index or no matching passages.

### `ask`

```python
def ask(home: Path, config: dict, question: str, k: int = 5) -> dict
```

Retrieves the top-k passages from knowledge/, asks the harness to
answer using only those passages, and records the exchange as a run.

Raises AskError if the index is empty/missing or nothing matches.
Returns {"answer", "passages", "run_id"}.

## `px0.builder`

px0 new: turn a sentence into a working workflow. Pure planning/
generation functions live here; the interactive plan/confirm/connect/
generate loop lives in the CLI, which is where user prompts belong.

### `class BuilderError`

Raised when a workflow plan can't be generated or parsed from the harness response.

### `class Plan`

A workflow plan produced by the harness: trigger, inputs, tools, output shape,
and the instruction body, plus the raw JSON the model returned.

### `generate_plan`

```python
def generate_plan(config: dict, description: str) -> Plan
```

Asks the harness to turn a natural-language request into a JSON workflow plan.
Raises BuilderError if the harness response has no JSON object or the JSON is malformed.

### `check_feasibility`

```python
def check_feasibility(plan: Plan, home: Path) -> list[str]
```

Validates a plan against reality: unknown tool ids, write tools used as inputs
(inputs must be read-only), and an invalid cron schedule. Returns a list of
human-readable issue strings; empty means the plan can proceed.

### `required_connections`

```python
def required_connections(plan: Plan) -> set[str]
```

Returns the set of provider names (e.g. "github") the plan's inputs and tools touch.

### `write_tools_named`

```python
def write_tools_named(plan: Plan) -> list[str]
```

Returns the subset of plan.tools that are write tools, so the CLI can warn the user
before granting them.

### `choose_guidelines`

```python
def choose_guidelines(home: Path, description: str, top_n: int = 3) -> list[str]
```

Match the task against topic files present in the store by simple
keyword overlap between the description and each file's headings.

### `render_workflow_file`

```python
def render_workflow_file(workflow_id: str, plan: Plan, guidelines: list[str]) -> str
```

Renders a Plan into the workflow file's text: YAML frontmatter followed by the
instruction body, in the same `---\nfrontmatter\n---\nbody` shape workflow.py parses.

### `save_workflow`

```python
def save_workflow(home: Path, workflow_id: str, content: str) -> Path
```

Writes a new workflow file to workflows/ and records it as a versioned change.
Overwrites any existing file at the same id.

## `px0.claims`

Guideline claims: `<path>#<heading-slug>` addressing, section-level
history, and rename aliasing by body similarity.

### `slugify`

```python
def slugify(heading: str) -> str
```

Converts a Markdown heading into the URL/id-safe slug used to address it as a claim.

### `class Section`

One `##`-or-deeper section of a guideline file: its heading, slug, line range
within the file, and raw lines (heading line included).

#### `text`

```python
def text(self) -> str
```

The section's full text, heading line included.

#### `body`

```python
def body(self) -> str
```

The section's text with the heading line stripped.

### `extract_sections`

```python
def extract_sections(content: str) -> list[Section]
```

Splits a guideline file's content into sections, one per heading, each
running up to the next heading (or end of file).

### `_normalize_tokens`

```python
def _normalize_tokens(text: str) -> set[str]
```

Lowercases text, unwraps inline code spans, and splits into a set of word tokens.

### `jaccard_similarity`

```python
def jaccard_similarity(a: str, b: str) -> float
```

Token-level Jaccard similarity between two texts. Two empty texts are
treated as identical (1.0); one empty and one non-empty are unrelated (0.0).

### `detect_renames`

```python
def detect_renames(old_content: str, new_content: str) -> list[tuple[str, str]]
```

Compare two versions of one guideline file's sections. Returns
(old_slug, new_slug) pairs whose bodies are similar enough (>= 0.7
token-level Jaccard) to be recorded as a rename rather than a
deletion plus a new claim.

### `add_alias`

```python
def add_alias(home: Path, old_claim: str, new_claim: str) -> None
```

Records (or updates) that old_claim now resolves to new_claim.

### `remove_alias`

```python
def remove_alias(home: Path, old_claim: str) -> None
```

Deletes an alias mapping; no-op if it doesn't exist.

### `list_aliases`

```python
def list_aliases(home: Path) -> list[dict]
```

Returns every alias mapping, sorted by old_claim.

### `resolve_claim`

```python
def resolve_claim(home: Path, claim_id: str, _seen: set | None = None) -> str
```

Follow the alias chain forward to the current claim id.

### `lineage_slugs`

```python
def lineage_slugs(home: Path, path: str, claim_id: str) -> set[str]
```

All slugs (past and present) belonging to this claim's identity,
by walking the alias graph in both directions.

### `process_change_for_renames`

```python
def process_change_for_renames(home: Path, change_id: str | None) -> None
```

For each guideline file touched by a change, diffs it against its previous
version and records any detected section renames as aliases. No-op for a
None change_id (nothing was actually recorded).

### `scan_and_process`

```python
def scan_and_process(home: Path, actor: str = 'user:manual', force_hash: bool = False) -> str | None
```

The checkpoint scan plus rename detection over what it captured.

### `capture_guideline_change`

```python
def capture_guideline_change(home: Path, actor: str, file_changes: list[versioning.FileChange]) -> str | None
```

Records a guideline edit as a version and immediately runs rename detection
on it, so aliases stay in sync with every guideline write path.

### `guidelines_log`

```python
def guidelines_log(home: Path, claim_id: str) -> list[dict]
```

Returns the version history of one claim, following its alias lineage so
a section that was renamed still shows its full history under either name.

### `guidelines_revert`

```python
def guidelines_revert(home: Path, claim_id: str, to_version: int, actor: str) -> str | None
```

Restores one claim's section text from an earlier file version, splicing it
back into the current file content (replacing the section if it's still present
under any alias, appending it otherwise) and recording the result as a new change.
Raises ValueError if the target version has no content or lacks this claim.

## `px0.cli`

px0's CLI surface. Argument parsing and interactive glue live here;
every subcommand delegates to the module that actually does the work.

### `_ctx`

```python
def _ctx(require_init: bool = True) -> tuple[Path, dict]
```

Resolves the store home and loads its config for a subcommand.

Exits the process with EXIT_USER_ERROR if the store hasn't been
initialized and require_init is True.

### `_parse_since`

```python
def _parse_since(text: str) -> datetime
```

Parses a `--since` value like "7d" into an absolute datetime that many days ago.

### `_dump`

```python
def _dump(args: argparse.Namespace, data) -> None
```

Prints data to stdout as indented JSON, coercing non-JSON-serializable values via str().

### `cmd_init`

```python
def cmd_init(args: argparse.Namespace) -> None
```

Handles `px0 init`: scaffolds a new store and prints suggested next commands.

### `cmd_new`

```python
def cmd_new(args: argparse.Namespace) -> None
```

Handles `px0 new`: generates a workflow plan from a natural-language description,
checks feasibility and required connections, then writes the workflow file after
interactive confirmation (unless --yes is passed).

### `cmd_run`

```python
def cmd_run(args: argparse.Namespace) -> None
```

Handles `px0 run`: executes a workflow with inputs collected from --stdin and
--input KEY=VALUE flags, then prints the outcome and, depending on --json/--quiet
and the workflow's output target, the run's output text.

### `cmd_ask`

```python
def cmd_ask(args: argparse.Namespace) -> None
```

Handles `px0 ask`: answers a question via retrieval over guidelines/knowledge
and prints the answer, optionally followed by source passages with --sources.

### `cmd_list`

```python
def cmd_list(args: argparse.Namespace) -> None
```

Handles `px0 list`: prints workflows, guidelines, and/or knowledge file paths.
With no kind given, prints all three sections; otherwise prints just that section.

### `cmd_connect`

```python
def cmd_connect(args: argparse.Namespace) -> None
```

Handles `px0 connect` and its sub-targets: setup-composio, list, remove, rotate,
and connecting a new service (native github only in this build; anything else
reports Composio auth-link creation as unimplemented).

### `cmd_tools`

```python
def cmd_tools(args: argparse.Namespace) -> None
```

Handles `px0 tools list`: prints each available tool with a read/write marker,
its id, provider, description, and parameters, optionally filtered by service.

### `cmd_daemon`

```python
def cmd_daemon(args: argparse.Namespace) -> None
```

Handles `px0 daemon` subcommands: install, status, start, stop, restart, logs,
serve. start/restart spawn `python -m px0.cli daemon serve` as a detached child
process with PX0_HOME set; stop/restart send SIGTERM to the recorded pid.

### `cmd_runs`

```python
def cmd_runs(args: argparse.Namespace) -> None
```

Handles `px0 runs` subcommands: list, show, output, rerun, logs -- inspecting
and replaying past workflow run records.

### `cmd_knowledge`

```python
def cmd_knowledge(args: argparse.Namespace) -> None
```

Handles `px0 knowledge add` and `refresh`: ingests a source (URL, file, etc.)
into the knowledge library or re-fetches an already-ingested source.

### `_interactive_review`

```python
def _interactive_review(home: Path, proposal_list: list, non_interactive: bool) -> None
```

Walks the user through each pending proposal, prompting accept/edit/dismiss
unless non_interactive is set (in which case proposals are only printed, not
acted on). Accepted/edited proposals are applied together as one change.

### `cmd_guidelines`

```python
def cmd_guidelines(args: argparse.Namespace) -> None
```

Handles `px0 guidelines` subcommands: review (pending proposals), log (claim
history), revert (roll a claim back to an earlier version), and alias
(list/link/unlink claim aliases).

### `cmd_consolidate`

```python
def cmd_consolidate(args: argparse.Namespace) -> None
```

Handles `px0 consolidate`: builds a consolidation session (pending proposals,
decayed claims, contradictions, unreferenced guideline files), prints a summary,
then runs the same interactive review flow as `guidelines review`.

### `_parse_version_ref`

```python
def _parse_version_ref(ref: str) -> tuple[str, int]
```

Splits a `<path>@v<N>` reference into (path, version number).

### `cmd_versions`

```python
def cmd_versions(args: argparse.Namespace) -> None
```

Handles `px0 versions` subcommands: list, show, diff, revert, prune -- the
per-file version history maintained by the tool's own versioning system.

### `cmd_changes`

```python
def cmd_changes(args: argparse.Namespace) -> None
```

Handles `px0 changes` subcommands: list, show, revert -- multi-file changesets
(as opposed to `versions`, which tracks a single file's history).

### `cmd_search`

```python
def cmd_search(args: argparse.Namespace) -> None
```

Handles `px0 search`: rebuilds the retrieval index when the query is literally
"reindex", otherwise retrieves and prints the top-k matching passages.

### `cmd_skills`

```python
def cmd_skills(args: argparse.Namespace) -> None
```

Handles `px0 skills build`: builds the skills/ output directory and prints
each file written.

### `cmd_why`

```python
def cmd_why(args: argparse.Namespace) -> None
```

Handles `px0 why <target_id>`: prints the provenance chain explaining how a
claim, proposal, or other tracked entity came to be.

### `cmd_store`

```python
def cmd_store(args: argparse.Namespace) -> None
```

Handles `px0 store export <dir>`: copies store content and version history to
another directory, excluding credentials.

### `cmd_config`

```python
def cmd_config(args: argparse.Namespace) -> None
```

Handles `px0 config` subcommands: list (every recognized key with its
current value, default, type, and allowed choices), get <key>, set <key>
<value>, and model (an interactive harness/model picker, see
_select_model).

### `_select_model`

```python
def _select_model(home: Path, config: dict) -> None
```

Interactive `px0 config model`: lists known harnesses with their PATH
status, lets the user pick one (or type a custom command) and an
optional model name, then verifies the resulting harness_cmd actually
responds before saving -- surfacing that CLI's own auth error (with a
hint from harness.AUTH_HINTS) rather than guessing why it failed.

px0 has no direct-API backend: it never asks for or stores a provider
API key itself. Authentication is entirely the chosen harness's own
(an env var it reads, or its own interactive login), same as every
other px0-invoked run of it.

### `cmd_update`

```python
def cmd_update(args: argparse.Namespace) -> None
```

Handles `px0 update`: switches the update channel, checks for/applies an
update, or reports that rollback is unavailable in this build.

### `cmd_version`

```python
def cmd_version(args: argparse.Namespace) -> None
```

Handles `px0 version`: prints version/build info. Works even without an
initialized store (require_init=False).

### `cmd_doctor`

```python
def cmd_doctor(args: argparse.Namespace) -> None
```

Handles `px0 doctor`: runs integrity/health checks and prints pass/fail per
check. Exits with EXIT_INTEGRITY_ERROR if any check failed.

### `build_parser`

```python
def build_parser() -> argparse.ArgumentParser
```

Builds the full px0 argparse tree: one subparser per top-level command, each
wiring its own flags and a `func` default that cmd dispatches to in main().

### `main`

```python
def main(argv: list[str] | None = None) -> None
```

CLI entry point: parses args, dispatches to the selected subcommand's handler,
and translates known exception types into the appropriate exit code.

## `px0.config`

config.toml read/write. Read via stdlib tomllib; write with a small
hand-rolled writer since the schema is a fixed, shallow set of tables.

### `_toml_value`

```python
def _toml_value(v: Any) -> str
```

Formats a Python value as a TOML scalar literal: bool, int, list
(recursively), or a quoted/escaped string for anything else.

### `dumps`

```python
def dumps(config: dict[str, Any]) -> str
```

Serializes a config dict to TOML text: root-level scalars first,
then each top-level table (recursing into nested dicts as dotted
`[a.b]` table headers). Assumes a shallow, well-formed structure --
not a general-purpose TOML writer.

### `_deep_merge`

```python
def _deep_merge(base: dict[str, Any], overlay: dict[str, Any]) -> dict[str, Any]
```

Recursively merges overlay onto base, returning a new dict; overlay
values win. Nested dicts are merged key-by-key rather than replaced
outright, but a non-dict overlay value replaces the base value as-is.

### `load`

```python
def load(path: Path) -> dict[str, Any]
```

Loads config from `path`, deep-merged on top of DEFAULTS so any keys
missing on disk fall back to their default values. Returns a fresh
copy of DEFAULTS if `path` doesn't exist yet.

### `save`

```python
def save(path: Path, config: dict[str, Any]) -> None
```

Writes `config` to `path` as TOML, overwriting any existing file.

### `get`

```python
def get(config: dict[str, Any], dotted_key: str, default: Any = None) -> Any
```

Looks up a dotted config key (e.g. "logs.path"), returning `default`
if any segment along the path is missing or not a dict.

### `_coerce`

```python
def _coerce(key: str, raw: str) -> Any
```

Validates `key` against SCHEMA and converts the raw string `raw` (as
typed on a command line) into the key's real type, checking choices
where the key restricts them. Raises ValueError with a message meant to
be printed as-is on a bad key, type, or choice.

### `get_key`

```python
def get_key(config: dict[str, Any], key: str) -> Any
```

Looks up a SCHEMA-known dotted key. Raises ValueError for a key not
in SCHEMA, unlike the more permissive `get`.

### `set_key`

```python
def set_key(config: dict[str, Any], key: str, raw: str) -> Any
```

Validates and coerces `raw` per SCHEMA, then writes it into `config`
at `key` (mutating the nested tables in place) and returns the coerced
value. Does not save to disk -- callers persist via `save`.

### `describe`

```python
def describe(config: dict[str, Any]) -> list[dict[str, Any]]
```

Returns one entry per SCHEMA key: its current value, default, type
name, allowed choices (or None), and help text. Used by `px0 config
list`.

## `px0.connect`

px0 connect: creating and managing connections.

The native GitHub PAT path is fully wired (verifies the token against the
GitHub API before storing it). `setup-composio` stores the API key; actually
creating a Composio-hosted auth link is not implemented in this build (see
tools.py) so `connect <app>` without --native says so plainly rather than
faking a flow.

### `setup_composio`

```python
def setup_composio(home: Path, api_key: str) -> None
```

Stores the Composio API key as a credential. Does not validate the key.

### `connect_github_native`

```python
def connect_github_native(home: Path, token: str) -> dict
```

Verifies a GitHub PAT against the GitHub API and stores it on success.

Raises ValueError if GitHub rejects the token. Returns the resolved login.

### `rotate_github`

```python
def rotate_github(home: Path, token: str) -> dict
```

Replaces the stored GitHub token; rotation is just a re-verify-and-store.

### `list_connections`

```python
def list_connections(home: Path) -> list[dict]
```

Returns one summary dict per configured connection (service, kind, login, expiry).

### `remove_connection`

```python
def remove_connection(home: Path, service: str) -> bool
```

Deletes a stored connection. Returns False if the service wasn't configured.

## `px0.consolidate`

`consolidate` / `px0 guidelines review`: the one capped review session
over everything pending. Presents new proposals ranked by repetition,
claims due for decay, contradiction pairs, and guideline files no
workflow references. The session closes as one change.

### `build_session`

```python
def build_session(home: Path, config: dict, decay_days: int = 180) -> dict
```

Assembles one consolidation session: proposals ranked by how often
their target file recurs, claims stale past decay_days, contradiction
pairs, and guideline files no workflow references.

Caps proposals at config's proposals.max_per_consolidation; the rest are
reported as overflow rather than dropped.

## `px0.credentials`

Local credential storage: `.state/credentials.toml`, mode 0600, plain text
by design (see spec's Security posture).

### `load`

```python
def load(home: Path) -> dict
```

Reads all stored credentials keyed by service. Returns {} if the file
is missing or empty (fresh store, nothing connected yet).

### `save`

```python
def save(home: Path, creds: dict) -> None
```

Writes the full credentials dict and re-asserts mode 0600 on the file.

### `set_service`

```python
def set_service(home: Path, service: str, values: dict) -> None
```

Stores/overwrites credentials for one service, leaving the rest untouched.

### `remove_service`

```python
def remove_service(home: Path, service: str) -> bool
```

Deletes a service's stored credentials. Returns False if it wasn't present.

## `px0.daemon`

px0d: the scheduler. Deliberately dumb -- it watches workflows/,
evaluates cron schedules in machine local time, spawns `px0 run <id>
--quiet`, recovers missed fires, and runs the nightly housekeeping pass.

Missed-fire detection here is a practical approximation: the same
poll-and-compare-to-last-fire logic runs on every tick and at startup. A
fire discovered more than LATE_THRESHOLD_SECONDS after it was due is
recorded as late (the machine was asleep or the daemon was down); a fire
discovered within that window is an ordinary on-time fire. There is no
separate OS sleep/wake hook, since none is available portably from plain
Python.

### `pidfile_path`

```python
def pidfile_path(home: Path) -> Path
```

Path to the file holding the running daemon's pid.

### `load_schedule_state`

```python
def load_schedule_state(home: Path) -> dict
```

Loads the last-fire-time-per-workflow-id map, or {} if none recorded yet.

### `save_schedule_state`

```python
def save_schedule_state(home: Path, state: dict) -> None
```

Persists the last-fire-time-per-workflow-id map.

### `_due_fires`

```python
def _due_fires(schedule: str, last_fire: datetime | None, now: datetime) -> list[datetime]
```

Returns every cron fire time for `schedule` between the later of last_fire
or today's midnight, and now. With no last_fire, starts one second before
midnight so a fire exactly at midnight is still included.

### `tick`

```python
def tick(home: Path, config: dict, state: dict) -> dict
```

Check every scheduled workflow once; spawn `px0 run` for anything
due. Returns the updated schedule state.

### `spawn_run`

```python
def spawn_run(home: Path, workflow_id: str, late: bool, fire_time: datetime) -> None
```

Launches `px0 run <workflow_id> --quiet` as a detached subprocess, passing
--late-scheduled-at when the fire was recovered rather than on-time.

### `recover_missed_fires`

```python
def recover_missed_fires(home: Path, config: dict) -> None
```

On start or wake: catch up fires from today only.

### `run_nightly`

```python
def run_nightly(home: Path, config: dict) -> dict
```

Runs the once-a-day housekeeping pass: hand-edit checkpoint scan, knowledge
reindex, and run-log retention. Reindex failures are captured in the report
rather than raised, so one broken index doesn't block the rest of housekeeping.

### `serve`

```python
def serve(home: Path, config: dict, poll_interval: float = POLL_INTERVAL_SECONDS) -> None
```

Runs the daemon's main loop until SIGTERM/SIGINT: writes a pidfile, recovers
missed fires from earlier today, then polls every poll_interval seconds,
ticking the schedule and running the nightly pass once per calendar day.

### `status`

```python
def status(home: Path, config: dict) -> dict
```

Reports whether the daemon is alive (by signaling its pid with signal 0),
plus the last recorded fire per workflow and each scheduled workflow's next
upcoming fire time.

### `detect_platform`

```python
def detect_platform() -> str
```

Picks the scheduling mechanism for this OS: launchd on macOS, systemd on
Linux when a user session bus is available, cron as the fallback everywhere else.

### `systemd_unit`

```python
def systemd_unit(home: Path, px0_bin: str) -> str
```

Renders a systemd user-service unit file that runs `px0 daemon serve`.

### `launchd_plist`

```python
def launchd_plist(home: Path, px0_bin: str) -> str
```

Renders a launchd plist that runs `px0 daemon serve` at load and keeps it alive.

### `crontab_block`

```python
def crontab_block(home: Path, px0_bin: str) -> str
```

Renders one crontab line per scheduled, non-pipeline workflow, for the
cron fallback path which has no long-running daemon process.

### `install`

```python
def install(home: Path, fallback_cron: bool = False) -> dict
```

Writes the platform-appropriate scheduler unit (systemd/launchd) or, on cron
fallback, only renders the crontab block without writing anything (the caller
installs it with `crontab -e`). Returns platform, path written (if any), the
rendered content, and a human hint for how to start it.

## `px0.doctor`

px0 doctor: credentials, daemon, harness, index, versions, locks, schema.

### `_check_credentials`

```python
def _check_credentials(home: Path) -> dict
```

Verifies credentials.toml is mode 0600 (or absent, which is also fine).

### `_check_daemon`

```python
def _check_daemon(home: Path, config: dict) -> dict
```

Reports daemon status. Always ok: a stopped daemon isn't an integrity failure.

### `_check_harness`

```python
def _check_harness(config: dict) -> dict
```

Sends a trivial prompt to the model backend to confirm it responds.

### `_check_index`

```python
def _check_index(home: Path, config: dict) -> dict
```

Flags a stale retrieval index: knowledge files exist but nothing is indexed.

### `_check_versions`

```python
def _check_versions(home: Path) -> dict
```

Confirms the version manifest database opens and is queryable.

### `_check_locks`

```python
def _check_locks(home: Path) -> dict
```

Checks the store lock is currently free, i.e. no run is stuck holding it.

### `_check_schema`

```python
def _check_schema(home: Path) -> dict
```

Confirms the store's on-disk schema version matches this binary's SCHEMA_VERSION.

### `_check_connections`

```python
def _check_connections(home: Path) -> dict
```

Reports configured connections. Always ok: having zero is a valid state.

### `_check_unreferenced_guidelines`

```python
def _check_unreferenced_guidelines(home: Path) -> dict
```

Flags guideline files that no workflow lists, since they're inlined into
prompts by reference and are otherwise dead weight.

### `run`

```python
def run(home: Path, config: dict, quick: bool = False) -> dict
```

Runs all health checks and returns a report with per-check results plus an
overall all_ok flag. quick=True skips the slower checks (daemon, harness,
retrieval index) that need a live subprocess or filesystem walk.

## `px0.harness`

The model backend: the user's coding agent CLI, shelled out to in
non-interactive mode (`harness_cmd` in config.toml, e.g. `claude -p`).
Text and tool-calls in, text out -- there is no direct-API backend.

### `class HarnessError`

Raised when the harness command is missing, times out, or exits non-zero.

### `installed_harnesses`

```python
def installed_harnesses() -> dict[str, bool]
```

Reports, for each name in KNOWN_HARNESSES, whether its binary is
found on PATH right now.

### `with_model`

```python
def with_model(harness_cmd: str, model: str | None) -> str
```

Appends a `--model <name>` flag to a harness command. All four known
harnesses accept `--model` for non-interactive model selection (verified
against each CLI's own docs); a custom command gets the same flag
appended on the same convention. Returns `harness_cmd` unchanged if
`model` is falsy.

### `resolve_harness_cmd`

```python
def resolve_harness_cmd(value: str) -> str
```

Expands a known harness name (e.g. "gemini") to its full invocation
command. A value that isn't a known name is returned unchanged, since
`model.harness_cmd` also accepts an arbitrary literal command.

### `parse_duration`

```python
def parse_duration(s: str) -> float
```

Parses a duration string with an optional ms/s/m/h suffix into seconds.
No suffix is treated as seconds.

### `invoke`

```python
def invoke(config: dict, prompt: str, timeout: float = 120) -> str
```

Shells out to the configured harness command (e.g. `claude -p`) with
the prompt as its final argument and returns stdout.

Raises HarnessError if the binary is missing, the call times out, or it
exits non-zero.

## `px0.knowledge`

px0 knowledge add: filing outside material into the library.

Ingestion is text only. Extraction always runs locally: web pages via
requests + BeautifulSoup, PDFs via `pdftotext`, documents via `pandoc`,
YouTube via `youtube-transcript-api` (no API key needed) with an oEmbed
metadata fallback when no transcript is published.

### `class IngestError`

A knowledge source could not be ingested (unrecognized, extraction tool
missing, extraction failed, etc.).

### `class IngestResult`

Where an ingested (or refreshed) source landed, and whether it's still
a stub awaiting a transcript.

### `read_header`

```python
def read_header(path: Path) -> tuple[dict, str]
```

Splits a knowledge file into its YAML frontmatter dict and body text.
Returns ({}, full_text) if the file has no frontmatter block.

### `write_file`

```python
def write_file(dest: Path, header: dict, body: str) -> None
```

Writes a knowledge file as YAML frontmatter followed by the body text,
creating parent directories as needed.

### `_slug_from_source`

```python
def _slug_from_source(source: str) -> str
```

Turns a URL or file path into a filesystem-safe filename stem, capped at 80 chars.

### `_detect_kind`

```python
def _detect_kind(source: str) -> tuple[str, str]
```

Returns (kind, routed folder).

### `_extract_web`

```python
def _extract_web(url: str) -> tuple[str, str]
```

Fetches a web page and extracts its readable text: strips script/style/nav/
footer/header/aside, prefers <article> or <main> if present. Returns (title, text).

### `_extract_pdf`

```python
def _extract_pdf(path: Path) -> str
```

Extracts text from a PDF via the `pdftotext` CLI. Raises IngestError if the
tool is missing or exits non-zero.

### `_extract_document`

```python
def _extract_document(path: Path) -> str
```

Extracts plain text from a document (.docx/.doc/.odt) via the `pandoc` CLI.
Raises IngestError if the tool is missing or exits non-zero.

### `_youtube_id`

```python
def _youtube_id(url: str) -> str
```

Extracts the 11-char video id from a youtube.com or youtu.be URL.

### `_youtube_oembed`

```python
def _youtube_oembed(url: str) -> dict
```

Fetches YouTube's public oEmbed metadata (title, author) for a video URL,
no API key required. Returns {} on any failure.

### `_extract_youtube`

```python
def _extract_youtube(url: str) -> tuple[str, str | None, dict]
```

Returns (title, transcript_text_or_None, metadata).

### `enumerate_playlist`

```python
def enumerate_playlist(url: str) -> list[str]
```

Scrapes a YouTube playlist page's HTML for video ids and returns their
watch URLs in playlist order, deduplicated.

### `_dest_path`

```python
def _dest_path(home: Path, config: dict, folder: str, source: str) -> Path
```

Resolves the destination path for an ingested source under knowledge/<folder>/.

### `add`

```python
def add(home: Path, config: dict, source: str, to: str | None = None, no_propose: bool = False) -> IngestResult
```

Ingests one source into the knowledge library: detects its kind, extracts
text (or queues a playlist for background processing), writes the knowledge
file, best-effort proposes guideline edits from it (unless no_propose), and
reindexes retrieval. A YouTube video with no published transcript is written
as a stub rather than failing.

### `refresh`

```python
def refresh(home: Path, config: dict, path: Path) -> IngestResult
```

Retries transcript extraction for a YouTube stub file; rewrites it in place
once a transcript is available. Raises IngestError if path isn't a stub or the
transcript still isn't published.

## `px0.paths`

Store location and path helpers.

### `store_home`

```python
def store_home() -> Path
```

Resolves the store root: `$PX0_HOME` if set, else `~/.px0`.

### `workflows_dir`

```python
def workflows_dir(home: Path | None = None) -> Path
```

Path to the versioned workflows folder under `home` (or the default store).

### `guidelines_dir`

```python
def guidelines_dir(home: Path | None = None) -> Path
```

Path to the versioned guidelines folder under `home` (or the default store).

### `outputs_dir`

```python
def outputs_dir(home: Path | None = None) -> Path
```

Path to the tool-managed outputs folder under `home` (or the default store).

### `skills_dir`

```python
def skills_dir(home: Path | None = None) -> Path
```

Path to the derived skills build output under `home` (or the default store).

### `state_dir`

```python
def state_dir(home: Path | None = None) -> Path
```

Path to `.state/`, the runtime-internal folder not meant for hand-editing.

### `versions_dir`

```python
def versions_dir(home: Path | None = None) -> Path
```

Path to the version history store under `.state/`.

### `proposals_dir`

```python
def proposals_dir(home: Path | None = None) -> Path
```

Path to pending guideline-edit proposals awaiting review.

### `index_dir`

```python
def index_dir(home: Path | None = None) -> Path
```

Path to the retrieval index over `knowledge/`.

### `ingest_dir`

```python
def ingest_dir(home: Path | None = None) -> Path
```

Path to the knowledge ingest queue/workspace.

### `credentials_path`

```python
def credentials_path(home: Path | None = None) -> Path
```

Path to `credentials.toml`, mode 0600, holding connector secrets.

### `lock_path`

```python
def lock_path(home: Path | None = None) -> Path
```

Path to the store's process lock file.

### `schema_path`

```python
def schema_path(home: Path | None = None) -> Path
```

Path to the file recording the store's on-disk schema version.

### `schedule_path`

```python
def schedule_path(home: Path | None = None) -> Path
```

Path to the daemon's persisted scheduling state.

### `config_path`

```python
def config_path(home: Path | None = None) -> Path
```

Path to the store's versioned `config.toml`.

## `px0.proposals`

Pending guideline edits. Ingestion, corrections, and verification never
touch guideline files directly -- each proposes a pending edit here, and
`consolidate` / `px0 guidelines review` is where the user disposes of them.
A proposal that is neither accepted nor edited is dismissed by deleting its
file; nothing is recorded about the dismissal.

### `class Proposal`

One pending guideline edit awaiting user review, with the evidence that
generated it (a knowledge source, or a manual correction).

### `_proposal_path`

```python
def _proposal_path(home: Path, proposal_id: str) -> Path
```

Path to a proposal's JSON file under .state/proposals/.

### `save_proposal`

```python
def save_proposal(home: Path, p: Proposal) -> None
```

Writes a proposal to disk as JSON.

### `list_proposals`

```python
def list_proposals(home: Path) -> list[Proposal]
```

Loads all pending proposals, skipping any file that fails to parse.

### `dismiss`

```python
def dismiss(home: Path, proposal_id: str) -> None
```

Deletes a proposal's file; no-op if it doesn't exist. Nothing is recorded
about a dismissal beyond the file's absence.

### `propose_from_knowledge`

```python
def propose_from_knowledge(home: Path, config: dict, knowledge_file: Path) -> list[Proposal]
```

Asks the harness to read one knowledge file and propose zero or more
guideline edits, saving each as a pending Proposal. Returns [] if the model
response has no JSON array (rather than raising).

### `_apply_proposal_to_content`

```python
def _apply_proposal_to_content(current: str, p: Proposal) -> str
```

Splices one accepted proposal into a guideline file's current content:
replaces the matching section for "amend", removes it for "retire" (no-op if
already absent), replaces it if a same-slug section exists, otherwise appends
a new section at the end.

### `apply_many`

```python
def apply_many(home: Path, actor: str, decisions: list[dict]) -> str | None
```

decisions: [{"proposal": Proposal, "edited_body": str|None}]. Batches
every accepted proposal into one change, grouped by target file.

### `unreferenced_guideline_files`

```python
def unreferenced_guideline_files(home: Path) -> list[str]
```

Guideline files that no workflow lists under its `guidelines:`, sorted.

### `decayed_claims`

```python
def decayed_claims(home: Path, decay_days: int = 180) -> list[dict]
```

Claims whose section has not changed in `decay_days`.

### `find_contradictions`

```python
def find_contradictions(config: dict, home: Path) -> list[dict]
```

Best-effort: asks the model backend to spot contradicting claims
across guideline files. Returns [] (with the caller told why) if the
harness is unavailable rather than fabricating a result.

## `px0.provenance`

px0 why: walk the chain for any run, answer, output, or claim.

### `class WhyError`

Raised when why() cannot resolve the given target id to a claim or run.

### `why`

```python
def why(home: Path, config: dict, target_id: str) -> dict
```

Resolves a target_id to its provenance: a claim id (containing '#')
returns its full edit history and current resolution, anything else is
looked up as a run id and returns that run's record.

Raises WhyError if the claim has no history or the run id doesn't exist.

## `px0.retrieval`

The `retrieve` interface over `knowledge/`: query, k, filters in;
ranked passages with file path and anchor out. Guidelines are never
retrieved by similarity -- only knowledge.

Backend: SQLite FTS5 with BM25 ranking, embedded, no server. This is a
pure-python-reachable subset of what the spec asks of qmd (hybrid keyword
+ vector search, rerank): only the keyword/BM25 half is implemented here,
since the vector and rerank stages need GGUF embedding models the spec
gates behind explicit, printed-size consent. `[retrieval] backend` names
this the "local" backend so a real qmd integration can be swapped in later
behind this same function signature.

### `class Passage`

One retrieved chunk: source file and heading anchor, text, BM25 score, and
provenance flags (when it was ingested, whether it's still a stub).

### `knowledge_path`

```python
def knowledge_path(home: Path, config: dict) -> Path
```

Resolves the configured knowledge/ directory, expanding ~.

### `index_db_path`

```python
def index_db_path(home: Path) -> Path
```

Path to the SQLite FTS5 index file backing retrieval.

### `_connect`

```python
def _connect(home: Path) -> sqlite3.Connection
```

Opens the index DB, creating the index directory and the FTS5 virtual table if needed.

### `_chunk_by_paragraph`

```python
def _chunk_by_paragraph(text: str) -> list[tuple[str, str]]
```

Fallback for material with no Markdown headings (extracted web
pages, transcripts): group paragraphs into ~1000-char chunks.

### `_chunk_file`

```python
def _chunk_file(text: str) -> list[tuple[str, str]]
```

Split a knowledge file's body into (anchor, text) chunks by heading,
falling back to paragraph grouping when there are no headings.

### `reindex`

```python
def reindex(home: Path, config: dict) -> int
```

Rebuilds the passage index from scratch: wipes the table, walks every knowledge/*.md
file, chunks it, and inserts each chunk as a row. Returns the number of passages indexed.

### `index_count`

```python
def index_count(home: Path) -> int
```

Number of indexed passages, or 0 if the index doesn't exist yet.

### `_fts_query`

```python
def _fts_query(query: str) -> str
```

Builds an FTS5 MATCH expression that OR-matches every word token in the query,
quoting each token so punctuation in the input can't break the query syntax.

### `retrieve`

```python
def retrieve(home: Path, config: dict, query: str, k: int = 5, local_only: bool = True) -> list[Passage]
```

Search knowledge/. `local_only=False` also returns knowledge/work/
passages; a run whose output destination or tool set is not local must
keep this True, per the work/ never-leaves-the-machine rule.

## `px0.runner`

px0 run: the eight-stage execution pipeline.

Tool-calling mid-run is a simplified stand-in for the spec's per-run MCP
endpoint: the model is told which tools it may call and asked to emit a
`TOOL_CALL: {...}` line to request one, since the harness invoked here is a
plain non-interactive subprocess rather than something wired to a real MCP
transport. Every call is still bound by the workflow's allowlist and
recorded in the run record exactly as the spec asks.

### `class RunError`

A run failed. Carries the partial run record (if any) so callers can
still write/report it before propagating the failure.

#### `__init__`

```python
def __init__(self, message: str, record: dict | None = None)
```

Stores the failure message and the partial run record, if any.

### `_now`

```python
def _now() -> datetime
```

Current time in UTC, used for all run timestamps.

### `_lookup`

```python
def _lookup(context: dict, dotted: str) -> Any
```

Resolves a dotted path like `input.foo` against a nested dict context.
Returns None if any segment is missing rather than raising.

### `render_value`

```python
def render_value(value: Any, context: dict) -> Any
```

Recursively resolves `{{dotted.path}}` template placeholders against context.
A string that is entirely one placeholder returns the looked-up value as-is
(preserving its type); a placeholder embedded in a larger string is
stringified in place. Lists and dicts are walked recursively; other types
pass through unchanged.

### `_with_retry`

```python
def _with_retry(config: dict, fn, *args, **kwargs)
```

Calls fn with exponential backoff on ConnectorError, up to
connectors.retries attempts. ConnectorNotConfigured is never retried --
it means the connector isn't set up, not that the call failed transiently.
Re-raises the last error if all attempts are exhausted.

### `resolve_inputs`

```python
def resolve_inputs(home: Path, config: dict, wf: workflow_mod.Workflow, cli_inputs: dict) -> tuple[dict, list[dict]]
```

Resolves every declared input of a workflow (tool call, retrieval query,
stdin source, or nested sub-workflow run) into a template context dict.
Returns (context, meta) where meta is a per-input list of resolution
outcomes for the run record. An optional input that fails resolves to None
and is marked degraded rather than aborting the run; a required input that
fails raises RunError.

### `render_prompt`

```python
def render_prompt(wf: workflow_mod.Workflow, guideline_texts: dict[str, str], context: dict) -> str
```

Builds the final prompt: renders the workflow body's templates against
context, then inlines guideline text either at an explicit `{{guidelines}}`
placeholder or, if none is present, prepended before the body.

### `_tool_call_loop`

```python
def _tool_call_loop(home: Path, config: dict, prompt: str, allowed_tools: list[str], dry_run: bool, timeout: float, run_id: str) -> tuple[str, list[dict]]
```

Drives the model through up to MAX_TOOL_TURNS turns, feeding it a
`TOOL_CALL: {...}` protocol line-by-line since the harness backend is a
plain non-interactive subprocess rather than a real MCP transport. Each
call is checked against the workflow's tool allowlist; write tools are
stubbed out (never executed) when dry_run is set. Returns the model's
final text output and the list of tool calls actually made, each recorded
for the run's audit trail.

### `route_output`

```python
def route_output(home: Path, output_spec: dict, text: str, note: str | None = None) -> dict
```

Writes the output where it belongs and returns a description of what
happened. Does not print: stdout routing is a decision for the CLI
layer, which also needs plain stdout free for `--json` output.
File writes are serialized with a store-wide lock to avoid two concurrent
runs racing on the same output path.

### `run`

```python
def run(home: Path, config: dict, workflow_id: str, trigger: str = 'manual', cli_inputs: dict | None = None, dry_run: bool = False, output_override: dict | None = None, late_scheduled_at: str | None = None) -> dict
```

Runs one workflow end to end through its eight stages: load/validate,
checkpoint hand edits under lock, resolve inputs, render the prompt,
run the model/tool-call loop, route the output, and write the run record.
A pipeline workflow (wf.pipeline set) is delegated to _run_pipeline instead.
Raises RunError on any stage failure, with a run record already persisted
describing the failure. Returns the completed run record on success.

### `_run_pipeline`

```python
def _run_pipeline(home: Path, config: dict, wf: workflow_mod.Workflow, trigger: str, dry_run: bool, run_id: str, start: datetime, record: dict) -> dict
```

Runs each workflow in wf.pipeline in sequence, piping one stage's
output text into the next stage's stdin, with only the final stage's
output routed to its real destination (intermediate stages route to
memory). Any stage failure aborts the pipeline and persists a failed
record carrying the stages completed so far. Returns the parent run
record with `stages` set to the list of child run records.

## `px0.runs`

Run records and raw logs. Two artifacts per run, both under the
configurable log directory, both subject to retention -- never inside the
store, so raw prompts and connector responses stay out of any folder the
user might copy or sync.

### `resolve_logs_path`

```python
def resolve_logs_path(config: dict) -> Path
```

Resolves the directory used for run logs and records, creating it if
needed. Falls back to `~/.local/state/px0/logs` if the configured path
(default `/var/log/px0`) isn't writable. Side effect: writes and
removes a probe file to test writability.

### `new_run_id`

```python
def new_run_id(prefix: str = 'run') -> str
```

Generates a unique run id from a UTC timestamp plus a short random
hex suffix.

### `_date_of`

```python
def _date_of(run_id: str) -> str
```

Extracts the run's date (YYYY-MM-DD) from its run id, used to
partition records and logs into per-day directories.

### `record_path`

```python
def record_path(config: dict, run_id: str) -> Path
```

Returns the path to a run's JSON record file, partitioned by date
under `records/`.

### `log_path`

```python
def log_path(config: dict, run_id: str) -> Path
```

Returns the path to a run's raw log file, partitioned by date under
`runs/`.

### `write_record`

```python
def write_record(config: dict, record: dict) -> None
```

Writes a run record as JSON to disk, creating parent directories as
needed. Overwrites any existing record for the same run id.

### `append_raw_log`

```python
def append_raw_log(config: dict, run_id: str, text: str) -> None
```

Appends text to a run's raw log file, creating parent directories
(and the file) as needed. Ensures the appended text ends with a
newline.

### `read_record`

```python
def read_record(config: dict, run_id: str) -> dict
```

Reads and parses a run's JSON record. Raises FileNotFoundError if
the record is missing, e.g. because it aged out under retention.

### `read_raw_log`

```python
def read_raw_log(config: dict, run_id: str) -> str
```

Reads a run's raw log file. Returns an empty string if the log
doesn't exist rather than raising.

### `list_records`

```python
def list_records(config: dict, workflow: str | None = None, failed: bool = False, since: datetime | None = None) -> list[dict]
```

Lists run records matching the given filters (workflow id,
failed-only, since a given time), newest first. Returns an empty list
if the records directory doesn't exist. Record files that fail to
parse as JSON are silently skipped.

### `apply_retention`

```python
def apply_retention(config: dict) -> dict
```

Delete artifacts past retention, per config, except runs that
called a write tool -- those are exempt.

## `px0.skills`

px0 skills build: compile guidelines/ into skills/, the harness-facing
bundle. `work/` guideline folders are excluded, per the never-leaves-the-
machine rule -- they still reach the model at run time (inlined into
prompts), but are not written into a bundle a coding agent might carry
into a repository.

### `build`

```python
def build(home: Path) -> list[str]
```

Copies every guidelines/*.md file into skills/, mirroring the relative
path, except files under a top-level work/ folder. Overwrites existing
files in skills/. Returns the list of relative paths written.

## `px0.starters`

Content for the store scaffolded by `px0 init`: built-in workflows and guidelines.

## `px0.store`

px0 init: scaffold the store.

### `is_initialized`

```python
def is_initialized(home: Path) -> bool
```

Returns whether a store already exists at `home`, based on the
presence of config.toml.

### `export`

```python
def export(home: Path, dest: Path) -> None
```

Content plus version history, credentials excluded -- the supported
way to move a store to another machine.

### `init`

```python
def init(home: Path, harness_cmd: str | None = None) -> list[str]
```

Scaffold a store at `home`. If `harness_cmd` is given, it overrides
the default `model.harness_cmd` in the generated config.toml (e.g. to
point a fresh store at gemini, pi, or opencode instead of claude).
Returns a list of human-readable lines describing what was created.

## `px0.tools`

The normalized tool namespace. Every input `tool:` and every workflow
`tools:` entry names something from here. The native GitHub adapter calls
the GitHub REST API directly with a stored PAT. Composio-backed tools
(calendar, gmail, slack) are listed in the namespace with the read/write
shape the spec describes, but this build does not implement a live
Composio client -- calling one raises ConnectorNotConfigured rather than
guessing at an unverified API shape.

### `class ConnectorError`

A tool call failed against the external system.

### `class ConnectorNotConfigured`

The connection this tool needs is not set up.

### `class ToolSpec`

Registry entry describing one callable tool: its id, provider, read/write shape, and handler.

### `class Context`

Execution context passed to every tool handler: the store home and loaded config.

### `_github_token`

```python
def _github_token(ctx: Context) -> str
```

Loads the stored GitHub PAT, raising ConnectorNotConfigured if github is not connected.

### `_github_headers`

```python
def _github_headers(ctx: Context) -> dict
```

Builds the standard bearer-auth headers used for every GitHub REST call.

### `_github_request`

```python
def _github_request(ctx: Context, method: str, path: str, **kwargs) -> requests.Response
```

Issues one authenticated GitHub API request and raises ConnectorError on network
failure, a rejected token (401), or any other 4xx/5xx response.

### `_parse_pr_url`

```python
def _parse_pr_url(url: str) -> tuple[str, str, str]
```

Extracts (owner, repo, pr number) from a github.com PR URL; raises ConnectorError if it doesn't match.

### `_since_to_date`

```python
def _since_to_date(since: str) -> str
```

Converts a relative window like "-7d" into an ISO date; passes through anything else unchanged.

### `github_list_my_prs`

```python
def github_list_my_prs(args: dict, ctx: Context) -> list[dict]
```

Lists PRs authored by the connected user, updated since args["since"]
(default -7d), optionally scoped to args["repos"]. Read-only.

### `github_get_pr`

```python
def github_get_pr(args: dict, ctx: Context) -> dict
```

Fetches one pull request's metadata by URL. Read-only.

### `github_get_pr_diff`

```python
def github_get_pr_diff(args: dict, ctx: Context) -> str
```

Fetches the unified diff text of a pull request by URL. Read-only.

### `github_list_review_comments`

```python
def github_list_review_comments(args: dict, ctx: Context) -> list[dict]
```

Lists existing review comments on a pull request by URL. Read-only.

### `github_create_review_comment`

```python
def github_create_review_comment(args: dict, ctx: Context) -> dict
```

Posts a single-line review comment on a pull request. Write tool: mutates the PR
on GitHub. Resolves the PR's head sha itself so the caller only needs the URL.

### `_composio_unconfigured`

```python
def _composio_unconfigured(args: dict, ctx: Context) -> Any
```

Handler for every Composio-backed tool (calendar, gmail, slack): this build has no
live Composio client, so calling one always raises ConnectorNotConfigured.

### `list_tools`

```python
def list_tools(service: str | None = None) -> list[ToolSpec]
```

Returns all registered tools, or only those for one provider, sorted by id.

### `exists`

```python
def exists(tool_id: str) -> bool
```

Whether tool_id is a known entry in the registry.

### `is_write`

```python
def is_write(tool_id: str) -> bool
```

Whether the given tool mutates external state (used to gate what a workflow may call).

### `call`

```python
def call(home, config: dict, tool_id: str, args: dict) -> Any
```

Dispatches to a tool's handler by id. Raises ConnectorError for an unknown tool id;
the handler itself may raise ConnectorError/ConnectorNotConfigured.

## `px0.update`

px0 update / px0 version.

The spec's self-update flow assumes a signed release manifest served from a
real distribution channel. No such channel exists for this build (there is
no px0.sh release infrastructure to check against), so `update` and
`update --check` report that plainly instead of fabricating a manifest
fetch against a URL nobody verified. Everything else here -- reading the
installed component versions -- is real.

### `version_info`

```python
def version_info(home: Path, config: dict) -> dict
```

Reports installed px0/schema versions and whether the configured
harness binary is actually on PATH.

### `check`

```python
def check(config: dict) -> dict
```

Reports update availability. Always says no manifest exists in this
build rather than fabricating a version check.

### `run_update`

```python
def run_update(config: dict, check_only: bool = False) -> dict
```

Entry point for `px0 update`. With check_only, same as check(); otherwise
still performs no action, since there's no manifest to update against.

## `px0.versioning`

The versioning layer: content-addressed blobs plus a sqlite manifest.

Covers workflows/, guidelines/, and config.toml only, per spec. A version is
an immutable snapshot of one file's bytes; a change groups the versions
produced by one session. Revert always writes a new version; history is
never rewritten.

### `class FileChange`

One file's new content (or deletion) waiting to be recorded as a version.

### `_now`

```python
def _now() -> str
```

Current UTC timestamp as an ISO 8601 string, for storing in the manifest.

### `manifest_path`

```python
def manifest_path(home: Path) -> Path
```

Path to the sqlite manifest that indexes all versions.

### `objects_dir`

```python
def objects_dir(home: Path) -> Path
```

Path to the content-addressed blob store (zstd-compressed file contents).

### `connect`

```python
def connect(home: Path) -> sqlite3.Connection
```

Opens the manifest db, creating the versions directory and schema if needed.
Caller is responsible for closing the connection.

### `store_blob`

```python
def store_blob(home: Path, content: bytes) -> str
```

Writes content to the blob store under its sha256 digest, compressed with zstd.
No-op if a blob with that digest already exists (content-addressed dedup).
Returns the hex digest.

### `read_blob`

```python
def read_blob(home: Path, digest: str) -> bytes
```

Reads and decompresses a blob by its digest.

### `new_change_id`

```python
def new_change_id(conn: sqlite3.Connection, actor: str) -> str
```

Allocates and inserts a new change id of the form chg_YYYY-MM-DD-NNN,
sequential per day. Caller must commit the transaction.

### `record_change`

```python
def record_change(home: Path, actor: str, file_changes: list[FileChange]) -> str | None
```

Write one or more file versions as a single atomic change.
Returns the change id, or None if nothing actually changed.

### `list_versions`

```python
def list_versions(home: Path, rel_path: str) -> list[dict]
```

Returns every recorded version of a file, oldest first, as dicts with
version/actor/change_id/timestamp/deleted/evidence.

### `show_version`

```python
def show_version(home: Path, rel_path: str, version: int) -> bytes | None
```

Returns the raw bytes of a file at a specific version, or None if that
version was a deletion. Raises ValueError if the version doesn't exist.

### `latest_version_number`

```python
def latest_version_number(home: Path, rel_path: str) -> int | None
```

Returns the newest version number for a file, or None if it has no history.

### `diff_versions`

```python
def diff_versions(home: Path, rel_path: str, v1: int, v2: int) -> str
```

Returns a unified diff string between two versions of a file.
A deleted version is treated as empty content.

### `revert_file`

```python
def revert_file(home: Path, rel_path: str, to_version: int, actor: str) -> str | None
```

Reverts a file to a prior version by recording its old content as a new
version (history is never rewritten). Returns the new change id, or None
if the content is already identical to the current version.

### `list_changes`

```python
def list_changes(home: Path, since: datetime | None = None, actor: str | None = None) -> list[dict]
```

Returns changes newest-first, optionally filtered by timestamp and actor,
each annotated with the list of (path, version) pairs it touched.

### `show_change`

```python
def show_change(home: Path, change_id: str) -> dict
```

Returns a change's metadata plus a per-file unified diff against each
file's previous version (or a diff from /dev/null for a first version).
Raises ValueError if the change id doesn't exist.

### `revert_change`

```python
def revert_change(home: Path, change_id: str, actor: str) -> str | None
```

Reverts every file touched by a change back to its version immediately
prior (or deletes it, if the file had no earlier version). Returns the
new change id, or None if there was nothing to revert.

### `_walk_versioned_files`

```python
def _walk_versioned_files(home: Path) -> list[Path]
```

Lists every file on disk that falls under version control: all
markdown under workflows/ and guidelines/, plus config.toml.

### `checkpoint_scan`

```python
def checkpoint_scan(home: Path, actor: str = 'user:manual', force_hash: bool = False) -> str | None
```

Scan workflows/, guidelines/, and config.toml for changes made
outside the tool (hand edits), and capture them as new versions.
`force_hash` skips the mtime/size shortcut (the daemon's nightly pass,
which catches what mtime tricks miss).

### `prune`

```python
def prune(home: Path, config: dict, dry_run: bool = False) -> dict
```

Apply [versions] retention policy: drop the oldest version rows
beyond max_versions_per_file, never the current version of a live
file. No-op when keep_all is true. Followed by blob garbage collection
over anything no longer referenced.

### `_gc_blobs`

```python
def _gc_blobs(home: Path) -> int
```

Deletes any blob in the objects store not referenced by any version row.
Returns the number of blobs removed.

### `ensure_secure_permissions`

```python
def ensure_secure_permissions(path: Path) -> None
```

Restricts a file to owner read/write only (mode 0600); no-op if it
doesn't exist yet. Used for credentials.toml.

## `px0.workflow`

Workflow file model: YAML frontmatter as the machine contract, the
Markdown body as the prompt the model receives.

### `class WorkflowError`

Raised when a workflow file fails to parse or fails validation.

### `class InputSpec`

One entry in a workflow's `inputs` list: a tool call, retrieval query,
static source, or sub-workflow used to gather context before the main
prompt runs.

#### `kind`

```python
def kind(self) -> str
```

Returns which of tool/retrieve/source/workflow this input is,
inferred from which field is set. Raises WorkflowError if none are
set.

### `class Workflow`

Parsed representation of a workflow file: YAML frontmatter fields
plus the Markdown body (the prompt).

#### `rel_path`

```python
def rel_path(self) -> str | None
```

Placeholder for a path relative to the store home; always None
here and filled in by the caller when needed.

### `parse`

```python
def parse(path: Path) -> Workflow
```

Parses a workflow file into a Workflow, splitting YAML frontmatter
from the Markdown body. Raises WorkflowError if the file has no
frontmatter delimiters or the frontmatter section is malformed.
Missing frontmatter keys fall back to their dataclass defaults.

### `load_all`

```python
def load_all(home: Path) -> dict[str, Workflow]
```

Loads every workflow file (*.md, recursively) under the store's
workflows directory, keyed by workflow id. Returns an empty dict if the
workflows directory doesn't exist. A duplicate id overwrites the
previously loaded workflow with that id.

### `load`

```python
def load(home: Path, workflow_id: str) -> Workflow
```

Loads a single workflow by id. Raises WorkflowError if no workflow
with that id exists.

### `validate`

```python
def validate(wf: Workflow, home: Path) -> list[str]
```

Validates a parsed workflow's cross-references and structural
constraints -- guideline files, tool references, pipeline stages, cron
schedule syntax, and output target -- returning a list of
human-readable error strings. An empty list means the workflow is
valid.
