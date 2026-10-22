// web/src/tabs.js
import { $, esc, S, doc_, api, apiPost, LH, CHUNK, withKeys } from './state.js';
import { emit } from './bus.js';
import { releaseDocFolds } from './folding-ui.js';
import { vp, sizer, rowsEl, editor } from './ui.js';
import { render, layout, refineChunk, getLH, lineTopY, lineAtScrollY, revealSourceLine, invalidatePaint } from './renderer.js';
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
import { syncDiffView, layoutPref, diffScrollTop, setDiffMode, setSourceJumpHandler, setCommitStepHandler, scrollDiffToLine } from './diff.js';
import { syncImageView } from './imageview.js';
import { syncEditUI, editTextarea, updateGutter, centerEditLine } from './editview.js';

// Recently closed files, newest last, for Alt+Shift+T.
const closedTabs = [];
const MAX_CLOSED = 20;
let tabMenu = null;
let tabMenuIndex = -1;

function closeTabMenu() {
  if (tabMenu) tabMenu.hidden = true;
  tabMenuIndex = -1;
}

function closeTabsForAction(action, index) {
  if (!S.tabs[index]) return;
  if (action === 'others') switchTab(index);
  const targets = S.tabs.map((_, i) => i).filter(i => {
    if (action === 'all') return true;
    if (action === 'close') return i === index;
    if (action === 'others') return i !== index;
    if (action === 'right') return i > index;
    return i < index;
  });
  closeTabs(targets);
}

function openTabMenu(index, x, y) {
  const actions = [
    { action: 'close', label: 'Close', disabled: false },
    { action: 'all', label: 'Close All', disabled: false },
    { action: 'others', label: 'Close Others', disabled: S.tabs.length < 2 },
    { action: 'right', label: 'Close to the Right', disabled: index === S.tabs.length - 1 },
    { action: 'left', label: 'Close to the Left', disabled: index === 0 },
  ];
  tabMenu.replaceChildren();
  for (const { action, label, disabled } of actions) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'sel-menu-item';
    button.dataset.tabAction = action;
    button.setAttribute('role', 'menuitem');
    button.textContent = label;
    button.disabled = disabled;
    tabMenu.append(button);
  }
  tabMenuIndex = index;
  tabMenu.hidden = false;
  const w = tabMenu.offsetWidth, h = tabMenu.offsetHeight;
  tabMenu.style.left = Math.max(4, Math.min(x, innerWidth - w - 4)) + 'px';
  tabMenu.style.top = Math.max(4, Math.min(y, innerHeight - h - 4)) + 'px';
}

/* opts.ref pins the tab to one commit: every diff it shows is that commit's
   own change to the file, frozen no matter what happens in the working tree
   afterwards. The Unpushed sidebar section is what passes it. Opening the same
   path from anywhere else (tree, search, go-to-definition) passes no ref and so
   puts the tab back on the working tree, which is what "open this file" means
   everywhere else in px0. */
export async function openFile(path, opts = {}) {
  const { line, push = true, col, view, soft = false, ref = '', base = '', turnFiles = null, turnLabel = '' } = opts;
  const prev = doc_();
  const wantsDiff = view === 'diff';
  const wantsSource = view === 'source';

  let idx = S.tabs.findIndex(t => t.path === path);
  if (idx >= 0 && ((S.tabs[idx].diffRef || '') !== ref || (S.tabs[idx].diffBase || '') !== base)) {
    /* Same tab, different revision. Drop the cached diff and let loadGutter
       re-decide: the file may have a diff at the commit and none in the
       working tree, or the other way round, so neither diffAvailable nor
       diffMode survives the switch. */
    const t = S.tabs[idx];
    t.diffRef = ref;
    t.diffBase = base;
    t.turnFiles = turnFiles;
    t.turnLabel = turnLabel;
    t.diffText = undefined;
    t.diffHunks = undefined;
    t.diffDismissed = false;
    t.diffMode = null;
    t.openedInDiffView = true;
    await loadGutter(t);
    if (base) t.diffAvailable = true;
  } else if (idx >= 0 && (turnFiles !== null || turnLabel !== '')) {
    S.tabs[idx].turnFiles = turnFiles;
    S.tabs[idx].turnLabel = turnLabel;
  }
  if (idx < 0) {
    let j;
    const start = line ? Math.max(0, Math.floor((line - 1) / CHUNK) * CHUNK) : 0;
    try {
      j = await api('/api/file', { path, start, count: CHUNK, ref });
    } catch (e) {
      setStatusNote(path + ': ' + e.message, 4000);
      return;
    }
    const isImg = !!j.image;
    const hasDiff = !isImg && (!!j.diffAvailable || !!base);

    let initialDiffMode = null;
    let initialOpenedInDiff = false;
    let initialDismissed = false;

    if (hasDiff) {
      if (wantsSource && !j.deleted) {
        initialDiffMode = null;
        initialDismissed = true;
        initialOpenedInDiff = false;
      } else {
        // Any file with a diff opens in diff mode unless source was asked for.
        initialDiffMode = layoutPref() || 'split';
        initialDismissed = false;
        initialOpenedInDiff = true;
      }
    }

    const d = {
      path, name: path.split('/').pop(), lang: isImg ? 'image' : j.lang,
      total: isImg ? 0 : j.total, maxCols: isImg ? 0 : j.maxCols,
      version: j.version,
      size: j.size, lines: isImg ? [] : new Array(j.total),
      chunks: new Set(isImg ? [] : [start / CHUNK]),
      pending: new Set(), refining: new Set(), scrollTop: 0, cur: line || 1,
      outline: null, gen: 0, markdown: !isImg && !!j.markdown, table: !isImg && !!j.table, isImage: isImg,
      deleted: !isImg && !!j.deleted,
      gutter: null,
      diagnostics: (!isImg && j.diagnostics) || [],
      diffMode: initialDiffMode,
      diffAvailable: hasDiff,
      diffDismissed: initialDismissed,
      openedInDiffView: initialOpenedInDiff,
      diffRef: ref,
      diffBase: base,
      turnFiles,
      turnLabel,
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
  if (prev && prev !== S.tabs[idx]) prev.scrollTop = vp.scrollTop;
  if (prev !== S.tabs[idx]) { clearSelectAll(); clearFind(); }
  S.active = idx;
  const d = S.tabs[idx];
  if (d) {
    if (wantsSource) {
      d.diffMode = null;
      d.diffDismissed = true;
      d.openedInDiffView = false;
    } else if (wantsDiff) {
      if (d.diffAvailable) {
        d.diffMode = layoutPref() || 'split';
        d.diffDismissed = false;
        d.openedInDiffView = true;
      } else {
        d.diffMode = null;
        if (!soft) setStatusNote('No diff for ' + d.name + ' — showing source', 3000);
      }
    } else if (d.diffAvailable && !d.diffDismissed && d.diffMode === null) {
      d.diffMode = layoutPref() || 'split';
      d.openedInDiffView = true;
    }
  }

  // A diff was asked for on a file that is open for editing: show the diff.
  // The unsaved text stays in editContent for when editing resumes.
  if (d && d.isEditing && d.diffMode && wantsDiff) {
    d.isEditing = false;
    document.body.classList.remove('editing-file');
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

  syncEditUI();
  if (line) {
    d.cur = line;
    if (d.diffMode) scrollDiffToLine(line);
    else centerLine(line);
  } else if (!d.diffMode && !d.isEditing) {
    vp.scrollTop = d.scrollTop;
  }
  render();
  updateStatus();
  if ($('#panel-outline')?.classList.contains('active')) loadOutline();
  if (push) pushHistory(path, line || d.cur);
  emit('tab:activated', { doc: d, prevDoc: prev });
}

// VS Code-style diff gutter for the normal file view. Fetches once per opened
// doc and caches on it (each tab keeps its own; switching tabs needs no clear).
// Fetches on any open in a git repo rather than threading per-file status
// through every open path — the backend returns available:false for
// clean/untracked files, so the extra request is cheap and self-limiting.
export async function loadGutter(d) {
  if (!S.meta?.git) return;
  // Same stale-answer guard as drawDiff: the doc may be pointed at a different
  // commit (or back at the working tree) before this resolves.
  const ref = d.diffRef || '';
  try {
    const j = await api('/api/gutter', { path: d.path, ref });
    if ((d.diffRef || '') !== ref) return;
    d.diffAvailable = !!j.available;
    if (j.available && d.diffMode === null && !d.diffDismissed && d.openedInDiffView) {
      d.diffMode = layoutPref() || 'split';
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
      if (mv && !mv.hidden) activeDoc.mdScroll = mv.scrollTop;
    }
  }

  const targets = S.tabs.map(t => ({
    oldDoc: t,
    path: t.path,
    anchor: t.cur || 1,
    start: t.cur ? Math.max(0, Math.floor((t.cur - 1) / CHUNK) * CHUNK) : 0,
    ref: t.diffRef || '',
  }));

  let anyChanged = false;
  const results = await Promise.allSettled(
    targets.map(tgt => api('/api/file', { path: tgt.path, start: tgt.start, count: CHUNK, ref: tgt.ref }))
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
    const hasDiff = !!j.diffAvailable || !!keep.diffBase;
    let linesSame = true;
    if (keep.lines && j.lines) {
      for (let k = 0; k < j.lines.length; k++) {
        if (keep.lines[tgt.start + k] !== j.lines[k]) { linesSame = false; break; }
      }
    }
    const diagCount = (j.diagnostics || []).length;
    const keepDiagCount = (keep.diagnostics || []).length;
    if (onlyIfChanged && linesSame && diagCount === keepDiagCount &&
        keep.version === j.version && keep.size === j.size && keep.total === j.total &&
        !!keep.diffAvailable === hasDiff && !!keep.deleted === !!j.deleted) continue;
    anyChanged = true;
    const newCur = Math.max(1, Math.min(keep.cur || 1, j.total || 1));

    /* A reload keeps each tab in the view it was in. The file changing under
       it, say from an agent edit, is no reason to swap source for a diff, so a
       tab in source is marked dismissed and loadGutter leaves it there too. */
    const diffMode = hasDiff ? (keep.diffMode || (j.deleted ? (layoutPref() || 'split') : null)) : null;

    const d = {
      path: tgt.path,
      name: tgt.path.split('/').pop(),
      lang: j.lang,
      total: j.total,
      maxCols: j.maxCols,
      size: j.size,
      version: j.version,
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
      table: !!j.table,
      deleted: !!j.deleted,
      mdScroll: keep.mdScroll || 0,
      mdLine: keep.mdLine || 0,
      gutter: null,
      diagnostics: j.diagnostics || [],
      diffMode,
      diffAvailable: hasDiff,
      diffDismissed: !!keep.diffDismissed || (!keep.diffMode && !j.deleted),
      openedInDiffView: !!keep.openedInDiffView || !!keep.diffMode || !!j.deleted,
      diffScroll: keep === activeDoc && keep.diffMode ? diffScrollTop() : 0,
      prCollapsed: keep.prCollapsed,
      youCollapsed: keep.youCollapsed,
      diffRef: tgt.ref,
      diffBase: keep.diffBase || '',
      turnFiles: keep.turnFiles || null,
      turnLabel: keep.turnLabel || '',
      isEditing: !!keep.isEditing,
      dirty: !!keep.dirty,
      editContent: keep.editContent,
    };

    for (let k = 0; k < j.lines.length; k++) {
      d.lines[j.start + k] = j.lines[k];
    }
    d.lsp = j.lsp || { state: 'off', server: '' };

    const topLine = keep === activeDoc ? lineAtScrollY(keep.scrollTop, keep) : 0;
    if (keep.version === j.version) {
      d.folds = keep.folds;
      if (d.folds) { d.folds.pixelOffsets = null; d.folds.pixelVersion = -1; }
      d.foldLoaded = keep.foldLoaded; d.foldLimited = keep.foldLimited;
      d.foldError = keep.foldError;
    }
    S.tabs[idx] = d;
    releaseDocFolds(keep);
    keep.reqGen = (keep.reqGen || 0) + 1;
    keep.lines = null; keep._wrap = null; keep._lineCols = null;
    invalidatePaint();
    // Preserve a source anchor when edits invalidate the old fold geometry.
    if (topLine && keep.version !== j.version) d.scrollTop = lineTopY(Math.min(topLine, d.total), d);
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
  if (d) emit('tab:activated', { doc: d });
}

export function centerLine(n) {
  const d = doc_();
  if (d?.isEditing) {
    centerEditLine(n);
    return;
  }
  if (previewing()) { previewLine(n); return; }
  revealSourceLine(n, d);
  const lh = getLH();
  const y = lineTopY(n) - Math.max(0, vp.clientHeight / 2 - lh * 2);
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
  if (!S.tabs[i]) return;
  closeTabs([i]);
}

export function closeAllTabs() {
  if (!S.tabs.length) return;
  closeTabs(S.tabs.map((_, idx) => idx));
}

function closeTabs(indices) {
  if (!indices.length) return;
  const dirtyTabs = indices.map(i => S.tabs[i]).filter(t => t?.dirty);
  if (dirtyTabs.length > 0) {
    const names = dirtyTabs.map(t => t.name).join(', ');
    if (!confirm(`You have unsaved changes in ${names}. Close anyway and discard changes?`)) {
      return;
    }
  }
  // Descending indices stay valid as tabs are removed.
  indices.sort((a, b) => b - a);
  closeTabMenu();
  clearSelectAll();
  const activeDoc = doc_();
  if (activeDoc) activeDoc.scrollTop = vp.scrollTop;
  const closedEvents = [];
  const evictions = [];
  for (const i of indices) {
    const [closed] = S.tabs.splice(i, 1);
    if (!closed) continue;
    if (closed.path) {
      closedTabs.push({ path: closed.path, cur: closed.cur, scrollTop: closed.scrollTop });
      if (closedTabs.length > MAX_CLOSED) closedTabs.shift();
      evictions.push(api('/api/close', { path: closed.path }));
    }
    // Release large arrays to assist garbage collection
    releaseDocFolds(closed);
    closed.reqGen = (closed.reqGen || 0) + 1;
    closed.lines = null;
    closed._wrap = null;
    closed._lineCols = null;
    closed._colsChunks?.clear?.();
    closed.chunks?.clear?.();
    closed.pending?.clear?.();
    closed.refining?.clear?.();
    closed.outline = null;
    if (i < S.active) {
      S.active--;
    } else if (i === S.active) {
      S.active = Math.min(i, S.tabs.length - 1);
    }
    closedEvents.push({ doc: closed, index: i });
  }
  Promise.allSettled(evictions).then(results => {
    if (results.some(result => result.status === 'fulfilled')) refreshMetrics();
  });
  if (S.tabs.length === 0) {
    S.active = -1;
    syncImageView();
    syncPreview();
    syncDiffView();
    syncEditUI();
    rowsEl.innerHTML = ''; sizer.style.height = '0px';
    $('#empty').hidden = false; drawCrumbs();
    drawTabs(); updateStatus();
    for (const event of closedEvents) emit('tab:closed', event);
    emit('tabs:cleared');
    return;
  }
  const d = doc_();
  syncImageView();
  syncPreview();
  syncDiffView();
  syncEditUI();
  if (d && d.isEditing && editTextarea) {
    editTextarea.value = d.editContent || '';
    updateGutter();
  }
  drawTabs(); drawCrumbs(); layout();
  vp.scrollTop = d.scrollTop; render(); updateStatus();
  for (const event of closedEvents) emit('tab:closed', event);
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

// Registered with diff.js: the commit strip's ‹ › open the next file of the
// commit and close the one stepped away from, so stepping swaps a tab in place
// rather than leaving one open per file.
async function stepCommitFile(from, file) {
  const ref = from.diffRef || '';
  const base = from.diffBase || '';
  const turnFiles = from.turnFiles || null;
  const turnLabel = from.turnLabel || '';
  await openFile(file.path, { ref, base, turnFiles, turnLabel, view: 'diff' });
  const i = S.tabs.indexOf(from);
  if (i >= 0 && from !== doc_() && (from.diffRef || '') === ref) closeTab(i);
}

export function drawTabs() {
  $('#tabs').innerHTML = S.tabs.map((t, i) =>
    '<div class="tab' + (i === S.active ? ' active' : '') + (t.isImage ? ' tab-image' : '') + (t.deleted ? ' tab-deleted' : '') + (t.dirty ? ' tab-dirty' : '') + '" data-i="' + i +
    '" title="' + esc(t.path) + (t.diffRef ? ' — in commit ' + esc(t.diffRef.slice(0, 7)) : '') + '">' +
    (t.isImage ? '<svg class="tab-icon" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.4"><rect x="2" y="2" width="12" height="12" rx="2"/><circle cx="5.5" cy="5.5" r="1.5"/><path d="M14 10l-3.5-3.5L3 14"/></svg>' : '') +
    '<span class="tn">' + esc(t.name) + '</span>' +
    // Pinned to a commit: the tab is not the working tree, and it should say so.
    (t.diffRef ? '<span class="tab-ref">@' + esc(t.diffRef.slice(0, 7)) + '</span>' : '') +
    (t.dirty ? '<span class="tab-dirty-dot" title="Unsaved changes">●</span>' : '') +
    '<span class="x" data-close="' + i + '" title="' + withKeys('Close tab ({Alt+W})') + '"><svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6"/></svg></span></div>').join('');
  const act = $('#tabs .tab.active');
  if (act) act.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

export function switchTab(i) {
  if (i === S.active || !S.tabs[i]) return;
  clearLink();
  const prev = doc_();
  if (prev) {
    prev.scrollTop = vp.scrollTop;
    if (prev.isEditing && editTextarea) {
      prev.editContent = editTextarea.value;
    }
  }
  S.active = i;
  const curDoc = S.tabs[i];
  syncImageView();
  syncPreview();
  syncDiffView();
  syncEditUI();
  if (curDoc.isEditing && editTextarea) {
    editTextarea.value = curDoc.editContent || '';
    updateGutter();
  }
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
  emit('tab:activated', { doc: S.tabs[i], prevDoc: prev });
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
  setCommitStepHandler(stepCommitFile);
  tabMenu = document.createElement('div');
  tabMenu.id = 'tab-menu';
  tabMenu.setAttribute('role', 'menu');
  tabMenu.hidden = true;
  document.body.append(tabMenu);
  tabMenu.addEventListener('click', e => {
    const button = e.target.closest('[data-tab-action]');
    if (!button || button.disabled) return;
    const index = tabMenuIndex;
    closeTabMenu();
    closeTabsForAction(button.dataset.tabAction, index);
  });
  $('#tabs').addEventListener('click', e => {
    closeTabMenu();
    const x = e.target.closest('[data-close]');
    if (x) { closeTab(+x.dataset.close); return; }
    const t = e.target.closest('.tab');
    if (t) switchTab(+t.dataset.i);
  });
  $('#tabs').addEventListener('auxclick', e => {
    const t = e.target.closest('.tab');
    if (t && e.button === 1) { e.preventDefault(); closeTab(+t.dataset.i); }
  });
  $('#tabs').addEventListener('contextmenu', e => {
    const tab = e.target.closest('.tab');
    if (!tab) { closeTabMenu(); return; }
    e.preventDefault();
    openTabMenu(+tab.dataset.i, e.clientX, e.clientY);
  });
  document.addEventListener('mousedown', e => {
    if (tabMenu && !tabMenu.hidden && !tabMenu.contains(e.target)) closeTabMenu();
  }, true);
  addEventListener('keydown', e => { if (e.key === 'Escape') closeTabMenu(); });
  addEventListener('resize', closeTabMenu);
  addEventListener('blur', closeTabMenu);
  document.addEventListener('scroll', closeTabMenu, true);
  const crumbsEl = $('#crumbs');
  if (crumbsEl) {
    crumbsEl.addEventListener('click', e => {
      const c = e.target.closest('[data-dir]');
      if (c) { showPanel('files'); revealDir(c.dataset.dir); }
    });
  }
  window.addEventListener('beforeunload', e => {
    if (S.tabs.some(t => t.dirty)) {
      e.preventDefault();
      e.returnValue = '';
    }
  });
}
