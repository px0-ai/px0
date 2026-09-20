# Image Viewer & Asset Inspection

px0 includes a dedicated image viewing canvas and inspection toolset. It treats visual assets as first-class documents alongside source code and Markdown files, providing interactive zoom, pan, background contrast modes, and an inline Markdown lightbox.

---

## Overview & Core Purpose

Modern software repositories increasingly contain visual assets: UI design mockups, architectural diagrams, vector SVG icons, charts, favicons, and generated media assets. Furthermore, AI coding agents frequently generate diagrams or UI screenshots as part of documentation and feature development.

Switching back and forth between a code reader and external image preview tools breaks concentration. px0 provides first-class support for opening image files directly in editor tabs. With precise zoom controls (up to 3200%), transparency background toggles, and pixelation options, developers can inspect graphics, verify icon alignment, and audit visual changes without leaving their browser workspace.

---

## Supported Formats

px0 natively renders and inspects standard web and raster image formats:

- **Vector Graphics**: `.svg`
- **Raster Formats**: `.png`, `.jpg`, `.jpeg`, `.webp`, `.gif`, `.avif`, `.ico`, `.bmp`

---

## Key Capabilities

- **Interactive Canvas Zoom**: Smoothly zoom into small icons or high-resolution architectural diagrams using mouse wheel or keyboard shortcuts (`+` and `-`), with zoom scaling ranging from 5% up to 3200%.
- **Fit-to-Window & 1:1 Actual Size**:
  - Press `0` to automatically fit large images within your browser viewport.
  - Press `1` to view images at their natural 1:1 pixel resolution.
- **Drag-to-Pan Navigation**: Click and drag anywhere across the viewport to pan around zoomed-in diagrams or large images with a natural grab cursor.
- **Background Contrast Modes**: Inspect transparency and alpha channels in PNGs and SVGs by pressing `b` to cycle between three distinct backgrounds:
  - **Checkerboard**: Standard repeating grid showing exact transparency boundaries.
  - **Dark Matte**: Solid `#121214` dark matte for evaluating white/light icons.
  - **Light Matte**: Solid `#ffffff` bright matte for checking dark icons.
- **Rendering Modes (Smooth vs. Pixelated)**: Press `p` to toggle between bilinear smoothing (ideal for photographs and continuous diagrams) and nearest-neighbor pixelated rendering (essential for pixel art, favicons, and auditing SVG crispness). Small icons ($\le 64\times64$ px) automatically default to pixelated mode.
- **Markdown Click-to-Expand Lightbox**: Clicking any inline image within a Markdown preview opens a centered, modal lightbox with a blurred backdrop, natural dimensions, a copy path button, and an option to promote the image into a dedicated editor tab.
- **Broken Image Safeguard**: If an image link in Markdown is missing or broken, px0 gracefully replaces it with a clean warning card instead of leaving broken browser icons or causing layout shifts.

---

## Developer Workflows & Practical Value

### Auditing Vector & Icon Assets
When adding SVG icons or favicons to a project:
1. Open the `.svg` or `.ico` file in px0.
2. Press `+` to zoom up to 800% or 1600%.
3. Press `b` to cycle through the checkerboard, dark, and light backgrounds to verify icon stroke contrast and transparent cutouts.
4. Press `p` to inspect vector edges with nearest-neighbor sharpness.

### Inspecting AI-Generated Diagrams
When a coding agent creates or updates architectural diagrams:
1. Open the diagram directly from the file tree or click it inside the Markdown preview to open the lightbox.
2. Click "Open in Tab" from the lightbox header to promote it to a full tab.
3. Pan and zoom across complex system diagrams to verify relationships and labels.

---

## Keyboard Shortcuts & Controls

| Shortcut | Context | Action |
| :--- | :--- | :--- |
| `+` or `=` | Image Tab | Zoom In ($1.25\times$, up to $3200\%$) |
| `-` or `_` | Image Tab | Zoom Out ($1.25\times$, down to $5\%$) |
| `0` | Image Tab | Fit Image to Window |
| `1` | Image Tab | Reset to Actual Size (1:1 / 100%) |
| `b` or `B` | Image Tab | Cycle Background (Checkerboard $\rightarrow$ Dark $\rightarrow$ Light) |
| `p` or `P` | Image Tab | Toggle Interpolation (Smooth $\leftrightarrow$ Pixelated) |
| `Mouse Drag` | Image Tab | Freeform pan canvas |
| `Mouse Wheel` | Image Tab | Interactive zoom centered on pointer |
| `Arrow Keys` | Image Tab | Nudge canvas position by 40 pixels |
| `Esc` | Lightbox | Dismiss Markdown image lightbox |
| `Alt+W` | Image Tab | Close active image tab |

---

## Technical Architecture Deep Dive

For an explanation of image tab document models, viewport transform matrix calculations, DOM view synchronization, and lazy decoding pipelines, see [Image Viewer Architecture Internals](../internals/image-viewer.md).
