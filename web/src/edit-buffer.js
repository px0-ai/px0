// web/src/edit-buffer.js — pure text-buffer edit helpers (no DOM).
import { CHUNK } from './state.js';

export function ensureRawLine(doc, line) {
  const i = line - 1;
  if (doc.raw[i] === undefined) doc.raw[i] = '';
}

export function lineText(doc, line) {
  ensureRawLine(doc, line);
  return doc.raw[line - 1];
}

export function markDirty(doc, line) {
  doc.dirty = true;
  if (!doc.dirtyLines) doc.dirtyLines = new Set();
  doc.dirtyLines.add(line);
}

function spliceLine(doc, at, text) {
  doc.raw.splice(at, 0, text);
  doc.lines.splice(at, 0, undefined);
  doc.total++;
}

export function insertText(doc, text) {
  if (!text) return;
  ensureRawLine(doc, doc.cur);
  const i = doc.cur - 1;
  const line = doc.raw[i];
  const col = Math.min(doc.col || 0, line.length);
  doc.raw[i] = line.slice(0, col) + text + line.slice(col);
  doc.col = col + text.length;
  markDirty(doc, doc.cur);
}

export function insertNewline(doc) {
  ensureRawLine(doc, doc.cur);
  const i = doc.cur - 1;
  const line = doc.raw[i];
  const col = Math.min(doc.col || 0, line.length);
  const rest = line.slice(col);
  doc.raw[i] = line.slice(0, col);
  spliceLine(doc, i + 1, rest);
  doc.cur++;
  doc.col = 0;
  markDirty(doc, doc.cur - 1);
  markDirty(doc, doc.cur);
}

export function deleteChar(doc, forward) {
  ensureRawLine(doc, doc.cur);
  const i = doc.cur - 1;
  const line = doc.raw[i];
  const col = Math.min(doc.col || 0, line.length);
  if (forward) {
    if (col >= line.length) {
      if (doc.cur >= doc.total) return false;
      const next = doc.raw[doc.cur] ?? '';
      doc.raw[i] = line + next;
      doc.raw.splice(doc.cur, 1);
      doc.lines.splice(doc.cur, 1);
      doc.total--;
      markDirty(doc, doc.cur);
      return true;
    }
    doc.raw[i] = line.slice(0, col) + line.slice(col + 1);
    markDirty(doc, doc.cur);
    return true;
  }
  if (col > 0) {
    doc.raw[i] = line.slice(0, col - 1) + line.slice(col);
    doc.col = col - 1;
    markDirty(doc, doc.cur);
    return true;
  }
  if (doc.cur <= 1) return false;
  const prev = doc.raw[doc.cur - 2] ?? '';
  doc.raw[doc.cur - 2] = prev + line;
  doc.raw.splice(i, 1);
  doc.lines.splice(i, 1);
  doc.total--;
  doc.cur--;
  doc.col = prev.length;
  markDirty(doc, doc.cur);
  return true;
}

export function deleteLine(doc) {
  if (doc.total <= 1) {
    doc.raw[0] = '';
    doc.col = 0;
    markDirty(doc, 1);
    return;
  }
  const i = doc.cur - 1;
  doc.raw.splice(i, 1);
  doc.lines.splice(i, 1);
  doc.total--;
  if (doc.cur > doc.total) doc.cur = doc.total;
  doc.col = 0;
  markDirty(doc, doc.cur);
}

export function deleteToEOL(doc) {
  ensureRawLine(doc, doc.cur);
  const i = doc.cur - 1;
  const line = doc.raw[i];
  const col = Math.min(doc.col || 0, line.length);
  doc.raw[i] = line.slice(0, col);
  markDirty(doc, doc.cur);
}

export function openLine(doc, below) {
  const at = below ? doc.cur : doc.cur - 1;
  spliceLine(doc, at, '');
  doc.cur = at + 1;
  doc.col = 0;
  markDirty(doc, doc.cur);
}

export function assembleContent(doc) {
  const lines = [];
  for (let i = 0; i < doc.total; i++) lines.push(doc.raw[i] ?? '');
  return lines.join('\n');
}

export function rawComplete(doc) {
  for (let i = 0; i < doc.total; i++) {
    if (doc.raw[i] === undefined) return false;
  }
  return true;
}

export function applyChunk(d, j) {
  if (!d.raw) d.raw = new Array(d.total);
  for (let i = 0; i < j.lines.length; i++) {
    const idx = j.start + i;
    d.lines[idx] = j.lines[i];
    if (j.raw) d.raw[idx] = j.raw[i];
  }
  d.chunks.add(Math.floor(j.start / CHUNK));
  if (rawComplete(d)) d.rawComplete = true;
}
