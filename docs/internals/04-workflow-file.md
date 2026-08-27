# 4. The workflow file

Module: `px0/workflow.py`

A workflow is one Markdown file. The YAML frontmatter is the machine contract; the body is the prompt the model receives at run time.

```markdown
---
id: friday-pr-digest
kind: workflow
version: 1
description: Summarize the pull requests I reviewed this week and post them to #eng
request: Every Friday, summarize the PRs I reviewed and post it to #eng-standup
trigger:
  manual: false
  schedule: "0 17 * * 5"
  timezone: Asia/Kolkata
guidelines:
  - digest-style.md
inputs:
  - id: prs
    tool: github.list_my_prs
    args:
      since: "{{now-7d}}"
tools:
  - slack.post_message
output:
  target: file
  path: digests/prs-{{today}}.md
timeout: 180s
---
Summarize `{{prs}}` into a short digest, then post it to #eng-standup.
```

## The parsed shape

`parse(path)` splits on the first two `---` delimiters, loads the frontmatter with `yaml.safe_load`, and fills a `Workflow` dataclass. Missing keys fall back to dataclass defaults, so an old file keeps parsing when a new field is added.

| Field | Type | What it means |
| ----- | ---- | ------------- |
| `id` | `str` | The name you run it by; defaults to the filename stem |
| `description` | `str` | The model's normalized restatement of the job |
| `request` | `str` | The sentence the user actually typed, kept verbatim |
| `trigger` | `dict` | `manual`, `schedule` (cron), `timezone`, `watch` |
| `enabled` | `bool` | `false` parks it: file and history stay, the daemon skips it |
| `on_failure` | `dict` | Overrides the store's `notify.*` settings |
| `retry` | `dict` | `max_attempts` and `backoff_seconds`, overriding `runs.*` |
| `confirm` | `bool` or `list` | Which writes wait for a person; `None` follows `tools.confirm_writes` |
| `capture` | `bool` | Whether a run keeps what its inputs resolved to |
| `guidelines` | `list[str]` | Files inlined verbatim into every run |
| `vars` | `list[dict]` | Values a run has to be given before it can start |
| `inputs` | `list[InputSpec]` | Context gathered before the prompt runs |
| `tools` | `list[str]` | What the model may call during the run |
| `output` | `dict` | `target`, `path`, `inbox` |
| `timeout` | `str` | Per-model-call ceiling, default `120s` |
| `pipeline` | `list` | Stages, when this workflow is a pipeline |
| `body` | `str` | Everything after the frontmatter: the prompt |

`request` and `description` are two fields on purpose. `description` is what the model wrote; `request` is what you said. `px0 workflows edit` shows the second back to you, because being shown a paraphrase of your own sentence and asked to revise it is confusing.

## Inputs versus tools

The distinction is the core of the format.

An entry in `inputs` runs before the prompt, unconditionally, to gather context. Its result is bound to `{{<id>}}` in the body. It must be read-only, and validation enforces that.

An entry in `tools` is offered to the model during the run. It may be a write tool. Nothing calls it unless the model asks for it.

`InputSpec.kind` is inferred from whichever field is set:

| Kind | Field | What resolution does |
| ---- | ----- | -------------------- |
| `tool` | `tool` + `args` | Calls the tool with rendered arguments |
| `retrieve` | `retrieve` | Runs a brain query and joins the passages |
| `source` | `source: stdin` | Reads what was piped in |
| `workflow` | `workflow` | Runs another workflow and takes its output text |

`optional: true` on an input means a failure resolves it to `None` and marks the run degraded, rather than aborting.

## The placeholder grammar

Two vocabularies used to exist here, and merging them was a bug fix.

Arguments accepted `{{now}}` and `{{today}}`. Output paths accepted only `{{date}}`, `{{datetime}}`, and `{{time}}`. A plan that wrote `logs/daily-{{today}}.md` was accepted everywhere except the one place it was used.

Now the names are one set:

| Placeholder | Resolves to |
| ----------- | ----------- |
| `{{now}}` | This instant |
| `{{today}}`, `{{date}}` | Today's date, `YYYY-MM-DD` |
| `{{datetime}}` | This instant, always filename-safe |
| `{{time}}` | The time of day |
| `{{now-24h}}` | 24 hours ago; units are `m`, `h`, `d`, `w` |

Formatting still differs by context. An argument gets ISO 8601 with a `Z`, which is what connectors' `since` and `until` parameters take. A path gets the same instant with colons swapped out, because a filename cannot hold them. What you may name is identical; how it renders is not.

Every digest workflow needs a window, and a scheduled run cannot be handed a literal timestamp, so `{{now-24h}}` is the only way to express one.

Arguments may also reference `{{config.<key>}}`, `{{input.<name>}}` from `--input`, and any input resolved above them. Nothing else. `_ARG_TEMPLATE_ROOTS` is that closed set, and the runner builds its context from exactly the same three sources, so what validation accepts and what a run can resolve cannot drift.

Output paths accept both `{{name}}` and `{name}`, because a plan that learned the double-brace form from the body carries the habit into the path, and a file literally named `report-{2026-08-17}.md` is nobody's intent.

## Validation

`validate(wf, home)` returns a list of human-readable strings. Empty means valid. It runs before a run touches the network, which is the whole point: an unfinished workflow should be named as one rather than producing a connector error about a repository called `<REPO>`.

### Unfinished arguments

`input_arg_errors` catches two shapes of "the plan gave up here", and both used to reach the connector as written.

A literal fill-me placeholder (`owner: <OWNER>`) is matched by `_PLACEHOLDER_ARG_RE`, which requires the angle brackets to be the entire value. That anchoring matters: Slack's own `<@U123>` and `<https://url|text>` syntax appears inside longer strings and must be left alone.

A template referencing something nothing provides (`author: {{github_username}}`) resolves to `None` at run time and is sent to the connector as a missing value. GitHub answers a `<OWNER>/<REPO>` fetch with a 404, which reads as a missing repository rather than as an unfinished workflow.

The same function runs over a plan the builder has not saved yet and over a workflow on disk. It takes plain dicts so both callers can use it, and takes the advice text as a parameter, because a build can regenerate and a run cannot.

`walk_strings` recurses into nested dicts and lists, so a placeholder buried in a sub-object is found and reported with a dotted location like `args.filter.repo`. It is public because `templates` walks the same values looking for literals to lift out, and two walkers over one structure would drift.

### Rules and their reasons

| Rule | Why |
| ---- | --- |
| Every `guidelines[]` entry must exist | An inlined file that is not there silently weakens every run |
| An input's tool must exist and be read-only | Inputs run unconditionally; a write there fires without the model deciding to |
| Every `tools[]` entry must exist | An unresolvable tool can only ever appear as a refused call |
| A scheduled or watched workflow's output must be `file` or `inbox` | Nobody is watching stdout at 6am. Posting via a tool call inside the body does not satisfy this |
| `trigger.timezone` must name a zone this machine knows | A silent fallback to local time fires at the wrong hour and looks like it worked |
| `confirm[]` entries must name this workflow's own tools | A misspelled entry means a write the user thought was held back gets sent, silently |
| `trigger.watch.tool` must be read-only | A watch only reads; it fires on what it sees |
| `trigger.watch.every` must be at least 60 seconds | Anything faster burns quota polling for a job that is waiting by definition |
| `retry.max_attempts` is capped at 10 | A retry policy survives a blip; it does not hammer a broken system all night |
| Pipelines cannot nest | One level of composition, checked here rather than at run time |
| Every `vars[]` entry needs a name and a description | The description is the whole point of declaring one: a var a stranger cannot interpret is worse than the literal it replaced |
| A `vars[]` entry must be referenced by something a run renders | Otherwise it is a knob the file advertises and no run reads, so the installer supplies a value and watches it change nothing |
| A scheduled or watched workflow cannot have a required var | Nothing passes `--input` to an unattended fire, so it would not run badly, it would fail every time, at 6am, having looked valid when it was written |
| `output.path` may only use clock placeholders | The path is rendered after the model call, so a typo would otherwise be found at the most expensive possible moment |

`output_path_errors` is the last one, and it exists because of when the path is rendered. A run resolves `output.path` in stage 7, after the model has already been paid for. Checking it at validation time turns "a failed run" into "a workflow that could never have succeeded", which is a different and more useful message.

## Vars, and what makes a workflow a template

A `vars:` block is the file format's answer to a workflow being shareable. Everything else in a workflow file describes a job; a var names a value that belongs to whoever is running it.

```yaml
vars:
  - name: repo
    description: The repository whose pull requests the digest covers, as owner/name
    values:
      - vercel/next.js
    default: null
```

`declared_vars(wf)` normalizes the block to `{name, description, values, default, required}`. Two decisions in that function are worth stating.

A var is required unless it carries a `default` or says `required: false`. That way round because the common case for a shared template is a value only the installer can know, and the safe reading of a value nobody supplied is to refuse the run rather than to send something somewhere with a blank in it.

The `values` list is examples, and is never enforced. It exists so a stranger can pattern-match the shape of what to put there. Enforcing them would mean treating a model's plausible list of repository names as an authority on which repositories exist, which it is not.

`references_var(wf, name)` decides whether a var is used, and it only counts the two surfaces a run actually renders: an input's `args`/`retrieve`, and the body. A var mentioned in the `description` is not referenced, because nothing will ever substitute it. That is why the validation rule is worth having rather than pedantic.

`var_values(wf, cli_inputs)` returns `(defaults to contribute, required vars nobody supplied)`. An empty string counts as not supplied: `--input channel=` is a mistake every time, since nothing useful is ever named by the empty string, and the alternative is a connector being asked to post to a channel called nothing.

`missing_vars_message(wf, missing)` is one line naming what is missing and the exact command that supplies it. It is what the run refusal prints, and it is here rather than in the CLI because the daemon and the MCP surface hit the same refusal.

Where these are enforced is [part 6](06-running.md); where the block gets written is [part 5](05-building.md).

## Loading

`load_all(home)` reads every `*.md` under `workflows/` recursively, keyed by id, and skips files that fail to parse. `load_errors(home)` reports those separately.

That split is a bug fix. Raising on a bad file meant one YAML typo took down `workflows list`, `doctor`, and the daemon at once. `strict=True` restores the raising behaviour for callers that want it.

`load(home, workflow_id)` distinguishes "not there" from "there but unparseable". If the lookup misses but a file with the matching stem exists, it re-parses that file so the real error surfaces instead of a misleading absence.

## Pipelines

A workflow with `pipeline:` set runs other workflows in sequence, piping each stage's output text into the next stage's stdin.

`pipeline_stages` normalizes both shapes the field can take. A plain list of ids is every existing pipeline in every store, so it stays the default reading and means `when: always`. A list of mappings can attach a condition:

```yaml
pipeline:
  - collect-errors
  - workflow: file-tickets
    when: has_output
```

Conditions are limited to `always`, `has_output`, and `no_output`. All three are facts about the previous stage's output that px0 can check itself. Anything richer would be a small language living in frontmatter, and the place for judgement about what to do next is a workflow body, which is written in English and read by a model.

A condition on stage 0 is refused: there is no previous output for it to test, so it can only be a mistake about which stage it belongs to.

## Policy resolution

Two helpers resolve the per-workflow override against the store default, and both clamp.

`retry_policy(wf, config)` returns `(max_attempts, backoff_seconds)`, with the workflow's own block winning, falling back to `runs.max_attempts` and `runs.retry_backoff_seconds`, and clamping attempts into `[1, MAX_ATTEMPTS]`.

`watch_spec(wf)` returns a normalized watch block or `None`, converting `every` to seconds with a floor of `MIN_WATCH_SECONDS`. An unparseable duration falls back to 900 seconds rather than failing, because the daemon reads this on every tick.

## Next

[Part 5](05-building.md) covers how one of these files gets written from a sentence.
