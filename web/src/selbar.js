// web/src/selbar.js
import { $, S, doc_, keyLabel } from './state.js';
import { on } from './bus.js';
import { vp, rowsEl, copyToClipboard, copyRichToClipboard, showToast } from './ui.js';
import { render, toPos, rowFor } from './renderer.js';
import { findReferences } from './lsp.js';
import { fitStatus } from './status.js';
import { domToMarkdown, getFragmentPlainText, getFragmentHtml, getMarkdownRangeLines, getDocRaw } from './markdown.js';

/* While code is selected, the left of the status bar trades its navigation
   buttons for actions on the selection, and hands them back once the selection
   is gone. Unlike a floating menu it never covers code, and its buttons stay put. */

const status = $('#status');
const statsEl = $('#sel-stats');
// Queried rather than imported from diff.js, to keep the modules independent.
const diffviewEl = $('#diffview');
const mdviewEl = $('#mdview');

// e.code, not e.key: Option+letter types a symbol on macOS.
export const SEL_KEYS = {
  KeyC: 'copy-ref',
  KeyA: 'copy-agent',
  KeyU: 'usages',
  KeyE: 'agent-edit',
  KeyR: 'review-comment',
  KeyT: 'thread',
  KeyM: 'copy-markdown',
  KeyS: 'copy-source',
};

/* Editing lives in agent.js, which registers itself here on load. Keeping the
   dependency one-way means selbar imports nothing back and the two never form
   a cycle; the button simply does nothing when no harness is configured. */
let agentHandler = null;
export function setAgentHandler(fn) { agentHandler = fn; }

/* Threads (thread.js) hook in the same way. */
let threadHandler = null;
export function setThreadHandler(fn) { threadHandler = fn; }

/* Same one-way registration for PR review comments (pr.js), active only in a
   `px0 pr ...` session. */
let reviewHandler = null;
export function setReviewHandler(fn) { reviewHandler = fn; }
// Read-only accessor so the diff gutter's pencil (linecomment.js) can offer
// "Add Review Comment" directly, without duplicating the registration.
export function getReviewHandler() { return reviewHandler; }

let current = null;   // the selection the bar is showing, or null when it is not
let pinnedInfo = null; // the line a gutter button opened the menu for; independent of any text selection
let allText = null;   // Ctrl+A: promise of the S.selAll file's full text
let allInfo = null;   // the bar's view of that selection, once the text arrives

function lineToText(body) {
  if (body === undefined || body === null) return '';
  if (!body.includes('<')) return body;
  const t = document.createElement('div');
  t.innerHTML = body;
  return t.textContent || '';
}

function extractEditorSelection(range, d) {
  const p1 = toPos(range.startContainer, range.startOffset);
  const p2 = toPos(range.endContainer, range.endOffset);

  let l1, c1, l2, c2;
  if (p1 && p2) {
    if (p1.line < p2.line || (p1.line === p2.line && p1.col <= p2.col)) {
      l1 = p1.line; c1 = p1.col;
      l2 = p2.line; c2 = p2.col;
    } else {
      l1 = p2.line; c1 = p2.col;
      l2 = p1.line; c2 = p1.col;
    }
  } else {
    // Fallback: search intersecting rows
    const rows = [];
    if (rowsEl) {
      for (const r of rowsEl.children) {
        if (range.intersectsNode(r)) rows.push(r);
      }
    }
    if (!rows.length) return null;
    l1 = +rows[0].dataset.l;
    l2 = +rows[rows.length - 1].dataset.l;
    c1 = 0;
    c2 = rows[rows.length - 1].querySelector('.c')?.textContent.length || 0;
  }

  // Gather code lines from l1 to l2 exclusively from .c elements or d.lines
  const parts = [];
  for (let line = l1; line <= l2; line++) {
    let lineText = '';
    const row = rowFor(line);
    if (row) {
      const codeEl = row.querySelector('.c');
      lineText = codeEl ? codeEl.textContent : '';
    } else if (d.lines && d.lines[line - 1] !== undefined) {
      lineText = lineToText(d.lines[line - 1]);
    }
    parts.push(lineText);
  }

  if (!parts.length) return null;

  if (parts.length === 1) {
    parts[0] = parts[0].slice(c1, c2);
  } else {
    parts[0] = parts[0].slice(c1);
    parts[parts.length - 1] = parts[parts.length - 1].slice(0, c2);
  }

  const text = parts.join('\n');
  if (!text) return null;

  return { text, l1, l2, path: d.path, isEditor: true };
}

function extractMarkdownSelection(range, d) {
  const frag = range.cloneContents();
  const text = getFragmentPlainText(frag).trim();
  if (!text) return null;

  const html = getFragmentHtml(frag);
  const rawMarkdown = domToMarkdown(frag).trim();
  const { l1, l2 } = getMarkdownRangeLines(range);

  return {
    text,
    html,
    rawMarkdown,
    l1: l1 || 1,
    l2: l2 || d.total || 1,
    path: d.path,
    isMarkdownPreview: true,
  };
}

export function getSelectedRangeInfo() {
  if (S.selAll) return allInfo;
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || !sel.rangeCount) return null;
  const d = doc_();
  if (!d) return null;

  const range = sel.getRangeAt(0);
  if (diffviewEl && !diffviewEl.hidden && diffviewEl.contains(range.commonAncestorContainer)) {
    return diffSelection(range, d);
  }
  if (mdviewEl && !mdviewEl.hidden && mdviewEl.contains(range.commonAncestorContainer)) {
    return extractMarkdownSelection(range, d);
  }
  if (!vp.contains(range.commonAncestorContainer)) return null;

  return extractEditorSelection(range, d);
}

/* The part of a code cell the selection actually covers: the whole cell for
   rows in the middle, a clipped slice for the first and last. */
function clipToRange(el, range) {
  const r = document.createRange();
  r.selectNodeContents(el);
  if (range.compareBoundaryPoints(Range.START_TO_START, r) > 0) r.setStart(range.startContainer, range.startOffset);
  if (range.compareBoundaryPoints(Range.END_TO_END, r) < 0) r.setEnd(range.endContainer, range.endOffset);
  return r.toString();
}

/* A diff selection is anchored to the working-tree lines stamped on its rows,
   on either side of a split. A selection of deleted lines alone has nothing on
   disk, so it is anchored to the lines either side of where they were. The text
   is gathered from the code cells alone, leaving out the line-number and +/-
   gutters a raw selection would otherwise sweep up, and a context line showing
   on both sides of a split is taken once. */
function diffSelection(range, d) {
  let l1 = Infinity, l2 = -Infinity, at1 = Infinity, at2 = -Infinity;
  let old1 = Infinity, old2 = -Infinity;
  const parts = [];
  const seen = new Set();
  for (const el of diffviewEl.querySelectorAll('[data-l], [data-at], [data-old-l]')) {
    if (!range.intersectsNode(el)) continue;
    const code = el.querySelector('.diff-code');
    if (el.dataset.l !== undefined) {
      const n = +el.dataset.l;
      if (n < l1) l1 = n;
      if (n > l2) l2 = n;
      if (seen.has(n)) continue;
      seen.add(n);
    } else if (el.dataset.at !== undefined) {
      const n = +el.dataset.at;
      if (n < at1) at1 = n;
      if (n > at2) at2 = n;
    }
    if (el.dataset.oldL !== undefined && el.dataset.l === undefined) {
      const n = +el.dataset.oldL;
      if (n < old1) old1 = n;
      if (n > old2) old2 = n;
    }
    parts.push(code ? clipToRange(code, range) : '');
  }
  if (!parts.length) return null;
  const isDeletedOnly = (l1 === Infinity);
  let side = 'RIGHT';
  let delL1 = 0, delL2 = 0;
  if (isDeletedOnly) {
    side = 'LEFT';
    delL1 = old1 !== Infinity ? old1 : 1;
    delL2 = old2 !== Infinity ? old2 : delL1;
    const last = Math.max(1, d.total || 1);
    l1 = Math.min(last, Math.max(1, at1 - 1));
    l2 = Math.max(l1, Math.min(last, at2));
  }
  const text = parts.join('\n');
  if (!text) return null;
  return { text, l1, l2, delL1, delL2, path: d.path, fromDiff: true, side };
}

const selectionRef = ({ path, l1, l2 }) => path + ':' + (l1 === l2 ? l1 : l1 + '-' + l2);

function showSelectionBar(info) {
  current = info;
  const lines = info.l2 - info.l1 + 1;
  statsEl.textContent = (lines === 1 ? '1 line' : lines + ' lines') + ' · ' +
    info.text.length.toLocaleString() + ' chars';

  const d = doc_();
  const footerSel = bar();
  if (footerSel) {
    const btnSource = footerSel.querySelector('[data-sel="copy-source"]');
    const btnPreviewText = footerSel.querySelector('[data-sel="copy-preview-text"]');
    const btnMarkdown = footerSel.querySelector('[data-sel="copy-markdown"]');

    if (info.isMarkdownPreview) {
      if (btnSource) btnSource.hidden = true;
      if (btnPreviewText) btnPreviewText.hidden = false;
      if (btnMarkdown) {
        btnMarkdown.hidden = false;
        const l = btnMarkdown.querySelector('.footer-btn-label');
        if (l) l.textContent = 'Copy Raw Markdown';
      }
    } else {
      if (btnSource) btnSource.hidden = false;
      if (btnPreviewText) btnPreviewText.hidden = true;
      if (btnMarkdown) {
        btnMarkdown.hidden = !(d && d.markdown);
        const l = btnMarkdown.querySelector('.footer-btn-label');
        if (l) l.textContent = 'Copy Markdown';
      }
    }
  }

  status.classList.add('selecting');
  fitStatus();
}

export function hideSelectionBar() {
  if (!pinnedInfo) closeSelMenu(); // a menu opened from the gutter is not tied to a selection
  if (!current) return;
  current = null;
  if (statsEl) statsEl.textContent = '';
  status.classList.remove('selecting');
  fitStatus();
}

export function updateSelectionBar() {
  const info = getSelectedRangeInfo();
  if (info) showSelectionBar(info); else hideSelectionBar();
}

/* Ctrl+A selects the open file, not the page around it. Only the rows in view
   exist in the DOM, so a native selection could never span the file: S.selAll
   marks the doc, paint() shades its rows, and the text comes whole from /api/raw. */
export function selectAll() {
  const d = doc_();
  if (!d) return;
  window.getSelection()?.removeAllRanges();
  S.selAll = d;
  allInfo = null;
  const rawUrl = new URL('api/raw?path=' + encodeURIComponent(d.path), document.baseURI || location.href).href;
  const text = allText = fetch(rawUrl)
    .then(r => { if (!r.ok) throw new Error(r.statusText); return r.text(); });
  text.then(t => {
    if (allText !== text) return; // cleared or selected again meanwhile
    allInfo = { text: t, l1: 1, l2: d.total, path: d.path };
    showSelectionBar(allInfo);
  }, () => {
    if (allText !== text) return;
    clearSelectAll();
    showToast('!', 'Could not read ' + d.path);
  });
}

export function clearSelectAll() {
  if (!S.selAll) return;
  S.selAll = null; allText = null; allInfo = null;
  render();
  hideSelectionBar();
}

/* Ctrl+C on a whole-file selection. Returns false when there is none, so the
   browser copies a native selection as usual. */
export function copySelectAll() {
  const d = S.selAll;
  if (!d) return false;
  if (allInfo && allInfo.text) {
    copyToClipboard(allInfo.text, 'Copied ' + d.path + ' (' + d.total.toLocaleString() + ' lines)');
    return true;
  }
  if (!allText) return false;
  allText.then(t => copyToClipboard(t, 'Copied ' + d.path + ' (' + d.total.toLocaleString() + ' lines)')).catch(() => {});
  return true;
}

/* Runs one of the bar's actions on the current selection. Returns false when the
   bar is not showing, so a shortcut can fall through to the browser. */
export function runSelectionAction(act, triggerBtn = null, override = null) {
  const target = override || current;
  if (!target) {
    if (act === 'thread') {
      const d = doc_();
      if (d && threadHandler) {
        const line = d.cur || 1;
        threadHandler({ text: (d.lines && d.lines[line - 1]) || '', l1: line, l2: line, path: d.path });
        return true;
      }
    }
    if (act === 'agent-edit') {
      const d = doc_();
      if (d && agentHandler) {
        const line = d.cur || 1;
        const text = (d.lines && d.lines[line - 1]) || '';
        agentHandler({ text, l1: line, l2: line, path: d.path });
        return true;
      }
    }
    return false;
  }
  const { text, path } = target;
  const ref = selectionRef(target);
  const targetBtn = triggerBtn || $('#footer-sel [data-sel="' + act + '"]');

  if (act === 'copy-source') {
    const lineCount = target.l2 - target.l1 + 1;
    const msg = lineCount === 1 ? 'Copied 1 line' : `Copied ${lineCount} lines`;
    copyToClipboard(target.text, msg, targetBtn);
  } else if (act === 'copy-preview-text') {
    copyRichToClipboard(target.text, target.html || '', 'Copied formatted text', targetBtn);
  } else if (act === 'copy-markdown') {
    if (target.rawMarkdown) {
      copyToClipboard(target.rawMarkdown, 'Copied raw Markdown', targetBtn);
    } else {
      const d = doc_();
      if (d) {
        getDocRaw(d).then(raw => {
          const lines = raw.split(/\r?\n/);
          const l1 = Math.max(1, target.l1 || 1);
          const l2 = Math.min(lines.length, target.l2 || lines.length);
          const sub = lines.slice(l1 - 1, l2).join('\n');
          copyToClipboard(sub || raw, 'Copied raw Markdown', targetBtn);
        }).catch(() => {
          copyToClipboard(target.text, 'Copied Markdown', targetBtn);
        });
      }
    }
  } else if (act === 'copy-ref') {
    copyToClipboard(ref, 'Copied', targetBtn);
  } else if (act === 'copy-agent') {
    const ext = path.split('.').pop() || '';
    const lineStr = target.l1 === target.l2 ? 'line ' + target.l1 : 'lines ' + target.l1 + '-' + target.l2;
    const snippet = '@' + path + ' ' + lineStr + '\n```' + ext + '\n' + text + '\n```';
    copyToClipboard(snippet, 'Copied', targetBtn);
  } else if (act === 'agent-edit') {
    if (!agentHandler) return false;
    agentHandler(target);
  } else if (act === 'thread') {
    if (!threadHandler) return false;
    threadHandler(target);
  } else if (act === 'review-comment') {
    if (!reviewHandler) return false;
    reviewHandler(target);
  } else if (act === 'usages') {
    findReferences(text.split(/\s+/)[0] || text);
  } else {
    return false;
  }
  return true;
}

/* ---------- context menu: the same actions, next to the pointer ---------- */

const menu = $('#sel-menu');

export function closeSelMenu() {
  pinnedInfo = null;
  if (menu && !menu.hidden) menu.hidden = true;
}

/* The gutter's thread button: the same actions as the right-click menu, aimed
   at one line. Find Usages needs a symbol, which a whole line is not, and
   Add Review Comment only makes sense on a line GitHub knows about. */
export function openLineMenu(info, x, y) {
  closeSelMenu();
  pinnedInfo = info;
  openSelMenu(x, y, item => item.sel !== 'usages' && item.sel !== 'copy-preview-text' && (item.sel !== 'review-comment' || !!info.fromDiff));
}

// Exported so pr.js can append "Add Review Comment" in a PR review session
// without selbar needing to know PR review exists.
export const SEL_MENU_ITEMS = [
  { sel: 'thread', label: 'Start Agent Thread', keys: 'Alt+T' },
  { sel: 'agent-edit', label: 'Edit Inline', keys: 'Alt+E' },
  { sel: 'copy-source', label: 'Copy Source', keys: 'Mod+C', forCode: true },
  { sel: 'copy-preview-text', label: 'Copy Formatted Text', keys: 'Mod+C', forPreview: true },
  { sel: 'copy-markdown', label: 'Copy Raw Markdown', keys: 'Alt+M', forMarkdown: true },
  { sel: 'copy-ref', label: 'Copy Ref', keys: 'Alt+C' },
  { sel: 'copy-agent', label: 'Copy with Context', keys: 'Alt+A' },
  { sel: 'usages', label: 'Find Usages', keys: 'Alt+U', forCode: true },
];

/* Built from the selection actions each time, keeping Find Usages in context menu. */
function openSelMenu(x, y, keep = (item) => true) {
  const d = doc_();
  const info = pinnedInfo || current;
  const isPreview = !!(info && info.isMarkdownPreview);
  const isMdDoc = !!(d && d.markdown);

  menu.replaceChildren();
  for (const item of SEL_MENU_ITEMS) {
    if (!keep(item)) continue;
    if (isPreview && item.forCode) continue;
    if (!isPreview && item.forPreview) continue;
    if (item.forMarkdown && !isMdDoc && !isPreview) continue;

    const btn = document.createElement('button');
    btn.className = 'sel-menu-item';
    btn.dataset.sel = item.sel;
    btn.setAttribute('role', 'menuitem');
    btn.title = item.label + (item.keys ? ` (${keyLabel(item.keys)})` : '');
    const label = document.createElement('span');
    label.textContent = item.label;
    btn.append(label);
    if (item.keys) {
      const kbd = document.createElement('kbd');
      kbd.className = 'footer-kbd';
      kbd.textContent = keyLabel(item.keys);
      btn.append(kbd);
    }
    menu.append(btn);
  }
  menu.hidden = false;
  // Open toward the pointer's bottom-right, flipping at the window's edges.
  const w = menu.offsetWidth, h = menu.offsetHeight;
  menu.style.left = Math.max(4, x + w > innerWidth - 4 ? x - w : x) + 'px';
  menu.style.top = Math.max(4, y + h > innerHeight - 4 ? y - h : y) + 'px';
}

const bar = () => $('#footer-sel');

export function initSelectionBar() {
  document.addEventListener('copy', (e) => {
    const act = /** @type {HTMLElement|null} */ (document.activeElement);
    if (act && (act.tagName === 'INPUT' || act.tagName === 'TEXTAREA' || act.isContentEditable)) {
      return;
    }

    if (S.selAll) {
      const d = S.selAll;
      if (allInfo && allInfo.text) {
        e.preventDefault();
        e.clipboardData.setData('text/plain', allInfo.text);
        showToast('✓', 'Copied ' + d.path + ' (' + d.total.toLocaleString() + ' lines)');
        return;
      }
    }
    const info = getSelectedRangeInfo();
    if (info) {
      e.preventDefault();
      if (info.isMarkdownPreview) {
        e.clipboardData.setData('text/plain', info.text);
        if (info.html) {
          e.clipboardData.setData('text/html', info.html);
        }
        showToast('✓', 'Copied formatted text (' + info.text.length.toLocaleString() + ' chars)');
      } else {
        e.clipboardData.setData('text/plain', info.text);
        const lines = info.l2 - info.l1 + 1;
        showToast('✓', 'Copied ' + (lines === 1 ? '1 line' : lines + ' lines'));
      }
    }
  });

  /* Enter only once the gesture is over: swapping the footer mid-drag flickers.
     Once showing, follow the selection as it changes, and leave when it collapses
     or moves out of the editor. Listening on the document catches a drag that
     is released outside the viewport. */
  document.addEventListener('mouseup', () => setTimeout(updateSelectionBar, 20));
  vp.addEventListener('keyup', e => { if (e.shiftKey) setTimeout(updateSelectionBar, 20); });
  if (mdviewEl) mdviewEl.addEventListener('keyup', e => { if (e.shiftKey) setTimeout(updateSelectionBar, 20); });
  document.addEventListener('selectionchange', () => updateSelectionBar());
  // Any click ends a whole-file selection, except on the bar's buttons or a viewport scrollbar.
  document.addEventListener('mousedown', e => {
    // A right click opens the menu for the selection, so it must not end it.
    const target = /** @type {HTMLElement|null} */ (e.target);
    if (!S.selAll || e.button === 2 || target?.closest?.('#footer-sel, #sel-menu')) return;
    if (e.target === vp && (e.offsetX >= vp.clientWidth || e.offsetY >= vp.clientHeight)) return;
    clearSelectAll();
  }, true);

  // Pressing a button must not clear the selection it is about to act on.
  for (const el of [bar(), menu]) {
    if (!el) continue;
    el.addEventListener('mousedown', e => e.preventDefault());
    el.addEventListener('click', e => {
      const btn = e.target.closest('[data-sel]');
      if (!btn) return;
      const info = pinnedInfo; // closing the menu forgets it
      closeSelMenu();
      runSelectionAction(btn.dataset.sel, btn, info);
    });
  }
  if (!menu) return;

  /* Only a right click on a selection is taken over. Anywhere else the browser
     keeps its own menu, which is what a right click on plain code expects. */
  document.addEventListener('contextmenu', e => {
    if (menu.contains(e.target)) { e.preventDefault(); return; }
    const inCode = vp.contains(e.target) ||
      (diffviewEl && !diffviewEl.hidden && diffviewEl.contains(e.target)) ||
      (mdviewEl && !mdviewEl.hidden && mdviewEl.contains(e.target));
    if (!inCode) { closeSelMenu(); return; }
    updateSelectionBar();
    if (!current) { closeSelMenu(); return; }
    e.preventDefault();
    openSelMenu(e.clientX, e.clientY);
  });
  document.addEventListener('mousedown', e => {
    if (!menu.hidden && !menu.contains(e.target)) closeSelMenu();
  }, true);
  addEventListener('keydown', e => { if (e.key === 'Escape') closeSelMenu(); });
  addEventListener('resize', closeSelMenu);
  addEventListener('blur', closeSelMenu);
  document.addEventListener('scroll', closeSelMenu, true);
  on('tab:activated', clearSelectAll);
}
