# Workflow use cases

116 jobs px0 could run, each written as the sentence you would tell `px0 workflows new` during its interview. This page is a catalogue of ideas, not a set of shipped workflows: px0 ships none, and every row here is one command away from existing in your store.

Toolkit names come from Composio's live catalogue, which held 1,364 toolkits when this page was written. Use `px0 tools list` to see what your store can already call.

## How to read a row

- The first column is the sentence to say when px0 asks. Write it the way you would ask a colleague: what should happen, to what, and when.
- The second column reads left to right as `reads -> writes`. Everything left of the arrow is a read-only tool px0 resolves as an `inputs` entry. Everything right of it is a write tool declared under `tools`, which is the only place a workflow may post, send, or file anything.
- `retrieve` on the left means brain retrieval rather than a connector. `file` on the right means the run's output goes to a file under `output/` and nothing is sent.
- `+ guideline` marks a row whose output only lands if it sounds like you, so it should inline a file from `guidelines/`.
- The third column is the cadence the sentence implies. `on demand` means no `trigger.schedule`: you run it yourself.

## What the workflow format supports

Every row below is a variation on five moves. The constraint column is what decides whether an idea is buildable.

| Move                 | What it gives a workflow                                       | The constraint that shapes it                                     |
| -------------------- | -------------------------------------------------------------- | ----------------------------------------------------------------- |
| `inputs: tool`       | Pull facts before the model runs: PRs, tickets, events, rows    | Read-only tools only. A write tool in `inputs` fails validation   |
| `inputs: retrieve`   | Ground the answer in your brain, cited by path and anchor       | Nothing under `brain/work/` is retrieved                          |
| `inputs: workflow`   | One workflow calls another and uses its text as context         | The sub-run's output stays in memory and is never written         |
| `tools:`             | The only place a workflow may post, send, file, or update       | Shown to you as writes at build time, so delivery stays explicit  |
| `guidelines:`        | Inlines your conventions verbatim, so output sounds like you    | The file must exist under `guidelines/` or the workflow is invalid |
| `trigger.schedule`   | Cron, run by the daemon                                         | A scheduled workflow's `output.target` must be `file`             |
| `pipeline:`          | Chains whole workflows into one job                              | One level deep. A stage that is itself a pipeline is rejected     |

One consequence is worth stating plainly. Because a scheduled run must write its output to a file, "post it to Slack" is never `output.target`. It is a Slack write tool the workflow calls itself. Every row below is written that way.

Check any of them before letting it act:

```shell
px0 workflows run friday-pr-digest --dry-run
```

## What is reachable today

Every toolkit in Composio's catalogue can be authorized, so any row here is
buildable. Authorize an app deliberately, or let the first run that needs it hand
you the URL:

```shell
px0 tools search --toolkits linear
px0 tools connect linear
px0 tools list --status
```

Rows that reach this machine rather than an app use px0's local tools:
`file.read`, `file.write`, `file.list`, `http.get`, `http.post`, `brain.add`, and
`shell.run`, which stays off until `tools.allow_shell` is true. Anything else you
need, declare as your own tool in the store's `tools/` folder.

Two things shape the cadence column:

- `trigger.schedule` is cron. For a row that should fire on an event rather than
  on the clock, `trigger.watch` polls a read-only tool and runs the workflow when
  an item it has not seen before appears.
- A scheduled or watched workflow writes its output to a file, so delivery is
  always a write tool the workflow calls itself.

## Contents

- [Engineering: code and change](#engineering-code-and-change) (13)
- [Developer productivity](#developer-productivity) (16)
- [Engineering management](#engineering-management) (18)
- [Incidents and reliability](#incidents-and-reliability) (9)
- [Product and analytics](#product-and-analytics) (9)
- [Customer support and customer voice](#customer-support-and-customer-voice) (8)
- [Sales, CRM, and revenue](#sales-crm-and-revenue) (8)
- [Finance and billing](#finance-and-billing) (7)
- [Operations, people, hiring](#operations-people-hiring) (7)
- [Marketing and content](#marketing-and-content) (8)
- [Personal operations](#personal-operations) (7)
- [Brain-grounded workflows](#brain-grounded-workflows) (6)
- [Compositions worth building as pipelines](#compositions-worth-building-as-pipelines)

## Engineering: code and change

Repository chores: reviews, releases, CI, and the reports nobody writes by hand twice. `github` alone exposes 871 tools, so the digest shape generalises well past a Friday summary.

| What you would type                                                                        | Apps (reads -> writes)               | Cadence      |
| ------------------------------------------------------------------------------------------ | ------------------------------------ | ------------ |
| Summarize the pull requests I reviewed this week and post it to #eng-standup               | `github` -> `slack`                  | Fri 17:00    |
| List PRs open more than three days with no review and nudge each author in a DM            | `github` -> `slack`                  | daily 10:00  |
| Draft release notes from the commits since the last tag and file them as a page            | `github` -> `notion`                 | on demand    |
| Precheck my open PR against my Go review conventions and leave inline comments             | `github` -> `github` + guideline     | on demand    |
| Report which merge queues stalled this week and what blocked them                          | `github` -> `slack`                  | Mon 09:00    |
| Turn last week's failed CI runs into a flaky-test list with counts, and file the top three | `github` -> `linear`                 | Mon 09:30    |
| Write this month's changelog page from merged PR titles, grouped by user-visible change    | `github` -> `confluence`             | 1st of month |
| Flag feature flags still on after 60 days, with the PR that introduced each                | `launch_darkly`, `github` -> `slack` | Wed weekly   |
| List merge requests waiting on my group and rank them by how long they have waited         | `gitlab` -> `slack`                  | daily 09:00  |
| Tell me what shipped to production yesterday, per service, with the deploy author          | `vercel`, `github` -> `slack`        | daily 08:30  |
| Diff our Postman collection against the committed OpenAPI spec and open an issue per drift | `postman` -> `github`                | Thu weekly   |
| Report strings added this week that have no translation yet, per locale                    | `crowdin` -> `jira`                  | Fri 12:00    |
| Summarize the review comments I received this month into what I keep getting wrong         | `github` -> `file`                   | monthly      |

## Developer productivity

One engineer's own day rather than reports about code. These assemble the context before you sit down, so the first twenty minutes go to work instead of to reconstructing where you left off. Most are read-heavy with a single small write, which makes them the cheapest useful workflows to build.

| What you would type                                                                                  | Apps (reads -> writes)                         | Cadence            |
| ---------------------------------------------------------------------------------------------------- | ---------------------------------------------- | ------------------ |
| Restore my context: the branches, PRs, and tickets I touched yesterday, and what each was waiting on | `github`, `linear` -> `file`                   | daily 08:00        |
| Build one ranked queue for today from my assigned tickets, review requests, and mentions             | `github`, `jira`, `slack` -> `file`            | daily 08:30        |
| List what I am blocked on and who owns each unblock, so I can chase it in one message                | `github`, `jira` -> `slack`                    | daily 09:30        |
| Triage my GitHub notifications into act now, read later, and ignore, with reasons                    | `github` -> `file`                             | daily 09:00, 15:00 |
| Draft the PR description from this branch's diff, following my PR conventions                        | `github` -> `github` + guideline               | on demand          |
| Write my standup update from my commits, reviews, and yesterday's messages                           | `github`, `slack` -> `slack` + guideline       | daily 09:45        |
| Tell me which of my branches are merged, stale, or duplicated, and which to delete                   | `github` -> `file`                             | Fri 17:30          |
| Report my review load: how many reviews I owe versus how many I am owed                              | `github` -> `slack`                            | Mon, Thu 10:00     |
| Batch this week's dependency PRs into one note: what upgraded, what looks risky, what to skip        | `github` -> `github`                           | Tue 11:00          |
| Summarize test failures on my branches and separate mine from flaky infrastructure                   | `github` -> `file`                             | daily 12:00        |
| Turn this ticket into a design-doc skeleton, pulling in what I have already read on the topic        | `retrieve`, `jira` -> `notion`                 | on demand          |
| Give me a tour of this repository: entry points, hot files, who last touched what                    | `github` -> `outline`                          | on demand          |
| Report where my coding hours actually went last week against what I planned                          | `wakatime`, `googlecalendar` -> `googlesheets` | Fri 18:00          |
| Find two focus blocks in next week's calendar and hold them before anyone else does                  | `googlecalendar` -> `googlecalendar`           | Fri 16:00          |
| Extract the recurring lessons from review comments I received this month                             | `github` -> `file`                             | monthly            |
| Assemble my brag doc entry for the quarter: what I shipped, reviewed, fixed, and unblocked           | `github`, `jira` -> `googledocs` + guideline   | quarterly          |

## Engineering management

A manager's recurring work is assembly: the same facts out of four systems, shaped into a status, a one-on-one, or a decision. Automate the assembly and keep the judgement.

| What you would type                                                                              | Apps (reads -> writes)                                    | Cadence             |
| ------------------------------------------------------------------------------------------------ | --------------------------------------------------------- | ------------------- |
| Post a Monday sprint status: what is done, what slipped, and what nobody has started             | `jira` -> `slack`                                         | Mon 09:00           |
| Prepare my one-on-one pack for each report: their week's work, wins, blockers, and open threads  | `github`, `jira` -> `file`                                | daily 07:30         |
| Write the weekly team report for leadership: shipped, at risk, incidents, and asks               | `jira`, `github`, `pagerduty` -> `googledocs` + guideline | Thu 17:00           |
| List cross-team dependencies we are waiting on and how long each has been waiting                | `jira` -> `slack`                                         | Tue, Fri 10:00      |
| Report the team's cycle time and review latency this sprint against the last three               | `github`, `jira` -> `slack`                               | sprint end          |
| Flag work in progress that has stalled: assigned, untouched for a week, still open               | `linear` -> `slack`                                       | Wed 10:00           |
| Check next sprint's capacity against booked leave, holidays, and on-call rotation                | `bamboohr`, `pagerduty`, `jira` -> `slack`                | Thu before planning |
| Show how pages were distributed across the team last month and whether it was fair               | `pagerduty` -> `googlesheets`                             | monthly             |
| Chase overdue action items from every postmortem, with the owner and the original incident       | `notion`, `jira` -> `slack`                               | Mon 11:00           |
| Report which epics slipped their target date this week and what moved them                       | `jira` -> `confluence`                                    | Fri 14:00           |
| Age the tech-debt backlog and name the three items that keep causing incidents                   | `github`, `sentry` -> `notion`                            | monthly             |
| Audit our recurring meetings: attendee hours per week, and the three cheapest to cancel          | `googlecalendar` -> `googlesheets`                        | monthly             |
| Summarize the retro into themes and open one issue per agreed action                             | `dovetail`, `miro` -> `linear`                            | sprint end          |
| Report interviewer load and which loops are short of a panel this week                           | `greenhouse`, `googlecalendar` -> `slack`                 | Mon 08:00           |
| Track each new joiner at day seven, thirty, and sixty: first PR, first deploy, gaps              | `github`, `bamboohr` -> `file`                            | weekly              |
| Assemble the evidence pack for each report's review from their own shipped work this half        | `github`, `jira` -> `googledocs` + guideline              | review cycle        |
| Report seats and spend on our developer tools and what is unused                                 | `ramp`, `github` -> `slack`                               | monthly             |
| Draft the project update our stakeholders keep asking for, from the tracker and the incident log | `clickup`, `sentry` -> `notion` + guideline               | Wed 16:00           |

## Incidents and reliability

`pagerduty` carries 356 tools and `sentry` 209, which covers the whole on-call paper trail: handover, triage, and the weekly review pack.

| What you would type                                                                       | Apps (reads -> writes)            | Cadence     |
| ----------------------------------------------------------------------------------------- | --------------------------------- | ----------- |
| Turn last night's error spike into a triaged bug ticket with the stack trace summarized   | `sentry` -> `linear`              | daily 07:00 |
| Write my on-call handover: what paged in the last twelve hours and what is still open     | `pagerduty`, `sentry` -> `slack`  | daily 09:00 |
| Build the weekly incident review pack: pages, time to acknowledge, worst services         | `pagerduty` -> `googledocs`       | Mon 08:00   |
| Report error-budget burn per service and name the two closest to breach                   | `datadog` -> `slack`              | daily 08:00 |
| Group this week's alerts by likely root cause instead of by alert name                    | `new_relic` -> `jira`             | Fri 16:00   |
| Draft the post-incident timeline from the incident channel and the page log               | `slack`, `pagerduty` -> `notion`  | on demand   |
| List every downtime window last week and the endpoint that caused it                      | `better_stack` -> `slack`         | Mon 07:30   |
| Find the twenty loudest log lines this week and open one cleanup issue for the worst      | `datadog` -> `github`             | Wed weekly  |
| Cross-check open incidents against the change log and say which deploy likely caused each | `servicenow`, `github` -> `slack` | daily 10:00 |

## Product and analytics

The dashboards already exist. What nobody writes weekly is the sentence explaining what moved, which is the shape a guideline improves most.

| What you would type                                                                            | Apps (reads -> writes)             | Cadence     |
| ---------------------------------------------------------------------------------------------- | ---------------------------------- | ----------- |
| Report the funnel's three biggest drop-offs this week against last, and what changed near them | `posthog` -> `slack`               | Mon 09:00   |
| Fourteen days after launch, tell me who adopted the new feature and who bounced off it         | `amplitude` -> `notion`            | on demand   |
| Append yesterday's core numbers to the metrics sheet and flag anything outside two sigma       | `googlebigquery` -> `googlesheets` | daily 06:00 |
| Turn our Metabase weekly questions into a written readout, not a screenshot                    | `metabase` -> `slack`              | Mon 08:30   |
| Say which running experiments crossed significance and which should be stopped                 | `posthog` -> `linear`              | Tue weekly  |
| List search queries that returned nothing this week, ranked by volume                          | `algolia` -> `jira`                | Fri 11:00   |
| Summarize rage-click and dead-click hotspots by page                                           | `posthog` -> `slack`               | Wed weekly  |
| Flag traffic anomalies against the four-week baseline, by channel and landing page             | `google_analytics` -> `slack`      | daily 07:30 |
| Compile the quarterly board metrics narrative from the warehouse and last quarter's doc        | `databricks` -> `googledocs`       | quarterly   |

## Customer support and customer voice

`zendesk` is the second-deepest toolkit in the catalogue at 451 tools. The useful automations convert ticket volume into engineering intent: themes, not counts.

| What you would type                                                                | Apps (reads -> writes)             | Cadence     |
| ---------------------------------------------------------------------------------- | ---------------------------------- | ----------- |
| Group this week's support tickets by theme and open issues for the top three       | `zendesk` -> `linear`              | Fri 15:00   |
| List first-response SLA breaches by queue and who owns each                        | `freshdesk` -> `slack`             | daily 09:30 |
| Turn repeated how-do-I questions into a docs backlog, one page stub each           | `intercom` -> `notion`             | Mon weekly  |
| Cluster this month's survey verbatims by cause and quote the sharpest three        | `delighted` -> `slack`             | monthly     |
| Flag open tickets from enterprise accounts and attach the account's renewal date   | `zendesk`, `salesforce` -> `slack` | daily 08:00 |
| Find feature requests mentioned by three or more customers this month              | `canny` -> `linear`                | monthly     |
| Write the customer-facing note for what we fixed that customers actually asked for | `zendesk`, `github` -> `notion`    | Thu weekly  |
| Summarize what the shop's angry reviews and tickets agree on                       | `gorgias`, `shopify` -> `slack`    | Fri weekly  |

## Sales, CRM, and revenue

Pipeline hygiene and call preparation, assembled from the CRM plus whatever else touched the account.

| What you would type                                                                 | Apps (reads -> writes)                          | Cadence     |
| ----------------------------------------------------------------------------------- | ----------------------------------------------- | ----------- |
| Report which deals changed stage this week and which slipped out of the quarter     | `hubspot` -> `slack`                            | Mon 08:00   |
| Nudge owners about deals with no activity in fourteen days, one DM per owner        | `pipedrive` -> `slack`                          | Tue 09:00   |
| Before each call today, brief me on the account: history, open tickets, last emails | `salesforce`, `zendesk`, `gmail` -> `gmail`     | daily 07:00 |
| Write the won-and-lost review with the reason each deal actually turned             | `hubspot` -> `googledocs`                       | Fri 16:00   |
| Enrich this week's inbound signups and drop the ones with no company match          | `hunter`, `peopledatalabs` -> `hubspot`         | daily 11:00 |
| Pull yesterday's call recordings into CRM notes and next steps                      | `gong`, `fireflies` -> `salesforce`             | daily 20:00 |
| List contracts sent but unsigned for more than five days, with who to chase         | `docusign` -> `slack`                           | daily 10:00 |
| Draft the follow-up email for every meeting I had yesterday, in my voice            | `googlecalendar`, `gong` -> `gmail` + guideline | daily 08:00 |

## Finance and billing

`stripe` carries 425 tools and `workday` 567, so coverage is rarely the constraint. These are the workflows whose write tool deserves the most scrutiny before you authorize it.

| What you would type                                                              | Apps (reads -> writes)        | Cadence      |
| -------------------------------------------------------------------------------- | ----------------------------- | ------------ |
| Log this week's revenue and refunds into the finance sheet                       | `stripe` -> `googlesheets`    | Fri 18:00    |
| List failed payments worth retrying and draft the recovery email for each        | `stripe` -> `gmail`           | daily 09:00  |
| Write the monthly recurring-revenue movement: new, expansion, contraction, churn | `stripe`, `maxio` -> `notion` | 1st of month |
| Report invoice aging and who to chase, oldest first                              | `quickbooks` -> `slack`       | Mon 09:00    |
| Flag card spend outside each team's monthly budget, with the merchant            | `ramp`, `brex` -> `slack`     | weekly       |
| Reconcile yesterday's settlements against our ledger and report only the gaps    | `razorpay` -> `googlesheets`  | daily 07:00  |
| Summarize the accounts closing this month and which have unsigned paperwork      | `xero`, `docusign` -> `slack` | monthly      |

## Operations, people, hiring

Recruiting loops, leave, timesheets, and vendor paperwork: high-friction, low-judgement, and almost entirely assembly.

| What you would type                                                                | Apps (reads -> writes)                   | Cadence     |
| ---------------------------------------------------------------------------------- | ---------------------------------------- | ----------- |
| Name candidates stuck in a stage for more than five days and whose action it is    | `greenhouse` -> `slack`                  | daily 09:00 |
| Report the hiring funnel by role with pass-through rates per stage                 | `lever` -> `googlesheets`                | Mon weekly  |
| Assemble the day-one pack for each new joiner: accounts, docs, first-week meetings | `bamboohr` -> `notion`, `googlecalendar` | daily 06:00 |
| Check next sprint's dates against booked time off and flag the collisions          | `bamboohr`, `jira` -> `slack`            | Thu weekly  |
| Chase whoever has not logged hours, one DM each, no channel shaming                | `clockify` -> `slack`                    | Fri 17:00   |
| List vendor contracts expiring in the next forty-five days and their owner         | `docusign` -> `slack`                    | Mon weekly  |
| Summarize this week's interview scorecards into a hiring-committee brief           | `ashby` -> `googledocs`                  | Thu 18:00   |

## Marketing and content

Campaign reporting, competitor watching, and turning one published piece into the versions each platform wants.

| What you would type                                                                | Apps (reads -> writes)               | Cadence     |
| ---------------------------------------------------------------------------------- | ------------------------------------ | ----------- |
| Draft next week's content calendar from the backlog, in the voice I write in       | `notion` -> `notion` + guideline     | Fri 14:00   |
| Turn the post I published into five platform-specific versions, queued not sent    | `firecrawl` -> `typefully`, `buffer` | on demand   |
| Report campaign performance: opens, clicks, unsubscribes, and the subject that won | `mailchimp`, `klaviyo` -> `slack`    | Tue weekly  |
| List keywords that dropped more than three positions this week                     | `dataforseo` -> `googlesheets`       | Mon 07:00   |
| Watch our competitors' pricing pages and report only what actually changed         | `firecrawl` -> `slack`               | daily 08:00 |
| Split webinar registrants into attendees and no-shows and draft each follow-up     | `zoom` -> `mailchimp`                | on demand   |
| Summarize what people said about us on Reddit and Hacker News this week            | `reddit`, `exa` -> `slack`           | Fri 12:00   |
| Report which paid campaigns are burning budget below target conversion             | `metaads` -> `slack`                 | daily 09:00 |

## Personal operations

The smallest workflows, and day to day the ones you notice most. They also need the fewest connectors: three of the four toolkits px0 can authorize today cover nearly all of this section.

| What you would type                                                            | Apps (reads -> writes)              | Cadence     |
| ------------------------------------------------------------------------------ | ----------------------------------- | ----------- |
| Brief me on today's meetings and the emails I have not replied to              | `googlecalendar`, `gmail` -> `file` | daily 07:00 |
| At the end of the day, list everything I said I would do in Slack today        | `slack` -> `todoist`                | daily 18:30 |
| Show inbox debt: threads waiting on me for more than three days, oldest first  | `gmail` -> `slack`                  | daily 16:00 |
| Audit last week's calendar: meeting hours, focus hours, and the worst offender | `googlecalendar` -> `googlesheets`  | Mon 08:00   |
| For each meeting tomorrow, attach the last three emails with those people      | `googlecalendar`, `gmail` -> `file` | daily 19:00 |
| Pull receipts out of my mail this week and add a row per expense               | `gmail` -> `googlesheets`           | Sun 20:00   |
| Assemble the travel pack for tomorrow's trip: flights, hotel, times, documents | `gmail`, `googlecalendar` -> `file` | daily 20:00 |

## Brain-grounded workflows

These need no connector. `inputs: retrieve` and a guideline are the whole apparatus, so they run offline and cost nothing to authorize. Start here.

| What you would type                                                          | Apps (reads -> writes)             | Cadence   |
| ---------------------------------------------------------------------------- | ---------------------------------- | --------- |
| Digest what I saved this week and name the thread running through it         | `retrieve` -> `file`               | Sun 18:00 |
| Review this design doc against the notes I have kept on distributed systems  | `retrieve`, `github` -> `github`   | on demand |
| Draft a blog post from the three papers I read on backpressure, citing each  | `retrieve` -> `file` + guideline   | on demand |
| Build an interview question sheet from my saved system-design notes          | `retrieve` -> `file`               | on demand |
| Tell me what I have read that contradicts the claim in this PR description   | `retrieve`, `github` -> `file`     | on demand |
| Answer support questions in my words, using only what my brain actually says | `retrieve`, `zendesk` -> `zendesk` | on demand |

## Compositions worth building as pipelines

A pipeline is one job made of workflows, one level deep. That constraint suits a specific shape: several narrow collectors, then one writer that reads all of them. Building it as stages keeps each collector runnable, and debuggable with `--dry-run`, on its own.

| Pipeline                | Stages                                                                | Why it is a pipeline                                                       |
| ----------------------- | --------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Friday wrap             | `pr-digest` -> `incident-digest` -> `ticket-themes` -> `weekly-post`  | Three read-only collectors and one write. Each stage survives mid-week      |
| Release train           | `commits-since-tag` -> `changelog-draft` -> `release-page`             | The draft stage holds the guideline, so the voice is fixed in one place     |
| Morning desk            | `calendar-brief` -> `inbox-debt` -> `overnight-alerts` -> `morning-brief` | One 07:00 cron instead of four, and one file to read                    |
| Manager Monday          | `sprint-status` -> `blocked-list` -> `stalled-wip` -> `team-status`    | The collectors are the ones an engineer runs alone, reused at team scope    |
| Monthly business review | `revenue-movement` -> `funnel-movement` -> `support-themes` -> `mbr-doc` | Only the writer stage touches a document, so the write surface is one tool |

## Related pages

- [`px0 workflows`](commands/workflows.md) for the commands that build and run these.
- [`px0 tools`](commands/tools.md) for what your store can call, and what is authorized.
- [`px0 brain`](commands/brain.md) for the retrieval the brain-grounded rows use.
- [`px0 guidelines`](commands/guidelines.md) for the files the `+ guideline` rows inline.
