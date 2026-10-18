# Direct File Editing & Creation

Implementation notes for in-browser editing. User-facing behavior is in [Direct File Editing](../features/file-editing.md).

## 1. Endpoints

Both are `POST` with a JSON body and are resolved through `safePath`, so paths outside the workspace are rejected.

| Endpoint | Body | Behavior |
| :--- | :--- | :--- |
| `/api/file/save` | `{path, content}` | Creates missing parent directories, refuses a directory target, writes the file (`0644`). Then `Evict` drops the highlight cache entry, the LSP document is closed and open documents refreshed, the git watcher is triggered, and the index rebuilds in the background. Returns `{ok, path, size}`. |
| `/api/fs/new` | `{path, type}` | `type` is `file` or `dir`. Returns `409` if the path exists. Creates parents, writes an empty file or the directory, triggers the git watcher, rebuilds the index. Returns `{ok, path, type}`. |

Handlers are `handleFileSave` and `handleFSNew` in [`server.go`](../../server.go); tests are in [`px0_test.go`](../../px0_test.go) (save, creation, conflict, traversal).

## 2. Frontend

[`web/src/editview.js`](../../web/src/editview.js) owns the edit surface: a `<textarea id="editview-textarea">` and a gutter, inside `#editview`, which sits over the virtualized viewport (`vp` is hidden while editing).

- **State** lives on the tab doc: `isEditing`, `dirty`, `editContent` (the working text), `editScrollTop`.
- **Text swap.** `loadedDoc` tracks whose text is in the textarea. `syncEditUI()` calls `loadEditContent()` whenever the active doc is editing, which stores the outgoing doc's text, loads the incoming doc's `editContent`, and restores its scroll. Every path that changes the active tab (`openFile`, `switchTab`, `closeTab`) calls `syncEditUI()`, so none of them needs to copy text itself.
- **Diff requests.** `openFile` with `view: 'diff'` on an editing doc clears `isEditing` first, so the diff view is not covered by the editor. `editContent` is kept.
- **Gutter.** Without wrap, line numbers are one text node. With wrap (`body.word-wrap`), `updateGutter()` lays each line out in a hidden mirror element (`#editview-mirror`, same font, padding, and width as the textarea) and gives the matching gutter number that line's measured height. It reruns on input, on textarea resize (`ResizeObserver`), and on wrap toggle (`MutationObserver` on the body class).
- **Keys.** Tab, Enter auto-indent, `Mod+S`, and `Esc` are handled on the textarea; edits go through `document.execCommand('insertText')` so the browser's native undo stack is kept.
- **After save.** `saveActiveFile()` clears `dirty`, updates `size`/`total`, reloads the diff gutter, and calls `triggerRefresh()` ([`gitstream.js`](../../web/src/gitstream.js)) so the git panel does not wait for the next stream tick. [`newitem.js`](../../web/src/newitem.js) does the same after creating an item.

`Mod+E` and `Mod+S` are registered in [`shortcuts.js`](../../web/src/shortcuts.js); the palette commands are in [`palette.js`](../../web/src/palette.js). Tab close and `beforeunload` guards for dirty tabs are in [`tabs.js`](../../web/src/tabs.js).
