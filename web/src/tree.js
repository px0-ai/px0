// web/src/tree.js
import { $, $$, esc, api, doc_ } from './state.js';
import { openFile } from './tabs.js';

export const treeEl = $('#tree');
export const openDirs = new Set();

/* git status letter -> CSS class + label. Empty/absent = clean, no badge. */
const GIT_STATUS = {
  M: ['git-M', 'modified'], A: ['git-A', 'added'], D: ['git-D', 'deleted'],
  U: ['git-untracked', 'untracked'], R: ['git-R', 'renamed'],
  C: ['git-A', 'copied'], '!': ['git-M', 'unmerged'],
};

export async function drawTree(dir, container, depth) {
  let j;
  try { j = await api('/api/tree', { dir }); } catch { return; }
  container.innerHTML = j.children.map(c => {
    const pad = 8 + depth * 12;
    // Ignored by .gitignore: still browsable, dimmed, and absent from search.
    const ig = c.ignored ? ' ignored' : '';
    const note = c.ignored ? ' (ignored by .gitignore, not searched)' : '';
    if (c.dir) {
      const dc = c.dirty ? ' dirty' : ''; // backend marks any ancestor of a change
      return '<div class="tw"><div class="tr dir' + ig + dc + '" data-dir="' + esc(c.path) + '" style="padding-left:' + pad + 'px" title="Folder: ' + esc(c.path) + note + '">' +
        '<span class="ar"></span><span class="nm">' + esc(c.name) + '</span></div>' +
        '<div class="kids" data-kids="' + esc(c.path) + '"></div></div>';
    }
    const g = GIT_STATUS[c.status];
    const gc = g ? ' dirty ' + g[0] : '';
    const badge = g ? '<span class="gs" title="git: ' + g[1] + '">' + esc(c.status) + '</span>' : '';
    return '<div class="tr file' + ig + gc + '" data-file="' + esc(c.path) + '" style="padding-left:' + (pad + 12) + 'px" title="Open ' + esc(c.path) + note + '">' +
      '<span class="ic" data-t="' + fileKind(c.name) + '"></span><span class="nm">' + esc(c.name) + '</span>' + badge + '</div>';
  }).join('');
}

/* A colour family per file kind, drawn in CSS. Emoji or icon fonts would be at
   the mercy of whatever the viewer has installed. */
export const FILE_KIND = {
  go: 'code', js: 'code', mjs: 'code', cjs: 'code', ts: 'code', tsx: 'code', jsx: 'code',
  py: 'code', rb: 'code', rs: 'code', java: 'code', kt: 'code', c: 'code', h: 'code',
  cc: 'code', cpp: 'code', hpp: 'code', cs: 'code', php: 'code', swift: 'code',
  lua: 'code', ex: 'code', exs: 'code', scala: 'code', dart: 'code', sh: 'code',
  bash: 'code', zsh: 'code', sql: 'code',
  json: 'data', yaml: 'data', yml: 'data', toml: 'data', ini: 'data', xml: 'data',
  csv: 'data', env: 'data', lock: 'data', mod: 'data', sum: 'data',
  md: 'doc', markdown: 'doc', txt: 'doc', rst: 'doc', adoc: 'doc',
  html: 'web', htm: 'web', css: 'web', scss: 'web', less: 'web', svg: 'web', vue: 'web',
  png: 'img', jpg: 'img', jpeg: 'img', gif: 'img', webp: 'img', ico: 'img', avif: 'img',
};

export function fileKind(name) {
  const i = name.lastIndexOf('.');
  return (i > 0 && FILE_KIND[name.slice(i + 1).toLowerCase()]) || 'other';
}

/* Open one rendered folder row and await its children. Idempotent: a folder
   that is already open resolves without refetching. False when the row is not
   in the tree at all, which ends an ancestor walk early. */
export async function expandDir(dir) {
  const row = treeEl.querySelector('[data-dir="' + CSS.escape(dir) + '"]');
  const kids = treeEl.querySelector('[data-kids="' + CSS.escape(dir) + '"]');
  if (!row || !kids) return false;
  row.classList.add('open');
  kids.classList.add('open');
  openDirs.add(dir);
  if (!kids.dataset.loaded) {
    kids.dataset.loaded = '1';
    await drawTree(dir, kids, dir.split('/').length);
  }
  return true;
}

/* Expand every ancestor of dir, outermost first. Awaits each level's children
   rather than clicking rows on a fixed timer, so a deep path costs one request
   per unloaded level and no artificial delay. */
async function expandPath(dir) {
  const parts = dir.split('/');
  for (let i = 0; i < parts.length; i++) {
    if (!await expandDir(parts.slice(0, i + 1).join('/'))) return false;
  }
  return true;
}

/* True while row sits inside the tree's own scroll window. */
function rowVisible(row) {
  const r = row.getBoundingClientRect(), t = treeEl.getBoundingClientRect();
  return r.bottom > t.top && r.top < t.bottom;
}

/* Expand the tree down to dir and scroll it into view. */
export async function revealDir(dir) {
  await expandPath(dir);
  const last = treeEl.querySelector('[data-dir="' + CSS.escape(dir) + '"]');
  if (last) last.scrollIntoView({ block: 'center' });
}

/* Select path in the tree, expanding folders on the way down. `ifNeeded` skips
   the scroll while the row is already on screen: auto-reveal fires on every
   open and tab switch, and must not yank the sidebar out from under the user. */
export async function revealFile(path, opts = {}) {
  const idx = path.lastIndexOf('/');
  if (idx > 0) await expandPath(path.slice(0, idx));
  const row = treeEl.querySelector('[data-file="' + CSS.escape(path) + '"]');
  if (!row) return;
  $$('.tr.sel', treeEl).forEach(x => x.classList.remove('sel'));
  row.classList.add('sel');
  if (!(opts.ifNeeded && rowVisible(row))) row.scrollIntoView({ block: 'center' });
}

/* Auto-reveal keeps the tree selection on the active document, the way
   explorer.autoReveal does. On by default, toggled from the command palette,
   and remembered in the browser because px0 writes no state to disk. */
const AUTOREVEAL_KEY = 'px0.autoReveal';
let autoReveal = true;
try { autoReveal = localStorage.getItem(AUTOREVEAL_KEY) !== 'false'; } catch {}

export function autoRevealOn() { return autoReveal; }

export function setAutoReveal(on) {
  autoReveal = !!on;
  try { localStorage.setItem(AUTOREVEAL_KEY, autoReveal ? 'true' : 'false'); } catch {}
  if (autoReveal) syncTreeSelection(doc_()?.path);
}

/* A reveal asked for while the sidebar was hidden, replayed when it reopens. */
let pendingReveal = null;

/* Point the tree at path because the active document changed. Does nothing
   when the user turned auto-reveal off, and defers while the sidebar is
   hidden so reopening it never lands on a stale file. */
export function syncTreeSelection(path) {
  if (!autoReveal || !path) return;
  if (document.body.classList.contains('side-hidden')) { pendingReveal = path; return; }
  pendingReveal = null;
  revealFile(path, { ifNeeded: true });
}

/* Run the deferred reveal, if any, now that the sidebar is visible again. */
export function flushPendingReveal() {
  const path = pendingReveal;
  pendingReveal = null;
  if (path) revealFile(path, { ifNeeded: true });
}

export function initTree() {
  // "Changed only" filter: hide clean files and known-clean folders (CSS-driven).
  $('#btn-changed')?.addEventListener('click', e => {
    const on = treeEl.classList.toggle('changed-only');
    e.currentTarget.classList.toggle('active', on);
  });

  treeEl.addEventListener('click', async e => {
    const dirRow = e.target.closest('[data-dir]');
    if (dirRow) {
      const path = dirRow.dataset.dir;
      if (dirRow.classList.contains('open')) {
        dirRow.classList.remove('open');
        treeEl.querySelector('[data-kids="' + CSS.escape(path) + '"]')?.classList.remove('open');
        openDirs.delete(path);
      } else await expandDir(path);
      return;
    }
    const f = e.target.closest('[data-file]');
    if (f) {
      $$('.tr.sel', treeEl).forEach(x => x.classList.remove('sel'));
      f.classList.add('sel');
      openFile(f.dataset.file);
    }
  });
}
