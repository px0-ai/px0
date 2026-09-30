// web/src/theme.js
// A theme is any CSS rule whose whole selector is [data-theme="<id>"], optionally
// prefixed with :root or html. The server joins web/themes/*.css into
// /static/themes.css, so themes are discovered from the loaded stylesheets and
// adding one needs no JavaScript change. See docs/internals/styling-and-themes.md.
import { showToast } from './ui.js';
import { S, apiPostJson } from './state.js';
import { DEFAULT_THEME, DEFAULT_LIGHT_THEME, chooseThemePreference, pickAutoTheme } from './theme-preference.js';

const KEY = 'px0.theme';
const AUTO_KEY = 'px0.autoTheme';
const darkQuery = typeof matchMedia === 'function' ? matchMedia('(prefers-color-scheme: dark)') : null;
const THEME_SELECTOR = /^(?::root|html)?\[data-theme=["']?([\w-]+)["']?\]$/;

export { DEFAULT_THEME, chooseThemePreference };

let themes = null;
let auto = null;

export function getStoredTheme() {
  try { return localStorage.getItem(KEY); } catch { return null; }
}

export function listThemes() {
  if (themes) return themes;
  const found = new Map();
  const walk = rules => {
    for (const r of rules) {
      if (r.styleSheet) { try { walk(r.styleSheet.cssRules); } catch {} continue; } // @import
      if (!r.selectorText) { if (r.cssRules) walk(r.cssRules); continue; }       // @media, @layer
      for (const part of r.selectorText.split(',')) {
        const m = part.trim().match(THEME_SELECTOR);
        if (!m) continue;
        const t = found.get(m[1]) || { id: m[1], name: m[1], scheme: '' };
        const name = r.style.getPropertyValue('--theme-name').trim().replace(/^["']|["']$/g, '');
        const scheme = r.style.getPropertyValue('color-scheme').trim();
        if (name) t.name = name;
        if (scheme) t.scheme = scheme;
        found.set(m[1], t);
      }
    }
  };
  for (const sheet of document.styleSheets) {
    try { walk(sheet.cssRules); } catch {} // cross-origin sheets (web fonts) are unreadable
  }
  themes = [...found.values()].sort((a, b) => a.name.localeCompare(b.name));
  return themes;
}

export const currentTheme = () => document.documentElement.dataset.theme;

export const isAutoTheme = () => !!auto;

function autoPair() {
  const s = S.settings || {};
  return { light: s['workbench.preferredLightColorTheme'], dark: s['workbench.preferredDarkColorTheme'] };
}

export const autoThemeId = () => {
  const p = auto || autoPair();
  return pickAutoTheme(!!darkQuery?.matches, p.light, p.dark);
};

export function autoThemeLabel() {
  const t = listThemes().find(t => t.id === currentTheme());
  return t ? `Auto (${t.name})` : 'Auto';
}

function applyAuto() {
  if (!setTheme(autoThemeId(), false)) setTheme(DEFAULT_THEME, false);
}

export function setAutoTheme(on, pair = autoPair(), persist = true) {
  auto = on ? { light: pair.light || DEFAULT_LIGHT_THEME, dark: pair.dark || DEFAULT_THEME } : null;
  document.documentElement.toggleAttribute('data-theme-auto', on);
  try { on ? localStorage.setItem(AUTO_KEY, JSON.stringify(auto)) : localStorage.removeItem(AUTO_KEY); } catch {}
  if (on) applyAuto();
  if (S.settings) S.settings['window.autoDetectColorScheme'] = on;
  if (persist) void apiPostJson('api/settings', { 'window.autoDetectColorScheme': on }).catch(() => {});
}

darkQuery?.addEventListener('change', () => { if (auto) applyAuto(); });

export function setTheme(id, persist = true) {
  if (!listThemes().some(t => t.id === id)) return false;
  document.documentElement.dataset.theme = id;
  if (persist) {
    const body = { 'workbench.colorTheme': id };
    if (auto) {
      setAutoTheme(false, undefined, false);
      body['window.autoDetectColorScheme'] = false;
    }
    try { localStorage.setItem(KEY, id); } catch {}
    void apiPostJson('api/settings', body).catch(() => {});
  }
  return true;
}

export function cycleTheme() {
  const all = listThemes();
  if (!all.length) return;
  const choices = [{ id: 'auto' }, ...all];
  const i = auto ? 0 : Math.max(0, choices.findIndex(t => t.id === currentTheme()));
  const next = choices[(i + 1) % choices.length];
  if (next.id === 'auto') {
    setAutoTheme(true);
    showToast('Theme', autoThemeLabel());
  } else {
    setTheme(next.id);
    showToast('Theme', next.name);
  }
}

export function initTheme() {
  let pair = null;
  try { pair = JSON.parse(localStorage.getItem(AUTO_KEY)); } catch {}
  if (pair && typeof pair === 'object') return setAutoTheme(true, pair, false);
  const saved = getStoredTheme();
  const preferred = chooseThemePreference(saved, null);
  if (preferred && setTheme(preferred, false)) return;
  if (setTheme(DEFAULT_THEME, false)) return;
  // The attribute in index.html may name a theme that was since removed.
  const all = listThemes();
  if (all.length && !all.some(t => t.id === currentTheme())) setTheme(all[0].id, false);
}
