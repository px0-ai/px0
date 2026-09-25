// web/src/pr.js
// GitHub PR review: shown only when this process was launched as `px0 pr ...`
// (S.meta.pr, set by main.go/pr.go). A persistent bar above the tabs shows
// the PR and hosts Approve/Request Changes/Comment; selecting a diff line and
// pressing Alt+R (or the footer/context-menu action) drafts an inline review
// comment. Everything here talks to /api/pr/*; nothing is stored client-side
// beyond what's needed to repaint -- a page refresh re-fetches the server's
// in-memory draft list (pr.go's prSession), which is the only source of truth.
import { $, S, doc_, esc, api, apiPostJson, keyLabel, withKeys } from './state.js';
import { showToast } from './ui.js';
import { setReviewHandler, SEL_MENU_ITEMS } from './selbar.js';
import { diffview, setPRSyncHandler } from './diff.js';
import { reloadWorkspace } from './agent.js';
import { openFile } from './tabs.js';
import { layout, render } from './renderer.js';
import { openSettings } from './settings.js';

let meta = null;      // this session's PR info: {number, title, base, head, writeAccess, readOnly}
let comments = [];    // draft comments known to the server
let issueComments = [];   // top-level PR conversation comments, already posted (fetched read-only)
let reviewComments = [];  // inline diff-line comments, already posted (fetched read-only) -- may include replies

const prBar = () => $('#pr-bar');
const list = () => $('#pr-comment-list');

export function initPR() {
  if (!S.meta || !S.meta.pr) return;
  meta = S.meta.pr;
  document.body.classList.add('pr-mode');

  if (!SEL_MENU_ITEMS.some(item => item.sel === 'review-comment')) {
    SEL_MENU_ITEMS.push({ sel: 'review-comment', label: 'Add Review Comment', keys: 'Alt+R' });
  }
  setReviewHandler(openCommentComposer);
  setPRSyncHandler(renderMarkersForActiveDoc);
  injectFooterButton();
  wireBarButtons();
  wireCommentsPanel();
  renderBar();
  refreshComments();
  refreshExistingComments();
}

// Re-fetches PR metadata and comments after an external change to the
// checkout -- specifically, the sidebar git panel's Pull fast-forwarding
// onto a new PR head -- so the bar, diff-base warning, and comments reflect
// the new state instead of the one captured at session start.
export async function refreshPRMeta() {
  if (!meta) return;
  try {
    const j = await api('/api/pr/meta');
    meta = { ...meta, ...j };
    renderBar();
  } catch {
    // Best-effort.
  }
  await refreshExistingComments();
  await refreshComments();
}

function fmtTime(iso) {
  if (!iso) return '';
  try {
    return new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
  } catch {
    return iso;
  }
}

// Comments already posted on GitHub -- top-level conversation and inline diff
// comments -- fetched live (never cached) so another reviewer's activity
// shows up on the next open of the panel or diff.
async function refreshExistingComments() {
  try {
    const j = await api('/api/pr/existing-comments');
    issueComments = j.issueComments || [];
    reviewComments = j.reviewComments || [];
    renderCommentsPanel();
    renderMarkersForActiveDoc();
  } catch {
    // Best-effort: panel/gutter just stay empty on a transient failure.
  }
}

async function refreshComments() {
  try {
    const j = await api('/api/pr/comments');
    comments = j.comments || [];
    renderBar();
    renderCommentsPanel();
    renderMarkersForActiveDoc();
  } catch {
    // Read-only or a transient error: the bar still shows PR metadata.
  }
}

function renderBar() {
  const b = prBar();
  if (!b || !meta) return;
  b.hidden = false;
  $('#pr-badge').textContent = '#' + meta.number;
  const link = $('#pr-link');
  if (link) link.href = meta.url || '#';
  const mb = $('#pr-merged-badge');
  if (mb) mb.hidden = !meta.merged;
  $('#pr-title').textContent = meta.title;
  $('#pr-title').title = meta.title;
  $('#pr-refs').textContent = meta.base + ' ← ' + meta.head;
  $('#pr-draft-count').textContent = comments.length
    ? (comments.length + (comments.length === 1 ? ' draft comment' : ' draft comments'))
    : '';
  const ro = $('#pr-readonly-note');
  if (ro) ro.hidden = !meta.readOnly;
  const dw = $('#pr-diff-warning');
  if (dw) {
    dw.hidden = !meta.diffBaseWarning;
    if (meta.diffBaseWarning) dw.title = meta.diffBaseWarning;
  }
  const batchBtn = $('#pr-batch-apply');
  if (batchBtn) {
    const hasApplicable = comments.some(c => c.path && c.line && c.body?.trim());
    batchBtn.hidden = !hasApplicable;
  }
  const reqBtn = $('#pr-submit-request-changes');
  const appBtn = $('#pr-submit-approve');
  if (reqBtn) reqBtn.hidden = !meta.writeAccess;
  if (appBtn) appBtn.hidden = !meta.writeAccess;
  const cmtBtn = $('#pr-submit-comment');
  if (cmtBtn) {
    cmtBtn.disabled = false;
    cmtBtn.title = meta.readOnly
      ? 'No GitHub token configured -- click to connect and submit'
      : 'Submit review with drafts, without approval or change requests';
  }
  const composeEl = $('#pr-issue-compose');
  if (composeEl) composeEl.hidden = false;
}

export function nudgeGitHubToken() {
  showToast('!', 'No GitHub token configured: set GITHUB_TOKEN, set GH_TOKEN, or run `gh auth login` -- or connect in Settings.', 5000);
  openSettings('ui', 'GitHub', 'github.token');
}

function wireBarButtons() {
  $('#pr-batch-apply')?.addEventListener('click', batchApplyComments);
  $('#pr-submit-comment')?.addEventListener('click', () => submitReview('COMMENT'));
  $('#pr-submit-request-changes')?.addEventListener('click', () => submitReview('REQUEST_CHANGES'));
  $('#pr-submit-approve')?.addEventListener('click', () => submitReview('APPROVE'));
  $('#pr-issue-compose-send')?.addEventListener('click', sendNewIssueComment);
  $('#pr-readonly-note')?.addEventListener('click', nudgeGitHubToken);
}

async function sendNewIssueComment() {
  if (meta?.readOnly) {
    nudgeGitHubToken();
    return;
  }
  const ta = $('#pr-issue-compose-body');
  if (!ta) return;
  const body = ta.value.trim();
  if (!body) return;
  const btn = $('#pr-issue-compose-send');
  if (btn) btn.disabled = true;
  try {
    const c = await apiPostJson('/api/pr/comments/issue', { body });
    issueComments.push(c);
    ta.value = '';
    expandedKeys.add('issue:' + c.id);
    renderCommentsPanel();
    showToast('✓', 'Comment posted');
  } catch (e) {
    showToast('!', e.message || 'Could not post comment');
  } finally {
    if (btn) btn.disabled = false;
  }
}

// Only the label swaps text; the icon markup (SVG + .footer-btn-label span,
// see index.html) stays put so the button never reverts to a plain-text look.
function setBatchBtnLabel(btn, text) {
  const label = btn?.querySelector('.footer-btn-label');
  if (label) label.textContent = text; else if (btn) btn.textContent = text;
}

async function batchApplyComments() {
  const applicable = comments.filter(c => c.path && c.line && c.body?.trim());
  if (!applicable.length) {
    showToast('!', 'No draft comments with line locations to apply');
    return;
  }
  const btn = $('#pr-batch-apply');
  if (btn) {
    btn.disabled = true;
    setBatchBtnLabel(btn, 'Applying...');
  }
  const edits = applicable.map(c => ({
    path: c.path,
    l1: c.line,
    l2: c.line,
    instruction: c.body.trim(),
  }));
  try {
    const job = await apiPostJson('/api/agent/batch', { edits });
    showToast('AI', `Batch applying ${edits.length} comments with ${job.harness || 'agent'}...`);
    pollPRBatch(job.id, applicable.length);
  } catch (e) {
    if (btn) {
      btn.disabled = false;
      setBatchBtnLabel(btn, 'Batch Apply');
    }
    showToast('!', e.message || 'Could not dispatch batch edit');
  }
}

async function pollPRBatch(id, count) {
  const btn = $('#pr-batch-apply');
  const poll = async () => {
    try {
      const j = await api('/api/agent/job?id=' + id);
      if (j.running) {
        const sec = Math.round((j.ms || 0) / 1000);
        if (btn) setBatchBtnLabel(btn, `Applying... (${sec}s)`);
        setTimeout(poll, 600);
        return;
      }
      if (btn) {
        btn.disabled = false;
        setBatchBtnLabel(btn, 'Batch Apply');
      }
      if (j.error) {
        showToast('!', `Agent error: ${j.error}`);
        if (j.changed?.length) await reloadWorkspace(null);
        return;
      }
      showToast('✓', `Batch applied ${count} comments!`);
      await reloadWorkspace(null);
      await refreshComments();
    } catch (e) {
      if (btn) {
        btn.disabled = false;
        setBatchBtnLabel(btn, 'Batch Apply');
      }
      showToast('!', e.message || 'Batch failed');
    }
  };
  setTimeout(poll, 400);
}

async function submitReview(event) {
  if (meta?.readOnly) {
    nudgeGitHubToken();
    return;
  }
  const bodyEl = $('#pr-review-body');
  const body = bodyEl ? bodyEl.value.trim() : '';
  if (event === 'REQUEST_CHANGES' && !body && !comments.length) {
    showToast('!', 'Add a comment or review body before requesting changes');
    return;
  }
  try {
    await apiPostJson('/api/pr/submit', { event, body });
    comments = [];
    if (bodyEl) bodyEl.value = '';
    closeAllComposers();
    renderBar();
    renderCommentsPanel();
    renderMarkersForActiveDoc();
    showToast('✓', event === 'APPROVE' ? 'Review approved'
      : event === 'REQUEST_CHANGES' ? 'Changes requested'
      : 'Review comment submitted');
  } catch (e) {
    showToast('!', e.message || 'Could not submit review');
  }
}

/* ---------- locating a diff row from a path/side/line ---------- */

// Shared by the composer (to pin itself beside its line) and the gutter
// markers below. Only searches the visible diff, and only when it's showing
// the same file -- a composer for a file that isn't on screen has nothing to
// find, which callers treat as "dock it instead" rather than an error.
function findDiffRowEl(path, side, line) {
  if (!diffview || diffview.hidden) return null;
  const d = doc_();
  if (!d || d.path !== path) return null;
  // Review comments are only ever posted against the PR's own diff (see
  // diff.js's reviewable flag), so a "Your changes" row sharing the same
  // line number must never be matched here.
  for (const el of diffview.querySelectorAll('[data-l]:not([data-reviewable="0"]), [data-old-l]:not([data-reviewable="0"])')) {
    const isOldOnly = el.dataset.oldL !== undefined && el.dataset.l === undefined;
    const elSide = isOldOnly ? 'LEFT' : 'RIGHT';
    const elLine = isOldOnly ? +el.dataset.oldL : +el.dataset.l;
    if (elSide === side && elLine === line) return el;
  }
  return null;
}

function flashDiffRow(el) {
  el.classList.add('pr-line-flash');
  setTimeout(() => el.classList.remove('pr-line-flash'), 1100);
}

/* ---------- inline draft comment composer ----------
   Each open composer is pinned beside the diff row it's on (recomputed as
   the diff scrolls) instead of sitting in one fixed spot -- so it never
   drifts from the line it's actually commenting on, and opening one never
   shifts the tabs or code beneath it (the list is an absolute overlay; see
   .pr-comment-list in style.css). A composer for a file that isn't the one
   on screen has no row to pin to, so it docks in the bottom-right corner
   until you jump back to it (click its ref, or the file's own tab). */

let seq = 0;
const openBoxes = new Map(); // id -> { box, path, side, line }
let trackingBound = false;

function ensureTracking() {
  if (trackingBound) return;
  trackingBound = true;
  let scheduled = false;
  const schedule = () => {
    if (scheduled) return;
    scheduled = true;
    requestAnimationFrame(() => { scheduled = false; repositionAll(); });
  };
  diffview?.addEventListener('scroll', schedule);
  addEventListener('resize', schedule);
}

function positionBox(entry) {
  const { box, path, side, line } = entry;
  const host = $('#editor');
  const target = findDiffRowEl(path, side, line);
  if (!host) return;
  if (!target) {
    box.classList.add('docked');
    box.style.top = '';
    box.style.left = '';
    // Stack docked boxes bottom-up so several at once don't overlap.
    const docked = [...openBoxes.values()].filter(e => e.box.classList.contains('docked'));
    const i = Math.max(0, docked.indexOf(entry));
    box.style.right = '16px';
    box.style.bottom = (16 + i * (box.offsetHeight + 8)) + 'px';
    return;
  }
  box.classList.remove('docked');
  box.style.right = '';
  box.style.bottom = '';
  const hostRect = host.getBoundingClientRect();
  const tRect = target.getBoundingClientRect();
  const left = Math.min(hostRect.width - box.offsetWidth - 16, Math.max(16, tRect.left - hostRect.left));
  let top = tRect.bottom - hostRect.top + 4;
  top = Math.max(4, Math.min(top, hostRect.height - box.offsetHeight - 4));
  box.style.left = left + 'px';
  box.style.top = top + 'px';
}

function repositionAll() {
  for (const entry of openBoxes.values()) positionBox(entry);
}

// Scrolls/switches back to the composer's line and calls it out, whether
// it's pinned on screen already or you've since navigated elsewhere.
async function revealComposer(entry) {
  const { path, side, line } = entry;
  if (!findDiffRowEl(path, side, line)) {
    await openFile(path, { line });
  }
  positionBox(entry);
  const target = findDiffRowEl(path, side, line);
  if (target) { target.scrollIntoView({ block: 'center', behavior: 'smooth' }); flashDiffRow(target); }
}

export function openCommentComposer(info) {
  if (!meta) return;
  const id = 'prc' + (++seq);
  const box = document.createElement('div');
  box.className = 'agent-box entering';
  box.dataset.id = id;
  const side = info.side || 'RIGHT';
  const line = side === 'LEFT' ? (info.delL1 || info.l1) : info.l1;
  const lineEnd = side === 'LEFT' ? (info.delL2 || info.l2) : info.l2;
  const ref = info.path + ':' + (line === lineEnd ? line : line + '-' + lineEnd) + (side === 'LEFT' ? ' (base)' : '');
  const modEnter = keyLabel('Mod+Enter');
  box.innerHTML =
    '<div class="agent-head"><span class="sel-chip">Review Comment</span>' +
    '<span class="agent-ref" role="button" tabindex="0" title="Jump to this line">' + esc(ref) + '</span>' +
    '<span class="grow"></span><button class="agent-close" title="Close (Esc)">✕</button></div>' +
    '<div class="agent-compose">' +
    '<textarea class="agent-input" rows="3" spellcheck="false" autocomplete="off" placeholder="Leave a comment on this line... (' + esc(modEnter) + ' to add)"></textarea>' +
    '<div class="agent-err" hidden></div>' +
    '<div class="agent-foot"><span class="agent-hint">' + esc(modEnter) + ' to add, Esc to cancel</span>' +
    '<button class="agent-send" title="Add comment (' + esc(modEnter) + ')">Add Comment</button></div></div>';

  const entry = { box, path: info.path, side, line };
  openBoxes.set(id, entry);
  ensureTracking();

  list().hidden = false;
  list().append(box);
  positionBox(entry);
  const target = findDiffRowEl(info.path, side, line);
  if (target) { target.scrollIntoView({ block: 'center', behavior: 'smooth' }); flashDiffRow(target); }
  const ta = /** @type {HTMLTextAreaElement|null} */ (box.querySelector('.agent-input'));
  if (ta) ta.focus();

  box.querySelector('.agent-ref')?.addEventListener('click', () => revealComposer(entry));

  const close = () => {
    openBoxes.delete(id);
    box.remove();
    if (!list().children.length) list().hidden = true;
  };
  box.querySelector('.agent-close')?.addEventListener('click', close);

  const send = async () => {
    if (!ta) return;
    const body = ta.value.trim();
    if (!body) return;
    const errEl = /** @type {HTMLElement|null} */ (box.querySelector('.agent-err'));
    if (errEl) errEl.hidden = true;
    try {
      const c = await apiPostJson('/api/pr/comments', { path: info.path, line, side, body });
      comments.push(c);
      expandedKeys.add('thread:' + threadKey(info.path, side, line));
      close();
      renderBar();
      renderCommentsPanel();
      renderMarkersForActiveDoc();
    } catch (e) {
      if (errEl) {
        errEl.hidden = false;
        errEl.textContent = e.message || 'Could not add comment';
      }
    }
  };
  box.querySelector('.agent-send')?.addEventListener('click', send);
  ta?.addEventListener('keydown', (/** @type {KeyboardEvent} */ e) => {
    if (e.key === 'Escape') { e.preventDefault(); close(); }
    else if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); send(); }
  });
}

function closeAllComposers() {
  const l = list();
  openBoxes.clear();
  if (!l) return;
  l.replaceChildren();
  l.hidden = true;
}

/* ---------- gutter markers on the active diff ---------- */

const COMMENT_ICON = '<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>';

function renderMarkersForActiveDoc() {
  if (!meta) return;
  // Runs whether or not a diff is on screen right now, so a composer whose
  // file just scrolled out of (or into) view re-docks or re-pins itself.
  repositionAll();
  if (!diffview || diffview.hidden) return;
  const d = doc_();
  if (!d) return;
  const draftsByKey = new Map();
  for (const c of comments) {
    if (c.path !== d.path) continue;
    const key = (c.side || 'RIGHT') + ':' + c.line;
    if (!draftsByKey.has(key)) draftsByKey.set(key, []);
    draftsByKey.get(key).push(c);
  }
  // Existing (already-posted) review comments: replies carry the same
  // path/line/side as their thread root, so grouping by key alone already
  // gathers a whole thread together.
  const existingByKey = new Map();
  for (const c of reviewComments) {
    if (c.path !== d.path) continue;
    const key = (c.side || 'RIGHT') + ':' + c.line;
    if (!existingByKey.has(key)) existingByKey.set(key, []);
    existingByKey.get(key).push(c);
  }
  for (const el of diffview.querySelectorAll('.pr-comment-mark')) el.remove();
  for (const el of diffview.querySelectorAll('[data-l]:not([data-reviewable="0"]), [data-old-l]:not([data-reviewable="0"])')) {
    const isOldOnly = el.dataset.oldL !== undefined && el.dataset.l === undefined;
    const side = isOldOnly ? 'LEFT' : 'RIGHT';
    const line = isOldOnly ? +el.dataset.oldL : +el.dataset.l;
    const key = side + ':' + line;
    const drafts = draftsByKey.get(key);
    const existing = existingByKey.get(key);
    el.classList.toggle('pr-has-comment', !!drafts || !!existing);
    if (!drafts && !existing) continue;
    const badge = document.createElement('span');
    badge.className = 'pr-comment-mark';
    const count = (existing?.length || 0) + (drafts?.length || 0);
    badge.title = 'View ' + count + ' comment' + (count === 1 ? '' : 's');
    badge.innerHTML = COMMENT_ICON;
    badge.addEventListener('click', e => {
      e.stopPropagation();
      revealThreadInPanel(threadKey(d.path, side, line));
    });
    el.querySelector('.diff-code')?.before(badge);
  }
}

/* ---------- bottom panel: existing comments + drafts, always open ---------- */

function threadKey(path, side, line) { return path + '|' + (side || 'RIGHT') + ':' + line; }

// Which accordion items ("issue:<id>" / "thread:<threadKey>") are expanded.
// Persists across re-renders within the session so replying, adding a draft,
// or a background refresh never silently collapses something you opened.
const expandedKeys = new Set();

function toggleAccordion(item) {
  if (!item) return;
  const key = item.dataset.acc;
  const open = !item.classList.contains('open');
  item.classList.toggle('open', open);
  if (!key) return;
  if (open) expandedKeys.add(key); else expandedKeys.delete(key);
}

function wireCommentsPanel() {
  const panel = $('#pr-comments-panel');
  if (panel) {
    panel.hidden = false;
    panel.classList.add('collapsed');
  }

  const toggle = () => {
    panel?.classList.toggle('collapsed');
    layout(); render();
  };

  $('#pr-comments-collapse')?.addEventListener('click', e => {
    e.stopPropagation();
    toggle();
  });

  $('.pr-comments-panel-head')?.addEventListener('click', e => {
    if (e.target.closest('#pr-comments-collapse')) return;
    toggle();
  });

  const rz = $('#pr-comments-resizer');
  if (rz && panel) {
    let dragging = false;
    rz.addEventListener('mousedown', e => {
      dragging = true;
      rz.classList.add('drag');
      panel.classList.remove('collapsed');
      e.preventDefault();
    });
    addEventListener('mousemove', e => {
      if (!dragging) return;
      const bottom = panel.getBoundingClientRect().bottom;
      const h = Math.max(80, Math.min(window.innerHeight * 0.8, bottom - e.clientY));
      panel.style.height = h + 'px';
      layout(); render();
    });
    addEventListener('mouseup', () => {
      if (!dragging) return;
      dragging = false;
      rz.classList.remove('drag');
      layout(); render();
    });
  }

  $('#pr-comments-list')?.addEventListener('click', e => {
    const replyBtn = e.target.closest('.pr-issue-comment-reply-btn');
    if (replyBtn) {
      const ta = $('#pr-issue-compose-body');
      if (ta) {
        const prefix = replyBtn.dataset.author ? '@' + replyBtn.dataset.author + ' ' : '';
        if (!ta.value.startsWith(prefix)) ta.value = prefix + ta.value;
        ta.focus();
        ta.setSelectionRange(ta.value.length, ta.value.length);
      }
      return;
    }
    const loc = e.target.closest('.pr-comment-loc');
    if (loc) {
      openFile(loc.dataset.path, { line: +loc.dataset.line });
      return;
    }
    const delBtn = e.target.closest('.pr-comment-delete-btn');
    if (delBtn) {
      deleteDraft(+delBtn.dataset.draftId);
      return;
    }
    const sendBtn = e.target.closest('.reply-send');
    if (sendBtn) {
      const row = sendBtn.closest('.pr-comment-reply-row');
      sendThreadReply(row);
      return;
    }
    const head = e.target.closest('.acc-head');
    if (head) toggleAccordion(head.closest('.acc-item'));
  });

  $('#pr-comments-list')?.addEventListener('keydown', e => {
    if (!e.target.closest('.reply-input')) return;
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      sendThreadReply(e.target.closest('.pr-comment-reply-row'));
    }
  });
}

async function sendThreadReply(row) {
  if (!row) return;
  if (meta?.readOnly) {
    nudgeGitHubToken();
    return;
  }
  const ta = row.querySelector('.reply-input');
  const body = ta?.value.trim();
  if (!body) return;
  const commentId = +row.dataset.replyTo;
  const btn = row.querySelector('.reply-send');
  if (btn) btn.disabled = true;
  try {
    const c = await apiPostJson('/api/pr/comments/review-reply', { commentId, body });
    reviewComments.push(c);
    renderCommentsPanel();
    renderMarkersForActiveDoc();
    showToast('✓', 'Reply posted');
  } catch (e) {
    showToast('!', e.message || 'Could not reply');
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function deleteDraft(id) {
  try {
    await apiPostJson('/api/pr/comments/delete?id=' + id, {});
    comments = comments.filter(c => c.id !== id);
    renderBar();
    renderCommentsPanel();
    renderMarkersForActiveDoc();
  } catch (e) {
    showToast('!', e.message || 'Could not delete draft');
  }
}

// Expands the panel if collapsed, opens the thread's accordion item, scrolls
// to it, and briefly flashes it -- the gutter badge's click target now lands
// here instead of opening a separate floating box.
function revealThreadInPanel(key) {
  const panel = $('#pr-comments-panel');
  panel?.classList.remove('collapsed');
  layout(); render();
  const el = $('#pr-comments-list')?.querySelector('[data-thread-key="' + CSS.escape(key) + '"]');
  if (!el) return;
  if (!el.classList.contains('open')) toggleAccordion(el);
  el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  el.classList.add('flash');
  setTimeout(() => el.classList.remove('flash'), 1200);
}

// Every comment/thread is an accordion item: a clickable .acc-head (author,
// time, a one-line ellipsized preview of the body) and an .acc-body that's
// only in the flow when the item carries the 'open' class -- so a scan of
// the panel is just headers until you click one open to read and reply.
function issueCommentCardHtml(c) {
  const key = 'issue:' + c.id;
  const open = expandedKeys.has(key);
  return '<div class="pr-comment-card acc-item' + (open ? ' open' : '') + '" data-acc="' + esc(key) + '">' +
    '<div class="acc-head">' +
      '<span class="acc-chevron">&#8250;</span>' +
      '<span class="pr-issue-comment-author">' + esc(c.author || 'unknown') + '</span>' +
      '<span class="pr-issue-comment-time">' + esc(fmtTime(c.createdAt)) + '</span>' +
      '<span class="acc-preview">' + esc(c.body) + '</span>' +
    '</div>' +
    '<div class="acc-body">' +
      '<div class="pr-issue-comment-body">' + esc(c.body) + '</div>' +
      '<button class="pr-issue-comment-reply-btn" data-author="' + esc(c.author || '') + '">Reply</button>' +
    '</div>' +
  '</div>';
}

function reviewCommentCardHtml(c) {
  return '<div class="pr-comment-card' + (c.inReplyTo ? ' reply' : '') + '">' +
    '<div class="pr-issue-comment-head">' +
      '<span class="pr-issue-comment-author">' + esc(c.author || 'unknown') + '</span>' +
      '<span class="pr-issue-comment-time">' + esc(fmtTime(c.createdAt)) + '</span>' +
    '</div>' +
    '<div class="pr-issue-comment-body">' + esc(c.body) + '</div>' +
  '</div>';
}

function draftCardHtml(c) {
  return '<div class="pr-comment-card draft">' +
    '<div class="pr-issue-comment-head"><span class="pr-issue-comment-author">You (draft, not yet submitted)</span></div>' +
    '<div class="pr-issue-comment-body">' + esc(c.body) + '</div>' +
    '<div class="pr-comment-card-actions">' +
      '<button class="pr-comment-delete-btn" data-draft-id="' + c.id + '">Delete draft</button>' +
    '</div>' +
  '</div>';
}

function threadHtml(path, t, key) {
  const sortedExisting = [...t.existing].sort((a, b) => (a.createdAt || '').localeCompare(b.createdAt || ''));
  const root = sortedExisting.find(c => !c.inReplyTo) || sortedExisting[0];
  const loc = path + ':' + t.line + (t.side === 'LEFT' ? ' (base)' : '');
  const itemsHtml = sortedExisting.map(reviewCommentCardHtml).join('');
  const draftsHtml = t.drafts.map(draftCardHtml).join('');
  const canReply = !!root;
  const replyRow = canReply
    ? '<div class="pr-comment-reply-row" data-reply-to="' + root.id + '">' +
      '<textarea class="pr-review-body reply-input" rows="1" spellcheck="false" autocomplete="off" placeholder="Reply..."></textarea>' +
      '<button class="footer-btn reply-send">Reply</button>' +
      '</div>'
    : '';
  const accKey = 'thread:' + key;
  const open = expandedKeys.has(accKey);
  const total = sortedExisting.length + t.drafts.length;
  const countBadge = total > 1 ? '<span class="acc-count">' + total + '</span>' : '';
  const preview = root ? root.body : (t.drafts[0]?.body || '');
  return '<div class="pr-comment-thread acc-item' + (open ? ' open' : '') + '" data-thread-key="' + esc(key) + '" data-acc="' + esc(accKey) + '">' +
    '<div class="acc-head">' +
      '<span class="acc-chevron">&#8250;</span>' +
      '<span class="pr-comment-loc" data-path="' + esc(path) + '" data-line="' + t.line + '">' + esc(loc) + '</span>' +
      '<span class="pr-issue-comment-author">' + esc(root ? (root.author || 'unknown') : 'you') + '</span>' +
      countBadge +
      '<span class="acc-preview">' + esc(preview) + '</span>' +
    '</div>' +
    '<div class="acc-body">' + itemsHtml + draftsHtml + replyRow + '</div>' +
  '</div>';
}

function renderCommentsPanel() {
  const listEl = $('#pr-comments-list');
  const countEl = $('#pr-comments-count');
  if (!listEl) return;

  // Group existing review comments and local drafts by path, then by
  // side:line -- a reply carries the same path/line/side as its thread
  // root, so grouping by that key alone already gathers a whole thread.
  const byPath = new Map();
  const threadFor = (path, side, line) => {
    if (!byPath.has(path)) byPath.set(path, new Map());
    const m = byPath.get(path);
    const key = threadKey(path, side, line);
    if (!m.has(key)) m.set(key, { existing: [], drafts: [], side, line });
    return m.get(key);
  };
  for (const c of reviewComments) {
    if (!c.path) continue;
    threadFor(c.path, c.side || 'RIGHT', c.line).existing.push(c);
  }
  for (const c of comments) {
    if (!c.path) continue;
    threadFor(c.path, c.side || 'RIGHT', c.line).drafts.push(c);
  }

  const totalReview = reviewComments.length + comments.length;
  let html = '<div class="pr-comments-section-title">Conversation' +
    (issueComments.length ? ' (' + issueComments.length + ')' : '') + '</div>';
  html += issueComments.length
    ? [...issueComments].sort((a, b) => (a.createdAt || '').localeCompare(b.createdAt || '')).map(issueCommentCardHtml).join('')
    : '<div class="pr-comments-empty">No top-level comments yet.</div>';

  html += '<div class="pr-comments-section-title">Review comments' +
    (totalReview ? ' (' + totalReview + ')' : '') + '</div>';
  if (byPath.size === 0) {
    html += '<div class="pr-comments-empty">No inline comments yet.</div>';
  } else {
    const entries = [];
    for (const [path, threads] of byPath) {
      for (const [key, t] of threads) entries.push([path, key, t]);
    }
    entries.sort((a, b) => a[0].localeCompare(b[0]) || a[2].line - b[2].line);
    html += entries.map(([path, key, t]) => threadHtml(path, t, key)).join('');
  }

  listEl.innerHTML = html;
  if (countEl) {
    const n = issueComments.length + totalReview;
    countEl.textContent = n ? String(n) : '';
  }
}

/* ---------- launching another PR from a running session ---------- */

// Called from palette.js's "Git: Open Pull Request..." command. Fire and
// forget: the server re-execs a brand new px0 process (pr.go's
// handleLaunchPR), which opens its own browser tab the same way any px0
// invocation does. A failed checkout only ever shows in that child's own
// terminal, not here -- see docs/internals/github-pr-review.md.
export async function launchPR(target) {
  try {
    await apiPostJson('/api/pr/launch', { target });
    showToast('✓', 'Opening PR in a new tab…');
  } catch (e) {
    showToast('!', e.message || 'Could not launch PR review');
  }
}

function injectFooterButton() {
  const sel = $('#footer-sel');
  if (!sel || sel.querySelector('[data-sel="review-comment"]')) return;
  const btn = document.createElement('button');
  btn.className = 'footer-btn';
  btn.dataset.sel = 'review-comment';
  btn.title = withKeys('Add a review comment on this selection ({Alt+R})');
  btn.innerHTML = '<span class="footer-btn-label">Comment</span><kbd class="footer-kbd">' + esc(keyLabel('Alt+R')) + '</kbd>';
  sel.append(btn);
}
