// web/src/vim.js — Vim modes, keys, visual selection, command mode.
import { $, S, MOD, doc_ } from './state.js';
import { moveCursor, moveCol, caretToEdge, moveWordForward, moveWordBackward } from './cursor.js';
import { enterInsert, runNormalEdit, exitInsertMode, blurEditInput } from './edit.js';
import { deleteChar, deleteLine, deleteToEOL, lineText } from './edit-buffer.js';
import { visualRangeFor as computeVisualRange } from './vim-visual.js';
import { undo, redo, pushUndo } from './edit-undo.js';
import { runExCommand } from './vim-cmd.js';
import { updateVimStatus } from './vim-status.js';
import { render } from './renderer.js';
import { updateStatus } from './status.js';
import { drawTabs } from './tabs.js';
import { copyToClipboard } from './ui.js';

export const VIM_KEY = 'px0.vim';

let pendingKey = '';
let pendingTimer = 0;

function clearPending() {
  pendingKey = '';
  clearTimeout(pendingTimer);
}

export function vimEnabled() {
  return !!S.vim;
}

export function vimMode() {
  return S.vimMode;
}

export function vimVisual() {
  return S.vimVisual;
}

export function applyVimClasses() {
  document.body.classList.toggle('vim-on', S.vim);
  document.body.classList.toggle('vim-normal', S.vim && S.vimMode === 'normal');
  document.body.classList.toggle('vim-insert', S.vim && S.vimMode === 'insert');
  document.body.classList.toggle('vim-visual', S.vim && S.vimMode === 'visual');
  document.body.classList.toggle('vim-command', S.vim && S.vimMode === 'command');
  updateVimStatus();
}

export function updateVimControl() {
  const btn = $('[data-action="vim"]');
  if (btn) btn.classList.toggle('active', !!S.vim);
}

export function toggleVim(forced) {
  S.vim = typeof forced === 'boolean' ? forced : !S.vim;
  if (S.vim) {
    S.vimMode = 'normal';
    S.vimVisual = null;
    S.vimCmd = '';
  } else {
    blurEditInput();
    S.vimMode = 'normal';
    S.vimVisual = null;
    S.vimCmd = '';
  }
  applyVimClasses();
  updateVimControl();
  try { localStorage.setItem(VIM_KEY, S.vim ? 'true' : 'false'); } catch {}
}

export function setVimMode(mode) {
  if (!S.vim) return;
  if (mode !== 'visual') S.vimVisual = null;
  if (mode !== 'command') S.vimCmd = '';
  S.vimMode = mode;
  applyVimClasses();
}

export function clearVisual() {
  S.vimVisual = null;
  if (S.vimMode === 'visual') S.vimMode = 'normal';
  applyVimClasses();
}

export function enterVisual(kind) {
  const d = doc_();
  if (!d || !S.vim) return;
  S.vimVisual = { anchor: { line: d.cur, col: d.col || 0 }, kind };
  setVimMode('visual');
  render();
}

function visualRange(d) {
  return computeVisualRange(d, S.vimVisual);
}

export function visualRangeFor(d) {
  return computeVisualRange(d, S.vimVisual);
}

function yankVisual(d) {
  const r = visualRange(d);
  if (!r) return;
  let text = '';
  if (r.kind === 'line') {
    for (let l = r.l1; l <= r.l2; l++) text += (lineText(d, l) + (l < r.l2 ? '\n' : ''));
  } else if (r.l1 === r.l2) {
    text = lineText(d, r.l1).slice(r.c1, r.c2);
  } else {
    for (let l = r.l1; l <= r.l2; l++) {
      const line = lineText(d, l);
      if (l === r.l1) text += line.slice(r.c1) + '\n';
      else if (l === r.l2) text += line.slice(0, r.c2);
      else text += line + '\n';
    }
  }
  copyToClipboard(text, 'Yanked');
}

function deleteVisual(d) {
  const r = visualRange(d);
  if (!r) return;
  if (r.kind === 'line') {
    for (let n = r.l2; n >= r.l1; n--) {
      d.cur = n;
      deleteLine(d);
    }
    d.cur = Math.min(r.l1, d.total);
    d.col = 0;
    return;
  }
  if (r.l1 === r.l2) {
    d.cur = r.l1;
    d.col = r.c1;
    const len = r.c2 - r.c1;
    for (let i = 0; i < len; i++) deleteChar(d, true);
    return;
  }
  // multiline char delete — simplified: line-wise for MVP
  for (let l = r.l2; l >= r.l1; l--) {
    d.cur = l;
    if (l === r.l1) {
      d.col = r.c1;
      deleteToEOL(d);
    } else if (l === r.l2) {
      d.col = r.c2;
      while (d.col > 0) deleteChar(d, false);
      if (l > r.l1) deleteLine(d);
    } else {
      deleteLine(d);
    }
  }
  d.cur = r.l1;
  d.col = r.c1;
}

function handleVisualKey(e) {
  const d = doc_();
  if (!d) return false;
  if (e.key === 'h') { moveCol(-1); render(); updateVimStatus(); return true; }
  if (e.key === 'j') { moveCursor(1); render(); updateVimStatus(); return true; }
  if (e.key === 'k') { moveCursor(-1); render(); updateVimStatus(); return true; }
  if (e.key === 'l') { moveCol(1); render(); updateVimStatus(); return true; }
  if (e.key === '0') { caretToEdge(false); render(); updateVimStatus(); return true; }
  if (e.key === '$') { caretToEdge(true); render(); updateVimStatus(); return true; }
  if (e.key === 'y') {
    yankVisual(d);
    clearVisual();
    render();
    return true;
  }
  if (e.key === 'd' || e.key === 'x') {
    pushUndo(d);
    deleteVisual(d);
    clearVisual();
    render(); updateStatus(); drawTabs();
    return true;
  }
  if (e.key === 'Escape') {
    clearVisual();
    render();
    return true;
  }
  return false;
}

function handleCommandKey(e) {
  if (e.key === 'Escape') {
    e.preventDefault();
    S.vimCmd = '';
    setVimMode('normal');
    return true;
  }
  if (e.key === 'Enter') {
    e.preventDefault();
    const line = S.vimCmd || '';
    S.vimCmd = '';
    setVimMode('normal');
    runExCommand(line);
    return true;
  }
  if (e.key === 'Backspace') {
    e.preventDefault();
    S.vimCmd = (S.vimCmd || '').slice(0, -1);
    updateVimStatus();
    return true;
  }
  if (e.key.length === 1 && !e.altKey && !e.ctrlKey && !e.metaKey) {
    e.preventDefault();
    S.vimCmd = (S.vimCmd || '') + e.key;
    updateVimStatus();
    return true;
  }
  return false;
}

function enterCommandMode() {
  if (!S.vim) return;
  S.vimCmd = '';
  setVimMode('command');
}

export function handleVimKey(e) {
  if (!S.vim) return false;

  if (S.vimMode === 'command') return handleCommandKey(e);

  if (S.vimMode === 'visual') {
    if (e[MOD] || e.altKey || e.ctrlKey) return false;
    return handleVisualKey(e);
  }

  if (S.vimMode !== 'normal') return false;
  if (e[MOD] || e.altKey || e.ctrlKey) return false;

  if (e.key === ':') {
    e.preventDefault();
    enterCommandMode();
    return true;
  }

  if (pendingKey === 'd' && e.key === 'd') {
    clearPending();
    runNormalEdit(d => deleteLine(d));
    return true;
  }
  clearPending();

  const d = doc_();

  switch (e.key) {
    case 'h': moveCol(-1); return true;
    case 'j': moveCursor(1); return true;
    case 'k': moveCursor(-1); return true;
    case 'l': moveCol(1); return true;
    case 'w': moveWordForward(); return true;
    case 'b': moveWordBackward(); return true;
    case '0': caretToEdge(false); return true;
    case '$': caretToEdge(true); return true;
    case 'i': enterInsert('i'); return true;
    case 'a': enterInsert('a'); return true;
    case 'A': enterInsert('A'); return true;
    case 'o': enterInsert('o'); return true;
    case 'O': enterInsert('O'); return true;
    case 'v': enterVisual('char'); return true;
    case 'V': enterVisual('line'); return true;
    case 'u':
      if (d && undo(d)) { render(); updateStatus(); drawTabs(); }
      return true;
    case 'x': runNormalEdit(doc => deleteChar(doc, true)); return true;
    case 'D': runNormalEdit(doc => deleteToEOL(doc)); return true;
    case 'd':
      pendingKey = 'd';
      pendingTimer = setTimeout(clearPending, 500);
      return true;
    default: return false;
  }
}

export function handleVimRedo(e) {
  if (!S.vim || S.vimMode !== 'normal') return false;
  if (!e.ctrlKey || e[MOD] || e.altKey || e.shiftKey) return false;
  if (e.key !== 'r' && e.key !== 'R') return false;
  const d = doc_();
  if (d && redo(d)) { render(); updateStatus(); drawTabs(); }
  return true;
}

export function initVim() {
  try {
    const pref = localStorage.getItem(VIM_KEY);
    if (pref !== null) S.vim = pref === 'true';
  } catch {}
  S.vimMode = 'normal';
  S.vimCmd = '';
  S.vimVisual = null;
  applyVimClasses();
  updateVimControl();
}
