// web/src/panels.js
import { $, $$, S, api, doc_ } from './state.js';
import { layout, render } from './renderer.js';
import { updateStatus } from './status.js';
import { loadOutline } from './outline.js';
import { treeEl, openDirs, drawTree, flushPendingReveal, syncTreeSelection } from './tree.js';
import { reloadOpenTabs } from './tabs.js';

/* Single entry point for sidebar visibility. Everything that shows the
   sidebar goes through here so a reveal deferred while it was hidden gets
   replayed, and so the editor is always re-laid out for the new width. */
export function toggleSidebar(show) {
  const hide = show === undefined ? !document.body.classList.contains('side-hidden') : !show;
  document.body.classList.toggle('side-hidden', hide);
  if (!hide) flushPendingReveal();
  layout();
  render();
}

export function showPanel(name) {
  toggleSidebar(true);
}

export function initPanels() {
  $('#btn-reindex').addEventListener('click', async () => {
    $('#st-index').textContent = 'reindexing…';
    const j = await api('/api/reindex');
    S.meta.files = j.files; S.meta.indexMs = j.indexMs;
    treeEl.innerHTML = ''; openDirs.clear();
    await drawTree('', treeEl, 0);
    // Reindex is a refresh: re-fetch open tabs quietly in place without tab switching.
    await reloadOpenTabs();
    updateStatus();
    // The tree was rebuilt from scratch above, so put the selection back on
    // the file the user is actually looking at.
    syncTreeSelection(doc_()?.path);
  });

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
