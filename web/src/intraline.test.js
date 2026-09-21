import assert from 'node:assert/strict';
import test from 'node:test';
import { intralineDiff } from './intraline.js';

function text(parts) {
  return parts.map(part => part.text).join('');
}

test('marks every separate replacement range', () => {
  const diff = intralineDiff('a=1, b=2', 'a=3, b=4');
  assert.deepEqual(diff.oldParts, [
    { type: 'equal', text: 'a=' }, { type: 'del', text: '1' },
    { type: 'equal', text: ', b=' }, { type: 'del', text: '2' },
  ]);
  assert.deepEqual(diff.newParts, [
    { type: 'equal', text: 'a=' }, { type: 'add', text: '3' },
    { type: 'equal', text: ', b=' }, { type: 'add', text: '4' },
  ]);
});

test('keeps insertions, deletions, and punctuation lossless', () => {
  const added = intralineDiff('call(a, b);', 'call(a, c, d);');
  assert.equal(text(added.oldParts), 'call(a, b);');
  assert.equal(text(added.newParts), 'call(a, c, d);');
  assert.deepEqual(added.oldParts.filter(part => part.type === 'del').map(part => part.text), ['b']);
  assert.deepEqual(added.newParts.filter(part => part.type === 'add').map(part => part.text), ['c, d']);

  const removed = intralineDiff('call(a, b, c);', 'call(a, c);');
  assert.equal(text(removed.oldParts), 'call(a, b, c);');
  assert.equal(text(removed.newParts), 'call(a, c);');
  assert.deepEqual(removed.oldParts.filter(part => part.type === 'del').map(part => part.text), ['b, ']);
});

test('refines short changed identifiers by character', () => {
  const diff = intralineDiff('timeout', 'timeoutMs');
  assert.deepEqual(diff.oldParts, [{ type: 'equal', text: 'timeout' }]);
  assert.deepEqual(diff.newParts, [{ type: 'equal', text: 'timeout' }, { type: 'add', text: 'Ms' }]);
});

test('keeps unicode text intact', () => {
  const diff = intralineDiff('const greeting = "hello 🌍";', 'const greeting = "hello 🌎";');
  assert.equal(text(diff.oldParts), 'const greeting = "hello 🌍";');
  assert.equal(text(diff.newParts), 'const greeting = "hello 🌎";');
  assert.deepEqual(diff.oldParts.filter(part => part.type === 'del').map(part => part.text), ['🌍']);
  assert.deepEqual(diff.newParts.filter(part => part.type === 'add').map(part => part.text), ['🌎']);
});

test('falls back for oversized lines', () => {
  assert.equal(intralineDiff('a'.repeat(2049), 'b'.repeat(2049)), null);
  assert.equal(intralineDiff(Array(513).fill('a').join(' '), Array(513).fill('b').join(' ')), null);
});
