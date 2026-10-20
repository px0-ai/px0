#!/usr/bin/env node
/**
 * scripts/build-web.js
 *
 * Bundles modular frontend sources from `web/src/*.js` into `web/app.js`.
 * Supports execution under Bun (`bun scripts/build-web.js`) or Node (`node scripts/build-web.js`).
 * Zero external dependencies.
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import zlib from 'node:zlib';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const rootDir = path.resolve(__dirname, '..');
const srcDir = path.join(rootDir, 'web', 'src');
// Each entry is bundled on its own: the app, and the workbench page that a
// bare `px0` serves (workbench.go), which shares only a few small modules.
const bundles = [
  { entry: path.join(srcDir, 'main.js'), out: path.join(rootDir, 'web', 'app.js') },
  { entry: path.join(srcDir, 'workbench.js'), out: path.join(rootDir, 'web', 'workbench.js') },
];

function hasBun() {
  try {
    execFileSync('bun', ['--version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

function buildWithBun(entryFile, outFile) {
  console.log(`[build-web] Bundling ${path.basename(entryFile)} with Bun...`);
  execFileSync('bun', [
    'build',
    entryFile,
    `--outfile=${outFile}`,
    '--target=browser',
    '--format=iife',
    '--minify'
  ], {
    cwd: rootDir,
    stdio: 'inherit'
  });
}

/**
 * Fallback bundler using pure Node.js if Bun is not installed.
 * Recursively resolves local ES module imports and emits an IIFE bundle.
 */
function buildWithNode(entryFile, outFile) {
  console.log(`[build-web] Bundling ${path.basename(entryFile)} with Node.js fallback...`);
  const visited = new Set();
  const moduleOrder = [];

  function visit(filePath) {
    const norm = path.normalize(filePath);
    if (visited.has(norm)) return;
    visited.add(norm);

    const code = fs.readFileSync(norm, 'utf8');
    const importRegex = /^\s*import\s+(?:(?:(?:\*\s+as\s+\w+)|(?:\{[^}]*\})|(?:\w+))\s+from\s+)?['"](\.[^'"]+)['"];?/gm;
    let match;
    const dependencies = [];
    while ((match = importRegex.exec(code)) !== null) {
      const rel = match[1];
      const depPath = path.resolve(path.dirname(norm), rel);
      dependencies.push(depPath);
    }

    for (const dep of dependencies) {
      visit(dep);
    }
    moduleOrder.push(norm);
  }

  visit(entryFile);

  // Transform modules into an IIFE
  let combined = '// Bundled by scripts/build-web.js\n(() => {\n\'use strict\';\n\n';

  // Every module shares one scope here, so a top-level name declared in two
  // modules is a SyntaxError in the browser. Fail the build instead.
  const declRegex = /^(?:export\s+)?(?:async\s+)?(?:function\*?|const|let|var|class)\s+([A-Za-z_$][\w$]*)/gm;
  const declaredIn = new Map();
  const collisions = [];

  for (const modPath of moduleOrder) {
    const rel = path.relative(rootDir, modPath);
    let src = fs.readFileSync(modPath, 'utf8');

    for (const m of src.matchAll(declRegex)) {
      const prev = declaredIn.get(m[1]);
      if (prev && prev !== rel) collisions.push(`${m[1]} (${prev}, ${rel})`);
      else declaredIn.set(m[1], rel);
    }

    // Strip ES import declarations (single-line and multi-line)
    src = src.replace(/^\s*import\s+\{[^}]*\}\s+from\s+['"][^'"]+['"];?\s*$/gm, ''); // single-line: import { A, B } from './x.js';
    src = src.replace(/^\s*import\s+\{[^}]*$(\n[^}]*)*\n\s*\}\s+from\s+['"][^'"]+['"];?\s*$/gm, ''); // multi-line: import {\n  A,\n  B\n} from './x.js';
    src = src.replace(/^\s*import\s+.*?;?\s*$/gm, ''); // remaining: import X from './y.js'; import './z.js';

    // Replace `export const X =` / `export function X(` / `export async function X(` / `export let X`
    src = src.replace(/^\s*export\s+(?:async\s+)?function\s+/gm, (m) => m.replace('export ', ''));
    src = src.replace(/^\s*export\s+(const|let|var)\s+/gm, '$1 ');
    src = src.replace(/^\s*export\s*\{[^}]*\};?\s*$/gm, '');
    src = src.replace(/^\s*export\s+default\s+.*?;?\s*$/gm, '');

    combined += `// --- File: ${rel} ---\n` + src.trim() + '\n\n';
  }

  if (collisions.length) {
    console.error('[build-web] Top-level names declared in more than one module (rename them or install Bun):');
    for (const c of collisions) console.error('  ' + c);
    process.exit(1);
  }

  combined += '})();\n';
  fs.writeFileSync(outFile, combined, 'utf8');
}

/**
 * Pre-compresses vendor minified JS assets with gzip to keep the compiled Go
 * binary small. Replaces uncompressed .min.js files with .min.js.gz.
 */
function compressVendorAssets() {
  const vendorDir = path.join(rootDir, 'web', 'vendor');
  if (!fs.existsSync(vendorDir)) return;
  const files = fs.readdirSync(vendorDir);
  for (const file of files) {
    if (file.endsWith('.min.js')) {
      const fullPath = path.join(vendorDir, file);
      const gzPath = fullPath + '.gz';
      const content = fs.readFileSync(fullPath);
      const compressed = zlib.gzipSync(content, { level: 9 });
      fs.writeFileSync(gzPath, compressed);
      fs.unlinkSync(fullPath);
      console.log(`[build-web] Compressed ${path.relative(rootDir, fullPath)} -> ${path.relative(rootDir, gzPath)} (${(content.length / 1024 / 1024).toFixed(2)} MB -> ${(compressed.length / 1024 / 1024).toFixed(2)} MB)`);
    }
  }
}

function run() {
  const start = Date.now();
  const bun = hasBun();
  for (const { entry, out } of bundles) {
    if (bun) buildWithBun(entry, out);
    else buildWithNode(entry, out);
  }
  compressVendorAssets();

  const duration = Date.now() - start;
  for (const { out } of bundles) {
    const sizeKb = (fs.statSync(out).size / 1024).toFixed(1);
    console.log(`[build-web] Successfully built ${path.relative(rootDir, out)} (${sizeKb} KB)`);
  }
  console.log(`[build-web] Done in ${duration}ms`);
}

run();
