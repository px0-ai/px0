import test from 'node:test';
import assert from 'node:assert/strict';
import { breadcrumbParts } from './breadcrumbs.js';

test('breadcrumb paths distinguish workspace and external files', () => {
  assert.deepEqual(breadcrumbParts('src/editor/tabs.js'), { external: false, dirs: ['src', 'editor'], name: 'tabs.js' });
  assert.deepEqual(breadcrumbParts('/usr/lib/file.go'), { external: true, dirs: ['usr', 'lib'], name: 'file.go' });
  assert.deepEqual(breadcrumbParts('C:/work/file.go'), { external: true, dirs: ['C:', 'work'], name: 'file.go' });
});
