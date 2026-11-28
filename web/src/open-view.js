export function openDiffByDefault({ hasDiff, deleted, view, preferDiff }) {
  return !!hasDiff && (!!deleted || view === 'diff' || (view !== 'source' && preferDiff === true));
}
