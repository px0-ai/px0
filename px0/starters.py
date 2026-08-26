"""Content for the store scaffolded by `px0 init`, and the ideas it offers.

px0 ships no workflows on purpose: a store full of things you did not ask for
is a store you have to read before you can trust it. But the consequence was a
cold start -- `px0 workflows new` opens an interview with a blank page, and the
hardest part of describing a job is knowing what sort of thing is describable.

So instead of shipping workflows, px0 ships *sentences*. Each recipe below is
what you would say during the interview, not a file that exists. Picking one
fills in the first answer and the interview proceeds exactly as it would have
if you had typed it -- which keeps every workflow in the store something the
user asked for, while removing the blank page.
"""

GUIDELINES: dict[str, str] = {}

WORKFLOWS: dict[str, str] = {}


# (id, the sentence, what it touches). Drawn from docs/workflow_usecases.md and
# kept short: this is a nudge, not a catalogue. The full 116 are in the docs,
# and `px0 workflows recipes --all` points there.
RECIPES: list[tuple[str, str, str]] = [
    ("friday-pr-digest",
     "Every Friday, summarize the pull requests I reviewed this week and post "
     "it to my team channel",
     "GitHub, Slack"),
    ("morning-brief",
     "Each weekday morning, brief me on today's meetings and the emails I have "
     "not replied to",
     "Google Calendar, Gmail"),
    ("error-to-ticket",
     "Turn last night's error spike into a triaged bug ticket",
     "Sentry, Linear"),
    ("release-notes",
     "Draft release notes from the commits since the last tag and file them as "
     "a page",
     "GitHub, Notion"),
    ("sprint-status",
     "Post a Monday sprint status from our issue tracker to the team channel",
     "Jira, Slack"),
    ("support-themes",
     "Group this week's support tickets by theme and open issues for the top "
     "three",
     "Zendesk, Linear"),
    ("reading-library",
     "Save every newsletter I star to my reading library",
     "Gmail, px0 brain"),
    ("deploy-report",
     "Run our deploy script and post what it printed",
     "shell, Slack"),
    ("weekly-reading",
     "Every Sunday, summarize what I read into my brain this week and write it "
     "to a file",
     "px0 brain, file"),
    ("standup",
     "Every weekday at 9am, draft my standup from yesterday's commits and hold "
     "it for me to approve before posting",
     "GitHub, Slack"),
]
