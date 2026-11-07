// web/src/find.js
import { $, S, doc_, api, LH } from './state.js';
import { on } from './bus.js';
import { foldedLineY } from './folding.js';
import { vp } from './ui.js';
import { render, paint, getLH, lineTopY, lineAtScrollY, revealSourceLine } from './renderer.js';
import { centerLine } from './tabs.js';
import { updateStatus } from './status.js';
import { mdview, previewing, findInPreview, showPreviewHit, clearPreviewMarks, previewHitOffsets, scrollPreviewTo } from './markdown.js';
import { centerEditLine, editTextarea } from './editview.js';

export const findbar = $('#findbar');
export const findInput = $('#find-input');
const findCase = $('#find-case');

let findTimer = 0;

/* Text currently selected inside the editor viewport, reduced to its first
   non-empty line since find matches within a single line. */
function editorSelection() {
  const d = doc_();
  if (d?.isEditing && editTextarea) {
    const s = editTextarea.selectionStart;
    const e = editTextarea.selectionEnd;
    if (s !== undefined && e !== undefined && s !== e) {
      const txt = editTextarea.value.substring(s, e);
      const line = txt.split(/\r?\n/).find(l => l.trim());
      if (line) return line.trim();
    }
  }
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || !sel.rangeCount) return '';
  const at = sel.getRangeAt(0).commonAncestorContainer;
  if (!vp.contains(at) && !mdview.contains(at)) return '';
  const line = sel.toString().split(/\r?\n/).find(l => l.trim());
  return line ? line.trim() : '';
}

/* Seed priority: live editor selection, then the query already in an open
   findbar, then the caller's fallback (the last double-clicked word). */
export function openFind(seed) {
  const d = doc_();
  if (!d) return;
  const sel = editorSelection();
  if (sel) findInput.value = sel;
  else if (findbar.hidden && seed) findInput.value = seed;
  findbar.hidden = false;
  findInput.focus(); findInput.select();
  if (findInput.value && !d.isEditing) runFind();
}

export function clearFind() {
  clearTimeout(findTimer);
  findbar.hidden = true;
  S.find = null;
  $('#find-count').textContent = '0';
  $('#minimap-hits').innerHTML = '';
  clearPreviewMarks();
  paint();
}

export function isFindCurrent() {
  const cs = !!findCase?.classList?.contains('on');
  return !!(S.find && S.find.q === findInput.value && S.find.cs === cs);
}

export async function executeFind(jumpDelta = 0) {
  clearTimeout(findTimer);
  const d = doc_(); if (!d) return;
  const q = findInput.value;
  const cs = !!findCase?.classList?.contains('on');

  // The Markdown preview is searched as rendered text, in the page itself.
  if (previewing(d)) {
    const n = findInPreview(q, cs);
    const target = !n ? -1 : (jumpDelta < 0 ? n - 1 : (jumpDelta > 0 ? (jumpDelta % n) : 0));
    S.find = q ? { q, cs, hits: new Array(n).fill(null), byLine: new Set(), active: target, preview: true } : null;
    $('#find-count').textContent = !q ? '0' : n ? (target + 1) + ' / ' + n : 'no results';
    $('#minimap-hits').innerHTML = previewHitOffsets().map(p => '<i style="top:' + p + '%"></i>').join('');
    if (n) jumpToHit(target);
    return;
  }
  if (!q) {
    S.find = null;
    $('#find-count').textContent = '0';
    $('#minimap-hits').innerHTML = '';
    paint();
    return;
  }

  if (d.isEditing && editTextarea) {
    const text = editTextarea.value;
    const lines = text.split('\n');
    const needle = cs ? q : q.toLowerCase();
    const hits = [];
    let charOffset = 0;
    for (let li = 0; li < lines.length; li++) {
      const lineStr = lines[li];
      const haystack = cs ? lineStr : lineStr.toLowerCase();
      let col = 0;
      let n = 0;
      while ((col = haystack.indexOf(needle, col)) !== -1) {
        hits.push({
          line: li + 1,
          n,
          start: charOffset + col,
          end: charOffset + col + q.length,
        });
        n++;
        col += needle.length;
      }
      charOffset += lineStr.length + 1;
    }
    const n = hits.length;
    const target = !n ? -1 : (jumpDelta < 0 ? n - 1 : (jumpDelta > 0 ? (jumpDelta % n) : 0));
    S.find = { q, cs, hits, byLine: new Set(hits.map(h => h.line)), active: target, editing: true };
    $('#find-count').textContent = n ? (target + 1) + ' / ' + n : 'no results';
    drawMinimap(hits, lines.length);
    if (n) jumpToHit(target);
    return;
  }

  let j;
  try { j = await api('/api/search', { q, glob: d.path, case: cs ? 1 : '' }); } catch { return; }
  const f = (j.results || []).find(r => r.path === d.path);
  const hits = [];
  if (f) {
    let prevLine = -1, n = 0;
    for (const m of f.matches) {
      n = m.line === prevLine ? n + 1 : 0;
      prevLine = m.line;
      hits.push({ line: m.line, n });
    }
  }
  const n = hits.length;
  const target = !n ? -1 : (jumpDelta < 0 ? n - 1 : (jumpDelta > 0 ? (jumpDelta % n) : 0));
  S.find = { q, cs, hits, byLine: new Set(hits.map(h => h.line)), active: target };
  $('#find-count').textContent = n ? (target + 1) + ' / ' + n : 'no results';
  drawMinimap(hits, d.total);
  if (n) jumpToHit(target); else paint();
}

export function runFind() {
  clearTimeout(findTimer);
  findTimer = setTimeout(() => executeFind(0), 140);
}

export function drawMinimap(hits, total) {
  const mm = $('#minimap-hits');
  if (!hits.length || !total) { mm.innerHTML = ''; return; }
  const seenBuckets = new Set();
  const parts = [];
  const d = doc_();
  const foldedHeight = d?.folds?.spans.length && !d.isEditing ? Math.max(1, lineTopY(total + 1, d)) : 0;
  for (let i = 0; i < hits.length; i++) {
    const h = hits[i];
    const ratio = foldedHeight
      ? foldedLineY(d.folds, h.line, S.wrap ? d._wrap?.offsets : null, getLH()) / foldedHeight
      : (h.line - 1) / total;
    const bucket = Math.round(ratio * 400);
    if (seenBuckets.has(bucket)) continue;
    seenBuckets.add(bucket);
    parts.push('<i style="top:' + (ratio * 100).toFixed(2) + '%"></i>');
  }
  mm.innerHTML = parts.join('');
}

export function jumpToHit(i) {
  const d = doc_(); if (!d || !S.find || !S.find.hits.length) return;
  const n = S.find.hits.length;
  S.find.active = ((i % n) + n) % n;
  if (S.find.preview) {
    $('#find-count').textContent = (S.find.active + 1) + ' / ' + n;
    showPreviewHit(S.find.active);
    return;
  }
  const h = S.find.hits[S.find.active];
  d.cur = h.line;
  $('#find-count').textContent = (S.find.active + 1) + ' / ' + n;
  if (d.isEditing && editTextarea) {
    centerEditLine(h.line, h.start, h.end);
    updateStatus();
    return;
  }
  const lh = getLH();
  revealSourceLine(h.line, d);
  const y = lineTopY(h.line, d);
  if (y < vp.scrollTop + lh * 2 || y > vp.scrollTop + vp.clientHeight - lh * 3) centerLine(h.line);
  render(); updateStatus();
}

export function findNextMatch(delta = 1) {
  if (!isFindCurrent()) {
    if (findInput.value) {
      executeFind(delta < 0 ? -1 : 0);
      return;
    }
    return;
  }
  jumpToHit(S.find.active + delta);
}

function onFindInput() {
  const d = doc_();
  if (d?.isEditing) {
    clearTimeout(findTimer);
    if (!findInput.value) {
      S.find = null;
      $('#find-count').textContent = '0';
      $('#minimap-hits').innerHTML = '';
    } else if (S.find && (S.find.q !== findInput.value || S.find.cs !== !!findCase?.classList?.contains('on'))) {
      S.find = null;
      $('#find-count').textContent = '—';
      $('#minimap-hits').innerHTML = '';
    }
    return;
  }
  runFind();
}

export function initFind() {
  findInput.addEventListener('input', onFindInput);
  findInput.addEventListener('keydown', e => {
    if (e.key === 'Enter') {
      e.preventDefault();
      e.stopPropagation();
      findNextMatch(e.shiftKey ? -1 : 1);
    }
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      clearFind();
      const d = doc_();
      if (d?.isEditing && editTextarea) editTextarea.focus();
      else vp.focus();
    }
  });
  findCase.addEventListener('click', () => {
    findCase.classList.toggle('on');
    const d = doc_();
    if (d?.isEditing) {
      if (S.find) executeFind(0);
    } else {
      runFind();
    }
  });
  $('#find-next').addEventListener('click', () => findNextMatch(1));
  $('#find-prev').addEventListener('click', () => findNextMatch(-1));
  $('#find-close').addEventListener('click', clearFind);
  $('#minimap-hits').addEventListener('click', e => {
    const r = $('#minimap-hits').getBoundingClientRect();
    const d = doc_(); if (!d || !r.height || !d.total) return;
    if (previewing(d)) { scrollPreviewTo((e.clientY - r.top) / r.height); return; }
    const ratio = (e.clientY - r.top) / r.height;
    const line = d.folds?.spans.length && !d.isEditing
      ? lineAtScrollY(ratio * lineTopY(d.total + 1, d), d)
      : Math.max(1, Math.round(ratio * d.total));
    centerLine(line);
    if (!d.isEditing) render();
  });
  on('tab:activated', clearFind);
  on('folds:changed', () => { if (S.find && !S.find.preview) drawMinimap(S.find.hits, doc_().total); });
}
