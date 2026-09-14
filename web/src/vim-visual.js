// web/src/vim-visual.js — visual selection range helpers (no DOM).
import { lineText } from './edit-buffer.js';

export function visualRangeFor(d, visual) {
  if (!visual || !d) return null;
  const a = visual.anchor;
  const b = { line: d.cur, col: d.col || 0 };
  if (visual.kind === 'line') {
    const l1 = Math.min(a.line, b.line);
    const l2 = Math.max(a.line, b.line);
    return { l1, l2, kind: 'line' };
  }
  if (a.line === b.line) {
    const c1 = Math.min(a.col, b.col);
    const c2 = Math.max(a.col, b.col);
    return { l1: a.line, l2: a.line, c1, c2, kind: 'char' };
  }
  const forward = a.line < b.line || (a.line === b.line && a.col <= b.col);
  const start = forward ? a : b;
  const end = forward ? b : a;
  return { l1: start.line, l2: end.line, c1: start.col, c2: end.col, kind: 'char', multiline: true };
}

export { lineText };
