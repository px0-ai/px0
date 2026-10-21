// web/src/diff.js
// Git diff view for the active tab: renders the file's unified diff against
// HEAD in a dedicated overlay (like the Markdown preview), in either a
// side-by-side split layout (default) or a single-column unified layout.
// Unlike the code viewport this is not virtualized -- a file's own diff is
// bounded in size, so a plain DOM render is simple and fast enough.
import { $, S, doc_, esc, api, MOD } from './state.js';
import { on } from './bus.js';
import { syncPreview } from './markdown.js';
import { setStatusNote, updateStatus } from './status.js';
import { wordAtPoint } from './cursor.js';
import { gotoDefinition } from './lsp.js';
import { hoverAt } from './hover.js';
import { pushHistory } from './history.js';
import { COMMIT_STATUS, loadCommitFiles, cachedCommitFiles } from './commitfiles.js';

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
  } else if (want) {
    /* Same doc still on screen. A caller that dropped the cached diff -- the
       tab being pointed at a different commit, say -- leaves the view showing
       the wrong revision, so that has to refetch rather than just repaint. */
    if (want.diffText === undefined) drawDiff(want);
    else if (want.diffHunks !== undefined) renderDiff(want);
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
    /* Which revision this fetch is for. Clicking through a commit's files, or
       from a commit back to the tree, can leave an earlier request in flight;
       without this its answer would land in a doc that has since been pointed
       somewhere else and show the wrong commit's diff. */
    const ref = d.diffRef || '';
    try {
      d.diffReq = api('/api/diff', { path: d.path, ref });
      const j = await d.diffReq;
      if ((d.diffRef || '') !== ref) return;
      d.diffText = j.diff || '';
      d.diffHunks = j.hunks || parseDiff(d.diffText);
      // In a PR review session the server also splits the diff at the PR's
      // checked-out head commit: prDiff is the PR's own change (frozen since
      // checkout/last Pull), yourDiff is whatever the reviewer has edited or
      // committed locally since then. Undefined outside PR mode.
      d.prDiffHunks = j.prHunks !== undefined ? j.prHunks : (j.prDiff !== undefined ? parseDiff(j.prDiff) : undefined);
      d.yourDiffHunks = j.yourHunks !== undefined ? j.yourHunks : (j.yourDiff !== undefined ? parseDiff(j.yourDiff) : undefined);
    } catch (e) {
      if ((d.diffRef || '') !== ref) return;
      d.diffText = '';
      d.diffHunks = [];
      d.prDiffHunks = undefined;
      d.yourDiffHunks = undefined;
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
  } else if (d.cur) {
    scrollDiffToLine(d.cur, false, false);
  }
}

export function scrollDiffToLine(line, smooth = false, flash = false) {
  if (!diffview || !line) return;
  const row = diffview.querySelector(`[data-l="${line}"]`) ||
              diffview.querySelector(`[data-at="${line}"]`) ||
              diffview.querySelector(`[data-old-l="${line}"]`);
  if (row) {
    row.scrollIntoView({ block: 'center', behavior: smooth ? 'smooth' : 'auto' });
    if (flash) {
      row.classList.add('diff-row-flash');
      setTimeout(() => row.classList.remove('diff-row-flash'), 1200);
    }
  }
}

function appendHunks(frag, hunks, mode, reviewable) {
  detectMoves(hunks);
  for (const hunk of hunks) {
    frag.append(hunkHeader(hunk));
    frag.append(mode === 'unified' ? unifiedTable(hunk, reviewable) : splitTable(hunk, reviewable));
  }
}

function renderDiff(d) {
  diffContent.replaceChildren();
  diffContent.classList.remove('has-diff-moves');
  if (diffContent.style.getPropertyValue('--diff-gutter-w')) {
    diffContent.style.removeProperty('--diff-gutter-w');
  }

  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.id = 'diff-moves-svg';
  svg.setAttribute('class', 'diff-moves-svg');
  diffContent.append(svg);

  const frag = document.createDocumentFragment();
  if (d.diffRef) {
    /* Pinned to one commit by the Unpushed sidebar section. Its line numbers
       are that commit's, not the working tree's, so nothing here is a review
       target (reviewable=false) -- see anchor() below. */
    const hunks = d.diffHunks || [];
    if (!hunks.length) {
      const p = document.createElement('div');
      p.className = 'diff-empty';
      p.textContent = 'This commit made no change to ' + d.name + '.';
      diffContent.append(p);
      return;
    }
    frag.append(commitStrip(d));
    frag.append(createDiffSection(d, 'commit', 'In commit ' + d.diffRef.slice(0, 7),
      'this commit only, not the working tree', (bodyEl) => appendHunks(bodyEl, hunks, d.diffMode, false)));
    diffContent.append(frag);
    syncDiffAgentTargets();
    if (prSyncHandler) prSyncHandler();
    scheduleDrawMoveConnectors();
    return;
  }
  if (S.meta?.pr && d.prDiffHunks !== undefined) {
    const prHunks = d.prDiffHunks || [];
    const yourHunks = d.yourDiffHunks || [];
    // Opened from the sidebar's "Yours" or "PR changes" scope: open the
    // matching section and fold the other. Decided once per tab; after that
    // the section's own header toggle wins.
    const tree = $('#tree');
    if (d.prCollapsed === undefined) {
      d.prCollapsed = !!tree?.classList.contains('scope-yours') && yourHunks.length > 0;
    }
    if (d.yourCollapsed === undefined) {
      d.yourCollapsed = !!tree?.classList.contains('scope-pr') && prHunks.length > 0;
    }
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
  scheduleDrawMoveConnectors();
}

/* Points a tab's diff at the section the sidebar scope is showing. A section
   with nothing in it is left open: folding away the only content leaves an
   empty pane. */
export function focusDiffScope(d) {
  const tree = $('#tree');
  if (!d || !S.meta?.pr || !tree) return;
  if (tree.classList.contains('scope-yours') && d.yourDiffHunks?.length !== 0) {
    d.prCollapsed = true; d.yourCollapsed = false;
  } else if (tree.classList.contains('scope-pr') && d.prDiffHunks?.length !== 0) {
    d.prCollapsed = false; d.yourCollapsed = true;
  }
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
    scheduleDrawMoveConnectors();
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

/* And for the commit strip's ‹ › buttons: tabs.js opens the file stepped to. */
let commitStepHandler = null;
export function setCommitStepHandler(fn) { commitStepHandler = fn; }

// The nearest file from i in direction dir that can be opened: binary files
// have no text diff and /api/file refuses them, so stepping passes over them.
function commitStepTarget(files, i, dir) {
  for (let k = i + dir; k >= 0 && k < files.length; k += dir) if (!files[k].binary) return k;
  return -1;
}

/* Where a pinned tab's file sits in its commit, with ‹ › to step through the
   rest. The file list is fetched on first need and filled in when it lands. */
function commitStrip(d) {
  const el = document.createElement('div');
  el.className = 'diff-commit-strip';
  const fill = files => {
    const i = files.findIndex(f => f.path === d.path);
    if (i < 0) { el.remove(); return; }
    const f = files[i];
    const g = COMMIT_STATUS[f.status] || ['git-M', f.status];
    const path = f.from ? esc(f.from) + ' → ' + esc(f.path) : esc(f.path);
    el.innerHTML =
      '<span class="gs ' + g[0] + '" title="' + esc(g[1]) + '">' + esc(f.status) + '</span>' +
      '<span class="dcs-path" title="' + path + '">' + path + '</span>' +
      (f.binary ? '' : (f.add ? '<span class="up-file-add">+' + f.add + '</span>' : '') + (f.del ? '<span class="up-file-del">&minus;' + f.del + '</span>' : '')) +
      '<span class="grow"></span>' +
      '<span class="dcs-pos">' + (i + 1) + ' / ' + files.length + '</span>' +
      '<button type="button" class="dcs-step" data-commit-step="-1" title="Previous file in this commit"' + (commitStepTarget(files, i, -1) < 0 ? ' disabled' : '') + '>‹</button>' +
      '<button type="button" class="dcs-step" data-commit-step="1" title="Next file in this commit"' + (commitStepTarget(files, i, 1) < 0 ? ' disabled' : '') + '>›</button>';
    el.dataset.i = i;
  };
  const have = cachedCommitFiles(d.diffRef);
  if (have) fill(have);
  else loadCommitFiles(d.diffRef).then(files => { if (el.isConnected || doc_() === d) fill(files); }, () => el.remove());
  return el;
}

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

/* ---------- moved block detection ---------- */

let diffMoveCounter = 0;

function isTrivialMoveLine(str) {
  const t = (str || '').trim();
  return t.length === 0 || /^[{}\[\]();,\/\\*]+$/.test(t);
}

function isValidMoveSlice(rows) {
  if (rows.length < 3) return false;
  let nonTrivial = 0;
  let totalChars = 0;
  for (const r of rows) {
    const t = (r.text || '').trim();
    if (!isTrivialMoveLine(t)) {
      nonTrivial++;
      totalChars += t.length;
    }
  }
  return nonTrivial >= 2 && totalChars >= 15;
}

function computeMaxConcurrentTracks(moveList) {
  if (!moveList || moveList.length <= 1) return moveList ? moveList.length : 0;
  const events = [];
  for (const m of moveList) {
    const s = Math.min(m.delStart, m.addStart);
    const e = Math.max(m.delEnd, m.addEnd);
    events.push({ pos: s, type: 1 });
    events.push({ pos: e, type: -1 });
  }
  events.sort((a, b) => a.pos - b.pos || b.type - a.type);
  let cur = 0, maxTracks = 0;
  for (const ev of events) {
    cur += ev.type;
    if (cur > maxTracks) maxTracks = cur;
  }
  return maxTracks;
}

function detectMoves(hunks) {
  diffMoveCounter = 0;
  if (!hunks || !hunks.length) return;
  for (const h of hunks) {
    for (const r of h.rows) {
      delete r.move;
    }
  }

  const detectedMoves = [];

  // Collect contiguous runs of 'del' rows and contiguous runs of 'add' rows
  const delRuns = [];
  const addRuns = [];

  for (let hi = 0; hi < hunks.length; hi++) {
    const rows = hunks[hi].rows;
    let i = 0;
    while (i < rows.length) {
      if (rows[i].type === 'del') {
        const run = [];
        while (i < rows.length && rows[i].type === 'del') run.push(rows[i++]);
        delRuns.push(run);
      } else if (rows[i].type === 'add') {
        const run = [];
        while (i < rows.length && rows[i].type === 'add') run.push(rows[i++]);
        addRuns.push(run);
      } else {
        i++;
      }
    }
  }

  // Find candidate matches between delRuns and addRuns
  const candidates = [];
  for (let di = 0; di < delRuns.length; di++) {
    const dRun = delRuns[di];
    for (let ai = 0; ai < addRuns.length; ai++) {
      const aRun = addRuns[ai];
      for (let dStart = 0; dStart < dRun.length; dStart++) {
        for (let aStart = 0; aStart < aRun.length; aStart++) {
          if (dStart > 0 && aStart > 0 && dRun[dStart - 1].text === aRun[aStart - 1].text) continue;
          let len = 0;
          while (
            dStart + len < dRun.length &&
            aStart + len < aRun.length &&
            dRun[dStart + len].text === aRun[aStart + len].text
          ) {
            len++;
          }
          if (len >= 3) {
            const slice = dRun.slice(dStart, dStart + len);
            if (isValidMoveSlice(slice)) {
              candidates.push({ di, dStart, ai, aStart, len });
            }
          }
        }
      }
    }
  }

  // Sort candidates by length descending so largest matching blocks win
  candidates.sort((a, b) => b.len - a.len);

  const matchedDels = new Set();
  const matchedAdds = new Set();

  for (const cand of candidates) {
    const dRun = delRuns[cand.di];
    const aRun = addRuns[cand.ai];

    let overlaps = false;
    for (let k = 0; k < cand.len; k++) {
      if (matchedDels.has(dRun[cand.dStart + k]) || matchedAdds.has(aRun[cand.aStart + k])) {
        overlaps = true;
        break;
      }
    }
    if (overlaps) continue;

    for (let k = 0; k < cand.len; k++) {
      matchedDels.add(dRun[cand.dStart + k]);
      matchedAdds.add(aRun[cand.aStart + k]);
    }

    const moveId = 'move-' + (++diffMoveCounter);
    const dSlice = dRun.slice(cand.dStart, cand.dStart + cand.len);
    const aSlice = aRun.slice(cand.aStart, cand.aStart + cand.len);

    const delStart = dSlice[0].oldLine;
    const delEnd = dSlice[dSlice.length - 1].oldLine;
    const addStart = aSlice[0].newLine;
    const addEnd = aSlice[aSlice.length - 1].newLine;

    detectedMoves.push({ delStart, delEnd, addStart, addEnd });

    for (let k = 0; k < cand.len; k++) {
      const d = dSlice[k];
      const a = aSlice[k];
      d.move = {
        id: moveId,
        side: 'from',
        isFirst: k === 0,
        isLast: k === cand.len - 1,
        lineCount: cand.len,
        myStartLine: delStart,
        myEndLine: delEnd,
        otherStartLine: addStart,
        otherEndLine: addEnd,
      };
      a.move = {
        id: moveId,
        side: 'to',
        isFirst: k === 0,
        isLast: k === cand.len - 1,
        lineCount: cand.len,
        myStartLine: addStart,
        myEndLine: addEnd,
        otherStartLine: delStart,
        otherEndLine: delEnd,
      };
    }
  }

  // Dynamically size the middle gutter corridor based on maximum concurrent tracks
  const maxTracks = computeMaxConcurrentTracks(detectedMoves);
  if (diffContent) {
    diffContent.classList.toggle('has-diff-moves', maxTracks > 0);
    if (maxTracks > 0) {
      const gutterW = maxTracks === 1 ? 28 : Math.min(84, 16 + maxTracks * 14);
      diffContent.style.setProperty('--diff-gutter-w', gutterW + 'px');
    } else {
      diffContent.style.removeProperty('--diff-gutter-w');
    }
  }
}

function createMoveHeader(move, layout = 'unified') {
  const el = document.createElement('div');
  el.className = 'diff-move-bar diff-move-bar-' + move.side;
  el.dataset.moveId = move.id;
  el.dataset.targetLine = move.otherStartLine;
  el.dataset.moveSide = move.side;

  const isFrom = move.side === 'from';
  const rangeStr = move.otherStartLine + (move.otherStartLine !== move.otherEndLine ? '–' + move.otherEndLine : '');

  const text = document.createElement('span');
  text.className = 'diff-move-text';
  text.textContent = (isFrom ? 'Code moved to line ' : 'Code moved from line ') + rangeStr;

  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'diff-move-jump';
  btn.dataset.moveId = move.id;
  btn.dataset.targetLine = move.otherStartLine;
  btn.dataset.moveSide = move.side;
  btn.title = 'Jump to ' + (isFrom ? 'destination' : 'source') + ' (line ' + move.otherStartLine + ')';
  btn.innerHTML = isFrom ? '↷ Jump' : '↶ Jump';

  el.append(text, btn);
  return el;
}

let diffMoveRaf = null;

function scheduleDrawMoveConnectors() {
  if (diffMoveRaf) cancelAnimationFrame(diffMoveRaf);
  diffMoveRaf = requestAnimationFrame(() => {
    diffMoveRaf = null;
    drawMoveConnectors();
  });
}

function drawMoveConnectors() {
  const svg = $('#diff-moves-svg');
  if (!svg || diffview.hidden) return;

  svg.replaceChildren();

  const d = doc_();
  if (d?.diffMode === 'unified') return;

  const moveIds = new Set();
  for (const el of diffContent.querySelectorAll('.diff-side-move-header[data-move-id], .diff-move-bar[data-move-id]')) {
    moveIds.add(el.dataset.moveId);
  }
  if (!moveIds.size) return;

  const w = Math.max(diffContent.scrollWidth, diffContent.clientWidth, diffview.scrollWidth || 0);
  const h = Math.max(diffContent.scrollHeight, diffContent.clientHeight, diffview.scrollHeight || 0);
  svg.setAttribute('width', String(w));
  svg.setAttribute('height', String(h));

  const defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs');
  defs.innerHTML = `
    <marker id="diff-move-arrow" viewBox="0 0 10 10" refX="7" refY="5" markerWidth="6" markerHeight="6" orient="auto">
      <path d="M 0 1 L 8 5 L 0 9 z" fill="var(--diff-move)" />
    </marker>
    <marker id="diff-move-arrow-active" viewBox="0 0 10 10" refX="7" refY="5" markerWidth="6" markerHeight="6" orient="auto">
      <path d="M 0 1 L 8 5 L 0 9 z" fill="var(--accent)" />
    </marker>
  `;
  svg.append(defs);

  const cRect = diffContent.getBoundingClientRect();

  // Collect all moves with their geometry and vertical spans
  const moves = [];
  for (const moveId of moveIds) {
    const sEl = diffContent.querySelector(`.diff-side-left.diff-side-move-header[data-move-id="${moveId}"]`) ||
                diffContent.querySelector(`.diff-side-left.diff-moved-first[data-move-id="${moveId}"]`);
    const tEl = diffContent.querySelector(`.diff-side-right.diff-side-move-header[data-move-id="${moveId}"]`) ||
                diffContent.querySelector(`.diff-side-right.diff-moved-first[data-move-id="${moveId}"]`);
    if (!sEl || !tEl || !sEl.offsetParent || !tEl.offsetParent) continue;

    const sRect = sEl.getBoundingClientRect();
    const tRect = tEl.getBoundingClientRect();
    if (sRect.height === 0 || tRect.height === 0) continue;

    const gutterEl = sEl.parentElement?.querySelector('.diff-split-gutter') || diffContent.querySelector('.diff-split-gutter');
    const gRect = gutterEl ? gutterEl.getBoundingClientRect() : null;

    const y1 = sRect.top - cRect.top + sRect.height / 2;
    const y2 = tRect.top - cRect.top + tRect.height / 2;

    moves.push({
      moveId,
      sRect,
      tRect,
      gRect,
      y1,
      y2,
      minY: Math.min(y1, y2),
      maxY: Math.max(y1, y2),
      track: 0,
    });
  }

  if (!moves.length) return;

  // Sort moves vertically to allocate non-overlapping tracks
  moves.sort((a, b) => a.minY - b.minY || a.maxY - b.maxY);

  // Interval track allocation (greedy coloring) so overlapping vertical lines use distinct lanes
  const trackEndTimes = [];
  for (const m of moves) {
    let assigned = -1;
    for (let t = 0; t < trackEndTimes.length; t++) {
      if (trackEndTimes[t] + 4 < m.minY) {
        assigned = t;
        trackEndTimes[t] = m.maxY;
        break;
      }
    }
    if (assigned === -1) {
      assigned = trackEndTimes.length;
      trackEndTimes.push(m.maxY);
    }
    m.track = assigned;
  }

  const numTracks = Math.max(1, trackEndTimes.length);

  for (const m of moves) {
    let x1, x2, xTrack;
    if (m.gRect) {
      x1 = m.gRect.left - cRect.left;
      x2 = m.gRect.right - cRect.left;
      const gWidth = m.gRect.width;

      if (numTracks === 1) {
        xTrack = x1 + gWidth / 2;
      } else {
        const pad = Math.min(10, Math.max(6, gWidth * 0.16));
        const avail = gWidth - 2 * pad;
        const step = avail / (numTracks - 1);
        xTrack = x1 + pad + m.track * step;
      }
    } else {
      const xMid = m.sRect.right - cRect.left;
      x1 = xMid - 22;
      x2 = xMid + 22;
      if (numTracks === 1) {
        xTrack = xMid;
      } else {
        const pad = 8;
        const avail = 44 - 2 * pad;
        const step = avail / (numTracks - 1);
        xTrack = x1 + pad + m.track * step;
      }
    }

    // Straight orthogonal path with non-overlapping vertical track
    const dStr = `M ${x1} ${m.y1} L ${xTrack} ${m.y1} L ${xTrack} ${m.y2} L ${x2} ${m.y2}`;

    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', dStr);
    path.setAttribute('class', 'diff-move-connector');
    path.dataset.moveId = m.moveId;
    path.setAttribute('marker-end', 'url(#diff-move-arrow)');
    svg.append(path);
  }
}

function flashMoveSection(moveId, targetSide) {
  if (!diffview || !moveId || !targetSide) return;
  const els = diffview.querySelectorAll(
    `.diff-moved-row.diff-moved-${targetSide}[data-move-id="${moveId}"], .diff-move-bar-${targetSide}[data-move-id="${moveId}"]`
  );
  for (const el of els) {
    el.classList.remove('diff-row-flash');
    void el.offsetWidth;
    el.classList.add('diff-row-flash');
    setTimeout(() => el.classList.remove('diff-row-flash'), 1050);
  }
}

const FLASH_PAUSE_MS = 180; // Intentional pause after arriving before the blink triggers

function jumpAndFlash(targetEl, moveId, targetSide) {
  if (!targetEl || !diffview) return;
  const diffRect = diffview.getBoundingClientRect();
  const targetRect = targetEl.getBoundingClientRect();
  const isCentered = Math.abs((targetRect.top + targetRect.height / 2) - (diffRect.top + diffRect.height / 2)) < 30;

  targetEl.scrollIntoView({ block: 'center', behavior: isCentered ? 'auto' : 'smooth' });

  if (isCentered) {
    if (moveId && targetSide) {
      setTimeout(() => flashMoveSection(moveId, targetSide), FLASH_PAUSE_MS);
    }
    return;
  }

  if (moveId && targetSide) {
    let fired = false;
    const trigger = () => {
      if (fired) return;
      fired = true;
      setTimeout(() => flashMoveSection(moveId, targetSide), FLASH_PAUSE_MS);
    };
    diffview.addEventListener('scrollend', trigger, { once: true });
    setTimeout(trigger, 450);
  }
}

function setMoveHover(moveId, active) {
  if (!diffview || !moveId) return;
  const els = diffview.querySelectorAll(`[data-move-id="${moveId}"]`);
  for (const el of els) {
    el.classList.toggle('diff-move-hover', active);
    if (el instanceof SVGPathElement || (el.tagName && el.tagName.toLowerCase() === 'path')) {
      el.setAttribute('marker-end', active ? 'url(#diff-move-arrow-active)' : 'url(#diff-move-arrow)');
    }
  }
}

/* ---------- unified layout: one row per diff line ---------- */

function unifiedTable(hunk, reviewable = true) {
  const table = document.createElement('div');
  table.className = 'diff-table diff-unified';

  // Pair consecutive runs of deletions and additions to compute intra-line diffs
  const rowHtml = new Map();
  let i = 0;
  while (i < hunk.rows.length) {
    if (hunk.rows[i].type === 'ctx') { i++; continue; }
    let dels = [], adds = [];
    while (i < hunk.rows.length && hunk.rows[i].type === 'del') dels.push(hunk.rows[i++]);
    while (i < hunk.rows.length && hunk.rows[i].type === 'add') adds.push(hunk.rows[i++]);
    const nonMovedDels = dels.filter(d => !d.move);
    const nonMovedAdds = adds.filter(a => !a.move);
    const n = Math.min(nonMovedDels.length, nonMovedAdds.length);
    for (let k = 0; k < n; k++) {
      const diff = diffLine(nonMovedDels[k].text, nonMovedAdds[k].text);
      rowHtml.set(nonMovedDels[k], diff.oldHtml);
      rowHtml.set(nonMovedAdds[k], diff.newHtml);
    }
  }

  for (const row of hunk.rows) {
    if (row.move && row.move.isFirst) {
      table.append(createMoveHeader(row.move, 'unified'));
    }
    const r = document.createElement('div');
    r.className = 'diff-row diff-' + row.type;
    if (row.move) {
      r.classList.add('diff-moved-row', 'diff-moved-' + row.move.side);
      r.dataset.moveId = row.move.id;
      if (row.move.isFirst) r.classList.add('diff-moved-first');
      if (row.move.isLast) r.classList.add('diff-moved-last');
    }
    anchor(r, row, reviewable);
    r.append(
      lineCell(row.type === 'add' ? '' : row.oldLine, reviewable),
      lineCell(row.type === 'del' ? '' : row.newLine, reviewable),
      markerCell(row.type),
      codeCell(rowHtml.has(row) ? { ...row, html: rowHtml.get(row) } : row),
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
    let leftRow = pair.left;
    let rightRow = pair.right;
    if (leftRow && rightRow && leftRow.type === 'del' && rightRow.type === 'add') {
      if (!leftRow.move && !rightRow.move) {
        const diff = diffLine(leftRow.text, rightRow.text);
        leftRow = { ...leftRow, html: diff.oldHtml };
        rightRow = { ...rightRow, html: diff.newHtml };
      }
    }
    if (leftRow?.move?.isFirst || rightRow?.move?.isFirst) {
      const hr = document.createElement('div');
      hr.className = 'diff-row-pair diff-move-header-pair';
      const leftHead = document.createElement('div');
      leftHead.className = 'diff-side diff-side-left' + (leftRow?.move?.isFirst ? ' diff-side-move-header' : ' diff-blank');
      if (leftRow?.move?.isFirst) {
        leftHead.dataset.moveId = leftRow.move.id;
        leftHead.append(createMoveHeader(leftRow.move, 'split'));
      }
      const rightHead = document.createElement('div');
      rightHead.className = 'diff-side diff-side-right' + (rightRow?.move?.isFirst ? ' diff-side-move-header' : ' diff-blank');
      if (rightRow?.move?.isFirst) {
        rightHead.dataset.moveId = rightRow.move.id;
        rightHead.append(createMoveHeader(rightRow.move, 'split'));
      }
      hr.append(leftHead, splitGutter(), rightHead);
      table.append(hr);
    }
    const r = document.createElement('div');
    r.className = 'diff-row-pair';
    r.append(splitSide(leftRow, 'left', reviewable), splitGutter(), splitSide(rightRow, 'right', reviewable));
    table.append(r);
  }
  return table;
}

function splitGutter() {
  const el = document.createElement('div');
  el.className = 'diff-split-gutter';
  return el;
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

    const hasMovedDel = dels.some(d => d.move);
    const hasMovedAdd = adds.some(a => a.move);

    if (hasMovedDel || hasMovedAdd) {
      let dIdx = 0, aIdx = 0;
      while (dIdx < dels.length || aIdx < adds.length) {
        const d = dels[dIdx];
        const a = adds[aIdx];
        if (d && a && d.move && a.move && d.move.id === a.move.id) {
          pairs.push({ left: d, right: a });
          dIdx++; aIdx++;
        } else if (d && d.move) {
          pairs.push({ left: d, right: null });
          dIdx++;
        } else if (a && a.move) {
          pairs.push({ left: null, right: a });
          aIdx++;
        } else if (d && a) {
          pairs.push({ left: d, right: a });
          dIdx++; aIdx++;
        } else if (d) {
          pairs.push({ left: d, right: null });
          dIdx++;
        } else if (a) {
          pairs.push({ left: null, right: a });
          aIdx++;
        }
      }
    } else {
      const n = Math.max(dels.length, adds.length);
      for (let k = 0; k < n; k++) pairs.push({ left: dels[k] || null, right: adds[k] || null });
    }
  }
  return pairs;
}

function splitSide(row, side, reviewable = true) {
  const el = document.createElement('div');
  el.className = 'diff-side diff-side-' + side + (row ? ' diff-' + row.type : ' diff-blank');
  if (row?.move) {
    el.classList.add('diff-moved-row', 'diff-moved-' + row.move.side);
    el.dataset.moveId = row.move.id;
    if (row.move.isFirst) el.classList.add('diff-moved-first');
    if (row.move.isLast) el.classList.add('diff-moved-last');
  }
  if (!row) { el.append(lineCell('', reviewable), markerCell(''), codeCell(null)); return el; }
  const ln = side === 'left' ? row.oldLine : row.newLine;
  anchor(el, row, reviewable);
  el.append(lineCell(ln, reviewable), markerCell(row.type), codeCell(row));
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
    btn.title = (S.meta?.pr && reviewable) ? 'Thread, review comment and line actions' : 'Thread and line actions';
    btn.textContent = 'Edit';
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

function codeCell(arg) {
  const el = document.createElement('div');
  el.className = 'diff-code';
  if (!arg) {
    el.innerHTML = '&nbsp;';
    return el;
  }
  if (typeof arg === 'string') {
    el.innerHTML = esc(arg) || '&nbsp;';
    return el;
  }
  el.innerHTML = arg.html || esc(arg.text || '') || '&nbsp;';
  return el;
}

function tokenize(str) {
  return str.match(/\w+|\s+|[^\w\s]/g) || [];
}

function myersDiff(aTokens, bTokens) {
  const n = aTokens.length, m = bTokens.length;
  if (n === 0 && m === 0) return [];
  if (n === 0) return bTokens.map(t => ({ type: 'add', val: t }));
  if (m === 0) return aTokens.map(t => ({ type: 'del', val: t }));

  const max = n + m;
  if (max > 400) return null; // Avoid deep search on massive minified lines

  const v = new Int32Array(2 * max + 1);
  const trace = [];

  for (let d = 0; d <= max; d++) {
    trace.push(v.slice(max - d, max + d + 1));
    for (let k = -d; k <= d; k += 2) {
      let x;
      if (k === -d || (k !== d && v[k - 1 + max] < v[k + 1 + max])) {
        x = v[k + 1 + max];
      } else {
        x = v[k - 1 + max] + 1;
      }
      let y = x - k;
      while (x < n && y < m && aTokens[x] === bTokens[y]) {
        x++;
        y++;
      }
      v[k + max] = x;
      if (x >= n && y >= m) {
        const script = [];
        let curX = n, curY = m;
        for (let step = d; step > 0; step--) {
          const prevV = trace[step];
          const curK = curX - curY;
          let prevK = (curK === -step || (curK !== step && prevV[curK - 1 + step] < prevV[curK + 1 + step]))
            ? curK + 1
            : curK - 1;
          const prevX = prevV[prevK + step];
          const prevY = prevX - prevK;

          while (curX > prevX && curY > prevY && aTokens[curX - 1] === bTokens[curY - 1]) {
            script.push({ type: 'eq', val: aTokens[curX - 1] });
            curX--; curY--;
          }
          if (curX > prevX) {
            script.push({ type: 'del', val: aTokens[curX - 1] });
            curX--;
          } else if (curY > prevY) {
            script.push({ type: 'add', val: bTokens[curY - 1] });
            curY--;
          }
        }
        while (curX > 0 && curY > 0) {
          script.push({ type: 'eq', val: aTokens[curX - 1] });
          curX--; curY--;
        }
        return script.reverse();
      }
    }
  }
  return null;
}

// Computes an intra-line diff using token Myers algorithm with sub-character refinement.
function diffLine(oldStr = '', newStr = '') {
  if (oldStr === newStr) {
    return { oldHtml: esc(oldStr), newHtml: esc(newStr) };
  }

  const script = myersDiff(tokenize(oldStr), tokenize(newStr));
  if (!script) {
    return diffLinePrefixSuffix(oldStr, newStr);
  }

  let oldHtml = '', newHtml = '';
  let i = 0;
  while (i < script.length) {
    if (script[i].type === 'eq') {
      oldHtml += esc(script[i].val);
      newHtml += esc(script[i].val);
      i++;
      continue;
    }
    let dels = '', adds = '';
    while (i < script.length && script[i].type === 'del') dels += script[i++].val;
    while (i < script.length && script[i].type === 'add') adds += script[i++].val;

    if (dels && adds) {
      // Sub-character diff between modified token clusters
      let start = 0;
      while (start < dels.length && start < adds.length && dels[start] === adds[start]) start++;
      let delEnd = dels.length - 1;
      let addEnd = adds.length - 1;
      while (delEnd >= start && addEnd >= start && dels[delEnd] === adds[addEnd]) {
        delEnd--; addEnd--;
      }
      const dMid = dels.slice(start, delEnd + 1);
      const aMid = adds.slice(start, addEnd + 1);

      const dSpan = dMid
        ? '<span class="diff-char-del">' + esc(dMid) + '</span>'
        : (aMid ? '<span class="diff-char-bar-del"></span>' : '');
      const aSpan = aMid
        ? '<span class="diff-char-add">' + esc(aMid) + '</span>'
        : (dMid ? '<span class="diff-char-bar-add"></span>' : '');

      oldHtml += esc(dels.slice(0, start)) + dSpan + esc(dels.slice(delEnd + 1));
      newHtml += esc(adds.slice(0, start)) + aSpan + esc(adds.slice(addEnd + 1));
    } else if (dels) {
      oldHtml += '<span class="diff-char-del">' + esc(dels) + '</span>';
      newHtml += '<span class="diff-char-bar-add"></span>';
    } else if (adds) {
      oldHtml += '<span class="diff-char-bar-del"></span>';
      newHtml += '<span class="diff-char-add">' + esc(adds) + '</span>';
    }
  }

  return { oldHtml, newHtml };
}

function diffLinePrefixSuffix(oldStr, newStr) {
  const oldLen = oldStr.length, newLen = newStr.length;
  let start = 0;
  while (start < oldLen && start < newLen && oldStr[start] === newStr[start]) start++;
  let oldEnd = oldLen - 1, newEnd = newLen - 1;
  while (oldEnd >= start && newEnd >= start && oldStr[oldEnd] === newStr[newEnd]) {
    oldEnd--; newEnd--;
  }
  const oldMid = oldStr.slice(start, oldEnd + 1);
  const newMid = newStr.slice(start, newEnd + 1);
  const dSpan = oldMid
    ? '<span class="diff-char-del">' + esc(oldMid) + '</span>'
    : (newMid ? '<span class="diff-char-bar-del"></span>' : '');
  const aSpan = newMid
    ? '<span class="diff-char-add">' + esc(newMid) + '</span>'
    : (oldMid ? '<span class="diff-char-bar-add"></span>' : '');
  return {
    oldHtml: esc(oldStr.slice(0, start)) + dSpan + esc(oldStr.slice(oldEnd + 1)),
    newHtml: esc(newStr.slice(0, start)) + aSpan + esc(newStr.slice(newEnd + 1)),
  };
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
  diffContent.addEventListener('mousedown', e => {
    if (e.button !== 0) return;
    if (e.target.closest('.line-btn') || e.target.closest('.diff-ln-nav')) return;
    const diffCode = e.target.closest('.diff-code');
    if (!diffCode) return;
    const w = wordAtPoint(e.clientX, e.clientY);
    if (!w) return;
    S.at = w;
    S.lastWord = w.word;
    const d = doc_();
    if (e[MOD]) {
      e.preventDefault();
      e.stopPropagation();
      if (d) pushHistory(d.path, w.line);
      // Plain click keeps the current mode; Alt flips it.
      const inDiff = !!d.diffMode;
      gotoDefinition(w, { view: (e.altKey ? !inDiff : inDiff) ? 'diff' : 'source' });
      return;
    }
  });

  // Clicking a line number jumps straight to the full file at that line --
  // the diff shows what changed, but reading it usually means seeing it in
  // context, not just the hunk. Clicking a symbol highlights and opens hover actions.
  diffContent.addEventListener('click', e => {
    const moveJump = e.target.closest('.diff-move-jump, .diff-move-bar, .diff-move-pill');
    if (moveJump) {
      e.stopPropagation();
      const targetLine = +(moveJump.dataset.targetLine || moveJump.closest('[data-target-line]')?.dataset.targetLine);
      const moveId = moveJump.dataset.moveId || moveJump.closest('[data-move-id]')?.dataset.moveId;
      const currentSide = moveJump.dataset.moveSide ||
                          moveJump.closest('[data-move-side]')?.dataset.moveSide ||
                          (moveJump.classList.contains('diff-move-bar-to') || moveJump.closest('.diff-move-bar-to') ? 'to' : 'from');
      const targetSide = currentSide === 'from' ? 'to' : 'from';

      const targetEl = diffview.querySelector(`.diff-side-${targetSide === 'from' ? 'left' : 'right'}.diff-side-move-header[data-move-id="${moveId}"]`) ||
                       diffview.querySelector(`.diff-moved-row.diff-moved-${targetSide}[data-move-id="${moveId}"]`) ||
                       (targetLine ? diffview.querySelector(`[data-l="${targetLine}"], [data-at="${targetLine}"], [data-old-l="${targetLine}"]`) : null);

      if (targetEl) {
        jumpAndFlash(targetEl, moveId, targetSide);
      } else if (targetLine) {
        scrollDiffToLine(targetLine, true, false);
      }
      return;
    }
    const step = e.target.closest('[data-commit-step]');
    if (step) {
      const d = doc_();
      const files = d && cachedCommitFiles(d.diffRef);
      const next = files?.[commitStepTarget(files, +step.closest('.diff-commit-strip').dataset.i, +step.dataset.commitStep)];
      if (!step.disabled && next && commitStepHandler) commitStepHandler(d, next);
      return;
    }
    if (e.target.closest('.line-btn')) return;
    const cell = e.target.closest('.diff-ln-nav');
    if (cell) {
      const rowEl = cell.closest('[data-l], [data-at]');
      if (!rowEl || !sourceJumpHandler) return;
      e.stopPropagation();
      const line = rowEl.dataset.l !== undefined ? +rowEl.dataset.l : +rowEl.dataset.at;
      sourceJumpHandler(line);
      return;
    }
    const diffCode = e.target.closest('.diff-code');
    if (diffCode && !e[MOD]) {
      const w = wordAtPoint(e.clientX, e.clientY);
      if (w) {
        S.at = w;
        S.lastWord = w.word;
        hoverAt(e.clientX, e.clientY);
      }
    }
  });

  diffContent.addEventListener('mouseover', e => {
    const moveEl = e.target.closest('[data-move-id]');
    const fromEl = e.relatedTarget?.closest?.('[data-move-id]');
    if (moveEl && moveEl.dataset.moveId !== fromEl?.dataset?.moveId) {
      setMoveHover(moveEl.dataset.moveId, true);
    }
  });

  diffContent.addEventListener('mouseout', e => {
    const moveEl = e.target.closest('[data-move-id]');
    const toEl = e.relatedTarget?.closest?.('[data-move-id]');
    if (moveEl && moveEl.dataset.moveId !== toEl?.dataset?.moveId) {
      setMoveHover(moveEl.dataset.moveId, false);
    }
  });

  window.addEventListener('resize', () => scheduleDrawMoveConnectors(), { passive: true });

  if (typeof ResizeObserver !== 'undefined') {
    const diffRo = new ResizeObserver(() => {
      if (diffview && !diffview.hidden) scheduleDrawMoveConnectors();
    });
    if (diffview) diffRo.observe(diffview);
    if (diffContent) diffRo.observe(diffContent);
    const editor = $('#editor');
    if (editor) diffRo.observe(editor);
  }

  // Live real-time tracking while dragging any UI resizer/sash
  window.addEventListener('mousemove', () => {
    if (diffview && !diffview.hidden && document.querySelector('.drag, #resizer.drag, #right-resizer.drag, [id$="-resizer"].drag')) {
      scheduleDrawMoveConnectors();
    }
  }, { passive: true });

  window.addEventListener('mouseup', () => {
    if (diffview && !diffview.hidden) {
      scheduleDrawMoveConnectors();
    }
  }, { passive: true });

  diffContent.addEventListener('dblclick', e => {
    const w = wordAtPoint(e.clientX, e.clientY);
    if (w) { S.at = w; S.lastWord = w.word; }
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
