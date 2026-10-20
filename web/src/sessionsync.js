// web/src/sessionsync.js
// Persists open tabs and active tab index to the server (/api/session)
// and restores them on page load. Tabs survive browser refresh.
import { S, api, apiPost } from './state.js';
import { openFile, switchTab } from './tabs.js';
import { openDirs } from './tree.js';
import { on } from './bus.js';

let syncTimer = 0;
let lastJSON = '';

export async function restoreSession() {
  try {
    const sess = await api('/api/session');
    if (!sess || !sess.tabs || !sess.tabs.length) return false;

    // Restore open directories first
    if (sess.openDirs) {
      for (const d of sess.openDirs) openDirs.add(d);
    }

    // Open each saved tab sequentially
    for (const tab of sess.tabs) {
      if (tab.path) {
        try { await openFile(tab.path, { ref: tab.ref || '' }); } catch {}
      }
    }

    // Switch to the previously active tab
    if (typeof sess.active === 'number' && sess.active >= 0 && sess.active < S.tabs.length) {
      switchTab(sess.active);
    }

    return S.tabs.length > 0;
  } catch { return false; }
}

function scheduleSync() {
  clearTimeout(syncTimer);
  syncTimer = setTimeout(doSync, 500);
}

function doSync() {
  const tabs = S.tabs.map(t => {
    const entry = { path: t.path };
    if (t.diffRef) entry.ref = t.diffRef;
    return entry;
  });
  const payload = { tabs, active: S.active, openDirs: Array.from(openDirs) };
  const json = JSON.stringify(payload);
  if (json === lastJSON) return; // no change
  lastJSON = json;
  apiPost('/api/session', payload).catch(() => {});
}

export function initSessionSync() {
  on('tab:activated', scheduleSync);
  on('tab:close', scheduleSync);
  on('file:open', scheduleSync);
  // Also sync on visibility change (user switches away)
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) doSync();
  });
  // Sync before page unload
  window.addEventListener('beforeunload', doSync);
}
