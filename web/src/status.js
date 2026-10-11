import { $, S, doc_, api, apiPost, esc, withKeys } from './state.js';
import { previewing, previewKind } from './markdown.js';
import { layoutPref } from './diff.js';
import { copyToClipboard, showToast } from './ui.js';

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

  // Markdown and CSV/TSV tabs share the preview switch; only its label differs.
  const kind = previewKind(d), isMd = !!kind, shown = previewing(d);
  const label = kind === 'table' ? 'Table' : 'Preview';
  const mdBtn = $('[data-action="md-preview"]');
  if (mdBtn) {
    mdBtn.hidden = !isMd;
    mdBtn.classList.toggle('active', shown);
    const l = $('.footer-btn-label', mdBtn);
    if (l && isMd) l.textContent = label;
  }
  const sw = $('#md-switch');
  if (sw) {
    sw.hidden = !isMd;
    const pb = $('[data-md="preview"]', sw);
    if (pb && isMd) pb.textContent = label;
    document.body.classList.toggle('md-tab', isMd);
    for (const b of sw.children) {

      if (b.dataset.md) b.classList.toggle('on', isMd && (b.dataset.md === 'preview') === shown);
    }
    const copyMdBtn = $('#btn-copy-md');
    if (copyMdBtn) copyMdBtn.hidden = !isMd || kind !== 'markdown';
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
  if ('anyRunning' in j) S.lsp.anyRunning = j.anyRunning;
  drawLspStatus();
}

export function drawLspStatus() {
  const el = $('#st-lsp');
  if (!el) return;
  const anyRunning = S.lsp.anyRunning || S.lsp.state === 'ready' || S.lsp.state === 'indexing';
  el.textContent = 'LSP';
  if (anyRunning) {
    el.dataset.state = S.lsp.state === 'indexing' ? 'indexing' : 'ready';
    el.title = 'Language Servers: Active (Click to manage)';
  } else if (S.lsp.state === 'failed') {
    el.dataset.state = 'failed';
    el.title = 'Language Server failed (Click to manage)';
  } else {
    el.dataset.state = 'off';
    el.title = 'Language Servers: Stopped (Click to manage)';
  }
}

function shortLang(lang) {
  if (!lang) return '';
  const m = {
    'TypeScript and JavaScript': 'TS / JS',
    'TypeScript': 'TS',
    'JavaScript': 'JS',
    'C and C++': 'C / C++',
  };
  return m[lang] || lang;
}

const lspMenuEl = $('#lsp-menu');
let lastLspData = null;
let lspPollTimer = null;
let pollActive = false;
const installingServers = new Set();

export function startLspPoll() {
  if (lspPollTimer) return;
  lspPollTimer = setInterval(async () => {
    if (pollActive) return;
    pollActive = true;
    try {
      const data = await api('/api/lsp/servers');
      if (data) {
        S.lsp.anyRunning = data.anyRunning;
        drawLspStatus();
        if (lspMenuEl && !lspMenuEl.hidden) {
          renderLspMenu(data);
          placeLspMenu();
        }

        // Auto-start any server that completed background installation
        for (const s of data.servers || []) {
          if (installingServers.has(s.name) && s.installed && !s.running && s.state !== 'installing') {
            installingServers.delete(s.name);
            showToast('✓', `${s.name} installed. Starting…`);
            apiPost('/api/lsp/start', { server: s.name }).then(d => {
              if (d) {
                S.lsp.anyRunning = d.anyRunning;
                drawLspStatus();
                refreshLspMenu();
              }
            });
          }
        }

        const anyInstalling = (data.servers || []).some(s => s.state === 'installing');
        if (!anyInstalling && installingServers.size === 0) {
          clearInterval(lspPollTimer);
          lspPollTimer = null;
        }
      }
    } catch {}
    finally {
      pollActive = false;
    }
  }, 1200);
}

export function renderLspMenu(data) {
  if (!lspMenuEl || !data) return;
  lastLspData = data;
  const servers = data.servers || [];
  const anyRunning = !!data.anyRunning;

  let headAction = '';
  if (servers.length > 0) {
    if (anyRunning) {
      headAction = `<button class="lsp-menu-head-action" id="lsp-action-all" data-action="stop-all" title="Stop all running language servers">Stop All</button>`;
    } else {
      const anyInstalled = servers.some(s => s.installed);
      if (anyInstalled) {
        headAction = `<button class="lsp-menu-head-action" id="lsp-action-all" data-action="start-all" title="Start all available language servers">Start All</button>`;
      }
    }
  }

  let rows = '';
  if (servers.length === 0) {
    rows = `<div class="lsp-empty-note">No language servers detected for workspace</div>`;
  } else {
    rows = servers.map(s => {
      let dotClass = s.state || 'stopped';
      let metaText = '';
      let btnHtml = '';

      if (s.state === 'installing' || installingServers.has(s.name)) {
        dotClass = 'starting';
        metaText = 'Installing…';
        btnHtml = `<button class="lsp-btn" disabled>Installing…</button>`;
      } else if (s.running) {
        dotClass = s.state === 'indexing' ? 'indexing' : 'ready';
        metaText = s.memBytes ? fmtBytes(s.memBytes) : 'Running';
        btnHtml = `<button class="lsp-btn lsp-btn-stop" data-name="${esc(s.name)}" data-act="stop" title="Stop ${esc(s.name)}">Stop</button>`;
      } else if (s.state === 'starting') {
        dotClass = 'starting';
        metaText = 'Starting…';
        btnHtml = `<button class="lsp-btn lsp-btn-stop" data-name="${esc(s.name)}" data-act="stop" title="Stop ${esc(s.name)}">Stop</button>`;
      } else if (s.state === 'failed') {
        dotClass = 'failed';
        metaText = s.error ? esc(s.error) : 'Failed';
        btnHtml = `<button class="lsp-btn lsp-btn-start" data-name="${esc(s.name)}" data-act="start" title="Restart ${esc(s.name)}">Start</button>`;
      } else if (s.installed) {
        dotClass = 'stopped';
        metaText = 'Stopped';
        btnHtml = `<button class="lsp-btn lsp-btn-start" data-name="${esc(s.name)}" data-act="start" title="Start ${esc(s.name)}">Start</button>`;
      } else {
        dotClass = 'missing';
        if (s.error) {
          metaText = 'Install failed';
        } else {
          metaText = 'Not installed';
        }
        if (s.autoOpt !== undefined && s.autoOpt >= 0) {
          btnHtml = `<button class="lsp-btn lsp-btn-install" data-name="${esc(s.name)}" data-opt="${s.autoOpt}" data-act="install" title="Install ${esc(s.name)}">Install</button>`;
        } else if (s.options && s.options.length > 0) {
          btnHtml = `<button class="lsp-btn lsp-btn-copy" data-cmd="${esc(s.options[0].cmd)}" data-act="copy" title="Copy install command: ${esc(s.options[0].cmd)}">Copy Cmd</button>`;
        } else {
          btnHtml = `<span class="lsp-meta-dim">Manual</span>`;
        }
      }

      return `
        <div class="lsp-server-row">
          <div class="lsp-server-info">
            <span class="lsp-dot ${dotClass}"></span>
            <span class="lsp-server-name">${esc(s.name)}</span>
            <span class="lsp-server-lang">${esc(shortLang(s.lang))}</span>
          </div>
          <span class="lsp-server-meta" title="${esc(s.error || metaText)}">${metaText}</span>
          ${btnHtml}
        </div>
      `;
    }).join('');
  }

  lspMenuEl.innerHTML = `
    <div class="lsp-menu-head">
      <span>Language Servers</span>
      ${headAction}
    </div>
    <div class="lsp-server-list">
      ${rows}
    </div>
  `;

  if (servers.some(s => s.state === 'installing')) {
    startLspPoll();
  }
}

export function closeLspMenu() {
  if (lspMenuEl) lspMenuEl.hidden = true;
}

function placeLspMenu() {
  const contEl = $('#st-lsp');
  if (!contEl || !lspMenuEl) return;
  const r = contEl.getBoundingClientRect();
  lspMenuEl.style.bottom = (innerHeight - r.top + 6) + 'px';
  lspMenuEl.style.right = Math.max(8, innerWidth - r.right) + 'px';
  lspMenuEl.style.left = 'auto';
}

export async function refreshLspMenu() {
  try {
    const data = await api('/api/lsp/servers');
    if (data) {
      S.lsp.anyRunning = data.anyRunning;
      drawLspStatus();
      if (lspMenuEl && !lspMenuEl.hidden) {
        renderLspMenu(data);
        placeLspMenu();
      }
    }
  } catch {}
}

export async function openLspMenu() {
  if (!lspMenuEl) return;
  closeMetricsMenu();
  lspMenuEl.hidden = false;
  placeLspMenu();
  try {
    const data = await api('/api/lsp/servers');
    renderLspMenu(data);
    placeLspMenu();
    S.lsp.anyRunning = data.anyRunning;
    drawLspStatus();
  } catch {
    lspMenuEl.innerHTML = `<div class="lsp-empty-note">Failed to load language servers</div>`;
  }
}

export async function toggleLspMenu() {
  if (!lspMenuEl) return;
  if (!lspMenuEl.hidden) {
    closeLspMenu();
    return;
  }
  await openLspMenu();
}

export function initLspMenu() {
  const contEl = $('#st-lsp');
  if (contEl) {
    contEl.addEventListener('click', (e) => {
      e.stopPropagation();
      toggleLspMenu();
    });
    contEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        toggleLspMenu();
      }
    });
  }

  if (lspMenuEl) {
    lspMenuEl.addEventListener('click', async (e) => {
      const btn = e.target.closest('button');
      if (!btn) return;
      e.stopPropagation();

      if (btn.id === 'lsp-action-all') {
        const action = btn.dataset.action;
        btn.disabled = true;
        try {
          if (action === 'stop-all') {
            const data = await apiPost('/api/lsp/stop');
            if (data) {
              S.lsp.anyRunning = data.anyRunning;
              S.lsp.state = 'off';
              renderLspMenu(data);
              drawLspStatus();
            }
          } else if (action === 'start-all') {
            const data = await apiPost('/api/lsp/start', { server: 'all' });
            if (data) {
              S.lsp.anyRunning = data.anyRunning;
              renderLspMenu(data);
              drawLspStatus();
            }
          }
        } catch {}
        return;
      }

      const name = btn.dataset.name;
      const act = btn.dataset.act;
      if (!name || !act) return;

      btn.disabled = true;
      try {
        if (act === 'stop') {
          const data = await apiPost('/api/lsp/stop', { server: name });
          if (data) {
            S.lsp.anyRunning = data.anyRunning;
            if (S.lsp.server === name) S.lsp.state = 'off';
            renderLspMenu(data);
            drawLspStatus();
          }
        } else if (act === 'start') {
          const data = await apiPost('/api/lsp/start', { server: name });
          if (data) {
            S.lsp.anyRunning = data.anyRunning;
            renderLspMenu(data);
            drawLspStatus();
          }
        } else if (act === 'install') {
          const opt = btn.dataset.opt || '0';
          btn.textContent = 'Installing…';
          installingServers.add(name);
          try {
            await apiPost('/api/lsp/install', { server: name, option: opt });
            showToast('✓', `Installing ${name} in background…`);
            startLspPoll();
            await refreshLspMenu();
          } catch (err) {
            installingServers.delete(name);
            showToast('!', err.message || 'Install failed');
            await refreshLspMenu();
          }
        } else if (act === 'copy') {
          const cmd = btn.dataset.cmd;
          if (cmd) copyToClipboard(cmd, `Copied ${cmd}`, btn);
        }
      } catch {}
    });
  }

  addEventListener('click', (e) => {
    const target = /** @type {HTMLElement|null} */ (e.target);
    if (!target?.closest('#lsp-menu, #st-lsp')) closeLspMenu();
  });
  addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeLspMenu();
  });
  window.addEventListener('resize', () => {
    if (lspMenuEl && !lspMenuEl.hidden) placeLspMenu();
  });
}

const metricsMenuEl = $('#metrics-menu');
let lastMetrics = null;

function renderMetricsMenu(m) {
  if (!metricsMenuEl || !m) return;
  const lspRow = m.lspEnabled ? `
      <div class="metrics-row">
        <span class="metrics-label">Language Servers (RSS)</span>
        <span class="metrics-val">${fmtBytes(m.lspMemBytes || 0)}</span>
      </div>` : '';
  metricsMenuEl.innerHTML = `
    <div class="metrics-title">
      <span>Process Metrics</span>
      <span class="toast-chip">px0</span>
    </div>
    <div class="metrics-grid">
      <div class="metrics-row">
        <span class="metrics-label">Resident RAM (RSS)</span>
        <span class="metrics-val">${fmtBytes(m.rssBytes || 0)}</span>
      </div>
      <div class="metrics-row">
        <span class="metrics-label">CPU Usage</span>
        <span class="metrics-val">${(m.cpuUsage != null ? m.cpuUsage : 0).toFixed(1)}%</span>
      </div>
      <div class="metrics-row">
        <span class="metrics-label">Active Goroutines</span>
        <span class="metrics-val">${m.goroutines || 0}</span>
      </div>${lspRow}
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
  if (cpuEl) cpuEl.textContent = `${(m.cpuUsage != null ? m.cpuUsage : 0).toFixed(1)}%`;
  if (ramEl) ramEl.textContent = fmtBytes(m.rssBytes || 0);
  const lspWrap = $('#st-lspmem-wrap');
  const lspEl = $('#st-lspmem');
  if (lspWrap) lspWrap.hidden = !m.lspEnabled;
  if (lspEl && m.lspEnabled) lspEl.textContent = fmtBytes(m.lspMemBytes || 0);
  if (m.lspEnabled !== undefined) {
    if (m.lspMemBytes > 0) {
      S.lsp.anyRunning = true;
    } else if (S.lsp.state !== 'indexing' && S.lsp.state !== 'starting') {
      S.lsp.anyRunning = false;
    }
    drawLspStatus();
  }
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
    const target = /** @type {HTMLElement|null} */ (e.target);
    if (!target?.closest('#metrics-menu, #st-metrics')) closeMetricsMenu();
  });
  addEventListener('keydown', (e) => {
    if (e.key === 'Escape') closeMetricsMenu();
  });
}

/* The status bar stays on one line. When its contents outgrow the width, it
   sheds detail in steps (see the fit-N rules in style.css), least useful first,
   stopping at the first step that fits. A step comes back only with FIT_SLACK px
   to spare, so a 1px wobble of a resizer cannot flip it on every move. */
const FIT_STEPS = 6, FIT_SLACK = 16;
const statusEl = $('#status');
const growEl = $('.grow', statusEl);
let fitLevel = 0;

/* Apply fit level n and return the room left: the .grow spacer's width, or the
   overflow as a negative number. */
function applyFit(n) {
  for (let i = 1; i <= FIT_STEPS; i++) statusEl.classList.toggle('fit-' + i, i <= n);
  return growEl.offsetWidth - (statusEl.scrollWidth - statusEl.clientWidth);
}

export function fitStatus() {
  let room = applyFit(fitLevel);
  while (fitLevel < FIT_STEPS && room < 0) room = applyFit(++fitLevel);
  // Showing more only ever shrinks the room, so stop as soon as it is short.
  while (fitLevel > 0 && room >= FIT_SLACK) {
    room = applyFit(fitLevel - 1);
    if (room < FIT_SLACK) { applyFit(fitLevel); return; }
    fitLevel--;
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
