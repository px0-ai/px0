// web/src/diagnostics.js
import { $, esc, S, doc_, api } from './state.js';
import { render } from './renderer.js';
import { setLspState } from './status.js';

const DIAGNOSTIC_TRIES = 8;
const DIAGNOSTIC_RENDER_LIMIT = 1000;
const DIAGNOSTIC_RANK = { error: 1, warning: 2, info: 3, hint: 4 };

export function emptyDiagnostics() {
  return {
    items: [], byLine: new Map(), pending: false, loaded: false,
    loading: false, timedOut: false, error: '', tries: 0, timer: 0, seq: 0,
  };
}

function diagnosticSeverity(n) {
  return n === 1 ? 'error' : n === 2 ? 'warning' : n === 4 ? 'hint' : 'info';
}

function indexDiagnosticLines(items) {
  const byLine = new Map();
  for (const item of items) {
    const severity = diagnosticSeverity(item.severity);
    const previous = byLine.get(item.line);
    if (!previous || DIAGNOSTIC_RANK[severity] < DIAGNOSTIC_RANK[previous]) {
      byLine.set(item.line, severity);
    }
  }
  return byLine;
}

export function clearDiagnostics(d) {
  const state = d?.diagnostics;
  if (!state) return;
  clearTimeout(state.timer);
  state.seq++;
  state.items = [];
  state.byLine.clear();
}

/* Diagnostics are server notifications, not request results. Ask briefly for
   the latest snapshot, then stop rather than leaving a permanent poller. */
export async function loadDiagnostics(d, force = false) {
  const state = d?.diagnostics;
  if (!state || !S.tabs.includes(d) || d.lsp?.state === 'off' || d.lsp?.state === 'failed') return;
  if (force) {
    clearTimeout(state.timer);
    state.timer = 0;
    state.loaded = false;
    state.timedOut = false;
    state.tries = 0;
  }
  if (state.loading || state.timer || state.loaded) return;

  state.loading = true;
  state.error = '';
  const seq = ++state.seq;
  let result;
  try {
    result = await api('/api/lsp/diagnostics', {
      path: d.path,
      wait: state.tries === 0 ? 1 : 1200,
    });
  } catch (err) {
    if (!S.tabs.includes(d) || d.diagnostics !== state || state.seq !== seq) return;
    state.loading = false;
    state.loaded = true;
    state.pending = false;
    state.error = err.message;
    if (doc_() === d) drawDiagnostics(d);
    return;
  }
  if (!S.tabs.includes(d) || d.diagnostics !== state || state.seq !== seq) return;

  d.lsp = { state: result.state, server: result.server || '', missing: '' };
  if (doc_() === d) setLspState(result);
  state.items = result.diagnostics || [];
  state.byLine = indexDiagnosticLines(state.items);
  state.loading = false;
  state.pending = !!result.pending;

  if (state.pending && state.tries < DIAGNOSTIC_TRIES) {
    state.tries++;
    state.timer = setTimeout(() => {
      state.timer = 0;
      loadDiagnostics(d);
    }, 450);
  } else {
    state.loaded = true;
    state.timedOut = state.pending;
    state.pending = false;
  }

  if (doc_() === d) {
    drawDiagnostics(d);
    render();
  }
}

function diagnosticChanged(d, item) {
  const marks = d.gutter?.marks;
  if (!marks) return false;
  for (const line of marks.keys()) {
    if (line >= item.line && line <= item.endLine) return true;
  }
  return false;
}

function renderDiagnosticGroup(label, items) {
  if (!items.length) return '';
  let html = label ? '<div class="problem-group">' + label + '</div>' : '';
  for (const item of items) {
    const severity = diagnosticSeverity(item.severity);
    const name = severity[0].toUpperCase() + severity.slice(1);
    const source = [item.source, item.code].filter(Boolean).join(' ');
    const position = item.line + ':' + (item.col + 1);
    html += '<div class="problem problem-' + severity + '" data-n="' + item.line + '" data-col="' + item.col +
      '" title="Jump to ' + esc(position) + '">' +
      '<span class="problem-dot"></span><span class="problem-main"><span class="problem-message">' +
      esc(String(item.message || 'Problem')) + '</span><span class="problem-meta">' + name +
      (source ? ' · ' + esc(source) : '') + '</span></span><span class="problem-pos">' + position + '</span></div>';
  }
  return html;
}

function renderDiagnosticLimit(total) {
  if (total <= DIAGNOSTIC_RENDER_LIMIT) return '';
  return '<div class="hint">Showing ' + DIAGNOSTIC_RENDER_LIMIT.toLocaleString() +
    ' of ' + total.toLocaleString() + ' problems.</div>';
}

export function drawDiagnostics(d = doc_()) {
  const title = $('#right-problems-target');
  const badge = $('#right-problems-badge');
  const list = $('#right-problems-list');
  if (!title || !badge || !list) return;
  const visible = !document.body.classList.contains('right-hidden') &&
    $('#pane-right-problems')?.classList.contains('active');
  if (!d) {
    title.textContent = '-';
    badge.textContent = '0';
    list.innerHTML = visible ? '<div class="hint">Open a file to see its problems.</div>' : '';
    return;
  }

  title.textContent = d.name;
  const state = d.diagnostics;
  if (d.fileError) {
    badge.textContent = '!';
  } else if (d.lsp?.state === 'off') {
    badge.textContent = '0';
  } else if (d.lsp?.state === 'failed') {
    badge.textContent = '!';
  } else {
    badge.textContent = !state?.loaded || state.pending || state.loading ? '…' : String(state.items.length);
  }
  if (!visible) {
    list.innerHTML = '';
    return;
  }
  if (d.fileError) {
    list.innerHTML = '<div class="hint">File is no longer available.</div>';
    return;
  }
  if (d.lsp?.state === 'off') {
    list.innerHTML = '<div class="hint">No language server for this file.</div>';
    return;
  }
  if (d.lsp?.state === 'failed') {
    list.innerHTML = '<div class="hint">The language server failed.</div>';
    return;
  }
  if (!state || !state.loaded || state.loading || state.pending) {
    list.innerHTML = '<div class="hint">Waiting for diagnostics from ' + esc(d.lsp?.server || 'the language server') + '…</div>';
    return;
  }
  if (state.error) {
    list.innerHTML = '<div class="hint">Could not load diagnostics: ' + esc(state.error) + '</div>';
    return;
  }
  if (state.timedOut) {
    list.innerHTML = '<div class="hint">No diagnostics received from ' + esc(d.lsp?.server || 'the language server') + '.</div>';
    return;
  }
  if (!state.items.length) {
    list.innerHTML = '<div class="hint">No problems reported by ' + esc(d.lsp?.server || 'the language server') + '.</div>';
    return;
  }

  if (!d.gutter?.marks) {
    list.innerHTML = renderDiagnosticGroup('', state.items.slice(0, DIAGNOSTIC_RENDER_LIMIT)) +
      renderDiagnosticLimit(state.items.length);
    return;
  }
  const changed = [], elsewhere = [];
  for (const item of state.items) (diagnosticChanged(d, item) ? changed : elsewhere).push(item);
  const shownChanged = changed.slice(0, DIAGNOSTIC_RENDER_LIMIT);
  const shownElsewhere = elsewhere.slice(0, DIAGNOSTIC_RENDER_LIMIT - shownChanged.length);
  list.innerHTML = renderDiagnosticGroup('Changed lines', shownChanged) +
    renderDiagnosticGroup('Elsewhere in file', shownElsewhere) + renderDiagnosticLimit(state.items.length);
}
