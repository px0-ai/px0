# Operational Guidelines for AI Agents

This document defines critical instructions, architectural principles, and documentation maintenance workflows for AI coding agents working on px0.

## 1. Core Architectural Tenets

1. Edits Go Through a Harness, Never Through px0: px0 does not author, format, or save changes to project files, and reads stay its hot path. The optional agent flow ([`agent.go`](../../agent.go)) composes an instruction anchored to a line range and hands it to a coding harness already installed on the machine; that harness performs the write, and px0 reloads what moved once it exits. The one write px0 makes itself is undoing the last harness edit ([`agent_undo.go`](../../agent_undo.go)), restoring bytes it captured before the run or blobs from git. Do not introduce file modification or editor save APIs that take content from the client.
1. Zero Runtime and Single Binary Footprint: Any change must compile into a single static binary (`go:embed` for web assets). Do not introduce runtime dependencies (no Node.js/npm runtime requirement, no external database, no CGO dependencies).
1. Stateless on Disk: px0 leaves zero configuration or temporary cache artifacts on the user filesystem (no local `.px0/` folders or cache files). Edit instructions are held in memory for the life of the process and are never persisted, so there is no review or comment store to migrate. The only writes to a working tree are those made by a dispatched harness, and an undo of the last one. Undo snapshots live in memory and die with the process.
1. Performance Budgets: Indexing must complete in milliseconds using bounded concurrency (`NumCPU * 4`). File open must remain $O(1)$ relative to file length using windowed chunking (`hlChunk = 1000`) and browser DOM virtualization. Maintain explicit memory reclamation (`debug.FreeOSMemory()` on idle).

## 2. Mandatory Documentation Maintenance Protocol

Whenever modifying, adding, or refactoring code in this repository, you must audit and update the documentation accordingly:

### Documentation Mapping Matrix

| Component Modified                   | Primary Source Files                                             | Docs to Update                                                                                                   |
| ------------------------------------ | ---------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| System Architecture / Optimizations  | All `.go` files, `web/app.js`                                    | [`docs/internals/architecture.md`](../internals/architecture.md)                                                 |
| Indexing / Tree Walk / Gitignore     | `index.go`, `ignore.go`                                          | [`docs/internals/indexing-and-ignore.md`](../internals/indexing-and-ignore.md), [`README.md`](../../README.md)    |
| Fuzzy File Finder                    | `fuzzy.go`                                                       | [`docs/internals/fuzzy-search.md`](../internals/fuzzy-search.md)                                                 |
| Search / Regex / Symbol Outlines     | `search.go`, `symbols.go`                                        | [`docs/internals/workspace-search.md`](../internals/workspace-search.md), [`BENCHMARKS.md`](../../BENCHMARKS.md) |
| Syntax Highlighting & Lexing         | `highlight.go`                                                   | [`docs/internals/syntax-highlighting.md`](../internals/syntax-highlighting.md), [`README.md`](../../README.md)   |
| Language Servers (LSP)               | `lsp.go`, `lspnav.go`, `lspservers.go`, `lspsetup.go`, `calls.go`| [`docs/internals/lsp-and-intelligence.md`](../internals/lsp-and-intelligence.md), [`README.md`](../../README.md)|
| Git Integration & Diffing            | `git.go`                                                         | [`docs/internals/git-integration.md`](../internals/git-integration.md)                                           |
| Harness Editing / Agent Dispatch     | `agent.go`, `agent_undo.go`, `settings.go`, `web/src/agent.js`, `web/src/selbar.js` | [`docs/internals/agent-editing.md`](../internals/agent-editing.md), [`README.md`](../../README.md)                |
| Frontend UI / Virtualization         | `web/app.js`, `web/index.html`, `web/style.css`                  | [`docs/internals/editor-virtualization.md`](../internals/editor-virtualization.md), [`README.md`](../../README.md)|
| Markdown Preview                     | `markdown.go`, `web/src/markdown.js`                             | [`docs/internals/markdown.md`](../internals/markdown.md), [`docs/internals/styling-and-themes.md`](../internals/styling-and-themes.md) |
| Themes / Colour Tokens               | `web/themes/*.css`, `web/style.css`, `web/src/theme.js`          | [`docs/internals/styling-and-themes.md`](../internals/styling-and-themes.md)                                    |
| CLI Flags / Configuration            | `main.go`                                                        | [`README.md`](../../README.md)                                                                                  |
| Performance Metrics / Scripts        | `benchmark.sh`                                                   | [`BENCHMARKS.md`](../../BENCHMARKS.md)                                                                           |
| Release Workflow                     | `Makefile`, `build.sh`, `scripts/build-web.js`                   | [`PUBLISHING.md`](../../PUBLISHING.md)                                                                           |

## 3. Checklist for Agents Prior to Submitting Work

- Verification: Ran `go test ./...` and confirmed all unit/regression tests pass (`ok px0`).
- Build Integrity: Verified successful build with `go build -o px0 .`.
- Web Bundling: If modifying `web/src/`, verified bundle update with `./scripts/build-web.js`.
- Architecture Sync: Any new optimization, algorithmic adjustment, or structural change is documented in the corresponding [`docs/internals/`](../internals/README.md) write-up.
- Flag & Shortcut Sync: Any new keyboard shortcut, UI behavior, or CLI flag is reflected in [`README.md`](../../README.md).
- Benchmark Alignment: If search, highlight, or index performance characteristics change, verify whether [`BENCHMARKS.md`](../../BENCHMARKS.md) requires updated notes or numbers.

## 4. Frontend Architecture & Code Map for Agents

To quickly locate and modify UI features, refer to this structured section index of [`web/index.html`](../../web/index.html) and `web/app.js`:

### HTML Structure

| Section / Element ID         | Description                                                                                                                                                                                                                                                                          |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `<nav id="rail">`            | Left activity rail (switch between Explorer, Search, Outline, Theme, Shortcuts).                                                                                                                                                                                                     |
| `<aside id="side">`          | Collapsible sidebar containing panels: `#panel-files` (tree), `#panel-search`, `#panel-outline`. Its `.panel-head` shows the workspace name (`#root-name`), the changed files filter button (`#btn-changed`), and the re-index button (`#btn-reindex`). Its `.side-foot` holds the px0 logo, the version (`#st-ver`), links to GitHub, and the theme button (`#btn-theme`). |
| `<div id="resizer">`         | Draggable splitter between sidebar and main editor viewport.                                                                                                                                                                                                                         |
| `<div id="tabs">` & `#crumbs`| Open file tabs bar and current file path breadcrumb navigation.                                                                                                                                                                                                                      |
| `<div id="editor">`          | Core editor container with `#viewport`, `#sizer`, and virtual rows container `#rows`.                                                                                                                                                                                                |
| `<div id="mdview">`          | Markdown preview over `#viewport` for Markdown tabs, holding `<article id="md" class="md">`. Every ID inside it is prefixed `md-`. The Preview / Source switch (`#md-switch`) and status bar Preview button (`data-action="md-preview"`) show only on Markdown tabs; `body.md-tab` marks that state. |
| `<div id="empty">`           | Welcome / splash screen shown when no files are open.                                                                                                                                                                                                                                |
| `<div id="hovercard">`       | Floating LSP type signature, doc preview, and quick reference buttons.                                                                                                                                                                                                              |
| `<div id="findbar">`         | In-file search overlay (Ctrl+F).                                                                                                                                                                                                                                                     |
| `<div id="agentbox">`        | Instruction composer for Edit with Agent: anchor (`#agent-ref`), harness switch (`#agent-harness`), picker (`#agent-pick`), instruction (`#agent-input`), and inline failure output with stdout / stderr (`#agent-err`).                                                                         |
| `<div id="sel-menu">`        | Right-click menu on a selection, rebuilt from the `#footer-sel` buttons each time it opens.                                                                                                                                                                                          |
| `<div id="agent-menu">`      | Harness menu opened from the footer's `Agent:` button.                                                                                                                                                                                                                               |
| `<div id="toast">`           | Floating bottom notification toast confirming clipboard actions.                                                                                                                                                                                                                     |
| `<footer id="status">`       | Bottom status bar: language, lines, size, cursor pos, LSP status, and index time. While code is selected, `#footer-sel` (Copy Ref, Copy for Agent, Edit with Agent, Find Usages) replaces the left-side buttons. `#footer-actions` also holds the harness switch (`data-action="agent-harness"`, opens `#agent-menu`) and Undo Edit (`data-action="agent-undo"`). Never wraps: `fitStatus()` in `status.js` adds cumulative `fit-1`..`fit-6` classes to hide detail as width runs out. |
| `<div id="overlay">`         | Modal overlay hosting Quick Open and Command Palette (`#palette`).                                                                                                                                                                                                                   |
| `<div id="helpsheet">`       | Keyboard shortcuts cheat-sheet modal overlay.                                                                                                                                                                                                                                        |

### Frontend Modules (`web/src/`)

The frontend is modularized into clean ES modules under `web/src/` and bundled into `web/app.js` using `scripts/build-web.js`:

| Module                 | Primary Responsibilities & Key Exports                                                                                                                                                       |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `web/src/state.js`     | Core state object `S`, `doc_()`, `api()`, `apiPost()`, `esc()`, `$`, `$$`, constants (`LH`, `CHUNK`, `OVERSCAN`, `MOD`, `isMac`). Per-OS shortcut labels: `keyLabel()`, `keyCaps()`, `withKeys()`, `applyKeyLabels()`. |
| `web/src/ui.js`        | DOM references (`vp`, `sizer`, `rowsEl`, `editor`, `toastEl`), `showToast()`, `copyToClipboard()`.                                                                                         |
| `web/src/renderer.js`  | `measure()`, `layout()`, `render()`, `paint()`, `toggleWordWrap()`, `toggleLineNumbers()`, on-demand chunk fetching.                                                                        |
| `web/src/tabs.js`      | `openFile()`, `closeTab()`, `switchTab()`, `drawTabs()`, `drawCrumbs()`, `showImage()`, `reloadOpenTabs()` (in-place tab refresh on reindex), `reopenClosedTab()` (Alt+Shift+T).          |
| `web/src/history.js`   | `pushHistory()`, `go()`: jump history back/forward (Alt+Left, Alt+Right).                                                                                                                   |
| `web/src/status.js`    | `updateStatus()`, `initMetrics()`, `updateMetricsDisplay()`, `setStatusNote()`, `fmtBytes()`, `setLspState()`, `drawLspStatus()`.                                                          |
| `web/src/cursor.js`    | `wordAtPoint()`, `moveCursor()`, click / double-click selection, occurrence highlight.                                                                                                       |
| `web/src/hover.js`     | `onMove()`, `hoverAt()`, `showHover()`, `hideHover()`, token link modifier handling.                                                                                                        |
| `web/src/selbar.js`    | Status bar selection mode: `updateSelectionBar()`, `hideSelectionBar()`, `runSelectionAction()`. Whole-file selection: `selectAll()`, `clearSelectAll()`, `copySelectAll()`. Diff-view selections, on either side of a split and including deleted lines, resolve to working-tree line numbers via `diffSelection()`. Right-click menu: `closeSelMenu()`.                  |
| `web/src/lsp.js`       | `gotoDefinition()`, `findReferences()`, `warmLSP()`, `lspCall()`, hit formatting.                                                                                                            |
| `web/src/tree.js`      | `drawTree()`, `fileKind()`, `revealDir()`, `revealFile()`, explorer tree click handlers.                                                                                                    |
| `web/src/search.js`    | `runSearch()`, `renderResults()`, `displayPath()`, workspace search panel.                                                                                                                   |
| `web/src/outline.js`   | `loadOutline()`, `upgradeOutline()`, `drawOutline()`, symbol kind badges.                                                                                                                    |
| `web/src/panels.js`    | `showPanel()`, sidebar switching, reindex trigger, draggable sidebar resizer.                                                                                                               |
| `web/src/inspector.js` | `showRightInspector()`, `setRightInspectorTab()`, `inspectReferences()`, right resizer.                                                                                                       |
| `web/src/calls.js`     | `showCalls()`, `initCalls()`, `openLspSetup()`: call trail tree in the right inspector (callers / callees via `/api/lsp/calls`).                                                            |
| `web/src/lspsetup.js`  | `renderLspSetup()`, `cancelLspSetup()`: install / detect-and-start panel shown when no language server is running.                                                                          |
| `web/src/find.js`      | `openFind()`, `clearFind()`, `runFind()`, `jumpToHit()`, minimap hit dots (Ctrl+F).                                                                                                         |
| `web/src/markdown.js`  | `syncPreview()`, `togglePreview()` (Alt+M), `previewing()`, `previewLine()`, `previewTopLine()`: Markdown preview, allowlist sanitizer, link and image resolution.                             |
| `web/src/palette.js`   | `openPalette()`, `refreshPalette()`, `COMMANDS`, fuzzy file/symbol/command finder.                                                                                                           |
| `web/src/shortcuts.js` | `showHelp()`, Alt+Z word wrap toggle, keyboard shortcuts listener.                                                                                                                          |
| `web/src/theme.js`     | `listThemes()`, `setTheme()`, `cycleTheme()`, `initTheme()`: theme discovery from loaded CSS and persistence.                                                                                |
| `web/src/agent.js`     | `initAgent()`, `openAgentEdit()`, `applyAgentMeta()`: the instruction composer, inline failure output, the poll of a running harness, the footer harness menu and Undo Edit button, and the reload shared by edit and undo (reindex, `reloadOpenTabs()`, tree redraw). |
| `web/src/main.js`      | Module initializations and application `boot()` sequence.                                                                                                                                    |
