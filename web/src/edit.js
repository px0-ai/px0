// web/src/edit.js — editing layer: hidden input, save, chunk sync.
import { $, S, doc_, api, apiPostJSON, CHUNK } from './state.js';
import { vp, sizer, showToast } from './ui.js';
import { render, placeCaret, rowFor, layout } from './renderer.js';
import { updateStatus } from './status.js';
import { drawTabs, closeTab, closeAllTabs } from './tabs.js';
import { previewing } from './markdown.js';
import { vimEnabled, vimMode, setVimMode } from './vim.js';
import { updateVimStatus } from './vim-status.js';
import { pushUndo } from './edit-undo.js';
import {
  insertText, insertNewline, deleteChar, deleteLine, deleteToEOL, openLine,
  assembleContent, rawComplete, lineText, ensureRawLine, applyChunk,
} from './edit-buffer.js';

export const editInput = $('#edit-input');

export function canEdit(d = doc_()) {
  return !!(d && !previewing(d) && !$('#imgview'));
}

/* True when the hidden input should accept keystrokes. */
export function isTyping() {
  if (!editInput || editInput.hidden) return false;
  if (vimEnabled()) return vimMode() === 'insert';
  return document.activeElement === editInput;
}

export async function ensureRawComplete(d) {
  if (!d || d.rawComplete) return;
  for (let c = 0; c * CHUNK < d.total; c++) {
    const start = c * CHUNK;
    let have = true;
    for (let i = start; i < Math.min(start + CHUNK, d.total); i++) {
      if (d.raw[i] === undefined) { have = false; break; }
    }
    if (have) continue;
    const j = await api('/api/file', { path: d.path, start, count: CHUNK });
    if (!S.tabs.includes(d)) return;
    applyChunk(d, j);
  }
  d.rawComplete = rawComplete(d);
}

export function positionEditInput(force = false) {
  if (!editInput) return;
  const d = doc_();
  const typing = vimEnabled() ? vimMode() === 'insert'
    : force || document.activeElement === editInput;
  if (!d || !canEdit(d) || !typing) { editInput.hidden = true; return; }
  const x = placeCaret();
  if (x == null) { editInput.hidden = true; return; }
  const row = rowFor(d.cur);
  if (!row) { editInput.hidden = true; return; }
  const base = sizer.getBoundingClientRect();
  const rowTop = row.getBoundingClientRect().top - base.top;
  editInput.style.left = x + 'px';
  editInput.style.top = rowTop + 'px';
  editInput.style.height = 'var(--lh)';
  editInput.hidden = false;
}

export function blurEditInput() {
  if (!editInput) return;
  editInput.blur();
  editInput.value = '';
  editInput.hidden = true;
}

export function exitInsertMode() {
  blurEditInput();
  if (vimEnabled()) setVimMode('normal');
}

export function focusEdit() {
  const d = doc_();
  if (!canEdit(d) || !editInput) return;
  ensureRawLine(d, d.cur);
  editInput.value = '';
  editInput.hidden = false;
  positionEditInput(true);
  editInput.focus();
  positionEditInput();
}

/* When Vim is off, route the first printable key into the buffer. */
export function handlePlainType(e) {
  if (vimEnabled() || !canEdit()) return false;
  if (e.metaKey || e.ctrlKey || e.altKey) return false;
  const d = doc_();
  if (!d) return false;

  if (e.key === 'Enter') {
    e.preventDefault();
    focusEdit();
    pushUndo(d);
    insertNewline(d);
    editInput.value = '';
    render(); updateStatus(); drawTabs(); positionEditInput();
    return true;
  }
  if (e.key === 'Backspace') {
    e.preventDefault();
    focusEdit();
    pushUndo(d);
    deleteChar(d, false);
    render(); updateStatus(); drawTabs(); positionEditInput();
    return true;
  }
  if (e.key === 'Delete') {
    e.preventDefault();
    focusEdit();
    pushUndo(d);
    deleteChar(d, true);
    render(); updateStatus(); drawTabs(); positionEditInput();
    return true;
  }
  if (e.key.length === 1) {
    e.preventDefault();
    focusEdit();
    pushUndo(d);
    insertText(d, e.key);
    editInput.value = '';
    render(); updateStatus(); drawTabs(); positionEditInput();
    return true;
  }
  return false;
}

export function editWithUndo(d, fn) {
  if (!d) return false;
  pushUndo(d);
  fn(d);
  render();
  updateStatus();
  drawTabs();
  updateVimStatus();
  return true;
}

/* mode: i | a | A | o | O */
export function enterInsert(mode = 'i') {
  const d = doc_();
  if (!canEdit(d)) return;
  if (mode === 'o' || mode === 'O') pushUndo(d);
  ensureRawLine(d, d.cur);
  if (mode === 'a') {
    d.col = Math.min((d.col || 0) + 1, lineText(d, d.cur).length);
  } else if (mode === 'A') {
    d.col = lineText(d, d.cur).length;
  } else if (mode === 'o') {
    openLine(d, true);
    drawTabs();
  } else if (mode === 'O') {
    openLine(d, false);
    drawTabs();
  }
  if (!vimEnabled()) { focusEdit(); return; }
  setVimMode('insert');
  if (mode === 'o' || mode === 'O') { render(); updateStatus(); }
  focusEdit();
}

export function runNormalEdit(fn) {
  const d = doc_();
  if (!canEdit(d) || !vimEnabled() || vimMode() !== 'normal') return false;
  return editWithUndo(d, fn);
}

export async function reloadHighlights(d) {
  d.lines = new Array(d.total);
  d.chunks = new Set();
  d.pending = new Set();
  d.refining = new Set();
  d.dirtyLines = new Set();
  d.dirty = false;
  d.rawComplete = rawComplete(d);
  d.gen++;
  layout();
  render();
  updateStatus();
  drawTabs();
  updateVimStatus();
}

export async function saveFile(d = doc_()) {
  if (!d) return false;
  if (!d.dirty) { showToast('', 'No changes'); return true; }
  try {
    await ensureRawComplete(d);
    if (!rawComplete(d)) throw new Error('file not fully loaded');
    await apiPostJSON('/api/file/save', { path: d.path, content: assembleContent(d) });
    await reloadHighlights(d);
    showToast('✓', 'Saved ' + d.name);
    return true;
  } catch (e) {
    showToast('!', e.message || 'Save failed');
    return false;
  }
}

export async function saveAllFiles() {
  const dirty = S.tabs.filter(t => t.dirty);
  if (!dirty.length) { showToast('', 'No changes'); return; }
  let ok = 0;
  for (const d of dirty) {
    if (await saveFile(d)) ok++;
  }
  if (ok) showToast('✓', 'Saved ' + ok + ' file(s)');
}

export function quitTab(force = false) {
  if (S.active < 0) return;
  closeTab(S.active, force);
  updateVimStatus();
}

export function quitAll(force = false) {
  closeAllTabs(force);
  updateVimStatus();
}

export function initEdit() {
  if (!editInput) return;

  editInput.addEventListener('input', () => {
    const d = doc_();
    if (!d || !canEdit(d) || !isTyping()) return;
    const v = editInput.value;
    if (!v) return;
    pushUndo(d);
    insertText(d, v);
    editInput.value = '';
    render();
    updateStatus();
    drawTabs();
    updateVimStatus();
    positionEditInput();
  });

  editInput.addEventListener('keydown', e => {
    const d = doc_();
    if (!d || !canEdit(d) || !isTyping()) return;
    if (e.key === 'Enter') {
      e.preventDefault();
      pushUndo(d);
      insertNewline(d);
      editInput.value = '';
      render();
      updateStatus();
      drawTabs();
      updateVimStatus();
      positionEditInput();
      return;
    }
    if (e.key === 'Backspace') {
      e.preventDefault();
      pushUndo(d);
      deleteChar(d, false);
      render();
      updateStatus();
      drawTabs();
      updateVimStatus();
      positionEditInput();
      return;
    }
    if (e.key === 'Delete') {
      e.preventDefault();
      pushUndo(d);
      deleteChar(d, true);
      render();
      updateStatus();
      drawTabs();
      updateVimStatus();
      positionEditInput();
    }
  });

  vp.addEventListener('scroll', () => {
    if (isTyping()) positionEditInput();
  });

  window.addEventListener('beforeunload', e => {
    if (S.tabs.some(t => t.dirty)) {
      e.preventDefault();
      e.returnValue = '';
    }
  });
}
