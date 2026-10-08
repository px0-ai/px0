import test from 'node:test';
import assert from 'node:assert/strict';
import { syncSidebarButton, toggleSidebarState } from './sidebar-state.js';

function bodyWithSidebar(initiallyHidden = false) {
  const classes = new Set(initiallyHidden ? ['side-hidden'] : []);
  return {
    classList: {
      contains: name => classes.has(name),
      toggle(name) {
        if (classes.has(name)) classes.delete(name);
        else classes.add(name);
      },
    },
  };
}

test('sidebar button follows restored collapsed state', () => {
  const body = bodyWithSidebar(true);
  const button = { hidden: true };

  syncSidebarButton(body, button);

  assert.equal(button.hidden, false);
});

test('sidebar button toggles with sidebar and can restore it', () => {
  const body = bodyWithSidebar();
  const button = { hidden: true };

  assert.equal(toggleSidebarState(body, button), true);
  assert.equal(button.hidden, false);
  assert.equal(toggleSidebarState(body, button), false);
  assert.equal(button.hidden, true);
});
