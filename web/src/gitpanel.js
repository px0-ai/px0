// web/src/gitpanel.js
// Sidebar git panel: stage/commit/push/pull, shown whenever the workspace is
// a git repo (same gate as the diff-view toggle in status.js). In a PR
// review session (S.meta.pr set), Push/Pull target the PR's actual head
// branch instead of the checkout's own remote -- see pr.go's Push/Pull.
import { $, esc, S, api, apiPostJson } from './state.js';
import { showToast, copyToClipboard, flashActionSuccess } from './ui.js';
import { reindexWorkspace } from './panels.js';
import { refreshPRMeta, nudgeForgeToken, forgeInfo } from './pr.js';
import { reloadOpenTabs } from './tabs.js';
import { openSettings } from './settings.js';
import { layout, render } from './renderer.js';
import { refreshUnpushed } from './unpushed.js';
import { openFile } from './tabs.js';
import { on, emit } from './bus.js';
import { commitFileRows, COMMIT_FILES_PAGE, loadCommitFiles, cachedCommitFiles } from './commitfiles.js';
import { handleGitStatus, triggerRefresh } from './gitstream.js';
import { registerAgentPicker } from './agent.js';

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

  $('#git-commit')?.addEventListener('click', doCommit);
  $('#git-commit-msg')?.addEventListener('input', () => $('#git-commit-msg')?.classList.remove('warn-border'));
  $('#git-commit-msg')?.addEventListener('keydown', e => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault();
      doCommit();
    }
  });
  $('#git-push')?.addEventListener('click', () => (rewritten ? confirmForcePush() : doPush()));
  $('#git-pull')?.addEventListener('click', doPull);
  $('#git-generate-msg')?.addEventListener('click', doCommitWithAI);
  $('#git-token-nudge')?.addEventListener('click', nudgeForgeToken);
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
  initNotice();

  syncGitPanelMeta();
}

export function syncGitPanelMeta() {
  updateGitPanelVisibility();
  if (S.meta?.git) {
    fetchRecentCommits();
    checkUnfinishedSync();
  }
}

function updateGitPanelVisibility() {
  const p = panel();
  if (!p) return;
  p.hidden = !S.meta?.git;
  const nudge = $('#git-token-nudge');
  if (nudge) {
    // Only for a remote on a forge px0 knows. The PR bar already carries its
    // own "no token" note, so skip this one in PR review mode to avoid
    // nudging twice.
    const forge = S.meta?.forge;
    nudge.hidden = !S.meta?.git || !forge || forge.hasToken || !!S.meta?.pr;
    const label = nudge.querySelector('span');
    if (forge && label) label.textContent = `No ${forgeInfo().label} token configured — add one`;
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

  // Commit button is enabled when there are changes or staged files
  const commitBtn = $('#git-commit');
  if (commitBtn) {
    commitBtn.disabled = !hasChanges;
    if (!commitBtn._flashTimer) commitBtn.textContent = 'Commit';
    commitBtn.title = staged === 0
      ? 'Commit all changes'
      : `Commit ${staged} staged file${staged > 1 ? 's' : ''}`;
  }

  // Push button is enabled only when there are unpushed commits (ahead > 0)
  const pushBtn = $('#git-push');
  if (payload && !!payload.rewritten !== rewritten) {
    rewritten = !!payload.rewritten;
    emitSync();
  }
  if (pushBtn) {
    const ahead = payload?.ahead ?? 0;
    pushBtn.disabled = ahead === 0 || !!syncJob;
    if (ahead > 0 && rewritten) {
      // A rebased PR branch only reaches the PR by replacing its commits.
      pushBtn.textContent = 'Force-push';
      pushBtn.title = `This checkout was rebased: replace the commits on ${S.meta?.pr?.head || 'the PR branch'} with it (asks first)`;
    } else if (ahead > 0) {
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
    if (syncJob) pullBtn.disabled = true;
  }

  // Render recent commits if provided in payload
  if (payload?.recentCommits) {
    renderRecentCommits(payload.recentCommits);
  }
  updateSeeAllCommits(payload?.commitsUrl, payload?.recentCommits ? payload.recentCommits.length : undefined);
}

export async function stagePath(path) {
  try {
    const res = await apiPostJson('/api/git/stage', { path });
    if (res && res.git !== undefined) await handleGitStatus(res);
    else await triggerRefresh();
  } catch (e) {
    showGitError('Could not stage', e.message);
  }
}

export async function unstagePath(path) {
  try {
    const res = await apiPostJson('/api/git/unstage', { path });
    if (res && res.git !== undefined) await handleGitStatus(res);
    else await triggerRefresh();
  } catch (e) {
    showGitError('Could not unstage', e.message);
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
    showGitError('Write a commit message first');
    return;
  }
  const btn = $('#git-commit');
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
    clearGitNotice();
    showToast('✓', 'Committed');
    if (btn) {
      btn.textContent = 'Commit';
      flashActionSuccess(btn, 'Committed');
    }
    await fetchRecentCommits();
    await refreshUnpushed();
    await triggerRefresh();
  } catch (e) {
    if (btn) {
      btn.textContent = 'Commit';
      btn.disabled = false;
    }
    showGitError('Commit failed', e.message);
  }
}

async function doPush(force = false) {
  const btn = $('#git-push');
  const prevText = btn?.textContent || 'Push';
  if (btn) {
    btn.disabled = true;
    btn.textContent = 'Pushing...';
  }
  try {
    await apiPostJson('/api/git/push', force ? { force: true } : {});
    rewritten = false;
    emitSync();
    clearGitNotice();
    showToast('✓', S.meta?.pr ? `${force ? 'Force-pushed' : 'Pushed'} to PR #${S.meta.pr.number}` : 'Pushed');
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
    showGitError('Push failed', e.message);
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
    clearGitNotice();
    showToast('✓', j.message || 'Pulled');
    if (btn) {
      btn.textContent = 'Pull';
      flashActionSuccess(btn, 'Pulled');
    }
    await afterHistoryChange();
  } catch (e) {
    if (btn) btn.textContent = prevText;
    // A refused fast-forward is a decision, not a dead end: offer to have the
    // harness rebase or merge. Nothing runs unless one of those is clicked.
    const b = e.body || {};
    if (b.code === 'diverged') offerSync(b);
    else if (b.code === 'in-progress') offerUnfinished(b);
    else showGitError('Pull failed', e.message);
  } finally {
    if (btn) btn.disabled = !!syncJob;
  }
}

// After anything that moved HEAD (pull, agent rebase/merge, abort): open
// files, diffs, the tree and the commit lists all show the new history.
async function afterHistoryChange() {
  await reindexWorkspace();
  if (S.meta?.pr) await refreshPRMeta();
  await reloadOpenTabs(); // open files and diffs show the pulled content, not what was loaded before
  await fetchRecentCommits();
  await refreshUnpushed();
  await triggerRefresh();
}

/* ---------- PR branch: rebase onto the base, force-push ----------
   In a PR review the same agent flow brings the PR up to date with its base
   branch: the PR bar asks for it ('pr:resolve') when the forge reports
   conflicts or a branch that is behind. A rebase rewrites the PR's commits,
   so it only reaches the PR by a force-push, and that is always confirmed
   here first. 'git:sync' tells the PR bar what is going on, so Approve and
   Merge never act on commits the checkout has since moved past. */

let rewritten = false; // PR mode: HEAD rebased past the PR head, not yet force-pushed

function emitSync() {
  emit('git:sync', { running: !!syncJob, inProgress: syncInProgress, rewritten });
}

let syncInProgress = ''; // "rebase"/"merge" left half-done, as last seen

async function resolvePRBase() {
  if (syncJob) return;
  const base = S.meta?.pr?.base || 'the base branch';
  notice({ tone: 'busy', title: `Fetching ${base}...`, closable: false });
  let st;
  try {
    st = await api('/api/git/sync-state?fetch=1');
  } catch (e) {
    showGitError(`Could not fetch ${base}`, e.message);
    return;
  }
  syncInProgress = st.inProgress || '';
  emitSync();
  if (st.inProgress) offerUnfinished(st);
  else if (st.rewritten) confirmForcePush();
  else if (st.behind > 0) offerSync(st);
  else {
    notice({
      tone: 'ok', title: `Up to date with ${base}`,
      html: `This checkout already has everything on ${code(base)}. If the PR still shows conflicts, the forge may not have recomputed them yet.`,
    });
  }
}

function confirmForcePush() {
  const pr = S.meta?.pr || {};
  notice({
    tone: 'warn', title: `Force-push to PR #${pr.number}?`,
    html: `This checkout was rebased, so its commits replace the ones on ${code(pr.head)} rather than adding to them.` +
      `<span class="hint">Pushed with a lease: if anyone else pushed to ${esc(pr.head || 'the branch')} since this checkout, the push is refused and nothing of theirs is lost.</span>`,
    actions: [
      { label: 'Force-push', primary: true, title: `git push --force-with-lease to ${pr.head}`, run: () => doPush(true) },
      { label: 'Not now', run: clearGitNotice },
    ],
  });
}

/* ---------- notice: git outcomes, inline ----------
   Every git failure lands in this one card at the top of the panel, never in
   a global toast. A pull that can't fast-forward turns into an offer to
   rebase or merge with the selected harness; the run, and whatever it left
   behind (done, still diverged, or stopped midway on a conflict), stay in
   the same card so the next step is always right there. */

let syncJob = 0; // id of the agent rebase/merge job while one runs

function initNotice() {
  $('#git-notice-close')?.addEventListener('click', clearGitNotice);
  const n = $('#git-notice');
  const harnessSelect = $('#git-sync-harness');
  const modelSelect = $('#git-sync-model');
  if (n && harnessSelect && modelSelect) registerAgentPicker({ el: n, harnessSelect, modelSelect });
  // Stage ticks in the file tree report their failures here too.
  on('git:error', ({ title, message }) => showGitError(title, message));
  on('pr:resolve', resolvePRBase);
  on('pr:force-push', confirmForcePush);
  on('pr:pull', doPull);
  on('forge:connected', updateGitPanelVisibility);
}

/**
 * @param {{tone?: string, title: string, html?: string, picker?: boolean,
 *   actions?: {label: string, run: () => void, primary?: boolean, title?: string}[],
 *   log?: string, closable?: boolean}} opts
 */
function notice({ tone = 'err', title, html = '', picker = false, actions = [], log = '', closable = true }) {
  const n = $('#git-notice');
  if (!n) return;
  n.dataset.tone = tone;
  $('#git-notice-title').textContent = title;
  $('#git-notice-text').innerHTML = html;
  $('#git-notice-agent').hidden = !picker;
  $('#git-notice-close').hidden = !closable;
  $('#git-notice-actions').replaceChildren(...actions.map(a => {
    const b = document.createElement('button');
    b.type = 'button';
    b.textContent = a.label;
    if (a.primary) b.className = 'primary';
    if (a.title) b.title = a.title;
    b.addEventListener('click', () => {
      for (const x of $('#git-notice-actions').children) x.disabled = true;
      a.run();
    });
    return b;
  }));
  const details = $('#git-notice-log');
  if (details) details.open = false;
  setNoticeLog(log);
  n.hidden = false;
  const p = panel();
  if (p?.classList.contains('collapsed')) {
    p.classList.remove('collapsed');
    layout(); render();
  }
  n.scrollIntoView({ block: 'nearest' });
}

function setNoticeLog(text) {
  const details = $('#git-notice-log');
  if (!details) return;
  const pre = details.querySelector('pre');
  const atEnd = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 4;
  details.hidden = !text;
  pre.textContent = text || '';
  if (atEnd) pre.scrollTop = pre.scrollHeight;
}

export function clearGitNotice() {
  if (syncJob) return; // a running rebase/merge keeps its card until it ends
  const n = $('#git-notice');
  if (n) n.hidden = true;
}

// showGitError reports a failed git action in the panel. message is the
// server's own error text when there is one.
export function showGitError(title, message = '') {
  if (syncJob) return; // don't bury the running rebase/merge; its outcome follows
  notice({ tone: 'err', title, html: esc(message) });
}

const plural = (n, word) => n + ' ' + word + (n === 1 ? '' : 's');
const code = s => '<code>' + esc(s || '') + '</code>';
const hasHarness = () => (S.meta?.agents || []).some(h => h.installed);

// offerSync is the card a diverged pull leaves: what diverged, and the two
// ways the harness can bring the branch up to date.
function offerSync(st, err = '') {
  const up = st.upstream || 'upstream';
  const pr = st.pr > 0;
  const title = pr ? `PR #${st.pr} is behind ${up}` : "Can't fast-forward";
  let html = pr
    ? `${code(st.branch)} is missing ${plural(st.behind || 0, 'commit')} from ${code(up)}; the PR has ${plural(st.ahead || 0, 'commit')} of its own.`
    : `${code(st.branch)} and ${code(up)} have diverged: ${plural(st.ahead || 0, 'local commit')} not upstream, ${st.behind || 0} incoming.`;
  if (!hasHarness()) {
    notice({
      tone: 'warn', title,
      html: html + `<span class="hint">Install a coding harness to rebase or merge from here, or run ${code(pr ? 'git rebase ' + up : 'git pull --rebase')} in a terminal.</span>`,
    });
    return;
  }
  html += pr
    ? `<span class="hint">Rebase replays the PR's commits on top of ${esc(up)} for a straight history, then needs a force-push to update the PR. Merge brings ${esc(up)} in with a merge commit and a normal push. The harness resolves any conflicts; nothing is pushed until you say so.</span>`
    : `<span class="hint">Rebase replays your commits on top of ${esc(up)} for a straight history; merge joins the two with a merge commit. The harness resolves any conflicts. Nothing is pushed.</span>`;
  if (err) html += `<span class="hint" style="color:var(--err)">${esc(err)}</span>`;
  notice({
    tone: 'warn', title, html, picker: true,
    actions: [
      { label: 'Rebase with AI', primary: true, title: `Have the harness run git rebase ${up} and resolve conflicts`, run: () => startSync('rebase', st) },
      { label: 'Merge with AI', title: `Have the harness run git merge ${up} and resolve conflicts`, run: () => startSync('merge', st) },
    ],
  });
}

// offerUnfinished is the card for a rebase or merge left half-done -- by a
// harness that stopped on a conflict, or started in a terminal.
function offerUnfinished(st, log = '', err = '') {
  const op = st.inProgress;
  let html = `${code(st.branch)} is in the middle of a ${op}${st.upstream ? ' onto ' + code(st.upstream) : ''}, most likely stopped on a conflict.` +
    `<span class="hint">Abort to put the branch back exactly as it was before the ${op}, or have the harness resolve the conflicts and finish it.</span>`;
  if (err) html += `<span class="hint" style="color:var(--err)">${esc(err)}</span>`;
  const actions = [{ label: `Abort ${op}`, run: abortSync }];
  if (hasHarness() && st.upstream) actions.unshift({ label: 'Finish with AI', primary: true, run: () => startSync(op, st) });
  notice({ tone: 'warn', title: `${op === 'rebase' ? 'Rebase' : 'Merge'} not finished`, html, picker: hasHarness(), actions, log, closable: true });
}

async function startSync(strategy, st) {
  let job;
  try {
    job = await apiPostJson('/api/git/sync-agent', { strategy });
  } catch (e) {
    // Re-offer with the reason (no harness picked, tree got dirty, ...).
    if (st.inProgress) offerUnfinished(st, '', e.message);
    else offerSync(st, e.message);
    return;
  }
  syncJob = job.id;
  setSyncButtons();
  emitSync();
  const verb = strategy === 'rebase' ? 'Rebasing' : 'Merging';
  const up = st.upstream || 'upstream';
  notice({
    tone: 'busy', title: `${verb} with ${job.harness}...`, closable: false,
    html: `${code(st.branch)} onto ${code(up)}. The harness resolves conflicts as it goes; nothing is pushed.`,
    actions: [{ label: 'Cancel', title: 'Stop the harness; anything half-done can be aborted next', run: () => apiPostJson('/api/agent/cancel', { id: job.id }).catch(() => {}) }],
  });
  const poll = async () => {
    let j;
    try {
      j = await api('/api/agent/job?id=' + job.id);
    } catch (e) {
      j = { running: false, error: e.message };
    }
    if (j.running) {
      $('#git-notice-title').textContent = `${verb} with ${job.harness}... (${Math.round((j.ms || 0) / 1000)}s)`;
      setNoticeLog(j.stdout || j.log || '');
      setTimeout(poll, 800);
      return;
    }
    await finishSync(strategy, j);
  };
  setTimeout(poll, 500);
}

// finishSync reads where the branch actually ended up rather than trusting
// the harness's exit status: a clean exit can still leave a conflict behind,
// and a failed one can still have finished the rebase.
async function finishSync(strategy, j) {
  syncJob = 0;
  setSyncButtons();
  const log = (j.stdout || j.log || '').trim();
  let st = {};
  try {
    st = await api('/api/git/sync-state');
  } catch {}
  syncInProgress = st.inProgress || '';
  rewritten = !!st.rewritten;
  emitSync();
  await afterHistoryChange();
  const done = strategy === 'rebase' ? 'Rebased' : 'Merged';
  if (st.inProgress) {
    offerUnfinished(st, log);
  } else if (st.behind > 0) {
    const why = j.error === 'cancelled' ? 'Cancelled.' : j.error ? (j.harness || 'harness') + ': ' + j.error : `The harness didn't finish the ${strategy}; its output below says why.`;
    notice({
      tone: 'err', title: `${strategy === 'rebase' ? 'Rebase' : 'Merge'} didn't complete`, log,
      html: `${esc(why)} ${code(st.branch)} is unchanged: still ${plural(st.ahead || 0, 'local commit')} and ${st.behind} incoming.`,
      actions: [{ label: 'Try again', run: () => offerSync(st) }],
    });
  } else if (st.pr > 0) {
    const pr = S.meta?.pr || {};
    notice({
      tone: 'ok', title: `${done} onto ${st.upstream}`, log,
      html: st.rewritten
        ? `The PR's commits now sit on top of ${code(st.upstream)}. Review the result; force-pushing replaces the commits on ${code(pr.head)}.` +
          `<span class="hint">Approve and Merge wait until it's pushed, so they never act on the old commits.</span>`
        : `${code(pr.head)} now contains ${code(st.upstream)}. Review the result, then push to update the PR.`,
      actions: st.rewritten
        ? [{ label: `Force-push to PR #${st.pr}`, primary: true, title: `git push --force-with-lease to ${pr.head}`, run: () => doPush(true) }]
        : [{ label: 'Push to PR', primary: true, run: () => doPush() }],
    });
  } else {
    const ahead = st.ahead || 0;
    notice({
      tone: 'ok', title: `${done} onto ${st.upstream || 'upstream'}`, log,
      html: ahead
        ? `${code(st.branch)} is ${plural(ahead, 'commit')} ahead and up to date. Review the result, then push when ready.`
        : `${code(st.branch)} is up to date.`,
      actions: ahead ? [{ label: `Push (${ahead})`, primary: true, run: doPush }] : [],
    });
  }
}

async function abortSync() {
  try {
    await apiPostJson('/api/git/abort', {});
  } catch (e) {
    showGitError('Abort failed', e.message);
    return;
  }
  syncInProgress = '';
  emitSync();
  await afterHistoryChange();
  notice({
    tone: 'ok', title: 'Aborted',
    html: S.meta?.pr ? 'The checkout is back where it was.' : 'The branch is back where it was. Pull again to choose rebase or merge.',
  });
}

// Pull and Push stay disabled while the harness rewrites history under them.
function setSyncButtons() {
  const pull = $('#git-pull');
  if (pull) pull.disabled = !!syncJob;
  const push = $('#git-push');
  if (push && syncJob) push.disabled = true;
  else if (push) triggerRefresh(); // the next status payload re-derives Push's state
}

// A rebase or merge left half-done (a closed tab mid-run, a terminal) is
// surfaced on load rather than discovered later as a confusing dirty tree.
async function checkUnfinishedSync() {
  try {
    const st = await api('/api/git/sync-state');
    syncInProgress = st.inProgress || '';
    rewritten = !!st.rewritten;
    emitSync();
    if (st.inProgress) offerUnfinished(st);
  } catch {}
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
    showGitError('Could not stage changes', e.message);
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
    showGitError('Could not write a commit message', e.message);
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
      showGitError('Could not write a commit message', e.message);
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
      showGitError('Could not write a commit message', (j.harness || 'agent') + ': ' + j.error);
      return;
    }
    const text = cleanCommitMessage(j.stdout || j.log || '');
    if (!text) {
      ui.reset();
      showGitError('Could not write a commit message', 'The harness returned an empty message.');
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
    showGitError('Could not write a commit message', e.message);
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
    clearGitNotice();
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
    showGitError('Commit failed', e.message);
  } finally {
    if (!btn?._flashTimer) resetGenerateBtn(btn);
    else btn.disabled = false;
  }
}

// Harnesses sometimes wrap output in a markdown code fence despite being
// asked not to; strip that and surrounding whitespace before using it.
function cleanCommitMessage(text) {
  let t = (text || '').trim();
  const fence = t.match(/```[a-zA-Z0-9_-]*\r?\n([\s\S]*?)\r?\n```/) || t.match(/^```([\s\S]*?)```$/);
  if (fence) t = fence[1].trim();
  if ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'"))) {
    t = t.slice(1, -1).trim();
  }
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
  if (S.meta?.pr?.url) {
    currentCommitsUrl = `${S.meta.pr.url.replace(/\/+$/, '')}/commits`;
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
    showGitError('Could not load commits', err.message);
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
    showGitError('Binary file, no diff to show');
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
    else showGitError('This commit changes no files');
  } catch (e) {
    showGitError('Could not load commit', e.message);
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
