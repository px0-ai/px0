import { test } from 'node:test';
import { strict as assert } from 'node:assert';
import { openDiffByDefault } from './open-view.js';

test('initial file view honors preference and explicit actions', () => {
  assert.equal(openDiffByDefault({ hasDiff: true, preferDiff: false }), false);
  assert.equal(openDiffByDefault({ hasDiff: true, preferDiff: true }), true);
  assert.equal(openDiffByDefault({ hasDiff: true, preferDiff: true, view: 'source' }), false);
  assert.equal(openDiffByDefault({ hasDiff: true, view: 'diff' }), true);
  assert.equal(openDiffByDefault({ hasDiff: false, view: 'diff' }), false);
  assert.equal(openDiffByDefault({ hasDiff: true, deleted: true, view: 'source' }), true);
});
