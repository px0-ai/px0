// web/src/theme.js
// A theme is any CSS rule whose whole selector is [data-theme="<id>"], optionally
// prefixed with :root or html. The server joins web/themes/*.css into
// /static/themes.css, so themes are discovered from the loaded stylesheets and
// adding one needs no JavaScript change. See docs/internals/styling-and-themes.md.
import { showToast } from './ui.js';

const KEY = 'px0.theme';
const DEFAULT_THEME = 'github-dark';
const THEME_SELECTOR = /^(?::root|html)?\[data-theme=["']?([\w-]+)["']?\]$/;

let themes = null;

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

export function setTheme(id, persist = true) {
  if (!listThemes().some(t => t.id === id)) return false;
  document.documentElement.dataset.theme = id;
  if (persist) { try { localStorage.setItem(KEY, id); } catch {} }
  return true;
}

export function cycleTheme() {
  const all = listThemes();
  if (!all.length) return;
  const next = all[(all.findIndex(t => t.id === currentTheme()) + 1) % all.length];
  setTheme(next.id);
  showToast('Theme', next.name);
}

export function initTheme() {
  let saved = null;
  try { saved = localStorage.getItem(KEY); } catch {}
  if (saved && setTheme(saved, false)) return;
  if (setTheme(DEFAULT_THEME, false)) return;
  // The attribute in index.html may name a theme that was since removed.
  const all = listThemes();
  if (all.length && !all.some(t => t.id === currentTheme())) setTheme(all[0].id, false);
}
