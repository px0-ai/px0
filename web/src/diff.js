// web/src/diff.js
// Git diff view for the active tab: renders the file's diff against HEAD in a
// dedicated overlay (like the Markdown preview), in either a side-by-side
// split layout (default) or a single-column unified layout.
//
// The rows arrive from /api/diff already parsed, already syntax-highlighted
// and already carrying their intra-line word ranges -- see difftext.go for why
// that work belongs on the server. This module lays out what it is given; it
// no longer parses a unified diff, and it no longer escapes source text.
// Unlike the code viewport it is not virtualized: a file's own diff is bounded
// in size, so a plain DOM render is simple and fast enough.
import { $, S, doc_, api } from './state.js';
import { syncPreview } from './markdown.js';
import { setStatusNote, updateStatus } from './status.js';

export const diffview = $('#diffview');
const diffContent = $('#diffcontent');

let shown = null; // doc the diff view is currently showing, null while hidden

// d.diffMode is 'split' | 'unified' | null (off), per tab. The layout last
// picked (split vs unified) is remembered globally as the default for the
// next file entering diff view.
function setLayoutPref(mode) {
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
export function syncDiffView() {
  const d = doc_();
  const want = (d && d.diffMode) ? d : null;
  if (want !== shown) {
    shown = want;
    diffview.hidden = !want;
    if (want) drawDiff(want);
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
    setLayoutPref(mode);
  }
  syncPreview(); // markdown preview and diff view are mutually exclusive
  syncDiffView();
  updateStatus();
}

/* Fetches a file's hunks once per tab and caches them on the doc. Shared with
   review mode, which needs the same rows for a file it has not opened. */
export async function fileHunks(path) {
  const j = await api('/api/diff', { path });
  return j.hunks || [];
}

async function drawDiff(d) {
  if (d.diffHunks === undefined) {
    diffContent.replaceChildren();
    try {
      d.diffReq = d.diffReq || fileHunks(d.path);
      d.diffHunks = await d.diffReq;
    } catch (e) {
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
  diffContent.append(renderHunks(d.diffHunks, d.diffMode));
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
    el.classList.toggle('agent-sel', inAgent);
  }
}

/* ---------- render ---------- */

// Renders a list of hunks, each headed by its @@ line. Returns a fragment so
// the caller decides where it lands; review mode renders a subset of one
// file's hunks through the very same path.
export function renderHunks(hunks, mode) {
  const frag = document.createDocumentFragment();
  for (const hunk of hunks) {
    frag.append(hunkHeader(hunk));
    // A deletion has no line on disk; at is the working-tree line it sat before,
    // which is the new-side line the walk has reached when the deletion comes up.
    let next = hunk.newStart;
    for (const row of hunk.rows) {
      if (row.new !== undefined) next = row.new + 1;
      else if (row.type === 'del') row.at = next;
    }
    frag.append(renderRows(hunk.rows, mode));
  }
  return frag;
}

export function hunkHeader(hunk) {
  const el = document.createElement('div');
  el.className = 'diff-hunk-head';
  el.textContent = '@@ -' + hunk.oldStart + ' +' + hunk.newStart + ' @@';
  return el;
}

// Renders one hunk's rows as a table: 'unified' puts every row on its own
// line, anything else pairs deletions with the additions that replaced them.
export function renderRows(rows, mode) {
  return mode === 'unified' ? unifiedTable(rows) : splitTable(rows);
}

/* ---------- unified layout: one row per diff line ---------- */

function unifiedTable(rows) {
  const table = document.createElement('div');
  table.className = 'diff-table diff-unified';
  for (const row of rows) {
    const r = document.createElement('div');
    r.className = 'diff-row diff-' + row.type;
    anchor(r, row);
    r.append(lineCell(row.old), lineCell(row.new), markerCell(row.type), codeCell(row));
    table.append(r);
  }
  return table;
}

/* ---------- split layout: deletions and additions paired side by side ---------- */

function splitTable(rows) {
  const table = document.createElement('div');
  table.className = 'diff-table diff-split';
  for (const pair of pairRows(rows)) {
    const r = document.createElement('div');
    r.className = 'diff-row-pair';
    r.append(splitSide(pair.left, 'left'), splitSide(pair.right, 'right'));
    table.append(r);
  }
  return table;
}

// Walks a hunk's flat row list, pairing each run of deletions with the run of
// additions that immediately follows it (a "changed" block) index-by-index,
// padding the shorter side with blanks. Context rows go straight across. This
// is the same pairing the server used to compute the word ranges, so a pair
// here is a pair there.
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
  if (!row) { el.append(lineCell(), markerCell(''), codeCell(null)); return el; }
  anchor(el, row);
  el.append(lineCell(side === 'left' ? row.old : row.new), markerCell(row.type), codeCell(row));
  return el;
}

/* Stamps where a row points in the working tree, so a selection on it can be
   edited. Context and added lines have a line on disk (data-l), which a context
   line shares across both sides of the split. A deleted line has none, only the
   place it used to be (data-at), which renderHunks derives. */
function anchor(el, row) {
  if (row.new !== undefined) el.dataset.l = row.new;
  else if (row.at !== undefined) el.dataset.at = row.at;
}

// A row carries "old" only on the sides where the line exists (the server
// omits the other), so an absent number is blank, not zero.
function lineCell(n) {
  const el = document.createElement('div');
  el.className = 'diff-ln';
  el.textContent = n ? String(n) : '';
  return el;
}

const MARKS = { add: '+', del: '−', ctx: '' };

function markerCell(type) {
  const el = document.createElement('div');
  el.className = 'diff-mk';
  el.textContent = MARKS[type] || '';
  return el;
}

function codeCell(row) {
  const el = document.createElement('div');
  el.className = 'diff-code';
  el.innerHTML = (row && markWords(row.html, row.words)) || '&nbsp;';
  return el;
}

/* ---------- intra-line word ranges ---------- */

/* The server sends row.words as half-open [start, end) ranges over the PLAIN
   text of the line, in UTF-16 code units -- JavaScript string offsets. The
   HTML is a flat, never-nested sequence of text and <i class=xx> spans escaped
   with &amp; &lt; &gt; and nothing else, which is exactly what makes slicing it
   at plain-text offsets mechanical: walk it, count one per plain character,
   count zero for a tag.

   A mark is closed before every tag and reopened after it, so the emphasis
   never straddles a token boundary and the result stays well-formed however a
   range lines up with the highlighter's spans. */
function markWords(html, words) {
  if (!html || !words || !words.length) return html || '';
  let out = '', pos = 0, wi = 0, on = false;
  const close = () => { if (on) { out += '</em>'; on = false; } };
  const open = () => { if (!on) { out += '<em class="diff-w">'; on = true; } };
  const marked = i => {
    while (wi < words.length && words[wi][1] <= i) wi++;
    return wi < words.length && words[wi][0] <= i;
  };
  for (const [text, width] of atoms(html)) {
    if (!width) { close(); out += text; continue; } // a tag occupies no plain offset
    if (marked(pos)) open(); else close();
    out += text;
    pos += width;
  }
  close();
  return out;
}

const PART = /<[^>]*>|&(?:amp|lt|gt);|[^<&]+/g;

// Splits tokenised HTML into [text, plainWidth] pairs: a tag is width 0, an
// entity is the one character it stands for, and a plain run is split per code
// unit -- with surrogate pairs kept whole, since splitting one would emit an
// unpaired half.
function* atoms(html) {
  for (const m of html.matchAll(PART)) {
    const s = m[0];
    if (s[0] === '<') { yield [s, 0]; continue; }
    if (s[0] === '&') { yield [s, 1]; continue; }
    for (let i = 0; i < s.length; i++) {
      const c = s.charCodeAt(i);
      const pair = c >= 0xd800 && c < 0xdc00 && i + 1 < s.length;
      yield pair ? [s.slice(i, i + 2), 2] : [s[i], 1];
      if (pair) i++;
    }
  }
}

// The plain text behind a tokenised line, for the callers that need an offset
// into the source rather than into the markup.
export function plainText(html) {
  let out = '';
  for (const [text, width] of atoms(html || '')) if (width) out += ENTITY[text] || text;
  return out;
}

const ENTITY = { '&amp;': '&', '&lt;': '<', '&gt;': '>' };

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
    setDiffMode(doc_()?.diffMode || layoutPref());
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
