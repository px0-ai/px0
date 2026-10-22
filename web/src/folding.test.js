import test from 'node:test';
import assert from 'node:assert/strict';
import { createFolds, foldIndex, rebuildFolds, foldLineToRow, foldRowToLine, visibleFoldLine, nextFoldLine, moveFoldLine, revealFoldLine, updateFoldPixels, foldedLineY, unfoldedY } from './folding.js';

test('nested collapse choices survive parent collapse without double counting', () => {
  const f = createFolds([[2, 10], [4, 7], [12, 15]], 20);
  f.closed[1] = 1; rebuildFolds(f);
  assert.equal(foldLineToRow(f, 8), 5);
  f.closed[0] = 1; rebuildFolds(f);
  assert.deepEqual([...f.spans], [2, 10, 8]);
  assert.equal(foldRowToLine(f, 3), 11);
  assert.equal(nextFoldLine(f, 2), 11);
  f.closed[0] = 0; rebuildFolds(f);
  assert.equal(f.closed[1], 1);
  assert.equal(visibleFoldLine(f, 6), 4);
});

test('cursor movements count visible rows and clamp at a folded EOF', () => {
  const f = createFolds([[2, 10], [12, 20]], 20);
  f.closed.fill(1); rebuildFolds(f);
  assert.equal(moveFoldLine(f, 2, 1, 20), 11);
  assert.equal(moveFoldLine(f, 11, -1, 20), 2);
  assert.equal(moveFoldLine(f, 1, 99, 20), 12);
  assert.equal(moveFoldLine(f, 12, -99, 20), 1);
  assert.equal(foldLineToRow(f, 21) - 1, 4);
});

test('revealing a target expands all enclosing folds, preserving unrelated folds', () => {
  const f = createFolds([[2, 10], [4, 7], [12, 15]], 20);
  f.closed.fill(1); rebuildFolds(f);
  assert.equal(revealFoldLine(f, 6), true);
  assert.deepEqual([...f.closed], [0, 0, 1]);
  assert.equal(revealFoldLine(f, 12), false);
  assert.equal(revealFoldLine(f, 14), true);
  assert.equal(f.spans.length, 0);
});

test('wrapped geometry uses original heights, including a multiline opening line', () => {
  const f = createFolds([[2, 4], [6, 7]], 8);
  f.closed.fill(1); rebuildFolds(f);
  const heights = [20, 60, 40, 20, 20, 40, 20, 20];
  const offsets = new Float64Array(9);
  heights.forEach((h, i) => { offsets[i + 1] = offsets[i] + h; });
  updateFoldPixels(f, offsets, 1, 20);
  assert.equal(foldedLineY(f, 2, offsets, 20), 20);
  assert.equal(foldedLineY(f, 3, offsets, 20), 20);
  assert.equal(foldedLineY(f, 5, offsets, 20), 80);
  assert.equal(foldedLineY(f, 8, offsets, 20), 140);
  assert.equal(foldedLineY(f, 9, offsets, 20), 160);
  assert.equal(unfoldedY(f, 79, offsets, 20), 79);
  assert.equal(unfoldedY(f, 80, offsets, 20), 140);
  assert.equal(unfoldedY(f, 140, offsets, 20), 220);
  offsets[3] += 10; offsets[4] += 10; offsets[5] += 10; offsets[6] += 10; offsets[7] += 10; offsets[8] += 10;
  updateFoldPixels(f, offsets, 2, 20);
  assert.equal(foldedLineY(f, 5, offsets, 20), 80);
});

test('mapping agrees with a naive visible-line model over all collapse combinations', () => {
  const pairs = [[2, 18], [4, 8], [10, 15], [20, 25], [21, 23]];
  for (let bits = 0; bits < 32; bits++) {
    const f = createFolds(pairs, 30);
    f.closed.forEach((_, i) => { f.closed[i] = bits >> i & 1; }); rebuildFolds(f);
    updateFoldPixels(f, null, 0, 20);
    const visible = Array.from({ length: 30 }, (_, i) => i + 1).filter(n => !pairs.some(([s, e], i) => f.closed[i] && n > s && n <= e));
    visible.forEach((line, row) => {
      assert.equal(foldRowToLine(f, row + 1), line);
      assert.equal(foldLineToRow(f, line), row + 1);
      assert.equal(foldedLineY(f, line, null, 20), row * 20);
      assert.equal(Math.floor(unfoldedY(f, row * 20, null, 20) / 20) + 1, line);
    });
    assert.equal(foldedLineY(f, 31, null, 20), visible.length * 20);
  }
});

test('validation rejects crossing, duplicate and out-of-file ranges', () => {
  const f = createFolds([[2, 9], [2, 5], [3, 12], [4, 7], [-1, 2], [10, 11], [12, 50]], 20);
  assert.deepEqual([...f.ranges], [2, 9, 4, 7, 10, 11]);
  assert.equal(foldIndex(f, 4), 1);
  assert.equal(foldIndex(f, 5), -1);
});

test('400,000-line file needs only sparse fold metadata', () => {
  const f = createFolds([[1, 399999]], 400000);
  f.closed[0] = 1; rebuildFolds(f);
  const bytes = f.ranges.byteLength + f.closed.byteLength + f.spans.byteLength + f.pixels.byteLength;
  assert.equal(bytes, 37);
  assert.equal(moveFoldLine(f, 1, 1, 400000), 400000);
});
