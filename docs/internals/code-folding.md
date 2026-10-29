# Code Folding Architecture

Folding adds optional metadata to the existing source document and changes its
visual projection. It does not copy the source, create an AST, invoke Chroma a
second time, spawn a language server, or create workspace files.

## Detection and lifetime

`internal/render/folding.go` scans the source already retained by `render.Doc`.
It runs only through `GET /api/folds?path=...&version=...`, requested after the
first source paint. The initial `/api/file` response supplies an opaque
mtime/size version. A mismatched version returns HTTP 409.

The scanner is linear in source bytes with fixed-size delimiter/suite stacks.
Completed ranges are sorted by opening line and duplicate opening lines retain
the outermost range. The result consists of eight-byte numeric pairs: a visible,
one-based opening line and the last hidden line, inclusive. Strings and comments
are excluded. Go declaration keywords mark parenthesized import/const/var/type
groups without treating ordinary calls or parameter lists as foldable blocks.
A closing line shared with another branch remains visible.

`internal/render/folding_tags.go` handles HTML/XML using a fixed element stack.
Tag names borrow slices during detection; results retain only numeric line pairs.
The scanner skips quoted attributes, processing instructions, and DTD internal
subsets without parsing entities or fetching external resources. Multiline
comments and CDATA produce ranges. HTML void elements never enter the stack;
raw-text content is opaque until its matching closing tag. XML names and foreign
SVG/MathML children preserve case. No DOM, parser dependency, per-line state,
source copy, or additional document fields are introduced for markup folding.
Malformed unmatched descendants are discarded when a matching ancestor closes.

Detection is single-flight per document, cancellable, limited to two concurrent
requests, and subject to a two-second request deadline. Limits are 8 MiB of
source, 8,192 ranges, and 256 nested delimiters/suites. Results are charged to the
existing render cache using their allocated capacity; closing, eviction, and
idle scavenging share the document's existing lifetime. There is no second cache.

## Browser geometry

`web/src/folding.js` is a pure module with no DOM dependencies. Range endpoints
use `Uint32Array`; collapse flags use `Uint8Array`. An additional sparse list
contains only outer collapsed intervals and cumulative hidden-line counts.
Nested collapse choices remain stored without counting hidden lines twice.

There is no visibility array proportional to the source line count. Coordinate
lookups allocate no objects. Source-to-row and row-to-source mapping use binary
search. Wrapped geometry reuses the existing original-line height offsets and
stores cumulative hidden pixel heights only per collapsed interval. Those sums
are refreshed when fold state or measured wrap offsets change, not every frame.

`renderer.js` preserves its normal uncollapsed paint window. When folds are
active, it jumps over hidden source spans while generating viewport rows and
requests only chunks containing those visible rows. `data-l` always means the
original source line. Row lookup uses its direct-index fast path or binary search
over the bounded set of live rows. Fold generation participates in paint caching.

## Interaction and cleanup

`folding-ui.js` owns the deferred active-document request, gutter actions, and
bulk actions. Switching away aborts pending detection; closing/reloading cancels
requests and releases fold metadata. Responses are checked against document
identity and source version. Unchanged-source refreshes retain collapse choices
while releasing references to the old wrap map.

Explicit source navigation expands enclosing ranges. Relative cursor movement
uses visible-row coordinates. Fold controls and hidden-line counts live outside
`.c` so source selection and caret offsets contain only source text.

A selection spanning folded lines is resolved on demand using
`/api/file?text=1&start=...&count=...&version=...`. That path returns a borrowed
source slice encoded as JSON without highlighting or populating hidden chunks.
The browser retains at most the current selected snippet, releases it when
selection ends, and waits for complete text before copy/edit/thread actions.
Vim visual actions keep the selection alive while that request resolves, then
act only if the document, selection anchor, and visual mode still match. Leaving
direct edit mode expands folds enclosing the returned source anchor. Unchanged
refreshes retain a detection error so explicit fold commands can retry it.

## Verification

`internal/render/folding_test.go` and `folding_tags_test.go` cover lexical cases, bounds, cancellation,
concurrent cache reuse, and raw selection boundaries. `folding_http_test.go`
covers endpoint access/version checks and document invalidation.
`web/src/folding.test.js` checks all collapse combinations against a naive model,
nested state, EOF movement, wrapped geometry, and sparse storage for a
400,000-line document.
`web/src/folding-interactions.test.js` exercises the real Vim and edit-mode
handlers with mocked service/UI boundaries: delayed hidden-source actions,
cancellation, load failure, and returning to a source line inside nested folds.

Run `node scripts/bench-folding.js` for browser-model lookup timings and typed
storage size, and `go test ./internal/render -run '^$' -bench 'Fold' -benchmem`
for scanner/cached lookup timings and allocations. Compare latency, server RSS,
and browser heap against the same workload on unchanged master; metadata size
alone does not prove equal total RAM use.

## Local verification results

Representative Apple M1 measurements from this implementation run:

| Check | Result |
| --- | --- |
| Cached backend range lookup | about 36–40 ns, 0 B/op, 0 allocs/op |
| Scanner, 80 KB Go fixture / 4,000 ranges | about 0.66–0.80 ms |
| HTML scanner, 180 KB / 4,000 ranges | about 0.70 ms, 18 allocations (range storage and sorting) |
| XML scanner, 160 KB / 6,000 ranges | about 0.68 ms, 19 allocations (range storage and sorting) |
| Sparse coordinate lookups, 8,000 collapsed blocks | about 26–35 ns in Node.js |
| Typed browser metadata, 400,000 lines / 8,000 collapsed blocks | 232,008 bytes, excluding JS object overhead |
| Cold-open HTTP median, 7 large-file cycles | master 27.17 ms; feature 27.49 ms |
| Rendered rows, 400,000-line browser fixture | 4 collapsed; 65 expanded, stable across 20 cycles |

The existing document-creation benchmark retains six allocations; the allocated
byte count increases by 16 bytes per document for the additional fields and
allocator rounding. Fold metadata itself is allocated lazily and bounded.
Server RSS samples varied with active browser connections and GC timing, so they
are not evidence of zero memory overhead. The available browser did not expose
JS heap measurements. A controlled browser heap/RSS comparison remains necessary
before making a claim about unchanged total application RAM.

Frontend validation, asset bundling, folding endpoint/model tests, scanner race
checks, and the binary build passed. The full Go suite has existing failures in
`TestAgentCancelJob`, `TestHandleDiffPRSplitsPRAndYourChanges`,
`TestPRReviewerChangesInIndex`, `TestGitStagedFilesStatAndDiff`, and
`TestDeletedFileHandling`; the failures were also reproduced on unchanged master
in a temporary Git checkout. The remaining full suite passed with those five
explicitly excluded. The public-tips fixture test is skipped when the separate
sibling website checkout is absent; the built-in tips are still validated.
