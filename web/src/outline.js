// web/src/outline.js
import { $, $$, esc, S, doc_, api } from './state.js';
import { centerLine } from './tabs.js';
import { render } from './renderer.js';
import { updateStatus, setLspState } from './status.js';
import { pushHistory } from './history.js';

export async function loadOutline(target = doc_()) {
  const d = target;
  const el = $('#outline');
  if (!d) { if (el) el.innerHTML = '<div class="hint">No file open.</div>'; return; }
  d.outlineScheduled = false;
  if (!d.outline) {
    if (!d.outlinePromise) {
      d.outlinePromise = api('/api/outline', { path: d.path })
        .then(j => j.symbols || [])
        .catch(() => []);
    }
    d.outline = await d.outlinePromise;
  }
  if (doc_() === d) {
    drawOutline();
    render();
  }
  upgradeOutline(d);
}

// Outline extraction is whole-file work. Keep it out of the open/switch path
// and let the browser run it when the active tab is idle.
export function scheduleOutline(d) {
  if (!d || d.outline || d.outlinePromise || d.outlineScheduled) return;
  d.outlineScheduled = true;
  const run = () => {
    d.outlineScheduled = false;
    if (doc_() === d) void loadOutline(d);
  };
  if (typeof window.requestIdleCallback === 'function') window.requestIdleCallback(run, { timeout: 1000 });
  else setTimeout(run, 250);
}

/* A language server's document symbols beat regex on every axis, so swap them
   in whenever one answers. Panel only: this never moves the viewport. */
export async function upgradeOutline(d) {
  if (d.outlineLSP || S.lsp.state === 'off' || S.lsp.state === 'failed') return;
  d.outlineLSP = true;
  let j;
  try { j = await api('/api/lsp/symbols', { path: d.path, wait: 20000 }); }
  catch { d.outlineLSP = false; return; }
  d.lsp = { state: j.state || 'off', server: j.server || '', missing: j.missing || '' };
  if (doc_() === d) setLspState(j);
  if (!j.symbols || !j.symbols.length) { d.outlineLSP = false; return; }
  d.outline = j.symbols;
  d.stickyFunctionEnds?.clear();
  d.outlineSource = j.server;
  if (doc_() === d) {
    if ($('#panel-outline')?.classList.contains('active')) drawOutline();
    render();
  }
}

export function drawOutline() {
  const d = doc_();
  const el = $('#outline');
  const rel = $('#right-symbols-list');
  if (!d || !d.outline) {
    if (el) el.innerHTML = '<div class="hint">No symbols found.</div>';
    if (rel) rel.innerHTML = '<div class="hint">No symbols found.</div>';
    return;
  }
  const f = ($('#outline-filter')?.value || '').toLowerCase();
  const rf = ($('#right-symbols-filter')?.value || '').toLowerCase();

  const syms = f ? d.outline.filter(s => s.name.toLowerCase().includes(f)) : d.outline;
  const rsyms = rf ? d.outline.filter(s => s.name.toLowerCase().includes(rf)) : d.outline;

  const renderSymHtml = (items) => {
    if (!items.length) return '<div class="hint">No symbols found.</div>';
    const base = Math.min(...items.map(s => s.indent));
    return (d.outlineSource ? '<div class="hint"><span class="src">' + esc(d.outlineSource) + '</span> · ' + items.length + ' symbols</div>' : '') +
      items.map(s =>
      '<div class="sym" data-n="' + s.line + '" style="padding-left:' + (10 + Math.min(s.indent - base, 16) * 5) + 'px" title="Jump to ' + esc(s.name) + ' at line ' + s.line + '">' +
      '<span class="kd" data-k="' + esc(s.kind) + '">' + esc(kindLabel(s.kind)) + '</span>' +
      '<span class="sn">' + esc(s.name) + '</span><span class="sl">' + s.line + '</span></div>').join('');
  };

  if (el) el.innerHTML = renderSymHtml(syms);
  if (rel) rel.innerHTML = renderSymHtml(rsyms);
}

export const KIND_LABEL = {
  func: 'fn', method: 'fn', fn: 'fn', def: 'fn', defp: 'fn', defmacro: 'mac',
  class: 'cls', struct: 'str', interface: 'int', trait: 'trt', impl: 'impl',
  type: 'typ', typealias: 'typ', enum: 'enm', record: 'rec', object: 'obj',
  const: 'cst', var: 'var', let: 'var', val: 'var',
  module: 'mod', mod: 'mod', namespace: 'ns', defmodule: 'mod', package: 'pkg',
  macro: 'mac', extension: 'ext', protocol: 'int', union: 'uni',
  heading: 'h', sym: '·',
};

export function kindLabel(k) { return KIND_LABEL[k] || k.slice(0, 3); }

export function initOutline() {
  $('#outline')?.addEventListener('click', e => {
    const s = e.target.closest('.sym');
    if (!s) return;
    $$('.sym.sel').forEach(x => x.classList.remove('sel'));
    s.classList.add('sel');
    const d = doc_(); if (!d) return;
    d.cur = +s.dataset.n; centerLine(d.cur); render(); updateStatus();
    pushHistory(d.path, d.cur);
  });
  $('#outline-filter')?.addEventListener('input', drawOutline);
}
