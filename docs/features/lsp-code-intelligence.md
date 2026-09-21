# Language Server Protocol (LSP) & Code Intelligence

px0 includes an integrated Language Server Protocol (LSP) client that brings IDE-grade semantic code intelligence to your browser. It provides instant Go to Definition (`F12`), Find References (`Shift+F12`), interactive Call Trails (`Alt+Shift+H`), and rich Hover documentation cards.

---

## Overview & Core Purpose

Navigating unfamiliar codebases often requires tracing types, following abstraction layers, and exploring how classes and functions interconnect. Pure text searching or regex heuristics can struggle when symbols share common names across different modules or packages.

px0 connects to official language servers already installed on your system (such as `gopls`, `rust-analyzer`, `typescript-language-server`, `pyright`, and `clangd`). It operates without background indexing drag: language servers are spawned lazily on-demand upon your first semantic action. If no language server is installed, px0 automatically falls back to high-speed regex-based outlines and text searches without breaking your flow.

---

## Key Capabilities

- **Go to Definition (`F12`, `Cmd/Ctrl+Click`)**: Jump directly to where any function, struct, interface, type, or variable is defined. If the definition lives in a different file or an external standard library module, px0 opens the file seamlessly and positions the cursor at the declaration line.
- **Find All References (`Shift+F12`, `Alt+U`)**: Discover every location across the workspace where a symbol is referenced, called, or implemented. Results are organized by file in the right-hand Inspector pane, complete with line numbers and preview snippets.
- **Interactive Call Trails (`Alt+Shift+H`)**: Explore bidirectional call hierarchies for any function or method:
  - **Incoming Calls (Callers)**: See every function that calls the selected routine, expandable level-by-level into an interactive call tree.
  - **Outgoing Calls (Callees)**: See all functions invoked by the routine.
- **Hover Documentation Cards**: Hovering your pointer over any identifier displays its type signature, return types, package path, and rendered docstring comments.
- **External Standard Library Navigation**: When jumping to definitions in standard libraries (such as Go's `net/http` or Rust's `std::sync`), px0 opens external read-only tabs so you can inspect standard library internals without cloning their sources.
- **Zero-Config Discovery & In-App Setup**: Automatically detects language servers in your `PATH`. If a server is missing, clicking **LSP: set up** in the status bar reveals one-click installation recipes tailored to your operating system.

---

## Supported Language Servers

px0 natively detects and communicates with standard language servers across major programming languages:

| Language | Detected Binary | Installation Command |
| :--- | :--- | :--- |
| **Go** | `gopls` | `go install golang.org/x/tools/gopls@latest` |
| **Rust** | `rust-analyzer` | `rustup component add rust-analyzer` |
| **TypeScript / JavaScript** | `typescript-language-server` | `npm install -g typescript-language-server typescript` |
| **Python** | `pyright` / `pylsp` / `ruff` | `npm install -g pyright` or `pipx install python-lsp-server` |
| **C / C++** | `clangd` | `sudo apt install clangd` or `brew install llvm` |
| **Zig** | `zls` | `brew install zls` or download from [zigtools/zls](https://github.com/zigtools/zls) |
| **Lua** | `lua-language-server` | `brew install lua-language-server` |
| **Ruby** | `solargraph` | `gem install solargraph` |
| **Java** | `jdtls` | `brew install jdtls` |
| **C#** | `omnisharp` | Install OmniSharp on `PATH` |
| **LaTeX** | `texlab` | `brew install texlab` |

---

## Developer Workflows & Practical Value

### Semantic Auditing & Refactoring Verification
When verifying changes made by a coding agent, you often need to confirm that function argument changes propagate correctly. Using `Shift+F12` on the function signature lists every call site across the repository so you can verify each caller at a glance.

### Deep Architectural Exploration via Call Trails
To understand how an unfamiliar subsystem processes requests:
1. Place your caret inside the core request handling function.
2. Press **`Alt+Shift+H`** to open the Call Trail in the right Inspector.
3. Expand incoming callers level-by-level to trace the entry point from CLI arguments or HTTP routes down to low-level storage engines.

### Instant Context via Hover Cards
Instead of navigating away from your current code to check parameter types or struct fields, simply hover your mouse over the identifier to read its full type signature and documentation comments.

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `F12` / `Cmd+Click` | Identifier | Go to Definition |
| `Shift+F12` | Identifier | Find All References (opens Inspector) |
| `Alt+Shift+H` | Function/Method | Open Call Trail (Call Hierarchy) |
| `Hover` | Identifier | Show Type Signature & Documentation Card |
| `Alt+U` | Selected Code | Find usages of selected symbol |
| `Alt+Left` / `Alt+Right` | Global | Return back to origin / step forward in navigation history |

---

## Configuration & Options

LSP features can be toggled in Settings (`Cmd/Ctrl+,`):

- **LSP: Enabled** (`lsp.enabled`): Master toggle for language server discovery and background communication (defaults to `true`).
- **LSP: Hover Enabled** (`lsp.hover.enabled`): Enable or disable hover documentation cards (defaults to `true`).
- **CLI Flag `-no-lsp`**: Run px0 with LSP completely disabled (`px0 -no-lsp`), relying exclusively on fast regex extraction.

---

## Technical Architecture Deep Dive

For details regarding JSON-RPC 2.0 framing, lazy on-demand server lifecycle management, memory sandboxing, and fallback pipelines, see [Language Server Protocol Architecture Internals](../internals/lsp-and-intelligence.md).
