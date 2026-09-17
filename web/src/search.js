// web/src/search.js
import { $, $$, esc, api, debounce } from './state.js';
import { openFile } from './tabs.js';
import { flashFind } from './lsp.js';

export const resultsEl = $('#results');
export let lastResults = null;

let searchAbort = null;

export function cancelSearch() {
  if (searchAbort) {
    searchAbort.abort();
    searchAbort = null;
  }
}

// The search panel is optional markup; without it every entry point is a no-op.
export const runSearch = debounce(async () => {
  const qEl = $('#q');
  if (!qEl || !resultsEl) return;
  const q = qEl.value;
  if (!q.trim()) {
    cancelSearch();
    resultsEl.innerHTML = '';
    return;
  }
  cancelSearch();
  const controller = new AbortController();
  searchAbort = controller;
  resultsEl.innerHTML = '<div class="hint">searching…</div>';
  const params = {
    q, glob: $('#glob')?.value || '',
    case: $('#o-case')?.classList.contains('on') ? 1 : '',
    word: $('#o-word')?.classList.contains('on') ? 1 : '',
    re: $('#o-re')?.classList.contains('on') ? 1 : '',
  };
  try {
    const j = await api('/api/search', params, { signal: controller.signal });
    if (searchAbort === controller) {
      searchAbort = null;
      renderResults(j);
    }
  } catch (e) {
    if (e.name === 'AbortError') return;
    if (searchAbort === controller) {
      searchAbort = null;
      resultsEl.innerHTML = '<div class="hint">' + esc(e.message) + '</div>';
    }
  }
}, 160);

export function renderResults(j) {
  lastResults = j;
  if (!resultsEl) return;
  if (!j.results || !j.results.length) {
    resultsEl.innerHTML = '<div class="hint">No results.</div>';
    return;
  }
  const head = j.header || (j.total.toLocaleString() + ' result' + (j.total === 1 ? '' : 's') +
    ' in ' + j.files.toLocaleString() + ' file' + (j.files === 1 ? '' : 's') + (j.truncated ? ' (truncated)' : ''));
  let html = '<div class="hint">' + esc(head) + '</div>';
  for (const f of j.results) {
    html += '<div class="rfile" data-toggle="' + esc(f.path) + '" title="' + esc(f.path) + '">' +
      '<span class="ar">&#9660;</span>' +
      (f.ext ? '<span class="ext">ext</span>' : '') +
      '<span class="fp">' + esc(displayPath(f.path)) + '</span>' +
      '<span class="cnt">' + f.matches.length + '</span></div>' +
      '<div data-group="' + esc(f.path) + '">';
    for (const m of f.matches) {
      html += '<div class="rline" data-p="' + esc(f.path) + '" data-n="' + m.line + '" title="Jump to ' + esc(f.path) + ':' + m.line + '">' +
        '<span class="rn">' + m.line + '</span><span class="rt">' +
        esc(m.pre) + '<mark>' + esc(m.mid) + '</mark>' + esc(m.post) + '</span></div>';
    }
    html += '</div>';
  }
  resultsEl.innerHTML = html;
}

/* External results carry an absolute path, which is far too long for the
   panel. Show enough of the tail to identify the file. */
export function displayPath(p) {
  if (p.length <= 48) return p;
  const parts = p.split('/');
  return '…/' + parts.slice(-3).join('/');
}

export function initSearch() {
  if (!resultsEl) return;
  resultsEl.addEventListener('click', e => {
    const t = e.target.closest('[data-toggle]');
    if (t) {
      const g = resultsEl.querySelector('[data-group="' + CSS.escape(t.dataset.toggle) + '"]');
      const hidden = g.style.display === 'none';
      g.style.display = hidden ? '' : 'none';
      $('.ar', t).innerHTML = hidden ? '&#9660;' : '&#9654;';
      return;
    }
    const r = e.target.closest('.rline');
    if (r) {
      $$('.rline.sel', resultsEl).forEach(x => x.classList.remove('sel'));
      r.classList.add('sel');
      openFile(r.dataset.p, { line: +r.dataset.n });
      const q = $('#q').value;
      if (q) flashFind(q);
    }
  });

  $('#q').addEventListener('input', runSearch);
  $('#glob').addEventListener('input', runSearch);
  $$('.opt').forEach(b => b.addEventListener('click', () => { b.classList.toggle('on'); runSearch(); }));
  $('#q').addEventListener('keydown', e => {
    if (e.key === 'Enter') { e.preventDefault(); const f = $('.rline', resultsEl); if (f) f.click(); }
  });
}
