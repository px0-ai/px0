#!/usr/bin/env node
import { pushUndo, undo, redo, snapshotDoc, restoreDoc } from '../web/src/edit-undo.js';

function doc(lines) {
  const raw = lines.slice();
  return {
    total: raw.length,
    raw,
    lines: raw.map(() => undefined),
    cur: 1,
    col: 0,
    dirty: false,
    dirtyLines: new Set(),
    undo: [],
    redo: [],
  };
}

function assert(cond, msg) {
  if (!cond) { console.error('FAIL:', msg); process.exit(1); }
}

let d = doc(['hello']);
pushUndo(d);
d.raw[0] = 'world';
d.dirty = true;
assert(undo(d) && d.raw[0] === 'hello', 'undo restores');
assert(redo(d) && d.raw[0] === 'world', 'redo reapplies');

console.log('edit-undo: all tests passed');
