// web/src/tree.js
import { $, $$, esc, api } from './state.js';
import { openFile } from './tabs.js';

export const treeEl = $('#tree');
export const openDirs = new Set();
const treeLoads = new WeakMap();

/* git status letter -> CSS class + label. Empty/absent = clean, no badge. */
const GIT_STATUS = {
  M: ['git-M', 'modified'], A: ['git-A', 'added'], D: ['git-D', 'deleted'],
  U: ['git-untracked', 'untracked'], R: ['git-R', 'renamed'],
  C: ['git-A', 'copied'], '!': ['git-M', 'unmerged'],
};

export async function drawTree(dir, container, depth) {
  let j;
  try { j = await api('/api/tree', { dir }); } catch { return false; }
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
  return true;
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

// Clicks and reveals share pending requests, including on slow remote servers.
async function expandTreeRow(row) {
  const path = row.dataset.dir;
  const kids = row.nextElementSibling;
  row.classList.add('open');
  kids.classList.add('open');
  openDirs.add(path);
  if (kids.dataset.loaded) return true;
  if (!treeLoads.has(kids)) {
    treeLoads.set(kids, drawTree(path, kids, path.split('/').length).then(loaded => {
      if (loaded) kids.dataset.loaded = '1';
      treeLoads.delete(kids);
      return loaded;
    }));
  }
  return treeLoads.get(kids);
}

async function expandTreePath(dir, isCurrent) {
  const parts = dir.split('/');
  for (let i = 0; i < parts.length; i++) {
    if (!isCurrent()) return false;
    const p = parts.slice(0, i + 1).join('/');
    const row = treeEl.querySelector('[data-dir="' + CSS.escape(p) + '"]');
    if (!row || !await expandTreeRow(row)) return false;
  }
  return isCurrent();
}

/* Expand the tree down to dir and scroll it into view. */
export async function revealDir(dir) {
  if (!await expandTreePath(dir, () => true)) return;
  const last = treeEl.querySelector('[data-dir="' + CSS.escape(dir) + '"]');
  if (last) last.scrollIntoView({ block: 'center' });
}

export async function revealFile(path, isCurrent = () => true) {
  const idx = path.lastIndexOf('/');
  if (idx > 0 && !await expandTreePath(path.slice(0, idx), isCurrent)) return;
  if (!isCurrent()) return;
  const row = treeEl.querySelector('[data-file="' + CSS.escape(path) + '"]');
  if (row) {
    $$('.tr.sel', treeEl).forEach(x => x.classList.remove('sel'));
    row.classList.add('sel');
    row.scrollIntoView({ block: 'center' });
  }
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
        dirRow.nextElementSibling.classList.remove('open');
        openDirs.delete(path);
      } else await expandTreeRow(dirRow);
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
