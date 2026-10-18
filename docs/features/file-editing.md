# Direct File Editing & Creation

px0 can edit a file in place and create new files and folders from the browser. This is for small, direct changes. Larger or multi-file changes still go through a coding harness (see [Agent Editing](agent-editing.md)).

---

## Editing a File

- Press **`Cmd/Ctrl+E`**, click the **Edit** button in the tab header, or run **File: Edit Current File** from the Command Palette. The code view is replaced by a plain text editor with its own line-number gutter.
- Press **`Cmd/Ctrl+S`** (or the **Save** button) to write the file. The button briefly turns solid green with a check mark on success.
- Press **`Esc`**, **`Cmd/Ctrl+E`** again, or the **Done** button to leave edit mode. If there are unsaved changes you are asked before they are discarded.
- Editing is not offered for images, deleted files, or tabs pinned to a commit.
- Files over 10,000 lines or 1 MB ask for confirmation first, because the whole file is loaded into the editor.

### Editor behavior

| Key | Behavior |
| :--- | :--- |
| `Tab` | Inserts four spaces. With a multi-line selection, indents every selected line. |
| `Enter` | Keeps the indentation of the current line. |
| `Cmd/Ctrl+S` | Saves the file. |
| `Esc` | Leaves edit mode. |

- **Word wrap** follows the same setting as the code view (`editor.wordWrap`, `Alt+Z`). Long lines wrap at the editor edge and the gutter number stays aligned with the first row of each wrapped line.
- **Unsaved changes** show a dot on the tab. Closing a dirty tab or the browser window asks first.
- **Switching tabs** while editing keeps your unsaved text and scroll position per file. Opening a diff for the file you are editing leaves edit mode and shows the diff; the unsaved text is kept for when you edit again.

## Creating Files and Folders

- Use the **New File** and **New Folder** buttons in the sidebar header, right-click a folder in the tree, or run **File: New File...** / **File: New Folder...** from the Command Palette.
- Enter a path relative to the workspace (or to the folder you right-clicked). Missing parent folders are created. Creating something that already exists is refused.
- A new file opens straight into edit mode.

## Git and the Rest of the UI

Saving or creating something refreshes the git panel, tree badges, and change counts immediately. The index, LSP documents, and syntax-highlight cache for the file are refreshed too, so search, symbols, and diagnostics see the new content.
