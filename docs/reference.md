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

px0 new: turn a sentence into a working workflow.

Building one runs four harness passes, each with a job small enough that the
model can do it well:

1. `clarify` -- ask what's ambiguous about the request. Repeated until the
   model has no questions left (or the user stops answering), because a plan
   built on a guess is worse than one more question.
2. `propose_queries` -- turn the settled request into Composio catalogue
   searches. The model knows what capabilities the task needs; it does not
   know Composio's tool names, so it writes queries rather than guessing slugs.
3. `select_tools` -- pick the few tools that actually fit from the candidates
   those searches returned. Raw relevance ranking is not good enough to trust
   blind (searching "post a message to a channel" surfaces a *delete* tool
   first), so a model with the task in hand chooses, and a human confirms.
4. `generate_plan` -- write the workflow against exactly those tools.

Pure planning functions live here; every prompt, spinner, and confirmation
lives in the CLI, which is where user interaction belongs.

### `class BuilderError`

Raised when a workflow plan can't be generated or parsed from the harness response.

### `_extract_json`

```python
def _extract_json(raw: str, want_array: bool = False)
```

Pulls the first JSON value out of a harness response.

Harnesses narrate around their answers, so the JSON is located rather than
assumed to be the whole reply.

### `_qa_block`

```python
def _qa_block(qa: list[tuple[str, str]]) -> str
```

Renders the clarification history for inclusion in a later prompt.

### `class Plan`

A workflow plan produced by the harness: trigger, inputs, tools, output shape,
and the instruction body, plus the raw JSON the model returned.

### `clarify`

```python
def clarify(config: dict, description: str, qa: list[tuple[str, str]]) -> list[str]
```

Asks what is still ambiguous about the request.

Returns up to three questions, or an empty list when the model considers the
request buildable. Only things that would change the generated workflow
count as ambiguous -- the model is told not to ask for detail it can pick a
sane default for, because an interrogation is worse than an assumption.

### `propose_queries`

```python
def propose_queries(config: dict, description: str, qa: list[tuple[str, str]]) -> list[dict]
```

Turns the settled request into Composio catalogue searches.

Each search is a toolkit plus a short capability phrase, because Composio's
search filters by substring within a toolkit rather than ranking by
relevance: a whole sentence matches almost nothing, while
`toolkit=github` + "list pull requests" lands on the right tool. The model
names services and actions -- never slugs, which it cannot know and would
invent.

### `describe_query`

```python
def describe_query(query: dict) -> str
```

A query as one readable line, for showing the user what px0 is searching for.

### `search_candidates`

```python
def search_candidates(home: Path, queries: list[dict]) -> list[catalogue.CatalogueTool]
```

Runs each search against Composio's catalogue and pools the results.

A toolkit-scoped search that comes back empty is retried without the scope,
since the model may have guessed a toolkit slug that doesn't exist.
De-duplicated by slug and order-preserving, so the first search's matches
stay near the top.

### `select_tools`

```python
def select_tools(config: dict, description: str, qa: list[tuple[str, str]], candidates: list[catalogue.CatalogueTool]) -> list[catalogue.CatalogueTool]
```

Picks the minimal set of candidate tools the request actually needs.

Relevance ranking alone is not trustworthy -- a search for "post a message"
can rank a delete tool first -- so the model chooses with the task in hand,
and is told to prefer fewer tools and to avoid writes it wasn't asked for.

### `generate_plan`

```python
def generate_plan(config: dict, description: str, qa: list[tuple[str, str]] | None = None, selected: list[catalogue.CatalogueTool] | None = None) -> Plan
```

Asks the harness to turn the settled request into a JSON workflow plan.

`selected` restricts it to the discovered tools the user confirmed; without
it the plan may only use px0's curated registry. Raises BuilderError if the
harness response has no JSON object or the JSON is malformed.

### `check_feasibility`

```python
def check_feasibility(plan: Plan, home: Path) -> list[str]
```

Validates a plan against reality: unknown tool ids, write tools used as inputs
(inputs must be read-only), and an invalid cron schedule. Returns a list of
human-readable issue strings; empty means the plan can proceed.

### `plan_tool_ids`

```python
def plan_tool_ids(plan: Plan) -> list[str]
```

Every tool id the plan references, inputs and tools alike, in order.

### `required_connections`

```python
def required_connections(plan: Plan, home: Path | None = None) -> set[str]
```

The provider names (e.g. "github", "slack") the plan's inputs and tools touch.

### `write_tools_named`

```python
def write_tools_named(plan: Plan, home: Path | None = None) -> list[str]
```

The subset of plan.tools that are write tools, so the CLI can warn the user
before granting them.

### `_terms`

```python
def _terms(text: str) -> set[str]
```

Distinctive lowercase words in `text`: no stopwords, nothing tiny.

### `_topic_hits`

```python
def _topic_hits(wanted: set[str], topic: set[str]) -> int
```

Counts topic words the request refers to, matching on a shared prefix.

Prefix matching stands in for stemming, which the word forms here need:
"summarize" has to match `summarization.md` and "pull request description"
has to match `pr-descriptions.md`. Cheap, and wrong only for words that
share five letters and nothing else.

### `_shared_prefix`

```python
def _shared_prefix(a: str, b: str) -> int
```

Length of the leading substring `a` and `b` have in common.

Compared on a shared prefix rather than "one is a prefix of the other":
"summarize" and "summarization" share eight characters but neither contains
the other.

### `choose_guidelines`

```python
def choose_guidelines(home: Path, description: str, top_n: int = 3) -> list[str]
```

Picks the guideline files whose topic actually matches the task.

A file's headings name what it is about, so a heading match counts for much
more than a body match, and body matches are normalized by vocabulary size
so a long file doesn't win on sheer surface area. Files scoring below
`_GUIDELINE_SCORE_FLOOR` are left off entirely -- attaching an unrelated
guideline is worse than attaching none, since every one is inlined verbatim
into the run's prompt.

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

## `px0.catalogue`

Composio's tool catalogue: searching it, and remembering what was found.

px0 ships a small set of curated tools (`tools.REGISTRY`), but Composio's
catalogue is thousands of tools across hundreds of toolkits. `px0 new`
searches it so a workflow can use the tool that actually fits the task
instead of the nearest curated approximation.

A discovered tool is *cached in the store* rather than looked up again at run
time. Two reasons: a workflow must keep working offline and unchanged after it
is written, and read-vs-write has to be knowable without a network call --
`px0 run --dry-run` decides what to stub from it.

Read/write comes from Composio's own MCP-style hints in each tool's `tags`:
`readOnlyHint` means it only reads; its absence means it can change something;
`destructiveHint` means it can delete or overwrite. Verified against the live
catalogue on 2026-08-20 (GMAIL_FETCH_EMAILS carries readOnlyHint,
GMAIL_SEND_EMAIL does not, GMAIL_DELETE_MESSAGE carries destructiveHint).

### `class CatalogueError`

Raised when Composio's catalogue can't be searched or a slug can't be read.

### `class CatalogueTool`

One tool from Composio's catalogue, in px0's own terms.

#### `id`

```python
def id(self) -> str
```

The id a workflow file uses for this tool.

### `is_catalogue_id`

```python
def is_catalogue_id(tool_id: str) -> bool
```

Whether `tool_id` names a discovered Composio tool rather than a curated one.

### `slug_of`

```python
def slug_of(tool_id: str) -> str
```

The Composio slug behind a `composio:` tool id.

### `_params_of`

```python
def _params_of(schema: dict) -> dict[str, str]
```

Flattens a tool's input_parameters JSON Schema into {name: type}.

Required fields come first so a generated `args` block leads with what the
tool actually needs.

### `_from_api`

```python
def _from_api(item: dict) -> CatalogueTool
```

Builds a CatalogueTool from one Composio tools-API item.

### `search`

```python
def search(home: Path, query: str, limit: int = SEARCH_LIMIT, toolkit: str | None = None) -> list[CatalogueTool]
```

Searches Composio's catalogue, newest-relevance first.

Raises CatalogueError rather than returning nothing when the search itself
failed -- "no tool matches" and "we could not ask" must not look alike.

### `fetch`

```python
def fetch(home: Path, slug: str) -> CatalogueTool
```

Reads one tool by slug, for confirming it exists and getting its schema.

### `_get`

```python
def _get(home: Path, path: str, params: dict) -> dict
```

One authenticated GET against Composio's REST API.

Goes through requests rather than the SDK: the SDK models connected
accounts and executions, not catalogue browsing.

### `cache_path`

```python
def cache_path(home: Path) -> Path
```

Where discovered tool metadata lives.

### `load_cached`

```python
def load_cached(home: Path) -> dict[str, CatalogueTool]
```

Every previously discovered tool, keyed by its px0 tool id.

Never raises: a corrupt or missing cache means "nothing discovered yet",
which degrades a workflow into an unknown-tool error rather than a crash.

### `remember`

```python
def remember(home: Path, discovered: list[CatalogueTool]) -> None
```

Adds tools to the cache, replacing any earlier entry for the same slug.

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

Parses a `--since` value like "7d" into an absolute datetime.

### `_dump`

```python
def _dump(args: argparse.Namespace, data) -> None
```

Prints data to stdout as indented JSON, coercing non-JSON-serializable values via str().

Flushed, because spinners write to stderr and block-buffered stdout would
let the two interleave out of order when piped.

### `_mask_key`

```python
def _mask_key(key: str) -> str
```

Returns a masked version of an API key (e.g. 'abcd...1234' or '****').

### `cmd_init`

```python
def cmd_init(args: argparse.Namespace) -> None
```

Handles `px0 init`: scaffolds a new store and prints suggested next commands.

### `_clarify_loop`

```python
def _clarify_loop(config: dict, description: str, skip: bool) -> list[tuple[str, str]]
```

Asks the model what's ambiguous and the user to resolve it, until nothing
is left to ask (or the user stops answering).

Returns the question/answer pairs, which every later pass is given so the
plan reflects the answers rather than re-guessing them. A blank answer skips
one question; an empty round ends the loop, because pressing Enter through
an interrogation should not block the build.

### `_describe_tool`

```python
def _describe_tool(spec_or_tool, width: int) -> str
```

One aligned line for a tool being proposed: id, access, description.

### `_discover_tools`

```python
def _discover_tools(home: Path, config: dict, description: str, qa: list[tuple[str, str]]) -> list
```

Searches Composio's catalogue for the task and returns the chosen tools.

Returns [] when the task needs no external service, which is a valid answer
-- plenty of useful workflows only summarize their own input.

### `_confirm_tools`

```python
def _confirm_tools(home: Path, selected: list, assume_yes: bool) -> list
```

Shows the chosen tools and their access, and gets explicit agreement.

This is the gate before anything is authorized or written: the model chose
these, and choosing a write tool the request didn't ask for is exactly the
mistake a human should catch here.

### `_authorize_toolkits`

```python
def _authorize_toolkits(home: Path, toolkits: set[str], assume_yes: bool) -> list[str]
```

Authorizes each toolkit the plan needs that isn't authorized yet, asking
first. Returns the toolkits still waiting on a browser consent.

Nothing is aborted over a pending consent: the workflow file is valid either
way, and making the user re-run `px0 new` would repeat the clarify, search,
selection, and planning passes just to reach the same file.

### `cmd_new`

```python
def cmd_new(args: argparse.Namespace) -> None
```

Handles `px0 new`: clarifies the request, searches Composio's catalogue for
the tools it needs, confirms them with the user, authorizes what isn't
authorized yet, then plans and writes the workflow file.

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

### `cmd_tools`

```python
def cmd_tools(args: argparse.Namespace) -> None
```

Handles `px0 tools list`: prints each available tool with a read/write marker,
its id, provider, description, and parameters, optionally filtered by service.

### `_dim_log`

```python
def _dim_log(text: str) -> str
```

Dims the leading timestamp on each log line so the message reads first.

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

### `_color_diff`

```python
def _color_diff(text: str) -> str
```

Colours a unified diff the way a pager would: adds green, removes red.

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

Handles `px0 skills`: acts as a proxy for the `npx skills` utility to discover,
install, list, update, and remove community agent skills, or runs local `build` to compile
guidelines into Claude Code skill bundles (`SKILL.md`).

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

### `_set_composio_key`

```python
def _set_composio_key(home: Path, key: str | None) -> None
```

Stores the Composio API key after verifying it against the live API.

This is the whole of connection setup: individual apps authorize themselves
when a workflow first needs them, so there is nothing else to configure
per service.

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
update, or rolls back.

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

The px0 argparse tree, built against this module's own `cmd_*` handlers.

The tree itself lives in `px0.parser`; this wrapper stays here because it is
the name callers and tests reach for.

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

Connections to external apps, all brokered through Composio.

There is no user-facing connect command: `setup_composio` stores the one API
key (via `px0 config composio`, or `px0 init`), and individual apps are
authorized on demand -- a tool that needs Gmail calls
`connect_composio_app("gmail")` itself and surfaces the returned URL, so the
only manual step left is the human consenting in a browser.

### `class ComposioUnreachable`

The Composio API could not be reached. Distinct from a rejected API key:
re-entering the key will not help, so callers must not re-prompt for one.

### `_is_cert_error`

```python
def _is_cert_error(exc: BaseException) -> bool
```

True if exc (or anything it wraps) is a TLS certificate verification failure.

### `_bundle_verifies`

```python
def _bundle_verifies(bundle: str, host: str = COMPOSIO_HOST) -> bool
```

True if `host`'s certificate chain validates against `bundle`.

### `find_ca_bundle`

```python
def find_ca_bundle(host: str = COMPOSIO_HOST) -> str | None
```

Returns the first existing CA bundle that validates `host`, or None.

### `ca_bundle`

```python
def ca_bundle(home: Path) -> str | None
```

The CA bundle TLS verification should use, or None for the default.

An explicit SSL_CERT_FILE always wins; otherwise the bundle a previous
interception detection stored in `connectors.ca_bundle`.

### `apply_ca_bundle`

```python
def apply_ca_bundle(home: Path) -> str | None
```

Exports the stored CA bundle so HTTP clients pick it up, and returns it.

Sets both names because the two clients px0 uses read different ones:
httpx/OpenSSL honour SSL_CERT_FILE, while requests verifies against certifi
unless REQUESTS_CA_BUNDLE says otherwise. A no-op when nothing is stored.

### `recover_ca_bundle`

```python
def recover_ca_bundle(home: Path) -> str | None
```

Called after a TLS verification failure: find a CA bundle that trusts
whatever is intercepting the connection, persist it, and return it.

Shared by every caller that talks to Composio, so an interception detected
once is remembered for all of them. Returns None when no known bundle helps.

### `_store_ca_bundle`

```python
def _store_ca_bundle(home: Path, bundle: str) -> None
```

### `_silence_sdk_logging`

```python
def _silence_sdk_logging() -> None
```

Mutes the Composio/httpx INFO chatter ("Retrying request to ...").

Those lines are the SDK narrating its own retries; they interleave with
px0's progress output and tell the user nothing actionable. Warnings and
errors still come through.

### `short_api_error`

```python
def short_api_error(exc: BaseException) -> str
```

Composio SDK errors stringify as `Error code: N - {...whole payload...}`.

Keeps the parts a human acts on -- the message and the suggested fix -- so a
permissions problem reads as one line instead of a wall of JSON.

### `_verify_key`

```python
def _verify_key(api_key: str) -> None
```

Hello world / healthcheck: fetch github toolkit info to verify the key.

### `setup_composio`

```python
def setup_composio(home: Path, api_key: str) -> dict
```

Stores the Composio API key inside config.toml and credentials after validating it.

Returns what the caller may want to report: {"ca_bundle": <path or None>},
naming the CA bundle a TLS interception forced px0 onto. Prints nothing --
presentation belongs to the CLI, which may be drawing a spinner over this.

### `_composio_client`

```python
def _composio_client(home: Path)
```

Returns a Composio client configured with the stored Composio API key.

### `_ensure_auth_config`

```python
def _ensure_auth_config(home: Path, toolkit: str) -> str
```

Checks [composio.auth_configs].<toolkit> in credentials; if absent,
creates it via Composio API and caches the returned ID.

### `connect_composio_app`

```python
def connect_composio_app(home: Path, app: str) -> dict
```

Creates (or reuses) an auth config, creates an auth link session for the app,
caches the connected_account_id, and returns the redirect_url.

### `connected_account_status`

```python
def connected_account_status(home: Path, app: str) -> str
```

Polls the status of the cached connected account from the Composio API.

### `list_connections`

```python
def list_connections(home: Path) -> list[dict]
```

Returns one summary dict per configured connection (service, kind, login, expiry).

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

### `_log_event`

```python
def _log_event(config: dict, message: str) -> None
```

Appends a timestamped message to daemon.log, swallowing OSError.

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
reindex, draining queued playlist ingest jobs, run-log retention, and a
once-a-week update-availability check. Every fallible step is captured in the
report rather than raised, so one broken index or unreachable playlist doesn't
block the rest of housekeeping.

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

### `restart_if_running`

```python
def restart_if_running(home: Path, config: dict) -> None
```

Checks daemon status, and if it is running/alive, sends SIGTERM and respawns it.

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

### `_check_qmd_version`

```python
def _check_qmd_version(home: Path, config: dict) -> dict
```

Runs qmd --version and compares against QMD_PINNED_VERSION.

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

Reports configured connections. Checks if any Composio connection is not ACTIVE.

### `_check_unreferenced_guidelines`

```python
def _check_unreferenced_guidelines(home: Path) -> dict
```

Counts guideline files that no workflow lists.

Informational, never a failure: spec.md:792 puts unreferenced files in the
consolidation report ("to surface staleness"), which `px0 consolidate`
already does. Failing here would also mean every freshly initialized store
is unhealthy -- `px0 init` scaffolds guidelines but no workflows, so all of
them start out unreferenced.

### `_check_update`

```python
def _check_update(home: Path) -> dict
```

Reports the newer version the daemon's weekly check found, if any.

Informational, never a failure -- being a release behind is not a broken
store. Reads what the nightly pass recorded rather than calling PyPI, so
`doctor` stays offline-safe.

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

### `process_ingest_queue`

```python
def process_ingest_queue(home: Path, config: dict) -> dict
```

Processes any queued YouTube playlist ingest jobs under .state/ingest/.

## `px0.parser`

The px0 argparse tree: what the CLI accepts, separated from what it does.

Kept out of `cli.py` so the ~200 lines of declarative flag wiring don't sit in
the middle of the command handlers. The dependency runs one way -- `cli`
imports this, never the reverse -- so `build` is handed the module holding the
handlers rather than importing them.

### `build`

```python
def build(handlers) -> argparse.ArgumentParser
```

Builds the full px0 argparse tree: one subparser per top-level command, each
wiring its own flags and a `func` default that main() dispatches to.

`handlers` is the module providing the `cmd_*` functions.

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

### `ingest_failed_dir`

```python
def ingest_failed_dir(home: Path | None = None) -> Path
```

Path to the directory holding failed knowledge ingest jobs.

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

### `update_history_path`

```python
def update_history_path(home: Path | None = None) -> Path
```

Path to `.state/update-history.json` recording update history.

### `update_check_path`

```python
def update_check_path(home: Path | None = None) -> Path
```

Path to `.state/update-check.json` recording last update availability check.

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

### `retrieval_consent_path`

```python
def retrieval_consent_path(home: Path | None = None) -> Path
```

Path to `.state/retrieval-consent.json` recording model download consent.

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

Two backends sit behind `retrieve()`, selected by `retrieval.backend`:

- "local" (default): SQLite FTS5 with BM25 ranking, embedded, no server.
  Keyword matching only -- no vectors, no rerank, nothing to download.
- "qmd": shells out to the qmd CLI (`retrieval.qmd_cmd`) for hybrid
  keyword + vector search with reranking. Needs qmd installed separately
  and gates its ~2GB of GGUF models behind explicit, printed-size
  consent on the first reindex.

Either way `local_only=True` (the default at every call site) excludes
`knowledge/work/`, which never leaves the machine.

### `class RetrievalBackendError`

Raised when the retrieval backend is missing, times out, or errors.

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

Chunks text by splitting on two or more newlines, grouping paragraphs
until the chunk is at least 150 words (or we hit a heading/EOF), and
finding the nearest preceding markdown heading (e.g. `## Section`) to
use as the anchor.

### `_chunk_file`

```python
def _chunk_file(text: str) -> list[tuple[str, str]]
```

Standard file-chunking entry point. Returns a list of (anchor, text) tuples.

### `_qmd_run`

```python
def _qmd_run(config: dict, *args, timeout: float = 60) -> str
```

Shells out to the qmd command configured in retrieval.qmd_cmd with args.

### `_qmd_ensure_collection`

```python
def _qmd_ensure_collection(home: Path, config: dict)
```

Idempotently adds the knowledge path to qmd's collections.

### `_qmd_ensure_embed_consent`

```python
def _qmd_ensure_embed_consent(home: Path, config: dict) -> bool
```

Checks and prompts for model download consent if not already given.

### `_parse_qmd_result`

```python
def _parse_qmd_result(home: Path, config: dict, raw_json: str) -> list[Passage]
```

Parses JSON output of qmd and returns a list of Passage instances.

### `_qmd_retrieve`

```python
def _qmd_retrieve(home: Path, config: dict, query: str, k: int) -> list[Passage]
```

Retrieves passages using the qmd query command.

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

### `parse_since`

```python
def parse_since(text: str) -> datetime
```

Parses an age like "7d", "-7d", "2w", or "12h" into an absolute datetime.

The leading minus is optional because it reads naturally as "7 days back"
and the TUI's own prompt suggests it; rejecting it was a bug. Lives here
rather than in the CLI so `runs_tui` can use it without importing the CLI,
which imports `runs_tui`.

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

### `tail_lines`

```python
def tail_lines(path: Path, poll_interval: float = 1.0)
```

Yields lines appended to `path` after this call starts, polling
every poll_interval seconds. Never returns on its own -- the caller
breaks out (e.g. on a terminal run outcome, or KeyboardInterrupt).

## `px0.runs_tui`

The `px0 runs` interactive browser: a curses list/detail view over run
records. The list is newest-first and filterable by workflow, outcome,
write activity, and age; the detail view adds the rendered prompt recovered
from the raw log, the guideline versions inlined, and per-tool-call
timings, with one keystroke each to rerun, page the log, show the output,
and trace provenance. Row text comes from `format_row`, shared with
`px0 runs list` so both render identically.

### `column_widths`

```python
def column_widths(records: list[dict]) -> dict[str, int]
```

Widths that align a whole batch of rows into columns.

Computed once by the caller and passed to every `format_row` so both the
plain listing and the TUI lay out identically; without it each row would be
formatted in isolation and the columns would jitter.

### `format_row`

```python
def format_row(r: dict, widths: dict[str, int] | None = None) -> str
```

Formats one run record into a single list row, shared between CLI and TUI.

Columns are separated by two spaces and padded to `widths` when given, so a
listing reads as a table; without widths the fields are simply joined.

### `extract_rendered_prompt`

```python
def extract_rendered_prompt(raw_log_text: str) -> str
```

Pulls the first turn's rendered prompt out of a run's raw log, which
interleaves `--- turn N PROMPT ---` / `--- turn N OUTPUT ---` blocks.
Returns "" if the log has no such block (e.g. the run failed before stage 5,
or its raw log has aged out under retention).

### `apply_filters`

```python
def apply_filters(records: list[dict], workflow: str | None, outcome: str | None, write_only: bool, since: str | None) -> list[dict]
```

Filters the list of run records based on the TUI parameters.

### `run`

```python
def run(home: Path, config: dict) -> None
```

Entry point for the px0 runs curses TUI.

### `_init_palette`

```python
def _init_palette() -> bool
```

Sets up colour pairs. False on a terminal without colour, so callers fall
back to A_DIM/A_BOLD attributes instead.

### `_attr`

```python
def _attr(pair: int, fallback: int = 0) -> int
```

The attribute for a palette entry, or `fallback` when colour is unavailable.

### `_suspended`

```python
def _suspended(prompt: str = '\nPress any key to resume...')
```

Drops out of curses for a block that writes to the real terminal.

Restores curses on the way out whatever happened, so an exception in a
keystroke handler can never leave the terminal in raw mode with no cursor.
Errors are shown to the user and swallowed: a failed pager or a missing
record should return you to the list, not tear the TUI down.

### `_dim_sep`

```python
def _dim_sep() -> str
```

The separator between header fields.

### `_filter_summary`

```python
def _filter_summary(workflow, outcome, write_only, since_raw) -> str
```

One dim line naming only the filters actually in effect.

### `_outcome_attr`

```python
def _outcome_attr(record: dict) -> int
```

Colours a row by its outcome: failures red, everything else plain.

### `_addkeys`

```python
def _addkeys(stdscr, y: int, width: int, keys: list[tuple[str, str]]) -> None
```

Renders the key hints: each key accented, its label dim.

### `_main`

```python
def _main(stdscr, home: Path, config: dict) -> None
```

### `_prompt`

```python
def _prompt(stdscr, y, prompt_text) -> str | None
```

### `_status_err`

```python
def _status_err(stdscr, y, text) -> None
```

### `_detail_view`

```python
def _detail_view(stdscr, home: Path, config: dict, record_brief: dict) -> None
```

## `px0.skills`

px0 skills build: compile guidelines/ into skills/, the harness-facing
bundle. `work/` guideline folders are excluded, per the never-leaves-the-
machine rule -- they still reach the model at run time (inlined into
prompts), but are not written into a bundle a coding agent might carry
into a repository.

### `_sync_claude_symlink`

```python
def _sync_claude_symlink(skill_dir: Path, name: str, claude_skills_dir: Path, create: bool) -> None
```

Manages ~/.claude/skills/px0-<name> symlink.
If create=True, ensures symlink points to skill_dir.
If create=False, removes the symlink if it exists and points to skill_dir.

### `build`

```python
def build(home: Path) -> list[str]
```

Compiles every guidelines/*.md file into Claude Code skill bundles (skills/<name>/SKILL.md),
prunes stale bundles, and manages ~/.claude/skills/ symlinks if the configured harness is Claude.

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
`tools:` entry names something from here. Every tool executes through the
Composio SDK against a connected account -- the GitHub tools proxy the
GitHub REST API through Composio rather than holding their own PAT.
A tool whose app is not authorized yet prepares that app's authorization
itself and raises ConnectorNotConfigured carrying the URL to consent at --
there is no separate connect step to run first.

### `class ConnectorError`

A tool call failed against the external system.

### `class ConnectorNotConfigured`

The connection this tool needs is not set up.

### `class ToolSpec`

Registry entry describing one callable tool: its id, provider, read/write shape, and handler.

### `class Context`

Execution context passed to every tool handler: the store home and loaded config.

### `_github_request`

```python
def _github_request(ctx: Context, method: str, path: str, **kwargs) -> Any
```

Issues one authenticated GitHub API request via Composio's proxy.

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

### `_needs_connection`

```python
def _needs_connection(home, app: str, reason: str) -> ConnectorNotConfigured
```

Builds the error raised when `app` isn't usable yet, with a live auth link.

There is no `px0 connect` to send the user to: a tool that needs an app
triggers that app's authorization itself, so the only thing missing is the
human opening the URL. Minting a link is idempotent -- the underlying auth
config is created once and cached -- and grants nothing until someone
consents in the browser.

### `_composio_credentials`

```python
def _composio_credentials(home)
```

The stored Composio credentials, or a ConnectorNotConfigured explaining how
to set the API key up.

### `_composio_execute`

```python
def _composio_execute(ctx: Context, app: str, tool_slug: str, arguments: dict) -> Any
```

Executes a Composio tool, authorizing the app on demand if it isn't yet.

### `calendar_list_events`

```python
def calendar_list_events(args: dict, ctx: Context) -> Any
```

Lists calendar events in a window.

### `gmail_search_messages`

```python
def gmail_search_messages(args: dict, ctx: Context) -> Any
```

Search gmail messages.

### `gmail_get_message`

```python
def gmail_get_message(args: dict, ctx: Context) -> Any
```

Fetch one gmail message.

### `gmail_send_message`

```python
def gmail_send_message(args: dict, ctx: Context) -> Any
```

Send a gmail message.

### `slack_post_message`

```python
def slack_post_message(args: dict, ctx: Context) -> Any
```

Post a message to a slack channel.

### `_discovered_spec`

```python
def _discovered_spec(tool) -> ToolSpec
```

Wraps a catalogue tool as a ToolSpec with a generic Composio handler.

Every discovered tool executes through the same path the curated Composio
tools use, so authorization-on-demand, retries, and dry-run stubbing all
behave identically whether a tool was hand-written or found by `px0 new`.

### `resolve`

```python
def resolve(tool_id: str, home = None) -> ToolSpec | None
```

The ToolSpec for a tool id, curated or discovered, or None if unknown.

`home` is needed to see discovered tools -- their metadata lives in the
store's catalogue cache, not in this module.

### `list_tools`

```python
def list_tools(service: str | None = None, home = None) -> list[ToolSpec]
```

Every usable tool -- curated, plus any discovered by `px0 new` when `home`
is given -- optionally narrowed to one provider, sorted by id.

### `exists`

```python
def exists(tool_id: str, home = None) -> bool
```

Whether tool_id names a usable tool.

### `is_write`

```python
def is_write(tool_id: str, home = None) -> bool
```

Whether the given tool mutates external state (used to gate what a workflow may call).

Raises KeyError for an unknown id, matching the previous registry-only
behaviour -- callers check `exists` first.

### `call`

```python
def call(home, config: dict, tool_id: str, args: dict) -> Any
```

Dispatches to a tool's handler by id. Raises ConnectorError for an unknown tool id;
the handler itself may raise ConnectorError/ConnectorNotConfigured.

## `px0.ui`

Terminal presentation: colors, status glyphs, and the spinner.

Everything user-facing goes through here so the CLI has one voice. Two
rules shape the design:

1. **Subtle by default.** Colour marks meaning -- a failure, a value you
   can act on -- and nothing else. Labels and chrome are dim; values are
   plain. A screen of px0 output should read as mostly grey with a few
   deliberate accents, never as a colour test page.
2. **Plain when not a terminal.** Pipe px0 anywhere and every escape
   sequence disappears, glyphs fall back to ASCII (`[OK]`, `[FAIL]`), and
   the spinner goes silent. Output stays greppable, so scripts parsing it
   never see a byte of styling.

Honours `NO_COLOR` (any value disables), `FORCE_COLOR` (any value
enables, even when piped), `TERM=dumb`, and `--no-color`.

### `set_color`

```python
def set_color(enabled: bool | None) -> None
```

Forces colour on/off for the process. None restores auto-detection.

### `is_tty`

```python
def is_tty(stream = None) -> bool
```

True when `stream` is a real terminal, regardless of colour settings.

Separate from `color_enabled` on purpose: FORCE_COLOR should add colour to
piped output, but carriage-return redraws only make sense on a terminal, so
the spinner gates on this instead.

### `color_enabled`

```python
def color_enabled(stream = None) -> bool
```

True when `stream` (default stdout) should receive escape sequences.

### `paint`

```python
def paint(text: str, code: str, bold: bool = False, stream = None) -> str
```

Wraps text in an SGR sequence, or returns it untouched when colour is off.

### `dim`

```python
def dim(text: str, **kw) -> str
```

Secondary text: labels, units, anything the eye should skip.

### `faint`

```python
def faint(text: str, **kw) -> str
```

Chrome: rules and separators.

### `accent`

```python
def accent(text: str, **kw) -> str
```

px0's own voice -- a value the user will act on.

### `strong`

```python
def strong(text: str, **kw) -> str
```

Emphasis without colour, for headings inside a plain block.

### `glyph`

```python
def glyph(role: str, stream = None) -> str
```

The status marker for `role`, coloured on a terminal, bracketed ASCII when piped.

### `_status`

```python
def _status(role: str, message: str, detail: str = '', width: int = 0, stream = None) -> None
```

### `ok`

```python
def ok(message: str, detail: str = '', **kw) -> None
```

A check that passed, a thing that got created.

### `err`

```python
def err(message: str, detail: str = '', **kw) -> None
```

A failure. Goes to stderr by default -- errors are not output.

### `warn`

```python
def warn(message: str, detail: str = '', **kw) -> None
```

Something worth knowing that isn't a failure.

### `info`

```python
def info(message: str, detail: str = '', **kw) -> None
```

Neutral progress narration.

### `step`

```python
def step(message: str, detail: str = '', **kw) -> None
```

One step in a multi-step flow.

### `heading`

```python
def heading(text: str, stream = None) -> None
```

A section title. One blank line above, never a box or a banner.

### `rule`

```python
def rule(stream = None) -> None
```

A full-width faint separator. Skipped entirely when not a terminal.

### `kv`

```python
def kv(label: str, value, width: int = 0, stream = None) -> None
```

A dim label and a plain value, aligned when `width` is given.

### `bullet`

```python
def bullet(text: str, stream = None) -> None
```

One item in a list.

### `hint`

```python
def hint(text: str, stream = None) -> None
```

A next step. Dim, indented, always after a blank line.

### `command`

```python
def command(text: str, stream = None) -> None
```

A command the user can copy and run.

### `prompt`

```python
def prompt(text: str) -> str
```

A styled input prompt. Returns what the user typed, stripped.

### `class Spinner`

An animated progress indicator with an elapsed-seconds counter.

A no-op unless stderr is a terminal: piped output gets one plain line
at the start instead of a stream of redraws, and nothing at all when
the caller asked for silence. Always writes to stderr so a spinner
never lands in output the user is capturing.

#### `__init__`

```python
def __init__(self, message: str, quiet: bool = False, stream = None)
```

#### `_spin`

```python
def _spin(self) -> None
```

#### `start`

```python
def start(self) -> 'Spinner'
```

#### `_erase`

```python
def _erase(self) -> None
```

#### `stop`

```python
def stop(self, final: str | None = None, role: str = 'ok') -> None
```

Stops the animation. `final` replaces the line with a status line.

#### `update`

```python
def update(self, message: str) -> None
```

Changes the message mid-spin.

### `spinner`

```python
def spinner(message: str, done: str | None = None, quiet: bool = False, stream = None)
```

Runs a block under a spinner, clearing it on success or failure.

    with ui.spinner("Verifying key", done="key verified"):
        verify()

On an exception the line is erased before it propagates, so a traceback
or error message never lands on top of a half-drawn spinner.

## `px0.update`

px0 update / px0 version: PyPI-backed version checks and self-update.

`check()` reads the published versions from PyPI's JSON API; `run_update()`
upgrades in place through whichever mechanism installed px0 (pipx or pip),
applies any pending store-schema MIGRATIONS, appends the result to
`.state/update-history.json`, restarts a running daemon, and finishes with
a quick doctor pass. `rollback()` reinstalls the last entry's from_version
and pops it; schema migrations are forward-only and are not undone.

### `class UpdateError`

Raised when an update or rollback fails.

### `version_info`

```python
def version_info(home: Path, config: dict) -> dict
```

Reports installed px0/schema versions and whether the configured
harness binary is actually on PATH.

### `class PyPIUnreachable`

PyPI could not be queried. Distinct from "no newer version exists":
reporting an unreachable index as "up to date" is a lie the user acts on.

### `_pypi_latest_version`

```python
def _pypi_latest_version(channel: str) -> str | None
```

The newest version published on `channel`, or None if px0 isn't on PyPI yet.

Raises PyPIUnreachable when the index could not be read at all -- network
down, proxy, TLS interception. Collapsing that into None would report
"already up to date" to someone who is actually several releases behind.

### `check`

```python
def check(config: dict) -> dict
```

Reports update availability. Raises PyPIUnreachable if PyPI can't be read.

The result always carries both keys, and they mean different things:
`available_version` is the newest version published on the channel (None if
px0 isn't published there at all), and `update_available` says whether that
is newer than what's installed. Callers gate on `update_available` -- an
earlier version of this returned `available_version: None` when current,
which made "up to date" and "not published" indistinguishable.

### `_load_history`

```python
def _load_history(path: Path) -> list
```

The update history, or [] when it's missing or unreadable.

An unreadable history costs a rollback target, not correctness, so it
degrades rather than raising.

### `_detect_install_mechanism`

```python
def _detect_install_mechanism(home: Path) -> str
```

Detects whether px0 is installed via pipx or pip.

### `run_update`

```python
def run_update(home: Path, config: dict, check_only: bool = False) -> dict
```

Entry point for `px0 update`. Performs PyPI check and upgrades using pipx/pip.

### `rollback`

```python
def rollback(home: Path, config: dict) -> None
```

Restores the previously installed px0 version from update history.

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
