// web/src/settings.js
import { $, $$, esc, S, api, apiPost } from './state.js';
import { applyEditorTypography, toggleWordWrap, toggleLineNumbers, layout, render } from './renderer.js';
import { setTheme, listThemes } from './theme.js';
import { setLayoutPref } from './diff.js';
import { setSidebarToggle } from './panels.js';

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
    key: "workbench.sideBar.location",
    title: "Sidebar Position",
    description: "Controls which side of the editor the file explorer sidebar is shown on.",
    category: "Workbench",
    type: "select",
    default: "left",
    options: ["left", "right"]
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
    key: "telemetry.enabled",
    title: "Telemetry",
    description: "Enable anonymous usage metrics to help improve px0.",
    category: "Security & Privacy",
    type: "boolean",
    default: true
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
  'workbench.sideBar.location',
  'editor.wordWrap',
  'editor.lineNumbers',
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
    case 'workbench.sideBar.location': {
      document.body.classList.toggle('side-right', val === 'right');
      try { localStorage.setItem('px0.side', val === 'right' ? 'right' : 'left'); } catch {}
      layout();
      render();
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
  }
}

export function applyAllSettingsLive() {
  if (!S.settings) return;
  for (const [k, v] of Object.entries(S.settings)) {
    applySettingLive(k, v);
  }
}

export function openSettings(mode = 'ui') {
  if (!settingsModalEl) initSettingsDOM();
  settingsViewMode = mode === 'json' ? 'json' : 'ui';
  settingsModalEl.hidden = false;

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
  });

  const searchInput = $('#settings-search');
  if (searchInput && settingsViewMode === 'ui') {
    setTimeout(() => searchInput.focus(), 50);
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
    return parseFloat(val) !== parseFloat(defVal);
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
    } else {
      controlHtml = `<input type="text" class="settings-input" data-key="${esc(key)}" value="${esc(String(val || ''))}">`;
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
          <div class="settings-card-meta">
            <span class="settings-tag tag-current">Current: <b>${esc(String(val))}</b></span>
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

export async function updateSetting(key, value) {
  // Update state locally
  if (!settingsData.settings) settingsData.settings = {};
  settingsData.settings[key] = value;
  applySettingLive(key, value);

  // Re-render setting card modified state
  renderSettingsList();

  // Persist to server
  try {
    const res = await apiPost('/api/settings', { [key]: value });
    if (res.raw) settingsData.raw = res.raw;
  } catch (err) {
    console.error(`Failed to save setting ${key}:`, err);
  }
}

async function handleResetSetting(key) {
  const def = settingsData.defaults ? settingsData.defaults[key] : undefined;
  if (def !== undefined) {
    await updateSetting(key, def);
  }
}

export function toggleSidebarPosition() {
  const cur = (S.settings && S.settings['workbench.sideBar.location']) || 'left';
  updateSetting('workbench.sideBar.location', cur === 'right' ? 'left' : 'right');
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
    return;
  }

  try {
    const res = await apiPost('/api/settings', { raw: rawText });
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
  } catch (err) {
    if (errEl) {
      errEl.textContent = 'Failed to save: ' + err.message;
      errEl.hidden = false;
    }
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
      updateSetting(key, value);
    });

    listEl.addEventListener('click', e => {
      const pill = e.target.closest('.settings-pill-tag');
      if (pill) {
        const key = pill.dataset.setKey;
        let value = pill.dataset.setVal;
        if (value === 'true') value = true;
        else if (value === 'false') value = false;
        else if (!isNaN(Number(value)) && value.trim() !== '') value = Number(value);
        if (key) updateSetting(key, value);
        return;
      }
      const resetBtn = e.target.closest('.settings-reset-btn');
      if (resetBtn) {
        const key = resetBtn.dataset.reset;
        if (key) handleResetSetting(key);
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
  setSidebarToggle(toggleSidebarPosition);
  initSettingsDOM();
  loadSettings().then(() => {
    applyAllSettingsLive();
  });
}
