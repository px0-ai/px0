# px0 Internal Architecture & Design Documentation

Welcome to the internal engineering documentation for px0, an ultra-lightweight, zero-config code reader and navigator that delegates edits to the user's coding agent, packaged as a single statically-linked binary (~9.5 MB).

This directory contains in-depth technical write-ups explaining how px0 achieves sub-millisecond startup, instantaneous file navigation, deep code intelligence, and a minimal memory footprint (~20 MB RSS) across codebases containing tens of thousands of files.

## 1. Subsystem Architecture Map

```mermaid
flowchart TD
    subgraph Client ["Browser Frontend (Vanilla JS + CSS, Virtualized)"]
        UI["DOM Shell (Rail, Sidebar, Tabs, Status)"]
        VirtEditor["Virtualized Code Viewer (~60 DOM rows)"]
        MDViewer["Sanitized Markdown Preview Engine"]
        Inspector["Right Inspector (Call Hierarchy / References)"]
        Palette["Command Palette & Fuzzy Picker"]
    end

    subgraph GoServer ["px0 Go Backend (HTTP / JSON / Gzip)"]
        Router["HTTP Router & Middleware"]
        Scavenger["Memory Scavenger (FreeOSMemory after 15s)"]
        IndexEngine["In-Memory Index (Paths & Tree Nodes)"]
        IgnoreEngine["Optimized Classification Ignore Engine"]
        FuzzyEngine["Two-Pass Bounded Fuzzy Matcher"]
        SearchEngine["Parallel Search Pool (Buffer Reuse)"]
        HLEngine["Windowed Syntax Highlighter (Chroma + LRU)"]
        LSPMgr["Lazy LSP Lifecycle Manager"]
        GitEngine["Git Status & Gutter Engine"]
    end

    subgraph HostFS ["Host Operating System & Tools"]
        FS["Workspace Files & .gitignore"]
        GitCLI["git CLI (porcelain=v2, diff)"]
        LSPBinaries["Installed Language Servers (gopls, rust-analyzer, etc.)"]
    end

    Client <-->|"HTTP / JSON (Pooled Gzip)"| Router
    Router --> IndexEngine
    Router --> SearchEngine
    Router --> FuzzyEngine
    Router --> HLEngine
    Router --> LSPMgr
    Router --> GitEngine
    Router --> Scavenger

    IndexEngine --> FS
    IgnoreEngine --> FS
    SearchEngine --> FS
    HLEngine --> FS
    GitEngine --> GitCLI
    LSPMgr --> LSPBinaries
```

## 2. Documentation Directory

The internal documentation is modularized into the following focused guides:

### Core Architecture & Server Runtime

- [System Architecture & Runtime Lifecycle](architecture.md): High-level architectural tenets, single-binary distribution, sub-millisecond startup sequence, HTTP router and endpoints, proactive memory scavenging (`debug.FreeOSMemory()`), and path sandboxing.
- [Filesystem Indexing & Ignore Engine](indexing-and-ignore.md): Bounded-concurrency directory traversal (`NumCPU * 4`), instant root availability, symlink cycle immunity, and the custom classification-based `.gitignore` engine.
- [Performance & Comparative Benchmarks](../../BENCHMARKS.md): Measurement methodology across the 7-repository test corpus (Flask to the Linux Kernel) and side-by-side memory/CPU comparison against the VS Code process tree.

### Search & Intelligence Engines

- [Fuzzy Path Matching](fuzzy-search.md): Two-pass bounded search algorithm ($O(N)$ scan time matching dynamic programming ranking accuracy), weighted scoring matrix, and parallel query slicing.
- [Workspace Search & Symbol Extraction](workspace-search.md): Multi-core parallel grep, buffer reuse (`workBuf`), whole-file rejection fast paths (`bytes.Contains`), smart snippet elision (`{Pre, Mid, Post}`), and regex outline extraction.
- [Windowed Syntax Highlighting](syntax-highlighting.md): Solving Chroma lexer bottlenecks with viewport-based windowing (`hlChunk = 1000`), byte-capping (`512 KB`), dual-tier tokenization (instant inexact window + background exact pass), and byte-budgeted LRU caching.
- [Language Server Protocol (LSP) Architecture](lsp-and-intelligence.md): Lazy on-demand server lifecycle, zero-cost background binary discovery, external path boundary control, stateless call hierarchy trails, in-app installer recipes, and regex fallback.
- [Git Awareness & Diffing](git-integration.md): CLI shell-out architecture that never stages or commits, concurrent status generation with indexing, ancestor folder dirty propagation, gutter diff parsing, and the client-side split/unified diff renderer.
- [Harness Editing & Agent Dispatch](agent-editing.md): The optional agent flow. Starting an edit from the selection bar, right-click menu or diff view, headless invocation contract for Claude Code / Gemini CLI / Cursor Agent, inline failure output, single-flight and uncommitted-work guards, change detection, undo of the last edit, and the cache, language-server and tab reload path.

### Frontend & UI Subsystems

- [Editor Virtualization & Caret Engine](editor-virtualization.md): Custom ~60-row DOM virtualization, offscreen sub-pixel font measurement, selection preservation across repaints, decoupled overlay caret, and non-destructive inline decorations.
- [File Updates & In-Place Tab Reloading](file-reload-and-updates.md): End-to-end flow for workspace reindex and tab refreshing, in-place document reconciliation, concurrent chunk fetches, live viewport/markdown scroll snapshotting, and file shrinkage handling.
- [Markdown Preview Implementation](markdown.md): Goldmark pipeline, source line anchors (`data-line`), robust browser-side DOM allowlist sanitizer, and synchronized bi-directional scrolling between preview and source.
- [Theme Architecture & CSS Tokens](styling-and-themes.md): CSS custom property token architecture, zero literal colors in `style.css`, dynamic stylesheet discovery at `/static/themes.css`, and custom theme authoring.

### Operations & Maintenance

- [AI Agent Operational Guidelines](../agents/README.md): Engineering principles for AI coding agents, mandatory documentation maintenance protocol, and frontend codebase index.
- [Publishing & Release Guide](../../PUBLISHING.md): Step-by-step instructions for preparing, testing, and publishing new px0 releases.
