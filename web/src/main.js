// web/src/main.js
import { $, S, api, applyKeyLabels } from './state.js';
import { measure, layout, render, initRenderer, updateEditorOptionControls } from './renderer.js';
import { initTabs, openFile, switchTab } from './tabs.js';
import { initCursor } from './cursor.js';
import { initCodeFolding } from './folding-ui.js';
import { initHover } from './hover.js';
import { initSelectionBar } from './selbar.js';
import { drawTree, treeEl, initTree, revealFile, refreshTree, collapseAllDirs, expandDirtyDirs, inGitMode, setAutoRevealSuppressed, setSidebarMode, updateSidebarToggleState, hasGitView } from './tree.js';
import { initSearch } from './search.js';
import { initOutline } from './outline.js';
import { initPanels } from './panels.js';
import { initInspector, showRightInspector } from './inspector.js';
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
import { initGitStream, triggerRefresh } from './gitstream.js';
import { initGitPanel, syncGitPanelMeta } from './gitpanel.js';
import { initPR } from './pr.js';
import { initLineComment } from './linecomment.js';
import { initUnpushed, refreshUnpushed } from './unpushed.js';
import { initPRScope } from './prscope.js';
import { initEditView } from './editview.js';
import { initNewItem } from './newitem.js';
import { initTips, startTipsAsync } from './tips.js';

// Initialize all subsystems
initRenderer();
initTabs();
initEditView();
initNewItem();
initTips();
initCursor();
initCodeFolding();
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
  syncGitPanelMeta();
  applyAgentMeta();
  initPR();
  showRightInspector(); // the right sidebar starts open, on Threads when there is a harness
  document.title = S.meta.name + ' - px0';
  $('#root-name').textContent = S.meta.name;
  $('#root-name').title = S.meta.root;
  if (S.meta.workbench) {
    // Opened from the workbench (workbench.go): link back to it.
    const back = document.createElement('a');
    back.className = 'mini wb-back';
    back.href = S.meta.workbench;
    back.title = 'Back to the px0 workbench';
    back.setAttribute('aria-label', 'Back to the px0 workbench');
    back.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/></svg>';
    $('#root-name').before(back);
  }
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
  setAutoRevealSuppressed(true);
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
    await setSidebarMode(showGit ? 'git' : 'files', { expandDirty: showGit });
    return !!(S.meta?.git && ((S.meta.gitChanges > 0) || S.meta?.pr));
  };
  const selectChangedFileTab = async () => {
    if (S.tabs[S.active]?.diffAvailable) return;
    const changedTabIdx = S.tabs.findIndex(t => t.diffAvailable);
    if (changedTabIdx >= 0) {
      switchTab(changedTabIdx);
    } else if (S.meta.gitFiles && S.meta.gitFiles.length > 0) {
      await openFile(S.meta.gitFiles[0]);
    }
  };

  let hasGitChanges = await applyGitSidebarState();

  const params = new URLSearchParams(window.location.search);
  const initialPath = params.get('path');
  const initialLine = parseInt(params.get('line'), 10) || undefined;
  if (initialPath) {
    await openFile(initialPath, { line: initialLine });
    try {
      const u = new URL(window.location.href);
      u.searchParams.delete('path');
      u.searchParams.delete('line');
      const cleanSearch = u.searchParams.toString();
      const cleanUrl = u.pathname + (cleanSearch ? '?' + cleanSearch : '') + u.hash;
      window.history.replaceState({}, '', cleanUrl);
    } catch {}
  } else {
    if (hasGitChanges) await selectChangedFileTab();
  }

  if (inGitMode()) {
    await expandDirtyDirs();
    const activeDoc = S.tabs[S.active];
    if (activeDoc?.path) await revealFile(activeDoc.path);
  } else {
    collapseAllDirs();
  }
  setAutoRevealSuppressed(false);

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
          updateSidebarToggleState();
          syncGitPanelMeta();
          updateStatus();
          setAutoRevealSuppressed(true);
          await refreshTree();
          await triggerRefresh();
          if (!initialPath) {
            hasGitChanges = await applyGitSidebarState();
            if (hasGitChanges) await selectChangedFileTab();
            if (inGitMode()) {
              await expandDirtyDirs();
              const activeDoc = S.tabs[S.active];
              if (activeDoc?.path) await revealFile(activeDoc.path);
            } else {
              collapseAllDirs();
            }
          }
          setAutoRevealSuppressed(false);
        }
      } catch {
        clearInterval(timer);
      }
    }, 150);
  }

  // Load harnesses and models asynchronously after the browser is loaded.
  loadAgentAsync();

  // Load and asynchronously display tips
  startTipsAsync();
})();
