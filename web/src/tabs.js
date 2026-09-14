// web/src/tabs.js
import { $, esc, S, doc_, api, LH, CHUNK, withKeys } from './state.js';
import { vp, sizer, rowsEl } from './ui.js';
import { render, layout, refineChunk } from './renderer.js';
import { updateStatus, setStatusNote, refreshMetrics } from './status.js';
import { pushHistory } from './history.js';
import { warmLSP } from './lsp.js';
import { loadOutline } from './outline.js';
import { showPanel } from './panels.js';
import { revealDir } from './tree.js';
import { clearLink } from './hover.js';
import { clearFind } from './find.js';
import { clearSelectAll } from './selbar.js';
import { syncPreview, previewing, previewLine } from './markdown.js';
import { syncDiffView, layoutPref } from './diff.js';
import { isImage, syncImage } from './image.js';

// Recently closed files, newest last, for Alt+Shift+T.
const closedTabs = [];
const MAX_CLOSED = 20;

export async function openFile(path, opts = {}) {
  const { line, push = true, col } = opts;
  let idx = S.tabs.findIndex(t => t.path === path);
  if (idx < 0) {
    let j;
    const start = line ? Math.max(0, Math.floor((line - 1) / CHUNK) * CHUNK) : 0;
    try {
      j = await api('/api/file', { path, start, count: CHUNK });
    } catch (e) {
      setStatusNote(path + ': ' + e.message);
      return;
    }
    if (j.image) {
      // Image files become lightweight tabs (no lines, gutter, LSP or
      // outline), rendered by a single reused <img> in syncImage().
      S.tabs.push({
        path, name: path.split('/').pop(), image: true,
        size: j.size, total: 0, maxCols: 0,
        lines: [], chunks: new Set(), pending: new Set(), refining: new Set(),
        scrollTop: 0, imgScroll: 0, cur: 1, col: 0,
        outline: [], outlineLSP: true, gutter: null,
        lsp: { state: 'off', server: '' },
        markdown: false, diffMode: null, diffAvailable: false,
      });
      idx = S.tabs.length - 1;
    } else {
      const hasDiff = !!j.diffAvailable;
      const d = {
        path, name: path.split('/').pop(), lang: j.lang, total: j.total, maxCols: j.maxCols,
        size: j.size, lines: new Array(j.total), chunks: new Set([start / CHUNK]),
        pending: new Set(), refining: new Set(), scrollTop: 0, cur: line || 1,
        outline: null, gen: 0, markdown: !!j.markdown, gutter: null,
        diffMode: hasDiff ? (layoutPref() || 'split') : null,
        diffAvailable: hasDiff,
        diffDismissed: false,
      };
      for (let i = 0; i < j.lines.length; i++) d.lines[j.start + i] = j.lines[i];
      d.lsp = j.lsp || { state: 'off', server: '' };
      S.tabs.push(d);
      idx = S.tabs.length - 1;
      if (j.refine) refineChunk(d, start / CHUNK);
      loadGutter(d);
    }
  }
  const prev = doc_();
  if (prev && prev !== S.tabs[idx]) saveScroll(prev);
  if (prev !== S.tabs[idx]) clearSelectAll();
  S.active = idx;
  const d = S.tabs[idx];

  $('#empty').hidden = true;
  syncImage();
  syncPreview();
  syncDiffView();
  if (!S.at || S.at.path !== d.path) S.at = null;
  S.lsp.state = (d.lsp && d.lsp.state) || 'off';
  S.lsp.server = (d.lsp && d.lsp.server) || '';
  S.lsp.missing = (d.lsp && d.lsp.missing) || '';
  if (!isImage(d)) warmLSP(d);
  drawTabs(); drawCrumbs(); layout();

  if (isImage(d)) {
    if (push) pushHistory(path, 1, col);
  } else if (line) { d.cur = line; centerLine(line); }
  else vp.scrollTop = d.scrollTop;
  render();
  updateStatus();
  if (!isImage(d) && $('#panel-outline')?.classList.contains('active')) loadOutline();
  if (!isImage(d) && push) pushHistory(path, line || d.cur, col);
}

// The code viewport and the image viewer scroll independently; stash the one
// that is currently visible so switching tabs restores each where it was left.
function saveScroll(d) {
  if (isImage(d)) {
    const v = $('#imgview');
    if (v) d.imgScroll = v.scrollTop;
  } else {
    d.scrollTop = vp.scrollTop;
  }
}

// VS Code-style diff gutter for the normal file view. Fetches once per opened
// doc and caches on it (each tab keeps its own; switching tabs needs no clear).
// Fetches on any open in a git repo rather than threading per-file status
// through every open path — the backend returns available:false for
// clean/untracked files, so the extra request is cheap and self-limiting.
function loadGutter(d) {
  if (!S.meta?.git) return;
  api('/api/gutter', { path: d.path }).then(j => {
    d.diffAvailable = !!j.available;
    if (j.available && d.diffMode === null && !d.diffDismissed) {
      d.diffMode = layoutPref() || 'split';
      if (doc_() === d) {
        syncDiffView();
        syncPreview();
      }
    }
    if (doc_() === d) updateStatus();
    if (!j.available) return;
    const marks = new Map();
    for (const n of j.modified) marks.set(n, 'mod');
    for (const n of j.added) marks.set(n, 'add');
    d.gutter = { marks, dels: new Set(j.deleted) };
    if (doc_() === d) render();
  }).catch(() => {});
}

// Quietly re-fetches all open tabs on workspace reindex without tab-switching thrash.
// Preserves live scroll position, cursor column/line (clamped), diff settings, and markdown scroll.
export async function reloadOpenTabs() {
  if (S.tabs.length === 0) return;

  const activeDoc = doc_();
  if (activeDoc) {
    if (isImage(activeDoc)) {
      const iv = $('#imgview');
      if (iv) activeDoc.imgScroll = iv.scrollTop;
    } else {
      activeDoc.scrollTop = vp.scrollTop;
    }
    if (previewing(activeDoc)) {
      const mv = $('#mdview');
      if (mv) activeDoc.mdScroll = mv.scrollTop;
    }
  }

  const targets = S.tabs.map(t => ({
    oldDoc: t,
    path: t.path,
    anchor: t.cur || 1,
    start: t.cur ? Math.max(0, Math.floor((t.cur - 1) / CHUNK) * CHUNK) : 0,
  }));

  const results = await Promise.allSettled(
    targets.map(tgt => api('/api/file', { path: tgt.path, start: tgt.start, count: CHUNK }))
  );

  for (let i = 0; i < targets.length; i++) {
    const res = results[i];
    const tgt = targets[i];
    const idx = S.tabs.indexOf(tgt.oldDoc);
    if (idx < 0) continue; // tab closed while reloading

    if (res.status !== 'fulfilled') {
      if (idx === S.active) {
        setStatusNote(tgt.path + ': ' + (res.reason?.message || 'failed to load'));
      }
      continue;
    }

    const j = res.value;
    if (j.image) continue;

    const keep = tgt.oldDoc;
    const hasDiff = !!j.diffAvailable;
    const newCur = Math.max(1, Math.min(keep.cur || 1, j.total));

    let diffMode = null;
    if (hasDiff) {
      if (keep.diffDismissed) {
        diffMode = null;
      } else if (keep.diffMode) {
        diffMode = keep.diffMode;
      } else {
        diffMode = layoutPref() || 'split';
      }
    }

    const d = {
      path: tgt.path,
      name: tgt.path.split('/').pop(),
      lang: j.lang,
      total: j.total,
      maxCols: j.maxCols,
      size: j.size,
      lines: new Array(j.total),
      chunks: new Set([tgt.start / CHUNK]),
      pending: new Set(),
      refining: new Set(),
      scrollTop: keep.scrollTop || 0,
      cur: newCur,
      col: keep.col || 0,
      outline: null,
      gen: 0,
      markdown: !!j.markdown,
      mdScroll: keep.mdScroll || 0,
      gutter: null,
      diffMode,
      diffAvailable: hasDiff,
      diffDismissed: !!keep.diffDismissed,
    };

    for (let k = 0; k < j.lines.length; k++) {
      d.lines[j.start + k] = j.lines[k];
    }
    d.lsp = j.lsp || { state: 'off', server: '' };

    S.tabs[idx] = d;
    if (j.refine) refineChunk(d, tgt.start / CHUNK);
    loadGutter(d);
  }

  const d = doc_();
  if (d) {
    S.lsp.state = (d.lsp && d.lsp.state) || 'off';
    S.lsp.server = (d.lsp && d.lsp.server) || '';
    S.lsp.missing = (d.lsp && d.lsp.missing) || '';
    if (!isImage(d)) warmLSP(d);
    syncImage();
    syncPreview();
    syncDiffView();
    layout();
    if (isImage(d)) { const iv = $('#imgview'); if (iv) iv.scrollTop = d.imgScroll || 0; }
    else vp.scrollTop = d.scrollTop;
    render();
    if (!isImage(d) && $('#panel-outline')?.classList.contains('active')) loadOutline();
  }

  drawTabs();
  drawCrumbs();
  updateStatus();
}

export function centerLine(n) {
  if (previewing()) { previewLine(n); return; }
  const y = (n - 1) * LH - Math.max(0, vp.clientHeight / 2 - LH * 2);
  vp.scrollTop = Math.max(0, y);
}

export function closeTab(i) {
  clearSelectAll();
  const [closed] = S.tabs.splice(i, 1);
  if (closed) {
    if (closed.path) {
      // The active tab's scroll is only saved on switch, so read the live one.
      let scrollTop = closed.scrollTop || 0;
      if (isImage(closed)) {
        const v = $('#imgview');
        scrollTop = (i === S.active && v) ? v.scrollTop : (closed.imgScroll || 0);
        closed.imgScroll = scrollTop;
      } else {
        scrollTop = i === S.active ? vp.scrollTop : closed.scrollTop;
      }
      closedTabs.push({ path: closed.path, cur: closed.cur, scrollTop });
      if (closedTabs.length > MAX_CLOSED) closedTabs.shift();
      if (!isImage(closed)) {
        // Images never opened a server doc; nothing to evict.
        api('/api/close', { path: closed.path })
          .then(() => refreshMetrics())
          .catch(() => {});
      }
    }
    // Release large arrays to assist garbage collection
    closed.lines = null;
    closed.chunks?.clear?.();
    closed.pending?.clear?.();
    closed.refining?.clear?.();
    closed.outline = null;
  }
  if (S.tabs.length === 0) {
    S.active = -1;
    syncImage();
    syncPreview();
    syncDiffView();
    rowsEl.innerHTML = ''; sizer.style.height = '0px';
    $('#empty').hidden = false; drawCrumbs();
    drawTabs(); updateStatus();
    return;
  }
  S.active = Math.min(i, S.tabs.length - 1);
  const d = doc_();
  syncImage();
  syncPreview();
  syncDiffView();
  drawTabs(); drawCrumbs(); layout();
  if (isImage(d)) { const v = $('#imgview'); if (v) v.scrollTop = d.imgScroll || 0; }
  else vp.scrollTop = d.scrollTop;
  render(); updateStatus();
}

// Reopens the most recently closed file that is not open already, where it was left.
export async function reopenClosedTab() {
  while (closedTabs.length) {
    const t = closedTabs.pop();
    if (S.tabs.some(d => d.path === t.path)) continue;
    await openFile(t.path, { line: t.cur });
    if (doc_()?.path !== t.path) return;
    const d = doc_();
    if (isImage(d)) { const v = $('#imgview'); if (v) v.scrollTop = t.scrollTop || 0; d.imgScroll = t.scrollTop || 0; }
    else vp.scrollTop = t.scrollTop;
    render(); updateStatus();
    return;
  }
}

export function drawTabs() {
  $('#tabs').innerHTML = S.tabs.map((t, i) =>
    '<div class="tab' + (i === S.active ? ' active' : '') + '" data-i="' + i + '" title="' + esc(t.path) + '">' +
    '<span class="tn">' + esc(t.name) + '</span><span class="x" data-close="' + i + '" title="' + withKeys('Close tab ({Alt+W})') + '"><svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6"/></svg></span></div>').join('');
  const act = $('#tabs .tab.active');
  if (act) act.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

export function switchTab(i) {
  if (i === S.active || !S.tabs[i]) return;
  clearLink();
  const prev = doc_();
  if (prev) saveScroll(prev);
  S.active = i;
  syncImage();
  syncPreview();
  syncDiffView();
  clearFind();
  clearSelectAll();
  S.at = null;
  S.lsp.state = (S.tabs[i].lsp && S.tabs[i].lsp.state) || 'off';
  S.lsp.server = (S.tabs[i].lsp && S.tabs[i].lsp.server) || '';
  S.lsp.missing = (S.tabs[i].lsp && S.tabs[i].lsp.missing) || '';
  if (!isImage(S.tabs[i])) warmLSP(S.tabs[i]);
  drawTabs(); drawCrumbs(); layout();
  if (isImage(S.tabs[i])) { const v = $('#imgview'); if (v) v.scrollTop = S.tabs[i].imgScroll || 0; }
  else vp.scrollTop = S.tabs[i].scrollTop;
  render(); updateStatus();
  if (!isImage(S.tabs[i]) && $('#panel-outline')?.classList.contains('active')) loadOutline();
  pushHistory(S.tabs[i].path, S.tabs[i].cur);
}

export function drawCrumbs() {
  const el = $('#crumbs');
  if (el) el.innerHTML = '';
}

export function initTabs() {
  $('#tabs').addEventListener('click', e => {
    const x = e.target.closest('[data-close]');
    if (x) { closeTab(+x.dataset.close); return; }
    const t = e.target.closest('.tab');
    if (t) switchTab(+t.dataset.i);
  });
  $('#tabs').addEventListener('auxclick', e => {
    const t = e.target.closest('.tab');
    if (t && e.button === 1) { e.preventDefault(); closeTab(+t.dataset.i); }
  });
  const crumbsEl = $('#crumbs');
  if (crumbsEl) {
    crumbsEl.addEventListener('click', e => {
      const c = e.target.closest('[data-dir]');
      if (c) { showPanel('files'); revealDir(c.dataset.dir); }
    });
  }
}
