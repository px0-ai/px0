// web/src/panels.js
import { $, $$, S, api, apiPost, withKeys } from './state.js';
import { showToast } from './ui.js';
import { layout, render } from './renderer.js';
import { updateStatus } from './status.js';
import { loadOutline } from './outline.js';
import { treeEl, openDirs, drawTree } from './tree.js';
import { reloadOpenTabs } from './tabs.js';

export function showPanel(name) {
  document.body.classList.remove('side-hidden');
  layout();
  render();
}

/* Re-index is a refresh: rebuild the tree and reload open tabs in place. A
   caller holding a reindex-shaped answer (the checkpoint endpoint) passes it in. */
export async function refreshWorkspace(j) {
  if (!j) {
    $('#st-index').textContent = 'reindexing…';
    j = await api('/api/reindex');
  }
  S.meta.files = j.files; S.meta.indexMs = j.indexMs;
  if (j.checkpoint) S.checkpoint = j.checkpoint;
  treeEl.innerHTML = ''; openDirs.clear();
  await drawTree('', treeEl, 0);
  await reloadOpenTabs();
  updateCheckpointUI();
  updateStatus();
}

/* A review checkpoint marks "now": from then on the tree badges every file added
   or changed since, with or without git. The server holds it in memory. */
export async function setCheckpoint() {
  let j;
  try { j = await apiPost('/api/checkpoint'); }
  catch (e) { showToast('!', 'Could not set checkpoint: ' + e.message); return; }
  await refreshWorkspace(j);
  showToast('Checkpoint set', 'files that change from now on are badged');
}

export async function clearCheckpoint() {
  let j;
  try { j = await apiPost('/api/checkpoint', { clear: 1 }); }
  catch (e) { showToast('!', 'Could not clear checkpoint: ' + e.message); return; }
  await refreshWorkspace(j);
  showToast('Checkpoint cleared', 'tree badges follow git again');
}

/* Header button state and tooltip. The changed-only filter is offered whenever
   git status or a checkpoint gives it something to filter. */
export function updateCheckpointUI() {
  const ck = S.checkpoint;
  const on = !!(ck && ck.active);
  const b = $('#btn-checkpoint');
  if (b) {
    b.classList.toggle('active', on);
    b.title = on
      ? withKeys('Review checkpoint set ' + ago(ck.at) + ': ' + ck.added + ' added, ' + ck.modified + ' changed, ' +
          ck.removed + ' removed since. Click to move it to now ({Alt+K}). Clear it from the command palette.')
      : withKeys('Set review checkpoint: badge what changes from now on ({Alt+K})');
  }
  const f = $('#btn-changed');
  if (f && (S.meta?.git || on)) f.hidden = false;
}

function ago(iso) {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return s + 's ago';
  if (s < 3600) return Math.round(s / 60) + 'm ago';
  return Math.round(s / 3600) + 'h ago';
}

export function initPanels() {
  $('#btn-reindex').addEventListener('click', () => refreshWorkspace());
  $('#btn-checkpoint')?.addEventListener('click', () => setCheckpoint());

  /* sidebar resize */
  (() => {
    const rz = $('#resizer'); let dragging = false;
    rz.addEventListener('mousedown', e => { dragging = true; rz.classList.add('drag'); e.preventDefault(); });
    addEventListener('mousemove', e => {
      if (!dragging) return;
      $('#side').style.width = Math.max(170, Math.min(620, e.clientX)) + 'px';
    });
    addEventListener('mouseup', () => { if (dragging) { dragging = false; rz.classList.remove('drag'); layout(); render(); } });
  })();
}
