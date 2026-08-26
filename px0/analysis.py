"""What a workflow's own runs say about it, computed from the records alone.

Everything here is arithmetic over `logs/records/`. No model call, no network,
nothing that can answer differently twice for the same input -- so a finding is
something you can argue with by reading the same records yourself. That
matters because the model-assisted half (`px0 workflows improve`) is handed
this report as its evidence: a proposal is only as honest as the numbers under
it, and numbers a model produced would be circular.

The vocabulary is deliberately small. A **problem** is something that is
costing the user output they wanted. A **note** is something worth knowing that
may well be fine. A finding is **fixable** when px0 can repair it mechanically,
which in practice means narrowing an allowlist or raising a timeout -- never
anything that changes what a workflow says or widens what it may reach.

Records are read, never written. `apply_fix` is the one exception and it edits
the workflow file, through the store's own versioning, so `px0 changes revert`
undoes it like any other edit.
"""

import re
import statistics
from collections import Counter, defaultdict
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path

import yaml

from px0 import config as config_mod, harness, paths, versioning
from px0 import runs as runs_mod
from px0 import workflow as workflow_mod

# How many runs a judgement needs before it is worth making. Below these, the
# report says so rather than calling two failures out of three a crisis.
MIN_RUNS_FOR_RATES = 3
MIN_RUNS_FOR_DEAD_TOOL = 5

# A tool erroring now and then is the network. A tool erroring a third of the
# time is the workflow.
TOOL_ERROR_RATE = 0.34
FAILURE_RATE_PROBLEM = 0.34
TURN_CAP_RATE = 0.5
RETRY_RATE = 0.25

# Digits, ids, timestamps and quoted fragments are what make two instances of
# the same error look like two different errors. Stripped before grouping.
# The digit rule is deliberately not anchored to word boundaries: "timed out
# after 30s" and "after 90s" differ inside a token, and a bounded \b\d+\b left
# them as two separate findings of one cause.
_NOISE = [
    (re.compile(r"\b[0-9a-f]{8,}\b", re.I), "<id>"),
    (re.compile(r"\b\d{4}-\d{2}-\d{2}[T ][\d:.+]+"), "<time>"),
    (re.compile(r"\d+"), "<n>"),
    (re.compile(r"'[^']{0,80}'"), "'<v>'"),
    (re.compile(r'"[^"]{0,80}"'), '"<v>"'),
    (re.compile(r"\s+"), " "),
]


def normalize_error(message: str) -> str:
    """Reduces an error message to the shape it shares with its siblings, so
    five failures of one cause group as one finding instead of five."""
    text = str(message or "").strip()
    for pattern, replacement in _NOISE:
        text = pattern.sub(replacement, text)
    return text.strip()[:200]


@dataclass
class Finding:
    """One thing the records say about a workflow.

    `fix` is what the user should run or do. `fixable` says px0 can do it
    itself, and `payload` carries what `apply_fix` needs to do it -- kept as
    data rather than a closure so a report survives being serialized to JSON
    and read back by something else.
    """
    code: str
    severity: str  # "problem" | "note"
    detail: str
    evidence: dict = field(default_factory=dict)
    fix: str = ""
    fixable: bool = False
    payload: dict = field(default_factory=dict)

    def as_dict(self) -> dict:
        return {"code": self.code, "severity": self.severity, "detail": self.detail,
                "evidence": self.evidence, "fix": self.fix, "fixable": self.fixable,
                "payload": self.payload}


def gather(config: dict, workflow_id: str, since: datetime | None = None,
           limit: int | None = None, records: list[dict] | None = None) -> list[dict]:
    """The runs a report is computed from, newest first.

    Dry runs are kept, because "this has only ever been rehearsed" is itself a
    finding, and dropped by every rate that would be distorted by them -- a
    rehearsal never calls a write tool, so counting one in a tool's error rate
    would be counting a call that never happened.

    `records` lets a caller that has already read the store's records hand them
    over to be filtered in memory. `overview` reports on every workflow at once,
    and re-reading every record file per workflow made a store with a few
    thousand runs take seconds to answer a question that is pure arithmetic.
    """
    if records is None:
        found = runs_mod.list_records(config, workflow=workflow_id, since=since)
    else:
        found = [r for r in records if r.get("workflow_id") == workflow_id]
    return found[:limit] if limit else found


def _live(records: list[dict]) -> list[dict]:
    """Runs that actually executed: a rehearsal is excluded from every rate."""
    return [r for r in records if not r.get("dry_run")]


def _tool_calls(records: list[dict]) -> list[tuple[dict, dict]]:
    """Every tool call across the given runs, paired with the run it came from."""
    return [(rec, call) for rec in records for call in (rec.get("tool_calls") or [])]


def _call_failed(call: dict) -> bool:
    """Whether one recorded tool call came back an error.

    Runs written before calls carried a `failed` flag are read the way the
    result was actually stored -- as the head of the repr of the result dict --
    so a report over an older window is not silently all-clear.
    """
    if "failed" in call:
        return bool(call["failed"])
    summary = str(call.get("result_summary", ""))
    return "'error'" in summary[:40] or '"error"' in summary[:40]


def health(home: Path, config: dict, workflow_id: str,
           since: datetime | None = None, limit: int | None = None,
           records: list[dict] | None = None) -> dict:
    """The full deterministic report for one workflow.

    Returns the counts a person would want to see alongside the findings, so
    `--json` is the same information the printed report is drawn from and
    nothing has to be recomputed to render it.
    """
    try:
        wf = workflow_mod.load(home, workflow_id)
    except workflow_mod.WorkflowError as e:
        return {"workflow": workflow_id, "error": str(e), "findings": [], "runs": {}}

    records = gather(config, workflow_id, since=since, limit=limit, records=records)
    live = _live(records)
    findings: list[Finding] = []

    outcomes = Counter(r.get("outcome", "unknown") for r in live)
    failed = outcomes.get("failed", 0)
    total = len(live)
    durations = [r["duration_seconds"] for r in live
                 if isinstance(r.get("duration_seconds"), (int, float))]

    summary = {
        "records": len(records),
        "live": total,
        "dry_runs": len(records) - total,
        "success": outcomes.get("success", 0),
        "failed": failed,
        "failure_rate": round(failed / total, 3) if total else None,
        "median_seconds": round(statistics.median(durations), 1) if durations else None,
        "slowest_seconds": round(max(durations), 1) if durations else None,
        "first": records[-1].get("start_time") if records else None,
        "last": records[0].get("start_time") if records else None,
        "versions": sorted({r.get("workflow_version") for r in records
                            if r.get("workflow_version")}),
    }
    summary.update(_usage_summary(live))

    for check in (
        _check_nothing_to_go_on, _check_dry_run_only, _check_failures,
        _check_silent_success, _check_refused_tools, _check_failing_tools,
        _check_dead_tools, _check_turn_cap, _check_retry_pressure,
        _check_timeouts, _check_empty_inputs, _check_marks,
        _check_version_churn, _check_estimated_cost, _check_parked,
    ):
        findings.extend(check(home, config, wf, records, live, summary))

    order = {"problem": 0, "note": 1}
    findings.sort(key=lambda f: (order.get(f.severity, 2), f.code))
    return {
        "workflow": workflow_id,
        "enabled": wf.enabled,
        "scheduled": bool((wf.trigger or {}).get("schedule")),
        "tools": list(wf.tools),
        "guidelines": list(wf.guidelines),
        "runs": summary,
        "findings": [f.as_dict() for f in findings],
        "ok": not any(f.severity == "problem" for f in findings),
    }


def _usage_summary(live: list[dict]) -> dict:
    """What the window cost, and whether anyone actually measured it."""
    reported = [r for r in live if (r.get("usage") or {}).get("reported")]
    tokens_estimated = sum((r.get("usage") or {}).get("estimated_tokens", 0) for r in live)
    out = {
        "cost_measured": bool(reported),
        "estimated_tokens": tokens_estimated or None,
    }
    if reported:
        out["input_tokens"] = sum((r["usage"]).get("input_tokens", 0) for r in reported)
        out["output_tokens"] = sum((r["usage"]).get("output_tokens", 0) for r in reported)
        cost = sum((r["usage"]).get("cost_usd", 0) or 0 for r in reported)
        out["cost_usd"] = round(cost, 4) if cost else None
    return out


# --- checks ---------------------------------------------------------------
#
# Each takes the same arguments and returns a list of findings, so the caller
# is a loop rather than a wall of conditionals, and a new check is one function
# plus one name in that tuple.


def _check_nothing_to_go_on(home, config, wf, records, live, summary) -> list[Finding]:
    if records:
        return []
    scheduled = bool((wf.trigger or {}).get("schedule"))
    detail = ("no runs on record in this window"
              + (" -- it is scheduled, so either the daemon is not running or "
                 "the window is shorter than its schedule" if scheduled else ""))
    return [Finding("no_runs", "note", detail,
                    fix="px0 status" if scheduled else f"px0 workflows run {wf.id}")]


def _check_dry_run_only(home, config, wf, records, live, summary) -> list[Finding]:
    if records and not live:
        return [Finding(
            "dry_run_only", "note",
            f"all {len(records)} run(s) here were rehearsals -- this has never run for real",
            evidence={"dry_runs": len(records)},
            fix=f"px0 workflows run {wf.id}")]
    return []


def _is_timeout(record: dict) -> bool:
    """Whether a failed run was killed by the clock rather than by an error.

    Its own check reports these, and can repair them, so the general failure
    grouping steps around them -- otherwise every timeout was reported twice,
    once as a failure and once as a timeout, and the second one was the useful
    one.

    Matched on the harness's own wording rather than on "timed out" anywhere in
    the message. A connector that times out is an ordinary failure: raising the
    workflow's `timeout:` would not help it, so it must not be diverted into
    the check whose whole purpose is to offer that repair.
    """
    return (record.get("outcome") == "failed"
            and "harness timed out" in str(record.get("error", "")))


def _check_failures(home, config, wf, records, live, summary) -> list[Finding]:
    failures = [r for r in live
                if r.get("outcome") == "failed" and not _is_timeout(r)]
    if not failures:
        return []
    groups = defaultdict(list)
    for rec in failures:
        groups[normalize_error(rec.get("error", ""))].append(rec)
    findings = []
    rate = len(failures) / len(live)
    for shape, group in sorted(groups.items(), key=lambda kv: -len(kv[1])):
        severity = ("problem" if len(group) > 1 or rate >= FAILURE_RATE_PROBLEM
                    else "note")
        stage = group[0].get("stage")
        # The shape is how these were grouped; one real message is what a
        # person can act on. `<n>` placeholders belong in the evidence, not in
        # the sentence someone reads.
        example = str(group[0].get("error") or "").strip() or "no message recorded"
        findings.append(Finding(
            "failing", severity,
            f"{len(group)} of {len(live)} run(s) failed: {example[:160]}",
            evidence={"count": len(group), "of": len(live), "stage": stage,
                      "shape": shape, "runs": [r["id"] for r in group[:5]]},
            fix=f"px0 runs why {group[0]['id']}"))
    return findings


def _check_silent_success(home, config, wf, records, live, summary) -> list[Finding]:
    """A run that succeeded and produced nothing.

    The most expensive kind of failure px0 has, because every listing shows it
    green. Two shapes count: an empty output, and a run whose every tool call
    errored but which still wrote whatever the model said around the errors.
    """
    empty, all_errored = [], []
    for rec in live:
        if rec.get("outcome") != "success":
            continue
        text = (rec.get("output") or {}).get("text")
        if text is not None and not str(text).strip():
            empty.append(rec)
        calls = [c for c in (rec.get("tool_calls") or []) if not c.get("stubbed")]
        if calls and all(_call_failed(c) for c in calls):
            all_errored.append(rec)
    findings = []
    if empty:
        findings.append(Finding(
            "empty_output", "problem",
            f"{len(empty)} run(s) succeeded but wrote nothing",
            evidence={"runs": [r["id"] for r in empty[:5]]},
            fix=f"px0 runs why {empty[0]['id']}"))
    if all_errored:
        findings.append(Finding(
            "success_despite_tool_errors", "problem",
            f"{len(all_errored)} run(s) recorded success with every tool call erroring -- "
            "the output was written from nothing",
            evidence={"runs": [r["id"] for r in all_errored[:5]]},
            fix=f"px0 runs why {all_errored[0]['id']}"))
    return findings


def _check_refused_tools(home, config, wf, records, live, summary) -> list[Finding]:
    """The model asked for a tool this workflow is not allowed to use.

    Never harmless. Either the instructions describe work the allowlist cannot
    do, or the model is wandering -- and until the allowlist bug was fixed, a
    refusal like this was recorded while the call went through anyway.
    """
    refused = Counter(call["tool"] for _, call in _tool_calls(records)
                      if call.get("refused"))
    if not refused:
        return []
    named = ", ".join(f"{t} ({n}x)" for t, n in refused.most_common(4))
    return [Finding(
        "tool_refused", "problem",
        f"the model reached for tools this workflow may not use: {named}",
        evidence={"tools": dict(refused), "allowed": list(wf.tools)},
        fix=f"px0 workflows improve {wf.id}")]


def _check_failing_tools(home, config, wf, records, live, summary) -> list[Finding]:
    stats: dict[str, list[bool]] = defaultdict(list)
    for _rec, call in _tool_calls(live):
        if call.get("stubbed") or call.get("refused"):
            continue
        stats[call.get("tool", "?")].append(_call_failed(call))
    findings = []
    for tool_id, results in sorted(stats.items()):
        if len(results) < MIN_RUNS_FOR_RATES:
            continue
        rate = sum(results) / len(results)
        if rate < TOOL_ERROR_RATE:
            continue
        findings.append(Finding(
            "tool_erroring", "problem",
            f"{tool_id} failed {sum(results)} of its {len(results)} calls",
            evidence={"tool": tool_id, "failures": sum(results), "calls": len(results),
                      "rate": round(rate, 2)},
            fix=f"px0 tools list --status"))
    return findings


def _check_dead_tools(home, config, wf, records, live, summary) -> list[Finding]:
    """Allowlisted tools the model has never once reached for.

    Every one of them is described in the prompt on every run, so a dead tool
    is a bill paid on every run for a capability nothing uses. Narrowing the
    allowlist is the one repair px0 will make mechanically, because it can only
    ever reduce what a workflow may do.
    """
    if len(live) < MIN_RUNS_FOR_DEAD_TOOL or not wf.tools:
        return []
    used = {call.get("tool") for _rec, call in _tool_calls(records)}
    dead = [t for t in wf.tools if t not in used]
    if not dead:
        return []
    return [Finding(
        "dead_tools", "note",
        f"{len(dead)} allowlisted tool(s) never called in {len(live)} run(s): "
        f"{', '.join(dead)}",
        evidence={"tools": dead, "runs": len(live)},
        fix=f"px0 workflows health {wf.id} --fix",
        fixable=True, payload={"drop_tools": dead})]


def _check_turn_cap(home, config, wf, records, live, summary) -> list[Finding]:
    capped = [r for r in live if (r.get("usage") or {}).get("hit_turn_cap")]
    if not capped or len(live) < MIN_RUNS_FOR_RATES:
        return []
    rate = len(capped) / len(live)
    if rate < TURN_CAP_RATE:
        return [Finding(
            "turn_cap", "note",
            f"{len(capped)} of {len(live)} run(s) used every tool-call turn available",
            evidence={"count": len(capped), "of": len(live)})]
    return [Finding(
        "turn_cap", "problem",
        f"{len(capped)} of {len(live)} run(s) ran out of tool-call turns -- "
        "the instructions ask for more steps than a run has",
        evidence={"count": len(capped), "of": len(live), "rate": round(rate, 2)},
        fix=f"px0 workflows improve {wf.id}")]


def _check_retry_pressure(home, config, wf, records, live, summary) -> list[Finding]:
    retried = [r for r in live if (r.get("attempt") or 1) > 1]
    if not retried or len(live) < MIN_RUNS_FOR_RATES:
        return []
    rate = len(retried) / len(live)
    if rate < RETRY_RATE:
        return []
    return [Finding(
        "retry_pressure", "note",
        f"{len(retried)} of {len(live)} run(s) needed a second attempt",
        evidence={"count": len(retried), "of": len(live), "rate": round(rate, 2)})]


_TIMED_OUT_AT = re.compile(r"harness timed out after ([\d.]+)\s*s")


def _timed_out_at(record: dict) -> float | None:
    """The limit a run actually hit, read out of its own error message.

    Needed because the workflow's `timeout:` says what the limit is *now*, and
    a window of runs can straddle a change to it. Blaming a raised timeout for
    failures that happened under the old one reads as "the fix did not work"
    when what happened is that the fix has not been tested yet.
    """
    match = _TIMED_OUT_AT.search(str(record.get("error", "")))
    try:
        return float(match.group(1)) if match else None
    except ValueError:
        return None


def _check_timeouts(home, config, wf, records, live, summary) -> list[Finding]:
    """Runs the clock killed, and what the timeout would have to be to survive.

    Fixable, because the repair is arithmetic: take the slowest run that did
    finish, leave half again on top, round up. It is the one fix here that
    changes behaviour rather than trimming it, so it stays behind the same
    confirmation as everything else.
    """
    timed_out = [r for r in live if _is_timeout(r)]
    if not timed_out:
        return []
    try:
        current = harness.parse_duration(wf.timeout)
    except ValueError:
        current = 120.0

    limits = [t for t in (_timed_out_at(r) for r in timed_out) if t is not None]
    if limits and max(limits) < current:
        # Every one of these died under a shorter timeout than the file now
        # carries: the repair has already been made and simply has not been
        # tried yet. Offering it again would be reporting a fixed problem.
        return [Finding(
            "timing_out", "note",
            f"{len(timed_out)} run(s) timed out at {max(limits):g}s, before the timeout "
            f"was raised to {wf.timeout} -- no run has hit the new limit yet",
            evidence={"count": len(timed_out), "hit_seconds": max(limits),
                      "timeout": wf.timeout})]

    completed = [r["duration_seconds"] for r in live
                 if r.get("outcome") == "success"
                 and isinstance(r.get("duration_seconds"), (int, float))]
    basis = max(completed) if completed else current
    suggested = max(int(current * 2), int(basis * 1.5) + 1)
    suggested = int(round(suggested / 30.0) * 30) or 30
    hit = f"{max(limits):g}s" if limits else wf.timeout
    return [Finding(
        "timing_out", "problem",
        f"{len(timed_out)} run(s) hit the {hit} timeout",
        evidence={"count": len(timed_out), "timeout": wf.timeout,
                  "hit_seconds": max(limits) if limits else None,
                  "slowest_completed_seconds": round(basis, 1),
                  "suggested": f"{suggested}s"},
        fix=f"px0 workflows health {wf.id} --fix",
        fixable=True, payload={"set_timeout": f"{suggested}s"})]


def _check_empty_inputs(home, config, wf, records, live, summary) -> list[Finding]:
    """Inputs that resolve, every time, to nothing.

    A quiet way for a workflow to rot: the query still runs, the run still
    succeeds, and the model writes a report around a hole. Only counted over
    runs recorded since inputs began carrying their size, so an older window
    reports nothing rather than reporting zero.
    """
    per_input: dict[str, list[bool]] = defaultdict(list)
    degraded: dict[str, int] = Counter()
    for rec in live:
        for meta in rec.get("inputs_resolved") or []:
            if meta.get("degraded"):
                degraded[meta.get("id", "?")] += 1
            if not meta.get("ok") or "empty" not in meta:
                continue
            per_input[meta.get("id", "?")].append(bool(meta["empty"]))
    findings = []
    for input_id, flags in sorted(per_input.items()):
        if len(flags) < MIN_RUNS_FOR_RATES or not all(flags):
            continue
        findings.append(Finding(
            "input_always_empty", "problem",
            f"input {input_id!r} has resolved to nothing on all {len(flags)} run(s) that "
            "recorded it -- the prompt is being built around a hole",
            evidence={"input": input_id, "runs": len(flags)},
            fix=f"px0 workflows show {wf.id}"))
    for input_id, count in degraded.most_common():
        if count < MIN_RUNS_FOR_RATES:
            continue
        findings.append(Finding(
            "input_degraded", "note",
            f"optional input {input_id!r} failed to resolve on {count} run(s)",
            evidence={"input": input_id, "runs": count}))
    return findings


def _check_marks(home, config, wf, records, live, summary) -> list[Finding]:
    """What the person said about the output, which nothing else can infer."""
    marked = [r for r in records if (r.get("review") or {}).get("verdict")]
    bad = [r for r in marked if r["review"]["verdict"] == "bad"]
    if not bad:
        return []
    notes = [r["review"]["note"] for r in bad if r["review"].get("note")]
    detail = f"{len(bad)} run(s) marked bad"
    if notes:
        detail += f": {notes[0][:120]}"
    return [Finding(
        "marked_bad", "problem", detail,
        evidence={"count": len(bad), "notes": notes[:5],
                  "runs": [r["id"] for r in bad[:5]]},
        fix=f"px0 workflows improve {wf.id}")]


def _check_parked(home, config, wf, records, live, summary) -> list[Finding]:
    """A workflow px0 parked because it kept failing the same way.

    Reported as a problem rather than a note: it is not firing, which is the
    whole point, and the only thing that will change that is a person.
    """
    if wf.enabled:
        return []
    streak = consecutive_failures(config, wf.id, records)
    if not streak["count"]:
        return []
    return [Finding(
        "parked", "problem",
        f"{wf.id} is disabled after {streak['count']} failures of one cause: "
        f"{streak['shape'] or 'no message recorded'}",
        evidence=streak,
        fix=f"px0 workflows enable {wf.id}")]


def _check_version_churn(home, config, wf, records, live, summary) -> list[Finding]:
    versions = summary.get("versions") or []
    if len(versions) < 2:
        return []
    return [Finding(
        "spans_versions", "note",
        f"these runs span {len(versions)} versions of the workflow, so rates here "
        "mix a file with the one it replaced",
        evidence={"versions": versions},
        fix=f"px0 changes list")]


def _check_estimated_cost(home, config, wf, records, live, summary) -> list[Finding]:
    """Whether what these runs cost was measured or guessed at."""
    if not live or summary.get("cost_measured"):
        return []
    cmd = harness.resolve_harness_cmd(
        config_mod.get(config, "model.harness_cmd", "claude -p"))
    if not harness.capabilities(cmd)["structured"]:
        return []  # this backend cannot report counts; nothing to suggest
    if str(config_mod.get(config, "model.output_format", "auto")) == "text":
        return [Finding(
            "cost_estimated", "note",
            "run cost here is px0's own estimate: model.output_format is pinned to text, "
            "so the harness is never asked for its token counts",
            fix="px0 config set model.output_format auto")]
    return []


def spend_today(config: dict, now: datetime | None = None) -> dict:
    """What runs have cost since midnight, measured where the harness reported
    it and estimated where it did not.

    Both numbers are returned rather than one blended figure, because a budget
    enforced against an estimate and a budget enforced against a bill are
    different promises and the caller should know which it is making.
    """
    from datetime import timedelta

    now = now or datetime.now()
    since = now.replace(hour=0, minute=0, second=0, microsecond=0)
    cost, tokens, measured = 0.0, 0, False
    for record in runs_mod.list_records(config, since=since):
        usage = record.get("usage") or {}
        if usage.get("reported"):
            measured = True
            cost += float(usage.get("cost_usd") or 0)
        tokens += int(usage.get("estimated_tokens") or 0)
    return {"cost_usd": round(cost, 4), "estimated_tokens": tokens,
            "measured": measured, "since": since.isoformat()}


def over_budget(config: dict) -> str | None:
    """Why this store should not start another run right now, or None.

    A watch on a busy source, a short poll interval, and a workflow that takes
    several model calls is a combination that spends real money without anyone
    watching. The ceiling is off by default -- a tool that refuses to work
    because of a number the user never set would be worse -- and exact when the
    harness reports costs, approximate when it does not.
    """
    limit = config_mod.get(config, "runs.daily_budget_usd", 0)
    token_limit = config_mod.get(config, "runs.daily_token_budget", 0)
    try:
        limit, token_limit = float(limit or 0), int(token_limit or 0)
    except (TypeError, ValueError):
        return None
    if limit <= 0 and token_limit <= 0:
        return None
    spent = spend_today(config)
    if limit > 0 and spent["cost_usd"] >= limit:
        return (f"today's runs have cost ${spent['cost_usd']}, at or past the "
                f"${limit} daily budget")
    if token_limit > 0 and spent["estimated_tokens"] >= token_limit:
        return (f"today's runs have used about {spent['estimated_tokens']:,} tokens, "
                f"at or past the {token_limit:,} daily budget")
    return None


# --- across every workflow ------------------------------------------------


def overview(home: Path, config: dict, since: datetime | None = None) -> dict:
    """One row per workflow, plus the same per-workflow findings rolled up.

    What `px0 workflows health` prints when given no id, and what `px0 status`
    folds its own line from. Runs of a workflow that no longer exists are kept
    in a separate bucket rather than dropped: they are still runs the user
    paid for, and their absence from the listing was confusing.
    """
    workflows = workflow_mod.load_all(home)
    records = runs_mod.list_records(config, since=since)
    by_workflow: dict[str, list[dict]] = defaultdict(list)
    for rec in records:
        by_workflow[rec.get("workflow_id") or "?"].append(rec)

    rows = []
    for wf_id in sorted(workflows):
        report = health(home, config, wf_id, since=since, records=records)
        problems = [f for f in report["findings"] if f["severity"] == "problem"]
        rows.append({
            "workflow": wf_id,
            "enabled": report.get("enabled", True),
            "runs": report["runs"].get("live", 0),
            "failed": report["runs"].get("failed", 0),
            "median_seconds": report["runs"].get("median_seconds"),
            "marked_bad": sum(1 for r in by_workflow.get(wf_id, [])
                              if (r.get("review") or {}).get("verdict") == "bad"),
            "problems": len(problems),
            "notes": len(report["findings"]) - len(problems),
            "headline": problems[0]["detail"] if problems else "",
            "findings": report["findings"],
        })

    orphans = {wf_id: len(recs) for wf_id, recs in by_workflow.items()
               if wf_id not in workflows}
    return {"workflows": rows, "orphan_runs": orphans,
            "total_runs": len(records),
            "problems": sum(r["problems"] for r in rows)}


# --- deterministic repair -------------------------------------------------


def fixable(report: dict) -> list[dict]:
    """The findings in a report that px0 can repair by itself."""
    return [f for f in report.get("findings", []) if f.get("fixable")]


def describe_fix(finding: dict) -> str:
    """One line saying exactly what applying this finding's fix would change."""
    payload = finding.get("payload") or {}
    if payload.get("drop_tools"):
        return f"drop {', '.join(payload['drop_tools'])} from this workflow's tools"
    if payload.get("set_timeout"):
        return f"raise the timeout to {payload['set_timeout']}"
    return finding.get("fix", "")


def consecutive_failures(config: dict, workflow_id: str,
                         records: list[dict] | None = None) -> dict:
    """How many runs in a row have failed, and whether of one cause.

    Counted from the newest backwards and stopped by the first success, which
    is the only reading that answers "is this broken *now*". A rate over a
    window cannot: a workflow that failed thirty times last week and has
    worked every day since has a terrible rate and nothing wrong with it.

    Rehearsals are skipped rather than counted or treated as successes -- a
    dry run says nothing about whether the real thing works.
    """
    found = (records if records is not None
             else runs_mod.list_records(config, workflow=workflow_id))
    streak, shape, ids = 0, None, []
    for record in found:
        if record.get("dry_run"):
            continue
        if record.get("outcome") != "failed":
            break
        current = normalize_error(record.get("error", ""))
        if shape is None:
            shape = current
        elif current != shape:
            break  # a different cause is a different problem, not a longer streak
        streak += 1
        ids.append(record.get("id"))
    return {"count": streak, "shape": shape or "", "runs": ids[:5]}


def breaker_limit(config: dict) -> int:
    """After how many identical consecutive failures a workflow parks itself.

    On by default, because the alternative is what px0 did before: a workflow
    whose connector died on Monday fires every hour for the rest of the week,
    fails every time, and notifies about each one. Nothing was learning from
    the fact that nothing had changed. Set to 0 to let it keep trying.
    """
    try:
        return max(0, int(config_mod.get(config, "runs.disable_after_failures", 5)))
    except (TypeError, ValueError):
        return 5


def should_trip_breaker(config: dict, workflow_id: str,
                        records: list[dict] | None = None) -> dict | None:
    """Whether this workflow has failed the same way often enough to be parked.

    Returns the streak that justifies it, or None. Deliberately requires the
    *same* cause each time: a workflow failing three different ways is one
    someone should look at, but it is not stuck in the way this exists to stop.
    """
    limit = breaker_limit(config)
    if limit <= 0:
        return None
    streak = consecutive_failures(config, workflow_id, records)
    return streak if streak["count"] >= limit else None


def set_enabled(home: Path, workflow_id: str, enabled: bool,
                reason: str = "", actor: str = "health") -> str | None:
    """Parks or unparks a workflow by rewriting one frontmatter key.

    Goes through the store's versioning like every other edit, so an
    automatic park shows up in `px0 changes list` next to the deliberate ones
    and is undone the same way.
    """
    path = paths.workflows_dir(home) / f"{workflow_id}.md"
    if not path.exists():
        raise workflow_mod.WorkflowError(f"no workflow file for {workflow_id!r}")
    text = path.read_text()
    parts = text.split("---", 2)
    if len(parts) < 3:
        raise workflow_mod.WorkflowError(f"{path.name}: malformed frontmatter")
    front = yaml.safe_load(parts[1]) or {}
    if bool(front.get("enabled", True)) == enabled:
        return None
    front["enabled"] = enabled
    body = parts[2].lstrip("\n")
    content = f"---\n{yaml.safe_dump(front, sort_keys=False).strip()}\n---\n{body.rstrip()}\n"
    path.write_text(content)
    return versioning.record_change(
        home, actor, [versioning.FileChange(f"workflows/{workflow_id}.md",
                                            content.encode(), reason)])


def apply_fixes(home: Path, config: dict, workflow_id: str,
                findings: list[dict]) -> dict:
    """Applies deterministic repairs to a workflow's frontmatter, as one change.

    Only two edits are ever made here -- dropping tools that were never called,
    and raising a timeout runs kept hitting -- and both are narrow enough to
    describe in a sentence before the user agrees to them. Nothing here touches
    the instruction body, adds a tool, or reaches a model: that is what
    `px0 workflows improve` is for, and it asks first.

    Written through `versioning.record_change`, so the previous file is in the
    store's history and `px0 changes revert` undoes it.
    """
    path = paths.workflows_dir(home) / f"{workflow_id}.md"
    if not path.exists():
        raise workflow_mod.WorkflowError(f"no workflow file for {workflow_id!r}")
    text = path.read_text()
    parts = text.split("---", 2)
    if len(parts) < 3:
        raise workflow_mod.WorkflowError(f"{path.name}: malformed frontmatter")
    front = yaml.safe_load(parts[1]) or {}
    body = parts[2].lstrip("\n")

    applied: list[str] = []
    for finding in findings:
        payload = finding.get("payload") or {}
        drop = payload.get("drop_tools")
        if drop:
            keep = [t for t in (front.get("tools") or []) if t not in set(drop)]
            if keep != (front.get("tools") or []):
                if keep:
                    front["tools"] = keep
                else:
                    front.pop("tools", None)
                applied.append(f"dropped {', '.join(drop)}")
        timeout = payload.get("set_timeout")
        if timeout:
            if front.get("timeout") != timeout:
                front["timeout"] = timeout
                applied.append(f"timeout {timeout}")

    if not applied:
        return {"changed": False, "applied": [], "change_id": None}

    rendered = yaml.safe_dump(front, sort_keys=False).strip()
    content = f"---\n{rendered}\n---\n{body.rstrip()}\n"
    path.write_text(content)
    change_id = versioning.record_change(
        home, "health", [versioning.FileChange(
            f"workflows/{workflow_id}.md", content.encode(),
            "; ".join(applied))])
    return {"changed": True, "applied": applied, "change_id": change_id}
