// web/src/hover.js
import { $, esc, S, doc_, api, isMac, MOD, withKeys } from './state.js';
import { vp, editor, copyToClipboard } from './ui.js';
import { paint } from './renderer.js';
import { setLspState } from './status.js';
import { wordAtPoint } from './cursor.js';
import { findReferences } from './lsp.js';
import { showCalls } from './calls.js';

export const hovercard = $('#hovercard');
export const HOVER_DELAY = 380;   // rest time before the card opens
export const HOVER_KEEP = 26;     // px the pointer may drift before the card closes

let hoverTimer = 0, hoverSeq = 0, moveRAF = 0, pendingMove = null, pointerAt = null;

const sameWord = (a, b) => !!a && !!b && a.line === b.line && a.col === b.col && a.word === b.word;

/* Hit-testing a point costs a few milliseconds: it forces layout and walks the
   line's nodes. Far too much to spend on every animation frame, so it runs only
   when the modifier is actually held, or once the pointer has come to rest and
   the card is about to open. Everything on the hot path below is arithmetic. */
export function onMove({ x, y, mod }) {
  if (mod) {
    const at = doc_() ? wordAtPoint(x, y) : null;
    if (!sameWord(at, S.link)) {
      S.link = at;
      vp.classList.toggle('linking', !!at);
      paint();
    }
    clearTimeout(hoverTimer);
    hideHover();
    return;
  }

  if (S.link) { S.link = null; vp.classList.remove('linking'); paint(); }

  // Dismiss an open card once the pointer has clearly left what it described.
  if (S.hoverAnchor) {
    if (!hovercard.hidden) {
      const rect = hovercard.getBoundingClientRect();
      if (x >= rect.left - 4 && x <= rect.right + 4 && y >= rect.top - 4 && y <= rect.bottom + 4) return;
    }
    const dx = x - S.hoverAnchor.x, dy = y - S.hoverAnchor.y;
    if (dx * dx + dy * dy > HOVER_KEEP * HOVER_KEEP) hideHover();
    else return; // still on the same word: nothing to do
  }

  if (S.settings && (S.settings['lsp.hover.enabled'] === false || S.settings['lsp.enabled'] === false)) return;
  if (S.lsp.state !== 'ready' && S.lsp.state !== 'indexing') return;
  clearTimeout(hoverTimer);
  hoverTimer = setTimeout(() => hoverAt(x, y), HOVER_DELAY);
}

export function hoverAt(x, y) {
  const at = doc_() ? wordAtPoint(x, y) : null;
  if (at && at.word) showHover(at, x, y);
}

export async function showHover(at, x, y) {
  const d = doc_();
  if (!d || at.path !== d.path) return;
  const seq = ++hoverSeq;
  let j;
  try { j = await api('/api/lsp/hover', { path: d.path, line: at.line, col: at.col, wait: 4000 }); }
  catch { return; }
  if (seq !== hoverSeq || doc_() !== d) return;   // the pointer moved on
  setLspState(j);
  if (!j || j.empty || (!j.signature && !j.doc)) return;

  S.hover = at;
  S.hoverAnchor = { x, y };
  const refPath = d.path + ':' + at.line;
  hovercard.innerHTML =
    (j.signature ? '<div class="sig">' + j.signature + '</div>' : '') +
    (j.doc ? '<div class="doc">' + esc(j.doc) + '</div>' : '') +
    '<div class="actions">' +
      '<button id="hc-copy-ref" title="Copy file and line reference">Copy Ref</button>' +
      '<button id="hc-copy-ai" title="Copy snippet with file path and line numbers">Copy with Context</button>' +
      '<button id="hc-find-refs" title="Find all usages across codebase">Usages</button>' +
      '<button id="hc-calls" title="' + withKeys('Trace callers and callees ({Alt+Shift+H})') + '">Calls</button>' +
    '</div>' +
    '<div class="foot"><b>' + esc(j.server || 'lsp') + '</b>' +
    '<span>' + withKeys('{Mod+Click} definition') + '</span>' +
    '<span>' + withKeys('{Shift+F12} references') + '</span></div>';

  const btnRef = hovercard.querySelector('#hc-copy-ref');
  const btnAi = hovercard.querySelector('#hc-copy-ai');
  const btnRefs = hovercard.querySelector('#hc-find-refs');

  if (btnRef) btnRef.onclick = (e) => {
    e.stopPropagation();
    copyToClipboard(refPath, 'Copied', btnRef);
  };
  if (btnAi) btnAi.onclick = (e) => {
    e.stopPropagation();
    const lineText = d.lines[at.line - 1] || at.word || '';
    const ext = d.path.split('.').pop() || '';
    const lineStr = 'line ' + at.line;
    const text = '@' + d.path + ' ' + lineStr + '\n```' + ext + '\n' + lineText + '\n```';
    copyToClipboard(text, 'Copied', btnAi);
  };
  if (btnRefs) btnRefs.onclick = (e) => {
    e.stopPropagation();
    hideHover();
    findReferences(at.word);
  };
  const btnCalls = hovercard.querySelector('#hc-calls');
  if (btnCalls) btnCalls.onclick = (e) => {
    e.stopPropagation();
    hideHover();
    S.at = at;
    showCalls(at);
  };

  hovercard.hidden = false;
  placeHover(x, y);
}

/* Anchor below the pointer, flipping above or inward when that would overflow
   the editor. */
export function placeHover(x, y) {
  const host = editor.getBoundingClientRect();
  const card = hovercard.getBoundingClientRect();
  let left = x - host.left + 6;
  let top = y - host.top + 20;
  if (left + card.width > host.width - 12) left = Math.max(8, host.width - card.width - 12);
  if (top + card.height > host.height - 8) {
    const above = y - host.top - card.height - 12;
    top = above > 8 ? above : Math.max(8, host.height - card.height - 8);
  }
  hovercard.style.left = left + 'px';
  hovercard.style.top = top + 'px';
}

export function hideHover() {
  hoverSeq++;
  S.hover = null;
  S.hoverAnchor = null;
  if (!hovercard.hidden) { hovercard.hidden = true; hovercard.innerHTML = ''; }
}

export function clearLink() {
  clearTimeout(hoverTimer);
  hideHover();
  if (S.link) { S.link = null; vp.classList.remove('linking'); paint(); }
}

export function initHover() {
  /* One mousemove handler drives both behaviours: with a modifier held the word
     becomes a link, without one it gets an info card after a short rest. */
  vp.addEventListener('mousemove', e => {
    pointerAt = { x: e.clientX, y: e.clientY };
    pendingMove = { x: e.clientX, y: e.clientY, mod: e[MOD] };
    if (moveRAF) return;
    moveRAF = requestAnimationFrame(() => {
      moveRAF = 0;
      const m = pendingMove;
      pendingMove = null;
      if (m) onMove(m);
    });
  });

  vp.addEventListener('mouseleave', () => { pointerAt = null; clearLink(); });
  vp.addEventListener('scroll', () => { clearTimeout(hoverTimer); hideHover(); }, { passive: true });
  vp.addEventListener('mousedown', (e) => {
    if (e.target.closest('#hovercard')) return;
    hideHover();
  });

  /* The modifier can be pressed or released without the pointer moving, and the
     underline has to follow. */
  const modKey = isMac ? 'Meta' : 'Control';   // the key MOD tests; Ctrl+click on a Mac is a right click
  addEventListener('keydown', e => {
    if (e.key === modKey && pointerAt) onMove({ ...pointerAt, mod: true });
  });
  addEventListener('keyup', e => {
    if (e.key === modKey) clearLink();
  });
}
