// web/src/tree.js
import { $, $$, esc, api, doc_ } from './state.js';
import { openFile, loadGutter } from './tabs.js';
import { setDiffMode, setDiffRef, syncDiffView } from './diff.js';
import { HOVER_DELAY } from './hover.js';

export const treeEl = $('#tree');
export const openDirs = new Set();
export const unpushedEl = $('#unpushed');
const unpushedList = unpushedEl?.querySelector('.unpushed-list');
const unpushedCount = unpushedEl?.querySelector('.unpushed-count');
const unpushedBranchEl = unpushedEl?.querySelector('.unpushed-branch');
const unpushedBranchName = unpushedEl?.querySelector('.unpushed-branch-name');
const commitHovercard = $('#commit-hovercard');

let expandedCommit = null; // hash of currently expanded commit
let unpushedCommits = null; // last fetched commits, or null when not eligible to show
let unpushedBranch = ''; // tracking ref these commits are ahead of, e.g. "origin/master"
let chcTimer = 0, chcSeq = 0, chcRow = null, chcHideTimer = 0;
const commitDetailCache = new Map(); // hash -> detail response

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

/* Expand the tree down to dir and scroll it into view. */
export async function revealDir(dir) {
  const parts = dir.split('/');
  for (let i = 0; i < parts.length; i++) {
    const p = parts.slice(0, i + 1).join('/');
    const row = treeEl.querySelector('[data-dir="' + CSS.escape(p) + '"]');
    if (!row) break;
    if (!row.classList.contains('open')) row.click();
    await new Promise(r => setTimeout(r, 30));
  }
  const last = treeEl.querySelector('[data-dir="' + CSS.escape(dir) + '"]');
  if (last) last.scrollIntoView({ block: 'center' });
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

// Shown only in "changed files only" mode — it's a review-focused list, not a
// home-screen fixture, so it stays behind the same toggle as the dirty-file filter.
function updateUnpushedVisibility() {
  if (!unpushedEl) return;
  const show = !!unpushedCommits && treeEl.classList.contains('changed-only');
  hideCommitHover();
  unpushedEl.hidden = !show;
  if (!show) { expandedCommit = null; return; }
  unpushedCount.textContent = unpushedCommits.length;
  if (unpushedBranchEl) {
    unpushedBranchEl.hidden = !unpushedBranch;
    unpushedBranchName.textContent = unpushedBranch;
    unpushedBranchEl.title = 'Ahead of ' + unpushedBranch;
  }
  renderUnpushedList(unpushedCommits);
}

export async function loadUnpushed() {
  if (!unpushedEl) return;
  try {
    const j = await api('/api/unpushed');
    unpushedCommits = (j.available && j.hasUpstream && j.commits && j.commits.length) ? j.commits : null;
    unpushedBranch = j.upstream || '';
  } catch (e) {
    unpushedCommits = null;
    unpushedBranch = '';
  }
  updateUnpushedVisibility();
}

function renderUnpushedList(commits) {
  hideCommitHover();
  unpushedList.innerHTML = commits.map(c => {
    const author = (c.author || 'unknown').split(' ').shift(); // first word
    return '<div class="unpushed-commit" data-hash="' + esc(c.short) + '">' +
      '<div class="unpushed-commit-header">' +
      '<span class="unpushed-commit-hash">' + esc(c.short) + '</span>' +
      '<span class="unpushed-commit-subject" title="' + esc(c.subject) + '">' + esc(c.subject) + '</span>' +
      '<span class="unpushed-commit-meta">' +
      '<span class="unpushed-commit-author" title="' + esc(c.author) + '">' + esc(author) + '</span>' +
      '</span></div>' +
      '<div class="unpushed-commit-files"></div>' +
      '</div>';
  }).join('');
}

async function loadCommitFiles(hash) {
  try {
    const j = await api('/api/commitfiles', { hash });
    if (!j.available) return null;
    return j.files || {};
  } catch (e) {
    return null;
  }
}

const fmtAbs = iso => {
  const d = new Date(iso);
  return isNaN(d) ? '' : d.toLocaleString(undefined, { dateStyle: 'long', timeStyle: 'short' });
};

async function showCommitHover(row) {
  const commitRow = row.closest('.unpushed-commit');
  const hash = commitRow?.dataset.hash;
  const c = unpushedCommits?.find(x => x.short === hash);
  if (!c) return;
  const seq = ++chcSeq;
  let detail = commitDetailCache.get(hash);
  if (!detail) {
    try { detail = await api('/api/commitdetail', { hash }); }
    catch { detail = { available: false }; }
    commitDetailCache.set(hash, detail);
  }
  if (seq !== chcSeq || chcRow !== row || !row.isConnected) return; // moved on, or tree re-rendered

  // detail.files/.insertions/.deletions are ints from the backend, not
  // user-controlled text — safe to interpolate directly, no esc() needed.
  const statParts = [];
  if (detail.available && detail.files > 0) {
    statParts.push(detail.files + ' file' + (detail.files === 1 ? '' : 's') + ' changed');
  }
  if (detail.insertions) statParts.push('<span class="chc-ins">+' + detail.insertions + '</span>');
  if (detail.deletions) statParts.push('<span class="chc-del">-' + detail.deletions + '</span>');
  const stats = statParts.join(', ');
  const authorName = c.author || 'unknown';
  const authorHtml = c.author
    // Best-effort only: a git author NAME isn't always the person's GitHub
    // HANDLE (e.g. "Dhruvil Kakadiya" vs "DhruvilK7") — this can 404 or point
    // at the wrong profile. Cheap and right often enough to be worth it; not
    // verified against the remote or any API.
    ? '<a class="chc-author" href="https://github.com/' + encodeURIComponent(c.author) + '" target="_blank" rel="noopener">' + esc(authorName) + '</a>'
    : '<span class="chc-author">' + esc(authorName) + '</span>';
  commitHovercard.innerHTML =
    '<div>' + authorHtml +
    '<span class="chc-time">' + esc(c.relTime || '') +
      (c.isoTime ? ' (' + esc(fmtAbs(c.isoTime)) + ')' : '') + '</span></div>' +
    '<div class="chc-subject">' + esc(c.subject) + '</div>' +
    (detail.available && detail.body ? '<div class="chc-body">' + esc(detail.body) + '</div>' : '') +
    '<div class="chc-foot">' +
    (stats ? '<span class="chc-stats">' + stats + '</span>' : '<span></span>') +
    '<button type="button" class="chc-copy" data-hash="' + esc(c.hash) + '">Copy SHA</button>' +
    '</div>';
  commitHovercard.hidden = false;
  placeCommitHover(row);
}

function placeCommitHover(row) {
  const r = row.getBoundingClientRect();
  const card = commitHovercard.getBoundingClientRect();
  let left = r.right + 8;
  if (left + card.width > window.innerWidth - 8) left = Math.max(8, r.left - card.width - 8);
  const top = Math.max(8, Math.min(r.top, window.innerHeight - card.height - 8));
  commitHovercard.style.left = left + 'px';
  commitHovercard.style.top = top + 'px';
}

function hideCommitHover() {
  chcSeq++;
  chcRow = null;
  clearTimeout(chcTimer);
  if (!commitHovercard.hidden) { commitHovercard.hidden = true; commitHovercard.innerHTML = ''; }
}

// ponytail: a squashed/vendor-bump commit could touch thousands of files;
// cap the DOM rows rather than render every one. Raise if a real case needs it.
const MAX_COMMIT_FILES = 200;

function renderCommitFiles(files, hash) {
  const el = unpushedList.querySelector('[data-hash="' + CSS.escape(hash) + '"] .unpushed-commit-files');
  if (!el) return;
  const entries = Object.entries(files);
  const shown = entries.slice(0, MAX_COMMIT_FILES);
  let html = shown.map(([path, status]) => {
    const name = path.split('/').pop();
    const g = GIT_STATUS[status];
    const gc = g ? ' ' + g[0] : ''; // g[0] is already formatted like 'git-M'
    const badge = g ? '<span class="gs" title="git: ' + g[1] + '">' + esc(status) + '</span>' : '';
    return '<div class="unpushed-file' + gc + '" data-file="' + esc(path) + '" data-hash="' + esc(hash) + '" title="' + esc(path) + '">' +
      '<span class="ic" data-t="' + fileKind(name) + '"></span>' +
      '<span class="nm">' + esc(name) + '</span>' + badge + '</div>';
  }).join('');
  if (entries.length > MAX_COMMIT_FILES) {
    html += '<div class="unpushed-file-more">+' + (entries.length - MAX_COMMIT_FILES) + ' more</div>';
  }
  el.innerHTML = html;
}

export function initTree() {
  // "Changed only" filter: hide clean files and known-clean folders (CSS-driven).
  $('#btn-changed')?.addEventListener('click', e => {
    const on = treeEl.classList.toggle('changed-only');
    e.currentTarget.classList.toggle('active', on);
    updateUnpushedVisibility();
  });

  treeEl.addEventListener('click', async e => {
    const dirRow = e.target.closest('[data-dir]');
    if (dirRow) {
      const path = dirRow.dataset.dir;
      const kids = treeEl.querySelector('[data-kids="' + CSS.escape(path) + '"]');
      const open = dirRow.classList.toggle('open');
      kids.classList.toggle('open', open);
      if (open) {
        openDirs.add(path);
        if (!kids.dataset.loaded) {
          kids.dataset.loaded = '1';
          await drawTree(path, kids, path.split('/').length);
        }
      } else openDirs.delete(path);
      return;
    }
    const f = e.target.closest('[data-file]');
    if (f) {
      $$('.tr.sel', treeEl).forEach(x => x.classList.remove('sel'));
      f.classList.add('sel');
      openFile(f.dataset.file).then(() => {
        // A tab opened from the plain tree is always working-tree scoped, even
        // if it was last shown pinned to a commit's ref.
        const d = doc_();
        if (d && d.diffRef) {
          setDiffRef(d, '');
          d.gutter = null;
          loadGutter(d, '');
          syncDiffView();
        }
      });
    }
  });

  // Unpushed commits sidebar
  unpushedList?.addEventListener('click', async e => {
    const commitRow = e.target.closest('.unpushed-commit');
    if (!commitRow) return;
    const hash = commitRow.dataset.hash;
    const fileRow = e.target.closest('.unpushed-file');

    if (fileRow) {
      // File clicked: open diff for this file in this commit
      e.stopPropagation();
      const path = fileRow.dataset.file;
      await openFile(path, { push: true });
      // After opening file, scope it to this commit and (re)draw its diff.
      const d = doc_();
      if (d) {
        if (d.diffRef !== hash) {
          setDiffRef(d, hash);
          d.gutter = null;
          loadGutter(d, hash);
        }
        d.diffAvailable = true;
        await setDiffMode('split');
      }
      return;
    }

    // Commit row clicked: expand/collapse
    const isExpanded = commitRow.classList.contains('expanded');
    if (isExpanded) {
      commitRow.classList.remove('expanded');
      expandedCommit = null;
    } else {
      // Collapse previous
      if (expandedCommit) {
        unpushedList.querySelector('[data-hash="' + CSS.escape(expandedCommit) + '"]')
          ?.classList.remove('expanded');
      }
      expandedCommit = hash;
      commitRow.classList.add('expanded');

      // Load files for this commit
      const files = await loadCommitFiles(hash);
      if (files) renderCommitFiles(files, hash);
    }
  });

  // Hover card for commit details. The card is a position:fixed element
  // placed 8px away from the row (placeCommitHover), NOT adjacent to it — a
  // relatedTarget/containment check on mouseout fails because the pointer
  // crosses that gap while briefly over neither element. A short debounced
  // hide (cancelled by mouseover on either the row or the card) tolerates
  // that gap-crossing regardless of the exact path the mouse takes.
  const cancelHideCommitHover = () => clearTimeout(chcHideTimer);
  const scheduleHideCommitHover = () => {
    clearTimeout(chcHideTimer);
    chcHideTimer = setTimeout(hideCommitHover, 250);
  };
  unpushedList?.addEventListener('mouseover', e => {
    const row = e.target.closest('.unpushed-commit-header');
    if (!row) return;
    cancelHideCommitHover();
    if (row === chcRow) return;
    chcRow = row;
    clearTimeout(chcTimer);
    chcTimer = setTimeout(() => showCommitHover(row), HOVER_DELAY);
  });
  unpushedList?.addEventListener('mouseout', e => {
    if (e.target.closest('.unpushed-commit-header')) scheduleHideCommitHover();
  });
  commitHovercard.addEventListener('mouseover', cancelHideCommitHover);
  commitHovercard.addEventListener('mouseleave', scheduleHideCommitHover);
  commitHovercard.addEventListener('click', async e => {
    const btn = e.target.closest('.chc-copy');
    if (!btn) return;
    try {
      await navigator.clipboard.writeText(btn.dataset.hash);
      const prev = btn.textContent;
      btn.textContent = 'Copied';
      setTimeout(() => { btn.textContent = prev; }, 1000);
    } catch { /* clipboard unavailable (e.g. insecure context) — no-op */ }
  });
  unpushedList?.addEventListener('scroll', hideCommitHover, { passive: true });
  // Any click outside the card dismisses it; a click INSIDE it (e.g. the
  // copy button above) must not be wiped from under itself mid-click.
  document.addEventListener('mousedown', e => {
    if (!commitHovercard.contains(e.target)) hideCommitHover();
  });
}
