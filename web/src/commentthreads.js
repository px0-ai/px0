// Groups review comments into threads. Pure data in, data out: nothing here
// touches the DOM, so it can be tested under node.

// A thread belongs to one source. 'rv' is the agent's review (px0 -review);
// 'gh' is everything that lives on the pull request: GitHub's comments and the
// reviewer's own drafts. Keeping them apart means an agent comment never
// becomes the root of a GitHub thread and hides its Reply box.
export function ctThreadKey(path, side, line, source) {
  return path + '|' + (side || 'RIGHT') + ':' + line + '|' + source;
}

// existing is GitHub's inline comments plus the agent's; drafts are the
// reviewer's unsubmitted comments. A reply carries the same path, side and line
// as its root, so grouping by key alone gathers a whole thread.
// An outdated GitHub comment (its line has since been edited) points at where
// the code used to be, so it is filed under line 0 -- the file's own strip --
// rather than next to unrelated code.
// Returns Map<key, { key, path, side, line, endLine, source, outdated, existing, drafts }>.
export function ctGroupThreads(existing, drafts) {
  const threads = new Map();
  const threadFor = (c, source) => {
    const outdated = !!c.outdated;
    const side = c.side || 'RIGHT';
    const line = outdated ? 0 : (c.line || 0);
    const key = ctThreadKey(c.path, side, line, source);
    let t = threads.get(key);
    if (!t) {
      t = { key, path: c.path, side, line, endLine: 0, source, outdated: false, existing: [], drafts: [] };
      threads.set(key, t);
    }
    if (outdated) t.outdated = true;
    if (line && c.endLine > t.endLine) t.endLine = c.endLine;
    return t;
  };
  for (const c of existing || []) {
    if (!c.path) continue;
    threadFor(c, c.agent ? 'rv' : 'gh').existing.push(c);
  }
  for (const c of drafts || []) {
    if (!c.path) continue;
    threadFor(c, 'gh').drafts.push(c);
  }
  return threads;
}
