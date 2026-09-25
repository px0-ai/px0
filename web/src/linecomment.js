// web/src/linecomment.js
// Handles hovering on line numbers to show a pencil icon (✎), and tapping
// it to trigger an inline AI edit on that line -- or, in a PR review
// session's diff view, to draft a review comment there instead (Alt+R's
// other entry point, and the one a reviewer actually reaches for first).
import { S, doc_ } from './state.js';
import { openAgentEdit } from './agent.js';
import { getReviewHandler } from './selbar.js';

export function initLineComment() {
  document.addEventListener('click', e => {
    const target = /** @type {HTMLElement|null} */ (e.target);
    const btn = target?.closest('.line-btn');
    if (!btn) return;
    e.preventDefault();
    e.stopPropagation();
    handleLineBtnClick(btn);
  });
}

function handleLineBtnClick(btn) {
  const d = doc_();
  if (!d) return;

  const diffRow = btn.closest('.diff-row, .diff-side');

  // In a PR review session, a diff line is for leaving a review comment --
  // the button's own tooltip promises that, and it's what a reviewer wants
  // most. AI edit stays reachable via selection + Alt+E either way. Rows
  // marked non-reviewable (diff.js's "Your changes" section, i.e. edits the
  // reviewer made locally since checkout) fall through to a plain inline
  // edit instead: those lines aren't part of any commit GitHub knows about,
  // so there's nothing a submitted review could attach a comment to.
  if (diffRow) {
    const reviewable = diffRow.dataset.reviewable !== '0';
    const reviewHandler = S.meta?.pr && reviewable && getReviewHandler();
    if (reviewHandler) { reviewHandler(diffLineInfo(diffRow, d.path)); return; }
  }

  let line = 1;
  let text = '';
  const row = btn.closest('.row');
  if (row) {
    line = +row.dataset.l || 1;
    text = (d.lines && d.lines[line - 1]) || '';
  } else if (diffRow) {
    line = diffRow.classList.contains('diff-side-left')
      ? +(diffRow.dataset.oldL || diffRow.dataset.at || diffRow.dataset.l || 1)
      : +(diffRow.dataset.l || diffRow.dataset.at || 1);
    text = diffRow.querySelector('.diff-code')?.textContent || '';
  }

  openAgentEdit({ path: d.path, l1: line, l2: line, text });
}

// Mirrors selbar.js's diffSelection() for a single row instead of a range: a
// pure deletion has no line on disk, so it's anchored on the old side with
// both delL/l set (pr.js picks whichever its side needs); everything else --
// context or an addition -- lives on the new side.
function diffLineInfo(diffRow, path) {
  const text = diffRow.querySelector('.diff-code')?.textContent || '';
  const isOldOnly = diffRow.dataset.oldL !== undefined && diffRow.dataset.l === undefined;
  if (isOldOnly) {
    const old = +diffRow.dataset.oldL || 1;
    return { text, l1: old, l2: old, delL1: old, delL2: old, path, fromDiff: true, side: 'LEFT' };
  }
  const line = +(diffRow.dataset.l || diffRow.dataset.at || 1);
  return { text, l1: line, l2: line, path, fromDiff: true, side: 'RIGHT' };
}
