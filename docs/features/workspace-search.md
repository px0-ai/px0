# Workspace Text & Regex Search

Workspace search provides full-repository text searching across all files in your project. Accessible via `Cmd/Ctrl+Shift+F`, it allows you to query literal text phrases or complex regular expressions across millions of lines of code with instant previews.

---

## Overview & Core Purpose

When investigating an unfamiliar codebase, tracing error strings, auditing security vulnerabilities, or auditing how a configuration flag is utilized across services, searching file names alone is insufficient. Developers need to search through the entire workspace content at blazing speed.

Traditional IDEs often struggle with whole-workspace text scans, stalling the UI or spinning up CPU fans as they churn through gigabytes of code. px0 provides multi-threaded parallel grep capability that queries tens of thousands of files in tens of milliseconds without causing browser stutters or high memory consumption. Results are cleanly grouped by file in the right-hand Inspector pane, providing line numbers, occurrence counts, and contextual snippets.

---

## Key Capabilities

- **Parallel Multi-Core Grep**: Utilizes all available CPU cores on the host machine to search across repository files concurrently. Searching the entire React, Django, or Kubernetes repository takes only 25–85 milliseconds.
- **Literal & Regular Expression Support**: Supports exact string matching as well as full Go/PCRE-compatible regular expressions for matching complex code patterns, IP addresses, function declarations, and identifier conventions.
- **Smart Case Sensitivity**: When enabled (default), searching with all lowercase letters performs a case-insensitive search (e.g., `error` matches `Error`, `ERROR`, and `error`). Introducing any uppercase character (e.g., `ErrNotFound`) automatically switches to case-sensitive matching.
- **Structured File Grouping**: Matches are organized hierarchically by file path in the right Inspector panel. Clicking any file header expands or collapses its matches.
- **Contextual Snippet Elision**: Each match displays a contextual code snippet with the matching phrase highlighted. Long lines are intelligently elided around the match so you can immediately understand the surrounding code without scrolling horizontally.
- **One-Click Navigation**: Clicking any snippet immediately opens the target file in the viewer, positions the caret at the matching row, and scrolls the line into the center of the screen.
- **Per-Search Path Filters**: Narrow candidates with an include glob, then remove exact paths, directory globs, or filename globs with a comma-separated exclude field. Exclusions run before files are opened.

---

## Developer Workflows & Practical Value

### Auditing Symbol Call Sites
Before refactoring a public API or modifying an exported function, you can search for the symbol name across the repository to verify every caller, mock, and test case that references it.

### Hunting Down Error Messages
When an application logs a specific error string (such as `connection refused on port 5432` or `unexpected EOF in header`), pasting that string into workspace search instantly pinpoints the exact line where the error was thrown.

### Pattern Matching with Regular Expressions
Using regex syntax, you can audit code patterns across the entire project. For example:
- `func\s+\(.*\)\s+Handle\w+` to discover all Go HTTP handler methods.
- `TODO\(.*?\):` or `FIXME` to review pending work across teams.
- `api/v[0-9]+/` to find all versioned API endpoints.

---

## Interactive Controls & Search Pane

1. Press **`Cmd/Ctrl+Shift+F`** to reveal the right-hand Inspector and focus the search input field.
2. Enter your query or regex pattern and press `Enter`.
3. As results stream in, the Inspector header displays the total match count and the number of affected files.
4. Optionally set **files to include** (for example `*.go`) and **files to exclude** (for example `vendor/**, *.min.js`). Include narrows the candidate set first; exclude removes paths from that set.
5. Click any result snippet to open the corresponding file at that line.
6. Use the collapsible chevron next to each file header to collapse files you have already reviewed.
7. Press `Esc` or click the close button to dismiss the Inspector panel.

---

## Configuration & Tuning

Workspace search behavior can be customized in Settings (`Cmd/Ctrl+,`):

- **Search: Smart Case** (`search.smartCase`): Toggle automatic smart casing (defaults to `true`).
- **Search: Max Results** (`search.maxResults`): Adjust the maximum number of results returned (defaults to `1000`, configurable from `50` to `10,000`).
- **Files: Exclude** (`files.exclude`): Exclude specific directories, build outputs, or generated bundles from search results using standard glob syntax.

---

## Technical Architecture Deep Dive

For details on the worker pool architecture, buffer reuse mechanisms, whole-file pre-filtering, and snippet elision algorithms, see [Workspace Search & Parallel Grep Internals](../internals/workspace-search.md).
