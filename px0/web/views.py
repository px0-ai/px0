"""HTML views and HTMX partial templates for the px0 web dashboard."""

import html
import json
from datetime import datetime
from typing import Any
from croniter import croniter

from px0 import workflow as workflow_mod, daemon as daemon_mod, runs as runs_mod


def _escape(val: Any) -> str:
    return html.escape(str(val if val is not None else ""))


def page_shell(content: str, active_tab: str = "dashboard", daemon_status: dict | None = None) -> str:
    is_alive = daemon_status.get("alive", False) if daemon_status else False
    badge_cls = "badge-success" if is_alive else "badge-dim"
    dot_cls = "dot-green" if is_alive else "dot-red"
    status_str = "RUNNING" if is_alive else "STOPPED"
    daemon_badge = (
        '<div id="header-daemon-badge" hx-get="/api/daemon/badge" hx-trigger="every 5s" hx-swap="outerHTML">'
        f'<span class="badge {badge_cls}">'
        f'<span class="dot {dot_cls}"></span>'
        f'daemon: {status_str}'
        '</span>'
        '</div>'
    )

    t_dash = 'active' if active_tab == 'dashboard' else ''
    t_wf = 'active' if active_tab == 'workflows' else ''
    t_sched = 'active' if active_tab == 'schedules' else ''
    t_runs = 'active' if active_tab == 'runs' else ''
    t_daemon = 'active' if active_tab == 'daemon' else ''

    script_block = """
  <script>
    document.body.addEventListener('htmx:afterOnLoad', function(evt) {
      if (evt.detail.target.id === 'main-view') {
        const path = window.location.pathname;
        document.querySelectorAll('nav .nav-btn').forEach(function(el) {
          el.classList.toggle('active', el.getAttribute('href') === path);
        });
      }
    });
    function closeModal() {
      const container = document.getElementById('modal-container');
      if (container) container.innerHTML = '';
    }
  </script>
"""

    return f"""<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>px0 dashboard</title>
  <link rel="stylesheet" href="/static/style.css">
  <script src="/static/htmx.min.js"></script>
</head>
<body>
  <header>
    <div class="logo-area">
      <div class="brand">px0<span>web</span></div>
      <nav>
        <a href="/" class="nav-btn {t_dash}" hx-get="/api/views/dashboard" hx-target="#main-view" hx-push-url="/">Dashboard</a>
        <a href="/workflows" class="nav-btn {t_wf}" hx-get="/api/views/workflows" hx-target="#main-view" hx-push-url="/workflows">Workflows</a>
        <a href="/schedules" class="nav-btn {t_sched}" hx-get="/api/views/schedules" hx-target="#main-view" hx-push-url="/schedules">Schedules</a>
        <a href="/runs" class="nav-btn {t_runs}" hx-get="/api/views/runs" hx-target="#main-view" hx-push-url="/runs">Runs</a>
        <a href="/daemon" class="nav-btn {t_daemon}" hx-get="/api/views/daemon" hx-target="#main-view" hx-push-url="/daemon">Daemon</a>
      </nav>
    </div>
    <div class="header-status">
      <div id="global-spinner" class="htmx-indicator spinner"></div>
      {daemon_badge}
    </div>
  </header>

  <main id="main-view">
    {content}
  </main>

  <div id="modal-container"></div>
  {script_block}
</body>
</html>"""


def render_daemon_badge(daemon_status: dict) -> str:
    is_alive = daemon_status.get("alive", False)
    badge_cls = "badge-success" if is_alive else "badge-dim"
    dot_cls = "dot-green" if is_alive else "dot-red"
    status_str = "RUNNING" if is_alive else "STOPPED"
    return (
        '<div id="header-daemon-badge" hx-get="/api/daemon/badge" hx-trigger="every 5s" hx-swap="outerHTML">'
        f'<span class="badge {badge_cls}">'
        f'<span class="dot {dot_cls}"></span>'
        f'daemon: {status_str}'
        '</span>'
        '</div>'
    )


def render_dashboard(home, config) -> str:
    all_wfs = workflow_mod.load_all(home)
    total_wfs = len(all_wfs)
    enabled_wfs = sum(1 for w in all_wfs.values() if w.enabled)
    scheduled_wfs = sum(1 for w in all_wfs.values() if (w.trigger or {}).get("schedule") and w.enabled)
    
    d_status = daemon_mod.status(home, config)
    recent_runs = runs_mod.list_records(config)[:5]

    runs_html = ""
    if recent_runs:
        rows = []
        for r in recent_runs:
            outcome = r.get("outcome", "unknown")
            badge_class = "badge-success" if outcome == "success" else ("badge-danger" if outcome == "failed" else "badge-dim")
            run_id = _escape(r.get('id', ''))
            wf_id = _escape(r.get('workflow_id', ''))
            st = _escape(r.get('start_time', '')[:19].replace('T', ' '))
            rows.append(
                f'<tr>'
                f'<td class="code-id">{run_id}</td>'
                f'<td><span class="code-font">{wf_id}</span></td>'
                f'<td><span class="badge {badge_class}">{_escape(outcome)}</span></td>'
                f'<td class="code-font">{st}</td>'
                f'<td><button class="btn btn-secondary btn-sm" hx-get="/api/runs/{run_id}" hx-target="#modal-container">Details</button></td>'
                f'</tr>'
            )
        runs_html = (
            '<div class="table-wrapper">'
            '<table>'
            '<thead><tr><th>Run ID</th><th>Workflow</th><th>Outcome</th><th>Started</th><th>Actions</th></tr></thead>'
            f'<tbody>{" ".join(rows)}</tbody>'
            '</table>'
            '</div>'
        )
    else:
        runs_html = '<div class="empty-state"><p>No historical runs found.</p></div>'

    daemon_state_text = f"Running (PID: {d_status.get('pid')})" if d_status.get('alive') else "Stopped"
    daemon_badge_class = "badge-success" if d_status.get('alive') else "badge-danger"

    return f"""
    <div class="metrics-grid">
      <div class="metric-card">
        <div class="metric-label">Total Workflows</div>
        <div class="metric-val">{total_wfs}</div>
      </div>
      <div class="metric-card">
        <div class="metric-label">Enabled Workflows</div>
        <div class="metric-val">{enabled_wfs}</div>
      </div>
      <div class="metric-card">
        <div class="metric-label">Active Schedules</div>
        <div class="metric-val">{scheduled_wfs}</div>
      </div>
      <div class="metric-card">
        <div class="metric-label">Daemon Status</div>
        <div style="margin-top: 6px;">
          <span class="badge {daemon_badge_class}">{_escape(daemon_state_text)}</span>
        </div>
      </div>
    </div>

    <div class="panel">
      <div class="panel-header">
        <div class="panel-title">Recent Historical Runs</div>
        <a href="/runs" class="btn btn-secondary btn-sm" hx-get="/api/views/runs" hx-target="#main-view" hx-push-url="/runs">View All Runs</a>
      </div>
      {runs_html}
    </div>
    """


def render_workflows_list(home, config) -> str:
    all_wfs = workflow_mod.load_all(home)
    errors = workflow_mod.load_errors(home)

    alert_html = ""
    if errors:
        error_items = "".join(f"<li>{_escape(err)}</li>" for err in errors)
        alert_html = f"""
        <div class="panel" style="border-color: rgba(224, 108, 117, 0.4); background: var(--danger-bg);">
          <div class="panel-title" style="color: var(--danger); margin-bottom: 8px;">Workflow Parse Errors</div>
          <ul style="padding-left: 20px;">{error_items}</ul>
        </div>
        """

    rows = []
    for wf_id, wf in sorted(all_wfs.items()):
        status_badge = '<span class="badge badge-success">ENABLED</span>' if wf.enabled else '<span class="badge badge-dim">DISABLED</span>'
        schedule = (wf.trigger or {}).get("schedule")
        sched_html = f'<code class="code-font">{_escape(schedule)}</code>' if schedule else '<span style="color: var(--text-faint);">manual only</span>'
        tools_summary = ", ".join(wf.tools[:3]) + (", ..." if len(wf.tools) > 3 else "") if wf.tools else "none"
        toggle_label = 'Disable' if wf.enabled else 'Enable'

        rows.append(f"""
        <tr id="wf-row-{_escape(wf_id)}">
          <td><span class="code-id">{_escape(wf_id)}</span></td>
          <td>{status_badge}</td>
          <td>{sched_html}</td>
          <td style="max-width: 250px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-dim);" title="{_escape(wf.description)}">
            {_escape(wf.description or wf.request or '-')}
          </td>
          <td class="code-font" style="color: var(--text-dim);">{_escape(tools_summary)}</td>
          <td>
            <div style="display: flex; gap: 6px;">
              <button class="btn btn-primary btn-sm" hx-get="/api/workflows/{_escape(wf_id)}/run-modal" hx-target="#modal-container">Run</button>
              <button class="btn btn-secondary btn-sm" hx-get="/api/workflows/{_escape(wf_id)}" hx-target="#modal-container">View</button>
              <button class="btn btn-secondary btn-sm" 
                      hx-post="/api/workflows/{_escape(wf_id)}/toggle" 
                      hx-target="#wf-row-{_escape(wf_id)}" 
                      hx-swap="outerHTML">
                {toggle_label}
              </button>
            </div>
          </td>
        </tr>
        """)

    table_content = ""
    if rows:
        table_content = f"""
        <div class="table-wrapper">
          <table>
            <thead>
              <tr>
                <th>Workflow ID</th>
                <th>Status</th>
                <th>Schedule</th>
                <th>Description</th>
                <th>Tools</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {''.join(rows)}
            </tbody>
          </table>
        </div>
        """
    else:
        table_content = '<div class="empty-state"><p>No workflows found in store.</p></div>'

    return f"""
    {alert_html}
    <div class="panel">
      <div class="panel-header">
        <div class="panel-title">Workflows ({len(all_wfs)})</div>
      </div>
      {table_content}
    </div>
    """


def render_workflow_row(wf) -> str:
    status_badge = '<span class="badge badge-success">ENABLED</span>' if wf.enabled else '<span class="badge badge-dim">DISABLED</span>'
    schedule = (wf.trigger or {}).get("schedule")
    sched_html = f'<code class="code-font">{_escape(schedule)}</code>' if schedule else '<span style="color: var(--text-faint);">manual only</span>'
    tools_summary = ", ".join(wf.tools[:3]) + (", ..." if len(wf.tools) > 3 else "") if wf.tools else "none"
    toggle_label = 'Disable' if wf.enabled else 'Enable'

    return f"""
    <tr id="wf-row-{_escape(wf.id)}">
      <td><span class="code-id">{_escape(wf.id)}</span></td>
      <td>{status_badge}</td>
      <td>{sched_html}</td>
      <td style="max-width: 250px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-dim);" title="{_escape(wf.description)}">
        {_escape(wf.description or wf.request or '-')}
      </td>
      <td class="code-font" style="color: var(--text-dim);">{_escape(tools_summary)}</td>
      <td>
        <div style="display: flex; gap: 6px;">
          <button class="btn btn-primary btn-sm" hx-get="/api/workflows/{_escape(wf.id)}/run-modal" hx-target="#modal-container">Run</button>
          <button class="btn btn-secondary btn-sm" hx-get="/api/workflows/{_escape(wf.id)}" hx-target="#modal-container">View</button>
          <button class="btn btn-secondary btn-sm" 
                  hx-post="/api/workflows/{_escape(wf.id)}/toggle" 
                  hx-target="#wf-row-{_escape(wf.id)}" 
                  hx-swap="outerHTML">
            {toggle_label}
          </button>
        </div>
      </td>
    </tr>
    """


def render_workflow_detail_modal(wf) -> str:
    tools_list = ", ".join(wf.tools) if wf.tools else "None"
    inputs_info = "None"
    if wf.inputs:
        inputs_info = "<ul style='padding-left: 20px;'>" + "".join(
            f"<li><code>{_escape(inp.id)}</code> ({_escape(inp.kind)})</li>" for inp in wf.inputs
        ) + "</ul>"
    
    vars_info = "None"
    if wf.vars:
        vars_info = "<ul style='padding-left: 20px;'>" + "".join(
            f"<li><code>{_escape(v.get('name'))}</code>: {_escape(v.get('description', ''))} (default: {_escape(v.get('default'))})</li>" 
            for v in wf.vars
        ) + "</ul>"

    trigger_info = json.dumps(wf.trigger, indent=2) if wf.trigger else "manual"

    return f"""
    <div class="modal-backdrop" onclick="if(event.target === this) closeModal();">
      <div class="modal-content">
        <div class="modal-header">
          <div class="panel-title code-id">{_escape(wf.id)}</div>
          <button class="btn btn-secondary btn-sm" onclick="closeModal()">Close</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <div class="form-label">Description</div>
            <div style="color: var(--text);">{_escape(wf.description or 'No description')}</div>
          </div>
          <div class="form-group">
            <div class="form-label">Path</div>
            <code class="code-font" style="color: var(--text-dim);">{_escape(str(wf.path))}</code>
          </div>
          <div class="form-group">
            <div class="form-label">Trigger</div>
            <pre class="code-view">{_escape(trigger_info)}</pre>
          </div>
          <div class="form-group">
            <div class="form-label">Tools</div>
            <div class="code-font">{_escape(tools_list)}</div>
          </div>
          <div class="form-group">
            <div class="form-label">Inputs</div>
            <div>{inputs_info}</div>
          </div>
          <div class="form-group">
            <div class="form-label">Template Vars</div>
            <div>{vars_info}</div>
          </div>
          <div class="form-group">
            <div class="form-label">Prompt Body</div>
            <pre class="code-view">{_escape(wf.body)}</pre>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-primary" hx-get="/api/workflows/{_escape(wf.id)}/run-modal" hx-target="#modal-container">Run Workflow</button>
          <button class="btn btn-secondary" onclick="closeModal()">Close</button>
        </div>
      </div>
    </div>
    """


def render_schedules_list(home, config) -> str:
    all_wfs = workflow_mod.load_all(home)
    scheduled = [(wf_id, wf) for wf_id, wf in sorted(all_wfs.items()) if (wf.trigger or {}).get("schedule")]

    state = daemon_mod.load_schedule_state(home)
    now = datetime.now()

    rows = []
    for wf_id, wf in scheduled:
        cron_expr = wf.trigger.get("schedule", "")
        zone = daemon_mod.resolve_zone(config, wf)
        last_fire = state.get(wf_id, "Never")
        
        next_fire = "Unknown"
        try:
            zone_now = datetime.now(zone) if zone else now
            itr = croniter(cron_expr, zone_now)
            next_dt = itr.get_next(datetime)
            next_fire = next_dt.strftime("%Y-%m-%d %H:%M:%S")
        except Exception:
            next_fire = "Invalid cron syntax"

        status_badge = '<span class="badge badge-success">ENABLED</span>' if wf.enabled else '<span class="badge badge-dim">DISABLED</span>'
        toggle_label = 'Disable' if wf.enabled else 'Enable'

        rows.append(f"""
        <tr id="sched-row-{_escape(wf_id)}">
          <td><span class="code-id">{_escape(wf_id)}</span></td>
          <td><code class="code-font" style="font-weight: bold; color: var(--accent);">{_escape(cron_expr)}</code></td>
          <td><span class="code-font" style="color: var(--text-dim);">{_escape(str(zone) if zone else 'Local')}</span></td>
          <td class="code-font">{_escape(last_fire[:19].replace('T', ' '))}</td>
          <td class="code-font" style="color: var(--info);">{_escape(next_fire)}</td>
          <td>{status_badge}</td>
          <td>
            <div style="display: flex; gap: 6px;">
              <button class="btn btn-primary btn-sm" hx-get="/api/schedules/{_escape(wf_id)}/edit" hx-target="#modal-container">Edit Schedule</button>
              <button class="btn btn-secondary btn-sm" 
                      hx-post="/api/workflows/{_escape(wf_id)}/toggle" 
                      hx-target="#main-view" 
                      hx-get="/api/views/schedules">
                {toggle_label}
              </button>
            </div>
          </td>
        </tr>
        """)

    table_content = ""
    if rows:
        table_content = f"""
        <div class="table-wrapper">
          <table>
            <thead>
              <tr>
                <th>Workflow</th>
                <th>Cron Schedule</th>
                <th>Timezone</th>
                <th>Last Fire</th>
                <th>Next Fire</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {''.join(rows)}
            </tbody>
          </table>
        </div>
        """
    else:
        table_content = '<div class="empty-state"><p>No scheduled workflows configured.</p></div>'

    return f"""
    <div class="panel">
      <div class="panel-header">
        <div class="panel-title">Workflow Schedules ({len(scheduled)})</div>
      </div>
      {table_content}
    </div>
    """


def render_schedule_edit_modal(wf) -> str:
    current_cron = (wf.trigger or {}).get("schedule", "")
    return f"""
    <div class="modal-backdrop" onclick="if(event.target === this) closeModal();">
      <div class="modal-content" style="max-width: 500px;">
        <div class="modal-header">
          <div class="panel-title">Edit Schedule: <span class="code-id">{_escape(wf.id)}</span></div>
          <button class="btn btn-secondary btn-sm" onclick="closeModal()">Close</button>
        </div>
        <form hx-post="/api/schedules/{_escape(wf.id)}/update" hx-target="#main-view">
          <div class="modal-body">
            <div class="form-group">
              <label class="form-label">Cron Expression</label>
              <input type="text" name="schedule" class="form-input code-font" value="{_escape(current_cron)}" required />
              <div class="form-help">e.g. <code>0 9 * * 1-5</code> or <code>@daily</code></div>
            </div>
          </div>
          <div class="modal-footer">
            <button type="submit" class="btn btn-primary">Save Changes</button>
            <button type="button" class="btn btn-secondary" onclick="closeModal()">Cancel</button>
          </div>
        </form>
      </div>
    </div>
    """


def render_runs_list(config) -> str:
    records = runs_mod.list_records(config)[:100]

    rows = []
    for r in records:
        outcome = r.get("outcome", "unknown")
        badge_class = "badge-success" if outcome == "success" else ("badge-danger" if outcome == "failed" else "badge-dim")
        trigger = r.get("trigger", "manual")
        duration = "-"
        if r.get("start_time") and r.get("end_time"):
            try:
                st = datetime.fromisoformat(r["start_time"])
                et = datetime.fromisoformat(r["end_time"])
                secs = (et - st).total_seconds()
                duration = f"{secs:.1f}s"
            except Exception:
                pass

        tool_calls_count = len(r.get("tool_calls", []))
        run_id = _escape(r.get('id', ''))
        wf_id = _escape(r.get('workflow_id', ''))
        st_str = _escape(r.get('start_time', '')[:19].replace('T', ' '))

        rows.append(f"""
        <tr>
          <td><span class="code-id">{run_id}</span></td>
          <td><span class="code-font">{wf_id}</span></td>
          <td><span class="badge {badge_class}">{_escape(outcome)}</span></td>
          <td><span class="badge badge-dim">{_escape(trigger)}</span></td>
          <td class="code-font">{_escape(duration)}</td>
          <td class="code-font">{tool_calls_count}</td>
          <td class="code-font">{st_str}</td>
          <td>
            <button class="btn btn-secondary btn-sm" hx-get="/api/runs/{run_id}" hx-target="#modal-container">Details</button>
          </td>
        </tr>
        """)

    table_content = ""
    if rows:
        table_content = f"""
        <div class="table-wrapper">
          <table>
            <thead>
              <tr>
                <th>Run ID</th>
                <th>Workflow</th>
                <th>Outcome</th>
                <th>Trigger</th>
                <th>Duration</th>
                <th>Tool Calls</th>
                <th>Started</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {''.join(rows)}
            </tbody>
          </table>
        </div>
        """
    else:
        table_content = '<div class="empty-state"><p>No runs recorded yet.</p></div>'

    return f"""
    <div class="panel">
      <div class="panel-header">
        <div class="panel-title">Historical Runs ({len(records)})</div>
        <button class="btn btn-secondary btn-sm" hx-get="/api/views/runs" hx-target="#main-view">Refresh</button>
      </div>
      {table_content}
    </div>
    """


def render_run_detail_modal(config, run_id: str) -> str:
    try:
        record = runs_mod.read_record(config, run_id)
    except Exception as e:
        return f"""
        <div class="modal-backdrop" onclick="if(event.target === this) closeModal();">
          <div class="modal-content">
            <div class="modal-header">
              <div class="panel-title">Run Error</div>
              <button class="btn btn-secondary btn-sm" onclick="closeModal()">Close</button>
            </div>
            <div class="modal-body"><p style="color: var(--danger);">{_escape(str(e))}</p></div>
          </div>
        </div>
        """

    raw_log = runs_mod.read_raw_log(config, run_id)
    events = runs_mod.read_events(config, run_id)
    outcome = record.get("outcome", "unknown")
    badge_class = "badge-success" if outcome == "success" else ("badge-danger" if outcome == "failed" else "badge-dim")

    tool_calls = record.get("tool_calls", [])
    tools_html = "None"
    if tool_calls:
        tc_rows = []
        for tc in tool_calls:
            dur = tc.get('duration_seconds', 0)
            tc_rows.append(f"""
            <li style="margin-bottom: 6px;">
              <strong>{_escape(tc.get('tool'))}</strong>
              <div class="code-font" style="color: var(--text-dim); margin-top: 2px;">
                duration: {dur:.2f}s | write: {tc.get('is_write', False)}
              </div>
            </li>
            """)
        tools_html = f"<ul style='padding-left: 20px;'>{''.join(tc_rows)}</ul>"

    output_spec = record.get("output", {})
    output_text = output_spec.get("text", "")

    return f"""
    <div class="modal-backdrop" onclick="if(event.target === this) closeModal();">
      <div class="modal-content">
        <div class="modal-header">
          <div class="panel-title">Run: <span class="code-id">{_escape(run_id)}</span></div>
          <button class="btn btn-secondary btn-sm" onclick="closeModal()">Close</button>
        </div>
        <div class="modal-body">
          <div style="display: flex; gap: 12px; margin-bottom: 16px; align-items: center;">
            <span class="badge {badge_class}">{_escape(outcome)}</span>
            <span class="badge badge-dim">trigger: {_escape(record.get('trigger', 'manual'))}</span>
            <span class="code-font" style="color: var(--text-dim);">Workflow: {_escape(record.get('workflow_id', ''))}</span>
          </div>

          <div class="form-group">
            <div class="form-label">Output ({_escape(output_spec.get('target', 'stdout'))})</div>
            <pre class="code-view">{_escape(output_text or 'No output recorded')}</pre>
          </div>

          <div class="form-group">
            <div class="form-label">Tool Calls ({len(tool_calls)})</div>
            <div>{tools_html}</div>
          </div>

          <div class="form-group">
            <div class="form-label">Raw Log</div>
            <pre class="code-view">{_escape(raw_log or 'No raw log available')}</pre>
          </div>

          <div class="form-group">
            <div class="form-label">Events JSON ({len(events)})</div>
            <pre class="code-view">{_escape(json.dumps(events, indent=2) if events else 'No event trace')}</pre>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-secondary" onclick="closeModal()">Close</button>
        </div>
      </div>
    </div>
    """


def render_run_modal(wf) -> str:
    declared_vars = workflow_mod.declared_vars(wf)
    vars_inputs_html = ""
    if declared_vars:
        fields = []
        for v in declared_vars:
            name = v["name"]
            req = v.get("required", False)
            default = v.get("default") or ""
            desc = v.get("description", "")
            req_label = "<span style='color: var(--danger);'>*</span>" if req else ""
            req_attr = 'required' if req else ''
            fields.append(f"""
            <div class="form-group">
              <label class="form-label">{_escape(name)} {req_label}</label>
              <input type="text" name="var_{_escape(name)}" value="{_escape(default)}" class="form-input" {req_attr} />
              <div class="form-help">{_escape(desc)}</div>
            </div>
            """)
        vars_inputs_html = f"""
        <div style="margin-top: 12px;">
          <div class="panel-title" style="font-size: 14px; margin-bottom: 8px;">Variables</div>
          {''.join(fields)}
        </div>
        """

    return f"""
    <div class="modal-backdrop" onclick="if(event.target === this) closeModal();">
      <div class="modal-content" style="max-width: 550px;">
        <div class="modal-header">
          <div class="panel-title">Run Workflow: <span class="code-id">{_escape(wf.id)}</span></div>
          <button class="btn btn-secondary btn-sm" onclick="closeModal()">Close</button>
        </div>
        <form hx-post="/api/workflows/{_escape(wf.id)}/trigger" hx-target="#run-status-result">
          <div class="modal-body">
            <p style="color: var(--text-dim); margin-bottom: 12px;">{_escape(wf.description or 'Execute this workflow immediately.')}</p>
            {vars_inputs_html}

            <div class="form-group" style="margin-top: 16px;">
              <label style="display: flex; align-items: center; gap: 8px; cursor: pointer;">
                <input type="checkbox" name="dry_run" value="true" />
                <span>Dry run (rehearsal - skips write tool calls)</span>
              </label>
            </div>

            <div id="run-status-result" style="margin-top: 16px;"></div>
          </div>
          <div class="modal-footer">
            <button type="submit" class="btn btn-primary">
              <span class="htmx-indicator spinner" style="margin-right: 6px;"></span>
              Start Run
            </button>
            <button type="button" class="btn btn-secondary" onclick="closeModal()">Cancel</button>
          </div>
        </form>
      </div>
    </div>
    """


def render_daemon_view(home, config) -> str:
    status = daemon_mod.status(home, config)
    alive = status.get("alive", False)
    pid = status.get("pid")

    log_path = runs_mod.resolve_logs_path(config) / "daemon.log"
    daemon_log_tail = ""
    if log_path.exists():
        try:
            lines = log_path.read_text().splitlines()
            daemon_log_tail = "\n".join(lines[-40:])
        except Exception:
            daemon_log_tail = "Could not read daemon log."
    else:
        daemon_log_tail = "No daemon.log found yet."

    status_badge = f'<span class="badge badge-success"><span class="dot dot-green"></span> RUNNING (PID {pid})</span>' if alive else '<span class="badge badge-danger"><span class="dot dot-red"></span> STOPPED</span>'
    action_btn = '<button class="btn btn-danger btn-sm" hx-post="/api/daemon/action?act=stop" hx-target="#main-view">Stop Daemon</button>' if alive else '<button class="btn btn-primary btn-sm" hx-post="/api/daemon/action?act=start" hx-target="#main-view">Start Daemon</button>'

    return f"""
    <div id="daemon-panel" class="panel">
      <div class="panel-header">
        <div class="panel-title">Daemon Control & Observability</div>
        <div style="display: flex; gap: 8px;">
          <button class="btn btn-secondary btn-sm" 
                  hx-post="/api/daemon/action?act=tick" 
                  hx-target="#daemon-action-result">
            Tick Now
          </button>
          {action_btn}
        </div>
      </div>

      <div style="display: flex; gap: 20px; align-items: center; margin-bottom: 20px;">
        <div>
          <span style="color: var(--text-dim); margin-right: 8px;">Current Status:</span>
          {status_badge}
        </div>
      </div>

      <div id="daemon-action-result"></div>

      <div class="form-group" style="margin-top: 16px;">
        <div class="panel-title" style="font-size: 14px; margin-bottom: 8px;">Daemon Logs (last 40 lines)</div>
        <pre class="code-view">{_escape(daemon_log_tail)}</pre>
      </div>
    </div>
    """
