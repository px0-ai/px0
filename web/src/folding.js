// Pure fold geometry. All coordinates are original, one-based source lines.
// Metadata scales with fold count, never with file line count. No DOM/imports.
export function createFolds(pairs, total) {
  const flat = [];
  const parents = [];
  let previous = 0;
  for (const pair of pairs.slice(0, 8192)) {
    const [start, end] = pair;
    if (!Number.isInteger(start) || !Number.isInteger(end) || start <= previous || end <= start || end > total) continue;
    while (parents.length && start > parents[parents.length - 1]) parents.pop();
    if (parents.length && end > parents[parents.length - 1]) continue;
    flat.push(start, end); parents.push(end); previous = start;
  }
  return { ranges: new Uint32Array(flat), closed: new Uint8Array(flat.length / 2),
    spans: new Uint32Array(0), pixels: new Float64Array(0), generation: 0,
    pixelOffsets: null, pixelVersion: -1, pixelLH: 0 };
}

export function foldIndex(f, line) {
  if (!f) return -1;
  let lo = 0, hi = f.closed.length;
  while (lo < hi) { const mid = (lo + hi) >>> 1; if (f.ranges[mid * 2] < line) lo = mid + 1; else hi = mid; }
  return lo < f.closed.length && f.ranges[lo * 2] === line ? lo : -1;
}

export function rebuildFolds(f) {
  let count = 0, end = 0;
  for (let i = 0; i < f.closed.length; i++) {
    if (f.closed[i] && f.ranges[i * 2] > end) { count++; end = f.ranges[i * 2 + 1]; }
  }
  const spans = new Uint32Array(count * 3);
  let at = 0, hidden = 0; end = 0;
  for (let i = 0; i < f.closed.length; i++) {
    const start = f.ranges[i * 2];
    if (!f.closed[i] || start <= end) continue;
    end = f.ranges[i * 2 + 1]; hidden += end - start;
    spans[at++] = start; spans[at++] = end; spans[at++] = hidden;
  }
  f.spans = spans;
  f.pixels = new Float64Array(count + 1);
  f.pixelVersion = -1; f.pixelOffsets = null; f.generation++;
}

// Last outer collapsed header <= line; nested folds stay in closed[] so they
// reappear when their parent opens, without double-counting hidden lines.
export function foldSpanAt(f, line) {
  if (!f?.spans.length) return -1;
  let lo = 0, hi = f.spans.length / 3;
  while (lo < hi) { const mid = (lo + hi) >>> 1; if (f.spans[mid * 3] <= line) lo = mid + 1; else hi = mid; }
  return lo - 1;
}

export function visibleFoldLine(f, line) {
  const i = foldSpanAt(f, line);
  return i >= 0 && line <= f.spans[i * 3 + 1] ? f.spans[i * 3] : line;
}

export function foldLineToRow(f, line) {
  const i = foldSpanAt(f, line);
  if (i < 0) return line;
  const p = i * 3;
  return line <= f.spans[p + 1]
    ? f.spans[p] - (i ? f.spans[p - 1] : 0)
    : line - f.spans[p + 2];
}

export function foldRowToLine(f, row) {
  if (!f?.spans.length) return row;
  const spans = f.spans;
  let lo = 0, hi = spans.length / 3;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1, p = mid * 3;
    const headerRow = spans[p] - (mid ? spans[p - 1] : 0);
    if (headerRow < row) lo = mid + 1; else hi = mid;
  }
  return row + (lo ? spans[lo * 3 - 1] : 0);
}

export function moveFoldLine(f, line, delta, total) {
  const rows = foldLineToRow(f, total + 1) - 1;
  return foldRowToLine(f, Math.max(1, Math.min(rows, foldLineToRow(f, line) + delta)));
}

export function nextFoldLine(f, line) {
  const i = foldSpanAt(f, line);
  return i >= 0 && line <= f.spans[i * 3 + 1] ? f.spans[i * 3 + 1] + 1 : line + 1;
}

export function revealFoldLine(f, line) {
  if (!f?.spans.length || visibleFoldLine(f, line) === line) return false;
  let changed = false;
  for (let i = 0; i < f.closed.length && f.ranges[i * 2] < line; i++) {
    if (f.closed[i] && f.ranges[i * 2 + 1] >= line) { f.closed[i] = 0; changed = true; }
  }
  if (changed) rebuildFolds(f);
  return changed;
}

export function updateFoldPixels(f, offsets, version, lh) {
  if (!f?.spans.length || f.pixelOffsets === offsets && f.pixelVersion === version && f.pixelLH === lh) return;
  f.pixelOffsets = offsets; f.pixelVersion = version; f.pixelLH = lh;
  f.pixels[0] = 0;
  for (let i = 0; i < f.spans.length / 3; i++) {
    const start = f.spans[i * 3], end = f.spans[i * 3 + 1];
    f.pixels[i + 1] = f.pixels[i] + (offsets ? offsets[end] - offsets[start] : (end - start) * lh);
  }
}

export function foldedLineY(f, line, offsets, lh) {
  const i = foldSpanAt(f, line);
  if (i >= 0 && line <= f.spans[i * 3 + 1]) {
    const header = f.spans[i * 3];
    return (offsets ? offsets[header - 1] : (header - 1) * lh) - f.pixels[i];
  }
  return (offsets ? offsets[line - 1] : (line - 1) * lh) - (i >= 0 ? f.pixels[i + 1] : 0);
}

// Convert visible Y back into the existing uncollapsed height map. The break
// point is the bottom of each visible header, including its wrapped height.
export function unfoldedY(f, y, offsets, lh) {
  if (!f?.spans.length) return y;
  let lo = 0, hi = f.spans.length / 3;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1, start = f.spans[mid * 3];
    const edge = (offsets ? offsets[start] : start * lh) - f.pixels[mid];
    if (edge <= y) lo = mid + 1; else hi = mid;
  }
  return y + f.pixels[lo];
}
