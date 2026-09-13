// web/src/mermaid-view.js
// The card a rendered Mermaid diagram lives in. Rendering itself — the lazy
// import, parse, theme config and failure fallback — belongs to mermaid.js;
// this module only takes finished SVG text and presents it: a fitted inline
// card that opens a modal zoom card on click.

/* ---------- stage ---------- */

const MM_MIN = 0.25;       // never shrink past a quarter of natural size
const MM_MAX = 4;          // or grow past 400%
const MM_STEP = 1.25;
const MM_PAD = 28;         // breathing room inside a stage viewport
const MM_INLINE_H = 0.8;   // inline cards grow to at most this share of the window
const MM_INLINE_CAP = 720; // ...and never past this many pixels
const MM_KEEP = 72;        // a pan may push the diagram off-screen, never fully

/* Natural size of a rendered diagram, read from its viewBox. */
function svgSize(svg) {
  const box = svg && svg.viewBox ? svg.viewBox.baseVal : null;
  return { w: box && box.width ? box.width : 0, h: box && box.height ? box.height : 0 };
}

/* Build the scrollable stage a diagram lives on. Mermaid's own inline sizing
   is dropped: the card decides the scale from the viewBox. */
function buildStage(svgText) {
  const view = document.createElement('div');
  view.className = 'mm-view';
  const stage = document.createElement('div');
  stage.className = 'mm-stage';
  stage.innerHTML = svgText;
  view.append(stage);
  const svg = stage.querySelector('svg');
  if (svg) {
    svg.removeAttribute('style');
    svg.style.maxWidth = 'none';
  }
  const { w, h } = svgSize(svg);
  return { view, stage, svg, w, h };
}

/* Scale that shows the whole diagram inside the viewport, never above 100%. */
function fitScale(view, w, h, maxH) {
  const cw = view.clientWidth || 0;
  if (!w || !cw) return 1;
  const byW = (cw - MM_PAD) / w;
  const byH = maxH && h ? (maxH - MM_PAD) / h : Infinity;
  return Math.min(1, byW, byH);
}

/* How tall an inline card may grow before the diagram must shrink: tall
   diagrams stay readable instead of being squeezed into a fixed box. */
function inlineMaxH() {
  const vh = window.innerHeight || 800;
  return Math.min(MM_INLINE_CAP, Math.round(vh * MM_INLINE_H));
}

function sizeSvg(svg, w, h, z) {
  if (svg && w && h) {
    svg.setAttribute('width', String(Math.round(w * z)));
    svg.setAttribute('height', String(Math.round(h * z)));
  }
}

/* Wheel zoom and drag pan over one stage. The zoom card owns scale and
   translation; drag moves the stage itself, so panning works at every zoom
   level, including a diagram that already fits the viewport. */
function wireStage(view, { zoom, pan }) {
  view.addEventListener('wheel', e => {
    e.preventDefault(); // in the zoom card the wheel zooms; it never scrolls the page
    const r = view.getBoundingClientRect();
    zoom(e.deltaY < 0 ? MM_STEP : 1 / MM_STEP, e.clientX - r.left, e.clientY - r.top);
  }, { passive: false });

  // Drag to pan. Move/up live on the window so a fast drag that leaves the
  // viewport keeps panning instead of stranding the gesture.
  view.addEventListener('pointerdown', e => {
    if (e.button !== 0) return;
    e.preventDefault(); // no text selection while panning
    let px = e.clientX, py = e.clientY;
    view.classList.add('dragging');
    const move = ev => {
      pan(ev.clientX - px, ev.clientY - py);
      px = ev.clientX;
      py = ev.clientY;
    };
    const end = () => {
      view.classList.remove('dragging');
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', end);
      window.removeEventListener('pointercancel', end);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', end);
    window.addEventListener('pointercancel', end);
  });
}

/* ---------- cards ---------- */

const fitted = new WeakMap(); // inline wrapper -> its ResizeObserver

/* The inline card: fitted to the preview column, and any click on the diagram
   opens the zoom card. The card follows the column's width, so window or
   sidebar resizes refit the diagram instead of overflowing it. */
export function mountDiagram(target, svgText) {
  const { view, svg, w, h } = buildStage(svgText);

  const bar = document.createElement('div');
  bar.className = 'mm-bar';
  const label = document.createElement('span');
  label.className = 'mm-label';
  label.textContent = 'mermaid';
  const zoomBtn = document.createElement('button');
  zoomBtn.type = 'button';
  zoomBtn.className = 'mm-btn';
  zoomBtn.title = 'Zoom diagram';
  zoomBtn.setAttribute('aria-label', 'Zoom diagram');
  zoomBtn.textContent = '\u2922';
  bar.append(label, zoomBtn);

  const card = document.createElement('div');
  card.className = 'mm-card';
  card.append(bar, view);
  target.replaceChildren(card);

  const refit = () => {
    const cap = inlineMaxH();
    const z = fitScale(view, w, h, cap);
    sizeSvg(svg, w, h, z);
    view.style.height = Math.min(Math.max(140, Math.round(h * z + MM_PAD)), cap + MM_PAD) + 'px';
  };
  refit();
  fitted.get(target)?.disconnect();
  if (typeof ResizeObserver !== 'undefined') {
    const ro = new ResizeObserver(refit);
    ro.observe(view);
    fitted.set(target, ro);
  }
  view.title = 'Click to zoom';

  view.addEventListener('click', () => openZoomCard(svgText));
  zoomBtn.addEventListener('click', () => openZoomCard(svgText));
}

/* The zoom card: a modal with the zoom controls and drag pan over a fixed
   viewport. Scale and translation live in this closure; the stage is moved
   with a transform, so dragging always moves the diagram — even when it is
   smaller than the viewport. Backdrop click, the close button and Escape
   dismiss. */
function openZoomCard(svgText) {
  const { view, stage, svg, w, h } = buildStage(svgText);
  const backdrop = document.createElement('div');
  backdrop.className = 'mm-backdrop';
  const card = document.createElement('div');
  card.className = 'mm-zoom-card';
  card.tabIndex = -1;

  const bar = document.createElement('div');
  bar.className = 'mm-bar';
  const label = document.createElement('span');
  label.className = 'mm-label';
  label.textContent = 'mermaid';
  const controls = document.createElement('div');
  controls.className = 'mm-zoom';

  const pct = document.createElement('button');
  pct.type = 'button';
  pct.className = 'mm-pct';
  pct.title = 'Reset zoom to fit';
  pct.setAttribute('aria-label', 'Reset zoom to fit');
  const mkBtn = (text, cls, title, on) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'mm-btn' + (cls ? ' ' + cls : '');
    b.textContent = text;
    b.title = title;
    b.setAttribute('aria-label', title);
    b.addEventListener('click', on);
    return b;
  };

  let z = 1, tx = 0, ty = 0;

  const draw = () => {
    sizeSvg(svg, w, h, z);
    stage.style.transform = 'translate(' + Math.round(tx) + 'px,' + Math.round(ty) + 'px)';
    pct.textContent = Math.round(z * 100) + '%';
  };

  /* Never let a pan or a zoom push the diagram fully out of sight: keep at
     least MM_KEEP pixels of it inside the viewport on each axis. */
  const keepInside = () => {
    const vw = view.clientWidth || 0, vh = view.clientHeight || 0;
    const cw = w * z, ch = h * z;
    const kx = Math.min(MM_KEEP, cw), ky = Math.min(MM_KEEP, ch);
    tx = Math.min(vw - kx, Math.max(kx - cw, tx));
    ty = Math.min(vh - ky, Math.max(ky - ch, ty));
  };

  const fit = () => {
    const cw = view.clientWidth || 0, ch = view.clientHeight || 0;
    if (!w || !cw) return 1;
    const byW = (cw - MM_PAD) / w;
    const byH = ch && h ? (ch - MM_PAD) / h : Infinity;
    return Math.min(2, byW, byH);
  };

  const reset = () => {
    z = Math.min(MM_MAX, Math.max(MM_MIN, fit()));
    tx = ((view.clientWidth || 0) - w * z) / 2;
    ty = ((view.clientHeight || 0) - h * z) / 2;
    draw();
  };

  /* Scale around a point in view coordinates: the diagram point under the
     cursor stays put. */
  const zoomAt = (factor, cx, cy) => {
    const before = z;
    z = Math.min(MM_MAX, Math.max(MM_MIN, z * factor));
    const k = z / before;
    tx = cx - (cx - tx) * k;
    ty = cy - (cy - ty) * k;
    keepInside();
    draw();
  };

  const close = () => {
    backdrop.remove();
    document.removeEventListener('keydown', onKey);
  };
  const onKey = e => { if (e.key === 'Escape') close(); };

  controls.append(
    mkBtn('\u2212', '', 'Zoom out', () => { zoomAt(1 / MM_STEP, (view.clientWidth || 0) / 2, (view.clientHeight || 0) / 2); }),
    pct,
    mkBtn('+', '', 'Zoom in', () => { zoomAt(MM_STEP, (view.clientWidth || 0) / 2, (view.clientHeight || 0) / 2); }),
    mkBtn('\u00d7', 'mm-close', 'Close', close),
  );
  pct.addEventListener('click', reset);
  bar.append(label, controls);
  card.append(bar, view);
  backdrop.append(card);
  document.body.append(backdrop);
  view.title = 'Scroll to zoom \u00b7 Drag to pan';
  reset();

  wireStage(view, {
    zoom: zoomAt,
    pan: (dx, dy) => {
      tx += dx;
      ty += dy;
      keepInside();
      draw();
    },
  });
  backdrop.addEventListener('click', e => { if (e.target === backdrop) close(); });
  document.addEventListener('keydown', onKey);
  card.focus();
}
