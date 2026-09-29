import test from 'node:test';
import assert from 'node:assert/strict';

import { DEFAULT_THEME, chooseThemePreference } from './theme-preference.js';

test('prefers saved theme over fallback default setting', () => {
  assert.equal(chooseThemePreference('nord', DEFAULT_THEME), 'nord');
});

test('prefers explicit non-default server setting over stored theme', () => {
  assert.equal(chooseThemePreference('nord', 'dracula'), 'dracula');
});

test('falls back to default when nothing else is set', () => {
  assert.equal(chooseThemePreference(null, null), DEFAULT_THEME);
});
