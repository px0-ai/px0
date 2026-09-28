// web/src/hover.js
import { $, esc, S, doc_, api, isMac, MOD, withKeys } from './state.js';
import { vp, editor, copyToClipboard } from './ui.js';
import { paint } from './renderer.js';
import { setLspState } from './status.js';
import { wordAtPoint } from './cursor.js';
import { gotoDefinition, findReferences } from './lsp.js';
import { showCalls } from './calls.js';
import { diffview } from './diff.js';

export const hovercard = $('#hovercard');
export const HOVER_DELAY = 380;   // rest time before the card opens
export const HOVER_KEEP = 26;     // px the pointer may drift before the card closes

let hoverTimer = 0, hoverSeq = 0, moveRAF = 0, pendingMove = null, pointerAt = null;
let hideTimer = 0;

const sameWord = (a, b) => !!a && !!b && a.line === b.line && a.col === b.col && a.word === b.word;

function isInsideCard(x, y, padding = 16) {
  if (!hovercard || hovercard.hidden || x == null || y == null) return false;
  const r = hovercard.getBoundingClientRect();
  return x >= r.left - padding && x <= r.right + padding &&
         y >= r.top - padding && y <= r.bottom + padding;
}

function isInCorridor(x, y, buffer = 48) {
  if (!S.hoverAnchor || !hovercard || hovercard.hidden || x == null || y == null) return false;
  const r = hovercard.getBoundingClientRect();
  const minX = Math.min(S.hoverAnchor.x, r.left) - buffer;
  const maxX = Math.max(S.hoverAnchor.x, r.right) + buffer;
  const minY = Math.min(S.hoverAnchor.y, r.top) - buffer;
  const maxY = Math.max(S.hoverAnchor.y, r.bottom) + buffer;
  return x >= minX && x <= maxX && y >= minY && y <= maxY;
}

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
      if (diffview) diffview.classList.toggle('linking', !!at);
      paint();
    }
    clearTimeout(hoverTimer);
    clearTimeout(hideTimer);
    hideHover();
    return;
  }

  if (S.link) {
    S.link = null;
    vp.classList.remove('linking');
    if (diffview) diffview.classList.remove('linking');
    paint();
  }

  // When hovercard is visible, keep it alive if moving towards or inside it
  if (S.hoverAnchor && !hovercard.hidden) {
    if (isInsideCard(x, y) || isInCorridor(x, y)) {
      clearTimeout(hideTimer);
      hideTimer = 0;
      clearTimeout(hoverTimer);
      return; // actively over the card or navigating towards it
    }
    // Pointer has left both the card and the navigation corridor
    if (!hideTimer) {
      hideTimer = setTimeout(hideHover, 280);
    }
    return;
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

  clearTimeout(hideTimer);
  hideTimer = 0;
  S.hover = at;
  S.hoverAnchor = { x, y };
  const refPath = d.path + ':' + at.line;
  hovercard.innerHTML =
    (j.signature ? '<div class="sig">' + j.signature + '</div>' : '') +
    (j.doc ? '<div class="doc">' + esc(j.doc) + '</div>' : '') +
    '<div class="actions">' +
      '<button id="hc-def-src" title="' + withKeys('Jump to definition in Source ({Mod+Click})') + '">Definition (Source)</button>' +
      '<button id="hc-def-diff" title="' + withKeys('Jump to definition in Diff ({Mod+Alt+Click})') + '">Definition (Diff)</button>' +
      '<button id="hc-copy-ref" title="Copy file and line reference">Copy Ref</button>' +
      '<button id="hc-copy-ai" title="Copy snippet with file path and line numbers">Copy with Context</button>' +
      '<button id="hc-find-refs" title="Find all usages across codebase">Usages</button>' +
      '<button id="hc-calls" title="' + withKeys('Trace callers and callees ({Alt+Shift+H})') + '">Calls</button>' +
    '</div>' +
    '<div class="foot"><b>' + esc(j.server || 'lsp') + '</b>' +
    '<span>' + withKeys('{Mod+Click} source') + '</span>' +
    '<span>' + withKeys('{Mod+Alt+Click} diff') + '</span>' +
    '<span>' + withKeys('{Shift+F12} usages') + '</span></div>';

  const btnDefSrc = hovercard.querySelector('#hc-def-src');
  const btnDefDiff = hovercard.querySelector('#hc-def-diff');
  const btnRef = hovercard.querySelector('#hc-copy-ref');
  const btnAi = hovercard.querySelector('#hc-copy-ai');
  const btnRefs = hovercard.querySelector('#hc-find-refs');
  const btnCalls = hovercard.querySelector('#hc-calls');

  if (btnDefSrc) btnDefSrc.onclick = (e) => {
    e.stopPropagation();
    hideHover();
    gotoDefinition(at, { view: 'source' });
  };
  if (btnDefDiff) btnDefDiff.onclick = (e) => {
    e.stopPropagation();
    hideHover();
    gotoDefinition(at, { view: 'diff' });
  };
  if (btnRef) btnRef.onclick = (e) => {
    e.stopPropagation();
    copyToClipboard(refPath, 'Copied', btnRef);
  };
  if (btnAi) btnAi.onclick = (e) => {
    e.stopPropagation();
    const lineText = (d.lines && d.lines[at.line - 1]) || at.word || '';
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
  clearTimeout(hideTimer);
  hideTimer = 0;
  hoverSeq++;
  S.hover = null;
  S.hoverAnchor = null;
  if (!hovercard.hidden) { hovercard.hidden = true; hovercard.innerHTML = ''; }
}

export function clearLink() {
  clearTimeout(hoverTimer);
  clearTimeout(hideTimer);
  hideTimer = 0;
  hideHover();
  if (S.link) {
    S.link = null;
    vp.classList.remove('linking');
    if (diffview) diffview.classList.remove('linking');
    paint();
  }
}

export function initHover() {
  hovercard.addEventListener('mouseenter', () => {
    clearTimeout(hideTimer);
    hideTimer = 0;
  });
  hovercard.addEventListener('mousemove', () => {
    clearTimeout(hideTimer);
    hideTimer = 0;
  });
  hovercard.addEventListener('mouseleave', () => {
    clearTimeout(hideTimer);
    hideTimer = setTimeout(hideHover, 280);
  });

  const attachPointer = (el) => {
    if (!el) return;
    el.addEventListener('mousemove', e => {
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
    el.addEventListener('mouseleave', (e) => {
      pointerAt = null;
      if (e.relatedTarget && (hovercard === e.relatedTarget || hovercard.contains(e.relatedTarget))) {
        return;
      }
      if (isInsideCard(e.clientX, e.clientY)) {
        return;
      }
      if (!hovercard.hidden) {
        if (!hideTimer) hideTimer = setTimeout(hideHover, 280);
      } else {
        clearLink();
      }
    });
    el.addEventListener('scroll', () => { clearTimeout(hoverTimer); hideHover(); }, { passive: true });
    el.addEventListener('mousedown', (e) => {
      if (e.target.closest('#hovercard')) return;
      hideHover();
    });
    el.addEventListener('dblclick', e => {
      clearTimeout(hideTimer);
      hideTimer = 0;
      hoverAt(e.clientX, e.clientY);
    });
  };

  attachPointer(vp);
  attachPointer(diffview);

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
