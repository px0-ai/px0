// web/src/linecomment.js
// Handles hovering on line numbers to show a thread icon, and clicking it to
// open the same actions menu as a right click on a selection, aimed at that
// line: start a thread, edit inline, copy a reference, and (in a PR review
// session's diff view) add a review comment.
import { S, doc_ } from './state.js';
import { getReviewHandler, openLineMenu } from './selbar.js';

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
  const at = btn.getBoundingClientRect();

  // On a line GitHub knows about in a PR review session the menu's review
  // action needs the diff side, so build the richer description for it. Rows
  // marked non-reviewable (diff.js's "Your changes" section, i.e. edits the
  // reviewer made locally since checkout) get a plain line instead: there is
  // nothing a submitted review could attach a comment to.
  if (diffRow && diffRow.dataset.reviewable !== '0' && S.meta?.pr && getReviewHandler()) {
    openLineMenu(diffLineInfo(diffRow, d.path), at.right + 4, at.top);
    return;
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

  openLineMenu({ path: d.path, l1: line, l2: line, text }, at.right + 4, at.top);
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
