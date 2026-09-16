// web/src/shortcuts.js
import { $, $$, esc, S, doc_, isMac, MOD, LH, keyCaps } from './state.js';
import { vp, sizer } from './ui.js';
import { layout, render, paint, toggleWordWrap } from './renderer.js';
import { updateStatus } from './status.js';
import { closeTab, switchTab, reopenClosedTab } from './tabs.js';
import { go } from './history.js';
import { clearLink, hovercard, hideHover } from './hover.js';
import { setBlame } from './blame.js';
import { openFind, clearFind, findbar } from './find.js';
import { gotoDefinition, findReferences } from './lsp.js';
import { showRightInspector, hideRightInspector } from './inspector.js';
import { overlay, openPalette, closePalette } from './palette.js';
import { moveCursor, moveCol, moveWord, caretToEdge } from './cursor.js';
import { showCalls } from './calls.js';
import { SEL_KEYS, runSelectionAction, selectAll, clearSelectAll, copySelectAll } from './selbar.js';

import { cycleTheme } from './theme.js';
import { previewing, togglePreview, previewKey, selectPreview } from './markdown.js';
import { toggleDiff } from './diff.js';

/* Each entry lists alternative combos, written as for keyLabel in state.js so
   they show as ⌘/⌥/⇧ on a Mac and Ctrl/Alt/Shift elsewhere. Browsers keep
   Ctrl+W and Cmd+W for themselves, so Alt+W is the close shortcut shown. */
export const SHORTCUTS = [
  [['Mod+K'], 'Quick search / palette'], [['Mod+P'], 'Go to file'],
  [['Mod+Shift+P'], 'Command palette'], [['Mod+Shift+O'], 'Go to symbol'],
  [['Mod+Shift+F'], 'Search in files'], [['Mod+F'], 'Find in file'],
  [['Mod+G'], 'Go to line'], [['Mod+D'], 'Toggle diff view (git)'], [['Alt+Z'], 'Toggle word wrap'],
  [['Alt+M'], 'Toggle Markdown preview'],
  [['Alt+B'], 'Toggle git blame on hover'],
  [['Enter', 'Shift+Enter'], 'Next / previous match'],
  [['F12', 'Mod+Click'], 'Go to definition'], [['Shift+F12'], 'Find all references'],
  [['Alt+Shift+H'], 'Call trail (callers / callees)'],
  [['Mod+J'], 'Toggle right inspector (Symbols/Refs)'],
  [['Alt+Left', 'Alt+Right'], 'Navigate back / forward'], [['Mod+B'], 'Toggle sidebar'],
  [['Alt+W'], 'Close tab'], [['Alt+Shift+T'], 'Reopen closed tab'], [['Ctrl+Tab'], 'Next tab'],
  [['Alt+1…9'], 'Select tab'], [['Double click'], 'Highlight all occurrences'],
  [['Mod+A'], 'Select whole file'],
  [['Alt+C', 'Alt+A'], 'Copy selection ref / for agent'], [['Alt+U'], 'Find usages of selection'],
  [['Alt+E'], 'Edit selection inline'],
  [['Right click'], 'Selection actions at the pointer'],
  [['Mod+Home|Mod+Up', 'Mod+End|Mod+Down'], 'Top / bottom of file'],
  [['Home|Mod+Left', 'End|Mod+Right'], 'Start / end of line'],
  [['Left', 'Right'], 'Move caret along the line'],
  [['Esc'], 'Dismiss'],
];

export function showHelp() {
  const h = $('#helpsheet');
  const ver = S.meta?.version ? ` <span class="help-version">v${esc(S.meta.version)}</span>` : '';
  h.innerHTML = '<div class="help-card"><div class="help-header"><h2>Keyboard Shortcuts</h2>' + ver + '</div><dl class="help-grid">' +
    SHORTCUTS.map(([combos, v]) =>
      '<dt>' + combos.map(keyCaps).filter(Boolean).join('<span class="key-or">/</span>') + '</dt>' +
      '<dd>' + esc(v) + '</dd>').join('') + '</dl></div>';
  h.hidden = false;
}

export const inField = el => el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA');

export function initShortcuts() {
  $('#btn-theme')?.addEventListener('click', cycleTheme);
  $('#btn-help')?.addEventListener('click', showHelp);
  $('#st-ver')?.addEventListener('click', showHelp);
  $('#helpsheet').addEventListener('click', () => { $('#helpsheet').hidden = true; });

  // Footer quick action buttons
  $('#footer-actions')?.addEventListener('click', e => {
    const btn = e.target.closest('.footer-btn');
    if (!btn) return;
    const act = btn.dataset.action;
    if (act === 'quick-open') openPalette('file');
    else if (act === 'search') { showRightInspector('search'); $('#q')?.select(); }
    else if (act === 'symbols') openPalette('symbol');
    else if (act === 'find') openFind(S.lastWord);
    else if (act === 'goto') openPalette('line');
    else if (act === 'wrap') toggleWordWrap();
    else if (act === 'md-preview') togglePreview();
    else if (act === 'blame') { if (S.meta?.git) { setBlame(!S.blame); if (!S.blame) hideHover(); } }
    else if (act === 'palette') openPalette('command');
    else if (act === 'help') showHelp();
  });

  addEventListener('keydown', e => {
    const mod = e[MOD];

    if (e.key === 'Escape') {
      if (!overlay.hidden) { closePalette(); return; }
      if (!$('#helpsheet').hidden) { $('#helpsheet').hidden = true; return; }
      if (!hovercard.hidden) { clearLink(); return; }
      if (!findbar.hidden) { clearFind(); return; }
      if (S.selAll) { clearSelectAll(); return; }
      if (!document.body.classList.contains('right-hidden')) { hideRightInspector(); return; }
      if (S.occ) { S.occ = null; paint(); return; }
      if (inField(document.activeElement)) document.activeElement.blur();
      return;
    }

    // Universal Quick Open / Command Palette: Cmd+K / Ctrl+K
    if (mod && (e.key === 'k' || e.key === 'K')) {
      e.preventDefault();
      openPalette(e.shiftKey ? 'command' : 'file');
      return;
    }

    if (mod && (e.key === 'j' || e.key === 'J')) {
      e.preventDefault();
      if (document.body.classList.contains('right-hidden')) showRightInspector('refs');
      else hideRightInspector();
      return;
    }

    if (mod && e.shiftKey && (e.key === 'P' || e.key === 'p')) { e.preventDefault(); openPalette('command'); return; }
    if (mod && e.shiftKey && (e.key === 'O' || e.key === 'o')) { e.preventDefault(); showRightInspector('symbols'); return; }
    if (mod && e.shiftKey && (e.key === 'F' || e.key === 'f')) { e.preventDefault(); showRightInspector('search'); $('#q')?.select(); return; }
    if (mod && !e.shiftKey && (e.key === 'p' || e.key === 'P')) { e.preventDefault(); openPalette('file'); return; }
    if (mod && (e.key === 'g' || e.key === 'G')) { e.preventDefault(); openPalette('line'); return; }
    if (mod && (e.key === 'f' || e.key === 'F')) { e.preventDefault(); openFind(S.lastWord); return; }
    if (mod && (e.key === 'b' || e.key === 'B')) { e.preventDefault(); document.body.classList.toggle('side-hidden'); layout(); render(); return; }
    // Diff view of the open file (git only; fails quiet when git is off).
    if (mod && !e.shiftKey && (e.key === 'd' || e.key === 'D')) { if (S.meta?.git) { e.preventDefault(); toggleDiff(); } return; }
    // Blame on hover (git only; fails quiet when git is off).
    if (e.altKey && e.code === 'KeyB') {
      if (S.meta?.git) { e.preventDefault(); setBlame(!S.blame); if (!S.blame) hideHover(); }
      return;
    }
    // Alt shortcuts match e.code: on a Mac, Option+letter types a symbol, so e.key is not the letter.
    if ((mod && (e.key === 'w' || e.key === 'W')) || (e.altKey && e.code === 'KeyW')) {
      e.preventDefault();
      e.stopPropagation();
      if (S.active >= 0) closeTab(S.active);
      return;
    }
    if (e.altKey && e.shiftKey && !mod && e.code === 'KeyT') { e.preventDefault(); reopenClosedTab(); return; }
    if (e.key === 'F12') {
      e.preventDefault();
      if (e.shiftKey) findReferences(); else gotoDefinition();
      return;
    }
    if (e.altKey && e.key === 'ArrowLeft') { e.preventDefault(); go(-1); return; }
    if (e.altKey && e.key === 'ArrowRight') { e.preventDefault(); go(1); return; }
    if (e.ctrlKey && e.key === 'Tab') {
      e.preventDefault();
      if (S.tabs.length > 1) switchTab((S.active + (e.shiftKey ? -1 : 1) + S.tabs.length) % S.tabs.length);
      return;
    }
    if (e.altKey && e.shiftKey && e.code === 'KeyH') { e.preventDefault(); showCalls(); return; }
    if (e.altKey && !mod && !e.shiftKey && /^Digit[1-9]$/.test(e.code)) { e.preventDefault(); switchTab(+e.code.slice(5) - 1); return; }
    // Selection actions, live only while the status bar is showing them.
    if (e.altKey && !mod && !e.shiftKey && SEL_KEYS[e.code] && runSelectionAction(SEL_KEYS[e.code])) { e.preventDefault(); return; }
    if (e.altKey && e.code === 'KeyZ') {
      e.preventDefault();
      toggleWordWrap();
      return;
    }

    if (e.altKey && !mod && !e.shiftKey && e.code === 'KeyM') {
      e.preventDefault();
      togglePreview();
      return;
    }

    if (inField(document.activeElement)) return;

    // Select all takes the open file only, never the sidebar or status bar around it.
    const plainMod = mod && !e.shiftKey && !e.altKey;
    if (plainMod && (e.key === 'a' || e.key === 'A')) { e.preventDefault(); if (previewing()) selectPreview(); else selectAll(); return; }
    if (plainMod && (e.key === 'c' || e.key === 'C') && copySelectAll()) { e.preventDefault(); return; }

    if (e.key === '?') { e.preventDefault(); showHelp(); return; }
    const d = doc_();
    if (!d) return;
    if (previewing(d)) { if (previewKey(e)) e.preventDefault(); return; }
    const toTop = () => { vp.scrollTop = 0; d.cur = 1; render(); updateStatus(); };
    const toBottom = () => { vp.scrollTop = sizer.offsetHeight; d.cur = d.total; render(); updateStatus(); };
    const shift = e.shiftKey;
    if (mod && e.key === 'Home') { e.preventDefault(); if (shift) caretToEdge(false, true); else toTop(); return; }
    if (mod && e.key === 'End') { e.preventDefault(); if (shift) caretToEdge(true, true); else toBottom(); return; }
    // A Mac keyboard has no Home or End: Cmd with the arrows does their job there.
    if (isMac && mod && e.key === 'ArrowUp') { e.preventDefault(); toTop(); return; }
    if (isMac && mod && e.key === 'ArrowDown') { e.preventDefault(); toBottom(); return; }
    if (isMac && mod && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) { e.preventDefault(); caretToEdge(e.key === 'ArrowRight', shift); return; }
    if (mod && !isMac && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) { e.preventDefault(); moveWord(e.key === 'ArrowRight' ? 1 : -1, shift); return; }
    if (isMac && e.altKey && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) { e.preventDefault(); moveWord(e.key === 'ArrowRight' ? 1 : -1, shift); return; }
    if (!mod && (e.key === 'ArrowDown' || e.key === 'j')) { e.preventDefault(); moveCursor(1, shift); return; }
    if (!mod && (e.key === 'ArrowUp' || e.key === 'k')) { e.preventDefault(); moveCursor(-1, shift); return; }
    if (!mod && !e.altKey && e.key === 'ArrowLeft') { e.preventDefault(); moveCol(-1, shift); return; }
    if (!mod && !e.altKey && e.key === 'ArrowRight') { e.preventDefault(); moveCol(1, shift); return; }
    if (!mod && (e.key === 'Home' || e.key === 'End')) { e.preventDefault(); caretToEdge(e.key === 'End', shift); return; }
    if (e.key === 'PageDown') { e.preventDefault(); moveCursor(Math.floor(vp.clientHeight / LH) - 2, shift); return; }
    if (e.key === 'PageUp') { e.preventDefault(); moveCursor(-(Math.floor(vp.clientHeight / LH) - 2), shift); return; }
  }, { capture: true });


}
