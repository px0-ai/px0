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
  for (const hunk of d.diffHunks) {
    frag.append(hunkHeader(hunk));
    frag.append(d.diffMode === 'unified' ? unifiedTable(hunk) : splitTable(hunk));
  }
  diffContent.append(frag);
  syncDiffAgentTargets();
}

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

function hunkHeader(hunk) {
  const el = document.createElement('div');
  el.className = 'diff-hunk-head';
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

/* ---------- unified layout: one row per diff line ---------- */

function unifiedTable(hunk) {
  const table = document.createElement('div');
  table.className = 'diff-table diff-unified';
  for (const row of hunk.rows) {
    const r = document.createElement('div');
    r.className = 'diff-row diff-' + row.type;
    anchor(r, row);
    r.append(
      lineCell(row.type === 'add' ? '' : row.oldLine),
      lineCell(row.type === 'del' ? '' : row.newLine),
      markerCell(row.type),
      codeCell(row.text),
    );
    table.append(r);
  }
  return table;
}

/* ---------- split layout: deletions and additions paired side by side ---------- */

function splitTable(hunk) {
  const table = document.createElement('div');
  table.className = 'diff-table diff-split';
  for (const pair of pairRows(hunk.rows)) {
    const r = document.createElement('div');
    r.className = 'diff-row-pair';
    r.append(splitSide(pair.left, 'left'), splitSide(pair.right, 'right'));
    table.append(r);
  }
  return table;
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
  el.append(lineCell(ln), markerCell(row.type), codeCell(row.text));
  return el;
}

/* Stamps where a row points in the working tree, so a selection on it can be
   edited. Context and added lines have a line on disk (data-l), which a context
   line shares across both sides of the split. A deleted line has none, only the
   place it used to be (data-at). */
function anchor(el, row) {
  if (row.newLine !== undefined) el.dataset.l = row.newLine;
  else if (row.at !== undefined) el.dataset.at = row.at;
}

function lineCell(n) {
  const el = document.createElement('div');
  el.className = 'diff-ln';
  el.textContent = n === '' || n === undefined ? '' : String(n);
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
