// web/src/diff.js
// Git diff view for the active tab: renders the file's unified diff against
// HEAD in a dedicated overlay (like the Markdown preview), in either a
// side-by-side split layout (default) or a single-column unified layout.
// Unlike the code viewport this is not virtualized -- a file's own diff is
// bounded in size, so a plain DOM render is simple and fast enough.
import { $, S, doc_, esc, api } from './state.js';
import { syncPreview } from './markdown.js';
import { setStatusNote, updateStatus } from './status.js';

export const diffview = $('#diffview');
const diffContent = $('#diffcontent');

let shown = null; // doc the diff view is currently showing, null while hidden

// d.diffMode is 'split' | 'unified' | null (off), per tab. The layout last
// picked (split vs unified) is remembered globally as the default for the
// next file entering diff view.
export function setLayoutPref(mode) {
  try { localStorage.setItem('px0.diffLayout', mode); } catch {}
}

export function layoutPref() {
  try { return localStorage.getItem('px0.diffLayout') || 'split'; } catch { return 'split'; }
}

function diffMode(d = doc_()) {
  return (d && d.diffMode) || null;
}

/* Show or hide the diff overlay to match the active tab, and re-render when
   the layout (split/unified) changes while already showing the same doc --
   switching layout doesn't change which doc is "shown", so that alone can't
   be the signal to redraw. Call whenever either might have changed. */
export function syncDiffView(force = false) {
  const d = doc_();
  const want = (d && d.diffMode) ? d : null;
  if (force && want) {
    want.diffText = undefined;
    want.diffHunks = undefined;
    // The diff itself is being refetched, so any expansion of it is stale too.
    want.diffExpand = null;
    want.diffCtx = null;
    want.diffPending = null;
  }
  if (want !== shown || force) {
    shown = want;
    diffview.hidden = !want;
    if (want) drawDiff(want, force);
    else diffContent.replaceChildren();
  } else if (want && want.diffHunks !== undefined) {
    renderDiff(want);
  }
}

// Read by a reload, which swaps the doc and so redraws the diff from the top.
export function diffScrollTop() {
  return diffview.hidden ? 0 : diffview.scrollTop;
}

export async function toggleDiff() {
  if (!S.meta?.git) return;
  const d = doc_();
  if (!d) return;
  if (!d.diffMode && !d.diffAvailable) { setStatusNote('No diff — clean file or not a git repo', 4000); return; }
  setDiffMode(d.diffMode ? 'source' : (layoutPref() || 'split'));
}

export async function setDiffMode(mode) {
  const d = doc_();
  if (!d) return;
  if (mode !== 'source' && !d.diffAvailable) { setStatusNote('No diff — clean file or not a git repo', 4000); return; }
  if (mode === 'source') {
    d.diffMode = null;
    d.diffDismissed = true;
  } else {
    d.diffMode = mode;
    d.diffDismissed = false;
    d.openedInDiffView = true;
    setLayoutPref(mode);
  }
  syncPreview(); // markdown preview and diff view are mutually exclusive
  syncDiffView();
  updateStatus();
}

async function drawDiff(d, force = false) {
  if (force || d.diffText === undefined) {
    diffContent.replaceChildren();
    try {
      d.diffReq = api('/api/diff', { path: d.path });
      const j = await d.diffReq;
      d.diffText = j.diff || '';
      d.diffHunks = parseDiff(d.diffText);
    } catch (e) {
      d.diffText = '';
      d.diffHunks = [];
      setStatusNote('No diff: ' + e.message, 4000);
    } finally {
      d.diffReq = null;
    }
    if (shown !== d) return;
  }
  renderDiff(d);
  if (d.diffScroll) {
    diffview.scrollTop = d.diffScroll;
    d.diffScroll = 0;
  }
}

function renderDiff(d) {
  diffContent.replaceChildren();
  if (!d.diffHunks || !d.diffHunks.length) {
    const p = document.createElement('div');
    p.className = 'diff-empty';
    p.textContent = 'No changes against HEAD.';
    diffContent.append(p);
    return;
  }
  const frag = document.createDocumentFragment();
  /* Hunks are stitched together with the unchanged lines git left out between
     them: ranges the reader expanded render as context rows, each run still
     hidden collapses into an expand button. lastNew/lastOld track where each
     gap starts on both sides of the diff, so an expanded line can be stamped
     with the base line it corresponds to. */
  let lastNew = 0, lastOld = 0;
  d.diffHunks.forEach((hunk, i) => {
    frag.append(...gapElements(d, lastNew + 1, hunk.newStart - 1, lastOld, lastNew, i));
    frag.append(hunkHeader(hunk, i));
    frag.append(diffTable(d, hunk.rows));
    for (const row of hunk.rows) {
      if (row.newLine > lastNew) lastNew = row.newLine;
      if (row.oldLine > lastOld) lastOld = row.oldLine;
    }
  });
  frag.append(...gapElements(d, lastNew + 1, d.total || lastNew, lastOld, lastNew, -1));
  diffContent.append(frag);
  syncDiffAgentTargets();
  if (prSyncHandler) prSyncHandler();
}

/* One-way registration for pr.js, mirroring agent.js's hook into selbar.js:
   diff.js never imports pr.js, it just calls this after every repaint when a
   PR review session has set it. */
let prSyncHandler = null;
export function setPRSyncHandler(fn) { prSyncHandler = fn; }

export function syncDiffAgentTargets() {
  if (!diffview || diffview.hidden) return;
  const d = doc_();
  if (!d) return;
  const ranges = (S.agentTargets || []).filter(t => t.path === d.path);
  for (const el of diffview.querySelectorAll('[data-l]')) {
    const l = +el.dataset.l;
    const inAgent = ranges.some(r => l >= r.l1 && l <= r.l2);
    const isAnchor = ranges.some(r => l === r.l1);
    el.classList.toggle('agent-sel', inAgent);
    el.classList.toggle('agent-anchor', isAnchor);
  }
}

function hunkHeader(hunk, i) {
  const el = document.createElement('div');
  el.className = 'diff-hunk-head';
  el.dataset.hunk = i; // an expansion pins the viewport on the header below its gap
  el.textContent = '@@ -' + hunk.oldStart + ' +' + hunk.newStart + ' @@';
  return el;
}

/* ---------- unified diff parsing ---------- */

const HUNK_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@[ \t]?(.*)$/;

// Parses a unified diff (as returned by `git diff`) into hunks, each a flat
// list of rows tagged ctx/add/del carrying old- and/or new-file line numbers.
// File headers (diff --git, index, ---, +++) are skipped: nothing before the
// first @@ is kept.
function parseDiff(text) {
  if (!text) return [];
  const hunks = [];
  let cur = null, oldLine = 0, newLine = 0;
  for (const line of text.split('\n')) {
    const m = HUNK_RE.exec(line);
    if (m) {
      oldLine = +m[1];
      newLine = +m[3];
      cur = { oldStart: oldLine, newStart: newLine, section: m[5] || '', rows: [] };
      hunks.push(cur);
      continue;
    }
    if (!cur || line === '' || line.startsWith('\\')) continue; // trailing split artifact, pre-hunk header, or "\ No newline..."
    const c = line[0], body = line.slice(1);
    if (c === '+') cur.rows.push({ type: 'add', newLine: newLine++, text: body });
    // A deletion has no line on disk; at is the working-tree line it sat before.
    else if (c === '-') cur.rows.push({ type: 'del', oldLine: oldLine++, at: newLine, text: body });
    else cur.rows.push({ type: 'ctx', oldLine: oldLine++, newLine: newLine++, text: body });
  }
  return hunks;
}

/* ---------- both layouts render a flat list of rows the same way ---------- */

function diffTable(d, rows) {
  const table = document.createElement('div');
  table.className = 'diff-table ' + (d.diffMode === 'unified' ? 'diff-unified' : 'diff-split');
  if (d.diffMode === 'unified') {
    for (const row of rows) {
      const r = document.createElement('div');
      r.className = 'diff-row diff-' + row.type;
      anchor(r, row);
      r.append(
        lineCell(row.type === 'add' ? '' : row.oldLine),
        lineCell(row.type === 'del' ? '' : row.newLine),
        markerCell(row.type),
        codeCellFor(row),
      );
      table.append(r);
    }
  } else {
    for (const pair of pairRows(rows)) {
      const r = document.createElement('div');
      r.className = 'diff-row-pair';
      r.append(splitSide(pair.left, 'left'), splitSide(pair.right, 'right'));
      table.append(r);
    }
  }
  return table;
}

/* ---------- expandable context between hunks ---------- */

const EXPAND_STEP = 20; // lines a side chevron reveals per click
const EXPAND_MAX = 500; // ceiling on the middle chevron: the diff DOM is not virtualized

/* The lines git skipped between hunks (and before the first / after the last).
   Expanded ranges render as context rows; each run still hidden collapses into
   an expand button. Everything in a gap is unchanged by definition, so oldOf
   maps a new-file line onto the base line it corresponds to. */
function gapElements(d, g1, g2, lastOld, lastNew, nextHunkIdx) {
  if (g2 < g1) return [];
  const out = [];
  const oldOf = l => lastOld + (l - lastNew);
  let cur = g1;
  for (const r of d.diffExpand || []) {
    if (r.e < g1 || r.s > g2) continue;
    const s = Math.max(r.s, g1), e = Math.min(r.e, g2);
    if (s > cur) out.push(expandRow(d, cur, s - 1, nextHunkIdx));
    out.push(ctxTable(d, s, e, oldOf));
    cur = e + 1;
  }
  if (cur <= g2) out.push(expandRow(d, cur, g2, nextHunkIdx));
  return out;
}

/* Highlighted rows come from /api/file's cache on the doc, keyed by line. */
function ctxTable(d, s, e, oldOf) {
  const cache = d.diffCtx || (d.diffCtx = new Map());
  const rows = [];
  for (let l = s; l <= e; l++) {
    rows.push({ type: 'ctx', oldLine: oldOf(l), newLine: l, html: cache.get(l) || '&nbsp;' });
  }
  return diffTable(d, rows);
}

const chev = paths => '<svg viewBox="0 0 10 10" width="11" height="11" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">' + paths.map(p => '<path d="' + p + '"/>').join('') + '</svg>';
const SVG_UP = chev(['M2 6.5L5 3.5L8 6.5']);
const SVG_DOWN = chev(['M2 3.5L5 6.5L8 3.5']);
const SVG_BOTH = chev(['M2 4.75L5 1.75L8 4.75', 'M2 8.25L5 5.25L8 8.25']);

function expandRow(d, s, e, nextHunkIdx) {
  const el = document.createElement('div');
  el.className = 'diff-expand';
  if (intersectsPending(d, s, e)) el.classList.add('busy');
  const n = e - s + 1;
  const mk = (from, to, title, svg) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'diff-expand-btn';
    b.title = title;
    b.setAttribute('aria-label', title);
    b.innerHTML = svg;
    b.addEventListener('click', () => expandLines(d, from, to, el, nextHunkIdx));
    el.append(b);
  };
  const whole = 'Expand ' + n + (n === 1 ? ' line' : ' lines');
  /* The chevron points at where the new lines will appear, and the row always
     lands on the boundary of what is still hidden -- so it travels with the
     expansion instead of being left behind (GitHub's expandable rows). */
  if (nextHunkIdx === -1) {
    // After the last hunk only the file's tail is missing: reveal upward from
    // the last line, the row staying above the lines it just revealed.
    if (n <= EXPAND_STEP) mk(s, e, whole, SVG_UP);
    else mk(e - EXPAND_STEP + 1, e, 'Expand ' + EXPAND_STEP + ' lines', SVG_UP);
  } else if (nextHunkIdx === 0) {
    // Before the first hunk only the file's start is missing: reveal downward
    // from line 1, the row staying below the lines it just revealed.
    if (n <= EXPAND_STEP) mk(s, e, whole, SVG_DOWN);
    else mk(s, s + EXPAND_STEP - 1, 'Expand ' + EXPAND_STEP + ' lines', SVG_DOWN);
  } else if (n <= EXPAND_STEP) {
    mk(s, e, whole, SVG_BOTH);
  } else {
    mk(s, s + EXPAND_STEP - 1, 'Expand ' + EXPAND_STEP + ' lines above', SVG_UP);
    const all = Math.min(n, EXPAND_MAX);
    mk(s, s + all - 1, 'Expand up to ' + all + ' lines', SVG_BOTH);
    mk(e - EXPAND_STEP + 1, e, 'Expand ' + EXPAND_STEP + ' lines below', SVG_DOWN);
  }
  return el;
}

/* Fetches the range from /api/file (already syntax-highlighted), caches it on
   the doc, merges the range into the expansion state, and redraws. The hunk
   header below the gap is pinned on screen: every new row lands above it. */
async function expandLines(d, s, e, rowEl, nextHunkIdx) {
  if (s > e || shown !== d || intersectsPending(d, s, e)) return;
  const key = s + ':' + e;
  (d.diffPending || (d.diffPending = new Set())).add(key);
  /* Mid gaps only: the lines appear above the hunk below, so pin that header
     to keep the reader's place. At the file's top and tail the expansion grows
     right where the reader is looking -- compensating the scroll there would
     slide the fresh lines out of view instead of letting the row travel with
     them. */
  const pin = nextHunkIdx > 0;
  const anchorSel = pin ? '.diff-hunk-head[data-hunk="' + nextHunkIdx + '"]' : null;
  const anchor = anchorSel ? diffContent.querySelector(anchorSel) : null;
  const was = anchor ? anchor.offsetTop : 0;
  rowEl.classList.add('busy');
  try {
    const j = await api('/api/file', { path: d.path, start: s - 1, count: e - s + 1 });
    const cache = d.diffCtx || (d.diffCtx = new Map());
    for (let i = 0; i < (j.lines || []).length; i++) cache.set(s + i, j.lines[i]);
    mergeExpand(d, s, e);
  } catch (err) {
    setStatusNote('Expand failed: ' + err.message, 4000);
  } finally {
    d.diffPending.delete(key);
  }
  if (shown !== d) return;
  renderDiff(d);
  if (anchor) {
    const nowEl = diffContent.querySelector(anchorSel);
    if (nowEl) diffview.scrollTop += nowEl.offsetTop - was;
  }
}

function mergeExpand(d, s, e) {
  const list = d.diffExpand || (d.diffExpand = []);
  list.push({ s, e });
  list.sort((a, b) => a.s - b.s);
  const merged = [];
  for (const r of list) {
    const last = merged[merged.length - 1];
    if (last && r.s <= last.e + 1) last.e = Math.max(last.e, r.e);
    else merged.push({ s: r.s, e: r.e });
  }
  d.diffExpand = merged;
}

function intersectsPending(d, s, e) {
  for (const key of d.diffPending || []) {
    const i = key.indexOf(':');
    const ps = +key.slice(0, i), pe = +key.slice(i + 1);
    if (ps <= e && pe >= s) return true;
  }
  return false;
}

// Walks a hunk's flat row list, pairing each run of deletions with the run of
// additions that immediately follows it (a "changed" block) index-by-index,
// padding the shorter side with blanks. Context rows go straight across.
function pairRows(rows) {
  const pairs = [];
  let i = 0;
  while (i < rows.length) {
    const row = rows[i];
    if (row.type === 'ctx') { pairs.push({ left: row, right: row }); i++; continue; }
    let dels = [], adds = [];
    while (i < rows.length && rows[i].type === 'del') dels.push(rows[i++]);
    while (i < rows.length && rows[i].type === 'add') adds.push(rows[i++]);
    const n = Math.max(dels.length, adds.length);
    for (let k = 0; k < n; k++) pairs.push({ left: dels[k] || null, right: adds[k] || null });
  }
  return pairs;
}

function splitSide(row, side) {
  const el = document.createElement('div');
  el.className = 'diff-side diff-side-' + side + (row ? ' diff-' + row.type : ' diff-blank');
  if (!row) { el.append(lineCell(''), markerCell(''), codeCell('')); return el; }
  const ln = side === 'left' ? row.oldLine : row.newLine;
  anchor(el, row);
  el.append(lineCell(ln), markerCell(row.type), codeCellFor(row));
  return el;
}

/* Stamps where a row points in the working tree, so a selection on it can be
   edited. Context and added lines have a line on disk (data-l), which a context
   line shares across both sides of the split. A deleted line has none, only the
   place it used to be (data-at). */
function anchor(el, row) {
  if (row.newLine !== undefined) el.dataset.l = row.newLine;
  else if (row.at !== undefined) el.dataset.at = row.at;
  if (row.oldLine !== undefined) el.dataset.oldL = row.oldLine;
}

function lineCell(n) {
  const el = document.createElement('div');
  el.className = 'diff-ln';
  if (n !== '' && n !== undefined) {
    const btn = document.createElement('span');
    btn.className = 'line-btn';
    btn.setAttribute('role', 'button');
    btn.title = 'Comment or Edit';
    btn.textContent = '✎';
    el.append(btn);
  }
  el.append(document.createTextNode(n === '' || n === undefined ? '' : String(n)));
  return el;
}

const MARKS = { add: '+', del: '-', ctx: '' };

function markerCell(type) {
  const el = document.createElement('div');
  el.className = 'diff-mk';
  el.textContent = MARKS[type] || '';
  return el;
}

function codeCell(text) {
  const el = document.createElement('div');
  el.className = 'diff-code';
  el.innerHTML = esc(text || '') || '&nbsp;';
  return el;
}

/* An expanded context row carries highlighted HTML straight from /api/file,
   the same markup the source viewport renders, so it skips the escaping. */
function codeCellFor(row) {
  if (row.html === undefined) return codeCell(row.text);
  const el = document.createElement('div');
  el.className = 'diff-code';
  el.innerHTML = row.html || '&nbsp;';
  return el;
}

export function initDiff() {
  const sw = $('#diff-switch');
  if (!sw) return;
  sw.addEventListener('mousedown', e => {
    if (!e.target.closest('button')) e.preventDefault();
  });
  // Each half of the switch names a view, so a click shows that view rather than toggling.
  $('#diff-source')?.addEventListener('click', e => {
    e.stopPropagation();
    setDiffMode('source');
  });
  $('#diff-btn')?.addEventListener('click', e => {
    e.stopPropagation();
    const d = doc_();
    if (!d || !d.diffAvailable) return;
    setDiffMode(d.diffMode || layoutPref());
  });
  const menu = $('#diff-menu');
  if (menu) {
    menu.addEventListener('click', e => {
      const item = e.target.closest('[data-diff-opt]');
      if (!item) return;
      e.stopPropagation();
      setDiffMode(item.dataset.diffOpt);
      item.blur();
    });
  }
}
