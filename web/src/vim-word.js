// web/src/vim-word.js — pure Vim word-motion helpers (no DOM dependencies).
export const WORD = /[A-Za-z0-9_$]/;

export function forwardWordCol(text, col) {
  const len = text.length;
  col = Math.min(Math.max(0, col), len);
  if (col >= len) return { col: len, pastEnd: true };
  let i = col;
  if (WORD.test(text[i])) while (i < len && WORD.test(text[i])) i++;
  while (i < len && !WORD.test(text[i])) i++;
  return { col: Math.min(i, len), pastEnd: i >= len };
}

export function backwardWordCol(text, col) {
  const len = text.length;
  col = Math.min(Math.max(0, col), len);
  if (col === 0) return { col: 0, beforeStart: true };
  let i = col - 1;
  while (i > 0 && !WORD.test(text[i])) i--;
  while (i > 0 && WORD.test(text[i - 1])) i--;
  return { col: i, beforeStart: i === 0 };
}
