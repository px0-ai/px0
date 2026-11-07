import { test, mock, beforeEach, afterAll } from 'bun:test';
import assert from 'node:assert/strict';
import { createFolds, rebuildFolds, revealFoldLine, visibleFoldLine, foldLineToRow } from './folding.js';

// Exercise the real interaction handlers with narrow UI/service boundaries.
// No browser, clipboard, filesystem, or harness is used by these tests.
let doc, selection;
const copied = [], actions = [], errors = [];
const noop = () => {};
const textarea = { value: '', scrollTop: 0 };
const vp = { scrollTop: 0, clientHeight: 600 };
const state = { wrap: false, tabs: [] };
const elements = { '#editview-textarea': textarea };
const body = { classList: { add: noop, remove: noop, toggle: noop } };
const originalDocument = globalThis.document;
const originalComputedStyle = globalThis.getComputedStyle;
globalThis.document = { body, activeElement: null };
globalThis.getComputedStyle = () => ({ lineHeight: '20px' });
mock.module('./state.js', () => ({ $: key => elements[key] || null, S: state, doc_: () => doc,
  esc: text => text, keyCaps: noop, LH: 20, apiPostJson: noop }));
mock.module('./ui.js', () => ({ vp, sizer: {}, copyToClipboard: text => copied.push(text),
  showToast: (_, message) => errors.push(message), flashActionSuccess: noop }));
mock.module('./renderer.js', () => ({ render: noop, paint: noop, rowFor: () => ({ querySelector: () => ({ textContent: 'header' }) }),
  placeCaret: noop, getLH: () => 20, lineAtScrollY: noop,
  lineTopY: (line, d = doc) => (foldLineToRow(d.folds, line) - 1) * 20,
  revealSourceLine: (line, d = doc) => revealFoldLine(d.folds, line) }));
mock.module('./cursor.js', () => ({ moveCursor: noop, moveCol: noop, moveWord: noop, caretToEdge: noop,
  updateDomSelection: noop, clearSelection: d => { d.selAnchor = null; }, revealCaretX: noop }));
mock.module('./status.js', () => ({ updateStatus: noop }));
mock.module('./lsp.js', () => ({ gotoDefinition: noop, findReferences: noop }));
mock.module('./history.js', () => ({ go: noop, pushHistory: noop }));
mock.module('./find.js', () => ({ openFind: noop, findNextMatch: noop, clearFind: noop }));
mock.module('./hover.js', () => ({ showHover: noop }));
mock.module('./calls.js', () => ({ showCalls: noop }));
mock.module('./tabs.js', () => ({ switchTab: noop, closeTab: noop, drawTabs: noop, loadGutter: noop,
  reloadOpenTabs: () => { assert.equal(visibleFoldLine(doc.folds, doc.cur), doc.cur); } }));
mock.module('./selbar.js', () => ({ getSelectedRangeInfo: () => selection,
  runSelectionAction: (action, _, info = selection) => actions.push({ action, text: info.text }) }));
mock.module('./palette.js', () => ({ openPalette: noop }));
mock.module('./shortcuts.js', () => ({ showHelp: noop }));
mock.module('./folding-ui.js', () => ({ runFoldAction: noop }));
mock.module('./markdown.js', () => ({ syncPreview: noop, previewing: () => false, previewTopLine: noop, previewKind: () => '' }));
mock.module('./diff.js', () => ({ syncDiffView: noop }));
mock.module('./gitstream.js', () => ({ triggerRefresh: noop }));

const vim = await import('./vim.js');
const edit = await import('./editview.js');
const key = value => vim.handleVimKeyDown({ key: value, preventDefault: noop });
const settle = () => new Promise(resolve => setTimeout(resolve, 0));

beforeEach(() => {
  vim.setVimModeEnabled(false, false);
  doc = { path: 'test.go', cur: 1, total: 100, col: 0 };
  state.tabs = [doc]; copied.length = actions.length = errors.length = 0;
  selection = { text: '', l1: 1, l2: 90, path: doc.path };
  vim.setVimModeEnabled(true, false);
});

function delayedSelection() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  selection.load = async () => { await promise; selection.text = 'header\nhidden source\nnext'; selection.load = null; };
  return { resolve, reject };
}

test('Vim yank waits for folded source before clearing visual selection', async () => {
  const pending = delayedSelection();
  key('V'); key('y');
  assert.equal(vim.getVimMode(), 'VISUAL_LINE');
  assert.equal(copied.length, 0);
  pending.resolve(); await settle();
  assert.deepEqual(copied, ['header\nhidden source\nnext']);
  assert.equal(vim.getVimMode(), 'NORMAL');
  assert.equal(doc.selAnchor, null);
});

test('Vim inline edit and usages receive complete folded source', async () => {
  for (const [press, action] of [['e', 'agent-edit'], ['u', 'usages']]) {
    selection.text = ''; const pending = delayedSelection();
    key('V'); key(press);
    assert.equal(actions.length, press === 'e' ? 0 : 1);
    pending.resolve(); await settle();
    assert.deepEqual(actions.at(-1), { action, text: 'header\nhidden source\nnext' });
    assert.equal(vim.getVimMode(), 'NORMAL');
  }
});

test('escaping or switching documents cancels a pending visual action', async () => {
  for (const cancel of [() => key('Escape'), () => { doc = { ...doc }; }]) {
    const pending = delayedSelection();
    key('V'); key('y'); cancel(); pending.resolve(); await settle();
    assert.equal(copied.length, 0);
    vim.setVimModeEnabled(false, false); vim.setVimModeEnabled(true, false);
  }
});

test('failed hidden-source load keeps the selection available for retry', async () => {
  const pending = delayedSelection(); key('V'); key('y');
  pending.reject(new Error('source changed')); await settle();
  assert.equal(copied.length, 0);
  assert.deepEqual(errors, ['source changed']);
  assert.equal(vim.getVimMode(), 'VISUAL_LINE');
});

test('leaving edit mode reveals its source anchor inside nested folds', async () => {
  doc.folds = createFolds([[2, 90], [10, 70], [92, 99]], 100);
  doc.folds.closed.fill(1); rebuildFolds(doc.folds);
  doc.isEditing = true; textarea.scrollTop = 20 * 29;
  await edit.exitEditMode();
  assert.equal(doc.cur, 30);
  assert.deepEqual([...doc.folds.closed], [0, 0, 1]);
  assert.equal(vp.scrollTop, 20 * 29);
});

// Restore the test process's DOM boundary after all imports/handlers have run.
afterAll(() => { globalThis.document = originalDocument; globalThis.getComputedStyle = originalComputedStyle; });
