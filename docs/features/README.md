# px0 Features Guide

px0 is an ultra-fast, zero-config code reader and navigator specifically optimized for fast reads, remote inspection, and AI-assisted workflows. It provides instant access to codebases of any size with minimal resource consumption (~20–30 MB server RAM, ~100–180 MB total including the browser tab, < 1 ms startup).

This directory provides comprehensive documentation for all px0 user-facing features, detailing how each capability works, its role in developer and AI agent pairing workflows, interactive controls, and configuration options.

---

## Feature Matrix

| Feature Area | Primary Shortcut | Core Capability | Detailed Guide |
| :--- | :--- | :--- | :--- |
| **Fuzzy File Search** | `Cmd/Ctrl+P`, `Cmd/Ctrl+K` | Instant fuzzy path finding across 100,000+ files with recency ranking | [Fuzzy File Search](fuzzy-file-search.md) |
| **Workspace Search** | `Cmd/Ctrl+Shift+F` | Full-repository literal and regex text search with match previews | [Workspace Search](workspace-search.md) |
| **Symbol Outline** | `Cmd/Ctrl+Shift+O` | In-file function, class, and struct hierarchy with regex fallback | [Symbol Outline](symbol-outline.md) |
| **In-File Find & Caret** | `Cmd/Ctrl+F`, `Cmd/Ctrl+G` | Active document search, minimap match markers, and line jumps | [In-File Search](in-file-search.md) |
| **Git Awareness, Diffs & Stage/Commit/Push/Pull** | `Cmd/Ctrl+D` | Real-time status stream, stat cache fast-path, split / unified diffs, a sidebar panel to stage/commit/push/fast-forward-pull, and AI-written commit messages | [Git Integration](git-integration.md) |
| **GitHub PR Review** | `px0 <pr-url>`, `Alt+R` | Full-tree checkout of a pull request, merge-base diffing, draft comments with Approve / Request Changes / Comment, and committing/pushing/pulling straight from the checkout | [GitHub PR Review](github-pr-review.md) |
| **Coding Agent Editing** | `Alt+E`, Right-click | Delegating edits to Claude Code, Gemini CLI, Cursor Agent, and more | [Agent Editing](agent-editing.md) |
| **Threads** | `Alt+T` | Long-running, multi-turn conversations with your coding harness that can read and change any file, with per-turn changed files | [Threads](threads.md) |
| **Semantic Code Intelligence** | `F12`, `Shift+F12`, `Alt+Shift+H` | Go to Definition, Find References, Call Trails, and Hover docs | [LSP & Intelligence](lsp-code-intelligence.md) |
| **Markdown Preview** | `Alt+M` | Full GFM preview, syntax-highlighted code fences, and scroll sync | [Markdown Preview](markdown-preview.md) |
| **Image Viewer & Assets** | Click image file / lightbox | Standalone image tabs, zoom/pan transforms, and markdown lightbox | [Image Viewer](image-viewer.md) |
| **Settings & Preferences** | `Cmd/Ctrl+,` | Graphical form editor, raw JSON sync, and instant live preview | [Settings & Configuration](settings-and-configuration.md) |
| **Syntax Highlighting** | Automatic | Viewport-windowed Chroma lexing for ~280 languages | [Syntax Highlighting](syntax-highlighting.md) |
| **Themes & Styling** | Settings / Palette | 14 built-in dark and light themes powered by CSS tokens | [Themes & Styling](themes-and-styling.md) |
| **Selection Actions** | `Alt+C`, `Alt+A`, `Alt+U` | Copy reference, format LLM context, find usages, and edit | [Selection Actions](selection-actions.md) |
| **Virtualized Scroller** | Automatic | Virtual DOM rendering ~60 rows with idle memory scavenging | [Editor Virtualization](editor-virtualization.md) |
| **Remote Workspaces** | CLI flags | Zero-config remote browsing over Tailscale, SSH-free operation | [Remote Workspaces](remote-workspaces.md) |
| **Vim Keybindings** | Toggle in Settings | Modal normal, visual, and motion modes for keyboard navigation | [Vim Mode](vim-mode.md) |
| **File Explorer** | `Cmd/Ctrl+B` | High-density tree, expand/collapse-all controls, compact folder chains, and tab management | [File Explorer](file-explorer.md) |

---

## 1. Navigation & Search

Fast code discovery is essential when reviewing large codebases or inspecting agent changes. px0 provides three complementary navigation layers:

- **[Fuzzy File Search & Quick Open](fuzzy-file-search.md)**: Jump directly to any file in the workspace using progressive fuzzy filtering (`Cmd/Ctrl+P`). It scores matches by boundary transitions, file extensions, and recent tab activity so target files appear with only 2–3 keystrokes.
- **[Full Workspace Search](workspace-search.md)**: Search across every file in the repository for text patterns or regular expressions (`Cmd/Ctrl+Shift+F`). Results are grouped by file with context snippets and match counts.
- **[In-File Search & Caret Navigation](in-file-search.md)**: Quickly find occurrences within the currently active document (`Cmd/Ctrl+F`), jump to line and column coordinates (`Cmd/Ctrl+G`), and traverse browsing history (`Alt+Left` / `Alt+Right`).

---

## 2. Code Intelligence & Structure

Understanding complex software systems requires more than raw text search. px0 delivers structural and semantic code intelligence while maintaining an ultra-lightweight footprint:

- **[Symbol Outline Navigation](symbol-outline.md)**: Open a structured list of symbols (`Cmd/Ctrl+Shift+O`) to view all functions, methods, classes, types, and variables defined within the active document.
- **[Language Server Protocol (LSP) Integration](lsp-code-intelligence.md)**: Tap into semantic analysis for Go, Rust, TypeScript, Python, C/C++, and other languages. Jump directly to definitions (`F12`), explore all reference call sites (`Shift+F12`), inspect interactive Call Trails (`Alt+Shift+H`), and view documentation cards on hover.
- **[Syntax Highlighting & Language Support](syntax-highlighting.md)**: Beautiful native highlighting for ~280 programming languages and markup formats via Chroma, enhanced with bracket pair colorization and word occurrence highlighting.

---

## 3. Git Integration & Review

Modern developers spend substantial time verifying diffs and reviewing code generated by background agents:

- **[Git Awareness & Visual Diff Viewer](git-integration.md)**: Real-time Git status synchronization powered by Server-Sent Events and sub-millisecond control file stat checks. Status badges and parent directory dirty indicators update without full re-indexing. A segmented sidebar switch toggles between the file explorer and uncommitted Git changes, while `Cmd/Ctrl+D` reveals side-by-side or unified diffs with live gutter indicators and automatic tab cleanup when changes are checked out or reset.
- **[Stage, Commit, Push, Pull](git-integration.md)**: A resizable git panel at the bottom of the sidebar stages files (per-file tick or Stage All), commits, pushes, and pulls fast-forward-only (a diverged history is refused rather than merged). **Commit with AI** writes the commit message from the staged diff with your configured coding harness and commits with it in one action.
- **[GitHub Pull Request Review](github-pr-review.md)**: `px0 <pr-url>` checks out a PR's full source tree into its own process, diffs it against the PR's merge-base instead of `HEAD`, provides real-time progress spinning, detects already-merged PRs with confirmation prompts, lets you draft inline review comments, batch-apply them with coding agents, or submit Approve / Request Changes / Comment back to GitHub — and the same git panel lets you commit and push fixes back to the PR's own branch, or pull in commits pushed by someone else mid-review.

---

## 4. Coding Agent Editing & Delegation

px0 intentionally omits a heavyweight text editor in favor of direct collaboration with the user's preferred coding agents:

- **[Editing with Coding Agents](agent-editing.md)**: Select any code block in a source file or git diff, press `Alt+E` (or right-click), describe the required change, and px0 delegates the task directly to your chosen agent harness (Claude Code, Gemini CLI, Cursor Agent, Antigravity, OpenCode, Codex, Aider, or Goose). Execution progress streams to the launch terminal, while px0 automatically detects file changes and reloads tabs in place upon completion.
- **[Threads](threads.md)**: For questions and multi-file work, select code and press `Alt+T` to open a long-running conversation in the right sidebar. The harness can read and change any file, replies stream in with the files each turn touched, and every message continues the same conversation. Threads are saved and survive restarts.
- **[Selection Toolbar & Context Actions](selection-actions.md)**: High-ergonomic footer toolbar providing instant buttons to copy canonical path:line references (`Alt+C`), copy formatted code snippets with surrounding context tailored for LLM chat windows (`Alt+A`), find usages (`Alt+U`), and dispatch inline edits (`Alt+E`).

---

## 5. Documentation & Media Viewing

Repositories contain documentation, architecture notes, and graphical assets alongside code:

- **[Rendered Markdown Preview](markdown-preview.md)**: Read project documentation, RFCs, and README files in rendered GitHub Flavored Markdown (GFM) mode (`Alt+M`), complete with tables, task lists, footnotes, and GitHub-style alert callouts (`[!NOTE]`, `[!WARNING]`). Features bi-directional scroll synchronization and one-click copy buttons on code blocks.
- **[Image Viewer & Asset Inspection](image-viewer.md)**: Open image files (PNG, SVG, JPG, WebP, GIF, etc.) as native interactive tabs. Smoothly zoom up to 3200%, pan freely, toggle alpha background modes (checkerboard, dark matte, light matte), switch between bilinear smoothing and pixelated rendering, and click inline Markdown images to inspect them in a modal lightbox.

---

## 6. Ergonomics & Customization

px0 adapts to developer habits and viewing environments without requiring manual config file tinkering:

- **[Settings & Preferences System](settings-and-configuration.md)**: Comprehensive settings manager accessible via `Cmd/Ctrl+,`. Offers a VS Code-style graphical UI with interactive attribute pills alongside a raw JSON editor (`~/.px0/settings.json`) that updates themes, typography, diff layouts, and search behavior in real time without refreshing the browser.
- **[Themes & Styling](themes-and-styling.md)**: 14 built-in dark and light themes crafted for high readability and visual consistency across all panels, diffs, and markdown documents.
- **[Vim Keybindings & Modal Navigation](vim-mode.md)**: Full modal navigation emulation supporting Normal, Visual, and Motion modes for developers accustomed to Vim, Neovim, or Helix.
- **[File Explorer & Workspace Management](file-explorer.md)**: Dense, clean file tree with single-child folder compacting, `.gitignore` dimming, expand/collapse-all buttons, tab shortcuts (`Alt+1..9`, `Ctrl+Tab`, `Alt+W`), and right-click tab close actions.

---

## 7. Performance & Remote Workspaces

Engineered from the ground up for instantaneous response times and zero-friction remote usage:

- **[Editor Virtualization & Memory Scavenging](editor-virtualization.md)**: Handles 500,000-line files effortlessly by rendering only ~60 visible rows in the browser DOM. Automatically releases memory back to the operating system after 15 seconds of inactivity.
- **[Remote Workspaces & Cloud Inspection](remote-workspaces.md)**: Run px0 on remote servers, cloud VMs, Docker containers, or CI runners and view code in your local browser over Tailscale or private networks without SSH keys, port forwarding setups, or remote desktop daemons.
