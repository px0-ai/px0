// web/src/problems.js
import { $, $$, esc, S, doc_, api } from './state.js';
import { emit } from './bus.js';
import { layout, render } from './renderer.js';
import { centerLine, openFile } from './tabs.js';
import { pushHistory } from './history.js';
import { showRightInspector } from './inspector.js';

let activeProblemFilter = 'all'; // 'all' | 'changed' | 'errors'

export function getActiveProblemFilter() {
  return activeProblemFilter;
}

export function setActiveProblemFilter(filter) {
  activeProblemFilter = filter;
  renderProblemsPane();
}

/**
 * Checks if a given problem falls on a changed (added or modified) line in the document.
 */
export function isProblemOnChangedLine(d, p) {
  if (!d || !d.gutter || !d.gutter.marks) return false;
  return d.gutter.marks.has(p.line);
}

/**
 * Loads problems for the given document tab.
 */
export async function loadProblems(d, force = false) {
  if (!d || d.isImage) return null;
  if (!force && d.problemsLoaded && d.problemsReq === null) return d.problems;
  if (d.problemsReq && !force) return d.problemsReq;

  d.problemsReq = (async () => {
    try {
      const j = await api('/api/lsp/problems', { path: d.path, wait: 250 });
      d.problems = j.problems || [];
      d.problemCounts = j.counts || { error: 0, warning: 0, info: 0, hint: 0, total: 0 };
      d.problemState = j.state || 'off';
      d.problemServer = j.server || '';
      d.problemError = j.error || '';
      d.problemsLoaded = true;

      // Group problems by 1-based line number for fast gutter lookup
      const byLine = new Map();
      for (const p of d.problems) {
        if (!byLine.has(p.line)) byLine.set(p.line, []);
        byLine.get(p.line).push(p);
      }
      d.problemsByLine = byLine;

      if (doc_() === d) {
        updateProblemsBadge(d);
        render(); // update gutter markers
        renderProblemsPane();
      }
      return d.problems;
    } catch (e) {
      d.problems = [];
      d.problemCounts = { error: 0, warning: 0, info: 0, hint: 0, total: 0 };
      d.problemState = 'failed';
      d.problemError = e.message;
      d.problemsByLine = new Map();
      if (doc_() === d) {
        updateProblemsBadge(d);
        renderProblemsPane();
      }
      return [];
    } finally {
      d.problemsReq = null;
    }
  })();

  return d.problemsReq;
}

/**
 * Updates the badge counter on the Problems tab in the right inspector.
 */
export function updateProblemsBadge(d = doc_()) {
  const badgeEl = $('#prob-tab-badge');
  if (!badgeEl) return;
  if (!d || !d.problemCounts || d.problemCounts.total === 0) {
    badgeEl.hidden = true;
    badgeEl.textContent = '';
    badgeEl.className = 'prob-tab-badge';
    return;
  }

  const { error, warning, total } = d.problemCounts;
  if (error > 0) {
    badgeEl.textContent = error;
    badgeEl.className = 'prob-tab-badge prob-badge-error';
    badgeEl.title = `${error} error${error === 1 ? '' : 's'}${warning ? `, ${warning} warning${warning === 1 ? '' : 's'}` : ''}`;
    badgeEl.hidden = false;
  } else if (warning > 0) {
    badgeEl.textContent = warning;
    badgeEl.className = 'prob-tab-badge prob-badge-warning';
    badgeEl.title = `${warning} warning${warning === 1 ? '' : 's'}`;
    badgeEl.hidden = false;
  } else if (total > 0) {
    badgeEl.textContent = total;
    badgeEl.className = 'prob-tab-badge prob-badge-info';
    badgeEl.title = `${total} note${total === 1 ? '' : 's'}`;
    badgeEl.hidden = false;
  } else {
    badgeEl.hidden = true;
  }
}

/**
 * Renders the Problems inspector pane for the active document.
 */
export function renderProblemsPane() {
  const pane = $('#pane-right-problems');
  if (!pane || !pane.classList.contains('active')) return;

  const d = doc_();
  const summaryEl = $('#prob-summary');
  const listEl = $('#prob-list');
  const filterChangedBtn = $('#prob-filter-changed');
  const filterContainer = $('#prob-filters');

  if (!listEl) return;
  if (!d) {
    if (summaryEl) summaryEl.textContent = 'Problems';
    listEl.innerHTML = '<div class="hint">No file open.</div>';
    return;
  }

  // Update filter buttons active state
  if (filterContainer) {
    $$('.opt', filterContainer).forEach(b => {
      b.classList.toggle('on', b.dataset.probFilter === activeProblemFilter);
    });
  }

  // Waiting / Loading state: language server is still starting or indexing
  const isStarting = d.problemState === 'starting' || d.problemState === 'indexing' || (d.lsp && (d.lsp.state === 'starting' || d.lsp.state === 'indexing'));
  if (isStarting && (!d.problems || !d.problems.length)) {
    const srv = d.problemServer || d.lsp?.server || 'LSP';
    if (summaryEl) summaryEl.textContent = 'Analyzing…';
    listEl.innerHTML = `<div class="hint prob-hint"><span class="prob-spinner"></span>Analyzing ${esc(d.name)}… Language server (${esc(srv)}) is ${esc(d.problemState || d.lsp?.state || 'starting')}.</div>`;
    return;
  }

  // Unavailable state: LSP is disabled or not found
  if (d.problemState === 'off' || (d.lsp && d.lsp.state === 'off')) {
    if (summaryEl) summaryEl.textContent = 'No Server';
    listEl.innerHTML = `<div class="hint prob-hint">No language server configured or available for <b>${esc(d.name)}</b>.<br><br>Language servers provide live compiler, type, and lint diagnostics.</div>`;
    return;
  }

  // Failed state: LSP crashed
  if (d.problemState === 'failed' || (d.lsp && d.lsp.state === 'failed')) {
    if (summaryEl) summaryEl.textContent = 'LSP Error';
    const msg = d.problemError || 'Language server failed to start.';
    listEl.innerHTML = `<div class="hint prob-hint prob-err-msg"><b>Language server error:</b><br>${esc(msg)}</div>`;
    return;
  }

  // No problems found state
  const allProblems = d.problems || [];
  if (allProblems.length === 0) {
    if (summaryEl) summaryEl.textContent = '0 Problems';
    listEl.innerHTML = `<div class="hint prob-hint prob-clean"><div class="prob-clean-icon">✓</div>No problems detected in <b>${esc(d.name)}</b>.</div>`;
    if (filterChangedBtn) filterChangedBtn.hidden = true;
    return;
  }

  // Count problems on changed lines
  const changedProblemsCount = allProblems.filter(p => isProblemOnChangedLine(d, p)).length;
  if (filterChangedBtn) {
    if (changedProblemsCount > 0) {
      filterChangedBtn.hidden = false;
      filterChangedBtn.textContent = `Changed lines (${changedProblemsCount})`;
    } else {
      filterChangedBtn.hidden = true;
      if (activeProblemFilter === 'changed') activeProblemFilter = 'all';
    }
  }

  // Apply filters
  let filtered = allProblems;
  if (activeProblemFilter === 'changed') {
    filtered = allProblems.filter(p => isProblemOnChangedLine(d, p));
  } else if (activeProblemFilter === 'errors') {
    filtered = allProblems.filter(p => p.severityNum === 1);
  }

  // Update summary header
  const counts = d.problemCounts || {};
  const parts = [];
  if (counts.error) parts.push(`${counts.error} error${counts.error === 1 ? '' : 's'}`);
  if (counts.warning) parts.push(`${counts.warning} warning${counts.warning === 1 ? '' : 's'}`);
  if (counts.info) parts.push(`${counts.info} info`);
  if (counts.hint) parts.push(`${counts.hint} hint${counts.hint === 1 ? '' : 's'}`);
  const summaryText = parts.length ? parts.join(', ') : `${allProblems.length} problem${allProblems.length === 1 ? '' : 's'}`;
  if (summaryEl) {
    summaryEl.textContent = summaryText;
    summaryEl.title = summaryText + (changedProblemsCount ? ` (${changedProblemsCount} on changed lines)` : '');
  }

  if (filtered.length === 0) {
    listEl.innerHTML = `<div class="hint prob-hint">No problems match the current filter.</div>`;
    return;
  }

  // Sort: changed lines first, then severity (error first), then line, then col
  const sorted = [...filtered].sort((a, b) => {
    const aCh = isProblemOnChangedLine(d, a);
    const bCh = isProblemOnChangedLine(d, b);
    if (aCh !== bCh) return aCh ? -1 : 1;
    if (a.severityNum !== b.severityNum) return a.severityNum - b.severityNum;
    if (a.line !== b.line) return a.line - b.line;
    return a.col - b.col;
  });

  // Separate into "On changed lines" and "Other problems" if not filtered to changed only and changed problems exist
  const onChanged = [];
  const otherLines = [];
  for (const p of sorted) {
    if (isProblemOnChangedLine(d, p)) onChanged.push(p);
    else otherLines.push(p);
  }

  let html = '';
  if (activeProblemFilter === 'all' && onChanged.length > 0 && otherLines.length > 0) {
    html += `<div class="prob-section-head">On changed lines (${onChanged.length})</div>`;
    for (const p of onChanged) html += renderProblemItemHtml(p, true);
    html += `<div class="prob-section-head">Other problems in file (${otherLines.length})</div>`;
    for (const p of otherLines) html += renderProblemItemHtml(p, false);
  } else {
    for (const p of sorted) {
      html += renderProblemItemHtml(p, isProblemOnChangedLine(d, p));
    }
  }

  listEl.innerHTML = html;
}

function renderProblemItemHtml(p, onDiff) {
  const sevClass = p.severity === 'error' ? 'prob-chip-error' : (p.severity === 'warning' ? 'prob-chip-warning' : 'prob-chip-info');
  const sevLabel = p.severity === 'error' ? 'Error' : (p.severity === 'warning' ? 'Warning' : (p.severity === 'info' ? 'Info' : 'Hint'));
  const diffBadge = onDiff ? '<span class="prob-badge-changed" title="Problem is on an added or modified line">Diff</span>' : '';
  const sourceCode = p.code || p.source ? `<span class="prob-code">${esc(p.source ? p.source + (p.code ? ` (${p.code})` : '') : p.code)}</span>` : '';

  let relatedHtml = '';
  if (p.related && p.related.length) {
    relatedHtml = '<div class="prob-related-list">';
    for (const r of p.related) {
      relatedHtml += `<div class="prob-related-item" data-jump-path="${esc(r.path)}" data-jump-line="${r.line}">` +
        `<span class="prob-related-target">${esc(r.path)}:${r.line}</span> ` +
        `<span class="prob-related-msg">${esc(r.message)}</span>` +
        `</div>`;
    }
    relatedHtml += '</div>';
  }

  return `<div class="prob-item" data-line="${p.line}" data-col="${p.col}" title="Jump to line ${p.line}">` +
    `<div class="prob-item-head">` +
      `<span class="prob-chip ${sevClass}">${sevLabel}</span>` +
      diffBadge +
      `<span class="prob-loc">Line ${p.line}:${p.col + 1}</span>` +
      sourceCode +
    `</div>` +
    `<div class="prob-msg">${esc(p.message)}</div>` +
    relatedHtml +
  `</div>`;
}

export function initProblems() {
  // Filter button clicks
  $('#prob-filters')?.addEventListener('click', e => {
    const btn = e.target.closest('[data-prob-filter]');
    if (!btn) return;
    setActiveProblemFilter(btn.dataset.probFilter);
  });

  // Jump to line when problem item or related location is clicked
  $('#prob-list')?.addEventListener('click', e => {
    const relJump = e.target.closest('[data-jump-path]');
    if (relJump) {
      e.stopPropagation();
      const p = relJump.dataset.jumpPath;
      const ln = +relJump.dataset.jumpLine || 1;
      openFile(p, { line: ln });
      return;
    }

    const item = e.target.closest('.prob-item');
    if (!item) return;
    const line = +item.dataset.line;
    const col = +item.dataset.col || 0;
    const d = doc_();
    if (!d || !line) return;

    d.cur = line;
    d.col = col;
    centerLine(line);
    render();
    pushHistory(d.path, d.cur);
  });
}
