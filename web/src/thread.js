// web/src/thread.js
import { $, $$, esc, doc_, api, apiPost, apiPostJson, applyKeyLabels } from './state.js';
import { on } from './bus.js';
import { showToast, copyToClipboard } from './ui.js';
import { openFile } from './tabs.js';
import { setThreadHandler, hideSelectionBar } from './selbar.js';
import { showRightInspector } from './inspector.js';
import { registerAgentPicker, reloadWorkspace } from './agent.js';

/* A thread is a long-running conversation with the coding harness, kept on the
   server. It starts from a spot in the code but is not limited to it: the
   harness may read and change any file, and each turn reports the files it
   touched beside its written reply. Every message continues the same
   conversation, so this is where to ask what code does and follow up, rather
   than one-shot inline edits.

   The right sidebar has two views: the list of threads, and one open thread
   (or a new draft) with its transcript and composer. Two event streams feed it:
   one for the list, which also tells the editor when a finished turn changed
   files so open tabs reload, and one per open thread, carrying the reply as it
   is written. Both are server-sent events, so a dropped connection simply
   reconnects to a fresh snapshot. */

const thr = {
  avail: false,
  list: [],
  filter: 'all',      // all | file
  cur: null,          // the open thread, as last received
  draft: null,        // {path, l1, l2} for a thread not yet created, or {} for a workspace-level one
  listEs: null,
  threadEs: null,
  sending: false,
};

const thrUrl = p => new URL(p, document.baseURI || location.href).href;
const thrRefText = t => t.path ? t.path + ':' + (t.l1 === t.l2 ? t.l1 : t.l1 + '-' + t.l2) : 'Workspace';

function thrAgo(ms) {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 60) return 'just now';
  if (s < 3600) return Math.floor(s / 60) + 'm ago';
  if (s < 86400) return Math.floor(s / 3600) + 'h ago';
  return Math.floor(s / 86400) + 'd ago';
}

function thrDur(ms) {
  return ms < 1000 ? ms + 'ms' : ms < 60000 ? (ms / 1000).toFixed(1) + 's' : Math.floor(ms / 60000) + 'm ' + Math.round((ms % 60000) / 1000) + 's';
}

/* ---------- Markdown renderer for thread messages ----------
   Safe, streaming-friendly Markdown rendering supporting headings, code fences
   with syntax badge and copy button, blockquotes with GitHub alerts, ordered and
   unordered lists with task checkboxes and nesting, tables, links (with local file
   navigation), autolinks, images, horizontal rules, and inline styling. */
function thrSafeUrl(u) {
  u = u.trim();
  if (/^(?:https?|mailto):/i.test(u)) return esc(u);
  if (/^[a-zA-Z0-9_\-./]+(?::\d+)?(?:#.*)?$/.test(u)) return esc(u);
  if (u.startsWith('#')) return esc(u);
  return '';
}

function thrInline(src) {
  if (!src) return '';
  const codes = [];
  // 1. Extract inline code spans first so nothing inside is formatted
  let s = src.replace(/(`+)([\s\S]*?[^`])\1(?!`)/g, (_, q, code) => {
    codes.push('<code>' + esc(code.trim()) + '</code>');
    return '%%PXCODE' + (codes.length - 1) + '%%';
  });

  // Escape HTML in the remaining text
  s = esc(s);

  // 2. Bold, italic, strikethrough
  s = s.replace(/(\*\*\*|___)([^\n]+?)\1/g, '<strong><em>$2</em></strong>');
  s = s.replace(/\*\*([^*\n]+?)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/(^|[^a-zA-Z0-9])__([^\n]+?)__(?![a-zA-Z0-9])/g, '$1<strong>$2</strong>');
  s = s.replace(/\*([^*\n]+?)\*/g, '<em>$1</em>');
  s = s.replace(/(^|[^a-zA-Z0-9])_([^\n]+?)_(?![a-zA-Z0-9])/g, '$1<em>$2</em>');
  s = s.replace(/~~(.+?)~~/g, '<del>$1</del>');

  // 3. Links & images (use placeholders so bare autolinks do not match inside href/src attributes)
  const links = [];
  const pushLink = html => {
    links.push(html);
    return '%%PXLINK' + (links.length - 1) + '%%';
  };

  // Images: ![alt](url)
  s = s.replace(/!\[([^\]]*)\]\(((?:[^()]+|\([^()]*\))*)\)/g, (_, alt, url) => {
    const u = thrSafeUrl(url);
    return u ? pushLink(`<img src="${u}" alt="${esc(alt)}" class="thr-img" />`) : esc(alt);
  });

  // Links: [text](url)
  s = s.replace(/\[([^\]]+)\]\(((?:[^()]+|\([^()]*\))*)\)/g, (_, text, url) => {
    const u = thrSafeUrl(url);
    if (!u) return text;
    const m = /^([a-zA-Z0-9_.\-/]+\.[a-zA-Z0-9]+)(?::(\d+))?$/.exec(url.trim());
    if (m && !url.includes('://')) {
      return pushLink(`<a href="#" class="thr-link" data-path="${esc(m[1])}"` + (m[2] ? ` data-line="${m[2]}"` : '') + `>${text}</a>`);
    }
    return pushLink(`<a href="${u}" target="_blank" rel="noopener noreferrer">${text}</a>`);
  });

  // 4. Bracketed autolinks: <https://...>
  s = s.replace(/&lt;(https?:\/\/[^&>]+)&gt;/g, (_, url) => {
    const u = thrSafeUrl(url);
    return u ? pushLink(`<a href="${u}" target="_blank" rel="noopener noreferrer">${u}</a>`) : url;
  });

  // 5. Bare autolinks
  s = s.replace(/(^|[\s(])(https?:\/\/[^\s<)]+)/g, (_, prefix, url) => {
    let trailing = '';
    const m = /[.,:;?!]+$/.exec(url);
    if (m) {
      trailing = m[0];
      url = url.slice(0, -trailing.length);
    }
    const u = thrSafeUrl(url);
    return u ? `${prefix}` + pushLink(`<a href="${u}" target="_blank" rel="noopener noreferrer">${u}</a>`) + trailing : prefix + url + trailing;
  });

  // 6. Restore links & codes
  s = s.replace(/%%PXLINK(\d+)%%/g, (_, idx) => links[+idx]);
  s = s.replace(/%%PXCODE(\d+)%%/g, (_, idx) => codes[+idx]);

  return s;
}

function thrMd(src) {
  if (!src) return '';
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  let html = '';
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    if (!line.trim()) { i++; continue; }

    // Fenced code block: ``` or ~~~
    const fenceMatch = /^( {0,3})(`{3,}|~{3,})(.*)$/.exec(line);
    if (fenceMatch) {
      const marker = fenceMatch[2][0];
      const markerLen = fenceMatch[2].length;
      const rawLang = fenceMatch[3].trim().split(/\s+/)[0] || '';
      const lang = /^[\w+#.-]+$/.test(rawLang) ? rawLang : '';
      const codeLines = [];
      i++;
      while (i < lines.length) {
        const endMatch = new RegExp('^( {0,3})' + marker + '{' + markerLen + ',}\\s*$').exec(lines[i]);
        if (endMatch) { i++; break; }
        codeLines.push(lines[i]);
        i++;
      }
      const code = esc(codeLines.join('\n'));
      html += `<div class="thr-pre"` + (lang ? ` data-lang="${esc(lang)}"` : '') + `><pre><code>` + code + `</code></pre><button class="thr-copy" title="Copy code" aria-label="Copy code"><svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"><rect x="5.5" y="5.5" width="8" height="8" rx="1.5"/><path d="M10.5 3.5V3a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5"/></svg></button></div>`;
      continue;
    }

    // Heading: #{1,6}
    const hMatch = /^(#{1,6})\s+(.*)$/.exec(line);
    if (hMatch) {
      const level = hMatch[1].length;
      html += `<h${level}>` + thrInline(hMatch[2]) + `</h${level}>`;
      i++;
      continue;
    }

    // Horizontal rule: ---, ***, ___
    if (/^( {0,3})([-*_])(?:\s*\2){2,}\s*$/.test(line)) {
      html += '<hr>';
      i++;
      continue;
    }

    // Blockquote: >
    if (/^( {0,3})>(?: (.*)|(.*))$/.test(line)) {
      const qLines = [];
      while (i < lines.length) {
        const qm = /^( {0,3})>(?: (.*)|(.*))$/.exec(lines[i]);
        if (qm) {
          qLines.push(qm[2] !== undefined ? qm[2] : qm[3] || '');
          i++;
        } else if (lines[i].trim() && !/^( {0,3})([#`~*-]|\d+\.)/.test(lines[i])) {
          qLines.push(lines[i]);
          i++;
        } else {
          break;
        }
      }
      let alertClass = '';
      let alertTitle = '';
      if (qLines.length > 0) {
        const am = /^\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$/i.exec(qLines[0]);
        if (am) {
          const kind = am[1].toLowerCase();
          const titles = { note: 'Note', tip: 'Tip', important: 'Important', warning: 'Warning', caution: 'Caution' };
          alertClass = ' thr-alert thr-alert-' + kind;
          alertTitle = `<p class="thr-alert-title">${titles[kind]}</p>`;
          qLines.shift();
        }
      }
      const inner = thrMd(qLines.join('\n'));
      html += `<blockquote class="${alertClass}">${alertTitle}${inner}</blockquote>`;
      continue;
    }

    // Table
    if (line.includes('|') && i + 1 < lines.length) {
      const nextLine = lines[i + 1];
      const isDelim = /^\s*\|?(\s*:?-+:?\s*\|)+\s*:?-+:?\s*\|?\s*$/.test(nextLine);
      if (isDelim) {
        const splitRow = r => {
          let s = r.trim();
          if (s.startsWith('|')) s = s.slice(1);
          if (s.endsWith('|')) s = s.slice(0, -1);
          return s.split('|').map(c => c.trim());
        };
        const headers = splitRow(line);
        const delims = splitRow(nextLine);
        const aligns = delims.map(d => {
          const left = d.startsWith(':');
          const right = d.endsWith(':');
          if (left && right) return 'center';
          if (right) return 'right';
          if (left) return 'left';
          return '';
        });
        i += 2;
        const rows = [];
        while (i < lines.length && lines[i].includes('|') && lines[i].trim()) {
          rows.push(splitRow(lines[i]));
          i++;
        }
        let tbl = '<table><thead><tr>';
        headers.forEach((h, col) => {
          const align = aligns[col] ? ` style="text-align:${aligns[col]}"` : '';
          tbl += `<th${align}>${thrInline(h)}</th>`;
        });
        tbl += '</tr></thead><tbody>';
        rows.forEach(r => {
          tbl += '<tr>';
          headers.forEach((_, col) => {
            const val = r[col] || '';
            const align = aligns[col] ? ` style="text-align:${aligns[col]}"` : '';
            tbl += `<td${align}>${thrInline(val)}</td>`;
          });
          tbl += '</tr>';
        });
        tbl += '</tbody></table>';
        html += tbl;
        continue;
      }
    }

    // List: unordered (*, -, +) or ordered (1.)
    const listMatch = /^( *)([-*+]|\d+[.)]) +(.*)$/.exec(line);
    if (listMatch) {
      const listStack = [];
      while (i < lines.length) {
        const l = lines[i];
        if (!l.trim()) {
          let j = i + 1;
          while (j < lines.length && !lines[j].trim()) j++;
          if (j < lines.length && /^( *)([-*+]|\d+[.)]) +(.*)$/.test(lines[j])) {
            i++;
            continue;
          }
          break;
        }

        const itemMatch = /^( *)([-*+]|\d+[.)]) +(.*)$/.exec(l);
        if (itemMatch) {
          const indent = itemMatch[1].length;
          const marker = itemMatch[2];
          const isOrdered = /^\d/.test(marker);
          const type = isOrdered ? 'ol' : 'ul';
          let itemText = itemMatch[3];

          let taskInput = '';
          const taskMatch = /^\[([ xX])\] +(.*)$/.exec(itemText);
          if (taskMatch) {
            const checked = taskMatch[1].toLowerCase() === 'x';
            taskInput = `<input type="checkbox" disabled${checked ? ' checked' : ''}> `;
            itemText = taskMatch[2];
          }

          while (listStack.length && indent < listStack[listStack.length - 1].indent) {
            const popped = listStack.pop();
            html += `</li></${popped.type}>`;
          }

          if (!listStack.length || indent > listStack[listStack.length - 1].indent) {
            html += `<${type}><li class="${taskInput ? 'thr-task-item' : ''}">` + taskInput + thrInline(itemText);
            listStack.push({ type, indent });
          } else {
            if (listStack[listStack.length - 1].type !== type) {
              const popped = listStack.pop();
              html += `</li></${popped.type}><${type}>`;
              listStack.push({ type, indent });
            } else {
              html += `</li><li class="${taskInput ? 'thr-task-item' : ''}">` + taskInput + thrInline(itemText);
            }
          }
          i++;
        } else {
          const indentMatch = /^( *)/.exec(l);
          const isIndented = indentMatch[1].length > listStack[listStack.length - 1].indent;
          if (isIndented && !/^( {0,3})([`~]{3,}|#{1,6}\s|>)/.test(l)) {
            html += '<br>' + thrInline(l.trim());
            i++;
          } else if (!isIndented && !/^( {0,3})([#`~>]|[-*_]{3,})/.test(l) && !l.includes('|')) {
            html += '<br>' + thrInline(l.trim());
            i++;
          } else {
            break;
          }
        }
      }

      while (listStack.length) {
        const popped = listStack.pop();
        html += `</li></${popped.type}>`;
      }
      continue;
    }

    // Paragraph: collect lines until blank line or block start
    const pLines = [];
    while (i < lines.length) {
      const l = lines[i];
      if (!l.trim()) break;
      if (/^( {0,3})(`{3,}|~{3,})/.test(l)) break;
      if (/^#{1,6}\s+/.test(l)) break;
      if (/^( {0,3})([-*_])(?:\s*\2){2,}\s*$/.test(l)) break;
      if (/^( {0,3})>/.test(l)) break;
      if (/^( *)([-*+]|\d+[.)]) +/.test(l)) break;
      if (l.includes('|') && i + 1 < lines.length && /^\s*\|?(\s*:?-+:?\s*\|)+\s*:?-+:?\s*\|?\s*$/.test(lines[i + 1])) break;

      pLines.push(l);
      i++;
    }
    if (pLines.length) {
      html += '<p>' + pLines.map(thrInline).join('<br>') + '</p>';
    }
  }
  return html;
}

/* ---------- list view ---------- */

const thrEl = {};

function thrShow(view) {
  thrEl.listView.hidden = view !== 'list';
  thrEl.threadView.hidden = view !== 'thread';
}

function thrVisible() {
  const d = doc_();
  return thr.list.filter(t => thr.filter !== 'file' || (d && t.path === d.path));
}

function thrDrawList() {
  if (!thrEl.list) return;
  const items = thrVisible();
  const dot = $('#tab-threads .thr-tab-dot');
  if (dot) dot.hidden = !thr.list.some(t => t.running);
  if (!items.length) {
    thrEl.list.innerHTML = '<div class="hint">' + (thr.filter === 'file'
      ? 'No threads started in this file. Select code and press <b data-keys="Alt+T"></b> to start one.'
      : 'No threads yet. Select code and press <b data-keys="Alt+T"></b>, or use + New, to start a long-running conversation.') + '</div>';
    applyKeyLabels(thrEl.list);
    return;
  }
  thrEl.list.innerHTML = items.map(t =>
    '<div class="thr-item' + (thr.cur && thr.cur.id === t.id ? ' sel' : '') + '" data-id="' + esc(t.id) + '" role="button" tabindex="0">' +
    '<div class="thr-item-title">' + (t.running ? '<span class="thr-spin" title="Working"></span>' : t.failed ? '<span class="thr-fail" title="Last reply failed">!</span>' : '') +
    '<span>' + esc(t.title) + '</span>' + (t.kind ? '<span class="thr-kind">' + (t.kind === 'batch' ? 'batch' : 'inline') + '</span>' : '') + '</div>' +
    '<div class="thr-item-meta"><span class="thr-item-ref">' + esc(thrRefText(t)) + '</span>' +
    '<span>' + t.turns + (t.turns === 1 ? ' turn' : ' turns') + ' · ' + thrAgo(t.updated) + '</span></div></div>').join('');
}

function thrApplyList(next) {
  const was = new Map(thr.list.map(t => [t.id, t.running]));
  thr.list = next;
  // A turn that just finished may have rewritten files under open tabs.
  if (next.some(t => was.get(t.id) && !t.running && t.touched)) reloadWorkspace(null, 'Thread');
  thrDrawList();
}

function thrConnectList() {
  if (thr.listEs) return;
  const es = thr.listEs = new EventSource(thrUrl('api/threads/stream'));
  es.addEventListener('list', e => thrApplyList(JSON.parse(e.data)));
  es.addEventListener('summary', e => {
    const s = JSON.parse(e.data);
    const next = thr.list.filter(t => t.id !== s.id);
    next.push(s);
    next.sort((a, b) => b.updated - a.updated);
    thrApplyList(next);
  });
  es.addEventListener('deleted', e => {
    const { id } = JSON.parse(e.data);
    thrApplyList(thr.list.filter(t => t.id !== id));
  });
}

/* ---------- thread view ---------- */

function thrCloseStream() {
  if (thr.threadEs) { thr.threadEs.close(); thr.threadEs = null; }
}

const thrRunning = () => !!(thr.cur && thr.cur.turns.length && thr.cur.turns[thr.cur.turns.length - 1].running);

function thrRenderAnchor() {
  const t = thr.cur || thr.draft;
  const el = thrEl.anchor;
  if (!t || !t.path) {
    el.hidden = !thr.draft;
    el.textContent = 'Workspace thread: not tied to a file';
    el.classList.remove('jump');
    delete el.dataset.path;
    return;
  }
  el.hidden = false;
  el.classList.add('jump');
  el.dataset.path = t.path;
  el.dataset.line = t.l1;
  el.textContent = thrRefText(t);
  el.title = 'Jump to this code';
}

function thrTurnHtml(turn) {
  let h = '<div class="thr-msg thr-user"><div class="thr-who">You</div><div class="thr-body">' + thrMd(turn.prompt) + '</div></div>';
  h += '<div class="thr-msg thr-agent" data-turn="' + turn.id + '"><div class="thr-who">' + esc(turn.harness || 'agent') +
    (turn.model ? ' · ' + esc(turn.model) : '') +
    (turn.running ? ' · <span class="thr-live">working…</span>' : turn.ms ? ' · ' + thrDur(turn.ms) : '') + '</div>';
  if (turn.tools && turn.tools.length) {
    h += '<details class="thr-steps"' + (turn.running ? ' open' : '') + '><summary>' + turn.tools.length + (turn.tools.length === 1 ? ' step' : ' steps') + '</summary>' +
      '<div class="thr-step-list">' + turn.tools.map(x => '<div>' + esc(x) + '</div>').join('') + '</div></details>';
  }
  h += '<div class="thr-body thr-reply">' + thrMd(turn.reply || '') + '</div>';
  if (turn.error) h += '<div class="thr-err">' + esc(turn.error) + '</div>';
  if (!turn.running) {
    if (turn.changed && turn.changed.length) {
      h += '<div class="thr-changed"><span class="thr-changed-label">Changed ' + turn.changed.length + (turn.changed.length === 1 ? ' file' : ' files') + '</span>' +
        turn.changed.map(p => '<button class="thr-file" data-path="' + esc(p) + '" title="Open ' + esc(p) + '">' + esc(p) + '</button>').join('') + '</div>';
    } else if (turn.tracked === false) {
      h += '<div class="thr-changed"><span class="thr-changed-label">Changes unknown outside a git repository; the workspace was reloaded.</span></div>';
    }
  }
  return h + '</div>';
}

function thrRenderMsgs() {
  const box = thrEl.msgs;
  const stick = box.scrollHeight - box.scrollTop - box.clientHeight < 60;
  const turns = thr.cur ? thr.cur.turns : [];
  box.innerHTML = turns.length ? turns.map(thrTurnHtml).join('')
    : '<div class="hint">' + (thr.draft && thr.draft.path
      ? 'Ask a question about this code, or ask for changes. The reply can touch any file.'
      : 'Ask anything about this workspace. The reply can touch any file.') + '</div>';
  if (stick) box.scrollTop = box.scrollHeight;
}

function thrRenderState() {
  const running = thrRunning();
  thrEl.title.textContent = thr.cur ? thr.cur.title : 'New thread';
  thrEl.del.hidden = !thr.cur;
  thrEl.stop.hidden = !running;
  thrEl.send.disabled = running || thr.sending;
  thrEl.hint.textContent = running ? 'Working. You can send the next message once it replies.' : 'Enter to send, Shift+Enter for a new line';
  thrRenderAnchor();
  thrRenderMsgs();
  thrDrawList();
}

function thrOpenStream(id) {
  thrCloseStream();
  const es = thr.threadEs = new EventSource(thrUrl('api/threads/stream?id=' + encodeURIComponent(id)));
  es.addEventListener('thread', e => {
    if (thr.threadEs !== es) return;
    thr.cur = JSON.parse(e.data);
    thrRenderState();
  });
  es.addEventListener('delta', e => {
    if (thr.threadEs !== es || !thr.cur) return;
    const d = JSON.parse(e.data);
    const turn = thr.cur.turns.find(x => x.id === d.turn);
    if (!turn) return;
    turn.reply = (turn.reply || '') + d.text;
    const body = thrEl.msgs.querySelector('.thr-agent[data-turn="' + d.turn + '"] .thr-reply');
    if (!body) return;
    const box = thrEl.msgs;
    const stick = box.scrollHeight - box.scrollTop - box.clientHeight < 60;
    body.innerHTML = thrMd(turn.reply);
    if (stick) box.scrollTop = box.scrollHeight;
  });
  es.addEventListener('tool', e => {
    if (thr.threadEs !== es || !thr.cur) return;
    const d = JSON.parse(e.data);
    const turn = thr.cur.turns.find(x => x.id === d.turn);
    if (!turn) return;
    (turn.tools = turn.tools || []).push(d.text);
    const box = thrEl.msgs;
    const stick = box.scrollHeight - box.scrollTop - box.clientHeight < 60;
    const msg = box.querySelector('.thr-agent[data-turn="' + d.turn + '"]');
    if (msg) {
      msg.querySelector('.thr-steps')?.remove();
      msg.querySelector('.thr-who').insertAdjacentHTML('afterend',
        '<details class="thr-steps" open><summary>' + turn.tools.length + (turn.tools.length === 1 ? ' step' : ' steps') + '</summary>' +
        '<div class="thr-step-list">' + turn.tools.map(x => '<div>' + esc(x) + '</div>').join('') + '</div></details>');
    }
    if (stick) box.scrollTop = box.scrollHeight;
  });
  es.addEventListener('deleted', () => { if (thr.threadEs === es) thrBack(); });
  es.onerror = () => { /* EventSource reconnects on its own and resends a snapshot */ };
}

export function openThread(id) {
  thr.draft = null;
  thr.cur = null;
  showRightInspector('threads');
  thrShow('thread');
  thrEl.msgs.innerHTML = '<div class="hint">Loading…</div>';
  thrEl.title.textContent = 'Thread';
  thrEl.del.hidden = true;
  thrOpenStream(id);
}

function thrBack() {
  thrCloseStream();
  thr.cur = null;
  thr.draft = null;
  thrShow('list');
  thrDrawList();
}

/* Starts a draft: anchored to info's range, or to the workspace when info is
   omitted. Nothing is created on the server until the first message is sent. */
export function newThread(info) {
  if (!thr.avail) { showToast('!', 'Threads need a coding harness: run px0 without -no-agent'); return; }
  thrCloseStream();
  thr.cur = null;
  thr.draft = info && info.path ? { path: info.path, l1: info.l1, l2: info.l2 } : {};
  showRightInspector('threads');
  thrShow('thread');
  thrRenderState();
  if (info) hideSelectionBar();
  thrEl.input.focus();
}

async function thrSend() {
  const message = thrEl.input.value.trim();
  if (!message || thr.sending || thrRunning()) return;
  thr.sending = true;
  thrEl.send.disabled = true;
  try {
    if (thr.draft) {
      const t = await apiPostJson('/api/threads/create', { ...thr.draft, message });
      thr.draft = null;
      thr.cur = t;
      thrOpenStream(t.id);
    } else if (thr.cur) {
      await apiPostJson('/api/threads/send', { id: thr.cur.id, message });
    } else {
      return;
    }
    thrEl.input.value = '';
  } catch (e) {
    showToast('!', e.message);
  } finally {
    thr.sending = false;
    thrRenderState();
  }
}

export function initThreads() {
  thrEl.listView = $('#thr-list-view');
  thrEl.threadView = $('#thr-thread-view');
  thrEl.list = $('#thr-list');
  thrEl.title = $('#thr-title');
  thrEl.del = $('#thr-delete');
  thrEl.anchor = $('#thr-anchor');
  thrEl.msgs = $('#thr-msgs');
  thrEl.input = $('#thr-input');
  thrEl.send = $('#thr-send');
  thrEl.stop = $('#thr-stop');
  thrEl.hint = $('#thr-hint');
  if (!thrEl.listView) return;

  setThreadHandler(newThread);
  registerAgentPicker({ el: $('.thr-compose'), harnessSelect: $('#thr-harness'), modelSelect: $('#thr-model') });

  $('#thr-new').addEventListener('click', () => newThread(null));
  $('#thr-back').addEventListener('click', thrBack);
  thrEl.send.addEventListener('click', thrSend);
  thrEl.stop.addEventListener('click', () => {
    if (thr.cur) apiPost('/api/threads/cancel', { id: thr.cur.id }).catch(e => showToast('!', e.message));
  });
  thrEl.del.addEventListener('click', async () => {
    if (!thr.cur || !confirm('Delete this thread and its transcript?')) return;
    try {
      await apiPost('/api/threads/delete', { id: thr.cur.id });
      thrBack();
    } catch (e) { showToast('!', e.message); }
  });
  thrEl.input.addEventListener('keydown', e => {
    // Typing here must not trigger editor shortcuts, or Esc closing the sidebar.
    e.stopPropagation();
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); thrSend(); }
    else if (e.key === 'Escape') thrEl.input.blur();
  });
  thrEl.anchor.addEventListener('click', () => {
    const p = thrEl.anchor.dataset.path;
    if (p) openFile(p, { line: +thrEl.anchor.dataset.line || 1 });
  });
  thrEl.msgs.addEventListener('click', e => {
    const cp = /** @type {HTMLElement|null} */ (e.target)?.closest('.thr-copy');
    if (cp) {
      const code = cp.closest('.thr-pre')?.querySelector('code')?.textContent || '';
      if (code) copyToClipboard(code, 'Copied code', cp);
      return;
    }
    const link = /** @type {HTMLElement|null} */ (e.target)?.closest('a.thr-link');
    if (link && link.dataset.path) {
      e.preventDefault();
      openFile(link.dataset.path, { line: +link.dataset.line || 1 });
      return;
    }
    const f = /** @type {HTMLElement|null} */ (e.target)?.closest('.thr-file');
    if (f) openFile(f.dataset.path);
  });
  const openFromList = e => {
    const item = /** @type {HTMLElement|null} */ (e.target)?.closest('.thr-item');
    if (item) openThread(item.dataset.id);
  };
  thrEl.list.addEventListener('click', openFromList);
  thrEl.list.addEventListener('keydown', e => { if (e.key === 'Enter') openFromList(e); });
  $$('[data-thr-filter]').forEach(b => b.addEventListener('click', () => {
    thr.filter = b.dataset.thrFilter;
    $$('[data-thr-filter]').forEach(x => x.classList.toggle('on', x === b));
    thrDrawList();
  }));
  on('tab:activated', () => { if (thr.filter === 'file') thrDrawList(); });
  // agent.js keeps its inline edit comments at the top of this pane and hands off here.
  on('threads:reveal', () => showRightInspector('threads'));
  on('threads:open', id => { if (id) openThread(id); });
  on('threads:drafts', n => {
    const c = $('#tab-threads .thr-tab-count');
    if (!c) return;
    c.hidden = !n;
    c.textContent = n;
    c.title = n + (n === 1 ? ' comment' : ' comments') + ' waiting to be applied';
  });
  on('threads:shown', () => { if (!thr.cur && !thr.draft) thrShow('list'); thrDrawList(); });

  // Threads exist whenever a harness manager does; -no-agent removes them.
  api('/api/threads').then(j => {
    thr.avail = true;
    $('#tab-threads').hidden = false;
    thr.list = j.threads || [];
    thrDrawList();
    thrConnectList();
  }).catch(() => {
    $('[data-sel="thread"]')?.setAttribute('hidden', '');
  });
}
