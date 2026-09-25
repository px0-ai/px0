import test from 'node:test';
import assert from 'node:assert/strict';
import { fuzzyFilter } from './model-picker.js';

test('fuzzyFilter ranks direct matches and ignores case', () => {
  const models = ['opencode/big-pickle', 'anthropic/claude-sonnet', 'google/gemini-2.5-pro'];
  assert.deepEqual(fuzzyFilter(models, ' CLAUDE ').map(match => match.value), ['anthropic/claude-sonnet']);
});

test('fuzzyFilter matches non-contiguous characters', () => {
  const models = ['opencode/gpt-5', 'opencode/big-pickle'];
  assert.deepEqual(fuzzyFilter(models, 'ogp5').map(match => match.value), ['opencode/gpt-5']);
});

test('fuzzyFilter preserves the original order for an empty query and rejects no matches', () => {
  const models = ['first/model', 'second/model'];
  assert.deepEqual(fuzzyFilter(models, '').map(match => match.value), models);
  assert.deepEqual(fuzzyFilter(models, 'missing'), []);
});
