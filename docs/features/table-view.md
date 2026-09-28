# CSV & TSV Table View

px0 opens `.csv` and `.tsv` files as a table by default. `Alt+M` switches between the table and the raw source, and your reading position carries across, as it does for the Markdown preview.

---

## Overview & Core Purpose

Data files turn up in most repositories: fixtures, benchmark results, exported reports, seed data, and files a coding agent has just written. Read as raw text, the columns drift apart as soon as values differ in width, and a quoted cell containing a comma or a line break is hard to follow by eye.

The table view lines the columns up, keeps the header row in sight while you scroll, and numbers each row by the source line it starts on, so line references still mean the same thing in both views.

---

## Key Capabilities

- **Correct CSV parsing**: Quoted cells can hold commas, quotes and line breaks. Stray quotes in unquoted cells are accepted as literal text instead of failing the file.
- **Delimiter from the file**: px0 reads the first line and splits on whichever of comma, tab, semicolon or pipe it uses most, ignoring any inside quotes. A tab-separated file named `.csv` or a semicolon export from a European spreadsheet still gets proper columns. The extension only breaks ties (`.csv` → comma, `.tsv` → tab).
- **Long text wraps**: A cell is at most about 48 characters wide. Longer text wraps inside the cell and the row grows taller, so one long note doesn't push the other columns off screen.
- **Sticky header row**: The first row is treated as the header and stays pinned while the table scrolls. A UTF-8 byte order mark at the start of the file is dropped.
- **Line-number gutter**: The left column shows each row's source line. A row whose quoted cell spans two lines makes the numbering skip (7, then 9), which is what keeps `Cmd/Ctrl+G`, `#L42` links and the source toggle lined up. The gutter sticks while you scroll sideways and is left out when you copy.
- **Uneven rows**: A row with fewer cells than the header shows hatched empty cells. A row with more cells widens the table, and the extra cells sit under blank headings.
- **Plain text only**: Cell contents are always shown as text. Markup in a cell such as `<b>` appears as typed.
- **Large files**: The table shows the first 1,000 rows (or the first 1 MB). A footer below the table says how many rows it shows and how many lines the file has, with an **Open Source** button for the rest. Opening a 50 MB file takes the same time as opening one at the cap.
- **Its own setting**: Whether data files open as a table is remembered separately from the Markdown preview (`table.preview.open`, on by default). Toggling one never changes the other.

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `Alt+M` | CSV / TSV Tab | Toggle between Table and Source |
| `#md-switch` | Tab Bar Header | Click "Table" or "Source" |
| Status Bar | Footer Button | Click "Table" to toggle |
| `Cmd/Ctrl+F` | In Table | Find text in the cells |
| `Cmd/Ctrl+G` | In Table | Scroll to the row holding that source line |
| `Cmd/Ctrl+A` | In Table | Select the table's text |
| `PageUp` / `PageDown`, `j` / `k` | In Table | Scroll the table |

---

## Technical Architecture Deep Dive

The table shares the Markdown preview's overlay, switch, find and position code. For the endpoint, the caps and how rows map to source lines, see [Table Preview](../internals/markdown.md#12-table-preview-csv--tsv) in the preview internals.
