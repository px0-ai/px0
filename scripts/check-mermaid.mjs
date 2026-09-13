#!/usr/bin/env node
/**
 * scripts/check-mermaid.mjs
 *
 * Parses every ```mermaid fence in tracked Markdown with the exact Mermaid
 * build px0 ships (web/lib/mermaid/<version>), so a diagram that would fail
 * in the preview fails here first: in CI, or before merging upstream docs.
 *
 * jsdom is a dev-only dependency; px0 itself runs with none. If it is missing:
 *   npm install --no-save jsdom
 *
 * Usage: node scripts/check-mermaid.mjs [file.md ...]
 */

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const version = '11.17.2'; // keep in lockstep with scripts/vendor-mermaid.sh

let JSDOM;
try {
  ({ JSDOM } = await import('jsdom'));
} catch {
  console.error('check-mermaid: jsdom is not installed. Run:\n  npm install --no-save jsdom');
  process.exit(2);
}

// Mermaid's renderer expects a DOM even when it only parses.
const dom = new JSDOM('<!doctype html><html><body></body></html>', { pretendToBeVisual: true });
globalThis.window = dom.window;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, 'navigator', { value: dom.window.navigator, configurable: true });

const entry = path.join(root, 'web', 'lib', 'mermaid', version, 'mermaid.esm.min.mjs');
const mermaid = (await import(pathToFileURL(entry).href)).default;
mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', suppressErrorRendering: true });

/* Every fence whose info string is exactly "mermaid", with its 1-based line. */
function mermaidFences(text) {
  const found = [];
  const re = /^```mermaid[ \t]*\r?\n([\s\S]*?)^```[ \t]*$/gm;
  let m;
  while ((m = re.exec(text)) !== null) {
    found.push({ line: text.slice(0, m.index).split('\n').length + 1, src: m[1] });
  }
  return found;
}

function files() {
  const args = process.argv.slice(2);
  if (args.length) return args;
  // Tracked files plus new ones that are not committed yet, so a fresh doc is
  // checked before its first commit; ignored files (node_modules, skills) stay out.
  const out = execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard', '--', '*.md', '*.markdown'], { cwd: root, encoding: 'utf8' });
  return out.split('\0').filter(Boolean);
}

let diagrams = 0, failed = 0;
for (const file of files()) {
  const abs = path.isAbsolute(file) ? file : path.join(root, file);
  let text;
  try { text = fs.readFileSync(abs, 'utf8'); } catch { continue; }
  for (const fence of mermaidFences(text)) {
    diagrams++;
    try {
      await mermaid.parse(fence.src);
    } catch (e) {
      failed++;
      const msg = String(e.message).split('\n')[0].slice(0, 200);
      console.error(`${file}:${fence.line}: ${msg}`);
    }
  }
}

console.log(`check-mermaid: ${diagrams} diagram(s) checked, ${failed} failed`);
process.exit(failed ? 1 : 0);
