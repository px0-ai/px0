// web/src/tree.js
import { $, $$, esc, api, apiPost, apiPostJson, S } from './state.js';
import { openFile } from './tabs.js';
import { showToast } from './ui.js';

export const treeEl = $('#tree');
export const openDirs = new Set();
// A newer tree action invalidates responses from older directory requests.
let expansionVersion = 0;

function setExpandBusy(busy) {
  const button = $('#btn-expand-tree');
  if (!button) return;
  button.classList.toggle('busy', busy);
  button.setAttribute('aria-busy', String(busy));
  button.setAttribute('aria-label', busy ? 'Expanding project folders' : 'Expand all project folders');
  button.title = busy ? 'Expanding project folders (Collapse all to stop)' : 'Expand all project folders (ignored folders stay closed)';
}

let saveDirsTimer = null;
function persistOpenDirs() {
  if (saveDirsTimer) clearTimeout(saveDirsTimer);
  saveDirsTimer = setTimeout(async () => {
    try {
      await apiPost('/api/session', { openDirs: Array.from(openDirs) });
    } catch {}
  }, 300);
}

/* git status letter -> CSS class + label. Empty/absent = clean, no badge. */
const GIT_STATUS = {
  M: ['git-M', 'modified'], A: ['git-A', 'added'], D: ['git-D', 'deleted'],
  U: ['git-untracked', 'untracked'], R: ['git-R', 'renamed'],
  C: ['git-A', 'copied'], '!': ['git-M', 'unmerged'],
};

export async function drawTree(dir, container, depth, isCurrent) {
  let j;
  try { j = await api('/api/tree', { dir }); } catch { return false; }
  if (isCurrent && !isCurrent()) return false;
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
    const tick = g ? '<button class="stage-tick' + (c.staged ? ' staged' : '') + '" data-stage="' + esc(c.path) + '" title="' + (c.staged ? 'Unstage' : 'Stage') + '"></button>' : '';
    return '<div class="tr file' + ig + gc + '" data-file="' + esc(c.path) + '" style="padding-left:' + (pad + 12) + 'px" title="Open ' + esc(c.path) + note + '">' +
      '<span class="ic" data-t="' + fileKind(c.name) + '"></span><span class="nm">' + esc(c.name) + '</span>' + badge + tick + '</div>';
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

export async function refreshTree() {
  const version = ++expansionVersion;
  const isCurrent = () => version === expansionVersion;
  setExpandBusy(false);
  if (!(await drawTree('', treeEl, 0, isCurrent))) return;
  const dirs = Array.from(openDirs).sort((a, b) => a.split('/').length - b.split('/').length);
  for (const path of dirs) {
    if (!isCurrent()) return;
    const dirRow = treeEl.querySelector('[data-dir="' + CSS.escape(path) + '"]');
    const kids = treeEl.querySelector('[data-kids="' + CSS.escape(path) + '"]');
    if (kids && dirRow) {
      dirRow.classList.add('open');
      kids.classList.add('open');
      const loaded = await drawTree(path, kids, path.split('/').length, isCurrent);
      if (!isCurrent()) return;
      if (loaded) kids.dataset.loaded = '1';
      else {
        dirRow.classList.remove('open');
        kids.classList.remove('open');
        openDirs.delete(path);
      }
    } else {
      openDirs.delete(path);
    }
  }
  if (isCurrent() && treeEl.classList.contains('changed-only')) {
    await expandDirtyDirs();
  }
}

export function restoreOpenDirs(dirs) {
  if (Array.isArray(dirs)) {
    for (const d of dirs) {
      if (typeof d === 'string') openDirs.add(d);
    }
  }
}

export function collapseAllDirs() {
  expansionVersion++;
  openDirs.clear();

  treeEl.querySelectorAll('.tr.dir.open').forEach(row => {
    row.classList.remove('open');
  });

  treeEl.querySelectorAll('.kids.open').forEach(kids => {
    kids.classList.remove('open');
  });

  setExpandBusy(false);
  persistOpenDirs();
}

export async function expandAllDirs() {
  if (treeEl.classList.contains('changed-only') || $('#btn-expand-tree')?.classList.contains('busy')) return;
  const version = ++expansionVersion;
  setExpandBusy(true);
  const queue = Array.from(treeEl.querySelectorAll('.tr.dir'), row => row.dataset.dir);
  const seen = new Set();
  let failed = 0;

  try {
    while (queue.length && version === expansionVersion && !treeEl.classList.contains('changed-only')) {
      const batch = queue.splice(0, 4);
      const descendants = await Promise.all(batch.map(async path => {
        if (seen.has(path)) return [];
        seen.add(path);
        const row = treeEl.querySelector('[data-dir="' + CSS.escape(path) + '"]');
        const kids = treeEl.querySelector('[data-kids="' + CSS.escape(path) + '"]');
        if (!row || !kids) return [];
        if (row.classList.contains('ignored')) return [];

        row.classList.add('open');
        kids.classList.add('open');
        openDirs.add(path);
        if (!kids.dataset.loaded) {
          const loaded = await drawTree(path, kids, path.split('/').length, () => version === expansionVersion);
          if (!loaded) {
            if (version === expansionVersion) {
              row.classList.remove('open');
              kids.classList.remove('open');
              openDirs.delete(path);
              failed++;
            }
            return [];
          }
          kids.dataset.loaded = '1';
        }
        if (version !== expansionVersion) return [];
        return Array.from(kids.querySelectorAll(':scope > .tw > .tr.dir'), child => child.dataset.dir);
      }));
      for (const paths of descendants) queue.push(...paths);
    }
  } finally {
    if (version === expansionVersion) {
      persistOpenDirs();
      setExpandBusy(false);
      if (failed) showToast('!', failed + (failed === 1 ? ' folder could not be loaded' : ' folders could not be loaded'));
    }
  }
}

/* Expand the tree down to dir and scroll it into view. */
export async function revealDir(dir) {
  const version = ++expansionVersion;
  const isCurrent = () => version === expansionVersion;
  setExpandBusy(false);
  const parts = dir.split('/');
  for (let i = 0; i < parts.length; i++) {
    if (!isCurrent()) return;
    const p = parts.slice(0, i + 1).join('/');
    const row = treeEl.querySelector('[data-dir="' + CSS.escape(p) + '"]');
    if (!row) break;
    if (!row.classList.contains('open')) {
      row.classList.add('open');
      const kids = treeEl.querySelector('[data-kids="' + CSS.escape(p) + '"]');
      if (kids) {
        kids.classList.add('open');
        openDirs.add(p);
        const loaded = await drawTree(p, kids, p.split('/').length, isCurrent);
        if (!isCurrent()) return;
        if (loaded) kids.dataset.loaded = '1';
        else {
          row.classList.remove('open');
          kids.classList.remove('open');
          openDirs.delete(p);
          break;
        }
      }
    }
  }
  if (!isCurrent()) return;
  const last = treeEl.querySelector('[data-dir="' + CSS.escape(dir) + '"]');
  if (last) last.scrollIntoView({ block: 'center' });
  persistOpenDirs();
}

export async function revealFile(path) {
  const idx = path.lastIndexOf('/');
  if (idx > 0) await revealDir(path.slice(0, idx));
  const row = treeEl.querySelector('[data-file="' + CSS.escape(path) + '"]');
  if (row) {
    $$('.tr.sel', treeEl).forEach(x => x.classList.remove('sel'));
    row.classList.add('sel');
    row.scrollIntoView({ block: 'center' });
  }
}

export async function expandDirtyDirs(container = treeEl, version = expansionVersion) {
  const isCurrent = () => version === expansionVersion && treeEl.classList.contains('changed-only');
  if (!isCurrent()) return;
  const dirtyRows = Array.from(container.querySelectorAll('.tr.dir.dirty:not(.open)'));
  for (const dirRow of dirtyRows) {
    if (!isCurrent()) return;
    const path = dirRow.dataset.dir;
    const kids = container.querySelector('[data-kids="' + CSS.escape(path) + '"]');
    if (kids) {
      dirRow.classList.add('open');
      kids.classList.add('open');
      openDirs.add(path);
      const loaded = await drawTree(path, kids, path.split('/').length, isCurrent);
      if (!isCurrent()) return;
      if (loaded) {
        kids.dataset.loaded = '1';
        await expandDirtyDirs(kids, version);
      } else {
        dirRow.classList.remove('open');
        kids.classList.remove('open');
        openDirs.delete(path);
      }
    }
  }
  if (isCurrent()) persistOpenDirs();
}

export async function patchTreeGitStatus(statuses = {}, dirtyDirs = {}, staged = {}) {
  // 1. Update folder dirty classes
  const dirRows = treeEl.querySelectorAll('.tr.dir');
  for (const dirRow of dirRows) {
    const p = dirRow.dataset.dir;
    dirRow.classList.toggle('dirty', !!dirtyDirs[p]);
  }

  // 2. Clear stale dirty/status markers on files that are now clean
  const dirtyFiles = treeEl.querySelectorAll('.tr.file.dirty');
  for (const fileRow of dirtyFiles) {
    const p = fileRow.dataset.file;
    if (!statuses[p]) {
      fileRow.classList.remove('dirty', 'git-M', 'git-A', 'git-D', 'git-untracked', 'git-R');
      const badge = fileRow.querySelector('.gs');
      if (badge) badge.remove();
      const tick = fileRow.querySelector('.stage-tick');
      if (tick) tick.remove();
    }
  }

  // 3. Update or apply badges + stage tick for changed files
  for (const [p, code] of Object.entries(statuses)) {
    const fileRow = treeEl.querySelector('[data-file="' + CSS.escape(p) + '"]');
    if (!fileRow) continue;
    const g = GIT_STATUS[code];
    fileRow.classList.remove('git-M', 'git-A', 'git-D', 'git-untracked', 'git-R');
    if (g) {
      fileRow.classList.add('dirty', g[0]);
      let badge = fileRow.querySelector('.gs');
      if (!badge) {
        badge = document.createElement('span');
        badge.className = 'gs';
        fileRow.appendChild(badge);
      }
      badge.title = 'git: ' + g[1];
      badge.textContent = code;
      let tick = fileRow.querySelector('.stage-tick');
      if (!tick) {
        tick = document.createElement('button');
        tick.className = 'stage-tick';
        tick.dataset.stage = p;
        fileRow.appendChild(tick);
      }
      tick.classList.toggle('staged', !!staged[p]);
      tick.title = staged[p] ? 'Unstage' : 'Stage';
    } else {
      fileRow.classList.remove('dirty');
      const badge = fileRow.querySelector('.gs');
      if (badge) badge.remove();
      const tick = fileRow.querySelector('.stage-tick');
      if (tick) tick.remove();
    }
  }

  // If in changed-only mode, auto-expand any newly dirty directories
  if (treeEl.classList.contains('changed-only')) {
    await expandDirtyDirs();
  }
}

export function updateSidebarToggleState() {
  const btnChanged = $('#btn-changed');
  const hasGitChanges = !!(S.meta?.git && S.meta.gitChanges > 0);
  if (btnChanged) {
    btnChanged.disabled = !hasGitChanges;
    btnChanged.classList.toggle('disabled', !hasGitChanges);
    if (!S.meta?.git) {
      btnChanged.title = 'Git not available in workspace';
    } else if (!hasGitChanges) {
      btnChanged.title = 'There are no git modified files.';
    } else {
      btnChanged.title = 'Git changes (show changed files only)';
    }
  }
}

export async function setSidebarMode(mode) {
  const btnChanged = $('#btn-changed');
  const btnFiles = $('#btn-files');
  const btnCollapse = $('#btn-collapse-tree');
  const btnExpand = $('#btn-expand-tree');
  updateSidebarToggleState();
  const hasGitChanges = !!(S.meta?.git && S.meta.gitChanges > 0);

  if (mode === 'git' && hasGitChanges) {
    treeEl.classList.add('changed-only');
    btnChanged?.classList.add('active');
    btnFiles?.classList.remove('active');
    if (btnCollapse) btnCollapse.hidden = true;
    if (btnExpand) btnExpand.hidden = true;
    expansionVersion++;
    setExpandBusy(false);
    await expandDirtyDirs();
  } else {
    treeEl.classList.remove('changed-only');
    btnFiles?.classList.add('active');
    btnChanged?.classList.remove('active');
    if (btnCollapse) btnCollapse.hidden = false;
    if (btnExpand) btnExpand.hidden = false;
  }
}

export function initTree() {
  updateSidebarToggleState();

  $('#btn-collapse-tree')?.addEventListener('click', () => {
    if (treeEl.classList.contains('changed-only')) return;
    collapseAllDirs();
  });

  $('#btn-expand-tree')?.addEventListener('click', () => {
    expandAllDirs();
  });

  $('#btn-changed')?.addEventListener('click', async () => {
    const hasGitChanges = !!(S.meta?.git && S.meta.gitChanges > 0);
    if (!hasGitChanges) return;
    await setSidebarMode('git');
  });

  $('#btn-files')?.addEventListener('click', () => {
    setSidebarMode('files');
  });

  treeEl.addEventListener('click', async e => {
    const tick = e.target.closest('.stage-tick');
    if (tick) {
      e.stopPropagation();
      const path = tick.dataset.stage;
      const staged = tick.classList.toggle('staged'); // optimistic; the git-status stream reconciles it
      tick.title = staged ? 'Unstage' : 'Stage';
      try {
        await apiPostJson(staged ? '/api/git/stage' : '/api/git/unstage', { path });
      } catch (err) {
        tick.classList.toggle('staged', !staged);
        tick.title = staged ? 'Stage' : 'Unstage';
        showToast('!', err.message || 'Could not update staging');
      }
      return;
    }
    const dirRow = e.target.closest('[data-dir]');
    if (dirRow) {
      expansionVersion++;
      setExpandBusy(false);
      const path = dirRow.dataset.dir;
      const kids = treeEl.querySelector('[data-kids="' + CSS.escape(path) + '"]');
      const open = dirRow.classList.toggle('open');
      kids.classList.toggle('open', open);
      if (open) {
        openDirs.add(path);
        kids.dataset.loaded = '1';
        await drawTree(path, kids, path.split('/').length);
      } else openDirs.delete(path);
      persistOpenDirs();
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
