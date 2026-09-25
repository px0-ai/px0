// web/src/inspector.js
import { $, $$, esc, S, doc_, api } from './state.js';
import { layout, render } from './renderer.js';
import { updateStatus, setStatusNote } from './status.js';
import { openFile, centerLine } from './tabs.js';
import { pushHistory } from './history.js';
import { loadOutline, drawOutline } from './outline.js';
import { displayPath, cancelSearch } from './search.js';
import { groupHits, flashFind, canAskServer, lspCall, positionNow } from './lsp.js';

export function showRightInspector(tab = 'refs') {
  document.body.classList.remove('right-hidden');
  setRightInspectorTab(tab);
  layout();
  render();
}

export function hideRightInspector() {
  cancelSearch();
  document.body.classList.add('right-hidden');
  layout();
  render();
}

export function setRightInspectorTab(tab) {
  if (tab !== 'search') cancelSearch();
  $$('.inspector-tab').forEach(b => b.classList.toggle('active', b.dataset.itab === tab));
  $('#pane-right-refs')?.classList.toggle('active', tab === 'refs');
  $('#pane-right-symbols')?.classList.toggle('active', tab === 'symbols');
  $('#pane-right-calls')?.classList.toggle('active', tab === 'calls');
  $('#pane-right-search')?.classList.toggle('active', tab === 'search');
  if (tab === 'symbols') {
    loadOutline();
    $('#right-symbols-filter')?.focus();
  }
  if (tab === 'search') $('#q')?.focus();
}

export function renderRightResults(word, hits, server, isExact) {
  const targetEl = $('#right-ref-target');
  const badgeEl = $('#right-ref-badge');
  const listEl = $('#right-refs-list');
  if (!targetEl || !badgeEl || !listEl) return;

  targetEl.textContent = word;
  badgeEl.textContent = hits.length;

  if (!hits.length) {
    listEl.innerHTML = '<div class="hint">No references found for "<b>' + esc(word) + '</b>".</div>';
    return;
  }

  const grouped = groupHits(hits);
  const head = hits.length + ' reference' + (hits.length === 1 ? '' : 's') +
    (server ? ' · ' + esc(server) : ' · text search');
  let html = '<div class="hint">' + head + '</div>';

  for (const f of grouped) {
    html += '<div class="rfile" data-toggle="r-' + esc(f.path) + '" title="' + esc(f.path) + '">' +
      '<span class="ar">&#9660;</span>' +
      '<span class="fp">' + esc(displayPath(f.path)) + '</span>' +
      '<span class="cnt">' + f.matches.length + '</span></div>' +
      '<div data-group="r-' + esc(f.path) + '">';
    for (const m of f.matches) {
      html += '<div class="rline" data-p="' + esc(f.path) + '" data-n="' + m.line + '" title="Jump to ' + esc(f.path) + ':' + m.line + '">' +
        '<span class="rn">' + m.line + '</span><span class="rt">' +
        esc(m.pre) + '<mark>' + esc(m.mid || word) + '</mark>' + esc(m.post) + '</span></div>';
    }
    html += '</div>';
  }
  listEl.innerHTML = html;
}

export async function inspectReferences(arg) {
  const d = doc_();
  const at = (arg && arg.word) ? arg : positionNow(typeof arg === 'string' ? arg : S.lastWord);
  if (!d || !at || !at.word) return;

  showRightInspector('refs');
  const targetEl = $('#right-ref-target');
  const badgeEl = $('#right-ref-badge');
  const listEl = $('#right-refs-list');
  if (targetEl) targetEl.textContent = at.word;
  if (badgeEl) badgeEl.textContent = '…';
  if (listEl) listEl.innerHTML = '<div class="hint">Finding references for "' + esc(at.word) + '"…</div>';

  if (canAskServer(at)) {
    setStatusNote('references to ' + at.word + '…', 8000);
    try {
      const j = await lspCall('refs', at, 30000);
      updateStatus();
      if (j && j.hits && j.hits.length) {
        setStatusNote('');
        renderRightResults(at.word, j.hits, j.server, true);
        return;
      }
    } catch {
      updateStatus();
    }
  }

  // Fallback: search workspace text for whole word
  setStatusNote('searching references to ' + at.word + '…', 8000);
  try {
    const j = await api('/api/search', { q: at.word, word: true, case: true });
    updateStatus();
    setStatusNote('');
    const hits = [];
    if (j.results) {
      for (const f of j.results) {
        for (const m of f.matches) {
          hits.push({ path: f.path, line: m.line, pre: m.pre, mid: m.mid, post: m.post });
        }
      }
    }
    renderRightResults(at.word, hits, '', false);
  } catch (err) {
    updateStatus();
    setStatusNote('');
    if (listEl) listEl.innerHTML = '<div class="hint">Search error: ' + esc(err.message) + '</div>';
  }
}

export function initInspector() {
  $$('.inspector-tab').forEach(btn => btn.addEventListener('click', () => {
    setRightInspectorTab(btn.dataset.itab);
  }));

  $('#btn-close-right')?.addEventListener('click', hideRightInspector);

  /* Right inspector resizer */
  (() => {
    const rrz = $('#right-resizer');
    if (!rrz) return;
    let dragging = false;
    rrz.addEventListener('mousedown', e => { dragging = true; rrz.classList.add('drag'); e.preventDefault(); });
    addEventListener('mousemove', e => {
      if (!dragging) return;
      const w = Math.max(200, Math.min(700, window.innerWidth - e.clientX));
      $('#right-side').style.width = w + 'px';
    });
    addEventListener('mouseup', () => { if (dragging) { dragging = false; rrz.classList.remove('drag'); layout(); render(); } });
  })();

  /* Right-side symbols list navigation */
  $('#right-symbols-list')?.addEventListener('click', e => {
    const s = e.target.closest('.sym');
    if (!s) return;
    $$('#right-symbols-list .sym.sel, #outline .sym.sel').forEach(x => x.classList.remove('sel'));
    s.classList.add('sel');
    const d = doc_(); if (!d) return;
    d.cur = +s.dataset.n;
    centerLine(d.cur);
    render();
    updateStatus();
    pushHistory(d.path, d.cur);
  });
  $('#right-symbols-filter')?.addEventListener('input', drawOutline);

  $('#right-refs-list')?.addEventListener('click', e => {
    const t = e.target.closest('[data-toggle]');
    if (t) {
      const listEl = $('#right-refs-list');
      const g = listEl.querySelector('[data-group="' + CSS.escape(t.dataset.toggle) + '"]');
      if (!g) return;
      const hidden = g.style.display === 'none';
      g.style.display = hidden ? '' : 'none';
      const ar = $('.ar', t);
      if (ar) ar.innerHTML = hidden ? '&#9660;' : '&#9654;';
      return;
    }
    const r = e.target.closest('.rline');
    if (r) {
      $$('#right-refs-list .rline.sel').forEach(x => x.classList.remove('sel'));
      r.classList.add('sel');
      openFile(r.dataset.p, { line: +r.dataset.n });
      const targetEl = $('#right-ref-target');
      if (targetEl && targetEl.textContent) flashFind(targetEl.textContent);
    }
  });
}
