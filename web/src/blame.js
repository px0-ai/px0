// web/src/blame.js
// Git blame on hover: shows who last touched the line under the pointer, in
// the same hover-card used for LSP hover (see hover.js). A file's blame is
// fetched once and cached on its tab -- hovering after that is a plain array
// lookup, so turning this on costs nothing in the virtualized paint() loop.
import { $, S, esc, api } from './state.js';

// Fetches and caches a doc's blame once; concurrent callers share the same
// in-flight request. d.blame ends up null (not undefined) when unavailable,
// so it's never re-requested for that tab.
export async function ensureBlame(d) {
  if (d.blame !== undefined) return d.blame;
  try {
    d.blameReq = d.blameReq || api('/api/blame', { path: d.path });
    const j = await d.blameReq;
    d.blame = j.available ? { commits: j.commits, lines: j.lines } : null;
  } catch {
    d.blame = null;
  } finally {
    d.blameReq = null;
  }
  return d.blame;
}

const UNITS = [['year', 31536000], ['month', 2592000], ['week', 604800], ['day', 86400], ['hour', 3600], ['minute', 60]];

// "3 months ago", GitHub/GitLens style, computed client-side so the server
// never has to know the viewer's clock or locale.
function relativeTime(unixSeconds) {
  const secs = Math.max(0, Math.round(Date.now() / 1000 - unixSeconds));
  for (const [name, span] of UNITS) {
    const n = Math.floor(secs / span);
    if (n >= 1) return n + ' ' + name + (n === 1 ? '' : 's') + ' ago';
  }
  return 'just now';
}

export function blameHTML(commit) {
  const when = commit.time ? relativeTime(commit.time) : 'uncommitted';
  return (
    '<div class="sig">' + esc(commit.author) + ', ' + when + '</div>' +
    (commit.summary ? '<div class="doc">' + esc(commit.summary) + '</div>' : '') +
    '<div class="foot"><b>' + esc(commit.short || 'blame') + '</b></div>'
  );
}

export function setBlame(on) {
  S.blame = on;
  try { localStorage.setItem('px0.blame', on ? 'true' : 'false'); } catch {}
  $('[data-action="blame"]')?.classList.toggle('active', on);
}
