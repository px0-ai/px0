# Image Viewer & Asset Inspection Architecture

This document describes px0's first-class image viewing architecture: how standalone image files are loaded and rendered as interactive tabs, how viewport transforms and background modes work, and how inline images in Markdown documents are enhanced with a click-to-expand lightbox and error fallbacks.

---

## 1. Overview & Motivation

Modern software repositories increasingly contain visual assets: SVG icons, UI mockups, generated architectural diagrams, charts, favicon bundles, and asset pipelines. When pairing with coding agents that generate or modify images and documentation, developers need to inspect these visual outputs immediately without switching to an external tool or desktop previewer.

px0 treats image formats (`.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.svg`, `.ico`, `.bmp`, `.avif`) as first-class documents alongside source code and Markdown.

```text
Tree / Palette Click
       │
       ▼
  openFile(path) ─── GET /api/file ───► { path, image: true, size: ... }
       │
       ├─► S.tabs.push({ isImage: true, imageScale: 1, imageFit: true, ... })
       │
       ▼
  syncImageView()
       │
       ├─► Hide #viewport, #mdview, #diffview
       ├─► Show #imgview
       ├─► Measure naturalWidth × naturalHeight
       └─► Apply initial fit transform & update HUD / Status Bar
```

---

## 2. Tab Lifecycle & Document Model

### Tab Document State

When `/api/file` returns `image: true`, `openFile` in [`web/src/tabs.js`](../../web/src/tabs.js) creates a lightweight document object in `S.tabs`:

```javascript
const d = {
  path,
  name: path.split('/').pop(),
  lang: 'image',
  total: 0,
  maxCols: 0,
  size: j.size,
  lines: [],
  chunks: new Set(),
  pending: new Set(),
  refining: new Set(),
  scrollTop: 0,
  cur: 1,
  outline: null,
  gen: 0,
  markdown: false,
  isImage: true,
  gutter: null,
  diffMode: null,
  diffAvailable: false,
  diffDismissed: true,
  imageFit: true,        // True if fitted to viewport; false if manually scaled
  imageScale: 1,         // Numeric zoom multiplier (0.05 to 32.0)
  imagePanX: 0,          // Pan offset X in pixels
  imagePanY: 0,          // Pan offset Y in pixels
  imageBg: 'checker',    // 'checker' | 'dark' | 'light'
  imagePixelated: false, // True for pixelated rendering; false for smooth
  imageMeta: null,       // { width: naturalWidth, height: naturalHeight }
};
```

### View Synchronization

Tab transitions are coordinated across four view containers in `#editor`:

| Container | Role | Toggled by |
| :--- | :--- | :--- |
| `#viewport` | Virtualized source code viewer | Shown when tab is source code |
| `#mdview` | Rendered Markdown article | `syncPreview()` (`Alt+M`) |
| `#diffview` | Side-by-side or unified Git diff | `syncDiffView()` (`Mod+D`) |
| `#imgview` | Interactive image canvas & HUD | `syncImageView()` |

When an image tab is active, `syncImageView()` makes `#imgview` visible and hides `#viewport`, `#mdview`, and `#diffview`. When switching away from an image tab, `syncImageView()` hides `#imgview`, allowing `#viewport` or `#mdview` to resume without rebuilding DOM elements.

### Workspace Reindexes

When an agent edit or filesystem event triggers `reloadOpenTabs()`, image tabs are preserved: their file size is updated from the new `/api/file` stat response while preserving zoom scale, pan position, and background mode.

---

## 3. Dedicated Image Viewer (`imageview.js`)

The dedicated image viewer lives in [`web/src/imageview.js`](../../web/src/imageview.js) and operates on `#imgview`:

```html
<div id="imgview" hidden>
  <div id="imgview-viewport">
    <div id="imgview-canvas" class="bg-checker">
      <img id="imgview-img" src="" alt="">
    </div>
  </div>
  <div id="imgview-hud">...</div>
</div>
```

### Zoom Math & Fitting

The image scale is calculated through two primary states:

1. **Fit-to-Window (`imageFit = true`)**:
   $$
   \text{fitScale} = \min\left(1, \frac{\text{viewportWidth} - 64}{\text{naturalWidth}}, \frac{\text{viewportHeight} - 64}{\text{naturalHeight}}\right)
   $$
   If both natural dimensions fit within the viewport without scaling, `currentScale` defaults to $1.0$ (100%) to avoid unneeded upscaling of small icons.
2. **Manual Scale (`imageFit = false`)**:
   Step zoom multiplies or divides `imageScale` by $1.25$, clamped between $0.05$ ($5\%$) and $32.0$ ($3200\%$).

CSS transforms are applied directly to `#imgview-canvas`:
```javascript
canvas.style.transform = `translate(${d.imagePanX}px, ${d.imagePanY}px) scale(${currentScale})`;
```

### Panning

Dragging anywhere on `#imgview-viewport` (with mouse button 0) initiates panning:
- On `mousedown`, the starting pointer $(x_0, y_0)$ and existing pan $(panX_0, panY_0)$ are captured.
- On `mousemove`, the delta $(\Delta x, \Delta y)$ is added to origin offsets. If movement exceeds 3px, `imageFit` is set to `false`.
- The viewport cursor toggles between `grab` and `grabbing`.

### Background Contrast Modes

To evaluate transparency in SVG icons and alpha-masked PNGs, users can cycle the background mode (`b` key or HUD button):

| Mode | Class | Appearance |
| :--- | :--- | :--- |
| **Checkerboard** | `.bg-checker` | Repeating conic gradient (`var(--bg3)` / `var(--bg2)`) |
| **Dark** | `.bg-dark` | Solid `#121214` dark matte |
| **Light** | `.bg-light` | Solid `#ffffff` bright matte |

### Rendering Quality Modes

Users can toggle between bilinear smoothing and nearest-neighbor pixelated rendering (`p` key or HUD button):
- **Smooth**: `image-rendering: auto` (standard for diagrams, photos, and high-res assets).
- **Pixelated**: `image-rendering: pixelated` (crisp pixel art and icon boundary inspection).
- **Smart default**: Images with natural dimensions $\le 64\times64$ px automatically default to pixelated mode on first load.

---

## 4. Markdown Inline Images & Lightbox

Markdown documents containing inline images (`![]()`) are enhanced in [`web/src/markdown.js`](../../web/src/markdown.js):

### Lazy Loading & Decoding

`mdSetImage` injects `loading="lazy"` and `decoding="async"` on all Markdown `<img>` tags. This prevents layout thrashing and avoids downloading offscreen images until scrolled near the viewport.

### Standalone Image Affordance

Standalone Markdown images receive `.md-zoomable`, giving them a subtle hover outline (`var(--accent)`) and a `zoom-in` cursor.

### Interactive Lightbox Overlay

Clicking an inline Markdown image opens the `#img-lightbox` modal overlay:

1. **Backdrop**: Fullscreen semi-transparent dimming (`rgba(0, 0, 0, 0.72)`) with a CSS backdrop blur filter (`blur(8px)`).
2. **Dialog Header**:
   - Displays the image file name or alt text.
   - Displays natural dimensions (`width × height px`).
   - **Open in Tab**: Calls `openFile(path)` to instantly promote the image into a first-class editor tab for full zoom/pan manipulation.
   - **Copy Path / URL**: Copies either the repository-relative path or remote URL to the clipboard.
   - **Close Button**: Dismisses the overlay.
3. **Dismissal Triggers**: Pressing `Escape`, clicking the `✕` button, or clicking anywhere on the dimmed backdrop.

### Broken Image Handling

Broken or missing image links are intercepted using a capture-phase error listener on `#md`:

```javascript
mdArticle.addEventListener('error', e => {
  if (e.target && e.target.localName === 'img') {
    const img = e.target;
    const path = img.dataset.rawPath || img.dataset.origSrc || img.getAttribute('src');
    const fallback = document.createElement('div');
    fallback.className = 'md-img-broken';
    fallback.innerHTML = `<svg ...></svg><span>Image not found: ${esc(path)}</span>`;
    img.replaceWith(fallback);
  }
}, true);
```
This replaces missing images with a clean warning card instead of leaving broken browser icons or causing unexpected layout shifts.

---

## 5. Keyboard & Interaction Reference

When an image tab is active, the following keyboard controls are active:

| Key | Action | Description |
| :--- | :--- | :--- |
| `+` or `=` | Zoom In | Multiplies current scale by $1.25\times$ (up to $3200\%$) |
| `-` or `_` | Zoom Out | Divides current scale by $1.25\times$ (down to $5\%$) |
| `0` | Fit to Window | Fits image inside current viewport bounds |
| `1` | Actual Size (1:1) | Resets scale to $100\%$ ($1.0\times$) |
| `b` or `B` | Cycle Background | Cycles **Checkerboard** $\rightarrow$ **Dark** $\rightarrow$ **Light** |
| `p` or `P` | Toggle Mode | Toggles between **Smooth** and **Pixelated** interpolation |
| `↑` `↓` `←` `→` | Pan Canvas | Shifts view offset by 40 px in corresponding direction |
| Mouse Drag | Pan Canvas | Freeform drag-to-pan (`grab`/`grabbing` cursor) |
| Mouse Wheel | Interactive Zoom | Zooms in/out centered at viewport |
| `Esc` | Dismiss Overlays | Closes Markdown lightbox if open |
| `Alt+W` | Close Tab | Closes active image tab and returns to previous tab |
