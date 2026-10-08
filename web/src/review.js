// web/src/review.js
// Agent-authored review (px0 -review): rendering helpers for the comments an
// agent wrote, which pr.js folds into the same gutter markers and comments
// panel GitHub's comments use.
//
// Everything in a review is untrusted: a pull request can steer the agent into
// writing anything. So text is escaped first and only a small set of tags is
// built from it, images are never loaded (the CSP allows https images, which
// would let an injected URL carry repository data out the moment a comment
// rendered), and links are limited to http(s). See
// docs/internals/review-file-spec.md section 7.
import { esc } from './state.js';

const SEV_LABEL = { blocker: 'Blocker', major: 'Major', minor: 'Minor', nit: 'Nit', question: 'Question', praise: 'Praise' };

const safeUrl = u => /^https?:\/\/[^\s"'<>]+$/i.test(u);

// Inline: `code`, **bold**, [text](http-url) and ![alt](http-url). An image
// becomes a plain link; nothing here ever produces an <img>.
function inline(src) {
  const re = /`([^`\n]+)`|(!?)\[([^\]\n]*)\]\(([^)\s]*)\)|\*\*([^*\n]+)\*\*/g;
  let out = '';
  let last = 0;
  let m;
  while ((m = re.exec(src))) {
    out += esc(src.slice(last, m.index));
    last = re.lastIndex;
    if (m[1] !== undefined) {
      out += '<code>' + esc(m[1]) + '</code>';
    } else if (m[5] !== undefined) {
      out += '<strong>' + esc(m[5]) + '</strong>';
    } else {
      const label = m[3] || m[4];
      if (!safeUrl(m[4])) {
        out += esc(label); // relative and other schemes are shown as text only
      } else {
        const text = m[2] ? 'image: ' + label : label;
        out += '<a href="' + esc(m[4]) + '" target="_blank" rel="noopener noreferrer">' + esc(text) + '</a>';
      }
    }
  }
  return out + esc(src.slice(last));
}

// A small block renderer: fenced code (a "suggestion" fence is labelled),
// headings, bullet and numbered lists, paragraphs.
export function reviewMd(src) {
  if (!src) return '';
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  let html = '';
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }

    const fence = /^ {0,3}(`{3,}|~{3,})\s*([\w+#.-]*)[^\n]*$/.exec(line);
    if (fence) {
      const marker = fence[1][0];
      const closer = new RegExp('^ {0,3}' + (marker === '`' ? '`' : '~') + '{' + fence[1].length + ',}\\s*$');
      const code = [];
      i++;
      while (i < lines.length && !closer.test(lines[i])) code.push(lines[i++]);
      i++;
      const lang = fence[2];
      if (lang === 'suggestion') {
        html += '<div class="rv-suggest"><div class="rv-suggest-label">Suggested change</div><pre><code>' + esc(code.join('\n')) + '</code></pre></div>';
      } else {
        html += '<pre class="rv-pre"' + (lang ? ' data-lang="' + esc(lang) + '"' : '') + '><code>' + esc(code.join('\n')) + '</code></pre>';
      }
      continue;
    }

    const h = /^#{1,6}\s+(.*)$/.exec(line);
    if (h) { html += '<div class="rv-h">' + inline(h[1]) + '</div>'; i++; continue; }

    if (/^\s*([-*]|\d+[.)])\s+/.test(line)) {
      const ordered = /^\s*\d+[.)]\s+/.test(line);
      const items = [];
      while (i < lines.length && /^\s*([-*]|\d+[.)])\s+/.test(lines[i])) {
        items.push('<li>' + inline(lines[i].replace(/^\s*([-*]|\d+[.)])\s+/, '')) + '</li>');
        i++;
      }
      html += (ordered ? '<ol>' : '<ul>') + items.join('') + (ordered ? '</ol>' : '</ul>');
      continue;
    }

    const para = [];
    while (i < lines.length && lines[i].trim() && !/^ {0,3}(`{3,}|~{3,})/.test(lines[i]) && !/^#{1,6}\s/.test(lines[i]) && !/^\s*([-*]|\d+[.)])\s+/.test(lines[i])) {
      para.push(lines[i++]);
    }
    html += '<p>' + para.map(inline).join('<br>') + '</p>';
  }
  return html;
}

function sevChip(c) {
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

// One agent comment as it appears in a thread or in the general list.
export function agentCommentCardHtml(c) {
  return '<div class="pr-comment-card rv-card' + (c.inReplyTo ? ' reply' : '') + '" data-rv="' + esc(c.rvId) + '">' +
    '<div class="pr-issue-comment-head">' +
      '<span class="pr-issue-comment-author">' + esc(c.author || 'review') + '</span>' +
      sevChip(c) +
      (c.category ? '<span class="rv-cat">' + esc(c.category) + '</span>' : '') +
    '</div>' +
    (c.title ? '<div class="rv-title">' + esc(c.title) + '</div>' : '') +
    '<div class="rv-body">' + reviewMd(c.body) + '</div>' +
    statusNote(c) +
    refsHtml(c) +
    '<div class="pr-comment-card-actions">' +
      '<button class="rv-discuss" data-rv="' + esc(c.rvId) + '" title="Ask your coding harness about this comment">Discuss</button>' +
    '</div>' +
  '</div>';
}

export { sevChip };

// One line of plain text for a collapsed thread: the Markdown source with
// fences and markers taken out, so a suggestion does not read as backticks.
export function previewText(md) {
  return (md || '').replace(/^ {0,3}(`{3,}|~{3,}).*$/gm, '').replace(/[`*#>]/g, '').replace(/\s+/g, ' ').trim();
}

// Path comments become entries shaped like GitHub's inline comments, so the
// existing markers and thread grouping apply to them unchanged.
export function reviewPathComments(snap) {
  const author = snap.generatedBy?.agent || 'review';
  return (snap.comments || []).filter(c => c.path).map(c => ({
    ...c,
    id: 'rv:' + c.id,
    rvId: c.id,
    agent: true,
    author,
    inReplyTo: c.inReplyTo ? 'rv:' + c.inReplyTo : 0,
    createdAt: '',
  }));
}

export function reviewGeneralComments(snap) {
  const author = snap.generatedBy?.agent || 'review';
  return (snap.comments || []).filter(c => !c.path).map(c => ({
    ...c,
    id: 'rv:' + c.id,
    rvId: c.id,
    agent: true,
    author,
    inReplyTo: c.inReplyTo ? 'rv:' + c.inReplyTo : 0,
    createdAt: '',
  }));
}

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
  if (snap.summary) html += '<div class="rv-body rv-summary">' + reviewMd(snap.summary) + '</div>';
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
