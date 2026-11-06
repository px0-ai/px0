// web/src/reviewflow.test.js
import test from 'node:test';
import assert from 'node:assert/strict';

// Set up minimal browser globals for headless unit tests
const elements = new Map();

function createElement(id) {
  const el = {
    id,
    hidden: false,
    classList: {
      classes: new Set(),
      add(c) { this.classes.add(c); },
      remove(c) { this.classes.delete(c); },
      contains(c) { return this.classes.has(c); }
    },
    dataset: {},
    innerHTML: '',
    textContent: '',
    addEventListener() {},
    removeEventListener() {},
    appendChild() {},
    setAttribute(k, v) { this[k] = v; },
    getAttribute(k) { return this[k] ?? null; },
    querySelector() { return null; },
    querySelectorAll() { return []; }
  };
  elements.set(id, el);
  return el;
}

globalThis.document = {
  body: {
    classList: {
      classes: new Set(),
      add(c) { this.classes.add(c); },
      remove(c) { this.classes.delete(c); },
      contains(c) { return this.classes.has(c); }
    }
  },
  querySelector(sel) {
    if (sel.startsWith('#')) {
      const id = sel.slice(1);
      if (!elements.has(id)) createElement(id);
      return elements.get(id);
    }
    return null;
  },
  querySelectorAll() { return []; },
  createElement(tag) {
    return createElement('elem-' + Math.random());
  },
  addEventListener() {},
  removeEventListener() {}
};

globalThis.window = globalThis;
globalThis.location = { href: 'http://localhost:7777/' };

const { getMode, setMode, getLayout, setLayout } = await import('./reviewflow.js');

test('defaults to code mode', () => {
  assert.equal(getMode(), 'code');
});

test('toggles to review mode and back to code mode', () => {
  setMode('review');
  assert.equal(getMode(), 'review');
  assert.equal(document.body.classList.contains('in-review-mode'), true);

  setMode('code');
  assert.equal(getMode(), 'code');
  assert.equal(document.body.classList.contains('in-review-mode'), false);
});

test('toggles between vertical and horizontal review layout', () => {
  setLayout('horizontal');
  assert.equal(getLayout(), 'horizontal');
  const reviewView = document.querySelector('#review-view');
  assert.equal(reviewView.classList.contains('layout-horizontal'), true);

  setLayout('vertical');
  assert.equal(getLayout(), 'vertical');
  assert.equal(reviewView.classList.contains('layout-vertical'), true);
});
