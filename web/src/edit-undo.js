// web/src/edit-undo.js — per-document undo/redo snapshots.
const MAX_UNDO = 100;

export function snapshotDoc(doc) {
  return {
    raw: doc.raw.slice(),
    lines: doc.lines.slice(),
    total: doc.total,
    cur: doc.cur,
    col: doc.col || 0,
    dirty: doc.dirty,
    dirtyLines: new Set(doc.dirtyLines || []),
  };
}

export function restoreDoc(doc, snap) {
  doc.raw = snap.raw.slice();
  doc.lines = snap.lines.slice();
  doc.total = snap.total;
  doc.cur = snap.cur;
  doc.col = snap.col;
  doc.dirty = snap.dirty;
  doc.dirtyLines = new Set(snap.dirtyLines);
}

export function initUndo(doc) {
  if (!doc.undo) doc.undo = [];
  if (!doc.redo) doc.redo = [];
}

export function pushUndo(doc) {
  initUndo(doc);
  doc.undo.push(snapshotDoc(doc));
  if (doc.undo.length > MAX_UNDO) doc.undo.shift();
  doc.redo.length = 0;
}

export function canUndo(doc) {
  return !!(doc?.undo?.length);
}

export function canRedo(doc) {
  return !!(doc?.redo?.length);
}

export function undo(doc) {
  if (!canUndo(doc)) return false;
  initUndo(doc);
  doc.redo.push(snapshotDoc(doc));
  restoreDoc(doc, doc.undo.pop());
  return true;
}

export function redo(doc) {
  if (!canRedo(doc)) return false;
  initUndo(doc);
  doc.undo.push(snapshotDoc(doc));
  restoreDoc(doc, doc.redo.pop());
  return true;
}
