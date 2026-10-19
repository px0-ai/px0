import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('../src/diff-expand.js', import.meta.url), 'utf8');
const expand = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));

test('upward expander moves from hunk 225 to line 205 after first expansion', () => {
  const plan = expand.upwardGapPlan([{ s: 205, e: 224 }], 1, 224);

  assert.deepEqual(plan.context, [[205, 224]]);
  assert.deepEqual(plan.expand, [185, 204]);
});

test('each upward expansion advances control another 20 lines', () => {
  const plan = expand.upwardGapPlan([{ s: 185, e: 224 }], 1, 224);

  assert.deepEqual(plan.context, [[185, 224]]);
  assert.deepEqual(plan.expand, [165, 184]);
});

test('upward expander disappears only when preceding gap is fully opened', () => {
  const plan = expand.upwardGapPlan([{ s: 1, e: 224 }], 1, 224);

  assert.deepEqual(plan.context, [[1, 224]]);
  assert.equal(plan.expand, null);
});

test('initial unopened gap has no leading expander; hunk header owns first click', () => {
  const plan = expand.upwardGapPlan([], 1, 224);

  assert.deepEqual(plan.context, []);
  assert.equal(plan.expand, null);
  assert.deepEqual(expand.upwardExpandRun([], 1, 224), [205, 224]);
});
