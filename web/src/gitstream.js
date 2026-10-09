import { S, apiPost, doc_ } from './state.js';
import { updateSidebarToggleState, patchTreeGitStatus, treeEl, setSidebarMode, hasGitView } from './tree.js';
import { drawTabs, loadGutter, closeTab, reloadOpenTabs } from './tabs.js';
import { syncDiffView } from './diff.js';
import { render } from './renderer.js';
import { updateStatus, updateMetricsDisplay } from './status.js';
import { updateGitPanel } from './gitpanel.js';
import { refreshUnpushed } from './unpushed.js';
import { updateScopeCounts } from './prscope.js';

let eventSource = null;
let reconnectTimer = null;
let lastSig = '';
let refreshing = null;
let lastRefreshAt = 0;
let lastTrafficAt = Date.now();

function markTraffic() {
  lastTrafficAt = Date.now();
}

function reconnect() {
  disconnect();
  connect();
}

async function handleWake() {
  lastSig = ''; // Clear signature to ensure git-status, badges and unpushed state apply freshly
  reconnect();
  await triggerRefresh();
}

export function initGitStream() {
  connect();

  // Instant refresh when user focuses the browser window
  window.addEventListener('focus', () => {
    if (document.visibilityState === 'visible') {
      const now = Date.now();
      // If the connection is closed, or if no SSE traffic has arrived for >5s, reconnect
      if (!eventSource || eventSource.readyState !== EventSource.OPEN || (now - lastTrafficAt > 5000)) {
        handleWake();
      } else {
        triggerRefresh();
      }
    }
  });

  // Page Visibility API: pause streaming when tab is hidden, resume and refresh when visible
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') {
      disconnect();
    } else {
      handleWake();
    }
  });

  // Sleep/wake detection & connection watchdog:
  // When laptop lid closes, timers suspend. When reopened, the timer delta jumps significantly.
  let lastTick = Date.now();
  setInterval(() => {
    const now = Date.now();
    const gap = now - lastTick;
    lastTick = now;

    // 1. Laptop lid closed/reopened or OS sleep: delta > 3000ms (expected ~1000ms)
    if (gap > 3000) {
      handleWake();
      return;
    }

    // 2. Stream watchdog: if tab is visible and no SSE events arrived for >12s, reconnect
    if (document.visibilityState === 'visible' && (now - lastTrafficAt > 12000)) {
      handleWake();
    }
  }, 1000);
}

export function triggerRefresh() {
  if (refreshing) return refreshing;
  refreshing = (async () => {
    try {
      if (S.meta?.git) {
        const data = await apiPost('/api/git/refresh');
        await handleGitStatus(data);
      }
      await reloadOpenTabs({ onlyIfChanged: true });
    } catch (e) {
      // Quiet fail on network hiccups
    } finally {
      lastRefreshAt = Date.now();
      refreshing = null;
    }
  })();
  return refreshing;
}

function connect() {
  if (eventSource && eventSource.readyState !== EventSource.CLOSED) return;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }

  try {
    const streamUrl = new URL('api/stream', document.baseURI || location.href).href;
    eventSource = new EventSource(streamUrl);
    markTraffic();

    eventSource.onopen = () => {
      markTraffic();
    };

    eventSource.addEventListener('git-status', async e => {
      markTraffic();
      try {
        const data = JSON.parse(e.data);
        await handleGitStatus(data);
      } catch (err) {
        // Drop malformed frame
      }
    });

    eventSource.addEventListener('metrics', e => {
      markTraffic();
      try {
        const data = JSON.parse(e.data);
        updateMetricsDisplay(data);
      } catch (err) {
        // Drop malformed frame
      }
    });

    eventSource.addEventListener('ping', () => {
      markTraffic();
    });

    eventSource.onerror = () => {
      disconnect();
      if (document.visibilityState === 'visible') {
        reconnectTimer = setTimeout(connect, 1500);
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

  // Identical snapshot to the last one applied (refocus, SSE reconnect): the
  // tree, panel and gutters are already right. Open modified tabs are still
  // re-checked, since a file can change on disk while its status stays "M",
  // but they only repaint if something actually differs.
  const sig = JSON.stringify(data);
  if (sig === lastSig) {
    await reloadOpenTabs({ onlyIfChanged: true });
    return;
  }
  lastSig = sig;

  if (data.gitChanges !== undefined) S.meta.gitChanges = data.gitChanges;
  if (data.gitFiles !== undefined) S.meta.gitFiles = data.gitFiles;

  // Before the Git-view check below: committing empties the tree but fills the
  // Unpushed section, and pushing empties that in turn, so whether Git view
  // still has anything to show depends on a fresh unpushed count.
  // Cheap: only re-reads git log when the ahead count or head commit moved.
  await refreshUnpushed(data);

  updateSidebarToggleState();
  if (treeEl?.classList.contains('changed-only') && !hasGitView()) {
    await setSidebarMode('files');
  }

  const statuses = data.statuses || {};
  const dirtyDirs = data.dirtyDirs || {};
  const staged = data.staged || {};
  const yourStatuses = data.yourStatuses || {};
  const yourDirtyDirs = data.yourDirtyDirs || {};

  // Patch rendered tree items in place without full DOM reload
  try {
    await patchTreeGitStatus(statuses, dirtyDirs, staged, yourStatuses, yourDirtyDirs);
  } catch (err) {
    console.error('Failed to patch tree git status', err);
  }
  try {
    updateScopeCounts(statuses, yourStatuses);
  } catch (err) {
    console.error('Failed to update scope counts', err);
  }
  try {
    updateGitPanel(data);
  } catch (err) {
    console.error('Failed to update git panel', err);
  }

  // Close tabs that were opened in git diff view or currently in diff view if their changes are gone.
  // In PR review mode, tabs should remain open even if clean relative to HEAD.
  if (!S.meta?.pr) {
    for (let i = S.tabs.length - 1; i >= 0; i--) {
      const t = S.tabs[i];
      // A tab pinned to a commit shows a frozen diff; the working tree going
      // clean is exactly when you want to still be reading it.
      if (t.diffRef) continue;
      const code = statuses[t.path];
      const isDiff = !!code && code !== 'U';
      const wasDiff = !!(t.diffMode || t.openedInDiffView);
      if (wasDiff && (t.diffAvailable || t.diffMode) && !isDiff) {
        closeTab(i);
      }
    }
  }

  // Check if any open tabs are affected by modifications
  const anyTabModified = S.tabs.some(t => {
    if (t.diffRef) return false; // frozen at a commit; the working tree can't move it
    const code = statuses[t.path];
    return code && code !== 'U';
  });

  if (anyTabModified) {
    // In-place reload of open tabs updates file lines, syntax highlighting, and diff view live
    await reloadOpenTabs({ onlyIfChanged: true });
  } else {
    // Synchronize open tabs' diff badges
    let tabsChanged = false;
    for (const t of S.tabs) {
      if (t.diffRef) continue;
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
    if (curDoc && !curDoc.diffRef) {
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
}
