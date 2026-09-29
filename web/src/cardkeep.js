// web/src/cardkeep.js
// Shared open/close lifetime for the floating cards that follow the pointer:
// the LSP hover card (hover.js) and the unpushed-commit card (unpushed.js).
//
// Both used to close while the pointer was still on its way into them. Two
// things caused that, and both are fixed here:
//
//   1. A card armed its hide from a `mouseleave` on whatever sat underneath
//      it and gave the pointer a fixed window to arrive. Cross the gap slower
//      than that -- or pause on the way -- and the card was gone before the
//      pointer reached it, with nothing left listening to cancel the timer:
//      the pointer had already left every element that was.
//   2. "Is the pointer still near the card" was measured from an anchor
//      captured when the card was *requested*, not when it opened. A hover
//      response can take seconds, so the card could appear anchored to where
//      the pointer used to be, and the very next move read as "left the card".
//
// So: one document-level pointer track, so the pointer is never unobserved;
// one timer per card; and every decision made against where the pointer is
// now, not where it was.

export const CARD_GRACE = 340; // ms the pointer may spend outside before the card closes

let ptr = null;                // live pointer position in client coords, or null when it left the window
const keepers = new Set();
let ptrWired = false;

function tickAll() {
  for (const k of keepers) k.tick();
}

function wirePointerTrack() {
  if (ptrWired) return;
  ptrWired = true;
  // Capture, so a card's own stopPropagation can't blind the tracker, and
  // passive, since this runs on every move and never cancels anything.
  addEventListener('mousemove', e => {
    ptr = { x: e.clientX, y: e.clientY };
    tickAll();
  }, { capture: true, passive: true });
  // relatedTarget null on a mouseout from the document means the pointer left
  // the window outright -- no further moves are coming, so decide now.
  document.addEventListener('mouseout', e => {
    if (e.relatedTarget === null) { ptr = null; tickAll(); }
  }, true);
  addEventListener('blur', () => { ptr = null; tickAll(); });
}

/* Where the pointer is right now, or null if it has left the window. */
export function pointerPos() { return ptr; }

/**
 * Keeps `el` open while the pointer is inside it or heading towards it from
 * the anchor it was opened at, and calls `hide` once it is neither.
 *
 * @param {HTMLElement} el   the card; its `hidden` attribute is the open flag
 * @param {object} opts
 * @param {() => void} opts.hide   close the card (the caller owns the actual hiding)
 * @param {number} [opts.grace]    ms outside before hiding
 * @param {number} [opts.pad]      px of slack around the card's own box
 * @param {number} [opts.corridor] px of slack around the anchor-to-card travel path
 */
export function makeCardKeeper(el, { hide, grace = CARD_GRACE, pad = 16, corridor = 52 } = {}) {
  wirePointerTrack();
  let anchor = null;  // client coords the card was opened from
  let timer = 0;
  let inside = false; // pointer is over the card itself

  /* Inside the card (plus slack), or anywhere in the rectangle spanned by the
     anchor and the card -- the path a pointer travelling to the card takes. */
  function near(p) {
    if (!p || !el || el.hidden) return false;
    const r = el.getBoundingClientRect();
    if (p.x >= r.left - pad && p.x <= r.right + pad &&
        p.y >= r.top - pad && p.y <= r.bottom + pad) return true;
    if (!anchor) return false;
    return p.x >= Math.min(anchor.x, r.left) - corridor &&
           p.x <= Math.max(anchor.x, r.right) + corridor &&
           p.y >= Math.min(anchor.y, r.top) - corridor &&
           p.y <= Math.max(anchor.y, r.bottom) + corridor;
  }

  const keeper = {
    /* Start watching, anchored where the pointer opened the card. */
    open(at) {
      anchor = at || ptr;
      inside = false;
      keeper.cancel();
    },
    /* Move the corridor's origin to where the pointer is now. Call this when
       an awaited card finally renders, so a slow response cannot strand the
       anchor somewhere the pointer has long since left. */
    reanchor(at) { anchor = at || ptr || anchor; },
    cancel() { if (timer) { clearTimeout(timer); timer = 0; } },
    arm() { if (!timer) timer = setTimeout(() => { timer = 0; hide(); }, grace); },
    /* The card is gone: stop watching until the next open(). */
    close() { keeper.cancel(); anchor = null; inside = false; },
    tick() {
      if (!el || el.hidden || !anchor) return;
      if (inside || near(ptr)) keeper.cancel();
      else keeper.arm();
    },
  };

  if (el) {
    el.addEventListener('mouseenter', () => { inside = true; keeper.cancel(); });
    el.addEventListener('mouseleave', () => { inside = false; keeper.tick(); });
    // Tabbing to a button inside the card counts as being in it.
    el.addEventListener('focusin', () => keeper.cancel());
  }

  keepers.add(keeper);
  return keeper;
}
