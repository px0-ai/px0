// web/src/renderer.js
import { $, S, doc_, api, esc, LH, CHUNK, OVERSCAN } from './state.js';
import { vp, sizer, rowsEl, editor } from './ui.js';
import { foldIndex, foldLineToRow, foldRowToLine, visibleFoldLine, nextFoldLine, revealFoldLine, updateFoldPixels, foldedLineY, unfoldedY } from './folding.js';
import { loadDocFolds } from './folding-ui.js';
import { emit } from './bus.js';

export function getLH() {
  return S.lh || LH;
}

export function measure() {
  const m = $('#measure');
  m.textContent = 'x'.repeat(100);
  const r = m.getBoundingClientRect();
  S.chW = r.width / 100 || 7.8;
  const cssLH = typeof getComputedStyle === 'function'
    ? parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--lh'))
    : 0;
  S.lh = cssLH || Math.round(r.height) || LH;
}

function bottomPad() {
  return Math.max(120, (vp.clientHeight || 0) * 0.5);
}

function htmlTextLen(s) {
  if (!s) return 0;
  if (s.indexOf('<') < 0 && s.indexOf('&') < 0 && s.indexOf('\t') < 0) return s.length;
  let len = 0, inTag = false;
  for (let i = 0; i < s.length; i++) {
    const ch = s.charCodeAt(i);
    if (inTag) {
      if (ch === 62) inTag = false; // '>'
    } else if (ch === 60) { // '<'
      inTag = true;
    } else if (ch === 38) { // '&'
      len++;
      const semi = s.indexOf(';', i + 1);
      if (semi > i && semi - i <= 8) i = semi;
    } else if (ch === 9) { // '\t'
      len += 4 - (len % 4);
    } else {
      len++;
    }
  }
  return len;
}

function ensureLineCols(d) {
  const total = d.total || 0;
  if (!d._lineCols || d._lineCols.length !== total) {
    d._lineCols = new Int32Array(total);
    d._lineCols.fill(-1);
    d._maxLineCols = d.maxCols || 0;
    d._colsChunks = new Set();
  }
  if (d.chunks && d.lines) {
    for (const c of d.chunks) {
      if (d._colsChunks.has(c)) continue;
      d._colsChunks.add(c);
      const start = c * CHUNK;
      const end = Math.min(total, start + CHUNK);
      for (let i = start; i < end; i++) {
        if (d.lines[i] === undefined) continue;
        const cols = htmlTextLen(d.lines[i]);
        d._lineCols[i] = cols;
        if (cols > (d._maxLineCols || 0)) d._maxLineCols = cols;
      }
    }
  }
}

function recomputeOffsets(w, total) {
  const h = w.heights, off = w.offsets;
  let sum = 0;
  off[0] = 0;
  for (let i = 0; i < total; i++) {
    sum += h[i];
    off[i + 1] = sum;
  }
  w.dirtyOffsets = false;
  w.version = (w.version || 0) + 1;
}

export function ensureWrapMap(d) {
  if (!d) return null;
  const total = d.total || 0;
  const lh = getLH();
  const chW = S.chW || 7.8;
  const digits = String(total || 1).length;
  const gutter = S.lineNumbers ? (digits * chW + 30) : 0;
  const vpW = vp.clientWidth || (d._wrap && d._wrap.vpW) || 800;
  const codeW = Math.max(40, vpW - gutter - 40);
  const colsPerLine = Math.max(1, Math.floor(codeW / chW));

  ensureLineCols(d);

  let w = d._wrap;
  const widthChanged = !w || (vp.clientWidth > 0 && Math.abs(w.codeW - codeW) > 1);
  if (!w || w.heights.length !== total || widthChanged || w.lh !== lh || Math.abs(w.chW - chW) > 0.05) {
    const avgCols = (total > 0 && d.size > 0) ? Math.round(d.size / total) : 0;
    const defaultRows = avgCols > colsPerLine ? Math.max(1, Math.ceil(avgCols / colsPerLine)) : 1;
    const defaultH = defaultRows * lh;
    const heights = new Float64Array(total);
    heights.fill(defaultH);
    w = {
      vpW,
      codeW,
      lh,
      chW,
      colsPerLine,
      heights,
      measured: new Uint8Array(total),
      chunkEst: new Set(),
      offsets: new Float64Array(total + 1),
      dirtyOffsets: true,
    };
    d._wrap = w;
  }

  if (d.chunks && d.lines) {
    for (const c of d.chunks) {
      if (w.chunkEst.has(c)) continue;
      w.chunkEst.add(c);
      const start = c * CHUNK;
      const end = Math.min(total, start + CHUNK);
      for (let i = start; i < end; i++) {
        const cols = d._lineCols[i];
        if (cols < 0) continue;
        if (!w.measured[i]) {
          const rows = cols > colsPerLine ? Math.ceil(cols / colsPerLine) : 1;
          const estH = rows * lh;
          if (w.heights[i] !== estH) {
            w.heights[i] = estH;
            w.dirtyOffsets = true;
          }
        }
      }
    }
  }

  if (w.dirtyOffsets) {
    recomputeOffsets(w, total);
  }
  return w;
}

function lineIndexAtY(w, total, y) {
  if (total <= 0 || y <= 0) return 0;
  const off = w.offsets;
  if (y >= off[total]) return total - 1;
  let lo = 0, hi = total - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >>> 1;
    if (off[mid + 1] <= y) lo = mid + 1;
    else if (off[mid] > y) hi = mid - 1;
    else return mid;
  }
  return Math.max(0, Math.min(total - 1, lo));
}

function foldGeometry(d, w, lh) {
  const f = d.folds;
  if (f?.spans.length) updateFoldPixels(f, w?.offsets || null, w?.version || 0, lh);
  return f?.spans.length ? f : null;
}

export function revealSourceLine(line, d = doc_()) {
  if (!d || !revealFoldLine(d.folds, line)) return;
  layout();
  emit('folds:changed', { doc: d });
}

export function lineTopY(line, d = doc_()) {
  const lh = getLH();
  if (!d || !d.total) return Math.max(0, ((line || 1) - 1) * lh);
  const n = Math.max(1, Math.min(d.total + 1, line || 1));
  const w = S.wrap ? ensureWrapMap(d) : null;
  const f = foldGeometry(d, w, lh);
  if (f) return foldedLineY(f, n, w?.offsets || null, lh);
  return w ? w.offsets[n - 1] : (n - 1) * lh;
}

export function lineHeightAt(line, d = doc_()) {
  const lh = getLH();
  if (!d || !d.total || !S.wrap) return lh;
  const idx = Math.max(0, Math.min(d.total - 1, (line || 1) - 1));
  const w = ensureWrapMap(d);
  return w ? w.heights[idx] : lh;
}

export function lineAtScrollY(y, d = doc_()) {
  const lh = getLH();
  if (!d || !d.total) return Math.max(1, Math.floor(Math.max(0, y) / lh) + 1);
  const w = S.wrap ? ensureWrapMap(d) : null;
  const f = w ? foldGeometry(d, w, lh) : d.folds?.spans.length ? d.folds : null;
  if (w) {
    const rawY = f ? unfoldedY(f, Math.max(0, y), w.offsets, lh) : y;
    return visibleFoldLine(f, lineIndexAtY(w, d.total, rawY) + 1);
  }
  // Clamp in visible coordinates before mapping: the file's last line may be hidden.
  const rows = f ? foldLineToRow(f, d.total + 1) - 1 : d.total;
  return foldRowToLine(f, Math.max(1, Math.min(rows, Math.floor(Math.max(0, y) / lh) + 1)));
}

export function layout() {
  invalidatePaint();
  const d = doc_();
  if (!d) return;
  const lh = getLH();
  const digits = String(d.total).length;
  editor.style.setProperty('--gw', digits);
  const gutter = digits * S.chW + 30;
  if (S.wrap) {
    sizer.style.width = '100%';
    rowsEl.style.width = '100%';
    if (!sizer.offsetHeight && d.total > 0) {
      sizer.style.height = (lineTopY(d.total + 1, d) + bottomPad()) + 'px';
    }
    const w = ensureWrapMap(d);
    sizer.style.height = (lineTopY(d.total + 1, d) + bottomPad()) + 'px';
  } else {
    ensureLineCols(d);
    const maxCols = Math.max(d.maxCols || 0, d._maxLineCols || 0);
    const w = Math.max(vp.clientWidth, gutter + (maxCols + 4) * S.chW);
    sizer.style.height = (lineTopY(d.total + 1, d) + bottomPad()) + 'px';
    sizer.style.width = w + 'px';
    rowsEl.style.width = w + 'px';
  }
}

export function toggleWordWrap(forced) {
  const d = doc_();
  const topLine = d ? lineAtScrollY(vp.scrollTop, d) : 1;
  S.wrap = typeof forced === 'boolean' ? forced : !S.wrap;
  document.body.classList.toggle('word-wrap', S.wrap);
  try { localStorage.setItem('px0.wrap', S.wrap ? 'true' : 'false'); } catch {}
  updateEditorOptionControls();
  layout();
  if (d) vp.scrollTop = lineTopY(topLine, d);
  render();
}

export function toggleLineNumbers(forced) {
  S.lineNumbers = typeof forced === 'boolean' ? forced : !S.lineNumbers;
  document.body.classList.toggle('hide-lines', !S.lineNumbers);
  layout();
  render();
}

export function applyEditorTypography(fontSize, fontFamily, lineHeight, tabSize) {
  if (fontSize) document.documentElement.style.setProperty('--fs', fontSize + 'px');
  if (fontFamily) document.documentElement.style.setProperty('--mono', fontFamily);
  if (lineHeight) {
    document.documentElement.style.setProperty('--lh', lineHeight + 'px');
  } else if (fontSize) {
    document.documentElement.style.setProperty('--lh', Math.round(fontSize * 1.5) + 'px');
  }
  if (tabSize) document.documentElement.style.setProperty('--tab-size', tabSize);
  measure();
  layout();
  render();
}

export function updateEditorOptionControls() {
  const wrapBtn = $('[data-action="wrap"]');
  if (wrapBtn) wrapBtn.classList.toggle('active', !!S.wrap);
}

let raf = 0;
let renderedFirst = 0;
let lastPaintKey = '';
export function invalidatePaint() {
  lastPaintKey = '';
}
export function render() {
  if (raf) return;
  raf = requestAnimationFrame(() => { raf = 0; paint(); });
}

export function paint() {
  const d = doc_();
  if (!d) { const c = $('#caret'); if (c) c.hidden = true; return; }
  const lh = getLH();
  loadDocFolds(d);
  const folded = !!d.folds?.spans.length;
  const top = vp.scrollTop;
  let first, last, anchorLine = 0, w = null;
  if (folded && d.total > 0) {
    w = S.wrap ? ensureWrapMap(d) : null;
    anchorLine = lineAtScrollY(top, d) - 1;
    first = lineAtScrollY(Math.max(0, top - OVERSCAN * lh), d) - 1;
    last = Math.min(d.total, nextFoldLine(d.folds, lineAtScrollY(top + (vp.clientHeight || 600) + OVERSCAN * lh, d)));
  } else if (S.wrap && d.total > 0) {
    w = ensureWrapMap(d);
    anchorLine = lineIndexAtY(w, d.total, top);
    const overscanPx = OVERSCAN * lh;
    const minY = Math.max(0, top - overscanPx);
    const maxY = top + (vp.clientHeight || 600) + overscanPx;
    first = lineIndexAtY(w, d.total, minY);
    if (first === anchorLine && first > 0) first--;
    last = Math.min(d.total, lineIndexAtY(w, d.total, maxY) + 2);
    if (last <= first) last = Math.min(d.total, first + 1);
  } else {
    first = Math.max(0, Math.floor(top / lh) - OVERSCAN);
    const count = Math.ceil((vp.clientHeight || 600) / lh) + OVERSCAN * 2;
    last = Math.min(d.total, first + count);
  }
  if (!folded) ensureChunks(d, first, last);

  const gut = d.gutter || null;
  const agentRanges = (S.agentTargets || []).filter(t => t.path === d.path);
  const agentKey = agentRanges.map(r => r.l1 + ':' + r.l2).join(',');
  const findKey = S.find && S.find.hits.length ? S.find.q + ':' + S.find.cs + ':' + S.find.active + ':' + S.find.hits.length : '';
  const linkKey = S.link ? S.link.line + ':' + S.link.col + ':' + S.link.word : '';
  const gutKey = gut ? gut.marks.size + ':' + gut.dels.size : '';
  const diagList = Array.isArray(d.diagnostics) ? d.diagnostics : [];
  const diagKey = diagList.length ? diagList.length + ':' + diagList[0].line + ':' + diagList[0].message : '';
  const paintKey = [
    d.path, d.gen || 0, d.folds?.generation || 0, d.foldLoaded ? 1 : 0, first, last, d.cur, S.occ || '',
    findKey, linkKey, gutKey, diagKey, agentKey, S.selAll === d ? 1 : 0, S.wrap ? 1 : 0
  ].join('|');

  if (paintKey === lastPaintKey) {
    const y0 = folded ? lineTopY(first + 1, d) : w ? w.offsets[first] : first * lh;
    rowsEl.style.transform = 'translateY(' + y0 + 'px)';
    placeCaret();
    return;
  }
  lastPaintKey = paintKey;
  renderedFirst = first;

  const diagByLine = new Map();
  for (const dg of diagList) {
    if (!dg || !dg.line) continue;
    const prev = diagByLine.get(dg.line);
    if (!prev || (dg.severity || 1) < (prev.severity || 1)) diagByLine.set(dg.line, dg);
  }

  let html = '';
  for (let i = first; i < last; i = folded ? nextFoldLine(d.folds, i + 1) - 1 : i + 1) {
    const n = i + 1;
    if (folded) ensureChunks(d, i, i + 1);
    const fold = foldIndex(d.folds, n);
    const closed = fold >= 0 && d.folds.closed[fold];
    const foldButton = fold < 0 ? '' : '<button type="button" class="fold-toggle" data-fold="' + n + '" aria-label="' + (closed ? 'Expand' : 'Collapse') + ' block at line ' + n + '" aria-expanded="' + !closed + '">' + (closed ? '▸' : '▾') + '</button>';
    const summary = !closed ? '' : '<button type="button" class="fold-summary" data-fold="' + n + '" aria-label="Expand hidden lines">… ' + (d.folds.ranges[fold * 2 + 1] - n) + ' lines</button>';
    const body = d.lines[i];
    let rc = closed ? 'row folded' : 'row', gc = 'g';
    if (n === d.cur) rc += ' cur';
    if (agentRanges.some(r => n >= r.l1 && n <= r.l2)) rc += ' agent-sel';
    if (agentRanges.some(r => n === r.l1)) rc += ' agent-anchor';
    if (gut) {
      const m = gut.marks.get(n);
      if (m) gc += m === 'add' ? ' gut-add' : ' gut-mod';
      if (gut.dels.has(n)) rc += ' gut-del';
    }
    const dg = diagByLine.get(n);
    let cAttr = '';
    if (dg) {
      const isErr = (dg.severity || 1) === 1;
      rc += isErr ? ' row-has-err' : ' row-has-warn';
      gc += isErr ? ' gut-diag-err' : ' gut-diag-warn';
      const label = '  — ' + (dg.source ? dg.source + ': ' : '') + (dg.message || '').split('\n')[0];
      cAttr = ' data-diag="' + esc(label) + '" title="' + esc(dg.message || '') + '"';
    }
    html += '<div class="' + rc + '" data-l="' + n + '">' +
      '<div class="' + gc + '"><span class="line-btn" role="button" data-l="' + n + '" title="Thread and line actions">Edit</span>' + n + foldButton + '</div><div class="c"' + cAttr + '>' + (body === undefined ? '' : body) + '</div>' + summary + '</div>';
  }
  const sel = saveSelection();
  const y0 = folded ? lineTopY(first + 1, d) : w ? w.offsets[first] : first * lh;
  rowsEl.style.transform = 'translateY(' + y0 + 'px)';
  rowsEl.innerHTML = html;
  rowsEl.classList.toggle('all', S.selAll === d);
  decorate(first, last);
  if (w && vp.clientWidth > 0) {
    let changed = false;
    let deltaAbove = 0;
    const children = rowsEl.children;
    for (let k = 0; k < children.length; k++) {
      const idx = +children[k].dataset.l - 1;
      if (idx >= d.total) break;
      if (d.lines[idx] === undefined) continue;
      const actualH = /** @type {HTMLElement} */ (children[k]).offsetHeight;
      if (actualH > 0) {
        w.measured[idx] = 1;
        const oldH = w.heights[idx];
        if (actualH !== oldH) {
          w.heights[idx] = actualH;
          changed = true;
          if (idx < anchorLine) {
            deltaAbove += actualH - oldH;
          }
        }
      }
    }
    if (changed) {
      recomputeOffsets(w, d.total);
      sizer.style.height = (lineTopY(d.total + 1, d) + bottomPad()) + 'px';
      if (deltaAbove !== 0 && top > 0) {
        vp.scrollTop = Math.max(0, top + deltaAbove);
      }
    }
  } else if (!S.wrap && vp.clientWidth > 0 && rowsEl.scrollWidth > sizer.offsetWidth) {
    const sw = rowsEl.scrollWidth;
    sizer.style.width = sw + 'px';
    rowsEl.style.width = sw + 'px';
  }
  if (sel) restoreSelection(sel);
  placeCaret();
}

let caretKey = '';

/* Position the caret at d.cur / d.col (UTF-16 units into the line's text,
   clamped to its length). It lives in #sizer rather than inside a row: rows are
   rewritten on every paint, and their text nodes are what selection restore and
   word lookup measure. Returns the caret's x within #sizer, or null if hidden. */
export function placeCaret() {
  const el = $('#caret');
  if (!el) return null;
  const d = doc_();
  const row = d && rowFor(d.cur);
  if (!row) { el.hidden = true; return null; }
  const code = $('.c', row);
  if (!code) { el.hidden = true; return null; }
  const col = Math.max(0, Math.min(d.col || 0, code.textContent.length));
  const [node, off] = toPoint({ line: d.cur, col });
  const base = sizer.getBoundingClientRect();
  let x, y;
  if (node.nodeType === 3) {
    const r = document.createRange();
    r.setStart(node, off);
    r.collapse(true);
    const rect = r.getClientRects()[0] || r.getBoundingClientRect();
    x = rect.left;
    const lh = getLH();
    // Wrapped rows are taller than one line; otherwise pin to the row's top.
    y = S.wrap ? rect.top - (lh - rect.height) / 2 : row.getBoundingClientRect().top;
  } else {
    const cr = code.getBoundingClientRect();
    x = cr.left + parseFloat(getComputedStyle(code).paddingLeft || '0');
    y = cr.top;
  }
  // Scrolled horizontally under the sticky gutter: hide rather than draw over it.
  const g = $('.g', row);
  if (g && x < g.getBoundingClientRect().right - 1) { el.hidden = true; return null; }
  el.style.transform = 'translate(' + (x - base.left) + 'px,' + (y - base.top) + 'px)';
  el.hidden = false;
  const key = d.path + ':' + d.cur + ':' + col;
  if (key !== caretKey) {
    caretKey = key;
    el.classList.remove('blink');
    void el.offsetWidth; // restart the blink so a moving caret stays solid
    el.classList.add('blink');
  }
  return x - base.left;
}

/* Rewriting the rows destroys any live DOM selection, and paint runs on far
   more than scrolls: pressing Ctrl to underline a link, a double-click, a
   background highlight refresh. Carry the selection across as line/column
   positions so Ctrl+C still has something to copy. */
function saveSelection() {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || !sel.rangeCount) return null;
  if (!rowsEl.contains(sel.getRangeAt(0).commonAncestorContainer)) return null;
  const a = toPos(sel.anchorNode, sel.anchorOffset);
  const f = toPos(sel.focusNode, sel.focusOffset);
  return a && f ? { a, f } : null;
}

function restoreSelection({ a, f }) {
  const pa = toPoint(a), pf = toPoint(f);
  if (pa && pf) window.getSelection().setBaseAndExtent(pa[0], pa[1], pf[0], pf[1]);
}

/* DOM boundary point -> { line, col } with col counted in the line's text. */
export function toPos(node, off) {
  if (node === rowsEl) {
    const row = rowsEl.children[off] || rowsEl.lastElementChild;
    if (!row) return null;
    const atEnd = !rowsEl.children[off];
    return { line: +row.dataset.l, col: atEnd ? $('.c', row).textContent.length : 0 };
  }
  const el = node.nodeType === 1 ? node : node.parentElement;
  const row = el && el.closest('.row');
  if (!row || !rowsEl.contains(row)) return null;
  const code = $('.c', row);
  const r = document.createRange();
  r.selectNodeContents(code);
  const cmp = r.comparePoint(node, off);
  if (cmp < 0) return { line: +row.dataset.l, col: 0 };
  if (cmp > 0) return { line: +row.dataset.l, col: code.textContent.length };
  r.setEnd(node, off);
  return { line: +row.dataset.l, col: r.toString().length };
}

/* { line, col } -> DOM boundary point in the freshly painted rows, or null when
   that line has scrolled out of the rendered window. */
export function toPoint({ line, col }) {
  const row = rowFor(line);
  if (!row) return null;
  const code = $('.c', row);
  if (!code) return null;
  const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
  let at = 0;
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const len = n.nodeValue.length;
    if (col <= at + len) return [n, col - at];
    at += len;
  }
  return [code, code.childNodes.length];
}

/* Decorations are applied to the ~60 live rows only, never to the whole file. */
export function decorate(first, last) {
  const d = doc_();
  if (S.occ) {
    for (const row of rowsEl.children) markNodes($('.c', row), S.occ, true, 'occ');
  }
  if (S.link) {
    const row = rowFor(S.link.line);
    if (row) wrapRange($('.c', row), S.link.col, S.link.col + S.link.word.length, 'link');
  }
  if (S.find && S.find.hits.length) {
    const byLine = S.find.byLine;
    const act = S.find.hits[S.find.active];
    for (const row of rowsEl.children) {
      const n = +row.dataset.l;
      if (!byLine.has(n)) continue;
      const marks = markNodes($('.c', row), S.find.q, S.find.cs, 'mark');
      if (act && act.line === n && marks[act.n]) marks[act.n].classList.add('on');
    }
  }
  void first; void last;
}

/* Wrap every occurrence of needle inside el, walking text nodes so the
   pre-highlighted token markup is never disturbed. */
export function markNodes(el, needle, caseSensitive, cls) {
  if (!el || !needle) return [];
  const out = [];
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const texts = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) texts.push(n);
  for (const node of texts) {
    const raw = node.nodeValue;
    const hay = caseSensitive ? raw : raw.toLowerCase();
    const nd = caseSensitive ? needle : needle.toLowerCase();
    let i = hay.indexOf(nd), at = 0;
    if (i < 0) continue;
    const frag = document.createDocumentFragment();
    while (i >= 0) {
      if (i > at) frag.appendChild(document.createTextNode(raw.slice(at, i)));
      const mk = document.createElement(cls === 'mark' ? 'mark' : 'span');
      if (cls !== 'mark') mk.className = cls;
      mk.textContent = raw.slice(i, i + nd.length);
      frag.appendChild(mk);
      out.push(mk);
      at = i + nd.length;
      i = hay.indexOf(nd, at);
    }
    if (at < raw.length) frag.appendChild(document.createTextNode(raw.slice(at)));
    node.parentNode.replaceChild(frag, node);
  }
  return out;
}

/* Wrap the half-open character range [from, to) of el in a span. Unlike the
   needle search used for find, this targets one exact occurrence, which is what
   a position-based decoration needs. */
export function wrapRange(el, from, to, cls) {
  if (!el || to <= from) return null;
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const nodes = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) nodes.push(n);
  let at = 0, out = null;
  for (const node of nodes) {
    const len = node.nodeValue.length;
    const s = Math.max(from, at), e = Math.min(to, at + len);
    if (s < e) {
      const a = s - at, b = e - at;
      const span = document.createElement('span');
      span.className = cls;
      span.textContent = node.nodeValue.slice(a, b);
      const frag = document.createDocumentFragment();
      if (a > 0) frag.appendChild(document.createTextNode(node.nodeValue.slice(0, a)));
      frag.appendChild(span);
      if (b < len) frag.appendChild(document.createTextNode(node.nodeValue.slice(b)));
      node.parentNode.replaceChild(frag, node);
      out = out || span;
    }
    at += len;
    if (at >= to) break;
  }
  return out;
}

export function rowFor(line) {
  const idx = line - renderedFirst - 1;
  if (idx >= 0 && idx < rowsEl.children.length) {
    const r = rowsEl.children[idx];
    if (+r.dataset.l === line) return r;
  }
  let lo = 0, hi = rowsEl.children.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1, row = rowsEl.children[mid];
    if (+row.dataset.l < line) lo = mid + 1; else hi = mid;
  }
  const row = rowsEl.children[lo];
  return row && +row.dataset.l === line ? row : null;
}

export function updateLinkDecoration() {
  for (const sp of rowsEl.querySelectorAll('span.link')) {
    const parent = sp.parentNode;
    if (parent) {
      parent.replaceChild(document.createTextNode(sp.textContent || ''), sp);
      parent.normalize();
    }
  }
  if (S.link) {
    const row = rowFor(S.link.line);
    if (row) wrapRange($('.c', row), S.link.col, S.link.col + S.link.word.length, 'link');
  }
}

export function ensureChunks(d, first, last) {
  const c0 = Math.floor(first / CHUNK), c1 = Math.floor(Math.max(first, last - 1) / CHUNK);
  for (let c = c0; c <= c1; c++) {
    if (d.chunks.has(c) || d.pending.has(c)) continue;
    d.pending.add(c);
    const reqGen = d.reqGen || 0;
    api('/api/file', { path: d.path, start: c * CHUNK, count: CHUNK })
      .then(j => {
        if (reqGen !== (d.reqGen || 0)) return; // superseded by a reload
        for (let i = 0; i < j.lines.length; i++) d.lines[j.start + i] = j.lines[i];
        if (Array.isArray(j.diagnostics)) d.diagnostics = j.diagnostics;
        d.gen = (d.gen || 0) + 1;
        d.chunks.add(c); d.pending.delete(c);
        d._colsChunks?.delete(c);
        d._wrap?.chunkEst?.delete(c);
        if (doc_() === d) { layout(); render(); }
        if (j.refine) refineChunk(d, c);
      })
      .catch(() => d.pending.delete(c));
  }
}

/* A window whose surrounding context was too short to close a very long string
   or comment is served as "inexact". The server's full-file pass settles it a
   moment later, so come back for that chunk and swap in the corrected lines. */
export function refineChunk(d, c, delay = 800, tries = 0) {
  if (tries === 0) {
    if (d.refining.has(c)) return;
    d.refining.add(c);
  }
  setTimeout(async () => {
    if (!S.tabs.includes(d) || tries > 6) { d.refining.delete(c); return; }
    let j;
    try { j = await api('/api/file', { path: d.path, start: c * CHUNK, count: CHUNK }); }
    catch { d.refining.delete(c); return; }
    if (!S.tabs.includes(d)) { d.refining.delete(c); return; }
    if (!j.exact) { refineChunk(d, c, Math.min(delay * 1.6, 5000), tries + 1); return; }
    d.refining.delete(c);
    let changed = false;
    for (let i = 0; i < j.lines.length; i++) {
      if (d.lines[j.start + i] !== j.lines[i]) { d.lines[j.start + i] = j.lines[i]; changed = true; }
    }
    if (Array.isArray(j.diagnostics)) d.diagnostics = j.diagnostics;
    if (changed) {
      d.gen = (d.gen || 0) + 1;
      d._colsChunks?.delete(c);
      d._wrap?.chunkEst?.delete(c);
    }
    if (changed && doc_() === d) render();
  }, delay);
}

export function initRenderer() {
  vp.addEventListener('scroll', render, { passive: true });
  new ResizeObserver(() => { layout(); render(); }).observe(editor);
}
