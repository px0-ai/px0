// web/src/main.js
import { $, $$, S, api, applyKeyLabels } from './state.js';
import { measure, layout, render, initRenderer, updateEditorOptionControls } from './renderer.js';
import { initTabs, openFile, restoreWorkspaceTabs, switchTab } from './tabs.js';
import { initCursor } from './cursor.js';
import { initHover } from './hover.js';
import { initSelectionBar } from './selbar.js';
import { drawTree, treeEl, initTree, revealFile, refreshTree, restoreOpenDirs, setSidebarMode, updateSidebarToggleState, hasGitView } from './tree.js';
import { initSearch } from './search.js';
import { initOutline } from './outline.js';
import { initPanels } from './panels.js';
import { initInspector, showRightInspector, setRightInspectorTab } from './inspector.js';
import { initCalls } from './calls.js';
import { initFind } from './find.js';
import { initPalette } from './palette.js';
import { initShortcuts } from './shortcuts.js';
import { initTheme } from './theme.js';
import { initMarkdown } from './markdown.js';
import { initDiff } from './diff.js';
import { initAgent, applyAgentMeta, loadAgentAsync } from './agent.js';
import { initThreads } from './thread.js';
import { initMetrics, initLspMenu, refreshLspMenu, initStatusFit, updateMetricsDisplay, updateStatus } from './status.js';
import { initSettings } from './settings.js';
import { initVim } from './vim.js';
import { initImageViewer } from './imageview.js';
import { initGitStream } from './gitstream.js';
import { initGitPanel, toggleCommitMsgBox } from './gitpanel.js';
import { initPR } from './pr.js';
import { initLineComment } from './linecomment.js';
import { initUnpushed, refreshUnpushed } from './unpushed.js';
import { initPRScope } from './prscope.js';

// Initialize all subsystems
initRenderer();
initTabs();
initCursor();
initHover();
initSelectionBar();
initTree();
initGitPanel();
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
initThreads();
initMetrics();
initLspMenu();
initStatusFit();
initSettings();
initVim();
initImageViewer();
initLineComment();
initUnpushed();
initPRScope();

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
    const tablePref = localStorage.getItem('px0.tablePreview');
    S.tablePreview = tablePref !== null ? tablePref === 'true' : true;

    updateEditorOptionControls();
  } catch {}

  applyKeyLabels();

  measure();
  S.meta = await api('/api/meta');
  if (S.meta.metrics) updateMetricsDisplay(S.meta.metrics);
  refreshLspMenu();
  updateSidebarToggleState();
  applyAgentMeta();
  initPR();
  showRightInspector(); // the right sidebar starts open, on Threads when there is a harness
  document.title = S.meta.name + ' - px0';
  $('#root-name').textContent = S.meta.name;
  $('#root-name').title = S.meta.root;
  if (S.meta.version) {
    const emptyVerEl = $('#empty-ver');
    if (emptyVerEl) emptyVerEl.textContent = 'v' + S.meta.version;
    const stVerEl = $('#st-ver');
    if (stVerEl) {
      stVerEl.textContent = 'v' + S.meta.version;
      stVerEl.title = `px0 v${S.meta.version} (Click for shortcuts & help)`;
    }
  }
  updateStatus();
  try {
    const session = await api('/api/session');
    const ui = session?.ui || {};
    if (session && Array.isArray(session.openDirs) && session.openDirs.length > 0) {
      restoreOpenDirs(session.openDirs);
    }
    if (typeof ui.sidebarHidden === 'boolean') document.body.classList.toggle('side-hidden', ui.sidebarHidden);
    if (Number.isFinite(ui.sidebarWidth)) $('#side').style.width = Math.max(170, Math.min(620, ui.sidebarWidth)) + 'px';
    if (typeof ui.rightTab === 'string' && $$('.inspector-tab').some(tab => tab.dataset.itab === ui.rightTab && !tab.hidden)) {
      setRightInspectorTab(ui.rightTab, { persist: false, focus: false });
    }
    if (typeof ui.rightHidden === 'boolean') document.body.classList.toggle('right-hidden', ui.rightHidden);
    if (typeof ui.gitPanelCollapsed === 'boolean') $('#git-panel')?.classList.toggle('collapsed', ui.gitPanelCollapsed);
    if (Number.isFinite(ui.gitPanelHeight)) $('#git-panel')?.style.setProperty('height', Math.max(60, Math.min(innerHeight * 0.8, ui.gitPanelHeight)) + 'px');
    if (typeof ui.unpushedCollapsed === 'boolean') $('#unpushed')?.classList.toggle('collapsed', ui.unpushedCollapsed);
    if (Number.isFinite(ui.unpushedHeight)) $('#unpushed')?.style.setProperty('height', Math.max(60, Math.min(innerHeight * 0.7, ui.unpushedHeight)) + 'px');
    if (typeof ui.commitMessageOpen === 'boolean') toggleCommitMsgBox(ui.commitMessageOpen, false, false);
  } catch {}
  await refreshTree();
  initGitStream();

  // Split into helpers so the readiness poll below can redo this once the
  // background indexer (main.go's `go ix.Build()`) finishes: gitChanges/
  // gitFiles are zero until then, so a session that loads before indexing
  // completes (common right after `px0 <pr-url>`) would otherwise never
  // auto-select a diff tab -- until the next manual reload.
  const applyGitSidebarState = async () => {
    // The unpushed list decides this too: a clean tree with local commits
    // still belongs in Git view, which is the only place they are listed.
    await refreshUnpushed();
    const showGit = hasGitView();
    await setSidebarMode(showGit ? 'git' : 'files');
    return !!(S.meta?.git && S.meta.gitChanges > 0);
  };
  const selectChangedFileTab = async () => {
    if (S.tabs[S.active]?.diffAvailable) return;
    const changedTabIdx = S.tabs.findIndex(t => t.diffAvailable);
    if (changedTabIdx >= 0) {
      switchTab(changedTabIdx);
    } else if (S.meta.gitFiles && S.meta.gitFiles.length > 0) {
      await openFile(S.meta.gitFiles[0]);
      await revealFile(S.meta.gitFiles[0]);
    }
  };

  let hasGitChanges = await applyGitSidebarState();
  let restoredWorkspaceTabs = false;

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
    restoredWorkspaceTabs = await restoreWorkspaceTabs();
    if (hasGitChanges && !restoredWorkspaceTabs) await selectChangedFileTab();
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
          if (!initialPath) {
            hasGitChanges = await applyGitSidebarState();
            if (hasGitChanges && !restoredWorkspaceTabs) await selectChangedFileTab();
          }
        }
      } catch {
        clearInterval(timer);
      }
    }, 150);
  }

  // Load harnesses and models asynchronously after the browser is loaded.
  loadAgentAsync();
})();
