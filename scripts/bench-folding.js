// Run with: node scripts/bench-folding.js
// Measures pure coordinate lookups independently of DOM/network timing.
import { performance } from 'node:perf_hooks';
import { createFolds, rebuildFolds, foldLineToRow, foldRowToLine, updateFoldPixels, foldedLineY } from '../web/src/folding.js';

const foldBenchPairs = Array.from({ length: 8000 }, (_, i) => [i * 50 + 1, i * 50 + 40]);
const foldBenchState = createFolds(foldBenchPairs, 400000);
foldBenchState.closed.fill(1);
rebuildFolds(foldBenchState);
updateFoldPixels(foldBenchState, null, 0, 20);
const foldBenchBytes = ['ranges', 'closed', 'spans', 'pixels'].reduce((n, key) => n + foldBenchState[key].byteLength, 0);
let foldBenchSink = 0;
function measureFoldLookup(name, fn) {
  for (let i = 0; i < 100000; i++) foldBenchSink += fn(i % 400000 + 1);
  const samples = [];
  for (let run = 0; run < 5; run++) {
    const start = performance.now();
    for (let i = 0; i < 1000000; i++) foldBenchSink += fn(i % 400000 + 1);
    samples.push((performance.now() - start) * 1e6 / 1000000);
  }
  samples.sort((a, b) => a - b);
  console.log(`${name}: ${samples[2].toFixed(1)} ns/lookup (median of 5 × 1M)`);
}
console.log(`400,000 source lines, 8,000 collapsed blocks: ${foldBenchBytes} bytes of typed metadata`);
measureFoldLookup('source → visible row', n => foldLineToRow(foldBenchState, n));
measureFoldLookup('visible row → source', n => foldRowToLine(foldBenchState, n % 88000 + 1));
measureFoldLookup('source → Y', n => foldedLineY(foldBenchState, n, null, 20));
if (!Number.isFinite(foldBenchSink)) throw new Error('Invalid coordinate result');
