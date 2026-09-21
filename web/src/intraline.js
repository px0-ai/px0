// web/src/intraline.js
// Bounded, lossless intraline diffing for paired replacement lines.

const INTRALINE_MAX_CODE_POINTS = 2048;
const INTRALINE_MAX_TOKENS = 512;
const INTRALINE_REFINE_MAX_CODE_POINTS = 128;
const INTRALINE_TOKEN_RE = /\s+|[\p{L}\p{N}_]+|[^\s\p{L}\p{N}_]/gu;
const INTRALINE_WORD_RE = /^[\p{L}\p{N}_]+$/u;

function intralineTokens(text) {
  return text.match(INTRALINE_TOKEN_RE) || [];
}

// Myers' shortest-edit-script algorithm. The input sizes are capped by the
// caller, so keeping one frontier per edit distance is modest and predictable.
function intralineMyers(oldItems, newItems) {
  const oldLen = oldItems.length, newLen = newItems.length;
  const max = oldLen + newLen;
  let frontier = new Map([[1, 0]]);
  const trace = [];

  for (let distance = 0; distance <= max; distance++) {
    for (let diagonal = -distance; diagonal <= distance; diagonal += 2) {
      const down = diagonal === -distance ||
        (diagonal !== distance && (frontier.get(diagonal - 1) ?? -Infinity) < (frontier.get(diagonal + 1) ?? -Infinity));
      let oldPos = down ? (frontier.get(diagonal + 1) ?? 0) : (frontier.get(diagonal - 1) ?? 0) + 1;
      let newPos = oldPos - diagonal;
      while (oldPos < oldLen && newPos < newLen && oldItems[oldPos] === newItems[newPos]) {
        oldPos++;
        newPos++;
      }
      frontier.set(diagonal, oldPos);
      if (oldPos >= oldLen && newPos >= newLen) {
        trace.push(new Map(frontier));
        return intralineBacktrack(trace, oldItems, newItems);
      }
    }
    trace.push(new Map(frontier));
  }
  return [];
}

function intralineBacktrack(trace, oldItems, newItems) {
  let oldPos = oldItems.length, newPos = newItems.length;
  const edits = [];
  for (let distance = trace.length - 1; distance > 0; distance--) {
    const frontier = trace[distance - 1];
    const diagonal = oldPos - newPos;
    const down = diagonal === -distance ||
      (diagonal !== distance && (frontier.get(diagonal - 1) ?? -Infinity) < (frontier.get(diagonal + 1) ?? -Infinity));
    const previousDiagonal = down ? diagonal + 1 : diagonal - 1;
    const previousOldPos = frontier.get(previousDiagonal) ?? 0;
    const previousNewPos = previousOldPos - previousDiagonal;

    while (oldPos > previousOldPos && newPos > previousNewPos) {
      edits.push({ type: 'equal', text: oldItems[--oldPos] });
      newPos--;
    }
    if (oldPos === previousOldPos) edits.push({ type: 'add', text: newItems[--newPos] });
    else edits.push({ type: 'del', text: oldItems[--oldPos] });
  }
  while (oldPos > 0 && newPos > 0) {
    edits.push({ type: 'equal', text: oldItems[--oldPos] });
    newPos--;
  }
  while (oldPos > 0) edits.push({ type: 'del', text: oldItems[--oldPos] });
  while (newPos > 0) edits.push({ type: 'add', text: newItems[--newPos] });
  return edits.reverse();
}

function intralinePush(parts, type, text) {
  if (!text) return;
  const previous = parts[parts.length - 1];
  if (previous && previous.type === type) previous.text += text;
  else parts.push({ type, text });
}

function intralineAppendEdits(oldParts, newParts, edits) {
  for (const edit of edits) {
    if (edit.type === 'equal') {
      intralinePush(oldParts, 'equal', edit.text);
      intralinePush(newParts, 'equal', edit.text);
    } else if (edit.type === 'del') intralinePush(oldParts, 'del', edit.text);
    else intralinePush(newParts, 'add', edit.text);
  }
}

function intralineAppendReplacement(oldParts, newParts, deleted, added) {
  if (deleted.length === 1 && added.length === 1 &&
      INTRALINE_WORD_RE.test(deleted[0]) && INTRALINE_WORD_RE.test(added[0])) {
    const oldChars = Array.from(deleted[0]), newChars = Array.from(added[0]);
    if (oldChars.length <= INTRALINE_REFINE_MAX_CODE_POINTS && newChars.length <= INTRALINE_REFINE_MAX_CODE_POINTS) {
      intralineAppendEdits(oldParts, newParts, intralineMyers(oldChars, newChars));
      return;
    }
  }
  intralinePush(oldParts, 'del', deleted.join(''));
  intralinePush(newParts, 'add', added.join(''));
}

// Returns matching text parts for both sides, or null when an unusually long
// line is deliberately left with its existing whole-line diff treatment.
export function intralineDiff(oldText, newText) {
  const oldChars = Array.from(oldText), newChars = Array.from(newText);
  if (oldChars.length > INTRALINE_MAX_CODE_POINTS || newChars.length > INTRALINE_MAX_CODE_POINTS) return null;
  const oldTokens = intralineTokens(oldText), newTokens = intralineTokens(newText);
  if (oldTokens.length > INTRALINE_MAX_TOKENS || newTokens.length > INTRALINE_MAX_TOKENS) return null;

  const edits = intralineMyers(oldTokens, newTokens);
  const oldParts = [], newParts = [];
  for (let i = 0; i < edits.length;) {
    if (edits[i].type === 'equal') {
      intralineAppendEdits(oldParts, newParts, [edits[i++]]);
      continue;
    }
    const deleted = [], added = [];
    while (i < edits.length && edits[i].type !== 'equal') {
      const edit = edits[i++];
      if (edit.type === 'del') deleted.push(edit.text);
      else added.push(edit.text);
    }
    intralineAppendReplacement(oldParts, newParts, deleted, added);
  }
  return { oldParts, newParts };
}
