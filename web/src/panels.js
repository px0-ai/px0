// web/src/panels.js
import { $, $$, S, api } from './state.js';
import { layout, render } from './renderer.js';
import { updateStatus } from './status.js';
import { loadOutline } from './outline.js';
import { treeEl, openDirs, drawTree, loadUnpushed } from './tree.js';
import { reloadOpenTabs } from './tabs.js';

export function showPanel(name) {
  document.body.classList.remove('side-hidden');
  layout();
  render();
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
    await loadUnpushed();
    updateStatus();
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

  /* unpushed panel resize */
  (() => {
    const rz = $('#unpushed-resizer'); const up = $('#unpushed'); let dragging = false;
    rz.addEventListener('mousedown', e => { dragging = true; rz.classList.add('drag'); e.preventDefault(); });
    addEventListener('mousemove', e => {
      if (!dragging) return;
      const h = up.getBoundingClientRect().bottom - e.clientY;
      up.style.height = Math.max(80, Math.min(600, h)) + 'px';
    });
    addEventListener('mouseup', () => { if (dragging) { dragging = false; rz.classList.remove('drag'); } });
  })();
}
