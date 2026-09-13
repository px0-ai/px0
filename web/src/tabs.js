// web/src/tabs.js
import { $, esc, S, doc_, api, LH, CHUNK, withKeys } from './state.js';
import { vp, sizer, rowsEl, editor } from './ui.js';
import { render, layout, refineChunk } from './renderer.js';
import { updateStatus, setStatusNote, refreshMetrics } from './status.js';
import { pushHistory } from './history.js';
import { warmLSP } from './lsp.js';
import { loadOutline } from './outline.js';
import { showPanel } from './panels.js';
import { revealDir } from './tree.js';
import { clearLink } from './hover.js';
import { clearFind } from './find.js';
import { clearSelectAll } from './selbar.js';

// Recently closed files, newest last, for Alt+Shift+T.
const closedTabs = [];
const MAX_CLOSED = 20;

export async function openFile(path, opts = {}) {
  const { line, push = true, col } = opts;
  let idx = S.tabs.findIndex(t => t.path === path);
  if (idx < 0) {
    let j;
    const start = line ? Math.max(0, Math.floor((line - 1) / CHUNK) * CHUNK) : 0;
    try {
      j = await api('/api/file', { path, start, count: CHUNK });
    } catch (e) {
      setStatusNote(path + ': ' + e.message);
      return;
    }
    if (j.image) {
      showImage(path);
      return;
    }
    const d = {
      path, name: path.split('/').pop(), lang: j.lang, total: j.total, maxCols: j.maxCols,
      size: j.size, lines: new Array(j.total), chunks: new Set([start / CHUNK]),
      pending: new Set(), refining: new Set(), scrollTop: 0, cur: line || 1,
      outline: null, gen: 0,
    };
    for (let i = 0; i < j.lines.length; i++) d.lines[j.start + i] = j.lines[i];
    d.lsp = j.lsp || { state: 'off', server: '' };
    S.tabs.push(d);
    idx = S.tabs.length - 1;
    if (j.refine) refineChunk(d, start / CHUNK);
  }
  const prev = doc_();
  if (prev && prev !== S.tabs[idx]) prev.scrollTop = vp.scrollTop;
  if (prev !== S.tabs[idx]) clearSelectAll();
  S.active = idx;
  const d = S.tabs[idx];

  $('#empty').hidden = true;
  hideImage();
  if (!S.at || S.at.path !== d.path) S.at = null;
  S.lsp.state = (d.lsp && d.lsp.state) || 'off';
  S.lsp.server = (d.lsp && d.lsp.server) || '';
  S.lsp.missing = (d.lsp && d.lsp.missing) || '';
  warmLSP(d);
  
  hideMarkdownPreview();
  const btnMd = $('#btn-preview-md');
  if (btnMd) btnMd.hidden = !d.name.toLowerCase().endsWith('.md');
  
  drawTabs(); drawCrumbs(); layout();

  if (line) { d.cur = line; centerLine(line); }
  else vp.scrollTop = d.scrollTop;
  render();
  updateStatus();
  if ($('#panel-outline')?.classList.contains('active')) loadOutline();
  if (push) pushHistory(path, line || d.cur, col);
}

export function centerLine(n) {
  const y = (n - 1) * LH - Math.max(0, vp.clientHeight / 2 - LH * 2);
  vp.scrollTop = Math.max(0, y);
}

export function closeTab(i) {
  clearSelectAll();
  const [closed] = S.tabs.splice(i, 1);
  if (closed) {
    if (closed.path) {
      // The active tab's scrollTop is only saved on switch, so read the live one.
      const scrollTop = i === S.active ? vp.scrollTop : closed.scrollTop;
      closedTabs.push({ path: closed.path, cur: closed.cur, scrollTop });
      if (closedTabs.length > MAX_CLOSED) closedTabs.shift();
      api('/api/close', { path: closed.path })
        .then(() => refreshMetrics())
        .catch(() => {});
    }
    // Release large arrays to assist garbage collection
    closed.lines = null;
    closed.chunks?.clear?.();
    closed.pending?.clear?.();
    closed.refining?.clear?.();
    closed.outline = null;
  }
  if (S.tabs.length === 0) {
    S.active = -1;
    rowsEl.innerHTML = ''; sizer.style.height = '0px';
    $('#empty').hidden = false; drawCrumbs();
    drawTabs(); updateStatus();
    return;
  }
  S.active = Math.min(i, S.tabs.length - 1);
  const d = doc_();
  drawTabs(); drawCrumbs(); layout();
  vp.scrollTop = d.scrollTop; render(); updateStatus();
}

// Reopens the most recently closed file that is not open already, where it was left.
export async function reopenClosedTab() {
  while (closedTabs.length) {
    const t = closedTabs.pop();
    if (S.tabs.some(d => d.path === t.path)) continue;
    await openFile(t.path, { line: t.cur });
    if (doc_()?.path !== t.path) return;
    vp.scrollTop = t.scrollTop;
    render(); updateStatus();
    return;
  }
}

export function drawTabs() {
  $('#tabs').innerHTML = S.tabs.map((t, i) =>
    '<div class="tab' + (i === S.active ? ' active' : '') + '" data-i="' + i + '" title="' + esc(t.path) + '">' +
    '<span class="tn">' + esc(t.name) + '</span><span class="x" data-close="' + i + '" title="' + withKeys('Close tab ({Alt+W})') + '"><svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6"/></svg></span></div>').join('');
  const act = $('#tabs .tab.active');
  if (act) act.scrollIntoView({ block: 'nearest', inline: 'nearest' });
}

export function switchTab(i) {
  if (i === S.active || !S.tabs[i]) return;
  clearLink();
  const prev = doc_();
  if (prev) prev.scrollTop = vp.scrollTop;
  S.active = i;
  clearFind();
  clearSelectAll();
  S.at = null;
  S.lsp.state = (S.tabs[i].lsp && S.tabs[i].lsp.state) || 'off';
  S.lsp.server = (S.tabs[i].lsp && S.tabs[i].lsp.server) || '';
  S.lsp.missing = (S.tabs[i].lsp && S.tabs[i].lsp.missing) || '';
  warmLSP(S.tabs[i]);

  hideMarkdownPreview();
  const d = S.tabs[i];
  const btnMd = $('#btn-preview-md');
  if (btnMd) btnMd.hidden = !d.name.toLowerCase().endsWith('.md');

  drawTabs(); drawCrumbs(); layout();

  vp.scrollTop = S.tabs[i].scrollTop;
  render(); updateStatus();
  if ($('#panel-outline')?.classList.contains('active')) loadOutline();
  pushHistory(S.tabs[i].path, S.tabs[i].cur);
}

export function drawCrumbs() {
  const el = $('#crumbs');
  if (el) el.innerHTML = '';
}

export function showImage(path) {
  hideImage();
  const box = document.createElement('div');
  box.id = 'imgview';
  box.innerHTML = '<img src="/api/raw?path=' + encodeURIComponent(path) + '" alt="">';
  editor.appendChild(box);
  $('#empty').hidden = true;
}

export function hideImage() {
  const b = $('#imgview');
  if (b) b.remove();
}

let mdPreviewActive = false;

export function hideMarkdownPreview() {
  mdPreviewActive = false;
  const prev = $('#md-preview');
  const vpEl = $('#viewport');
  const btn = $('#btn-preview-md');
  if (prev) prev.hidden = true;
  if (vpEl) vpEl.hidden = false;
  if (btn) btn.classList.remove('active');
}

export async function toggleMarkdownPreview() {
  const d = doc_();
  if (!d) return;
  const btn = $('#btn-preview-md');
  const prev = $('#md-preview');
  const vpEl = $('#viewport');
  
  if (mdPreviewActive) {
    hideMarkdownPreview();
    render();
  } else {
    mdPreviewActive = true;
    if (btn) btn.classList.add('active');
    if (prev) {
      prev.innerHTML = '<div class="hint">Loading preview...</div>';
      prev.hidden = false;
    }
    if (vpEl) vpEl.hidden = true;
    try {
      const res = await fetch('/api/raw?path=' + encodeURIComponent(d.path));
      if (!res.ok) throw new Error('Failed to load markdown');
      const text = await res.text();
      if (prev && mdPreviewActive) prev.innerHTML = '<div class="md-preview-inner">' + marked.parse(text) + '</div>';
    } catch (e) {
      if (prev && mdPreviewActive) prev.innerHTML = '<div class="md-preview-inner"><div class="hint">Error loading preview: ' + esc(e.message) + '</div></div>';
    }
  }
}

export function initTabs() {
  $('#tabs').addEventListener('click', e => {
    const x = e.target.closest('[data-close]');
    if (x) { closeTab(+x.dataset.close); return; }
    const t = e.target.closest('.tab');
    if (t) switchTab(+t.dataset.i);
  });
  $('#tabs').addEventListener('auxclick', e => {
    const t = e.target.closest('.tab');
    if (t && e.button === 1) { e.preventDefault(); closeTab(+t.dataset.i); }
  });
  const crumbsEl = $('#crumbs');
  if (crumbsEl) {
    crumbsEl.addEventListener('click', e => {
      const c = e.target.closest('[data-dir]');
      if (c) { showPanel('files'); revealDir(c.dataset.dir); }
    });
  }
}
