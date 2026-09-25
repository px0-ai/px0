// web/src/markdown.js
import { $, $$, S, doc_, esc, api, isMac, MOD, LH } from './state.js';
import { on } from './bus.js';
import { vp, rowsEl, copyToClipboard, showToast } from './ui.js';
import { render, paint, rowFor, markNodes } from './renderer.js';
import { openFile } from './tabs.js';
import { updateStatus } from './status.js';
import { pushHistory } from './history.js';
import { showPanel } from './panels.js';
import { revealDir } from './tree.js';
import { findbar, runFind } from './find.js';
import { hideHover } from './hover.js';

/* Markdown tabs open rendered. The server converts the file with goldmark and
   passes raw HTML through, so nothing it returns is trusted: mdSanitize rebuilds
   it against an allowlist in an inert document before any of it reaches the page.
   Every block carries the source line it starts on (data-line), which keeps the
   preview in step with line-based navigation and with the source view. */

export const mdview = $('#mdview');
const mdArticle = $('#md');

let mdShown = null;  // doc the preview is showing, null while it is hidden
let mdDrawn = null;  // doc whose HTML is in the article; drawing can wait on a fetch
let mdGen = 0;

export function previewing(d = doc_()) {
  return !!(d && d.markdown && S.mdPreview && !d.mdError && !d.diffMode);
}

/* Show or hide the preview to match the active tab. Call whenever that changes. */
export function syncPreview() {
  const d = doc_();
  const want = previewing(d) ? d : null;
  if (want === mdShown) return;
  if (mdShown && mdDrawn === mdShown) mdShown.mdScroll = mdview.scrollTop;
  mdShown = want;
  mdDrawn = null;
  mdview.hidden = !want;
  mdArticle.replaceChildren();
  if (want) drawPreview(want);
}

async function drawPreview(d) {
  const gen = ++mdGen;
  if (d.mdHtml === undefined) {
    try {
      d.mdReq = d.mdReq || api('/api/markdown', { path: d.path });
      d.mdHtml = (await d.mdReq).html;
    } catch (e) {
      d.mdError = e.message; // this tab falls back to its source
      if (gen === mdGen && mdShown === d) {
        showToast('!', 'No preview for ' + d.name + ': ' + e.message);
        syncPreview();
        updateStatus();
      }
      return;
    } finally {
      d.mdReq = null;
    }
    if (gen !== mdGen || mdShown !== d) return;
  }
  mdArticle.replaceChildren(mdSanitize(d.mdHtml, d.path));
  mdEnhance();
  mdDrawn = d;
  const target = d.mdAnchor && mdFindAnchor(d.mdAnchor);
  if (target) mdScrollTo(target);
  else if (d.mdLine) previewLine(d.mdLine);
  else mdview.scrollTop = d.mdScroll || 0;
  d.mdAnchor = '';
  d.mdLine = 0;
  if (!findbar.hidden) runFind();
}

export function togglePreview() {
  const d = doc_();
  if (!d || !d.markdown) { showToast('!', 'Preview works on Markdown files'); return; }
  hideHover();
  if (previewing(d)) {
    const line = mdDrawn === d ? previewTopLine() : 1;
    mdSetPref(false);
    syncPreview();
    sourceToLine(line);
  } else {
    d.mdError = '';
    d.mdLine = sourceTopLine();
    mdSetPref(true);
    syncPreview();
  }
  if (!findbar.hidden) runFind(); else S.find = null;
  render();
  updateStatus();
}

function mdSetPref(on) {
  S.mdPreview = on;
  try { localStorage.setItem('px0.mdPreview', on ? 'true' : 'false'); } catch {}
}

/* ---------- sanitising ---------- */

const HTML_NS = 'http://www.w3.org/1999/xhtml';
// Removed along with everything inside them.
const MD_DROP = new Set(('script style iframe frame frameset object embed applet template noscript noembed ' +
  'svg math form textarea select option button link meta base title audio video source track canvas dialog').split(' '));
// Kept. Any other element is unwrapped: its children stay, the element goes.
const MD_KEEP = new Set(('a abbr b bdi bdo blockquote br caption center cite code col colgroup dd del details dfn div dl dt ' +
  'em figcaption figure h1 h2 h3 h4 h5 h6 hr i img input ins kbd li mark ol p pre q rp rt ruby s samp section small span ' +
  'strike strong sub summary sup table tbody td tfoot th thead tr tt u ul var wbr').split(' '));
// Attributes that can neither run script nor reach the network.
const MD_ATTRS = new Set(('align valign alt title lang dir width height colspan rowspan start reversed open checked ' +
  'disabled type data-line data-lang').split(' '));
// Token classes from the server's highlighter, allowed on <i>.
const MD_TOKENS = new Set('k kt nf nc nb nv no na nt nd np s m o p c cp gi gd gh ge gs err g'.split(' '));
const MD_SCHEME = /^([a-z][a-z0-9+.-]*):/i;
// Relative references resolve against this stand-in origin; landing anywhere else means they were not relative.
const MD_ORIGIN = 'http://px0.invalid';

/* The URL parser drops tabs and newlines anywhere and control characters at
   either end, so "java&#9;script:" still has a scheme. Test what it will see. */
const mdURL = ref => ref.replace(/[\t\n\r]/g, '').replace(/^[\x00-\x20]+|[\x00-\x20]+$/g, '');

/* Parsing into a DOMParser document runs no script and loads nothing, so the
   markup can be cleaned there and only the survivors adopted into the page. */
function mdSanitize(html, docPath) {
  const body = new DOMParser().parseFromString(html, 'text/html').body;
  const dir = docPath.slice(0, docPath.lastIndexOf('/') + 1);
  const base = MD_ORIGIN + '/' + dir.split('/').map(encodeURIComponent).join('/');
  for (const el of [...body.querySelectorAll('*')]) {
    if (!body.contains(el)) continue; // inside something already removed
    const tag = el.localName;
    if (el.namespaceURI !== HTML_NS || MD_DROP.has(tag)) { el.remove(); continue; }
    if (!MD_KEEP.has(tag) || (tag === 'input' && el.getAttribute('type') !== 'checkbox')) {
      el.replaceWith(...el.childNodes);
      continue;
    }
    const attrs = {};
    for (const a of [...el.attributes]) { attrs[a.name] = a.value; el.removeAttribute(a.name); }
    for (const name in attrs) if (MD_ATTRS.has(name)) el.setAttribute(name, attrs[name]);
    // Prefixed so a heading called "status" cannot shadow the status bar's id.
    const id = attrs.id || (tag === 'a' && attrs.name);
    if (id) el.id = 'md-' + id;
    if (attrs.class) {
      const keep = attrs.class.split(/\s+/).filter(c =>
        c === 'md-code' || c.startsWith('footnote') || (tag === 'i' && MD_TOKENS.has(c)));
      if (keep.length) el.className = keep.join(' ');
    }
    if (tag === 'input') /** @type {HTMLInputElement} */ (el).disabled = true;
    if (tag === 'img') mdSetImage(el, mdURL(attrs.src || ''), base);
    if (tag === 'a' && attrs.href) mdSetLink(el, mdURL(attrs.href), base);
  }
  const frag = document.createDocumentFragment();
  while (body.firstChild) frag.appendChild(document.adoptNode(body.firstChild));
  return frag;
}

/* A reference without a scheme names a file in the workspace, relative to the
   Markdown file's directory, or to the root when it starts with /, as on GitHub.
   Returns null for anything that does not resolve that way. */
function mdLocal(ref, base) {
  let u;
  try { u = new URL(ref, base); } catch { return null; }
  if (u.origin !== MD_ORIGIN) return null;
  let path = u.pathname;
  try { path = decodeURIComponent(path); } catch {}
  return { path: path.slice(1), hash: u.hash.slice(1) };
}

function mdSetImage(img, src, base) {
  img.setAttribute('loading', 'lazy');
  img.setAttribute('decoding', 'async');
  img.classList.add('md-zoomable');
  const m = MD_SCHEME.exec(src);
  if (m) {
    if (/^https?$/i.test(m[1]) || /^data:image\//i.test(src)) {
      img.setAttribute('src', src);
      img.dataset.origSrc = src;
    }
  } else if (src.startsWith('//')) {
    img.setAttribute('src', src);
    img.dataset.origSrc = src;
  } else if (src) {
    const t = mdLocal(src, base);
    if (t) {
      const rawUrl = new URL('api/raw?path=' + encodeURIComponent(t.path), document.baseURI || location.href).href;
      img.setAttribute('src', rawUrl);
      img.dataset.rawPath = t.path;
      img.dataset.origSrc = src;
    }
  }
}

/* Links within the file scroll the preview, links to workspace files open them
   in px0, web links open a new browser tab, and any other scheme loses its href. */
function mdSetLink(a, href, base) {
  if (href.startsWith('#')) {
    a.setAttribute('href', href);
    a.dataset.anchor = href.slice(1);
    return;
  }
  const m = MD_SCHEME.exec(href);
  if (m || href.startsWith('//')) {
    if (m && !/^(https?|mailto)$/i.test(m[1])) return;
    a.setAttribute('href', href);
    a.target = '_blank';
    a.rel = 'noopener noreferrer';
    return;
  }
  const t = mdLocal(href, base);
  if (!t) return;
  const rawUrl = new URL('api/raw?path=' + encodeURIComponent(t.path), document.baseURI || location.href).href;
  a.setAttribute('href', rawUrl);
  a.dataset.path = t.path;
  if (t.hash) a.dataset.anchor = t.hash;
}

/* ---------- presentation ---------- */

const MD_ALERTS = { note: 'Note', tip: 'Tip', important: 'Important', warning: 'Warning', caution: 'Caution' };

function mdEnhance() {
  for (const q of $$('blockquote', mdArticle)) mdAlert(q);
  for (const pre of $$('pre', mdArticle)) {
    const wrap = document.createElement('div');
    wrap.className = 'md-pre';
    if (pre.dataset.lang) wrap.dataset.lang = pre.dataset.lang;
    pre.replaceWith(wrap);
    const copy = document.createElement('button');
    copy.className = 'md-copy';
    copy.title = 'Copy code';
    copy.setAttribute('aria-label', 'Copy code');
    // An icon, not a label: find in the preview walks text nodes.
    copy.innerHTML = '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"><rect x="5.5" y="5.5" width="8" height="8" rx="1.5"/><path d="M10.5 3.5V3a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5"/></svg>';
    wrap.append(pre, copy);
  }
}

/* GitHub alerts: a blockquote opening with [!NOTE], [!TIP], [!IMPORTANT],
   [!WARNING] or [!CAUTION]. */
function mdAlert(q) {
  const p = q.firstElementChild;
  const t = p && p.localName === 'p' && p.firstChild;
  if (!t || t.nodeType !== 3) return;
  const m = /^\s*\[!(\w+)\][ \t]*\n?/.exec(t.nodeValue);
  const kind = m && m[1].toLowerCase();
  if (!kind || !MD_ALERTS[kind]) return;
  t.nodeValue = t.nodeValue.slice(m[0].length);
  if (!t.nodeValue) t.remove();
  if (p.firstChild && p.firstChild.localName === 'br') p.firstChild.remove();
  if (!p.textContent.trim() && !p.children.length) p.remove();
  const title = document.createElement('p');
  title.className = 'md-alert-title';
  title.textContent = MD_ALERTS[kind];
  q.prepend(title);
  q.classList.add('md-alert', 'md-alert-' + kind);
}

/* ---------- position ---------- */

const MD_GAP = 16; // space left above a block scrolled into place

function mdScrollTo(el) {
  mdview.scrollTop += el.getBoundingClientRect().top - mdview.getBoundingClientRect().top - MD_GAP;
}

function mdFindAnchor(anchor) {
  let id = anchor;
  try { id = decodeURIComponent(anchor); } catch {}
  for (const k of [id, id.toLowerCase()]) {
    const el = document.getElementById('md-' + k);
    if (el && mdArticle.contains(el)) return el;
  }
  return null;
}

/* Line-based navigation lands on the block holding that line. Before the HTML
   has arrived, the line waits for drawPreview. */
export function previewLine(n) {
  const d = doc_();
  if (!d || mdDrawn !== d) { if (d) d.mdLine = n; return; }
  let best = null, at = 0;
  for (const el of mdArticle.querySelectorAll('[data-line]')) {
    const l = +el.dataset.line;
    if (l <= n && l > at) { best = el; at = l; }
  }
  if (best) mdScrollTo(best); else mdview.scrollTop = 0;
}

/* Source line of the last block starting at or above the top of the preview,
   counting one that mdScrollTo has just placed there. */
export function previewTopLine() {
  const top = mdview.getBoundingClientRect().top + MD_GAP + 8;
  let line = 1;
  for (const el of mdArticle.querySelectorAll('[data-line]')) {
    if (el.getBoundingClientRect().top > top) break;
    line = +el.dataset.line;
  }
  return line;
}

function sourceTopLine() {
  const top = vp.getBoundingClientRect().top;
  for (const r of rowsEl.children) if (r.getBoundingClientRect().bottom > top + 1) return +r.dataset.l;
  return 1;
}

/* Put a source line at the top of the code view. Rows are placed by LH, which
   wrapped rows outgrow, so correct against where the row was actually painted. */
function sourceToLine(line) {
  vp.scrollTop = (line - 1) * LH;
  for (let i = 0; i < 3; i++) {
    paint();
    const r = rowFor(line);
    const off = r ? r.getBoundingClientRect().top - vp.getBoundingClientRect().top : 0;
    if (Math.abs(off) < 1) break;
    vp.scrollTop += off;
  }
}

async function mdFollow(path, anchor) {
  const d = doc_();
  path = path.replace(/\/+$/, '');
  if (d && path === d.path) { mdJump(anchor); return; }
  if (d) pushHistory(d.path, previewing(d) && mdDrawn === d ? previewTopLine() : d.cur);
  // A link to a folder reveals it in the explorer.
  try {
    await api('/api/tree', { dir: path });
    showPanel('files');
    revealDir(path);
    return;
  } catch {}
  const line = /^L(\d+)/.exec(anchor);
  await openFile(path, line ? { line: +line[1] } : {});
  const nd = doc_();
  if (!nd || nd.path !== path) { showToast('!', 'Cannot open ' + path); return; }
  if (anchor && !line) {
    const el = mdDrawn === nd && mdFindAnchor(anchor);
    if (el) mdScrollTo(el); else nd.mdAnchor = anchor;
  }
}

function mdJump(anchor) {
  const d = doc_();
  const el = anchor && mdFindAnchor(anchor);
  if (!d || !el) return;
  pushHistory(d.path, previewTopLine());
  mdScrollTo(el);
  const block = /** @type {HTMLElement|null} */ (el.closest('[data-line]'));
  if (block && block.dataset.line) pushHistory(d.path, +block.dataset.line);
}

/* ---------- keys, select all, find ---------- */

export function previewKey(e) {
  const mod = e[MOD];
  if (e.key === 'Home' || (isMac && mod && e.key === 'ArrowUp')) { mdview.scrollTop = 0; return true; }
  if (e.key === 'End' || (isMac && mod && e.key === 'ArrowDown')) { mdview.scrollTop = mdview.scrollHeight; return true; }
  let by = 0;
  if (e.key === 'ArrowDown' || e.key === 'j') by = 48;
  else if (e.key === 'ArrowUp' || e.key === 'k') by = -48;
  else if (e.key === 'PageDown') by = mdview.clientHeight * 0.9;
  else if (e.key === 'PageUp') by = -mdview.clientHeight * 0.9;
  if (!by) return false;
  mdview.scrollBy({ top: by });
  return true;
}

export function selectPreview() {
  const r = document.createRange();
  r.selectNodeContents(mdArticle);
  const sel = window.getSelection();
  sel.removeAllRanges();
  sel.addRange(r);
}

export function clearPreviewMarks() {
  const marks = $$('mark.md-hit', mdArticle);
  for (const m of marks) m.replaceWith(...m.childNodes);
  if (marks.length) mdArticle.normalize();
}

/* Marks every case-insensitive match of q in the rendered text; returns the count. */
export function findInPreview(q) {
  clearPreviewMarks();
  if (!q) return 0;
  const marks = markNodes(mdArticle, q, false, 'mark');
  for (const m of marks) m.classList.add('md-hit');
  return marks.length;
}

export function showPreviewHit(i) {
  const marks = $$('mark.md-hit', mdArticle);
  marks.forEach((m, k) => m.classList.toggle('on', k === i));
  const m = marks[i];
  if (!m) return;
  const box = mdview.getBoundingClientRect(), r = m.getBoundingClientRect();
  if (r.top < box.top + 40 || r.bottom > box.bottom - 40) {
    mdview.scrollTop += r.top - box.top - mdview.clientHeight / 2;
  }
}

/* Match positions as percentages of the preview's height, for the minimap. */
export function previewHitOffsets() {
  const h = mdview.scrollHeight || 1, top = mdview.getBoundingClientRect().top - mdview.scrollTop;
  const seen = new Set();
  return $$('mark.md-hit', mdArticle)
    .map(m => ((m.getBoundingClientRect().top - top) / h * 100).toFixed(2))
    .filter(p => !seen.has(p) && seen.add(p));
}

export function scrollPreviewTo(fraction) {
  mdview.scrollTop = fraction * mdview.scrollHeight - mdview.clientHeight / 2;
}

export function initMarkdown() {
  const sw = $('#md-switch');
  // Keep focus where it was, so arrow keys go on scrolling the view afterwards.
  sw.addEventListener('mousedown', e => e.preventDefault());
  sw.addEventListener('click', e => {
    const b = e.target.closest('[data-md]');
    if (b && (b.dataset.md === 'preview') !== previewing()) togglePreview();
  });

  mdArticle.addEventListener('click', e => {
    const copy = e.target.closest('.md-copy');
    if (copy) {
      const pre = $('pre', copy.parentElement);
      if (pre) {
        copyToClipboard(pre.textContent || '', 'Copied code block');
        copy.classList.add('copied');
        copy.innerHTML = '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3.5 8.5 6.5 11.5 12.5 5.5"/></svg>';
        setTimeout(() => {
          copy.classList.remove('copied');
          copy.innerHTML = '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"><rect x="5.5" y="5.5" width="8" height="8" rx="1.5"/><path d="M10.5 3.5V3a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5"/></svg>';
        }, 1400);
      }
      return;
    }

    // Standalone image click opens interactive lightbox
    const img = e.target.closest('img.md-zoomable');
    const a = e.target.closest('a');
    if (img && !a && e.button === 0 && !e[MOD] && !e.shiftKey) {
      e.preventDefault();
      openLightbox(img);
      return;
    }

    // Modified clicks keep the browser's behaviour: the href opens the raw file.
    if (!a || e.button !== 0 || e[MOD] || e.shiftKey) return;
    if ('path' in a.dataset) { e.preventDefault(); mdFollow(a.dataset.path, a.dataset.anchor || ''); }
    else if ('anchor' in a.dataset) { e.preventDefault(); mdJump(a.dataset.anchor); }
  });

  // Gracefully handle broken / 404 images in markdown
  mdArticle.addEventListener('error', e => {
    if (e.target && e.target.localName === 'img') {
      const img = e.target;
      const path = img.dataset.rawPath || img.dataset.origSrc || img.getAttribute('src') || 'image';
      const fallback = document.createElement('div');
      fallback.className = 'md-img-broken';
      fallback.innerHTML = '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="2" width="12" height="12" rx="2"/><path d="M2 14l5-5 3 3 4-4"/><circle cx="5.5" cy="5.5" r="1.5"/><line x1="2" y1="2" x2="14" y2="14"/></svg><span>Image not found: ' + esc(path) + '</span>';
      img.replaceWith(fallback);
    }
  }, true);

  // Lightbox backdrop & close button
  const lb = $('#img-lightbox');
  if (lb) {
    lb.addEventListener('click', e => {
      if (e.target.closest('.lightbox-close') || e.target.classList.contains('lightbox-backdrop')) {
        lb.hidden = true;
      }
    });
  }
  on('tab:activated', () => syncPreview());
  on('tabs:cleared', () => syncPreview());
}

export function openLightbox(img) {
  const lb = $('#img-lightbox');
  if (!lb) return;

  const lbImg = $('#lb-img');
  const lbTitle = $('#lb-title');
  const lbMeta = $('#lb-meta');
  const lbOpenTab = $('#lb-open-tab');
  const lbCopyPath = $('#lb-copy-path');

  const src = img.getAttribute('src');
  const rawPath = img.dataset.rawPath || '';
  const alt = img.getAttribute('alt') || '';
  const displayTitle = rawPath || alt || src.split('/').pop() || 'Image Preview';

  lbImg.src = src;
  lbTitle.textContent = displayTitle;
  lbTitle.title = displayTitle;

  const updateMeta = () => {
    if (lbImg.naturalWidth) {
      lbMeta.textContent = `${lbImg.naturalWidth} × ${lbImg.naturalHeight} px`;
    } else {
      lbMeta.textContent = '';
    }
  };
  if (lbImg.complete && lbImg.naturalWidth) updateMeta(); else lbImg.onload = updateMeta;

  if (rawPath) {
    lbOpenTab.hidden = false;
    lbOpenTab.onclick = () => {
      lb.hidden = true;
      openFile(rawPath);
    };
    lbCopyPath.hidden = false;
    lbCopyPath.onclick = () => {
      copyToClipboard(rawPath, 'Copied image path', lbCopyPath);
    };
  } else {
    lbOpenTab.hidden = true;
    lbCopyPath.onclick = () => {
      copyToClipboard(src, 'Copied image URL', lbCopyPath);
    };
  }

  lb.hidden = false;
}
