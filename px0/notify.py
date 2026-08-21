"""Telling you a run failed.

A scheduled workflow that fails at 07:00 used to fail in silence: the record
was written, and nothing else happened. That caps what you can trust the
scheduler with, because the only way to learn about a failure was to go
looking for it.

Three channels, chosen by `notify.on_failure`:

- `none` (the default) stays silent, which is right for a manual run.
- `desktop` raises a local notification, needs nothing configured, and cannot
  leak anything off the machine.
- `tool` sends through `notify.channel`, a write tool the store has already
  authorized, to `notify.target`.

A workflow may override all of it with an `on_failure` block in frontmatter,
so the noisy hourly job can stay quiet while the nightly report shouts.
"""

import platform
import shutil
import subprocess

from px0 import config as config_mod

CHANNELS = ("none", "desktop", "tool")

# Tool ids that can carry a message, and the args each one needs. Anything else
# named as a channel is refused with this list, rather than a schema error from
# the far end of a Composio call.
MESSAGE_TOOLS = {
    "slack.post_message": lambda target, text: {"channel": target, "text": text},
    "gmail.send_message": lambda target, text: {"to": target, "subject": text.splitlines()[0][:120],
                                                 "body": text},
}


def _policy(config: dict, wf_on_failure: dict | None) -> dict:
    """Merges the workflow's on_failure block over the store's notify defaults."""
    policy = {
        "notify": (config_mod.get(config, "notify.on_failure", "") or "none").strip() or "none",
        "channel": config_mod.get(config, "notify.channel", "") or "",
        "target": config_mod.get(config, "notify.target", "") or "",
    }
    for key in ("notify", "channel", "target"):
        if wf_on_failure and wf_on_failure.get(key) not in (None, ""):
            policy[key] = str(wf_on_failure[key]).strip()
    return policy


def _desktop(title: str, body: str) -> tuple[bool, str]:
    """Raises a desktop notification on macOS or Linux, if either can."""
    system = platform.system()
    text = body.replace('"', "'")[:400]
    if system == "Darwin" and shutil.which("osascript"):
        script = f'display notification "{text}" with title "{title}"'
        cmd = ["osascript", "-e", script]
    elif shutil.which("notify-send"):
        cmd = ["notify-send", title, text]
    else:
        return False, "no desktop notifier available (osascript or notify-send)"
    try:
        subprocess.run(cmd, capture_output=True, timeout=10, check=False)
        return True, "desktop"
    except (OSError, subprocess.SubprocessError) as e:
        return False, f"desktop notification failed: {e}"


def message_for(record: dict) -> tuple[str, str]:
    """The title and body describing a failed run."""
    wf = record.get("workflow_id", "workflow")
    run_id = record.get("id", "")
    error = str(record.get("error") or "failed").strip().splitlines()[0][:300]
    attempts = record.get("attempts")
    tried = f" after {attempts} attempts" if attempts and attempts > 1 else ""
    title = f"px0: {wf} failed"
    body = (f"{wf} failed{tried}: {error}\n"
            f"Run {run_id}. See it with `px0 runs why {run_id}`.")
    return title, body


def on_failure(home, config: dict, record: dict, wf_on_failure: dict | None = None) -> dict:
    """Notifies about a failed run, per policy. Never raises.

    Returns what happened, which the caller records on the run so a silent
    failure to notify is still visible in `px0 runs show`.
    """
    policy = _policy(config, wf_on_failure)
    channel = policy["notify"]
    if channel in ("", "none"):
        return {"notified": False, "channel": "none"}

    title, body = message_for(record)

    if channel == "desktop":
        ok, detail = _desktop(title, body)
        return {"notified": ok, "channel": "desktop", "detail": detail}

    if channel != "tool":
        return {"notified": False, "channel": channel,
                "detail": f"unknown notify channel {channel!r}; expected one of {list(CHANNELS)}"}

    tool_id = policy["channel"]
    target = policy["target"]
    if not tool_id:
        return {"notified": False, "channel": "tool",
                "detail": "notify.on_failure is 'tool' but notify.channel names no tool"}
    if tool_id not in MESSAGE_TOOLS:
        return {"notified": False, "channel": "tool",
                "detail": f"{tool_id} cannot carry a message; use one of "
                          f"{', '.join(sorted(MESSAGE_TOOLS))}"}
    if not target:
        return {"notified": False, "channel": "tool",
                "detail": f"notify.target is empty, so {tool_id} has nowhere to send"}

    from px0 import tools as tools_mod

    args = MESSAGE_TOOLS[tool_id](target, f"{title}\n{body}")
    try:
        tools_mod.call(home, config, tool_id, args)
    except Exception as e:  # a failed notification must never mask the failure it reports
        return {"notified": False, "channel": "tool", "tool": tool_id, "detail": str(e)[:300]}
    return {"notified": True, "channel": "tool", "tool": tool_id, "target": target}
