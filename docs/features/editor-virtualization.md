# Virtualized Editor & Memory Scavenger

px0 is built on a custom virtualized DOM rendering engine and an aggressive host memory scavenger. It renders massive source files containing hundreds of thousands of lines in milliseconds, while maintaining an exceptionally small memory footprint (~20–30 MB server RSS, ~100–180 MB total including the browser tab) that automatically reclaims idle memory.

---

## Overview & Core Purpose

Conventional code editors and IDEs (especially those running on Electron or heavy web runtimes) create browser DOM nodes for thousands of lines of text. When opening large source files, database exports, or minified assets, memory usage spikes into gigabytes, scrolling stutters, and the browser tab often freezes or crashes.

px0 approaches rendering with strict mechanical efficiency. Regardless of whether a file has 15 lines or 450,000 lines, px0 mounts only ~60 visible rows in the browser DOM at any given time. As you scroll, offscreen rows are recycled instantaneously, keeping client browser tab RAM bounded at ~80–150 MB. Paired with a background scavenger that invokes Go runtime garbage collection and returns unused pages to the operating system after 15 seconds of inactivity (~20–30 MB host RSS), px0 operates as a lightweight inspection tool that never drains host battery or hoards system RAM.

---

## Key Capabilities

- **DOM Row Recycling (~60 Nodes)**: Only the lines currently visible in your viewport (plus a small overscan buffer) are attached to the browser DOM. Memory and render cost remain entirely constant regardless of file length.
- **Instantaneous File Opening**: Opening a 500,000-line file takes the exact same sub-millisecond duration as opening a 10-line file.
- **Silky 60fps Scrolling**: Offscreen DOM reuse and CSS transforms eliminate layout recalculations and garbage collection pauses during rapid wheel scrolling.
- **Accurate Sub-Pixel Scrollbar**: A proportional scrollbar accurately reflects the total height of the file. Dragging the thumb or clicking the track jumps across tens of thousands of lines smoothly without latency.
- **Active Memory Scavenging**: When px0 sits idle for 15 seconds after an indexing or search operation, the Go backend automatically invokes proactive memory scavenging (`debug.FreeOSMemory()`), returning physical RAM to the host operating system.
- **Non-Destructive Caret & Selection**: Caret positioning, text selection, and inline highlight overlays are maintained seamlessly across virtualized repaints and scrolling.

---

## Developer Workflows & Practical Value

### Auditing Massive Datasets & Logs
When inspecting generated SQL dumps, raw CSV data, compiler outputs, or large protobuf definitions:
- Traditional editors either crash, display a "File too large" warning, or freeze the system.
- In px0, opening a 200 MB, 400,000-line file happens instantly, scrolls smoothly, and consumes practically no excess RAM.

### Running on Constrained Hardware & Laptops
px0 was specifically designed to run comfortably on low-spec virtual machines, cloud devboxes, and battery-powered laptops. Leaving px0 running in the background while compiling or testing will not trigger fan noise, thermal throttling, or battery drain.

---

## Head-to-Head Performance Comparison

| Metric / Parameter | px0 (Local / Remote) | VS Code (Desktop / Remote) | Notes |
| :--- | :--- | :--- | :--- |
| **Host / Server Memory (RSS)** | **~20–30 MB** *(Static Go binary)* | ~500 MB – 1,440 MB | 20–50x leaner on host/server |
| **Client UI Memory** | **~80 – 150 MB** *(Single browser tab)* | ~700 – 1,000 MB *(Bundled Chromium + GPU)* | Bounded by ~60 virtualized DOM rows |
| **Total System RAM** | **~100 – 180 MB** | **1,166 MB – 1,440 MB** | ~85–90% total system reduction |
| **Startup CPU Spike** | **< 1%** | 35% – 50% | Near-instant startup |
| **Startup / Boot Time** | **< 1 ms** | 4 – 10 seconds | Zero Electron boot overhead |
| **Process Count** | **1 single static binary** | 15+ Node.js / Electron processes | Single native binary |
| **50,000-File Indexing** | **< 50 ms** | Multiple seconds of background churn | Parallel goroutines |
| **Idle Memory Release** | **Automatic (after 15s)** | Retained indefinitely | Proactive `debug.FreeOSMemory()` |

> **Accounting for the Browser Tab**: Because px0 serves its UI to a standard web browser rather than embedding Electron, a full local audit includes both the Go server daemon (~20–30 MB) and the active browser tab (~80–150 MB). Even with the tab included, px0 consumes ~100–180 MB total—roughly 90% less RAM than Electron IDEs. On remote servers, cloud devboxes, and containers, the host pays strictly the ~20–30 MB server cost while rendering runs on the client machine.

---

## Technical Architecture Deep Dive

For mathematical details regarding sub-pixel font measurement, offscreen row recycling, decoupled caret rendering, and `debug.FreeOSMemory()` scavenger timing, see [Editor Virtualization & Caret Engine Internals](../internals/editor-virtualization.md) and [System Architecture & Runtime Lifecycle](../internals/architecture.md).
