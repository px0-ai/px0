// web/src/calls.js
import { $, $$, esc, S, doc_, api, keyLabel } from './state.js';
import { updateStatus, setStatusNote, setLspState } from './status.js';
import { openFile } from './tabs.js';
import { showRightInspector } from './inspector.js';
import { positionNow, flashFind } from './lsp.js';
import { renderLspSetup, cancelLspSetup } from './lspsetup.js';

/* Call trail: the language server's call hierarchy, grown one level at a time
   as the reader expands it. Callers walk up toward entry points, callees walk
   down toward leaves. Each node keeps the server's opaque item so the next
   level can be asked for without the server remembering anything. */

let T = null;       // { path, word, dir, roots: [node] }
let dirPref = 'in'; // 'in' = callers, 'out' = callees
let callSeq = 0;
const flat = [];    // node by row index, rebuilt on every draw

const listEl = () => $('#right-calls-list');
const hint = html => { const el = listEl(); if (el) el.innerHTML = '<div class="hint">' + html + '</div>'; };
const base = p => p.split('/').pop();
// Some servers crash on particular call hierarchy requests; say so plainly.
const explain = msg => /connection lost|exited|EOF/i.test(msg)
  ? msg + ' (the language server crashed answering this; px0 restarts it on the next request)'
  : msg;

function wrap(n, parent) {
  let cycle = false;
  for (let p = parent; p; p = p.parent) {
    if (p.n.path === n.path && p.n.line === n.line && p.n.name === n.name) { cycle = true; break; }
  }
  return { n, parent, kids: null, open: false, loading: false, err: '', cycle };
}

/* Callers jump to the line that makes the call; callees to their declaration. */
function target(node) {
  const n = node.n;
  if (T.dir === 'in' && n.sites && n.sites.length) return { path: n.sitePath, line: n.sites[0] };
  return { path: n.path, line: n.line };
}

export async function showCalls(arg) {
  const d = doc_();
  const at = (arg && arg.word) ? arg : positionNow(typeof arg === 'string' ? arg : S.lastWord);
  showRightInspector('calls');
  cancelLspSetup();
  if (!d) return;
  // Without a server there is nothing to trace: offer to install or start one, then come back here.
  if (S.lsp.state === 'off' || S.lsp.state === 'failed') {
    T = null;
    $('#right-calls-target').textContent = at ? at.word : '-';
    renderLspSetup(listEl(), () => showCalls(arg));
    return;
  }
  if (!at || at.imprecise) { hint('Click a function name in the editor, then press <b>' + esc(keyLabel('Alt+Shift+H')) + '</b>.'); return; }

  const my = ++callSeq;
  T = null;
  $('#right-calls-target').textContent = at.word;
  hint('Tracing calls for "' + esc(at.word) + '"…');
  setStatusNote('call trail for ' + at.word + '…', 8000);
  let j;
  try {
    j = await api('/api/lsp/calls', { path: d.path, line: at.line, col: at.col, wait: S.lsp.state === 'ready' ? 10000 : 30000 });
  } catch (e) {
    if (my === callSeq) { updateStatus(); setStatusNote(''); hint('Could not trace "' + esc(at.word) + '": ' + esc(explain(e.message))); }
    return;
  }
  if (my !== callSeq) return;
  setLspState(j);
  updateStatus();
  setStatusNote('');
  if (!j.nodes || !j.nodes.length) {
    hint('"' + esc(at.word) + '" is not a function ' + esc(j.server || 'the language server') + ' can trace.');
    return;
  }
  T = { path: d.path, word: at.word, dir: dirPref, roots: j.nodes.map(n => wrap(n, null)) };
  for (const r of T.roots) expand(r);
}

async function expand(node) {
  if (node.cycle) return;
  node.open = true;
  if (node.kids) { draw(); return; }
  node.loading = true;
  draw();
  const t = T, dir = t.dir;
  try {
    const j = await api('/api/lsp/calls', { path: t.path, item: node.n.item, dir, wait: 30000 });
    if (t !== T || dir !== T.dir) return;
    node.kids = (j.nodes || []).map(n => wrap(n, node));
  } catch (e) {
    if (t !== T || dir !== T.dir) return;
    node.err = explain(e.message);
    node.kids = [];
  }
  node.loading = false;
  draw();
}

function setDir(dir) {
  dirPref = dir;
  $$('#calls-dir [data-dir]').forEach(b => b.classList.toggle('on', b.dataset.dir === dir));
  if (!T || T.dir === dir) return;
  T.dir = dir;
  for (const r of T.roots) Object.assign(r, { kids: null, open: false, loading: false, err: '' });
  for (const r of T.roots) expand(r);
}

function draw() {
  const el = listEl();
  if (!el || !T) return;
  flat.length = 0;
  const none = T.dir === 'in' ? 'no callers found' : 'calls nothing traceable';
  let html = '';
  const walk = (node, depth) => {
    const i = flat.push(node) - 1;
    const n = node.n, t = target(node);
    const arrow = node.cycle ? '&#8635;' : node.loading ? '&#8230;' : node.open ? '&#9660;' : '&#9654;';
    const calls = n.sites && n.sites.length > 1 ? ' &times;' + n.sites.length : '';
    const tip = t.path + ':' + t.line + (node.cycle ? '\n(recursive, already in this trail)' : '') + (n.detail ? '\n' + n.detail : '');
    html += '<div class="sym cnode" data-i="' + i + '" style="padding-left:' + (6 + depth * 14) + 'px" title="' + esc(tip) + '">' +
      '<span class="car' + (node.cycle ? ' cyc' : '') + '">' + arrow + '</span>' +
      '<span class="kd" data-k="' + esc(n.kind) + '">' + esc(n.kind) + '</span>' +
      '<span class="sn">' + esc(n.name) + '</span>' +
      '<span class="sl">' + esc(base(t.path)) + ':' + t.line + calls + '</span></div>';
    const pad = 'style="padding-left:' + (26 + (depth + 1) * 14) + 'px"';
    if (node.err) html += '<div class="cnone" ' + pad + '>' + esc(node.err) + '</div>';
    else if (node.open && node.kids && !node.kids.length) html += '<div class="cnone" ' + pad + '>' + none + '</div>';
    if (node.open && node.kids) for (const k of node.kids) walk(k, depth + 1);
  };
  for (const r of T.roots) walk(r, 0);
  el.innerHTML = html;
}

// Opens the setup panel whatever the server's state, for the palette command.
export function openLspSetup() {
  showRightInspector('calls');
  T = null;
  renderLspSetup(listEl(), () => showCalls(S.at));
}

export function initCalls() {
  $('#calls-dir')?.addEventListener('click', e => {
    const b = e.target.closest('[data-dir]');
    if (b) setDir(b.dataset.dir);
  });

  $('.inspector-tab[data-itab="calls"]')?.addEventListener('click', () => {
    if (!T && (S.at || S.lsp.state === 'off' || S.lsp.state === 'failed')) showCalls(S.at);
  });

  // The status bar names a missing or failed server; clicking it goes to the fix.
  $('#st-lsp')?.addEventListener('click', () => {
    if (S.lsp.missing || S.lsp.state === 'failed') openLspSetup();
  });

  listEl()?.addEventListener('click', async e => {
    const row = e.target.closest('.cnode');
    if (!row) return;
    const node = flat[+row.dataset.i];
    if (!node) return;
    if (e.target.closest('.car')) {
      if (node.open) { node.open = false; draw(); } else expand(node);
      return;
    }
    $$('#right-calls-list .cnode.sel').forEach(x => x.classList.remove('sel'));
    row.classList.add('sel');
    const t = target(node);
    await openFile(t.path, { line: t.line });
    // At a call site the name worth marking is the function being called.
    const called = T && T.dir === 'in' && node.parent ? node.parent.n.name : node.n.name;
    flashFind(called);
  });
}
