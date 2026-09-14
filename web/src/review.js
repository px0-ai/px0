// web/src/review.js
// Review mode: the whole changeset against HEAD, walked by symbol.
//
// The parti: the rail is an index of which symbols the change touched and who
// they affect; the file is only how they are grouped. So the rail lists
// symbols under a file heading, the served pane shows that symbol's hunks, and
// a strip under it names the callers the change reaches.
//
// Degradation is the point, not an afterthought. /api/review always answers:
// a file with no outline (no rule for its language, deleted from disk, binary)
// comes back with an empty symbol list, and those files get their own group at
// the end where each row is the file itself. When no file has symbols the rail
// is a list of files and the mode still works -- same screen, same navigation,
// one layer of meaning short, and it says so with the way to fix it.
import { $, $$, S, api, esc, keyLabel } from './state.js';
import { renderHunks, plainText, layoutPref, fileHunks } from './diff.js';
import { openFile } from './tabs.js';
import { updateStatus, setLspState } from './status.js';

let view = null;      // #reviewview, built on first init
let railList = null, railFoot = null, paneHead = null, paneBody = null, paneCallers = null;

let R = null;         // the /api/review answer: {available, files:[...]}
let entries = [];     // flat rail model, one per rendered row
let cur = -1;         // selected index into entries, -1 while the overview shows
let opened = false;
let gen = 0;          // guards against a slow answer landing after a newer one

const seen = new Set();          // symbol keys already opened, for the dimming
const hunksByPath = new Map();   // path -> hunks, one /api/diff per file per session

const symKey = (file, sym) => file.path + '\u0000' + sym.name + '\u0000' + sym.line;
const heading = f => (f.old ? f.old + ' → ' + f.path : f.path);
const counts = f => '+' + f.added + ' −' + f.deleted;
const navigable = e => e.kind === 'sym' || e.kind === 'file';

export function toggleReview() {
  if (opened) closeReview();
  else showReview();
}

export function showReview() {
  if (!view) buildView();
  opened = true;
  view.hidden = false;
  document.body.classList.add('review-on');
  load();
}

export function closeReview() {
  if (!view) return;
  opened = false;
  view.hidden = true;
  document.body.classList.remove('review-on');
  updateStatus();
}

export function reviewing() { return opened; }

/* ---------- the changeset ---------- */

async function load() {
  const my = ++gen;
  cur = -1;
  entries = [];
  drawLoading();
  let j;
  try {
    j = await api('/api/review');
  } catch (e) {
    if (my !== gen) return;
    R = null;
    drawRail();
    centerState('Review is unavailable.', [e.message]);
    return;
  }
  if (my !== gen) return;
  R = j;
  entries = buildEntries(j.files || []);
  drawRail();
  drawOverview();
}

// The rail model: symbols grouped under their file, and every file the outline
// could not read collected into one group at the end -- never interleaved,
// because a file row and a symbol row do not mean the same thing.
function buildEntries(files) {
  const withSyms = files.filter(f => f.symbols && f.symbols.length);
  const without = files.filter(f => !f.symbols || !f.symbols.length);
  const out = [];
  for (const f of withSyms) {
    out.push({ kind: 'group', label: heading(f) });
    for (const sym of f.symbols) out.push({ kind: 'sym', file: f, sym });
  }
  if (without.length) {
    out.push({
      kind: 'group',
      label: withSyms.length ? 'no symbols · ' + without.length + ' files' : 'changed files',
    });
    for (const f of without) out.push({ kind: 'file', file: f });
  }
  return out;
}

/* ---------- rail ---------- */

function drawLoading() {
  // The grid holds its shape while the answer is in flight: rows at the same
  // height they will have, and no animation -- waiting is not a state change.
  railList.innerHTML = '<div class="rv-row rv-grp">reading changes</div>' +
    ['·'.repeat(9), '·'.repeat(14), '·'.repeat(10), '·'.repeat(12)]
      .map(d => '<div class="rv-row rv-sym rv-dim">' + d + '</div>').join('');
  railFoot.textContent = '';
  paneHead.innerHTML = '<span class="rv-h-nm">Review</span><span class="rv-h-sub">against HEAD</span>';
  centerState('Reading changes…', []);
  paneCallers.hidden = true;
}

function drawRail() {
  const html = entries.map((e, i) => {
    if (e.kind === 'group') return '<div class="rv-row rv-grp">' + esc(e.label) + '</div>';
    if (e.kind === 'file') {
      return '<div class="rv-row rv-file' + (i === cur ? ' rv-on' : '') + '" data-i="' + i + '" title="' + esc(e.file.path) + '">' +
        '<span class="rv-nm">' + esc(e.file.path) + '</span>' +
        '<span class="rv-cnt">' + esc(counts(e.file)) + '</span></div>';
    }
    const key = symKey(e.file, e.sym);
    const cls = 'rv-row rv-sym' + (i === cur ? ' rv-on' : '') + (seen.has(key) ? ' rv-seen' : '');
    return '<div class="' + cls + '" data-i="' + i + '" title="' + esc(e.file.path + ':' + e.sym.line) + '">' +
      '<span class="rv-nm">' + esc(e.sym.name) + '</span>' +
      '<span class="rv-cnt">' + e.sym.changed + '</span></div>';
  }).join('');
  railList.innerHTML = html || '<div class="rv-row rv-grp">no changes</div>';
  drawFoot();
}

function drawFoot() {
  const syms = entries.filter(e => e.kind === 'sym');
  const files = (R && R.files ? R.files.length : 0);
  railFoot.textContent = syms.length
    ? countSeen() + ' of ' + syms.length + ' reviewed · ' + files + ' files'
    : files + ' files';
}

function countSeen() {
  return entries.filter(e => e.kind === 'sym' && seen.has(symKey(e.file, e.sym))).length;
}

/* ---------- served pane ---------- */

function centerState(big, small) {
  paneBody.innerHTML = '<div class="rv-center"><span class="rv-big">' + esc(big) + '</span>' +
    (small || []).map(s => '<span class="rv-sm">' + esc(s) + '</span>').join('') + '</div>';
}

// The arrival screen, and the one at the end of the queue: an inventory, not a
// diff. It says what happened, how much of it there is, and what is left.
function drawOverview() {
  cur = -1;
  paneCallers.hidden = true;
  drawRail();
  if (!R) return;
  if (!R.available) {
    paneHead.innerHTML = '<span class="rv-h-nm">Review</span><span class="rv-h-sub">unavailable</span>';
    centerState('Not a git repository.', [
      'Review compares the working tree against HEAD.',
      'Started with -no-git? Restart without it.',
    ]);
    return;
  }
  const files = R.files || [];
  paneHead.innerHTML = '<span class="rv-h-nm">Review</span><span class="rv-h-sub">against HEAD</span>';
  if (!files.length) {
    centerState('Nothing changed against HEAD.', ['Untracked files are included when there are any.']);
    return;
  }
  const syms = entries.filter(e => e.kind === 'sym').length;
  const done = countSeen();
  const job = syms && done >= syms
    ? 'Reviewed ' + syms + ' symbols across ' + files.length + ' files.'
    : 'Review what changed against HEAD, by symbol.';
  paneBody.innerHTML = '<div class="rv-overview"><div class="rv-job">' + esc(job) + '</div>' +
    '<div class="rv-ovr rv-ovr-hd"><span class="rv-f">File</span><span class="rv-d">Lines</span><span class="rv-s">Symbols</span></div>' +
    files.map(f =>
      '<div class="rv-ovr"><span class="rv-f">' + esc(heading(f)) + '</span>' +
      '<span class="rv-d"><span class="rv-i">+' + f.added + '</span> <span class="rv-x">−' + f.deleted + '</span></span>' +
      '<span class="rv-s">' + (f.symbols.length || '—') + '</span></div>').join('') +
    '</div>';
}

async function select(i) {
  const e = entries[i];
  if (!e || !navigable(e)) return;
  cur = i;
  const my = ++gen;
  if (e.kind === 'sym') seen.add(symKey(e.file, e.sym));
  drawRail();
  railList.querySelector('.rv-on')?.scrollIntoView({ block: 'nearest' });

  paneHead.innerHTML = e.kind === 'sym'
    ? '<span class="rv-h-nm">' + esc(e.sym.name) + '</span>' +
      '<span class="rv-h-sub">' + esc(e.file.path + ' · ' + e.sym.changed + ' changed lines') + '</span>'
    : '<span class="rv-h-nm">' + esc(heading(e.file)) + '</span>' +
      '<span class="rv-h-sub">' + esc(counts(e.file)) + '</span>';
  centerState('Diffing ' + e.file.path + '…', []);
  paneCallers.hidden = true;

  let hunks;
  try {
    hunks = await diffOf(e.file.path);
  } catch (err) {
    if (my !== gen) return;
    centerState('Could not read the diff.', [err.message]);
    return;
  }
  if (my !== gen) return;
  const mine = e.kind === 'sym' ? hunksFor(e, hunks) : hunks;
  paneBody.replaceChildren();
  if (!mine.length) {
    centerState(e.kind === 'sym' ? 'No hunk covers this symbol.' : 'No hunks in this file.',
      ['The file reports ' + counts(e.file) + ' against HEAD.']);
  } else {
    const wrap = document.createElement('div');
    wrap.className = 'rv-rows';
    wrap.append(renderHunks(mine, layoutPref()));
    paneBody.append(wrap);
    const sub = paneHead.querySelector('.rv-h-sub');
    if (sub) sub.textContent += ' · ' + mine.length + (mine.length === 1 ? ' hunk' : ' hunks');
  }
  drawCallers(e, hunks, my);
}

async function diffOf(path) {
  if (!hunksByPath.has(path)) {
    hunksByPath.set(path, fileHunks(path));
  }
  try {
    return await hunksByPath.get(path);
  } catch (e) {
    hunksByPath.delete(path); // a failed fetch must not poison the cache
    throw e;
  }
}

/* A symbol owns the lines from its declaration up to the next symbol's, so a
   hunk belongs to it when any of the hunk's new-side lines fall in that span.
   Rows deleted outright have no new-side line and ride along with the hunk
   they are in, which is what a reader wants: the removed lines shown next to
   what replaced them. */
function hunksFor(entry, hunks) {
  const syms = entry.file.symbols;
  const at = syms.indexOf(entry.sym);
  const from = entry.sym.line;
  const to = at >= 0 && at + 1 < syms.length ? syms[at + 1].line : Infinity;
  return hunks.filter(h => h.rows.some(r => r.new && r.new >= from && r.new < to));
}

/* ---------- callers ---------- */

const lspOff = () => S.lsp.state === 'off' || S.lsp.state === 'failed';
const setupHint = '<div class="rv-row rv-cl rv-quiet"><span class="rv-nm">' +
  esc(keyLabel('Mod+K')) + '</span><span class="rv-at">Set up a language server</span></div>';

function quiet(msg, withSetup) {
  paneCallers.hidden = false;
  paneCallers.innerHTML = '<div class="rv-row rv-cl rv-quiet">' + esc(msg) + '</div>' + (withSetup ? setupHint : '');
}

// Callers are asked for one symbol at a time and only when one is selected:
// the call hierarchy is the expensive question, and most symbols are never
// opened. Without a language server the strip says so and points at the fix --
// it is a row of the inventory, not a warning banner.
async function drawCallers(entry, hunks, my) {
  if (entry.kind !== 'sym') {
    quiet('No symbols here — grouping by file.', lspOff());
    return;
  }
  if (lspOff()) {
    quiet('No language server — callers unavailable.', true);
    return;
  }
  const col = await declarationCol(entry, hunks);
  if (my !== gen) return;
  quiet('Tracing callers of ' + entry.sym.name + '…', false);
  let j;
  try {
    j = await api('/api/lsp/calls', {
      path: entry.file.path, line: entry.sym.line, col,
      wait: S.lsp.state === 'ready' ? 10000 : 30000,
    });
  } catch (e) {
    if (my === gen) quiet('Could not trace callers: ' + e.message, false);
    return;
  }
  if (my !== gen) return;
  setLspState(j);
  updateStatus();
  const nodes = j.nodes || [];
  if (!nodes.length) { quiet('No callers ' + (j.server || 'the language server') + ' can find.', false); return; }
  const sites = nodes.reduce((n, c) => n + Math.max(1, (c.sites || []).length), 0);
  const shown = nodes.slice(0, 8);
  paneCallers.hidden = false;
  paneCallers.innerHTML =
    '<div class="rv-row rv-lbl">Called from · ' + nodes.length + ' callers, ' + sites + ' sites</div>' +
    shown.map(n => {
      const path = n.sitePath || n.path;
      const line = (n.sites && n.sites.length) ? n.sites[0] : n.line;
      const more = n.sites && n.sites.length > 1 ? ', :' + n.sites.slice(1, 3).join(', :') : '';
      return '<div class="rv-row rv-cl" data-path="' + esc(path) + '" data-line="' + line + '">' +
        '<span class="rv-nm">' + esc(n.name) + '</span>' +
        '<span class="rv-at">' + esc(path.split('/').pop()) + ':' + line + esc(more) + '</span></div>';
    }).join('') +
    (nodes.length > shown.length ? '<div class="rv-row rv-cl rv-quiet">' + (nodes.length - shown.length) + ' more</div>' : '');
}

/* The call hierarchy is asked at a position, not at a name, so the symbol's
   column has to come from the declaration line itself. It is usually already
   in a hunk we hold; when the change did not touch the declaration, one line
   of the file answers it. Column 0 is the honest fallback -- a server that
   resolves the whole line still answers, and one that does not says so. */
async function declarationCol(entry, hunks) {
  const name = entry.sym.name;
  for (const h of hunks || []) {
    for (const r of h.rows) {
      if (r.new !== entry.sym.line || r.type === 'del') continue;
      const at = plainText(r.html).indexOf(name);
      if (at >= 0) return at;
    }
  }
  try {
    const j = await api('/api/file', { path: entry.file.path, start: entry.sym.line - 1, count: 1 });
    const at = plainText((j.lines || [])[0] || '').indexOf(name);
    if (at >= 0) return at;
  } catch {}
  return 0;
}

/* ---------- navigation ---------- */

function step(delta) {
  const idx = entries.map((e, i) => (navigable(e) ? i : -1)).filter(i => i >= 0);
  if (!idx.length) return;
  const at = idx.indexOf(cur);
  const next = at < 0 ? (delta > 0 ? 0 : idx.length - 1) : Math.min(idx.length - 1, Math.max(0, at + delta));
  select(idx[next]);
}

function stepHunk(delta) {
  const heads = $$('.diff-hunk-head', paneBody);
  if (!heads.length) return;
  // Offsets read from rects, not offsetTop: the pane's offset parent depends
  // on styling this module does not own.
  const base = paneBody.getBoundingClientRect().top - paneBody.scrollTop;
  const tops = heads.map(h => h.getBoundingClientRect().top - base);
  const at = tops.findIndex(t => t > paneBody.scrollTop + 1);
  const i = delta > 0 ? (at < 0 ? tops.length - 1 : at) : Math.max(0, (at < 0 ? tops.length : at) - 2);
  paneBody.scrollTop = tops[i];
}

// Enter opens the real file at the line under review, which is where editing
// happens: review is for deciding, the editor is for changing.
function openHere() {
  const e = entries[cur];
  if (!e) return;
  closeReview();
  openFile(e.file.path, e.kind === 'sym' ? { line: e.sym.line } : {});
}

function onKey(e) {
  if (!opened) return;
  if (e.altKey || e.ctrlKey || e.metaKey) return;
  const t = e.target;
  if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return;
  const k = e.key;
  let hit = true;
  if (k === 'Escape') closeReview();
  else if (k === 'ArrowDown' || k === 'j') step(1);
  else if (k === 'ArrowUp' || k === 'k') step(-1);
  else if (k === 'Enter') openHere();
  else if (k === 'n') stepHunk(1);
  else if (k === 'N') stepHunk(-1);
  else if (k === 'r') load();
  else hit = false;
  if (!hit) return;
  e.preventDefault();
  e.stopPropagation();
}

/* ---------- wiring ---------- */

function buildView() {
  view = $('#reviewview');
  if (!view) {
    view = document.createElement('div');
    view.id = 'reviewview';
    ($('#editor') || document.body).append(view);
  }
  view.hidden = true;
  view.innerHTML =
    '<div class="rv-body">' +
      '<div class="rv-rail">' +
        '<div class="rv-phead"><span>REVIEW</span><span class="rv-esc">esc</span></div>' +
        '<div class="rv-list"></div>' +
        '<div class="rv-row rv-foot"></div>' +
      '</div>' +
      '<div class="rv-pane">' +
        '<div class="rv-head"></div>' +
        '<div class="rv-content"></div>' +
        '<div class="rv-callers" hidden></div>' +
      '</div>' +
    '</div>';
  railList = $('.rv-list', view);
  railFoot = $('.rv-foot', view);
  paneHead = $('.rv-head', view);
  paneBody = $('.rv-content', view);
  paneCallers = $('.rv-callers', view);

  railList.addEventListener('click', e => {
    const row = e.target.closest('[data-i]');
    if (row) select(+row.dataset.i);
  });
  $('.rv-esc', view).addEventListener('click', closeReview);
  paneCallers.addEventListener('click', e => {
    const row = e.target.closest('.rv-cl[data-path]');
    if (!row) return;
    closeReview();
    openFile(row.dataset.path, { line: +row.dataset.line });
  });
}

export function initReview() {
  buildView();
  // Capture, so review's own keys win over the global shortcuts while it is
  // the surface in front of the reader -- and only while it is.
  document.addEventListener('keydown', onKey, true);
}
