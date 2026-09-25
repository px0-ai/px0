// web/src/tabs.js
import { $, esc, S, doc_, api, apiPost, apiPostJson, LH, CHUNK, withKeys } from './state.js';
import { emit } from './bus.js';
import { vp, sizer, rowsEl, editor } from './ui.js';
import { render, layout, refineChunk } from './renderer.js';
import { updateStatus, setStatusNote, refreshMetrics } from './status.js';
import { pushHistory } from './history.js';
import { warmLSP } from './lsp.js';
import { loadOutline } from './outline.js';
import { showPanel } from './panels.js';
import { revealDir, treeEl } from './tree.js';
import { clearLink } from './hover.js';
import { clearFind } from './find.js';
import { clearSelectAll } from './selbar.js';
import { syncPreview, previewing, previewLine } from './markdown.js';
import { syncDiffView, layoutPref, diffScrollTop, setDiffMode, setSourceJumpHandler } from './diff.js';
import { syncImageView } from './imageview.js';

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
      setStatusNote(path + ': ' + e.message, 4000);
      return;
    }
    const isImg = !!j.image;
    const hasDiff = !isImg && !!j.diffAvailable;
    const d = {
      path, name: path.split('/').pop(), lang: isImg ? 'image' : j.lang,
      total: isImg ? 0 : j.total, maxCols: isImg ? 0 : j.maxCols,
      size: j.size, lines: isImg ? [] : new Array(j.total),
      chunks: new Set(isImg ? [] : [start / CHUNK]),
      pending: new Set(), refining: new Set(), scrollTop: 0, cur: line || 1,
      outline: null, gen: 0, markdown: !isImg && !!j.markdown, isImage: isImg,
      gutter: null,
      diffMode: hasDiff ? (layoutPref() || 'split') : null,
      diffAvailable: hasDiff,
      diffDismissed: false,
      openedInDiffView: hasDiff,
    };
    if (!isImg) {
      for (let i = 0; i < j.lines.length; i++) d.lines[j.start + i] = j.lines[i];
    }
    d.lsp = (!isImg && j.lsp) || { state: 'off', server: '' };
    S.tabs.push(d);
    idx = S.tabs.length - 1;
    if (!isImg && j.refine) refineChunk(d, start / CHUNK);
    if (!isImg) loadGutter(d);
  }
  const prev = doc_();
  if (prev && prev !== S.tabs[idx]) prev.scrollTop = vp.scrollTop;
  if (prev !== S.tabs[idx]) { clearSelectAll(); clearFind(); }
  S.active = idx;
  const d = S.tabs[idx];
  if (d && d.diffAvailable && (treeEl?.classList.contains('changed-only') || (!d.diffDismissed && d.diffMode === null))) {
    d.diffMode = layoutPref() || 'split';
    d.diffDismissed = false;
    d.openedInDiffView = true;
  }

  $('#empty').hidden = true;
  syncImageView();
  syncPreview();
  syncDiffView();
  if (!S.at || S.at.path !== d.path) S.at = null;
  S.lsp.state = (d.lsp && d.lsp.state) || 'off';
  S.lsp.server = (d.lsp && d.lsp.server) || '';
  S.lsp.missing = (d.lsp && d.lsp.missing) || '';
  warmLSP(d);
  drawTabs(); drawCrumbs(); layout();

  if (line) { d.cur = line; centerLine(line); }
  else vp.scrollTop = d.scrollTop;
  render();
  updateStatus();
  if ($('#panel-outline')?.classList.contains('active')) loadOutline();
  if (push) pushHistory(path, line || d.cur);
  saveWorkspaceState();
  emit('tab:activated', { doc: d, prevDoc: prev });
}

// VS Code-style diff gutter for the normal file view. Fetches once per opened
// doc and caches on it (each tab keeps its own; switching tabs needs no clear).
// Fetches on any open in a git repo rather than threading per-file status
// through every open path — the backend returns available:false for
// clean/untracked files, so the extra request is cheap and self-limiting.
export async function loadGutter(d) {
  if (!S.meta?.git) return;
  try {
    const j = await api('/api/gutter', { path: d.path });
    d.diffAvailable = !!j.available;
    if (j.available && d.diffMode === null && !d.diffDismissed) {
      d.diffMode = layoutPref() || 'split';
      d.openedInDiffView = true;
      if (doc_() === d) {
        syncDiffView();
        syncPreview();
      }
    }
    if (!j.available) {
      d.gutter = null;
    } else {
      const marks = new Map();
      for (const n of j.modified) marks.set(n, 'mod');
      for (const n of j.added) marks.set(n, 'add');
      d.gutter = { marks, dels: new Set(j.deleted) };
    }
    if (doc_() === d) {
      updateStatus();
      render();
    }
    drawTabs();
  } catch {}
}

// Quietly re-fetches all open tabs on workspace reindex without tab-switching thrash.
// Preserves live scroll position, cursor column/line (clamped), diff settings, and markdown scroll.
// onlyIfChanged: leave a tab's doc untouched, and skip the repaint when no tab
// changed, if the file comes back with the same size, line count and diff
// availability. Keeps a background refresh from flickering an unchanged view.
export async function reloadOpenTabs({ onlyIfChanged = false } = {}) {
  if (S.tabs.length === 0) return;

  const activeDoc = doc_();
  if (activeDoc) {
    activeDoc.scrollTop = vp.scrollTop;
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

  let anyChanged = false;
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
        setStatusNote(tgt.path + ': ' + (res.reason?.message || 'failed to load'), 4000);
      }
      continue;
    }

    const j = res.value;
    if (j.image) {
      tgt.oldDoc.size = j.size;
      continue;
    }

    const keep = tgt.oldDoc;
    const hasDiff = !!j.diffAvailable;
    if (onlyIfChanged && keep.size === j.size && keep.total === j.total &&
        !!keep.diffAvailable === hasDiff) continue;
    anyChanged = true;
    const newCur = Math.max(1, Math.min(keep.cur || 1, j.total));

    /* A reload keeps each tab in the view it was in. The file changing under
       it, say from an agent edit, is no reason to swap source for a diff, so a
       tab in source is marked dismissed and loadGutter leaves it there too. */
    const diffMode = hasDiff ? (keep.diffMode || null) : null;

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
      diffDismissed: !!keep.diffDismissed || !keep.diffMode,
      openedInDiffView: !!keep.openedInDiffView || !!keep.diffMode,
      diffScroll: keep === activeDoc && keep.diffMode ? diffScrollTop() : 0,
      prCollapsed: keep.prCollapsed,
      youCollapsed: keep.youCollapsed,
    };

    for (let k = 0; k < j.lines.length; k++) {
      d.lines[j.start + k] = j.lines[k];
    }
    d.lsp = j.lsp || { state: 'off', server: '' };

    S.tabs[idx] = d;
    if (j.refine) refineChunk(d, tgt.start / CHUNK);
  }

  if (onlyIfChanged && !anyChanged) return;

  // Load all gutters concurrently before initial paint
  await Promise.allSettled(S.tabs.filter(t => !t.isImage).map(t => loadGutter(t)));

  const d = doc_();
  if (d) {
    S.lsp.state = (d.lsp && d.lsp.state) || 'off';
    S.lsp.server = (d.lsp && d.lsp.server) || '';
    S.lsp.missing = (d.lsp && d.lsp.missing) || '';
    warmLSP(d);
    syncImageView();
    syncPreview();
    syncDiffView(true);
    layout();
    vp.scrollTop = d.scrollTop;
    render();
    if ($('#panel-outline')?.classList.contains('active')) loadOutline();
  }

  drawTabs();
  drawCrumbs();
  updateStatus();
  saveWorkspaceState();
  if (d) emit('tab:activated', { doc: d });
}

export function centerLine(n) {
  if (previewing()) { previewLine(n); return; }
  const y = (n - 1) * LH - Math.max(0, vp.clientHeight / 2 - LH * 2);
  vp.scrollTop = Math.max(0, y);
}

// Registered with diff.js (setSourceJumpHandler): leaves diff view for the
// plain source view of the doc already open in the active tab, caret and
// scroll landing on the given working-tree line -- what a click on a diff
// line number, or the Source toggle, hands over.
export function jumpToSourceLine(line) {
  const d = doc_();
  if (!d || !line) return;
  setDiffMode('source');
  d.cur = Math.max(1, Math.min(d.total || line, line));
  centerLine(d.cur);
  render();
  updateStatus();
  pushHistory(d.path, d.cur);
}

export function closeTab(i) {
  clearSelectAll();
  const [closed] = S.tabs.splice(i, 1);
  if (closed) {
    if (closed.path) {
      // The active tab's scrollTop is only saved on switch, so read the live one.
      const scrollTop = i === S.active ? vp.scrollTop : closed.scrollTop;
      closedTabs.push({ path: closed.path, cur: closed.cur, scrollTop });
      if (closedTabs.length > MAX_CLOSED) closedTabs.shift();
      api('/api/close', { path: closed.path })
        .then(() => refreshMetrics())
        .catch(() => {});
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
    syncImageView();
    syncPreview();
    syncDiffView();
    rowsEl.innerHTML = ''; sizer.style.height = '0px';
    $('#empty').hidden = false; drawCrumbs();
    drawTabs(); updateStatus();
    saveWorkspaceState();
    emit('tab:closed', { doc: closed, index: i });
    emit('tabs:cleared');
    return;
  }
  if (i < S.active) {
    S.active--;
  } else if (i === S.active) {
    S.active = Math.min(i, S.tabs.length - 1);
  }
  const d = doc_();
  syncImageView();
  syncPreview();
  syncDiffView();
  drawTabs(); drawCrumbs(); layout();
  vp.scrollTop = d.scrollTop; render(); updateStatus();
  saveWorkspaceState();
  emit('tab:closed', { doc: closed, index: i });
  if (d) emit('tab:activated', { doc: d });
}

// Reopens the most recently closed file that is not open already, where it was left.
export async function reopenClosedTab() {
  while (closedTabs.length) {
    const t = closedTabs.pop();
    if (S.tabs.some(d => d.path === t.path)) continue;
    await openFile(t.path, { line: t.cur });
    if (doc_()?.path !== t.path) return;
    vp.scrollTop = t.scrollTop;
    render(); updateStatus();
    return;
  }
}

export function drawTabs() {
  $('#tabs').innerHTML = S.tabs.map((t, i) =>
    '<div class="tab' + (i === S.active ? ' active' : '') + (t.isImage ? ' tab-image' : '') + '" data-i="' + i + '" title="' + esc(t.path) + '">' +
    (t.isImage ? '<svg class="tab-icon" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.4"><rect x="2" y="2" width="12" height="12" rx="2"/><circle cx="5.5" cy="5.5" r="1.5"/><path d="M14 10l-3.5-3.5L3 14"/></svg>' : '') +
    '<span class="tn">' + esc(t.name) + '</span>' +
    '<span class="x" data-close="' + i + '" title="' + withKeys('Close tab ({Alt+W})') + '"><svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6"/></svg></span></div>').join('');
  const act = $('#tabs .tab.active');
  if (act) act.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

export function switchTab(i) {
  if (i === S.active || !S.tabs[i]) return;
  clearLink();
  const prev = doc_();
  if (prev) prev.scrollTop = vp.scrollTop;
  S.active = i;
  const curDoc = S.tabs[i];
  if (curDoc && curDoc.diffAvailable && (treeEl?.classList.contains('changed-only') || (!curDoc.diffDismissed && curDoc.diffMode === null))) {
    curDoc.diffMode = layoutPref() || 'split';
    curDoc.diffDismissed = false;
    curDoc.openedInDiffView = true;
  }
  syncImageView();
  syncPreview();
  syncDiffView();
  clearFind();
  clearSelectAll();
  S.at = null;
  S.lsp.state = (S.tabs[i].lsp && S.tabs[i].lsp.state) || 'off';
  S.lsp.server = (S.tabs[i].lsp && S.tabs[i].lsp.server) || '';
  S.lsp.missing = (S.tabs[i].lsp && S.tabs[i].lsp.missing) || '';
  warmLSP(S.tabs[i]);
  drawTabs(); drawCrumbs(); layout();
  vp.scrollTop = S.tabs[i].scrollTop;
  render(); updateStatus();
  if ($('#panel-outline')?.classList.contains('active')) loadOutline();
  pushHistory(S.tabs[i].path, S.tabs[i].cur);
  saveWorkspaceState();
  emit('tab:activated', { doc: S.tabs[i], prevDoc: prev });
}

let saveSessionTimer = null;
export function saveWorkspaceState() {
  if (saveSessionTimer) clearTimeout(saveSessionTimer);
  saveSessionTimer = setTimeout(async () => {
    try {
      const tabs = S.tabs.map(t => ({ path: t.path }));
      await apiPostJson('/api/session', { tabs, active: S.active });
    } catch {}
  }, 200);
}

export async function restoreWorkspaceTabs() {
  try {
    const session = await api('/api/session');
    if (!session || !Array.isArray(session.tabs) || session.tabs.length === 0) return false;
    for (const t of session.tabs) {
      if (t.path) await openFile(t.path, { push: false });
    }
    if (typeof session.active === 'number' && session.active >= 0 && session.active < S.tabs.length) {
      switchTab(session.active);
    }
    return true;
  } catch {
    return false;
  }
}

export function drawCrumbs() {
  const el = $('#crumbs');
  if (el) el.innerHTML = '';
}

export function showImage(path) {
  openFile(path);
}

export function hideImage() {
  const b = $('#imgview');
  if (b) b.hidden = true;
}

export function initTabs() {
  setSourceJumpHandler(jumpToSourceLine);
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
