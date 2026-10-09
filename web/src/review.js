// web/src/review.js
// Agent-authored review (px0 -review): rendering helpers for the comments an
// agent wrote, which pr.js folds into the same gutter markers and comments
// panel GitHub's comments use.
//
// Everything in a review is untrusted: a pull request can steer the agent into
// writing anything. Text goes through thread.js's escape-first renderer in its
// no-images mode: the CSP allows https images, so an injected image URL would
// carry repository data out the moment a comment rendered. See
// docs/internals/local-review.md section 9.
import { esc } from './state.js';
import { thrMdNoImages } from './thread.js';

const SEV_LABEL = { blocker: 'Blocker', major: 'Major', minor: 'Minor', nit: 'Nit', question: 'Question', praise: 'Praise' };

export function sevChip(c) {
  const sev = c.severity || 'minor';
  return '<span class="rv-sev rv-sev-' + esc(sev) + '">' + esc(SEV_LABEL[sev] || sev) + '</span>';
}

// Where px0 had to move or demote a comment, say so: the reviewer should know
// the placement is px0's best match for the agent's anchor text, not a given.
function statusNote(c) {
  const bits = [];
  if (c.status === 'moved') bits.push('moved from line ' + c.origLine + ' to where this code is now');
  else if (c.status === 'unanchored') bits.push('this code could not be found any more (the review said line ' + c.origLine + ')');
  else if (c.status === 'out_of_range') bits.push('line ' + c.origLine + ' is past the end of the file');
  if (c.outsideDiff) bits.push('this file is not part of the diff');
  return bits.length ? '<div class="rv-note">' + esc(bits.join('; ')) + '</div>' : '';
}

function refsHtml(c) {
  if (!c.refs || !c.refs.length) return '';
  const items = c.refs.map(r => {
    const where = (r.rev || 'head') + ':' + r.path + (r.line ? ':' + r.line + (r.endLine ? '-' + r.endLine : '') : '');
    return '<span class="rv-ref" title="' + esc(where) + '">' + esc(r.label ? r.label + ' · ' + where : where) + '</span>';
  });
  return '<div class="rv-refs"><span class="rv-refs-label">See also</span>' + items.join('') + '</div>';
}

// Marks a comment that exists only in the local review, not on the forge. CSS
// shows it only in a pull request session, where the other comments are GitHub's.
export const LOCAL_CHIP = '<span class="rv-local" title="From the local review; not posted to GitHub">not posted</span>';

// One agent comment as it appears in a thread or in the general list.
export function agentCommentCardHtml(c) {
  return '<div class="pr-comment-card rv-card' + (c.inReplyTo ? ' reply' : '') + '" data-rv="' + esc(c.rvId) + '">' +
    '<div class="pr-issue-comment-head">' +
      '<span class="pr-issue-comment-author">' + esc(c.author || 'review') + '</span>' +
      LOCAL_CHIP +
      sevChip(c) +
      (c.category ? '<span class="rv-cat">' + esc(c.category) + '</span>' : '') +
    '</div>' +
    (c.title ? '<div class="rv-title">' + esc(c.title) + '</div>' : '') +
    '<div class="thr-body rv-body">' + thrMdNoImages(c.body) + '</div>' +
    statusNote(c) +
    refsHtml(c) +
    '<div class="pr-comment-card-actions">' +
      '<button class="rv-discuss" data-rv="' + esc(c.rvId) + '" title="Ask your coding harness about this comment">Discuss</button>' +
    '</div>' +
  '</div>';
}

// One line of plain text for a collapsed thread: the Markdown source with
// fences and markers taken out, so a suggestion does not read as backticks.
export function previewText(md) {
  return (md || '')
    .replace(/^ {0,3}(`{3,}|~{3,}).*$/gm, '')
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[`*#>|]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

// Review comments become entries shaped like GitHub's inline comments, so the
// existing markers and thread grouping apply to them unchanged.
function asPRComments(snap, keep) {
  const author = snap.generatedBy?.agent || 'review';
  return (snap.comments || []).filter(keep).map(c => ({
    ...c,
    id: 'rv:' + c.id,
    rvId: c.id,
    agent: true,
    author,
    inReplyTo: c.inReplyTo ? 'rv:' + c.inReplyTo : 0,
    createdAt: '',
  }));
}

export const reviewPathComments = snap => asPRComments(snap, c => c.path);
export const reviewGeneralComments = snap => asPRComments(snap, c => !c.path);

const VERDICT = { approve: 'The review suggests approving', request_changes: 'The review suggests requesting changes', comment: 'The review is comments only' };

// The review's own section at the top of the comments panel: banners for
// anything that needs the reader's attention, the summary, then the comments
// that belong to no file.
export function reviewSummaryHtml(snap, general) {
  let html = '<div class="pr-comments-section-title">Review' +
    (snap.comments?.length ? ' (' + snap.comments.length + ')' : '') + '</div>';
  if (snap.title) html += '<div class="rv-review-title">' + esc(snap.title) + '</div>';
  if (snap.verdict) html += '<div class="rv-verdict rv-verdict-' + esc(snap.verdict) + '">' + esc(VERDICT[snap.verdict] || snap.verdict) + '</div>';
  if (!snap.resolved) html += '<div class="rv-banner">Checking comments against the code…</div>';
  if (snap.stale) html += '<div class="rv-banner warn">' + esc(snap.staleNote || 'This review was written against a different head.') + ' Comments are placed by matching their code, so check them.</div>';
  if (snap.loadError) html += '<div class="rv-banner warn">' + esc(snap.loadError) + ' Showing the last review that loaded.</div>';
  for (const w of snap.warnings || []) html += '<div class="rv-banner warn">' + esc(w) + '</div>';
  if (snap.rejected?.length) {
    html += '<details class="rv-banner warn"><summary>' + snap.rejected.length + ' comment' + (snap.rejected.length === 1 ? ' was' : 's were') + ' not shown</summary><ul>' +
      snap.rejected.map(r => '<li><code>' + esc(r.id) + '</code> ' + esc(r.reason) + '</li>').join('') + '</ul></details>';
  }
  if (snap.summary) html += '<div class="thr-body rv-body rv-summary">' + thrMdNoImages(snap.summary) + '</div>';
  if (general.length) html += general.map(agentCommentCardHtml).join('');
  if (!snap.summary && !general.length && !snap.comments?.length) html += '<div class="pr-comments-empty">The review has no comments.</div>';
  return html;
}

// What to hand newThread() for a comment: the code it is about when that is a
// head-side line (so the thread is anchored and the harness gets the snippet),
// and a prefilled message quoting the comment either way.
export function discussInfo(c) {
  const anchored = !!(c.path && c.line && c.side !== 'LEFT');
  const loc = c.path
    ? c.path + (c.line ? ':' + c.line + (c.endLine ? '-' + c.endLine : '') : '') + (c.side === 'LEFT' ? ' (old side)' : '')
    : 'general';
  const body = c.body.length > 600 ? c.body.slice(0, 600) + '…' : c.body;
  const message = 'Re ' + c.rvId + ' (' + (c.severity || 'minor') + ', ' + loc + '):\n' +
    body.split('\n').map(l => '> ' + l).join('\n') + '\n\n';
  return anchored
    ? { path: c.path, l1: c.line, l2: c.endLine || c.line, text: '', message }
    : { message };
}
