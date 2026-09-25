// web/src/settings.js
import { $, $$, esc, S, api, apiPost, apiPostJson } from './state.js';
import { showToast } from './ui.js';
import { applyEditorTypography, toggleWordWrap, toggleLineNumbers } from './renderer.js';
import { setTheme, listThemes } from './theme.js';
import { setLayoutPref } from './diff.js';
import { setVimModeEnabled, showVimHelp } from './vim.js';

export let settingsModalEl = null;
const BUILTIN_SCHEMA = [
  {
    key: "editor.fontSize",
    title: "Font Size",
    description: "Controls the font size in pixels for the code viewer.",
    category: "Text Editor",
    type: "number",
    default: 13.5,
    min: 9.0,
    max: 32.0,
    step: 0.5
  },
  {
    key: "editor.fontFamily",
    title: "Font Family",
    description: "Controls the font family used in the code viewer.",
    category: "Text Editor",
    type: "string",
    default: '"JetBrains Mono", "Fira Code", "Cascadia Code", "SF Mono", Menlo, Consolas, ui-monospace, monospace'
  },
  {
    key: "editor.lineHeight",
    title: "Line Height",
    description: "Controls the line height in pixels for the code viewer.",
    category: "Text Editor",
    type: "number",
    default: 21.0,
    min: 14.0,
    max: 48.0,
    step: 1.0
  },
  {
    key: "editor.tabSize",
    title: "Tab Size",
    description: "The number of spaces a tab is equal to.",
    category: "Text Editor",
    type: "select",
    default: 4,
    options: ["2", "4", "8"]
  },
  {
    key: "editor.wordWrap",
    title: "Word Wrap",
    description: "Controls whether lines should wrap around or scroll horizontally.",
    category: "Text Editor",
    type: "select",
    default: "on",
    options: ["on", "off"]
  },
  {
    key: "editor.lineNumbers",
    title: "Line Numbers",
    description: "Controls the display of line numbers in the gutter.",
    category: "Text Editor",
    type: "select",
    default: "on",
    options: ["on", "off"]
  },
  {
    key: "editor.vimMode",
    title: "Vim Keybindings",
    description: "Enable Vim modal navigation (Normal mode, Visual mode, motions, search, and LSP shortcuts).",
    category: "Text Editor",
    type: "boolean",
    default: false
  },
  {
    key: "editor.cursorStyle",
    title: "Cursor Style",
    description: "Controls the cursor style in the code viewer.",
    category: "Text Editor",
    type: "select",
    default: "line",
    options: ["line", "block", "underline"]
  },
  {
    key: "editor.cursorBlinking",
    title: "Cursor Blinking",
    description: "Controls the cursor animation style.",
    category: "Text Editor",
    type: "select",
    default: "smooth",
    options: ["blink", "smooth", "solid"]
  },
  {
    key: "editor.renderLineHighlight",
    title: "Render Line Highlight",
    description: "Controls how the editor should render the current line highlight.",
    category: "Text Editor",
    type: "select",
    default: "line",
    options: ["line", "none"]
  },
  {
    key: "editor.occurrencesHighlight",
    title: "Occurrences Highlight",
    description: "Controls whether the editor should highlight occurrences of the selected word.",
    category: "Text Editor",
    type: "boolean",
    default: true
  },
  {
    key: "editor.scrollBeyondLastLine",
    title: "Scroll Beyond Last Line",
    description: "Controls whether the editor will scroll beyond the last line of the file.",
    category: "Text Editor",
    type: "boolean",
    default: true
  },
  {
    key: "editor.bracketPairColorization",
    title: "Bracket Pair Colorization",
    description: "Controls whether bracket pair colorization and matching is enabled.",
    category: "Text Editor",
    type: "boolean",
    default: true
  },
  {
    key: "editor.renderWhitespace",
    title: "Render Whitespace",
    description: "Controls how whitespace characters are rendered in the viewer.",
    category: "Text Editor",
    type: "select",
    default: "selection",
    options: ["none", "boundary", "selection", "all"]
  },
  {
    key: "editor.minimap.enabled",
    title: "Minimap Hits",
    description: "Controls whether search hit indicators are shown in the scroll minimap gutter.",
    category: "Text Editor",
    type: "boolean",
    default: true
  },
  {
    key: "workbench.colorTheme",
    title: "Color Theme",
    description: "Specifies the color theme used in the workbench.",
    category: "Workbench",
    type: "select",
    default: "github-dark",
    options: [
      "github-dark", "dark", "light",
      "catppuccin-mocha", "catppuccin-latte",
      "dracula", "gruvbox-dark", "gruvbox-light",
      "monokai", "nord", "one-dark", "rose-pine",
      "solarized-dark", "solarized-light"
    ]
  },
  {
    key: "diffEditor.renderSideBySide",
    title: "Diff Side By Side",
    description: "Controls whether the diff editor shows changes in split (side-by-side) or unified mode.",
    category: "Workbench",
    type: "boolean",
    default: true
  },
  {
    key: "diffEditor.ignoreTrimWhitespace",
    title: "Diff: Ignore Trim Whitespace",
    description: "Controls whether the diff viewer ignores changes in leading or trailing whitespace.",
    category: "Git & Diff",
    type: "boolean",
    default: true
  },
  {
    key: "git.gutterIndicators",
    title: "Git Gutter Indicators",
    description: "Controls whether changed line indicators are shown in the editor gutter.",
    category: "Git & Diff",
    type: "boolean",
    default: true
  },
  {
    key: "markdown.preview.open",
    title: "Markdown Preview",
    description: "Controls whether Markdown files open in rendered preview by default.",
    category: "Workbench",
    type: "boolean",
    default: true
  },
  {
    key: "explorer.compactFolders",
    title: "Compact Folders",
    description: "Controls whether the file tree renders single-child directory chains compactly.",
    category: "Files & Explorer",
    type: "boolean",
    default: true
  },
  {
    key: "explorer.autoReveal",
    title: "Auto Reveal Active File",
    description: "Controls whether the file explorer automatically scrolls to and reveals active tabs.",
    category: "Files & Explorer",
    type: "boolean",
    default: true
  },
  {
    key: "files.exclude",
    title: "Files Exclude Patterns",
    description: "Configure glob patterns for excluding files and folders from search and trees.",
    category: "Files & Explorer",
    type: "string",
    default: "**/.git, **/node_modules, **/target, **/.DS_Store"
  },
  {
    key: "search.smartCase",
    title: "Smart Case Search",
    description: "Searches case-insensitively when query is lowercase, and case-sensitively when uppercase characters exist.",
    category: "Search",
    type: "boolean",
    default: true
  },
  {
    key: "search.maxResults",
    title: "Max Search Results",
    description: "Controls the maximum number of results returned in workspace-wide searches.",
    category: "Search",
    type: "number",
    default: 1000.0,
    min: 50.0,
    max: 10000.0,
    step: 50.0
  },
  {
    key: "lsp.enabled",
    title: "Language Server Protocol (LSP)",
    description: "Master switch for language server integrations (definitions, references, diagnostics).",
    category: "LSP & Intelligence",
    type: "boolean",
    default: true
  },
  {
    key: "lsp.hover.enabled",
    title: "Hover Documentation",
    description: "Controls whether hovercards with documentation and type signatures appear on hover.",
    category: "LSP & Intelligence",
    type: "boolean",
    default: true
  },
  {
    key: "agent.harness",
    title: "Coding Harness",
    description: "Coding agent harness invoked for code edits (e.g. claude, gemini, cursor-agent, agy, opencode, codex, aider, goose).",
    category: "Agent / AI",
    type: "string",
    default: ""
  },
  {
    key: "agent.timeoutSeconds",
    title: "Agent Timeout (Seconds)",
    description: "Controls the maximum execution time in seconds for agent edits before canceling.",
    category: "Agent / AI",
    type: "number",
    default: 120.0,
    min: 10.0,
    max: 600.0,
    step: 10.0
  },
  {
    key: "agent.autoAcceptEdits",
    title: "Auto Accept Agent Edits",
    description: "Controls whether agent-generated code diffs are accepted without manual confirmation.",
    category: "Agent / AI",
    type: "boolean",
    default: false
  },
  {
    key: "server.basePath",
    title: "Base Path",
    description: "Base URL path prefix for the px0 server and web interface (e.g. /rev-123/).",
    category: "Server",
    type: "string",
    default: "/"
  }
];

let settingsData = {
  settings: {},
  defaults: Object.fromEntries(BUILTIN_SCHEMA.map(s => [s.key, s.default])),
  schema: BUILTIN_SCHEMA,
  raw: '{\n}\n',
  path: '~/.px0/settings.json'
};
let activeSettingsCategory = 'Commonly Used';
let settingsViewMode = 'ui'; // 'ui' | 'json'
let settingsFilterQuery = '';

const COMMONLY_USED_KEYS = new Set([
  'editor.fontSize',
  'workbench.colorTheme',
  'editor.wordWrap',
  'editor.lineNumbers',
  'editor.vimMode',
  'editor.tabSize',
  'diffEditor.renderSideBySide',
  'editor.cursorStyle',
  'explorer.autoReveal',
  'search.smartCase',
  'lsp.hover.enabled',
  'agent.harness',
]);

export async function loadSettings() {
  try {
    const data = await api('/api/settings');
    if (data && data.schema && data.schema.length > 0) {
      settingsData = data;
    } else if (data) {
      settingsData.settings = data.settings || {};
      settingsData.raw = data.raw || settingsData.raw;
      settingsData.path = data.path || settingsData.path;
      if (data.defaults) settingsData.defaults = { ...settingsData.defaults, ...data.defaults };
    }
    S.settings = settingsData.settings || {};
    return settingsData;
  } catch (err) {
    console.warn('Using built-in settings schema (offline/fallback):', err);
    return settingsData;
  }
}

export function applySettingLive(key, val) {
  if (!S.settings) S.settings = {};
  S.settings[key] = val;

  switch (key) {
    case 'editor.fontSize':
    case 'editor.fontFamily':
    case 'editor.lineHeight':
    case 'editor.tabSize': {
      const fs = parseFloat(S.settings['editor.fontSize']) || 13.5;
      const ff = S.settings['editor.fontFamily'] || '';
      const lh = parseFloat(S.settings['editor.lineHeight']) || 21.0;
      const ts = parseInt(S.settings['editor.tabSize'], 10) || 4;
      applyEditorTypography(fs, ff, lh, ts);
      break;
    }
    case 'editor.wordWrap': {
      const on = val === 'on' || val === true;
      toggleWordWrap(on);
      break;
    }
    case 'editor.lineNumbers': {
      const on = val === 'on' || val === true;
      toggleLineNumbers(on);
      break;
    }
    case 'editor.cursorStyle': {
      document.body.classList.remove('cursor-block', 'cursor-underline');
      if (val === 'block') document.body.classList.add('cursor-block');
      else if (val === 'underline') document.body.classList.add('cursor-underline');
      break;
    }
    case 'editor.cursorBlinking': {
      document.body.classList.remove('cursor-blink-smooth', 'cursor-blink-solid', 'cursor-blink-blink');
      if (val === 'solid') document.body.classList.add('cursor-blink-solid');
      else if (val === 'blink') document.body.classList.add('cursor-blink-blink');
      else document.body.classList.add('cursor-blink-smooth');
      break;
    }
    case 'editor.renderLineHighlight': {
      document.body.classList.toggle('no-line-highlight', val === 'none');
      break;
    }
    case 'editor.scrollBeyondLastLine': {
      document.body.classList.toggle('no-scroll-beyond', val === false || val === 'false');
      break;
    }
    case 'git.gutterIndicators': {
      document.body.classList.toggle('hide-git-gutter', val === false || val === 'false');
      break;
    }
    case 'editor.minimap.enabled': {
      const minimap = $('#minimap-hits');
      if (minimap) minimap.style.display = (val === false || val === 'false') ? 'none' : '';
      break;
    }
    case 'workbench.colorTheme': {
      if (val) setTheme(val, true);
      break;
    }
    case 'diffEditor.renderSideBySide': {
      const split = val === true || val === 'true';
      setLayoutPref(split ? 'split' : 'unified');
      break;
    }
    case 'markdown.preview.open': {
      S.mdPreview = val === true || val === 'true';
      try { localStorage.setItem('px0.mdPreview', S.mdPreview ? 'true' : 'false'); } catch {}
      break;
    }
    case 'editor.vimMode': {
      setVimModeEnabled(val === true || val === 'true', false);
      break;
    }
  }
}

export function applyAllSettingsLive() {
  if (!S.settings) return;
  for (const [k, v] of Object.entries(S.settings)) {
    applySettingLive(k, v);
  }
}

export function openSettings(mode = 'ui', category = null, highlightKey = null) {
  if (!settingsModalEl) initSettingsDOM();
  settingsViewMode = mode === 'json' ? 'json' : 'ui';
  settingsModalEl.hidden = false;
  pendingSettingsChanges = {};
  setSaveStatus('saved', 'All changes saved');

  if (category && settingsViewMode === 'ui') {
    activeSettingsCategory = category;
    settingsFilterQuery = '';
    const searchInput = $('#settings-search');
    if (searchInput) searchInput.value = '';
  }

  // Immediately render with current schema & settings
  updateSettingsHeader();
  if (settingsViewMode === 'json') {
    showSettingsJSONView();
  } else {
    showSettingsUIView();
  }

  // Refresh with latest settings from server
  loadSettings().then(() => {
    updateSettingsHeader();
    if (settingsViewMode === 'json') {
      showSettingsJSONView();
    } else {
      showSettingsUIView();
    }
    if (highlightKey && settingsViewMode === 'ui') {
      focusSettingCard(highlightKey);
    }
  });

  const searchInput = $('#settings-search');
  if (searchInput && settingsViewMode === 'ui' && !category) {
    setTimeout(() => searchInput.focus(), 50);
  }

  if (highlightKey && settingsViewMode === 'ui') {
    setTimeout(() => focusSettingCard(highlightKey), 30);
  }
}

function focusSettingCard(key) {
  const card = settingsModalEl?.querySelector(`[data-setting="${key}"]`);
  if (card) {
    card.scrollIntoView({ block: 'center', behavior: 'smooth' });
    const input = card.querySelector('textarea, input, select');
    if (input) input.focus();
  }
}

export function closeSettings() {
  if (settingsModalEl) settingsModalEl.hidden = true;
}

export function isSettingsOpen() {
  return settingsModalEl && !settingsModalEl.hidden;
}

function updateSettingsHeader() {
  const pathEl = $('#settings-path');
  if (pathEl && settingsData.path) {
    pathEl.textContent = settingsData.path;
    pathEl.title = 'Click to copy path: ' + settingsData.path;
  }
  const btnUI = $('#settings-mode-ui');
  const btnJSON = $('#settings-mode-json');
  if (btnUI && btnJSON) {
    btnUI.classList.toggle('active', settingsViewMode === 'ui');
    btnJSON.classList.toggle('active', settingsViewMode === 'json');
  }
}

function showSettingsUIView() {
  settingsViewMode = 'ui';
  updateSettingsHeader();
  $('#settings-ui-container').hidden = false;
  $('#settings-json-container').hidden = true;
  $('#settings-search-bar').hidden = false;
  renderSettingsNav();
  renderSettingsList();
}

function showSettingsJSONView() {
  settingsViewMode = 'json';
  updateSettingsHeader();
  $('#settings-ui-container').hidden = true;
  $('#settings-json-container').hidden = false;
  $('#settings-search-bar').hidden = true;

  const rawEditor = $('#settings-raw-editor');
  if (rawEditor) {
    rawEditor.value = settingsData.raw || '{\n}\n';
    rawEditor.focus();
  }
  const errEl = $('#settings-raw-error');
  if (errEl) errEl.hidden = true;
}

function getSettingCategories() {
  const cats = ['Commonly Used'];
  const seen = new Set(cats);
  for (const item of (settingsData.schema || [])) {
    const cat = item.category || item.Category;
    if (cat && !seen.has(cat)) {
      cats.push(cat);
      seen.add(cat);
    }
  }
  return cats;
}

function renderSettingsNav() {
  const nav = $('#settings-nav');
  if (!nav) return;
  const cats = getSettingCategories();
  nav.innerHTML = cats.map(cat => {
    const active = cat === activeSettingsCategory ? ' active' : '';
    return `<button class="settings-nav-item${active}" data-cat="${esc(cat)}">${esc(cat)}</button>`;
  }).join('');
}

function isSettingModified(key, val, defVal) {
  if (val === undefined || val === null) return false;
  if (defVal === undefined || defVal === null) return val !== '';
  if (typeof defVal === 'number') {
    return Number(val) !== Number(defVal);
  }
  if (typeof defVal === 'boolean') {
    return Boolean(val) !== Boolean(defVal);
  }
  return String(val) !== String(defVal);
}

function renderSettingsList() {
  const container = $('#settings-list');
  if (!container) return;

  const q = settingsFilterQuery.trim().toLowerCase();
  const schema = settingsData.schema || [];
  const currentSettings = settingsData.settings || {};
  const defaults = settingsData.defaults || {};

  let items = schema;
  if (q) {
    items = schema.filter(s => {
      const title = (s.title || s.Title || '').toLowerCase();
      const key = (s.key || s.Key || '').toLowerCase();
      const desc = (s.description || s.Description || '').toLowerCase();
      const cat = (s.category || s.Category || '').toLowerCase();
      return title.includes(q) || key.includes(q) || desc.includes(q) || cat.includes(q);
    });
  } else if (activeSettingsCategory === 'Commonly Used') {
    items = schema.filter(s => COMMONLY_USED_KEYS.has(s.key || s.Key));
  } else {
    items = schema.filter(s => (s.category || s.Category) === activeSettingsCategory);
  }

  if (items.length === 0) {
    container.innerHTML = `<div class="settings-empty">No matching settings found for "${esc(q || activeSettingsCategory)}".</div>`;
    return;
  }

  const html = items.map(item => {
    const key = item.key || item.Key;
    const title = item.title || item.Title || key;
    const desc = item.description || item.Description || '';
    const cat = item.category || item.Category || 'General';
    const type = item.type || item.Type || 'string';
    const itemDef = item.default !== undefined ? item.default : item.Default;
    const def = defaults[key] !== undefined ? defaults[key] : itemDef;
    const val = currentSettings[key] !== undefined ? currentSettings[key] : def;
    const modified = isSettingModified(key, currentSettings[key], def);
    const modClass = modified ? ' is-modified' : '';

    let controlHtml = '';
    let aptValuesHtml = '';

    if (type === 'boolean') {
      const checked = (val === true || val === 'true') ? 'checked' : '';
      controlHtml = `
        <label class="settings-switch">
          <input type="checkbox" data-key="${esc(key)}" ${checked}>
          <span class="settings-slider"></span>
        </label>`;
      const isT = val === true || val === 'true';
      aptValuesHtml = `
        <div class="settings-apt-bar">
          <span class="settings-apt-label">Allowed Values:</span>
          <div class="settings-apt-pills">
            <button type="button" class="settings-pill-tag${isT ? ' active' : ''}" data-set-key="${esc(key)}" data-set-val="true" title="Set to true">true</button>
            <button type="button" class="settings-pill-tag${!isT ? ' active' : ''}" data-set-key="${esc(key)}" data-set-val="false" title="Set to false">false</button>
          </div>
        </div>`;
    } else if (type === 'select') {
      const opts = item.options || item.Options || [];
      const optHtml = opts.map(o => {
        const sel = String(o) === String(val) ? 'selected' : '';
        return `<option value="${esc(o)}" ${sel}>${esc(o)}</option>`;
      }).join('');
      controlHtml = `<select class="settings-select" data-key="${esc(key)}">${optHtml}</select>`;
      const pills = opts.map(o => {
        const isSel = String(o) === String(val);
        return `<button type="button" class="settings-pill-tag${isSel ? ' active' : ''}" data-set-key="${esc(key)}" data-set-val="${esc(String(o))}" title="Select ${esc(String(o))}">${esc(String(o))}</button>`;
      }).join('');
      aptValuesHtml = `
        <div class="settings-apt-bar">
          <span class="settings-apt-label">Options:</span>
          <div class="settings-apt-pills">
            ${pills}
          </div>
        </div>`;
    } else if (type === 'number') {
      const min = item.min !== undefined ? item.min : item.Min;
      const max = item.max !== undefined ? item.max : item.Max;
      const step = item.step !== undefined ? item.step : item.Step;
      const minAttr = min !== undefined ? `min="${min}"` : '';
      const maxAttr = max !== undefined ? `max="${max}"` : '';
      const stepAttr = step !== undefined ? `step="${step}"` : 'step="1"';
      controlHtml = `<input type="number" class="settings-input settings-input-num" data-key="${esc(key)}" value="${esc(String(val))}" ${minAttr} ${maxAttr} ${stepAttr}>`;

      let numberPresets = [];
      if (key === 'editor.fontSize') numberPresets = [12, 13, 13.5, 14, 16, 18];
      else if (key === 'editor.lineHeight') numberPresets = [18, 20, 21, 24, 28];
      else if (key === 'search.maxResults') numberPresets = [200, 500, 1000, 5000];
      else if (key === 'agent.timeoutSeconds') numberPresets = [60, 120, 180, 300];

      const presetPills = numberPresets.length ? `
        <span class="settings-apt-label">Presets:</span>
        <div class="settings-apt-pills">
          ${numberPresets.map(n => {
            const isSel = Number(val) === n;
            return `<button type="button" class="settings-pill-tag${isSel ? ' active' : ''}" data-set-key="${esc(key)}" data-set-val="${n}">${n}</button>`;
          }).join('')}
        </div>` : '';

      aptValuesHtml = `
        <div class="settings-apt-bar">
          <span class="settings-tag tag-range">Min: <b>${min !== undefined ? min : '—'}</b></span>
          <span class="settings-tag tag-range">Max: <b>${max !== undefined ? max : '—'}</b></span>
          ${step !== undefined ? `<span class="settings-tag tag-step">Step: <b>${step}</b></span>` : ''}
          ${presetPills}
        </div>`;
    } else if (type === 'textarea') {
      controlHtml = `<textarea class="settings-input settings-textarea" data-key="${esc(key)}" rows="3" spellcheck="false">${esc(String(val || ''))}</textarea>`;
    } else {
      const isSecret = item.secret || item.Secret;
      controlHtml = `<input type="${isSecret ? 'password' : 'text'}" class="settings-input" data-key="${esc(key)}" value="${esc(String(val || ''))}"${isSecret ? ' autocomplete="off"' : ''}>`;
      let stringPresets = [];
      if (key === 'agent.harness') {
        stringPresets = ['claude', 'gemini', 'cursor-agent', 'agy', 'aider'];
      }
      const presetPills = stringPresets.length ? `
        <div class="settings-apt-bar">
          <span class="settings-apt-label">Suggestions:</span>
          <div class="settings-apt-pills">
            ${stringPresets.map(s => {
              const isSel = String(val) === s;
              return `<button type="button" class="settings-pill-tag${isSel ? ' active' : ''}" data-set-key="${esc(key)}" data-set-val="${esc(s)}">${esc(s)}</button>`;
            }).join('')}
          </div>
        </div>` : '';

      aptValuesHtml = presetPills;
    }

    const resetBtn = modified
      ? `<button class="settings-reset-btn" data-reset="${esc(key)}" title="Reset to default (${esc(String(def))})">Reset</button>`
      : '';

    const extraAction = (key === 'editor.vimMode') ? `
      <div style="margin: 6px 0 2px;">
        <button type="button" class="settings-btn-link btn-vim-cheatsheet-trigger" style="cursor:pointer;font-size:11.5px;display:inline-flex;align-items:center;gap:4px;color:var(--accent-fg);">
          <span>View Vim Keybindings Cheat Sheet</span><kbd class="footer-kbd" style="font-size:10px;">?</kbd>
        </button>
      </div>` : '';

    return `
      <div class="settings-card${modClass}" data-setting="${esc(key)}">
        <div class="settings-card-left">
          <div class="settings-card-header">
            <span class="settings-card-title">${esc(title)}</span>
            <span class="settings-card-key">${esc(key)}</span>
            <span class="settings-tag tag-cat">${esc(cat)}</span>
            <span class="settings-tag tag-type">${esc(type)}</span>
          </div>
          <div class="settings-card-desc">${esc(desc)}</div>
          ${aptValuesHtml}
          ${extraAction}
          <div class="settings-card-meta">
            ${(type !== 'textarea' && key !== 'git.commitMessageInstruction') ? `<span class="settings-tag tag-current">Current: <b>${esc(String(val))}</b></span>` : ''}
            <span class="settings-tag tag-default">Default: <code>${esc(String(def))}</code></span>
            ${modified ? `<span class="settings-tag tag-modified">Modified</span>` : ''}
            ${resetBtn}
          </div>
        </div>
        <div class="settings-card-right">
          ${controlHtml}
        </div>
      </div>
    `;
  }).join('');

  container.innerHTML = html;
}

let pendingSettingsChanges = {};

function setSaveStatus(state, msg) {
  const statusEl = $('#settings-footer-status');
  const statusText = $('#settings-status-text');
  const statusIcon = $('#settings-status-icon');
  const saveBtn = $('#btn-settings-save');
  const saveLabel = $('#settings-btn-save-label');
  if (!statusEl || !statusText) return;

  statusEl.className = 'settings-footer-status';

  if (state === 'saving') {
    statusEl.classList.add('is-saving');
    statusText.textContent = msg || 'Saving changes...';
    if (statusIcon) {
      statusIcon.style.animation = 'spin 0.8s linear infinite';
      statusIcon.innerHTML = '<circle cx="12" cy="12" r="9" stroke="currentColor" stroke-dasharray="28" stroke-dashoffset="14" stroke-width="2.5"/>';
    }
    if (saveBtn) saveBtn.disabled = true;
    if (saveLabel) saveLabel.textContent = 'Saving...';
  } else if (state === 'saved') {
    statusEl.classList.add('is-saved');
    statusText.textContent = msg || 'All changes saved';
    if (statusIcon) {
      statusIcon.style.animation = '';
      statusIcon.innerHTML = '<polyline points="20 6 9 17 4 12"/>';
    }
    if (saveBtn) {
      saveBtn.disabled = false;
      const saveIcon = saveBtn.querySelector('.settings-btn-save-icon');
      if (saveIcon) saveIcon.innerHTML = '<polyline points="20 6 9 17 4 12"/>';
      if (saveLabel) saveLabel.textContent = 'Saved';
      setTimeout(() => {
        if (saveIcon) {
          saveIcon.innerHTML = '<path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/>';
        }
        if (saveLabel) saveLabel.textContent = 'Save';
      }, 2000);
    }
  } else if (state === 'unsaved') {
    statusEl.classList.add('is-unsaved');
    statusText.textContent = msg || 'Unsaved changes';
    if (statusIcon) {
      statusIcon.style.animation = '';
      statusIcon.innerHTML = '<circle cx="12" cy="12" r="5" fill="currentColor"/>';
    }
    if (saveBtn) {
      saveBtn.disabled = false;
      const saveIcon = saveBtn.querySelector('.settings-btn-save-icon');
      if (saveIcon) {
        saveIcon.innerHTML = '<path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/>';
      }
      if (saveLabel) saveLabel.textContent = 'Save';
    }
  } else if (state === 'error') {
    statusEl.classList.add('is-error');
    statusText.textContent = msg || 'Failed to save';
    if (statusIcon) {
      statusIcon.style.animation = '';
      statusIcon.innerHTML = '<circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>';
    }
    if (saveBtn) {
      saveBtn.disabled = false;
      if (saveLabel) saveLabel.textContent = 'Retry Save';
    }
  }
}

async function handleSettingChange(key, value) {
  // Update state locally
  if (!settingsData.settings) settingsData.settings = {};
  settingsData.settings[key] = value;
  delete pendingSettingsChanges[key];
  applySettingLive(key, value);

  // Re-render setting card modified state
  renderSettingsList();

  // Persist to server
  try {
    setSaveStatus('saving');
    const res = await apiPostJson('/api/settings', { [key]: value });
    if (res.raw) settingsData.raw = res.raw;
    setSaveStatus('saved', 'All changes saved');
  } catch (err) {
    console.error(`Failed to save setting ${key}:`, err);
    setSaveStatus('error', 'Failed to save: ' + (err.message || 'unknown error'));
  }
}

async function handleResetSetting(key) {
  const def = settingsData.defaults ? settingsData.defaults[key] : undefined;
  if (def !== undefined) {
    await handleSettingChange(key, def);
  }
}

async function handleExplicitSave() {
  if (settingsViewMode === 'json') {
    return handleSaveRawSettings();
  }

  // Capture value of currently focused input/textarea inside settings list if any
  const activeEl = /** @type {HTMLInputElement|HTMLTextAreaElement|null} */ (document.activeElement);
  if (activeEl && activeEl.dataset?.key && activeEl.closest('#settings-list')) {
    const key = activeEl.dataset.key;
    let val = /** @type {any} */ (activeEl.value);
    if (activeEl instanceof HTMLInputElement && activeEl.type === 'checkbox') val = activeEl.checked;
    else if (activeEl.type === 'number') val = parseFloat(val);
    pendingSettingsChanges[key] = val;
  }

  // Apply any pending changes locally
  for (const [k, v] of Object.entries(pendingSettingsChanges)) {
    if (!settingsData.settings) settingsData.settings = {};
    settingsData.settings[k] = v;
    applySettingLive(k, v);
  }

  setSaveStatus('saving');

  try {
    const toSave = Object.keys(pendingSettingsChanges).length > 0
      ? { ...pendingSettingsChanges }
      : { ...(settingsData.settings || {}) };

    const res = await apiPostJson('/api/settings', toSave);
    if (res.settings) {
      settingsData.settings = res.settings;
      S.settings = res.settings;
      applyAllSettingsLive();
    }
    if (res.raw) settingsData.raw = res.raw;
    pendingSettingsChanges = {};
    renderSettingsList();

    setSaveStatus('saved', 'Settings saved successfully');
    showToast('✓', 'Settings saved');
  } catch (err) {
    setSaveStatus('error', 'Failed to save: ' + (err.message || 'unknown error'));
    showToast('!', err.message || 'Failed to save settings');
  }
}

async function handleSaveRawSettings() {
  const rawEditor = $('#settings-raw-editor');
  const errEl = $('#settings-raw-error');
  if (!rawEditor) return;

  const rawText = rawEditor.value;
  try {
    JSON.parse(rawText);
    if (errEl) errEl.hidden = true;
  } catch (err) {
    if (errEl) {
      errEl.textContent = 'JSON Syntax Error: ' + err.message;
      errEl.hidden = false;
    }
    setSaveStatus('error', 'JSON syntax error');
    return;
  }

  setSaveStatus('saving');
  try {
    const res = await apiPostJson('/api/settings', { raw: rawText });
    if (res.settings) {
      settingsData.settings = res.settings;
      S.settings = res.settings;
      applyAllSettingsLive();
    }
    if (res.raw) settingsData.raw = res.raw;
    if (errEl) {
      errEl.textContent = 'Settings saved successfully.';
      errEl.hidden = false;
      errEl.classList.add('success');
      setTimeout(() => {
        errEl.hidden = true;
        errEl.classList.remove('success');
      }, 2500);
    }
    setSaveStatus('saved', 'Settings saved successfully');
    showToast('✓', 'Settings saved');
  } catch (err) {
    if (errEl) {
      errEl.textContent = 'Failed to save: ' + err.message;
      errEl.hidden = false;
    }
    setSaveStatus('error', 'Failed to save: ' + (err.message || 'unknown error'));
  }
}

function initSettingsDOM() {
  settingsModalEl = $('#settings-modal');
  if (!settingsModalEl) return;

  // Header close button
  $('#settings-close')?.addEventListener('click', closeSettings);

  // Click outside dialog to close
  settingsModalEl.addEventListener('click', e => {
    if (e.target === settingsModalEl) closeSettings();
  });

  // Explicit Save button in settings footer
  $('#btn-settings-save')?.addEventListener('click', handleExplicitSave);

  // Keyboard shortcut: Ctrl+S / Cmd+S inside settings modal
  settingsModalEl.addEventListener('keydown', e => {
    if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S')) {
      e.preventDefault();
      handleExplicitSave();
    }
  });

  // Switch between UI and JSON mode
  $('#settings-mode-ui')?.addEventListener('click', () => showSettingsUIView());
  $('#settings-mode-json')?.addEventListener('click', () => showSettingsJSONView());

  // Copy path to clipboard
  $('#settings-path')?.addEventListener('click', () => {
    if (settingsData.path) {
      navigator.clipboard.writeText(settingsData.path);
      const toast = $('#toast');
      if (toast) {
        toast.textContent = 'Copied settings path to clipboard';
        toast.hidden = false;
        setTimeout(() => { toast.hidden = true; }, 2000);
      }
    }
  });

  // Search filter
  const searchInput = $('#settings-search');
  if (searchInput) {
    searchInput.addEventListener('input', e => {
      settingsFilterQuery = e.target.value;
      renderSettingsList();
    });
    $('#settings-search-clear')?.addEventListener('click', () => {
      searchInput.value = '';
      settingsFilterQuery = '';
      renderSettingsList();
      searchInput.focus();
    });
  }

  // Nav categories
  $('#settings-nav')?.addEventListener('click', e => {
    const btn = e.target.closest('.settings-nav-item');
    if (!btn) return;
    activeSettingsCategory = btn.dataset.cat;
    settingsFilterQuery = '';
    if (searchInput) searchInput.value = '';
    renderSettingsNav();
    renderSettingsList();
  });

  // Settings list events (controls & reset buttons)
  const listEl = $('#settings-list');
  if (listEl) {
    listEl.addEventListener('input', e => {
      const target = e.target;
      const key = target.dataset.key;
      if (!key) return;
      let value = target.value;
      if (target.type === 'number') value = parseFloat(value);
      pendingSettingsChanges[key] = value;
      setSaveStatus('unsaved', 'Unsaved changes');
    });

    listEl.addEventListener('change', e => {
      const target = e.target;
      const key = target.dataset.key;
      if (!key) return;

      let value;
      if (target.type === 'checkbox') {
        value = target.checked;
      } else if (target.type === 'number') {
        value = parseFloat(target.value);
      } else {
        value = target.value;
      }
      handleSettingChange(key, value);
    });

    listEl.addEventListener('click', e => {
      const pill = e.target.closest('.settings-pill-tag');
      if (pill) {
        const key = pill.dataset.setKey;
        let value = pill.dataset.setVal;
        if (value === 'true') value = true;
        else if (value === 'false') value = false;
        else if (!isNaN(Number(value)) && value.trim() !== '') value = Number(value);
        if (key) handleSettingChange(key, value);
        return;
      }
      const resetBtn = e.target.closest('.settings-reset-btn');
      if (resetBtn) {
        const key = resetBtn.dataset.reset;
        if (key) handleResetSetting(key);
        return;
      }
      const vimHelpBtn = e.target.closest('.btn-vim-cheatsheet-trigger');
      if (vimHelpBtn) {
        showVimHelp();
        return;
      }
    });
  }

  // JSON Raw view buttons
  $('#btn-settings-save-raw')?.addEventListener('click', handleSaveRawSettings);
  $('#btn-settings-reset-raw')?.addEventListener('click', () => {
    const rawEditor = $('#settings-raw-editor');
    if (rawEditor) rawEditor.value = settingsData.raw || '{\n}\n';
    const errEl = $('#settings-raw-error');
    if (errEl) errEl.hidden = true;
  });
}

export function initSettings() {
  initSettingsDOM();
  loadSettings().then(() => {
    applyAllSettingsLive();
  });
}
