#!/usr/bin/env node
/**
 * Unit tests for Vim word-motion helpers in web/src/cursor.js.
 * Run via: node scripts/vim-test.js
 */
import { forwardWordCol, backwardWordCol } from '../web/src/vim-word.js';

function assert(cond, msg) {
  if (!cond) {
    console.error('FAIL:', msg);
    process.exit(1);
  }
}

let r = forwardWordCol('hello world', 0);
assert(r.col === 6 && !r.pastEnd, 'forward from line start');

r = forwardWordCol('hello world', 6);
assert(r.col === 11 && r.pastEnd, 'forward from second word');

r = forwardWordCol('  spaced  words', 0);
assert(r.col === 2, 'forward skips leading whitespace');

r = backwardWordCol('hello world', 11);
assert(r.col === 6, 'backward from line end');

r = backwardWordCol('hello world', 6);
assert(r.col === 0, 'backward to first word');

r = backwardWordCol('hello world', 0);
assert(r.col === 0 && r.beforeStart, 'backward at line start');

console.log('vim word motion: all tests passed');
