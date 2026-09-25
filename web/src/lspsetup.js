// web/src/lspsetup.js
import { esc, S, doc_, api, apiPost } from './state.js';
import { copyToClipboard, showToast } from './ui.js';
import { setLspState, updateStatus } from './status.js';
import { warmLSP } from './lsp.js';

/* With no language server running for the open file, call trails are a dead
   end. This panel says why and offers the fix in place: run a known installer,
   or pick up a server installed by hand, then start it and carry on. */

let setupSeq = 0;
let pollTimer = 0;

const hintHtml = html => '<div class="hint">' + html + '</div>';

// Stops a pending refresh, so it cannot draw over whatever replaced the panel.
export function cancelLspSetup() {
  setupSeq++;
  clearTimeout(pollTimer);
}

export async function renderLspSetup(el, onReady) {
  const d = doc_();
  if (!el || !d) return;
  cancelLspSetup();
  const my = setupSeq;
  let s;
  try { s = await api('/api/lsp/setup', { path: d.path }); }
  catch (e) { if (my === setupSeq) el.innerHTML = hintHtml('Could not check language servers: ' + esc(e.message)); return; }
  if (my !== setupSeq || doc_() !== d) return;

  const again = ms => { pollTimer = setTimeout(() => { if (my === setupSeq) renderLspSetup(el, onReady); }, ms); };
  if (s.state === 'starting' && !s.server) {
    el.innerHTML = hintHtml('Looking for language servers…');
    again(700);
    return;
  }
  // Installed (just now, or all along) but this page has not caught up: start it.
  if (s.state !== 'off' && s.state !== 'failed') { start(el, d, onReady); return; }

  el.innerHTML = drawSetup(s, d);
  wire(el, d, onReady);
  if (s.servers.some(v => v.job && v.job.running)) again(1000);
}

async function start(el, d, onReady) {
  cancelLspSetup();
  el.innerHTML = hintHtml('Starting the language server…');
  let j;
  try { j = await apiPost('/api/lsp/start', { path: d.path }); }
  catch (e) { el.innerHTML = hintHtml('Could not start the language server: ' + esc(e.message)); return; }
  if (doc_() !== d) return;
  // Other open files may have been waiting on the same server: let them ask again.
  for (const t of S.tabs) {
    if (t !== d && t.lsp && (t.lsp.state === 'off' || t.lsp.state === 'failed')) t.lsp = { state: 'starting', server: '' };
  }
  d.lsp = { state: j.state, server: j.server, missing: j.missing || '' };
  setLspState(j);
  updateStatus();
  warmLSP(d);
  if (j.state === 'off' || j.state === 'failed') { renderLspSetup(el, onReady); return; }
  if (onReady) onReady();
}

function drawSetup(s, d) {
  const ext = (d.path.match(/\.[^./]+$/) || [d.name])[0];
  if (!s.enabled) {
    return hintHtml('Language servers are turned off: px0 was started with <b>-no-lsp</b>. ' +
      'Restart it without that flag for call trails, hover and precise references.');
  }
  if (!s.servers.length) {
    return hintHtml('px0 knows no language server for <b>' + esc(ext) + '</b> files, so call trails are not available here.');
  }

  const offer = s.servers.filter(v => v.options.length || v.job);
  const running = s.servers.some(v => v.job && v.job.running);
  let html = '<div class="lsp-setup">';
  if (s.state === 'failed') {
    html += '<p><b>' + esc(s.server) + '</b> did not start: <span class="lsp-reason">' + esc(s.reason || 'unknown error') + '</span></p>' +
      '<div class="lsp-row"><button class="lsp-btn" data-start title="Retry starting language server">Retry</button></div>';
    if (offer.length) html += '<p>If it is broken or incomplete, install it again:</p>';
  } else {
    html += '<p>Call trails, hover and precise references for ' + esc(s.lang) + ' need a language server, and none is installed.</p>';
  }

  for (const v of offer) {
    html += '<div class="lsp-server"><div class="lsp-name">' + esc(v.name) + '</div>';
    v.options.forEach((o, i) => {
      html += '<div class="lsp-opt"><code>' + esc(o.cmd) + '</code><span class="lsp-acts">';
      if (!o.auto) html += '<span class="lsp-need">run in a terminal</span>';
      else if (!o.hasTool) html += '<span class="lsp-need">needs ' + esc(o.tool) + '</span>';
      else html += '<button class="lsp-btn primary" data-install="' + esc(v.name) + '" data-option="' + i + '"' + (running ? ' disabled' : '') + ' title="Install language server">Install</button>';
      html += '<button class="lsp-btn" data-copy="' + esc(o.cmd) + '" title="Copy command to clipboard">Copy</button></span></div>';
    });
    if (v.job) html += job(v.job);
    html += '</div>';
  }
  if (!offer.length) {
    html += '<p>px0 has no installer for this one. Install ' + s.servers.map(v => '<b>' + esc(v.name) + '</b>').join(' or ') +
      ' and make sure it is on PATH.</p>';
  }
  html += '<div class="lsp-row"><span>Installed one yourself?</span><button class="lsp-btn" data-start title="Detect and start language server">Detect and start</button></div></div>';
  return html;
}

function job(j) {
  const tail = (j.log || '').trimEnd().split('\n').slice(-12).join('\n');
  const log = tail ? '<pre>' + esc(tail) + '</pre>' : '';
  if (j.running) return '<div class="lsp-job">Installing with <code>' + esc(j.cmd) + '</code>…' + log + '</div>';
  if (j.error) return '<div class="lsp-job err">Install failed: ' + esc(j.error) + log + '</div>';
  return '';
}

function wire(el, d, onReady) {
  el.querySelectorAll('[data-install]').forEach(b => b.addEventListener('click', async () => {
    el.querySelectorAll('[data-install]').forEach(x => { x.disabled = true; });
    try { await apiPost('/api/lsp/install', { server: b.dataset.install, option: b.dataset.option }); }
    catch (e) { showToast('!', e.message); }
    renderLspSetup(el, onReady);
  }));
  el.querySelectorAll('[data-copy]').forEach(b => b.addEventListener('click', () => {
    copyToClipboard(b.dataset.copy, 'Copied ' + b.dataset.copy, b);
  }));
  el.querySelectorAll('[data-start]').forEach(b => b.addEventListener('click', () => start(el, d, onReady)));
}
