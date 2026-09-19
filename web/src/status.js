import { $, S, doc_, api, withKeys } from './state.js';
import { previewing } from './markdown.js';
import { layoutPref } from './diff.js';

export function updateStatus() {
  const d = doc_();
  const sizeEl = $('#st-size');
  if (sizeEl) sizeEl.textContent = d ? fmtBytes(d.size) : '';

  if (d && d.isImage) {
    const posEl = $('#st-pos');
    if (posEl) {
      const zoomText = d.imageFit ? `Fit (${Math.round((d.imageScale || 1) * 100)}%)` : `${Math.round((d.imageScale || 1) * 100)}%`;
      posEl.textContent = d.imageMeta ? `${d.imageMeta.width} × ${d.imageMeta.height} px · ${zoomText}` : zoomText;
    }
  }

  const isMd = !!(d && d.markdown), shown = previewing(d);
  const mdBtn = $('[data-action="md-preview"]');
  if (mdBtn) {
    mdBtn.hidden = !isMd;
    mdBtn.classList.toggle('active', shown);
  }
  const sw = $('#md-switch');
  if (sw) {
    sw.hidden = !isMd;
    document.body.classList.toggle('md-tab', isMd);
    for (const b of sw.children) b.classList.toggle('on', isMd && (b.dataset.md === 'preview') === shown);
  }

  const isCode = d && !d.isImage;
  const inGit = !!S.meta?.git;
  const hasDiff = !!(d && d.diffAvailable);
  const isDiffOn = !!(d && d.diffMode);
  const currentLayout = (d && d.diffMode) || layoutPref();
  const dsw = $('#diff-switch');
  if (dsw) {
    const showSwitch = inGit && isCode;
    dsw.hidden = !showSwitch;
    document.body.classList.toggle('diff-tab', hasDiff);
    const btn = $('#diff-btn');
    if (btn) {
      const gitBase = S.meta?.gitBase || 'HEAD';
      btn.disabled = !hasDiff;
      btn.classList.toggle('disabled', !hasDiff);
      btn.classList.toggle('on', hasDiff && isDiffOn);
      btn.title = hasDiff
        ? withKeys(`Show changes against ${gitBase}, ${currentLayout === 'unified' ? 'unified' : 'split'} ({Mod+D})`)
        : 'There are no git modified files.';
    }
    const srcBtn = $('#diff-source');
    if (srcBtn) {
      srcBtn.classList.toggle('on', !hasDiff || !isDiffOn);
      srcBtn.title = withKeys('Show the file ({Mod+D})');
    }
    const menuItems = dsw.querySelectorAll('.diff-menu-item');
    for (const item of menuItems) {
      item.classList.toggle('active', item.dataset.diffOpt === currentLayout);
    }
  }

  const verEl = $('#st-ver');
  if (verEl && S.meta?.version) {
    verEl.textContent = 'v' + S.meta.version;
    verEl.title = `px0 v${S.meta.version} (Click for shortcuts & help)`;
  }
  drawLspStatus();
}

let noteTimer = null;

export function setStatusNote(msg, timeoutMs = 0) {
  if (noteTimer) {
    clearTimeout(noteTimer);
    noteTimer = null;
  }
  const el = $('#st-pos');
  if (el) el.textContent = msg || '';
  if (msg && timeoutMs > 0) {
    noteTimer = setTimeout(() => {
      if (el && el.textContent === msg) el.textContent = '';
      noteTimer = null;
    }, timeoutMs);
  }
}

export function fmtBytes(n) {
  if (n < 1024) return n + ' B';
  if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1048576).toFixed(1) + ' MB';
}

export function setLspState(j) {
  if (!j || !j.state) return;
  S.lsp.state = j.state;
  S.lsp.server = j.server || S.lsp.server;
  // Only file, warm and start replies say what is missing; any running server means nothing is.
  if ('missing' in j || j.state !== 'off') S.lsp.missing = j.missing || '';
  drawLspStatus();
}

export function drawLspStatus() {
  const el = $('#st-lsp');
  const { state, server, missing } = S.lsp;
  el.title = '';
  if (state === 'off' && missing) {
    el.dataset.state = 'missing';
    el.textContent = 'LSP: set up';
    el.title = 'No language server for ' + missing + '. Click to install or start one.';
    return;
  }
  if (!server || state === 'off') { el.textContent = ''; el.removeAttribute('data-state'); return; }
  el.dataset.state = state;
  el.textContent = state === 'ready' ? server : server + ' ' + state;
  if (state === 'failed') el.title = 'The language server did not start. Click for details.';
}

const metricsMenuEl = $('#metrics-menu');
let lastMetrics = null;

function renderMetricsMenu(m) {
  if (!metricsMenuEl || !m) return;
  metricsMenuEl.innerHTML = `
    <div class="metrics-title">
      <span>Process Metrics</span>
      <span class="toast-chip">px0</span>
    </div>
    <div class="metrics-grid">
      <div class="metrics-row">
        <span class="metrics-label">Resident RAM (RSS)</span>
        <span class="metrics-val">${fmtBytes(m.rssBytes)}</span>
      </div>
      <div class="metrics-row">
        <span class="metrics-label">CPU Usage</span>
        <span class="metrics-val">${m.cpuUsage.toFixed(1)}%</span>
      </div>
      <div class="metrics-row">
        <span class="metrics-label">Active Goroutines</span>
        <span class="metrics-val">${m.goroutines || 0}</span>
      </div>
    </div>
  `;
}

export function closeMetricsMenu() {
  if (metricsMenuEl) metricsMenuEl.hidden = true;
}

function placeMetricsMenu() {
  const contEl = $('#st-metrics');
  if (!contEl || !metricsMenuEl) return;
  const r = contEl.getBoundingClientRect();
  metricsMenuEl.style.bottom = (innerHeight - r.top + 6) + 'px';
  metricsMenuEl.style.right = Math.max(8, innerWidth - r.right) + 'px';
  metricsMenuEl.style.left = 'auto';
}

export function toggleMetricsMenu() {
  if (!metricsMenuEl) return;
  if (!metricsMenuEl.hidden) {
    closeMetricsMenu();
    return;
  }
  if (lastMetrics) renderMetricsMenu(lastMetrics);
  metricsMenuEl.hidden = false;
  placeMetricsMenu();
  refreshMetrics();
}

export function updateMetricsDisplay(m) {
  if (!m) return;
  lastMetrics = m;
  const cpuEl = $('#st-cpu');
  const ramEl = $('#st-ram');
  if (cpuEl) cpuEl.textContent = `${m.cpuUsage.toFixed(1)}%`;
  if (ramEl) ramEl.textContent = fmtBytes(m.rssBytes);
  if (metricsMenuEl && !metricsMenuEl.hidden) {
    renderMetricsMenu(m);
    placeMetricsMenu();
  }
}

export async function refreshMetrics() {
  try {
    const m = await api('/api/metrics');
    updateMetricsDisplay(m);
  } catch {}
}

export function initMetrics() {
  const contEl = $('#st-metrics');
  if (contEl) {
    contEl.addEventListener('click', (e) => {
      e.stopPropagation();
      toggleMetricsMenu();
    });
    contEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        toggleMetricsMenu();
      }
    });
  }
  addEventListener('click', (e) => {
    if (!e.target.closest('#metrics-menu, #st-metrics')) closeMetricsMenu();
  });
  addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeMetricsMenu();
  });

  refreshMetrics();
  setInterval(refreshMetrics, 2500);
}

/* The status bar stays on one line. When its contents outgrow the width, it
   sheds detail in steps (see the fit-N rules in style.css), least useful first,
   stopping at the first step that fits. */
const FIT_STEPS = 6;
const statusEl = $('#status');

export function fitStatus() {
  for (let i = 1; i <= FIT_STEPS; i++) statusEl.classList.remove('fit-' + i);
  for (let i = 1; i <= FIT_STEPS && statusEl.scrollWidth > statusEl.clientWidth; i++) {
    statusEl.classList.add('fit-' + i);
  }
}

export function initStatusFit() {
  // Width changes come from the window and the sidebar resizers; content changes
  // from metrics, LSP state and the selection bar. Class changes are not observed,
  // so fitStatus() toggling them cannot re-trigger itself.
  new ResizeObserver(fitStatus).observe(statusEl);
  new MutationObserver(fitStatus).observe(statusEl, { childList: true, subtree: true, characterData: true });
  document.fonts?.ready.then(fitStatus);
}
