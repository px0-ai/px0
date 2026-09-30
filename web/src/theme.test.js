import test from 'node:test';
import assert from 'node:assert/strict';

import { DEFAULT_THEME, DEFAULT_LIGHT_THEME, chooseThemePreference, pickAutoTheme } from './theme-preference.js';

test('prefers saved theme over fallback default setting', () => {
  assert.equal(chooseThemePreference('nord', DEFAULT_THEME), 'nord');
});

test('prefers explicit non-default server setting over stored theme', () => {
  assert.equal(chooseThemePreference('nord', 'dracula'), 'dracula');
});

test('falls back to default when nothing else is set', () => {
  assert.equal(chooseThemePreference(null, null), DEFAULT_THEME);
});

test('auto picks the dark theme when the system prefers dark', () => {
  assert.equal(pickAutoTheme(true, 'solarized-light', 'nord'), 'nord');
});

test('auto picks the light theme when the system prefers light', () => {
  assert.equal(pickAutoTheme(false, 'solarized-light', 'nord'), 'solarized-light');
});

test('auto falls back to the default pair when none is set', () => {
  assert.equal(pickAutoTheme(true), DEFAULT_THEME);
  assert.equal(pickAutoTheme(false), DEFAULT_LIGHT_THEME);
});
