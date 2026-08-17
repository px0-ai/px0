# Building a workflow

The starters that ship with `px0 init` cover the basics, but the real
point of px0 is that you can describe a workflow in plain English and
get a working file back. This walks through `px0 new` end to end.

## 1. Describe what you want

```shell
px0 new "every friday afternoon, summarize the PRs I reviewed this week and post it to #eng"
```

The builder turns the sentence into a plan: what the workflow does, what
inputs it needs, which tools it will call, and on what schedule. The plan
prints as JSON so you can see exactly what's about to be generated before
anything is written.

## 2. Review what it needs

Three things print after the plan, in order:

- **Write tools.** If the plan needs a tool that posts, comments, or
  otherwise changes something outside px0, it's called out explicitly --
  `this workflow would be granted write tools: [...]`.
- **Feasibility issues.** If the plan can't work as described (a tool
  that doesn't exist, an input that can't be resolved), the issues print
  and `px0 new` stops. Nothing is saved.
- **Missing connections.** If the plan needs a service you haven't
  connected yet, it's listed here. Connect it first:

  ```shell
  px0 connect github --native --pat <token>
  ```

  Only native GitHub executes in this build; other services print a
  message rather than connecting.

## 3. Confirm and name it

```
Generate this workflow? [y/N] y
workflow id [summarize-prs-reviewed-this-week]:
```

Accept the suggested id or type your own. Pass `--yes` and `--id <id>`
to skip both prompts for scripted use.

## 4. See what was picked for you

The builder also selects guideline files relevant to the description --
for a PR-summary workflow, that might be `pr-descriptions.md`. These are
printed as `guidelines selected: [...]` and written into the workflow
file's declared list, where you can edit or remove them by hand.

## 5. Inspect and run it

```shell
px0 list workflows
px0 run summarize-prs-reviewed-this-week --dry-run
```

The generated file is plain Markdown under `workflows/`. Open it, read
it, change the schedule or the guideline list, and re-run -- there's no
separate "compile" step.

## Next

- [03-knowledge-and-ask.md](03-knowledge-and-ask.md) -- build a personal
  knowledge library and query it.
- [04-guidelines-and-provenance.md](04-guidelines-and-provenance.md) --
  how guidelines evolve and how to trace any output back to its sources.
