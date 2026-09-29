# File Explorer & Workspace Management

px0 includes a high-density file tree sidebar and multi-tab document manager. Designed for rapid spatial orientation across large directory trees, it features compact directory collapsing, Git status badging, automatic file revelation, and keyboard tab management.

---

## Overview & Core Purpose

Navigating deeply nested package structures (such as `src/main/java/com/company/project/controllers/...` or deeply nested monorepo packages) often clutters file sidebars with endless single-child folders, forcing developers to click repeatedly just to reach a single source file. Furthermore, cluttered sidebars make it difficult to distinguish between active project files and ignored artifacts (such as build outputs, test coverage dumps, or `node_modules`).

px0's file explorer eliminates unnecessary clicks by automatically collapsing single-child directory chains into a single line (e.g., `src/core/auth/`). It honors your `.gitignore` configuration automatically—dimming ignored files and omitting them from searches while keeping the tree clean. Paired with a versatile tab bar, you can manage multiple documents and switch contexts effortlessly.

---

## Key Capabilities

- **Compact Single-Child Folder Chains**: When a directory contains only a single subdirectory (common in Java, Go, and modular TypeScript architectures), px0 collapses the entire chain into a single combined entry (`internal/service/auth`). Clicking expands the entire chain in one click.
- **Git Status Decorators**: Files with unstaged or staged modifications display color-coded status badges (`M`, `A`, `D`, `U`, `R`), with dirty status propagating up parent directory branches.
- **Gitignore Awareness & Dimming**: Files and folders ignored by `.gitignore` rules (such as `dist/`, `build/`, `vendor/`, `node_modules`) are visually dimmed in the tree and excluded from background search indexes.
- **Auto-Reveal Active File**: Opening a file via fuzzy search (`Cmd/Ctrl+P`) or Go to Definition (`F12`) automatically scrolls the sidebar tree to reveal and highlight the active file.
- **Expand All / Collapse All**: The explorer header has buttons to open project folders or close every folder. Expand All shows a progress state while loading nested folders in small batches. Ignored folders stay closed to avoid bulk-loading generated trees; you can still open them individually. Collapse All can stop an expansion while it runs. The resulting folder state is remembered between launches.
- **Collapsible Sidebar (`Cmd/Ctrl+B`)**: Quickly toggle the entire file explorer sidebar on or off to maximize reading space for wide diffs or code inspection.
- **Multi-Tab Document Bar**:
  - Open multiple files in tabs and switch between them.
  - Switch tabs using keyboard shortcuts (`Ctrl+Tab`, `Alt+1` through `Alt+9`).
  - Close active tabs with `Alt+W` (or `Cmd/Ctrl+W`).
  - Right-click a tab for **Close**, **Close All**, **Close Others**, **Close to the Right**, or **Close to the Left**. Actions without matching tabs are disabled.
  - Image tabs, Markdown previews, diff views, and external standard library files sit cleanly alongside source files.

---

## Developer Workflows & Practical Value

### Orientation in Unfamiliar Codebases
When onboarding to a new repository, scrolling through the file explorer gives an immediate spatial understanding of project boundaries, package architecture, and documentation assets without opening thousands of nested folders.

### Maximizing Screen Real Estate
On laptops or split-screen terminal workflows:
1. Press **`Cmd/Ctrl+B`** to hide the file explorer.
2. Use **`Cmd/Ctrl+P`** exclusively for keyboard-driven file jumping.
3. Enjoy an unobstructed, edge-to-edge reading viewport.

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `Cmd/Ctrl+B` | Global | Toggle File Explorer Sidebar |
| Expand All / Collapse All buttons | Explorer header | Open project folders or close every folder |
| `Alt+W` | Tab Bar | Close Active Tab |
| `Ctrl+Tab` | Tab Bar | Switch to Next Tab |
| `Alt+1` … `Alt+9` | Tab Bar | Switch to Tab by Position |
| Click Tab Close `✕` | Tab | Close Specific Tab |
| Right-click Tab | Tab Bar | Open tab close menu |

---

## Configuration & Tuning

Explorer behavior can be customized in Settings (`Cmd/Ctrl+,`):

- **Explorer: Compact Folders** (`explorer.compactFolders`): Automatically collapse single-child folder chains into single tree rows (defaults to `true`).
- **Explorer: Auto Reveal** (`explorer.autoReveal`): Automatically scroll the tree and expand parent folders to highlight the active file (defaults to `true`).
- **Files: Exclude** (`files.exclude`): Configure custom glob patterns to hide specific directories or generated files from the tree entirely.

---

## Technical Architecture Deep Dive

For details on the bounded-concurrency directory walker (`NumCPU * 4`), in-memory path index structures, and classification-based `.gitignore` engine, see [Filesystem Indexing & Ignore Engine Internals](../internals/indexing-and-ignore.md).
