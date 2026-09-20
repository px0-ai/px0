# Styling & Theme Architecture

This document describes px0's styling architecture, CSS custom property design system, dynamic theme loading pipeline, and token specifications.

px0 reads every colour in the user interface through a CSS custom property, known as a token. A theme is defined in a single CSS file that assigns values to these tokens.

## 1. How Themes Load

- `web/style.css`: Holds layout, grid geometry, and component styling. It contains no literal colors. Its `:root` block defines structural typography tokens (fonts, sizes) and provides fallbacks for optional colour tokens.
- `web/themes/<id>.css`: Each theme resides in its own file under [`web/themes/`](../../web/themes/), containing a single rule for `:root[data-theme="<id>"]`. The filename without extension acts as the theme ID.
- Dynamic Concatenation (`/static/themes.css`): The Go server concatenates every file matching `web/themes/*.css` in alphanumeric order and serves the result dynamically at `/static/themes.css`. [`web/index.html`](../../web/index.html) links this file immediately after `style.css`.
- Client-Side Discovery: At application boot, [`web/src/theme.js`](../../web/src/theme.js) scans the loaded document stylesheets for rules matching `:root[data-theme="<id>"]`. It extracts the human-readable display name from `--theme-name` and the color scheme hint from `color-scheme`.
- State Persistence: The active theme is applied via the `data-theme` attribute on the `<html>` root element and persisted in `localStorage` under `px0.theme`. If a saved theme is removed, px0 falls back to the default `github-dark`.

> [!NOTE]
> Theme rules intentionally use `:root[data-theme="<id>"]` rather than a bare attribute selector `[data-theme="<id>"]`. The `:root` pseudo-class raises CSS specificity above the fallback rules in `style.css`, ensuring theme tokens always win regardless of stylesheet evaluation order.

## 2. Built-in Themes

| Name             | ID                 | Scheme | Inspiration / Palette                  |
| ---------------- | ------------------ | ------ | -------------------------------------- |
| Catppuccin Latte | `catppuccin-latte` | light  | Catppuccin palette (contrast-tuned)    |
| Catppuccin Mocha | `catppuccin-mocha` | dark   | Catppuccin palette                     |
| Dracula          | `dracula`          | dark   | Classic Dracula palette                |
| GitHub Dark      | `github-dark`      | dark   | GitHub dark default (default px0 theme)|
| Gruvbox Dark     | `gruvbox-dark`     | dark   | Gruvbox dark retro groove              |
| Gruvbox Light    | `gruvbox-light`    | light  | Gruvbox light                          |
| Monokai          | `monokai`          | dark   | Classic Monokai high-contrast          |
| Nord             | `nord`             | dark   | Arctic Nord palette                    |
| One Dark         | `one-dark`         | dark   | Atom One Dark                          |
| Paper            | `light`            | light  | Minimalist GitHub light                |
| Rose Pine        | `rose-pine`        | dark   | Rose Pine Soho vibes                   |
| Solarized Dark   | `solarized-dark`   | dark   | Canonical Ethan Schoonover Solarized   |
| Solarized Light  | `solarized-light`  | light  | Canonical Solarized light              |
| Tokyo Night      | `dark`             | dark   | Tokyo Night deep blue                  |

## 3. Creating a Custom Theme

1. Copy an existing theme file:
  ```bash
  cp web/themes/dark.css web/themes/midnight.css
  ```
1. Update the selector to match the new ID:
  ```css
  :root[data-theme="midnight"] {
  ```
1. Set the metadata properties:
  ```css
  --theme-name: "Midnight";
  color-scheme: dark;
  ```
1. Define the required color tokens (see Token Reference below).
1. Preview live without recompiling Go code by running px0 in dev mode:
  ```bash
  go run . -dev . .
  ```
1. Run unit tests to verify theme conformance:
  ```bash
  go test ./...
  ```
  `TestThemesStylesheetJoinsEveryThemeFile` in [`px0_test.go`](../../px0_test.go) ensures all required tokens are present and selector IDs match filenames.

### Minimal Working Theme Example

```css
:root[data-theme="midnight"] {
  --theme-name: "Midnight";
  color-scheme: dark;

  --bg: #002b36; --bg2: #00252e; --bg3: #073642; --bg4: #0d4452;
  --fg: #93a1a1; --dim: #839496; --faint: #586e75;
  --line: #0a3b47; --accent: #268bd2; --accent-fg: #2aa198;
  --sel: #0f4b5c; --mark: #4a3f0b; --mark-active: #7a4a12; --cur: #04313c;
  --shadow: 0 16px 48px rgba(0, 0, 0, 0.6);

  --k: #859900; --nf: #268bd2; --s: #2aa198; --m: #d33682; --c: #586e75; --err: #dc322f;
}
```

## 4. Complete Token Reference
### Surface Elevation Tokens

Four progressive elevation steps radiating from the editor viewport outward:

| Token  | Required | Controls                                                                                                                |
| ------ | -------- | ----------------------------------------------------------------------------------------------------------------------- |
| `--bg` | Yes      | Editor background, line gutter, active tab, text inputs, hovercard signature block, Markdown preview background.        |
| `--bg2`| Yes      | File explorer sidebar, tab bar, right inspector, status bar, hovercard action row, Markdown code blocks and table headers.|
| `--bg3`| Yes      | Floating surfaces (hovercard, findbar, command palette, shortcut cheatsheet, toast), row hover state, keycaps, badge fills.|
| `--bg4`| Yes      | Small button hovers (close tab, status bar icons), active status buttons, double-click occurrence highlight, scrollbar thumb.|

### Text & Contrast Tokens

| Token              | Required | Fallback      | Controls                                                                                                              |
| ------------------ | -------- | ------------- | --------------------------------------------------------------------------------------------------------------------- |
| `--fg`             | Yes      | -             | Primary text, active file tab, current line number, unstyled identifiers.                                             |
| `--dim`            | Yes      | -             | Secondary text: inactive tabs, explorer file names, hovercard docs, Markdown blockquotes and footnotes.              |
| `--faint`          | Yes      | -             | Line numbers, keyboard hints, tree chevrons, close buttons at rest, inactive LSP indicator.                         |
| `--on-accent`      | No       | `#fff`        | Text rendered on top of an `--accent` background.                                                                     |
| `--on-badge`       | No       | `var(--bg)`   | Text rendered inside colored badges (symbol kinds, file extensions).                                                  |
| `--on-mark-active` | No       | `var(--bg)`   | Text of current search/find hit on top of `--mark-active`.                                                             |

### Borders, Accents & Selection

| Token            | Required | Controls                                                                                                |
| ---------------- | -------- | ------------------------------------------------------------------------------------------------------- |
| `--line`         | Yes      | Dividers, pane borders, occurrence outlines, Markdown table borders, code block borders.                |
| `--accent`       | Yes      | Active tab top indicator, input focus borders, resizer drag handle, Ctrl+hover link underlines, button hovers.|
| `--accent-fg`    | Yes      | Accent-colored text: active inspector tab, palette match characters, Markdown links, status button hovers.|
| `--sel`          | Yes      | Selected tree row, palette row, native text selection, whole-file selection (Ctrl+A).                   |
| `--mark`         | Yes      | Background of search and find matches.                                                                  |
| `--mark-active`  | Yes      | Active find match, minimap match indicators, pulsing LSP status dot.                                   |
| `--cur`          | Yes      | Active line background and current gutter cell highlight.                                               |
| `--shadow`       | Yes      | Elevation box-shadow for floating overlays (palette, hovercard, findbar).                                |

### Syntax Highlighting Tokens

Generated by Chroma and formatted using short CSS classes ([`highlight.go`](../../highlight.go)):

| Token   | Class  | Semantic Role                           | Required | Fallback   |
| ------- | ------ | --------------------------------------- | -------- | ---------- |
| `--k`   | `.k`   | Keywords (`func`, `return`, `class`)    | Yes      | -          |
| `--kt`  | `.kt`  | Primitive types (`int`, `string`, `bool`)| No      | `var(--k)` |
| `--nf`  | `.nf`  | Function names and method calls         | Yes      | -          |
| `--nc`  | `.nc`  | Classes, structs, interfaces            | No       | `var(--nf)`|
| `--nb`  | `.nb`  | Built-in functions and standard types   | No       | `var(--nf)`|
| `--nv`  | `.nv`  | Variables and parameters                | No       | `var(--fg)`|
| `--no`  | `.no`  | Constants                               | No       | `var(--m)` |
| `--s`   | `.s`   | String literals and characters          | Yes      | -          |
| `--m`   | `.m`   | Numeric literals                        | Yes      | -          |
| `--o`   | `.o`   | Operators (`+`, `-`, `*`, `&&`)         | No       | `var(--fg)`|
| `--p`   | `.p`   | Punctuation (braces, parentheses, commas)| No      | `var(--dim)`|
| `--c`   | `.c`   | Comments (italicized)                   | Yes      | -          |
| `--err` | `.err` | Syntax errors (wavy underline)          | Yes      | -          |
| `--gi`  | `.gi`  | Git added lines, ready LSP dot          | No       | `var(--s)` |
| `--gd`  | `.gd`  | Git deleted lines, failed LSP dot       | No       | `var(--err)`|
