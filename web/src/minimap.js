// web/src/minimap.js
/* The minimap (Alt+K): a canvas beside the code drawing each line as coloured
   blocks, 2px tall and 1px per character, with a slider marking the view.

   Its cost follows the minimap's height, never the file's length. Only the lines
   the minimap can show are drawn, from the highlighted HTML the code view has
   already fetched; each line's blocks are parsed once and cached against that
   HTML. Lines that have not arrived stay blank while scrolling and are fetched
   only once scrolling rests, so a fling through a huge file costs the server no
   extra highlighting. A scroll that moves the drawing by less than a device
   pixel only moves the slider.

   Drawing writes pixels straight into an ImageData buffer, with theme colours
   blended over the background once per theme, and hands it to the canvas in a
   single putImageData. That is about ten times faster than filling canvas paths,
   which matters because smooth scrolling redraws on nearly every frame. */
import { $, S, doc_, LH } from './state.js';
import { vp, editor } from './ui.js';
import { layout, render, ensureChunks, updateEditorOptionControls, setAfterPaint } from './renderer.js';
import { previewing } from './markdown.js';

const ROW = 2;             // CSS px per source line
const PAD = 4;             // CSS px left of the text, where change marks go
const MIN_EDITOR = 600;    // narrower editors keep every pixel for code
const FETCH_IDLE = 150;    // ms of rest before fetching lines the minimap lacks
const TEXT_ALPHA = 0.7;    // text over the background
const FIND_ALPHA = 0.45;   // find-match bands over the background

// Token classes from highlight.go's classFor, each drawn in the colour its CSS
// rule uses. Index 0 is plain text; ge, gs and g carry no colour of their own.
const TOKENS = ['k', 'kt', 'nf', 'nc', 'nb', 'nv', 'no', 'na', 'nt', 'nd', 'np', 's', 'm', 'o', 'p', 'c', 'cp', 'gi', 'gd', 'gh', 'err'];
const TOKEN_VAR = { gh: 'accent-fg' };
const CLASS_IDX = Object.fromEntries(TOKENS.map((t, i) => [t, i + 1]));

const box = $('#minimap');
const canvas = $('canvas', box);
const slider = $('#minimap-slider');
const ctx = canvas.getContext('2d', { alpha: false });
const LITTLE_ENDIAN = new Uint8Array(new Uint32Array([1]).buffer)[0] === 1;

let shown = false;
let colors = null;
let themeVer = 0;
let last = [];          // what the canvas holds, to skip redraws that change nothing
let geo = null;         // slider geometry from the last sync, for pointer input
let drag = null;
let fetchTimer = 0;
let img = null, px = null; // the canvas's pixels, as ImageData and a 32-bit view of it
const caches = new WeakMap(); // doc -> { cols, lines: Map<index, { h, r }> }

export function toggleMinimap(forced) {
  S.minimap = typeof forced === 'boolean' ? forced : !S.minimap;
  try { localStorage.setItem('px0.minimap', S.minimap ? 'true' : 'false'); } catch {}
  updateEditorOptionControls();
  render();
}

/* Runs at the end of every paint. Cheap when nothing moved. */
function syncMinimap() {
  const d = doc_();
  const on = !!(S.minimap && d && d.lines && !d.diffMode && !previewing(d) && editor.clientWidth >= MIN_EDITOR);
  if (on !== shown) {
    shown = on;
    box.hidden = !on;
    editor.classList.toggle('mm-on', on);
    last = [];
    layout(); // the viewport just changed width
    render();
  }
  if (!on) return;

  const w = box.clientWidth, h = box.clientHeight;
  if (!w || !h) return;
  const dpr = window.devicePixelRatio || 1;
  const cw = Math.round(w * dpr), ch = Math.round(h * dpr);
  if (!img || img.width !== cw || img.height !== ch) {
    canvas.width = cw; canvas.height = ch;
    img = ctx.createImageData(cw, ch);
    px = new Uint32Array(img.data.buffer);
    last = [];
  }

  /* The minimap's content is the scroll height at ROW px per LH. When it fits,
     it sits still and the slider moves 1:1; otherwise both move, in proportion,
     so the slider reaches the bottom exactly when the view does. */
  const scrollable = Math.max(0, vp.scrollHeight - vp.clientHeight);
  const contentH = vp.scrollHeight * ROW / LH;
  const sliderH = Math.min(h, vp.clientHeight * ROW / LH);
  const f = contentH <= h ? ROW / LH : scrollable ? (h - sliderH) / scrollable : 0;
  const sliderTop = vp.scrollTop * f;
  const mmTop = Math.max(0, vp.scrollTop * ROW / LH - sliderTop);
  geo = { f, sliderTop, sliderH, mmTop };
  slider.style.height = sliderH + 'px';
  slider.style.transform = 'translateY(' + sliderTop + 'px)';

  const top = Math.round(mmTop * dpr);
  const key = [d, top, cw, ch, themeVer, d.linesVer, d.cur, S.find, d.gutter];
  if (key.length === last.length && key.every((v, i) => v === last[i])) return;
  last = key;
  draw(d, top / dpr, dpr);
}

function draw(d, mmTop, dpr) {
  const c = colors || (colors = resolveColors());
  const W = img.width, H = img.height;
  const rowH = Math.max(1, Math.ceil(ROW * dpr));
  const glyphH = Math.max(1, Math.floor(ROW * dpr * 0.75));
  const lift = Math.floor((rowH - glyphH) / 2);
  const left = Math.round(PAD * dpr);
  const first = Math.max(0, Math.floor(mmTop / ROW));
  const end = Math.min(d.total, Math.ceil((mmTop + H / dpr) / ROW));
  const yOf = i => Math.round((i * ROW - mmTop) * dpr);
  const fill = (x, y, w, h, v) => {
    const x0 = Math.max(0, x), x1 = Math.min(W, x + w), y1 = Math.min(H, y + h);
    if (x0 >= x1) return;
    for (let r = Math.max(0, y); r < y1; r++) px.fill(v, r * W + x0, r * W + x1);
  };

  px.fill(c.bg);
  if (d.cur > first && d.cur <= end) fill(0, yOf(d.cur - 1), W, rowH, c.cur);
  const hits = S.find && !S.find.preview ? S.find.byLine : null;
  if (hits && hits.size) {
    for (let i = first; i < end; i++) if (hits.has(i + 1)) fill(0, yOf(i), W, rowH, c.mark);
  }

  const cols = Math.max(1, Math.floor((W - left) / dpr));
  let cache = caches.get(d);
  if (!cache || cache.cols !== cols) { cache = { cols, lines: new Map() }; caches.set(d, cache); }
  let missing = false;
  for (let i = first; i < end; i++) {
    const html = d.lines[i];
    if (html === undefined) { missing = true; continue; }
    let e = cache.lines.get(i);
    if (!e || e.h !== html) { e = { h: html, r: runsOf(html, cols) }; cache.lines.set(i, e); }
    const r = e.r, y = yOf(i) + lift;
    for (let k = 0; k < r.length; k += 3) {
      fill(left + Math.round(r[k] * dpr), y, Math.max(1, Math.round(r[k + 1] * dpr)), glyphH, c.tok[r[k + 2]]);
    }
  }

  if (d.gutter) {
    const markW = Math.max(1, Math.round(2 * dpr));
    for (let i = first; i < end; i++) {
      const m = d.gutter.marks.get(i + 1);
      const v = m === 'add' ? c.add : m === 'mod' ? c.mod : d.gutter.dels.has(i + 1) ? c.del : 0;
      if (v) fill(0, yOf(i), markW, rowH, v);
    }
  }
  ctx.putImageData(img, 0, 0);

  // Keep the cache to the neighbourhood of the view.
  if (cache.lines.size > (end - first) * 4 + 2000) {
    for (const k of cache.lines.keys()) if (k < first - 1000 || k > end + 1000) cache.lines.delete(k);
  }

  if (missing) {
    clearTimeout(fetchTimer);
    fetchTimer = setTimeout(() => {
      if (shown && doc_() === d && d.lines) ensureChunks(d, first, end);
    }, FETCH_IDLE);
  }
}

/* One line of highlighted HTML (flat <i class=x>text</i> tokens around escaped
   text) -> Uint16Array of [column, length, colour] blocks. Spaces split blocks,
   tabs stop every 4 columns, an entity is one character, and scanning stops at
   the minimap's width so a minified megabyte line costs no more than a short one. */
export function runsOf(html, maxCols) {
  const out = [];
  let col = 0, color = 0, start = -1, runColor = 0, i = 0;
  const n = html.length;
  const close = () => { if (start >= 0) { out.push(start, col - start, runColor); start = -1; } };
  while (i < n && col < maxCols) {
    const ch = html.charCodeAt(i);
    if (ch === 60) { // <
      const gt = html.indexOf('>', i);
      if (gt < 0) break;
      color = html.charCodeAt(i + 1) === 47 ? 0 : CLASS_IDX[html.slice(i + 9, gt)] || 0; // </i> or <i class=
      i = gt + 1;
      continue;
    }
    if (ch === 32 || ch === 9 || ch === 13) {
      close();
      if (ch === 9) col += 4 - col % 4; else if (ch === 32) col++;
      i++;
      continue;
    }
    if (ch === 38) { const semi = html.indexOf(';', i); i = semi < 0 ? i + 1 : semi + 1; } else i++;
    if (start >= 0 && runColor !== color) close();
    if (start < 0) { start = col; runColor = color; }
    col++;
  }
  close();
  return Uint16Array.from(out);
}

/* Theme colours as packed pixels, each already blended over the background at
   the opacity it is drawn with. The browser parses the CSS colour, whatever
   syntax the theme used, by painting it into a 1x1 canvas. */
function resolveColors() {
  const cs = getComputedStyle(document.documentElement);
  const v = name => cs.getPropertyValue('--' + name).trim();
  const probe = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
  probe.canvas.width = probe.canvas.height = 1;
  const rgba = css => {
    probe.clearRect(0, 0, 1, 1);
    probe.fillStyle = '#888'; // kept if css does not parse
    probe.fillStyle = css;
    probe.fillRect(0, 0, 1, 1);
    return probe.getImageData(0, 0, 1, 1).data;
  };
  const bg = rgba(v('bg') || '#000');
  const mix = (css, alpha) => {
    const f = rgba(css), k = alpha * f[3] / 255;
    const [r, g, b] = [0, 1, 2].map(i => Math.round(bg[i] + (f[i] - bg[i]) * k));
    return (LITTLE_ENDIAN ? (255 << 24 | b << 16 | g << 8 | r) : (r << 24 | g << 16 | b << 8 | 255)) >>> 0;
  };
  const fg = v('fg') || '#888';
  return {
    bg: mix(v('bg') || '#000', 1), cur: mix(v('cur') || v('bg4') || fg, 1), mark: mix(v('mark-active') || fg, FIND_ALPHA),
    add: mix(v('gi') || fg, 1), mod: mix(v('accent') || fg, 1), del: mix(v('gd') || fg, 1),
    tok: [fg, ...TOKENS.map(t => v(TOKEN_VAR[t] || t) || fg)].map(css => mix(css, TEXT_ALPHA)),
  };
}

function pointerY(e) { return e.clientY - box.getBoundingClientRect().top; }

export function initMinimap() {
  setAfterPaint(syncMinimap);

  // Colours come from the theme's variables; a theme switch sets data-theme.
  new MutationObserver(() => { colors = null; themeVer++; render(); })
    .observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });

  /* Press on the slider to drag it; press anywhere else to centre that line and
     keep dragging from there. */
  box.addEventListener('pointerdown', e => {
    if (e.button !== 0 || !shown || !geo) return;
    e.preventDefault();
    const y = pointerY(e);
    const onSlider = y >= geo.sliderTop && y < geo.sliderTop + geo.sliderH;
    if (!onSlider) {
      const line = Math.floor((y + geo.mmTop) / ROW);
      vp.scrollTop = Math.max(0, line * LH - vp.clientHeight / 2);
    }
    drag = { offset: onSlider ? y - geo.sliderTop : geo.sliderH / 2 };
    box.setPointerCapture(e.pointerId);
    box.classList.add('dragging');
  });
  box.addEventListener('pointermove', e => {
    if (!drag || !geo || !geo.f) return;
    vp.scrollTop = (pointerY(e) - drag.offset) / geo.f;
  });
  const endDrag = () => { drag = null; box.classList.remove('dragging'); };
  box.addEventListener('pointerup', endDrag);
  box.addEventListener('pointercancel', endDrag);
  box.addEventListener('lostpointercapture', endDrag);

  box.addEventListener('wheel', e => {
    const unit = e.deltaMode === 1 ? LH : e.deltaMode === 2 ? vp.clientHeight : 1;
    vp.scrollTop += e.deltaY * unit;
  }, { passive: true });
}
