// web/src/mermaid.js
// Mermaid rendering for Markdown previews. Diagrams draw as soon as a preview
// is shown, in the order they appear, through one queue; the vendored ESM build
// in web/lib/mermaid/ (see scripts/vendor-mermaid.sh) is imported on the first
// diagram, so a preview without fences never fetches or parses it. Theme
// variables are read from the tokens documented in STYLING.md at initialize
// time. The card a finished diagram is mounted in lives in
// web/src/mermaid-view.js.

import { mountDiagram } from './mermaid-view.js';

/* Keep in lockstep with scripts/vendor-mermaid.sh. The version directory keeps
   the immutable /static/lib/ caching safe across Mermaid upgrades. */
const MERMAID_VERSION = '11.17.2';
const MERMAID_URL = '/static/lib/mermaid/' + MERMAID_VERSION + '/mermaid.esm.min.mjs';

/* Hard caps: over-cap blocks stay readable as source with a short note. */
const MAX_BLOCKS = 50;
const MAX_CHARS = 2000;

/* Wrapping width for node and edge labels, and the CSS that enforces it.
   Mermaid only turns wrapping on when a label measures exactly wrappingWidth
   and leaves `white-space: nowrap` otherwise; browsers that ignore max-width
   on table-cell (Firefox) then clip the tail of long labels. themeCSS is
   embedded in the SVG and applied during measurement too, so the node is
   sized for wrapped text and a label can never be cut off. */
const MM_LABEL_W = 200;
const MM_THEME_CSS = 'foreignObject > div {' +
  ' display: table !important;' +
  ' white-space: break-spaces !important;' +
  ' max-width: ' + MM_LABEL_W + 'px !important;' +
  ' overflow-wrap: anywhere; }' +
  // Subgraph titles are sized to their content by Mermaid (no wrapping width);
  // keep that so a title stays on one line inside its cluster.
  ' g.cluster foreignObject > div {' +
  ' display: table-cell !important;' +
  ' white-space: nowrap !important;' +
  ' max-width: none !important;' +
  ' width: auto !important; }';

/* The server marks a diagram fence with data-lang, and the preview sanitizer
   keeps that attribute; the code element itself carries no class by then. */
const MD_MERMAID = 'pre[data-lang="mermaid"] > code';

let mermaidPromise = null;           // in-flight/finished import: mermaid loads once
let mermaidModule = null;            // resolved module, for theme re-initialize
let renderQueue = Promise.resolve(); // diagrams render one at a time
let svgSeq = 0;                      // unique id per mermaid.render() call
let themeWatcher = null;             // shared html[data-theme] observer
const rendered = new Set();          // wrapper nodes that currently hold an SVG
const snapshots = new WeakMap();     // wrapper -> its original <pre>, for restore

/* A literal dynamic specifier breaks `bun build --format=iife`; keeping the
   URL in a variable leaves the import to the browser, not the bundler. */
async function loadMermaid() {
  if (!mermaidPromise) {
    const u = MERMAID_URL;
    mermaidPromise = import(u).then(mod => {
      const mermaid = mod.default || mod;
      mermaid.initialize(mermaidConfig());
      mermaidModule = mermaid;
      return mermaid;
    }).catch(err => {
      mermaidPromise = null; // a later preview may retry a transient failure
      throw err;
    });
  }
  return mermaidPromise;
}

let colorCtx = null;

/* Mermaid expects hex colours, but themes write tokens in any CSS form
   (`#abc`, `rgb(...)`, `rgba(...)`). Canonicalise through a canvas fillStyle;
   translucent tokens are composited over the editor surface. */
function readHex(name) {
  const g = getComputedStyle(document.documentElement);
  const raw = g.getPropertyValue(name).trim();
  if (!raw) return '';
  if (!colorCtx) colorCtx = document.createElement('canvas').getContext('2d');
  if (!colorCtx) return '';
  colorCtx.fillStyle = '#000000';
  colorCtx.fillStyle = raw;
  const value = colorCtx.fillStyle;
  if (value.charAt(0) === '#') return value;
  const m = /^rgba\((\d+), (\d+), (\d+), ([\d.]+)\)$/.exec(value);
  if (!m) return '';
  colorCtx.fillStyle = g.getPropertyValue('--bg').trim() || '#000000';
  const over = colorCtx.fillStyle;
  if (over.charAt(0) !== '#') return '';
  const a = parseFloat(m[4]);
  const chan = i => parseInt(m[i], 10) * a + parseInt(over.slice((i - 1) * 2 + 1, (i - 1) * 2 + 3), 16) * (1 - a);
  const byte = n => Math.round(n).toString(16).padStart(2, '0');
  return '#' + byte(chan(1)) + byte(chan(2)) + byte(chan(3));
}

const colorOf = (name, fallback) => readHex(name) || fallback;

/* The base theme derives its "calculated" colours from darkMode, so the flag
   has to reach themeVariables, not just the top-level config. `color-scheme`
   names the active theme's intent; luminance is the fallback. */
function isDark() {
  const scheme = getComputedStyle(document.documentElement).getPropertyValue('color-scheme');
  if (scheme.indexOf('dark') >= 0) return true;
  if (scheme.indexOf('light') >= 0) return false;
  const bg = readHex('--bg');
  if (!/^#[0-9a-f]{6}$/i.test(bg)) return true; // px0's default theme is dark
  const r = parseInt(bg.slice(1, 3), 16), g = parseInt(bg.slice(3, 5), 16), b = parseInt(bg.slice(5, 7), 16);
  return (r * 299 + g * 587 + b * 114) / 1000 < 128;
}

/* Mermaid knows nothing about px0's tokens, so the base theme gets the roles
   settled in STYLING.md; everything mermaid derives from them (cluster
   borders, gradient stops, git accents) follows automatically. */
function mermaidConfig() {
  const g = getComputedStyle(document.documentElement);
  const text = name => g.getPropertyValue(name).trim();
  const dark = isDark();
  const bg = colorOf('--bg', '#0d1117');
  const bg2 = colorOf('--bg2', '#010409');
  const bg3 = colorOf('--bg3', '#161b22');
  const bg4 = colorOf('--bg4', '#21262d');
  const fg = colorOf('--fg', '#e6edf3');
  const dim = colorOf('--dim', '#8b949e');
  const line = colorOf('--line', '#30363d');
  const accent = colorOf('--accent', '#1f6feb');
  const accentFg = colorOf('--accent-fg', '#58a6ff');
  const err = colorOf('--err', '#f85149');
  const k = colorOf('--k', fg), kt = colorOf('--kt', k), nf = colorOf('--nf', fg);
  const nc = colorOf('--nc', nf), nb = colorOf('--nb', fg), nv = colorOf('--nv', fg);
  const s = colorOf('--s', fg), m = colorOf('--m', fg), o = colorOf('--o', fg), c = colorOf('--c', dim);
  return {
    startOnLoad: false,
    securityLevel: 'strict',
    theme: 'base',
    layout: 'dagre',
    suppressErrorRendering: true,
    themeCSS: MM_THEME_CSS,
    flowchart: { htmlLabels: true, wrappingWidth: MM_LABEL_W, subGraphTitleMargin: { top: 8, bottom: 16 } },
    sequence: { wrap: true },
    darkMode: dark,
    themeVariables: {
      darkMode: dark,
      // flowchart surfaces, text and edges
      background: bg,
      primaryColor: bg3, primaryTextColor: fg, primaryBorderColor: line,
      secondaryColor: bg2, secondaryTextColor: fg, secondaryBorderColor: line,
      tertiaryColor: bg4, tertiaryTextColor: fg, tertiaryBorderColor: line,
      lineColor: dim, textColor: fg,
      mainBkg: bg3, nodeBorder: line, nodeTextColor: fg,
      clusterBkg: bg2, clusterBorder: line,
      titleColor: fg, edgeLabelBackground: bg, labelBackground: bg,
      // sequence diagrams
      actorBkg: bg3, actorBorder: line, actorTextColor: fg, actorLineColor: line,
      signalColor: fg, signalTextColor: fg,
      labelBoxBkgColor: bg3, labelBoxBorderColor: line, labelTextColor: fg,
      loopTextColor: fg,
      noteBkgColor: bg4, noteBorderColor: line, noteTextColor: fg,
      activationBkgColor: bg4, activationBorderColor: line,
      sequenceNumberColor: bg,
      // class, state and ER diagrams
      classText: fg, stateBkg: bg3, labelColor: fg, altBackground: bg2,
      // failure text reads as an error, not as a surface
      errorBkgColor: bg3, errorTextColor: err,
      // pie and mindmap palettes reuse the syntax roles
      pie1: k, pie2: nf, pie3: s, pie4: m, pie5: o, pie6: c, pie7: accent, pie8: accentFg,
      pie9: kt, pie10: nc, pie11: nb, pie12: nv,
      fontFamily: text('--ui'),
      fontSize: text('--fs'),
    },
  };
}

/* One diagram at a time: render() is not reentrant, and chaining keeps the
   parse/render pairs off each other's shared state. */
function enqueue(target) {
  renderQueue = renderQueue.then(() => renderTarget(target)).catch(() => {});
}

/* Diagrams are drawn as soon as the preview is shown: a reader who opens a
   Markdown file expects the diagrams in it to be there, not to appear as the
   page is scrolled. The queue keeps the parse/render pairs serial. */

/* Small note under a block. The preview stylesheet does not know this class
   yet, so the note carries token-based styling of its own. */
function note(pre, text, isErr) {
  let el = pre.nextElementSibling;
  if (!el || !el.classList || !el.classList.contains('md-mermaid-note')) {
    el = document.createElement('small');
    el.className = 'md-mermaid-note';
    el.style.cssText = 'display:block;padding:2px 0 8px';
    pre.after(el);
  }
  el.style.color = isErr ? 'var(--err)' : 'var(--faint)';
  el.textContent = text;
}

/* mermaid.parse with suppressErrors hides the grammar detail a reader needs to
   fix their diagram. A second, unsuppressed parse recovers the real message;
   its first lines carry the line number and the offending token. */
async function parseDetail(mermaid, src) {
  try {
    await mermaid.parse(src);
  } catch (err) {
    const msg = String((err && err.message) || err).split('\n').slice(0, 3).join(' ').trim();
    if (msg) return msg.slice(0, 140);
  }
  return 'invalid diagram syntax';
}

/* Rebuild the server's block shape when the original node is gone. */
function sourceBlock(src) {
  const pre = document.createElement('pre');
  pre.className = 'md-code';
  pre.dataset.lang = 'mermaid';
  const code = document.createElement('code');
  code.textContent = src;
  pre.appendChild(code);
  return pre;
}

/* Put the block back exactly as the server rendered it and say what happened;
   a diagram must never vanish or blank because mermaid could not draw it. */
function fail(target, src, err) {
  rendered.delete(target);
  const original = snapshots.get(target) || sourceBlock(src);
  target.replaceWith(original);
  // Point at the fence's own source line: Mermaid counts within the diagram.
  const at = original.dataset.line ? ' (line ' + original.dataset.line + ')' : '';
  note(original, 'Mermaid' + at + ': ' + (err && err.message ? String(err.message).split('\n')[0] : 'render failed').slice(0, 140), true);
}

async function renderTarget(target) {
  if (!target.isConnected) return; // preview closed before its turn came
  const src = target.dataset.mermaidSource;
  if (!src) return;

  let mermaid;
  try { mermaid = await loadMermaid(); }
  catch (err) { fail(target, src, err); return; }

  let svg;
  try {
    const parsed = await mermaid.parse(src, { suppressErrors: true });
    if (!parsed) throw new Error(await parseDetail(mermaid, src));
    svg = (await mermaid.render('px0-mermaid-' + (++svgSeq), src)).svg;
    if (!svg) throw new Error('render produced no SVG');
  } catch (err) { fail(target, src, err); return; }
  if (!target.isConnected) return; // the preview was replaced while rendering
  mountDiagram(target, svg);
  rendered.add(target);
}

function watchTheme() {
  if (themeWatcher) return;
  themeWatcher = new MutationObserver(() => {
    if (!mermaidModule) return; // nothing rendered, nothing to refresh
    mermaidModule.initialize(mermaidConfig()); // computed styles already reflect the new theme
    for (const node of rendered) {
      if (!node.isConnected) { rendered.delete(node); continue; }
      enqueue(node);
    }
  });
  themeWatcher.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
}

export async function renderMermaidBlocks(root) {
  if (!root || !root.querySelectorAll) return;
  const codes = root.querySelectorAll(MD_MERMAID);
  if (!codes.length) return; // no blocks: mermaid is never imported
  watchTheme();
  let seen = 0;
  for (const code of codes) {
    seen++;
    const pre = code.parentElement;
    if (seen > MAX_BLOCKS) {
      note(pre, 'Diagram not rendered: this preview has more than ' + MAX_BLOCKS + ' diagrams.');
      continue;
    }
    if (code.textContent.length > MAX_CHARS) {
      note(pre, 'Diagram not rendered: source is longer than ' + MAX_CHARS + ' characters.');
      continue;
    }
    // Swap in the wrapper before queueing, so the code-block enhancer never
    // sees a diagram as a code block and the source stays for a failed render.
    const node = document.createElement('div');
    node.className = 'md-mermaid';
    node.dataset.mermaidSource = code.textContent;
    if (pre.dataset.line) node.dataset.line = pre.dataset.line; // keep line navigation
    snapshots.set(node, pre);
    pre.replaceWith(node);
    enqueue(node);
  }
}

/* Drop bookkeeping for one preview's blocks when its tab closes: rendered
   wrappers leave the theme re-render set with the body. */
export function forgetMermaid(root) {
  if (!root) return;
  for (const node of rendered) {
    if (!node.isConnected || root.contains(node)) rendered.delete(node);
  }
}
