// web/src/mermaid.js
import { esc } from './state.js';
import { listThemes, currentTheme } from './theme.js';

/* Native Mermaid diagrams for the Markdown preview and thread messages.

   A ```mermaid fence reaches the client as raw, un-highlighted source (see
   highlightFence in markdown.go). Both surfaces turn such a fence into a
   .md-mermaid placeholder holding that source as text; renderMermaidIn then
   swaps in the SVG mermaid produces. The library (~2.7MB) is vendored under
   web/vendor and loaded on demand the first time a diagram appears, so pages
   without diagrams never pay for it. Rendered SVG is cached by source+theme,
   which keeps streaming thread replies cheap: a finished diagram is restored
   from cache synchronously on every re-render instead of laid out again. */

let loadPromise = null;   // resolves to window.mermaid, once
let idSeq = 0;            // unique ids mermaid.render requires
const cache = new Map();  // "<theme>\u0000<source>" -> svg string
let observing = false;

// Mermaid ships light ('default') and 'dark' themes; map px0's active theme to
// one by the color-scheme it declares, falling back to light for unknown themes.
function mermaidTheme() {
  const t = listThemes().find(x => x.id === currentTheme());
  return t && t.scheme === 'dark' ? 'dark' : 'default';
}

function ensureMermaid() {
  if (loadPromise) return loadPromise;
  loadPromise = new Promise((resolve, reject) => {
    const src = new URL('static/vendor/mermaid.min.js', document.baseURI || location.href).href;
    const s = document.createElement('script');
    s.src = src;
    s.async = true;
    s.onload = () => {
      if (!window.mermaid) { reject(new Error('mermaid did not load')); return; }
      window.mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',   // no script/HTML injected from diagram text
        suppressErrorRendering: true, // we render our own error state
        theme: mermaidTheme(),
      });
      watchTheme();
      resolve(window.mermaid);
    };
    s.onerror = () => { loadPromise = null; reject(new Error('failed to load mermaid')); };
    document.head.appendChild(s);
  });
  return loadPromise;
}

// Re-theme every rendered diagram when the user switches themes.
function watchTheme() {
  if (observing) return;
  observing = true;
  let last = currentTheme();
  new MutationObserver(() => {
    if (currentTheme() === last) return;
    last = currentTheme();
    if (!window.mermaid) return;
    window.mermaid.initialize({
      startOnLoad: false, securityLevel: 'strict', suppressErrorRendering: true, theme: mermaidTheme(),
    });
    for (const el of document.querySelectorAll('.md-mermaid[data-md-done]')) {
      el.removeAttribute('data-md-done');
      el.classList.remove('rendered', 'md-mermaid-error');
      el.textContent = el.dataset.src || el.textContent;
    }
    for (const root of document.querySelectorAll('#md, #thr-msgs, .thr-body')) renderMermaidIn(root);
  }).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
}

/* HTML for a placeholder, for callers that build markup as strings (thread.js).
   The source lives both as text content and in data-src so a theme switch can
   restore it after the SVG has replaced the text. */
export function mermaidBlockHtml(source) {
  const s = esc(source);
  return `<div class="md-mermaid" data-src="${s}">${s}</div>`;
}

function fail(el, source, msg) {
  el.classList.add('md-mermaid-error');
  el.setAttribute('data-md-done', '');
  el.textContent = '';
  const label = document.createElement('div');
  label.className = 'md-mermaid-err-label';
  label.textContent = 'Diagram error: ' + msg;
  const pre = document.createElement('pre');
  pre.textContent = source;
  el.append(label, pre);
}

/* Render every not-yet-drawn .md-mermaid placeholder inside root. Cache hits are
   applied synchronously; misses load mermaid (once) and render asynchronously. */
export function renderMermaidIn(root) {
  if (!root) return;
  const theme = mermaidTheme();
  const blocks = [...root.querySelectorAll('.md-mermaid:not([data-md-done])')];
  if (!blocks.length) return;

  const pending = [];
  for (const el of blocks) {
    if (!el.dataset.src) el.dataset.src = el.textContent;
    const svg = cache.get(theme + '\u0000' + el.dataset.src);
    if (svg) applySvg(el, svg);
    else pending.push(el);
  }
  if (!pending.length) return;

  ensureMermaid().then(async (m) => {
    for (const el of pending) {
      if (!el.isConnected || el.hasAttribute('data-md-done')) continue;
      const source = el.dataset.src;
      const key = theme + '\u0000' + source;
      const cached = cache.get(key);
      if (cached) { applySvg(el, cached); continue; }
      try {
        const { svg } = await m.render('mmd-' + (++idSeq), source);
        cache.set(key, svg);
        if (el.isConnected) applySvg(el, svg);
      } catch (e) {
        if (el.isConnected) fail(el, source, (e && e.message) || String(e));
      }
    }
  }).catch(err => {
    for (const el of pending) if (el.isConnected) fail(el, el.dataset.src || '', err.message);
  });
}

function applySvg(el, svg) {
  el.innerHTML = svg;
  el.classList.add('rendered');
  el.classList.remove('md-mermaid-error');
  el.setAttribute('data-md-done', '');
}
