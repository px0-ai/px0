// web/src/palette.js
import { $, esc, S, doc_, api, debounce, withKeys } from './state.js';
import { render, toggleWordWrap, toggleLineNumbers } from './renderer.js';
import { openFile, centerLine, closeTab, reopenClosedTab } from './tabs.js';
import { updateStatus } from './status.js';
import { pushHistory } from './history.js';
import { showPanel } from './panels.js';
import { openFind } from './find.js';
import { gotoDefinition, findReferences } from './lsp.js';
import { revealFile } from './tree.js';
import { showRightInspector, hideRightInspector } from './inspector.js';
import { showCalls, openLspSetup } from './calls.js';
import { showHelp } from './shortcuts.js';
import { listThemes, currentTheme, setTheme, cycleTheme } from './theme.js';
import { togglePreview } from './markdown.js';
import { toggleVim } from './vim.js';

export const overlay = $('#overlay');
export const palInput = $('#pal');
export const palList = $('#pal-list');
export let pal = null;

export const COMMANDS = [
  { name: 'Go to File…', run: () => openPalette('file') },
  { name: 'Go to Symbol in File…', run: () => openPalette('symbol') },
  { name: 'Go to Line…', run: () => openPalette('line') },
  { name: 'Search in Files', run: () => showPanel('search') },
  { name: 'Find in Current File', run: () => openFind(S.lastWord) },
  { name: 'Go to Definition', run: () => gotoDefinition() },
  { name: 'Find All References (Right Panel)', run: () => findReferences() },
  { name: withKeys('Show Call Trail: Callers / Callees ({Alt+Shift+H})'), run: () => showCalls() },
  { name: 'Set Up Language Server…', run: () => openLspSetup() },
  { name: 'Toggle Right Inspector (Symbols & References)', run: () => {
    if (document.body.classList.contains('right-hidden')) showRightInspector('refs');
    else hideRightInspector();
  } },
  { name: 'Show File Symbols (Right Panel)', run: () => showRightInspector('symbols') },
  { name: 'Reveal Active File in Explorer', run: () => { const d = doc_(); if (d) { showPanel('files'); revealFile(d.path); } } },
  { name: withKeys('Toggle Word Wrap ({Alt+Z})'), run: () => toggleWordWrap() },
  { name: withKeys('Toggle Line Numbers ({Alt+L})'), run: () => toggleLineNumbers() },
  { name: 'Toggle Vim Keybindings', run: () => toggleVim() },
  { name: withKeys('Toggle Markdown Preview ({Alt+M})'), run: () => togglePreview() },
  { name: withKeys('Toggle Sidebar ({Mod+B})'), run: () => document.body.classList.toggle('side-hidden') },
  { name: 'Select Theme…', run: () => openPalette('theme') },
  { name: 'Next Theme', run: cycleTheme },
  { name: 'Re-index Workspace', run: () => $('#btn-reindex').click() },
  { name: 'Close Tab', run: () => { if (S.active >= 0) closeTab(S.active); } },
  { name: 'Close All Tabs', run: () => { while (S.tabs.length) closeTab(0); } },
  { name: withKeys('Reopen Closed Tab ({Alt+Shift+T})'), run: () => reopenClosedTab() },
  { name: 'Keyboard Shortcuts', run: showHelp },
];

export const PAL_MODES = {
  file: { tag: 'File', hint: 'Type to fuzzy-match any file. Prefix : for a line, @ for a symbol, > for a command.' },
  symbol: { tag: 'Symbol', hint: 'Symbols in the active file.' },
  line: { tag: 'Line', hint: 'Enter a line number.' },
  command: { tag: 'Command', hint: '' },
  theme: { tag: 'Theme', hint: 'Arrows preview a theme. Enter keeps it, Esc restores the previous one.' },
};

export function openPalette(mode, seed) {
  pal = { mode, items: [], sel: 0, restoreTheme: mode === 'theme' ? currentTheme() : null };
  overlay.hidden = false;
  palInput.value = seed !== undefined ? seed : ({ symbol: '@', line: ':', command: '>' }[mode] || '');
  $('#pal-mode').textContent = PAL_MODES[mode].tag;
  $('#pal-hint').textContent = PAL_MODES[mode].hint;
  palInput.focus();
  palInput.setSelectionRange(palInput.value.length, palInput.value.length);
  refreshPalette();
}

export function closePalette() {
  overlay.hidden = true;
  if (pal && pal.restoreTheme) setTheme(pal.restoreTheme, false); // dismissed mid-preview
  pal = null;
}

export const refreshPalette = debounce(async () => {
  if (!pal) return;
  let raw = palInput.value;
  let mode = pal.mode === 'theme' ? 'theme' : 'file';
  if (mode === 'theme') { /* no prefixes: the query is a theme name */ }
  else if (raw.startsWith('>')) { mode = 'command'; raw = raw.slice(1); }
  else if (raw.startsWith('@')) { mode = 'symbol'; raw = raw.slice(1); }
  else if (raw.startsWith(':')) { mode = 'line'; raw = raw.slice(1); }
  pal.mode = mode;
  $('#pal-mode').textContent = PAL_MODES[mode].tag;
  $('#pal-hint').textContent = PAL_MODES[mode].hint;
  const q = raw.trim();

  if (mode === 'line') {
    const d = doc_();
    const n = parseInt(q, 10);
    pal.items = (d && n > 0) ? [{ kind: 'line', n: Math.min(n, d.total), label: 'Line ' + Math.min(n, d.total), sub: d.path }] : [];
  } else if (mode === 'command') {
    const lq = q.toLowerCase();
    pal.items = COMMANDS.filter(c => c.name.toLowerCase().includes(lq)).map(c => ({ kind: 'cmd', cmd: c, label: c.name, sub: '' }));
  } else if (mode === 'symbol') {
    const d = doc_();
    if (d && !d.outline) { try { d.outline = (await api('/api/outline', { path: d.path })).symbols || []; } catch { d.outline = []; } }
    const lq = q.toLowerCase();
    pal.items = ((d && d.outline) || []).filter(s => !lq || s.name.toLowerCase().includes(lq))
      .slice(0, 400).map(s => ({ kind: 'sym', n: s.line, label: s.name, sub: s.kind, right: String(s.line) }));
  } else if (mode === 'theme') {
    const lq = q.toLowerCase();
    pal.items = listThemes().filter(t => (t.name + ' ' + t.id).toLowerCase().includes(lq))
      .map(t => ({ kind: 'theme', id: t.id, label: t.name, sub: t.scheme, right: t.id === pal.restoreTheme ? 'current' : '' }));
  } else {
    let j;
    try { j = await api('/api/find', { q, limit: 120 }); } catch { return; }
    pal.items = j.results.map(r => {
      const cut = r.path.length - r.name.length;
      return {
        kind: 'file', path: r.path,
        label: fuzzyHTML(r.path.slice(cut), (r.pos || []).filter(p => p >= cut).map(p => p - cut)),
        sub: fuzzyHTML(r.path.slice(0, Math.max(0, cut - 1)), (r.pos || []).filter(p => p < cut)),
        raw: true,
      };
    });
  }
  pal.sel = mode === 'theme' ? Math.max(0, pal.items.findIndex(it => it.id === currentTheme())) : 0;
  drawPalette();
}, 40);

export function fuzzyHTML(text, pos) {
  if (!pos || !pos.length) return esc(text);
  const set = new Set(pos);
  let out = '', open = false;
  for (let i = 0; i < text.length; i++) {
    const hit = set.has(i);
    if (hit && !open) { out += '<b>'; open = true; }
    if (!hit && open) { out += '</b>'; open = false; }
    out += esc(text[i]);
  }
  return out + (open ? '</b>' : '');
}

export function drawPalette() {
  if (!pal) return;
  if (!pal.items.length) { palList.innerHTML = '<div class="pi"><span class="pp">No matches</span></div>'; return; }
  palList.innerHTML = pal.items.map((it, i) =>
    '<div class="pi' + (i === pal.sel ? ' sel' : '') + '" data-i="' + i + '">' +
    '<span class="pn">' + (it.raw ? it.label : esc(it.label)) + '</span>' +
    '<span class="pp">' + (it.raw ? it.sub : esc(it.sub || '')) + '</span>' +
    (it.right ? '<span class="pr">' + esc(it.right) + '</span>' : '') + '</div>').join('');
  const s = palList.children[pal.sel];
  if (s) s.scrollIntoView({ block: 'nearest' });
  if (pal.mode === 'theme') setTheme(pal.items[pal.sel].id, false); // live preview
}

export function movePalette(delta) {
  if (!pal || !pal.items.length) return;
  pal.sel = (pal.sel + delta + pal.items.length) % pal.items.length;
  drawPalette();
}

export function acceptPalette() {
  if (!pal || !pal.items.length) return;
  const it = pal.items[pal.sel];
  if (it.kind === 'theme') pal.restoreTheme = null;
  closePalette();
  if (it.kind === 'file') openFile(it.path);
  else if (it.kind === 'sym' || it.kind === 'line') {
    const d = doc_(); if (!d) return;
    d.cur = it.n; centerLine(it.n); render(); updateStatus(); pushHistory(d.path, it.n);
  } else if (it.kind === 'cmd') it.cmd.run();
  else if (it.kind === 'theme') setTheme(it.id);
}

export function initPalette() {
  palInput.addEventListener('input', refreshPalette);
  palInput.addEventListener('keydown', e => {
    if (e.key === 'ArrowDown' || (e.ctrlKey && e.key === 'n')) { e.preventDefault(); movePalette(1); }
    else if (e.key === 'ArrowUp' || (e.ctrlKey && e.key === 'p')) { e.preventDefault(); movePalette(-1); }
    else if (e.key === 'Enter') { e.preventDefault(); acceptPalette(); }
    else if (e.key === 'Escape') { e.preventDefault(); closePalette(); }
    else if (e.key === 'Tab') { e.preventDefault(); movePalette(e.shiftKey ? -1 : 1); }
  });
  palList.addEventListener('click', e => {
    const p = e.target.closest('.pi');
    if (p && p.dataset.i !== undefined) { pal.sel = +p.dataset.i; acceptPalette(); }
  });
  overlay.addEventListener('mousedown', e => { if (e.target === overlay) closePalette(); });
}
