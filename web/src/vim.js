// web/src/vim.js
import { $, esc, S, doc_, keyCaps, LH } from './state.js';
import { vp, sizer, copyToClipboard, showToast } from './ui.js';
import { render, paint, rowFor, placeCaret } from './renderer.js';
import { moveCursor, moveCol, moveWord, caretToEdge, updateDomSelection, clearSelection, revealCaretX } from './cursor.js';
import { updateStatus } from './status.js';
import { gotoDefinition, findReferences } from './lsp.js';
import { go, pushHistory } from './history.js';
import { openFind, findNextMatch, clearFind } from './find.js';
import { showHover } from './hover.js';
import { showCalls } from './calls.js';
import { switchTab, closeTab } from './tabs.js';
import { getSelectedRangeInfo, runSelectionAction } from './selbar.js';
import { openPalette } from './palette.js';
import { showHelp } from './shortcuts.js';

let vimEnabled = false;
let vimMode = 'NORMAL'; // 'NORMAL' | 'VISUAL' | 'VISUAL_LINE'
let vimCount = '';
let vimPending = '';
let vimPendingTimer = null;

const WORD_RE = /[A-Za-z0-9_$]/;

export function isVimEnabled() {
  return vimEnabled;
}

export function getVimMode() {
  return vimMode;
}

export function setVimModeEnabled(enabled, persist = true) {
  vimEnabled = !!enabled;
  if (!vimEnabled) {
    exitVisualMode();
    resetVimState();
  }
  document.body.classList.toggle('vim-mode-enabled', vimEnabled);
  updateVimCaret();
  updateVimStatus();

  const chip = $('#st-vim');
  if (chip) chip.hidden = !vimEnabled;
  const helpBtn = $('#btn-vim-help');
  if (helpBtn) helpBtn.hidden = !vimEnabled;

  if (persist) {
    try {
      localStorage.setItem('px0.editor.vimMode', vimEnabled ? 'true' : 'false');
    } catch {}
    if (S.settings) S.settings['editor.vimMode'] = vimEnabled;
  }
}

function updateVimCaret() {
  if (!vimEnabled) {
    document.body.classList.remove('vim-normal-caret');
    return;
  }
  document.body.classList.toggle('vim-normal-caret', vimMode === 'NORMAL');
}

export function resetVimState() {
  vimCount = '';
  vimPending = '';
  if (vimPendingTimer) {
    clearTimeout(vimPendingTimer);
    vimPendingTimer = null;
  }
  updateVimStatus();
}

function setVimPending(key) {
  vimPending = key;
  if (vimPendingTimer) clearTimeout(vimPendingTimer);
  vimPendingTimer = setTimeout(() => {
    resetVimState();
  }, 1400);
  updateVimStatus();
}

function getCount() {
  const c = parseInt(vimCount, 10);
  return (isNaN(c) || c <= 0) ? 1 : c;
}

function updateVimStatus() {
  const chip = $('#st-vim');
  if (!chip) return;
  chip.hidden = !vimEnabled;
  if (!vimEnabled) return;

  chip.className = 'status-vim-chip';
  let modeLabel = vimMode;
  if (vimMode === 'VISUAL_LINE') {
    chip.classList.add('mode-visual-line');
    modeLabel = 'V-LINE';
  } else if (vimMode === 'VISUAL') {
    chip.classList.add('mode-visual');
    modeLabel = 'VISUAL';
  } else {
    chip.classList.add('mode-normal');
    modeLabel = 'NORMAL';
  }

  let extra = '';
  if (vimCount) extra += vimCount;
  if (vimPending) extra += vimPending;

  if (extra) {
    chip.innerHTML = esc(modeLabel) + ' <span class="status-vim-pending">' + esc(extra) + '</span>';
  } else {
    chip.textContent = modeLabel;
  }
}

export function wordAtCaret() {
  const d = doc_();
  if (!d) return null;
  const row = rowFor(d.cur);
  if (!row) return S.at || null;
  const code = row.querySelector('.c');
  if (!code) return S.at || null;
  const full = code.textContent;
  let col = Math.min(d.col === Infinity ? full.length : (d.col || 0), full.length);
  if (col >= full.length && col > 0) col = full.length - 1;
  let a = col, b = col;
  if (full[a] && WORD_RE.test(full[a])) {
    while (a > 0 && WORD_RE.test(full[a - 1])) a--;
    while (b < full.length && WORD_RE.test(full[b])) b++;
    if (a < b) return { word: full.slice(a, b), line: d.cur, col: a, path: d.path };
  }
  return S.at || null;
}

export function showHoverForCaret() {
  const at = wordAtCaret();
  if (!at) return;
  const caret = $('#caret');
  let x = 120, y = 120;
  if (caret) {
    const r = caret.getBoundingClientRect();
    x = Math.max(16, r.left);
    y = r.bottom + 4;
  }
  showHover(at, x, y);
}

function enterVisualMode(lineWise = false) {
  const d = doc_();
  if (!d) return;
  vimMode = lineWise ? 'VISUAL_LINE' : 'VISUAL';
  const row = rowFor(d.cur);
  const len = row ? row.querySelector('.c')?.textContent.length || 0 : 0;
  if (lineWise) {
    d.selAnchor = { line: d.cur, col: 0 };
    d.col = len;
  } else {
    if (!d.selAnchor) {
      const col = d.col === Infinity ? len : (d.col || 0);
      d.selAnchor = { line: d.cur, col };
    }
  }
  placeCaret();
  updateDomSelection();
  updateVimCaret();
  updateVimStatus();
}

function exitVisualMode() {
  const d = doc_();
  vimMode = 'NORMAL';
  if (d) clearSelection(d);
  resetVimState();
  updateVimCaret();
  updateVimStatus();
}

function ensureLineSelection() {
  const d = doc_();
  if (!d || vimMode !== 'VISUAL_LINE') return;
  if (!d.selAnchor) d.selAnchor = { line: d.cur, col: 0 };
  const row = rowFor(d.cur);
  d.col = row ? row.querySelector('.c')?.textContent.length || 0 : 0;
  placeCaret();
  updateDomSelection();
}

export function handleVimKeyDown(e) {
  if (!vimEnabled) return false;

  const active = /** @type {HTMLElement|null} */ (document.activeElement);
  if (active && (active.tagName === 'INPUT' || active.tagName === 'TEXTAREA' || active.isContentEditable)) {
    return false;
  }

  // If helpsheet or overlay palette is open, let Esc close them
  if (e.key === 'Escape') {
    if (vimMode !== 'NORMAL') {
      e.preventDefault();
      exitVisualMode();
      return true;
    }
    if (vimPending || vimCount) {
      e.preventDefault();
      resetVimState();
      return true;
    }
    return false;
  }

  const d = doc_();
  if (!d) return false;

  const isVisual = vimMode === 'VISUAL' || vimMode === 'VISUAL_LINE';

  // Handling Ctrl combos in Normal / Visual mode
  if (e.ctrlKey && !e.altKey && !e.metaKey) {
    if (e.key === 'd') {
      e.preventDefault();
      const half = Math.max(1, Math.floor((vp.clientHeight / LH) / 2)) * getCount();
      moveCursor(half, isVisual);
      if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      resetVimState();
      return true;
    }
    if (e.key === 'u') {
      e.preventDefault();
      const half = Math.max(1, Math.floor((vp.clientHeight / LH) / 2)) * getCount();
      moveCursor(-half, isVisual);
      if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      resetVimState();
      return true;
    }
    if (e.key === 'f') {
      e.preventDefault();
      const page = Math.max(1, Math.floor(vp.clientHeight / LH) - 2) * getCount();
      moveCursor(page, isVisual);
      if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      resetVimState();
      return true;
    }
    if (e.key === 'b') {
      e.preventDefault();
      const page = Math.max(1, Math.floor(vp.clientHeight / LH) - 2) * getCount();
      moveCursor(-page, isVisual);
      if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      resetVimState();
      return true;
    }
    if (e.key === 'o') {
      e.preventDefault();
      go(-getCount());
      resetVimState();
      return true;
    }
    if (e.key === 'i') {
      e.preventDefault();
      go(getCount());
      resetVimState();
      return true;
    }
  }

  // Avoid intercepting browser modifiers like Cmd/Alt
  if (e.metaKey || e.altKey) return false;

  // VISUAL MODE ACTIONS
  if (isVisual) {
    if (e.key === 'v' && !e.shiftKey) {
      e.preventDefault();
      if (vimMode === 'VISUAL') exitVisualMode();
      else enterVisualMode(false);
      return true;
    }
    if (e.key === 'V') {
      e.preventDefault();
      if (vimMode === 'VISUAL_LINE') exitVisualMode();
      else enterVisualMode(true);
      return true;
    }
    if (e.key === 'y') {
      e.preventDefault();
      const info = getSelectedRangeInfo();
      if (info && info.text) {
        const lineCount = info.l2 - info.l1 + 1;
        copyToClipboard(info.text, 'Yanked ' + (lineCount === 1 ? '1 line' : lineCount + ' lines'));
      }
      exitVisualMode();
      return true;
    }
    if (e.key === 'Y') {
      e.preventDefault();
      runSelectionAction('copy-ref');
      exitVisualMode();
      return true;
    }
    if (e.key === 'e' || e.key === 'c') {
      e.preventDefault();
      runSelectionAction('agent-edit');
      exitVisualMode();
      return true;
    }
    if (e.key === 'u') {
      e.preventDefault();
      runSelectionAction('usages');
      exitVisualMode();
      return true;
    }
  }

  // ACCUMULATE COUNT PREFIX (1-9, or 0 when count > 0)
  if (!vimPending && /^[0-9]$/.test(e.key)) {
    if (e.key === '0' && !vimCount) {
      // '0' with no prior digits means jump to beginning of line!
    } else {
      e.preventDefault();
      vimCount += e.key;
      updateVimStatus();
      return true;
    }
  }

  // MULTI-KEY PREFIXES (g, z)
  if (vimPending === 'g') {
    e.preventDefault();
    if (e.key === 'g') {
      // gg: Top of document, or line [count]
      const count = parseInt(vimCount, 10);
      if (!isNaN(count) && count > 0) {
        d.cur = Math.max(1, Math.min(d.total, count));
        d.col = 0;
        const y = (d.cur - 1) * LH;
        vp.scrollTop = Math.max(0, y - LH * 3);
        render(); updateStatus();
      } else {
        vp.scrollTop = 0;
        d.cur = 1;
        d.col = 0;
        render(); updateStatus();
      }
      if (isVisual) {
        placeCaret();
        updateDomSelection();
        if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      }
    } else if (e.key === 'd') {
      // gd: Go to definition
      const w = wordAtCaret();
      if (w) {
        pushHistory(d.path, d.cur);
        gotoDefinition(w);
      }
    } else if (e.key === 'r') {
      // gr: Find references
      findReferences();
    } else if (e.key === 'h') {
      // gh: Call trail
      showCalls();
    } else if (e.key === 't') {
      // gt: Next tab
      const count = parseInt(vimCount, 10);
      if (!isNaN(count) && count > 0 && count <= S.tabs.length) {
        switchTab(count - 1);
      } else if (S.tabs.length > 1) {
        switchTab((S.active + 1) % S.tabs.length);
      }
    } else if (e.key === 'T') {
      // gT: Previous tab
      if (S.tabs.length > 1) {
        switchTab((S.active - 1 + S.tabs.length) % S.tabs.length);
      }
    }
    resetVimState();
    return true;
  }

  if (vimPending === 'z') {
    e.preventDefault();
    if (e.key === 'z') {
      // zz: Center current line in viewport
      vp.scrollTop = Math.max(0, (d.cur - 1) * LH - (vp.clientHeight - LH) / 2);
      render(); updateStatus();
    } else if (e.key === 't') {
      // zt: Scroll current line to top of viewport
      vp.scrollTop = Math.max(0, (d.cur - 1) * LH);
      render(); updateStatus();
    } else if (e.key === 'b') {
      // zb: Scroll current line to bottom of viewport
      vp.scrollTop = Math.max(0, (d.cur - 1) * LH - vp.clientHeight + LH * 2);
      render(); updateStatus();
    }
    resetVimState();
    return true;
  }

  // SINGLE KEY COMMANDS
  const count = getCount();

  switch (e.key) {
    case 'h': {
      e.preventDefault();
      moveCol(-count, isVisual);
      resetVimState();
      return true;
    }
    case 'l': {
      e.preventDefault();
      moveCol(count, isVisual);
      resetVimState();
      return true;
    }
    case 'j': {
      e.preventDefault();
      moveCursor(count, isVisual);
      if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      resetVimState();
      return true;
    }
    case 'k': {
      e.preventDefault();
      moveCursor(-count, isVisual);
      if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      resetVimState();
      return true;
    }
    case 'w': {
      e.preventDefault();
      moveWord(count, isVisual);
      if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      resetVimState();
      return true;
    }
    case 'b': {
      e.preventDefault();
      moveWord(-count, isVisual);
      if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      resetVimState();
      return true;
    }
    case '0': {
      e.preventDefault();
      caretToEdge(false, isVisual);
      resetVimState();
      return true;
    }
    case '$': {
      e.preventDefault();
      caretToEdge(true, isVisual);
      resetVimState();
      return true;
    }
    case '^': {
      e.preventDefault();
      const row = rowFor(d.cur);
      const text = row ? row.querySelector('.c')?.textContent || '' : '';
      const idx = text.search(/\S/);
      d.col = idx >= 0 ? idx : 0;
      revealCaretX(placeCaret());
      if (isVisual) updateDomSelection();
      resetVimState();
      return true;
    }
    case 'G': {
      e.preventDefault();
      const targetLine = vimCount ? parseInt(vimCount, 10) : d.total;
      d.cur = Math.max(1, Math.min(d.total, targetLine));
      d.col = 0;
      const y = (d.cur - 1) * LH;
      vp.scrollTop = Math.max(0, y - LH * 3);
      render(); updateStatus();
      if (isVisual) {
        placeCaret();
        updateDomSelection();
        if (vimMode === 'VISUAL_LINE') ensureLineSelection();
      }
      resetVimState();
      return true;
    }
    case 'g': {
      e.preventDefault();
      setVimPending('g');
      return true;
    }
    case 'z': {
      e.preventDefault();
      setVimPending('z');
      return true;
    }
    case 'H': {
      e.preventDefault();
      const topL = Math.floor(vp.scrollTop / LH) + 1;
      d.cur = Math.max(1, Math.min(d.total, topL));
      render(); updateStatus();
      if (isVisual) { placeCaret(); updateDomSelection(); if (vimMode === 'VISUAL_LINE') ensureLineSelection(); }
      resetVimState();
      return true;
    }
    case 'M': {
      e.preventDefault();
      const midL = Math.floor((vp.scrollTop + vp.clientHeight / 2) / LH) + 1;
      d.cur = Math.max(1, Math.min(d.total, midL));
      render(); updateStatus();
      if (isVisual) { placeCaret(); updateDomSelection(); if (vimMode === 'VISUAL_LINE') ensureLineSelection(); }
      resetVimState();
      return true;
    }
    case 'L': {
      e.preventDefault();
      const botL = Math.floor((vp.scrollTop + vp.clientHeight - LH) / LH);
      d.cur = Math.max(1, Math.min(d.total, botL));
      render(); updateStatus();
      if (isVisual) { placeCaret(); updateDomSelection(); if (vimMode === 'VISUAL_LINE') ensureLineSelection(); }
      resetVimState();
      return true;
    }
    case 'K': {
      e.preventDefault();
      showHoverForCaret();
      resetVimState();
      return true;
    }
    case '/': {
      e.preventDefault();
      openFind();
      resetVimState();
      return true;
    }
    case '?': {
      e.preventDefault();
      openFind();
      findNextMatch(-1);
      resetVimState();
      return true;
    }
    case 'n': {
      e.preventDefault();
      findNextMatch(1);
      resetVimState();
      return true;
    }
    case 'N': {
      e.preventDefault();
      findNextMatch(-1);
      resetVimState();
      return true;
    }
    case '*': {
      e.preventDefault();
      const w = wordAtCaret();
      if (w && w.word) {
        S.at = w;
        S.lastWord = w.word;
        S.occ = w.word;
        paint();
        openFind(w.word);
        findNextMatch(1);
      }
      resetVimState();
      return true;
    }
    case '#': {
      e.preventDefault();
      const w = wordAtCaret();
      if (w && w.word) {
        S.at = w;
        S.lastWord = w.word;
        S.occ = w.word;
        paint();
        openFind(w.word);
        findNextMatch(-1);
      }
      resetVimState();
      return true;
    }
    case 'v': {
      e.preventDefault();
      enterVisualMode(false);
      resetVimState();
      return true;
    }
    case 'V': {
      e.preventDefault();
      enterVisualMode(true);
      resetVimState();
      return true;
    }
    case ':': {
      e.preventDefault();
      openPalette('command');
      resetVimState();
      return true;
    }
  }

  return false;
}

export const VIM_SHORTCUT_SECTIONS = [
  {
    title: 'Modes & Motions',
    items: [
      [['h', 'j', 'k', 'l'], 'Move left, down, up, right'],
      [['w', 'b'], 'Next / previous word boundary'],
      [['0', '^', '$'], 'Start of line / first non-blank / end of line'],
      [['gg', 'G'], 'First line / last line (or [count]gg / [count]G)'],
      [['Ctrl+d', 'Ctrl+u'], 'Scroll half-page down / up'],
      [['Ctrl+f', 'Ctrl+b'], 'Scroll full-page down / up'],
      [['zz', 'zt', 'zb'], 'Center line / line to top / line to bottom'],
      [['H', 'M', 'L'], 'Move to top, middle, bottom visible line'],
    ]
  },
  {
    title: 'Code Intelligence & LSP',
    items: [
      [['gd'], 'Go to Definition (replaces F12)'],
      [['gr'], 'Find References across workspace (replaces Shift+F12)'],
      [['K'], 'Show hover documentation & signatures'],
      [['gh'], 'Call Trail (callers / callees)'],
      [['Ctrl+o', 'Ctrl+i'], 'Jump back / forward in navigation history'],
    ]
  },
  {
    title: 'Search & Occurrences',
    items: [
      [['/'], 'Find in file (forward)'],
      [['?'], 'Find in file (backward)'],
      [['n', 'N'], 'Next / previous match'],
      [['*', '#'], 'Search current word under cursor forward / backward'],
      [['Esc'], 'Clear highlights, search, and occurrences'],
    ]
  },
  {
    title: 'Visual Mode & AI Agent Actions',
    items: [
      [['v'], 'Character-wise visual selection'],
      [['V'], 'Line-wise visual selection'],
      [['e', 'c'], 'Edit selection inline with AI coding agent'],
      [['y'], 'Yank (copy) code to clipboard'],
      [['Y'], 'Yank reference (file:line-range)'],
      [['u'], 'Find usages of selected symbol'],
      [['Esc'], 'Cancel selection and return to Normal mode'],
    ]
  },
  {
    title: 'Tabs & Commands',
    items: [
      [['gt', 'gT'], 'Next tab / previous tab'],
      [['[N]gt'], 'Switch to tab N'],
      [[':'], 'Open Command Palette'],
    ]
  }
];

export function showVimHelp() {
  let modal = $('#vim-helpsheet');
  if (!modal) {
    modal = document.createElement('div');
    modal.id = 'vim-helpsheet';
    document.body.appendChild(modal);
  }

  const isChecked = vimEnabled ? 'checked' : '';

  const sectionsHtml = VIM_SHORTCUT_SECTIONS.map(sec => {
    const itemsHtml = sec.items.map(([combos, v]) => {
      const comboList = Array.isArray(combos) ? combos : [combos];
      const keysHtml = comboList.map(keyCaps).filter(Boolean).join('<span class="key-or">/</span>');
      return '<dt>' + keysHtml + '</dt><dd>' + esc(v) + '</dd>';
    }).join('');
    return '<div class="vim-help-section">' +
      '<div class="vim-sec-title">' + esc(sec.title) + '</div>' +
      '<dl class="help-grid vim-help-grid">' + itemsHtml + '</dl>' +
      '</div>';
  }).join('');

  modal.innerHTML = '<div class="help-card vim-help-card">' +
    '<div class="help-header vim-help-header">' +
    '<div class="vim-help-title"><h2>Vim Keybindings</h2><span class="help-version">Modal Navigation</span></div>' +
    '<div class="vim-toggle-row">' +
    '<label class="vim-switch-label">' +
    '<input type="checkbox" id="vim-toggle-input" ' + isChecked + '>' +
    '<span class="vim-switch-slider"></span>' +
    '<span class="vim-switch-text">' + (vimEnabled ? 'Enabled' : 'Disabled') + '</span>' +
    '</label>' +
    '<button id="btn-close-vim-help" class="mini" title="Close (Esc)">✕</button>' +
    '</div>' +
    '</div>' +
    '<div class="vim-help-content">' + sectionsHtml + '</div>' +
    '<div class="vim-help-footer">' +
    '<button id="btn-switch-to-std-help" class="settings-btn-link" title="View Standard Shortcuts (?)">View Standard Shortcuts (?)</button>' +
    '<span class="agent-hint">Press Esc or click outside to dismiss</span>' +
    '</div>' +
    '</div>';

  modal.hidden = false;

  // Toggle listener inside help sheet
  const toggleInput = modal.querySelector('#vim-toggle-input');
  if (toggleInput) {
    toggleInput.addEventListener('change', e => {
      const active = e.target.checked;
      setVimModeEnabled(active, true);
      const txt = modal.querySelector('.vim-switch-text');
      if (txt) txt.textContent = active ? 'Enabled' : 'Disabled';
      showToast('✓', active ? 'Vim mode enabled' : 'Vim mode disabled');
    });
  }

  modal.querySelector('#btn-close-vim-help')?.addEventListener('click', closeVimHelp);
  modal.querySelector('#btn-switch-to-std-help')?.addEventListener('click', () => {
    closeVimHelp();
    showHelp();
  });
  modal.addEventListener('click', e => {
    if (e.target === modal) closeVimHelp();
  });
}

export function closeVimHelp() {
  const modal = $('#vim-helpsheet');
  if (modal) modal.hidden = true;
}

export function initVim() {
  // Check initial setting from settings or localStorage fallback
  let initial = false;
  try {
    const val = localStorage.getItem('px0.editor.vimMode');
    if (val === 'true') initial = true;
  } catch {}
  if (S.settings && S.settings['editor.vimMode'] !== undefined) {
    initial = S.settings['editor.vimMode'] === true || S.settings['editor.vimMode'] === 'true';
  }
  setVimModeEnabled(initial, false);

  // Status chip click -> show help
  const chip = $('#st-vim');
  if (chip) {
    chip.addEventListener('click', () => {
      showVimHelp();
    });
  }

  // Footer button click
  const helpBtn = $('#btn-vim-help');
  if (helpBtn) {
    helpBtn.addEventListener('click', () => {
      showVimHelp();
    });
  }
}
