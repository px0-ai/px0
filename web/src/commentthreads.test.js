import test from 'node:test';
import assert from 'node:assert/strict';

import { ctGroupThreads, ctThreadKey } from './commentthreads.js';

const agent = (id, path, line, extra = {}) => ({ id: 'rv:' + id, agent: true, path, line, side: 'RIGHT', ...extra });
const gh = (id, path, line, extra = {}) => ({ id, path, line, side: 'RIGHT', ...extra });

test('an agent comment and a GitHub comment on one line are two threads', () => {
  const t = ctGroupThreads([agent('c1', 'a.go', 10), gh(5, 'a.go', 10)], []);
  assert.equal(t.size, 2);
  assert.equal(t.get(ctThreadKey('a.go', 'RIGHT', 10, 'rv')).existing.length, 1);
  assert.equal(t.get(ctThreadKey('a.go', 'RIGHT', 10, 'gh')).existing.length, 1);
});

test('a draft joins the GitHub thread on its line, not the agent one', () => {
  const t = ctGroupThreads([agent('c1', 'a.go', 10), gh(5, 'a.go', 10)], [{ id: 1, path: 'a.go', line: 10, side: 'RIGHT' }]);
  assert.equal(t.size, 2);
  assert.equal(t.get(ctThreadKey('a.go', 'RIGHT', 10, 'gh')).drafts.length, 1);
  assert.equal(t.get(ctThreadKey('a.go', 'RIGHT', 10, 'rv')).drafts.length, 0);
});

test('an outdated comment is filed under line 0 and flagged', () => {
  const t = ctGroupThreads([gh(5, 'a.go', 12, { outdated: true })], []);
  const th = t.get(ctThreadKey('a.go', 'RIGHT', 0, 'gh'));
  assert.ok(th);
  assert.equal(th.line, 0);
  assert.equal(th.outdated, true);
});

test('replies join their root', () => {
  const t = ctGroupThreads([gh(5, 'a.go', 10), gh(6, 'a.go', 10, { inReplyTo: 5 })], []);
  assert.equal(t.size, 1);
  assert.equal([...t.values()][0].existing.length, 2);
});

test('sides are separate threads', () => {
  const t = ctGroupThreads([gh(5, 'a.go', 10), gh(6, 'a.go', 10, { side: 'LEFT' })], []);
  assert.equal(t.size, 2);
});

test('endLine is the largest in the thread, and only for a line comment', () => {
  const t = ctGroupThreads([agent('c1', 'a.go', 10, { endLine: 14 }), agent('c2', 'a.go', 0, { endLine: 3 })], []);
  assert.equal(t.get(ctThreadKey('a.go', 'RIGHT', 10, 'rv')).endLine, 14);
  assert.equal(t.get(ctThreadKey('a.go', 'RIGHT', 0, 'rv')).endLine, 0);
});

test('comments with no path are left out', () => {
  assert.equal(ctGroupThreads([agent('g', '', 0)], []).size, 0);
});
