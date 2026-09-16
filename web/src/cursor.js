// web/src/cursor.js
import { $, S, doc_, MOD, LH } from './state.js';
import { vp, rowsEl } from './ui.js';
import { paint, render, rowFor, placeCaret, toPoint } from './renderer.js';
import { updateStatus } from './status.js';
import { gotoDefinition } from './lsp.js';
import { pushHistory } from './history.js';

export const WORD = /[A-Za-z0-9_$]/;

/* Returns {word, line, col} where col counts UTF-16 units from the start of the
   line, which is both what JS string indexes give us and what the server needs
   to place an LSP request. Walking text nodes keeps this correct even after
   find or occurrence marks have wrapped parts of the line. */
export function wordAtPoint(x, y) {
  let node, off;
  if (document.caretPositionFromPoint) {
    const p = document.caretPositionFromPoint(x, y);
    if (!p) return null;
    node = p.offsetNode; off = p.offset;
  } else if (document.caretRangeFromPoint) {
    const r = document.caretRangeFromPoint(x, y);
    if (!r) return null;
    node = r.startContainer; off = r.startOffset;
  } else return null;
  if (!node || node.nodeType !== 3) return null;

  const code = node.parentElement && node.parentElement.closest('.c');
  const row = code && code.closest('.row');
  if (!code || !row) return null;

  let col = 0;
  const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    if (n === node) { col += off; break; }
    col += n.nodeValue.length;
  }

  const full = code.textContent;
  let a = Math.min(col, full.length), b = a;
  while (a > 0 && WORD.test(full[a - 1])) a--;
  while (b < full.length && WORD.test(full[b])) b++;
  if (a === b) return null;
  const d = doc_();
  return { word: full.slice(a, b), line: +row.dataset.l, col: a, path: d && d.path };
}

/* Column (UTF-16 units into the line's text) under a point. Clicking the gutter
   gives 0; clicking the empty space right of the text gives the line's end. */
export function colAtPoint(x, y) {
  let node, off;
  if (document.caretPositionFromPoint) {
    const p = document.caretPositionFromPoint(x, y);
    if (!p) return null;
    node = p.offsetNode; off = p.offset;
  } else if (document.caretRangeFromPoint) {
    const r = document.caretRangeFromPoint(x, y);
    if (!r) return null;
    node = r.startContainer; off = r.startOffset;
  } else return null;
  const el = node && (node.nodeType === 1 ? node : node.parentElement);
  const row = el && el.closest('.row');
  if (!row) return null;
  const code = $('.c', row);
  const line = +row.dataset.l;
  if (!code.contains(node)) return { line, col: el.closest('.g') ? 0 : code.textContent.length };
  const r = document.createRange();
  r.setStart(code, 0);
  r.setEnd(node, off);
  return { line, col: r.toString().length };
}

/* Keep the caret inside the horizontally scrolled area when it moves. */
function revealCaretX(x) {
  const d = doc_();
  if (x == null || S.wrap || !d) return;
  const g = rowFor(d.cur)?.querySelector('.g');
  const gw = S.lineNumbers && g ? g.offsetWidth : 0;
  if (x < vp.scrollLeft + gw + 8) vp.scrollLeft = Math.max(0, x - gw - 40);
  else if (x > vp.scrollLeft + vp.clientWidth - 24) vp.scrollLeft = x - vp.clientWidth + 60;
}

export function updateDomSelection() {
  const d = doc_();
  if (!d) return;
  const sel = window.getSelection();
  if (!sel) return;
  if (!d.selAnchor) {
    if (sel.rangeCount && !sel.isCollapsed && vp.contains(sel.getRangeAt(0).commonAncestorContainer)) {
      sel.removeAllRanges();
    }
    return;
  }
  const pa = toPoint(d.selAnchor);
  const headCol = d.col === Infinity ? (rowFor(d.cur) ? $('.c', rowFor(d.cur)).textContent.length : 0) : (d.col || 0);
  const pf = toPoint({ line: d.cur, col: headCol });
  if (pa && pf) {
    sel.setBaseAndExtent(pa[0], pa[1], pf[0], pf[1]);
  }
}

function ensureAnchor(d) {
  if (!d.selAnchor) {
    const col = d.col === Infinity ? (rowFor(d.cur) ? $('.c', rowFor(d.cur)).textContent.length : 0) : (d.col || 0);
    d.selAnchor = { line: d.cur, col };
  }
}

export function clearSelection(d) {
  if (d) d.selAnchor = null;
  const sel = window.getSelection();
  if (sel && sel.rangeCount && !sel.isCollapsed && vp.contains(sel.getRangeAt(0).commonAncestorContainer)) {
    sel.removeAllRanges();
  }
}

/* Left/Right along the line, wrapping onto the neighbouring line at either end. */
export function moveCol(delta, shift = false) {
  const d = doc_(); if (!d) return;
  if (shift) ensureAnchor(d);
  else d.selAnchor = null;

  const row = rowFor(d.cur);
  const len = row ? $('.c', row).textContent.length : 0;
  const col = Math.min(d.col || 0, len) + delta;
  if (col < 0) {
    if (d.cur > 1) { d.col = Infinity; moveCursor(-1, shift); }
    else updateDomSelection();
    return;
  }
  if (col > len) {
    if (d.cur < d.total) { d.col = 0; moveCursor(1, shift); }
    else updateDomSelection();
    return;
  }
  d.col = col;
  revealCaretX(placeCaret());
  updateDomSelection();
}

/* Jump by word boundary left/right */
export function moveWord(delta, shift = false) {
  const d = doc_(); if (!d) return;
  if (shift) ensureAnchor(d);
  else d.selAnchor = null;

  const row = rowFor(d.cur);
  const text = row ? $('.c', row).textContent : '';
  const len = text.length;
  let col = Math.min(d.col === Infinity ? len : (d.col || 0), len);

  if (delta < 0) {
    if (col === 0) {
      if (d.cur > 1) { d.col = Infinity; moveCursor(-1, shift); }
      return;
    }
    col--;
    while (col > 0 && /\s/.test(text[col])) col--;
    if (WORD.test(text[col])) {
      while (col > 0 && WORD.test(text[col - 1])) col--;
    } else {
      while (col > 0 && !WORD.test(text[col - 1]) && !/\s/.test(text[col - 1])) col--;
    }
  } else {
    if (col >= len) {
      if (d.cur < d.total) { d.col = 0; moveCursor(1, shift); }
      return;
    }
    if (WORD.test(text[col])) {
      while (col < len && WORD.test(text[col])) col++;
    } else if (!/\s/.test(text[col])) {
      while (col < len && !WORD.test(text[col]) && !/\s/.test(text[col])) col++;
    }
    while (col < len && /\s/.test(text[col])) col++;
  }
  d.col = col;
  revealCaretX(placeCaret());
  updateDomSelection();
}

export function caretToEdge(end, shift = false) {
  const d = doc_(); if (!d) return;
  if (shift) ensureAnchor(d);
  else d.selAnchor = null;

  d.col = end ? Infinity : 0;
  revealCaretX(placeCaret());
  updateDomSelection();
}

export function moveCursor(delta, shift = false) {
  const d = doc_(); if (!d) return;
  if (shift) ensureAnchor(d);
  else d.selAnchor = null;

  d.cur = Math.max(1, Math.min(d.total, d.cur + delta));
  const y = (d.cur - 1) * LH;
  if (y < vp.scrollTop) vp.scrollTop = y - LH;
  else if (y > vp.scrollTop + vp.clientHeight - LH * 2) vp.scrollTop = y - vp.clientHeight + LH * 3;
  render(); updateStatus();
  updateDomSelection();
}

export function initCursor() {
  vp.addEventListener('mousedown', e => {
    // Only the primary button moves the caret: a right click opens a menu on
    // what is already selected and must leave it where it is.
    if (e.button !== 0) return;
    const row = e.target.closest('.row');
    if (!row) return;
    const d = doc_(); if (!d) return;
    const targetLine = +row.dataset.l;
    const p = colAtPoint(e.clientX, e.clientY);
    const targetCol = p && p.line === targetLine ? p.col : 0;

    if (e.shiftKey) {
      ensureAnchor(d);
      d.cur = targetLine;
      d.col = targetCol;
      placeCaret();
      updateStatus();
      updateDomSelection();
      for (const r of rowsEl.children) r.classList.toggle('cur', +r.dataset.l === d.cur);
      return;
    }

    d.selAnchor = null;
    d.cur = targetLine;
    d.col = targetCol;
    placeCaret(); // no repaint here: rewriting rows would break the drag that starts a selection
    updateStatus();
    const w = wordAtPoint(e.clientX, e.clientY);
    // The clicked identifier is what F12, Shift+F12 and Alt+Shift+H act on.
    S.at = w;
    if (w) S.lastWord = w.word;
    if (e[MOD] && w) {
      e.preventDefault();
      S.at = w; S.lastWord = w.word;
      pushHistory(d.path, d.cur); // so Alt+Left returns to the call site
      gotoDefinition(w);
      return;
    }
    for (const r of rowsEl.children) r.classList.toggle('cur', +r.dataset.l === d.cur);
  });

  vp.addEventListener('dblclick', e => {
    const w = wordAtPoint(e.clientX, e.clientY);
    if (w) { S.at = w; S.lastWord = w.word; }
    S.occ = (w && w.word.length > 1) ? w.word : null;
    paint();
  });
}
