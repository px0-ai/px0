import { S, doc_, api } from './state.js';
import { vp } from './ui.js';
import { createFolds, foldIndex, rebuildFolds, visibleFoldLine } from './folding.js';
import { layout, render, lineAtScrollY, lineTopY } from './renderer.js';
import { updateStatus, setStatusNote } from './status.js';
import { clearSelectAll } from './selbar.js';
import { on } from './bus.js';
import { drawMinimap } from './find.js';
import { previewing } from './markdown.js';

let activeFoldRequest = null;

function canFoldDoc(d) {
  return d && !d.isImage && !d.deleted && !d.isEditing && !d.diffMode && !previewing(d);
}

export function releaseDocFolds(d) {
  d?.foldRequest?.abort();
  if (activeFoldRequest?.doc === d) activeFoldRequest = null;
  if (d) { d.foldRequest = null; d.foldPromise = null; d.folds = null; }
}

// First source paint starts one deferred request. Only the active document may
// request detection; closing/reloading/switching aborts work and stale replies
// cannot retain buffers or repopulate discarded documents.
export function loadDocFolds(d) {
  if (d.foldLoaded || !canFoldDoc(d)) return null;
  if (d.foldRequest) return d.foldPromise;
  if (activeFoldRequest) {
    activeFoldRequest.controller.abort();
    activeFoldRequest.doc.foldRequest = null;
  }
  const controller = new AbortController();
  d.foldRequest = controller;
  activeFoldRequest = { doc: d, controller };
  d.foldPromise = new Promise(resolve => setTimeout(resolve, 0)).then(async () => {
    if (controller.signal.aborted) return;
    try {
      const j = await api('/api/folds', { path: d.path, version: d.version }, { signal: controller.signal });
      if (controller.signal.aborted || !S.tabs.includes(d) || j.version !== d.version) return;
      d.foldLoaded = true;
      d.foldError = '';
      d.foldLimited = !!j.limited;
      if (j.ranges?.length) d.folds = createFolds(j.ranges, d.total);
      if (doc_() === d) render();
    } catch (e) {
      if (!controller.signal.aborted) { d.foldLoaded = true; d.foldError = e.message || 'Could not load code folding'; }
    } finally {
      if (d.foldRequest === controller) { d.foldRequest = null; d.foldPromise = null; }
      if (activeFoldRequest?.controller === controller) activeFoldRequest = null;
    }
  });
  return d.foldPromise;
}

export async function runFoldAction(action, line) {
  const d = doc_();
  if (!canFoldDoc(d)) return;
  if (d.foldError) { d.foldLoaded = false; d.foldError = ''; }
  await loadDocFolds(d);
  if (doc_() !== d || !canFoldDoc(d)) return;
  const f = d.folds;
  if (d.foldError) { setStatusNote(d.foldError, 3000); return; }
  if (!f?.ranges.length) {
    setStatusNote(d.foldLimited ? 'Folding limit reached for this file' : 'No foldable blocks in this file', 2500);
    return;
  }
  const anchor = lineAtScrollY(vp.scrollTop, d);
  const within = vp.scrollTop - lineTopY(anchor, d);
  if (action === 'all' || action === 'none') {
    f.closed.fill(action === 'all' ? 1 : 0);
  } else {
    const target = line || d.cur;
    let idx = foldIndex(f, target);
    if (idx < 0) {
      for (let i = 0; i < f.closed.length && f.ranges[i * 2] <= target; i++) {
        if (f.ranges[i * 2 + 1] >= target && (action !== 'unfold' || f.closed[i])) idx = i;
      }
    }
    if (idx < 0) return;
    f.closed[idx] = action === 'toggle' ? 1 - f.closed[idx] : action === 'fold' ? 1 : 0;
  }
  rebuildFolds(f);
  d.cur = visibleFoldLine(f, d.cur);
  d.col = 0; d.selAnchor = null; S.at = null;
  clearSelectAll();
  window.getSelection()?.removeAllRanges();
  layout();
  vp.scrollTop = Math.max(0, lineTopY(visibleFoldLine(f, anchor), d) + within);
  render(); updateStatus();
  if (S.find && !S.find.preview) drawMinimap(S.find.hits, d.total);
}

export function initCodeFolding() {
  on('tab:activated', () => {
    if (activeFoldRequest && activeFoldRequest.doc !== doc_()) {
      activeFoldRequest.controller.abort();
      activeFoldRequest.doc.foldRequest = null;
      activeFoldRequest = null;
    }
  });
  vp.addEventListener('mousedown', e => {
    if (e.target.closest('[data-fold]')) e.preventDefault();
  });
  vp.addEventListener('click', e => {
    const button = e.target.closest('[data-fold]');
    if (!button) return;
    e.preventDefault(); e.stopPropagation();
    runFoldAction('toggle', +button.dataset.fold);
  });
}
