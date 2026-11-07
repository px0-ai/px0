import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { DEFAULT_TIPS, renderTipMarkdown, escapeTipHtml } from './tips-util.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

test('DEFAULT_TIPS contains valid tips with required properties', () => {
  assert.ok(Array.isArray(DEFAULT_TIPS));
  assert.ok(DEFAULT_TIPS.length >= 10);
  for (const tip of DEFAULT_TIPS) {
    assert.ok(tip.id, 'Tip must have an id');
    assert.ok(tip.title, 'Tip must have a title');
    assert.ok(tip.content, 'Tip must have content');
    assert.ok(tip.tag || tip.category, 'Tip must have a tag or category');
  }
});

test('renderTipMarkdown formats bold, italic, code, and escapes html', () => {
  const input = 'Use **bold** and *italic* and `code` with <script>alert(1)</script>';
  const output = renderTipMarkdown(input);

  assert.ok(output.includes('<strong>bold</strong>'));
  assert.ok(output.includes('<em>italic</em>'));
  assert.ok(output.includes('<code class="tip-code">code</code>'));
  assert.ok(!output.includes('<script>'));
  assert.ok(output.includes('&lt;script&gt;alert(1)&lt;/script&gt;'));
});

test('renderTipMarkdown formats links securely', () => {
  const input = 'Visit [px0](https://px0.ai) for more info.';
  const output = renderTipMarkdown(input);

  assert.ok(output.includes('<a href="https://px0.ai" target="_blank" rel="noopener noreferrer" class="tip-link">px0</a>'));
});

test('renderTipMarkdown formats kbd and KBD tags as styled HTML', () => {
  const input = 'Press <kbd>Cmd</kbd> + <kbd>P</kbd> or <KBD>Enter</KBD> to confirm.';
  const output = renderTipMarkdown(input);

  assert.ok(output.includes('<kbd class="tip-kbd">Cmd</kbd>'));
  assert.ok(output.includes('<kbd class="tip-kbd">P</kbd>'));
  assert.ok(output.includes('<kbd class="tip-kbd">Enter</kbd>'));
  assert.ok(!output.includes('&lt;kbd&gt;'));
  assert.ok(!output.includes('&lt;KBD&gt;'));
});

test('renderTipMarkdown formats shortcut braces into kbd elements', () => {
  const input = 'Press {Mod+P} to open files.';
  const output = renderTipMarkdown(input);

  assert.ok(output.includes('<kbd class="tip-kbd">Mod+P</kbd>'));
});

test('escapeTipHtml properly escapes special characters', () => {
  assert.equal(escapeTipHtml('a < b & c > d "e"'), 'a &lt; b &amp; c &gt; d &quot;e&quot;');
});

// The public website lives in a separate, optional sibling checkout.
const publicTipsTest = fs.existsSync(path.resolve(__dirname, '../../../web/public/tips.json')) ? test : test.skip;
publicTipsTest('public tips.json in ../web/public exists and contains valid tips', () => {
  const publicTipsPath = path.resolve(__dirname, '../../../web/public/tips.json');

  const content = fs.readFileSync(publicTipsPath, 'utf8');
  const data = JSON.parse(content);
  assert.ok(Array.isArray(data));
  assert.ok(data.length >= 10);

  for (const tip of data) {
    assert.ok(tip.id);
    assert.ok(tip.title);
    assert.ok(tip.content);
  }
});
