// web/src/editview.js
import { $, S, doc_, apiPostJson, LH } from './state.js';
import { vp, showToast, flashActionSuccess } from './ui.js';
import { drawTabs, loadGutter, reloadOpenTabs } from './tabs.js';
import { lineTopY, revealSourceLine } from './renderer.js';
import { syncPreview, previewing, previewTopLine, previewKind } from './markdown.js';
import { syncDiffView } from './diff.js';
import { triggerRefresh } from './gitstream.js';
import { updateStatus } from './status.js';

export const editViewEl = $('#editview');
export const editGutterEl = $('#editview-gutter');
export const editTextarea = $('#editview-textarea');
export const editSwitch = $('#edit-switch');
export const btnEditMode = $('#btn-edit-mode');
export const btnSaveFile = $('#btn-save-file');
export const btnCancelEdit = $('#btn-cancel-edit');

let mirror = null;
let lastGutterLineCount = -1;
let lastGutterWrap = null;
let gutterRAF = 0;

export function scheduleUpdateGutter() {
  if (gutterRAF) return;
  gutterRAF = requestAnimationFrame(() => {
    gutterRAF = 0;
    updateGutter();
  });
}

export function updateGutter() {
  if (!editTextarea || !editGutterEl) return;
  const text = editTextarea.value;
  if (!S.wrap) {
    let lineCount = 1;
    for (let i = 0; i < text.length; i++) {
      if (text.charCodeAt(i) === 10) lineCount++;
    }
    if (lineCount === lastGutterLineCount && lastGutterWrap === false) return;
    lastGutterLineCount = lineCount;
    lastGutterWrap = false;
    const digits = String(lineCount).length;
    editGutterEl.style.width = Math.max(45, digits * (S.chW || 7.8) + 26) + 'px';
    let s = '';
    for (let i = 1; i <= lineCount; i++) s += i + '\n';
    editGutterEl.textContent = s;
    return;
  }
  const lines = text.split('\n');
  const lineCount = lines.length;
  const digits = String(lineCount).length;
  editGutterEl.style.width = Math.max(45, digits * (S.chW || 7.8) + 26) + 'px';
  lastGutterLineCount = lineCount;
  lastGutterWrap = true;
  // Wrapped lines span several visual rows: measure each in a hidden mirror
  // laid out like the textarea and size the matching gutter number to it.
  if (!mirror) {
    mirror = document.createElement('div');
    mirror.id = 'editview-mirror';
    mirror.setAttribute('aria-hidden', 'true');
    editTextarea.parentElement.appendChild(mirror);
  }
  mirror.style.width = editTextarea.clientWidth + 'px';
  const mRows = mirror.children;
  while (mRows.length > lineCount) mirror.lastElementChild.remove();
  if (mRows.length < lineCount) {
    const frag = document.createDocumentFragment();
    for (let i = mRows.length; i < lineCount; i++) {
      frag.appendChild(document.createElement('div'));
    }
    mirror.appendChild(frag);
  }
  for (let i = 0; i < lineCount; i++) {
    const want = lines[i] || ' ';
    if (mRows[i].textContent !== want) mRows[i].textContent = want;
  }
  const heights = new Array(lineCount);
  for (let i = 0; i < lineCount; i++) heights[i] = mRows[i].offsetHeight;

  if (editGutterEl.childElementCount === 0 && editGutterEl.textContent) {
    editGutterEl.textContent = '';
  }
  const gRows = editGutterEl.children;
  while (gRows.length > lineCount) editGutterEl.lastElementChild.remove();
  if (gRows.length < lineCount) {
    const out = document.createDocumentFragment();
    for (let i = gRows.length; i < lineCount; i++) {
      const n = document.createElement('div');
      n.textContent = String(i + 1);
      out.appendChild(n);
    }
    editGutterEl.appendChild(out);
  }
  for (let i = 0; i < lineCount; i++) {
    const h = heights[i] + 'px';
    if (gRows[i].style.height !== h) gRows[i].style.height = h;
  }
}

// The doc whose text is currently in the textarea; lets any tab switch path
// (openFile, switchTab, closeTab) swap the content without each repeating it.
let loadedDoc = null;

function loadEditContent(d) {
  if (loadedDoc === d) return;
  if (loadedDoc && loadedDoc.isEditing) loadedDoc.editContent = editTextarea.value;
  loadedDoc = d;
  editTextarea.value = d.editContent || '';
  editTextarea.scrollTop = d.editScrollTop || 0;
  updateGutter();
  if (editGutterEl) editGutterEl.scrollTop = editTextarea.scrollTop;
}

// The textarea's line height follows the --lh setting, not LH.
function editLH() {
  return parseFloat(getComputedStyle(editTextarea).lineHeight) || LH;
}

// Top of line n in the textarea. Wrapped lines are taller than one line;
// their gutter rows were sized to match, so read the offset from there.
export function editLineTop(n) {
  const row = S.wrap && editGutterEl?.children[n - 1];
  return row ? row.offsetTop : (n - 1) * editLH();
}

export function centerEditLine(n, startChar, endChar) {
  if (!editTextarea || !editViewEl || editViewEl.hidden) return;
  const d = doc_();
  const text = editTextarea.value;
  const lines = text.split('\n');
  const lineCount = lines.length;
  const targetLine = Math.max(1, Math.min(n, lineCount));

  if (typeof startChar === 'string' && startChar) {
    const q = startChar;
    const lineStr = lines[targetLine - 1] || '';
    const idx = lineStr.toLowerCase().indexOf(q.toLowerCase());
    let offset = 0;
    for (let i = 0; i < targetLine - 1 && i < lineCount; i++) {
      offset += lines[i].length + 1;
    }
    if (idx >= 0) {
      startChar = offset + idx;
      endChar = startChar + q.length;
    } else {
      startChar = offset;
      endChar = offset;
    }
  }

  if (typeof startChar === 'number' && typeof endChar === 'number') {
    editTextarea.focus();
    editTextarea.setSelectionRange(startChar, endChar);
  } else {
    let offset = 0;
    for (let i = 0; i < targetLine - 1 && i < lineCount; i++) {
      offset += lines[i].length + 1;
    }
    editTextarea.focus();
    editTextarea.setSelectionRange(offset, offset);
  }

  const top = Math.max(0, editLineTop(targetLine) - Math.max(0, editTextarea.clientHeight / 2 - editLH() * 2));
  editTextarea.scrollTop = top;
  if (editGutterEl) editGutterEl.scrollTop = top;
  if (d) {
    d.cur = targetLine;
    d.editScrollTop = top;
  }
}

// The first line visible at the top of the textarea.
function editTopLine() {
  const top = editTextarea.scrollTop;
  const rows = S.wrap && editGutterEl ? editGutterEl.children : null;
  if (rows && rows.length) {
    for (let i = 0; i < rows.length; i++) {
      if (rows[i].offsetTop + rows[i].offsetHeight > top + 1) return i + 1;
    }
    return rows.length;
  }
  return Math.floor(top / editLH()) + 1;
}

export function syncEditUI() {
  const d = doc_();
  if (!editSwitch) return;
  if (!d || d.isImage || d.deleted || d.diffRef) {
    editSwitch.hidden = true;
    if (btnEditMode) btnEditMode.hidden = true;
    if (btnSaveFile) btnSaveFile.hidden = true;
    if (btnCancelEdit) btnCancelEdit.hidden = true;
    if (editViewEl) editViewEl.hidden = true;
    if (!d) {
      if (editTextarea) editTextarea.value = '';
      if (editGutterEl) editGutterEl.textContent = '';
      document.body.classList.remove('editing-file');
    }
    vp.hidden = false;
    return;
  }
  editSwitch.hidden = false;
  if (d.isEditing) {
    if (btnEditMode) btnEditMode.hidden = true;
    if (btnSaveFile) btnSaveFile.hidden = false;
    if (btnCancelEdit) btnCancelEdit.hidden = false;
    if (editViewEl) editViewEl.hidden = false;
    if (editTextarea) loadEditContent(d);
    vp.hidden = true;
    const mv = $('#mdview');
    if (mv) mv.hidden = true;
    const dv = $('#diffview');
    if (dv) dv.hidden = true;
    const iv = $('#imgview');
    if (iv) iv.hidden = true;
  } else {
    if (btnEditMode) btnEditMode.hidden = false;
    if (btnSaveFile) btnSaveFile.hidden = true;
    if (btnCancelEdit) btnCancelEdit.hidden = true;
    if (editViewEl) editViewEl.hidden = true;
    vp.hidden = false;
  }
}

export async function enterEditMode(line) {
  const d = doc_();
  if (!d || d.isImage || d.deleted || d.diffRef) return;

  if (d.total > 10000 || (d.size && d.size > 1024 * 1024)) {
    const sizeStr = d.size ? ` (${(d.size / 1024 / 1024).toFixed(1)} MB)` : '';
    const ok = confirm(`"${d.name}" is large: ${d.total.toLocaleString()} lines${sizeStr}.\nLoading the entire file into edit mode may take a moment.\n\nDo you want to proceed?`);
    if (!ok) return;
  }

  let text = '';
  if (d.editContent !== undefined && d.editContent !== null) {
    text = d.editContent;
  } else {
    try {
      const u = new URL('api/raw', document.baseURI || location.href);
      u.searchParams.set('path', d.path);
      const res = await fetch(u);
      if (!res.ok) throw new Error('Failed to fetch file content');
      text = await res.text();
    } catch (err) {
      showToast('!', 'Failed to load file for editing: ' + err.message);
      return;
    }
  }

  // The preview's own cursor is not d.cur: start editing where the preview is.
  const from = line || (previewing(d) ? previewTopLine() : d.cur) || 1;

  d.isEditing = true;
  d.editContent = text;
  document.body.classList.add('editing-file');
  syncEditUI();
  syncPreview(); // hides the preview through markdown.js so it redraws on exit
  updateStatus();

  editTextarea.value = text;
  loadedDoc = d;
  updateGutter();

  const targetLine = from;
  const lines = text.split('\n');
  let charOffset = 0;
  for (let i = 0; i < targetLine - 1 && i < lines.length; i++) {
    charOffset += lines[i].length + 1;
  }

  editTextarea.focus();
  editTextarea.setSelectionRange(charOffset, charOffset);
  editTextarea.scrollTop = Math.max(0, editLineTop(Math.max(1, targetLine - 4)));
  if (editGutterEl) editGutterEl.scrollTop = editTextarea.scrollTop;
}

export async function exitEditMode(force = false) {
  const d = doc_();
  if (!d || !d.isEditing) return;

  if (d.dirty && !force) {
    if (!confirm(`You have unsaved changes in "${d.name}". Discard changes and exit edit mode?`)) {
      return;
    }
    d.dirty = false;
    d.editContent = null;
    drawTabs();
  }

  // Come back to the line that was at the top of the editor, in source or preview.
  const top = Math.min(editTopLine(), d.total || 1);
  d.isEditing = false;
  document.body.classList.remove('editing-file');
  syncEditUI();

  if (d.diffMode) {
    syncDiffView();
    return;
  }
  d.cur = top;
  revealSourceLine(top, d);
  vp.scrollTop = lineTopY(top, d);
  if (previewKind(d)) d.mdLine = top;
  // Reloads the source and, through a fresh doc, the preview: a save made in
  // edit mode leaves both stale.
  await reloadOpenTabs({ onlyIfChanged: false });
}

export async function saveActiveFile() {
  const d = doc_();
  if (!d || !d.isEditing) return;

  const content = editTextarea.value;
  if (btnSaveFile) btnSaveFile.disabled = true;
  try {
    await apiPostJson('/api/file/save', { path: d.path, content });
    d.dirty = false;
    d.editContent = content;
    d.size = new Blob([content]).size;
    d.total = (content.match(/\n/g) || []).length + 1;
    drawTabs();
    if (btnSaveFile) flashActionSuccess(btnSaveFile, 'Saved');
    showToast('✓', 'Saved ' + d.name);
    loadGutter(d);
    triggerRefresh();
  } catch (err) {
    showToast('!', 'Failed to save: ' + (err.message || 'unknown error'));
  } finally {
    if (btnSaveFile) btnSaveFile.disabled = false;
  }
}

export function initEditView() {
  if (!editTextarea) return;

  let lastW = 0;
  new ResizeObserver(() => {
    const w = editTextarea.clientWidth;
    if (S.wrap && w && w !== lastW && !editViewEl.hidden) updateGutter();
    lastW = w;
  }).observe(editTextarea);
  let lastWrapClass = document.body.classList.contains('word-wrap');
  let lastHideLinesClass = document.body.classList.contains('hide-lines');
  new MutationObserver(() => {
    const wrapClass = document.body.classList.contains('word-wrap');
    const hideLinesClass = document.body.classList.contains('hide-lines');
    if (wrapClass === lastWrapClass && hideLinesClass === lastHideLinesClass) return;
    lastWrapClass = wrapClass;
    lastHideLinesClass = hideLinesClass;
    if (!editViewEl.hidden) updateGutter();
  }).observe(document.body, { attributes: true, attributeFilter: ['class'] });

  editTextarea.addEventListener('scroll', () => {
    if (loadedDoc) loadedDoc.editScrollTop = editTextarea.scrollTop;
    if (editGutterEl) editGutterEl.scrollTop = editTextarea.scrollTop;
  }, { passive: true });

  editTextarea.addEventListener('input', () => {
    const d = doc_();
    if (d) {
      d.editContent = editTextarea.value;
      if (!d.dirty) {
        d.dirty = true;
        drawTabs();
      }
    }
    if (S.find && S.find.editing) {
      S.find = null;
      const cnt = $('#find-count');
      if (cnt) cnt.textContent = '—';
      const mm = $('#minimap-hits');
      if (mm) mm.innerHTML = '';
    }
    if (S.wrap) scheduleUpdateGutter();
    else updateGutter();
  });

  editTextarea.addEventListener('keydown', e => {
    if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S')) {
      e.preventDefault();
      saveActiveFile();
      return;
    }
    if (e.key === 'Escape') {
      e.preventDefault();
      exitEditMode();
      return;
    }
    if (e.key === 'Tab') {
      e.preventDefault();
      const start = editTextarea.selectionStart;
      const end = editTextarea.selectionEnd;
      const val = editTextarea.value;
      const spaces = '    ';
      if (!e.shiftKey) {
        if (start === end) {
          document.execCommand('insertText', false, spaces);
        } else {
          const selStart = val.lastIndexOf('\n', start - 1) + 1;
          const selEnd = val.indexOf('\n', end);
          const sliceEnd = selEnd === -1 ? val.length : selEnd;
          const lines = val.substring(selStart, sliceEnd).split('\n');
          const indented = lines.map(l => spaces + l).join('\n');
          editTextarea.setSelectionRange(selStart, sliceEnd);
          document.execCommand('insertText', false, indented);
          editTextarea.setSelectionRange(start + spaces.length, sliceEnd + spaces.length * lines.length);
        }
      }
      updateGutter();
      return;
    }
    if (e.key === 'Enter') {
      const pos = editTextarea.selectionStart;
      const val = editTextarea.value;
      const lineStart = val.lastIndexOf('\n', pos - 1) + 1;
      const currentLine = val.substring(lineStart, pos);
      const match = currentLine.match(/^[ \t]+/);
      if (match) {
        e.preventDefault();
        const indent = match[0];
        document.execCommand('insertText', false, '\n' + indent);
        updateGutter();
      }
    }
  });

  btnEditMode?.addEventListener('click', () => enterEditMode());
  btnSaveFile?.addEventListener('click', () => saveActiveFile());
  btnCancelEdit?.addEventListener('click', () => exitEditMode());
}
