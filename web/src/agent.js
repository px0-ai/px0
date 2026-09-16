// web/src/agent.js
import { $, esc, S, api, apiPost } from './state.js';
import { showToast } from './ui.js';
import { setStatusNote } from './status.js';
import { openFile, reloadOpenTabs } from './tabs.js';
import { drawTree, treeEl } from './tree.js';
import { setAgentHandler, hideSelectionBar } from './selbar.js';

/* px0 does not author edits. This box composes an instruction and the range it
   is anchored to, hands both to a coding harness on this machine, and reloads
   whatever moved once that harness exits. Because px0 dispatched the run it
   knows when the work ended, so nothing here watches the filesystem. The last
   run's changes can be reverted from the footer's Undo Edit button.

   Harnesses are detected, not configured: the picker lists what is installed
   and the choice is remembered in the settings file. Detecting one is never
   enough to run it, so the first edit in a fresh install asks which to use. */

const box = $('#agentbox');
const input = $('#agent-input');
const refEl = $('#agent-ref');
const harnessBtn = $('#agent-harness');
const pickEl = $('#agent-pick');
const composeEl = $('#agent-compose');
const sendBtn = $('#agent-send');
const hintEl = $('.agent-hint');
const errEl = $('#agent-err');

let target = null;   // the selection the instruction is anchored to
let timer = null;    // poll timer of the run in flight, or null

const installed = () => (S.meta?.agents || []).filter(h => h.installed);
const chosen = () => (S.meta && S.meta.agent) || '';
const offerable = () => !!chosen() || installed().length > 0;
const refOf = ({ path, l1, l2 }) => path + ':' + (l1 === l2 ? l1 : l1 + '-' + l2);

/* The button ships hidden: only the workspace metadata knows whether any
   harness is installed, and that arrives after the modules are wired up. */
export function applyAgentMeta() {
  const btn = $('[data-sel="agent-edit"]');
  if (btn) btn.hidden = !offerable();
  const foot = $('[data-action="agent-harness"]');
  if (foot) {
    foot.hidden = !offerable();
    foot.querySelector('.footer-btn-label').textContent = 'Agent: ' + (chosen() || 'choose');
    foot.title = S.meta && S.meta.agentPinned
      ? 'Coding harness, fixed for this run by -agent'
      : 'Coding harness for Edit with Agent. Click to change';
  }
  if (harnessBtn) {
    harnessBtn.textContent = chosen() || 'choose harness';
    harnessBtn.disabled = !!(S.meta && S.meta.agentPinned);
    harnessBtn.title = S.meta && S.meta.agentPinned
      ? 'Fixed for this run by -agent'
      : 'Change the coding harness';
  }
}

export function openAgentEdit(info) {
  if (!offerable() || !info) return;
  if (timer) { showToast('!', 'An edit is already running'); return; }
  target = info;
  refEl.textContent = refOf(info);
  refEl.title = refOf(info);
  setBusy(false);
  resetHint();
  clearErr();
  input.value = '';
  box.hidden = false;
  // Nothing runs until a harness has been picked at least once.
  if (chosen()) showCompose(); else showPicker();
}

export function closeAgentEdit() {
  if (timer) return; // Do not dismiss box while edit is in flight
  box.hidden = true;
  setBusy(false);
  clearErr();
  target = null;
}

function clearErr() {
  if (!errEl) return;
  errEl.textContent = '';
  errEl.hidden = true;
}

/* Renders a failure inline under the instruction. A harness run also carries
   what it printed, which is usually the only clue to why it exited non-zero. */
function showErr(msg, streams = []) {
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

function resetHint() {
  if (!hintEl) return;
  hintEl.textContent = target?.fromDiff
    ? 'Editing uncommitted changes · Enter to send'
    : 'Enter to send, Esc to cancel';
}

function setBusy(busy, msg) {
  box.classList.toggle('busy', busy);
  input.disabled = busy;
  sendBtn.disabled = busy;
  harnessBtn.disabled = busy || !!(S.meta && S.meta.agentPinned);
  if (hintEl && msg) hintEl.textContent = msg;
}

function showCompose() {
  pickEl.hidden = true;
  composeEl.hidden = false;
  input.focus();
}

async function showPicker() {
  composeEl.hidden = true;
  pickEl.hidden = false;
  pickEl.innerHTML = '<div class="hint">Looking for coding harnesses…</div>';

  let list = S.meta?.agents || [];
  let settingsPath = '';
  // Re-scan, so a harness installed since startup shows up without a restart.
  try {
    const j = await api('/api/agent/harnesses');
    list = j.harnesses || [];
    settingsPath = j.settings || '';
    S.meta.agents = list;
    S.meta.agent = j.selected || '';
    S.meta.agentPinned = !!j.pinned;
  } catch (e) {
    pickEl.innerHTML = '<div class="hint">Could not look for harnesses: ' + esc(e.message) + '</div>';
    return;
  }

  const ready = list.filter(h => h.installed);
  if (!ready.length) {
    pickEl.innerHTML = '<div class="hint">No coding harness found. Install ' +
      list.map(h => '<b>' + esc(h.name) + '</b>').join(', ') +
      ' and make sure it is on PATH.</div>';
    return;
  }

  pickEl.innerHTML = '<div class="hint">This harness will edit files in this workspace.</div>' +
    optionsHtml(ready, settingsPath);

  pickEl.querySelectorAll('[data-pick]').forEach(b => {
    b.addEventListener('click', () => pick(b.dataset.pick));
  });
}

function optionsHtml(ready, settingsPath) {
  let html = '';
  for (const h of ready) {
    html += '<button class="agent-opt' + (h.name === chosen() ? ' on' : '') + '" data-pick="' + esc(h.name) + '">' +
      '<span class="agent-opt-name">' + esc(h.name) + '</span>' +
      '<code class="agent-opt-cmd">' + esc(h.cmd) + '</code></button>';
  }
  if (settingsPath) html += '<div class="agent-note">Remembered in ' + esc(settingsPath) + '</div>';
  return html;
}

async function pick(name) {
  if (await select(name, showErr)) showCompose();
}

// Makes name the harness for every later edit. Returns whether it took.
async function select(name, onError) {
  try {
    const j = await apiPost('/api/agent/select', { name });
    S.meta.agent = j.selected || '';
    S.meta.agents = j.harnesses || S.meta.agents;
    S.meta.agentPinned = !!j.pinned;
  } catch (e) {
    onError(e.message);
    return false;
  }
  applyAgentMeta();
  showToast('✓', 'Edits will run through ' + name);
  return true;
}

/* ---------- footer: the chosen harness, and a menu to change it ---------- */

const footBtn = $('[data-action="agent-harness"]');
const menuEl = $('#agent-menu');

function closeMenu() {
  if (menuEl) menuEl.hidden = true;
}

async function toggleMenu() {
  if (!menuEl.hidden) { closeMenu(); return; }
  menuEl.innerHTML = '<div class="hint">Looking for coding harnesses…</div>';
  menuEl.hidden = false;
  placeMenu();

  let j;
  try {
    j = await api('/api/agent/harnesses');
  } catch (e) {
    menuEl.innerHTML = '<div class="hint">Could not look for harnesses: ' + esc(e.message) + '</div>';
    return;
  }
  S.meta.agents = j.harnesses || [];
  S.meta.agent = j.selected || '';
  S.meta.agentPinned = !!j.pinned;
  applyAgentMeta();
  if (menuEl.hidden) return;

  const ready = S.meta.agents.filter(h => h.installed);
  let html = '<div class="hint">Coding harness for Edit with Agent</div>';
  if (!ready.length) {
    html += '<div class="hint">None found. Install ' +
      S.meta.agents.map(h => '<b>' + esc(h.name) + '</b>').join(', ') + ' and make sure it is on PATH.</div>';
  } else {
    html += optionsHtml(ready, j.settings || '');
    if (S.meta.agentPinned) html += '<div class="agent-note">Fixed for this run by -agent</div>';
  }
  menuEl.innerHTML = html;
  menuEl.classList.toggle('pinned', S.meta.agentPinned);
  placeMenu();
}

// Opens upward from the footer button, kept inside the window.
function placeMenu() {
  const r = footBtn.getBoundingClientRect();
  menuEl.style.bottom = (innerHeight - r.top + 4) + 'px';
  menuEl.style.left = Math.max(8, Math.min(r.left, innerWidth - menuEl.offsetWidth - 8)) + 'px';
}

async function submit() {
  if (timer) return;
  clearErr();
  const instruction = input.value.trim();
  if (!instruction || !target) return;
  const params = { path: target.path, l1: target.l1, l2: target.l2, instruction };
  /* The uncommitted-work guard exists to make invisible changes visible. In the
     diff view they are on screen and are the reason the user is here, so the
     composer says so in place of a confirm on every single edit. */
  if (target.fromDiff) params.force = 1;

  try {
    await apiPost('/api/agent/edit', params);
  } catch (e) {
    /* Undo only reaches back one edit, so the server refuses once when the
       file holds work that was never committed. */
    if (!/uncommitted/.test(e.message) || !confirm(e.message + '\n\nRun the edit anyway?')) {
      showErr(e.message);
      return;
    }
    try {
      await apiPost('/api/agent/edit', { ...params, force: 1 });
    } catch (e2) {
      showErr(e2.message);
      return;
    }
  }

  hideSelectionBar();
  setUndo(null); // the run replaces whatever the last one could undo
  const initialNote = 'Editing with ' + chosen() + '...';
  setBusy(true, initialNote);
  setStatusNote(initialNote);
  timer = setTimeout(tick, 400);
}

async function tick() {
  let j;
  try {
    j = await api('/api/agent/job');
  } catch (e) {
    timer = null;
    /* A finished job that failed comes back with an error field, which the
       request helper turns into a throw. It still holds the harness output. */
    if (e.body && 'running' in e.body) {
      await finish(e.body);
      return;
    }
    setBusy(false);
    resetHint();
    setStatusNote('');
    showErr(e.message);
    box.hidden = false;
    return;
  }

  if (j.running) {
    const note = 'Editing with ' + j.harness + '... ' + Math.round((j.ms || 0) / 1000) + 's';
    setBusy(true, note);
    setStatusNote(note);
    timer = setTimeout(tick, 600);
    return;
  }

  timer = null;
  await finish(j);
}

async function finish(j) {
  setStatusNote('');
  const editTarget = target;
  // A failed run may still have written files, and those are undoable too.
  setUndo(j);
  if (j.error) {
    setBusy(false);
    resetHint();
    showErr((j.harness || 'agent') + ': ' + j.error, [
      ['stderr', (j.stderr || '').trim()],
      ['stdout', (j.stdout || j.log || '').trim()],
    ]);
    if (j.changed?.length) reloadWorkspace(null);
    box.hidden = false;
    return;
  }

  /* Close the agent compose box on completion */
  box.hidden = true;
  setBusy(false);
  target = null;

  /* Without git px0 cannot tell what the harness touched, so an empty list
     means "unknown" rather than "nothing" and everything is reloaded. */
  const changed = j.changed || [];
  if (!changed.length && j.tracked !== false) {
    showToast('✓', 'Finished with no file changes');
    return;
  }

  if (!await reloadWorkspace(editTarget, 'Edited')) return;
  showToast('✓', (!changed.length ? 'Reloaded the workspace'
    : changed.length === 1 ? 'Updated ' + changed[0]
      : 'Updated ' + changed.length + ' files') + (j.undoable ? ' · Undo in the footer' : ''));
}

/* Reload in the order the data depends on: the index first, so the tree and
   git badges agree with disk, then the open tabs, which keep their scroll,
   cursor and view across the swap. Returns whether it worked. */
async function reloadWorkspace(focus, what = 'Changed') {
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
}

/* ---------- undo the last edit ---------- */

const undoBtn = $('[data-action="agent-undo"]');
let undoJob = null; // the finished job whose changes can still be reversed

function setUndo(j) {
  undoJob = j && j.undoable && j.changed?.length ? j : null;
  if (!undoBtn) return;
  undoBtn.hidden = !undoJob;
  if (undoJob) {
    const n = undoJob.changed.length;
    undoBtn.title = 'Revert what ' + undoJob.harness + ' changed: ' + undoJob.changed.join(', ');
    undoBtn.querySelector('.footer-btn-label').textContent = n === 1 ? 'Undo Edit' : 'Undo Edit (' + n + ')';
  }
}

async function undo() {
  const j = undoJob;
  if (!j || timer) return;
  const files = j.changed;
  const list = files.slice(0, 12).join('\n') + (files.length > 12 ? '\n…and ' + (files.length - 12) + ' more' : '');
  if (!confirm('Revert what ' + j.harness + ' changed?\n\n' + list)) return;

  let res;
  try {
    res = await apiPost('/api/agent/undo');
  } catch (e) {
    // Something moved since the edit. Say what, and let the user decide.
    if (!/changed since/.test(e.message) ||
        !confirm(e.message + '.\n\nUndo anyway and lose those later changes?')) {
      showToast('!', e.message);
      if (!/changed since/.test(e.message)) setUndo(null);
      return;
    }
    try {
      res = await apiPost('/api/agent/undo', { force: 1 });
    } catch (e2) {
      showToast('!', e2.message);
      setUndo(null);
      await reloadWorkspace(null, 'Undo failed');
      return;
    }
  }
  setUndo(null);
  const undone = res.undone || files;
  if (!await reloadWorkspace(null, 'Undone')) return;
  showToast('✓', undone.length === 1 ? 'Reverted ' + undone[0] : 'Reverted ' + undone.length + ' files');
}

export function initAgent() {
  if (!box) return;
  setAgentHandler(openAgentEdit);

  sendBtn.addEventListener('click', submit);
  undoBtn?.addEventListener('click', undo);
  // After a page reload the last edit may still be undoable.
  api('/api/agent/job').then(setUndo, e => { if (e.body) setUndo(e.body); });
  if (footBtn && menuEl) {
    footBtn.addEventListener('click', toggleMenu);
    menuEl.addEventListener('click', async e => {
      const b = e.target.closest('[data-pick]');
      if (!b || S.meta.agentPinned || timer) return;
      if (await select(b.dataset.pick, msg => showToast('!', msg))) closeMenu();
    });
    document.addEventListener('mousedown', e => {
      if (!menuEl.hidden && !menuEl.contains(e.target) && !footBtn.contains(e.target)) closeMenu();
    });
    addEventListener('keydown', e => { if (e.key === 'Escape') closeMenu(); });
    addEventListener('resize', closeMenu);
  }
  harnessBtn.addEventListener('click', () => {
    if (!harnessBtn.disabled) showPicker();
  });

  /* The composer swallows every key while it is open. Nothing typed into an
     instruction should also fire a viewport shortcut. */
  box.addEventListener('keydown', e => {
    e.stopPropagation();
    if (e.key === 'Escape') {
      e.preventDefault();
      closeAgentEdit();
    } else if (e.key === 'Enter' && !e.shiftKey && !composeEl.hidden && !timer) {
      e.preventDefault();
      submit();
    }
  });
}
