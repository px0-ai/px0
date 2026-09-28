// web/src/agent.js
import { $, esc, S, doc_, api, apiPost, apiPostJson, MOD, keyLabel } from './state.js';
import { showToast } from './ui.js';
import { setStatusNote } from './status.js';
import { openFile, reloadOpenTabs } from './tabs.js';
import { refreshTree, treeEl } from './tree.js';
import { setAgentHandler, hideSelectionBar } from './selbar.js';
import { render } from './renderer.js';
import { syncDiffAgentTargets } from './diff.js';
import { emit } from './bus.js';

/* px0 does not author edits. Each box composes an instruction and the range it
   is anchored to, hands both to a coding harness on this machine, and reloads
   whatever moved once that harness exits. Because px0 dispatched the run it
   knows when the work ended, so nothing here watches the filesystem.

   Several edits can run at once, one box per range: two harnesses rewriting
   the same lines would produce a result nobody could review, so a range that
   overlaps one already open is refused before it ever reaches the server
   (which enforces the same rule for a race between two tabs).

   Each box has two actions: Add comment (Enter) keeps the comment and folds the
   box away so more code can be commented on, and Apply now (Mod+Enter) runs just
   that one. The batch bar applies every added comment together (Apply all,
   Mod+Shift+Enter) as a single coordinated edit.

   Harnesses are detected, not configured: the picker lists what is installed
   and the choice is remembered in the settings file. Detecting one is never
   enough to run it, so the first edit in a fresh install asks which to use. */

const box = $('#agentbox');
const tpl = $('#agentbox-tpl');
const agentListEl = $('#agentbox-list');
const batchBar = $('#agent-batch-bar');
const batchCount = $('#agent-batch-count');
const batchClear = $('#agent-batch-clear');
const batchHarness = $('#agent-batch-harness');
const batchModel = $('#agent-batch-model');
const batchHint = $('#agent-batch-hint');
const batchApply = $('#agent-batch-apply');
const batchCancel = $('#agent-batch-cancel');
const batchOpen = $('#agent-batch-open');
const batchErr = $('#agent-batch-err');
const gitHarness = $('#git-harness');
const gitModel = $('#git-model');

const sessions = new Map(); // local session id -> in-progress compose/edit
let agentSeq = 0;

let batchTimer = null;
let batchJobId = null;
let batchThreadId = '';
let batchElapsed = '';
let activeBatchTargets = null;

const installed = () => (S.meta?.agents || []).filter(h => h.installed);
const chosen = () => (S.meta && S.meta.agent) || '';
const chosenModel = () => (S.meta && S.meta.agentModel) || '';
const targetRef = ({ path, l1, l2 }) => path + ':' + (l1 === l2 ? l1 : l1 + '-' + l2);
const rangesOverlap = (a, b) => a.path === b.path && a.l1 <= b.l2 && b.l1 <= a.l2;

// Harness/model selects owned by other panels (the thread composer). They
// show the same global selection as every composer box and write it back.
const extraPickers = new Set();
export function registerAgentPicker(picker) {
  extraPickers.add(picker);
  picker.harnessSelect.addEventListener('change', () => {
    if (picker.harnessSelect.value) select(picker.harnessSelect.value, msg => showToast('!', msg));
  });
  picker.modelSelect.addEventListener('change', () => {
    select(picker.harnessSelect.value || chosen(), picker.modelSelect.value, msg => showToast('!', msg));
  });
  updateSessionMeta(picker);
}

export function applyAgentMeta() {
  for (const session of sessions.values()) {
    updateSessionMeta(session);
  }
  for (const picker of extraPickers) updateSessionMeta(picker);
  syncBatchMeta();
  syncGitPanelMeta();
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

function syncBatchMeta() {
  if (!batchHarness || !batchModel) return;
  const ready = installed();
  const currentHarness = chosen();
  const currentModel = chosenModel();

  batchHarness.innerHTML = '';
  if (!ready.length) {
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = 'no harness';
    batchHarness.appendChild(opt);
    batchHarness.disabled = true;
    batchModel.innerHTML = '';
    batchModel.hidden = true;
    return;
  }

  for (const h of ready) {
    const opt = document.createElement('option');
    opt.value = h.name;
    opt.textContent = h.name;
    if (h.name === currentHarness) opt.selected = true;
    batchHarness.appendChild(opt);
  }
  const isBusy = !!batchJobId;
  batchHarness.disabled = isBusy || !!(S.meta && S.meta.agentPinned);
  batchHarness.title = S.meta && S.meta.agentPinned ? 'Fixed for this run by -agent' : 'Change the coding harness';

  const activeH = ready.find(h => h.name === (batchHarness.value || currentHarness)) || ready[0];
  batchModel.innerHTML = '';
  const models = activeH?.models || [];
  if (models.length > 0) {
    for (const m of models) {
      const opt = document.createElement('option');
      opt.value = m;
      opt.textContent = m;
      if (m === currentModel) opt.selected = true;
      batchModel.appendChild(opt);
    }
    batchModel.hidden = false;
    batchModel.disabled = isBusy;
    batchModel.title = 'Model for ' + activeH.name;
  } else {
    batchModel.hidden = true;
  }
}

// Harness/model pickers for the sidebar git panel's "Generate" commit
// message action -- same global selection as every other harness picker,
// just a second view onto it (see syncBatchMeta above).
function syncGitPanelMeta() {
  if (!gitHarness || !gitModel) return;
  const ready = installed();
  const currentHarness = chosen();
  const currentModel = chosenModel();

  gitHarness.innerHTML = '';
  if (!ready.length) {
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = 'no harness';
    gitHarness.appendChild(opt);
    gitHarness.disabled = true;
    gitModel.innerHTML = '';
    gitModel.hidden = true;
    return;
  }

  for (const h of ready) {
    const opt = document.createElement('option');
    opt.value = h.name;
    opt.textContent = h.name;
    if (h.name === currentHarness) opt.selected = true;
    gitHarness.appendChild(opt);
  }
  gitHarness.disabled = !!(S.meta && S.meta.agentPinned);
  gitHarness.title = S.meta && S.meta.agentPinned ? 'Fixed for this run by -agent' : 'Change the coding harness';

  const activeH = ready.find(h => h.name === (gitHarness.value || currentHarness)) || ready[0];
  gitModel.innerHTML = '';
  const models = activeH?.models || [];
  if (models.length > 0) {
    for (const m of models) {
      const opt = document.createElement('option');
      opt.value = m;
      opt.textContent = m;
      if (m === currentModel) opt.selected = true;
      gitModel.appendChild(opt);
    }
    gitModel.hidden = false;
    gitModel.title = 'Model for ' + activeH.name;
  } else {
    gitModel.hidden = true;
  }
}

function getReadySessions() {
  return [...sessions.values()].filter(s => !s.timer && !s.jobId);
}

function syncBatchBar() {
  if (!batchBar) return;
  const total = sessions.size;
  const ready = getReadySessions();
  const runningCount = total - ready.length;
  // Boxes still empty are not comments yet.
  const comments = ready.filter(s => s.input.value.trim()).length;
  const plural = n => n + (n === 1 ? ' comment' : ' comments');

  box.classList.toggle('has-batch', total >= 1);

  if (total < 1 && !batchJobId) {
    batchBar.hidden = true;
    return;
  }
  batchBar.hidden = false;

  if (batchJobId) {
    if (batchCount) {
      batchCount.textContent = comments > 0
        ? comments + ' remaining (' + (activeBatchTargets?.length || 0) + ' in batch)'
        : (activeBatchTargets?.length || 0) + ' in batch';
    }
    if (batchApply) batchApply.hidden = true;
    if (batchCancel) batchCancel.hidden = false;
    if (batchOpen) batchOpen.hidden = !batchThreadId;
  } else {
    if (batchCount) {
      batchCount.textContent = comments === 0 ? 'No comments yet'
        : runningCount > 0 ? comments + ' remaining (' + runningCount + ' running)'
          : plural(comments) + ' in batch';
    }
    if (batchApply) {
      batchApply.hidden = false;
      batchApply.disabled = comments === 0;
      const label = comments === 1 ? 'Apply comment' : 'Apply all (' + comments + ')';
      batchApply.textContent = comments === 0 ? 'Apply all' : label;
      batchApply.title = label + ' (' + keyLabel('Mod+Shift+Enter') + ')';
    }
    if (batchCancel) batchCancel.hidden = true;
    if (batchOpen) batchOpen.hidden = true;
    if (batchHint) {
      batchHint.textContent = comments === 0
        ? (runningCount > 0 ? runningCount + ' running...' : 'Write a comment, then press Enter to add it')
        : 'Select more code to add more';
    }
  }
  syncBatchMeta();
}

function anyInFlight() {
  if (batchTimer || batchJobId) return true;
  for (const s of sessions.values()) if (s.timer || s.jobId) return true;
  return false;
}

function syncBoxVisibility() {
  box.hidden = sessions.size === 0;
  emit('threads:drafts', sessions.size);
  syncBatchBar();
}

export function openAgentEdit(info) {
  if (!info) return;
  for (const s of sessions.values()) {
    if (rangesOverlap(s.target, info)) {
      showToast('!', 'Overlaps the edit already open on ' + targetRef(s.target));
      return;
    }
  }
  emit('threads:reveal'); // the comment boxes live in the Threads pane
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
  // Stack into #agentbox-list inside #agentbox from top to bottom
  const parent = agentListEl || box;
  const existing = [...parent.children];
  let inserted = false;
  for (const child of existing) {
    const s = [...sessions.values()].find(sess => sess.el === child);
    if (s && s.target) {
      if (s.target.path === info.path && s.target.l1 > info.l1) {
        parent.insertBefore(el, child);
        inserted = true;
        break;
      }
    }
  }
  if (!inserted) {
    parent.appendChild(el);
  }
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
    nowBtn: el.querySelector('.agent-now:not(.agent-open-thread)'),
    threadBtn: el.querySelector('.agent-open-thread'),
    threadId: '',
    queuedEl: el.querySelector('.agent-queued'),
    queued: false,
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
  el.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  session.input.focus();
  return session;
}

function wireSession(session) {
  session.sendBtn.addEventListener('click', () => queueComment(session));
  session.nowBtn.addEventListener('click', () => submit(session));
  session.queuedEl.addEventListener('click', () => reopenComment(session));
  session.threadBtn.addEventListener('click', () => emit('threads:open', session.threadId));
  session.input.addEventListener('input', () => syncBatchBar());
  session.cancelBtn?.addEventListener('click', () => cancelSession(session));
  session.closeBtn.addEventListener('click', () => closeAgentEdit(session));
  if (session.refEl) {
    session.refEl.addEventListener('click', () => {
      openFile(session.target.path, { line: session.target.l1 });
    });
  }
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
    } else if ((e[MOD] || e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      if (session.timer || session.jobId) return;
      // Mod+Enter runs this comment alone; adding Shift applies every comment.
      if (e.shiftKey) submitBatch(); else submit(session);
    } else if (e.key === 'Enter' && !e.shiftKey && !session.composeEl.hidden && !session.timer && !session.jobId) {
      e.preventDefault();
      queueComment(session);
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
  syncBatchBar();
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
  session.refEl.title = ref + ' (click to jump)';
}

function clearErr(session) {
  const errEl = session.errEl;
  if (!errEl) return;
  errEl.textContent = '';
  errEl.hidden = true;
}

function attachAlreadyRunningCancel(errContainer) {
  const row = document.createElement('div');
  row.className = 'agent-err-actions';
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'agent-err-cancel-btn';
  btn.textContent = 'Cancel in-flight edit';
  btn.title = 'Stop and cancel running edits on the server';
  btn.onclick = async (e) => {
    e.preventDefault();
    e.stopPropagation();
    btn.disabled = true;
    btn.textContent = 'Cancelling...';
    try {
      await apiPost('/api/agent/cancel', { id: 0 });
      showToast('✓', 'Cancelled running edit');
      errContainer.hidden = true;
    } catch (err) {
      showToast('!', 'Failed to cancel: ' + err.message);
      btn.disabled = false;
      btn.textContent = 'Cancel in-flight edit';
    }
  };
  row.appendChild(btn);
  errContainer.appendChild(row);
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
  if (msg && msg.includes('already running')) {
    attachAlreadyRunningCancel(errEl);
  }
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
  session.hintEl.textContent = 'Enter adds to the batch · ' + keyLabel('Mod+Enter') + ' applies now';
}

/* Adding a comment keeps it without running it, and folds the box down to one
   line so the next piece of code can be selected and commented on. The batch
   bar then applies every comment together. */
function queueComment(session) {
  if (session.timer || session.jobId) return;
  const text = session.input.value.trim();
  if (!text) {
    session.input.focus();
    session.el.classList.remove('shake');
    void session.el.offsetWidth;
    session.el.classList.add('shake');
    return;
  }
  session.queued = true;
  session.el.classList.add('queued');
  session.queuedEl.textContent = text;
  session.queuedEl.hidden = false;
  hideSelectionBar();
  syncBatchBar();
}

function reopenComment(session) {
  if (session.timer || session.jobId) return;
  session.queued = false;
  session.el.classList.remove('queued');
  session.queuedEl.hidden = true;
  session.input.focus();
  syncBatchBar();
}

function setBusy(session, busy, msg) {
  session.el.classList.toggle('busy', busy);
  // A comment applied as part of a batch stays folded, since the batch bar carries the status; it
  // opens again when the run ends so an error is visible.
  if (!busy && session.queued) { session.queued = false; session.el.classList.remove('queued'); session.queuedEl.hidden = true; }
  session.input.disabled = busy;
  if (session.sendBtn) session.sendBtn.hidden = busy;
  if (session.nowBtn) session.nowBtn.hidden = busy;
  if (session.threadBtn) session.threadBtn.hidden = !(busy && session.threadId);
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

  // A single edit shows its own status and Cancel, so its box opens out of the folded state.
  session.queued = false;
  session.el.classList.remove('queued');
  session.queuedEl.hidden = true;
  session.jobId = job.id;
  session.threadId = job.threadId || '';
  session.harness = job.harness;
  hideSelectionBar();
  const initialNote = 'Editing with ' + (chosenModel() ? chosen() + ' (' + chosenModel() + ')' : chosen()) + '...';
  setBusy(session, true, initialNote);
  syncBatchBar();
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
    syncBatchBar();
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

function setBatchBusy(busy, msg) {
  if (!batchBar) return;
  batchBar.classList.toggle('busy', busy);
  if (batchApply) batchApply.hidden = busy;
  if (batchCancel) batchCancel.hidden = !busy;
  if (batchOpen) batchOpen.hidden = !(busy && batchThreadId);
  if (batchClear) batchClear.disabled = busy;
  if (batchHarness) batchHarness.disabled = busy || !!(S.meta && S.meta.agentPinned);
  if (batchModel) batchModel.disabled = busy;
  if (batchHint) {
    if (msg) batchHint.textContent = msg;
    else syncBatchBar();
  }
}

function clearBatchErr() {
  if (!batchErr) return;
  batchErr.textContent = '';
  batchErr.hidden = true;
}

function showBatchErr(msg, streams = []) {
  if (!batchErr) return;
  batchErr.textContent = '';
  const head = document.createElement('div');
  head.className = 'agent-err-msg';
  head.textContent = msg;
  batchErr.appendChild(head);
  if (msg && msg.includes('already running')) {
    attachAlreadyRunningCancel(batchErr);
  }
  for (const [label, text] of streams) {
    if (!text) continue;
    const name = document.createElement('div');
    name.className = 'agent-err-label';
    name.textContent = label;
    const pre = document.createElement('pre');
    pre.className = 'agent-err-out';
    pre.textContent = text;
    batchErr.append(name, pre);
  }
  batchErr.hidden = false;
}

export async function submitBatch() {
  if (batchTimer || batchJobId) return;
  clearBatchErr();
  const ready = getReadySessions();
  const targets = [];
  for (const s of ready) {
    const ins = s.input.value.trim();
    if (ins && s.target) {
      targets.push({ session: s, item: { path: s.target.path, l1: s.target.l1, l2: s.target.l2, instruction: ins } });
    }
  }
  if (!targets.length) {
    if (ready.length > 0) {
      showToast('!', 'Please enter an instruction for the remaining comment(s)');
      ready[0].input.focus();
    } else {
      showToast('!', 'All open edits are already in progress');
    }
    return;
  }
  if (targets.length === 1) {
    submit(targets[0].session);
    return;
  }

  const harnessName = chosen();
  if (!harnessName) {
    showToast('!', 'Please select a coding harness first');
    return;
  }

  let job;
  try {
    job = await apiPostJson('/api/agent/batch', { edits: targets.map(t => t.item) });
  } catch (e) {
    showBatchErr(e.message);
    return;
  }

  batchJobId = job.id;
  batchThreadId = job.threadId || '';
  activeBatchTargets = targets;
  batchElapsed = '';
  hideSelectionBar();
  const initialNote = 'Batch editing ' + targets.length + ' items with ' + (chosenModel() ? chosen() + ' (' + chosenModel() + ')' : chosen()) + '...';
  setBatchBusy(true, initialNote);
  for (const t of targets) {
    t.session.threadId = batchThreadId;
    setBusy(t.session, true, 'Applying in batch...');
  }
  syncBatchBar();
  refreshStatusNote();
  batchTimer = setTimeout(() => tickBatch(targets), 400);
}

async function tickBatch(targets) {
  if (!batchJobId) return;
  let j;
  try {
    j = await api('/api/agent/job?id=' + batchJobId);
  } catch (e) {
    if (!batchJobId) return;
    batchTimer = null;
    if (e.body && 'running' in e.body) {
      await finishBatch(targets, e.body);
      refreshStatusNote();
      return;
    }
    setBatchBusy(false);
    for (const t of targets) {
      setBusy(t.session, false);
      resetHint(t.session);
    }
    syncBatchBar();
    refreshStatusNote();
    showBatchErr(e.message);
    return;
  }

  if (!batchJobId) return;
  if (j.running) {
    batchElapsed = Math.round((j.ms || 0) / 1000) + 's';
    setBatchBusy(true, 'Applying ' + targets.length + ' edits with ' + j.harness + '... ' + batchElapsed);
    refreshStatusNote();
    batchTimer = setTimeout(() => tickBatch(targets), 600);
    return;
  }

  batchTimer = null;
  await finishBatch(targets, j);
  refreshStatusNote();
}

async function finishBatch(targets, j) {
  const currentTargets = targets;
  batchJobId = null;
  batchThreadId = '';
  batchElapsed = '';
  activeBatchTargets = null;
  setBatchBusy(false);

  if (j.error) {
    for (const t of currentTargets) {
      setBusy(t.session, false);
      resetHint(t.session);
    }
    syncBatchBar();
    showBatchErr((j.harness || 'agent') + ': ' + j.error, [
      ['stderr', (j.stderr || '').trim()],
      ['stdout', (j.stdout || j.log || '').trim()],
    ]);
    if (j.changed?.length) reloadWorkspace(null);
    return;
  }

  for (const t of currentTargets) {
    sessions.delete(t.session.id);
    t.session.el.remove();
  }
  syncBoxVisibility();
  syncAgentTargets();

  const changed = j.changed || [];
  if (!changed.length && j.tracked !== false) {
    showToast('✓', 'Finished batch edit with no file changes. Saved in Threads');
    return;
  }

  const focusTarget = currentTargets[0]?.session?.target;
  if (!await reloadWorkspace(focusTarget, 'Batch edited')) return;
  showToast('✓', (!changed.length ? 'Reloaded workspace'
    : changed.length === 1 ? 'Updated ' + changed[0] + ' (' + currentTargets.length + ' edits)'
    : 'Updated ' + changed.length + ' files across ' + currentTargets.length + ' edits') + '. Saved in Threads', 3200);
}

async function cancelBatch() {
  if (!batchTimer && !batchJobId) return;
  if (batchTimer) {
    clearTimeout(batchTimer);
    batchTimer = null;
  }
  const id = batchJobId;
  batchJobId = null;
  batchElapsed = '';
  setBatchBusy(false);
  if (activeBatchTargets) {
    for (const t of activeBatchTargets) {
      setBusy(t.session, false);
      resetHint(t.session);
    }
  }
  activeBatchTargets = null;
  syncBatchBar();
  refreshStatusNote();
  showToast('!', 'Cancelled batch edit');
  if (id) {
    try {
      await apiPost('/api/agent/cancel', { id });
    } catch {}
  }
}

function clearAllEdits() {
  if (batchJobId) cancelBatch();
  for (const s of [...sessions.values()]) {
    closeAgentEdit(s);
  }
}

/* The status bar has one shared note. With one edit running it names the
   harness and how long it has been going; with several, a count is all that
   fits without the bar fighting itself over whose turn it is to speak. */
function refreshStatusNote() {
  const busySessions = [...sessions.values()].filter(s => s.timer || s.jobId);
  const batchCount = batchJobId ? (activeBatchTargets?.length || 0) : 0;
  const individualBusy = busySessions.filter(s => !activeBatchTargets?.some(t => t.session === s));

  if (batchJobId && individualBusy.length > 0) {
    setStatusNote('Batch editing ' + batchCount + ' items + ' + individualBusy.length + ' edit running... ' + (batchElapsed || ''));
  } else if (batchJobId) {
    setStatusNote('Batch editing ' + batchCount + ' items with ' + (chosen()) + '... ' + (batchElapsed || ''));
  } else if (!individualBusy.length) {
    setStatusNote('');
  } else if (individualBusy.length === 1) {
    const s = individualBusy[0];
    setStatusNote('Editing with ' + (s.harness || chosen()) + '... ' + (s.elapsed || ''));
  } else {
    setStatusNote(individualBusy.length + ' edits running...');
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
    showToast('✓', 'Finished with no file changes. Saved in Threads');
    return;
  }

  if (!await reloadWorkspace(editTarget, 'Edited')) return;
  showToast('✓', (!changed.length ? 'Reloaded the workspace'
    : changed.length === 1 ? 'Updated ' + changed[0]
      : 'Updated ' + changed.length + ' files') + '. Saved in Threads', 3200);
}

/* Reload in the order the data depends on: the index first, so the tree and
   git badges agree with disk, then the open tabs, which keep their scroll,
   cursor and view across the swap. Two edits can finish close together, so
   reloads are queued rather than left to interleave. Returns whether it worked. */
let reloadChain = Promise.resolve();
export function reloadWorkspace(focus, what = 'Changed') {
  const run = async () => {
    try {
      await api('/api/reindex');
      await reloadOpenTabs();
      if (focus?.path) {
        await openFile(focus.path, { line: focus.l1, push: false });
      }
      await refreshTree();
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

  if (batchApply) batchApply.addEventListener('click', () => submitBatch());
  if (batchCancel) batchCancel.addEventListener('click', () => cancelBatch());
  if (batchOpen) batchOpen.addEventListener('click', () => emit('threads:open', batchThreadId));
  if (batchClear) batchClear.addEventListener('click', () => clearAllEdits());
  if (batchHarness) {
    batchHarness.addEventListener('change', async () => {
      const hName = batchHarness.value;
      if (!hName) return;
      await select(hName, msg => showBatchErr(msg));
    });
  }
  if (batchModel) {
    batchModel.addEventListener('change', async () => {
      const hName = batchHarness?.value || chosen();
      const mName = batchModel.value;
      await select(hName, mName, msg => showBatchErr(msg));
    });
  }
  if (gitHarness) {
    gitHarness.addEventListener('change', async () => {
      const hName = gitHarness.value;
      if (!hName) return;
      await select(hName, msg => showToast('!', msg));
    });
  }
  if (gitModel) {
    gitModel.addEventListener('change', async () => {
      const hName = gitHarness?.value || chosen();
      const mName = gitModel.value;
      await select(hName, mName, msg => showToast('!', msg));
    });
  }

  document.addEventListener('click', e => {
    const target = /** @type {HTMLElement|null} */ (e.target);
    const row = /** @type {HTMLElement|null} */ (target?.closest('.row.agent-sel, .row.agent-anchor, .diff-row.agent-sel, .diff-row.agent-anchor, .diff-side.agent-sel, .diff-side.agent-anchor'));
    if (!row || !row.dataset.l) return;
    const line = +row.dataset.l;
    const d = doc_();
    if (!d) return;
    for (const s of sessions.values()) {
      if (s.target.path === d.path && line >= s.target.l1 && line <= s.target.l2) {
        s.input.focus();
        s.el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
        break;
      }
    }
  });

  /* At least one harness is still writing to disk: leaving would abandon it
     with no way back to see how it went, so the tab asks first. */
  addEventListener('beforeunload', e => {
    if (!anyInFlight()) return;
    e.preventDefault();
    e.returnValue = '';
  });

  // Probe if an in-flight job is already active on the server
  api('/api/agent/job?id=0').then(j => {
    if (j && j.running) {
      setStatusNote('In-flight edit running on ' + (j.path || 'workspace') + ' (' + (j.harness || 'agent') + ')', 6000);
    }
  }).catch(() => {});
}
