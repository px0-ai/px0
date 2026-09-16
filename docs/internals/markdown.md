# Markdown Preview Implementation

This document describes how px0 renders Markdown files: the server-side conversion, the browser-side sanitizer, and the logic that keeps the rendered view and the source view in step.

A Markdown tab (`.md` or `.markdown`) opens rendered by default. The reader switches to the raw source with the Preview / Source switch in the tab bar, the Preview button in the status bar, or Alt+M. The code lives in two files: [`markdown.go`](../../markdown.go) on the server and [`web/src/markdown.js`](../../web/src/markdown.js) in the browser.

## 1. Request Flow

```text
openFile(path)
  |
  |  GET /api/file            -> { lines, total, ..., markdown: true }
  v
tab doc { markdown: true }  -- syncPreview() --> #mdview shown over #viewport
  |
  |  GET /api/markdown        -> { path, html }   (goldmark, raw HTML kept)
  v
mdSanitize(html)            -- inert DOMParser document, allowlist
  |
  v
mdEnhance()                 -- alerts, code block wrappers, copy buttons
  |
  v
#md article                 -- scroll restored or line / anchor applied
```

The source view is not replaced. `#viewport` keeps loading and painting its rows underneath `#mdview`, so switching to the source costs one `hidden` toggle.

## 2. Opening a Markdown Tab

`handleFile` in [`server.go`](../../server.go) adds `markdown: isMarkdown(rel)` to every `/api/file` response. `openFile` in [`web/src/tabs.js`](../../web/src/tabs.js) copies it onto the tab's doc object. Tab state then flows through one function:

- `previewing(d)` returns true when the doc is Markdown, the global preference `S.mdPreview` is on, and the doc has no `mdError`.
- `syncPreview()` compares the active doc against the doc currently shown. When they differ, it saves the outgoing doc's `mdScroll`, clears `#md`, toggles `#mdview`, and starts `drawPreview` for the incoming doc. `openFile`, `switchTab` and `closeTab` call it every time the active tab changes.
- `drawPreview(d)` fetches `/api/markdown` once per tab and caches the response string in `d.mdHtml`. It re-sanitizes that string on every draw, which avoids serializing and re-parsing cleaned DOM.

A generation counter (`mdGen`) discards a draw whose fetch finishes after the reader has moved to another tab. A shared promise (`d.mdReq`) stops a quick switch away and back from issuing a second request.

When the fetch fails (for example a file over 4 MB), `drawPreview` stores the message in `d.mdError`, shows a toast, and calls `syncPreview()` again. Because `previewing(d)` is now false, the tab falls back to its source. Choosing Preview again clears `mdError` and retries.

The preference persists in `localStorage` under `px0.mdPreview` and is restored in `boot()` in [`web/src/main.js`](../../web/src/main.js).

## 3. Server Rendering
### Converter Configuration

`mdConverter` is a single goldmark instance built at package init:

```go
var mdConverter = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
		parser.WithASTTransformers(util.Prioritized(lineMarker{}, 100)),
	),
	goldmark.WithRendererOptions(
		html.WithUnsafe(),
		renderer.WithNodeRenderers(util.Prioritized(fenceRenderer{}, 100)),
	),
)
```

- `extension.GFM` adds tables, task lists, strikethrough and autolinks. `extension.Footnote` adds footnotes.
- `html.WithUnsafe()` passes raw HTML through. READMEs depend on it for centred logos and `<details>`. Section 4 explains why this is safe.
- goldmark sorts node renderers by ascending priority and registers them in reverse, so the lowest number wins. `fenceRenderer` at 100 replaces the default HTML renderer's code block functions, which sit at 1000.

`renderMarkdown` normalizes CRLF to LF, then converts with a fresh `headingIDs` table in the parser context. The table must be per-request because it tracks which ids are already taken.

### Source Line Anchors

`lineMarker` is an AST transformer. It builds a sorted slice of newline byte offsets once, then walks the document and sets `data-line` on these node kinds: `Heading`, `Paragraph`, `List`, `ListItem`, `Blockquote`, `FencedCodeBlock`, `CodeBlock` and `Table`.

Container nodes such as lists and blockquotes own no source lines. `blockStart` descends through first children until it finds a block with lines and returns that segment's start offset. The line number is the count of newlines before that offset, plus one:

$$
\text{line} = \left|\{\, i : \text{nl}_i < \text{off} \,\}\right| + 1
$$

`sort.SearchInts(newlines, off)` returns that count in $O(\log n)$.

goldmark's HTML renderer writes node attributes for headings, paragraphs, lists, list items, blockquotes and tables, and `RenderAttributes` lets any `data-` attribute through its filters. `fenceRenderer` writes `data-line` on `<pre>` itself. A fenced block's line is its first content line, one below the opening fence.

### Heading IDs

goldmark's default id generator drops every non-ASCII character and turns `_` into `-`, which breaks tables of contents written for GitHub. `headingIDs` implements `parser.IDs` with GitHub's rules: lowercase, keep Unicode letters and digits, keep `-` and `_`, turn spaces into `-`, drop everything else, and suffix repeats with `-1`, `-2`.

| Heading text        | Id                   |
| ------------------- | -------------------- |
| `Getting Started!`  | `getting-started`    |
| `snake_case API`    | `snake_case-api`     |
| `Überblick`         | `überblick`          |
| `Getting Started`   | `getting-started-1`  |
| `???`               | `section`            |

### Fenced Code Highlighting

`renderFence` emits:

```html
<pre class="md-code" data-line="8" data-lang="go"><code><i class=k>func</i> <i class=nf>main</i>...</code></pre>
```

`highlightFence` looks up the fence's language with `lexers.Get` and calls `highlightLines`, the function the code view uses in [`highlight.go`](../../highlight.go). `Doc.tokenise` is now a thin wrapper around it. Both views therefore share lexers, the `classFor` token mapping, and the panic recovery that falls back to plain text. A theme colours both through the same CSS rules (`.c .k, .md-code .k`).

Two cases stay plain and HTML-escaped:

- Fences with no language. Guessing a language from content with `lexers.Analyse` is slow and often wrong.
- Fences over `maxFenceBytes` (256 KB).

### Endpoint

`GET /api/markdown?path=<path>` resolves the path with `resolvePath`, the same rule `/api/file` uses: inside the workspace, or an absolute path a language server has named. It reads the whole file and returns `{ "path": ..., "html": ... }`. It holds no cache. Every error comes back as JSON through `fail`:

| Status | Cause                                        |
| ------ | -------------------------------------------- |
| 400    | Path escapes the workspace                   |
| 415    | Not `.md` or `.markdown`, or a directory     |
| 404    | File missing or unreadable                   |
| 413    | Larger than `maxMarkdownBytes` (4 MB)        |
| 500    | goldmark returned an error                   |

## 4. Sanitization
### Why the Browser Treats the HTML as Untrusted

The preview renders on px0's own origin. That origin also serves `/api/lsp/install` and `/api/lsp/start`, which accept a POST whose `Origin` matches the host. Script injected into the preview would pass that check. A Markdown file in any repository the reader opens is attacker-controlled input, so every byte from `/api/markdown` goes through `mdSanitize` before it touches the page.

### Pipeline

`mdSanitize(html, docPath)` works in five steps:

1. Parse the HTML with `new DOMParser().parseFromString(html, 'text/html')`. A DOMParser document has no browsing context: it runs no script and loads no images.
1. Walk a static snapshot of `body.querySelectorAll('*')`, skipping elements already detached with a removed ancestor.
1. Remove, with all content, any element outside the HTML namespace or in `MD_DROP`: `script style iframe frame frameset object embed applet template noscript noembed svg math form textarea select option button link meta base title audio video source track canvas dialog`.
1. Unwrap any element not in `MD_KEEP`, keeping its children in place. An `<input>` survives only as `type="checkbox"` and is forced `disabled`.
1. Strip every attribute from kept elements, then restore only those that follow these rules:
  - Attributes in `MD_ATTRS`: `align valign alt title lang dir width height colspan rowspan start reversed open checked disabled type data-line data-lang`.
  - `id`, and `name` on `<a>`, rewritten as `id="md-<value>"`. A heading called "Status" becomes `md-status` and cannot shadow the status bar's `#status`.
  - Class tokens only when they are `md-code`, start with `footnote`, or are highlighter tokens on `<i>`. Content cannot borrow px0's layout classes such as `row`.
  - `src` and `href` through the URL rules below.

The cleaned children move into a `DocumentFragment` with `document.adoptNode`. The cleaned tree is never serialized and re-parsed, which rules out mutation XSS from parser round trips. `style` attributes never survive, so content cannot position an overlay over the UI.

### URL Rules

`mdURL` first removes tabs and newlines anywhere and control characters at either end, because the URL parser ignores them. Without this, `java&#9;script:` would slip past a scheme check.

| Reference                                   | `<a href>`                                                      | `<img src>`                        |
| ------------------------------------------- | --------------------------------------------------------------- | ---------------------------------- |
| `#section`                                  | Kept, `data-anchor="section"`                                   | n/a                                |
| `http:`, `https:`, `//host`                 | Kept, `target="_blank" rel="noopener noreferrer"`               | Kept                               |
| `mailto:`                                   | Kept, new tab                                                   | Dropped                            |
| `data:image/...`                            | Dropped                                                         | Kept                               |
| Any other scheme (`javascript:`, `file:`)   | Dropped                                                         | Dropped                            |
| No scheme (`docs/a.md#x`, `../img.png`)     | `/api/raw?path=<resolved>` plus `data-path` and `data-anchor`   | `/api/raw?path=<resolved>`         |

`mdLocal` resolves a scheme-less reference with `new URL(ref, base)`, where `base` is the stand-in origin `http://px0.invalid/` plus the Markdown file's directory, each segment percent-encoded. A leading `/` resolves to the workspace root, as on GitHub. If the result lands on any other origin, the reference was not relative after all and gets no URL.

### Hostile Input Examples

These cases come from the browser checks run against the implementation:

| Input                                              | Result                                        |
| -------------------------------------------------- | --------------------------------------------- |
| `<script>window.__xss = 1</script>`                | Removed with its content                      |
| `<img src="x" onerror="...">`                      | `onerror` stripped, `src` becomes a raw path  |
| `<a href="javascript:...">`                        | Text kept, no `href`                          |
| `<a href="java&#x09;script:...">`                  | Text kept, no `href`                          |
| `<svg onload="...">`                               | Removed                                       |
| `<style>body { display: none }</style>`            | Removed                                       |
| `<div style="position:fixed;inset:0" class="row">` | Plain `<div>` with no style and no class      |
| `## Status`                                        | `<h2 id="md-status">`                         |

## 5. Presentation

`mdEnhance` runs after sanitization and adds markup that px0 itself creates:

- GitHub alerts. A blockquote whose first paragraph opens with `[!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]` or `[!CAUTION]` loses the marker, gains a `.md-alert-title` paragraph, and gets `.md-alert .md-alert-<kind>`.
- Code block wrappers. Each `<pre>` moves into `.md-pre`, which carries `data-lang` for a corner label and a copy button. The button holds an SVG icon rather than a text label, because find in the preview walks text nodes and would otherwise match the word "Copy".

Styles live under `/* ---------- markdown preview ---------- */` in [`web/style.css`](../../web/style.css). They read only existing theme tokens, so all themes work without changes. Alerts set a local `--alert` property from existing tokens: Note `--accent`, Tip `--gi`, Important `--nc`, Warning `--mark-active`, Caution `--err`. [`styling-and-themes.md`](styling-and-themes.md) lists every token the preview uses.

## 6. Keeping the Reader's Place

Navigation in px0 is line-based: the outline, go to line, search results, references, history, and `openFile(path, { line })`. All of them call `centerLine(n)` in [`web/src/tabs.js`](../../web/src/tabs.js), which hands off to `previewLine(n)` while previewing.

### Line to Block (`previewLine`)

`previewLine` picks the element with the largest `data-line` that is at most `n`, and scrolls it to `MD_GAP` (16 px) below the top edge. It picks the largest line rather than stopping at the first one past `n`, because footnote definitions render at the end of the document but can sit anywhere in the source.

If the HTML has not arrived yet, the line waits in `d.mdLine` and `drawPreview` applies it. Links to another file's heading wait the same way in `d.mdAnchor`.

### Block to Line (`previewTopLine`)

`previewTopLine` walks `[data-line]` elements in document order and returns the last one whose top edge sits within `MD_GAP + 8` px of the view's top. The tolerance has to exceed `MD_GAP`. Otherwise a heading just scrolled into place by `previewLine` would report the block above it.

### Switching Views

`togglePreview` converts the reader's position in each direction:

- Preview to source. `previewTopLine()` gives the line, then `sourceToLine(line)` scrolls the code view. The virtual scroller places rows at multiples of `LH` (20 px), but rows render at `--lh` (21 px) and grow when wrapped. `sourceToLine` therefore paints, measures where the row actually landed, and corrects `scrollTop`, up to three times.
- Source to preview. `sourceTopLine()` reads the first painted row below the viewport's top, stores it in `d.mdLine`, and `drawPreview` applies it.

## 7. Links and History

A click handler on `#md` routes plain left clicks. Modified clicks fall through to the browser, which opens the `/api/raw` href in a new tab.

- `data-anchor` without `data-path` calls `mdJump`. It pushes the current position to history, scrolls to `md-<anchor>`, and pushes the target block's line, so Alt+Left and Alt+Right move between the two.
- `data-path` calls `mdFollow`, which handles four cases:
  - A link to the current file becomes `mdJump`.
  - A folder is detected by probing `/api/tree?dir=<path>` and revealed in the explorer.
  - A `#L12` anchor opens the file at line 12.
  - Any other anchor opens the file and then scrolls to the heading, immediately if the preview is drawn or later through `d.mdAnchor`.
- A path that fails to open shows a "Cannot open" toast.

`mdFindAnchor` tries the anchor as written and lowercased, both URL-decoded, and accepts the element only if it sits inside `#md`.

## 8. Switch Controls

Three controls call `togglePreview()`:

| Control                          | Location                                     | Wiring                            |
| -------------------------------- | -------------------------------------------- | --------------------------------- |
| Preview / Source switch          | `#md-switch`, right end of the tab bar       | `initMarkdown()` in `markdown.js` |
| Preview button                   | `data-action="md-preview"` in the status bar | Footer handler in `shortcuts.js`  |
| Alt+M, "Toggle Markdown Preview" | Keyboard, command palette                    | `shortcuts.js`, `palette.js`      |

`updateStatus()` in [`web/src/status.js`](../../web/src/status.js) owns the visible state. It shows `#md-switch` and the status button only on Markdown tabs, marks the active half with `.on`, and toggles `body.md-tab`. That class pads the end of `#tabs` so the last tab can scroll clear of the absolutely positioned switch. The switch buttons cancel `mousedown`, so focus stays where it was and arrow keys keep scrolling the view.

Clicking the half that is already selected does nothing. The handler toggles only when the requested view differs from `previewing()`.

## 9. Find, Select All and Keys

The code view's features assume rows, so the preview substitutes its own versions:

- Find (Ctrl+F). `runFind` in [`web/src/find.js`](../../web/src/find.js) calls `findInPreview(q)` instead of `/api/search`. It clears earlier `mark.md-hit` elements, then reuses `markNodes` from [`web/src/renderer.js`](../../web/src/renderer.js), the same text-node walker the code view uses, case-insensitively. `S.find.preview` routes `jumpToHit` to `showPreviewHit`, which scrolls the match to the middle when it is near an edge. Minimap ticks come from each mark's offset within `#mdview`'s scroll height.
- Select all (Ctrl+A). `selectPreview()` selects the contents of `#md` natively instead of the whole-file `S.selAll` mode, so Ctrl+C copies the rendered text.
- Scrolling. `previewKey(e)` maps ArrowUp and ArrowDown (and `k` / `j`) to 48 px, PageUp and PageDown to 90% of the view, and Home and End (Cmd+Up and Cmd+Down on macOS) to the ends. `shortcuts.js` calls it before the code view's caret handling.

Hover cards, Ctrl+click definitions and the selection bar listen on `#viewport`. `#mdview` covers it, so none of them fire in the preview.

## 10. Limits and Known Gaps

- Files over 4 MB are not previewed.
- Fences over 256 KB and fences without a language are not highlighted.
- Mermaid diagrams and math render as code blocks.
- The preview does not reload when the file changes on disk. Close and reopen the tab.
- Images that load after a scroll position is restored can push content down.
- Relative images in a Markdown file outside the workspace (opened through a language server) do not load, because `/api/raw` accepts only workspace paths.
- The selection bar and right-click menu actions (Copy Ref, Copy for Agent, Edit with Agent, Find Usages) do not act on text selected in the preview. Switch to Source to edit.

## 11. Tests

[`markdown_test.go`](../../markdown_test.go) covers the server side:

- `TestMarkdownPreview` checks heading ids and `data-line`, highlighted fenced code, preserved relative links, task list checkboxes, the `markdown` flag on `/api/file`, and the 415 and 400 responses.
- `TestHeadingIDsFollowGitHub` checks the id rules and deduplication.

The sanitizer, link routing, position sync and switch run only in a browser and have no automated test in the repository.
