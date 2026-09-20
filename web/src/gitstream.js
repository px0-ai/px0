import { S, apiPost, doc_ } from './state.js';
import { updateSidebarToggleState, patchTreeGitStatus, treeEl, setSidebarMode } from './tree.js';
import { drawTabs, loadGutter, closeTab } from './tabs.js';
import { syncDiffView } from './diff.js';
import { render } from './renderer.js';
import { updateStatus } from './status.js';

let eventSource = null;
let reconnectTimer = null;

export function initGitStream() {
  if (!S.meta?.git) return;

  connect();

  // Instant refresh when user focuses the browser window
  window.addEventListener('focus', () => {
    if (document.visibilityState === 'visible') {
      if (!eventSource || eventSource.readyState === EventSource.CLOSED) {
        connect();
      }
      triggerRefresh();
    }
  });

  // Page Visibility API: pause streaming when tab is hidden, resume and refresh when visible
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      disconnect();
    } else {
      connect();
      triggerRefresh();
    }
  });
}

export async function triggerRefresh() {
  if (!S.meta?.git) return;
  try {
    const data = await apiPost('/api/git/refresh');
    await handleGitStatus(data);
  } catch (e) {
    // Quiet fail on network hiccups
  }
}

function connect() {
  if (eventSource && eventSource.readyState !== EventSource.CLOSED) return;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }

  try {
    eventSource = new EventSource('/api/git/stream');

    eventSource.addEventListener('git-status', async e => {
      try {
        const data = JSON.parse(e.data);
        await handleGitStatus(data);
      } catch (err) {
        // Drop malformed frame
      }
    });

    eventSource.onerror = () => {
      disconnect();
      if (document.visibilityState === 'visible') {
        reconnectTimer = setTimeout(connect, 3000);
      }
    };
  } catch (err) {
    // Fallback if EventSource fails to construct
  }
}

function disconnect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
}

async function handleGitStatus(data) {
  if (!data) return;

  if (data.gitChanges !== undefined) S.meta.gitChanges = data.gitChanges;
  if (data.gitFiles !== undefined) S.meta.gitFiles = data.gitFiles;

  updateSidebarToggleState();
  if (treeEl?.classList.contains('changed-only') && (!S.meta?.gitChanges || S.meta.gitChanges <= 0)) {
    await setSidebarMode('files');
  }

  const statuses = data.statuses || {};
  const dirtyDirs = data.dirtyDirs || {};

  // Patch rendered tree items in place without full DOM reload
  await patchTreeGitStatus(statuses, dirtyDirs);

  // Close tabs that were opened in git diff view or currently in diff view if their changes are gone
  for (let i = S.tabs.length - 1; i >= 0; i--) {
    const t = S.tabs[i];
    const code = statuses[t.path];
    const isDiff = !!code && code !== 'U';
    const wasDiff = !!(t.diffMode || t.openedInDiffView);
    if (wasDiff && (t.diffAvailable || t.diffMode) && !isDiff) {
      closeTab(i);
    }
  }

  // Synchronize open tabs' diff badges
  let tabsChanged = false;
  for (const t of S.tabs) {
    const code = statuses[t.path];
    const isDiff = !!code && code !== 'U';
    if (t.diffAvailable !== isDiff) {
      t.diffAvailable = isDiff;
      tabsChanged = true;
    }
  }
  if (tabsChanged) {
    drawTabs();
  }

  // Update active editor gutter and diff view if active document is affected
  const curDoc = doc_();
  if (curDoc) {
    const curCode = statuses[curDoc.path];
    const hasDiff = !!curCode && curCode !== 'U';

    if (curDoc.diffAvailable !== hasDiff || curCode) {
      curDoc.diffAvailable = hasDiff;
      await loadGutter(curDoc);
      render();
      if (curDoc.diffMode) {
        syncDiffView(true);
      }
      updateStatus();
    }
  }
}
