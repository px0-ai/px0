// web/src/main.js
import { $, S, api, applyKeyLabels } from './state.js';
import { measure, layout, render, initRenderer, updateEditorOptionControls } from './renderer.js';
import { initTabs, openFile, restoreWorkspaceTabs, switchTab } from './tabs.js';
import { initCursor } from './cursor.js';
import { initHover } from './hover.js';
import { initSelectionBar } from './selbar.js';
import { drawTree, treeEl, initTree, revealFile, refreshTree, restoreOpenDirs, setSidebarMode, updateSidebarToggleState } from './tree.js';
import { initSearch } from './search.js';
import { initOutline } from './outline.js';
import { initPanels } from './panels.js';
import { initInspector } from './inspector.js';
import { initCalls } from './calls.js';
import { initFind } from './find.js';
import { initPalette } from './palette.js';
import { initShortcuts } from './shortcuts.js';
import { initTheme } from './theme.js';
import { initMarkdown } from './markdown.js';
import { initDiff } from './diff.js';
import { initAgent, applyAgentMeta, loadAgentAsync } from './agent.js';
import { initMetrics, initStatusFit, updateMetricsDisplay, updateStatus } from './status.js';
import { initSettings } from './settings.js';
import { initVim } from './vim.js';
import { initImageViewer } from './imageview.js';
import { initGitStream } from './gitstream.js';

// Initialize all subsystems
initRenderer();
initTabs();
initCursor();
initHover();
initSelectionBar();
initTree();
initSearch();
initOutline();
initPanels();
initInspector();
initCalls();
initFind();
initPalette();
initShortcuts();
initMarkdown();
initDiff();
initAgent();
initMetrics();
initStatusFit();
initSettings();
initVim();
initImageViewer();

// Bootstrap application lifecycle
(async function boot() {
  try {
    initTheme();

    // Restore word wrap (default ON)
    const wrapPref = localStorage.getItem('px0.wrap');
    S.wrap = wrapPref !== null ? wrapPref === 'true' : true;
    document.body.classList.toggle('word-wrap', S.wrap);

    // Line numbers are always ON
    S.lineNumbers = true;
    document.body.classList.remove('hide-lines');

    // Restore Markdown preview (default ON)
    const mdPref = localStorage.getItem('px0.mdPreview');
    S.mdPreview = mdPref !== null ? mdPref === 'true' : true;

    updateEditorOptionControls();
  } catch {}

  applyKeyLabels();

  measure();
  S.meta = await api('/api/meta');
  if (S.meta.metrics) updateMetricsDisplay(S.meta.metrics);
  updateSidebarToggleState();
  applyAgentMeta();
  document.title = S.meta.name + ' - px0';
  $('#root-name').textContent = S.meta.name;
  $('#root-name').title = S.meta.root;
  if (S.meta.version) {
    const emptyVerEl = $('#empty-ver');
    if (emptyVerEl) emptyVerEl.textContent = 'v' + S.meta.version;
  }
  try {
    const savedDirs = JSON.parse(sessionStorage.getItem('px0.openDirs') || '[]');
    restoreOpenDirs(savedDirs);
  } catch {}
  await refreshTree();
  initGitStream();

  const hasGitChanges = !!(S.meta?.git && S.meta.gitChanges > 0);
  if (hasGitChanges) {
    await setSidebarMode('git');
  } else {
    setSidebarMode('files');
  }

  const params = new URLSearchParams(window.location.search);
  const initialPath = params.get('path');
  const initialLine = parseInt(params.get('line'), 10) || undefined;
  if (initialPath) {
    await openFile(initialPath, { line: initialLine });
    await revealFile(initialPath);
    try {
      const u = new URL(window.location.href);
      u.searchParams.delete('path');
      u.searchParams.delete('line');
      const cleanSearch = u.searchParams.toString();
      const cleanUrl = u.pathname + (cleanSearch ? '?' + cleanSearch : '') + u.hash;
      window.history.replaceState({}, '', cleanUrl);
    } catch {}
  } else {
    const restored = await restoreWorkspaceTabs();
    if (hasGitChanges) {
      const hasActiveDiff = S.tabs[S.active]?.diffAvailable;
      if (!hasActiveDiff) {
        const changedTabIdx = S.tabs.findIndex(t => t.diffAvailable);
        if (changedTabIdx >= 0) {
          switchTab(changedTabIdx);
        } else if (S.meta.gitFiles && S.meta.gitFiles.length > 0) {
          await openFile(S.meta.gitFiles[0]);
          await revealFile(S.meta.gitFiles[0]);
        }
      }
    }
  }

  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(() => { measure(); layout(); render(); });
  }

  // If the background indexer was still running when the UI loaded, poll briefly
  // until complete to update the total file count and index time in the status bar.
  if (S.meta && !S.meta.ready) {
    const timer = setInterval(async () => {
      try {
        const m = await api('/api/meta');
        if (m.ready) {
          clearInterval(timer);
          S.meta = m;
          updateStatus();
        }
      } catch {
        clearInterval(timer);
      }
    }, 150);
  }

  // Load harnesses and models asynchronously after the browser is loaded.
  loadAgentAsync();
})();
