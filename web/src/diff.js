// web/src/diff.js
// Git diff view for the active tab: renders the file's unified diff against
// HEAD in a dedicated overlay (like the Markdown preview), in either a
// side-by-side split layout (default) or a single-column unified layout.
// Unlike the code viewport this is not virtualized -- a file's own diff is
// bounded in size, so a plain DOM render is simple and fast enough.
import { $, S, doc_, esc, api } from './state.js';
import { on } from './bus.js';
import { syncPreview } from './markdown.js';
import { fmtBytes, setStatusNote, updateStatus } from './status.js';

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
    else { diffContent.replaceChildren(); if (prSyncHandler) prSyncHandler(); }
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
      d.diffHunks = parseDiff(d.diffText, j.diffLines);
      d.diffHighlightSkipped = !!j.diffHighlightSkipped;
      d.diffHighlightLimitKB = j.diffHighlightLimitKB;
      d.diffHighlightBytes = j.diffHighlightBytes;
      // In a PR review session the server also splits the diff at the PR's
      // checked-out head commit: prDiff is the PR's own change (frozen since
      // checkout/last Pull), yourDiff is whatever the reviewer has edited or
      // committed locally since then. Undefined outside PR mode.
      d.prDiffHunks = j.prDiff !== undefined ? parseDiff(j.prDiff, j.prDiffLines) : undefined;
      d.yourDiffHunks = j.yourDiff !== undefined ? parseDiff(j.yourDiff, j.yourDiffLines) : undefined;
    } catch (e) {
      d.diffText = '';
      d.diffHunks = [];
      d.prDiffHunks = undefined;
      d.yourDiffHunks = undefined;
      d.diffHighlightSkipped = false;
      d.diffHighlightLimitKB = undefined;
      d.diffHighlightBytes = undefined;
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

function appendHunks(frag, hunks, mode, reviewable) {
  for (const hunk of hunks) {
    frag.append(hunkHeader(hunk));
    frag.append(mode === 'unified' ? unifiedTable(hunk, reviewable) : splitTable(hunk, reviewable));
  }
}

function renderDiff(d) {
  diffContent.replaceChildren();
  const frag = document.createDocumentFragment();
  if (d.diffHighlightSkipped) frag.append(highlightSkippedNote(d.diffHighlightLimitKB, d.diffHighlightBytes));
  if (S.meta?.pr && d.prDiffHunks !== undefined) {
    const prHunks = d.prDiffHunks || [];
    const yourHunks = d.yourDiffHunks || [];
    if (!prHunks.length && !yourHunks.length) {
      const p = document.createElement('div');
      p.className = 'diff-empty';
      p.textContent = 'No changes.';
      diffContent.append(p);
      return;
    }
    frag.append(createDiffSection(d, 'pr', 'PR changes', 'from ' + (S.meta.pr.base || 'base'), (body) => {
      if (!prHunks.length) body.append(sectionNote('The PR itself makes no change to this file.'));
      else appendHunks(body, prHunks, d.diffMode, true);
    }));

    const since = S.meta.pr.headSHA ? 'since ' + S.meta.pr.headSHA.slice(0, 7) : 'since checkout';
    frag.append(createDiffSection(d, 'you', 'Your changes', since, (body) => {
      if (!yourHunks.length) body.append(sectionNote('Nothing edited or committed yet — changes you make will show up here.'));
      else appendHunks(body, yourHunks, d.diffMode, false);
    }));
  } else {
    if (!d.diffHunks || !d.diffHunks.length) {
      const p = document.createElement('div');
      p.className = 'diff-empty';
      p.textContent = 'No changes against HEAD.';
      diffContent.append(p);
      return;
    }
    appendHunks(frag, d.diffHunks, d.diffMode, true);
  }
  diffContent.append(frag);
  syncDiffAgentTargets();
  if (prSyncHandler) prSyncHandler();
}

function highlightSkippedNote(limitKB, totalBytes) {
  const el = document.createElement('div');
  el.className = 'diff-highlight-note';
  el.textContent = limitKB === 0
    ? 'Syntax highlighting is disabled for diffs. Configure Diff Tokenization Limit in Settings.'
    : `Syntax highlighting skipped: ${fmtBytes(totalBytes || 0)} combined diff exceeds the ${fmtBytes(limitKB * 1024)} limit. Configure Diff Tokenization Limit in Settings.`;
  return el;
}

function createDiffSection(d, kind, title, sub, populateBody) {
  const sec = document.createElement('div');
  sec.className = 'diff-section diff-section-' + kind;
  const collapsedKey = kind + 'Collapsed';
  if (d[collapsedKey]) {
    sec.classList.add('collapsed');
  }

  const head = document.createElement('div');
  head.className = 'diff-section-head diff-section-head-' + kind;
  head.title = 'Click to collapse/expand section';

  const t = document.createElement('span');
  t.className = 'diff-section-title';
  t.textContent = title;
  head.append(t);

  if (sub) {
    const s = document.createElement('span');
    s.className = 'diff-section-sub';
    s.textContent = sub;
    head.append(s);
  }

  const grow = document.createElement('span');
  grow.className = 'grow';
  head.append(grow);

  const btn = document.createElement('button');
  btn.className = 'pr-comments-collapse diff-section-collapse';
  btn.title = 'Collapse/Expand';
  btn.innerHTML = '&#9662;';
  head.append(btn);

  const body = document.createElement('div');
  body.className = 'diff-section-body';
  populateBody(body);

  head.addEventListener('click', () => {
    sec.classList.toggle('collapsed');
    d[collapsedKey] = sec.classList.contains('collapsed');
    if (prSyncHandler) prSyncHandler();
  });

  sec.append(head, body);
  return sec;
}

function sectionNote(text) {
  const el = document.createElement('div');
  el.className = 'diff-section-note';
  el.textContent = text;
  return el;
}

/* One-way registration for pr.js, mirroring agent.js's hook into selbar.js:
   diff.js never imports pr.js, it just calls this after every repaint when a
   PR review session has set it. */
let prSyncHandler = null;
export function setPRSyncHandler(fn) { prSyncHandler = fn; }

/* Same one-way registration for tabs.js: diff.js knows how to leave diff mode
   but not how to move the caret and scroll the (already open) source view to
   a given line, so it hands the line off to whatever tabs.js registered. */
let sourceJumpHandler = null;
export function setSourceJumpHandler(fn) { sourceJumpHandler = fn; }

// The working-tree line number of whichever diff row currently sits at the
// top of the scrolled viewport -- what "Source" should land on so switching
// out of diff view keeps you where you were reading, not wherever the
// doc's cursor last happened to be.
function currentDiffLine() {
  if (!diffview || diffview.hidden) return null;
  const top = diffview.getBoundingClientRect().top;
  for (const el of diffview.querySelectorAll('[data-l], [data-at]')) {
    if (el.getBoundingClientRect().bottom > top) {
      return el.dataset.l !== undefined ? +el.dataset.l : +el.dataset.at;
    }
  }
  return null;
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
function parseDiff(text, highlighted = []) {
  if (!text) return [];
  const hunks = [];
  let cur = null, oldLine = 0, newLine = 0;
  for (const [i, line] of text.split('\n').entries()) {
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
    const html = highlighted?.[i] || esc(body);
    if (c === '+') cur.rows.push({ type: 'add', newLine: newLine++, html });
    // A deletion has no line on disk; at is the working-tree line it sat before.
    else if (c === '-') cur.rows.push({ type: 'del', oldLine: oldLine++, at: newLine, html });
    else cur.rows.push({ type: 'ctx', oldLine: oldLine++, newLine: newLine++, html });
  }
  return hunks;
}

/* ---------- unified layout: one row per diff line ---------- */

function unifiedTable(hunk, reviewable = true) {
  const table = document.createElement('div');
  table.className = 'diff-table diff-unified';
  for (const row of hunk.rows) {
    const r = document.createElement('div');
    r.className = 'diff-row diff-' + row.type;
    anchor(r, row, reviewable);
    r.append(
      lineCell(row.type === 'add' ? '' : row.oldLine, reviewable),
      lineCell(row.type === 'del' ? '' : row.newLine, reviewable),
      markerCell(row.type),
      codeCell(row.html),
    );
    table.append(r);
  }
  return table;
}

/* ---------- split layout: deletions and additions paired side by side ---------- */

function splitTable(hunk, reviewable = true) {
  const table = document.createElement('div');
  table.className = 'diff-table diff-split';
  for (const pair of pairRows(hunk.rows)) {
    const r = document.createElement('div');
    r.className = 'diff-row-pair';
    r.append(splitSide(pair.left, 'left', reviewable), splitSide(pair.right, 'right', reviewable));
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

function splitSide(row, side, reviewable = true) {
  const el = document.createElement('div');
  el.className = 'diff-side diff-side-' + side + (row ? ' diff-' + row.type : ' diff-blank');
  if (!row) { el.append(lineCell('', reviewable), markerCell(''), codeCell('')); return el; }
  const ln = side === 'left' ? row.oldLine : row.newLine;
  anchor(el, row, reviewable);
  el.append(lineCell(ln, reviewable), markerCell(row.type), codeCell(row.html));
  return el;
}

/* Stamps where a row points in the working tree, so a selection on it can be
   edited. Context and added lines have a line on disk (data-l), which a context
   line shares across both sides of the split. A deleted line has none, only the
   place it used to be (data-at).
   reviewable marks whether this row's line number is meaningful as a GitHub
   review-comment target -- true for the PR's own diff (numbered against the
   checked-out head commit, which is what a submitted review is posted
   against), false for the reviewer's local "Your changes" section, whose
   lines don't correspond to anything pushed yet. linecomment.js reads this
   to fall back to a plain inline AI edit instead of opening the composer. */
function anchor(el, row, reviewable = true) {
  if (row.newLine !== undefined) el.dataset.l = row.newLine;
  else if (row.at !== undefined) el.dataset.at = row.at;
  if (row.oldLine !== undefined) el.dataset.oldL = row.oldLine;
  if (!reviewable) el.dataset.reviewable = '0';
}

function lineCell(n, reviewable = true) {
  const el = document.createElement('div');
  el.className = 'diff-ln';
  if (n !== '' && n !== undefined) {
    el.classList.add('diff-ln-nav');
    el.title = 'Open in file view at line ' + n;
    const btn = document.createElement('span');
    btn.className = 'line-btn';
    btn.setAttribute('role', 'button');
    btn.title = (S.meta?.pr && reviewable) ? 'Add review comment' : 'Edit inline';
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

function codeCell(html = '') {
  const el = document.createElement('div');
  el.className = 'diff-code';
  el.innerHTML = html || '&nbsp;';
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
    const line = currentDiffLine();
    if (line && sourceJumpHandler) sourceJumpHandler(line);
    else setDiffMode('source');
  });
  // Clicking a line number jumps straight to the full file at that line --
  // the diff shows what changed, but reading it usually means seeing it in
  // context, not just the hunk.
  diffContent.addEventListener('click', e => {
    if (e.target.closest('.line-btn')) return;
    const cell = e.target.closest('.diff-ln-nav');
    if (!cell) return;
    const rowEl = cell.closest('[data-l], [data-at]');
    if (!rowEl || !sourceJumpHandler) return;
    e.stopPropagation();
    const line = rowEl.dataset.l !== undefined ? +rowEl.dataset.l : +rowEl.dataset.at;
    sourceJumpHandler(line);
  });
  $('#diff-btn')?.addEventListener('click', e => {
    e.stopPropagation();
    toggleDiff();
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
  on('tab:activated', () => syncDiffView());
  on('tabs:cleared', () => syncDiffView());
}
