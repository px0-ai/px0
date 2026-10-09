// web/src/gitpanel.js
// Sidebar git panel: stage/commit/push/pull, shown whenever the workspace is
// a git repo (same gate as the diff-view toggle in status.js). In a PR
// review session (S.meta.pr set), Push/Pull target the PR's actual head
// branch instead of the checkout's own remote -- see pr.go's Push/Pull.
import { $, esc, S, api, apiPostJson } from './state.js';
import { showToast, copyToClipboard, flashActionSuccess } from './ui.js';
import { reindexWorkspace } from './panels.js';
import { refreshPRMeta } from './pr.js';
import { reloadOpenTabs } from './tabs.js';
import { openSettings } from './settings.js';
import { layout, render } from './renderer.js';
import { refreshUnpushed } from './unpushed.js';
import { openFile } from './tabs.js';
import { on } from './bus.js';
import { commitFileRows, COMMIT_FILES_PAGE, loadCommitFiles, cachedCommitFiles } from './commitfiles.js';
import { triggerRefresh } from './gitstream.js';

const panel = () => $('#git-panel');

export function initGitPanel() {
  if (!panel()) return;

  $('#git-panel-collapse')?.addEventListener('click', () => {
    panel()?.classList.toggle('collapsed');
    layout(); render();
  });

  const rz = $('#git-panel-resizer');
  if (rz && panel()) {
    let dragging = false;
    rz.addEventListener('mousedown', e => {
      dragging = true;
      rz.classList.add('drag');
      panel().classList.remove('collapsed');
      e.preventDefault();
    });
    addEventListener('mousemove', e => {
      if (!dragging) return;
      const bottom = panel().getBoundingClientRect().bottom;
      const h = Math.max(60, Math.min(window.innerHeight * 0.8, bottom - e.clientY));
      panel().style.height = h + 'px';
      layout(); render();
    });
    addEventListener('mouseup', () => {
      if (!dragging) return;
      dragging = false;
      rz.classList.remove('drag');
      layout(); render();
    });
  }

  $('#git-stage-all')?.addEventListener('click', doStageAll);
  $('#git-commit')?.addEventListener('click', doCommit);
  $('#git-commit-msg')?.addEventListener('input', () => $('#git-commit-msg')?.classList.remove('warn-border'));
  $('#git-push')?.addEventListener('click', doPush);
  $('#git-pull')?.addEventListener('click', doPull);
  $('#git-generate-msg')?.addEventListener('click', doCommitWithAI);
  $('#git-token-nudge')?.addEventListener('click', () => {
    showToast('!', 'No GitHub token found: set GITHUB_TOKEN, set GH_TOKEN, or run `gh auth login` -- or add one below.', 5000);
    openSettings('ui', 'GitHub', 'github.token');
  });
  $('#git-write-msg-link')?.addEventListener('click', e => {
    e.preventDefault();
    toggleCommitMsgBox();
  });
  $('#git-generate-link')?.addEventListener('click', e => {
    e.preventDefault();
    doGenerateMessage();
  });
  $('#git-instructions-link')?.addEventListener('click', e => {
    e.preventDefault();
    openSettings('ui', 'Git & Diff', 'git.commitMessageInstruction');
  });
  $('#git-see-all-commits')?.addEventListener('click', handleSeeAllCommits);
  initCommitList();

  updateGitPanelVisibility();
  if (S.meta?.git) {
    fetchRecentCommits();
  }
}

function updateGitPanelVisibility() {
  const p = panel();
  if (!p) return;
  p.hidden = !S.meta?.git;
  const nudge = $('#git-token-nudge');
  if (nudge) {
    // The PR bar already carries its own "no GitHub token" note, so skip
    // this one in PR review mode to avoid nudging twice.
    nudge.hidden = !S.meta?.git || S.meta?.githubToken !== false || !!S.meta?.pr;
  }
}

// Called from gitstream.js's handleGitStatus with each SSE/refresh payload,
// so the branch name and staged/changed counts stay live without a manual
// reload.
export function updateGitPanel(payload) {
  updateGitPanelVisibility();
  const branchEl = $('#git-branch');
  if (branchEl) {
    const branch = S.meta?.pr ? S.meta.pr.head + ' (PR review)' : (payload?.branch || '');
    branchEl.textContent = branch;
    branchEl.title = branch;
  }
  const staged = payload?.staged ? Object.keys(payload.staged).length : 0;
  const changed = payload?.gitChanges ?? (payload?.statuses ? Object.keys(payload.statuses).length : (S.meta?.gitChanges || 0));
  const countsEl = $('#git-counts');
  if (countsEl) {
    countsEl.textContent = changed ? staged + ' / ' + changed + ' staged' : '';
  }

  // Lifecycle state management:
  // Show commit section when there are uncommitted changes or staged files.
  // When clean, hide commit section and show clean state message.
  const hasChanges = changed > 0 || staged > 0;
  const commitSection = $('#git-commit-section');
  if (commitSection) {
    commitSection.hidden = !hasChanges;
  }
  const cleanState = $('#git-clean-state');
  if (cleanState) {
    cleanState.hidden = hasChanges;
  }

  // Commit button is only enabled when something is staged
  const commitBtn = $('#git-commit');
  if (commitBtn) {
    commitBtn.disabled = staged === 0;
    commitBtn.title = staged === 0 ? 'Stage changes to commit' : 'Commit staged changes';
  }

  // Push button is enabled only when there are unpushed commits (ahead > 0)
  const pushBtn = $('#git-push');
  if (pushBtn) {
    const ahead = payload?.ahead ?? 0;
    pushBtn.disabled = ahead === 0;
    if (ahead > 0) {
      pushBtn.textContent = `Push (${ahead})`;
      pushBtn.title = S.meta?.pr
        ? `Push ${ahead} of your commit${ahead > 1 ? 's' : ''} to PR #${S.meta.pr.number} (${S.meta.pr.head})`
        : `Push ${ahead} unpushed commit${ahead > 1 ? 's' : ''} to remote`;
    } else {
      pushBtn.textContent = 'Push';
      pushBtn.title = S.meta?.pr ? 'Commit your changes to push them to this PR' : 'No unpushed commits to push';
    }
  }

  // Pull button: show incoming badge if behind > 0
  const pullBtn = $('#git-pull');
  if (pullBtn) {
    const behind = payload?.behind ?? 0;
    if (behind > 0) {
      pullBtn.textContent = `Pull (${behind})`;
      pullBtn.title = `Pull ${behind} incoming commit${behind > 1 ? 's' : ''} from remote`;
    } else {
      pullBtn.textContent = 'Pull';
      pullBtn.title = 'Pull changes from remote';
    }
  }

  // Render recent commits if provided in payload
  if (payload?.recentCommits) {
    renderRecentCommits(payload.recentCommits);
  }
  updateSeeAllCommits(payload?.commitsUrl, payload?.recentCommits ? payload.recentCommits.length : undefined);
}

export async function stagePath(path) {
  try {
    await apiPostJson('/api/git/stage', { path });
  } catch (e) {
    showToast('!', e.message || 'Could not stage');
  }
}

async function doStageAll() {
  const btn = $('#git-stage-all');
  const prevText = btn?.textContent || 'Stage All';
  if (btn) {
    btn.disabled = true;
    btn.textContent = 'Staging...';
  }
  try {
    await apiPostJson('/api/git/stage', { path: '.' });
    await triggerRefresh();
    if (btn) {
      btn.textContent = prevText;
      flashActionSuccess(btn, 'Staged');
    }
  } catch (e) {
    if (btn) btn.textContent = prevText;
    showToast('!', e.message || 'Could not stage');
  } finally {
    if (btn) btn.disabled = false;
  }
}

export async function unstagePath(path) {
  try {
    await apiPostJson('/api/git/unstage', { path });
    await triggerRefresh();
  } catch (e) {
    showToast('!', e.message || 'Could not unstage');
  }
}

// The message box stays collapsed behind a text link since most commits use
// "Stage all + Commit with AI"; open it on demand to write a message by hand.
function toggleCommitMsgBox(open) {
  const ta = $('#git-commit-msg');
  const link = $('#git-write-msg-link');
  if (!ta) return;
  ta.hidden = open === undefined ? !ta.hidden : !open;
  if (link) link.textContent = ta.hidden ? 'write message' : 'hide message';
  const gen = $('#git-generate-link');
  if (gen) {
    gen.hidden = ta.hidden;
    if (gen.previousElementSibling) gen.previousElementSibling.hidden = ta.hidden;
  }
  if (!ta.hidden) ta.focus();
}

async function doCommit() {
  const ta = $('#git-commit-msg');
  if (ta?.hidden) {
    toggleCommitMsgBox(true);
    return;
  }
  const message = ta ? ta.value.trim() : '';
  if (!message) {
    if (ta) {
      ta.classList.remove('shake');
      void ta.offsetWidth;
      ta.classList.add('shake', 'warn-border');
      setTimeout(() => ta.classList.remove('shake'), 350);
      ta.focus();
    }
    showToast('!', 'Write a commit message first');
    return;
  }
  const btn = $('#git-commit');
  const prevText = btn?.textContent || 'Commit';
  if (btn) {
    btn.disabled = true;
    btn.textContent = 'Committing...';
  }
  try {
    await apiPostJson('/api/git/commit', { message });
    if (ta) {
      ta.value = '';
      ta.classList.remove('warn-border');
    }
    toggleCommitMsgBox(false);
    showToast('✓', 'Committed');
    if (btn) {
      btn.textContent = prevText;
      flashActionSuccess(btn, 'Committed');
    }
    await fetchRecentCommits();
    await refreshUnpushed();
    await triggerRefresh();
  } catch (e) {
    if (btn) btn.textContent = prevText;
    showToast('!', e.message || 'Commit failed');
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function doPush() {
  const btn = $('#git-push');
  const prevText = btn?.textContent || 'Push';
  if (btn) {
    btn.disabled = true;
    btn.textContent = 'Pushing...';
  }
  try {
    await apiPostJson('/api/git/push', {});
    showToast('✓', S.meta?.pr ? `Pushed to PR #${S.meta.pr.number}` : 'Pushed');
    if (btn) {
      btn.textContent = 'Push';
      flashActionSuccess(btn, 'Pushed');
      btn.title = 'No unpushed commits to push';
      btn.disabled = true;
    }
    await refreshUnpushed(); // the section empties out with the push
    if (S.meta?.pr) {
      // Your commits are the PR's now: re-read the PR state (its file set and
      // head) and repaint open diffs so "Your changes" empties out.
      await refreshPRMeta();
      await reloadOpenTabs();
    }
  } catch (e) {
    if (btn) btn.textContent = prevText;
    showToast('!', e.message || 'Push failed');
    if (btn) btn.disabled = false;
  }
}

async function doPull() {
  const btn = $('#git-pull');
  const prevText = btn?.textContent || 'Pull';
  if (btn) {
    btn.disabled = true;
    btn.textContent = 'Pulling...';
  }
  try {
    const j = await apiPostJson('/api/git/pull', {});
    showToast('✓', j.message || 'Pulled');
    if (btn) {
      btn.textContent = 'Pull';
      flashActionSuccess(btn, 'Pulled');
    }
    await reindexWorkspace();
    if (S.meta?.pr) await refreshPRMeta();
    await reloadOpenTabs(); // open files and diffs show the pulled content, not what was loaded before
    await fetchRecentCommits();
    await refreshUnpushed();
  } catch (e) {
    if (btn) btn.textContent = prevText;
    showToast('!', e.message || 'Pull failed');
  } finally {
    if (btn) btn.disabled = false;
  }
}

// Dispatches Stage + Commit with AI:
// 1. Stages all changes
// 2. Dispatches the selected coding harness to write a commit message
//    (honoring git.commitMessageInstruction setting)
// 3. Automatically commits with the generated message
async function doCommitWithAI() {
  const btn = $('#git-generate-msg');
  if (btn) {
    btn.disabled = true;
    btn.textContent = 'Staging...';
  }

  // Stage all changes
  try {
    await apiPostJson('/api/git/stage', { path: '.' });
  } catch (e) {
    resetGenerateBtn(btn);
    showToast('!', e.message || 'Could not stage changes');
    return;
  }

  if (btn) {
    btn.textContent = 'Writing message...';
  }
  let job;
  try {
    job = await apiPostJson('/api/git/commit-message', {});
  } catch (e) {
    resetGenerateBtn(btn);
    showToast('!', e.message || 'Could not start generation');
    return;
  }
  pollCommitMessage(job.id, {
    progress: t => { if (btn) btn.textContent = t; },
    reset: () => resetGenerateBtn(btn),
    done: async text => {
      const ta = $('#git-commit-msg');
      if (ta) ta.value = text;
      await commitWithMessage(text, btn);
    },
  });
}

function resetGenerateBtn(btn) {
  if (!btn) return;
  btn.disabled = false;
  btn.textContent = 'Stage all + Commit with AI';
}

// Polls the commit-message job. `ui` abstracts the trigger element:
// progress(text) shows status, reset() restores it, done(text) receives the
// cleaned message (the AI button commits it; the link only fills the textarea).
function pollCommitMessage(id, ui) {
  const poll = async () => {
    let j;
    try {
      j = await api('/api/agent/job?id=' + id);
    } catch (e) {
      ui.reset();
      showToast('!', e.message || 'Generation failed');
      return;
    }
    if (j.running) {
      const sec = Math.round((j.ms || 0) / 1000);
      ui.progress('Writing message... (' + sec + 's)');
      setTimeout(poll, 600);
      return;
    }
    if (j.error) {
      ui.reset();
      showToast('!', (j.harness || 'agent') + ': ' + j.error);
      return;
    }
    const text = cleanCommitMessage(j.stdout || j.log || '');
    if (!text) {
      ui.reset();
      showToast('!', 'Harness returned an empty message');
      return;
    }
    await ui.done(text);
  };
  setTimeout(poll, 400);
}

// Generates a message for already-staged changes and fills the textarea
// without committing, so it can be reviewed or edited first.
async function doGenerateMessage() {
  const link = $('#git-generate-link');
  const ta = $('#git-commit-msg');
  if (!link || link.dataset.busy) return;
  const label = link.textContent;
  link.dataset.busy = '1';
  const reset = () => {
    delete link.dataset.busy;
    link.textContent = label;
  };
  link.textContent = 'Writing message...';
  let job;
  try {
    job = await apiPostJson('/api/git/commit-message', {});
  } catch (e) {
    reset();
    showToast('!', e.message || 'Could not start generation');
    return;
  }
  pollCommitMessage(job.id, {
    progress: t => { link.textContent = t; },
    reset,
    done: text => {
      reset();
      if (ta) ta.value = text;
      toggleCommitMsgBox(true);
    },
  });
}

async function commitWithMessage(message, btn) {
  if (btn) btn.textContent = 'Committing...';
  try {
    await apiPostJson('/api/git/commit', { message });
    const ta = $('#git-commit-msg');
    if (ta) {
      ta.value = '';
      ta.classList.remove('warn-border');
    }
    showToast('✓', 'Committed with AI');
    await fetchRecentCommits();
    await refreshUnpushed();
    await triggerRefresh();
    if (btn) {
      btn.textContent = 'Stage all + Commit with AI';
      flashActionSuccess(btn, 'Committed');
    }
  } catch (e) {
    toggleCommitMsgBox(true); // surface the generated message so it isn't lost
    showToast('!', e.message || 'Commit failed');
  } finally {
    if (!btn?._flashTimer) resetGenerateBtn(btn);
    else btn.disabled = false;
  }
}

// Harnesses sometimes wrap output in a markdown code fence despite being
// asked not to; strip that and surrounding whitespace before using it.
function cleanCommitMessage(text) {
  let t = text.trim();
  const fence = t.match(/^```[a-z]*\n([\s\S]*?)\n```$/);
  if (fence) t = fence[1].trim();
  return t;
}

export async function fetchRecentCommits() {
  if (!S.meta?.git) return;
  try {
    const res = await api('/api/git/log?limit=5');
    if (res?.commits) {
      renderRecentCommits(res.commits);
      updateSeeAllCommits(res.commitsUrl, res.commits.length);
    }
  } catch {
    // Silently ignore if git log not available
  }
}

let currentCommitsUrl = '';

function updateSeeAllCommits(commitsUrl, commitCount) {
  if (commitsUrl) currentCommitsUrl = commitsUrl;
  if (S.meta?.pr?.webUrl) {
    currentCommitsUrl = S.meta.pr.webUrl + '/commits';
  }
  const link = $('#git-see-all-commits');
  if (!link) return;
  if (commitCount === 0) {
    link.hidden = true;
    return;
  }
  link.hidden = false;
  if (currentCommitsUrl) {
    link.href = currentCommitsUrl;
    link.target = '_blank';
    link.rel = 'noopener';
    link.title = 'View all repository commits in browser';
  } else {
    link.href = '#';
    link.removeAttribute('target');
    link.title = 'View commits';
  }
}

async function handleSeeAllCommits(e) {
  if (currentCommitsUrl) return; // Follow standard hyperlink
  e.preventDefault();
  try {
    const res = await api('/api/git/log?limit=50');
    if (res?.commits) {
      renderRecentCommits(res.commits, 0);
      const link = $('#git-see-all-commits');
      if (link) link.hidden = true;
    }
  } catch (err) {
    showToast('!', err.message || 'Could not load commits');
  }
}

/* ---------- recent commits: expandable rows ----------
   The git status stream re-sends the commits on every tick, so the list is
   rebuilt only when they change, and expanded rows are restored from cache.
   A file opens as a tab pinned to the commit (openFile's ref), the same as
   the Unpushed section; the rows themselves come from commitfiles.js. */

const listedCommits = new Map(); // full SHA -> GitCommit, for the expanded meta line
const expandedCommits = new Set();
let renderedCommitsKey = '';

function renderRecentCommits(commits, max = 5) {
  const list = $('#git-commits-list');
  if (!list) return;
  if (!commits || commits.length === 0) {
    renderedCommitsKey = '';
    list.innerHTML = '<div class="git-commits-empty">No commits yet</div>';
    return;
  }
  const slice = max ? commits.slice(0, max) : commits;
  const key = slice.map(c => c.full + '\x1f' + c.subject + '\x1f' + c.date).join('\x1e');
  if (key === renderedCommitsKey) return;
  renderedCommitsKey = key;
  listedCommits.clear();
  for (const c of slice) listedCommits.set(c.full, c);
  const focused = list.contains(document.activeElement) ? document.activeElement.closest('.git-commit')?.dataset.sha : null;
  list.innerHTML = slice.map(c => `
    <div class="git-commit" data-sha="${esc(c.full)}">
      <div class="git-commit-row" tabindex="0" role="button" aria-expanded="false" title="${esc(c.subject || '')} (${esc(c.author || '')}, ${esc(c.date || '')})&#10;Click to show changed files">
        <svg class="git-commit-caret" viewBox="0 0 10 10" width="8" height="8" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3.5 2L6.5 5L3.5 8"/></svg>
        <svg class="git-commit-icon" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="8" cy="8" r="2.8"/><line x1="8" y1="1" x2="8" y2="5.2"/><line x1="8" y1="10.8" x2="8" y2="15"/></svg>
        <span class="git-commit-msg-text">${esc(c.subject || '(no message)')}</span>
        <button type="button" class="git-commit-sha" tabindex="-1" title="Copy SHA">${esc(c.hash)}</button>
      </div>
      <div class="git-commit-files" hidden></div>
    </div>
  `).join('');
  for (const sha of [...expandedCommits]) {
    const el = commitEl(sha);
    if (el) setCommitExpanded(el, true);
    else expandedCommits.delete(sha);
  }
  if (focused) commitEl(focused)?.querySelector('.git-commit-row')?.focus();
}

function commitEl(sha) {
  return $('#git-commits-list')?.querySelector(`.git-commit[data-sha="${CSS.escape(sha)}"]`);
}

async function setCommitExpanded(el, open) {
  const sha = el.dataset.sha;
  const row = el.querySelector('.git-commit-row');
  const body = el.querySelector('.git-commit-files');
  el.classList.toggle('expanded', open);
  row.setAttribute('aria-expanded', String(open));
  body.hidden = !open;
  if (!open) {
    expandedCommits.delete(sha);
    body.replaceChildren(); // hiding would keep every row in memory
    return;
  }
  expandedCommits.add(sha);
  const cached = cachedCommitFiles(sha);
  if (cached) { drawCommitFiles(body, sha, cached); return; }
  body.innerHTML = '<div class="git-commit-note">Loading…</div>';
  try {
    const files = await loadCommitFiles(sha);
    if (el.isConnected && expandedCommits.has(sha)) drawCommitFiles(body, sha, files);
  } catch (e) {
    if (el.isConnected) body.innerHTML = `<div class="git-commit-note">${esc(e.message || 'Could not load commit')}</div>`;
  }
}

function drawCommitFiles(body, sha, files) {
  const c = listedCommits.get(sha);
  const meta = c ? `<div class="git-commit-meta">${esc(c.author)} · ${esc(c.date)}</div>` : '';
  if (!files.length) {
    body.innerHTML = meta + '<div class="git-commit-note">No file changes</div>';
    return;
  }
  body.innerHTML = meta;
  appendCommitFiles(body, sha, files);
}

// Builds the next page of rows; returns the index of the first one added.
function appendCommitFiles(body, sha, files) {
  const from = body.querySelectorAll('.up-file').length;
  body.querySelector('.up-more')?.remove();
  body.insertAdjacentHTML('beforeend', commitFileRows(files, from, from + COMMIT_FILES_PAGE, activeCommitPath(sha)));
  return from;
}

function showMoreCommitFiles(el) {
  const files = cachedCommitFiles(el.dataset.sha);
  if (!files) return -1;
  return appendCommitFiles(el.querySelector('.git-commit-files'), el.dataset.sha, files);
}

function openCommitPath(el, path) {
  // px0 has no diff for a binary file, so say so rather than doing nothing.
  if (cachedCommitFiles(el.dataset.sha)?.find(f => f.path === path)?.binary) {
    showToast('!', 'Binary file, no diff to show');
    return;
  }
  openFile(path, { ref: el.dataset.sha, view: 'diff' });
}

// Double-click or Enter on a commit: expand it and open its first file.
async function openFirstCommitFile(el) {
  if (!el.classList.contains('expanded')) setCommitExpanded(el, true);
  try {
    const files = await loadCommitFiles(el.dataset.sha);
    if (files.length) openCommitPath(el, files[0].path);
    else showToast('!', 'This commit changes no files');
  } catch (e) {
    showToast('!', e.message || 'Could not load commit');
  }
}

// The path the active tab shows if it is pinned to sha, else ''.
function activeCommitPath(sha) {
  const d = S.tabs[S.active];
  return d && d.diffRef === sha ? d.path : '';
}

function markActiveCommitFile() {
  const list = $('#git-commits-list');
  if (!list) return;
  for (const x of list.querySelectorAll('.up-file.sel')) x.classList.remove('sel');
  for (const el of list.querySelectorAll('.git-commit.expanded')) {
    const path = activeCommitPath(el.dataset.sha);
    if (path) el.querySelector(`.up-file[data-path="${CSS.escape(path)}"]`)?.classList.add('sel');
  }
}

function initCommitList() {
  const list = $('#git-commits-list');
  if (!list) return;
  list.addEventListener('click', e => {
    const sha = e.target.closest('.git-commit-sha');
    if (sha) {
      e.stopPropagation();
      copyToClipboard(sha.textContent, 'Copied ' + sha.textContent, sha);
      return;
    }
    const more = e.target.closest('.up-more');
    if (more) { showMoreCommitFiles(more.closest('.git-commit')); return; }
    const file = e.target.closest('.up-file');
    if (file) { openCommitPath(file.closest('.git-commit'), file.dataset.path); return; }
    const row = e.target.closest('.git-commit-row');
    // The second click of a double-click opens instead (dblclick below).
    if (row && e.detail < 2) {
      const el = row.closest('.git-commit');
      setCommitExpanded(el, !el.classList.contains('expanded'));
    }
  });
  list.addEventListener('dblclick', e => {
    const row = e.target.closest('.git-commit-row');
    if (!row || e.target.closest('.git-commit-sha')) return;
    openFirstCommitFile(row.closest('.git-commit'));
  });
  list.addEventListener('keydown', e => {
    const item = e.target.closest('.git-commit-row, .up-file, .up-more');
    if (!item) return;
    const el = item.closest('.git-commit');
    const isRow = item.classList.contains('git-commit-row');
    if (item.classList.contains('up-more') && (e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault();
      const first = showMoreCommitFiles(el);
      el.querySelectorAll('.up-file')[first]?.focus();
      return;
    }
    const items = [...list.querySelectorAll('.git-commit-row, .git-commit.expanded .up-file, .git-commit.expanded .up-more')];
    const at = items.indexOf(item);
    switch (e.key) {
      case 'ArrowDown': items[at + 1]?.focus(); break;
      case 'ArrowUp': items[at - 1]?.focus(); break;
      case 'ArrowRight':
        if (isRow && !el.classList.contains('expanded')) setCommitExpanded(el, true);
        else if (isRow) items[at + 1]?.focus();
        break;
      case 'ArrowLeft':
        if (isRow) setCommitExpanded(el, false);
        else el.querySelector('.git-commit-row').focus();
        break;
      case 'Enter':
        if (isRow) openFirstCommitFile(el);
        else openCommitPath(el, item.dataset.path);
        break;
      case ' ':
        if (isRow) setCommitExpanded(el, !el.classList.contains('expanded'));
        else openCommitPath(el, item.dataset.path);
        break;
      default: return;
    }
    e.preventDefault();
  });
  on('tab:activated', markActiveCommitFile);
}
