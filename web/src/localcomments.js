// web/src/localcomments.js
// Local text comments for non-PR directory reviews.
// Saves plain-text notes to the server (/api/local-comments), renders gutter
// markers, highlights commented lines, and provides a comments panel.
import { S, $, doc_ } from './state.js';
import { showToast, copyToClipboard } from './ui.js';
import { on } from './bus.js';
import { setLocalCommentHandler } from './selbar.js';
import { openFile } from './tabs.js';

let localNotes = [];
let composerEl = null;

export function initLocalComments() {
  setLocalCommentHandler(openLocalComposer);
  loadComments();
  on('file:open', () => setTimeout(renderGutterBadges, 150));
  on('tab:activated', () => setTimeout(renderGutterBadges, 150));

  // Re-render badges on scroll since px0 virtualizes rows (~60 in DOM)
  const vp = document.getElementById('viewport');
  if (vp) {
    let scrollTimer = 0;
    vp.addEventListener('scroll', () => {
      clearTimeout(scrollTimer);
      scrollTimer = setTimeout(renderGutterBadges, 80);
    });
  }

  const clearBtn = $('#lc-clear-all');
  if (clearBtn) clearBtn.onclick = clearAllComments;
  const exportBtn = $('#lc-export');
  if (exportBtn) exportBtn.onclick = exportComments;
}

async function loadComments() {
  try {
    const base = document.baseURI || (location.origin + '/');
    const r = await fetch(new URL('api/local-comments', base));
    if (r.ok) localNotes = await r.json() || [];
    renderPanel();
    updateBadge();
  } catch {}
}

// --- Composer ---

export function openLocalComposer(sel) {
  closeComposer();
  const d = doc_();
  if (!d) return;
  const lineRef = d.path + ':' + sel.l1 + (sel.l1 !== sel.l2 ? '-' + sel.l2 : '');

  const el = document.createElement('div');
  el.className = 'lc-composer';
  el.innerHTML =
    '<div class="lc-header">💬 Add Comment</div>' +
    '<textarea class="lc-textarea" placeholder="Type your review note..." rows="3"></textarea>' +
    '<div class="lc-footer">' +
      '<span class="lc-hint">' + lineRef + '</span>' +
      '<div class="lc-btns">' +
        '<button class="lc-btn lc-cancel">Cancel</button>' +
        '<button class="lc-btn lc-save">Save ⌘↵</button>' +
      '</div>' +
    '</div>';
  document.body.append(el);
  composerEl = el;

  const ta = el.querySelector('.lc-textarea');
  setTimeout(() => ta.focus(), 50);

  ta.addEventListener('keydown', e => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); saveComment(sel, ta.value); }
    if (e.key === 'Escape') closeComposer();
  });
  el.querySelector('.lc-cancel').onclick = () => closeComposer();
  el.querySelector('.lc-save').onclick = () => saveComment(sel, ta.value);
}

function closeComposer() { if (composerEl) { composerEl.remove(); composerEl = null; } }

async function saveComment(sel, body) {
  if (!body.trim()) { showToast('!', 'Comment is empty'); return; }
  const d = doc_();
  if (!d) return;
  try {
    const base = document.baseURI || (location.origin + '/');
    const r = await fetch(new URL('api/local-comments', base), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: d.path, line: sel.l1, endLine: sel.l2, body: body.trim() }),
    });
    if (r.ok) {
      localNotes.push(await r.json());
      closeComposer();
      showToast('✓', 'Comment saved');
      renderGutterBadges();
      renderPanel();
      updateBadge();
    } else { showToast('!', 'Failed to save'); }
  } catch { showToast('!', 'Failed to save comment'); }
}

async function deleteComment(id) {
  try {
    const base = document.baseURI || (location.origin + '/');
    await fetch(new URL('api/local-comments/' + id, base), { method: 'POST' });
    localNotes = localNotes.filter(c => c.id !== id);
    renderGutterBadges();
    renderPanel();
    updateBadge();
    showToast('✓', 'Comment deleted');
  } catch {}
}

async function clearAllComments() {
  if (!localNotes.length) return;
  const base = document.baseURI || (location.origin + '/');
  for (const c of [...localNotes]) {
    try { await fetch(new URL('api/local-comments/' + c.id, base), { method: 'POST' }); } catch {}
  }
  localNotes = [];
  renderGutterBadges();
  renderPanel();
  updateBadge();
  showToast('✓', 'All comments cleared');
}

function exportComments() {
  if (!localNotes.length) { showToast('!', 'No comments to export'); return; }
  const out = JSON.stringify(localNotes, null, 2);
  copyToClipboard(out, 'Copied ' + localNotes.length + ' comments as JSON');
}

// --- Panel rendering ---

function renderPanel() {
  const list = $('#lc-panel-list');
  if (!list) return;
  list.innerHTML = '';

  if (!localNotes.length) {
    list.innerHTML = '<div class="lc-empty">No comments yet.<br>Hover a line number → Add Comment (Alt+N)</div>';
    return;
  }

  // Group by file
  const byFile = {};
  for (const c of localNotes) {
    (byFile[c.path] = byFile[c.path] || []).push(c);
  }
  for (const [filePath, fileComments] of Object.entries(byFile)) {
    const fileHeader = document.createElement('div');
    fileHeader.className = 'lc-file-header';
    fileHeader.textContent = filePath;
    list.append(fileHeader);

    for (const c of fileComments) {
      const item = document.createElement('div');
      item.className = 'lc-item';
      const lineRef = ':' + c.line + (c.endLine > c.line ? '-' + c.endLine : '');
      item.innerHTML =
        '<div class="lc-item-head">' +
          '<span class="lc-item-line">' + lineRef + '</span>' +
          '<button class="lc-item-del" title="Delete">✕</button>' +
        '</div>' +
        '<div class="lc-item-body">' + escHtml(c.body) + '</div>';
      item.querySelector('.lc-item-line').onclick = () => {
        openFile(c.path, { line: c.line });
        setTimeout(renderGutterBadges, 300);
      };
      item.querySelector('.lc-item-del').onclick = () => deleteComment(c.id);
      list.append(item);
    }
  }
}

function updateBadge() {
  const tabCount = $('#lc-tab-count');
  const panelCount = $('#lc-panel-count');
  const n = localNotes.length;
  if (tabCount) { tabCount.textContent = n; tabCount.hidden = n === 0; }
  if (panelCount) { panelCount.textContent = n; }
}

// --- Gutter badges + line highlighting ---

function renderGutterBadges() {
  // Clear old badges and highlights
  for (const old of document.querySelectorAll('.lc-gutter-badge')) old.remove();
  for (const old of document.querySelectorAll('.lc-highlight')) old.classList.remove('lc-highlight');

  const d = doc_();
  if (!d) return;
  const fileComments = localNotes.filter(c => c.path === d.path);
  if (!fileComments.length) return;

  const rows = document.querySelectorAll('#rows .row');
  for (const row of rows) {
    const lineNum = +row.dataset.l;
    if (!lineNum) continue;
    const matching = fileComments.filter(c => lineNum >= c.line && lineNum <= c.endLine);
    if (!matching.length) continue;

    // Highlight the row
    row.classList.add('lc-highlight');

    // Add gutter badge
    const gutter = row.querySelector('.n');
    if (!gutter) continue;
    const badge = document.createElement('span');
    badge.className = 'lc-gutter-badge';
    badge.textContent = '💬';
    badge.title = matching.map(c => c.body).join('\n---\n');
    gutter.append(badge);
  }
}

function escHtml(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

export function commentsForFile(path) { return localNotes.filter(c => c.path === path); }
export function commentsForLine(path, line) { return localNotes.filter(c => c.path === path && line >= c.line && line <= c.endLine); }
export function allComments() { return localNotes; }
export function commentCount() { return localNotes.length; }
