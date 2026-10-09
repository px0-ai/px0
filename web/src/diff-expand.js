// Pure expansion planning for diff.js. Kept DOM-free so upward movement can be
// smoke-tested without a browser.
export const EXPAND_STEP = 20;

export function hasExpanded(ranges, g1, g2) {
  return ranges.some(r => r.e >= g1 && r.s <= g2);
}

export function gapHidden(ranges, g1, g2) {
  let s = g1, e = g2;
  for (const r of ranges) {
    if (r.e < g1 || r.s > g2) continue;
    if (r.s <= s) s = Math.max(s, r.e + 1);
    else { e = Math.min(e, r.s - 1); break; }
  }
  return e < s ? null : [s, e];
}

export function upwardExpandRun(ranges, g1, g2) {
  const hidden = gapHidden(ranges, g1, g2);
  if (!hidden) return null;
  const [s, e] = hidden;
  return [Math.max(s, e - EXPAND_STEP + 1), e];
}

/* Render order for a gap above a hunk. The control must precede context so it
   relocates from the hunk header to the leading edge after first expansion. */
export function upwardGapPlan(ranges, g1, g2) {
  const context = [];
  for (const r of ranges) {
    if (r.e < g1 || r.s > g2) continue;
    context.push([Math.max(r.s, g1), Math.min(r.e, g2)]);
  }
  return {
    context,
    expand: context.length ? upwardExpandRun(ranges, g1, g2) : null,
  };
}
