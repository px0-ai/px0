# Symbol Outline & Navigation

The symbol outline provides a structural overview of all declarations within the active file. Accessible via `Cmd/Ctrl+Shift+O` or through the right Inspector pane, it allows you to quickly locate and jump between functions, methods, classes, interfaces, structs, and constants.

---

## Overview & Core Purpose

Navigating long source files (such as complex controllers, database models, or parser files containing hundreds or thousands of lines) can be tedious when relying on manual scrolling. Developers often know the name of the function or type they want to review, but not its line number.

The symbol outline extracts every structural symbol from the current document into a searchable, categorized list. By pressing `Cmd/Ctrl+Shift+O`, you can immediately filter symbols as you type and jump directly to any definition without losing mental context.

---

## Key Capabilities

- **Instant Structural Tree**: Automatically extracts functions, methods, classes, structs, interfaces, enums, type definitions, and package-level constants.
- **Dual-Engine Architecture**:
  - **LSP Semantic Outline**: When a Language Server is detected (such as `gopls`, `rust-analyzer`, or `pyright`), px0 pulls high-fidelity semantic document symbols with precise symbol kinds and nested container hierarchies.
  - **Zero-Config Regex Fallback**: When no language server is running or installed, px0 immediately falls back to high-speed built-in regex parsers across Go, Rust, TypeScript, JavaScript, Python, C/C++, Java, Ruby, and other languages, ensuring outline navigation is always available.
- **Fuzzy Symbol Filtering**: Type characters in the filter input to narrow down the list. Matches are ranked and highlighted in real time.
- **Visual Symbol Badges**: Each entry displays an informative badge indicating its kind (e.g., `func`, `struct`, `interface`, `class`, `const`, `var`), allowing you to distinguish methods from types at a glance.
- **Two Presentation Modes**:
  - **Quick Outline Modal (`Cmd/Ctrl+Shift+O`)**: A fast centered popover for rapid search and keyboard-driven jumping.
  - **Inspector Symbols Tab**: A persistent right-hand sidebar tab allowing you to browse the file structure while reviewing code side-by-side.

---

## Developer Workflows & Practical Value

### Understanding New Files Quickly
When reviewing a file created or modified by an AI coding agent, opening the symbol outline gives you an instant architectural summary of all newly defined methods, data structures, and exported types before diving into implementation details.

### Rapid In-File Navigation
Instead of scrolling up and down through a 2,000-line source file, pressing `Cmd/Ctrl+Shift+O` and typing `Init` or `Handle` immediately takes you to the initialization routine or HTTP handler.

### Keyboard Workflow
1. Open any source file.
2. Press **`Cmd/Ctrl+Shift+O`**.
3. Type the symbol name or abbreviation (e.g., `parse`).
4. Use `Up` and `Down` arrow keys to highlight the desired symbol.
5. Press `Enter` to jump to the declaration and center the line in the viewer.

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `Cmd/Ctrl+Shift+O` | Editor | Open In-File Symbol Outline Modal |
| `@` (First character) | `Cmd/Ctrl+P` | Switch Quick Open into Symbol mode |
| `Enter` | Outline | Jump to selected symbol and dismiss |
| `Esc` | Outline | Close outline and restore editor focus |

---

## Technical Architecture Deep Dive

For technical details regarding the LSP document symbol extraction protocol and regex tokenizing fallbacks, see [Language Server Protocol Architecture](../internals/lsp-and-intelligence.md) and [Workspace Search & Symbol Extraction](../internals/workspace-search.md).
