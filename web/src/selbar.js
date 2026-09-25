// web/src/selbar.js
import { $, S, doc_, keyLabel } from './state.js';
import { on } from './bus.js';
import { vp, copyToClipboard, showToast } from './ui.js';
import { render } from './renderer.js';
import { findReferences } from './lsp.js';
import { fitStatus } from './status.js';

/* While code is selected, the left of the status bar trades its navigation
   buttons for actions on the selection, and hands them back once the selection
   is gone. Unlike a floating menu it never covers code, and its buttons stay put. */

const status = $('#status');
const statsEl = $('#sel-stats');
// Queried rather than imported from diff.js, to keep the modules independent.
const diffviewEl = $('#diffview');

// e.code, not e.key: Option+letter types a symbol on macOS.
export const SEL_KEYS = { KeyC: 'copy-ref', KeyA: 'copy-agent', KeyU: 'usages', KeyE: 'agent-edit', KeyR: 'review-comment' };

/* Editing lives in agent.js, which registers itself here on load. Keeping the
   dependency one-way means selbar imports nothing back and the two never form
   a cycle; the button simply does nothing when no harness is configured. */
let agentHandler = null;
export function setAgentHandler(fn) { agentHandler = fn; }

/* Same one-way registration for PR review comments (pr.js), active only in a
   `px0 pr ...` session. */
let reviewHandler = null;
export function setReviewHandler(fn) { reviewHandler = fn; }
// Read-only accessor so the diff gutter's pencil (linecomment.js) can offer
// "Add Review Comment" directly, without duplicating the registration.
export function getReviewHandler() { return reviewHandler; }

let current = null;   // the selection the bar is showing, or null when it is not
let allText = null;   // Ctrl+A: promise of the S.selAll file's full text
let allInfo = null;   // the bar's view of that selection, once the text arrives

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
  if (!vp.contains(range.commonAncestorContainer)) return null;

  const text = sel.toString().trim();
  if (!text) return null;

  const startEl = /** @type {HTMLElement|null} */ (range.startContainer.nodeType === 1 ? range.startContainer : range.startContainer.parentElement);
  const endEl = /** @type {HTMLElement|null} */ (range.endContainer.nodeType === 1 ? range.endContainer : range.endContainer.parentElement);

  const startRow = /** @type {HTMLElement|null} */ (startEl ? startEl.closest('.row') : null);
  const endRow = /** @type {HTMLElement|null} */ (endEl ? endEl.closest('.row') : null);

  let l1 = d.cur || 1, l2 = d.cur || 1;
  if (startRow && startRow.dataset.l) l1 = +startRow.dataset.l;
  if (endRow && endRow.dataset.l) l2 = +endRow.dataset.l;

  if (l1 > l2) { const tmp = l1; l1 = l2; l2 = tmp; }

  return { text, l1, l2, path: d.path };
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
    parts.push(code ? code.textContent : '');
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
  const text = parts.join('\n').trim();
  if (!text) return null;
  return { text, l1, l2, delL1, delL2, path: d.path, fromDiff: true, side };
}

const selectionRef = ({ path, l1, l2 }) => path + ':' + (l1 === l2 ? l1 : l1 + '-' + l2);

function showSelectionBar(info) {
  current = info;
  const lines = info.l2 - info.l1 + 1;
  statsEl.textContent = (lines === 1 ? '1 line' : lines + ' lines') + ' · ' +
    info.text.length.toLocaleString() + ' chars';
  status.classList.add('selecting');
  fitStatus();
}

export function hideSelectionBar() {
  closeSelMenu();
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
  if (!d || !allText) return false;
  allText.then(t => copyToClipboard(t, 'Copied ' + d.path + ' (' + d.total.toLocaleString() + ' lines)'), () => {});
  return true;
}

/* Runs one of the bar's actions on the current selection. Returns false when the
   bar is not showing, so a shortcut can fall through to the browser. */
export function runSelectionAction(act, triggerBtn = null) {
  if (!current) {
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
  const { text, path } = current;
  const ref = selectionRef(current);
  const targetBtn = triggerBtn || $('#footer-sel [data-sel="' + act + '"]');
  if (act === 'copy-ref') {
    copyToClipboard(ref, 'Copied', targetBtn);
  } else if (act === 'copy-agent') {
    const ext = path.split('.').pop() || '';
    const lineStr = current.l1 === current.l2 ? 'line ' + current.l1 : 'lines ' + current.l1 + '-' + current.l2;
    const snippet = '@' + path + ' ' + lineStr + '\n```' + ext + '\n' + text + '\n```';
    copyToClipboard(snippet, 'Copied', targetBtn);
  } else if (act === 'agent-edit') {
    if (!agentHandler) return false;
    agentHandler(current);
  } else if (act === 'review-comment') {
    if (!reviewHandler) return false;
    reviewHandler(current);
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
  if (menu && !menu.hidden) menu.hidden = true;
}

// Exported so pr.js can append "Add Review Comment" in a PR review session
// without selbar needing to know PR review exists.
export const SEL_MENU_ITEMS = [
  { sel: 'copy-ref', label: 'Copy Ref', keys: 'Alt+C' },
  { sel: 'copy-agent', label: 'Copy with Context', keys: 'Alt+A' },
  { sel: 'agent-edit', label: 'Edit Inline', keys: 'Alt+E' },
  { sel: 'usages', label: 'Find Usages', keys: 'Alt+U' },
];

/* Built from the selection actions each time, keeping Find Usages in context menu. */
function openSelMenu(x, y) {
  menu.replaceChildren();
  for (const item of SEL_MENU_ITEMS) {
    const btn = document.createElement('button');
    btn.className = 'sel-menu-item';
    btn.dataset.sel = item.sel;
    btn.setAttribute('role', 'menuitem');
    btn.title = item.label + (item.keys ? ` (${keyLabel(item.keys)})` : '');
    const label = document.createElement('span');
    label.textContent = item.label;
    btn.append(label);
    const kbd = document.createElement('kbd');
    kbd.className = 'footer-kbd';
    kbd.textContent = keyLabel(item.keys);
    btn.append(kbd);
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
  /* Enter only once the gesture is over: swapping the footer mid-drag flickers.
     Once showing, follow the selection as it changes, and leave when it collapses
     or moves out of the editor. Listening on the document catches a drag that
     is released outside the viewport. */
  document.addEventListener('mouseup', () => setTimeout(updateSelectionBar, 20));
  vp.addEventListener('keyup', e => { if (e.shiftKey) setTimeout(updateSelectionBar, 20); });
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
      closeSelMenu();
      runSelectionAction(btn.dataset.sel, btn);
    });
  }
  if (!menu) return;

  /* Only a right click on a selection is taken over. Anywhere else the browser
     keeps its own menu, which is what a right click on plain code expects. */
  document.addEventListener('contextmenu', e => {
    if (menu.contains(e.target)) { e.preventDefault(); return; }
    const inCode = vp.contains(e.target) || (diffviewEl && !diffviewEl.hidden && diffviewEl.contains(e.target));
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
