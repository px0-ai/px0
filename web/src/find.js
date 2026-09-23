// web/src/find.js
import { $, S, doc_, api, debounce, LH } from './state.js';
import { vp } from './ui.js';
import { render, paint } from './renderer.js';
import { centerLine } from './tabs.js';
import { updateStatus } from './status.js';
import { mdview, previewing, findInPreview, showPreviewHit, clearPreviewMarks, previewHitOffsets, scrollPreviewTo } from './markdown.js';

export const findbar = $('#findbar');
export const findInput = $('#find-input');

/* Text currently selected inside the editor viewport, reduced to its first
   non-empty line since find matches within a single line. */
function editorSelection() {
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
  if (!doc_()) return;
  const sel = editorSelection();
  if (sel) findInput.value = sel;
  else if (findbar.hidden && seed) findInput.value = seed;
  findbar.hidden = false;
  findInput.focus(); findInput.select();
  if (findInput.value) runFind();
}

export function clearFind() {
  findbar.hidden = true;
  S.find = null;
  $('#find-count').textContent = '0';
  clearPreviewMarks();
  renderMinimap();
  paint();
}

export const runFind = debounce(async () => {
  const d = doc_(); if (!d) return;
  const q = findInput.value;
  // The Markdown preview is searched as rendered text, in the page itself.
  if (previewing(d)) {
    const n = findInPreview(q);
    S.find = q ? { q, ci: false, hits: new Array(n).fill(null), byLine: new Set(), active: n ? 0 : -1, preview: true } : null;
    $('#find-count').textContent = !q ? '0' : n ? '1 / ' + n : 'no results';
    $('#minimap-hits').innerHTML = previewHitOffsets().map(p => '<i style="top:' + p + '%"></i>').join('');
    if (n) jumpToHit(0);
    return;
  }
  if (!q) { S.find = null; $('#find-count').textContent = '0'; renderMinimap(); paint(); return; }
  let j;
  try { j = await api('/api/search', { q, glob: d.path }); } catch { return; }
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
  S.find = { q, ci: false, hits, byLine: new Set(hits.map(h => h.line)), active: hits.length ? 0 : -1 };
  $('#find-count').textContent = hits.length ? '1 / ' + hits.length : 'no results';
  renderMinimap();
  if (hits.length) jumpToHit(0); else paint();
}, 140);

export function drawMinimap(hits, total) {
  // Legacy entry point: find hits are already on S.find, so just re-render
  // the merged (find + occurrence) minimap. Kept for external callers.
  void hits; void total;
  renderMinimap();
}

/* Merged GitHub-style scrollbar markers: solid find hits plus orange
   occurrence dots for the double-clicked word. Occ dots are skipped when a
   find query already shows the same word, so the two layers never double up. */
export function renderMinimap() {
  const mm = $('#minimap-hits');
  if (!mm) return;
  const d = doc_();
  if (!d) { mm.innerHTML = ''; return; }
  let html = '';
  if (S.find && S.find.hits && S.find.hits.length && !S.find.preview) {
    const seen = new Set();
    for (const h of S.find.hits) {
      if (!h || h.line == null || seen.has(h.line)) continue;
      seen.add(h.line);
      html += '<i style="top:' + ((h.line - 1) / d.total * 100).toFixed(3) + '%"></i>';
    }
  }
  const sameAsFind = S.find && S.find.hits && S.find.hits.length && S.find.q === S.occ;
  if (S.occ && S.occHits && S.occHits.length && !sameAsFind) {
    const seen = new Set();
    for (const h of S.occHits) {
      if (seen.has(h.line)) continue;
      seen.add(h.line);
      html += '<i class="occ" data-l="' + h.line + '" title="' + h.line + '" style="top:' + ((h.line - 1) / d.total * 100).toFixed(3) + '%"></i>';
    }
  }
  mm.innerHTML = html;
}

/* Whether the double-clicked word should highlight at all. Defaults to true. */
export function occEnabled() {
  const v = S.settings && S.settings['editor.occurrencesHighlight'];
  return v !== false && v !== 'false';
}

let occSeq = 0;

/* Recompute the whole-file line set for S.occ via the server index (case-
   sensitive substring, matching the viewport's markNodes(..., true, 'occ')),
   then re-render the merged minimap. Stale responses are dropped. */
export async function refreshOccMinimap() {
  const d = doc_();
  const word = S.occ;
  const my = ++occSeq;
  if (!word || !d || !occEnabled()) {
    if (S.occHits) { S.occHits = null; renderMinimap(); }
    else renderMinimap();
    return;
  }
  let j;
  try { j = await api('/api/search', { q: word, glob: d.path, case: 1 }); } catch { return; }
  if (my !== occSeq || S.occ !== word || doc_() !== d) return;
  const f = (j.results || []).find(r => r.path === d.path);
  if (!f) { S.occHits = []; renderMinimap(); return; }
  const seen = new Set();
  const hits = [];
  for (const m of f.matches) {
    if (seen.has(m.line)) continue;
    seen.add(m.line);
    hits.push({ line: m.line });
    if (hits.length >= 5000) break; // cap DOM nodes on pathological words
  }
  S.occHits = hits;
  renderMinimap();
}

/* Clear the occurrence highlight and its minimap markers, keeping any
   active find markers intact. */
export function clearOcc() {
  if (!S.occ && !S.occHits) return;
  occSeq++;
  S.occ = null;
  S.occHits = null;
  renderMinimap();
  paint();
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
  const y = (h.line - 1) * LH;
  if (y < vp.scrollTop + LH * 2 || y > vp.scrollTop + vp.clientHeight - LH * 3) centerLine(h.line);
  $('#find-count').textContent = (S.find.active + 1) + ' / ' + n;
  render(); updateStatus();
}

export function findNextMatch(delta = 1) {
  if (!S.find || !S.find.hits || !S.find.hits.length) {
    if (findInput.value) { runFind(); return; }
    return;
  }
  jumpToHit(S.find.active + delta);
}

export function initFind() {
  findInput.addEventListener('input', runFind);
  findInput.addEventListener('keydown', e => {
    if (e.key === 'Enter') { e.preventDefault(); jumpToHit(S.find ? S.find.active + (e.shiftKey ? -1 : 1) : 0); }
    if (e.key === 'Escape') { clearFind(); vp.focus(); }
  });
  $('#find-next').addEventListener('click', () => jumpToHit(S.find ? S.find.active + 1 : 0));
  $('#find-prev').addEventListener('click', () => jumpToHit(S.find ? S.find.active - 1 : 0));
  $('#find-close').addEventListener('click', clearFind);
  $('#minimap-hits').addEventListener('click', e => {
    const r = $('#minimap-hits').getBoundingClientRect();
    const d = doc_(); if (!d) return;
    if (previewing(d)) { scrollPreviewTo((e.clientY - r.top) / r.height); return; }
    centerLine(Math.round((e.clientY - r.top) / r.height * d.total));
    render();
  });
}
