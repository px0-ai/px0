#!/usr/bin/env node
/**
 * Unit tests for edit-buffer helpers in web/src/edit-buffer.js.
 */
import {
  insertText, insertNewline, deleteChar, deleteLine, deleteToEOL, openLine, assembleContent,
} from '../web/src/edit-buffer.js';

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
  };
}

function assert(cond, msg) {
  if (!cond) { console.error('FAIL:', msg); process.exit(1); }
}

let d = doc(['hello']);
d.col = 5;
insertText(d, '!');
assert(d.raw[0] === 'hello!' && d.col === 6, 'insertText at end');

d = doc(['ab', 'cd']);
d.cur = 1;
d.col = 1;
insertNewline(d);
assert(d.total === 3 && d.raw[0] === 'a' && d.raw[1] === 'b' && d.cur === 2, 'insertNewline splits line');

d = doc(['hello']);
d.col = 0;
deleteChar(d, false);
assert(d.raw[0] === 'hello', 'backspace at start noop');

d = doc(['hello']);
d.col = 1;
deleteChar(d, false);
assert(d.raw[0] === 'ello' && d.col === 0, 'backspace deletes char');

d = doc(['a', 'b']);
d.cur = 2;
d.col = 0;
deleteChar(d, false);
assert(d.raw[0] === 'ab' && d.total === 1, 'backspace joins lines');

d = doc(['line1', 'line2']);
d.cur = 1;
deleteLine(d);
assert(d.total === 1 && d.raw[0] === 'line2', 'deleteLine');

d = doc(['hello world']);
d.col = 5;
deleteToEOL(d);
assert(d.raw[0] === 'hello', 'deleteToEOL');

d = doc(['x']);
openLine(d, true);
assert(d.total === 2 && d.raw[1] === '' && d.cur === 2, 'openLine below');

d = doc(['a', 'b']);
assert(assembleContent(d) === 'a\nb', 'assembleContent');

console.log('edit-buffer: all tests passed');
