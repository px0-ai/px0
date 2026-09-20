# In-File Find & Caret Navigation

px0 provides precise in-file text search and caret navigation tools designed for rapid reading, auditing, and spot-checking within individual source files. Accessible via `Cmd/Ctrl+F` and `Cmd/Ctrl+G`, these controls allow you to jump between occurrences and specific lines without leaving the keyboard.

---

## Overview & Core Purpose

When reading through an implementation file or verifying modifications, developers frequently need to trace where a specific variable is declared, mutated, or passed within the current document. In-file find offers instant text highlighting, match iteration, and minimap indicators to help you locate every occurrence across small and massive files alike.

Combined with dedicated line-jumping tools (`Cmd/Ctrl+G` or direct CLI opening like `px0 file.go:42`), caret motion controls, and back/forward navigation history (`Alt+Left` / `Alt+Right`), you can navigate large files with high speed and zero disorientation.

---

## Key Capabilities

- **Active Find Bar (`Cmd/Ctrl+F`)**: Opens an unobtrusive search bar in the top-right corner of the editor.
- **Selection Pre-Seeding**: If text is selected when pressing `Cmd/Ctrl+F`, that text is automatically copied into the search box, eliminating the need to retype identifier names.
- **Instant Match Highlighting**: All matches in the active file are highlighted in real time. The active match is highlighted with a distinct high-contrast accent marker.
- **Minimap & Scrollbar Markers**: Small vertical tick marks appear along the scrollbar track indicating the positions of all matches throughout the entire document, giving you an immediate sense of occurrence distribution.
- **Regex & Case-Sensitivity Toggles**: Easily switch between literal and regular expression matching, or toggle case sensitivity to locate specific casing conventions.
- **Match Counter & Cycling**: Displays the current match index and total match count (e.g., `4 of 27`). Pressing `Enter` or `F3` advances to the next match; `Shift+Enter` or `Shift+F3` navigates backward.
- **Go to Line & Column (`Cmd/Ctrl+G`)**: A quick prompt that instantly scrolls to a specific line number (and optional column offset).
- **Navigation History Stack**: Jumps initiated by search, go-to-line, or symbol navigation push entries to a browsing history stack. Pressing `Alt+Left` returns to your previous vantage point, while `Alt+Right` steps forward.

---

## Developer Workflows & Practical Value

### Auditing Variable Lifecycles
To audit how a parameter or variable is manipulated inside a 500-line function:
1. Double-click or select the variable name.
2. Press `Cmd/Ctrl+F` (the search box opens already populated with the variable name).
3. Press `Enter` repeatedly to step through each reading and writing site in chronological order.

### Jumping Directly to Compiler or Linter Errors
When a compiler or CI runner outputs an error like `router.go:148:12: undefined identifier`, you can navigate directly there without manual scrolling:
- From the terminal: `px0 router.go:148`
- From within px0: Press `Cmd/Ctrl+G`, type `148`, and press `Enter`.

### Tracing Flow with Back / Forward History
When exploring deep nested logic, clicking definitions or jumping to line anchors moves your viewport. Pressing **`Alt+Left`** steps back through your navigation history, allowing you to trace complex logic paths and effortlessly retrace your steps.

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `Cmd/Ctrl+F` | Viewer | Open In-File Find Bar (pre-seeded with selection) |
| `Enter` / `F3` | Find Bar | Jump to Next Match |
| `Shift+Enter` / `Shift+F3` | Find Bar | Jump to Previous Match |
| `Esc` | Find Bar | Close Find Bar and return focus to caret |
| `Cmd/Ctrl+G` | Viewer | Jump to Line (`:line` or `:line:col`) |
| `Alt+Left` | Global | Navigate Back in history |
| `Alt+Right` | Global | Navigate Forward in history |
| `Home` / `End` | Viewer | Jump to Start / End of current line |
| `Ctrl+Home` / `Ctrl+End` | Viewer | Jump to Start / End of document (`Cmd+Up`/`Cmd+Down` on macOS) |
| `Alt+Z` | Viewer | Toggle soft word wrapping |
| `Alt+L` | Viewer | Toggle line numbers in gutter |

---

## Configuration & Editor Options

You can configure editor navigation behavior in Settings (`Cmd/Ctrl+,`):
- **Editor: Line Numbers** (`editor.lineNumbers`): Toggle line numbers in the gutter on or off.
- **Editor: Word Wrap** (`editor.wordWrap`): Enable soft line wrapping to prevent long lines from running off-screen.
- **Editor: Scroll Beyond Last Line** (`editor.scrollBeyondLastLine`): Allow scrolling past the final row of a document.
- **Editor: Occurrences Highlight** (`editor.occurrencesHighlight`): Automatically highlight other occurrences of the word currently under the caret without pressing `Cmd/Ctrl+F`.
