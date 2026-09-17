// web/src/agent.js
import { $, esc, S, api, apiPost } from './state.js';
import { showToast } from './ui.js';
import { setStatusNote } from './status.js';
import { openFile, reloadOpenTabs } from './tabs.js';
import { drawTree, treeEl } from './tree.js';
import { setAgentHandler, hideSelectionBar } from './selbar.js';
import { render } from './renderer.js';
import { syncDiffAgentTargets } from './diff.js';

/* px0 does not author edits. Each box composes an instruction and the range it
   is anchored to, hands both to a coding harness on this machine, and reloads
   whatever moved once that harness exits. Because px0 dispatched the run it
   knows when the work ended, so nothing here watches the filesystem.

   Several edits can run at once, one box per range: two harnesses rewriting
   the same lines would produce a result nobody could review, so a range that
   overlaps one already open is refused before it ever reaches the server
   (which enforces the same rule for a race between two tabs).

   Harnesses are detected, not configured: the picker lists what is installed
   and the choice is remembered in the settings file. Detecting one is never
   enough to run it, so the first edit in a fresh install asks which to use. */

const box = $('#agentbox');
const tpl = $('#agentbox-tpl');

const sessions = new Map(); // local session id -> in-progress compose/edit
let agentSeq = 0;

const installed = () => (S.meta?.agents || []).filter(h => h.installed);
const chosen = () => (S.meta && S.meta.agent) || '';
const chosenModel = () => (S.meta && S.meta.agentModel) || '';
const targetRef = ({ path, l1, l2 }) => path + ':' + (l1 === l2 ? l1 : l1 + '-' + l2);
const rangesOverlap = (a, b) => a.path === b.path && a.l1 <= b.l2 && b.l1 <= a.l2;

export function applyAgentMeta() {
  for (const session of sessions.values()) {
    updateSessionMeta(session);
  }
}

function updateSessionMeta(session) {
  if (!session.harnessSelect || !session.modelSelect) return;
  const ready = (S.meta?.agents || []).filter(h => h.installed);
  const currentHarness = chosen();
  const currentModel = chosenModel();

  // Populate harness select
  session.harnessSelect.innerHTML = '';
  if (!ready.length) {
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = 'no harness';
    session.harnessSelect.appendChild(opt);
    session.harnessSelect.disabled = true;
    session.modelSelect.innerHTML = '';
    session.modelSelect.hidden = true;
    return;
  }

  for (const h of ready) {
    const opt = document.createElement('option');
    opt.value = h.name;
    opt.textContent = h.name;
    if (h.name === currentHarness) opt.selected = true;
    session.harnessSelect.appendChild(opt);
  }
  const isBusy = session.el.classList.contains('busy');
  session.harnessSelect.disabled = isBusy || !!(S.meta && S.meta.agentPinned);
  session.harnessSelect.title = S.meta && S.meta.agentPinned
    ? 'Fixed for this run by -agent'
    : 'Change the coding harness';

  // Populate model select for the currently selected harness
  const activeH = ready.find(h => h.name === (session.harnessSelect.value || currentHarness)) || ready[0];
  session.modelSelect.innerHTML = '';
  const models = activeH?.models || [];
  if (models.length > 0) {
    for (const m of models) {
      const opt = document.createElement('option');
      opt.value = m;
      opt.textContent = m;
      if (m === currentModel) opt.selected = true;
      session.modelSelect.appendChild(opt);
    }
    session.modelSelect.hidden = false;
    session.modelSelect.disabled = isBusy;
    session.modelSelect.title = 'Model for ' + activeH.name;
  } else {
    session.modelSelect.hidden = true;
  }
}

// Loads harnesses and models asynchronously after the browser UI has loaded.
export async function loadAgentAsync() {
  try {
    const j = await api('/api/agent/harnesses');
    S.meta.agents = j.harnesses || [];
    S.meta.agent = j.selected || S.meta.agent || '';
    S.meta.agentModel = j.model || S.meta.agentModel || '';
    S.meta.agentPinned = !!j.pinned;
    applyAgentMeta();
  } catch {}
}

function anyInFlight() {
  for (const s of sessions.values()) if (s.timer) return true;
  return false;
}

function syncBoxVisibility() {
  box.hidden = sessions.size === 0;
}

export function openAgentEdit(info) {
  if (!info) return;
  for (const s of sessions.values()) {
    if (rangesOverlap(s.target, info)) {
      showToast('!', 'Overlaps the edit already open on ' + targetRef(s.target));
      return;
    }
  }
  const session = createSession(info);
  sessions.set(session.id, session);
  syncAgentTargets();
  applyAgentMeta();
  syncBoxVisibility();
  if (chosen() && installed().some(h => h.name === chosen())) {
    showCompose(session);
  } else {
    showPicker(session);
  }
}

function syncAgentTargets() {
  S.agentTargets = [...sessions.values()].map(s => ({
    id: s.id,
    path: s.target.path,
    l1: s.target.l1,
    l2: s.target.l2,
  }));
  render();
  syncDiffAgentTargets();
}

function createSession(info) {
  const el = tpl.content.firstElementChild.cloneNode(true);
  // Newest first in markup: #agentbox is column-reverse, so it lands closest
  // to the corner the stack grows from, where a triggered action was aimed.
  box.prepend(el);
  const session = {
    id: ++agentSeq,
    target: info,
    timer: null,
    jobId: null,
    harness: '',
    el,
    refEl: el.querySelector('.agent-ref'),
    metaEl: el.querySelector('.agent-meta'),
    harnessSelect: el.querySelector('.agent-harness-select'),
    modelSelect: el.querySelector('.agent-model-select'),
    closeBtn: el.querySelector('.agent-close'),
    pickEl: el.querySelector('.agent-pick'),
    composeEl: el.querySelector('.agent-compose'),
    input: el.querySelector('.agent-input'),
    sendBtn: el.querySelector('.agent-send'),
    cancelBtn: el.querySelector('.agent-cancel'),
    hintEl: el.querySelector('.agent-hint'),
    errEl: el.querySelector('.agent-err'),
  };
  wireSession(session);
  refreshRef(session);
  setBusy(session, false);
  resetHint(session);
  clearErr(session);
  session.input.value = '';
  session.input.focus();
  return session;
}

function wireSession(session) {
  session.sendBtn.addEventListener('click', () => submit(session));
  session.cancelBtn?.addEventListener('click', () => cancelSession(session));
  session.closeBtn.addEventListener('click', () => closeAgentEdit(session));
  if (session.harnessSelect) {
    session.harnessSelect.addEventListener('change', async () => {
      const hName = session.harnessSelect.value;
      if (!hName) return;
      await select(hName, msg => showErr(session, msg));
      session.input.focus();
    });
  }
  if (session.modelSelect) {
    session.modelSelect.addEventListener('change', async () => {
      const hName = session.harnessSelect?.value || chosen();
      const mName = session.modelSelect.value;
      await select(hName, mName, msg => showErr(session, msg));
      session.input.focus();
    });
  }
  /* The composer swallows every key while it is open. Nothing typed into an
     instruction should also fire a viewport shortcut. */
  session.el.addEventListener('keydown', e => {
    e.stopPropagation();
    if (e.key === 'Escape') {
      e.preventDefault();
      if (session.timer || session.jobId) {
        cancelSession(session);
      } else {
        closeAgentEdit(session);
      }
    } else if (e.key === 'Enter' && !e.shiftKey && !session.composeEl.hidden && !session.timer) {
      e.preventDefault();
      submit(session);
    }
  });
}

async function cancelSession(session) {
  if (!session.timer && !session.jobId) return;
  if (session.timer) {
    clearTimeout(session.timer);
    session.timer = null;
  }
  const jobId = session.jobId;
  session.jobId = null;
  setBusy(session, false);
  resetHint(session);
  refreshStatusNote();
  showToast('!', 'Cancelled edit on ' + targetRef(session.target));
  if (jobId) {
    try {
      await apiPost('/api/agent/cancel', { id: jobId });
    } catch {}
  }
  session.input.focus();
}

function closeAgentEdit(session) {
  if (session.timer || session.jobId) {
    cancelSession(session);
  }
  sessions.delete(session.id);
  session.el.remove();
  syncBoxVisibility();
  syncAgentTargets();
}

function refreshRef(session) {
  const ref = targetRef(session.target);
  session.refEl.textContent = ref;
  session.refEl.title = ref;
}

function clearErr(session) {
  const errEl = session.errEl;
  if (!errEl) return;
  errEl.textContent = '';
  errEl.hidden = true;
}

/* Renders a failure inline under the instruction. A harness run also carries
   what it printed, which is usually the only clue to why it exited non-zero. */
function showErr(session, msg, streams = []) {
  const errEl = session.errEl;
  if (!errEl) return;
  errEl.textContent = '';
  const head = document.createElement('div');
  head.className = 'agent-err-msg';
  head.textContent = msg;
  errEl.appendChild(head);
  for (const [label, text] of streams) {
    if (!text) continue;
    const name = document.createElement('div');
    name.className = 'agent-err-label';
    name.textContent = label;
    const pre = document.createElement('pre');
    pre.className = 'agent-err-out';
    pre.textContent = text;
    errEl.append(name, pre);
  }
  errEl.hidden = false;
}

function resetHint(session) {
  if (!session.hintEl) return;
  session.hintEl.textContent = 'Enter to send, Esc to cancel';
}

function setBusy(session, busy, msg) {
  session.el.classList.toggle('busy', busy);
  session.input.disabled = busy;
  if (session.sendBtn) session.sendBtn.hidden = busy;
  if (session.cancelBtn) session.cancelBtn.hidden = !busy;
  session.closeBtn.disabled = false;
  if (session.harnessSelect) session.harnessSelect.disabled = busy || !!(S.meta && S.meta.agentPinned);
  if (session.modelSelect) session.modelSelect.disabled = busy;
  if (session.hintEl && msg) session.hintEl.textContent = msg;
}

function showCompose(session) {
  session.pickEl.hidden = true;
  if (session.metaEl) session.metaEl.hidden = false;
  session.composeEl.hidden = false;
  session.input.focus();
}

async function showPicker(session) {
  session.composeEl.hidden = true;
  if (session.metaEl) session.metaEl.hidden = true;
  session.pickEl.hidden = false;
  session.pickEl.innerHTML = '<div class="hint">Looking for coding harnesses…</div>';

  let list = S.meta?.agents || [];
  let settingsPath = '';
  // Re-scan, so a harness installed since startup shows up without a restart.
  try {
    const j = await api('/api/agent/harnesses');
    list = j.harnesses || [];
    settingsPath = j.settings || '';
    S.meta.agents = list;
    S.meta.agent = j.selected || '';
    S.meta.agentModel = j.model || '';
    S.meta.agentPinned = !!j.pinned;
  } catch (e) {
    session.pickEl.innerHTML = '<div class="hint">Could not look for harnesses: ' + esc(e.message) + '</div>';
    return;
  }

  const ready = list.filter(h => h.installed);
  if (!ready.length) {
    showToast('!', 'Could not find any coding harness like Claude Code, OpenCode, Codex, Antigravity, Aider, etc. Install one and restart px0.', 6000);
    session.pickEl.innerHTML = '<div class="hint" style="line-height: 1.5; padding: 4px 2px;">' +
      'Could not find any coding harness like <b>Claude Code</b>, <b>OpenCode</b>, <b>Codex</b>, <b>Antigravity</b> (<code>agy</code>), <b>Aider</b>, <b>Goose</b>, <b>Gemini CLI</b>, or <b>Cursor Agent</b>.<br><br>' +
      'Please install a coding harness, make sure it is on your <code>PATH</code>, and restart px0 after that.</div>';
    return;
  }

  session.pickEl.innerHTML = '<div class="hint">This harness will edit files in this workspace.</div>' +
    optionsHtml(ready, settingsPath);

  session.pickEl.querySelectorAll('[data-pick]').forEach(b => {
    b.addEventListener('click', () => pick(session, b.dataset.pick));
  });
  session.pickEl.querySelectorAll('.agent-model-select').forEach(sel => {
    sel.addEventListener('change', async (e) => {
      e.stopPropagation();
      await select(sel.dataset.harness, sel.value, msg => showErr(session, msg));
      showPicker(session);
    });
  });
}

function optionsHtml(ready, settingsPath) {
  let html = '';
  for (const h of ready) {
    const isSelected = h.name === chosen();
    html += '<div class="agent-opt-wrap">' +
      '<button class="agent-opt' + (isSelected ? ' on' : '') + '" data-pick="' + esc(h.name) + '">' +
      '<span class="agent-opt-name">' + esc(h.name) + '</span>' +
      '<code class="agent-opt-cmd">' + esc(h.cmd) + '</code></button>';
    if (isSelected && h.models && h.models.length > 0) {
      html += '<div class="agent-model-row">' +
        '<span class="agent-model-label">Model:</span>' +
        '<select class="agent-model-select" data-harness="' + esc(h.name) + '">';
      for (const m of h.models) {
        const sel = m === (h.model || chosenModel()) ? ' selected' : '';
        html += '<option value="' + esc(m) + '"' + sel + '>' + esc(m) + '</option>';
      }
      html += '</select></div>';
    }
    html += '</div>';
  }
  if (settingsPath) html += '<div class="agent-note">Remembered in ' + esc(settingsPath) + '</div>';
  return html;
}

async function pick(session, name) {
  if (await select(name, msg => showErr(session, msg))) showCompose(session);
}

// Makes name the harness for every later edit. Returns whether it took.
async function select(name, model, onError) {
  if (typeof model === 'function') {
    onError = model;
    model = '';
  }
  try {
    const params = { name };
    if (model) params.model = model;
    const j = await apiPost('/api/agent/select', params);
    S.meta.agent = j.selected || '';
    S.meta.agentModel = j.model || '';
    S.meta.agents = j.harnesses || S.meta.agents;
    S.meta.agentPinned = !!j.pinned;
  } catch (e) {
    if (onError) onError(e.message);
    return false;
  }
  applyAgentMeta();
  return true;
}

async function submit(session) {
  if (session.timer) return;
  clearErr(session);
  const instruction = session.input.value.trim();
  if (!instruction || !session.target) return;
  const params = { path: session.target.path, l1: session.target.l1, l2: session.target.l2, instruction };

  let job;
  try {
    job = await apiPost('/api/agent/edit', params);
  } catch (e) {
    showErr(session, e.message);
    return;
  }

  session.jobId = job.id;
  session.harness = job.harness;
  hideSelectionBar();
  const initialNote = 'Editing with ' + (chosenModel() ? chosen() + ' (' + chosenModel() + ')' : chosen()) + '...';
  setBusy(session, true, initialNote);
  refreshStatusNote();
  session.timer = setTimeout(() => tick(session), 400);
}

async function tick(session) {
  if (!session.jobId) return;
  let j;
  try {
    j = await api('/api/agent/job?id=' + session.jobId);
  } catch (e) {
    if (!session.jobId) return;
    session.timer = null;
    /* A finished job that failed comes back with an error field, which the
       request helper turns into a throw. It still holds the harness output. */
    if (e.body && 'running' in e.body) {
      await finish(session, e.body);
      refreshStatusNote();
      return;
    }
    setBusy(session, false);
    resetHint(session);
    refreshStatusNote();
    showErr(session, e.message);
    return;
  }

  if (!session.jobId) return;
  if (j.running) {
    session.harness = j.harness;
    session.elapsed = Math.round((j.ms || 0) / 1000) + 's';
    setBusy(session, true, 'Editing with ' + j.harness + '... ' + session.elapsed);
    refreshStatusNote();
    session.timer = setTimeout(() => tick(session), 600);
    return;
  }

  session.timer = null;
  await finish(session, j);
  refreshStatusNote();
}

/* The status bar has one shared note. With one edit running it names the
   harness and how long it has been going; with several, a count is all that
   fits without the bar fighting itself over whose turn it is to speak. */
function refreshStatusNote() {
  const busy = [...sessions.values()].filter(s => s.timer);
  if (!busy.length) {
    setStatusNote('');
  } else if (busy.length === 1) {
    const s = busy[0];
    setStatusNote('Editing with ' + (s.harness || chosen()) + '... ' + (s.elapsed || ''));
  } else {
    setStatusNote(busy.length + ' edits running...');
  }
}

async function finish(session, j) {
  const editTarget = session.target;
  if (j.error) {
    setBusy(session, false);
    resetHint(session);
    showErr(session, (j.harness || 'agent') + ': ' + j.error, [
      ['stderr', (j.stderr || '').trim()],
      ['stdout', (j.stdout || j.log || '').trim()],
    ]);
    if (j.changed?.length) reloadWorkspace(null);
    return; // leave the box open so the error stays visible
  }

  sessions.delete(session.id);
  session.el.remove();
  syncBoxVisibility();
  syncAgentTargets();

  /* Without git px0 cannot tell what the harness touched, so an empty list
     means "unknown" rather than "nothing" and everything is reloaded. */
  const changed = j.changed || [];
  if (!changed.length && j.tracked !== false) {
    showToast('✓', 'Finished with no file changes');
    return;
  }

  if (!await reloadWorkspace(editTarget, 'Edited')) return;
  showToast('✓', !changed.length ? 'Reloaded the workspace'
    : changed.length === 1 ? 'Updated ' + changed[0]
      : 'Updated ' + changed.length + ' files');
}

/* Reload in the order the data depends on: the index first, so the tree and
   git badges agree with disk, then the open tabs, which keep their scroll,
   cursor and view across the swap. Two edits can finish close together, so
   reloads are queued rather than left to interleave. Returns whether it worked. */
let reloadChain = Promise.resolve();
function reloadWorkspace(focus, what = 'Changed') {
  const run = async () => {
    try {
      await api('/api/reindex');
      await reloadOpenTabs();
      if (focus?.path) {
        await openFile(focus.path, { line: focus.l1, push: false });
      }
      await drawTree('', treeEl, 0);
    } catch (e) {
      showToast('!', what + ', but the reload failed: ' + e.message);
      return false;
    }
    return true;
  };
  const result = reloadChain.then(run, run);
  reloadChain = result.then(() => {}, () => {});
  return result;
}

export function initAgent() {
  if (!box || !tpl) return;
  setAgentHandler(openAgentEdit);

  /* At least one harness is still writing to disk: leaving would abandon it
     with no way back to see how it went, so the tab asks first. */
  addEventListener('beforeunload', e => {
    if (!anyInFlight()) return;
    e.preventDefault();
    e.returnValue = '';
  });
}
