// web/src/hover.js
import { $, esc, S, doc_, api, isMac, MOD, withKeys } from './state.js';
import { vp, editor, copyToClipboard } from './ui.js';
import { paint } from './renderer.js';
import { setLspState } from './status.js';
import { wordAtPoint } from './cursor.js';
import { gotoDefinition, findReferences } from './lsp.js';
import { showCalls } from './calls.js';
import { diffview } from './diff.js';
import { makeCardKeeper, pointerPos } from './cardkeep.js';

export const hovercard = $('#hovercard');
export const HOVER_DELAY = 380;   // rest time before the card opens
// How far the pointer may have drifted while the hover request was in flight
// before the answer is stale enough to drop rather than show somewhere else.
const HOVER_DRIFT = 220;

let hoverTimer = 0, hoverSeq = 0, moveRAF = 0, pendingMove = null, pointerAt = null;

/* The card's lifetime belongs to the keeper, which watches the pointer across
   the whole document -- not to a mouseleave on whatever the card happens to
   overlap. See cardkeep.js for why that distinction is the whole fix. */
const hoverKeeper = makeCardKeeper(hovercard, { hide: hideHover });

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
      if (diffview) diffview.classList.toggle('linking', !!at);
      paint();
    }
    clearTimeout(hoverTimer);
    hideHover();
    return;
  }

  if (S.link) {
    S.link = null;
    vp.classList.remove('linking');
    if (diffview) diffview.classList.remove('linking');
    paint();
  }

  // An open card is the keeper's business; moving under it must not re-arm a
  // second request for the word the card is already describing.
  if (!hovercard.hidden) { clearTimeout(hoverTimer); return; }

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

  /* The request can take seconds. Open the card where the pointer is *now*,
     not where it rested when the request went out -- an anchor that far behind
     puts the card outside its own corridor and closes it on the next move. If
     the pointer has gone somewhere else entirely, drop the answer instead. */
  const live = pointerPos();
  if (live && Math.hypot(live.x - x, live.y - y) > HOVER_DRIFT) return;
  const ax = live ? live.x : x, ay = live ? live.y : y;

  S.hover = at;
  S.hoverAnchor = { x: ax, y: ay };
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
  placeHover(ax, ay);
  hoverKeeper.open({ x: ax, y: ay });
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
  hoverKeeper.close();
  hoverSeq++;
  S.hover = null;
  S.hoverAnchor = null;
  if (!hovercard.hidden) { hovercard.hidden = true; hovercard.innerHTML = ''; }
}

export function clearLink() {
  clearTimeout(hoverTimer);
  hideHover();
  if (S.link) {
    S.link = null;
    vp.classList.remove('linking');
    if (diffview) diffview.classList.remove('linking');
    paint();
  }
}

export function initHover() {
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
    /* Leaving the code only cancels a card that hasn't opened yet. One that
       has is the keeper's, which is still watching the pointer out here. */
    el.addEventListener('mouseleave', () => {
      pointerAt = null;
      clearTimeout(hoverTimer);
      if (hovercard.hidden) clearLink();
    });
    el.addEventListener('scroll', () => { clearTimeout(hoverTimer); hideHover(); }, { passive: true });
    el.addEventListener('mousedown', (e) => {
      if (e.target.closest('#hovercard')) return;
      hideHover();
    });
    el.addEventListener('dblclick', e => {
      hoverKeeper.cancel();
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
