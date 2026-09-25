// web/src/state.js
/**
 * @param {string} s
 * @param {ParentNode} [r=document]
 * @returns {any}
 */
export const $ = (s, r = document) => r.querySelector(s);
/**
 * @param {string} s
 * @param {ParentNode} [r=document]
 * @returns {any[]}
 */
export const $$ = (s, r = document) => [...r.querySelectorAll(s)];
export const esc = s => s.replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

const request = async (method, path, params, opts = {}) => {
  const relPath = path.startsWith('/') ? path.slice(1) : path;
  const base = document.baseURI || (location.origin + '/');
  const u = new URL(relPath, base);
  /** @type {RequestInit} */
  const fetchOpts = { method, ...opts };
  const isPost = method === 'POST' || method === 'PUT' || method === 'PATCH';

  if (params) {
    const hasComplex = typeof params === 'object' && params !== null && (
      Array.isArray(params) || Object.values(params).some(v => typeof v === 'object' && v !== null)
    );
    if (isPost && (opts.json || hasComplex)) {
      fetchOpts.headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
      fetchOpts.body = JSON.stringify(params);
    } else {
      for (const [k, v] of Object.entries(params)) {
        if (v !== undefined && v !== '') u.searchParams.set(k, v);
      }
    }
  }

  const r = await fetch(u, fetchOpts);
  const j = await r.json();
  // The body rides along: some replies, like a failed agent job, carry detail beyond the message.
  if (j.error) throw Object.assign(new Error(j.error), { body: j });
  return j;
};
export const api = (path, params, opts) => request('GET', path, params, opts);
// For requests that change the machine; the server only accepts these as POST from this page.
export const apiPost = (path, params, opts) => request('POST', path, params, opts);
export const apiPostJson = (path, params, opts) => request('POST', path, params, { json: true, ...opts });

export const debounce = (fn, ms) => { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };
// navigator.platform is deprecated but is still the only signal some browsers give.
export const isMac = /mac|iphone|ipad/i.test(/** @type {any} */ (navigator).userAgentData?.platform || navigator.platform || '');
export const MOD = isMac ? 'metaKey' : 'ctrlKey';

/* Shortcuts are written once, as "Mod+Shift+F", and shown the way the reader's
   keyboard labels them: ⌘⇧F on a Mac, Ctrl+Shift+F elsewhere. Mod is the key MOD
   tests: Cmd on a Mac, Ctrl elsewhere. "A|B" shows A off the Mac and B on it,
   for shortcuts that differ; an empty side means none on that system. */
const MAC_KEYS = { Mod: '⌘', Ctrl: '⌃', Alt: '⌥', Shift: '⇧', Enter: '↩', Left: '←', Right: '→', Up: '↑', Down: '↓' };
const PC_KEYS = { Mod: 'Ctrl', Left: '←', Right: '→', Up: '↑', Down: '↓' };

const keyParts = combo => {
  const c = combo.includes('|') ? combo.split('|')[isMac ? 1 : 0] : combo;
  return c ? c.split('+').map(k => (isMac ? MAC_KEYS : PC_KEYS)[k] || k) : [];
};

export const keyLabel = combo => {
  const parts = keyParts(combo);
  if (!isMac) return parts.join('+');
  const key = parts.pop() || '';
  // Mac symbols run together (⌘⇧F); a spelled-out key gets a space (⌘ Click).
  return parts.join('') + (parts.length && /^[a-z]{2,}$/i.test(key) ? ' ' : '') + key;
};

export const keyCaps = combo => keyParts(combo).map(k => '<kbd>' + esc(k) + '</kbd>').join('');

// "Go to File ({Mod+P})" -> "Go to File (⌘P)"
export const withKeys = text => text.replace(/\{([^}]+)\}/g, (_, combo) => keyLabel(combo));

/* Static markup names shortcuts the same way: data-keys fills a label, data-caps
   fills key caps, and {combo} in a title is replaced. */
export function applyKeyLabels(root = document) {
  for (const el of $$('[data-keys]', root)) el.textContent = keyLabel(el.dataset.keys);
  for (const el of $$('[data-caps]', root)) el.innerHTML = keyCaps(el.dataset.caps);
  for (const el of $$('[title*="{"]', root)) el.title = withKeys(el.title);
}

export const LH = 20, CHUNK = 1000, OVERSCAN = 24;

/**
 * @typedef {Object} LSPState
 * @property {string[]} [servers]
 * @property {string} state - 'off' | 'starting' | 'ready' | 'missing'
 * @property {string} server - server identifier
 * @property {string} [missing]
 */

/**
 * @typedef {Object} DocTab
 * @property {string} path - Workspace relative path
 * @property {string} name - File name
 * @property {string} lang - Language identifier
 * @property {number} total - Total line count
 * @property {number} maxCols - Maximum column width
 * @property {number} size - File size in bytes
 * @property {string[]|null} lines - Windowed line buffer
 * @property {Set<number>} [chunks]
 * @property {Set<number>} [pending]
 * @property {Set<number>} [refining]
 * @property {number} scrollTop
 * @property {number} cur - Active line number
 * @property {number} [col] - Active column number
 * @property {any} [outline]
 * @property {number} [gen]
 * @property {boolean} [markdown]
 * @property {boolean} [isImage]
 * @property {string|null} [diffMode] - 'split' | 'unified' | null
 * @property {boolean} [diffAvailable]
 * @property {boolean} [diffDismissed]
 * @property {boolean} [openedInDiffView]
 * @property {any} [gutter]
 * @property {LSPState} [lsp]
 * @property {boolean} [imageFit]
 * @property {number} [imageScale]
 * @property {number} [imagePanX]
 * @property {number} [imagePanY]
 * @property {string} [imageBg]
 * @property {boolean} [imagePixelated]
 * @property {any} [imageMeta]
 * @property {number} [mdScroll]
 * @property {string} [mdError]
 * @property {string} [mdHtml]
 * @property {any} [mdReq]
 * @property {boolean} [prCollapsed]
 * @property {boolean} [youCollapsed]
 * @property {{line: number, col: number}|null} [selAnchor]
 * @property {string} [diffText]
 * @property {any} [diffHunks]
 * @property {number} [mdLine]
 * @property {string} [mdAnchor]
 * @property {string} [outlineSource]
 */

/**
 * @typedef {Object} WorkspaceMeta
 * @property {string} root - Absolute root path
 * @property {string} name - Project name
 * @property {string} [version]
 * @property {number} [files]
 * @property {number} [indexMs]
 * @property {boolean} [git]
 * @property {number} [gitChanges]
 * @property {string[]} [gitFiles]
 * @property {boolean} [ready]
 * @property {any} [metrics]
 * @property {any} [pr]
 * @property {boolean} [githubToken]
 * @property {any} [agents]
 * @property {string} [agent]
 * @property {string} [agentModel]
 * @property {boolean} [agentPinned]
 */

/**
 * @typedef {Object} AppState
 * @property {WorkspaceMeta|null} meta
 * @property {DocTab[]} tabs
 * @property {number} active
 * @property {Array<{path: string, line: number}>} hist
 * @property {number} histIdx
 * @property {any} find
 * @property {string|null} occ
 * @property {DocTab|null} selAll
 * @property {string} lastWord
 * @property {{word?: string, line?: number, col?: number, path?: string}|null} at
 * @property {{word: string, line: number, col: number}|null} link
 * @property {any} hover
 * @property {any} hoverAnchor
 * @property {LSPState} lsp
 * @property {number} gen
 * @property {number} chW
 * @property {boolean} wrap
 * @property {boolean} lineNumbers
 * @property {boolean} mdPreview
 * @property {any} settings
 * @property {Array<{id: string, path: string, l1: number, l2: number}>} agentTargets
 */

/** @type {AppState} */
export const S = {
  meta: null,
  tabs: [],
  active: -1,
  hist: [], histIdx: -1,
  find: null,         // {q, ci, hits:[{line,n}], active}
  occ: null,          // word to highlight everywhere
  selAll: null,       // doc whose whole text is selected (Ctrl+A)
  lastWord: '',
  at: null,           // {word, line, col} of the last click in the code area
  link: null,         // identifier currently underlined under a held modifier
  hover: null,        // identifier the hover card is describing
  hoverAnchor: null,  // where the card was opened, to cheaply detect leaving
  lsp: { servers: [], state: 'off', server: '' },
  gen: 0,
  chW: 7.8,
  wrap: true,        // word wrap (default ON)
  lineNumbers: true, // line numbers gutter (default ON)
  mdPreview: true,   // Markdown tabs open rendered (default ON)
  settings: null,    // loaded from /api/settings
  agentTargets: [],  // [{ id, path, l1, l2 }, ...] ranges of open compose/edit sessions
};

/** @returns {DocTab|null} */
export const doc_ = () => (S.active >= 0 ? S.tabs[S.active] : null);
